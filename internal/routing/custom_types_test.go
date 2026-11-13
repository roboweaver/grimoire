// This file is in package routing rather than routing_test because it asserts
// on the unexported customTypes/customTaxes fields (the copy-and-clone contract
// of WithCustomTypes) and, like front_internal_test.go, on helpers whose output
// is only observable from inside the package.
//
// M12 task 4.1: the route value types and helpers. Each test below is named for
// the contract it pins. The last group re-asserts today's archive output so the
// frontIf refactor of archivePrefix is shown to change nothing.
package routing

import (
	"reflect"
	"slices"
	"testing"
)

const (
	ctPostName      = "/%postname%/"
	ctPostNameNoSl  = "/%postname%"
	ctBlogDated     = "/blog/%year%/%monthnum%/%postname%/"
	ctBlogNews      = "/blog/news/%postname%/"
	ctPostIDOnly    = "/%post_id%/"
	ctNumericPreset = "/archives/%post_id%"
)

func ctParse(t *testing.T, structure, categoryBase, tagBase string) Structure {
	t.Helper()
	s, err := Parse(structure, categoryBase, tagBase)
	if err != nil {
		t.Fatalf("Parse(%q, %q, %q) error: %v", structure, categoryBase, tagBase, err)
	}
	return s
}

func ctTypes() []TypeRoute {
	return []TypeRoute{
		{Name: "book", Slug: "books", ArchiveSlug: "library", WithFront: true},
		{Name: "volume", Slug: "volumes", Hierarchical: true},
	}
}

func ctTaxes() []TaxRoute {
	return []TaxRoute{{Name: "genre", Slug: "genre", WithFront: true, Hierarchical: true}}
}

// ctCorpus is a spread of paths over every row of the precedence table plus
// shapes the custom rows will later claim, so an accidental behavior change in
// WithCustomTypes(nil, nil) cannot hide in an unexercised row.
var ctCorpus = []string{
	"/", "/hello", "/hello/", "/books/dune/", "/books/dune", "/books", "/library/",
	"/genre/scifi/", "/genre", "/blog/books/dune/", "/blog/library/",
	"/category/news", "/category/news/local/", "/blog/category/news/",
	"/tag/go", "/tag/go/", "/blog/tag/go/", "/author/rob", "/blog/author/rob/",
	"/2024", "/2024/03/", "/2024/03/15/", "/blog/2024/03/", "/date/2024/",
	"/2024/03/15/hello/", "/blog/2024/03/hello/", "/archives/42", "/42",
	"/parent/child/", "/sections/news/", "/topics/go",
}

type ctStructCfg struct {
	structure, cat, tag string
}

var ctStructures = []ctStructCfg{
	{"", "", ""},
	{ctPostName, "", ""},
	{ctPostNameNoSl, "", ""},
	{ctBlogDated, "", ""},
	{ctBlogDated, "sections", "topics"},
	{ctBlogNews, "", ""},
	{ctPostIDOnly, "", ""},
	{ctNumericPreset, "", ""},
	{"/%year%/%monthnum%/%postname%/", "", ""},
}

