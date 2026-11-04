package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/ai/decision"
	"ezyapper/internal/ai/mcp"
	"ezyapper/internal/ai/tools"
	"ezyapper/internal/config"
	"ezyapper/internal/hotreload"
	"ezyapper/internal/ratelimit"

	"github.com/bwmarrin/discordgo"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var mcpTool = mcpsdk.Tool{Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object"}}

func exampleConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(filepath.Join("..", "..", "examples", "config.yaml.example"))
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	return cfg
}

func reloadTestBot(t *testing.T, cfg *config.Config) *Bot {
	t.Helper()
	store := &atomic.Value{}
	store.Store(cfg)
	b := &Bot{
		configStore:  store,
		toolRegistry: tools.NewToolRegistry(),
		rateLimiter:  ratelimit.NewLimiter(1, time.Second, time.Second),
		mcpManager:   mcp.NewMCPManager(nil),
		mcpToolNames: map[string]struct{}{},
	}
	s, _ := discordgo.New("Bot old")
	b.session.Store(s)
	return b
}

// apply runs the bot's two phases the way the reloader does.
func apply(t *testing.T, b *Bot, next *config.Config) error {
	t.Helper()
	old := b.cfg()
	p, err := b.Prepare(context.Background(), old, next, hotreload.Diff(old, next))
	if err != nil {
		return err
	}
	if p != nil {
		b.configStore.Store(next)
		p.Commit()
	}
	return nil
}

type stubDecider struct{}

func (stubDecider) ShouldRespondWithInfo(context.Context, string, decision.MessageInfo, int, []decision.ContextMessage) (*decision.DecisionResult, error) {
	return &decision.DecisionResult{}, nil
}

func TestReload_DecisionProviderRebuiltAndDisabled(t *testing.T) {
	cfg := exampleConfig(t)
	b := reloadTestBot(t, cfg)
	b.decider = stubDecider{}

	next := *cfg
	next.Decision.Enabled = true
	next.Decision.Provider = config.DecisionProviderSystemOne
	next.Decision.SystemOne = config.SystemOneConfig{
		APIBaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "jev", Timeout: 5, RetryCount: 0,
		Threshold: 0.5, Instructions: "Should the bot reply?", MaxStateBytes: 8000,
	}
	if err := apply(t, b, &next); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.getDecider().(*decision.SystemOneService); !ok {
		t.Fatalf("decider = %T, want the system_one provider", b.getDecider())
	}

	off := next
	off.Decision.Enabled = false
	if err := apply(t, b, &off); err != nil {
		t.Fatal(err)
	}
	if b.getDecider() != nil {
		t.Error("decider must be nil once decision is disabled")
	}
}

func TestReload_DecisionFailureKeepsOldProvider(t *testing.T) {
	cfg := exampleConfig(t)
	b := reloadTestBot(t, cfg)
	old := stubDecider{}
	b.decider = old

	next := *cfg
	next.Decision.Enabled = true
	next.Decision.Provider = "no-such-provider"
	if err := apply(t, b, &next); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
	if b.getDecider() != old {
		t.Error("a failed prepare must keep the old decider")
	}
	if b.cfg() != cfg {
		t.Error("a failed prepare must not publish the config")
	}
}

func TestReload_MCPFailureKeepsToolsAndDisableRemovesThem(t *testing.T) {
	cfg := exampleConfig(t)
	b := reloadTestBot(t, cfg)
	keep := b.getMCPManager()
	b.installMCPTools(keep, []mcp.MCPTool{{ServerName: "srv", Tool: &mcpTool}})
	if _, ok := b.toolRegistry.Get("srv_echo"); !ok {
		t.Fatal("setup: tool not registered")
	}

	bad := *cfg
	bad.MCP = config.MCPConfig{Enabled: true, Servers: []config.MCPServer{{Name: "broken", Type: "carrier-pigeon"}}}
	if err := apply(t, b, &bad); err == nil || !strings.Contains(err.Error(), "operations.mcp") {
		t.Fatalf("err = %v, want an operations.mcp error", err)
	}
	if b.getMCPManager() != keep {
		t.Error("failed MCP reload replaced the manager")
	}
	if _, ok := b.toolRegistry.Get("srv_echo"); !ok {
		t.Error("failed MCP reload dropped existing tools")
	}

	off := *cfg
	off.MCP = config.MCPConfig{Enabled: false, Servers: []config.MCPServer{{Name: "srv", Type: "http", URL: "http://x"}}}
	if err := apply(t, b, &off); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.toolRegistry.Get("srv_echo"); ok {
		t.Error("disabling MCP must unregister its tools")
	}
	if b.getMCPManager() == nil || b.getMCPManager() == keep {
		t.Error("manager not replaced")
	}
}

func TestReload_TokenRejectedKeepsSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401: Unauthorized","code":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"42","username":"bot"}`))
	}))
	defer srv.Close()
	orig := discordgo.EndpointUser
	discordgo.EndpointUser = func(uID string) string { return srv.URL + "/users/" + uID }
	defer func() { discordgo.EndpointUser = orig }()

	if u, err := ValidateToken(context.Background(), "good"); err != nil || u.ID != "42" {
		t.Fatalf("ValidateToken(good) = %v, %v", u, err)
	}

	cfg := exampleConfig(t)
	b := reloadTestBot(t, cfg)
	before := b.sess()
	next := *cfg
	next.Discord.Token = "revoked"
	err := apply(t, b, &next)
	if err == nil || !strings.Contains(err.Error(), "core.discord.token") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "revoked") {
		t.Error("the error must not contain the token")
	}
	if b.sess() != before {
		t.Error("rejected token replaced the session")
	}
}

func TestReload_EventsFromNonCurrentSessionIgnored(t *testing.T) {
	b := &Bot{}
	cur, _ := discordgo.New("Bot a")
	other, _ := discordgo.New("Bot b")
	b.session.Store(cur)
	if !b.isCurrentSession(cur) || b.isCurrentSession(other) || b.isCurrentSession(nil) {
		t.Fatal("isCurrentSession wrong")
	}
	b.swapSession(other)
	if b.sess() != other || b.isCurrentSession(cur) {
		t.Fatal("swapSession did not make the new session current")
	}
}

func TestReload_UnrelatedChangeNeedsNothing(t *testing.T) {
	cfg := exampleConfig(t)
	b := reloadTestBot(t, cfg)
	next := *cfg
	next.Web.Port = cfg.Web.Port + 1
	p, err := b.Prepare(context.Background(), cfg, &next, hotreload.Diff(cfg, &next))
	if err != nil || p != nil {
		t.Fatalf("Prepare = %v, %v; want nothing to do", p, err)
	}
}
