// Package postgres provides the PostgreSQL adapter wiring (pgdriver + Bun
// dialect). It carries no query logic; repositories come from wprepo.
package postgres

import (
	"database/sql"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// Open opens a *sql.DB using the Bun pgdriver connector.
func Open(cfg config.DatabaseConfig) (*sql.DB, error) {
	return sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(cfg.DSN))), nil
}

// NewBunDB wraps a *sql.DB with the PostgreSQL Bun dialect.
func NewBunDB(db *sql.DB) *bun.DB {
	return bun.NewDB(db, pgdialect.New())
}
