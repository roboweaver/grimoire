package storagetest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Nested category fixture identifiers, exported so assertions can refer to
// known rows without re-deriving them from a read under test.
//
// The ids sit in their own band, clear of everything SeedFixtures pins (posts
// reach 304, terms reach 30, term_taxonomy reaches 40), so the two fixture sets
// coexist in one database without either having to know the other's numbering.
const (
	// NestedParentTermID is the top-level category: no parent, so its
	// term_taxonomy.parent is 0.
	NestedParentTermID = 50
	// NestedChildTermID is the middle category; its parent is
	// NestedParentTermID.
	NestedChildTermID = 51
	// NestedGrandchildTermID is the deepest category; its parent is
	// NestedChildTermID. Three levels is the shallowest chain that can catch a
	// walk which handles one hop and stops.
	NestedGrandchildTermID = 52

	NestedParentTermSlug     = "tech"
	NestedChildTermSlug      = "go"
	NestedGrandchildTermSlug = "generics"

	NestedParentTermName     = "Tech"
	NestedChildTermName      = "Go"
	NestedGrandchildTermName = "Generics"

	// One post per level, so a descendant-inclusive read of the parent can be
	// distinguished from a read that returns only the parent's own post.
	NestedParentPostID     = 401
	NestedChildPostID      = 402
	NestedGrandchildPostID = 403

	NestedParentPostSlug     = "tech-post"
	NestedChildPostSlug      = "go-post"
	NestedGrandchildPostSlug = "generics-post"

	// term_taxonomy_ids are the term ids plus 10, keeping both bands distinct
	// from each other and from SeedFixtures'. They are exported because
	// {prefix}term_relationships joins on term_taxonomy_id rather than term_id,
	// so a sibling fixture set that relates a post to one of these categories
	// has to name the row rather than re-derive it.
	NestedParentTermTaxonomyID     = 60
	NestedChildTermTaxonomyID      = 61
	NestedGrandchildTermTaxonomyID = 62
)

// SeedNestedCategories seeds a three-level category chain and one published
// post per level, on top of an already-seeded SeedFixtures database:
//
//	Tech      (term 50, parent 0)             -> tech-post       2024-05-15
//	 └─ Go    (term 51, parent 50)            -> go-post         2024-05-16
//	     └─ Generics (term 52, parent 51)     -> generics-post   2024-05-17
//
// Every term_taxonomy row SeedFixtures inserts carries parent 0, so a ParentID
// assertion against that set alone would also pass for a repository that
// hard-coded 0. This chain is what makes the assertion mean something.
//
// It is a separate helper rather than an extension of SeedFixtures on purpose.
// Several assertions against the SeedFixtures set are absolute rather than
// relative -- contract.go asserts exactly 3 posts and their exact slug order,
// termreader_contract.go asserts exactly 3 category terms, and
// internal/web/permalinks_test.go and rest_terms_test.go derive expectations
// from that same fixed set. SeedFixtures is shared by the internal/web handler
// and REST tests too, so growing it would turn a storage phase into a
// cross-package test-update phase. Call this helper only from the cases that
// need a hierarchy; every other case keeps the fixture set it was written
// against.
//
// Preconditions: SeedFixtures has run (the posts are authored by user 1, which
// it seeds) and the schema is migrated. term_taxonomy.parent holds the parent's
// *term_id*, not its term_taxonomy_id, matching WordPress.
//
// Like SeedFixtures this pins explicit primary keys, so it re-syncs the
// PostgreSQL identity sequences afterward -- otherwise a writer running later
// in the same database would collide with these rows.
func SeedNestedCategories(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	termInsert := `INSERT INTO ` + prefix + `terms (term_id, name, slug) VALUES (?, ?, ?)`
	taxInsert := `INSERT INTO ` + prefix +
		`term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`
	relInsert := `INSERT INTO ` + prefix +
		`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`

	const (
		parentTaxID     = NestedParentTermTaxonomyID
		childTaxID      = NestedChildTermTaxonomyID
		grandchildTaxID = NestedGrandchildTermTaxonomyID
	)

	stmts := []struct {
		q    string
		args []any
	}{
		{termInsert, []any{NestedParentTermID, NestedParentTermName, NestedParentTermSlug}},
		{termInsert, []any{NestedChildTermID, NestedChildTermName, NestedChildTermSlug}},
		{termInsert, []any{NestedGrandchildTermID, NestedGrandchildTermName, NestedGrandchildTermSlug}},

		{taxInsert, []any{parentTaxID, NestedParentTermID, "category", "", 0, 1}},
		{taxInsert, []any{childTaxID, NestedChildTermID, "category", "", NestedParentTermID, 1}},
		{taxInsert, []any{grandchildTaxID, NestedGrandchildTermID, "category", "", NestedChildTermID, 1}},

		{postInsert(vendor, prefix), postArgs(NestedParentPostID, NestedParentPostSlug, "Tech Post",
			"post", "publish", "2024-05-15 00:00:00", "open", 0, "", 0)},
		{postInsert(vendor, prefix), postArgs(NestedChildPostID, NestedChildPostSlug, "Go Post",
			"post", "publish", "2024-05-16 00:00:00", "open", 0, "", 0)},
		{postInsert(vendor, prefix), postArgs(NestedGrandchildPostID, NestedGrandchildPostSlug, "Generics Post",
			"post", "publish", "2024-05-17 00:00:00", "open", 0, "", 0)},

		{relInsert, []any{NestedParentPostID, parentTaxID, 0}},
		{relInsert, []any{NestedChildPostID, childTaxID, 0}},
		{relInsert, []any{NestedGrandchildPostID, grandchildTaxID, 0}},
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, s.q), s.args...); err != nil {
			return fmt.Errorf("seed nested categories %q: %w", s.q, err)
		}
	}
	return migrate.SyncIdentitySequences(ctx, db, vendor, prefix)
}
