package web_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// Regression coverage for PageService.Resolve (plans/16-page-resolve-fix):
// the leaf is confirmed by ID and type "page", and a moved-page redirect never
// discloses an unpublished page. Same-slug pages under different parents must
// each serve their own body.

// newResolveFixServer builds a Server over SQLite with this fixture:
//
//	a/x             publish  body "BODY-A-X"
//	b/x             publish  body "BODY-B-X"
//	handbook/onboarding publish
//	handbook/secret draft
//	x (post)        publish  same slug as the nested pages
func newResolveFixServer(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	cfg := config.DatabaseConfig{
		Vendor: "sqlite", DSN: filepath.Join(t.TempDir(), "grimoire.db"), TablePrefix: "wp_",
	}
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

	d := time.Date(2024, 5, 17, 0, 0, 0, 0, time.UTC)
	mk := func(typ, slug, status, body string, parent int64) int64 {
		id, err := repos.PostWriter.Create(ctx, domain.Post{
			Author: 1, Date: d, Content: "<p>" + body + "</p>", Title: slug,
			Excerpt: "ex", Status: status, Slug: slug, Type: typ,
			CommentStatus: "closed", ParentID: parent,
		})
		if err != nil {
			t.Fatalf("create %s %s: %v", typ, slug, err)
		}
		return id
	}
	// The post is created first so a slug-based confirmation would find it.
	mk("post", "x", "publish", "BODY-POST-X", 0)
	a := mk("page", "a", "publish", "BODY-A", 0)
	b := mk("page", "b", "publish", "BODY-B", 0)
	mk("page", "x", "publish", "BODY-A-X", a)
	mk("page", "x", "publish", "BODY-B-X", b)
	h := mk("page", "handbook", "publish", "BODY-HANDBOOK", 0)
	mk("page", "onboarding", "publish", "BODY-ONBOARDING", h)
	mk("page", "secret", "draft", "BODY-SECRET", h)

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse(structDayAndName, "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	posts := content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users)
	return web.NewServer(
		posts,
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithThemeStatic(filepath.Join("..", "..", "themes"), "default").
		WithPermalinks(st).
		WithPages(content.NewPageService(repos.PageReader, repos.Posts)).
		Routes()
}

func TestPagesResolveSameSlugServesOwnBody(t *testing.T) {
	srv := newResolveFixServer(t)
	cases := []struct{ path, want, other string }{
		{"/a/x/", "BODY-A-X", "BODY-B-X"},
		{"/b/x/", "BODY-B-X", "BODY-A-X"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := get(t, srv, tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (body: %.200s)", tc.path, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("GET %s body lacks %q", tc.path, tc.want)
			}
			if strings.Contains(body, tc.other) || strings.Contains(body, "BODY-POST-X") {
				t.Errorf("GET %s served another row's body (want only %q)", tc.path, tc.want)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("GET %s unexpected Location %q", tc.path, loc)
			}
		})
	}
}

func TestPagesResolveDraftIsNotDisclosed(t *testing.T) {
	srv := newResolveFixServer(t)
	for _, path := range []string{"/secret/", "/secret", "/wrong/secret/", "/handbook/secret/"} {
		t.Run(path, func(t *testing.T) {
			rec := get(t, srv, path)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404", path, rec.Code)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("GET %s disclosed Location %q", path, loc)
			}
			if strings.Contains(rec.Body.String(), "BODY-SECRET") {
				t.Errorf("GET %s leaked the draft body", path)
			}
		})
	}
}

func TestPagesResolvePublishedFlatURLStillRedirects(t *testing.T) {
	srv := newResolveFixServer(t)
	for _, path := range []string{"/onboarding/", "/wrong/onboarding/"} {
		t.Run(path, func(t *testing.T) {
			rec := get(t, srv, path)
			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("GET %s = %d, want 301", path, rec.Code)
			}
			if got := rec.Header().Get("Location"); got != "/handbook/onboarding/" {
				t.Errorf("GET %s Location = %q, want /handbook/onboarding/", path, got)
			}
		})
	}
}
