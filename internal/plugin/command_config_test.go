package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helperReadConfig is the "read-config" command of TestManagerHelperProcess.
// It behaves like the command-runtime example plugins (datetime-zig,
// datetime-java): it reads $EZYAPPER_PLUGIN_CONFIG, expects a single
// "value: <text>" line, prints the value and exits 0. An invalid file is
// reported on stderr with exit status 1.
func helperReadConfig() int {
	data, err := os.ReadFile(os.Getenv("EZYAPPER_PLUGIN_CONFIG"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v", err)
		return 1
	}
	line := strings.TrimSpace(string(data))
	value, ok := strings.CutPrefix(line, "value:")
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		fmt.Fprintf(os.Stderr, "failed to load config: invalid line %q", line)
		return 1
	}
	fmt.Fprint(os.Stdout, value)
	return 0
}

// A command-runtime plugin is a new process per tool call, so an edit to its
// config.yaml takes effect on the next call without restarting the bot or
// reloading the plugin, and a broken file fails only the calls made while it
// is broken.
func TestExecuteCommandTool_ConfigEditAppliesOnNextCall(t *testing.T) {
	t.Setenv("GO_WANT_PLUGIN_HELPER_PROCESS", "1")

	pluginsRoot := t.TempDir()
	pluginDir := filepath.Join(pluginsRoot, "config-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := pluginManifest{
		Runtime: pluginRuntimeCommand,
		Name:    "config-plugin",
		Version: "1.0.0",
		Tools: []manifestToolSpec{{
			Name:        "read_config",
			Description: "prints the configured value",
			Command:     os.Args[0],
			Args:        []string{"-test.run=TestManagerHelperProcess", "--", "read-config"},
			TimeoutMs:   new(10000),
		}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(pluginDir, "config.yaml")
	writeConfig := func(body string) {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pm := NewManager(0, 90, 5, 180, 30, 5, 2, 3, 300)
	writeConfig("value: first\n")
	if err := pm.LoadPluginsFromDir(pluginsRoot); err != nil {
		t.Fatalf("LoadPluginsFromDir: %v", err)
	}
	call := func() (string, error) {
		return pm.ExecuteTool(context.Background(), "config-plugin", "read_config", map[string]any{})
	}

	if got, err := call(); err != nil || got != "first" {
		t.Fatalf("first call = %q, %v; want %q", got, err, "first")
	}

	writeConfig("value: second\n")
	if got, err := call(); err != nil || got != "second" {
		t.Fatalf("call after edit = %q, %v; want %q without a reload", got, err, "second")
	}

	writeConfig("broken\n")
	if _, err := call(); err == nil || !strings.Contains(err.Error(), "failed to load config") {
		t.Fatalf("call with broken config: err = %v; want the plugin's stderr in the error", err)
	}

	writeConfig("value: third\n")
	if got, err := call(); err != nil || got != "third" {
		t.Fatalf("call after fixing the config = %q, %v; want %q (the plugin must stay loaded)", got, err, "third")
	}
}
