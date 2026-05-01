// Package config provides configuration management using Viper
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

// Config represents the complete application configuration
type Config struct {
	Discord    DiscordConfig    `mapstructure:"discord" yaml:"discord"`
	AI         AIConfig         `mapstructure:"ai" yaml:"ai"`
	Embedding  EmbeddingConfig  `mapstructure:"embedding" yaml:"embedding"`
	Memory     MemoryConfig     `mapstructure:"memory" yaml:"memory"`
	Web        WebConfig        `mapstructure:"web" yaml:"web"`
	Logging    LoggingConfig    `mapstructure:"logging" yaml:"logging"`
	Qdrant     QdrantConfig     `mapstructure:"qdrant" yaml:"qdrant"`
	Blacklist  BlacklistConfig  `mapstructure:"blacklist" yaml:"blacklist"`
	Whitelist  WhitelistConfig  `mapstructure:"whitelist" yaml:"whitelist"`
	Plugins    PluginsConfig    `mapstructure:"plugins" yaml:"plugins"`
	MCP        MCPConfig        `mapstructure:"mcp" yaml:"mcp"`
	Decision   DecisionConfig   `mapstructure:"decision" yaml:"decision"`
	Operations OperationsConfig `mapstructure:"operations" yaml:"operations"`

	configPath string `yaml:"-"`
}

const currentConfigSchemaVersion = 3

type fileConfig struct {
	SchemaVersion int                 `mapstructure:"schema_version" yaml:"schema_version"`
	Core          coreSection         `mapstructure:"core" yaml:"core"`
	MemoryPipe    memoryPipelineGroup `mapstructure:"memory_pipeline" yaml:"memory_pipeline"`
	Access        accessControlGroup  `mapstructure:"access_control" yaml:"access_control"`
	Operations    operationsGroup     `mapstructure:"operations" yaml:"operations"`
}

type coreSection struct {
	Discord  DiscordConfig  `mapstructure:"discord" yaml:"discord"`
	AI       AIConfig       `mapstructure:"ai" yaml:"ai"`
	Decision DecisionConfig `mapstructure:"decision" yaml:"decision"`
}

type memoryPipelineGroup struct {
	Embedding EmbeddingConfig `mapstructure:"embedding" yaml:"embedding"`
	Memory    MemoryConfig    `mapstructure:"memory" yaml:"memory"`
	Qdrant    QdrantConfig    `mapstructure:"qdrant" yaml:"qdrant"`
}

type accessControlGroup struct {
	Blacklist BlacklistConfig `mapstructure:"blacklist" yaml:"blacklist"`
	Whitelist WhitelistConfig `mapstructure:"whitelist" yaml:"whitelist"`
}

type operationsGroup struct {
	Web     WebConfig        `mapstructure:"web" yaml:"web"`
	Logging LoggingConfig    `mapstructure:"logging" yaml:"logging"`
	Plugins PluginsConfig    `mapstructure:"plugins" yaml:"plugins"`
	MCP     MCPConfig        `mapstructure:"mcp" yaml:"mcp"`
	Ops     OperationsConfig `mapstructure:"runtime" yaml:"runtime"`
}

// OperationsConfig holds operational runtime settings.
type OperationsConfig struct {
	ShutdownTimeoutSec int `mapstructure:"shutdown_timeout_sec" yaml:"shutdown_timeout_sec"`
	CleanupIntervalMin int `mapstructure:"cleanup_interval_min" yaml:"cleanup_interval_min"`
}

func (f fileConfig) toRuntimeConfig() Config {
	return Config{
		Discord:    f.Core.Discord,
		AI:         f.Core.AI,
		Decision:   f.Core.Decision,
		Embedding:  f.MemoryPipe.Embedding,
		Memory:     f.MemoryPipe.Memory,
		Qdrant:     f.MemoryPipe.Qdrant,
		Blacklist:  f.Access.Blacklist,
		Whitelist:  f.Access.Whitelist,
		Web:        f.Operations.Web,
		Logging:    f.Operations.Logging,
		Plugins:    f.Operations.Plugins,
		MCP:        f.Operations.MCP,
		Operations: f.Operations.Ops,
	}
}

func toFileConfig(cfg *Config) fileConfig {
	return fileConfig{
		SchemaVersion: currentConfigSchemaVersion,
		Core: coreSection{
			Discord:  cfg.Discord,
			AI:       cfg.AI,
			Decision: cfg.Decision,
		},
		MemoryPipe: memoryPipelineGroup{
			Embedding: cfg.Embedding,
			Memory:    cfg.Memory,
			Qdrant:    cfg.Qdrant,
		},
		Access: accessControlGroup{
			Blacklist: cfg.Blacklist,
			Whitelist: cfg.Whitelist,
		},
		Operations: operationsGroup{
			Web:     cfg.Web,
			Logging: cfg.Logging,
			Plugins: cfg.Plugins,
			MCP:     cfg.MCP,
			Ops:     cfg.Operations,
		},
	}
}

