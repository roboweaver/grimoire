package render

import (
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// baseTemplate and indexTemplate are required in every theme.
const (
	baseTemplate  = "base.tmpl"
	indexTemplate = "index.tmpl"
)

// contentTemplates are the content-level templates that may exist in a theme.
// Each is parsed together with base.tmpl when present.
var contentTemplates = []string{"index", "single", "page", "category", "archive", "login", "partials/comments", "partials/nav-menu"}

// hierarchy maps a requested render kind to the ordered list of content
// templates to try; the first one loaded in the theme wins (a WordPress-style
// template hierarchy subset).
var hierarchy = map[string][]string{
	"index":    {"index"},
	"single":   {"single", "index"},
	"page":     {"page", "single", "index"},
	"category": {"category", "archive", "index"},
	"archive":  {"archive", "index"},
	"login":    {"login"},
}

// Engine holds the compiled templates for one theme, keyed by content-template
// name. Each entry has "base" and "content" defined so ExecuteTemplate("base")
// composes the page.
type Engine struct {
	templates map[string]*template.Template
}

// Load compiles the theme at themesDir/theme/templates. It requires base.tmpl
// and index.tmpl and returns an error naming any missing required file.
func Load(themesDir, theme string) (*Engine, error) {
	dir := filepath.Join(themesDir, theme, "templates")

	basePath := filepath.Join(dir, baseTemplate)
	if !fileExists(basePath) {
		return nil, fmt.Errorf("render: theme %q missing required %s", theme, baseTemplate)
	}
	if !fileExists(filepath.Join(dir, indexTemplate)) {
		return nil, fmt.Errorf("render: theme %q missing required %s", theme, indexTemplate)
	}

	e := &Engine{templates: make(map[string]*template.Template)}
	partials := []string{}
	for _, partial := range []string{"partials/comments.tmpl", "partials/nav-menu.tmpl"} {
		if fileExists(filepath.Join(dir, partial)) {
			partials = append(partials, filepath.Join(dir, partial))
		}
	}
	for _, name := range contentTemplates {
		contentPath := filepath.Join(dir, name+".tmpl")
		if !fileExists(contentPath) || strings.HasPrefix(name, "partials/") {
			continue
		}
		files := append([]string{basePath, contentPath}, partials...)
		tmpl, err := template.New(baseTemplate).ParseFiles(files...)
		if err != nil {
			return nil, fmt.Errorf("render: parse %s: %w", name, err)
		}
		e.templates[name] = tmpl
	}
	return e, nil
}

// Render resolves kind through the template hierarchy to the first loaded
// template and executes its "base" definition with data.
func (e *Engine) Render(w io.Writer, kind string, data any) error {
	candidates, ok := hierarchy[kind]
	if !ok {
		candidates = []string{"index"}
	}
	for _, name := range candidates {
		if tmpl, ok := e.templates[name]; ok {
			return tmpl.ExecuteTemplate(w, "base", data)
		}
	}
	return fmt.Errorf("render: no template available for kind %q", kind)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
