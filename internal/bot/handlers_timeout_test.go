package bot

import (
	"testing"
	"time"

	"ezyapper/internal/config"
)

func TestCalculateSessionTimeout(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want time.Duration
	}{
		{
			name: "plugins enabled, CommandTimeoutSec dominates",
			cfg: &config.Config{
				AI: config.AIConfig{
					LLMConfig: config.LLMConfig{
						Timeout: 30,
					},
					MaxToolIterations: 5,
				},
				Plugins: config.PluginsConfig{
					Enabled:              true,
					CommandTimeoutSec:    45,
					DefaultToolTimeoutMs: 0,
				},
			},
			// (30 + 45) * 5 + 30 * 2 + 10 = 445s
			want: 445 * time.Second,
		},
		{
			name: "plugins disabled",
			cfg: &config.Config{
				AI: config.AIConfig{
					LLMConfig: config.LLMConfig{
						Timeout: 30,
					},
					MaxToolIterations: 5,
				},
				Plugins: config.PluginsConfig{
					Enabled: false,
				},
			},
			// 30 * 5 + 30 * 2 + 10 = 220s
			want: 220 * time.Second,
		},
		{
			name: "zero iterations: closing requests and buffer only",
			cfg: &config.Config{
				AI: config.AIConfig{
					LLMConfig: config.LLMConfig{
						Timeout: 30,
					},
					MaxToolIterations: 0,
				},
				Plugins: config.PluginsConfig{
					Enabled: false,
				},
			},
			want: 70 * time.Second,
		},
		{
			name: "DefaultToolTimeoutMs dominates over CommandTimeoutSec",
			cfg: &config.Config{
				AI: config.AIConfig{
					LLMConfig: config.LLMConfig{
						Timeout: 30,
					},
					MaxToolIterations: 5,
				},
				Plugins: config.PluginsConfig{
					Enabled:              true,
					CommandTimeoutSec:    10,
					DefaultToolTimeoutMs: 60000,
				},
			},
			// (30 + 60) * 5 + 30 * 2 + 10 = 520s
			want: 520 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateSessionTimeout(tt.cfg)
			if got != tt.want {
				t.Errorf("calculateSessionTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}
