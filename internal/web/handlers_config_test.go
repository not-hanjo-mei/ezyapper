package web

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"ezyapper/internal/config"
)

const testConfigYAML = `schema_version: 3
core:
  discord:
    token: "test-token"
    bot_name: "TestBot"
    reply_percentage: 0.5
    cooldown_seconds: 5
    max_responses_per_minute: 10
    consolidation_timeout_sec: 300
    typing_indicator_interval_sec: 5
    long_response_delay_ms: 500
    chunk_split_delay_sec: 2
    reply_truncation_length: 200
    image_cache_ttl_min: 60
    image_cache_max_entries: 100
    rate_limit:
      reset_period_seconds: 60
  ai:
    api_base_url: "https://api.openai.com"
    api_key: "sk-test"
    model: "gpt-4"
    vision_model: "gpt-4-vision"
    vision:
      mode: "hybrid"
      description_prompt: "test"
      max_images: 4
    max_tokens: 1024
    temperature: 0.8
    retry_count: 3
    timeout: 30
    http_timeout_sec: 30
    max_tool_iterations: 10
    max_image_bytes: 5242880
    user_agent: "ezyapper-test"
    system_prompt: "test"
  decision:
    enabled: false
memory_pipeline:
  embedding:
    model: "text-embedding-3-small"
    timeout: 30
    retry_count: 0
  memory:
    consolidation_interval: 10
    short_term_limit: 20
    max_paginated_limit: 100
    embedding_cache_max_size: 500
    embedding_cache_ttl_min: 30
    eviction_interval_min: 5
    retry_base_delay_ms: 100
    retry_max_delay_ms: 5000
    max_retries: 3
    retrieval:
      top_k: 0
      min_score: 0.0
      default_top_k: 5
      default_min_score: 0.75
    consolidation:
      enabled: false
      memory_search_limit: 20
      worker_queue_size: 10
  qdrant:
    host: "localhost"
    port: 6333
    vector_size: 1536
access_control:
  blacklist:
  whitelist:
operations:
  web:
    enabled: false
    port: 8080
    username: ""
    password: ""
  logging:
    level: "info"
    file: "ezyapper.log"
    max_size: 100
    max_backups: 3
    max_age: 7
  plugins:
    enabled: false
  mcp:
    enabled: false
    servers:
      - name: "test-server"
        type: "stdio"
        command: "echo"
        args: ["hello"]
  operations:
    shutdown_timeout_sec: 300
    cleanup_interval_min: 5
`

func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(testConfigYAML), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}
	return cfg
}

func testConfigTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(templateFuncs()).Option("missingkey=error")

	base := `{{define "base"}}{{template "sidebar" .}}{{if .Flash}}{{template "toast" .Flash}}{{end}}{{template "content" .}}{{end}}`
	sidebar := `{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}" class="md3-nav-item{{if .Active}} md3-nav-item--active{{end}}">{{.Label}}</a>{{end}}{{end}}`
	toast := `{{define "toast"}}<div class="md3-toast md3-toast--{{.Type}}">{{.Message}}</div>{{end}}`
	formField := `{{define "form_field"}}<div class="md3-form-field"><label for="field_{{.Name}}">{{.Label}}</label><input id="field_{{.Name}}" name="{{.Name}}" type="{{or .Type "text"}}" value="{{.Value}}" class="md3-text-field"></div>{{end}}`
	content := `{{define "content"}}<form method="POST" action="/config"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input name="bot_name" value="{{.Data.Discord.BotName}}"><input name="consolidation_interval" type="number" value="{{.Data.Memory.ConsolidationInterval}}"><input name="retrieval_min_score" type="number" value="{{.Data.Memory.Retrieval.MinScore}}">{{if .Data.MCP.Servers}}<table class="md3-table"><thead><tr><th>Name</th><th>Type</th></tr></thead><tbody>{{range .Data.MCP.Servers}}<tr><td>{{.Name}}</td><td>{{.Type}}</td></tr>{{end}}</tbody></table>{{else}}<div class="md3-empty-state">No MCP servers configured.</div>{{end}}<div class="md3-tab-panel" id="tab-ratelimit"><p>Rate limit settings are read-only.</p><table class="md3-table"><tr><td>Cooldown (seconds)</td><td>{{.Data.Discord.CooldownSeconds}}</td></tr><tr><td>Max Responses/Minute</td><td>{{.Data.Discord.MaxResponsesPerMin}}</td></tr></table></div><button type="submit">Save</button></form>{{end}}`

	tmpl = template.Must(tmpl.Parse(base))
	tmpl = template.Must(tmpl.Parse(sidebar))
	tmpl = template.Must(tmpl.Parse(toast))
	tmpl = template.Must(tmpl.Parse(formField))
	tmpl = template.Must(tmpl.Parse(content))

	return &TemplateSet{templates: map[string]*template.Template{"config": tmpl}}
}

func newConfigStore(t *testing.T) *atomic.Value {
	t.Helper()
	cfg := loadTestConfig(t)
	var store atomic.Value
	store.Store(cfg)
	return &store
}

func requestWithCSRF(r *http.Request) *http.Request {
	token, _ := GenerateCSRFToken()
	ctx := context.WithValue(r.Context(), csrfCtxKey, token)
	return r.WithContext(ctx)
}

func TestConfigHandler_GET_ReturnsPage(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestConfigHandler_GET_ContainsCSRFToken(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Error("expected page to contain CSRF token hidden input")
	}
}

