package tools

import (
	"os"
	"testing"

	"ezyapper/internal/logger"
)

func TestMain(m *testing.M) {
	// Initialize logger so that extractLimit's Warnf call doesn't crash tests.
	if err := logger.Init(logger.Config{Level: "error"}); err != nil {
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestExtractLimit_ExceedsMaxWarnsAndHonors(t *testing.T) {
	args := map[string]any{"limit": float64(200)}
	limit := extractLimit(args, "limit", 5, 100)
	if limit != 200 {
		t.Fatalf("expected user value 200 to be honored, got %d (warn+honor expected, not clamp)", limit)
	}
}
