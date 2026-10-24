package web_test

import (
	"context"
	"errors"
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

// Task 1.1 (M10.E) — REST post/page write capability matrix. A single
// table-driven test that pins the CURRENT authorization contract of the three
// wp-json post-write routes (POST /posts create, PUT /posts/{id} update, DELETE
// /posts/{id} delete) as enforced today by handleRESTPostCreate/Update/Delete
// (rest_posts.go) over the REAL content.PostWriteService and the real
// auth.CanCreatePost/CanEditPost/CanDeletePost capability path. This asserts an
// ALREADY-shipped contract (no new production code), so every cell is written
// to PASS against the live handler the moment it lands.
//
// Harness/helpers reused: newRESTPostWriteRouter(t, fake) (wires the REAL
// PostWriteService over the live SQLite PostWriter), decodeRESTErrCode, and
// repos.AdminPosts.ListForAdmin / repos.PostWriter.ByID for the no-state-change
// readback. Each principal is built through the real auth.NewPrincipal /
// auth.CapabilitiesForRoles path (via postMatrixFake) — never a hand-forged
// capability set.
//
// Auth mechanism: session fake + CSRF. Each non-anonymous cell carries the
// session cookie and the matching X-CSRF-Token "matrix-token"; the anonymous
// cell (fake == nil) sends neither (a bare request through an empty
// &fakeSessions{}), so no principal is resolved and the capability check sees
// the empty set.
//
// Seeded fixture pinned (storagetest.SeedFixtures / postArgs): post 1 is
// {id:1, author:1, type:"post", status:"publish", title:"Hello One",
// content:"<p>body</p>"}. So update/delete target /posts/1, "own" means the
// caller is UserID 1, and because the post is PUBLISHED the own-author cells
// additionally need edit_published_posts / delete_published_posts (author holds
// both). The matrix encodes:
//
//	CREATE (POST /posts):
//	  anonymous            draft   → 403 rest_cannot_create
//	  subscriber (uid 1)   draft   → 403 rest_cannot_create  (lacks edit_posts)
//	  contributor (uid 1)  draft   → 201
//	  author (uid 1)       draft   → 201
//	  editor (uid 1)       draft   → 201
//	  administrator (uid 1)draft   → 201
//	  contributor (uid 1)  publish → 403 rest_cannot_create  (lacks publish_posts)
//	  author (uid 1)       publish → 201
//	  editor (uid 1)       publish → 201
//	UPDATE (PUT /posts/1, stored author 1, published):
//	  anonymous                    → 403 rest_cannot_edit
//	  subscriber (uid 1, own)      → 403 rest_cannot_edit     (lacks edit_posts)
//	  author (uid 1, own)          → 200
//	  author (uid 7, other)        → 403 rest_cannot_edit     (lacks edit_others_posts)
//	  editor (uid 7, other)        → 200
//	  administrator (uid 7, other) → 200
//	DELETE (DELETE /posts/1): same ownership shape as update; forbidden reuses
//	  403 rest_cannot_edit (handler reuses the edit code, rest_posts.go).

// postMatrixFake wraps a matrix principal in the fakeSessions shape the REST
// post-write requests expect: a session carrying the shared "matrix-token" CSRF
// token so a request built with matrixPostReq (which sets X-CSRF-Token:
// matrix-token) clears the M4 synchronizer check and reaches the capability
// decision. The principal is resolved through the real auth.NewPrincipal /
// auth.CapabilitiesForRoles path for the given role. The anonymous case is a
// bare &fakeSessions{}, not this helper.
func postMatrixFake(userID int64, login, role string) *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(userID, login, []string{role}),
		authSession:   domain.Session{ID: "sm", UserID: userID, CSRFToken: "matrix-token"},
	}
}

// matrixPostReq builds a session-authenticated wp-json post-write request
// carrying the session cookie and the matching "matrix-token" CSRF token (the
// token every postMatrixFake uses).
func matrixPostReq(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "matrix-token")
	return req
}

