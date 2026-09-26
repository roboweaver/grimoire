package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
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

// The fixtures these tests rely on: storagetest.SeedFixtures plus
// storagetest.SeedNestedCategories, so the category taxonomy carries a real
// three-level chain rather than the six flat terms SeedFixtures alone produces.
//
//	categories (term id, parent)
//	  news   10, 0     zeta 11, 0     alpha 12, 0
//	  tech   50, 0  -> tech-post
//	   └─ go 51, 50 -> go-post
//	       └─ generics 52, 51 -> generics-post
//
//	tags     golang (term 13) -> hello-1
//	users    id 1, user_nicename "admin", 3 published posts
//
// Every expectation below is written as a literal rather than computed from
// routing.Structure, so an assertion cannot merely agree with the constructor
// the code under test calls.
const (
	restCategoryTermIDNews     = 10
	restCategoryTermIDTech     = 50
	restCategoryTermIDGo       = 51
	restCategoryTermIDGenerics = 52
	restTagTermIDGolang        = 13
)

// countingTermReadWriter is the domain.TermWriter+TermReader pair
// content.NewTermWriteService requires, counting ListByTaxonomy calls per
// taxonomy so a test can assert how many times a single REST request resolved
// the term graph (Req 10.2's once-per-request property). It delegates every
// call to the real repository, so nothing here is faked away: the counts
// describe reads that actually happened against the seeded database.
type countingTermReadWriter struct {
	domain.TermWriter
	domain.TermReader

	mu    sync.Mutex
	lists map[string]int
}

func (c *countingTermReadWriter) ListByTaxonomy(ctx context.Context, taxonomy string) ([]domain.Term, error) {
	c.mu.Lock()
	if c.lists == nil {
		c.lists = map[string]int{}
	}
	c.lists[taxonomy]++
	c.mu.Unlock()
	return c.TermReader.ListByTaxonomy(ctx, taxonomy)
}

func (c *countingTermReadWriter) reset() {
	c.mu.Lock()
	c.lists = map[string]int{}
	c.mu.Unlock()
}

func (c *countingTermReadWriter) listCount(taxonomy string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lists[taxonomy]
}

// newRESTLinkServer builds a server that serves both the wp-json term/user
// endpoints and the public archive routes, from one routing.Structure, so the
// link a REST response advertises and the path the web layer serves at 200 are
// asserted against a single configuration rather than two (Req 10.2).
//
// It returns the counting term reader as well, so a test can assert the term
// graph is resolved once per REST request rather than once per row.
func newRESTLinkServer(t *testing.T, structure, categoryBase, tagBase string) (http.Handler, *countingTermReadWriter) {
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
	if err := storagetest.SeedNestedCategories(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedNestedCategories: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}

	st, err := routing.Parse(structure, categoryBase, tagBase)
	if err != nil {
		t.Fatalf("routing.Parse(%q, %q, %q): %v", structure, categoryBase, tagBase, err)
	}

	counting := &countingTermReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader, lists: map[string]int{}}
	termWrite := content.NewTermWriteService(counting)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, cfg.TablePrefix).WithPermalinks(st)
	posts := content.NewPostService(repos.Posts).
		WithCounter(repos.PostCounter).
		WithAuthors(repos.Users)
	srv := web.NewServer(
		posts,
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithPermalinks(st).
		WithAdminWrites(nil, termWrite, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	return srv.Routes(), counting
}

// restJSONArray issues a wp-json GET against host example.test and decodes the
// response body as a JSON array of objects.
func restJSONArray(t *testing.T, h http.Handler, path string) []map[string]any {
	t.Helper()
	rec := restJSONGet(t, h, path)
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v (body %.300s)", path, err, rec.Body.String())
	}
	return out
}

// restJSONObject is restJSONArray for a single-item endpoint.
func restJSONObject(t *testing.T, h http.Handler, path string) map[string]any {
	t.Helper()
	rec := restJSONGet(t, h, path)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v (body %.300s)", path, err, rec.Body.String())
	}
	return out
}

func restJSONGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %.300s)", path, rec.Code, rec.Body.String())
	}
	return rec
}

// restTermsByID indexes a term collection response by its "id" field, so an
// assertion names the term it means rather than a position in a list whose
// order is the repository's business.
func restTermsByID(t *testing.T, terms []map[string]any) map[int64]map[string]any {
	t.Helper()
	out := make(map[int64]map[string]any, len(terms))
	for _, term := range terms {
		raw, ok := term["id"].(float64)
		if !ok {
			t.Fatalf("term has no numeric id: %+v", term)
		}
		out[int64(raw)] = term
	}
	return out
}

func termField(t *testing.T, term map[string]any, field string) any {
	t.Helper()
	v, ok := term[field]
	if !ok {
		t.Fatalf("term missing field %q: %+v", field, term)
	}
	return v
}

// assertServedAt200 requests the path component of an advertised absolute link
// back through the same router, which is what makes the link assertion a claim
// about a URL rather than about a string (Req 10.2).
func assertServedAt200(t *testing.T, h http.Handler, link string) {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	if u.RawQuery != "" {
		// A query-parameter fallback such as "/?tag=golang" would otherwise
		// pass this check by serving the home page at 200.
		t.Errorf("link %q carries a query string, want a path a route serves", link)
		return
	}
	if rec := get(t, h, u.Path); rec.Code != http.StatusOK {
		t.Errorf("GET %s = %d, want 200 (an advertised link must be the path served, not one that 301s or 404s)",
			u.Path, rec.Code)
	}
}

// TestRESTTermParentCarriesParentID covers Req 10.1: restTerm.Parent is
// hard-coded 0 today, so a REST consumer cannot see the hierarchy the archive
// routes serve. The nested chain is what makes this assertion mean something --
// against SeedFixtures alone every parent legitimately is 0.
func TestRESTTermParentCarriesParentID(t *testing.T) {
	h, _ := newRESTLinkServer(t, structDayAndName, "", "")

	wantParent := map[int64]int64{
		restCategoryTermIDNews:     0,
		restCategoryTermIDTech:     0,
		restCategoryTermIDGo:       restCategoryTermIDTech,
		restCategoryTermIDGenerics: restCategoryTermIDGo,
	}

	byID := restTermsByID(t, restJSONArray(t, h, "/wp-json/wp/v2/categories"))
	for id, want := range wantParent {
		term, ok := byID[id]
		if !ok {
			t.Fatalf("categories collection missing term %d: %+v", id, byID)
		}
		if got := termField(t, term, "parent").(float64); int64(got) != want {
			t.Errorf("collection term %d parent = %v, want %d", id, got, want)
		}
	}

	// The single-item endpoint maps through the same termToREST, but it is the
	// endpoint a consumer follows from a "parent" value, so a placeholder here
	// would make the hierarchy unwalkable even with the collection fixed.
	for id, want := range wantParent {
		term := restJSONObject(t, h, "/wp-json/wp/v2/categories/"+itoa(id))
		if got := termField(t, term, "parent").(float64); int64(got) != want {
			t.Errorf("single term %d parent = %v, want %d", id, got, want)
		}
	}
}

