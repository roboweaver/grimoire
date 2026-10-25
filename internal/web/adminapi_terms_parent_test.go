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
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// --- Task 4.1 (M13): failing admin handler tests for the term `parent` field,
// the load-and-merge update relaxation, and the parent sentinel -> 400 mapping.
//
// These drive the admin JSON API (/admin/api/terms) over srv.Routes() with a
// real content.TermWriteService on SQLite (seeded by storagetest.SeedFixtures),
// mirroring the REST harness in rest_terms_test.go (newRESTTermsWriteRouter)
// but authenticating through an injectable *fakeSessions so the authorized
// (manage_categories) and unauthorized (no manage_categories) actors can share
// one builder. Auth matches adminAPIRouter's term-write group: a valid session
// cookie, the manage_categories capability, and the M4 X-CSRF-Token header.
//
// SeedFixtures seeds these category terms (all top-level, parent 0):
//   term_id 10 "news", 11 "zeta", 12 "alpha"
// and one post_tag term: term_id 13 "golang". The nav_menu term 30 is NOT a
// category. These ids are the fixture category ids the assertions below use,
// resolved the same way the existing REST admin term tests resolve them.
//
// Until task 4.2 wires the handler, these FAIL because: termWriteRequest has no
// Parent field (so `parent` is dropped on create/update), adminTermUpdate still
// rejects a body missing name/slug (so a parent-only update 400s on the
// Name/Slug-required rule rather than re-parenting), and the cycle/invalid
// parent sentinels are never produced nor mapped to 400.

const adminCSRFToken = "admin-csrf-token"

// manageCategoriesFake authenticates as an editor (RoleEditor holds
// manage_categories) carrying the shared CSRF token, so an admin term-write
// request that supplies the session cookie + X-CSRF-Token clears the capability
// gate and the M4 CSRF gate and reaches adminTermCreate/adminTermUpdate.
func manageCategoriesFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "editor", []string{auth.RoleEditor}),
		authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: adminCSRFToken},
	}
}

// noManageCategoriesFake authenticates as an author (RoleAuthor lacks
// manage_categories) carrying the same CSRF token, so the request clears CSRF
// and is rejected by the capability gate alone -> admin-JSON 403.
func noManageCategoriesFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(2, "author", []string{auth.RoleAuthor}),
		authSession:   domain.Session{ID: "s2", UserID: 2, CSRFToken: adminCSRFToken},
	}
}

// newAdminTermsWriteRouter builds the full server router (srv.Routes()) with a
// real content.TermWriteService wired into WithAdminWrites so the admin
// /admin/api/terms write routes are live, authenticated via the supplied
// *fakeSessions. It returns the handler and the seeded repositories so a test
// can read a term's parent back through repos.TermReader after a write.
func newAdminTermsWriteRouter(t *testing.T, fake *fakeSessions) (http.Handler, *storage.Repositories) {
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
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())
	// Same combined TermWriter+TermReader the REST harness and
	// cmd/grimoire/main.go wire into content.NewTermWriteService.
	termWrite := content.NewTermWriteService(termReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader})
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(nil, termWrite, nil, nil).
		WithContentFeatures(comments, nil, nil)
	return srv.Routes(), repos
}

// doAdminTermWrite issues an authenticated admin term write (session cookie +
// X-CSRF-Token) and returns the recorder. A session cookie value is required so
// SessionMiddleware calls fakeSessions.Authenticate (which returns the
// configured principal/session regardless of the token value).
func doAdminTermWrite(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRFToken)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "session-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// termParentByID reads a term's stored ParentID back through the live
// TermReader, the same port the admin/REST reads render.
func termParentByID(t *testing.T, repos *storage.Repositories, id int64) int64 {
	t.Helper()
	terms, err := repos.TermReader.TermsByIDs(context.Background(), []int64{id})
	if err != nil {
		t.Fatalf("TermsByIDs(%d): %v", id, err)
	}
	if len(terms) != 1 {
		t.Fatalf("TermsByIDs(%d) returned %d terms, want 1", id, len(terms))
	}
	return terms[0].ParentID
}

