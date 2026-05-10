package bot

import (
	"time"

	"ezyapper/internal/config"
)

// calculateSessionTimeout computes the global context timeout for a message
// processing session based on per-request timeout, plugin timeout, tool loop
// iterations, and a fixed buffer.
//
// Formula: (Timeout + maxPluginTimeout) × MaxToolIterations + 10s buffer
func calculateSessionTimeout(cfg *config.Config) time.Duration {
	timeout := time.Duration(cfg.AI.Timeout) * time.Second
	iterations := cfg.AI.MaxToolIterations

	var maxPluginTimeout time.Duration
	if cfg.Plugins.Enabled {
		cmdTimeout := time.Duration(cfg.Plugins.CommandTimeoutSec) * time.Second
		toolTimeout := time.Duration(cfg.Plugins.DefaultToolTimeoutMs) * time.Millisecond
		maxPluginTimeout = max(cmdTimeout, toolTimeout)
	}

	return (timeout+maxPluginTimeout)*time.Duration(iterations) + 10*time.Second
}
