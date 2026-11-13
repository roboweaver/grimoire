// This file is in package routing rather than routing_test so it can reuse the
// ctParse helper and call the unexported joinPattern when asserting the pattern
// set, keeping the expected strings derived from the same builder the
// implementation uses.
package routing

import (
	"slices"
	"testing"
)

// cpStructure parses structure and attaches the given custom entries.
func cpStructure(t *testing.T, structure string, types []TypeRoute, taxes []TaxRoute) Structure {
	t.Helper()
	return ctParse(t, structure, "", "").WithCustomTypes(types, taxes)
}

// Requirement 4.4, 4.5: a custom post path is /{slug}/{postname}, with the
// structure's trailing-slash form, the front only when with_front, and never a
// dated permalink token.
func TestCustomPathPostPath(t *testing.T) {
	types := []TypeRoute{
		{Name: "book", Slug: "books", WithFront: true},
		{Name: "gadget", Slug: "gadgets", WithFront: false},
	}
	cases := []struct {
		name      string
		structure string
		typeName  string
		ancestry  []string
		want      string
	}{
		{"slash-less structure", "/%postname%", "book", []string{"dune"}, "/books/dune"},
		{"trailing-slash structure", "/%postname%/", "book", []string{"dune"}, "/books/dune/"},
		{"front prepended with with_front", "/blog/%postname%/", "book", []string{"dune"}, "/blog/books/dune/"},
		{"front omitted without with_front", "/blog/%postname%/", "gadget", []string{"widget"}, "/gadgets/widget/"},
		{"dated structure adds no date tokens", "/blog/%year%/%monthnum%/%postname%/", "book", []string{"dune"}, "/blog/books/dune/"},
		{"day-dated structure adds no date tokens", "/%year%/%monthnum%/%day%/%postname%/", "book", []string{"dune"}, "/books/dune/"},
		{"unregistered type", "/%postname%/", "nope", []string{"dune"}, ""},
		{"empty ancestry", "/%postname%/", "book", nil, ""},
		{"non-hierarchical with two segments", "/%postname%/", "book", []string{"a", "b"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cpStructure(t, tc.structure, types, nil)
			if got := s.CustomPostPath(tc.typeName, tc.ancestry); got != tc.want {
				t.Fatalf("CustomPostPath(%q, %v) = %q, want %q", tc.typeName, tc.ancestry, got, tc.want)
			}
		})
	}
}

// Requirement 4.6: a hierarchical type nests its ancestry root-first.
func TestCustomPathPostPathHierarchical(t *testing.T) {
	types := []TypeRoute{{Name: "volume", Slug: "volumes", Hierarchical: true}}
	s := cpStructure(t, "/%postname%/", types, nil)
	if got, want := s.CustomPostPath("volume", []string{"vol-1", "part-one"}), "/volumes/vol-1/part-one/"; got != want {
		t.Fatalf("nested = %q, want %q", got, want)
	}
	if got, want := s.CustomPostPath("volume", []string{"vol-1"}), "/volumes/vol-1/"; got != want {
		t.Fatalf("single = %q, want %q", got, want)
	}
	if got := s.CustomPostPath("volume", nil); got != "" {
		t.Fatalf("empty ancestry = %q, want empty", got)
	}
}

// Requirement 5.1: the archive path uses the ArchiveSlug (which may differ from
// Slug), honours with_front and the slash form, and is "" unless the type is
// registered with an archive.
func TestCustomPathArchivePath(t *testing.T) {
	types := []TypeRoute{
		{Name: "book", Slug: "books", ArchiveSlug: "library", WithFront: true},
		{Name: "gadget", Slug: "gadgets", ArchiveSlug: "gadgets"},
		{Name: "note", Slug: "notes"},
	}
	cases := []struct {
		name      string
		structure string
		typeName  string
		want      string
	}{
		{"distinct archive slug", "/%postname%/", "book", "/library/"},
		{"slash-less", "/%postname%", "book", "/library"},
		{"front with with_front", "/blog/%postname%/", "book", "/blog/library/"},
		{"front omitted without with_front", "/blog/%postname%/", "gadget", "/gadgets/"},
		{"registered without archive", "/%postname%/", "note", ""},
		{"unregistered", "/%postname%/", "nope", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cpStructure(t, tc.structure, types, nil)
			if got := s.CustomArchivePath(tc.typeName); got != tc.want {
				t.Fatalf("CustomArchivePath(%q) = %q, want %q", tc.typeName, got, tc.want)
			}
		})
	}
}

// Requirement 6.2, 6.5: CustomTermPath mirrors CustomPostPath for taxonomies.
func TestCustomPathTermPath(t *testing.T) {
	taxes := []TaxRoute{
		{Name: "genre", Slug: "genre", WithFront: true, Hierarchical: true},
		{Name: "label", Slug: "labels"},
	}
	cases := []struct {
		name      string
		structure string
		taxName   string
		ancestry  []string
		want      string
	}{
		{"slash-less", "/%postname%", "genre", []string{"scifi"}, "/genre/scifi"},
		{"trailing slash", "/%postname%/", "genre", []string{"scifi"}, "/genre/scifi/"},
		{"front with with_front", "/blog/%postname%/", "genre", []string{"scifi"}, "/blog/genre/scifi/"},
		{"front omitted without with_front", "/blog/%postname%/", "label", []string{"new"}, "/labels/new/"},
		{"dated structure adds no date tokens", "/blog/%year%/%monthnum%/%postname%/", "genre", []string{"scifi"}, "/blog/genre/scifi/"},
		{"hierarchical nests", "/%postname%/", "genre", []string{"fiction", "scifi"}, "/genre/fiction/scifi/"},
		{"unregistered", "/%postname%/", "nope", []string{"x"}, ""},
		{"empty ancestry", "/%postname%/", "genre", nil, ""},
		{"non-hierarchical with two segments", "/%postname%/", "label", []string{"a", "b"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cpStructure(t, tc.structure, nil, taxes)
			if got := s.CustomTermPath(tc.taxName, tc.ancestry); got != tc.want {
				t.Fatalf("CustomTermPath(%q, %v) = %q, want %q", tc.taxName, tc.ancestry, got, tc.want)
			}
		})
	}
}

