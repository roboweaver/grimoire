package storagetest

import (
	"context"
	"testing"
)

// RunTermParentContract covers Term.ParentID on every read that returns a
// domain.Term: TermRepository.BySlug, TermReader.ListByTaxonomy and
// TermReader.TermsByIDs (Req 1.2), plus the agreement guarantee between the two
// taxonomy-scoped reads.
//
// newNestedRepos MUST build a backend carrying SeedNestedCategories on top of
// SeedFixtures. Every term_taxonomy row SeedFixtures inserts has parent 0, so
// asserting ParentID against that set alone would also pass for a repository
// that hard-coded 0 — the seeded chain (Tech -> Go -> Generics) is what gives
// the assertion teeth. It is a separate backend rather than the one RunContract
// uses because several assertions there are absolute: exactly 3 published posts
// in a fixed slug order, exactly 3 category terms.
//
// The suite deliberately keeps a top-level term in the table on every read, so
// "reports 0 for no parent" is asserted rather than inferred from the chain.
func RunTermParentContract(t *testing.T, newNestedRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	// The expected parent of each seeded category, spanning both fixture sets:
	// the three-level chain and a SeedFixtures term that has no parent.
	wantParents := map[string]int64{
		NestedParentTermSlug:     0,
		NestedChildTermSlug:      NestedParentTermID,
		NestedGrandchildTermSlug: NestedChildTermID,
		"news":                   0,
		"zeta":                   0,
		"alpha":                  0,
	}

	t.Run("TermRepository BySlug populates ParentID", func(t *testing.T) {
		repos, cleanup := newNestedRepos(t)
		defer cleanup()

		for slug, want := range wantParents {
			term, err := repos.Terms.BySlug(ctx, "category", slug)
			if err != nil {
				t.Fatalf("BySlug(category, %q): %v", slug, err)
			}
			if term.ParentID != want {
				t.Errorf("BySlug(category, %q).ParentID = %d, want %d",
					slug, term.ParentID, want)
			}
		}
	})

	t.Run("TermReader ListByTaxonomy populates ParentID", func(t *testing.T) {
		repos, cleanup := newNestedRepos(t)
		defer cleanup()

		terms, err := repos.TermReader.ListByTaxonomy(ctx, "category")
		if err != nil {
			t.Fatalf("ListByTaxonomy(category): %v", err)
		}
		got := make(map[string]int64, len(terms))
		for _, term := range terms {
			got[term.Slug] = term.ParentID
		}
		for slug, want := range wantParents {
			parent, ok := got[slug]
			if !ok {
				t.Fatalf("ListByTaxonomy(category) missing %q, got %+v", slug, got)
			}
			if parent != want {
				t.Errorf("ListByTaxonomy(category)[%q].ParentID = %d, want %d",
					slug, parent, want)
			}
		}
	})

	t.Run("TermReader TermsByIDs populates ParentID", func(t *testing.T) {
		repos, cleanup := newNestedRepos(t)
		defer cleanup()

		ids := []int64{NestedParentTermID, NestedChildTermID, NestedGrandchildTermID}
		terms, err := repos.TermReader.TermsByIDs(ctx, ids)
		if err != nil {
			t.Fatalf("TermsByIDs: %v", err)
		}
		if len(terms) != len(ids) {
			t.Fatalf("TermsByIDs len = %d, want %d", len(terms), len(ids))
		}
		got := make(map[int64]int64, len(terms))
		for _, term := range terms {
			got[term.ID] = term.ParentID
		}
		want := map[int64]int64{
			NestedParentTermID:     0,
			NestedChildTermID:      NestedParentTermID,
			NestedGrandchildTermID: NestedChildTermID,
		}
		for id, wantParent := range want {
			if got[id] != wantParent {
				t.Errorf("TermsByIDs term %d ParentID = %d, want %d",
					id, got[id], wantParent)
			}
		}
	})

	t.Run("BySlug and ListByTaxonomy agree about ParentID", func(t *testing.T) {
		repos, cleanup := newNestedRepos(t)
		defer cleanup()

		listed, err := repos.TermReader.ListByTaxonomy(ctx, "category")
		if err != nil {
			t.Fatalf("ListByTaxonomy(category): %v", err)
		}
		for _, term := range listed {
			single, err := repos.Terms.BySlug(ctx, "category", term.Slug)
			if err != nil {
				t.Fatalf("BySlug(category, %q): %v", term.Slug, err)
			}
			if single.ParentID != term.ParentID {
				t.Errorf("term %q: BySlug ParentID = %d, ListByTaxonomy ParentID = %d — the two taxonomy-scoped reads must not disagree",
					term.Slug, single.ParentID, term.ParentID)
			}
		}
	})
}