// Requirement 7.3, 4.4. WithCustomTypes returns a copy: the receiver is
// byte-for-byte what Parse built, the copy carries the given entries, and
// neither shares a backing array with the caller's slices.
func TestCustomTypesWithCustomTypesReturnsCopy(t *testing.T) {
	s := ctParse(t, ctBlogDated, "", "")
	pristine := ctParse(t, ctBlogDated, "", "")

	types, taxes := ctTypes(), ctTaxes()
	got := s.WithCustomTypes(types, taxes)

	if !reflect.DeepEqual(s, pristine) {
		t.Fatalf("WithCustomTypes modified its receiver:\n got %+v\nwant %+v", s, pristine)
	}
	if len(s.customTypes) != 0 || len(s.customTaxes) != 0 {
		t.Errorf("receiver carries custom entries after WithCustomTypes: types=%v taxes=%v",
			s.customTypes, s.customTaxes)
	}
	if !slices.Equal(got.customTypes, ctTypes()) {
		t.Errorf("copy customTypes = %v, want %v", got.customTypes, ctTypes())
	}
	if !slices.Equal(got.customTaxes, ctTaxes()) {
		t.Errorf("copy customTaxes = %v, want %v", got.customTaxes, ctTaxes())
	}

	// The copy cloned the inputs: scribbling on the caller's slices afterwards
	// must not reach it (the registry owns those slices and may reuse them).
	types[0].Slug = "mutated"
	taxes[0].Slug = "mutated"
	if got.customTypes[0].Slug != "books" || got.customTaxes[0].Slug != "genre" {
		t.Errorf("copy aliases the caller's slices: types[0].Slug=%q taxes[0].Slug=%q",
			got.customTypes[0].Slug, got.customTaxes[0].Slug)
	}

	// Everything else the Structure derived is carried over unchanged.
	if got.Raw != s.Raw || got.Flat != s.Flat || got.TrailingSlash != s.TrailingSlash ||
		got.CategoryBase != s.CategoryBase || got.TagBase != s.TagBase {
		t.Errorf("copy lost parsed fields: got %+v, want base %+v", got, s)
	}
	assertFront(t, got.Front(), s.Front())
	if !slices.Equal(got.ArchivePatterns(), s.ArchivePatterns()) {
		t.Errorf("copy ArchivePatterns = %q, want %q", got.ArchivePatterns(), s.ArchivePatterns())
	}
}

func TestCustomTypesHasArchive(t *testing.T) {
	if !(TypeRoute{Name: "book", Slug: "books", ArchiveSlug: "library"}).HasArchive() {
		t.Error("HasArchive() = false for a type with an ArchiveSlug")
	}
	if (TypeRoute{Name: "book", Slug: "books"}).HasArchive() {
		t.Error("HasArchive() = true for a type with no ArchiveSlug")
	}
}

// Requirement 7.3. Registering nothing is observably the identity: the same
// Target for every path, the same chi patterns, the same Notes.
func TestCustomTypesNilRegistryIsObservablyIdentical(t *testing.T) {
	for _, cfg := range ctStructures {
		s := ctParse(t, cfg.structure, cfg.cat, cfg.tag)
		got := s.WithCustomTypes(nil, nil)

		for _, p := range ctCorpus {
			if want, have := s.Classify(p), got.Classify(p); !reflect.DeepEqual(want, have) {
				t.Errorf("structure %q cat %q tag %q: Classify(%q) = %+v after WithCustomTypes(nil, nil), want %+v",
					cfg.structure, cfg.cat, cfg.tag, p, have, want)
			}
		}
		if want, have := s.ArchivePatterns(), got.ArchivePatterns(); !slices.Equal(want, have) {
			t.Errorf("structure %q: ArchivePatterns = %q after WithCustomTypes(nil, nil), want %q",
				cfg.structure, have, want)
		}
		if want, have := s.Notes(), got.Notes(); !slices.Equal(want, have) {
			t.Errorf("structure %q: Notes = %q after WithCustomTypes(nil, nil), want %q",
				cfg.structure, have, want)
		}
		if got.Flat != s.Flat || got.TrailingSlash != s.TrailingSlash || got.Raw != s.Raw {
			t.Errorf("structure %q: parsed fields changed by WithCustomTypes(nil, nil)", cfg.structure)
		}
		if len(got.customTypes) != 0 || len(got.customTaxes) != 0 {
			t.Errorf("structure %q: WithCustomTypes(nil, nil) registered entries: %v %v",
				cfg.structure, got.customTypes, got.customTaxes)
		}
	}
}

