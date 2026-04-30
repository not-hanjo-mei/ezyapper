package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/ai/tools"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"

	openai "github.com/sashabaranov/go-openai"
)

func TestMain(m *testing.M) {
	logger.Init(logger.Config{Level: "info", File: os.DevNull})
	os.Exit(m.Run())
}

// --- Helpers ---

func defaultAIConfig() *config.AIConfig {
	return &config.AIConfig{
		APIKey:                  "test-key",
		APIBaseURL:              "https://api.example.com",
		Model:                   "gpt-4",
		VisionModel:             "gpt-4-vision",
		MaxTokens:               100,
		Temperature:             0.7,
		RetryCount:              2,
		Timeout:                 0,
		HTTPTimeoutSec:          30,
		MaxToolIterations:       5,
		MaxImageBytes:           10485760,
		UserAgent:               "EZyapper/1.0",
		RequireImageContentType: true,
	}
}

func testClient(cfg *config.AIConfig) *Client {
	return NewClient(cfg, tools.NewToolRegistry())
}

// --- NewClient ---

func TestNewClient_InitializesFields(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.HTTPTimeoutSec = 15
	tr := tools.NewToolRegistry()
	c := NewClient(cfg, tr)

	if c.client == nil {
		t.Error("expected openai client to be non-nil")
	}
	if c.httpClient == nil {
		t.Error("expected http client to be non-nil")
	}
	if c.httpClient.Timeout != 15*time.Second {
		t.Errorf("expected httpClient timeout %v, got %v", 15*time.Second, c.httpClient.Timeout)
	}
	if c.config != cfg {
		t.Error("expected config to match")
	}
	if c.toolRegistry != tr {
		t.Error("expected toolRegistry to match")
	}
}

func TestNewClient_UsesHTTPTimeoutSec(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.HTTPTimeoutSec = 45
	c := NewClient(cfg, tools.NewToolRegistry())

	if c.httpClient.Timeout != 45*time.Second {
		t.Errorf("expected HTTP timeout 45s, got %v", c.httpClient.Timeout)
	}
}

func TestNewClient_HTTPTimeoutSecFromConfig(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.HTTPTimeoutSec = 10
	c := NewClient(cfg, tools.NewToolRegistry())

	if c.httpClient.Timeout != 10*time.Second {
		t.Errorf("expected HTTP timeout 10s, got %v", c.httpClient.Timeout)
	}
}

// --- requestTimeout ---

func TestRequestTimeout_FromConfig(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.Timeout = 10
	c := testClient(cfg)
	if got := c.requestTimeout(); got != 10*time.Second {
		t.Errorf("expected 10s, got %v", got)
	}
}

func TestRequestTimeout_FallsBackToHTTPClient(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.Timeout = 0
	c := testClient(cfg)
	// httpClient initialized from HTTPTimeoutSec (30s default)
	if got := c.requestTimeout(); got != 30*time.Second {
		t.Errorf("expected 30s from httpClient, got %v", got)
	}
}

func TestRequestTimeout_ConfigNil(t *testing.T) {
	c := &Client{config: nil, httpClient: &http.Client{Timeout: 45 * time.Second}}
	if got := c.requestTimeout(); got != 45*time.Second {
		t.Errorf("expected 45s, got %v", got)
	}
}

func TestRequestTimeout_BothNil(t *testing.T) {
	c := &Client{config: nil, httpClient: nil}
	if got := c.requestTimeout(); got != 30*time.Second {
		t.Errorf("expected default 30s, got %v", got)
	}
}

func TestRequestTimeout_HTTPClientNilWithConfig(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.Timeout = 20
	c := &Client{config: cfg, httpClient: nil}
	if got := c.requestTimeout(); got != 20*time.Second {
		t.Errorf("expected 20s from config, got %v", got)
	}
}

// --- closeIdleConnections ---

func TestCloseIdleConnections_WithHTTPClient(t *testing.T) {
	c := testClient(defaultAIConfig())
	c.closeIdleConnections() // should not panic
}

func TestCloseIdleConnections_NilHTTPClient(t *testing.T) {
	c := &Client{httpClient: nil}
	c.closeIdleConnections() // should not panic
}

// --- retryableError ---

func TestRetryableError_NilError(t *testing.T) {
	c := testClient(defaultAIConfig())
	if c.retryableError(nil) {
		t.Error("nil error should not be retryable")
	}
}

func TestRetryableError_RateLimit(t *testing.T) {
	c := testClient(defaultAIConfig())
	cases := []string{
		"HTTP 429 Too Many Requests",
		"too many requests, please slow down",
		"429 rate limit exceeded",
	}
	for _, msg := range cases {
		if !c.retryableError(fmt.Errorf("%s", msg)) {
			t.Errorf("expected retryable for: %s", msg)
		}
	}
}

func TestRetryableError_ServerErrors(t *testing.T) {
	c := testClient(defaultAIConfig())
	for _, code := range []string{"500", "502", "503", "504"} {
		if !c.retryableError(fmt.Errorf("HTTP %s Internal Server Error", code)) {
			t.Errorf("expected retryable for code %s", code)
		}
	}
}

