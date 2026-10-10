package sanitize

import (
	stdhtml "html"
	"strings"
	"testing"
)

// This file holds the byte-oriented correctness properties of the pure package:
// the Go native fuzz targets named in design.md's "Mechanism per property" table.
// They are the repository's first fuzz targets (design: "The repository has no
// Fuzz target today; these are its first four"), and they exist because the
// structured testing/quick generator in properties_test.go cannot reach the
// inputs these guard: testing/quick's string generator produces only valid
// UTF-8 runes, so it cannot express the malformed and non-UTF-8 bytes P4 is
// stated over, and a []byte-driven re-tokenization is the honest way to fuzz
// P2 closure and P5/P9 title convergence over arbitrary input (Req 9.9).
//
// Each target ships a committed seed corpus two ways: f.Add seeds in the target
// itself (always run) and files under testdata/fuzz/FuzzXxx/ (also always run).
// `go test ./...` runs the seed corpus only -- `-fuzz` is not passed -- so CI
// stays deterministic and fast (Req 9.13), while
// `go test -fuzz=FuzzTierCIdentity ./internal/sanitize` remains available for a
// deliberate campaign; any crasher a campaign finds is committed to the corpus
// as its own regression (design "Fuzz corpus location").
//
// allKinds is every FieldKind, including PostTitle: tier C's byte-identity
// bypass (P4) must hold for the title kind too, and that it does is the whole
// point of handling tier C before the kind switch in SanitizeAt.
var allKinds = []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent}

// -----------------------------------------------------------------------------
// P4 -- Tier-C bypass is byte-identity (Req 9.9; design P4)
//
// For all byte strings b and every field kind k:
//   SanitizeAt(k, TierC, string(b)) == string(b)
//
// Native fuzzing rather than testing/quick, because Req 2.3's byte-identity is
// stated "including for malformed and non-UTF-8 input" and testing/quick's rune
// generator only produces valid code points. The seed corpus deliberately
// includes invalid UTF-8 (\xff\xfe, a lone \xc3, a truncated surrogate), markup
// that never parses (<b with no >, <<<), a pathologically nested chain, and an
// embedded NUL -- exactly the inputs a parse-and-print refactor of tier C would
// corrupt. The error arm must never fire for a recognised kind at tier C.
// -----------------------------------------------------------------------------

func FuzzTierCIdentity(f *testing.F) {
	// Committed seeds (design P4): invalid and truncated UTF-8, unparseable
	// markup, a deeply nested chain, and a NUL byte.
	f.Add([]byte("\xff\xfe"))                              // invalid UTF-8 BOM-ish bytes
	f.Add([]byte("\xc3"))                                  // lone continuation-expecting lead byte
	f.Add([]byte("\xed\xa0\x80"))                          // truncated/encoded surrogate (invalid UTF-8)
	f.Add([]byte("<b"))                                    // start tag with no '>'
	f.Add([]byte("<<<"))                                   // stray delimiters, never a tag
	f.Add([]byte(strings.Repeat("<div>", 64)))             // 64-level nested <div> chain
	f.Add([]byte("a\x00b"))                                // embedded NUL byte
	f.Add([]byte(""))                                      // empty input
	f.Add([]byte("<script>alert(1)</script>"))             // ordinary hostile markup, still identity at C
	f.Add([]byte("plain text & entities &amp; &lt;b&gt;")) // text and entities, untouched at C

	p := New()
	f.Fuzz(func(t *testing.T, b []byte) {
		in := string(b)
		for _, k := range allKinds {
			out, err := p.SanitizeAt(k, TierC, in)
			if err != nil {
				t.Fatalf("tier C returned an error for kind=%s: %v (input %q)",
					fieldKindName(k), err, in)
			}
			if out != in {
				t.Fatalf("tier C is not byte-identity for kind=%s:\n in  = %q\n out = %q",
					fieldKindName(k), in, out)
			}
		}
	})
}

// -----------------------------------------------------------------------------
// P2 (fuzz arm) -- Allow-list closure over arbitrary bytes (Req 9.9; design P2)
//
// For all byte strings b, parsing the tier-A and tier-B output of every
// non-title kind yields no element, attribute or URL scheme outside that tier's
// own table. This is the fuzz companion to TestP2Closure: the quick arm drives
// markup-dense structured input, this arm drives arbitrary bytes, and both
// assert closure on the RE-TOKENIZED tree (outputIsClosed) rather than by
// substring -- the check that would catch an implementation emitting
// "<scr<script>ipt>". Tier C is excluded (it is identity, covered by P4) and
// PostTitle is excluded (it is the convergence path, covered by FuzzTitleText).
// -----------------------------------------------------------------------------

func FuzzTierClosure(f *testing.F) {
	// Committed seeds (design P2): hostile markup, nested encodings, bad schemes,
	// malformed tokens and the "<scr<script>ipt>" splice closure must survive.
	f.Add([]byte("<script>alert(1)</script>"))
	f.Add([]byte("<scr<script>ipt>alert(1)</script>"))
	f.Add([]byte("<a href=\"javascript:alert(1)\">x</a>"))
	f.Add([]byte("<a href=\"data:text/html,<b>x\">x</a>"))
	f.Add([]byte("<img src=\"http://example.com/a.png\" onerror=\"steal()\">"))
	f.Add([]byte("<b><i>deep</b></i>"))
	f.Add([]byte("<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->"))
	f.Add([]byte("<p style=\"color:red;-moz-binding:url(http://evil/x.xml)\">x</p>"))
	f.Add([]byte("&amp;lt;b&amp;gt;"))
	f.Add([]byte("<<<"))
	f.Add([]byte(""))

	p := New()
	f.Fuzz(func(t *testing.T, b []byte) {
		in := string(b)
		for _, k := range nonTitleKinds {
			for _, tier := range filteringTiers {
				out, err := p.SanitizeAt(k, tier, in)
				if err != nil {
					t.Fatalf("unexpected error at kind=%s tier=%s: %v (input %q)",
						fieldKindName(k), tierName(tier), err, in)
				}
				allowed := allowedElementsFor(tier)
				if !outputIsClosed(out, tier, allowed) {
					t.Fatalf("P2 closure violated at kind=%s tier=%s:\n in  = %q\n out = %q",
						fieldKindName(k), tierName(tier), in, out)
				}
			}
		}
	})
}

