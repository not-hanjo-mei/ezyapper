package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modalitiesBaseYAML is a complete, valid config; {{AI_EXTRA}} is replaced
// with extra keys under core.ai (indented 4 spaces).
const modalitiesBaseYAML = `core:
  discord:
    token: "test-token"
    bot_name: "TestBot"
    own_bot_id: "123"
    reply_percentage: 0.5
    cooldown_seconds: 5
    max_responses_per_minute: 10
    other_bot_policy: "ignore"
    consolidation_timeout_sec: 300
    typing_indicator_interval_sec: 5
    long_response_delay_ms: 500
    reply_truncation_length: 200
    image_cache_ttl_min: 60
    image_cache_max_entries: 100
    rate_limit:
      reset_period_seconds: 60
  ai:
    api_base_url: "https://api.openai.com"
    api_key: "sk-test"
    model: "gpt-4"
{{AI_EXTRA}}    max_tokens: 1024
    temperature: 0.8
    retry_count: 3
    timeout: 30
    max_tool_iterations: 10
    max_image_bytes: 5242880
    system_prompt: "test"
  decision:
    enabled: false
memory_pipeline:
  embedding:
    model: "text-embedding-3-small"
    timeout: 30
    retry_count: 0
  memory:
    consolidation_interval: 10
    short_term_limit: 20
    max_paginated_limit: 100
    retrieval:
      top_k: 0
      min_score: 0.0

    consolidation:
      enabled: false
      memory_search_limit: 20
    maintenance_interval_sec: 3600
    merge_cron_hour_utc: 3
    summarize_cron_day: 0
    merge_cosine_threshold: 0.85
    prune_decay_threshold: 0.1
    prune_age_days: 90
    relationship_prune_age_days: 90
    max_maintenance_llm_calls_per_day: 50
    entropy_min_unique_word_ratio: 0.15
    decay_rates:
      fact: 0.01
      episode: 0.05
      interest: 0.02
      summary: 0.005
    memory_strength_multiplier: 1.0
    scoring:
      importance_weight: 0.4
      recency_weight: 0.3
      access_weight: 0.2
      confidence_weight: 0.1
    rrf_k: 60
    context_max_memories: 20
  qdrant:
    host: "localhost"
    port: 6333
    vector_size: 1536
    retry_base_delay_ms: 100
    retry_max_delay_ms: 5000
    max_retries: 3
access_control:
  blacklist:
  whitelist:
operations:
  web:
    enabled: false
    port: 8080
    username: ""
    password: ""
  logging:
    level: "info"
    file: "ezyapper.log"
    max_size: 100
    max_backups: 3
    max_age: 7
  plugins:
    enabled: false
  mcp:
    enabled: false
    servers:
      - name: "test-server"
        type: "stdio"
        command: "echo"
        args: ["hello"]
  runtime:
    shutdown_timeout_sec: 300
    cleanup_interval_min: 5
`

