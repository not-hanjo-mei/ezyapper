package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

func TestMain(m *testing.M) {
	logger.Init(logger.Config{Level: "info", File: os.DevNull})
	os.Exit(m.Run())
}

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
	case "sleep":
		time.Sleep(5 * time.Second)
		fmt.Fprint(os.Stdout, "woke up")
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper command: %s", command)
		os.Exit(2)
	}
}

func TestEnablePluginAlreadyEnabled(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 5, 2)
	pm.plugins["demo"] = &Client{Name: "demo"}

	if err := pm.EnablePlugin("demo"); err != nil {
		t.Fatalf("expected nil error for already enabled plugin, got %v", err)
	}
}

func TestEnablePluginNotFound(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 5, 2)

	err := pm.EnablePlugin("missing")
	if err == nil {
		t.Fatal("expected error when enabling missing plugin")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnablePluginLoadFailureFromDisabled(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 5, 2)
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
	pm := NewManager(0, 90, 5, 180, 45, 5, 2)

	err := pm.DisablePlugin("missing")
	if err == nil {
		t.Fatal("expected error when disabling missing plugin")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDisablePluginMovesToDisabledRegistry(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 5, 2)
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
	pm := NewManager(0, 90, 5, 180, 45, 5, 2)
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

	pm := NewManager(0, 90, 5, 180, 45, 5, 2)
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

	output, err := pm.ExecuteTool(context.Background(), "command-plugin", "echo_value", map[string]interface{}{"value": "hello"})
	if err != nil {
		t.Fatalf("ExecuteTool returned error: %v", err)
	}
	if strings.TrimSpace(output) != "hello" {
		t.Fatalf("unexpected tool output: %q", output)
	}

	if _, err := pm.ExecuteTool(context.Background(), "command-plugin", "echo_value", map[string]interface{}{}); err == nil {
		t.Fatal("expected ExecuteTool to fail when required argument is missing")
	}

	envOutput, err := pm.ExecuteTool(context.Background(), "command-plugin", "check_env", map[string]interface{}{})
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

func TestToolSpecTimeoutMsPropagation(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 5, 2)
	pm.plugins["test-plugin"] = &Client{
		Name:    "test-plugin",
		runtime: pluginRuntimeJSONRPC,
		tools: []ToolSpec{
			{Name: "test-tool", Description: "test", TimeoutMs: 15000},
		},
	}

	tools := pm.ListTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Spec.TimeoutMs != 15000 {
		t.Fatalf("expected TimeoutMs=15000, got %d", tools[0].Spec.TimeoutMs)
	}
}

func TestToolSpecTimeoutMsJSONDeserialization(t *testing.T) {
	raw := `{"name":"test","description":"test tool","parameters":{"type":"object"},"timeout_ms":15000}`
	var spec ToolSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("failed to unmarshal ToolSpec: %v", err)
	}
	if spec.TimeoutMs != 15000 {
		t.Fatalf("expected TimeoutMs=15000, got %d", spec.TimeoutMs)
	}
}

func TestToolSpecTimeoutMsJSONOmittedDefaultsToZero(t *testing.T) {
	raw := `{"name":"test","description":"test tool","parameters":{"type":"object"}}`
	var spec ToolSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("failed to unmarshal ToolSpec: %v", err)
	}
	if spec.TimeoutMs != 0 {
		t.Fatalf("expected TimeoutMs=0 when omitted, got %d", spec.TimeoutMs)
	}
}

func newMockJSONRPCClient(responseDelay time.Duration) (*stdioJSONRPCClient, func()) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	client := newStdioJSONRPCClient(stdinWriter, stdoutReader)

	go func() {
		var req jsonRPCRequest
		if err := json.NewDecoder(stdinReader).Decode(&req); err != nil {
			stdinWriter.Close()
			stdoutWriter.Close()
			return
		}
		time.Sleep(responseDelay)
		writeJSONRPCResponse(json.NewEncoder(stdoutWriter), req.ID, "done", nil)
		stdinWriter.Close()
		stdoutWriter.Close()
	}()

	cleanup := func() {
		stdinWriter.Close()
		stdoutReader.Close()
	}

	return client, cleanup
}

func newTimeoutTestManager(t *testing.T, toolTimeoutMs int, toolDelay time.Duration, defaultToolTimeoutMs int) *Manager {
	t.Helper()

	jsonClient, cleanup := newMockJSONRPCClient(toolDelay)
	t.Cleanup(cleanup)

	pm := NewManager(defaultToolTimeoutMs, 90, 5, 180, 45, 5, 2)
	pm.plugins["mock-plugin"] = &Client{
		Name:    "mock-plugin",
		jsonrpc: jsonClient,
		runtime: pluginRuntimeJSONRPC,
		tools: []ToolSpec{
			{Name: "mock-tool", Description: "mock tool", TimeoutMs: toolTimeoutMs},
		},
	}

	return pm
}

