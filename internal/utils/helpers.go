// Package utils provides shared utility functions used across the codebase
package utils

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// ExtractImageURLs extracts image URLs from a Discord message
// This consolidates the duplicate implementations from handlers.go and shortterm.go
func ExtractImageURLs(msg *discordgo.Message) []string {
	var urls []string

	// From attachments
	for _, attachment := range msg.Attachments {
		if strings.HasPrefix(attachment.ContentType, "image/") {
			urls = append(urls, attachment.URL)
		}
	}

	// From embeds
	for _, embed := range msg.Embeds {
		if embed.Image != nil && embed.Image.URL != "" {
			urls = append(urls, embed.Image.URL)
		}
		if embed.Thumbnail != nil && embed.Thumbnail.URL != "" {
			urls = append(urls, embed.Thumbnail.URL)
		}
	}

	return urls
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

// ContainsString checks if a string contains a substring (case-sensitive)
func ContainsString(s, substr string) bool {
	return strings.Contains(s, substr)
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

// RemoveFromSlice removes an item from a string slice
func RemoveFromSlice(slice []string, item string) []string {
	for i, v := range slice {
		if v == item {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

// UserMapping represents a mapping from Discord user ID to username
type UserMapping struct {
	ID       string
	Username string
}

// ReplaceMentions replaces Discord mention IDs with readable usernames
// Discord format: <@USER_ID> or <@!USER_ID> (for nickname mentions)
// This function converts: <@123456> -> @Username
func ReplaceMentions(content string, userMappings []UserMapping) string {
	result := content
	for _, um := range userMappings {
		// Replace both regular mention <@ID> and nickname mention <@!ID>
		result = strings.ReplaceAll(result, "<@"+um.ID+">", "@"+um.Username)
		result = strings.ReplaceAll(result, "<@!"+um.ID+">", "@"+um.Username)
	}
	return result
}
