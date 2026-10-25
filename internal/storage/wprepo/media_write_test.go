package wprepo

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	sqlitemig "github.com/roboweaver/grimoire/internal/storage/migrations/sqlite"
	sqliteadapter "github.com/roboweaver/grimoire/internal/storage/sqlite"
)

// newMediaRepo builds a migrated SQLite-backed *MediaRepo for the media write
// contract tests, mirroring newUserRepo exactly (fresh temp-file DB per test,
// same greenfield migration set, direct NewMediaRepo construction to avoid the
// storage->wprepo import cycle).
func newMediaRepo(t *testing.T) *MediaRepo {
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
	return NewMediaRepo(sqliteadapter.NewBunDB(db), prefix)
}

// seedAttachment inserts an attachment {prefix}posts row (post_type=attachment)
// with its _wp_attached_file + _wp_attachment_image_alt postmeta and one
// term_relationships row (object_id = the attachment post ID). Returns the id.
func seedAttachment(t *testing.T, repo *MediaRepo, author int64, relFile string) int64 {
	t.Helper()
	ctx := context.Background()
	vendor := vendorOf(repo.db)
	id, err := insertReturningID(ctx, repo.db, vendor, repo.prefix+"posts",
		[]string{"post_author", "post_title", "post_type", "post_status", "post_mime_type"},
		`"ID"`, author, "An image", "attachment", "inherit", "image/png")
	if err != nil {
		t.Fatalf("seed attachment post: %v", err)
	}
	for _, kv := range [][2]string{{"_wp_attached_file", relFile}, {"_wp_attachment_image_alt", "alt text"}} {
		if _, err := repo.db.ExecContext(ctx,
			"INSERT INTO "+repo.prefix+"postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)",
			id, kv[0], kv[1]); err != nil {
			t.Fatalf("seed postmeta %s: %v", kv[0], err)
		}
	}
	// A term_relationships row linking the attachment to some term_taxonomy_id.
	if _, err := repo.db.ExecContext(ctx,
		"INSERT INTO "+repo.prefix+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)",
		id, 1, 0); err != nil {
		t.Fatalf("seed term_relationships: %v", err)
	}
	return id
}

func mediaCount(t *testing.T, repo *MediaRepo, table, where string, args ...any) int {
	t.Helper()
	q := "SELECT COUNT(*) FROM " + repo.prefix + table
	if where != "" {
		q += " WHERE " + where
	}
	var n int
	if err := repo.db.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestMediaRepoDeleteRemovesRowMetaRelationships covers Req 4.1/4.5: Delete
// removes the attachment's posts row, every postmeta row, and every
// term_relationships row, and a read-back returns domain.ErrNotFound.
func TestMediaRepoDeleteRemovesRowMetaRelationships(t *testing.T) {
	ctx := context.Background()
	repo := newMediaRepo(t)
	id := seedAttachment(t, repo, 2, "2024/05/image.png")

	// preconditions
	if got := mediaCount(t, repo, "postmeta", "post_id = ?", id); got == 0 {
		t.Fatalf("precondition postmeta rows = 0, want > 0")
	}
	if got := mediaCount(t, repo, "term_relationships", "object_id = ?", id); got != 1 {
		t.Fatalf("precondition term_relationships = %d, want 1", got)
	}

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := mediaCount(t, repo, "posts", `"ID" = ?`, id); got != 0 {
		t.Errorf("after delete: posts rows = %d, want 0", got)
	}
	if got := mediaCount(t, repo, "postmeta", "post_id = ?", id); got != 0 {
		t.Errorf("after delete: postmeta rows = %d, want 0 (orphan)", got)
	}
	if got := mediaCount(t, repo, "term_relationships", "object_id = ?", id); got != 0 {
		t.Errorf("after delete: term_relationships = %d, want 0 (orphan)", got)
	}
	if _, err := repo.ByID(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByID after delete = %v, want domain.ErrNotFound", err)
	}
}

// TestMediaRepoDeleteUnknownReturnsNotFound covers Req 4.4: deleting an id no
// attachment has returns domain.ErrNotFound, and a double-delete does too.
func TestMediaRepoDeleteUnknownReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	repo := newMediaRepo(t)
	if err := repo.Delete(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete(unknown) = %v, want domain.ErrNotFound", err)
	}
	id := seedAttachment(t, repo, 2, "2024/05/x.png")
	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	if err := repo.Delete(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second Delete(same id) = %v, want domain.ErrNotFound", err)
	}
}
