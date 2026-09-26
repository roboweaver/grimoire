package routing_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// noteWant describes one expected Structure.Notes() entry by the substrings any
// message carrying it has to name: the option whose value is at fault and the
// value itself, plus -- where two readings of a segment compete -- which reading
// won.
//
// Substrings rather than whole strings, because the wording of a WARN line is
// not a contract and pinning it would make every future rephrasing a test
// failure. What *is* a contract is that an operator reading the line can tell
// which option to change and what it currently resolves to: a note saying only
// "base collision detected" leaves them reading source code, which is the
// failure mode Req 4.4's "SHALL emit a startup WARN naming the collision" exists
// to prevent.
type noteWant struct {
	// why names the condition, for the failure message only.
	why string
	// contains are the substrings the note must carry, all of them.
	contains []string
}

// assertNotes matches each expected note against exactly one of the notes
// returned, and requires that no further notes were returned.
//
// One-to-one rather than "at least one match": a configuration with a single
// fault that reports two notes has an operator chasing a collision that is not
// there, and a configuration whose two faults collapse into one note hides the
// second. The count is therefore asserted as well as the content.
func assertNotes(t *testing.T, notes []string, want []noteWant) {
	t.Helper()

	matched := make([]bool, len(notes))
	for _, w := range want {
		found := -1
		for i, note := range notes {
			if matched[i] {
				continue
			}
			if containsAll(note, w.contains) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Errorf("Notes() has no entry naming %s -- want one containing all of %q, got:\n\t%s",
				w.why, w.contains, strings.Join(notes, "\n\t"))
			continue
		}
		matched[found] = true
	}

	for i, note := range notes {
		if !matched[i] {
			t.Errorf("Notes() returned an unexpected entry %q -- a note for a "+
				"condition the configuration does not have sends an operator "+
				"looking for a fault that is not there", note)
		}
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// parseUsable parses and asserts the two claims Req 4.5 makes about every
// collision case: the error is nil, and the Structure is usable and not flat.
//
// The nil error is asserted explicitly rather than via a bare `if err != nil`
// buried in a table runner, because it is the whole point of Req 4.5. M9a gives
// Parse's error exactly one meaning at every call site -- "unsupported
// structure, fall back to the flat route" -- so reporting a renamed or colliding
// base through it would turn a cosmetic option clash into a site-wide permalink
// outage: every post on the site would stop resolving at its own canonical URL
// because two taxonomy bases happened to match.
//
// Non-flat is asserted for the same reason from the other side. A Parse that
// returned the flat fallback Structure *with* a nil error would pass an
// error-only check while producing exactly the outage the nil error was meant to
// avoid.
func parseUsable(t *testing.T, structure, categoryBase, tagBase string) routing.Structure {
	t.Helper()

	s, err := routing.Parse(structure, categoryBase, tagBase)
	if err != nil {
		t.Fatalf("Parse(%q, %q, %q) error = %v, want nil -- a base collision is a "+
			"diagnostic, not an unsupported structure; every caller reads this "+
			"error as \"fall back to the flat route\", so reporting a renamed "+
			"base here would 404 every permalink on the site (Req 4.5)",
			structure, categoryBase, tagBase, err)
	}
	if s.Flat {
		t.Fatalf("Parse(%q, %q, %q) returned a Flat Structure, want a usable one -- "+
			"a nil error alongside the flat fallback is the same permalink outage "+
			"the nil error exists to prevent (Req 4.5)",
			structure, categoryBase, tagBase)
	}
	if len(s.ChiPatterns()) == 0 {
		t.Fatalf("Parse(%q, %q, %q) returned a Structure with no chi patterns, so "+
			"no permalink route would be registered at all (Req 4.5)",
			structure, categoryBase, tagBase)
	}
	return s
}

// Requirements 4.4a and 4.5. The three base-segment collisions -- category_base
// equal to tag_base, category_base equal to "author", tag_base equal to
// "author" -- each surface a note naming the collision, while Parse returns a
// nil error and a usable non-flat Structure.
//
// The colliding segment keeps the *earlier* meaning in Classify's precedence
// table, which is what Req 4.4a's "keep the category meaning" decides: the
// category row is evaluated before the tag row and both before the author row,
// so a taxonomy base always wins the segment and the author interpretation is
// the one that becomes unreachable. Each row therefore asserts the Kind its own
// collision leaves reachable rather than asserting KindCategory three times,
// since for tag_base = "author" there is no category reading of /author/... to
// keep -- category_base still resolves to "category".
//
// The classification half of each row passes against the classifier as it
// stands; the notes are what this task leaves failing. Asserting both together
// is deliberate: a note that named a collision the classifier did not actually
// resolve that way would be a WARN telling an operator the wrong thing.
func TestNotesReportsBaseCollisions(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
		wantNotes    []noteWant
		// path and wantKind pin the surviving meaning of the colliding segment.
		path         string
		wantKind     routing.Kind
		wantSegments []string
		wantSlug     string
	}{
		{
			// Req 4.4a's first clause. Both options renamed to the same segment
			// -- one surface has to lose, and the category keeps it, so the tag
			// archive is the surface that degrades.
			name:         "category_base equal to tag_base",
			structure:    presetPostName,
			categoryBase: "sections",
			tagBase:      "sections",
			wantNotes: []noteWant{{
				why:      "category_base and tag_base resolving to the same segment",
				contains: []string{"category_base", "tag_base", "sections"},
			}},
			path:         "/sections/news",
			wantKind:     routing.KindCategory,
			wantSegments: []string{"news"},
		},
		{
			// Req 4.4a's second clause, category side. "author" is not a
			// resolved option (Req 4.3 -- WordPress stores no author_base), so
			// the author archive has no way to move out of the way and is simply
			// unreachable while the rename stands.
			name:         "category_base equal to author",
			structure:    presetPostName,
			categoryBase: routing.AuthorBase,
			wantNotes: []noteWant{{
				why:      "category_base colliding with the author base",
				contains: []string{"category_base", routing.AuthorBase},
			}},
			path:         "/author/alice",
			wantKind:     routing.KindCategory,
			wantSegments: []string{"alice"},
		},
		{
			// Req 4.4a's second clause, tag side. The tag row precedes the
			// author row, so the segment reads as a tag here -- there is no
			// category reading to keep, because category_base still resolves to
			// its own default.
			name:      "tag_base equal to author",
			structure: presetPostName,
			tagBase:   routing.AuthorBase,
			wantNotes: []noteWant{{
				why:      "tag_base colliding with the author base",
				contains: []string{"tag_base", routing.AuthorBase},
			}},
			path:     "/author/alice",
			wantKind: routing.KindTag,
			wantSlug: "alice",
		},
		{
			// Both clauses at once: the two taxonomy bases collide with each
			// other *and* with the author base. Two independent faults, so two
			// notes -- an implementation that returns on the first collision it
			// finds reports only one and leaves an operator fixing half the
			// problem.
			name:         "both bases equal to author",
			structure:    presetPostName,
			categoryBase: routing.AuthorBase,
			tagBase:      routing.AuthorBase,
			wantNotes: []noteWant{
				{
					why:      "category_base and tag_base resolving to the same segment",
					contains: []string{"category_base", "tag_base", routing.AuthorBase},
				},
				{
					why:      "category_base colliding with the author base",
					contains: []string{"category_base", routing.AuthorBase},
				},
				{
					why:      "tag_base colliding with the author base",
					contains: []string{"tag_base", routing.AuthorBase},
				},
			},
			path:         "/author/alice",
			wantKind:     routing.KindCategory,
			wantSegments: []string{"alice"},
		},
		{
			// Req 4.11 and 4.4a together: the collision is decided on the
			// *normalized* values, since the stored shape of a base set through
			// WordPress's admin UI carries a leading slash. An implementation
			// comparing the raw option values reports nothing here and leaves
			// the collision silent.
			name:         "collision is detected after normalization",
			structure:    presetPostName,
			categoryBase: "/sections/",
			tagBase:      "  sections  ",
			wantNotes: []noteWant{{
				why:      "category_base and tag_base resolving to the same segment after normalization",
				contains: []string{"category_base", "tag_base", "sections"},
			}},
			path:         "/sections/news",
			wantKind:     routing.KindCategory,
			wantSegments: []string{"news"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := parseUsable(t, tc.structure, tc.categoryBase, tc.tagBase)
			assertNotes(t, s.Notes(), tc.wantNotes)

			got := s.Classify(tc.path)
			if got.Kind != tc.wantKind {
				t.Fatalf("Classify(%q) Kind = %s, want %s -- the colliding segment's "+
					"surviving meaning is Classify's precedence table's decision, and "+
					"the note has to describe the same outcome (Req 4.4a, 4.4c)",
					tc.path, kindName(got.Kind), kindName(tc.wantKind))
			}
			if tc.wantSlug != "" && got.Slug != tc.wantSlug {
				t.Errorf("Classify(%q) Slug = %q, want %q", tc.path, got.Slug, tc.wantSlug)
			}
			if len(tc.wantSegments) > 0 && !slices.Equal(got.Segments, tc.wantSegments) {
				t.Errorf("Classify(%q) Segments = %q, want %q",
					tc.path, got.Segments, tc.wantSegments)
			}
		})
	}
}

// Requirement 4.12. A base that normalizes to more than one segment is used, not
// rejected -- and reported, so the oddity is visible rather than something an
// operator discovers from an archive URL that looks two segments too long.
//
// The path-construction and pattern halves of 4.12 are asserted by
// TestParseNormalizesArchiveBases and TestArchivePatterns; what is left here is
// the note.
func TestNotesReportsMultiSegmentBase(t *testing.T) {
	cases := []struct {
		name         string
		categoryBase string
		tagBase      string
		wantNotes    []noteWant
	}{
		{
			name:         "multi segment category base",
			categoryBase: "topics/news",
			wantNotes: []noteWant{{
				why:      "category_base normalizing to more than one segment",
				contains: []string{"category_base", "topics/news"},
			}},
		},
		{
			name:    "multi segment tag base",
			tagBase: "labels/lang",
			wantNotes: []noteWant{{
				why:      "tag_base normalizing to more than one segment",
				contains: []string{"tag_base", "labels/lang"},
			}},
		},
		{
			// The admin-UI shape again (Req 4.11): the note names the
			// *normalized* value, because that is the value the patterns, the
			// classifier and the constructors all consume. A note quoting
			// "//topics//news/" would describe a base that is not the one being
			// served.
			name:         "the note names the normalized value",
			categoryBase: "//topics//news/",
			wantNotes: []noteWant{{
				why:      "category_base normalizing to more than one segment",
				contains: []string{"category_base", "topics/news"},
			}},
		},
		{
			// Both bases, so two notes. Nothing collides here -- "topics/news"
			// and "labels/lang" share no segment -- so a collision note would be
			// a false report.
			name:         "both bases multi segment",
			categoryBase: "topics/news",
			tagBase:      "labels/lang",
			wantNotes: []noteWant{
				{
					why:      "category_base normalizing to more than one segment",
					contains: []string{"category_base", "topics/news"},
				},
				{
					why:      "tag_base normalizing to more than one segment",
					contains: []string{"tag_base", "labels/lang"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := parseUsable(t, presetPostName, tc.categoryBase, tc.tagBase)
			assertNotes(t, s.Notes(), tc.wantNotes)
		})
	}
}

// Requirements 4.4b and 9.7. A base equal to a leading literal of the permalink
// structure keeps the **post** meaning, and the note is the only thing that tells
// an operator why their archive base stopped working.
//
// The classification half is already asserted by
// TestClassifyBaseCollidingWithFrontKeepsPostMeaning; this is the loud half. It
// matters more here than in the other two collisions, because chi registers
// /archives/{post_id} alongside /archives/* without complaining -- probed
// directly -- so with no note the configuration is silently half-broken: every
// post URL keeps working and the archive base simply never matches, with nothing
// anywhere saying so.
//
// The note names the surviving interpretation as well as the collision, since
// "category_base collides with the permalink structure" without "the post
// interpretation wins" leaves the operator unable to tell whether their posts or
// their archives are the casualty -- and Req 4.4b's whole point is that the two
// outcomes are not interchangeable.
func TestNotesReportsBaseCollidingWithFront(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
		wantNotes    []noteWant
	}{
		{
			// WordPress's own Numeric preset against the rename an operator
			// makes precisely *because* their URLs live under /archives/.
			name:         "category_base equal to the structure's front",
			structure:    presetNumeric,
			categoryBase: "archives",
			wantNotes: []noteWant{{
				why:      "category_base colliding with the structure's leading literal",
				contains: []string{"category_base", "archives", "post"},
			}},
		},
		{
			name:      "tag_base equal to the structure's front",
			structure: presetNumeric,
			tagBase:   "archives",
			wantNotes: []noteWant{{
				why:      "tag_base colliding with the structure's leading literal",
				contains: []string{"tag_base", "archives", "post"},
			}},
		},
		{
			// A two-segment front, whose *first* segment is the one a base
			// collides with (Req 4.4b: "the front, or its first segment when the
			// front is longer than one segment"). An implementation comparing
			// only against the joined front reports nothing here, and
			// Classify -- which already handles both shapes -- would then be
			// declining the base with no diagnostic anywhere.
			name:         "category_base equal to the first segment of a longer front",
			structure:    presetTwoSegmentFront,
			categoryBase: "blog",
			wantNotes: []noteWant{{
				why:      "category_base colliding with the first segment of the front",
				contains: []string{"category_base", "blog", "post"},
			}},
		},
		{
			// The whole front as one base value, which is the other shape
			// baseCollidesWithFront accepts.
			name:         "category_base equal to the whole multi segment front",
			structure:    presetTwoSegmentFront,
			categoryBase: "blog/news",
			wantNotes: []noteWant{
				{
					why:      "category_base colliding with the structure's leading literals",
					contains: []string{"category_base", "blog/news", "post"},
				},
				{
					// Req 4.12 is independent of the collision, so both
					// conditions are reported.
					why:      "category_base normalizing to more than one segment",
					contains: []string{"category_base", "blog/news"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := parseUsable(t, tc.structure, tc.categoryBase, tc.tagBase)
			assertNotes(t, s.Notes(), tc.wantNotes)
		})
	}
}

// Requirement 4.5, from the other side: a configuration with nothing wrong
// reports nothing.
//
// This is the row that keeps Notes() worth reading. A note emitted for every
// parsed Structure -- or for every structure that merely *has* a front, or for
// every base that happens to equal its default -- is a WARN an operator learns
// to ignore, and the collisions above stop being visible at all. It is also what
// Req 10.2 leans on when it limits the REST "link" 200 guarantee to
// "configurations that Notes() reports as clean".
func TestNotesEmptyWhenNothingCollides(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
	}{
		{
			// Plain permalinks and no base options, which is what the live
			// reference database stores.
			name: "flat structure with no base options",
		},
		{
			name:      "default bases with no front",
			structure: presetPostName,
		},
		{
			// Set explicitly, distinct from each other, distinct from "author",
			// and no front to collide with.
			name:         "distinct non default bases",
			structure:    presetPostName,
			categoryBase: "sections",
			tagBase:      "topics",
		},
		{
			// Bases set to the default strings are "set" (Req 4.9) but collide
			// with nothing, so the front they drop is not a fault to report.
			name:         "bases set to the default strings under a front",
			structure:    presetFrontMonthAndName,
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
		},
		{
			// Req 4.11: normalization is not a diagnostic. A single-segment base
			// stored in the admin-UI shape is served correctly, so an operator
			// has nothing to act on.
			name:         "bases needing normalization but resolving to one segment",
			structure:    presetPostName,
			categoryBase: "/topics/",
			tagBase:      "  labels  ",
		},
		{
			// A whitespace-only base is unset (Req 4.13), so it resolves to the
			// default and collides with nothing -- notably not with tag_base,
			// which is also unset and resolves to its own different default.
			name:         "whitespace only bases fall back to distinct defaults",
			structure:    presetFrontMonthAndName,
			categoryBase: " ",
			tagBase:      "\t",
		},
		{
			// A front the bases do not touch: the structure's literal is "blog"
			// and the bases are "sections" and "topics", so 4.4b does not fire.
			name:         "a front that no base collides with",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
		},
		{
			// The Numeric preset with no base rename at all -- the structure
			// whose front makes 4.4b reachable, in the configuration where
			// nothing is wrong with it.
			name:      "the numeric preset with default bases",
			structure: presetNumeric,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					tc.structure, tc.categoryBase, tc.tagBase, err)
			}
			if notes := s.Notes(); len(notes) != 0 {
				t.Errorf("Notes() = %q, want none -- a note on a clean configuration "+
					"is noise that hides the collisions Notes() exists to report",
					notes)
			}
		})
	}
}

