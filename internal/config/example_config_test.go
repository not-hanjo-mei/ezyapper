package config

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ezyapper/internal/filter"

	"gopkg.in/yaml.v3"
)

const exampleConfigPath = "../../examples/config.yaml.example"

// TestExampleConfigLoads loads examples/config.yaml.example through the real
// loader: the shipped example (placeholders included) must validate.
func TestExampleConfigLoads(t *testing.T) {
	quietWarnings(t)
	cfg, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("examples/config.yaml.example does not load: %v", err)
	}
	// The modality values written in the example are the defaults.
	want := ModalitiesConfig{}
	want.ApplyDefaults()
	got := cfg.AI.Modalities
	if got.Image.MaxItems != want.Image.MaxItems || got.Image.Transport != want.Image.Transport ||
		got.Audio.Mode != want.Audio.Mode || got.Audio.MaxItems != want.Audio.MaxItems ||
		got.Audio.MaxBytes != want.Audio.MaxBytes || got.Audio.MaxDurationSec != want.Audio.MaxDurationSec ||
		got.Video.MaxItems != want.Video.MaxItems || got.Video.MaxBytes != want.Video.MaxBytes ||
		got.Video.MaxDurationSec != want.Video.MaxDurationSec || got.Video.Transport != want.Video.Transport ||
		got.Sticker.Mode != want.Sticker.Mode || got.Sticker.MaxItems != want.Sticker.MaxItems ||
		got.Sticker.CacheTTLMin != want.Sticker.CacheTTLMin || got.Sticker.CacheMaxEntries != want.Sticker.CacheMaxEntries {
		t.Errorf("example modality values differ from the defaults:\n got %+v\nwant %+v", got, want)
	}
}

// TestExampleConfigUncommentedLoads enables every optional key the example
// shows commented out with "##" and loads the result: the optional values
// must be valid too.
func TestExampleConfigUncommentedLoads(t *testing.T) {
	quietWarnings(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, uncommentedExample(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("example with its ## lines enabled does not load: %v", err)
	}
}

// TestExampleConfigListsEveryKey checks that every key the loader reads
// appears in the example, either set or commented out with "##".
// Deprecated and ignored keys are documented in comments instead.
func TestExampleConfigListsEveryKey(t *testing.T) {
	var tree map[string]any
	if err := yaml.Unmarshal(uncommentedExample(t), &tree); err != nil {
		t.Fatal(err)
	}
	sections := map[string]reflect.Type{
		"core.discord":                   reflect.TypeOf(DiscordConfig{}),
		"core.ai":                        reflect.TypeOf(AIConfig{}),
		"core.decision":                  reflect.TypeOf(DecisionConfig{}),
		"memory_pipeline.embedding":      reflect.TypeOf(EmbeddingConfig{}),
		"memory_pipeline.memory":         reflect.TypeOf(MemoryConfig{}),
		"memory_pipeline.qdrant":         reflect.TypeOf(QdrantConfig{}),
		"access_control.blacklist":       reflect.TypeOf(BlacklistConfig{}),
		"access_control.whitelist":       reflect.TypeOf(WhitelistConfig{}),
		"access_control.message_filters": reflect.TypeOf(filter.Config{}),
		"operations.web":                 reflect.TypeOf(WebConfig{}),
		"operations.logging":             reflect.TypeOf(LoggingConfig{}),
		"operations.plugins":             reflect.TypeOf(PluginsConfig{}),
		"operations.mcp":                 reflect.TypeOf(MCPConfig{}),
		"operations.runtime":             reflect.TypeOf(OperationsConfig{}),
	}
	skip := map[string]bool{
		"core.ai.vision":                true, // deprecated block
		"core.ai.modalities.image.mode": true, // removed key
		"core.ai.modalities.video.mode": true, // removed key
		// Read into VisionConfig but ignored for consolidation.
		"memory_pipeline.memory.consolidation.vision.mode":         true,
		"memory_pipeline.memory.consolidation.vision.base64":       true,
		"memory_pipeline.memory.consolidation.vision.max_images":   true,
		"memory_pipeline.memory.consolidation.vision.extra_params": true,
	}
	var paths []string
	for prefix, typ := range sections {
		collectKeyPaths(typ, prefix, &paths)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if skip[p] || skipUnder(skip, p) {
			continue
		}
		if !treeHas(tree, p) {
			t.Errorf("examples/config.yaml.example does not show %s", p)
		}
	}
}

func quietWarnings(t *testing.T) {
	t.Helper()
	old := warningOutput
	warningOutput = io.Discard
	t.Cleanup(func() { warningOutput = old })
}

// uncommentedExample returns the example with the "## " marker removed from
// every line, which turns the optional keys into real ones.
func uncommentedExample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		indent := l[:len(l)-len(trimmed)]
		if strings.HasPrefix(trimmed, "## ") {
			lines[i] = indent + strings.TrimPrefix(trimmed, "## ")
		} else if trimmed == "##" {
			lines[i] = ""
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func collectKeyPaths(t reflect.Type, prefix string, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
		if strings.Contains(opts, "squash") {
			collectKeyPaths(f.Type, prefix, out)
			continue
		}
		if name == "" || name == "-" {
			continue
		}
		p := prefix + "." + name
		*out = append(*out, p)
		if f.Type.Kind() == reflect.Struct {
			collectKeyPaths(f.Type, p, out)
		}
	}
}

func skipUnder(skip map[string]bool, p string) bool {
	for s := range skip {
		if strings.HasPrefix(p, s+".") {
			return true
		}
	}
	return false
}

func treeHas(tree map[string]any, path string) bool {
	var cur any = tree
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[part]
		if !ok {
			return false
		}
	}
	return true
}
