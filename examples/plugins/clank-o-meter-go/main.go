package main

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"ezyapper/internal/plugin"

	"gopkg.in/yaml.v3"
)

// fileConfig mirrors config.yaml with pointer-based fields for strict loading.
// nil pointer = field missing from config.
type fileConfig struct {
	ToolTimeouts *struct {
		GetClankOMeterMs *int `yaml:"get_clank_o_meter_ms"`
	} `yaml:"tool_timeouts"`
}

// Config holds the fully resolved configuration.
type Config struct {
	GetClankOMeterMs int
}

// ClankOMeterPlugin implements the plugin interface.
type ClankOMeterPlugin struct {
	config Config
}

func (p *ClankOMeterPlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "clank-o-meter",
		Version:     "0.0.0",
		Author:      "EZyapper",
		Description: "Detecting clanker levels / gooner level / wanker level by given Discord user ID.",
		Priority:    10,
	}, nil
}

func (p *ClankOMeterPlugin) OnMessage(msg plugin.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *ClankOMeterPlugin) OnResponse(msg plugin.DiscordMessage, response string) error {
	return nil
}

func (p *ClankOMeterPlugin) Shutdown() error {
	return nil
}

func (p *ClankOMeterPlugin) ListTools() ([]plugin.ToolSpec, error) {
	return []plugin.ToolSpec{
		{
			Name:        "get_clank_o_meter",
			Description: "Return deterministic score (0-100) for a Discord user ID",
			TimeoutMs:   p.config.GetClankOMeterMs,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"user_id": map[string]interface{}{
						"type":        "string",
						"description": "Discord numeric user ID",
					},
				},
				"required": []string{"user_id"},
			},
		},
	}, nil
}

func (p *ClankOMeterPlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	if name != "get_clank_o_meter" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	rawID, ok := args["user_id"]
	if !ok {
		return "", fmt.Errorf("missing required argument: user_id")
	}

	userID := strings.TrimSpace(fmt.Sprint(rawID))
	if userID == "" {
		return "", fmt.Errorf("user_id cannot be empty")
	}

	if _, err := strconv.ParseUint(userID, 10, 64); err != nil {
		return "", fmt.Errorf("user_id must be a numeric Discord user ID")
	}

	digest := md5.Sum([]byte(userID))
	value := binary.BigEndian.Uint64(digest[:8])
	score := int(value % 101)

	result := map[string]interface{}{
		"user_id":       userID,
		"clank_o_meter": score,
		"algorithm":     "MD5",
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func loadConfig(configPath string) (Config, error) {
	cfg := Config{
		GetClankOMeterMs: 5000,
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

	if fileCfg.ToolTimeouts != nil && fileCfg.ToolTimeouts.GetClankOMeterMs != nil {
		cfg.GetClankOMeterMs = *fileCfg.ToolTimeouts.GetClankOMeterMs
	}

	return cfg, nil
}

func validateConfig(cfg *Config) error {
	if cfg.GetClankOMeterMs <= 0 {
		return fmt.Errorf("tool_timeouts.get_clank_o_meter_ms is required and must be > 0")
	}
	return nil
}

func main() {
	configPath := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG"))
	config, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[CLANK-O-METER] Error loading config: %v\n", err)
		os.Exit(1)
	}

	if err := validateConfig(&config); err != nil {
		fmt.Fprintf(os.Stderr, "[CLANK-O-METER] Config validation error: %v\n", err)
		os.Exit(1)
	}

	p := &ClankOMeterPlugin{config: config}
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[CLANK-O-METER] Error: %v\n", err)
		os.Exit(1)
	}
}
