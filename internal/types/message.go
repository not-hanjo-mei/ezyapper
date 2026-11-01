// Package types provides shared domain types used across the bot.
package types

import (
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// DiscordMessage represents a simplified Discord message for short-term context
// and plugin communication.
type DiscordMessage struct {
	ID                string   `json:"id"`
	ChannelID         string   `json:"channel_id"`
	GuildID           string   `json:"guild_id"`
	AuthorID          string   `json:"author_id"`
	Username          string   `json:"username"`
	DisplayName       string   `json:"display_name"`
	Content           string   `json:"content"`
	ImageURLs         []string `json:"image_urls,omitempty"`
	ImageDescriptions []string `json:"image_descriptions,omitempty"` // Cached image descriptions to avoid redundant API calls
	// Embeds holds the readable text of the message's embeds (bot embeds,
	// link previews). Content stays the raw message text; use FullText for
	// content plus embeds.
	Embeds []EmbedSummary `json:"embeds,omitempty"`
	// Kinds lists what the message carries (text, embed, image, sticker,
	// voice, ...); see MessageKinds. Used by message filters.
	Kinds     []string  `json:"kinds,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	IsBot     bool      `json:"is_bot"`

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
	displayName := m.Author.GlobalName
	if displayName == "" {
		displayName = m.Author.Username
	}

	msg := DiscordMessage{
		ID:          m.ID,
		ChannelID:   m.ChannelID,
		GuildID:     m.GuildID,
		AuthorID:    m.Author.ID,
		Username:    m.Author.Username,
		DisplayName: displayName,
		Content:     m.Content,
		ImageURLs:   extractImageURLsFromMessage(m.Message),
		Embeds:      SummarizeEmbeds(m.Embeds),
		Kinds:       MessageKinds(m.Message),
		Timestamp:   m.Timestamp,
		IsBot:       m.Author.Bot,
	}

	if m.MessageReference != nil {
		msg.ReplyToID = m.MessageReference.MessageID
		if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
			msg.ReplyToUsername = m.ReferencedMessage.Author.Username
			content := m.ReferencedMessage.Content
			msg.ReplyToContent = content
		} else {
			msg.ReplyToUsername = "(deleted message)"
		}
	}

	return msg
}

func extractImageURLsFromMessage(msg *discordgo.Message) []string {
	return ExtractImageURLs(msg, 0)
}

// ExtractImageURLs returns the image URLs a message carries, capped at
// maxImages when maxImages > 0. It includes image attachments, the main image
// of embeds, and the thumbnail of image/gifv embeds (where Discord puts the
// posted image itself). Thumbnails of link previews (article, link, video,
// rich) are skipped: they are decoration, and describing them made every link
// post cost a vision call.
func ExtractImageURLs(msg *discordgo.Message, maxImages int) []string {
	urls := []string{}
	if msg == nil {
		return urls
	}
	add := func(u string) bool {
		if u == "" {
			return false
		}
		urls = append(urls, u)
		return maxImages > 0 && len(urls) >= maxImages
	}

	for _, attachment := range msg.Attachments {
		if attachment != nil && strings.HasPrefix(attachment.ContentType, "image/") {
			if add(attachment.URL) {
				return urls
			}
		}
	}

	for _, embed := range msg.Embeds {
		if embed == nil {
			continue
		}
		if embed.Image != nil && add(embed.Image.URL) {
			return urls
		}
		if embed.Thumbnail != nil && isImageEmbed(embed) && add(embed.Thumbnail.URL) {
			return urls
		}
	}

	return urls
}

func isImageEmbed(e *discordgo.MessageEmbed) bool {
	return e.Type == discordgo.EmbedTypeImage || e.Type == discordgo.EmbedTypeGifv
}

// StableImageKey returns a cache key for an image URL that survives Discord
// CDN re-signing. Discord attachment URLs carry ex/is/hm query parameters
// that change every time the message is fetched, so the raw URL never hits a
// cache. For Discord CDN hosts the query string is dropped; other URLs are
// returned unchanged.
func StableImageKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	switch strings.ToLower(u.Host) {
	case "cdn.discordapp.com", "media.discordapp.net", "images-ext-1.discordapp.net", "images-ext-2.discordapp.net":
		return "discord-cdn:" + u.Path
	}
	return rawURL
}
