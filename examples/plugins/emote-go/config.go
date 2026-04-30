package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// fileConfig mirrors config.yaml with pointer-based fields for strict loading.
// nil pointer = field missing from config.
type fileConfig struct {
	Storage *struct {
		DataDir        *string   `yaml:"data_dir"`
		MaxImageSizeKb *int      `yaml:"max_image_size_kb"`
		AllowedFormats *[]string `yaml:"allowed_formats"`
	} `yaml:"storage"`
	Vision *struct {
		ApiKey         *string `yaml:"api_key"`
		ApiBaseUrl     *string `yaml:"api_base_url"`
		Model          *string `yaml:"model"`
		TimeoutSeconds *int    `yaml:"timeout_seconds"`
		Prompt         *string `yaml:"prompt"`
	} `yaml:"vision"`
	AutoSteal *struct {
		Enabled                     *bool     `yaml:"enabled"`
		AdditionalBlacklistChannels *[]string `yaml:"additional_blacklist_channels"`
		AdditionalWhitelistChannels *[]string `yaml:"additional_whitelist_channels"`
		AdditionalBlacklistUsers    *[]string `yaml:"additional_blacklist_users"`
		RateLimitPerMinute          *int      `yaml:"rate_limit_per_minute"`
		CooldownSeconds             *int      `yaml:"cooldown_seconds"`
	} `yaml:"auto_steal"`
	Logging *struct {
		Enabled *bool   `yaml:"enabled"`
		Level   *string `yaml:"level"`
	} `yaml:"logging"`
	Emote *struct {
		Model       *string  `yaml:"model"`
		ApiKey      *string  `yaml:"api_key"`
		ApiBaseURL  *string  `yaml:"api_base_url"`
		MaxTokens   *int     `yaml:"max_tokens"`
		Temperature *float64 `yaml:"temperature"`
	} `yaml:"emote"`
	Discord *struct {
		Token *string `yaml:"token"`
	} `yaml:"discord"`
}

// Config holds the fully resolved configuration with defaults applied.
type Config struct {
	DataDir                     string
	MaxImageSizeKb              int
	AllowedFormats              []string
	VisionApiKey                string
	VisionApiBaseUrl            string
	VisionModel                 string
	VisionTimeoutSeconds        int
	VisionPrompt                string
	AutoStealEnabled            bool
	AdditionalBlacklistChannels []string
	AdditionalWhitelistChannels []string
	AdditionalBlacklistUsers    []string
	RateLimitPerMinute          int
	CooldownSeconds             int
	LoggingEnabled              bool
	LoggingLevel                string
	EmoteModel                  string
	EmoteApiKey                 string
	EmoteApiBaseURL             string
	EmoteMaxTokens              int
	EmoteTemperature            float64
	DiscordToken                string
}