// termByID reads back a full term (name/slug/parent) for the preservation
// assertions on partial updates.
func termByID(t *testing.T, repos *storage.Repositories, id int64) domain.Term {
	t.Helper()
	terms, err := repos.TermReader.TermsByIDs(context.Background(), []int64{id})
	if err != nil {
		t.Fatalf("TermsByIDs(%d): %v", id, err)
	}
	if len(terms) != 1 {
		t.Fatalf("TermsByIDs(%d) returned %d terms, want 1", id, len(terms))
	}
	return terms[0]
}

func assertAdminJSONErrorCode(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response not a JSON error envelope: %v (%s)", err, rec.Body.String())
	}
	if payload.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q (body=%s)", payload.Error.Code, wantCode, rec.Body.String())
	}
}

// TestAdminTermCreateWithParent: POST /admin/api/terms with a parent nests the
// new category under the given category id, reflected on the next read
// (Req 1.2, 6.2).
func TestAdminTermCreateWithParent(t *testing.T) {
	h, repos := newAdminTermsWriteRouter(t, manageCategoriesFake())
	// parent 10 ("news") is a seeded top-level category.
	body := `{"name":"Child","slug":"child","taxonomy":"category","parent":10}`
	rec := doAdminTermWrite(t, h, http.MethodPost, "/admin/api/terms", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID == 0 {
		t.Fatalf("expected a created term id, got %s", rec.Body.String())
	}
	if got := termParentByID(t, repos, resp.ID); got != 10 {
		t.Fatalf("created term parent = %d, want 10", got)
	}
}

// TestAdminTermUpdateParentOnlyAllowed: a PUT body carrying ONLY parent (no
// name, no slug) now succeeds via load-and-merge -- the term is re-parented and
// its name/slug are preserved from the pre-read (Req 2.1, 2.2). Today this 400s
// on the unconditional Name/Slug-required rule.
func TestAdminTermUpdateParentOnlyAllowed(t *testing.T) {
	h, repos := newAdminTermsWriteRouter(t, manageCategoriesFake())
	before := termByID(t, repos, 12) // "alpha", top-level
	// Re-parent category 12 ("alpha") under category 10 ("news"), name/slug omitted.
	body := `{"parent":10}`
	rec := doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/12", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	after := termByID(t, repos, 12)
	if after.ParentID != 10 {
		t.Fatalf("parent after update = %d, want 10", after.ParentID)
	}
	if after.Name != before.Name || after.Slug != before.Slug {
		t.Fatalf("name/slug not preserved: got (%q,%q), want (%q,%q)", after.Name, after.Slug, before.Name, before.Slug)
	}
}

// TestAdminTermUpdateReparentAndPartial: re-parent, promote-to-top-level, and a
// name-only update that leaves parent unchanged (Req 2.1, 2.2, 2.3).
func TestAdminTermUpdateReparentAndPartial(t *testing.T) {
	h, repos := newAdminTermsWriteRouter(t, manageCategoriesFake())

	// Re-parent 12 ("alpha") under 10.
	rec := doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/12", `{"parent":10}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-parent: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := termParentByID(t, repos, 12); got != 10 {
		t.Fatalf("after re-parent, parent = %d, want 10", got)
	}

	// Re-parent 12 again under 11 ("zeta").
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/12", `{"parent":11}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-parent 2: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := termParentByID(t, repos, 12); got != 11 {
		t.Fatalf("after second re-parent, parent = %d, want 11", got)
	}

	// Promote 12 to top-level via explicit parent 0.
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/12", `{"parent":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := termParentByID(t, repos, 12); got != 0 {
		t.Fatalf("after promote, parent = %d, want 0", got)
	}

	// Re-parent under 10, then a name-only update (no parent key) must leave
	// parent unchanged (omitted-vs-explicit-zero).
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/12", `{"parent":10}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set parent before name-only: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/12", `{"name":"X"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("name-only update: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := termParentByID(t, repos, 12); got != 10 {
		t.Fatalf("name-only update changed parent to %d, want unchanged 10", got)
	}
}

// TestAdminTermUpdateCycleRejected: a self-parent and a parent->child cycle are
// each admin-JSON 400 (Req 3.3). parent->child: make 11 a child of 10, then try
// to re-parent 10 under 11.
func TestAdminTermUpdateCycleRejected(t *testing.T) {
	h, repos := newAdminTermsWriteRouter(t, manageCategoriesFake())

	// Self-parent: PUT /terms/10 {parent:10} -> 400.
	rec := doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/10", `{"parent":10}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-parent: status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "bad_request")
	if got := termParentByID(t, repos, 10); got != 0 {
		t.Fatalf("self-parent altered parent to %d, want unchanged 0", got)
	}

	// Build a chain: 11 under 10.
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/11", `{"parent":10}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed chain: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	// Now re-parenting 10 under its descendant 11 is a cycle -> 400.
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/10", `{"parent":11}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("descendant cycle: status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "bad_request")
	if got := termParentByID(t, repos, 10); got != 0 {
		t.Fatalf("descendant cycle altered 10's parent to %d, want unchanged 0", got)
	}
}

