package main

import (
	"fmt"
	"os"
)

// validateConfig checks the resolved Config for required fields and applies
// runtime defaults/safeguards. Returns error only for fatal misconfiguration.
// Non-fatal issues (missing API key, out-of-range values) are fixed silently
// with a warning to stderr.
func validateConfig(cfg *Config) error {
	if cfg.DataDir == "" {
		return fmt.Errorf("storage.data_dir is required")
	}

	if cfg.MaxImageSizeKb <= 0 {
		fmt.Fprintf(os.Stderr, "[EMOTE] Warning: max_image_size_kb is %d, defaulting to 512\n", cfg.MaxImageSizeKb)
		cfg.MaxImageSizeKb = 512
	}

	if len(cfg.AllowedFormats) == 0 {
		return fmt.Errorf("storage.allowed_formats must have at least one format")
	}

	if cfg.AutoStealEnabled {
		if cfg.VisionApiKey == "" {
			fmt.Fprintf(os.Stderr, "[EMOTE] Warning: auto_steal enabled but vision.api_key is empty, disabling auto-steal\n")
			cfg.AutoStealEnabled = false
		}
		if cfg.RateLimitPerMinute < 0 {
			fmt.Fprintf(os.Stderr, "[EMOTE] Warning: rate_limit_per_minute is %d, defaulting to 5\n", cfg.RateLimitPerMinute)
			cfg.RateLimitPerMinute = 5
		}
		if cfg.CooldownSeconds < 0 {
			fmt.Fprintf(os.Stderr, "[EMOTE] Warning: cooldown_seconds is %d, defaulting to 2\n", cfg.CooldownSeconds)
			cfg.CooldownSeconds = 2
		}
	}

	return nil
}
