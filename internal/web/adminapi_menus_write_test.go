package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	sqlitemig "github.com/roboweaver/grimoire/internal/storage/migrations/sqlite"
	sqliteadapter "github.com/roboweaver/grimoire/internal/storage/sqlite"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/storage/wprepo"

	"github.com/roboweaver/grimoire/internal/config"
)

// Task 4.1 — web-layer tests for the nav-menu write surface (M10.F).
//
// STRICT TDD: these tests are written BEFORE task 4.2 ships the handlers, the
// WithNavMenuWrites builder, and the s.navMenuWrite field, so they are EXPECTED
// to fail to compile (undefined: WithNavMenuWrites / s.navMenuWrite) until 4.2
// lands. They mirror the media/user write harness verbatim: a *Server built
// over a real migrated+seeded SQLite database (the adminapi_comments_test.go
// idiom), session/role injected via fakeSessions, CSRF driven through the real
// router (SessionMiddleware -> requireLoginJSON -> requireCapabilityJSON ->
// csrfJSONMiddleware -> handler), and the capability matrix asserted per route.
//
// The single shared *wprepo.NavMenuRepo backs BOTH the read service (s.menus,
// a content.NavMenuService) and the write service (s.navMenuWrite, a
// content.NavMenuWriteService), so a write round-trips through the existing read
// path on one instance — exactly how cmd/grimoire/main.go wires them and how the
// storage round-trip tests (task 2.1) back the one repo. Pure Go, SQLite-only,
// no new env var.

const navTestPrefix = "wp_"

// newNavMenuWriteServer builds a *Server backed by a real migrated and
// fixture-seeded SQLite database, wired with a live read NavMenuService and a
// live NavMenuWriteService over the SAME *wprepo.NavMenuRepo, plus a fakeSessions
// principal carrying the supplied roles. The storagetest fixtures seed the
// nav_menu term_id 30 ("Primary"/"primary", term_taxonomy_id 40) with item posts
// 301-304, so read-gate assertions have an existing menu to read.
func newNavMenuWriteServer(t *testing.T, roles ...string) *Server {
	t.Helper()
	ctx := context.Background()
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: t.TempDir() + "/grimoire.db", TablePrefix: navTestPrefix}

	db, err := sqliteadapter.Open(cfg)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := migrate.Apply(ctx, db, sqlitemig.FS(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, db, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}

	// One repo instance is both the reader and the writer (it implements
	// domain.NavMenuRepository AND domain.NavMenuWriter).
	repo := wprepo.NewNavMenuRepo(sqliteadapter.NewBunDB(db), cfg.TablePrefix)

	// A single process policy threads into the write service, as main.go wires it.
	policy := sanitize.New()

	srv := NewServer(nil, nil, nil, nil, nil)
	srv.log = slog.Default()
	srv.menus = content.NewNavMenuService(repo, "default")
	srv.navMenuWrite = content.NewNavMenuWriteService(repo, repo, content.WithNavMenuContentPolicy(policy))
	if roles != nil {
		srv.auth = fakeSessions{
			p: auth.NewPrincipal(7, "acting", roles),
			s: domain.Session{CSRFToken: "token"},
		}
	}
	return srv
}

