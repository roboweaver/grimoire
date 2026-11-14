package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// M12 Phase 8.3 -- the RED handler tests (design Component 8b).
//
// Drives a Server wired with a registry (WithRegistry, task 8.2 already landed)
// through the LIVE router over SQLite. The Server's Structure carries the
// registry's routes (WithPermalinks(st.WithCustomTypes(reg.Routes()))) exactly
// as cmd/grimoire does, so the chi patterns exist and every request reaches
// s.resolve. RED today: resolve has no arm for KindCustomPost / KindCustomArchive
// / KindCustomTerm, so those requests fall to the default branch and 404.
//
// HARNESS DECISIONS
//
//   - newCustomServer(t, structure, cfg) builds a real SQLite DB seeded with
//     storagetest.SeedFixtures (post hello-1..3, draft post "secret", page
//     "about", category "news") plus the custom fixture below, then wires
//     PostService, TermService.WithHierarchy, PageService (so a KindPage
//     fall-through is the production shape), CustomPostService and the registry.
//   - The three structures under test are "flat" (""), "/%postname%/" and
//     "/blog/%postname%/". A flat Structure has no trailing slash; the other two
//     do. Expected paths are written by hand from shape.p (front + base +
//     segments + slash) and NOT read back from Structure.CustomPostPath, so the
//     tests cannot agree with a wrong constructor.
//   - Fixture names are chosen so the singular slug and the archive slug differ
//     (book -> /book/..., archive /library/) and so every title is a fixed-width
//     unique token ("Tome-01-x"), letting a listing be checked by substring.
//   - Cycle rows: the exact canonical a cycle resolves to is the walk's choice,
//     so the test asserts only termination and a fixed point (follow at most one
//     301, land on a 200, and GET the landing path again to the same 200).
//
// Fixture (all ids from PostWriter.Create / SQL; terms in the 500/600 bands):
//
//	book (slug book, archive library, taxonomies genre+shelf, with_front)
//	  tome-dune, tome-neuromancer (genre scifi), tome-01..12 filler, DRAFT "unpublished"
//	  tome-dune is also in shelf/classics (child of shelf/fiction)
//	volume (hierarchical, slug volume, no archive)
//	  vol-1 > part-one > (draft-child DRAFT); orphan-vol (parent 999999);
//	  cyc-a <-> cyc-b; draft-vol DRAFT
//	zine (slug zine, archive zines, with_front=false) issue-1
//	ledger (public=false, archive ledgers) entry
//	post "library" and page "library" (shadowed by the archive, Req 7.9)
//	genre (flat, object_types [book]): scifi (dune, neuromancer, hello-1 post!, draft), fantasy (empty)
//	shelf (hierarchical, object_types [book]): fiction > classics

const (
	customBooksPublished = 14 // dune, neuromancer, 12 filler
)

// shape is one permalink structure under test.
type shape struct {
	name      string
	structure string
	front     string // "" or "/blog"
	slash     string // "" (flat) or "/"
}

// p builds an expected path under the structure's front.
func (sh shape) p(segs ...string) string { return sh.front + "/" + strings.Join(segs, "/") + sh.slash }

// bare builds an expected path for with_front=false entries (no front).
func (sh shape) bare(segs ...string) string { return "/" + strings.Join(segs, "/") + sh.slash }

// alt is p with the trailing-slash form toggled (the non-canonical slash form).
func (sh shape) alt(segs ...string) string {
	if sh.slash == "" {
		return sh.front + "/" + strings.Join(segs, "/") + "/"
	}
	return sh.front + "/" + strings.Join(segs, "/")
}

var customShapes = []shape{
	{name: "flat", structure: "", front: "", slash: ""},
	{name: "postname", structure: "/%postname%/", front: "", slash: "/"},
	{name: "blog", structure: "/blog/%postname%/", front: "/blog", slash: "/"},
}

func forEachShape(t *testing.T, fn func(t *testing.T, sh shape, srv *customHarness)) {
	t.Helper()
	for _, sh := range customShapes {
		t.Run(sh.name, func(t *testing.T) {
			fn(t, sh, newCustomServer(t, sh.structure, customContentConfig()))
		})
	}
}

