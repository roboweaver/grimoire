package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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

// Task 7.1 — public comment-form transport (commentSubmit) write-boundary proof
// (Req 4.1, 4.4, 4.8, 9.5). These are end-to-end HTTP tests: a form POST goes
// through the real router, SessionMiddleware, commentSubmit, the live
// CommentService.Create, and lands in the SQLite store, and the assertion reads
// the raw stored bytes back out of {prefix}comments. That read is "what reached
// the writer": a value that fails these assertions is a value that reached
// storage unsanitized, which is exactly what Req 9.5 forbids.
//
// Written RED first (task 7.1): internal/web does not compile until tasks 7.2
// (commentSubmit builds the actor and calls the new Create signature), 7.5 and
// 8.2 land, and the two harnesses in the package (newCommentServer /
// newBackstopServer) still call content.NewCommentService with the pre-M10a
// arity. Once the package compiles, these tests assert the POST-7.2 behavior:
// the transport builds sanitize.Anonymous() by default and sanitize.For(p) when
// PrincipalFrom yields a principal, and maps content.ErrCommentEmpty onto its
// existing missing-field 400.

// newCommentFormAuthServer builds the full public server (real migrated+seeded
// SQLite store, live CommentService wired through the shared sanitize.Policy)
// AND wires WithAuth(fake, ...) so a logged-in principal can be injected on the
// public /comment form via a session cookie, AND returns the *storage.Repositories
// so a test can read back exactly what reached the comment writer. It is the
// union of newCommentServer (content features) and newAuthServer (session auth):
// neither existing harness does both and also exposes storage.
//
// fake.authPrincipal is the principal SessionMiddleware injects for any request
// carrying a (any-valued) session cookie — fakeSessions.Authenticate ignores the
// token and returns the configured principal/session — so passing an editor
// principal here lets the end-to-end test exercise commentSubmit's
// sanitize.For(p) branch and the tier-C bypass (editor holds unfiltered_html).
func newCommentFormAuthServer(t *testing.T, fake *fakeSessions) (http.Handler, *storage.Repositories, config.DatabaseConfig) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: root + "/grimoire.db", TablePrefix: "wp_"}
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
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())
	menus := content.NewNavMenuService(repos.NavMenus, "default")
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: root + "/uploads", BaseURL: "/wp-content/uploads"})
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithContentFeatures(comments, media, menus)
	if fake != nil {
		srv = srv.WithAuth(fake, web.AuthConfig{})
	}
	return srv.Routes(), repos, cfg
}

