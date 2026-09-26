package routing_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// The four archive families' pattern shapes, spelled once so a row reads as the
// prefix it is asserting rather than as five near-identical strings.
//
// Parameter names match token.paramName() for the date components and the
// diagram in design.md's "Architecture" block for the rest
// (/{CategoryBase}/*, /{TagBase}/{slug}, /author/{nicename}). Nothing reads
// them back: the handlers derive their components from r.URL.Path, the way
// web.resolveSingle already does through ParamsFromPath, because chi patterns
// carry no width or range syntax. They still have to be *stable* names, since
// every pattern is registered to one handler and a rename would silently move
// which chi node a colliding pattern lands on.
const (
	patCategoryTail = "/*"
	patTagTail      = "/{slug}"
	patAuthorTail   = "/{nicename}"
)

// archivePatternSet is the full expected pattern list for one Structure, written
// as the four prefixes each kind's pattern is rooted at. Assembling the expected
// slice from these rather than listing eleven literals per row keeps each row
// readable while still asserting the exact set: the tails are fixed above, so a
// row only varies in the part the front asymmetry and the base overrides move.
type archivePatternSet struct {
	// category, tag and author are the full base-inclusive prefixes, front
	// included where that kind carries one -- "/blog/category", "/sections",
	// "/blog/author".
	category string
	tag      string
	author   string
	// date is the prefix the date components hang off: the front, plus
	// WordPress's "date" disambiguation segment where the structure needs it.
	// Empty means the front is empty and no disambiguation applies, so the
	// components sit at the path root. noDate reports that the structure serves
	// no date archive at all, which is a different claim from "the prefix is
	// empty" (Req 6.8).
	date   string
	noDate bool
}

// patterns expands the set into every pattern ArchivePatterns is expected to
// return, both slash forms included.
func (p archivePatternSet) patterns() []string {
	out := []string{
		// One wildcard, not a pair. A nested category path has a segment count
		// no chi pattern can express (Req 9.5), so the route is a wildcard
		// rooted at the base and the handler derives the segments from
		// r.URL.Path -- and a wildcard's remainder already spans both slash
		// forms, so there is no second form to register.
		p.category + patCategoryTail,
		p.tag + patTagTail,
		p.tag + patTagTail + "/",
		p.author + patAuthorTail,
		p.author + patAuthorTail + "/",
	}
	if p.noDate {
		return out
	}
	for _, tail := range []string{
		"/{year}",
		"/{year}/{monthnum}",
		"/{year}/{monthnum}/{day}",
	} {
		out = append(out, p.date+tail, p.date+tail+"/")
	}
	return out
}

// assertArchivePatterns compares two pattern lists as sets, and rejects
// duplicates.
//
// Order is deliberately not asserted. Every pattern is registered to the same
// handler precisely so that which one chi picks at a colliding parameter node
// has no observable effect (design.md, "The structural idea: one handler, one
// classifier"), so ordering them would pin down a property the design has
// removed the meaning of -- unlike ChiPatterns, whose two elements are ordered
// because cmd/grimoire-cli/permalinks.go reads the no-slash form by index.
//
// Duplicates are asserted, because a repeated pattern is not a harmless
// repetition: it is the same route registered twice on one chi mux, which the
// router has no way to interpret as anything but a mistake.
func assertArchivePatterns(t *testing.T, got, want []string) {
	t.Helper()

	seen := make(map[string]int, len(got))
	for _, p := range got {
		seen[p]++
		if seen[p] == 2 {
			t.Errorf("ArchivePatterns() returned %q more than once: %v", p, got)
		}
		if !strings.HasPrefix(p, "/") {
			t.Errorf("ArchivePatterns() returned %q, which is not a rooted chi pattern", p)
		}
	}

	gotSorted := slices.Clone(got)
	wantSorted := slices.Clone(want)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Errorf("ArchivePatterns() =\n\t%s\nwant (order-insensitive)\n\t%s",
			strings.Join(gotSorted, "\n\t"), strings.Join(wantSorted, "\n\t"))
	}
}

