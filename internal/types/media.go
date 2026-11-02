package types

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Media attachment kinds (see MediaAttachment.Kind).
const (
	MediaAudio = "audio" // audio file attachment
	MediaVoice = "voice" // Discord voice message (audio/ogg, opus)
	MediaVideo = "video" // video attachment
)

// MediaAttachment is an audio, voice or video attachment of a message.
// Images are kept in DiscordMessage.ImageURLs. The attachment ID is stable
// (unlike the signed CDN URL), so it is the cache key for transcripts and
// descriptions.
type MediaAttachment struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	URL         string  `json:"url"`
	Filename    string  `json:"filename,omitempty"`
	ContentType string  `json:"content_type,omitempty"`
	Size        int     `json:"size,omitempty"`
	DurationSec float64 `json:"duration_sec,omitempty"`
}

// IsAudio reports whether the attachment is an audio file or voice message.
func (a MediaAttachment) IsAudio() bool { return a.Kind == MediaAudio || a.Kind == MediaVoice }

// ExtractMedia returns the audio, voice and video attachments of a message,
// in attachment order.
func ExtractMedia(msg *discordgo.Message) []MediaAttachment {
	if msg == nil {
		return nil
	}
	isVoice := msg.Flags&discordgo.MessageFlagsIsVoiceMessage != 0
	var out []MediaAttachment
	for _, a := range msg.Attachments {
		if a == nil {
			continue
		}
		ct := strings.ToLower(a.ContentType)
		kind := ""
		switch {
		case strings.HasPrefix(ct, "audio/") && isVoice:
			kind = MediaVoice
		case strings.HasPrefix(ct, "audio/"):
			kind = MediaAudio
		case strings.HasPrefix(ct, "video/"):
			kind = MediaVideo
		default:
			continue
		}
		out = append(out, MediaAttachment{
			ID:          a.ID,
			Kind:        kind,
			URL:         a.URL,
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Size:        a.Size,
			DurationSec: a.DurationSecs,
		})
	}
	return out
}
