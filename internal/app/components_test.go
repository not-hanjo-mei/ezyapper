package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/hotreload"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
)

func exampleCfg(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Load(filepath.Join("..", "..", "examples", "config.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func prep(t *testing.T, c hotreload.Component, old, next *config.Config) (hotreload.Pending, error) {
	t.Helper()
	return c.Prepare(context.Background(), old, next, hotreload.Diff(old, next))
}

type countingService struct {
	memory.NoopService
	closed   atomic.Bool
	swapped  atomic.Bool
	maintain atomic.Bool
}

func (s *countingService) Close() error                           { s.closed.Store(true); return nil }
func (s *countingService) SwapEmbedder(e memory.Embedder)         { s.swapped.Store(true); e.Stop() }
func (s *countingService) StartMaintenance(context.Context) error { s.maintain.Store(true); return nil }

func memoryOn(c *config.Config) *config.Config {
	n := *c
	n.Memory.Retrieval.TopK = 5
	n.Embedding.Model = "text-embedding-3-small"
	return &n
}

func TestMemoryComponent_Decisions(t *testing.T) {
	base := memoryOn(exampleCfg(t))
	cur := &countingService{}
	proxy := memory.NewSwappable(cur)
	builds := 0
	var built *countingService
	mc := NewMemoryComponent(proxy, nil)
	mc.build = func(*config.Config) (memory.Service, error) {
		builds++
		built = &countingService{}
		return built, nil
	}

	// Chat prompt only: nothing for memory.
	n := *base
	n.AI.SystemPrompt = base.AI.SystemPrompt + " (edited)"
	if p, err := prep(t, mc, base, &n); p != nil || err != nil {
		t.Fatalf("prompt edit reached memory: %v %v", p, err)
	}

	// Embedding key only: embedder swap, no rebuild.
	n = *base
	n.Embedding.APIKey = "other-key"
	p, err := prep(t, mc, base, &n)
	if err != nil || p == nil {
		t.Fatalf("Prepare = %v, %v", p, err)
	}
	p.Commit()
	if builds != 0 || !cur.swapped.Load() {
		t.Fatalf("key change: builds=%d swapped=%v, want swap without rebuild", builds, cur.swapped.Load())
	}

	// Model change: full rebuild through the proxy.
	n = *base
	n.Embedding.Model = "text-embedding-3-large"
	n.Qdrant.VectorSize = 3072
	p, err = prep(t, mc, base, &n)
	if err != nil || p == nil {
		t.Fatalf("Prepare = %v, %v", p, err)
	}
	p.Commit()
	if builds != 1 || proxy.Current() != built {
		t.Fatal("model change must rebuild and install the new service")
	}

	// Build failure: nothing replaced.
	mc.build = func(*config.Config) (memory.Service, error) { return nil, errors.New("qdrant down") }
	n = *base
	n.Qdrant.Host = "elsewhere"
	if _, err := prep(t, mc, base, &n); err == nil {
		t.Fatal("expected the build error")
	}
	if proxy.Current() != built {
		t.Error("a failed build replaced the service")
	}
}

func TestLoggingComponent(t *testing.T) {
	dir := t.TempDir()
	base := exampleCfg(t)
	base.Logging.File = filepath.Join(dir, "a.log")
	if err := logger.Init(LoggerConfig(base.Logging)); err != nil {
		t.Fatal(err)
	}

	bad := *base
	bad.Logging.Level = "loud"
	if _, err := prep(t, LoggingComponent{}, base, &bad); err == nil {
		t.Fatal("invalid level must reject the reload")
	}

	unwritable := *base
	unwritable.Logging.File = filepath.Join(dir, "missing-dir", "x", "b.log")
	_ = os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	if p, err := prep(t, LoggingComponent{}, base, &unwritable); err == nil {
		// Some environments (root) can still create it; then it must commit.
		p.Abort()
	}
	_ = os.Chmod(dir, 0o700)

	moved := *base
	moved.Logging.File = filepath.Join(dir, "b.log")
	p, err := prep(t, LoggingComponent{}, base, &moved)
	if err != nil {
		t.Fatal(err)
	}
	p.Commit()
	logger.Info("written after reload")
	_ = logger.L().Sync()
	data, _ := os.ReadFile(moved.Logging.File)
	if len(data) == 0 {
		t.Error("new log file not used after commit")
	}
	time.Sleep(10 * time.Millisecond)
}

func TestPluginsComponent(t *testing.T) {
	base := exampleCfg(t)
	pm := plugin.NewManager(1000, 1, 1, 1, 1, 1, 1, 1, 1)

	n := *base
	n.Plugins.RPCTimeoutSec = base.Plugins.RPCTimeoutSec + 7
	p, err := prep(t, PluginsComponent{Manager: pm}, base, &n)
	if err != nil || p == nil {
		t.Fatalf("Prepare = %v, %v", p, err)
	}
	p.Commit()
	if pm.Settings().RPCTimeoutSec != n.Plugins.RPCTimeoutSec {
		t.Error("settings not applied")
	}

	missing := *base
	missing.Plugins.Enabled = true
	missing.Plugins.PluginsDir = filepath.Join(t.TempDir(), "nope")
	if _, err := prep(t, PluginsComponent{Manager: pm}, base, &missing); err == nil {
		t.Error("a missing plugins_dir must reject the reload")
	}
}
