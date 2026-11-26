package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// M12c Phase 10.1 -- RED tests for custom-post-type writes through the admin
// JSON API (/admin/api/posts) (Req 7.7, 8, 9, 10.1-10.6; D13).
//
// Registered here: product (capability_type "product" -> suffix "products").
// The live content.PostWriteService is built with content.WithPostRegistry and
// content.WithPostNodeReader, so the registered-type capability, sanitization
// and slug rules apply. The RED today: content.WithPostRegistry /
// content.WithPostNodeReader do not exist (compile failure); once stubbed,
// `product` answers 400 `type must be "post" or "page"`.
//
// Helper prefix: apc.

const apcCSRF = "apc-csrf-token"

// apcProductCaps is the product capability family a shop manager holds.
var apcProductCaps = []string{
	"edit_products", "edit_others_products", "edit_published_products",
	"publish_products", "delete_products", "delete_others_products",
	"delete_published_products",
}

func apcSessions(userID int64, keys ...string) *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(userID, "apc-user", keys),
		authSession:   domain.Session{ID: "apc-s", UserID: userID, CSRFToken: apcCSRF},
	}
}

// apcShopManager: editor (edit_posts floor, unfiltered_html) plus the product family.
func apcShopManager() *fakeSessions {
	return apcSessions(1, append([]string{auth.RoleEditor}, apcProductCaps...)...)
}

func apcConfig() config.ContentConfig {
	yes := true
	return config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "product", Public: &yes, Slug: "product", RESTBase: "products", CapabilityType: "product"},
		},
	}
}

// newAPCRouter builds the full router over a migrated, seeded SQLite store.
// withRegistry=false wires the legacy (no registry) service and server.
func newAPCRouter(t *testing.T, fake *fakeSessions, withRegistry bool) (http.Handler, *storage.Repositories) {
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
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	var reg *registry.Registry
	opts := []content.PostWriteOption{}
	if withRegistry {
		reg, err = registry.Build(apcConfig(), st)
		if err != nil {
			t.Fatalf("registry.Build: %v", err)
		}
		opts = append(opts, content.WithPostRegistry(reg), content.WithPostNodeReader(repos.PostNodeReader))
	}
	postWrite := content.NewPostWriteService(repos.PostWriter, opts...)
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(postWrite, nil, nil, nil)
	if withRegistry {
		srv = srv.WithRegistry(reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	}
	return srv.Routes(), repos
}

func apcDo(t *testing.T, h http.Handler, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-CSRF-Token", apcCSRF)
	}
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "session-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func apcWant(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, want, rec.Body.String())
	}
}

func apcDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	return m
}

func apcID(t *testing.T, m map[string]any) int64 {
	t.Helper()
	f, _ := m["id"].(float64)
	if f == 0 {
		t.Fatalf("no id in response: %v", m)
	}
	return int64(f)
}

// apcRow reads the stored row straight from SQL; ok=false when absent.
func apcRow(t *testing.T, repos *storage.Repositories, id int64) (typ, slug, title, body string, ok bool) {
	t.Helper()
	row := repos.DB().QueryRowContext(context.Background(),
		"SELECT post_type, post_name, post_title, post_content FROM wp_posts WHERE ID = ?", id)
	if err := row.Scan(&typ, &slug, &title, &body); err != nil {
		return "", "", "", "", false
	}
	return typ, slug, title, body, true
}

func apcCountType(t *testing.T, repos *storage.Repositories, typ string) int {
	t.Helper()
	var n int
	if err := repos.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM wp_posts WHERE post_type = ?", typ).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", typ, err)
	}
	return n
}

func apcCreateProduct(t *testing.T, h http.Handler, body string) map[string]any {
	t.Helper()
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts", body, true)
	apcWant(t, rec, http.StatusCreated)
	return apcDecode(t, rec)
}

// Req 10.1, 10.4: create a registered type -> 201, stored post_type = product,
// response carries the stored type.
func TestAdminPostCustomCreate(t *testing.T) {
	h, repos := newAPCRouter(t, apcShopManager(), true)
	m := apcCreateProduct(t, h, `{"title":"Blue Widget","type":"product","status":"draft"}`)
	if m["type"] != "product" {
		t.Fatalf("response type = %v, want product", m["type"])
	}
	typ, _, _, _, ok := apcRow(t, repos, apcID(t, m))
	if !ok || typ != "product" {
		t.Fatalf("stored post_type = %q (found=%v), want product", typ, ok)
	}
}

// Req 10.2: unregistered type -> 400. With a registry the request is rejected
// and nothing is written; with no registry the message is unchanged.
func TestAdminPostCustomUnknownType(t *testing.T) {
	h, repos := newAPCRouter(t, apcShopManager(), true)
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts", `{"title":"X","type":"nonsense"}`, true)
	apcWant(t, rec, http.StatusBadRequest)
	if n := apcCountType(t, repos, "nonsense"); n != 0 {
		t.Fatalf("unknown type wrote %d rows", n)
	}

	h2, _ := newAPCRouter(t, apcShopManager(), false)
	rec = apcDo(t, h2, http.MethodPost, "/admin/api/posts", `{"title":"X","type":"product"}`, true)
	apcWant(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), `type must be \"post\" or \"page\"`) {
		t.Fatalf("no-registry message changed: %s", rec.Body.String())
	}
}

