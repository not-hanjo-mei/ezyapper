package main

import (
	"testing"

	"ezyapper/internal/config"
	"ezyapper/internal/memory"
)

func TestBuildEmbeddingAIConfig_UsesEmbeddingOverrides(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			APIBaseURL:  "https://main.example/v1",
			APIKey:      "main-key",
			RetryCount:  3,
			Timeout:     60,
			ExtraParams: map[string]interface{}{"source": "main"},
		},
		Embedding: config.EmbeddingConfig{
			APIBaseURL:  "https://embed.example/v1",
			APIKey:      "embed-key",
			RetryCount:  1,
			Timeout:     15,
			ExtraParams: map[string]interface{}{"source": "embed"},
		},
	}

	embeddingAI := buildEmbeddingAIConfig(cfg)
	if embeddingAI.APIBaseURL != "https://embed.example/v1" {
		t.Fatalf("expected embedding API base URL override, got %q", embeddingAI.APIBaseURL)
	}
	if embeddingAI.APIKey != "embed-key" {
		t.Fatalf("expected embedding API key override, got %q", embeddingAI.APIKey)
	}
	if embeddingAI.RetryCount != 1 {
		t.Fatalf("expected embedding retry_count=1, got %d", embeddingAI.RetryCount)
	}
	if embeddingAI.Timeout != 15 {
		t.Fatalf("expected embedding timeout=15, got %d", embeddingAI.Timeout)
	}
	if embeddingAI.ExtraParams["source"] != "embed" {
		t.Fatalf("expected embedding extra params, got %+v", embeddingAI.ExtraParams)
	}
}

func TestBuildEmbeddingAIConfig_FallsBackToMainAI(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			APIBaseURL:  "https://main.example/v1",
			APIKey:      "main-key",
			RetryCount:  3,
			Timeout:     45,
			ExtraParams: map[string]interface{}{"source": "main"},
		},
		Embedding: config.EmbeddingConfig{
			RetryCount: 0,
			Timeout:    0,
		},
	}

	embeddingAI := buildEmbeddingAIConfig(cfg)
	if embeddingAI.APIBaseURL != "https://main.example/v1" {
		t.Fatalf("expected main API base URL fallback, got %q", embeddingAI.APIBaseURL)
	}
	if embeddingAI.APIKey != "main-key" {
		t.Fatalf("expected main API key fallback, got %q", embeddingAI.APIKey)
	}
	if embeddingAI.RetryCount != 0 {
		t.Fatalf("expected embedding retry_count override, got %d", embeddingAI.RetryCount)
	}
	if embeddingAI.Timeout != 45 {
		t.Fatalf("expected main timeout fallback, got %d", embeddingAI.Timeout)
	}
	if embeddingAI.ExtraParams["source"] != "main" {
		t.Fatalf("expected main extra params fallback, got %+v", embeddingAI.ExtraParams)
	}
}

func TestInitMemoryService_DisabledBySwitchesUsesNoop(t *testing.T) {
	cfg := &config.Config{
		Memory: config.MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             config.RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation:         config.ConsolidationConfig{Enabled: false},
		},
	}

	svc, err := initMemoryService(cfg)
	if err != nil {
		t.Fatalf("initMemoryService returned error: %v", err)
	}
	if _, ok := svc.(*memory.NoopService); !ok {
		t.Fatalf("expected NoopService when memory switches are disabled, got %T", svc)
	}
}
