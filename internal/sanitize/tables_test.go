package sanitize

import (
	"strings"
	"testing"
)

// These tests pin the tier-A and tier-B element/attribute behavior of the
// compiled Policy (Req 2.1, 2.2, 2.9, 2.10; design "Tier A", "Tier B", "URL
// schemes", "Tier B style properties", divergences D8 and D14). They are
// table-driven and database-free (Req 9.2), and they are written before the
// Policy type and its SanitizeAt method exist: the file is expected to fail to
// compile until New() and (*Policy).SanitizeAt are defined.
//
// The assertions are intentionally structural rather than byte-exact. The
// construction tables, not the test, own the exact serialization bluemonday
// produces (attribute ordering, quote style, self-closing form), so a case
// asserts the markers that distinguish "element/attribute survived" from
// "element/attribute was stripped" -- a surviving tag name, a surviving
// attribute name, surviving text content -- using contains/not-contains. That
// keeps the table resilient to cosmetic serialization while still failing hard
// if an allow-listed element is dropped or a forbidden one is kept.

// containsTag reports whether out contains an opening tag for the named element,
// e.g. tag "em" matches "<em>" and "<em ...". It deliberately does not match a
// closing tag so that a case asserting "<script> is gone" is not fooled by a
// stray "</script>" in surrounding text.
func containsTag(out, tag string) bool {
	return strings.Contains(out, "<"+tag+">") || strings.Contains(out, "<"+tag+" ") || strings.Contains(out, "<"+tag+"\n")
}

// containsAttr reports whether out contains the named attribute as a token,
// e.g. attr "href" matches `href="..."`. It is a coarse check -- good enough to
// distinguish "attribute survived" from "attribute stripped", which is all the
// table needs.
func containsAttr(out, attr string) bool {
	return strings.Contains(out, " "+attr+"=") || strings.Contains(out, " "+attr+" ") ||
		strings.HasPrefix(strings.TrimSpace(out), attr+"=")
}

// --- Tier A: the 14 allow-listed elements (WordPress $allowedtags) ----------

func TestTierAElementsSurvive(t *testing.T) {
	p := New()
	// Every tier-A element, exercised with an attribute-less instance where the
	// element permits that and with its allow-listed attribute where it has one.
	// Attribute-less survival is itself the D8 fix (a bare allow-listed element
	// must not be dropped for ending with zero attributes).
	cases := []struct {
		name string
		in   string
		tag  string
		text string
	}{
		{"a", `<a>text</a>`, "a", "text"},
		{"abbr", `<abbr>WHO</abbr>`, "abbr", "WHO"},
		{"acronym", `<acronym>NASA</acronym>`, "acronym", "NASA"},
		{"b", `<b>bold</b>`, "b", "bold"},
		{"blockquote", `<blockquote>quoted</blockquote>`, "blockquote", "quoted"},
		{"cite", `<cite>citation</cite>`, "cite", "citation"},
		{"code", `<code>x := 1</code>`, "code", "x := 1"},
		{"del", `<del>removed</del>`, "del", "removed"},
		{"em", `<em>stressed</em>`, "em", "stressed"},
		{"i", `<i>italic</i>`, "i", "italic"},
		{"q", `<q>quoted</q>`, "q", "quoted"},
		{"s", `<s>struck</s>`, "s", "struck"},
		{"strike", `<strike>struck</strike>`, "strike", "struck"},
		{"strong", `<strong>important</strong>`, "strong", "important"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := p.SanitizeAt(CommentContent, TierA, c.in)
			if err != nil {
				t.Fatalf("SanitizeAt(CommentContent, TierA, %q) error = %v", c.in, err)
			}
			if !containsTag(out, c.tag) {
				t.Errorf("tier A dropped allow-listed <%s>: SanitizeAt(%q) = %q", c.tag, c.in, out)
			}
			if !strings.Contains(out, c.text) {
				t.Errorf("tier A lost text content %q: SanitizeAt(%q) = %q", c.text, c.in, out)
			}
		})
	}
}

func TestTierAAttributesSurvive(t *testing.T) {
	p := New()
	cases := []struct {
		name string
		in   string
		attr string
	}{
		{"a[href]", `<a href="http://example.com/">x</a>`, "href"},
		{"a[title]", `<a title="hint">x</a>`, "title"},
		{"abbr[title]", `<abbr title="World Health Org">WHO</abbr>`, "title"},
		{"acronym[title]", `<acronym title="National Aeronautics">NASA</acronym>`, "title"},
		{"blockquote[cite]", `<blockquote cite="http://example.com/">q</blockquote>`, "cite"},
		{"del[datetime]", `<del datetime="2024-01-01">gone</del>`, "datetime"},
		{"q[cite]", `<q cite="http://example.com/">q</q>`, "cite"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := p.SanitizeAt(CommentContent, TierA, c.in)
			if err != nil {
				t.Fatalf("SanitizeAt(CommentContent, TierA, %q) error = %v", c.in, err)
			}
			if !containsAttr(out, c.attr) {
				t.Errorf("tier A dropped allow-listed attribute %s: SanitizeAt(%q) = %q", c.attr, c.in, out)
			}
		})
	}
}

