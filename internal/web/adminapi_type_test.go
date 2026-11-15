package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

// M12 Phase 12.1 -- RED tests for admin list `type` validation (design 8d,
// Finding 2; Req 2.8-2.10, 12.2, 12.3; D9).
//
// Wiring: the registry reaches the handler through the Server's existing
// registry field, set by the existing WithRegistry (a nil CustomPostService is
// fine for the admin list path). No new API is assumed: the RED is behavioral.
// Today adminPosts forwards `type` unvalidated, so `shop_order` returns 200 and
// reaches the reader.
//
// The reader is a recording fakeAdmin over seeded rows (post, page, book,
// vault and shop_order). It mimics the AdminService contract: an empty Type
// means {post, page}, a non-empty Type selects exactly that post_type.

var adminTypeSeed = []domain.Post{
	{ID: 1, Title: "A post", Slug: "a-post", Type: "post", Status: "publish", Author: 1},
	{ID: 2, Title: "A page", Slug: "a-page", Type: "page", Status: "publish", Author: 1},
	{ID: 3, Title: "Dune", Slug: "dune", Type: "book", Status: "publish", Author: 1},
	{ID: 4, Title: "Secret", Slug: "secret", Type: "vault", Status: "draft", Author: 1},
	{ID: 5, Title: "Order 5", Slug: "order-5", Type: "shop_order", Status: "private", Author: 1},
}

// typeRecorder wraps fakeAdmin with call recording over adminTypeSeed.
type typeRecorder struct {
	*fakeAdmin
	calls   int
	filters []content.AdminListFilter
}

func newTypeRecorder() *typeRecorder {
	r := &typeRecorder{}
	r.fakeAdmin = &fakeAdmin{
		list: func(page, perPage int, f content.AdminListFilter) (content.AdminList, error) {
			r.calls++
			r.filters = append(r.filters, f)
			var items []domain.Post
			for _, p := range adminTypeSeed {
				switch {
				case f.Type == "" && (p.Type == "post" || p.Type == "page"):
					items = append(items, p)
				case f.Type != "" && p.Type == f.Type:
					items = append(items, p)
				}
			}
			return content.AdminList{Items: items, Page: 1, PerPage: 20, Total: len(items), TotalPages: 1}, nil
		},
	}
	return r
}

// adminTypeRegistry registers a public type `book` and a NON-public type `vault`.
func adminTypeRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	yes, no := true, false
	st, err := routing.Parse("", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	reg, err := registry.Build(config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "book", Public: &yes, Slug: "book"},
			{Name: "vault", Public: &no},
		},
	}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

// getAdminTypeList issues GET /admin/api/posts<query> as an edit_posts principal.
func getAdminTypeList(s *Server, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts"+query, nil).
		WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPosts).ServeHTTP(rec, req)
	return rec
}

// itemTypes decodes the response items and returns each item's type.
func itemTypes(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	out := make([]string, 0, len(body.Items))
	for _, it := range body.Items {
		out = append(out, it.Type)
	}
	return out
}

func assertInvalidType(t *testing.T, rec *httptest.ResponseRecorder, rec2 *typeRecorder) {
	t.Helper()
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
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	if body.Error.Code != "invalid_type" {
		t.Errorf("error code = %q, want invalid_type (body %s)", body.Error.Code, rec.Body.String())
	}
	if rec2.calls != 0 {
		t.Errorf("reader was called %d time(s), want 0: a rejected type must read nothing", rec2.calls)
	}
}

func TestAdminAPITypeRejectsUnregisteredType(t *testing.T) {
	for _, withRegistry := range []bool{true, false} {
		name := "with registry"
		if !withRegistry {
			name = "no registry"
		}
		t.Run(name, func(t *testing.T) {
			rd := newTypeRecorder()
			s := testAdminServer(rd)
			if withRegistry {
				s.WithRegistry(adminTypeRegistry(t), nil)
			}
			rec := getAdminTypeList(s, "?type=shop_order")
			assertInvalidType(t, rec, rd)
			for _, ty := range itemTypes(t, rec) {
				if ty == "shop_order" {
					t.Errorf("shop_order row leaked in response: %s", rec.Body.String())
				}
			}
		})
	}
}