// -----------------------------------------------------------------------------
// P5 / P9 (fuzz arm) -- Title convergence over arbitrary bytes (Req 9.9; design P5)
//
// For all byte strings b, the PostTitle path either:
//   (a) converges to a value containing no '<' and no '>', that is a fixed point
//       of UnescapeString (no entity sequence survives) and is itself a fixed
//       point of the title function -- titleText(titleText(x)) == titleText(x);
// or
//   (b) fails closed with exactly ErrTitleNotConverging and persists nothing.
// This is the fuzz companion to TestP5P9TitleConvergence: the quick arm drives
// structured markup, this arm drives arbitrary bytes including malformed UTF-8
// and nested encodings. Asserting the output is a fixed point of UnescapeString
// is the design's stronger, simpler stand-in for pattern-matching entity syntax.
// -----------------------------------------------------------------------------

func FuzzTitleText(f *testing.F) {
	// Committed seeds (design P5): the three worked examples, nested encodings,
	// stray delimiters, invalid UTF-8 and markup that must reduce to plain text.
	f.Add([]byte("<em>Hello</em>"))
	f.Add([]byte("5 &amp; 6"))
	f.Add([]byte("&amp;lt;b&amp;gt;"))
	f.Add([]byte("<script>alert(1)</script>"))
	f.Add([]byte("a < b > c"))
	f.Add([]byte("&lt;script&gt;alert(1)&lt;/script&gt;"))
	f.Add([]byte("\xff\xfe"))
	f.Add([]byte("<<<"))
	f.Add([]byte(""))

	p := New()
	f.Fuzz(func(t *testing.T, b []byte) {
		in := string(b)
		// Both tier A and tier B route PostTitle through titleText; assert over
		// both so a tier-dependent regression is caught.
		for _, tier := range filteringTiers {
			out, err := p.SanitizeAt(PostTitle, tier, in)
			if err != nil {
				// Fail-closed is legitimate: it must be exactly the convergence
				// sentinel and carry no value (Req 1.8, 3.8).
				if err != ErrTitleNotConverging {
					t.Fatalf("unexpected error at tier=%s: %v (input %q)", tierName(tier), err, in)
				}
				if out != "" {
					t.Fatalf("ErrTitleNotConverging returned a non-empty value %q at tier=%s (input %q)",
						out, tierName(tier), in)
				}
				continue
			}
			// (a) no markup delimiters (Req 3.3).
			if strings.ContainsAny(out, "<>") {
				t.Fatalf("title output carries a markup delimiter at tier=%s:\n in  = %q\n out = %q",
					tierName(tier), in, out)
			}
			// (b) no entity sequence: output is a fixed point of UnescapeString.
			if stdhtml.UnescapeString(out) != out {
				t.Fatalf("title output is not a fixed point of UnescapeString at tier=%s:\n in  = %q\n out = %q",
					tierName(tier), in, out)
			}
			// (c) the result is itself a fixed point of the title function.
			again, err := p.SanitizeAt(PostTitle, tier, out)
			if err != nil {
				t.Fatalf("re-sanitizing a converged title errored at tier=%s: %v (out %q)", tierName(tier), err, out)
			}
			if again != out {
				t.Fatalf("titleText is not a fixed point at tier=%s:\n out      = %q\n again    = %q",
					tierName(tier), out, again)
			}
		}
	})
}

// -----------------------------------------------------------------------------
// Companion table test (design P4): tier C does not route through bluemonday.
//
// Byte-identity is trivially satisfiable by the current implementation, so the
// property exists to guard a FUTURE refactor that routes tier C through "the
// laxest policy" or a parse-and-print round trip. This table pins inputs that
// ANY bluemonday policy -- even the most permissive -- would alter (drop a
// disallowed element, normalize an unclosed tag, re-encode a stray delimiter,
// corrupt invalid UTF-8), and asserts tier C returns them byte-for-byte. If a
// refactor sends tier C through a policy, these rows break even though the fuzz
// seed corpus alone might not surface a mutation on well-formed input.
// -----------------------------------------------------------------------------

func TestTierCDoesNotRouteThroughBluemonday(t *testing.T) {
	p := New()
	// Each input is something a bluemonday pass would demonstrably change:
	// script would be dropped, the unclosed/stray markup normalized, the invalid
	// UTF-8 replaced, the NUL stripped. Tier C must leave every byte intact.
	inputs := []string{
		"<script>alert(1)</script>",
		"<b><i>deep</b></i>",
		"<a href=\"javascript:alert(1)\">x</a>",
		"<<<",
		"<b",
		"a < b > c",
		"\xff\xfe",
		"\xc3",
		"a\x00b",
		strings.Repeat("<div>", 64),
	}
	for _, k := range allKinds {
		for _, in := range inputs {
			out, err := p.SanitizeAt(k, TierC, in)
			if err != nil {
				t.Fatalf("tier C errored for kind=%s input=%q: %v", fieldKindName(k), in, err)
			}
			if out != in {
				t.Fatalf("tier C altered input for kind=%s (looks like it routed through a policy):\n in  = %q\n out = %q",
					fieldKindName(k), in, out)
			}
		}
	}
}
