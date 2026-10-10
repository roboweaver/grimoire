// Package sanitize is grimoire's write-boundary content policy: the single place
// that decides what markup may be persisted. It is pure -- no database, no file,
// no network, no clock -- so every behavior here is assertable in a unit test
// with no environment gating (Req 1.2).
package sanitize

import "github.com/roboweaver/grimoire/internal/auth"

// FieldKind is which content field is being sanitized. It is always an explicit
// argument and is never inferred from the input's shape, its length or the
// calling transport (Req 1.7).
type FieldKind int

const (
	PostContent    FieldKind = iota // post_content
	PostExcerpt                     // post_excerpt
	PostTitle                       // post_title
	CommentContent                  // comment_content
)

// Tier is one of the three filtering tiers (Req 2). Tier selection is a pure
// function of (FieldKind, whether the writer holds unfiltered_html); nothing
// else -- notably not authentication status -- is an input (Req 2.5).
type Tier int

const (
	TierA Tier = iota // WordPress $allowedtags: the tight comment list
	TierB             // WordPress $allowedposttags: the broad post list
	TierC             // no filtering at all; output is byte-identical to input
)

// CapUnfilteredHTML is the capability name, declared here so the tier-selection
// test and the roles table are comparing one string rather than two literals.
const CapUnfilteredHTML = "unfiltered_html"

// Writer is the Policy's capability input. It carries two bits, and carrying two
// rather than one is Requirement 1.6: a caller with no Principal at all (public
// comment submission, where internal/web/comments.go reads PrincipalFrom and
// proceeds when it is absent) must be distinguishable at the call site from a
// logged-in user holding no capabilities, even though both land in the same tier.
// A zero-value Writer is the anonymous caller, which is the fail-closed default:
// a struct nobody filled in holds no capabilities and therefore never reaches
// TierC.
type Writer struct {
	authenticated bool
	unfiltered    bool
}

// Anonymous returns the Writer for a caller with no Principal.
func Anonymous() Writer { return Writer{} }

// For returns the Writer for a resolved Principal, reading unfiltered_html
// through Principal.Can -- the existing capability mechanism, with no second
// role table and no per-endpoint capability string (Req 1.5). Explicit grants
// resolved by auth.NewPrincipal select TierC exactly as the editor and
// administrator roles do, so a custom role carrying unfiltered_html works with
// no additional resolution step and a role rename changes nothing (Req 2.4).
func For(p auth.Principal) Writer {
	return Writer{authenticated: true, unfiltered: p.Can(CapUnfilteredHTML)}
}

// Authenticated reports whether a Principal was present. It is NOT an input to
// tier selection (Req 2.5); it exists so a caller and a reviewer can tell the
// two capability-less cases apart, and so a future change that starts
// distinguishing them has to say so out loud.
func (w Writer) Authenticated() bool { return w.authenticated }

// TierFor is tier selection, standing alone so the matrix test of Req 9.3 can
// assert it without sanitizing anything.
func TierFor(k FieldKind, w Writer) Tier {
	if w.unfiltered {
		return TierC
	}
	if k == CommentContent {
		return TierA
	}
	return TierB
}
