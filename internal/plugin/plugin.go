// Package plugin provides a cross-platform plugin system with JSON-RPC and command runtimes.
// This works on Windows, Linux, and macOS by running plugins as separate processes.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

// Interface defines the methods a plugin must implement
type Interface interface {
	// Info returns plugin metadata
	Info() (Info, error)

	// OnMessage is called for every message
	// Returns: shouldContinue (bool), error
	OnMessage(msg types.DiscordMessage) (bool, error)

	// OnResponse is called after the bot generates a response
	OnResponse(msg types.DiscordMessage, response string) error

	// Shutdown is called when the plugin is being stopped
	Shutdown() error
}

// ToolSpec describes a callable tool exposed by a plugin.
type ToolSpec struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
	TimeoutMs   int                    `json:"timeout_ms,omitempty"`
}

// validateToolSpec validates and normalizes a ToolSpec's TimeoutMs field.
// Returns an error if TimeoutMs is negative. Clamps values > 300000 to 300000 with a warning.
// A value of 0 means "use global default" and is accepted.
func validateToolSpec(ts *ToolSpec) error {
	if ts.TimeoutMs < 0 {
		return fmt.Errorf("tool %q: timeout_ms cannot be negative", ts.Name)
	}
	if ts.TimeoutMs > 300000 {
		logger.Warnf("tool %q: timeout_ms %d exceeds maximum 300000, clamping to 300000", ts.Name, ts.TimeoutMs)
		ts.TimeoutMs = 300000
	}
	return nil
}

type pluginManifest struct {
	Runtime     string             `json:"runtime"`
	Name        string             `json:"name"`
	Version     string             `json:"version"`
	Author      string             `json:"author"`
	Description string             `json:"description"`
	Priority    int                `json:"priority"`
	Tools       []manifestToolSpec `json:"tools"`
}

type manifestToolSpec struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
	Command     string                 `json:"command"`
	Args        []string               `json:"args"`
	ArgKeys     []string               `json:"arg_keys"`
	TimeoutMs   int                    `json:"timeout_ms,omitempty"`
}

type commandTool struct {
	CommandPath string
	Args        []string
	ArgKeys     []string
}

// ToolProvider is an optional interface for plugins that expose AI tools.
type ToolProvider interface {
	ListTools() ([]ToolSpec, error)
	ExecuteTool(name string, args map[string]interface{}) (string, error)
}

// LocalFile describes a local file upload requested by a plugin.
type LocalFile struct {
	Path              string `json:"path"`
	Name              string `json:"name"`
	ContentType       string `json:"content_type"`
	Data              []byte `json:"data"`
	DeleteAfterUpload bool   `json:"delete_after_upload"`
}

// BeforeSendResult contains optional response mutation and upload files.
type BeforeSendResult struct {
	Response string      `json:"response"`
	Files    []LocalFile `json:"files"`
	SkipSend bool        `json:"skip_send"`
}

// BeforeSendProvider is an optional interface for pre-send hooks.
type BeforeSendProvider interface {
	BeforeSend(msg types.DiscordMessage, response string) (BeforeSendResult, error)
}

type Info struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Priority    int    `json:"priority"`
}

// InfoExt extends Info with enabled status
type InfoExt struct {
	Info
	Enabled bool `json:"enabled"`
}

// ResponseArgs holds arguments for OnResponse
type ResponseArgs struct {
	Message  types.DiscordMessage
	Response string
}

// BeforeSendArgs holds arguments for before-send plugin hooks.
type BeforeSendArgs struct {
	Message  types.DiscordMessage
	Response string
}

// ExecuteToolArgs holds arguments for ExecuteTool RPC.
type ExecuteToolArgs struct {
	Name      string
	Arguments map[string]interface{}
}

// Client represents a connection to a plugin
type Client struct {
	Name      string
	Info      Info
	jsonrpc   *stdioJSONRPCClient
	process   *os.Process
	priority  int
	path      string
	configDir string
	tools     []ToolSpec
	runtime   string
	commands  map[string]commandTool
	wg        *sync.WaitGroup // parent Manager's WaitGroup for RPC call tracking
}

// PluginTool wraps a tool with its owning plugin name.
type PluginTool struct {
	PluginName string
	Spec       ToolSpec
}

const (
	pluginRuntimeJSONRPC = "jsonrpc"
	pluginRuntimeCommand = "command"
)

