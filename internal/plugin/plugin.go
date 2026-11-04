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
	"sync/atomic"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

// Interface defines the methods every plugin must implement.
//
// The message hook chain is, in order:
//
//	OnMessage      every incoming message; can block further processing
//	AfterDecision  only when the bot decided to reply, before generation;
//	               can cancel the reply and inject prompt context
//	BeforeSend     the generated text, before it is sent; can rewrite it,
//	               attach files, or cancel sending
//	AfterSend      after sending, with the final text and sent message IDs
//
// AfterDecision, BeforeSend and AfterSend are optional interfaces.
type Interface interface {
	// Info returns plugin metadata
	Info() (Info, error)

	// OnMessage is called for every message
	// Returns: shouldContinue (bool), error
	OnMessage(msg types.DiscordMessage) (bool, error)

	// Shutdown is called when the plugin is being stopped
	Shutdown() error
}

// ContextBlock is a section a plugin injects into the prompt. Tag becomes the
// XML tag name (letters, digits, '_' and '-' only); Content is its body.
type ContextBlock struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

// AfterDecisionArgs holds arguments for the after_decision hook.
type AfterDecisionArgs struct {
	Message types.DiscordMessage `json:"message"`
	// Reason is why the bot decided to reply (mentioned, llm decision, ...).
	Reason string `json:"reason"`
}

// AfterDecisionResult lets a plugin cancel the reply or add prompt context.
// Context blocks are placed in the user prompt's plugin slot (after the
// conversation and participants, before user profile and key facts), never
// in the system prompt, so the system prompt stays cacheable.
type AfterDecisionResult struct {
	Cancel  bool           `json:"cancel"`
	Context []ContextBlock `json:"context,omitempty"`
}

// AfterDecisionProvider is an optional interface for the after_decision hook.
type AfterDecisionProvider interface {
	AfterDecision(args AfterDecisionArgs) (AfterDecisionResult, error)
}

// SystemPromptProvider is an optional interface for plugins that add a
// static section to the system prompt. It is queried once when the plugin
// loads; the text must not change per message (the system prompt has to stay
// byte-identical across requests for prompt caching).
type SystemPromptProvider interface {
	SystemPrompt() (ContextBlock, error)
}

// AfterSendArgs holds arguments for the after_send hook.
type AfterSendArgs struct {
	Message types.DiscordMessage `json:"message"`
	// Response is the final text that was sent (after BeforeSend rewrites).
	Response string `json:"response"`
	// MessageIDs are the IDs of the Discord messages sent, in order (long
	// replies are split into several messages).
	MessageIDs []string `json:"message_ids"`
}

// AfterSendProvider is an optional interface for the after_send hook.
type AfterSendProvider interface {
	AfterSend(args AfterSendArgs) error
}

// ToolSpec describes a callable tool exposed by a plugin.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	// TimeoutMs is the tool's execution timeout in milliseconds. nil (the
	// key absent from the tool spec) uses the host's fallback:
	// operations.plugins.default_tool_timeout_ms for JSON-RPC tools,
	// operations.plugins.command_timeout_sec for command tools. A set value
	// must be greater than 0: an explicit 0 is rejected, not treated as
	// absent. Set it with new(ms).
	TimeoutMs *int `json:"timeout_ms,omitempty"`
}

