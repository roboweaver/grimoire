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

func TestMySQLMigrationContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_MYSQL_DSN to run the MySQL migration contract suite")
	}
	RunMigrationContract(t, "mysql", func(t *testing.T) (*sql.DB, string, func()) {
		t.Helper()
		prefix := fmt.Sprintf("gm_%d_", time.Now().UnixNano())
		cfg := config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix}
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
