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
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// termReadWriter combines domain.TermWriter and domain.TermReader into the
// single value content.NewTermWriteService requires. storage.Set exposes
// them as separate interface-typed fields (both backed by the same
// concrete wprepo.TermRepo), so this test-local combiner wires them
// together the same way cmd/grimoire/main.go's unexported termReadWriter
// does for the real server.
type termReadWriter struct {
	domain.TermWriter
	domain.TermReader
}

// newRESTTermsWriteRouter builds the same wp-json router as
// newAppPasswordWriteRESTRouter, additionally wiring a real
// content.TermWriteService into WithAdminWrites so Phase 4's REST
// categories/tags routes (Req 6) are live rather than 501 stubs. It seeds
// the fixture "admin" user (ID 1) with real capabilities (including
// manage_categories) the same way newAppPasswordWriteRESTRouter does for
// posts, and returns the seeded Repositories plus the ApplicationPasswords
// manager so a test can mint an authenticated principal or a
// zero-capability one (via noCapAppPassword) for the 403 cases.
func newRESTTermsWriteRouter(t *testing.T) (http.Handler, *storage.Repositories, *auth.ApplicationPasswords) {
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

	caps, err := auth.SerializeCapabilities(auth.RoleAdministrator)
	if err != nil {
		t.Fatalf("SerializeCapabilities: %v", err)
	}
	if err := repos.UserMeta.Set(ctx, 1, cfg.TablePrefix+"capabilities", caps); err != nil {
		t.Fatalf("seed admin capabilities: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, cfg.TablePrefix)
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	ap := &auth.ApplicationPasswords{Users: repos.Users, Meta: repos.UserMeta, Prefix: cfg.TablePrefix}
	termWrite := content.NewTermWriteService(termReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader})
	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(&fakeSessions{}, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(nil, termWrite, nil, nil).
		WithContentFeatures(comments, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(ap, false, "")
	return srv.Routes(), repos, ap
}

// --- 4.1: GET /categories, /tags (collection + single-item) ---

func TestRESTCategoriesCollection(t *testing.T) {
	h, _, _ := newRESTTermsWriteRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/categories", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// SeedFixtures seeds 3 category terms (news, Zeta, Alpha).
	if len(got) != 3 {
		t.Fatalf("len(categories) = %d, want 3: %+v", len(got), got)
	}
	for _, term := range got {
		for _, field := range []string{"id", "count", "name", "slug", "taxonomy", "link", "parent"} {
			if _, ok := term[field]; !ok {
				t.Fatalf("category missing field %q: %+v", field, term)
			}
		}
		if term["taxonomy"] != "category" {
			t.Fatalf("taxonomy = %v, want category", term["taxonomy"])
		}
		if term["parent"].(float64) != 0 {
			t.Fatalf("parent = %v, want placeholder 0", term["parent"])
		}
	}
}

func TestRESTCategoriesCollectionNoAuthRequired(t *testing.T) {
	h, _, _ := newRESTTermsWriteRouter(t)
	// No Authorization header at all: GET routes require no authentication
	// (design.md's REST-terms section).
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/tags", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRESTTagsCollection(t *testing.T) {
	h, _, _ := newRESTTermsWriteRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/tags", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// SeedFixtures seeds 1 post_tag term (Golang).
	if len(got) != 1 {
		t.Fatalf("len(tags) = %d, want 1: %+v", len(got), got)
	}
	if got[0]["taxonomy"] != "post_tag" {
		t.Fatalf("taxonomy = %v, want post_tag", got[0]["taxonomy"])
	}
}

func TestRESTCategorySingleFound(t *testing.T) {
	h, _, _ := newRESTTermsWriteRouter(t)
	// term_id 10 ("news") is seeded as a category by SeedFixtures.
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/categories/10", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["slug"] != "news" {
		t.Fatalf("slug = %v, want news", got["slug"])
	}
}

func TestRESTCategorySingleNotFound(t *testing.T) {
	h, _, _ := newRESTTermsWriteRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/categories/999999", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Req 6.6: a category ID requested via /tags/{id} (taxonomy/type mismatch)
// must 404, not 200 with the wrong taxonomy's term.
func TestRESTTermSingleTaxonomyMismatch(t *testing.T) {
	h, _, _ := newRESTTermsWriteRouter(t)
	// term_id 10 is a category, not a post_tag.
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/tags/10", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// --- 4.2: POST /categories, /tags (create) ---

func TestRESTCategoryCreate(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	body := `{"name":"Sports","slug":"sports"}`
	rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/categories", secret, body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["name"] != "Sports" || got["slug"] != "sports" || got["taxonomy"] != "category" {
		t.Fatalf("unexpected created category: %+v", got)
	}
	if got["id"] == nil {
		t.Fatalf("expected id in response, got %+v", got)
	}
}

func TestRESTTagCreate(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	body := `{"name":"Rust"}`
	rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/tags", secret, body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Slug omitted: it must be auto-derived from name (Req 6.2).
	if got["slug"] != "rust" {
		t.Fatalf("slug = %v, want auto-derived rust", got["slug"])
	}
}

// Req 6.5: insufficient capability is 403, never 404 -- terms have no
// existence to leak.
func TestRESTCategoryCreateForbidden(t *testing.T) {
	h, repos, ap := newRESTTermsWriteRouter(t)
	secret := noCapAppPassword(t, repos, ap)
	body := `{"name":"Forbidden","slug":"forbidden"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/categories", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("nocap", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// --- 4.3: PUT/PATCH .../{id}, DELETE .../{id} ---

func TestRESTCategoryUpdate(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	body := `{"name":"News Updated","slug":"news-updated"}`
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/10", secret, body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["name"] != "News Updated" || got["slug"] != "news-updated" {
		t.Fatalf("unexpected updated category: %+v", got)
	}
}

func TestRESTCategoryPatchNameOnly(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	// PATCH with only "name": the existing slug must be preserved (Req
	// 6.3's "name and/or slug" partial-update semantics).
	body := `{"name":"News Renamed"}`
	rec := doRESTWrite(t, h, http.MethodPatch, "/wp-json/wp/v2/categories/10", secret, body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["name"] != "News Renamed" || got["slug"] != "news" {
		t.Fatalf("unexpected patched category: %+v", got)
	}
}

func TestRESTCategoryUpdateNotFound(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	body := `{"name":"Nope","slug":"nope"}`
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/999999", secret, body, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRESTCategoryUpdateTaxonomyMismatch(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	// term_id 10 is a category, not a post_tag.
	body := `{"name":"Nope","slug":"nope"}`
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/tags/10", secret, body, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRESTCategoryUpdateForbidden(t *testing.T) {
	h, repos, ap := newRESTTermsWriteRouter(t)
	secret := noCapAppPassword(t, repos, ap)
	body := `{"name":"Nope","slug":"nope"}`
	req := httptest.NewRequest(http.MethodPut, "/wp-json/wp/v2/categories/10", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("nocap", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRESTCategoryDelete(t *testing.T) {
	h, repos, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	// term_id 10 ("news") is related to hello-3 and hello-2 (per
	// SeedFixtures' doc comment) -- deleting it must detach those posts,
	// not delete them (Req 6.7).
	rec := doRESTWrite(t, h, http.MethodDelete, "/wp-json/wp/v2/categories/10", secret, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	ctx := context.Background()
	if _, err := repos.TermReader.TermsByIDs(ctx, []int64{10}); err != nil {
		t.Fatalf("TermsByIDs after delete: %v", err)
	}
	remaining, err := repos.TermReader.TermsByIDs(ctx, []int64{10})
	if err != nil {
		t.Fatalf("TermsByIDs: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("term 10 still present after delete: %+v", remaining)
	}
	// The posts it was related to must still exist.
	if _, err := repos.PostWriter.ByID(ctx, 2); err != nil {
		t.Fatalf("post 2 should still exist after term delete: %v", err)
	}
	if _, err := repos.PostWriter.ByID(ctx, 3); err != nil {
		t.Fatalf("post 3 should still exist after term delete: %v", err)
	}
}

func TestRESTCategoryDeleteNotFound(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	rec := doRESTWrite(t, h, http.MethodDelete, "/wp-json/wp/v2/categories/999999", secret, "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRESTCategoryDeleteForbidden(t *testing.T) {
	h, repos, ap := newRESTTermsWriteRouter(t)
	secret := noCapAppPassword(t, repos, ap)
	req := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/categories/10", nil)
	req.SetBasicAuth("nocap", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// --- 4.4: regression -- still-501 write verbs unaffected ---

func TestRESTOtherWriteVerbsStill501(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/wp-json/wp/v2/media"},
		{http.MethodPut, "/wp-json/wp/v2/media/1"},
		{http.MethodPatch, "/wp-json/wp/v2/media/1"},
		{http.MethodDelete, "/wp-json/wp/v2/media/1"},
		{http.MethodPost, "/wp-json/wp/v2/users"},
		{http.MethodPut, "/wp-json/wp/v2/users/1"},
		{http.MethodPatch, "/wp-json/wp/v2/users/1"},
		{http.MethodDelete, "/wp-json/wp/v2/users/1"},
	}
	for _, c := range cases {
		rec := doRESTWrite(t, h, c.method, c.path, secret, "{}", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s: status = %d, want 501, body = %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}
