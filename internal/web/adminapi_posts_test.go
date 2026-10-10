package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// fakePostWrite implements postAdminWriter for handler-level tests.
type fakePostWrite struct {
	create       func(p domain.Post) (int64, error)
	update       func(p domain.Post, expectedModified time.Time) error
	del          func(p domain.Post) error
	lastCreate   domain.Post
	lastUpdate   domain.Post
	lastExpected time.Time
	lastDelete   domain.Post
}

func (f *fakePostWrite) Create(_ context.Context, _ auth.Principal, p domain.Post) (int64, error) {
	f.lastCreate = p
	if f.create != nil {
		return f.create(p)
	}
	return 1, nil
}

func (f *fakePostWrite) Update(_ context.Context, _ auth.Principal, p domain.Post, expectedModified time.Time) error {
	f.lastUpdate = p
	f.lastExpected = expectedModified
	if f.update != nil {
		return f.update(p, expectedModified)
	}
	return nil
}

func (f *fakePostWrite) Delete(_ context.Context, _ auth.Principal, p domain.Post) error {
	f.lastDelete = p
	if f.del != nil {
		return f.del(p)
	}
	return nil
}

// fakePostTermsWrite implements postTermsAdminWriter.
type fakePostTermsWrite struct {
	setPostTerms func(postID int64, taxonomy string, termIDs []int64) error
	calls        []string
}

func (f *fakePostTermsWrite) SetPostTerms(_ context.Context, _ auth.Principal, postID int64, taxonomy string, termIDs []int64) error {
	f.calls = append(f.calls, taxonomy)
	if f.setPostTerms != nil {
		return f.setPostTerms(postID, taxonomy, termIDs)
	}
	return nil
}

// fakePostTermsRead implements postTermsAdminReader.
type fakePostTermsRead struct {
	terms map[string][]int64 // taxonomy -> term IDs
}

func (f *fakePostTermsRead) TermsForPost(_ context.Context, _ int64, taxonomy string) ([]int64, error) {
	return f.terms[taxonomy], nil
}

// fakeTermWrite implements termAdminService.
type fakeTermWrite struct {
	create       func(t domain.Term) (int64, error)
	update       func(t domain.Term) error
	del          func(id int64) error
	listByTax    func(taxonomy string) ([]domain.Term, error)
	byIDs        map[int64]domain.Term
	lastCreate   domain.Term
	lastUpdate   domain.Term
	lastDeleteID int64
}

func (f *fakeTermWrite) Create(_ context.Context, _ auth.Principal, t domain.Term) (int64, error) {
	f.lastCreate = t
	if f.create != nil {
		return f.create(t)
	}
	return 1, nil
}

func (f *fakeTermWrite) Update(_ context.Context, _ auth.Principal, t domain.Term) error {
	f.lastUpdate = t
	if f.update != nil {
		return f.update(t)
	}
	return nil
}

func (f *fakeTermWrite) Delete(_ context.Context, _ auth.Principal, id int64) error {
	f.lastDeleteID = id
	if f.del != nil {
		return f.del(id)
	}
	return nil
}

func (f *fakeTermWrite) ListByTaxonomy(_ context.Context, taxonomy string) ([]domain.Term, error) {
	if f.listByTax != nil {
		return f.listByTax(taxonomy)
	}
	return nil, nil
}

