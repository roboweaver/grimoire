package web

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/render"
)

// erroringTheme writes a minimal theme whose index template references a
// missing method, so ExecuteTemplate fails partway through rendering.
func erroringTheme(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tdir := filepath.Join(dir, "boom", "templates")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(tdir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("base.tmpl", `{{define "base"}}<html>{{template "content" .}}</html>{{end}}`)
	// .Explode is not a field/method on struct{}{}, so execution errors mid-write.
	write("index.tmpl", `{{define "content"}}<p>{{.Explode}}</p>{{end}}`)
	return dir
}

func TestRenderHTMLNoPartialWriteOnError(t *testing.T) {
	dir := erroringTheme(t)
	eng, err := render.Load(dir, "boom")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	s := &Server{render: eng}

	rec := httptest.NewRecorder()
	if err := s.renderHTML(rec, "index", struct{}{}); err == nil {
		t.Fatal("expected render error, got nil")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty body on render error, got %d bytes: %q", rec.Body.Len(), rec.Body.String())
	}
}
