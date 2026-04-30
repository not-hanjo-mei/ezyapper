package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ezyapper/internal/plugin"
	"ezyapper/internal/types"

	"gopkg.in/yaml.v3"
)

type DateTimePlugin struct {
	config DateTimeConfig
}

type DateTimeConfig struct {
	Location             *time.Location
	TimezoneLabel        string
	GetCurrentDatetimeMs int
}

type dateTimeFileConfig struct {
	Timezone         *string `yaml:"timezone"`
	UTCOffsetHours   *int    `yaml:"utc_offset_hours"`
	UTCOffsetMinutes *int    `yaml:"utc_offset_minutes"`
	ToolTimeouts     *struct {
		GetCurrentDatetimeMs *int `yaml:"get_current_datetime_ms"`
	} `yaml:"tool_timeouts"`
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

func loadDateTimeConfig() (DateTimeConfig, error) {
	var cfg DateTimeConfig
	path := pluginConfigPath()

	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read plugin config file: %s: %w\n"+
			"where to configure:\n"+
			"- create/edit %s\n"+
			"- required keys: tool_timeouts.get_current_datetime_ms\n"+
			"- optional keys: timezone, utc_offset_hours, utc_offset_minutes\n"+
			"- example: examples/plugins/datetime-go/config.yaml.example",
			path, err, path)
	}

	var raw dateTimeFileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return cfg, fmt.Errorf("invalid plugin config file: %s: %w\n"+
			"where to configure:\n"+
			"- fix %s\n"+
			"- example: examples/plugins/datetime-go/config.yaml.example",
			path, err, path)
	}

	var errors []string
	zoneName, _ := time.Now().Zone()
	if strings.TrimSpace(zoneName) == "" {
		zoneName = time.Local.String()
	}

	cfg.Location = time.Local
	cfg.TimezoneLabel = zoneName
	cfg.GetCurrentDatetimeMs = 5000

	if raw.ToolTimeouts != nil && raw.ToolTimeouts.GetCurrentDatetimeMs != nil {
		cfg.GetCurrentDatetimeMs = *raw.ToolTimeouts.GetCurrentDatetimeMs
	} else if raw.ToolTimeouts == nil {
		errors = append(errors, "tool_timeouts is required")
	} else if raw.ToolTimeouts.GetCurrentDatetimeMs == nil {
		errors = append(errors, "tool_timeouts.get_current_datetime_ms is required")
	}

	if cfg.GetCurrentDatetimeMs <= 0 {
		errors = append(errors, fmt.Sprintf("tool_timeouts.get_current_datetime_ms must be > 0, got %d", cfg.GetCurrentDatetimeMs))
	}

	if raw.UTCOffsetMinutes != nil || raw.UTCOffsetHours != nil {
		offsetMinutes := 0
		if raw.UTCOffsetMinutes != nil {
			offsetMinutes = *raw.UTCOffsetMinutes
		} else if raw.UTCOffsetHours != nil {
			offsetMinutes = *raw.UTCOffsetHours * 60
		}

		label := ""
		if raw.Timezone != nil {
			label = strings.TrimSpace(*raw.Timezone)
		}
		if label == "" {
			sign := "+"
			if offsetMinutes < 0 {
				sign = "-"
			}
			absMinutes := absInt(offsetMinutes)
			label = fmt.Sprintf("UTC%s%d:%02d", sign, absMinutes/60, absMinutes%60)
		}

		cfg.Location = time.FixedZone(label, offsetMinutes*60)
		cfg.TimezoneLabel = label
	} else if raw.Timezone != nil && strings.TrimSpace(*raw.Timezone) != "" {
		tz := strings.TrimSpace(*raw.Timezone)
		loc, err := time.LoadLocation(tz)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to load timezone %q: %v", tz, err))
		} else {
			cfg.Location = loc
			cfg.TimezoneLabel = tz
		}
	}

	if len(errors) > 0 {
		return cfg, fmt.Errorf("configuration errors in %s: %v\n"+
			"where to configure:\n"+
			"- edit %s\n"+
			"- example: examples/plugins/datetime-go/config.yaml.example",
			path, errors, path)
	}

	return cfg, nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (p *DateTimePlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "datetime",
		Version:     "0.0.0",
		Author:      "EZyapper",
		Description: "Provides current datetime tool",
		Priority:    10,
	}, nil
}

func (p *DateTimePlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *DateTimePlugin) OnResponse(msg types.DiscordMessage, response string) error {
	return nil
}

func (p *DateTimePlugin) Shutdown() error {
	return nil
}

func (p *DateTimePlugin) ListTools() ([]plugin.ToolSpec, error) {
	return []plugin.ToolSpec{
		{
			Name:        "get_current_datetime",
			Description: "Get the current date and time",
			TimeoutMs:   p.config.GetCurrentDatetimeMs,
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}, nil
}

func (p *DateTimePlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	if name != "get_current_datetime" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	now := time.Now().In(p.config.Location)
	result := map[string]interface{}{
		"date":         now.Format("2006-01-02"),
		"time":         now.Format("15:04:05"),
		"timezone":     p.config.TimezoneLabel,
		"weekday":      now.Weekday().String(),
		"unix_seconds": now.Unix(),
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(data), nil
}

func main() {
	config, err := loadDateTimeConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DATETIME] Error loading config: %v\n", err)
		os.Exit(1)
	}

	p := &DateTimePlugin{config: config}
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[DATETIME] Error: %v\n", err)
		os.Exit(1)
	}
}
