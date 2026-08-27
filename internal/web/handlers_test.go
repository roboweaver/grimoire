package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}

	posts := content.NewPostService(repos.Posts).WithCounter(repos.PostCounter)
	srv := web.NewServer(
		posts,
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithThemeStatic(filepath.Join("..", "..", "themes"), "default")
	return srv.Routes()
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/healthz")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func TestHome(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("home status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("home content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "grimoire test") {
		t.Fatalf("home missing site title; body: %s", rec.Body.String())
	}
}

func TestSinglePost(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/hello-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("single status = %d", rec.Code)
	}
}

func TestPage(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/about")
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownSlug404(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug status = %d", rec.Code)
	}
}

func TestCategory(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/category/news")
	if rec.Code != http.StatusOK {
		t.Fatalf("category status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownCategory404(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/category/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown category status = %d", rec.Code)
	}
}
