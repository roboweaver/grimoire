package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// Task 7.1 — REST comment-create transport (handleRESTCommentCreate)
// write-boundary proof (Req 4.1, 4.4, 4.8, 9.5). Same shape as the public-form
// tests in comments_test.go, through the wp-json POST /comments transport: the
// request goes through the real router, the live CommentService.Create and the
// shared sanitize.Policy, lands in the SQLite store, and the assertion reads the
// raw stored bytes back — "what reached the writer".
//
// Written RED first (task 7.1): the package does not compile until tasks 7.2 /
// 7.5 / 8.2 land. Once it compiles, these assert the POST-7.2 behavior:
// handleRESTCommentCreate builds sanitize.Anonymous() by default and
// sanitize.For(p) when PrincipalFrom yields a principal, and maps
// content.ErrCommentEmpty onto its EXISTING 400 branch (error code
// "rest_comment_content_invalid"), byte-identically.
//
// The harness is newAppPasswordRESTRouter (defined in
// rest_apppassword_comments_test.go): it wires WithAuth + the full REST surface
// and returns the seeded *storage.Repositories so a test can read a persisted
// comment directly. The decodeRESTErrCode helper used below is the one shared by
// the other REST tests in this package.

// TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter is the Req 9.5 proof
// for the REST transport: an anonymous POST (no session, no Application
// Password, so PrincipalFrom is absent and the handler must build
// sanitize.Anonymous()) carries a disallowed <script> and an allow-listed <em>.
// The bytes reaching the writer must have the <script> stripped (tier A) and the
// <em> surviving.
func TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter(t *testing.T) {
	h, repos, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")

	body := `{"post":1,"author_name":"Anon","author_email":"anon-tierA@example.com","content":"<em>ok</em><script>alert(1)</script>"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	req.RemoteAddr = "198.51.100.7:9999"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}

	items, err := repos.Comments.List(context.Background(), domain.CommentFilter{PostID: 1})
	if err != nil {
		t.Fatalf("Comments.List: %v", err)
	}
	var stored string
	var found bool
	for _, c := range items {
		if c.AuthorEmail == "anon-tierA@example.com" {
			stored, found = c.Content, true
		}
	}
	if !found {
		t.Fatalf("anonymous REST comment never reached the writer")
	}
	if strings.Contains(stored, "<script") || strings.Contains(stored, "alert(1)") {
		t.Errorf("tier-A sanitize did not run before the writer; stored = %q", stored)
	}
	if !strings.Contains(stored, "<em>ok</em>") {
		t.Errorf("allow-listed <em> did not survive tier A; stored = %q", stored)
	}
}

// TestRESTCommentCreateMarkupOnlyBodyMapsErrCommentEmptyTo400 pins the
// ErrCommentEmpty → existing-400 contract for REST (Req 4.8). The content is
// markup-only that sanitizes to empty at tier A, so CommentService.Create
// returns content.ErrCommentEmpty, which handleRESTCommentCreate must map onto
// its EXISTING 400 — the same status and the same error code
// "rest_comment_content_invalid" it already returns for a missing/empty content
// field — rather than a new contract. (The non-empty content passes the
// handler's own required-field guard, so reaching this 400 proves the mapping
// of the service-level sentinel, not the pre-decode field check.)
func TestRESTCommentCreateMarkupOnlyBodyMapsErrCommentEmptyTo400(t *testing.T) {
	h, repos, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")

	body := `{"post":1,"author_name":"Anon","author_email":"anon-empty@example.com","content":"<script>alert(1)</script>"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_comment_content_invalid" {
		t.Errorf("error code = %q, want the existing rest_comment_content_invalid", code)
	}

	items, err := repos.Comments.List(context.Background(), domain.CommentFilter{PostID: 1})
	if err != nil {
		t.Fatalf("Comments.List: %v", err)
	}
	for _, c := range items {
		if c.AuthorEmail == "anon-empty@example.com" {
			t.Errorf("a markup-only submission that sanitizes to empty must not reach the writer; stored = %q", c.Content)
		}
	}
}

