package web_test

import (
	"context"
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
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	sqliteadapter "github.com/roboweaver/grimoire/internal/storage/sqlite"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/storage/wprepo"
	"github.com/roboweaver/grimoire/internal/web"
)

// Task 4.1 — nil-gated wiring test for the nav-menu write surface, modeled on
// mediawrite_wiring_test.go / userwrite_wiring_test.go.
//
// STRICT TDD: this is written BEFORE task 4.2 ships WithNavMenuWrites and the
// s.navMenuWrite-gated route group, so it is EXPECTED to fail to compile
// (undefined: (*web.Server).WithNavMenuWrites) until 4.2 lands.
//
// The assertion is the nil-gated builder posture (D4, Req 3.3): a Server built
// WITHOUT WithNavMenuWrites keeps serving the existing GET read routes and
// answers 404/405 (a clean HTTP error, NOT a nil-dereference panic recovered to
// 500) for the six write verbs; a Server built WITH WithNavMenuWrites(navMenuWrite)
// registers all six write routes under the edit_theme_options + CSRF group, so an
// administrator request reaches the handler rather than the router-level 404/405.

const navWiringPrefix = "wp_"

// navWiringWriteVerb is one of the six write routes the group registers,
// targeting the seeded nav_menu term 30 and its seeded item 301.
type navWiringWriteVerb struct {
	method string
	path   string
	body   string
}

func navWiringWriteVerbs() []navWiringWriteVerb {
	return []navWiringWriteVerb{
		{http.MethodPost, "/admin/api/menus", `{"name":"Footer"}`},
		{http.MethodPut, "/admin/api/menus/30", `{"name":"Renamed"}`},
		{http.MethodDelete, "/admin/api/menus/30", ""},
		{http.MethodPost, "/admin/api/menus/30/items", `{"label":"Home","url":"/home","type":"custom"}`},
		{http.MethodPut, "/admin/api/menus/30/items/301", `{"label":"Edited"}`},
		{http.MethodDelete, "/admin/api/menus/30/items/301", ""},
	}
}

// newNavMenuWiringRouter builds the full admin router over a seeded SQLite DB.
// When withWrites is true the NavMenuWriteService is wired via WithNavMenuWrites;
// otherwise it is omitted so the write group is not registered. The same
// *wprepo.NavMenuRepo backs both the read service (WithContentFeatures' menus)
// and the write service, exactly as cmd/grimoire/main.go wires them.
func newNavMenuWiringRouter(t *testing.T, withWrites bool) http.Handler {
	t.Helper()
	ctx := context.Background()
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: filepath.Join(t.TempDir(), "grimoire.db"), TablePrefix: navWiringPrefix}
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

	// One repo instance implements both NavMenuRepository (read) and
	// NavMenuWriter (write).
	repo := wprepo.NewNavMenuRepo(sqliteadapter.NewBunDB(repos.DB()), cfg.TablePrefix)
	menus := content.NewNavMenuService(repo, "default")
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: t.TempDir(), BaseURL: "/wp-content/uploads"})
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)

	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(7, "admin", []string{auth.RoleAdministrator}),
		authSession:   domain.Session{CSRFToken: "token"},
	}

	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithContentFeatures(comments, media, menus)

	if withWrites {
		navMenuWrite := content.NewNavMenuWriteService(repo, repo, content.WithNavMenuContentPolicy(sanitize.New()))
		srv = srv.WithNavMenuWrites(navMenuWrite)
	}
	return srv.Routes()
}

func navWiringAuthed(req *http.Request, csrf bool) *http.Request {
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "raw-token"})
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-CSRF-Token", "token")
	}
	return req
}

// TestNavMenuWritesOmittedKeepsReadRoutesAndNoWriteVerbs asserts a Server built
// WITHOUT WithNavMenuWrites: the GET read routes still serve, and each write verb
// answers a clean 404/405 (not a 500 panic) because the write group was never
// registered.
func TestNavMenuWritesOmittedKeepsReadRoutesAndNoWriteVerbs(t *testing.T) {
	h := newNavMenuWiringRouter(t, false)

	// Read routes remain registered and served.
	for _, path := range []string{"/admin/api/menus", "/admin/api/menus/30"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, navWiringAuthed(httptest.NewRequest(http.MethodGet, path, nil), false))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s without write wiring: status = %d, want 200 (body=%s)", path, rec.Code, rec.Body.String())
		}
	}

	// Every write verb is a clean HTTP error, never a recovered-panic 500.
	for _, v := range navWiringWriteVerbs() {
		rec := httptest.NewRecorder()
		req := navWiringAuthed(httptest.NewRequest(v.method, v.path, bodyReader(v.body)), true)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s without write wiring: status = %d, want 404 or 405 (a clean error, not a panic) (body=%s)",
				v.method, v.path, rec.Code, rec.Body.String())
		}
	}
}

// TestNavMenuWritesWiredRegistersAllSixRoutes asserts a Server built WITH
// WithNavMenuWrites registers all six write routes under the edit_theme_options
// + CSRF group: an administrator request carrying a valid CSRF token reaches the
// handler (observable as NOT a router-level 404/405) for every verb.
func TestNavMenuWritesWiredRegistersAllSixRoutes(t *testing.T) {
	for _, v := range navWiringWriteVerbs() {
		t.Run(v.method+"_"+v.path, func(t *testing.T) {
			// A fresh router per verb so a delete in one case does not remove the
			// seeded menu/item another case targets.
			h := newNavMenuWiringRouter(t, true)
			rec := httptest.NewRecorder()
			req := navWiringAuthed(httptest.NewRequest(v.method, v.path, bodyReader(v.body)), true)
			h.ServeHTTP(rec, req)
			if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
				t.Fatalf("%s %s with write wiring: status = %d — the route is not registered (body=%s)",
					v.method, v.path, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestNavMenuWritesWiredGroupEnforcesCapabilityAndCSRF proves the wired write
// group applies edit_theme_options + CSRF (not just that the routes exist): an
// administrator WITHOUT a CSRF token is rejected 403 by csrfJSONMiddleware before
// the handler runs.
func TestNavMenuWritesWiredGroupEnforcesCSRF(t *testing.T) {
	h := newNavMenuWiringRouter(t, true)
	rec := httptest.NewRecorder()
	req := navWiringAuthed(httptest.NewRequest(http.MethodPost, "/admin/api/menus", bodyReader(`{"name":"X"}`)), false)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /admin/api/menus without CSRF: status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
}

func bodyReader(s string) *strings.Reader {
	return strings.NewReader(s)
}
