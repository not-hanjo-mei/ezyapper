// Package bot provides Discord bot session management
package bot

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/ai/decision"
	"ezyapper/internal/ai/mcp"
	"ezyapper/internal/ai/tools"
	"ezyapper/internal/ai/vision"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
	"ezyapper/internal/ratelimit"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

// MessageProcessingPhase represents the current phase of message processing
type MessageProcessingPhase int

const (
	// PhaseReceived message just received, not yet decided
	PhaseReceived MessageProcessingPhase = iota
	// PhaseDeciding currently deciding whether to respond
	PhaseDeciding
	// PhaseGenerating currently generating AI response
	PhaseGenerating
	// PhaseSending currently sending the response
	PhaseSending

	// Discord platform limits (hardcoded — not user-configurable per Discord API specs)
	discordMessageLimit = 2000
	discordChunkLimit   = 1900
)

// ProcessingMessage represents a message being processed
type ProcessingMessage struct {
	MessageID       string
	ChannelID       string
	AuthorID        string
	Content         string
	Phase           MessageProcessingPhase
	CancelFunc      context.CancelFunc
	OriginalContent string // Store original content before any edits
	EditCount       int    // Track number of edits
	mu              sync.RWMutex
}

// SetPhase updates the processing phase thread-safely
func (pm *ProcessingMessage) SetPhase(phase MessageProcessingPhase) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.Phase = phase
}

// GetPhase returns the current processing phase thread-safely
func (pm *ProcessingMessage) GetPhase() MessageProcessingPhase {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.Phase
}

// UpdateContent updates the message content when edited
func (pm *ProcessingMessage) UpdateContent(content string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.Content = content
	pm.EditCount++
}

// GetOriginalContent returns the original content before any edits
func (pm *ProcessingMessage) GetOriginalContent() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.OriginalContent
}

// GetEditCount returns the number of edits
func (pm *ProcessingMessage) GetEditCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.EditCount
}

// GetContent returns the current content thread-safely
func (pm *ProcessingMessage) GetContent() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.Content
}

// consolidationManager wraps memory.ConsolidationManager with channel-level
// message counter methods used to trigger and schedule batch consolidation.
type consolidationManager interface {
	memory.ConsolidationManager
	IncrementChannelMessageCount(ctx context.Context, channelID string) (int, error)
	ConsumeChannelMessageCount(channelID string, consumed int) int
}

type Bot struct {
	session         *discordgo.Session
	ctx             context.Context
	cancel          context.CancelFunc
	configStore     *atomic.Value // stores *config.Config
	memoryStore     memory.MemoryStore
	profileStore    memory.ProfileStore
	consolidation   consolidationManager
	discordClient   *memory.ShortTermClient
	toolRegistry    *tools.ToolRegistry
	pluginManager   *plugin.Manager
	mcpManager      *mcp.MCPManager
	decisionService *decision.DecisionService
	rateLimiter     *ratelimit.Limiter
	visionDescriber *vision.VisionDescriber
	pluginToolNames map[string]struct{}

	mu               sync.RWMutex
	lastResponseTime map[string]time.Time
	wg               sync.WaitGroup

	channelMessageBuffer   map[string][]*types.DiscordMessage // Channel-level buffer for batch consolidation
	channelBufferMu        sync.RWMutex
	channelConsolidating   map[string]bool
	channelConsolidationMu sync.Mutex

	// historicalImageDescCache stores image descriptions for historical messages
	// fetched from Discord API to avoid repeated vision calls on the same messages.
	historicalImageDescCache   map[string]historicalImageDescCacheEntry
	historicalImageDescCacheMu sync.RWMutex

	// processingMessages tracks messages currently being processed
	processingMessages map[string]*ProcessingMessage
	processingMu       sync.RWMutex
}

// cfg returns the current config snapshot atomically
func (b *Bot) cfg() *config.Config {
	c, ok := b.configStore.Load().(*config.Config)
	if !ok {
		panic("configStore contains non-*config.Config value")
	}
	return c
}

type historicalImageDescCacheEntry struct {
	imageURLsKey string
	descriptions []string
	cachedAt     time.Time
}

