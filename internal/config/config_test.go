package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoad_MissingRequired(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	emptyConfig := ``
	if err := os.WriteFile(configPath, []byte(emptyConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	_, err := Load(configPath)
	if err == nil {
		t.Error("Expected error for missing config, got nil")
	}
}

func validatePluginManifest(manifest map[string]interface{}) []string {
	var errs []string

	requiredFields := []string{"runtime", "name", "version", "author", "description", "priority", "tools"}
	for _, f := range requiredFields {
		if _, ok := manifest[f]; !ok {
			errs = append(errs, fmt.Sprintf("missing required field: %q", f))
		}
	}

	allowed := map[string]bool{
		"runtime": true, "name": true, "version": true, "author": true,
		"description": true, "priority": true, "tools": true,
	}
	for k := range manifest {
		if !allowed[k] {
			errs = append(errs, fmt.Sprintf("unknown top-level field: %q", k))
		}
	}

	if r, ok := manifest["runtime"]; ok {
		rs, ok := r.(string)
		if !ok {
			errs = append(errs, "runtime must be a string")
		} else if rs != "jsonrpc" && rs != "command" {
			errs = append(errs, fmt.Sprintf("runtime %q not in enum [jsonrpc, command]", rs))
		}
	}

	if p, ok := manifest["priority"]; ok {
		switch p.(type) {
		case float64, int:
			// valid: JSON decodes as float64, Go map literal as int
		default:
			errs = append(errs, "priority must be an integer")
		}
	}

	if t, ok := manifest["tools"]; ok {
		tools, ok := t.([]interface{})
		if !ok {
			errs = append(errs, "tools must be an array")
		} else {
			for i, ti := range tools {
				tool, ok := ti.(map[string]interface{})
				if !ok {
					errs = append(errs, fmt.Sprintf("tools[%d] must be an object", i))
					continue
				}

				for _, f := range []string{"name", "description", "parameters"} {
					if _, ok := tool[f]; !ok {
						errs = append(errs, fmt.Sprintf("tools[%d] missing required field: %q", i, f))
					}
				}

				toolAllowed := map[string]bool{
					"name": true, "description": true, "parameters": true,
					"command": true, "args": true, "arg_keys": true, "timeout_ms": true,
				}
				for k := range tool {
					if !toolAllowed[k] {
						errs = append(errs, fmt.Sprintf("tools[%d] unknown field: %q", i, k))
					}
				}

				if tm, ok := tool["timeout_ms"]; ok {
					var tmVal float64
					switch v := tm.(type) {
					case float64:
						tmVal = v
					case int:
						tmVal = float64(v)
					default:
						errs = append(errs, fmt.Sprintf("tools[%d].timeout_ms must be an integer", i))
						continue
					}
					if tmVal < 0 {
						errs = append(errs, fmt.Sprintf("tools[%d].timeout_ms must be >= 0", i))
					}
					if tmVal > 300000 {
						errs = append(errs, fmt.Sprintf("tools[%d].timeout_ms must be <= 300000", i))
					}
				}

				if args, ok := tool["args"]; ok {
					argsArr, ok := args.([]interface{})
					if !ok {
						errs = append(errs, fmt.Sprintf("tools[%d].args must be an array", i))
					} else {
						for j, a := range argsArr {
							if _, ok := a.(string); !ok {
								errs = append(errs, fmt.Sprintf("tools[%d].args[%d] must be a string", i, j))
							}
						}
					}
				}

				if ak, ok := tool["arg_keys"]; ok {
					akArr, ok := ak.([]interface{})
					if !ok {
						errs = append(errs, fmt.Sprintf("tools[%d].arg_keys must be an array", i))
					} else {
						for j, a := range akArr {
							if _, ok := a.(string); !ok {
								errs = append(errs, fmt.Sprintf("tools[%d].arg_keys[%d] must be a string", i, j))
							}
						}
					}
				}
			}
		}
	}

	return errs
}
func TestLoad_MissingDiscordToken(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	config := `schema_version: 3
core:
	discord:
		bot_name: "TestBot"
		reply_percentage: 0.15
		cooldown_seconds: 5
		max_responses_per_minute: 10
		consolidation_timeout_sec: 300
		typing_indicator_interval_sec: 5
		long_response_delay_ms: 500
		reply_truncation_length: 200
		image_cache_ttl_min: 60
		image_cache_max_entries: 100
	ai:
		api_base_url: "https://api.openai.com/v1"
		api_key: "test-key"
		model: "gpt-4o-mini"
		vision_model: "gpt-4o"
		max_tokens: 1024
		temperature: 0.8
		retry_count: 1
		timeout: 10
		system_prompt: "test"
		http_timeout_sec: 30
		max_tool_iterations: 5
		max_image_bytes: 10485760
		user_agent: "EZyapper/1.0"
memory_pipeline:
	embedding:
		model: "text-embedding-3-small"
		retry_count: 1
		timeout: 10
	memory:
		consolidation_interval: 50
		short_term_limit: 20
		max_paginated_limit: 100
		retrieval:
			top_k: 5
			min_score: 0.75
		consolidation:
			enabled: false
			system_prompt: "test"
			memory_search_limit: 20
	qdrant:
		host: "localhost"
		port: 6334
		vector_size: 1536
access_control:
	blacklist:
		users: []
		guilds: []
		channels: []
operations:
	web:
		port: 8080
		username: "admin"
		password: "test"
		enabled: true
		memories_page_limit: 50
		content_truncation_length: 500
		session_ttl_min: 30
		session_cleanup_interval_min: 5
		stats_query_timeout_sec: 5
		log_default_lines: 100
		log_max_lines: 1000
		log_max_read_bytes: 1048576
	logging:
		level: "info"
		file: "logs/test.log"
		max_size: 100
		max_backups: 3
		max_age: 30
	plugins:
		enabled: true
		plugins_dir: "plugins"
		startup_timeout_sec: 90
		rpc_timeout_sec: 5
		before_send_timeout_sec: 180
		command_timeout_sec: 45
		shutdown_timeout_sec: 5
		disable_timeout_sec: 2
	mcp:
		enabled: false
		servers: []
	operations:
		shutdown_timeout_sec: 300
		cleanup_interval_min: 5
`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	_, err := Load(configPath)
	if err == nil {
		t.Error("Expected error for missing discord token, got nil")
	}
}
func TestLoad_MissingAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	config := `schema_version: 3
core:
	discord:
		token: "test-token"
		bot_name: "TestBot"
		reply_percentage: 0.15
		cooldown_seconds: 5
		max_responses_per_minute: 10
		consolidation_timeout_sec: 300
		typing_indicator_interval_sec: 5
		long_response_delay_ms: 500
		reply_truncation_length: 200
		image_cache_ttl_min: 60
		image_cache_max_entries: 100
	ai:
		api_base_url: "https://api.openai.com/v1"
		model: "gpt-4o-mini"
		vision_model: "gpt-4o"
		max_tokens: 1024
		temperature: 0.8
		retry_count: 1
		timeout: 10
		system_prompt: "test"
		http_timeout_sec: 30
		max_tool_iterations: 5
		max_image_bytes: 10485760
		user_agent: "EZyapper/1.0"
memory_pipeline:
	embedding:
		model: "text-embedding-3-small"
		retry_count: 1
		timeout: 10
	memory:
		consolidation_interval: 50
		short_term_limit: 20
		max_paginated_limit: 100
		retrieval:
			top_k: 5
			min_score: 0.75
		consolidation:
			enabled: false
			system_prompt: "test"
			memory_search_limit: 20
	qdrant:
		host: "localhost"
		port: 6334
		vector_size: 1536
access_control:
	blacklist:
		users: []
		guilds: []
		channels: []
operations:
	web:
		port: 8080
		username: "admin"
		password: "test"
		enabled: true
		memories_page_limit: 50
		content_truncation_length: 500
		session_ttl_min: 30
		session_cleanup_interval_min: 5
		stats_query_timeout_sec: 5
		log_default_lines: 100
		log_max_lines: 1000
		log_max_read_bytes: 1048576
	logging:
		level: "info"
		file: "logs/test.log"
		max_size: 100
		max_backups: 3
		max_age: 30
	plugins:
		enabled: true
		plugins_dir: "plugins"
		startup_timeout_sec: 90
		rpc_timeout_sec: 5
		before_send_timeout_sec: 180
		command_timeout_sec: 45
		shutdown_timeout_sec: 5
		disable_timeout_sec: 2
	mcp:
		enabled: false
		servers: []
	operations:
		shutdown_timeout_sec: 300
		cleanup_interval_min: 5
`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	_, err := Load(configPath)
	if err == nil {
		t.Error("Expected error for missing API key, got nil")
	}
}
func TestValidate_InvalidReplyPercentage(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:                      "test",
			BotName:                    "TestBot",
			ReplyPercentage:            0.15,
			ConsolidationTimeoutSec:    300,
			TypingIndicatorIntervalSec: 5,
			LongResponseDelayMs:        500,
			ReplyTruncationLength:      200,
			ImageCacheTTLMin:           60,
			ImageCacheMaxEntries:       100,
			RateLimit:                  RateLimitConfig{ResetPeriodSeconds: 60},
		},
		AI: AIConfig{
			APIBaseURL:     "https://api.openai.com/v1",
			APIKey:         "test",
			Model:          "gpt-4o-mini",
			VisionModel:    "gpt-4o",
			MaxTokens:      1024,
			Temperature:    0.8,
			SystemPrompt:   "test",
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			MaxPaginatedLimit:     100,

			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:           true,
				MemorySearchLimit: 20,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:                      8080,
			Username:                  "admin",
			Password:                  "test",
			Enabled:                   true,
			MemoriesPageLimit:         50,
			ContentTruncationLength:   500,
			SessionTTLMin:             30,
			SessionCleanupIntervalMin: 5,
			StatsQueryTimeoutSec:      5,
			LogDefaultLines:           100,
			LogMaxLines:               1000,
			LogMaxReadBytes:           1048576,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:              true,
			PluginsDir:           "plugins",
			StartupTimeoutSec:    90,
			RPCTimeoutSec:        5,
			BeforeSendTimeoutSec: 180,
			CommandTimeoutSec:    45,
			ShutdownTimeoutSec:   5,
			DisableTimeoutSec:    2,
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for invalid reply_percentage, got nil")
	}
}
func TestValidate_InvalidTemperature(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:                      "test",
			BotName:                    "TestBot",
			ReplyPercentage:            0.15,
			CooldownSeconds:            5,
			MaxResponsesPerMin:         10,
			ConsolidationTimeoutSec:    300,
			TypingIndicatorIntervalSec: 5,
			LongResponseDelayMs:        500,
			ReplyTruncationLength:      200,
			ImageCacheTTLMin:           60,
			ImageCacheMaxEntries:       100,
			RateLimit:                  RateLimitConfig{ResetPeriodSeconds: 60},
		},
		AI: AIConfig{
			APIBaseURL:     "https://api.openai.com/v1",
			APIKey:         "test",
			Model:          "gpt-4o-mini",
			VisionModel:    "gpt-4o",
			MaxTokens:      1024,
			Temperature:    3.0,
			SystemPrompt:   "test",
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			MaxPaginatedLimit:     100,

			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:           true,
				MemorySearchLimit: 20,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:                      8080,
			Username:                  "admin",
			Password:                  "test",
			Enabled:                   true,
			MemoriesPageLimit:         50,
			ContentTruncationLength:   500,
			SessionTTLMin:             30,
			SessionCleanupIntervalMin: 5,
			StatsQueryTimeoutSec:      5,
			LogDefaultLines:           100,
			LogMaxLines:               1000,
			LogMaxReadBytes:           1048576,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:              true,
			PluginsDir:           "plugins",
			StartupTimeoutSec:    90,
			RPCTimeoutSec:        5,
			BeforeSendTimeoutSec: 180,
			CommandTimeoutSec:    45,
			ShutdownTimeoutSec:   5,
			DisableTimeoutSec:    2,
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for invalid temperature, got nil")
	}
}
func TestFormatSystemPrompt(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			BotName: "TestBot",
		},
	}
	cfg.AI.SystemPrompt = "Hello {AuthorName}, I'm {BotName} in {ServerName} on {CurrentDate}"
	result := cfg.FormatSystemPrompt("Alice", "MyServer", "123456789", "987654321")
	if result == "" {
		t.Error("Expected formatted prompt, got empty string")
	}
	if strings.Contains(result, "{AuthorName}") {
		t.Error("{AuthorName} was not replaced")
	}
	if strings.Contains(result, "{BotName}") {
		t.Error("{BotName} was not replaced")
	}
	if strings.Contains(result, "{ServerName}") {
		t.Error("{ServerName} was not replaced")
	}
}
func TestValidate_MissingVisionMode(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:                      "test",
			BotName:                    "TestBot",
			ReplyPercentage:            0.15,
			CooldownSeconds:            5,
			MaxResponsesPerMin:         10,
			ConsolidationTimeoutSec:    300,
			TypingIndicatorIntervalSec: 5,
			LongResponseDelayMs:        500,
			ReplyTruncationLength:      200,
			ImageCacheTTLMin:           60,
			ImageCacheMaxEntries:       100,
			RateLimit:                  RateLimitConfig{ResetPeriodSeconds: 60},
		},
		AI: AIConfig{
			APIBaseURL:   "https://api.openai.com/v1",
			APIKey:       "test",
			Model:        "gpt-4o-mini",
			VisionModel:  "gpt-4o",
			MaxTokens:    1024,
			Temperature:  0.8,
			SystemPrompt: "test",
			Vision: VisionConfig{
				MaxImages: 4,
			},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			MaxPaginatedLimit:     100,

			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:           true,
				MemorySearchLimit: 20,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:                      8080,
			Username:                  "admin",
			Password:                  "test",
			Enabled:                   true,
			MemoriesPageLimit:         50,
			ContentTruncationLength:   500,
			SessionTTLMin:             30,
			SessionCleanupIntervalMin: 5,
			StatsQueryTimeoutSec:      5,
			LogDefaultLines:           100,
			LogMaxLines:               1000,
			LogMaxReadBytes:           1048576,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:              true,
			PluginsDir:           "plugins",
			StartupTimeoutSec:    90,
			RPCTimeoutSec:        5,
			BeforeSendTimeoutSec: 180,
			CommandTimeoutSec:    45,
			ShutdownTimeoutSec:   5,
			DisableTimeoutSec:    2,
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for missing vision.mode, got nil")
	}
	if !strings.Contains(err.Error(), "core.ai.vision.mode is required") {
		t.Errorf("Expected error about vision.mode, got: %v", err)
	}
}
func TestValidate_MissingVisionMaxImages(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:                      "test",
			BotName:                    "TestBot",
			ReplyPercentage:            0.15,
			CooldownSeconds:            5,
			MaxResponsesPerMin:         10,
			ConsolidationTimeoutSec:    300,
			TypingIndicatorIntervalSec: 5,
			LongResponseDelayMs:        500,
			ReplyTruncationLength:      200,
			ImageCacheTTLMin:           60,
			ImageCacheMaxEntries:       100,
			RateLimit:                  RateLimitConfig{ResetPeriodSeconds: 60},
		},
		AI: AIConfig{
			APIBaseURL:   "https://api.openai.com/v1",
			APIKey:       "test",
			Model:        "gpt-4o-mini",
			VisionModel:  "gpt-4o",
			MaxTokens:    1024,
			Temperature:  0.8,
			SystemPrompt: "test",
			Vision: VisionConfig{
				Mode:      VisionModeMultimodal,
				MaxImages: 0,
			},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			MaxPaginatedLimit:     100,

			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:           true,
				MemorySearchLimit: 20,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:                      8080,
			Username:                  "admin",
			Password:                  "test",
			Enabled:                   true,
			MemoriesPageLimit:         50,
			ContentTruncationLength:   500,
			SessionTTLMin:             30,
			SessionCleanupIntervalMin: 5,
			StatsQueryTimeoutSec:      5,
			LogDefaultLines:           100,
			LogMaxLines:               1000,
			LogMaxReadBytes:           1048576,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:              true,
			PluginsDir:           "plugins",
			StartupTimeoutSec:    90,
			RPCTimeoutSec:        5,
			BeforeSendTimeoutSec: 180,
			CommandTimeoutSec:    45,
			ShutdownTimeoutSec:   5,
			DisableTimeoutSec:    2,
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for vision.max_images = 0, got nil")
	}
	if !strings.Contains(err.Error(), "core.ai.vision.max_images") {
		t.Errorf("Expected error about vision.max_images, got: %v", err)
	}
}
func TestValidate_MissingVisionDescriptionPrompt(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:                      "test",
			BotName:                    "TestBot",
			ReplyPercentage:            0.15,
			CooldownSeconds:            5,
			MaxResponsesPerMin:         10,
			ConsolidationTimeoutSec:    300,
			TypingIndicatorIntervalSec: 5,
			LongResponseDelayMs:        500,
			ReplyTruncationLength:      200,
			ImageCacheTTLMin:           60,
			ImageCacheMaxEntries:       100,
			RateLimit:                  RateLimitConfig{ResetPeriodSeconds: 60},
		},
		AI: AIConfig{
			APIBaseURL:   "https://api.openai.com/v1",
			APIKey:       "test",
			Model:        "gpt-4o-mini",
			VisionModel:  "gpt-4o",
			MaxTokens:    1024,
			Temperature:  0.8,
			SystemPrompt: "test",
			Vision: VisionConfig{
				Mode:              VisionModeHybrid,
				MaxImages:         4,
				DescriptionPrompt: "",
			},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			MaxPaginatedLimit:     100,

			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:           true,
				MemorySearchLimit: 20,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:                      8080,
			Username:                  "admin",
			Password:                  "test",
			Enabled:                   true,
			MemoriesPageLimit:         50,
			ContentTruncationLength:   500,
			SessionTTLMin:             30,
			SessionCleanupIntervalMin: 5,
			StatsQueryTimeoutSec:      5,
			LogDefaultLines:           100,
			LogMaxLines:               1000,
			LogMaxReadBytes:           1048576,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:              true,
			PluginsDir:           "plugins",
			StartupTimeoutSec:    90,
			RPCTimeoutSec:        5,
			BeforeSendTimeoutSec: 180,
			CommandTimeoutSec:    45,
			ShutdownTimeoutSec:   5,
			DisableTimeoutSec:    2,
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for missing vision.description_prompt in hybrid mode, got nil")
	}
	if !strings.Contains(err.Error(), "core.ai.vision.description_prompt is required") {
		t.Errorf("Expected error about vision.description_prompt, got: %v", err)
	}
}
func TestValidate_InvalidRetrievalTopK(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:                      "test",
			BotName:                    "TestBot",
			OwnBotID:                   "123",
			ReplyPercentage:            0.15,
			CooldownSeconds:            5,
			MaxResponsesPerMin:         10,
			ConsolidationTimeoutSec:    300,
			TypingIndicatorIntervalSec: 5,
			LongResponseDelayMs:        500,
			ReplyTruncationLength:      200,
			ImageCacheTTLMin:           60,
			ImageCacheMaxEntries:       100,
			RateLimit:                  RateLimitConfig{ResetPeriodSeconds: 60},
		},
		AI: AIConfig{
			APIBaseURL:   "https://api.openai.com/v1",
			APIKey:       "test",
			Model:        "gpt-4o-mini",
			VisionModel:  "gpt-4o",
			MaxTokens:    1024,
			Temperature:  0.8,
			SystemPrompt: "test",
			RetryCount:   1,
			Timeout:      30,
			Vision: VisionConfig{
				Mode:      VisionModeTextOnly,
				MaxImages: 1,
			},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{
			Model:      "text-embedding-3-small",
			RetryCount: 0,
			Timeout:    30,
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			RetryBaseDelayMs: 1000,
			RetryMaxDelayMs:  30000,
			MaxRetries:       3,
			Retrieval: RetrievalConfig{
				TopK:     0,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:           false,
				SystemPrompt:      "extract",
				MemorySearchLimit: 20,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:                      8080,
			Username:                  "admin",
			Password:                  "test",
			Enabled:                   true,
			MemoriesPageLimit:         50,
			ContentTruncationLength:   500,
			SessionTTLMin:             30,
			SessionCleanupIntervalMin: 5,
			StatsQueryTimeoutSec:      5,
			LogDefaultLines:           100,
			LogMaxLines:               1000,
			LogMaxReadBytes:           1048576,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:              true,
			PluginsDir:           "plugins",
			StartupTimeoutSec:    90,
			RPCTimeoutSec:        5,
			BeforeSendTimeoutSec: 180,
			CommandTimeoutSec:    45,
			ShutdownTimeoutSec:   5,
			DisableTimeoutSec:    2,
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err != nil {
		t.Errorf("Expected top_k=0 to be valid when on-demand memory is disabled, got: %v", err)
	}
}
func TestValidate_WebDisabled_DoesNotRequireWebCredentials(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "em", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			RetryBaseDelayMs: 1000,
			RetryMaxDelayMs:  30000,
			MaxRetries:       3,
			Retrieval:        RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:    ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{Host: "h", Port: 1, VectorSize: 1},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("Expected validation to pass when web is disabled, got: %v", err)
	}
}
func TestValidate_PluginsDisabled_DoesNotRequirePluginsDir(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "em", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			RetryBaseDelayMs: 1000,
			RetryMaxDelayMs:  30000,
			MaxRetries:       3,
			Retrieval:        RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:    ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{Host: "h", Port: 1, VectorSize: 1},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false, PluginsDir: ""},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("Expected validation to pass when plugins are disabled, got: %v", err)
	}
}
func TestValidate_MCPEnabled_RequiresValidServerConfig(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "em", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:  QdrantConfig{Host: "h", Port: 1, VectorSize: 1},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false},
		MCP: MCPConfig{
			Enabled: true,
			Servers: []MCPServer{{Name: "", Type: "stdio", Command: ""}},
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("Expected validation error for invalid MCP server config")
	}
	if !strings.Contains(err.Error(), "operations.mcp.servers[0].name is required") {
		t.Fatalf("Expected MCP name validation error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "operations.mcp.servers[0].command is required when type is stdio") {
		t.Fatalf("Expected MCP stdio command validation error, got: %v", err)
	}
}
func TestValidate_MemoryFeaturesDisabled_DoesNotRequireEmbeddingOrQdrant(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("Expected validation to pass with memory features disabled, got: %v", err)
	}
}
func TestValidate_MemoryRetrievalEnabled_RequiresEmbeddingAndQdrant(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("Expected validation to fail when memory retrieval is enabled without embedding/qdrant config")
	}
	if !strings.Contains(err.Error(), "memory_pipeline.embedding.model is required") {
		t.Fatalf("Expected embedding model requirement error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "memory_pipeline.qdrant.host is required") {
		t.Fatalf("Expected qdrant requirement error, got: %v", err)
	}
}
func TestValidate_MemoryEnabled_MissingRetryBaseDelay(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{Model: "text-embedding-3-small", RetryCount: 1, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{Host: "localhost", Port: 6333, VectorSize: 1536},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("Expected validation error for missing memory.retry_base_delay_ms when memory is enabled")
	}
	if !strings.Contains(err.Error(), "memory_pipeline.memory.retry_base_delay_ms") {
		t.Fatalf("Expected error about memory.retry_base_delay_ms, got: %v", err)
	}
}

