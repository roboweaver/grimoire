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
	return newAppPasswordWriteRESTRouterWithSessions(t, &fakeSessions{})
}

// newAppPasswordWriteRESTRouterWithSessions is newAppPasswordWriteRESTRouter
// with an injectable session backend, so CSRF tests (finding #4 from the PR
// #16 review) can supply a *fakeSessions with an authenticated principal +
// CSRF token, mirroring TestRESTCommentCreateSessionAuthRequiresCSRF's
// pattern for the comment-write REST handlers.
func newAppPasswordWriteRESTRouterWithSessions(t *testing.T, fake *fakeSessions) (http.Handler, *storage.Repositories, *auth.ApplicationPasswords) {
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
	).WithAuth(fake, web.AuthConfig{}).
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

// TestRESTPostCreateDefaultsDateWhenOmitted guards finding #1 from the PR
// #16 review at the REST layer: a create body that omits date must not
// store an epoch post_date -- it should end up close to "now" (via
// PostWriteService.Create's defaulting), so a REST-created post sorts
// correctly alongside admin-API-created ones.
func TestRESTPostCreateDefaultsDateWhenOmitted(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	before := time.Now().Add(-time.Minute)
	body := `{"title":"No Date","status":"draft"}`
	rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret, body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	dateStr, _ := got["date"].(string)
	if dateStr == "" {
		t.Fatalf("expected non-empty date in response, got %v", got["date"])
	}
	d, err := time.Parse("2006-01-02T15:04:05", dateStr)
	if err != nil {
		// Fall back to RFC3339 in case the response serializes with a
		// timezone offset rather than the write layout.
		d, err = time.Parse(time.RFC3339, dateStr)
		if err != nil {
			t.Fatalf("parse date %q: %v", dateStr, err)
		}
	}
	if d.Before(before) {
		t.Fatalf("date = %v, want at/after %v (i.e. defaulted to now, not epoch)", d, before)
	}
}

// TestRESTPostCreateRejectsFutureStatusWithNoDate guards the flip side of
// finding #1 at the REST layer: a future-status create with no date must be
// rejected rather than silently stored with today's date.
func TestRESTPostCreateRejectsFutureStatusWithNoDate(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	body := `{"title":"Scheduled","status":"future"}`
	rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret, body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
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

// TestRESTPostUpdatePartial guards finding #2 from the PR #16 review: a
// sparse PATCH-style body containing only "title" must not wipe
// content/excerpt/slug/comment_status or silently demote a published post
// back to draft. Uses PATCH explicitly (PUT exercises the same
// parseRESTPostWrite code path; both are asserted since Req 6 lists PATCH
// alongside PUT).
func TestRESTPostUpdatePartial(t *testing.T) {
	h, _, ap := newAppPasswordWriteRESTRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	createRec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret,
		`{"title":"Original","content":"<p>full body</p>","excerpt":"orig excerpt",`+
			`"slug":"original-slug","status":"publish","comment_status":"open"}`, nil)
	id := restJSONID(t, createRec)

	rec := doRESTWrite(t, h, http.MethodPatch, "/wp-json/wp/v2/posts/"+strconv.FormatInt(id, 10), secret,
		`{"title":"New"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	title, _ := got["title"].(map[string]any)
	if title == nil || title["rendered"] != "New" {
		t.Fatalf("title.rendered = %v, want New", got["title"])
	}
	content, _ := got["content"].(map[string]any)
	if content == nil || content["rendered"] != "<p>full body</p>" {
		t.Fatalf("content.rendered = %v, want unchanged full body", got["content"])
	}
	excerpt, _ := got["excerpt"].(map[string]any)
	if excerpt == nil || excerpt["rendered"] != "orig excerpt" {
		t.Fatalf("excerpt.rendered = %v, want unchanged orig excerpt", got["excerpt"])
	}
	if got["slug"] != "original-slug" {
		t.Fatalf("slug = %v, want unchanged original-slug", got["slug"])
	}
	if got["status"] != "publish" {
		t.Fatalf("status = %v, want unchanged publish (not demoted to draft)", got["status"])
	}
	if got["comment_status"] != "open" {
		t.Fatalf("comment_status = %v, want unchanged open", got["comment_status"])
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

// TestRESTPostWriteSessionAuthRequiresCSRF guards finding #4 from the PR #16
// review: session-cookie-authenticated REST post/page write requests must
// go through the same CSRF guard the comment-write handlers already have
// (isAppPasswordAuth -> skip; session present -> requireSessionCSRFREST),
// not just application-password-authenticated ones. Mirrors
// TestRESTCommentCreateSessionAuthRequiresCSRF's pattern, extended to
// create/update/delete.
func TestRESTPostWriteSessionAuthRequiresCSRF(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin", Caps: auth.CapabilitiesForRoles(auth.RoleAdministrator)},
		authSession:   domain.Session{ID: "s1", CSRFToken: "correct-token"},
	}
	h, _, ap := newAppPasswordWriteRESTRouterWithSessions(t, fake)

	// Seed a post via app-password auth (which skips CSRF entirely) so the
	// update/delete cases below have something to target.
	secret := mintAppPassword(t, context.Background(), ap)
	createRec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret,
		`{"title":"Session CSRF Target","status":"draft"}`, nil)
	id := restJSONID(t, createRec)
	idPath := "/wp-json/wp/v2/posts/" + strconv.FormatInt(id, 10)

	missing := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"Sess","status":"draft"}`},
		{"update", http.MethodPut, idPath, `{"title":"Sess Updated","status":"draft"}`},
		{"delete", http.MethodDelete, idPath, ``},
	}
	for _, tc := range missing {
		t.Run(tc.name+"/missing token", func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s missing CSRF status = %d, want 403; body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_forbidden" {
				t.Errorf("%s error code = %q, want rest_forbidden", tc.name, code)
			}
		})
	}

	// With the correct token, update and delete against the still-live
	// seeded post succeed (delete last, since it consumes the post).
	t.Run("update/correct token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, idPath, strings.NewReader(`{"title":"Sess Updated","status":"draft"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		req.Header.Set("X-CSRF-Token", "correct-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("delete/correct token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, idPath, nil)
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		req.Header.Set("X-CSRF-Token", "correct-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
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
