package memory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

// TestSetPayload_PreservesUntouchedFields verifies that merging fields via
// SetPayload semantics (copy + override) preserves all untouched fields,
// proving that switching from OverwritePayload to SetPayload fixes data loss.
func TestSetPayload_PreservesUntouchedFields(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now()

	record := &Record{
		UserID:              "user_001",
		GuildID:             "guild_002",
		ChannelID:           "channel_003",
		MentionedUserIDs:    []string{"mentioned_u1"},
		MentionedChannelIDs: []string{"mentioned_c1"},
		MemoryType:          TypeFact,
		Content:             "Alice likes hiking in the mountains",
		Keywords:            []string{"hiking", "mountains", "outdoors"},
		Confidence:          0.92,
		CreatedAt:           now,
		ImportanceScore:     0.75,
		AccessCount:         3,
		LastAccessedAt:      now,
		DecayCategory:       "medium",
		ExpiresAt:           now.Add(7 * 24 * time.Hour),
	}

	original, err := qc.memoryToPayload(record)
	if err != nil {
		t.Fatalf("memoryToPayload failed: %v", err)
	}

	mustSurvive := []string{
		"user_id", "guild_id", "channel_id", "memory_type",
		"content", "confidence", "created_at", "updated_at",
		"keywords", "importance_score", "last_accessed_at",
		"decay_category", "schema_version",
	}

	for _, k := range mustSurvive {
		if _, ok := original[k]; !ok {
			t.Fatalf("setup: expected key %q missing from original payload", k)
		}
	}
	originalKeyCount := len(original)

	copyPayload := func(src map[string]*qdrant.Value) map[string]*qdrant.Value {
		dst := make(map[string]*qdrant.Value, len(src))
		for k, v := range src {
			dst[k] = v
		}
		return dst
	}

	t.Run("merge onto empty map only contains update keys", func(t *testing.T) {
		merged := map[string]*qdrant.Value{
			"access_count": {Kind: &qdrant.Value_IntegerValue{IntegerValue: 5}},
		}
		if len(merged) != 1 {
			t.Errorf("got %d keys, want 1", len(merged))
		}
		if v := merged["access_count"].GetIntegerValue(); v != 5 {
			t.Errorf("access_count = %d, want 5", v)
		}
	})

	tests := []struct {
		name    string
		updates map[string]*qdrant.Value
		checkFn func(t *testing.T, merged map[string]*qdrant.Value)
	}{
		{
			name: "single-field merge preserves all fields",
			updates: map[string]*qdrant.Value{
				"access_count": {Kind: &qdrant.Value_IntegerValue{IntegerValue: 5}},
			},
			checkFn: func(t *testing.T, merged map[string]*qdrant.Value) {
				for _, k := range mustSurvive {
					if _, ok := merged[k]; !ok {
						t.Errorf("key %q missing after single-field merge", k)
					}
				}
				if v := merged["access_count"].GetIntegerValue(); v != 5 {
					t.Errorf("access_count = %d, want 5", v)
				}
				if len(merged) != originalKeyCount {
					t.Errorf("got %d keys, want %d (keys were dropped)", len(merged), originalKeyCount)
				}
			},
		},
		{
			name: "multi-field merge preserves all fields",
			updates: map[string]*qdrant.Value{
				"access_count": {Kind: &qdrant.Value_IntegerValue{IntegerValue: 5}},
				"updated_at":   {Kind: &qdrant.Value_DoubleValue{DoubleValue: 1234567890.0}},
			},
			checkFn: func(t *testing.T, merged map[string]*qdrant.Value) {
				for _, k := range mustSurvive {
					if _, ok := merged[k]; !ok {
						t.Errorf("key %q missing after multi-field merge", k)
					}
				}
				if v := merged["access_count"].GetIntegerValue(); v != 5 {
					t.Errorf("access_count = %d, want 5", v)
				}
				if v := merged["updated_at"].GetDoubleValue(); v != 1234567890.0 {
					t.Errorf("updated_at = %f, want 1234567890.0", v)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := copyPayload(original)
			for k, v := range tt.updates {
				merged[k] = v
			}
			tt.checkFn(t, merged)
		})
	}
}

// healthyPayload returns a full-featured payload used in damage detection tests.
func healthyPayload() map[string]*qdrantValue {
	return map[string]*qdrantValue{
		"schema_version":        {Kind: &qdrant.Value_IntegerValue{IntegerValue: 4}},
		"user_id":               {Kind: &qdrant.Value_StringValue{StringValue: "user1"}},
		"guild_id":              {Kind: &qdrant.Value_StringValue{StringValue: "guild1"}},
		"channel_id":            {Kind: &qdrant.Value_StringValue{StringValue: "channel1"}},
		"memory_type":           {Kind: &qdrant.Value_StringValue{StringValue: "fact"}},
		"content":               {Kind: &qdrant.Value_StringValue{StringValue: "healthy memory content"}},
		"confidence":            {Kind: &qdrant.Value_DoubleValue{DoubleValue: 0.95}},
		"created_at":            {Kind: &qdrant.Value_DoubleValue{DoubleValue: 1000000}},
		"updated_at":            {Kind: &qdrant.Value_DoubleValue{DoubleValue: 1000000}},
		"keywords":              {Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: []*qdrant.Value{}}}},
		"mentioned_user_ids":    {Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: []*qdrant.Value{}}}},
		"mentioned_channel_ids": {Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: []*qdrant.Value{}}}},
		"importance_score":      {Kind: &qdrant.Value_DoubleValue{DoubleValue: 0.5}},
		"access_count":          {Kind: &qdrant.Value_IntegerValue{IntegerValue: 10}},
		"last_accessed_at":      {Kind: &qdrant.Value_DoubleValue{DoubleValue: 1000000}},
		"decay_category":        {Kind: &qdrant.Value_StringValue{StringValue: "medium"}},
		"expires_at":            {Kind: &qdrant.Value_DoubleValue{DoubleValue: 2000000}},
	}
}

