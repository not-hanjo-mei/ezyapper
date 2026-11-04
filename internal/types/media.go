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
	// Text is what the media modality turned the attachment into: an audio
	// transcript or description, or a video description. It is filled for
	// memory consolidation (see media.Processor.AnnotateForMemory); empty
	// when the attachment was not processed.
	Text string `json:"text,omitempty"`
	// Transcript reports that Text is a transcript (audio in asr mode)
	// rather than a description.
	Transcript bool `json:"transcript,omitempty"`
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

// Sticker format types (Discord sticker format_type).
const (
	StickerPNG    = 1
	StickerAPNG   = 2
	StickerLottie = 3
	StickerGIF    = 4
)

// StickerRef is a sticker sent with a message (Discord sticker item). Its
// description and tags need a separate lookup by ID.
type StickerRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FormatType int    `json:"format_type"`
}

// ExtractStickers returns the stickers of a message.
func ExtractStickers(msg *discordgo.Message) []StickerRef {
	if msg == nil {
		return nil
	}
	var out []StickerRef
	for _, s := range msg.StickerItems {
		if s == nil {
			continue
		}
		out = append(out, StickerRef{ID: s.ID, Name: s.Name, FormatType: int(s.FormatType)})
	}
	return out
}
