package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/memory"
)

type testProfileStore struct {
	profile       *memory.Profile
	getProfileErr error
	updatedProfile *memory.Profile
	updateErr     error
}

func (m *testProfileStore) GetProfile(ctx context.Context, userID string) (*memory.Profile, error) {
	if m.getProfileErr != nil {
		return nil, m.getProfileErr
	}
	if m.profile != nil && m.profile.UserID == userID {
		return m.profile, nil
	}
	return nil, fmt.Errorf("profile not found for %s", userID)
}

func (m *testProfileStore) UpdateProfile(ctx context.Context, p *memory.Profile) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedProfile = p
	return nil
}

func (m *testProfileStore) GetUserStats(ctx context.Context, userID string) (*memory.UserStats, error) {
	return &memory.UserStats{UserID: userID}, nil
}

func testProfilesTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(templateFuncs()).Option("missingkey=error")

	base := `{{define "base"}}{{template "sidebar" .}}{{if .Flash}}{{template "toast" .Flash}}{{end}}{{template "content" .}}{{end}}`
	sidebar := `{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}" class="md3-nav-item{{if .Active}} md3-nav-item--active{{end}}">{{.Label}}</a>{{end}}{{end}}`
	toast := `{{define "toast"}}<div class="md3-toast md3-toast--{{.Type}}">{{.Message}}</div>{{end}}`
	content := `{{define "content"}}
{{if not .Data.Searched}}
<div class="profiles-empty">
	<div class="profiles-empty__text">Enter a User ID to view their profile</div>
</div>
{{else if not .Data.Found}}
<div class="profiles-empty">
	<div class="profiles-empty__text">No profile found</div>
	<div class="profiles-empty__hint">Profile will be auto-created on next bot interaction. User ID: {{.Data.UserID}}</div>
</div>
{{else if .Data.EditMode}}
<div class="profile-header">
	<div class="profile-header__name">{{.Data.Profile.DisplayName}}</div>
	<div class="profile-header__id">{{.Data.Profile.UserID}}</div>
</div>
<form method="POST" action="/profiles/update" class="profile-edit-form">
	<input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
	<input type="hidden" name="userID" value="{{.Data.Profile.UserID}}">
	<div class="profile-edit-form__field">
		<label for="display-name">Display Name</label>
		<input type="text" id="display-name" name="display_name" value="{{.Data.Profile.DisplayName}}" class="md3-text-field">
	</div>
	<div class="profile-noedit-section">
		<div class="profile-noedit-section__title">Traits</div>
		<div class="profile-chips">{{range .Data.Profile.Traits}}<span class="profile-chip">{{.}}</span>{{end}}</div>
	</div>
	<div class="profile-noedit-section">
		<div class="profile-noedit-section__title">Interests</div>
		<div class="profile-chips">{{range .Data.Profile.Interests}}<span class="profile-chip">{{.}}</span>{{end}}</div>
	</div>
	<div class="profile-edit-form__actions">
		<button type="submit" class="md3-btn md3-btn--filled">Save</button>
		<a href="/profiles?userID={{.Data.Profile.UserID}}" class="md3-btn md3-btn--text">Cancel</a>
	</div>
</form>
{{else}}
<div class="profile-header">
	<div class="profile-header__name">{{.Data.Profile.DisplayName}}</div>
	<div class="profile-header__id">{{.Data.Profile.UserID}}</div>
	<a href="/profiles?userID={{.Data.Profile.UserID}}&edit=true">Edit</a>
</div>
<div class="profile-stats">
	<div class="profile-stat"><div class="profile-stat__value">{{.Data.Profile.MessageCount}}</div></div>
	<div class="profile-stat"><div class="profile-stat__value">{{.Data.Profile.MemoryCount}}</div></div>
</div>
<div class="profile-section">
	<div class="profile-section__title">Traits</div>
	<div class="profile-chips">{{range .Data.Profile.Traits}}<span class="profile-chip">{{.}}</span>{{end}}</div>
</div>
<div class="profile-section">
	<div class="profile-section__title">Facts</div>
	{{range $key, $value := .Data.Profile.Facts}}<span class="profile-kv__key">{{$key}}</span><span class="profile-kv__value">{{$value}}</span>{{end}}
</div>
<div class="profile-section">
	<div class="profile-section__title">Preferences</div>
	{{range $key, $value := .Data.Profile.Preferences}}<span class="profile-kv__key">{{$key}}</span><span class="profile-kv__value">{{$value}}</span>{{end}}
</div>
<div class="profile-section">
	<div class="profile-section__title">Interests</div>
	<div class="profile-chips">{{range .Data.Profile.Interests}}<span class="profile-chip">{{.}}</span>{{end}}</div>
</div>
{{end}}
{{end}}`

	tmpl = template.Must(tmpl.Parse(base))
	tmpl = template.Must(tmpl.Parse(sidebar))
	tmpl = template.Must(tmpl.Parse(toast))
	tmpl = template.Must(tmpl.Parse(content))
	return &TemplateSet{templates: map[string]*template.Template{"profiles": tmpl}}
}

func TestProfilesHandler_GET_NoUserID(t *testing.T) {
	store := &testProfileStore{}
	tmpl := testProfilesTemplate()
	handler := ProfilesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/profiles", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Enter a User ID to view their profile") {
		t.Error("expected prompt to enter User ID")
	}
	if strings.Contains(body, "profile-header") {
		t.Error("expected no profile data when no userID")
	}
}

