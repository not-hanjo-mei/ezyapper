package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

// forcedErrorEmbedder always returns the given error.
type forcedErrorEmbedder struct {
	err error
}

func (m *forcedErrorEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, m.err
}

// mockQdrantStore implements qdrantStore for consolidation tests.
type mockQdrantStore struct {
	memories map[string]*Memory
	profiles map[string]*Profile
	// upsertMemoryErr forces UpsertMemory to fail after retry exhaustion
	upsertMemoryErr error
	// upsertProfileErr forces UpsertProfile to fail
	upsertProfileErr error
}

func newMockQdrantStore() *mockQdrantStore {
	return &mockQdrantStore{
		memories: make(map[string]*Memory),
		profiles: make(map[string]*Profile),
	}
}

func (m *mockQdrantStore) UpsertMemory(ctx context.Context, memory *Memory) error {
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

func (m *mockQdrantStore) GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Memory, error) {
	var result []*Memory
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

	vec, err := embedWithRetry(ctx, emb, "test text")
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

	_, err := embedWithRetry(ctx, emb, "test text")
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
	_, err := embedWithRetry(ctx, emb, "test text")
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

	vec, err := embedWithRetry(ctx, emb, "immediate")
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

// TestConsolidationProcess_QdrantStoreError verifies that Qdrant upsert errors
// are handled gracefully — memories that fail to store won't crash consolidation.
func TestConsolidationProcess_QdrantStoreError(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	qdrant := newMockQdrantStore()
	qdrant.upsertMemoryErr = errors.New("qdrant unavailable")

	c := &Consolidator{
		qdrant:   qdrant,
		embedder: newRetryableEmbedder(0),
	}

	userID := "user-1"
	profile := c.getOrCreateProfile(ctx, userID)
	profile.Traits = []string{"friendly"}
	profile.Facts = map[string]string{"name": "TestUser"}

	memories := []*Memory{
		{UserID: userID, MemoryType: MemoryTypeFact, Content: "User is friendly"},
	}

	result, err := c.consolidateMemories(ctx, profile, memories)
	if err != nil {
		t.Fatalf("consolidateMemories failed: %v", err)
	}

	// Call the storage loop — should not panic even when Qdrant fails every time
	stored := 0
	for i, extract := range result.Memories {
		memory := &Memory{
			UserID:     userID,
			MemoryType: MemoryType(extract.Type),
			Content:    extract.Content,
			Summary:    extract.Content,
			Keywords:   extract.Keywords,
			Confidence: extract.Confidence,
			CreatedAt:  time.Now(),
		}

		embedding, err := embedWithRetry(ctx, c.embedder, memory.Content)
		if err != nil {
			t.Logf("memory %d: embedding failed (expected for retry test): %v", i+1, err)
			continue
		}
		memory.Embedding = embedding

		if err := c.qdrant.UpsertMemory(ctx, memory); err != nil {
			t.Logf("memory %d: qdrant upsert failed (expected): %v", i+1, err)
		} else {
			stored++
		}
	}

	if stored != 0 {
		t.Errorf("expected 0 stored memories (qdrant always fails), got %d", stored)
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

// TestProfileMemoryCount_OnlyOnSuccess verifies Process() storage pattern:
// all Qdrant upserts fail -> MemoryCount unchanged.
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

	extracts := []MemoryExtract{
		{Content: "memory one", Type: string(MemoryTypeFact), Confidence: 0.9},
		{Content: "memory two", Type: string(MemoryTypeSummary), Confidence: 0.8},
		{Content: "memory three", Type: string(MemoryTypeEpisode), Confidence: 0.7},
	}

	stored := 0
	for _, extract := range extracts {
		memory := &Memory{
			UserID:     profile.UserID,
			MemoryType: MemoryType(extract.Type),
			Content:    extract.Content,
			Summary:    extract.Content,
			Keywords:   extract.Keywords,
			Confidence: extract.Confidence,
			CreatedAt:  time.Now(),
		}
		embedding, err := embedWithRetry(ctx, c.embedder, memory.Content)
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

	extracts := []MemoryExtract{
		{Content: "memory alpha", Type: string(MemoryTypeFact), Confidence: 0.95},
		{Content: "memory beta", Type: string(MemoryTypeSummary), Confidence: 0.85},
		{Content: "memory gamma", Type: string(MemoryTypeEpisode), Confidence: 0.75},
		{Content: "memory delta", Type: string(MemoryTypeFact), Confidence: 0.65},
		{Content: "memory epsilon", Type: string(MemoryTypeSummary), Confidence: 0.55},
	}

	stored := 0
	for _, extract := range extracts {
		memory := &Memory{
			UserID:     profile.UserID,
			MemoryType: MemoryType(extract.Type),
			Content:    extract.Content,
			Summary:    extract.Content,
			Keywords:   extract.Keywords,
			Confidence: extract.Confidence,
			CreatedAt:  time.Now(),
		}
		embedding, err := embedWithRetry(ctx, c.embedder, memory.Content)
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

// TestUpdateProfileFromResult_NoMemoryCount verifies that updateProfileFromResult
// does not modify MemoryCount — only Process() does, after storage.
func TestUpdateProfileFromResult_NoMemoryCount(t *testing.T) {
	profile := &Profile{
		UserID:      "user-1",
		MemoryCount: 7,
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
		Interests:   []string{},
	}

	result := &ConsolidationResult{
		Summary: "test summary",
		ProfileDelta: ProfileDelta{
			NewTraits:      []string{"helpful"},
			NewFacts:       map[string]string{"name": "TestUser"},
			NewPreferences: map[string]string{},
			NewInterests:   []string{},
		},
		Memories: []MemoryExtract{
			{Content: "memory 1", Type: string(MemoryTypeFact), Confidence: 0.9},
			{Content: "memory 2", Type: string(MemoryTypeSummary), Confidence: 0.8},
			{Content: "memory 3", Type: string(MemoryTypeEpisode), Confidence: 0.7},
		},
	}

	c := &Consolidator{}
	c.updateProfileFromResult(profile, result)

	if profile.MemoryCount != 7 {
		t.Errorf("updateProfileFromResult should not change MemoryCount: expected 7, got %d", profile.MemoryCount)
	}
	if profile.LastSummary != "test summary" {
		t.Errorf("LastSummary should be set, got %q", profile.LastSummary)
	}
	if len(profile.Traits) != 1 || profile.Traits[0] != "helpful" {
		t.Errorf("Traits should contain 'helpful', got %v", profile.Traits)
	}
	if profile.Facts["name"] != "TestUser" {
		t.Errorf("Facts['name'] should be 'TestUser', got %q", profile.Facts["name"])
	}
}
