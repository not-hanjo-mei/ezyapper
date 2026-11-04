package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Inheritance between config keys is decided by presence, not by value: a
// key that is absent inherits, a key that is set (0 and {} included) is
// used as is, and an explicit 0 that would be invalid (a timeout) is
// rejected. These tests load the shipped example config with keys set or
// removed through the yaml tree.

// exampleTree returns the shipped example config as a yaml tree.
func exampleTree(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "config.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := yaml.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	return tree
}

// setPath sets the dotted path in tree, creating mappings on the way. A nil
// val deletes the key.
func setPath(t *testing.T, tree map[string]any, path string, val any) {
	t.Helper()
	parts := strings.Split(path, ".")
	m := tree
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	last := parts[len(parts)-1]
	if val == nil {
		delete(m, last)
		return
	}
	m[last] = val
}

// loadTree writes tree to a temp file and loads it.
func loadTree(t *testing.T, tree map[string]any) (*Config, error) {
	t.Helper()
	data, err := yaml.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

const imgPath = "core.ai.modalities.image."

// enableImage turns the image modality on with the example's suggested
// values and no model override keys.
func enableImage(t *testing.T, tree map[string]any) {
	t.Helper()
	setPath(t, tree, imgPath+"enabled", true)
	setPath(t, tree, imgPath+"model", "vl")
	setPath(t, tree, imgPath+"prompt", "describe")
	setPath(t, tree, imgPath+"max_items", 4)
	setPath(t, tree, imgPath+"transport", "base64")
}

func mustLoad(t *testing.T, tree map[string]any) *Config {
	t.Helper()
	cfg, err := loadTree(t, tree)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func TestPresence_ModalityNumericAbsentVsZero(t *testing.T) {
	tree := exampleTree(t)
	enableImage(t, tree)
	setPath(t, tree, "core.ai.temperature", 0.8)
	setPath(t, tree, "core.ai.retry_count", 3)
	cfg := mustLoad(t, tree)
	img := cfg.AI.Modalities.Image
	if img.Temperature != nil || img.RetryCount != nil || img.Timeout != nil || img.MaxTokens != nil {
		t.Fatalf("absent keys must load as nil, got %+v", img.ModalityModel)
	}
	r := img.Resolve(cfg.AI)
	if r.Temperature != 0.8 || r.RetryCount != 3 || r.Timeout != cfg.AI.Timeout || r.MaxTokens != cfg.AI.MaxTokens {
		t.Fatalf("absent keys must inherit core.ai, got %+v", r.LLMConfig)
	}

	setPath(t, tree, imgPath+"temperature", 0)
	setPath(t, tree, imgPath+"retry_count", 0)
	cfg = mustLoad(t, tree)
	img = cfg.AI.Modalities.Image
	if img.Temperature == nil || *img.Temperature != 0 || img.RetryCount == nil || *img.RetryCount != 0 {
		t.Fatalf("explicit 0 must load as a set 0, got %+v", img.ModalityModel)
	}
	r = img.Resolve(cfg.AI)
	if r.Temperature != 0 || r.RetryCount != 0 {
		t.Fatalf("explicit 0 must be used as is, got temperature %v retry_count %d", r.Temperature, r.RetryCount)
	}
}

func TestPresence_ModalityExplicitZeroTimeoutRejected(t *testing.T) {
	for _, key := range []string{"timeout", "max_tokens"} {
		tree := exampleTree(t)
		enableImage(t, tree)
		setPath(t, tree, imgPath+key, 0)
		_, err := loadTree(t, tree)
		want := imgPath + key + " must be greater than 0 (omit the key to use core.ai." + key + ")"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("explicit %s 0 must be rejected with %q, got %v", key, want, err)
		}
	}
}

func TestPresence_StickerInheritsImageOnlyWhenAbsent(t *testing.T) {
	tree := exampleTree(t)
	enableImage(t, tree)
	setPath(t, tree, imgPath+"temperature", 0.5)
	setPath(t, tree, imgPath+"retry_count", 2)
	setPath(t, tree, "core.ai.modalities.sticker.enabled", true)
	setPath(t, tree, "core.ai.modalities.sticker.prompt", "sticker")
	cfg := mustLoad(t, tree)
	sv := cfg.AI.Modalities.StickerVisionModel()
	if *sv.Temperature != 0.5 || *sv.RetryCount != 2 || sv.Model != "vl" {
		t.Fatalf("absent sticker keys must inherit image's, got %+v", sv)
	}

	setPath(t, tree, "core.ai.modalities.sticker.temperature", 0)
	setPath(t, tree, "core.ai.modalities.sticker.retry_count", 0)
	cfg = mustLoad(t, tree)
	r := cfg.AI.Modalities.StickerVisionModel().Resolve(cfg.AI)
	if r.Temperature != 0 || r.RetryCount != 0 {
		t.Fatalf("explicit sticker 0 must win over image's, got %+v", r.LLMConfig)
	}

	setPath(t, tree, "core.ai.modalities.sticker.timeout", 0)
	if _, err := loadTree(t, tree); err == nil || !strings.Contains(err.Error(), "sticker.timeout must be greater than 0 (omit the key to use core.ai.modalities.image.timeout)") {
		t.Fatalf("explicit sticker timeout 0 must be rejected, got %v", err)
	}
}

func TestPresence_ModalityExtraParamsAbsentVsEmpty(t *testing.T) {
	tree := exampleTree(t)
	enableImage(t, tree)
	setPath(t, tree, "core.ai.extra_params", map[string]any{"top_p": 0.9})
	cfg := mustLoad(t, tree)
	if cfg.AI.Modalities.Image.ExtraParams != nil {
		t.Fatalf("absent image.extra_params must load as nil, got %+v", cfg.AI.Modalities.Image.ExtraParams)
	}
	if r := cfg.AI.Modalities.Image.Resolve(cfg.AI); r.ExtraParams["top_p"] != 0.9 {
		t.Fatalf("absent image.extra_params must inherit core.ai's, got %+v", r.ExtraParams)
	}

	setPath(t, tree, imgPath+"extra_params", map[string]any{})
	cfg = mustLoad(t, tree)
	if cfg.AI.Modalities.Image.ExtraParams == nil {
		t.Fatal("explicit image.extra_params {} must load as an empty, non-nil map")
	}
	if r := cfg.AI.Modalities.Image.Resolve(cfg.AI); len(r.ExtraParams) != 0 {
		t.Fatalf("explicit image.extra_params {} must send none, got %+v", r.ExtraParams)
	}
}

func TestPresence_EmbeddingExtraParamsAbsentVsEmpty(t *testing.T) {
	tree := exampleTree(t)
	setPath(t, tree, "core.ai.extra_params", map[string]any{"top_p": 0.9})
	setPath(t, tree, "memory_pipeline.embedding.extra_params", nil)
	cfg := mustLoad(t, tree)
	if cfg.Embedding.ExtraParams != nil {
		t.Fatalf("absent embedding.extra_params must load as nil (inherit), got %+v", cfg.Embedding.ExtraParams)
	}

	setPath(t, tree, "memory_pipeline.embedding.extra_params", map[string]any{})
	cfg = mustLoad(t, tree)
	if cfg.Embedding.ExtraParams == nil || len(cfg.Embedding.ExtraParams) != 0 {
		t.Fatalf("explicit embedding.extra_params {} must load as an empty, non-nil map, got %#v", cfg.Embedding.ExtraParams)
	}

	setPath(t, tree, "memory_pipeline.embedding.extra_params", nil)
	tree["memory_pipeline"].(map[string]any)["embedding"].(map[string]any)["extra_params"] = (*int)(nil) // explicit null
	cfg = mustLoad(t, tree)
	if cfg.Embedding.ExtraParams != nil {
		t.Fatalf("explicit null embedding.extra_params must count as absent, got %+v", cfg.Embedding.ExtraParams)
	}
}

const consPath = "memory_pipeline.memory.consolidation."

func enableConsolidation(t *testing.T, tree map[string]any) {
	t.Helper()
	setPath(t, tree, consPath+"enabled", true)
	setPath(t, tree, consPath+"max_tokens", 1000)
	setPath(t, tree, consPath+"retry_count", 5)
	setPath(t, tree, consPath+"timeout", 30)
	setPath(t, tree, consPath+"system_prompt", "consolidate")
}

func TestPresence_ConsolidationTemperatureAbsentVsZero(t *testing.T) {
	tree := exampleTree(t)
	enableConsolidation(t, tree)
	cfg := mustLoad(t, tree)
	if cfg.Memory.Consolidation.Temperature != nil {
		t.Fatalf("absent consolidation.temperature must load as nil, got %v", *cfg.Memory.Consolidation.Temperature)
	}

	setPath(t, tree, consPath+"temperature", 0)
	cfg = mustLoad(t, tree)
	if cfg.Memory.Consolidation.Temperature == nil || *cfg.Memory.Consolidation.Temperature != 0 {
		t.Fatalf("explicit consolidation.temperature 0 must load as a set 0, got %v", cfg.Memory.Consolidation.Temperature)
	}

	setPath(t, tree, consPath+"temperature", 3)
	if _, err := loadTree(t, tree); err == nil || !strings.Contains(err.Error(), "consolidation.temperature must be between 0 and 2") {
		t.Fatalf("out-of-range consolidation.temperature must be rejected, got %v", err)
	}
}

func TestPresence_ConsolidationVision(t *testing.T) {
	tree := exampleTree(t)
	enableConsolidation(t, tree)
	setPath(t, tree, consPath+"vision.temperature", 0)
	setPath(t, tree, consPath+"vision.retry_count", 0)
	cfg := mustLoad(t, tree)
	v := cfg.Memory.Consolidation.Vision
	if v.Temperature == nil || *v.Temperature != 0 || v.RetryCount == nil || *v.RetryCount != 0 || v.Timeout != nil {
		t.Fatalf("consolidation.vision presence not kept: %+v", v.ModalityModel)
	}

	setPath(t, tree, consPath+"vision.timeout", 0)
	if _, err := loadTree(t, tree); err == nil || !strings.Contains(err.Error(), "consolidation.vision.timeout must be greater than 0 (omit the key to use core.ai.modalities.image.timeout)") {
		t.Fatalf("explicit consolidation.vision.timeout 0 must be rejected, got %v", err)
	}
}

func TestPresence_RetrievalLimitsAbsentVsZero(t *testing.T) {
	const rp = "memory_pipeline.memory.retrieval."
	tree := exampleTree(t)
	setPath(t, tree, rp+"top_k", 7)
	setPath(t, tree, rp+"max_mentioned_memories", nil)
	setPath(t, tree, rp+"max_channel_memories", nil)
	cfg := mustLoad(t, tree)
	if got := cfg.Memory.Retrieval.MentionedLimit(); got != 7 {
		t.Fatalf("absent max_mentioned_memories must use top_k 7, got %d", got)
	}
	if got := cfg.Memory.Retrieval.ChannelLimit(); got != 7 {
		t.Fatalf("absent max_channel_memories must use top_k 7, got %d", got)
	}

	setPath(t, tree, rp+"max_mentioned_memories", 0)
	setPath(t, tree, rp+"max_channel_memories", 0)
	cfg = mustLoad(t, tree)
	if got := cfg.Memory.Retrieval.MentionedLimit(); got != 0 {
		t.Fatalf("explicit max_mentioned_memories 0 must mean none, got %d", got)
	}
	if got := cfg.Memory.Retrieval.ChannelLimit(); got != 0 {
		t.Fatalf("explicit max_channel_memories 0 must mean none, got %d", got)
	}
}

// Hot reload re-reads the file with Load, and the dashboard's Save copies
// the running config, validates it and writes it with Save. Both must keep
// absent keys absent and explicit zeros (and {}) as zeros.
func TestPresence_SaveAndReloadRoundTrip(t *testing.T) {
	tree := exampleTree(t)
	enableImage(t, tree)
	enableConsolidation(t, tree)
	setPath(t, tree, imgPath+"temperature", 0)
	setPath(t, tree, imgPath+"extra_params", map[string]any{})
	setPath(t, tree, consPath+"temperature", 0)
	setPath(t, tree, "memory_pipeline.memory.retrieval.max_mentioned_memories", 0)
	setPath(t, tree, "memory_pipeline.memory.retrieval.max_channel_memories", nil)
	setPath(t, tree, "memory_pipeline.embedding.extra_params", map[string]any{})
	cfg := mustLoad(t, tree)

	check := func(stage string, c *Config) {
		t.Helper()
		img := c.AI.Modalities.Image
		if img.Temperature == nil || *img.Temperature != 0 {
			t.Fatalf("%s: explicit image.temperature 0 lost: %v", stage, img.Temperature)
		}
		if img.RetryCount != nil || img.Timeout != nil || img.MaxTokens != nil {
			t.Fatalf("%s: absent image keys became set: %+v", stage, img.ModalityModel)
		}
		if img.ExtraParams == nil || len(img.ExtraParams) != 0 {
			t.Fatalf("%s: explicit image.extra_params {} lost: %#v", stage, img.ExtraParams)
		}
		if s := c.AI.Modalities.Sticker; s.Temperature != nil || s.ExtraParams != nil {
			t.Fatalf("%s: absent sticker keys became set: %+v", stage, s.ModalityModel)
		}
		if ct := c.Memory.Consolidation.Temperature; ct == nil || *ct != 0 {
			t.Fatalf("%s: explicit consolidation.temperature 0 lost: %v", stage, ct)
		}
		if v := c.Memory.Consolidation.Vision; v.Temperature != nil || v.Timeout != nil {
			t.Fatalf("%s: absent consolidation.vision keys became set: %+v", stage, v.ModalityModel)
		}
		r := c.Memory.Retrieval
		if r.MaxMentionedMemories == nil || *r.MaxMentionedMemories != 0 || r.MaxChannelMemories != nil {
			t.Fatalf("%s: retrieval limits not kept: mentioned %v channel %v", stage, r.MaxMentionedMemories, r.MaxChannelMemories)
		}
		if c.Embedding.ExtraParams == nil || len(c.Embedding.ExtraParams) != 0 {
			t.Fatalf("%s: explicit embedding.extra_params {} lost: %#v", stage, c.Embedding.ExtraParams)
		}
	}
	check("load", cfg)

	// Hot reload: the same file loaded again.
	reloaded, err := Load(cfg.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	check("hot reload", reloaded)

	// Dashboard Save: copy, change an unrelated key, validate, save, load.
	next := *reloaded
	next.Discord.BotName = "Renamed"
	if err := Validate(&next); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := next.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(next.Path())
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := yaml.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	sticker := saved["core"].(map[string]any)["ai"].(map[string]any)["modalities"].(map[string]any)["sticker"].(map[string]any)
	for _, k := range []string{"temperature", "retry_count", "timeout", "max_tokens", "extra_params"} {
		if _, ok := sticker[k]; ok {
			t.Fatalf("Save wrote absent key sticker.%s:\n%s", k, data)
		}
	}
	afterSave, err := Load(next.Path())
	if err != nil {
		t.Fatalf("load after save: %v", err)
	}
	check("after Save", afterSave)
	if afterSave.Discord.BotName != "Renamed" {
		t.Fatalf("saved change lost: %q", afterSave.Discord.BotName)
	}
}
