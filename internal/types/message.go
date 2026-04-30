// Package types provides shared domain types used across the bot.
package types

import (
	"time"

	"ezyapper/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// DiscordMessage represents a simplified Discord message for short-term context
// and plugin communication.
type DiscordMessage struct {
	ID                string    `json:"id"`
	ChannelID         string    `json:"channel_id"`
	GuildID           string    `json:"guild_id"`
	AuthorID          string    `json:"author_id"`
	Username          string    `json:"username"`
	Content           string    `json:"content"`
	ImageURLs         []string  `json:"image_urls,omitempty"`
	ImageDescriptions []string  `json:"image_descriptions,omitempty"` // Cached image descriptions to avoid redundant API calls
	Timestamp         time.Time `json:"timestamp"`
	IsBot             bool      `json:"is_bot"`

	// ReplyToID is the ID of the message being replied to (from MessageReference)
	ReplyToID string `json:"reply_to_id"`
	// ReplyToUsername is the username of the author of the replied-to message
	ReplyToUsername string `json:"reply_to_username"`
	// ReplyToContent is the content of the replied-to message
	ReplyToContent string `json:"reply_to_content"`
}

// FromDiscordgo converts a discordgo.MessageCreate to a DiscordMessage,
// filling all canonical fields including reply metadata and image URLs.
func FromDiscordgo(m *discordgo.MessageCreate) DiscordMessage {
	msg := DiscordMessage{
		ID:        m.ID,
		ChannelID: m.ChannelID,
		GuildID:   m.GuildID,
		AuthorID:  m.Author.ID,
		Username:  m.Author.Username,
		Content:   m.Content,
		ImageURLs: utils.ExtractImageURLs(m.Message),
		Timestamp: m.Timestamp,
		IsBot:     m.Author.Bot,
	}

	if m.MessageReference != nil {
		msg.ReplyToID = m.MessageReference.MessageID
		if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
			msg.ReplyToUsername = m.ReferencedMessage.Author.Username
			content := m.ReferencedMessage.Content
			if len(content) > 100 {
				content = content[:100]
			}
			msg.ReplyToContent = content
		} else {
			msg.ReplyToUsername = "(deleted message)"
		}
	}

	return msg
}
