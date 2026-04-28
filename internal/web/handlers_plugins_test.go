package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ezyapper/internal/plugin"
)

type mockPluginMgr struct {
	plugins    []plugin.InfoExt
	enableErr  error
	disableErr error
}

func (m *mockPluginMgr) ListPluginsExt() []plugin.InfoExt {
	return m.plugins
}

func (m *mockPluginMgr) EnablePlugin(name string) error {
	if m.enableErr != nil {
		return m.enableErr
	}
	for i, p := range m.plugins {
		if p.Info.Name == name {
			m.plugins[i].Enabled = true
			return nil
		}
	}
	return fmt.Errorf("plugin %s not found", name)
}

func (m *mockPluginMgr) DisablePlugin(name string) error {
	if m.disableErr != nil {
		return m.disableErr
	}
	for i, p := range m.plugins {
		if p.Info.Name == name {
			if !p.Enabled {
				return nil
			}
			m.plugins[i].Enabled = false
			return nil
		}
	}
	return fmt.Errorf("plugin %s not found", name)
}

type mockToolRefresher struct {
	refreshed bool
}

func (m *mockToolRefresher) RefreshPluginTools() {
	m.refreshed = true
}

func testPluginsTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(templateFuncs()).Option("missingkey=error")

	base := `{{define "base"}}{{template "sidebar" .}}{{if .Flash}}{{template "toast" .Flash}}{{end}}{{template "content" .}}{{end}}`
	sidebar := `{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}" class="md3-nav-item{{if .Active}} md3-nav-item--active{{end}}">{{.Label}}</a>{{end}}{{end}}`
	toast := `{{define "toast"}}<div class="md3-toast md3-toast--{{.Type}}">{{.Message}}</div>{{end}}`
	content := `{{define "content"}}
{{if .Data.Plugins}}
<div class="md3-card-grid">
	{{range .Data.Plugins}}
	<div class="md3-card md3-card--elevated">
		<div class="md3-card__content">
			<div class="plugin-card__header">
				<h3 style="font: var(--md-sys-typescale-title-medium); margin: 0;">{{.Name}}</h3>
				<span class="md3-chip">{{.Version}}</span>
			</div>
			<p style="margin: 0.5rem 0;">{{.Description}}</p>
			<div class="plugin-card__meta">
				<span>{{.Author}}</span>
				<span>Priority: {{.Priority}}</span>
			</div>
			<div class="plugin-card__status">
				{{if .Enabled}}<span class="md3-chip md3-chip--enabled">Enabled</span>{{else}}<span class="md3-chip md3-chip--disabled">Disabled</span>{{end}}
				<form method="POST" action="/plugins/toggle" style="display:inline">
					<input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
					<input type="hidden" name="name" value="{{.Name}}">
					{{if .Enabled}}
					<input type="hidden" name="action" value="disable">
					<button type="submit" class="md3-btn md3-btn--text">Disable</button>
					{{else}}
					<input type="hidden" name="action" value="enable">
					<button type="submit" class="md3-btn md3-btn--filled">Enable</button>
					{{end}}
				</form>
			</div>
		</div>
	</div>
	{{end}}
</div>
{{else}}
<div class="md3-empty-state">
	<p>No plugins installed. Add plugins to plugins_dir in config.yaml.</p>
</div>
{{end}}
{{end}}`

	tmpl = template.Must(tmpl.Parse(base))
	tmpl = template.Must(tmpl.Parse(sidebar))
	tmpl = template.Must(tmpl.Parse(toast))
	tmpl = template.Must(tmpl.Parse(content))
	return &TemplateSet{templates: map[string]*template.Template{"plugins": tmpl}}
}

func TestPluginsHandler_GET_ListsPlugins(t *testing.T) {
	mgr := &mockPluginMgr{
		plugins: []plugin.InfoExt{
			{Info: plugin.Info{Name: "greeter", Version: "1.0.0", Author: "test", Description: "A greeting plugin", Priority: 10}, Enabled: true},
			{Info: plugin.Info{Name: "logger", Version: "0.5.0", Author: "dev", Description: "Logs messages", Priority: 5}, Enabled: false},
		},
	}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/plugins", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "greeter") {
		t.Error("expected plugin name 'greeter' in response")
	}
	if !strings.Contains(body, "logger") {
		t.Error("expected plugin name 'logger' in response")
	}
	if !strings.Contains(body, "1.0.0") {
		t.Error("expected version '1.0.0' in response")
	}
	if !strings.Contains(body, "A greeting plugin") {
		t.Error("expected description in response")
	}
	if !strings.Contains(body, "Enabled") {
		t.Error("expected enabled status in response")
	}
	if !strings.Contains(body, "Disabled") {
		t.Error("expected disabled status in response")
	}
}

