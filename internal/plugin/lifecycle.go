package plugin

import (
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
)

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
	cmd.Env = buildPluginEnv(pluginConfigDir,
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
	if err := callJSONRPCWithTimeout(jsonClient, &pm.wg, "info", map[string]interface{}{}, &info, time.Duration(pm.startupTimeoutSec)*time.Second); err != nil {
		jsonClient.Close()
		cmd.Process.Kill()
		return fmt.Errorf("plugin failed to initialize jsonrpc: %w", err)
	}

	pluginTools, err := listPluginToolsJSONRPC(jsonClient, &pm.wg, time.Duration(pm.rpcTimeoutSec)*time.Second)
	if err != nil {
		logger.Warnf("Failed to list tools for plugin %s: %v", info.Name, err)
		pluginTools = []ToolSpec{}
	}

	for i := range pluginTools {
		if err := validateToolSpec(&pluginTools[i]); err != nil {
			jsonClient.Close()
			cmd.Process.Kill()
			return fmt.Errorf("invalid tool %q in plugin %s: %w", pluginTools[i].Name, info.Name, err)
		}
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
		wg:        &pm.wg,
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
			TimeoutMs:   rawTool.TimeoutMs,
		})
	}

	for i := range tools {
		if err := validateToolSpec(&tools[i]); err != nil {
			return fmt.Errorf("invalid tool %q in plugin %s: %w", tools[i].Name, pluginName, err)
		}
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
		wg:        &pm.wg,
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
		resolved, err := resolveExecutablePath(candidate)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(trimmedCommand) {
			if err := ensurePathWithinBase(resolved, absPluginDir); err != nil {
				return "", fmt.Errorf("plugin command path escapes plugin directory: %w", err)
			}
		}
		return resolved, nil
	}

	if lookedUp, err := exec.LookPath(trimmedCommand); err == nil {
		return toAbsolutePath(lookedUp), nil
	}

	resolved, err := resolveExecutablePath(filepath.Join(absPluginDir, trimmedCommand))
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func ensurePathWithinBase(resolved, base string) error {
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		return fmt.Errorf("cannot resolve plugin base directory %s: %w", base, err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return fmt.Errorf("cannot resolve plugin command path %s: %w", resolved, err)
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil {
		return fmt.Errorf("cannot determine relative path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("command %q is outside plugin directory %s", resolved, base)
	}
	return nil
}

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
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

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
		pm.wg.Add(1)

		go func(t pluginLoadTarget) {
			defer wg.Done()
			defer pm.wg.Done()
			defer func() {
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

func (pm *Manager) shutdownPlugin(plugin *Client) error {
	if plugin == nil {
		return nil
	}

	if plugin.runtime == pluginRuntimeCommand {
		return nil
	}

	err := callPluginShutdownWithTimeout(plugin, time.Duration(pm.rpcTimeoutSec)*time.Second)
	if isBenignPluginShutdownError(err) {
		return nil
	}

	return err
}

func isBenignPluginShutdownError(err error) bool {
	if err == nil {
		return true
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

func callPluginShutdownWithTimeout(plugin *Client, timeout time.Duration) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	var reply struct{}
	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "shutdown", map[string]interface{}{}, &reply, timeout)
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
	pm.wg.Add(1)
	go func() {
		defer pm.wg.Done()
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

// WaitForPending waits for all in-flight goroutines (RPC calls, process shutdown waits)
// to complete, with a timeout to prevent indefinite blocking.
func (pm *Manager) WaitForPending() error {
	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[plugin] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()
		pm.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(time.Duration(pm.shutdownTimeoutSec) * time.Second)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("timeout waiting for pending plugin operations")
	}
}

func (pm *Manager) Shutdown(ctx context.Context) error {
	pm.mu.Lock()
	plugins := make(map[string]*Client, len(pm.plugins))
	for name, plugin := range pm.plugins {
		plugins[name] = plugin
	}
	pm.plugins = make(map[string]*Client)
	pm.mu.Unlock()

	errs := []error{}
	for name, plugin := range plugins {
		logger.Infof("Shutting down plugin: %s", name)

		if err := pm.shutdownPlugin(plugin); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s shutdown error: %w", name, err))
		}

		pm.stopPluginProcess(plugin, time.Duration(pm.shutdownTimeoutSec)*time.Second)
	}

	if err := pm.WaitForPending(); err != nil {
		logger.Warnf("WaitForPending: %v", err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %v", errs)
	}
	return nil
}

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

func (pm *Manager) DisablePlugin(name string) error {
	pm.mu.Lock()
	plugin, exists := pm.plugins[name]
	if !exists {
		pm.mu.Unlock()
		return fmt.Errorf("plugin %s not found", name)
	}

	// Save metadata before removing so we can add to disabled registry later.
	info := plugin.Info
	pluginPath := plugin.path
	pluginConfigDir := plugin.configDir

	delete(pm.plugins, name)
	pm.mu.Unlock()

	// Perform RPC calls and process cleanup without holding the lock,
	// allowing concurrent plugin operations to proceed during shutdown.
	if err := pm.shutdownPlugin(plugin); err != nil {
		logger.Warnf("Error shutting down plugin %s: %v", name, err)
	}

	pm.stopPluginProcess(plugin, time.Duration(pm.disableTimeoutSec)*time.Second)

	pm.mu.Lock()
	pm.disabled[name] = disabledPlugin{
		Info:      info,
		Path:      pluginPath,
		ConfigDir: pluginConfigDir,
	}
	pm.mu.Unlock()

	logger.Infof("Plugin disabled: %s", name)
	return nil
}
