package web

import (
	"context"
	"encoding/json"
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
