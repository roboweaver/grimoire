package storagetest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// RunOverlayContract pins down the behavior that lets grimoire adopt an existing,
// populated WordPress database (issue #4).
//
// Every case runs against a "stock WordPress" fixture: a database that has all
// the WordPress tables and columns grimoire reads, already carries rows, and has
// none of grimoire's own objects -- no {prefix}sessions and no version-tracking
// table. See newStockWordPressDB for how that shape is derived rather than
// hand-written.
//
// The contract is:
//
//   - The greenfield set fails against it (that is the reported bug, and the
//     reason the overlay set exists).
//   - The overlay set succeeds, adds only grimoire-owned tables, and leaves every
//     WordPress table's columns and rows untouched.
//   - The overlay set is re-runnable.
//   - Preflight accepts a WordPress-shaped database and explains itself on one
//     that is not.
//   - The sessions table the overlay path creates matches the one the greenfield
//     path creates, so the two definitions cannot drift apart.
func RunOverlayContract(t *testing.T, vendor string, open OpenRawDB) {
	t.Helper()
	ctx := context.Background()

	// The tables a stock WordPress database owns, and which the overlay set must
	// therefore leave strictly alone.
	wpTables := func(prefix string) []string {
		var out []string
		for _, rt := range migrate.RequiredSchema() {
			out = append(out, prefix+rt.Name)
		}
		return out
	}

	t.Run("greenfield migrate fails against a stock WordPress database (the bug the overlay set fixes)", func(t *testing.T) {
		db, prefix, cleanup := open(t)
		defer cleanup()
		newStockWordPressDB(ctx, t, db, vendor, prefix)

		migFS, err := storage.MigrationsFS(vendor)
		if err != nil {
			t.Fatalf("MigrationsFS: %v", err)
		}
		_, err = migrate.Apply(ctx, db, migFS, vendor, prefix)
		if vendor == "postgres" {
			// Postgres's ADD COLUMN IF NOT EXISTS means the greenfield set
			// happens not to error here. It is still the wrong set to use --
			// it would claim ownership of a schema it did not create -- but
			// there is no failure to assert.
			if err != nil {
				t.Fatalf("greenfield Apply on Postgres: %v", err)
			}
			return
		}
		if err == nil {
			t.Fatalf("greenfield migrate.Apply unexpectedly succeeded against a stock WordPress %s database; "+
				"it uses plain ALTER TABLE ADD COLUMN and must fail on columns that already exist "+
				"(this failure is what -overlay exists to route around)", vendor)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") &&
			!strings.Contains(strings.ToLower(err.Error()), "already exists") {
			t.Logf("greenfield Apply failed as required, though not with a recognizably "+
				"duplicate-column message: %v", err)
		}
	})

	t.Run("overlay creates only grimoire-owned tables and alters nothing that already exists", func(t *testing.T) {
		db, prefix, cleanup := open(t)
		defer cleanup()
		newStockWordPressDB(ctx, t, db, vendor, prefix)

		// Snapshot the shape and contents of every WordPress table beforehand.
		before := map[string][]string{}
		beforeRows := map[string]int{}
		for _, table := range wpTables(prefix) {
			before[table] = columnsOf(ctx, t, db, table)
			beforeRows[table] = countRows(ctx, t, db, table)
		}
		if got := beforeRows[prefix+"users"]; got == 0 {
			t.Fatalf("fixture should have pre-populated %susers; got 0 rows", prefix+"users")
		}
		if tableReadable(ctx, db, prefix+"sessions") {
			t.Fatalf("fixture should not have %ssessions before the overlay runs", prefix)
		}

		overlayFS, err := storage.OverlayMigrationsFS(vendor)
		if err != nil {
			t.Fatalf("OverlayMigrationsFS: %v", err)
		}
		v, err := migrate.ApplyOverlay(ctx, db, overlayFS, vendor, prefix)
		if err != nil {
			t.Fatalf("ApplyOverlay against a stock WordPress database must succeed, got: %v", err)
		}
		if v != 1 {
			t.Errorf("overlay version = %d, want 1", v)
		}

		// The grimoire-owned table now exists and is usable.
		if !tableReadable(ctx, db, prefix+"sessions") {
			t.Errorf("%ssessions was not created by the overlay set", prefix)
		}
		insertSession(ctx, t, db, vendor, prefix)

		// Nothing about the WordPress tables changed: same columns, same order,
		// same row counts.
		for _, table := range wpTables(prefix) {
			after := columnsOf(ctx, t, db, table)
			if !sameStrings(before[table], after) {
				t.Errorf("overlay changed the columns of %s\n before: %v\n  after: %v",
					table, before[table], after)
			}
			if got := countRows(ctx, t, db, table); got != beforeRows[table] {
				t.Errorf("overlay changed the row count of %s: before %d, after %d",
					table, beforeRows[table], got)
			}
		}
	})

	t.Run("overlay is re-runnable", func(t *testing.T) {
		db, prefix, cleanup := open(t)
		defer cleanup()
		newStockWordPressDB(ctx, t, db, vendor, prefix)
		overlayFS, err := storage.OverlayMigrationsFS(vendor)
		if err != nil {
			t.Fatalf("OverlayMigrationsFS: %v", err)
		}
		if _, err := migrate.ApplyOverlay(ctx, db, overlayFS, vendor, prefix); err != nil {
			t.Fatalf("first ApplyOverlay: %v", err)
		}
		// A session row proves the second run does not recreate (and so empty)
		// the table.
		insertSession(ctx, t, db, vendor, prefix)
		v, err := migrate.ApplyOverlay(ctx, db, overlayFS, vendor, prefix)
		if err != nil {
			t.Fatalf("second ApplyOverlay must be a no-op, got: %v", err)
		}
		if v != 1 {
			t.Errorf("overlay version after re-run = %d, want 1", v)
		}
		if got := countRows(ctx, t, db, prefix+"sessions"); got != 1 {
			t.Errorf("%ssessions row count after re-run = %d, want 1 (table must not be recreated)", prefix, got)
		}
		if got := countRows(ctx, t, db, prefix+"grimoire_migrations"); got != 1 {
			t.Errorf("overlay tracking rows = %d, want 1 (version must be recorded once)", got)
		}
	})

	t.Run("overlay tracking table is separate from the greenfield one", func(t *testing.T) {
		// The two sets both number their migrations from 0001. Sharing a
		// tracking table would let the greenfield set's version 1 convince the
		// overlay set it had already run, silently skipping the sessions table.
		db, prefix, cleanup := open(t)
		defer cleanup()
		migFS, err := storage.MigrationsFS(vendor)
		if err != nil {
			t.Fatalf("MigrationsFS: %v", err)
		}
		if _, err := migrate.Apply(ctx, db, migFS, vendor, prefix); err != nil {
			t.Fatalf("greenfield Apply: %v", err)
		}
		overlayFS, err := storage.OverlayMigrationsFS(vendor)
		if err != nil {
			t.Fatalf("OverlayMigrationsFS: %v", err)
		}
		pending, err := migrate.PendingOverlay(ctx, db, overlayFS, prefix)
		if err != nil {
			t.Fatalf("PendingOverlay: %v", err)
		}
		if len(pending) != 1 {
			t.Errorf("after a greenfield migrate, pending overlay migrations = %v, want exactly 1 "+
				"(the greenfield version stream must not mask the overlay one)", pending)
		}
	})

	t.Run("RequiredSchema matches the schema the greenfield set builds, exactly", func(t *testing.T) {
		// The drift guard for migrate.RequiredSchema, and it has to run in both
		// directions to be worth anything.
		//
		// Too strict (RequiredSchema names a column the migrations do not create)
		// makes Preflight reject databases that are actually fine. The OK() check
		// below catches that.
		//
		// Too lax is the dangerous direction and an OK() check cannot see it: if a
		// later migration adds a column grimoire's queries depend on and
		// RequiredSchema is not updated to match, Preflight would wave through a
		// WordPress database missing that column and grimoire would fail later,
		// mid-request. So the column sets are compared for equality, not
		// containment.
		db, prefix, cleanup := open(t)
		defer cleanup()
		migFS, err := storage.MigrationsFS(vendor)
		if err != nil {
			t.Fatalf("MigrationsFS: %v", err)
		}
		if _, err := migrate.Apply(ctx, db, migFS, vendor, prefix); err != nil {
			t.Fatalf("greenfield Apply: %v", err)
		}

		report, err := migrate.Preflight(ctx, db, vendor, prefix)
		if err != nil {
			t.Fatalf("Preflight: %v", err)
		}
		if !report.OK() {
			t.Errorf("Preflight rejected a database the greenfield set just built; "+
				"migrate.RequiredSchema asks for more than the migrations create: %v", report.Err())
		}

		for _, rt := range migrate.RequiredSchema() {
			actual := columnsOf(ctx, t, db, prefix+rt.Name)
			if !sameStrings(rt.Columns, actual) {
				t.Errorf("migrate.RequiredSchema is out of step with the migrations for %q\n"+
					" RequiredSchema: %v\n"+
					"  actual schema: %v\n"+
					"Update RequiredSchema so Preflight rejects a WordPress database that "+
					"is missing a column grimoire reads.",
					rt.Name, sortedCopy(rt.Columns), sortedCopy(actual))
			}
		}
	})

	t.Run("preflight accepts the stock WordPress fixture", func(t *testing.T) {
		db, prefix, cleanup := open(t)
		defer cleanup()
		newStockWordPressDB(ctx, t, db, vendor, prefix)
		report, err := migrate.Preflight(ctx, db, vendor, prefix)
		if err != nil {
			t.Fatalf("Preflight: %v", err)
		}
		if !report.OK() {
			t.Errorf("Preflight rejected a stock WordPress database: %v", report.Err())
		}
	})

	t.Run("preflight reports missing tables on an empty database", func(t *testing.T) {
		db, prefix, cleanup := open(t)
		defer cleanup()
		report, err := migrate.Preflight(ctx, db, vendor, prefix)
		if err != nil {
			t.Fatalf("Preflight: %v", err)
		}
		if report.OK() {
			t.Fatal("Preflight passed an empty database; it must report the missing WordPress tables")
		}
		if len(report.MissingTables) != len(migrate.RequiredSchema()) {
			t.Errorf("missing tables = %d, want all %d",
				len(report.MissingTables), len(migrate.RequiredSchema()))
		}
		msg := report.Err().Error()
		for _, want := range []string{prefix + "posts", prefix + "users", "table_prefix"} {
			if !strings.Contains(msg, want) {
				t.Errorf("preflight error should mention %q, got: %s", want, msg)
			}
		}
	})

	t.Run("preflight reports a missing column on a table that does exist", func(t *testing.T) {
		db, prefix, cleanup := open(t)
		defer cleanup()
		newStockWordPressDB(ctx, t, db, vendor, prefix)
		// Simulate a WordPress database predating a column grimoire needs.
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf(`ALTER TABLE %sposts DROP COLUMN post_password`, prefix)); err != nil {
			t.Skipf("cannot drop a column on %s to build this fixture: %v", vendor, err)
		}
		report, err := migrate.Preflight(ctx, db, vendor, prefix)
		if err != nil {
			t.Fatalf("Preflight: %v", err)
		}
		if report.OK() {
			t.Fatal("Preflight passed a database missing posts.post_password")
		}
		if len(report.MissingTables) != 0 {
			t.Errorf("missing tables = %v, want none (the table exists, only a column is gone)",
				report.MissingTables)
		}
		got := report.MissingColumns[prefix+"posts"]
		if !sameStrings(got, []string{"post_password"}) {
			t.Errorf("missing columns for %sposts = %v, want [post_password]", prefix, got)
		}
	})

	t.Run("overlay and greenfield define the same sessions table", func(t *testing.T) {
		// The sessions DDL is spelled out in both migration sets. This is what
		// stops them drifting.
		greenfieldDB, greenfieldPrefix, cleanupA := open(t)
		defer cleanupA()
		migFS, err := storage.MigrationsFS(vendor)
		if err != nil {
			t.Fatalf("MigrationsFS: %v", err)
		}
		if _, err := migrate.Apply(ctx, greenfieldDB, migFS, vendor, greenfieldPrefix); err != nil {
			t.Fatalf("greenfield Apply: %v", err)
		}

		overlayDB, overlayPrefix, cleanupB := open(t)
		defer cleanupB()
		newStockWordPressDB(ctx, t, overlayDB, vendor, overlayPrefix)
		overlayFS, err := storage.OverlayMigrationsFS(vendor)
		if err != nil {
			t.Fatalf("OverlayMigrationsFS: %v", err)
		}
		if _, err := migrate.ApplyOverlay(ctx, overlayDB, overlayFS, vendor, overlayPrefix); err != nil {
			t.Fatalf("ApplyOverlay: %v", err)
		}

		fromGreenfield := columnsOf(ctx, t, greenfieldDB, greenfieldPrefix+"sessions")
		fromOverlay := columnsOf(ctx, t, overlayDB, overlayPrefix+"sessions")
		if !sameStrings(fromGreenfield, fromOverlay) {
			t.Errorf("sessions columns differ between the two migration sets\n greenfield: %v\n    overlay: %v\n"+
				"keep 0002_users_auth.up.sql and overlay/0001_grimoire_owned.up.sql in step",
				fromGreenfield, fromOverlay)
		}
	})
}

