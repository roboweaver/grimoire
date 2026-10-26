package web_test

import (
	"context"
	"net/http"
	"path/filepath"
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

// M15 Phase 4.1 — the RED handler tests for nested page routing.
//
// WHY A SEPARATE HARNESS (not newTestServerSeeded). The shared harness in
// handlers_test.go builds the *web.Server internally and returns only its
// http.Handler, so it cannot call the NEW .WithPages(*content.PageService)
// option that task 4.2 must add to wire a PageService onto the Server. This
// file therefore builds the Server itself — exactly like newTestServerSeeded,
// plus the one new wiring call — over a real content.PageService on SQLite:
//
//	content.NewPageService(repos.PageReader, repos.Posts)
//
// (PageReader and NewPageService landed in tasks 2.2/3.2; WithPages is the
// method 4.2 must add.)
//
// WHAT MAKES THIS RED. The test references web.Server.WithPages, which does NOT
// exist yet, so the package FAILS TO COMPILE — a compile-RED that names the
// exact missing wiring symbol (WithPages), the cleaner RED the task asks for.
// Once 4.2 adds WithPages, the Server.pages field, the resolve KindPage arm,
// page(), the single() page branch, and the page wildcard route in
// contentPatterns, these tests would then fail BEHAVIORALLY until the handler
// wiring is complete (a nested page path 404s / mis-resolves today because
// KindPage, page(), the single() page branch, and the wildcard route are
// absent) — so the RED is both: compile-RED now, behavioral-RED after WithPages
// alone is added without the handler logic.

// The nested-page fixture this file seeds on top of storagetest.SeedFixtures,
// using the same PostWriter.Create(ParentID -> post_parent) pattern the
// storagetest page contract uses (contract.go runPageReaderContract). Leaf
// slugs are deliberately UNIQUE so BySlug(leaf) returns the one intended row
// (BySlug resolves a slug across {post,page} filtered to publish and, with two
// pages sharing a leaf slug, would pick one arbitrarily — a separate concern
// from path resolution, avoided here by keeping leaf slugs unique).
//
//	handbook        publish  parent 0            -> canonical /handbook/
//	  onboarding    publish  parent handbook     -> canonical /handbook/onboarding/
//	orphan-child    publish  parent 999999 (gone)-> orphan-as-root, /orphan-child/
//	cycle-a         publish  parent cycle-b  \ cycle: each points at the other,
//	cycle-b         publish  parent cycle-a  / the walk must terminate, not loop
//
// onboarding is dated 2024-05-17 so that under structDayAndName the OLD dated
// post-shape form /2024/05/17/onboarding/ classifies as KindPost (row 7, the
// structure's own shape) and single() 301s it to the nested page canonical
// (Req 5.2) — the dated-form case only bites when the seeded page's date makes
// that path resolvable through the post-shape row.
const (
	pageHandbookCanonical   = "/handbook/"
	pageOnboardingCanonical = "/handbook/onboarding/"
	pageOrphanCanonical     = "/orphan-child/"
)

// newNestedPageServer builds a *web.Server over SQLite seeded with the shared
// fixtures plus the nested-page tree, wiring a real content.PageService through
// the NEW .WithPages option (the symbol task 4.2 must add). Mirrors
// newTestServerSeeded's construction exactly, plus WithPages.
func newNestedPageServer(t *testing.T, structure string) http.Handler {
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
	if err := seedNestedPagesRepos(ctx, repos, cfg.TablePrefix); err != nil {
		t.Fatalf("seed nested pages: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}

	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", structure, err)
	}

	posts := content.NewPostService(repos.Posts).
		WithCounter(repos.PostCounter).
		WithAuthors(repos.Users)
	pages := content.NewPageService(repos.PageReader, repos.Posts)

	srv := web.NewServer(
		posts,
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithThemeStatic(filepath.Join("..", "..", "themes"), "default").
		WithPermalinks(st).
		// WithPages is the NEW wiring option task 4.2 must add on *web.Server,
		// mirroring WithPermalinks/WithContentPolicy. Referencing it here is the
		// compile-RED that names the missing symbol.
		WithPages(pages)
	return srv.Routes()
}

// seedNestedPagesRepos inserts the nested-page tree using repos the harness
// already holds (so it writes to the same DB handle the server reads). It is the
// in-process twin of seedNestedPages.
func seedNestedPagesRepos(ctx context.Context, repos *storage.Repositories, prefix string) error {
	mk := func(slug, status string, parentID int64, date time.Time) (int64, error) {
		return repos.PostWriter.Create(ctx, domain.Post{
			Author:        1,
			Date:          date,
			Content:       "<p>page body</p>",
			Title:         slug,
			Excerpt:       "ex",
			Status:        status,
			Slug:          slug,
			Type:          "page",
			CommentStatus: "closed",
			ParentID:      parentID,
		})
	}

	d := time.Date(2024, 5, 17, 0, 0, 0, 0, time.UTC)

	handbook, err := mk("handbook", "publish", 0, d)
	if err != nil {
		return err
	}
	if _, err := mk("onboarding", "publish", handbook, d); err != nil {
		return err
	}
	if _, err := mk("orphan-child", "publish", 999999, d); err != nil {
		return err
	}
	cycleA, err := mk("cycle-a", "publish", 0, d)
	if err != nil {
		return err
	}
	cycleB, err := mk("cycle-b", "publish", cycleA, d)
	if err != nil {
		return err
	}
	_, err = repos.DB().ExecContext(ctx,
		"UPDATE "+prefix+"posts SET post_parent = ? WHERE ID = ?", cycleB, cycleA)
	return err
}

// TestPagesNestedStatusCodes covers the handler rows of design.md's Testing
// strategy for M15 nested pages, under structDayAndName (trailing slash, so a
// page canonical carries one). Each case asserts the exact status and, for a
// redirect, the exact Location (an empty wantLocation asserts the header's
// absence, so a 200 that also redirected cannot pass).
func TestPagesNestedStatusCodes(t *testing.T) {
	srv := newNestedPageServer(t, structDayAndName)

	cases := []struct {
		name         string
		path         string
		wantStatus   int
		wantLocation string
	}{
		{
			// Req 3.5, 5.4: the canonical is built from the RESOLVED leaf's own
			// ancestry, a fixed point — a request already at it renders, no
			// redirect-to-self. Reaching a 200 on a 2-segment page path also
			// proves the page WILDCARD route is registered and dispatches through
			// s.resolve -> KindPage -> page() (Req 9.1, 9.2, 9.4).
			name:       "nested page canonical renders",
			path:       pageOnboardingCanonical,
			wantStatus: http.StatusOK,
		},
		{
			// Req 5.1: the flat /{leaf} form is a single segment -> KindPost ->
			// single(), which must detect post.Type=="page" and 301 to the nested
			// canonical via the shared PageService ancestry (the single() page
			// branch task 4.2 adds).
			name:         "flat leaf of a nested page 301s to the nested canonical",
			path:         "/onboarding",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: pageOnboardingCanonical,
		},
		{
			// Req 5.2: the OLD dated post-shape form /2024/05/17/onboarding/
			// classifies as KindPost (row 7, the structure's own shape, the date
			// matching the seeded page) and single()'s page branch 301s it to the
			// nested page canonical — a page no longer takes a dated canonical.
			name:         "old dated form 301s to the nested canonical",
			path:         "/2024/05/17/onboarding/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: pageOnboardingCanonical,
		},
		{
			// Req 6.3 outcome via the walk: a 2+-segment path whose final segment
			// names no page resolves to nothing and 404s (reached through page()'s
			// walk, the KindNone-equivalent outcome).
			name:       "multi-segment path naming no page 404s",
			path:       "/handbook/nope/",
			wantStatus: http.StatusNotFound,
		},
		{
			// Req 10.3 / 2.3: an orphan-parent page still resolves to a servable
			// canonical (its shorter, orphan-as-root ancestry /orphan-child/)
			// rather than erroring. Requested at its canonical, it renders.
			name:       "orphan-parent page renders at its servable canonical",
			path:       pageOrphanCanonical,
			wantStatus: http.StatusOK,
		},
		{
			// The orphan's flat leaf 301s to that same servable canonical — a
			// finite, non-looping redirect (Req 10.3).
			name:         "orphan-parent flat leaf 301s to its servable canonical",
			path:         "/orphan-child",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: pageOrphanCanonical,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, tc.path)
			if rec.Code != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d (body: %.200s)",
					tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tc.wantLocation {
				t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.wantLocation)
			}
		})
	}
}

