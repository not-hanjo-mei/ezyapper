package web

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/hotreload"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func loginStatus(port int) int {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/login")
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// applyWeb runs the server's two phases the way the reloader does.
func applyWeb(s *Server, store *atomic.Value, next *config.Config) error {
	old := store.Load().(*config.Config)
	p, err := s.Prepare(context.Background(), old, next, hotreload.Diff(old, next))
	if err != nil {
		return err
	}
	if p != nil {
		store.Store(next)
		p.Commit()
	}
	return nil
}

func TestServer_HotReloadPortCredentialsAndDisable(t *testing.T) {
	cfg, err := config.Load(filepath.Join("..", "..", "examples", "config.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	p1, p2 := freePort(t), freePort(t)
	cfg.Web.Enabled = true
	cfg.Web.Port = p1
	cfg.Web.Username, cfg.Web.Password = "admin", "password"
	cfg.Web.SessionTTLMin, cfg.Web.SessionCleanupIntervalMin = 30, 5
	store := &atomic.Value{}
	store.Store(cfg)
	s := NewServer(store, nil, nil, nil, nil, nil, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background())
	waitFor(t, "first listener", func() bool { return loginStatus(p1) == http.StatusOK })

	// Port change: new port serves, old one is retired.
	moved := *cfg
	moved.Web.Port = p2
	if err := applyWeb(s, store, &moved); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "new port", func() bool { return loginStatus(p2) == http.StatusOK })
	waitFor(t, "old port closed", func() bool { return loginStatus(p1) == 0 })

	// A port that is in use rejects the reload; the running listener stays.
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	clash := moved
	clash.Web.Port = busy.Addr().(*net.TCPAddr).Port
	if err := applyWeb(s, store, &clash); err == nil {
		t.Fatal("expected an error for a port in use")
	}
	if loginStatus(p2) != http.StatusOK {
		t.Fatal("failed reload took the dashboard down")
	}

	// Credential change keeps the port but swaps the session store.
	before := s.currentSessionStore()
	creds := moved
	creds.Web.Password = "another-password"
	if err := applyWeb(s, store, &creds); err != nil {
		t.Fatal(err)
	}
	if s.currentSessionStore() == before {
		t.Error("credential change must start a new session store")
	}
	if loginStatus(p2) != http.StatusOK {
		t.Error("same-port reload must keep serving")
	}

	// Disable.
	off := creds
	off.Web.Enabled = false
	if err := applyWeb(s, store, &off); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "listener closed", func() bool { return loginStatus(p2) == 0 })
}

func TestNewSessionStore_ZeroIntervalDoesNotPanic(t *testing.T) {
	s := NewSessionStore(0, 0)
	s.Stop()
	time.Sleep(10 * time.Millisecond)
}
