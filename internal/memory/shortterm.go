package memory

import (
	"context"
	"fmt"
	"sync/atomic"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

// MessageFetcher abstracts Discord message retrieval so the memory package
// does not depend on Discord-specific types. Implementations are provided
// by the bot package which holds the Discord session.
type MessageFetcher interface {
	// FetchMessages retrieves messages from a channel, handling pagination
	// and conversion to the canonical types.DiscordMessage format.
	FetchMessages(ctx context.Context, channelID string, limit int) ([]types.DiscordMessage, error)
}

// ShortTermClient provides access to Discord messages for short-term context.
// It delegates actual API calls to a MessageFetcher implementation.
type ShortTermClient struct {
	fetcher MessageFetcher
	// maxPaginatedLimit is atomic so config hot reload can change it.
	maxPaginatedLimit atomic.Int64
}

// NewShortTermClient creates a new short-term context client.
func NewShortTermClient(fetcher MessageFetcher, maxPaginatedLimit int) *ShortTermClient {
	c := &ShortTermClient{fetcher: fetcher}
	c.maxPaginatedLimit.Store(int64(maxPaginatedLimit))
	return c
}

// SetMaxPaginatedLimit changes the recommended maximum fetch size
// (memory_pipeline.memory.max_paginated_limit) at runtime.
func (c *ShortTermClient) SetMaxPaginatedLimit(n int) {
	c.maxPaginatedLimit.Store(int64(n))
}

// validateLimit validates the requested limit and caps it.
func validateLimit(limit int, maxPaginatedLimit int, funcName string) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("limit must be greater than 0, got: %d", limit)
	}
	if limit > maxPaginatedLimit {
		logger.Warnf("[%s] limit=%d exceeds recommended max of %d, honoring user value", funcName, limit, maxPaginatedLimit)
		return limit, nil
	}
	return limit, nil
}

// FetchRecentMessages fetches recent messages from a channel.
func (c *ShortTermClient) FetchRecentMessages(ctx context.Context, channelID string, limit int) ([]*DiscordMessage, error) {
	limit, err := validateLimit(limit, int(c.maxPaginatedLimit.Load()), "FetchRecentMessages")
	if err != nil {
		return nil, err
	}

	messages, err := c.fetcher.FetchMessages(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}

	result := make([]*DiscordMessage, len(messages))
	for i := range messages {
		result[i] = &messages[i]
	}
	return result, nil
}

// FetchChannelMessages fetches all messages from a channel for batch consolidation.
func (c *ShortTermClient) FetchChannelMessages(ctx context.Context, channelID string, limit int) ([]*DiscordMessage, error) {
	limit, err := validateLimit(limit, int(c.maxPaginatedLimit.Load()), "FetchChannelMessages")
	if err != nil {
		return nil, err
	}

	messages, err := c.fetcher.FetchMessages(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}

	result := make([]*DiscordMessage, len(messages))
	for i := range messages {
		result[i] = &messages[i]
	}
	return result, nil
}
