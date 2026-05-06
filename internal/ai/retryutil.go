// Package ai provides AI/LLM integration using OpenAI-compatible API
package ai

import "strings"

// IsRetryableError checks if an error should trigger a retry.
// Only 429 (rate limit), 5xx (server errors), and connection/timeout errors are retryable.
// 400, 401, 403, 404 and other client errors are not retried.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	// Rate limit errors
	if strings.Contains(errStr, "429") || strings.Contains(errStr, "too many requests") {
		return true
	}
	// Server errors
	if strings.Contains(errStr, "500") || strings.Contains(errStr, "502") || strings.Contains(errStr, "503") || strings.Contains(errStr, "504") {
		return true
	}
	// Connection errors
	if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "timeout") || strings.Contains(errStr, "context deadline exceeded") || strings.Contains(errStr, "eof") {
		return true
	}
	return false
}
