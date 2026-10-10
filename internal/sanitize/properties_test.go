package sanitize

import (
	stdhtml "html"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/quick"

	"golang.org/x/net/html"
)

// This file holds the executable correctness properties of the pure package:
// P1 (idempotence), P2 (allow-list closure), P3 (tier monotonicity), P6 (tier
// selection purity + determinism) and the title convergence property P5/P9.
// Every property named in design.md's "Mechanism per property" table that is
// driven by generated structured input lives here; the byte-oriented properties
// (P4, the fuzz arms of P2/P5) live in fuzz_test.go and the import-set arm of P6
// lives in importset_test.go (Req 9.9; design "Mechanism per property").
//
// The generator and the quick.Config below are shared by every property so a
// single corpus and a single fixed seed drive them all, which is what makes a CI
// failure reproducible and the suite itself deterministic (design: "a fixed
// seed, so a CI failure is reproducible and the test is itself deterministic").

// quickConfig is the shared testing/quick configuration. MaxCount is 1000 so
// each property sees a thousand generated inputs, and Rand is seeded from a
// fixed source so the generated corpus is identical on every run and in every
// process -- a failure a reviewer sees locally is the failure CI saw (design
// "quick.Config{MaxCount: 1000, Rand: rand.New(rand.NewSource(1))}").
func quickConfig() *quick.Config {
	return &quick.Config{
		MaxCount: 1000,
		Rand:     rand.New(rand.NewSource(1)),
	}
}

// markupFragments is the corpus markupish draws from. The default string
// generator testing/quick ships produces random runes that almost never form
// markup, so a property quantified over it would exercise the sanitizer on text
// that never trips a single allow-list rule. This corpus is deliberately
// markup-dense: allow-listed tags, disallowed tags, nested and unclosed tags,
// mixed-case attribute names, on* handlers, hostile URL schemes, doubled
// encodings, stray angle brackets, unbalanced quotes and entity sequences
// (design: "assembles inputs from a fragment corpus").
var markupFragments = []string{
	// allow-listed tags (both tiers / tier B)
	"<a href=\"http://example.com/\">link</a>",
	"<a>bare</a>",
	"<em>em</em>",
	"<strong>strong</strong>",
	"<blockquote cite=\"http://x/\">q</blockquote>",
	"<p>para</p>",
	"<div class=\"c\">block</div>",
	"<span id=\"s\">inline</span>",
	"<img src=\"http://example.com/a.png\" alt=\"a\">",
	"<ul><li>item</li></ul>",
	"<table><tr><td>cell</td></tr></table>",
	"<code>x := 1</code>",
	"<del datetime=\"2024-01-01\">d</del>",
	// disallowed tags
	"<script>alert(1)</script>",
	"<style>.x{color:red}</style>",
	"<iframe src=\"http://evil/\"></iframe>",
	"<form action=\"http://evil/\"><input></form>",
	"<object data=\"http://evil/\"></object>",
	"<title>t</title>",
	"<textarea>t</textarea>",
	// MathML (dropped at tier B)
	"<math><mtext>x</mtext></math>",
	// nested and unclosed
	"<b><i>deep</b></i>",
	"<a><a><a>x",
	"<div><p>unclosed",
	"<<<",
	"<b",
	"<p/>",
	// mixed-case attribute names and tags
	"<A HREF=\"http://x/\" TITLE=\"t\">y</A>",
	"<IMG SRC=\"http://x/a.png\" OnErRoR=\"steal()\">",
	// on* handlers
	"<a href=\"http://x/\" onclick=\"steal()\">x</a>",
	"<img src=\"http://x/a.png\" onerror=\"steal()\">",
	"<div onmouseover=\"x()\">y</div>",
	// hostile schemes
	"<a href=\"javascript:alert(1)\">x</a>",
	"<a href=\"data:text/html,<b>x\">x</a>",
	"<img src=\"data:image/svg+xml,<svg/onload=alert(1)>\">",
	"<blockquote cite=\"javascript:alert(1)\">q</blockquote>",
	// doubled / nested encodings and entity sequences
	"&amp;lt;b&amp;gt;",
	"&lt;script&gt;alert(1)&lt;/script&gt;",
	"&amp;amp;",
	"5 &amp; 6",
	"&#60;b&#62;",
	// stray delimiters, unbalanced quotes, Gutenberg comments
	"a < b > c",
	"<p class=\"unterminated>x</p>",
	"\" onload=\"x",
	"<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->",
	// style attribute values (one allowed, one forbidden property)
	"<p style=\"color:red;-moz-binding:url(http://evil/x.xml)\">x</p>",
	"<p style=\"position:fixed;top:0\">x</p>",
	// bare text
	"hello world",
	"",
}

