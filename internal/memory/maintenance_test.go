package memory

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

func TestCosineSimilarity_IdenticalVectors(t *testing.T) {
	v := []float32{1.0, 0.0, 0.0}
	got := cosineSimilarity(v, v)
	if got != 1.0 {
		t.Fatalf("identical vectors should have similarity 1.0, got %v", got)
	}
}

func TestCosineSimilarity_OrthogonalVectors(t *testing.T) {
	a := []float32{1.0, 0.0, 0.0}
	b := []float32{0.0, 1.0, 0.0}
	got := cosineSimilarity(a, b)
	if got != 0.0 {
		t.Fatalf("orthogonal vectors should have similarity 0.0, got %v", got)
	}
}

func TestCosineSimilarity_OppositeVectors(t *testing.T) {
	a := []float32{1.0, 0.0, 0.0}
	b := []float32{-1.0, 0.0, 0.0}
	got := cosineSimilarity(a, b)
	if got != -1.0 {
		t.Fatalf("opposite vectors should have similarity -1.0, got %v", got)
	}
}

func TestCosineSimilarity_ZeroLengthVector(t *testing.T) {
	v := []float32{0.0, 0.0, 0.0}
	got := cosineSimilarity(v, v)
	if got != 0.0 {
		t.Fatalf("zero-length vectors should return 0, got %v", got)
	}
}

func TestCosineSimilarity_MismatchedLengths(t *testing.T) {
	a := []float32{1.0, 2.0}
	b := []float32{1.0, 2.0, 3.0}
	got := cosineSimilarity(a, b)
	if got != 0.0 {
		t.Fatalf("mismatched length vectors should return 0, got %v", got)
	}
}

func TestCosineSimilarity_EmptyVectors(t *testing.T) {
	got := cosineSimilarity([]float32{}, []float32{})
	if got != 0.0 {
		t.Fatalf("empty vectors should return 0, got %v", got)
	}
	got = cosineSimilarity(nil, nil)
	if got != 0.0 {
		t.Fatalf("nil vectors should return 0, got %v", got)
	}
}

func TestCosineSimilarity_NearUnit(t *testing.T) {
	a := []float32{1.0, 0.1, 0.0}
	b := []float32{1.0, 0.0, 0.1}
	got := cosineSimilarity(a, b)
	expected := float32(1.0 / (math.Sqrt(1.01) * math.Sqrt(1.01)))
	if math.Abs(float64(got-expected)) > 0.0001 {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

type mockMaintenanceStore struct {
	mu        sync.Mutex
	points    []*scrollPoint
	deleted   []string
	scrollErr error
	deleteErr error
}

func (m *mockMaintenanceStore) ScrollMemories(ctx context.Context, limit uint32) ([]*scrollPoint, error) {
	if m.scrollErr != nil {
		return nil, m.scrollErr
	}
	return m.points, nil
}

func (m *mockMaintenanceStore) DeletePoint(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, id)
	return nil
}

func makePayload(userID string, memType Type, importance float64, access int64, confidence float64, createdAt time.Time, lastAccessedAt time.Time) map[string]*qdrant.Value {
	pl := map[string]*qdrant.Value{
		"user_id":          {Kind: &qdrant.Value_StringValue{StringValue: userID}},
		"memory_type":      {Kind: &qdrant.Value_StringValue{StringValue: string(memType)}},
		"importance_score": {Kind: &qdrant.Value_DoubleValue{DoubleValue: importance}},
		"access_count":     {Kind: &qdrant.Value_IntegerValue{IntegerValue: access}},
		"confidence":       {Kind: &qdrant.Value_DoubleValue{DoubleValue: confidence}},
		"created_at":       {Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(createdAt.UnixMilli()) / 1000.0}},
		"last_accessed_at": {Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(lastAccessedAt.UnixMilli()) / 1000.0}},
	}
	return pl
}

