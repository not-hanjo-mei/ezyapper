package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/utils"
)

// MemoryStore groups memory CRUD, search, and statistics operations.
type MemoryStore interface {
	// Store stores a memory in the vector database
	Store(ctx context.Context, m *Record) error

	// Search performs semantic memory search
	Search(ctx context.Context, userID string, query string, opts *SearchOptions) ([]*Record, error)

	// HybridSearch performs hybrid search (semantic + keyword)
	HybridSearch(ctx context.Context, userID string, query string, keywords []string, opts *SearchOptions) ([]*Record, error)

	// GetMemories retrieves all memories for a user
	GetMemories(ctx context.Context, userID string, limit int) ([]*Record, error)

	// GetMemory retrieves a single memory by ID
	GetMemory(ctx context.Context, memoryID string) (*Record, error)

	// DeleteMemory deletes a single memory
	DeleteMemory(ctx context.Context, memoryID string) error

	// DeleteUserData deletes all data for a user
	DeleteUserData(ctx context.Context, userID string) error

	// GetStats retrieves global statistics
	GetStats(ctx context.Context) (*GlobalStats, error)
}

// ProfileStore groups user profile operations.
type ProfileStore interface {
	// GetProfile retrieves a user profile
	GetProfile(ctx context.Context, userID string) (*Profile, error)

	// UpdateProfile updates a user profile
	UpdateProfile(ctx context.Context, p *Profile) error

	// GetUserStats retrieves user statistics
	GetUserStats(ctx context.Context, userID string) (*UserStats, error)
}

// ConsolidationManager groups consolidation trigger and execution operations.
type ConsolidationManager interface {
	// Consolidate executes consolidation for a user
	Consolidate(ctx context.Context, userID string) error

	// ConsolidateWithMessages executes consolidation with provided messages
	ConsolidateWithMessages(ctx context.Context, userID string, messages []*DiscordMessage) error

	// ConsolidateChannel executes batch consolidation for all users in a channel
	ConsolidateChannel(ctx context.Context, channelID string, messages []*DiscordMessage) error
}

// Service defines the composite interface for long-term memory operations.
// It embeds MemoryStore, ProfileStore, and ConsolidationManager for consumers
// that need the full set of memory capabilities.
type Service interface {
	MemoryStore
	ProfileStore
	ConsolidationManager

	// IncrementMessageCount increments the message counter for consolidation triggering
	IncrementMessageCount(ctx context.Context, userID string) (int, error)

	// IncrementChannelMessageCount increments the channel message counter for consolidation triggering
	IncrementChannelMessageCount(ctx context.Context, channelID string) (int, error)

	// ResetMessageCount resets the message counter after consolidation
	ResetMessageCount(userID string)

	// ResetChannelMessageCount resets the channel message counter after consolidation
	ResetChannelMessageCount(channelID string)

	// ConsumeChannelMessageCount decreases the channel message counter after consolidation
	// and returns the remaining count.
	ConsumeChannelMessageCount(channelID string, consumed int) int

	// Close closes the service and its connections
	Close() error
}

// MemoryService implements Service (comprising MemoryStore, ProfileStore, and ConsolidationManager).
type MemoryService struct {
	qdrant       *QdrantClient
	consolidator *Consolidator
	embedder     Embedder
	config       *ServiceConfig

	// Message counters for consolidation triggering (user-level for backward compat)
	messageCounters map[string]int
	counterMu       sync.RWMutex

	// Channel message counters for batch consolidation
	channelCounters map[string]int

	// Consolidation interval from config
	consolidationInterval int
}

// ServiceConfig holds configuration parameters for the memory service.
type ServiceConfig struct {
	ConsolidationInterval int
	ShortTermLimit        int
	TopK                  int
	MinScore              float64
	Consolidation         *config.ConsolidationConfig
	OwnBotID              string // Bot's own Discord ID to distinguish from other bots
}

