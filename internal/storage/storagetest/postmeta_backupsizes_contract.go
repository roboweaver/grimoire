package storagetest

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the BackupSizes contract, far above the other contracts.
const (
	bsSet   int64 = 9950 // attachment with _wp_attachment_backup_sizes
	bsUnset int64 = 9951 // attachment without it
)

// bsRaw is the PHP-serialized backup-sizes value seeded on bsSet.
const bsRaw = `a:1:{s:14:"thumbnail-orig";a:2:{s:4:"file";s:18:"bs-150x150-old.png";s:5:"width";i:150;}}`

// RunPostMetaBackupSizesContract covers PostMetaRepository.BackupSizes: the raw
// "_wp_attachment_backup_sizes" value is returned as stored, and "" when the
// attachment has none.
//
// The vendor and table prefix come from newRepos; the contract holds no
// literal prefix and no vendor-specific placeholder.
func RunPostMetaBackupSizesContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	seed := func(t *testing.T) (repos func() string, cleanup func(), unset func() string) {
		t.Helper()
		r, vendor, prefix, cl := newRepos(t)
		db := r.DB()
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
				t.Fatalf("seed %q: %v", q, err)
			}
		}
		exec(postInsert(vendor, prefix), postArgs(bsSet, "bs-set", "T bs-set", "attachment", "publish", "2000-01-01 00:00:00", "open", 0, "image/png", 0)...)
		exec(postInsert(vendor, prefix), postArgs(bsUnset, "bs-unset", "T bs-unset", "attachment", "publish", "2000-01-01 00:00:00", "open", 0, "image/png", 0)...)
		exec(`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, bsSet, "_wp_attachment_backup_sizes", bsRaw)
		get := func(id int64) string {
			t.Helper()
			v, err := r.PostMeta.BackupSizes(ctx, id)
			if err != nil {
				t.Fatalf("BackupSizes(%d): %v", id, err)
			}
			return v
		}
		return func() string { return get(bsSet) }, cl, func() string { return get(bsUnset) }
	}

	t.Run("BackupSizes returns the seeded raw value", func(t *testing.T) {
		set, cleanup, _ := seed(t)
		defer cleanup()
		if got := set(); got != bsRaw {
			t.Errorf("BackupSizes(%d) = %q, want %q", bsSet, got, bsRaw)
		}
	})

	t.Run("BackupSizes is empty when unset", func(t *testing.T) {
		_, cleanup, unset := seed(t)
		defer cleanup()
		if got := unset(); got != "" {
			t.Errorf("BackupSizes(%d) = %q, want empty", bsUnset, got)
		}
	})
}
