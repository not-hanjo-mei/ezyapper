package bot

import (
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
)

// calculateSessionTimeout computes the global context timeout for a message
// processing session based on per-request timeout, plugin timeout, tool loop
// iterations, the closing requests, and a fixed buffer.
//
// Formula: (Timeout + maxPluginTimeout) × MaxToolIterations
// + Timeout × ai.MaxClosingRequests (2) + 10s buffer
//
// The closing requests (sent when the model still calls tools after the
// last round, see ai.ToolBudgetToolName) execute no tool, so each adds one
// request timeout and no plugin timeout.
func calculateSessionTimeout(cfg *config.Config) time.Duration {
	timeout := time.Duration(cfg.AI.Timeout) * time.Second
	iterations := cfg.AI.MaxToolIterations

	var maxPluginTimeout time.Duration
	if cfg.Plugins.Enabled {
		cmdTimeout := time.Duration(cfg.Plugins.CommandTimeoutSec) * time.Second
		toolTimeout := time.Duration(cfg.Plugins.DefaultToolTimeoutMs) * time.Millisecond
		maxPluginTimeout = max(cmdTimeout, toolTimeout)
	}

	return (timeout+maxPluginTimeout)*time.Duration(iterations) + timeout*ai.MaxClosingRequests + 10*time.Second
}
