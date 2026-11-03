package config

import (
	"bytes"
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
	if m.Audio.Mode != AudioModeASR || m.Image.Prompt != DefaultImagePrompt || m.Video.Prompt != DefaultVideoPrompt {
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
	if !img.Enabled {
		t.Fatalf("hybrid must map to an enabled image modality, got %+v", img)
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
        model: "vl"
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
	if !m.Image.Enabled || m.Image.Model != "vl" || m.Image.RemovedMode != "" {
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

// A config that still says mode: direct fails to load with a removal hint.
func TestLoad_RemovedDirectModeRejected(t *testing.T) {
	_, err := loadModalitiesYAML(t, `    modalities:
      image:
        enabled: true
        mode: direct
        model: "vl"
`)
	if err == nil || !strings.Contains(err.Error(), "core.ai.modalities.image.mode") || !strings.Contains(err.Error(), "remove the mode field") {
		t.Fatalf("expected image.mode removal error, got %v", err)
	}
	_, err = loadModalitiesYAML(t, `    modalities:
      audio:
        enabled: true
        mode: direct
`)
	if err == nil || !strings.Contains(err.Error(), `core.ai.modalities.audio.mode "direct" was removed`) {
		t.Fatalf("expected audio.mode removal error, got %v", err)
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
	// multimodal used to send images to the reply model; the reply model is
	// text-only now, so it maps onto describe like hybrid.
	cases := []struct {
		mode    string
		enabled bool
	}{
		{"text_only", false},
		{"hybrid", true},
		{"multimodal", true},
	}
	for _, c := range cases {
		ai := AIConfig{Vision: VisionConfig{Mode: c.mode, Model: "vm", Base64: true}}
		var errs []string
		if notice := mapLegacyVision(&ai, &errs); notice == "" || len(errs) > 0 {
			t.Fatalf("%s: notice=%q errs=%v", c.mode, notice, errs)
		}
		img := ai.Modalities.Image
		if img.Enabled != c.enabled || img.RemovedMode != "" || img.Transport != TransportBase64 || img.Model != "vm" {
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
	if e := modalityErrors(ModalitiesConfig{Image: ImageModalityConfig{RemovedMode: "direct"}}); !strings.Contains(e, "image.mode") || !strings.Contains(e, "remove the mode field") {
		t.Fatalf("image.mode direct must be rejected with a removal hint: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Image: ImageModalityConfig{RemovedMode: "describe"}}); !strings.Contains(e, "image.mode") {
		t.Fatalf("image.mode no longer exists: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Enabled: true}}); !strings.Contains(e, "audio.model is required") {
		t.Fatalf("asr without model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Enabled: true, Mode: AudioModeDescribe}}); !strings.Contains(e, "audio.model is required") {
		t.Fatalf("audio describe without model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Enabled: true, Mode: AudioModeDescribe, ModalityModel: ModalityModel{Model: "omni"}}}); e != "" {
		t.Fatalf("audio describe with a model is valid: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Mode: "direct"}}); !strings.Contains(e, `audio.mode "direct" was removed`) {
		t.Fatalf("audio.mode direct must be rejected with a removal hint: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Video: VideoModalityConfig{Enabled: true}}); !strings.Contains(e, "video.model is required") {
		t.Fatalf("video without model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Mode: "transcribe"}}); !strings.Contains(e, "audio.mode") {
		t.Fatalf("bad audio mode: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Video: VideoModalityConfig{RemovedMode: "direct"}}); !strings.Contains(e, "video.mode") {
		t.Fatalf("video.mode no longer exists: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Image: ImageModalityConfig{Transport: "ftp"}}); !strings.Contains(e, "image.transport") {
		t.Fatalf("bad transport: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{MaxBytes: -1}}); !strings.Contains(e, "audio.max_bytes") {
		t.Fatalf("negative limit: %s", e)
	}
}

func TestSave_RoundTripsModalities(t *testing.T) {
	cfg, err := loadModalitiesYAML(t, `    vision:
      mode: "hybrid"
      model: "vm"
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(cfg.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\n    vision:") {
		t.Fatalf("saved config must not contain the legacy vision block:\n%s", data)
	}
	again, err := Load(cfg.configPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !again.AI.Modalities.Image.Enabled || again.AI.Modalities.Image.Model != "vm" {
		t.Fatalf("modalities lost in round trip: %+v", again.AI.Modalities.Image)
	}
}

func TestValidateStickerModality(t *testing.T) {
	if e := modalityErrors(ModalitiesConfig{Sticker: StickerModalityConfig{Enabled: true}}); e != "" {
		t.Fatalf("text mode needs no model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Sticker: StickerModalityConfig{Enabled: true, Mode: StickerModeVision}}); !strings.Contains(e, "sticker.model is required") {
		t.Fatalf("vision without any model: %s", e)
	}
	withImage := ModalitiesConfig{Sticker: StickerModalityConfig{Enabled: true, Mode: StickerModeVision}, Image: ImageModalityConfig{ModalityModel: ModalityModel{Model: "vm", APIKey: "ik"}}}
	if e := modalityErrors(withImage); e != "" {
		t.Fatalf("sticker vision may borrow the image model: %s", e)
	}
	if mm := withImage.StickerVisionModel(); mm.Model != "vm" || mm.APIKey != "ik" {
		t.Fatalf("fallback: %+v", mm)
	}
	if e := modalityErrors(ModalitiesConfig{Sticker: StickerModalityConfig{Mode: "ocr"}}); !strings.Contains(e, "sticker.mode") {
		t.Fatalf("bad mode: %s", e)
	}
}

func TestValidateFFmpegOptions(t *testing.T) {
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{Transcode: "flac"}}); !strings.Contains(e, "audio.transcode") {
		t.Fatalf("bad transcode: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{PartType: AudioPartInput}}); !strings.Contains(e, "input_audio only accepts mp3 or wav") {
		t.Fatalf("input_audio without transcode: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Audio: AudioModalityConfig{PartType: AudioPartInput, Transcode: "wav"}}); e != "" {
		t.Fatalf("input_audio with transcode: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Video: VideoModalityConfig{Transcode: true, Transport: TransportURL}}); !strings.Contains(e, "video.transcode needs transport base64") {
		t.Fatalf("transcode+url: %s", e)
	}
	m := ModalitiesConfig{Audio: AudioModalityConfig{Enabled: true, Transcode: "mp3"}, Video: VideoModalityConfig{Transcode: true}}
	if got := m.FFmpegRequirements(); len(got) != 1 || got[0] != "core.ai.modalities.audio.transcode=mp3" {
		t.Fatalf("only enabled modalities require ffmpeg: %v", got)
	}
	if got := (ModalitiesConfig{}).FFmpegRequirements(); len(got) != 0 {
		t.Fatalf("default config needs no ffmpeg: %v", got)
	}
}

// core.ai.require_image_content_type was removed; an old config that still
// sets it must load and get a deprecation warning.
func TestLoad_RemovedRequireImageContentTypeWarns(t *testing.T) {
	var buf bytes.Buffer
	old := warningOutput
	warningOutput = &buf
	defer func() { warningOutput = old }()

	if _, err := loadModalitiesYAML(t, "    require_image_content_type: true\n"); err != nil {
		t.Fatalf("a config that still sets the removed key must load: %v", err)
	}
	if !strings.Contains(buf.String(), "core.ai.require_image_content_type is deprecated") {
		t.Fatalf("missing deprecation warning, got %q", buf.String())
	}

	buf.Reset()
	if _, err := loadModalitiesYAML(t, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "require_image_content_type") {
		t.Fatalf("no warning expected without the key, got %q", buf.String())
	}
}
