package routing_test

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// Structures carrying a front, which M9a parsed but nothing consumed. The front
// is the maximal run of leading literal segments before the first token, so
// these are the shapes the archive constructors have to read it from.
const (
	presetFrontMonthAndName = "/blog/%year%/%monthnum%/%postname%/"
	presetTwoSegmentFront   = "/blog/news/%postname%/"
	presetPostNameNoSlash   = "/%postname%"
)

// Structures carrying %post_id% at each of the four token positions that matter
// to WordPress's "date/" disambiguation rule, plus the two front-carrying forms
// that separate "which token index" from "how many segments precede it".
//
// The rule is an index comparison over *tokens*:
// WP_Rewrite::get_date_permastruct() collects them with
// preg_match_all( '/%.+?%/', ... ) -- which skips literal segments entirely --
// and prefixes the date structure when %post_id% lands at index <= 3.
const (
	presetPostIDOnly      = "/%post_id%/"
	presetYearPostID      = "/%year%/%post_id%/"
	presetMonthPostID     = "/%year%/%monthnum%/%post_id%/"
	presetDayPostID       = "/%year%/%monthnum%/%day%/%post_id%/"
	presetFrontPostID     = "/blog/%post_id%/"
	presetFrontMonthPstID = "/blog/%year%/%monthnum%/%post_id%/"
)

// Requirements 2.1, 2.7 and 4.1. A category's canonical path is the base segment
// followed by its full ancestry, root first, ending in its own slug -- so a
// top-level category is the one-element case and its path is byte-for-byte the
// flat path grimoire serves today.
func TestCategoryPath(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		ancestry     []string
		want         string
	}{
		{
			// Req 2.4's fixed point, and Req 2.7's "no trailing slash when
			// permalink_structure is empty": this is today's /category/{slug}
			// exactly, which is the byte-for-byte compatibility claim.
			name:     "top level category under a flat structure is today's flat path",
			ancestry: []string{"news"},
			want:     "/category/news",
		},
		{
			name:      "top level category equals the flat path with the structure's slash",
			structure: presetPostName,
			ancestry:  []string{"news"},
			want:      "/category/news/",
		},
		{
			// The whole point of the milestone: the ancestry is in the path, root
			// first, so a nested category stops being served as if top-level.
			name:      "nested category carries its ancestry root first",
			structure: presetPostName,
			ancestry:  []string{"news", "local"},
			want:      "/category/news/local/",
		},
		{
			// Three levels, because a two-level case passes for an
			// implementation that appends only the immediate parent.
			name:      "three level ancestry is carried in full",
			structure: presetPostName,
			ancestry:  []string{"news", "local", "towns"},
			want:      "/category/news/local/towns/",
		},
		{
			// Req 4.1: the base is Structure.CategoryBase, which M9a resolved and
			// no route consumed.
			name:         "a non-default category base replaces the default segment",
			structure:    presetPostName,
			categoryBase: "sections",
			ancestry:     []string{"news", "local"},
			want:         "/sections/news/local/",
		},
		{
			// Req 2.7: the trailing slash follows Structure.TrailingSlash, the
			// same rule M9a applies to posts, so only one form is canonical.
			name:      "no trailing slash when the structure has none",
			structure: presetPostNameNoSlash,
			ancestry:  []string{"news", "local"},
			want:      "/category/news/local",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, "")
			if err != nil {
				t.Fatalf("Parse(%q, %q, \"\") error: %v", tc.structure, tc.categoryBase, err)
			}
			if got := s.CategoryPath(tc.ancestry); got != tc.want {
				t.Errorf("CategoryPath(%q) = %q, want %q", tc.ancestry, got, tc.want)
			}
		})
	}
}