func TestExecuteToolTimeout_ToolSpecWins(t *testing.T) {
	pm := newTimeoutTestManager(t, 200, 1*time.Second, 0)

	_, err := pm.ExecuteTool(context.Background(), "mock-plugin", "mock-tool", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "jsonrpc call timeout") {
		t.Fatalf("expected timeout error message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "(200ms)") {
		t.Fatalf("expected error to mention (200ms), got: %v", err)
	}
}

func TestExecuteToolTimeout_ConfigWins(t *testing.T) {
	pm := newTimeoutTestManager(t, 0, 2*time.Second, 500)

	_, err := pm.ExecuteTool(context.Background(), "mock-plugin", "mock-tool", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "(500ms)") {
		t.Fatalf("expected error to mention (500ms), got: %v", err)
	}
}

func TestExecuteToolTimeout_NoFallbackError(t *testing.T) {
	pm := newTimeoutTestManager(t, 0, 100*time.Millisecond, 0)

	_, err := pm.ExecuteTool(context.Background(), "mock-plugin", "mock-tool", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when no tool timeout or config timeout is set, got nil")
	}
	if !strings.Contains(err.Error(), "no timeout configured") {
		t.Fatalf("expected error to contain 'no timeout configured', got: %v", err)
	}
}

func TestExecuteToolTimeout_SuccessWithToolTimeout(t *testing.T) {
	pm := newTimeoutTestManager(t, 15000, 100*time.Millisecond, 0)

	result, err := pm.ExecuteTool(context.Background(), "mock-plugin", "mock-tool", map[string]interface{}{})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result != "done" {
		t.Fatalf("expected result 'done', got %q", result)
	}
}

func TestExecuteToolTimeout_SuccessWithConfigTimeout(t *testing.T) {
	pm := newTimeoutTestManager(t, 0, 100*time.Millisecond, 10000)

	result, err := pm.ExecuteTool(context.Background(), "mock-plugin", "mock-tool", map[string]interface{}{})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result != "done" {
		t.Fatalf("expected result 'done', got %q", result)
	}
}

func TestManagerDefaultToolTimeoutMsZero(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 5, 2)
	if pm.defaultToolTimeoutMs != 0 {
		t.Fatalf("expected DefaultToolTimeoutMs=0, got %d", pm.defaultToolTimeoutMs)
	}
}

func TestToolSpecTimeoutMsZeroRequiresConfig(t *testing.T) {
	pm := newTimeoutTestManager(t, 0, 50*time.Millisecond, 0)

	_, err := pm.ExecuteTool(context.Background(), "mock-plugin", "mock-tool", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when ToolSpec.TimeoutMs is 0 and no config fallback, got nil")
	}
	if !strings.Contains(err.Error(), "no timeout configured") {
		t.Fatalf("expected error to contain 'no timeout configured', got: %v", err)
	}
}

func TestManifestToolSpec_HasTimeoutMs(t *testing.T) {
	typ := reflect.TypeOf(manifestToolSpec{})
	field, ok := typ.FieldByName("TimeoutMs")
	if !ok {
		t.Fatal("manifestToolSpec does not have TimeoutMs field")
	}
	tag := field.Tag.Get("json")
	if tag != "timeout_ms,omitempty" {
		t.Fatalf("expected json tag 'timeout_ms,omitempty', got %q", tag)
	}
}