// DecisionConfig holds LLM-based reply decision settings
type DecisionConfig struct {
	Enabled        bool                   `mapstructure:"enabled" yaml:"enabled"`
	Model          string                 `mapstructure:"model" yaml:"model"`
	APIBaseURL     string                 `mapstructure:"api_base_url" yaml:"api_base_url"`
	APIKey         string                 `mapstructure:"api_key" yaml:"api_key"`
	MaxTokens      int                    `mapstructure:"max_tokens" yaml:"max_tokens"`
	Temperature    float32                `mapstructure:"temperature" yaml:"temperature"`
	RetryCount     int                    `mapstructure:"retry_count" yaml:"retry_count"`
	Timeout        int                    `mapstructure:"timeout" yaml:"timeout"`
	SystemPrompt   string                 `mapstructure:"system_prompt" yaml:"system_prompt"`
	ExtraParams    map[string]interface{} `mapstructure:"extra_params" yaml:"extra_params"`
	HTTPTimeoutSec int                    `mapstructure:"http_timeout_sec" yaml:"http_timeout_sec"`
}

// RateLimitConfig holds per-user rate limiting window settings.
type RateLimitConfig struct {
	ResetPeriodSeconds int `mapstructure:"reset_period_seconds" yaml:"reset_period_seconds"`
}

// DiscordConfig holds Discord bot specific settings
type DiscordConfig struct {
	Token                      string          `mapstructure:"token" yaml:"token"`
	BotName                    string          `mapstructure:"bot_name" yaml:"bot_name"`
	OwnBotID                   string          `mapstructure:"own_bot_id" yaml:"own_bot_id"` // Bot's own ID to distinguish from other bots
	ReplyPercentage            float64         `mapstructure:"reply_percentage" yaml:"reply_percentage"`
	CooldownSeconds            int             `mapstructure:"cooldown_seconds" yaml:"cooldown_seconds"`
	MaxResponsesPerMin         int             `mapstructure:"max_responses_per_minute" yaml:"max_responses_per_minute"`
	RateLimit                  RateLimitConfig `mapstructure:"rate_limit" yaml:"rate_limit"`
	ReplyToBots                bool            `mapstructure:"reply_to_bots" yaml:"reply_to_bots"`
	ConsolidationTimeoutSec    int             `mapstructure:"consolidation_timeout_sec" yaml:"consolidation_timeout_sec"`
	TypingIndicatorIntervalSec int             `mapstructure:"typing_indicator_interval_sec" yaml:"typing_indicator_interval_sec"`
	LongResponseDelayMs        int             `mapstructure:"long_response_delay_ms" yaml:"long_response_delay_ms"`
	ReplyTruncationLength      int             `mapstructure:"reply_truncation_length" yaml:"reply_truncation_length"`
	ImageCacheTTLMin           int             `mapstructure:"image_cache_ttl_min" yaml:"image_cache_ttl_min"`
	ImageCacheMaxEntries       int             `mapstructure:"image_cache_max_entries" yaml:"image_cache_max_entries"`
}

// AIConfig holds AI/LLM settings for chat
type AIConfig struct {
	APIBaseURL              string                 `mapstructure:"api_base_url" yaml:"api_base_url"`
	APIKey                  string                 `mapstructure:"api_key" yaml:"api_key"`
	Model                   string                 `mapstructure:"model" yaml:"model"`
	VisionModel             string                 `mapstructure:"vision_model" yaml:"vision_model"`
	VisionBase64            bool                   `mapstructure:"vision_base64" yaml:"vision_base64"`
	Vision                  VisionConfig           `mapstructure:"vision" yaml:"vision"`
	MaxTokens               int                    `mapstructure:"max_tokens" yaml:"max_tokens"`
	Temperature             float32                `mapstructure:"temperature" yaml:"temperature"`
	RetryCount              int                    `mapstructure:"retry_count" yaml:"retry_count"`
	Timeout                 int                    `mapstructure:"timeout" yaml:"timeout"`
	SystemPrompt            string                 `mapstructure:"system_prompt" yaml:"system_prompt"`
	ExtraParams             map[string]interface{} `mapstructure:"extra_params" yaml:"extra_params"`
	HTTPTimeoutSec          int                    `mapstructure:"http_timeout_sec" yaml:"http_timeout_sec"`
	MaxToolIterations       int                    `mapstructure:"max_tool_iterations" yaml:"max_tool_iterations"`
	MaxImageBytes           int                    `mapstructure:"max_image_bytes" yaml:"max_image_bytes"`
	UserAgent               string                 `mapstructure:"user_agent" yaml:"user_agent"`
	RequireImageContentType bool                   `mapstructure:"require_image_content_type" yaml:"require_image_content_type"`
}

