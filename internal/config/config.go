// Package config provides configuration management using Viper
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Config represents the complete application configuration
type Config struct {
	Discord   DiscordConfig   `mapstructure:"discord" yaml:"discord"`
	AI        AIConfig        `mapstructure:"ai" yaml:"ai"`
	Embedding EmbeddingConfig `mapstructure:"embedding" yaml:"embedding"`
	Memory    MemoryConfig    `mapstructure:"memory" yaml:"memory"`
	Web       WebConfig       `mapstructure:"web" yaml:"web"`
	Logging   LoggingConfig   `mapstructure:"logging" yaml:"logging"`
	Qdrant    QdrantConfig    `mapstructure:"qdrant" yaml:"qdrant"`
	Blacklist BlacklistConfig `mapstructure:"blacklist" yaml:"blacklist"`
	Whitelist WhitelistConfig `mapstructure:"whitelist" yaml:"whitelist"`
	Plugins   PluginsConfig   `mapstructure:"plugins" yaml:"plugins"`
	MCP       MCPConfig       `mapstructure:"mcp" yaml:"mcp"`
	Decision  DecisionConfig  `mapstructure:"decision" yaml:"decision"`

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
	Web     WebConfig     `mapstructure:"web" yaml:"web"`
	Logging LoggingConfig `mapstructure:"logging" yaml:"logging"`
	Plugins PluginsConfig `mapstructure:"plugins" yaml:"plugins"`
	MCP     MCPConfig     `mapstructure:"mcp" yaml:"mcp"`
}

func (f fileConfig) toRuntimeConfig() Config {
	return Config{
		Discord:   f.Core.Discord,
		AI:        f.Core.AI,
		Decision:  f.Core.Decision,
		Embedding: f.MemoryPipe.Embedding,
		Memory:    f.MemoryPipe.Memory,
		Qdrant:    f.MemoryPipe.Qdrant,
		Blacklist: f.Access.Blacklist,
		Whitelist: f.Access.Whitelist,
		Web:       f.Operations.Web,
		Logging:   f.Operations.Logging,
		Plugins:   f.Operations.Plugins,
		MCP:       f.Operations.MCP,
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
		},
	}
}

// DecisionConfig holds LLM-based reply decision settings
type DecisionConfig struct {
	Enabled         bool                   `mapstructure:"enabled" yaml:"enabled"`
	Model           string                 `mapstructure:"model" yaml:"model"`
	APIBaseURL      string                 `mapstructure:"api_base_url" yaml:"api_base_url"`
	APIKey          string                 `mapstructure:"api_key" yaml:"api_key"`
	MaxTokens       int                    `mapstructure:"max_tokens" yaml:"max_tokens"`
	Temperature     float32                `mapstructure:"temperature" yaml:"temperature"`
	RetryCount      int                    `mapstructure:"retry_count" yaml:"retry_count"`
	Timeout         int                    `mapstructure:"timeout" yaml:"timeout"`
	SystemPrompt    string                 `mapstructure:"system_prompt" yaml:"system_prompt"`
	ExtraParams     map[string]interface{} `mapstructure:"extra_params" yaml:"extra_params"`
}

// DiscordConfig holds Discord bot specific settings
type DiscordConfig struct {
	Token              string  `mapstructure:"token" yaml:"token"`
	BotName            string  `mapstructure:"bot_name" yaml:"bot_name"`
	OwnBotID           string  `mapstructure:"own_bot_id" yaml:"own_bot_id"` // Bot's own ID to distinguish from other bots
	ReplyPercentage    float64 `mapstructure:"reply_percentage" yaml:"reply_percentage"`
	CooldownSeconds    int     `mapstructure:"cooldown_seconds" yaml:"cooldown_seconds"`
	MaxResponsesPerMin int     `mapstructure:"max_responses_per_minute" yaml:"max_responses_per_minute"`
	ReplyToBots        bool    `mapstructure:"reply_to_bots" yaml:"reply_to_bots"`
}