// Requirements 4.2, 5.1 and 2.7. Tags are flat -- post_tag is non-hierarchical
// in WordPress -- so a tag path is the base and one slug, never an ancestry.
func TestTagPath(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		tagBase   string
		slug      string
		want      string
	}{
		{
			name: "flat structure yields no trailing slash",
			slug: "go",
			want: "/tag/go",
		},
		{
			name:      "default base with the structure's trailing slash",
			structure: presetPostName,
			slug:      "go",
			want:      "/tag/go/",
		},
		{
			name:      "a non-default tag base replaces the default segment",
			structure: presetPostName,
			tagBase:   "topics",
			slug:      "go",
			want:      "/topics/go/",
		},
		{
			name:      "no trailing slash when the structure has none",
			structure: presetPostNameNoSlash,
			slug:      "go",
			want:      "/tag/go",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, \"\", %q) error: %v", tc.structure, tc.tagBase, err)
			}
			if got := s.TagPath(tc.slug); got != tc.want {
				t.Errorf("TagPath(%q) = %q, want %q", tc.slug, got, tc.want)
			}
		})
	}
}

// Requirements 4.3 and 7.1. The author base is the literal "author" and is not
// configurable: WordPress stores no author_base option -- it is a WP_Rewrite
// property, not a row in {prefix}options -- so there is nothing to read and
// nothing the taxonomy base overrides can move.
func TestAuthorPath(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
		want         string
	}{
		{
			name: "flat structure yields no trailing slash",
			want: "/author/alice",
		},
		{
			name:      "the base is the literal author",
			structure: presetPostName,
			want:      "/author/alice/",
		},
		{
			// The taxonomy bases are options; the author base is not. Overriding
			// both must leave the author path untouched.
			name:         "taxonomy base overrides do not move the author base",
			structure:    presetPostName,
			categoryBase: "sections",
			tagBase:      "topics",
			want:         "/author/alice/",
		},
		{
			name:      "no trailing slash when the structure has none",
			structure: presetPostNameNoSlash,
			want:      "/author/alice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					tc.structure, tc.categoryBase, tc.tagBase, err)
			}
			if got := s.AuthorPath("alice"); got != tc.want {
				t.Errorf("AuthorPath(%q) = %q, want %q", "alice", got, tc.want)
			}
		})
	}
}

