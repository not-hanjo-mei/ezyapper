package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLogsTemplate() *TemplateSet {
	tmpl := template.New("").Funcs(templateFuncs()).Option("missingkey=error")

	base := `{{define "base"}}{{template "sidebar" .}}{{template "content" .}}{{end}}`
	sidebar := `{{define "sidebar"}}{{range .NavItems}}<a href="{{.Href}}">{{.Label}}</a>{{end}}{{end}}`
	toast := `{{define "toast"}}{{end}}`
	content := `{{define "content"}}<pre>{{.Data.Content}}</pre><span>{{.Data.Stats}}</span><span>{{.Data.Lines}}</span>{{end}}`

	tmpl = template.Must(tmpl.Parse(base))
	tmpl = template.Must(tmpl.Parse(sidebar))
	tmpl = template.Must(tmpl.Parse(toast))
	tmpl = template.Must(tmpl.Parse(content))
	return &TemplateSet{templates: map[string]*template.Template{"logs": tmpl}}
}

func writeTestLogFile(t *testing.T, dir string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, "test.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		_, err := f.WriteString(line + "\n")
		if err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestLogsHandler_GET_ReturnsLogs(t *testing.T) {
	dir := t.TempDir()
	logLines := []string{"line1", "line2", "line3", "line4", "line5"}
	logPath := writeTestLogFile(t, dir, logLines)

	tmpl := testLogsTemplate()
	handler := LogsHandler(logPath, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/logs?lines=3", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "line4") {
		t.Error("expected response to contain 'line4'")
	}
	if !strings.Contains(body, "line5") {
		t.Error("expected response to contain 'line5'")
	}
	if strings.Contains(body, "line1") {
		t.Error("expected response NOT to contain 'line1' (only last 3 lines)")
	}
	if strings.Contains(body, "line2") {
		t.Error("expected response NOT to contain 'line2' (only last 3 lines)")
	}
}

func TestLogsHandler_GET_DefaultLines(t *testing.T) {
	dir := t.TempDir()
	logLines := make([]string, 300)
	for i := range logLines {
		logLines[i] = "line" + strings.Repeat("0", 3-len(sprintLen(i+1))) + sprintLen(i+1)
	}
	logPath := writeTestLogFile(t, dir, logLines)

	tmpl := testLogsTemplate()
	handler := LogsHandler(logPath, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "200") {
		t.Error("expected response to contain default 200 lines")
	}
}

func sprintLen(n int) string {
	if n < 10 {
		return "00" + strings.Repeat("", 0) + itoa(n)
	}
	if n < 100 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestLogsHandler_GET_InvalidLines(t *testing.T) {
	dir := t.TempDir()
	logLines := []string{"a", "b", "c", "d", "e"}
	logPath := writeTestLogFile(t, dir, logLines)

	tmpl := testLogsTemplate()
	handler := LogsHandler(logPath, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/logs?lines=abc", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "a") {
		t.Error("expected response to contain all lines with invalid param")
	}
	if !strings.Contains(body, "e") {
		t.Error("expected response to contain all lines with invalid param")
	}
}

func TestLogsHandler_GET_LinesExceedsMax(t *testing.T) {
	dir := t.TempDir()
	logLines := make([]string, 6000)
	for i := range logLines {
		logLines[i] = "line" + itoa(i+1)
	}
	logPath := writeTestLogFile(t, dir, logLines)

	tmpl := testLogsTemplate()
	handler := LogsHandler(logPath, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/logs?lines=10000", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "5000") {
		t.Errorf("expected lines to be capped at 5000, got body: %s", body)
	}
}

func TestLogsHandler_FileNotFound(t *testing.T) {
	tmpl := testLogsTemplate()
	handler := LogsHandler("/nonexistent/path/to/logs.log", tmpl)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 with error message, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Log file not found") {
		t.Error("expected page to show 'Log file not found' message")
	}
}

func TestLogsHandler_GET_NavItemsPresent(t *testing.T) {
	dir := t.TempDir()
	logLines := []string{"test line"}
	logPath := writeTestLogFile(t, dir, logLines)

	tmpl := testLogsTemplate()
	handler := LogsHandler(logPath, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
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

func TestLogsHandler_GET_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "empty.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	tmpl := testLogsTemplate()
	handler := LogsHandler(logPath, tmpl)

	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 for empty file, got %d", rec.Code)
	}
}

func TestLogsHandler_GET_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	logLines := []string{"test"}
	logPath := writeTestLogFile(t, dir, logLines)

	tmpl := testLogsTemplate()
	handler := LogsHandler(logPath, tmpl)

	req := httptest.NewRequest(http.MethodPost, "/logs", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}
