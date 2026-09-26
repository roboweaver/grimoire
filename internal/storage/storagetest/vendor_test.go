package storagetest

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// seedFunc is the shape SeedFixtures and SeedNestedCategories share, so a
// backend builder can layer additional fixture sets on the base one without
// each vendor file repeating the migrate/seed dance.
type seedFunc func(ctx context.Context, db *sql.DB, vendor, prefix string) error

// tables are the schema tables a contract run creates, used for best-effort
// cleanup of the scratch prefix on env-gated backends.
var tables = []string{
	"term_relationships", "term_taxonomy", "terms", "postmeta",
	"posts", "options", "sessions", "usermeta", "users", "schema_migrations",
}

// newReposFromDSN builds a migrated + seeded backend against a scratch prefix
// on a real vendor DSN, cleaning up its tables afterward. extraSeeds run after
// SeedFixtures, in order — only the cases that need them pass any, since the
// base fixture set is what the rest of the suite's absolute counts assume.
func newReposFromDSN(t *testing.T, vendor, dsn string, extraSeeds ...seedFunc) (*storage.Repositories, func()) {
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
	for i, seed := range extraSeeds {
		if err := seed(ctx, repos.DB(), vendor, prefix); err != nil {
			repos.Close()
			t.Fatalf("extra seed %d: %v", i, err)
		}
	}
	cleanup := func() {
		for _, tbl := range tables {
			repos.DB().ExecContext(ctx, "DROP TABLE IF EXISTS "+prefix+tbl)
		}
		repos.Close()
	}
	return repos, cleanup
}
