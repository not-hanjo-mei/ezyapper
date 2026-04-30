package plugin

import (
	"context"
	"fmt"
	"sort"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

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
