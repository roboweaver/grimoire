package routing_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// classifyCase is one path evaluated against one configuration, asserting the
// Kind design.md's precedence table gives it and the payload that Kind carries.
//
// The payload is checked per Kind rather than field by field across the whole
// struct: a Target that named the right Kind and the wrong entity would pass a
// Kind-only assertion while sending the handler to a different archive, and a
// whole-struct comparison would instead over-constrain fields the table says
// nothing about for that row.
type classifyCase struct {
	name         string
	structure    string
	categoryBase string
	tagBase      string
	path         string

	want routing.Kind

	// Exactly one of these is read, selected by want: Segments for
	// KindCategory, Slug for KindTag and KindAuthor, Date for KindDate, Post
	// for KindPost. KindNone carries no payload.
	wantSegments []string
	wantSlug     string
	wantDate     routing.DateRef
	wantPost     routing.Ref
}

// kindNames keeps the failure messages readable without requiring Kind to carry
// a String method -- design.md specifies the constants and nothing more, and a
// test should not be the reason an exported method exists.
var kindNames = map[routing.Kind]string{
	routing.KindNone:     "KindNone",
	routing.KindPost:     "KindPost",
	routing.KindCategory: "KindCategory",
	routing.KindTag:      "KindTag",
	routing.KindAuthor:   "KindAuthor",
	routing.KindDate:     "KindDate",
}

func kindName(k routing.Kind) string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return "Kind(unknown)"
}

func runClassifyCases(t *testing.T, cases []classifyCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					tc.structure, tc.categoryBase, tc.tagBase, err)
			}

			got := s.Classify(tc.path)
			if got.Kind != tc.want {
				t.Fatalf("Classify(%q) Kind = %s, want %s "+
					"(structure %q, category_base %q, tag_base %q)",
					tc.path, kindName(got.Kind), kindName(tc.want),
					tc.structure, tc.categoryBase, tc.tagBase)
			}

			switch tc.want {
			case routing.KindCategory:
				// The base segment is consumed, so Segments is the ancestry the
				// handler walks through the taxonomy graph -- root first.
				if !slices.Equal(got.Segments, tc.wantSegments) {
					t.Errorf("Classify(%q) Segments = %q, want %q",
						tc.path, got.Segments, tc.wantSegments)
				}
			case routing.KindTag, routing.KindAuthor:
				if got.Slug != tc.wantSlug {
					t.Errorf("Classify(%q) Slug = %q, want %q",
						tc.path, got.Slug, tc.wantSlug)
				}
			case routing.KindDate:
				if got.Date != tc.wantDate {
					t.Errorf("Classify(%q) Date = %+v, want %+v",
						tc.path, got.Date, tc.wantDate)
				}
			case routing.KindPost:
				if got.Post != tc.wantPost {
					t.Errorf("Classify(%q) Post = %+v, want %+v",
						tc.path, got.Post, tc.wantPost)
				}
			case routing.KindNone:
				// Nothing to carry: the caller 404s.
				return
			}

			// TrailingSlash records the form the request arrived in, not the
			// canonical form, because that is what lets the handler canonicalise
			// without re-parsing the path. It is therefore derivable from the
			// path itself, and asserting it here is what keeps a classifier that
			// reports the *canonical* form from redirecting every already-
			// canonical archive URL to itself.
			wantSlash := tc.path != "/" && strings.HasSuffix(tc.path, "/")
			if got.TrailingSlash != wantSlash {
				t.Errorf("Classify(%q) TrailingSlash = %v, want %v",
					tc.path, got.TrailingSlash, wantSlash)
			}
		})
	}
}