// safeEnvVars lists environment variable names that are always safe to pass to plugin subprocesses.
var safeEnvVars = map[string]bool{
	"PATH":       true,
	"HOME":       true,
	"USER":       true,
	"TMP":        true,
	"TEMP":       true,
	"TMPDIR":     true,
	"LANG":       true,
	"LC_ALL":     true,
	"SYSTEMROOT": true,
	"WINDIR":     true,
}

// secretEnvKeywords are keywords that, if present in an env var name, cause it
// to be filtered out to prevent secrets from leaking to plugin subprocesses.
var secretEnvKeywords = []string{
	"TOKEN",
	"SECRET",
	"KEY",
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"AUTH",
}

func buildPluginEnv(configDir string, extra ...string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+len(extra))
outer:
	for _, e := range env {
		key, _, found := strings.Cut(e, "=")
		if !found {
			continue
		}
		// Always pass known-safe system variables
		if safeEnvVars[key] {
			out = append(out, e)
			continue
		}
		// Filter out any env var whose name contains a secret keyword
		upper := strings.ToUpper(key)
		for _, kw := range secretEnvKeywords {
			if strings.Contains(upper, kw) {
				continue outer
			}
		}
		out = append(out, e)
	}
	out = append(out, extra...)
	return out
}

type disabledPlugin struct {
	Info      Info
	Path      string
	ConfigDir string
}

type pluginLoadTarget struct {
	binaryPath string
	configDir  string
}

type Manager struct {
	plugins              map[string]*Client
	disabled             map[string]disabledPlugin
	mu                   sync.RWMutex
	defaultToolTimeoutMs int
	startupTimeoutSec    int
	rpcTimeoutSec        int
	beforeSendTimeoutSec int
	commandTimeoutSec    int
	shutdownTimeoutSec   int
	disableTimeoutSec    int
	pluginsDir           string
	wg                   sync.WaitGroup
}

// ListTools returns all tools exposed by currently enabled plugins.
func (pm *Manager) ListTools() []PluginTool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	tools := make([]PluginTool, 0)
	for name, p := range pm.plugins {
		for _, t := range p.tools {
			tools = append(tools, PluginTool{PluginName: name, Spec: t})
		}
	}

	return tools
}

// ExecuteTool executes a named tool on a specific plugin.
func (pm *Manager) ExecuteTool(ctx context.Context, pluginName string, toolName string, args map[string]interface{}) (string, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	plugin, exists := pm.plugins[pluginName]
	if !exists {
		return "", fmt.Errorf("plugin %s not found", pluginName)
	}

	if plugin.runtime == pluginRuntimeCommand {
		return pm.executeCommandTool(ctx, plugin, toolName, args)
	}

	if plugin.jsonrpc != nil {
		return pm.executeJSONRPCTool(plugin, toolName, args)
	}

	return "", fmt.Errorf("jsonrpc plugin is not initialized")
}

func (pm *Manager) executeJSONRPCTool(plugin *Client, toolName string, args map[string]interface{}) (string, error) {
	if plugin == nil || plugin.jsonrpc == nil {
		return "", fmt.Errorf("jsonrpc plugin is not initialized")
	}

	// Resolve per-tool timeout: ToolSpec.TimeoutMs > defaultToolTimeoutMs; no fallback.
	var timeout time.Duration
	var source string
	for _, t := range plugin.tools {
		if t.Name == toolName && t.TimeoutMs > 0 {
			timeout = time.Duration(t.TimeoutMs) * time.Millisecond
			source = "tool_spec"
			break
		}
	}
	if timeout == 0 && pm.defaultToolTimeoutMs > 0 {
		timeout = time.Duration(pm.defaultToolTimeoutMs) * time.Millisecond
		source = "config"
	}
	if timeout == 0 {
		return "", fmt.Errorf("tool %q has no timeout configured — set tool_timeouts in plugin config or operations.plugins.default_tool_timeout_ms in main config", toolName)
	}

	logger.Debugf("[plugin] execute_tool %q timeout=%dms (source: %s)", toolName, timeout/time.Millisecond, source)

	var reply string
	err := callJSONRPCWithTimeout(
		plugin.jsonrpc,
		plugin.wg,
		"execute_tool",
		ExecuteToolArgs{Name: toolName, Arguments: args},
		&reply,
		timeout,
	)
	if err != nil {
		return "", fmt.Errorf("plugin tool execution failed: %w", err)
	}

	return reply, nil
}

