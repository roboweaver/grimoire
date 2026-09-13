package e2e_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// testBusyTimeoutMS is the busy_timeout applied to every e2e SQLite database,
// matching configs/grimoire.sqlite.yaml's shipped DSN.
const testBusyTimeoutMS = 5000

// testDSN returns a SQLite DSN for a fresh database in the test's temp
// directory, with the same busy_timeout pragma the shipped configuration uses.
//
// The pragma is not cosmetic. SQLite allows a single writer, and a connection
// with busy_timeout=0 -- the driver default -- fails immediately with
// SQLITE_BUSY rather than waiting when it finds the database locked. Any test
// with a background writer therefore races its own goroutine.
//
// TestM7SchedulerWiring hit exactly that: M7's publish scheduler ticks on its
// own interval while the test polls the same database, and on a loaded CI
// runner the two overlapped and the read failed with
// "database is locked (5) (SQLITE_BUSY)". It was the only e2e test affected
// because it is the only one with a background writer, and it passed locally
// because the window is timing-dependent.
//
// Every e2e test builds its DSN through this helper so a future test with a
// background writer inherits the timeout instead of rediscovering the flake.
func testDSN(t *testing.T) string {
	t.Helper()
	return "file:" + filepath.Join(t.TempDir(), "grimoire.db") +
		"?_pragma=busy_timeout(5000)"
}

// TestDSNSetsBusyTimeout guards the helper itself: if the pragma is ever
// dropped from the DSN, tests would keep passing locally and only fail
// intermittently on CI, which is the failure mode this replaced. Asserting the
// effective value makes that regression loud and immediate.
func TestDSNSetsBusyTimeout(t *testing.T) {
	db, err := sql.Open("sqlite", testDSN(t))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	var got int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if got != testBusyTimeoutMS {
		t.Fatalf("busy_timeout = %d, want %d; a connection at 0 fails "+
			"immediately with SQLITE_BUSY instead of waiting", got, testBusyTimeoutMS)
	}
}