// submitCommentForm performs the GET→POST comment-form dance against the public
// /comment handler: it fetches a valid double-submit CSRF token+cookie from the
// single-post page, posts the form with the given content, and optionally adds a
// session cookie so SessionMiddleware injects the harness's authPrincipal. It
// returns the POST recorder so the caller can assert status/body.
func submitCommentForm(t *testing.T, h http.Handler, content string, sessionCookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	token, csrfCookie := commentCSRFToken(t, h)
	form := url.Values{
		"post_id":            {"1"},
		"author":             {"A"},
		"email":              {"a@example.com"},
		"content":            {content},
		"comment_csrf_token": {token},
	}
	req := httptest.NewRequest(http.MethodPost, "/comment", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	if sessionCookie != nil {
		req.AddCookie(sessionCookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// latestCommentContentFor reads the comment_content of the most recently
// created comment on post 1 straight out of storage — the bytes that actually
// reached the writer, independent of anything the redirect/echo returned.
func latestCommentContentFor(t *testing.T, repos *storage.Repositories, email string) (string, bool) {
	t.Helper()
	items, err := repos.Comments.List(context.Background(), domain.CommentFilter{PostID: 1})
	if err != nil {
		t.Fatalf("Comments.List: %v", err)
	}
	for _, c := range items {
		if c.AuthorEmail == email {
			return c.Content, true
		}
	}
	return "", false
}

// TestPublicCommentFormAnonymousSanitizesTierABeforeWriter is the Req 9.5 proof
// for the public form: an anonymous submission (no session cookie, so
// PrincipalFrom is absent and commentSubmit must build sanitize.Anonymous())
// carries both a disallowed <script> and an allow-listed <em>. The bytes that
// reach the comment writer must have the <script> stripped (Anonymous() selects
// tier A for CommentContent) and the <em> surviving. A transport that failed to
// build the actor — or built the wrong one — would either not compile, panic,
// or leave the <script> in storage; the storage read is what makes "<script>
// never reaches the writer" an assertion rather than a hope.
func TestPublicCommentFormAnonymousSanitizesTierABeforeWriter(t *testing.T) {
	h, repos, _ := newCommentFormAuthServer(t, nil)

	rec := submitCommentForm(t, h, `<em>ok</em><script>alert(1)</script>`, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /comment status = %d, want 303 (body=%s)", rec.Code, rec.Body.String())
	}

	stored, ok := latestCommentContentFor(t, repos, "a@example.com")
	if !ok {
		t.Fatalf("anonymous comment never reached the writer")
	}
	if strings.Contains(stored, "<script") || strings.Contains(stored, "alert(1)") {
		t.Errorf("tier-A sanitize did not run before the writer; stored = %q", stored)
	}
	if !strings.Contains(stored, "<em>ok</em>") {
		t.Errorf("allow-listed <em> did not survive tier A; stored = %q", stored)
	}
}

// TestPublicCommentFormMarkupOnlyBodyMapsErrCommentEmptyTo400 pins the
// ErrCommentEmpty → existing-400 contract (Req 4.8). The submitted content is
// markup-only that sanitizes to empty at tier A (a bare <script> with no other
// text), so content.CommentService.Create returns content.ErrCommentEmpty.
// commentSubmit must map that onto its EXISTING missing-field 400 — same status,
// same body — rather than inventing a new error contract. The body asserted here
// is commentSubmit's current literal for a missing required field, so this is a
// byte-identical-response assertion, not a new branch.
func TestPublicCommentFormMarkupOnlyBodyMapsErrCommentEmptyTo400(t *testing.T) {
	h, repos, _ := newCommentFormAuthServer(t, nil)

	rec := submitCommentForm(t, h, `<script>alert(1)</script>`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	// commentSubmit's existing missing-field body (http.Error appends a newline).
	const wantBody = "Bad Request: name, email, and comment are required\n"
	if rec.Body.String() != wantBody {
		t.Errorf("body = %q, want the existing missing-field 400 body %q", rec.Body.String(), wantBody)
	}
	if _, ok := latestCommentContentFor(t, repos, "a@example.com"); ok {
		t.Errorf("a markup-only submission that sanitizes to empty must not reach the writer")
	}
}

// TestPublicCommentFormLoggedInEditorUsesForPrincipalTierC proves commentSubmit
// builds sanitize.For(p) — not Anonymous() — when PrincipalFrom yields a
// principal: a logged-in editor holds unfiltered_html, so For(editor) selects
// tier C and the submitted <script> survives BYTE-IDENTICALLY in storage. If the
// transport ignored the principal and always built Anonymous(), the editor's
// <script> would be tier-A stripped and this test would fail — which is what
// makes it a positive proof that For(p) is wired, not merely that Anonymous()
// is the default.
//
// The session principal is injected by SessionMiddleware: fakeSessions returns
// authPrincipal for any request carrying a session cookie (the token value is
// ignored by the fake), so adding the cookie is enough to make the request
// logged-in end-to-end. The editor principal is built through the real
// auth.NewPrincipal/RoleEditor so a roles.go change that dropped unfiltered_html
// would surface here.
func TestPublicCommentFormLoggedInEditorUsesForPrincipalTierC(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "editor", []string{auth.RoleEditor}),
		authSession:   domain.Session{ID: "sid", UserID: 1, CSRFToken: "tok"},
	}
	h, repos, _ := newCommentFormAuthServer(t, fake)

	const raw = `<em>ok</em><script>alert(1)</script>`
	rec := submitCommentForm(t, h, raw, &http.Cookie{Name: "grimoire_session", Value: "anything"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /comment status = %d, want 303 (body=%s)", rec.Code, rec.Body.String())
	}

	stored, ok := latestCommentContentFor(t, repos, "a@example.com")
	if !ok {
		t.Fatalf("editor comment never reached the writer")
	}
	if stored != raw {
		t.Errorf("tier-C bypass not applied for unfiltered_html holder (For(p) not used?):\n got  %q\n want %q (byte-identical)", stored, raw)
	}
}
