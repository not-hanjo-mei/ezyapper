package web

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// --- SessionStore unit tests ---

func TestCreateSession_GeneratesID(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if session.ID == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestCreateSession_SetsExpiry(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	before := time.Now()
	session, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	after := time.Now()

	if session.CreatedAt.Before(before.Add(-time.Second)) || session.CreatedAt.After(after.Add(time.Second)) {
		t.Error("CreatedAt should be approximately now")
	}

	expectedExpiry := session.CreatedAt.Add(30 * time.Minute)
	if !session.ExpiresAt.Equal(expectedExpiry) {
		t.Errorf("expected ExpiresAt %v, got %v", expectedExpiry, session.ExpiresAt)
	}
}

func TestGetSession_FindsValidSession(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	got, err := store.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.ID != session.ID {
		t.Errorf("expected session ID %q, got %q", session.ID, got.ID)
	}
	if got.Username != "testuser" {
		t.Errorf("expected username %q, got %q", "testuser", got.Username)
	}
}

func TestGetSession_ExpiredReturnsError(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Manually set expiry to the past
	store.mu.Lock()
	store.sessions[session.ID].ExpiresAt = time.Now().Add(-1 * time.Minute)
	store.mu.Unlock()

	_, err = store.GetSession(session.ID)
	if err == nil {
		t.Error("expected error for expired session, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected error containing 'expired', got %q", err.Error())
	}
}

func TestGetSession_NotFoundReturnsError(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	_, err := store.GetSession("nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent session, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error containing 'not found', got %q", err.Error())
	}
}

func TestDeleteSession_RemovesSession(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	store.DeleteSession(session.ID)

	_, err = store.GetSession(session.ID)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestCreateSession_UniqueIDs(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	s1, err := store.CreateSession("user1")
	if err != nil {
		t.Fatalf("first CreateSession failed: %v", err)
	}
	s2, err := store.CreateSession("user2")
	if err != nil {
		t.Fatalf("second CreateSession failed: %v", err)
	}

	if s1.ID == s2.ID {
		t.Error("expected different session IDs for two creates")
	}
}

// --- Test helpers ---

type sessionTestHandler struct {
	called  bool
	session *Session
}

func (h *sessionTestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.session = SessionFromContext(r.Context())
	io.WriteString(w, "ok")
}

// testLoginTemplate returns a minimal template for testing LoginHandler rendering.
func testLoginTemplate() *template.Template {
	tmpl := template.New("").Option("missingkey=error")
	login := `{{define "login"}}<!DOCTYPE html>
<html>
<head><title>Login — EZyapper</title></head>
<body>
<div class="md3-login-page">
	<div class="md3-login-card">
		<div class="md3-login__header">
			<span class="md3-login__logo">EZ</span>
			<h1>EZyapper</h1>
			<p>WebUI Management</p>
		</div>
		{{if .Flash}}
		<div class="md3-toast md3-toast--{{.Flash.Type}}" role="alert">
			<span>{{.Flash.Message}}</span>
		</div>
		{{end}}
		<form method="POST" action="/login">
			<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
			<label>Username: <input name="username" type="text" required></label>
			<label>Password: <input name="password" type="password" required></label>
			<button type="submit">Login</button>
		</form>
	</div>
</div>
</body>
</html>{{end}}`
	return template.Must(tmpl.Parse(login))
}

// --- SessionMiddleware tests ---

func TestSessionMiddleware_RedirectsUnauthenticated(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	handler := &sessionTestHandler{}
	mw := SessionMiddleware(store)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/dashboard", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}

	client := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}
	if handler.called {
		t.Error("handler should not have been called for unauthenticated request")
	}
}

func TestSessionMiddleware_AllowsAuthenticated(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	handler := &sessionTestHandler{}
	mw := SessionMiddleware(store)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/dashboard", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "session_id", Value: session.ID})

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if !handler.called {
		t.Error("handler should have been called")
	}
	if handler.session == nil {
		t.Fatal("expected session in context")
	}
	if handler.session.Username != "testuser" {
		t.Errorf("expected username %q, got %q", "testuser", handler.session.Username)
	}
}

func TestSessionMiddleware_ExcludesLoginPath(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	handler := &sessionTestHandler{}
	mw := SessionMiddleware(store)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /login, got %d", resp.StatusCode)
	}
	if !handler.called {
		t.Error("handler should have been called for /login")
	}
}

func TestSessionMiddleware_ExcludesStaticPath(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	handler := &sessionTestHandler{}
	mw := SessionMiddleware(store)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	tests := []string{
		"/static/css/style.css",
		"/static/js/app.js",
		"/static/",
	}
	for _, path := range tests {
		handler.called = false
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 for %s, got %d", path, resp.StatusCode)
		}
		if !handler.called {
			t.Errorf("handler should have been called for %s", path)
		}
	}
}

// --- SessionFromContext tests ---

