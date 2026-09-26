package routing

import (
	"slices"
	"strings"
)

// Kind is what a request path addresses.
type Kind int

const (
	// KindNone is the total function's escape hatch: the path carries no
	// archive and no post interpretation, so the caller 404s. Classify never
	// returns an error, because "this path means nothing" is an answer rather
	// than a failure, and an error return would invite a caller to treat an
	// ordinary 404 as a server fault.
	KindNone Kind = iota
	KindPost
	KindCategory
	KindTag
	KindAuthor
	KindDate
)

// Target is the classified meaning of one request path.
type Target struct {
	Kind Kind
	// Post is set when Kind is KindPost.
	Post Ref
	// Segments is the category path as given, base segment excluded, root
	// first. Set when Kind is KindCategory. The handler walks it through the
	// taxonomy graph; a walk that fails is what produces the 301 or the 404, so
	// the classifier makes no claim about whether these segments name anything.
	Segments []string
	// Slug is the tag slug or the author nicename, per Kind.
	Slug string
	// Date is set when Kind is KindDate.
	Date DateRef
	// TrailingSlash records the form the request arrived in, not the canonical
	// form, so the handler can canonicalise it without re-parsing the path. A
	// classifier that reported the canonical form instead would leave the
	// handler redirecting every already-canonical archive URL to itself.
	TrailingSlash bool
}

// Classify decides what path addresses, applying the fixed precedence order of
// design.md's precedence table: the rows are evaluated top to bottom and the
// first match wins.
//
// It is pure and total: an unrecognised path yields KindNone rather than an
// error. Precedence lives here rather than in the order of route registrations
// because chi resolves a collision at a parameter node silently in favour of
// whichever pattern was registered last (M9a probed this and recorded it in
// router.go's route-registration comment), which makes registration-order
// precedence unreviewable. Every ambiguous pattern is registered to one handler
// precisely so that which pattern chi picks has no observable effect.
//
// Each archive row tests its **own** expected prefix, computed by archivePrefix
// -- the same helper the four path constructors call, so a constructor and the
// classifier cannot disagree about whether a front applies. Stripping the front
// once, up front, would be wrong in both directions: with front "blog" and
// category_base set to "sections", /sections/news is a category path and
// /blog/sections/news is not, while /blog/2024 is a date archive in the same
// structure.
func (s Structure) Classify(path string) Target {
	t := Target{TrailingSlash: path != "/" && strings.HasSuffix(path, "/")}

	segs := pathSegments(path)
	if len(segs) == 0 {
		// Row 1. The "/" route is registered before the dispatcher and owns the
		// home page (Req 9.6), so claiming it here would take it away.
		return t
	}

	// Rows 2 and 3 -- the category base, then at least one further segment.
	// Nothing further is KindNone rather than a fall-through: WordPress serves
	// no category index (Req 2.8).
	if rest, ok := s.archiveRest(segs, archiveCategory, s.CategoryBase); ok {
		if len(rest) == 0 {
			return t
		}
		t.Kind = KindCategory
		t.Segments = slices.Clone(rest)
		return t
	}

	// Row 4 -- the tag base and exactly one further segment. post_tag is
	// non-hierarchical, so a deeper path is not a nested tag (Req 5.2).
	//
	// Rows 4 and 5 carry row 3's clause as well: a path rooted at a base
	// segment names that archive or names nothing, and never falls through to
	// the post rows. design.md's table spells the zero-further-segment case out
	// for the category row only, but read strictly that would let /tag resolve
	// as a post slugged "tag" under a single-segment structure -- the one thing
	// Req 9.4 and design.md's own consequences list say must not happen ("a
	// post slugged identically to one of those base segments is unreachable").
	if rest, ok := s.archiveRest(segs, archiveTag, s.TagBase); ok {
		if len(rest) != 1 {
			return t
		}
		t.Kind = KindTag
		t.Slug = rest[0]
		return t
	}

	// Row 5 -- the author base, which is the constant AuthorBase because
	// WordPress stores no author_base option (Req 4.3).
	if rest, ok := s.archiveRest(segs, archiveAuthor, AuthorBase); ok {
		if len(rest) != 1 {
			return t
		}
		t.Kind = KindAuthor
		t.Slug = rest[0]
		return t
	}

	// Row 6 -- date archives, **before** posts, matching WordPress's own
	// rewrite-rule ordering (Req 9.3). A shape that is not a real date declines
	// the row rather than ending the walk, so /blog/hello-world still reaches
	// the post rows.
	if s.servesDateArchives() {
		if rest, ok := trimSegmentPrefix(segs, s.archivePrefix(archiveDate)); ok {
			if d, ok := dateFromSegments(rest); ok {
				t.Kind = KindDate
				t.Date = d
				return t
			}
		}
	}

	// Row 7 -- the structure's own shape. This is M9a's post resolution
	// unchanged, and the structure already carries its own front, so no archive
	// prefix applies. It runs after the base rows, which is what makes Req
	// 9.7/4.4b hold: a base that collides with the structure's front is
	// skipped above, so /archives/123 under the Numeric preset stays post 123
	// instead of 404ing every post URL on the site as an unresolvable category.
	if params, ok := s.ParamsFromPath(path); ok {
		if ref, ok := s.Match(params); ok {
			t.Kind = KindPost
			t.Post = ref
			return t
		}
	}

	// Row 8 -- the flat /{slug} fallback, which is what makes M9a's
	// flat-to-canonical 301 reachable.
	//
	// One narrowing: a lone segment carrying a date archive's own shape is not
	// claimed here when the structure serves date archives at all. Under every
	// front-less structure row 6 already claims a bare 4-digit segment, which
	// is Req 9.3's documented consequence -- a post slugged "2024" is
	// unreachable, exactly as in WordPress -- and letting the flat row claim it
	// only when the structure happens to carry a front, or a "date/"
	// disambiguation segment, would make that consequence depend on the front
	// rather than on the date grammar. A Flat structure serves no date archives
	// at all (Req 6.8), so the narrowing does not apply there and /2024 stays
	// post "2024" exactly as it resolves today (Req 9.8).
	if len(segs) == 1 {
		if _, dateShaped := dateFromSegments(segs); dateShaped && s.servesDateArchives() {
			return t
		}
		t.Kind = KindPost
		t.Post = Ref{Slug: segs[0]}
		return t
	}

	// Row 9 -- anything else.
	return t
}

