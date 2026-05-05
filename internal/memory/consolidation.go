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
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"

	openai "github.com/sashabaranov/go-openai"
)

// qdrantStore is the subset of QdrantClient methods used by Consolidator.
type qdrantStore interface {
	UpsertMemory(ctx context.Context, memory *Record) error
	UpsertProfile(ctx context.Context, profile *Profile) error
	GetProfile(ctx context.Context, userID string) (*Profile, error)
	GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Record, error)
}

// aiChatCompleter is the subset of ai.Client methods used by Consolidator.
type aiChatCompleter interface {
	CreateChatCompletion(ctx context.Context, req ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error)
}

// visionDescriber is the subset of vision.VisionDescriber methods used by Consolidator.
type visionDescriber interface {
	DescribeImages(ctx context.Context, imageURLs []string) ([]string, error)
}

// Consolidator extracts and stores memories from conversation context using LLM analysis.
type Consolidator struct {
	qdrant            qdrantStore
	embedder          Embedder
	aiClient          aiChatCompleter
	visionDescriber   visionDescriber
	maxMessages       int
	model             string
	prompt            string
	ownBotID          string // Bot's own ID to distinguish from other bots
	memorySearchLimit int
	retryMaxRetries   int
	retryBaseDelay    time.Duration
	retryMaxDelay     time.Duration

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
func (c *Consolidator) embedWithRetry(ctx context.Context, text string) ([]float32, error) {
	return retry.Retry(ctx, c.retryMaxRetries, func(ctx context.Context) ([]float32, error) {
		return c.embedder.Embed(ctx, text)
	}, retry.WithBaseDelay(c.retryBaseDelay), retry.WithMaxDelay(c.retryMaxDelay))
}

// NewConsolidator creates a new consolidator with the given Qdrant client, embedder, and AI configuration.
func NewConsolidator(qdrant *QdrantClient, embedder Embedder, aiClient aiChatCompleter, visionDescriber visionDescriber, cfg *config.ConsolidationConfig, ownBotID string, consolidationInterval int, memorySearchLimit int, retryMaxRetries int, retryBaseDelayMs int, retryMaxDelayMs int) *Consolidator {
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
		retryMaxRetries:   retryMaxRetries,
		retryBaseDelay:    time.Duration(retryBaseDelayMs) * time.Millisecond,
		retryMaxDelay:     time.Duration(retryMaxDelayMs) * time.Millisecond,
	}
}

// buildConversationText builds a conversation text from messages for LLM analysis.
// userID is used for logging context; if empty, logs omit per-user details.
func (c *Consolidator) buildConversationText(ctx context.Context, messages []*DiscordMessage, userID string) (string, int) {
	var conversation strings.Builder
	var imageCount int
	for i, msg := range messages {
		timeStr := msg.Timestamp.UTC().Format(time.RFC3339)
		var botMarker string
		if msg.AuthorID == c.ownBotID {
			botMarker = ",BOT=2" // Own bot - completely ignore
		} else if msg.IsBot {
			botMarker = ",BOT=1" // Other bots - minimal extraction
		}
		fmt.Fprintf(&conversation, `"%s"{UserID=%s,Time=%s%s}: "%s"`+"\n", msg.Username, msg.AuthorID, timeStr, botMarker, msg.Content)

		if userID != "" {
			logger.Debugf("[consolidation] message %d [%s] for user=%s: %s%s: %s", i+1, timeStr, userID, msg.Username, botMarker, msg.Content)
		} else {
			logger.Debugf("[consolidation] message %d [%s]: %s (ID=%s)%s: %s", i+1, timeStr, msg.Username, msg.AuthorID, botMarker, msg.Content)
		}

		if len(msg.ImageURLs) > 0 && c.visionDescriber != nil {
			descriptions := make([]string, 0, len(msg.ImageDescriptions))

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
				fmt.Fprintf(&conversation, "  [Attached Image %d: %s]\n", j+1, desc)
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
		logger.Warnf("[consolidation] truncating messages for user=%s from %d to %d", userID, len(messages), c.maxMessages)
		messages = messages[:c.maxMessages]
	}

	conversation, imageCount := c.buildConversationText(ctx, messages, userID)
	logger.Infof("[consolidation] built conversation for user=%s length=%d chars images=%d", userID, len(conversation), imageCount)

	profile, err := c.getOrCreateProfile(ctx, userID)
	if err != nil {
		return fmt.Errorf("getOrCreateProfile: %w", err)
	}

	extracted, err := c.analyzeConversation(ctx, conversation, []string{userID})
	if err != nil {
		return fmt.Errorf("analyzeConversation: %w", err)
	}
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

	// Store memories first, then update profile
	stored, err := c.storeMemories(ctx, userID, extracted)
	if err != nil {
		if stored == 0 {
			return fmt.Errorf("failed to store memories for user=%s: %w", userID, err)
		}
		logger.Warnf("[consolidation] partial failure storing memories for user=%s: %v", userID, err)
	}

	// Update MemoryCount before persisting profile
	if stored > 0 {
		profile.MemoryCount += stored
	}

	// Persist profile after memory storage succeeds
	if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
		return fmt.Errorf("update profile for user=%s: %w", userID, err)
	}
	logger.Infof("[consolidation] updated profile for user=%s before=[%s] after=[%s]",
		userID, profileBefore, profileAfter)

	c.setLastConsolidatedAt(time.Now())

	elapsed := time.Since(start)
	logger.Infof("[consolidation] completed for user=%s duration=%s messages_processed=%d memories_extracted=%d memories_stored=%d",
		userID, elapsed, len(messages), len(extracted), stored)
	return nil
}

