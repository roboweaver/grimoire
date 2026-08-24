package storagetest

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
)

func TestSQLiteMigrationContract(t *testing.T) {
	RunMigrationContract(t, "sqlite", func(t *testing.T) (*sql.DB, string, func()) {
		t.Helper()
		dsn := filepath.Join(t.TempDir(), "grimoire-migrate.db")
		cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
		db, err := storage.OpenSQL(cfg)
		if err != nil {
			t.Fatalf("OpenSQL: %v", err)
		}
		return db, cfg.TablePrefix, func() { db.Close() }
	})
}
