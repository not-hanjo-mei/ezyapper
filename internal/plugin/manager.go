package plugin

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

// NewManager creates a new plugin manager
func NewManager(defaultToolTimeoutMs int, startupTimeoutSec int, rpcTimeoutSec int, beforeSendTimeoutSec int, commandTimeoutSec int, shutdownTimeoutSec int, disableTimeoutSec int, maxRestarts int, restartCooldownSec int) *Manager {
	return &Manager{
		plugins:              make(map[string]*Client),
		disabled:             make(map[string]disabledPlugin),
		defaultToolTimeoutMs: defaultToolTimeoutMs,
		startupTimeoutSec:    startupTimeoutSec,
		rpcTimeoutSec:        rpcTimeoutSec,
		beforeSendTimeoutSec: beforeSendTimeoutSec,
		commandTimeoutSec:    commandTimeoutSec,
		shutdownTimeoutSec:   shutdownTimeoutSec,
		disableTimeoutSec:    disableTimeoutSec,
		maxRestarts:          maxRestarts,
		restartCooldown:      time.Duration(restartCooldownSec) * time.Second,
	}
}

// OnMessage calls all plugins' OnMessage methods
// Returns false if any plugin wants to block the message
func (pm *Manager) OnMessage(ctx context.Context, msg types.DiscordMessage) (bool, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	plugins := make([]*Client, 0, len(pm.plugins))
	for _, p := range pm.plugins {
		plugins = append(plugins, p)
	}

	// Sort by priority (highest first)
	slices.SortFunc(plugins, func(a, b *Client) int {
		return cmp.Compare(b.priority, a.priority)
	})

	for _, plugin := range plugins {
		if plugin.jsonrpc == nil {
			continue
		}

		if plugin.jsonrpc.dead.Load() {
			pm.triggerRestart(plugin.Name, plugin.jsonrpc.DeadReason())
			continue
		}

		var shouldContinue bool
		err := callPluginOnMessageWithTimeout(plugin, msg, &shouldContinue, time.Duration(pm.rpcTimeoutSec)*time.Second)
		if err != nil {
			if isClientDeadError(err) {
				pm.triggerRestart(plugin.Name, err.Error())
			}
			logger.Warnf("[plugin] Plugin %s error in OnMessage: %v", plugin.Name, err)
			continue
		}
		if !shouldContinue {
			logger.Debugf("[plugin] %s blocked message", plugin.Name)
			return false, nil
		}
	}

	return true, nil
}

// sortedPlugins returns the loaded JSON-RPC plugins, highest priority first.
// Callers must hold pm.mu (read).
func (pm *Manager) sortedPlugins() []*Client {
	plugins := make([]*Client, 0, len(pm.plugins))
	for _, p := range pm.plugins {
		if p.jsonrpc != nil {
			plugins = append(plugins, p)
		}
	}
	slices.SortFunc(plugins, func(a, b *Client) int {
		if c := cmp.Compare(b.priority, a.priority); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return plugins
}

// AfterDecision runs the after_decision hook of every plugin, in priority
// order, once the bot has decided to reply and before generation starts.
// It returns cancel=true if any plugin cancels the reply, and the prompt
// context blocks the plugins injected (in plugin priority order).
func (pm *Manager) AfterDecision(ctx context.Context, msg types.DiscordMessage, reason string) (bool, []ContextBlock) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var blocks []ContextBlock
	for _, plugin := range pm.sortedPlugins() {
		if ctx.Err() != nil {
			return false, blocks
		}
		if plugin.jsonrpc.dead.Load() {
			pm.triggerRestart(plugin.Name, plugin.jsonrpc.DeadReason())
			continue
		}
		var reply AfterDecisionResult
		err := callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "after_decision",
			AfterDecisionArgs{Message: msg, Reason: reason}, &reply, time.Duration(pm.rpcTimeoutSec)*time.Second)
		if err != nil {
			if isMethodNotFoundPluginError(err) {
				continue
			}
			if isClientDeadError(err) {
				pm.triggerRestart(plugin.Name, err.Error())
			}
			logger.Warnf("[plugin] Plugin %s error in after_decision: %v", plugin.Name, err)
			continue
		}
		for _, b := range reply.Context {
			if clean, ok := sanitizeContextBlock(b); ok {
				blocks = append(blocks, clean)
			} else {
				logger.Warnf("[plugin] Plugin %s injected a context block with invalid tag %q, dropped", plugin.Name, b.Tag)
			}
		}
		if reply.Cancel {
			logger.Infof("[plugin] %s cancelled the reply in after_decision", plugin.Name)
			return true, blocks
		}
	}
	return false, blocks
}

