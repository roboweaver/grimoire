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
	"github.com/roboweaver/grimoire/pkg/extensions"
)

// newAppPasswordRESTRouter builds the same wp-json router as newRESTRouter,
// additionally wiring Application Password auth (WithApplicationPasswords)
// so Phase 6 tests can exercise Basic-auth resolution, the TLS/loopback
// gate, and the CSRF-branching POST /comments handler. It returns the
// router, the seeded Repositories (so a test can mint an Application
// Password or inspect a persisted comment directly), and the
// ApplicationPasswords manager itself.
func newAppPasswordRESTRouter(t *testing.T, fake *fakeSessions, requireTLS bool, trustedProxyHeader string) (http.Handler, *storage.Repositories, *auth.ApplicationPasswords) {
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
	// A published post with comments closed, for the "comment POST to a
	// closed post" case (the default fixtures have no published+closed
	// post: only drafts/attachments are closed).
	if _, err := repos.DB().ExecContext(ctx,
		`INSERT INTO `+cfg.TablePrefix+`posts (ID, post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_type, comment_status, post_parent, post_mime_type, menu_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		50, 1, "2024-01-09 00:00:00", "<p>closed</p>", "Closed Comments", "excerpt", "publish", "closed-comments", "post", "closed", 0, "", 0,
	); err != nil {
		t.Fatalf("insert closed-comments post: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, "wp_")
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	ap := &auth.ApplicationPasswords{Users: repos.Users, Meta: repos.UserMeta, Prefix: "wp_"}
	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithContentFeatures(comments, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(ap, requireTLS, trustedProxyHeader)
	return srv.Routes(), repos, ap
}

// mintAppPassword creates a real Application Password for the seeded
// "admin" user (ID 1) and returns its plaintext secret, grouped into 4-char
// chunks the way WordPress conventionally displays (and real clients often
// paste) it, to also exercise the middleware's own space-stripping.
func mintAppPassword(t *testing.T, ctx context.Context, ap *auth.ApplicationPasswords) string {
	t.Helper()
	_, secret, err := ap.Create(ctx, 1, "test app")
	if err != nil {
		t.Fatalf("ApplicationPasswords.Create: %v", err)
	}
	var b strings.Builder
	for i, r := range secret {
		if i != 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestApplicationPasswordAuthValidBasicSkipsCSRF(t *testing.T) {
	h, repos, ap := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	secret := mintAppPassword(t, context.Background(), ap)

	body := `{"post":1,"author_name":"App Client","author_email":"app@example.com","content":"Hi via app password"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	req.SetBasicAuth("admin", secret)
	req.RemoteAddr = "203.0.113.9:4242"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// AuthorIP was populated from the request (Req 7.3), confirming the
	// real, unmodified CommentService.Create path was used.
	items, err := repos.Comments.List(context.Background(), domain.CommentFilter{PostID: 1})
	if err != nil {
		t.Fatalf("List comments: %v", err)
	}
	found := false
	for _, c := range items {
		if c.AuthorEmail == "app@example.com" {
			found = true
			if c.AuthorIP != "203.0.113.9" {
				t.Errorf("AuthorIP = %q, want 203.0.113.9", c.AuthorIP)
			}
		}
	}
	if !found {
		t.Fatalf("created comment not found via repos.Comments.List")
	}
}

func TestApplicationPasswordAuthInvalidBasicRejectsEveryEndpointEvenWithValidSession(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin", Caps: map[string]bool{"moderate_comments": true}},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, _ := newAppPasswordRESTRouter(t, fake, false, "")

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"posts collection", http.MethodGet, "/wp-json/wp/v2/posts"},
		{"comments collection", http.MethodGet, "/wp-json/wp/v2/comments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.SetBasicAuth("admin", "totally-wrong-secret")
			req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_invalid_credentials" {
				t.Errorf("error code = %q, want rest_invalid_credentials", code)
			}
		})
	}
}

func TestApplicationPasswordAuthUnknownLoginAlso401(t *testing.T) {
	h, _, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts", nil)
	req.SetBasicAuth("no-such-user", "whatever")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_invalid_credentials" {
		t.Errorf("error code = %q, want rest_invalid_credentials", code)
	}
}

func TestApplicationPasswordAuthAbsentHeaderFallsThroughToSession(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, _ := newAppPasswordRESTRouter(t, fake, false, "")
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestApplicationPasswordAuthRequiresTLSOrLoopbackBeforeVerification(t *testing.T) {
	h, _, ap := newAppPasswordRESTRouter(t, &fakeSessions{}, true, "")
	secret := mintAppPassword(t, context.Background(), ap)

	// Non-TLS, non-loopback (httptest.NewRequest defaults Host to
	// "example.com"): rejected before verification is even attempted, even
	// with valid credentials (Req 8.9).
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts", nil)
	req.SetBasicAuth("admin", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-TLS non-loopback status = %d, want 401", rec.Code)
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_invalid_credentials" {
		t.Errorf("error code = %q, want rest_invalid_credentials", code)
	}

	// Loopback host: the same valid credentials succeed even without TLS
	// (Req 8.9's loopback exception).
	req2 := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts", nil)
	req2.Host = "localhost"
	req2.SetBasicAuth("admin", secret)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("loopback status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestRESTCommentCreateSessionAuthRequiresCSRF(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "correct-token"},
	}
	h, _, _ := newAppPasswordRESTRouter(t, fake, false, "")

	body := `{"post":1,"author_name":"Sess","author_email":"sess@example.com","content":"Hi"}`

	// Missing X-CSRF-Token -> 403 rest_forbidden.
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_forbidden" {
		t.Errorf("error code = %q, want rest_forbidden", code)
	}

	// Mismatched X-CSRF-Token -> 403 rest_forbidden.
	req2 := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	req2.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req2.Header.Set("X-CSRF-Token", "wrong-token")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("mismatched CSRF status = %d, want 403; body=%s", rec2.Code, rec2.Body.String())
	}

	// Correct X-CSRF-Token -> 201.
	req3 := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	req3.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req3.Header.Set("X-CSRF-Token", "correct-token")
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusCreated {
		t.Fatalf("valid CSRF status = %d, want 201; body=%s", rec3.Code, rec3.Body.String())
	}
}