// --- Tier B: a representative set of allow-listed elements and attributes ---

func TestTierBElementsSurvive(t *testing.T) {
	p := New()
	// A representative set drawn from design.md's "Tier B" tables: all 14
	// tier-A elements must still survive (monotonicity, Req 2.6), plus elements
	// unique to the broad post list -- block, media, table, and list elements --
	// and img, which Req 2.2 names explicitly as a tier-B allowance.
	cases := []struct {
		name string
		in   string
		tag  string
	}{
		// Inherited from tier A.
		{"a", `<a>text</a>`, "a"},
		{"em", `<em>x</em>`, "em"},
		{"strong", `<strong>x</strong>`, "strong"},
		{"blockquote", `<blockquote>x</blockquote>`, "blockquote"},
		// Tier-B-only structure.
		{"p", `<p>para</p>`, "p"},
		{"div", `<div>block</div>`, "div"},
		{"span", `<span>inline</span>`, "span"},
		{"h1", `<h1>heading</h1>`, "h1"},
		{"h2", `<h2>heading</h2>`, "h2"},
		{"ul", `<ul><li>item</li></ul>`, "ul"},
		{"ol", `<ol><li>item</li></ol>`, "ol"},
		{"li", `<ul><li>item</li></ul>`, "li"},
		{"table", `<table><tr><td>cell</td></tr></table>`, "table"},
		{"tr", `<table><tr><td>cell</td></tr></table>`, "tr"},
		{"td", `<table><tr><td>cell</td></tr></table>`, "td"},
		{"pre", `<pre>fixed</pre>`, "pre"},
		{"br", `line<br>break`, "br"},
		{"hr", `<hr>`, "hr"},
		{"figure", `<figure>fig</figure>`, "figure"},
		{"figcaption", `<figure><figcaption>cap</figcaption></figure>`, "figcaption"},
		// img and its allow-listed attributes (Req 2.2).
		{"img", `<img src="http://example.com/a.png" alt="a">`, "img"},
		{"audio", `<audio src="http://example.com/a.mp3"></audio>`, "audio"},
		{"video", `<video src="http://example.com/a.mp4"></video>`, "video"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := p.SanitizeAt(PostContent, TierB, c.in)
			if err != nil {
				t.Fatalf("SanitizeAt(PostContent, TierB, %q) error = %v", c.in, err)
			}
			if !containsTag(out, c.tag) {
				t.Errorf("tier B dropped allow-listed <%s>: SanitizeAt(%q) = %q", c.tag, c.in, out)
			}
		})
	}
}

func TestTierBAttributesSurvive(t *testing.T) {
	p := New()
	cases := []struct {
		name string
		in   string
		attr string
	}{
		// The global attribute tail (Req 2.2's "deliberately permissive").
		{"class global", `<p class="lead">x</p>`, "class"},
		{"id global", `<p id="first">x</p>`, "id"},
		{"title global", `<p title="hint">x</p>`, "title"},
		{"lang global", `<p lang="en">x</p>`, "lang"},
		{"dir global", `<p dir="ltr">x</p>`, "dir"},
		{"role global", `<div role="note">x</div>`, "role"},
		{"aria-label global", `<p aria-label="info">x</p>`, "aria-label"},
		{"data-* global", `<p data-foo="bar">x</p>`, "data-foo"},
		// Element-specific attributes.
		{"a[href]", `<a href="http://example.com/">x</a>`, "href"},
		{"a[rel]", `<a rel="nofollow" href="http://example.com/">x</a>`, "rel"},
		{"img[src]", `<img src="http://example.com/a.png">`, "src"},
		{"img[alt]", `<img src="http://example.com/a.png" alt="caption">`, "alt"},
		{"img[width]", `<img src="http://example.com/a.png" width="10">`, "width"},
		{"td[colspan]", `<table><tr><td colspan="2">x</td></tr></table>`, "colspan"},
		{"ol[start]", `<ol start="3"><li>x</li></ol>`, "start"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := p.SanitizeAt(PostContent, TierB, c.in)
			if err != nil {
				t.Fatalf("SanitizeAt(PostContent, TierB, %q) error = %v", c.in, err)
			}
			if !containsAttr(out, c.attr) {
				t.Errorf("tier B dropped allow-listed attribute %s: SanitizeAt(%q) = %q", c.attr, c.in, out)
			}
		})
	}
}

