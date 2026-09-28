package storagetest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Empty-archive fixture identifiers, exported so assertions can name known rows
// instead of re-deriving them from the read under test.
//
// The ids continue the bands SeedNestedCategories and SeedArchiveExtras opened
// (terms 50-52, term_taxonomy 60-62, posts 401-407, user 2) rather than reusing
// them, so TestSeedNestedCategoriesIsAdditive keeps holding for a database
// carrying only the nested chain.
const (
	// EmptyCategoryTermID is a category with **no** posts at all, nested under
	// NestedParentTermID so one row exercises both "existing but empty" and
	// "canonical nested path" at once. Every other category in every seed has
	// at least one published post, so nothing else can distinguish the
	// empty-archive 200 (Req 2.6) from a 404.
	EmptyCategoryTermID         = 53
	EmptyCategoryTermTaxonomyID = 63
	EmptyCategoryTermSlug       = "idle"
	EmptyCategoryTermName       = "Idle"

	// EmptyTagTermID is a post_tag term with no relationships, which is what
	// makes the empty tag archive's 200 (Req 5.4) distinguishable from the
	// unknown tag's 404 (Req 5.3). SeedFixtures' only tag ("golang") carries a
	// published post.
	EmptyTagTermID         = 54
	EmptyTagTermTaxonomyID = 64
	EmptyTagTermSlug       = "unused"
	EmptyTagTermName       = "Unused"

	// PostlessAuthorID owns no posts of any status, so the author archive's
	// empty-but-200 rule (Req 7.3) is asserted against a user who exists rather
	// than against an absent nicename. Both other seeded users author posts.
	PostlessAuthorID       = 3
	PostlessAuthorLogin    = "ghost"
	PostlessAuthorNicename = "ghost"
	PostlessAuthorName     = "Ghost"
)

// SeedEmptyArchiveTargets adds the three rows an archive's "exists but holds no
// published posts" case needs, which no other seed provides:
//
//	term 53 idle    category (parent Tech) -- no posts, at any status
//	term 54 unused  post_tag               -- no relationships
//	user 3  ghost                          -- authors nothing
//
// Each exists to make one status-code row falsifiable rather than accidentally
// true. An empty archive must render 200 and an absent one must 404, and those
// two outcomes are indistinguishable unless a category, a tag and an author
// exist that hold nothing: every category, tag and user the other three seeds
// create carries at least one published post, so a handler that 404ed an empty
// archive would pass against them.
//
// It is a fourth helper for the same reason SeedArchiveExtras is a third one:
// the assertions against the earlier sets are absolute. contract.go pins exactly
// 3 published posts in a fixed slug order and a user Count of 3 after creating
// 2, termreader_contract.go pins exactly 3 category terms, and
// TestSeedNestedCategoriesIsAdditive pins the highest term and term_taxonomy ids
// at 52 and 62. A fourth user and two more terms in any of those seeds would
// break them; layering them only under the handler tests that need them breaks
// nothing.
//
// Preconditions: SeedFixtures and SeedNestedCategories have both run. The empty
// category's parent is NestedParentTermID, so the nested chain has to exist; it
// creates no posts and no relationships of its own.
//
// Like every other seed it pins explicit primary keys, so it re-syncs the
// PostgreSQL identity sequences afterward.
func SeedEmptyArchiveTargets(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	termInsert := `INSERT INTO ` + prefix + `terms (term_id, name, slug) VALUES (?, ?, ?)`
	taxInsert := `INSERT INTO ` + prefix +
		`term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`
	userInsert := `INSERT INTO ` + prefix + `users (` + rebind.Ident(vendor, "ID") +
		`, user_login, user_nicename, display_name) VALUES (?, ?, ?, ?)`

	stmts := []struct {
		q    string
		args []any
	}{
		{termInsert, []any{EmptyCategoryTermID, EmptyCategoryTermName, EmptyCategoryTermSlug}},
		{taxInsert, []any{EmptyCategoryTermTaxonomyID, EmptyCategoryTermID, "category", "",
			NestedParentTermID, 0}},

		{termInsert, []any{EmptyTagTermID, EmptyTagTermName, EmptyTagTermSlug}},
		{taxInsert, []any{EmptyTagTermTaxonomyID, EmptyTagTermID, "post_tag", "", 0, 0}},

		{userInsert, []any{PostlessAuthorID, PostlessAuthorLogin, PostlessAuthorNicename,
			PostlessAuthorName}},
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, s.q), s.args...); err != nil {
			return fmt.Errorf("seed empty archive targets %q: %w", s.q, err)
		}
	}
	return migrate.SyncIdentitySequences(ctx, db, vendor, prefix)
}