// markupish is a markup-dense string generator for testing/quick. It implements
// quick.Generator so quick drives it directly: Generate concatenates between 0
// and ~size+2 fragments drawn uniformly from markupFragments, so a single input
// mixes allow-listed and hostile markup, nested encodings and stray delimiters
// the way a real attack string does. The generator is seeded by the Rand quick
// passes, which comes from quickConfig's fixed source, so the whole corpus is
// deterministic (design "type markupish string implements quick.Generator").
type markupish string

// Generate implements quick.Generator. The returned reflect.Value wraps a
// markupish, assembled from a random count of corpus fragments joined by random
// single-character "glue" (a space, an angle bracket, an ampersand or nothing)
// so adjacent fragments sometimes fuse into a new malformed token rather than
// staying cleanly separated.
func (markupish) Generate(r *rand.Rand, size int) reflect.Value {
	glue := []string{"", " ", "<", ">", "&", "\"", "\n"}
	n := r.Intn(size + 3) // 0..size+2 fragments
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(markupFragments[r.Intn(len(markupFragments))])
		if i < n-1 {
			b.WriteString(glue[r.Intn(len(glue))])
		}
	}
	return reflect.ValueOf(markupish(b.String()))
}

// nonTitleKinds is the set of field kinds P1 and P2 quantify over: every field
// kind EXCEPT PostTitle. PostTitle is excluded from P1 by statement (Finding 1):
// the title path is a bounded fixed-point loop, not an allow-list filter, and it
// is covered by the convergence property P5/P9 below instead.
var nonTitleKinds = []FieldKind{PostContent, PostExcerpt, CommentContent}

// filteringTiers is the set of tiers whose output P1 and P2 constrain: tier A
// and tier B. Tier C is the unfiltered byte-identity bypass (P4), so neither
// idempotence-beyond-identity nor allow-list closure says anything new about it.
var filteringTiers = []Tier{TierA, TierB}

// -----------------------------------------------------------------------------
// P1 -- Idempotence (Req 9.9; design P1; Finding 1)
//
// For all inputs x, all tiers t and every field kind k EXCEPT PostTitle:
//   Sanitize(k, t, Sanitize(k, t, x)) == Sanitize(k, t, x)
//
// A non-idempotent sanitizer emits output that is not in the language it claims
// to produce -- the classic symptom of a filter a pre-encoded input can walk
// past. PostTitle is excluded here deliberately and asserted through convergence
// (P5/P9) instead, so this property reads as a design rather than as a loop
// coincidentally rescuing one kind.
// -----------------------------------------------------------------------------

func TestP1Idempotence(t *testing.T) {
	p := New()
	for _, k := range nonTitleKinds {
		for _, tier := range filteringTiers {
			k, tier := k, tier
			t.Run(fieldKindName(k)+"/"+tierName(tier), func(t *testing.T) {
				f := func(in markupish) bool {
					once, err := p.SanitizeAt(k, tier, string(in))
					if err != nil {
						return false // no error is expected at tier A/B for non-title kinds
					}
					twice, err := p.SanitizeAt(k, tier, once)
					if err != nil {
						return false
					}
					return once == twice
				}
				if err := quick.Check(f, quickConfig()); err != nil {
					t.Errorf("P1 idempotence failed for kind=%s tier=%s: %v",
						fieldKindName(k), tierName(tier), err)
				}
			})
		}
	}
}

// -----------------------------------------------------------------------------
// P2 -- Allow-list closure (Req 9.9; design P2)
//
// For all x, parsing Sanitize(k, t, x) yields no element and no attribute outside
// tier t's allow-list, and no URL-bearing attribute whose scheme is outside the
// allowed scheme set. Asserted on the PARSED TREE, re-tokenized with
// golang.org/x/net/html -- a substring check would pass an implementation that
// emitted "<scr<script>ipt>". The sanitized output is a closed point of the
// policy: feeding it back through the parser surfaces nothing the table forbids.
// -----------------------------------------------------------------------------

