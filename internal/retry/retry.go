// Package retry provides generic retry logic with exponential backoff and jitter.
package retry

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"ezyapper/internal/logger"
)

// retryConfig holds all configurable retry parameters.
type retryConfig struct {
	baseDelay              time.Duration
	maxDelay               time.Duration
	maxRetriesOverride     int // -1 means not set (use maxRetries param)
	ignoreDeadlineExceeded bool
	errorClassifier        func(error) bool // returns true if error is retryable
}

func defaultConfig() retryConfig {
	return retryConfig{
		baseDelay:          1 * time.Second,
		maxDelay:           30 * time.Second,
		maxRetriesOverride: -1,
	}
}

// RetryOption configures the retry behavior.
type RetryOption func(*retryConfig)

// WithBaseDelay sets the base delay for exponential backoff. Default: 1s.
func WithBaseDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) { c.baseDelay = d }
}

// WithMaxDelay sets the maximum delay cap for exponential backoff. Default: 30s.
func WithMaxDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) { c.maxDelay = d }
}

// WithMaxRetries overrides the maxRetries parameter passed to Retry.
func WithMaxRetries(n int) RetryOption {
	return func(c *retryConfig) { c.maxRetriesOverride = n }
}

// WithIgnoreDeadlineExceeded instructs the retry loop to continue even when
// the context's deadline is exceeded. Use when the operation itself manages
// per-attempt timeouts and the parent deadline should not abort retries.
func WithIgnoreDeadlineExceeded() RetryOption {
	return func(c *retryConfig) { c.ignoreDeadlineExceeded = true }
}

// WithErrorClassifier sets a function that determines whether an error is
// retryable. When set, only errors for which the classifier returns true are
// retried; all others abort immediately. When nil (default), all errors are
// considered retryable.
func WithErrorClassifier(fn func(error) bool) RetryOption {
	return func(c *retryConfig) { c.errorClassifier = fn }
}

// Retry executes fn with exponential backoff retry on failure.
// Generate UUIDs or other idempotency keys BEFORE calling Retry to prevent
// duplicate records on retry.
//
// maxRetries is the maximum number of retries (not counting the initial
// attempt). Total attempts = maxRetries + 1.
//
// Backoff: delay = min(2^attempt * baseDelay, maxDelay) with uniform ±25% jitter.
func Retry[T any](ctx context.Context, maxRetries int, fn func(context.Context) (T, error), opts ...RetryOption) (T, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxRetriesOverride >= 0 {
		maxRetries = cfg.maxRetriesOverride
	}

	var zero T
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Check context before each attempt. DeadlineExceeded may be ignored
		// if the caller opts in (matching the existing decision.go behavior).
		if ctxErr := ctx.Err(); ctxErr != nil {
			if !(cfg.ignoreDeadlineExceeded && ctxErr == context.DeadlineExceeded) {
				return zero, fmt.Errorf("retry cancelled before attempt %d: %w", attempt+1, ctxErr)
			}
		}

		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Use error classifier to determine if this error is retryable.
		if cfg.errorClassifier != nil && !cfg.errorClassifier(err) {
			return zero, fmt.Errorf("non-retryable error: %w", err)
		}

		if attempt == maxRetries {
			return zero, fmt.Errorf("retry exhausted after %d attempts: %w", maxRetries+1, lastErr)
		}

		delay := computeDelay(attempt, cfg.baseDelay, cfg.maxDelay)
		logger.Debugf("[retry] attempt %d/%d failed, retrying in %s: %v", attempt+1, maxRetries+1, delay, lastErr)

		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			ctxErr := ctx.Err()
			if !(cfg.ignoreDeadlineExceeded && ctxErr == context.DeadlineExceeded) {
				return zero, fmt.Errorf("retry cancelled: %w", ctxErr)
			}
		}
	}

	// Guard: timeout/cancellation should have been caught in the loop.
	// This return is unreachable — kept to satisfy the compiler.
	return zero, fmt.Errorf("retry exhausted after %d attempts: %w", maxRetries+1, lastErr)
}

// computeDelay calculates exponential backoff with ±25% uniform jitter.
// delay = min(2^attempt * baseDelay, maxDelay)
// Uses overflow-safe computation to prevent negative delays at high attempt counts.
func computeDelay(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	// Cap attempt to prevent integer overflow in bit shift
	// Use int64(1) to avoid undefined behavior on 32-bit platforms where int is 32 bits
	exp := min(uint(attempt), 62)
	// Overflow-safe: compute shift value, then compare with maxDelay/baseDelay
	// before multiplying to prevent int64 overflow.
	shift := time.Duration(int64(1) << exp)
	var delay time.Duration
	if shift > maxDelay || baseDelay > 0 && shift > maxDelay/baseDelay {
		delay = maxDelay
	} else {
		delay = min(shift*baseDelay, maxDelay)
	}
	jitter := time.Duration(float64(delay) * 0.25 * (2.0*rand.Float64() - 1.0))
	return delay + jitter
}
