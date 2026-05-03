// Package utils provides shared utility functions used across the codebase
package utils

import (
	"ezyapper/internal/types"
)

func ExtractImageURLs(msg *types.DiscordMessage) []string {
	return msg.ImageURLs
}

func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// SplitMessage splits at word boundaries for Discord's 2000 character limit.
func SplitMessage(content string, maxLen int) []string {
	if maxLen <= 0 {
		return []string{content}
	}
	if len(content) <= maxLen {
		return []string{content}
	}

	var parts []string
	for len(content) > 0 {
		if len(content) <= maxLen {
			parts = append(parts, content)
			break
		}

		// Find a safe cut point that doesn't split multi-byte UTF-8 characters
		cutAt := maxLen
		// Ensure we don't cut in the middle of a UTF-8 sequence
		for cutAt > 0 && (content[cutAt]&0xC0) == 0x80 {
			cutAt--
		}
		// If we're at a multi-byte start, check if we need to go back further
		if cutAt > 0 && content[cutAt]&0x80 != 0 {
			// We're at a multi-byte character start, try to find a space before
			for i := cutAt; i > 0; i-- {
				if content[i] == ' ' {
					cutAt = i
					break
				}
			}
		} else {
			// We're at a single-byte character, try to find a space
			for i := cutAt; i > 0; i-- {
				if content[i] == ' ' {
					cutAt = i
					break
				}
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
	// Return a copy to prevent aliasing issues
	result := make([]string, len(slice))
	copy(result, slice)
	return result
}
