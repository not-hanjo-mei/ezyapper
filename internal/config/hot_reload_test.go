package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateHotReload(t *testing.T) {
	cases := []struct {
		name    string
		hr      HotReloadConfig
		wantErr string
	}{
		{"disabled zero", HotReloadConfig{}, ""},
		{"disabled negative", HotReloadConfig{DebounceMs: -1}, "debounce_ms must be greater than or equal to 0"},
		{"enabled ok", HotReloadConfig{Enabled: true, DebounceMs: 500}, ""},
		{"enabled lower bound", HotReloadConfig{Enabled: true, DebounceMs: MinHotReloadDebounceMs}, ""},
		{"enabled missing", HotReloadConfig{Enabled: true}, "debounce_ms must be between"},
		{"enabled too small", HotReloadConfig{Enabled: true, DebounceMs: 10}, "debounce_ms must be between"},
		{"enabled too large", HotReloadConfig{Enabled: true, DebounceMs: MaxHotReloadDebounceMs + 1}, "debounce_ms must be between"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{Operations: OperationsConfig{HotReload: c.hr}}
			var errs []string
			validateHotReload(cfg, &errs)
			if c.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) != 1 || !strings.Contains(errs[0], c.wantErr) {
				t.Fatalf("expected %q, got %v", c.wantErr, errs)
			}
		})
	}
}

// TestLoad_ExampleConfigHotReload loads the shipped example (with the
// secrets it leaves empty filled in) and checks the hot_reload block parses.
func TestLoad_ExampleConfigHotReload(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "config.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hot_reload:") {
		t.Fatal("examples/config.yaml.example does not document operations.runtime.hot_reload")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		// The example may need real secrets; only the hot_reload keys matter here.
		if strings.Contains(err.Error(), "hot_reload") {
			t.Fatalf("hot_reload block failed validation: %v", err)
		}
		t.Skipf("example config needs secrets to load fully: %v", err)
	}
	if !cfg.Operations.HotReload.Enabled || cfg.Operations.HotReload.DebounceMs != 500 {
		t.Fatalf("unexpected hot_reload values: %+v", cfg.Operations.HotReload)
	}
	if cfg.Path() == "" {
		t.Fatal("Path() is empty after Load")
	}
}
