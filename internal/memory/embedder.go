package memory

import (
	"context"
	"fmt"

	"ezyapper/internal/ai"
)

// AIEmbedder implements the Embedder interface using the AI client
type AIEmbedder struct {
	client *ai.Client
	model  string
}

// NewAIEmbedder creates a new AI-based embedder
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
