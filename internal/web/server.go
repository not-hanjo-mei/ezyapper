// Package web provides an HTTP API server and WebUI for managing the bot.
package web

import (
	"context"
	"crypto/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
	"ezyapper/internal/types"
)

// Server provides the WebUI HTTP server and API endpoints for bot management.
type Server struct {
	router           http.Handler
	configStore      *atomic.Value // stores *config.Config
	memoryStore      memory.MemoryStore
	profileStore     memory.ProfileStore
	consolidationMgr memory.ConsolidationManager
	pluginManager    *plugin.Manager
	server           *http.Server
	sessionStore     *SessionStore
	csrfSecret       []byte
	runtimeApplier   RuntimeConfigApplier
	toolRefresher    PluginToolRefresher
	mu               sync.RWMutex
	wg               sync.WaitGroup
	startTime        time.Time
	discordFetcher   DiscordMessageFetcher
	discordInfo      DiscordInfoProvider
	webDir           string
}

func (s *Server) cfg() *config.Config {
	c, ok := s.configStore.Load().(*config.Config)
	if !ok {
		panic("configStore contains non-*config.Config value")
	}
	return c
}

type DiscordMessageFetcher interface {
	FetchUserMessages(ctx context.Context, channelID string, userID string, limit int) ([]*types.DiscordMessage, error)
}

// DiscordInfoProvider provides read-only access to Discord metadata (channel names, user names, guild names).
// All methods are non-blocking 鈥?they read from Discord's in-memory state cache only.
type DiscordInfoProvider interface {
	GetChannelName(channelID string) string
	GetUserName(guildID, userID string) string
	GetGuildName(guildID string) string
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
		staticDir := filepath.Join(dir, "static")
		if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
			return dir
		}
	}

	return "./web"
}

func findStaticDir() string {
	candidates := []string{
		"./internal/web/static",
		"../internal/web/static",
		"./web/static",
		"./static",
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "internal", "web", "static"),
			filepath.Join(exeDir, "static"),
		)
	}

	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}

	return "./internal/web/static"
}

// NewServer creates a new web server with the given stores, plugin manager, and Discord info provider.
func NewServer(cfgStore *atomic.Value, memStore memory.MemoryStore, profileStore memory.ProfileStore, conMgr memory.ConsolidationManager, pluginManager *plugin.Manager, discordFetcher DiscordMessageFetcher, discordInfo DiscordInfoProvider) *Server {
	var runtimeApplier RuntimeConfigApplier
	if ra, ok := discordFetcher.(RuntimeConfigApplier); ok {
		runtimeApplier = ra
	}
	var toolRefresher PluginToolRefresher
	if tr, ok := discordFetcher.(PluginToolRefresher); ok {
		toolRefresher = tr
	}

	csrfSecret := make([]byte, 32)
	if _, err := rand.Read(csrfSecret); err != nil {
		logger.Fatalf("[web] failed to generate CSRF secret: %v", err)
	}

	cfg, ok := cfgStore.Load().(*config.Config)
	if !ok {
		panic("configStore contains non-*config.Config value")
	}
	webCfg := cfg.Web
	server := &Server{
		configStore:      cfgStore,
		memoryStore:      memStore,
		profileStore:     profileStore,
		consolidationMgr: conMgr,
		pluginManager:    pluginManager,
		startTime:        time.Now(),
		discordFetcher:   discordFetcher,
		discordInfo:      discordInfo,
		webDir:           findWebDir(),
		sessionStore:     NewSessionStore(webCfg.SessionTTLMin, webCfg.SessionCleanupIntervalMin),
		csrfSecret:       csrfSecret,
		runtimeApplier:   runtimeApplier,
		toolRefresher:    toolRefresher,
	}

	server.setupRoutes()

	return server
}

// securityHeaders wraps an http.Handler to inject security-related HTTP headers.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data:; script-src 'self' 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setupRoutes() {
	mux := http.NewServeMux()

	tmpl, err := LoadTemplates()
	if err != nil {
		logger.Fatalf("[web] Failed to load templates: %v", err)
	}
	// Static files
	staticDir := findStaticDir()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	// Auth routes (excluded from session requirement via SessionMiddleware)
	mux.HandleFunc("/login", LoginHandler(s.sessionStore, s.cfg().Web.Username, s.cfg().Web.Password, tmpl.Login(), s.cfg().Web.SessionTTLMin))
	mux.HandleFunc("/logout", LogoutHandler(s.sessionStore))

	// Dashboard
	stats := NewStatsProvider(s.memoryStore, s.profileStore, s.configStore)
	mux.HandleFunc("/", DashboardHandler(stats, s.startTime, tmpl))

	// Configuration
	mux.HandleFunc("/config", ConfigHandler(s.configStore, tmpl, s.runtimeApplier))

	// Channels (exact + sub-paths for blacklist/whitelist CRUD)
	chHandler := ChannelsHandler(s.configStore, s.discordInfo, tmpl)
	mux.HandleFunc("/channels", chHandler)
	mux.HandleFunc("/channels/blacklist/add", chHandler)
	mux.HandleFunc("/channels/blacklist/remove", chHandler)
	mux.HandleFunc("/channels/whitelist/add", chHandler)
	mux.HandleFunc("/channels/whitelist/remove", chHandler)

	// Memories
	memHandler := MemoriesHandler(s.configStore, s.memoryStore, tmpl)
	mux.HandleFunc("/memories", memHandler)
	mux.HandleFunc("/memories/delete", memHandler)

	// Profiles
	profHandler := ProfilesHandler(s.profileStore, tmpl)
	mux.HandleFunc("/profiles", profHandler)
	mux.HandleFunc("/profiles/update", profHandler)

	// Plugins
	plugHandler := PluginsHandler(s.pluginManager, s.toolRefresher, tmpl)
	mux.HandleFunc("/plugins", plugHandler)
	mux.HandleFunc("/plugins/toggle", plugHandler)

	// Logs
	mux.HandleFunc("/logs", LogsHandler(s.cfg().Logging.File, s.configStore, tmpl))

	// Chain middleware: Security 鈫?CSRF 鈫?Session 鈫?mux
	s.router = Chain(mux,
		securityHeaders,
		CSRFMiddleware(s.csrfSecret),
		SessionMiddleware(s.sessionStore),
	)
}

// Start begins listening on the configured port. No-op if web is disabled.
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

	s.sessionStore.SetWG(&s.wg)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("Web server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the web server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}

	logger.Info("Stopping web server...")

	// Signal session cleanup goroutine to exit
	s.sessionStore.Stop()

	err := s.server.Shutdown(ctx)

	// Wait for tracked goroutines with remaining deadline
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		logger.Warnf("[web] goroutines did not finish before context deadline")
	}

	return err
}