func TestAdminAPITypeBuiltinsStillWork(t *testing.T) {
	for _, withRegistry := range []bool{true, false} {
		for _, ty := range []string{"post", "page"} {
			name := ty + "/no registry"
			if withRegistry {
				name = ty + "/with registry"
			}
			t.Run(name, func(t *testing.T) {
				rd := newTypeRecorder()
				s := testAdminServer(rd)
				if withRegistry {
					s.WithRegistry(adminTypeRegistry(t), nil)
				}
				rec := getAdminTypeList(s, "?type="+ty)
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
				}
				got := itemTypes(t, rec)
				if len(got) != 1 || got[0] != ty {
					t.Errorf("item types = %v, want [%s]", got, ty)
				}
			})
		}
	}
}

func TestAdminAPITypeRegisteredTypeLists(t *testing.T) {
	// book is public, vault is registered with public=false: both are listable.
	for _, ty := range []string{"book", "vault"} {
		t.Run(ty, func(t *testing.T) {
			rd := newTypeRecorder()
			s := testAdminServer(rd).WithRegistry(adminTypeRegistry(t), nil)
			rec := getAdminTypeList(s, "?type="+ty)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			got := itemTypes(t, rec)
			if len(got) != 1 || got[0] != ty {
				t.Errorf("item types = %v, want [%s]", got, ty)
			}
			if rd.calls != 1 || rd.filters[0].Type != ty {
				t.Errorf("reader calls = %d filters = %+v, want one call with Type=%s", rd.calls, rd.filters, ty)
			}
		})
	}
}

func TestAdminAPITypeEmptyOrAbsentKeepsDefault(t *testing.T) {
	for _, query := range []string{"", "?type="} {
		t.Run("query="+query, func(t *testing.T) {
			rd := newTypeRecorder()
			s := testAdminServer(rd).WithRegistry(adminTypeRegistry(t), nil)
			rec := getAdminTypeList(s, query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			got := itemTypes(t, rec)
			if len(got) != 2 {
				t.Fatalf("item types = %v, want exactly post and page", got)
			}
			for _, ty := range got {
				if ty != "post" && ty != "page" {
					t.Errorf("default list contains %q, want post and page only", ty)
				}
			}
			if rd.calls != 1 || rd.filters[0].Type != "" {
				t.Errorf("reader calls = %d filters = %+v, want one call with empty Type", rd.calls, rd.filters)
			}
		})
	}
}

func TestAdminAPITypeNoRegistryRejectsBook(t *testing.T) {
	rd := newTypeRecorder()
	s := testAdminServer(rd) // never called WithRegistry
	assertInvalidType(t, getAdminTypeList(s, "?type=book"), rd)
}

// Auth behavior is unchanged: the middleware, not adminPosts, gates the route.
// An anonymous caller still gets 401 and an under-privileged one 403, and an
// invalid type must not turn either into a 400.
func TestAdminAPITypeAuthUnchanged(t *testing.T) {
	rd := newTypeRecorder()
	s := testAdminServer(rd).WithRegistry(adminTypeRegistry(t), nil)
	h := s.requireCapabilityJSON("edit_posts")(s.jsonHandler(s.adminPosts))

	anon := httptest.NewRecorder()
	h.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/admin/api/posts?type=shop_order", nil))
	if anon.Code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", anon.Code)
	}

	low := httptest.NewRecorder()
	h.ServeHTTP(low, httptest.NewRequest(http.MethodGet, "/admin/api/posts?type=shop_order", nil).
		WithContext(principalCtx("read")))
	if low.Code != http.StatusForbidden {
		t.Errorf("under-privileged status = %d, want 403", low.Code)
	}
	if rd.calls != 0 {
		t.Errorf("reader called %d time(s) for rejected callers, want 0", rd.calls)
	}
}
