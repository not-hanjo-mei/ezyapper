package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_PLUGIN_HELPER_PROCESS") != "1" {
		return
	}

	idx := -1
	for i, arg := range os.Args {
		if arg == "--" {
			idx = i
			break
		}
	}

	if idx < 0 || idx+1 >= len(os.Args) {
		fmt.Fprint(os.Stderr, "missing helper command")
		os.Exit(2)
	}

	command := os.Args[idx+1]
	switch command {
	case "emit":
		if idx+2 >= len(os.Args) {
			fmt.Fprint(os.Stderr, "missing emit value")
			os.Exit(2)
		}

		fmt.Fprint(os.Stdout, os.Args[idx+2])
		os.Exit(0)
	case "check-env":
		pluginPath := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_PATH"))
		pluginConfig := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG"))
		if pluginPath == "" || pluginConfig == "" {
			fmt.Fprint(os.Stderr, "plugin runtime env not found")
			os.Exit(3)
		}

		fmt.Fprint(os.Stdout, "ok")
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper command: %s", command)
		os.Exit(2)
	}
}

func TestEnablePluginAlreadyEnabled(t *testing.T) {
	pm := NewManager(0)
	pm.plugins["demo"] = &Client{Name: "demo"}

	if err := pm.EnablePlugin("demo"); err != nil {
		t.Fatalf("expected nil error for already enabled plugin, got %v", err)
	}
}

