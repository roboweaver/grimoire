package storagetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// newSQLiteRepos builds a migrated SQLite backend carrying SeedFixtures plus any
// extra fixture sets the caller layers on top, mirroring newReposFromDSN's
// contract for the env-gated vendors.
func newSQLiteRepos(t *testing.T, extraSeeds ...seedFunc) (*storage.Repositories, func()) {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		repos.Close()
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		repos.Close()
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		repos.Close()
		t.Fatalf("SeedFixtures: %v", err)
	}
	for i, seed := range extraSeeds {
		if err := seed(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
			repos.Close()
			t.Fatalf("extra seed %d: %v", i, err)
		}
	}
	return repos, func() { repos.Close() }
}

func TestSQLiteContract(t *testing.T) {
	RunContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newSQLiteRepos(t)
	})
}

// TestSQLiteTermParentContract runs the hierarchy reads against a backend that
// also carries the nested category chain; the base contract backend deliberately
// does not, so its absolute post and term counts still hold.
func TestSQLiteTermParentContract(t *testing.T) {
	RunTermParentContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newSQLiteRepos(t, SeedNestedCategories)
	})
}

// TestSQLiteArchiveContract runs the descendant-inclusive archive reads against a
// backend carrying the nested chain plus the archive-specific rows (a
// dual-filed post, a draft and a page in a category, a second author).
func TestSQLiteArchiveContract(t *testing.T) {
	RunArchiveContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newSQLiteRepos(t, SeedArchiveFixtures)
	})
}

// TestSQLiteNicenameContract runs the author lookup against a backend carrying
// two users that share a user_nicename; the base contract backend deliberately
// does not, so its absolute user Count still holds.
func TestSQLiteNicenameContract(t *testing.T) {
	RunNicenameContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newSQLiteRepos(t, SeedDuplicateNicenames)
	})
}

// TestSQLiteNicenameAuditorContract runs the operator-only duplicate report
// against two backends: one carrying the shared-nicename pair, and the plain
// contract backend, where every nicename is unique.
func TestSQLiteNicenameAuditorContract(t *testing.T) {
	RunNicenameAuditorContract(t,
		func(t *testing.T) (*storage.Repositories, func()) {
			return newSQLiteRepos(t, SeedDuplicateNicenames)
		},
		func(t *testing.T) (*storage.Repositories, func()) {
			return newSQLiteRepos(t)
		})
}
