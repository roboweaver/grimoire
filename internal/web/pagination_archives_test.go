package web_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// M16 task 3.1 -- the RED handler and router tests for path-form pagination
// (requirements 4-9, design "Canonical policy" and "Error Handling").
//
// Fixture, layered on the archive fixtures of archives_test.go by seedPaginated:
//
//	25 published posts PagPost-00..PagPost-24 (ids 1000..1024), author admin,
//	all dated 2025-03-15 (one minute apart, so PagPost-24 is newest), all in
//	category go (tech/go) and tag golang. With DefaultPerPage 10 each archive
//	below has 3+ pages; page 2 holds PagPost-14..PagPost-05 and never PagPost-24.
//	category "page" (slug page, term 70) holds PagPost-14..24 (two pages) and has a
//	child category slugged "2" (term 71, holds PagPost-24).
//	seedPageSluggedPage (id 1051) and seedPostSluggedPage (id 1052, dated
//	2024-01-10) are opt-in extras: a post and a page slugged "page" cannot share a
//	site, so the page test and the route probes each take one.
const (
	pagNewest = "PagPost-24"
	pagOnP2   = "PagPost-14" // on page 2 of every 25-post archive
	pagOldest = "PagPost-00" // on page 3 of every 25-post archive
)

const (
	baseHome   = "/"
	baseGo     = "/category/tech/go/"
	baseTag    = "/tag/golang/"
	baseAuthor = "/author/admin/"
	baseYear   = "/2025/"
	baseMonth  = "/2025/03/"
	baseDay    = "/2025/03/15/"
)

// seedPaginated adds the fixture above to the archive fixtures.
func seedPaginated(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	if err := seedArchives(ctx, db, vendor, prefix); err != nil {
		return err
	}
	exec := func(q string, args ...any) error {
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("seed %q: %w", q, err)
		}
		return nil
	}
	post := func(id int64, slug, title, ptype, date string) error {
		return exec("INSERT INTO "+prefix+"posts (ID, post_author, post_date, post_content, post_title, "+
			"post_excerpt, post_status, post_name, post_type, comment_status, post_parent, post_mime_type, menu_order) "+
			"VALUES (?, 1, ?, '<p>body</p>', ?, 'ex', 'publish', ?, ?, 'open', 0, '', 0)", id, date, title, slug, ptype)
	}
	rel := func(obj, tt int64) error {
		return exec("INSERT INTO "+prefix+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, 0)", obj, tt)
	}
	for i := 0; i < 25; i++ {
		id := int64(1000 + i)
		if err := post(id, fmt.Sprintf("pag-post-%02d", i), fmt.Sprintf("PagPost-%02d", i), "post",
			fmt.Sprintf("2025-03-15 10:%02d:00", i)); err != nil {
			return err
		}
		if err := rel(id, storagetest.NestedChildTermTaxonomyID); err != nil {
			return err
		}
		if err := rel(id, 23); err != nil { // tag golang
			return err
		}
	}
	// Category slugged "page" with a child slugged "2".
	if err := exec("INSERT INTO " + prefix + "terms (term_id, name, slug) VALUES (70, 'Page Cat', 'page')"); err != nil {
		return err
	}
	if err := exec("INSERT INTO " + prefix + "terms (term_id, name, slug) VALUES (71, 'Child Two', '2')"); err != nil {
		return err
	}
	for _, r := range [][]any{{80, 70, 0}, {81, 71, 70}} {
		if err := exec("INSERT INTO "+prefix+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) "+
			"VALUES (?, ?, 'category', '', ?, 0)", r[0], r[1], r[2]); err != nil {
			return err
		}
	}
	for i := 14; i < 25; i++ {
		if err := rel(int64(1000+i), 80); err != nil {
			return err
		}
	}
	if err := rel(1024, 81); err != nil {
		return err
	}
	return nil
}

// seedPageSluggedPage adds a top-level page slugged "page".
func seedPageSluggedPage(ctx context.Context, db *sql.DB, _, prefix string) error {
	_, err := db.ExecContext(ctx, "INSERT INTO "+prefix+"posts (ID, post_author, post_date, post_content, post_title, "+
		"post_excerpt, post_status, post_name, post_type, comment_status, post_parent, post_mime_type, menu_order) "+
		"VALUES (1051, 1, '2024-01-09 00:00:00', '<p>body</p>', 'PageSlugged', 'ex', 'publish', 'page', 'page', 'open', 0, '', 0)")
	return err
}

// seedPostSluggedPage adds a published post slugged "page", dated 2024-01-10.
func seedPostSluggedPage(ctx context.Context, db *sql.DB, _, prefix string) error {
	_, err := db.ExecContext(ctx, "INSERT INTO "+prefix+"posts (ID, post_author, post_date, post_content, post_title, "+
		"post_excerpt, post_status, post_name, post_type, comment_status, post_parent, post_mime_type, menu_order) "+
		"VALUES (1052, 1, '2024-01-10 00:00:00', '<p>body</p>', 'PostSlugged', 'ex', 'publish', 'page', 'post', 'open', 0, '', 0)")
	return err
}