// Requirements 6.1, 6.2 and 2.7. A date path carries the granularity its DateRef
// carries, at WordPress's widths -- 4-digit year, zero-padded 2-digit month and
// day -- so a 1-digit month is never advertised as canonical.
//
// None of these structures carries %post_id%, so none of them fires WordPress's
// "date/" disambiguation prefix; that rule is keyed on the token index and is
// exercised on its own over token positions 1-4.
func TestDatePath(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		ref       routing.DateRef
		want      string
	}{
		{
			name:      "year only",
			structure: presetDayAndName,
			ref:       routing.DateRef{Year: 2024},
			want:      "/2024/",
		},
		{
			// Zero-padding: a 1-digit month must not reach the path, because
			// Match rejects that shape and the archive would 404 its own
			// canonical URL.
			name:      "year and month are zero padded",
			structure: presetDayAndName,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/2024/05/",
		},
		{
			name:      "year month and day",
			structure: presetDayAndName,
			ref:       routing.DateRef{Year: 2024, Month: 5, Day: 17},
			want:      "/2024/05/17/",
		},
		{
			name:      "no trailing slash when the structure has none",
			structure: presetPostNameNoSlash,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/2024/05",
		},
		{
			// Req 6.8: a Flat structure serves no date archives at all, so there
			// is no path to advertise. Returning "/2024" here would claim a URL
			// that today resolves a post slugged "2024".
			name: "flat structure yields no date path at all",
			ref:  routing.DateRef{Year: 2024},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.structure, err)
			}
			if got := s.DatePath(tc.ref); got != tc.want {
				t.Errorf("DatePath(%+v) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

// Requirements 4.7, 4.8 and 4.9, and design.md's "The front" table. The front is
// applied to archives *asymmetrically*, replicating WordPress:
//
//	date   -- always   (get_date_permastruct(): $front . $date_endian)
//	author -- always   (get_author_permastruct(): $this->front . $this->author_base)
//	category/tag -- only when the corresponding base option is unset
//	                (create_initial_taxonomies(): with_front => ! get_option(...))
//
// All four constructors are asserted against the same Structure in each row, so
// an implementation that applies the front globally, or drops it globally, fails
// on the row rather than on a later redirect loop.
func TestArchivePathFrontAsymmetry(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
		wantCategory string
		wantTag      string
		wantAuthor   string
		wantDate     string
	}{
		{
			name:         "neither base set: the front applies to all four",
			structure:    presetFrontMonthAndName,
			wantCategory: "/blog/category/news/local/",
			wantTag:      "/blog/tag/go/",
			wantAuthor:   "/blog/author/alice/",
			wantDate:     "/blog/2024/05/",
		},
		{
			// A site that renamed its bases publishes /sections/news/local, not
			// /blog/sections/news/local -- which is why the asymmetry is
			// replicated rather than simplified.
			name:         "both bases set: category and tag drop the front, date and author keep it",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			wantCategory: "/sections/news/local/",
			wantTag:      "/topics/go/",
			wantAuthor:   "/blog/author/alice/",
			wantDate:     "/blog/2024/05/",
		},
		{
			// Req 4.9, and the case that separates provision from
			// inequality-with-the-default. WordPress tests get_option()'s
			// truthiness, so category_base = "category" is *set* and the front is
			// dropped. An implementation that decided "set" by comparing against
			// DefaultCategoryBase passes every other row and fails this one,
			// serving /blog/category/news/local against a site that publishes
			// /category/news/local.
			name:         "bases set to the default strings still drop the front",
			structure:    presetFrontMonthAndName,
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
			wantCategory: "/category/news/local/",
			wantTag:      "/tag/go/",
			wantAuthor:   "/blog/author/alice/",
			wantDate:     "/blog/2024/05/",
		},
		{
			// WordPress evaluates with_front once per taxonomy, so the two
			// decisions are independent: the same structure can carry the front
			// on its tag archive and not on its category archive.
			name:         "only the category base set: tag keeps the front, category does not",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			wantCategory: "/sections/news/local/",
			wantTag:      "/blog/tag/go/",
			wantAuthor:   "/blog/author/alice/",
			wantDate:     "/blog/2024/05/",
		},
		{
			// The front is the maximal run of leading literals, so both segments
			// are prepended. An implementation taking only the first segment
			// reports /blog/author/alice here.
			name:         "a two segment front is prepended in full",
			structure:    presetTwoSegmentFront,
			wantCategory: "/blog/news/category/news/local/",
			wantTag:      "/blog/news/tag/go/",
			wantAuthor:   "/blog/news/author/alice/",
			wantDate:     "/blog/news/2024/05/",
		},
		{
			// A structure with no leading literal has no front, so no archive
			// gains a prefix. This is the row that fails for an implementation
			// that strips every literal rather than the leading run.
			name:         "no front means no prefix on any kind",
			structure:    presetMonthAndName,
			wantCategory: "/category/news/local/",
			wantTag:      "/tag/go/",
			wantAuthor:   "/author/alice/",
			wantDate:     "/2024/05/",
		},
		{
			// WordPress's own Numeric preset already carries a front, and it has
			// no trailing slash -- so the front and the slash rule are exercised
			// together on a structure a real site may be using. Its date path is
			// asserted separately, because %post_id% at token 1 also fires the
			// "date/" prefix.
			name:         "the numeric preset's front applies without a trailing slash",
			structure:    presetNumeric,
			wantCategory: "/archives/category/news/local",
			wantTag:      "/archives/tag/go",
			wantAuthor:   "/archives/author/alice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					tc.structure, tc.categoryBase, tc.tagBase, err)
			}
			ancestry := []string{"news", "local"}
			if got := s.CategoryPath(ancestry); got != tc.wantCategory {
				t.Errorf("CategoryPath(%q) = %q, want %q", ancestry, got, tc.wantCategory)
			}
			if got := s.TagPath("go"); got != tc.wantTag {
				t.Errorf("TagPath(%q) = %q, want %q", "go", got, tc.wantTag)
			}
			if got := s.AuthorPath("alice"); got != tc.wantAuthor {
				t.Errorf("AuthorPath(%q) = %q, want %q", "alice", got, tc.wantAuthor)
			}
			if tc.wantDate == "" {
				return
			}
			ref := routing.DateRef{Year: 2024, Month: 5}
			if got := s.DatePath(ref); got != tc.wantDate {
				t.Errorf("DatePath(%+v) = %q, want %q", ref, got, tc.wantDate)
			}
		})
	}
}