// Requirements 4.1, 4.2, 4.7, 4.8, 6.7 and 9.5. ArchivePatterns returns every
// chi pattern the archive routes need, for the resolved bases and the
// structure's front, in both slash forms.
//
// Both forms are returned for the same reason ChiPatterns returns both: chi
// matches the two slash forms as distinct routes, so registering only the
// canonical one would have chi 404 the other before the handler could 301 it --
// the duplicate-URL problem TrailingSlash exists to resolve. The patterns
// therefore do not depend on Structure.TrailingSlash at all; which form is
// canonical is the handler's decision, made from the path constructors.
//
// The category route is the one exception to the pair: its segment count is
// unbounded, so it is a wildcard rooted at the base (Req 9.5).
func TestArchivePatterns(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
		want         archivePatternSet
	}{
		{
			// Plain permalinks. The three entity archives keep the shapes
			// grimoire serves today -- /category/{slug} is already served under
			// plain permalinks -- and there is no date route at all, because
			// WordPress with plain permalinks registers no rewrite rules and its
			// date archives are ?m=2024 (Req 6.8). An ungated date row would
			// also claim /2024 ahead of the flat /{slug} route that serves a post
			// slugged "2024" today.
			name: "flat structure registers the entity archives and no date route",
			want: archivePatternSet{
				category: "/category",
				tag:      "/tag",
				author:   "/author",
				noDate:   true,
			},
		},
		{
			// Req 4.1, 4.2: the defaults WordPress uses when neither option is
			// set, which is what the live reference database stores.
			name:      "default bases with no front",
			structure: presetPostName,
			want: archivePatternSet{
				category: "/category",
				tag:      "/tag",
				author:   "/author",
			},
		},
		{
			// Req 4.1, 4.2: the bases M9a resolved and no route consumed. This
			// is the row that fails for an implementation that hard-codes
			// "/category/*" and "/tag/{slug}" the way router.go does today.
			name:         "overridden bases replace the default segments",
			structure:    presetPostName,
			categoryBase: "sections",
			tagBase:      "topics",
			want: archivePatternSet{
				category: "/sections",
				tag:      "/topics",
				// Req 4.3: the author base is a constant, not an option, so the
				// taxonomy overrides do not move it.
				author: "/author",
			},
		},
		{
			// Req 4.11. The base is the seat of pattern registration, so the
			// patterns consume the *normalized* value: WordPress's
			// options-permalink.php prefixes the submitted base with "/" before
			// update_option, so "/topics" is a realistic stored value, and
			// unnormalized it registers "//topics/*" -- a pattern no request path
			// can match.
			name:         "a stored base with surrounding slashes is normalized in the pattern",
			structure:    presetPostName,
			categoryBase: "/topics/",
			tagBase:      "  /labels  ",
			want: archivePatternSet{
				category: "/topics",
				tag:      "/labels",
				author:   "/author",
			},
		},
		{
			// Req 4.7, 4.8: with neither base set, the front applies to all four
			// kinds.
			name:      "a front with neither base set prefixes all four kinds",
			structure: presetFrontMonthAndName,
			want: archivePatternSet{
				category: "/blog/category",
				tag:      "/blog/tag",
				author:   "/blog/author",
				date:     "/blog",
			},
		},
		{
			// Req 4.8's asymmetry, which is the case the whole front table
			// exists for: a site that renamed its bases publishes
			// /sections/news/local, not /blog/sections/news/local, while its date
			// and author archives keep the front because WordPress's
			// get_date_permastruct() and get_author_permastruct() concatenate it
			// unconditionally. An implementation that prefixes the front once,
			// for every kind, registers /blog/sections/* here and never matches a
			// URL the site publishes.
			name:         "bases set: category and tag drop the front, date and author keep it",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
			want: archivePatternSet{
				category: "/sections",
				tag:      "/topics",
				author:   "/blog/author",
				date:     "/blog",
			},
		},
		{
			// Req 4.9. WordPress tests get_option()'s truthiness, so a base set
			// explicitly to the default string is *set* and drops the front. An
			// implementation deciding "set" by comparing against
			// DefaultCategoryBase passes every other row and registers
			// /blog/category/* here, against a site publishing /category/news.
			name:         "bases set to the default strings still drop the front",
			structure:    presetFrontMonthAndName,
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
			want: archivePatternSet{
				category: "/category",
				tag:      "/tag",
				author:   "/blog/author",
				date:     "/blog",
			},
		},
		{
			// The front is the maximal run of leading literals, so both segments
			// are registered. An implementation taking only the first reports
			// /blog/author/{nicename} here.
			name:      "a two segment front is registered in full",
			structure: presetTwoSegmentFront,
			want: archivePatternSet{
				category: "/blog/news/category",
				tag:      "/blog/news/tag",
				author:   "/blog/news/author",
				date:     "/blog/news",
			},
		},
		{
			// Req 6.7. %post_id% at token 1 moves the date routes under
			// WordPress's "date/" disambiguation segment, which is what keeps
			// /2024 registrable as post 2024's own canonical permalink. The
			// entity archives are untouched by the rule -- it is
			// get_date_permastruct()'s alone.
			name:      "a post_id leading structure registers the date routes under date/",
			structure: presetPostIDOnly,
			want: archivePatternSet{
				category: "/category",
				tag:      "/tag",
				author:   "/author",
				date:     "/date",
			},
		},
		{
			// WordPress's own Numeric preset: a literal front *and* %post_id% at
			// token 1, so the date patterns carry both prefixes in that order --
			// the front first, then the disambiguation segment (Req 6.7's
			// placement claim). The preset carries no trailing slash, and the
			// patterns are unaffected by that: both slash forms are registered
			// regardless, so the handler can 301 the non-canonical one.
			name:      "the numeric preset carries the front and the date segment",
			structure: presetNumeric,
			want: archivePatternSet{
				category: "/archives/category",
				tag:      "/archives/tag",
				author:   "/archives/author",
				date:     "/archives/date",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					tc.structure, tc.categoryBase, tc.tagBase, err)
			}
			assertArchivePatterns(t, s.ArchivePatterns(), tc.want.patterns())
		})
	}
}