// Embedder defines the interface for generating embeddings
type Embedder interface {
	// Embed generates an embedding for the given text
	Embed(ctx context.Context, text string) ([]float32, error)
}

func NewService(cfg *ServiceConfig, qdrantClient *QdrantClient, embedder Embedder, aiClient *ai.Client, visionDescriber *ai.VisionDescriber) (*MemoryService, error) {
	if qdrantClient == nil {
		return nil, fmt.Errorf("qdrant client is required")
	}

	if embedder == nil {
		return nil, fmt.Errorf("embedder is required")
	}

	if cfg.ConsolidationInterval <= 0 {
		return nil, fmt.Errorf("consolidation interval must be greater than 0")
	}

	if cfg.ShortTermLimit <= 0 {
		return nil, fmt.Errorf("short term limit must be greater than 0")
	}

	if cfg.TopK < 0 {
		return nil, fmt.Errorf("top_k must be greater than or equal to 0")
	}

	if cfg.MinScore < 0 || cfg.MinScore > 1 {
		return nil, fmt.Errorf("min_score must be between 0 and 1")
	}

	if cfg.Consolidation == nil {
		return nil, fmt.Errorf("consolidation config is required")
	}

	service := &MemoryService{
		qdrant:                qdrantClient,
		embedder:              embedder,
		config:                cfg,
		messageCounters:       make(map[string]int),
		channelCounters:       make(map[string]int),
		consolidationInterval: cfg.ConsolidationInterval,
	}

	service.consolidator = NewConsolidator(qdrantClient, embedder, aiClient, visionDescriber, cfg.Consolidation, cfg.OwnBotID, cfg.ConsolidationInterval)

	logger.Info("Memory service initialized")
	return service, nil
}

func (s *MemoryService) Store(ctx context.Context, m *Record) error {
	if len(m.Embedding) == 0 {
		return fmt.Errorf("memory embedding is required (should be generated by consolidation)")
	}

	logger.Debugf("[MemoryService.Store] storing memory for userID=%s type=%s", m.UserID, m.MemoryType)
	if err := s.qdrant.UpsertMemory(ctx, m); err != nil {
		logger.Errorf("[MemoryService.Store] failed to store memory for userID=%s: %v", m.UserID, err)
		return err
	}
	logger.Debugf("[MemoryService.Store] successfully stored memoryID=%s for userID=%s", m.ID, m.UserID)
	return nil
}

// Search performs semantic memory search
func (s *MemoryService) Search(ctx context.Context, userID string, query string, opts *SearchOptions) ([]*Record, error) {
	if opts == nil {
		opts = &SearchOptions{
			TopK:     s.config.TopK,
			MinScore: s.config.MinScore,
		}
	}

	logger.Debugf("[MemoryService.Search] searching for userID=%s query=%.50s topK=%d", userID, query, opts.TopK)

	// Generate query embedding
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		logger.Errorf("[MemoryService.Search] failed to generate embedding for userID=%s: %v", userID, err)
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	memories, err := s.qdrant.SearchMemories(ctx, userID, embedding, opts)
	if err != nil {
		logger.Errorf("[MemoryService.Search] search failed for userID=%s: %v", userID, err)
		return nil, err
	}

	logger.Debugf("[MemoryService.Search] found %d memories for userID=%s", len(memories), userID)
	return memories, nil
}

// HybridSearch performs hybrid search (semantic + keyword)
func (s *MemoryService) HybridSearch(ctx context.Context, userID string, query string, keywords []string, opts *SearchOptions) ([]*Record, error) {
	if opts == nil {
		opts = &SearchOptions{
			TopK:     s.config.TopK,
			MinScore: s.config.MinScore,
		}
	}

	// Generate query embedding
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// First try semantic search
	memories, err := s.qdrant.SearchMemories(ctx, userID, embedding, opts)
	if err != nil {
		return nil, err
	}

	// If we have keywords, filter results
	if len(keywords) > 0 {
		memories = s.filterByKeywords(memories, keywords)
	}

	return memories, nil
}