// Req 2.7 and 6.8. A Flat structure -- an empty permalink_structure, or the
// fallback returned alongside ErrUnsupported -- has no front and no trailing
// slash, so the three entity archives keep exactly the shape grimoire serves
// today and the date archive has no path at all. Asserted together because "flat
// is unchanged" is the compatibility promise the milestone rests on.
func TestArchivePathsUnderFlatStructure(t *testing.T) {
	for _, structure := range []string{"", "/%category%/%postname%/"} {
		s, _ := routing.Parse(structure, "", "")
		if !s.Flat {
			t.Fatalf("Parse(%q).Flat = false, want true", structure)
		}
		if got, want := s.CategoryPath([]string{"news"}), "/category/news"; got != want {
			t.Errorf("Parse(%q): CategoryPath = %q, want %q", structure, got, want)
		}
		if got, want := s.CategoryPath([]string{"news", "local"}), "/category/news/local"; got != want {
			// Nested-vs-flat canonicalisation applies regardless of
			// permalink_structure: it is a statement about taxonomy shape, so a
			// plain-permalink site still serves the nested path.
			t.Errorf("Parse(%q): nested CategoryPath = %q, want %q", structure, got, want)
		}
		if got, want := s.TagPath("go"), "/tag/go"; got != want {
			t.Errorf("Parse(%q): TagPath = %q, want %q", structure, got, want)
		}
		if got, want := s.AuthorPath("alice"), "/author/alice"; got != want {
			t.Errorf("Parse(%q): AuthorPath = %q, want %q", structure, got, want)
		}
		if got := s.DatePath(routing.DateRef{Year: 2024, Month: 5, Day: 17}); got != "" {
			t.Errorf("Parse(%q): DatePath = %q, want \"\" -- a flat structure serves no date archive", structure, got)
		}
	}
}