// Req 10.6: PUT with a differing type keeps the stored type.
func TestAdminPostCustomUpdateKeepsStoredType(t *testing.T) {
	h, repos := newAPCRouter(t, apcShopManager(), true)
	m := apcCreateProduct(t, h, `{"title":"Blue Widget","type":"product","status":"draft"}`)
	id := apcID(t, m)
	put := `{"title":"Blue Widget v2","type":"page","status":"draft","modified":"` + m["modified"].(string) + `"}`
	rec := apcDo(t, h, http.MethodPut, "/admin/api/posts/"+apcItoa(id), put, true)
	apcWant(t, rec, http.StatusOK)
	if got := apcDecode(t, rec)["type"]; got != "product" {
		t.Fatalf("response type = %v, want product", got)
	}
	typ, _, title, _, _ := apcRow(t, repos, id)
	if typ != "product" || title != "Blue Widget v2" {
		t.Fatalf("stored type/title = %q/%q, want product/Blue Widget v2", typ, title)
	}
}

// Req 10.3: DELETE of a registered type's row -> 204 and the row is gone.
func TestAdminPostCustomDelete(t *testing.T) {
	h, repos := newAPCRouter(t, apcShopManager(), true)
	id := apcID(t, apcCreateProduct(t, h, `{"title":"Gone","type":"product","status":"draft"}`))
	rec := apcDo(t, h, http.MethodDelete, "/admin/api/posts/"+apcItoa(id)+"?force=true", "", true)
	apcWant(t, rec, http.StatusNoContent)
	if _, _, _, _, ok := apcRow(t, repos, id); ok {
		t.Fatal("row still present after DELETE")
	}
}

// Req 7.7: edit_posts without the product family -> 403 on create, update and
// delete; nothing is written or removed.
func TestAdminPostCustomForbiddenWithoutProductCaps(t *testing.T) {
	// Seed a product as the shop manager, then act as a plain editor on the
	// same database by reusing the file through a second router is not
	// possible, so seed through the privileged router and swap the session
	// principal in place.
	fake := apcShopManager()
	h, repos := newAPCRouter(t, fake, true)
	m := apcCreateProduct(t, h, `{"title":"Keep","type":"product","status":"draft"}`)
	id := apcID(t, m)

	fake.authPrincipal = auth.NewPrincipal(2, "plain-editor", []string{auth.RoleEditor})
	before := apcCountType(t, repos, "product")

	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts", `{"title":"Nope","type":"product"}`, true)
	apcWant(t, rec, http.StatusForbidden)
	rec = apcDo(t, h, http.MethodPut, "/admin/api/posts/"+apcItoa(id),
		`{"title":"Hacked","type":"product","status":"draft","modified":"`+m["modified"].(string)+`"}`, true)
	apcWant(t, rec, http.StatusForbidden)
	rec = apcDo(t, h, http.MethodDelete, "/admin/api/posts/"+apcItoa(id)+"?force=true", "", true)
	apcWant(t, rec, http.StatusForbidden)

	if after := apcCountType(t, repos, "product"); after != before {
		t.Fatalf("product rows %d -> %d on forbidden writes", before, after)
	}
	if _, _, title, _, ok := apcRow(t, repos, id); !ok || title != "Keep" {
		t.Fatalf("row changed by forbidden write: title=%q found=%v", title, ok)
	}
}

// Req 10.5: a user without edit_posts is stopped by the route gate even when
// holding the product family.
func TestAdminPostCustomRouteGateRequiresEditPosts(t *testing.T) {
	h, repos := newAPCRouter(t, apcSessions(3, append([]string{auth.RoleSubscriber}, apcProductCaps...)...), true)
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts", `{"title":"Nope","type":"product"}`, true)
	apcWant(t, rec, http.StatusForbidden)
	if n := apcCountType(t, repos, "product"); n != 0 {
		t.Fatalf("route gate let %d product rows through", n)
	}
}

// Req 10.5: CSRF is required on every custom write.
func TestAdminPostCustomRequiresCSRF(t *testing.T) {
	fake := apcShopManager()
	h, repos := newAPCRouter(t, fake, true)
	m := apcCreateProduct(t, h, `{"title":"Keep","type":"product","status":"draft"}`)
	id := apcID(t, m)
	before := apcCountType(t, repos, "product")

	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts", `{"title":"NoCSRF","type":"product"}`, false)
	apcWant(t, rec, http.StatusForbidden)
	rec = apcDo(t, h, http.MethodPut, "/admin/api/posts/"+apcItoa(id),
		`{"title":"NoCSRF","type":"product","status":"draft","modified":"`+m["modified"].(string)+`"}`, false)
	apcWant(t, rec, http.StatusForbidden)
	rec = apcDo(t, h, http.MethodDelete, "/admin/api/posts/"+apcItoa(id), "", false)
	apcWant(t, rec, http.StatusForbidden)

	if after := apcCountType(t, repos, "product"); after != before {
		t.Fatalf("product rows %d -> %d without CSRF", before, after)
	}
	if _, _, title, _, _ := apcRow(t, repos, id); title != "Keep" {
		t.Fatalf("title = %q, want Keep", title)
	}
}