func (f *fakeTermWrite) TermsByIDs(_ context.Context, ids []int64) ([]domain.Term, error) {
	out := make([]domain.Term, 0, len(ids))
	for _, id := range ids {
		if t, ok := f.byIDs[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// testWriteServer builds a Server wired for the write-handler unit tests:
// admin (read detail), postWrite, termWrite, postTermsWrite/Read fakes.
func testWriteServer(a adminReader, pw postAdminWriter, tw termAdminService, ptw postTermsAdminWriter, ptr postTermsAdminReader) *Server {
	return &Server{
		log:            slog.Default(),
		admin:          a,
		postWrite:      pw,
		termWrite:      tw,
		postTermsWrite: ptw,
		postTermsRead:  ptr,
	}
}

func TestAdminPostCreateHappyPath(t *testing.T) {
	pw := &fakePostWrite{create: func(domain.Post) (int64, error) { return 42, nil }}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) {
		return domain.Post{ID: id, Title: "Hello", Slug: "hello", Type: "post", Status: "draft", Content: "Body", Modified: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)}, nil
	}}
	s := testWriteServer(a, pw, nil, nil, nil)
	body := `{"title":"Hello","content":"Body","status":"draft"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["id"].(float64) != 42 || resp["title"] != "Hello" {
		t.Errorf("resp = %v", resp)
	}
	if pw.lastCreate.Status != "draft" || pw.lastCreate.Type != "post" {
		t.Errorf("service got = %+v", pw.lastCreate)
	}
}

func TestAdminPostCreateDefaultsStatusToDraft(t *testing.T) {
	pw := &fakePostWrite{}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return domain.Post{ID: id}, nil }}
	s := testWriteServer(a, pw, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(`{"title":"x"}`)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	if pw.lastCreate.Status != "draft" {
		t.Errorf("status = %q, want draft", pw.lastCreate.Status)
	}
}

func TestAdminPostCreateRejectsMissingTitleUnlessDraft(t *testing.T) {
	s := testWriteServer(&fakeAdmin{}, &fakePostWrite{}, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(`{"status":"publish"}`)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertJSONError(t, rec, "bad_request")
}

func TestAdminPostCreateRejectsInvalidStatus(t *testing.T) {
	s := testWriteServer(&fakeAdmin{}, &fakePostWrite{}, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(`{"title":"x","status":"bogus"}`)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAdminPostCreateRejectsInvalidType(t *testing.T) {
	s := testWriteServer(&fakeAdmin{}, &fakePostWrite{}, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(`{"title":"x","type":"widget"}`)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAdminPostCreateRejectsFutureStatusWithPastDate(t *testing.T) {
	s := testWriteServer(&fakeAdmin{}, &fakePostWrite{}, nil, nil, nil)
	body := `{"title":"x","status":"future","date":"2000-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminPostCreateRejectsFutureStatusWithNoDate guards finding #1 from
// the PR #16 review: a future-status post created with no date at all must
// be rejected, not silently stored with today's date (which would make it
// immediately "published" despite the future status label).
func TestAdminPostCreateRejectsFutureStatusWithNoDate(t *testing.T) {
	s := testWriteServer(&fakeAdmin{}, &fakePostWrite{}, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(`{"title":"x","status":"future"}`)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminPostCreateDefaultsDateWhenOmitted guards finding #1: a draft/
// publish post created with no date must be stored with something close to
// "now" (via PostWriteService.Create's defaulting), not the epoch. The
// admin API layer doesn't do the defaulting itself (that's the write
// service's job per its doc comment), so this only asserts parsePostWrite
// hands through a zero Date unmolested for the service to default.
func TestAdminPostCreateDefaultsDateWhenOmitted(t *testing.T) {
	var captured domain.Post
	pw := &fakePostWrite{create: func(p domain.Post) (int64, error) {
		captured = p
		return 5, nil
	}}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return domain.Post{ID: id}, nil }}
	s := testWriteServer(a, pw, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(`{"title":"x","status":"draft"}`)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	if !captured.Date.IsZero() {
		t.Fatalf("parsePostWrite should hand a zero Date to the write service when omitted, got %v", captured.Date)
	}
}

func TestAdminPostCreateAppliesTermIDsAndReportsPartial(t *testing.T) {
	pw := &fakePostWrite{create: func(domain.Post) (int64, error) { return 5, nil }}
	ptw := &fakePostTermsWrite{setPostTerms: func(_ int64, taxonomy string, _ []int64) error {
		if taxonomy == "post_tag" {
			return domain.ErrNotFound
		}
		return nil
	}}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return domain.Post{ID: id}, nil }}
	s := testWriteServer(a, pw, nil, ptw, &fakePostTermsRead{})
	body := `{"title":"x","status":"draft","termIds":{"category":[1],"post_tag":[2]}}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	partial, ok := resp["partial"].(map[string]any)
	if !ok {
		t.Fatalf("partial missing/wrong type: %v", resp)
	}
	if _, ok := partial["post_tag"]; !ok {
		t.Errorf("partial should contain post_tag failure: %v", partial)
	}
	if _, ok := partial["category"]; ok {
		t.Errorf("partial should not contain category (succeeded): %v", partial)
	}
}

func TestAdminPostUpdateHappyPath(t *testing.T) {
	stored := domain.Post{ID: 7, Title: "Old", Modified: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	pw := &fakePostWrite{}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) {
		return domain.Post{ID: id, Title: "New", Modified: stored.Modified}, nil
	}}
	s := testWriteServer(a, pw, nil, nil, nil)
	body := `{"title":"New","status":"draft","modified":"2024-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/7", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "7")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if pw.lastUpdate.ID != 7 || pw.lastUpdate.Title != "New" {
		t.Errorf("update called with = %+v", pw.lastUpdate)
	}
	if !pw.lastExpected.Equal(stored.Modified) {
		t.Errorf("expectedModified = %v, want %v", pw.lastExpected, stored.Modified)
	}
}

func TestAdminPostUpdateRequiresModified(t *testing.T) {
	s := testWriteServer(&fakeAdmin{}, &fakePostWrite{}, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/7", strings.NewReader(`{"title":"x","status":"draft"}`)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "7")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPostUpdateConflictOnStaleModified(t *testing.T) {
	conflictAt := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	pw := &fakePostWrite{update: func(domain.Post, time.Time) error {
		return &content.ConflictError{CurrentModified: conflictAt}
	}}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return domain.Post{ID: id}, nil }}
	s := testWriteServer(a, pw, nil, nil, nil)
	body := `{"title":"x","status":"draft","modified":"2020-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/7", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "7")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["error"] != "conflict" {
		t.Errorf("error field = %v, want conflict", resp["error"])
	}
	if resp["currentModified"] != conflictAt.Format(time.RFC3339) {
		t.Errorf("currentModified = %v, want %v", resp["currentModified"], conflictAt.Format(time.RFC3339))
	}
}

func TestAdminPostUpdateAllowsUnchangedFutureDate(t *testing.T) {
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	pw := &fakePostWrite{}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) {
		return domain.Post{ID: id, Status: "future", Date: past, Modified: past}, nil
	}}
	s := testWriteServer(a, pw, nil, nil, nil)
	body := `{"title":"x","status":"future","date":"2000-01-01T00:00:00Z","modified":"2000-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/9", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "9")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (unchanged future date exception), body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminPostUpdateRejectsFutureTransitionWithUnchangedPastDate guards
// re-review finding #2: the unchanged-date exception (Req 5.2) must only
// apply when the post is ALREADY status "future". A draft/published post
// (whose date is necessarily in the past) transitioning into "future" while
// resubmitting its own currently-stored past date unchanged must still be
// rejected -- otherwise the exception lets any past-dated post become
// "future" with no future date required, simply by not touching the date
// field.
func TestAdminPostUpdateRejectsFutureTransitionWithUnchangedPastDate(t *testing.T) {
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) {
		return domain.Post{ID: id, Status: "draft", Date: past, Modified: past}, nil
	}}
	s := testWriteServer(a, &fakePostWrite{}, nil, nil, nil)
	body := `{"title":"x","status":"future","date":"2000-01-01T00:00:00Z","modified":"2000-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/9", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "9")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (draft->future transition must require an explicit future date), body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPostUpdateRejectsChangedPastFutureDate(t *testing.T) {
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	otherPast := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) {
		return domain.Post{ID: id, Status: "future", Date: past}, nil
	}}
	s := testWriteServer(a, &fakePostWrite{}, nil, nil, nil)
	body := `{"title":"x","status":"future","date":"` + otherPast.Format(time.RFC3339) + `","modified":"2000-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/9", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "9")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminPostUpdateMissingRecordIsForbidden guards finding #9 from the PR
