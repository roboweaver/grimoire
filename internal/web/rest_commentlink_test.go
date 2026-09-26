package web_test

import (
	"net/http"
	"net/url"
	"testing"
)

// The fixture comment this file asserts against (storagetest.SeedFixtures):
//
//	comment 101, post 1 ("hello-1"), comment_approved "1"
//
// so the fallback link Req 10.4 keeps is "/?p=1#comment-101", absolutised by the
// web layer against the request Host. Comment 101 is the approved one, which is
// what makes it readable through the public wp-json endpoints with no principal.
const (
	restCommentIDApproved   = 101
	restCommentPostID       = 1
	restCommentLinkAbsolute = "http://example.test/?p=1#comment-101"
)

// TestRESTCommentLinkStaysOnPlainPermalinkFallback is Req 10.4 at the boundary a
// consumer actually reads: the absolutised "link" of a comment in both wp-json
// comment endpoints, under every permalink structure. The content-layer
// counterpart (internal/content's TestRESTCommentLinkStaysOnPlainPermalinkFallback)
// pins the relative form; this one pins what ships in the response.
//
// It passes today. Its value is failing if tasks 8.3/8.4 — which rewrite the
// term and user links either side of it — sweep commentLink along with them.
func TestRESTCommentLinkStaysOnPlainPermalinkFallback(t *testing.T) {
	for _, structure := range regressionStructures {
		name := structure
		if name == "" {
			name = "flat"
		}
		t.Run(name, func(t *testing.T) {
			h, _ := newFullRouter(t, structure)

			single := restJSONObject(t, h, "/wp-json/wp/v2/comments/"+itoa(restCommentIDApproved))
			got, ok := single["link"].(string)
			if !ok {
				t.Fatalf("comment response has no string link: %+v", single)
			}
			if got != restCommentLinkAbsolute {
				t.Errorf("single comment link = %q, want %q", got, restCommentLinkAbsolute)
			}

			// The collection maps through the same mapRESTComment, but it is the
			// endpoint a consumer pages through, so a link fixed in one and not
			// the other would ship both shapes at once.
			items := restJSONArray(t, h, "/wp-json/wp/v2/comments?post="+itoa(restCommentPostID))
			if len(items) == 0 {
				t.Fatalf("comments collection for post %d is empty (fixture problem, not a link bug)", restCommentPostID)
			}
			for _, c := range items {
				id, ok := c["id"].(float64)
				if !ok {
					t.Fatalf("comment has no numeric id: %+v", c)
				}
				if int64(id) != restCommentIDApproved {
					continue
				}
				if link, _ := c["link"].(string); link != restCommentLinkAbsolute {
					t.Errorf("collection comment link = %q, want %q", link, restCommentLinkAbsolute)
				}
			}
		})
	}
}

// TestNoRouteServesTheAdvertisedCommentLink asserts the *reason* Req 10.4 gives,
// rather than only the string it prescribes: there is no comment route to point
// at, so any link built from the IDs a comment carries would advertise a URL
// this router does not serve. Req 10.2's "an advertised link returns 200"
// guarantee therefore cannot be extended to comments, and the fallback is kept
// precisely because it is honestly shaped like a query rather than like a path.
//
// Three facts together are that reason:
//
//  1. The fragment ("#comment-101") never reaches the server, so all a client
//     sends is "/?p=1" — and grimoire honors no "?p=" query, so that request is
//     byte-for-byte the home page. The fallback is a WordPress-shaped mirror,
//     not a resolution mechanism, and asserting it is served at 200 (as
//     assertServedAt200 does for terms and users) would pass for the wrong
//     reason.
//  2. No comment permalink route exists under any structure.
//  3. A link keyed by post ID resolves only under a %post_id% structure, and
//     404s under the other four — so an ID-keyed comment link would be right in
//     one configuration out of five and silently broken in the rest. That is the
//     asymmetry the fallback avoids.
func TestNoRouteServesTheAdvertisedCommentLink(t *testing.T) {
	for _, structure := range regressionStructures {
		name := structure
		if name == "" {
			name = "flat"
		}
		t.Run(name, func(t *testing.T) {
			h, _ := newFullRouter(t, structure)

			// (1) "?p=" is ignored: the advertised link's server-visible part is
			// the home page, not the comment's post.
			home := get(t, h, "/")
			withQuery := get(t, h, "/?p="+itoa(restCommentPostID))
			if withQuery.Code != http.StatusOK {
				t.Fatalf("GET /?p=%d = %d, want 200 (the home route answers it)", restCommentPostID, withQuery.Code)
			}
			if withQuery.Body.String() != home.Body.String() {
				t.Errorf("GET /?p=%d differs from GET /: the ?p= query now resolves something, which would change what the comment link means",
					restCommentPostID)
			}

			// (2) and (3): every path a comment link could name, and what this
			// router does with it.
			for _, tc := range commentRouteAbsenceCases(structure) {
				rec := get(t, h, tc.path)
				if rec.Code != tc.wantStatus {
					t.Errorf("GET %s = %d, want %d (%s)", tc.path, rec.Code, tc.wantStatus, tc.why)
				}
			}
		})
	}
}

