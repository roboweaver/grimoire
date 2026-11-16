package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

// M12c Phase 16.1 -- RED tests for term assignment on REST custom post writes
// (Req 12.1, 12.2, 12.3, 12.5, 12.6, 12.7; D10).
//
// Registry fixture:
//
//	product (products) flat, taxonomies: genre
//	guide   (guides)   flat, taxonomies: badge
//	genre   (genre)    flat taxonomy attached to product; terms 9101, 9102
//	badge   (badge)    flat taxonomy attached to guide only; term 9103
//
// RED today: the REST write handlers ignore the "genre" key, and
// content.WithPostTermsRegistry / content.WithPostTermsReader (task 15.2) do
// not exist, so this file does not compile until they land.
//
// Helper prefix: rcpt.

const (
	rcptGenreA = int64(9101)
	rcptGenreB = int64(9102)
	rcptBadge  = int64(9103)
)

type rcptEnv struct {
	repos *storage.Repositories
	reg   *registry.Registry
	st    routing.Structure
	cfg   config.DatabaseConfig
	// existing is a published product used by the update tests.
	existing int64
}

func rcptConfig() config.ContentConfig {
	yes := true
	return config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "product", Public: &yes, Slug: "product", RESTBase: "products", CapabilityType: "product", Taxonomies: []string{"genre"}},
			{Name: "guide", Public: &yes, Slug: "guide", RESTBase: "guides", CapabilityType: "guide", Taxonomies: []string{"badge"}},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", RESTBase: "genre", ObjectTypes: []string{"product"}},
			{Name: "badge", Public: &yes, Slug: "badge", RESTBase: "badge", ObjectTypes: []string{"guide"}},
		},
	}
}

func newRCPTEnv(t *testing.T) *rcptEnv {
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
		taxonomy   string
		ttID       int64
	}{
		{rcptGenreA, "Sci-Fi", "rcpt-scifi", "genre", 9201},
		{rcptGenreB, "Noir", "rcpt-noir", "genre", 9202},
		{rcptBadge, "Staff Pick", "rcpt-pick", "badge", 9203},
	} {
		exec("INSERT INTO "+p+"terms (term_id, name, slug) VALUES (?, ?, ?)", tm.id, tm.name, tm.slug)
		exec("INSERT INTO "+p+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
			tm.ttID, tm.id, tm.taxonomy, "", 0, 0)
	}
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	reg, err := registry.Build(rcptConfig(), st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	types, taxes := reg.Routes()
	e := &rcptEnv{repos: repos, reg: reg, st: st.WithCustomTypes(types, taxes), cfg: cfg}
	id, err := repos.PostWriter.Create(ctx, domain.Post{
		Author: 1, Date: time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), Content: "<p>body</p>", Title: "T-existing",
		Status: "publish", Slug: "existing", Type: "product", CommentStatus: "closed",
	})
	if err != nil {
		t.Fatalf("create existing product: %v", err)
	}
	e.existing = id
	return e
}

func (e *rcptEnv) handler(t *testing.T, caps map[string]bool) http.Handler {
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
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, e.cfg.TablePrefix).
		WithPermalinks(e.st).WithRegistry(e.reg)
	ap := &auth.ApplicationPasswords{Users: repos.Users, Meta: repos.UserMeta, Prefix: e.cfg.TablePrefix}
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "u", Roles: []string{"author"}, Caps: caps},
		authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: "tok"},
	}
	pw := content.NewPostWriteService(repos.PostWriter,
		content.WithPostRegistry(e.reg),
		content.WithPostNodeReader(repos.PostNodeReader),
		content.WithTermDetacher(repos.PostTermsWriter),
	)
	ptw := content.NewPostTermsWriteService(repos.PostWriter, repos.PostTermsWriter,
		content.WithPostTermsRegistry(e.reg),
		content.WithPostTermsReader(repos.TermReader),
	)
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithPermalinks(e.st).
		WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(ap, false, "").
		WithRegistry(e.reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts)).
		WithAdminWrites(pw, nil, ptw, nil)
	return srv.Routes()
}

func rcptDo(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "example.test"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "tok")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rcptWant(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if code != "" {
		if got := decodeRESTErrCode(t, rec); got != code {
			t.Fatalf("error code = %q, want %q: %s", got, code, rec.Body.String())
		}
	}
}

// rcptCaps is the product capability family plus the post floor.
func rcptCaps() map[string]bool {
	m := map[string]bool{}
	for _, c := range []string{
		"edit_products", "edit_others_products", "edit_published_products", "publish_products",
		"delete_products", "delete_others_products", "delete_published_products",
		"edit_posts",
	} {
		m[c] = true
	}
	return m
}

