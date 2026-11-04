// Package filewatch watches a single file for changes and reports them after
// a debounce delay. It is shared by the bot's config hot reload and by the
// plugin config helper (internal/plugin/pluginconfig).
//
// The parent directory is watched, not the file itself: most editors (vim,
// VS Code, JetBrains, sed -i, Kubernetes ConfigMap updates) save by writing a
// temporary file and renaming it over the original. A watch on the file's
// inode would silently die after the first such save; a watch on the
// directory keeps seeing Create/Rename events for the file name.
package filewatch

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce is the debounce delay used when none is given.
const DefaultDebounce = 500 * time.Millisecond

// Watcher reports changes to one file.
type Watcher struct {
	path      string
	fsw       *fsnotify.Watcher
	debouncer *Debouncer
	onError   func(error)

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// Options tune a Watcher. The zero value is usable.
type Options struct {
	// Debounce is the quiet period after the last event before onChange runs.
	// Zero means DefaultDebounce.
	Debounce time.Duration
	// OnError receives watcher errors (e.g. inotify queue overflow). Nil
	// discards them.
	OnError func(error)
}

// Watch starts watching path and calls onChange (debounced, never
// concurrently) whenever the file is written, created, replaced by a rename,
// or removed. onChange should re-read the file and tolerate it being missing
// or half-written: the debounce makes that rare but cannot rule it out.
func Watch(path string, opts Options, onChange func()) (*Watcher, error) {
	if onChange == nil {
		return nil, errors.New("filewatch: onChange is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("filewatch: resolve %s: %w", path, err)
	}
	abs = filepath.Clean(abs)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("filewatch: create watcher: %w", err)
	}
	if err := fsw.Add(filepath.Dir(abs)); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("filewatch: watch directory of %s: %w", abs, err)
	}

	delay := opts.Debounce
	if delay <= 0 {
		delay = DefaultDebounce
	}
	w := &Watcher{
		path:      abs,
		fsw:       fsw,
		debouncer: NewDebouncer(delay, onChange),
		onError:   opts.OnError,
		done:      make(chan struct{}),
	}
	w.wg.Add(1)
	go w.loop()
	return w, nil
}

// Path returns the absolute path being watched.
func (w *Watcher) Path() string { return w.path }

// SetDebounce changes the debounce delay for later events.
func (w *Watcher) SetDebounce(d time.Duration) {
	if d <= 0 {
		d = DefaultDebounce
	}
	w.debouncer.SetDelay(d)
}

func (w *Watcher) loop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if Relevant(ev, w.path) {
				w.debouncer.Trigger()
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			if w.onError != nil && err != nil {
				w.onError(err)
			}
		}
	}
}

// Relevant reports whether ev concerns target. Chmod-only events are ignored
// (touching permissions does not change content, and some tools emit them
// constantly); every other operation on the target name counts, including a
// Rename *away* from it, since the replacement usually follows immediately.
func Relevant(ev fsnotify.Event, target string) bool {
	if filepath.Clean(ev.Name) != target {
		return false
	}
	return ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0
}

// Close stops the watcher. A debounced call that has not started yet is
// dropped; one already running finishes.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)
		w.debouncer.Stop()
		err = w.fsw.Close()
		w.wg.Wait()
	})
	return err
}
