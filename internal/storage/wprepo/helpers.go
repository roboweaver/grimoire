package wprepo

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

// tsLayout is the fixed-width timestamp format used across the WordPress-shaped
// schema ('YYYY-MM-DD HH:MM:SS', UTC, no zone). Fixed width keeps lexical string
// comparison valid for expiry and garbage-collection queries and matches the
// column defaults in the migrations.
const tsLayout = "2006-01-02 15:04:05"

// formatTS renders t as a fixed-width UTC timestamp string. A zero time is
// stored as the epoch default so columns never receive an empty value.
func formatTS(t time.Time) string {
	if t.IsZero() {
		return "1970-01-01 00:00:00"
	}
	return t.UTC().Format(tsLayout)
}

// parseLayouts are the timestamp formats parseTS accepts, tried in order. The
// fixed-width space layout is the canonical stored form (parseTime=false, the
// lexical path). RFC3339 / RFC3339Nano are what the go-sql-driver emits when
// parseTime=true: DATETIME columns are scanned as time.Time and bun then formats
// them as RFC3339 into the string destination fields, so parseTS must accept
// them or every timestamp (crucially session Expires) collapses to the zero time.
var parseLayouts = []string{
	tsLayout,
	time.RFC3339,
	time.RFC3339Nano,
}

// parseTS parses a stored timestamp as UTC, tolerating the formats the DB
// drivers actually emit. Unparseable or empty values yield the zero time rather
// than an error, so partially populated rows still map cleanly to domain
// entities.
func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range parseLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// errNotFoundIfZero returns domain.ErrNotFound when a write affected no rows,
// used to signal a missing target for UPDATE/DELETE by primary key.
func errNotFoundIfZero(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// errNotFoundIfMissing disambiguates "0 rows affected" from an UPDATE that
// genuinely found no matching row versus one that matched a row but made no
// actual change to it. SQLite and PostgreSQL's default drivers report
// RowsAffected as "rows matched by WHERE", so a true no-op UPDATE (setting a
// column to the value it already holds) still reports 1. MySQL's default
// driver instead reports "rows whose values actually changed", so the same
// no-op UPDATE reports 0 there even though the row exists — which would
// otherwise be misread as domain.ErrNotFound. When RowsAffected is 0, exists
// is consulted to tell the two cases apart; only a genuinely absent row is an
// error.
func errNotFoundIfMissing(ctx context.Context, res sql.Result, exists func(ctx context.Context) (bool, error)) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	ok, err := exists(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotFound
	}
	return nil
}

// vendorOf maps a Bun dialect to the vendor string understood by rebind.
func vendorOf(db *bun.DB) string {
	switch db.Dialect().Name() {
	case dialect.PG:
		return "postgres"
	case dialect.MySQL:
		return "mysql"
	default:
		return "sqlite"
	}
}

// execQuerier is the subset of database/sql used by raw write paths. Both
// *bun.DB and bun.Tx satisfy it, so insert helpers work inside a transaction.
type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// insertReturningID runs an INSERT and returns the generated primary key.
//
// The column list must contain only lowercase/underscore identifiers (never the
// mixed-case "ID" column) so the unquoted SQL is valid on every vendor. On
// PostgreSQL the returning expression (already vendor-quoted, e.g. `"ID"`) is
// appended as a RETURNING clause and scanned; on SQLite and MySQL the generated
// key comes from LastInsertId.
func insertReturningID(ctx context.Context, q execQuerier, vendor, table string, cols []string, returning string, args ...any) (int64, error) {
	ph := make([]string, len(cols))
	for i := range ph {
		ph[i] = "?"
	}
	query := "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(ph, ", ") + ")"
	if vendor == "postgres" {
		query += " RETURNING " + returning
		var id int64
		if err := q.QueryRowContext(ctx, rebind.Rebind(vendor, query), args...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	res, err := q.ExecContext(ctx, rebind.Rebind(vendor, query), args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
