package main

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// M16 task 7.1: the `?m=` date query wiring, asserted through buildHandler.
// Written RED, before internal/web learns serveDateQuery: every valid-m subtest
// fails because `/?m=…` renders the ordinary home page. The invalid-m subtests
// pass today and must keep passing; task 7.4 reverts the wiring and requires the
// valid-m subtests (and only those) to fail.
//
// Fixture: seedPagination -- 12 posts in 2024-04, 13 in 2024-05, all "Post NN";
// the home page shows the 10 newest (Post 25..16), so the archive of a month is
// told apart from the home page by its heading and its posts.

func TestBuildHandlerDateQuery(t *testing.T) {
	structures := []struct {
		name, raw string
		flat      bool
	}{
		{"postname slash", "/%postname%/", false},
		{"postname no slash", "/%postname%", false},
		{"flat", "", true},
	}
	for _, sc := range structures {
		t.Run(sc.name, func(t *testing.T) {
			h := paginationHandler(t, sc.raw, !sc.flat)
			get := func(path string) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				return rec
			}
			hasHref := func(body, url string) bool {
				return strings.Contains(body, `href="`+url+`"`) ||
					strings.Contains(body, `href="`+html.EscapeString(url)+`"`)
			}

			t.Run("valid m serves only that range with its heading", func(t *testing.T) {
				for _, tc := range []struct {
					m, heading, has string
					hasNot          []string
				}{
					{"2024", "2024", "Post 25", nil},
					// April holds Post 01..12; May holds Post 13..25 (page 1: 25..16).
					{"202404", "April 2024", "Post 12", []string{"Post 13", "Post 25"}},
					{"202405", "May 2024", "Post 25", []string{"Post 12", "Post 01"}},
					{"20240501", "1 May 2024", "Post 13", []string{"Post 14", "Post 12"}},
				} {
					rec := get("/?m=" + tc.m)
					if rec.Code != http.StatusOK {
						t.Fatalf("GET /?m=%s = %d, want 200", tc.m, rec.Code)
					}
					body := rec.Body.String()
					if !strings.Contains(body, tc.heading) {
						t.Errorf("GET /?m=%s lacks the heading %q", tc.m, tc.heading)
					}
					if !strings.Contains(body, tc.has) {
						t.Errorf("GET /?m=%s lacks %s", tc.m, tc.has)
					}
					for _, no := range tc.hasNot {
						if strings.Contains(body, no) {
							t.Errorf("GET /?m=%s holds %s from outside the range", tc.m, no)
						}
					}
				}
			})

			t.Run("Link header and pagination links", func(t *testing.T) {
				// 2024 holds 25 posts: three pages.
				rec := get("/?m=2024")
				if rec.Code != http.StatusOK {
					t.Fatalf("GET /?m=2024 = %d, want 200", rec.Code)
				}
				link := rec.Header().Values("Link")
				body := rec.Body.String()
				if sc.flat {
					if len(link) != 0 {
						t.Errorf("Flat Link = %q, want none", link)
					}
					if !hasHref(body, "/?m=2024&page=2") {
						t.Errorf("Flat page 1 Next is not /?m=2024&page=2")
					}
					return
				}
				slash := ""
				if sc.raw == "/%postname%/" {
					slash = "/"
				}
				base, p2 := "/2024"+slash, "/2024/page/2"+slash
				if len(link) != 1 || link[0] != "<"+base+`>; rel="canonical"` {
					t.Errorf("Link = %q, want <%s>; rel=\"canonical\"", link, base)
				}
				if !hasHref(body, p2) || strings.Contains(body, "?page=") {
					t.Errorf("page 1 Next is not the path form %s (or renders ?page=)", p2)
				}
			})

			t.Run("page and paged select the page", func(t *testing.T) {
				for _, q := range []string{"/?m=2024&page=2", "/?m=2024&paged=2"} {
					rec := get(q)
					body := rec.Body.String()
					if rec.Code != http.StatusOK || !strings.Contains(body, "Post 15") || strings.Contains(body, "Post 25") ||
						!strings.Contains(body, "2024") {
						t.Errorf("GET %s = %d, want page 2 of the 2024 archive", q, rec.Code)
					}
				}
				if rec := get("/?m=2024&page=99"); rec.Code != http.StatusNotFound {
					t.Errorf("GET /?m=2024&page=99 = %d, want 404", rec.Code)
				}
				if rec := get("/?m=20240501&page=2"); rec.Code != http.StatusNotFound {
					t.Errorf("GET /?m=20240501&page=2 = %d, want 404 (one post, one page)", rec.Code)
				}
			})

			t.Run("invalid m renders the home page", func(t *testing.T) {
				want := get("/")
				for _, q := range []string{"?m=garbage", "?m=202413", "?m=2024051712", "?m=", "?m=2024-05"} {
					rec := get("/" + q)
					if rec.Code != http.StatusOK || rec.Body.String() != want.Body.String() {
						t.Errorf("GET /%s = %d, want 200 with the same body as /", q, rec.Code)
					}
				}
			})

			t.Run("first m wins", func(t *testing.T) {
				rec := get("/?m=202404&m=x")
				if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "April 2024") {
					t.Errorf("GET /?m=202404&m=x = %d, want the April 2024 archive", rec.Code)
				}
			})

			t.Run("m is ignored on the home page path form and other archives", func(t *testing.T) {
				plain := get("/page/2" + map[bool]string{true: "/", false: ""}[sc.raw == "/%postname%/"])
				rec := get("/page/2" + map[bool]string{true: "/", false: ""}[sc.raw == "/%postname%/"] + "?m=202404")
				if rec.Code != http.StatusOK || rec.Body.String() != plain.Body.String() {
					t.Errorf("home page 2 with ?m= differs from home page 2")
				}
				cat := "/category/x" + map[bool]string{true: "/", false: ""}[sc.raw == "/%postname%/"]
				a, b := get(cat), get(cat+"?m=202404")
				if b.Code != http.StatusOK || a.Body.String() != b.Body.String() {
					t.Errorf("GET %s?m=202404 differs from %s", cat, cat)
				}
			})
		})
	}
}