func TestValidate_EmbeddingVectorSizeRelationCheck(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "text-embedding-3-small", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{Host: "localhost", Port: 6334, VectorSize: 3072},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("Expected vector size relation validation error")
	}
	if !strings.Contains(err.Error(), "memory_pipeline.qdrant.vector_size") {
		t.Fatalf("Expected vector size relation error, got: %v", err)
	}
}
func TestValidate_DecisionEnabledRequiresExplicitCredentials(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
		Decision: DecisionConfig{
			Enabled:        true,
			Model:          "gpt-4o-mini",
			MaxTokens:      64,
			Temperature:    0.1,
			RetryCount:     1,
			Timeout:        10,
			SystemPrompt:   "decide",
			HTTPTimeoutSec: 60,
		},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation failure for missing decision credentials")
	}
	if !strings.Contains(err.Error(), "core.decision.api_base_url is required") {
		t.Fatalf("expected decision.api_base_url validation error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "core.decision.api_key is required") {
		t.Fatalf("expected decision.api_key validation error, got: %v", err)
	}
}
func TestPluginsConfig_DefaultToolTimeoutMs_ParsesCorrectly(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	config := `schema_version: 3
core:
  discord:
    token: "test-token"
    bot_name: "TestBot"
    reply_percentage: 0.15
    cooldown_seconds: 5
    max_responses_per_minute: 10
    consolidation_timeout_sec: 300
    typing_indicator_interval_sec: 5
    long_response_delay_ms: 500
    reply_truncation_length: 200
    image_cache_ttl_min: 60
    image_cache_max_entries: 100
    rate_limit:
      reset_period_seconds: 60
  ai:
    api_base_url: "https://api.openai.com/v1"
    api_key: "test-key"
    model: "gpt-4o-mini"
    vision_model: "gpt-4o"
    max_tokens: 1024
    temperature: 0.8
    retry_count: 1
    timeout: 10
    system_prompt: "test"
    http_timeout_sec: 30
    max_tool_iterations: 5
    max_image_bytes: 10485760
    user_agent: "EZyapper/1.0"
    vision:
      mode: "text_only"
      max_images: 1
memory_pipeline:
  embedding:
    model: "text-embedding-3-small"
    retry_count: 1
    timeout: 10
  memory:
    consolidation_interval: 50
    short_term_limit: 20
    max_paginated_limit: 100
    embedding_cache_max_size: 500
    embedding_cache_ttl_min: 30
    eviction_interval_min: 5
    retry_base_delay_ms: 1000
    retry_max_delay_ms: 30000
    max_retries: 3
    retrieval:
      top_k: 5
      min_score: 0.75
    consolidation:
      enabled: false
      max_messages: 20
      system_prompt: "test"
      memory_search_limit: 20
  qdrant:
    host: "localhost"
    port: 6334
    vector_size: 1536
access_control:
  blacklist:
    users: []
    guilds: []
    channels: []
operations:
  web:
    port: 8080
    username: "admin"
    password: "test"
    enabled: true
    memories_page_limit: 50
    content_truncation_length: 500
    session_ttl_min: 30
    session_cleanup_interval_min: 5
    stats_query_timeout_sec: 5
    log_default_lines: 100
    log_max_lines: 1000
    log_max_read_bytes: 1048576
  logging:
    level: "info"
    file: "logs/test.log"
    max_size: 100
    max_backups: 3
    max_age: 30
  plugins:
    enabled: true
    plugins_dir: "plugins"
    default_tool_timeout_ms: 30000
    startup_timeout_sec: 90
    rpc_timeout_sec: 5
    before_send_timeout_sec: 180
    command_timeout_sec: 45
    shutdown_timeout_sec: 5
    disable_timeout_sec: 2
  mcp:
    enabled: false
    servers: []
  runtime:
    shutdown_timeout_sec: 300
    cleanup_interval_min: 5
`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Expected valid config, got error: %v", err)
	}
	if cfg.Plugins.DefaultToolTimeoutMs != 30000 {
		t.Errorf("Expected DefaultToolTimeoutMs=30000, got %d", cfg.Plugins.DefaultToolTimeoutMs)
	}
}

