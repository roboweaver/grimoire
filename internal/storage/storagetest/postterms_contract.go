package storagetest

import (
	"context"
	"testing"
)

// runPostTermsContract covers PostTermsRepository.TermsForPost: empty result
// for a post with no terms of the given taxonomy, taxonomy isolation, a
// nonexistent post ID returning an empty slice rather than an error, and
// name-ascending ordering (not insertion order).
func runPostTermsContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("TermsForPost returns category terms name-ascending, not insertion order", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		// hello-1 (post 1) is related to Zeta then Alpha (insertion order),
		// both category terms 11 and 12 respectively; name-ascending order
		// must return Alpha (12) before Zeta (11).
		ids, err := repos.PostTerms.TermsForPost(ctx, 1, "category")
		if err != nil {
			t.Fatalf("TermsForPost: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("want 2 category terms, got %d (%v)", len(ids), ids)
		}
		if ids[0] != 12 || ids[1] != 11 {
			t.Errorf("TermsForPost(1, category) = %v, want [12 11] (name-ascending: Alpha, Zeta)", ids)
		}
	})

	t.Run("TermsForPost isolates taxonomy", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		tags, err := repos.PostTerms.TermsForPost(ctx, 1, "post_tag")
		if err != nil {
			t.Fatalf("TermsForPost post_tag: %v", err)
		}
		if len(tags) != 1 || tags[0] != 13 {
			t.Errorf("TermsForPost(1, post_tag) = %v, want [13] (Golang)", tags)
		}
	})

	t.Run("TermsForPost empty for a post with no terms of the taxonomy", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		ids, err := repos.PostTerms.TermsForPost(ctx, 1, "nav_menu")
		if err != nil {
			t.Fatalf("TermsForPost nav_menu: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("TermsForPost(1, nav_menu) = %v, want empty", ids)
		}
	})

	t.Run("TermsForPost nonexistent post returns empty slice, not an error", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		ids, err := repos.PostTerms.TermsForPost(ctx, 999999, "category")
		if err != nil {
			t.Fatalf("TermsForPost nonexistent post: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("TermsForPost(999999, category) = %v, want empty", ids)
		}
	})
}