// #16 review: production's PostWriteService.Update never returns the raw
// domain.ErrNotFound for a missing record -- per its own doc comment it maps
// a missing record to the generic ErrForbidden (existence is not leaked,
// Req 1.6). The previous version of this test stubbed the write service to
// return domain.ErrNotFound directly and asserted 404, which exercised a
// path production never hits and would have kept passing even if the real
// 403-for-missing behavior regressed. It now stubs the real contract.
func TestAdminPostUpdateMissingRecordIsForbidden(t *testing.T) {
	pw := &fakePostWrite{update: func(domain.Post, time.Time) error { return content.ErrForbidden }}
	a := &fakeAdmin{detail: func(int64) (domain.Post, error) { return domain.Post{}, domain.ErrNotFound }}
	s := testWriteServer(a, pw, nil, nil, nil)
	body := `{"title":"x","status":"draft","modified":"2020-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/999", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "999")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing record maps to ErrForbidden, not ErrNotFound), body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPostUpdateForbidden(t *testing.T) {
	pw := &fakePostWrite{update: func(domain.Post, time.Time) error { return content.ErrForbidden }}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return domain.Post{ID: id}, nil }}
	s := testWriteServer(a, pw, nil, nil, nil)
	body := `{"title":"x","status":"draft","modified":"2020-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/9", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "9")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPostDeleteHappyPath(t *testing.T) {
	pw := &fakePostWrite{}
	s := testWriteServer(&fakeAdmin{}, pw, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/posts/3", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "3")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostDelete).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if pw.lastDelete.ID != 3 {
		t.Errorf("delete called with id=%d, want 3", pw.lastDelete.ID)
	}
}

// TestAdminPostDeleteMissingRecordIsForbidden guards finding #9 the same way
// as TestAdminPostUpdateMissingRecordIsForbidden: production's
// PostWriteService.Delete maps a missing record to ErrForbidden, never the
// raw domain.ErrNotFound, so the test must stub and assert that contract.
func TestAdminPostDeleteMissingRecordIsForbidden(t *testing.T) {
	pw := &fakePostWrite{del: func(domain.Post) error { return content.ErrForbidden }}
	s := testWriteServer(&fakeAdmin{}, pw, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/posts/999", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "999")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostDelete).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing record maps to ErrForbidden, not ErrNotFound)", rec.Code)
	}
}

