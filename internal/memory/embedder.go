package memory

import (
	"context"
	"fmt"
	"sync/atomic"
)

// embeddingClient is the subset of ai.Client methods used by AIEmbedder.
type embeddingClient interface {
	CreateEmbedding(ctx context.Context, text string, model string) ([]float32, error)
}

// AIEmbedder implements the Embedder interface using an AI embedding client.
type AIEmbedder struct {
	client embeddingClient
	model  string
}

// NewAIEmbedder creates a new AI-based embedder that uses the configured embedding model.
func NewAIEmbedder(client embeddingClient, model string) (*AIEmbedder, error) {
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

func (e *AIEmbedder) Stop() {}

// SwappableEmbedder is an Embedder whose implementation can be replaced at
// runtime (config hot reload of memory_pipeline.embedding). The memory
// service and the consolidator share one SwappableEmbedder, so a swap is
// seen by both at once.
//
// A swap is only correct while the embedding model and dimension stay the
// same (a new API key or base URL): the vector-tier collection is chosen by
// model and dimension when the QdrantClient is built. A model change
// rebuilds the memory service instead, which selects the new vector
// collection and re-embeds it from the source tier (see Swappable).
type SwappableEmbedder struct {
	cur atomic.Pointer[embedderBox]
}

// embedderBox lets atomic.Pointer hold an interface value.
type embedderBox struct{ e Embedder }

// NewSwappableEmbedder wraps e. If e is already a *SwappableEmbedder it is
// returned as is.
func NewSwappableEmbedder(e Embedder) *SwappableEmbedder {
	if s, ok := e.(*SwappableEmbedder); ok {
		return s
	}
	s := &SwappableEmbedder{}
	s.cur.Store(&embedderBox{e: e})
	return s
}

// Embed delegates to the current embedder.
func (s *SwappableEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	b := s.cur.Load()
	if b == nil || b.e == nil {
		return nil, fmt.Errorf("embedder is not configured")
	}
	return b.e.Embed(ctx, text)
}

// Swap installs next and stops the previous embedder. It returns the
// previous embedder.
func (s *SwappableEmbedder) Swap(next Embedder) Embedder {
	if next == nil {
		return nil
	}
	old := s.cur.Swap(&embedderBox{e: next})
	if old != nil && old.e != nil {
		old.e.Stop()
		return old.e
	}
	return nil
}

// Current returns the embedder in use.
func (s *SwappableEmbedder) Current() Embedder {
	if b := s.cur.Load(); b != nil {
		return b.e
	}
	return nil
}

// Stop stops the current embedder.
func (s *SwappableEmbedder) Stop() {
	if b := s.cur.Load(); b != nil && b.e != nil {
		b.e.Stop()
	}
}
