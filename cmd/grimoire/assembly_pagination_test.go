package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
)

// M16 task 3.2: the path-form pagination wiring, asserted through buildHandler.
// The test is written RED, before internal/web learns the /page/{n}/ routes, so
// every path-form subtest fails with a 404 until task 3.3.
//
//	assertion                                         guards
//	------------------------------------------------  ----------------------------------
//	<base>/page/2/ -> 200 (every kind)                router: st.PaginationPatterns()
//	<base>/page/1/ -> 301 Location <base>             handler: redirectCanonical(page 1)
//	<base>?page=2  -> 200 + Link canonical            handler: setCanonicalLink
//	home body has an href to /page/2/                 handler PrevURL/NextURL + theme
//	page 2 Previous link is <base>, not /page/1/      PageLinks / PaginatedPath
//	last page has no Next link                        PageLinks (next == "")

const (
	paginationPosts = 25 // three pages at content.DefaultPerPage (10)
	paginationBooks = 25
)

var (
	paginationPrevRE = regexp.MustCompile(`href="([^"]*)"[^>]*>\s*Previous\s*<`)
	paginationNextRE = regexp.MustCompile(`>\s*Next\s*<`)
)

// seedPagination inserts the admin user, 25 published posts (12 in 2024-04,
// 13 in 2024-05) all in category x and tag t by author 1, and 25 published
// books all in the hierarchical shelf term fiction.
func seedPagination(t *testing.T, repos *storage.Repositories, prefix string) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repos.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed sql %q: %v", q, err)
		}
	}
	exec(`INSERT INTO `+prefix+`users (ID, user_login, user_nicename, display_name) VALUES (?, ?, ?, ?)`,
		1, "admin", "admin", "Admin")
	term := func(termID, ttID int64, name, slug, taxonomy string, count int) {
		exec("INSERT INTO "+prefix+"terms (term_id, name, slug) VALUES (?, ?, ?)", termID, name, slug)
		exec("INSERT INTO "+prefix+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
			ttID, termID, taxonomy, "", 0, count)
	}
	term(701, 801, "X", "x", "category", paginationPosts)
	term(702, 802, "T", "t", "post_tag", paginationPosts)
	term(703, 803, "Fiction", "fiction", "shelf", paginationBooks)

	const ins = `INSERT INTO ` + "wp_" + `posts ` +
		`(ID, post_author, post_date, post_date_gmt, post_content, post_title, post_excerpt, post_status, post_name, post_type, comment_status, post_parent, post_mime_type, menu_order) ` +
		`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	rel := func(objectID, ttID int64) {
		exec("INSERT INTO "+prefix+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)", objectID, ttID, 0)
	}
	for i := 1; i <= paginationPosts; i++ {
		month, day := "04", i
		if i > 12 {
			month, day = "05", i-12
		}
		date := fmt.Sprintf("2024-%s-%02d 10:00:00", month, day)
		id := int64(1000 + i)
		exec(ins, id, 1, date, date, "<p>body</p>", fmt.Sprintf("Post %02d", i), "", "publish",
			fmt.Sprintf("post-%02d", i), "post", "closed", 0, "", 0)
		rel(id, 801)
		rel(id, 802)
	}
	for i := 1; i <= paginationBooks; i++ {
		date := fmt.Sprintf("2024-03-%02d 10:00:00", i)
		id := int64(2000 + i)
		exec(ins, id, 1, date, date, "<p>book</p>", fmt.Sprintf("Book %02d", i), "", "publish",
			fmt.Sprintf("book-%02d", i), "book", "closed", 0, "", 0)
		rel(id, 803)
	}
}

// paginationHandler builds the production handler for structure over a freshly
// seeded database. The custom registry (a book archive at /library/ and a
// hierarchical shelf taxonomy) is attached unless the structure is Flat.
func paginationHandler(t *testing.T, structure string, withRegistry bool) http.Handler {
	t.Helper()
	repos, dbCfg := newAssemblySQLite(t)
	seedPagination(t, repos, dbCfg.TablePrefix)
	cfg := config.Config{
		Theme:    "default",
		Database: dbCfg,
		Session:  config.SessionConfig{CookieName: "grimoire_session", TTLHours: 24},
		Media:    config.MediaConfig{UploadsDir: t.TempDir()},
		REST:     config.RESTConfig{PerPageMax: 100},
	}
	themesDir := filepath.Join("..", "..", "themes")
	eng, err := render.Load(themesDir, cfg.Theme)
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", structure, err)
	}
	var reg *registry.Registry
	if withRegistry {
		yes := true
		reg, err = registry.Build(config.ContentConfig{
			PostTypes: []config.PostTypeConfig{
				{Name: "book", Public: &yes, Slug: "book", WithFront: &yes,
					HasArchive: config.HasArchive{Enabled: true, Slug: "library"},
					Taxonomies: []string{"shelf"}},
			},
			Taxonomies: []config.TaxonomyConfig{
				{Name: "shelf", Public: &yes, Slug: "shelf", WithFront: &yes,
					Hierarchical: true, ObjectTypes: []string{"book"}},
			},
		}, st)
		if err != nil {
			t.Fatalf("registry.Build: %v", err)
		}
	}
	h, _, err := buildHandler(serverDeps{
		Repos:      repos,
		Config:     cfg,
		Log:        slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		Permalinks: st,
		Registry:   reg,
		Sessions: &auth.SessionManager{
			Users:    repos.Users,
			Meta:     repos.UserMeta,
			Sessions: repos.Sessions,
			TTL:      cfg.Session.TTL(),
			Prefix:   cfg.Database.TablePrefix,
		},
		ContentPolicy: sanitize.New(),
		Engine:        eng,
		ThemesDir:     themesDir,
	})
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	return h
}

func TestBuildHandlerPagination(t *testing.T) {
	type archive struct {
		name string
		base string // page-1 path as the structure spells it, without the slash decision
		// custom archives are absent under Flat, and so are date archives
		custom, date bool
	}
	archives := []archive{
		{name: "home", base: "/"},
		{name: "category", base: "/category/x"},
		{name: "tag", base: "/tag/t"},
		{name: "author", base: "/author/admin"},
		{name: "date", base: "/2024", date: true},
		{name: "custom archive", base: "/library", custom: true},
		{name: "custom term", base: "/shelf/fiction", custom: true},
	}
	structures := []struct {
		name, raw string
		slash     bool
		flat      bool
	}{
		{"postname", "/%postname%/", true, false},
		{"year-month-postname", "/%year%/%monthnum%/%postname%/", true, false},
		{"flat", "", false, true},
	}
	for _, sc := range structures {
		t.Run(sc.name, func(t *testing.T) {
			h := paginationHandler(t, sc.raw, !sc.flat)
			get := func(path string) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				return rec
			}
			// base is the canonical page-1 path; paged is the canonical page-n path.
			base := func(a archive) string {
				p := strings.TrimSuffix(a.base, "/")
				if sc.slash || p == "" {
					return p + "/"
				}
				return p
			}
			paged := func(a archive, n int) string {
				p := strings.TrimSuffix(a.base, "/") + fmt.Sprintf("/page/%d", n)
				if sc.slash {
					p += "/"
				}
				return p
			}
			for _, a := range archives {
				if sc.flat && (a.custom || a.date) {
					continue
				}
				t.Run(a.name, func(t *testing.T) {
					t.Run("page 2 answers 200", func(t *testing.T) {
						if rec := get(paged(a, 2)); rec.Code != http.StatusOK {
							t.Fatalf("GET %s = %d, want 200", paged(a, 2), rec.Code)
						}
					})
					t.Run("page 1 redirects to the base", func(t *testing.T) {
						rec := get(paged(a, 1))
						if rec.Code != http.StatusMovedPermanently {
							t.Fatalf("GET %s = %d, want 301", paged(a, 1), rec.Code)
						}
						if loc := rec.Header().Get("Location"); loc != base(a) {
							t.Fatalf("Location = %q, want %q", loc, base(a))
						}
					})
					t.Run("?page=2 answers 200 with a canonical Link", func(t *testing.T) {
						rec := get(base(a) + "?page=2")
						if rec.Code != http.StatusOK {
							t.Fatalf("GET %s?page=2 = %d, want 200", base(a), rec.Code)
						}
						want := `<` + paged(a, 2) + `>; rel="canonical"`
						if got := rec.Header().Get("Link"); got != want {
							t.Fatalf("Link = %q, want %q", got, want)
						}
					})
					t.Run("page 2 Previous link is the base", func(t *testing.T) {
						rec := get(paged(a, 2))
						if rec.Code != http.StatusOK {
							t.Fatalf("GET %s = %d, want 200", paged(a, 2), rec.Code)
						}
						m := paginationPrevRE.FindStringSubmatch(rec.Body.String())
						if m == nil {
							t.Fatalf("no Previous link in page 2 body")
						}
						if m[1] != base(a) {
							t.Fatalf("Previous href = %q, want %q (never a /page/1/ form)", m[1], base(a))
						}
					})
					t.Run("last page has no Next link", func(t *testing.T) {
						rec := get(paged(a, 3))
						if rec.Code != http.StatusOK {
							t.Fatalf("GET %s = %d, want 200", paged(a, 3), rec.Code)
						}
						if paginationNextRE.MatchString(rec.Body.String()) {
							t.Fatalf("page 3 of 3 renders a Next link")
						}
					})
					t.Run("page 1 body links to page 2 in the path form", func(t *testing.T) {
						rec := get(base(a))
						if rec.Code != http.StatusOK {
							t.Fatalf("GET %s = %d, want 200", base(a), rec.Code)
						}
						if want := `href="` + paged(a, 2) + `"`; !strings.Contains(rec.Body.String(), want) {
							t.Fatalf("page 1 body has no %s", want)
						}
					})
				})
			}
		})
	}
}
