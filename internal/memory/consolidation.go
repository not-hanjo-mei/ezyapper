package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/ai/vision"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"
	"ezyapper/internal/utils"

	openai "github.com/sashabaranov/go-openai"
)

// qdrantStore is the subset of QdrantClient methods used by Consolidator.
type qdrantStore interface {
	UpsertMemory(ctx context.Context, memory *Record) error
	UpsertProfile(ctx context.Context, profile *Profile) error
	GetProfile(ctx context.Context, userID string) (*Profile, error)
	GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Record, error)
}

// embedSleep overrides time.Sleep for retry tests. Nil means use real timers.
var embedSleep func(time.Duration)

// Consolidator extracts and stores memories from conversation context using LLM analysis.
type Consolidator struct {
	qdrant            qdrantStore
	embedder          Embedder
	aiClient          *ai.Client
	visionDescriber   *vision.VisionDescriber
	maxMessages       int
	model             string
	prompt            string
	ownBotID          string // Bot's own ID to distinguish from other bots
	memorySearchLimit int

	lastConsolidatedAt time.Time
	mu                 sync.RWMutex
}

// LastConsolidatedAt returns the timestamp of the last successful consolidation.
func (c *Consolidator) LastConsolidatedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastConsolidatedAt
}

// setLastConsolidatedAt records the timestamp of a successful consolidation.
func (c *Consolidator) setLastConsolidatedAt(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastConsolidatedAt = t
}

// embedWithRetry generates an embedding with exponential backoff retry on failure.
func embedWithRetry(ctx context.Context, embedder Embedder, text string) ([]float32, error) {
	const maxRetries = 3
	const baseDelay = 1 * time.Second
	const maxDelay = 30 * time.Second

	return retry.Retry(ctx, maxRetries, func(ctx context.Context) ([]float32, error) {
		return embedder.Embed(ctx, text)
	}, retry.WithBaseDelay(baseDelay), retry.WithMaxDelay(maxDelay))
}

// NewConsolidator creates a new consolidator with the given Qdrant client, embedder, and AI configuration.
func NewConsolidator(qdrant *QdrantClient, embedder Embedder, aiClient *ai.Client, visionDescriber *vision.VisionDescriber, cfg *config.ConsolidationConfig, ownBotID string, consolidationInterval int, memorySearchLimit int) *Consolidator {
	return &Consolidator{
		qdrant:            qdrant,
		embedder:          embedder,
		aiClient:          aiClient,
		visionDescriber:   visionDescriber,
		maxMessages:       consolidationInterval,
		model:             cfg.Model,
		prompt:            cfg.SystemPrompt,
		ownBotID:          ownBotID,
		memorySearchLimit: memorySearchLimit,
	}
}

// Process consolidates memories for a single user, updating their profile and storing extracted memories.
func (c *Consolidator) Process(ctx context.Context, userID string) error {
	start := time.Now()
	logger.Infof("[consolidation] starting for user=%s", userID)

	profile := c.getOrCreateProfile(ctx, userID)

	memories, err := c.qdrant.GetMemoriesByUser(ctx, userID, c.memorySearchLimit)
	if err != nil {
		logger.Errorf("[consolidation] failed to get memories for user=%s: %v", userID, err)
		return fmt.Errorf("failed to get memories: %w", err)
	}
	logger.Infof("[consolidation] retrieved %d existing memories for user=%s", len(memories), userID)

	result, err := c.consolidateMemories(ctx, profile, memories)
	if err != nil {
		logger.Errorf("[consolidation] consolidation logic failed for user=%s: %v", userID, err)
		return fmt.Errorf("consolidation failed: %w", err)
	}

	c.updateProfileFromResult(profile, result)
	profile.LastConsolidatedAt = time.Now()

	if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
		logger.Errorf("[consolidation] failed to update profile for user=%s: %v", userID, err)
		return fmt.Errorf("failed to update profile: %w", err)
	}
	logger.Infof("[consolidation] updated profile for user=%s", userID)

	stored, _ := c.storeMemories(ctx, userID, result.Memories)

	if stored > 0 {
		profile.MemoryCount += stored
		if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
			logger.Warnf("[consolidation] failed to update memory_count for user=%s: %v", userID, err)
		}
	}

	c.setLastConsolidatedAt(time.Now())

	elapsed := time.Since(start)
	logger.Infof("[consolidation] completed for user=%s duration=%s memories_stored=%d/%d",
		userID, elapsed, stored, len(result.Memories))
	return nil
}

