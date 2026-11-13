package routing_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// M12 task 4.5 (TDD RED): the classifier's two custom rows.
//
//   - Row 6.5, the custom ARCHIVE row, sits after the date row and BEFORE the
//     structure's own post-shape row 7 (D6 as amended; Finding 1; Req 7.8, 7.9).
//   - Row 7.2, the custom SINGLE then custom TERM rows, sit after row 7 and
//     BEFORE the M15 KindPage row (Req 7.4).
//
// What each Kind carries (resolved from design.md Component 3):
//
//	KindCustomPost    TypeName = registry key, Segments = the ancestry below the base
//	KindCustomTerm    TypeName = registry key, Segments = the ancestry below the base
//	KindCustomArchive TypeName = registry key, Segments = nil (the match is exact,
//	                  there is nothing below the base to carry)
//
// Slug stays unset for all three: it documents "the tag slug or the author
// nicename", and the registry key is neither.

// customKindName keeps failure messages readable; kindName in classify_test.go
// predates the custom Kinds and must not be edited from here.
func customKindName(k routing.Kind) string {
	switch k {
	case routing.KindCustomPost:
		return "KindCustomPost"
	case routing.KindCustomArchive:
		return "KindCustomArchive"
	case routing.KindCustomTerm:
		return "KindCustomTerm"
	default:
		return kindName(k)
	}
}

const (
	ccFrontBlog   = "/blog/%postname%/"
	ccDated       = "/%year%/%monthnum%/%postname%/"
	ccBlogNewsPS  = "/blog/news/%postname%/"
	ccPostNameNoS = "/%postname%"
)

// ccParse parses a structure with the default category and tag bases and
// attaches the given entries.
func ccParse(t *testing.T, structure string, types []routing.TypeRoute, taxes []routing.TaxRoute) routing.Structure {
	t.Helper()
	s, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", structure, err)
	}
	return s.WithCustomTypes(types, taxes)
}

// ccWant is the expected classification of one path.
type ccWant struct {
	kind     routing.Kind
	typeName string
	segments []string
	post     routing.Ref // KindPost only
	date     routing.DateRef
}

// ccAssert checks one Classify result against want, including TrailingSlash.
func ccAssert(t *testing.T, s routing.Structure, path string, want ccWant) {
	t.Helper()
	got := s.Classify(path)
	if got.Kind != want.kind {
		t.Fatalf("Classify(%q) Kind = %s, want %s (structure %q)",
			path, customKindName(got.Kind), customKindName(want.kind), s.Raw)
	}
	if got.TypeName != want.typeName {
		t.Errorf("Classify(%q) TypeName = %q, want %q", path, got.TypeName, want.typeName)
	}
	switch want.kind {
	case routing.KindCustomPost, routing.KindCustomTerm:
		if !slices.Equal(got.Segments, want.segments) {
			t.Errorf("Classify(%q) Segments = %q, want %q", path, got.Segments, want.segments)
		}
	case routing.KindCustomArchive:
		if len(got.Segments) != 0 {
			t.Errorf("Classify(%q) Segments = %q, want none for an archive", path, got.Segments)
		}
	case routing.KindPage:
		if !slices.Equal(got.Segments, want.segments) {
			t.Errorf("Classify(%q) Segments = %q, want %q", path, got.Segments, want.segments)
		}
	case routing.KindPost:
		if got.Post != want.post {
			t.Errorf("Classify(%q) Post = %+v, want %+v", path, got.Post, want.post)
		}
	case routing.KindDate:
		if got.Date != want.date {
			t.Errorf("Classify(%q) Date = %+v, want %+v", path, got.Date, want.date)
		}
	}
	switch want.kind {
	case routing.KindCustomPost, routing.KindCustomArchive, routing.KindCustomTerm:
		if got.Slug != "" {
			t.Errorf("Classify(%q) Slug = %q, want empty (TypeName carries the key)", path, got.Slug)
		}
	}
	if wantSlash := path != "/" && strings.HasSuffix(path, "/"); got.TrailingSlash != wantSlash {
		t.Errorf("Classify(%q) TrailingSlash = %v, want %v", path, got.TrailingSlash, wantSlash)
	}
}