func (pm *Manager) executeCommandTool(ctx context.Context, plugin *Client, toolName string, args map[string]interface{}) (string, error) {
	if plugin == nil {
		return "", fmt.Errorf("plugin is nil")
	}

	tool, ok := plugin.commands[toolName]
	if !ok {
		return "", fmt.Errorf("plugin tool not found: %s", toolName)
	}

	commandArgs := append([]string{}, tool.Args...)
	for _, key := range tool.ArgKeys {
		value, exists := args[key]
		if !exists {
			return "", fmt.Errorf("missing required argument %q for tool %s", key, toolName)
		}

		argText, err := commandArgumentValue(value)
		if err != nil {
			return "", fmt.Errorf("invalid argument %q for tool %s: %w", key, toolName, err)
		}

		commandArgs = append(commandArgs, argText)
	}

	var timeout time.Duration
	for _, t := range plugin.tools {
		if t.Name == toolName && t.TimeoutMs > 0 {
			timeout = time.Duration(t.TimeoutMs) * time.Millisecond
			break
		}
	}
	if timeout == 0 {
		timeout = time.Duration(pm.commandTimeoutSec) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, tool.CommandPath, commandArgs...)
	cmd.Dir = plugin.configDir
	cmd.Env = buildPluginEnv(plugin.configDir,
		fmt.Sprintf("EZYAPPER_PLUGIN_PATH=%s", plugin.configDir),
		fmt.Sprintf("EZYAPPER_PLUGIN_CONFIG=%s", filepath.Join(plugin.configDir, "config.yaml")),
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	stderrText := strings.TrimSpace(stderr.String())
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("plugin command timeout for tool %s", toolName)
		}
		if stderrText != "" {
			return "", fmt.Errorf("plugin command execution failed: %w: %s", err, stderrText)
		}
		return "", fmt.Errorf("plugin command execution failed: %w", err)
	}

	result := strings.TrimSpace(stdout.String())
	if result == "" && stderrText != "" {
		return "", fmt.Errorf("plugin command produced empty stdout: %s", stderrText)
	}

	return result, nil
}

func commandArgumentValue(value interface{}) (string, error) {
	if value == nil {
		return "", fmt.Errorf("value is null")
	}

	var result string
	switch typed := value.(type) {
	case string:
		result = typed
	case bool:
		result = fmt.Sprint(typed)
	case float64:
		result = fmt.Sprint(typed)
	case float32:
		result = fmt.Sprint(typed)
	case int:
		result = fmt.Sprint(typed)
	case int64:
		result = fmt.Sprint(typed)
	case int32:
		result = fmt.Sprint(typed)
	case uint:
		result = fmt.Sprint(typed)
	case uint64:
		result = fmt.Sprint(typed)
	case uint32:
		result = fmt.Sprint(typed)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("failed to encode argument: %w", err)
		}
		result = string(encoded)
	}
	return sanitizeCommandArg(result)
}

// sanitizeCommandArg validates and sanitizes a single argument value
// for command-tool plugin execution. Since exec.Command uses argv (not
// shell), shell metacharacters are not directly exploitable, but this
// provides defense-in-depth against control character injection.
func sanitizeCommandArg(arg string) (string, error) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return "", fmt.Errorf("argument value is empty")
	}

	const maxArgLen = 4096
	if len(trimmed) > maxArgLen {
		return "", fmt.Errorf("argument value exceeds maximum length of %d", maxArgLen)
	}

	var buf bytes.Buffer
	buf.Grow(len(trimmed))
	hadControlChars := false
	for _, r := range trimmed {
		if r == '\n' || r == '\r' {
			return "", fmt.Errorf("argument value contains newline character")
		}
		if r == 0 {
			return "", fmt.Errorf("argument value contains null byte")
		}
		if r < 0x20 && r != '\t' {
			buf.WriteByte(' ')
			hadControlChars = true
			continue
		}
		buf.WriteRune(r)
	}
	if hadControlChars {
		logger.Warnf("[plugin] control characters stripped from command argument")
	}
	sanitized := buf.String()

	if strings.TrimSpace(sanitized) == "" {
		return "", fmt.Errorf("argument value is empty after sanitization")
	}

	return sanitized, nil
}
