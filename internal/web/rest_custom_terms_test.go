package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

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

// M12 Phase 12.5 -- RED tests for the REST custom-taxonomy read surface and the
// route index (design 8c, terms; Req 6.6, 10.1-10.6, 12.1, 14.5; D1, D8).
//
// Wiring mirrors cmd/grimoire (see rest_custom_posts_test.go): one Structure
// shared by the Server and the mapper, the registry on both, and a real
// content.TermWriteService on WithAdminWrites, because the CT routes are gated
// on s.termWrite != nil exactly like registerRESTTerms. Registered here:
//
//	book  (library) post type, taxonomies genre and topic
//	genre (genres)  flat taxonomy,         term 501 "scifi"
//	topic (topics)  hierarchical taxonomy, term 511 "fiction" > 512 "hard-sf"
//
// The RED today is behavioral: none of the CT routes exist, so every GET is 404.

type rctEnv struct {
	repos *storage.Repositories
	reg   *registry.Registry
	st    routing.Structure
	cfg   config.DatabaseConfig
	// listReads counts ListByTaxonomy calls per taxonomy, so "one graph read
	// per request" and "a flat taxonomy never reads a graph" are observable.
	listReads map[string]*atomic.Int64
}

const (
	rctGenre     = int64(501)
	rctFiction   = int64(511)
	rctHardSF    = int64(512)
	rctGenreTT   = int64(601)
	rctFictionTT = int64(611)
	rctHardSFTT  = int64(612)
)

type rctCountingTerms struct {
	termReadWriter
	reads map[string]*atomic.Int64
}

func (c rctCountingTerms) ListByTaxonomy(ctx context.Context, taxonomy string) ([]domain.Term, error) {
	if n, ok := c.reads[taxonomy]; ok {
		n.Add(1)
	}
	return c.TermReader.ListByTaxonomy(ctx, taxonomy)
}

func rctConfig() config.ContentConfig {
	yes := true
	return config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "book", Public: &yes, Slug: "book", RESTBase: "library", Taxonomies: []string{"genre", "topic"}},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", RESTBase: "genres", ObjectTypes: []string{"book"}},
			{Name: "topic", Public: &yes, Slug: "topic", RESTBase: "topics", Hierarchical: true, ObjectTypes: []string{"book"}},
		},
	}
}

func newRCTEnv(t *testing.T) *rctEnv {
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
	reg, err := registry.Build(rctConfig(), st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	types, taxes := reg.Routes()
	e := &rctEnv{
		repos: repos, reg: reg, st: st.WithCustomTypes(types, taxes), cfg: cfg,
		listReads: map[string]*atomic.Int64{"genre": {}, "topic": {}},
	}
	p := cfg.TablePrefix
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repos.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed sql %q: %v", q, err)
		}
	}
	for _, tm := range []struct {
		id         int64
		name, slug string
	}{{rctGenre, "Science Fiction", "scifi"}, {rctFiction, "Fiction", "fiction"}, {rctHardSF, "Hard SF", "hard-sf"}} {
		exec("INSERT INTO "+p+"terms (term_id, name, slug) VALUES (?, ?, ?)", tm.id, tm.name, tm.slug)
	}
	for _, tt := range []struct {
		ttID, termID int64
		taxonomy     string
		parent       int64
	}{
		{rctGenreTT, rctGenre, "genre", 0},
		{rctFictionTT, rctFiction, "topic", 0},
		{rctHardSFTT, rctHardSF, "topic", rctFiction}, // parent holds the parent TERM id
	} {
		exec("INSERT INTO "+p+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
			tt.ttID, tt.termID, tt.taxonomy, "", tt.parent, 0)
	}
	return e
}

