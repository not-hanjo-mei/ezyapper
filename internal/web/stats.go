package web

import (
	"context"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
)

type memoryCounter interface {
	CountMemories(ctx context.Context) (int64, error)
}

type profileCounter interface {
	CountProfiles(ctx context.Context) (int64, error)
}

// StatsProvider aggregates dashboard statistics from memory stores.
type StatsProvider struct {
	memStore     memory.MemoryStore
	profileStore memory.ProfileStore
}

func NewStatsProvider(memStore memory.MemoryStore, profileStore memory.ProfileStore) *StatsProvider {
	return &StatsProvider{
		memStore:     memStore,
		profileStore: profileStore,
	}
}

// GetDashboardStats returns aggregated statistics. Each query has a 5-second
// timeout. Failed queries return 0 values without blocking the page render.
func (s *StatsProvider) GetDashboardStats(ctx context.Context) memory.GlobalStats {
	stats := memory.GlobalStats{}

	s.countMemories(ctx, &stats)
	s.countProfiles(ctx, &stats)
	s.countMessages(ctx, &stats)

	return stats
}

func (s *StatsProvider) countMemories(ctx context.Context, stats *memory.GlobalStats) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if mc, ok := s.memStore.(memoryCounter); ok {
		if count, err := mc.CountMemories(ctx); err == nil {
			stats.TotalMemories = count
			return
		} else {
			logger.Warnf("[StatsProvider] failed to count memories: %v", err)
		}
	}
	// Fallback: use GetStats if the store supports it
	if gs, err := s.memStore.GetStats(ctx); err == nil {
		stats.TotalMemories = gs.TotalMemories
	}
}

func (s *StatsProvider) countProfiles(ctx context.Context, stats *memory.GlobalStats) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if pc, ok := s.profileStore.(profileCounter); ok {
		if count, err := pc.CountProfiles(ctx); err == nil {
			stats.TotalUsers = count
			return
		} else {
			logger.Warnf("[StatsProvider] failed to count profiles: %v", err)
		}
	}
	// Fallback: try GetStats from MemoryStore
	if gs, err := s.memStore.GetStats(ctx); err == nil {
		stats.TotalUsers = gs.TotalUsers
	}
}

func (s *StatsProvider) countMessages(_ context.Context, stats *memory.GlobalStats) {
	// TotalMessages is not directly countable from Qdrant
	// Set to 0 with note: "N/A"
	stats.TotalMessages = 0
}