// TestRESTCategoryLinkIsCanonicalNestedPath covers Req 10.2: the advertised
// category link must be the canonical nested path the archive route serves at
// 200, not today's flat restAbs(r, "/category/"+slug), which 301s for every
// nested category.
func TestRESTCategoryLinkIsCanonicalNestedPath(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		want      map[int64]string
	}{
		{
			// Req 2.7: the structure's trailing slash is canonical for
			// archives too, so a link without one would 301.
			name:      "trailing-slash structure",
			structure: structDayAndName,
			want: map[int64]string{
				restCategoryTermIDNews:     "http://example.test/category/news/",
				restCategoryTermIDTech:     "http://example.test/category/tech/",
				restCategoryTermIDGo:       "http://example.test/category/tech/go/",
				restCategoryTermIDGenerics: "http://example.test/category/tech/go/generics/",
			},
		},
		{
			// Req 2.7: a flat structure's canonical archive path carries no
			// trailing slash, so a top-level category's link is byte-for-byte
			// what it is today and only the nested ones change.
			name:      "flat structure",
			structure: "",
			want: map[int64]string{
				restCategoryTermIDNews:     "http://example.test/category/news",
				restCategoryTermIDTech:     "http://example.test/category/tech",
				restCategoryTermIDGo:       "http://example.test/category/tech/go",
				restCategoryTermIDGenerics: "http://example.test/category/tech/go/generics",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newRESTLinkServer(t, tc.structure, "", "")
			byID := restTermsByID(t, restJSONArray(t, h, "/wp-json/wp/v2/categories"))
			for id, want := range tc.want {
				term, ok := byID[id]
				if !ok {
					t.Fatalf("categories collection missing term %d: %+v", id, byID)
				}
				got := termField(t, term, "link").(string)
				if got != want {
					t.Errorf("term %d link = %q, want %q", id, got, want)
				}
				assertServedAt200(t, h, got)
			}
		})
	}
}

// TestRESTCategoryLinkReflectsCategoryBase covers Req 10.2 against Req 4.1: an
// operator who renamed category_base in WordPress must see that base in the
// advertised link, because that is the base the archive route is registered at.
func TestRESTCategoryLinkReflectsCategoryBase(t *testing.T) {
	h, _ := newRESTLinkServer(t, structDayAndName, "sections", "")

	byID := restTermsByID(t, restJSONArray(t, h, "/wp-json/wp/v2/categories"))
	want := map[int64]string{
		restCategoryTermIDTech:     "http://example.test/sections/tech/",
		restCategoryTermIDGo:       "http://example.test/sections/tech/go/",
		restCategoryTermIDGenerics: "http://example.test/sections/tech/go/generics/",
	}
	for id, wantLink := range want {
		term, ok := byID[id]
		if !ok {
			t.Fatalf("categories collection missing term %d: %+v", id, byID)
		}
		got := termField(t, term, "link").(string)
		if got != wantLink {
			t.Errorf("term %d link = %q, want %q", id, got, wantLink)
		}
		assertServedAt200(t, h, got)
	}
}

// TestRESTTagLinkIsTagArchivePath covers Req 10.2 for tags: the link is
// "/?tag={slug}" today -- the fallback M9a kept only because no tag route
// existed -- and must become the tag archive path, at whatever tag_base
// resolved to (Req 4.2, 5.1).
func TestRESTTagLinkIsTagArchivePath(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		tagBase   string
		want      string
	}{
		{
			name:      "default tag base",
			structure: structDayAndName,
			tagBase:   "",
			want:      "http://example.test/tag/golang/",
		},
		{
			name:      "overridden tag base",
			structure: structDayAndName,
			tagBase:   "labels",
			want:      "http://example.test/labels/golang/",
		},
		{
			name:      "flat structure",
			structure: "",
			tagBase:   "",
			want:      "http://example.test/tag/golang",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newRESTLinkServer(t, tc.structure, "", tc.tagBase)

			byID := restTermsByID(t, restJSONArray(t, h, "/wp-json/wp/v2/tags"))
			tag, ok := byID[restTagTermIDGolang]
			if !ok {
				t.Fatalf("tags collection missing term %d: %+v", restTagTermIDGolang, byID)
			}
			got := termField(t, tag, "link").(string)
			if got != tc.want {
				t.Errorf("tag link = %q, want %q", got, tc.want)
			}
			assertServedAt200(t, h, got)

			single := restJSONObject(t, h, "/wp-json/wp/v2/tags/"+itoa(restTagTermIDGolang))
			if gotSingle := termField(t, single, "link").(string); gotSingle != tc.want {
				t.Errorf("single tag link = %q, want %q", gotSingle, tc.want)
			}
		})
	}
}