func TestConfigHandler_POST_ValidDiscordSettings(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	form := url.Values{
		"bot_name":                 {"NewBotName"},
		"reply_percentage":         {"75"},
		"cooldown_seconds":         {"30"},
		"max_responses_per_minute": {"20"},
	}
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	loc := rec.Header().Get("Location")
	if loc != "/config" {
		t.Errorf("expected redirect to /config, got %q", loc)
	}

	updatedCfg := store.Load().(*config.Config)
	if updatedCfg.Discord.BotName != "NewBotName" {
		t.Errorf("expected BotName 'NewBotName', got %q", updatedCfg.Discord.BotName)
	}
	if updatedCfg.Discord.ReplyPercentage != 0.75 {
		t.Errorf("expected ReplyPercentage 0.75, got %f", updatedCfg.Discord.ReplyPercentage)
	}
	if updatedCfg.Discord.CooldownSeconds != 30 {
		t.Errorf("expected CooldownSeconds 30, got %d", updatedCfg.Discord.CooldownSeconds)
	}
	if updatedCfg.Discord.MaxResponsesPerMin != 20 {
		t.Errorf("expected MaxResponsesPerMin 20, got %d", updatedCfg.Discord.MaxResponsesPerMin)
	}
}

func TestConfigHandler_POST_InvalidReplyPercentage(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	form := url.Values{
		"reply_percentage": {"150"},
	}
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "reply_percentage") {
		t.Error("expected error message to mention reply_percentage")
	}
}

func TestConfigHandler_POST_InvalidVisionMode(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	form := url.Values{
		"vision_mode": {"invalid_mode"},
	}
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "vision.mode") {
		t.Error("expected error message to mention vision.mode")
	}
}

func TestConfigHandler_POST_RollbackOnValidateFail(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(testConfigYAML), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}

	var store atomic.Value
	store.Store(cfg)

	originalContent, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read original config: %v", err)
	}

	tmpl := testConfigTemplate()
	handler := ConfigHandler(&store, tmpl, nil)

	form := url.Values{
		"reply_percentage": {"999"},
	}
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	currentContent, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after POST: %v", err)
	}
	if string(currentContent) != string(originalContent) {
		t.Error("validate fail should NOT write to YAML (rollback)")
	}

	storedCfg := store.Load().(*config.Config)
	if storedCfg.Discord.ReplyPercentage != 0.5 {
		t.Errorf("store should NOT be updated on validation failure, got ReplyPercentage %f", storedCfg.Discord.ReplyPercentage)
	}
}

func TestConfigHandler_MethodNotAllowed(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	req := httptest.NewRequest(http.MethodPut, "/config", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestConfigHandler_POST_MemorySettings(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	form := url.Values{
		"consolidation_interval": {"50"},
		"short_term_limit":       {"30"},
		"retrieval_top_k":        {"8"},
		"retrieval_min_score":    {"0.75"},
	}
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	updatedCfg := store.Load().(*config.Config)
	if updatedCfg.Memory.ConsolidationInterval != 50 {
		t.Errorf("expected ConsolidationInterval 50, got %d", updatedCfg.Memory.ConsolidationInterval)
	}
	if updatedCfg.Memory.ShortTermLimit != 30 {
		t.Errorf("expected ShortTermLimit 30, got %d", updatedCfg.Memory.ShortTermLimit)
	}
	if updatedCfg.Memory.Retrieval.TopK != 8 {
		t.Errorf("expected TopK 8, got %d", updatedCfg.Memory.Retrieval.TopK)
	}
	if updatedCfg.Memory.Retrieval.MinScore != 0.75 {
		t.Errorf("expected MinScore 0.75, got %f", updatedCfg.Memory.Retrieval.MinScore)
	}
}

func TestConfigHandler_POST_InvalidMinScore(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	form := url.Values{
		"retrieval_min_score": {"2.5"},
	}
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "min_score") {
		t.Error("expected error message to mention min_score")
	}
}

func TestConfigHandler_GET_MCPTableDisplayed(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "md3-table") {
		t.Error("expected page to contain MCP servers table")
	}
	if !strings.Contains(body, "test-server") {
		t.Error("expected page to mention test-server in MCP table")
	}
}

func TestConfigHandler_GET_RateLimitReadOnly(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "Rate limit settings are read-only") {
		t.Error("expected page to contain read-only rate limit notice")
	}
	if !strings.Contains(body, "Cooldown") {
		t.Error("expected page to contain Cooldown row")
	}
	if !strings.Contains(body, "5") {
		t.Error("expected page to contain cooldown value 5")
	}
	if !strings.Contains(body, "10") {
		t.Error("expected page to contain max responses value 10")
	}
}

func TestConfigHandler_ValidMemorySettingsPersisted(t *testing.T) {
	store := newConfigStore(t)
	tmpl := testConfigTemplate()
	handler := ConfigHandler(store, tmpl, nil)

	form := url.Values{
		"consolidation_interval": {"100"},
		"short_term_limit":       {"50"},
		"retrieval_top_k":        {"15"},
		"retrieval_min_score":    {"0.3"},
	}
	req := httptest.NewRequest(http.MethodPost, "/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected status 303, got %d", rec.Code)
	}

	storedCfg := store.Load().(*config.Config)
	if storedCfg.Memory.ConsolidationInterval != 100 {
		t.Errorf("store: expected ConsolidationInterval 100, got %d", storedCfg.Memory.ConsolidationInterval)
	}
	if storedCfg.Memory.ShortTermLimit != 50 {
		t.Errorf("store: expected ShortTermLimit 50, got %d", storedCfg.Memory.ShortTermLimit)
	}
	if storedCfg.Memory.Retrieval.TopK != 15 {
		t.Errorf("store: expected TopK 15, got %d", storedCfg.Memory.Retrieval.TopK)
	}
	if storedCfg.Memory.Retrieval.MinScore != 0.3 {
		t.Errorf("store: expected MinScore 0.3, got %f", storedCfg.Memory.Retrieval.MinScore)
	}
}
