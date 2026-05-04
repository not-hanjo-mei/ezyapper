package web

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"ezyapper/internal/config"
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
	cfgStore     *atomic.Value
}

func NewStatsProvider(memStore memory.MemoryStore, profileStore memory.ProfileStore, cfgStore *atomic.Value) *StatsProvider {
	return &StatsProvider{
		memStore:     memStore,
		profileStore: profileStore,
		cfgStore:     cfgStore,
	}
}

// GetDashboardStats returns aggregated statistics. Each query has a 5-second
// timeout. An error is returned if either the memory count or profile count
// sub-query fails — the caller should log the error and render the page with
// zero values to avoid blocking the user.
func (s *StatsProvider) GetDashboardStats(ctx context.Context) (memory.GlobalStats, error) {
	stats := memory.GlobalStats{}

	totalMemories, err := s.countMemories(ctx)
	if err != nil {
		return stats, fmt.Errorf("get dashboard stats: count memories: %w", err)
	}
	stats.TotalMemories = totalMemories

	totalUsers, err := s.countProfiles(ctx)
	if err != nil {
		return stats, fmt.Errorf("get dashboard stats: count profiles: %w", err)
	}
	stats.TotalUsers = totalUsers

	stats.TotalMessages = 0 // not directly countable from Qdrant

	return stats, nil
}

func (s *StatsProvider) countMemories(ctx context.Context) (int64, error) {
	cfg, ok := s.cfgStore.Load().(*config.Config)
	if !ok {
		return 0, fmt.Errorf("count memories: failed to load config")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Web.StatsQueryTimeoutSec)*time.Second)
	defer cancel()

	if mc, ok := s.memStore.(memoryCounter); ok {
		count, err := mc.CountMemories(ctx)
		if err == nil {
			return count, nil
		}
		logger.Warnf("[StatsProvider] failed to count memories: %v", err)
	}
	// Fallback: use GetStats if the store supports it
	gs, err := s.memStore.GetStats(ctx)
	if err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return gs.TotalMemories, nil
}

func (s *StatsProvider) countProfiles(ctx context.Context) (int64, error) {
	cfg, ok := s.cfgStore.Load().(*config.Config)
	if !ok {
		return 0, fmt.Errorf("count profiles: failed to load config")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Web.StatsQueryTimeoutSec)*time.Second)
	defer cancel()

	if pc, ok := s.profileStore.(profileCounter); ok {
		count, err := pc.CountProfiles(ctx)
		if err == nil {
			return count, nil
		}
		logger.Warnf("[StatsProvider] failed to count profiles: %v", err)
	}
	// Fallback: try GetStats from MemoryStore
	gs, err := s.memStore.GetStats(ctx)
	if err != nil {
		return 0, fmt.Errorf("count profiles: %w", err)
	}
	return gs.TotalUsers, nil
}