func TestRESTPostWriteCapabilityMatrix(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		cases := []struct {
			name     string
			fake     *fakeSessions // nil → anonymous (bare &fakeSessions{})
			status   string        // "draft" or "publish"
			wantCode int
			wantErr  string // "" when wantCode is 201
		}{
			{"anonymous-draft", nil, "draft", http.StatusForbidden, "rest_cannot_create"},
			{"subscriber-draft", postMatrixFake(1, "sub", auth.RoleSubscriber), "draft", http.StatusForbidden, "rest_cannot_create"},
			{"contributor-draft", postMatrixFake(1, "contrib", auth.RoleContributor), "draft", http.StatusCreated, ""},
			{"author-draft", postMatrixFake(1, "author", auth.RoleAuthor), "draft", http.StatusCreated, ""},
			{"editor-draft", postMatrixFake(1, "editor", auth.RoleEditor), "draft", http.StatusCreated, ""},
			{"administrator-draft", postMatrixFake(1, "admin", auth.RoleAdministrator), "draft", http.StatusCreated, ""},
			// Publish sub-cases: publish_posts is now required. Contributor
			// lacks it → 403; author and up hold it → 201.
			{"contributor-publish", postMatrixFake(1, "contrib", auth.RoleContributor), "publish", http.StatusForbidden, "rest_cannot_create"},
			{"author-publish", postMatrixFake(1, "author", auth.RoleAuthor), "publish", http.StatusCreated, ""},
			{"editor-publish", postMatrixFake(1, "editor", auth.RoleEditor), "publish", http.StatusCreated, ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				fake := tc.fake
				if fake == nil {
					fake = &fakeSessions{}
				}
				h, repos := newRESTPostWriteRouter(t, fake)

				// A per-cell unique title so the no-state-change readback can
				// scan for exactly this cell's submission.
				title := "Matrix Create " + tc.name
				body := `{"title":"` + title + `","content":"body","status":"` + tc.status + `"}`
				var req *http.Request
				if tc.fake == nil {
					// Anonymous: no session cookie, no CSRF token.
					req = httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/posts", strings.NewReader(body))
				} else {
					req = matrixPostReq(http.MethodPost, "/wp-json/wp/v2/posts", body)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				if rec.Code != tc.wantCode {
					t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
				}
				if tc.wantErr != "" {
					if code := decodeRESTErrCode(t, rec); code != tc.wantErr {
						t.Errorf("error code = %q, want %q", code, tc.wantErr)
					}
					// Fail closed: a rejected create persisted no post with the
					// submitted title.
					assertNoPostWithTitle(t, repos, title)
				}
			})
		}
	})

	t.Run("update", func(t *testing.T) {
		cases := []struct {
			name     string
			fake     *fakeSessions // nil → anonymous
			wantCode int
			wantErr  string // "" when wantCode is 200
		}{
			{"anonymous", nil, http.StatusForbidden, "rest_cannot_edit"},
			// Subscriber lacks edit_posts entirely → 403 even as the owner.
			{"subscriber-owner", postMatrixFake(1, "sub", auth.RoleSubscriber), http.StatusForbidden, "rest_cannot_edit"},
			// OWN edit (caller is user 1, the stored author of post 1):
			// edit_posts + edit_published_posts suffice (post 1 is published).
			{"author-owner", postMatrixFake(1, "author", auth.RoleAuthor), http.StatusOK, ""},
			// OTHER-author edit (caller is user 7, stored author is 1):
			// edit_others_posts required. Author lacks it → 403; editor and
			// administrator hold it → 200.
			{"author-other", postMatrixFake(7, "author7", auth.RoleAuthor), http.StatusForbidden, "rest_cannot_edit"},
			{"editor-other", postMatrixFake(7, "editor7", auth.RoleEditor), http.StatusOK, ""},
			{"administrator-other", postMatrixFake(7, "admin7", auth.RoleAdministrator), http.StatusOK, ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				fake := tc.fake
				if fake == nil {
					fake = &fakeSessions{}
				}
				h, repos := newRESTPostWriteRouter(t, fake)

				before, err := repos.PostWriter.ByID(context.Background(), 1)
				if err != nil {
					t.Fatalf("PostWriter.ByID(1) before: %v", err)
				}

				body := `{"title":"Matrix Updated ` + tc.name + `","content":"matrix new body ` + tc.name + `"}`
				var req *http.Request
				if tc.fake == nil {
					req = httptest.NewRequest(http.MethodPut, "/wp-json/wp/v2/posts/1", strings.NewReader(body))
				} else {
					req = matrixPostReq(http.MethodPut, "/wp-json/wp/v2/posts/1", body)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				if rec.Code != tc.wantCode {
					t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
				}
				after, err := repos.PostWriter.ByID(context.Background(), 1)
				if err != nil {
					t.Fatalf("PostWriter.ByID(1) after: %v", err)
				}
				if tc.wantErr != "" {
					if code := decodeRESTErrCode(t, rec); code != tc.wantErr {
						t.Errorf("error code = %q, want %q", code, tc.wantErr)
					}
					// Fail closed: a rejected edit left Title/Content unchanged.
					if after.Title != before.Title {
						t.Errorf("a 403 edit persisted a title change: before=%q after=%q", before.Title, after.Title)
					}
					if after.Content != before.Content {
						t.Errorf("a 403 edit persisted a content change: before=%q after=%q", before.Content, after.Content)
					}
				} else if after.Title == before.Title && after.Content == before.Content {
					t.Errorf("a permitted edit did not persist; Title/Content unchanged (Title=%q Content=%q)", after.Title, after.Content)
				}
			})
		}
	})

	t.Run("delete", func(t *testing.T) {
		cases := []struct {
			name     string
			fake     *fakeSessions // nil → anonymous
			wantCode int
			wantErr  string // "" when wantCode is 200
		}{
			{"anonymous", nil, http.StatusForbidden, "rest_cannot_edit"},
			{"subscriber-owner", postMatrixFake(1, "sub", auth.RoleSubscriber), http.StatusForbidden, "rest_cannot_edit"},
			// OWN delete (caller is user 1): delete_posts + delete_published_posts.
			{"author-owner", postMatrixFake(1, "author", auth.RoleAuthor), http.StatusOK, ""},
			// OTHER-author delete (caller is user 7): delete_others_posts
			// required. Author lacks it → 403; editor/administrator hold it → 200.
			{"author-other", postMatrixFake(7, "author7", auth.RoleAuthor), http.StatusForbidden, "rest_cannot_edit"},
			{"editor-other", postMatrixFake(7, "editor7", auth.RoleEditor), http.StatusOK, ""},
			{"administrator-other", postMatrixFake(7, "admin7", auth.RoleAdministrator), http.StatusOK, ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				fake := tc.fake
				if fake == nil {
					fake = &fakeSessions{}
				}
				h, repos := newRESTPostWriteRouter(t, fake)

				var req *http.Request
				if tc.fake == nil {
					req = httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/posts/1", nil)
				} else {
					req = matrixPostReq(http.MethodDelete, "/wp-json/wp/v2/posts/1", "")
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				if rec.Code != tc.wantCode {
					t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
				}
				if tc.wantErr != "" {
					if code := decodeRESTErrCode(t, rec); code != tc.wantErr {
						t.Errorf("error code = %q, want %q", code, tc.wantErr)
					}
					// Fail closed: a rejected delete left post 1 resolvable.
					if _, err := repos.PostWriter.ByID(context.Background(), 1); err != nil {
						t.Errorf("a 403 delete removed post 1 (it must still resolve): %v", err)
					}
				} else {
					// A permitted delete removed post 1: it no longer resolves.
					if _, err := repos.PostWriter.ByID(context.Background(), 1); !errors.Is(err, domain.ErrNotFound) {
						t.Errorf("a permitted delete did not remove post 1; ByID err = %v, want domain.ErrNotFound", err)
					}
				}
			})
		}
	})
}

// assertNoPostWithTitle fails if any post carries the given title, proving a
// rejected create persisted nothing. It scans the admin list (which, unlike the
// public collection, includes draft/pending posts) so a would-be draft create
// is caught too.
func assertNoPostWithTitle(t *testing.T, repos *storage.Repositories, title string) {
	t.Helper()
	all, err := repos.AdminPosts.ListForAdmin(context.Background(), domain.AdminPostFilter{Types: []string{"post"}, Limit: 100})
	if err != nil {
		t.Fatalf("ListForAdmin: %v", err)
	}
	for _, p := range all {
		if p.Title == title {
			t.Errorf("a rejected create persisted a post: id=%d title=%q", p.ID, p.Title)
		}
	}
}