func loadConfig(configPath string) (Config, error) {
	cfg := Config{
		DataDir:              "data",
		MaxImageSizeKb:       512,
		AllowedFormats:       []string{"png", "jpg", "jpeg", "webp", "gif"},
		VisionApiBaseUrl:     "https://api.openai.com/v1",
		VisionModel:          "gpt-4o-mini",
		VisionTimeoutSeconds: 30,
		VisionPrompt:         "Analyze this image and determine if it is a \"meme/emote/sticker\" suitable for a chat reaction library.",
		AutoStealEnabled:     true,
		RateLimitPerMinute:   5,
		CooldownSeconds:      2,
		LoggingEnabled:       true,
		LoggingLevel:         "info",
		EmoteApiBaseURL:      "https://asus.omgpizzatnt.top:3000/v1",
		EmoteMaxTokens:       1024,
		EmoteTemperature:     0.1,
	}

	if strings.TrimSpace(configPath) == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("failed to read config file %q: %w", configPath, err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}

	var fileCfg fileConfig
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config file %q: %w", configPath, err)
	}

	if fileCfg.Storage != nil {
		if fileCfg.Storage.DataDir != nil {
			cfg.DataDir = *fileCfg.Storage.DataDir
		}
		if fileCfg.Storage.MaxImageSizeKb != nil {
			cfg.MaxImageSizeKb = *fileCfg.Storage.MaxImageSizeKb
		}
		if fileCfg.Storage.AllowedFormats != nil {
			cfg.AllowedFormats = *fileCfg.Storage.AllowedFormats
		}
	}

	if fileCfg.Vision != nil {
		if fileCfg.Vision.ApiKey != nil {
			cfg.VisionApiKey = *fileCfg.Vision.ApiKey
		}
		if fileCfg.Vision.ApiBaseUrl != nil {
			cfg.VisionApiBaseUrl = *fileCfg.Vision.ApiBaseUrl
		}
		if fileCfg.Vision.Model != nil {
			cfg.VisionModel = *fileCfg.Vision.Model
		}
		if fileCfg.Vision.TimeoutSeconds != nil {
			cfg.VisionTimeoutSeconds = *fileCfg.Vision.TimeoutSeconds
		}
		if fileCfg.Vision.Prompt != nil {
			cfg.VisionPrompt = *fileCfg.Vision.Prompt
		}
	}

	if fileCfg.AutoSteal != nil {
		if fileCfg.AutoSteal.Enabled != nil {
			cfg.AutoStealEnabled = *fileCfg.AutoSteal.Enabled
		}
		if fileCfg.AutoSteal.AdditionalBlacklistChannels != nil {
			cfg.AdditionalBlacklistChannels = *fileCfg.AutoSteal.AdditionalBlacklistChannels
		}
		if fileCfg.AutoSteal.AdditionalWhitelistChannels != nil {
			cfg.AdditionalWhitelistChannels = *fileCfg.AutoSteal.AdditionalWhitelistChannels
		}
		if fileCfg.AutoSteal.AdditionalBlacklistUsers != nil {
			cfg.AdditionalBlacklistUsers = *fileCfg.AutoSteal.AdditionalBlacklistUsers
		}
		if fileCfg.AutoSteal.RateLimitPerMinute != nil {
			cfg.RateLimitPerMinute = *fileCfg.AutoSteal.RateLimitPerMinute
		}
		if fileCfg.AutoSteal.CooldownSeconds != nil {
			cfg.CooldownSeconds = *fileCfg.AutoSteal.CooldownSeconds
		}
	}

	if fileCfg.Logging != nil {
		if fileCfg.Logging.Enabled != nil {
			cfg.LoggingEnabled = *fileCfg.Logging.Enabled
		}
		if fileCfg.Logging.Level != nil {
			cfg.LoggingLevel = *fileCfg.Logging.Level
		}
	}

	if fileCfg.Emote != nil {
		if fileCfg.Emote.Model != nil {
			cfg.EmoteModel = *fileCfg.Emote.Model
		}
		if fileCfg.Emote.ApiKey != nil {
			cfg.EmoteApiKey = *fileCfg.Emote.ApiKey
		}
		if fileCfg.Emote.ApiBaseURL != nil {
			cfg.EmoteApiBaseURL = *fileCfg.Emote.ApiBaseURL
		}
		if fileCfg.Emote.MaxTokens != nil {
			cfg.EmoteMaxTokens = *fileCfg.Emote.MaxTokens
		}
		if fileCfg.Emote.Temperature != nil {
			cfg.EmoteTemperature = *fileCfg.Emote.Temperature
		}
	}

	if fileCfg.Discord != nil {
		if fileCfg.Discord.Token != nil {
			cfg.DiscordToken = *fileCfg.Discord.Token
		}
	}

	return cfg, nil
}

// validateConfig checks the resolved Config for required fields and applies
// runtime defaults/safeguards. Returns error only for fatal misconfiguration.
// Non-fatal issues are fixed silently with a warning to stderr.
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

	// Emote LLM config is optional — empty values gracefully disable.
	return nil
}
