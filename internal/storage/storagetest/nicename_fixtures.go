package storagetest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Duplicate-nicename fixture identifiers, exported so assertions can name the
// row that must win instead of re-deriving it from the read under test.
//
// The user ids sit in their own band: SeedFixtures pins user 1 and
// SeedArchiveExtras pins user 2, so 70/71 leaves room for either set to grow
// and keeps a database carrying several fixture sets unambiguous.
const (
	// DuplicateNicename is shared by two user rows. user_nicename carries a
	// non-unique index in WordPress's schema, so this is schema-legal even
	// though wp_insert_user() prevents it on write (Req 7.6).
	DuplicateNicename = "twin"

	// DuplicateNicenameWinnerID is the *lower* of the two ids, and therefore
	// the row ByNicename must resolve to (Req 7.6). It is inserted *second* —
	// see SeedDuplicateNicenames.
	DuplicateNicenameWinnerID          = 70
	DuplicateNicenameWinnerLogin       = "twin-lower"
	DuplicateNicenameWinnerDisplayName = "Twin Lower"

	// DuplicateNicenameLoserID is the higher id: a real row, reachable by ID
	// and by login, that ByNicename must nonetheless not return.
	DuplicateNicenameLoserID          = 71
	DuplicateNicenameLoserLogin       = "twin-higher"
	DuplicateNicenameLoserDisplayName = "Twin Higher"

	// UniqueNicenameUserID / UniqueNicenameUser are SeedFixtures' own user,
	// named here so the "resolves a seeded user" case does not repeat a literal
	// that lives in another file.
	UniqueNicenameUserID = 1
	UniqueNicename       = "admin"

	// UnknownNicename matches no row in any fixture set, backing the
	// ErrNotFound case.
	UnknownNicename = "nobody"
)

// SeedDuplicateNicenames adds two user rows sharing one user_nicename, on top of
// an already-seeded SeedFixtures database:
//
//	71 twin-higher  user_nicename "twin"   <- inserted FIRST
//	70 twin-lower   user_nicename "twin"   <- inserted SECOND, and must win
//
// The insertion order is load-bearing rather than incidental. If the lower id
// were inserted first, a repository issuing WHERE user_nicename = ? LIMIT 1
// with no ORDER BY would very likely return it anyway — on every vendor a
// single-row-scan plan tends to surface rows in physical insertion order — and
// the case would pass against exactly the implementation Req 7.6 exists to
// forbid. Inserting the higher id first makes "lowest ID wins" a claim about
// ORDER BY and not about storage layout.
//
// It is a separate helper rather than an extension of SeedFixtures or
// SeedArchiveExtras for the reason both of those already document: assertions
// against those sets are absolute. contract.go asserts a user Count of 3 after
// creating 2 users, so two more seeded users would break it; SeedArchiveExtras
// already adds user 2 and the archive contract counts posts per author. Layering
// this pair only under the nicename contract's own backend breaks neither.
//
// Preconditions: the schema is migrated and SeedFixtures has run (the unique
// nicename the contract resolves is its admin row).
//
// Like every other seed it pins explicit primary keys, so it re-syncs the
// PostgreSQL identity sequences afterward.
func SeedDuplicateNicenames(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	userInsert := `INSERT INTO ` + prefix + `users (` + rebind.Ident(vendor, "ID") +
		`, user_login, user_nicename, display_name) VALUES (?, ?, ?, ?)`

	stmts := []struct {
		q    string
		args []any
	}{
		// Higher id first — deliberately. See the doc comment above.
		{userInsert, []any{DuplicateNicenameLoserID, DuplicateNicenameLoserLogin,
			DuplicateNicename, DuplicateNicenameLoserDisplayName}},
		{userInsert, []any{DuplicateNicenameWinnerID, DuplicateNicenameWinnerLogin,
			DuplicateNicename, DuplicateNicenameWinnerDisplayName}},
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, s.q), s.args...); err != nil {
			return fmt.Errorf("seed duplicate nicenames %q: %w", s.q, err)
		}
	}
	return migrate.SyncIdentitySequences(ctx, db, vendor, prefix)
}