func makePayloadWithExpires(expiresAt time.Time) map[string]*qdrant.Value {
	now := time.Now()
	return map[string]*qdrant.Value{
		"user_id":          {Kind: &qdrant.Value_StringValue{StringValue: "user-1"}},
		"memory_type":      {Kind: &qdrant.Value_StringValue{StringValue: string(TypeFact)}},
		"importance_score": {Kind: &qdrant.Value_DoubleValue{DoubleValue: 0.5}},
		"access_count":     {Kind: &qdrant.Value_IntegerValue{IntegerValue: 0}},
		"confidence":       {Kind: &qdrant.Value_DoubleValue{DoubleValue: 0.5}},
		"created_at":       {Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(now.UnixMilli()) / 1000.0}},
		"expires_at":       {Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(expiresAt.UnixMilli()) / 1000.0}},
	}
}

func TestMaintenanceWorker_StartStop(t *testing.T) {
	store := &mockMaintenanceStore{}
	config := &ServiceConfig{
		MaintenanceIntervalSec: 3600,
		MergeCronHourUTC:       3,
		SummarizeCronDay:       0,
		MergeCosineThreshold:   0.9,
		PruneDecayThreshold:    0.1,
		PruneAgeDays:           90,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact:     0.01,
				TypeEpisode:  0.02,
				TypeInterest: 0.05,
				TypeSummary:  0.005,
			},
		},
	}

	ctx := context.Background()
	w := StartMaintenanceWorker(ctx, store, config)
	if w == nil {
		t.Fatal("expected non-nil worker")
	}

	time.Sleep(50 * time.Millisecond)
	w.Stop()

	select {
	case <-w.done:
	default:
		t.Fatal("done channel should be closed after Stop")
	}
}

func TestMaintenanceWorker_StopIdempotent(t *testing.T) {
	store := &mockMaintenanceStore{}
	config := &ServiceConfig{
		MaintenanceIntervalSec: 3600,
		MergeCronHourUTC:       3,
		SummarizeCronDay:       0,
		MergeCosineThreshold:   0.9,
		PruneDecayThreshold:    0.1,
		PruneAgeDays:           90,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}

	ctx := context.Background()
	w := StartMaintenanceWorker(ctx, store, config)
	w.Stop()
	w.Stop()
}

func TestMaintenanceWorker_ContextCancellation(t *testing.T) {
	store := &mockMaintenanceStore{}
	config := &ServiceConfig{
		MaintenanceIntervalSec: 3600,
		MergeCronHourUTC:       3,
		SummarizeCronDay:       0,
		MergeCosineThreshold:   0.9,
		PruneDecayThreshold:    0.1,
		PruneAgeDays:           90,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := StartMaintenanceWorker(ctx, store, config)
	time.Sleep(50 * time.Millisecond)
	cancel()
	w.Stop()
}

func TestMergeDuplicates_EmptyStore(t *testing.T) {
	store := &mockMaintenanceStore{points: []*scrollPoint{}}
	config := &ServiceConfig{
		MergeCosineThreshold: 0.9,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.mergeDuplicates(context.Background())
	if err != nil {
		t.Fatalf("mergeDuplicates on empty store should not error: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected 0 deletions, got %d", len(store.deleted))
	}
}

func TestMergeDuplicates_NoSimilarPairs(t *testing.T) {
	now := time.Now()
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{ID: "a", Embedding: []float32{1.0, 0.0, 0.0}, Payload: makePayload("user-1", TypeFact, 0.8, 5, 0.9, now, now)},
			{ID: "b", Embedding: []float32{0.0, 1.0, 0.0}, Payload: makePayload("user-1", TypeFact, 0.7, 3, 0.8, now, now)},
		},
	}
	config := &ServiceConfig{
		MergeCosineThreshold: 0.9,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.mergeDuplicates(context.Background())
	if err != nil {
		t.Fatalf("mergeDuplicates error: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected 0 deletions for orthogonal vectors, got %d", len(store.deleted))
	}
}

func TestMergeDuplicates_HighSimilarity(t *testing.T) {
	now := time.Now()
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{ID: "a", Embedding: []float32{1.0, 0.0, 0.0}, Payload: makePayload("user-1", TypeFact, 0.5, 0, 0.5, now, now)},
			{ID: "b", Embedding: []float32{0.99, 0.0, 0.0}, Payload: makePayload("user-1", TypeFact, 0.9, 10, 0.9, now, now)},
		},
	}
	config := &ServiceConfig{
		MergeCosineThreshold: 0.9,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.mergeDuplicates(context.Background())
	if err != nil {
		t.Fatalf("mergeDuplicates error: %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("expected 1 deletion for highly similar pair, got %d", len(store.deleted))
	}
}

func TestMergeDuplicates_DifferentUsers(t *testing.T) {
	now := time.Now()
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{ID: "a", Embedding: []float32{1.0, 0.0, 0.0}, Payload: makePayload("user-1", TypeFact, 0.8, 5, 0.9, now, now)},
			{ID: "b", Embedding: []float32{1.0, 0.0, 0.0}, Payload: makePayload("user-2", TypeFact, 0.7, 3, 0.8, now, now)},
		},
	}
	config := &ServiceConfig{
		MergeCosineThreshold: 0.9,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.mergeDuplicates(context.Background())
	if err != nil {
		t.Fatalf("mergeDuplicates error: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected 0 deletions for different users, got %d", len(store.deleted))
	}
}

