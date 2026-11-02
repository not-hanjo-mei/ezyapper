// Package web provides an HTTP API server and WebUI for managing the bot.
package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/hotreload"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
)

// Server provides the WebUI HTTP server and API endpoints for bot management.
type Server struct {
	// router is the current handler chain. Config hot reload rebuilds it
	// (handlers capture credentials and the session store) and swaps it
	// under the running listener.
	router           atomic.Pointer[http.Handler]
	configStore      *atomic.Value // stores *config.Config
	memoryStore      memory.MemoryStore
	profileStore     memory.ProfileStore
	consolidationMgr memory.ConsolidationManager
	pluginManager    *plugin.Manager
	csrfSecret       []byte
	configApplier    ConfigApplier
	toolRefresher    PluginToolRefresher
	mu               sync.RWMutex
	wg               sync.WaitGroup

	// srvMu guards the listener state below and sessionStore swaps.
	srvMu          sync.Mutex
	server         *http.Server
	listenPort     int
	sessionStore   *SessionStore
	startTime      time.Time
	discordFetcher DiscordMessageFetcher
	discordInfo    DiscordInfoProvider
}

func (s *Server) cfg() *config.Config {
	c, ok := s.configStore.Load().(*config.Config)
	if !ok {
		panic("configStore contains non-*config.Config value")
	}
	return c
}

type DiscordMessageFetcher any

// DiscordInfoProvider provides read-only access to Discord metadata (channel names, user names, guild names).
// All methods are non-blocking — they read from Discord's in-memory state cache only.
type DiscordInfoProvider interface {
	GetChannelName(channelID string) string
	GetUserName(guildID, userID string) string
	GetGuildName(guildID string) string
}

// ConfigApplier makes a validated config live. In the bot it is the hot
// reload coordinator, so a dashboard save goes through exactly the same
// prepare/commit/rollback path as an edit of the config file.
type ConfigApplier interface {
	ApplyConfig(ctx context.Context, next *config.Config) error
}

type PluginToolRefresher interface {
	RefreshPluginTools()
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
		sessionStore:     NewSessionStore(webCfg.SessionTTLMin, webCfg.SessionCleanupIntervalMin),
		csrfSecret:       csrfSecret,
		toolRefresher:    toolRefresher,
	}

	server.router.Store(ptrTo(server.buildRouter(cfg, server.sessionStore)))

	return server
}

func ptrTo(h http.Handler) *http.Handler { return &h }

// SetConfigApplier routes dashboard config saves through a (with hot
// reload: validate, prepare, commit or roll back). Call it before Start.
func (s *Server) SetConfigApplier(a ConfigApplier) {
	s.configApplier = a
	s.router.Store(ptrTo(s.buildRouter(s.cfg(), s.currentSessionStore())))
}

func (s *Server) currentSessionStore() *SessionStore {
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	return s.sessionStore
}

// ServeHTTP dispatches to the current router.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.router.Load()).ServeHTTP(w, r)
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

// buildRouter builds the handler chain for cfg. Handlers that need live
// values read them from the config store per request; the login handler,
// session middleware and logout capture the credentials and the session
// store, which is why a change under operations.web rebuilds the router.
func (s *Server) buildRouter(cfg *config.Config, sessions *SessionStore) http.Handler {
	mux := http.NewServeMux()

	tmpl, err := LoadTemplates()
	if err != nil {
		logger.Fatalf("[web] Failed to load templates: %v", err)
	}
	// Static files
	staticDir := findStaticDir()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	// Auth routes (excluded from session requirement via SessionMiddleware)
	mux.HandleFunc("/login", LoginHandler(sessions, cfg.Web.Username, cfg.Web.Password, tmpl.Login(), cfg.Web.SessionTTLMin))
	mux.HandleFunc("/logout", LogoutHandler(sessions))

	// Dashboard
	stats := NewStatsProvider(s.memoryStore, s.profileStore, s.configStore)
	mux.HandleFunc("/", DashboardHandler(stats, s.startTime, tmpl))

	// Configuration
	mux.HandleFunc("/config", ConfigHandler(s.configStore, tmpl, s.configApplier))

	// Channels (exact + sub-paths for blacklist/whitelist CRUD)
	chHandler := ChannelsHandlerWithApplier(s.configStore, s.discordInfo, tmpl, s.configApplier)
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
	mux.HandleFunc("/logs", LogsHandler("", s.configStore, tmpl))

	// Chain middleware: Security -> CSRF -> Session -> mux
	return Chain(mux,
		securityHeaders,
		CSRFMiddleware(s.csrfSecret),
		SessionMiddleware(sessions),
	)
}