// TestAdminTermInvalidParentRejected: a nonexistent parent and a post_tag term
// with a non-zero parent are each admin-JSON 400 (Req 4.1, 4.3). Covers both
// create and update.
func TestAdminTermInvalidParentRejected(t *testing.T) {
	h, repos := newAdminTermsWriteRouter(t, manageCategoriesFake())

	// Nonexistent parent on create -> 400.
	rec := doAdminTermWrite(t, h, http.MethodPost, "/admin/api/terms",
		`{"name":"Orphan","slug":"orphan","taxonomy":"category","parent":999999}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create nonexistent parent: status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "bad_request")

	// Nonexistent parent on update -> 400, 12 unchanged.
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/12", `{"parent":999999}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("update nonexistent parent: status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "bad_request")
	if got := termParentByID(t, repos, 12); got != 0 {
		t.Fatalf("invalid update altered 12's parent to %d, want unchanged 0", got)
	}

	// post_tag term (13 "golang") with a non-zero parent on update -> 400.
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/13", `{"parent":10}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("post_tag non-zero parent: status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "bad_request")
	if got := termParentByID(t, repos, 13); got != 0 {
		t.Fatalf("post_tag parent altered to %d, want unchanged 0", got)
	}

	// post_tag with a non-zero parent on create -> 400.
	rec = doAdminTermWrite(t, h, http.MethodPost, "/admin/api/terms",
		`{"name":"Tagged","slug":"tagged","taxonomy":"post_tag","parent":10}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create post_tag with parent: status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "bad_request")
}

// TestAdminTermUpdateUnknownIDStill404: a PUT to an unknown term id with a
// parent is still an admin-JSON 404 (Req 2.5).
func TestAdminTermUpdateUnknownIDStill404(t *testing.T) {
	h, _ := newAdminTermsWriteRouter(t, manageCategoriesFake())
	rec := doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/999999", `{"parent":10}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "not_found")
}

// TestAdminTermParentGateRequiresManageCategories: a caller lacking
// manage_categories sending a parent-carrying create or update is admin-JSON
// 403, with no parent altered (Req 5.2).
func TestAdminTermParentGateRequiresManageCategories(t *testing.T) {
	h, repos := newAdminTermsWriteRouter(t, noManageCategoriesFake())

	rec := doAdminTermWrite(t, h, http.MethodPost, "/admin/api/terms",
		`{"name":"Child","slug":"child","taxonomy":"category","parent":10}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create: status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "forbidden")

	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/12", `{"parent":10}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("update: status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
	assertAdminJSONErrorCode(t, rec, "forbidden")
	if got := termParentByID(t, repos, 12); got != 0 {
		t.Fatalf("forbidden update altered 12's parent to %d, want unchanged 0", got)
	}
}
