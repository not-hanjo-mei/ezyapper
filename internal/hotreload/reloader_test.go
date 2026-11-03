package hotreload

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
)

// baseConfig loads the repository's example config, which is valid.
func baseConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(filepath.Join("..", "..", "examples", "config.yaml.example"))
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	return cfg
}

func clone(c *config.Config) *config.Config {
	n := *c
	return &n
}

func TestDiff_ReportsPathsInFileNotation(t *testing.T) {
	old := baseConfig(t)
	next := clone(old)
	next.Discord.Token = "another-token"
	next.AI.Model = "other-model"               // squashed LLMConfig: no "llmconfig" segment
	next.Decision.SystemOne.Threshold = 0.42    // nested struct
	next.Operations.HotReload.DebounceMs = 1234 // operations.runtime section
	next.MCP.Servers = append([]config.MCPServer{}, old.MCP.Servers...)
	next.MCP.Servers = append(next.MCP.Servers, config.MCPServer{Name: "x", Type: "http", URL: "http://x"})

	got := Diff(old, next)
	want := []string{
		"core.ai.model",
		"core.decision.system_one.threshold",
		"core.discord.token",
		"operations.mcp.servers",
		"operations.runtime.hot_reload.debounce_ms",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Diff = %v\nwant   %v", got, want)
	}
}

func TestDiff_NilAndEmptyAreEqual(t *testing.T) {
	old := baseConfig(t)
	a, b := clone(old), clone(old)
	a.Blacklist.Users = nil
	b.Blacklist.Users = []string{}
	a.AI.ExtraParams = nil
	b.AI.ExtraParams = map[string]any{}
	if d := Diff(a, b); len(d) != 0 {
		t.Fatalf("nil vs empty reported as change: %v", d)
	}
	if d := Diff(old, clone(old)); len(d) != 0 {
		t.Fatalf("identical configs reported as changed: %v", d)
	}
}

func TestChanges_Any(t *testing.T) {
	c := Changes{"core.ai.model", "operations.web.port"}
	if !c.Any("core.ai") || !c.Any("operations.web.port") {
		t.Error("prefix match failed")
	}
	if c.Any("core.a") || c.Any("core.ai.model.x") || c.Any("core.decision") {
		t.Error("matched a non-prefix")
	}
}

// recorder is a Component that records its lifecycle calls.
type recorder struct {
	name    string
	prefix  string
	failErr error
	panics  bool
	log     *[]string
	mu      *sync.Mutex
	// seen is the config visible in the store when Commit ran.
	store *atomic.Value
	seen  *config.Config
}

func (r *recorder) Name() string { return r.name }

func (r *recorder) add(s string) {
	r.mu.Lock()
	*r.log = append(*r.log, s)
	r.mu.Unlock()
}

func (r *recorder) Prepare(_ context.Context, _, _ *config.Config, ch Changes) (Pending, error) {
	if r.panics {
		panic("boom")
	}
	if !ch.Any(r.prefix) {
		return nil, nil
	}
	r.add("prepare " + r.name)
	if r.failErr != nil {
		return nil, r.failErr
	}
	return Funcs{
		CommitFunc: func() {
			r.seen, _ = r.store.Load().(*config.Config)
			r.add("commit " + r.name)
		},
		AbortFunc: func() { r.add("abort " + r.name) },
	}, nil
}

func newTestReloader(t *testing.T, comps ...*recorder) (*Reloader, *atomic.Value, *[]string) {
	t.Helper()
	store := &atomic.Value{}
	store.Store(baseConfig(t))
	var log []string
	var mu sync.Mutex
	cs := make([]Component, len(comps))
	for i, c := range comps {
		c.log, c.mu, c.store = &log, &mu, store
		cs[i] = c
	}
	return New(store, cs...), store, &log
}

func TestApply_CommitsInOrderAfterPublishing(t *testing.T) {
	a := &recorder{name: "a", prefix: "core.ai"}
	b := &recorder{name: "b", prefix: "core.ai"}
	skip := &recorder{name: "skip", prefix: "operations.web"}
	r, store, log := newTestReloader(t, a, b, skip)

	next := clone(r.Current())
	next.AI.Model = "m2"
	changes, err := r.Apply(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if !changes.Any("core.ai.model") {
		t.Errorf("changes = %v", changes)
	}
	if store.Load().(*config.Config) != next {
		t.Error("new config was not published")
	}
	want := "prepare a,prepare b,commit a,commit b"
	if got := strings.Join(*log, ","); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
	if a.seen != next || b.seen != next {
		t.Error("components must see the new config in the store when they commit")
	}
	if next.Path() == "" {
		t.Error("the file path must carry over to the new config")
	}
}

func TestApply_FailureAbortsPreparedAndKeepsOldConfig(t *testing.T) {
	a := &recorder{name: "a", prefix: "core"}
	b := &recorder{name: "b", prefix: "core", failErr: errors.New("cannot connect")}
	c := &recorder{name: "c", prefix: "core"}
	r, store, log := newTestReloader(t, a, b, c)
	old := r.Current()

	next := clone(old)
	next.Discord.Token = "secret-new-token"
	_, err := r.Apply(context.Background(), next)
	if err == nil || !strings.Contains(err.Error(), "b: cannot connect") {
		t.Fatalf("err = %v", err)
	}
	if store.Load().(*config.Config) != old {
		t.Error("old config must stay live after a failed prepare")
	}
	want := "prepare a,prepare b,abort a"
	if got := strings.Join(*log, ","); got != want {
		t.Errorf("calls = %s, want %s", got, want)
	}
}

func TestApply_PanicInPrepareIsAnError(t *testing.T) {
	r, store, _ := newTestReloader(t, &recorder{name: "p", panics: true})
	old := r.Current()
	next := clone(old)
	next.AI.Model = "x"
	if _, err := r.Apply(context.Background(), next); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("err = %v", err)
	}
	if store.Load().(*config.Config) != old {
		t.Error("old config must stay live after a panic")
	}
}

