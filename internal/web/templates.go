package web

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"time"
)

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

func LoadTemplates(basePath string) (*TemplateSet, error) {
	funcs := templateFuncs()

	base := template.New("").Funcs(funcs)

	dirs := []string{"layouts", "partials"}
	for _, dir := range dirs {
		pattern := filepath.Join(basePath, dir, "*.html")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", pattern, err)
		}
		if len(matches) == 0 {
			continue
		}
		base, err = base.ParseFiles(matches...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", dir, err)
		}
	}

	pagesPattern := filepath.Join(basePath, "pages", "*.html")
	pageFiles, err := filepath.Glob(pagesPattern)
	if err != nil {
		return nil, fmt.Errorf("glob pages: %w", err)
	}

	ts := &TemplateSet{
		templates: make(map[string]*template.Template, len(pageFiles)),
	}

	for _, pageFile := range pageFiles {
		pageName := pageKey(filepath.Base(pageFile))

		pageTmpl, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone for %s: %w", pageFile, err)
		}

		pageTmpl, err = pageTmpl.ParseFiles(pageFile)
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", pageFile, err)
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
