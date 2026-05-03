package memory

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockEmbedder implements Embedder, counts API calls for verification.
type mockEmbedder struct {
	mu        sync.Mutex
	callCount map[string]int
}

func newMockEmbedder() *mockEmbedder {
	return &mockEmbedder{callCount: make(map[string]int)}
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount[text]++
	// Deterministic: vector depends on text length
	return []float32{float32(len(text)), float32(len(text) * 2)}, nil
}
func (m *mockEmbedder) Stop() {}

func (m *mockEmbedder) count(text string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount[text]
}

// TestCachedEmbedder_Hit verifies that identical requests hit the cache.
func TestCachedEmbedder_Hit(t *testing.T) {
	mock := newMockEmbedder()
	e := newCachedEmbedder(mock, "test-model", 2000, 1*time.Hour, 1*time.Minute)
	defer e.Stop()
	ctx := context.Background()

	// First call hits API.
	v1, err := e.Embed(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if mock.count("hello") != 1 {
		t.Fatalf("first call should hit API: got %d calls", mock.count("hello"))
	}

	// Second call hits cache.
	v2, err := e.Embed(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if mock.count("hello") != 1 {
		t.Fatalf("cache hit should not call API: got %d calls", mock.count("hello"))
	}
	if v1[0] != v2[0] || v1[1] != v2[1] {
		t.Fatal("cached vector differs from original")
	}
}

// TestCachedEmbedder_Miss verifies that different texts are separate cache keys.
func TestCachedEmbedder_Miss(t *testing.T) {
	mock := newMockEmbedder()
	e := newCachedEmbedder(mock, "test-model", 2000, 1*time.Hour, 1*time.Minute)
	defer e.Stop()
	ctx := context.Background()

	e.Embed(ctx, "hello")
	e.Embed(ctx, "world")

	if mock.count("hello") != 1 {
		t.Fatalf("expected 1 API call for 'hello': got %d", mock.count("hello"))
	}
	if mock.count("world") != 1 {
		t.Fatalf("expected 1 API call for 'world': got %d", mock.count("world"))
	}
}

// TestCachedEmbedder_TTL verifies entries expire after TTL.
func TestCachedEmbedder_TTL(t *testing.T) {
	mock := newMockEmbedder()
	e := newCachedEmbedder(mock, "test-model", 2000, 100*time.Millisecond, 1*time.Minute)
	defer e.Stop()
	ctx := context.Background()

	e.Embed(ctx, "hello")
	if mock.count("hello") != 1 {
		t.Fatalf("first call should hit API: got %d calls", mock.count("hello"))
	}

	time.Sleep(150 * time.Millisecond)

	e.Embed(ctx, "hello")
	if mock.count("hello") != 2 {
		t.Fatalf("after TTL expiry should call API again: got %d calls", mock.count("hello"))
	}
}

// TestCachedEmbedder_MaxSize verifies oldest entry is evicted when cache is full.
func TestCachedEmbedder_MaxSize(t *testing.T) {
	mock := newMockEmbedder()
	e := newCachedEmbedder(mock, "test-model", 3, 1*time.Hour, 1*time.Minute)
	defer e.Stop()
	ctx := context.Background()

	e.Embed(ctx, "a")
	e.Embed(ctx, "b")
	e.Embed(ctx, "c")

	// 4th entry triggers eviction of oldest ("a").
	e.Embed(ctx, "d")

	// "a" was evicted — next access calls API again.
	e.Embed(ctx, "a")
	if mock.count("a") != 2 {
		t.Fatalf("evicted 'a' should call API again: got %d calls", mock.count("a"))
	}

	// "c" was NOT evicted — still in cache (only "a" was evicted, then re-inserting
	// "a" evicted "b"). Let's verify cache is working: a second call to the last
	// inserted item should be a cache hit.
	if mock.count("a") != 2 {
		t.Fatalf("expected 2 API calls for 'a': got %d", mock.count("a"))
	}
	e.Embed(ctx, "a")
	if mock.count("a") != 2 {
		t.Fatalf("re-inserted 'a' should be cached: got %d calls", mock.count("a"))
	}
}

// TestCachedEmbedder_ModelChange verifies the model is part of the cache key.
func TestCachedEmbedder_ModelChange(t *testing.T) {
	mock := newMockEmbedder()
	eA := newCachedEmbedder(mock, "model-A", 2000, 1*time.Hour, 1*time.Minute)
	defer eA.Stop()
	eB := newCachedEmbedder(mock, "model-B", 2000, 1*time.Hour, 1*time.Minute)
	defer eB.Stop()
	ctx := context.Background()

	eA.Embed(ctx, "hello")
	eB.Embed(ctx, "hello")

	// Different models produce different cache keys — both call API.
	if mock.count("hello") != 2 {
		t.Fatalf("different models should have different cache keys: expected 2 calls, got %d", mock.count("hello"))
	}
}

// TestCachedEmbedder_Concurrency verifies singleflight deduplication under load.
func TestCachedEmbedder_Concurrency(t *testing.T) {
	mock := newMockEmbedder()
	e := newCachedEmbedder(mock, "test-model", 2000, 1*time.Hour, 1*time.Minute)
	defer e.Stop()
	ctx := context.Background()

	n := 50
	var wg sync.WaitGroup
	errCh := make(chan error, n)

	for range n {
		wg.Go(func() {
			_, err := e.Embed(ctx, "same-text")
			if err != nil {
				errCh <- err
			}
		})
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("unexpected error: %v", err)
	}

	// Singleflight should deduplicate 50 concurrent calls into 1 API call.
	if mock.count("same-text") != 1 {
		t.Fatalf("singleflight should dedup 50 concurrent calls to 1: got %d", mock.count("same-text"))
	}
}
