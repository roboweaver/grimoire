package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTheme creates a minimal theme dir under a temp root and returns the
// themesDir + theme name.
func writeTheme(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "mini", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, "mini"
}

const miniBase = `{{define "base"}}<!doctype html><title>{{.SiteTitle}}</title><main>{{block "content" .}}{{end}}</main>{{end}}`
const miniIndex = `{{define "content"}}<h1>{{.SiteTitle}}</h1>{{end}}`

func TestLoadMissingBase(t *testing.T) {
	root, theme := writeTheme(t, map[string]string{"index.tmpl": miniIndex})
	_, err := Load(root, theme)
	if err == nil || !strings.Contains(err.Error(), "base.tmpl") {
		t.Fatalf("want error naming base.tmpl, got %v", err)
	}
}

func TestLoadMissingIndex(t *testing.T) {
	root, theme := writeTheme(t, map[string]string{"base.tmpl": miniBase})
	_, err := Load(root, theme)
	if err == nil || !strings.Contains(err.Error(), "index.tmpl") {
		t.Fatalf("want error naming index.tmpl, got %v", err)
	}
}

func TestRenderIndex(t *testing.T) {
	root, theme := writeTheme(t, map[string]string{"base.tmpl": miniBase, "index.tmpl": miniIndex})
	e, err := Load(root, theme)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "index", IndexData{SiteTitle: "grimoire"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "<h1>grimoire</h1>") {
		t.Fatalf("output missing site title: %q", buf.String())
	}
}

func TestRenderSingleFallsBackToIndex(t *testing.T) {
	// Theme has only base + index; requesting "single" must fall back to index.
	root, theme := writeTheme(t, map[string]string{"base.tmpl": miniBase, "index.tmpl": miniIndex})
	e, err := Load(root, theme)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "single", IndexData{SiteTitle: "fallback"}); err != nil {
		t.Fatalf("Render single fallback: %v", err)
	}
	if !strings.Contains(buf.String(), "fallback") {
		t.Fatalf("fallback render missing content: %q", buf.String())
	}
}

func TestLoadIncludesTemplatePartials(t *testing.T) {
	root, theme := writeTheme(t, map[string]string{
		"base.tmpl":               miniBase,
		"index.tmpl":              `{{define "content"}}{{template "post-card" .}}{{end}}`,
		"partials/post-card.tmpl": `{{define "post-card"}}<article>{{.SiteTitle}}</article>{{end}}`,
	})
	e, err := Load(root, theme)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "index", IndexData{SiteTitle: "partial"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "<article>partial</article>") {
		t.Fatalf("output missing partial content: %q", buf.String())
	}
}