// --- Rejection cases, asserted at BOTH tier A and tier B (Req 2.9) ----------

// forbiddenElement names an element whose tag must never survive, in either
// tier A or tier B. Text content may remain (bluemonday keeps the children of
// a dropped element); it is the live tag we assert is gone.
func TestForbiddenElementsStrippedBothTiers(t *testing.T) {
	p := New()
	cases := []struct {
		name string
		in   string
		tag  string
	}{
		{"script", `<script>alert(1)</script>`, "script"},
		{"style", `<style>.x{color:red}</style>`, "style"},
		{"iframe", `<iframe src="http://evil.example/"></iframe>`, "iframe"},
		{"form", `<form action="http://evil.example/"><input></form>`, "form"},
		{"input", `<input type="text" name="x">`, "input"},
	}
	for _, c := range cases {
		for _, tier := range []struct {
			name string
			kind FieldKind
			t    Tier
		}{
			{"tierA", CommentContent, TierA},
			{"tierB", PostContent, TierB},
		} {
			t.Run(c.name+"/"+tier.name, func(t *testing.T) {
				out, err := p.SanitizeAt(tier.kind, tier.t, c.in)
				if err != nil {
					t.Fatalf("SanitizeAt(%v, %v, %q) error = %v", tier.kind, tier.t, c.in, err)
				}
				if containsTag(out, c.tag) {
					t.Errorf("%s kept forbidden <%s>: SanitizeAt(%q) = %q", tier.name, c.tag, c.in, out)
				}
			})
		}
	}
}

// TestOnHandlerStrippedBothTiers checks that an on* event-handler attribute is
// removed while the element it rode in on may survive (Req 2.9). The marker is
// the handler attribute name, not the element.
func TestOnHandlerStrippedBothTiers(t *testing.T) {
	p := New()
	cases := []struct {
		name string
		kind FieldKind
		t    Tier
		in   string
	}{
		// a is allow-listed in both tiers, so the element survives and only the
		// onclick must be stripped.
		{"tierA onclick", CommentContent, TierA, `<a href="http://example.com/" onclick="steal()">x</a>`},
		{"tierB onclick", PostContent, TierB, `<a href="http://example.com/" onclick="steal()">x</a>`},
		{"tierB onerror", PostContent, TierB, `<img src="http://example.com/a.png" onerror="steal()">`},
		{"tierB onload", PostContent, TierB, `<img src="http://example.com/a.png" onload="steal()">`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := p.SanitizeAt(c.kind, c.t, c.in)
			if err != nil {
				t.Fatalf("SanitizeAt(%v, %v, %q) error = %v", c.kind, c.t, c.in, err)
			}
			if strings.Contains(strings.ToLower(out), "onclick") ||
				strings.Contains(strings.ToLower(out), "onerror") ||
				strings.Contains(strings.ToLower(out), "onload") {
				t.Errorf("%s kept an on* handler: SanitizeAt(%q) = %q", c.name, c.in, out)
			}
			if strings.Contains(out, "steal()") {
				t.Errorf("%s kept handler body: SanitizeAt(%q) = %q", c.name, c.in, out)
			}
		})
	}
}

// TestHostileSchemesRejectedBothTiers is the RequireParseableURLs(true) witness
// (Req 2.9; design "URL schemes"). Every javascript: and data: URL on an
// allow-listed, scheme-checked attribute must have its attribute dropped, in
// both tiers. Removing RequireParseableURLs from the construction would make the
// scheme allow-list unconsulted and silently pass these -- so these cases
// failing is the signal that the flag is gone. The element itself survives
// (it is allow-listed); the URL-bearing attribute must not.
func TestHostileSchemesRejectedBothTiers(t *testing.T) {
	p := New()
	cases := []struct {
		name string
		kind FieldKind
		t    Tier
		in   string
		// forbidden is the substring that must NOT appear if the scheme was
		// correctly rejected.
		forbidden string
	}{
		{"tierA a[href] javascript", CommentContent, TierA, `<a href="javascript:alert(1)">x</a>`, "javascript:"},
		{"tierA a[href] data", CommentContent, TierA, `<a href="data:text/html,<b>x">x</a>`, "data:"},
		{"tierA blockquote[cite] javascript", CommentContent, TierA, `<blockquote cite="javascript:alert(1)">q</blockquote>`, "javascript:"},
		{"tierA q[cite] data", CommentContent, TierA, `<q cite="data:text/html,x">q</q>`, "data:"},
		{"tierB a[href] javascript", PostContent, TierB, `<a href="javascript:alert(1)">x</a>`, "javascript:"},
		{"tierB a[href] data", PostContent, TierB, `<a href="data:text/html,x">x</a>`, "data:"},
		{"tierB img[src] javascript", PostContent, TierB, `<img src="javascript:alert(1)">`, "javascript:"},
		{"tierB img[src] data", PostContent, TierB, `<img src="data:image/svg+xml,<svg/onload=alert(1)>">`, "data:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := p.SanitizeAt(c.kind, c.t, c.in)
			if err != nil {
				t.Fatalf("SanitizeAt(%v, %v, %q) error = %v", c.kind, c.t, c.in, err)
			}
			if strings.Contains(strings.ToLower(out), c.forbidden) {
				t.Errorf("%s passed a hostile %s URL (RequireParseableURLs likely not in force): SanitizeAt(%q) = %q",
					c.name, c.forbidden, c.in, out)
			}
		})
	}
}