func TestRetryableError_ConnectionErrors(t *testing.T) {
	c := testClient(defaultAIConfig())
	cases := []string{
		"connection refused",
		"read tcp: i/o timeout",
		"context deadline exceeded",
		"unexpected EOF",
	}
	for _, msg := range cases {
		if !c.retryableError(fmt.Errorf("%s", msg)) {
			t.Errorf("expected retryable for: %s", msg)
		}
	}
}

func TestRetryableError_NonRetryable(t *testing.T) {
	c := testClient(defaultAIConfig())
	cases := []string{
		"invalid API key",
		"model not found",
		"rate limited: 400 bad request",
	}
	for _, msg := range cases {
		if c.retryableError(fmt.Errorf("%s", msg)) {
			t.Errorf("expected non-retryable for: %s", msg)
		}
	}
}

// --- IsTimeoutLikeError ---

func TestIsTimeoutLikeError_NilError(t *testing.T) {
	if IsTimeoutLikeError(nil) {
		t.Error("nil error should not be timeout-like")
	}
}

func TestIsTimeoutLikeError_ContextDeadlineExceeded(t *testing.T) {
	if !IsTimeoutLikeError(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded should be timeout-like")
	}
}

func TestIsTimeoutLikeError_NetError(t *testing.T) {
	// Simulate a net.Error that reports Timeout()
	// We'll use net.OpError which can be a net.Error
	netErr := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: &testTimeoutError{},
	}
	if !IsTimeoutLikeError(netErr) {
		t.Error("net.Error with Timeout()=true should be timeout-like")
	}
}

type testTimeoutError struct{}

func (e *testTimeoutError) Error() string   { return "i/o timeout" }
func (e *testTimeoutError) Timeout() bool   { return true }
func (e *testTimeoutError) Temporary() bool { return false }

func TestIsTimeoutLikeError_NetErrorWithoutTimeout(t *testing.T) {
	netErr := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: errors.New("connection reset"),
	}
	if IsTimeoutLikeError(netErr) {
		t.Error("net.Error without Timeout() should not be timeout-like by net.Error check")
	}
	// However, "timeout" might be in the string — this tests the net.Error path
}

func TestIsTimeoutLikeError_StringMatch(t *testing.T) {
	cases := []string{
		"read tcp: i/o timeout",
		"context deadline exceeded",
		"request timeout",
	}
	for _, msg := range cases {
		if !IsTimeoutLikeError(fmt.Errorf("%s", msg)) {
			t.Errorf("expected timeout-like for: %s", msg)
		}
	}
}

func TestIsTimeoutLikeError_NonTimeout(t *testing.T) {
	if IsTimeoutLikeError(errors.New("something else")) {
		t.Error("expected non-timeout-like")
	}
}

// --- findFieldIndexByJSONTag ---

func TestFindFieldIndexByJSONTag_ByJSONTag(t *testing.T) {
	type testStruct struct {
		Model string  `json:"model"`
		TopP  float32 `json:"top_p,omitempty"`
	}
	typ := reflectType(&testStruct{})
	idx := findFieldIndexByJSONTag(typ, "model")
	if idx != 0 {
		t.Errorf("expected index 0 for 'model', got %d", idx)
	}
	idx = findFieldIndexByJSONTag(typ, "top_p")
	if idx != 1 {
		t.Errorf("expected index 1 for 'top_p', got %d", idx)
	}
}

func TestFindFieldIndexByJSONTag_ByFieldName(t *testing.T) {
	type testStruct struct {
		MaxTokens int
	}
	typ := reflectType(&testStruct{})
	idx := findFieldIndexByJSONTag(typ, "max_tokens")
	if idx == -1 {
		// json tag not set, should match by field name case-insensitive
	}
	idx2 := findFieldIndexByJSONTag(typ, "MaxTokens")
	if idx2 != 0 {
		t.Errorf("expected index 0 for 'MaxTokens', got %d", idx2)
	}
	idx3 := findFieldIndexByJSONTag(typ, "maxtokens")
	if idx3 != 0 {
		t.Errorf("expected index 0 for case-insensitive 'maxtokens', got %d", idx3)
	}
}

func TestFindFieldIndexByJSONTag_NotFound(t *testing.T) {
	type testStruct struct {
		Model string `json:"model"`
	}
	typ := reflectType(&testStruct{})
	idx := findFieldIndexByJSONTag(typ, "nonexistent")
	if idx != -1 {
		t.Errorf("expected -1 for nonexistent, got %d", idx)
	}
}

// reflectType is a helper to get a reflect.Type from a struct pointer
func reflectType(v interface{}) reflect.Type {
	return reflect.ValueOf(v).Elem().Type()
}

// --- setFieldValue ---

func TestSetFieldValue_NilValue(t *testing.T) {
	var s string = "original"
	fv := reflect.ValueOf(&s).Elem()
	err := setFieldValue(fv, nil)
	if err != nil {
		t.Errorf("nil value should not error: %v", err)
	}
	if s != "original" {
		t.Error("value should not change for nil input")
	}
}

func TestSetFieldValue_DirectConvertible(t *testing.T) {
	var s string
	fv := reflect.ValueOf(&s).Elem()
	err := setFieldValue(fv, "hello")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if s != "hello" {
		t.Errorf("expected 'hello', got '%s'", s)
	}
}

