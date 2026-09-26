// Package routing turns a WordPress permalink_structure option into the two
// operations the web layer needs: matching a request path to a post identifier,
// and building a post's one canonical path.
//
// The package is deliberately pure. It depends on neither storage nor HTTP, so
// the permalink grammar can be exercised exhaustively in unit tests with no
// database and no server. The only outside type it touches is domain.Post, and
// only to read fields.
//
// Supported tokens are %postname%, %post_id%, %year%, %monthnum% and %day%.
// %category% and %author% are out of scope: both require resolving a term or
// user *during* path matching, which is a materially different problem, and
// neither appears in a WordPress preset.
package routing

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// ErrUnsupported reports a permalink_structure this package cannot serve. It is
// returned alongside a usable flat Structure so the caller can log the problem
// and keep serving the flat route rather than failing to start.
var ErrUnsupported = errors.New("unsupported permalink structure")

// Default archive base segments, matching WordPress when the corresponding
// option is unset.
const (
	DefaultCategoryBase = "category"
	DefaultTagBase      = "tag"
)

// AuthorBase is the author archive's base segment. It is a constant rather than
// a resolved option because WordPress stores no author_base option -- it is a
// WP_Rewrite property, not a row in {prefix}options -- so there is nothing to
// read and nothing a taxonomy base override can move. The constructor, the
// classifier and the base-collision check all read this one value.
const AuthorBase = "author"

// datePrefixSegment is WordPress's date-archive disambiguation segment, added
// when %post_id% appears among the structure's first three tokens. See
// datePrefixed.
const datePrefixSegment = "date"

// token is one recognised permalink placeholder.
type token int

const (
	tokLiteral token = iota // a fixed path segment, e.g. "archives"
	tokPostname
	tokPostID
	tokYear
	tokMonth
	tokDay
)

// tokenNames maps the permalink_structure spelling to its token. The chi
// parameter name is the spelling without the percent signs, so a structure
// reads almost identically to the route pattern it produces.
var tokenNames = map[string]token{
	"%postname%": tokPostname,
	"%post_id%":  tokPostID,
	"%year%":     tokYear,
	"%monthnum%": tokMonth,
	"%day%":      tokDay,
}

// paramName is the chi URL-parameter name for a token.
func (t token) paramName() string {
	switch t {
	case tokPostname:
		return "postname"
	case tokPostID:
		return "post_id"
	case tokYear:
		return "year"
	case tokMonth:
		return "monthnum"
	case tokDay:
		return "day"
	}
	return ""
}

// segment is one path segment: either a literal or a token.
type segment struct {
	tok     token
	literal string // set only when tok == tokLiteral
}

// Structure is a parsed, validated permalink_structure.
type Structure struct {
	// Raw is the option value exactly as read, for logging and diagnostics.
	Raw string
	// Flat reports that no usable structure is configured -- either the option
	// is empty (WordPress's "plain" setting) or it was unsupported and this is
	// the fallback. When Flat, the flat /{slug} route is canonical and no
	// canonical redirects are issued.
	Flat bool
	// TrailingSlash mirrors whether Raw ends in "/". It decides which of the
	// two slash forms is canonical, so the other can be redirected instead of
	// silently serving the same content at two URLs.
	TrailingSlash bool
	// CategoryBase and TagBase are the resolved archive base segments,
	// normalized: surrounding whitespace and slashes trimmed and repeated
	// internal slashes collapsed, so a stored "/topics" is served as "topics"
	// (Req 4.11). A base that normalizes to more than one segment is kept as
	// such and used consistently everywhere a base is consumed (Req 4.12).
	CategoryBase string
	TagBase      string

	// CategoryBaseSet and TagBaseSet report that the corresponding option
	// arrived non-empty, i.e. WordPress's get_option() would have been truthy.
	// They are NOT "differs from the default": an option explicitly set to
	// "category" is set, and WordPress drops the front for it
	// (create_initial_taxonomies() registers each taxonomy with
	// with_front => ! get_option( '{taxonomy}_base' )), so a comparison against
	// DefaultCategoryBase would get that case wrong.
	//
	// These exist only to decide whether the front applies to the category and
	// tag archive paths (Req 4.8, 4.9). Nothing else reads them.
	CategoryBaseSet bool
	TagBaseSet      bool

	segments []segment

	// front is the structure's leading literal segments -- "blog" in
	// /blog/%year%/%monthnum%/%postname%/ -- as the maximal run of literals
	// before the first token. Both words matter: a literal appearing *after* a
	// token belongs to the post path only, never to an archive path.
	//
	// Unexported because only the archive path constructors and Classify inside
	// this package consume it; no caller has a reason to read it. Always empty
	// for a Flat structure, including the unsupported-structure fallback, which
	// serves no archive at a front at all.
	front []string

	// notes are the non-fatal diagnostics Parse recorded about this
	// configuration, read through Notes(). Unexported and copied on the way out
	// so a caller logging them cannot alter what the next caller reads.
	notes []string
}