// ccRegistry is the shared entry set. Name differs from Slug on purpose (a type
// named "book" is served under /books/ with its archive at /library/), so a
// classifier that put the URL spelling into TypeName fails.
func ccRegistry() ([]routing.TypeRoute, []routing.TaxRoute) {
	types := []routing.TypeRoute{
		{Name: "book", Slug: "books", ArchiveSlug: "library"},
		{Name: "note", Slug: "notes"},
		{Name: "volume", Slug: "volumes", Hierarchical: true},
	}
	taxes := []routing.TaxRoute{
		{Name: "book_genre", Slug: "genre"},
		{Name: "book_series", Slug: "series", Hierarchical: true},
	}
	return types, taxes
}

// Requirement 4.1, 4.2, 5.1, 6.1: the three shapes, in both slash forms.
func TestClassifyCustomShapes(t *testing.T) {
	types, taxes := ccRegistry()
	for _, structure := range []string{"", presetPostName, ccPostNameNoS} {
		s := ccParse(t, structure, types, taxes)
		for _, slash := range []string{"", "/"} {
			t.Run("structure "+structure+" slash "+slash, func(t *testing.T) {
				ccAssert(t, s, "/books/dune"+slash,
					ccWant{kind: routing.KindCustomPost, typeName: "book", segments: []string{"dune"}})
				ccAssert(t, s, "/library"+slash,
					ccWant{kind: routing.KindCustomArchive, typeName: "book"})
				ccAssert(t, s, "/genre/scifi"+slash,
					ccWant{kind: routing.KindCustomTerm, typeName: "book_genre", segments: []string{"scifi"}})
			})
		}
	}
}

// Requirement 4.2, 6.1: hierarchical entries claim arbitrary depth and carry the
// whole ancestry, root first.
func TestClassifyCustomHierarchicalArbitraryDepth(t *testing.T) {
	types, taxes := ccRegistry()
	s := ccParse(t, presetPostName, types, taxes)
	for depth := 1; depth <= 6; depth++ {
		anc := make([]string, depth)
		for i := range anc {
			anc[i] = "n" + string(rune('a'+i))
		}
		path := "/volumes/" + strings.Join(anc, "/") + "/"
		ccAssert(t, s, path, ccWant{kind: routing.KindCustomPost, typeName: "volume", segments: anc})
		path = "/series/" + strings.Join(anc, "/") + "/"
		ccAssert(t, s, path, ccWant{kind: routing.KindCustomTerm, typeName: "book_series", segments: anc})
	}
}

// Requirement 4.1, 6.1: a path rooted at a NON-hierarchical entry's base names
// that entry or nothing. A deeper path is KindNone, not a fall-through to the
// M15 KindPage walk (the category-row precedent).
func TestClassifyCustomNonHierarchicalDeeperPathIsNone(t *testing.T) {
	types, taxes := ccRegistry()
	for _, structure := range []string{"", presetPostName, presetMonthAndName} {
		s := ccParse(t, structure, types, taxes)
		for _, path := range []string{
			"/books/dune/extra", "/books/dune/extra/", "/books/a/b/c/",
			"/notes/a/b/", "/genre/a/b/", "/genre/a/b/c",
		} {
			if got := s.Classify(path); got.Kind != routing.KindNone {
				t.Errorf("structure %q: Classify(%q) Kind = %s, want KindNone",
					structure, path, customKindName(got.Kind))
			}
		}
	}
}

// Design Component 3, "the bare form is a decision": the bare base of a type
// without an archive, the bare single base of a type whose archive is spelled
// differently, and the bare base of ANY taxonomy are not claimed. They fall to
// row 8, so a page slugged "genre" stays reachable at /genre.
func TestClassifyCustomBareBaseFallsThroughToRow8(t *testing.T) {
	types, taxes := ccRegistry()
	for _, structure := range []string{"", presetPostName, ccPostNameNoS, presetMonthAndName} {
		s := ccParse(t, structure, types, taxes)
		for _, slug := range []string{"notes", "volumes", "books", "genre", "series"} {
			for _, slash := range []string{"", "/"} {
				ccAssert(t, s, "/"+slug+slash,
					ccWant{kind: routing.KindPost, post: routing.Ref{Slug: slug}})
			}
		}
	}
}