// newPaginationServer builds the live router over the fixture with pages wired
// (WithPages), because a top-level page slugged "page" must stay reachable.
// withPages=false builds the same server without them for the route probes.
func newPaginationServer(t *testing.T, structure string, withPages bool, extras ...seedFunc) http.Handler {
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
	for _, extra := range extras {
		if err := extra(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
			t.Fatalf("extra seed: %v", err)
		}
	}
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", structure, err)
	}
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithThemeStatic(filepath.Join("..", "..", "themes"), "default").WithPermalinks(st)
	if withPages {
		srv = srv.WithPages(content.NewPageService(repos.PageReader, repos.Posts))
	}
	return srv.Routes()
}

// archiveKind is one paginated archive: its canonical page-1 path.
type archiveKind struct {
	name string
	base string
}

var paginatedKinds = []archiveKind{
	{"home", baseHome},
	{"nested category", baseGo},
	{"tag", baseTag},
	{"author", baseAuthor},
	{"year", baseYear},
	{"month", baseMonth},
	{"day", baseDay},
}

// pageForm returns the canonical path-form URL of page n under base (a trailing
// slash structure).
func pageForm(base string, n int) string {
	return strings.TrimSuffix(base, "/") + fmt.Sprintf("/page/%d/", n)
}

// Req 4.1, 5.1: the path form serves the right page; a page above the last is 404.
func TestPaginationPathFormServesPage(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true)
	for _, k := range paginatedKinds {
		t.Run(k.name, func(t *testing.T) {
			p2 := pageForm(k.base, 2)
			rec := get(t, srv, p2)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (Location %q)", p2, rec.Code, rec.Header().Get("Location"))
			}
			body := rec.Body.String()
			if !strings.Contains(body, pagOnP2) || strings.Contains(body, pagNewest) {
				t.Errorf("GET %s is not page 2: has %s=%v, has %s=%v", p2,
					pagOnP2, strings.Contains(body, pagOnP2), pagNewest, strings.Contains(body, pagNewest))
			}
			p3 := pageForm(k.base, 3)
			if rec := get(t, srv, p3); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), pagOldest) {
				t.Errorf("GET %s = %d, want 200 holding %s", p3, rec.Code, pagOldest)
			}
			past := pageForm(k.base, 99)
			if rec := get(t, srv, past); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", past, rec.Code)
			}
		})
	}
}

// Req 4.3, 5.3, 9.1: page 1 and the other slash form redirect, keeping the raw query.
func TestPaginationRedirectsKeepRawQuery(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true)
	const q = "?utm=a%20b&x"
	for _, k := range paginatedKinds {
		t.Run(k.name, func(t *testing.T) {
			p1 := strings.TrimSuffix(k.base, "/") + "/page/1/"
			assertRedirectLocation(t, srv, p1+q, k.base+q)
			assertRedirectLocation(t, srv, p1, k.base)
			noSlash := strings.TrimSuffix(k.base, "/") + "/page/2"
			assertRedirectLocation(t, srv, noSlash+q, pageForm(k.base, 2)+q)
		})
	}
}

// Req 5.4, 5.5: an unknown entity is 404 before the page is read; an existing
// empty archive answers 200 on page 2.
func TestPaginationUnknownAndEmptyArchives(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true)
	for path, want := range map[string]int{
		"/category/nope/page/2/":      http.StatusNotFound,
		"/tag/nope/page/2/":           http.StatusNotFound,
		"/author/nobody/page/2/":      http.StatusNotFound,
		"/tag/unused/page/2/":         http.StatusOK,
		"/author/ghost/page/2/":       http.StatusOK,
		"/2023/page/2/":               http.StatusOK,
		"/category/tech/idle/page/2/": http.StatusOK,
	} {
		if rec := get(t, srv, path); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}

// Req 5.6: a moved category keeps its page number in the 301 target.
func TestPaginationMovedCategoryKeepsPage(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true)
	assertRedirectLocation(t, srv, "/category/go/page/2/", "/category/tech/go/page/2/")
	assertRedirectLocation(t, srv, "/category/go/page/2/?utm=a%20b", "/category/tech/go/page/2/?utm=a%20b")
}