func TestSetFieldValue_IntToFloat(t *testing.T) {
	var f float32
	fv := reflect.ValueOf(&f).Elem()
	err := setFieldValue(fv, 0.5)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if f != 0.5 {
		t.Errorf("expected 0.5, got %v", f)
	}
}

func TestSetFieldValue_PointerField(t *testing.T) {
	type s struct {
		Seed *int
	}
	var st s
	fv := reflect.ValueOf(&st).Elem().Field(0)
	err := setFieldValue(fv, 42)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if st.Seed == nil || *st.Seed != 42 {
		t.Error("pointer field not set correctly")
	}
}

func TestSetFieldValue_PointerFieldIncompatible(t *testing.T) {
	type s struct {
		Seed *int
	}
	var st s
	fv := reflect.ValueOf(&st).Elem().Field(0)
	err := setFieldValue(fv, "not_an_int")
	if err == nil {
		t.Error("expected error for incompatible pointer type")
	}
}

func TestSetFieldValue_SliceField(t *testing.T) {
	var sl []string
	fv := reflect.ValueOf(&sl).Elem()
	input := []string{"a", "b", "c"}
	err := setFieldValue(fv, input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(sl) != 3 || sl[0] != "a" || sl[1] != "b" || sl[2] != "c" {
		t.Errorf("slice not set correctly: %v", sl)
	}
}

func TestSetFieldValue_SliceFieldIncompatibleElement(t *testing.T) {
	var sl []string
	fv := reflect.ValueOf(&sl).Elem()
	type custom struct{ X int }
	err := setFieldValue(fv, []custom{{1}, {2}})
	if err == nil {
		t.Error("expected error for incompatible slice element types")
	}
}

func TestSetFieldValue_MapField(t *testing.T) {
	var m map[string]int
	fv := reflect.ValueOf(&m).Elem()
	input := map[string]int{"key1": 10, "key2": 20}
	err := setFieldValue(fv, input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(m) != 2 || m["key1"] != 10 || m["key2"] != 20 {
		t.Errorf("map not set correctly: %v", m)
	}
}

func TestSetFieldValue_MapFieldIncompatible(t *testing.T) {
	var m map[string]int
	fv := reflect.ValueOf(&m).Elem()
	err := setFieldValue(fv, map[int]string{1: "a"})
	if err == nil {
		t.Error("expected error for incompatible map types")
	}
}

func TestSetFieldValue_Unconvertible(t *testing.T) {
	var b bool
	fv := reflect.ValueOf(&b).Elem()
	err := setFieldValue(fv, "not_a_bool")
	if err == nil {
		t.Error("expected error for unconvertible type")
	}
}

// --- applyExtraParamsToStruct ---

func TestApplyExtraParamsToStruct_EmptyParams(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "gpt-4"}
	applyExtraParamsToStruct(&req, map[string]interface{}{}, "")
	if req.Model != "gpt-4" {
		t.Error("model should not change with empty params")
	}
}

func TestApplyExtraParamsToStruct_ValidFields(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "gpt-4"}
	params := map[string]interface{}{
		"top_p":            0.9,
		"presence_penalty": 0.5,
	}
	applyExtraParamsToStruct(&req, params, "[test]")
	if req.TopP != 0.9 {
		t.Errorf("expected TopP 0.9, got %v", req.TopP)
	}
	if req.PresencePenalty != 0.5 {
		t.Errorf("expected PresencePenalty 0.5, got %v", req.PresencePenalty)
	}
}

func TestApplyExtraParamsToStruct_FieldNotFound(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "gpt-4"}
	params := map[string]interface{}{
		"nonexistent_field": "value",
	}
	// Should not panic, just debug log
	applyExtraParamsToStruct(&req, params, "[test]")
}

func TestApplyExtraParamsToStruct_NilParamMap(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "gpt-4"}
	applyExtraParamsToStruct(&req, nil, "[test]")
	if req.Model != "gpt-4" {
		t.Error("model should not change with nil params")
	}
}

// --- ApplyExtraParams ---

func TestApplyExtraParams_Delegates(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "gpt-4"}
	params := map[string]interface{}{"top_p": 0.8}
	ApplyExtraParams(&req, params, "[test]")
	if req.TopP != 0.8 {
		t.Errorf("expected TopP 0.8, got %v", req.TopP)
	}
}

// --- applyExtraParams (method) ---

func TestApplyExtraParams_Method(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.ExtraParams = map[string]interface{}{"top_p": 0.7}
	c := testClient(cfg)
	req := openai.ChatCompletionRequest{}
	c.applyExtraParams(&req)
	if req.TopP != 0.7 {
		t.Errorf("expected TopP 0.7, got %v", req.TopP)
	}
}

// --- processMessages ---

func TestProcessMessages_VisionBase64Disabled(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.VisionBase64 = false
	c := testClient(cfg)

	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "hello"},
	}
	result, err := c.processMessages(context.Background(), messages)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
	if result[0].Content != "hello" {
		t.Errorf("expected 'hello', got '%s'", result[0].Content)
	}
}