// Under a front-ful structure the bare base is still unclaimed. With front
// "blog" and a with_front type, /blog/books is the structure's own post shape
// (row 7), which is KindPost, never a custom Kind.
func TestClassifyCustomBareBaseWithFrontIsNotCustom(t *testing.T) {
	types := []routing.TypeRoute{{Name: "note", Slug: "notes", WithFront: true}}
	taxes := []routing.TaxRoute{{Name: "topic", Slug: "topics", WithFront: true}}
	s := ccParse(t, ccFrontBlog, types, taxes)
	ccAssert(t, s, "/blog/notes/", ccWant{kind: routing.KindPost, post: routing.Ref{Slug: "notes"}})
	ccAssert(t, s, "/blog/topics/", ccWant{kind: routing.KindPost, post: routing.Ref{Slug: "topics"}})
	ccAssert(t, s, "/blog/notes/n1/",
		ccWant{kind: routing.KindCustomPost, typeName: "note", segments: []string{"n1"}})
	ccAssert(t, s, "/blog/topics/t1/",
		ccWant{kind: routing.KindCustomTerm, typeName: "topic", segments: []string{"t1"}})
}

// Nothing under the archive base is claimed by the archive row (the match is
// exact): /library/x falls through, and under /%postname%/ that is KindPage.
func TestClassifyCustomArchiveMatchIsExact(t *testing.T) {
	types, taxes := ccRegistry()
	s := ccParse(t, presetPostName, types, taxes)
	ccAssert(t, s, "/library/x/", ccWant{kind: routing.KindPage, segments: []string{"library", "x"}})
	// A with_front archive is not claimed without its front.
	front := ccParse(t, ccFrontBlog, []routing.TypeRoute{
		{Name: "book", Slug: "books", ArchiveSlug: "library", WithFront: true},
	}, nil)
	ccAssert(t, front, "/blog/library/", ccWant{kind: routing.KindCustomArchive, typeName: "book"})
	ccAssert(t, front, "/library/", ccWant{kind: routing.KindPost, post: routing.Ref{Slug: "library"}})
}

// Req 7.8, Finding 1. The archive row precedes the post-shape row, so under the
// two most common structures the archive slug is the archive, not a post.
func TestClassifyCustomArchiveBeatsPostShape(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		withFront bool
		path      string
	}{
		{"postname", presetPostName, false, "/library/"},
		{"postname with_front (front is empty)", presetPostName, true, "/library/"},
		{"postname slash-less", ccPostNameNoS, false, "/library"},
		{"blog front, with_front", ccFrontBlog, true, "/blog/library/"},
		{"blog front, without with_front", ccFrontBlog, false, "/library/"},
		{"flat", "", false, "/library"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ccParse(t, tc.structure, []routing.TypeRoute{
				{Name: "book", Slug: "books", ArchiveSlug: "library", WithFront: tc.withFront},
			}, nil)
			ccAssert(t, s, tc.path, ccWant{kind: routing.KindCustomArchive, typeName: "book"})
		})
	}
}

// Req 7.9. A post or a page slugged like the archive slug is unreachable at that
// URL: the same single-segment path that would be a post or a page today is the
// archive once the type is registered, and is KindPost without it.
func TestClassifyCustomArchiveShadowsSameSlugPostAndPage(t *testing.T) {
	types := []routing.TypeRoute{{Name: "book", Slug: "books", ArchiveSlug: "library"}}
	plain := ccParse(t, presetPostName, nil, nil)
	ccAssert(t, plain, "/library/", ccWant{kind: routing.KindPost, post: routing.Ref{Slug: "library"}})
	with := ccParse(t, presetPostName, types, nil)
	ccAssert(t, with, "/library/", ccWant{kind: routing.KindCustomArchive, typeName: "book"})
	ccAssert(t, with, "/library", ccWant{kind: routing.KindCustomArchive, typeName: "book"})
}

