package sanitize

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
)

// These tests pin the dispatch surface of the Policy: the Sanitize/SanitizeAt
// pair, the tier-C bypass's byte-identity, and the fail-closed error returns for
// unrecognised FieldKind/Tier values (Req 1.8, 2.3; property P4). They are
// written before the production code implements them: Sanitize does not yet
// exist, and SanitizeAt's current minimal form returns "" for TierC rather than
// the byte-identical input and never returns an error, so this file is expected
// to fail to compile (missing Sanitize) and, once that compiles, to fail at
// runtime until SanitizeAt gains the tier-C bypass and the error paths.

// allFieldKinds is every real FieldKind, used wherever a property must hold for
// each kind including PostTitle (whose tier-C bypass must NOT route through the
// plain-text title path).
var allFieldKinds = []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent}

// TestSanitizeAtTierCByteIdentical pins Req 2.3 / P4: SanitizeAt(k, TierC, in)
// returns in byte-for-byte for every field kind, with no error. Tier C is the
// unfiltered bypass; it must not tokenize, re-serialize, validate UTF-8 or route
// a title through the plain-text path. The malformed and non-UTF-8 inputs are the
// cases that distinguish a true bypass from a parse-and-print round trip that
// happens to be identity on well-formed fixtures.
func TestSanitizeAtTierCByteIdentical(t *testing.T) {
	p := New()
	inputs := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"plain text", "hello world"},
		{"well-formed markup", `<em>hi</em>`},
		{"script element", `<script>alert(1)</script>`},
		{"unclosed tag", `<a href="http://x/"`},
		{"stray gt lt", `5 < 6 & 7 > 2`},
		{"entity text", `&amp;lt;b&amp;gt;`},
		{"malformed nesting", `<b><i>x</b></i>`},
		{"bare ampersand", `a & b & c`},
		{"non-utf8 bytes", "ab\xff\xfecd"},
		{"null byte", "a\x00b"},
		{"gutenberg comment", `<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->`},
	}
	for _, k := range allFieldKinds {
		for _, in := range inputs {
			t.Run(in.name, func(t *testing.T) {
				out, err := p.SanitizeAt(k, TierC, in.in)
				if err != nil {
					t.Fatalf("SanitizeAt(%v, TierC, %q) error = %v, want nil", k, in.in, err)
				}
				if out != in.in {
					t.Errorf("SanitizeAt(%v, TierC, %q) = %q, want byte-identical input", k, in.in, out)
				}
			})
		}
	}
}

// TestSanitizeAtTierCPostTitleNotPlainText is a focused restatement of the above
// for the one kind most likely to be special-cased wrong: PostTitle. Tier C must
// return the raw markup unchanged, NOT the plain-text title transform (which
// would strip the <em> and delete the angle brackets). This is the regression a
// future "titles are always plain text" shortcut would introduce.
func TestSanitizeAtTierCPostTitleNotPlainText(t *testing.T) {
	p := New()
	const in = `<em>Hello</em> & <b>World</b>`
	out, err := p.SanitizeAt(PostTitle, TierC, in)
	if err != nil {
		t.Fatalf("SanitizeAt(PostTitle, TierC, %q) error = %v, want nil", in, err)
	}
	if out != in {
		t.Errorf("SanitizeAt(PostTitle, TierC, %q) = %q; tier C must bypass the plain-text title path and return input byte-identically", in, out)
	}
}

// TestSanitizeAtUnrecognisedTierFailsClosed pins Req 1.8: an out-of-range Tier is
// a programming error that must return a non-nil error, never a permissive
// passthrough and never a silent degrade to TierC. Tier(99) is constructed by an
// out-of-range int conversion.
func TestSanitizeAtUnrecognisedTierFailsClosed(t *testing.T) {
	p := New()
	const bad = Tier(99)
	for _, k := range allFieldKinds {
		out, err := p.SanitizeAt(k, bad, `<script>alert(1)</script>`)
		if err == nil {
			t.Errorf("SanitizeAt(%v, Tier(99), ...) err = nil, want non-nil (fail closed); got out = %q", k, out)
		}
	}
}

// TestSanitizeAtUnrecognisedFieldKindFailsClosed pins Req 1.8 for the other axis:
// an out-of-range FieldKind must fail closed with a non-nil error rather than
// defaulting to any tier's behavior. Exercised at every real tier so a kind check
// cannot be skipped on a particular tier. FieldKind(99) is an out-of-range int
// conversion.
func TestSanitizeAtUnrecognisedFieldKindFailsClosed(t *testing.T) {
	p := New()
	const bad = FieldKind(99)
	for _, tr := range []Tier{TierA, TierB, TierC} {
		out, err := p.SanitizeAt(bad, tr, `<script>alert(1)</script>`)
		if err == nil {
			t.Errorf("SanitizeAt(FieldKind(99), %v, ...) err = nil, want non-nil (fail closed); got out = %q", tr, out)
		}
	}
}

// TestSanitizeRoutesThroughTierFor pins that Sanitize(k, w, in) selects the tier
// TierFor would select for (k, w) and produces the same result as SanitizeAt at
// that tier. The oracle is SanitizeAt itself: for each (kind, writer) the two must
// agree, which is what "Sanitize routes through TierFor" means operationally.
//
// Sanitize does not yet exist, so this test is what forces the file to fail to
// compile until the method is added.
func TestSanitizeRoutesThroughTierFor(t *testing.T) {
	p := New()

	// A writer lacking unfiltered_html (anonymous): kinds route to tier A/B.
	// A writer holding it: every kind routes to tier C.
	writers := []struct {
		name string
		w    Writer
	}{
		{"anonymous", Anonymous()},
		{"unfiltered", For(auth.NewPrincipal(1, "u", []string{CapUnfilteredHTML}))},
	}

	// Inputs chosen so the three tiers produce visibly different results: markup
	// a comment list strips, a post list keeps, and tier C passes verbatim.
	inputs := []string{
		`<em>hi</em>`,
		`<p class="lead">para</p>`,
		`<script>alert(1)</script>`,
		`plain text`,
	}

	for _, wc := range writers {
		for _, k := range allFieldKinds {
			wantTier := TierFor(k, wc.w)
			for _, in := range inputs {
				want, wantErr := p.SanitizeAt(k, wantTier, in)
				got, gotErr := p.Sanitize(k, wc.w, in)
				if (gotErr == nil) != (wantErr == nil) {
					t.Errorf("Sanitize(%v, %s, %q) err = %v; SanitizeAt at TierFor-selected tier %v err = %v",
						k, wc.name, in, gotErr, wantTier, wantErr)
					continue
				}
				if gotErr == nil && got != want {
					t.Errorf("Sanitize(%v, %s, %q) = %q; SanitizeAt(%v, %v, %q) = %q; Sanitize must route through TierFor",
						k, wc.name, in, got, k, wantTier, in, want)
				}
			}
		}
	}
}
