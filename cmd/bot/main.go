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

	"ezyapper/internal/app"
	"ezyapper/internal/bot"
	"ezyapper/internal/config"
	"ezyapper/internal/discordvis"
	"ezyapper/internal/hotreload"
	"ezyapper/internal/logger"
	"ezyapper/internal/media"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
	"ezyapper/internal/web"

	"github.com/spf13/pflag"
)

var configFile string

func main() {
	pflag.StringVarP(&configFile, "config", "c", "", "Path to config file (default: ./config.yaml)")
	pflag.Parse()

	cfg, err := config.Load(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// ffmpeg is optional, but an enabled option that needs it must not
	// fail on the first message: refuse to start instead.
	if _, err := media.CheckFFmpeg(context.Background(), cfg.AI.Modalities); err != nil {
		fmt.Fprintf(os.Stderr, "Refusing to start: %v\n", err)
		os.Exit(1)
	}

	if err := logger.Init(app.LoggerConfig(cfg.Logging)); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	logger.Info("[main] Starting EZyapper")

	// One media cache (image descriptions, transcripts, video descriptions
	// by attachment ID) shared by the bot and memory consolidation, so each
	// attachment is turned into text once.
	mediaCache := media.NewCache(time.Duration(cfg.Discord.ImageCacheTTLMin)*time.Minute, cfg.Discord.ImageCacheMaxEntries)

	memoryService, err := app.BuildMemoryService(cfg, mediaCache)
	if err != nil {
		logger.Fatalf("[main] Failed to initialize memory service: %v", err)
	}
	// Bot and dashboard hold the proxy, so hot reload can replace the
	// memory service underneath them.
	memoryProxy := memory.NewSwappable(memoryService)
	defer memoryProxy.Close()
	logger.Info("[main] Memory service initialized")

	if err := memoryProxy.StartMaintenance(context.Background()); err != nil {
		logger.Warnf("[main] maintenance start failed: %v", err)
	}

	ps := app.PluginSettings(cfg.Plugins)
	pluginManager := plugin.NewManager(ps.DefaultToolTimeoutMs,
		ps.StartupTimeoutSec,
		ps.RPCTimeoutSec,
		ps.BeforeSendTimeoutSec,
		ps.CommandTimeoutSec,
		ps.ShutdownTimeoutSec,
		ps.DisableTimeoutSec,
		ps.MaxRestarts,
		cfg.Plugins.RestartCooldownSec,
	)

	// Shared config store (copy-on-write via atomic.Value). Every
	// subsystem reads the live config from it; hot reload publishes here.
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)
	currentCfg := func() *config.Config { return cfgStore.Load().(*config.Config) }

	discordBot, err := bot.New(cfgStore, memoryProxy, memoryProxy, memoryProxy, pluginManager, bot.WithMediaCache(mediaCache))
	if err != nil {
		logger.Fatalf("[main] Failed to create Discord bot: %v", err)
	}
	// A plugin that reloads its own config may change its tools; it sends
	// tools_changed and the registry is refreshed here.
	pluginManager.SetToolsChangedHandler(discordBot.RefreshPluginTools)

	if err := discordBot.Start(context.Background()); err != nil {
		logger.Fatalf("[main] Failed to start Discord bot: %v", err)
	}

	// The adapter resolves the session per call: a token hot reload
	// replaces it.
	discordAdapter := web.NewDiscordAdapter(
		func(channelID string) string {
			// Hidden channels fall back to the ID like unknown ones, so
			// the dashboard never shows Discord's "___hidden___"
			// placeholder and the configured entry stays visible.
			st := discordBot.GetSession().State
			ch, err := st.Channel(channelID)
			if err != nil || ch == nil {
				return channelID
			}
			if name, ok := discordvis.Name(ch, discordvis.StatePermissions(st, discordvis.StateBotID(st))); ok {
				return name
			}
			return channelID
		},
		func(guildID, userID string) string {
			s := discordBot.GetSession()
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
			g, err := discordBot.GetSession().State.Guild(guildID)
			if err != nil || g == nil {
				return guildID
			}
			return g.Name
		},
	)
	webServer := web.NewServer(cfgStore, memoryProxy, memoryProxy, memoryProxy, pluginManager, discordBot, discordAdapter)

	// Config hot reload. Components are prepared in this order and
	// committed in the same order once all of them prepared.
	reloader := hotreload.New(cfgStore,
		app.LoggingComponent{},
		app.NewMemoryComponent(memoryProxy, mediaCache),
		discordBot,
		app.PluginsComponent{Manager: pluginManager},
		webServer,
	)
	reloader.Register(reloader.WatcherComponent())
	webServer.SetConfigApplier(app.Applier{R: reloader})

	if err := webServer.Start(); err != nil {
		logger.Warnf("[main] Failed to start web server: %v", err)
	}
	if err := reloader.Start(); err != nil {
		logger.Warnf("[main] Config hot reload unavailable: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[cleanup] panic in cleanup routine: %v", r)
			}
		}()
		runCleanupRoutine(ctx, currentCfg, discordBot)
	}()

	logger.Info("[main] Bot is now running. Press CTRL+C to exit.")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("[main] Shutting down...")
	reloader.Stop()
	cancel() // Cancel the context to stop the cleanup routine

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(currentCfg().Operations.ShutdownTimeoutSec)*time.Second)
	defer cancel()

	if err := webServer.Stop(shutdownCtx); err != nil {
		logger.Warnf("[main] Error stopping web server: %v", err)
	}

	if err := discordBot.Shutdown(shutdownCtx); err != nil {
		logger.Warnf("[main] Error during bot shutdown: %v", err)
	}

	if err := discordBot.Stop(); err != nil {
		logger.Warnf("[main] Error stopping Discord bot: %v", err)
	}

	if err := pluginManager.Shutdown(shutdownCtx); err != nil {
		logger.Warnf("[main] Error shutting down plugins: %v", err)
	}

	logger.Info("[main] Bot stopped. Goodbye!")
}

// runCleanupRoutine runs periodic cleanup tasks. The interval is re-read
// after every run so operations.runtime.cleanup_interval_min hot reloads.
func runCleanupRoutine(ctx context.Context, cfg func() *config.Config, discordBot *bot.Bot) {
	interval := time.Duration(cfg().Operations.CleanupIntervalMin) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			discordBot.CleanupCache()
			logger.Debug("[main] Cleanup routine completed")
			if next := time.Duration(cfg().Operations.CleanupIntervalMin) * time.Minute; next > 0 && next != interval {
				interval = next
				ticker.Reset(interval)
			}
		case <-ctx.Done():
			return
		}
	}
}
