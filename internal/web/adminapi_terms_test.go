package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

func testTermServer(tw termAdminService) *Server {
	return &Server{log: slog.Default(), termWrite: tw}
}

func TestAdminTermsListRequiresTaxonomy(t *testing.T) {
	s := testTermServer(&fakeTermWrite{})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/terms", nil).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTerms).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertJSONError(t, rec, "bad_request")
}

func TestAdminTermsListRejectsUnknownTaxonomy(t *testing.T) {
	s := testTermServer(&fakeTermWrite{})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/terms?taxonomy=nav_menu", nil).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTerms).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertJSONError(t, rec, "bad_request")
}

func TestAdminTermsListReturnsItems(t *testing.T) {
	tw := &fakeTermWrite{listByTax: func(taxonomy string) ([]domain.Term, error) {
		if taxonomy != "category" {
			t.Fatalf("taxonomy = %q, want category", taxonomy)
		}
		return []domain.Term{{ID: 1, Name: "News", Slug: "news"}, {ID: 2, Name: "Sports", Slug: "sports"}}, nil
	}}
	s := testTermServer(tw)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/terms?taxonomy=category", nil).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTerms).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 || resp.Items[0].Name != "News" {
		t.Errorf("items = %+v", resp.Items)
	}
}

func TestAdminTermCreateHappyPath(t *testing.T) {
	tw := &fakeTermWrite{create: func(domain.Term) (int64, error) { return 9, nil }}
	s := testTermServer(tw)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/terms", strings.NewReader(`{"name":"News","slug":"news","taxonomy":"category"}`)).
		WithContext(principalCtx("manage_categories"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTermCreate).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["id"].(float64) != 9 || resp["name"] != "News" || resp["taxonomy"] != "category" {
		t.Errorf("resp = %v", resp)
	}
	if tw.lastCreate.Taxonomy != "category" || tw.lastCreate.Slug != "news" {
		t.Errorf("service got = %+v", tw.lastCreate)
	}
}

func TestAdminTermCreateRequiresFields(t *testing.T) {
	cases := []string{
		`{"slug":"news","taxonomy":"category"}`,
		`{"name":"News","taxonomy":"category"}`,
		`{"name":"News","slug":"news"}`,
	}
	for _, body := range cases {
		s := testTermServer(&fakeTermWrite{})
		req := httptest.NewRequest(http.MethodPost, "/admin/api/terms", strings.NewReader(body)).WithContext(principalCtx("manage_categories"))
		rec := httptest.NewRecorder()
		s.jsonHandler(s.adminTermCreate).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestAdminTermUpdateHappyPath(t *testing.T) {
	tw := &fakeTermWrite{}
	s := testTermServer(tw)
	req := httptest.NewRequest(http.MethodPut, "/admin/api/terms/4", strings.NewReader(`{"name":"Renamed","slug":"renamed"}`)).
		WithContext(principalCtx("manage_categories"))
	req = withURLParam(req, "id", "4")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTermUpdate).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if tw.lastUpdate.ID != 4 || tw.lastUpdate.Name != "Renamed" || tw.lastUpdate.Slug != "renamed" {
		t.Errorf("service got = %+v", tw.lastUpdate)
	}
}

func TestAdminTermUpdateNotFound(t *testing.T) {
	tw := &fakeTermWrite{update: func(domain.Term) error { return domain.ErrNotFound }}
	s := testTermServer(tw)
	req := httptest.NewRequest(http.MethodPut, "/admin/api/terms/999", strings.NewReader(`{"name":"x","slug":"x"}`)).
		WithContext(principalCtx("manage_categories"))
	req = withURLParam(req, "id", "999")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTermUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
	assertJSONError(t, rec, "not_found")
}

func TestAdminTermUpdateRequiresFields(t *testing.T) {
	s := testTermServer(&fakeTermWrite{})
	req := httptest.NewRequest(http.MethodPut, "/admin/api/terms/4", strings.NewReader(`{"name":""}`)).
		WithContext(principalCtx("manage_categories"))
	req = withURLParam(req, "id", "4")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTermUpdate).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAdminTermDeleteHappyPath(t *testing.T) {
	tw := &fakeTermWrite{}
	s := testTermServer(tw)
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/terms/4", nil).WithContext(principalCtx("manage_categories"))
	req = withURLParam(req, "id", "4")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTermDelete).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if tw.lastDeleteID != 4 {
		t.Errorf("delete called with id=%d, want 4", tw.lastDeleteID)
	}
}

func TestAdminTermDeleteNotFound(t *testing.T) {
	tw := &fakeTermWrite{del: func(int64) error { return domain.ErrNotFound }}
	s := testTermServer(tw)
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/terms/999", nil).WithContext(principalCtx("manage_categories"))
	req = withURLParam(req, "id", "999")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTermDelete).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAdminTermDeleteBadID(t *testing.T) {
	s := testTermServer(&fakeTermWrite{})
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/terms/abc", nil).WithContext(principalCtx("manage_categories"))
	req = withURLParam(req, "id", "abc")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminTermDelete).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestAdminPostsWriteRoutesRequireCSRF proves the posts-write route group
// registered in adminroutes.go is actually wrapped by csrfJSONMiddleware
// (not just unit-tested at the handler level): a POST /posts without the
// X-CSRF-Token header must be rejected with a 403 JSON envelope even though
// the caller has edit_posts.
func TestAdminPostsWriteRoutesRequireCSRF(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.postWrite = &fakePostWrite{}
	srv.auth = fakeSessions{p: auth.NewPrincipal(1, "author", []string{auth.RoleAuthor, auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	r := srv.SessionMiddleware(srv.adminAPIRouter())

	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"title":"x","status":"draft"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] == "" {
		t.Fatalf("missing error.code in envelope: %s", rec.Body.String())
	}
}

// TestAdminTermsWriteRoutesRequireCSRF proves the terms-write route group is
// gated by both manage_categories and csrfJSONMiddleware end-to-end.
func TestAdminTermsWriteRoutesRequireCSRF(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.termWrite = &fakeTermWrite{}
	srv.auth = fakeSessions{p: auth.NewPrincipal(1, "ed", []string{auth.RoleEditor, auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	r := srv.SessionMiddleware(srv.adminAPIRouter())

	req := httptest.NewRequest(http.MethodPost, "/terms", strings.NewReader(`{"name":"News","slug":"news","taxonomy":"category"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] == "" {
		t.Fatalf("missing error.code in envelope: %s", rec.Body.String())
	}
}

// TestAdminTermsWriteRoutesRequireCapability proves a caller without
// manage_categories is rejected before ever reaching adminTermCreate, even
// with a valid CSRF token.
func TestAdminTermsWriteRoutesRequireCapability(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.termWrite = &fakeTermWrite{}
	srv.auth = fakeSessions{p: auth.NewPrincipal(1, "author", []string{auth.RoleAuthor}), s: domain.Session{CSRFToken: "token"}}
	r := srv.SessionMiddleware(srv.adminAPIRouter())

	req := httptest.NewRequest(http.MethodPost, "/terms", strings.NewReader(`{"name":"News","slug":"news","taxonomy":"category"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "token")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminAPIRouterOmitsWriteRoutesWhenDepsNil proves adminAPIRouter itself
// gates the M6 write-route groups on their dependency being non-nil, rather
// than registering them unconditionally: an embedder that calls WithAdmin
// without WithAdminWrites (postWrite/termWrite left nil) must get a plain 404
// on the write paths, not a nil-dereference panic recovered to 500.
func TestAdminAPIRouterOmitsWriteRoutesWhenDepsNil(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.admin = &fakeAdmin{list: func(int, int, content.AdminListFilter) (content.AdminList, error) {
		return content.AdminList{}, nil
	}}
	srv.auth = fakeSessions{p: auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	r := srv.SessionMiddleware(srv.adminAPIRouter())

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodPut, "/posts/1", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodDelete, "/posts/1", nil),
		httptest.NewRequest(http.MethodPost, "/terms", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodPut, "/terms/1", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodDelete, "/terms/1", nil),
	} {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", "token")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		// Since the read-only GET routes for /posts and /terms remain
		// registered even when postWrite/termWrite are nil, chi reports an
		// unmatched method on a known path as 405 rather than 404; either
		// way the important thing is that it's a clean HTTP error, not a
		// nil-pointer panic recovered to 500.
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status=%d, want 404 or 405, body=%s", req.Method, req.URL.Path, rec.Code, rec.Body.String())
		}
	}

	// A read-only route that does not depend on postWrite/termWrite (GET
	// /posts, backed by s.admin) must remain registered and unaffected.
	getReq := httptest.NewRequest(http.MethodGet, "/posts", nil)
	getReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code == http.StatusNotFound {
		t.Errorf("GET /posts unexpectedly 404 when postWrite/termWrite are nil (read route should remain registered)")
	}
}