// Requirement 9.2, and design.md's precedence table row by row.
//
// Precedence lives in this one pure function rather than in the order of eight
// r.Method calls, because chi resolves a collision at a parameter node silently
// in favour of whichever pattern was registered last (M9a probed this and
// recorded it in router.go's route-registration comment). Every ambiguous
// pattern is registered to one handler precisely so that which pattern chi picks
// has no observable effect -- which makes this table the only place precedence is
// decided, and the only place it can be reviewed.
//
// The rows are evaluated top to bottom and the first match wins, so each case is
// named for the row it exercises and the rows that must *not* have claimed it
// first. The configurations here carry no front, which isolates the ordering from
// the per-kind prefix rule tested in TestClassifyExpectedPrefixPerRow.
func TestClassifyPrecedenceTable(t *testing.T) {
	runClassifyCases(t, []classifyCase{
		// Row 1 -- the empty path and "/" address no archive and no post. The
		// "/" route is registered before the dispatcher and owns it (Req 9.6),
		// so claiming it here would take the home page.
		{
			name:      "row 1: empty path",
			structure: presetPostName,
			path:      "",
			want:      routing.KindNone,
		},
		{
			name:      "row 1: root path",
			structure: presetPostName,
			path:      "/",
			want:      routing.KindNone,
		},

		// Row 2 -- the category base plus at least one further segment. The
		// base is consumed and the rest is the ancestry, as given.
		{
			name:         "row 2: category base and one segment",
			structure:    presetPostName,
			path:         "/category/news/",
			want:         routing.KindCategory,
			wantSegments: []string{"news"},
		},
		{
			// Three levels, because a one-level case passes for an
			// implementation that reads only the segment after the base and a
			// two-level case passes for one that reads only the last two.
			name:         "row 2: category base and a three level ancestry",
			structure:    presetPostName,
			path:         "/category/news/local/towns/",
			want:         routing.KindCategory,
			wantSegments: []string{"news", "local", "towns"},
		},
		{
			// The non-canonical slash form still classifies, so the handler can
			// 301 it. chi matches the two forms as distinct routes, so a
			// classifier that recognised only one would 404 the other before any
			// redirect could happen (Req 2.7).
			name:         "row 2: category path in the non canonical slash form",
			structure:    presetPostName,
			path:         "/category/news",
			want:         routing.KindCategory,
			wantSegments: []string{"news"},
		},

		// Row 3 -- the bare base segment. Req 2.8: WordPress serves no category
		// index, and inventing one here would be an unspecified surface.
		{
			name:      "row 3: bare category base",
			structure: presetPostName,
			path:      "/category",
			want:      routing.KindNone,
		},
		{
			name:      "row 3: bare category base with a trailing slash",
			structure: presetPostName,
			path:      "/category/",
			want:      routing.KindNone,
		},

		// Row 4 -- the tag base plus exactly one segment. Req 5.2: post_tag is
		// non-hierarchical, so there is no ancestry to carry and a deeper path
		// is not a nested tag.
		{
			name:      "row 4: tag base and one segment",
			structure: presetPostName,
			path:      "/tag/go/",
			want:      routing.KindTag,
			wantSlug:  "go",
		},
		{
			name:      "row 4: multi segment tag path is not a nested tag",
			structure: presetPostName,
			path:      "/tag/go/extra/",
			want:      routing.KindNone,
		},
		{
			// The bare base is the one shape the table states for the category
			// row (row 3) and leaves implicit for the tag and author rows, so it
			// is spelled out here: a path rooted at a base segment either names
			// that archive or names nothing, and it never falls through to the
			// post rows. Req 9.4 and design.md's consequences list both say so
			// directly -- "a post slugged exactly category, tag or author (or the
			// configured base) is unreachable" under a single-segment structure --
			// and the fall-through reading would contradict them, because
			// /%postname%/ matches "/tag/" perfectly well with postname "tag".
			name:      "row 4: bare tag base",
			structure: presetPostName,
			path:      "/tag/",
			want:      routing.KindNone,
		},

		// Row 5 -- the author base, which is the constant AuthorBase and which
		// no option moves (Req 4.3).
		{
			name:      "row 5: author base and one segment",
			structure: presetPostName,
			path:      "/author/alice/",
			want:      routing.KindAuthor,
			wantSlug:  "alice",
		},
		{
			name:      "row 5: deeper author path",
			structure: presetPostName,
			path:      "/author/alice/extra/",
			want:      routing.KindNone,
		},

		// Row 6 -- 1 to 3 bare segments at WordPress's widths, forming a real
		// calendar date, **before** the post rows.
		{
			name:      "row 6: year archive",
			structure: presetPostName,
			path:      "/2024/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024},
		},
		{
			name:      "row 6: year and month archive",
			structure: presetPostName,
			path:      "/2024/05/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024, Month: 5},
		},
		{
			name:      "row 6: year month and day archive",
			structure: presetPostName,
			path:      "/2024/05/17/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024, Month: 5, Day: 17},
		},
		{
			// Req 6.2: WordPress zero-pads, so a 1-digit month is not a month.
			// Falling through to the post rows is not an option either -- a
			// single-segment structure cannot match two segments -- so this is
			// row 9.
			name:      "row 6: a one digit month does not match the date widths",
			structure: presetPostName,
			path:      "/2024/5/",
			want:      routing.KindNone,
		},
		{
			// Req 6.3: an impossible date 404s rather than reaching a query.
			name:      "row 6: month 13 is not a real date",
			structure: presetPostName,
			path:      "/2024/13/",
			want:      routing.KindNone,
		},
		{
			name:      "row 6: february 30 is not a real date",
			structure: presetPostName,
			path:      "/2024/02/30/",
			want:      routing.KindNone,
		},

		// Row 7 -- the structure's own shape, which is M9a's unchanged post
		// resolution. The structure carries its own front, so no prefix applies.
		{
			name:      "row 7: the structure's shape resolves a post",
			structure: presetPostName,
			path:      "/hello-world/",
			want:      routing.KindPost,
			wantPost:  routing.Ref{Slug: "hello-world"},
		},
		{
			// A numeric single segment that is not 4 digits wide is not a year,
			// so row 6 declines it and the structure claims it as a slug. This
			// is the boundary of Req 9.3's consequence, not an exception to it.
			name:      "row 7: a five digit segment is a slug, not a year",
			structure: presetPostName,
			path:      "/12345/",
			want:      routing.KindPost,
			wantPost:  routing.Ref{Slug: "12345"},
		},
		{
			name:      "row 7: a multi segment structure resolves its own shape",
			structure: presetMonthAndName,
			path:      "/2024/05/hello-world/",
			want:      routing.KindPost,
			wantPost:  routing.Ref{Year: 2024, Month: 5, Slug: "hello-world"},
		},

		// Row 8 -- the flat /{slug} fallback. This row is what makes M9a's
		// flat-to-canonical 301 reachable: a single segment that the structure
		// itself cannot match still names a post, and the handler redirects it to
		// Canonical.
		{
			name:      "row 8: single segment under a multi segment structure",
			structure: presetMonthAndName,
			path:      "/hello-world/",
			want:      routing.KindPost,
			wantPost:  routing.Ref{Slug: "hello-world"},
		},

		// Row 9 -- everything else.
		{
			name:      "row 9: an unrecognised deep path",
			structure: presetPostName,
			path:      "/a/b/c/d/e/",
			want:      routing.KindNone,
		},
	})
}

