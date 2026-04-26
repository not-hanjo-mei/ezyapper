package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type Server struct {
	router            *gin.Engine
	configStore       *atomic.Value // stores *config.Config
	memoryStore       memory.MemoryStore
	profileStore      memory.ProfileStore
	consolidationMgr  memory.ConsolidationManager
	pluginManager     *plugin.Manager
	server            *http.Server
	mu                sync.RWMutex
	startTime         time.Time
	discordFetcher    DiscordMessageFetcher
	webDir            string
}

func (s *Server) cfg() *config.Config {
	return s.configStore.Load().(*config.Config)
}

type DiscordMessageFetcher interface {
	FetchUserMessages(channelID string, userID string, limit int) ([]*memory.DiscordMessage, error)
}

type RuntimeConfigApplier interface {
	ApplyRuntimeConfig() error
}

type PluginToolRefresher interface {
	RefreshPluginTools()
}

func findWebDir() string {
	candidates := []string{
		"./web",
		"../web",
		"../../web",
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "web"),
			filepath.Join(exeDir, "../web"),
		)
	}

	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			return dir
		}
	}

	return "./web"
}

func NewServer(cfgStore *atomic.Value, memStore memory.MemoryStore, profileStore memory.ProfileStore, conMgr memory.ConsolidationManager, pluginManager *plugin.Manager, discordFetcher DiscordMessageFetcher) *Server {
	cfg := cfgStore.Load().(*config.Config)
	if cfg.Logging.Level == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.Use(func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Next()
	})

	server := &Server{
		router:            router,
		configStore:       cfgStore,
		memoryStore:       memStore,
		profileStore:      profileStore,
		consolidationMgr:  conMgr,
		pluginManager:     pluginManager,
		startTime:         time.Now(),
		discordFetcher:    discordFetcher,
		webDir:            findWebDir(),
	}

	server.setupRoutes()

	return server
}

func (s *Server) setupRoutes() {
	s.router.GET("/health", s.healthCheck)

	api := s.router.Group("/api", s.basicAuth())
	{
		api.GET("/config", s.getConfig)
		api.PUT("/config", s.updateConfig)

		api.GET("/config/discord", s.getDiscordConfig)
		api.PUT("/config/discord", s.updateDiscordConfig)

		api.GET("/config/ai", s.getAIConfig)
		api.PUT("/config/ai", s.updateAIConfig)

		api.GET("/blacklist", s.getBlacklist)
		api.POST("/blacklist", s.addToBlacklist)
		api.DELETE("/blacklist/:type/:id", s.removeFromBlacklist)

		api.GET("/whitelist", s.getWhitelist)
		api.POST("/whitelist", s.addToWhitelist)
		api.DELETE("/whitelist/:type/:id", s.removeFromWhitelist)

		api.GET("/memories/:userID", s.getMemories)
		api.GET("/memories/:userID/search", s.searchMemories)
		api.DELETE("/memories/:userID/:id", s.deleteMemory)
		api.DELETE("/memories/:userID", s.clearMemories)

		api.GET("/profiles/:userID", s.getProfile)
		api.PUT("/profiles/:userID", s.updateProfile)
		api.DELETE("/profiles/:userID", s.deleteProfile)

		api.POST("/consolidate/:userID", s.triggerConsolidation)

		api.GET("/logs", s.getLogs)

		api.GET("/plugins", s.listPlugins)
		api.POST("/plugins/:name/enable", s.enablePlugin)
		api.POST("/plugins/:name/disable", s.disablePlugin)

		api.GET("/stats", s.getStats)
		api.GET("/stats/:userID", s.getUserStats)
	}

	s.router.Static("/static", filepath.Join(s.webDir, "static"))
	s.router.StaticFile("/", filepath.Join(s.webDir, "index.html"))
	s.router.StaticFile("/favicon.ico", filepath.Join(s.webDir, "favicon.ico"))

	s.router.NoRoute(func(c *gin.Context) {
		c.File(filepath.Join(s.webDir, "index.html"))
	})
}

func (s *Server) basicAuth() gin.HandlerFunc {
	return gin.BasicAuth(gin.Accounts{
		s.cfg().Web.Username: s.cfg().Web.Password,
	})
}

func (s *Server) Start() error {
	if !s.cfg().Web.Enabled {
		logger.Info("WebUI is disabled")
		return nil
	}

	s.server = &http.Server{
		Addr:    ":" + strconv.Itoa(s.cfg().Web.Port),
		Handler: s.router,
	}

	logger.Infof("Starting WebUI on port %d", s.cfg().Web.Port)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[web] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()

		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("Web server error: %v", err)
		}
	}()

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}

	logger.Info("Stopping web server...")
	return s.server.Shutdown(ctx)
}

func (s *Server) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"timestamp": time.Now().Unix(),
	})
}