// commentRouteAbsenceCase is one candidate path for a comment permalink and the
// status this router gives it. why is reported on failure so a break names the
// property that moved rather than only the number.
type commentRouteAbsenceCase struct {
	path       string
	wantStatus int
	why        string
}

// commentRouteAbsenceCases enumerates the paths a comment link could plausibly
// be rewritten to. The ID-keyed post path is the interesting one: it is
// structure-dependent, which is exactly why a link built from a post ID cannot
// be made to work in general.
func commentRouteAbsenceCases(structure string) []commentRouteAbsenceCase {
	cases := []commentRouteAbsenceCase{
		{
			path:       "/comment/" + itoa(restCommentIDApproved),
			wantStatus: http.StatusNotFound,
			why:        "no comment permalink route is added in this milestone (Req 10.4)",
		},
		{
			path:       "/comments/" + itoa(restCommentIDApproved),
			wantStatus: http.StatusNotFound,
			why:        "no comment archive route either",
		},
		{
			path:       "/" + itoa(restCommentPostID),
			wantStatus: http.StatusNotFound,
			why:        "the flat fallback resolves a slug, not a post ID",
		},
	}
	// Under /archives/%post_id%/ the ID-keyed path is the canonical permalink and
	// serves the post; under every other structure it names nothing. Both halves
	// are asserted, because the coincidence is the point.
	if structure == structNumeric {
		return append(cases, commentRouteAbsenceCase{
			path:       "/archives/" + itoa(restCommentPostID) + "/",
			wantStatus: http.StatusOK,
			why:        "%post_id% is in the structure, so this one configuration does serve an ID-keyed path",
		})
	}
	return append(cases, commentRouteAbsenceCase{
		path:       "/archives/" + itoa(restCommentPostID) + "/",
		wantStatus: http.StatusNotFound,
		why:        "no %post_id% in the structure, so an ID-keyed link would 404 here",
	})
}

// TestCommentLinkFragmentIsNotSentToTheServer records the mechanism behind fact
// (1) above rather than assuming it: a client that parses the advertised link
// sends only "/?p=1", because the "#comment-101" half is a fragment and
// fragments are client-side. No routing change could make it addressable, which
// is why the comment link cannot be held to Req 10.2's served-at-200 rule the
// way the term and user links are.
func TestCommentLinkFragmentIsNotSentToTheServer(t *testing.T) {
	u, err := url.Parse(restCommentLinkAbsolute)
	if err != nil {
		t.Fatalf("parse %q: %v", restCommentLinkAbsolute, err)
	}
	if want := "comment-" + itoa(restCommentIDApproved); u.Fragment != want {
		t.Errorf("fragment = %q, want %q", u.Fragment, want)
	}
	if got := u.RequestURI(); got != "/?p=1" {
		t.Errorf("request-URI of %q = %q, want %q (the fragment is not sent)",
			restCommentLinkAbsolute, got, "/?p=1")
	}
}
