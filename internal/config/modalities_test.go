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

// stickerRequiredYAML holds the sticker keys that are always required
// (stickers are always annotated), with the example's suggested values.
const stickerRequiredYAML = `      sticker:
        max_items: 3
        cache_ttl_min: 1440
        cache_max_entries: 1000
`

// withStickerKeys adds the always-required sticker keys to aiExtra unless it
// configures the sticker section itself.
func withStickerKeys(aiExtra string) string {
	if strings.Contains(aiExtra, "      sticker:") {
		return aiExtra
	}
	if i := strings.Index(aiExtra, "    modalities:\n"); i >= 0 {
		j := i + len("    modalities:\n")
		return aiExtra[:j] + stickerRequiredYAML + aiExtra[j:]
	}
	return aiExtra + "    modalities:\n" + stickerRequiredYAML
}

// loadModalitiesYAML loads the base config with aiExtra under core.ai and
// the always-required sticker keys added (see withStickerKeys).
func loadModalitiesYAML(t *testing.T, aiExtra string) (*Config, error) {
	t.Helper()
	return loadModalitiesYAMLRaw(t, withStickerKeys(aiExtra))
}

// loadModalitiesYAMLRaw loads the base config with exactly aiExtra added.
func loadModalitiesYAMLRaw(t *testing.T, aiExtra string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := strings.Replace(modalitiesBaseYAML, "{{AI_EXTRA}}", aiExtra, 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

// There are no built-in defaults. Stickers are always annotated, so their
// item cap and cache bounds are required even with no modalities block; the
// error names each key and points at the example.
func TestLoad_NoModalitiesBlock_RequiresStickerKeys(t *testing.T) {
	_, err := loadModalitiesYAMLRaw(t, "")
	if err == nil {
		t.Fatal("a config without the sticker keys must not load")
	}
	for _, key := range []string{"core.ai.modalities.sticker.max_items", "core.ai.modalities.sticker.cache_ttl_min", "core.ai.modalities.sticker.cache_max_entries"} {
		if !strings.Contains(err.Error(), key+" is required and must be greater than 0 when stickers are always added to the message (no built-in default; see examples/config.yaml.example") {
			t.Errorf("missing error for %s: %v", key, err)
		}
	}
}

// With only the always-required sticker keys every modality is off and no
// value is filled in by the loader.
func TestLoad_StickerKeysOnly_AllDisabledNothingFilled(t *testing.T) {
	cfg, err := loadModalitiesYAML(t, "")
	if err != nil {
		t.Fatalf("config with only the sticker keys must load: %v", err)
	}
	m := cfg.AI.Modalities
	if m.AnyEnabled() {
		t.Fatalf("no modality may be enabled: %+v", m)
	}
	if m.Audio.Mode != "" || m.Image.Prompt != "" || m.Image.MaxItems != 0 || m.Image.Transport != "" ||
		m.Video.Prompt != "" || m.Video.MaxHeight != 0 || m.Sticker.Prompt != "" || m.FFmpegPath != "" {
		t.Fatalf("the loader must not fill in values: %+v", m)
	}
}

// An enabled modality must set every key it uses; each missing one is
// reported by name with a pointer to the example.
func TestLoad_EnabledModalityMissingKeys(t *testing.T) {
	cases := []struct {
		name, yaml string
		keys       []string
	}{
		{"image", "    modalities:\n      image:\n        enabled: true\n        model: vl\n",
			[]string{"image.prompt is required when core.ai.modalities.image.enabled is true", "image.max_items is required and must be greater than 0", "image.transport is required"}},
		{"audio asr", "    modalities:\n      audio:\n        enabled: true\n        model: whisper-1\n",
			[]string{"audio.mode is required", "audio.max_items is required", "audio.max_bytes is required", "audio.max_duration_sec is required"}},
		{"audio describe", "    modalities:\n      audio:\n        enabled: true\n        mode: describe\n        model: omni\n        max_items: 2\n        max_bytes: 100\n        max_duration_sec: 60\n",
			[]string{"audio.prompt is required when core.ai.modalities.audio.enabled is true and audio.mode is describe", "audio.part_type is required"}},
		{"video", "    modalities:\n      video:\n        enabled: true\n        model: omni\n        transcode: true\n",
			[]string{"video.prompt is required", "video.max_items is required", "video.max_bytes is required", "video.max_duration_sec is required", "video.transport is required", "video.max_height is required", "ffmpeg_path is required when core.ai.modalities.video.transcode=true is set"}},
		{"sticker", "    modalities:\n      sticker:\n        enabled: true\n        model: sm\n        max_items: 3\n        cache_ttl_min: 10\n        cache_max_entries: 10\n",
			[]string{"sticker.prompt is required when core.ai.modalities.sticker.enabled is true"}},
	}
	for _, c := range cases {
		_, err := loadModalitiesYAML(t, c.yaml)
		if err == nil {
			t.Fatalf("%s: expected validation errors", c.name)
		}
		for _, k := range c.keys {
			if !strings.Contains(err.Error(), "core.ai.modalities."+k) {
				t.Errorf("%s: missing %q in:\n%v", c.name, k, err)
			}
		}
		if !strings.Contains(err.Error(), "see examples/config.yaml.example") {
			t.Errorf("%s: error must point at the example: %v", c.name, err)
		}
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
	if img.Model != "gpt-4-vision" || img.Prompt != "describe it" || img.MaxItems != 3 || *img.MaxTokens != 512 ||
		*img.RetryCount != 1 || *img.Timeout != 30 || img.Transport != TransportURL ||
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
        prompt: "describe"
        max_items: 4
        transport: base64
      audio:
        enabled: true
        mode: asr
        model: "IFM/Jais-ASR"
        api_base_url: "https://api.ifm.ai/v1"
        api_key: "ak"
        max_items: 2
        max_bytes: 1000
        max_duration_sec: 60
      video:
        enabled: true
        model: "omni"
        max_tokens: 12000
        transport: url
        prompt: "describe the video"
        max_items: 1
        max_bytes: 2000
        max_duration_sec: 120
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := cfg.AI.Modalities
	if !m.Image.Enabled || m.Image.Model != "vl" || m.Image.RemovedMode != "" {
		t.Fatalf("image: %+v", m.Image)
	}
	if m.Audio.Model != "IFM/Jais-ASR" || m.Audio.APIBaseURL != "https://api.ifm.ai/v1" || m.Audio.MaxDurationSec != 60 || m.Audio.MaxBytes != 1000 {
		t.Fatalf("audio: %+v", m.Audio)
	}
	if *m.Video.MaxTokens != 12000 || m.Video.Transport != TransportURL || m.Video.Prompt != "describe the video" {
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
	// The legacy model key is mapped.
	ai = AIConfig{Vision: VisionConfig{Mode: "hybrid", ModalityModel: ModalityModel{Model: "inner"}}}
	mapLegacyVision(&ai, &errs)
	if ai.Modalities.Image.Model != "inner" {
		t.Fatalf("model from embedded block not mapped: %q", ai.Modalities.Image.Model)
	}
}

// fillSuggested sets the empty non-endpoint fields of m to the values
// suggested in examples/config.yaml.example (configtest.FillSuggested for
// this package, which configtest cannot be imported into).
func fillSuggested(m *ModalitiesConfig) {
	str := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	num := func(dst *int, v int) {
		if *dst == 0 {
			*dst = v
		}
	}
	str(&m.Image.Prompt, "Describe this image.")
	num(&m.Image.MaxItems, 4)
	if m.Image.Transport == "" {
		m.Image.Transport = TransportBase64
	}
	if m.Audio.Mode == "" {
		m.Audio.Mode = AudioModeASR
	}
	num(&m.Audio.MaxItems, 2)
	num(&m.Audio.MaxBytes, 25<<20)
	num(&m.Audio.MaxDurationSec, 600)
	if m.Audio.PartType == "" {
		m.Audio.PartType = AudioPartURL
	}
	if m.Audio.Mode == AudioModeDescribe {
		str(&m.Audio.Prompt, "Transcribe this audio.")
	}
	str(&m.Video.Prompt, "Describe this video.")
	num(&m.Video.MaxItems, 1)
	num(&m.Video.MaxBytes, 50<<20)
	num(&m.Video.MaxDurationSec, 120)
	if m.Video.Transport == "" {
		m.Video.Transport = TransportBase64
	}
	num(&m.Video.MaxHeight, 480)
	str(&m.Sticker.Prompt, "Describe this sticker.")
	num(&m.Sticker.MaxItems, 3)
	num(&m.Sticker.CacheTTLMin, 1440)
	num(&m.Sticker.CacheMaxEntries, 1000)
	str(&m.FFmpegPath, "ffmpeg")
}

func modalityErrors(m ModalitiesConfig) string {
	cfg := &Config{AI: AIConfig{Modalities: m}}
	fillSuggested(&cfg.AI.Modalities)
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
      description_prompt: "describe it"
      max_images: 2
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
	if e := modalityErrors(ModalitiesConfig{}); e != "" {
		t.Fatalf("sticker.enabled false (text only) needs no model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Sticker: StickerModalityConfig{Enabled: true}}); !strings.Contains(e, "sticker.model is required when sticker.enabled is true") {
		t.Fatalf("enabled (vision) without any model: %s", e)
	}
	if e := modalityErrors(ModalitiesConfig{Sticker: StickerModalityConfig{Enabled: true, ModalityModel: ModalityModel{Model: "sm"}}}); e != "" {
		t.Fatalf("enabled with its own model: %s", e)
	}
	withImage := ModalitiesConfig{Sticker: StickerModalityConfig{Enabled: true}, Image: ImageModalityConfig{ModalityModel: ModalityModel{Model: "vm", APIKey: "ik"}}}
	if e := modalityErrors(withImage); e != "" {
		t.Fatalf("sticker vision may borrow the image model: %s", e)
	}
	if mm := withImage.StickerVisionModel(); mm.Model != "vm" || mm.APIKey != "ik" {
		t.Fatalf("fallback: %+v", mm)
	}
	if e := modalityErrors(ModalitiesConfig{Sticker: StickerModalityConfig{Enabled: true, ModalityModel: ModalityModel{Model: "sm", Temperature: new(float32(3))}}}); !strings.Contains(e, "sticker.temperature") {
		t.Fatalf("endpoint fields are still checked when enabled: %s", e)
	}
}

// core.ai.modalities.sticker.mode was removed: an old config that sets it
// still loads, with a deprecation warning; mode text with enabled true also
// warns that the mode is ignored (enabled now reads the image).
func TestLoad_RemovedStickerModeWarns(t *testing.T) {
	var buf bytes.Buffer
	old := warningOutput
	warningOutput = &buf
	defer func() { warningOutput = old }()
	sticker := func(body string) string {
		return "    modalities:\n      image:\n        model: vm\n      sticker:\n        prompt: describe\n        max_items: 3\n        cache_ttl_min: 1440\n        cache_max_entries: 1000\n" + body
	}

	cases := []struct {
		name, yaml string
		enabled    bool
		ignored    bool
	}{
		{"text+enabled", sticker("        enabled: true\n        mode: text\n"), true, true},
		{"vision+enabled", sticker("        enabled: true\n        mode: vision\n"), true, false},
		{"text+disabled", sticker("        enabled: false\n        mode: text\n"), false, false},
		{"vision+disabled", sticker("        mode: vision\n"), false, false},
	}
	for _, c := range cases {
		buf.Reset()
		cfg, err := loadModalitiesYAML(t, c.yaml)
		if err != nil {
			t.Fatalf("%s: a config that still sets sticker.mode must load: %v", c.name, err)
		}
		if cfg.AI.Modalities.Sticker.Enabled != c.enabled {
			t.Fatalf("%s: enabled=%v", c.name, cfg.AI.Modalities.Sticker.Enabled)
		}
		out := buf.String()
		if !strings.Contains(out, "core.ai.modalities.sticker.mode is deprecated and ignored") {
			t.Fatalf("%s: missing deprecation warning, got %q", c.name, out)
		}
		if got := strings.Contains(out, `sticker.mode "text" is ignored`); got != c.ignored {
			t.Fatalf("%s: mode-ignored warning=%v, got %q", c.name, got, out)
		}
	}

	buf.Reset()
	if _, err := loadModalitiesYAML(t, sticker("        enabled: true\n")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "sticker.mode") {
		t.Fatalf("no warning expected without the key, got %q", buf.String())
	}
	// An old "enabled + mode text" config with no model anywhere now fails
	// with an error that says how to keep text only.
	if _, err := loadModalitiesYAML(t, "    modalities:\n      sticker:\n        enabled: true\n        mode: text\n"); err == nil || !strings.Contains(err.Error(), "set sticker.enabled to false") {
		t.Fatalf("expected model-required error, got %v", err)
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
