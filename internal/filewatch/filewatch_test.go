package filewatch

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestDebouncer_CoalescesBurst(t *testing.T) {
	var calls atomic.Int32
	d := NewDebouncer(40*time.Millisecond, func() { calls.Add(1) })
	defer d.Stop()

	for i := 0; i < 10; i++ {
		d.Trigger()
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call after a burst, got %d", got)
	}

	d.Trigger()
	time.Sleep(150 * time.Millisecond)
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected a second call after a later trigger, got %d", got)
	}
}

func TestDebouncer_WaitsForQuietPeriod(t *testing.T) {
	var calls atomic.Int32
	d := NewDebouncer(80*time.Millisecond, func() { calls.Add(1) })
	defer d.Stop()

	d.Trigger()
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("fired before the debounce delay elapsed")
	}
	d.Trigger() // restarts the timer
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("a new trigger did not restart the timer")
	}
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 call, got %d", calls.Load())
	}
}

func TestDebouncer_NeverConcurrent(t *testing.T) {
	var running, maxRunning, calls atomic.Int32
	d := NewDebouncer(5*time.Millisecond, func() {
		n := running.Add(1)
		if n > maxRunning.Load() {
			maxRunning.Store(n)
		}
		time.Sleep(30 * time.Millisecond)
		running.Add(-1)
		calls.Add(1)
	})
	defer d.Stop()

	d.Trigger()
	time.Sleep(15 * time.Millisecond) // first call is running now
	d.Trigger()
	time.Sleep(15 * time.Millisecond)
	d.Trigger()
	time.Sleep(200 * time.Millisecond)
	if maxRunning.Load() > 1 {
		t.Fatalf("callback ran concurrently (%d at once)", maxRunning.Load())
	}
	if calls.Load() < 2 {
		t.Fatalf("trigger during a run was lost: %d calls", calls.Load())
	}
}

func TestDebouncer_StopDropsPending(t *testing.T) {
	var calls atomic.Int32
	d := NewDebouncer(30*time.Millisecond, func() { calls.Add(1) })
	d.Trigger()
	d.Stop()
	d.Trigger()
	time.Sleep(80 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("expected no calls after Stop, got %d", calls.Load())
	}
}

func TestRelevant(t *testing.T) {
	target := filepath.Clean("/etc/app/config.yaml")
	cases := []struct {
		ev   fsnotify.Event
		want bool
	}{
		{fsnotify.Event{Name: target, Op: fsnotify.Write}, true},
		{fsnotify.Event{Name: target, Op: fsnotify.Create}, true},
		{fsnotify.Event{Name: target, Op: fsnotify.Rename}, true},
		{fsnotify.Event{Name: target, Op: fsnotify.Remove}, true},
		{fsnotify.Event{Name: target, Op: fsnotify.Chmod}, false},
		{fsnotify.Event{Name: "/etc/app/config.yaml.swp", Op: fsnotify.Write}, false},
		{fsnotify.Event{Name: "/etc/app/other.yaml", Op: fsnotify.Create}, false},
	}
	for _, c := range cases {
		if got := Relevant(c.ev, target); got != c.want {
			t.Errorf("Relevant(%v) = %v, want %v", c.ev, got, c.want)
		}
	}
}

// waitCalls waits until counter reaches want or the deadline passes.
func waitCalls(t *testing.T, counter *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if counter.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected at least %d change notifications, got %d", want, counter.Load())
}

func TestWatch_InPlaceWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	w, err := Watch(path, Options{Debounce: 50 * time.Millisecond}, func() { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.WriteFile(path, []byte("a: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, &calls, 1)
}

func TestWatch_RenameReplaceKeepsWatching(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	w, err := Watch(path, Options{Debounce: 50 * time.Millisecond}, func() { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Two editor-style saves: write a temp file and rename it over the
	// original. A watch on the file inode would miss the second one.
	for i, content := range []string{"a: 2\n", "a: 3\n"} {
		tmp := filepath.Join(dir, ".config.yaml.tmp")
		if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
		waitCalls(t, &calls, int32(i+1))
		time.Sleep(120 * time.Millisecond)
	}
}

func TestWatch_IgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	w, err := Watch(path, Options{Debounce: 30 * time.Millisecond}, func() { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.WriteFile(filepath.Join(dir, "other.yaml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("unrelated file triggered %d reloads", calls.Load())
	}
}

func TestWatch_CloseIsIdempotentAndStops(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	w, err := Watch(path, Options{Debounce: 20 * time.Millisecond}, func() { mu.Lock(); calls++; mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	_ = os.WriteFile(path, []byte("a: 2\n"), 0o600)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("closed watcher still reported %d changes", calls)
	}
}
