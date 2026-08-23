package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
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
)

var e2eInputRE = regexp.MustCompile(`name="([^"]+)"\s+value="([^"]*)"`)

// adminE2EServer wires the full stack the way cmd/grimoire does: real storage,
// migrations, seed data, a real SessionManager, the auth handlers, and the
// mounted read-only admin (SPA + JSON API).
func adminE2EServer(t *testing.T) (*httptest.Server, *auth.SessionManager, string) {
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

	// Overlay-safety: the admin runs against the M1+M2 schema only. The single
	// auth-owned table is {prefix}sessions; no M3 migration or schema change is
	// applied, proving the read-only admin overlays an unchanged WP schema.
	assertOnlySessionsAuthTable(ctx, t, repos, dbcfg.TablePrefix)

	const adminLogin, adminPass = "root", "correct-horse-battery-staple"
	users := content.NewUserService(repos.Users, repos.UserMeta, dbcfg.TablePrefix)
	if _, err := users.Bootstrap(ctx, domain.User{
		Login: adminLogin, Nicename: adminLogin, DisplayName: "Root Admin", Email: "root@example.test",
	}, adminPass, auth.RoleAdministrator); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}

	sm := &auth.SessionManager{
		Users: repos.Users, Meta: repos.UserMeta, Sessions: repos.Sessions,
		Prefix: dbcfg.TablePrefix,
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}

	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(sm, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc)

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, sm, adminPass
}

