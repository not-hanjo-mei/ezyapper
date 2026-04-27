package memory

import (
	"context"
	"fmt"

	"ezyapper/internal/logger"
	"ezyapper/internal/utils"

	"github.com/bwmarrin/discordgo"
)

const (
	// discordAPIMaxLimit is Discord's hard limit for messages per API call.
	discordAPIMaxLimit = 100
	// maxPaginatedLimit caps the total across all pages to prevent abuse.
	maxPaginatedLimit = 500
)

// ShortTermClient provides access to Discord messages for short-term context
type ShortTermClient struct {
	session *discordgo.Session
}

// NewShortTermClient creates a new short-term context client
func NewShortTermClient(session *discordgo.Session) *ShortTermClient {
	return &ShortTermClient{session: session}
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

// fetchPaginated fetches up to totalLimit messages using pagination (Discord API max 100/call).
func (c *ShortTermClient) fetchPaginated(ctx context.Context, channelID string, totalLimit int) ([]*discordgo.Message, error) {
	var all []*discordgo.Message
	var beforeID string
	remaining := totalLimit

	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		batchSize := remaining
		if batchSize > discordAPIMaxLimit {
			batchSize = discordAPIMaxLimit
		}

		batch, err := c.session.ChannelMessages(channelID, batchSize, beforeID, "", "")
		if err != nil {
			return nil, err
		}

		all = append(all, batch...)
		remaining -= len(batch)

		if len(batch) < batchSize {
			break
		}
		if len(batch) > 0 {
			beforeID = batch[len(batch)-1].ID
		}
	}

	return all, nil
}

// FetchRecentMessages fetches recent messages from a channel (paginated if >100).
func (c *ShortTermClient) FetchRecentMessages(ctx context.Context, channelID string, limit int) ([]*DiscordMessage, error) {
	limit, err := validateLimit(limit, "FetchRecentMessages")
	if err != nil {
		return nil, err
	}

	messages, err := c.fetchPaginated(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}

	return c.convertMessages(messages), nil
}

// FetchUserMessages fetches messages from a specific user in a channel (paginated if >100).
func (c *ShortTermClient) FetchUserMessages(ctx context.Context, channelID string, userID string, limit int) ([]*DiscordMessage, error) {
	limit, err := validateLimit(limit, "FetchUserMessages")
	if err != nil {
		return nil, err
	}

	messages, err := c.fetchPaginated(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}

	var userMessages []*DiscordMessage
	for _, msg := range messages {
		if msg.Author.ID == userID {
			userMessages = append(userMessages, c.convertMessage(msg))
		}
	}

	return userMessages, nil
}

// FetchChannelMessages fetches all messages from a channel for batch consolidation (paginated if >100).
func (c *ShortTermClient) FetchChannelMessages(ctx context.Context, channelID string, limit int) ([]*DiscordMessage, error) {
	limit, err := validateLimit(limit, "FetchChannelMessages")
	if err != nil {
		return nil, err
	}

	messages, err := c.fetchPaginated(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}

	return c.convertMessages(messages), nil
}

// convertMessages converts discordgo messages to our format
func (c *ShortTermClient) convertMessages(messages []*discordgo.Message) []*DiscordMessage {
	result := make([]*DiscordMessage, len(messages))
	for i, msg := range messages {
		result[i] = c.convertMessage(msg)
	}
	return result
}

// convertMessage converts a single discordgo message
func (c *ShortTermClient) convertMessage(msg *discordgo.Message) *DiscordMessage {
	d := &DiscordMessage{
		ID:        msg.ID,
		ChannelID: msg.ChannelID,
		GuildID:   msg.GuildID,
		AuthorID:  msg.Author.ID,
		Username:  msg.Author.Username,
		Content:   msg.Content,
		ImageURLs: utils.ExtractImageURLs(msg),
		Timestamp: msg.Timestamp,
		IsBot:     msg.Author.Bot,
	}

	if msg.MessageReference != nil {
		d.ReplyToID = msg.MessageReference.MessageID
		if msg.ReferencedMessage != nil && msg.ReferencedMessage.Author != nil {
			d.ReplyToUsername = msg.ReferencedMessage.Author.Username
			content := msg.ReferencedMessage.Content
			if len(content) > 100 {
				content = content[:100]
			}
			d.ReplyToContent = content
		} else {
			d.ReplyToUsername = "(deleted message)"
		}
	}

	return d
}
