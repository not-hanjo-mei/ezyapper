// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
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

// formatMessageXML formats a single message in unified style:
//
//	displayname (username, ID:id): "content"
//	displayname (username, ID:id): "content" (replying to @parent: "parent content")
//	displayname (username, ID:id): "content" (replying to deleted message)
//
// Reply marker is placed OUTSIDE the content quotes.
func formatMessageXML(displayName, username, userID, content string, timestamp time.Time, replyToUsername, replyToContent string) string {
	base := fmt.Sprintf(`[%s] %s (%s, ID:%s): "%s"`, timestamp.UTC().Format(time.RFC3339), displayName, username, userID, html.EscapeString(content))

	if replyToUsername == "" {
		return base
	}

	if replyToUsername == "(deleted message)" {
		return base + " (replying to deleted message)"
	}

	if replyToContent != "" {
		return base + fmt.Sprintf(` (replying to @%s: "%s")`, replyToUsername, html.EscapeString(replyToContent))
	}

	return base + fmt.Sprintf(" (replying to @%s)", replyToUsername)
}

// shouldEnrichRecentHistoricalImages checks if recent historical images should be enriched
// based on message content and whether the user is replying to a previous message.
func shouldEnrichRecentHistoricalImages(userContent string, hasReference bool) bool {
	if userContent == "" && !hasReference {
		return false
	}

	// Replying to a previous message usually means the user is referring to it.
	if hasReference {
		return true
	}

	content := strings.ToLower(strings.TrimSpace(userContent))
	if content == "" {
		return false
	}

	return containsImageKeyword(content)
}

// buildConversationHistoryText builds formatted conversation history text from Discord messages
// Returns XML formatted <context> section with previous messages
// Filters out the current message being processed and marks only the current bot as "Assistant"
// In hybrid mode, primarily uses cached historical image descriptions; optionally performs
// tightly bounded on-demand enrichment for the most recent image message.
func (b *Bot) buildConversationHistoryText(ctx context.Context, messages []*types.DiscordMessage, currentMsgID, botID string, allowOnDemandRecentImageEnrichment bool, mentions []*discordgo.User, channelMappings []ChannelMapping) string {
	if len(messages) == 0 {
		return ""
	}

	var result strings.Builder
	result.WriteString("<context>\n")

	var mostRecentImageIndex int = -1
	visionDescriber := b.getVisionDescriber()

	for i, msg := range messages {
		if msg.ID == currentMsgID {
			continue
		}
		if len(msg.ImageURLs) > 0 {
			mostRecentImageIndex = i
			break // Found the most recent
		}
	}

	// Build user mappings from history + current message mentions for ReplaceMentions.
	// Current message mentions take precedence over history usernames.
	historyUsers := b.collectRecentUsers(messages)
	userMappings := make([]UserMapping, 0, len(historyUsers))
	userIdx := make(map[string]int)

	for _, u := range historyUsers {
		userIdx[u.ID] = len(userMappings)
		userMappings = append(userMappings, UserMapping{ID: u.ID, Username: u.Username})
	}

	for _, mention := range mentions {
		if idx, ok := userIdx[mention.ID]; ok {
			userMappings[idx].Username = mention.Username
		} else {
			userIdx[mention.ID] = len(userMappings)
			userMappings = append(userMappings, UserMapping{ID: mention.ID, Username: mention.Username})
		}
	}

	seenNames := make(map[string]string)

	// Process messages in reverse (Discord returns newest first, we need oldest first)
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.ID == currentMsgID {
			continue
		}

		// Determine role: Assistant (self), Bot (other bots), User (humans)
		role := "User"
		if msg.IsBot {
			if msg.AuthorID == botID {
				role = "Assistant"
			} else {
				role = "Bot"
			}
		}

		content := msg.Content

		if len(msg.ImageURLs) > 0 && b.cfg().AI.Vision.Mode != config.VisionModeTextOnly {
			isMostRecentImage := (i == mostRecentImageIndex && allowOnDemandRecentImageEnrichment)

			descriptions := []string{}
			var haveCachedDescriptions bool

			if len(msg.ImageDescriptions) > 0 {
				descriptions = msg.ImageDescriptions
				haveCachedDescriptions = true
				logger.Debugf("[history] using memory-cached descriptions for message %s", msg.ID)
			} else if cached, ok := b.getHistoricalImageDescriptions(msg.ID, msg.ImageURLs); ok {
				descriptions = cached
				haveCachedDescriptions = true
				logger.Debugf("[history] using bot-cache descriptions for message %s", msg.ID)
			}

			if isMostRecentImage && !haveCachedDescriptions && visionDescriber != nil {
				logger.Debugf("[history] performing on-demand enrichment for most recent image message %s", msg.ID)
				freshDescriptions, err := visionDescriber.DescribeImages(ctx, msg.ImageURLs)
				if err == nil {
					descriptions = freshDescriptions
					// Cache for future use
					b.setHistoricalImageDescriptions(msg.ID, msg.ImageURLs, descriptions)
					haveCachedDescriptions = true
				} else {
					logger.Warnf("[history] on-demand enrichment failed for message %s: %v", msg.ID, err)
				}
			}

			if haveCachedDescriptions {
				var sb strings.Builder
				sb.WriteString(content)
				for idx, desc := range descriptions {
					fmt.Fprintf(&sb, "\n[Image %d: %s]", idx+1, desc)
				}
				content = sb.String()
			}
		}

		var replyMarker string
		if msg.ReplyToID != "" {
			if msg.ReplyToUsername == "(deleted message)" {
				replyMarker = " (replying to deleted message)"
			} else if msg.ReplyToContent != "" {
				replyMarker = fmt.Sprintf(" (replying to @%s: %q)", msg.ReplyToUsername, msg.ReplyToContent)
			} else {
				replyMarker = fmt.Sprintf(" (replying to @%s)", msg.ReplyToUsername)
			}
		}

		var renameMarker string
		if msg.AuthorID != botID && msg.Username != "(deleted message)" {
			if oldName, seen := seenNames[msg.AuthorID]; seen && oldName != msg.Username {
				renameMarker = " (was @" + oldName + ")"
			}
		}
		seenNames[msg.AuthorID] = msg.Username

		displayContent := ReplaceMentions(content, userMappings, channelMappings)
		timeStr := msg.Timestamp.UTC().Format(time.RFC3339)
		displayName := msg.DisplayName
		if displayName == "" {
			displayName = msg.Username
		}
		fmt.Fprintf(&result, "[%s] [%s] %s (%s, ID:%s): \"%s\"%s%s\n", timeStr, role, displayName, msg.Username, msg.AuthorID, displayContent, renameMarker, replyMarker)
	}

	result.WriteString("</context>")

	return result.String()
}

// collectRecentUsers collects unique users from recent messages
func (b *Bot) collectRecentUsers(messages []*types.DiscordMessage) []UserInfo {
	seen := make(map[string]struct{})
	users := make([]UserInfo, 0, len(messages))

	for _, msg := range messages {
		if _, ok := seen[msg.AuthorID]; !ok {
			seen[msg.AuthorID] = struct{}{}
			users = append(users, UserInfo{
				ID:       msg.AuthorID,
				Username: msg.Username,
			})
		}
	}

	return users
}
