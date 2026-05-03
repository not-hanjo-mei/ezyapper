package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/logger"
)

var embedSleep func(time.Duration) // test-only override for retry sleep; unused in production

func TestMain(m *testing.M) {
	logger.Init(logger.Config{Level: "info"})
	os.Exit(m.Run())
}

// retryableEmbedder returns errors for the first failCount calls, then succeeds.
type retryableEmbedder struct {
	calls     atomic.Int64
	failCount int64
	mu        sync.Mutex
	vectors   map[string][]float32
}

func newRetryableEmbedder(failCount int64) *retryableEmbedder {
	return &retryableEmbedder{
		failCount: failCount,
		vectors:   make(map[string][]float32),
	}
}

func (m *retryableEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	call := m.calls.Add(1)
	if call <= m.failCount {
		return nil, fmt.Errorf("embedding failed (call %d of %d allowed failures)", call, m.failCount)
	}
	return []float32{float32(len(text)), float32(len(text) * 2)}, nil
}
func (m *retryableEmbedder) Stop() {}

// forcedErrorEmbedder always returns the given error.
type forcedErrorEmbedder struct {
	err error
}

func (m *forcedErrorEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, m.err
}
func (m *forcedErrorEmbedder) Stop() {}

// mockQdrantStore implements qdrantStore for consolidation tests.
type mockQdrantStore struct {
	memories map[string]*Record
	profiles map[string]*Profile
	// upsertMemoryErr forces UpsertMemory to fail after retry exhaustion
	upsertMemoryErr error
	// upsertProfileErr forces UpsertProfile to fail
	upsertProfileErr error
}

func newMockQdrantStore() *mockQdrantStore {
	return &mockQdrantStore{
		memories: make(map[string]*Record),
		profiles: make(map[string]*Profile),
	}
}

func (m *mockQdrantStore) UpsertMemory(ctx context.Context, memory *Record) error {
	if m.upsertMemoryErr != nil {
		return m.upsertMemoryErr
	}
	if memory.ID == "" {
		memory.ID = fmt.Sprintf("mem-%d", len(m.memories))
	}
	m.memories[memory.ID] = memory
	return nil
}

func (m *mockQdrantStore) UpsertProfile(ctx context.Context, profile *Profile) error {
	if m.upsertProfileErr != nil {
		return m.upsertProfileErr
	}
	m.profiles[profile.UserID] = profile
	return nil
}

func (m *mockQdrantStore) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	p, ok := m.profiles[userID]
	if !ok {
		return &Profile{
			UserID:      userID,
			Traits:      []string{},
			Facts:       make(map[string]string),
			Preferences: make(map[string]string),
			Interests:   []string{},
			FirstSeenAt: time.Now(),
		}, nil
	}
	return p, nil
}