func TestPluginsConfig_DefaultToolTimeoutMs_OmitsDefaultsToZero(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	config := `schema_version: 3
core:
  discord:
    token: "test-token"
    bot_name: "TestBot"
    reply_percentage: 0.15
    cooldown_seconds: 5
    max_responses_per_minute: 10
    consolidation_timeout_sec: 300
    typing_indicator_interval_sec: 5
    long_response_delay_ms: 500
    reply_truncation_length: 200
    image_cache_ttl_min: 60
    image_cache_max_entries: 100
    rate_limit:
      reset_period_seconds: 60
  ai:
    api_base_url: "https://api.openai.com/v1"
    api_key: "test-key"
    model: "gpt-4o-mini"
    vision_model: "gpt-4o"
    max_tokens: 1024
    temperature: 0.8
    retry_count: 1
    timeout: 10
    system_prompt: "test"
    http_timeout_sec: 30
    max_tool_iterations: 5
    max_image_bytes: 10485760
    user_agent: "EZyapper/1.0"
    vision:
      mode: "text_only"
      max_images: 1
memory_pipeline:
  embedding:
    model: "text-embedding-3-small"
    retry_count: 1
    timeout: 10
  memory:
    consolidation_interval: 50
    short_term_limit: 20
    max_paginated_limit: 100
    embedding_cache_max_size: 500
    embedding_cache_ttl_min: 30
    eviction_interval_min: 5
    retry_base_delay_ms: 1000
    retry_max_delay_ms: 30000
    max_retries: 3
    retrieval:
      top_k: 5
      min_score: 0.75
    consolidation:
      enabled: false
      max_messages: 20
      system_prompt: "test"
      memory_search_limit: 20
  qdrant:
    host: "localhost"
    port: 6334
    vector_size: 1536
access_control:
  blacklist:
    users: []
    guilds: []
    channels: []
operations:
  web:
    port: 8080
    username: "admin"
    password: "test"
    enabled: true
    memories_page_limit: 50
    content_truncation_length: 500
    session_ttl_min: 30
    session_cleanup_interval_min: 5
    stats_query_timeout_sec: 5
    log_default_lines: 100
    log_max_lines: 1000
    log_max_read_bytes: 1048576
  logging:
    level: "info"
    file: "logs/test.log"
    max_size: 100
    max_backups: 3
    max_age: 30
  plugins:
    enabled: true
    plugins_dir: "plugins"
    startup_timeout_sec: 90
    rpc_timeout_sec: 5
    before_send_timeout_sec: 180
    command_timeout_sec: 45
    shutdown_timeout_sec: 5
    disable_timeout_sec: 2
  mcp:
    enabled: false
    servers: []
  runtime:
    shutdown_timeout_sec: 300
    cleanup_interval_min: 5
`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Expected valid config, got error: %v", err)
	}
	if cfg.Plugins.DefaultToolTimeoutMs != 0 {
		t.Errorf("Expected DefaultToolTimeoutMs=0 when omitted, got %d", cfg.Plugins.DefaultToolTimeoutMs)
	}
}

