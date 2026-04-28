package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/memory"
)

// mockMemoryStore implements MemoryStore and memoryCounter for testing.
type mockMemoryStore struct {
	countMemories int64
	countProfiles int64
	countErr      error
	getStatsErr   error
}

func (m *mockMemoryStore) CountMemories(ctx context.Context) (int64, error) {
	return m.countMemories, m.countErr
}

func (m *mockMemoryStore) CountProfiles(ctx context.Context) (int64, error) {
	return m.countProfiles, m.countErr
}

func (m *mockMemoryStore) Store(ctx context.Context, mem *memory.Record) error { return nil }
func (m *mockMemoryStore) Search(ctx context.Context, userID, query string, opts *memory.SearchOptions) ([]*memory.Record, error) {
	return nil, nil
}
func (m *mockMemoryStore) HybridSearch(ctx context.Context, userID, query string, keywords []string, opts *memory.SearchOptions) ([]*memory.Record, error) {
	return nil, nil
}
func (m *mockMemoryStore) GetMemories(ctx context.Context, userID string, limit int) ([]*memory.Record, error) {
	return nil, nil
}
func (m *mockMemoryStore) GetMemory(ctx context.Context, memoryID string) (*memory.Record, error) {
	return nil, nil
}
func (m *mockMemoryStore) DeleteMemory(ctx context.Context, memoryID string) error { return nil }
func (m *mockMemoryStore) DeleteUserData(ctx context.Context, userID string) error { return nil }
func (m *mockMemoryStore) GetStats(ctx context.Context) (*memory.GlobalStats, error) {
	if m.getStatsErr != nil {
		return nil, m.getStatsErr
	}
	return &memory.GlobalStats{
		TotalMemories: m.countMemories,
		TotalUsers:    m.countProfiles,
	}, nil
}

// mockProfileStore implements ProfileStore and profileCounter for testing.
type mockProfileStore struct {
	countProfiles int64
	countErr      error
}

func (m *mockProfileStore) CountProfiles(ctx context.Context) (int64, error) {
	return m.countProfiles, m.countErr
}

func (m *mockProfileStore) GetProfile(ctx context.Context, userID string) (*memory.Profile, error) {
	return nil, nil
}
func (m *mockProfileStore) UpdateProfile(ctx context.Context, p *memory.Profile) error { return nil }
func (m *mockProfileStore) GetUserStats(ctx context.Context, userID string) (*memory.UserStats, error) {
	return nil, nil
}

func testDashboardTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(template.FuncMap{
		"formatDuration": formatDuration,
	}).Option("missingkey=error")

	baseContent := `{{define "base"}}{{template "sidebar" .}}<main>{{template "content" .}}</main>{{end}}
{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}" class="md3-nav-item{{if .Active}} md3-nav-item--active{{end}}">{{.Label}}</a>{{end}}{{end}}
{{define "toast"}}{{end}}`
	tmpl = template.Must(tmpl.Parse(baseContent))

	content := `{{define "content"}}
<div class="md3-card-grid">
	<div class="md3-stat-card"><p class="md3-stat-label">Total Memories</p><p class="md3-stat-value">{{.Data.TotalMemories}}</p></div>
	<div class="md3-stat-card"><p class="md3-stat-label">Total Users</p><p class="md3-stat-value">{{.Data.TotalUsers}}</p></div>
	<div class="md3-stat-card"><p class="md3-stat-label">Total Messages</p><p class="md3-stat-value">{{.Data.TotalMessages}}</p></div>
	<div class="md3-stat-card"><p class="md3-stat-label">Uptime</p><p class="md3-stat-value">{{formatDuration .Data.Uptime}}</p></div>
</div>
{{end}}`
	tmpl = template.Must(tmpl.Parse(content))
	return &TemplateSet{templates: map[string]*template.Template{"dashboard": tmpl}}
}

func TestDashboardHandler_Returns200(t *testing.T) {
	memStore := &mockMemoryStore{countMemories: 42, countProfiles: 7}
	profStore := &mockProfileStore{countProfiles: 7}
	stats := NewStatsProvider(memStore, profStore)
	tmpl := testDashboardTemplate()
	handler := DashboardHandler(stats, time.Now(), tmpl)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestDashboardHandler_ShowsStatsHTML(t *testing.T) {
	memStore := &mockMemoryStore{countMemories: 42, countProfiles: 7}
	profStore := &mockProfileStore{countProfiles: 7}
	stats := NewStatsProvider(memStore, profStore)
	tmpl := testDashboardTemplate()
	handler := DashboardHandler(stats, time.Now(), tmpl)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "Total Memories") {
		t.Error("expected response to contain 'Total Memories'")
	}
	if !strings.Contains(body, "42") {
		t.Error("expected response to contain memory count '42'")
	}
	if !strings.Contains(body, "7") {
		t.Error("expected response to contain user count '7'")
	}
}

func TestDashboardHandler_NoStatsAvailable(t *testing.T) {
	memStore := &mockMemoryStore{countMemories: 0, countProfiles: 0, countErr: fmt.Errorf("qdrant unavailable")}
	profStore := &mockProfileStore{countProfiles: 0, countErr: fmt.Errorf("qdrant unavailable")}
	stats := NewStatsProvider(memStore, profStore)
	tmpl := testDashboardTemplate()
	handler := DashboardHandler(stats, time.Now(), tmpl)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 when stats unavailable, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Total Memories") {
		t.Error("expected page to render with 'Total Memories' even when stats unavailable")
	}
	if !strings.Contains(body, "0") {
		t.Error("expected zero values when stats unavailable")
	}
}

func TestDashboardHandler_MethodNotAllowed(t *testing.T) {
	memStore := &mockMemoryStore{}
	profStore := &mockProfileStore{}
	stats := NewStatsProvider(memStore, profStore)
	tmpl := testDashboardTemplate()
	handler := DashboardHandler(stats, time.Now(), tmpl)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestDashboardHandler_NavItemsPresent(t *testing.T) {
	memStore := &mockMemoryStore{countMemories: 10, countProfiles: 3}
	profStore := &mockProfileStore{countProfiles: 3}
	stats := NewStatsProvider(memStore, profStore)
	tmpl := testDashboardTemplate()
	handler := DashboardHandler(stats, time.Now(), tmpl)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
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

func TestDashboardHandler_UptimePresent(t *testing.T) {
	memStore := &mockMemoryStore{}
	profStore := &mockProfileStore{}
	stats := NewStatsProvider(memStore, profStore)
	startTime := time.Now().Add(-2 * time.Hour)
	tmpl := testDashboardTemplate()
	handler := DashboardHandler(stats, startTime, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "2h") && !strings.Contains(body, "1h") {
		t.Errorf("expected uptime to be around 2h, got body: %s", body)
	}
}
