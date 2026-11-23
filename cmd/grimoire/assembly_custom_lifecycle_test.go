package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// Task 19.1 (Req 17.3, 15, 14, 1-13; D16): the consolidated M17 lifecycle test.
// It asserts behavior wired by Phases 2-17 through the production handler, so
// it has no RED step; its job is to fail loudly if a requirement has no
// end-to-end path. Each row names the requirement group it covers and drives
// REST and the admin API side by side with session (CSRF) and Application
// Password callers.

// lifecycleHelper bundles the request and decode helpers every row uses.
type lifecycleHelper struct {
	t   *testing.T
	env customWriteEnv
}

func (l lifecycleHelper) want(rec *httptest.ResponseRecorder, status int, what string) {
	l.t.Helper()
	if rec.Code != status {
		l.t.Fatalf("%s: status = %d, want %d; body: %s", what, rec.Code, status, rec.Body.String())
	}
}

func (l lifecycleHelper) do(actor *customWriteActor, method, path, body string) *httptest.ResponseRecorder {
	l.t.Helper()
	return doJSON(l.t, l.env.Handler, actor, method, path, body)
}

// doAppPassword sends a request authenticated by HTTP Basic Application
// Password and no session cookie or CSRF header.
func (l lifecycleHelper) doAppPassword(login, secret, method, path, body string) *httptest.ResponseRecorder {
	l.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(login, secret)
	rec := httptest.NewRecorder()
	l.env.Handler.ServeHTTP(rec, req)
	return rec
}

// appPassword mints an Application Password for the named fixture user.
func (l lifecycleHelper) appPassword(login string) string {
	l.t.Helper()
	ctx := context.Background()
	u, err := l.env.Repos.Users.ByLogin(ctx, login)
	if err != nil {
		l.t.Fatalf("ByLogin %s: %v", login, err)
	}
	ap := &auth.ApplicationPasswords{Users: l.env.Repos.Users, Meta: l.env.Repos.UserMeta, Prefix: l.env.Prefix}
	_, secret, err := ap.Create(ctx, u.ID, "lifecycle")
	if err != nil {
		l.t.Fatalf("create application password: %v", err)
	}
	return secret
}

// obj decodes a JSON object response.
func (l lifecycleHelper) obj(rec *httptest.ResponseRecorder) map[string]any {
	l.t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		l.t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func (l lifecycleHelper) id(rec *httptest.ResponseRecorder) int64 {
	l.t.Helper()
	f, ok := l.obj(rec)["id"].(float64)
	if !ok {
		l.t.Fatalf("response has no numeric id: %s", rec.Body.String())
	}
	return int64(f)
}

func (l lifecycleHelper) str(rec *httptest.ResponseRecorder, key string) string {
	l.t.Helper()
	s, _ := l.obj(rec)[key].(string)
	return s
}

// rendered reads a {"rendered": "..."} field such as title or content.
func (l lifecycleHelper) rendered(rec *httptest.ResponseRecorder, key string) string {
	l.t.Helper()
	m, _ := l.obj(rec)[key].(map[string]any)
	s, _ := m["rendered"].(string)
	return s
}

func (l lifecycleHelper) termCount(termID int64) int {
	l.t.Helper()
	var n int
	if err := l.env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT count FROM "+l.env.Prefix+"term_taxonomy WHERE term_id = ?", termID).Scan(&n); err != nil {
		l.t.Fatalf("read count for term %d: %v", termID, err)
	}
	return n
}

func (l lifecycleHelper) rels(objectID int64) int {
	l.t.Helper()
	var n int
	if err := l.env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM "+l.env.Prefix+"term_relationships WHERE object_id = ?", objectID).Scan(&n); err != nil {
		l.t.Fatalf("read relationships for %d: %v", objectID, err)
	}
	return n
}

// lifecycleRow is one acceptance-criteria group of the M17 requirements.
type lifecycleRow struct {
	req  int    // requirement number
	name string // what the row asserts
	run  func(t *testing.T, l lifecycleHelper)
}

const (
	lcGenre   = "/wp-json/wp/v2/genre"
	lcBadge   = "/wp-json/wp/v2/badge"
	lcProduct = "/wp-json/wp/v2/product"
	lcSecret  = "/wp-json/wp/v2/secret"
	lcGuide   = "/wp-json/wp/v2/guide"
)

