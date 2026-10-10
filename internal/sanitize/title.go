package sanitize

import (
	"errors"
	stdhtml "html"
	"strings"
)

// ErrTitleNotConverging is returned by titleText when a title still changes after
// titleMaxPasses decode passes. A title needing more than titleMaxPasses layers of
// nested encoding is an attack rather than content, so the Policy fails closed
// (Req 1.8, 3.8; Finding 1): the caller receives no value and must abandon the
// write rather than persist a partially-decoded title.
var ErrTitleNotConverging = errors.New("sanitize: title did not converge; fail closed (no value persisted)")

// titleMaxPasses bounds both the entity-decode fixed point and the outer
// decode-then-parse loop in titleText. Each outer pass peels at most one layer of
// nested encoding; a legitimate title converges in one or two passes, so 16 is far
// above any real content yet finite enough to reject an attacker's deeply nested
// encoding (Req 3.8; Finding 1).
const titleMaxPasses = 16

// unescapeToFixedPoint fully decodes HTML entities in s, applying
// stdhtml.UnescapeString repeatedly until the string stops changing. It returns
// (decoded, true) on reaching a fixed point within titleMaxPasses iterations, or
// ("", false) if decoding still changes the string after the bound -- a signal to
// the caller to fail closed. Decoding to a true fixed point (rather than a single
// pass) is what reveals markup that was hidden behind nested entity encoding, so
// the parse step that follows can strip it as markup.
func unescapeToFixedPoint(s string) (string, bool) {
	for i := 0; i < titleMaxPasses; i++ {
		n := stdhtml.UnescapeString(s)
		if n == s {
			return s, true
		}
		s = n
	}
	return "", false
}

// titleText strips all markup from a title, preserving its text (Req 3.2) and
// emitting neither markup delimiters nor HTML entities (Req 3.3).
//
// Each pass runs four steps, in this exact order:
//
//  1. Fully decode HTML entities to a fixed point (unescapeToFixedPoint). This
//     peels every layer of nested entity encoding before any parsing happens, so
//     that markup hidden behind encoding (e.g. "&amp;lt;b&amp;gt;" -> "<b>") is
//     laid bare as real markup. If entity-decoding itself cannot reach a fixed
//     point within titleMaxPasses, the title fails closed with
//     ErrTitleNotConverging (Req 1.8, 3.8; Finding 1).
//  2. Parse the fully-decoded string with p.strict.Sanitize -- StrictPolicy, a
//     real parser, never a regex (Req 8.7). Because step 1 already revealed the
//     markup, the parser strips those now-visible tags and their content, leaving
//     HTML-escaped text behind.
//  3. stdhtml.UnescapeString once, to undo the escaping the StrictPolicy applied
//     to the surviving text in step 2 (so "5 &lt; 6" from the parser becomes
//     "5 < 6"); the next step removes any residual delimiters.
//  4. deleteAngles -- remove every '<' and '>' rune. This is Req 3.4's stated
//     loss and is what makes the result safe for an auto-escaped string field
//     with no entity in sight.
//
// The outer loop repeats steps 1-4 until a pass is a no-op (fixed point) or
// titleMaxPasses is exceeded; exceeding the bound fails closed.
//
// WHY decode-then-parse (not parse-then-decode): this ordering resolves an
// internal inconsistency in design.md, per the M10a owner's decision. The design's
// canonical code block decoded once per pass AFTER parsing, which left nested
// encodings like "&amp;lt;b&amp;gt;" converging to the text "b" -- a pre-encoding
// "walk past the sanitizer" that re-encoded content can smuggle through. Decoding
// to a fixed point FIRST means markup revealed by decoding is stripped AS markup,
// so that example converges to empty. That is the intent of property P1 / Req 3.3:
// the stored title carries no markup delimiters and no live entities, and the
// nested-encoding class is killed rather than merely peeled down to its tag name.
func (p *Policy) titleText(in string) (string, error) {
	cur := in
	for i := 0; i < titleMaxPasses; i++ {
		dec, ok := unescapeToFixedPoint(cur)
		if !ok {
			return "", ErrTitleNotConverging
		}
		next := deleteAngles(stdhtml.UnescapeString(p.strict.Sanitize(dec)))
		if next == cur {
			return next, nil
		}
		cur = next
	}
	return "", ErrTitleNotConverging
}

// deleteAngles removes every '<' and '>' rune from s, leaving all other bytes
// untouched. It is the markup-delimiter strip of the title path's fourth step
// (Req 3.4). It deliberately does NOT use regexp: Req 8.7 forbids a regex on the
// tag-splitting path, so this walks the string with strings.Builder and copies
// through everything that is not an angle bracket.
func deleteAngles(s string) string {
	// Fast path: nothing to strip, so return the input without allocating.
	if !strings.ContainsAny(s, "<>") {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '<' || r == '>' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