// Req 9.1, 9.3, 9.4: slug derived from the title, de-duplicated with -2, and an
// update that leaves its own slug unchanged does not collide with itself.
func TestAdminPostCustomSlug(t *testing.T) {
	h, repos := newAPCRouter(t, apcShopManager(), true)
	first := apcCreateProduct(t, h, `{"title":"Blue Widget","type":"product","status":"draft"}`)
	second := apcCreateProduct(t, h, `{"title":"Blue Widget","type":"product","status":"draft"}`)
	if first["slug"] != "blue-widget" || second["slug"] != "blue-widget-2" {
		t.Fatalf("slugs = %v, %v; want blue-widget, blue-widget-2", first["slug"], second["slug"])
	}
	if _, slug, _, _, _ := apcRow(t, repos, apcID(t, second)); slug != "blue-widget-2" {
		t.Fatalf("stored slug = %q, want blue-widget-2", slug)
	}

	id := apcID(t, first)
	rec := apcDo(t, h, http.MethodPut, "/admin/api/posts/"+apcItoa(id),
		`{"title":"Blue Widget","slug":"blue-widget","type":"product","status":"draft","modified":"`+first["modified"].(string)+`"}`, true)
	apcWant(t, rec, http.StatusOK)
	if got := apcDecode(t, rec)["slug"]; got != "blue-widget" {
		t.Fatalf("self-update slug = %v, want blue-widget (no self collision)", got)
	}
}

// Req 9.2: an invalid slug -> 400 bad_request, nothing written.
func TestAdminPostCustomInvalidSlug(t *testing.T) {
	h, repos := newAPCRouter(t, apcShopManager(), true)
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts", `{"title":"Bad","slug":"Bad Slug!","type":"product"}`, true)
	apcWant(t, rec, http.StatusBadRequest)
	assertAdminJSONErrorCode(t, rec, "bad_request")
	if n := apcCountType(t, repos, "product"); n != 0 {
		t.Fatalf("invalid slug wrote %d rows", n)
	}
}

// Req 8.1, 8.2: a writer without unfiltered_html is sanitized; one holding it
// is stored byte-identically.
func TestAdminPostCustomSanitization(t *testing.T) {
	const dirty = `<p>hi</p><script>alert(1)</script>`

	authorKeys := append([]string{auth.RoleAuthor}, apcProductCaps...)
	h, repos := newAPCRouter(t, apcSessions(1, authorKeys...), true)
	m := apcCreateProduct(t, h, `{"title":"Widget","content":"<p>hi</p><script>alert(1)</script>","type":"product","status":"draft"}`)
	_, _, _, body, _ := apcRow(t, repos, apcID(t, m))
	if strings.Contains(body, "<script") || !strings.Contains(body, "hi") {
		t.Fatalf("tier-B content not sanitized: %q", body)
	}

	h2, repos2 := newAPCRouter(t, apcSessions(1, append(authorKeys, "unfiltered_html")...), true)
	m2 := apcCreateProduct(t, h2, `{"title":"Widget","content":"<p>hi</p><script>alert(1)</script>","type":"product","status":"draft"}`)
	_, _, _, body2, _ := apcRow(t, repos2, apcID(t, m2))
	if body2 != dirty {
		t.Fatalf("tier-C content = %q, want byte-identical %q", body2, dirty)
	}
}

// Req 8.3: a title that sanitizes to empty -> 400 for a registered type.
func TestAdminPostCustomTitleEmptyAfterSanitize(t *testing.T) {
	h, repos := newAPCRouter(t, apcSessions(1, append([]string{auth.RoleAuthor}, apcProductCaps...)...), true)
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts", `{"title":"<script></script>","type":"product","status":"publish"}`, true)
	apcWant(t, rec, http.StatusBadRequest)
	if n := apcCountType(t, repos, "product"); n != 0 {
		t.Fatalf("empty title wrote %d rows", n)
	}
}

// Req 10 (unchanged built-ins): post and page still write and echo their type
// when a registry is configured.
func TestAdminPostCustomBuiltinsUnchanged(t *testing.T) {
	h, repos := newAPCRouter(t, apcShopManager(), true)
	for _, typ := range []string{"post", "page"} {
		m := apcCreateProduct(t, h, `{"title":"Built-in `+typ+`","type":"`+typ+`","status":"draft"}`)
		if m["type"] != typ {
			t.Fatalf("%s response type = %v", typ, m["type"])
		}
		if got, _, _, _, ok := apcRow(t, repos, apcID(t, m)); !ok || got != typ {
			t.Fatalf("%s stored type = %q (found=%v)", typ, got, ok)
		}
	}
}

func apcItoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