// navWriteRequest issues a write against the fully-wired admin router, driving
// the real middleware chain. When sendCSRF is true a valid X-CSRF-Token is set;
// a session cookie is always attached so SessionMiddleware resolves the
// fakeSessions principal.
func navWriteRequest(t *testing.T, srv *Server, method, path, body string, sendCSRF bool) *httptest.ResponseRecorder {
	t.Helper()
	r := srv.SessionMiddleware(srv.adminAPIRouter())
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	if sendCSRF {
		req.Header.Set("X-CSRF-Token", "token")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// countMenuTerms returns the number of nav_menu terms currently stored, so a
// forbidden write can be asserted to have persisted no new row.
func countMenuTerms(t *testing.T, srv *Server) int {
	t.Helper()
	menus, err := srv.menus.List(context.Background())
	if err != nil {
		t.Fatalf("menus.List: %v", err)
	}
	return len(menus)
}

// ---------------------------------------------------------------------------
// Capability matrix (Req 7.1, 7.2, 10.1): administrator (holds
// edit_theme_options) allowed; a non-administrator holding only edit_posts
// (contributor/author) forbidden -> 403 with NO row written.
// ---------------------------------------------------------------------------

// navWriteRoute is one of the six write routes, with a body that reaches the
// service (so an allowed administrator produces a success, not a 400). The
// {id}/{itemId} segments target the seeded menu 30 / its seeded item 301.
type navWriteRoute struct {
	name   string
	method string
	path   string
	body   string
	// wantAllowed is the status an administrator (edit_theme_options) gets.
	wantAllowed int
}

func navWriteRoutes() []navWriteRoute {
	return []navWriteRoute{
		{"create_menu", http.MethodPost, "/menus", `{"name":"Footer"}`, http.StatusCreated},
		{"update_menu", http.MethodPut, "/menus/30", `{"name":"Renamed"}`, http.StatusOK},
		{"delete_menu", http.MethodDelete, "/menus/30", "", http.StatusNoContent},
		{"create_item", http.MethodPost, "/menus/30/items", `{"label":"Home","url":"/home","type":"custom"}`, http.StatusCreated},
		{"update_item", http.MethodPut, "/menus/30/items/301", `{"label":"Edited"}`, http.StatusOK},
		{"delete_item", http.MethodDelete, "/menus/30/items/301", "", http.StatusNoContent},
	}
}

// TestAdminMenuWriteCapabilityMatrixAdministratorAllowed asserts an
// administrator (who holds edit_theme_options) is permitted on every write
// route and gets the success status the handler mapping pins.
func TestAdminMenuWriteCapabilityMatrixAdministratorAllowed(t *testing.T) {
	for _, rt := range navWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
			rec := navWriteRequest(t, srv, rt.method, rt.path, rt.body, true)
			if rec.Code != rt.wantAllowed {
				t.Fatalf("administrator %s %s: status = %d, want %d (body=%s)",
					rt.method, rt.path, rec.Code, rt.wantAllowed, rec.Body.String())
			}
		})
	}
}

