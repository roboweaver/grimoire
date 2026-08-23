package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// newAdminRouter builds the full chi router with auth + admin wired, backed by a
// seeded SQLite database, plus the fakeSessions so tests can drive the principal.
func newAdminRouter(t *testing.T, fake *fakeSessions) http.Handler {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
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
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc)
	return srv.Routes()
}

// authed adds the session cookie for a principal the fake will authenticate.
func authed(req *http.Request) *http.Request {
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "raw-token"})
	return req
}

func decodeErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestAdminPageUnauthenticatedRedirects(t *testing.T) {
	h := newAdminRouter(t, &fakeSessions{})
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /admin unauth = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?redirect=%2Fadmin" {
		t.Fatalf("Location = %q, want /login?redirect=%%2Fadmin", loc)
	}
}

func TestAdminAPIUnauthenticatedJSON401(t *testing.T) {
	h := newAdminRouter(t, &fakeSessions{})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/session", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /admin/api/session unauth = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if code := decodeErrCode(t, rec); code != "unauthorized" {
		t.Fatalf("error code = %q, want unauthorized", code)
	}
}

func TestAdminPageForbiddenWithoutCapability(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(9, "sub", []string{"subscriber"})}
	h := newAdminRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/admin", nil)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /admin no-cap = %d, want 403", rec.Code)
	}
}

func TestAdminAPIForbiddenWithoutCapability(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(9, "sub", []string{"subscriber"})}
	h := newAdminRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /admin/api/stats no-cap = %d, want 403", rec.Code)
	}
	if code := decodeErrCode(t, rec); code != "forbidden" {
		t.Fatalf("error code = %q, want forbidden", code)
	}
}

func TestAdminAPISessionRequiresOnlyLogin(t *testing.T) {
	// A subscriber (no edit_posts) can still read the session endpoint (Req 2.6).
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(9, "sub", []string{"subscriber"})}
	h := newAdminRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/admin/api/session", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/session subscriber = %d, want 200", rec.Code)
	}
}

func TestAdminPageServedWithCapability(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(7, "editor", []string{"editor"})}
	h := newAdminRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/admin/posts/1", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/posts/1 editor = %d, want 200 (SPA index)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
}

func TestAdminAPIStatsServedWithCapability(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(7, "editor", []string{"editor"})}
	h := newAdminRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/stats editor = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestAdminAPIMethodNotAllowed(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(7, "editor", []string{"editor"})}
	h := newAdminRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodPost, "/admin/api/stats", nil)))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /admin/api/stats = %d, want 405", rec.Code)
	}
	if code := decodeErrCode(t, rec); code != "method_not_allowed" {
		t.Fatalf("error code = %q, want method_not_allowed", code)
	}
}

func TestAdminAPIUnknownPathJSON404(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(7, "editor", []string{"editor"})}
	h := newAdminRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/admin/api/nope", nil)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /admin/api/nope = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want JSON", ct)
	}
	if code := decodeErrCode(t, rec); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

func TestPublicSlugStillResolves(t *testing.T) {
	// The /admin group must not shadow the public catch-all /{slug}.
	h := newAdminRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /hello-1 = %d, want 200 (public post)", rec.Code)
	}
}
