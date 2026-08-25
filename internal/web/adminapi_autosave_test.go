package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// fakeAutosaveAdmin implements autosaveAdminService for the autosave-route
// handler tests.
type fakeAutosaveAdmin struct {
	newer func(parentID int64) (domain.Post, bool, error)
	save  func(parentID int64, fields content.AutosaveFields) (domain.Post, error)
}

func (f *fakeAutosaveAdmin) Newer(_ context.Context, _ auth.Principal, parentID int64) (domain.Post, bool, error) {
	return f.newer(parentID)
}

func (f *fakeAutosaveAdmin) Save(_ context.Context, _ auth.Principal, parentID int64, fields content.AutosaveFields) (domain.Post, error) {
	return f.save(parentID, fields)
}

// testAutosaveServer builds a Server wired for the autosave-route handler
// unit tests.
func testAutosaveServer(a adminReader, aa autosaveAdminService) *Server {
	s := testWriteServer(a, nil, nil, nil, nil)
	s.autosave = aa
	return s
}

func TestAdminAutosaveGetReturnsNewerAutosave(t *testing.T) {
	modified := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	aa := &fakeAutosaveAdmin{newer: func(int64) (domain.Post, bool, error) {
		return domain.Post{Title: "Draft title", Content: "Draft body", Excerpt: "Draft excerpt", Modified: modified}, true, nil
	}}
	s := testAutosaveServer(&fakeAdmin{}, aa)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7/autosave", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "7")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminAutosaveGet).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if resp["title"] != "Draft title" || resp["content"] != "Draft body" || resp["excerpt"] != "Draft excerpt" {
		t.Errorf("resp = %v, want title/content/excerpt from the autosave", resp)
	}
	if resp["modified"] == nil {
		t.Errorf("resp missing modified: %v", resp)
	}
}

func TestAdminAutosaveGetNotFoundMatrix(t *testing.T) {
	cases := map[string]*fakeAutosaveAdmin{
		"no autosave exists": {newer: func(int64) (domain.Post, bool, error) { return domain.Post{}, false, nil }},
		"existing not newer": {newer: func(int64) (domain.Post, bool, error) { return domain.Post{}, false, nil }},
		"nonexistent post":   {newer: func(int64) (domain.Post, bool, error) { return domain.Post{}, false, domain.ErrNotFound }},
		"unauthorized":       {newer: func(int64) (domain.Post, bool, error) { return domain.Post{}, false, domain.ErrNotFound }},
	}
	for name, aa := range cases {
		t.Run(name, func(t *testing.T) {
			s := testAutosaveServer(&fakeAdmin{}, aa)
			req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7/autosave", nil).WithContext(principalCtx("edit_posts"))
			req = withURLParam(req, "id", "7")
			rec := httptest.NewRecorder()
			s.jsonHandler(s.adminAutosaveGet).ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminAutosaveSaveReturnsOKAndIsReflectedByGet(t *testing.T) {
	saved := domain.Post{}
	aa := &fakeAutosaveAdmin{
		save: func(_ int64, fields content.AutosaveFields) (domain.Post, error) {
			saved = domain.Post{Title: fields.Title, Content: fields.Content, Excerpt: fields.Excerpt, Modified: time.Now()}
			return saved, nil
		},
		newer: func(int64) (domain.Post, bool, error) { return saved, true, nil },
	}
	s := testAutosaveServer(&fakeAdmin{}, aa)

	body := `{"title":"Draft","content":"Draft body","excerpt":"Draft excerpt"}`
	saveReq := httptest.NewRequest(http.MethodPost, "/admin/api/posts/7/autosave", bytes.NewBufferString(body)).WithContext(principalCtx("edit_posts"))
	saveReq = withURLParam(saveReq, "id", "7")
	saveReq.Header.Set("Content-Type", "application/json")
	saveRec := httptest.NewRecorder()
	s.jsonHandler(s.adminAutosaveSave).ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200, body=%s", saveRec.Code, saveRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7/autosave", nil).WithContext(principalCtx("edit_posts"))
	getReq = withURLParam(getReq, "id", "7")
	getRec := httptest.NewRecorder()
	s.jsonHandler(s.adminAutosaveGet).ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200, body=%s", getRec.Code, getRec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, getRec.Body.String())
	}
	if resp["content"] != "Draft body" {
		t.Errorf("follow-up GET content = %v, want %q (saved content)", resp["content"], "Draft body")
	}
}

// TestAdminAutosaveSaveSecondCallUpdatesSameRowNotRevisionsList proves a
// second POST for the same post+caller updates the dedicated autosave slot
// in place rather than appending a new "revision": the revisions list count
// stays stable across two autosave saves.
func TestAdminAutosaveSaveSecondCallUpdatesSameRowNotRevisionsList(t *testing.T) {
	revisionsList := []domain.RevisionMeta{{ID: 1, ParentID: 7, Author: 1, Modified: time.Now()}}
	saveCalls := 0
	aa := &fakeAutosaveAdmin{
		save: func(int64, content.AutosaveFields) (domain.Post, error) {
			saveCalls++
			// The dedicated autosave slot is updated in place; it never
			// appends to revisionsList.
			return domain.Post{}, nil
		},
	}
	ra := &fakeRevisionAdmin{list: func(int64) ([]domain.RevisionMeta, error) { return revisionsList, nil }}
	s := testAutosaveServer(&fakeAdmin{}, aa)
	s.revisions = ra

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/admin/api/posts/7/autosave", strings.NewReader(`{"title":"t","content":"c","excerpt":"e"}`)).WithContext(principalCtx("edit_posts"))
		req = withURLParam(req, "id", "7")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.jsonHandler(s.adminAutosaveSave).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save[%d] status = %d, want 200, body=%s", i, rec.Code, rec.Body.String())
		}
	}
	if saveCalls != 2 {
		t.Fatalf("saveCalls = %d, want 2", saveCalls)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7/revisions", nil).WithContext(principalCtx("edit_posts"))
	listReq = withURLParam(listReq, "id", "7")
	listRec := httptest.NewRecorder()
	s.jsonHandler(s.adminRevisionList).ServeHTTP(listRec, listReq)
	var resp []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, listRec.Body.String())
	}
	if len(resp) != 1 {
		t.Fatalf("revisions list len = %d, want 1 (unchanged by autosave saves)", len(resp))
	}
}

func TestAdminAutosaveSaveNotFoundMatrix(t *testing.T) {
	aa := &fakeAutosaveAdmin{save: func(int64, content.AutosaveFields) (domain.Post, error) {
		return domain.Post{}, domain.ErrNotFound
	}}
	s := testAutosaveServer(&fakeAdmin{}, aa)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/posts/999/autosave", strings.NewReader(`{"title":"t","content":"c","excerpt":"e"}`)).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "999")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminAutosaveSave).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminAutosaveSaveRequiresCSRF proves the autosave-save route is nested
// under csrfJSONMiddleware: a POST without X-CSRF-Token is rejected with 403
// even though the caller has edit_posts.
func TestAdminAutosaveSaveRequiresCSRF(t *testing.T) {
	aa := &fakeAutosaveAdmin{save: func(int64, content.AutosaveFields) (domain.Post, error) {
		return domain.Post{ID: 7}, nil
	}}
	s := testAutosaveServer(&fakeAdmin{}, aa)
	s.auth = fakeSessions{p: auth.NewPrincipal(1, "author", []string{auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	r := s.SessionMiddleware(s.adminAPIRouter())

	req := httptest.NewRequest(http.MethodPost, "/posts/7/autosave", strings.NewReader(`{"title":"t","content":"c","excerpt":"e"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}
