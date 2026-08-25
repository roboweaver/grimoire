package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// withRevisionURLParams attaches both the {id} (parent post) and
// {revisionId} chi route params to a single request context. withURLParam
// alone can't be chained twice for this: each call wraps a brand-new
// chi.RouteContext around the request, so a second call's params shadow the
// first's rather than accumulating.
func withRevisionURLParams(r *http.Request, id, revisionID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	rctx.URLParams.Add("revisionId", revisionID)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// fakeRevisionAdmin implements revisionAdminService for the revision-route
// handler tests.
type fakeRevisionAdmin struct {
	list    func(parentID int64) ([]domain.RevisionMeta, error)
	get     func(parentID, revisionID int64) (domain.Post, error)
	restore func(parentID, revisionID int64) (domain.Post, error)
}

func (f *fakeRevisionAdmin) List(_ context.Context, _ auth.Principal, parentID int64) ([]domain.RevisionMeta, error) {
	return f.list(parentID)
}

func (f *fakeRevisionAdmin) Get(_ context.Context, _ auth.Principal, parentID, revisionID int64) (domain.Post, error) {
	return f.get(parentID, revisionID)
}

func (f *fakeRevisionAdmin) Restore(_ context.Context, _ auth.Principal, parentID, revisionID int64) (domain.Post, error) {
	return f.restore(parentID, revisionID)
}

// testRevisionServer builds a Server wired for the revision-route handler
// unit tests: admin (read detail, used by restore's response) plus the
// revisionAdminService fake under test.
func testRevisionServer(a adminReader, ra revisionAdminService) *Server {
	s := testWriteServer(a, nil, nil, nil, nil)
	s.revisions = ra
	return s
}

func TestAdminRevisionListReturnsNewestFirstSummaries(t *testing.T) {
	newest := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	oldest := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	ra := &fakeRevisionAdmin{list: func(int64) ([]domain.RevisionMeta, error) {
		return []domain.RevisionMeta{
			{ID: 2, ParentID: 7, Author: 1, Modified: newest},
			{ID: 1, ParentID: 7, Author: 1, Modified: oldest},
		}, nil
	}}
	s := testRevisionServer(&fakeAdmin{}, ra)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7/revisions", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "7")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminRevisionList).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if len(resp) != 2 {
		t.Fatalf("len(resp) = %d, want 2: %v", len(resp), resp)
	}
	if resp[0]["id"].(float64) != 2 || resp[1]["id"].(float64) != 1 {
		t.Errorf("resp not newest-first: %v", resp)
	}
	for _, item := range resp {
		if _, ok := item["content"]; ok {
			t.Errorf("summary list must not include content body: %v", item)
		}
	}
}

func TestAdminRevisionListNotFoundForNonexistentPost(t *testing.T) {
	ra := &fakeRevisionAdmin{list: func(int64) ([]domain.RevisionMeta, error) { return nil, domain.ErrNotFound }}
	s := testRevisionServer(&fakeAdmin{}, ra)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/999/revisions", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "999")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminRevisionList).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminRevisionListNotFoundForUnauthorizedCaller confirms the
// unauthorized case yields the exact same 404 shape as the nonexistent-post
// case above (Req 2.5): the service layer (already tested in
// internal/content) collapses both into domain.ErrNotFound, and this proves
// the HTTP layer doesn't reintroduce a distinguishing status/body.
func TestAdminRevisionListNotFoundForUnauthorizedCaller(t *testing.T) {
	ra := &fakeRevisionAdmin{list: func(int64) ([]domain.RevisionMeta, error) { return nil, domain.ErrNotFound }}
	s := testRevisionServer(&fakeAdmin{}, ra)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7/revisions", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "7")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminRevisionList).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminRevisionGetReturnsFullDetail(t *testing.T) {
	modified := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	ra := &fakeRevisionAdmin{get: func(int64, int64) (domain.Post, error) {
		return domain.Post{ID: 5, Title: "Old title", Content: "Old content", Excerpt: "Old excerpt", Modified: modified}, nil
	}}
	s := testRevisionServer(&fakeAdmin{}, ra)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7/revisions/5", nil).WithContext(principalCtx("edit_posts"))
	req = withRevisionURLParams(req, "7", "5")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminRevisionGet).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["title"] != "Old title" || resp["content"] != "Old content" || resp["excerpt"] != "Old excerpt" {
		t.Errorf("resp = %v", resp)
	}
}