// New creates a new Discord bot instance
func New(cfgStore *atomic.Value, memoryStore memory.MemoryStore, profileStore memory.ProfileStore, conMgr consolidationManager, pluginMgr *plugin.Manager) (*Bot, error) {
	cfg := cfgStore.Load().(*config.Config)
	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	// Set intents
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsMessageContent |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsGuildMembers

	// Enable state caching for member search
	session.StateEnabled = true
	session.State.TrackMembers = true

	// Create tool registry
	toolRegistry := tools.NewToolRegistry()

	// Use provided plugin manager
	pluginManager := pluginMgr

	// Create MCP manager
	mcpManager := mcp.NewMCPManager(cfg.MCP.Servers)

	// Create Discord client for short-term context
	discordClient := memory.NewShortTermClient(NewDiscordMessageFetcher(session), cfg.Memory.MaxPaginatedLimit)

	var decisionService *decision.DecisionService
	if cfg.Decision.Enabled {
		var err error
		decisionService, err = decision.NewDecisionService(&cfg.Decision)
		if err != nil {
			return nil, fmt.Errorf("failed to create decision service: %w", err)
		}
	}

	cooldownDuration := time.Duration(cfg.Discord.CooldownSeconds) * time.Second
	limiter := ratelimit.NewLimiter(cfg.Discord.MaxResponsesPerMin, cooldownDuration)

	rootCtx, rootCancel := context.WithCancel(context.Background())
	bot := &Bot{
		session:                  session,
		ctx:                      rootCtx,
		cancel:                   rootCancel,
		configStore:              cfgStore,
		memoryStore:              memoryStore,
		profileStore:             profileStore,
		consolidation:            conMgr,
		discordClient:            discordClient,
		toolRegistry:             toolRegistry,
		pluginManager:            pluginManager,
		mcpManager:               mcpManager,
		decisionService:          decisionService,
		rateLimiter:              limiter,
		visionDescriber:          nil,
		pluginToolNames:          make(map[string]struct{}),
		lastResponseTime:         make(map[string]time.Time),
		channelMessageBuffer:     make(map[string][]*types.DiscordMessage),
		channelConsolidating:     make(map[string]bool),
		historicalImageDescCache: make(map[string]historicalImageDescCacheEntry),
		processingMessages:       make(map[string]*ProcessingMessage),
	}

	// Register Discord tools
	discordTools := tools.NewDiscordTools(session)
	discordTools.RegisterTools(toolRegistry)

	bot.registerPluginTools()

	if err := bot.ApplyRuntimeConfig(); err != nil {
		return nil, fmt.Errorf("failed to apply runtime config: %w", err)
	}

	// Register event handlers
	bot.registerHandlers()

	return bot, nil
}

// Start starts the Discord bot
func (b *Bot) Start(ctx context.Context) error {
	logger.Info("Starting Discord bot...")

	// Open WebSocket connection
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord connection: %w", err)
	}

	logger.Infof("Bot connected as: %s#%s",
		b.session.State.User.Username,
		b.session.State.User.Discriminator)

	// Connect to MCP servers and register tools
	if b.cfg().MCP.Enabled {
		if err := b.mcpManager.Connect(ctx); err != nil {
			logger.Warnf("Failed to connect to MCP servers: %v", err)
		} else {
			b.registerMCPTools(ctx)
		}
	}

	// Load plugins
	if b.cfg().Plugins.Enabled {
		if err := b.pluginManager.LoadPluginsFromDir(b.cfg().Plugins.PluginsDir); err != nil {
			logger.Warnf("Failed to load plugins: %v", err)
		}
		b.registerPluginTools()
	}

	return nil
}

// Stop stops the Discord bot
func (b *Bot) Stop() error {
	logger.Info("Stopping Discord bot...")

	b.cancel()

	// Close MCP connections
	if b.mcpManager != nil {
		if err := b.mcpManager.Close(); err != nil {
			logger.Warnf("Error closing MCP connections: %v", err)
		}
	}

	// Close Discord connection
	if err := b.session.Close(); err != nil {
		return fmt.Errorf("failed to close Discord connection: %w", err)
	}

	return nil
}