// Requirement 7.3 again, for the flat fallback: Parse returns a usable flat
// Structure alongside ErrUnsupported and WithCustomTypes(nil, nil) must leave it
// alone too.
func TestCustomTypesNilRegistryOnUnsupportedFallback(t *testing.T) {
	s, err := Parse("/%category%/%postname%/", "", "")
	if err == nil {
		t.Fatal("Parse accepted an unsupported token; expected ErrUnsupported")
	}
	got := s.WithCustomTypes(nil, nil)
	for _, p := range ctCorpus {
		if want, have := s.Classify(p), got.Classify(p); !reflect.DeepEqual(want, have) {
			t.Errorf("Classify(%q) = %+v, want %+v", p, have, want)
		}
	}
	if !slices.Equal(s.Notes(), got.Notes()) {
		t.Errorf("Notes = %q, want %q", got.Notes(), s.Notes())
	}
}

// Front returns a defensive copy: a caller scribbling on the result cannot
// move where the front sits for the next caller (or for Classify).
func TestCustomTypesFrontIsDefensiveCopy(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		want      []string
	}{
		{"one literal", ctBlogDated, []string{"blog"}},
		{"two literals", ctBlogNews, []string{"blog", "news"}},
		{"no front", ctPostName, nil},
		{"flat", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ctParse(t, tc.structure, "", "")
			assertFront(t, s.Front(), tc.want)

			got := s.Front()
			for i := range got {
				got[i] = "mutated"
			}
			got = append(got, "extra")
			_ = got
			assertFront(t, s.Front(), tc.want)

			// The mutation is also invisible to path construction.
			if len(tc.want) > 0 {
				if path := s.CategoryPath([]string{"news"}); path[:len("/"+tc.want[0])] != "/"+tc.want[0] {
					t.Errorf("CategoryPath after mutating Front() result = %q, front lost", path)
				}
			}
		})
	}
}

// Requirement 7.2. ReservedBases is the category, tag and author sequences, in
// that order, each exactly archivePrefix + baseSegments -- the same two helpers
// the constructors and ArchivePatterns call.
func TestCustomTypesReservedBases(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		cat, tag  string
		want      [][]string
	}{
		{
			name:      "flat has no front and the default bases",
			structure: "",
			want:      [][]string{{"category"}, {"tag"}, {"author"}},
		},
		{
			name:      "no-front structure",
			structure: ctPostName,
			want:      [][]string{{"category"}, {"tag"}, {"author"}},
		},
		{
			name:      "front applies to default category and tag and to author",
			structure: ctBlogDated,
			want:      [][]string{{"blog", "category"}, {"blog", "tag"}, {"blog", "author"}},
		},
		{
			name:      "custom category base drops the front, tag and author keep it",
			structure: ctBlogDated,
			cat:       "sections",
			want:      [][]string{{"sections"}, {"blog", "tag"}, {"blog", "author"}},
		},
		{
			name:      "custom tag base drops the front for tag only",
			structure: ctBlogDated,
			tag:       "topics",
			want:      [][]string{{"blog", "category"}, {"topics"}, {"blog", "author"}},
		},
		{
			name:      "custom category base without a front",
			structure: ctPostName,
			cat:       "sections",
			want:      [][]string{{"sections"}, {"tag"}, {"author"}},
		},
		{
			name:      "multi-segment category base is split",
			structure: ctBlogNews,
			cat:       "topics/news",
			want:      [][]string{{"topics", "news"}, {"blog", "news", "tag"}, {"blog", "news", "author"}},
		},
		{
			name:      "explicit default category base counts as set and drops the front",
			structure: ctBlogDated,
			cat:       "category",
			want:      [][]string{{"category"}, {"blog", "tag"}, {"blog", "author"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ctParse(t, tc.structure, tc.cat, tc.tag)
			got := s.ReservedBases()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ReservedBases() = %q, want %q", got, tc.want)
			}

			// Built by the same helpers: the sequences equal what the three
			// constructors' own prefix+base computation yields.
			wantHelpers := [][]string{
				append(s.archivePrefix(archiveCategory), baseSegments(s.CategoryBase)...),
				append(s.archivePrefix(archiveTag), baseSegments(s.TagBase)...),
				append(s.archivePrefix(archiveAuthor), AuthorBase),
			}
			if !reflect.DeepEqual(got, wantHelpers) {
				t.Errorf("ReservedBases() = %q, want archivePrefix+baseSegments %q", got, wantHelpers)
			}

			// Defensive copy: mutating the result changes nothing for the next call.
			for _, seq := range got {
				for i := range seq {
					seq[i] = "mutated"
				}
			}
			if again := s.ReservedBases(); !reflect.DeepEqual(again, tc.want) {
				t.Errorf("ReservedBases() after mutating a prior result = %q, want %q", again, tc.want)
			}
		})
	}
}

