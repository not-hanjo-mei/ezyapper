package web

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGenerateCSRFToken_NotEmpty(t *testing.T) {
	token, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("GenerateCSRFToken failed: %v", err)
	}
	if token == "" {
		t.Error("Expected non-empty token, got empty string")
	}
}

func TestGenerateCSRFToken_Unique(t *testing.T) {
	t1, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}
	t2, err := GenerateCSRFToken()
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}
	if t1 == t2 {
		t.Error("Expected different tokens from two calls, got identical")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	token := "test-raw-token-value"

	signed := signToken(token, secret)
	if signed == "" {
		t.Fatal("signToken returned empty string")
	}
	if !strings.Contains(signed, ".") {
		t.Fatal("signed token should contain a dot separator")
	}

	extracted, err := verifySignedToken(signed, secret)
	if err != nil {
		t.Fatalf("verifySignedToken failed: %v", err)
	}
	if extracted != token {
		t.Errorf("Expected extracted token %q, got %q", token, extracted)
	}
}

func TestVerifySignedToken_InvalidFormat(t *testing.T) {
	secret := []byte("test-secret")

	tests := []string{
		"",
		".",
		"token.",
		".signature",
		"no-dot-here",
	}
	for _, tc := range tests {
		_, err := verifySignedToken(tc, secret)
		if err == nil {
			t.Errorf("Expected error for input %q, got nil", tc)
		}
	}
}

func TestVerifySignedToken_WrongSecret(t *testing.T) {
	token := "my-token"
	signed := signToken(token, []byte("correct-secret"))

	_, err := verifySignedToken(signed, []byte("wrong-secret"))
	if err == nil {
		t.Error("Expected error when verifying with wrong secret, got nil")
	}
}

func TestVerifySignedToken_TamperedSignature(t *testing.T) {
	secret := []byte("test-secret")
	token := "my-token"
	signed := signToken(token, secret)

	parts := strings.SplitN(signed, ".", 2)
	if len(parts) == 2 {
		tampered := parts[0] + "." + "deadbeef"
		_, err := verifySignedToken(tampered, secret)
		if err == nil {
			t.Error("Expected error for tampered signature, got nil")
		}
	}
}

type csrfTestHandler struct {
	called bool
	req    *http.Request
}

func (h *csrfTestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.req = r
	io.WriteString(w, "ok")
}

func extractCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestCSRFMiddleware_AllowsGET(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	if !handler.called {
		t.Error("Handler was not called")
	}

	cookie := extractCookie(resp, "csrf_token")
	if cookie == nil {
		t.Fatal("Expected csrf_token cookie to be set")
	}
	if cookie.HttpOnly {
		t.Error("csrf_token cookie should not be HttpOnly (JS needs to read it)")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("Expected SameSite=Strict, got %v", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Expected cookie path '/', got %q", cookie.Path)
	}
	if cookie.Value == "" {
		t.Error("Cookie value should not be empty")
	}
}

func TestCSRFMiddleware_RejectsPOSTWithoutToken(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/test", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", resp.StatusCode)
	}
	if handler.called {
		t.Error("Handler should not have been called")
	}
}

func TestCSRFMiddleware_RejectsPOSTWithWrongToken(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")

	mw := CSRFMiddleware(secret)
	ts := httptest.NewServer(mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})))
	defer ts.Close()

	getResp, err := http.Get(ts.URL + "/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	getResp.Body.Close()

	cookie := extractCookie(getResp, "csrf_token")
	if cookie == nil {
		t.Fatal("No csrf_token cookie in GET response")
	}

	body := url.Values{"csrf_token": {"invalid-token-value"}}.Encode()
	req, err := http.NewRequest("POST", ts.URL+"/test", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST request creation failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", resp.StatusCode)
	}
}