// Parse parses a permalink_structure together with the category_base and
// tag_base options. Base values are normalized first (Req 4.11 -- see
// normalizeBase). Empty base values fall back to WordPress's defaults, and
// whether each one arrived non-empty is recorded on the returned Structure as
// CategoryBaseSet/TagBaseSet -- the empty case and the explicitly-set-to-the-
// default case resolve to the same string and still have to be distinguishable
// afterwards, because WordPress drops the front for the latter (Req 4.9).
//
// The signature stays (structure, categoryBase, tagBase string) deliberately.
// content.OptionService maps an absent option to the empty string, so the empty
// string arriving here *is* WordPress's falsy get_option(); an options struct or
// *string parameters would churn every one of the twenty-odd call sites for no
// behavioral gain.
//
// An empty structure yields a flat Structure and a nil error. A structure that
// contains an unrecognised token, or that carries no token capable of
// identifying a single post, yields a flat Structure and an error wrapping
// ErrUnsupported whose message names the specific problem -- the caller is
// expected to log it and continue with the returned flat Structure.
func Parse(structure, categoryBase, tagBase string) (Structure, error) {
	s := Structure{Raw: structure}
	s.CategoryBase, s.CategoryBaseSet = resolveBase(categoryBase, DefaultCategoryBase)
	s.TagBase, s.TagBaseSet = resolveBase(tagBase, DefaultTagBase)
	// Base collisions and multi-segment bases are decidable from the resolved
	// bases alone, so they are recorded before the flat fallback is copied: that
	// fallback still serves the category and tag routes, so it has to answer
	// Notes() about its bases too (Req 4.4a, 4.5, 4.12).
	s.notes = s.baseOptionNotes()
	flat := s
	flat.Flat = true

	trimmed := strings.TrimSpace(structure)
	if trimmed == "" {
		return flat, nil
	}

	s.TrailingSlash = strings.HasSuffix(trimmed, "/")
	flat.TrailingSlash = false

	var unsupported []string
	hasIdentifier := false
	for _, raw := range strings.Split(strings.Trim(trimmed, "/"), "/") {
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "%") && strings.HasSuffix(raw, "%") {
			tok, ok := tokenNames[raw]
			if !ok {
				unsupported = append(unsupported, raw)
				continue
			}
			if tok == tokPostname || tok == tokPostID {
				hasIdentifier = true
			}
			s.segments = append(s.segments, segment{tok: tok})
			continue
		}
		s.segments = append(s.segments, segment{tok: tokLiteral, literal: raw})
	}

	if len(unsupported) > 0 {
		return flat, fmt.Errorf("%w %q: token(s) %s are not supported",
			ErrUnsupported, structure, strings.Join(unsupported, ", "))
	}
	if !hasIdentifier {
		return flat, fmt.Errorf("%w %q: contains no identifying token "+
			"(%%postname%% or %%post_id%%), so it cannot address a single post",
			ErrUnsupported, structure)
	}
	s.front = leadingLiterals(s.segments)
	// The front collision (Req 4.4b) is the one diagnostic that needs the parsed
	// structure, so it is appended once the front exists. slices.Concat rather
	// than append, because flat above shares this slice's backing array.
	s.notes = slices.Concat(s.notes, s.frontCollisionNotes())
	return s, nil
}

