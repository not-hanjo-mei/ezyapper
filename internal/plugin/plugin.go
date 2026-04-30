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
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
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

// Info contains plugin metadata
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
	pluginStartupTimeout    = 90 * time.Second
	pluginRPCTimeout        = 5 * time.Second
	pluginBeforeSendTimeout = 180 * time.Second
	pluginCommandTimeout    = 45 * time.Second
	pluginRuntimeJSONRPC    = "jsonrpc"
	pluginRuntimeCommand    = "command"
)

type disabledPlugin struct {
	Info      Info
	Path      string
	ConfigDir string
}

type pluginLoadTarget struct {
	binaryPath string
	configDir  string
}

// Manager manages all plugins
type Manager struct {
	plugins              map[string]*Client
	disabled             map[string]disabledPlugin
	mu                   sync.RWMutex
	defaultToolTimeoutMs int
	wg                   sync.WaitGroup // tracks in-flight goroutines (plugin loads, RPC calls, process shutdown)
}

// NewManager creates a new plugin manager
func NewManager(defaultToolTimeoutMs int) *Manager {
	return &Manager{
		plugins:              make(map[string]*Client),
		disabled:             make(map[string]disabledPlugin),
		defaultToolTimeoutMs: defaultToolTimeoutMs,
	}
}


// OnMessage calls all plugins' OnMessage methods
// Returns false if any plugin wants to block the message
func (pm *Manager) OnMessage(ctx context.Context, m *discordgo.MessageCreate) (bool, error) {
	pm.mu.RLock()
	plugins := make([]*Client, 0, len(pm.plugins))
	for _, p := range pm.plugins {
		plugins = append(plugins, p)
	}
	pm.mu.RUnlock()

	// Sort by priority (highest first)
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].priority > plugins[j].priority
	})

	msg := types.FromDiscordgo(m)

	for _, plugin := range plugins {
		if plugin.jsonrpc == nil {
			continue
		}

		var shouldContinue bool
		err := callPluginOnMessageWithTimeout(plugin, msg, &shouldContinue, pluginRPCTimeout)
		if err != nil {
			logger.Warnf("Plugin %s error in OnMessage: %v", plugin.Name, err)
			continue
		}
		if !shouldContinue {
			logger.Debugf("Plugin %s blocked message", plugin.Name)
			return false, nil
		}
	}

	return true, nil
}

// OnResponse calls all plugins' OnResponse methods
func (pm *Manager) OnResponse(ctx context.Context, m *discordgo.MessageCreate, response string) error {
	pm.mu.RLock()
	plugins := make([]*Client, 0, len(pm.plugins))
	for _, p := range pm.plugins {
		plugins = append(plugins, p)
	}
	pm.mu.RUnlock()

	msg := types.FromDiscordgo(m)
	args := ResponseArgs{
		Message:  msg,
		Response: response,
	}

	for _, plugin := range plugins {
		if plugin.jsonrpc == nil {
			continue
		}

		var reply struct{}
		err := callPluginOnResponseWithTimeout(plugin, args, &reply, pluginRPCTimeout)
		if err != nil {
			logger.Warnf("Plugin %s error in OnResponse: %v", plugin.Name, err)
		}
	}

	return nil
}

// BeforeSend runs optional pre-send hooks and returns mutated response/upload files.
func (pm *Manager) BeforeSend(
	ctx context.Context,
	m *discordgo.MessageCreate,
	response string,
) (string, []LocalFile, bool, error) {
	pm.mu.RLock()
	plugins := make([]*Client, 0, len(pm.plugins))
	for _, p := range pm.plugins {
		plugins = append(plugins, p)
	}
	pm.mu.RUnlock()

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].priority > plugins[j].priority
	})

	currentResponse := response
	uploadFiles := make([]LocalFile, 0)
	msg := types.FromDiscordgo(m)

	for _, plugin := range plugins {
		if err := ctx.Err(); err != nil {
			return currentResponse, uploadFiles, false, fmt.Errorf("before_send cancelled: %w", err)
		}

		if plugin.jsonrpc == nil {
			continue
		}

		var reply BeforeSendResult
		err := callPluginBeforeSendWithTimeout(
			plugin,
			BeforeSendArgs{Message: msg, Response: currentResponse},
			&reply,
			pluginBeforeSendTimeout,
		)
		if err != nil {
			if isMethodNotFoundPluginError(err) {
				continue
			}
			return "", nil, false, fmt.Errorf("plugin %s before_send error: %w", plugin.Name, err)
		}

		if reply.Response != "" {
			currentResponse = reply.Response
		}
		if len(reply.Files) > 0 {
			uploadFiles = append(uploadFiles, reply.Files...)
		}
		if reply.SkipSend {
			return currentResponse, uploadFiles, true, nil
		}
	}

	return currentResponse, uploadFiles, false, nil
}