// TestAdminMenuWriteCapabilityMatrixEditPostsForbidden asserts a
// non-administrator holding ONLY edit_posts (a contributor — edit_posts but not
// edit_theme_options) is forbidden (403) on every write route, and that no
// nav_menu term was created/removed by the rejected write (the gate runs before
// the handler, so stored state is unchanged).
func TestAdminMenuWriteCapabilityMatrixEditPostsForbidden(t *testing.T) {
	for _, rt := range navWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			// RoleContributor holds edit_posts but NOT edit_theme_options.
			srv := newNavMenuWriteServer(t, auth.RoleContributor)
			if !auth.NewPrincipal(7, "c", []string{auth.RoleContributor}).Can("edit_posts") {
				t.Fatal("precondition: contributor must hold edit_posts")
			}
			if auth.NewPrincipal(7, "c", []string{auth.RoleContributor}).Can("edit_theme_options") {
				t.Fatal("precondition: contributor must NOT hold edit_theme_options")
			}
			before := countMenuTerms(t, srv)
			rec := navWriteRequest(t, srv, rt.method, rt.path, rt.body, true)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("contributor %s %s: status = %d, want 403 (body=%s)",
					rt.method, rt.path, rec.Code, rec.Body.String())
			}
			assertJSONError(t, rec, "forbidden")
			if after := countMenuTerms(t, srv); after != before {
				t.Fatalf("forbidden %s %s changed menu count: before=%d after=%d", rt.method, rt.path, before, after)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Read gate unmoved (Req 7.4, 9.4): after the write group is wired, the read
// routes still succeed for an edit_posts caller.
// ---------------------------------------------------------------------------

func TestAdminMenuReadGateUnmovedForEditPostsCaller(t *testing.T) {
	srv := newNavMenuWriteServer(t, auth.RoleContributor) // edit_posts, not edit_theme_options
	r := srv.SessionMiddleware(srv.adminAPIRouter())

	for _, path := range []string{"/menus", "/menus/30"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s for edit_posts caller: status = %d, want 200 (body=%s)",
				path, rec.Code, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Content safety at the handler boundary (Req 10.2, 4).
// ---------------------------------------------------------------------------

// TestAdminMenuWriteAdminMarkupTitleRoundTripsByteIdentical asserts the M10a
// frozen contract: an administrator holds unfiltered_html -> tier C, which
// PRESERVES markup for PostTitle byte-for-byte. So a menu name and an item label
// authored by an administrator round-trip through a subsequent GET unchanged.
// (Plain-text reduction is NOT asserted for an administrator writer — writes
// require edit_theme_options, which is administrator-only, and the admin tier is
// C. This matches the note in task 3.2's content-safety assertion.)
func TestAdminMenuWriteAdminMarkupTitleRoundTripsByteIdentical(t *testing.T) {
	srv := newNavMenuWriteServer(t, auth.RoleAdministrator)

	const markupName = `Menu <b>bold</b> <script>x</script>`
	createRec := navWriteRequest(t, srv, http.MethodPost, "/menus",
		`{"name":`+jsonString(markupName)+`}`, true)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create menu: status = %d, want 201 (body=%s)", createRec.Code, createRec.Body.String())
	}
	menuID := decodeMenuID(t, createRec)

	const markupLabel = `Home <i>italic</i> <script>y</script>`
	itemRec := navWriteRequest(t, srv, http.MethodPost, pathMenuItems(menuID),
		`{"label":`+jsonString(markupLabel)+`,"url":"/home","type":"custom"}`, true)
	if itemRec.Code != http.StatusCreated {
		t.Fatalf("create item: status = %d, want 201 (body=%s)", itemRec.Code, itemRec.Body.String())
	}

	// Read the menu back through GET and assert the admin-authored markup is
	// preserved byte-identically (tier C), not reduced.
	menu := getMenu(t, srv, menuID)
	if menu.Name != markupName {
		t.Fatalf("menu name = %q, want byte-identical %q (tier C preserves markup for an administrator)", menu.Name, markupName)
	}
	if len(menu.Items) != 1 || menu.Items[0].Label != markupLabel {
		t.Fatalf("item label round-trip = %+v, want label %q preserved", menu.Items, markupLabel)
	}
}

// TestAdminMenuWriteRejectsJavascriptCustomURL asserts the role-independent
// safety guarantee: a custom item URL of javascript:alert(1) (and data:...) is
// rejected 400 with no persistence, regardless of writer tier. The admin tier
// preserves markup for titles, but the custom-URL scheme allow-list is NOT a
// markup path — it rejects dangerous schemes for every writer.
func TestAdminMenuWriteRejectsJavascriptCustomURL(t *testing.T) {
	for _, badURL := range []string{"javascript:alert(1)", "data:text/html,<script>x</script>"} {
		t.Run(badURL, func(t *testing.T) {
			srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
			createRec := navWriteRequest(t, srv, http.MethodPost, "/menus", `{"name":"Nav"}`, true)
			if createRec.Code != http.StatusCreated {
				t.Fatalf("create menu: status = %d, want 201 (body=%s)", createRec.Code, createRec.Body.String())
			}
			menuID := decodeMenuID(t, createRec)

			rec := navWriteRequest(t, srv, http.MethodPost, pathMenuItems(menuID),
				`{"label":"Evil","url":`+jsonString(badURL)+`,"type":"custom"}`, true)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("custom url %q: status = %d, want 400 (body=%s)", badURL, rec.Code, rec.Body.String())
			}

			// No item persisted: the menu still has zero items.
			menu := getMenu(t, srv, menuID)
			if len(menu.Items) != 0 {
				t.Fatalf("rejected custom URL persisted an item: %+v", menu.Items)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Handler mapping (Req 1.1-1.4, 2.4): bad id -> 400; unknown id/itemId -> 404;
// create -> 201 + navMenuDTO; update -> 200 + navMenuDTO; delete -> 204 no body.
// ---------------------------------------------------------------------------

func TestAdminMenuWriteNonNumericIDsReturn400(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"update_menu_bad_id", http.MethodPut, "/menus/abc", `{"name":"x"}`},
		{"delete_menu_bad_id", http.MethodDelete, "/menus/abc", ""},
		{"create_item_bad_menu_id", http.MethodPost, "/menus/abc/items", `{"label":"x","type":"custom","url":"/x"}`},
		{"update_item_bad_menu_id", http.MethodPut, "/menus/abc/items/1", `{"label":"x"}`},
		{"update_item_bad_item_id", http.MethodPut, "/menus/30/items/xyz", `{"label":"x"}`},
		{"delete_item_bad_item_id", http.MethodDelete, "/menus/30/items/xyz", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
			rec := navWriteRequest(t, srv, tc.method, tc.path, tc.body, true)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s %s: status = %d, want 400 (body=%s)", tc.method, tc.path, rec.Code, rec.Body.String())
			}
			assertJSONError(t, rec, "bad_request")
		})
	}
}

func TestAdminMenuWriteUnknownIDsReturn404(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"update_unknown_menu", http.MethodPut, "/menus/999999", `{"name":"x"}`},
		{"delete_unknown_menu", http.MethodDelete, "/menus/999999", ""},
		{"create_item_unknown_menu", http.MethodPost, "/menus/999999/items", `{"label":"x","type":"custom","url":"/x"}`},
		{"update_unknown_item", http.MethodPut, "/menus/30/items/999999", `{"label":"x"}`},
		{"delete_unknown_item", http.MethodDelete, "/menus/30/items/999999", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
			rec := navWriteRequest(t, srv, tc.method, tc.path, tc.body, true)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s: status = %d, want 404 (body=%s)", tc.method, tc.path, rec.Code, rec.Body.String())
			}
			assertJSONError(t, rec, "not_found")
		})
	}
}