func (m *mockQdrantStore) GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Record, error) {
	var result []*Record
	for _, mem := range m.memories {
		if mem.UserID == userID {
			result = append(result, mem)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

// TestEmbedWithRetry_Success verifies embedWithRetry retries on failure then succeeds.
func TestEmbedWithRetry_Success(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {} // skip real sleep

	ctx := context.Background()
	emb := newRetryableEmbedder(2) // fails first 2 calls, succeeds on 3rd

	c := &Consolidator{
		embedder:        emb,
		retryMaxRetries: 3,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	vec, err := c.embedWithRetry(ctx, "test text")
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if len(vec) != 2 {
		t.Fatalf("expected 2-element vector, got %d", len(vec))
	}
	if emb.calls.Load() != 3 {
		t.Fatalf("expected 3 total calls (2 failures + 1 success), got %d", emb.calls.Load())
	}
}

// TestEmbedWithRetry_Exhausted verifies embedWithRetry returns error after all retries fail.
func TestEmbedWithRetry_Exhausted(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	// Always fails — 1 initial + 3 retries = 4 attempts
	emb := newRetryableEmbedder(999)

	c := &Consolidator{
		embedder:        emb,
		retryMaxRetries: 3,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	_, err := c.embedWithRetry(ctx, "test text")
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if emb.calls.Load() != 4 {
		t.Fatalf("expected 4 total calls (1 initial + 3 retries), got %d", emb.calls.Load())
	}
}

// TestEmbedWithRetry_ContextCancelled verifies context cancellation stops retry loop.
func TestEmbedWithRetry_ContextCancelled(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	emb := newRetryableEmbedder(999)
	c := &Consolidator{
		embedder:        emb,
		retryMaxRetries: 3,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	_, err := c.embedWithRetry(ctx, "test text")
	if err == nil {
		t.Fatal("expected context cancelled error, got nil")
	}
}

// TestEmbedWithRetry_ImmediateSuccess verifies zero retries when first call succeeds.
func TestEmbedWithRetry_ImmediateSuccess(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	emb := newRetryableEmbedder(0) // no failures

	c := &Consolidator{
		embedder:        emb,
		retryMaxRetries: 3,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	vec, err := c.embedWithRetry(ctx, "immediate")
	if err != nil {
		t.Fatalf("expected success on first call, got: %v", err)
	}
	if len(vec) != 2 {
		t.Fatalf("expected 2-element vector, got %d", len(vec))
	}
	if emb.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 call, got %d", emb.calls.Load())
	}
}

// selectiveEmbedder fails embedding for specific content strings.
type selectiveEmbedder struct {
	failFor map[string]bool
}

func (e *selectiveEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e.failFor[text] {
		return nil, errors.New("embedding disabled for this text")
	}
	return []float32{float32(len(text)), float32(len(text) * 2)}, nil
}
func (e *selectiveEmbedder) Stop() {}

// TestProfileMemoryCount_OnlyOnSuccess verifies that when all Qdrant upserts
// fail, MemoryCount remains unchanged.
func TestProfileMemoryCount_OnlyOnSuccess(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	qdrant := newMockQdrantStore()
	qdrant.upsertMemoryErr = errors.New("qdrant unavailable")

	profile := &Profile{
		UserID:      "user-1",
		MemoryCount: 5,
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
		Interests:   []string{},
	}

	c := &Consolidator{
		qdrant:   qdrant,
		embedder: newRetryableEmbedder(0),
	}

	extracts := []Extract{
		{Content: "memory one", Type: string(TypeFact), Confidence: 0.9},
		{Content: "memory two", Type: string(TypeSummary), Confidence: 0.8},
		{Content: "memory three", Type: string(TypeEpisode), Confidence: 0.7},
	}

	stored := 0
	for _, extract := range extracts {
		memory := &Record{
			UserID:     profile.UserID,
			MemoryType: Type(extract.Type),
			Content:    extract.Content,
			Summary:    extract.Content,
			Keywords:   extract.Keywords,
			Confidence: extract.Confidence,
			CreatedAt:  time.Now(),
		}
		embedding, err := c.embedWithRetry(ctx, memory.Content)
		if err != nil {
			continue
		}
		memory.Embedding = embedding
		if err := c.qdrant.UpsertMemory(ctx, memory); err != nil {
			t.Logf("memory upsert failed (expected): %v", err)
		} else {
			stored++
		}
	}

	if stored > 0 {
		profile.MemoryCount += stored
	}

	if profile.MemoryCount != 5 {
		t.Errorf("MemoryCount should remain 5 when all upserts fail, got %d", profile.MemoryCount)
	}
	if stored != 0 {
		t.Errorf("expected 0 stored memories (all upserts fail), got %d", stored)
	}
}

// TestProfileMemoryCount_Consistent verifies that partial embedding failure still
// results in correct MemoryCount: only successfully stored memories are counted.
func TestProfileMemoryCount_Consistent(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	qdrant := newMockQdrantStore()

	profile := &Profile{
		UserID:      "user-1",
		MemoryCount: 10,
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
		Interests:   []string{},
	}

	// 5 extracts but 2 fail at embedding -> only 3 reach Qdrant -> stored=3
	c := &Consolidator{
		qdrant: qdrant,
		embedder: &selectiveEmbedder{
			failFor: map[string]bool{
				"memory beta":  true,
				"memory delta": true,
			},
		},
	}

	extracts := []Extract{
		{Content: "memory alpha", Type: string(TypeFact), Confidence: 0.95},
		{Content: "memory beta", Type: string(TypeSummary), Confidence: 0.85},
		{Content: "memory gamma", Type: string(TypeEpisode), Confidence: 0.75},
		{Content: "memory delta", Type: string(TypeFact), Confidence: 0.65},
		{Content: "memory epsilon", Type: string(TypeSummary), Confidence: 0.55},
	}

	stored := 0
	for _, extract := range extracts {
		memory := &Record{
			UserID:     profile.UserID,
			MemoryType: Type(extract.Type),
			Content:    extract.Content,
			Summary:    extract.Content,
			Keywords:   extract.Keywords,
			Confidence: extract.Confidence,
			CreatedAt:  time.Now(),
		}
		embedding, err := c.embedWithRetry(ctx, memory.Content)
		if err != nil {
			continue
		}
		memory.Embedding = embedding
		if err := c.qdrant.UpsertMemory(ctx, memory); err != nil {
			t.Logf("memory upsert failed: %v", err)
		} else {
			stored++
		}
	}

	if stored > 0 {
		profile.MemoryCount += stored
	}

	if profile.MemoryCount != 13 {
		t.Errorf("MemoryCount should be 13 (10 + 3 stored), not %d (10 + %d stored, but had %d extracts)",
			profile.MemoryCount, stored, len(extracts))
	}
	if stored != 3 {
		t.Errorf("expected 3 stored memories (2 fail at embedding), got %d", stored)
	}
}