func loadModalitiesYAML(t *testing.T, aiExtra string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := strings.Replace(modalitiesBaseYAML, "{{AI_EXTRA}}", aiExtra, 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoad_NoModalitiesBlock_AllOptional(t *testing.T) {
	cfg, err := loadModalitiesYAML(t, "")
	if err != nil {
		t.Fatalf("config without modalities must load: %v", err)
	}
	m := cfg.AI.Modalities
	if m.AnyEnabled() {
		t.Fatalf("no modality may be enabled by default: %+v", m)
	}
	if m.Image.Mode != ImageModeDescribe || m.Audio.Mode != AudioModeASR || m.Video.Mode != VideoModeDescribe {
		t.Fatalf("defaults not applied: %+v", m)
	}
}

func TestLoad_LegacyHybridMapsToImageDescribe(t *testing.T) {
	cfg, err := loadModalitiesYAML(t, `    vision:
      mode: "hybrid"
      model: "gpt-4-vision"
      description_prompt: "describe it"
      max_images: 3
      max_tokens: 512
      retry_count: 1
      timeout: 30
      base64: false
      api_base_url: "https://vision.example/v1"
      api_key: "vk"
`)
	if err != nil {
		t.Fatalf("legacy config must keep loading: %v", err)
	}
	img := cfg.AI.Modalities.Image
	if !img.Enabled || img.Mode != ImageModeDescribe {
		t.Fatalf("hybrid must map to enabled describe, got %+v", img)
	}
	if img.Model != "gpt-4-vision" || img.Prompt != "describe it" || img.MaxItems != 3 || img.MaxTokens != 512 ||
		img.RetryCount != 1 || img.Timeout != 30 || img.Transport != TransportURL ||
		img.APIBaseURL != "https://vision.example/v1" || img.APIKey != "vk" {
		t.Fatalf("legacy fields not carried over: %+v", img)
	}
	if cfg.AI.Vision.Mode != "" {
		t.Fatal("legacy block must be cleared after mapping so Save does not write it back")
	}
}

func TestLoad_ModalitiesBlock(t *testing.T) {
	cfg, err := loadModalitiesYAML(t, `    modalities:
      image:
        enabled: true
        mode: direct
      audio:
        enabled: true
        mode: asr
        model: "IFM/Jais-ASR"
        api_base_url: "https://api.ifm.ai/v1"
        api_key: "ak"
        max_duration_sec: 60
      video:
        enabled: true
        model: "omni"
        max_tokens: 12000
        transport: url
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := cfg.AI.Modalities
	if !m.Image.Enabled || m.Image.Mode != ImageModeDirect || m.Image.Model != "" {
		t.Fatalf("image: %+v", m.Image)
	}
	if m.Audio.Model != "IFM/Jais-ASR" || m.Audio.APIBaseURL != "https://api.ifm.ai/v1" || m.Audio.MaxDurationSec != 60 || m.Audio.MaxBytes != DefaultAudioMaxBytes {
		t.Fatalf("audio: %+v", m.Audio)
	}
	if m.Video.MaxTokens != 12000 || m.Video.Transport != TransportURL || m.Video.Prompt != DefaultVideoPrompt {
		t.Fatalf("video: %+v", m.Video)
	}
	// Endpoint fields left empty fall back to core.ai.
	r := m.Video.Resolve(cfg.AI)
	if r.APIBaseURL != cfg.AI.APIBaseURL || r.APIKey != cfg.AI.APIKey || r.Model != "omni" || r.MaxTokens != 12000 {
		t.Fatalf("resolve: %+v", r.LLMConfig)
	}
}

func TestLoad_ModalitiesWinOverLegacy(t *testing.T) {
	cfg, err := loadModalitiesYAML(t, `    vision:
      mode: "multimodal"
      model: "old"
    modalities:
      image:
        enabled: false
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.AI.Modalities.Image.Enabled || cfg.AI.Vision.Mode != "" {
		t.Fatalf("modalities must win and legacy be dropped: %+v / %+v", cfg.AI.Modalities.Image, cfg.AI.Vision)
	}
}

func TestMapLegacyVision(t *testing.T) {
	cases := []struct {
		mode    string
		enabled bool
		img     ImageMode
	}{
		{"text_only", false, ""},
		{"hybrid", true, ImageModeDescribe},
		{"multimodal", true, ImageModeDirect},
	}
	for _, c := range cases {
		ai := AIConfig{Vision: VisionConfig{Mode: c.mode, Model: "vm", Base64: true}}
		var errs []string
		if notice := mapLegacyVision(&ai, &errs); notice == "" || len(errs) > 0 {
			t.Fatalf("%s: notice=%q errs=%v", c.mode, notice, errs)
		}
		img := ai.Modalities.Image
		if img.Enabled != c.enabled || img.Mode != c.img || img.Transport != TransportBase64 || img.Model != "vm" {
			t.Fatalf("%s: got %+v", c.mode, img)
		}
	}
	ai := AIConfig{Vision: VisionConfig{Mode: "bogus"}}
	var errs []string
	mapLegacyVision(&ai, &errs)
	if len(errs) != 1 || !strings.Contains(errs[0], "core.ai.vision.mode") {
		t.Fatalf("invalid legacy mode must be reported, got %v", errs)
	}
	// Legacy model given through the embedded LLM block is honored too.
	ai = AIConfig{Vision: VisionConfig{Mode: "hybrid", LLMConfig: LLMConfig{Model: "inner"}}}
	mapLegacyVision(&ai, &errs)
	if ai.Modalities.Image.Model != "inner" {
		t.Fatalf("model from embedded block not mapped: %q", ai.Modalities.Image.Model)
	}
}

func modalityErrors(m ModalitiesConfig) string {
	cfg := &Config{AI: AIConfig{Modalities: m}}
	cfg.AI.Modalities.ApplyDefaults()
	var errs []string
	validateModalities(cfg, &errs)
	return strings.Join(errs, "\n")
}

func TestValidateModalities(t *testing.T) {
	if e := modalityErrors(ModalitiesConfig{}); e != "" {
		t.Fatalf("all-disabled config must validate: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Image: ImageModalityConfig{Enabled: true}}); !strings.Contains(e, "image.model is required") {
		t.Fatalf("describe without model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Image: ImageModalityConfig{Enabled: true, Mode: ImageModeDirect}}); e != "" {
		t.Fatalf("direct image may use core.ai.model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Enabled: true}}); !strings.Contains(e, "audio.model is required") {
		t.Fatalf("asr without model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Enabled: true, Mode: AudioModeDirect}}); e != "" {
		t.Fatalf("direct audio may use core.ai.model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Video: VideoModalityConfig{Enabled: true}}); !strings.Contains(e, "video.model is required") {
		t.Fatalf("video without model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Mode: "transcribe"}}); !strings.Contains(e, "audio.mode") {
		t.Fatalf("bad audio mode: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Video: VideoModalityConfig{Mode: "direct"}}); !strings.Contains(e, "video.mode") {
		t.Fatalf("video has no direct mode: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Image: ImageModalityConfig{Transport: "ftp"}}); !strings.Contains(e, "image.transport") {
		t.Fatalf("bad transport: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{MaxBytes: -1}}); !strings.Contains(e, "audio.max_bytes") {
		t.Fatalf("negative limit: %s", e)
	}
	conflict := ModalitiesConfig{
		Image: ImageModalityConfig{Enabled: true, Mode: ImageModeDirect, ModalityModel: ModalityModel{Model: "a"}},
		Audio: AudioModalityConfig{Enabled: true, Mode: AudioModeDirect, ModalityModel: ModalityModel{Model: "b"}},
	}
	if e := modalityErrors(conflict); !strings.Contains(e, "both in direct mode") {
		t.Fatalf("conflicting direct models must be rejected: %s", e)
	}
	conflict.Audio.Model = "a"
	if e := modalityErrors(conflict); e != "" {
		t.Fatalf("matching direct models are fine: %s", e)
	}
	conflict.Audio.Model = ""
	if e := modalityErrors(conflict); e != "" {
		t.Fatalf("a direct modality without override shares the other's model: %s", e)
	}
}