// Shutdown cancels the root context and waits for tracked goroutines (e.g. consolidation)
// to drain, respecting the timeout from ctx. Returns an error if the timeout expires.
func (b *Bot) Shutdown(ctx context.Context) error {
	logger.Info("Shutting down bot goroutines...")

	b.cancel()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("All goroutines completed")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown timed out waiting for goroutines")
	}
}

// registerMCPTools fetches and registers MCP tools from connected servers
func (b *Bot) registerMCPTools(ctx context.Context) {
	mcpTools, err := b.mcpManager.GetAllTools(ctx)
	if err != nil {
		logger.Warnf("Failed to get MCP tools: %v", err)
		return
	}

	for _, tool := range mcpTools {
		mcpTool := tool
		b.toolRegistry.Register(&tools.Tool{
			Name:        fmt.Sprintf("%s_%s", mcpTool.ServerName, mcpTool.Tool.Name),
			Description: fmt.Sprintf("[%s] %s", mcpTool.ServerName, mcpTool.Tool.Description),
			Parameters:  mcpTool.Tool.InputSchema,
			Handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
				return b.mcpManager.CallTool(ctx, mcpTool.ServerName, mcpTool.Tool.Name, args)
			},
		})
	}

	logger.Infof("Registered %d MCP tools from external servers", len(mcpTools))
}

// GetSession returns the Discord session
func (b *Bot) GetSession() *discordgo.Session {
	return b.session
}

// GetMemory returns the memory store
func (b *Bot) GetMemory() memory.MemoryStore {
	return b.memoryStore
}

// GetDiscordClient returns the Discord short-term client
func (b *Bot) GetDiscordClient() *memory.ShortTermClient {
	return b.discordClient
}

// GetChannelName returns the name of a Discord channel, or the channelID if not found.
func (b *Bot) GetChannelName(channelID string) string {
	ch, err := b.GetChannel(channelID)
	if err != nil {
		return channelID
	}
	return ch.Name
}

// GetUserName returns the display name of a guild member, or the userID if not found.
func (b *Bot) GetUserName(guildID, userID string) string {
	member, err := b.GetMember(guildID, userID)
	if err != nil {
		return userID
	}
	if member.Nick != "" {
		return member.Nick
	}
	return member.User.Username
}

// GetGuildName returns the name of a guild, or the guildID if not found.
func (b *Bot) GetGuildName(guildID string) string {
	guild, err := b.GetGuild(guildID)
	if err != nil {
		return guildID
	}
	return guild.Name
}

// GetGuild retrieves guild information
func (b *Bot) GetGuild(guildID string) (*discordgo.Guild, error) {
	// Try state cache first
	guild, err := b.session.State.Guild(guildID)
	if err != nil {
		// Fall back to API
		guild, err = b.session.Guild(guildID)
		if err != nil {
			return nil, err
		}
	}
	return guild, nil
}

// GetChannel retrieves channel information
func (b *Bot) GetChannel(channelID string) (*discordgo.Channel, error) {
	// Try state cache first
	channel, err := b.session.State.Channel(channelID)
	if err != nil {
		// Fall back to API
		channel, err = b.session.Channel(channelID)
		if err != nil {
			return nil, err
		}
	}
	return channel, nil
}

// GetMember retrieves guild member information
func (b *Bot) GetMember(guildID, userID string) (*discordgo.Member, error) {
	// Try state cache first
	member, err := b.session.State.Member(guildID, userID)
	if err != nil {
		// Fall back to API
		member, err = b.session.GuildMember(guildID, userID)
		if err != nil {
			return nil, err
		}
	}
	return member, nil
}

// IsBotMentioned checks if the bot is mentioned in a message
func (b *Bot) IsBotMentioned(m *discordgo.MessageCreate) bool {
	// Check direct mention
	for _, mention := range m.Mentions {
		if mention.ID == b.session.State.User.ID {
			return true
		}
	}

	// Check @everyone or @here if bot has permissions
	if m.MentionEveryone {
		return true
	}

	return false
}

// IsChannelAllowed checks if the bot should respond in a channel
func (b *Bot) IsChannelAllowed(channelID string) bool {
	// Check blacklist
	for _, id := range b.cfg().Blacklist.Channels {
		if id == channelID {
			return false
		}
	}

	// Check whitelist (if set)
	if len(b.cfg().Whitelist.Channels) > 0 {
		for _, id := range b.cfg().Whitelist.Channels {
			if id == channelID {
				return true
			}
		}
		return false
	}

	return true
}