// leadingLiterals returns the maximal run of literal segments before the first
// token. The run stops at the first token, so a literal that follows one is not
// part of the front: /%year%/blog/%postname%/ has no front, and stripping every
// literal instead would advertise /blog/category/news on a site that serves
// /category/news.
func leadingLiterals(segs []segment) []string {
	var out []string
	for _, seg := range segs {
		if seg.tok != tokLiteral {
			break
		}
		out = append(out, seg.literal)
	}
	return out
}

// ChiPatterns returns the chi route patterns for this structure: the form
// without a trailing slash and the form with one, in that order. Empty when
// Flat.
//
// Both are returned, and both must be registered, because chi matches the two
// slash forms as distinct routes. Registering only the canonical one would make
// chi 404 the other before any handler could redirect it, which is exactly the
// duplicate-URL problem TrailingSlash exists to resolve.
func (s Structure) ChiPatterns() []string {
	if s.Flat || len(s.segments) == 0 {
		return nil
	}
	var b strings.Builder
	for _, seg := range s.segments {
		b.WriteByte('/')
		if seg.tok == tokLiteral {
			b.WriteString(seg.literal)
			continue
		}
		b.WriteByte('{')
		b.WriteString(seg.tok.paramName())
		b.WriteByte('}')
	}
	base := b.String()
	return []string{base, base + "/"}
}

// Ref identifies a post extracted from a request path.
type Ref struct {
	// Slug is set when the structure carries %postname%.
	Slug string
	// ID is set when the structure carries %post_id%.
	ID int64
	// Year, Month and Day are 0 when the structure carries no date tokens.
	// When non-zero they must be checked against the resolved post's date, so
	// a post is not served at a date path that is not its own.
	Year, Month, Day int
}

// HasDate reports whether the structure supplied date components that a caller
// should verify against the resolved post.
func (r Ref) HasDate() bool { return r.Year != 0 }

// DateRef is a date archive at year, year/month or year/month/day granularity.
// Month and Day are 0 when the path did not carry them.
type DateRef struct{ Year, Month, Day int }

// Range returns the half-open [start, end) interval the archive covers, and
// false when the components do not form a real calendar date.
//
// Half-open is what keeps a post out of two archives at once: the start instant
// is included and the end instant is the first one excluded, so a post published
// exactly on a month boundary belongs to the later month only.
//
// The interval is built with time.Date(..., time.UTC), which is what "no
// timezone conversion" requires here: formatTS is
// `return t.UTC().Format(tsLayout)` (internal/storage/wprepo/helpers.go), so a
// UTC-constructed bound passes through it unchanged, and post_date itself is
// UTC-based wall-clock time because parseTS reads it with
// time.ParseInLocation(layout, s, time.UTC) followed by .UTC(). That is the same
// no-conversion basis Canonical reads post.Date on. Building the interval in
// time.Local instead would shift every bound by the host's offset: on a UTC-7
// host a May 2024 archive would query 2024-05-01 07:00:00 ..
// 2024-06-01 07:00:00, dropping the first seven hours of May 1 and including the
// last seven of April 30.
func (d DateRef) Range() (start, end time.Time, ok bool) {
	if d.Year <= 0 || d.Month < 0 || d.Month > 12 || d.Day < 0 {
		return time.Time{}, time.Time{}, false
	}
	// A day without a month names no calendar date, so it cannot produce an
	// interval -- and silently widening it to the whole year would serve a
	// nonsense path 200 instead of 404.
	if d.Month == 0 && d.Day != 0 {
		return time.Time{}, time.Time{}, false
	}

	switch {
	case d.Month == 0:
		start = time.Date(d.Year, time.January, 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(1, 0, 0), true
	case d.Day == 0:
		start = time.Date(d.Year, time.Month(d.Month), 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, 0), true
	}

	start = time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
	// time.Date normalises out-of-range components rather than failing, so
	// 2024/02/30 comes back as 2024-03-01. Reading the components back is how an
	// impossible date is rejected without hard-coding month lengths or a leap
	// rule, and it accepts a real leap day for exactly the same reason.
	if start.Year() != d.Year || int(start.Month()) != d.Month || start.Day() != d.Day {
		return time.Time{}, time.Time{}, false
	}
	return start, start.AddDate(0, 0, 1), true
}

// Match extracts a Ref from a map of URL parameters, as produced by chi or by
// ParamsFromPath. It returns false when any component fails its shape rule:
// dates must be zero-padded to WordPress's widths and be calendar-plausible, an
// id must be a positive integer, and a slug must be non-empty.
//
// Shape is enforced here rather than in the route pattern because chi patterns
// have no width or range syntax, and because a wrong-shaped path should 404
// rather than reach a database query.
// The len(s.segments) check mirrors ChiPatterns and Canonical. Without it a
// zero-value Structure -- which is not Flat, because false is the zero value of
// a bool -- would iterate no segments and report a successful match carrying an
// empty Ref, handing the caller an identifier-less "hit".
func (s Structure) Match(params map[string]string) (Ref, bool) {
	if s.Flat || len(s.segments) == 0 {
		return Ref{}, false
	}
	var ref Ref
	for _, seg := range s.segments {
		if seg.tok == tokLiteral {
			continue
		}
		raw := params[seg.tok.paramName()]
		switch seg.tok {
		case tokPostname:
			if raw == "" {
				return Ref{}, false
			}
			ref.Slug = raw
		case tokPostID:
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return Ref{}, false
			}
			ref.ID = id
		case tokYear:
			v, ok := fixedWidthInt(raw, 4)
			if !ok || v == 0 {
				return Ref{}, false
			}
			ref.Year = v
		case tokMonth:
			v, ok := fixedWidthInt(raw, 2)
			if !ok || v < 1 || v > 12 {
				return Ref{}, false
			}
			ref.Month = v
		case tokDay:
			v, ok := fixedWidthInt(raw, 2)
			if !ok || v < 1 || v > 31 {
				return Ref{}, false
			}
			ref.Day = v
		}
	}
	return ref, true
}