// customHarness is the live router plus what the assertions need.
type customHarness struct {
	h  http.Handler
	st routing.Structure
}

func (c *customHarness) get(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// settle follows at most one 301 and returns the final response and path.
func (c *customHarness) settle(t *testing.T, path string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := c.get(path)
	if rec.Code == http.StatusMovedPermanently {
		path = rec.Header().Get("Location")
		rec = c.get(path)
	}
	return rec, path
}

func customContentConfig() config.ContentConfig {
	yes, no := true, false
	return config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "book", Public: &yes, Slug: "book", WithFront: &yes,
				HasArchive: config.HasArchive{Enabled: true, Slug: "library"},
				Taxonomies: []string{"genre", "shelf"}},
			{Name: "volume", Public: &yes, Slug: "volume", WithFront: &yes, Hierarchical: true},
			{Name: "zine", Public: &yes, Slug: "zine", WithFront: &no,
				HasArchive: config.HasArchive{Enabled: true, Slug: "zines"}},
			{Name: "ledger", Public: &no, Slug: "ledger",
				HasArchive: config.HasArchive{Enabled: true, Slug: "ledgers"}},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", WithFront: &yes, ObjectTypes: []string{"book"}},
			{Name: "shelf", Public: &yes, Slug: "shelf", WithFront: &yes, Hierarchical: true, ObjectTypes: []string{"book"}},
		},
	}
}

// newCustomServer builds the live router for structure over cfg's registry and
// the fixture above.
func newCustomServer(t *testing.T, structure string, cfg config.ContentConfig) *customHarness {
	t.Helper()
	h, st, _ := buildCustomHandler(t, structure, cfg, true)
	return &customHarness{h: h, st: st}
}

// buildCustomHandler is newCustomServer's core. withRegistry=false builds the
// pre-M12 Server (plain Structure, no WithRegistry) over the SAME database.
func buildCustomHandler(t *testing.T, structure string, cfg config.ContentConfig, withRegistry bool) (http.Handler, routing.Structure, *registry.Registry) {
	t.Helper()
	ctx := context.Background()
	dbcfg := config.DatabaseConfig{Vendor: "sqlite", DSN: filepath.Join(t.TempDir(), "grimoire.db"), TablePrefix: "wp_"}
	repos, err := storage.New(dbcfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })
	migFS, err := storage.MigrationsFS(dbcfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, dbcfg.Vendor, dbcfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, repos.DB(), dbcfg.Vendor, dbcfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}
	seedCustomFixture(t, ctx, repos, dbcfg.TablePrefix)

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", structure, err)
	}
	reg, err := registry.Build(cfg, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	if withRegistry {
		types, taxes := reg.Routes()
		st = st.WithCustomTypes(types, taxes)
	}
	posts := content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users)
	terms := content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader)
	srv := web.NewServer(posts, terms, content.NewOptionService(repos.Options), eng, nil).
		WithThemeStatic(filepath.Join("..", "..", "themes"), "default").
		WithPermalinks(st).
		WithPages(content.NewPageService(repos.PageReader, repos.Posts))
	if withRegistry {
		srv = srv.WithRegistry(reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	}
	return srv.Routes(), st, reg
}

