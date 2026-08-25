package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAdminPostUpdateNotFound(t *testing.T) {
	pw := &fakePostWrite{update: func(domain.Post, time.Time) error { return domain.ErrNotFound }}
	a := &fakeAdmin{detail: func(int64) (domain.Post, error) { return domain.Post{}, domain.ErrNotFound }}
	s := testWriteServer(a, pw, nil, nil, nil)
	body := `{"title":"x","status":"draft","modified":"2020-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/posts/999", strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "999")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
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

func TestAdminPostDeleteNotFound(t *testing.T) {
	pw := &fakePostWrite{del: func(domain.Post) error { return domain.ErrNotFound }}
	s := testWriteServer(&fakeAdmin{}, pw, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/posts/999", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "999")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostDelete).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
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