func TestAdminMenuCreateReturns201WithNavMenuDTO(t *testing.T) {
	srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
	rec := navWriteRequest(t, srv, http.MethodPost, "/menus", `{"name":"Footer","slug":"footer"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp navMenuResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not a navMenuDTO: %v (%s)", err, rec.Body.String())
	}
	if resp.ID == 0 || resp.Name != "Footer" || resp.Slug != "footer" {
		t.Fatalf("create DTO = %+v, want id>0 name=Footer slug=footer", resp)
	}
	if resp.Items == nil {
		t.Fatalf("navMenuDTO.items must be a non-nil (possibly empty) array, got nil: %s", rec.Body.String())
	}
}

func TestAdminMenuUpdateReturns200WithNavMenuDTO(t *testing.T) {
	srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
	rec := navWriteRequest(t, srv, http.MethodPut, "/menus/30", `{"name":"Primary Renamed"}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp navMenuResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not a navMenuDTO: %v (%s)", err, rec.Body.String())
	}
	if resp.ID != 30 || resp.Name != "Primary Renamed" {
		t.Fatalf("update DTO = %+v, want id=30 name=\"Primary Renamed\"", resp)
	}
}

func TestAdminMenuDeleteReturns204NoBody(t *testing.T) {
	srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
	rec := navWriteRequest(t, srv, http.MethodDelete, "/menus/30", "", true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("204 response must have no body, got %q", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// CSRF enforcement (Req 8.2): a write with a missing/invalid CSRF token is
// rejected by csrfJSONMiddleware (403 JSON envelope) before the handler runs,
// mirroring TestAdminTermsWriteRoutesRequireCSRF.
// ---------------------------------------------------------------------------

func TestAdminMenuWriteRoutesRequireCSRF(t *testing.T) {
	for _, rt := range navWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
			rec := navWriteRequest(t, srv, rt.method, rt.path, rt.body, false) // no CSRF token
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s without CSRF: status = %d, want 403 (body=%s)",
					rt.method, rt.path, rec.Code, rec.Body.String())
			}
			var payload map[string]map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
			}
			if payload["error"]["code"] == "" {
				t.Fatalf("missing error.code in envelope: %s", rec.Body.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Request DTO field-name contract (Req 2.1, 5.1-5.3): the create-item body
// uses the exact json tags the design pins (label/url/type/object/objectId/
// parentId/order), and the persisted item reads back with those fields.
// ---------------------------------------------------------------------------

func TestAdminMenuItemCreateDecodesPinnedJSONFieldNames(t *testing.T) {
	srv := newNavMenuWriteServer(t, auth.RoleAdministrator)
	body := `{"label":"Blog","url":"/blog","type":"custom","object":"custom","objectId":0,"parentId":0,"order":5}`
	rec := navWriteRequest(t, srv, http.MethodPost, "/menus/30/items", body, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp navMenuResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not a navMenuDTO: %v (%s)", err, rec.Body.String())
	}
	var found *navMenuItemResponse
	for i := range resp.Items {
		if resp.Items[i].Label == "Blog" {
			found = &resp.Items[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("created item not in menu DTO: %+v", resp.Items)
	}
	if found.URL != "/blog" || found.Type != "custom" || found.Order != 5 {
		t.Fatalf("decoded item = %+v, want url=/blog type=custom order=5", *found)
	}
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func pathMenuItems(menuID int64) string {
	return "/menus/" + itoa(menuID) + "/items"
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func decodeMenuID(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	var resp navMenuResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode menu id: %v (%s)", err, rec.Body.String())
	}
	if resp.ID == 0 {
		t.Fatalf("decoded menu id is 0: %s", rec.Body.String())
	}
	return resp.ID
}

func getMenu(t *testing.T, srv *Server, id int64) domain.NavMenu {
	t.Helper()
	menu, err := srv.menus.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("menus.Get(%d): %v", id, err)
	}
	return menu
}