func seedCustomFixture(t *testing.T, ctx context.Context, repos *storage.Repositories, prefix string) {
	t.Helper()
	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	n := 0
	mk := func(typ, slug, title, status string, parent int64) int64 {
		t.Helper()
		n++
		id, err := repos.PostWriter.Create(ctx, domain.Post{
			Author: 1, Date: base.Add(time.Duration(n) * time.Hour), Content: "<p>body " + slug + "</p>",
			Title: title, Excerpt: "ex", Status: status, Slug: slug, Type: typ,
			CommentStatus: "closed", ParentID: parent,
		})
		if err != nil {
			t.Fatalf("create %s/%s: %v", typ, slug, err)
		}
		return id
	}
	// books: filler first (oldest), then neuromancer, dune newest.
	for i := 1; i <= 12; i++ {
		mk("book", "filler-"+two(i), "Tome-"+two(i)+"-x", "publish", 0)
	}
	neuro := mk("book", "neuromancer", "Tome-neuromancer-x", "publish", 0)
	dune := mk("book", "dune", "Tome-dune-x", "publish", 0)
	draftBook := mk("book", "unpublished", "Tome-unpublished-x", "draft", 0)

	// volumes (hierarchical).
	vol1 := mk("volume", "vol-1", "Vol-one-x", "publish", 0)
	mk("volume", "part-one", "Vol-part-one-x", "publish", vol1)
	mk("volume", "draft-child", "Vol-draft-child-x", "draft", vol1)
	mk("volume", "draft-vol", "Vol-draft-vol-x", "draft", 0)
	mk("volume", "orphan-vol", "Vol-orphan-x", "publish", 999999)
	cycA := mk("volume", "cyc-a", "Vol-cyc-a-x", "publish", 0)
	cycB := mk("volume", "cyc-b", "Vol-cyc-b-x", "publish", cycA)
	if _, err := repos.DB().ExecContext(ctx, "UPDATE "+prefix+"posts SET post_parent = ? WHERE ID = ?", cycB, cycA); err != nil {
		t.Fatalf("make cycle: %v", err)
	}

	mk("zine", "issue-1", "Zine-issue-1-x", "publish", 0)
	mk("ledger", "entry", "Ledger-entry-x", "publish", 0)
	mk("post", "library", "Shadow-post-x", "publish", 0)
	mk("page", "library", "Shadow-page-x", "publish", 0)

	// Terms: genre (flat) and shelf (hierarchical; parent is the parent's term_id).
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repos.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed sql %q: %v", q, err)
		}
	}
	term := func(termID, ttID int64, name, slug, taxonomy string, parentTermID int64) {
		exec("INSERT INTO "+prefix+"terms (term_id, name, slug) VALUES (?, ?, ?)", termID, name, slug)
		exec("INSERT INTO "+prefix+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
			ttID, termID, taxonomy, "", parentTermID, 0)
	}
	rel := func(postID, ttID int64) {
		exec("INSERT INTO "+prefix+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)", postID, ttID, 0)
	}
	term(501, 601, "Science Fiction", "scifi", "genre", 0)
	term(502, 602, "Fantasy", "fantasy", "genre", 0)
	term(503, 603, "Fiction Shelf", "fiction", "shelf", 0)
	term(504, 604, "Classics Shelf", "classics", "shelf", 503)
	rel(dune, 601)
	rel(neuro, 601)
	rel(draftBook, 601)
	rel(1, 601) // hello-1 (a plain POST) related to the term: the Types trap, it must NOT list
	rel(dune, 604)
}

func two(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, path string, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("GET %s = %d, want %d (Location %q)", path, rec.Code, want, rec.Header().Get("Location"))
	}
}

func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, from, to string) {
	t.Helper()
	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("GET %s = %d, want 301 to %q", from, rec.Code, to)
		return
	}
	if got := rec.Header().Get("Location"); got != to {
		t.Errorf("GET %s Location = %q, want %q", from, got, to)
	}
}

// ---- CPT single (Req 4.1, 4.3, 4.4, 4.7) ----

func TestHandlersCustomSingleCanonicalRenders(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		p := sh.p("book", "dune")
		rec := c.get(p)
		wantStatus(t, rec, p, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "Tome-dune-x") {
			t.Errorf("GET %s body lacks the item title", p)
		}
		if rec.Header().Get("Location") != "" {
			t.Errorf("a 200 carried Location %q", rec.Header().Get("Location"))
		}
	})
}

func TestHandlersCustomSingleNonCanonicalRedirectsWithRawQuery(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		// Opposite trailing-slash form 301s to the canonical, query kept byte for byte.
		from := sh.alt("book", "dune") + "?a=b%20c&flag"
		wantRedirect(t, c.get(from), from, sh.p("book", "dune")+"?a=b%20c&flag")
	})
}