// An unsupported structure is reported through Parse's error, so the flat
// fallback it returns still has to answer Notes() -- and still has to answer it
// about the bases, which Parse resolves before it ever looks at the structure.
//
// Two claims, both about the fallback Structure rather than about the error:
// Notes() does not panic on it, and the base collision it carries is still
// reported. The fallback keeps serving the flat /category/{slug} and
// /tag/{slug} routes (ArchivePatterns returns them for a Flat structure), so a
// collision between those two bases is just as real there as under a parsed
// structure. What cannot be reported is a 4.4b front collision: the fallback has
// no front at all, so there is no leading literal for a base to collide with.
func TestNotesOnUnsupportedStructureFallback(t *testing.T) {
	const unsupported = "/%category%/%postname%/"

	s, err := routing.Parse(unsupported, "sections", "sections")
	if err == nil {
		t.Fatalf("Parse(%q, ...) error = nil, want an ErrUnsupported wrapper", unsupported)
	}
	if !s.Flat {
		t.Fatalf("Parse(%q, ...) returned a non-Flat Structure alongside its error", unsupported)
	}

	assertNotes(t, s.Notes(), []noteWant{{
		why:      "category_base and tag_base colliding on the flat fallback",
		contains: []string{"category_base", "tag_base", "sections"},
	}})
}
