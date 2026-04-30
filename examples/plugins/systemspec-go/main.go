package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"ezyapper/internal/plugin"

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

func loadConfig(configPath string) (Config, error) {
	cfg := Config{
		GetSystemSpecMs: 5000,
	}

	if strings.TrimSpace(configPath) == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg, fmt.Errorf("failed to read config file %q: %w", configPath, err)
	}

	var fileCfg fileConfig
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config file %q: %w", configPath, err)
	}

	if fileCfg.ToolTimeouts != nil && fileCfg.ToolTimeouts.GetSystemSpecMs != nil {
		cfg.GetSystemSpecMs = *fileCfg.ToolTimeouts.GetSystemSpecMs
	}

	return cfg, nil
}

func validateConfig(cfg *Config) error {
	if cfg.GetSystemSpecMs <= 0 {
		return fmt.Errorf("tool_timeouts.get_system_spec_ms must be > 0, got %d", cfg.GetSystemSpecMs)
	}
	return nil
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

func (p *SystemSpecPlugin) OnMessage(msg plugin.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *SystemSpecPlugin) OnResponse(msg plugin.DiscordMessage, response string) error {
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

	// Use logical CPU count for thread reporting; cpu.Info() entries are unreliable on Windows.
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
	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func main() {
	p := &SystemSpecPlugin{}

	configPath := os.Getenv("EZYAPPER_PLUGIN_CONFIG")
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[SYSTEMSPEC] Config error: %v\n", err)
		os.Exit(1)
	}
	if err := validateConfig(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[SYSTEMSPEC] Config validation error: %v\n", err)
		os.Exit(1)
	}
	p.config = cfg

	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[SYSTEMSPEC] Error: %v\n", err)
		os.Exit(1)
	}
}
