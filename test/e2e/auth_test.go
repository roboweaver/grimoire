package e2e_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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

var inputValueRE = regexp.MustCompile(`name="([^"]+)"\s+value="([^"]*)"`)

// formInputs extracts hidden/text input name=value pairs from an HTML form.
func formInputs(body string) map[string]string {
	out := map[string]string{}
	for _, m := range inputValueRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// TestAuthEndToEnd exercises the full M2 auth stack on SQLite through the real
// SessionManager and server: createadmin bootstrap -> GET /login -> POST /login
// (bcrypt verify + session create) -> authenticated request -> POST /logout
// (session revoke).
func TestAuthEndToEnd(t *testing.T) {
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

	// createadmin bootstrap (mirrors grimoire-cli createadmin). Uses a login
	// distinct from the seed fixture's passwordless "admin" row.
	const adminLogin, adminPass = "root", "correct-horse-battery-staple"
	users := content.NewUserService(repos.Users, repos.UserMeta, dbcfg.TablePrefix)
	if _, err := users.Bootstrap(ctx, domain.User{
		Login: adminLogin, Nicename: adminLogin, DisplayName: "Admin", Email: "admin@example.test",
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
	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(sm, web.AuthConfig{})

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	// GET /login -> form + CSRF token.
	resp, err := client.Get(ts.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", resp.StatusCode)
	}
	inputs := formInputs(string(body))
	csrf := inputs["csrf_token"]
	if csrf == "" {
		t.Fatal("GET /login form missing csrf_token")
	}

	// POST /login with valid credentials -> 303 + session cookie.
	form := url.Values{"log": {adminLogin}, "pwd": {adminPass}, "csrf_token": {csrf}, "redirect": {"/"}}
	resp, err = client.PostForm(ts.URL+"/login", form)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("POST /login Location = %q, want /", loc)
	}

	// The jar now holds the session cookie; recover the raw token to prove the
	// server-side session exists via the real SessionManager.
	u, _ := url.Parse(ts.URL)
	var token string
	for _, c := range jar.Cookies(u) {
		if c.Name == "grimoire_session" {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("no session cookie set after login")
	}
	principal, sess, err := sm.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate after login: %v", err)
	}
	if principal.Login != adminLogin || !principal.Can("manage_options") {
		t.Fatalf("principal = %+v, want admin with manage_options", principal)
	}

	// Authenticated public request succeeds with the session cookie attached.
	resp, err = client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / authenticated: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / authenticated = %d, want 200", resp.StatusCode)
	}

	// POST /logout with the per-session synchronizer token -> 303 + revocation.
	resp, err = client.PostForm(ts.URL+"/logout", url.Values{"csrf_token": {sess.CSRFToken}})
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /logout = %d, want 303", resp.StatusCode)
	}
	if _, _, err := sm.Authenticate(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("Authenticate after logout err = %v, want ErrNoSession", err)
	}
}

// TestLoginRejectsWrongPassword confirms invalid credentials produce a generic
// 401 with no session cookie, end to end.
func TestLoginRejectsWrongPassword(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	dbcfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(dbcfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })
	migFS, _ := storage.MigrationsFS(dbcfg.Vendor)
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, dbcfg.Vendor, dbcfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	users := content.NewUserService(repos.Users, repos.UserMeta, dbcfg.TablePrefix)
	if _, err := users.Bootstrap(ctx, domain.User{Login: "admin", Nicename: "admin", Email: "a@b.test"}, "right-pass", auth.RoleAdministrator); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	sm := &auth.SessionManager{Users: repos.Users, Meta: repos.UserMeta, Sessions: repos.Sessions, Prefix: dbcfg.TablePrefix}
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng, nil,
	).WithAuth(sm, web.AuthConfig{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, _ := client.Get(ts.URL + "/login")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := formInputs(string(body))["csrf_token"]

	resp, err = client.PostForm(ts.URL+"/login", url.Values{"log": {"admin"}, "pwd": {"wrong-pass"}, "csrf_token": {csrf}})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /login wrong pw = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Invalid username or password") {
		t.Error("expected generic invalid-credentials message")
	}
	u, _ := url.Parse(ts.URL)
	for _, c := range jar.Cookies(u) {
		if c.Name == "grimoire_session" {
			t.Fatal("session cookie must not be set on failed login")
		}
	}
}
