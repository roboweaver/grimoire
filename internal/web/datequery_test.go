package web_test

import (
	"context"
	"html"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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

// M16 task 7.1 -- the RED handler tests for the `?m=` date query on the home
// route (requirements 10 and 11, design "PR 2: the date query").
//
// Fixture: the pagination fixture of pagination_archives_test.go. March 2025
// holds the 25 PagPost posts (three pages); January 2024 holds Hello One..Three
// and a post slugged "page" is not seeded; May 2024 holds the archive fixtures
// (one page). The home page 1 shows the newest ten, so a date archive of an
// older month is told apart from the home page by its posts and its heading.
//
// Written RED: every valid `?m=` request renders the ordinary home page today,
// because the `/` handler ignores unknown query arguments. The invalid-value
// cases pass already and must keep passing (they pin the "ignore" rule).

const (
	dqMarchPosts = "PagPost-24" // newest of March 2025; also on home page 1
	dqJanPost    = "Hello One"  // January 2024 only
	dqMayPost    = "Dual Post"  // May 2024 only
)

// countingPosts counts the date-archive reads (the two queries DateArchive
// makes) made through the wrapped repository.
type countingPosts struct {
	domain.PostRepository
	reads *atomic.Int32
}

func (c countingPosts) PublishedArchive(ctx context.Context, f domain.ArchiveFilter, limit, offset int) ([]domain.Post, error) {
	c.reads.Add(1)
	return c.PostRepository.PublishedArchive(ctx, f, limit, offset)
}

func (c countingPosts) CountPublishedArchive(ctx context.Context, f domain.ArchiveFilter) (int, error) {
	c.reads.Add(1)
	return c.PostRepository.CountPublishedArchive(ctx, f)
}

// newDateQueryServer builds the live router over the pagination fixture for
// structure (a permalink_structure value, "" for Flat) and returns the number of
// date-archive reads made so far through reads.
func newDateQueryServer(t *testing.T, structure string) (http.Handler, *atomic.Int32) {
	t.Helper()
	ctx := context.Background()
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: filepath.Join(t.TempDir(), "grimoire.db"), TablePrefix: "wp_"}
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
	if err := seedPaginated(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("seedPaginated: %v", err)
	}
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", structure, err)
	}
	reads := new(atomic.Int32)
	srv := web.NewServer(
		content.NewPostService(countingPosts{repos.Posts, reads}).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithThemeStatic(filepath.Join("..", "..", "themes"), "default").WithPermalinks(st)
	return srv.Routes(), reads
}

// hasHref reports whether body links to url, accepting the HTML-escaped form
// html/template writes for an attribute holding "&".
func hasHref(body, url string) bool {
	return strings.Contains(body, `href="`+url+`"`) ||
		strings.Contains(body, `href="`+html.EscapeString(url)+`"`)
}

var dqStructures = []struct {
	name, raw string
	flat      bool
}{
	{"postname slash", "/%postname%/", false},
	{"postname no slash", "/%postname%", false},
	{"flat", "", true},
}

// Req 10.1: valid 4, 6 and 8 digit values serve the date archive in place.
func TestDateQueryServesArchive(t *testing.T) {
	for _, sc := range dqStructures {
		t.Run(sc.name, func(t *testing.T) {
			srv, _ := newDateQueryServer(t, sc.raw)
			cases := []struct {
				m, heading, has string
				hasNot          []string
			}{
				{"2024", "2024", dqMayPost, []string{dqMarchPosts}},
				{"202401", "January 2024", dqJanPost, []string{dqMarchPosts, dqMayPost}},
				{"202405", "May 2024", dqMayPost, []string{dqMarchPosts, dqJanPost}},
				{"20240101", "1 January 2024", dqJanPost, []string{dqMarchPosts, "Hello Two"}},
				{"20250315", "15 March 2025", dqMarchPosts, nil},
			}
			for _, tc := range cases {
				t.Run(tc.m, func(t *testing.T) {
					rec := get(t, srv, "/?m="+tc.m)
					if rec.Code != http.StatusOK {
						t.Fatalf("GET /?m=%s = %d, want 200", tc.m, rec.Code)
					}
					body := rec.Body.String()
					if !strings.Contains(body, tc.heading) {
						t.Errorf("GET /?m=%s body lacks the date heading %q", tc.m, tc.heading)
					}
					if !strings.Contains(body, tc.has) {
						t.Errorf("GET /?m=%s body lacks %s", tc.m, tc.has)
					}
					for _, no := range tc.hasNot {
						if strings.Contains(body, no) {
							t.Errorf("GET /?m=%s body holds %s from outside the range", tc.m, no)
						}
					}
				})
			}
		})
	}
}

