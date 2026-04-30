package web

import (
	"os"
	"testing"

	"ezyapper/internal/logger"
)

func TestMain(m *testing.M) {
	logger.Init(logger.Config{Level: "info", File: os.DevNull})
	os.Exit(m.Run())
}
