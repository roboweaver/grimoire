package sanitize

import (
	"github.com/microcosm-cc/bluemonday"
)

// Policy holds the compiled tier tables. It is built once by New and never
// mutated afterwards, so Sanitize only reads it and is safe for concurrent use
// by request goroutines (Req 1.9). bluemonday's own guidance -- policy
// creation/editing is not goroutine-safe, but using a built policy is -- splits
// cleanly along New / SanitizeAt: New is the only mutator.
//
// tierA and tierB are built from NewPolicy() over the declarative tables in
// tables.go; neither UGCPolicy nor StrictPolicy is used as a tier, because
// neither matches WordPress's lists and using one would make Req 2.11's
// fixed-point claim unprovable (design "UGCPolicy and StrictPolicy are not
// usable as tiers"). strict is StrictPolicy() for the post_title path (design
// "post_title is plain text").
type Policy struct {
	tierA  *bluemonday.Policy
	tierB  *bluemonday.Policy
	strict *bluemonday.Policy // titles; see title.go
}

// New compiles the tier tables. It allocates and mutates the three bluemonday
// policies, so it must be called once at startup and the result shared; it is
// not safe to call concurrently with itself (it is a constructor, not a
// request-path function).
func New() *Policy {
	return &Policy{
		tierA:  buildTier(tierAElements, false),
		tierB:  buildTier(tierBElements, true),
		strict: bluemonday.StrictPolicy(),
	}
}

// buildTier compiles one tier from its element table. The global flag selects
// tier B's extra permissions: the 19-attribute global tail, data-* attributes,
// the style property allow-list, and HTML comments (Gutenberg block delimiters).
// Tier A passes global=false and therefore has no global attribute set and
// leaves comments stripped (design "Tier A", "Tier B", D14).
func buildTier(elements map[string][]string, global bool) *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// D8 defect fix: bluemonday drops an allow-listed element that ends with
	// zero surviving attributes unless it is in its setOfElementsAllowedWithoutAttrs,
	// and nineteen elements this policy allows are not in that set. Calling
	// AllowNoAttrs().OnElements for EVERY element in the table -- unconditionally
	// -- makes a bare <a>text</a> survive rather than being silently deleted
	// (design "D8 is a defect to fix during construction").
	for el, attrs := range elements {
		p.AllowNoAttrs().OnElements(el)
		if len(attrs) > 0 {
			p.AllowAttrs(attrs...).OnElements(el)
		}
	}

	// URL handling (Req 2.9; design "URL schemes"). RequireParseableURLs(true)
	// is REQUIRED and not cosmetic: bluemonday gates its whole scheme check on
	// it, so omitting it would allow-list schemes and never check one, silently
	// passing javascript:/data: URLs. AllowRelativeURLs(true) matches kses, which
	// permits "/local/page". AllowURLSchemes installs the 22-scheme allow-list.
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes(allowedSchemes...)

	if global {
		// Tier B's "deliberately permissive" shared tail (Req 2.2): the 19 named
		// global attributes, data-* (policy-wide in bluemonday), and the style
		// property allow-list (Req 2.10). AllowComments keeps Gutenberg block
		// delimiters, which are HTML comments stripping would destroy (D14); it
		// is tier B only -- tier A leaves comments stripped.
		p.AllowAttrs(tierBGlobalAttrs...).Globally()
		p.AllowDataAttributes()
		p.AllowStyles(tierBStyleProps...).Globally()
		p.AllowComments()
	}

	return p
}

// Sanitize returns the value that may be persisted, at the tier TierFor selects
// for (k, w). It is the write-path entry point; it derives the tier and
// delegates to SanitizeAt so tier selection lives in exactly one place (TierFor)
// and the dispatch/fail-closed logic lives in exactly one place (SanitizeAt).
//
// It fails closed (Req 1.8): on any non-nil error the caller must abandon the
// write. The error is never "this content is unacceptable" -- the Policy reports
// what survived rather than rejecting content, and the calling path keeps owning
// required-field validation (Req 4.8). Errors propagate unchanged from
// SanitizeAt (an unrecognised FieldKind/Tier, or -- once task 2.6 lands -- a
// title that does not converge).
func (p *Policy) Sanitize(k FieldKind, w Writer, in string) (string, error) {
	return p.SanitizeAt(k, TierFor(k, w), in)
}

// SanitizeAt is Sanitize with the tier supplied rather than derived. It exists
// for the tier tables' own tests and the correctness properties, which quantify
// over tiers directly; no write path calls it.
//
// Three dispatch rules, in order (Req 1.2, 1.8, 2.3, 2.7; P4):
//
//   - Tier C is the unfiltered bypass: it returns (in, nil) byte-for-byte for
//     every field kind, INCLUDING PostTitle, before touching any table. It does
//     not call bluemonday, does not tokenize, does not re-serialize and does not
//     validate UTF-8 (Req 2.3's byte-identity). Handling it first, outside the
//     kind switch, is what guarantees PostTitle at tier C is NOT routed through
//     the plain-text title path.
//   - Tiers A and B dispatch on the field kind. PostTitle below tier C is always
//     reduced to plain text via titleText regardless of whether the selected
//     tier is A or B (the title is governed by WordPress's comment list, not the
//     post list; design "post_title plain text"). Every other kind dispatches to
//     the tier's compiled bluemonday policy.
//   - An unrecognised FieldKind or Tier is a programming error and MUST fail
//     closed with a non-nil error rather than silently degrading to a permissive
//     default; a switch with a permissive `default` is the realistic break this
//     error return exists to catch (Req 1.8).
//
// No path returns a non-nil error together with a persistable value, and no path
// returns unsanitized bytes for tier A or B.
func (p *Policy) SanitizeAt(k FieldKind, t Tier, in string) (string, error) {
	// Reject an unrecognised FieldKind before anything else, at every tier
	// (including C), so a bad kind can never ride the tier-C bypass to a
	// persistable value.
	switch k {
	case PostContent, PostExcerpt, PostTitle, CommentContent:
		// recognised; fall through to tier dispatch
	default:
		return "", &unrecognisedError{kind: k, tier: t, what: "FieldKind"}
	}

	switch t {
	case TierC:
		// Unfiltered bypass: byte-identical, no tokenize, no title path.
		return in, nil
	case TierA:
		if k == PostTitle {
			return p.titleText(in)
		}
		return p.tierA.Sanitize(in), nil
	case TierB:
		if k == PostTitle {
			return p.titleText(in)
		}
		return p.tierB.Sanitize(in), nil
	default:
		return "", &unrecognisedError{kind: k, tier: t, what: "Tier"}
	}
}

// unrecognisedError is returned by SanitizeAt for an out-of-range FieldKind or
// Tier. It carries both values so a reviewer reading a log sees which axis broke;
// its only required property for task 2.4 is that it is a non-nil error paired
// with no persistable value (fail closed, Req 1.8).
type unrecognisedError struct {
	kind FieldKind
	tier Tier
	what string
}

func (e *unrecognisedError) Error() string {
	return "sanitize: unrecognised " + e.what + ": fail closed (no value persisted)"
}
