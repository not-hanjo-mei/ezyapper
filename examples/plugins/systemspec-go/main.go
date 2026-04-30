package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"ezyapper/internal/plugin"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

type SystemSpecPlugin struct{}

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
			TimeoutMs:   5000,
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
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[SYSTEMSPEC] Error: %v\n", err)
		os.Exit(1)
	}
}