// Canonical builds the one canonical path for a post.
//
// This is the single place a permalink is constructed. The canonical-redirect
// target and the REST API's "link" field both come from here, so the two cannot
// disagree, and the no-redirect-loop property follows from Canonical being a
// pure function of the post: a request already at Canonical(p) renders, and
// everything else redirects to it.
//
// Date components come from the post's stored date without timezone
// conversion. WordPress stores post_date in site-local time, so shifting to UTC
// would move a post published just after local midnight to the previous day and
// permanently redirect it away from its real URL.
func (s Structure) Canonical(p domain.Post) string {
	if s.Flat || len(s.segments) == 0 {
		return "/" + p.Slug
	}
	var b strings.Builder
	for _, seg := range s.segments {
		b.WriteByte('/')
		switch seg.tok {
		case tokLiteral:
			b.WriteString(seg.literal)
		case tokPostname:
			b.WriteString(p.Slug)
		case tokPostID:
			b.WriteString(strconv.FormatInt(p.ID, 10))
		case tokYear:
			b.WriteString(fmt.Sprintf("%04d", p.Date.Year()))
		case tokMonth:
			b.WriteString(fmt.Sprintf("%02d", int(p.Date.Month())))
		case tokDay:
			b.WriteString(fmt.Sprintf("%02d", p.Date.Day()))
		}
	}
	if s.TrailingSlash {
		b.WriteByte('/')
	}
	return b.String()
}

// archiveKind names the four archive families whose paths this package builds.
// It exists so one helper can answer "what precedes this kind's own segments?"
// for all of them -- see archivePrefix.
type archiveKind int

const (
	archiveCategory archiveKind = iota
	archiveTag
	archiveAuthor
	archiveDate
)

