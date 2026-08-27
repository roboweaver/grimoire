package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"

	"github.com/go-chi/chi/v5"
)

// withURLParam attaches a chi route param to the request so handlers that call
// chi.URLParam resolve it without a full router mount.
func withURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// fakeAdmin implements adminReader for the JSON API tests.
type fakeAdmin struct {
	list        func(page, perPage int, f content.AdminListFilter) (content.AdminList, error)
	detail      func(id int64) (domain.Post, error)
	stats       func() (content.Stats, error)
	displayName func(userID int64) (string, error)
	authors     func() ([]domain.AuthorOption, error)
}

func (f *fakeAdmin) List(_ context.Context, page, perPage int, filter content.AdminListFilter) (content.AdminList, error) {
	return f.list(page, perPage, filter)
}
func (f *fakeAdmin) Detail(_ context.Context, id int64) (domain.Post, error) { return f.detail(id) }
func (f *fakeAdmin) Stats(_ context.Context) (content.Stats, error)          { return f.stats() }
func (f *fakeAdmin) DisplayName(_ context.Context, id int64) (string, error) {
	return f.displayName(id)
}
func (f *fakeAdmin) Authors(_ context.Context) ([]domain.AuthorOption, error) {
	return f.authors()
}

func testAdminServer(a adminReader) *Server {
	return &Server{log: slog.Default(), admin: a}
}

// principalCtx builds a request context carrying an authenticated principal and
// session, as SessionMiddleware would.
func principalCtx(caps ...string) context.Context {
	capSet := make(map[string]bool, len(caps))
	for _, c := range caps {
		capSet[c] = true
	}
	p := auth.Principal{UserID: 1, Login: "admin", Roles: []string{"administrator"}, Caps: capSet}
	sess := domain.Session{ID: "abc", UserID: 1, CSRFToken: "csrf-token-123"}
	return withAuth(context.Background(), p, sess)
}

func TestAdminSessionReturnsIdentityAndCSRF(t *testing.T) {
	a := &fakeAdmin{
		displayName: func(id int64) (string, error) { return "Ada Admin", nil },
	}
	s := testAdminServer(a)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/session", nil).
		WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminSession).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["login"] != "admin" || body["displayName"] != "Ada Admin" {
		t.Errorf("body = %v", body)
	}
	if body["csrfToken"] != "csrf-token-123" {
		t.Errorf("csrfToken = %v, want csrf-token-123", body["csrfToken"])
	}
	if _, ok := body["capabilities"].([]any); !ok {
		t.Errorf("capabilities missing/!array: %v", body["capabilities"])
	}
	// No password/pass field must ever leak.
	if _, bad := body["pass"]; bad {
		t.Errorf("session body leaked pass field")
	}
}

func TestAdminStatsShape(t *testing.T) {
	a := &fakeAdmin{
		stats: func() (content.Stats, error) {
			return content.Stats{PostsPublished: 3, PostsDraft: 1, Pages: 2, Categories: 5, Users: 4}, nil
		},
	}
	s := testAdminServer(a)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminStats).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Posts struct {
			Published int `json:"published"`
			Draft     int `json:"draft"`
		} `json:"posts"`
		Pages      int `json:"pages"`
		Categories int `json:"categories"`
		Users      int `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Posts.Published != 3 || body.Posts.Draft != 1 || body.Pages != 2 ||
		body.Categories != 5 || body.Users != 4 {
		t.Errorf("stats body = %+v", body)
	}
}

func TestAdminPostsPagination(t *testing.T) {
	a := &fakeAdmin{
		list: func(page, perPage int, f content.AdminListFilter) (content.AdminList, error) {
			if f.Type != "post" {
				t.Fatalf("type = %q, want post", f.Type)
			}
			if f.Status != "publish" {
				t.Fatalf("status = %q, want publish", f.Status)
			}
			return content.AdminList{
				Items:      []domain.Post{{ID: 3, Title: "Third", Slug: "third", Type: "post", Status: "publish", Author: 1}},
				Page:       page,
				PerPage:    perPage,
				Total:      25,
				TotalPages: 3,
			}, nil
		},
	}
	s := testAdminServer(a)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?page=2&perPage=10&type=post&status=publish", nil).
		WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPosts).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Author int64  `json:"author"`
		} `json:"items"`
		Page       int `json:"page"`
		PerPage    int `json:"perPage"`
		Total      int `json:"total"`
		TotalPages int `json:"totalPages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Total != 25 || body.TotalPages != 3 || body.Page != 2 || body.PerPage != 10 {
		t.Errorf("pagination meta = %+v", body)
	}
	if len(body.Items) != 1 || body.Items[0].ID != 3 || body.Items[0].Title != "Third" {
		t.Errorf("items = %+v", body.Items)
	}
}

func TestAdminPostsSearchQueryParamForwarded(t *testing.T) {
	var gotFilter content.AdminListFilter
	admin := &fakeAdmin{
		list: func(_, _ int, f content.AdminListFilter) (content.AdminList, error) {
			gotFilter = f
			return content.AdminList{Page: 1, PerPage: 10, Total: 1, TotalPages: 1}, nil
		},
	}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?search=hello", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if gotFilter.Search != "hello" {
		t.Fatalf("adminReader.List got Search=%q, want %q", gotFilter.Search, "hello")
	}
}

