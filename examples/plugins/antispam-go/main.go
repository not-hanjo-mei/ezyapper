// Anti-Spam Plugin
// This is a working example plugin that uses the external plugin system
// It works on Windows, Linux, and macOS
//
// Build:
//
//	go build -o antispam-plugin main.go
//
// Run (as plugin, started by main bot):
//
//	The main bot will start this automatically from the plugins directory
//
// Configuration (required file):
//
//	plugins/antispam-go/config.yaml
//
// Run (standalone for testing - waits for host net/rpc connection):
//
//	./antispam-plugin
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/plugin"
	"ezyapper/internal/types"

	"gopkg.in/yaml.v3"
)

// AntiSpamPlugin implements the plugin interface
type AntiSpamPlugin struct {
	messageLog map[string][]time.Time
	mu         sync.RWMutex
	config     Config
}

// Config holds plugin configuration
type Config struct {
	MaxMessages  int
	TimeWindow   time.Duration
	IgnoreBots   bool
	LogDetection bool
}

// fileConfig is the strict on-disk plugin configuration format.
type fileConfig struct {
	MaxMessages       *int  `yaml:"max_messages"`
	TimeWindowSeconds *int  `yaml:"time_window_seconds"`
	IgnoreBots        *bool `yaml:"ignore_bots"`
	LogDetection      *bool `yaml:"log_detection"`
}

// Info returns plugin metadata
func (p *AntiSpamPlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "antispam",
		Version:     "0.0.0",
		Author:      "EZyapper",
		Description: "Prevents spam by limiting message rate per user",
		Priority:    100,
	}, nil
}

// OnMessage is called for every message
func (p *AntiSpamPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	// Skip bot messages if configured
	if p.config.IgnoreBots && msg.IsBot {
		return true, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Create unique key for channel + user
	key := msg.ChannelID + ":" + msg.AuthorID
	now := time.Now()

	// Clean up old entries
	var recent []time.Time
	for _, t := range p.messageLog[key] {
		if now.Sub(t) < p.config.TimeWindow {
			recent = append(recent, t)
		}
	}

	// Check limit
	if len(recent) >= p.config.MaxMessages {
		if p.config.LogDetection {
			fmt.Printf("[ANTISPAM] Detected spam from user %s in channel %s (%d messages in %v)\n",
				msg.AuthorID, msg.ChannelID, len(recent), p.config.TimeWindow)
		}
		return false, nil
	}

	// Record message
	p.messageLog[key] = append(recent, now)
	return true, nil
}

// OnResponse is called after the bot generates a response
func (p *AntiSpamPlugin) OnResponse(msg types.DiscordMessage, response string) error {
	// No action needed
	return nil
}

// Shutdown is called when the plugin is being stopped
func (p *AntiSpamPlugin) Shutdown() error {
	fmt.Println("[ANTISPAM] Plugin shutting down")
	return nil
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
	path := pluginConfigPath()

	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf(
			"failed to read plugin config file: %s: %w\n"+
				"where to configure:\n"+
				"- create/edit %s\n"+
				"- required keys: max_messages, time_window_seconds, ignore_bots, log_detection\n"+
				"- example: examples/plugins/antispam-go/config.yaml.example",
			path,
			err,
			path,
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
				"- required keys: max_messages, time_window_seconds, ignore_bots, log_detection\n"+
				"- example: examples/plugins/antispam-go/config.yaml.example",
			path,
			err,
			path,
		)
	}

	var errors []string
	if raw.MaxMessages == nil {
		errors = append(errors, "max_messages is required")
	} else if *raw.MaxMessages <= 0 {
		errors = append(errors, "max_messages must be a positive integer")
	} else {
		cfg.MaxMessages = *raw.MaxMessages
	}

	if raw.TimeWindowSeconds == nil {
		errors = append(errors, "time_window_seconds is required")
	} else if *raw.TimeWindowSeconds <= 0 {
		errors = append(errors, "time_window_seconds must be a positive integer")
	} else {
		cfg.TimeWindow = time.Duration(*raw.TimeWindowSeconds) * time.Second
	}

	if raw.IgnoreBots == nil {
		errors = append(errors, "ignore_bots is required")
	} else {
		cfg.IgnoreBots = *raw.IgnoreBots
	}

	if raw.LogDetection == nil {
		errors = append(errors, "log_detection is required")
	} else {
		cfg.LogDetection = *raw.LogDetection
	}

	if len(errors) > 0 {
		return cfg, fmt.Errorf(
			"configuration errors in %s: %v\n"+
				"where to configure:\n"+
				"- edit %s\n"+
				"- required keys: max_messages, time_window_seconds, ignore_bots, log_detection\n"+
				"- example: examples/plugins/antispam-go/config.yaml.example\n"+
				"- plugin folder path is available in env EZYAPPER_PLUGIN_PATH",
			path,
			errors,
			path,
		)
	}

	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ANTISPAM] Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	p := &AntiSpamPlugin{
		messageLog: make(map[string][]time.Time),
		config:     cfg,
	}

	fmt.Println("[ANTISPAM] Plugin starting...")
	fmt.Printf("[ANTISPAM] Config: max %d messages per %v\n", p.config.MaxMessages, p.config.TimeWindow)

	// This will connect to the host process and serve RPC requests
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[ANTISPAM] Error: %v\n", err)
		os.Exit(1)
	}
}