func TestEnablePluginNotFound(t *testing.T) {
	pm := NewManager(0)

	err := pm.EnablePlugin("missing")
	if err == nil {
		t.Fatal("expected error when enabling missing plugin")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnablePluginLoadFailureFromDisabled(t *testing.T) {
	pm := NewManager(0)
	missingPath := filepath.Join(t.TempDir(), "missing-plugin")
	pm.disabled["demo"] = disabledPlugin{
		Info: Info{Name: "demo"},
		Path: missingPath,
	}

	err := pm.EnablePlugin("demo")
	if err == nil {
		t.Fatal("expected error when loading missing plugin binary")
	}
	if !strings.Contains(err.Error(), "failed to enable plugin demo") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDisablePluginNotFound(t *testing.T) {
	pm := NewManager(0)

	err := pm.DisablePlugin("missing")
	if err == nil {
		t.Fatal("expected error when disabling missing plugin")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDisablePluginMovesToDisabledRegistry(t *testing.T) {
	pm := NewManager(0)
	pm.plugins["demo"] = &Client{
		Name:      "demo",
		Info:      Info{Name: "demo", Version: "1.0.0"},
		path:      "plugins/demo",
		configDir: "plugins/demo-config",
	}

	err := pm.DisablePlugin("demo")
	if err != nil {
		t.Fatalf("expected disable to succeed, got %v", err)
	}

	if _, ok := pm.plugins["demo"]; ok {
		t.Fatal("expected plugin removed from enabled map")
	}

	disabled, ok := pm.disabled["demo"]
	if !ok {
		t.Fatal("expected plugin added to disabled registry")
	}
	if disabled.Path != "plugins/demo" {
		t.Fatalf("unexpected disabled path: %s", disabled.Path)
	}
	if disabled.ConfigDir != "plugins/demo-config" {
		t.Fatalf("unexpected disabled config dir: %s", disabled.ConfigDir)
	}
}

func TestShutdownSkipsCommandRuntimePlugins(t *testing.T) {
	pm := NewManager(0)
	pm.plugins["command-plugin"] = &Client{
		Name:    "command-plugin",
		Info:    Info{Name: "command-plugin", Version: "1.0.0"},
		runtime: pluginRuntimeCommand,
	}

	err := pm.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("expected shutdown to succeed for command runtime plugin, got %v", err)
	}

	if len(pm.plugins) != 0 {
		t.Fatalf("expected all plugins to be cleared after shutdown, got %d", len(pm.plugins))
	}
}

func TestResolvePluginCommandPathStableAcrossWorkingDirChanges(t *testing.T) {
	tempDir := t.TempDir()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change working directory: %v", err)
	}

	pluginDir := filepath.Join("plugins", "datetime")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("failed to create plugin directory: %v", err)
	}

	commandName := "datetime-zig"
	commandPath := filepath.Join(pluginDir, commandName)
	if runtime.GOOS == "windows" {
		commandPath += ".exe"
	}

	if err := os.WriteFile(commandPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("failed to create command file: %v", err)
	}

	resolved, err := resolvePluginCommandPath("./"+commandName, pluginDir)
	if err != nil {
		t.Fatalf("resolvePluginCommandPath returned error: %v", err)
	}

	if !filepath.IsAbs(resolved) {
		t.Fatalf("expected absolute path, got %s", resolved)
	}

	otherDir := filepath.Join(tempDir, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatalf("failed to create secondary directory: %v", err)
	}

	if err := os.Chdir(otherDir); err != nil {
		t.Fatalf("failed to switch to secondary directory: %v", err)
	}

	if _, err := os.Stat(resolved); err != nil {
		t.Fatalf("expected resolved path to remain valid after cwd change: %v", err)
	}
}

func TestToAbsolutePathRelative(t *testing.T) {
	tempDir := t.TempDir()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change working directory: %v", err)
	}

	relativePath := filepath.Join("plugins", "sample")
	resolved := toAbsolutePath(relativePath)

	if !filepath.IsAbs(resolved) {
		t.Fatalf("expected absolute path, got %s", resolved)
	}

	expected := filepath.Join(tempDir, relativePath)
	if filepath.Clean(resolved) != filepath.Clean(expected) {
		t.Fatalf("unexpected absolute path: got %s, want %s", resolved, expected)
	}
}

func TestToAbsolutePathEmpty(t *testing.T) {
	if got := toAbsolutePath(" "); got != "" {
		t.Fatalf("expected empty result for blank input, got %q", got)
	}
}

func TestResolvePluginRuntimeRejectsManifestWithoutRuntime(t *testing.T) {
	_, err := resolvePluginRuntime(&pluginManifest{
		Tools: []manifestToolSpec{{
			Name:    "demo",
			Command: "./demo",
		}},
	})
	if err == nil {
		t.Fatal("expected error for manifest without runtime")
	}
}

func TestResolvePluginRuntimeNilManifestDefaultsToJSONRPC(t *testing.T) {
	runtimeMode, err := resolvePluginRuntime(nil)
	if err != nil {
		t.Fatalf("resolvePluginRuntime returned error: %v", err)
	}
	if runtimeMode != pluginRuntimeJSONRPC {
		t.Fatalf("expected runtime %q, got %q", pluginRuntimeJSONRPC, runtimeMode)
	}
}

func TestResolvePluginRuntimeAcceptsJSONRPCRuntime(t *testing.T) {
	runtimeMode, err := resolvePluginRuntime(&pluginManifest{Runtime: "jsonrpc"})
	if err != nil {
		t.Fatalf("resolvePluginRuntime returned error: %v", err)
	}
	if runtimeMode != pluginRuntimeJSONRPC {
		t.Fatalf("expected runtime %q, got %q", pluginRuntimeJSONRPC, runtimeMode)
	}
}

func TestResolvePluginRuntimeRejectsUnknownRuntime(t *testing.T) {
	_, err := resolvePluginRuntime(&pluginManifest{Runtime: "legacy-rpc"})
	if err == nil {
		t.Fatal("expected error for unknown runtime")
	}
}

func TestResolvePluginRuntimeRejectsRPCAlias(t *testing.T) {
	_, err := resolvePluginRuntime(&pluginManifest{Runtime: "rpc"})
	if err == nil {
		t.Fatal("expected error for deprecated rpc runtime alias")
	}
}

func TestLoadPluginsFromDirCommandRuntimeWithoutLocalExecutable(t *testing.T) {
	t.Setenv("GO_WANT_PLUGIN_HELPER_PROCESS", "1")

	pluginsRoot := t.TempDir()
	pluginDir := filepath.Join(pluginsRoot, "command-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("failed to create plugin directory: %v", err)
	}

	manifest := pluginManifest{
		Runtime:     pluginRuntimeCommand,
		Name:        "command-plugin",
		Version:     "1.0.0",
		Author:      "test",
		Description: "command runtime test",
		Priority:    10,
		Tools: []manifestToolSpec{
			{
				Name:        "echo_value",
				Description: "echoes provided value",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"value": map[string]interface{}{"type": "string"},
					},
					"required": []interface{}{"value"},
				},
				Command: os.Args[0],
				Args:    []string{"-test.run=TestManagerHelperProcess", "--", "emit"},
				ArgKeys: []string{"value"},
			},
			{
				Name:        "check_env",
				Description: "verifies plugin env propagation",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
				Command: os.Args[0],
				Args:    []string{"-test.run=TestManagerHelperProcess", "--", "check-env"},
			},
		},
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}

	manifestPath := filepath.Join(pluginDir, "plugin.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	pm := NewManager(0)
	if err := pm.LoadPluginsFromDir(pluginsRoot); err != nil {
		t.Fatalf("LoadPluginsFromDir returned error: %v", err)
	}

	plugins := pm.ListPlugins()
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].Name != "command-plugin" {
		t.Fatalf("unexpected plugin name: %s", plugins[0].Name)
	}

	output, err := pm.ExecuteTool("command-plugin", "echo_value", map[string]interface{}{"value": "hello"})
	if err != nil {
		t.Fatalf("ExecuteTool returned error: %v", err)
	}
	if strings.TrimSpace(output) != "hello" {
		t.Fatalf("unexpected tool output: %q", output)
	}

	if _, err := pm.ExecuteTool("command-plugin", "echo_value", map[string]interface{}{}); err == nil {
		t.Fatal("expected ExecuteTool to fail when required argument is missing")
	}

	envOutput, err := pm.ExecuteTool("command-plugin", "check_env", map[string]interface{}{})
	if err != nil {
		t.Fatalf("ExecuteTool check_env returned error: %v", err)
	}
	if strings.TrimSpace(envOutput) != "ok" {
		t.Fatalf("unexpected check_env output: %q", envOutput)
	}
}

func TestValidateToolSpec_Negative(t *testing.T) {
	err := validateToolSpec(&ToolSpec{Name: "test", TimeoutMs: -1})
	if err == nil {
		t.Fatal("expected error for negative timeout_ms")
	}
	if !strings.Contains(err.Error(), "test") || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestValidateToolSpec_MaxCap(t *testing.T) {
	ts := &ToolSpec{Name: "test", TimeoutMs: 500000}
	err := validateToolSpec(ts)
	if err != nil {
		t.Fatalf("expected no error for clamped timeout_ms, got %v", err)
	}
	if ts.TimeoutMs != 300000 {
		t.Fatalf("expected timeout_ms clamped to 300000, got %d", ts.TimeoutMs)
	}
}

func TestValidateToolSpec_Zero(t *testing.T) {
	err := validateToolSpec(&ToolSpec{Name: "test", TimeoutMs: 0})
	if err != nil {
		t.Fatalf("expected no error for zero timeout_ms (use default), got %v", err)
	}
}

func TestValidateToolSpec_OK(t *testing.T) {
	err := validateToolSpec(&ToolSpec{Name: "test", TimeoutMs: 15000})
	if err != nil {
		t.Fatalf("expected no error for valid timeout_ms, got %v", err)
	}
}
