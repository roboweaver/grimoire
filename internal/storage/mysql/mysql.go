// Package mysql provides the MySQL/MariaDB adapter wiring (driver + Bun
// dialect). It carries no query logic; repositories come from wprepo.
package mysql

import (
	"database/sql"

	_ "github.com/go-sql-driver/mysql" // MySQL driver, registered as "mysql"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
)

// Open opens a *sql.DB using the go-sql-driver/mysql driver. The DSN should set
// parseTime=true so DATETIME columns scan into time.Time.
func Open(cfg config.DatabaseConfig) (*sql.DB, error) {
	return sql.Open("mysql", cfg.DSN)
}

// NewBunDB wraps a *sql.DB with the MySQL Bun dialect.
func NewBunDB(db *sql.DB) *bun.DB {
	return bun.NewDB(db, mysqldialect.New())
}