// Requirement 4.4, 7.2. CustomBase prepends the front only when withFront, and
// a Flat structure has no front so withFront is a no-op there.
func TestCustomTypesCustomBase(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		slug      string
		withFront bool
		want      []string
	}{
		{"front with with_front", ctBlogDated, "books", true, []string{"blog", "books"}},
		{"front without with_front", ctBlogDated, "books", false, []string{"books"}},
		{"two-segment front with with_front", ctBlogNews, "books", true, []string{"blog", "news", "books"}},
		{"two-segment front without with_front", ctBlogNews, "books", false, []string{"books"}},
		{"no front structure with with_front", ctPostName, "books", true, []string{"books"}},
		{"no front structure without with_front", ctPostName, "books", false, []string{"books"}},
		{"flat with with_front is a no-op", "", "books", true, []string{"books"}},
		{"flat without with_front", "", "books", false, []string{"books"}},
		{"unsupported fallback is flat", "/%category%/%postname%/", "books", true, []string{"books"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := Parse(tc.structure, "", "")
			got := s.CustomBase(tc.slug, tc.withFront)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("CustomBase(%q, %v) = %q, want %q", tc.slug, tc.withFront, got, tc.want)
			}

			// Defensive copy: the result never aliases the structure's front.
			for i := range got {
				got[i] = "mutated"
			}
			got = append(got, "extra")
			_ = got
			if again := s.CustomBase(tc.slug, tc.withFront); !slices.Equal(again, tc.want) {
				t.Errorf("CustomBase after mutating a prior result = %q, want %q", again, tc.want)
			}
			assertFront(t, s.Front(), ctFrontOf(tc.structure))
		})
	}
}

func ctFrontOf(structure string) []string {
	s, _ := Parse(structure, "", "")
	return slices.Clone(s.front)
}

// CustomBase is independent of what has been registered: the receiver's and the
// copy's answers agree.
func TestCustomTypesCustomBaseSameOnCopy(t *testing.T) {
	s := ctParse(t, ctBlogDated, "", "")
	c := s.WithCustomTypes(ctTypes(), ctTaxes())
	for _, wf := range []bool{true, false} {
		if a, b := s.CustomBase("books", wf), c.CustomBase("books", wf); !slices.Equal(a, b) {
			t.Errorf("CustomBase(books, %v): receiver %q vs copy %q", wf, a, b)
		}
	}
}