func TestAdminPostsMissingFilterParamsReturnAll(t *testing.T) {
	var gotFilter content.AdminListFilter
	admin := &fakeAdmin{
		list: func(_, _ int, f content.AdminListFilter) (content.AdminList, error) {
			gotFilter = f
			return content.AdminList{Page: 1, PerPage: 10, Total: 3, TotalPages: 1}, nil
		},
	}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotFilter != (content.AdminListFilter{}) {
		t.Fatalf("no query params should mean zero-value filter, got %+v", gotFilter)
	}
	var body postsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 3 {
		t.Fatalf("Total = %d, want 3 (unfiltered)", body.Total)
	}
}

func TestAdminPostsInvalidStatusReturns400(t *testing.T) {
	admin := &fakeAdmin{list: func(_, _ int, _ content.AdminListFilter) (content.AdminList, error) {
		t.Fatal("List should not be called for an invalid status")
		return content.AdminList{}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?status=bogus", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("expected non-empty error code/message, got %+v", body.Error)
	}
}

func TestAdminPostsMissingStatusReturns200(t *testing.T) {
	admin := &fakeAdmin{list: func(_, _ int, _ content.AdminListFilter) (content.AdminList, error) {
		return content.AdminList{Page: 1, PerPage: 10, Total: 3, TotalPages: 1}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Req 4.5: absent status must not 400): %s", rec.Code, rec.Body.String())
	}
}

func TestAdminAuthorsEndpoint(t *testing.T) {
	admin := &fakeAdmin{authors: func() ([]domain.AuthorOption, error) {
		return []domain.AuthorOption{{ID: 1, DisplayName: "Admin"}}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/authors", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminAuthors).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"displayName":"Admin"`) {
		t.Fatalf("body missing displayName: %s", rec.Body.String())
	}
}

func TestAdminPostsInvalidAuthorReturns400(t *testing.T) {
	admin := &fakeAdmin{list: func(_, _ int, _ content.AdminListFilter) (content.AdminList, error) {
		t.Fatal("list should not be called for an invalid author param")
		return content.AdminList{}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?author=not-a-number", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code"`) {
		t.Fatalf("expected {\"error\":{\"code\":...}} envelope, got %s", rec.Body.String())
	}
}

func TestAdminPostsAuthorFilterForwarded(t *testing.T) {
	var gotFilter content.AdminListFilter
	admin := &fakeAdmin{list: func(_, _ int, f content.AdminListFilter) (content.AdminList, error) {
		gotFilter = f
		return content.AdminList{Page: 1, PerPage: 10, Total: 1, TotalPages: 1}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?author=42", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotFilter.Author != 42 {
		t.Fatalf("Author = %d, want 42", gotFilter.Author)
	}
}

func TestAdminPostDetail(t *testing.T) {
	a := &fakeAdmin{
		detail: func(id int64) (domain.Post, error) {
			return domain.Post{ID: id, Title: "Hello", Slug: "hello", Type: "post", Status: "publish", Content: "Body", Excerpt: "Ex"}, nil
		},
	}
	s := testAdminServer(a)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/7", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "7")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPost).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["content"] != "Body" || body["excerpt"] != "Ex" || body["title"] != "Hello" {
		t.Errorf("detail body = %v", body)
	}
}

func TestAdminPostDetailNotFound(t *testing.T) {
	a := &fakeAdmin{
		detail: func(int64) (domain.Post, error) { return domain.Post{}, domain.ErrNotFound },
	}
	s := testAdminServer(a)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/999", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "999")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPost).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	assertJSONError(t, rec, "not_found")
}

func TestAdminPostDetailBadID(t *testing.T) {
	s := testAdminServer(&fakeAdmin{})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/abc", nil).WithContext(principalCtx("edit_posts"))
	req = withURLParam(req, "id", "abc")
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPost).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	assertJSONError(t, rec, "bad_request")
}

func TestRequireLoginJSONUnauthenticated(t *testing.T) {
	s := testAdminServer(&fakeAdmin{})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/admin/api/session", nil) // no auth context
	rec := httptest.NewRecorder()
	s.requireLoginJSON(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q, want json (never a redirect)", ct)
	}
	assertJSONError(t, rec, "unauthorized")
}

func TestRequireCapabilityJSONForbidden(t *testing.T) {
	s := testAdminServer(&fakeAdmin{})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil).
		WithContext(principalCtx()) // authenticated but lacks edit_posts
	rec := httptest.NewRecorder()
	s.requireCapabilityJSON("edit_posts")(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	assertJSONError(t, rec, "forbidden")
}

func TestRequireCapabilityJSONAllows(t *testing.T) {
	s := testAdminServer(&fakeAdmin{})
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats", nil).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.requireCapabilityJSON("edit_posts")(next).ServeHTTP(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("capable request blocked: called=%v status=%d", called, rec.Code)
	}
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q, want json", ct)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", body.Error.Code, wantCode)
	}
	if body.Error.Message == "" {
		t.Errorf("error message empty")
	}
}