// The archive row sits AFTER the category, tag, author and date rows: a base
// segment names its own archive or nothing, and a year-shaped archive slug loses
// to the date row exactly as a post slugged "2024" does (Req 9.3).
func TestClassifyCustomArchiveLosesToEarlierRows(t *testing.T) {
	archive := func(slug string, withFront bool) []routing.TypeRoute {
		return []routing.TypeRoute{{Name: "book", Slug: "books", ArchiveSlug: slug, WithFront: withFront}}
	}
	t.Run("year-shaped slug loses to the date row", func(t *testing.T) {
		s := ccParse(t, presetPostName, archive("2024", false), nil)
		ccAssert(t, s, "/2024/", ccWant{kind: routing.KindDate, date: routing.DateRef{Year: 2024}})
	})
	t.Run("year-shaped slug under a front loses to the date row", func(t *testing.T) {
		s := ccParse(t, ccFrontBlog, archive("2024", true), nil)
		ccAssert(t, s, "/blog/2024/", ccWant{kind: routing.KindDate, date: routing.DateRef{Year: 2024}})
	})
	t.Run("category base wins", func(t *testing.T) {
		s := ccParse(t, presetPostName, archive("category", false), nil)
		ccAssert(t, s, "/category/", ccWant{kind: routing.KindNone})
	})
	t.Run("tag base wins", func(t *testing.T) {
		s := ccParse(t, presetPostName, archive("tag", false), nil)
		ccAssert(t, s, "/tag/", ccWant{kind: routing.KindNone})
	})
	t.Run("author base wins", func(t *testing.T) {
		s := ccParse(t, presetPostName, archive("author", false), nil)
		ccAssert(t, s, "/author/", ccWant{kind: routing.KindNone})
	})
	t.Run("a custom single base named tag loses to the tag row", func(t *testing.T) {
		s := ccParse(t, presetPostName, []routing.TypeRoute{{Name: "t", Slug: "tag"}}, nil)
		ccAssert(t, s, "/tag/go/", ccWant{kind: routing.KindTag})
	})
}

// Req 7.4, D6. Singles and terms sit after row 7 and before the KindPage row: a
// path that would be a two-segment page walk is claimed by the custom row, and
// the structure's own post shape still wins over a single when both match.
func TestClassifyCustomSinglesAndTermsPrecedeKindPage(t *testing.T) {
	types, taxes := ccRegistry()
	plain := ccParse(t, presetPostName, nil, nil)
	with := ccParse(t, presetPostName, types, taxes)
	for _, tc := range []struct {
		path string
		want ccWant
	}{
		{"/books/dune/", ccWant{kind: routing.KindCustomPost, typeName: "book", segments: []string{"dune"}}},
		{"/volumes/a/b/", ccWant{kind: routing.KindCustomPost, typeName: "volume", segments: []string{"a", "b"}}},
		{"/genre/scifi/", ccWant{kind: routing.KindCustomTerm, typeName: "book_genre", segments: []string{"scifi"}}},
		{"/series/a/b/", ccWant{kind: routing.KindCustomTerm, typeName: "book_series", segments: []string{"a", "b"}}},
	} {
		// Without the registry the very same path is the M15 page walk.
		segs := strings.Split(strings.Trim(tc.path, "/"), "/")
		ccAssert(t, plain, tc.path, ccWant{kind: routing.KindPage, segments: segs})
		ccAssert(t, with, tc.path, tc.want)
	}

	// Row 7 first: under /blog/news/%postname%/ the path /blog/news/x/ is the
	// structure's own post shape even when a registered single's base is "blog".
	// (The registry rejects such a base at startup; the classifier is still
	// specified to evaluate row 7 first.)
	s := ccParse(t, ccBlogNewsPS, []routing.TypeRoute{{Name: "b", Slug: "blog"}}, nil)
	ccAssert(t, s, "/blog/news/x/", ccWant{kind: routing.KindPost, post: routing.Ref{Slug: "x"}})
}

// ccEntry is one registered entry under test in the fixed-point matrix.
type ccEntry struct {
	name string
	// exactly one of typ/tax is meaningful
	typ *routing.TypeRoute
	tax *routing.TaxRoute
}

