package types

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Message kinds describe what a message carries. A message usually has
// several (a reply with an image from a bot is reply+image+bot).
const (
	KindText    = "text"    // non-empty text content
	KindEmbed   = "embed"   // at least one embed
	KindImage   = "image"   // image attachment or image embed
	KindSticker = "sticker" // at least one sticker
	KindAudio   = "audio"   // audio attachment (not a voice message)
	KindVoice   = "voice"   // Discord voice message
	KindVideo   = "video"   // video attachment
	KindFile    = "file"    // any other attachment
	KindReply   = "reply"   // replies to another message
	KindBot     = "bot"     // author is a bot
	KindWebhook = "webhook" // sent by a webhook
)

// AllKinds lists every valid kind, for config validation.
var AllKinds = []string{KindText, KindEmbed, KindImage, KindSticker, KindAudio, KindVoice, KindVideo, KindFile, KindReply, KindBot, KindWebhook}

// MessageKinds derives the kinds of a Discord message.
func MessageKinds(m *discordgo.Message) []string {
	if m == nil {
		return nil
	}
	set := map[string]bool{}
	if strings.TrimSpace(m.Content) != "" {
		set[KindText] = true
	}
	if len(m.Embeds) > 0 {
		set[KindEmbed] = true
	}
	for _, e := range m.Embeds {
		if e != nil && (e.Type == discordgo.EmbedTypeImage || e.Type == discordgo.EmbedTypeGifv || e.Image != nil) {
			set[KindImage] = true
		}
	}
	if len(m.StickerItems) > 0 {
		set[KindSticker] = true
	}
	isVoice := m.Flags&discordgo.MessageFlagsIsVoiceMessage != 0
	for _, a := range m.Attachments {
		if a == nil {
			continue
		}
		ct := strings.ToLower(a.ContentType)
		switch {
		case strings.HasPrefix(ct, "image/"):
			set[KindImage] = true
		case strings.HasPrefix(ct, "audio/") && isVoice:
			set[KindVoice] = true
		case strings.HasPrefix(ct, "audio/"):
			set[KindAudio] = true
		case strings.HasPrefix(ct, "video/"):
			set[KindVideo] = true
		default:
			set[KindFile] = true
		}
	}
	if m.MessageReference != nil && m.MessageReference.MessageID != "" {
		set[KindReply] = true
	}
	if m.Author != nil && m.Author.Bot {
		set[KindBot] = true
	}
	if m.WebhookID != "" {
		set[KindWebhook] = true
	}
	out := make([]string, 0, len(set))
	for _, k := range AllKinds {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}