// Requirement 9.3. The classifier resolves a date archive **before** a post,
// matching WordPress's own rewrite-rule ordering.
//
// The consequence is a real loss of a URL and is documented as matching
// WordPress rather than left to be discovered: a post whose slug is a bare
// 4-digit number is unreachable under /%postname%/, and one slugged with a bare
// 2-digit number is unreachable at the day position of
// /%year%/%monthnum%/%postname%/. Both shapes are asserted here, and both assert
// what the path is **not**, because "it is a date" and "it is not the post" are
// the same claim only while the table's ordering holds -- and the ordering is the
// thing under test.
func TestClassifyResolvesDatesBeforePosts(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		path      string
		wantDate  routing.DateRef
		// wouldBePost is the post the path would resolve to if the date row ran
		// after the post rows instead of before them, named so the failure
		// message says which URL the ordering gave away.
		wouldBePost routing.Ref
	}{
		{
			// A post titled "2024" gets the slug "2024", so this is not a
			// contrived shape.
			name:        "bare four digit segment under postname",
			structure:   presetPostName,
			path:        "/2024/",
			wantDate:    routing.DateRef{Year: 2024},
			wouldBePost: routing.Ref{Slug: "2024"},
		},
		{
			// Three numeric segments match /%year%/%monthnum%/%postname%/'s
			// shape exactly -- postname "17" is a legal slug -- so this row is
			// decided by ordering alone.
			name:        "three numeric segments under month and name",
			structure:   presetMonthAndName,
			path:        "/2024/05/17/",
			wantDate:    routing.DateRef{Year: 2024, Month: 5, Day: 17},
			wouldBePost: routing.Ref{Year: 2024, Month: 5, Slug: "17"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q, \"\", \"\") error: %v", tc.structure, err)
			}
			got := s.Classify(tc.path)
			if got.Kind != routing.KindDate {
				t.Fatalf("Classify(%q) under %q Kind = %s, want KindDate -- "+
					"the date row runs before the post rows, so this path must "+
					"not resolve post %+v", tc.path, tc.structure,
					kindName(got.Kind), tc.wouldBePost)
			}
			if got.Date != tc.wantDate {
				t.Errorf("Classify(%q) Date = %+v, want %+v",
					tc.path, got.Date, tc.wantDate)
			}
			if got.Post != (routing.Ref{}) {
				t.Errorf("Classify(%q) Post = %+v, want the zero Ref -- a date "+
					"archive addresses no post", tc.path, got.Post)
			}
		})
	}
}