func TestRESTCommentCreateAnonymousAcceptedWithAuthorIP(t *testing.T) {
	h, repos, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	body := `{"post":1,"author_name":"Anon","author_email":"anon@example.com","content":"Hello anon"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	req.RemoteAddr = "198.51.100.7:9999"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["status"] != "hold" {
		t.Errorf("status = %v, want hold (default moderation)", out["status"])
	}

	items, err := repos.Comments.List(context.Background(), domain.CommentFilter{PostID: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, c := range items {
		if c.AuthorEmail == "anon@example.com" {
			found = true
			if c.AuthorIP != "198.51.100.7" {
				t.Errorf("AuthorIP = %q, want 198.51.100.7", c.AuthorIP)
			}
		}
	}
	if !found {
		t.Fatalf("anonymous comment not persisted")
	}
}

func TestRESTCommentCreateClosedPost403(t *testing.T) {
	h, _, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	body := `{"post":50,"author_name":"A","author_email":"a@example.com","content":"Hi"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRESTCommentCreateMissingPost404(t *testing.T) {
	h, _, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	body := `{"post":9999,"author_name":"A","author_email":"a@example.com","content":"Hi"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRESTCommentCreateMissingFields400(t *testing.T) {
	h, _, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(`{"post":1}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRESTCommentCreateFiresCommentSubmittedExactlyOnce(t *testing.T) {
	h, _, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")

	var fired int
	extensions.RegisterAction("comment.submitted", func(context.Context, any) {
		fired++
	})

	body := `{"post":1,"author_name":"Hook","author_email":"hook@example.com","content":"Hi"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fired != 1 {
		t.Errorf("comment.submitted fired %d times, want 1", fired)
	}
}

func TestRESTCommentsCollectionApprovedOnlyForAnonymous(t *testing.T) {
	h, repos, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	ctx := context.Background()
	if _, err := repos.CommentWriter.Create(ctx, domain.Comment{PostID: 1, Author: "Held", AuthorEmail: "h@example.com", Content: "held", Status: "0"}); err != nil {
		t.Fatalf("seed held comment: %v", err)
	}
	if _, err := repos.CommentWriter.Create(ctx, domain.Comment{PostID: 1, Author: "OK", AuthorEmail: "ok@example.com", Content: "approved", Status: "1"}); err != nil {
		t.Fatalf("seed approved comment: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/comments?post=1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range out {
		if c["status"] != "approved" {
			t.Errorf("anonymous collection returned non-approved comment: %v", c)
		}
	}
	if len(out) == 0 {
		t.Fatalf("expected at least the approved seed comment")
	}
}

func TestRESTCommentsCollectionStatusParamRequiresModerateCapability(t *testing.T) {
	ctx := context.Background()
	fakeMod := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin", Caps: map[string]bool{"moderate_comments": true}},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, repos, _ := newAppPasswordRESTRouter(t, fakeMod, false, "")
	if _, err := repos.CommentWriter.Create(ctx, domain.Comment{PostID: 1, Author: "Held", AuthorEmail: "h2@example.com", Content: "held2", Status: "0"}); err != nil {
		t.Fatalf("seed held comment: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/comments?post=1&status=hold", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("moderator-requested status=hold returned nothing")
	}
	for _, c := range out {
		if c["status"] != "hold" {
			t.Errorf("status=hold returned non-hold comment: %v", c)
		}
	}
}

func TestRESTCommentsCollectionStatusParamIgnoredForNonModerator(t *testing.T) {
	ctx := context.Background()
	h, repos, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	if _, err := repos.CommentWriter.Create(ctx, domain.Comment{PostID: 1, Author: "Held", AuthorEmail: "h4@example.com", Content: "held4", Status: "0"}); err != nil {
		t.Fatalf("seed held comment: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/comments?post=1&status=hold", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range out {
		if c["status"] != "approved" {
			t.Errorf("non-moderator status=hold leaked a non-approved comment: %v", c)
		}
	}
}

func TestRESTCommentSingleNotFoundForHeldCommentAsAnonymous(t *testing.T) {
	ctx := context.Background()
	h, repos, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	id, err := repos.CommentWriter.Create(ctx, domain.Comment{PostID: 1, Author: "Held", AuthorEmail: "h3@example.com", Content: "held3", Status: "0"})
	if err != nil {
		t.Fatalf("seed held comment: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/comments/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRESTCommentSingleVisibleToModerator(t *testing.T) {
	ctx := context.Background()
	fakeMod := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin", Caps: map[string]bool{"moderate_comments": true}},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, repos, _ := newAppPasswordRESTRouter(t, fakeMod, false, "")
	id, err := repos.CommentWriter.Create(ctx, domain.Comment{PostID: 1, Author: "Held", AuthorEmail: "h5@example.com", Content: "held5", Status: "0"})
	if err != nil {
		t.Fatalf("seed held comment: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/comments/"+strconv.FormatInt(id, 10), nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRESTCommentWriteMethods501(t *testing.T) {
	h, _, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(m, "/wp-json/wp/v2/comments/1", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s /comments/1 status = %d, want 501", m, rec.Code)
		}
	}
}