// EmbeddingConfig holds settings for embedding generation
type EmbeddingConfig struct {
	APIBaseURL  string                 `mapstructure:"api_base_url" yaml:"api_base_url"`
	APIKey      string                 `mapstructure:"api_key" yaml:"api_key"`
	Model       string                 `mapstructure:"model" yaml:"model"`
	RetryCount  int                    `mapstructure:"retry_count" yaml:"retry_count"`
	Timeout     int                    `mapstructure:"timeout" yaml:"timeout"`
	ExtraParams map[string]interface{} `mapstructure:"extra_params" yaml:"extra_params"`
}

type MemoryConfig struct {
	ConsolidationInterval int                 `mapstructure:"consolidation_interval" yaml:"consolidation_interval"`
	ShortTermLimit        int                 `mapstructure:"short_term_limit" yaml:"short_term_limit"`
	MaxPaginatedLimit     int                 `mapstructure:"max_paginated_limit" yaml:"max_paginated_limit"`
	RetryBaseDelayMs      int                 `mapstructure:"retry_base_delay_ms" yaml:"retry_base_delay_ms"`
	RetryMaxDelayMs       int                 `mapstructure:"retry_max_delay_ms" yaml:"retry_max_delay_ms"`
	MaxRetries            int                 `mapstructure:"max_retries" yaml:"max_retries"`
	Retrieval             RetrievalConfig     `mapstructure:"retrieval" yaml:"retrieval"`
	Consolidation         ConsolidationConfig `mapstructure:"consolidation" yaml:"consolidation"`
}

type ConsolidationConfig struct {
	Enabled           bool                   `mapstructure:"enabled" yaml:"enabled"`
	Model             string                 `mapstructure:"model" yaml:"model"`
	APIBaseURL        string                 `mapstructure:"api_base_url" yaml:"api_base_url"`
	APIKey            string                 `mapstructure:"api_key" yaml:"api_key"`
	MaxTokens         int                    `mapstructure:"max_tokens" yaml:"max_tokens"`
	Temperature       float32                `mapstructure:"temperature" yaml:"temperature"`
	RetryCount        int                    `mapstructure:"retry_count" yaml:"retry_count"`
	Timeout           int                    `mapstructure:"timeout" yaml:"timeout"`
	VisionModel       string                 `mapstructure:"vision_model" yaml:"vision_model"`
	VisionBase64      bool                   `mapstructure:"vision_base64" yaml:"vision_base64"`
	VisionAPIBaseURL  string                 `mapstructure:"vision_api_base_url" yaml:"vision_api_base_url"`
	VisionAPIKey      string                 `mapstructure:"vision_api_key" yaml:"vision_api_key"`
	VisionMaxTokens   int                    `mapstructure:"vision_max_tokens" yaml:"vision_max_tokens"`
	VisionTemperature float32                `mapstructure:"vision_temperature" yaml:"vision_temperature"`
	VisionRetryCount  int                    `mapstructure:"vision_retry_count" yaml:"vision_retry_count"`
	VisionTimeout     int                    `mapstructure:"vision_timeout" yaml:"vision_timeout"`
	SystemPrompt      string                 `mapstructure:"system_prompt" yaml:"system_prompt"`
	ExtraParams       map[string]interface{} `mapstructure:"extra_params" yaml:"extra_params"`
	MemorySearchLimit int                    `mapstructure:"memory_search_limit" yaml:"memory_search_limit"`
	WorkerQueueSize   int                    `mapstructure:"worker_queue_size" yaml:"worker_queue_size"`
}

type RetrievalConfig struct {
	TopK     int     `mapstructure:"top_k" yaml:"top_k"`
	MinScore float64 `mapstructure:"min_score" yaml:"min_score"`
}

