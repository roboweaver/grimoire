package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
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

// runPostTermsWriterContract covers PostTermsWriter.SetPostTerms: assignment,
// reassignment, clearing, taxonomy isolation on the same post, cross-post
// isolation (assigning a term already used by another post must not disturb
// that other post's relationships), and error handling for a nonexistent
// postID or a termID with no term_taxonomy row for the requested taxonomy
// (tasks.md 1.3: both must return a clear error, not a silent no-op).
func runPostTermsWriterContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("SetPostTerms assigns then reassigns a taxonomy for a post with no prior terms", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		// Post 5 ("about") has no term relationships in the fixture.
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", []int64{10, 12}); err != nil {
			t.Fatalf("SetPostTerms assign: %v", err)
		}
		ids, err := repos.PostTerms.TermsForPost(ctx, 5, "category")
		if err != nil {
			t.Fatalf("TermsForPost after assign: %v", err)
		}
		if len(ids) != 2 || ids[0] != 12 || ids[1] != 10 {
			t.Errorf("TermsForPost(5, category) = %v, want [12 10] (name-ascending: Alpha, News)", ids)
		}

		// Reassign: drop 10 (News), keep 12 (Alpha).
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", []int64{12}); err != nil {
			t.Fatalf("SetPostTerms reassign: %v", err)
		}
		ids, err = repos.PostTerms.TermsForPost(ctx, 5, "category")
		if err != nil {
			t.Fatalf("TermsForPost after reassign: %v", err)
		}
		if len(ids) != 1 || ids[0] != 12 {
			t.Errorf("TermsForPost(5, category) after reassign = %v, want [12]", ids)
		}

		// Clear entirely.
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", nil); err != nil {
			t.Fatalf("SetPostTerms clear: %v", err)
		}
		ids, err = repos.PostTerms.TermsForPost(ctx, 5, "category")
		if err != nil {
			t.Fatalf("TermsForPost after clear: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("TermsForPost(5, category) after clear = %v, want empty", ids)
		}
	})

	t.Run("SetPostTerms isolates taxonomies on the same post", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", []int64{12}); err != nil {
			t.Fatalf("SetPostTerms category: %v", err)
		}
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "post_tag", []int64{13}); err != nil {
			t.Fatalf("SetPostTerms post_tag: %v", err)
		}
		cats, err := repos.PostTerms.TermsForPost(ctx, 5, "category")
		if err != nil {
			t.Fatalf("TermsForPost category: %v", err)
		}
		if len(cats) != 1 || cats[0] != 12 {
			t.Errorf("category relations disturbed by post_tag assignment: %v", cats)
		}
		tags, err := repos.PostTerms.TermsForPost(ctx, 5, "post_tag")
		if err != nil {
			t.Fatalf("TermsForPost post_tag: %v", err)
		}
		if len(tags) != 1 || tags[0] != 13 {
			t.Errorf("TermsForPost(5, post_tag) = %v, want [13]", tags)
		}

		// Clearing post_tag must not disturb category.
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "post_tag", nil); err != nil {
			t.Fatalf("SetPostTerms clear post_tag: %v", err)
		}
		cats, err = repos.PostTerms.TermsForPost(ctx, 5, "category")
		if err != nil {
			t.Fatalf("TermsForPost category after clearing post_tag: %v", err)
		}
		if len(cats) != 1 || cats[0] != 12 {
			t.Errorf("category relations disturbed by clearing post_tag: %v", cats)
		}
	})

	t.Run("SetPostTerms isolates posts sharing a term", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		// hello-1 (post 1) is already related to category term 12 (Alpha) in
		// the fixture. Assigning the same term to post 5 must not disturb
		// post 1's relationship, and clearing post 5's later must not
		// disturb post 1's either.
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", []int64{12}); err != nil {
			t.Fatalf("SetPostTerms post5: %v", err)
		}
		post1Terms, err := repos.PostTerms.TermsForPost(ctx, 1, "category")
		if err != nil {
			t.Fatalf("TermsForPost post1: %v", err)
		}
		if len(post1Terms) != 2 || post1Terms[0] != 12 || post1Terms[1] != 11 {
			t.Errorf("post 1's category relations disturbed by post 5's assignment: %v", post1Terms)
		}

		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", nil); err != nil {
			t.Fatalf("SetPostTerms clear post5: %v", err)
		}
		post1Terms, err = repos.PostTerms.TermsForPost(ctx, 1, "category")
		if err != nil {
			t.Fatalf("TermsForPost post1 after clearing post5: %v", err)
		}
		if len(post1Terms) != 2 || post1Terms[0] != 12 || post1Terms[1] != 11 {
			t.Errorf("post 1's category relations disturbed by clearing post 5: %v", post1Terms)
		}
	})

	t.Run("SetPostTerms rejects a termID with no term_taxonomy row for the taxonomy", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		// Term 13 (Golang) exists but only as post_tag, not category; term
		// 999999 does not exist at all. Either must return a clear error
		// (tasks.md 1.3), and the post's existing category relations must be
		// left untouched (validation runs before any mutation).
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", []int64{12}); err != nil {
			t.Fatalf("SetPostTerms seed: %v", err)
		}
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", []int64{12, 13}); err == nil {
			t.Fatalf("SetPostTerms with termID valid for a different taxonomy: want error, got nil")
		} else if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("SetPostTerms with termID valid for a different taxonomy: err = %v, want wraps domain.ErrNotFound", err)
		}
		if err := repos.PostTermsWriter.SetPostTerms(ctx, 5, "category", []int64{12, 999999}); err == nil {
			t.Fatalf("SetPostTerms with nonexistent termID: want error, got nil")
		} else if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("SetPostTerms with nonexistent termID: err = %v, want wraps domain.ErrNotFound", err)
		}

		// The rejected calls must not have mutated anything.
		ids, err := repos.PostTerms.TermsForPost(ctx, 5, "category")
		if err != nil {
			t.Fatalf("TermsForPost: %v", err)
		}
		if len(ids) != 1 || ids[0] != 12 {
			t.Errorf("TermsForPost(5, category) = %v, want [12] unchanged (rejected calls must not mutate)", ids)
		}
	})

	t.Run("SetPostTerms rejects a nonexistent postID", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		err := repos.PostTermsWriter.SetPostTerms(ctx, 999999, "category", []int64{12})
		if err == nil {
			t.Fatalf("SetPostTerms with nonexistent postID: want error, got nil")
		}
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("SetPostTerms with nonexistent postID: err = %v, want wraps domain.ErrNotFound", err)
		}
	})
}