// Req 3.3, 3.4: a category slugged "page" still resolves, and the
// disambiguation matrix rows hold through the handler.
func TestPaginationCategorySluggedPage(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true)

	rec := get(t, srv, "/category/page/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Page Cat") {
		t.Errorf("GET /category/page/ = %d, want the category page", rec.Code)
	}
	// Page 2 of category "page": the remainder /category/page is a category.
	rec = get(t, srv, "/category/page/page/2/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /category/page/page/2/ = %d, want 200 (page 2 of category page)", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "PagPost-14") || strings.Contains(body, pagNewest) {
		t.Errorf("GET /category/page/page/2/ is not page 2 of category page")
	}
	// /category/page/2/ stays the category walk [page 2]: the child category "2".
	rec = get(t, srv, "/category/page/2/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Child Two") {
		t.Errorf("GET /category/page/2/ = %d, want the child category 2 (walk [page 2])", rec.Code)
	}
}

// Req 3.5: a top-level page slugged "page" is still reachable at /page/.
func TestPaginationTopLevelPageSluggedPage(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true, seedPageSluggedPage)
	rec := get(t, srv, "/page/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "PageSlugged") {
		t.Errorf("GET /page/ = %d, want the page slugged page (body %.120s)", rec.Code, rec.Body.String())
	}
}

// Req 9.2: a Flat structure serves the path form without a trailing slash.
func TestPaginationFlatStructure(t *testing.T) {
	srv := newPaginationServer(t, "", true)
	for _, p := range []string{"/page/2", "/tag/golang/page/2"} {
		rec := get(t, srv, p)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), pagOnP2) {
			t.Errorf("GET %s = %d, want 200 page 2", p, rec.Code)
		}
		assertRedirectLocation(t, srv, p+"/?utm=1", p+"?utm=1")
	}
	assertRedirectLocation(t, srv, "/page/1", "/")
	assertRedirectLocation(t, srv, "/tag/golang/page/1", "/tag/golang")
}

// ---- custom post type archive and custom taxonomy term (registry fixture) ----

// newPaginationCustom is buildCustomHandler plus a flat "bestseller" genre term
// holding all 14 published books, so a custom term has a second page.
func newPaginationCustom(t *testing.T, structure string) *customHarness {
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
	seedCustomFixture(t, ctx, repos, cfg.TablePrefix)
	db := repos.DB()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	mustExec("INSERT INTO wp_terms (term_id, name, slug) VALUES (505, 'Bestseller', 'bestseller')")
	mustExec("INSERT INTO wp_term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (605, 505, 'genre', '', 0, 0)")
	mustExec("INSERT INTO wp_term_relationships (object_id, term_taxonomy_id, term_order) " +
		"SELECT ID, 605, 0 FROM wp_posts WHERE post_type = 'book' AND post_status = 'publish'")

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	reg, err := registry.Build(customContentConfig(), st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	types, taxes := reg.Routes()
	st = st.WithCustomTypes(types, taxes)
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithThemeStatic(filepath.Join("..", "..", "themes"), "default").
		WithPermalinks(st).
		WithPages(content.NewPageService(repos.PageReader, repos.Posts)).
		WithRegistry(reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	return &customHarness{h: srv.Routes(), st: st}
}

// Req 4.1, 5.1, 5.3, 5.6: a custom archive and a custom term (flat and
// hierarchical), under Flat, a trailing-slash and a fronted structure.
func TestPaginationCustomArchiveAndTerm(t *testing.T) {
	for _, sh := range customShapes {
		t.Run(sh.name, func(t *testing.T) {
			c := newPaginationCustom(t, sh.structure)

			for _, base := range [][]string{{"library"}, {"genre", "bestseller"}} {
				p2 := sh.p(append(append([]string{}, base...), "page", "2")...)
				rec := c.get(p2)
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s = %d, want 200 (Location %q)", p2, rec.Code, rec.Header().Get("Location"))
				}
				if body := rec.Body.String(); !strings.Contains(body, "Tome-01-x") || strings.Contains(body, "Tome-dune-x") {
					t.Errorf("GET %s is not page 2 of the 14 books", p2)
				}
				past := sh.p(append(append([]string{}, base...), "page", "3")...)
				wantStatus(t, c.get(past), past, http.StatusNotFound)

				baseP := sh.p(base...)
				p1 := sh.p(append(append([]string{}, base...), "page", "1")...)
				wantRedirect(t, c.get(p1+"?utm=a%20b&x"), p1, baseP+"?utm=a%20b&x")
				alt := sh.alt(append(append([]string{}, base...), "page", "2")...)
				wantRedirect(t, c.get(alt+"?utm=a%20b&x"), alt, p2+"?utm=a%20b&x")
			}

			// Unknown entity 404 before the page; an existing empty term is 200.
			wantStatus(t, c.get(sh.p("genre", "nope", "page", "2")), "unknown term", http.StatusNotFound)
			wantStatus(t, c.get(sh.p("genre", "fantasy", "page", "2")), "empty term", http.StatusOK)

			// A hierarchical term reached by a wrong ancestry moves with its page.
			from := sh.p("shelf", "classics", "page", "2")
			wantRedirect(t, c.get(from), from, sh.p("shelf", "fiction", "classics", "page", "2"))
		})
	}
}
