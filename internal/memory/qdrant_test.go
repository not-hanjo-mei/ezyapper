package memory

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var retrySleep func(time.Duration) // test-only override for retry sleep; unused in production

// retryableCounter is a callable function that returns a retryable error
// for the first `failCount` calls, then succeeds.
type retryableCounter struct {
	calls     atomic.Int64
	failCount int64
}

func (r *retryableCounter) call() error {
	call := r.calls.Add(1)
	if call <= r.failCount {
		return status.Errorf(codes.Unavailable, "qdrant unavailable (attempt %d)", call)
	}
	return nil
}

var testRetryQC = &QdrantClient{
	maxRetries:  3,
	baseBackoff: 1 * time.Second,
	maxBackoff:  30 * time.Second,
}

// TestRetryWithBackoff_Success verifies that when the operation eventually succeeds
// within the retry budget, no error is returned.
func TestRetryWithBackoff_Success(t *testing.T) {
	ctx := context.Background()
	counter := &retryableCounter{failCount: 2} // fails twice, succeeds on 3rd

	err := testRetryQC.retryWithBackoff(ctx, "test_op", counter.call)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if counter.calls.Load() != 3 {
		t.Fatalf("expected 3 total calls (2 failures + 1 success), got %d", counter.calls.Load())
	}
}

// TestRetryWithBackoff_Exhausted verifies that when all retries are exhausted
// with retryable errors, the last error is returned with an appropriate message.
func TestRetryWithBackoff_Exhausted(t *testing.T) {
	ctx := context.Background()
	// Always return Unavailable — will exhaust all 4 attempts (1 initial + 3 retries)
	counter := &retryableCounter{failCount: 999}

	err := testRetryQC.retryWithBackoff(ctx, "test_op", counter.call)
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("expected 'exhausted' in error message, got: %v", err)
	}
	if counter.calls.Load() != 4 {
		t.Fatalf("expected 4 total calls (1 initial + 3 retries), got %d", counter.calls.Load())
	}
}

// TestRetryWithBackoff_NonRetryable verifies that non-retryable errors
// (e.g., InvalidArgument) are returned immediately without any retry.
func TestRetryWithBackoff_NonRetryable(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64

	err := testRetryQC.retryWithBackoff(ctx, "test_op", func() error {
		calls.Add(1)
		return status.Errorf(codes.InvalidArgument, "bad request")
	})
	if err == nil {
		t.Fatal("expected error for non-retryable code, got nil")
	}
	if !strings.Contains(err.Error(), "non-retryable") {
		t.Fatalf("expected 'non-retryable' in error message, got: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 call (no retries for non-retryable), got %d", calls.Load())
	}
}

// TestRetryWithBackoff_ContextCancelled verifies that context cancellation
// stops the retry loop early.
func TestRetryWithBackoff_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := testRetryQC.retryWithBackoff(ctx, "test_op", func() error {
		return status.Errorf(codes.Unavailable, "unavailable")
	})
	if err == nil {
		t.Fatal("expected context cancelled error, got nil")
	}
}

// TestRetryWithBackoff_ImmediateSuccess verifies zero retries when first call succeeds.
func TestRetryWithBackoff_ImmediateSuccess(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64

	err := testRetryQC.retryWithBackoff(ctx, "test_op", func() error {
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("expected success on first call, got: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 call, got %d", calls.Load())
	}
}

// TestIsRetryableGrpc tests the gRPC status code classification.
func TestIsRetryableGrpc(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"non-grpc error", errors.New("plain error"), false},
		{"Unavailable", status.Errorf(codes.Unavailable, "unavailable"), true},
		{"DeadlineExceeded", status.Errorf(codes.DeadlineExceeded, "deadline"), true},
		{"ResourceExhausted", status.Errorf(codes.ResourceExhausted, "exhausted"), true},
		{"InvalidArgument", status.Errorf(codes.InvalidArgument, "invalid"), false},
		{"NotFound", status.Errorf(codes.NotFound, "not found"), false},
		{"PermissionDenied", status.Errorf(codes.PermissionDenied, "denied"), false},
		{"Internal", status.Errorf(codes.Internal, "internal"), false},
		{"Aborted", status.Errorf(codes.Aborted, "aborted"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableGrpc(tt.err)
			if got != tt.expected {
				t.Errorf("isRetryableGrpc(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

// TestRetryWithBackoff_DeadlineExceeded_Retryable verifies DeadlineExceeded triggers retry.
func TestRetryWithBackoff_DeadlineExceeded_Retryable(t *testing.T) {
	ctx := context.Background()
	var deadlineCalls atomic.Int64
	err := testRetryQC.retryWithBackoff(ctx, "test_op", func() error {
		call := deadlineCalls.Add(1)
		if call <= 1 {
			return status.Errorf(codes.DeadlineExceeded, "deadline exceeded")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if deadlineCalls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", deadlineCalls.Load())
	}
}

// TestRetryWithBackoff_ResourceExhausted_Retryable verifies ResourceExhausted triggers retry.
func TestRetryWithBackoff_ResourceExhausted_Retryable(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64

	err := testRetryQC.retryWithBackoff(ctx, "test_op", func() error {
		call := calls.Add(1)
		if call <= 3 {
			return status.Errorf(codes.ResourceExhausted, "resource exhausted")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if calls.Load() != 4 {
		t.Fatalf("expected 4 calls, got %d", calls.Load())
	}
}

func init() {
	retrySleep = func(time.Duration) {}
}
