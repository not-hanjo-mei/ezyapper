// Package utils provides shared utility functions used across the codebase
package utils

import (
	"unicode/utf8"
)

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

	parts := []string{}
	for len(content) > 0 {
		if len(content) <= maxLen {
			parts = append(parts, content)
			break
		}

		cutAt := maxLen
		// Ensure we don't cut in the middle of a multi-byte UTF-8 sequence
		for cutAt > 0 && !utf8.RuneStart(content[cutAt]) {
			cutAt--
		}
		// If we backed up to position 0, find the end of the first complete character
		if cutAt == 0 {
			_, size := utf8.DecodeRuneInString(content)
			if size > 0 {
				cutAt = size
			} else {
				cutAt = maxLen
			}
		}

		// Try to find a word boundary (space) near the cut point
		spaceAt := -1
		for i := cutAt; i > 0; i-- {
			if content[i] == ' ' {
				spaceAt = i
				break
			}
		}
		if spaceAt > 0 {
			cutAt = spaceAt
		}

		parts = append(parts, content[:cutAt])
		content = content[cutAt:]
		if len(content) > 0 && content[0] == ' ' {
			content = content[1:]
		}
	}

	return parts
}
