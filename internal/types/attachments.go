package types

import (
	"fmt"
	"strings"
)

// MediaStatus says whether a media item was turned into text.
type MediaStatus string

const (
	// MediaOK: Text holds the description or transcript.
	MediaOK MediaStatus = "ok"
	// MediaFailed: processing was attempted and failed.
	MediaFailed MediaStatus = "failed"
	// MediaPending: no result is cached and the policy did not allow
	// computing one (history media nobody asked about).
	MediaPending MediaStatus = "pending"
)

// TextKind says what ResolvedMedia.Text is.
type TextKind string

const (
	TextDescription TextKind = "description"
	TextTranscript  TextKind = "transcript"
)

// Kinds of ResolvedMedia (audio and voice share the "Audio N" numbering).
const (
	ResolvedImage   = "image"
	ResolvedSticker = "sticker"
	ResolvedAudio   = MediaAudio
	ResolvedVoice   = MediaVoice
	ResolvedVideo   = MediaVideo
)

// ResolvedMedia is one media item of a message (image, sticker, audio,
// voice message or video) as text. media.Processor.Resolve fills it; the
// message's Content is never changed, so speech and media text stay apart.
type ResolvedMedia struct {
	Kind string
	// Label names the item: "voice message, 7s", a filename, a sticker
	// name. Images have none.
	Label    string
	Text     string
	TextKind TextKind
	Status   MediaStatus
}

// AttachmentStyle selects how RenderAttachments formats a message's media.
type AttachmentStyle int

const (
	// StylePrompt is one indented line for the reply prompt, placed under
	// the message line and outside its quoted text:
	//
	//	  attachments: [Image 1: ...] [Audio 1 transcript (voice message, 7s): ...]
	StylePrompt AttachmentStyle = iota + 1
	// StyleMemory is one indented "[Attached ...]" line per item, for the
	// memory consolidation conversation.
	StyleMemory
	// StyleQuery is the plain text of the processed items, one per line,
	// for the memory search query.
	StyleQuery
)

// AttachmentsPrefix starts the StylePrompt line.
const AttachmentsPrefix = "attachments:"

// unprocessedText marks an item whose processing failed.
const unprocessedText = "(could not be processed)"

// RenderAttachments renders the resolved media of m in style. It returns ""
// when there is nothing to show. Images, audio and video are numbered per
// kind (voice messages count as audio).
func RenderAttachments(m *DiscordMessage, style AttachmentStyle) string {
	if m == nil {
		return ""
	}
	var items []string
	n := map[string]int{}
	for _, it := range m.ResolvedMedia {
		group := it.Kind
		if group == ResolvedVoice {
			group = ResolvedAudio
		}
		if group != ResolvedSticker {
			n[group]++
		}
		if s := renderItem(it, n[group], style); s != "" {
			items = append(items, s)
		}
	}
	if len(items) == 0 {
		return ""
	}
	switch style {
	case StylePrompt:
		return "  " + AttachmentsPrefix + " " + strings.Join(items, " ")
	case StyleMemory:
		return "  " + strings.Join(items, "\n  ")
	default:
		return strings.Join(items, "\n")
	}
}

func renderItem(it ResolvedMedia, num int, style AttachmentStyle) string {
	text := oneLineText(it.Text)
	switch it.Status {
	case MediaFailed:
		text = unprocessedText
	case MediaOK:
		if text == "" && it.Kind != ResolvedSticker {
			return ""
		}
	}
	if style == StyleQuery {
		if it.Status != MediaOK || text == "" {
			return ""
		}
		return text
	}
	if style == StyleMemory {
		if it.Status == MediaPending {
			return ""
		}
		if it.Kind == ResolvedSticker {
			return "[Attached " + stickerBody(it.Label, text) + "]"
		}
		return "[Attached " + numberedHead(it, num) + ": " + text + "]"
	}

	switch {
	case it.Kind == ResolvedSticker:
		return "[" + stickerBody(it.Label, text) + "]"
	case it.Status == MediaPending && it.Label == "":
		return fmt.Sprintf("[%s %d]", kindName(it.Kind), num)
	case it.Status == MediaPending:
		return fmt.Sprintf("[%s %d: %s]", kindName(it.Kind), num, it.Label)
	}
	head := numberedHead(it, num)
	if it.Label != "" {
		head += " (" + it.Label + ")"
	}
	return "[" + head + ": " + text + "]"
}

// numberedHead is "Image 1", "Video 2" or "Audio 1 transcript".
func numberedHead(it ResolvedMedia, num int) string {
	head := fmt.Sprintf("%s %d", kindName(it.Kind), num)
	if it.TextKind == TextTranscript && it.Status == MediaOK {
		head += " transcript"
	}
	return head
}

func stickerBody(name, text string) string {
	if text == "" {
		return "Sticker: " + name
	}
	return "Sticker: " + name + " — " + text
}

func kindName(kind string) string {
	switch kind {
	case ResolvedImage:
		return "Image"
	case ResolvedAudio, ResolvedVoice:
		return "Audio"
	case ResolvedVideo:
		return "Video"
	case ResolvedSticker:
		return "Sticker"
	}
	return "Attachment"
}

// oneLineText collapses whitespace so a transcript stays on its line.
func oneLineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