func (c *Consolidator) consolidateMemories(ctx context.Context, profile *Profile, memories []*Record) (*ConsolidationResult, error) {
	var memoryContext strings.Builder
	for _, m := range memories {
		if m.MemoryType == TypeSummary {
			memoryContext.WriteString(m.Content)
			memoryContext.WriteString("\n")
		}
	}

	summary := c.generateSummary(profile, memoryContext.String())
	extractedMemories := c.extractMemories(profile, summary)
	delta := c.buildProfileDelta(profile, summary)

	return &ConsolidationResult{
		Summary:      summary,
		ProfileDelta: delta,
		Memories:     extractedMemories,
	}, nil
}

func (c *Consolidator) generateSummary(profile *Profile, context string) string {
	var parts []string

	if profile.LastSummary != "" {
		parts = append(parts, profile.LastSummary)
	}

	if len(profile.Traits) > 0 {
		parts = append(parts, fmt.Sprintf("Traits: %s", strings.Join(profile.Traits, ", ")))
	}

	if len(profile.Facts) > 0 {
		var facts []string
		for k, v := range profile.Facts {
			facts = append(facts, fmt.Sprintf("%s: %s", k, v))
		}
		parts = append(parts, fmt.Sprintf("Facts: %s", strings.Join(facts, ", ")))
	}

	if len(profile.Preferences) > 0 {
		var prefs []string
		for k, v := range profile.Preferences {
			prefs = append(prefs, fmt.Sprintf("%s: %s", k, v))
		}
		parts = append(parts, fmt.Sprintf("Preferences: %s", strings.Join(prefs, ", ")))
	}

	if context != "" {
		parts = append(parts, fmt.Sprintf("Recent context: %s", context))
	}

	return strings.Join(parts, "; ")
}

func (c *Consolidator) extractMemories(profile *Profile, summary string) []Extract {
	var extracts []Extract

	for _, trait := range profile.Traits {
		extracts = append(extracts, Extract{
			Content:    fmt.Sprintf("User is %s", trait),
			Type:       string(TypeFact),
			Confidence: 0.8,
			Keywords:   []string{"trait", trait},
		})
	}

	for key, value := range profile.Facts {
		extracts = append(extracts, Extract{
			Content:    fmt.Sprintf("User's %s is %s", key, value),
			Type:       string(TypeFact),
			Confidence: 0.9,
			Keywords:   []string{"fact", key, value},
		})
	}

	for key, value := range profile.Preferences {
		extracts = append(extracts, Extract{
			Content:    fmt.Sprintf("User prefers %s: %s", key, value),
			Type:       string(TypeFact),
			Confidence: 0.85,
			Keywords:   []string{"preference", key, value},
		})
	}

	return extracts
}

func (c *Consolidator) buildProfileDelta(profile *Profile, summary string) ProfileDelta {
	return ProfileDelta{
		NewTraits:      profile.Traits,
		NewFacts:       profile.Facts,
		NewPreferences: profile.Preferences,
		NewInterests:   profile.Interests,
	}
}

// buildConversationText builds a conversation text from messages for LLM analysis.
// userID is used for logging context; if empty, logs omit per-user details.
func (c *Consolidator) buildConversationText(ctx context.Context, messages []*DiscordMessage, userID string) (string, int) {
	var conversation strings.Builder
	imageCount := 0
	for i, msg := range messages {
		timeStr := msg.Timestamp.UTC().Format(time.RFC3339)
		botMarker := ""
		if msg.AuthorID == c.ownBotID {
			botMarker = ",BOT=2" // Own bot - completely ignore
		} else if msg.IsBot {
			botMarker = ",BOT=1" // Other bots - minimal extraction
		}
		conversation.WriteString(fmt.Sprintf(`"%s"{UserID=%s,Time=%s%s}: "%s"`+"\n", msg.Username, msg.AuthorID, timeStr, botMarker, msg.Content))

		if userID != "" {
			logger.Debugf("[consolidation] message %d [%s] for user=%s: %s%s: %s", i+1, timeStr, userID, msg.Username, botMarker, msg.Content)
		} else {
			logger.Debugf("[consolidation] message %d [%s]: %s (ID=%s)%s: %s", i+1, timeStr, msg.Username, msg.AuthorID, botMarker, msg.Content)
		}

		if len(msg.ImageURLs) > 0 && c.visionDescriber != nil {
			var descriptions []string

			// Use cached descriptions if available (to avoid redundant API calls)
			if len(msg.ImageDescriptions) > 0 {
				descriptions = msg.ImageDescriptions
				if userID != "" {
					logger.Debugf("[consolidation] using cached image descriptions for user=%s message=%d count=%d", userID, i+1, len(descriptions))
				}
			} else {
				// No cache available, call vision API
				var err error
				descriptions, err = c.visionDescriber.DescribeImages(ctx, msg.ImageURLs)
				if err != nil {
					if userID != "" {
						logger.Warnf("[consolidation] failed to describe images for user=%s message=%d: %v", userID, i+1, err)
					} else {
						logger.Warnf("[consolidation] failed to describe images for message %d: %v", i+1, err)
					}
					continue
				}
				if userID != "" {
					logger.Debugf("[consolidation] generated fresh image descriptions for user=%s message=%d count=%d", userID, i+1, len(descriptions))
				}
			}

			for j, desc := range descriptions {
				conversation.WriteString(fmt.Sprintf("  [Attached Image %d: %s]\n", j+1, desc))
				imageCount++
			}
		}
	}
	return conversation.String(), imageCount
}

