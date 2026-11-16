package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// M12c Phase 9.1 -- RED tests for REST custom post writes (Req 6.1-6.7, 7.1,
// 7.7, 8.1-8.3, 9.2, 9.3, 13.1; D1, D2, D17).
//
// Registry fixture (rest_base in parentheses):
//
//	product (products)  flat, public, capability_type "product"
//	guide   (guides)    hierarchical, public, capability_type "guide"
//
// RED today: every write verb on a registered type answers 501.
//
// WIRING SEAM (task 9.2 owner): rcpwPostWriteOpts is the single place the
// PostWriteService learns about the registry. WithPostRegistry,
// WithPostNodeReader and WithTermDetacher are added by task 8.2 and do not
// compile yet, so they are listed in the comment below rather than called.
// Once 8.2 lands, return:
//
//	content.WithPostRegistry(reg), content.WithPostNodeReader(repos.PostNodeReader),
//	content.WithTermDetacher(repos.PostTermsWriter)
//
// Without them the service keeps the legacy suffix and the capability and slug
// assertions below cannot pass.
func rcpwPostWriteOpts(reg *registry.Registry, repos *storage.Repositories) []content.PostWriteOption {
	return []content.PostWriteOption{
		content.WithPostRegistry(reg),
		content.WithPostNodeReader(repos.PostNodeReader),
		content.WithTermDetacher(repos.PostTermsWriter),
	}
}

const rcpwTimeGMT = "Mon, 01 Jan 2001 00:00:00 GMT"

type rcpwEnv struct {
	repos *storage.Repositories
	reg   *registry.Registry
	st    routing.Structure
	cfg   config.DatabaseConfig
	// seeded ids
	existing  int64 // published product "existing"
	plainPost int64 // built-in post, to prove wrong-type ids 404
	guideID   int64
}

func rcpwConfig() config.ContentConfig {
	yes := true
	return config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "product", Public: &yes, Slug: "product", RESTBase: "products", CapabilityType: "product"},
			{Name: "guide", Public: &yes, Slug: "guide", RESTBase: "guides", Hierarchical: true, CapabilityType: "guide"},
		},
	}
}

// rcpwCaps lists the product capability family a "full" holder has.
var rcpwFullCaps = []string{
	"edit_products", "edit_others_products", "edit_published_products", "publish_products",
	"delete_products", "delete_others_products", "delete_published_products",
}

func rcpwCapSet(extra ...string) map[string]bool {
	m := map[string]bool{}
	for _, c := range append(append([]string{}, rcpwFullCaps...), extra...) {
		m[c] = true
	}
	return m
}

func newRCPWEnv(t *testing.T) *rcpwEnv {
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
	reg, err := registry.Build(rcpwConfig(), st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	types, taxes := reg.Routes()
	e := &rcpwEnv{repos: repos, reg: reg, st: st.WithCustomTypes(types, taxes), cfg: cfg}
	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	mk := func(typ, slug, status string) int64 {
		t.Helper()
		id, err := repos.PostWriter.Create(ctx, domain.Post{
			Author: 1, Date: base, Content: "<p>body " + slug + "</p>", Title: "T-" + slug,
			Status: status, Slug: slug, Type: typ, CommentStatus: "closed",
		})
		if err != nil {
			t.Fatalf("create %s/%s: %v", typ, slug, err)
		}
		return id
	}
	e.existing = mk("product", "existing", "publish")
	e.plainPost = mk("post", "taken", "publish") // a post owning the slug "taken"
	e.guideID = mk("guide", "g-one", "publish")
	return e
}

func (e *rcpwEnv) server(t *testing.T, withWrites bool, fake *fakeSessions) *web.Server {
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
		WithRegistry(e.reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	if withWrites {
		pw := content.NewPostWriteService(repos.PostWriter, rcpwPostWriteOpts(e.reg, repos)...)
		srv = srv.WithAdminWrites(pw, nil, nil, nil)
	}
	return srv
}

// handler returns a router whose session principal holds caps. withWrites=false
// leaves postWrite nil.
func (e *rcpwEnv) handler(t *testing.T, withWrites bool, caps map[string]bool) http.Handler {
	t.Helper()
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "u", Roles: []string{"author"}, Caps: caps},
		authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: "tok"},
	}
	return e.server(t, withWrites, fake).Routes()
}

func rcpwDo(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "example.test"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "tok")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rcpwWant(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if code != "" {
		if got := decodeRESTErrCode(t, rec); got != code {
			t.Fatalf("error code = %q, want %q", got, code)
		}
	}
}

func rcpwObj(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return m
}

func rcpwPath(id int64) string { return "/wp-json/wp/v2/products/" + strconv.FormatInt(id, 10) }

func rcpwStored(t *testing.T, e *rcpwEnv, id int64) domain.Post {
	t.Helper()
	p, err := e.repos.PostWriter.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("ByID(%d): %v", id, err)
	}
	return p
}

