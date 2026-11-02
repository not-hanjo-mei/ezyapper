package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeedsRebuild(t *testing.T) {
	base := Config{Level: "info", File: "a.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1}
	lvl := base
	lvl.Level = "debug"
	if NeedsRebuild(base, lvl) {
		t.Error("level-only change must not rebuild the logger")
	}
	for _, mut := range []func(*Config){
		func(c *Config) { c.File = "b.log" },
		func(c *Config) { c.MaxSize = 2 },
		func(c *Config) { c.MaxBackups = 2 },
		func(c *Config) { c.MaxAge = 2 },
	} {
		c := base
		mut(&c)
		if !NeedsRebuild(base, c) {
			t.Errorf("expected rebuild for %+v", c)
		}
	}
}

func TestReconfigure_LevelInPlace(t *testing.T) {
	prev := globalLogger.Load()
	defer globalLogger.Store(prev)

	if err := Init(Config{Level: "info"}); err != nil {
		t.Fatal(err)
	}
	before := L()
	if err := Reconfigure(Config{Level: "debug"}); err != nil {
		t.Fatal(err)
	}
	if L() != before {
		t.Error("level-only change replaced the logger instead of updating it")
	}
	if L().Level() != "debug" {
		t.Errorf("level = %s, want debug", L().Level())
	}
}

func TestReconfigure_InvalidLevelKeepsLogger(t *testing.T) {
	prev := globalLogger.Load()
	defer globalLogger.Store(prev)

	if err := Init(Config{Level: "info"}); err != nil {
		t.Fatal(err)
	}
	before := L()
	if err := Reconfigure(Config{Level: "loud"}); err == nil {
		t.Fatal("expected an error for an invalid level")
	}
	if L() != before || L().Level() != "info" {
		t.Error("invalid level changed the logger")
	}
}

func TestReconfigure_NewFileSwapsLogger(t *testing.T) {
	prev := globalLogger.Load()
	defer globalLogger.Store(prev)

	dir := t.TempDir()
	first := filepath.Join(dir, "first.log")
	second := filepath.Join(dir, "second.log")
	if err := Init(Config{Level: "info", File: first, MaxSize: 1, MaxBackups: 1, MaxAge: 1}); err != nil {
		t.Fatal(err)
	}
	before := L()
	if err := Reconfigure(Config{Level: "info", File: second, MaxSize: 1, MaxBackups: 1, MaxAge: 1}); err != nil {
		t.Fatal(err)
	}
	if L() == before {
		t.Fatal("file change did not swap the logger")
	}
	Info("after-swap-marker")
	_ = L().Sync()
	data, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "after-swap-marker") {
		t.Errorf("new log file does not contain the line logged after the swap: %q", data)
	}
	_ = L().Close()
}
