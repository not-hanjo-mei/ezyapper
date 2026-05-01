package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"time"
)

//go:embed templates/layouts/*.html templates/partials/*.html templates/pages/*.html
var templateFS embed.FS

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDuration": formatDuration,
		"dict":           dictFunc,
		"multiply":       multiplyFunc,
		"list":           listFunc,
	}
}

type TemplateSet struct {
	templates map[string]*template.Template
	loginTmpl *template.Template
}

func (ts *TemplateSet) Get(page string) *template.Template {
	return ts.templates[page]
}

func (ts *TemplateSet) Login() *template.Template {
	return ts.loginTmpl
}

func LoadTemplates() (*TemplateSet, error) {
	funcs := templateFuncs()

	base, err := template.New("").Funcs(funcs).ParseFS(templateFS,
		"templates/layouts/*.html",
		"templates/partials/*.html",
	)
	if err != nil {
		return nil, fmt.Errorf("parse layouts/partials: %w", err)
	}

	entries, err := templateFS.ReadDir("templates/pages")
	if err != nil {
		return nil, fmt.Errorf("read pages: %w", err)
	}

	ts := &TemplateSet{
		templates: make(map[string]*template.Template, len(entries)),
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".html" {
			continue
		}
		pageName := pageKey(entry.Name())

		pageTmpl, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone for %s: %w", entry.Name(), err)
		}

		pageTmpl, err = pageTmpl.ParseFS(templateFS, "templates/pages/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", entry.Name(), err)
		}

		if pageName == "login" {
			ts.loginTmpl = pageTmpl
		} else {
			ts.templates[pageName] = pageTmpl
		}
	}

	if ts.loginTmpl == nil {
		return nil, fmt.Errorf("login template not found")
	}

	return ts, nil
}

func pageKey(filename string) string {
	return filename[:len(filename)-len(".html")]
}

func RenderPage(w http.ResponseWriter, ts *TemplateSet, page string, data *PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := ts.Get(page)
	if tmpl == nil {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func formatDuration(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	d := time.Duration(seconds) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 24 {
		days := h / 24
		h = h % 24
		return fmt.Sprintf("%dd %dh", days, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func dictFunc(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict requires even number of arguments")
	}
	m := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		m[key] = values[i+1]
	}
	return m, nil
}

func multiplyFunc(a, b float64) float64 {
	return a * b
}

func listFunc(values ...any) []any {
	return values
}

// PageData holds common data passed to every page template.
type PageData struct {
	Title     string
	ActiveNav string
	CSRFToken string
	Flash     *FlashMessage
	Data      any
	NavItems  []NavItem
	BuildTag  string
}

// FlashMessage represents a one-time notification displayed on a page.
type FlashMessage struct {
	Type    string // "success", "error", "info", "warning"
	Message string
}

// NavItem represents a single entry in the navigation bar.
type NavItem struct {
	Label  string
	Href   string
	Icon   string
	Active bool
}