func isMethodNotFoundPluginError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "method not found") ||
		strings.Contains(msg, "jsonrpc -32601")
}

func callJSONRPCWithTimeout(
	client *stdioJSONRPCClient,
	wg *sync.WaitGroup,
	method string,
	params interface{},
	reply interface{},
	timeout time.Duration,
) error {
	if client == nil {
		return fmt.Errorf("jsonrpc client is nil for method %s", method)
	}

	done := make(chan error, 1)
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[plugin] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()
		if wg != nil {
			defer wg.Done()
		}
		done <- client.Call(method, params, reply)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("jsonrpc call timeout (%dms): %s", timeout/time.Millisecond, method)
	}
}

func callPluginOnMessageWithTimeout(
	plugin *Client,
	msg types.DiscordMessage,
	reply *bool,
	timeout time.Duration,
) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "on_message", msg, reply, timeout)
	}

	return fmt.Errorf("plugin %s has no jsonrpc transport", plugin.Name)
}

func callPluginOnResponseWithTimeout(
	plugin *Client,
	args ResponseArgs,
	reply *struct{},
	timeout time.Duration,
) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "on_response", args, reply, timeout)
	}

	return fmt.Errorf("plugin %s has no jsonrpc transport", plugin.Name)
}

func callPluginBeforeSendWithTimeout(
	plugin *Client,
	args BeforeSendArgs,
	reply *BeforeSendResult,
	timeout time.Duration,
) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "before_send", args, reply, timeout)
	}

	return fmt.Errorf("plugin %s has no jsonrpc transport", plugin.Name)
}



// ListPlugins returns a list of loaded plugins
func (pm *Manager) ListPlugins() []Info {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	infos := make([]Info, 0, len(pm.plugins))
	for _, p := range pm.plugins {
		infos = append(infos, p.Info)
	}
	return infos
}

// ListPluginsExt returns a list of loaded plugins with extended info
func (pm *Manager) ListPluginsExt() []InfoExt {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	infos := make([]InfoExt, 0, len(pm.plugins)+len(pm.disabled))
	for _, p := range pm.plugins {
		infos = append(infos, InfoExt{
			Info:    p.Info,
			Enabled: true,
		})
	}

	for _, p := range pm.disabled {
		infos = append(infos, InfoExt{
			Info:    p.Info,
			Enabled: false,
		})
	}
	return infos
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
func (pm *Manager) ExecuteTool(pluginName string, toolName string, args map[string]interface{}) (string, error) {
	pm.mu.RLock()
	plugin, exists := pm.plugins[pluginName]
	pm.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("plugin %s not found", pluginName)
	}

	if plugin.runtime == pluginRuntimeCommand {
		return executeCommandTool(plugin, toolName, args)
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
		return "", fmt.Errorf("tool %q has no timeout configured 鈥?set tool_timeouts in plugin config or operations.plugins.default_tool_timeout_ms in main config", toolName)
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

func executeCommandTool(plugin *Client, toolName string, args map[string]interface{}) (string, error) {
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

	ctx, cancel := context.WithTimeout(context.Background(), pluginCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, tool.CommandPath, commandArgs...)
	cmd.Dir = plugin.configDir
	cmd.Env = append(os.Environ(),
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

	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return fmt.Sprint(typed), nil
	case float64:
		return fmt.Sprint(typed), nil
	case float32:
		return fmt.Sprint(typed), nil
	case int:
		return fmt.Sprint(typed), nil
	case int64:
		return fmt.Sprint(typed), nil
	case int32:
		return fmt.Sprint(typed), nil
	case uint:
		return fmt.Sprint(typed), nil
	case uint64:
		return fmt.Sprint(typed), nil
	case uint32:
		return fmt.Sprint(typed), nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("failed to encode argument: %w", err)
		}
		return string(encoded), nil
	}
}
