package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// newAppPasswordWriteRESTRouter builds the same wp-json router as
// newAppPasswordRESTRouter, additionally wiring WithAdminWrites so Phase 3's
// REST create/update/delete handlers (Req 6) are live rather than 501
// stubs. It also grants the seeded "admin" user (ID 1) real capabilities:
// storagetest.SeedFixtures seeds that user with no {prefix}capabilities
// usermeta row at all (a role-less, zero-capability principal once
// authenticated), which is fine for the comment-POST tests that need no
// capability, but every post/page write requires edit_posts/publish_posts/
// delete_posts. This seeds an "administrator" capabilities row directly via
// UserMeta.Set (the lower-level equivalent of
// content.NewUserService.Bootstrap, chosen here to keep reusing the
// existing seeded user/fixtures rather than minting a second user).
func newAppPasswordWriteRESTRouter(t *testing.T) (http.Handler, *storage.Repositories, *auth.ApplicationPasswords) {
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
	postWrite := content.NewPostWriteService(repos.PostWriter)
	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(&fakeSessions{}, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(postWrite, nil, nil, nil).
		WithContentFeatures(comments, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(ap, false, "")
	return srv.Routes(), repos, ap
}

// noCapAppPassword mints an Application Password for a brand-new,
// zero-capability user (created directly against repos.Users, bypassing
// content.NewUserService.Bootstrap so no capabilities row is written at
// all), for the 403-forbidden test cases: the seeded "admin" user (ID 1)
// has real capabilities once newAppPasswordWriteRESTRouter has seeded them,
// so the forbidden-write tests need a distinct, deliberately powerless
// principal instead.
func noCapAppPassword(t *testing.T, repos *storage.Repositories, ap *auth.ApplicationPasswords) string {
	t.Helper()
	ctx := context.Background()
	id, err := repos.Users.Create(ctx, domain.User{
		Login: "nocap", Nicename: "nocap", DisplayName: "No Cap", Email: "nocap@example.test",
	})
	if err != nil {
		t.Fatalf("create no-cap user: %v", err)
	}
	_, secret, err := ap.Create(ctx, id, "test app (no caps)")
	if err != nil {
		t.Fatalf("ApplicationPasswords.Create: %v", err)
	}
	return secret
}

func doRESTWrite(t *testing.T, h http.Handler, method, path, secret, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", secret)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRESTPostCreate(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	body := `{"title":"REST Created","content":"<p>hi</p>","status":"draft"}`
	rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret, body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["id"] == nil {
		t.Fatalf("expected id in response, got %v", got)
	}
	title, _ := got["title"].(map[string]any)
	if title == nil || title["rendered"] != "REST Created" {
		t.Fatalf("expected title.rendered = REST Created, got %v", got["title"])
	}
}

func TestRESTPageCreate(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	body := `{"title":"REST Page","content":"<p>hi</p>","status":"draft"}`
	rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/pages", secret, body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRESTPostCreateForbidden(t *testing.T) {
	h, repos, ap := newAppPasswordWriteRESTRouter(t)
	secret := noCapAppPassword(t, repos, ap)

	body := `{"title":"Nope","status":"draft"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("nocap", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["code"] != "rest_cannot_create" {
		t.Fatalf("code = %v, want rest_cannot_create", got["code"])
	}
}

func TestRESTPostUpdate(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	createRec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret,
		`{"title":"Original","content":"<p>v1</p>","status":"draft"}`, nil)
	id := restJSONID(t, createRec)

	updateRec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/posts/"+strconv.FormatInt(id, 10), secret,
		`{"title":"Updated","content":"<p>v2</p>","status":"draft"}`, nil)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", updateRec.Code, updateRec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(updateRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	title, _ := got["title"].(map[string]any)
	if title == nil || title["rendered"] != "Updated" {
		t.Fatalf("expected title.rendered = Updated, got %v", got["title"])
	}
}

func TestRESTPostUpdateConflict(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	createRec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret,
		`{"title":"Original","status":"draft"}`, nil)
	id := restJSONID(t, createRec)

	stale := time.Now().Add(-1 * time.Hour).UTC().Format(http.TimeFormat)
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/posts/"+strconv.FormatInt(id, 10), secret,
		`{"title":"Conflicted","status":"draft"}`, map[string]string{"If-Unmodified-Since": stale})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["code"] != "rest_conflict" {
		t.Fatalf("code = %v, want rest_conflict", got["code"])
	}
}

func TestRESTPostUpdateNoConflictHeaderIsLastWriteWins(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	createRec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret,
		`{"title":"Original","status":"draft"}`, nil)
	id := restJSONID(t, createRec)

	// No If-Unmodified-Since header at all: last-write-wins, even though the
	// stored Modified timestamp has necessarily moved since the create
	// response above (Req 6.5).
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/posts/"+strconv.FormatInt(id, 10), secret,
		`{"title":"Overwritten","status":"draft"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRESTPostUpdateNotFound(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/posts/999999", secret,
		`{"title":"Nope","status":"draft"}`, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["code"] != "rest_post_invalid_id" {
		t.Fatalf("code = %v, want rest_post_invalid_id", got["code"])
	}
}

func TestRESTPostDelete(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	createRec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret,
		`{"title":"To Delete","status":"draft"}`, nil)
	id := restJSONID(t, createRec)

	rec := doRESTWrite(t, h, http.MethodDelete, "/wp-json/wp/v2/posts/"+strconv.FormatInt(id, 10), secret, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if deleted, _ := got["deleted"].(bool); !deleted {
		t.Fatalf("expected deleted = true, got %v", got["deleted"])
	}
	prev, _ := got["previous"].(map[string]any)
	if prev == nil {
		t.Fatalf("expected previous object, got %v", got["previous"])
	}
	prevID, _ := prev["id"].(float64)
	if int64(prevID) != id {
		t.Fatalf("previous.id = %v, want %d", prev["id"], id)
	}

	// A second GET now 404s: the post is really gone.
	getReq := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts/"+strconv.FormatInt(id, 10), nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
}

func TestRESTPageDelete(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	createRec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/pages", secret,
		`{"title":"To Delete","status":"draft"}`, nil)
	id := restJSONID(t, createRec)

	rec := doRESTWrite(t, h, http.MethodDelete, "/wp-json/wp/v2/pages/"+strconv.FormatInt(id, 10), secret, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// restJSONID extracts the "id" field from a create response recorded by
// doRESTWrite, failing the test if the response wasn't a 201 with a
// numeric id.
func restJSONID(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from create, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	id, _ := got["id"].(float64)
	if id == 0 {
		t.Fatalf("expected non-zero id, got %v", got["id"])
	}
	return int64(id)
}
