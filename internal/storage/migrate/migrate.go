// Package migrate applies embedded, per-vendor SQL migrations against a
// *sql.DB. It records applied versions in a prefixed tracking table so that
// applying a set repeatedly is idempotent.
//
// There are two independent migration sets, each with its own version stream:
//
//   - The greenfield set (Apply, tracked in {prefix}schema_migrations) builds
//     grimoire's WordPress-compatible schema from an empty database. It uses
//     plain ALTER TABLE ... ADD COLUMN, so it must never be pointed at a real
//     WordPress database, whose tables already have those columns.
//
//   - The overlay set (ApplyOverlay, tracked in {prefix}grimoire_migrations)
//     creates only grimoire-owned objects, entirely with IF NOT EXISTS and
//     without any ALTER TABLE. It is safe against a live, populated WordPress
//     database and is the supported way to adopt one.
//
// The two tracking tables are deliberately separate: the sets number their
// migrations independently, so a shared table would let one set's version 1
// mask the other's.
//
// Preflight complements ApplyOverlay by verifying, without writing anything,
// that a database already provides the WordPress tables and columns grimoire
// reads.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// prefixToken is replaced with the configured table prefix in migration SQL.
const prefixToken = "{{prefix}}"

// Tracking-table suffixes for the two migration sets. Each set counts its
// migrations from 0001, so they must not share a version stream.
const (
	greenfieldTable = "schema_migrations"
	overlayTable    = "grimoire_migrations"
)

// Apply runs the greenfield migration set: all migrations in migFS with a
// version greater than the highest already applied, replacing prefixToken with
// prefix. It returns the highest applied version. Apply is safe to call
// repeatedly; already-applied migrations are skipped. vendor selects the
// placeholder dialect for the version-tracking INSERT (see
// internal/storage/rebind).
//
// Apply assumes an empty or grimoire-provisioned database. Against an existing
// WordPress database its ALTER TABLE ... ADD COLUMN statements will fail with a
// duplicate-column error on MySQL and SQLite; use ApplyOverlay there instead.
func Apply(ctx context.Context, db *sql.DB, migFS fs.FS, vendor, prefix string) (int, error) {
	return apply(ctx, db, migFS, vendor, prefix, prefix+greenfieldTable)
}

// ApplyOverlay runs the overlay migration set against an existing, populated
// WordPress database, creating only the grimoire-owned objects that database is
// missing. Progress is tracked in {prefix}grimoire_migrations, separately from
// Apply's {prefix}schema_migrations.
//
// The overlay set contains no ALTER TABLE and only IF NOT EXISTS statements, so
// it alters nothing that already exists and is safe to run more than once.
// Callers adopting an unknown database should run Preflight first to confirm the
// WordPress tables and columns grimoire reads are actually present.
func ApplyOverlay(ctx context.Context, db *sql.DB, migFS fs.FS, vendor, prefix string) (int, error) {
	return apply(ctx, db, migFS, vendor, prefix, prefix+overlayTable)
}

// PendingOverlay returns the names of overlay migrations in migFS that have not
// yet been recorded in {prefix}grimoire_migrations, in apply order.
//
// It writes nothing at all -- not even the tracking table, which ApplyOverlay
// would create -- so it is safe to run against a production database. A missing
// tracking table simply means nothing has been applied yet.
func PendingOverlay(ctx context.Context, db *sql.DB, migFS fs.FS, prefix string) ([]string, error) {
	migTable := prefix + overlayTable
	applied := 0
	// Probe with a zero-row read so an absent tracking table is not an error.
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT MAX(version) FROM %s`, migTable)).Scan(&v); err == nil && v.Valid {
		applied = int(v.Int64)
	}
	migs, err := loadMigrations(migFS)
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, m := range migs {
		if m.version > applied {
			pending = append(pending, m.name)
		}
	}
	return pending, nil
}

// apply is the shared migration runner behind Apply and ApplyOverlay; migTable
// selects which version stream to record progress in.
func apply(ctx context.Context, db *sql.DB, migFS fs.FS, vendor, prefix, migTable string) (int, error) {
	if _, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (version BIGINT PRIMARY KEY, applied_at VARCHAR(64) NOT NULL)`,
		migTable,
	)); err != nil {
		return 0, fmt.Errorf("migrate: ensure %s: %w", migTable, err)
	}

	applied, err := currentVersion(ctx, db, migTable)
	if err != nil {
		return 0, err
	}

	migs, err := loadMigrations(migFS)
	if err != nil {
		return 0, err
	}

	highest := applied
	for _, m := range migs {
		if m.version <= applied {
			if m.version > highest {
				highest = m.version
			}
			continue
		}
		if err := applyOne(ctx, db, migTable, m, vendor, prefix); err != nil {
			return highest, err
		}
		highest = m.version
	}
	return highest, nil
}

type migration struct {
	version int
	name    string
	sql     string
}

func currentVersion(ctx context.Context, db *sql.DB, migTable string) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT MAX(version) FROM %s`, migTable)).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("migrate: read current version: %w", err)
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

func loadMigrations(migFS fs.FS) ([]migration, error) {
	entries, err := fs.Glob(migFS, "*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("migrate: glob: %w", err)
	}
	migs := make([]migration, 0, len(entries))
	for _, name := range entries {
		version, err := parseVersion(name)
		if err != nil {
			return nil, err
		}
		data, err := fs.ReadFile(migFS, name)
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", name, err)
		}
		migs = append(migs, migration{version: version, name: name, sql: string(data)})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	return migs, nil
}

func parseVersion(name string) (int, error) {
	base := path.Base(name)
	idx := strings.IndexByte(base, '_')
	if idx <= 0 {
		return 0, fmt.Errorf("migrate: cannot parse version from %q (expected NNNN_name.up.sql)", base)
	}
	version, err := strconv.Atoi(base[:idx])
	if err != nil {
		return 0, fmt.Errorf("migrate: cannot parse version from %q: %w", base, err)
	}
	return version, nil
}

func applyOne(ctx context.Context, db *sql.DB, migTable string, m migration, vendor, prefix string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: begin tx for %s: %w", m.name, err)
	}
	defer tx.Rollback()

	body := strings.ReplaceAll(m.sql, prefixToken, prefix)
	for _, stmt := range splitStatements(body) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: exec %s: %w\nSQL: %s", m.name, err, stmt)
		}
	}
	versionInsert := rebind.Rebind(vendor,
		fmt.Sprintf(`INSERT INTO %s (version, applied_at) VALUES (%d, ?)`, migTable, m.version))
	if _, err := tx.ExecContext(ctx, versionInsert,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("migrate: record version %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: commit %s: %w", m.name, err)
	}
	return nil
}

// splitStatements splits a SQL script into individual statements on semicolon
// boundaries, stripping full-line comments (-- ...) and blank statements.
//
// Limitation: the split is naive — it treats every ';' as a statement
// terminator and does not account for semicolons inside single-quoted string
// literals, dollar-quoted bodies ($$...$$), or trigger/function definitions.
// This is safe for the current per-vendor DDL (plain CREATE TABLE/INDEX with no
// embedded ';'); revisit this if migrations ever add such constructs.
func splitStatements(script string) []string {
	var cleaned strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cleaned.WriteString(line)
		cleaned.WriteByte('\n')
	}
	var out []string
	for _, part := range strings.Split(cleaned.String(), ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