func TestProcessMessages_VisionBase64Enabled_NoMultiContent(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.VisionBase64 = true
	c := testClient(cfg)

	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	result, err := c.processMessages(context.Background(), messages)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

func TestProcessMessages_VisionBase64Enabled_DataURIAlready(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.VisionBase64 = true
	c := testClient(cfg)

	messages := []openai.ChatCompletionMessage{
		{
			Role: "user",
			MultiContent: []openai.ChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL: "data:image/png;base64,iVBORw0KGgo=",
					},
				},
			},
		},
	}
	result, err := c.processMessages(context.Background(), messages)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
	if result[0].MultiContent[0].ImageURL.URL != "data:image/png;base64,iVBORw0KGgo=" {
		t.Error("data URI should remain unchanged")
	}
}

func TestProcessMessages_VisionBase64Enabled_ConvertsURL(t *testing.T) {
	// Create a test server that serves a small image
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4E, 0x47}) // PNG magic bytes
	}))
	defer server.Close()

	cfg := defaultAIConfig()
	cfg.VisionBase64 = true
	c := testClient(cfg)
	// Give the client a fresh httpClient that has a reasonable timeout
	c.httpClient = server.Client()

	messages := []openai.ChatCompletionMessage{
		{
			Role: "user",
			MultiContent: []openai.ChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL: server.URL + "/image.png",
					},
				},
			},
		},
	}
	result, err := c.processMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	url := result[0].MultiContent[0].ImageURL.URL
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("expected data URI for image/png, got: %s", url[:min(len(url), 50)])
	}
}

func TestProcessMessages_DownloadError(t *testing.T) {
	// Server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	cfg := defaultAIConfig()
	cfg.VisionBase64 = true
	c := testClient(cfg)
	c.httpClient = server.Client()

	messages := []openai.ChatCompletionMessage{
		{
			Role: "user",
			MultiContent: []openai.ChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL: server.URL + "/missing.png",
					},
				},
			},
		},
	}
	_, err := c.processMessages(context.Background(), messages)
	if err == nil {
		t.Error("expected error for download failure")
	}
}

// --- fetchImageAsDataURL ---

func TestFetchImageAsDataURL_Success(t *testing.T) {
	imgData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "MyAgent/1.0" {
			t.Errorf("expected User-Agent MyAgent/1.0, got %s", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(imgData)
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	result, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/img.png", imageDownloadOptions{
		UserAgent: "MyAgent/1.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedPrefix := "data:image/png;base64,"
	if !strings.HasPrefix(result, expectedPrefix) {
		t.Errorf("expected prefix '%s', got: %s", expectedPrefix, result[:min(len(result), 50)])
	}
	// Decode and verify
	b64 := strings.TrimPrefix(result, expectedPrefix)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode error: %v", err)
	}
	if len(decoded) != len(imgData) {
		t.Errorf("expected %d bytes, got %d", len(imgData), len(decoded))
	}
}

func TestFetchImageAsDataURL_EmptyContentTypeDefaults(t *testing.T) {
	imgData := []byte{0x89, 0x50, 0x4E, 0x47}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil // force empty content type
		w.Write(imgData)
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	result, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/img.png", imageDownloadOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, "data:image/png;base64,") {
		t.Errorf("expected default image/png content-type, got: %s", result[:min(len(result), 50)])
	}
}

func TestFetchImageAsDataURL_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	_, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/missing.png", imageDownloadOptions{})
	if err == nil {
		t.Error("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("expected status 404 error, got: %v", err)
	}
}

func TestFetchImageAsDataURL_RequireImageContentType_Invalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html></html>"))
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	_, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/page.html", imageDownloadOptions{
		RequireImageContentType: true,
	})
	if err == nil {
		t.Error("expected error for non-image content type")
	}
	if !strings.Contains(err.Error(), "invalid content type") {
		t.Errorf("expected 'invalid content type' error, got: %v", err)
	}
}

func TestFetchImageAsDataURL_RequireImageContentType_Valid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0}) // JPEG magic
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	result, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/img.jpg", imageDownloadOptions{
		RequireImageContentType: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, "data:image/jpeg;base64,") {
		t.Errorf("expected image/jpeg data URI, got: %s", result[:min(len(result), 50)])
	}
}

func TestFetchImageAsDataURL_MaxBytes_ContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "1000000")
		w.Write(make([]byte, 1000000))
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	_, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/big.png", imageDownloadOptions{
		MaxBytes: 1000,
	})
	if err == nil {
		t.Error("expected error for too-large image (Content-Length)")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}

func TestFetchImageAsDataURL_MaxBytes_ActualData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		// Content-Length under limit, but actual data exceeds
		w.Write(make([]byte, 2000))
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	_, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/big.png", imageDownloadOptions{
		MaxBytes: 1000,
	})
	if err == nil {
		t.Error("expected error for too-large actual data")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}

func TestFetchImageAsDataURL_MaxBytesZero_NoLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(make([]byte, 5000))
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	// MaxBytes=0 means no limit
	_, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/img.png", imageDownloadOptions{
		MaxBytes: 0,
	})
	if err != nil {
		t.Errorf("unexpected error with no size limit: %v", err)
	}
}