// handler builds the live router. withRegistry=false builds the pre-M12 REST
// surface over the SAME database; withTermWrite=false omits WithAdminWrites.
// caps nil means an anonymous caller.
func (e *rctEnv) handler(t *testing.T, withRegistry, withTermWrite bool, caps ...string) http.Handler {
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
		if st, err = routing.Parse("/%postname%/", "", ""); err != nil {
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
		WithAdmin(admin.Handler("/admin"), adminSvc)
	if withTermWrite {
		trw := rctCountingTerms{
			termReadWriter: termReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader},
			reads:          e.listReads,
		}
		var opts []content.TermWriteOption
		if withRegistry {
			opts = append(opts, content.WithTermRegistry(e.reg))
		}
		srv = srv.WithAdminWrites(nil, content.NewTermWriteService(trw, opts...), nil, nil)
	}
	srv = srv.WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	if withRegistry {
		srv = srv.WithRegistry(e.reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	}
	return srv.Routes()
}

func (e *rctEnv) resetReads() {
	for _, n := range e.listReads {
		n.Store(0)
	}
}

func rctID(id int64) string { return strconv.FormatInt(id, 10) }

func TestRESTCustomTermsCollectionShape(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true)

	rec := rcpGet(h, "/wp-json/wp/v2/genres", false)
	rcpWantOK(t, rec, "/genres")
	items := rcpDecodeList(t, rec)
	if len(items) != 1 {
		t.Fatalf("len(genres) = %d, want 1: %v", len(items), items)
	}
	it := items[0]
	for _, key := range []string{"id", "count", "link", "name", "slug", "taxonomy", "parent"} {
		if _, ok := it[key]; !ok {
			t.Errorf("genre term lacks %q (categories/tags shape): %v", key, it)
		}
	}
	if _, ok := it["description"]; ok {
		t.Errorf("description must stay absent (Finding 12): %v", it)
	}
	if it["id"] != float64(rctGenre) || it["slug"] != "scifi" || it["name"] != "Science Fiction" {
		t.Errorf("genre term = %v, want id 501 scifi", it)
	}
	if it["taxonomy"] != "genre" {
		t.Errorf("taxonomy = %v, want genre", it["taxonomy"])
	}
	if it["count"] != float64(0) {
		t.Errorf("count = %v, want the documented literal 0", it["count"])
	}
	if it["parent"] != float64(0) {
		t.Errorf("parent = %v, want 0", it["parent"])
	}
}

func TestRESTCustomTermsSingle(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true)

	path := "/wp-json/wp/v2/genres/" + rctID(rctGenre)
	rec := rcpGet(h, path, false)
	rcpWantOK(t, rec, path)
	m := rcpDecodeObj(t, rec)
	if m["id"] != float64(rctGenre) || m["taxonomy"] != "genre" || m["slug"] != "scifi" {
		t.Errorf("single = %v, want genre term 501", m)
	}
}

func TestRESTCustomTermsOtherTaxonomyIs404(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true)

	cases := []string{
		"/wp-json/wp/v2/genres/" + rctID(rctFiction), // a topic term via /genres
		"/wp-json/wp/v2/topics/" + rctID(rctGenre),   // a genre term via /topics
		"/wp-json/wp/v2/genres/999999",               // nonexistent
	}
	for _, path := range cases {
		rec := rcpGet(h, path, false)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404: %s", path, rec.Code, rec.Body.String())
		} else if got := decodeRESTErrCode(t, rec); got != "rest_term_invalid_id" {
			t.Errorf("GET %s error code = %q, want rest_term_invalid_id (an unrouted 404 is rest_no_route)", path, got)
		}
	}
}

func TestRESTCustomTermsLinkFlat(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true)

	want := rcpHost + e.st.CustomTermPath("genre", []string{"scifi"})
	if want == rcpHost {
		t.Fatal("fixture error: CustomTermPath(genre) is empty")
	}
	path := "/wp-json/wp/v2/genres/" + rctID(rctGenre)
	rec := rcpGet(h, path, false)
	rcpWantOK(t, rec, path)
	if got := rcpDecodeObj(t, rec)["link"]; got != want {
		t.Errorf("link = %v, want %q (CustomTermPath, flat one-element slug)", got, want)
	}

	// A flat taxonomy never reads a graph: the collection costs its one row read.
	e.resetReads()
	rcpWantOK(t, rcpGet(h, "/wp-json/wp/v2/genres", false), "/genres")
	if got := e.listReads["genre"].Load(); got != 1 {
		t.Errorf("flat collection: %d ListByTaxonomy reads, want 1 (rows only, no graph)", got)
	}
}

func TestRESTCustomTermsHierarchicalLinkAndParent(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true)

	want := rcpHost + e.st.CustomTermPath("topic", []string{"fiction", "hard-sf"})
	if !strings.Contains(want, "fiction/hard-sf") {
		t.Fatalf("fixture error: nested path %q", want)
	}

	e.resetReads()
	path := "/wp-json/wp/v2/topics/" + rctID(rctHardSF)
	rec := rcpGet(h, path, false)
	rcpWantOK(t, rec, path)
	m := rcpDecodeObj(t, rec)
	if m["link"] != want {
		t.Errorf("single link = %v, want %q (nested ancestry)", m["link"], want)
	}
	if m["parent"] != float64(rctFiction) {
		t.Errorf("parent = %v, want %d (Term.ParentID)", m["parent"], rctFiction)
	}
	if got := e.listReads["topic"].Load(); got != 1 {
		t.Errorf("single: %d graph reads, want exactly 1 per request", got)
	}

	// Collection: one row read plus at most one graph read, regardless of rows.
	e.resetReads()
	rec = rcpGet(h, "/wp-json/wp/v2/topics", false)
	rcpWantOK(t, rec, "/topics")
	links := map[string]string{}
	for _, it := range rcpDecodeList(t, rec) {
		links[it["slug"].(string)], _ = it["link"].(string)
	}
	if links["hard-sf"] != want {
		t.Errorf("collection link for hard-sf = %q, want %q", links["hard-sf"], want)
	}
	if wantTop := rcpHost + e.st.CustomTermPath("topic", []string{"fiction"}); links["fiction"] != wantTop {
		t.Errorf("collection link for fiction = %q, want %q", links["fiction"], wantTop)
	}
	if got := e.listReads["topic"].Load(); got > 2 {
		t.Errorf("collection: %d ListByTaxonomy reads for %d terms, want at most 2 (rows + one graph)", got, len(links))
	}
}