// AIConfig holds AI/LLM settings for chat
type AIConfig struct {
	APIBaseURL   string                 `mapstructure:"api_base_url" yaml:"api_base_url"`
	APIKey       string                 `mapstructure:"api_key" yaml:"api_key"`
	Model        string                 `mapstructure:"model" yaml:"model"`
	VisionModel  string                 `mapstructure:"vision_model" yaml:"vision_model"`
	VisionBase64 bool                   `mapstructure:"vision_base64" yaml:"vision_base64"`
	Vision       VisionConfig           `mapstructure:"vision" yaml:"vision"`
	MaxTokens    int                    `mapstructure:"max_tokens" yaml:"max_tokens"`
	Temperature  float32                `mapstructure:"temperature" yaml:"temperature"`
	RetryCount   int                    `mapstructure:"retry_count" yaml:"retry_count"`
	Timeout      int                    `mapstructure:"timeout" yaml:"timeout"`
	SystemPrompt string                 `mapstructure:"system_prompt" yaml:"system_prompt"`
	ExtraParams  map[string]interface{} `mapstructure:"extra_params" yaml:"extra_params"`
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
	Retrieval             RetrievalConfig     `mapstructure:"retrieval" yaml:"retrieval"`
	Consolidation         ConsolidationConfig `mapstructure:"consolidation" yaml:"consolidation"`
}

type ConsolidationConfig struct {
	Enabled           bool                   `mapstructure:"enabled" yaml:"enabled"`
	MaxMessages       int                    `mapstructure:"max_messages" yaml:"max_messages"`
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
}

type RetrievalConfig struct {
	TopK     int     `mapstructure:"top_k" yaml:"top_k"`
	MinScore float64 `mapstructure:"min_score" yaml:"min_score"`
}

type WebConfig struct {
	Port     int    `mapstructure:"port" yaml:"port"`
	Username string `mapstructure:"username" yaml:"username"`
	Password string `mapstructure:"password" yaml:"password"`
	Enabled  bool   `mapstructure:"enabled" yaml:"enabled"`
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
	Enabled    bool   `mapstructure:"enabled" yaml:"enabled"`
	PluginsDir string `mapstructure:"plugins_dir" yaml:"plugins_dir"`
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
}

func validateDiscord(cfg *Config, errs *[]string) {
	requireNonEmpty(cfg.Discord.Token, "discord.token", errs)
	requireNonEmpty(cfg.Discord.BotName, "discord.bot_name", errs)
	if cfg.Discord.ReplyPercentage < 0 || cfg.Discord.ReplyPercentage > 1 {
		*errs = append(*errs, "discord.reply_percentage must be between 0 and 1")
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
	if cfg.Embedding.Timeout <= 0 {
		*errs = append(*errs, "embedding.timeout must be greater than 0 when memory retrieval or consolidation is enabled")
	}
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
}

func validateMemory(cfg *Config, errs *[]string) {
	requirePositive(cfg.Memory.ConsolidationInterval, "memory.consolidation_interval", errs)
	requirePositive(cfg.Memory.ShortTermLimit, "memory.short_term_limit", errs)
	if cfg.Memory.Retrieval.TopK < 0 {
		*errs = append(*errs, "memory.retrieval.top_k must be greater than or equal to 0")
	}
	if cfg.Memory.Retrieval.MinScore < 0 || cfg.Memory.Retrieval.MinScore > 1 {
		*errs = append(*errs, "memory.retrieval.min_score must be between 0 and 1")
	}
}

func validateRateLimit(cfg *Config, errs *[]string) {
	requirePositive(cfg.Discord.CooldownSeconds, "discord.cooldown_seconds", errs)
	requirePositive(cfg.Discord.MaxResponsesPerMin, "discord.max_responses_per_minute", errs)
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
}

func validatePlugins(cfg *Config, errs *[]string) {
	if !cfg.Plugins.Enabled {
		return
	}
	requireNonEmpty(cfg.Plugins.PluginsDir, "plugins.plugins_dir", errs)
}

func validateBlacklist(cfg *Config, errs *[]string) {
	// Blacklist/whitelist mutual exclusivity is handled by validateAccess
}

func validateOperations(cfg *Config, errs *[]string) {
	requireNonEmpty(cfg.Logging.Level, "logging.level", errs)
	requireNonEmpty(cfg.Logging.File, "logging.file", errs)
	requirePositive(cfg.Logging.MaxSize, "logging.max_size", errs)
	requirePositive(cfg.Logging.MaxBackups, "logging.max_backups", errs)
	requirePositive(cfg.Logging.MaxAge, "logging.max_age", errs)
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
	if cfg.Memory.Consolidation.MaxMessages <= 0 {
		*errs = append(*errs, "memory.consolidation.max_messages must be greater than 0 when consolidation is enabled")
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
		return fmt.Errorf("%s", strings.Join(errs, "; "))
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
