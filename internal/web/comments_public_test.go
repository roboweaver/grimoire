package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

func newCommentServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	dsn := root + "/grimoire.db"
	uploads := root + "/uploads"
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
	eng, err := render.Load("../../themes", "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	menus := content.NewNavMenuService(repos.NavMenus, "default")
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: uploads, BaseURL: "/wp-content/uploads"})
	h := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithContentFeatures(comments, media, menus).Routes()
	return h, uploads
}

func TestPublicCommentSubmissionRejectsMissingDoubleSubmitCSRF(t *testing.T) {
	h, _ := newCommentServer(t)
	form := url.Values{"post_id": {"1"}, "author": {"A"}, "email": {"a@example.com"}, "content": {"Hello"}}
	req := httptest.NewRequest(http.MethodPost, "/comment", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestPublicCommentSubmissionAcceptsValidTokenAndEscapesPendingEcho(t *testing.T) {
	h, _ := newCommentServer(t)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/hello-1", nil))
	body := getRec.Body.String()
	idx := strings.Index(body, `name="comment_csrf_token" value="`)
	if idx < 0 {
		t.Fatalf("comment token field missing: %s", body)
	}
	token := body[idx+33:]
	token = token[:strings.Index(token, `"`)]
	var csrfCookie *http.Cookie
	for _, c := range getRec.Result().Cookies() {
		if c.Name == "grimoire_comment_csrf" {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("comment csrf cookie missing")
	}
	form := url.Values{"post_id": {"1"}, "author": {"A"}, "email": {"a@example.com"}, "content": {"<b>Hello</b>"}, "comment_csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/comment", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	follow := httptest.NewRecorder()
	h.ServeHTTP(follow, httptest.NewRequest(http.MethodGet, loc, nil))
	if !strings.Contains(follow.Body.String(), "&amp;lt;b&amp;gt;Hello&amp;lt;/b&amp;gt;") {
		t.Fatalf("pending echo not escaped: %s", follow.Body.String())
	}
}

// commentCSRFToken performs an anonymous GET on the single post page and
// returns a valid double-submit comment CSRF token plus its matching cookie,
// for use as a POST /comment prerequisite in tests below (Req 9 support).
func commentCSRFToken(t *testing.T, h http.Handler) (string, *http.Cookie) {
	t.Helper()
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/hello-1", nil))
	body := getRec.Body.String()
	idx := strings.Index(body, `name="comment_csrf_token" value="`)
	if idx < 0 {
		t.Fatalf("comment token field missing: %s", body)
	}
	token := body[idx+33:]
	token = token[:strings.Index(token, `"`)]
	var csrfCookie *http.Cookie
	for _, c := range getRec.Result().Cookies() {
		if c.Name == "grimoire_comment_csrf" {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("comment csrf cookie missing")
	}
	return token, csrfCookie
}

// TestPublicCommentSubmissionRequiredFieldsAndEmailValidation proves
// commentSubmit rejects a request that has a valid double-submit CSRF token
// but is otherwise missing author/email/content, or carries a malformed
// email, with 400 Bad Request rather than silently defaulting/creating a
// comment (Req 9).
func TestPublicCommentSubmissionRequiredFieldsAndEmailValidation(t *testing.T) {
	h, _ := newCommentServer(t)
	cases := []struct {
		name   string
		values url.Values
	}{
		{"missing author", url.Values{"post_id": {"1"}, "author": {""}, "email": {"a@example.com"}, "content": {"Hello"}}},
		{"missing email", url.Values{"post_id": {"1"}, "author": {"A"}, "email": {""}, "content": {"Hello"}}},
		{"missing content", url.Values{"post_id": {"1"}, "author": {"A"}, "email": {"a@example.com"}, "content": {""}}},
		{"whitespace-only content", url.Values{"post_id": {"1"}, "author": {"A"}, "email": {"a@example.com"}, "content": {"   "}}},
		{"invalid email", url.Values{"post_id": {"1"}, "author": {"A"}, "email": {"not-an-email"}, "content": {"Hello"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, cookie := commentCSRFToken(t, h)
			form := tc.values
			form.Set("comment_csrf_token", token)
			req := httptest.NewRequest(http.MethodPost, "/comment", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}
