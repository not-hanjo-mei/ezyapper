// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"fmt"
	"strings"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
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

// recentImageEnrichment describes when the most recent historical image may
// be described on demand (a vision call) while building history.
type recentImageEnrichment struct {
	// replyToID is the message the user replied to, if any. A reply only
	// justifies enrichment when it targets the image message itself.
	replyToID string
	// keyword is true when the user's text mentions an image.
	keyword bool
}

// planRecentImageEnrichment decides whether on-demand enrichment is allowed.
// Previously any reply enabled it, and the target was "the most recent image
// in history" rather than the replied-to message, so in hybrid mode nearly
// every reply re-described the same image.
func planRecentImageEnrichment(userContent, replyToID string) recentImageEnrichment {
	content := strings.ToLower(strings.TrimSpace(userContent))
	return recentImageEnrichment{
		replyToID: replyToID,
		keyword:   content != "" && containsImageKeyword(content),
	}
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
	if len(messages) == 0 || b.cfg().AI.Vision.Mode == config.VisionModeTextOnly {
		return messages
	}

	mostRecentImageIndex := -1
	for i, msg := range messages {
		if msg != nil && msg.ID != currentMsgID && len(msg.ImageURLs) > 0 {
			mostRecentImageIndex = i
			break // newest first
		}
	}
	visionDescriber := b.getVisionDescriber()

	out := make([]*types.DiscordMessage, len(messages))
	for i, msg := range messages {
		out[i] = msg
		if msg == nil || msg.ID == currentMsgID || len(msg.ImageURLs) == 0 {
			continue
		}

		descriptions := msg.ImageDescriptions
		if len(descriptions) == 0 {
			if cached, ok := b.getHistoricalImageDescriptions(msg.ID, msg.ImageURLs); ok {
				descriptions = cached
				logger.Debugf("[history] using bot-cache descriptions for message %s", msg.ID)
			}
		}
		if len(descriptions) == 0 && i == mostRecentImageIndex && enrichment.allows(msg.ID) && visionDescriber != nil {
			logger.Debugf("[history] performing on-demand enrichment for image message %s", msg.ID)
			fresh, err := visionDescriber.DescribeImages(ctx, msg.ImageURLs)
			if err != nil {
				logger.Warnf("[history] on-demand enrichment failed for message %s: %v", msg.ID, err)
			} else {
				descriptions = fresh
				b.setHistoricalImageDescriptions(msg.ID, msg.ImageURLs, descriptions)
			}
		}
		if len(descriptions) == 0 {
			continue
		}

		cp := *msg
		var sb strings.Builder
		sb.WriteString(cp.Content)
		for idx, desc := range descriptions {
			fmt.Fprintf(&sb, "\n[Image %d: %s]", idx+1, desc)
		}
		cp.Content = sb.String()
		out[i] = &cp
	}
	return out
}
