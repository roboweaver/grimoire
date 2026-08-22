// Package migrate applies embedded, per-vendor SQL migrations against a
// *sql.DB. It records applied versions in a prefixed schema_migrations table so
// that Apply is idempotent.
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
)

// prefixToken is replaced with the configured table prefix in migration SQL.
const prefixToken = "{{prefix}}"

// Apply runs all migrations in migFS with a version greater than the highest
// already applied, replacing prefixToken with prefix. It returns the highest
// applied version. Apply is safe to call repeatedly; already-applied
// migrations are skipped.
func Apply(ctx context.Context, db *sql.DB, migFS fs.FS, prefix string) (int, error) {
	migTable := prefix + "schema_migrations"
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
		if err := applyOne(ctx, db, migTable, m, prefix); err != nil {
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

func applyOne(ctx context.Context, db *sql.DB, migTable string, m migration, prefix string) error {
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
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s (version, applied_at) VALUES (%d, ?)`, migTable, m.version),
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
