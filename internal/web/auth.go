package web

import (
	"crypto/subtle"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/logger"
)

// loginRateLimiter provides simple in-memory rate limiting for login attempts.
// It tracks attempts per IP within a sliding window and rejects requests
// that exceed the configured maximum.
type loginRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string][]time.Time
	maxAttempts int
	window      time.Duration
}

// allow reports whether a login attempt from the given IP should be permitted.
// It cleans up expired entries and returns false if the IP has exceeded the
// attempt limit within the configured time window.
func (l *loginRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	recent := l.attempts[ip][:0]
	for _, t := range l.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	l.attempts[ip] = recent

	if len(recent) >= l.maxAttempts {
		return false
	}

	l.attempts[ip] = append(l.attempts[ip], now)

	l.pruneStaleLocked(cutoff)
	return true
}

func (l *loginRateLimiter) pruneStaleLocked(cutoff time.Time) {
	if len(l.attempts) <= 1000 {
		return
	}
	for ip, times := range l.attempts {
		allOld := true
		for _, t := range times {
			if t.After(cutoff) {
				allOld = false
				break
			}
		}
		if allOld {
			delete(l.attempts, ip)
		}
	}
}

// LoginHandler returns an http.HandlerFunc for GET and POST /login.
//
// GET /login renders the login form. If the user already has a valid session
// (from context), they are redirected to "/".
//
// POST /login validates the username and password against the provided
// credentials. On success, a new session is created and stored in a cookie,
// and the user is redirected to "/". On failure, the login page is re-rendered
// with an "Invalid credentials" error message. The error message does not
// distinguish between unknown username and wrong password.
func LoginHandler(store *SessionStore, username, password string, loginTmpl *template.Template, sessionTTLMin int) http.HandlerFunc {
	limiter := &loginRateLimiter{
		attempts:    make(map[string][]time.Time),
		maxAttempts: 5,
		window:      time.Minute,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if SessionFromContext(r.Context()) != nil {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			csrfToken := CSRFTokenFromContext(r.Context())
			data := &PageData{
				Title:     "Login",
				CSRFToken: csrfToken,
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := loginTmpl.ExecuteTemplate(w, "login", data); err != nil {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}

		case http.MethodPost:
			clientIP := clientIP(r)
			if !limiter.allow(clientIP) {
				http.Error(w, "Too many login attempts. Please try again later.", http.StatusTooManyRequests)
				return
			}

			if err := r.ParseForm(); err != nil {
				renderLoginError(w, loginTmpl, "Invalid credentials")
				return
			}

			formUser := r.FormValue("username")
			formPass := r.FormValue("password")

			if formUser == "" || formPass == "" || subtle.ConstantTimeCompare([]byte(formUser), []byte(username)) != 1 || subtle.ConstantTimeCompare([]byte(formPass), []byte(password)) != 1 {
				// All checks done in constant time to prevent username enumeration.
				// Empty-credential check is separate because it can't be constant-time
				// against known values, but the error message is identical regardless.
				renderLoginError(w, loginTmpl, "Invalid credentials")
				return
			}

			session, err := store.CreateSession(username)
			if err != nil {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			http.SetCookie(w, &http.Cookie{
				Name:     "__Host-session_id",
				Value:    session.ID,
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   sessionTTLMin * 60,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// clientIP extracts the client IP address from the request, checking common
// proxy headers before falling back to RemoteAddr.
func clientIP(r *http.Request) string {
	// Only trust proxy headers if request comes from localhost
	remoteIP := extractIP(r.RemoteAddr)
	if isLocalhost(remoteIP) {
		if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
			// X-Forwarded-For can contain a comma-separated proxy chain.
			// Extract the leftmost (original client) IP.
			if idx := strings.IndexByte(ip, ','); idx > 0 {
				ip = strings.TrimSpace(ip[:idx])
			}
			return ip
		}
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return ip
		}
	}
	return remoteIP
}

func extractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func isLocalhost(ip string) bool {
	if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
		return true
	}
	// Check if it's a loopback address
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// LogoutHandler returns an http.HandlerFunc for POST /logout.
//
// It deletes the session from the store, clears the session cookie, and
// redirects to /login. If no valid session is present, it still clears the
// cookie and redirects.
func LogoutHandler(store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Defense-in-depth: CSRFMiddleware already validates the token,
		// but we explicitly check presence here as an additional guard.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		if r.FormValue("csrf_token") == "" {
			http.Error(w, "CSRF validation failed", http.StatusForbidden)
			return
		}

		session := SessionFromContext(r.Context())
		if session != nil {
			store.DeleteSession(session.ID)
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "__Host-session_id",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

// renderLoginError renders the login template with an error flash message.
func renderLoginError(w http.ResponseWriter, loginTmpl *template.Template, message string) {
	token, err := GenerateCSRFToken()
	if err != nil {
		logger.Errorf("[web] failed to generate CSRF token for login error: %v", err)
		// Non-fatal: render page without CSRF token
		token = ""
	}
	data := &PageData{
		Title:     "Login",
		CSRFToken: token,
		Flash: &FlashMessage{
			Type:    "error",
			Message: message,
		},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := loginTmpl.ExecuteTemplate(w, "login", data); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