func TestHandlersCustomSingleNotFound(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		for name, p := range map[string]string{
			"nonexistent item":         sh.p("book", "no-such-book"),
			"draft item":               sh.p("book", "unpublished"),
			"extra segment, flat type": sh.p("book", "x", "dune"),
			"volume row under book":    sh.p("book", "vol-1"),
			"book row under volume":    sh.p("volume", "dune"),
			"book row, post base":      sh.p("dune"), // never a post
		} {
			rec := c.get(p)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: GET %s = %d, want 404", name, p, rec.Code)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("%s: GET %s disclosed Location %q", name, p, loc)
			}
		}
	})
}

// A with_front type keeps the front; a without_front type never carries it.
func TestHandlersCustomWithFront(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		withFront := sh.p("book", "dune")
		wantStatus(t, c.get(withFront), withFront, http.StatusOK)
		noFront := sh.bare("zine", "issue-1")
		wantStatus(t, c.get(noFront), noFront, http.StatusOK)
		if sh.front != "" {
			// /blog is part of the with_front canonical, so the bare form is no route.
			bare := sh.bare("book", "dune")
			wantStatus(t, c.get(bare), bare, http.StatusNotFound)
			// ...and a without_front item is not served under the front.
			fronted := sh.p("zine", "issue-1")
			wantStatus(t, c.get(fronted), fronted, http.StatusNotFound)
		}
	})
}

// ---- CPT archive (Req 4.2, 4.3, 4.7) ----

func archiveTitles(body string) (present []string) {
	for i := 1; i <= 12; i++ {
		if strings.Contains(body, "Tome-"+two(i)+"-x") {
			present = append(present, "Tome-"+two(i)+"-x")
		}
	}
	for _, s := range []string{"Tome-dune-x", "Tome-neuromancer-x"} {
		if strings.Contains(body, s) {
			present = append(present, s)
		}
	}
	return present
}

func TestHandlersCustomArchiveListsOnlyThatTypePublished(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		p := sh.p("library")
		rec := c.get(p)
		wantStatus(t, rec, p, http.StatusOK)
		body := rec.Body.String()
		if !strings.Contains(body, ">Book</h1>") {
			t.Errorf("archive heading is not the registry's PostType.Heading() (%q): %s", "Book", body)
		}
		for _, bad := range []string{
			"Tome-unpublished-x", "Vol-one-x", "Zine-issue-1-x", "Ledger-entry-x", "Hello One", "Hello Two", "About",
		} {
			if strings.Contains(body, bad) {
				t.Errorf("archive %s leaked %q", p, bad)
			}
		}
		if got := archiveTitles(body); len(got) != content.DefaultPerPage {
			t.Errorf("page 1 lists %d books %v, want %d", len(got), got, content.DefaultPerPage)
		}
		if !strings.Contains(body, "Tome-dune-x") {
			t.Errorf("newest-first: page 1 must hold the newest book dune")
		}
		if !strings.Contains(body, `href="`+p+`?page=2"`) {
			t.Errorf("page 1 lacks a Next link built from the canonical %q?page=2", p)
		}
	})
}

func TestHandlersCustomArchivePaginationCoversEveryItemOnce(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		p := sh.p("library")
		first, second := c.get(p), c.get(p+"?page=2")
		wantStatus(t, second, p+"?page=2", http.StatusOK)
		a, b := archiveTitles(first.Body.String()), archiveTitles(second.Body.String())
		if len(b) != customBooksPublished-content.DefaultPerPage {
			t.Errorf("page 2 lists %d books %v, want %d", len(b), b, customBooksPublished-content.DefaultPerPage)
		}
		seen := map[string]bool{}
		for _, s := range append(append([]string{}, a...), b...) {
			if seen[s] {
				t.Errorf("%q appears on both pages (duplicate)", s)
			}
			seen[s] = true
		}
		if len(seen) != customBooksPublished {
			t.Errorf("%d distinct books across pages, want %d", len(seen), customBooksPublished)
		}
		if !strings.Contains(second.Body.String(), `href="`+p+`?page=1"`) {
			t.Errorf("page 2 lacks a Previous link built from the canonical")
		}
		// Past the last page is a 404, not an empty 200.
		wantStatus(t, c.get(p+"?page=3"), p+"?page=3", http.StatusNotFound)
	})
}

