package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/logger"

	"golang.org/x/sync/singleflight"
)

// AIEmbedder implements the Embedder interface using the AI client
type AIEmbedder struct {
	client *ai.Client
	model  string
}

// NewAIEmbedder creates a new AI-based embedder that uses the configured embedding model.
func NewAIEmbedder(client *ai.Client, model string) (*AIEmbedder, error) {
	if model == "" {
		return nil, fmt.Errorf("embedding model is required")
	}
	return &AIEmbedder{
		client: client,
		model:  model,
	}, nil
}

// Embed generates an embedding for the given text
func (e *AIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return e.client.CreateEmbedding(ctx, text, e.model)
}

type cacheEntry struct {
	vector     []float32
	insertedAt time.Time
}

// NewAIEmbedder creates a new AI-based embedder that uses the configured embedding model.
type CachedEmbedder struct {
	embedder Embedder
	model    string
	mu       sync.RWMutex
	cache    map[string]cacheEntry
	order    []string
	maxSize  int
	ttl      time.Duration
	sf       singleflight.Group
	now      func() time.Time
	stopCh   <-chan struct{}
	stop     chan struct{} // internal bidirectional channel for closing
	eviction time.Duration
}

func newCachedEmbedder(embedder Embedder, model string, maxSize int, ttl time.Duration, evictionInterval time.Duration) *CachedEmbedder {
	stop := make(chan struct{})
	e := &CachedEmbedder{
		embedder: embedder,
		model:    model,
		cache:    make(map[string]cacheEntry),
		order:    make([]string, 0, maxSize),
		maxSize:  maxSize,
		ttl:      ttl,
		now:      time.Now,
		stopCh:   stop,
		stop:     stop,
		eviction: evictionInterval,
	}
	go e.evictLoop()
	return e
}

func (e *CachedEmbedder) cacheKey(text string) string {
	key := sha256.Sum256([]byte(e.model + ":" + text))
	return fmt.Sprintf("%x", key)
}

func (e *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := e.cacheKey(text)

	e.mu.RLock()
	if entry, ok := e.cache[key]; ok && e.now().Sub(entry.insertedAt) < e.ttl {
		vec := entry.vector
		e.mu.RUnlock()
		return vec, nil
	}
	e.mu.RUnlock()

	result, err, _ := e.sf.Do(key, func() (any, error) {
		e.mu.RLock()
		if entry, ok := e.cache[key]; ok && e.now().Sub(entry.insertedAt) < e.ttl {
			vec := entry.vector
			e.mu.RUnlock()
			return vec, nil
		}
		e.mu.RUnlock()

		vec, err := e.embedder.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		if len(vec) == 0 {
			return vec, nil
		}

		e.mu.Lock()
		if len(e.cache) >= e.maxSize {
			logger.Warnf("evicting embedding cache entry (cache size=%d, max=%d)", len(e.cache), e.maxSize)
			e.evictOne()
		}
		e.cache[key] = cacheEntry{vector: vec, insertedAt: e.now()}
		e.order = append(e.order, key)
		e.mu.Unlock()

		return vec, nil
	})

	if err != nil {
		return nil, err
	}
	vec, ok := result.([]float32)
	if !ok {
		return nil, fmt.Errorf("embedder: unexpected type from singleflight: %T", result)
	}
	return vec, nil
}

func (e *CachedEmbedder) evictLoop() {
	ticker := time.NewTicker(e.eviction)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.evictExpired()
		case <-e.stopCh:
			return
		}
	}
}

func (e *CachedEmbedder) evictExpired() {
	e.mu.Lock()
	defer e.mu.Unlock()
	nowT := e.now()
	remaining := e.order[:0]
	for _, key := range e.order {
		entry, ok := e.cache[key]
		if !ok {
			continue
		}
		if nowT.Sub(entry.insertedAt) >= e.ttl {
			delete(e.cache, key)
		} else {
			remaining = append(remaining, key)
		}
	}
	e.order = remaining
}

func (e *CachedEmbedder) evictOne() {
	for i, key := range e.order {
		if _, ok := e.cache[key]; ok {
			delete(e.cache, key)
			e.order = append(e.order[:i], e.order[i+1:]...)
			return
		}
	}
}

// Stop shuts down the eviction loop and releases resources.
func (e *CachedEmbedder) Stop() {
	close(e.stop)
}
