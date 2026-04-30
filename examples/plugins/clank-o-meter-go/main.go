package main

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ezyapper/internal/plugin"
	"ezyapper/internal/types"

	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	ToolTimeouts *struct {
		GetClankOMeterMs *int `yaml:"get_clank_o_meter_ms"`
	} `yaml:"tool_timeouts"`
}

type Config struct {
	GetClankOMeterMs int
}

type ClankOMeterPlugin struct {
	config Config
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

func loadConfig() (Config, error) {
	var cfg Config
	path := pluginConfigPath()

	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read plugin config file: %s: %w\n"+
			"where to configure:\n"+
			"- create/edit %s\n"+
			"- required keys: tool_timeouts.get_clank_o_meter_ms\n"+
			"- example: examples/plugins/clank-o-meter-go/config.yaml.example",
			path, err, path)
	}

	var raw fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return cfg, fmt.Errorf("invalid plugin config file: %s: %w\n"+
			"where to configure:\n"+
			"- fix %s\n"+
			"- example: examples/plugins/clank-o-meter-go/config.yaml.example",
			path, err, path)
	}

	var errors []string
	if raw.ToolTimeouts == nil {
		errors = append(errors, "tool_timeouts is required")
	} else if raw.ToolTimeouts.GetClankOMeterMs == nil {
		errors = append(errors, "tool_timeouts.get_clank_o_meter_ms is required")
	} else {
		cfg.GetClankOMeterMs = *raw.ToolTimeouts.GetClankOMeterMs
		if cfg.GetClankOMeterMs <= 0 {
			errors = append(errors, fmt.Sprintf("tool_timeouts.get_clank_o_meter_ms must be > 0, got %d", cfg.GetClankOMeterMs))
		}
	}

	if len(errors) > 0 {
		return cfg, fmt.Errorf("configuration errors in %s: %v\n"+
			"where to configure:\n"+
			"- edit %s\n"+
			"- example: examples/plugins/clank-o-meter-go/config.yaml.example",
			path, errors, path)
	}

	return cfg, nil
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

func (p *ClankOMeterPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *ClankOMeterPlugin) OnResponse(msg types.DiscordMessage, response string) error {
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

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(data), nil
}

func main() {
	config, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[CLANK-O-METER] Error loading config: %v\n", err)
		os.Exit(1)
	}

	p := &ClankOMeterPlugin{config: config}
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[CLANK-O-METER] Error: %v\n", err)
		os.Exit(1)
	}
}