func TestHandlersCustomArchiveNonCanonicalRedirectsWithRawQuery(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		from := sh.alt("library") + "?page=2&q=a%20b"
		wantRedirect(t, c.get(from), from, sh.p("library")+"?page=2&q=a%20b")
	})
}

func TestHandlersCustomArchiveNotRoutedCases(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		for name, p := range map[string]string{
			"public=false type archive": sh.p("ledgers"),
			"public=false type item":    sh.p("ledger", "entry"),
			"type without has_archive":  sh.p("volume"),
			"archive has no sub-path":   sh.p("library", "x"),
		} {
			if rec := c.get(p); rec.Code != http.StatusNotFound {
				t.Errorf("%s: GET %s = %d, want 404", name, p, rec.Code)
			}
		}
		// The with_front=false type's archive lives bare.
		z := sh.bare("zines")
		rec := c.get(z)
		wantStatus(t, rec, z, http.StatusOK)
		if !strings.Contains(rec.Body.String(), ">Zine</h1>") || !strings.Contains(rec.Body.String(), "Zine-issue-1-x") {
			t.Errorf("zine archive lacks its heading or item")
		}
	})
}

// ---- Req 7.9: a post or page slugged like the archive is shadowed ----

func TestHandlersCustomArchiveShadowsSameSlugPostAndPage(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		p := sh.p("library")
		rec := c.get(p)
		wantStatus(t, rec, p, http.StatusOK)
		body := rec.Body.String()
		if !strings.Contains(body, ">Book</h1>") {
			t.Errorf("GET %s is not the CPT archive", p)
		}
		if strings.Contains(body, "Shadow-post-x") || strings.Contains(body, "Shadow-page-x") {
			t.Errorf("the same-slug post or page leaked through the archive route")
		}
		// Other posts and pages still resolve.
		for _, w := range []struct{ path, title string }{
			{sh.p("hello-1"), "Hello One"},
			{sh.bare("about"), "About"},
		} {
			final, at := c.settle(t, w.path)
			if final.Code != http.StatusOK || !strings.Contains(final.Body.String(), w.title) {
				t.Errorf("GET %s settled at %s = %d, want 200 containing %q", w.path, at, final.Code, w.title)
			}
		}
	})
}

// ---- Hierarchical CPT (Req 5.1-5.5) ----

func TestHandlersCustomHierarchicalSingle(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		canon := sh.p("volume", "vol-1", "part-one")
		rec := c.get(canon)
		wantStatus(t, rec, canon, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "Vol-part-one-x") {
			t.Errorf("nested canonical lacks the child's title")
		}
		top := sh.p("volume", "vol-1")
		wantStatus(t, c.get(top), top, http.StatusOK)

		// Skipped level (the flat leaf of a nested item) and a wrong ancestor 301 to the canonical.
		for name, from := range map[string]string{
			"flat leaf":      sh.p("volume", "part-one"),
			"wrong ancestor": sh.p("volume", "cyc-a", "part-one"),
			"slash form":     sh.alt("volume", "vol-1", "part-one"),
		} {
			wantRedirect(t, c.get(from+"?a=b%20c"), name+" "+from, canon+"?a=b%20c")
		}
		for name, p := range map[string]string{
			"unknown leaf":        sh.p("volume", "vol-1", "nope"),
			"draft leaf":          sh.p("volume", "vol-1", "draft-child"),
			"draft leaf, skipped": sh.p("volume", "draft-child"),
			"draft top":           sh.p("volume", "draft-vol"),
		} {
			rec := c.get(p)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: GET %s = %d, want 404 (Location %q)", name, p, rec.Code, rec.Header().Get("Location"))
			}
		}
	})
}

