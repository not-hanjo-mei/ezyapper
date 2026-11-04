package app

import (
	"os"
	"testing"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
)

func TestMain(m *testing.M) {
	logger.Init(logger.Config{Level: "info", File: os.DevNull})
	os.Exit(m.Run())
}

func TestBuildEmbeddingAIConfig_UsesEmbeddingOverrides(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			LLMConfig: config.LLMConfig{
				APIBaseURL:  "https://main.example/v1",
				APIKey:      "main-key",
				RetryCount:  3,
				Timeout:     60,
				ExtraParams: map[string]interface{}{"source": "main"},
			},
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
			LLMConfig: config.LLMConfig{
				APIBaseURL:  "https://main.example/v1",
				APIKey:      "main-key",
				RetryCount:  3,
				Timeout:     45,
				ExtraParams: map[string]interface{}{"source": "main"},
			},
		},
		Embedding: config.EmbeddingConfig{
			RetryCount: 0,
			Timeout:    20,
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
	// embedding.timeout is the embedding's own (required when memory is
	// on); it is never taken from core.ai.
	if embeddingAI.Timeout != 20 {
		t.Fatalf("expected the embedding's own timeout, got %d", embeddingAI.Timeout)
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

	svc, err := BuildMemoryService(cfg, nil)
	if err != nil {
		t.Fatalf("BuildMemoryService returned error: %v", err)
	}
	if _, ok := svc.(*memory.NoopService); !ok {
		t.Fatalf("expected NoopService when memory switches are disabled, got %T", svc)
	}
}

// embedding.extra_params inherits core.ai.extra_params only when the key is
// absent; an explicit {} sends none.
func TestBuildEmbeddingAIConfig_ExtraParamsAbsentVsEmpty(t *testing.T) {
	base := config.AIConfig{LLMConfig: config.LLMConfig{ExtraParams: map[string]any{"top_p": 0.9}}}

	absent := buildEmbeddingAIConfig(&config.Config{AI: base, Embedding: config.EmbeddingConfig{Timeout: 5}})
	if absent.ExtraParams["top_p"] != 0.9 {
		t.Fatalf("absent embedding.extra_params must inherit core.ai's, got %+v", absent.ExtraParams)
	}

	empty := buildEmbeddingAIConfig(&config.Config{AI: base, Embedding: config.EmbeddingConfig{Timeout: 5, ExtraParams: map[string]any{}}})
	if empty.ExtraParams == nil || len(empty.ExtraParams) != 0 {
		t.Fatalf("explicit embedding.extra_params {} must send none, got %+v", empty.ExtraParams)
	}
}

// consolidation.temperature inherits core.ai.temperature only when the key
// is absent; an explicit 0 is sent as 0.
func TestBuildConsolidationAIConfig_TemperatureAbsentVsZero(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{LLMConfig: config.LLMConfig{Model: "chat", Temperature: 0.8}},
		Memory: config.MemoryConfig{Consolidation: config.ConsolidationConfig{
			Enabled: true, MaxTokens: 100, RetryCount: 1, Timeout: 30,
		}},
	}
	if got := buildConsolidationAIConfig(cfg).Temperature; got != 0.8 {
		t.Fatalf("absent consolidation.temperature must inherit core.ai's 0.8, got %v", got)
	}
	cfg.Memory.Consolidation.Temperature = new(float32(0))
	got := buildConsolidationAIConfig(cfg)
	if got.Temperature != 0 {
		t.Fatalf("explicit consolidation.temperature 0 must be used, got %v", got.Temperature)
	}
	if got.Timeout != 30 || got.RetryCount != 1 || got.MaxTokens != 100 || got.Model != "chat" {
		t.Fatalf("unexpected consolidation client config: %+v", got.LLMConfig)
	}
}

// consolidation.vision keys override image's when present (explicit 0 and
// {} included) and leave image's (then core.ai's) when absent.
func TestBuildConsolidationImageAI_Presence(t *testing.T) {
	cfg := &config.Config{AI: config.AIConfig{LLMConfig: config.LLMConfig{Temperature: 0.8, RetryCount: 3, Timeout: 60, ExtraParams: map[string]any{"a": 1}}}}
	cfg.AI.Modalities.Image = config.ImageModalityConfig{Enabled: true, Prompt: "p", ModalityModel: config.ModalityModel{Model: "vl", Temperature: new(float32(0.5))}}

	r := buildConsolidationImageAI(cfg)
	resolved := r.Modalities.Image.Resolve(r)
	if resolved.Temperature != 0.5 || resolved.RetryCount != 3 || resolved.ExtraParams["a"] != 1 {
		t.Fatalf("absent vision keys must keep image's then core.ai's, got %+v", resolved.LLMConfig)
	}

	cfg.Memory.Consolidation.Vision = config.ConsolidationVisionConfig{ModalityModel: config.ModalityModel{
		Temperature: new(float32(0)), RetryCount: new(0), ExtraParams: map[string]any{},
	}}
	r = buildConsolidationImageAI(cfg)
	resolved = r.Modalities.Image.Resolve(r)
	if resolved.Temperature != 0 || resolved.RetryCount != 0 || len(resolved.ExtraParams) != 0 || resolved.Model != "vl" {
		t.Fatalf("explicit 0 / {} vision keys must be used as is, got %+v", resolved.LLMConfig)
	}
}
