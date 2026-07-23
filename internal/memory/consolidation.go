package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/ai/llmjson"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"

	"github.com/google/uuid"
	openai "github.com/sashabaranov/go-openai"
)

// Package-level compiled regexes for Discord mention extraction.
var (
	userMentionRe    = regexp.MustCompile(`<@!?(\d+)>`)
	channelMentionRe = regexp.MustCompile(`<#(\d+)>`)
)

// qdrantStore is the subset of QdrantClient methods used by Consolidator.
type qdrantStore interface {
	UpsertMemory(ctx context.Context, memory *Record) error
	UpsertProfile(ctx context.Context, profile *Profile) error
	GetProfile(ctx context.Context, userID string) (*Profile, error)
	GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Record, error)
	SearchMemories(ctx context.Context, userID string, embedding []float32, opts *SearchOptions) ([]*Record, error)
	ListMemoriesByType(ctx context.Context, userID string, memoryType string) ([]*Record, error)
	UpsertRelationship(ctx context.Context, rel *Relationship) error
	GetRelationshipBetween(ctx context.Context, userA, userB string, relType RelationshipType) ([]*Relationship, error)
}

// extractUserMentions extracts Discord user mention IDs from content.
func extractUserMentions(content string) []string {
	matches := userMentionRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	result := make([]string, 0, len(matches))
	for _, m := range matches {
		id := m[1]
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result
}

// extractChannelMentions extracts Discord channel mention IDs from content.
func extractChannelMentions(content string) []string {
	matches := channelMentionRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	result := make([]string, 0, len(matches))
	for _, m := range matches {
		id := m[1]
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result
}

// extractMentions extracts all mentioned user IDs and channel IDs from a batch of messages.
func extractMentions(messages []*DiscordMessage) (userIDs, channelIDs []string) {
	userSeen := make(map[string]struct{})
	channelSeen := make(map[string]struct{})
	for _, msg := range messages {
		for _, uid := range extractUserMentions(msg.Content) {
			userSeen[uid] = struct{}{}
		}
		for _, cid := range extractChannelMentions(msg.Content) {
			channelSeen[cid] = struct{}{}
		}
	}
	userIDs = make([]string, 0, len(userSeen))
	for uid := range userSeen {
		userIDs = append(userIDs, uid)
	}
	channelIDs = make([]string, 0, len(channelSeen))
	for cid := range channelSeen {
		channelIDs = append(channelIDs, cid)
	}
	return
}

// relationshipID builds a deterministic UUID from two user IDs and a type.
// Uses UUID v5 (SHA-1) to generate a valid RFC 4122 UUID that Qdrant accepts,
// while remaining deterministic (same inputs 鈫?same UUID).
func relationshipID(userA, userB string, relType RelationshipType) string {
	if userA > userB {
		userA, userB = userB, userA
	}
	name := []byte(userA + ":" + userB + ":" + string(relType))
	return uuid.NewSHA1(uuid.NameSpaceDNS, name).String()
}

// aiChatCompleter is the subset of ai.Client methods used by Consolidator.
type aiChatCompleter interface {
	CreateChatCompletionSingle(ctx context.Context, req ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error)
}

// visionDescriber is the subset of vision.VisionDescriber methods used by Consolidator.
type visionDescriber interface {
	DescribeImages(ctx context.Context, imageURLs []string) ([]string, error)
}

// Consolidator extracts and stores memories from conversation context using LLM analysis.
type Consolidator struct {
	qdrant              qdrantStore
	embedder            Embedder
	aiClient            aiChatCompleter
	visionDescriber     visionDescriber
	maxMessages         int
	model               string
	prompt              string
	ownBotID            string // Bot's own ID to distinguish from other bots
	memorySearchLimit   int
	otherBotPolicy      config.OtherBotPolicy
	entropyMinWordRatio float64
	retryMaxRetries     int
	retryBaseDelay      time.Duration
	retryMaxDelay       time.Duration

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
func NewConsolidator(qdrant *QdrantClient, embedder Embedder, aiClient aiChatCompleter, visionDescriber visionDescriber, cfg *config.ConsolidationConfig, ownBotID string, consolidationInterval int, memorySearchLimit int, otherBotPolicy config.OtherBotPolicy, entropyMinWordRatio float64, retryMaxRetries int, retryBaseDelayMs int, retryMaxDelayMs int) *Consolidator {
	return &Consolidator{
		qdrant:              qdrant,
		embedder:            embedder,
		aiClient:            aiClient,
		visionDescriber:     visionDescriber,
		model:               cfg.Model,
		maxMessages:         consolidationInterval,
		prompt:              cfg.SystemPrompt,
		ownBotID:            ownBotID,
		memorySearchLimit:   memorySearchLimit,
		otherBotPolicy:      otherBotPolicy,
		entropyMinWordRatio: entropyMinWordRatio,
		retryMaxRetries:     retryMaxRetries,
		retryBaseDelay:      time.Duration(retryBaseDelayMs) * time.Millisecond,
		retryMaxDelay:       time.Duration(retryMaxDelayMs) * time.Millisecond,
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
			botMarker = ",BOT=SELF" // BOT=SELF tag - always present, prompt controls behavior
		} else if msg.IsBot {
			botMarker = ",BOT=OTHERS" // BOT=OTHERS tag - always present, prompt controls behavior
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

	// Apply entropy gate - filter noise messages
	filtered := make([]*DiscordMessage, 0, len(messages))
	entropyCfg := EntropyGateConfig{
		OtherBotPolicy:     c.otherBotPolicy,
		MinUniqueWordRatio: c.entropyMinWordRatio,
	}
	for _, msg := range messages {
		if PassesEntropyGate(msg, entropyCfg) {
			filtered = append(filtered, msg)
		}
	}
	if len(filtered) == 0 {
		logger.Infof("[consolidation] all messages filtered by entropy gate for user=%s", userID)
		return nil
	}
	messages = filtered

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

	var channelID, guildID string
	if len(messages) > 0 {
		channelID = messages[0].ChannelID
		guildID = messages[0].GuildID
	}

	mentionedUserIDs, mentionedChannelIDs := extractMentions(messages)

	stored, err := c.storeMemories(ctx, userID, extracted, channelID, guildID, mentionedUserIDs, mentionedChannelIDs)
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

	// Track relationships from mentions
	if err := c.updateRelationships(ctx, messages); err != nil {
		logger.Warnf("[consolidation] failed to update relationships for user=%s: %v", userID, err)
	}

	c.setLastConsolidatedAt(time.Now())

	elapsed := time.Since(start)
	logger.Infof("[consolidation] completed for user=%s duration=%s messages_processed=%d memories_extracted=%d memories_stored=%d",
		userID, elapsed, len(messages), len(extracted), stored)
	return nil
}

// storeMemories creates Records from extracts, generates embeddings with retry,
// performs evolutionary deduplication (reusing IDs of similar existing memories),
// and upserts into Qdrant. Returns the number successfully stored.
// channelID and guildID are used to scope memories for retrieval filtering.
func (c *Consolidator) storeMemories(ctx context.Context, userID string, extracts []Extract, channelID, guildID string, mentionedUserIDs, mentionedChannelIDs []string) (int, error) {
	var stored int
	errs := make([]error, 0, len(extracts))
	for i, extract := range extracts {
		content := extract.Content
		if content == "" {
			continue
		}

		embedding, err := retry.Retry(ctx, c.retryMaxRetries, func(ctx context.Context) ([]float32, error) {
			return c.embedder.Embed(ctx, content)
		},
			retry.WithBaseDelay(c.retryBaseDelay),
			retry.WithMaxDelay(c.retryMaxDelay),
		)
		if err != nil {
			logger.Errorf("[consolidation] embedding exhausted for memory %d for user=%s: %v", i+1, userID, err)
			errs = append(errs, fmt.Errorf("embedding memory %d for user=%s: %w", i+1, userID, err))
			continue
		}

		memory := &Record{
			UserID:              userID,
			GuildID:             guildID,
			ChannelID:           channelID,
			MentionedUserIDs:    mentionedUserIDs,
			MentionedChannelIDs: mentionedChannelIDs,
			MemoryType:          Type(extract.Type),
			Content:             content,
			Keywords:            extract.Keywords,
			Confidence:          extract.Confidence,
			Embedding:           embedding,
		}

		// Type-aware dedup
		skip := false
		switch extract.Type {
		case string(TypeFact):
			dedupOpts := &SearchOptions{
				TopK:        1,
				MinScore:    0.70,
				MemoryTypes: []string{string(TypeFact)},
			}
			existing, searchErr := c.qdrant.SearchMemories(ctx, userID, embedding, dedupOpts)
			if searchErr != nil {
				logger.Warnf("[consolidation] fact dedup search failed for user=%s: %v", userID, searchErr)
			} else if len(existing) > 0 {
				old := existing[0]
				// Importance-aware dedup
				newImportance := 0.5
				if extract.ImportanceScore != nil {
					newImportance = *extract.ImportanceScore
				}
				if extract.ImportanceScore != nil && old.Confidence > newImportance {
					logger.Debugf("[consolidation] skipping fact extract: old confidence=%.2f > new importance=%.2f", old.Confidence, newImportance)
					skip = true
					break
				}
				memory.ID = old.ID
				memory.CreatedAt = old.CreatedAt
				memory.GuildID = old.GuildID
				memory.ChannelID = old.ChannelID
				// Union keywords
				if len(old.Keywords) > 0 {
					seen := make(map[string]struct{}, len(old.Keywords)+len(extract.Keywords))
					for _, kw := range old.Keywords {
						seen[kw] = struct{}{}
					}
					for _, kw := range extract.Keywords {
						seen[kw] = struct{}{}
					}
					merged := make([]string, 0, len(seen))
					for kw := range seen {
						merged = append(merged, kw)
					}
					memory.Keywords = merged
				}
				logger.Debugf("[consolidation] fact dedup: reused memoryID=%s for user=%s semantic match", old.ID, userID)
			}

		case string(TypeEpisode):
			dedupOpts := &SearchOptions{
				TopK:        1,
				MinScore:    0.85,
				MemoryTypes: []string{string(TypeEpisode)},
			}
			existing, searchErr := c.qdrant.SearchMemories(ctx, userID, embedding, dedupOpts)
			if searchErr != nil {
				logger.Warnf("[consolidation] episode dedup search failed for user=%s: %v", userID, searchErr)
			} else if len(existing) > 0 {
				old := existing[0]
				// Importance-aware dedup
				newImportance := 0.5
				if extract.ImportanceScore != nil {
					newImportance = *extract.ImportanceScore
				}
				if extract.ImportanceScore != nil && old.Confidence > newImportance {
					logger.Debugf("[consolidation] skipping episode extract: old confidence=%.2f > new importance=%.2f", old.Confidence, newImportance)
					skip = true
					break
				}
				memory.ID = old.ID
				memory.CreatedAt = old.CreatedAt
				memory.GuildID = old.GuildID
				memory.ChannelID = old.ChannelID
				// Union keywords
				if len(old.Keywords) > 0 {
					seen := make(map[string]struct{}, len(old.Keywords)+len(extract.Keywords))
					for _, kw := range old.Keywords {
						seen[kw] = struct{}{}
					}
					for _, kw := range extract.Keywords {
						seen[kw] = struct{}{}
					}
					merged := make([]string, 0, len(seen))
					for kw := range seen {
						merged = append(merged, kw)
					}
					memory.Keywords = merged
				}
				logger.Debugf("[consolidation] episode dedup: reused memoryID=%s for user=%s", old.ID, userID)
			}

		default:
			dedupOpts := &SearchOptions{
				TopK:        1,
				MinScore:    0.90,
				MemoryTypes: []string{extract.Type},
			}
			existing, searchErr := c.qdrant.SearchMemories(ctx, userID, embedding, dedupOpts)
			if searchErr != nil {
				logger.Warnf("[consolidation] dedup search failed for memory %d user=%s: %v", i+1, userID, searchErr)
			} else if len(existing) > 0 {
				old := existing[0]
				// Importance-aware dedup
				newImportance := 0.5
				if extract.ImportanceScore != nil {
					newImportance = *extract.ImportanceScore
				}
				if extract.ImportanceScore != nil && old.Confidence > newImportance {
					logger.Debugf("[consolidation] skipping extract: old confidence=%.2f > new importance=%.2f", old.Confidence, newImportance)
					skip = true
					break
				}
				memory.ID = old.ID
				memory.CreatedAt = old.CreatedAt
				memory.GuildID = old.GuildID
				memory.ChannelID = old.ChannelID
				// Union keywords
				if len(old.Keywords) > 0 {
					seen := make(map[string]struct{}, len(old.Keywords)+len(extract.Keywords))
					for _, kw := range old.Keywords {
						seen[kw] = struct{}{}
					}
					for _, kw := range extract.Keywords {
						seen[kw] = struct{}{}
					}
					merged := make([]string, 0, len(seen))
					for kw := range seen {
						merged = append(merged, kw)
					}
					memory.Keywords = merged
				}
				logger.Debugf("[consolidation] dedup: reused memoryID=%s for user=%s type=%s", old.ID, userID, old.MemoryType)
			}
		}

		if skip {
			continue
		}

		// Set importance score from extract
		if extract.ImportanceScore != nil {
			memory.ImportanceScore = *extract.ImportanceScore
		}

		// Assign decay tier
		score := memory.ImportanceScore*0.7 + memory.Confidence*0.3
		memory.DecayCategory = ClassifyTier(score)

		if memory.ID == "" {
			memory.CreatedAt = time.Now()
		}

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
		if msg.AuthorID == c.ownBotID {
			continue
		}
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

	// Apply entropy gate - filter noise messages
	filtered := make([]*DiscordMessage, 0, len(messages))
	entropyCfg := EntropyGateConfig{
		OtherBotPolicy:     c.otherBotPolicy,
		MinUniqueWordRatio: c.entropyMinWordRatio,
	}
	for _, msg := range messages {
		if PassesEntropyGate(msg, entropyCfg) {
			filtered = append(filtered, msg)
		}
	}
	if len(filtered) == 0 {
		logger.Infof("[consolidation] all messages filtered by entropy gate for channel=%s", channelID)
		return nil
	}
	messages = filtered

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

	// Count messages per user from the filtered batch to update profile.MessageCount
	userMsgCount := make(map[string]int, len(targetUserIDs))
	for _, msg := range messages {
		userMsgCount[msg.AuthorID]++
	}

	mentionedUserIDs, mentionedChannelIDs := extractMentions(messages)

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
		profile.MessageCount += userMsgCount[userID]
		profile.LastConsolidatedAt = time.Now()

		if err := c.qdrant.UpsertProfile(ctx, profile); err != nil {
			logger.Errorf("[consolidation] failed to update profile for user=%s: %v", userID, err)
			continue
		}

		var guildID string
		if len(messages) > 0 {
			guildID = messages[0].GuildID
		}
		stored, err := c.storeMemories(ctx, userID, extracts, channelID, guildID, mentionedUserIDs, mentionedChannelIDs)
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
		logger.Infof("[consolidation] stored %d memories for user=%s (message_count=%d)", stored, userID, profile.MessageCount)
	}

	// Track relationships from mentions across all messages in the channel
	if err := c.updateRelationships(ctx, messages); err != nil {
		logger.Warnf("[consolidation] failed to update relationships for channel=%s: %v", channelID, err)
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

// updateRelationships extracts mentions from messages and creates or increments
// mention-type relationships between users. Skips self-mentions and own-bot messages.
func (c *Consolidator) updateRelationships(ctx context.Context, messages []*DiscordMessage) error {
	for _, msg := range messages {
		if msg.AuthorID == c.ownBotID {
			continue
		}

		mentioned := extractUserMentions(msg.Content)
		if len(mentioned) == 0 {
			continue
		}

		channelIDs := make([]string, 0, 1)
		if msg.ChannelID != "" {
			channelIDs = append(channelIDs, msg.ChannelID)
		}

		for _, mentionedID := range mentioned {
			if mentionedID == msg.AuthorID {
				continue
			}

			relID := relationshipID(msg.AuthorID, mentionedID, RelMention)
			existing, err := c.qdrant.GetRelationshipBetween(ctx, msg.AuthorID, mentionedID, RelMention)
			if err != nil {
				logger.Warnf("[consolidation] failed to query relationship between %s and %s: %v",
					msg.AuthorID, mentionedID, err)
				continue
			}

			if len(existing) > 0 {
				rel := existing[0]
				rel.InteractionCount++
				rel.LastInteractionAt = time.Now()
				rel.Weight = float64(rel.InteractionCount)

				chSeen := make(map[string]struct{}, len(rel.ChannelIDs)+len(channelIDs))
				for _, ch := range rel.ChannelIDs {
					chSeen[ch] = struct{}{}
				}
				for _, ch := range channelIDs {
					if _, ok := chSeen[ch]; !ok {
						chSeen[ch] = struct{}{}
						rel.ChannelIDs = append(rel.ChannelIDs, ch)
					}
				}

				if err := c.qdrant.UpsertRelationship(ctx, rel); err != nil {
					logger.Warnf("[consolidation] failed to update relationship id=%s: %v", rel.ID, err)
				}
			} else {
				rel := &Relationship{
					ID:                relID,
					UserA:             msg.AuthorID,
					UserB:             mentionedID,
					Type:              RelMention,
					InteractionCount:  1,
					LastInteractionAt: time.Now(),
					ChannelIDs:        channelIDs,
					Weight:            1.0,
				}
				// Ensure UserA <= UserB for consistency
				if rel.UserA > rel.UserB {
					rel.UserA, rel.UserB = rel.UserB, rel.UserA
				}
				if err := c.qdrant.UpsertRelationship(ctx, rel); err != nil {
					logger.Warnf("[consolidation] failed to create relationship between %s and %s: %v",
						msg.AuthorID, mentionedID, err)
				}
			}
		}
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

func (c *Consolidator) analyzeConversationBatch(ctx context.Context, conversation string, targetUserIDs []string) ([]UserMemoryExtract, error) {
	if c.aiClient == nil {
		logger.Error("[consolidation] AI client not configured, cannot perform LLM extraction")
		return nil, fmt.Errorf("consolidation: AI client not configured")
	}
	if strings.TrimSpace(conversation) == "" {
		logger.Warnf("[consolidation] empty conversation, skipping LLM analysis")
		return nil, nil
	}
	if c.prompt == "" {
		logger.Error("[consolidation] consolidation prompt is empty, cannot perform LLM extraction")
		return nil, fmt.Errorf("consolidation: system prompt is empty")
	}

	allow := make(map[string]struct{}, len(targetUserIDs))
	for _, id := range targetUserIDs {
		allow[id] = struct{}{}
	}

	targetUsersStr := strings.Join(targetUserIDs, ", ")
	systemPrompt := fmt.Sprintf("%s\n\nTarget UserIDs: %s (extract memories for these users only)", c.prompt, targetUsersStr)
	baseReq := ai.ChatCompletionRequest{
		SystemPrompt: systemPrompt,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: conversation},
		},
	}
	var feedback string

	batch, err := retry.Retry(ctx, c.retryMaxRetries, func(ctx context.Context) ([]UserMemoryExtract, error) {
		req := baseReq
		if feedback != "" {
			req.Messages = append(append([]openai.ChatCompletionMessage{}, baseReq.Messages...), openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: feedback,
			})
		}
		start := time.Now()
		logger.Debugf("[consolidation] sending request to LLM for batch memory extraction")
		resp, err := c.aiClient.CreateChatCompletionSingle(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("consolidation: LLM batch request failed: %w", err)
		}
		logger.Infof("[consolidation] LLM batch memory extraction received duration=%s", time.Since(start))
		logger.Debugf("[consolidation] raw LLM response: %s", resp.Content)

		items, err := llmjson.DecodeSlice(resp.Content, func(e *UserMemoryExtract) error {
			return validateUserMemoryExtract(e, allow)
		}, nil)
		if err != nil {
			feedback = "Your previous response failed validation: " + err.Error() +
				`. Return ONLY a JSON array of {"user_id":"...","memories":[{"content":"...","type":"fact|episode|interest|summary","confidence":0-1,...}]}`
			logger.Warnf("[consolidation] schema/parse failed, will retry if budget remains: %v", err)
			return nil, err
		}
		return items, nil
	},
		retry.WithBaseDelay(c.retryBaseDelay),
		retry.WithMaxDelay(c.retryMaxDelay),
		retry.WithErrorClassifier(func(err error) bool {
			return llmjson.IsOutputError(err) || ai.IsRetryableError(err)
		}),
	)
	if err != nil {
		return nil, err
	}
	logger.Infof("[consolidation] successfully extracted memories for %d users from LLM response", len(batch))
	return batch, nil
}

func validateUserMemoryExtract(u *UserMemoryExtract, allow map[string]struct{}) error {
	if strings.TrimSpace(u.UserID) == "" {
		return llmjson.SchemaError("user_id must be non-empty", nil)
	}
	if _, ok := allow[u.UserID]; !ok {
		return llmjson.SchemaError(fmt.Sprintf("user_id %q not in target allowlist", u.UserID), nil)
	}
	for i := range u.Memories {
		if err := validateExtract(&u.Memories[i]); err != nil {
			return err
		}
	}
	return nil
}

func profileUpdatesNonEmpty(p *ProfileUpdateSet) bool {
	if p == nil {
		return false
	}
	for _, t := range p.Traits {
		if strings.TrimSpace(t) != "" {
			return true
		}
	}
	for _, i := range p.Interests {
		if strings.TrimSpace(i) != "" {
			return true
		}
	}
	for k, v := range p.Facts {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	for k, v := range p.Preferences {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

func validateExtract(e *Extract) error {
	content := strings.TrimSpace(e.Content)
	profileOnly := content == "" && profileUpdatesNonEmpty(e.ProfileUpdates)
	if content == "" && !profileOnly {
		return llmjson.SchemaError("content required unless profile_updates has a non-empty update", nil)
	}
	switch Type(e.Type) {
	case TypeSummary, TypeFact, TypeEpisode, TypeInterest:
	default:
		return llmjson.SchemaError(fmt.Sprintf("invalid memory type %q", e.Type), nil)
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return llmjson.SchemaError(fmt.Sprintf("confidence %v out of range [0,1]", e.Confidence), nil)
	}
	if e.ImportanceScore != nil {
		if *e.ImportanceScore < 0 || *e.ImportanceScore > 1 {
			return llmjson.SchemaError(fmt.Sprintf("importance_score %v out of range [0,1]", *e.ImportanceScore), nil)
		}
	}
	if e.ProfileUpdates != nil {
		for k := range e.ProfileUpdates.Facts {
			if strings.TrimSpace(k) == "" {
				return llmjson.SchemaError("profile_updates.facts has empty key", nil)
			}
		}
		for k := range e.ProfileUpdates.Preferences {
			if strings.TrimSpace(k) == "" {
				return llmjson.SchemaError("profile_updates.preferences has empty key", nil)
			}
		}
	}
	return nil
}

func (c *Consolidator) updateProfileFromExtraction(profile *Profile, extracts []Extract) {
	var interestsAdded int
	var factsAdded int

	if profile.Facts == nil {
		profile.Facts = make(map[string]string)
	}
	if profile.Preferences == nil {
		profile.Preferences = make(map[string]string)
	}

	for _, extract := range extracts {
		// Structured profile updates (from LLM output) - supports ALL languages
		if extract.ProfileUpdates != nil {
			for _, t := range extract.ProfileUpdates.Traits {
				if !containsFold(profile.Traits, t) {
					profile.Traits = append(profile.Traits, t)
				}
			}
			for k, v := range extract.ProfileUpdates.Facts {
				profile.Facts[k] = v
				factsAdded++
			}
			for k, v := range extract.ProfileUpdates.Preferences {
				profile.Preferences[k] = v
			}
			for _, i := range extract.ProfileUpdates.Interests {
				if !containsFold(profile.Interests, i) {
					profile.Interests = append(profile.Interests, i)
					interestsAdded++
				}
			}
			continue
		}

		// Legacy fallback: fact key-value parsing (English-only, preserved for backward compat)
		switch extract.Type {
		case string(TypeFact):
			if facts := parseFactKeyValues(extract.Content); len(facts) > 0 {
				for key, value := range facts {
					profile.Facts[key] = value
					factsAdded++
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
				}
			}
		}
	}

	if factsAdded > 0 {
		logger.Infof("[consolidation] added %d facts to profile for user=%s", factsAdded, profile.UserID)
	}
	if interestsAdded > 0 {
		logger.Infof("[consolidation] added %d interests to profile for user=%s", interestsAdded, profile.UserID)
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

// trySplitOnSep returns ok=false on sep-absent OR empty-normalized so the
// caller can fall through to the next separator (do not switch to IndexAny).
func trySplitOnSep(content, sep string) (key, value string, ok bool) {
	idx := strings.Index(content, sep)
	if idx == -1 {
		return "", "", false
	}
	key = normalizeFactKey(content[:idx])
	value = normalizeFactValue(content[idx+1:])
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

func splitFactKeyValue(content string) (string, string, bool) {
	for _, sep := range []string{":", "="} {
		if key, value, ok := trySplitOnSep(content, sep); ok {
			return key, value, true
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