func TestAdminPostDeleteBadID(t *testing.T) {
	s := testWriteServer(&fakeAdmin{}, &fakePostWrite{}, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/posts/abc", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "abc")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostDelete).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAdminPostDetailResolvesTerms(t *testing.T) {
	tw := &fakeTermWrite{byIDs: map[int64]domain.Term{
		1: {ID: 1, Name: "Zeta", Slug: "zeta"},
		2: {ID: 2, Name: "Alpha", Slug: "alpha"},
	}}
	ptr := &fakePostTermsRead{terms: map[string][]int64{"category": {1, 2}}}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return domain.Post{ID: id}, nil }}
	s := testWriteServer(a, nil, tw, nil, ptr)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/1", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "1")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPost).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Terms map[string][]struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"terms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cats := resp.Terms["category"]
	if len(cats) != 2 || cats[0].Name != "Alpha" || cats[1].Name != "Zeta" {
		t.Errorf("category terms = %+v, want [Alpha, Zeta] sorted", cats)
	}
	if tags, ok := resp.Terms["post_tag"]; !ok || len(tags) != 0 {
		t.Errorf("post_tag terms = %+v, want empty non-nil slice present", resp.Terms["post_tag"])
	}
}

// --- Task 7.3: admin POST-write transport content-safety proof (Req 4.2, 4.4, 9.5) ---
//
// These tests assert END-TO-END, through the real adminPostCreate /
// adminPostUpdate handlers (adminapi_posts.go), that content/excerpt are
// sanitized and the title is reduced to plain text, and that a title which
// sanitizes to empty maps onto the admin API's EXISTING missing-title 400
// (badRequestError -> "bad_request").
//
// The proof is end-to-end on purpose: unlike the handler-level tests above
// (which inject a fakePostWrite that records whatever it is handed), these wire
// the REAL *content.PostWriteService as the Server's postWrite dependency, over
// an in-memory domain.PostWriter. The sanitization therefore happens inside
// PostWriteService.Create/Update exactly as production does -- a transport that
// wrote directly to storage, bypassing PostWriteService, could NOT pass these.
// The actor is an author (RoleAuthor, built through the real auth tables), who
// LACKS unfiltered_html and so is a tier-B writer for post content/excerpt and
// gets the plain-text title path (sanitize.TierFor); an editor would be tier C
// and would not strip anything, so the author is the role that makes the
// stripping observable.
//
// PostWriteService defaults its policy to sanitize.New() (NewPostWriteService),
// so these pass once the package compiles even before task 8.2 wires an
// explicit WithContentPolicy -- the behavior is asserted regardless of whether
// the shared policy is injected or defaulted.

