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
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// Task 7.3 — REST post-write transport (handleRESTPostCreate /
// handleRESTPostUpdate) content-safety proof (Req 4.2, 4.4, 9.5). Same shape as
// the admin-API end-to-end tests in adminapi_posts_test.go, but through the
// wp-json POST/PUT /posts transports: the request goes through the real router,
// the live content.PostWriteService and the shared sanitize.Policy, lands in the
// SQLite store, and the assertion reads the raw STORED bytes back via
// repos.PostWriter.ByID — "what reached the writer".
//
// The proof is end-to-end on purpose: rest_posts.go:207's write path calls
// s.postWrite.Create/Update, so a transport that wrote directly to storage,
// bypassing PostWriteService, could NOT pass. The harness wires the REAL
// *content.PostWriteService as the Server's postWrite dependency (via
// WithAdminWrites, which both transports share — see adminapi_posts.go and
// rest_posts.go both reading s.postWrite). PostWriteService defaults its policy
// to sanitize.New() (NewPostWriteService), so these assert the behavior whether
// or not task 8.2 injects an explicit WithContentPolicy.
//
// The actor is an author (RoleAuthor, built through the real auth tables): user
// ID 1 (matching the seeded posts' author) so the ownership capability checks
// pass, and LACKING unfiltered_html so it is a tier-B writer for post
// content/excerpt and gets the plain-text title path. An editor/administrator
// would be tier C and would strip nothing, so the author is the role that makes
// the stripping observable (Req 9.5).
//
// Written RED first (task 7.3): the package does not compile until tasks 7.5 /
// 8.2 land, and the empty-title->400 mapping is part of that wiring.

// newRESTPostWriteRouter builds the wp-json router with the REAL
// PostWriteService wired as postWrite (via WithAdminWrites) and the REST post
// surface wired via WithREST, returning the router and the seeded Repositories
// so a test can read a persisted post directly. It mirrors
// newAppPasswordRESTRouter but exercises the post-write path rather than the
// comment-write path, and authenticates via the session fake rather than an
// Application Password (so the actor's tier is controllable per test).
func newRESTPostWriteRouter(t *testing.T, fake *fakeSessions) (http.Handler, *storage.Repositories) {
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
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, "wp_")
	// The REAL post-write service, over the live SQLite PostWriter. Its policy
	// defaults to sanitize.New() (fail closed / never skip), so the write path
	// sanitizes exactly as production does.
	postWrite := content.NewPostWriteService(repos.PostWriter)
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(postWrite, nil, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	return srv.Routes(), repos
}

// authorFake builds a session fake authenticating as an author (tier B):
// RoleAuthor holds edit_posts / publish_posts / edit_published_posts but NOT
// unfiltered_html. UserID 1 matches the seeded posts' author so ownership
// checks pass. The REST write path enforces the M4 CSRF synchronizer token for
// session auth, so requests must carry the session cookie and the matching
// X-CSRF-Token "author-token".
func authorFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "author", []string{auth.RoleAuthor}),
		authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: "author-token"},
	}
}

func authorSessionPost(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "author-token")
	return req
}

// TestRESTPostCreateSanitizesContentExcerptAndTitleEndToEnd is the Req 9.5 proof
// for the REST create transport: an author (tier B) POSTs content and excerpt
// carrying a disallowed <script> and an allow-listed <em>, and a title carrying
// markup. The STORED record (read back from the writer) must have the <script>
// stripped and the <em> surviving in content/excerpt, and the title reduced to
// plain text.
func TestRESTPostCreateSanitizesContentExcerptAndTitleEndToEnd(t *testing.T) {
	h, repos := newRESTPostWriteRouter(t, authorFake())

	body := `{"title":"<em>Hello</em>","content":"<em>ok</em><script>alert(1)</script>","excerpt":"<em>ex</em><script>bad()</script>","status":"draft"}`
	req := authorSessionPost(http.MethodPost, "/wp-json/wp/v2/posts", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}

	stored := latestPost(t, repos, "Hello")
	if strings.Contains(stored.Content, "<script") || strings.Contains(stored.Content, "alert(1)") {
		t.Errorf("tier-B sanitize did not run before the writer; content = %q", stored.Content)
	}
	if !strings.Contains(stored.Content, "<em>ok</em>") {
		t.Errorf("allow-listed <em> did not survive tier B in content; content = %q", stored.Content)
	}
	if strings.Contains(stored.Excerpt, "<script") || strings.Contains(stored.Excerpt, "bad()") {
		t.Errorf("tier-B sanitize did not run on excerpt; excerpt = %q", stored.Excerpt)
	}
	if !strings.Contains(stored.Excerpt, "<em>ex</em>") {
		t.Errorf("allow-listed <em> did not survive tier B in excerpt; excerpt = %q", stored.Excerpt)
	}
	if stored.Title != "Hello" {
		t.Errorf("title not reduced to plain text; title = %q, want %q", stored.Title, "Hello")
	}
}

