package pluginconfig

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type cfg struct{ N int }

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func TestValue(t *testing.T) {
	var v Value[cfg]
	if v.Load().N != 0 {
		t.Fatal("zero Value must load the zero config")
	}
	v.Store(cfg{N: 2})
	if NewValue(cfg{N: 3}).Load().N != 3 || v.Load().N != 2 {
		t.Fatal("Load/Store broken")
	}
}

func TestWatch_ReloadsAndKeepsOldOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("n: 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	val := NewValue(cfg{N: 1})
	load := func() error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := strings.TrimSpace(string(data))
		if s == "n: 2" {
			val.Store(cfg{N: 2})
			return nil
		}
		return errors.New("invalid value")
	}
	var log syncBuf
	w, err := Watch(path, Options{Name: "TEST", Debounce: 30 * time.Millisecond, Log: &log}, load)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Editor-style save: write a temp file and rename it over the config.
	tmp := filepath.Join(dir, "config.yaml.tmp")
	_ = os.WriteFile(tmp, []byte("n: 2"), 0o600)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	eventually(t, "reload", func() bool { return val.Load().N == 2 })
	eventually(t, "success log", func() bool { return strings.Contains(log.String(), "[TEST] config reloaded") })

	if err := os.WriteFile(path, []byte("n: broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "rejection log", func() bool {
		return strings.Contains(log.String(), "keeping the previous configuration: invalid value")
	})
	if val.Load().N != 2 {
		t.Error("a rejected reload changed the config")
	}
}

func TestWatch_Disabled(t *testing.T) {
	t.Setenv(DisableEnv, "off")
	w, err := Watch(filepath.Join(t.TempDir(), "c.yaml"), Options{}, func() error { return nil })
	if err != nil || w != nil {
		t.Fatalf("Watch = %v, %v; want disabled", w, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPath(t *testing.T) {
	t.Setenv("EZYAPPER_PLUGIN_CONFIG", "")
	t.Setenv("EZYAPPER_PLUGIN_PATH", "/plugins/x")
	if Path() != filepath.Join("/plugins/x", "config.yaml") {
		t.Fatal(Path())
	}
	t.Setenv("EZYAPPER_PLUGIN_CONFIG", "/etc/x.yaml")
	if Path() != "/etc/x.yaml" {
		t.Fatal(Path())
	}
}