// Requirement 6.7 and design.md's "Date disambiguation: WordPress's date/
// segment". WHEN %post_id% appears among the structure's first three tokens,
// date archives move under an extra "date/" segment, placed after the front and
// before the date components -- WP_Rewrite::get_date_permastruct()'s rule
// verbatim, which walks the structure's tokens with a 1-based index and sets
// $front = $front . 'date/' on finding %post_id% at index <= 3.
//
// The table runs over token positions 1-4 because the rule is an *index
// comparison*, not a "does the structure carry %post_id%" test and not a "does a
// front exist" test. Both boundaries are asserted:
//
//	index 3 (/%year%/%monthnum%/%post_id%/)     -- prefixed
//	index 4 (/%year%/%monthnum%/%day%/%post_id%/) -- not prefixed
//
// That index-4 case genuinely is ambiguous in WordPress too, and "fixing" it
// would break URL compatibility with the site grimoire reads, so it is copied
// rather than corrected.
//
// Without the prefix, a bare /%post_id%/ structure makes post 2024 unreachable
// by its own canonical permalink: /2024 classifies as a year archive ahead of
// the post rows (Req 9.3). That collision is what the segment exists to prevent.
func TestDatePathDateDisambiguationPrefix(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		ref       routing.DateRef
		want      string
	}{
		{
			// Token index 1, the case the rule exists for.
			name:      "post_id at token 1 prefixes the year archive",
			structure: presetPostIDOnly,
			ref:       routing.DateRef{Year: 2024},
			want:      "/date/2024/",
		},
		{
			// The prefix precedes the date components at every granularity, not
			// only the shortest one.
			name:      "post_id at token 1 prefixes the year month archive",
			structure: presetPostIDOnly,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/date/2024/05/",
		},
		{
			name:      "post_id at token 1 prefixes the year month day archive",
			structure: presetPostIDOnly,
			ref:       routing.DateRef{Year: 2024, Month: 5, Day: 17},
			want:      "/date/2024/05/17/",
		},
		{
			name:      "post_id at token 2 is prefixed",
			structure: presetYearPostID,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/date/2024/05/",
		},
		{
			// The inclusive edge of index <= 3.
			name:      "post_id at token 3 is prefixed",
			structure: presetMonthPostID,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/date/2024/05/",
		},
		{
			// The exclusive edge. An implementation testing "contains
			// %post_id%" rather than its index passes every row above and fails
			// this one, serving /date/2024/05 on a site that publishes
			// /2024/05.
			name:      "post_id at token 4 is not prefixed",
			structure: presetDayPostID,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/2024/05/",
		},
		{
			name:      "post_id at token 4 is not prefixed at day granularity",
			structure: presetDayPostID,
			ref:       routing.DateRef{Year: 2024, Month: 5, Day: 17},
			want:      "/2024/05/17/",
		},
		{
			// The common cases carry no %post_id% at all and keep serving date
			// archives at their bare paths, exactly as WordPress does.
			name:      "postname only structure is not prefixed",
			structure: presetPostName,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/2024/05/",
		},
		{
			name:      "day and name structure is not prefixed",
			structure: presetDayAndName,
			ref:       routing.DateRef{Year: 2024, Month: 5, Day: 17},
			want:      "/2024/05/17/",
		},
		{
			// Req 6.7's placement claim: the prefix lands *after* the front,
			// which dates always carry (Req 4.7). An implementation prepending
			// it to the whole path reports /date/blog/2024 here.
			name:      "the prefix lands after the front",
			structure: presetFrontPostID,
			ref:       routing.DateRef{Year: 2024},
			want:      "/blog/date/2024/",
		},
		{
			name:      "the prefix lands after the front at month granularity",
			structure: presetFrontPostID,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/blog/date/2024/05/",
		},
		{
			// Literals do not count toward the index: %post_id% is the third
			// *token* but the fourth *segment*. An implementation counting
			// segments drops the prefix here.
			name:      "a literal front does not count toward the token index",
			structure: presetFrontMonthPstID,
			ref:       routing.DateRef{Year: 2024, Month: 5},
			want:      "/blog/date/2024/05/",
		},
		{
			// WordPress's own Numeric preset, deferred from the front table
			// because its date path fires this rule: its literal front already
			// sidesteps the /2024 collision, yet the prefix applies anyway
			// because the rule is keyed on the token index rather than on
			// whether a front happens to exist. The trailing-slash rule holds
			// alongside it -- the preset carries no slash.
			name:      "the numeric preset is prefixed despite its front",
			structure: presetNumeric,
			ref:       routing.DateRef{Year: 2024},
			want:      "/archives/date/2024",
		},
		{
			name:      "the numeric preset is prefixed at day granularity without a trailing slash",
			structure: presetNumeric,
			ref:       routing.DateRef{Year: 2024, Month: 5, Day: 17},
			want:      "/archives/date/2024/05/17",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.structure, err)
			}
			if got := s.DatePath(tc.ref); got != tc.want {
				t.Errorf("Parse(%q).DatePath(%+v) = %q, want %q",
					tc.structure, tc.ref, got, tc.want)
			}
		})
	}
}