func TestPluginsConfig_DefaultToolTimeoutMs_NegativeClampedToZero(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	config := `schema_version: 3
core:
  discord:
    token: "test-token"
    bot_name: "TestBot"
    reply_percentage: 0.15
    cooldown_seconds: 5
    max_responses_per_minute: 10
    consolidation_timeout_sec: 300
    typing_indicator_interval_sec: 5
    long_response_delay_ms: 500
    reply_truncation_length: 200
    image_cache_ttl_min: 60
    image_cache_max_entries: 100
    rate_limit:
      reset_period_seconds: 60
  ai:
    api_base_url: "https://api.openai.com/v1"
    api_key: "test-key"
    model: "gpt-4o-mini"
    vision_model: "gpt-4o"
    max_tokens: 1024
    temperature: 0.8
    retry_count: 1
    timeout: 10
    system_prompt: "test"
    http_timeout_sec: 30
    max_tool_iterations: 5
    max_image_bytes: 10485760
    user_agent: "EZyapper/1.0"
    vision:
      mode: "text_only"
      max_images: 1
memory_pipeline:
  embedding:
    model: "text-embedding-3-small"
    retry_count: 1
    timeout: 10
  memory:
    consolidation_interval: 50
    short_term_limit: 20
    max_paginated_limit: 100
    embedding_cache_max_size: 500
    embedding_cache_ttl_min: 30
    eviction_interval_min: 5
    retry_base_delay_ms: 1000
    retry_max_delay_ms: 30000
    max_retries: 3
    retrieval:
      top_k: 5
      min_score: 0.75
    consolidation:
      enabled: false
      max_messages: 20
      system_prompt: "test"
      memory_search_limit: 20
  qdrant:
    host: "localhost"
    port: 6334
    vector_size: 1536
access_control:
  blacklist:
    users: []
    guilds: []
    channels: []
operations:
  web:
    port: 8080
    username: "admin"
    password: "test"
    enabled: true
    memories_page_limit: 50
    content_truncation_length: 500
    session_ttl_min: 30
    session_cleanup_interval_min: 5
    stats_query_timeout_sec: 5
    log_default_lines: 100
    log_max_lines: 1000
    log_max_read_bytes: 1048576
  logging:
    level: "info"
    file: "logs/test.log"
    max_size: 100
    max_backups: 3
    max_age: 30
  plugins:
    enabled: true
    plugins_dir: "plugins"
    default_tool_timeout_ms: -1
    startup_timeout_sec: 90
    rpc_timeout_sec: 5
    before_send_timeout_sec: 180
    command_timeout_sec: 45
    shutdown_timeout_sec: 5
    disable_timeout_sec: 2
  mcp:
    enabled: false
    servers: []
  runtime:
    shutdown_timeout_sec: 300
    cleanup_interval_min: 5
`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected validation error for negative plugins.default_tool_timeout_ms")
	}
	if !strings.Contains(err.Error(), "operations.plugins.default_tool_timeout_ms must be non-negative") {
		t.Fatalf("expected default_tool_timeout_ms error, got: %v", err)
	}
}