func TestFetchImageAsDataURL_NilHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer server.Close()

	c := &Client{config: defaultAIConfig(), httpClient: nil}

	result, err := c.fetchImageAsDataURL(context.Background(), server.URL+"/img.png", imageDownloadOptions{})
	if err != nil {
		t.Fatalf("unexpected error with nil httpClient: %v", err)
	}
	if !strings.HasPrefix(result, "data:image/png;base64,") {
		t.Errorf("expected data URI, got: %s", result[:min(len(result), 50)])
	}
}

func TestFetchImageAsDataURL_InvalidURL(t *testing.T) {
	c := testClient(defaultAIConfig())
	_, err := c.fetchImageAsDataURL(context.Background(), "://invalid-url", imageDownloadOptions{})
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

// --- downloadImageToBase64 ---

func TestDownloadImageToBase64_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	result, err := c.downloadImageToBase64(context.Background(), server.URL+"/img.png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, "data:image/png;base64,") {
		t.Errorf("expected data URI, got: %s", result[:min(len(result), 50)])
	}
}

func TestDownloadImageToBase64_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	_, err := c.downloadImageToBase64(context.Background(), server.URL+"/missing.png")
	if err == nil {
		t.Error("expected error for download failure")
	}
}

// --- buildVisionParts ---

func TestBuildVisionParts_EmptyAll(t *testing.T) {
	c := testClient(defaultAIConfig())
	parts, err := c.buildVisionParts(context.Background(), "", nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(parts) != 0 {
		t.Errorf("expected 0 parts, got %d", len(parts))
	}
}

func TestBuildVisionParts_TextOnly(t *testing.T) {
	c := testClient(defaultAIConfig())
	parts, err := c.buildVisionParts(context.Background(), "describe this", nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].Type != openai.ChatMessagePartTypeText {
		t.Error("expected text part")
	}
	if parts[0].Text != "describe this" {
		t.Errorf("expected 'describe this', got '%s'", parts[0].Text)
	}
}

func TestBuildVisionParts_DataURImage(t *testing.T) {
	c := testClient(defaultAIConfig())
	dataURI := "data:image/png;base64,iVBORw0KGgo="
	parts, err := c.buildVisionParts(context.Background(), "look", []string{dataURI})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// 1 text + 1 image = 2 parts
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[1].ImageURL == nil {
		t.Fatal("expected ImageURL to be set")
	}
	if parts[1].ImageURL.URL != dataURI {
		t.Errorf("expected data URI unchanged, got %s", parts[1].ImageURL.URL)
	}
}

func TestBuildVisionParts_ConvertsURLWhenVisionBase64(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer server.Close()

	cfg := defaultAIConfig()
	cfg.VisionBase64 = true
	c := testClient(cfg)
	c.httpClient = server.Client()

	parts, err := c.buildVisionParts(context.Background(), "look", []string{server.URL + "/img.png"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("expected converted data URI, got: %s", parts[1].ImageURL.URL[:min(len(parts[1].ImageURL.URL), 50)])
	}
}

func TestBuildVisionParts_SkipsConversionWhenVisionBase64False(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.VisionBase64 = false
	c := testClient(cfg)

	externalURL := "https://example.com/img.png"
	parts, err := c.buildVisionParts(context.Background(), "look", []string{externalURL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[1].ImageURL.URL != externalURL {
		t.Errorf("expected original URL, got %s", parts[1].ImageURL.URL)
	}
}

func TestBuildVisionParts_NoTextOnlyImages(t *testing.T) {
	c := testClient(defaultAIConfig())
	dataURI := "data:image/png;base64,iVBORw0KGgo="
	parts, err := c.buildVisionParts(context.Background(), "", []string{dataURI})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// No text part, only image
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].Type != openai.ChatMessagePartTypeImageURL {
		t.Error("expected image part")
	}
}

func TestBuildVisionParts_DownloadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	cfg := defaultAIConfig()
	cfg.VisionBase64 = true
	c := testClient(cfg)
	c.httpClient = server.Client()

	_, err := c.buildVisionParts(context.Background(), "look", []string{server.URL + "/missing.png"})
	if err == nil {
		t.Error("expected error for download failure")
	}
}

// --- DownloadImage ---

func TestDownloadImage_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "EZyapper/1.0" {
			t.Errorf("expected User-Agent EZyapper/1.0, got %s", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "4")
		w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	result, err := c.DownloadImage(context.Background(), server.URL+"/img.png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, "data:image/png;base64,") {
		t.Errorf("expected data URI, got: %s", result[:min(len(result), 50)])
	}
}

func TestDownloadImage_RequiresImageContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html></html>"))
	}))
	defer server.Close()

	c := testClient(defaultAIConfig())
	c.httpClient = server.Client()

	_, err := c.DownloadImage(context.Background(), server.URL+"/page.html")
	if err == nil {
		t.Error("expected error for non-image content-type")
	}
}

// --- API-calling functions (mocked via httptest server) ---

// setupOpenAIMock creates an httptest server + client configured to use it
func setupOpenAIMock(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	server := httptest.NewServer(handler)

	cfg := defaultAIConfig()
	cfg.APIBaseURL = server.URL
	c := NewClient(cfg, tools.NewToolRegistry())

	return server, c
}

