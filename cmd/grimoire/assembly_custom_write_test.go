package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// TestBuildHandlerCustomTaxonomyWrites drives term writes for the registered
// taxonomies genre (hierarchical) and badge (flat) through the production
// handler, over both the REST surface and /admin/api/terms. It is the RED test
// for tasks 5.1/5.2: until buildHandler hands the term service the registry,
// the REST routes for genre/badge are not registered, and the service has no
// vocabulary, cycle, slug or flat-taxonomy checks.
//
// Each assertion is reachable only through the buildHandler wiring named here
// (as assembly_test.go does):
//
//	assertion                                      guards
//	---------------------------------------------  ------------------------------------------
//	POST /wp-json/wp/v2/genre        -> 201        REST term routes (per
//	  with parent applied                          registered taxonomy) + content.WithTermRegistry
//	PUT  /wp-json/wp/v2/genre/{id}   -> 200        term-route wiring + WithTermRegistry (re-parent)
//	PUT  parent = descendant         -> 400        WithTermRegistry hierarchy read (ErrTermParentCycle)
//	POST duplicate sibling slug      -> 400        WithTermRegistry (ErrTermSlugTaken) mapped to
//	  code term_exists                             REST code term_exists
//	POST /wp-json/wp/v2/badge parent -> 400        WithTermRegistry flat taxonomy (ErrTermParentInvalid)
//	DELETE /wp-json/wp/v2/genre/{id} -> 200        term delete route wiring
//	POST/PUT /admin/api/terms genre  -> 201/200    isKnownAdminTaxonomy via Server registry
//	  and cycle -> 400                             (WithRegistry) + WithTermRegistry
//	no manage_categories -> 403 on both surfaces   termWrite capability gate, unchanged
//	GET /genre/{slug}/ and nested    -> 200        WithRegistry + WithCustomTypes (M12 archive)
func TestBuildHandlerCustomTaxonomyWrites(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	h := env.Handler

	type termResp struct {
		ID     int64  `json:"id"`
		Slug   string `json:"slug"`
		Parent int64  `json:"parent"`
		Code   string `json:"code"`
	}
	decode := func(rec *httptest.ResponseRecorder) termResp {
		t.Helper()
		var out termResp
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return out
	}
	want := func(rec *httptest.ResponseRecorder, status int, what string) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("%s: status = %d, want %d; body: %s", what, rec.Code, status, rec.Body.String())
		}
	}

	const restGenre = "/wp-json/wp/v2/genre"

	// Create a root and a child over REST as the administrator.
	rec := doJSON(t, h, &env.Admin, http.MethodPost, restGenre, `{"name":"Fiction","slug":"fiction"}`)
	want(rec, http.StatusCreated, "create genre root")
	root := decode(rec)

	rec = doJSON(t, h, &env.Admin, http.MethodPost, restGenre,
		fmt.Sprintf(`{"name":"Novels","slug":"novels","parent":%d}`, root.ID))
	want(rec, http.StatusCreated, "create genre child")
	child := decode(rec)
	if child.Parent != root.ID {
		t.Fatalf("child parent = %d, want %d", child.Parent, root.ID)
	}

	// Public archives for the new terms (M12 route), nested path for the child.
	for _, p := range []string{"/genre/fiction/", "/genre/fiction/novels/"} {
		want(doJSON(t, h, nil, http.MethodGet, p, ""), http.StatusOK, "GET "+p)
	}

	// Re-parent the child under a second root, then back.
	rec = doJSON(t, h, &env.Admin, http.MethodPost, restGenre, `{"name":"Poetry","slug":"poetry"}`)
	want(rec, http.StatusCreated, "create second root")
	other := decode(rec)
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("%s/%d", restGenre, child.ID),
		fmt.Sprintf(`{"parent":%d}`, other.ID))
	want(rec, http.StatusOK, "re-parent child")
	if got := decode(rec); got.Parent != other.ID {
		t.Fatalf("re-parented parent = %d, want %d", got.Parent, other.ID)
	}
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("%s/%d", restGenre, child.ID),
		fmt.Sprintf(`{"parent":%d}`, root.ID))
	want(rec, http.StatusOK, "re-parent child back")

	// Cycle: the root under its own child.
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("%s/%d", restGenre, root.ID),
		fmt.Sprintf(`{"parent":%d}`, child.ID))
	want(rec, http.StatusBadRequest, "cycle")

	// Duplicate sibling slug.
	rec = doJSON(t, h, &env.Admin, http.MethodPost, restGenre, `{"name":"Fiction 2","slug":"fiction"}`)
	want(rec, http.StatusBadRequest, "duplicate slug")
	if got := decode(rec); got.Code != "term_exists" {
		t.Fatalf("duplicate slug code = %q, want term_exists", got.Code)
	}

	// A flat taxonomy rejects any parent.
	rec = doJSON(t, h, &env.Admin, http.MethodPost, "/wp-json/wp/v2/badge",
		fmt.Sprintf(`{"name":"Staff Pick","slug":"staff-pick","parent":%d}`, root.ID))
	want(rec, http.StatusBadRequest, "badge with parent")

	// The same flows through /admin/api/terms.
	rec = doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
		fmt.Sprintf(`{"name":"Essays","slug":"essays","taxonomy":"genre","parent":%d}`, root.ID))
	want(rec, http.StatusCreated, "admin create genre child")
	adminChild := decode(rec)
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("/admin/api/terms/%d", root.ID),
		fmt.Sprintf(`{"name":"Fiction","slug":"fiction","parent":%d}`, adminChild.ID))
	want(rec, http.StatusBadRequest, "admin cycle")
	rec = doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
		`{"name":"Fiction 3","slug":"fiction","taxonomy":"genre"}`)
	want(rec, http.StatusBadRequest, "admin duplicate slug")
	rec = doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
		fmt.Sprintf(`{"name":"Gold","slug":"gold","taxonomy":"badge","parent":%d}`, root.ID))
	want(rec, http.StatusBadRequest, "admin badge with parent")

	// A user without manage_categories is refused on both surfaces.
	for _, actor := range []*customWriteActor{&env.Author, &env.Contrib, &env.Products} {
		want(doJSON(t, h, actor, http.MethodPost, restGenre, `{"name":"Nope","slug":"nope"}`),
			http.StatusForbidden, actor.Login+" REST create")
		want(doJSON(t, h, actor, http.MethodPost, "/admin/api/terms",
			`{"name":"Nope","slug":"nope","taxonomy":"genre"}`),
			http.StatusForbidden, actor.Login+" admin create")
	}

	// Delete the parent term.
	want(doJSON(t, h, &env.Admin, http.MethodDelete, fmt.Sprintf("%s/%d", restGenre, root.ID), ""),
		http.StatusOK, "delete parent")
}

