package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/logger"
)

// sessionCtxKey is the context key for the authenticated Session.
const sessionCtxKey contextKey = "session"

// Session represents an authenticated web session.
type Session struct {
	ID        string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore provides an in-memory session store with periodic cleanup
// of expired sessions.
type SessionStore struct {
	mu              sync.RWMutex
	sessions        map[string]*Session
	stopCh          chan struct{}
	stopped         bool
	wg              *sync.WaitGroup
	ttl             time.Duration
	cleanupInterval time.Duration
}

// NewSessionStore creates a new SessionStore and starts the cleanup goroutine.
// Call Stop to stop the cleanup goroutine.
func NewSessionStore(ttlMin int, cleanupIntervalMin int) *SessionStore {
	store := &SessionStore{
		sessions:        make(map[string]*Session),
		stopCh:          make(chan struct{}),
		ttl:             time.Duration(ttlMin) * time.Minute,
		cleanupInterval: time.Duration(cleanupIntervalMin) * time.Minute,
	}
	go store.cleanupLoop()
	return store
}

// CreateSession creates a new session for the given username.
// The session ID is a cryptographically random 32-byte value encoded as hex.
// Sessions expire after 30 minutes.
func (s *SessionStore) CreateSession(username string) (*Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	now := time.Now()
	session := &Session{
		ID:        hex.EncodeToString(b),
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}

	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()

	return session, nil
}

// GetSession retrieves a session by ID.
// Returns an error if the session is not found or has expired.
// Expired sessions are automatically cleaned up on lookup.
func (s *SessionStore) GetSession(sessionID string) (*Session, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	if time.Now().After(session.ExpiresAt) {
		s.DeleteSession(sessionID)
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

// DeleteSession removes a session from the store by ID.
func (s *SessionStore) DeleteSession(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

// SetWG sets the WaitGroup for goroutine lifecycle tracking.
// Must be called before the cleanup goroutine exits.
func (s *SessionStore) SetWG(wg *sync.WaitGroup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wg != nil {
		return
	}
	s.wg = wg
	// Only add to WaitGroup if cleanup loop is still running
	if !s.stopped {
		wg.Add(1)
	}
}

// Stop stops the cleanup goroutine. Safe to call multiple times.
func (s *SessionStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	close(s.stopCh)
}

func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()
	defer func() {
		s.mu.RLock()
		if s.wg != nil {
			s.wg.Done()
		}
		s.mu.RUnlock()
	}()
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[web] panic in session cleanup: %v", r)
		}
	}()

	for {
		select {
		case <-ticker.C:
			s.cleanupExpired()
		case <-s.stopCh:
			return
		}
	}
}

func (s *SessionStore) cleanupExpired() {
	now := time.Now()
	s.mu.Lock()
	for id, session := range s.sessions {
		if now.After(session.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
}

// SessionFromContext extracts the Session from a request context.
// Returns nil if no session is present or the value is not a *Session.
func SessionFromContext(ctx context.Context) *Session {
	s, ok := ctx.Value(sessionCtxKey).(*Session)
	if !ok {
		return nil
	}
	return s
}

// isExcludedPath returns true if the given path should not require session auth.
func isExcludedPath(path string) bool {
	if path == "/login" || path == "/favicon.ico" {
		return true
	}
	if strings.HasPrefix(path, "/static/") {
		return true
	}
	return false
}

// SessionMiddleware returns a Middleware that validates session cookies.
//
// For excluded paths (/login, /static/, /favicon.ico), the middleware will
// still check for a valid session cookie and store it in context if found,
// but will not redirect if missing.
//
// For all other paths, a valid session is required. Requests without a valid
// session cookie are redirected to /login.
func SessionMiddleware(store *SessionStore) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			excluded := isExcludedPath(r.URL.Path)

			// Try to find existing session from cookie
			cookie, err := r.Cookie("session_id")
			var session *Session
			if err == nil {
				var sessErr error
				session, sessErr = store.GetSession(cookie.Value)
				if sessErr != nil {
					logger.Debugf("[Web] invalid session cookie: %v", sessErr)
				}
			}

			// If session found, store in context
			if session != nil {
				ctx := context.WithValue(r.Context(), sessionCtxKey, session)
				r = r.WithContext(ctx)
			}

			// Non-excluded paths require a valid session
			if !excluded && session == nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuth is an alias for SessionMiddleware for semantic clarity.
func RequireAuth(store *SessionStore) Middleware {
	return SessionMiddleware(store)
}

// Middleware wraps an http.Handler with pre/post-processing logic.
type Middleware func(http.Handler) http.Handler

// Chain composes middlewares around a final handler.
// The first middleware in the list becomes the outermost layer.
func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
