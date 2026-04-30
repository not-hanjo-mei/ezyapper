// Package bot provides Discord bot event handlers
package bot

import (
	"regexp"
	"strings"
)

var channelMentionRe = regexp.MustCompile(`<#(\d+)>`)

// UserMapping represents a mapping from Discord user ID to username
type UserMapping struct {
	ID       string
	Username string
}

// ChannelMapping represents a mapping from Discord channel ID to channel name
type ChannelMapping struct {
	ID   string
	Name string
}

// ReplaceMentions replaces Discord mention IDs with readable names.
// Handles user mentions (<@ID>, <@!ID>) and channel mentions (<#ID>).
func ReplaceMentions(content string, userMappings []UserMapping, channelMappings []ChannelMapping) string {
	result := content

	for _, um := range userMappings {
		result = strings.ReplaceAll(result, "<@"+um.ID+">", "@"+um.Username)
		result = strings.ReplaceAll(result, "<@!"+um.ID+">", "@"+um.Username)
	}

	if len(channelMappings) > 0 {
		channelMap := make(map[string]string, len(channelMappings))
		for _, cm := range channelMappings {
			channelMap[cm.ID] = cm.Name
		}
		result = channelMentionRe.ReplaceAllStringFunc(result, func(match string) string {
			matches := channelMentionRe.FindStringSubmatch(match)
			if len(matches) < 2 {
				return match
			}
			if name, ok := channelMap[matches[1]]; ok {
				return "#" + name
			}
			return match
		})
	}

	return result
}
