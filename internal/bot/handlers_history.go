// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
	openai "github.com/sashabaranov/go-openai"
)

// UserInfo holds information about a user for mention purposes
type UserInfo struct {
	ID       string
	Username string
}

// formatMessageXML formats a single message in XML style with UserID and timestamp (UTC)
// If replyToUsername is set, appends an inline reply marker so the LLM sees "who replied to whom."
// Special case "(deleted message)" renders without @ prefix.
func formatMessageXML(username, userID, content string, timestamp time.Time, replyToUsername string) string {
	if replyToUsername != "" {
		if replyToUsername == "(deleted message)" {
			content = content + " (replying to deleted message)"
		} else {
			content = content + " (replying to @" + replyToUsername + ")"
		}
	}
	return fmt.Sprintf(`"%s"{UserID=%s,Time=%s}: "%s"`, username, userID, timestamp.UTC().Format(time.RFC3339), content)
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

	imageKeywords := []string{
		"image",
		"img",
		"photo",
		"picture",
		"pic",
		"screenshot",
	}

	for _, keyword := range imageKeywords {
		if strings.Contains(content, keyword) {
			return true
		}
	}

	return false
}

// buildConversationHistory builds message history from Discord messages
func (b *Bot) buildConversationHistory(ctx context.Context, messages []*types.DiscordMessage) []openai.ChatCompletionMessage {
	var result []openai.ChatCompletionMessage
	visionDescriber := b.getVisionDescriber()

	// Discord returns messages in reverse chronological order (newest first)
	// We need to reverse them to get chronological order for the AI
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		role := openai.ChatMessageRoleUser
		if msg.IsBot {
			role = openai.ChatMessageRoleAssistant
		}

		// If message has no images, add as text-only and continue
		if len(msg.ImageURLs) == 0 {
			result = append(result, openai.ChatCompletionMessage{
				Role:    role,
				Content: msg.Content,
			})
			continue
		}

		// Handle images based on vision mode
		switch b.cfg().AI.Vision.Mode {
		case config.VisionModeHybrid:
			content := msg.Content
			if visionDescriber != nil {
				var descriptions []string

				// Check if we have cached descriptions
				if len(msg.ImageDescriptions) > 0 {
					descriptions = msg.ImageDescriptions
					logger.Debugf("[vision] using cached image descriptions for message %s count=%d", msg.ID, len(descriptions))
				} else if cachedDescriptions, ok := b.getHistoricalImageDescriptions(msg.ID, msg.ImageURLs); ok {
					descriptions = cachedDescriptions
					msg.ImageDescriptions = cachedDescriptions
					logger.Debugf("[vision] using bot cache image descriptions for message %s count=%d", msg.ID, len(descriptions))
				} else {
					// Avoid blocking reply generation on cold-start history scans.
					// Historical images are enriched only when descriptions are already cached.
					logger.Debugf("[vision] skipping uncached historical image descriptions for message %s", msg.ID)
				}

				// Add descriptions to content
				for j, desc := range descriptions {
					if j < b.cfg().AI.Vision.MaxImages || b.cfg().AI.Vision.MaxImages == 0 {
						content = fmt.Sprintf("%s\n[Image %d: %s]", content, j+1, desc)
					}
				}
			}
			result = append(result, openai.ChatCompletionMessage{
				Role:    role,
				Content: content,
			})

		case config.VisionModeMultimodal:
			var parts []openai.ChatMessagePart
			if msg.Content != "" {
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeText,
					Text: msg.Content,
				})
			}
			for _, imgURL := range msg.ImageURLs {
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    imgURL,
						Detail: openai.ImageURLDetailAuto,
					},
				})
			}
			result = append(result, openai.ChatCompletionMessage{
				Role:         role,
				MultiContent: parts,
			})

		default:
			// Text-only mode or unknown mode: ignore images, keep text only
			result = append(result, openai.ChatCompletionMessage{
				Role:    role,
				Content: msg.Content,
			})
		}
	}

	return result
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

	// Track the most recent image message for on-demand enrichment
	var mostRecentImageIndex int = -1
	visionDescriber := b.getVisionDescriber()

	// Find the most recent image message (excluding current message)
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

	// Track AuthorID -> most recent Username for rename detection
	seenNames := make(map[string]string)

	// Process messages and build history
	for i, msg := range messages {
		// Skip the current message being processed
		if msg.ID == currentMsgID {
			continue
		}

		// Determine role - only mark current bot as "Assistant", everyone else is "User"
		role := "User"
		if msg.IsBot && msg.AuthorID == botID {
			role = "Assistant"
		}

		// Build content with optional image descriptions
		content := msg.Content

		// Handle images based on vision mode
		if len(msg.ImageURLs) > 0 && b.cfg().AI.Vision.Mode != config.VisionModeTextOnly {
			// Check if this is the most recent image message and on-demand enrichment is allowed
			isMostRecentImage := (i == mostRecentImageIndex && allowOnDemandRecentImageEnrichment)

			var descriptions []string
			haveCachedDescriptions := false

			// Try to get cached descriptions first
			if len(msg.ImageDescriptions) > 0 {
				descriptions = msg.ImageDescriptions
				haveCachedDescriptions = true
				logger.Debugf("[history] using memory-cached descriptions for message %s", msg.ID)
			} else if cached, ok := b.getHistoricalImageDescriptions(msg.ID, msg.ImageURLs); ok {
				descriptions = cached
				haveCachedDescriptions = true
				logger.Debugf("[history] using bot-cache descriptions for message %s", msg.ID)
			}

			// On-demand enrichment for most recent image only
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

			// Add image descriptions to content if available
			if haveCachedDescriptions {
				for j, desc := range descriptions {
					if j < b.cfg().AI.Vision.MaxImages || b.cfg().AI.Vision.MaxImages == 0 {
						content = fmt.Sprintf("%s\n[Image %d: %s]", content, j+1, desc)
					}
				}
			}
		}

		// Build inline reply marker if this message is a reply
		replyMarker := ""
		if msg.ReplyToID != "" {
			if msg.ReplyToUsername == "(deleted message)" {
				replyMarker = " (replying to deleted message)"
			} else {
				replyMarker = " (replying to @" + msg.ReplyToUsername + ")"
			}
		}

		// Detect rename: if AuthorID was seen before with a different Username
		renameMarker := ""
		if msg.AuthorID != botID && msg.Username != "(deleted message)" {
			if oldName, seen := seenNames[msg.AuthorID]; seen && oldName != msg.Username {
				renameMarker = " (was @" + oldName + ")"
			}
		}
		seenNames[msg.AuthorID] = msg.Username

		// Write formatted message with mention IDs replaced by readable usernames and channel names
		displayContent := ReplaceMentions(content, userMappings, channelMappings)
		result.WriteString(fmt.Sprintf("  [%s] %s (ID:%s): %s%s%s\n", role, msg.Username, msg.AuthorID, displayContent, renameMarker, replyMarker))
	}

	result.WriteString("</context>")

	return result.String()
}

// collectRecentUsers collects unique users from recent messages
func (b *Bot) collectRecentUsers(messages []*types.DiscordMessage) []UserInfo {
	seen := make(map[string]bool)
	var users []UserInfo

	for _, msg := range messages {
		if !seen[msg.AuthorID] {
			seen[msg.AuthorID] = true
			users = append(users, UserInfo{
				ID:       msg.AuthorID,
				Username: msg.Username,
			})
		}
	}

	return users
}
