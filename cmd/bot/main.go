// Package main is the entry point for the Discord bot
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/ai/tools"
	"ezyapper/internal/ai/vision"
	"ezyapper/internal/bot"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
	"ezyapper/internal/web"

	"github.com/spf13/pflag"
)

var configFile string

func init() {
	pflag.StringVarP(&configFile, "config", "c", "", "Path to config file (default: ./config.yaml)")
}

func main() {
	pflag.Parse()

	// Load configuration
	cfg, err := config.Load(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	if err := logger.Init(logger.Config{
		Level:      cfg.Logging.Level,
		File:       cfg.Logging.File,
		MaxSize:    cfg.Logging.MaxSize,
		MaxBackups: cfg.Logging.MaxBackups,
		MaxAge:     cfg.Logging.MaxAge,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	logger.Info("Starting EZyapper")

	// Initialize Qdrant memory service
	memoryService, err := initMemoryService(cfg)
	if err != nil {
		logger.Fatalf("Failed to initialize memory service: %v", err)
	}
	defer memoryService.Close()
	logger.Info("Memory service initialized")

	// Initialize plugin manager
	pluginManager := plugin.NewManager(cfg.Plugins.DefaultToolTimeoutMs,
		cfg.Plugins.StartupTimeoutSec,
		cfg.Plugins.RPCTimeoutSec,
		cfg.Plugins.BeforeSendTimeoutSec,
		cfg.Plugins.CommandTimeoutSec,
		cfg.Plugins.ShutdownTimeoutSec,
		cfg.Plugins.DisableTimeoutSec,
	)

	// Create shared config store (copy-on-write via atomic.Value)
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)

	// Initialize Discord bot
	discordBot, err := bot.New(cfgStore, memoryService, memoryService, memoryService, pluginManager)
	if err != nil {
		logger.Fatalf("Failed to create Discord bot: %v", err)
	}

	// Start Discord bot
	ctx := context.Background()
	if err := discordBot.Start(ctx); err != nil {
		logger.Fatalf("Failed to start Discord bot: %v", err)
	}

	// Initialize web server
	s := discordBot.GetSession()
	discordAdapter := web.NewDiscordAdapter(
		func(channelID string) string {
			ch, err := s.State.Channel(channelID)
			if err != nil || ch == nil {
				return channelID
			}
			return ch.Name
		},
		func(guildID, userID string) string {
			member, err := s.State.Member(guildID, userID)
			if err == nil && member != nil && member.User != nil {
				return member.User.Username
			}
			user, err := s.User(userID)
			if err == nil && user != nil {
				return user.Username
			}
			return userID
		},
		func(guildID string) string {
			g, err := s.State.Guild(guildID)
			if err != nil || g == nil {
				return guildID
			}
			return g.Name
		},
	)
	webServer := web.NewServer(cfgStore, memoryService, memoryService, memoryService, pluginManager, discordBot, discordAdapter)
	if err := webServer.Start(); err != nil {
		logger.Warnf("Failed to start web server: %v", err)
	}

	// Start cleanup routine
	go runCleanupRoutine(cfg, discordBot)

	logger.Info("Bot is now running. Press CTRL+C to exit.")

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down...")

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Operations.ShutdownTimeoutSec)*time.Second)
	defer cancel()

	// Stop web server
	if err := webServer.Stop(shutdownCtx); err != nil {
		logger.Warnf("Error stopping web server: %v", err)
	}

	if err := discordBot.Shutdown(shutdownCtx); err != nil {
		logger.Warnf("Error during bot shutdown: %v", err)
	}

	// Stop Discord bot
	if err := discordBot.Stop(); err != nil {
		logger.Warnf("Error stopping Discord bot: %v", err)
	}

	// Shutdown plugins
	if err := pluginManager.Shutdown(shutdownCtx); err != nil {
		logger.Warnf("Error shutting down plugins: %v", err)
	}

	logger.Info("Bot stopped. Goodbye!")
}

