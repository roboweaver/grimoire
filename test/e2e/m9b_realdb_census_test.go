package e2e_test

import (
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// These tests boot nothing and need no database. They exercise the decision
// TestM9bNestedCategoriesRealDBE2E makes *about* a database: given a taxonomy,
// does a nested archive actually get driven over HTTP, or does Req 12.7's skip
// fire?
//
// That decision is the part of an env-gated test nobody can read off a green run.
// Without GRIMOIRE_TEST_WP_DSN the whole test skips at its first line; with one it
// skips or passes for reasons buried in the target's rows. Driving the census and
// the candidate selection over synthetic taxonomies is the only way to show that
// an all-top-level site reaches the skip instead of reporting success for a claim
// it never touched.
//
// The specific defect the first case below would catch: `checked` counts
// categories *driven over HTTP*, so it must be incremented after the
// `len(ancestry) < 2` filter. Incremented before it, an all-top-level site with
// five or more categories would reach realWPCheckCategories, break out of the
// loop with `checked == 5`, and make the Req 12.7 skip unreachable — a green run
// asserting nothing about nesting, which is the exact outcome Req 12.7 exists to
// prevent.

// realWPNestedCandidates drives the loop TestM9bNestedCategoriesRealDBE2E runs,
// minus the HTTP: honour the realWPCheckCategories cap, classify the term with
// the same realWPNestedCandidate that test uses, drop it when a slug in its
// ancestry cannot sit in a path, drop it again when it is not genuinely nested,
// and only then count it. It returns the canonical/flat pairs it would have
// requested, so the derivation is visible to the assertions rather than only its
// count.
//
// The classification is called rather than reproduced, so this cannot drift from
// what the real-database test does.
func realWPNestedCandidates(terms []domain.Term) (checked int, canonical, flat []string) {
	byID := make(map[int64]domain.Term, len(terms))
	for _, term := range terms {
		byID[term.ID] = term
	}
	for _, term := range terms {
		if checked == realWPCheckCategories {
			break
		}
		ancestry, nested, pathSafe := realWPNestedCandidate(byID, term)
		if !pathSafe {
			continue
		}
		if !nested {
			continue
		}
		checked++
		canonical = append(canonical,
			realWPCategoryPath(nil, routing.DefaultCategoryBase, ancestry, false))
		flat = append(flat,
			realWPCategoryPath(nil, routing.DefaultCategoryBase, ancestry[len(ancestry)-1:], false))
	}
	return checked, canonical, flat
}

func TestRealWPNestedCensusAllTopLevelSkips(t *testing.T) {
	// Deliberately more terms than realWPCheckCategories: a counter incremented
	// before the nesting filter would stop at the cap and never reach the skip.
	terms := make([]domain.Term, 0, 8)
	for i, slug := range []string{"news", "sport", "weather", "travel", "food", "music", "film", "books"} {
		terms = append(terms, domain.Term{
			ID: int64(i + 1), Name: strings.ToUpper(slug), Slug: slug, Taxonomy: "category",
		})
	}
	if len(terms) <= realWPCheckCategories {
		t.Fatalf("this case needs more than %d terms to be meaningful", realWPCheckCategories)
	}

	census := realWPNestedCensusOf(terms)
	if census.Terms != len(terms) || census.Nested != 0 || census.Usable != 0 {
		t.Fatalf("census = %+v, want {Terms:%d Nested:0 Usable:0}", census, len(terms))
	}

	checked, canonical, flat := realWPNestedCandidates(terms)
	if checked != 0 {
		t.Fatalf("checked = %d, want 0 — nothing here is nested, so nothing can be "+
			"driven over HTTP and the Req 12.7 skip must be reachable", checked)
	}
	if len(canonical) != 0 || len(flat) != 0 {
		t.Errorf("derived paths = %v / %v, want none", canonical, flat)
	}
	if checked != census.Usable {
		t.Errorf("loop counted %d, census counted %d usable", checked, census.Usable)
	}

	msg := census.precondition()
	for _, substr := range []string{"parent <> 0", "all top-level", "8 category terms"} {
		if !strings.Contains(msg, substr) {
			t.Errorf("precondition() = %q, want it to mention %q", msg, substr)
		}
	}
}

func TestRealWPNestedCensusOrphanParentIsTopLevel(t *testing.T) {
	// Req 1.4: a parent absent from the taxonomy is no parent, so an imported
	// database of orphans reaches the skip rather than reporting success.
	terms := []domain.Term{
		{ID: 2, Name: "Local", Slug: "local", Taxonomy: "category", ParentID: 999},
	}

	census := realWPNestedCensusOf(terms)
	if census.Nested != 0 || census.Usable != 0 {
		t.Fatalf("census = %+v, want Nested and Usable 0 for an orphaned parent", census)
	}
	if checked, _, _ := realWPNestedCandidates(terms); checked != 0 {
		t.Errorf("checked = %d, want 0", checked)
	}
	if !strings.Contains(census.precondition(), "all top-level") {
		t.Errorf("precondition() = %q, want the no-nested-category wording", census.precondition())
	}
}

func TestRealWPNestedCensusNestedButUnusableNamesItsOwnPrecondition(t *testing.T) {
	// The case the first wording got wrong: this site IS nested, so telling an
	// operator it is "all top-level" sends them to fix something that is not
	// broken. One term is rejected for a percent-encoded slug, one because its
	// chain closes a cycle (Req 1.5).
	terms := []domain.Term{
		{ID: 1, Name: "Nyheter", Slug: "nyheter", Taxonomy: "category"},
		{ID: 2, Name: "Lokalt", Slug: "l%C3%B6kalt", Taxonomy: "category", ParentID: 1},
		{ID: 3, Name: "Loop", Slug: "loop", Taxonomy: "category", ParentID: 3},
	}

	census := realWPNestedCensusOf(terms)
	if census.Terms != 3 || census.Nested != 2 || census.Usable != 0 {
		t.Fatalf("census = %+v, want {Terms:3 Nested:2 Usable:0}", census)
	}
	if checked, _, _ := realWPNestedCandidates(terms); checked != 0 {
		t.Errorf("checked = %d, want 0", checked)
	}

	msg := census.precondition()
	if strings.Contains(msg, "all top-level") {
		t.Errorf("precondition() = %q, but this taxonomy IS nested — the message "+
			"names the wrong precondition", msg)
	}
	for _, substr := range []string{"2 of 3", "plain URL token", "cycle"} {
		if !strings.Contains(msg, substr) {
			t.Errorf("precondition() = %q, want it to mention %q", msg, substr)
		}
	}
}

func TestRealWPNestedCensusNestedChainIsDriven(t *testing.T) {
	// The other direction: a usable chain must be counted and its canonical/flat
	// pair derived, so `checked == 0` is false and the skip does not swallow a
	// run that had work to do.
	terms := []domain.Term{
		{ID: 1, Name: "News", Slug: "news", Taxonomy: "category"},
		{ID: 2, Name: "Local", Slug: "local", Taxonomy: "category", ParentID: 1},
		{ID: 3, Name: "Council", Slug: "council", Taxonomy: "category", ParentID: 2},
	}

	census := realWPNestedCensusOf(terms)
	if census.Terms != 3 || census.Nested != 2 || census.Usable != 2 {
		t.Fatalf("census = %+v, want {Terms:3 Nested:2 Usable:2}", census)
	}

	checked, canonical, flat := realWPNestedCandidates(terms)
	if checked != census.Usable {
		t.Errorf("loop counted %d, census counted %d usable", checked, census.Usable)
	}
	wantCanonical := []string{"/category/news/local", "/category/news/local/council"}
	if strings.Join(canonical, " ") != strings.Join(wantCanonical, " ") {
		t.Errorf("canonical paths = %v, want %v", canonical, wantCanonical)
	}
	// The flat path is the zero-ancestor form the site published pre-M9b, and it
	// must differ from the canonical one or the 301 assertion would be vacuous.
	wantFlat := []string{"/category/local", "/category/council"}
	if strings.Join(flat, " ") != strings.Join(wantFlat, " ") {
		t.Errorf("flat paths = %v, want %v", flat, wantFlat)
	}
}
