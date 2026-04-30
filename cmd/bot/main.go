// Package main is the entry point for the Discord bot
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	"ezyapper/internal/ai"
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
	pluginManager := plugin.NewManager(cfg.Plugins.DefaultToolTimeoutMs)

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
	webServer := web.NewServer(cfgStore, memoryService, memoryService, memoryService, pluginManager, discordBot, web.NewDiscordAdapter(discordBot.GetSession()))
	if err := webServer.Start(); err != nil {
		logger.Warnf("Failed to start web server: %v", err)
	}

	// Start cleanup routine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[main] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()
		runCleanupRoutine(discordBot)
	}()

	logger.Info("Bot is now running. Press CTRL+C to exit.")

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down...")

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop web server
	if err := webServer.Stop(shutdownCtx); err != nil {
		logger.Warnf("Error stopping web server: %v", err)
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
	embedderClient := ai.NewClient(embeddingAIConfig, ai.NewToolRegistry())

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
	consolidationAIClient := ai.NewClient(consolidationConfig, ai.NewToolRegistry())

	visionConfig, visionAIConfig := buildConsolidationVisionConfig(cfg, consolidationConfig)
	visionClient := ai.NewClient(visionAIConfig, ai.NewToolRegistry())
	visionDescriber := ai.NewVisionDescriber(visionClient, visionConfig, visionAIConfig)

	embedder, err := memory.NewAIEmbedder(embedderClient, cfg.Embedding.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedder: %w", err)
	}

	qdrantClient, err := memory.NewQdrantClient(&cfg.Qdrant)
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
func runCleanupRoutine(discordBot *bot.Bot) {
	ticker := time.NewTicker(1 * time.Hour)
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
