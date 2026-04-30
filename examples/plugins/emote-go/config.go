package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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
	ToolTimeouts *struct {
		SearchEmoteMs *int `yaml:"search_emote_ms"`
		SendEmoteMs   *int `yaml:"send_emote_ms"`
	} `yaml:"tool_timeouts"`
}

// Config holds the fully resolved configuration. All fields are required and
// must be explicitly set in config.yaml — no implicit defaults.
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
	SearchEmoteMs               int
	SendEmoteMs                 int
}

func pluginConfigPath() string {
	if cfg := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG")); cfg != "" {
		return cfg
	}

	if dir := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_PATH")); dir != "" {
		return filepath.Join(dir, "config.yaml")
	}

	return "config.yaml"
}

// loadConfig loads and validates plugin config file.
// All configuration values are required and there are no defaults.
func loadConfig() (Config, error) {
	var cfg Config
	configPath := pluginConfigPath()

	content, err := os.ReadFile(configPath)
	if err != nil {
		return cfg, fmt.Errorf(
			"failed to read plugin config file: %s: %w\n"+
				"where to configure:\n"+
				"- create/edit %s\n"+
				"- example: examples/plugins/emote-go/config.yaml.example\n"+
				"- plugin folder path is available in env EZYAPPER_PLUGIN_PATH",
			configPath, err, configPath,
		)
	}

	if len(bytes.TrimSpace(content)) == 0 {
		return cfg, fmt.Errorf(
			"plugin config file is empty: %s\n"+
				"where to configure:\n"+
				"- create/edit %s\n"+
				"- example: examples/plugins/emote-go/config.yaml.example",
			configPath, configPath,
		)
	}

	var raw fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return cfg, fmt.Errorf(
			"invalid plugin config file: %s: %w\n"+
				"where to configure:\n"+
				"- fix %s\n"+
				"- example: examples/plugins/emote-go/config.yaml.example",
			configPath, err, configPath,
		)
	}

	var errs []string

	// storage.data_dir (required)
	if raw.Storage == nil || raw.Storage.DataDir == nil {
		errs = append(errs, "storage.data_dir is required")
	} else {
		cfg.DataDir = strings.TrimSpace(*raw.Storage.DataDir)
		if cfg.DataDir == "" {
			errs = append(errs, "storage.data_dir is required")
		}
	}

	// storage.max_image_size_kb (required)
	if raw.Storage == nil || raw.Storage.MaxImageSizeKb == nil {
		errs = append(errs, "storage.max_image_size_kb is required")
	} else {
		cfg.MaxImageSizeKb = *raw.Storage.MaxImageSizeKb
	}

	// storage.allowed_formats (required)
	if raw.Storage == nil || raw.Storage.AllowedFormats == nil {
		errs = append(errs, "storage.allowed_formats is required")
	} else {
		cfg.AllowedFormats = *raw.Storage.AllowedFormats
	}

	// vision.api_key (required)
	if raw.Vision == nil || raw.Vision.ApiKey == nil {
		errs = append(errs, "vision.api_key is required")
	} else {
		cfg.VisionApiKey = strings.TrimSpace(*raw.Vision.ApiKey)
	}

	// vision.api_base_url (required)
	if raw.Vision == nil || raw.Vision.ApiBaseUrl == nil {
		errs = append(errs, "vision.api_base_url is required")
	} else {
		cfg.VisionApiBaseUrl = strings.TrimRight(strings.TrimSpace(*raw.Vision.ApiBaseUrl), "/")
		if cfg.VisionApiBaseUrl == "" {
			errs = append(errs, "vision.api_base_url is required")
		}
	}

	// vision.model (required)
	if raw.Vision == nil || raw.Vision.Model == nil {
		errs = append(errs, "vision.model is required")
	} else {
		cfg.VisionModel = strings.TrimSpace(*raw.Vision.Model)
		if cfg.VisionModel == "" {
			errs = append(errs, "vision.model is required")
		}
	}

	// vision.timeout_seconds (required)
	if raw.Vision == nil || raw.Vision.TimeoutSeconds == nil {
		errs = append(errs, "vision.timeout_seconds is required")
	} else {
		cfg.VisionTimeoutSeconds = *raw.Vision.TimeoutSeconds
	}

	// vision.prompt (required)
	if raw.Vision == nil || raw.Vision.Prompt == nil {
		errs = append(errs, "vision.prompt is required")
	} else {
		cfg.VisionPrompt = strings.TrimSpace(*raw.Vision.Prompt)
		if cfg.VisionPrompt == "" {
			errs = append(errs, "vision.prompt is required")
		}
	}

	// auto_steal.enabled (required)
	if raw.AutoSteal == nil || raw.AutoSteal.Enabled == nil {
		errs = append(errs, "auto_steal.enabled is required (explicit true or false)")
	} else {
		cfg.AutoStealEnabled = *raw.AutoSteal.Enabled
	}

	// auto_steal.additional_blacklist_channels (required)
	if raw.AutoSteal == nil || raw.AutoSteal.AdditionalBlacklistChannels == nil {
		errs = append(errs, "auto_steal.additional_blacklist_channels is required")
	} else {
		cfg.AdditionalBlacklistChannels = *raw.AutoSteal.AdditionalBlacklistChannels
	}

	// auto_steal.additional_whitelist_channels (required)
	if raw.AutoSteal == nil || raw.AutoSteal.AdditionalWhitelistChannels == nil {
		errs = append(errs, "auto_steal.additional_whitelist_channels is required")
	} else {
		cfg.AdditionalWhitelistChannels = *raw.AutoSteal.AdditionalWhitelistChannels
	}

	// auto_steal.additional_blacklist_users (required)
	if raw.AutoSteal == nil || raw.AutoSteal.AdditionalBlacklistUsers == nil {
		errs = append(errs, "auto_steal.additional_blacklist_users is required")
	} else {
		cfg.AdditionalBlacklistUsers = *raw.AutoSteal.AdditionalBlacklistUsers
	}

	// auto_steal.rate_limit_per_minute (required)
	if raw.AutoSteal == nil || raw.AutoSteal.RateLimitPerMinute == nil {
		errs = append(errs, "auto_steal.rate_limit_per_minute is required")
	} else {
		cfg.RateLimitPerMinute = *raw.AutoSteal.RateLimitPerMinute
	}

	// auto_steal.cooldown_seconds (required)
	if raw.AutoSteal == nil || raw.AutoSteal.CooldownSeconds == nil {
		errs = append(errs, "auto_steal.cooldown_seconds is required")
	} else {
		cfg.CooldownSeconds = *raw.AutoSteal.CooldownSeconds
	}

	// logging.enabled (required)
	if raw.Logging == nil || raw.Logging.Enabled == nil {
		errs = append(errs, "logging.enabled is required (explicit true or false)")
	} else {
		cfg.LoggingEnabled = *raw.Logging.Enabled
	}

	// logging.level (required)
	if raw.Logging == nil || raw.Logging.Level == nil {
		errs = append(errs, "logging.level is required")
	} else {
		cfg.LoggingLevel = strings.TrimSpace(*raw.Logging.Level)
		if cfg.LoggingLevel == "" {
			errs = append(errs, "logging.level is required")
		}
	}

	// emote.model (required)
	if raw.Emote == nil || raw.Emote.Model == nil {
		errs = append(errs, "emote.model is required")
	} else {
		cfg.EmoteModel = strings.TrimSpace(*raw.Emote.Model)
	}

	// emote.api_key (required)
	if raw.Emote == nil || raw.Emote.ApiKey == nil {
		errs = append(errs, "emote.api_key is required")
	} else {
		cfg.EmoteApiKey = strings.TrimSpace(*raw.Emote.ApiKey)
	}

	// emote.api_base_url (required)
	if raw.Emote == nil || raw.Emote.ApiBaseURL == nil {
		errs = append(errs, "emote.api_base_url is required")
	} else {
		cfg.EmoteApiBaseURL = strings.TrimRight(strings.TrimSpace(*raw.Emote.ApiBaseURL), "/")
		if cfg.EmoteApiBaseURL == "" {
			errs = append(errs, "emote.api_base_url is required")
		}
	}

	// emote.max_tokens (required)
	if raw.Emote == nil || raw.Emote.MaxTokens == nil {
		errs = append(errs, "emote.max_tokens is required")
	} else {
		cfg.EmoteMaxTokens = *raw.Emote.MaxTokens
	}

	// emote.temperature (required)
	if raw.Emote == nil || raw.Emote.Temperature == nil {
		errs = append(errs, "emote.temperature is required")
	} else {
		cfg.EmoteTemperature = *raw.Emote.Temperature
	}

	// discord.token (required)
	if raw.Discord == nil || raw.Discord.Token == nil {
		errs = append(errs, "discord.token is required")
	} else {
		cfg.DiscordToken = strings.TrimSpace(*raw.Discord.Token)
	}

	// tool_timeouts.search_emote_ms (required)
	if raw.ToolTimeouts == nil || raw.ToolTimeouts.SearchEmoteMs == nil {
		errs = append(errs, "tool_timeouts.search_emote_ms is required")
	} else {
		cfg.SearchEmoteMs = *raw.ToolTimeouts.SearchEmoteMs
	}

	// tool_timeouts.send_emote_ms (required)
	if raw.ToolTimeouts == nil || raw.ToolTimeouts.SendEmoteMs == nil {
		errs = append(errs, "tool_timeouts.send_emote_ms is required")
	} else {
		cfg.SendEmoteMs = *raw.ToolTimeouts.SendEmoteMs
	}

	// Validate positive integers
	if cfg.MaxImageSizeKb <= 0 {
		errs = append(errs, "storage.max_image_size_kb must be a positive integer")
	}
	if cfg.VisionTimeoutSeconds <= 0 {
		errs = append(errs, "vision.timeout_seconds must be a positive integer")
	}
	if cfg.RateLimitPerMinute < 0 {
		errs = append(errs, "auto_steal.rate_limit_per_minute must be >= 0")
	}
	if cfg.CooldownSeconds < 0 {
		errs = append(errs, "auto_steal.cooldown_seconds must be >= 0")
	}
	if cfg.EmoteMaxTokens <= 0 {
		errs = append(errs, "emote.max_tokens must be a positive integer")
	}
	if cfg.EmoteTemperature < 0 || cfg.EmoteTemperature > 2 {
		errs = append(errs, "emote.temperature must be between 0 and 2")
	}
	if cfg.SearchEmoteMs <= 0 {
		errs = append(errs, "tool_timeouts.search_emote_ms must be a positive integer")
	}
	if cfg.SendEmoteMs <= 0 {
		errs = append(errs, "tool_timeouts.send_emote_ms must be a positive integer")
	}

	if len(errs) > 0 {
		return cfg, fmt.Errorf(
			"configuration errors in %s: %s\n"+
				"where to configure:\n"+
				"- edit %s\n"+
				"- example: examples/plugins/emote-go/config.yaml.example\n"+
				"- plugin folder path is available in env EZYAPPER_PLUGIN_PATH",
			configPath, strings.Join(errs, "; "), configPath,
		)
	}

	return cfg, nil
}
