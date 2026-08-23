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