func ccEntries() []ccEntry {
	ty := func(t routing.TypeRoute) *routing.TypeRoute { return &t }
	tx := func(t routing.TaxRoute) *routing.TaxRoute { return &t }
	return []ccEntry{
		{name: "type flat no archive", typ: ty(routing.TypeRoute{Name: "note", Slug: "notes"})},
		{name: "type flat archive", typ: ty(routing.TypeRoute{Name: "book", Slug: "books", ArchiveSlug: "library"})},
		{name: "type same archive slug", typ: ty(routing.TypeRoute{Name: "gadget", Slug: "gadgets", ArchiveSlug: "gadgets"})},
		{name: "type with front archive", typ: ty(routing.TypeRoute{Name: "book", Slug: "books", ArchiveSlug: "library", WithFront: true})},
		{name: "type with front no archive", typ: ty(routing.TypeRoute{Name: "note", Slug: "notes", WithFront: true})},
		{name: "type hierarchical", typ: ty(routing.TypeRoute{Name: "volume", Slug: "volumes", Hierarchical: true})},
		{name: "type hierarchical front archive", typ: ty(routing.TypeRoute{Name: "volume", Slug: "volumes", ArchiveSlug: "shelf", Hierarchical: true, WithFront: true})},
		{name: "tax flat", tax: tx(routing.TaxRoute{Name: "book_genre", Slug: "genre"})},
		{name: "tax with front", tax: tx(routing.TaxRoute{Name: "book_genre", Slug: "genre", WithFront: true})},
		{name: "tax hierarchical", tax: tx(routing.TaxRoute{Name: "book_series", Slug: "series", Hierarchical: true})},
		{name: "tax hierarchical with front", tax: tx(routing.TaxRoute{Name: "book_series", Slug: "series", Hierarchical: true, WithFront: true})},
	}
}

// toggleSlash returns the other slash form of a non-root path.
func toggleSlash(p string) string {
	if strings.HasSuffix(p, "/") {
		return strings.TrimRight(p, "/")
	}
	return p + "/"
}

// Req 4.2, 5.1, 6.1, 7.1: the classifier/constructor fixed point over the finite
// matrix. For every structure, entry and accepted ancestry,
// Classify(constructor(...)) yields the matching Kind, the registry key as
// TypeName and the ancestry as Segments, in the canonical and the other slash
// form; and a constructor never returns a path Classify declines.
func TestClassifyCustomConstructorsAreClassifyFixedPoints(t *testing.T) {
	structures := []string{
		"", presetPostName, ccPostNameNoS, ccFrontBlog,
		presetMonthAndName, presetNumeric, ccBlogNewsPS,
	}
	hier := [][]string{{"a1"}, {"a1", "b2"}, {"a1", "b2", "c3"}}
	flat := [][]string{{"dune"}}
	for _, structure := range structures {
		for _, e := range ccEntries() {
			t.Run(structure+"/"+e.name, func(t *testing.T) {
				var types []routing.TypeRoute
				var taxes []routing.TaxRoute
				if e.typ != nil {
					types = []routing.TypeRoute{*e.typ}
				}
				if e.tax != nil {
					taxes = []routing.TaxRoute{*e.tax}
				}
				s := ccParse(t, structure, types, taxes)

				check := func(path string, want ccWant) {
					t.Helper()
					if path == "" {
						t.Fatalf("constructor returned no path for %+v", want)
					}
					ccAssert(t, s, path, want)
					other := toggleSlash(path)
					got := s.Classify(other)
					if got.Kind != want.kind || got.TypeName != want.typeName ||
						!slices.Equal(got.Segments, want.segments) {
						t.Errorf("Classify(%q) = %+v, want kind %s typeName %q segments %q (the non-canonical slash form of %q)",
							other, got, customKindName(want.kind), want.typeName, want.segments, path)
					}
				}

				if e.typ != nil {
					ancestries := flat
					if e.typ.Hierarchical {
						ancestries = hier
					}
					for _, anc := range ancestries {
						check(s.CustomPostPath(e.typ.Name, anc),
							ccWant{kind: routing.KindCustomPost, typeName: e.typ.Name, segments: anc})
					}
					if e.typ.HasArchive() {
						check(s.CustomArchivePath(e.typ.Name),
							ccWant{kind: routing.KindCustomArchive, typeName: e.typ.Name})
					} else if got := s.CustomArchivePath(e.typ.Name); got != "" {
						t.Errorf("CustomArchivePath for a type without an archive = %q, want empty", got)
					}
					if !e.typ.Hierarchical {
						if got := s.CustomPostPath(e.typ.Name, []string{"a", "b"}); got != "" {
							t.Errorf("CustomPostPath with two segments on a non-hierarchical type = %q, want empty", got)
						}
					}
				}
				if e.tax != nil {
					ancestries := flat
					if e.tax.Hierarchical {
						ancestries = hier
					}
					for _, anc := range ancestries {
						check(s.CustomTermPath(e.tax.Name, anc),
							ccWant{kind: routing.KindCustomTerm, typeName: e.tax.Name, segments: anc})
					}
				}
			})
		}
	}
}