// TestBuildHandlerCustomPostWrites is the RED test for tasks 11.1/11.2: custom
// post writes through the production handler, over REST and /admin/api/posts.
// Until buildHandler hands the post service the registry and node reader, every
// custom write answers 403 or 501.
//
//	assertion                                         guards
//	------------------------------------------------  -----------------------------------------
//	Products user (edit/publish_products) creates,    REST + admin custom post routes,
//	updates, deletes a product on both surfaces       content.WithPostRegistry (suffix caps)
//	plain editor (edit_posts only) -> 403 on both     suffix capability, not edit_posts
//	script in content is stripped                     contentPolicy on a registered type
//	duplicate title -> slug "-2"                      WithPostNodeReader (resolveSlug)
//	public product serves at /product/{slug}/        M12 route over a written row
//	non-public secret: writable, 404 on every         D12 (public governs routes, not writes)
//	  public route
//	delete product with terms -> count decremented    WithTermDetacher (Req 13.3)
func TestBuildHandlerCustomPostWrites(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	h := env.Handler
	type restItem struct {
		ID      int64                     `json:"id"`
		Slug    string                    `json:"slug"`
		Type    string                    `json:"type"`
		Status  string                    `json:"status"`
		Title   struct{ Rendered string } `json:"title"`
		Content struct{ Rendered string } `json:"content"`
		Deleted bool                      `json:"deleted"`
	}
	type adminItem struct {
		ID       int64  `json:"id"`
		Slug     string `json:"slug"`
		Type     string `json:"type"`
		Title    string `json:"title"`
		Content  string `json:"content"`
		Modified string `json:"modified"`
	}
	want := func(rec *httptest.ResponseRecorder, status int, what string) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("%s: status = %d, want %d; body: %s", what, rec.Code, status, rec.Body.String())
		}
	}
	rest := func(rec *httptest.ResponseRecorder) restItem {
		t.Helper()
		var out restItem
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return out
	}
	admin := func(rec *httptest.ResponseRecorder) adminItem {
		t.Helper()
		var out adminItem
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return out
	}
	const restProduct = "/wp-json/wp/v2/product"
	pu := &env.Products

	// --- REST: create, sanitize, slug, public URL, update ---
	rec := doJSON(t, h, pu, http.MethodPost, restProduct,
		`{"title":"Widget","content":"<p>hello</p><script>alert(1)</script>","status":"publish"}`)
	want(rec, http.StatusCreated, "REST create product")
	widget := rest(rec)
	if widget.Type != "product" || widget.Slug != "widget" {
		t.Fatalf("created product type/slug = %q/%q, want product/widget", widget.Type, widget.Slug)
	}
	if strings.Contains(widget.Content.Rendered, "<script") {
		t.Fatalf("content not sanitized: %q", widget.Content.Rendered)
	}
	if !strings.Contains(widget.Content.Rendered, "hello") {
		t.Fatalf("sanitized content lost the paragraph: %q", widget.Content.Rendered)
	}

	// Duplicate title gets the -2 slug.
	rec = doJSON(t, h, pu, http.MethodPost, restProduct, `{"title":"Widget","status":"publish"}`)
	want(rec, http.StatusCreated, "REST create duplicate title")
	dup := rest(rec)
	if dup.Slug != "widget-2" {
		t.Fatalf("duplicate slug = %q, want widget-2", dup.Slug)
	}

	// public: true, so the written rows serve on their public URLs.
	for _, p := range []string{"/product/widget/", "/product/widget-2/"} {
		want(doJSON(t, h, nil, http.MethodGet, p, ""), http.StatusOK, "GET "+p)
	}

	rec = doJSON(t, h, pu, http.MethodPatch, fmt.Sprintf("%s/%d", restProduct, widget.ID), `{"title":"Widget Pro"}`)
	want(rec, http.StatusOK, "REST update product")
	if got := rest(rec); got.Title.Rendered != "Widget Pro" {
		t.Fatalf("updated title = %q, want Widget Pro", got.Title.Rendered)
	}

	// --- plain editor: edit_posts is not enough, on both surfaces ---
	ed := &env.Editor
	want(doJSON(t, h, ed, http.MethodPost, restProduct, `{"title":"Nope","status":"draft"}`),
		http.StatusForbidden, "editor REST create")
	want(doJSON(t, h, ed, http.MethodPatch, fmt.Sprintf("%s/%d", restProduct, widget.ID), `{"title":"Nope"}`),
		http.StatusForbidden, "editor REST update")
	want(doJSON(t, h, ed, http.MethodDelete, fmt.Sprintf("%s/%d", restProduct, widget.ID), ""),
		http.StatusForbidden, "editor REST delete")
	want(doJSON(t, h, ed, http.MethodPost, "/admin/api/posts", `{"title":"Nope","type":"product","status":"draft"}`),
		http.StatusForbidden, "editor admin create")

	// --- admin API: create, update, delete ---
	rec = doJSON(t, h, pu, http.MethodPost, "/admin/api/posts",
		`{"title":"Gadget","type":"product","status":"publish","content":"<p>g</p><script>x()</script>"}`)
	want(rec, http.StatusCreated, "admin create product")
	gadget := admin(rec)
	if gadget.Type != "product" || gadget.Slug != "gadget" {
		t.Fatalf("admin product type/slug = %q/%q, want product/gadget", gadget.Type, gadget.Slug)
	}
	if strings.Contains(gadget.Content, "<script") {
		t.Fatalf("admin content not sanitized: %q", gadget.Content)
	}
	rec = doJSON(t, h, pu, http.MethodPut, fmt.Sprintf("/admin/api/posts/%d", gadget.ID),
		fmt.Sprintf(`{"title":"Gadget 2","type":"product","status":"publish","modified":%q}`, gadget.Modified))
	want(rec, http.StatusOK, "admin update product")
	if got := admin(rec); got.Title != "Gadget 2" {
		t.Fatalf("admin updated title = %q, want Gadget 2", got.Title)
	}
	want(doJSON(t, h, ed, http.MethodDelete, fmt.Sprintf("/admin/api/posts/%d", gadget.ID), ""),
		http.StatusForbidden, "editor admin delete")
	want(doJSON(t, h, pu, http.MethodDelete, fmt.Sprintf("/admin/api/posts/%d", gadget.ID), ""),
		http.StatusNoContent, "admin delete product")

	// --- non-public secret: writable, never routable ---
	rec = doJSON(t, h, pu, http.MethodPost, "/wp-json/wp/v2/secret", `{"title":"Hidden","status":"publish"}`)
	want(rec, http.StatusCreated, "REST create secret")
	secret := rest(rec)
	rec = doJSON(t, h, pu, http.MethodPost, "/admin/api/posts", `{"title":"Hidden Two","type":"secret","status":"publish"}`)
	want(rec, http.StatusCreated, "admin create secret")
	secret2 := admin(rec)
	rec = doJSON(t, h, pu, http.MethodPatch, fmt.Sprintf("/wp-json/wp/v2/secret/%d", secret.ID), `{"title":"Hidden Renamed"}`)
	want(rec, http.StatusOK, "REST update secret")
	for _, p := range []string{
		"/secret/hidden/", "/hidden/", "/secret/" + secret2.Slug + "/", "/" + secret2.Slug + "/",
	} {
		want(doJSON(t, h, nil, http.MethodGet, p, ""), http.StatusNotFound, "public GET "+p)
	}
	// grimoire has no ?p= routing: the secret's content must simply never leak.
	for _, p := range []string{
		fmt.Sprintf("/?p=%d", secret.ID), fmt.Sprintf("/?p=%d&post_type=secret", secret.ID),
	} {
		rec := doJSON(t, h, nil, http.MethodGet, p, "")
		if body := rec.Body.String(); strings.Contains(body, "Hidden") {
			t.Fatalf("public GET %s leaked secret content (status %d)", p, rec.Code)
		}
	}
	want(doJSON(t, h, nil, http.MethodGet, "/secret/", ""), http.StatusNotFound, "public GET /secret/")

	// --- delete a product that has terms: counts decrement (Req 13.3) ---
	rec = doJSON(t, h, pu, http.MethodPost, restProduct, `{"title":"Termed","status":"publish"}`)
	want(rec, http.StatusCreated, "REST create termed product")
	termed := rest(rec)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := env.Repos.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec("INSERT INTO "+env.Prefix+"terms (term_id, name, slug) VALUES (?, ?, ?)", 9001, "Fantasy", "fantasy")
	exec("INSERT INTO "+env.Prefix+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
		9101, 9001, "genre", "", 0, 1)
	exec("INSERT INTO "+env.Prefix+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)", termed.ID, 9101, 0)
	rec = doJSON(t, h, pu, http.MethodDelete, fmt.Sprintf("%s/%d", restProduct, termed.ID), "")
	want(rec, http.StatusOK, "REST delete termed product")
	if !rest(rec).Deleted {
		t.Fatalf("delete response not marked deleted: %s", rec.Body.String())
	}
	var count, rels int
	if err := env.Repos.DB().QueryRowContext(ctx,
		"SELECT count FROM "+env.Prefix+"term_taxonomy WHERE term_taxonomy_id = ?", 9101).Scan(&count); err != nil {
		t.Fatalf("read count: %v", err)
	}
	if count != 0 {
		t.Fatalf("term_taxonomy.count after delete = %d, want 0", count)
	}
	if err := env.Repos.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE object_id = ?", termed.ID).Scan(&rels); err != nil {
		t.Fatalf("read relationships: %v", err)
	}
	if rels != 0 {
		t.Fatalf("term_relationships rows after delete = %d, want 0", rels)
	}

	// Remaining REST delete on the first product.
	want(doJSON(t, h, pu, http.MethodDelete, fmt.Sprintf("%s/%d", restProduct, widget.ID), ""),
		http.StatusOK, "REST delete product")
	want(doJSON(t, h, nil, http.MethodGet, "/product/widget/", ""), http.StatusNotFound, "GET deleted product")
}

