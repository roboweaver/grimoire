package sanitize

import (
	"errors"
	stdhtml "html"
	"strings"
	"testing"
)

// These tests pin the post_title fixed-point path (Requirement 3; properties P5,
// P9; Finding 1). They are written before the production titleText exists in its
// converging form: title.go currently carries a single-pass stub that does one
// strict strip and never returns ErrTitleNotConverging, and the sentinel error
// does not exist yet. This file is therefore expected to FAIL to compile (missing
// ErrTitleNotConverging) and, once that lands, to fail at runtime against the
// stub until task 2.6 replaces title.go with the three-step fixed-point loop
// (p.strict.Sanitize -> html.UnescapeString -> deleteAngles, bounded at
// titleMaxPasses = 16, failing closed with ErrTitleNotConverging). That is the
// intended TDD red.
//
// The method under test is p.titleText (unexported, same package). Each case is
// also exercised through SanitizeAt(PostTitle, TierA/TierB, in) to pin that both
// below-tier-C paths route the title through titleText and surface its error
// (design "post_title is plain text": the title is governed by WordPress's
// comment list, so tier A and tier B must produce the same plain-text result and
// the same convergence failure).

// TestTitleTextWorkedExamples pins the three worked examples from design.md's
// post_title table (Req 3.1, 3.2, 3.3). Each is also run through SanitizeAt at
// both tier A and tier B, which must agree with titleText since a title ignores
// the post/comment tier distinction.
func TestTitleTextWorkedExamples(t *testing.T) {
	p := New()

	cases := []struct {
		name string
		in   string
		want string
	}{
		// <em>Hello</em> -> Hello: markup removed, text preserved (Req 3.2).
		{"inline markup stripped to text", `<em>Hello</em>`, `Hello`},
		// 5 &amp; 6 -> 5 & 6: the entity decodes to a bare ampersand; no &lt;,
		// no entity survives in the stored value (Req 3.3). html/template
		// re-escapes it at the emission site, so the reader still sees "5 & 6".
		{"single entity decoded to raw text", `5 &amp; 6`, `5 & 6`},
		// &amp;lt;b&amp;gt; -> "" (empty): the nested-encoding class. Pass 1
		// decodes to &lt;b&gt;, pass 2 strips the resulting <b> and deletes the
		// angle brackets, leaving nothing (Req 3.3, 3.4; the fixed-point loop).
		{"nested encoding fully unwrapped to empty", `&amp;lt;b&amp;gt;`, ``},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := p.titleText(c.in)
			if err != nil {
				t.Fatalf("titleText(%q) error = %v, want nil", c.in, err)
			}
			if got != c.want {
				t.Errorf("titleText(%q) = %q, want %q", c.in, got, c.want)
			}

			// Both below-tier-C paths must route through titleText and agree.
			for _, tr := range []Tier{TierA, TierB} {
				out, err := p.SanitizeAt(PostTitle, tr, c.in)
				if err != nil {
					t.Fatalf("SanitizeAt(PostTitle, %v, %q) error = %v, want nil", tr, c.in, err)
				}
				if out != c.want {
					t.Errorf("SanitizeAt(PostTitle, %v, %q) = %q, want %q", tr, c.in, out, c.want)
				}
			}
		})
	}
}

// TestTitleTextCarriesNoDelimitersOrEntities pins property P5 for the worked
// examples and a few additional shapes: the stored title must contain no '<', no
// '>' and no HTML entity sequence (Req 3.3). "No entity sequence" is checked the
// way the design specifies it -- the output is a fixed point of UnescapeString,
// which is stronger and simpler than pattern-matching entity syntax: if any
// decodable entity remained, unescaping would change the string.
func TestTitleTextCarriesNoDelimitersOrEntities(t *testing.T) {
	p := New()

	inputs := []string{
		`<em>Hello</em>`,
		`5 &amp; 6`,
		`&amp;lt;b&amp;gt;`,
		`<b>bold</b> &amp; <i>ital</i>`,
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		`a < b > c & d`,
		`&amp;amp;amp;lt;`,
	}

	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			out, err := p.titleText(in)
			if err != nil {
				t.Fatalf("titleText(%q) error = %v, want nil", in, err)
			}
			if strings.ContainsRune(out, '<') {
				t.Errorf("titleText(%q) = %q, contains '<' (Req 3.3 forbids markup delimiters)", in, out)
			}
			if strings.ContainsRune(out, '>') {
				t.Errorf("titleText(%q) = %q, contains '>' (Req 3.3 forbids markup delimiters)", in, out)
			}
			if un := stdhtml.UnescapeString(out); un != out {
				t.Errorf("titleText(%q) = %q, not a fixed point of UnescapeString (-> %q); an HTML entity survived (Req 3.3)", in, out, un)
			}
		})
	}
}

// TestTitleTextFailsClosedWhenNotConverging pins Req 3.8 / Finding 1: a crafted
// input that would require more decode layers than titleMaxPasses (= 16) must
// return ErrTitleNotConverging and NO value (fail closed, Req 1.8) rather than a
// partially-decoded title.
//
// The input is many nested encodings of a single '<': "&" + "amp;"*n + "lt;".
// Each pass peels exactly one "amp;" layer and strictly changes the string, so a
// string with well over 16 layers cannot reach a fixed point within the bound.
// n = 20 is comfortably past the 16-pass limit.
func TestTitleTextFailsClosedWhenNotConverging(t *testing.T) {
	p := New()

	in := "&" + strings.Repeat("amp;", 20) + "lt;"

	got, err := p.titleText(in)
	if !errors.Is(err, ErrTitleNotConverging) {
		t.Fatalf("titleText(nonconverging) error = %v, want ErrTitleNotConverging", err)
	}
	if got != "" {
		t.Errorf("titleText(nonconverging) = %q, want \"\" (fail closed: no value on error)", got)
	}

	// The failure must propagate through SanitizeAt at both below-tier-C tiers:
	// a non-converging title aborts the write at either tier, with no value.
	for _, tr := range []Tier{TierA, TierB} {
		out, err := p.SanitizeAt(PostTitle, tr, in)
		if !errors.Is(err, ErrTitleNotConverging) {
			t.Errorf("SanitizeAt(PostTitle, %v, nonconverging) error = %v, want ErrTitleNotConverging", tr, err)
		}
		if out != "" {
			t.Errorf("SanitizeAt(PostTitle, %v, nonconverging) = %q, want \"\" (fail closed)", tr, out)
		}
	}
}