// A Flat structure has no front and no trailing slash, so with_front is moot
// and every shape is the bare /books/dune form.
func TestCustomPathFlatIgnoresWithFront(t *testing.T) {
	for _, withFront := range []bool{true, false} {
		types := []TypeRoute{{Name: "book", Slug: "books", ArchiveSlug: "library", WithFront: withFront}}
		taxes := []TaxRoute{{Name: "genre", Slug: "genre", WithFront: withFront}}
		s := cpStructure(t, "", types, taxes)
		if !s.Flat {
			t.Fatal("expected a Flat structure")
		}
		if got, want := s.CustomPostPath("book", []string{"dune"}), "/books/dune"; got != want {
			t.Errorf("with_front=%v post = %q, want %q", withFront, got, want)
		}
		if got, want := s.CustomArchivePath("book"), "/library"; got != want {
			t.Errorf("with_front=%v archive = %q, want %q", withFront, got, want)
		}
		if got, want := s.CustomTermPath("genre", []string{"scifi"}), "/genre/scifi"; got != want {
			t.Errorf("with_front=%v term = %q, want %q", withFront, got, want)
		}
	}
}

// Requirement 7.5: nothing registered means no patterns at all.
func TestCustomPathPatternsNilWhenEmpty(t *testing.T) {
	for _, structure := range []string{"", "/%postname%/", "/blog/%postname%/"} {
		s := ctParse(t, structure, "", "")
		if got := s.CustomPatterns(); got != nil {
			t.Errorf("%q: CustomPatterns() = %v, want nil", structure, got)
		}
		if got := s.WithCustomTypes(nil, nil).CustomPatterns(); got != nil {
			t.Errorf("%q: after WithCustomTypes(nil, nil) = %v, want nil", structure, got)
		}
	}
}

// Requirement 7.5: per public type the single wildcard plus, when the type has
// an archive, the bare archive in both slash forms (chi's /products/* does not
// match bare /products); per taxonomy the wildcard only, with no bare pattern.
func TestCustomPathPatternsExact(t *testing.T) {
	types := []TypeRoute{
		{Name: "book", Slug: "books", ArchiveSlug: "library", WithFront: true},
		{Name: "volume", Slug: "volumes", Hierarchical: true},
	}
	taxes := []TaxRoute{{Name: "genre", Slug: "genre", WithFront: true, Hierarchical: true}}

	for _, structure := range []string{"", "/%postname%/", "/%postname%", "/blog/%year%/%monthnum%/%postname%/"} {
		t.Run(structure, func(t *testing.T) {
			s := cpStructure(t, structure, types, taxes)

			bookBase := s.CustomBase("books", true)
			archiveBase := s.CustomBase("library", true)
			volumeBase := s.CustomBase("volumes", false)
			genreBase := s.CustomBase("genre", true)

			want := []string{
				joinPattern(bookBase, "*"),
				joinPattern(archiveBase),
				joinPattern(archiveBase) + "/",
				joinPattern(volumeBase, "*"),
				joinPattern(genreBase, "*"),
			}
			got := s.CustomPatterns()
			if len(got) != len(want) {
				t.Fatalf("CustomPatterns() = %v, want the %d patterns %v", got, len(want), want)
			}
			for _, w := range want {
				if !slices.Contains(got, w) {
					t.Errorf("CustomPatterns() = %v, missing %q", got, w)
				}
			}
			// The taxonomy contributes no bare pattern.
			for _, bare := range []string{joinPattern(genreBase), joinPattern(genreBase) + "/"} {
				if slices.Contains(got, bare) {
					t.Errorf("CustomPatterns() contains bare taxonomy pattern %q", bare)
				}
			}
			// A type without an archive contributes no bare pattern.
			for _, bare := range []string{joinPattern(volumeBase), joinPattern(volumeBase) + "/"} {
				if slices.Contains(got, bare) {
					t.Errorf("CustomPatterns() contains bare pattern %q for an archive-less type", bare)
				}
			}
		})
	}
}

// Requirement 7.5: chi overwrites a duplicate pattern silently, so the set must
// contain none -- asserted the way ArchivePatterns' set is.
func TestCustomPathPatternsHaveNoDuplicates(t *testing.T) {
	types := []TypeRoute{
		{Name: "book", Slug: "books", ArchiveSlug: "library", WithFront: true},
		{Name: "gadget", Slug: "gadgets", ArchiveSlug: "gadgets"},
		{Name: "volume", Slug: "volumes", Hierarchical: true},
	}
	taxes := []TaxRoute{
		{Name: "genre", Slug: "genre", WithFront: true, Hierarchical: true},
		{Name: "label", Slug: "labels"},
	}
	for _, structure := range []string{"", "/%postname%/", "/blog/%postname%/", "/blog/%year%/%monthnum%/%postname%/"} {
		t.Run(structure, func(t *testing.T) {
			got := cpStructure(t, structure, types, taxes).CustomPatterns()
			if len(got) == 0 {
				t.Fatal("CustomPatterns() is empty with entries registered")
			}
			seen := make(map[string]bool, len(got))
			for _, p := range got {
				if seen[p] {
					t.Errorf("pattern %q appears more than once in %v", p, got)
				}
				seen[p] = true
			}
		})
	}
}