// newStockWordPressDB turns a fresh database into a stand-in for a real,
// populated WordPress installation: every WordPress table and column grimoire
// reads is present, some rows exist, and none of grimoire's own objects do.
//
// The shape is derived rather than hand-written. Applying the greenfield set
// produces exactly grimoire's required WordPress schema, so dropping the two
// objects WordPress would never have -- {prefix}sessions and the greenfield
// version-tracking table -- leaves precisely what a stock WordPress database
// looks like from grimoire's side. Hand-writing WordPress's DDL three times over
// would only add a second definition to keep in sync.
func newStockWordPressDB(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	migFS, err := storage.MigrationsFS(vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, db, migFS, vendor, prefix); err != nil {
		t.Fatalf("building stock WordPress fixture: %v", err)
	}
	for _, table := range []string{prefix + "sessions", prefix + "schema_migrations"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("dropping %s to simulate a stock WordPress database: %v", table, err)
		}
	}

	// Populate the tables the issue called out: a real adoption target has users
	// and posts already in it, and the overlay run must not disturb them.
	if _, err := db.ExecContext(ctx, placeholders(vendor,
		`INSERT INTO `+prefix+`users (user_login, user_nicename, display_name, user_pass, user_email) VALUES (%s)`, 5),
		"existing_admin", "existing-admin", "Existing Admin", "$P$Bexampleexamplehash", "admin@example.test",
	); err != nil {
		t.Fatalf("seeding fixture users: %v", err)
	}
	if _, err := db.ExecContext(ctx, placeholders(vendor,
		`INSERT INTO `+prefix+`posts (post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_type, comment_status) VALUES (%s)`, 9),
		1, "2024-01-01 00:00:00", "existing body", "Existing Post", "", "publish", "existing-post", "post", "open",
	); err != nil {
		t.Fatalf("seeding fixture posts: %v", err)
	}
}