func TestLoadCommandPlugin_UsesPerToolTimeout(t *testing.T) {
	pluginDir := t.TempDir()

	manifest := pluginManifest{
		Runtime: pluginRuntimeCommand,
		Name:    "test-plugin",
		Version: "1.0.0",
		Tools: []manifestToolSpec{
			{
				Name:        "test-tool",
				Description: "test",
				Command:     "echo",
				Args:        []string{"hello"},
				TimeoutMs:   5000,
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

	loaded, err := loadPluginManifest(pluginDir)
	if err != nil {
		t.Fatalf("loadPluginManifest returned error: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected manifest, got nil")
	}
	if len(loaded.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(loaded.Tools))
	}
	if loaded.Tools[0].TimeoutMs != 5000 {
		t.Fatalf("expected TimeoutMs=5000, got %d", loaded.Tools[0].TimeoutMs)
	}
}

func TestExecuteCommandTool_PerToolTimeout(t *testing.T) {
	t.Setenv("GO_WANT_PLUGIN_HELPER_PROCESS", "1")

	pluginsRoot := t.TempDir()
	pluginDir := filepath.Join(pluginsRoot, "timeout-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("failed to create plugin directory: %v", err)
	}

	manifest := pluginManifest{
		Runtime:     pluginRuntimeCommand,
		Name:        "timeout-plugin",
		Version:     "1.0.0",
		Description: "timeout test",
		Tools: []manifestToolSpec{
			{
				Name:        "slow_tool",
				Description: "a slow tool that exceeds per-tool timeout",
				Command:     os.Args[0],
				Args:        []string{"-test.run=TestManagerHelperProcess", "--", "sleep"},
				TimeoutMs:   2000,
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

	pm := NewManager(0, 90, 5, 180, 30, 5, 2)
	if err := pm.LoadPluginsFromDir(pluginsRoot); err != nil {
		t.Fatalf("LoadPluginsFromDir returned error: %v", err)
	}

	start := time.Now()
	_, err = pm.ExecuteTool(context.Background(), "timeout-plugin", "slow_tool", map[string]interface{}{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "plugin command timeout") {
		t.Fatalf("expected 'plugin command timeout' error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("expected timeout within ~2 seconds (per-tool), but took %v (global fallback?)", elapsed)
	}
}

func TestConcurrentExecuteToolAndDisablePluginNoPanic(t *testing.T) {
	jsonClient, cleanup := newMockJSONRPCClient(200 * time.Millisecond)
	t.Cleanup(cleanup)

	pm := NewManager(10000, 90, 5, 180, 45, 5, 2)
	pm.plugins["test"] = &Client{
		Name:    "test",
		jsonrpc: jsonClient,
		runtime: pluginRuntimeJSONRPC,
		tools: []ToolSpec{
			{Name: "tool", Description: "test tool", TimeoutMs: 5000},
		},
		priority: 10,
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := pm.ExecuteTool(context.Background(), "test", "tool", map[string]interface{}{})
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := pm.DisablePlugin("test")
		errCh <- err
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Logf("operation error (expected): %v", err)
		}
	}
}

func TestConcurrentOnMessageAndDisablePluginNoPanic(t *testing.T) {
	jsonClient, cleanup := newMockJSONRPCClient(200 * time.Millisecond)
	t.Cleanup(cleanup)

	pm := NewManager(10000, 90, 5, 180, 45, 5, 2)
	pm.plugins["test"] = &Client{
		Name:    "test",
		Info:    Info{Name: "test", Priority: 10},
		jsonrpc: jsonClient,
		runtime: pluginRuntimeJSONRPC,
		tools: []ToolSpec{
			{Name: "tool", Description: "test tool", TimeoutMs: 5000},
		},
		priority: 10,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := pm.OnMessage(context.Background(), types.DiscordMessage{Username: "user", Content: "hello"})
		if err != nil {
			t.Logf("OnMessage error (expected): %v", err)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := pm.DisablePlugin("test")
		if err != nil {
			t.Logf("DisablePlugin error (expected): %v", err)
		}
	}()

	wg.Wait()
}

func TestWaitForPending_SuccessWhenNoPendingOperations(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 1, 2)

	err := pm.WaitForPending()
	if err != nil {
		t.Fatalf("expected nil error for no pending operations, got %v", err)
	}
}

func TestWaitForPending_TimeoutWhenOperationsBlocking(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 1, 2)

	pm.wg.Add(1)

	err := pm.WaitForPending()
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout waiting for pending plugin operations") {
		t.Fatalf("unexpected error: %v", err)
	}

	pm.wg.Done()
}

func TestWaitForPending_RecoversFromPanic(t *testing.T) {
	pm := NewManager(0, 90, 5, 180, 45, 1, 2)

	err := pm.WaitForPending()
	if err != nil {
		t.Fatalf("expected nil error for no pending operations, got %v", err)
	}

	pm.wg.Add(1)

	done := make(chan error, 1)
	go func() {
		done <- pm.WaitForPending()
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "timeout waiting for pending plugin operations") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForPending did not return within 5 seconds (deadlock or crash)")
	}

	pm.wg.Done()
}

func TestConcurrentBeforeSendAndDisablePluginNoPanic(t *testing.T) {
	jsonClient, cleanup := newMockJSONRPCClient(200 * time.Millisecond)
	t.Cleanup(cleanup)

	pm := NewManager(10000, 90, 5, 180, 45, 5, 2)
	pm.plugins["test"] = &Client{
		Name:    "test",
		Info:    Info{Name: "test", Priority: 10},
		jsonrpc: jsonClient,
		runtime: pluginRuntimeJSONRPC,
		tools: []ToolSpec{
			{Name: "tool", Description: "test tool", TimeoutMs: 5000},
		},
		priority: 10,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _, err := pm.BeforeSend(context.Background(), types.DiscordMessage{Username: "user", Content: "hello"}, "response")
		if err != nil {
			t.Logf("BeforeSend error (expected): %v", err)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := pm.DisablePlugin("test")
		if err != nil {
			t.Logf("DisablePlugin error (expected): %v", err)
		}
	}()

	wg.Wait()
}
