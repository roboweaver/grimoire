package storagetest

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// OpenRawDB opens a fresh, empty vendor database (schema not yet migrated) for
// migration testing, and returns the table prefix to migrate against plus a
// cleanup func. Unlike NewReposFunc this does not build a wprepo bundle or
// seed fixtures; the migration contract only needs a raw *sql.DB.
type OpenRawDB func(t *testing.T) (db *sql.DB, prefix string, cleanup func())

// RunMigrationContract applies migrations 0001-0004 to a fresh database for
// vendor and asserts the M5 0004 migration's six new {prefix}posts columns
// (post_date_gmt, post_modified, post_modified_gmt, ping_status,
// post_password, guid) exist with their documented defaults.
//
// It then documents 0004's intentionally vendor-asymmetric re-application
// behavior (see that migration file's per-vendor header comment): PostgreSQL's
// "ADD COLUMN IF NOT EXISTS" makes 0004 safe to re-run against a schema that
// already has its six columns (e.g. an already-overlaid pre-existing
// database); MySQL/SQLite's plain "ADD COLUMN" does not, and errors with a
// duplicate-column error. This asymmetry must never be "fixed" to be uniform
// across vendors -- it matches M4's 0003 migration's own precedent, and the
// safe-to-rerun guarantee is documented as holding for Postgres only.
func RunMigrationContract(t *testing.T, vendor string, open OpenRawDB) {
	t.Helper()
	ctx := context.Background()

	t.Run("0001-0004 add the six 0004 columns with documented defaults", func(t *testing.T) {
		db, prefix, cleanup := open(t)
		defer cleanup()

		migFS, err := storage.MigrationsFS(vendor)
		if err != nil {
			t.Fatalf("MigrationsFS: %v", err)
		}
		v, err := migrate.Apply(ctx, db, migFS, vendor, prefix)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if v != 4 {
			t.Fatalf("version = %d, want 4", v)
		}

		insert := placeholders(vendor,
			`INSERT INTO `+prefix+`posts (post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_type, comment_status) VALUES (%s)`,
			9)
		if _, err := db.ExecContext(ctx, insert, 1, "2024-01-01 00:00:00", "body", "title", "excerpt", "publish", "0004-test", "post", "open"); err != nil {
			t.Fatalf("insert post: %v", err)
		}

		row := db.QueryRowContext(ctx, placeholders(vendor,
			`SELECT post_date_gmt, post_modified, post_modified_gmt, ping_status, post_password, guid FROM `+prefix+`posts WHERE post_name = %s`,
			1),
			"0004-test")
		var dateGMT, modified, modifiedGMT, pingStatus, password, guid any
		if err := row.Scan(&dateGMT, &modified, &modifiedGMT, &pingStatus, &password, &guid); err != nil {
			t.Fatalf("scan 0004 columns: %v", err)
		}
		if !isEpochDefault(dateGMT) {
			t.Errorf("post_date_gmt = %v, want epoch default", dateGMT)
		}
		if !isEpochDefault(modified) {
			t.Errorf("post_modified = %v, want epoch default", modified)
		}
		if !isEpochDefault(modifiedGMT) {
			t.Errorf("post_modified_gmt = %v, want epoch default", modifiedGMT)
		}
		if asString(pingStatus) != "open" {
			t.Errorf("ping_status = %q, want %q", asString(pingStatus), "open")
		}
		if asString(password) != "" {
			t.Errorf("post_password = %q, want empty", asString(password))
		}
		if asString(guid) != "" {
			t.Errorf("guid = %q, want empty", asString(guid))
		}
	})

	if vendor == "postgres" {
		t.Run("0004 is safe to re-apply against a schema that already has its columns (Postgres ADD COLUMN IF NOT EXISTS)", func(t *testing.T) {
			db, prefix, cleanup := open(t)
			defer cleanup()
			if err := migrateThenRerun0004(ctx, db, vendor, prefix); err != nil {
				t.Fatalf("re-applying 0004 against an already-migrated Postgres schema errored (want a silent no-op via ADD COLUMN IF NOT EXISTS): %v", err)
			}
		})
		return
	}

	t.Run("0004 errors if re-applied against a schema that already has its columns ("+vendor+" plain ADD COLUMN)", func(t *testing.T) {
		db, prefix, cleanup := open(t)
		defer cleanup()
		err := migrateThenRerun0004(ctx, db, vendor, prefix)
		if err == nil {
			t.Fatalf("re-applying 0004 against an already-migrated %s schema unexpectedly succeeded; it must error here -- this is documented and intentional: %s uses plain ADD COLUMN, not ADD COLUMN IF NOT EXISTS, so 0004 must never be pointed at an already-overlaid %s database", vendor, vendor, vendor)
		}
	})
}

// migrateThenRerun0004 applies 0001-0004 normally, then re-executes 0004's raw
// SQL file a second time directly (bypassing the schema_migrations version
// guard, which would otherwise just skip an already-applied version). This
// simulates the real-world scenario the migration file's header documents: a
// database that already has 0004's six columns (whether from a prior
// grimoire migration run or, on Postgres only, from any other source) being
// pointed at 0004 again.
func migrateThenRerun0004(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	migFS, err := storage.MigrationsFS(vendor)
	if err != nil {
		return err
	}
	if _, err := migrate.Apply(ctx, db, migFS, vendor, prefix); err != nil {
		return fmt.Errorf("initial Apply: %w", err)
	}
	data, err := fs.ReadFile(migFS, "0004_rest_post_fields.up.sql")
	if err != nil {
		return err
	}
	body := strings.ReplaceAll(string(data), "{{prefix}}", prefix)
	for _, stmt := range splitSQLStatements(body) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// splitSQLStatements splits a SQL script into individual statements on
// semicolon boundaries, stripping full-line comments. Mirrors the migrate
// package's own (unexported) splitStatements; duplicated here since this test
// helper re-executes a single already-loaded migration file directly rather
// than going through migrate.Apply.
func splitSQLStatements(script string) []string {
	var cleaned strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		cleaned.WriteString(line)
		cleaned.WriteByte('\n')
	}
	var out []string
	for _, part := range strings.Split(cleaned.String(), ";") {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

// placeholders renders a query template's single "%s" (or repeated call for
// n params) as either "?, ?, ..." (SQLite/MySQL) or "$1, $2, ..." (Postgres).
func placeholders(vendor, template string, n int) string {
	ph := make([]string, n)
	for i := range ph {
		if vendor == "postgres" {
			ph[i] = fmt.Sprintf("$%d", i+1)
		} else {
			ph[i] = "?"
		}
	}
	return fmt.Sprintf(template, strings.Join(ph, ", "))
}

// isEpochDefault reports whether a scanned 0004 timestamp column value
// represents the documented '1970-01-01 00:00:00' UTC default, regardless of
// whether the driver returned it as a string, []byte, or time.Time.
func isEpochDefault(v any) bool {
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Equal(epoch)
	case []byte:
		return isEpochDefaultString(string(t))
	case string:
		return isEpochDefaultString(t)
	default:
		return false
	}
}

func isEpochDefaultString(s string) bool {
	if strings.TrimSpace(s) == "1970-01-01 00:00:00" {
		return true
	}
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm.UTC().Equal(epoch)
		}
	}
	return false
}

// asString normalizes a driver-scanned value (string/[]byte/nil) to a string
// for equality assertions against plain-text columns.
func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
