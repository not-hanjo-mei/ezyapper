// Package utils provides shared utility functions used across the codebase
package utils

import (
	"ezyapper/internal/types"
)

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