func TestRESTCustomTermsNotRegisteredAtTagsOrCategories(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true)

	// The restTermBase default trap: an unknown taxonomy must not fall through
	// to /tags. /tags/{genre id} and /categories/{genre id} stay 404.
	for _, path := range []string{
		"/wp-json/wp/v2/tags/" + rctID(rctGenre),
		"/wp-json/wp/v2/categories/" + rctID(rctGenre),
		"/wp-json/wp/v2/tags/" + rctID(rctFiction),
	} {
		if rec := rcpGet(h, path, false); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	for _, path := range []string{"/wp-json/wp/v2/tags", "/wp-json/wp/v2/categories"} {
		rec := rcpGet(h, path, false)
		rcpWantOK(t, rec, path)
		for _, it := range rcpDecodeList(t, rec) {
			if tax := it["taxonomy"]; tax == "genre" || tax == "topic" {
				t.Errorf("GET %s lists a %v term: %v", path, tax, it)
			}
		}
	}
}

// TestRESTCustomTermsBuiltinsByteIdentical is a regression guard: with the
// registry wired, category and post_tag REST JSON equals the output of the same
// database served by the pre-M12 wiring.
func TestRESTCustomTermsBuiltinsByteIdentical(t *testing.T) {
	e := newRCTEnv(t)
	with := e.handler(t, true, true)
	without := e.handler(t, false, true)

	rec := rcpGet(without, "/wp-json/wp/v2/categories", false)
	rcpWantOK(t, rec, "/categories")
	cats := rcpDecodeList(t, rec)
	if len(cats) == 0 {
		t.Fatal("fixture error: no categories seeded")
	}
	paths := []string{
		"/wp-json/wp/v2/categories",
		"/wp-json/wp/v2/tags",
		"/wp-json/wp/v2/categories/" + rctID(int64(cats[0]["id"].(float64))),
	}
	for _, path := range paths {
		a, b := rcpGet(with, path, false), rcpGet(without, path, false)
		if a.Code != http.StatusOK || b.Code != http.StatusOK {
			t.Fatalf("GET %s = %d / %d, want 200 / 200", path, a.Code, b.Code)
		}
		if a.Body.String() != b.Body.String() {
			t.Errorf("GET %s differs with the registry wired:\nwith:    %s\nwithout: %s", path, a.Body.String(), b.Body.String())
		}
	}
}

func TestRESTCustomTermsWritesAre501AndWriteNothing(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true, "manage_categories", "edit_posts", "edit_terms", "delete_terms")

	count := func() int {
		var n int
		if err := e.repos.DB().QueryRow("SELECT COUNT(*) FROM " + e.cfg.TablePrefix + "terms").Scan(&n); err != nil {
			t.Fatalf("count terms: %v", err)
		}
		return n
	}
	before := count()
	coll := "/wp-json/wp/v2/genres"
	single := coll + "/" + rctID(rctGenre)
	// M17 3.2: create/update/delete are live; only these verbs stay 501.
	cases := []struct{ method, path, code string }{
		{http.MethodPost, single, "rest_cannot_create"},
		{http.MethodPut, coll, "rest_cannot_edit"},
		{http.MethodPatch, coll, "rest_cannot_edit"},
		{http.MethodDelete, coll, "rest_cannot_edit"},
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
		t.Errorf("terms rows %d -> %d; a 501 write must change nothing", before, after)
	}
	terms, err := e.repos.TermReader.TermsByIDs(context.Background(), []int64{rctGenre})
	if err != nil || len(terms) != 1 || terms[0].Slug != "scifi" {
		t.Errorf("genre term after writes = %+v, %v; want untouched", terms, err)
	}
}