// archiveRest reports whether segs is rooted at an archive kind's expected
// prefix -- the front where that kind carries it, plus the kind's base
// segments -- and returns what follows it.
//
// The base is skipped entirely when it collides with a leading literal of the
// permalink structure (Req 4.4b, 9.7). WordPress's own Numeric preset is
// /archives/%post_id%, and an operator renames category_base to "archives"
// precisely *because* their URLs live under /archives/; giving the base
// priority there would classify /archives/123 as a category, resolve no
// category, and 404 every post URL on the site from its own canonical
// permalink. The milestone's premise is that an existing site's published URLs
// keep working, so the archive base is the surface that degrades -- loudly,
// through Structure.Notes().
func (s Structure) archiveRest(segs []string, k archiveKind, base string) ([]string, bool) {
	if s.baseCollidesWithFront(base) {
		return nil, false
	}
	want := s.archivePrefix(k)
	want = append(want, baseSegments(base)...)
	return trimSegmentPrefix(segs, want)
}

// baseCollidesWithFront reports whether a resolved archive base equals a leading
// literal segment of the permalink structure: the front itself, or the front's
// first segment when the front is longer than one segment (Req 4.4b).
//
// chi does not catch this -- /archives/{post_id} alongside /archives/* registers
// without panicking, probed directly -- so the classifier is the only place the
// collision can be resolved reviewably.
func (s Structure) baseCollidesWithFront(base string) bool {
	if base == "" || len(s.front) == 0 {
		return false
	}
	return base == s.front[0] || base == strings.Join(s.front, "/")
}

// servesDateArchives reports whether this structure serves date archives at all.
// It mirrors DatePath's own gate, so the classifier and the constructor agree:
// a Flat structure -- WordPress's plain permalinks, which register no rewrite
// rules and serve ?m=2024 -- has no date archive to address (Req 6.8, 9.8).
func (s Structure) servesDateArchives() bool {
	return !s.Flat && len(s.segments) > 0
}

// dateFromSegments reads 1 to 3 segments as a date archive at WordPress's
// widths: a 4-digit year and zero-padded 2-digit month and day (Req 6.2). It
// returns false for any other count, for a component of the wrong width, and
// for components that name no real calendar date (Req 6.3).
//
// A present-but-zero month or day is rejected here rather than in Range, which
// reads 0 as "this granularity was not supplied" -- without that check /2024/00
// would widen to the whole-year archive instead of 404ing.
//
// The calendar check itself is delegated to DateRef.Range, so month lengths and
// the leap rule live in exactly one place.
func dateFromSegments(segs []string) (DateRef, bool) {
	if len(segs) == 0 || len(segs) > 3 {
		return DateRef{}, false
	}
	var d DateRef
	year, ok := fixedWidthInt(segs[0], 4)
	if !ok {
		return DateRef{}, false
	}
	d.Year = year
	if len(segs) > 1 {
		month, ok := fixedWidthInt(segs[1], 2)
		if !ok || month == 0 {
			return DateRef{}, false
		}
		d.Month = month
	}
	if len(segs) > 2 {
		day, ok := fixedWidthInt(segs[2], 2)
		if !ok || day == 0 {
			return DateRef{}, false
		}
		d.Day = day
	}
	if _, _, ok := d.Range(); !ok {
		return DateRef{}, false
	}
	return d, true
}

// pathSegments splits a request path into its non-empty segments, so the two
// slash forms and a doubled slash all reduce to the same segment list and only
// Target.TrailingSlash records which form arrived.
func pathSegments(path string) []string {
	parts := strings.Split(path, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

// trimSegmentPrefix reports whether segs starts with prefix and returns the
// remainder. An empty prefix always matches, which is what makes a front-less
// structure -- and every Flat one -- reduce to the shapes grimoire serves today.
func trimSegmentPrefix(segs, prefix []string) ([]string, bool) {
	if len(segs) < len(prefix) {
		return nil, false
	}
	for i, want := range prefix {
		if segs[i] != want {
			return nil, false
		}
	}
	return segs[len(prefix):], true
}
