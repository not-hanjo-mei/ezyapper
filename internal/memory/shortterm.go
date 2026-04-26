package memory

import (
	"fmt"

	"ezyapper/internal/logger"
	"ezyapper/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// ShortTermClient provides access to Discord messages for short-term context
type ShortTermClient struct {
	session *discordgo.Session
}

// NewShortTermClient creates a new short-term context client
func NewShortTermClient(session *discordgo.Session) *ShortTermClient {
	return &ShortTermClient{session: session}
}

// validateLimit checks that limit is valid (>0) and warns if it's excessively large (>100).
func validateLimit(limit int, funcName string) error {
	if limit <= 0 {
		return fmt.Errorf("limit must be greater than 0, got: %d", limit)
	}
	if limit > 100 {
		logger.Warnf("[%s] large limit=%d may cause excessive API calls, consider reducing", funcName, limit)
	}
	return nil
}

// FetchRecentMessages fetches recent messages from a channel
func (c *ShortTermClient) FetchRecentMessages(channelID string, limit int) ([]*DiscordMessage, error) {
	if err := validateLimit(limit, "FetchRecentMessages"); err != nil {
		return nil, err
	}

	messages, err := c.session.ChannelMessages(channelID, limit, "", "", "")
	if err != nil {
		return nil, err
	}

	return c.convertMessages(messages), nil
}

// FetchUserMessages fetches messages from a specific user in a channel
func (c *ShortTermClient) FetchUserMessages(channelID string, userID string, limit int) ([]*DiscordMessage, error) {
	if err := validateLimit(limit, "FetchUserMessages"); err != nil {
		return nil, err
	}

	messages, err := c.session.ChannelMessages(channelID, limit, "", "", "")
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

// FetchChannelMessages fetches all messages from a channel (for batch consolidation)
func (c *ShortTermClient) FetchChannelMessages(channelID string, limit int) ([]*DiscordMessage, error) {
	if err := validateLimit(limit, "FetchChannelMessages"); err != nil {
		return nil, err
	}

	messages, err := c.session.ChannelMessages(channelID, limit, "", "", "")
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
