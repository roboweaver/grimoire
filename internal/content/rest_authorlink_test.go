package content

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// restStructFrontDayAndName is a structure with a permalink front, the one
// archive-shaping input that is invisible in every other case here: the front is
// prepended to author archives always (Req 4.7), unlike category and tag
// archives where it depends on the base option.
const restStructFrontDayAndName = "/blog/%year%/%monthnum%/%day%/%postname%/"

// restStructPostNameNoSlash is the no-trailing-slash structure that carries *no*
// front, which is what isolates the trailing-slash rule from the front rule.
// restStructNumeric ("/archives/%post_id%") cannot: its leading literal is a
// front, and the front applies to author archives unconditionally (Req 4.7), so
// its author path is "/archives/author/alice" and a case expecting
// "/author/alice" from it would be asserting the absence of a front the spec
// requires. Both configurations are covered below, one per dimension.
const restStructPostNameNoSlash = "/%postname%"

// TestRESTUserLinkIsAuthorArchivePath covers Req 7.5 and 10.3. userLink returns
// "/?author={id}" today -- the plain-permalink fallback M9a kept explicitly
// because no author route existed -- and must become the author archive path,
// keyed by user_nicename rather than by ID, now that /author/{nicename} is
// served.
//
// The expected paths are literals rather than calls to Structure.AuthorPath, so
// the assertion cannot merely agree with the constructor under test. The subject
// is nicename "alice" with ID 5, which is where "/author/alice" comes from and
// what makes a link still keyed by ID obvious rather than coincidentally equal.
func TestRESTUserLinkIsAuthorArchivePath(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		want      string
	}{
		{
			// Req 2.7's rule applies to every archive path: a flat structure
			// carries no trailing slash.
			name:      "flat structure",
			structure: "",
			want:      "/author/alice",
		},
		{
			name:      "trailing-slash structure",
			structure: restStructDayAndName,
			want:      "/author/alice/",
		},
		{
			name:      "structure without a trailing slash",
			structure: restStructPostNameNoSlash,
			want:      "/author/alice",
		},
		{
			// Req 4.7: the front applies to author archives unconditionally,
			// and no base option can move it.
			name:      "structure with a front",
			structure: restStructFrontDayAndName,
			want:      "/blog/author/alice/",
		},
		{
			// The two rules together, which is the combination
			// "/archives/%post_id%" actually exercises: the front still
			// applies (Req 4.7) and the missing trailing slash still applies
			// (Req 2.7), so neither rule can quietly stand in for the other.
			name:      "front without a trailing slash",
			structure: restStructNumeric,
			want:      "/archives/author/alice",
		},
	}

	u := domain.User{ID: 5, DisplayName: "Alice", Nicename: "alice", Email: "alice@example.test"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapper := newTestMapperWithPermalinks(t, tc.structure)

			got, err := mapper.User(context.Background(), u, RESTContextView)
			if err != nil {
				t.Fatalf("User: %v", err)
			}
			view, ok := got.(RESTUser)
			if !ok {
				t.Fatalf("got type %T, want RESTUser", got)
			}
			if view.Link != tc.want {
				t.Errorf("view-context user Link = %q, want %q", view.Link, tc.want)
			}

			// The edit context returns a different type wrapping the same
			// view, so a link fixed in one and not the other would leave
			// authenticated consumers on the old fallback.
			gotEdit, err := mapper.User(context.Background(), u, RESTContextEdit)
			if err != nil {
				t.Fatalf("User (edit): %v", err)
			}
			edit, ok := gotEdit.(RESTUserEdit)
			if !ok {
				t.Fatalf("got type %T, want RESTUserEdit", gotEdit)
			}
			if edit.Link != tc.want {
				t.Errorf("edit-context user Link = %q, want %q", edit.Link, tc.want)
			}
		})
	}
}

// TestRESTUserLinkUsesNicenameNotDisplayName pins the field the path is keyed
// on. A user whose nicename differs from both the display name and the ID is the
// only fixture that can tell "/author/{nicename}" apart from a path built from
// whatever else is to hand (Req 7.1's resolution by user_nicename).
func TestRESTUserLinkUsesNicenameNotDisplayName(t *testing.T) {
	mapper := newTestMapperWithPermalinks(t, restStructPostName)

	u := domain.User{ID: 7, DisplayName: "Robert Weaver", Nicename: "rob-w"}
	got, err := mapper.User(context.Background(), u, RESTContextView)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	view, ok := got.(RESTUser)
	if !ok {
		t.Fatalf("got type %T, want RESTUser", got)
	}
	if want := "/author/rob-w/"; view.Link != want {
		t.Errorf("user Link = %q, want %q", view.Link, want)
	}
}