// TestRESTCommentCreateLoggedInEditorUsesForPrincipalTierC proves
// handleRESTCommentCreate builds sanitize.For(p) when PrincipalFrom yields a
// principal: a session-authenticated editor holds unfiltered_html, so For(editor)
// selects tier C and the submitted <script> survives byte-identically in
// storage. A handler that always built Anonymous() would tier-A strip it and
// fail this test. Session auth on the REST comment path requires the M4 CSRF
// synchronizer token, so the request carries both the session cookie and the
// matching X-CSRF-Token.
func TestRESTCommentCreateLoggedInEditorUsesForPrincipalTierC(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "editor", []string{auth.RoleEditor}),
		authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: "correct-token"},
	}
	h, repos, _ := newAppPasswordRESTRouter(t, fake, false, "")

	const raw = `<em>ok</em><script>alert(1)</script>`
	body := `{"post":1,"author_name":"Ed","author_email":"editor-tierC@example.com","content":"<em>ok</em><script>alert(1)</script>"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "correct-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}

	items, err := repos.Comments.List(context.Background(), domain.CommentFilter{PostID: 1})
	if err != nil {
		t.Fatalf("Comments.List: %v", err)
	}
	var stored string
	var found bool
	for _, c := range items {
		if c.AuthorEmail == "editor-tierC@example.com" {
			stored, found = c.Content, true
		}
	}
	if !found {
		t.Fatalf("editor REST comment never reached the writer")
	}
	if stored != raw {
		t.Errorf("tier-C bypass not applied for unfiltered_html holder (For(p) not used?):\n got  %q\n want %q (byte-identical)", stored, raw)
	}
}

// TestRESTCommentCreateCapabilityMatrix is the M10.E consolidated coverage
// matrix for the REST comment-create route (POST /wp-json/wp/v2/comments).
//
// NOTE (Finding 2 — this is an AUTHORIZATION-CONTRACT matrix, NOT an
// allowed-vs-forbidden *capability* split like the other M10 write-route
// matrices. Comment create is ANONYMOUS-ALLOWED: handleRESTCommentCreate builds
// sanitize.Anonymous() when no principal is present and proceeds, and the only
// capability in the path — moderate_comments — gates comment *status/moderation*
// (isModerator), never whether a comment may be CREATED. There is no caller for
// whom create returns a capability 403. So this table enumerates the outcomes the
// route genuinely distinguishes — the anonymous/subscriber/editor success path,
// the sanitize-to-empty 400, the closed-comments 403, and the session-CSRF 403 —
// rather than a create capability that does not exist.
//
// Every principal is built through the real auth path (auth.NewPrincipal →
// auth.CapabilitiesForRoles), never a hand-forged capability set. Auth mechanism:
// a bare request (no session cookie, no CSRF) for the anonymous-allowed cell
// (matching TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter); a session
// fake carrying the principal + a domain.Session{CSRFToken} with a matching
// grimoire_session cookie and X-CSRF-Token header for the logged-in cells.
//
// No-state-change on forbidden: for every non-201 cell the test scans
// repos.Comments.List(ctx, domain.CommentFilter{PostID: ...}) by that cell's
// unique author_email and asserts the comment is absent.
func TestRESTCommentCreateCapabilityMatrix(t *testing.T) {
	// A session fake carrying the given principal + CSRF token, mirroring the
	// logged-in cells of TestRESTCommentCreateLoggedInEditorUsesForPrincipalTierC.
	sessionFake := func(p auth.Principal) *fakeSessions {
		return &fakeSessions{
			authPrincipal: p,
			authSession:   domain.Session{ID: "s1", UserID: p.UserID, CSRFToken: "correct-token"},
		}
	}

	const (
		postOpen   = 1  // seeded published post that accepts comments
		postClosed = 50 // seeded published post with comment_status="closed" (newAppPasswordRESTRouter)
	)

	cases := []struct {
		name string
		fake *fakeSessions
		// authEmail is the unique author_email this cell submits — used both as
		// the request's author_email and as the no-state-change scan key.
		authEmail string
		// post is the target post id (postOpen unless the cell targets the
		// closed-comments post).
		post int
		// addSession attaches the grimoire_session cookie (logged-in cells).
		addSession bool
		// csrf, when non-empty, sets the X-CSRF-Token header. Left empty on the
		// missing-CSRF cell to exercise the session-CSRF 403 seam.
		csrf string
		// content overrides the default non-empty body content (used by the
		// markup-only-sanitizes-to-empty cell).
		content  string
		wantCode int
		// wantErr is the expected flat wp-json error code for a non-201 cell.
		wantErr string
		// wantPersist is true when the cell expects the comment to reach the
		// writer (the 201 cells); false cells assert the comment is absent.
		wantPersist bool
	}{
		{
			name:        "anonymous allowed → 201 persisted",
			fake:        &fakeSessions{},
			authEmail:   "matrix-anon@example.com",
			post:        postOpen,
			wantCode:    http.StatusCreated,
			wantPersist: true,
		},
		{
			name:        "subscriber (session+CSRF) → 201 persisted",
			fake:        sessionFake(auth.NewPrincipal(1, "subscriber", []string{auth.RoleSubscriber})),
			authEmail:   "matrix-subscriber@example.com",
			post:        postOpen,
			addSession:  true,
			csrf:        "correct-token",
			wantCode:    http.StatusCreated,
			wantPersist: true,
		},
		{
			name:        "editor moderate_comments (session+CSRF) → 201 persisted",
			fake:        sessionFake(auth.NewPrincipal(1, "editor", []string{auth.RoleEditor})),
			authEmail:   "matrix-editor@example.com",
			post:        postOpen,
			addSession:  true,
			csrf:        "correct-token",
			wantCode:    http.StatusCreated,
			wantPersist: true,
		},
		{
			name:      "markup-only body sanitizes to empty at tier A → 400 rest_comment_content_invalid",
			fake:      &fakeSessions{},
			authEmail: "matrix-empty@example.com",
			post:      postOpen,
			// Non-empty at the handler's required-field guard, but tier-A
			// sanitize reduces it to empty inside CommentService.Create, which
			// returns content.ErrCommentEmpty → the existing 400 branch.
			content:     "<script>alert(1)</script>",
			wantCode:    http.StatusBadRequest,
			wantErr:     "rest_comment_content_invalid",
			wantPersist: false,
		},
		{
			name:        "closed-comments post → 403 rest_comment_closed",
			fake:        &fakeSessions{},
			authEmail:   "matrix-closed@example.com",
			post:        postClosed,
			wantCode:    http.StatusForbidden,
			wantErr:     "rest_comment_closed",
			wantPersist: false,
		},
		{
			name:        "session cell with missing CSRF → 403 rest_forbidden",
			fake:        sessionFake(auth.NewPrincipal(1, "subscriber", []string{auth.RoleSubscriber})),
			authEmail:   "matrix-nocsrf@example.com",
			post:        postOpen,
			addSession:  true,
			csrf:        "", // deliberately omitted: exercises requireSessionCSRFREST
			wantCode:    http.StatusForbidden,
			wantErr:     "rest_forbidden",
			wantPersist: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, repos, _ := newAppPasswordRESTRouter(t, tc.fake, false, "")

			content := tc.content
			if content == "" {
				content = "Matrix comment body"
			}
			body := `{"post":` + strconv.Itoa(tc.post) +
				`,"author_name":"Matrix","author_email":"` + tc.authEmail +
				`","content":"` + content + `"}`
			req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/comments", strings.NewReader(body))
			req.RemoteAddr = "198.51.100.7:9999"
			if tc.addSession {
				req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
			}
			if tc.csrf != "" {
				req.Header.Set("X-CSRF-Token", tc.csrf)
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
			}

			// Read back through the real store by this cell's unique email.
			items, err := repos.Comments.List(context.Background(), domain.CommentFilter{PostID: int64(tc.post)})
			if err != nil {
				t.Fatalf("Comments.List: %v", err)
			}
			var persisted bool
			for _, c := range items {
				if c.AuthorEmail == tc.authEmail {
					persisted = true
				}
			}
			if tc.wantPersist && !persisted {
				t.Errorf("expected comment for %q to be persisted, but it was absent", tc.authEmail)
			}
			if !tc.wantPersist && persisted {
				t.Errorf("no-state-change violated: a non-201 cell persisted a comment for %q", tc.authEmail)
			}
		})
	}
}
