package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// contextKey is an unexported type for context keys to prevent collisions.
type contextKey string

// csrfCtxKey is the context key for the CSRF token value.
const csrfCtxKey contextKey = "csrf_token"

// CSRSTokenFromContext extracts the signed CSRF token from a request context.
// Returns empty string if no token is set.
func CSRFTokenFromContext(ctx context.Context) string {
	t, _ := ctx.Value(csrfCtxKey).(string)
	return t
}

// GenerateCSRFToken generates a cryptographically random 32-byte token
// and returns it as a hex-encoded string.
func GenerateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate csrf token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// signToken signs a token with HMAC-SHA256 using the given secret.
// Returns a string in the format "token.signature" where both parts are hex-encoded.
func signToken(token string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(token))
	sig := hex.EncodeToString(mac.Sum(nil))
	return token + "." + sig
}

// verifySignedToken verifies the HMAC-SHA256 signature of a signed token
// and returns the original unsigned token. The input must be in "token.signature" format.
func verifySignedToken(signed string, secret []byte) (string, error) {
	lastDot := strings.LastIndex(signed, ".")
	if lastDot <= 0 || lastDot == len(signed)-1 {
		return "", fmt.Errorf("invalid csrf token format")
	}
	token := signed[:lastDot]
	sig := signed[lastDot+1:]

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(token))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", fmt.Errorf("invalid csrf token signature")
	}
	return token, nil
}

// CSRFMiddleware returns a Middleware that implements Double-Submit Cookie
// CSRF protection with HMAC-SHA256 signing.
//
// For GET and HEAD requests, it generates a signed token, sets it as a
// non-HttpOnly cookie (csrf_token), and stores the signed token in the
// request context for use by templates.
//
// For POST, PUT, and DELETE requests, it validates the token by:
//  1. Verifying the HMAC signature of the cookie value
//  2. Verifying the HMAC signature of the form field "csrf_token"
//  3. Comparing the extracted original tokens for equality
//
// POST /login and POST /logout are excluded from CSRF validation.
func CSRFMiddleware(secret []byte) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exclude POST /login and /logout from CSRF validation
			if r.Method == http.MethodPost && (r.URL.Path == "/login" || r.URL.Path == "/logout") {
				next.ServeHTTP(w, r)
				return
			}

			switch r.Method {
			case http.MethodGet, http.MethodHead:
				token, err := GenerateCSRFToken()
				if err != nil {
					http.Error(w, "Internal server error", http.StatusInternalServerError)
					return
				}
				signed := signToken(token, secret)
				http.SetCookie(w, &http.Cookie{
					Name:     "csrf_token",
					Value:    signed,
					Path:     "/",
					HttpOnly: false,
					SameSite: http.SameSiteStrictMode,
				})
				ctx := context.WithValue(r.Context(), csrfCtxKey, signed)
				next.ServeHTTP(w, r.WithContext(ctx))

			case http.MethodPost, http.MethodPut, http.MethodDelete:
				cookie, err := r.Cookie("csrf_token")
				if err != nil {
					http.Error(w, "CSRF validation failed", http.StatusForbidden)
					return
				}
				formToken := r.FormValue("csrf_token")
				if formToken == "" {
					http.Error(w, "CSRF validation failed", http.StatusForbidden)
					return
				}

				// Verify HMAC signature of form field value
				formRaw, err := verifySignedToken(formToken, secret)
				if err != nil {
					http.Error(w, "CSRF validation failed", http.StatusForbidden)
					return
				}
				// Verify HMAC signature of cookie value
				cookieRaw, err := verifySignedToken(cookie.Value, secret)
				if err != nil {
					http.Error(w, "CSRF validation failed", http.StatusForbidden)
					return
				}
				// Double-Submit check: compare extracted original tokens
				if formRaw != cookieRaw {
					http.Error(w, "CSRF validation failed", http.StatusForbidden)
					return
				}

				ctx := context.WithValue(r.Context(), csrfCtxKey, formToken)
				next.ServeHTTP(w, r.WithContext(ctx))

			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}

// CSRFTemplateField generates an HTML hidden input field containing the CSRF
// token from the request context. Returns empty string if no token is set.
//
// The returned HTML is safe because CSRF tokens are hex-encoded strings
// that do not contain HTML-special characters.
func CSRFTemplateField(r *http.Request) template.HTML {
	token := CSRFTokenFromContext(r.Context())
	if token == "" {
		return template.HTML("")
	}
	return template.HTML(`<input type="hidden" name="csrf_token" value="` + template.HTMLEscapeString(token) + `">`)
}
