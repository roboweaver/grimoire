package mysql_test

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"

	mysqlmigrations "github.com/roboweaver/grimoire/internal/storage/migrations/mysql"
)

// A prefix index cannot be longer than the column it indexes; MySQL rejects it
// with "Error 1089 (HY000): Incorrect prefix key". That is a static property of
// the SQL, so it is checked here with no database rather than only surfacing in
// the DSN-gated MySQL contract run.
//
// This exists because exactly that defect shipped: 0003 declared
// KEY comment_author_email (comment_author_email(191)) on a VARCHAR(100)
// column, which failed the fixture build for the ENTIRE MySQL contract suite
// (~60 subtests). It went unnoticed for two milestones because CI runs
// `go test` against SQLite only, and the MySQL run is gated behind
// GRIMOIRE_TEST_MYSQL_DSN, which CI does not set.
//
// The 191 is the utf8mb4 767-byte index-limit workaround (767/4), legitimate on
// the VARCHAR(255) columns it was copied from and invalid on anything narrower.
// A per-file, per-index check catches the next copy of it in CI, with no MySQL.
var (
	varcharRe = regexp.MustCompile(`(?im)^\s*` + "`?" + `(\w+)` + "`?" + `\s+VARCHAR\((\d+)\)`)
	prefixRe  = regexp.MustCompile("(?i)KEY\\s+`?[\\w]+`?\\s*\\(\\s*`?(\\w+)`?\\((\\d+)\\)")
)

func TestPrefixKeysFitTheirColumns(t *testing.T) {
	entries, err := fs.Glob(mysqlmigrations.FS(), "*.up.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations found; the embed pattern or package layout changed")
	}

	checked := 0
	for _, name := range entries {
		body, err := fs.ReadFile(mysqlmigrations.FS(), name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		sql := string(body)

		// VARCHAR widths declared anywhere in this file. Migrations are
		// self-contained per file, so a prefix key referencing a column
		// declared elsewhere is reported rather than silently skipped.
		widths := map[string]int{}
		for _, m := range varcharRe.FindAllStringSubmatch(sql, -1) {
			n, convErr := strconv.Atoi(m[2])
			if convErr != nil {
				continue
			}
			widths[strings.ToLower(m[1])] = n
		}

		for _, m := range prefixRe.FindAllStringSubmatch(sql, -1) {
			col := strings.ToLower(m[1])
			prefix, convErr := strconv.Atoi(m[2])
			if convErr != nil {
				t.Errorf("%s: unparseable prefix length %q on %s", name, m[2], col)
				continue
			}
			checked++
			width, ok := widths[col]
			if !ok {
				// Not a failure on its own: the column may be a TEXT/BLOB type,
				// which legitimately requires a prefix. Surface it so a genuine
				// cross-file reference is not hidden.
				t.Logf("%s: prefix key on %s(%d) — no VARCHAR width found in this "+
					"file (TEXT/BLOB column, or declared elsewhere)", name, col, prefix)
				continue
			}
			if prefix > width {
				t.Errorf("%s: KEY on %s(%d) exceeds VARCHAR(%d) — MySQL rejects this "+
					"with Error 1089. Use a prefix <= %d, or match WordPress core's "+
					"own length for this column.", name, col, prefix, width, width)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no prefix keys examined; the detection regex has stopped matching")
	}
	t.Logf("validated %d prefix keys across %d migrations", checked, len(entries))
}

// Guards the guard: a known-bad snippet must be reported, so a future regex
// change cannot silently turn this test into a no-op that always passes.
func TestPrefixKeyDetectionCatchesKnownBadShape(t *testing.T) {
	bad := "CREATE TABLE x (\n  email VARCHAR(100) NOT NULL DEFAULT '',\n  KEY email (email(191))\n);"
	widths := map[string]int{}
	for _, m := range varcharRe.FindAllStringSubmatch(bad, -1) {
		n, _ := strconv.Atoi(m[2])
		widths[strings.ToLower(m[1])] = n
	}
	matches := prefixRe.FindAllStringSubmatch(bad, -1)
	if len(matches) != 1 {
		t.Fatalf("prefix regex matched %d times, want 1", len(matches))
	}
	col := strings.ToLower(matches[0][1])
	prefix, _ := strconv.Atoi(matches[0][2])
	if got, want := widths[col], 100; got != want {
		t.Fatalf("width for %s = %d, want %d", col, got, want)
	}
	if prefix <= widths[col] {
		t.Fatalf("prefix %d not detected as exceeding width %d", prefix, widths[col])
	}
	_ = fmt.Sprint(prefix)
}