// archivePrefix returns the segments that precede an archive kind's own base and
// components: the structure's front where the kind carries it, plus the "date/"
// disambiguation segment for date archives that need it.
//
// This is the single place the front asymmetry is decided, and Classify's
// per-row expected prefix comes from here too, so a constructor and the
// classifier cannot disagree about whether a front applies. That shared answer
// is what makes the canonical fixed-point property hold for front-carrying
// structures and not only for front-less ones.
//
// The asymmetry is WordPress's, replicated rather than simplified:
//
//	date   -- always   (get_date_permastruct(): $front . $date_endian)
//	author -- always   (get_author_permastruct(): $this->front . $this->author_base)
//	category/tag -- only when the corresponding base option is set to something
//	                falsy (create_initial_taxonomies(): with_front =>
//	                ! get_option( '{taxonomy}_base' ))
//
// A site that renamed its category base publishes /sections/news/local, not
// /blog/sections/news/local, so dropping the front there is what keeps its
// existing URLs working.
//
// A Flat structure has no segments and therefore no front, so every prefix is
// empty and archive paths reduce to the shapes grimoire serves today.
func (s Structure) archivePrefix(k archiveKind) []string {
	withFront := false
	switch k {
	case archiveDate, archiveAuthor:
		withFront = true
	case archiveCategory:
		withFront = !s.CategoryBaseSet
	case archiveTag:
		withFront = !s.TagBaseSet
	}

	var out []string
	if withFront {
		out = append(out, s.front...)
	}
	if k == archiveDate && s.datePrefixed() {
		out = append(out, datePrefixSegment)
	}
	return out
}

// datePrefixed reports whether date archives move under an extra "date/"
// segment: WP_Rewrite::get_date_permastruct()'s rule verbatim, which walks the
// structure's tokens with a 1-based index and, on finding %post_id% at index
// <= 3, sets $front = $front . 'date/'.
//
// The index counts tokens only -- preg_match_all( '/%.+?%/', ... ) never sees a
// literal -- so /blog/%year%/%monthnum%/%post_id%/ has %post_id% at token 3 and
// is prefixed even though it is the fourth segment.
//
// The rule exists to prevent a concrete collision: under a bare /%post_id%/
// structure, /2024 classifies as a year archive, which would make post 2024
// unreachable by its own canonical permalink. Its edges are WordPress's, not
// ours -- /%year%/%monthnum%/%post_id%/ (index 3) is prefixed and
// /%year%/%monthnum%/%day%/%post_id%/ (index 4) is not -- and diverging to "fix"
// the second would break URL compatibility with the database grimoire reads.
func (s Structure) datePrefixed() bool {
	index := 0
	for _, seg := range s.segments {
		if seg.tok == tokLiteral {
			continue
		}
		index++
		if index > 3 {
			return false
		}
		if seg.tok == tokPostID {
			return true
		}
	}
	return false
}

// CategoryPath builds the canonical path for a category archive. ancestry is
// root-first and ends in the category's own slug, so a top-level category is the
// one-element case and its path is byte-for-byte the flat /category/{slug} path
// grimoire serves today (Req 2.1, 2.4).
//
// This and the three constructors below it are the only places an archive URL is
// built, for the same reason Canonical is the only place a permalink is built:
// the redirect target, the theme's pagination links and the REST "link" field
// all come from here, so they cannot diverge.
//
// An empty ancestry yields "" rather than the bare base segment, because the
// base alone addresses no archive (Req 2.8).
func (s Structure) CategoryPath(ancestry []string) string {
	if len(ancestry) == 0 {
		return ""
	}
	segs := s.archivePrefix(archiveCategory)
	segs = append(segs, baseSegments(s.CategoryBase)...)
	segs = append(segs, ancestry...)
	return s.archivePath(segs)
}

// TagPath builds the canonical path for a tag archive. post_tag is
// non-hierarchical in WordPress, so a tag path is the base and one slug and
// never carries an ancestry (Req 5.1, 5.2).
func (s Structure) TagPath(slug string) string {
	if slug == "" {
		return ""
	}
	segs := s.archivePrefix(archiveTag)
	segs = append(segs, baseSegments(s.TagBase)...)
	segs = append(segs, slug)
	return s.archivePath(segs)
}

// AuthorPath builds the canonical path for an author archive. The base is the
// constant AuthorBase, which no option moves (Req 4.3), and the front always
// applies (Req 4.7).
func (s Structure) AuthorPath(nicename string) string {
	if nicename == "" {
		return ""
	}
	segs := s.archivePrefix(archiveAuthor)
	segs = append(segs, AuthorBase, nicename)
	return s.archivePath(segs)
}