// Requirements 9.4 and 2.8. A path whose first segment is the category base, the
// tag base or `author` is resolved as that archive kind ahead of any post
// reading.
//
// The bare-base cases are where the two readings genuinely compete: under a
// single-segment structure /category, /tag and /author are all perfectly good
// postnames, and row 7 would match every one of them if the base rows let a path
// rooted at a base fall through. They do not. That is Req 9.4's documented
// consequence -- "a post slugged identically to one of those base segments is
// unreachable under a single-segment structure", which design.md's consequences
// list repeats verbatim for `category`, `tag`, `author` and the configured base --
// and it is asserted here rather than left to chi, whose static-over-parameter
// preference would produce the same result for reasons nobody can review.
//
// design.md's table spells the zero-further-segment case out for the category row
// only (row 3, from Req 2.8's no-category-index rule) and leaves the tag and
// author rows saying "exactly 1 further segment". Read strictly, that lets /tag
// fall to row 7 as a post slugged "tag", which is the one thing Req 9.4 says must
// not happen -- so rows 4 and 5 need row 3's clause too: a path rooted at a base
// segment names that archive or names nothing.
func TestClassifyResolvesArchiveBasesBeforePosts(t *testing.T) {
	cases := []struct {
		name string
		path string
		want routing.Kind
	}{
		{
			name: "bare category base is not a post slugged category",
			path: "/category/",
			want: routing.KindNone,
		},
		{
			name: "bare tag base is not a post slugged tag",
			path: "/tag/",
			want: routing.KindNone,
		},
		{
			name: "bare author base is not a post slugged author",
			path: "/author/",
			want: routing.KindNone,
		},
		{
			name: "category base with a segment is a category, not a two segment miss",
			path: "/category/news/",
			want: routing.KindCategory,
		},
		{
			name: "tag base with a segment is a tag",
			path: "/tag/news/",
			want: routing.KindTag,
		},
		{
			name: "author base with a segment is an author",
			path: "/author/news/",
			want: routing.KindAuthor,
		},
	}

	s, err := routing.Parse(presetPostName, "", "")
	if err != nil {
		t.Fatalf("Parse(%q, \"\", \"\") error: %v", presetPostName, err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Classify(tc.path)
			if got.Kind != tc.want {
				t.Fatalf("Classify(%q) Kind = %s, want %s",
					tc.path, kindName(got.Kind), kindName(tc.want))
			}
			if tc.want == routing.KindNone && got.Post != (routing.Ref{}) {
				t.Errorf("Classify(%q) Post = %+v, want the zero Ref -- a base "+
					"segment must not fall through to the flat post reading",
					tc.path, got.Post)
			}
		})
	}
}