// TestRESTCustomTermsGatedOnTermWrite: like registerRESTTerms, the CT routes
// are registered only when s.termWrite != nil.
func TestRESTCustomTermsGatedOnTermWrite(t *testing.T) {
	e := newRCTEnv(t)

	without := e.handler(t, true, false)
	rec := rcpGet(without, "/wp-json/wp/v2/genres", false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /genres without termWrite = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if got := decodeRESTErrCode(t, rec); got != "rest_no_route" {
		t.Errorf("error code = %q, want rest_no_route (route unregistered)", got)
	}

	with := e.handler(t, true, true)
	rcpWantOK(t, rcpGet(with, "/wp-json/wp/v2/genres", false), "/genres")
}

type rctIndex struct {
	Routes map[string]struct {
		Methods []string `json:"methods"`
	} `json:"routes"`
}

func rctRoutes(t *testing.T, h http.Handler, path string) map[string][]string {
	t.Helper()
	rec := rcpGet(h, path, false)
	rcpWantOK(t, rec, path)
	var idx rctIndex
	if err := json.Unmarshal(rec.Body.Bytes(), &idx); err != nil {
		t.Fatalf("decode %s index: %v", path, err)
	}
	out := make(map[string][]string, len(idx.Routes))
	for k, v := range idx.Routes {
		out[k] = v.Methods
	}
	return out
}

func TestRESTRouteIndexListsRegisteredTypesAndTaxonomies(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true)

	wantGet := []string{http.MethodGet}
	want := []string{
		"/wp/v2/library", "/wp/v2/library/(?P<id>[\\d]+)",
		"/wp/v2/genres", "/wp/v2/genres/(?P<id>[\\d]+)",
		"/wp/v2/topics", "/wp/v2/topics/(?P<id>[\\d]+)",
	}
	for _, index := range []string{"/wp-json/", "/wp-json/wp/v2/"} {
		routes := rctRoutes(t, h, index)
		for _, route := range want {
			got, ok := routes[route]
			if !ok {
				t.Errorf("GET %s: routes lacks %q", index, route)
				continue
			}
			// M17 3.2: registered taxonomies also list their write methods.
			wantGet := wantGet
			if strings.Contains(route, "/genres") || strings.Contains(route, "/topics") {
				wantGet = []string{http.MethodGet, http.MethodPost}
				if strings.Contains(route, "(?P<id>") {
					wantGet = []string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete}
				}
			}
			if !reflect.DeepEqual(got, wantGet) {
				t.Errorf("GET %s: routes[%q] methods = %v, want %v (write verbs are 501)", index, route, got, wantGet)
			}
		}
		for _, bad := range []string{"/wp/v2/types", "/wp/v2/taxonomies"} {
			if _, ok := routes[bad]; ok {
				t.Errorf("GET %s: routes lists %q, which is not served (Req 10.6)", index, bad)
			}
		}
	}
	for _, path := range []string{"/wp-json/wp/v2/types", "/wp-json/wp/v2/taxonomies"} {
		if rec := rcpGet(h, path, false); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (not served, Req 10.6)", path, rec.Code)
		}
	}
}

// TestRESTRouteIndexUnchangedWithoutRegistry: the registry only ADDS entries.
// Without one the index is the pre-M12 restRoutes() output, and with one every
// pre-existing entry is untouched.
func TestRESTRouteIndexUnchangedWithoutRegistry(t *testing.T) {
	e := newRCTEnv(t)
	without := rctRoutes(t, e.handler(t, false, true), "/wp-json/wp/v2/")
	with := rctRoutes(t, e.handler(t, true, true), "/wp-json/wp/v2/")

	for _, route := range []string{"/wp/v2/library", "/wp/v2/genres", "/wp/v2/topics"} {
		if _, ok := without[route]; ok {
			t.Errorf("no-registry index lists %q", route)
		}
	}
	for _, route := range []string{"/wp/v2/posts", "/wp/v2/pages", "/wp/v2/categories", "/wp/v2/tags", "/wp/v2/media"} {
		if _, ok := without[route]; !ok {
			t.Errorf("no-registry index lacks built-in %q", route)
		}
	}
	stripped := map[string][]string{}
	for k, v := range with {
		switch {
		case strings.HasPrefix(k, "/wp/v2/library"), strings.HasPrefix(k, "/wp/v2/genres"), strings.HasPrefix(k, "/wp/v2/topics"):
		default:
			stripped[k] = v
		}
	}
	if !reflect.DeepEqual(stripped, without) {
		t.Errorf("with-registry index minus custom entries = %v, want the no-registry index %v", stripped, without)
	}
	// The top-level index agrees with the namespace index.
	if top := rctRoutes(t, e.handler(t, false, true), "/wp-json/"); !reflect.DeepEqual(top, without) {
		t.Errorf("GET /wp-json/ routes = %v, want %v", top, without)
	}
}