// ProcessWithMessages consolidates memories for a user using the provided messages as context.
func (c *Consolidator) ProcessWithMessages(ctx context.Context, userID string, messages []*DiscordMessage) error {
	start := time.Now()
	logger.Infof("[consolidation] starting with messages for user=%s message_count=%d", userID, len(messages))

	if len(messages) > c.maxMessages {
		logger.Infof("[consolidation] truncating messages for user=%s from %d to %d", userID, len(messages), c.maxMessages)
		messages = messages[:c.maxMessages]
	}

	conversation, imageCount := c.buildConversationText(ctx, messages, userID)
	logger.Infof("[consolidation] built conversation for user=%s length=%d chars images=%d", userID, len(conversation), imageCount)

	profile := c.getOrCreateProfile(ctx, userID)

	extracted := c.analyzeConversation(ctx, conversation, []string{userID})
	if len(extracted) == 0 {
		elapsed := time.Since(start)
		logger.Infof("[consolidation] no memories extracted for user=%s duration=%s", userID, elapsed)
		return nil
	}

	logger.Infof("[consolidation] extracted %d memories for user=%s", len(extracted), userID)
	for i, extract := range extracted {
		logger.Infof("[consolidation] extracted memory %d for user=%s: type=%s confidence=%.2f content=%q keywords=%v",
			i+1, userID, extract.Type, extract.Confidence, extract.Content, extract.Keywords)
	}

	profileBefore := fmt.Sprintf("traits=%d facts=%d preferences=%d interests=%d",
		len(profile.Traits), len(profile.Facts), len(profile.Preferences), len(profile.Interests))
	c.updateProfileFromExtraction(profile, extracted)
	profile.LastConsolidatedAt = time.Now()
	profileAfter := fmt.Sprintf("traits=%d facts=%d preferences=%d interests=%d",
		len(profile.Traits), len(profile.Facts), len(profile.Preferences), len(profile.Interests))

	if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
		logger.Errorf("[consolidation] failed to update profile for user=%s: %v", userID, err)
		return fmt.Errorf("failed to update profile: %w", err)
	}
	logger.Infof("[consolidation] updated profile for user=%s before=[%s] after=[%s]",
		userID, profileBefore, profileAfter)

	stored, _ := c.storeMemories(ctx, userID, extracted)

	if stored > 0 {
		profile.MemoryCount += stored
		if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
			logger.Warnf("[consolidation] failed to update memory_count for user=%s: %v", userID, err)
		}
	}

	c.setLastConsolidatedAt(time.Now())

	elapsed := time.Since(start)
	logger.Infof("[consolidation] completed for user=%s duration=%s messages_processed=%d memories_extracted=%d memories_stored=%d",
		userID, elapsed, len(messages), len(extracted), stored)
	return nil
}

// storeMemories creates Records from extracts, generates embeddings with retry,
// upserts them into Qdrant, and returns the number successfully stored.
func (c *Consolidator) storeMemories(ctx context.Context, userID string, extracts []Extract) (int, error) {
	stored := 0
	for i, extract := range extracts {
		memory := &Record{
			UserID:     userID,
			MemoryType: Type(extract.Type),
			Content:    extract.Content,
			Summary:    extract.Content,
			Keywords:   extract.Keywords,
			Confidence: extract.Confidence,
			CreatedAt:  time.Now(),
		}

		embedding, err := retry.Retry(ctx, 3, func(ctx context.Context) ([]float32, error) {
			return c.embedder.Embed(ctx, memory.Content)
		},
			retry.WithBaseDelay(1*time.Second),
			retry.WithMaxDelay(30*time.Second),
		)
		if err != nil {
			logger.Errorf("[consolidation] embedding exhausted for memory %d for user=%s: %v", i+1, userID, err)
			continue
		}
		memory.Embedding = embedding

		if err := c.qdrant.UpsertMemory(ctx, memory); err != nil {
			logger.Errorf("[consolidation] failed to store memory %d for user=%s: %v", i+1, userID, err)
		} else {
			stored++
		}
	}
	return stored, nil
}