// filterByKeywords filters memories by keywords
func (s *MemoryService) filterByKeywords(memories []*Record, keywords []string) []*Record {
	var filtered []*Record
	for _, m := range memories {
		for _, keyword := range keywords {
			if utils.Contains(m.Keywords, keyword) || strings.Contains(m.Content, keyword) {
				filtered = append(filtered, m)
				break
			}
		}
	}
	return filtered
}

// GetMemories retrieves all memories for a user
func (s *MemoryService) GetMemories(ctx context.Context, userID string, limit int) ([]*Record, error) {
	logger.Debugf("[MemoryService.GetMemories] retrieving memories for userID=%s limit=%d", userID, limit)
	memories, err := s.qdrant.GetMemoriesByUser(ctx, userID, limit)
	if err != nil {
		logger.Errorf("[MemoryService.GetMemories] failed to retrieve memories for userID=%s: %v", userID, err)
		return nil, err
	}
	logger.Debugf("[MemoryService.GetMemories] retrieved %d memories for userID=%s", len(memories), userID)
	return memories, nil
}

// GetMemory retrieves a single memory by ID
func (s *MemoryService) GetMemory(ctx context.Context, memoryID string) (*Record, error) {
	logger.Debugf("[MemoryService.GetMemory] retrieving memoryID=%s", memoryID)
	memory, err := s.qdrant.GetMemory(ctx, memoryID)
	if err != nil {
		logger.Errorf("[MemoryService.GetMemory] failed to retrieve memoryID=%s: %v", memoryID, err)
		return nil, err
	}
	logger.Debugf("[MemoryService.GetMemory] successfully retrieved memoryID=%s", memoryID)
	return memory, nil
}

// GetProfile retrieves a user profile
func (s *MemoryService) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	logger.Debugf("[MemoryService.GetProfile] retrieving profile for userID=%s", userID)
	profile, err := s.qdrant.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			logger.Debugf("[MemoryService.GetProfile] profile not found for userID=%s, creating default", userID)
			// Return default profile if not found
			return &Profile{
				UserID:       userID,
				Traits:       []string{},
				Facts:        make(map[string]string),
				Preferences:  make(map[string]string),
				Interests:    []string{},
				FirstSeenAt:  time.Now(),
				LastActiveAt: time.Now(),
			}, nil
		}

		logger.Errorf("[MemoryService.GetProfile] failed to retrieve profile for userID=%s: %v", userID, err)
		return nil, err
	}
	logger.Debugf("[MemoryService.GetProfile] retrieved profile for userID=%s messageCount=%d", userID, profile.MessageCount)
	return profile, nil
}

// UpdateProfile updates a user profile
func (s *MemoryService) UpdateProfile(ctx context.Context, p *Profile) error {
	logger.Debugf("[MemoryService.UpdateProfile] updating profile for userID=%s", p.UserID)
	if err := s.qdrant.UpsertProfile(ctx, p); err != nil {
		logger.Errorf("[MemoryService.UpdateProfile] failed to update profile for userID=%s: %v", p.UserID, err)
		return err
	}
	logger.Debugf("[MemoryService.UpdateProfile] successfully updated profile for userID=%s", p.UserID)
	return nil
}

// DeleteMemory deletes a single memory
func (s *MemoryService) DeleteMemory(ctx context.Context, memoryID string) error {
	logger.Warnf("[MemoryService.DeleteMemory] deleting memoryID=%s", memoryID)
	if err := s.qdrant.DeleteMemory(ctx, memoryID); err != nil {
		logger.Errorf("[MemoryService.DeleteMemory] failed to delete memoryID=%s: %v", memoryID, err)
		return err
	}
	logger.Infof("[MemoryService.DeleteMemory] successfully deleted memoryID=%s", memoryID)
	return nil
}

