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

type testMemStore struct {
	memories   []*memory.Record
	getMemory  *memory.Record
	getMemErr  error
	deleteErr  error
	deletedID  string
}

func (m *testMemStore) Store(ctx context.Context, mem *memory.Record) error { return nil }
func (m *testMemStore) Search(ctx context.Context, userID, query string, opts *memory.SearchOptions) ([]*memory.Record, error) {
	return nil, nil
}
func (m *testMemStore) HybridSearch(ctx context.Context, userID, query string, keywords []string, opts *memory.SearchOptions) ([]*memory.Record, error) {
	return nil, nil
}
func (m *testMemStore) GetMemories(ctx context.Context, userID string, limit int) ([]*memory.Record, error) {
	if m.getMemErr != nil {
		return nil, m.getMemErr
	}
	return m.memories, nil
}
func (m *testMemStore) GetMemory(ctx context.Context, memoryID string) (*memory.Record, error) {
	if m.getMemory != nil {
		return m.getMemory, nil
	}
	return nil, fmt.Errorf("memory %s not found", memoryID)
}
func (m *testMemStore) DeleteMemory(ctx context.Context, memoryID string) error {
	m.deletedID = memoryID
	return m.deleteErr
}
func (m *testMemStore) DeleteUserData(ctx context.Context, userID string) error { return nil }
func (m *testMemStore) GetStats(ctx context.Context) (*memory.GlobalStats, error) {
	return &memory.GlobalStats{}, nil
}

func testMemoriesTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(templateFuncs()).Option("missingkey=error")

	base := `{{define "base"}}{{template "sidebar" .}}{{if .Flash}}{{template "toast" .Flash}}{{end}}{{template "content" .}}{{end}}`
	sidebar := `{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}" class="md3-nav-item{{if .Active}} md3-nav-item--active{{end}}">{{.Label}}</a>{{end}}{{end}}`
	toast := `{{define "toast"}}<div class="md3-toast md3-toast--{{.Type}}">{{.Message}}</div>{{end}}`
	content := `{{define "content"}}
{{if not .Data.Searched}}
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
		<div class="memories-card__main">
			<div class="memories-card__header">
				<span>{{.MemoryType}}</span>
				<span>{{.ID}}</span>
			</div>
			<div class="memories-card__content">{{.Content}}</div>
			<div class="memories-card__meta">
				<span>{{.CreatedAt}}</span>
				<span>{{printf "%.0f" (multiply .Confidence 100)}}%</span>
				{{range .Keywords}}<span class="memories-card__chip">{{.}}</span>{{end}}
			</div>
		</div>
		<form method="POST" action="/memories/delete">
			<input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
			<input type="hidden" name="userID" value="{{$.Data.UserID}}">
			<input type="hidden" name="memoryID" value="{{.ID}}">
			<button type="submit">Delete</button>
		</form>
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

func TestMemoriesHandler_GET_NoUserID(t *testing.T) {
	store := &testMemStore{}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/memories", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Enter a User ID to browse memories") {
		t.Error("expected prompt to browse memories")
	}
	if strings.Contains(body, "memories-list") {
		t.Error("expected no memory list when no userID")
	}
}

func TestMemoriesHandler_GET_WithUserID_HasMemories(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	store := &testMemStore{
		memories: []*memory.Record{
			{
				ID:         "mem-001",
				UserID:     "user123",
				MemoryType: memory.TypeFact,
				Content:    "User likes pizza with pineapple",
				Keywords:   []string{"pizza", "pineapple", "food"},
				Confidence: 0.85,
				CreatedAt:  now,
			},
			{
				ID:         "mem-002",
				UserID:     "user123",
				MemoryType: memory.TypeSummary,
				Content:    "Discussed favorite movies",
				Keywords:   []string{"movies"},
				Confidence: 0.60,
				CreatedAt:  now.Add(-1 * time.Hour),
			},
		},
	}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/memories?userID=user123", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "mem-001") {
		t.Error("expected memory mem-001 in response")
	}
	if !strings.Contains(body, "mem-002") {
		t.Error("expected memory mem-002 in response")
	}
	if !strings.Contains(body, "fact") {
		t.Error("expected memory type 'fact'")
	}
	if !strings.Contains(body, "summary") {
		t.Error("expected memory type 'summary'")
	}
	if !strings.Contains(body, "pizza") {
		t.Error("expected keyword 'pizza'")
	}
	if !strings.Contains(body, "85") {
		t.Error("expected confidence 85%")
	}
	if !strings.Contains(body, "Showing 2 memories") {
		t.Errorf("expected 'Showing 2 memories', got: %s", body)
	}
}

func TestMemoriesHandler_GET_WithUserID_NoMemories(t *testing.T) {
	store := &testMemStore{
		memories: []*memory.Record{},
	}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/memories?userID=user_empty", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "No memories found for this user") {
		t.Errorf("expected 'No memories found', got: %s", body)
	}
	if strings.Contains(body, "memories-card") {
		t.Error("expected no memory cards for empty results")
	}
}

func TestMemoriesHandler_GET_InvalidUserID(t *testing.T) {
	store := &testMemStore{}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	tests := []struct {
		name   string
		userID string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"tabs", "\t\t"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/memories?userID="+url.QueryEscape(tc.userID), nil)
			req = requestWithCSRF(req)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", rec.Code)
			}

			body := rec.Body.String()
			if !strings.Contains(body, "Enter a User ID to browse memories") {
				t.Errorf("expected prompt, got: %s", body)
			}
		})
	}
}

func TestMemoriesHandler_Delete_ValidOwnership(t *testing.T) {
	now := time.Now()
	store := &testMemStore{
		getMemory: &memory.Record{
			ID:         "mem-del-001",
			UserID:     "owner123",
			MemoryType: memory.TypeFact,
			Content:    "test content",
			CreatedAt:  now,
		},
		memories: []*memory.Record{},
	}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	form := url.Values{
		"userID":   {"owner123"},
		"memoryID": {"mem-del-001"},
	}
	req := httptest.NewRequest(http.MethodPost, "/memories/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	loc := rec.Header().Get("Location")
	if loc != "/memories?userID=owner123" {
		t.Errorf("expected redirect to /memories?userID=owner123, got %q", loc)
	}

	if store.deletedID != "mem-del-001" {
		t.Errorf("expected DeleteMemory called with mem-del-001, got %q", store.deletedID)
	}
}

func TestMemoriesHandler_Delete_WrongOwnership(t *testing.T) {
	now := time.Now()
	store := &testMemStore{
		getMemory: &memory.Record{
			ID:         "mem-wrong-001",
			UserID:     "realOwner456",
			MemoryType: memory.TypeFact,
			Content:    "test content",
			CreatedAt:  now,
		},
	}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	form := url.Values{
		"userID":   {"attacker789"},
		"memoryID": {"mem-wrong-001"},
	}
	req := httptest.NewRequest(http.MethodPost, "/memories/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	if store.deletedID != "" {
		t.Errorf("expected DeleteMemory NOT to be called, but got deletedID=%q", store.deletedID)
	}
}

func TestMemoriesHandler_Delete_MissingParams(t *testing.T) {
	store := &testMemStore{}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	tests := []struct {
		name   string
		userID string
		memID  string
	}{
		{"both missing", "", ""},
		{"userID missing", "", "mem-001"},
		{"memoryID missing", "user123", ""},
		{"both whitespace", "  ", "  "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{
				"userID":   {tc.userID},
				"memoryID": {tc.memID},
			}
			req := httptest.NewRequest(http.MethodPost, "/memories/delete", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = requestWithCSRF(req)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected status 400, got %d. Body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMemoriesHandler_NavItemsPresent(t *testing.T) {
	store := &testMemStore{}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/memories", nil)
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

func TestMemoriesHandler_MethodNotAllowed(t *testing.T) {
	store := &testMemStore{}
	tmpl := testMemoriesTemplate()
	handler := MemoriesHandler(store, tmpl)

	req := httptest.NewRequest(http.MethodPut, "/memories", nil)
	req = requestWithCSRF(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for unknown method, got %d", rec.Code)
	}
}