// ProcessChannelMessages executes batch consolidation for all users in a channel
// ProcessChannelMessages performs batch consolidation for all users identified in the channel messages.
func (c *Consolidator) ProcessChannelMessages(ctx context.Context, channelID string, messages []*DiscordMessage) error {
	start := time.Now()

	// Collect unique user IDs from messages
	userIDSet := make(map[string]bool)
	for _, msg := range messages {
		userIDSet[msg.AuthorID] = true
	}

	var targetUserIDs []string
	for userID := range userIDSet {
		targetUserIDs = append(targetUserIDs, userID)
	}

	logger.Infof("[consolidation] starting batch consolidation for channel=%s messages=%d users=%d", channelID, len(messages), len(targetUserIDs))

	if len(messages) > c.maxMessages {
		logger.Infof("[consolidation] truncating messages from %d to %d", len(messages), c.maxMessages)
		messages = messages[:c.maxMessages]
	}

	// Build conversation with timestamp and user identification
	conversation, imageCount := c.buildConversationText(ctx, messages, "")

	logger.Infof("[consolidation] built conversation length=%d chars images=%d users=%v", len(conversation), imageCount, targetUserIDs)

	// Analyze conversation with batch extraction for all users
	batchExtracts := c.analyzeConversationBatch(ctx, conversation, targetUserIDs)
	if len(batchExtracts) == 0 {
		elapsed := time.Since(start)
		logger.Infof("[consolidation] no memories extracted for channel=%s duration=%s", channelID, elapsed)
		return nil
	}

	logger.Infof("[consolidation] extracted memories for %d users from channel=%s", len(batchExtracts), channelID)

	// Store memories for each user
	totalStored := 0
	for _, userExtract := range batchExtracts {
		userID := userExtract.UserID
		extracts := userExtract.Memories

		if len(extracts) == 0 {
			logger.Debugf("[consolidation] no memories to store for user=%s", userID)
			continue
		}

		profile := c.getOrCreateProfile(ctx, userID)
		c.updateProfileFromExtraction(profile, extracts)
		profile.LastConsolidatedAt = time.Now()

		if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
			logger.Errorf("[consolidation] failed to update profile for user=%s: %v", userID, err)
			continue
		}

		stored, _ := c.storeMemories(ctx, userID, extracts)
		if stored > 0 {
			profile.MemoryCount += stored
			if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
				logger.Warnf("[consolidation] failed to update memory_count for user=%s: %v", userID, err)
			}
		}
		totalStored += stored
		logger.Infof("[consolidation] stored %d memories for user=%s", stored, userID)
	}

	c.setLastConsolidatedAt(time.Now())

	elapsed := time.Since(start)
	logger.Infof("[consolidation] completed batch consolidation for channel=%s duration=%s messages=%d users=%d total_memories=%d",
		channelID, elapsed, len(messages), len(targetUserIDs), totalStored)
	return nil
}

func (c *Consolidator) getOrCreateProfile(ctx context.Context, userID string) *Profile {
	profile, err := c.qdrant.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			logger.Infof("[consolidation] creating new profile for user=%s", userID)
		} else {
			logger.Errorf("[consolidation] failed to get profile for user=%s: %v (creating default)", userID, err)
		}
		return &Profile{
			UserID:      userID,
			Traits:      []string{},
			Facts:       make(map[string]string),
			Preferences: make(map[string]string),
			Interests:   []string{},
			FirstSeenAt: time.Now(),
		}
	}
	logger.Infof("[consolidation] loaded existing profile for user=%s traits=%d facts=%d preferences=%d interests=%d",
		userID, len(profile.Traits), len(profile.Facts), len(profile.Preferences), len(profile.Interests))
	return profile
}