// Requirements 4.7, 4.8 and 4.9, and design.md's per-row **expected prefix**
// column.
//
// Each archive row carries its own prefix, so a single strip-the-front-first
// step would be wrong: with front `blog` and category_base set to `sections`,
// /sections/news is a category path and /blog/sections/news is not, while
// /blog/2024 is a date archive in the same structure. Every negative case here
// is the path the stripping implementation would accept, and every positive case
// is the one it would reject -- which is why they are asserted in pairs.
//
// The prefix comes from the same unexported helper the constructors use, so this
// test and the archive path tests cannot drift apart: a front applied on
// construction but not on classification fails the fixed-point property in
// TestArchivePathsAreClassifyFixedPoints, and a front applied to the wrong kind
// fails here.
func TestClassifyExpectedPrefixPerRow(t *testing.T) {
	runClassifyCases(t, []classifyCase{
		// ---- front, both taxonomy bases unset: the front applies to all four
		// kinds, because with_front is ! get_option( '{taxonomy}_base' ) and
		// neither option was provided.
		{
			name:         "unset bases: category carries the front",
			structure:    presetFrontMonthAndName,
			path:         "/blog/category/news/",
			want:         routing.KindCategory,
			wantSegments: []string{"news"},
		},
		{
			name:      "unset bases: category without the front is not a category",
			structure: presetFrontMonthAndName,
			path:      "/category/news/",
			want:      routing.KindNone,
		},
		{
			name:      "unset bases: tag carries the front",
			structure: presetFrontMonthAndName,
			path:      "/blog/tag/go/",
			want:      routing.KindTag,
			wantSlug:  "go",
		},
		{
			name:      "unset bases: tag without the front is not a tag",
			structure: presetFrontMonthAndName,
			path:      "/tag/go/",
			want:      routing.KindNone,
		},
		{
			name:      "unset bases: author carries the front",
			structure: presetFrontMonthAndName,
			path:      "/blog/author/alice/",
			want:      routing.KindAuthor,
			wantSlug:  "alice",
		},
		{
			// The author front is unconditional: get_author_permastruct()
			// concatenates $this->front with no with_front flag and no option
			// lookup, because author_base is a WP_Rewrite property rather than a
			// row in {prefix}options.
			name:      "unset bases: author without the front is not an author",
			structure: presetFrontMonthAndName,
			path:      "/author/alice/",
			want:      routing.KindNone,
		},
		{
			name:      "unset bases: date carries the front",
			structure: presetFrontMonthAndName,
			path:      "/blog/2024/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024},
		},
		{
			name:      "unset bases: date without the front is not a date",
			structure: presetFrontMonthAndName,
			path:      "/2024/",
			want:      routing.KindNone,
		},

		// ---- front, both taxonomy bases set: the front drops off the two
		// taxonomies and stays on date and author. A site that renamed its
		// category base publishes /sections/news, not /blog/sections/news, so
		// this asymmetry is what keeps its existing URLs working.
		{
			name:         "set bases: category drops the front",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			path:         "/sections/news/",
			want:         routing.KindCategory,
			wantSegments: []string{"news"},
		},
		{
			name:         "set bases: category with the front is not a category",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			path:         "/blog/sections/news/",
			want:         routing.KindNone,
		},
		{
			name:         "set bases: nested category drops the front",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			path:         "/sections/news/local/",
			want:         routing.KindCategory,
			wantSegments: []string{"news", "local"},
		},
		{
			name:         "set bases: bare base is still KindNone",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			path:         "/sections/",
			want:         routing.KindNone,
		},
		{
			name:         "set bases: tag drops the front",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			path:         "/topics/go/",
			want:         routing.KindTag,
			wantSlug:     "go",
		},
		{
			name:         "set bases: tag with the front is not a tag",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			path:         "/blog/topics/go/",
			want:         routing.KindNone,
		},
		{
			// Req 4.7: the author front does not depend on a taxonomy option, so
			// it survives a configuration that dropped the category and tag
			// fronts.
			name:         "set bases: author still carries the front",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			path:         "/blog/author/alice/",
			want:         routing.KindAuthor,
			wantSlug:     "alice",
		},
		{
			name:         "set bases: date still carries the front",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			path:         "/blog/2024/",
			want:         routing.KindDate,
			wantDate:     routing.DateRef{Year: 2024},
		},

		// ---- front, bases set to the **default strings**. Req 4.9: WordPress
		// tests get_option()'s truthiness, not inequality with the default, so
		// these are set and the front drops. An implementation that decides
		// "set" by comparing against DefaultCategoryBase passes every case above
		// and fails these four.
		{
			name:         "bases set to the defaults: category drops the front",
			structure:    presetFrontMonthAndName,
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
			path:         "/category/news/",
			want:         routing.KindCategory,
			wantSegments: []string{"news"},
		},
		{
			name:         "bases set to the defaults: category with the front is not a category",
			structure:    presetFrontMonthAndName,
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
			path:         "/blog/category/news/",
			want:         routing.KindNone,
		},
		{
			name:         "bases set to the defaults: tag drops the front",
			structure:    presetFrontMonthAndName,
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
			path:         "/tag/go/",
			want:         routing.KindTag,
			wantSlug:     "go",
		},
		{
			name:         "bases set to the defaults: tag with the front is not a tag",
			structure:    presetFrontMonthAndName,
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
			path:         "/blog/tag/go/",
			want:         routing.KindNone,
		},
	})
}

