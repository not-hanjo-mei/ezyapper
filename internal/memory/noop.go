package memory

import (
	"context"
	"fmt"
	"time"
)

// NewNoopService returns a memory service implementation that performs no external IO.
func NewNoopService() Service {
	return &NoopService{}
}

// NoopService disables long-term memory behaviors while keeping API compatibility.
type NoopService struct{}

func (s *NoopService) Store(ctx context.Context, m *Record) error { return nil }

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
		UserID:       userID,
		Traits:       []string{},
		Facts:        map[string]string{},
		Preferences:  map[string]string{},
		Interests:    []string{},
		FirstSeenAt:  time.Now(),
		LastActiveAt: time.Now(),
	}, nil
}

func (s *NoopService) UpdateProfile(ctx context.Context, p *Profile) error { return nil }

func (s *NoopService) DeleteMemory(ctx context.Context, memoryID string) error { return nil }

func (s *NoopService) DeleteUserData(ctx context.Context, userID string) error { return nil }

func (s *NoopService) Consolidate(ctx context.Context, userID string) error { return nil }

func (s *NoopService) ConsolidateWithMessages(ctx context.Context, userID string, messages []*DiscordMessage) error {
	return nil
}

func (s *NoopService) ConsolidateChannel(ctx context.Context, channelID string, messages []*DiscordMessage) error {
	return nil
}

func (s *NoopService) IncrementMessageCount(ctx context.Context, userID string) (int, error) {
	return 0, nil
}

func (s *NoopService) IncrementChannelMessageCount(ctx context.Context, channelID string) (int, error) {
	return 0, nil
}

func (s *NoopService) ResetMessageCount(userID string) {}

func (s *NoopService) ResetChannelMessageCount(channelID string) {}

func (s *NoopService) ConsumeChannelMessageCount(channelID string, consumed int) int {
	return 0
}

func (s *NoopService) GetStats(ctx context.Context) (*GlobalStats, error) {
	return &GlobalStats{LastConsolidated: time.Now()}, nil
}

func (s *NoopService) GetUserStats(ctx context.Context, userID string) (*UserStats, error) {
	now := time.Now()
	return &UserStats{UserID: userID, FirstSeenAt: now, LastActiveAt: now}, nil
}

func (s *NoopService) Close() error { return nil }