// Req 6.1: create returns 201, type "product", and the shape GET serves.
func TestRESTCustomPostWriteCreate(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())
	rec := rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"Widget","content":"<p>hi</p>","status":"draft"}`, nil)
	rcpwWant(t, rec, http.StatusCreated, "")
	got := rcpwObj(t, rec)
	if got["type"] != "product" {
		t.Errorf("type = %v, want product", got["type"])
	}
	id := int64(got["id"].(float64))
	if id == 0 {
		t.Fatalf("missing id: %v", got)
	}
	// The create response has the same keys a GET of the row serves.
	get := rcpwDo(h, http.MethodGet, rcpwPath(id), "", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET after create = %d: %s", get.Code, get.Body.String())
	}
	gotGet := rcpwObj(t, get)
	for k := range gotGet {
		if _, ok := got[k]; !ok {
			t.Errorf("create response lacks key %q that GET serves", k)
		}
	}
	if st := rcpwStored(t, e, id); st.Type != "product" {
		t.Errorf("stored type = %q, want product", st.Type)
	}
}

// Req 7.7 and 7.1: the product family gates writes, not edit_posts.
func TestRESTCustomPostWriteCapabilities(t *testing.T) {
	e := newRCPWEnv(t)
	body := `{"title":"Widget","status":"draft"}`

	t.Run("edit_posts without edit_products is forbidden", func(t *testing.T) {
		h := e.handler(t, true, map[string]bool{"edit_posts": true, "publish_posts": true})
		rcpwWant(t, rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products", body, nil), http.StatusForbidden, "rest_cannot_create")
	})
	t.Run("publish needs publish_products", func(t *testing.T) {
		h := e.handler(t, true, map[string]bool{"edit_products": true, "publish_posts": true})
		rcpwWant(t, rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"W","status":"publish"}`, nil),
			http.StatusForbidden, "rest_cannot_create")
		// the same caller may still save a draft
		rcpwWant(t, rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products", body, nil), http.StatusCreated, "")
	})
	t.Run("publish with publish_products", func(t *testing.T) {
		h := e.handler(t, true, rcpwCapSet())
		rcpwWant(t, rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products", `{"title":"W","status":"publish"}`, nil), http.StatusCreated, "")
	})
	t.Run("update and delete use the stored row's family", func(t *testing.T) {
		h := e.handler(t, true, map[string]bool{"edit_posts": true, "delete_posts": true})
		rcpwWant(t, rcpwDo(h, http.MethodPut, rcpwPath(e.existing), `{"title":"X"}`, nil), http.StatusForbidden, "rest_cannot_edit")
		rcpwWant(t, rcpwDo(h, http.MethodDelete, rcpwPath(e.existing), "", nil), http.StatusForbidden, "rest_cannot_edit")
		if got := rcpwStored(t, e, e.existing); got.Title != "T-existing" {
			t.Errorf("forbidden write changed the row: %q", got.Title)
		}
	})
}

// Req 6.2: PUT/PATCH partial update and If-Unmodified-Since conflict.
func TestRESTCustomPostWriteUpdate(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())
	for _, m := range []string{http.MethodPut, http.MethodPatch} {
		rec := rcpwDo(h, m, rcpwPath(e.existing), `{"title":"Renamed `+m+`"}`, nil)
		rcpwWant(t, rec, http.StatusOK, "")
		got := rcpwStored(t, e, e.existing)
		if got.Title != "Renamed "+m {
			t.Errorf("%s title = %q", m, got.Title)
		}
		if got.Content != "<p>body existing</p>" || got.Status != "publish" || got.Slug != "existing" {
			t.Errorf("%s partial update clobbered fields: content=%q status=%q slug=%q", m, got.Content, got.Status, got.Slug)
		}
	}
	rec := rcpwDo(h, http.MethodPut, rcpwPath(e.existing), `{"title":"Stale"}`,
		map[string]string{"If-Unmodified-Since": rcpwTimeGMT})
	rcpwWant(t, rec, http.StatusConflict, "rest_conflict")
}

// Req 6.3: wrong-type and unknown ids 404 before the capability check.
func TestRESTCustomPostWriteInvalidID(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, map[string]bool{}) // no capability at all
	for _, id := range []int64{e.plainPost, e.guideID, 999999} {
		for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			rec := rcpwDo(h, m, rcpwPath(id), `{"title":"x"}`, nil)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s id %d = %d, want 404: %s", m, id, rec.Code, rec.Body.String())
				continue
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_post_invalid_id" {
				t.Errorf("%s id %d code = %q", m, id, code)
			}
		}
	}
}