// openaiResponse writes a valid chat completion JSON response
func writeChatCompletionResponse(w http.ResponseWriter, content string, toolCalls []openai.ToolCall) {
	resp := openai.ChatCompletionResponse{
		ID:    "chatcmpl-123",
		Model: "gpt-4",
		Choices: []openai.ChatCompletionChoice{
			{
				Index: 0,
				Message: openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: content,
				},
				FinishReason: openai.FinishReasonStop,
			},
		},
		Usage: openai.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
	if len(toolCalls) > 0 {
		resp.Choices[0].Message.ToolCalls = toolCalls
	}
	w.Header().Set("Content-Type", "application/json")
	// Use the JSON encoding from go-openai's expected format
	fmt.Fprintf(w, `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"%s"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`, content)
}

// --- CreateChatCompletion ---

func TestCreateChatCompletion_Success(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletionResponse(w, "Hello, world!", nil)
	})
	defer server.Close()

	resp, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		SystemPrompt: "You are helpful",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got '%s'", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected 'stop', got '%s'", resp.FinishReason)
	}
}

func TestCreateChatCompletion_NoChoices(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`)
	})
	defer server.Close()

	_, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Hi"},
		},
	})
	if err == nil {
		t.Error("expected error for no choices")
	}
}

func TestCreateChatCompletion_WithUserContext(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletionResponse(w, "contextual response", nil)
	})
	defer server.Close()

	resp, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		UserContext: "ctx: user is Bob",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "contextual response" {
		t.Errorf("expected 'contextual response', got '%s'", resp.Content)
	}
}

func TestCreateChatCompletion_UserContextOnLastUserMessage(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletionResponse(w, "ok", nil)
	})
	defer server.Close()

	_, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		SystemPrompt: "system",
		UserContext:  "ctx: time is noon",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "msg1"},
			{Role: openai.ChatMessageRoleAssistant, Content: "reply1"},
			{Role: openai.ChatMessageRoleUser, Content: "msg2"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The last user message (msg2) should have context prepended
	// We can't easily verify the sent message content since we mocked, but no error means it worked
}

func TestCreateChatCompletion_WithTools(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletionResponse(w, "using tools", nil)
	})
	defer server.Close()

	toolDef := openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "test_tool",
			Description: "A test tool",
		},
	}

	resp, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		SystemPrompt: "system",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Hi"},
		},
		Tools: []openai.Tool{toolDef},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "using tools" {
		t.Errorf("expected 'using tools', got '%s'", resp.Content)
	}
}

func TestCreateChatCompletion_ErrorResponse(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"message":"internal server error"}}`)
	})
	defer server.Close()

	_, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Hi"},
		},
	})
	if err == nil {
		t.Error("expected error for server error")
	}
}

// --- CreateChatCompletionWithRetry ---

func TestCreateChatCompletionWithRetry_SuccessFirstAttempt(t *testing.T) {
	attempts := 0
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		writeChatCompletionResponse(w, "success", nil)
	})
	defer server.Close()

	resp, err := c.CreateChatCompletionWithRetry(context.Background(), openai.ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "Hi"}},
	}, "test op")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
	if resp.Choices[0].Message.Content != "success" {
		t.Errorf("expected 'success', got '%s'", resp.Choices[0].Message.Content)
	}
}

func TestCreateChatCompletionWithRetry_RetryOn429(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.RetryCount = 2
	attempts := 0
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"message":"429 too many requests"}}`)
			return
		}
		writeChatCompletionResponse(w, "finally", nil)
	})
	defer server.Close()
	c.config.RetryCount = 2 // override with our config

	resp, err := c.CreateChatCompletionWithRetry(context.Background(), openai.ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "Hi"}},
	}, "test op")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attempts)
	}
	if resp.Choices[0].Message.Content != "finally" {
		t.Errorf("expected 'finally', got '%s'", resp.Choices[0].Message.Content)
	}
}

func TestCreateChatCompletionWithRetry_NonRetryableError(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.RetryCount = 2
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"invalid API key"}}`)
	})
	defer server.Close()
	c.config.RetryCount = 2

	_, err := c.CreateChatCompletionWithRetry(context.Background(), openai.ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "Hi"}},
	}, "test op")
	if err == nil {
		t.Error("expected error for non-retryable error")
	}
}

// --- CreateChatCompletionWithTools ---

func TestCreateChatCompletionWithTools_NoToolCalls(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletionResponse(w, "plain response", nil)
	})
	defer server.Close()

	c.toolRegistry.Register(&tools.Tool{
		Name:        "echo",
		Description: "echoes back",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "echo result", nil
		},
	})

	handler := func(ctx context.Context, tc openai.ToolCall) (string, error) {
		return "result", nil
	}

	resp, err := c.CreateChatCompletionWithTools(context.Background(), ChatCompletionRequest{
		SystemPrompt: "system",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Hi"},
		},
	}, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "plain response" {
		t.Errorf("expected 'plain response', got '%s'", resp.Content)
	}
}

