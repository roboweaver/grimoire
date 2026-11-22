package web_test

import (
	"net/http"
	"strings"
	"testing"
)

// M16 task 3.1 -- the Link canonical header (design "Canonical policy"), the
// ?page= bounds (requirement 8) and the rendered Prev/Next links (requirement 7).
// Fixture and builder: pagination_archives_test.go.

func canonicalLink(path string) string { return "<" + path + `>; rel="canonical"` }

// Req 6: the Link header is set only when the query carries page or paged, and
// names the canonical path-form URL of the page served.
func TestPaginationLinkHeader(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true)
	cases := []struct {
		name     string
		path     string
		wantLink string // "" means no Link header
		wantHas  string // a title the body must hold
	}{
		{"?page=2", baseGo + "?page=2", canonicalLink(baseGo + "page/2/"), pagOnP2},
		{"?paged=2", baseGo + "?paged=2", canonicalLink(baseGo + "page/2/"), pagOnP2},
		{"?page=1", baseGo + "?page=1", canonicalLink(baseGo), pagNewest},
		{"?page=2&page=3 uses the first", baseGo + "?page=2&page=3", canonicalLink(baseGo + "page/2/"), pagOnP2},
		{"path form, no query", baseGo + "page/2/", "", pagOnP2},
		{"path form with ?page=3 serves the path page", baseGo + "page/2/?page=3", canonicalLink(baseGo + "page/2/"), pagOnP2},
		{"plain archive", baseGo, "", pagNewest},
		{"home ?page=2", "/?page=2", canonicalLink("/page/2/"), pagOnP2},
		{"home path form", "/page/2/", "", pagOnP2},
		{"tag ?paged=3", baseTag + "?paged=3", canonicalLink(baseTag + "page/3/"), pagOldest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tc.path, rec.Code)
			}
			if got := rec.Header().Values("Link"); tc.wantLink == "" {
				if len(got) != 0 {
					t.Errorf("GET %s Link = %q, want none", tc.path, got)
				}
			} else if len(got) != 1 || got[0] != tc.wantLink {
				t.Errorf("GET %s Link = %q, want %q", tc.path, got, tc.wantLink)
			}
			if !strings.Contains(rec.Body.String(), tc.wantHas) {
				t.Errorf("GET %s body lacks %s", tc.path, tc.wantHas)
			}
		})
	}
}

// Req 8: the ?page= and ?paged= bounds, on an archive with posts.
func TestPaginationQueryBounds(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true)
	for _, base := range []string{"/", baseGo} {
		t.Run(base, func(t *testing.T) {
			// A value past int64 is clamped, so it is a 404 and never a panic or page 1.
			if rec := get(t, srv, base+"?page=99999999999999999999"); rec.Code != http.StatusNotFound {
				t.Errorf("?page=99999999999999999999 = %d, want 404", rec.Code)
			}
			for _, q := range []string{"?page=abc", "?page=0", "?page=-3"} {
				rec := get(t, srv, base+q)
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), pagNewest) {
					t.Errorf("%s%s = %d, want 200 page 1", base, q, rec.Code)
				}
			}
			rec := get(t, srv, base+"?paged=2")
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), pagOnP2) ||
				strings.Contains(rec.Body.String(), pagNewest) {
				t.Errorf("%s?paged=2 = %d, want page 2", base, rec.Code)
			}
			// page wins over paged.
			rec = get(t, srv, base+"?page=3&paged=2")
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), pagOnP2) {
				t.Errorf("%s?page=3&paged=2 = %d, want page 3", base, rec.Code)
			}
		})
	}
}

// Req 7: the rendered Previous and Next links are path-form, and the last page
// has no Next link.
func TestPaginationRenderedLinks(t *testing.T) {
	srv := newPaginationServer(t, structDayAndName, true)
	href := func(u string) string { return `href="` + u + `"` }

	body := get(t, srv, "/").Body.String()
	if !strings.Contains(body, href("/page/2/")) {
		t.Errorf("home page 1 lacks %s", href("/page/2/"))
	}
	body = get(t, srv, "/page/2/").Body.String()
	for _, want := range []string{href("/page/3/"), href("/")} {
		if !strings.Contains(body, want) {
			t.Errorf("home page 2 lacks %s", want)
		}
	}
	if strings.Contains(body, href("/page/1/")) {
		t.Errorf("home page 2 Previous is /page/1/, want the base")
	}

	body = get(t, srv, baseGo+"page/2/").Body.String()
	for _, want := range []string{href(baseGo + "page/3/"), href(baseGo)} {
		if !strings.Contains(body, want) {
			t.Errorf("%spage/2/ lacks %s", baseGo, want)
		}
	}
	last := get(t, srv, baseGo+"page/3/").Body.String()
	if strings.Contains(last, href(baseGo+"page/4/")) || strings.Contains(last, "Next</a>") {
		t.Errorf("last page has a Next link")
	}
	for _, p := range []string{"/", baseGo, baseTag, baseAuthor, baseDay, baseGo + "page/2/"} {
		if b := get(t, srv, p).Body.String(); strings.Contains(b, "?page=") {
			t.Errorf("GET %s renders a ?page= href", p)
		}
	}
}
