package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/seed"
	"github.com/roboweaver/grimoire/internal/web"
)

// m6TermReadWriter combines domain.TermWriter and domain.TermReader for
// content.NewTermWriteService, mirroring cmd/grimoire/main.go's unexported
// termReadWriter (internal/web and internal/content have no shared adapter
// type, so every wiring site -- main.go, and now this e2e rig -- defines its
// own copy).
type m6TermReadWriter struct {
	domain.TermWriter
	domain.TermReader
}

// m6Env wires the full M6 surface exactly as cmd/grimoire/main.go does: the
// M1-M5 stack (render, auth, comments/media/menus, REST, Application
// Passwords) plus WithAdminWrites (Req 1-4), the admin post/term write
// services from Phase 1/2, and REST write parity from Phase 3. Every test in
// this file exercises the real HTTP boundary (httptest.Server) against a
// fully wired server -- the actual admin JSON API + REST parity endpoints,
// not internal/web's in-package handler tests -- per the milestone's
// explicit no-skip instruction for Phase 5.
type m6Env struct {
	t      *testing.T
	ts     *httptest.Server
	client *http.Client
	csrf   string
}

func newM6Env(t *testing.T) *m6Env {
	t.Helper()
	ctx := t.Context()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	dbcfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}

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
	if err := seed.Run(ctx, repos.DB(), dbcfg.Vendor, dbcfg.TablePrefix); err != nil {
		t.Fatalf("seed.Run: %v", err)
	}

	const adminLogin, adminPass = "root", "correct-horse-battery-staple"
	users := content.NewUserService(repos.Users, repos.UserMeta, dbcfg.TablePrefix)
	if _, err := users.Bootstrap(ctx, domain.User{
		Login: adminLogin, Nicename: adminLogin, DisplayName: "Admin", Email: "admin@example.test",
	}, adminPass, auth.RoleAdministrator); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}

	sm := &auth.SessionManager{Users: repos.Users, Meta: repos.UserMeta, Sessions: repos.Sessions, Prefix: dbcfg.TablePrefix}
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	menus := content.NewNavMenuService(repos.NavMenus, "default")
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: t.TempDir(), BaseURL: "/wp-content/uploads"})

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}

	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	restMapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, dbcfg.TablePrefix)
	appPasswords := &auth.ApplicationPasswords{Users: repos.Users, Meta: repos.UserMeta, Prefix: dbcfg.TablePrefix}

	termRW := m6TermReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader}
	postWrite := content.NewPostWriteService(repos.PostWriter)
	termWrite := content.NewTermWriteService(termRW)
	postTermsWrite := content.NewPostTermsWriteService(repos.PostWriter, repos.PostTermsWriter)

	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithContentFeatures(comments, media, menus).
		WithAuth(sm, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(postWrite, termWrite, postTermsWrite, repos.PostTerms).
		WithREST(restMapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(appPasswords, true, "")

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	env := &m6Env{t: t, ts: ts, client: client}
	env.login(adminLogin, adminPass)
	return env
}

// login authenticates the admin client and captures the session CSRF token
// via the real /admin/api/session endpoint (mirrors the SPA bootstrap),
// exactly as m5Env.login does.
func (e *m6Env) login(loginName, password string) {
	e.t.Helper()
	resp, err := e.client.Get(e.ts.URL + "/login")
	if err != nil {
		e.t.Fatalf("GET /login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := formInputs(string(body))["csrf_token"]
	if csrf == "" {
		e.t.Fatal("GET /login missing csrf_token")
	}
	resp, err = e.client.PostForm(e.ts.URL+"/login", url.Values{"log": {loginName}, "pwd": {password}, "csrf_token": {csrf}, "redirect": {"/"}})
	if err != nil {
		e.t.Fatalf("POST /login: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("POST /login = %d, want 303", resp.StatusCode)
	}

	resp, err = e.client.Get(e.ts.URL + "/admin/api/session")
	if err != nil {
		e.t.Fatalf("GET /admin/api/session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET /admin/api/session = %d, want 200", resp.StatusCode)
	}
	var sess struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		e.t.Fatalf("decode session: %v", err)
	}
	if sess.CSRFToken == "" {
		e.t.Fatal("admin session missing csrfToken")
	}
	e.csrf = sess.CSRFToken
}

// adminJSON issues a session-cookie + X-CSRF-Token authenticated JSON
// request against /admin/api/*, mirroring the SPA's api/client.ts.
func (e *m6Env) adminJSON(method, path string, body any) *http.Response {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-CSRF-Token", e.csrf)
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// decodeJSON decodes resp's body into v, failing the test on error, and
// always closes the body.
func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5.1: full-stack admin scenario -- log in, create a post with a
// newly-created inline category, confirm it appears in the admin listing,
// edit it (title change + draft -> publish), confirm the public site now
// renders it, delete it, confirm 404 on both the public route and the admin
// detail endpoint.
// ---------------------------------------------------------------------------

// TestM6AdminCRUDFullLifecycleE2E exercises the entire admin CRUD editor
// story end-to-end over real HTTP against the actual wired-up admin API:
// PostsList's listing endpoint, TermPicker's inline-create endpoint,
// PostEditor's create/update/delete flow (including the draft -> publish
// status transition and the public render that unlocks), and the delete
// confirmation's expected 404s. This is the scenario Req 1-5/9 describe as a
// single user journey through the SPA; the SPA itself is exercised by
// web/admin's own component-level Vitest suites (Phase 4), so here we drive
// the same HTTP surface the SPA calls, which is what actually matters for
// catching a wiring gap between the two.
func TestM6AdminCRUDFullLifecycleE2E(t *testing.T) {
	env := newM6Env(t)

	// Inline-create a category the same way TermPicker's "New category"
	// field does: client-side slug, then POST /admin/api/terms.
	catResp := env.adminJSON(http.MethodPost, "/admin/api/terms", map[string]string{
		"name": "Announcements", "slug": "announcements", "taxonomy": "category",
	})
	if catResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(catResp.Body)
		catResp.Body.Close()
		t.Fatalf("create category = %d, want 201 (body %q)", catResp.StatusCode, string(body))
	}
	var cat struct {
		ID int64 `json:"id"`
	}
	decodeJSON(t, catResp, &cat)
	if cat.ID == 0 {
		t.Fatal("create category returned zero id")
	}

	// Create a draft post with that category assigned, per PostEditor's
	// create request shape (no `date`, no `modified`, termIds keyed by
	// taxonomy).
	createResp := env.adminJSON(http.MethodPost, "/admin/api/posts", map[string]any{
		"title": "Hello from M6", "content": "<p>First post body.</p>",
		"excerpt": "First post.", "slug": "hello-from-m6", "status": "draft",
		"type": "post", "commentStatus": "open",
		"termIds": map[string][]int64{"category": {cat.ID}},
	})
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("create post = %d, want 201 (body %q)", createResp.StatusCode, string(body))
	}
	var created struct {
		ID       int64  `json:"id"`
		Slug     string `json:"slug"`
		Modified string `json:"modified"`
		Terms    map[string][]struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"terms"`
	}
	decodeJSON(t, createResp, &created)
	if created.ID == 0 || created.Modified == "" {
		t.Fatalf("create post response missing id/modified: %+v", created)
	}
	if cats := created.Terms["category"]; len(cats) != 1 || cats[0].ID != cat.ID {
		t.Fatalf("create post terms.category = %+v, want [%d]", cats, cat.ID)
	}

	// Confirm it appears in PostsList's backing endpoint.
	listResp := env.adminJSON(http.MethodGet, "/admin/api/posts?page=1&perPage=50&type=post", nil)
	var list struct {
		Items []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}
	decodeJSON(t, listResp, &list)
	found := false
	for _, item := range list.Items {
		if item.ID == created.ID && item.Title == "Hello from M6" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created post %d not found in admin listing: %+v", created.ID, list.Items)
	}

	// A draft is not publicly reachable yet.
	preResp, err := env.client.Get(env.ts.URL + "/" + created.Slug)
	if err != nil {
		t.Fatalf("GET public draft: %v", err)
	}
	preResp.Body.Close()
	if preResp.StatusCode == http.StatusOK {
		t.Fatalf("GET /%s for a draft = 200, want non-200 (not yet published)", created.Slug)
	}

	// Edit: change the title and transition draft -> publish, exactly as
	// PostEditor's Save does on an existing post (modified round-tripped,
	// termIds re-sent to preserve the assignment).
	updateResp := env.adminJSON(http.MethodPut, "/admin/api/posts/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"title": "Hello from M6 (published)", "content": "<p>First post body.</p>",
		"excerpt": "First post.", "slug": created.Slug, "status": "publish",
		"type": "post", "commentStatus": "open", "modified": created.Modified,
		"termIds": map[string][]int64{"category": {cat.ID}},
	})
	if updateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResp.Body)
		updateResp.Body.Close()
		t.Fatalf("update post = %d, want 200 (body %q)", updateResp.StatusCode, string(body))
	}
	var updated struct {
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	decodeJSON(t, updateResp, &updated)
	if updated.Title != "Hello from M6 (published)" || updated.Status != "publish" {
		t.Fatalf("update post response = %+v, want title/status updated", updated)
	}

	// Now the public site renders it (reusing M1/M2's public-route
	// rendering, per the task's explicit instruction).
	pubResp, err := env.client.Get(env.ts.URL + "/" + created.Slug)
	if err != nil {
		t.Fatalf("GET public published: %v", err)
	}
	pubBody, _ := io.ReadAll(pubResp.Body)
	pubResp.Body.Close()
	if pubResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /%s after publish = %d, want 200 (body %q)", created.Slug, pubResp.StatusCode, string(pubBody))
	}
	if !strings.Contains(string(pubBody), "Hello from M6 (published)") {
		t.Errorf("public page missing published title; body: %s", pubBody)
	}

	// Delete, then confirm 404 on both the public route and the admin
	// detail endpoint.
	deleteResp := env.adminJSON(http.MethodDelete, "/admin/api/posts/"+strconv.FormatInt(created.ID, 10), nil)
	deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete post = %d, want 204", deleteResp.StatusCode)
	}

	postDeleteResp, err := env.client.Get(env.ts.URL + "/" + created.Slug)
	if err != nil {
		t.Fatalf("GET public after delete: %v", err)
	}
	postDeleteResp.Body.Close()
	if postDeleteResp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /%s after delete = %d, want 404", created.Slug, postDeleteResp.StatusCode)
	}

	adminDeleteResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(created.ID, 10), nil)
	adminDeleteResp.Body.Close()
	if adminDeleteResp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /admin/api/posts/%d after delete = %d, want 404", created.ID, adminDeleteResp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 5.2: REST-only E2E scenario using an Application Password: create ->
// update (with a correct If-Unmodified-Since) -> conflict (stale
// If-Unmodified-Since) -> delete, asserting response shapes match
// design.md's status-code table.
// ---------------------------------------------------------------------------

// TestM6RESTWriteParityE2E drives Req 6's REST write parity purely over HTTP
// Basic auth via an Application Password (no session cookie at all), the
// same authentication path a real external REST client would use, and
// checks every status code design.md's table promises: 201 create, 200
// update, 409 conflict on a stale If-Unmodified-Since, 200 (not 204) delete
// with the {"deleted":true,"previous":{...}} shape Req 6.3 specifies.
func TestM6RESTWriteParityE2E(t *testing.T) {
	env := newM6Env(t)

	createAppPwBody, _ := json.Marshal(map[string]string{"name": "e2e-m6-rest"})
	appPwResp := env.adminAuthedJSON(http.MethodPost, "/wp-json/wp/v2/users/me/application-passwords", createAppPwBody)
	if appPwResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(appPwResp.Body)
		appPwResp.Body.Close()
		t.Fatalf("create application password = %d, want 201 (body %q)", appPwResp.StatusCode, string(body))
	}
	var appPw struct {
		Password string `json:"password"`
	}
	decodeJSON(t, appPwResp, &appPw)
	if appPw.Password == "" {
		t.Fatal("create application password returned empty secret")
	}

	restClient := env.ts.Client()
	restDo := func(method, path string, body []byte, headers map[string]string) *http.Response {
		t.Helper()
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, env.ts.URL+path, reader)
		if err != nil {
			t.Fatalf("new request %s %s: %v", method, path, err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.SetBasicAuth("root", appPw.Password)
		resp, err := restClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// Create.
	createBody, _ := json.Marshal(map[string]string{
		"title": "REST parity post", "content": "<p>Body.</p>",
		"excerpt": "Excerpt.", "slug": "rest-parity-post", "status": "publish",
	})
	createResp := restDo(http.MethodPost, "/wp-json/wp/v2/posts", createBody, nil)
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("REST create post = %d, want 201 (body %q)", createResp.StatusCode, string(body))
	}
	var created struct {
		ID   int64  `json:"id"`
		Date string `json:"date"`
	}
	decodeJSON(t, createResp, &created)
	if created.ID == 0 {
		t.Fatal("REST create post returned zero id")
	}

	// Capture a good If-Unmodified-Since. The REST responses carry no
	// Last-Modified response header (grimoire's REST parity is body-field
	// only, unlike a real HTTP cache-validator setup), so a client must
	// build the header itself from the body's modified_gmt field (naive
	// UTC, restDateFormat) reformatted to the HTTP-date shape
	// http.ParseTime accepts on the server side.
	getResp := restDo(http.MethodGet, "/wp-json/wp/v2/posts/"+strconv.FormatInt(created.ID, 10), nil, nil)
	var got struct {
		ModifiedGMT string `json:"modified_gmt"`
	}
	decodeJSON(t, getResp, &got)
	if got.ModifiedGMT == "" {
		t.Fatal("GET single post missing modified_gmt needed to build If-Unmodified-Since")
	}
	modTime, err := time.Parse("2006-01-02T15:04:05", got.ModifiedGMT)
	if err != nil {
		t.Fatalf("parse modified_gmt %q: %v", got.ModifiedGMT, err)
	}
	lastModified := modTime.UTC().Format(http.TimeFormat)

	// grimoire truncates modified timestamps to whole seconds (restDateFormat
	// has no sub-second component), so a same-second update wouldn't
	// actually change the stored value the stale-header check compares
	// against below. Cross a second boundary first so the "correct" update
	// genuinely advances Modified past the captured lastModified.
	time.Sleep(1100 * time.Millisecond)

	// Update with a correct If-Unmodified-Since: 200.
	updateBody, _ := json.Marshal(map[string]string{
		"title": "REST parity post (updated)", "content": "<p>Body.</p>",
		"excerpt": "Excerpt.", "slug": "rest-parity-post", "status": "publish",
	})
	updateResp := restDo(http.MethodPut, "/wp-json/wp/v2/posts/"+strconv.FormatInt(created.ID, 10), updateBody, map[string]string{"If-Unmodified-Since": lastModified})
	if updateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResp.Body)
		updateResp.Body.Close()
		t.Fatalf("REST update post = %d, want 200 (body %q)", updateResp.StatusCode, string(body))
	}
	updateResp.Body.Close()

	// Conflict: reuse the now-stale If-Unmodified-Since from before the
	// update above.
	conflictBody, _ := json.Marshal(map[string]string{
		"title": "REST parity post (conflicting)", "content": "<p>Body.</p>",
		"excerpt": "Excerpt.", "slug": "rest-parity-post", "status": "publish",
	})
	conflictResp := restDo(http.MethodPut, "/wp-json/wp/v2/posts/"+strconv.FormatInt(created.ID, 10), conflictBody, map[string]string{"If-Unmodified-Since": lastModified})
	conflictRespBody, _ := io.ReadAll(conflictResp.Body)
	conflictResp.Body.Close()
	if conflictResp.StatusCode != http.StatusConflict {
		t.Fatalf("REST update with stale If-Unmodified-Since = %d, want 409 (body %q)", conflictResp.StatusCode, string(conflictRespBody))
	}
	if !strings.Contains(string(conflictRespBody), "rest_conflict") {
		t.Errorf("409 response body missing rest_conflict code: %q", string(conflictRespBody))
	}

	// Delete: 200 with {"deleted":true,"previous":{...}}.
	deleteResp := restDo(http.MethodDelete, "/wp-json/wp/v2/posts/"+strconv.FormatInt(created.ID, 10), nil, nil)
	deleteRespBody, _ := io.ReadAll(deleteResp.Body)
	deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("REST delete post = %d, want 200 (body %q)", deleteResp.StatusCode, string(deleteRespBody))
	}
	var deleteResult struct {
		Deleted  bool           `json:"deleted"`
		Previous map[string]any `json:"previous"`
	}
	if err := json.Unmarshal(deleteRespBody, &deleteResult); err != nil {
		t.Fatalf("decode delete response: %v (body %q)", err, string(deleteRespBody))
	}
	if !deleteResult.Deleted || deleteResult.Previous == nil {
		t.Fatalf("delete response = %+v, want deleted=true with a non-nil previous", deleteResult)
	}

	// A follow-up GET confirms it is actually gone.
	afterDeleteResp := restDo(http.MethodGet, "/wp-json/wp/v2/posts/"+strconv.FormatInt(created.ID, 10), nil, nil)
	afterDeleteResp.Body.Close()
	if afterDeleteResp.StatusCode != http.StatusNotFound {
		t.Errorf("GET deleted post = %d, want 404", afterDeleteResp.StatusCode)
	}
}

// adminAuthedJSON issues a session-cookie + X-CSRF-Token authenticated JSON
// request, used only for the self-service Application Password create call
// (Req 6's REST scenario is otherwise Basic-auth-only by design).
func (e *m6Env) adminAuthedJSON(method, path string, body []byte) *http.Response {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-CSRF-Token", e.csrf)
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}
