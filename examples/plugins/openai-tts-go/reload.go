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

// reloadingPlugin serves the current openAITTSPlugin. A config reload
// builds a complete new instance (validated config, HTTP client with the
// new timeout, output directory) and swaps it in only if that succeeded.
// Requests already running finish on the instance they started on.
type reloadingPlugin struct {
	cur atomic.Pointer[openAITTSPlugin]
}

func (r *reloadingPlugin) Info() (plugin.Info, error) { return r.cur.Load().Info() }

func (r *reloadingPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	return r.cur.Load().OnMessage(msg)
}

func (r *reloadingPlugin) BeforeSend(args plugin.BeforeSendArgs) (plugin.BeforeSendResult, error) {
	return r.cur.Load().BeforeSend(args)
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
	next, err := newOpenAITTSPlugin(cfg)
	if err != nil {
		return err
	}
	if old := r.cur.Swap(next); old != nil {
		old.httpClient.CloseIdleConnections()
	}
	if err := pluginserver.NotifyToolsChanged(); err != nil {
		fmt.Fprintf(os.Stderr, "[OPENAI-TTS] config reloaded but the host could not be notified: %v\n", err)
	}
	return nil
}

func (r *reloadingPlugin) watch() *pluginconfig.Watcher {
	w, err := pluginconfig.Watch(pluginConfigPath(), pluginconfig.Options{Name: "OPENAI-TTS"}, r.reload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[OPENAI-TTS] config hot reload unavailable: %v\n", err)
	}
	return w
}