func TestValidate_DecisionEnabledWithExplicitCredentials(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
		Decision: DecisionConfig{
			Enabled:        true,
			Model:          "gpt-4o-mini",
			APIBaseURL:     "https://decision.example.com/v1",
			APIKey:         "decision-key",
			MaxTokens:      64,
			Temperature:    0.1,
			RetryCount:     1,
			Timeout:        10,
			SystemPrompt:   "decide",
			HTTPTimeoutSec: 60,
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("expected validation success with explicit decision credentials, got: %v", err)
	}
}

func TestValidate_ConsolidationEnabled_RequiresOwnBotID(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "text-embedding-3-small", RetryCount: 1, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: true, SystemPrompt: "sp", MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{Host: "localhost", Port: 6333, VectorSize: 1536},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation failure when consolidation enabled but discord.own_bot_id is empty")
	}
	if !strings.Contains(err.Error(), "core.discord.own_bot_id") {
		t.Fatalf("expected error about discord.own_bot_id, got: %v", err)
	}
}

func TestValidate_ConsolidationDisabled_DoesNotRequireOwnBotID(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("expected validation success when consolidation disabled even without discord.own_bot_id, got: %v", err)
	}
}

func TestValidateAI_MissingHTTPTimeoutSec(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			// HTTPTimeoutSec intentionally omitted
			MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing ai.http_timeout_sec")
	}
	if !strings.Contains(err.Error(), "core.ai.http_timeout_sec must be greater than 0") {
		t.Fatalf("expected ai.http_timeout_sec error, got: %v", err)
	}
}

