package web_test

import (
	"net/http"
	"testing"
)

// WordPress's three permalink presets, plus the numeric form, as they appear in
// the permalink_structure option.
const (
	structDayAndName   = "/%year%/%monthnum%/%day%/%postname%/"
	structMonthAndName = "/%year%/%monthnum%/%postname%/"
	structPostName     = "/%postname%/"
	structNumeric      = "/archives/%post_id%/"
)

// The fixture posts this file relies on (storagetest.SeedFixtures):
//
//	id 1  hello-1  post  publish  2024-01-01
//	id 4  secret   post  draft    2024-01-04
//	id 5  about    page  publish  2024-01-05
//
// so under structDayAndName hello-1's canonical path is /2024/01/01/hello-1/.
const (
	helloCanonical = "/2024/01/01/hello-1/"
	aboutCanonical = "/2024/01/05/about/"
)

// TestPermalinkStatusCodes covers every row of design.md's status-code table
// (Req 2.3, 2.5, 2.6, 3.1, 3.2). Each case is a request path against a server
// configured with structDayAndName, and asserts the exact status and, for
// redirects, the exact Location.
func TestPermalinkStatusCodes(t *testing.T) {
	cases := []struct {
		name         string
		path         string
		wantStatus   int
		wantLocation string
	}{
		{
			name:       "canonical path renders",
			path:       helloCanonical,
			wantStatus: http.StatusOK,
		},
		{
			name:       "canonical path of a page renders",
			path:       aboutCanonical,
			wantStatus: http.StatusOK,
		},
		{
			// Req 3.3: the structure ends in "/", so the slashless form is not
			// canonical and must not serve the same content at a second URL.
			name:         "matching path missing the trailing slash redirects",
			path:         "/2024/01/01/hello-1",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: helloCanonical,
		},
		{
			// Req 3.1: the flat path is what grimoire served before this
			// milestone, and what WordPress itself redirects away from.
			name:         "flat slug redirects to the canonical path",
			path:         "/hello-1",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: helloCanonical,
		},
		{
			name:         "flat slug for a page redirects to the canonical path",
			path:         "/about",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: aboutCanonical,
		},
		{
			// Req 2.5: serving hello-1 here would publish it at a date that is
			// not its own, so this is a 404 rather than a redirect.
			name:       "date components contradicting the post 404",
			path:       "/2024/01/02/hello-1/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "wrong year 404s",
			path:       "/2023/01/01/hello-1/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "matching path with no such post 404s",
			path:       "/2024/01/01/no-such-post/",
			wantStatus: http.StatusNotFound,
		},
		{
			// The fixture's draft. Resolution is published-only, so an
			// unpublished slug must not render even at its own correct date.
			name:       "matching path for an unpublished post 404s",
			path:       "/2024/01/04/secret/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "flat slug for an unpublished post 404s",
			path:       "/secret",
			wantStatus: http.StatusNotFound,
		},
		{
			// Req 2.2: WordPress zero-pads, so a 1-digit month is not a path
			// this structure can produce and must not resolve.
			name:       "one-digit month 404s",
			path:       "/2024/1/01/hello-1/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "two-digit year 404s",
			path:       "/24/01/01/hello-1/",
			wantStatus: http.StatusNotFound,
		},
		{
			// Req 2.6: unchanged from today's behavior.
			name:       "path matching no route 404s",
			path:       "/2024/01/01",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unrelated two-segment path 404s",
			path:       "/nope/nope",
			wantStatus: http.StatusNotFound,
		},
	}

	srv := newTestServerWithPermalinks(t, structDayAndName)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, tc.path)
			if rec.Code != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d (body: %.200s)",
					tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tc.wantLocation {
				t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.wantLocation)
			}
		})
	}
}

// TestCanonicalPathDoesNotRedirect is Req 3.5's explicit no-loop test. A
// redirect here would be a loop on every post URL on the site, so it is asserted
// separately from the table above rather than left implicit in the 200.
func TestCanonicalPathDoesNotRedirect(t *testing.T) {
	structures := map[string]string{
		structDayAndName:   helloCanonical,
		structMonthAndName: "/2024/01/hello-1/",
		structPostName:     "/hello-1/",
		structNumeric:      "/archives/1/",
	}
	for structure, canonical := range structures {
		t.Run(structure, func(t *testing.T) {
			srv := newTestServerWithPermalinks(t, structure)
			rec := get(t, srv, canonical)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (body: %.200s)",
					canonical, rec.Code, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("canonical path redirected to %q; a request already at "+
					"the canonical path must render", loc)
			}
		})
	}
}