type WebConfig struct {
	Port                      int    `mapstructure:"port" yaml:"port"`
	Username                  string `mapstructure:"username" yaml:"username"`
	Password                  string `mapstructure:"password" yaml:"password"`
	Enabled                   bool   `mapstructure:"enabled" yaml:"enabled"`
	MemoriesPageLimit         int    `mapstructure:"memories_page_limit" yaml:"memories_page_limit"`
	ContentTruncationLength   int    `mapstructure:"content_truncation_length" yaml:"content_truncation_length"`
	SessionTTLMin             int    `mapstructure:"session_ttl_min" yaml:"session_ttl_min"`
	SessionCleanupIntervalMin int    `mapstructure:"session_cleanup_interval_min" yaml:"session_cleanup_interval_min"`
	StatsQueryTimeoutSec      int    `mapstructure:"stats_query_timeout_sec" yaml:"stats_query_timeout_sec"`
	LogDefaultLines           int    `mapstructure:"log_default_lines" yaml:"log_default_lines"`
	LogMaxLines               int    `mapstructure:"log_max_lines" yaml:"log_max_lines"`
	LogMaxReadBytes           int    `mapstructure:"log_max_read_bytes" yaml:"log_max_read_bytes"`
}

type LoggingConfig struct {
	Level      string `mapstructure:"level" yaml:"level"`
	File       string `mapstructure:"file" yaml:"file"`
	MaxSize    int    `mapstructure:"max_size" yaml:"max_size"`
	MaxBackups int    `mapstructure:"max_backups" yaml:"max_backups"`
	MaxAge     int    `mapstructure:"max_age" yaml:"max_age"`
}

type QdrantConfig struct {
	Host       string `mapstructure:"host" yaml:"host"`
	Port       int    `mapstructure:"port" yaml:"port"`
	APIKey     string `mapstructure:"api_key" yaml:"api_key"`
	VectorSize int    `mapstructure:"vector_size" yaml:"vector_size"`
}

type BlacklistConfig struct {
	Users    []string `mapstructure:"users" yaml:"users"`
	Guilds   []string `mapstructure:"guilds" yaml:"guilds"`
	Channels []string `mapstructure:"channels" yaml:"channels"`
}

type WhitelistConfig struct {
	Users    []string `mapstructure:"users" yaml:"users"`
	Guilds   []string `mapstructure:"guilds" yaml:"guilds"`
	Channels []string `mapstructure:"channels" yaml:"channels"`
}

type PluginsConfig struct {
	Enabled              bool   `mapstructure:"enabled" yaml:"enabled"`
	PluginsDir           string `mapstructure:"plugins_dir" yaml:"plugins_dir"`
	DefaultToolTimeoutMs int    `mapstructure:"default_tool_timeout_ms" yaml:"default_tool_timeout_ms"`
	StartupTimeoutSec    int    `mapstructure:"startup_timeout_sec" yaml:"startup_timeout_sec"`
	RPCTimeoutSec        int    `mapstructure:"rpc_timeout_sec" yaml:"rpc_timeout_sec"`
	BeforeSendTimeoutSec int    `mapstructure:"before_send_timeout_sec" yaml:"before_send_timeout_sec"`
	CommandTimeoutSec    int    `mapstructure:"command_timeout_sec" yaml:"command_timeout_sec"`
	ShutdownTimeoutSec   int    `mapstructure:"shutdown_timeout_sec" yaml:"shutdown_timeout_sec"`
	DisableTimeoutSec    int    `mapstructure:"disable_timeout_sec" yaml:"disable_timeout_sec"`
}

type MCPConfig struct {
	Enabled bool        `mapstructure:"enabled" yaml:"enabled"`
	Servers []MCPServer `mapstructure:"servers" yaml:"servers"`
}

type MCPServer struct {
	Name    string            `mapstructure:"name" yaml:"name"`
	Command string            `mapstructure:"command" yaml:"command"`
	Args    []string          `mapstructure:"args" yaml:"args"`
	Env     map[string]string `mapstructure:"env" yaml:"env"`
	URL     string            `mapstructure:"url" yaml:"url"`
	Type    string            `mapstructure:"type" yaml:"type"`
}

// VisionMode represents the vision processing mode
type VisionMode string

const (
	VisionModeTextOnly   VisionMode = "text_only"
	VisionModeHybrid     VisionMode = "hybrid"
	VisionModeMultimodal VisionMode = "multimodal"
)

// VisionConfig holds vision processing settings
type VisionConfig struct {
	Mode              VisionMode             `mapstructure:"mode" yaml:"mode"`
	DescriptionPrompt string                 `mapstructure:"description_prompt" yaml:"description_prompt"`
	MaxImages         int                    `mapstructure:"max_images" yaml:"max_images"`
	APIBaseURL        string                 `mapstructure:"api_base_url" yaml:"api_base_url"`
	APIKey            string                 `mapstructure:"api_key" yaml:"api_key"`
	MaxTokens         int                    `mapstructure:"max_tokens" yaml:"max_tokens"`
	Temperature       float32                `mapstructure:"temperature" yaml:"temperature"`
	RetryCount        int                    `mapstructure:"retry_count" yaml:"retry_count"`
	Timeout           int                    `mapstructure:"timeout" yaml:"timeout"`
	ExtraParams       map[string]interface{} `mapstructure:"extra_params" yaml:"extra_params"`
}

