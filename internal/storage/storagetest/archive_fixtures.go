package storagetest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Archive fixture identifiers, exported so assertions can name known rows
// instead of re-deriving them from the read under test.
//
// The ids continue the band SeedNestedCategories opened (posts 401-403, terms
// 50-52, term_taxonomy 60-62) rather than reusing it, so
// TestSeedNestedCategoriesIsAdditive keeps holding for a database carrying only
// the nested chain, and a database carrying both sets still has its highest ids
// in one contiguous run.
const (
	// ArchiveDualPostID is filed under BOTH the parent and the child category.
	// A term-set predicate expressed as a join would list and count it twice,
	// which is the defect Req 3.3 exists to prevent; no other fixture row can
	// catch that.
	ArchiveDualPostID   = 404
	ArchiveDualPostSlug = "dual-post"

	// ArchiveDraftPostID is a *draft* in the child category, so
	// "published-only" is asserted against a row that the term predicate
	// otherwise matches (Req 3.4). A draft outside the hierarchy would be
	// excluded by the term filter and prove nothing.
	ArchiveDraftPostID   = 405
	ArchiveDraftPostSlug = "hidden-go-post"

	// ArchivePagePostID is a published post_type='page' in the parent category.
	// SeedFixtures' page carries no category, so this is the only row that can
	// fail a missing Types default (Req 3.5).
	ArchivePagePostID   = 406
	ArchivePagePostSlug = "tech-page"

	// ArchiveOtherAuthorID owns exactly one published post and nothing else, so
	// an author predicate that is silently ignored returns a different row count
	// rather than the same one.
	ArchiveOtherAuthorID       = 2
	ArchiveOtherAuthorLogin    = "archivist"
	ArchiveOtherAuthorNicename = "archivist"

	ArchiveOtherAuthorPostID   = 407
	ArchiveOtherAuthorPostSlug = "archivist-post"
)

// SeedArchiveExtras adds the rows the archive contract needs which neither
// SeedFixtures nor SeedNestedCategories provides, on top of both:
//
//	404 dual-post       post  publish 2024-05-18  author 1  Tech AND Go
//	405 hidden-go-post  post  draft   2024-05-19  author 1  Go
//	406 tech-page       page  publish 2024-05-20  author 1  Tech
//	407 archivist-post  post  publish 2024-05-21  author 2  (no category)
//
// Each row exists to make one archive rule falsifiable: a post in a parent and
// a child at once (Req 3.3), an unpublished post the term predicate matches
// (Req 3.4), a page the term predicate matches (Req 3.5) and a post by a second
// author (Req 7/12.2). The date-boundary cases need no new rows —
// SeedNestedCategories already spaces its three posts a day apart, so
// [2024-05-15, 2024-05-17) has one post exactly on each bound.
//
// It is a third helper rather than an extension of either existing seed for the
// same reason SeedNestedCategories is a second one: the assertions against those
// sets are absolute. contract.go pins exactly 3 published posts in a fixed slug
// order and a user Count of 3 after creating 2, and
// TestSeedNestedCategoriesIsAdditive pins the highest post, term and
// term_taxonomy ids at 403, 52 and 62. Adding a user and four posts to either
// seed would break those; layering them only under the archive contract's own
// backend breaks nothing.
//
// Preconditions: SeedFixtures and then SeedNestedCategories have both run. This
// helper relates posts to the nested categories by term_taxonomy_id, so it
// depends on that chain existing; it creates no terms of its own.
//
// Like both other seeds it pins explicit primary keys, so it re-syncs the
// PostgreSQL identity sequences afterward.
func SeedArchiveExtras(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	userInsert := `INSERT INTO ` + prefix + `users (` + rebind.Ident(vendor, "ID") +
		`, user_login, user_nicename, display_name) VALUES (?, ?, ?, ?)`
	relInsert := `INSERT INTO ` + prefix +
		`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`

	stmts := []struct {
		q    string
		args []any
	}{
		{userInsert, []any{ArchiveOtherAuthorID, ArchiveOtherAuthorLogin,
			ArchiveOtherAuthorNicename, "Archivist"}},

		{postInsert(vendor, prefix), archivePostArgs(ArchiveDualPostID, 1, ArchiveDualPostSlug,
			"Dual Post", "post", "publish", "2024-05-18 00:00:00")},
		{postInsert(vendor, prefix), archivePostArgs(ArchiveDraftPostID, 1, ArchiveDraftPostSlug,
			"Hidden Go Post", "post", "draft", "2024-05-19 00:00:00")},
		{postInsert(vendor, prefix), archivePostArgs(ArchivePagePostID, 1, ArchivePagePostSlug,
			"Tech Page", "page", "publish", "2024-05-20 00:00:00")},
		{postInsert(vendor, prefix), archivePostArgs(ArchiveOtherAuthorPostID, ArchiveOtherAuthorID,
			ArchiveOtherAuthorPostSlug, "Archivist Post", "post", "publish", "2024-05-21 00:00:00")},

		// The duplicate assignment: one post, two term_taxonomy rows, one of
		// them the other's parent.
		{relInsert, []any{ArchiveDualPostID, NestedParentTermTaxonomyID, 0}},
		{relInsert, []any{ArchiveDualPostID, NestedChildTermTaxonomyID, 0}},

		{relInsert, []any{ArchiveDraftPostID, NestedChildTermTaxonomyID, 0}},
		{relInsert, []any{ArchivePagePostID, NestedParentTermTaxonomyID, 0}},
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, s.q), s.args...); err != nil {
			return fmt.Errorf("seed archive extras %q: %w", s.q, err)
		}
	}
	return migrate.SyncIdentitySequences(ctx, db, vendor, prefix)
}

// archivePostArgs mirrors postArgs but takes the author, because every row
// postArgs builds is authored by user 1 and the author predicate needs a second
// one. The column list is postInsert's, unchanged.
func archivePostArgs(id, author int64, slug, title, ptype, status, date string) []any {
	return []any{id, author, date, "<p>body</p>", title, "excerpt", status, slug, ptype,
		"open", 0, "", 0}
}

// SeedArchiveFixtures is the single seed the archive contract's backends layer on
// SeedFixtures: the nested chain plus the archive-specific rows, in the order
// SeedArchiveExtras requires. It exists so each vendor file names one function
// rather than repeating a two-element ordering that matters.
func SeedArchiveFixtures(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	if err := SeedNestedCategories(ctx, db, vendor, prefix); err != nil {
		return err
	}
	return SeedArchiveExtras(ctx, db, vendor, prefix)
}