func TestValidateAI_MissingMaxToolIterations(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
			// MaxToolIterations intentionally omitted
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing ai.max_tool_iterations")
	}
	if !strings.Contains(err.Error(), "core.ai.max_tool_iterations must be greater than 0") {
		t.Fatalf("expected ai.max_tool_iterations error, got: %v", err)
	}
}

func TestValidate_VisionMaxTokensNegative(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeHybrid, MaxImages: 1, DescriptionPrompt: "desc", MaxTokens: -1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "text-embedding-3-small", RetryCount: 1, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{Host: "localhost", Port: 6333, VectorSize: 1536},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative ai.vision.max_tokens")
	}
	if !strings.Contains(err.Error(), "core.ai.vision.max_tokens must be greater than 0") {
		t.Fatalf("expected ai.vision.max_tokens error, got: %v", err)
	}
}

func TestValidate_EmbeddingTimeoutZero(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "text-embedding-3-small", RetryCount: 1, Timeout: 0},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,

			Retrieval:     RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation: ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:     QdrantConfig{Host: "localhost", Port: 6333, VectorSize: 1536},
		Web:        WebConfig{Enabled: false},
		Logging:    LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins:    PluginsConfig{Enabled: false},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for embedding.timeout = 0 when memory is enabled")
	}
	if !strings.Contains(err.Error(), "memory_pipeline.embedding.timeout must be greater than 0") {
		t.Fatalf("expected embedding.timeout error, got: %v", err)
	}
}