// TestRESTPostUpdateSanitizesContentExcerptAndTitleEndToEnd is the same proof
// for the REST update transport (PUT /posts/{id}). Post 1 is a seeded,
// author-1 published post; the author updates its content/excerpt/title, and
// the sanitized new values must land in the stored record. (Content/excerpt
// differ from the stored "<p>body</p>"/"excerpt", so the Finding-3
// unchanged-field skip does not apply — these are genuinely new caller values
// and are sanitized.)
func TestRESTPostUpdateSanitizesContentExcerptAndTitleEndToEnd(t *testing.T) {
	h, repos := newRESTPostWriteRouter(t, authorFake())

	body := `{"title":"<em>Edited</em>","content":"<em>fresh</em><script>alert(1)</script>","excerpt":"<em>sum</em><script>x()</script>"}`
	req := authorSessionPost(http.MethodPut, "/wp-json/wp/v2/posts/1", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	stored, err := repos.PostWriter.ByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("PostWriter.ByID(1): %v", err)
	}
	if strings.Contains(stored.Content, "<script") || strings.Contains(stored.Content, "alert(1)") {
		t.Errorf("tier-B sanitize did not run on update content; content = %q", stored.Content)
	}
	if !strings.Contains(stored.Content, "<em>fresh</em>") {
		t.Errorf("allow-listed <em> did not survive on update content; content = %q", stored.Content)
	}
	if strings.Contains(stored.Excerpt, "<script") || strings.Contains(stored.Excerpt, "x()") {
		t.Errorf("tier-B sanitize did not run on update excerpt; excerpt = %q", stored.Excerpt)
	}
	if stored.Title != "Edited" {
		t.Errorf("title not reduced to plain text on update; title = %q, want %q", stored.Title, "Edited")
	}
}

// TestRESTPostCreateEmptySanitizedTitleMapsErrTitleEmptyTo400 pins the
// ErrTitleEmpty -> existing-missing-title-400 contract for the REST create
// transport (Req 4.2/4.4). The raw title "<em></em>" is NON-empty, so it passes
// parseRESTPostWrite's own `p.Title == ""` check (which only fires for a
// literally empty title) and reaches PostWriteService.Create, where the
// plain-text title path reduces it to "" and the service returns
// content.ErrTitleEmpty. The handler must map that onto the SAME 400 the REST
// post path already returns for a missing title — status 400, error code
// "rest_invalid_param" (parseRESTPostWrite's own missing-title rejection) —
// rather than a 500 rest_create_failed. A non-draft status ("publish") is
// required so the empty-title rule applies at all (a draft title is optional).
func TestRESTPostCreateEmptySanitizedTitleMapsErrTitleEmptyTo400(t *testing.T) {
	h, repos := newRESTPostWriteRouter(t, authorFake())

	// publish needs a future date to pass the Req 5.2 schedule check and reach
	// the title-empty failure rather than being rejected for the date.
	body := `{"title":"<em></em>","content":"body","status":"publish","date":"2999-01-01T00:00:00"}`
	req := authorSessionPost(http.MethodPost, "/wp-json/wp/v2/posts", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (ErrTitleEmpty maps to the existing missing-title 400), body=%s", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_invalid_param" {
		t.Errorf("error code = %q, want the existing rest_invalid_param", code)
	}

	// A title that sanitizes to empty must not persist a post. The seed has
	// posts 1-5/201-304; a new publish post would be the next ID. Assert no
	// post carries the markup-only title in any form.
	all, err := repos.AdminPosts.ListForAdmin(context.Background(), domain.AdminPostFilter{Types: []string{"post"}, Limit: 100})
	if err != nil {
		t.Fatalf("ListForAdmin: %v", err)
	}
	for _, p := range all {
		if strings.Contains(p.Title, "<em>") || strings.Contains(p.Title, "</em>") {
			t.Errorf("a title that sanitizes to empty must not persist a post; found title = %q", p.Title)
		}
	}
}

// latestPost finds the most recently created post whose title matches want,
// reading STORED bytes straight from the writer port.
func latestPost(t *testing.T, repos *storage.Repositories, want string) domain.Post {
	t.Helper()
	posts, err := repos.AdminPosts.ListForAdmin(context.Background(), domain.AdminPostFilter{Types: []string{"post"}, Limit: 100})
	if err != nil {
		t.Fatalf("ListForAdmin: %v", err)
	}
	for _, p := range posts {
		if p.Title == want {
			stored, err := repos.PostWriter.ByID(context.Background(), p.ID)
			if err != nil {
				t.Fatalf("PostWriter.ByID(%d): %v", p.ID, err)
			}
			return stored
		}
	}
	t.Fatalf("no stored post with title %q found (write never reached the writer)", want)
	return domain.Post{}
}