// Req 11: structured response has an always-set canonical Link and path-form
// pagination links; the Flat response has no Link and `?m=` links.
func TestDateQueryLinksAndPagination(t *testing.T) {
	t.Run("structured", func(t *testing.T) {
		srv, _ := newDateQueryServer(t, "/%postname%/")
		rec := get(t, srv, "/?m=202503")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /?m=202503 = %d, want 200", rec.Code)
		}
		if got := rec.Header().Values("Link"); len(got) != 1 || got[0] != canonicalLink("/2025/03/") {
			t.Errorf("Link = %q, want %q", got, canonicalLink("/2025/03/"))
		}
		if body := rec.Body.String(); !hasHref(body, "/2025/03/page/2/") || strings.Contains(body, "?page=") {
			t.Errorf("page 1 Next is not the path form /2025/03/page/2/ (or renders ?page=)")
		}

		for _, q := range []string{"?m=202503&page=2", "?m=202503&paged=2"} {
			rec := get(t, srv, "/"+q)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /%s = %d, want 200", q, rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, pagOnP2) || strings.Contains(body, pagNewest) {
				t.Errorf("GET /%s is not page 2 of March 2025", q)
			}
			if got := rec.Header().Values("Link"); len(got) != 1 || got[0] != canonicalLink("/2025/03/page/2/") {
				t.Errorf("GET /%s Link = %q, want %q", q, got, canonicalLink("/2025/03/page/2/"))
			}
			for _, want := range []string{"/2025/03/page/3/", "/2025/03/"} {
				if !hasHref(body, want) {
					t.Errorf("GET /%s lacks href %s", q, want)
				}
			}
		}
	})
	t.Run("flat", func(t *testing.T) {
		srv, _ := newDateQueryServer(t, "")
		rec := get(t, srv, "/?m=202503")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /?m=202503 = %d, want 200", rec.Code)
		}
		if got := rec.Header().Values("Link"); len(got) != 0 {
			t.Errorf("Flat Link = %q, want none", got)
		}
		if !hasHref(rec.Body.String(), "/?m=202503&page=2") {
			t.Errorf("Flat page 1 Next is not /?m=202503&page=2")
		}
		rec = get(t, srv, "/?m=202503&page=2")
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, pagOnP2) || strings.Contains(body, pagNewest) {
			t.Fatalf("GET /?m=202503&page=2 = %d, want page 2", rec.Code)
		}
		if !hasHref(body, "/?m=202503") || hasHref(body, "/?m=202503&page=1") {
			t.Errorf("Flat page 2 Previous is not /?m=202503")
		}
	})
}

// Req 10, 11: a page past the end is 404 (the shared rule).
func TestDateQueryPastTheEnd(t *testing.T) {
	for _, sc := range dqStructures {
		t.Run(sc.name, func(t *testing.T) {
			srv, _ := newDateQueryServer(t, sc.raw)
			if rec := get(t, srv, "/?m=202503&page=99"); rec.Code != http.StatusNotFound {
				t.Errorf("GET /?m=202503&page=99 = %d, want 404", rec.Code)
			}
			// May 2024 has one page; the home page has three, so only the date
			// archive can answer 404 here.
			if rec := get(t, srv, "/?m=202405&page=2"); rec.Code != http.StatusNotFound {
				t.Errorf("GET /?m=202405&page=2 = %d, want 404", rec.Code)
			}
		})
	}
}

// Req 10.1-10.3: an invalid or empty value is ignored (the home page, 200, same
// body as "/") and makes no date-archive read; the first of several values wins.
func TestDateQueryInvalidIgnored(t *testing.T) {
	for _, sc := range dqStructures {
		t.Run(sc.name, func(t *testing.T) {
			srv, reads := newDateQueryServer(t, sc.raw)
			want := get(t, srv, "/")
			if want.Code != http.StatusOK {
				t.Fatalf("GET / = %d", want.Code)
			}
			for _, q := range []string{"?m=garbage", "?m=202413", "?m=2024051712", "?m=", "?m=20240230", "?m=2024-05", "?m=%2B20240"} {
				t.Run(q, func(t *testing.T) {
					before := reads.Load()
					rec := get(t, srv, "/"+q)
					if rec.Code != http.StatusOK || rec.Body.String() != want.Body.String() {
						t.Errorf("GET /%s = %d, want the home page (200, same body as /)", q, rec.Code)
					}
					if got := rec.Header().Values("Link"); len(got) != 0 {
						t.Errorf("GET /%s Link = %q, want none", q, got)
					}
					if n := reads.Load() - before; n != 0 {
						t.Errorf("GET /%s made %d date-archive reads, want 0", q, n)
					}
				})
			}
			t.Run("valid value does read", func(t *testing.T) {
				before := reads.Load()
				get(t, srv, "/?m=202405")
				if reads.Load() == before {
					t.Errorf("GET /?m=202405 made no date-archive read")
				}
			})
			t.Run("first value wins", func(t *testing.T) {
				rec := get(t, srv, "/?m=2024&m=x")
				if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "2024") ||
					!strings.Contains(body, dqJanPost) || strings.Contains(body, dqMarchPosts) {
					t.Errorf("GET /?m=2024&m=x = %d, want the 2024 archive", rec.Code)
				}
			})
		})
	}
}

// Req 10, 11: `m` is read on the zero home target only. Path-form home pages and
// other archives ignore it.
func TestDateQueryIgnoredElsewhere(t *testing.T) {
	srv, _ := newDateQueryServer(t, "/%postname%/")
	home2 := get(t, srv, "/page/2/")
	rec := get(t, srv, "/page/2/?m=202405")
	if rec.Code != http.StatusOK || rec.Body.String() != home2.Body.String() {
		t.Errorf("GET /page/2/?m=202405 = %d, want home page 2 with m ignored", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), pagOnP2) || strings.Contains(rec.Body.String(), dqMayPost) {
		t.Errorf("GET /page/2/?m=202405 is not home page 2")
	}
	for _, p := range []string{baseGo, baseTag, baseAuthor} {
		plain := get(t, srv, p)
		rec := get(t, srv, p+"?m=202405")
		if rec.Code != http.StatusOK || rec.Body.String() != plain.Body.String() {
			t.Errorf("GET %s?m=202405 = %d, want the same archive as %s with m ignored", p, rec.Code, p)
		}
	}
}
