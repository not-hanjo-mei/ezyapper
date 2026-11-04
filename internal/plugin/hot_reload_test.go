package plugin

import (
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/types"
)

// fakeRPCPlugin answers JSON-RPC requests from the host over pipes. Before
// each reply it may emit queued notifications, like a plugin that reloaded
// its config between two host calls.
type fakeRPCPlugin struct {
	mu      sync.Mutex
	tools   []ToolSpec
	prompt  ContextBlock
	pending []string // notifications to emit before the next reply
}

func (f *fakeRPCPlugin) setTools(tools []ToolSpec, notify bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = tools
	if notify {
		f.pending = append(f.pending, NotificationToolsChanged)
	}
}

func (f *fakeRPCPlugin) serve(r io.Reader, w io.Writer) {
	dec := NewFrameDecoder(r)
	enc := json.NewEncoder(w)
	for {
		var req jsonRPCRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		f.mu.Lock()
		for _, m := range f.pending {
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": m})
		}
		f.pending = nil
		var result any = map[string]any{}
		switch req.Method {
		case "list_tools":
			result = f.tools
		case "on_message":
			result = true
		case "system_prompt":
			result = f.prompt
		}
		f.mu.Unlock()
		raw, _ := json.Marshal(result)
		_ = enc.Encode(jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: raw})
	}
}

func startFakePlugin(t *testing.T, pm *Manager, name string, f *fakeRPCPlugin) *stdioJSONRPCClient {
	t.Helper()
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	go f.serve(hostToPluginR, pluginToHostW)
	t.Cleanup(func() {
		hostToPluginW.Close()
		pluginToHostW.Close()
	})

	client := newStdioJSONRPCClient(hostToPluginW, pluginToHostR)
	client.SetName(name)
	pm.attachNotifications(client)

	tools, err := listPluginToolsJSONRPC(client, &pm.wg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pm.mu.Lock()
	pm.plugins[name] = &Client{Name: name, Info: Info{Name: name}, jsonrpc: client, tools: tools, runtime: pluginRuntimeJSONRPC, wg: &pm.wg}
	pm.mu.Unlock()
	return client
}

func toolNames(pm *Manager) map[string]bool {
	out := map[string]bool{}
	for _, t := range pm.ListTools() {
		out[t.Spec.Name] = true
	}
	return out
}

func TestToolsChangedNotification_RefreshesToolList(t *testing.T) {
	pm := NewManager(1000, 5, 5, 5, 5, 5, 5, 3, 60)
	var handlerCalls atomic.Int32
	pm.SetToolsChangedHandler(func() { handlerCalls.Add(1) })

	f := &fakeRPCPlugin{tools: []ToolSpec{{Name: "old_tool"}}}
	startFakePlugin(t, pm, "fake", f)
	if !toolNames(pm)["old_tool"] {
		t.Fatalf("initial tools missing: %v", toolNames(pm))
	}

	// The plugin reloads its config and announces new tools.
	f.setTools([]ToolSpec{{Name: "new_tool"}}, true)

	// The host reads the notification during its next exchange.
	if ok, err := pm.OnMessage(t.Context(), types.DiscordMessage{ID: "1"}); err != nil || !ok {
		t.Fatalf("OnMessage: ok=%v err=%v", ok, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && handlerCalls.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if handlerCalls.Load() == 0 {
		t.Fatal("tools-changed handler was not called")
	}
	names := toolNames(pm)
	if !names["new_tool"] || names["old_tool"] {
		t.Fatalf("tool list not refreshed: %v", names)
	}
}

func TestToolsChangedNotification_InvalidListKeepsOld(t *testing.T) {
	pm := NewManager(1000, 5, 5, 5, 5, 5, 5, 3, 60)
	var handlerCalls atomic.Int32
	pm.SetToolsChangedHandler(func() { handlerCalls.Add(1) })

	f := &fakeRPCPlugin{tools: []ToolSpec{{Name: "good"}}}
	client := startFakePlugin(t, pm, "fake", f)

	f.setTools([]ToolSpec{{Name: "bad", TimeoutMs: -1}}, false)
	pm.refreshTools(client)

	if handlerCalls.Load() != 0 {
		t.Fatal("handler called for a rejected tool list")
	}
	if !toolNames(pm)["good"] {
		t.Fatalf("previous tool list was not kept: %v", toolNames(pm))
	}
}

func TestCall_NotificationBeforeReplyIsDispatched(t *testing.T) {
	pm := NewManager(1000, 5, 5, 5, 5, 5, 5, 3, 60)
	f := &fakeRPCPlugin{}
	client := startFakePlugin(t, pm, "fake", f)

	got := make(chan string, 1)
	client.onNotify = func(m string) { got <- m }
	f.setTools(nil, true)

	var reply bool
	if err := client.Call("on_message", map[string]any{}, &reply); err != nil || !reply {
		t.Fatalf("call failed around a notification: reply=%v err=%v", reply, err)
	}
	select {
	case m := <-got:
		if m != NotificationToolsChanged {
			t.Fatalf("unexpected notification %q", m)
		}
	case <-time.After(time.Second):
		t.Fatal("notification was not dispatched")
	}
}

func TestUpdateSettings(t *testing.T) {
	pm := NewManager(1000, 1, 2, 3, 4, 5, 6, 7, 8)
	if s := pm.Settings(); s.RPCTimeoutSec != 2 || s.RestartCooldown != 8*time.Second {
		t.Fatalf("unexpected initial settings %+v", s)
	}
	pm.UpdateSettings(Settings{DefaultToolTimeoutMs: 9, RPCTimeoutSec: 20, RestartCooldown: time.Minute})
	s := pm.Settings()
	if s.RPCTimeoutSec != 20 || s.DefaultToolTimeoutMs != 9 || s.RestartCooldown != time.Minute {
		t.Fatalf("settings not updated: %+v", s)
	}
}

func TestReloadFromDir_DisabledUnloadsAndNotifies(t *testing.T) {
	pm := NewManager(1000, 5, 5, 5, 5, 5, 5, 3, 60)
	var handlerCalls atomic.Int32
	pm.SetToolsChangedHandler(func() { handlerCalls.Add(1) })
	pm.mu.Lock()
	pm.plugins["cmd"] = &Client{Name: "cmd", runtime: pluginRuntimeCommand, tools: []ToolSpec{{Name: "x"}}, wg: &pm.wg}
	pm.disabled["gone"] = disabledPlugin{}
	pm.mu.Unlock()

	if err := pm.ReloadFromDir(t.Context(), t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if len(pm.ListTools()) != 0 || len(pm.ListPluginsExt()) != 0 {
		t.Fatalf("plugins still loaded after disabling: %v", pm.ListPluginsExt())
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls.Load())
	}
}

func TestToolsChangedNotification_RefreshesSystemPrompt(t *testing.T) {
	pm := NewManager(1000, 5, 5, 5, 5, 5, 5, 3, 60)
	f := &fakeRPCPlugin{tools: []ToolSpec{{Name: "t"}}}
	client := startFakePlugin(t, pm, "fake", f)
	if len(pm.SystemPromptBlocks()) != 0 {
		t.Fatal("unexpected initial prompt section")
	}
	f.mu.Lock()
	f.prompt = ContextBlock{Tag: "clock", Content: "Timezone is now UTC+9"}
	f.mu.Unlock()
	pm.refreshTools(client)
	blocks := pm.SystemPromptBlocks()
	if len(blocks) != 1 || blocks[0].Content != "Timezone is now UTC+9" {
		t.Fatalf("system prompt section not refreshed: %+v", blocks)
	}
}
