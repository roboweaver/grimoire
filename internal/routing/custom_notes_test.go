package routing_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// Substrings the CustomNotes() entries must carry (task 4.8 matches these).
// Substrings, not whole strings, for the reason noteWant documents: the wording
// is not a contract, but an operator must be able to tell which registry entry
// is at fault and what happens to the URL.
//
//	(a) post-shape shadow  : entry Name, "archive", "wins", "unreachable"
//	(b) year-first archive : entry Name, the year ("2024"), "date"
//	(b) year-first single  : entry Name, the year ("2024"), "date"

// Req 7.8, 7.9; design Finding 1. Under a single-token post shape a custom
// archive whose base equals that shape wins row 6.5 over row 7, so a post slugged
// like the archive slug is unreachable. The note says so and names the entry.
func TestCustomNotes_ArchiveShadowsPostShape(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		entry     routing.TypeRoute
	}{
		{
			name:      "bare postname",
			structure: "/%postname%/",
			entry:     routing.TypeRoute{Name: "product", Slug: "product", ArchiveSlug: "products"},
		},
		{
			name:      "front plus postname, archive withFront",
			structure: "/blog/%postname%/",
			entry:     routing.TypeRoute{Name: "product", Slug: "product", ArchiveSlug: "products", WithFront: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ccParse(t, tc.structure, []routing.TypeRoute{tc.entry}, nil)
			assertNotes(t, s.CustomNotes(), []noteWant{{
				why:      "the archive shadowing the post shape",
				contains: []string{tc.entry.Name, "archive", "wins", "unreachable"},
			}})
		})
	}
}

// Req 7.4; design Component 3. A base whose first segment is a four-digit year is
// claimed first by the date row, for an archive base and for a single base alike.
func TestCustomNotes_YearFirstBase(t *testing.T) {
	cases := []struct {
		name  string
		entry routing.TypeRoute
		want  noteWant
	}{
		{
			name:  "archive base",
			entry: routing.TypeRoute{Name: "report", Slug: "report", ArchiveSlug: "2024"},
			want:  noteWant{why: "the year-first archive base", contains: []string{"report", "2024", "date"}},
		},
		{
			name:  "single base",
			entry: routing.TypeRoute{Name: "report", Slug: "2024"},
			want:  noteWant{why: "the year-first single base", contains: []string{"report", "2024", "date"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// /blog/%postname%/ with WithFront=false: the base is just the slug, so
			// only the year rule can fire (not the post-shape shadow).
			s := ccParse(t, ccFrontBlog, []routing.TypeRoute{tc.entry}, nil)
			assertNotes(t, s.CustomNotes(), []noteWant{tc.want})
		})
	}
}

// A year that is not the FIRST segment of the base is not claimed by the date
// row, and a non-four-digit number is not a year: neither draws a year/date
// note. Under /blog/%postname%/ these single-segment archives legitimately draw
// the post-shape shadow note, so the assertion is the absence of the date note
// (the year rule's "date" marker), not an empty result.
func TestCustomNotes_YearRuleIsFirstSegmentAndFourDigits(t *testing.T) {
	s := ccParse(t, ccFrontBlog, []routing.TypeRoute{
		{Name: "behind_front", Slug: "behind_front", ArchiveSlug: "2024", WithFront: true}, // /blog/2024
		{Name: "short", Slug: "short", ArchiveSlug: "202"},
		{Name: "long", Slug: "long", ArchiveSlug: "20245"},
	}, nil)
	for _, n := range s.CustomNotes() {
		if strings.Contains(n, "date") {
			t.Errorf("unexpected year/date note: %q", n)
		}
	}
}

// Req 7.4. A clean registry draws no notes, and a Structure that never saw
// WithCustomTypes (or saw it empty) has none either.
func TestCustomNotes_EmptyWhenClean(t *testing.T) {
	plain, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	// The post shape must not match a single segment, or every single-segment
	// archive slug shadows a same-slug post (row 6.5 vs row 7).
	clean := ccParse(t, "/%year%/%monthnum%/%postname%/",
		[]routing.TypeRoute{{Name: "event", Slug: "event", ArchiveSlug: "events"}},
		[]routing.TaxRoute{{Name: "genre", Slug: "genre"}})
	cases := map[string]routing.Structure{
		"never saw WithCustomTypes": plain,
		"nil entries":               plain.WithCustomTypes(nil, nil),
		"clean registry":            clean,
	}
	for name, s := range cases {
		if got := s.CustomNotes(); len(got) != 0 {
			t.Errorf("%s: CustomNotes() = %q, want empty", name, got)
		}
	}
}

// Req 7.9; design Component 3. CustomNotes is a separate method: Notes() is
// byte-identical with and without custom entries, and none of the custom text
// leaks into it.
func TestCustomNotes_NotesUnchanged(t *testing.T) {
	plain, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	withShadow := plain.WithCustomTypes([]routing.TypeRoute{
		{Name: "product", Slug: "product", ArchiveSlug: "products"},
		{Name: "report", Slug: "2024"},
	}, nil)
	if len(withShadow.CustomNotes()) == 0 {
		t.Fatal("precondition: expected CustomNotes() entries for the shadowing registry")
	}
	if got, want := withShadow.Notes(), plain.Notes(); !slices.Equal(got, want) {
		t.Errorf("Notes() changed after WithCustomTypes:\n got  %q\n want %q", got, want)
	}
	for _, n := range withShadow.Notes() {
		if strings.Contains(n, "product") || strings.Contains(n, "report") {
			t.Errorf("Notes() leaked a custom-entry note: %q", n)
		}
	}
}