func TestP2Closure(t *testing.T) {
	p := New()
	for _, k := range nonTitleKinds {
		for _, tier := range filteringTiers {
			k, tier := k, tier
			t.Run(fieldKindName(k)+"/"+tierName(tier), func(t *testing.T) {
				allowed := allowedElementsFor(tier)
				f := func(in markupish) bool {
					out, err := p.SanitizeAt(k, tier, string(in))
					if err != nil {
						return false
					}
					return outputIsClosed(out, tier, allowed)
				}
				if err := quick.Check(f, quickConfig()); err != nil {
					t.Errorf("P2 closure failed for kind=%s tier=%s: %v",
						fieldKindName(k), tierName(tier), err)
				}
			})
		}
	}
}

// outputIsClosed re-tokenizes out with golang.org/x/net/html and reports whether
// every element, every attribute and every URL-bearing attribute's scheme is
// permitted by the given tier. It walks the token stream rather than the DOM so
// a token the policy emitted but the tree builder would discard is still checked.
func outputIsClosed(out string, tier Tier, allowed map[string]map[string]bool) bool {
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			// io.EOF (clean end) or a tokenizer error; either way there is
			// nothing more to inspect.
			return true
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			name := strings.ToLower(tok.Data)
			attrs, ok := allowed[name]
			if !ok {
				return false // element outside the tier's allow-list survived
			}
			for _, a := range tok.Attr {
				an := strings.ToLower(a.Key)
				if !attributeAllowed(tier, attrs, an) {
					return false
				}
				if isURLAttr(name, an) && !schemeAllowed(a.Val) {
					return false
				}
			}
		}
	}
}

// allowedElementsFor returns the lower-cased element->attribute allow-list for a
// tier, read straight from the construction tables so the property cannot drift
// from the policy (design "The tables are data, not code"). Tier A has no global
// attribute set; tier B's per-element sets are unioned with the global tail.
func allowedElementsFor(tier Tier) map[string]map[string]bool {
	src := tierAElements
	if tier == TierB {
		src = tierBElements
	}
	out := make(map[string]map[string]bool, len(src))
	for el, attrs := range src {
		set := make(map[string]bool, len(attrs))
		for _, a := range attrs {
			set[strings.ToLower(a)] = true
		}
		out[strings.ToLower(el)] = set
	}
	return out
}

// attributeAllowed reports whether an attribute name is permitted on an element
// at a tier, given that element's own attribute set. Tier A permits only the
// element-specific set. Tier B additionally permits the global tail, the
// data-* family, and style (which the policy filters by value, not by presence);
// these are the Globally()/AllowDataAttributes()/AllowStyles() grants in New.
func attributeAllowed(tier Tier, elemAttrs map[string]bool, attr string) bool {
	if elemAttrs[attr] {
		return true
	}
	if tier == TierB {
		if tierBGlobalAttrSet[attr] {
			return true
		}
		if attr == "style" {
			return true
		}
		if strings.HasPrefix(attr, "data-") {
			return true
		}
	}
	return false
}

// tierBGlobalAttrSet is tierBGlobalAttrs as a lower-cased lookup set, built once.
var tierBGlobalAttrSet = func() map[string]bool {
	m := make(map[string]bool, len(tierBGlobalAttrs))
	for _, a := range tierBGlobalAttrs {
		m[strings.ToLower(a)] = true
	}
	return m
}()

// allowedSchemeSet is allowedSchemes as a lower-cased lookup set, built once.
var allowedSchemeSet = func() map[string]bool {
	m := make(map[string]bool, len(allowedSchemes))
	for _, s := range allowedSchemes {
		m[strings.ToLower(s)] = true
	}
	return m
}()

// isURLAttr reports whether (element, attribute) is one of the scheme-bearing
// pairs bluemonday scheme-checks (sanitize.go's validURL switch). Only these can
// carry a scheme the policy is responsible for, so only these are checked for
// closure -- the design's three scheme-bearing exclusions keep every URL
// attribute either tier grants inside this set.
func isURLAttr(el, attr string) bool {
	switch attr {
	case "href":
		return el == "a" || el == "area" || el == "base" || el == "link"
	case "cite":
		return el == "blockquote" || el == "del" || el == "ins" || el == "q"
	case "src":
		switch el {
		case "audio", "embed", "iframe", "img", "script", "source", "track", "video":
			return true
		}
	}
	return false
}

