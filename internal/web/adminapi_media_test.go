package web

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// capturingMediaRepo records the last domain.MediaFilter it received from
// MediaService, so handler tests can assert adminMediaList parses query
// parameters into the filter correctly without a real database.
type capturingMediaRepo struct {
	gotFilter domain.MediaFilter
	items     []domain.Media
	count     int
}

func (r *capturingMediaRepo) List(_ context.Context, f domain.MediaFilter) ([]domain.Media, error) {
	r.gotFilter = f
	return r.items, nil
}

func (r *capturingMediaRepo) Count(_ context.Context, f domain.MediaFilter) (int, error) {
	r.gotFilter = f
	return r.count, nil
}

func (r *capturingMediaRepo) ByID(context.Context, int64) (domain.Media, error) {
	return domain.Media{}, domain.ErrNotFound
}

func newMediaTestServer(t *testing.T, repo *capturingMediaRepo) *Server {
	t.Helper()
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.auth = fakeSessions{
		p: auth.NewPrincipal(1, "editor", []string{auth.RoleEditor, auth.RoleAdministrator}),
		s: domain.Session{CSRFToken: "token"},
	}
	srv.media = content.NewMediaService(repo, stubMediaWriter{}, content.MediaConfig{UploadsDir: t.TempDir()})
	return srv
}

func doMediaListRequest(t *testing.T, srv *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := srv.SessionMiddleware(srv.adminAPIRouter())
	req := httptest.NewRequest(http.MethodGet, "/media"+query, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAdminMediaListForwardsSearchTypeDateFilters(t *testing.T) {
	repo := &capturingMediaRepo{items: []domain.Media{{ID: 201}}, count: 1}
	srv := newMediaTestServer(t, repo)
	rec := doMediaListRequest(t, srv, "?search=jpg&type=image&after=2024-01-01&before=2024-02-01&parentId=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if repo.gotFilter.Search != "jpg" || repo.gotFilter.Type != "image" || repo.gotFilter.ParentID != 1 {
		t.Fatalf("filter not forwarded: %+v", repo.gotFilter)
	}
	wantAfter, _ := time.Parse("2006-01-02", "2024-01-01")
	wantBefore, _ := time.Parse("2006-01-02", "2024-02-01")
	if !repo.gotFilter.After.Equal(wantAfter) {
		t.Fatalf("After = %v, want %v", repo.gotFilter.After, wantAfter)
	}
	if !repo.gotFilter.Before.Equal(wantBefore) {
		t.Fatalf("Before = %v, want %v", repo.gotFilter.Before, wantBefore)
	}
}

func TestAdminMediaListInvalidTypeReturns400(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{})
	rec := doMediaListRequest(t, srv, "?type=spreadsheet")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] != "invalid_type" {
		t.Fatalf("error.code = %q, want invalid_type: %s", payload["error"]["code"], rec.Body.String())
	}
}

func TestAdminMediaListInvalidParentIDReturns400(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{})
	rec := doMediaListRequest(t, srv, "?parentId=not-a-number")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] != "invalid_parent_id" {
		t.Fatalf("error.code = %q, want invalid_parent_id: %s", payload["error"]["code"], rec.Body.String())
	}
}

func TestAdminMediaListInvalidAfterReturns400(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{})
	rec := doMediaListRequest(t, srv, "?after=not-a-date")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] != "invalid_after" {
		t.Fatalf("error.code = %q, want invalid_after: %s", payload["error"]["code"], rec.Body.String())
	}
}

func TestAdminMediaListInvalidBeforeReturns400(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{})
	rec := doMediaListRequest(t, srv, "?before=not-a-date")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] != "invalid_before" {
		t.Fatalf("error.code = %q, want invalid_before: %s", payload["error"]["code"], rec.Body.String())
	}
}

