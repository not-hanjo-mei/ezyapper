package bot

import (
	"fmt"
	"strings"

	"ezyapper/internal/logger"
	"ezyapper/internal/memory"

	"github.com/bwmarrin/discordgo"
)

// truncateString truncates a string to a maximum length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Try to truncate at a word boundary.
	if maxLen > 3 {
		for i := maxLen - 3; i > 0; i-- {
			if s[i] == ' ' {
				return s[:i] + "..."
			}
		}
	}
	return s[:maxLen-3] + "..."
}

// buildDynamicContext builds the dynamic user context that changes each request.
// This content is appended to the user message rather than the system prompt
// to preserve prompt caching benefits.
func (b *Bot) buildDynamicContext(authorName string, profile *memory.Profile, memories []*memory.Memory, recentMessages []*memory.DiscordMessage) string {
	var context strings.Builder

	// User identification is included in <currentMessage> XML format.
	// No need to repeat here.

	// Add display name header.
	if profile.DisplayName != "" {
		context.WriteString(fmt.Sprintf("User profile for @%s:\n", profile.DisplayName))
	} else {
		context.WriteString("User profile:\n")
	}

	// Add profile information.
	first := true
	if len(profile.Traits) > 0 {
		context.WriteString(fmt.Sprintf("User traits: %s", strings.Join(profile.Traits, ", ")))
		first = false
	}
	if len(profile.Facts) > 0 {
		var facts []string
		for k, v := range profile.Facts {
			facts = append(facts, fmt.Sprintf("%s: %s", k, v))
		}
		if !first {
			context.WriteString("\n")
		}
		context.WriteString(fmt.Sprintf("User facts: %s", strings.Join(facts, ", ")))
		first = false
	}
	if len(profile.Preferences) > 0 {
		var prefs []string
		for k, v := range profile.Preferences {
			prefs = append(prefs, fmt.Sprintf("%s: %s", k, v))
		}
		if !first {
			context.WriteString("\n")
		}
		context.WriteString(fmt.Sprintf("User preferences: %s", strings.Join(prefs, ", ")))
	}

	// Add relevant memories.
	if len(memories) > 0 {
		context.WriteString("\n\nRelevant context from previous conversations:")
		for _, mem := range memories {
			context.WriteString(fmt.Sprintf("\n- %s", mem.Summary))
		}
		logger.Debugf("[memory] added %d memories to dynamic context", len(memories))
	}

	return context.String()
}

func shouldSendGenerationFallback(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "context deadline exceeded")
}

func (b *Bot) addGenerationFailureReaction(s *discordgo.Session, m *discordgo.MessageCreate) {
	if s == nil || m == nil {
		return
	}

	const timeoutReaction = "💀"
	if err := s.MessageReactionAdd(m.ChannelID, m.ID, timeoutReaction); err != nil {
		logger.Warnf("Failed to add generation timeout reaction: %v", err)
	}
}
