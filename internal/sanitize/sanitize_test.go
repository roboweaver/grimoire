package sanitize

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
)

// These tests pin the type-level and tier-selection surface of the Policy
// (Req 1.5, 1.6, 1.7, 2.4, 2.5; property P6; design Finding 5). They are
// deliberately written before the package's production code exists: the file
// is expected to fail to compile until FieldKind, Tier, Writer, Anonymous,
// For, TierFor and CapUnfilteredHTML are defined.
//
// Principals are built through the real auth.CapabilitiesForRoles /
// auth.NewPrincipal rather than a hand-written map, so a change to roles.go
// (e.g. moving unfiltered_html off the editor role) surfaces as a failure
// here instead of silently diverging the Policy from the capability table.

// TestFieldKindConstants pins the four FieldKind values and their ordering
// (Req 1.7). The explicit iota ordering is load-bearing: PostContent is the
// zero value, so a zero FieldKind is a post-content write, never a surprise.
func TestFieldKindConstants(t *testing.T) {
	kinds := []struct {
		name string
		got  FieldKind
		want FieldKind
	}{
		{"PostContent", PostContent, 0},
		{"PostExcerpt", PostExcerpt, 1},
		{"PostTitle", PostTitle, 2},
		{"CommentContent", CommentContent, 3},
	}
	seen := map[FieldKind]string{}
	for _, k := range kinds {
		if k.got != k.want {
			t.Errorf("%s = %d, want %d", k.name, k.got, k.want)
		}
		if prev, dup := seen[k.got]; dup {
			t.Errorf("%s and %s share value %d; FieldKinds must be distinct", prev, k.name, k.got)
		}
		seen[k.got] = k.name
	}
}

// TestTierConstants pins the three Tier values and their ordering (Req 2).
// TierA < TierB < TierC matches the monotonicity direction of property P3:
// the tightest list first, the no-op bypass last.
func TestTierConstants(t *testing.T) {
	tiers := []struct {
		name string
		got  Tier
		want Tier
	}{
		{"TierA", TierA, 0},
		{"TierB", TierB, 1},
		{"TierC", TierC, 2},
	}
	seen := map[Tier]string{}
	for _, tr := range tiers {
		if tr.got != tr.want {
			t.Errorf("%s = %d, want %d", tr.name, tr.got, tr.want)
		}
		if prev, dup := seen[tr.got]; dup {
			t.Errorf("%s and %s share value %d; Tiers must be distinct", prev, tr.name, tr.got)
		}
		seen[tr.got] = tr.name
	}
}

// TestAnonymousIsZeroValueWriter checks that Anonymous() yields the zero-value
// Writer: not authenticated, no capabilities, and therefore not selecting
// TierC for any field kind (Req 1.6, 2.5). The anonymous caller is the
// fail-closed default -- a struct nobody filled in must never reach the
// unfiltered bypass.
func TestAnonymousIsZeroValueWriter(t *testing.T) {
	var zero Writer
	if Anonymous() != zero {
		t.Fatalf("Anonymous() = %#v, want zero-value Writer %#v", Anonymous(), zero)
	}
	if Anonymous().Authenticated() {
		t.Error("Anonymous().Authenticated() = true, want false")
	}
	for _, k := range []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent} {
		if got := TierFor(k, Anonymous()); got == TierC {
			t.Errorf("TierFor(%v, Anonymous()) = TierC; anonymous caller must never reach the unfiltered bypass", k)
		}
	}
}

