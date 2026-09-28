package routing_test

import (
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// These tests carry no database. They exercise the decision
// TestRealWordPressNestedCategories makes *about* a database: given a taxonomy,
// does the nested-category claim get exercised, or does Req 12.7's skip fire?
//
// That decision is the part of an env-gated test nobody can read off a green
// run, because on a machine without GRIMOIRE_TEST_WP_DSN the whole test skips at
// its first line and on a machine with one it skips or passes for reasons buried
// in the target's rows. Driving the census and the walk over synthetic taxonomies
// is the only way to show that an all-top-level site reaches the skip rather than
// reporting success for a claim it never touched.
//
// realWPCensus and the loop in TestRealWordPressNestedCategories agree by
// cross-check rather than by construction: the census counts nesting from
// term_taxonomy.parent, while the loop counts it from the length of the walked
// ancestry. Each case below asserts both arrive at the same number, so a change
// to either one that drifts from the other fails here.

// realWPNestedSelection drives the loop TestRealWordPressNestedCategories runs,
// minus the assertions: per term, classify it with the same
// realWPNestedCandidate that test uses, drop it if a slug in its ancestry cannot
// sit in a path, and sort the derived path into the nested or the flat bucket.
// It returns the derived paths so the derivation itself is visible to the
// assertions, not only its count.
//
// The classification is called rather than reproduced, so this cannot drift from
// what the real-database test does.
func realWPNestedSelection(terms []domain.Term, s routing.Structure) (checked int, nestedPaths, flatPaths []string) {
	byID := make(map[int64]domain.Term, len(terms))
	for _, term := range terms {
		byID[term.ID] = term
	}
	for _, term := range terms {
		ancestry, nested, pathSafe := realWPNestedCandidate(byID, term)
		if !pathSafe {
			continue
		}
		checked++
		path := realWPDerivedCategoryPath(nil, routing.DefaultCategoryBase, ancestry, false)
		if got := s.CategoryPath(ancestry); got != path {
			// Reported by the caller: keeping the derivation honest is the whole
			// point of this helper, so a disagreement must not be swallowed.
			nestedPaths = append(nestedPaths, "MISMATCH:"+got+"!="+path)
			continue
		}
		if nested {
			nestedPaths = append(nestedPaths, path)
		} else {
			flatPaths = append(flatPaths, path)
		}
	}
	return checked, nestedPaths, flatPaths
}

func TestRealWPCensusAllTopLevelSkips(t *testing.T) {
	// A whole taxonomy with no parent anywhere: the shape Req 12.7 is about.
	terms := []domain.Term{
		{ID: 1, Name: "News", Slug: "news", Taxonomy: "category"},
		{ID: 2, Name: "Sport", Slug: "sport", Taxonomy: "category"},
		{ID: 3, Name: "Weather", Slug: "weather", Taxonomy: "category"},
	}

	census := realWPCensus(terms)
	if census.Terms != 3 || census.Nested != 0 || census.Usable != 0 {
		t.Fatalf("census = %+v, want {Terms:3 Nested:0 Usable:0}", census)
	}

	s, err := routing.Parse("", "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checked, nestedPaths, flatPaths := realWPNestedSelection(terms, s)
	if checked != 3 {
		t.Errorf("checked = %d, want 3 — every slug here is a plain URL token", checked)
	}
	// The decision under test: the loop ran three subtests and not one of them
	// said anything about nesting, so the skip has to fire.
	if len(nestedPaths) != 0 {
		t.Errorf("nested paths = %v, want none", nestedPaths)
	}
	if len(nestedPaths) != census.Usable {
		t.Errorf("loop counted %d nested, census counted %d", len(nestedPaths), census.Usable)
	}
	want := []string{"/category/news", "/category/sport", "/category/weather"}
	if strings.Join(flatPaths, " ") != strings.Join(want, " ") {
		t.Errorf("flat paths = %v, want %v", flatPaths, want)
	}

	msg := census.precondition()
	for _, substr := range []string{"parent <> 0", "all top-level", "3 category terms"} {
		if !strings.Contains(msg, substr) {
			t.Errorf("precondition() = %q, want it to mention %q", msg, substr)
		}
	}
}

func TestRealWPCensusOrphanParentIsTopLevel(t *testing.T) {
	// Req 1.4: a parent absent from the taxonomy is no parent. An imported
	// database full of orphans must reach the skip, not report success — the
	// derived path is the flat one either way.
	terms := []domain.Term{
		{ID: 2, Name: "Local", Slug: "local", Taxonomy: "category", ParentID: 999},
	}

	census := realWPCensus(terms)
	if census.Nested != 0 || census.Usable != 0 {
		t.Fatalf("census = %+v, want Nested and Usable 0 for an orphaned parent", census)
	}
	if !strings.Contains(census.precondition(), "all top-level") {
		t.Errorf("precondition() = %q, want the no-nested-category wording", census.precondition())
	}

	s, err := routing.Parse("", "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, nestedPaths, flatPaths := realWPNestedSelection(terms, s)
	if len(nestedPaths) != 0 {
		t.Errorf("nested paths = %v, want none", nestedPaths)
	}
	if len(flatPaths) != 1 || flatPaths[0] != "/category/local" {
		t.Errorf("flat paths = %v, want [/category/local]", flatPaths)
	}
}

func TestRealWPCensusNestedButUnusableNamesItsOwnPrecondition(t *testing.T) {
	// The case the first wording got wrong: this site IS nested, so telling an
	// operator it is "all top-level" sends them to fix something that is not
	// broken. Both nested terms are rejected by the walk — one for a
	// percent-encoded slug, one because its chain closes a cycle (Req 1.5).
	terms := []domain.Term{
		{ID: 1, Name: "Nyheter", Slug: "nyheter", Taxonomy: "category"},
		{ID: 2, Name: "Lokalt", Slug: "l%C3%B6kalt", Taxonomy: "category", ParentID: 1},
		{ID: 3, Name: "Loop", Slug: "loop", Taxonomy: "category", ParentID: 3},
	}

	census := realWPCensus(terms)
	if census.Terms != 3 || census.Nested != 2 || census.Usable != 0 {
		t.Fatalf("census = %+v, want {Terms:3 Nested:2 Usable:0}", census)
	}

	s, err := routing.Parse("", "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, nestedPaths, _ := realWPNestedSelection(terms, s)
	if len(nestedPaths) != 0 {
		t.Errorf("nested paths = %v, want none", nestedPaths)
	}
	if len(nestedPaths) != census.Usable {
		t.Errorf("loop counted %d nested, census counted %d", len(nestedPaths), census.Usable)
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

func TestRealWPCensusNestedChainDoesNotSkip(t *testing.T) {
	// The other direction, which is what stops the skip from swallowing a real
	// run: a usable chain must be counted, so `nested == 0` is false and the
	// assertions proceed.
	terms := []domain.Term{
		{ID: 1, Name: "News", Slug: "news", Taxonomy: "category"},
		{ID: 2, Name: "Local", Slug: "local", Taxonomy: "category", ParentID: 1},
		{ID: 3, Name: "Council", Slug: "council", Taxonomy: "category", ParentID: 2},
	}

	census := realWPCensus(terms)
	if census.Terms != 3 || census.Nested != 2 || census.Usable != 2 {
		t.Fatalf("census = %+v, want {Terms:3 Nested:2 Usable:2}", census)
	}

	s, err := routing.Parse("", "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checked, nestedPaths, flatPaths := realWPNestedSelection(terms, s)
	if checked != 3 {
		t.Errorf("checked = %d, want 3", checked)
	}
	if len(nestedPaths) != census.Usable {
		t.Errorf("loop counted %d nested, census counted %d", len(nestedPaths), census.Usable)
	}
	want := []string{"/category/news/local", "/category/news/local/council"}
	if strings.Join(nestedPaths, " ") != strings.Join(want, " ") {
		t.Errorf("nested paths = %v, want %v", nestedPaths, want)
	}
	if len(flatPaths) != 1 || flatPaths[0] != "/category/news" {
		t.Errorf("flat paths = %v, want [/category/news]", flatPaths)
	}
}