// storeMemories creates Records from extracts, generates embeddings with retry,
// upserts them into Qdrant, and returns the number successfully stored.
func (c *Consolidator) storeMemories(ctx context.Context, userID string, extracts []Extract) (int, error) {
	var stored int
	errs := make([]error, 0, len(extracts))
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

		embedding, err := retry.Retry(ctx, c.retryMaxRetries, func(ctx context.Context) ([]float32, error) {
			return c.embedder.Embed(ctx, memory.Content)
		},
			retry.WithBaseDelay(c.retryBaseDelay),
			retry.WithMaxDelay(c.retryMaxDelay),
		)
		if err != nil {
			logger.Errorf("[consolidation] embedding exhausted for memory %d for user=%s: %v", i+1, userID, err)
			errs = append(errs, fmt.Errorf("embedding memory %d for user=%s: %w", i+1, userID, err))
			continue
		}
		memory.Embedding = embedding

		if err := c.qdrant.UpsertMemory(ctx, memory); err != nil {
			logger.Errorf("[consolidation] failed to store memory %d for user=%s: %v", i+1, userID, err)
			errs = append(errs, fmt.Errorf("store memory %d for user=%s: %w", i+1, userID, err))
		} else {
			stored++
		}
	}
	return stored, errors.Join(errs...)
}

// ProcessChannelMessages performs batch consolidation for all users identified in the channel messages.
func (c *Consolidator) ProcessChannelMessages(ctx context.Context, channelID string, messages []*DiscordMessage) error {
	start := time.Now()

	userIDSet := make(map[string]struct{})
	for _, msg := range messages {
		userIDSet[msg.AuthorID] = struct{}{}
	}

	targetUserIDs := make([]string, 0, len(userIDSet))
	for userID := range userIDSet {
		targetUserIDs = append(targetUserIDs, userID)
	}

	logger.Infof("[consolidation] starting batch consolidation for channel=%s messages=%d users=%d", channelID, len(messages), len(targetUserIDs))

	if len(messages) > c.maxMessages {
		logger.Warnf("[consolidation] truncating messages from %d to %d", len(messages), c.maxMessages)
		messages = messages[:c.maxMessages]
	}

	conversation, imageCount := c.buildConversationText(ctx, messages, "")

	logger.Infof("[consolidation] built conversation length=%d chars images=%d users=%v", len(conversation), imageCount, targetUserIDs)

	batchExtracts, err := c.analyzeConversationBatch(ctx, conversation, targetUserIDs)
	if err != nil {
		return fmt.Errorf("analyzeConversationBatch: %w", err)
	}
	if len(batchExtracts) == 0 {
		elapsed := time.Since(start)
		logger.Infof("[consolidation] no memories extracted for channel=%s duration=%s", channelID, elapsed)
		return nil
	}

	logger.Infof("[consolidation] extracted memories for %d users from channel=%s", len(batchExtracts), channelID)

	var totalStored int
	allErrs := make([]error, 0, len(batchExtracts))
	for _, userExtract := range batchExtracts {
		userID := userExtract.UserID
		extracts := userExtract.Memories

		if len(extracts) == 0 {
			logger.Debugf("[consolidation] no memories to store for user=%s", userID)
			continue
		}

		profile, err := c.getOrCreateProfile(ctx, userID)
		if err != nil {
			logger.Errorf("[consolidation] failed to get or create profile for user=%s: %v", userID, err)
			continue
		}
		c.updateProfileFromExtraction(profile, extracts)
		profile.LastConsolidatedAt = time.Now()

		if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
			logger.Errorf("[consolidation] failed to update profile for user=%s: %v", userID, err)
			continue
		}

		stored, err := c.storeMemories(ctx, userID, extracts)
		if err != nil {
			if stored == 0 {
				allErrs = append(allErrs, fmt.Errorf("user=%s: %w", userID, err))
				continue
			}
			logger.Warnf("[consolidation] partial failure storing memories for user=%s: %v", userID, err)
		}
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

	if len(allErrs) > 0 {
		return fmt.Errorf("batch consolidation partial failures: %w", errors.Join(allErrs...))
	}
	return nil
}

