// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"fmt"
	"strings"

	"ezyapper/internal/logger"
	"ezyapper/internal/media"
	"ezyapper/internal/types"
)

// UserInfo holds information about a user for mention purposes
type UserInfo struct {
	ID       string
	Username string
}

// imageKeywords is the read-only list of substrings that indicate a message
// is likely about an image. Callers must lowercase content before matching.
var imageKeywords = []string{
	"image",
	"img",
	"photo",
	"picture",
	"pic",
	"screenshot",
}

// containsImageKeyword reports whether content contains any image-related
// keyword substring. The caller is responsible for lowercasing content.
func containsImageKeyword(content string) bool {
	for _, keyword := range imageKeywords {
		if strings.Contains(content, keyword) {
			return true
		}
	}
	return false
}

// audioKeywords / videoKeywords play the role of imageKeywords for audio and
// video history enrichment.
var (
	audioKeywords = []string{"audio", "voice", "listen", "recording", "song"}
	videoKeywords = []string{"video", "clip"}
)

func containsAny(content string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(content, k) {
			return true
		}
	}
	return false
}

// recentImageEnrichment describes when the most recent historical image (or
// audio / video) may be processed on demand (a model call) while building
// history.
type recentImageEnrichment struct {
	// replyToID is the message the user replied to, if any. A reply only
	// justifies enrichment when it targets the media message itself.
	replyToID string
	// keyword is true when the user's text mentions an image.
	keyword bool
	// audioKeyword / videoKeyword: the text mentions audio / a video.
	audioKeyword, videoKeyword bool
}

// planRecentImageEnrichment decides whether on-demand enrichment is allowed.
// Previously any reply enabled it, and the target was "the most recent image
// in history" rather than the replied-to message, so in hybrid mode nearly
// every reply re-described the same image.
func planRecentImageEnrichment(userContent, replyToID string) recentImageEnrichment {
	content := strings.ToLower(strings.TrimSpace(userContent))
	return recentImageEnrichment{
		replyToID:    replyToID,
		keyword:      content != "" && containsImageKeyword(content),
		audioKeyword: content != "" && containsAny(content, audioKeywords),
		videoKeyword: content != "" && containsAny(content, videoKeywords),
	}
}

// allowsKind is allows for audio ("audio") and video ("video") messages.
func (p recentImageEnrichment) allowsKind(kind, messageID string) bool {
	if (kind == "audio" && p.audioKeyword) || (kind == "video" && p.videoKeyword) {
		return true
	}
	return p.replyToID != "" && p.replyToID == messageID
}

// allows reports whether the image message with the given ID may be enriched.
func (p recentImageEnrichment) allows(messageID string) bool {
	if p.keyword {
		return true
	}
	return p.replyToID != "" && p.replyToID == messageID
}

// enrichHistory returns copies of the history messages whose image
// descriptions are appended to their content as "[Image N: ...]" lines.
// Descriptions come from the message itself or the bot's description cache;
// at most one vision call is made, for the most recent image message, and
// only when the user replied to it or mentioned an image (see
// planRecentImageEnrichment). The input slice is not modified.
func (b *Bot) enrichHistory(ctx context.Context, messages []*types.DiscordMessage, currentMsgID string, enrichment recentImageEnrichment) []*types.DiscordMessage {
	mods := b.cfg().AI.Modalities
	if len(messages) == 0 || !mods.AnyEnabled() {
		return messages
	}
	proc := b.mediaProcessor()

	// Newest first: the first match is the most recent media message.
	mostRecentImageIndex, mostRecentAudioIndex, mostRecentVideoIndex := -1, -1, -1
	for i, msg := range messages {
		if msg == nil || msg.ID == currentMsgID {
			continue
		}
		if mostRecentImageIndex < 0 && len(msg.ImageURLs) > 0 {
			mostRecentImageIndex = i
		}
		for _, a := range msg.Media {
			if mostRecentAudioIndex < 0 && a.IsAudio() {
				mostRecentAudioIndex = i
			}
			if mostRecentVideoIndex < 0 && a.Kind == types.MediaVideo {
				mostRecentVideoIndex = i
			}
		}
	}
	var describer imageDescriber
	if mods.Image.Enabled {
		describer = b.imageDescriber()
	}

	out := make([]*types.DiscordMessage, len(messages))
	for i, msg := range messages {
		out[i] = msg
		if msg == nil || msg.ID == currentMsgID {
			continue
		}
		var extra strings.Builder

		if mods.Image.Enabled && len(msg.ImageURLs) > 0 {
			descriptions := msg.ImageDescriptions
			if len(descriptions) == 0 {
				if cached, ok := b.getHistoricalImageDescriptions(msg.ID, msg.ImageURLs); ok {
					descriptions = cached
					logger.Debugf("[history] using bot-cache descriptions for message %s", msg.ID)
				}
			}
			if len(descriptions) == 0 && i == mostRecentImageIndex && enrichment.allows(msg.ID) && describer != nil {
				logger.Debugf("[history] performing on-demand enrichment for image message %s", msg.ID)
				fresh, err := describer.DescribeImages(ctx, msg.ImageURLs)
				if err != nil {
					logger.Warnf("[history] on-demand enrichment failed for message %s: %v", msg.ID, err)
				} else {
					descriptions = fresh
					b.setHistoricalImageDescriptions(msg.ID, msg.ImageURLs, descriptions)
				}
			}
			for idx, desc := range descriptions {
				fmt.Fprintf(&extra, "\n[Image %d: %s]", idx+1, desc)
			}
		}

		if proc != nil && len(msg.Stickers) > 0 {
			extra.WriteString(proc.AnnotateStickers(ctx, msg, false))
		}
		if proc != nil && len(msg.Media) > 0 {
			ann := proc.AnnotateAV(ctx, msg, media.AnnotateOptions{
				ComputeAudio: i == mostRecentAudioIndex && enrichment.allowsKind("audio", msg.ID),
				ComputeVideo: i == mostRecentVideoIndex && enrichment.allowsKind("video", msg.ID),
			})
			extra.WriteString(ann.Text)
		}

		if extra.Len() == 0 {
			continue
		}
		cp := *msg
		cp.Content += extra.String()
		out[i] = &cp
	}
	return out
}