// Req 7.2, 7.7. The classifier is pure and total (no DB, no error, no panic on
// odd input), deterministic, and independent of the order the entries were
// registered in, which is the order chi would see them.
func TestClassifyCustomIsPureTotalAndOrderIndependent(t *testing.T) {
	types, taxes := ccRegistry()
	reversedTypes := slices.Clone(types)
	slices.Reverse(reversedTypes)
	reversedTaxes := slices.Clone(taxes)
	slices.Reverse(reversedTaxes)

	paths := []string{
		"", "/", "//", "///", "/books", "/books/", "/books//dune", "//books/dune",
		"/books/dune/extra", "/library", "/library/", "/library/x", "/genre", "/genre/",
		"/genre/a/b", "/series/a/b/c", "/volumes", "/volumes/a/b/c/d", "/notes/n1",
		"/2024/", "/category/x", "/tag/", "/a/b/c", "/hello", "/%00/%zz",
	}
	for _, structure := range []string{"", presetPostName, ccFrontBlog, presetMonthAndName, presetNumeric} {
		a := ccParse(t, structure, types, taxes)
		b := ccParse(t, structure, reversedTypes, reversedTaxes)
		for _, p := range paths {
			first := a.Classify(p)
			if again := a.Classify(p); !reflect.DeepEqual(first, again) {
				t.Errorf("structure %q: Classify(%q) not deterministic: %+v then %+v", structure, p, first, again)
			}
			if rev := b.Classify(p); !reflect.DeepEqual(first, rev) {
				t.Errorf("structure %q: Classify(%q) depends on registration order: %+v vs %+v", structure, p, first, rev)
			}
			// Scribbling on a returned Segments must not leak into the next call.
			for i := range first.Segments {
				first.Segments[i] = "mutated"
			}
			if after := a.Classify(p); slices.Contains(after.Segments, "mutated") {
				t.Errorf("structure %q: Classify(%q) shares Segments storage between calls", structure, p)
			}
		}
	}
}