// TestPagesNestedRedirectPreservesQueryStringRaw covers Req 5.3: a page 301
// appends r.URL.RawQuery RAW (redirectCanonical, handlers.go:137), so a query
// with percent-encoding survives byte-for-byte across the redirect. Dropping or
// re-encoding it would silently alter campaign/pagination parameters on every
// redirected page link.
func TestPagesNestedRedirectPreservesQueryStringRaw(t *testing.T) {
	srv := newNestedPageServer(t, structDayAndName)

	cases := []struct {
		name string
		path string
		want string
	}{
		{
			"flat leaf keeps a percent-encoded query raw",
			"/onboarding?foo=bar%20baz",
			pageOnboardingCanonical + "?foo=bar%20baz",
		},
		{
			"flat leaf keeps several parameters",
			"/onboarding?a=1&b=2",
			pageOnboardingCanonical + "?a=1&b=2",
		},
		{
			"dated form keeps the query across the redirect",
			"/2024/05/17/onboarding/?page=3",
			pageOnboardingCanonical + "?page=3",
		},
		{
			"orphan flat leaf keeps the query",
			"/orphan-child?utm_source=news",
			pageOrphanCanonical + "?utm_source=news",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, tc.path)
			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("GET %s = %d, want 301 (body: %.200s)",
					tc.path, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tc.want {
				t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestPagesNestedCanonicalDoesNotRedirect is the explicit no-loop guard for the
// fixed point (Req 3.5, 5.4): a request already at a page's nested canonical
// must render, never redirect to itself — a loop here would break every nested
// page URL on the site.
func TestPagesNestedCanonicalDoesNotRedirect(t *testing.T) {
	srv := newNestedPageServer(t, structDayAndName)
	for _, canonical := range []string{pageHandbookCanonical, pageOnboardingCanonical, pageOrphanCanonical} {
		t.Run(canonical, func(t *testing.T) {
			rec := get(t, srv, canonical)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (body: %.200s)",
					canonical, rec.Code, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("canonical page path %s redirected to %q; a request already "+
					"at the canonical path must render", canonical, loc)
			}
		})
	}
}

// TestPagesCyclePageStillServable covers the corrupt-graph degradation at the
// handler level (Req 10.3 / 2.4): a page caught in a post_parent cycle must
// still resolve to a servable canonical — a 200 or a FINITE, non-looping 301 —
// rather than 500ing or hanging. The exact canonical a cycle produces is a
// property of the walk's first-repeat termination and is asserted in the content
// unit tests; here the handler-level contract is only that the request
// terminates with a non-error status and, if it redirects, does not point back
// at itself.
func TestPagesCyclePageStillServable(t *testing.T) {
	srv := newNestedPageServer(t, structDayAndName)

	for _, path := range []string{"/cycle-a", "/cycle-b"} {
		t.Run(path, func(t *testing.T) {
			rec := get(t, srv, path)
			switch rec.Code {
			case http.StatusOK:
				// Served directly — acceptable.
			case http.StatusMovedPermanently:
				loc := rec.Header().Get("Location")
				if loc == "" {
					t.Fatalf("GET %s 301 with no Location", path)
				}
				if loc == path {
					t.Fatalf("GET %s 301s to itself (%q) — a cycle must not loop", path, loc)
				}
			default:
				t.Fatalf("GET %s = %d; a cycle page must resolve to a servable "+
					"canonical (200 or a finite 301), not error or loop (body: %.200s)",
					path, rec.Code, rec.Body.String())
			}
		})
	}
}
