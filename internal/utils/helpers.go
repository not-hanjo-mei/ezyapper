// Package utils provides shared utility functions used across the codebase
package utils

import (
	"regexp"
	"strings"

	"ezyapper/internal/types"
)

var channelMentionRe = regexp.MustCompile(`<#(\d+)>`)

// ExtractImageURLs returns the image URLs from a DiscordMessage.
func ExtractImageURLs(msg *types.DiscordMessage) []string {
	return msg.ImageURLs
}

// Contains checks if a string slice contains a specific item
func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// SplitMessage splits a long message into chunks at word boundaries
// This is used for Discord's 2000 character limit
func SplitMessage(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}

	var parts []string
	for len(content) > 0 {
		if len(content) <= maxLen {
			parts = append(parts, content)
			break
		}

		cutAt := maxLen
		for i := maxLen; i > 0; i-- {
			if content[i] == ' ' {
				cutAt = i
				break
			}
		}

		parts = append(parts, content[:cutAt])
		content = content[cutAt:]
		if len(content) > 0 && content[0] == ' ' {
			content = content[1:]
		}
	}

	return parts
}

// RemoveFromSlice removes an item from a string slice without mutating the input.
func RemoveFromSlice(slice []string, item string) []string {
	for i, v := range slice {
		if v == item {
			result := make([]string, 0, len(slice)-1)
			result = append(result, slice[:i]...)
			return append(result, slice[i+1:]...)
		}
	}
	return slice
}

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
