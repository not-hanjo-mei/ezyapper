package retry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestRetry_Success verifies that a successful call returns on the first attempt.
func TestRetry_Success(t *testing.T) {
	ctx := context.Background()
	result, err := Retry(ctx, 3, func(ctx context.Context) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected 'ok', got %q", result)
	}
}

// TestRetry_SuccessAfterRetries verifies that a transient failure is retried and eventually succeeds.
func TestRetry_SuccessAfterRetries(t *testing.T) {
	attempts := 0
	ctx := context.Background()
	result, err := Retry(ctx, 3, func(ctx context.Context) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("transient error")
		}
		return "recovered", nil
	}, WithBaseDelay(1*time.Millisecond))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("expected 'recovered', got %q", result)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

// TestRetry_MaxRetriesExceeded verifies that exhausting all retries returns an error.
func TestRetry_MaxRetriesExceeded(t *testing.T) {
	attempts := 0
	ctx := context.Background()
	_, err := Retry(ctx, 2, func(ctx context.Context) (int, error) {
		attempts++
		return 0, errors.New("persistent error")
	}, WithBaseDelay(1*time.Millisecond))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("error should mention 'exhausted', got: %v", err)
	}
	// maxRetries=2 means 1 initial attempt + 2 retries = 3 total
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

// TestRetry_ContextCancelled verifies that cancelling the context during a retry delay aborts immediately.
func TestRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay so ctx.Done() fires during the backoff wait.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := Retry(ctx, 3, func(ctx context.Context) (int, error) {
		return 0, errors.New("transient error")
	}, WithBaseDelay(200*time.Millisecond))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error should mention 'cancelled', got: %v", err)
	}
}

// TestRetry_ExponentialBackoff verifies that delays increase monotonically with each retry.
func TestRetry_ExponentialBackoff(t *testing.T) {
	var delays []time.Duration
	var lastTime time.Time
	attempts := 0

	ctx := context.Background()
	_, _ = Retry(ctx, 4, func(ctx context.Context) (int, error) {
		attempts++
		if !lastTime.IsZero() {
			delays = append(delays, time.Since(lastTime))
		}
		lastTime = time.Now()
		return 0, errors.New("transient error")
	}, WithBaseDelay(50*time.Millisecond))

	// Verify each delay is longer than the previous (monotonic increase is guaranteed
	// with ±25% jitter because (1.25 * 50) < (0.75 * 100) even in worst case).
	for i := 1; i < len(delays); i++ {
		if delays[i] < delays[i-1] {
			t.Errorf("delay %d (%v) should be greater than delay %d (%v)",
				i, delays[i], i-1, delays[i-1])
		}
	}
}

// TestRetry_NonRetryableError verifies that errors classified as non-retryable abort immediately.
func TestRetry_NonRetryableError(t *testing.T) {
	attempts := 0
	ctx := context.Background()
	_, err := Retry(ctx, 5, func(ctx context.Context) (string, error) {
		attempts++
		return "", errors.New("permanent error")
	}, WithErrorClassifier(func(err error) bool {
		return false // never retryable
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "non-retryable") {
		t.Fatalf("error should mention 'non-retryable', got: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt (no retries), got %d", attempts)
	}
}

// TestRetry_IgnoreDeadlineExceeded verifies that DeadlineExceeded does not cancel
// the retry loop when the option is set, matching the existing decision.go behavior.
func TestRetry_IgnoreDeadlineExceeded(t *testing.T) {
	// Create a context whose deadline is already in the past.
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	time.Sleep(1 * time.Millisecond) // ensure deadline is exceeded

	attempts := 0
	_, err := Retry(ctx, 2, func(ctx context.Context) (int, error) {
		attempts++
		return 0, errors.New("fn error")
	}, WithIgnoreDeadlineExceeded(), WithBaseDelay(1*time.Millisecond))

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("should not return context.DeadlineExceeded when ignored")
	}
	// Should have made all 3 attempts (initial + 2 retries) instead of aborting.
	if attempts != 3 {
		t.Fatalf("expected 3 attempts with ignore option, got %d", attempts)
	}
}