// DatePath builds the canonical path for the date archive at the granularity d
// carries, at WordPress's widths: a 4-digit year and zero-padded 2-digit month
// and day, so a shape Match would reject is never advertised as canonical (Req
// 6.2).
//
// It returns "" in two cases. A Flat structure serves no date archives at all
// (Req 6.8) -- WordPress with plain permalinks registers no rewrite rules, its
// date archives are ?m=2024, and claiming /2024 here would take a URL that today
// resolves a post slugged "2024". And components that name no real calendar date
// have no archive to address, which is DateRef.Range's own emptiness test rather
// than a second copy of the calendar rules.
func (s Structure) DatePath(d DateRef) string {
	if s.Flat || len(s.segments) == 0 {
		return ""
	}
	if _, _, ok := d.Range(); !ok {
		return ""
	}
	segs := s.archivePrefix(archiveDate)
	segs = append(segs, fmt.Sprintf("%04d", d.Year))
	if d.Month != 0 {
		segs = append(segs, fmt.Sprintf("%02d", d.Month))
	}
	if d.Day != 0 {
		segs = append(segs, fmt.Sprintf("%02d", d.Day))
	}
	return s.archivePath(segs)
}

// archivePath joins archive segments into a path, applying the same
// trailing-slash rule Canonical applies to posts: whichever form
// permalink_structure implies is canonical and the other is redirected to it. A
// Flat structure carries no trailing slash, which preserves today's
// /category/{slug} byte for byte (Req 2.7).
func (s Structure) archivePath(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, seg := range segs {
		b.WriteByte('/')
		b.WriteString(seg)
	}
	if s.TrailingSlash {
		b.WriteByte('/')
	}
	return b.String()
}

// ArchivePatterns returns every chi pattern the archive routes need, derived
// from the resolved bases and the structure's front. Returned as a slice for the
// same reason ChiPatterns is: the caller registers all of them to one handler,
// so a collision at a chi parameter node has no observable effect and precedence
// stays in Classify where it is reviewable.
//
// The prefixes come from archivePrefix and the base from baseSegments -- the
// same two helpers the constructors and Classify call -- so a pattern cannot be
// registered at a path that Classify would decline, or that the constructors
// would never advertise. That is the whole reason this does not assemble the
// front itself: the front asymmetry (Req 4.7, 4.8) and the "date/"
// disambiguation segment (Req 6.7) are each decided in one place.
//
// Both slash forms are returned for every pattern except the category wildcard,
// because chi matches the two forms as distinct routes: registering only the
// canonical one would have chi 404 the other inside the router, before a handler
// could 301 it. The set therefore does not depend on TrailingSlash at all --
// which form is canonical is the handler's decision, made from the path
// constructors.
//
// The category route is a single wildcard rooted at its base. A nested category
// path has a segment count no chi pattern can express (Req 9.5), so the handler
// derives the segments from r.URL.Path the way web.resolveSingle already does,
// and a wildcard's remainder spans both slash forms on its own.
//
// A Flat structure still yields the category, tag and author patterns -- it
// serves /category/{slug} today -- but no date patterns, mirroring DatePath's
// and Classify's own gate: WordPress with plain permalinks registers no rewrite
// rules and its date archives are ?m=2024, so claiming /2024 here would take a
// path that today resolves a post slugged "2024" (Req 6.8, 9.8).
func (s Structure) ArchivePatterns() []string {
	category := s.archivePrefix(archiveCategory)
	category = append(category, baseSegments(s.CategoryBase)...)

	tag := s.archivePrefix(archiveTag)
	tag = append(tag, baseSegments(s.TagBase)...)

	author := s.archivePrefix(archiveAuthor)
	author = append(author, AuthorBase)

	pats := []string{joinPattern(category, "*")}
	pats = append(pats, bothSlashForms(joinPattern(tag, "{slug}"))...)
	pats = append(pats, bothSlashForms(joinPattern(author, "{nicename}"))...)

	if !s.servesDateArchives() {
		return pats
	}

	// The date component names are token.paramName()'s, not literals, so a
	// pattern and the Ref that Match reads out of it name the same parameters.
	date := s.archivePrefix(archiveDate)
	year, month, day := paramPattern(tokYear), paramPattern(tokMonth), paramPattern(tokDay)
	for _, tail := range [][]string{
		{year},
		{year, month},
		{year, month, day},
	} {
		pats = append(pats, bothSlashForms(joinPattern(date, tail...))...)
	}
	return pats
}