// rcptAssigned returns the sorted term ids assigned to postID in taxonomy,
// read straight from SQL.
func rcptAssigned(t *testing.T, e *rcptEnv, postID int64, taxonomy string) []int64 {
	t.Helper()
	rows, err := e.repos.DB().QueryContext(context.Background(),
		"SELECT tt.term_id FROM wp_term_relationships tr JOIN wp_term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id "+
			"WHERE tr.object_id = ? AND tt.taxonomy = ? ORDER BY tt.term_id", postID, taxonomy)
	if err != nil {
		t.Fatalf("query assigned: %v", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func rcptTermCount(t *testing.T, e *rcptEnv, termID int64) int {
	t.Helper()
	var n int
	if err := e.repos.DB().QueryRowContext(context.Background(),
		"SELECT count FROM wp_term_taxonomy WHERE term_id = ?", termID).Scan(&n); err != nil {
		t.Fatalf("term count: %v", err)
	}
	return n
}

func rcptRows(t *testing.T, e *rcptEnv, typ string) int {
	t.Helper()
	var n int
	if err := e.repos.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM wp_posts WHERE post_type = ?", typ).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func rcptIDList(t *testing.T, v any) []int64 {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("value %v (%T) is not an array", v, v)
	}
	out := make([]int64, 0, len(arr))
	for _, x := range arr {
		f, _ := x.(float64)
		out = append(out, int64(f))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func rcptEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Req 12.1, 12.6: create assigns both ids, maintains count, and the response
// lists them under the rest_base key.
func TestRESTCustomPostTermsCreateAssigns(t *testing.T) {
	e := newRCPTEnv(t)
	h := e.handler(t, rcptCaps())
	rec := rcptDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"Widget","status":"draft","genre":[9101,9102]}`)
	rcptWant(t, rec, http.StatusCreated, "")
	got := rcpwObj(t, rec)
	id := int64(got["id"].(float64))
	if ids := rcptIDList(t, got["genre"]); !rcptEqual(ids, []int64{rcptGenreA, rcptGenreB}) {
		t.Errorf("response genre = %v, want [9101 9102]", got["genre"])
	}
	if ids := rcptAssigned(t, e, id, "genre"); !rcptEqual(ids, []int64{rcptGenreA, rcptGenreB}) {
		t.Errorf("stored genre terms = %v, want [9101 9102]", ids)
	}
	for _, tid := range []int64{rcptGenreA, rcptGenreB} {
		if n := rcptTermCount(t, e, tid); n != 1 {
			t.Errorf("term %d count = %d, want 1", tid, n)
		}
	}
}

// Req 12.1: update replaces the set exactly.
func TestRESTCustomPostTermsUpdateReplaces(t *testing.T) {
	e := newRCPTEnv(t)
	h := e.handler(t, rcptCaps())
	path := "/wp-json/wp/v2/products/" + strconv.FormatInt(e.existing, 10)
	rcptWant(t, rcptDo(h, http.MethodPatch, path, `{"genre":[9101,9102]}`), http.StatusOK, "")
	rcptWant(t, rcptDo(h, http.MethodPatch, path, `{"genre":[9102]}`), http.StatusOK, "")
	if ids := rcptAssigned(t, e, e.existing, "genre"); !rcptEqual(ids, []int64{rcptGenreB}) {
		t.Errorf("genre after replace = %v, want [9102]", ids)
	}
	if n := rcptTermCount(t, e, rcptGenreA); n != 0 {
		t.Errorf("dropped term count = %d, want 0", n)
	}
}

// Req 12.1: PATCH with an empty array clears the taxonomy.
func TestRESTCustomPostTermsPatchEmptyClears(t *testing.T) {
	e := newRCPTEnv(t)
	h := e.handler(t, rcptCaps())
	path := "/wp-json/wp/v2/products/" + strconv.FormatInt(e.existing, 10)
	rcptWant(t, rcptDo(h, http.MethodPatch, path, `{"genre":[9101,9102]}`), http.StatusOK, "")
	rec := rcptDo(h, http.MethodPatch, path, `{"genre":[]}`)
	rcptWant(t, rec, http.StatusOK, "")
	if ids := rcptAssigned(t, e, e.existing, "genre"); len(ids) != 0 {
		t.Errorf("genre after clear = %v, want none", ids)
	}
	for _, tid := range []int64{rcptGenreA, rcptGenreB} {
		if n := rcptTermCount(t, e, tid); n != 0 {
			t.Errorf("term %d count = %d, want 0", tid, n)
		}
	}
	if ids := rcptIDList(t, rcpwObj(t, rec)["genre"]); len(ids) != 0 {
		t.Errorf("response genre = %v, want empty", ids)
	}
}

// Req 12.2: a non-array or non-positive value is 400 and nothing is written.
func TestRESTCustomPostTermsMalformedValue(t *testing.T) {
	e := newRCPTEnv(t)
	h := e.handler(t, rcptCaps())
	before := rcptRows(t, e, "product")
	for _, v := range []string{`"9101"`, `9101`, `{"a":1}`, `[0]`, `[-3]`, `[1.5]`, `["x"]`, `[9101,0]`, `null`} {
		rec := rcptDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"Bad","status":"draft","genre":`+v+`}`)
		rcptWant(t, rec, http.StatusBadRequest, "rest_invalid_param")
	}
	if after := rcptRows(t, e, "product"); after != before {
		t.Errorf("product rows = %d, want %d (nothing written)", after, before)
	}
	// Update: the same rejection leaves the row and its terms untouched.
	path := "/wp-json/wp/v2/products/" + strconv.FormatInt(e.existing, 10)
	rcptWant(t, rcptDo(h, http.MethodPatch, path, `{"title":"Changed","genre":[0]}`), http.StatusBadRequest, "rest_invalid_param")
	if got := rcpwStored(t, &rcpwEnv{repos: e.repos}, e.existing); got.Title != "T-existing" {
		t.Errorf("rejected update changed the title to %q", got.Title)
	}
}

// Req 12.3: an id from another taxonomy, or no term at all, is 400 before the
// post exists.
func TestRESTCustomPostTermsForeignOrMissingID(t *testing.T) {
	e := newRCPTEnv(t)
	h := e.handler(t, rcptCaps())
	before := rcptRows(t, e, "product")
	for _, v := range []string{`[9103]`, `[9101,9103]`, `[999999]`} {
		rec := rcptDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"Bad","status":"draft","genre":`+v+`}`)
		rcptWant(t, rec, http.StatusBadRequest, "rest_invalid_param")
	}
	if after := rcptRows(t, e, "product"); after != before {
		t.Errorf("product rows = %d, want %d (no row created)", after, before)
	}
	for _, tid := range []int64{rcptGenreA, rcptBadge} {
		if n := rcptTermCount(t, e, tid); n != 0 {
			t.Errorf("term %d count = %d, want 0", tid, n)
		}
	}
	// Update: rejected before the write, so the row is unchanged.
	path := "/wp-json/wp/v2/products/" + strconv.FormatInt(e.existing, 10)
	rcptWant(t, rcptDo(h, http.MethodPatch, path, `{"title":"Changed","genre":[9103]}`), http.StatusBadRequest, "rest_invalid_param")
	if got := rcpwStored(t, &rcpwEnv{repos: e.repos}, e.existing); got.Title != "T-existing" {
		t.Errorf("rejected update changed the title to %q", got.Title)
	}
}

// A key for a taxonomy not attached to the type is not a recognized key: it is
// ignored, even with a malformed value.
func TestRESTCustomPostTermsUnattachedKeyIgnored(t *testing.T) {
	e := newRCPTEnv(t)
	h := e.handler(t, rcptCaps())
	for _, v := range []string{`[9103]`, `"junk"`} {
		rec := rcptDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"Ignored","status":"draft","badge":`+v+`}`)
		rcptWant(t, rec, http.StatusCreated, "")
		got := rcpwObj(t, rec)
		if _, ok := got["badge"]; ok {
			t.Errorf("response carries unattached key badge: %v", got["badge"])
		}
		if ids := rcptAssigned(t, e, int64(got["id"].(float64)), "badge"); len(ids) != 0 {
			t.Errorf("badge assigned to a product: %v", ids)
		}
	}
	if n := rcptTermCount(t, e, rcptBadge); n != 0 {
		t.Errorf("badge term count = %d, want 0", n)
	}
}

