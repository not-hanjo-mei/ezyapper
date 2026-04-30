package logger

import (
	"testing"
)

func TestNew_InvalidLevel_ReturnsError(t *testing.T) {
	cfg := Config{
		Level: "invalid_level",
	}

	logger, err := New(cfg)
	if err == nil {
		t.Error("Expected error for invalid log level, got nil")
	}
	if logger != nil {
		t.Error("Expected nil logger when level is invalid")
	}
}

func TestNew_ValidLevel_NoError(t *testing.T) {
	cfg := Config{
		Level: "debug",
	}

	logger, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed with valid level 'debug': %v", err)
	}
	if logger == nil {
		t.Error("Expected non-nil logger for valid level")
	}
	if logger.SugaredLogger == nil {
		t.Error("Expected non-nil SugaredLogger")
	}
}