// Requirements 4.11, 4.12 and 4.13. A stored base is normalized before anything
// consumes it -- surrounding whitespace and slashes trimmed, repeated internal
// slashes collapsed -- because the base is the seat of pattern registration, the
// classifier's segment comparison and these path constructors. "/topics" is a
// realistic stored value: WordPress's options-permalink.php prefixes the
// submitted base with "/" before update_option, so get_option( 'category_base' )
// plausibly returns "/topics" on any site whose base was set through the admin
// UI, and unnormalized it would advertise //topics/news.
//
// The emptiness test that decides the fallback and the one that decides
// CategoryBaseSet/TagBaseSet are the same test, applied after normalization
// (Req 4.13), which is what the whitespace and slash-only rows assert: a base of
// " " or "/" falls back to the default *and* counts as unset, so the front still
// applies.
func TestParseNormalizesArchiveBases(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
		wantCatBase  string
		wantTagBase  string
		wantCatSet   bool
		wantTagSet   bool
		wantCategory string
		wantTag      string
	}{
		{
			// The admin-UI shape. Unnormalized this yields //topics/news/local.
			name:         "a leading slash is trimmed",
			structure:    presetPostName,
			categoryBase: "/topics",
			tagBase:      "/labels",
			wantCatBase:  "topics",
			wantTagBase:  "labels",
			wantCatSet:   true,
			wantTagSet:   true,
			wantCategory: "/topics/news/local/",
			wantTag:      "/labels/go/",
		},
		{
			name:         "surrounding slashes and whitespace are trimmed",
			structure:    presetPostName,
			categoryBase: "  /topics/  ",
			tagBase:      " labels/ ",
			wantCatBase:  "topics",
			wantTagBase:  "labels",
			wantCatSet:   true,
			wantTagSet:   true,
			wantCategory: "/topics/news/local/",
			wantTag:      "/labels/go/",
		},
		{
			name:         "repeated internal slashes are collapsed",
			structure:    presetPostName,
			categoryBase: "a//b",
			tagBase:      "c///d",
			wantCatBase:  "a/b",
			wantTagBase:  "c/d",
			wantCatSet:   true,
			wantTagSet:   true,
			// Req 4.12: a multi-segment base stays usable rather than being
			// rejected; it is reported as a diagnostic instead.
			wantCategory: "/a/b/news/local/",
			wantTag:      "/c/d/go/",
		},
		{
			name:         "a multi segment base is carried in full",
			structure:    presetPostName,
			categoryBase: "/topics/news/",
			wantCatBase:  "topics/news",
			wantTagBase:  routing.DefaultTagBase,
			wantCatSet:   true,
			wantCategory: "/topics/news/news/local/",
			wantTag:      "/tag/go/",
		},
		{
			// Req 4.13's shared emptiness test: whitespace only is unset, so it
			// falls back to the default *and* keeps the front.
			name:         "a whitespace only base is unset and keeps the front",
			structure:    presetFrontMonthAndName,
			categoryBase: " ",
			tagBase:      "\t",
			wantCatBase:  routing.DefaultCategoryBase,
			wantTagBase:  routing.DefaultTagBase,
			wantCategory: "/blog/category/news/local/",
			wantTag:      "/blog/tag/go/",
		},
		{
			// Same, for a value that is nothing but slashes -- the shape a
			// normalization step introduces and a pre-normalization emptiness
			// test would call "provided".
			name:         "a slash only base is unset and keeps the front",
			structure:    presetFrontMonthAndName,
			categoryBase: "//",
			tagBase:      " / ",
			wantCatBase:  routing.DefaultCategoryBase,
			wantTagBase:  routing.DefaultTagBase,
			wantCategory: "/blog/category/news/local/",
			wantTag:      "/blog/tag/go/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					tc.structure, tc.categoryBase, tc.tagBase, err)
			}
			if s.CategoryBase != tc.wantCatBase {
				t.Errorf("CategoryBase = %q, want %q", s.CategoryBase, tc.wantCatBase)
			}
			if s.TagBase != tc.wantTagBase {
				t.Errorf("TagBase = %q, want %q", s.TagBase, tc.wantTagBase)
			}
			if s.CategoryBaseSet != tc.wantCatSet {
				t.Errorf("CategoryBaseSet = %v, want %v (category_base = %q)",
					s.CategoryBaseSet, tc.wantCatSet, tc.categoryBase)
			}
			if s.TagBaseSet != tc.wantTagSet {
				t.Errorf("TagBaseSet = %v, want %v (tag_base = %q)",
					s.TagBaseSet, tc.wantTagSet, tc.tagBase)
			}
			ancestry := []string{"news", "local"}
			if got := s.CategoryPath(ancestry); got != tc.wantCategory {
				t.Errorf("CategoryPath(%q) = %q, want %q", ancestry, got, tc.wantCategory)
			}
			if got := s.TagPath("go"); got != tc.wantTag {
				t.Errorf("TagPath(%q) = %q, want %q", "go", got, tc.wantTag)
			}
		})
	}
}