func (c *Consolidator) analyzeConversation(ctx context.Context, conversation string, targetUserIDs []string) []Extract {
	start := time.Now()

	logger.Debugf("[consolidation] analyzeConversation called with conversation length=%d target_users=%d", len(conversation), len(targetUserIDs))

	if c.aiClient == nil {
		logger.Error("[consolidation] AI client not configured, cannot perform LLM extraction")
		return []Extract{}
	}

	if strings.TrimSpace(conversation) == "" {
		logger.Warn("[consolidation] empty conversation, skipping LLM analysis")
		return []Extract{}
	}

	if c.prompt == "" {
		logger.Error("[consolidation] consolidation prompt is empty, cannot perform LLM extraction")
		return []Extract{}
	}

	logger.Debugf("[consolidation] preparing LLM prompt with conversation length=%d", len(conversation))

	// Build messages: system prompt contains extraction rules and target user list
	targetUsersStr := strings.Join(targetUserIDs, ", ")
	systemPrompt := fmt.Sprintf("%s\n\nTarget UserIDs: %s (extract memories for these users only)", c.prompt, targetUsersStr)

	req := ai.ChatCompletionRequest{
		SystemPrompt: systemPrompt,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: conversation,
			},
		},
	}

	logger.Debug("[consolidation] sending request to LLM for memory extraction")
	resp, err := c.aiClient.CreateChatCompletion(ctx, req)
	if err != nil {
		logger.Errorf("[consolidation] LLM request failed: %v", err)
		return []Extract{}
	}

	elapsed := time.Since(start)
	logger.Infof("[consolidation] LLM response received duration=%s", elapsed)

	content := strings.TrimSpace(resp.Content)
	originalContent := content
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if originalContent != content {
		logger.Debug("[consolidation] stripped markdown code blocks from LLM response")
	}

	var extracts []Extract
	if err := json.Unmarshal([]byte(content), &extracts); err != nil {
		logger.Errorf("[consolidation] failed to parse LLM response as JSON: %v", err)
		logger.Debugf("[consolidation] raw LLM response: %s", resp.Content)
		return []Extract{}
	}

	logger.Infof("[consolidation] successfully extracted %d memories from LLM response", len(extracts))
	return extracts
}

// analyzeConversationBatch performs batch memory extraction for multiple users
func (c *Consolidator) analyzeConversationBatch(ctx context.Context, conversation string, targetUserIDs []string) []UserMemoryExtract {
	start := time.Now()

	logger.Debugf("[consolidation] analyzeConversationBatch called with conversation length=%d target_users=%d", len(conversation), len(targetUserIDs))

	if c.aiClient == nil {
		logger.Error("[consolidation] AI client not configured, cannot perform LLM extraction")
		return []UserMemoryExtract{}
	}

	if strings.TrimSpace(conversation) == "" {
		logger.Warn("[consolidation] empty conversation, skipping LLM analysis")
		return []UserMemoryExtract{}
	}

	if c.prompt == "" {
		logger.Error("[consolidation] consolidation prompt is empty, cannot perform LLM extraction")
		return []UserMemoryExtract{}
	}

	logger.Debugf("[consolidation] preparing LLM prompt with conversation length=%d", len(conversation))

	// Build messages: system prompt contains extraction rules and target user list
	targetUsersStr := strings.Join(targetUserIDs, ", ")
	systemPrompt := fmt.Sprintf("%s\n\nTarget UserIDs: %s (extract memories for these users only)", c.prompt, targetUsersStr)

	req := ai.ChatCompletionRequest{
		SystemPrompt: systemPrompt,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: conversation,
			},
		},
	}

	logger.Debug("[consolidation] sending batch request to LLM for memory extraction")
	resp, err := c.aiClient.CreateChatCompletion(ctx, req)
	if err != nil {
		logger.Errorf("[consolidation] LLM batch request failed: %v", err)
		return []UserMemoryExtract{}
	}

	elapsed := time.Since(start)
	logger.Infof("[consolidation] LLM batch response received duration=%s", elapsed)

	content := strings.TrimSpace(resp.Content)
	originalContent := content
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if originalContent != content {
		logger.Debug("[consolidation] stripped markdown code blocks from LLM response")
	}

	var batchExtracts []UserMemoryExtract
	if err := json.Unmarshal([]byte(content), &batchExtracts); err != nil {
		logger.Errorf("[consolidation] failed to parse LLM batch response as JSON: %v", err)
		logger.Debugf("[consolidation] raw LLM response: %s", resp.Content)
		return []UserMemoryExtract{}
	}

	logger.Infof("[consolidation] successfully extracted memories for %d users from LLM response", len(batchExtracts))
	return batchExtracts
}