func TestHandlersCustomHierarchicalOrphanAndCycleTerminate(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		// Orphan: parent id names no row, so the item degrades to the shorter path.
		orphan := sh.p("volume", "orphan-vol")
		rec := c.get(orphan)
		wantStatus(t, rec, orphan, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "Vol-orphan-x") {
			t.Errorf("orphan item not rendered at its shortened canonical")
		}
		// Cycle (Req 5.5): the walk only has to terminate on the first repeat so a
		// corrupt graph cannot loop; a 200 is not required. The content layer
		// (PageHierarchy.recoverPath equal-ancestry guard, shared with M15 pages)
		// returns ErrNotFound for a cycle member whose canonical equals the
		// requested path, so a 404 at the landing path is the documented outcome.
		// Follow at most 2 redirect hops and require: no 5xx, no self-redirect,
		// no redirect loop, and a final status of 200 or 404.
		for _, leaf := range []string{"cyc-a", "cyc-b"} {
			at := sh.p("volume", leaf)
			seen := map[string]bool{at: true}
			var rec *httptest.ResponseRecorder
			for hop := 0; ; hop++ {
				rec = c.get(at)
				if rec.Code != http.StatusMovedPermanently {
					break
				}
				loc := rec.Header().Get("Location")
				if loc == at || seen[loc] {
					t.Errorf("cycle row %s: redirect loop/self-redirect at %s -> %s", leaf, at, loc)
					break
				}
				if hop >= 2 {
					t.Errorf("cycle row %s: still redirecting after 2 hops (at %s -> %s)", leaf, at, loc)
					break
				}
				seen[loc] = true
				at = loc
			}
			if rec.Code >= 500 {
				t.Errorf("cycle row %s settled at %s with %d, want no 5xx", leaf, at, rec.Code)
			} else if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
				t.Errorf("cycle row %s settled at %s with %d, want 200 or 404", leaf, at, rec.Code)
			}
		}
	})
}

// ---- Custom taxonomy term archive (Req 6.1-6.5) ----

func TestHandlersCustomTermFlatArchive(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		p := sh.p("genre", "scifi")
		rec := c.get(p)
		wantStatus(t, rec, p, http.StatusOK)
		body := rec.Body.String()
		for _, want := range []string{">Science Fiction</h1>", "Tome-dune-x", "Tome-neuromancer-x"} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %q", p, want)
			}
		}
		// The Types trap: the listing is the taxonomy's OBJECT TYPES (book), so
		// the plain post related to the same term and the draft book are absent.
		for _, bad := range []string{"Hello One", "Tome-unpublished-x"} {
			if strings.Contains(body, bad) {
				t.Errorf("GET %s leaked %q", p, bad)
			}
		}
		// An existing term with no items is an empty 200, not a 404.
		empty := sh.p("genre", "fantasy")
		wantStatus(t, c.get(empty), empty, http.StatusOK)

		from := sh.alt("genre", "scifi") + "?x=a%20b"
		wantRedirect(t, c.get(from), from, p+"?x=a%20b")

		for name, bad := range map[string]string{
			"unknown term":              sh.p("genre", "nope"),
			"term of another taxonomy":  sh.p("genre", "fiction"),
			"flat taxonomy, 2 segments": sh.p("genre", "scifi", "extra"),
		} {
			if rec := c.get(bad); rec.Code != http.StatusNotFound {
				t.Errorf("%s: GET %s = %d, want 404", name, bad, rec.Code)
			}
		}
	})
}

