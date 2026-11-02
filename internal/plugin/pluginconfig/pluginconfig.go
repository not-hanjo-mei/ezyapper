// Package pluginconfig lets a plugin reload its own config file while it
// runs, without the host or the other plugins being involved.
//
// A plugin keeps its config in a Value and calls Watch with a reload
// function. When the file is saved (debounced, editor rename-saves
// included) the reload function runs: it should read and validate the
// file, build anything derived from it, and only then Store the new config
// (and swap the derived state). If it returns an error the plugin keeps
// running on the previous config and the error is written to stderr, which
// the host forwards to its log. If the reload changed the plugin's tools or
// its system prompt section, call server.NotifyToolsChanged so the host
// re-reads them.
package pluginconfig

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"ezyapper/internal/filewatch"
)

// Value holds the live config of type T. The zero value is empty; Load on
// an empty Value returns T's zero value.
type Value[T any] struct {
	p atomic.Pointer[T]
}

// NewValue returns a Value holding v.
func NewValue[T any](v T) *Value[T] {
	val := &Value[T]{}
	val.Store(v)
	return val
}

// Load returns the current config.
func (v *Value[T]) Load() T {
	if p := v.p.Load(); p != nil {
		return *p
	}
	var zero T
	return zero
}

// Store replaces the current config.
func (v *Value[T]) Store(c T) { v.p.Store(&c) }

// Path returns the plugin's config file: EZYAPPER_PLUGIN_CONFIG if set,
// else config.yaml in EZYAPPER_PLUGIN_PATH, else ./config.yaml.
func Path() string {
	if cfg := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG")); cfg != "" {
		return cfg
	}
	if dir := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_PATH")); dir != "" {
		return filepath.Join(dir, "config.yaml")
	}
	return "config.yaml"
}

// DebounceEnv overrides the debounce delay in milliseconds.
const DebounceEnv = "EZYAPPER_PLUGIN_CONFIG_DEBOUNCE_MS"

// DisableEnv turns watching off when set to "0", "false" or "off".
const DisableEnv = "EZYAPPER_PLUGIN_CONFIG_WATCH"

// Options tune Watch. The zero value is usable.
type Options struct {
	// Name prefixes log lines, e.g. "DATETIME".
	Name string
	// Debounce overrides filewatch.DefaultDebounce (and DebounceEnv).
	Debounce time.Duration
	// Log receives log lines; nil writes to stderr.
	Log io.Writer
}

// Watcher is a running config watch.
type Watcher struct{ w *filewatch.Watcher }

// Close stops watching. Safe on a nil Watcher.
func (w *Watcher) Close() error {
	if w == nil || w.w == nil {
		return nil
	}
	return w.w.Close()
}

// Watch calls reload after path changes. It returns a nil Watcher (and no
// error) when watching is disabled through DisableEnv.
func Watch(path string, opts Options, reload func() error) (*Watcher, error) {
	if reload == nil {
		return nil, fmt.Errorf("pluginconfig: reload is required")
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DisableEnv))) {
	case "0", "false", "off", "no":
		return nil, nil
	}
	out := opts.Log
	if out == nil {
		out = os.Stderr
	}
	prefix := "[plugin]"
	if opts.Name != "" {
		prefix = "[" + opts.Name + "]"
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(out, prefix+" "+format+"\n", args...)
	}

	delay := opts.Debounce
	if delay <= 0 {
		if ms, err := strconv.Atoi(strings.TrimSpace(os.Getenv(DebounceEnv))); err == nil && ms > 0 {
			delay = time.Duration(ms) * time.Millisecond
		}
	}
	w, err := filewatch.Watch(path, filewatch.Options{
		Debounce: delay,
		OnError:  func(err error) { logf("config watcher error: %v", err) },
	}, func() {
		if err := safeReload(reload); err != nil {
			logf("config reload rejected, keeping the previous configuration: %v", err)
			return
		}
		logf("config reloaded from %s", path)
	})
	if err != nil {
		return nil, err
	}
	return &Watcher{w: w}, nil
}

func safeReload(reload func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return reload()
}
