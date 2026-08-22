package storagetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// tables are the schema tables a contract run creates, used for best-effort
// cleanup of the scratch prefix on env-gated backends.
var tables = []string{
	"term_relationships", "term_taxonomy", "terms", "postmeta",
	"posts", "options", "users", "schema_migrations",
}

// newReposFromDSN builds a migrated + seeded backend against a scratch prefix
// on a real vendor DSN, cleaning up its tables afterward.
func newReposFromDSN(t *testing.T, vendor, dsn string) (*storage.Repositories, func()) {
	t.Helper()
	ctx := context.Background()
	prefix := fmt.Sprintf("gt_%d_", time.Now().UnixNano())
	cfg := config.DatabaseConfig{Vendor: vendor, DSN: dsn, TablePrefix: prefix}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	migFS, err := storage.MigrationsFS(vendor)
	if err != nil {
		repos.Close()
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, vendor, prefix); err != nil {
		repos.Close()
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := SeedFixtures(ctx, repos.DB(), vendor, prefix); err != nil {
		repos.Close()
		t.Fatalf("SeedFixtures: %v", err)
	}
	cleanup := func() {
		for _, tbl := range tables {
			repos.DB().ExecContext(ctx, "DROP TABLE IF EXISTS "+prefix+tbl)
		}
		repos.Close()
	}
	return repos, cleanup
}