// --- Construction-specific cases from the design (D8, D14, style) -----------

// TestBareAnchorSurvivesD8 pins the D8 fix: a bare attribute-less <a>text</a>
// must survive both tier B and tier A rather than being dropped for ending with
// zero surviving attributes. bluemonday drops such an element by default unless
// AllowNoAttrs().OnElements is called for it; New() must do that for every
// element in each tier's table.
func TestBareAnchorSurvivesD8(t *testing.T) {
	p := New()
	cases := []struct {
		name string
		kind FieldKind
		t    Tier
	}{
		{"tierA", CommentContent, TierA},
		{"tierB", PostContent, TierB},
	}
	const in = `<a>text</a>`
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := p.SanitizeAt(c.kind, c.t, in)
			if err != nil {
				t.Fatalf("SanitizeAt(%v, %v, %q) error = %v", c.kind, c.t, in, err)
			}
			if !containsTag(out, "a") {
				t.Errorf("%s dropped bare <a> (D8 regression): SanitizeAt(%q) = %q", c.name, in, out)
			}
			if !strings.Contains(out, "text") {
				t.Errorf("%s lost text content of bare <a>: SanitizeAt(%q) = %q", c.name, in, out)
			}
		})
	}
}

// TestGutenbergBlockDelimiterD14 pins D14: a Gutenberg block delimiter is an
// HTML comment, kept at tier B (AllowComments) and stripped at tier A (comments
// left stripped). Stripping it at tier B would destroy block structure in every
// saved post; keeping it at tier A has no legitimate use in a comment body.
func TestGutenbergBlockDelimiterD14(t *testing.T) {
	p := New()
	const in = `<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->`

	outB, err := p.SanitizeAt(PostContent, TierB, in)
	if err != nil {
		t.Fatalf("SanitizeAt(PostContent, TierB, %q) error = %v", in, err)
	}
	if !strings.Contains(outB, "wp:paragraph") {
		t.Errorf("tier B stripped a Gutenberg block delimiter (D14): SanitizeAt(%q) = %q", in, outB)
	}

	outA, err := p.SanitizeAt(CommentContent, TierA, in)
	if err != nil {
		t.Fatalf("SanitizeAt(CommentContent, TierA, %q) error = %v", in, err)
	}
	if strings.Contains(outA, "wp:paragraph") || strings.Contains(outA, "<!--") {
		t.Errorf("tier A kept an HTML comment (D14): SanitizeAt(%q) = %q", in, outA)
	}
}

// TestTierBStyleProperties pins the tier-B style property allow-list (Req 2.10):
// a style attribute carrying one allowed property (color) and one forbidden one
// (position is dropped here as a stand-in for "property bluemonday cannot
// validate / WordPress forbids") keeps the allowed declaration and drops the
// forbidden one, rather than passing the declaration block through unexamined.
func TestTierBStyleProperties(t *testing.T) {
	p := New()
	// color is in the tier-B property list; -moz-binding is not and is a classic
	// active-content CSS sink, so it must be dropped. The style attribute itself
	// must survive (it is in the global tail), carrying only the allowed property.
	const in = `<p style="color:red;-moz-binding:url(http://evil.example/x.xml)">x</p>`
	out, err := p.SanitizeAt(PostContent, TierB, in)
	if err != nil {
		t.Fatalf("SanitizeAt(PostContent, TierB, %q) error = %v", in, err)
	}
	if !strings.Contains(out, "color") {
		t.Errorf("tier B dropped the allowed style property color: SanitizeAt(%q) = %q", in, out)
	}
	if strings.Contains(strings.ToLower(out), "-moz-binding") {
		t.Errorf("tier B kept a forbidden style property: SanitizeAt(%q) = %q", in, out)
	}
}