// Req 6.4: validation is identical to /posts.
func TestRESTCustomPostWriteValidation(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())
	past := time.Now().Add(-48 * time.Hour).UTC().Format("2006-01-02T15:04:05")
	cases := []struct{ name, body string }{
		{"invalid status", `{"title":"W","status":"bogus"}`},
		{"missing title", `{"status":"publish"}`},
		{"future status with past date", `{"title":"W","status":"future","date":"` + past + `"}`},
		{"malformed body", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rcpwWant(t, rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products", tc.body, nil), http.StatusBadRequest, "rest_invalid_param")
		})
	}
}

// Req 6.5: collection-level PUT/PATCH/DELETE and POST /{id} stay 501.
func TestRESTCustomPostWriteCollectionVerbsStay501(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())
	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, "/wp-json/wp/v2/products"},
		{http.MethodPatch, "/wp-json/wp/v2/products"},
		{http.MethodDelete, "/wp-json/wp/v2/products"},
		{http.MethodPost, rcpwPath(e.existing)},
	} {
		if rec := rcpwDo(h, tc.method, tc.path, `{"title":"x"}`, nil); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501: %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// Req 6.6: a session needs the CSRF token; an Application Password does not.
func TestRESTCustomPostWriteAuthModes(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())

	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/products", strings.NewReader(`{"title":"W","status":"draft"}`))
	req.Host = "example.test"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	rcpwWant(t, rec, http.StatusForbidden, "rest_forbidden")

	// Application Password: user 1 holds the product family via its capabilities row.
	ctx := context.Background()
	raw, err := auth.SerializeCapabilities(append([]string{auth.RoleAdministrator}, rcpwFullCaps...)...)
	if err != nil {
		t.Fatalf("SerializeCapabilities: %v", err)
	}
	if err := e.repos.UserMeta.Set(ctx, 1, e.cfg.TablePrefix+"capabilities", raw); err != nil {
		t.Fatalf("seed capabilities: %v", err)
	}
	ap := &auth.ApplicationPasswords{Users: e.repos.Users, Meta: e.repos.UserMeta, Prefix: e.cfg.TablePrefix}
	_, secret, err := ap.Create(ctx, 1, "rcpw")
	if err != nil {
		t.Fatalf("ApplicationPasswords.Create: %v", err)
	}
	apH := e.server(t, true, &fakeSessions{authErr: auth.ErrInvalidCredentials}).Routes()
	areq := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/products", strings.NewReader(`{"title":"W","status":"draft"}`))
	areq.Host = "example.test"
	areq.Header.Set("Content-Type", "application/json")
	areq.SetBasicAuth("admin", secret)
	arec := httptest.NewRecorder()
	apH.ServeHTTP(arec, areq)
	rcpwWant(t, arec, http.StatusCreated, "")
}

// Req 6.7: the route index advertises the write methods.
func TestRESTCustomPostWriteRouteIndex(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())
	rec := rcpwDo(h, http.MethodGet, "/wp-json/wp/v2", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("index = %d: %s", rec.Code, rec.Body.String())
	}
	var idx struct {
		Routes map[string]struct {
			Methods []string `json:"methods"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &idx); err != nil {
		t.Fatalf("decode: %v", err)
	}
	has := func(route string, want ...string) {
		t.Helper()
		got := map[string]bool{}
		for _, m := range idx.Routes[route].Methods {
			got[m] = true
		}
		for _, m := range want {
			if !got[m] {
				t.Errorf("route %s lacks %s (has %v)", route, m, idx.Routes[route].Methods)
			}
		}
	}
	has("/wp/v2/products", "GET", "POST")
	has(`/wp/v2/products/(?P<id>[\d]+)`, "GET", "PUT", "PATCH", "DELETE")
	has("/wp/v2/guides", "GET", "POST")

	// With no post-write service the index stays read-only.
	ro := e.handler(t, false, rcpwCapSet())
	rec = rcpwDo(ro, http.MethodGet, "/wp-json/wp/v2", "", nil)
	var roIdx struct {
		Routes map[string]struct {
			Methods []string `json:"methods"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &roIdx); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range roIdx.Routes["/wp/v2/products"].Methods {
		if m == "POST" {
			t.Errorf("read-only index advertises POST for products")
		}
	}
}

// Req 13.1: DELETE answers 200 {deleted, previous}, ignores ?force, removes the row.
func TestRESTCustomPostWriteDelete(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())
	rec := rcpwDo(h, http.MethodDelete, rcpwPath(e.existing)+"?force=false", "", nil)
	rcpwWant(t, rec, http.StatusOK, "")
	got := rcpwObj(t, rec)
	if got["deleted"] != true {
		t.Errorf("deleted = %v, want true", got["deleted"])
	}
	prev, _ := got["previous"].(map[string]any)
	if prev == nil || prev["type"] != "product" {
		t.Errorf("previous = %v, want the product object", got["previous"])
	}
	if _, err := e.repos.PostWriter.ByID(context.Background(), e.existing); err == nil {
		t.Errorf("row %d still exists after DELETE ?force=false", e.existing)
	}
}

