package memory

import (
	"context"
	"fmt"
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