// Requirement 7.3, and the frontIf refactor: archivePrefix now delegates the
// "carry the front" decision to frontIf, and its output must be unchanged. These
// vectors were taken from the pre-refactor archive tests and are re-asserted
// here, in this file, so the refactor commit is proved a no-op on its own.
func TestCustomTypesFrontIfRefactorLeavesArchiveOutputUnchanged(t *testing.T) {
	type pathCase struct {
		name string
		cfg  ctStructCfg
		got  func(Structure) string
		want string
	}
	cases := []pathCase{
		{"flat category", ctStructCfg{"", "", ""}, func(s Structure) string { return s.CategoryPath([]string{"news"}) }, "/category/news"},
		{"flat tag", ctStructCfg{"", "", ""}, func(s Structure) string { return s.TagPath("go") }, "/tag/go"},
		{"flat author", ctStructCfg{"", "", ""}, func(s Structure) string { return s.AuthorPath("rob") }, "/author/rob"},
		{"flat date is empty", ctStructCfg{"", "", ""}, func(s Structure) string { return s.DatePath(DateRef{Year: 2024}) }, ""},
		{"postname category", ctStructCfg{ctPostName, "", ""}, func(s Structure) string { return s.CategoryPath([]string{"news", "local"}) }, "/category/news/local/"},
		{"front category", ctStructCfg{ctBlogDated, "", ""}, func(s Structure) string { return s.CategoryPath([]string{"news", "local"}) }, "/blog/category/news/local/"},
		{"front tag", ctStructCfg{ctBlogDated, "", ""}, func(s Structure) string { return s.TagPath("go") }, "/blog/tag/go/"},
		{"front author", ctStructCfg{ctBlogDated, "", ""}, func(s Structure) string { return s.AuthorPath("rob") }, "/blog/author/rob/"},
		{"front date month", ctStructCfg{ctBlogDated, "", ""}, func(s Structure) string { return s.DatePath(DateRef{Year: 2024, Month: 3}) }, "/blog/2024/03/"},
		{"two-segment front category", ctStructCfg{ctBlogNews, "", ""}, func(s Structure) string { return s.CategoryPath([]string{"a"}) }, "/blog/news/category/a/"},
		{"custom category base drops front", ctStructCfg{ctBlogDated, "sections", ""}, func(s Structure) string { return s.CategoryPath([]string{"news"}) }, "/sections/news/"},
		{"custom category base leaves tag front", ctStructCfg{ctBlogDated, "sections", ""}, func(s Structure) string { return s.TagPath("go") }, "/blog/tag/go/"},
		{"custom tag base drops front", ctStructCfg{ctBlogDated, "", "topics"}, func(s Structure) string { return s.TagPath("go") }, "/topics/go/"},
		{"custom tag base leaves author front", ctStructCfg{ctBlogDated, "", "topics"}, func(s Structure) string { return s.AuthorPath("rob") }, "/blog/author/rob/"},
		{"explicit default base counts as set", ctStructCfg{ctBlogDated, "category", ""}, func(s Structure) string { return s.CategoryPath([]string{"news"}) }, "/category/news/"},
		{"post_id date gets date/ prefix", ctStructCfg{ctPostIDOnly, "", ""}, func(s Structure) string { return s.DatePath(DateRef{Year: 2024}) }, "/date/2024/"},
		{"post_id date gets date/ prefix after no front", ctStructCfg{ctPostIDOnly, "", ""}, func(s Structure) string { return s.DatePath(DateRef{Year: 2024, Month: 3, Day: 5}) }, "/date/2024/03/05/"},
		{"slash-less structure drops trailing slash", ctStructCfg{ctPostNameNoSl, "", ""}, func(s Structure) string { return s.CategoryPath([]string{"news"}) }, "/category/news"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ctParse(t, tc.cfg.structure, tc.cfg.cat, tc.cfg.tag)
			if got := tc.got(s); got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
			// The same path is produced by a copy that registered entries.
			c := s.WithCustomTypes(ctTypes(), ctTaxes())
			if got := tc.got(c); got != tc.want {
				t.Errorf("path on WithCustomTypes copy = %q, want %q", got, tc.want)
			}
		})
	}

	// Classify, row by row for the archive kinds.
	classify := []struct {
		name string
		cfg  ctStructCfg
		path string
		want Target
	}{
		{"category", ctStructCfg{ctBlogDated, "", ""}, "/blog/category/news/local/",
			Target{Kind: KindCategory, Segments: []string{"news", "local"}, TrailingSlash: true}},
		{"category custom base", ctStructCfg{ctBlogDated, "sections", ""}, "/sections/news/",
			Target{Kind: KindCategory, Segments: []string{"news"}, TrailingSlash: true}},
		{"tag", ctStructCfg{ctBlogDated, "", ""}, "/blog/tag/go/",
			Target{Kind: KindTag, Slug: "go", TrailingSlash: true}},
		{"author", ctStructCfg{ctBlogDated, "", ""}, "/blog/author/rob",
			Target{Kind: KindAuthor, Slug: "rob"}},
		{"date", ctStructCfg{ctBlogDated, "", ""}, "/blog/2024/03/",
			Target{Kind: KindDate, Date: DateRef{Year: 2024, Month: 3}, TrailingSlash: true}},
		{"date with post_id prefix", ctStructCfg{ctPostIDOnly, "", ""}, "/date/2024/",
			Target{Kind: KindDate, Date: DateRef{Year: 2024}, TrailingSlash: true}},
		{"flat category", ctStructCfg{"", "", ""}, "/category/news",
			Target{Kind: KindCategory, Segments: []string{"news"}}},
		{"flat tag", ctStructCfg{"", "", ""}, "/tag/go",
			Target{Kind: KindTag, Slug: "go"}},
		{"flat author", ctStructCfg{"", "", ""}, "/author/rob",
			Target{Kind: KindAuthor, Slug: "rob"}},
	}
	for _, tc := range classify {
		t.Run("classify "+tc.name, func(t *testing.T) {
			s := ctParse(t, tc.cfg.structure, tc.cfg.cat, tc.cfg.tag)
			for label, st := range map[string]Structure{
				"parsed": s,
				"copy":   s.WithCustomTypes(ctTypes(), ctTaxes()),
			} {
				got := st.Classify(tc.path)
				if got.Kind != tc.want.Kind || got.Slug != tc.want.Slug || got.Date != tc.want.Date ||
					!slices.Equal(got.Segments, tc.want.Segments) || got.TrailingSlash != tc.want.TrailingSlash {
					t.Errorf("%s: Classify(%q) = %+v, want %+v", label, tc.path, got, tc.want)
				}
			}
		})
	}

	// ArchivePatterns, whole sets in order.
	patterns := []struct {
		name string
		cfg  ctStructCfg
		want []string
	}{
		{"flat", ctStructCfg{"", "", ""}, []string{
			"/category/*",
			"/tag/{slug}", "/tag/{slug}/",
			"/author/{nicename}", "/author/{nicename}/",
		}},
		{"front", ctStructCfg{ctBlogDated, "", ""}, []string{
			"/blog/category/*",
			"/blog/tag/{slug}", "/blog/tag/{slug}/",
			"/blog/author/{nicename}", "/blog/author/{nicename}/",
			"/blog/{year}", "/blog/{year}/",
			"/blog/{year}/{monthnum}", "/blog/{year}/{monthnum}/",
			"/blog/{year}/{monthnum}/{day}", "/blog/{year}/{monthnum}/{day}/",
		}},
		{"custom category base", ctStructCfg{ctBlogDated, "sections", ""}, []string{
			"/sections/*",
			"/blog/tag/{slug}", "/blog/tag/{slug}/",
			"/blog/author/{nicename}", "/blog/author/{nicename}/",
			"/blog/{year}", "/blog/{year}/",
			"/blog/{year}/{monthnum}", "/blog/{year}/{monthnum}/",
			"/blog/{year}/{monthnum}/{day}", "/blog/{year}/{monthnum}/{day}/",
		}},
		{"post_id date prefix", ctStructCfg{ctPostIDOnly, "", ""}, []string{
			"/category/*",
			"/tag/{slug}", "/tag/{slug}/",
			"/author/{nicename}", "/author/{nicename}/",
			"/date/{year}", "/date/{year}/",
			"/date/{year}/{monthnum}", "/date/{year}/{monthnum}/",
			"/date/{year}/{monthnum}/{day}", "/date/{year}/{monthnum}/{day}/",
		}},
	}
	for _, tc := range patterns {
		t.Run("patterns "+tc.name, func(t *testing.T) {
			s := ctParse(t, tc.cfg.structure, tc.cfg.cat, tc.cfg.tag)
			if got := s.ArchivePatterns(); !slices.Equal(got, tc.want) {
				t.Errorf("ArchivePatterns() = %q, want %q", got, tc.want)
			}
			if got := s.WithCustomTypes(ctTypes(), ctTaxes()).ArchivePatterns(); !slices.Equal(got, tc.want) {
				t.Errorf("ArchivePatterns() on WithCustomTypes copy = %q, want %q", got, tc.want)
			}
		})
	}
}
