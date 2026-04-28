package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"ezyapper/internal/config"
)

// lookupDiscordInfo resolves known IDs to names; falls back to raw ID.
type lookupDiscordInfo struct{}

func (m *lookupDiscordInfo) GetChannelName(channelID string) string {
	if channelID == "chan123" {
		return "general"
	}
	return channelID
}

func (m *lookupDiscordInfo) GetUserName(guildID, userID string) string {
	if userID == "user456" {
		return "testuser"
	}
	return userID
}

func (m *lookupDiscordInfo) GetGuildName(guildID string) string {
	if guildID == "guild789" {
		return "testguild"
	}
	return guildID
}

// testChannelsTemplate returns a minimal template for channels page testing.
func testChannelsTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(templateFuncs()).Option("missingkey=error")

	base := `{{define "base"}}{{template "sidebar" .}}{{if .Flash}}{{template "toast" .Flash}}{{end}}{{template "content" .}}{{end}}`
	sidebar := `{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}" class="md3-nav-item{{if .Active}} md3-nav-item--active{{end}}">{{.Label}}</a>{{end}}{{end}}`
	toast := `{{define "toast"}}<div class="md3-toast md3-toast--{{.Type}}">{{.Message}}</div>{{end}}`

	content := `{{define "content"}}
<div class="md3-card-grid">
	<div class="md3-card md3-card--elevated">
		<div class="md3-card__content">
			<h2>Blacklist</h2>
			{{template "channelSubsection" dict "Title" "Users" "Type" "user" "Entries" .Data.Blacklist.Users "CSRFToken" $.CSRFToken "Prefix" "blacklist"}}
			{{template "channelSubsection" dict "Title" "Channels" "Type" "channel" "Entries" .Data.Blacklist.Channels "CSRFToken" $.CSRFToken "Prefix" "blacklist"}}
			{{template "channelSubsection" dict "Title" "Guilds" "Type" "guild" "Entries" .Data.Blacklist.Guilds "CSRFToken" $.CSRFToken "Prefix" "blacklist"}}
		</div>
	</div>
	<div class="md3-card md3-card--elevated">
		<div class="md3-card__content">
			<h2>Whitelist</h2>
			{{template "channelSubsection" dict "Title" "Users" "Type" "user" "Entries" .Data.Whitelist.Users "CSRFToken" $.CSRFToken "Prefix" "whitelist"}}
			{{template "channelSubsection" dict "Title" "Channels" "Type" "channel" "Entries" .Data.Whitelist.Channels "CSRFToken" $.CSRFToken "Prefix" "whitelist"}}
			{{template "channelSubsection" dict "Title" "Guilds" "Type" "guild" "Entries" .Data.Whitelist.Guilds "CSRFToken" $.CSRFToken "Prefix" "whitelist"}}
		</div>
	</div>
</div>
{{end}}
{{define "channelSubsection"}}
<div class="channel-subsection">
	<h3>{{.Title}}</h3>
	{{if .Entries}}
	{{range .Entries}}
	<div class="channel-item">
		<span>{{.Name}}</span>
		<span>({{.ID}})</span>
		<form method="POST" action="/channels/{{$.Prefix}}/remove" style="display:inline">
			<input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
			<input type="hidden" name="type" value="{{.Type}}">
			<input type="hidden" name="id" value="{{.ID}}">
			<button type="submit">Remove</button>
		</form>
	</div>
	{{end}}
	{{else}}
	<p>No entries.</p>
	{{end}}
	<form method="POST" action="/channels/{{$.Prefix}}/add" class="channel-add-form">
		<input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
		<input type="hidden" name="type" value="{{.Type}}">
		<input type="text" name="id" placeholder="Enter ID" required>
		<button type="submit">Add</button>
	</form>
</div>
{{end}}`

	tmpl = template.Must(tmpl.Parse(base))
	tmpl = template.Must(tmpl.Parse(sidebar))
	tmpl = template.Must(tmpl.Parse(toast))
	tmpl = template.Must(tmpl.Parse(content))

	return &TemplateSet{templates: map[string]*template.Template{"channels": tmpl}}
}

func newChannelsConfigStore(t *testing.T) *atomic.Value {
	t.Helper()
	cfg := loadTestConfig(t)
	var store atomic.Value
	store.Store(cfg)
	return &store
}

func TestChannelsHandler_GET_ShowsPage(t *testing.T) {
	store := newChannelsConfigStore(t)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(store, info, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/channels", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Blacklist") {
		t.Error("expected page to contain 'Blacklist'")
	}
	if !strings.Contains(body, "Whitelist") {
		t.Error("expected page to contain 'Whitelist'")
	}
	if !strings.Contains(body, "No entries.") {
		t.Error("expected page to show empty state")
	}
}

func TestChannelsHandler_GET_ResolvesNames(t *testing.T) {
	cfg := loadTestConfig(t)
	cfg.Blacklist.Users = []string{"user456"}
	cfg.Blacklist.Channels = []string{"chan123"}
	cfg.Blacklist.Guilds = []string{"guild789"}

	var store atomic.Value
	store.Store(cfg)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(&store, info, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/channels", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "testuser") {
		t.Errorf("expected resolved username 'testuser', got body: %s", body)
	}
	if !strings.Contains(body, "general") {
		t.Errorf("expected resolved channel name 'general', got body: %s", body)
	}
	if !strings.Contains(body, "testguild") {
		t.Errorf("expected resolved guild name 'testguild', got body: %s", body)
	}
}