func Load(configPath string) (*Config, error) {
	v := viper.New()

	if configPath != "" {
		v.SetConfigFile(configPath)
		v.SetConfigType("yaml")
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		v.AddConfigPath("/etc/ezyapper")
	}

	v.SetEnvPrefix("EZYAPPER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil, fmt.Errorf("config file not found: %w", err)
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var parsed fileConfig
	if err := v.Unmarshal(&parsed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if parsed.SchemaVersion != currentConfigSchemaVersion {
		return nil, fmt.Errorf("unsupported config schema_version %d: expected %d", parsed.SchemaVersion, currentConfigSchemaVersion)
	}

	cfg := parsed.toRuntimeConfig()

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	cfg.configPath = v.ConfigFileUsed()

	return &cfg, nil
}

func requireNonEmpty(val string, path string, errs *[]string) {
	if val == "" {
		*errs = append(*errs, path+" is required")
	}
}

func requirePositive(val int, path string, errs *[]string) {
	if val <= 0 {
		*errs = append(*errs, path+" must be greater than 0")
	}
}

func validateCore(cfg *Config, errs *[]string) {
	// Core validation is handled by individual validators (discord, ai, decision)
}

func validateAI(cfg *Config, errs *[]string) {
	requireNonEmpty(cfg.AI.APIBaseURL, "ai.api_base_url", errs)
	requireNonEmpty(cfg.AI.APIKey, "ai.api_key", errs)
	requireNonEmpty(cfg.AI.Model, "ai.model", errs)
	requirePositive(cfg.AI.MaxTokens, "ai.max_tokens", errs)
	if cfg.AI.Temperature < 0 || cfg.AI.Temperature > 2 {
		*errs = append(*errs, "ai.temperature must be between 0 and 2")
	}
	requireNonEmpty(cfg.AI.SystemPrompt, "ai.system_prompt", errs)
	requirePositive(cfg.AI.RetryCount, "ai.retry_count", errs)
	requirePositive(cfg.AI.Timeout, "ai.timeout", errs)
	requirePositive(cfg.AI.HTTPTimeoutSec, "ai.http_timeout_sec", errs)
	requirePositive(cfg.AI.MaxToolIterations, "ai.max_tool_iterations", errs)
	requirePositive(cfg.AI.MaxImageBytes, "ai.max_image_bytes", errs)
	requireNonEmpty(cfg.AI.UserAgent, "ai.user_agent", errs)
	if !cfg.AI.VisionBase64 {
		fmt.Fprintf(os.Stderr, "WARNING: ai.vision_base64 is false — images will be sent as URLs (may not work with local endpoints)\n")
	}
}

func validateVision(cfg *Config, errs *[]string) {
	requireNonEmpty(string(cfg.AI.Vision.Mode), "ai.vision.mode", errs)
	validVisionModes := map[VisionMode]bool{
		VisionModeTextOnly:   true,
		VisionModeHybrid:     true,
		VisionModeMultimodal: true,
	}
	if !validVisionModes[cfg.AI.Vision.Mode] {
		*errs = append(*errs, "ai.vision.mode must be one of: text_only, hybrid, multimodal")
	}
	if cfg.AI.Vision.Mode != VisionModeTextOnly && cfg.AI.VisionModel == "" {
		*errs = append(*errs, "ai.vision_model is required when vision.mode is not text_only")
	}
	if cfg.AI.Vision.Mode != VisionModeTextOnly && cfg.AI.Vision.MaxImages <= 0 {
		*errs = append(*errs, "ai.vision.max_images must be greater than 0")
	}
	if cfg.AI.Vision.Mode == VisionModeHybrid && cfg.AI.Vision.DescriptionPrompt == "" {
		*errs = append(*errs, "ai.vision.description_prompt is required when vision.mode is hybrid")
	}
	if cfg.AI.Vision.MaxTokens != 0 {
		requirePositive(cfg.AI.Vision.MaxTokens, "ai.vision.max_tokens", errs)
	}
}

func validateDiscord(cfg *Config, errs *[]string) {
	requireNonEmpty(cfg.Discord.Token, "discord.token", errs)
	requireNonEmpty(cfg.Discord.BotName, "discord.bot_name", errs)
	if cfg.Discord.ReplyPercentage < 0 || cfg.Discord.ReplyPercentage > 1 {
		*errs = append(*errs, "discord.reply_percentage must be between 0 and 1")
	}
	requirePositive(cfg.Discord.ConsolidationTimeoutSec, "discord.consolidation_timeout_sec", errs)
	requirePositive(cfg.Discord.TypingIndicatorIntervalSec, "discord.typing_indicator_interval_sec", errs)
	requirePositive(cfg.Discord.LongResponseDelayMs, "discord.long_response_delay_ms", errs)
	requirePositive(cfg.Discord.ReplyTruncationLength, "discord.reply_truncation_length", errs)
	requirePositive(cfg.Discord.ImageCacheTTLMin, "discord.image_cache_ttl_min", errs)
	requirePositive(cfg.Discord.ImageCacheMaxEntries, "discord.image_cache_max_entries", errs)
	if cfg.Discord.ReplyToBots {
		fmt.Fprintf(os.Stderr, "WARNING: discord.reply_to_bots is true — bot will respond to other bots\n")
	}
}

func validateQdrant(cfg *Config, errs *[]string) {
	memoryEnabled := cfg.Memory.Retrieval.TopK > 0 || cfg.Memory.Consolidation.Enabled
	if !memoryEnabled {
		return
	}
	requireNonEmpty(cfg.Embedding.Model, "embedding.model", errs)
	if cfg.Embedding.RetryCount < 0 {
		*errs = append(*errs, "embedding.retry_count must be greater than or equal to 0")
	}
	requirePositive(cfg.Embedding.Timeout, "memory_pipeline.embedding.timeout", errs)
	requireNonEmpty(cfg.Qdrant.Host, "qdrant.host", errs)
	if cfg.Qdrant.Port <= 0 {
		*errs = append(*errs, "qdrant.port must be greater than 0 when memory retrieval or consolidation is enabled")
	}
	if cfg.Qdrant.VectorSize <= 0 {
		*errs = append(*errs, "qdrant.vector_size must be greater than 0 when memory retrieval or consolidation is enabled")
	}
	expectedVectorSize := expectedEmbeddingVectorSize(cfg.Embedding.Model)
	if expectedVectorSize > 0 && cfg.Qdrant.VectorSize > 0 && cfg.Qdrant.VectorSize != expectedVectorSize {
		*errs = append(*errs, fmt.Sprintf("qdrant.vector_size (%d) must match embedding.model %q size (%d)", cfg.Qdrant.VectorSize, cfg.Embedding.Model, expectedVectorSize))
	}
	requirePositive(cfg.Memory.RetryBaseDelayMs, "memory.retry_base_delay_ms", errs)
	requirePositive(cfg.Memory.RetryMaxDelayMs, "memory.retry_max_delay_ms", errs)
	requirePositive(cfg.Memory.MaxRetries, "memory.max_retries", errs)
}

func validateMemory(cfg *Config, errs *[]string) {
	requirePositive(cfg.Memory.ConsolidationInterval, "memory.consolidation_interval", errs)
	requirePositive(cfg.Memory.ShortTermLimit, "memory.short_term_limit", errs)
	requirePositive(cfg.Memory.MaxPaginatedLimit, "memory.max_paginated_limit", errs)
	if cfg.Memory.Retrieval.TopK < 0 {
		*errs = append(*errs, "memory.retrieval.top_k must be greater than or equal to 0")
	}
	if cfg.Memory.Retrieval.MinScore < 0 || cfg.Memory.Retrieval.MinScore > 1 {
		*errs = append(*errs, "memory.retrieval.min_score must be between 0 and 1")
	}
	requirePositive(cfg.Memory.Consolidation.MemorySearchLimit, "memory.consolidation.memory_search_limit", errs)
	requirePositive(cfg.Memory.Consolidation.WorkerQueueSize, "memory.consolidation.worker_queue_size", errs)
}

func validateRateLimit(cfg *Config, errs *[]string) {
	requirePositive(cfg.Discord.CooldownSeconds, "discord.cooldown_seconds", errs)
	requirePositive(cfg.Discord.MaxResponsesPerMin, "discord.max_responses_per_minute", errs)
	requirePositive(cfg.Discord.RateLimit.ResetPeriodSeconds, "discord.rate_limit.reset_period_seconds", errs)
}

func validateWeb(cfg *Config, errs *[]string) {
	if !cfg.Web.Enabled {
		return
	}
	if cfg.Web.Port <= 0 {
		*errs = append(*errs, "web.port must be greater than 0 when web is enabled")
	}
	if cfg.Web.Username == "" {
		*errs = append(*errs, "web.username is required when web is enabled")
	}
	if cfg.Web.Password == "" {
		*errs = append(*errs, "web.password is required when web is enabled")
	}
	requirePositive(cfg.Web.MemoriesPageLimit, "web.memories_page_limit", errs)
	requirePositive(cfg.Web.ContentTruncationLength, "web.content_truncation_length", errs)
	requirePositive(cfg.Web.SessionTTLMin, "web.session_ttl_min", errs)
	requirePositive(cfg.Web.SessionCleanupIntervalMin, "web.session_cleanup_interval_min", errs)
	requirePositive(cfg.Web.StatsQueryTimeoutSec, "web.stats_query_timeout_sec", errs)
	requirePositive(cfg.Web.LogDefaultLines, "web.log_default_lines", errs)
	requirePositive(cfg.Web.LogMaxLines, "web.log_max_lines", errs)
	requirePositive(cfg.Web.LogMaxReadBytes, "web.log_max_read_bytes", errs)
}

func validatePlugins(cfg *Config, errs *[]string) {
	if cfg.Plugins.DefaultToolTimeoutMs < 0 {
		cfg.Plugins.DefaultToolTimeoutMs = 0
	}
	if !cfg.Plugins.Enabled {
		return
	}
	requireNonEmpty(cfg.Plugins.PluginsDir, "plugins.plugins_dir", errs)
	if cfg.Plugins.DefaultToolTimeoutMs == 0 {
		fmt.Fprintf(os.Stderr, "[config][WARNING] plugins.default_tool_timeout_ms is 0 — tool execution will fail unless per-tool timeouts are set in plugin config\n")
	}
	requirePositive(cfg.Plugins.StartupTimeoutSec, "plugins.startup_timeout_sec", errs)
	requirePositive(cfg.Plugins.RPCTimeoutSec, "plugins.rpc_timeout_sec", errs)
	requirePositive(cfg.Plugins.BeforeSendTimeoutSec, "plugins.before_send_timeout_sec", errs)
	requirePositive(cfg.Plugins.CommandTimeoutSec, "plugins.command_timeout_sec", errs)
	requirePositive(cfg.Plugins.ShutdownTimeoutSec, "plugins.shutdown_timeout_sec", errs)
	requirePositive(cfg.Plugins.DisableTimeoutSec, "plugins.disable_timeout_sec", errs)
}

func validateBlacklist(cfg *Config, errs *[]string) {
	// Blacklist/whitelist mutual exclusivity is handled by validateAccess
}

func validateOperations(cfg *Config, errs *[]string) {
	requireNonEmpty(cfg.Logging.Level, "logging.level", errs)
	if _, err := zapcore.ParseLevel(cfg.Logging.Level); err != nil && cfg.Logging.Level != "" {
		*errs = append(*errs, fmt.Sprintf("logging.level %q is invalid: %v", cfg.Logging.Level, err))
	}
	requireNonEmpty(cfg.Logging.File, "logging.file", errs)
	requirePositive(cfg.Logging.MaxSize, "logging.max_size", errs)
	requirePositive(cfg.Logging.MaxBackups, "logging.max_backups", errs)
	requirePositive(cfg.Logging.MaxAge, "logging.max_age", errs)
	requirePositive(cfg.Operations.ShutdownTimeoutSec, "operations.runtime.shutdown_timeout_sec", errs)
	requirePositive(cfg.Operations.CleanupIntervalMin, "operations.runtime.cleanup_interval_min", errs)
}

func validatePrompt(cfg *Config, errs *[]string) {
	// System prompt validation is handled by validateAI, validateConsolidation, validateDecision
}

func validateAccess(cfg *Config, errs *[]string) {
	if len(cfg.Blacklist.Users) > 0 && len(cfg.Whitelist.Users) > 0 {
		*errs = append(*errs, "cannot have both blacklist.users and whitelist.users enabled")
	}
	if len(cfg.Blacklist.Guilds) > 0 && len(cfg.Whitelist.Guilds) > 0 {
		*errs = append(*errs, "cannot have both blacklist.guilds and whitelist.guilds enabled")
	}
	if len(cfg.Blacklist.Channels) > 0 && len(cfg.Whitelist.Channels) > 0 {
		*errs = append(*errs, "cannot have both blacklist.channels and whitelist.channels enabled")
	}
}

func validateConsolidation(cfg *Config, errs *[]string) {
	if !cfg.Memory.Consolidation.Enabled {
		return
	}
	if cfg.Discord.OwnBotID == "" {
		*errs = append(*errs, "discord.own_bot_id is required when consolidation is enabled")
	}
	if cfg.Memory.Consolidation.SystemPrompt == "" {
		*errs = append(*errs, "memory.consolidation.system_prompt is required when consolidation is enabled")
	}
}

func validateDecision(cfg *Config, errs *[]string) {
	if !cfg.Decision.Enabled {
		return
	}
	requireNonEmpty(cfg.Decision.Model, "decision.model", errs)
	requireNonEmpty(cfg.Decision.APIBaseURL, "decision.api_base_url", errs)
	requireNonEmpty(cfg.Decision.APIKey, "decision.api_key", errs)
	requireNonEmpty(cfg.Decision.SystemPrompt, "decision.system_prompt", errs)
	if cfg.Decision.MaxTokens <= 0 {
		*errs = append(*errs, "decision.max_tokens must be greater than 0 when decision is enabled")
	}
	if cfg.Decision.Temperature < 0 || cfg.Decision.Temperature > 2 {
		*errs = append(*errs, "decision.temperature must be between 0 and 2")
	}
	if cfg.Decision.Timeout <= 0 {
		*errs = append(*errs, "decision.timeout must be greater than 0 when decision is enabled")
	}
	if cfg.Decision.RetryCount < 0 {
		*errs = append(*errs, "decision.retry_count must be greater than or equal to 0")
	}
	requirePositive(cfg.Decision.HTTPTimeoutSec, "decision.http_timeout_sec", errs)
}

func validateSystem(cfg *Config, errs *[]string) {
	if !cfg.MCP.Enabled {
		return
	}
	if len(cfg.MCP.Servers) == 0 {
		*errs = append(*errs, "mcp.servers must contain at least one server when mcp is enabled")
		return
	}
	for i, server := range cfg.MCP.Servers {
		prefix := fmt.Sprintf("mcp.servers[%d]", i)
		requireNonEmpty(server.Name, prefix+".name", errs)
		if server.Type == "" {
			*errs = append(*errs, prefix+".type is required")
			continue
		}
		switch server.Type {
		case "stdio":
			if server.Command == "" {
				*errs = append(*errs, prefix+".command is required when type is stdio")
			}
		case "sse":
			if server.URL == "" {
				*errs = append(*errs, prefix+".url is required when type is sse")
			}
		default:
			*errs = append(*errs, prefix+".type must be one of: stdio, sse")
		}
	}
}

func validate(cfg *Config) error {
	var errs []string
	validateCore(cfg, &errs)
	validateAI(cfg, &errs)
	validateVision(cfg, &errs)
	validateDiscord(cfg, &errs)
	validateQdrant(cfg, &errs)
	validateMemory(cfg, &errs)
	validateRateLimit(cfg, &errs)
	validateWeb(cfg, &errs)
	validatePlugins(cfg, &errs)
	validateBlacklist(cfg, &errs)
	validateOperations(cfg, &errs)
	validatePrompt(cfg, &errs)
	validateAccess(cfg, &errs)
	validateConsolidation(cfg, &errs)
	validateDecision(cfg, &errs)
	validateSystem(cfg, &errs)
	if len(errs) > 0 {
		errList := make([]error, len(errs))
		for i, s := range errs {
			errList[i] = fmt.Errorf("%s", s)
		}
		return errors.Join(errList...)
	}
	return nil
}

// Validate checks runtime config consistency and required fields.
func Validate(cfg *Config) error {
	return validate(cfg)
}

func (c *Config) FormatSystemPrompt(authorName, serverName, guildID, channelID string) string {
	prompt := c.AI.SystemPrompt

	replacements := map[string]string{
		"{BotName}":    c.Discord.BotName,
		"{AuthorName}": authorName,
		"{ServerName}": serverName,
		"{GuildID}":    guildID,
		"{ChannelID}":  channelID,
	}

	for placeholder, value := range replacements {
		prompt = strings.ReplaceAll(prompt, placeholder, value)
	}

	return prompt
}

func (c *Config) SetConfigPath(path string) {
	c.configPath = path
}

func (c *Config) Save() error {
	if c.configPath == "" {
		return fmt.Errorf("config path not set")
	}

	fileData := toFileConfig(c)
	data, err := yaml.Marshal(fileData)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(c.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

const (
	// openAISmallVector is the default vector dimension for text-embedding-3-small and text-embedding-ada-002.
	openAISmallVector = 1536
	// openAILargeVector is the default vector dimension for text-embedding-3-large.
	openAILargeVector = 3072
)

func expectedEmbeddingVectorSize(model string) int {
	switch strings.TrimSpace(strings.ToLower(model)) {
	case "text-embedding-3-small", "text-embedding-ada-002":
		return openAISmallVector
	case "text-embedding-3-large":
		return openAILargeVector
	default:
		return 0
	}
}
