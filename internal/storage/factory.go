// Package storage wires vendor adapters to the domain repository ports. New
// selects the adapter set for the configured vendor; OpenSQL and MigrationsFS
// are shared with the grimoire-cli migrate/seed commands.
package storage

import (
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	mysqlmig "github.com/roboweaver/grimoire/internal/storage/migrations/mysql"
	postgresmig "github.com/roboweaver/grimoire/internal/storage/migrations/postgres"
	sqlitemig "github.com/roboweaver/grimoire/internal/storage/migrations/sqlite"
	"github.com/roboweaver/grimoire/internal/storage/mysql"
	"github.com/roboweaver/grimoire/internal/storage/postgres"
	"github.com/roboweaver/grimoire/internal/storage/sqlite"
	"github.com/roboweaver/grimoire/internal/storage/wprepo"
	"github.com/uptrace/bun"
)

// Set bundles the three repository ports for a single backend.
type Set struct {
	Posts   domain.PostRepository
	Terms   domain.TermRepository
	Options domain.OptionRepository
}

// Repositories owns the underlying database handles and exposes the repository
// Set. Close releases the database connection.
type Repositories struct {
	Set
	db    *sql.DB
	bunDB *bun.DB
}

// DB returns the underlying *sql.DB (used by migrate/seed tooling).
func (r *Repositories) DB() *sql.DB { return r.db }

// Close closes the underlying database connection.
func (r *Repositories) Close() error { return r.db.Close() }

// OpenSQL opens a *sql.DB for the configured vendor without building repos.
// It is shared with the grimoire-cli migrate and seed commands.
func OpenSQL(cfg config.DatabaseConfig) (*sql.DB, error) {
	switch cfg.Vendor {
	case "sqlite":
		return sqlite.Open(cfg)
	case "postgres":
		return postgres.Open(cfg)
	case "mysql":
		return mysql.Open(cfg)
	default:
		return nil, fmt.Errorf("storage: unsupported vendor %q", cfg.Vendor)
	}
}

// NewBunDB wraps an already-open *sql.DB with the vendor's Bun dialect.
func NewBunDB(vendor string, db *sql.DB) (*bun.DB, error) {
	switch vendor {
	case "sqlite":
		return sqlite.NewBunDB(db), nil
	case "postgres":
		return postgres.NewBunDB(db), nil
	case "mysql":
		return mysql.NewBunDB(db), nil
	default:
		return nil, fmt.Errorf("storage: unsupported vendor %q", vendor)
	}
}

// MigrationsFS returns the embedded migration files for the given vendor.
func MigrationsFS(vendor string) (fs.FS, error) {
	switch vendor {
	case "sqlite":
		return sqlitemig.FS(), nil
	case "postgres":
		return postgresmig.FS(), nil
	case "mysql":
		return mysqlmig.FS(), nil
	default:
		return nil, fmt.Errorf("storage: unsupported vendor %q", vendor)
	}
}

// New opens the configured vendor, wraps it with the vendor Bun dialect, and
// builds wprepo-backed repositories.
func New(cfg config.DatabaseConfig) (*Repositories, error) {
	db, err := OpenSQL(cfg)
	if err != nil {
		return nil, err
	}
	bunDB, err := NewBunDB(cfg.Vendor, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	prefix := cfg.TablePrefix
	return &Repositories{
		Set: Set{
			Posts:   wprepo.NewPostRepo(bunDB, prefix),
			Terms:   wprepo.NewTermRepo(bunDB, prefix),
			Options: wprepo.NewOptionRepo(bunDB, prefix),
		},
		db:    db,
		bunDB: bunDB,
	}, nil
}