func TestChannelsHandler_GET_NavItemsPresent(t *testing.T) {
	store := newChannelsConfigStore(t)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(store, info, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/channels", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, label := range []string{"Dashboard", "Configuration", "Channels"} {
		if !strings.Contains(body, label) {
			t.Errorf("expected nav to contain %q", label)
		}
	}
}

func TestAddToBlacklist_ValidUser(t *testing.T) {
	store := newChannelsConfigStore(t)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(store, info, tmpl)

	form := url.Values{
		"type": {"user"},
		"id":   {"newuser123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/channels/blacklist/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	loc := rec.Header().Get("Location")
	if loc != "/channels" {
		t.Errorf("expected redirect to /channels, got %q", loc)
	}

	updatedCfg := store.Load().(*config.Config)
	if len(updatedCfg.Blacklist.Users) != 1 || updatedCfg.Blacklist.Users[0] != "newuser123" {
		t.Errorf("expected Blacklist.Users to contain newuser123, got %v", updatedCfg.Blacklist.Users)
	}
}

func TestAddToBlacklist_Duplicate(t *testing.T) {
	cfg := loadTestConfig(t)
	cfg.Blacklist.Users = []string{"existing123"}
	var store atomic.Value
	store.Store(cfg)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(&store, info, tmpl)

	form := url.Values{
		"type": {"user"},
		"id":   {"existing123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/channels/blacklist/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 for duplicate error, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Entry already exists") {
		t.Errorf("expected error flash 'Entry already exists', got body: %s", body)
	}

	storedCfg := store.Load().(*config.Config)
	if len(storedCfg.Blacklist.Users) != 1 {
		t.Errorf("expected Blacklist.Users to still have 1 entry, got %d", len(storedCfg.Blacklist.Users))
	}
}

func TestAddToBlacklist_InvalidType(t *testing.T) {
	store := newChannelsConfigStore(t)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(store, info, tmpl)

	form := url.Values{
		"type": {"invalid"},
		"id":   {"whatever"},
	}
	req := httptest.NewRequest(http.MethodPost, "/channels/blacklist/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 for invalid type error, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Invalid type") {
		t.Errorf("expected error flash 'Invalid type', got body: %s", body)
	}
}

func TestRemoveFromBlacklist_Works(t *testing.T) {
	cfg := loadTestConfig(t)
	cfg.Blacklist.Users = []string{"user456", "user789"}
	var store atomic.Value
	store.Store(cfg)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(&store, info, tmpl)

	form := url.Values{
		"type": {"user"},
		"id":   {"user456"},
	}
	req := httptest.NewRequest(http.MethodPost, "/channels/blacklist/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	updatedCfg := store.Load().(*config.Config)
	if len(updatedCfg.Blacklist.Users) != 1 || updatedCfg.Blacklist.Users[0] != "user789" {
		t.Errorf("expected Blacklist.Users to have 1 entry (user789), got %v", updatedCfg.Blacklist.Users)
	}
}

func TestRemoveFromBlacklist_NotFound(t *testing.T) {
	cfg := loadTestConfig(t)
	cfg.Blacklist.Users = []string{"user456"}
	var store atomic.Value
	store.Store(cfg)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(&store, info, tmpl)

	form := url.Values{
		"type": {"user"},
		"id":   {"nonexistent"},
	}
	req := httptest.NewRequest(http.MethodPost, "/channels/blacklist/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 for not-found error, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Entry not found") {
		t.Errorf("expected error flash 'Entry not found', got body: %s", body)
	}
}

func TestAddToWhitelist_Valid(t *testing.T) {
	store := newChannelsConfigStore(t)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(store, info, tmpl)

	form := url.Values{
		"type": {"channel"},
		"id":   {"whitelist-chan"},
	}
	req := httptest.NewRequest(http.MethodPost, "/channels/whitelist/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	loc := rec.Header().Get("Location")
	if loc != "/channels" {
		t.Errorf("expected redirect to /channels, got %q", loc)
	}

	updatedCfg := store.Load().(*config.Config)
	if len(updatedCfg.Whitelist.Channels) != 1 || updatedCfg.Whitelist.Channels[0] != "whitelist-chan" {
		t.Errorf("expected Whitelist.Channels to contain whitelist-chan, got %v", updatedCfg.Whitelist.Channels)
	}
}

func TestRemoveFromWhitelist_Works(t *testing.T) {
	cfg := loadTestConfig(t)
	cfg.Whitelist.Guilds = []string{"guild789"}
	var store atomic.Value
	store.Store(cfg)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(&store, info, tmpl)

	form := url.Values{
		"type": {"guild"},
		"id":   {"guild789"},
	}
	req := httptest.NewRequest(http.MethodPost, "/channels/whitelist/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	updatedCfg := store.Load().(*config.Config)
	if len(updatedCfg.Whitelist.Guilds) != 0 {
		t.Errorf("expected Whitelist.Guilds to be empty, got %v", updatedCfg.Whitelist.Guilds)
	}
}

func TestChannelsHandler_MethodNotAllowed(t *testing.T) {
	store := newChannelsConfigStore(t)
	info := &lookupDiscordInfo{}
	tmpl := testChannelsTemplate()
	handler := ChannelsHandler(store, info, tmpl)

	req := httptest.NewRequest(http.MethodPut, "/channels", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for unknown method/path, got %d", rec.Code)
	}
}