// AfterSend runs the after_send hook of every plugin with the final text and
// the IDs of the sent messages. Errors are logged, never returned.
func (pm *Manager) AfterSend(ctx context.Context, msg types.DiscordMessage, response string, messageIDs []string) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	args := AfterSendArgs{Message: msg, Response: response, MessageIDs: messageIDs}
	for _, plugin := range pm.sortedPlugins() {
		if plugin.jsonrpc.dead.Load() {
			pm.triggerRestart(plugin.Name, plugin.jsonrpc.DeadReason())
			continue
		}
		var reply struct{}
		err := callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "after_send", args, &reply, time.Duration(pm.rpcTimeoutSec)*time.Second)
		if err != nil {
			if isMethodNotFoundPluginError(err) {
				continue
			}
			if isClientDeadError(err) {
				pm.triggerRestart(plugin.Name, err.Error())
			}
			logger.Warnf("[plugin] Plugin %s error in after_send: %v", plugin.Name, err)
		}
	}
}

// SystemPromptBlocks returns the static system prompt sections plugins
// registered at load time, in plugin priority order.
func (pm *Manager) SystemPromptBlocks() []ContextBlock {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	var blocks []ContextBlock
	for _, plugin := range pm.sortedPlugins() {
		if plugin.systemPrompt != nil {
			blocks = append(blocks, *plugin.systemPrompt)
		}
	}
	return blocks
}

// sanitizeContextBlock validates a plugin-supplied tag so it cannot break
// out of its XML section, and trims the content.
func sanitizeContextBlock(b ContextBlock) (ContextBlock, bool) {
	tag := strings.TrimSpace(b.Tag)
	if tag == "" || len(tag) > 64 {
		return ContextBlock{}, false
	}
	for _, r := range tag {
		if !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ContextBlock{}, false
		}
	}
	content := strings.TrimSpace(b.Content)
	if content == "" {
		return ContextBlock{}, false
	}
	// Prevent the content from closing its own tag early.
	content = strings.ReplaceAll(content, "</"+tag+">", "<\\/"+tag+">")
	return ContextBlock{Tag: tag, Content: content}, true
}

// BeforeSend runs optional pre-send hooks and returns mutated response/upload files.
func (pm *Manager) BeforeSend(
	ctx context.Context,
	msg types.DiscordMessage,
	response string,
) (string, []LocalFile, bool, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	plugins := pm.sortedPlugins()

	currentResponse := response
	uploadFiles := make([]LocalFile, 0)

	for _, plugin := range plugins {
		if err := ctx.Err(); err != nil {
			return currentResponse, uploadFiles, false, fmt.Errorf("before_send cancelled: %w", err)
		}

		if plugin.jsonrpc == nil {
			continue
		}

		if plugin.jsonrpc.dead.Load() {
			pm.triggerRestart(plugin.Name, plugin.jsonrpc.DeadReason())
			continue
		}

		var reply BeforeSendResult
		timeout := time.Duration(pm.beforeSendTimeoutSec) * time.Second
		err := callPluginBeforeSendWithTimeout(
			plugin,
			BeforeSendArgs{Message: msg, Response: currentResponse, DeadlineUnixMs: time.Now().Add(timeout).UnixMilli()},
			&reply,
			timeout,
		)
		if err != nil {
			if isClientDeadError(err) {
				logger.Errorf("[plugin] Plugin %s before_send failed, skipping it for this message: %v", plugin.Name, err)
				pm.triggerRestart(plugin.Name, err.Error())
				continue
			}
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
