package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

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
	"github.com/roboweaver/grimoire/pkg/extensions"
)

// m5Env mirrors newM4Env but wires the full M5 surface exactly as
// cmd/grimoire/main.go does: content.RESTMapper, *auth.ApplicationPasswords,
// WithREST, and WithApplicationPasswords, on top of the M1-M4 stack. It is
// the e2e-level rig for Milestone 5: every test in this file exercises the
// real HTTP boundary (httptest.Server) against a fully wired server, not
// internal/web's in-package handler tests.
type m5Env struct {
	t      *testing.T
	ctx    context.Context
	repos  *storage.Repositories
	dbcfg  config.DatabaseConfig
	ts     *httptest.Server
	client *http.Client
	csrf   string
}

func newM5Env(t *testing.T) *m5Env {
	t.Helper()
	ctx := context.Background()
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

	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithContentFeatures(comments, media, menus).
		WithAuth(sm, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithREST(restMapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(appPasswords, true, "")

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	env := &m5Env{t: t, ctx: ctx, repos: repos, dbcfg: dbcfg, ts: ts, client: client}
	env.login(adminLogin, adminPass)
	return env
}

// login authenticates the admin client and captures the session CSRF token
// via the real /admin/api/session endpoint (mirrors the SPA bootstrap).
func (e *m5Env) login(loginName, password string) {
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

func (e *m5Env) restJSON(method, path string, body io.Reader) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, body)
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

// ---------------------------------------------------------------------------
// 9.2 acceptance: server boots with defaults; /wp-json/wp/v2/ is reachable
// out of the box with zero extensions registered.
// ---------------------------------------------------------------------------

// TestM5RESTBootsWithDefaultsNoExtensions boots the full M5 wiring (mirroring
// cmd/grimoire/main.go exactly: content.RESTMapper, *auth.ApplicationPasswords,
// WithREST, WithApplicationPasswords) with zero pkg/extensions hooks
// registered, and confirms the REST namespace, a real resource collection,
// and the pre-existing public/admin routes are all reachable unmodified.
// This is the e2e-level regression guard called out by Phase 9's acceptance
// criteria and Phase 10.1's "zero extensions registered" sweep.
func TestM5RESTBootsWithDefaultsNoExtensions(t *testing.T) {
	env := newM5Env(t)

	cases := []struct {
		path       string
		wantStatus int
	}{
		{"/wp-json/", http.StatusOK},
		{"/wp-json/wp/v2/", http.StatusOK},
		{"/wp-json/wp/v2/posts", http.StatusOK},
		{"/wp-json/wp/v2/pages", http.StatusOK},
		{"/wp-json/wp/v2/comments", http.StatusOK},
		{"/wp-json/wp/v2/media", http.StatusOK},
		{"/wp-json/wp/v2/users", http.StatusOK},
		{"/", http.StatusOK},
		{"/healthz", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := env.client.Get(env.ts.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("GET %s = %d, want %d (body %q)", tc.path, resp.StatusCode, tc.wantStatus, string(body))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Application Password self-service create -> Basic-auth REST use -> revoke.
// ---------------------------------------------------------------------------

// TestM5ApplicationPasswordFullLifecycleE2E exercises the entire Application
// Password story end-to-end over real HTTP: create one via the session+CSRF
// gated self-service endpoint, use its plaintext secret exactly once as HTTP
// Basic auth against a REST endpoint (proving wp_fast_hash verification
// round-trips against the httptest loopback server, which counts as a
// permitted non-TLS transport per Req 8.9's loopback exception), confirm the
// secret is accepted, then revoke it and confirm the same secret is rejected
// afterward. Also confirms a bad Basic secret is rejected with 401 even
// though this client also carries a valid session cookie (Req 8.6 — must
// not be softened).
func TestM5ApplicationPasswordFullLifecycleE2E(t *testing.T) {
	env := newM5Env(t)

	createBody, _ := json.Marshal(map[string]string{"name": "e2e-test-app"})
	resp := env.restJSON(http.MethodPost, "/wp-json/wp/v2/users/me/application-passwords", bytes.NewReader(createBody))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create application password = %d, want 201 (body %q)", resp.StatusCode, string(body))
	}
	var created struct {
		UUID     string `json:"uuid"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.UUID == "" || created.Password == "" {
		t.Fatalf("create response missing uuid/password: %+v", created)
	}

	// A fresh client with no session cookie: the Application Password alone
	// must be sufficient to authenticate a REST request via HTTP Basic.
	basicClient := env.ts.Client()
	basicReq, err := http.NewRequest(http.MethodGet, env.ts.URL+"/wp-json/wp/v2/users/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	basicReq.SetBasicAuth("root", created.Password)
	basicResp, err := basicClient.Do(basicReq)
	if err != nil {
		t.Fatalf("GET /users/me via app password: %v", err)
	}
	basicBody, _ := io.ReadAll(basicResp.Body)
	basicResp.Body.Close()
	if basicResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /users/me via app password = %d, want 200 (body %q)", basicResp.StatusCode, string(basicBody))
	}
	var whoami struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(basicBody, &whoami); err != nil {
		t.Fatalf("decode /users/me: %v", err)
	}
	if whoami.Slug != "root" {
		t.Errorf("/users/me slug = %q, want root", whoami.Slug)
	}

	// Invalid Basic credentials on the session-cookie-carrying client must
	// be rejected with 401 immediately, even though a valid session cookie
	// is also present (Req 8.6, not to be softened).
	badReq, err := http.NewRequest(http.MethodGet, env.ts.URL+"/wp-json/wp/v2/users/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	badReq.SetBasicAuth("root", "totally-wrong-secret")
	badResp, err := env.client.Do(badReq)
	if err != nil {
		t.Fatalf("GET /users/me with bad basic auth: %v", err)
	}
	badBody, _ := io.ReadAll(badResp.Body)
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /users/me with bad basic auth + valid session cookie = %d, want 401 (body %q)", badResp.StatusCode, string(badBody))
	}
	if !strings.Contains(string(badBody), "rest_invalid_credentials") {
		t.Errorf("bad basic auth response body missing rest_invalid_credentials code: %q", string(badBody))
	}

	// Revoke, then confirm the (still session-authenticated) revoke
	// succeeds and the old secret no longer authenticates.
	revokeResp := env.restJSON(http.MethodDelete, "/wp-json/wp/v2/users/me/application-passwords/"+created.UUID, nil)
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("revoke application password = %d, want 200", revokeResp.StatusCode)
	}

	postRevokeReq, err := http.NewRequest(http.MethodGet, env.ts.URL+"/wp-json/wp/v2/users/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	postRevokeReq.SetBasicAuth("root", created.Password)
	postRevokeResp, err := basicClient.Do(postRevokeReq)
	if err != nil {
		t.Fatalf("GET /users/me after revoke: %v", err)
	}
	postRevokeResp.Body.Close()
	if postRevokeResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /users/me with revoked app password = %d, want 401", postRevokeResp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Extension hooks fire end-to-end over real HTTP (comment.submitted,
// rest.pre_dispatch, rest.response, render.post_html), all four
// Requirement-11 hook points wired across M5's phases.
// ---------------------------------------------------------------------------

// m5HookCalls records how many times each hook has fired for the whole test
// binary, guarded by a mutex since extensions.DoAction/ApplyFilters may run
// concurrently in principle (goroutine-safe registry). pkg/extensions's
// registry is process-global and append-only (no unregister), so
// registration is done exactly once (guarded by sync.Once) and every
// counter is read via before/after deltas around the specific request under
// test -- that makes the assertions immune to any other test in this binary
// also happening to create comments, fetch REST collections, or render
// single/page HTML, regardless of test execution order.
type m5HookCalls struct {
	mu    sync.Mutex
	fired map[string]int
}

func (h *m5HookCalls) record(hook string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fired[hook]++
}

func (h *m5HookCalls) count(hook string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.fired[hook]
}

var (
	m5HooksRegisterOnce sync.Once
	m5Hooks             = &m5HookCalls{fired: map[string]int{}}
)

// registerM5HooksOnce registers one instance of each Requirement-11 hook,
// exactly once for the whole test binary. Each hook unconditionally records
// its firing in the shared, process-global m5Hooks counters; render.post_html
// also appends a detectable HTML comment to every single/page render so its
// effect on the response body can be asserted. Note this hook's context
// argument is the *server-side* request context created fresh by the
// httptest server for each inbound connection -- values set on the client's
// context (e.g. via a custom http.RoundTripper) never cross the wire, so
// per-request scoping is intentionally done via before/after deltas in the
// test bodies below rather than any context- or header-based marker.
func registerM5HooksOnce() {
	m5HooksRegisterOnce.Do(func() {
		extensions.RegisterAction(content.HookCommentSubmitted, func(_ context.Context, _ any) {
			m5Hooks.record(content.HookCommentSubmitted)
		})
		extensions.RegisterAction("rest.pre_dispatch", func(_ context.Context, _ any) {
			m5Hooks.record("rest.pre_dispatch")
		})
		extensions.RegisterFilter("rest.response", func(_ context.Context, v any) (any, error) {
			m5Hooks.record("rest.response")
			return v, nil
		})
		extensions.RegisterFilter("render.post_html", func(_ context.Context, v []byte) ([]byte, error) {
			m5Hooks.record("render.post_html")
			return append(v, []byte("<!--m5-e2e-render-marker-->")...), nil
		})
	})
}

// TestM5ExtensionHooksFireEndToEnd registers all four Requirement-11 hooks
// once for the process, then confirms each fires (and, for the filters,
// visibly affects behavior) when driven through real HTTP requests against
// the fully wired server -- not just via internal/web's in-package handler
// tests. This is the e2e-level coverage explicitly called out as missing
// from M4's review and required not to be skipped for M5.
func TestM5ExtensionHooksFireEndToEnd(t *testing.T) {
	registerM5HooksOnce()
	env := newM5Env(t)

	t.Run("comment.submitted", func(t *testing.T) {
		before := m5Hooks.count(content.HookCommentSubmitted)
		body, _ := json.Marshal(map[string]any{
			"post": 1, "author_name": "E2E Hooks", "author_email": "hooks@example.test", "content": "hook check",
		})
		resp := env.restJSON(http.MethodPost, "/wp-json/wp/v2/comments", bytes.NewReader(body))
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /comments = %d, want 201 (body %q)", resp.StatusCode, string(respBody))
		}
		if got := m5Hooks.count(content.HookCommentSubmitted) - before; got != 1 {
			t.Errorf("comment.submitted fired %d times, want 1", got)
		}
	})

	t.Run("rest.pre_dispatch and rest.response", func(t *testing.T) {
		before := m5Hooks.count("rest.pre_dispatch")
		beforeResp := m5Hooks.count("rest.response")
		resp, err := env.client.Get(env.ts.URL + "/wp-json/wp/v2/posts")
		if err != nil {
			t.Fatalf("GET /posts: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /posts = %d, want 200", resp.StatusCode)
		}
		if got := m5Hooks.count("rest.pre_dispatch") - before; got != 1 {
			t.Errorf("rest.pre_dispatch fired %d times for this request, want 1", got)
		}
		if got := m5Hooks.count("rest.response") - beforeResp; got != 1 {
			t.Errorf("rest.response fired %d times for this request, want 1", got)
		}
	})

	t.Run("render.post_html", func(t *testing.T) {
		before := m5Hooks.count("render.post_html")
		resp, err := env.client.Get(env.ts.URL + "/about")
		if err != nil {
			t.Fatalf("GET /about: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /about = %d, want 200", resp.StatusCode)
		}
		if !strings.Contains(string(body), "m5-e2e-render-marker") {
			t.Error("GET /about body missing render.post_html marker: filter did not visibly affect the response")
		}
		if got := m5Hooks.count("render.post_html") - before; got != 1 {
			t.Errorf("render.post_html fired %d times, want 1", got)
		}
	})
}

// ---------------------------------------------------------------------------
// REST comment write reuses the existing M4 CommentService.Create
// unmodified: WP-shaped response envelope sanity check.
// ---------------------------------------------------------------------------

// TestM5RESTCommentCreateWPShapedResponse confirms the one real REST write
// endpoint (POST /wp-json/wp/v2/comments) returns a WordPress-shaped
// envelope end-to-end, and that the created comment is subsequently visible
// via GET, over real HTTP against the fully wired server.
func TestM5RESTCommentCreateWPShapedResponse(t *testing.T) {
	env := newM5Env(t)

	body, _ := json.Marshal(map[string]any{
		"post": 1, "author_name": "Shape Check", "author_email": "shape@example.test", "content": "checking the envelope",
	})
	resp := env.restJSON(http.MethodPost, "/wp-json/wp/v2/comments", bytes.NewReader(body))
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /comments = %d, want 201 (body %q)", resp.StatusCode, string(respBody))
	}
	var created struct {
		ID    int64          `json:"id"`
		Post  int64          `json:"post"`
		Links map[string]any `json:"_links"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		t.Fatalf("decode created comment: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("created comment missing id")
	}
	if created.Links == nil {
		t.Error("created comment missing _links envelope")
	}

	getResp, err := env.client.Get(env.ts.URL + "/wp-json/wp/v2/comments/" + strconv.FormatInt(created.ID, 10))
	if err != nil {
		t.Fatalf("GET /comments/%d: %v", created.ID, err)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /comments/%d = %d, want 200 (body %q)", created.ID, getResp.StatusCode, string(getBody))
	}

	// Writes other than POST /comments and the app-password endpoints are
	// deferred to a later milestone (Req 7.5) -- spot-check one at the e2e
	// boundary.
	putResp := env.restJSON(http.MethodPut, "/wp-json/wp/v2/posts/1", nil)
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusNotImplemented {
		t.Errorf("PUT /posts/1 = %d, want 501", putResp.StatusCode)
	}
}