// Req 7.3. A Structure that never saw WithCustomTypes classifies exactly as it
// did before M12: the corpus below is drawn from the existing classify vectors
// with their pre-M12 answers, run once through the shared harness and once
// through structures that were handed an empty registry, and no custom Kind or
// TypeName ever appears.
func TestClassifyCustomZeroRegistryRegression(t *testing.T) {
	cases := []classifyCase{
		{name: "empty path", structure: presetPostName, path: "", want: routing.KindNone},
		{name: "root", structure: presetPostName, path: "/", want: routing.KindNone},
		{name: "category", structure: presetPostName, path: "/category/news/", want: routing.KindCategory, wantSegments: []string{"news"}},
		{name: "category nested slash-less", structure: presetPostName, path: "/category/news/local", want: routing.KindCategory, wantSegments: []string{"news", "local"}},
		{name: "bare category", structure: presetPostName, path: "/category/", want: routing.KindNone},
		{name: "tag", structure: presetPostName, path: "/tag/go/", want: routing.KindTag, wantSlug: "go"},
		{name: "deep tag", structure: presetPostName, path: "/tag/go/extra/", want: routing.KindNone},
		{name: "bare tag", structure: presetPostName, path: "/tag/", want: routing.KindNone},
		{name: "author", structure: presetPostName, path: "/author/alice/", want: routing.KindAuthor, wantSlug: "alice"},
		{name: "deep author", structure: presetPostName, path: "/author/alice/extra/", want: routing.KindNone},
		{name: "year", structure: presetPostName, path: "/2024/", want: routing.KindDate, wantDate: routing.DateRef{Year: 2024}},
		{name: "year month", structure: presetPostName, path: "/2024/05/", want: routing.KindDate, wantDate: routing.DateRef{Year: 2024, Month: 5}},
		{name: "full date", structure: presetPostName, path: "/2024/05/17/", want: routing.KindDate, wantDate: routing.DateRef{Year: 2024, Month: 5, Day: 17}},
		{name: "one digit month", structure: presetPostName, path: "/2024/5/", want: routing.KindPage, wantSegments: []string{"2024", "5"}},
		{name: "month 13", structure: presetPostName, path: "/2024/13/", want: routing.KindPage, wantSegments: []string{"2024", "13"}},
		{name: "post", structure: presetPostName, path: "/hello-world/", want: routing.KindPost, wantPost: routing.Ref{Slug: "hello-world"}},
		{name: "five digit slug", structure: presetPostName, path: "/12345/", want: routing.KindPost, wantPost: routing.Ref{Slug: "12345"}},
		{name: "dated post", structure: presetMonthAndName, path: "/2024/05/hello-world/", want: routing.KindPost, wantPost: routing.Ref{Year: 2024, Month: 5, Slug: "hello-world"}},
		{name: "flat slug under dated", structure: presetMonthAndName, path: "/hello-world/", want: routing.KindPost, wantPost: routing.Ref{Slug: "hello-world"}},
		{name: "deep path", structure: presetPostName, path: "/a/b/c/d/e/", want: routing.KindPage, wantSegments: []string{"a", "b", "c", "d", "e"}},
		// The paths the custom rows will later claim, with no registry.
		{name: "would-be single", structure: presetPostName, path: "/books/dune/", want: routing.KindPage, wantSegments: []string{"books", "dune"}},
		{name: "would-be archive", structure: presetPostName, path: "/library/", want: routing.KindPost, wantPost: routing.Ref{Slug: "library"}},
		{name: "would-be term", structure: presetPostName, path: "/genre/scifi/", want: routing.KindPage, wantSegments: []string{"genre", "scifi"}},
		{name: "would-be bare taxonomy", structure: presetPostName, path: "/genre", want: routing.KindPost, wantPost: routing.Ref{Slug: "genre"}},
		{name: "numeric preset post", structure: presetNumeric, path: "/archives/123", want: routing.KindPost, wantPost: routing.Ref{ID: 123}},
		{name: "flat year-shaped slug", structure: "", path: "/2024", want: routing.KindPost, wantPost: routing.Ref{Slug: "2024"}},
		{name: "flat deep path", structure: "", path: "/books/dune", want: routing.KindPage, wantSegments: []string{"books", "dune"}},
	}
	runClassifyCases(t, cases)

	for _, tc := range cases {
		plain, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", tc.structure, err)
		}
		want := plain.Classify(tc.path)
		if want.TypeName != "" {
			t.Errorf("%s: zero-registry Classify(%q) TypeName = %q, want empty", tc.name, tc.path, want.TypeName)
		}
		switch want.Kind {
		case routing.KindCustomPost, routing.KindCustomArchive, routing.KindCustomTerm:
			t.Errorf("%s: zero-registry Classify(%q) returned custom Kind %s", tc.name, tc.path, customKindName(want.Kind))
		}
		for label, s := range map[string]routing.Structure{
			"nil":   plain.WithCustomTypes(nil, nil),
			"empty": plain.WithCustomTypes([]routing.TypeRoute{}, []routing.TaxRoute{}),
		} {
			if got := s.Classify(tc.path); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: Classify(%q) after WithCustomTypes(%s) = %+v, want %+v", tc.name, tc.path, label, got, want)
			}
		}
	}
}