func TestDiscordConfig_ChunkSplitDelaySec_NotExist(t *testing.T) {
	dcType := reflect.TypeOf(DiscordConfig{})
	for i := range dcType.NumField() {
		f := dcType.Field(i)
		yamlTag := f.Tag.Get("yaml")
		mapTag := f.Tag.Get("mapstructure")
		if yamlTag == "chunk_split_delay_sec" || mapTag == "chunk_split_delay_sec" {
			t.Fatalf("DiscordConfig field %q still has chunk_split_delay_sec tag (yaml=%q, mapstructure=%q)",
				f.Name, yamlTag, mapTag)
		}
	}
}

func TestConfigSchema_HasRequiredWebFields(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "examples", "config.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("Failed to read schema file: %v", err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Failed to parse schema JSON: %v", err)
	}

	// Navigate: properties.operations.properties.web.allOf[0].then.required
	props, _ := root["properties"].(map[string]interface{})
	ops, _ := props["operations"].(map[string]interface{})
	opsProps, _ := ops["properties"].(map[string]interface{})
	web, _ := opsProps["web"].(map[string]interface{})

	// web.required should only have "enabled"
	reqRaw, _ := web["required"].([]interface{})
	if len(reqRaw) != 1 || reqRaw[0].(string) != "enabled" {
		t.Errorf("expected web.required to be [\"enabled\"], got %v", reqRaw)
	}

	// Conditional fields are in allOf[0].then.required
	allOf, _ := web["allOf"].([]interface{})
	if len(allOf) == 0 {
		t.Fatalf("expected web.allOf to contain conditional requirements")
	}
	thenBlock, _ := allOf[0].(map[string]interface{})["then"].(map[string]interface{})
	thenReq, _ := thenBlock["required"].([]interface{})

	var thenRequiredFields []string
	for _, r := range thenReq {
		s, ok := r.(string)
		if !ok {
			t.Fatalf("Expected string in allOf.then.required array, got %T", r)
		}
		thenRequiredFields = append(thenRequiredFields, s)
	}

	checkField := func(name string) {
		for _, f := range thenRequiredFields {
			if f == name {
				return
			}
		}
		t.Errorf("Required field %q not found in operations.web.allOf[0].then.required array", name)
	}

	checkField("content_truncation_length")
	checkField("stats_query_timeout_sec")
}

func TestPluginManifestSchema_ValidatesExistingFiles(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "examples", "plugin.schema.json")
	if _, err := os.ReadFile(schemaPath); err != nil {
		t.Fatalf("Failed to read schema file: %v", err)
	}

	manifests := []struct {
		name string
		path string
	}{
		{"datetime-zig", filepath.Join("..", "..", "examples", "plugins", "datetime-zig", "plugin.json")},
		{"datetime-java", filepath.Join("..", "..", "examples", "plugins", "datetime-java", "plugin.json")},
		{"clank-o-meter-zig", filepath.Join("..", "..", "examples", "plugins", "clank-o-meter-zig", "plugin.json")},
		{"systemspec-c", filepath.Join("..", "..", "examples", "plugins", "systemspec-c", "plugin.json")},
	}

	for _, m := range manifests {
		data, err := os.ReadFile(m.path)
		if err != nil {
			t.Errorf("Failed to read %s manifest: %v", m.name, err)
			continue
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Errorf("Failed to parse %s manifest: %v", m.name, err)
			continue
		}

		errs := validatePluginManifest(parsed)
		for _, e := range errs {
			t.Errorf("%s: %s", m.name, e)
		}
	}
}