// Requirement 6.7. Row 6's prefix carries WordPress's `date/` disambiguation
// segment when %post_id% appears among the structure's first three tokens, and
// the segment lands **after** the front.
//
// Without it, /2024 under a bare /%post_id%/ structure classifies as a year
// archive and post 2024 becomes unreachable by its own canonical permalink. The
// paired cases are the point: the prefixed path must be the date and the bare
// path must be the post, in the same configuration.
func TestClassifyDateDisambiguationPrefix(t *testing.T) {
	runClassifyCases(t, []classifyCase{
		{
			name:      "post id structure: date prefixed year is a date archive",
			structure: presetPostIDOnly,
			path:      "/date/2024/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024},
		},
		{
			name:      "post id structure: the bare year is post 2024",
			structure: presetPostIDOnly,
			path:      "/2024/",
			want:      routing.KindPost,
			wantPost:  routing.Ref{ID: 2024},
		},
		{
			name:      "post id structure: date prefixed year and month",
			structure: presetPostIDOnly,
			path:      "/date/2024/05/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024, Month: 5},
		},
		{
			name:      "post id structure: date prefixed year month and day",
			structure: presetPostIDOnly,
			path:      "/date/2024/05/17/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024, Month: 5, Day: 17},
		},
		{
			// The prefix is required, not optional: an unprefixed month path
			// matches no row at all under this structure.
			name:      "post id structure: unprefixed year and month is nothing",
			structure: presetPostIDOnly,
			path:      "/2024/05/",
			want:      routing.KindNone,
		},
		{
			// The segment lands after the front, so the two prefixes compose
			// rather than compete.
			name:      "front and post id: date prefix follows the front",
			structure: presetFrontPostID,
			path:      "/blog/date/2024/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024},
		},
		{
			name:      "front and post id: the front plus a bare year is a post",
			structure: presetFrontPostID,
			path:      "/blog/2024/",
			want:      routing.KindPost,
			wantPost:  routing.Ref{ID: 2024},
		},
		{
			// %post_id% at token index 4 gets no prefix (the index counts
			// tokens, not segments), so date archives stay at their bare paths
			// and this structure's own shape claims the four-segment form.
			name:      "day and post id: token index four gets no date prefix",
			structure: presetDayPostID,
			path:      "/2024/",
			want:      routing.KindDate,
			wantDate:  routing.DateRef{Year: 2024},
		},
		{
			name:      "day and post id: the date prefixed path is not an archive",
			structure: presetDayPostID,
			path:      "/date/2024/",
			want:      routing.KindNone,
		},
	})
}