func TestSessionFromContext_ReturnsSession(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Simulate middleware setting session in context
	ctx := context.WithValue(context.Background(), sessionCtxKey, session)
	got := SessionFromContext(ctx)
	if got == nil {
		t.Fatal("expected non-nil session from context")
	}
	if got.ID != session.ID {
		t.Errorf("expected ID %q, got %q", session.ID, got.ID)
	}
}

func TestSessionFromContext_EmptyWithoutMiddleware(t *testing.T) {
	ctx := context.Background()
	got := SessionFromContext(ctx)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// --- LoginHandler tests ---

func TestLoginHandler_GET_RedirectsAuthenticated(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	handler := LoginHandler(store, "admin", "secret", testLoginTemplate())

	// Create request with session in context (simulating middleware)
	r := httptest.NewRequest("GET", "/login", nil)
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

func TestLoginHandler_GET_RendersForm(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	handler := LoginHandler(store, "admin", "secret", testLoginTemplate())

	r := httptest.NewRequest("GET", "/login", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Login") {
		t.Error("response body should contain 'Login'")
	}
	if !strings.Contains(body, "username") {
		t.Error("response body should contain a username field")
	}
}

// --- LogoutHandler tests ---

func TestLogoutHandler_ClearsSession(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	handler := LogoutHandler(store)

	// Create POST /logout with session in context
	body := url.Values{}.Encode()
	r := httptest.NewRequest("POST", "/logout", strings.NewReader(body))
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

	// Verify session deleted from store
	_, err = store.GetSession(session.ID)
	if err == nil {
		t.Error("session should be deleted from store after logout")
	}

	// Verify cookie cleared
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

func TestLogoutHandler_NoSession(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	handler := LogoutHandler(store)

	body := url.Values{}.Encode()
	r := httptest.NewRequest("POST", "/logout", strings.NewReader(body))
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
}

// --- Login + Logout integration via middleware ---

func TestLoginLogoutFlow(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	tmpl := testLoginTemplate()
	loginHandler := LoginHandler(store, "admin", "secret", tmpl)
	logoutHandler := LogoutHandler(store)

	mux := http.NewServeMux()
	mux.Handle("/login", loginHandler)
	mux.Handle("/logout", SessionMiddleware(store)(logoutHandler))

	protected := SessionMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := SessionFromContext(r.Context())
		fmt.Fprintf(w, "hello %s", s.Username)
	}))
	mux.Handle("/dashboard", protected)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	noRedirect := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Step 1: Access dashboard without auth -> redirect
	req, _ := http.NewRequest("GET", ts.URL+"/dashboard", nil)
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("GET /dashboard failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 302 without auth, got %d", resp.StatusCode)
	}

	// Step 2: Login with valid credentials
	loginBody := url.Values{"username": {"admin"}, "password": {"secret"}}.Encode()
	req, _ = http.NewRequest("POST", ts.URL+"/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}

	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "session_id" {
			sessionCookie = c
			break
		}
	}
	resp.Body.Close()
	if sessionCookie == nil {
		t.Fatal("expected session_id cookie after login")
	}

	// Step 3: Access dashboard with auth cookie -> success
	req, _ = http.NewRequest("GET", ts.URL+"/dashboard", nil)
	req.AddCookie(sessionCookie)
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatalf("GET /dashboard failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello admin" {
		t.Errorf("expected 'hello admin', got %q", string(body))
	}

	// Step 4: POST to logout
	logoutBody := url.Values{}.Encode()
	req, _ = http.NewRequest("POST", ts.URL+"/logout", strings.NewReader(logoutBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(sessionCookie)
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatalf("POST /logout failed: %v", err)
	}
	resp.Body.Close()

	// Step 5: Verify session no longer valid
	_, err = store.GetSession(sessionCookie.Value)
	if err == nil {
		t.Error("session should be invalid after logout")
	}
}

// --- Edge cases ---

func TestSessionStore_Stop(t *testing.T) {
	store := NewSessionStore()
	// Stop should not panic
	store.Stop()
	// Calling Stop twice should not panic
	store.Stop()
}

func TestLogoutHandler_RejectsGET(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	handler := LogoutHandler(store)

	r := httptest.NewRequest("GET", "/logout", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestLoginHandler_RejectsPUT(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	handler := LoginHandler(store, "admin", "secret", testLoginTemplate())

	r := httptest.NewRequest("PUT", "/login", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestSessionMiddleware_ExpiredCookieRedirects(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Manually expire the session
	store.mu.Lock()
	store.sessions[session.ID].ExpiresAt = time.Now().Add(-1 * time.Minute)
	store.mu.Unlock()

	handler := &sessionTestHandler{}
	mw := SessionMiddleware(store)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/dashboard", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "session_id", Value: session.ID})

	client := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 302 for expired session, got %d", resp.StatusCode)
	}
	if handler.called {
		t.Error("handler should not have been called for expired session")
	}
}

func TestRequireAuthIsSessionMiddleware(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	handler := &sessionTestHandler{}
	mw := RequireAuth(store)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, _ := http.NewRequest("GET", ts.URL+"/protected", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 302 via RequireAuth, got %d", resp.StatusCode)
	}
}
