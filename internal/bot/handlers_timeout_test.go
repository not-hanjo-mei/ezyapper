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
			want: 385 * time.Second,
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
			want: 160 * time.Second,
		},
		{
			name: "zero iterations returns buffer only",
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
			want: 10 * time.Second,
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
			// (30 + 60) * 5 + 10 = 460s
			want: 460 * time.Second,
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