// validateToolSpec validates a ToolSpec's TimeoutMs field: an absent value
// is accepted (the host fallback applies), a set value must be greater than
// 0. Values over 300000 are honored with a warning.
func validateToolSpec(ts *ToolSpec) error {
	if ts.TimeoutMs == nil {
		return nil
	}
	if *ts.TimeoutMs <= 0 {
		return fmt.Errorf("tool %q: timeout_ms must be greater than 0 (omit the key to use the host's operations.plugins timeout)", ts.Name)
	}
	if *ts.TimeoutMs > 300000 {
		logger.Warnf("[plugin] tool %q: timeout_ms %d exceeds recommended maximum 300000, honoring user value", ts.Name, *ts.TimeoutMs)
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
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Command     string         `json:"command"`
	Args        []string       `json:"args"`
	ArgKeys     []string       `json:"arg_keys"`
	TimeoutMs   *int           `json:"timeout_ms,omitempty"`
}

type commandTool struct {
	CommandPath string
	Args        []string
	ArgKeys     []string
}

// ToolProvider is an optional interface for plugins that expose AI tools.
type ToolProvider interface {
	ListTools() ([]ToolSpec, error)
	ExecuteTool(name string, args map[string]any) (string, error)
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
// A non-empty Response replaces the text; SkipSend cancels sending.
type BeforeSendResult struct {
	Response string      `json:"response"`
	Files    []LocalFile `json:"files"`
	SkipSend bool        `json:"skip_send"`
}

// BeforeSendProvider is an optional interface for pre-send hooks.
type BeforeSendProvider interface {
	BeforeSend(args BeforeSendArgs) (BeforeSendResult, error)
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

// BeforeSendArgs holds arguments for before-send plugin hooks.
type BeforeSendArgs struct {
	Message  types.DiscordMessage
	Response string
	// DeadlineUnixMs is when the host stops waiting for this hook (Unix
	// milliseconds). Plugins doing slow work (TTS, network calls) should
	// finish or give up before it; past it the host marks the plugin dead
	// and restarts it.
	DeadlineUnixMs int64 `json:"deadline_unix_ms,omitempty"`
}

// ExecuteToolArgs holds arguments for ExecuteTool RPC.
type ExecuteToolArgs struct {
	Name      string
	Arguments map[string]any
}

// Client represents a connection to a plugin
type Client struct {
	Name    string
	Info    Info
	jsonrpc *stdioJSONRPCClient
	process *os.Process
	// exited is closed once the plugin process has exited and been reaped.
	exited    chan struct{}
	priority  int
	path      string
	configDir string
	tools     []ToolSpec
	// systemPrompt is the static system prompt section the plugin
	// registered at load (nil when it has none).
	systemPrompt *ContextBlock
	runtime      string
	commands     map[string]commandTool
	wg           *sync.WaitGroup // parent Manager's WaitGroup for RPC call tracking
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

// isSecretEnvVar prevents credential leakage to plugin subprocesses by
// matching secret keywords (TOKEN, SECRET, KEY, ...) as substrings.
func isSecretEnvVar(key string) bool {
	upper := strings.ToUpper(key)
	for _, kw := range secretEnvKeywords {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

func buildPluginEnv(configDir string, extra ...string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+len(extra))
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
		if isSecretEnvVar(key) {
			continue
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
	plugins  map[string]*Client
	disabled map[string]disabledPlugin
	mu       sync.RWMutex
	// settings holds the current *Settings; replaced by UpdateSettings.
	settings atomic.Pointer[Settings]
	// toolsChanged holds the handler set by SetToolsChangedHandler.
	toolsChanged  atomic.Pointer[func()]
	wg            sync.WaitGroup
	restartStates sync.Map // name -> *restartState
}

type restartState struct {
	inProgress  atomic.Bool
	count       int
	lastAttempt time.Time
	mu          sync.Mutex
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
func (pm *Manager) ExecuteTool(ctx context.Context, pluginName string, toolName string, args map[string]any) (string, error) {
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
		if plugin.jsonrpc.dead.Load() {
			pm.triggerRestart(pluginName, plugin.jsonrpc.DeadReason())
			return "", fmt.Errorf("plugin %s transport is dead", pluginName)
		}
		return pm.executeJSONRPCTool(plugin, toolName, args)
	}

	return "", fmt.Errorf("jsonrpc plugin is not initialized")
}

func (pm *Manager) executeJSONRPCTool(plugin *Client, toolName string, args map[string]any) (string, error) {
	if plugin == nil || plugin.jsonrpc == nil {
		return "", fmt.Errorf("jsonrpc plugin is not initialized")
	}

	// Resolve the per-tool timeout: the tool spec's timeout_ms when the key
	// is set (validated > 0), else operations.plugins.default_tool_timeout_ms
	// when that is > 0 (0 there documents "no fallback").
	var timeout time.Duration
	var source string
	for _, t := range plugin.tools {
		if t.Name == toolName && t.TimeoutMs != nil {
			timeout = time.Duration(*t.TimeoutMs) * time.Millisecond
			source = "tool_spec"
			break
		}
	}
	if timeout == 0 && pm.st().DefaultToolTimeoutMs > 0 {
		timeout = time.Duration(pm.st().DefaultToolTimeoutMs) * time.Millisecond
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
		if isClientDeadError(err) {
			pm.triggerRestart(plugin.Name, err.Error())
		}
		return "", fmt.Errorf("plugin tool execution failed: %w", err)
	}

	return reply, nil
}

func (pm *Manager) executeCommandTool(ctx context.Context, plugin *Client, toolName string, args map[string]any) (string, error) {
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

	// plugin.json timeout_ms when the key is set (validated > 0), else
	// operations.plugins.command_timeout_sec.
	timeout := time.Duration(pm.st().CommandTimeoutSec) * time.Second
	for _, t := range plugin.tools {
		if t.Name == toolName && t.TimeoutMs != nil {
			timeout = time.Duration(*t.TimeoutMs) * time.Millisecond
			break
		}
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

func commandArgumentValue(value any) (string, error) {
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