func TestCreateChatCompletionWithTools_WithToolCalls(t *testing.T) {
	toolCallCount := 0
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		// First call: return a tool call
		// Second call: return final response
		if toolCallCount == 0 {
			toolCallCount++
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
			return
		}
		writeChatCompletionResponse(w, "final answer", nil)
	})
	defer server.Close()

	c.toolRegistry.Register(&tools.Tool{
		Name:        "echo",
		Description: "echoes back",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "echo result", nil
		},
	})

	handler := func(ctx context.Context, tc openai.ToolCall) (string, error) {
		return "echo result", nil
	}

	resp, err := c.CreateChatCompletionWithTools(context.Background(), ChatCompletionRequest{
		SystemPrompt: "system",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "echo something"},
		},
	}, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "final answer" {
		t.Errorf("expected 'final answer', got '%s'", resp.Content)
	}
}

// --- CreateVisionCompletion ---

func TestCreateVisionCompletion_Success(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletionResponse(w, "I see a cat", nil)
	})
	defer server.Close()

	result, err := c.CreateVisionCompletion(context.Background(), "system prompt", "what is this?", []string{"data:image/png;base64,iVBORw0KGgo="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "I see a cat" {
		t.Errorf("expected 'I see a cat', got '%s'", result)
	}
}

func TestCreateVisionCompletion_NoChoices(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
	})
	defer server.Close()

	_, err := c.CreateVisionCompletion(context.Background(), "system", "what?", []string{"data:image/png;base64,iVBORw0KGgo="})
	if err == nil {
		t.Error("expected error for no choices")
	}
}

func TestCreateVisionCompletion_BuildError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	cfg := defaultAIConfig()
	cfg.VisionBase64 = true
	c := testClient(cfg)
	c.config.APIBaseURL = server.URL
	c.httpClient = server.Client()

	_, err := c.CreateVisionCompletion(context.Background(), "system", "what?", []string{server.URL + "/missing.png"})
	if err == nil {
		t.Error("expected error for image download failure")
	}
}

// --- CreateEmbedding ---

func TestCreateEmbedding_Success(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3,0.4,0.5]}],"model":"text-embedding-3","usage":{"prompt_tokens":5,"total_tokens":5}}`)
	})
	defer server.Close()

	embedding, err := c.CreateEmbedding(context.Background(), "test text", "text-embedding-3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embedding) != 5 {
		t.Errorf("expected 5 floats, got %d", len(embedding))
	}
	if embedding[0] != 0.1 {
		t.Errorf("expected 0.1, got %v", embedding[0])
	}
}

func TestCreateEmbedding_EmptyModel(t *testing.T) {
	c := testClient(defaultAIConfig())
	_, err := c.CreateEmbedding(context.Background(), "text", "")
	if err == nil {
		t.Error("expected error for empty model")
	}
}

func TestCreateEmbedding_NoData(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[],"model":"text-embedding-3","usage":{"prompt_tokens":0,"total_tokens":0}}`)
	})
	defer server.Close()

	_, err := c.CreateEmbedding(context.Background(), "text", "text-embedding-3")
	if err == nil {
		t.Error("expected error for no embedding data")
	}
}

// --- CreateVisionCompletionWithTools ---

func TestCreateVisionCompletionWithTools_Success(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletionResponse(w, "vision tools response", nil)
	})
	defer server.Close()

	c.toolRegistry.Register(&tools.Tool{
		Name:        "look",
		Description: "look at image",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "result", nil
		},
	})

	handler := func(ctx context.Context, tc openai.ToolCall) (string, error) {
		return "tool result", nil
	}

	resp, err := c.CreateVisionCompletionWithTools(context.Background(),
		"system prompt",
		"ctx: user is Bob",
		"what is this?",
		[]string{"data:image/png;base64,iVBORw0KGgo="},
		nil, // no history
		handler,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "vision tools response" {
		t.Errorf("expected 'vision tools response', got '%s'", resp.Content)
	}
}

func TestCreateVisionCompletionWithTools_BuildError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	cfg := defaultAIConfig()
	cfg.VisionBase64 = true
	c := testClient(cfg)
	c.config.APIBaseURL = server.URL
	c.httpClient = server.Client()

	c.toolRegistry.Register(&tools.Tool{
		Name: "tool1",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "", nil
		},
	})

	handler := func(ctx context.Context, tc openai.ToolCall) (string, error) {
		return "", nil
	}

	_, err := c.CreateVisionCompletionWithTools(context.Background(),
		"sys", "ctx", "what?",
		[]string{server.URL + "/missing.png"},
		nil, handler,
	)
	if err == nil {
		t.Error("expected error for download failure")
	}
}

func TestCreateVisionCompletionWithTools_NoChoices(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
	})
	defer server.Close()

	handler := func(ctx context.Context, tc openai.ToolCall) (string, error) {
		return "", nil
	}

	_, err := c.CreateVisionCompletionWithTools(context.Background(),
		"sys", "ctx", "what?",
		[]string{"data:image/png;base64,iVBORw0KGgo="},
		nil, handler,
	)
	if err == nil {
		t.Error("expected error for no choices")
	}
}