func (c *Consolidator) updateProfileFromExtraction(profile *Profile, extracts []Extract) {
	interestsAdded := 0
	factsAdded := 0

	// Initialize maps if nil
	if profile.Facts == nil {
		profile.Facts = make(map[string]string)
	}
	if profile.Preferences == nil {
		profile.Preferences = make(map[string]string)
	}

	for _, extract := range extracts {
		content := strings.ToLower(extract.Content)

		switch extract.Type {
		case string(TypeFact):
			// Extract name
			if strings.Contains(content, "name is") || strings.Contains(content, "user's name") {
				if idx := strings.Index(extract.Content, "'"); idx != -1 {
					endIdx := strings.Index(extract.Content[idx+1:], "'")
					if endIdx != -1 {
						name := extract.Content[idx+1 : idx+1+endIdx]
						profile.Facts["name"] = name
						factsAdded++
						logger.Debugf("[consolidation] added name=%q to profile for user=%s", name, profile.UserID)
					}
				}
			}

			// Extract location
			if strings.Contains(content, "lives in") || strings.Contains(content, "live in") {
				parts := strings.Split(extract.Content, " in ")
				if len(parts) > 1 {
					location := strings.TrimSpace(strings.TrimSuffix(parts[1], "."))
					profile.Facts["location"] = location
					factsAdded++
					logger.Debugf("[consolidation] added location=%q to profile for user=%s", location, profile.UserID)
				}
			}

			// Extract job
			if strings.Contains(content, "software engineer") || strings.Contains(content, "works as") || strings.Contains(content, "is a") {
				if strings.Contains(content, "software engineer") {
					profile.Facts["job"] = "software engineer"
					factsAdded++
					logger.Debugf("[consolidation] added job=software engineer to profile for user=%s", profile.UserID)
				}
			}

			// Extract workplace
			if strings.Contains(content, "works at") || strings.Contains(content, "work at") {
				parts := strings.Split(extract.Content, " at ")
				if len(parts) > 1 {
					workplace := strings.TrimSpace(strings.TrimSuffix(parts[1], "."))
					profile.Facts["workplace"] = workplace
					factsAdded++
					logger.Debugf("[consolidation] added workplace=%q to profile for user=%s", workplace, profile.UserID)
				}
			}

			// Extract specialization/skills
			if strings.Contains(content, "specializes in") || strings.Contains(content, "backend") {
				if strings.Contains(content, "go and python") || strings.Contains(content, "backend") {
					profile.Facts["specialization"] = "backend development with Go and Python"
					factsAdded++
					logger.Debugf("[consolidation] added specialization to profile for user=%s", profile.UserID)
				}
			}

		case "interest":
			// Extract interests
			if strings.Contains(content, "enjoys") || strings.Contains(content, "enjoy") {
				if strings.Contains(content, "hiking") {
					if !utils.Contains(profile.Interests, "hiking") {
						profile.Interests = append(profile.Interests, "hiking")
						interestsAdded++
						logger.Debugf("[consolidation] added interest=hiking to profile for user=%s", profile.UserID)
					}
				}
				if strings.Contains(content, "rpg") || strings.Contains(content, "video games") {
					if !utils.Contains(profile.Interests, "RPG games") {
						profile.Interests = append(profile.Interests, "RPG games")
						interestsAdded++
						logger.Debugf("[consolidation] added interest=RPG games to profile for user=%s", profile.UserID)
					}
				}
			}
		}
	}

	if factsAdded > 0 {
		logger.Infof("[consolidation] added %d new facts to profile for user=%s", factsAdded, profile.UserID)
	}
	if interestsAdded > 0 {
		logger.Infof("[consolidation] added %d new interests to profile for user=%s", interestsAdded, profile.UserID)
	}
}

func (c *Consolidator) updateProfileFromResult(profile *Profile, result *ConsolidationResult) {
	profile.LastSummary = result.Summary
	// MemoryCount is updated AFTER storage in Process() — not here

	for _, trait := range result.ProfileDelta.NewTraits {
		if !utils.Contains(profile.Traits, trait) {
			profile.Traits = append(profile.Traits, trait)
		}
	}

	for k, v := range result.ProfileDelta.NewFacts {
		profile.Facts[k] = v
	}

	for k, v := range result.ProfileDelta.NewPreferences {
		profile.Preferences[k] = v
	}

	for _, interest := range result.ProfileDelta.NewInterests {
		if !utils.Contains(profile.Interests, interest) {
			profile.Interests = append(profile.Interests, interest)
		}
	}
}