// TestAdminMediaListMissingFiltersReturns200 is the Requirement 4.5 negative
// case: no filter query parameters at all must succeed, not 400.
func TestAdminMediaListMissingFiltersReturns200(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{items: []domain.Media{{ID: 201}, {ID: 202}}, count: 2})
	rec := doMediaListRequest(t, srv, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminMediaListZeroResultsTotalPagesZero locks in the Requirement 8.1
// unified contract: an empty result set reports TotalPages 0, matching
// AdminService.List's existing behavior (I7) — not clamped to 1.
func TestAdminMediaListZeroResultsTotalPagesZero(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{items: nil, count: 0})
	rec := doMediaListRequest(t, srv, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		TotalPages int `json:"totalPages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if payload.TotalPages != 0 {
		t.Fatalf("totalPages=%d, want 0", payload.TotalPages)
	}
}

// TestAdminMediaListClampsExcessivePerPage is the I3 regression: a
// `perPage` above content.MaxPerPage must be clamped consistently — the
// filter forwarded to the repository, the reported `perPage`, and
// `totalPages` must all agree on the clamped value, never the raw query
// input.
func TestAdminMediaListClampsExcessivePerPage(t *testing.T) {
	repo := &capturingMediaRepo{items: []domain.Media{{ID: 201}}, count: 250}
	srv := newMediaTestServer(t, repo)
	rec := doMediaListRequest(t, srv, "?perPage=99999")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if repo.gotFilter.Limit != content.MaxPerPage {
		t.Fatalf("filter.Limit=%d, want clamped %d", repo.gotFilter.Limit, content.MaxPerPage)
	}
	var payload struct {
		PerPage    int `json:"perPage"`
		TotalPages int `json:"totalPages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if payload.PerPage != content.MaxPerPage {
		t.Fatalf("perPage=%d, want clamped %d", payload.PerPage, content.MaxPerPage)
	}
	wantTotalPages := content.TotalPages(250, content.MaxPerPage)
	if payload.TotalPages != wantTotalPages {
		t.Fatalf("totalPages=%d, want %d (computed against the clamped perPage, not the raw 99999)", payload.TotalPages, wantTotalPages)
	}
}

// authorCapturingMediaWriter records the domain.Media passed to Create so a
// handler test can assert the attachment's author was threaded through from
// the acting principal.
type authorCapturingMediaWriter struct {
	created []domain.Media
}

func (w *authorCapturingMediaWriter) Create(_ context.Context, m domain.Media) (int64, error) {
	w.created = append(w.created, m)
	return int64(len(w.created)), nil
}
func (w *authorCapturingMediaWriter) SetParent(context.Context, int64, int64) error { return nil }
func (w *authorCapturingMediaWriter) UpdateMeta(context.Context, int64, domain.MediaMeta) error {
	return nil
}

// TestAdminMediaUploadAuthorsAttachmentToActingAdmin proves the admin upload
// path threads the acting principal's user ID onto the stored attachment's
// post_author, matching the REST path (D7, Req 3.6).
func TestAdminMediaUploadAuthorsAttachmentToActingAdmin(t *testing.T) {
	const adminUserID int64 = 7
	writer := &authorCapturingMediaWriter{}
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.auth = fakeSessions{
		p: auth.NewPrincipal(adminUserID, "admin", []string{auth.RoleAdministrator}),
		s: domain.Session{CSRFToken: "token"},
	}
	srv.media = content.NewMediaService(&capturingMediaRepo{}, writer, content.MediaConfig{UploadsDir: t.TempDir(), BaseURL: "/wp-content/uploads"})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "photo.png")
	// Minimal PNG signature so DetectContentType sniffs image/png.
	fw.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	mw.Close()

	r := srv.SessionMiddleware(srv.adminAPIRouter())
	req := httptest.NewRequest(http.MethodPost, "/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", "token")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(writer.created) != 1 {
		t.Fatalf("Create called %d times, want 1", len(writer.created))
	}
	if got := writer.created[0].AuthorID; got != adminUserID {
		t.Fatalf("stored attachment AuthorID = %d, want acting admin %d", got, adminUserID)
	}
}

