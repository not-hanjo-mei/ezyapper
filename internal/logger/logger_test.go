package logger

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNew(t *testing.T) {
	cfg := Config{
		Level: "info",
	}

	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if logger == nil {
		t.Error("Expected non-nil logger")
	}

	if logger.SugaredLogger == nil {
		t.Error("Expected non-nil SugaredLogger")
	}
}

func TestNew_WithFile(t *testing.T) {
	if os.Getenv("SKIP_FILE_TEST") != "" || runtime.GOOS == "windows" {
		t.Skip("Skipping file test on Windows due to file lock issues")
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	cfg := Config{
		Level:      "info",
		File:       logFile,
		MaxSize:    1,
		MaxBackups: 1,
		MaxAge:     1,
	}

	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer logger.Sync()

	if logger == nil {
		t.Error("Expected non-nil logger")
	}

	logger.Info("test message")

	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Error("Log directory was not created")
	}
}

func TestNew_InvalidLevel(t *testing.T) {
	cfg := Config{
		Level: "invalid_level",
	}

	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if logger == nil {
		t.Error("Expected non-nil logger")
	}
}

func TestLogger_SetLevel(t *testing.T) {
	cfg := Config{
		Level: "info",
	}

	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	err = logger.SetLevel("debug")
	if err != nil {
		t.Errorf("SetLevel failed: %v", err)
	}

	if logger.GetLevel() != "debug" {
		t.Errorf("Expected level='debug', got '%s'", logger.GetLevel())
	}
}

func TestLogger_SetLevel_Invalid(t *testing.T) {
	cfg := Config{
		Level: "info",
	}

	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	err = logger.SetLevel("invalid")
	if err == nil {
		t.Error("Expected error for invalid level")
	}
}

func TestLogger_With(t *testing.T) {
	cfg := Config{
		Level: "info",
	}

	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	child := logger.With("key", "value")
	if child == nil {
		t.Error("Expected non-nil child logger")
	}
}

func TestLogger_Named(t *testing.T) {
	cfg := Config{
		Level: "info",
	}

	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	named := logger.Named("test-component")
	if named == nil {
		t.Error("Expected non-nil named logger")
	}
}

func TestInit(t *testing.T) {
	globalLogger = nil

	cfg := Config{
		Level: "info",
	}

	err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if globalLogger == nil {
		t.Error("Expected globalLogger to be initialized")
	}
}

func TestL(t *testing.T) {
	globalLogger = nil

	cfg := Config{Level: "info"}
	err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	logger := L()
	if logger == nil {
		t.Error("Expected non-nil logger from L() after Init")
	}
}

func TestGlobalFunctions(t *testing.T) {
	cfg := Config{
		Level: "debug",
	}
	err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Debug("debug message")
	Debugf("debug %s", "formatted")
	Info("info message")
	Infof("info %s", "formatted")
	Warn("warn message")
	Warnf("warn %s", "formatted")
	Error("error message")
	Errorf("error %s", "formatted")
}

func TestWith(t *testing.T) {
	cfg := Config{
		Level: "info",
	}
	err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	logger := With("request_id", "12345")
	if logger == nil {
		t.Error("Expected non-nil logger from With()")
	}
}

func TestNamed(t *testing.T) {
	cfg := Config{
		Level: "info",
	}
	err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	logger := Named("test-component")
	if logger == nil {
		t.Error("Expected non-nil logger from Named()")
	}
}
