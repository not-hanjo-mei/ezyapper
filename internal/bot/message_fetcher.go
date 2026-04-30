package bot

import (
	"context"
	"strings"

	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

const discordAPIMaxLimit = 100

// DiscordMessageFetcher implements memory.MessageFetcher using the Discord session.
type DiscordMessageFetcher struct {
	session *discordgo.Session
}

// NewDiscordMessageFetcher creates a new Discord-backed message fetcher.
func NewDiscordMessageFetcher(session *discordgo.Session) *DiscordMessageFetcher {
	return &DiscordMessageFetcher{session: session}
}

// FetchMessages implements memory.MessageFetcher by paginating the Discord API
// and converting results to the canonical types.DiscordMessage.
func (f *DiscordMessageFetcher) FetchMessages(ctx context.Context, channelID string, limit int) ([]types.DiscordMessage, error) {
	messages, err := f.fetchPaginated(ctx, channelID, limit)
	if err != nil {
		return nil, err
	}
	return f.convertMessages(messages), nil
}

func (f *DiscordMessageFetcher) fetchPaginated(ctx context.Context, channelID string, totalLimit int) ([]*discordgo.Message, error) {
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

		batch, err := f.session.ChannelMessages(channelID, batchSize, beforeID, "", "")
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

func (f *DiscordMessageFetcher) convertMessages(messages []*discordgo.Message) []types.DiscordMessage {
	result := make([]types.DiscordMessage, len(messages))
	for i, msg := range messages {
		result[i] = f.convertMessage(msg)
	}
	return result
}

func (f *DiscordMessageFetcher) convertMessage(msg *discordgo.Message) types.DiscordMessage {
	d := types.DiscordMessage{
		ID:        msg.ID,
		ChannelID: msg.ChannelID,
		GuildID:   msg.GuildID,
		AuthorID:  msg.Author.ID,
		Username:  msg.Author.Username,
		Content:   msg.Content,
		ImageURLs: extractImageURLs(msg),
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

func extractImageURLs(msg *discordgo.Message) []string {
	var urls []string

	for _, attachment := range msg.Attachments {
		if strings.HasPrefix(attachment.ContentType, "image/") {
			urls = append(urls, attachment.URL)
		}
	}

	for _, embed := range msg.Embeds {
		if embed.Image != nil && embed.Image.URL != "" {
			urls = append(urls, embed.Image.URL)
		}
		if embed.Thumbnail != nil && embed.Thumbnail.URL != "" {
			urls = append(urls, embed.Thumbnail.URL)
		}
	}

	return urls
}