// Req 8.1, 8.2: sanitization through the REST path by tier.
func TestRESTCustomPostWriteSanitization(t *testing.T) {
	e := newRCPWEnv(t)
	dirty := `<p onclick="x()">hi</p><script>alert(1)</script>`
	bodyJSON, _ := json.Marshal(map[string]string{"title": "Sani", "content": dirty, "status": "draft"})

	author := e.handler(t, true, rcpwCapSet())
	rec := rcpwDo(author, http.MethodPost, "/wp-json/wp/v2/products", string(bodyJSON), nil)
	rcpwWant(t, rec, http.StatusCreated, "")
	got := rcpwStored(t, e, int64(rcpwObj(t, rec)["id"].(float64)))
	if strings.Contains(got.Content, "<script") || strings.Contains(got.Content, "onclick") {
		t.Errorf("author content not sanitized: %q", got.Content)
	}

	editor := e.handler(t, true, rcpwCapSet("unfiltered_html"))
	rec = rcpwDo(editor, http.MethodPost, "/wp-json/wp/v2/products", string(bodyJSON), nil)
	rcpwWant(t, rec, http.StatusCreated, "")
	got = rcpwStored(t, e, int64(rcpwObj(t, rec)["id"].(float64)))
	if got.Content != dirty {
		t.Errorf("unfiltered_html content altered: %q, want byte-identical %q", got.Content, dirty)
	}

	// Update path re-sanitizes for the author.
	rec = rcpwDo(author, http.MethodPatch, rcpwPath(e.existing), string(bodyJSON), nil)
	rcpwWant(t, rec, http.StatusOK, "")
	if c := rcpwStored(t, e, e.existing).Content; strings.Contains(c, "<script") {
		t.Errorf("author update not sanitized: %q", c)
	}
}

// Req 9.3: a colliding slug among siblings of the same type gets -2. A post
// that owns the same slug is not a sibling of a flat product.
func TestRESTCustomPostWriteSlugCollision(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())
	create := func(body string) domain.Post {
		t.Helper()
		rec := rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products", body, nil)
		rcpwWant(t, rec, http.StatusCreated, "")
		return rcpwStored(t, e, int64(rcpwObj(t, rec)["id"].(float64)))
	}
	first := create(`{"title":"Widget","status":"draft"}`)
	second := create(`{"title":"Widget","status":"draft"}`)
	if first.Slug != "widget" || second.Slug != "widget-2" {
		t.Errorf("derived slugs = %q, %q; want widget, widget-2", first.Slug, second.Slug)
	}
	// Explicit slug colliding with the sibling product "existing".
	if got := create(`{"title":"Other","slug":"existing","status":"draft"}`); got.Slug != "existing-2" {
		t.Errorf("explicit colliding slug = %q, want existing-2", got.Slug)
	}
	// The post "taken" lives in another type's scope, so it is no collision.
	if got := create(`{"title":"Other","slug":"taken","status":"draft"}`); got.Slug != "taken" {
		t.Errorf("slug owned by a post = %q, want taken (flat type scopes by type)", got.Slug)
	}
	// Updating a row keeps its own slug.
	rcpwWant(t, rcpwDo(h, http.MethodPatch, rcpwPath(e.existing), `{"slug":"existing","title":"Same"}`, nil), http.StatusOK, "")
	if got := rcpwStored(t, e, e.existing).Slug; got != "existing" {
		t.Errorf("own slug on update = %q, want existing", got)
	}
}

// Req 9.2: an invalid slug charset is rejected.
func TestRESTCustomPostWriteInvalidSlug(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcpwCapSet())
	for _, slug := range []string{"Has Space", "UPPER", "bad/slash", "emoji-\U0001F600"} {
		body, _ := json.Marshal(map[string]string{"title": "W", "slug": slug, "status": "draft"})
		rcpwWant(t, rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products", string(body), nil), http.StatusBadRequest, "rest_invalid_param")
	}
	rcpwWant(t, rcpwDo(h, http.MethodPatch, rcpwPath(e.existing), `{"slug":"Not Valid"}`, nil), http.StatusBadRequest, "rest_invalid_param")
}

// With no post-write service every verb stays 501.
func TestRESTCustomPostWriteNilPostWriteStays501(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, false, rcpwCapSet())
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/wp-json/wp/v2/products"},
		{http.MethodPut, rcpwPath(e.existing)},
		{http.MethodPatch, rcpwPath(e.existing)},
		{http.MethodDelete, rcpwPath(e.existing)},
	} {
		if rec := rcpwDo(h, tc.method, tc.path, `{"title":"x"}`, nil); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501", tc.method, tc.path, rec.Code)
		}
	}
}
