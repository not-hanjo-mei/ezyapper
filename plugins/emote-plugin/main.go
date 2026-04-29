package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"ezyapper/internal/plugin"

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
}

// Config holds the fully resolved configuration with defaults applied.
type Config struct {
	DataDir                      string
	MaxImageSizeKb               int
	AllowedFormats               []string
	VisionApiKey                 string
	VisionApiBaseUrl             string
	VisionModel                  string
	VisionTimeoutSeconds         int
	VisionPrompt                 string
	AutoStealEnabled             bool
	AdditionalBlacklistChannels  []string
	AdditionalWhitelistChannels  []string
	AdditionalBlacklistUsers     []string
	RateLimitPerMinute           int
	CooldownSeconds              int
	LoggingEnabled               bool
	LoggingLevel                 string
}

// EmotePlugin is the main plugin struct.
type EmotePlugin struct {
	config  Config
	storage *Storage
}

func loadConfig(configPath string) (Config, error) {
	cfg := Config{
		DataDir:                      "data",
		MaxImageSizeKb:               512,
		AllowedFormats:               []string{"png", "jpg", "jpeg", "webp"},
		VisionApiBaseUrl:             "https://api.openai.com/v1",
		VisionModel:                  "gpt-4o-mini",
		VisionTimeoutSeconds:         30,
		VisionPrompt:                 "Analyze this image and determine if it is a \"meme/emote/sticker\" suitable for a chat reaction library.",
		AutoStealEnabled:             true,
		RateLimitPerMinute:           5,
		CooldownSeconds:              2,
		LoggingEnabled:               true,
		LoggingLevel:                 "info",
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

	return cfg, nil
}

// Info returns plugin metadata.
func (p *EmotePlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "emote-plugin",
		Version:     "0.0.1",
		Author:      "EZyapper",
		Description: "Auto-steals emotes from images and provides searchable emote library",
		Priority:    10,
	}, nil
}

// OnMessage is called for every Discord message.
// Returns true to continue processing (placeholder, T5 will implement steal logic).
func (p *EmotePlugin) OnMessage(msg plugin.DiscordMessage) (bool, error) {
	return true, nil
}

// OnResponse is called after the bot generates a response.
func (p *EmotePlugin) OnResponse(msg plugin.DiscordMessage, response string) error {
	return nil
}

// Shutdown is called when the plugin is being stopped.
func (p *EmotePlugin) Shutdown() error {
	return nil
}

// ListTools returns the tool specs exposed by this plugin.
func (p *EmotePlugin) ListTools() ([]plugin.ToolSpec, error) {
	return []plugin.ToolSpec{
		{
			Name:        "list_emotes",
			Description: "List available emotes with optional filtering",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "search_emote",
			Description: "Search for emotes by name, description, or tags",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "get_emote",
			Description: "Get a specific emote by ID or name",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}, nil
}

// ExecuteTool dispatches tool calls by name.
func (p *EmotePlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "list_emotes":
		result := map[string]interface{}{"status": "not yet implemented"}
		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	case "search_emote":
		result := map[string]interface{}{"status": "not yet implemented"}
		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	case "get_emote":
		result := map[string]interface{}{"status": "not yet implemented"}
		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func main() {
	configPath := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG"))
	config, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Error loading config: %v\n", err)
		os.Exit(1)
	}

	p := &EmotePlugin{config: config}
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Error: %v\n", err)
		os.Exit(1)
	}
}