// initMemoryService initializes the memory service with Qdrant
func initMemoryService(cfg *config.Config) (memory.Service, error) {
	memoryEnabled := cfg.Memory.Retrieval.TopK > 0 || cfg.Memory.Consolidation.Enabled
	if !memoryEnabled {
		logger.Info("Memory subsystem is disabled by config (retrieval.top_k=0 and consolidation.enabled=false)")
		return memory.NewNoopService(), nil
	}

	embeddingAIConfig := buildEmbeddingAIConfig(cfg)
	embedderClient := ai.NewClient(embeddingAIConfig, tools.NewToolRegistry())

	consolidationConfig := &config.AIConfig{
		APIBaseURL:  cfg.AI.APIBaseURL,
		APIKey:      cfg.AI.APIKey,
		Model:       cfg.AI.Model,
		VisionModel: cfg.AI.VisionModel,
	}
	if cfg.Memory.Consolidation.APIBaseURL != "" {
		consolidationConfig.APIBaseURL = cfg.Memory.Consolidation.APIBaseURL
	}
	if cfg.Memory.Consolidation.APIKey != "" {
		consolidationConfig.APIKey = cfg.Memory.Consolidation.APIKey
	}
	if cfg.Memory.Consolidation.Model != "" {
		consolidationConfig.Model = cfg.Memory.Consolidation.Model
	}
	// Copy extra params from consolidation config
	consolidationConfig.ExtraParams = cfg.Memory.Consolidation.ExtraParams
	consolidationAIClient := ai.NewClient(consolidationConfig, tools.NewToolRegistry())

	visionConfig, visionAIConfig := buildConsolidationVisionConfig(cfg, consolidationConfig)
	visionClient := ai.NewClient(visionAIConfig, tools.NewToolRegistry())
	visionDescriber := vision.NewVisionDescriber(visionClient, visionConfig, visionAIConfig)

	embedder, err := memory.NewAIEmbedder(embedderClient, cfg.Embedding.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedder: %w", err)
	}

	qdrantClient, err := memory.NewQdrantClient(&cfg.Qdrant, cfg.Memory.MaxRetries, cfg.Memory.RetryBaseDelayMs, cfg.Memory.RetryMaxDelayMs, cfg.Memory.Retrieval.DefaultTopK, cfg.Memory.Retrieval.DefaultMinScore)
	if err != nil {
		return nil, fmt.Errorf("failed to create qdrant client: %w", err)
	}

	memoryCfg := &memory.ServiceConfig{
		ConsolidationInterval: cfg.Memory.ConsolidationInterval,
		ShortTermLimit:        cfg.Memory.ShortTermLimit,
		TopK:                  cfg.Memory.Retrieval.TopK,
		MinScore:              cfg.Memory.Retrieval.MinScore,
		Consolidation:         &cfg.Memory.Consolidation,
		OwnBotID:              cfg.Discord.OwnBotID,
		MemorySearchLimit:     cfg.Memory.Consolidation.MemorySearchLimit,
		WorkerQueueSize:       cfg.Memory.Consolidation.WorkerQueueSize,
		MaxPaginatedLimit:     cfg.Memory.MaxPaginatedLimit,
		RetryMaxRetries:       cfg.Memory.MaxRetries,
		RetryBaseDelayMs:      cfg.Memory.RetryBaseDelayMs,
		RetryMaxDelayMs:       cfg.Memory.RetryMaxDelayMs,
	}

	return memory.NewService(memoryCfg, qdrantClient, embedder, consolidationAIClient, visionDescriber)
}

func buildEmbeddingAIConfig(cfg *config.Config) *config.AIConfig {
	embeddingCfg := cfg.AI

	if cfg.Embedding.APIBaseURL != "" {
		embeddingCfg.APIBaseURL = cfg.Embedding.APIBaseURL
	}
	if cfg.Embedding.APIKey != "" {
		embeddingCfg.APIKey = cfg.Embedding.APIKey
	}
	embeddingCfg.RetryCount = cfg.Embedding.RetryCount
	if cfg.Embedding.Timeout > 0 {
		embeddingCfg.Timeout = cfg.Embedding.Timeout
	}
	if cfg.Embedding.ExtraParams != nil {
		embeddingCfg.ExtraParams = cfg.Embedding.ExtraParams
	}

	return &embeddingCfg
}

// runCleanupRoutine runs periodic cleanup tasks
func runCleanupRoutine(cfg *config.Config, discordBot *bot.Bot) {
	ticker := time.NewTicker(time.Duration(cfg.Operations.CleanupIntervalMin) * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		discordBot.CleanupCache()
		logger.Debug("Cleanup routine completed")
	}
}

func buildConsolidationVisionConfig(cfg *config.Config, baseConfig *config.AIConfig) (*config.VisionConfig, *config.AIConfig) {
	visionConfig := cfg.AI.Vision
	visionAIConfig := *baseConfig
	if visionAIConfig.VisionModel == "" {
		visionAIConfig.VisionModel = cfg.AI.VisionModel
	}
	if visionAIConfig.VisionModel == "" {
		visionAIConfig.VisionModel = visionAIConfig.Model
	}

	if cfg.Memory.Consolidation.VisionModel != "" {
		visionAIConfig.VisionModel = cfg.Memory.Consolidation.VisionModel
	}
	if cfg.Memory.Consolidation.VisionAPIBaseURL != "" {
		visionAIConfig.APIBaseURL = cfg.Memory.Consolidation.VisionAPIBaseURL
	}
	if cfg.Memory.Consolidation.VisionAPIKey != "" {
		visionAIConfig.APIKey = cfg.Memory.Consolidation.VisionAPIKey
	}
	if cfg.Memory.Consolidation.VisionMaxTokens > 0 {
		visionAIConfig.MaxTokens = cfg.Memory.Consolidation.VisionMaxTokens
	}
	if cfg.Memory.Consolidation.VisionTemperature > 0 {
		visionAIConfig.Temperature = cfg.Memory.Consolidation.VisionTemperature
	}
	if cfg.Memory.Consolidation.VisionRetryCount > 0 {
		visionAIConfig.RetryCount = cfg.Memory.Consolidation.VisionRetryCount
	}
	if cfg.Memory.Consolidation.VisionTimeout > 0 {
		visionAIConfig.Timeout = cfg.Memory.Consolidation.VisionTimeout
	}

	// Copy extra params from consolidation vision config (if any specific ones are set)
	// Note: VisionConfig.ExtraParams is applied in VisionDescriber, not here

	return &visionConfig, &visionAIConfig
}