// IsUserBlacklisted checks if a user is blacklisted
func (b *Bot) IsUserBlacklisted(userID string) bool {
	for _, id := range b.cfg().Blacklist.Users {
		if id == userID {
			return true
		}
	}
	return false
}

// IsUserWhitelisted checks if a user is whitelisted
func (b *Bot) IsUserWhitelisted(userID string) bool {
	if len(b.cfg().Whitelist.Users) == 0 {
		return true
	}
	for _, id := range b.cfg().Whitelist.Users {
		if id == userID {
			return true
		}
	}
	return false
}

func (b *Bot) CheckRateLimit(channelID, userID string) bool {
	b.rateLimiter.UpdateConfig(
		b.cfg().Discord.MaxResponsesPerMin,
		time.Duration(b.cfg().Discord.CooldownSeconds)*time.Second,
	)
	return b.rateLimiter.Check(channelID, userID)
}

func (b *Bot) SetCooldown(userID string, duration time.Duration) {
	b.rateLimiter.SetCooldown(userID, duration)
}

// FetchUserMessages proxies Discord message retrieval for the Web server.
func (b *Bot) FetchUserMessages(ctx context.Context, channelID string, userID string, limit int) ([]*types.DiscordMessage, error) {
	return b.discordClient.FetchUserMessages(ctx, channelID, userID, limit)
}

// ApplyRuntimeConfig reapplies hot-updateable runtime dependencies after config changes.
func (b *Bot) ApplyRuntimeConfig() error {
	b.rateLimiter.UpdateConfig(
		b.cfg().Discord.MaxResponsesPerMin,
		time.Duration(b.cfg().Discord.CooldownSeconds)*time.Second,
	)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cfg().AI.Vision.Mode != config.VisionModeHybrid {
		b.visionDescriber = nil
		return nil
	}

	visionAIConfig := b.cfg().AI
	if b.cfg().AI.Vision.APIBaseURL != "" {
		visionAIConfig.APIBaseURL = b.cfg().AI.Vision.APIBaseURL
	}
	if b.cfg().AI.Vision.APIKey != "" {
		visionAIConfig.APIKey = b.cfg().AI.Vision.APIKey
	}

	clientWrapper := ai.NewClient(&visionAIConfig, b.toolRegistry)
	b.visionDescriber = vision.NewVisionDescriber(clientWrapper, &b.cfg().AI.Vision, &visionAIConfig)
	return nil
}

func (b *Bot) getVisionDescriber() *vision.VisionDescriber {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.visionDescriber
}

// RefreshPluginTools reloads plugin tool registrations into the AI tool registry.
func (b *Bot) RefreshPluginTools() {
	b.registerPluginTools()
}

func sanitizeToolNameToken(name string) string {
	if name == "" {
		return "plugin"
	}

	var out []byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}

	if len(out) > 0 && out[0] >= '0' && out[0] <= '9' {
		out = append([]byte("p_"), out...)
	}

	return string(out)
}

func pluginToolBaseName(toolName string) string {
	return sanitizeToolNameToken(toolName)
}

func pluginToolScopedName(pluginName, toolName string) string {
	return fmt.Sprintf("%s_%s", sanitizeToolNameToken(pluginName), sanitizeToolNameToken(toolName))
}

func (b *Bot) registerPluginTools() {
	if b.pluginManager == nil || b.toolRegistry == nil {
		return
	}

	pluginTools := b.pluginManager.ListTools()
	nameCount := make(map[string]int, len(pluginTools))
	for _, tool := range pluginTools {
		baseName := pluginToolBaseName(tool.Spec.Name)
		nameCount[baseName]++
	}

	next := make(map[string]struct{}, len(pluginTools))
	for _, tool := range pluginTools {
		baseName := pluginToolBaseName(tool.Spec.Name)
		fullName := baseName
		if nameCount[baseName] > 1 {
			fullName = pluginToolScopedName(tool.PluginName, tool.Spec.Name)
		}
		next[fullName] = struct{}{}

		pluginName := tool.PluginName
		toolName := tool.Spec.Name
		parameters := tool.Spec.Parameters
		if parameters == nil {
			parameters = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		description := tool.Spec.Description
		if description == "" {
			description = "Plugin tool"
		}

		b.toolRegistry.Register(&tools.Tool{
			Name:        fullName,
			Description: fmt.Sprintf("[plugin:%s] %s", pluginName, description),
			Parameters:  parameters,
			Handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
				return b.pluginManager.ExecuteTool(pluginName, toolName, args)
			},
		})
	}

	b.mu.Lock()
	previous := b.pluginToolNames
	b.pluginToolNames = next
	b.mu.Unlock()

	for oldName := range previous {
		if _, exists := next[oldName]; !exists {
			b.toolRegistry.Unregister(oldName)
		}
	}
}

