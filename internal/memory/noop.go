package memory

import (
	"context"
	"fmt"

	"ezyapper/internal/logger"
)

// NewNoopService returns a memory service implementation that performs no external IO.
func NewNoopService() Service {
	return &NoopService{}
}

// NoopService disables long-term memory behaviors while keeping API compatibility.
type NoopService struct{}

func (s *NoopService) Store(ctx context.Context, m *Record) error {
	logger.Warnf("[memory] noop: Store called — memory disabled, data not stored")
	return nil
}

func (s *NoopService) Search(ctx context.Context, userID string, query string, opts *SearchOptions) ([]*Record, error) {
	return []*Record{}, nil
}

func (s *NoopService) HybridSearch(ctx context.Context, userID string, query string, keywords []string, opts *SearchOptions) ([]*Record, error) {
	return []*Record{}, nil
}

func (s *NoopService) GetMemories(ctx context.Context, userID string, limit int) ([]*Record, error) {
	return []*Record{}, nil
}

func (s *NoopService) GetMemory(ctx context.Context, memoryID string) (*Record, error) {
	return nil, fmt.Errorf("memory not found")
}

func (s *NoopService) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	return &Profile{
		UserID:      userID,
		Traits:      []string{},
		Facts:       map[string]string{},
		Preferences: map[string]string{},
		Interests:   []string{},
	}, nil
}

func (s *NoopService) UpdateProfile(ctx context.Context, p *Profile) error {
	logger.Warnf("[memory] noop: UpdateProfile called — memory disabled, profile not stored")
	return nil
}

func (s *NoopService) DeleteMemory(ctx context.Context, memoryID string) error {
	logger.Warnf("[memory] noop: DeleteMemory called — memory disabled, no-op")
	return nil
}

func (s *NoopService) DeleteUserData(ctx context.Context, userID string) error {
	logger.Warnf("[memory] noop: DeleteUserData called — memory disabled, no-op")
	return nil
}

func (s *NoopService) SearchByMentionedUser(ctx context.Context, userID string, mentionedUserID string, opts *SearchOptions) ([]*Record, error) {
	return []*Record{}, nil
}

func (s *NoopService) SearchByChannel(ctx context.Context, channelID string, opts *SearchOptions) ([]*Record, error) {
	return []*Record{}, nil
}

func (s *NoopService) GetRelationships(ctx context.Context, userID string, relType RelationshipType) ([]*Relationship, error) {
	return []*Relationship{}, nil
}

func (s *NoopService) GetRelationshipBetween(ctx context.Context, userA, userB string, relType RelationshipType) (*Relationship, error) {
	return nil, nil
}

func (s *NoopService) ConsolidateWithMessages(ctx context.Context, userID string, messages []*DiscordMessage) error {
	logger.Warnf("[memory] noop: ConsolidateWithMessages called — memory disabled, consolidation skipped")
	return nil
}

func (s *NoopService) ConsolidateChannel(ctx context.Context, channelID string, messages []*DiscordMessage) error {
	logger.Warnf("[memory] noop: ConsolidateChannel called — memory disabled, consolidation skipped")
	return nil
}

func (s *NoopService) IncrementMessageCount(ctx context.Context, userID string) (int, error) {
	logger.Warnf("[memory] noop: IncrementMessageCount called — memory disabled, counter not incremented")
	return 0, nil
}

func (s *NoopService) IncrementChannelMessageCount(ctx context.Context, channelID string) (int, error) {
	logger.Warnf("[memory] noop: IncrementChannelMessageCount called — memory disabled, counter not incremented")
	return 0, nil
}

func (s *NoopService) ConsumeChannelMessageCount(channelID string, consumed int) int {
	return 0
}

func (s *NoopService) GetStats(ctx context.Context) (*GlobalStats, error) {
	return &GlobalStats{}, nil
}

func (s *NoopService) StartMaintenance(ctx context.Context) error {
	logger.Warnf("[memory] noop: StartMaintenance called — memory disabled, maintenance not started")
	return nil
}

func (s *NoopService) StopMaintenance() {}

func (s *NoopService) Close() error { return nil }