// joinPattern builds a rooted chi pattern from a segment prefix and the tail
// segments that follow it. It is archivePath's pattern-side twin, minus the
// trailing-slash rule, and it copies rather than appends to prefix so one
// prefix can serve several patterns.
func joinPattern(prefix []string, tail ...string) string {
	var b strings.Builder
	for _, seg := range prefix {
		b.WriteByte('/')
		b.WriteString(seg)
	}
	for _, seg := range tail {
		b.WriteByte('/')
		b.WriteString(seg)
	}
	return b.String()
}

// bothSlashForms returns a pattern and its other slash form, in the same order
// ChiPatterns uses: no trailing slash first.
func bothSlashForms(pattern string) []string {
	return []string{pattern, pattern + "/"}
}

// paramPattern renders a token as its chi parameter placeholder.
func paramPattern(t token) string { return "{" + t.paramName() + "}" }

// baseSegments splits a resolved archive base into its segments. Bases are
// normalized by resolveBase, so a plain Split is enough and a multi-segment base
// such as "topics/news" is carried consistently wherever a base is consumed.
func baseSegments(base string) []string {
	if base == "" {
		return nil
	}
	return strings.Split(base, "/")
}

// ParamsFromPath splits a path into the chi-style parameter map this structure
// would produce, without needing a router. It is used by the canonical
// fixed-point test, and by callers that need to evaluate a path outside a chi
// route context. It returns false when the segment count does not match.
func (s Structure) ParamsFromPath(path string) (map[string]string, bool) {
	if s.Flat || len(s.segments) == 0 {
		return nil, false
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != len(s.segments) {
		return nil, false
	}
	out := make(map[string]string, len(parts))
	for i, seg := range s.segments {
		if seg.tok == tokLiteral {
			if parts[i] != seg.literal {
				return nil, false
			}
			continue
		}
		out[seg.tok.paramName()] = parts[i]
	}
	return out, true
}

// SupportedTokens lists the tokens this package understands, for diagnostics
// such as the startup warning and migrate -check's report.
func SupportedTokens() []string {
	return []string{"%postname%", "%post_id%", "%year%", "%monthnum%", "%day%"}
}

// fixedWidthInt parses s as a non-negative integer of exactly width digits,
// rejecting anything shorter, longer, or non-numeric. WordPress zero-pads
// month and day, so "7" is not a valid %monthnum% and must not resolve.
func fixedWidthInt(s string, width int) (int, bool) {
	if len(s) != width {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

// resolveBase normalizes an archive base option, resolves it to the value that
// will be served, and reports whether the option was *provided*.
//
// The emptiness test is shared between the two answers on purpose (Req 4.13),
// and it is applied to the *normalized* value: with two separate tests a base of
// " " or "/" would both fall back to the default and count as provided, dropping
// the front for a base that was effectively unset -- the one combination neither
// behavior intends.
func resolveBase(v, fallback string) (base string, provided bool) {
	normalized := normalizeBase(v)
	if normalized == "" {
		return fallback, false
	}
	return normalized, true
}

// normalizeBase trims surrounding whitespace and slashes and collapses repeated
// internal slashes, so a stored base is reduced to bare segments joined by single
// slashes (Req 4.11). It returns "" for a value that carries no segment at all.
//
// This is load-bearing rather than cosmetic, because the base is the seat of chi
// pattern registration, Classify's segment comparison, archive path construction
// and the REST "link" field. "/topics" is a realistic stored value, not a
// hypothetical: WordPress's options-permalink.php prefixes the submitted base
// with "/" before update_option, so get_option( 'category_base' ) plausibly
// returns "/topics" on any site whose base was set through the admin UI --
// unnormalized it would emit //topics/* patterns and a first-segment comparison
// that never matches.
//
// A base that normalizes to more than one segment ("topics/news") is kept and
// used consistently everywhere a base is consumed; it is reported separately as a
// diagnostic rather than rejected here (Req 4.12).
func normalizeBase(v string) string {
	parts := strings.Split(strings.TrimSpace(v), "/")
	segs := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		segs = append(segs, part)
	}
	return strings.Join(segs, "/")
}