func TestHandlersCustomTermHierarchicalArchive(t *testing.T) {
	forEachShape(t, func(t *testing.T, sh shape, c *customHarness) {
		child := sh.p("shelf", "fiction", "classics")
		rec := c.get(child)
		wantStatus(t, rec, child, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "Tome-dune-x") {
			t.Errorf("nested term archive lacks its item")
		}
		// A parent term's archive includes its descendants' items.
		parent := sh.p("shelf", "fiction")
		rec = c.get(parent)
		wantStatus(t, rec, parent, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "Tome-dune-x") {
			t.Errorf("parent term archive lacks the descendant's item")
		}
		// Wrong ancestry (CategoryMovedError reused) 301s to the canonical, query raw.
		from := sh.p("shelf", "classics") + "?a=b%20c"
		wantRedirect(t, c.get(from), from, child+"?a=b%20c")
		slashForm := sh.alt("shelf", "fiction", "classics")
		wantRedirect(t, c.get(slashForm), slashForm, child)

		for name, bad := range map[string]string{
			"unknown term":      sh.p("shelf", "fiction", "nope"),
			"unknown top term":  sh.p("shelf", "nope"),
			"term of other tax": sh.p("shelf", "scifi"),
		} {
			if rec := c.get(bad); rec.Code != http.StatusNotFound {
				t.Errorf("%s: GET %s = %d, want 404", name, bad, rec.Code)
			}
		}
	})
}

// ---- Template story and the render.post_html filter (design 8b) ----

// A CPT single renders single.tmpl (article markup), the archive renders
// archive.tmpl; neither falls back to index.tmpl.
func TestHandlersCustomRendersExistingTemplates(t *testing.T) {
	c := newCustomServer(t, "/%postname%/", customContentConfig())
	single := c.get("/book/dune/")
	wantStatus(t, single, "/book/dune/", http.StatusOK)
	if !strings.Contains(single.Body.String(), `theme-article`) {
		t.Errorf("CPT single did not render single.tmpl")
	}
	arch := c.get("/library/")
	wantStatus(t, arch, "/library/", http.StatusOK)
	if !strings.Contains(arch.Body.String(), `theme-card-grid`) {
		t.Errorf("CPT archive did not render archive.tmpl")
	}
	term := c.get("/genre/scifi/")
	wantStatus(t, term, "/genre/scifi/", http.StatusOK)
	if !strings.Contains(term.Body.String(), `theme-card-grid`) || strings.Contains(term.Body.String(), "No posts in this category") {
		t.Errorf("custom term archive did not render archive.tmpl")
	}
}

func TestHandlersCustomSingleRunsPostHTMLFilter(t *testing.T) {
	registerPhase8Filter()
	c := newCustomServer(t, "/%postname%/", customContentConfig())
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, phase8Request(http.MethodGet, "/book/dune/", phase8ActionMark))
	wantStatus(t, rec, "/book/dune/", http.StatusOK)
	if !strings.Contains(rec.Body.String(), phase8MarkerString) {
		t.Errorf("render.post_html did not fire for a CPT single")
	}
}

// ---- A Server with no registry is unchanged ----

func TestHandlersCustomNoRegistryServerUnchanged(t *testing.T) {
	for _, sh := range customShapes {
		t.Run(sh.name, func(t *testing.T) {
			h, _, _ := buildCustomHandler(t, sh.structure, customContentConfig(), false)
			c := &customHarness{h: h}
			for name, w := range map[string]struct {
				path  string
				title string
			}{
				"post":     {sh.p("hello-1"), "Hello One"},
				"page":     {sh.bare("about"), "About"},
				"category": {sh.p("category", "news"), ""},
			} {
				final, at := c.settle(t, w.path)
				if final.Code != http.StatusOK {
					t.Errorf("%s: GET %s settled at %s = %d, want 200", name, w.path, at, final.Code)
				}
				if w.title != "" && !strings.Contains(final.Body.String(), w.title) {
					t.Errorf("%s: %s lacks %q", name, at, w.title)
				}
			}
			// Without WithRegistry nothing custom is routed, even though the rows exist.
			for _, p := range []string{sh.p("book", "dune"), sh.p("genre", "scifi")} {
				if rec := c.get(p); rec.Code != http.StatusNotFound {
					t.Errorf("GET %s on a registry-less Server = %d, want 404", p, rec.Code)
				}
			}
			// The archive-slug post/page is reachable again (no registry, no shadow).
			final, at := c.settle(t, sh.p("library"))
			if final.Code != http.StatusOK || !strings.Contains(final.Body.String(), "Shadow-") {
				t.Errorf("GET %s settled at %s = %d, want the same-slug post or page", sh.p("library"), at, final.Code)
			}
		})
	}
}