// ShouldRespond determines if the bot should respond to a message
// The context is used to cancel the LLM decision if the message is edited
func (b *Bot) ShouldRespond(ctx context.Context, m *discordgo.MessageCreate, recentMessages []*types.DiscordMessage) (bool, string) {
	// Ignore bot's own messages
	if m.Author.ID == b.session.State.User.ID {
		return false, "own message"
	}

	// Ignore other bot messages if not configured to reply
	if m.Author.Bot && !b.cfg().Discord.ReplyToBots {
		return false, "bot message"
	}

	// Check if user is blacklisted
	if b.IsUserBlacklisted(m.Author.ID) {
		return false, "user blacklisted"
	}

	// Check if channel is allowed
	if !b.IsChannelAllowed(m.ChannelID) {
		return false, "channel not allowed"
	}

	// Check rate limit
	if !b.CheckRateLimit(m.ChannelID, m.Author.ID) {
		return false, "rate limited"
	}

	// Always respond if mentioned
	if b.IsBotMentioned(m) {
		return true, "mentioned"
	}

	// Check if replying to bot's message
	if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil && m.ReferencedMessage.Author.ID == b.session.State.User.ID {
		return true, "reply to bot"
	}

	// Use LLM decision if enabled
	if b.decisionService != nil && b.cfg().Decision.Enabled {
		// Create a timeout context that accommodates all retries plus buffer
		// Total timeout = (timeout per attempt) * (retry_count + 2) to allow for all retries plus overhead
		totalTimeout := time.Duration(b.cfg().Decision.Timeout) * time.Second * time.Duration(b.cfg().Decision.RetryCount+2)
		decisionCtx, cancel := context.WithTimeout(ctx, totalTimeout)
		defer cancel()

		imageCount := len(extractImageURLs(m.Message))
		decisionMessages := b.getRecentMessagesForDecision(m.ID, recentMessages)

		// Build message info with author and reply metadata
		msgInfo := decision.MessageInfo{
			AuthorName: m.Author.Username,
			AuthorID:   m.Author.ID,
			Content:    m.Content,
		}
		if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
			msgInfo.ReplyTo = m.ReferencedMessage.Author.Username
			msgInfo.ReplyToID = m.ReferencedMessage.Author.ID
		}

		result, err := b.decisionService.ShouldRespondWithInfo(decisionCtx, b.cfg().Discord.BotName, msgInfo, imageCount, decisionMessages)
		if err != nil {
			logger.Warnf("decision service failed: %v, using fallback", err)
			return b.ShouldRandomReply(), "llm decision failed, fallback"
		}

		if result.ShouldRespond {
			return true, fmt.Sprintf("llm decision: %s (confidence: %.2f)", result.Reason, result.Confidence)
		}
		return false, fmt.Sprintf("llm decision: %s", result.Reason)
	}

	// Fallback to random reply
	if !b.ShouldRandomReply() {
		return false, "probability check failed"
	}

	return true, "random engagement"
}

func (b *Bot) getRecentMessagesForDecision(currentMessageID string, messages []*types.DiscordMessage) []decision.ContextMessage {
	var result []decision.ContextMessage
	for _, msg := range messages {
		if msg.ID == currentMessageID {
			continue
		}
		result = append(result, decision.ContextMessage{
			AuthorName: msg.Username,
			AuthorID:   msg.AuthorID,
			Content:    msg.Content,
			IsBot:      msg.IsBot,
			Timestamp:  msg.Timestamp,
		})
	}
	return result
}

