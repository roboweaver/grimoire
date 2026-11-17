package wprepo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	sqlitemig "github.com/roboweaver/grimoire/internal/storage/migrations/sqlite"
	sqliteadapter "github.com/roboweaver/grimoire/internal/storage/sqlite"
)

// newPostDependentsRepo builds a migrated SQLite-backed *PostRepo, mirroring
// newMediaRepo (direct construction avoids the storage->wprepo import cycle).
func newPostDependentsRepo(t *testing.T) *PostRepo {
	t.Helper()
	ctx := context.Background()
	const prefix = "wp_"
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: filepath.Join(t.TempDir(), "grimoire.db"), TablePrefix: prefix}
	db, err := sqliteadapter.Open(cfg)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := migrate.Apply(ctx, db, sqlitemig.FS(), cfg.Vendor, prefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	return NewPostRepo(sqliteadapter.NewBunDB(db), prefix)
}

// TestPurgePostDependentsAtomicOnFailure covers Req 8.5 and 9.5: when the
// commentmeta delete fails (the table is dropped), the method returns an error
// and the two earlier re-point updates are rolled back, and the comments and
// postmeta of the post are unchanged. The statement order puts both updates
// before the failing delete, so any non-transactional implementation leaves the
// children re-pointed.
func TestPurgePostDependentsAtomicOnFailure(t *testing.T) {
	ctx := context.Background()
	repo := newPostDependentsRepo(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	// P=2 (parent G=1), child page 3, attachment 4, one comment and a postmeta row.
	for _, p := range []struct {
		id     int64
		parent int64
		typ    string
	}{{1, 0, "page"}, {2, 1, "page"}, {3, 2, "page"}, {4, 2, "attachment"}} {
		exec(`INSERT INTO wp_posts ("ID", post_type, post_status, post_parent) VALUES (?, ?, 'publish', ?)`, p.id, p.typ, p.parent)
	}
	exec(`INSERT INTO wp_comments (comment_post_ID, comment_content) VALUES (2, 'c')`)
	exec(`INSERT INTO wp_postmeta (post_id, meta_key, meta_value) VALUES (2, 'k', 'v')`)
	exec(`DROP TABLE wp_commentmeta`)

	if err := repo.PurgePostDependents(ctx, 2, 1, "page"); err == nil {
		t.Fatal("PurgePostDependents with commentmeta dropped = nil, want an error")
	}

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := repo.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM wp_posts WHERE "ID" IN (3, 4) AND post_parent = 2`); n != 2 {
		t.Errorf("children still pointing at P = %d, want 2 (re-point rolled back)", n)
	}
	if n := count(`SELECT COUNT(*) FROM wp_comments WHERE comment_post_ID = 2`); n != 1 {
		t.Errorf("comments of P = %d, want 1 (unchanged)", n)
	}
	if n := count(`SELECT COUNT(*) FROM wp_postmeta WHERE post_id = 2`); n != 1 {
		t.Errorf("postmeta of P = %d, want 1 (unchanged)", n)
	}
}