// TestRESTUserLinkIsServedAuthorArchive covers Req 7.5 and 10.3 end to end: the
// user "link" is "/?author={id}" today, a fallback M9a kept explicitly because
// no author route existed, and must become the author archive path keyed by
// user_nicename -- a path this same router serves at 200. The content-layer
// counterpart (internal/content's TestRESTUserLinkIsAuthorArchivePath) pins the
// relative form; this one pins that the absolutised form is a live URL.
func TestRESTUserLinkIsServedAuthorArchive(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		want      string
	}{
		{
			name:      "trailing-slash structure",
			structure: structDayAndName,
			want:      "http://example.test/author/admin/",
		},
		{
			name:      "flat structure",
			structure: "",
			want:      "http://example.test/author/admin",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newRESTLinkServer(t, tc.structure, "", "")

			single := restJSONObject(t, h, "/wp-json/wp/v2/users/1")
			got, ok := single["link"].(string)
			if !ok {
				t.Fatalf("user response has no string link: %+v", single)
			}
			if got != tc.want {
				t.Errorf("user link = %q, want %q", got, tc.want)
			}
			assertServedAt200(t, h, got)
		})
	}
}

// TestRESTCategoriesListingResolvesTermGraphOncePerRequest covers the
// once-per-request property Req 10.2's nested links depend on: every row of a
// /wp-json/wp/v2/categories listing needs the term graph to know its ancestry,
// and resolving it per row would turn one listing into one graph read per
// category.
//
// The bound is stated as a constant rather than as an exact count so an
// implementation may reuse the listing's own read instead of taking a second
// one, but it cannot grow with the number of rows -- which the comparison
// against the row count is what actually pins.
func TestRESTCategoriesListingResolvesTermGraphOncePerRequest(t *testing.T) {
	h, counting := newRESTLinkServer(t, structDayAndName, "", "")

	const maxTaxonomyReads = 2 // the listing itself, plus at most one graph read

	counting.reset()
	terms := restJSONArray(t, h, "/wp-json/wp/v2/categories")
	reads := counting.listCount(content.TaxonomyCategory)

	// SeedFixtures' 3 categories plus the nested chain's 3: enough rows that a
	// per-row read is unmistakable against the bound.
	if len(terms) < 6 {
		t.Fatalf("len(categories) = %d, want at least 6 (fixture problem, not a link bug): %+v", len(terms), terms)
	}
	if reads > maxTaxonomyReads {
		t.Errorf("ListByTaxonomy(%q) called %d times for a listing of %d terms, want at most %d",
			content.TaxonomyCategory, reads, len(terms), maxTaxonomyReads)
	}
	if reads >= len(terms) {
		t.Errorf("ListByTaxonomy(%q) called %d times for %d terms: the graph is being resolved per row",
			content.TaxonomyCategory, reads, len(terms))
	}
}

// TestRESTCategorySingleResolvesTermGraphOnce is the single-item counterpart:
// one request, one term, so the graph read must not multiply there either.
func TestRESTCategorySingleResolvesTermGraphOnce(t *testing.T) {
	h, counting := newRESTLinkServer(t, structDayAndName, "", "")

	counting.reset()
	term := restJSONObject(t, h, "/wp-json/wp/v2/categories/"+itoa(restCategoryTermIDGenerics))
	if got := termField(t, term, "link").(string); got != "http://example.test/category/tech/go/generics/" {
		t.Errorf("link = %q, want %q", got, "http://example.test/category/tech/go/generics/")
	}
	if reads := counting.listCount(content.TaxonomyCategory); reads > 1 {
		t.Errorf("ListByTaxonomy(%q) called %d times for one term, want at most 1",
			content.TaxonomyCategory, reads)
	}
}

// itoa keeps the request paths above readable without importing strconv into
// every assertion.
func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