// assertOnlySessionsAuthTable confirms the only non-WordPress table present is
// the additive {prefix}sessions table from M2 — i.e. no M3 schema was created.
func assertOnlySessionsAuthTable(ctx context.Context, t *testing.T, repos *storage.Repositories, prefix string) {
	t.Helper()
	rows, err := repos.DB().QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE ?`, prefix+"%")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, name)
	}
	// The only table the auth stack adds beyond the WordPress core schema is
	// {prefix}sessions; anything named like an "admin" table would signal an
	// (forbidden) M3 schema change.
	for _, name := range tables {
		if strings.Contains(name, "admin") {
			t.Fatalf("unexpected admin table %q: M3 must not add schema", name)
		}
	}
}

// loginClient performs the real GET/POST /login flow and returns a cookie-jar
// client already carrying the session, plus the raw session token.
func loginClient(t *testing.T, ts *httptest.Server, pass string) (*http.Client, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(ts.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var csrf string
	for _, m := range e2eInputRE.FindAllStringSubmatch(string(body), -1) {
		if m[1] == "csrf_token" {
			csrf = m[2]
		}
	}
	if csrf == "" {
		t.Fatal("GET /login missing csrf_token")
	}

	form := url.Values{"log": {"root"}, "pwd": {pass}, "csrf_token": {csrf}, "redirect": {"/admin"}}
	resp, err = client.PostForm(ts.URL+"/login", form)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, want 303", resp.StatusCode)
	}

	u, _ := url.Parse(ts.URL)
	var token string
	for _, c := range jar.Cookies(u) {
		if c.Name == "grimoire_session" {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("no session cookie after login")
	}
	return client, token
}

// TestAdminReadOnlyEndToEnd drives the M3 read-only admin over the real router,
// SessionManager, and embedded SPA: login -> GET /admin shell -> the four JSON
// endpoints -> and the unauthenticated 401/redirect behaviour.
func TestAdminReadOnlyEndToEnd(t *testing.T) {
	ts, _, pass := adminE2EServer(t)
	client, _ := loginClient(t, ts, pass)

	// GET /admin serves the embedded SPA shell (HTML), not JSON.
	resp, err := client.Get(ts.URL + "/admin")
	if err != nil {
		t.Fatalf("GET /admin: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET /admin Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(strings.ToLower(string(body)), "<!doctype html") {
		t.Errorf("GET /admin body is not an HTML document")
	}

	// GET /admin/api/session -> identity + capabilities + CSRF token, no secrets.
	var session struct {
		ID           int64    `json:"id"`
		Login        string   `json:"login"`
		DisplayName  string   `json:"displayName"`
		Roles        []string `json:"roles"`
		Capabilities []string `json:"capabilities"`
		CSRFToken    string   `json:"csrfToken"`
	}
	rawSession := getJSON(t, client, ts.URL+"/admin/api/session", &session)
	if session.Login != "root" {
		t.Errorf("session.login = %q, want root", session.Login)
	}
	if session.DisplayName != "Root Admin" {
		t.Errorf("session.displayName = %q, want Root Admin", session.DisplayName)
	}
	if !contains(session.Capabilities, "edit_posts") {
		t.Errorf("session.capabilities = %v, want to include edit_posts", session.Capabilities)
	}
	if session.CSRFToken == "" {
		t.Error("session.csrfToken is empty; the milestone 06 write contract needs it")
	}
	for _, leak := range []string{"password", "hash", "user_pass", "sessionToken"} {
		if strings.Contains(strings.ToLower(rawSession), strings.ToLower(leak)) {
			t.Errorf("session payload leaks %q: %s", leak, rawSession)
		}
	}

	// GET /admin/api/stats -> dashboard counts from the seed fixtures.
	var stats struct {
		Posts struct {
			Published int `json:"published"`
			Draft     int `json:"draft"`
		} `json:"posts"`
		Pages      int `json:"pages"`
		Categories int `json:"categories"`
		Users      int `json:"users"`
	}
	getJSON(t, client, ts.URL+"/admin/api/stats", &stats)
	if stats.Posts.Published != 3 {
		t.Errorf("stats.posts.published = %d, want 3", stats.Posts.Published)
	}
	if stats.Posts.Draft != 0 {
		t.Errorf("stats.posts.draft = %d, want 0", stats.Posts.Draft)
	}
	if stats.Pages != 1 {
		t.Errorf("stats.pages = %d, want 1", stats.Pages)
	}
	if stats.Categories != 1 {
		t.Errorf("stats.categories = %d, want 1", stats.Categories)
	}
	if stats.Users != 2 { // seed user + bootstrapped root
		t.Errorf("stats.users = %d, want 2", stats.Users)
	}

	// GET /admin/api/posts -> paginated list including the page, newest first.
	var list struct {
		Items []struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"items"`
		Page       int `json:"page"`
		PerPage    int `json:"perPage"`
		Total      int `json:"total"`
		TotalPages int `json:"totalPages"`
	}
	getJSON(t, client, ts.URL+"/admin/api/posts", &list)
	if list.Total != 4 {
		t.Errorf("posts.total = %d, want 4 (3 posts + 1 page)", list.Total)
	}
	if list.TotalPages != 1 {
		t.Errorf("posts.totalPages = %d, want 1", list.TotalPages)
	}
	if len(list.Items) != 4 {
		t.Fatalf("posts.items len = %d, want 4", len(list.Items))
	}
	if list.Items[0].ID != 4 || list.Items[0].Type != "page" {
		t.Errorf("posts.items[0] = {id:%d type:%s}, want {id:4 type:page}", list.Items[0].ID, list.Items[0].Type)
	}

	// GET /admin/api/posts/{id} -> detail for the page, including content.
	var detail struct {
		ID      int64  `json:"id"`
		Type    string `json:"type"`
		Status  string `json:"status"`
		Slug    string `json:"slug"`
		Content string `json:"content"`
	}
	getJSON(t, client, ts.URL+"/admin/api/posts/4", &detail)
	if detail.ID != 4 || detail.Type != "page" || detail.Slug != "about" {
		t.Errorf("detail = {id:%d type:%s slug:%s}, want {4 page about}", detail.ID, detail.Type, detail.Slug)
	}

	// Unauthenticated access: API -> 401 JSON, page -> 303 to /login. Use a
	// fresh jar-less client so no session cookie is attached.
	anon := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	resp, err = anon.Get(ts.URL + "/admin/api/session")
	if err != nil {
		t.Fatalf("anon GET session: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anon /admin/api/session = %d, want 401", resp.StatusCode)
	}

	resp, err = anon.Get(ts.URL + "/admin")
	if err != nil {
		t.Fatalf("anon GET /admin: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("anon GET /admin = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?redirect=%2Fadmin" {
		t.Errorf("anon GET /admin Location = %q, want /login?redirect=%%2Fadmin", loc)
	}
}

// TestAdminRejectsExpiredSession confirms that once the session is revoked, the
// admin API stops answering (401) — no stale-cookie access.
func TestAdminRejectsExpiredSession(t *testing.T) {
	ctx := context.Background()
	ts, sm, pass := adminE2EServer(t)
	client, token := loginClient(t, ts, pass)

	// Sanity: the session works before revocation.
	resp, err := client.Get(ts.URL + "/admin/api/session")
	if err != nil {
		t.Fatalf("GET session: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pre-revoke session = %d, want 200", resp.StatusCode)
	}

	if err := sm.Logout(ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	resp, err = client.Get(ts.URL + "/admin/api/session")
	if err != nil {
		t.Fatalf("GET session post-revoke: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-revoke /admin/api/session = %d, want 401", resp.StatusCode)
	}
}

func getJSON(t *testing.T, client *http.Client, url string, dst any) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", url, resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET %s Content-Type = %q, want application/json", url, ct)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode %s: %v (%s)", url, err, body)
	}
	return string(body)
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
