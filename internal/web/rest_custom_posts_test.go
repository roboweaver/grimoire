package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
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

// M12 Phase 12.3 -- RED tests for the REST custom-post-type read surface
// (design 8c, posts; Req 8.1, 8.2, 8.4, 8.5, 8.6, 9.1-9.6, 12.1, 14.5; D1, D7, D8).
//
// Wiring mirrors cmd/grimoire: one parsed Structure (with the registry's custom
// routes) shared by the Server and the REST mapper, the registry on both the
// mapper (WithRegistry) and the Server (WithRegistry), and a CustomPostService
// over a counting PostNodeReader so "one graph read per request" is observable.
// Registered post types (rest_base in parentheses):
//
//	book   (library)  flat, public, taxonomy genre (rest_base genres)
//	volume (volumes)  hierarchical, public
//	event  (events)   capability_type "event" -> edit_events
//
// The RED today is behavioral: none of these routes exist, so every GET is 404.

const rcpHost = "http://example.test"

type rcpEnv struct {
	repos *storage.Repositories
	reg   *registry.Registry
	st    routing.Structure
	cfg   config.DatabaseConfig
	reads *atomic.Int64 // ListPostNodes walks started (afterID == 0)

	dune, draftBook, vol1, partOne, publishedEvent, draftEvent int64
}

type countingNodes struct {
	inner domain.PostNodeReader
	reads *atomic.Int64
}

func (c countingNodes) ListPostNodes(ctx context.Context, postType string, afterID int64, limit int) ([]domain.PostNode, error) {
	if afterID == 0 {
		c.reads.Add(1)
	}
	return c.inner.ListPostNodes(ctx, postType, afterID, limit)
}

func rcpConfig() config.ContentConfig {
	yes := true
	return config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "book", Public: &yes, Slug: "book", RESTBase: "library", Taxonomies: []string{"genre"}},
			{Name: "volume", Public: &yes, Slug: "volume", RESTBase: "volumes", Hierarchical: true},
			{Name: "event", Public: &yes, Slug: "event", RESTBase: "events", CapabilityType: "event"},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", RESTBase: "genres", ObjectTypes: []string{"book"}},
		},
	}
}

func newRCPEnv(t *testing.T) *rcpEnv {
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
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	reg, err := registry.Build(rcpConfig(), st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	types, taxes := reg.Routes()
	e := &rcpEnv{repos: repos, reg: reg, st: st.WithCustomTypes(types, taxes), cfg: cfg, reads: &atomic.Int64{}}
	e.seed(t, ctx)
	return e
}

func (e *rcpEnv) seed(t *testing.T, ctx context.Context) {
	t.Helper()
	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	n := 0
	mk := func(typ, slug, status string, parent int64) int64 {
		t.Helper()
		n++
		id, err := e.repos.PostWriter.Create(ctx, domain.Post{
			Author: 1, Date: base.Add(time.Duration(n) * time.Hour), Content: "<p>body " + slug + "</p>",
			Title: "T-" + slug, Excerpt: "ex", Status: status, Slug: slug, Type: typ,
			CommentStatus: "closed", ParentID: parent,
		})
		if err != nil {
			t.Fatalf("create %s/%s: %v", typ, slug, err)
		}
		return id
	}
	e.dune = mk("book", "dune", "publish", 0)
	mk("book", "neuromancer", "publish", 0)
	e.draftBook = mk("book", "unpublished", "draft", 0)
	e.vol1 = mk("volume", "vol-1", "publish", 0)
	e.partOne = mk("volume", "part-one", "publish", e.vol1)
	e.publishedEvent = mk("event", "launch", "publish", 0)
	e.draftEvent = mk("event", "secret-launch", "draft", 0)

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.repos.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed sql %q: %v", q, err)
		}
	}
	p := e.cfg.TablePrefix
	exec("INSERT INTO "+p+"terms (term_id, name, slug) VALUES (?, ?, ?)", 501, "Science Fiction", "scifi")
	exec("INSERT INTO "+p+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
		601, 501, "genre", "", 0, 0)
	exec("INSERT INTO "+p+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)", e.dune, 601, 0)
}