// TestDetectDamagedMemories covers damage detection across several payload states.
func TestDetectDamagedMemories(t *testing.T) {
	tests := []struct {
		name     string
		points   []*scrollPoint
		expected []string
	}{
		{
			name: "full 17-field payload not damaged",
			points: []*scrollPoint{
				{ID: "uuid-healthy", Payload: healthyPayload()},
			},
			expected: nil,
		},
		{
			name: "access_count-only payload is damaged",
			points: []*scrollPoint{
				{ID: "uuid-access-only", Payload: map[string]*qdrantValue{
					"access_count": {Kind: &qdrant.Value_IntegerValue{IntegerValue: 3}},
				}},
			},
			expected: []string{"uuid-access-only"},
		},
		{
			name: "mentions-only payload is damaged",
			points: []*scrollPoint{
				{ID: "uuid-mentions", Payload: map[string]*qdrantValue{
					"mentioned_user_ids": {Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: []*qdrant.Value{}}}},
				}},
			},
			expected: []string{"uuid-mentions"},
		},
		{
			name: "empty payload is damaged",
			points: []*scrollPoint{
				{ID: "uuid-empty", Payload: map[string]*qdrantValue{}},
			},
			expected: []string{"uuid-empty"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectDamagedPoints(tt.points)
			if !slices.Equal(got, tt.expected) {
				t.Errorf("detectDamagedPoints() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestDetectDamagedMemories_NoDamage verifies that all-healthy points return an empty slice.
func TestDetectDamagedMemories_NoDamage(t *testing.T) {
	points := []*scrollPoint{
		{ID: "uuid-a", Payload: healthyPayload()},
		{ID: "uuid-b", Payload: healthyPayload()},
		{ID: "uuid-c", Payload: healthyPayload()},
	}

	got := detectDamagedPoints(points)
	if len(got) != 0 {
		t.Errorf("expected no damaged points, got %v", got)
	}
}

// TestRepairOrDeleteDamagedPoints verifies repair logic with mixed healthy/damaged points.
func TestRepairOrDeleteDamagedPoints(t *testing.T) {
	tests := []struct {
		name        string
		points      []*scrollPoint
		wantDeleted int
		wantUserIDs []string
	}{
		{
			name: "healthy plus damaged-with-userid plus damaged-without-userid",
			points: []*scrollPoint{
				{ID: "uuid-healthy", Payload: healthyPayload()},
				{
					ID: "uuid-damaged-uid",
					Payload: map[string]*qdrantValue{
						"user_id": {Kind: &qdrant.Value_StringValue{StringValue: "user-a"}},
						"content": {Kind: &qdrant.Value_StringValue{StringValue: "partial payload"}},
					},
				},
				{
					ID:      "uuid-damaged-nouid",
					Payload: map[string]*qdrantValue{},
				},
			},
			wantDeleted: 2,
			wantUserIDs: []string{"user-a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			deletedIDs := make(map[string]bool)

			deleteFn := func(_ context.Context, id string) error {
				deletedIDs[id] = true
				return nil
			}

			gotDeleted, gotUserIDs := repairOrDeleteDamagedPoints(ctx, tt.points, deleteFn)

			if gotDeleted != tt.wantDeleted {
				t.Errorf("deleted = %d, want %d", gotDeleted, tt.wantDeleted)
			}
			if !slices.Equal(gotUserIDs, tt.wantUserIDs) {
				t.Errorf("userIDs = %v, want %v", gotUserIDs, tt.wantUserIDs)
			}

			// Verify healthy point was NOT deleted.
			if deletedIDs["uuid-healthy"] {
				t.Error("healthy point was deleted unexpectedly")
			}
			// Verify damaged points WERE deleted.
			if !deletedIDs["uuid-damaged-uid"] {
				t.Error("damaged point uuid-damaged-uid was not deleted")
			}
			if !deletedIDs["uuid-damaged-nouid"] {
				t.Error("damaged point uuid-damaged-nouid was not deleted")
			}
		})
	}
}

// TestRepairOrDeleteDamagedPoints_Idempotent verifies running repair
// on an all-healthy collection returns zero deletions.
func TestRepairOrDeleteDamagedPoints_Idempotent(t *testing.T) {
	ctx := context.Background()
	points := []*scrollPoint{
		{ID: "uuid-a", Payload: healthyPayload()},
		{ID: "uuid-b", Payload: healthyPayload()},
		{ID: "uuid-c", Payload: healthyPayload()},
	}

	deleteFn := func(_ context.Context, id string) error {
		t.Errorf("unexpected delete call for healthy point %s", id)
		return nil
	}

	gotDeleted, gotUserIDs := repairOrDeleteDamagedPoints(ctx, points, deleteFn)

	if gotDeleted != 0 {
		t.Errorf("deleted = %d, want 0", gotDeleted)
	}
	if len(gotUserIDs) != 0 {
		t.Errorf("userIDs = %v, want nil", gotUserIDs)
	}
}