func TestCSRFMiddleware_AcceptsPOSTWithValidToken(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	getResp, err := http.Get(ts.URL + "/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	getResp.Body.Close()

	cookie := extractCookie(getResp, "csrf_token")
	if cookie == nil {
		t.Fatal("No csrf_token cookie in GET response")
	}

	handler.called = false
	body := url.Values{"csrf_token": {cookie.Value}}.Encode()
	req, err := http.NewRequest("POST", ts.URL+"/test", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST request creation failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	if !handler.called {
		t.Error("Handler should have been called")
	}
}

func TestCSRFMiddleware_RejectsPUTWithoutToken(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	req, err := http.NewRequest("PUT", ts.URL+"/test", nil)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", resp.StatusCode)
	}
	if handler.called {
		t.Error("Handler should not have been called")
	}
}

func TestCSRFMiddleware_RejectsDELETEWithoutToken(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	req, err := http.NewRequest("DELETE", ts.URL+"/test", nil)
	if err != nil {
		t.Fatalf("DELETE request failed: %v", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403, got %d", resp.StatusCode)
	}
	if handler.called {
		t.Error("Handler should not have been called")
	}
}

func TestCSRFMiddleware_ExcludesLoginPath(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/login", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 for POST /login, got %d", resp.StatusCode)
	}
	if !handler.called {
		t.Error("Handler should have been called for POST /login")
	}
}

func TestCSRFMiddleware_ExcludesLoginPathExact(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/login/other", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST /login/other failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403 for POST /login/other, got %d", resp.StatusCode)
	}
}

func TestCSRFTemplateField_GeneratesCorrectInput(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	mw(handler).ServeHTTP(rr, req)

	if !handler.called {
		t.Fatal("Handler was not called by middleware")
	}

	field := CSRFTemplateField(handler.req)
	if field == "" {
		t.Error("Expected non-empty template field from GET request")
	}

	expectedPrefix := `<input type="hidden" name="csrf_token" value="`
	if !strings.HasPrefix(string(field), expectedPrefix) {
		t.Errorf("Expected prefix %q, got %q", expectedPrefix, string(field))
	}
	if !strings.HasSuffix(string(field), `">`) {
		t.Errorf("Expected suffix %q, got %q", `">`, string(field))
	}

	inner := strings.TrimPrefix(string(field), expectedPrefix)
	inner = strings.TrimSuffix(inner, `">`)
	if inner == "" {
		t.Error("Expected non-empty token value in template field")
	}
}

func TestCSRFTemplateField_EmptyWhenNoToken(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	field := CSRFTemplateField(req)
	if field != template.HTML("") {
		t.Errorf("Expected empty template field, got %q", string(field))
	}
}

func TestCSRFTokenFromContext_ReturnsToken(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	mw(handler).ServeHTTP(rr, req)

	if !handler.called {
		t.Fatal("Handler was not called by middleware")
	}

	token := CSRFTokenFromContext(handler.req.Context())
	if token == "" {
		t.Error("Expected non-empty token from context after GET")
	}
}

func TestCSRFTokenFromContext_EmptyWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	token := CSRFTokenFromContext(req.Context())
	if token != "" {
		t.Errorf("Expected empty token, got %q", token)
	}
}

func TestCSRFMiddleware_AllowsHEAD(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")
	handler := &csrfTestHandler{}
	mw := CSRFMiddleware(secret)

	ts := httptest.NewServer(mw(handler))
	defer ts.Close()

	req, err := http.NewRequest("HEAD", ts.URL+"/test", nil)
	if err != nil {
		t.Fatalf("HEAD request failed: %v", err)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HEAD failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 for HEAD, got %d", resp.StatusCode)
	}
	if !handler.called {
		t.Error("Handler should have been called for HEAD")
	}

	cookie := extractCookie(resp, "csrf_token")
	if cookie == nil {
		t.Fatal("Expected csrf_token cookie on HEAD request")
	}
}

func TestCSRFMiddleware_TamperedCookieRejected(t *testing.T) {
	secret := []byte("test-secret-32-byte-key-for-hmac!")

	mw := CSRFMiddleware(secret)
	ts := httptest.NewServer(mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})))
	defer ts.Close()

	getResp, err := http.Get(ts.URL + "/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	getResp.Body.Close()

	cookie := extractCookie(getResp, "csrf_token")
	if cookie == nil {
		t.Fatal("No csrf_token cookie in GET response")
	}

	parts := strings.SplitN(cookie.Value, ".", 2)
	tamperedCookie := &http.Cookie{
		Name:  "csrf_token",
		Value: parts[0] + "." + "abcdef1234567890",
	}
	body := url.Values{"csrf_token": {cookie.Value}}.Encode()
	req, err := http.NewRequest("POST", ts.URL+"/test", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(tamperedCookie)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403 for tampered cookie, got %d", resp.StatusCode)
	}
}