func TestApply_InvalidConfigRejected(t *testing.T) {
	r, store, log := newTestReloader(t, &recorder{name: "a", prefix: "core"})
	old := r.Current()
	next := clone(old)
	next.Discord.Token = ""
	if _, err := r.Apply(context.Background(), next); err == nil {
		t.Fatal("expected validation error")
	}
	if store.Load().(*config.Config) != old || len(*log) != 0 {
		t.Error("an invalid config must not reach any component")
	}
}

func TestApply_NoChange(t *testing.T) {
	r, _, log := newTestReloader(t, &recorder{name: "a", prefix: "core"})
	if _, err := r.Apply(context.Background(), clone(r.Current())); !errors.Is(err, ErrNoChange) {
		t.Fatalf("err = %v, want ErrNoChange", err)
	}
	if len(*log) != 0 {
		t.Error("no component should run for an unchanged config")
	}
}

func TestApply_LogsPathsNotSecretValues(t *testing.T) {
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "log.txt")
	if err := logger.Init(logger.Config{Level: "debug", File: path, MaxSize: 1, MaxBackups: 1, MaxAge: 1}); err != nil {
		t.Fatal(err)
	}
	r, _, _ := newTestReloader(t, &recorder{name: "a", prefix: "core", failErr: errors.New("nope")})
	next := clone(r.Current())
	next.Discord.Token = "SUPER-SECRET-TOKEN-VALUE"
	next.Qdrant.APIKey = "SUPER-SECRET-QDRANT-KEY"
	_, _ = r.Apply(context.Background(), next)
	_ = logger.L().Sync()
	data, _ := os.ReadFile(path)
	buf.Write(data)
	out := buf.String()
	if !strings.Contains(out, "core.discord.token") || !strings.Contains(out, "memory_pipeline.qdrant.api_key") {
		t.Errorf("changed paths missing from log: %s", out)
	}
	if strings.Contains(out, "SUPER-SECRET") {
		t.Errorf("secret value leaked into the log: %s", out)
	}
}

func TestReloadFile_KeepsConfigWhenLoadFails(t *testing.T) {
	r, store, _ := newTestReloader(t)
	old := r.Current()
	r.load = func(string) (*config.Config, error) { return nil, errors.New("yaml: line 3: bad indent") }
	if err := r.ReloadFile(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if store.Load().(*config.Config) != old {
		t.Error("config replaced after a failed load")
	}
}

func TestWatcher_ReloadsOnSave(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	comp := &recorder{name: "ai", prefix: "core.ai"}
	r, store, log := newTestReloader(t, comp)
	cur := clone(r.Current())
	cur.SetPath(file)
	cur.Operations.HotReload = config.HotReloadConfig{Enabled: true, DebounceMs: 50}
	store.Store(cur)

	var loads atomic.Int32
	r.load = func(p string) (*config.Config, error) {
		loads.Add(1)
		n := clone(cur)
		n.AI.Model = "reloaded"
		return n, nil
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	// Simulate an editor's atomic save: write a temp file and rename it.
	tmp := filepath.Join(dir, ".config.yaml.swp")
	if err := os.WriteFile(tmp, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, file); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c := store.Load().(*config.Config); c.AI.Model == "reloaded" {
			if !strings.Contains(strings.Join(*log, ","), "commit ai") {
				t.Errorf("component not committed: %v", *log)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("config was not reloaded after save (loads=%d)", loads.Load())
}

func TestWatcherComponent_DisablesWatching(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &atomic.Value{}
	cur := baseConfig(t)
	cur.SetPath(file)
	cur.Operations.HotReload = config.HotReloadConfig{Enabled: true, DebounceMs: 50}
	store.Store(cur)
	r := New(store)
	r.comps = []Component{r.WatcherComponent()}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	next := clone(cur)
	next.Operations.HotReload.Enabled = false
	if _, err := r.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.watchMu.Lock()
		w := r.watcher
		r.watchMu.Unlock()
		if w == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watcher still running after hot_reload.enabled was switched off")
}

// A dashboard save writes the file, which the watcher then reloads. The
// written file must load back to the same config, otherwise every save
// triggers a second reload (or, as before Save was fixed, a failed one).
func TestSaveLoadRoundTripHasNoDiff(t *testing.T) {
	cfg := baseConfig(t)
	cfg.Decision.Provider = config.DecisionProviderSystemOne
	cfg.Decision.SystemOne = config.SystemOneConfig{APIBaseURL: "http://jev", APIKey: "k", Model: "m", Timeout: 5, RetryCount: 1, Threshold: 0.5, Instructions: "q", MaxStateBytes: 1000}
	cfg.MCP.Servers = []config.MCPServer{
		{Name: "s", Type: "http", URL: "http://x", Headers: map[string]string{"Authorization": "Bearer t", "X-Api-Key": "k"}},
		{Name: "e", Type: "stdio", Command: "/bin/true", Env: map[string]string{"GITHUB_TOKEN": "t", "MTest_Secret_Word": "w"}},
	}
	cfg.AI.ExtraParams = map[string]any{"metadata": map[string]any{"TraceID": "abc"}}
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg.SetPath(path)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := config.Load(path)
	if err != nil {
		data, _ := os.ReadFile(path)
		t.Fatalf("saved file does not load: %v\n%s", err, data)
	}
	if d := Diff(cfg, back); len(d) != 0 {
		t.Fatalf("save/load round trip changed: %v", d)
	}
}
