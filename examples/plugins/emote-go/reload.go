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

// reloadingPlugin serves the current EmotePlugin. A config reload builds a
// complete new instance (data dir, vision/emote LLM/CDN clients) and swaps
// it in only if that succeeded. Per-channel state that is in flight (an
// emote queued by send_emote and not yet posted, the last channel seen)
// moves to the new instance; the plugin's own Discord session moves too
// when the token did not change, otherwise the old one is closed and the
// new token connects lazily on the next send.
type reloadingPlugin struct {
	cur atomic.Pointer[EmotePlugin]
}

func (r *reloadingPlugin) Info() (plugin.Info, error) { return r.cur.Load().Info() }

func (r *reloadingPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	return r.cur.Load().OnMessage(msg)
}

func (r *reloadingPlugin) AfterSend(args plugin.AfterSendArgs) error {
	return r.cur.Load().AfterSend(args)
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
	next, err := newEmotePlugin(cfg)
	if err != nil {
		return err
	}
	old := r.cur.Load()

	old.mu.Lock()
	for ch, content := range old.sendQueue {
		next.sendQueue[ch] = content
	}
	next.lastChannelID = old.lastChannelID
	if old.config.DiscordToken == cfg.DiscordToken {
		next.discord = old.discord
	}
	r.cur.Store(next)
	old.mu.Unlock()

	if next.discord != old.discord && old.discord != nil {
		if err := old.discord.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "[EMOTE] closing the previous Discord session: %v\n", err)
		}
	}
	if err := pluginserver.NotifyToolsChanged(); err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] config reloaded but the host could not be notified: %v\n", err)
	}
	return nil
}

func (r *reloadingPlugin) watch() *pluginconfig.Watcher {
	w, err := pluginconfig.Watch(pluginConfigPath(), pluginconfig.Options{Name: "EMOTE"}, r.reload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] config hot reload unavailable: %v\n", err)
	}
	return w
}