// memPostWriter is a tiny in-memory domain.PostWriter: the real
// PostWriteService writes the SANITIZED post into it, and the test reads the
// stored bytes back out (and backs the handler's s.admin.Detail read-back).
type memPostWriter struct {
	mu     sync.Mutex
	nextID int64
	store  map[int64]domain.Post
}

func newMemPostWriter() *memPostWriter {
	return &memPostWriter{nextID: 1, store: map[int64]domain.Post{}}
}

func (m *memPostWriter) ByID(_ context.Context, id int64) (domain.Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.store[id]
	if !ok {
		return domain.Post{}, domain.ErrNotFound
	}
	return p, nil
}

func (m *memPostWriter) Create(_ context.Context, p domain.Post) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	p.ID = id
	m.store[id] = p
	return id, nil
}

func (m *memPostWriter) Update(_ context.Context, p domain.Post) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.store[p.ID]; !ok {
		return domain.ErrNotFound
	}
	m.store[p.ID] = p
	return nil
}

func (m *memPostWriter) Delete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, id)
	return nil
}

func (m *memPostWriter) get(id int64) domain.Post {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store[id]
}

// newEndToEndWriteServer wires the real *content.PostWriteService (default
// sanitize.New() policy) as the Server's postWrite over mem, and a fakeAdmin
// whose Detail reads back out of the same store, so the create/update handlers
// run their full read-after-write path against the sanitized record.
func newEndToEndWriteServer(mem *memPostWriter) *Server {
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return mem.ByID(context.Background(), id) }}
	pw := content.NewPostWriteService(mem)
	return testWriteServer(a, pw, nil, nil, nil)
}

// authorCtx carries an author principal (RoleAuthor): holds edit_posts /
// publish_posts but NOT unfiltered_html, so it is a tier-B writer. UserID 1
// matches the posts it authors below so the ownership capability checks pass.
func authorCtx() context.Context {
	p := auth.NewPrincipal(1, "author", []string{auth.RoleAuthor})
	sess := domain.Session{ID: "s1", UserID: 1, CSRFToken: "csrf-token-123"}
	return withAuth(context.Background(), p, sess)
}

