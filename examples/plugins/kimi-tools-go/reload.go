package main

import (
	"fmt"
	"os"
	"sync/atomic"

	"ezyapper/internal/plugin"
	"ezyapper/internal/plugin/pluginconfig"
	pluginserver "ezyapper/internal/plugin/server"
	"ezyapper/internal/types"
)

// reloadingPlugin serves the current kimiToolsPlugin. A config reload
// builds a complete new instance (new HTTP client, tool lists fetched from
// the new formulas/base_url/api_key) and swaps it in only if that
// succeeded, so a bad key or unreachable formula keeps the old tools.
type reloadingPlugin struct {
	cur atomic.Pointer[kimiToolsPlugin]
}

func (r *reloadingPlugin) Info() (plugin.Info, error) { return r.cur.Load().Info() }

func (r *reloadingPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	return r.cur.Load().OnMessage(msg)
}

func (r *reloadingPlugin) Shutdown() error { return r.cur.Load().Shutdown() }

func (r *reloadingPlugin) ListTools() ([]plugin.ToolSpec, error) { return r.cur.Load().ListTools() }

func (r *reloadingPlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	return r.cur.Load().ExecuteTool(name, args)
}

func (r *reloadingPlugin) reload() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.ToolTimeoutMs <= 0 {
		return fmt.Errorf("tool_timeouts.default_ms is required and must be > 0")
	}
	next, err := newKimiToolsPlugin(cfg)
	if err != nil {
		return err
	}
	old := r.cur.Swap(next)
	if old != nil {
		old.http.CloseIdleConnections()
	}
	if err := pluginserver.NotifyToolsChanged(); err != nil {
		fmt.Fprintf(os.Stderr, "[KIMI-TOOLS] config reloaded but the host could not be notified: %v\n", err)
	}
	return nil
}

func (r *reloadingPlugin) watch() *pluginconfig.Watcher {
	w, err := pluginconfig.Watch(pluginConfigPath(), pluginconfig.Options{Name: "KIMI-TOOLS"}, r.reload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[KIMI-TOOLS] config hot reload unavailable: %v\n", err)
	}
	return w
}