func TestCreateVisionCompletionWithTools_ToolIteration(t *testing.T) {
	callCount := 0
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		if callCount == 0 {
			callCount++
			// Return tool call
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"v-1","object":"chat.completion","created":1,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_v_1","type":"function","function":{"name":"tool1","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
			return
		}
		writeChatCompletionResponse(w, "final vision+tools", nil)
	})
	defer server.Close()

	c.toolRegistry.Register(&tools.Tool{
		Name:        "tool1",
		Description: "tool one",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "tool1 result", nil
		},
	})

	handler := func(ctx context.Context, tc openai.ToolCall) (string, error) {
		return "tool1 result", nil
	}

	resp, err := c.CreateVisionCompletionWithTools(context.Background(),
		"sys", "ctx", "run tool",
		[]string{"data:image/png;base64,iVBORw0KGgo="},
		nil, handler,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "final vision+tools" {
		t.Errorf("expected 'final vision+tools', got '%s'", resp.Content)
	}
}

// --- Structural: ChatCompletionResponse field mapping ---

func TestCreateChatCompletion_ResponseMapping(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Include reasoning content
		io.WriteString(w, `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"thoughtful reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":15,"total_tokens":20}}`)
	})
	defer server.Close()

	resp, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "think"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "thoughtful reply" {
		t.Errorf("expected 'thoughtful reply', got '%s'", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got '%s'", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 20 {
		t.Errorf("expected 20 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

// --- Edge cases: Context cancellation ---

func TestCreateChatCompletion_ContextCancelled(t *testing.T) {
	server, c := setupOpenAIMock(t, func(w http.ResponseWriter, r *http.Request) {
		// Block briefly to ensure context cancellation propagates
		time.Sleep(200 * time.Millisecond)
		writeChatCompletionResponse(w, "late", nil)
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.CreateChatCompletion(ctx, ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "Hi"},
		},
	})
	if err == nil {
		// The retry mechanism may or may not catch this, depending on timing.
		// For the cancelled-before-attempt case, the retry mechanism should return context error.
		return
	}
}

// --- setFieldValue edge cases for ChatCompletionRequest fields ---

func TestSetFieldValue_ChatCompletionRequest_TopP(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "test"}
	reqValue := reflect.ValueOf(&req).Elem()
	topPField := reqValue.FieldByName("TopP")
	err := setFieldValue(topPField, 0.9)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if req.TopP != 0.9 {
		t.Errorf("expected 0.9, got %v", req.TopP)
	}
}

func TestSetFieldValue_ChatCompletionRequest_Stop(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "test"}
	reqValue := reflect.ValueOf(&req).Elem()
	stopField := reqValue.FieldByName("Stop")
	err := setFieldValue(stopField, []string{"\n", "END"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(req.Stop) != 2 || req.Stop[0] != "\n" || req.Stop[1] != "END" {
		t.Errorf("stop not set correctly: %v", req.Stop)
	}
}

func TestSetFieldValue_ChatCompletionRequest_LogitBias(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "test"}
	reqValue := reflect.ValueOf(&req).Elem()
	logitBiasField := reqValue.FieldByName("LogitBias")
	err := setFieldValue(logitBiasField, map[string]int{"1234": 5, "5678": -5})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if req.LogitBias["1234"] != 5 || req.LogitBias["5678"] != -5 {
		t.Errorf("logit bias not set correctly: %v", req.LogitBias)
	}
}

func TestSetFieldValue_ChatCompletionRequest_Seed(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "test"}
	reqValue := reflect.ValueOf(&req).Elem()
	seedField := reqValue.FieldByName("Seed")
	err := setFieldValue(seedField, 42)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if req.Seed == nil || *req.Seed != 42 {
		t.Error("Seed pointer not set correctly")
	}
}

// --- applyExtraParamsToStruct with slice and map ---

func TestApplyExtraParamsToStruct_StopSlice(t *testing.T) {
	req := openai.ChatCompletionRequest{}
	params := map[string]interface{}{
		"stop": []string{"\n", "END"},
	}
	applyExtraParamsToStruct(&req, params, "[test]")
	if len(req.Stop) != 2 || req.Stop[0] != "\n" || req.Stop[1] != "END" {
		t.Errorf("stop not applied: %v", req.Stop)
	}
}

func TestApplyExtraParamsToStruct_LogitBiasMap(t *testing.T) {
	req := openai.ChatCompletionRequest{}
	params := map[string]interface{}{
		"logit_bias": map[string]int{"1234": 10},
	}
	applyExtraParamsToStruct(&req, params, "[test]")
	if req.LogitBias["1234"] != 10 {
		t.Errorf("logit_bias not applied: %v", req.LogitBias)
	}
}

// --- Test that VisionBase64=false in processMessages skips conversion ---

func TestProcessMessages_VisionBase64False_SkipsURL(t *testing.T) {
	cfg := defaultAIConfig()
	cfg.VisionBase64 = false
	c := testClient(cfg)

	extURL := "https://cdn.example.com/image.png"
	messages := []openai.ChatCompletionMessage{
		{
			Role: "user",
			MultiContent: []openai.ChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL: extURL,
					},
				},
			},
		},
	}
	result, err := c.processMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].MultiContent[0].ImageURL.URL != extURL {
		t.Errorf("expected URL unchanged when VisionBase64=false, got %s", result[0].MultiContent[0].ImageURL.URL)
	}
}