func lifecycleRows() []lifecycleRow {
	return []lifecycleRow{
		{1, "REST term create: shape, slug derivation, blank name, capability, CSRF, app password", func(t *testing.T, l lifecycleHelper) {
			adm := &l.env.Admin
			rec := l.do(adm, http.MethodPost, lcGenre, `{"name":"Sci Fi"}`)
			l.want(rec, http.StatusCreated, "create genre")
			if l.str(rec, "slug") != "sci-fi" || l.str(rec, "taxonomy") != "genre" {
				t.Fatalf("created term slug/taxonomy = %q/%q", l.str(rec, "slug"), l.str(rec, "taxonomy"))
			}
			if link := l.str(rec, "link"); !strings.HasSuffix(link, "/genre/sci-fi/") {
				t.Fatalf("term link = %q, want suffix /genre/sci-fi/", link)
			}
			l.want(l.do(adm, http.MethodPost, lcGenre, `{"name":"  "}`), http.StatusBadRequest, "blank name")
			l.want(l.do(&l.env.Author, http.MethodPost, lcGenre, `{"name":"Nope"}`), http.StatusForbidden, "author create")
			// A session caller without the CSRF header is refused.
			req := httptest.NewRequest(http.MethodPost, lcGenre, strings.NewReader(`{"name":"No Csrf"}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: adm.Token})
			out := httptest.NewRecorder()
			l.env.Handler.ServeHTTP(out, req)
			if out.Code != http.StatusForbidden {
				t.Fatalf("session without CSRF = %d, want 403: %s", out.Code, out.Body.String())
			}
			// An Application Password caller needs no CSRF header.
			secret := l.appPassword("admin")
			l.want(l.doAppPassword("admin", secret, http.MethodPost, lcGenre, `{"name":"Via AP"}`),
				http.StatusCreated, "app password create")
		}},
		{2, "REST term update and delete", func(t *testing.T, l lifecycleHelper) {
			adm := &l.env.Admin
			id := l.id(l.do(adm, http.MethodPost, lcGenre, `{"name":"Old"}`))
			path := fmt.Sprintf("%s/%d", lcGenre, id)
			rec := l.do(adm, http.MethodPut, path, `{"name":"New","slug":"renamed"}`)
			l.want(rec, http.StatusOK, "PUT term")
			if l.str(rec, "slug") != "renamed" {
				t.Fatalf("slug after PUT = %q", l.str(rec, "slug"))
			}
			l.want(l.do(adm, http.MethodPatch, path, `{"name":"Newer"}`), http.StatusOK, "PATCH term")
			l.want(l.do(&l.env.Author, http.MethodDelete, path, ""), http.StatusForbidden, "author delete")
			l.want(l.do(adm, http.MethodDelete, path, ""), http.StatusOK, "DELETE term")
			l.want(l.do(adm, http.MethodPatch, path, `{"name":"Gone"}`), http.StatusNotFound, "PATCH deleted term")
		}},
		{3, "custom taxonomy hierarchy: parent, cycle, flat taxonomy refuses parent", func(t *testing.T, l lifecycleHelper) {
			adm := &l.env.Admin
			root := l.id(l.do(adm, http.MethodPost, lcGenre, `{"name":"Root"}`))
			rec := l.do(adm, http.MethodPost, lcGenre, fmt.Sprintf(`{"name":"Kid","parent":%d}`, root))
			l.want(rec, http.StatusCreated, "child")
			kid := l.id(rec)
			l.want(l.do(adm, http.MethodPut, fmt.Sprintf("%s/%d", lcGenre, root), fmt.Sprintf(`{"parent":%d}`, kid)),
				http.StatusBadRequest, "cycle")
			l.want(l.do(adm, http.MethodPost, lcBadge, fmt.Sprintf(`{"name":"B","parent":%d}`, root)),
				http.StatusBadRequest, "flat taxonomy parent")
		}},
		{4, "term slug uniqueness, name sanitization, vocabulary", func(t *testing.T, l lifecycleHelper) {
			adm := &l.env.Admin
			l.want(l.do(adm, http.MethodPost, lcGenre, `{"name":"Dup","slug":"dup"}`), http.StatusCreated, "first")
			rec := l.do(adm, http.MethodPost, lcGenre, `{"name":"Dup 2","slug":"dup"}`)
			l.want(rec, http.StatusBadRequest, "duplicate slug")
			if l.str(rec, "code") != "term_exists" {
				t.Fatalf("duplicate code = %q, want term_exists", l.str(rec, "code"))
			}
			// Registered-taxonomy names are plain text for every caller, including
			// an unfiltered_html holder (Req 4.1; the term tier is fixed at B).
			rec = l.do(adm, http.MethodPost, lcGenre, `{"name":"Raw<b>Name</b>"}`)
			l.want(rec, http.StatusCreated, "admin term name")
			if l.str(rec, "name") != "RawName" {
				t.Fatalf("admin term name = %q, want plain text RawName", l.str(rec, "name"))
			}
			if _, err := content.NewUserService(l.env.Repos.Users, l.env.Repos.UserMeta, l.env.Prefix).Bootstrap(context.Background(), domain.User{
				Login: "termer", Nicename: "termer", DisplayName: "termer", Email: "termer@example.test",
			}, customWritePassword, "read", "edit_posts", "manage_categories"); err != nil {
				t.Fatalf("bootstrap termer: %v", err)
			}
			secret := l.appPassword("termer")
			rec = l.doAppPassword("termer", secret, http.MethodPost, lcGenre, `{"name":"Safe<script>alert(1)</script>Name"}`)
			l.want(rec, http.StatusCreated, "name with script")
			// The 201 body reflects the stored (sanitized) term, same as GET.
			if got := l.str(rec, "name"); got != "SafeName" {
				t.Fatalf("create response name = %q, want plain text SafeName", got)
			}
			rec = l.do(nil, http.MethodGet, fmt.Sprintf("%s/%d", lcGenre, l.id(rec)), "")
			l.want(rec, http.StatusOK, "read stored term")
			if got := l.str(rec, "name"); got != "SafeName" {
				t.Fatalf("stored term name = %q, want plain text SafeName", got)
			}
			l.want(l.doAppPassword("termer", secret, http.MethodPost, lcGenre, `{"name":"<script>x()</script>"}`),
				http.StatusBadRequest, "name sanitizing to empty")
			// A term of one taxonomy is refused as another's (genre vs badge).
			b := l.id(l.do(adm, http.MethodPost, lcBadge, `{"name":"Hot"}`))
			l.want(l.do(&l.env.Curator, http.MethodPost, lcProduct,
				fmt.Sprintf(`{"title":"Bad Vocab","status":"publish","genre":[%d]}`, b)),
				http.StatusBadRequest, "wrong vocabulary")
		}},
		{5, "admin-API term writes for a registered taxonomy", func(t *testing.T, l lifecycleHelper) {
			adm := &l.env.Admin
			rec := l.do(adm, http.MethodPost, "/admin/api/terms", `{"name":"Admin Genre","slug":"admin-genre","taxonomy":"genre"}`)
			l.want(rec, http.StatusCreated, "admin create")
			id := l.id(rec)
			l.want(l.do(adm, http.MethodPut, fmt.Sprintf("/admin/api/terms/%d", id), `{"name":"Admin Genre 2","slug":"ag2"}`),
				http.StatusOK, "admin update")
			l.want(l.do(&l.env.Author, http.MethodPost, "/admin/api/terms", `{"name":"X","slug":"x","taxonomy":"genre"}`),
				http.StatusForbidden, "author admin create")
			l.want(l.do(adm, http.MethodPost, "/admin/api/terms", `{"name":"X","slug":"x","taxonomy":"nosuch"}`),
				http.StatusBadRequest, "unregistered taxonomy")
			l.want(l.do(adm, http.MethodDelete, fmt.Sprintf("/admin/api/terms/%d", id), ""),
				http.StatusNoContent, "admin delete")
		}},
		{6, "REST post create, update, delete, 501 shapes, route index", func(t *testing.T, l lifecycleHelper) {
			pu := &l.env.Products
			rec := l.do(pu, http.MethodPost, lcProduct, `{"title":"Lamp","status":"publish"}`)
			l.want(rec, http.StatusCreated, "create")
			id := l.id(rec)
			if l.str(rec, "type") != "product" {
				t.Fatalf("type = %q", l.str(rec, "type"))
			}
			path := fmt.Sprintf("%s/%d", lcProduct, id)
			l.want(l.do(pu, http.MethodPut, path, `{"title":"Lamp 2"}`), http.StatusOK, "PUT")
			l.want(l.do(pu, http.MethodPatch, path, `{"excerpt":"short"}`), http.StatusOK, "PATCH")
			// Another type's id is a 404 on this route.
			g := l.id(l.do(&l.env.Curator, http.MethodPost, lcGuide, `{"title":"G","status":"publish"}`))
			l.want(l.do(pu, http.MethodPatch, fmt.Sprintf("%s/%d", lcProduct, g), `{"title":"x"}`), http.StatusNotFound, "wrong type id")
			l.want(l.do(pu, http.MethodPost, path, `{"title":"x"}`), http.StatusNotImplemented, "POST on item")
			l.want(l.do(pu, http.MethodDelete, lcProduct, ""), http.StatusNotImplemented, "collection DELETE")
			// Application Password caller.
			secret := l.appPassword("products")
			l.want(l.doAppPassword("products", secret, http.MethodPost, lcProduct, `{"title":"By AP","status":"publish"}`),
				http.StatusCreated, "app password create")
			l.want(l.do(pu, http.MethodDelete, path+"?force=true", ""), http.StatusOK, "DELETE")
			var idx struct {
				Routes map[string]struct {
					Methods []string `json:"methods"`
				} `json:"routes"`
			}
			if err := json.Unmarshal(l.do(nil, http.MethodGet, "/wp-json/wp/v2", "").Body.Bytes(), &idx); err != nil {
				t.Fatalf("decode index: %v", err)
			}
			has := func(route string, want ...string) {
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
			has("/wp/v2/product", "GET", "POST")
			has(`/wp/v2/product/(?P<id>[\d]+)`, "GET", "PUT", "PATCH", "DELETE")
		}},
		{7, "capability family from the registry entry", func(t *testing.T, l lifecycleHelper) {
			ed := &l.env.Editor
			l.want(l.do(ed, http.MethodPost, lcProduct, `{"title":"No","status":"draft"}`), http.StatusForbidden, "editor REST")
			l.want(l.do(ed, http.MethodPost, "/admin/api/posts", `{"title":"No","type":"product","status":"draft"}`),
				http.StatusForbidden, "editor admin")
			l.want(l.do(&l.env.Contrib, http.MethodPost, lcProduct, `{"title":"No","status":"draft"}`), http.StatusForbidden, "contributor")
			// An Application Password for a user without the family is refused too.
			secret := l.appPassword("editor")
			l.want(l.doAppPassword("editor", secret, http.MethodPost, lcProduct, `{"title":"No","status":"draft"}`),
				http.StatusForbidden, "editor app password")
			id := l.id(l.do(&l.env.Products, http.MethodPost, lcProduct, `{"title":"Mine","status":"publish"}`))
			l.want(l.do(ed, http.MethodDelete, fmt.Sprintf("%s/%d?force=true", lcProduct, id), ""), http.StatusForbidden, "editor delete")
		}},
		{8, "write-boundary sanitization", func(t *testing.T, l lifecycleHelper) {
			pu := &l.env.Products
			rec := l.do(pu, http.MethodPost, lcProduct,
				`{"title":"S<script>x()</script>afe","content":"<p>ok</p><script>y()</script>","excerpt":"<b>e</b><script>z()</script>","status":"publish"}`)
			l.want(rec, http.StatusCreated, "create")
			for _, f := range []string{"title", "content", "excerpt"} {
				if strings.Contains(l.rendered(rec, f), "<script") {
					t.Fatalf("%s not sanitized: %q", f, l.rendered(rec, f))
				}
			}
			l.want(l.do(pu, http.MethodPost, lcProduct, `{"title":"<script>x()</script>","status":"publish"}`),
				http.StatusBadRequest, "title sanitizing to empty")
			rec = l.do(pu, http.MethodPost, "/admin/api/posts",
				`{"title":"A","type":"product","status":"publish","content":"<p>a</p><script>q()</script>"}`)
			l.want(rec, http.StatusCreated, "admin create")
			if strings.Contains(l.str(rec, "content"), "<script") {
				t.Fatalf("admin content not sanitized: %q", l.str(rec, "content"))
			}
		}},
		{9, "post slug derivation and uniqueness", func(t *testing.T, l lifecycleHelper) {
			pu := &l.env.Products
			a := l.do(pu, http.MethodPost, lcProduct, `{"title":"Same Name","status":"publish"}`)
			l.want(a, http.StatusCreated, "first")
			b := l.do(pu, http.MethodPost, lcProduct, `{"title":"Same Name","status":"publish"}`)
			l.want(b, http.StatusCreated, "second")
			if l.str(a, "slug") != "same-name" || l.str(b, "slug") != "same-name-2" {
				t.Fatalf("slugs = %q, %q", l.str(a, "slug"), l.str(b, "slug"))
			}
			l.want(l.do(pu, http.MethodPost, lcProduct, `{"title":"Bad","slug":"Bad Slug!","status":"publish"}`),
				http.StatusBadRequest, "invalid slug REST")
			l.want(l.do(pu, http.MethodPost, "/admin/api/posts", `{"title":"Bad","slug":"Bad Slug!","type":"product","status":"publish"}`),
				http.StatusBadRequest, "invalid slug admin")
			// Re-saving with the row's own slug does not collide with itself.
			rec := l.do(pu, http.MethodPatch, fmt.Sprintf("%s/%d", lcProduct, l.id(b)), `{"slug":"same-name-2","title":"Renamed"}`)
			l.want(rec, http.StatusOK, "own slug")
			if l.str(rec, "slug") != "same-name-2" {
				t.Fatalf("slug changed to %q", l.str(rec, "slug"))
			}
		}},
		{10, "admin-API post writes for registered types", func(t *testing.T, l lifecycleHelper) {
			pu := &l.env.Products
			rec := l.do(pu, http.MethodPost, "/admin/api/posts", `{"title":"Adm","type":"product","status":"publish"}`)
			l.want(rec, http.StatusCreated, "create")
			if l.str(rec, "type") != "product" {
				t.Fatalf("type = %q", l.str(rec, "type"))
			}
			id, mod := l.id(rec), l.str(rec, "modified")
			// A differing type on PUT is ignored.
			rec = l.do(pu, http.MethodPut, fmt.Sprintf("/admin/api/posts/%d", id),
				fmt.Sprintf(`{"title":"Adm 2","type":"secret","status":"publish","modified":%q}`, mod))
			l.want(rec, http.StatusOK, "PUT")
			if l.str(rec, "type") != "product" {
				t.Fatalf("type after PUT = %q, want product", l.str(rec, "type"))
			}
			l.want(l.do(pu, http.MethodPost, "/admin/api/posts", `{"title":"U","type":"unregistered","status":"publish"}`),
				http.StatusBadRequest, "unregistered type")
			l.want(l.do(pu, http.MethodDelete, fmt.Sprintf("/admin/api/posts/%d", id), ""), http.StatusNoContent, "delete")
		}},
		{11, "hierarchical post_parent writes", func(t *testing.T, l lifecycleHelper) {
			cu := &l.env.Curator
			root := l.id(l.do(cu, http.MethodPost, lcGuide, `{"title":"Top","status":"publish"}`))
			rec := l.do(cu, http.MethodPost, lcGuide, fmt.Sprintf(`{"title":"Sub","status":"publish","parent":%d}`, root))
			l.want(rec, http.StatusCreated, "child")
			if !strings.HasSuffix(l.str(rec, "link"), "/guide/top/sub/") {
				t.Fatalf("child link = %q", l.str(rec, "link"))
			}
			sub := l.id(rec)
			l.want(l.do(cu, http.MethodPatch, fmt.Sprintf("%s/%d", lcGuide, root), fmt.Sprintf(`{"parent":%d}`, sub)),
				http.StatusBadRequest, "cycle")
			l.want(l.do(cu, http.MethodPost, lcProduct, fmt.Sprintf(`{"title":"F","status":"publish","parent":%d}`, root)),
				http.StatusBadRequest, "flat type parent")
			// Omitted parent keeps it; zero makes the row top-level.
			rec = l.do(cu, http.MethodPatch, fmt.Sprintf("%s/%d", lcGuide, sub), `{"title":"Sub 2"}`)
			l.want(rec, http.StatusOK, "omit parent")
			if p, _ := l.obj(rec)["parent"].(float64); int64(p) != root {
				t.Fatalf("parent after omit = %v, want %d", p, root)
			}
			rec = l.do(cu, http.MethodPatch, fmt.Sprintf("%s/%d", lcGuide, sub), `{"parent":0}`)
			l.want(rec, http.StatusOK, "zero parent")
			if p, _ := l.obj(rec)["parent"].(float64); p != 0 {
				t.Fatalf("parent after 0 = %v", p)
			}
			// Capability before validation: 403, not 400.
			l.want(l.do(&l.env.Contrib, http.MethodPatch, fmt.Sprintf("%s/%d", lcGuide, root), fmt.Sprintf(`{"parent":%d}`, root)),
				http.StatusForbidden, "403 before 400")
			// A parent key on a built-in post stays ignored.
			rec = l.do(&l.env.Admin, http.MethodPost, "/wp-json/wp/v2/posts", fmt.Sprintf(`{"title":"Plain","status":"publish","parent":%d}`, root))
			l.want(rec, http.StatusCreated, "built-in post with parent key")
		}},
		{12, "term assignment to attached taxonomies only", func(t *testing.T, l lifecycleHelper) {
			adm, cu := &l.env.Admin, &l.env.Curator
			g := l.id(l.do(adm, http.MethodPost, lcGenre, `{"name":"G1"}`))
			b := l.id(l.do(adm, http.MethodPost, lcBadge, `{"name":"B1"}`))
			rec := l.do(cu, http.MethodPost, lcProduct, fmt.Sprintf(`{"title":"T","status":"publish","genre":[%d]}`, g))
			l.want(rec, http.StatusCreated, "REST assign")
			pid := l.id(rec)
			if l.termCount(g) != 1 || l.rels(pid) != 1 {
				t.Fatalf("count/rels = %d/%d, want 1/1", l.termCount(g), l.rels(pid))
			}
			l.want(l.do(cu, http.MethodPatch, fmt.Sprintf("%s/%d", lcProduct, pid), `{"genre":["x"]}`),
				http.StatusBadRequest, "non-integer ids")
			l.want(l.do(cu, http.MethodPatch, fmt.Sprintf("%s/%d", lcProduct, pid), `{"genre":[999999]}`),
				http.StatusBadRequest, "unknown term id")
			// Replace with none.
			l.want(l.do(cu, http.MethodPatch, fmt.Sprintf("%s/%d", lcProduct, pid), `{"genre":[]}`), http.StatusOK, "clear")
			if l.termCount(g) != 0 {
				t.Fatalf("count after clear = %d", l.termCount(g))
			}
			// Admin termIds: attached taxonomy assigns, unattached is partial.
			rec = l.do(cu, http.MethodPost, "/admin/api/posts",
				fmt.Sprintf(`{"title":"G","type":"guide","status":"publish","termIds":{"genre":[%d],"badge":[%d]}}`, g, b))
			l.want(rec, http.StatusCreated, "admin assign")
			partial, _ := l.obj(rec)["partial"].(map[string]any)
			if partial["badge"] == nil || partial["genre"] != nil {
				t.Fatalf("partial = %v, want only badge", partial)
			}
			if l.termCount(b) != 0 || l.termCount(g) != 1 {
				t.Fatalf("counts genre/badge = %d/%d, want 1/0", l.termCount(g), l.termCount(b))
			}
			// Assignment is authorized as an edit of the post, not manage_categories.
			l.want(l.do(&l.env.Contrib, http.MethodPost, lcProduct, fmt.Sprintf(`{"title":"X","status":"draft","genre":[%d]}`, g)),
				http.StatusForbidden, "contributor assign")
		}},
		{13, "delete semantics: permanent, counts, children re-pointed", func(t *testing.T, l lifecycleHelper) {
			adm, cu := &l.env.Admin, &l.env.Curator
			g := l.id(l.do(adm, http.MethodPost, lcGenre, `{"name":"DG"}`))
			rec := l.do(cu, http.MethodPost, lcProduct, fmt.Sprintf(`{"title":"Del","status":"publish","genre":[%d]}`, g))
			l.want(rec, http.StatusCreated, "create")
			pid := l.id(rec)
			rec = l.do(cu, http.MethodDelete, fmt.Sprintf("%s/%d?force=true", lcProduct, pid), "")
			l.want(rec, http.StatusOK, "delete ignoring force")
			if d, _ := l.obj(rec)["deleted"].(bool); !d {
				t.Fatalf("not marked deleted: %s", rec.Body.String())
			}
			if l.termCount(g) != 0 || l.rels(pid) != 0 {
				t.Fatalf("count/rels after delete = %d/%d", l.termCount(g), l.rels(pid))
			}
			l.want(l.do(cu, http.MethodDelete, fmt.Sprintf("%s/%d", lcProduct, pid), ""), http.StatusNotFound, "delete again")
			// Children are re-pointed to the deleted post's own parent (M18 Req 9.1/9.3).
			root := l.id(l.do(cu, http.MethodPost, lcGuide, `{"title":"Par","status":"publish"}`))
			kid := l.id(l.do(cu, http.MethodPost, lcGuide, fmt.Sprintf(`{"title":"Chi","status":"publish","parent":%d}`, root)))
			l.want(l.do(cu, http.MethodDelete, fmt.Sprintf("%s/%d?force=true", lcGuide, root), ""), http.StatusOK, "delete parent")
			var parent int64
			if err := l.env.Repos.DB().QueryRowContext(context.Background(),
				"SELECT post_parent FROM "+l.env.Prefix+"posts WHERE ID = ?", kid).Scan(&parent); err != nil {
				t.Fatalf("read post_parent: %v", err)
			}
			if parent != 0 {
				t.Fatalf("child post_parent = %d, want 0 (re-pointed to deleted post's parent)", parent)
			}
		}},
		{14, "public and non-public types, nested path change", func(t *testing.T, l lifecycleHelper) {
			cu := &l.env.Curator
			// public: true serves; public: false is writable on both surfaces yet never served.
			l.want(l.do(cu, http.MethodPost, lcProduct, `{"title":"Shown","status":"publish"}`), http.StatusCreated, "public create")
			l.want(l.do(nil, http.MethodGet, "/product/shown/", ""), http.StatusOK, "public GET")
			rec := l.do(cu, http.MethodPost, lcSecret, `{"title":"Hush","status":"publish"}`)
			l.want(rec, http.StatusCreated, "secret REST create")
			sid := l.id(rec)
			l.want(l.do(cu, http.MethodPost, "/admin/api/posts", `{"title":"Hush Two","type":"secret","status":"publish"}`),
				http.StatusCreated, "secret admin create")
			l.want(l.do(cu, http.MethodPatch, fmt.Sprintf("%s/%d", lcSecret, sid), `{"title":"Hush Renamed"}`), http.StatusOK, "secret update")
			for _, p := range []string{"/secret/hush/", "/hush/", "/secret/hush-two/", "/hush-two/", "/secret/"} {
				l.want(l.do(nil, http.MethodGet, p, ""), http.StatusNotFound, "GET "+p)
			}
			// Hierarchical rename: new path serves, old path 301s.
			root := l.id(l.do(cu, http.MethodPost, lcGuide, `{"title":"Book","status":"publish"}`))
			l.want(l.do(cu, http.MethodPost, lcGuide, fmt.Sprintf(`{"title":"Page","status":"publish","parent":%d}`, root)), http.StatusCreated, "child")
			l.want(l.do(cu, http.MethodPatch, fmt.Sprintf("%s/%d", lcGuide, root), `{"slug":"tome"}`), http.StatusOK, "rename")
			l.want(l.do(nil, http.MethodGet, "/guide/tome/page/", ""), http.StatusOK, "new nested path")
			rec = l.do(nil, http.MethodGet, "/guide/book/page/", "")
			l.want(rec, http.StatusMovedPermanently, "old nested path")
			if loc := rec.Header().Get("Location"); loc != "/guide/tome/page/" {
				t.Fatalf("Location = %q", loc)
			}
		}},
		{15, "built-in golden files still match with the registry present", func(t *testing.T, l lifecycleHelper) {
			for _, tc := range []struct {
				withRegistry bool
				file         string
			}{{false, "without_registry.json"}, {true, "with_registry.json"}} {
				got, err := json.MarshalIndent(runBuiltinGolden(t, tc.withRegistry), "", "  ")
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				got = append(got, '\n')
				want, err := os.ReadFile(filepath.Join("testdata", "builtin_golden", tc.file))
				if err != nil {
					t.Fatalf("read golden: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("built-in behavior changed vs %s;\n%s", tc.file, firstDiff(string(want), string(got)))
				}
			}
		}},
	}
}

// TestCustomLifecycle runs one row per requirement group (1-15) through
// buildHandler, each on a fresh database.
func TestCustomLifecycle(t *testing.T) {
	for _, row := range lifecycleRows() {
		t.Run(fmt.Sprintf("Req%02d_%s", row.req, strings.ReplaceAll(row.name[:min(len(row.name), 24)], " ", "_")), func(t *testing.T) {
			row.run(t, lifecycleHelper{t: t, env: newCustomWriteHandler(t, true)})
		})
	}
}

// TestCustomLifecycleCoversEveryRequirement fails when a requirement from 1 to
// 15 has no row.
func TestCustomLifecycleCoversEveryRequirement(t *testing.T) {
	seen := map[int]bool{}
	for _, row := range lifecycleRows() {
		seen[row.req] = true
	}
	var missing []int
	for r := 1; r <= 15; r++ {
		if !seen[r] {
			missing = append(missing, r)
		}
	}
	sort.Ints(missing)
	if len(missing) > 0 {
		t.Fatalf("requirements without a lifecycle row: %v", missing)
	}
}

// TestCustomLifecyclePublicAndPrivateTypes drives the full create, read,
// update, delete cycle on a public and a non-public type, over REST (session
// and Application Password) and the admin API side by side.
func TestCustomLifecyclePublicAndPrivateTypes(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		public bool
	}{{"product", true}, {"secret", false}} {
		t.Run(tc.typ, func(t *testing.T) {
			l := lifecycleHelper{t: t, env: newCustomWriteHandler(t, true)}
			pu := &l.env.Products
			secret := l.appPassword("products")
			rest := "/wp-json/wp/v2/" + tc.typ
			publicStatus := http.StatusNotFound
			if tc.public {
				publicStatus = http.StatusOK
			}
			// REST via session, via Application Password, and admin via session.
			rec := l.do(pu, http.MethodPost, rest, `{"title":"Rest One","status":"publish"}`)
			l.want(rec, http.StatusCreated, "REST create")
			restID := l.id(rec)
			rec = l.doAppPassword("products", secret, http.MethodPost, rest, `{"title":"Ap One","status":"publish"}`)
			l.want(rec, http.StatusCreated, "AP create")
			apID := l.id(rec)
			rec = l.do(pu, http.MethodPost, "/admin/api/posts", fmt.Sprintf(`{"title":"Adm One","type":%q,"status":"publish"}`, tc.typ))
			l.want(rec, http.StatusCreated, "admin create")
			admID, mod := l.id(rec), l.str(rec, "modified")

			// Read: REST by id is always served to the writer; the public URL follows `public`.
			l.want(l.do(pu, http.MethodGet, fmt.Sprintf("%s/%d", rest, restID), ""), http.StatusOK, "REST read")
			for _, slug := range []string{"rest-one", "ap-one", "adm-one"} {
				l.want(l.do(nil, http.MethodGet, "/"+tc.typ+"/"+slug+"/", ""), publicStatus, "public GET "+slug)
			}
			// Update.
			l.want(l.do(pu, http.MethodPatch, fmt.Sprintf("%s/%d", rest, restID), `{"title":"Rest Two"}`), http.StatusOK, "REST update")
			l.want(l.doAppPassword("products", secret, http.MethodPatch, fmt.Sprintf("%s/%d", rest, apID), `{"title":"Ap Two"}`),
				http.StatusOK, "AP update")
			l.want(l.do(pu, http.MethodPut, fmt.Sprintf("/admin/api/posts/%d", admID),
				fmt.Sprintf(`{"title":"Adm Two","type":%q,"status":"publish","modified":%q}`, tc.typ, mod)), http.StatusOK, "admin update")
			// Delete.
			l.want(l.do(pu, http.MethodDelete, fmt.Sprintf("%s/%d?force=true", rest, restID), ""), http.StatusOK, "REST delete")
			l.want(l.doAppPassword("products", secret, http.MethodDelete, fmt.Sprintf("%s/%d?force=true", rest, apID), ""), http.StatusOK, "AP delete")
			l.want(l.do(pu, http.MethodDelete, fmt.Sprintf("/admin/api/posts/%d", admID), ""), http.StatusNoContent, "admin delete")
			l.want(l.do(pu, http.MethodGet, fmt.Sprintf("%s/%d", rest, restID), ""), http.StatusNotFound, "REST read after delete")
		})
	}
}

// TestCustomTermWriteResponseReflectsStoredName is the regression for REST and
// admin term create/update answering with the raw request name instead of the
// stored (sanitized) one, for a registered taxonomy.
func TestCustomTermWriteResponseReflectsStoredName(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	l := lifecycleHelper{t: t, env: env}
	ts := &env.Termsmith
	const raw = `Safe<script>alert(1)</script>Name`

	// REST create.
	rec := l.do(ts, http.MethodPost, lcGenre, `{"name":"`+raw+`"}`)
	l.want(rec, http.StatusCreated, "rest create")
	restID := l.id(rec)
	stored := l.str(l.do(nil, http.MethodGet, fmt.Sprintf("%s/%d", lcGenre, restID), ""), "name")
	if stored != "SafeName" || l.str(rec, "name") != stored {
		t.Fatalf("rest create response name = %q, stored = %q, want both SafeName", l.str(rec, "name"), stored)
	}
	// REST update.
	rec = l.do(ts, http.MethodPut, fmt.Sprintf("%s/%d", lcGenre, restID), `{"name":"`+raw+`2"}`)
	l.want(rec, http.StatusOK, "rest update")
	stored = l.str(l.do(nil, http.MethodGet, fmt.Sprintf("%s/%d", lcGenre, restID), ""), "name")
	if stored != "SafeName2" || l.str(rec, "name") != stored {
		t.Fatalf("rest update response name = %q, stored = %q, want both SafeName2", l.str(rec, "name"), stored)
	}
	// Admin create.
	rec = l.do(ts, http.MethodPost, "/admin/api/terms", `{"name":"`+raw+`","slug":"adm-safe","taxonomy":"genre"}`)
	l.want(rec, http.StatusCreated, "admin create")
	admID := l.id(rec)
	stored = l.str(l.do(nil, http.MethodGet, fmt.Sprintf("%s/%d", lcGenre, admID), ""), "name")
	if stored != "SafeName" || l.str(rec, "name") != stored {
		t.Fatalf("admin create response name = %q, stored = %q, want both SafeName", l.str(rec, "name"), stored)
	}
	// Admin update.
	rec = l.do(ts, http.MethodPut, fmt.Sprintf("/admin/api/terms/%d", admID), `{"name":"`+raw+`2"}`)
	l.want(rec, http.StatusOK, "admin update")
	stored = l.str(l.do(nil, http.MethodGet, fmt.Sprintf("%s/%d", lcGenre, admID), ""), "name")
	if stored != "SafeName2" || l.str(rec, "name") != stored {
		t.Fatalf("admin update response name = %q, stored = %q, want both SafeName2", l.str(rec, "name"), stored)
	}
}
