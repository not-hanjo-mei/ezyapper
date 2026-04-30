package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Test 1: CountMemories error → GetDashboardStats returns error ---

func TestStatsErr_CountMemories_ErrorPropagates(t *testing.T) {
	memStore := &mockMemoryStore{
		countMemories: 0,
		countErr:      fmt.Errorf("qdrant connection refused"),
		getStatsErr:   fmt.Errorf("qdrant get stats failed"),
	}
	profStore := &mockProfileStore{countProfiles: 10}
	stats := NewStatsProvider(memStore, profStore, newTestCfgStore())

	_, err := stats.GetDashboardStats(context.Background())
	if err == nil {
		t.Fatal("expected error from GetDashboardStats when CountMemories fails, got nil")
	}
	if !strings.Contains(err.Error(), "count memories") {
		t.Errorf("expected error to contain 'count memories', got: %v", err)
	}
}

// --- Test 2: CountProfiles error → GetDashboardStats returns error ---

func TestStatsErr_CountProfiles_ErrorPropagates(t *testing.T) {
	memStore := &mockMemoryStore{
		countMemories: 42,
		countProfiles: 0,
		getStatsErr:   fmt.Errorf("qdrant get stats failed"),
	}
	profStore := &mockProfileStore{
		countProfiles: 0,
		countErr:      fmt.Errorf("qdrant unavailable"),
	}
	stats := NewStatsProvider(memStore, profStore, newTestCfgStore())

	_, err := stats.GetDashboardStats(context.Background())
	if err == nil {
		t.Fatal("expected error from GetDashboardStats when CountProfiles fails, got nil")
	}
	if !strings.Contains(err.Error(), "count profiles") {
		t.Errorf("expected error to contain 'count profiles', got: %v", err)
	}
}

// --- Test 3: GetMemories DB error → handler renders error page ---

func testMemoriesErrorTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(templateFuncs()).Option("missingkey=error")

	base := `{{define "base"}}{{template "sidebar" .}}{{if .Flash}}{{template "toast" .Flash}}{{end}}{{template "content" .}}{{end}}`
	sidebar := `{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}" class="md3-nav-item{{if .Active}} md3-nav-item--active{{end}}">{{.Label}}</a>{{end}}{{end}}`
	toast := `{{define "toast"}}<div class="md3-toast md3-toast--{{.Type}}">{{.Message}}</div>{{end}}`
	content := `{{define "content"}}
{{if .Data.Error}}
<div class="md3-error">{{.Data.Error}}</div>
{{else if not .Data.Searched}}
<div class="memories-empty">
	<div class="memories-empty__text">Enter a User ID to browse memories</div>
</div>
{{else if not .Data.Memories}}
<div class="memories-empty">
	<div class="memories-empty__text">No memories found for this user</div>
</div>
{{else}}
<div class="memories-list">
	{{range .Data.Memories}}
	<div class="memories-card">
		<div class="memories-card__content">{{.Content}}</div>
	</div>
	{{end}}
</div>
<div class="stats-footer">Showing {{.Data.Count}} memories</div>
{{end}}
{{end}}`

	tmpl = template.Must(tmpl.Parse(base))
	tmpl = template.Must(tmpl.Parse(sidebar))
	tmpl = template.Must(tmpl.Parse(toast))
	tmpl = template.Must(tmpl.Parse(content))
	return &TemplateSet{templates: map[string]*template.Template{"memories": tmpl}}
}

func TestStatsErr_GetMemories_DBError_RendersErrorPage(t *testing.T) {
	store := &testMemStore{
		getMemErr: fmt.Errorf("qdrant connection timeout"),
	}
	tmpl := testMemoriesErrorTemplate()
	handler := MemoriesHandler(newTestCfgStore(), store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/memories?userID=user123", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "qdrant connection timeout") {
		t.Errorf("expected error message in response body, got: %s", body)
	}
	if strings.Contains(body, "No memories found") {
		t.Error("should NOT show 'No memories found' for DB errors (misleading)")
	}
	if strings.Contains(body, "memories-card") {
		t.Error("should NOT show memory cards on DB error")
	}
}

// --- Test 4: GetProfile DB error → handler renders error ---

func testProfilesErrorTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(templateFuncs()).Option("missingkey=error")

	base := `{{define "base"}}{{template "sidebar" .}}{{if .Flash}}{{template "toast" .Flash}}{{end}}{{template "content" .}}{{end}}`
	sidebar := `{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}" class="md3-nav-item{{if .Active}} md3-nav-item--active{{end}}">{{.Label}}</a>{{end}}{{end}}`
	toast := `{{define "toast"}}<div class="md3-toast md3-toast--{{.Type}}">{{.Message}}</div>{{end}}`
	content := `{{define "content"}}
{{if .Data.Error}}
<div class="md3-error">{{.Data.Error}}</div>
{{else if not .Data.Searched}}
<div class="profiles-empty">
	<div class="profiles-empty__text">Enter a User ID to view their profile</div>
</div>
{{else if not .Data.Found}}
<div class="profiles-empty">
	<div class="profiles-empty__text">No profile found</div>
</div>
{{else}}
<div class="profile-header">
	<div class="profile-header__name">{{.Data.Profile.DisplayName}}</div>
</div>
{{end}}
{{end}}`

	tmpl = template.Must(tmpl.Parse(base))
	tmpl = template.Must(tmpl.Parse(sidebar))
	tmpl = template.Must(tmpl.Parse(toast))
	tmpl = template.Must(tmpl.Parse(content))
	return &TemplateSet{templates: map[string]*template.Template{"profiles": tmpl}}
}

func TestStatsErr_GetProfile_DBError_RendersError(t *testing.T) {
	store := &testProfileStore{
		getProfileErr: fmt.Errorf("qdrant unavailable"),
	}
	tmpl := testProfilesErrorTemplate()
	handler := ProfilesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/profiles?userID=user123", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "qdrant unavailable") {
		t.Errorf("expected error message in response body, got: %s", body)
	}
	if strings.Contains(body, "No profile found") {
		t.Error("should NOT show 'No profile found' for DB errors (misleading)")
	}
	if strings.Contains(body, "profile-header") {
		t.Error("should NOT show profile data on DB error")
	}
}