// TestAdminPostCreateSanitizesContentExcerptAndTitleEndToEnd is the Req 9.5
// proof for the admin create transport: an author (tier B) POSTs content and
// excerpt carrying a disallowed <script> and an allow-listed <em>, and a title
// carrying markup. The STORED record (read back from the writer) must have the
// <script> stripped and the <em> surviving in content/excerpt, and the title
// reduced to plain text.
func TestAdminPostCreateSanitizesContentExcerptAndTitleEndToEnd(t *testing.T) {
	mem := newMemPostWriter()
	s := newEndToEndWriteServer(mem)
	body := `{"title":"<em>Hello</em>","content":"<em>ok</em><script>alert(1)</script>","excerpt":"<em>ex</em><script>bad()</script>","status":"publish","date":"` +
		time.Now().Add(-time.Hour).UTC().Format(adminDateLayout) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(body)).WithContext(authorCtx())
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	stored := mem.get(1)
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

// TestAdminPostUpdateSanitizesContentExcerptAndTitleEndToEnd is the same proof
// for the admin update transport. A pre-existing stored post is updated by its
// author; the sanitized new values must land in the stored record (and the
// title reduced to plain text).
func TestAdminPostUpdateSanitizesContentExcerptAndTitleEndToEnd(t *testing.T) {
	mem := newMemPostWriter()
	modified := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mem.store[7] = domain.Post{
		ID: 7, Author: 1, Type: "post", Status: "publish",
		Title: "Old", Content: "<p>old</p>", Excerpt: "old", Modified: modified,
		Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	mem.nextID = 8
	s := newEndToEndWriteServer(mem)
	body := `{"title":"<em>New</em>","content":"<em>fresh</em><script>alert(1)</script>","excerpt":"<em>sum</em><script>x()</script>","status":"publish","modified":"2024-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/7", strings.NewReader(body)).WithContext(authorCtx())
	req = withURLParam(req, "id", "7")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	stored := mem.get(7)
	if strings.Contains(stored.Content, "<script") || strings.Contains(stored.Content, "alert(1)") {
		t.Errorf("tier-B sanitize did not run on update content; content = %q", stored.Content)
	}
	if !strings.Contains(stored.Content, "<em>fresh</em>") {
		t.Errorf("allow-listed <em> did not survive on update content; content = %q", stored.Content)
	}
	if strings.Contains(stored.Excerpt, "<script") || strings.Contains(stored.Excerpt, "x()") {
		t.Errorf("tier-B sanitize did not run on update excerpt; excerpt = %q", stored.Excerpt)
	}
	if stored.Title != "New" {
		t.Errorf("title not reduced to plain text on update; title = %q, want %q", stored.Title, "New")
	}
}

// TestAdminPostCreateEmptySanitizedTitleMapsErrTitleEmptyTo400 pins the
// ErrTitleEmpty -> existing-missing-title-400 contract for the admin create
// transport (Req 4.2/4.4). The raw title "<em></em>" is NON-empty, so it passes
// parsePostWrite's own `body.Title == ""` pre-check (which only fires for a
// literally empty title) and reaches PostWriteService.Create, where the
// plain-text title path reduces it to "" and the service returns
// content.ErrTitleEmpty. The handler must map that onto the SAME 400 the admin
// API already returns for a missing title (badRequestError -> "bad_request"),
// not a 500. A non-draft status ("publish") is required so the empty-title rule
// applies at all (a draft title is optional).
func TestAdminPostCreateEmptySanitizedTitleMapsErrTitleEmptyTo400(t *testing.T) {
	mem := newMemPostWriter()
	s := newEndToEndWriteServer(mem)
	body := `{"title":"<em></em>","content":"body","status":"publish","date":"` +
		time.Now().Add(-time.Hour).UTC().Format(adminDateLayout) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts", strings.NewReader(body)).WithContext(authorCtx())
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (ErrTitleEmpty maps to the existing missing-title 400), body=%s", rec.Code, rec.Body.String())
	}
	assertJSONError(t, rec, "bad_request")
	if _, ok := mem.store[1]; ok {
		t.Errorf("a title that sanitizes to empty must not persist a post; stored = %+v", mem.store[1])
	}
}
