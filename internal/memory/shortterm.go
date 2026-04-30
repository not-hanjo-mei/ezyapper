package memory

import (
	"context"
	"fmt"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

const (
	// maxPaginatedLimit caps the total across all pages to prevent abuse.
	maxPaginatedLimit = 500
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
}

// NewShortTermClient creates a new short-term context client.
func NewShortTermClient(fetcher MessageFetcher) *ShortTermClient {
	return &ShortTermClient{fetcher: fetcher}
}

// validateLimit validates the requested limit and caps it.
func validateLimit(limit int, funcName string) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("limit must be greater than 0, got: %d", limit)
	}
	if limit > maxPaginatedLimit {
		logger.Warnf("[%s] limit=%d exceeds max of %d, capping", funcName, limit, maxPaginatedLimit)
		return maxPaginatedLimit, nil
	}
	return limit, nil
}

// FetchRecentMessages fetches recent messages from a channel.
func (c *ShortTermClient) FetchRecentMessages(ctx context.Context, channelID string, limit int) ([]*DiscordMessage, error) {
	limit, err := validateLimit(limit, "FetchRecentMessages")
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

// FetchUserMessages fetches messages from a specific user in a channel.
func (c *ShortTermClient) FetchUserMessages(ctx context.Context, channelID string, userID string, limit int) ([]*DiscordMessage, error) {
	limit, err := validateLimit(limit, "FetchUserMessages")
	if err != nil {
		return nil, err
	}

	messages, err := c.fetcher.FetchMessages(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}

	var userMessages []*DiscordMessage
	for i := range messages {
		if messages[i].AuthorID == userID {
			userMessages = append(userMessages, &messages[i])
		}
	}

	return userMessages, nil
}

// FetchChannelMessages fetches all messages from a channel for batch consolidation.
func (c *ShortTermClient) FetchChannelMessages(ctx context.Context, channelID string, limit int) ([]*DiscordMessage, error) {
	limit, err := validateLimit(limit, "FetchChannelMessages")
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