// Requirements 9.8 and 6.8. A Flat structure -- plain permalinks -- yields no
// date archive at all, and /2024 classifies as KindPost with Ref{Slug: "2024"},
// exactly as it resolves today.
//
// Asserted explicitly rather than inferred from the gate, for three reasons that
// agree: WordPress with plain permalinks registers no rewrite rules and serves
// ?m=2024 instead; a Flat structure has no front, so an ungated date row would
// claim /2024 ahead of the flat /{slug} route that Req 9.6 lists among the
// routes whose behavior must not change; and "2024" is the slug WordPress
// generates for a post titled "2024", so that post would become unreachable.
//
// Category, tag and author archives are **not** gated -- grimoire already serves
// /category/{slug} under plain permalinks -- so their rows are asserted here too,
// including the trailing-slash form, whose canonical counterpart carries no slash
// under a Flat structure (Req 2.7).
func TestClassifyFlatStructure(t *testing.T) {
	runClassifyCases(t, []classifyCase{
		{
			name:     "flat: a bare year is a post, never a date archive",
			path:     "/2024",
			want:     routing.KindPost,
			wantPost: routing.Ref{Slug: "2024"},
		},
		{
			name: "flat: a year and month path is nothing",
			path: "/2024/05",
			want: routing.KindNone,
		},
		{
			name: "flat: a year month and day path is nothing",
			path: "/2024/05/17",
			want: routing.KindNone,
		},
		{
			name:     "flat: a slug is a post",
			path:     "/hello-world",
			want:     routing.KindPost,
			wantPost: routing.Ref{Slug: "hello-world"},
		},
		{
			name:         "flat: category archives still classify",
			path:         "/category/news",
			want:         routing.KindCategory,
			wantSegments: []string{"news"},
		},
		{
			// The slash form is not canonical under a Flat structure, so the
			// handler 301s it -- which it can only do if the classifier
			// recognises it and reports the form it arrived in.
			name:         "flat: the non canonical slash form still classifies",
			path:         "/category/news/",
			want:         routing.KindCategory,
			wantSegments: []string{"news"},
		},
		{
			name:     "flat: tag archives still classify",
			path:     "/tag/go",
			want:     routing.KindTag,
			wantSlug: "go",
		},
		{
			name:     "flat: author archives still classify",
			path:     "/author/alice",
			want:     routing.KindAuthor,
			wantSlug: "alice",
		},
		{
			name: "flat: root path",
			path: "/",
			want: routing.KindNone,
		},
	})
}

// Requirements 9.7 and 4.4b. When a resolved archive base equals a leading
// literal segment of the permalink structure, the **post** interpretation wins
// and that archive base is unreachable.
//
// The concrete case is WordPress's own Numeric preset, /archives/%post_id%, on a
// site whose category_base is `archives` -- a rename an operator makes precisely
// *because* their URLs live under /archives/. Giving the base priority would
// classify /archives/123 as a category, resolve no category, and 404 every post
// URL on the site from its own canonical permalink. The milestone's premise is
// that an existing site's published URLs keep working, so the archive base is
// the surface that degrades, loudly, through Structure.Notes().
//
// This has its own test because chi registers the colliding patterns without
// complaining -- /archives/{post_id} alongside /archives/* was probed directly --
// so nothing else would ever surface it.
func TestClassifyBaseCollidingWithFrontKeepsPostMeaning(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
		path         string
		wantPost     routing.Ref
	}{
		{
			name:         "category base equal to the structure's literal front",
			structure:    presetNumeric,
			categoryBase: "archives",
			path:         "/archives/123",
			wantPost:     routing.Ref{ID: 123},
		},
		{
			name:      "tag base equal to the structure's literal front",
			structure: presetNumeric,
			tagBase:   "archives",
			path:      "/archives/123",
			wantPost:  routing.Ref{ID: 123},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					tc.structure, tc.categoryBase, tc.tagBase, err)
			}
			got := s.Classify(tc.path)
			if got.Kind != routing.KindPost {
				t.Fatalf("Classify(%q) Kind = %s, want KindPost -- a base that "+
					"collides with the structure's front must not take every "+
					"post URL on the site", tc.path, kindName(got.Kind))
			}
			if got.Post != tc.wantPost {
				t.Errorf("Classify(%q) Post = %+v, want %+v",
					tc.path, got.Post, tc.wantPost)
			}
		})
	}
}
