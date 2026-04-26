// Package plugin provides a cross-platform plugin system with JSON-RPC and command runtimes.
// This works on Windows, Linux, and macOS by running plugins as separate processes.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// Interface defines the methods a plugin must implement
type Interface interface {
	// Info returns plugin metadata
	Info() (Info, error)

	// OnMessage is called for every message
	// Returns: shouldContinue (bool), error
	OnMessage(msg DiscordMessage) (bool, error)

	// OnResponse is called after the bot generates a response
	OnResponse(msg DiscordMessage, response string) error

	// Shutdown is called when the plugin is being stopped
	Shutdown() error
}

// ToolSpec describes a callable tool exposed by a plugin.
type ToolSpec struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
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
	BeforeSend(msg DiscordMessage, response string) (BeforeSendResult, error)
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

// DiscordMessage is a serializable version of discordgo.MessageCreate
type DiscordMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	AuthorID  string `json:"author_id"`
	Username  string `json:"username"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	IsBot     bool   `json:"is_bot"`
}

// FromDiscordgo converts a discordgo.MessageCreate to DiscordMessage
func FromDiscordgo(m *discordgo.MessageCreate) DiscordMessage {
	return DiscordMessage{
		ID:        m.ID,
		ChannelID: m.ChannelID,
		GuildID:   m.GuildID,
		AuthorID:  m.Author.ID,
		Username:  m.Author.Username,
		Content:   m.Content,
		Timestamp: m.Timestamp.Format(time.RFC3339),
		IsBot:     m.Author.Bot,
	}
}

// ResponseArgs holds arguments for OnResponse
type ResponseArgs struct {
	Message  DiscordMessage
	Response string
}

// BeforeSendArgs holds arguments for before-send plugin hooks.
type BeforeSendArgs struct {
	Message  DiscordMessage
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
	plugins  map[string]*Client
	disabled map[string]disabledPlugin
	mu       sync.RWMutex
}

// NewManager creates a new plugin manager
func NewManager() *Manager {
	return &Manager{
		plugins:  make(map[string]*Client),
		disabled: make(map[string]disabledPlugin),
	}
}

// LoadPlugin loads a plugin from a binary path
func (pm *Manager) LoadPlugin(pluginPath string) error {
	absPath := toAbsolutePath(pluginPath)
	return pm.loadPluginWithConfig(absPath, resolvePluginConfigDir(absPath))
}

func (pm *Manager) loadPluginWithConfig(pluginPath string, pluginConfigDir string) error {
	pluginPath = toAbsolutePath(pluginPath)
	pluginConfigDir = toAbsolutePath(pluginConfigDir)

	if pluginConfigDir == "" {
		if pluginPath == "" {
			return fmt.Errorf("plugin path is required")
		}

		pluginInfo, err := os.Stat(pluginPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("plugin not found: %s", pluginPath)
			}
			return fmt.Errorf("failed to stat plugin path %s: %w", pluginPath, err)
		}

		if pluginInfo.IsDir() {
			pluginConfigDir = pluginPath
		} else {
			pluginConfigDir = filepath.Dir(pluginPath)
		}
	}

	configInfo, err := os.Stat(pluginConfigDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plugin config directory not found: %s", pluginConfigDir)
		}
		return fmt.Errorf("failed to stat plugin config directory %s: %w", pluginConfigDir, err)
	}
	if !configInfo.IsDir() {
		return fmt.Errorf("plugin config path is not a directory: %s", pluginConfigDir)
	}

	if pluginPath != "" {
		if _, err := os.Stat(pluginPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("plugin not found: %s", pluginPath)
			}
			return fmt.Errorf("failed to stat plugin path %s: %w", pluginPath, err)
		}
	}

	manifest, err := loadPluginManifest(pluginConfigDir)
	if err != nil {
		return err
	}

	runtimeMode, err := resolvePluginRuntime(manifest)
	if err != nil {
		return err
	}

	targetLabel := pluginPath
	if targetLabel == "" {
		targetLabel = pluginConfigDir
	}
	logger.Infof("Loading plugin: %s", targetLabel)

	if runtimeMode == pluginRuntimeCommand {
		if pluginPath == "" {
			pluginPath = pluginConfigDir
		}
		return pm.loadCommandPlugin(pluginPath, pluginConfigDir, manifest)
	}

	if pluginPath == "" {
		return fmt.Errorf("jsonrpc plugin binary path is required for directory %s", pluginConfigDir)
	}

	pluginInfo, err := os.Stat(pluginPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plugin not found: %s", pluginPath)
		}
		return fmt.Errorf("failed to stat plugin path %s: %w", pluginPath, err)
	}
	if pluginInfo.IsDir() {
		return fmt.Errorf("jsonrpc plugin path must be a file: %s", pluginPath)
	}

	return pm.loadRPCPlugin(pluginPath, pluginConfigDir)
}

func (pm *Manager) loadRPCPlugin(pluginPath string, pluginConfigDir string) error {
	cmd := exec.Command(pluginPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("EZYAPPER_PLUGIN_PATH=%s", pluginConfigDir),
		fmt.Sprintf("EZYAPPER_PLUGIN_CONFIG=%s", filepath.Join(pluginConfigDir, "config.yaml")),
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create plugin stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("failed to create plugin stdout pipe: %w", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return fmt.Errorf("failed to start plugin: %w", err)
	}

	jsonClient := newStdioJSONRPCClient(stdin, stdout)

	var info Info
	if err := callJSONRPCWithTimeout(jsonClient, "info", map[string]interface{}{}, &info, pluginStartupTimeout); err != nil {
		jsonClient.Close()
		cmd.Process.Kill()
		return fmt.Errorf("plugin failed to initialize jsonrpc: %w", err)
	}

	pluginTools, err := listPluginToolsJSONRPC(jsonClient)
	if err != nil {
		logger.Warnf("Failed to list tools for plugin %s: %v", info.Name, err)
		pluginTools = []ToolSpec{}
	}

	logger.Infof("Plugin loaded: %s v%s by %s (runtime: %s)", info.Name, info.Version, info.Author, pluginRuntimeJSONRPC)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.plugins[info.Name]; exists {
		jsonClient.Close()
		cmd.Process.Kill()
		return fmt.Errorf("plugin %s already loaded", info.Name)
	}

	pm.plugins[info.Name] = &Client{
		Name:      info.Name,
		Info:      info,
		jsonrpc:   jsonClient,
		process:   cmd.Process,
		priority:  info.Priority,
		path:      pluginPath,
		configDir: pluginConfigDir,
		tools:     pluginTools,
		runtime:   pluginRuntimeJSONRPC,
	}
	delete(pm.disabled, info.Name)

	return nil
}

func (pm *Manager) loadCommandPlugin(
	pluginPath string,
	pluginConfigDir string,
	manifest *pluginManifest,
) error {
	if manifest == nil {
		return fmt.Errorf("plugin manifest is required for command runtime: %s", pluginConfigDir)
	}

	pluginName := strings.TrimSpace(manifest.Name)
	if pluginName == "" {
		pluginName = filepath.Base(pluginConfigDir)
	}

	if len(manifest.Tools) == 0 {
		return fmt.Errorf("command plugin %s has no tools in plugin.json", pluginName)
	}

	tools := make([]ToolSpec, 0, len(manifest.Tools))
	commands := make(map[string]commandTool, len(manifest.Tools))

	for _, rawTool := range manifest.Tools {
		toolName := strings.TrimSpace(rawTool.Name)
		if toolName == "" {
			return fmt.Errorf("command plugin %s contains a tool with empty name", pluginName)
		}
		if _, exists := commands[toolName]; exists {
			return fmt.Errorf("command plugin %s contains duplicate tool %s", pluginName, toolName)
		}

		commandPath, err := resolvePluginCommandPath(rawTool.Command, pluginConfigDir)
		if err != nil {
			return fmt.Errorf("command plugin %s tool %s command error: %w", pluginName, toolName, err)
		}

		argKeys := make([]string, 0, len(rawTool.ArgKeys))
		for _, rawArgKey := range rawTool.ArgKeys {
			key := strings.TrimSpace(rawArgKey)
			if key == "" {
				return fmt.Errorf("command plugin %s tool %s has empty arg_keys entry", pluginName, toolName)
			}
			argKeys = append(argKeys, key)
		}

		toolArgs := append([]string{}, rawTool.Args...)
		toolParameters := rawTool.Parameters
		if toolParameters == nil {
			toolParameters = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		toolDescription := strings.TrimSpace(rawTool.Description)
		if toolDescription == "" {
			toolDescription = "Plugin tool"
		}

		commands[toolName] = commandTool{
			CommandPath: commandPath,
			Args:        toolArgs,
			ArgKeys:     argKeys,
		}

		tools = append(tools, ToolSpec{
			Name:        toolName,
			Description: toolDescription,
			Parameters:  toolParameters,
		})
	}

	info := Info{
		Name:        pluginName,
		Version:     strings.TrimSpace(manifest.Version),
		Author:      strings.TrimSpace(manifest.Author),
		Description: strings.TrimSpace(manifest.Description),
		Priority:    manifest.Priority,
	}

	logger.Infof("Plugin loaded: %s v%s by %s (runtime: command)", info.Name, info.Version, info.Author)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.plugins[info.Name]; exists {
		return fmt.Errorf("plugin %s already loaded", info.Name)
	}

	pm.plugins[info.Name] = &Client{
		Name:      info.Name,
		Info:      info,
		priority:  info.Priority,
		path:      pluginPath,
		configDir: pluginConfigDir,
		tools:     tools,
		runtime:   pluginRuntimeCommand,
		commands:  commands,
	}
	delete(pm.disabled, info.Name)

	return nil
}

func isExecutableEntry(entry os.DirEntry) bool {
	name := entry.Name()
	if runtime.GOOS == "windows" {
		return filepath.Ext(name) == ".exe"
	}

	info, err := entry.Info()
	if err != nil {
		return false
	}

	return info.Mode()&0111 != 0
}

func resolvePluginConfigDir(pluginPath string) string {
	if pluginPath == "" {
		return ""
	}

	pluginPath = toAbsolutePath(pluginPath)
	if info, err := os.Stat(pluginPath); err == nil && info.IsDir() {
		return pluginPath
	}

	baseDir := filepath.Dir(pluginPath)
	baseName := strings.TrimSuffix(filepath.Base(pluginPath), filepath.Ext(pluginPath))

	candidates := []string{
		filepath.Join(baseDir, baseName),
		filepath.Join(baseDir, strings.TrimSuffix(baseName, "-plugin")),
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	return baseDir
}

func loadPluginManifest(pluginDir string) (*pluginManifest, error) {
	manifestPath := filepath.Join(pluginDir, "plugin.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read plugin manifest %s: %w", manifestPath, err)
	}

	var manifest pluginManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse plugin manifest %s: %w", manifestPath, err)
	}

	return &manifest, nil
}

func resolvePluginRuntime(manifest *pluginManifest) (string, error) {
	if manifest == nil {
		return pluginRuntimeJSONRPC, nil
	}

	rawRuntime := strings.ToLower(strings.TrimSpace(manifest.Runtime))
	if rawRuntime == "" {
		return "", fmt.Errorf("plugin manifest runtime is required (supported: %s, %s)", pluginRuntimeJSONRPC, pluginRuntimeCommand)
	}

	if rawRuntime == pluginRuntimeJSONRPC {
		return pluginRuntimeJSONRPC, nil
	}

	if rawRuntime == pluginRuntimeCommand {
		return pluginRuntimeCommand, nil
	}

	return "", fmt.Errorf("unsupported plugin runtime %q", manifest.Runtime)
}

func toAbsolutePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}

	cleaned := filepath.Clean(trimmed)
	if filepath.IsAbs(cleaned) {
		return cleaned
	}

	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return cleaned
	}

	return filepath.Clean(abs)
}

func resolveExecutablePath(candidate string) (string, error) {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return "", fmt.Errorf("plugin command is required")
	}

	cleaned := filepath.Clean(trimmed)
	candidates := []string{cleaned}
	if runtime.GOOS == "windows" && filepath.Ext(cleaned) == "" {
		candidates = append(candidates, cleaned+".exe")
	}

	for _, item := range candidates {
		if _, err := os.Stat(item); err == nil {
			return toAbsolutePath(item), nil
		}
	}

	return "", fmt.Errorf("plugin command not found: %s", cleaned)
}

func resolvePluginCommandPath(command string, pluginDir string) (string, error) {
	trimmedCommand := strings.TrimSpace(command)
	if trimmedCommand == "" {
		return "", fmt.Errorf("plugin command is required")
	}

	absPluginDir := toAbsolutePath(pluginDir)
	hasPathHint := strings.ContainsAny(trimmedCommand, `/\\`) || strings.HasPrefix(trimmedCommand, ".")

	if filepath.IsAbs(trimmedCommand) || hasPathHint {
		candidate := trimmedCommand
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(absPluginDir, candidate)
		}
		return resolveExecutablePath(candidate)
	}

	if lookedUp, err := exec.LookPath(trimmedCommand); err == nil {
		return toAbsolutePath(lookedUp), nil
	}

	return resolveExecutablePath(filepath.Join(absPluginDir, trimmedCommand))
}

// LoadPluginsFromDir loads all plugins from a directory
func (pm *Manager) LoadPluginsFromDir(dir string) error {
	dir = toAbsolutePath(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Infof("Plugins directory does not exist: %s", dir)
			return nil
		}
		return fmt.Errorf("failed to read plugins directory: %w", err)
	}

	targets := make([]pluginLoadTarget, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			pluginDir := toAbsolutePath(filepath.Join(dir, entry.Name()))

			manifest, manifestErr := loadPluginManifest(pluginDir)
			if manifestErr != nil {
				logger.Warnf("Failed to parse plugin manifest in %s: %v", pluginDir, manifestErr)
				continue
			}

			if manifest != nil {
				runtimeMode, runtimeErr := resolvePluginRuntime(manifest)
				if runtimeErr != nil {
					logger.Warnf("Invalid plugin runtime in %s: %v", pluginDir, runtimeErr)
					continue
				}

				if runtimeMode == pluginRuntimeCommand {
					targets = append(targets, pluginLoadTarget{
						binaryPath: pluginDir,
						configDir:  pluginDir,
					})
					continue
				}
			}

			subEntries, err := os.ReadDir(pluginDir)
			if err != nil {
				logger.Warnf("Failed to read plugin directory %s: %v", pluginDir, err)
				continue
			}

			executables := make([]os.DirEntry, 0)
			for _, subEntry := range subEntries {
				if subEntry.IsDir() {
					continue
				}
				if isExecutableEntry(subEntry) {
					executables = append(executables, subEntry)
				}
			}

			if len(executables) == 0 {
				continue
			}

			sort.Slice(executables, func(i, j int) bool {
				return executables[i].Name() < executables[j].Name()
			})

			selected := executables[0]
			if len(executables) > 1 {
				logger.Warnf(
					"Multiple plugin binaries found in %s, using %s",
					pluginDir,
					selected.Name(),
				)
			}

			targets = append(targets, pluginLoadTarget{
				binaryPath: filepath.Join(pluginDir, selected.Name()),
				configDir:  pluginDir,
			})
			continue
		}

		if !isExecutableEntry(entry) {
			continue
		}

		pluginPath := toAbsolutePath(filepath.Join(dir, entry.Name()))
		targets = append(targets, pluginLoadTarget{
			binaryPath: pluginPath,
			configDir:  resolvePluginConfigDir(pluginPath),
		})
	}

	if len(targets) == 0 {
		return nil
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].binaryPath < targets[j].binaryPath
	})

	maxConcurrentLoads := runtime.NumCPU()
	if maxConcurrentLoads < 1 {
		maxConcurrentLoads = 1
	}
	if maxConcurrentLoads > len(targets) {
		maxConcurrentLoads = len(targets)
	}

	logger.Infof("Loading %d plugins with max parallelism %d", len(targets), maxConcurrentLoads)

	semaphore := make(chan struct{}, maxConcurrentLoads)
	var wg sync.WaitGroup

	for _, target := range targets {
		wg.Add(1)

		go func(t pluginLoadTarget) {
			defer func() {
				wg.Done()
				if r := recover(); r != nil {
					logger.Errorf("[plugin] panic recovered: %v\n%s", r, debug.Stack())
				}
			}()

			semaphore <- struct{}{}
			defer func() {
				<-semaphore
			}()

			if err := pm.loadPluginWithConfig(t.binaryPath, t.configDir); err != nil {
				logger.Warnf("Failed to load plugin %s: %v", filepath.Base(t.binaryPath), err)
			}
		}(target)
	}

	wg.Wait()

	return nil
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

	msg := FromDiscordgo(m)

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

	msg := FromDiscordgo(m)
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
	msg := FromDiscordgo(m)

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

func (pm *Manager) shutdownPlugin(plugin *Client) error {
	if plugin == nil {
		return nil
	}

	// Command runtime plugins do not keep a persistent JSON-RPC transport,
	// so there is no remote shutdown method to invoke.
	if plugin.runtime == pluginRuntimeCommand {
		return nil
	}

	err := callPluginShutdownWithTimeout(plugin, pluginRPCTimeout)
	if isBenignPluginShutdownError(err) {
		return nil
	}

	return err
}

func isBenignPluginShutdownError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return true
	}

	msg := err.Error()
	return strings.Contains(msg, "connection is shut down") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "pipe is being closed")
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
	method string,
	params interface{},
	reply interface{},
	timeout time.Duration,
) error {
	if client == nil {
		return fmt.Errorf("jsonrpc client is nil for method %s", method)
	}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[plugin] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()
		done <- client.Call(method, params, reply)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("jsonrpc call timeout: %s", method)
	}
}

func callPluginOnMessageWithTimeout(
	plugin *Client,
	msg DiscordMessage,
	reply *bool,
	timeout time.Duration,
) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, "on_message", msg, reply, timeout)
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
		return callJSONRPCWithTimeout(plugin.jsonrpc, "on_response", args, reply, timeout)
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
		return callJSONRPCWithTimeout(plugin.jsonrpc, "before_send", args, reply, timeout)
	}

	return fmt.Errorf("plugin %s has no jsonrpc transport", plugin.Name)
}

func callPluginShutdownWithTimeout(plugin *Client, timeout time.Duration) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	var reply struct{}
	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, "shutdown", map[string]interface{}{}, &reply, timeout)
	}

	return fmt.Errorf("plugin %s has no jsonrpc transport", plugin.Name)
}

func (pm *Manager) stopPluginProcess(plugin *Client, timeout time.Duration) {
	if plugin == nil {
		return
	}

	if plugin.jsonrpc != nil {
		plugin.jsonrpc.Close()
	}

	if plugin.process == nil {
		return
	}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[plugin] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()
		_, err := plugin.process.Wait()
		done <- err
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		plugin.process.Kill()
	}
}

// Shutdown gracefully stops all plugins
func (pm *Manager) Shutdown(ctx context.Context) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var errs []error
	for name, plugin := range pm.plugins {
		logger.Infof("Shutting down plugin: %s", name)

		if err := pm.shutdownPlugin(plugin); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s shutdown error: %w", name, err))
		}

		pm.stopPluginProcess(plugin, 5*time.Second)
	}

	pm.plugins = make(map[string]*Client)

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %v", errs)
	}
	return nil
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
		return executeJSONRPCTool(plugin, toolName, args)
	}

	return "", fmt.Errorf("jsonrpc plugin is not initialized")
}

func executeJSONRPCTool(plugin *Client, toolName string, args map[string]interface{}) (string, error) {
	if plugin == nil || plugin.jsonrpc == nil {
		return "", fmt.Errorf("jsonrpc plugin is not initialized")
	}

	var reply string
	err := callJSONRPCWithTimeout(
		plugin.jsonrpc,
		"execute_tool",
		ExecuteToolArgs{Name: toolName, Arguments: args},
		&reply,
		pluginRPCTimeout,
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

	ctx, cancel := context.WithTimeout(context.TODO(), pluginCommandTimeout)
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

// EnablePlugin loads a disabled plugin back into memory.
func (pm *Manager) EnablePlugin(name string) error {
	pm.mu.RLock()
	if _, exists := pm.plugins[name]; exists {
		pm.mu.RUnlock()
		return nil
	}

	disabled, exists := pm.disabled[name]
	pm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	if err := pm.loadPluginWithConfig(disabled.Path, disabled.ConfigDir); err != nil {
		return fmt.Errorf("failed to enable plugin %s: %w", name, err)
	}

	logger.Infof("Plugin enabled: %s", name)
	return nil
}

// DisablePlugin stops and removes a plugin
func (pm *Manager) DisablePlugin(name string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	plugin, exists := pm.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	// Shutdown the plugin
	if err := pm.shutdownPlugin(plugin); err != nil {
		logger.Warnf("Error shutting down plugin %s: %v", name, err)
	}

	pm.stopPluginProcess(plugin, 2*time.Second)

	delete(pm.plugins, name)
	pm.disabled[name] = disabledPlugin{
		Info:      plugin.Info,
		Path:      plugin.path,
		ConfigDir: plugin.configDir,
	}
	logger.Infof("Plugin disabled: %s", name)
	return nil
}
