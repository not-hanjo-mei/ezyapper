package web

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// testAuthTemplate returns a realistic login template for testing MD3 rendering.
func testAuthTemplate() *template.Template {
	tmpl := template.New("").Option("missingkey=error")
	login := `{{define "login"}}<!DOCTYPE html>
<html>
<head><title>{{.Title}} — EZyapper</title></head>
<body>
<div class="md3-login-page">
	<div class="md3-card md3-card--outlined md3-login-card">
		<div class="md3-card__content">
			<div class="md3-login__header">
				<span class="md3-login__logo">EZ</span>
				<h1 class="md3-login__title">EZyapper</h1>
				<p class="md3-login__subtitle">WebUI Management</p>
			</div>
			{{if .Flash}}
			<div class="md3-toast md3-toast--{{.Flash.Type}}" role="alert">
				<span>{{.Flash.Message}}</span>
			</div>
			{{end}}
			<form method="POST" action="/login" class="md3-form">
				<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
				<div class="md3-form-field">
					<label class="md3-form-field__label" for="field_username">Username</label>
					<input id="field_username" name="username" type="text" class="md3-text-field" required autofocus>
				</div>
				<div class="md3-form-field">
					<label class="md3-form-field__label" for="field_password">Password</label>
					<input id="field_password" name="password" type="password" class="md3-text-field" required>
				</div>
				<button type="submit" class="md3-btn md3-btn--filled" style="width:100%">Login</button>
			</form>
		</div>
	</div>
</div>
</body>
</html>{{end}}`
	return template.Must(tmpl.Parse(login))
}

func TestLoginHandler_GET_RendersLoginForm(t *testing.T) {
	store := NewSessionStore(30, 5)
	defer store.Stop()

	handler := LoginHandler(store, "admin", "secret", testAuthTemplate())

	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()

	// Simulate CSRF middleware setting token in context
	csrfToken, _ := GenerateCSRFToken()
	ctx := context.WithValue(r.Context(), csrfCtxKey, csrfToken)
	r = r.WithContext(ctx)

	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "Login") {
		t.Error("response should contain page title 'Login'")
	}
	if !strings.Contains(body, "md3-login-page") {
		t.Error("response should contain MD3 login page wrapper")
	}
	if !strings.Contains(body, "md3-login-card") {
		t.Error("response should contain MD3 login card")
	}
	if !strings.Contains(body, "md3-btn--filled") {
		t.Error("response should contain MD3 filled button")
	}
	if !strings.Contains(body, "csrf_token") {
		t.Error("response should contain CSRF token field")
	}
	if !strings.Contains(body, "username") {
		t.Error("response should contain a username field")
	}
	if !strings.Contains(body, "password") {
		t.Error("response should contain a password field")
	}
}

func TestLoginHandler_GET_RedirectsIfLoggedIn(t *testing.T) {
	store := NewSessionStore(30, 5)
	defer store.Stop()

	session, err := store.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	handler := LoginHandler(store, "admin", "secret", testAuthTemplate())

	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	ctx := context.WithValue(r.Context(), sessionCtxKey, session)
	r = r.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}
}

func TestLoginHandler_POST_InvalidCredentials(t *testing.T) {
	store := NewSessionStore(30, 5)
	defer store.Stop()

	handler := LoginHandler(store, "admin", "secret", testAuthTemplate())

	tests := []struct {
		name     string
		username string
		password string
	}{
		{"wrong password", "admin", "wrongpass"},
		{"wrong username", "hacker", "secret"},
		{"both wrong", "hacker", "wrongpass"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := url.Values{"username": {tc.username}, "password": {tc.password}}.Encode()
			r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)

			if rr.Code != http.StatusOK {
				t.Errorf("expected 200 for invalid credentials, got %d", rr.Code)
			}
			bodyStr := rr.Body.String()
			if !strings.Contains(bodyStr, "Invalid credentials") {
				t.Error("response should contain 'Invalid credentials'")
			}
			if !strings.Contains(bodyStr, "md3-toast--error") {
				t.Error("response should contain error toast")
			}
			if !strings.Contains(bodyStr, "Login") {
				t.Error("response should render login form")
			}
		})
	}
}