// DeleteUserData deletes all data for a user
func (s *MemoryService) DeleteUserData(ctx context.Context, userID string) error {
	logger.Warnf("[MemoryService.DeleteUserData] deleting all data for userID=%s", userID)

	// Delete all memories
	if err := s.qdrant.DeleteUserMemories(ctx, userID); err != nil {
		logger.Errorf("[MemoryService.DeleteUserData] failed to delete memories for userID=%s: %v", userID, err)
		return fmt.Errorf("failed to delete memories: %w", err)
	}

	// Delete profile
	if err := s.qdrant.DeleteProfile(ctx, userID); err != nil {
		logger.Errorf("[MemoryService.DeleteUserData] failed to delete profile for userID=%s: %v", userID, err)
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	// Clear message counter
	s.counterMu.Lock()
	delete(s.messageCounters, userID)
	s.counterMu.Unlock()

	logger.Infof("[MemoryService.DeleteUserData] successfully deleted all data for userID=%s", userID)
	return nil
}

// Consolidate executes consolidation for a user
func (s *MemoryService) Consolidate(ctx context.Context, userID string) error {
	logger.Infof("[MemoryService.Consolidate] starting consolidation for userID=%s", userID)
	if err := s.consolidator.Process(ctx, userID); err != nil {
		logger.Errorf("[MemoryService.Consolidate] consolidation failed for userID=%s: %v", userID, err)
		return err
	}
	logger.Infof("[MemoryService.Consolidate] completed consolidation for userID=%s", userID)
	return nil
}

// ConsolidateWithMessages executes consolidation with provided messages
func (s *MemoryService) ConsolidateWithMessages(ctx context.Context, userID string, messages []*DiscordMessage) error {
	logger.Infof("[MemoryService.ConsolidateWithMessages] starting consolidation for userID=%s with %d messages", userID, len(messages))
	if err := s.consolidator.ProcessWithMessages(ctx, userID, messages); err != nil {
		logger.Errorf("[MemoryService.ConsolidateWithMessages] consolidation failed for userID=%s: %v", userID, err)
		return err
	}
	logger.Infof("[MemoryService.ConsolidateWithMessages] completed consolidation for userID=%s", userID)
	return nil
}

// IncrementMessageCount increments the message counter and checks if consolidation should trigger
func (s *MemoryService) IncrementMessageCount(ctx context.Context, userID string) (int, error) {
	s.counterMu.Lock()
	defer s.counterMu.Unlock()

	s.messageCounters[userID]++
	count := s.messageCounters[userID]

	logger.Debugf("[MemoryService.IncrementMessageCount] userID=%s count=%d/%d", userID, count, s.consolidationInterval)

	// Update profile last active time
	profile, err := s.GetProfile(ctx, userID)
	if err != nil {
		logger.Warnf("[MemoryService.IncrementMessageCount] failed to get profile for userID=%s: %v", userID, err)
	} else {
		profile.LastActiveAt = time.Now()
		profile.MessageCount = count
		if err := s.UpdateProfile(ctx, profile); err != nil {
			logger.Warnf("[MemoryService.IncrementMessageCount] failed to update profile for userID=%s: %v", userID, err)
		}
	}

	return count, nil
}

// ShouldConsolidate checks if consolidation should be triggered for a user
func (s *MemoryService) ShouldConsolidate(userID string) bool {
	s.counterMu.RLock()
	defer s.counterMu.RUnlock()

	count, exists := s.messageCounters[userID]
	if !exists {
		return false
	}

	return count >= s.consolidationInterval
}

func (s *MemoryService) ResetMessageCount(userID string) {
	s.counterMu.Lock()
	defer s.counterMu.Unlock()
	s.messageCounters[userID] = 0
}

// IncrementChannelMessageCount increments the channel message counter
func (s *MemoryService) IncrementChannelMessageCount(ctx context.Context, channelID string) (int, error) {
	s.counterMu.Lock()
	defer s.counterMu.Unlock()

	s.channelCounters[channelID]++
	count := s.channelCounters[channelID]

	logger.Debugf("[MemoryService.IncrementChannelMessageCount] channelID=%s count=%d/%d", channelID, count, s.consolidationInterval)
	return count, nil
}

// ResetChannelMessageCount resets the channel message counter
func (s *MemoryService) ResetChannelMessageCount(channelID string) {
	s.counterMu.Lock()
	defer s.counterMu.Unlock()
	delete(s.channelCounters, channelID)
}

// ConsumeChannelMessageCount decreases the channel message counter and returns remaining count.
func (s *MemoryService) ConsumeChannelMessageCount(channelID string, consumed int) int {
	s.counterMu.Lock()
	defer s.counterMu.Unlock()

	if consumed <= 0 {
		return s.channelCounters[channelID]
	}

	current := s.channelCounters[channelID]
	remaining := current - consumed
	if remaining <= 0 {
		delete(s.channelCounters, channelID)
		return 0
	}

	s.channelCounters[channelID] = remaining
	return remaining
}

// ConsolidateChannel executes batch consolidation for all users in a channel
func (s *MemoryService) ConsolidateChannel(ctx context.Context, channelID string, messages []*DiscordMessage) error {
	logger.Infof("[MemoryService.ConsolidateChannel] starting batch consolidation for channel=%s with %d messages", channelID, len(messages))

	if err := s.consolidator.ProcessChannelMessages(ctx, channelID, messages); err != nil {
		logger.Errorf("[MemoryService.ConsolidateChannel] batch consolidation failed for channel=%s: %v", channelID, err)
		return err
	}

	logger.Infof("[MemoryService.ConsolidateChannel] completed batch consolidation for channel=%s", channelID)
	return nil
}

func (s *MemoryService) GetStats(ctx context.Context) (*GlobalStats, error) {
	memories, err := s.CountMemories(ctx)
	if err != nil {
		logger.Warnf("[MemoryService.GetStats] failed to count memories: %v", err)
	}
	users, err := s.CountProfiles(ctx)
	if err != nil {
		logger.Warnf("[MemoryService.GetStats] failed to count profiles: %v", err)
	}
	return &GlobalStats{
		TotalMemories:    memories,
		TotalUsers:       users,
		LastConsolidated: s.consolidator.LastConsolidatedAt(),
	}, nil
}

func (s *MemoryService) CountMemories(ctx context.Context) (int64, error) {
	count, err := s.qdrant.CountCollection(ctx, CollectionMemories)
	return int64(count), err
}

func (s *MemoryService) CountProfiles(ctx context.Context) (int64, error) {
	count, err := s.qdrant.CountCollection(ctx, CollectionProfiles)
	return int64(count), err
}

// GetUserStats retrieves user statistics
func (s *MemoryService) GetUserStats(ctx context.Context, userID string) (*UserStats, error) {
	logger.Debugf("[MemoryService.GetUserStats] retrieving stats for userID=%s", userID)

	profile, err := s.GetProfile(ctx, userID)
	if err != nil {
		logger.Errorf("[MemoryService.GetUserStats] failed to get profile for userID=%s: %v", userID, err)
		return nil, err
	}

	memoryLimit := s.config.TopK
	if memoryLimit <= 0 {
		memoryLimit = 50
	}

	memories, err := s.GetMemories(ctx, userID, memoryLimit)
	if err != nil {
		logger.Errorf("[MemoryService.GetUserStats] failed to get memories for userID=%s: %v", userID, err)
		return nil, err
	}

	stats := &UserStats{
		UserID:        userID,
		MessageCount:  profile.MessageCount,
		MemoryCount:   len(memories),
		FirstSeenAt:   profile.FirstSeenAt,
		LastActiveAt:  profile.LastActiveAt,
		LastSummaryAt: profile.LastConsolidatedAt,
	}

	logger.Debugf("[MemoryService.GetUserStats] userID=%s messageCount=%d memoryCount=%d", userID, stats.MessageCount, stats.MemoryCount)
	return stats, nil
}

// Close closes the service and its connections
func (s *MemoryService) Close() error {
	return s.qdrant.Close()
}