// newMediaUploadMultipart builds a multipart body carrying a minimal PNG
// signature under the "file" field, so http.DetectContentType sniffs
// image/png and the upload handler's MIME allowlist does not reject it. It
// returns the body and the Content-Type header value the request must carry.
func newMediaUploadMultipart(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "photo.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	// Minimal PNG signature so DetectContentType sniffs image/png.
	if _, err := fw.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}); err != nil {
		t.Fatalf("write PNG signature: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	return &body, mw.FormDataContentType()
}

// TestAdminMediaUploadCapabilityMatrix asserts the admin media upload route's
// CURRENT authorization contract as a table-driven capability matrix, driven
// through the real admin router (SessionMiddleware -> requireLoginJSON ->
// requireCapabilityJSON -> adminMediaUpload). It is TEST-ONLY and passes
// against the live handler with no new production code (Req 2.4, 2.6, 2.7;
// Finding 3; D1).
//
// Finding 3 — DECISION B discrepancy (asserting OBSERVED behavior):
// The spec's Finding 3 states the effective gate is edit_posts, wired at
// adminroutes.go:84. The live router does NOT match that claim: the media
// group is gated by requireCapabilityJSON("upload_files"), not edit_posts
// (see internal/web/adminroutes.go, the `gr.Use(s.requireCapabilityJSON(
// "upload_files"))` group mounting GET/POST /media and POST /media/{id}/
// attach). Per the no-production-change rule, this matrix asserts the OBSERVED
// gate (upload_files) rather than the claimed one. The three cells named in
// the task (administrator 201, subscriber 403, anonymous 401) produce the same
// outcomes under either capability because administrator holds BOTH caps and
// subscriber holds NEITHER. To make the matrix actively prove which capability
// gates the route, a fourth cell is added: a contributor principal, which
// holds edit_posts but NOT upload_files (see internal/auth/roles.go
// capabilitySets). Under the observed gate (upload_files) contributor is
// forbidden (403); had the gate truly been edit_posts, contributor would have
// been allowed (201). The contributor->403 assertion therefore distinguishes
// the two and records the discrepancy as a potential Finding.
//
// Confirmed codes against current code:
//   - success: 201 (adminMediaUpload's writeJSON(w, http.StatusCreated, ...)).
//   - forbidden (authenticated, lacks capability): 403, envelope code
//     "forbidden" (requireCapabilityJSON).
//   - anonymous (no session principal): 401, envelope code "unauthorized"
//     (requireLoginJSON runs first via r.Use, so the no-principal case is 401,
//     NOT 403).
func TestAdminMediaUploadCapabilityMatrix(t *testing.T) {
	const actingUserID int64 = 7

	cases := []struct {
		name string
		// roles is the principal's role set; nil means anonymous (no session
		// cookie, no injected principal).
		roles []string
		// sendCSRF controls whether a valid X-CSRF-Token header is attached.
		// Allowed cells must send it (the handler enforces CSRF after the
		// gate); forbidden/anonymous cells are rejected at the gate before CSRF
		// is ever checked, so it is irrelevant there.
		sendCSRF    bool
		wantCode    int
		wantErrCode string // admin-JSON envelope error.code; "" for success
		wantRows    int    // writer.created length after the request
	}{
		{
			// administrator holds upload_files (and edit_posts): allowed -> 201.
			name:     "administrator_holds_upload_files_allowed",
			roles:    []string{auth.RoleAdministrator},
			sendCSRF: true,
			wantCode: http.StatusCreated,
			wantRows: 1,
		},
		{
			// author holds upload_files: allowed -> 201 (control confirming the
			// gate is upload_files, which author has).
			name:     "author_holds_upload_files_allowed",
			roles:    []string{auth.RoleAuthor},
			sendCSRF: true,
			wantCode: http.StatusCreated,
			wantRows: 1,
		},
		{
			// DECISION B discriminator: contributor holds edit_posts but NOT
			// upload_files. Observed gate is upload_files -> 403 forbidden. Had
			// the gate been edit_posts (Finding 3's claim) this would be 201.
			name:        "contributor_has_edit_posts_but_not_upload_files_forbidden",
			roles:       []string{auth.RoleContributor},
			sendCSRF:    true,
			wantCode:    http.StatusForbidden,
			wantErrCode: "forbidden",
			wantRows:    0,
		},
		{
			// subscriber holds neither upload_files nor edit_posts -> 403.
			name:        "subscriber_lacks_upload_files_forbidden",
			roles:       []string{auth.RoleSubscriber},
			sendCSRF:    true,
			wantCode:    http.StatusForbidden,
			wantErrCode: "forbidden",
			wantRows:    0,
		},
		{
			// anonymous (no session) -> 401 unauthorized (requireLoginJSON).
			name:        "anonymous_no_session_unauthorized",
			roles:       nil,
			sendCSRF:    false,
			wantCode:    http.StatusUnauthorized,
			wantErrCode: "unauthorized",
			wantRows:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer := &authorCapturingMediaWriter{}
			srv := NewServer(nil, nil, nil, nil, nil)
			if tc.roles != nil {
				srv.auth = fakeSessions{
					p: auth.NewPrincipal(actingUserID, "acting", tc.roles),
					s: domain.Session{CSRFToken: "token"},
				}
			}
			srv.media = content.NewMediaService(&capturingMediaRepo{}, writer, content.MediaConfig{UploadsDir: t.TempDir(), BaseURL: "/wp-content/uploads"})

			body, contentType := newMediaUploadMultipart(t)
			r := srv.SessionMiddleware(srv.adminAPIRouter())
			req := httptest.NewRequest(http.MethodPost, "/media", body)
			req.Header.Set("Content-Type", contentType)
			if tc.roles != nil {
				// A session cookie is required for SessionMiddleware to resolve
				// the fakeSessions principal into the request context.
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
			}
			if tc.sendCSRF {
				req.Header.Set("X-CSRF-Token", "token")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
			}

			if tc.wantErrCode != "" {
				var payload map[string]map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
					t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
				}
				if got := payload["error"]["code"]; got != tc.wantErrCode {
					t.Fatalf("error.code = %q, want %q (%s)", got, tc.wantErrCode, rec.Body.String())
				}
			}

			// No-state-change: every forbidden/unauthorized cell must leave the
			// capturing writer untouched (zero Create calls); the allowed cell
			// records exactly one row.
			if len(writer.created) != tc.wantRows {
				t.Fatalf("writer.created length = %d, want %d", len(writer.created), tc.wantRows)
			}
		})
	}
}