// ShouldRandomReply determines if bot should reply randomly using crypto/rand
func (b *Bot) ShouldRandomReply() bool {
	if b.cfg().Discord.ReplyPercentage <= 0 {
		return false
	}

	// Use crypto/rand for proper randomness
	n, err := crand.Int(crand.Reader, big.NewInt(100))
	if err != nil {
		logger.Warnf("failed to generate random number: %v", err)
		return false
	}

	return float64(n.Int64()) < b.cfg().Discord.ReplyPercentage*100
}

func (b *Bot) CleanupCache() {
	b.rateLimiter.Cleanup()
	b.historicalImageDescCacheMu.Lock()
	b.pruneHistoricalImageDescCacheLocked()
	b.historicalImageDescCacheMu.Unlock()
}

// registerProcessingMessage registers a new message being processed
func (b *Bot) registerProcessingMessage(messageID, channelID, authorID, content string) *ProcessingMessage {
	b.processingMu.Lock()
	defer b.processingMu.Unlock()

	pm := &ProcessingMessage{
		MessageID:       messageID,
		ChannelID:       channelID,
		AuthorID:        authorID,
		Content:         content,
		OriginalContent: content, // Store original content
		EditCount:       0,
		Phase:           PhaseReceived,
	}
	b.processingMessages[messageID] = pm
	return pm
}

// getProcessingMessage retrieves a processing message by ID
func (b *Bot) getProcessingMessage(messageID string) *ProcessingMessage {
	b.processingMu.RLock()
	defer b.processingMu.RUnlock()
	return b.processingMessages[messageID]
}

// removeProcessingMessage removes a message from processing tracking
func (b *Bot) removeProcessingMessage(messageID string) {
	b.processingMu.Lock()
	defer b.processingMu.Unlock()
	delete(b.processingMessages, messageID)
}

func (b *Bot) clearProcessingMessage(pm *ProcessingMessage, messageID string) {
	if pm != nil {
		b.removeProcessingMessage(messageID)
	}
}

// handleEditedMessage handles a message edit event based on current processing phase
// Returns true if should re-process the message, false otherwise
func (b *Bot) handleEditedMessage(pm *ProcessingMessage, newContent string) bool {
	if pm == nil {
		return false
	}

	phase := pm.GetPhase()

	switch phase {
	case PhaseReceived:
		// Message edited before we started processing - just update context
		logger.Debugf("[edit] Message %s edited in PhaseReceived, updating context only", pm.MessageID)
		pm.UpdateContent(newContent)
		return false

	case PhaseDeciding:
		// Message edited while deciding - cancel and re-decide
		logger.Infof("[edit] Message %s edited in PhaseDeciding, cancelling and re-deciding", pm.MessageID)
		if pm.CancelFunc != nil {
			pm.CancelFunc()
		}
		pm.UpdateContent(newContent)
		return true

	case PhaseGenerating:
		// Message edited while generating - cancel generation and re-decide
		logger.Infof("[edit] Message %s edited in PhaseGenerating, cancelling generation and re-deciding", pm.MessageID)
		if pm.CancelFunc != nil {
			pm.CancelFunc()
		}
		pm.UpdateContent(newContent)
		return true

	case PhaseSending:
		// Message edited while sending - too late to cancel send, but cancel follow-up work
		logger.Debugf("[edit] Message %s edited in PhaseSending, too late to cancel", pm.MessageID)
		if pm.CancelFunc != nil {
			pm.CancelFunc()
		}
		pm.UpdateContent(newContent)
		return false

	default:
		return false
	}
}

// Channel-level buffer methods for batch consolidation

func (b *Bot) addMessageToChannelBuffer(channelID string, msg *types.DiscordMessage) {
	if msg == nil {
		return
	}

	b.channelBufferMu.Lock()
	defer b.channelBufferMu.Unlock()

	existing := b.channelMessageBuffer[channelID]
	for i := len(existing) - 1; i >= 0; i-- {
		if existing[i] != nil && existing[i].ID == msg.ID {
			return
		}
	}

	b.channelMessageBuffer[channelID] = append(b.channelMessageBuffer[channelID], msg)
	logger.Debugf("[channel_buffer] added message for channel=%s, buffer_size=%d", channelID, len(b.channelMessageBuffer[channelID]))

	maxBuffer := b.cfg().Memory.ConsolidationInterval * 2
	if len(b.channelMessageBuffer[channelID]) > maxBuffer {
		b.channelMessageBuffer[channelID] = b.channelMessageBuffer[channelID][len(b.channelMessageBuffer[channelID])-maxBuffer:]
		logger.Debugf("[channel_buffer] truncated buffer for channel=%s to %d messages", channelID, maxBuffer)
	}
}

