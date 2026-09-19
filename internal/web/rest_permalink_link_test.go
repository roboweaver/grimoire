package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The fixture post this file relies on (storagetest.SeedFixtures):
//
//	id 3  hello-3  post  publish  2024-01-03
//
// so under structDayAndName its canonical path is /2024/01/03/hello-3/ and
// under structNumeric it is /archives/3/.
const hello3Canonical = "/2024/01/03/hello-3/"

// TestRESTAbsoluteLinkForCanonicalPermalink covers Req 6.3: the web layer
// resolves the relative "link" the content layer produces into an absolute URL
// from the request's scheme and host, unchanged by M9a.
//
// The pre-M9a assertion (TestRESTAbsoluteLinksUseRequestHost) only ever saw a
// single-segment, slash-free "/hello-3". Now that postLink delegates to
// routing.Structure.Canonical, the value handed to restAbs can be
// multi-segment and can carry a trailing slash, which are precisely the shapes
// a naive join would mangle — a dropped or doubled separator, or a lost
// trailing slash, would advertise a URL that 301s (or 404s) for every consumer.
// So this asserts the absolute form byte-for-byte, and then requests the path
// it advertises back through the same router to prove it is the path served at
// 200 rather than merely a well-formed string.
func TestRESTAbsoluteLinkForCanonicalPermalink(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		want      string
	}{
		{
			// Four segments and a trailing slash: the shape Req 6.3 had never
			// been exercised against.
			name:      "day and name",
			structure: structDayAndName,
			want:      "http://example.test" + hello3Canonical,
		},
		{
			name:      "month and name",
			structure: structMonthAndName,
			want:      "http://example.test/2024/01/hello-3/",
		},
		{
			name:      "post name",
			structure: structPostName,
			want:      "http://example.test/hello-3/",
		},
		{
			// A literal segment plus an id, to show the absolutised link is
			// not postname-specific.
			name:      "numeric",
			structure: structNumeric,
			want:      "http://example.test/archives/3/",
		},
		{
			// Req 6.2: unchanged from before M9a, and identical to what
			// TestRESTAbsoluteLinksUseRequestHost asserts.
			name:      "flat",
			structure: "",
			want:      "http://example.test/hello-3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRESTRouterWithPermalinks(t, &fakeSessions{}, tc.structure)

			got := restPostLink(t, h, "/wp-json/wp/v2/posts/3")
			if got != tc.want {
				t.Fatalf("link = %q, want %q", got, tc.want)
			}

			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse link %q: %v", got, err)
			}
			if rec := get(t, h, u.Path); rec.Code != http.StatusOK {
				t.Errorf("GET %s = %d, want 200 (the advertised link must be the path served)", u.Path, rec.Code)
			}
		})
	}
}

// TestRESTAbsoluteLinkForCanonicalPermalinkUnderTLS asserts the https scheme is
// still derived from the request, not baked in, now that the path component is
// structure-derived (Req 6.3).
func TestRESTAbsoluteLinkForCanonicalPermalinkUnderTLS(t *testing.T) {
	h := newRESTRouterWithPermalinks(t, &fakeSessions{}, structDayAndName)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://example.test/wp-json/wp/v2/posts/3", nil)
	req.Host = "example.test"
	h.ServeHTTP(rec, req)

	want := "https://example.test" + hello3Canonical
	if got := decodeRESTPostLink(t, rec); got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

// restPostLink issues a wp-json request for a single item against host
// example.test and returns its "link" field.
func restPostLink(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "example.test"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, rec.Code)
	}
	return decodeRESTPostLink(t, rec)
}

func decodeRESTPostLink(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body.Link
}