func TestLoginHandler_POST_ValidCredentials(t *testing.T) {
	store := NewSessionStore(30, 5)
	defer store.Stop()

	handler := LoginHandler(store, "admin", "secret", testAuthTemplate())

	body := url.Values{"username": {"admin"}, "password": {"secret"}}.Encode()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	var sessionCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "session_id" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session_id cookie to be set")
	}
	if sessionCookie.Value == "" {
		t.Error("session_id cookie value should not be empty")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session_id cookie should be HttpOnly")
	}
	if sessionCookie.MaxAge != 1800 {
		t.Errorf("expected MaxAge 1800, got %d", sessionCookie.MaxAge)
	}

	stored, err := store.GetSession(sessionCookie.Value)
	if err != nil {
		t.Fatalf("session should exist in store: %v", err)
	}
	if stored.Username != "admin" {
		t.Errorf("expected username admin, got %q", stored.Username)
	}
}

func TestLoginHandler_POST_MissingUsername(t *testing.T) {
	store := NewSessionStore(30, 5)
	defer store.Stop()

	handler := LoginHandler(store, "admin", "secret", testAuthTemplate())

	tests := []struct {
		name     string
		username string
		password string
	}{
		{"empty username", "", "secret"},
		{"empty password", "admin", ""},
		{"both empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := url.Values{"username": {tc.username}, "password": {tc.password}}.Encode()
			r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)

			if rr.Code != http.StatusOK {
				t.Errorf("expected 200 for missing fields, got %d", rr.Code)
			}
			bodyStr := rr.Body.String()
			if !strings.Contains(bodyStr, "Invalid credentials") {
				t.Error("response should contain 'Invalid credentials'")
			}
			if !strings.Contains(bodyStr, "md3-toast--error") {
				t.Error("response should contain error toast")
			}
		})
	}
}

func TestLogoutHandler_POST_ClearsSession(t *testing.T) {
	store := NewSessionStore(30, 5)
	defer store.Stop()

	session, err := store.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	handler := LogoutHandler(store)

	body := url.Values{}.Encode()
	r := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(r.Context(), sessionCtxKey, session)
	r = r.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}

	_, err = store.GetSession(session.ID)
	if err == nil {
		t.Error("session should be deleted from store after logout")
	}

	var sessionCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "session_id" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session_id cookie in response")
	}
	if sessionCookie.MaxAge != -1 {
		t.Errorf("expected MaxAge -1, got %d", sessionCookie.MaxAge)
	}
	if sessionCookie.Value != "" {
		t.Errorf("expected empty cookie value, got %q", sessionCookie.Value)
	}
}

func TestLogoutHandler_NoSession_StillRedirects(t *testing.T) {
	store := NewSessionStore(30, 5)
	defer store.Stop()

	handler := LogoutHandler(store)

	body := url.Values{}.Encode()
	r := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}

	// Verify no Set-Cookie with session_id that would be set for deletion
	// (just verify we redirect properly regardless)
}

func TestLoginPageTemplate_RendersWithoutSidebar(t *testing.T) {
	store := NewSessionStore(30, 5)
	defer store.Stop()

	handler := LoginHandler(store, "admin", "secret", testAuthTemplate())

	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	csrfToken, _ := GenerateCSRFToken()
	ctx := context.WithValue(r.Context(), csrfCtxKey, csrfToken)
	r = r.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	body := rr.Body.String()
	if strings.Contains(body, "md3-sidebar") {
		t.Error("login page should NOT render sidebar")
	}
	if strings.Contains(body, "md3-app") {
		t.Error("login page should NOT contain md3-app wrapper")
	}
	if strings.Contains(body, "md3-main") {
		t.Error("login page should NOT contain md3-main")
	}
}