// schemeAllowed reports whether a URL-bearing attribute value carries only an
// allowed scheme. A relative URL (no scheme) is allowed, matching
// AllowRelativeURLs(true). A value whose scheme is not in allowedSchemeSet --
// notably javascript: and data: -- is rejected.
func schemeAllowed(val string) bool {
	v := strings.TrimSpace(val)
	colon := strings.IndexByte(v, ':')
	if colon < 0 {
		return true // relative, no scheme
	}
	// A ':' that appears after a '/', '?', '#' or '@' is part of the path or
	// authority, not a scheme (e.g. "/a:b" or "foo?x:y"); such URLs are relative.
	for i := 0; i < colon; i++ {
		switch v[i] {
		case '/', '?', '#', '@':
			return true
		}
	}
	scheme := strings.ToLower(v[:colon])
	return allowedSchemeSet[scheme]
}

// -----------------------------------------------------------------------------
// P3 -- Tier monotonicity (Req 2.6, 9.9; design P3)
//
// allow(A) ⊆ allow(B) ⊆ allow(C), and for all x, every element surviving tier A
// also survives tier B. The containment half is set containment over the tables
// themselves (the honest way to assert a property OF the lists); the survival
// half is generated. The HTML-comment carve-out -- tier A strips comments, tier
// B keeps them -- is on a separate axis from elements/attributes and is asserted
// explicitly so it is not mistaken for a monotonicity violation.
// -----------------------------------------------------------------------------

// TestP3TableContainment is the set-containment half: every tier-A element is a
// tier-B element, and every tier-A attribute grant on an element is present on
// the same element in tier B (either element-specifically or via the global
// tail). Tier C "allows everything", so A ⊆ B ⊆ C reduces to A ⊆ B.
func TestP3TableContainment(t *testing.T) {
	for el, aAttrs := range tierAElements {
		bAttrs, ok := tierBElements[el]
		if !ok {
			t.Errorf("P3: tier-A element %q is not in tier B (A ⊆ B broken)", el)
			continue
		}
		bSet := make(map[string]bool, len(bAttrs))
		for _, a := range bAttrs {
			bSet[strings.ToLower(a)] = true
		}
		for _, a := range aAttrs {
			la := strings.ToLower(a)
			if bSet[la] || tierBGlobalAttrSet[la] {
				continue
			}
			t.Errorf("P3: tier-A grant %s[%s] is absent from tier B (A ⊆ B broken)", el, a)
		}
	}
}

// TestP3SurvivalMonotone is the generated half: for every input, every element
// that survives tier A also survives tier B. Walking the two token streams and
// comparing the multiset of surviving element names is enough -- if a tag the
// tight list kept were dropped by the broad list, that is the inversion P3 forbids.
func TestP3SurvivalMonotone(t *testing.T) {
	p := New()
	f := func(in markupish) bool {
		outA, err := p.SanitizeAt(CommentContent, TierA, string(in))
		if err != nil {
			return false
		}
		outB, err := p.SanitizeAt(PostContent, TierB, string(in))
		if err != nil {
			return false
		}
		a := elementCounts(outA)
		b := elementCounts(outB)
		for el, nA := range a {
			if b[el] < nA {
				return false // tier A kept more of this element than tier B did
			}
		}
		return true
	}
	if err := quick.Check(f, quickConfig()); err != nil {
		t.Errorf("P3 survival monotonicity failed (an element survived tier A but not tier B): %v", err)
	}
}

// TestP3CommentCarveOut pins the one place tier A is STRICTER than tier B: HTML
// comments. Tier A strips them (AllowComments not called); tier B keeps them
// (Gutenberg block delimiters are HTML comments). This is a separate axis from
// element/attribute containment and points the same way (A strips, B keeps), so
// nothing survives tier A only to be removed at tier B.
func TestP3CommentCarveOut(t *testing.T) {
	p := New()
	const in = `<!-- keep me --><p>x</p>`

	outA, err := p.SanitizeAt(CommentContent, TierA, in)
	if err != nil {
		t.Fatalf("tier A: %v", err)
	}
	if strings.Contains(outA, "<!--") {
		t.Errorf("P3 carve-out: tier A kept an HTML comment: %q", outA)
	}

	outB, err := p.SanitizeAt(PostContent, TierB, in)
	if err != nil {
		t.Fatalf("tier B: %v", err)
	}
	if !strings.Contains(outB, "<!--") {
		t.Errorf("P3 carve-out: tier B stripped an HTML comment: %q", outB)
	}
}