// Start begins listening on the configured port. No-op if web is disabled.
// A port that cannot be bound is reported here rather than only logged
// from the serving goroutine.
func (s *Server) Start() error {
	cfg := s.cfg()
	if !cfg.Web.Enabled {
		logger.Info("[web] WebUI is disabled")
		return nil
	}
	ln, err := listenWeb(cfg.Web.Port)
	if err != nil {
		return err
	}
	s.srvMu.Lock()
	s.serveLocked(ln, cfg.Web.Port)
	s.srvMu.Unlock()
	return nil
}

func listenWeb(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return nil, fmt.Errorf("listen on web port %d: %w", port, err)
	}
	return ln, nil
}

// serveLocked starts serving ln. srvMu must be held.
func (s *Server) serveLocked(ln net.Listener, port int) {
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 30 * time.Second,
	}
	s.server = srv
	s.listenPort = port
	s.sessionStore.SetWG(&s.wg)
	logger.Infof("[web] Starting WebUI on port %d", port)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[web] panic in HTTP listener: %v", r)
			}
		}()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf("[web] Web server error: %v", err)
		}
	}()
}

// webShutdownGrace bounds the graceful shutdown of a listener replaced by
// hot reload.
const webShutdownGrace = 10 * time.Second

// retire shuts srv down in the background. It must not block: the request
// that saved the config may itself be running on srv.
func retire(srv *http.Server) {
	if srv == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), webShutdownGrace)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Warnf("[web] shutting down the previous listener: %v", err)
		}
	}()
}

// Stop gracefully shuts down the web server.
func (s *Server) Stop(ctx context.Context) error {
	s.srvMu.Lock()
	s.sessionStore.Stop()
	srv := s.server
	s.server = nil
	s.srvMu.Unlock()

	if srv == nil {
		return nil
	}

	logger.Info("[web] Stopping web server...")

	err := srv.Shutdown(ctx)

	// Wait for tracked goroutines with remaining deadline
	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[web] panic in shutdown waiter: %v", r)
				close(done)
			}
		}()
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

// Name implements hotreload.Component.
func (s *Server) Name() string { return "web dashboard" }

// Prepare implements hotreload.Component for operations.web.*. The router
// is rebuilt (credentials and session settings are captured by handlers);
// a credential or session-lifetime change also starts a fresh session
// store, which signs everyone out. Turning the dashboard on or moving it
// to another port binds the new port here, so a port in use rejects the
// reload and the old listener keeps serving. With an unchanged port the
// new router is swapped under the running listener.
func (s *Server) Prepare(_ context.Context, old, next *config.Config, changes hotreload.Changes) (hotreload.Pending, error) {
	if !changes.Any("operations.web") {
		return nil, nil
	}
	o, n := old.Web, next.Web

	s.srvMu.Lock()
	running := s.server != nil
	runningPort := s.listenPort
	sessions := s.sessionStore
	s.srvMu.Unlock()

	var newSessions *SessionStore
	if o.Username != n.Username || o.Password != n.Password ||
		o.SessionTTLMin != n.SessionTTLMin || o.SessionCleanupIntervalMin != n.SessionCleanupIntervalMin {
		newSessions = NewSessionStore(n.SessionTTLMin, n.SessionCleanupIntervalMin)
		sessions = newSessions
	}

	var ln net.Listener
	if n.Enabled && (!running || runningPort != n.Port) {
		var err error
		ln, err = listenWeb(n.Port)
		if err != nil {
			if newSessions != nil {
				newSessions.Stop()
			}
			return nil, err
		}
	}
	router := s.buildRouter(next, sessions)

	return hotreload.Funcs{
		CommitFunc: func() {
			s.srvMu.Lock()
			defer s.srvMu.Unlock()
			if newSessions != nil {
				s.sessionStore.Stop()
				s.sessionStore = newSessions
				s.sessionStore.SetWG(&s.wg)
				logger.Info("[web] dashboard credentials or session settings changed: existing sessions signed out")
			}
			s.router.Store(ptrTo(router))
			switch {
			case !n.Enabled:
				if s.server != nil {
					retire(s.server)
					s.server = nil
					logger.Info("[web] WebUI disabled")
				}
			case ln != nil:
				prev := s.server
				s.serveLocked(ln, n.Port)
				retire(prev)
			}
		},
		AbortFunc: func() {
			if ln != nil {
				_ = ln.Close()
			}
			if newSessions != nil {
				newSessions.Stop()
			}
		},
	}, nil
}
