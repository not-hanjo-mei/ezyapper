package memory

import (
	"context"
	"errors"
	"testing"

	"ezyapper/internal/config"
)

type countingEmbeddingClient struct{ calls int }

func (c *countingEmbeddingClient) CreateEmbedding(ctx context.Context, text, model string) ([]float32, error) {
	c.calls++
	return []float32{1}, nil
}

// Embedding APIs answer 400 for empty input; the embedder must not send it.
func TestAIEmbedder_RejectsEmptyInputWithoutCallingAPI(t *testing.T) {
	client := &countingEmbeddingClient{}
	e, err := NewAIEmbedder(client, "emb")
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"", "   ", "\n\t"} {
		if _, err := e.Embed(context.Background(), in); !errors.Is(err, ErrEmptyEmbeddingInput) {
			t.Fatalf("Embed(%q) err = %v, want ErrEmptyEmbeddingInput", in, err)
		}
	}
	if client.calls != 0 {
		t.Fatalf("embedding API called %d times for empty input", client.calls)
	}
	if _, err := e.Embed(context.Background(), "hello"); err != nil || client.calls != 1 {
		t.Fatalf("non-empty input must be embedded: err=%v calls=%d", err, client.calls)
	}
}

type recordingEmbedder struct{ texts []string }

func (r *recordingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	r.texts = append(r.texts, text)
	return []float32{1}, nil
}
func (r *recordingEmbedder) Stop() {}

// A media-only message can leave the query empty; the search is skipped
// instead of failing.
func TestSearch_EmptyQuerySkipsSearch(t *testing.T) {
	emb := &recordingEmbedder{}
	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        50,
		TopK:                  5,
		Consolidation:         &config.ConsolidationConfig{Model: "m", SystemPrompt: "p"},
	}
	svc, err := NewService(cfg, testQdrantCfg, &QdrantClient{}, emb, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	for _, q := range []string{"", "  \n "} {
		mems, err := svc.Search(context.Background(), "u1", q, nil)
		if err != nil || len(mems) != 0 {
			t.Fatalf("Search(%q) = %v, %v; want no results and no error", q, mems, err)
		}
		mems, err = svc.HybridSearch(context.Background(), "u1", q, nil, nil)
		if err != nil || len(mems) != 0 {
			t.Fatalf("HybridSearch(%q) = %v, %v; want no results and no error", q, mems, err)
		}
	}
	if len(emb.texts) != 0 {
		t.Fatalf("embedder called with %q", emb.texts)
	}
}