// TestP3TierCKeepsEverything pins the ⊆ TierC end of the chain (Req 2.11): tier C
// is the unfiltered bypass, so for any input it returns the input byte-for-byte
// and therefore "allows everything" -- no element or attribute the broad list
// kept can be absent at tier C.
func TestP3TierCKeepsEverything(t *testing.T) {
	p := New()
	f := func(in markupish) bool {
		for _, k := range []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent} {
			out, err := p.SanitizeAt(k, TierC, string(in))
			if err != nil || out != string(in) {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, quickConfig()); err != nil {
		t.Errorf("P3 tier-C-keeps-everything failed (tier C altered its input): %v", err)
	}
}

// elementCounts tokenizes s and returns a multiset of lower-cased element names
// seen as start or self-closing tags.
func elementCounts(s string) map[string]int {
	counts := map[string]int{}
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return counts
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			tok := z.Token()
			counts[strings.ToLower(tok.Data)]++
		}
	}
}

// -----------------------------------------------------------------------------
// P6 -- Tier selection purity, plus determinism (Req 1.9, 2.5, 2.7, 9.9; design P6)
//
// TierFor is a pure function of (FieldKind, the writer's unfiltered bit) and of
// nothing else -- notably not of Authenticated() (Req 2.5). The determinism arms
// catch the realistic failures: map-iteration-order dependence (invisible in a
// single-shot test) and a data race under concurrent use (Req 1.9). The import-set
// arm of design's P6 is a separate file (importset_test.go, task 3.3).
// -----------------------------------------------------------------------------

// TestP6TierSelectionPurity asserts TierFor depends only on (FieldKind,
// unfiltered). It drives all four field kinds against four Writers that cover
// the two-bit (authenticated, unfiltered) space, and asserts (a) the selected
// tier matches the pure truth table and (b) Authenticated() never moves the tier:
// the authenticated-but-not-unfiltered writer selects the same tier as the
// anonymous one for every kind.
func TestP6TierSelectionPurity(t *testing.T) {
	anon := Anonymous()                                             // {authenticated:false, unfiltered:false}
	authNoCap := Writer{authenticated: true}                        // logged-in, no unfiltered_html
	anonUnfiltered := Writer{unfiltered: true}                      // (not reachable via For, but TierFor must be pure in its bits)
	authUnfiltered := Writer{authenticated: true, unfiltered: true} // editor/administrator

	for _, k := range []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent} {
		want := TierB
		if k == CommentContent {
			want = TierA
		}

		if got := TierFor(k, anon); got != want {
			t.Errorf("TierFor(%s, anon) = %s, want %s", fieldKindName(k), tierName(got), tierName(want))
		}
		// Authenticated must not move the tier (Req 2.5): same tier as anon.
		if got := TierFor(k, authNoCap); got != want {
			t.Errorf("TierFor(%s, authNoCap) = %s, want %s (Authenticated must not change tier)",
				fieldKindName(k), tierName(got), tierName(want))
		}
		// The unfiltered bit, regardless of the authenticated bit, selects tier C.
		if got := TierFor(k, anonUnfiltered); got != TierC {
			t.Errorf("TierFor(%s, {unfiltered}) = %s, want TierC", fieldKindName(k), tierName(got))
		}
		if got := TierFor(k, authUnfiltered); got != TierC {
			t.Errorf("TierFor(%s, {auth,unfiltered}) = %s, want TierC", fieldKindName(k), tierName(got))
		}
	}
}

// TestP6TierSelectionDeterministic is the generated purity arm: for any pair of
// writers sharing the same unfiltered bit, TierFor returns the same tier for the
// same field kind no matter what the authenticated bit is. Quantified so a future
// change that started reading Authenticated() in TierFor is caught.
func TestP6TierSelectionDeterministic(t *testing.T) {
	f := func(unfiltered bool, kindSel uint8) bool {
		kinds := []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent}
		k := kinds[int(kindSel)%len(kinds)]
		w1 := Writer{authenticated: false, unfiltered: unfiltered}
		w2 := Writer{authenticated: true, unfiltered: unfiltered}
		return TierFor(k, w1) == TierFor(k, w2)
	}
	if err := quick.Check(f, quickConfig()); err != nil {
		t.Errorf("P6 tier-selection purity failed (Authenticated() influenced the tier): %v", err)
	}
}