// handler builds the live router. withRegistry=false builds the pre-M12 REST
// surface over the SAME database. caps nil means an anonymous caller.
func (e *rcpEnv) handler(t *testing.T, withRegistry bool, caps ...string) http.Handler {
	t.Helper()
	repos := e.repos
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	st := e.st
	if !withRegistry {
		var err error
		st, err = routing.Parse("/%postname%/", "", "")
		if err != nil {
			t.Fatalf("routing.Parse: %v", err)
		}
	}
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, "wp_").WithPermalinks(st)
	if withRegistry {
		mapper = mapper.WithRegistry(e.reg)
	}
	fake := &fakeSessions{}
	if len(caps) > 0 {
		capSet := map[string]bool{}
		for _, c := range caps {
			capSet[c] = true
		}
		fake.authPrincipal = auth.Principal{UserID: 1, Login: "u", Roles: []string{"author"}, Caps: capSet}
		fake.authSession = domain.Session{ID: "s1", UserID: 1, CSRFToken: "tok"}
	} else {
		fake.authErr = auth.ErrInvalidCredentials
	}
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithPermalinks(st).
		WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	if withRegistry {
		srv = srv.WithRegistry(e.reg, content.NewCustomPostService(countingNodes{inner: repos.PostNodeReader, reads: e.reads}, repos.Posts))
	}
	return srv.Routes()
}