func TestAllSchemas_ValidateAgainstExamples(t *testing.T) {
	pluginDirs := []string{
		"datetime-zig", "datetime-java",
		"clank-o-meter-zig", "systemspec-c",
	}
	for _, dir := range pluginDirs {
		path := filepath.Join("..", "..", "examples", "plugins", dir, "plugin.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("plugin.json [%s]: read error: %v", dir, err)
			continue
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Errorf("plugin.json [%s]: not valid JSON: %v", dir, err)
		}
	}

	pluginConfigDirs := []string{
		"antispam-go", "datetime-go", "clank-o-meter-go",
		"systemspec-go", "openai-tts-go", "kimi-tools-go", "emote-go",
	}
	for _, dir := range pluginConfigDirs {
		path := filepath.Join("..", "..", "examples", "plugins", dir, "config.yaml.example")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("config.yaml.example [%s]: read error: %v", dir, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("config.yaml.example [%s]: file is empty", dir)
		}
	}

	schemaFiles := []string{
		"examples/plugin.schema.json",
		"examples/config.schema.json",
	}
	for _, rel := range schemaFiles {
		path := filepath.Join("..", "..", rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("schema [%s]: read error: %v", rel, err)
			continue
		}
		var parsed interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Errorf("schema [%s]: not valid JSON: %v", rel, err)
		}
	}
}

func TestPluginManifestSchema_RejectsInvalid(t *testing.T) {
	t.Run("missing runtime", func(t *testing.T) {
		manifest := map[string]interface{}{
			"name":        "test",
			"version":     "1.0.0",
			"author":      "test",
			"description": "test",
			"priority":    10,
			"tools":       []interface{}{},
		}
		errs := validatePluginManifest(manifest)
		var found bool
		for _, e := range errs {
			if strings.Contains(e, "runtime") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error about missing runtime, got: %v", errs)
		}
	})

	t.Run("negative timeout_ms", func(t *testing.T) {
		manifest := map[string]interface{}{
			"runtime":     "command",
			"name":        "test",
			"version":     "1.0.0",
			"author":      "test",
			"description": "test",
			"priority":    10,
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "tool1",
					"description": "a tool",
					"parameters":  map[string]interface{}{},
					"timeout_ms":  -1,
				},
			},
		}
		errs := validatePluginManifest(manifest)
		var found bool
		for _, e := range errs {
			if strings.Contains(e, "timeout_ms") && strings.Contains(e, ">= 0") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error about negative timeout_ms, got: %v", errs)
		}
	})

	t.Run("invalid runtime", func(t *testing.T) {
		manifest := map[string]interface{}{
			"runtime":     "invalid",
			"name":        "test",
			"version":     "1.0.0",
			"author":      "test",
			"description": "test",
			"priority":    10,
			"tools":       []interface{}{},
		}
		errs := validatePluginManifest(manifest)
		var found bool
		for _, e := range errs {
			if strings.Contains(e, "runtime") && strings.Contains(e, "enum") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error about invalid runtime, got: %v", errs)
		}
	})
}

func TestValidate_PluginsDefaultToolTimeoutMsNegative(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "em", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,
			RetryBaseDelayMs:      1000,
			RetryMaxDelayMs:       30000,
			MaxRetries:            3,
			Retrieval:             RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:  QdrantConfig{Host: "h", Port: 1, VectorSize: 1},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{
			Enabled:              true,
			PluginsDir:           "/opt/plugins",
			DefaultToolTimeoutMs: -1,
			StartupTimeoutSec:    30,
			RPCTimeoutSec:        30,
			BeforeSendTimeoutSec: 30,
			CommandTimeoutSec:    30,
			ShutdownTimeoutSec:   30,
			DisableTimeoutSec:    30,
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative plugins.default_tool_timeout_ms")
	}
	if !strings.Contains(err.Error(), "operations.plugins.default_tool_timeout_ms must be non-negative") {
		t.Fatalf("expected default_tool_timeout_ms error, got: %v", err)
	}
}

func TestValidate_PluginsDefaultToolTimeoutMsPositive_NoError(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1, ConsolidationTimeoutSec: 300, TypingIndicatorIntervalSec: 5, LongResponseDelayMs: 500, ReplyTruncationLength: 200, ImageCacheTTLMin: 60, ImageCacheMaxEntries: 100, RateLimit: RateLimitConfig{ResetPeriodSeconds: 1}},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision:         VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
			HTTPTimeoutSec: 30, MaxToolIterations: 5, MaxImageBytes: 10485760, UserAgent: "EZyapper/1.0",
		},
		Embedding: EmbeddingConfig{Model: "em", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			MaxPaginatedLimit:     100,
			RetryBaseDelayMs:      1000,
			RetryMaxDelayMs:       30000,
			MaxRetries:            3,
			Retrieval:             RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false, MemorySearchLimit: 20},
		},
		Qdrant:  QdrantConfig{Host: "h", Port: 1, VectorSize: 1},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{
			Enabled:              true,
			PluginsDir:           "/opt/plugins",
			DefaultToolTimeoutMs: 5000,
			StartupTimeoutSec:    30,
			RPCTimeoutSec:        30,
			BeforeSendTimeoutSec: 30,
			CommandTimeoutSec:    30,
			ShutdownTimeoutSec:   30,
			DisableTimeoutSec:    30,
		},
		Operations: OperationsConfig{ShutdownTimeoutSec: 300, CleanupIntervalMin: 5},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("expected validation to pass with positive default_tool_timeout_ms, got: %v", err)
	}
}