// Req 12.5: capability is checked before any term read: a caller without the
// type's edit capability gets 403 even when the ids are invalid.
func TestRESTCustomPostTermsForbiddenBeforeTermRead(t *testing.T) {
	e := newRCPTEnv(t)
	h := e.handler(t, map[string]bool{"edit_posts": true, "manage_categories": true})
	before := rcptRows(t, e, "product")
	rcptWant(t, rcptDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"No","status":"draft","genre":[999999]}`),
		http.StatusForbidden, "rest_cannot_create")
	rcptWant(t, rcptDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"No","status":"draft","genre":"bad"}`),
		http.StatusForbidden, "rest_cannot_create")
	path := "/wp-json/wp/v2/products/" + strconv.FormatInt(e.existing, 10)
	rcptWant(t, rcptDo(h, http.MethodPatch, path, `{"genre":[9101]}`), http.StatusForbidden, "rest_cannot_edit")
	if after := rcptRows(t, e, "product"); after != before {
		t.Errorf("product rows = %d, want %d", after, before)
	}
	if ids := rcptAssigned(t, e, e.existing, "genre"); len(ids) != 0 {
		t.Errorf("forbidden update assigned terms: %v", ids)
	}
}

// Req 12.7: the same body on /posts leaves built-in behavior unchanged; the
// genre key is ignored.
func TestRESTCustomPostTermsIgnoredOnBuiltinPosts(t *testing.T) {
	e := newRCPTEnv(t)
	h := e.handler(t, rcptCaps())
	for _, v := range []string{`[9101]`, `"not-an-array"`} {
		rec := rcptDo(h, http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"Plain","status":"draft","genre":`+v+`}`)
		rcptWant(t, rec, http.StatusCreated, "")
		id := int64(rcpwObj(t, rec)["id"].(float64))
		if ids := rcptAssigned(t, e, id, "genre"); len(ids) != 0 {
			t.Errorf("genre assigned to a built-in post: %v", ids)
		}
	}
	if n := rcptTermCount(t, e, rcptGenreA); n != 0 {
		t.Errorf("genre term count = %d, want 0", n)
	}
}