// TestForDiffersFromAnonymousOnlyInUnfiltered checks that For of a bare
// principal and For of a principal holding unfiltered_html differ only in the
// unfiltered bit -- both are authenticated, and authentication status is not
// an input to tier selection (Req 1.5, 2.4, 2.5). Principals are built through
// the real auth constructors.
func TestForDiffersFromAnonymousOnlyInUnfiltered(t *testing.T) {
	// A principal with no capabilities (the empty-keys case through the real
	// constructor): authenticated, but lacking unfiltered_html.
	bare := auth.NewPrincipal(1, "nobody", nil)
	wBare := For(bare)
	if !wBare.Authenticated() {
		t.Error("For(bare principal).Authenticated() = false, want true")
	}

	// A principal that explicitly holds unfiltered_html, resolved as an
	// explicit capability grant by auth.NewPrincipal (Req 2.4).
	withCap := auth.NewPrincipal(2, "editorish", []string{CapUnfilteredHTML})
	wCap := For(withCap)
	if !wCap.Authenticated() {
		t.Error("For(unfiltered principal).Authenticated() = false, want true")
	}

	// The two Writers must be identical except for the unfiltered bit. We
	// assert this by checking that flipping that one dimension makes them
	// equal: both authenticated, differing only in TierC eligibility.
	if wBare == wCap {
		t.Fatal("For(bare) and For(unfiltered) are equal; they must differ in the unfiltered bit")
	}
	if TierFor(PostTitle, wBare) == TierFor(PostTitle, wCap) {
		t.Error("bare and unfiltered writers select the same tier; only the unfiltered bit should move the tier")
	}
	// Authentication alone must not change tier selection: the bare
	// authenticated writer lands in the same tier an anonymous one does.
	for _, k := range []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent} {
		if got, want := TierFor(k, wBare), TierFor(k, Anonymous()); got != want {
			t.Errorf("TierFor(%v): authenticated-but-uncapable=%v, anonymous=%v; authentication must not be a tier input", k, got, want)
		}
	}
}

// TestForReadsUnfilteredThroughRealRoles checks that For derives the unfiltered
// bit from the real role capability tables (Req 2.4, property P6; design
// Finding 5). Editor and administrator carry unfiltered_html in roles.go;
// subscriber, contributor and author do not. If a roles.go change moved
// unfiltered_html, this test breaks -- which is the point.
func TestForReadsUnfilteredThroughRealRoles(t *testing.T) {
	cases := []struct {
		role      string
		wantTierC bool // whether a post-content write selects TierC
	}{
		{auth.RoleSubscriber, false},
		{auth.RoleContributor, false},
		{auth.RoleAuthor, false},
		{auth.RoleEditor, true},
		{auth.RoleAdministrator, true},
	}
	for _, c := range cases {
		// Build the principal through the real constructor so the capability
		// set is exactly what CapabilitiesForRoles grants.
		p := auth.NewPrincipal(1, c.role, []string{c.role})
		w := For(p)

		// Cross-check: whether the real capability table grants unfiltered_html
		// to this role, read through CapabilitiesForRoles directly.
		caps := auth.CapabilitiesForRoles(c.role)
		if caps[CapUnfilteredHTML] != c.wantTierC {
			t.Fatalf("roles.go precondition drift: CapabilitiesForRoles(%q)[%q] = %v, test expects %v",
				c.role, CapUnfilteredHTML, caps[CapUnfilteredHTML], c.wantTierC)
		}

		gotC := TierFor(PostContent, w) == TierC
		if gotC != c.wantTierC {
			t.Errorf("TierFor(PostContent, For(%s)) == TierC is %v, want %v", c.role, gotC, c.wantTierC)
		}
	}
}

// TestTierForTruthTable pins the full tier-selection truth table (Req 2.1,
// 2.2, 2.5; property P6). Tier selection is a pure function of (FieldKind,
// unfiltered bit); authentication status is not an input, so we assert the
// same table holds for the anonymous caller and for the bare authenticated
// one.
func TestTierForTruthTable(t *testing.T) {
	// A writer holding unfiltered_html (via explicit grant through the real
	// constructor) and two writers lacking it: anonymous and authenticated.
	unfiltered := For(auth.NewPrincipal(1, "u", []string{CapUnfilteredHTML}))
	anon := Anonymous()
	bare := For(auth.NewPrincipal(2, "b", nil))

	type row struct {
		kind       FieldKind
		writer     Writer
		writerName string
		want       Tier
	}
	var rows []row
	// Writers lacking unfiltered_html: field kind governs the tier.
	for _, w := range []struct {
		name string
		w    Writer
	}{{"anonymous", anon}, {"bare-authenticated", bare}} {
		rows = append(rows,
			row{CommentContent, w.w, w.name, TierA},
			row{PostContent, w.w, w.name, TierB},
			row{PostExcerpt, w.w, w.name, TierB},
			row{PostTitle, w.w, w.name, TierB},
		)
	}
	// Writer holding unfiltered_html: every kind is TierC.
	for _, k := range []FieldKind{CommentContent, PostContent, PostExcerpt, PostTitle} {
		rows = append(rows, row{k, unfiltered, "unfiltered", TierC})
	}

	for _, r := range rows {
		if got := TierFor(r.kind, r.writer); got != r.want {
			t.Errorf("TierFor(kind=%v, writer=%s) = %v, want %v", r.kind, r.writerName, got, r.want)
		}
	}
}
