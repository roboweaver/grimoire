package storagetest

import (
	"context"
	"testing"
)

// runTermReaderContract covers TermReader.ListByTaxonomy (name-ascending
// order, taxonomy isolation) and TermReader.TermsByIDs (bulk resolve,
// unknown IDs silently omitted per the same convention already established
// by PostTermsRepository.TermsForPost, empty input yielding empty output).
func runTermReaderContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("ListByTaxonomy returns terms name-ascending and isolated by taxonomy", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		cats, err := repos.TermReader.ListByTaxonomy(ctx, "category")
		if err != nil {
			t.Fatalf("ListByTaxonomy(category): %v", err)
		}
		if len(cats) != 3 {
			t.Fatalf("ListByTaxonomy(category) len = %d, want 3 (News, Zeta, Alpha)", len(cats))
		}
		wantNames := []string{"Alpha", "News", "Zeta"}
		for i, want := range wantNames {
			if cats[i].Name != want {
				t.Errorf("ListByTaxonomy(category)[%d].Name = %q, want %q", i, cats[i].Name, want)
			}
		}

		tags, err := repos.TermReader.ListByTaxonomy(ctx, "post_tag")
		if err != nil {
			t.Fatalf("ListByTaxonomy(post_tag): %v", err)
		}
		if len(tags) != 1 || tags[0].Name != "Golang" {
			t.Errorf("ListByTaxonomy(post_tag) = %+v, want [Golang]", tags)
		}

		menus, err := repos.TermReader.ListByTaxonomy(ctx, "nav_menu")
		if err != nil {
			t.Fatalf("ListByTaxonomy(nav_menu): %v", err)
		}
		if len(menus) != 1 || menus[0].Name != "Primary" {
			t.Errorf("ListByTaxonomy(nav_menu) = %+v, want [Primary]", menus)
		}

		none, err := repos.TermReader.ListByTaxonomy(ctx, "no_such_taxonomy")
		if err != nil {
			t.Fatalf("ListByTaxonomy(no_such_taxonomy): %v", err)
		}
		if len(none) != 0 {
			t.Errorf("ListByTaxonomy(no_such_taxonomy) = %+v, want empty", none)
		}
	})

	t.Run("TermsByIDs bulk-resolves terms and silently omits unknown IDs", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		terms, err := repos.TermReader.TermsByIDs(ctx, []int64{10, 999999, 12})
		if err != nil {
			t.Fatalf("TermsByIDs: %v", err)
		}
		if len(terms) != 2 {
			t.Fatalf("TermsByIDs len = %d, want 2 (999999 silently omitted)", len(terms))
		}
		byID := map[int64]string{}
		for _, term := range terms {
			byID[term.ID] = term.Name
		}
		if byID[10] != "News" || byID[12] != "Alpha" {
			t.Errorf("TermsByIDs resolved = %+v, want {10:News 12:Alpha}", byID)
		}

		empty, err := repos.TermReader.TermsByIDs(ctx, nil)
		if err != nil {
			t.Fatalf("TermsByIDs(nil): %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("TermsByIDs(nil) = %+v, want empty", empty)
		}

		allUnknown, err := repos.TermReader.TermsByIDs(ctx, []int64{888888, 999999})
		if err != nil {
			t.Fatalf("TermsByIDs(all unknown): %v", err)
		}
		if len(allUnknown) != 0 {
			t.Errorf("TermsByIDs(all unknown) = %+v, want empty", allUnknown)
		}
	})
}
