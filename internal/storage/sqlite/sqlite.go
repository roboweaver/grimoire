// Package sqlite provides the pure-Go SQLite adapter wiring (driver + Bun
// dialect). It carries no query logic; repositories come from wprepo.
package sqlite

import (
	"database/sql"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// Open opens a *sql.DB using the pure-Go modernc SQLite driver.
func Open(cfg config.DatabaseConfig) (*sql.DB, error) {
	return sql.Open("sqlite", cfg.DSN)
}

// NewBunDB wraps a *sql.DB with the SQLite Bun dialect.
func NewBunDB(db *sql.DB) *bun.DB {
	return bun.NewDB(db, sqlitedialect.New())
}