// customWriteDeps builds the serverDeps assemblyHandler builds, so a test can
// call buildHandler and buildScheduler itself.
func customWriteDeps(t *testing.T, env customWriteEnv, reg *registry.Registry) serverDeps {
	t.Helper()
	cfg := config.Config{
		Theme:     "default",
		Database:  config.DatabaseConfig{Vendor: "sqlite", TablePrefix: env.Prefix},
		Session:   config.SessionConfig{CookieName: "grimoire_session", TTLHours: 24},
		Media:     config.MediaConfig{UploadsDir: t.TempDir()},
		REST:      config.RESTConfig{PerPageMax: 100},
		Scheduler: config.SchedulerConfig{IntervalSeconds: 1},
	}
	themesDir := filepath.Join("..", "..", "themes")
	eng, err := render.Load(themesDir, cfg.Theme)
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	return serverDeps{
		Repos:         env.Repos,
		Config:        cfg,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Permalinks:    st,
		Registry:      reg,
		Sessions:      &auth.SessionManager{Users: env.Repos.Users, Meta: env.Repos.UserMeta, Sessions: env.Repos.Sessions, TTL: cfg.Session.TTL(), Prefix: env.Prefix},
		ContentPolicy: sanitize.New(),
		Engine:        eng,
		ThemesDir:     themesDir,
	}
}

// TestBuildSchedulerPublishesCustomTypes (task 11.1, Req 17.2, 7.6; D14): the
// scheduler returned by buildScheduler publishes a due future product row,
// which works only if it carries the registered capability suffixes. It fails
// to compile until buildScheduler exists.
func TestBuildSchedulerPublishesCustomTypes(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	deps := customWriteDeps(t, env, customWriteRegistry(t))
	_, postWrite, err := buildHandler(deps)
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	id, err := env.Repos.PostWriter.Create(ctx, domain.Post{
		Author: 1, Title: "Scheduled widget", Status: "future", Type: "product",
		Slug: "scheduled-widget", Date: past, DateGMT: past,
	})
	if err != nil {
		t.Fatalf("create future product: %v", err)
	}
	sched := buildScheduler(deps, postWrite)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go sched.Run(runCtx)
	deadline := time.Now().Add(5 * time.Second)
	var got domain.Post
	for time.Now().Before(deadline) {
		if got, err = env.Repos.PostWriter.ByID(ctx, id); err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.Status == "publish" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("future product status = %q after waiting for the scheduler, want publish", got.Status)
}