// Both slash forms, asserted as a property over every structure rather than only
// through the tables above: for each pattern that is not the category wildcard,
// the other slash form is registered too.
//
// This is the archive counterpart of TestChiPatternsCoverBothSlashForms, and it
// exists for the same reason. chi resolves the two forms as distinct routes, so
// an unregistered form 404s inside the router, before any handler can issue the
// 301 that makes one form canonical -- which would leave /tag/go/ dead on a site
// whose structure has no trailing slash, with nothing in the archive tables
// noticing as long as the canonical form happens to be the registered one.
func TestArchivePatternsCoverBothSlashForms(t *testing.T) {
	structures := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
	}{
		{name: "flat structure"},
		{name: "no front, default bases", structure: presetPostName},
		{name: "no trailing slash", structure: presetPostNameNoSlash},
		{name: "front, bases unset", structure: presetFrontMonthAndName},
		{
			name:         "front, bases set",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
		},
		{name: "numeric preset", structure: presetNumeric},
	}

	for _, tc := range structures {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					tc.structure, tc.categoryBase, tc.tagBase, err)
			}
			pats := s.ArchivePatterns()
			if len(pats) == 0 {
				t.Fatalf("ArchivePatterns() is empty -- the category, tag and author "+
					"routes are registered for every structure, including a flat one "+
					"(%q)", tc.structure)
			}
			for _, p := range pats {
				if strings.HasSuffix(p, patCategoryTail) {
					// The wildcard's remainder spans both forms, so there is no
					// paired pattern to look for.
					continue
				}
				want := p + "/"
				if strings.HasSuffix(p, "/") {
					want = strings.TrimSuffix(p, "/")
				}
				if !slices.Contains(pats, want) {
					t.Errorf("ArchivePatterns() has %q but not %q: %v", p, want, pats)
				}
			}
		})
	}
}