func (c *Consolidator) getOrCreateProfile(ctx context.Context, userID string) (*Profile, error) {
	profile, err := c.qdrant.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			logger.Infof("[consolidation] creating new profile for user=%s", userID)
			return &Profile{
				UserID:      userID,
				Traits:      []string{},
				Facts:       make(map[string]string),
				Preferences: make(map[string]string),
				Interests:   []string{},
				FirstSeenAt: time.Now(),
			}, nil
		}
		return nil, fmt.Errorf("failed to get profile for user=%s: %w", userID, err)
	}
	logger.Infof("[consolidation] loaded existing profile for user=%s traits=%d facts=%d preferences=%d interests=%d",
		userID, len(profile.Traits), len(profile.Facts), len(profile.Preferences), len(profile.Interests))
	return profile, nil
}

// sanitizeJSON preprocesses JSON from LLM responses for Go 1.25 compatibility.
// It handles invalid UTF-8 bytes and removes duplicate keys.
func sanitizeJSON(s string) string {
	// Replace invalid UTF-8 bytes with the Unicode replacement character
	// This prevents Go 1.25's stricter json.Unmarshal from rejecting them
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x80 {
			buf.WriteByte(b)
		} else if b < 0xC0 {
			// Invalid continuation byte, replace
			buf.WriteRune('\uFFFD')
		} else if b < 0xE0 {
			// 2-byte sequence
			if i+1 < len(s) && (s[i+1]&0xC0) == 0x80 {
				buf.WriteByte(b)
				buf.WriteByte(s[i+1])
				i++
			} else {
				buf.WriteRune('\uFFFD')
			}
		} else if b < 0xF0 {
			// 3-byte sequence
			if i+2 < len(s) && (s[i+1]&0xC0) == 0x80 && (s[i+2]&0xC0) == 0x80 {
				buf.WriteByte(b)
				buf.WriteByte(s[i+1])
				buf.WriteByte(s[i+2])
				i += 2
			} else {
				buf.WriteRune('\uFFFD')
			}
		} else if b < 0xF8 {
			// 4-byte sequence
			if i+3 < len(s) && (s[i+1]&0xC0) == 0x80 && (s[i+2]&0xC0) == 0x80 && (s[i+3]&0xC0) == 0x80 {
				buf.WriteByte(b)
				buf.WriteByte(s[i+1])
				buf.WriteByte(s[i+2])
				buf.WriteByte(s[i+3])
				i += 3
			} else {
				buf.WriteRune('\uFFFD')
			}
		} else {
			buf.WriteRune('\uFFFD')
		}
	}
	return buf.String()
}

// extractJSONFromLLMResponse extracts JSON from LLM responses that may contain
// markdown code blocks, explanatory text, or other non-JSON content.
func extractJSONFromLLMResponse(content string) string {
	content = strings.TrimSpace(content)

	// Try to find JSON array first (for consolidation responses)
	if idx := strings.Index(content, "["); idx >= 0 {
		endIdx := strings.LastIndex(content, "]")
		if endIdx > idx {
			return strings.TrimSpace(content[idx : endIdx+1])
		}
	}

	// Try to find JSON object
	if idx := strings.Index(content, "{"); idx >= 0 {
		endIdx := strings.LastIndex(content, "}")
		if endIdx > idx {
			return strings.TrimSpace(content[idx : endIdx+1])
		}
	}

	// Fall back to markdown stripping
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}

func (c *Consolidator) analyzeConversation(ctx context.Context, conversation string, targetUserIDs []string) ([]Extract, error) {
	batch, err := c.analyzeConversationBatch(ctx, conversation, targetUserIDs)
	if err != nil {
		return nil, err
	}
	extracts := make([]Extract, 0, len(batch))
	for _, userMem := range batch {
		extracts = append(extracts, userMem.Memories...)
	}
	logger.Infof("[consolidation] successfully extracted %d memories from LLM response", len(extracts))
	return extracts, nil
}

// analyzeConversationBatch performs batch memory extraction for multiple users
func (c *Consolidator) analyzeConversationBatch(ctx context.Context, conversation string, targetUserIDs []string) ([]UserMemoryExtract, error) {
	content, err := c.callExtractionLLM(ctx, conversation, targetUserIDs, "batch memory extraction", "LLM batch request failed")
	if err != nil {
		return nil, err
	}
	if content == "" {
		return nil, nil
	}
	batchExtracts := make([]UserMemoryExtract, 0, len(targetUserIDs))
	if err := json.Unmarshal([]byte(content), &batchExtracts); err != nil {
		return nil, fmt.Errorf("consolidation: failed to parse LLM batch response: %w", err)
	}
	logger.Infof("[consolidation] successfully extracted memories for %d users from LLM response", len(batchExtracts))
	return batchExtracts, nil
}

