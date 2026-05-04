package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/plugin"
	"ezyapper/internal/types"
)

// EmotePlugin is the main plugin struct.
type EmotePlugin struct {
	config        Config
	storage       *Storage
	vision        *VisionClient
	emoteLLM      *EmoteLLMClient
	cdnRefresh    *CDNRefreshClient
	discord       *DiscordSession
	sendQueue     map[string]string
	lastChannelID string // set in OnMessage, used by send_emote
	mu            sync.Mutex
}

// Info returns plugin metadata.
func (p *EmotePlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "emote-plugin",
		Version:     "0.0.1",
		Author:      "EZyapper",
		Description: "Auto-steals emotes from images and provides searchable emote library",
		Priority:    10,
	}, nil
}

// OnMessage is called for every Discord message. Records attachment URLs as emote entries.
func (p *EmotePlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	p.mu.Lock()
	p.lastChannelID = msg.ChannelID
	p.mu.Unlock()

	if !p.config.AutoStealEnabled || p.storage == nil || len(msg.ImageURLs) == 0 || msg.IsBot {
		return true, nil
	}

	fmt.Fprintf(os.Stderr, "[EMOTE] OnMessage: %d attachments from channel=%s\n", len(msg.ImageURLs), msg.ChannelID)

	guildID := msg.GuildID
	if guildID == "" {
		guildID = "global"
	}

	for _, url := range msg.ImageURLs {
		if !p.storage.CheckBlacklist(
			guildID, msg.ChannelID, msg.AuthorID,
			p.config.AdditionalBlacklistChannels,
			p.config.AdditionalWhitelistChannels,
			p.config.AdditionalBlacklistUsers,
		) {
			continue
		}

		// Store bare URL (strip Discord query params for Discord CDN)
		bare := url
		if strings.Contains(url, "cdn.discordapp.com") && strings.Contains(url, "?") {
			bare = url[:strings.Index(url, "?")]
		}

		entry := EmoteEntry{
			ID:        md5Hash(bare),
			URL:       bare,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		err := p.storage.AddEmote(guildID, entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[EMOTE] failed to add emote: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "[EMOTE] recorded URL: %s (guild=%s)\n", bare, guildID)
	}

	return true, nil
}

// OnResponse is called after the bot generates a response.
func (p *EmotePlugin) OnResponse(msg types.DiscordMessage, response string) error {
	p.mu.Lock()
	content, ok := p.sendQueue[msg.ChannelID]
	if ok {
		delete(p.sendQueue, msg.ChannelID)
	}
	p.mu.Unlock()

	if !ok {
		return nil
	}

	// Connect Discord if needed (lazy)
	if p.config.DiscordToken != "" && p.discord.session == nil {
		if err := p.discord.Connect(p.config.DiscordToken); err != nil {
			return fmt.Errorf("failed to connect discord: %w", err)
		}
	}

	if strings.HasPrefix(content, "__file__:") {
		fileName := strings.TrimPrefix(content, "__file__:")
		fileName = filepath.Base(fileName)
		filePath := filepath.Join(p.config.DataDir, "global", "images", fileName)
		if err := p.discord.SendFile(msg.ChannelID, filePath); err != nil {
			return fmt.Errorf("failed to send file: %w", err)
		}
	} else {
		if err := p.discord.SendMessage(msg.ChannelID, content); err != nil {
			return fmt.Errorf("failed to send message: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "[EMOTE] OnResponse: sent URL to channel=%s\n", msg.ChannelID)
	return nil
}

// Shutdown is called when the plugin is being stopped.
func (p *EmotePlugin) Shutdown() error {
	if p.discord != nil {
		if err := p.discord.Close(); err != nil {
			return fmt.Errorf("failed to close discord session: %w", err)
		}
	}
	return nil
}

// ListTools returns the tool specs exposed by this plugin.
func (p *EmotePlugin) ListTools() ([]plugin.ToolSpec, error) {
	return []plugin.ToolSpec{
		{
			Name:        "search_emote",
			Description: "Search for emotes by describing what you want",
			TimeoutMs:   p.config.SearchEmoteMs,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "What kind of emote the user wants (describe the emotion/situation)",
					},
					"guild_id": map[string]interface{}{
						"type":        "string",
						"description": "Guild ID to search (searches global + this guild)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "send_emote",
			Description: "Send an emote to the channel, requires the emote ID from search_emote results",
			TimeoutMs:   p.config.SendEmoteMs,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Emote ID (MD5 hash) to send",
					},
					"guild_id": map[string]interface{}{
						"type":        "string",
						"description": "Guild ID context",
					},
				},
				"required": []string{"id"},
			},
		},
	}, nil
}

// ExecuteTool dispatches tool calls by name.
func (p *EmotePlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "search_emote":
		return p.executeSearchEmote(args)
	case "send_emote":
		return p.executeSendEmote(args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (p *EmotePlugin) executeSearchEmote(args map[string]interface{}) (string, error) {
	query, ok, err := getStringArg(args, "query")
	if err != nil {
		return "", err
	}
	if !ok || query == "" {
		return "", fmt.Errorf("query is required")
	}

	guildID, _, err := getStringArg(args, "guild_id")
	if err != nil {
		return "", err
	}
	if guildID == "" {
		guildID = "global"
	}

	// Load emotes from global + guild
	global, err := p.storage.LoadMetadata("global")
	if err != nil {
		return "", fmt.Errorf("failed to load global emotes: %w", err)
	}
	guild, err := p.storage.LoadMetadata(guildID)
	if err != nil {
		return "", fmt.Errorf("failed to load guild emotes: %w", err)
	}

	// Merge and dedup by ID
	allEmotes := global.Emotes
	seen := make(map[string]bool)
	for _, e := range global.Emotes {
		seen[e.ID] = true
	}
	for _, e := range guild.Emotes {
		if !seen[e.ID] {
			allEmotes = append(allEmotes, e)
			seen[e.ID] = true
		}
	}

	fmt.Fprintf(os.Stderr, "[EMOTE] search_emote: query=%q guild=%s emotes=%d\n", query, guildID, len(allEmotes))

	if p.emoteLLM == nil {
		return "", fmt.Errorf("emote LLM not configured")
	}

	matches, err := p.emoteLLM.Match(query, allEmotes)
	if err != nil {
		return "", fmt.Errorf("emote search failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[EMOTE] search_emote: %d matches for %q\n", len(matches), query)

	if len(matches) == 0 {
		return "no matching emotes found", nil
	}

	// Build response with emote details
	type SearchResult struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Reason      string   `json:"reason"`
	}
	results := make([]SearchResult, 0, len(matches))
	for _, m := range matches {
		for _, e := range allEmotes {
			if e.ID == m.ID {
				results = append(results, SearchResult{
					ID: e.ID, Name: e.Name, Description: e.Description,
					Tags: e.Tags, Reason: m.Reason,
				})
				break
			}
		}
	}

	data, err := json.MarshalIndent(map[string]interface{}{"results": results}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal search results: %w", err)
	}
	return string(data), nil
}

func (p *EmotePlugin) executeSendEmote(args map[string]interface{}) (string, error) {
	id, ok, err := getStringArg(args, "id")
	if err != nil {
		return "", err
	}
	if !ok || id == "" {
		return "", fmt.Errorf("id is required")
	}

	guildID, _, err := getStringArg(args, "guild_id")
	if err != nil {
		return "", err
	}
	if guildID == "" {
		guildID = "global"
	}

	// Search metadata for the emote (try guild first, then global)
	var entry *EmoteEntry
	for _, gid := range []string{guildID, "global"} {
		mf, err := p.storage.LoadMetadata(gid)
		if err != nil {
			continue
		}
		for i := range mf.Emotes {
			if mf.Emotes[i].ID == id {
				e := mf.Emotes[i]
				entry = &e
				break
			}
		}
		if entry != nil {
			break
		}
	}
	if entry == nil {
		return "", fmt.Errorf("emote not found: %s", id)
	}

	// Resolve the output: CDN refresh if URL, or local file path
	var sendContent string
	if entry.URL != "" {
		refreshed, err := p.cdnRefresh.RefreshURL(entry.URL)
		if err != nil {
			return "", fmt.Errorf("failed to refresh CDN URL: %w", err)
		}
		sendContent = refreshed
	} else if entry.FileName != "" {
		sendContent = "__file__:" + entry.FileName
	} else {
		return "", fmt.Errorf("emote has neither URL nor file_name: %s", id)
	}

	// Queue for OnResponse
	p.mu.Lock()
	if p.lastChannelID != "" {
		p.sendQueue[p.lastChannelID] = sendContent
	}
	p.mu.Unlock()

	fmt.Fprintf(os.Stderr, "[EMOTE] send_emote: %s -> channel=%s\n", entry.Name, p.lastChannelID)

	return fmt.Sprintf("sent %s", entry.Name), nil
}

// getStringArg extracts a string argument from the args map using comma-ok pattern.
func getStringArg(args map[string]interface{}, key string) (string, bool, error) {
	value, exists := args[key]
	if !exists {
		return "", false, nil
	}
	s, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("argument %s must be a string", key)
	}
	return s, true, nil
}

func newEmotePlugin(cfg Config) (*EmotePlugin, error) {
	dataDir := cfg.DataDir
	if !filepath.IsAbs(dataDir) {
		pluginRoot := pluginRuntimePath()
		dataDir = filepath.Join(pluginRoot, dataDir)
	}

	dataDir, err := filepath.Abs(filepath.Clean(dataDir))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve data dir: %w", err)
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create data dir %s: %w", dataDir, err)
	}

	storage, err := NewStorage(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create storage: %v\n", err)
		os.Exit(1)
	}
	p := &EmotePlugin{
		config:    cfg,
		storage:   storage,
		sendQueue: make(map[string]string),
	}

	if cfg.VisionApiKey != "" {
		p.vision = NewVisionClient(
			cfg.VisionApiKey,
			cfg.VisionApiBaseUrl,
			cfg.VisionModel,
			cfg.VisionPrompt,
			time.Duration(cfg.VisionTimeoutSeconds)*time.Second,
		)
	}

	p.emoteLLM = NewEmoteLLMClient(
		cfg.EmoteModel,
		cfg.EmoteApiKey,
		cfg.EmoteApiBaseURL,
		cfg.EmoteMaxTokens,
		cfg.EmoteTemperature,
		15*time.Second,
	)

	p.cdnRefresh = NewCDNRefreshClient(cfg.DiscordToken)
	p.discord = &DiscordSession{}

	return p, nil
}

func pluginRuntimePath() string {
	if dir := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_PATH")); dir != "" {
		return dir
	}
	return "."
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	p, err := newEmotePlugin(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Failed to initialize plugin: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "[EMOTE] Plugin starting...\n")
	if p.config.LoggingEnabled {
		fmt.Fprintf(os.Stderr, "[EMOTE] Config: data_dir=%s, auto_steal=%v\n", p.config.DataDir, p.config.AutoStealEnabled)
	}

	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Error: %v\n", err)
		os.Exit(1)
	}
}
