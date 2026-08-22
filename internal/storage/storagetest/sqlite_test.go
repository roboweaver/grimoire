package storagetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

func TestSQLiteContract(t *testing.T) {
	newRepos := func(t *testing.T) (*storage.Repositories, func()) {
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
		if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.TablePrefix); err != nil {
			repos.Close()
			t.Fatalf("migrate.Apply: %v", err)
		}
		if err := SeedFixtures(ctx, repos.DB(), cfg.TablePrefix); err != nil {
			repos.Close()
			t.Fatalf("SeedFixtures: %v", err)
		}
		return repos, func() { repos.Close() }
	}
	RunContract(t, newRepos)
}