func TestProfilesHandler_GET_ProfileExists(t *testing.T) {
	now := time.Date(2025, 3, 15, 14, 30, 0, 0, time.UTC)
	store := &testProfileStore{
		profile: &memory.Profile{
			UserID:      "user123",
			DisplayName: "TestUser",
			Traits:      []string{"friendly", "helpful"},
			Facts:       map[string]string{"location": "Tokyo", "language": "Japanese"},
			Preferences: map[string]string{"theme": "dark"},
			Interests:   []string{"coding", "music"},
			MessageCount: 42,
			MemoryCount:  15,
			FirstSeenAt:  now.Add(-30 * 24 * time.Hour),
			LastActiveAt: now,
		},
	}
	tmpl := testProfilesTemplate()
	handler := ProfilesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/profiles?userID=user123", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "TestUser") {
		t.Error("expected display name 'TestUser'")
	}
	if !strings.Contains(body, "user123") {
		t.Error("expected userID 'user123'")
	}
	if !strings.Contains(body, "friendly") {
		t.Error("expected trait 'friendly'")
	}
	if !strings.Contains(body, "helpful") {
		t.Error("expected trait 'helpful'")
	}
	if !strings.Contains(body, "location") {
		t.Error("expected fact key 'location'")
	}
	if !strings.Contains(body, "Tokyo") {
		t.Error("expected fact value 'Tokyo'")
	}
	if !strings.Contains(body, "theme") {
		t.Error("expected preference key 'theme'")
	}
	if !strings.Contains(body, "dark") {
		t.Error("expected preference value 'dark'")
	}
	if !strings.Contains(body, "coding") {
		t.Error("expected interest 'coding'")
	}
	if !strings.Contains(body, "music") {
		t.Error("expected interest 'music'")
	}
	if !strings.Contains(body, "42") {
		t.Error("expected message count 42")
	}
	if !strings.Contains(body, "15") {
		t.Error("expected memory count 15")
	}
	if !strings.Contains(body, "Edit") {
		t.Error("expected Edit button in view mode")
	}
	if !strings.Contains(body, "edit=true") {
		t.Error("expected edit link")
	}
}

func TestProfilesHandler_GET_ProfileNotFound(t *testing.T) {
	store := &testProfileStore{}
	tmpl := testProfilesTemplate()
	handler := ProfilesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/profiles?userID=nonexistent", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "No profile found") {
		t.Errorf("expected 'No profile found', got: %s", body)
	}
	if !strings.Contains(body, "Profile will be auto-created") {
		t.Error("expected auto-creation hint")
	}
	if strings.Contains(body, "profile-header") {
		t.Error("expected no profile data when not found")
	}
}

func TestProfilesHandler_GET_EditMode(t *testing.T) {
	store := &testProfileStore{
		profile: &memory.Profile{
			UserID:      "user456",
			DisplayName: "EditMe",
			Traits:      []string{"curious"},
			Interests:   []string{"reading"},
		},
	}
	tmpl := testProfilesTemplate()
	handler := ProfilesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/profiles?userID=user456&edit=true", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "EditMe") {
		t.Error("expected display name 'EditMe' in form field")
	}
	if !strings.Contains(body, "display_name") {
		t.Error("expected display_name form field")
	}
	if !strings.Contains(body, "Save") {
		t.Error("expected Save button")
	}
	if !strings.Contains(body, "Cancel") {
		t.Error("expected Cancel link")
	}
	if !strings.Contains(body, "curious") {
		t.Error("expected trait 'curious' in read-only section")
	}
	if !strings.Contains(body, "reading") {
		t.Error("expected interest 'reading' in read-only section")
	}
	if !strings.Contains(body, "/profiles/update") {
		t.Error("expected form action to post to /profiles/update")
	}
}

func TestProfilesHandler_Update_ValidData(t *testing.T) {
	store := &testProfileStore{
		profile: &memory.Profile{
			UserID:      "user789",
			DisplayName: "OldName",
			Traits:      []string{"patient"},
			MessageCount: 10,
		},
	}
	tmpl := testProfilesTemplate()
	handler := ProfilesHandler(store, tmpl)

	form := url.Values{
		"userID":       {"user789"},
		"display_name": {"NewName"},
	}
	req := httptest.NewRequest(http.MethodPost, "/profiles/update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	loc := rec.Header().Get("Location")
	if loc != "/profiles?userID=user789" {
		t.Errorf("expected redirect to /profiles?userID=user789, got %q", loc)
	}

	if store.updatedProfile == nil {
		t.Fatal("expected UpdateProfile to be called")
	}
	if store.updatedProfile.DisplayName != "NewName" {
		t.Errorf("expected DisplayName 'NewName', got %q", store.updatedProfile.DisplayName)
	}
	if store.updatedProfile.UserID != "user789" {
		t.Errorf("expected UserID 'user789', got %q", store.updatedProfile.UserID)
	}
}

func TestProfilesHandler_Update_MissingUserID(t *testing.T) {
	store := &testProfileStore{}
	tmpl := testProfilesTemplate()
	handler := ProfilesHandler(store, tmpl)

	form := url.Values{
		"display_name": {"SomeName"},
	}
	req := httptest.NewRequest(http.MethodPost, "/profiles/update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestProfilesHandler_NavItemsPresent(t *testing.T) {
	store := &testProfileStore{}
	tmpl := testProfilesTemplate()
	handler := ProfilesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/profiles", nil)
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

func TestProfilesHandler_MethodNotAllowed(t *testing.T) {
	store := &testProfileStore{}
	tmpl := testProfilesTemplate()
	handler := ProfilesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodPut, "/profiles", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for unknown method, got %d", rec.Code)
	}
}