func TestPluginsHandler_GET_NoPlugins(t *testing.T) {
	mgr := &mockPluginMgr{plugins: []plugin.InfoExt{}}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/plugins", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "No plugins installed") {
		t.Error("expected empty state message")
	}
}

func TestPluginsHandler_GET_NavItemsPresent(t *testing.T) {
	mgr := &mockPluginMgr{plugins: []plugin.InfoExt{}}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/plugins", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	navLabels := []string{"Dashboard", "Configuration", "Memories", "Profiles", "Channels", "Plugins", "Logs"}
	for _, label := range navLabels {
		if !strings.Contains(body, label) {
			t.Errorf("expected nav to contain %q", label)
		}
	}
}

func TestTogglePlugin_Enable(t *testing.T) {
	mgr := &mockPluginMgr{
		plugins: []plugin.InfoExt{
			{Info: plugin.Info{Name: "greeter", Version: "1.0.0", Author: "test", Description: "A greeting plugin"}, Enabled: false},
		},
	}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	form := url.Values{"name": {"greeter"}, "action": {"enable"}}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	loc := rec.Header().Get("Location")
	if loc != "/plugins" {
		t.Errorf("expected redirect to /plugins, got %q", loc)
	}

	if !mgr.plugins[0].Enabled {
		t.Error("expected plugin to be enabled after toggle")
	}

	if !refresher.refreshed {
		t.Error("expected RefreshPluginTools to be called")
	}
}

func TestTogglePlugin_Disable(t *testing.T) {
	mgr := &mockPluginMgr{
		plugins: []plugin.InfoExt{
			{Info: plugin.Info{Name: "greeter", Version: "1.0.0", Author: "test", Description: "A greeting plugin"}, Enabled: true},
		},
	}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	form := url.Values{"name": {"greeter"}, "action": {"disable"}}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	loc := rec.Header().Get("Location")
	if loc != "/plugins" {
		t.Errorf("expected redirect to /plugins, got %q", loc)
	}

	if mgr.plugins[0].Enabled {
		t.Error("expected plugin to be disabled after toggle")
	}

	if !refresher.refreshed {
		t.Error("expected RefreshPluginTools to be called")
	}
}

func TestTogglePlugin_NotFound(t *testing.T) {
	mgr := &mockPluginMgr{plugins: []plugin.InfoExt{}}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	form := url.Values{"name": {"nonexistent"}, "action": {"enable"}}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestTogglePlugin_InvalidAction(t *testing.T) {
	mgr := &mockPluginMgr{
		plugins: []plugin.InfoExt{
			{Info: plugin.Info{Name: "greeter", Version: "1.0.0"}, Enabled: true},
		},
	}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	form := url.Values{"name": {"greeter"}, "action": {"invalid"}}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestTogglePlugin_MissingName(t *testing.T) {
	mgr := &mockPluginMgr{
		plugins: []plugin.InfoExt{
			{Info: plugin.Info{Name: "greeter", Version: "1.0.0"}, Enabled: true},
		},
	}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	form := url.Values{"action": {"enable"}}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestPluginsHandler_MethodNotAllowed(t *testing.T) {
	mgr := &mockPluginMgr{plugins: []plugin.InfoExt{}}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	req := httptest.NewRequest(http.MethodPut, "/plugins", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestTogglePlugin_Enable_FlashMessage(t *testing.T) {
	mgr := &mockPluginMgr{
		plugins: []plugin.InfoExt{
			{Info: plugin.Info{Name: "greeter", Version: "1.0.0", Author: "test", Description: "A greeting plugin"}, Enabled: false},
		},
	}
	refresher := &mockToolRefresher{}
	tmpl := testPluginsTemplate()
	handler := PluginsHandler(mgr, refresher, tmpl)

	form := url.Values{"name": {"greeter"}, "action": {"enable"}}
	req := httptest.NewRequest(http.MethodPost, "/plugins/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	cookies := rec.Result().Cookies()
	var foundFlash bool
	for _, c := range cookies {
		if c.Name == "flash_type" && c.Value == "success" {
			foundFlash = true
			break
		}
	}
	if !foundFlash {
		t.Error("expected success flash cookie to be set")
	}
}
