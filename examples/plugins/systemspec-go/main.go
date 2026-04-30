package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ezyapper/internal/plugin"
	"ezyapper/internal/types"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	ToolTimeouts *struct {
		GetSystemSpecMs *int `yaml:"get_system_spec_ms"`
	} `yaml:"tool_timeouts"`
}

type Config struct {
	GetSystemSpecMs int
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
			"- required keys: tool_timeouts.get_system_spec_ms\n"+
			"- example: examples/plugins/systemspec-go/config.yaml.example",
			path, err, path)
	}

	var raw fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return cfg, fmt.Errorf("invalid plugin config file: %s: %w\n"+
			"where to configure:\n"+
			"- fix %s\n"+
			"- example: examples/plugins/systemspec-go/config.yaml.example",
			path, err, path)
	}

	var errors []string
	if raw.ToolTimeouts == nil {
		errors = append(errors, "tool_timeouts is required")
	} else if raw.ToolTimeouts.GetSystemSpecMs == nil {
		errors = append(errors, "tool_timeouts.get_system_spec_ms is required")
	} else {
		cfg.GetSystemSpecMs = *raw.ToolTimeouts.GetSystemSpecMs
		if cfg.GetSystemSpecMs <= 0 {
			errors = append(errors, fmt.Sprintf("tool_timeouts.get_system_spec_ms must be > 0, got %d", cfg.GetSystemSpecMs))
		}
	}

	if len(errors) > 0 {
		return cfg, fmt.Errorf("configuration errors in %s: %v\n"+
			"where to configure:\n"+
			"- edit %s\n"+
			"- example: examples/plugins/systemspec-go/config.yaml.example",
			path, errors, path)
	}

	return cfg, nil
}

type SystemSpecPlugin struct {
	config Config
}

func (p *SystemSpecPlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "systemspec",
		Version:     "0.0.0",
		Author:      "EZyapper",
		Description: "Provides system specification tool",
		Priority:    10,
	}, nil
}

func (p *SystemSpecPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *SystemSpecPlugin) OnResponse(msg types.DiscordMessage, response string) error {
	return nil
}

func (p *SystemSpecPlugin) Shutdown() error {
	return nil
}

func (p *SystemSpecPlugin) ListTools() ([]plugin.ToolSpec, error) {
	return []plugin.ToolSpec{
		{
			Name:        "get_system_spec",
			Description: "Get CPU model, thread count, max frequency, and total memory",
			TimeoutMs:   p.config.GetSystemSpecMs,
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}, nil
}

func (p *SystemSpecPlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	if name != "get_system_spec" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	info, err := cpu.Info()
	if err != nil {
		return "", fmt.Errorf("failed to get cpu info: %w", err)
	}

	var model string
	var maxFreq float64
	if len(info) > 0 {
		model = strings.TrimSpace(info[0].ModelName)
		if model == "" {
			model = fmt.Sprintf("%s %s", info[0].VendorID, info[0].Family)
		}
		for _, c := range info {
			if c.Mhz > maxFreq {
				maxFreq = c.Mhz
			}
		}
	} else {
		model = "Unknown"
	}

	threads, err := cpu.Counts(true)
	if err != nil || threads <= 0 {
		threads = len(info)
	}

	vmem, err := mem.VirtualMemory()
	if err != nil {
		return "", fmt.Errorf("failed to get memory info: %w", err)
	}
	totalGB := float64(vmem.Total) / (1024 * 1024 * 1024)

	result := map[string]interface{}{
		"cpu_model":        model,
		"cpu_threads":      threads,
		"cpu_max_freq_mhz": maxFreq,
		"memory_total_gb":  fmt.Sprintf("%.2f", totalGB),
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(data), nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[SYSTEMSPEC] Config error: %v\n", err)
		os.Exit(1)
	}

	p := &SystemSpecPlugin{config: cfg}
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[SYSTEMSPEC] Error: %v\n", err)
		os.Exit(1)
	}
}
