package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ezyapper/internal/plugin"

	"gopkg.in/yaml.v3"
)

type DateTimeConfig struct {
	Location      *time.Location
	TimezoneLabel string
}

type dateTimeConfigFile struct {
	Timezone         string `yaml:"timezone"`
	UTCOffsetHours   *int   `yaml:"utc_offset_hours"`
	UTCOffsetMinutes *int   `yaml:"utc_offset_minutes"`
}

type DateTimePlugin struct {
	config DateTimeConfig
}

func loadDateTimeConfig(configPath string) (DateTimeConfig, error) {
	zoneName, _ := time.Now().Zone()
	if strings.TrimSpace(zoneName) == "" {
		zoneName = time.Local.String()
	}

	config := DateTimeConfig{
		Location:      time.Local,
		TimezoneLabel: zoneName,
	}

	if strings.TrimSpace(configPath) == "" {
		return config, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config, nil
		}
		return config, fmt.Errorf("failed to read config file %q: %w", configPath, err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return config, nil
	}

	var fileConfig dateTimeConfigFile
	if err := yaml.Unmarshal(data, &fileConfig); err != nil {
		return config, fmt.Errorf("failed to parse config file %q: %w", configPath, err)
	}

	if fileConfig.UTCOffsetMinutes != nil || fileConfig.UTCOffsetHours != nil {
		offsetMinutes := 0
		if fileConfig.UTCOffsetMinutes != nil {
			offsetMinutes = *fileConfig.UTCOffsetMinutes
		} else if fileConfig.UTCOffsetHours != nil {
			offsetMinutes = *fileConfig.UTCOffsetHours * 60
		}

		label := strings.TrimSpace(fileConfig.Timezone)
		if label == "" {
			sign := "+"
			if offsetMinutes < 0 {
				sign = "-"
			}
			absMinutes := absInt(offsetMinutes)
			label = fmt.Sprintf("UTC%s%d:%02d", sign, absMinutes/60, absMinutes%60)
		}

		config.Location = time.FixedZone(label, offsetMinutes*60)
		config.TimezoneLabel = label
		return config, nil
	}

	if tz := strings.TrimSpace(fileConfig.Timezone); tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return config, fmt.Errorf("failed to load timezone %q: %w", tz, err)
		}
		config.Location = loc
		config.TimezoneLabel = tz
	}

	return config, nil
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

func (p *DateTimePlugin) OnMessage(msg plugin.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *DateTimePlugin) OnResponse(msg plugin.DiscordMessage, response string) error {
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
	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func main() {
	configPath := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG"))
	config, err := loadDateTimeConfig(configPath)
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