func (b *Bot) getAndClearChannelMessageBuffer(channelID string) []*types.DiscordMessage {
	b.channelBufferMu.Lock()
	defer b.channelBufferMu.Unlock()

	messages := b.channelMessageBuffer[channelID]
	delete(b.channelMessageBuffer, channelID)
	return messages
}

func (b *Bot) tryStartChannelConsolidation(channelID string) bool {
	b.channelConsolidationMu.Lock()
	defer b.channelConsolidationMu.Unlock()

	if b.channelConsolidating[channelID] {
		return false
	}

	b.channelConsolidating[channelID] = true
	return true
}

func (b *Bot) finishChannelConsolidation(channelID string) {
	b.channelConsolidationMu.Lock()
	defer b.channelConsolidationMu.Unlock()
	delete(b.channelConsolidating, channelID)
}

func (b *Bot) getHistoricalImageDescriptions(messageID string, imageURLs []string) ([]string, bool) {
	if messageID == "" || len(imageURLs) == 0 {
		return nil, false
	}

	key := strings.Join(imageURLs, "\n")

	b.historicalImageDescCacheMu.RLock()
	entry, ok := b.historicalImageDescCache[messageID]
	b.historicalImageDescCacheMu.RUnlock()
	if !ok {
		return nil, false
	}

	cacheTTL := time.Duration(b.cfg().Discord.ImageCacheTTLMin) * time.Minute
	if entry.imageURLsKey != key || time.Since(entry.cachedAt) > cacheTTL || len(entry.descriptions) == 0 {
		b.historicalImageDescCacheMu.Lock()
		if latest, exists := b.historicalImageDescCache[messageID]; exists {
			if latest.imageURLsKey != key || time.Since(latest.cachedAt) > cacheTTL || len(latest.descriptions) == 0 {
				delete(b.historicalImageDescCache, messageID)
			}
		}
		b.historicalImageDescCacheMu.Unlock()
		return nil, false
	}

	descriptions := make([]string, len(entry.descriptions))
	copy(descriptions, entry.descriptions)
	return descriptions, true
}

func (b *Bot) setHistoricalImageDescriptions(messageID string, imageURLs, descriptions []string) {
	if messageID == "" || len(imageURLs) == 0 || len(descriptions) == 0 {
		return
	}

	key := strings.Join(imageURLs, "\n")
	copied := make([]string, len(descriptions))
	copy(copied, descriptions)

	b.historicalImageDescCacheMu.Lock()
	b.historicalImageDescCache[messageID] = historicalImageDescCacheEntry{
		imageURLsKey: key,
		descriptions: copied,
		cachedAt:     time.Now(),
	}
	b.pruneHistoricalImageDescCacheLocked()
	b.historicalImageDescCacheMu.Unlock()
}

func (b *Bot) pruneHistoricalImageDescCacheLocked() {
	now := time.Now()
	cacheTTL := time.Duration(b.cfg().Discord.ImageCacheTTLMin) * time.Minute
	maxEntries := b.cfg().Discord.ImageCacheMaxEntries

	for messageID, entry := range b.historicalImageDescCache {
		if now.Sub(entry.cachedAt) > cacheTTL {
			delete(b.historicalImageDescCache, messageID)
		}
	}

	if len(b.historicalImageDescCache) <= maxEntries {
		return
	}

	for len(b.historicalImageDescCache) > maxEntries {
		var oldestMessageID string
		var oldestTime time.Time
		first := true

		for messageID, entry := range b.historicalImageDescCache {
			if first || entry.cachedAt.Before(oldestTime) {
				first = false
				oldestTime = entry.cachedAt
				oldestMessageID = messageID
			}
		}

		if oldestMessageID == "" {
			break
		}

		delete(b.historicalImageDescCache, oldestMessageID)
	}
}
