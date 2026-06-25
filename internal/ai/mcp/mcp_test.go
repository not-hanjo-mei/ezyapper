package mcp

import (
	"os"
	"strings"
	"testing"

	"ezyapper/internal/logger"
)

func TestMain(m *testing.M) {
	logger.Init(logger.Config{Level: "debug"})
	os.Exit(m.Run())
}

func TestCallTool_ServerNotConnected(t *testing.T) {
	mgr := NewMCPManager(nil)
	_, err := mgr.CallTool(t.Context(), "nonexistent", "some_tool", nil)
	if err == nil {
		t.Fatal("expected error for unconnected server")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected 'not connected' error, got: %v", err)
	}
}

func TestGetAllTools_NoSessions(t *testing.T) {
	mgr := NewMCPManager(nil)
	tools, err := mgr.GetAllTools(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestClose_NoSessions(t *testing.T) {
	mgr := NewMCPManager(nil)
	if err := mgr.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
