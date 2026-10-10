package sanitize

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
)

// TestTierSelectionMatrix is the canonical tier-selection matrix (Req 9.3): the
// full (role/writer × FieldKind) selection table asserted in one place, built
// through the real auth.CapabilitiesForRoles / auth.NewPrincipal so a change to
// roles.go (for example moving unfiltered_html off the editor role) surfaces as
// a failure here rather than silently diverging the Policy from the capability
// table.
//
// Tier selection is a pure function of (FieldKind, whether the writer holds
// unfiltered_html); authentication status is not an input (Req 2.5). The matrix
// is therefore organised by whether the writer holds unfiltered_html:
//
//   - A writer without unfiltered_html (anonymous, subscriber, contributor,
//     author) selects TierA for CommentContent and TierB for the three post
//     fields. In particular subscriber, contributor and author commenting ALL
//     select TierA -- the cell an earlier draft of the spec got wrong and the
//     one a regression would most plausibly reintroduce.
//   - A writer holding unfiltered_html (editor, administrator, and any custom
//     role granted the capability) selects TierC for every field kind.
//
// Req 2.4 is pinned by the custom-role row: a principal built from a role name
// that is not one of the five defaults but was granted unfiltered_html as an
// explicit capability still reaches TierC, with no additional resolution step.
func TestTierSelectionMatrix(t *testing.T) {
	// Every field kind, in iota order, so the matrix is exhaustive over the
	// FieldKind axis.
	allKinds := []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent}

	// expected returns the tier a writer lacking (false) or holding (true)
	// unfiltered_html selects for the given kind. This is the matrix stated
	// once; every row below is checked against it.
	expected := func(kind FieldKind, unfiltered bool) Tier {
		if unfiltered {
			return TierC
		}
		if kind == CommentContent {
			return TierA
		}
		return TierB
	}

	rows := []struct {
		name           string
		writer         Writer
		wantUnfiltered bool // whether this writer holds unfiltered_html
	}{
		// The capability-less callers. Anonymous is the zero-value Writer; the
		// four lower roles are authenticated but hold no unfiltered_html.
		{"anonymous", Anonymous(), false},
		{"subscriber", For(auth.NewPrincipal(1, "sub", []string{auth.RoleSubscriber})), false},
		{"contributor", For(auth.NewPrincipal(2, "contrib", []string{auth.RoleContributor})), false},
		{"author", For(auth.NewPrincipal(3, "auth", []string{auth.RoleAuthor})), false},

		// The capability-holding callers: the two default roles that carry
		// unfiltered_html, plus a custom role granted it explicitly (Req 2.4).
		{"editor", For(auth.NewPrincipal(4, "ed", []string{auth.RoleEditor})), true},
		{"administrator", For(auth.NewPrincipal(5, "admin", []string{auth.RoleAdministrator})), true},
		{"custom-role-with-unfiltered", For(auth.NewPrincipal(6, "custom", []string{"gardener", CapUnfilteredHTML})), true},
	}

	for _, r := range rows {
		// Precondition cross-check: whether the real capability table agrees
		// with wantUnfiltered. For Anonymous there is no principal, so this is
		// skipped; the zero-value Writer holds no capability by construction.
		if r.name != "anonymous" {
			if got := r.writer.unfiltered; got != r.wantUnfiltered {
				t.Fatalf("%s: writer.unfiltered = %v, want %v (roles.go drift?)", r.name, got, r.wantUnfiltered)
			}
		}

		for _, kind := range allKinds {
			want := expected(kind, r.wantUnfiltered)
			if got := TierFor(kind, r.writer); got != want {
				t.Errorf("TierFor(kind=%v, writer=%s) = %v, want %v", kind, r.name, got, want)
			}
		}
	}
}

// TestMatrixCommentingLowerRolesSelectTierA pins, as a standalone assertion so a
// regression names it directly, that subscriber, contributor and author
// commenting all select TierA (Req 2.5, 9.3). This is the matrix cell most
// likely to be reintroduced wrong.
func TestMatrixCommentingLowerRolesSelectTierA(t *testing.T) {
	for _, role := range []string{auth.RoleSubscriber, auth.RoleContributor, auth.RoleAuthor} {
		w := For(auth.NewPrincipal(1, "u", []string{role}))
		if got := TierFor(CommentContent, w); got != TierA {
			t.Errorf("TierFor(CommentContent, For(%s)) = %v, want TierA", role, got)
		}
	}
}

// TestMatrixAnonymousAndBareAuthenticatedSelectSameTier pins that Anonymous()
// and For(auth.Principal{}) select the same tier for every field kind while
// remaining distinguishable through Authenticated() (Req 2.4, 2.5, 9.3).
// Authentication status carries information for the call site but is not a tier
// input.
func TestMatrixAnonymousAndBareAuthenticatedSelectSameTier(t *testing.T) {
	anon := Anonymous()
	bare := For(auth.Principal{})

	if anon.Authenticated() {
		t.Error("Anonymous().Authenticated() = true, want false")
	}
	if !bare.Authenticated() {
		t.Error("For(auth.Principal{}).Authenticated() = false, want true")
	}

	for _, kind := range []FieldKind{PostContent, PostExcerpt, PostTitle, CommentContent} {
		if got, want := TierFor(kind, bare), TierFor(kind, anon); got != want {
			t.Errorf("TierFor(%v): bare-authenticated=%v, anonymous=%v; the two must select the same tier", kind, got, want)
		}
	}
}