func TestAdminRevisionGetNotFoundMatrix(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"different parent", domain.ErrNotFound},
		{"nonexistent", domain.ErrNotFound},
		{"unauthorized", domain.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ra := &fakeRevisionAdmin{get: func(int64, int64) (domain.Post, error) { return domain.Post{}, tc.err }}
			s := testRevisionServer(&fakeAdmin{}, ra)
			req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7/revisions/5", nil).WithContext(principalCtx("edit_posts"))
			req = withRevisionURLParams(req, "7", "5")
			rec := httptest.NewRecorder()
			s.jsonHandler(s.adminRevisionGet).ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminRevisionRestoreReturnsPostDetail(t *testing.T) {
	restored := domain.Post{ID: 7, Title: "Restored", Content: "Restored body", Excerpt: "Restored excerpt", Status: "publish", Type: "post"}
	ra := &fakeRevisionAdmin{restore: func(int64, int64) (domain.Post, error) { return restored, nil }}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return restored, nil }}
	s := testRevisionServer(a, ra)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts/7/revisions/5/restore", nil).WithContext(principalCtx("edit_posts"))
	req = withRevisionURLParams(req, "7", "5")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminRevisionRestore).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["title"] != "Restored" || resp["content"] != "Restored body" {
		t.Errorf("resp = %v, want the restored post's detail shape", resp)
	}
}

func TestAdminRevisionRestoreNotFoundMatrix(t *testing.T) {
	ra := &fakeRevisionAdmin{restore: func(int64, int64) (domain.Post, error) { return domain.Post{}, domain.ErrNotFound }}
	s := testRevisionServer(&fakeAdmin{}, ra)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts/7/revisions/5/restore", nil).WithContext(principalCtx("edit_posts"))
	req = withRevisionURLParams(req, "7", "5")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminRevisionRestore).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminRevisionRestoreListGrowsByOne is an end-to-end proof (through the
// full adminAPIRouter, not the bare handler) that restoring a revision
// snapshots the pre-restore state: a follow-up GET .../revisions shows one
// additional row beyond what existed before the restore call.
func TestAdminRevisionRestoreListGrowsByOne(t *testing.T) {
	restored := domain.Post{ID: 7, Title: "Restored", Content: "Restored body", Status: "publish", Type: "post"}
	before := []domain.RevisionMeta{{ID: 1, ParentID: 7, Author: 1, Modified: time.Now()}}
	after := []domain.RevisionMeta{
		{ID: 2, ParentID: 7, Author: 1, Modified: time.Now()},
		{ID: 1, ParentID: 7, Author: 1, Modified: time.Now()},
	}
	restoreCalled := false
	ra := &fakeRevisionAdmin{
		list: func(int64) ([]domain.RevisionMeta, error) {
			if restoreCalled {
				return after, nil
			}
			return before, nil
		},
		restore: func(int64, int64) (domain.Post, error) {
			restoreCalled = true
			return restored, nil
		},
	}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return restored, nil }}
	s := testRevisionServer(a, ra)
	s.auth = fakeSessions{p: auth.NewPrincipal(1, "author", []string{auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	r := s.SessionMiddleware(s.adminAPIRouter())

	listReq := httptest.NewRequest(http.MethodGet, "/posts/7/revisions", nil)
	listReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	var beforeList []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &beforeList); err != nil {
		t.Fatalf("unmarshal before-list: %v (%s)", err, listRec.Body.String())
	}

	restoreReq := httptest.NewRequest(http.MethodPost, "/posts/7/revisions/1/restore", nil)
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreReq.Header.Set("X-CSRF-Token", "token")
	restoreReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	restoreRec := httptest.NewRecorder()
	r.ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want 200, body=%s", restoreRec.Code, restoreRec.Body.String())
	}

	listReq2 := httptest.NewRequest(http.MethodGet, "/posts/7/revisions", nil)
	listReq2.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	listRec2 := httptest.NewRecorder()
	r.ServeHTTP(listRec2, listReq2)
	var afterList []map[string]any
	if err := json.Unmarshal(listRec2.Body.Bytes(), &afterList); err != nil {
		t.Fatalf("unmarshal after-list: %v (%s)", err, listRec2.Body.String())
	}
	if len(afterList) != len(beforeList)+1 {
		t.Fatalf("after-list len = %d, want %d (before+1)", len(afterList), len(beforeList)+1)
	}
}

// TestAdminRevisionRestoreRequiresCSRF proves the restore route is nested
// under csrfJSONMiddleware: a POST without X-CSRF-Token is rejected with 403
// even though the caller has edit_posts.
func TestAdminRevisionRestoreRequiresCSRF(t *testing.T) {
	ra := &fakeRevisionAdmin{restore: func(int64, int64) (domain.Post, error) {
		return domain.Post{ID: 7}, nil
	}}
	a := &fakeAdmin{detail: func(id int64) (domain.Post, error) { return domain.Post{ID: id}, nil }}
	s := testRevisionServer(a, ra)
	s.auth = fakeSessions{p: auth.NewPrincipal(1, "author", []string{auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	r := s.SessionMiddleware(s.adminAPIRouter())

	req := httptest.NewRequest(http.MethodPost, "/posts/7/revisions/1/restore", strings.NewReader(``))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}