// insertSession writes one row into the grimoire-owned sessions table, proving it
// is not merely present but usable.
func insertSession(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, placeholders(vendor,
		`INSERT INTO `+prefix+`sessions (id, user_id, csrf_token, created, expires) VALUES (%s)`, 5),
		"overlay-contract-session", 1, "csrf", "2024-01-01 00:00:00", "2099-01-01 00:00:00",
	); err != nil {
		t.Fatalf("inserting into %ssessions: %v", prefix, err)
	}
}

// columnsOf returns table's column names as the driver reports them, via a
// zero-row SELECT *. This works identically on all three vendors, unlike
// information_schema or pragma introspection.
func columnsOf(ctx context.Context, t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+table+" WHERE 1=0")
	if err != nil {
		t.Fatalf("reading columns of %s: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}
	return cols
}

// countRows returns the number of rows in table.
func countRows(ctx context.Context, t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// tableReadable reports whether table can be selected from.
func tableReadable(ctx context.Context, db *sql.DB, table string) bool {
	rows, err := db.QueryContext(ctx, "SELECT 1 FROM "+table+" WHERE 1=0")
	if err != nil {
		return false
	}
	defer rows.Close()
	return rows.Err() == nil
}

// sameStrings compares two string slices as multisets, so a vendor reporting
// columns in a different order does not fail a comparison about which columns
// exist. Comparison is case-insensitive because Postgres folds the mixed-case
// WordPress column names (ID, comment_post_ID) to lower case.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := sortedCopy(a), sortedCopy(b)
	for i := range x {
		if !strings.EqualFold(x[i], y[i]) {
			return false
		}
	}
	return true
}

// sortedCopy returns a case-normalized, sorted copy of in, for stable diff
// output in failure messages.
func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	sort.Strings(out)
	return out
}