// TestRedirectPreservesQueryString covers Req 3.4. Losing the query string would
// silently drop pagination and campaign parameters on every redirected link.
func TestRedirectPreservesQueryString(t *testing.T) {
	srv := newTestServerWithPermalinks(t, structDayAndName)
	cases := []struct {
		name string
		path string
		want string
	}{
		{"single parameter", "/hello-1?utm_source=news", helloCanonical + "?utm_source=news"},
		{"several parameters", "/hello-1?a=1&b=2", helloCanonical + "?a=1&b=2"},
		{"empty value", "/hello-1?a=", helloCanonical + "?a="},
		{"no query string adds no ?", "/hello-1", helloCanonical},
		{
			"trailing-slash redirect keeps the query too",
			"/2024/01/01/hello-1?page=2",
			helloCanonical + "?page=2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, tc.path)
			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("GET %s = %d, want 301", tc.path, rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tc.want {
				t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestFlatStructureIssuesNoRedirects covers Req 1.2 and 3.6: with
// permalink_structure empty, the flat path is canonical, so behavior must be
// byte-for-byte what it was before this milestone.
func TestFlatStructureIssuesNoRedirects(t *testing.T) {
	srv := newTestServerWithPermalinks(t, "")
	for _, path := range []string{"/hello-1", "/about"} {
		t.Run(path, func(t *testing.T) {
			rec := get(t, srv, path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (body: %.200s)",
					path, rec.Code, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("GET %s redirected to %q; an empty permalink_structure "+
					"must issue no canonical redirects", path, loc)
			}
		})
	}

	// A dated path is not a route at all when no structure is configured.
	if rec := get(t, srv, helloCanonical); rec.Code != http.StatusNotFound {
		t.Errorf("GET %s = %d with no structure configured, want 404",
			helloCanonical, rec.Code)
	}
}

// TestNumericPermalinkResolvesByID covers the %post_id% half of Req 2.4, and the
// disclosure boundary behind PublishedByID: ids are guessable, so an unpublished
// post must 404 rather than render for an anonymous visitor.
func TestNumericPermalinkResolvesByID(t *testing.T) {
	srv := newTestServerWithPermalinks(t, structNumeric)

	if rec := get(t, srv, "/archives/1/"); rec.Code != http.StatusOK {
		t.Errorf("GET /archives/1/ = %d, want 200 (body: %.200s)",
			rec.Code, rec.Body.String())
	}
	// id 4 is the fixture's draft.
	if rec := get(t, srv, "/archives/4/"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /archives/4/ = %d for a draft, want 404", rec.Code)
	}
	if rec := get(t, srv, "/archives/9999/"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /archives/9999/ = %d, want 404", rec.Code)
	}
	// Non-numeric where an id is expected is not a path this structure produces.
	if rec := get(t, srv, "/archives/abc/"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /archives/abc/ = %d, want 404", rec.Code)
	}
	// The flat slug still redirects to the id-based canonical path.
	rec := get(t, srv, "/hello-1")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /hello-1 = %d, want 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/archives/1/" {
		t.Errorf("GET /hello-1 Location = %q, want /archives/1/", got)
	}
}

// TestPermalinksDoNotShadowOtherRoutes guards the registration-order claim in
// task 3.5: adding the structure routes must leave the existing routes reachable.
func TestPermalinksDoNotShadowOtherRoutes(t *testing.T) {
	for _, structure := range []string{structDayAndName, structMonthAndName, structPostName, structNumeric} {
		t.Run(structure, func(t *testing.T) {
			srv := newTestServerWithPermalinks(t, structure)
			for _, tc := range []struct {
				path string
				want int
			}{
				{"/", http.StatusOK},
				{"/category/news", http.StatusOK},
				{"/healthz", http.StatusOK},
			} {
				if rec := get(t, srv, tc.path); rec.Code != tc.want {
					t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.want)
				}
			}
		})
	}
}