func rcpDo(h http.Handler, method, path string, authed bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{"title":"x","status":"publish"}`))
	req.Host = "example.test"
	if authed {
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		req.Header.Set("X-CSRF-Token", "tok")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rcpGet(h http.Handler, path string, authed bool) *httptest.ResponseRecorder {
	return rcpDo(h, http.MethodGet, path, authed)
}

func rcpDecodeObj(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode object %q: %v", rec.Body.String(), err)
	}
	return m
}

func rcpDecodeList(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var l []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatalf("decode list %q: %v", rec.Body.String(), err)
	}
	return l
}

func rcpWantOK(t *testing.T, rec *httptest.ResponseRecorder, path string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
	}
}

func TestRESTCustomPostsCollectionEnvelopeAndHeaders(t *testing.T) {
	e := newRCPEnv(t)
	h := e.handler(t, true)

	rec := rcpGet(h, "/wp-json/wp/v2/library?per_page=1", false)
	rcpWantOK(t, rec, "/library")
	items := rcpDecodeList(t, rec)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1 (per_page=1)", len(items))
	}
	// Two published books (draft excluded): total 2 over 2 pages.
	if got := rec.Header().Get("X-WP-Total"); got != "2" {
		t.Errorf("X-WP-Total = %q, want 2", got)
	}
	if got := rec.Header().Get("X-WP-TotalPages"); got != "2" {
		t.Errorf("X-WP-TotalPages = %q, want 2", got)
	}
	for _, key := range []string{"id", "date", "slug", "status", "type", "link", "title", "content", "excerpt", "author", "_links"} {
		if _, ok := items[0][key]; !ok {
			t.Errorf("collection item lacks %q (posts/pages envelope): %v", key, items[0])
		}
	}
	if items[0]["type"] != "book" {
		t.Errorf("type = %v, want book", items[0]["type"])
	}
}

func TestRESTCustomPostsSlugFilter(t *testing.T) {
	e := newRCPEnv(t)
	h := e.handler(t, true)

	rec := rcpGet(h, "/wp-json/wp/v2/library?slug=dune", false)
	rcpWantOK(t, rec, "/library?slug=dune")
	items := rcpDecodeList(t, rec)
	if len(items) != 1 || items[0]["slug"] != "dune" {
		t.Fatalf("slug=dune items = %v, want exactly dune", items)
	}
	rec = rcpGet(h, "/wp-json/wp/v2/library?slug=unpublished", false)
	rcpWantOK(t, rec, "/library?slug=unpublished")
	if items := rcpDecodeList(t, rec); len(items) != 0 {
		t.Errorf("draft slug returned %v, want empty", items)
	}
}

func TestRESTCustomPostsSingleTypeLinkAndTermArrays(t *testing.T) {
	e := newRCPEnv(t)
	h := e.handler(t, true)

	path := "/wp-json/wp/v2/library/" + strconv.FormatInt(e.dune, 10)
	rec := rcpGet(h, path, false)
	rcpWantOK(t, rec, path)
	m := rcpDecodeObj(t, rec)
	if m["type"] != "book" {
		t.Errorf("type = %v, want book", m["type"])
	}
	wantLink := rcpHost + e.st.CustomPostPath("book", []string{"dune"})
	if wantLink == rcpHost {
		t.Fatal("fixture error: CustomPostPath(book) is empty")
	}
	if m["link"] != wantLink {
		t.Errorf("link = %v, want %q (CustomPostPath, never the dated/post canonical)", m["link"], wantLink)
	}
	genres, ok := m["genres"].([]any)
	if !ok || len(genres) != 1 || genres[0] != float64(501) {
		t.Errorf("genres = %v, want [501] keyed by the taxonomy rest_base", m["genres"])
	}
	for _, bad := range []string{"categories", "tags", "genre"} {
		if _, ok := m[bad]; ok {
			t.Errorf("unexpected key %q in a custom post (term arrays are keyed by rest_base only)", bad)
		}
	}
}

func TestRESTCustomPostsHierarchicalLinkOneGraphRead(t *testing.T) {
	e := newRCPEnv(t)
	h := e.handler(t, true)

	wantChild := rcpHost + e.st.CustomPostPath("volume", []string{"vol-1", "part-one"})
	if !strings.Contains(wantChild, "vol-1/part-one") {
		t.Fatalf("fixture error: nested path %q", wantChild)
	}

	e.reads.Store(0)
	path := "/wp-json/wp/v2/volumes/" + strconv.FormatInt(e.partOne, 10)
	rec := rcpGet(h, path, false)
	rcpWantOK(t, rec, path)
	if got := rcpDecodeObj(t, rec)["link"]; got != wantChild {
		t.Errorf("single link = %v, want %q", got, wantChild)
	}
	if got := e.reads.Load(); got != 1 {
		t.Errorf("single: %d graph reads, want exactly 1", got)
	}

	e.reads.Store(0)
	rec = rcpGet(h, "/wp-json/wp/v2/volumes", false)
	rcpWantOK(t, rec, "/volumes")
	links := map[string]string{}
	for _, it := range rcpDecodeList(t, rec) {
		links[it["slug"].(string)], _ = it["link"].(string)
	}
	if links["part-one"] != wantChild {
		t.Errorf("collection link for part-one = %q, want %q", links["part-one"], wantChild)
	}
	if got := e.reads.Load(); got != 1 {
		t.Errorf("collection: %d graph reads for %d items, want exactly 1 per request", got, len(links))
	}

	// A flat type never walks the graph.
	e.reads.Store(0)
	rcpGet(h, "/wp-json/wp/v2/library", false)
	if got := e.reads.Load(); got != 0 {
		t.Errorf("flat type: %d graph reads, want 0", got)
	}
}

func TestRESTCustomPostsOtherTypeRowIs404(t *testing.T) {
	e := newRCPEnv(t)
	h := e.handler(t, true)

	// Post 3 (hello-3) and page 5 (about) are fixtures of other types.
	for _, id := range []int64{3, 5, e.vol1} {
		path := "/wp-json/wp/v2/library/" + strconv.FormatInt(id, 10)
		rec := rcpGet(h, path, true)
		// Even a caller with every capability must not see another type's row.
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		} else if got := decodeRESTErrCode(t, rec); got != "rest_post_invalid_id" {
			t.Errorf("GET %s error code = %q, want rest_post_invalid_id (an unrouted 404 is rest_no_route)", path, got)
		}
	}
}

func TestRESTCustomPostsUnpublishedVisibility(t *testing.T) {
	e := newRCPEnv(t)
	bookPath := "/wp-json/wp/v2/library/" + strconv.FormatInt(e.draftBook, 10)
	eventPath := "/wp-json/wp/v2/events/" + strconv.FormatInt(e.draftEvent, 10)

	cases := []struct {
		name   string
		caps   []string
		path   string
		want   int
		wantCd string
	}{
		{"anonymous book", nil, bookPath, 404, "rest_post_invalid_id"},
		{"anonymous event", nil, eventPath, 404, "rest_post_invalid_id"},
		{"authenticated without edit_posts, book", []string{"read"}, bookPath, 404, "rest_post_invalid_id"},
		{"authenticated without edit_events, event", []string{"read"}, eventPath, 404, "rest_post_invalid_id"},
		// edit_posts is NOT edit_events: a non-default capability_type is honored.
		{"edit_posts is not edit_events", []string{"edit_posts"}, eventPath, 404, "rest_post_invalid_id"},
		{"edit_posts holder, default capability_type book", []string{"edit_posts"}, bookPath, 200, ""},
		{"edit_events holder, event", []string{"edit_events"}, eventPath, 200, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := e.handler(t, true, tc.caps...)
			rec := rcpGet(h, tc.path, len(tc.caps) > 0)
			if rec.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d: %s", tc.path, rec.Code, tc.want, rec.Body.String())
			}
			if tc.wantCd != "" {
				if got := decodeRESTErrCode(t, rec); got != tc.wantCd {
					t.Errorf("error code = %q, want %q", got, tc.wantCd)
				}
			}
		})
	}
}

func TestRESTCustomPostsPublishedVisibleToEveryone(t *testing.T) {
	e := newRCPEnv(t)
	path := "/wp-json/wp/v2/events/" + strconv.FormatInt(e.publishedEvent, 10)
	for _, caps := range [][]string{nil, {"read"}, {"edit_events"}} {
		h := e.handler(t, true, caps...)
		rcpWantOK(t, rcpGet(h, path, len(caps) > 0), path)
	}
}

func TestRESTCustomPostsWritesAre501AndWriteNothing(t *testing.T) {
	e := newRCPEnv(t)
	h := e.handler(t, true, "edit_posts", "edit_events", "publish_posts", "publish_events", "delete_posts", "edit_published_posts")

	count := func() int {
		var n int
		if err := e.repos.DB().QueryRow("SELECT COUNT(*) FROM " + e.cfg.TablePrefix + "posts").Scan(&n); err != nil {
			t.Fatalf("count posts: %v", err)
		}
		return n
	}
	before := count()
	single := "/wp-json/wp/v2/library/" + strconv.FormatInt(e.dune, 10)
	coll := "/wp-json/wp/v2/library"
	cases := []struct{ method, path, code string }{
		{http.MethodPost, coll, "rest_cannot_create"},
		{http.MethodPut, coll, "rest_cannot_edit"},
		{http.MethodPatch, coll, "rest_cannot_edit"},
		{http.MethodDelete, coll, "rest_cannot_edit"},
		{http.MethodPut, single, "rest_cannot_edit"},
		{http.MethodPatch, single, "rest_cannot_edit"},
		{http.MethodDelete, single, "rest_cannot_edit"},
		{http.MethodPost, single, "rest_cannot_edit"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := rcpDo(h, tc.method, tc.path, true)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s %s = %d, want 501: %s", tc.method, tc.path, rec.Code, rec.Body.String())
			}
			if got := decodeRESTErrCode(t, rec); got != tc.code {
				t.Errorf("error code = %q, want %q", got, tc.code)
			}
		})
	}
	if after := count(); after != before {
		t.Errorf("posts rows %d -> %d; a 501 write must change nothing", before, after)
	}
	if p, err := e.repos.PostWriter.ByID(context.Background(), e.dune); err != nil || p.Title != "T-dune" || p.Status != "publish" {
		t.Errorf("dune after writes = %+v, %v; want untouched", p, err)
	}
}

func TestRESTCustomPostsNoPluralization(t *testing.T) {
	e := newRCPEnv(t)
	h := e.handler(t, true)

	rcpWantOK(t, rcpGet(h, "/wp-json/wp/v2/library", false), "/library")
	for _, bad := range []string{"/wp-json/wp/v2/librarys", "/wp-json/wp/v2/books", "/wp-json/wp/v2/book"} {
		if rec := rcpGet(h, bad, false); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (rest_base is used verbatim, never typ+\"s\")", bad, rec.Code)
		}
	}
}

// TestRESTCustomPostsBuiltinsByteIdentical is a regression guard: with the
// registry wired, post and page REST JSON equals the output of the same
// database served by the pre-M12 wiring (no registry on Server or mapper).
func TestRESTCustomPostsBuiltinsByteIdentical(t *testing.T) {
	e := newRCPEnv(t)
	with := e.handler(t, true)
	without := e.handler(t, false)
	for _, path := range []string{
		"/wp-json/wp/v2/posts",
		"/wp-json/wp/v2/posts/3",
		"/wp-json/wp/v2/posts?slug=hello-3",
		"/wp-json/wp/v2/pages",
		"/wp-json/wp/v2/pages/5",
		"/wp-json/wp/v2/posts/3?_embed=1",
	} {
		a, b := rcpGet(with, path, false), rcpGet(without, path, false)
		if a.Code != http.StatusOK || b.Code != http.StatusOK {
			t.Fatalf("GET %s = %d / %d, want 200 / 200", path, a.Code, b.Code)
		}
		if a.Body.String() != b.Body.String() {
			t.Errorf("GET %s differs with the registry wired:\nwith:    %s\nwithout: %s", path, a.Body.String(), b.Body.String())
		}
	}
}
