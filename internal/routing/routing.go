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
	"strconv"
	"strings"

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
	// CategoryBase and TagBase are the resolved archive base segments.
	CategoryBase string
	TagBase      string

	segments []segment
}

// Parse parses a permalink_structure together with the category_base and
// tag_base options. Empty base values fall back to WordPress's defaults.
//
// An empty structure yields a flat Structure and a nil error. A structure that
// contains an unrecognised token, or that carries no token capable of
// identifying a single post, yields a flat Structure and an error wrapping
// ErrUnsupported whose message names the specific problem -- the caller is
// expected to log it and continue with the returned flat Structure.
func Parse(structure, categoryBase, tagBase string) (Structure, error) {
	s := Structure{
		Raw:          structure,
		CategoryBase: firstNonEmpty(categoryBase, DefaultCategoryBase),
		TagBase:      firstNonEmpty(tagBase, DefaultTagBase),
	}
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
	return s, nil
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

// Match extracts a Ref from chi's parsed URL parameters. It returns false when
// any component fails its shape rule: dates must be zero-padded to WordPress's
// widths and be calendar-plausible, an id must be a positive integer, and a
// slug must be non-empty.
//
// Shape is enforced here rather than in the route pattern because chi patterns
// have no width or range syntax, and because a wrong-shaped path should 404
// rather than reach a database query.
func (s Structure) Match(params map[string]string) (Ref, bool) {
	if s.Flat {
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

func firstNonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
