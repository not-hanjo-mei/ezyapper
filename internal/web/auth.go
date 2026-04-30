package web

import (
	"html/template"
	"net/http"

	"ezyapper/internal/logger"
)

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
func LoginHandler(store *SessionStore, username, password string, loginTmpl *template.Template) http.HandlerFunc {
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
			if err := r.ParseForm(); err != nil {
				renderLoginError(w, loginTmpl, "Invalid credentials")
				return
			}

			formUser := r.FormValue("username")
			formPass := r.FormValue("password")

			if formUser == "" || formPass == "" || formUser != username || formPass != password {
				renderLoginError(w, loginTmpl, "Invalid credentials")
				return
			}

			session, err := store.CreateSession(username)
			if err != nil {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			http.SetCookie(w, &http.Cookie{
				Name:     "session_id",
				Value:    session.ID,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   1800,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
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

		session := SessionFromContext(r.Context())
		if session != nil {
			store.DeleteSession(session.ID)
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
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
		logger.Errorf("[Web] failed to generate CSRF token for login error: %v", err)
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
