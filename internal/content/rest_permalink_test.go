package content

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// WordPress's three permalink presets, plus the numeric form, as they appear in
// the permalink_structure option. Mirrors the constants the web handler tests
// use so the two layers are asserted against the same structures.
const (
	restStructDayAndName   = "/%year%/%monthnum%/%day%/%postname%/"
	restStructMonthAndName = "/%year%/%monthnum%/%postname%/"
	restStructPostName     = "/%postname%/"
	restStructNumeric      = "/archives/%post_id%"
)

// newTestMapperWithPermalinks builds a RESTMapper whose permalink structure is
// the parsed form of the given raw permalink_structure option value. An empty
// structure yields the flat behavior newTestMapper relies on, so the two agree
// on everything except the structure.
func newTestMapperWithPermalinks(t *testing.T, structure string) *RESTMapper {
	t.Helper()
	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", structure, err)
	}
	terms := &fakeRESTPostTerms{byPost: map[int64]map[string][]int64{}}
	meta := &fakeRESTPostMeta{featured: map[int64]int64{}, attach: map[int64]string{}}
	users := &fakeRESTUserMeta{values: map[int64]map[string]string{}}
	return NewRESTMapper(terms, meta, users, "wp_").WithPermalinks(st)
}

// TestRESTLinkIsCanonicalPermalink asserts the REST "link" field is the
// canonical permalink for the configured structure, not the hard-coded
// "/"+slug (Req 6.1), and that an empty structure leaves it at /{slug}
// (Req 6.2).
//
// The expected paths are written out as literals rather than computed with
// routing.Canonical, so the assertion cannot merely agree with the very
// function under test. samplePost() is dated 2024-03-05 with slug
// "hello-world" and id 42, which is where every expectation below comes from.
func TestRESTLinkIsCanonicalPermalink(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		wantPost  string
		wantPage  string
	}{
		{
			// Req 6.2: no structure configured, so the flat path is canonical
			// and the field is byte-for-byte what it was before M9a.
			name:      "flat structure keeps /{slug}",
			structure: "",
			wantPost:  "/hello-world",
			wantPage:  "/about",
		},
		{
			name:      "day and name",
			structure: restStructDayAndName,
			wantPost:  "/2024/03/05/hello-world/",
			wantPage:  "/2024/03/05/about/",
		},
		{
			name:      "month and name",
			structure: restStructMonthAndName,
			wantPost:  "/2024/03/hello-world/",
			wantPage:  "/2024/03/about/",
		},
		{
			name:      "post name",
			structure: restStructPostName,
			wantPost:  "/hello-world/",
			wantPage:  "/about/",
		},
		{
			// No trailing slash in the structure, so none in the link
			// (Req 3.3 feeding Req 6.1): a link carrying the non-canonical
			// slash form would be a URL that 301s for every consumer.
			name:      "numeric without trailing slash",
			structure: restStructNumeric,
			wantPost:  "/archives/42",
			wantPage:  "/archives/42",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapper := newTestMapperWithPermalinks(t, tc.structure)

			post, err := mapper.Post(context.Background(), samplePost())
			if err != nil {
				t.Fatalf("Post: %v", err)
			}
			if post.Link != tc.wantPost {
				t.Errorf("post Link = %q, want %q", post.Link, tc.wantPost)
			}

			p := samplePost()
			p.Type = "page"
			p.Slug = "about"
			page, err := mapper.Page(context.Background(), p)
			if err != nil {
				t.Fatalf("Page: %v", err)
			}
			if page.Link != tc.wantPage {
				t.Errorf("page Link = %q, want %q", page.Link, tc.wantPage)
			}
		})
	}
}

// TestRESTCommentLinkUnchangedByPermalinkStructure is the surviving half of
// M9a's TestRESTCommentAndUserLinksUnchangedByPermalinkStructure. That test
// asserted Req 6.4 for both links at once: commentLink and userLink stay on
// their plain-permalink-shaped fallbacks even when a structure is configured,
// because neither route existed.
//
// Only the comment half of that premise survives M9b. No comment route is added
// in this milestone, so routing a comment link through the structure would still
// advertise a URL that 404s (Req 10.4) — which is what this keeps asserting, and
// what task 8.2's rest_commentlink_test.go widens across five structures and
// overridden bases.
//
// The user half was not weakened, it was inverted: /author/{nicename} is now
// served, so the user link *is* structure-dependent and "unchanged by permalink
// structure" is no longer a true statement about it. It moved to
// TestRESTUserLinkNowFollowsPermalinkStructure below, on the same fixture and the
// same mapper, and the full path table lives in
// TestRESTUserLinkIsAuthorArchivePath (rest_authorlink_test.go).
func TestRESTCommentLinkUnchangedByPermalinkStructure(t *testing.T) {
	c := Comment(domain.Comment{ID: 9, PostID: 42, Status: commentStatusOK})
	if want := "/?p=42#comment-9"; c.Link != want {
		t.Errorf("comment Link = %q, want %q", c.Link, want)
	}
}

// TestRESTUserLinkNowFollowsPermalinkStructure is the inverted user half of the
// test above, kept on its original fixture (user 5, nicename "alice", the
// day-and-name structure) so the transition is legible at the exact input that
// used to assert the opposite.
//
// M9a Req 6.4 held the user link on "/?author=5" *explicitly because no author
// route existed*; Req 7.5 ends that condition with this milestone. So the
// assertion that matters here is both directions at once: the configured
// structure now reaches the link, and the old fallback is gone rather than
// coincidentally equal to something else.
func TestRESTUserLinkNowFollowsPermalinkStructure(t *testing.T) {
	mapper := newTestMapperWithPermalinks(t, restStructDayAndName)

	got, err := mapper.User(context.Background(), domain.User{ID: 5, DisplayName: "Alice", Nicename: "alice"}, RESTContextView)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	view, ok := got.(RESTUser)
	if !ok {
		t.Fatalf("got type %T, want RESTUser", got)
	}
	if view.Link == "/?author=5" {
		t.Errorf("user Link = %q, want the author archive path, not M9a's plain-permalink fallback", view.Link)
	}
	if want := "/author/alice/"; view.Link != want {
		t.Errorf("user Link = %q, want %q", view.Link, want)
	}
}
