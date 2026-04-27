package config
import (
	"os"
	"path/filepath"
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
memory_pipeline:
	embedding:
		model: "text-embedding-3-small"
		retry_count: 1
		timeout: 10
	memory:
		consolidation_interval: 50
		short_term_limit: 20
		retrieval:
			top_k: 5
			min_score: 0.75
		consolidation:
			enabled: false
			max_messages: 20
			system_prompt: "test"
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
	logging:
		level: "info"
		file: "logs/test.log"
		max_size: 100
		max_backups: 3
		max_age: 30
	plugins:
		enabled: true
		plugins_dir: "plugins"
	mcp:
		enabled: false
		servers: []
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
	ai:
		api_base_url: "https://api.openai.com/v1"
		model: "gpt-4o-mini"
		vision_model: "gpt-4o"
		max_tokens: 1024
		temperature: 0.8
		retry_count: 1
		timeout: 10
		system_prompt: "test"
memory_pipeline:
	embedding:
		model: "text-embedding-3-small"
		retry_count: 1
		timeout: 10
	memory:
		consolidation_interval: 50
		short_term_limit: 20
		retrieval:
			top_k: 5
			min_score: 0.75
		consolidation:
			enabled: false
			max_messages: 20
			system_prompt: "test"
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
	logging:
		level: "info"
		file: "logs/test.log"
		max_size: 100
		max_backups: 3
		max_age: 30
	plugins:
		enabled: true
		plugins_dir: "plugins"
	mcp:
		enabled: false
		servers: []
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
			Token:              "test",
			BotName:            "TestBot",
			ReplyPercentage:    1.5,
			CooldownSeconds:    5,
			MaxResponsesPerMin: 10,
		},
		AI: AIConfig{
			APIBaseURL:   "https://api.openai.com/v1",
			APIKey:       "test",
			Model:        "gpt-4o-mini",
			VisionModel:  "gpt-4o",
			MaxTokens:    1024,
			Temperature:  0.8,
			SystemPrompt: "test",
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:     true,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:     8080,
			Username: "admin",
			Password: "test",
			Enabled:  true,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:    true,
			PluginsDir: "plugins",
		},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for invalid reply_percentage, got nil")
	}
}
func TestValidate_InvalidTemperature(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:              "test",
			BotName:            "TestBot",
			ReplyPercentage:    0.15,
			CooldownSeconds:    5,
			MaxResponsesPerMin: 10,
		},
		AI: AIConfig{
			APIBaseURL:   "https://api.openai.com/v1",
			APIKey:       "test",
			Model:        "gpt-4o-mini",
			VisionModel:  "gpt-4o",
			MaxTokens:    1024,
			Temperature:  3.0,
			SystemPrompt: "test",
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:     true,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:     8080,
			Username: "admin",
			Password: "test",
			Enabled:  true,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:    true,
			PluginsDir: "plugins",
		},
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
			Token:              "test",
			BotName:            "TestBot",
			ReplyPercentage:    0.15,
			CooldownSeconds:    5,
			MaxResponsesPerMin: 10,
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
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:     true,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:     8080,
			Username: "admin",
			Password: "test",
			Enabled:  true,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:    true,
			PluginsDir: "plugins",
		},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for missing vision.mode, got nil")
	}
	if !strings.Contains(err.Error(), "ai.vision.mode is required") {
		t.Errorf("Expected error about vision.mode, got: %v", err)
	}
}
func TestValidate_MissingVisionMaxImages(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:              "test",
			BotName:            "TestBot",
			ReplyPercentage:    0.15,
			CooldownSeconds:    5,
			MaxResponsesPerMin: 10,
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
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:     true,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:     8080,
			Username: "admin",
			Password: "test",
			Enabled:  true,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:    true,
			PluginsDir: "plugins",
		},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for vision.max_images = 0, got nil")
	}
	if !strings.Contains(err.Error(), "ai.vision.max_images") {
		t.Errorf("Expected error about vision.max_images, got: %v", err)
	}
}
func TestValidate_MissingVisionDescriptionPrompt(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:              "test",
			BotName:            "TestBot",
			ReplyPercentage:    0.15,
			CooldownSeconds:    5,
			MaxResponsesPerMin: 10,
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
		},
		Embedding: EmbeddingConfig{
			Model: "text-embedding-3-small",
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			Retrieval: RetrievalConfig{
				TopK:     5,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:     true,
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:     8080,
			Username: "admin",
			Password: "test",
			Enabled:  true,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:    true,
			PluginsDir: "plugins",
		},
	}
	err := validate(cfg)
	if err == nil {
		t.Error("Expected error for missing vision.description_prompt in hybrid mode, got nil")
	}
	if !strings.Contains(err.Error(), "ai.vision.description_prompt is required") {
		t.Errorf("Expected error about vision.description_prompt, got: %v", err)
	}
}
func TestValidate_InvalidRetrievalTopK(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{
			Token:              "test",
			BotName:            "TestBot",
			ReplyPercentage:    0.15,
			CooldownSeconds:    5,
			MaxResponsesPerMin: 10,
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
		},
		Embedding: EmbeddingConfig{
			Model:      "text-embedding-3-small",
			RetryCount: 0,
			Timeout:    30,
		},
		Memory: MemoryConfig{
			ConsolidationInterval: 50,
			ShortTermLimit:        20,
			Retrieval: RetrievalConfig{
				TopK:     0,
				MinScore: 0.75,
			},
			Consolidation: ConsolidationConfig{
				Enabled:      true,
				SystemPrompt: "extract",
			},
		},
		Qdrant: QdrantConfig{
			Host:       "localhost",
			Port:       6334,
			VectorSize: 1536,
		},
		Web: WebConfig{
			Port:     8080,
			Username: "admin",
			Password: "test",
			Enabled:  true,
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "logs/test.log",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
		},
		Plugins: PluginsConfig{
			Enabled:    true,
			PluginsDir: "plugins",
		},
	}
	err := validate(cfg)
	if err != nil {
		t.Errorf("Expected top_k=0 to be valid when on-demand memory is disabled, got: %v", err)
	}
}
func TestValidate_WebDisabled_DoesNotRequireWebCredentials(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{Model: "em", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false},
		},
		Qdrant:  QdrantConfig{Host: "h", Port: 1, VectorSize: 1},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("Expected validation to pass when web is disabled, got: %v", err)
	}
}
func TestValidate_PluginsDisabled_DoesNotRequirePluginsDir(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{Model: "em", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false},
		},
		Qdrant:  QdrantConfig{Host: "h", Port: 1, VectorSize: 1},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false, PluginsDir: ""},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("Expected validation to pass when plugins are disabled, got: %v", err)
	}
}
func TestValidate_MCPEnabled_RequiresValidServerConfig(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{Model: "em", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false},
		},
		Qdrant:  QdrantConfig{Host: "h", Port: 1, VectorSize: 1},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false},
		MCP: MCPConfig{
			Enabled: true,
			Servers: []MCPServer{{Name: "", Type: "stdio", Command: ""}},
		},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("Expected validation error for invalid MCP server config")
	}
	if !strings.Contains(err.Error(), "mcp.servers[0].name is required") {
		t.Fatalf("Expected MCP name validation error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "mcp.servers[0].command is required when type is stdio") {
		t.Fatalf("Expected MCP stdio command validation error, got: %v", err)
	}
}
func TestValidate_MemoryFeaturesDisabled_DoesNotRequireEmbeddingOrQdrant(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false},
		},
		Qdrant:  QdrantConfig{},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("Expected validation to pass with memory features disabled, got: %v", err)
	}
}
func TestValidate_MemoryRetrievalEnabled_RequiresEmbeddingAndQdrant(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false},
		},
		Qdrant:  QdrantConfig{},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("Expected validation to fail when memory retrieval is enabled without embedding/qdrant config")
	}
	if !strings.Contains(err.Error(), "embedding.model is required") {
		t.Fatalf("Expected embedding model requirement error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qdrant.host is required") {
		t.Fatalf("Expected qdrant requirement error, got: %v", err)
	}
}
func TestValidate_EmbeddingVectorSizeRelationCheck(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{Model: "text-embedding-3-small", RetryCount: 0, Timeout: 1},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             RetrievalConfig{TopK: 1, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false},
		},
		Qdrant:  QdrantConfig{Host: "localhost", Port: 6334, VectorSize: 3072},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("Expected vector size relation validation error")
	}
	if !strings.Contains(err.Error(), "qdrant.vector_size") {
		t.Fatalf("Expected vector size relation error, got: %v", err)
	}
}
func TestValidate_DecisionEnabledRequiresExplicitCredentials(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false},
		},
		Qdrant:  QdrantConfig{},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false},
		Decision: DecisionConfig{
			Enabled:         true,
			Model:           "gpt-4o-mini",
			MaxTokens:       64,
			Temperature:     0.1,
			RetryCount:      1,
			Timeout:         10,
			SystemPrompt:    "decide",
		},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation failure for missing decision credentials")
	}
	if !strings.Contains(err.Error(), "decision.api_base_url is required") {
		t.Fatalf("expected decision.api_base_url validation error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "decision.api_key is required") {
		t.Fatalf("expected decision.api_key validation error, got: %v", err)
	}
}
func TestValidate_DecisionEnabledWithExplicitCredentials(t *testing.T) {
	cfg := &Config{
		Discord: DiscordConfig{Token: "t", BotName: "b", ReplyPercentage: 0.1, CooldownSeconds: 1, MaxResponsesPerMin: 1},
		AI: AIConfig{
			APIBaseURL: "https://api.example.com/v1",
			APIKey:     "k", Model: "m", VisionModel: "vm", MaxTokens: 1, Temperature: 0.1,
			SystemPrompt: "sp", RetryCount: 1, Timeout: 1,
			Vision: VisionConfig{Mode: VisionModeTextOnly, MaxImages: 1},
		},
		Embedding: EmbeddingConfig{},
		Memory: MemoryConfig{
			ConsolidationInterval: 1,
			ShortTermLimit:        1,
			Retrieval:             RetrievalConfig{TopK: 0, MinScore: 0.5},
			Consolidation:         ConsolidationConfig{Enabled: false},
		},
		Qdrant:  QdrantConfig{},
		Web:     WebConfig{Enabled: false},
		Logging: LoggingConfig{Level: "info", File: "f.log", MaxSize: 1, MaxBackups: 1, MaxAge: 1},
		Plugins: PluginsConfig{Enabled: false},
		Decision: DecisionConfig{
			Enabled:         true,
			Model:           "gpt-4o-mini",
			APIBaseURL:      "https://decision.example.com/v1",
			APIKey:          "decision-key",
			MaxTokens:       64,
			Temperature:     0.1,
			RetryCount:      1,
			Timeout:         10,
			SystemPrompt:    "decide",
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("expected validation success with explicit decision credentials, got: %v", err)
	}
}