func TestMergeDuplicates_DifferentTypes(t *testing.T) {
	now := time.Now()
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{ID: "a", Embedding: []float32{1.0, 0.0, 0.0}, Payload: makePayload("user-1", TypeFact, 0.8, 5, 0.9, now, now)},
			{ID: "b", Embedding: []float32{1.0, 0.0, 0.0}, Payload: makePayload("user-1", TypeEpisode, 0.7, 3, 0.8, now, now)},
		},
	}
	config := &ServiceConfig{
		MergeCosineThreshold: 0.9,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact:    0.01,
				TypeEpisode: 0.02,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.mergeDuplicates(context.Background())
	if err != nil {
		t.Fatalf("mergeDuplicates error: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected 0 deletions for different types, got %d", len(store.deleted))
	}
}

func TestMergeDuplicates_ScrollError(t *testing.T) {
	store := &mockMaintenanceStore{
		scrollErr: context.DeadlineExceeded,
	}
	config := &ServiceConfig{
		MergeCosineThreshold: 0.9,
		Scoring: ScoringConfig{
			Weights:    ScoringWeights{},
			DecayRates: map[Type]float64{},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.mergeDuplicates(context.Background())
	if err == nil {
		t.Fatal("expected error on scroll failure")
	}
}

func TestSummarizeAndPrune_LowDecayScore(t *testing.T) {
	now := time.Now()
	oldDate := now.AddDate(0, 0, -100)
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{
				ID:      "old",
				Payload: makePayload("user-1", TypeFact, 0.01, 0, 0.5, oldDate, oldDate),
			},
		},
	}
	config := &ServiceConfig{
		PruneDecayThreshold: 0.1,
		PruneAgeDays:        90,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.summarizeAndPrune(context.Background())
	if err != nil {
		t.Fatalf("summarizeAndPrune error: %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("expected 1 deletion for low decay score, got %d", len(store.deleted))
	}
}

func TestSummarizeAndPrune_OldZeroAccess(t *testing.T) {
	now := time.Now()
	oldDate := now.AddDate(0, 0, -200)
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{
				ID:      "stale",
				Payload: makePayload("user-1", TypeFact, 0.9, 0, 0.9, oldDate, oldDate),
			},
		},
	}
	config := &ServiceConfig{
		PruneDecayThreshold: 0.01,
		PruneAgeDays:        90,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{},
			DecayRates: map[Type]float64{
				TypeFact: 0.001,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.summarizeAndPrune(context.Background())
	if err != nil {
		t.Fatalf("summarizeAndPrune error: %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("expected 1 deletion for old zero-access memory, got %d", len(store.deleted))
	}
}

func TestSummarizeAndPrune_Healthy(t *testing.T) {
	now := time.Now()
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{
				ID:      "healthy",
				Payload: makePayload("user-1", TypeFact, 0.9, 100, 0.9, now, now),
			},
		},
	}
	config := &ServiceConfig{
		PruneDecayThreshold: 0.1,
		PruneAgeDays:        90,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.summarizeAndPrune(context.Background())
	if err != nil {
		t.Fatalf("summarizeAndPrune error: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected 0 deletions for healthy memory, got %d", len(store.deleted))
	}
}

func TestPruneExpired_Expired(t *testing.T) {
	expiredTime := time.Now().Add(-1 * time.Hour)
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{
				ID:      "exp-1",
				Payload: makePayloadWithExpires(expiredTime),
			},
		},
	}
	config := &ServiceConfig{
		Scoring: ScoringConfig{
			Weights:    ScoringWeights{},
			DecayRates: map[Type]float64{},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.pruneExpired(context.Background())
	if err != nil {
		t.Fatalf("pruneExpired error: %v", err)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("expected 1 deletion for expired memory, got %d", len(store.deleted))
	}
}

func TestPruneExpired_NotExpired(t *testing.T) {
	futureTime := time.Now().Add(24 * time.Hour)
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{
				ID:      "future",
				Payload: makePayloadWithExpires(futureTime),
			},
		},
	}
	config := &ServiceConfig{
		Scoring: ScoringConfig{
			Weights:    ScoringWeights{},
			DecayRates: map[Type]float64{},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.pruneExpired(context.Background())
	if err != nil {
		t.Fatalf("pruneExpired error: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected 0 deletions for unexpired memory, got %d", len(store.deleted))
	}
}

func TestPruneExpired_NoExpiresAt(t *testing.T) {
	now := time.Now()
	store := &mockMaintenanceStore{
		points: []*scrollPoint{
			{
				ID:      "noexpiry",
				Payload: makePayload("user-1", TypeFact, 0.5, 0, 0.5, now, now),
			},
		},
	}
	config := &ServiceConfig{
		Scoring: ScoringConfig{
			Weights:    ScoringWeights{},
			DecayRates: map[Type]float64{},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}
	err := w.pruneExpired(context.Background())
	if err != nil {
		t.Fatalf("pruneExpired error: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected 0 deletions for memory without expires_at, got %d", len(store.deleted))
	}
}

func TestRunScheduled_DispatchesCorrectJob(t *testing.T) {
	store := &mockMaintenanceStore{}
	config := &ServiceConfig{
		MaintenanceIntervalSec: 60,
		MergeCronHourUTC:       2,
		SummarizeCronDay:       1, // Monday
		MergeCosineThreshold:   0.9,
		PruneDecayThreshold:    0.1,
		PruneAgeDays:           90,
		Scoring: ScoringConfig{
			Weights: ScoringWeights{
				Importance: 0.4,
				Recency:    0.3,
				Access:     0.2,
				Confidence: 0.1,
			},
			DecayRates: map[Type]float64{
				TypeFact: 0.01,
			},
		},
	}
	w := &MaintenanceWorker{store: store, config: config}

	mondayAt2AM := time.Date(2026, 5, 4, 2, 2, 0, 0, time.UTC)
	w.runScheduled(context.Background(), mondayAt2AM)

	firstOfMonth := time.Date(2026, 6, 1, 2, 2, 0, 0, time.UTC)
	w.runScheduled(context.Background(), firstOfMonth)

	nonMergeHour := time.Date(2026, 6, 1, 5, 0, 0, 0, time.UTC)
	w.runScheduled(context.Background(), nonMergeHour)
}
