package storagetest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
)

func TestPostgresMigrationContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the Postgres migration contract suite")
	}
	RunMigrationContract(t, "postgres", func(t *testing.T) (*sql.DB, string, func()) {
		t.Helper()
		prefix := fmt.Sprintf("gp_%d_", time.Now().UnixNano())
		cfg := config.DatabaseConfig{Vendor: "postgres", DSN: dsn, TablePrefix: prefix}
		db, err := storage.OpenSQL(cfg)
		if err != nil {
			t.Fatalf("OpenSQL: %v", err)
		}
		cleanup := func() {
			ctx := context.Background()
			for _, tbl := range tables {
				db.ExecContext(ctx, "DROP TABLE IF EXISTS "+prefix+tbl)
			}
			db.Close()
		}
		return db, prefix, cleanup
	})
}