// callExtractionLLM performs the shared LLM interaction for memory extraction.
// Returns the sanitized JSON content from the LLM response.
func (c *Consolidator) callExtractionLLM(ctx context.Context, conversation string, targetUserIDs []string, logLabel, errLabel string) (string, error) {
	start := time.Now()

	if c.aiClient == nil {
		logger.Error("[consolidation] AI client not configured, cannot perform LLM extraction")
		return "", fmt.Errorf("consolidation: AI client not configured")
	}

	if strings.TrimSpace(conversation) == "" {
		logger.Warnf("[consolidation] empty conversation, skipping LLM analysis")
		return "", nil
	}

	if c.prompt == "" {
		logger.Error("[consolidation] consolidation prompt is empty, cannot perform LLM extraction")
		return "", fmt.Errorf("consolidation: system prompt is empty")
	}

	logger.Debugf("[consolidation] preparing LLM prompt with conversation length=%d", len(conversation))

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

	logger.Debugf("[consolidation] sending request to LLM for %s", logLabel)
	resp, err := c.aiClient.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("consolidation: %s: %w", errLabel, err)
	}

	elapsed := time.Since(start)
	logger.Infof("[consolidation] LLM %s received duration=%s", logLabel, elapsed)

	content := extractJSONFromLLMResponse(resp.Content)
	content = sanitizeJSON(content)

	logger.Debugf("[consolidation] raw LLM response: %s", resp.Content)

	return content, nil
}

func (c *Consolidator) updateProfileFromExtraction(profile *Profile, extracts []Extract) {
	// TODO: Fact extraction uses English-only strings.Contains patterns ("name is",
	// "lives in", "software engineer", etc.). This fragile approach should be
	// replaced with structured LLM output parsing that supports any language the
	// LLM produces.
	var interestsAdded int
	var factsAdded int

	// Initialize maps if nil
	if profile.Facts == nil {
		profile.Facts = make(map[string]string)
	}
	if profile.Preferences == nil {
		profile.Preferences = make(map[string]string)
	}

	for _, extract := range extracts {
		switch extract.Type {
		case string(TypeFact):
			if facts := parseFactKeyValues(extract.Content); len(facts) > 0 {
				for key, value := range facts {
					profile.Facts[key] = value
					factsAdded++
					logger.Debugf("[consolidation] added fact %s=%q to profile for user=%s", key, value, profile.UserID)
				}
				continue
			}

			content := strings.ToLower(extract.Content)
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

		case string(TypeInterest):
			for _, interest := range extractInterestItems(extract) {
				if interest == "" {
					continue
				}
				if !containsFold(profile.Interests, interest) {
					profile.Interests = append(profile.Interests, interest)
					interestsAdded++
					logger.Debugf("[consolidation] added interest=%q to profile for user=%s", interest, profile.UserID)
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

func parseFactKeyValues(content string) map[string]string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(trimmed), &raw); err == nil {
			values := make(map[string]string, len(raw))
			for key, value := range raw {
				factKey := normalizeFactKey(key)
				factValue := normalizeFactValue(fmt.Sprint(value))
				if factKey != "" && factValue != "" {
					values[factKey] = factValue
				}
			}
			if len(values) > 0 {
				return values
			}
		}
	}

	key, value, ok := splitFactKeyValue(trimmed)
	if !ok {
		return nil
	}
	return map[string]string{key: value}
}

func splitFactKeyValue(content string) (string, string, bool) {
	for _, sep := range []string{":", "="} {
		if idx := strings.Index(content, sep); idx != -1 {
			key := normalizeFactKey(content[:idx])
			value := normalizeFactValue(content[idx+1:])
			if key != "" && value != "" {
				return key, value, true
			}
		}
	}
	return "", "", false
}

func normalizeFactKey(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	return strings.ToLower(value)
}

func normalizeFactValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	value = strings.TrimSuffix(value, ".")
	return strings.TrimSpace(value)
}

func extractInterestItems(extract Extract) []string {
	if len(extract.Keywords) > 0 {
		items := make([]string, 0, len(extract.Keywords))
		for _, keyword := range extract.Keywords {
			value := normalizeInterestValue(keyword)
			if value != "" {
				items = append(items, value)
			}
		}
		return items
	}

	value := normalizeInterestValue(extract.Content)
	if value == "" {
		return nil
	}
	return []string{value}
}

func normalizeInterestValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	value = strings.TrimSuffix(value, ".")
	return strings.TrimSpace(value)
}

func containsFold(values []string, candidate string) bool {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}