// TestP6DeterministicSequential is design's P6(a): 1000 sequential invocations of
// Sanitize on one input, in one process, must yield exactly one distinct output.
// A single-shot test cannot see map-iteration-order dependence; this can.
func TestP6DeterministicSequential(t *testing.T) {
	p := New()
	const in = `<p style="color:red;background:blue" class="a b c" id="x" data-k="v">` +
		`<a href="http://example.com/" rel="nofollow" title="t">link</a>` +
		`<img src="http://example.com/a.png" alt="a" width="10" height="20"></p>`
	first, err := p.SanitizeAt(PostContent, TierB, in)
	if err != nil {
		t.Fatalf("SanitizeAt: %v", err)
	}
	for i := 0; i < 1000; i++ {
		got, err := p.SanitizeAt(PostContent, TierB, in)
		if err != nil {
			t.Fatalf("SanitizeAt (iteration %d): %v", i, err)
		}
		if got != first {
			t.Fatalf("P6 determinism: invocation %d differs from the first\n first = %q\n   got = %q", i, first, got)
		}
	}
}

// TestP6ConcurrentRace is design's P6(b): 64 goroutines each sanitizing the same
// input 100 times over one shared *Policy, every output compared against the
// sequential result (Req 1.9). Run under `go test -race` it fails on any data
// race in the shared, supposedly-immutable compiled policy.
func TestP6ConcurrentRace(t *testing.T) {
	p := New()
	const in = `<p class="x"><a href="http://example.com/">link</a><script>alert(1)</script></p>`
	want, err := p.SanitizeAt(PostContent, TierB, in)
	if err != nil {
		t.Fatalf("SanitizeAt: %v", err)
	}

	const goroutines, iterations = 64, 100
	var wg sync.WaitGroup
	errs := make(chan string, goroutines*iterations)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				got, err := p.SanitizeAt(PostContent, TierB, in)
				if err != nil {
					errs <- "unexpected error: " + err.Error()
					return
				}
				if got != want {
					errs <- "concurrent output diverged: got " + got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	if msg, ok := <-errs; ok {
		t.Fatalf("P6 concurrency: %s", msg)
	}
}

// -----------------------------------------------------------------------------
// P5 / P9 -- Title convergence (Req 3.1-3.4, 3.8, 9.9; design P5, P9; Finding 1)
//
// For all x, the PostTitle path either:
//   (a) converges to a value that contains no '<' and no '>', is a fixed point of
//       UnescapeString (introduces no entity sequence), and is itself a fixed
//       point of the title function -- titleText(titleText(x)) == titleText(x);
// or
//   (b) fails closed with ErrTitleNotConverging and persists nothing.
// This is the narrowed form of P1 for titles (P1 excludes PostTitle) and is
// stronger: it asserts the loop terminates inside its bound AND that the bounded
// result is genuinely a fixed point.
// -----------------------------------------------------------------------------

func TestP5P9TitleConvergence(t *testing.T) {
	p := New()
	// Quantify over tier A and tier B: both route PostTitle through titleText
	// (the title is governed by WordPress's comment list, not the post list).
	for _, tier := range filteringTiers {
		tier := tier
		t.Run(tierName(tier), func(t *testing.T) {
			f := func(in markupish) bool {
				out, err := p.SanitizeAt(PostTitle, tier, string(in))
				if err != nil {
					// Fail-closed is a legitimate outcome: it must be exactly the
					// convergence sentinel and must carry no value (Req 3.8, 1.8).
					return err == ErrTitleNotConverging && out == ""
				}
				// (a) no markup delimiters (Req 3.3).
				if strings.ContainsAny(out, "<>") {
					return false
				}
				// (b) no entity sequence: output is a fixed point of UnescapeString
				// (a stronger, simpler check than pattern-matching entity syntax).
				if stdhtml.UnescapeString(out) != out {
					return false
				}
				// (c) the result is itself a fixed point of the title function
				// (the narrowed P1 for titles).
				again, err := p.SanitizeAt(PostTitle, tier, out)
				if err != nil {
					return false
				}
				return again == out
			}
			if err := quick.Check(f, quickConfig()); err != nil {
				t.Errorf("P5/P9 title convergence failed at tier %s: %v", tierName(tier), err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func fieldKindName(k FieldKind) string {
	switch k {
	case PostContent:
		return "PostContent"
	case PostExcerpt:
		return "PostExcerpt"
	case PostTitle:
		return "PostTitle"
	case CommentContent:
		return "CommentContent"
	default:
		return "FieldKind(?)"
	}
}

func tierName(t Tier) string {
	switch t {
	case TierA:
		return "TierA"
	case TierB:
		return "TierB"
	case TierC:
		return "TierC"
	default:
		return "Tier(?)"
	}
}
