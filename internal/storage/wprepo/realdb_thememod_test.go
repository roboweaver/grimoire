package wprepo

import (
	"database/sql"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// TestRealWordPressThemeMods runs the M23 theme-mod decision logic over the
// REAL theme_mods_* rows of a WordPress database, read-only. It uses the same
// single gate as the other real-database tests: GRIMOIRE_TEST_WP_DSN and
// GRIMOIRE_TEST_WP_PREFIX (no new variable).
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/storage/wprepo -run TestRealWordPressThemeMods -v -count=1
//
// For every row the scanner must either read the value, or the value must have
// a string whose declared length does not match its bytes. PHP's unserialize
// cannot read such a value either (a naive host search-and-replace on a
// migrated database produces exactly this), so the cleaner skipping it is
// correct; any other unreadable row is a scanner bug and fails the test. Rows
// with a length mismatch are also checked in a copy whose lengths are repaired
// in memory, so the real object-valued header_image_data shapes are exercised.
//
// For every header_image / background_image that is a real URL it asserts that
// (a) an attachment whose URL has the same path (relative form, as grimoire
// builds it) removes exactly that key, plus header_image_data for the header,
// and leaves every other byte alone, and (b) a different path changes nothing.
// Non-URL sentinels such as "remove-header" never match. Only SELECTs are run.
func TestRealWordPressThemeMods(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress theme-mod validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if !regexp.MustCompile(`^[A-Za-z0-9_]*$`).MatchString(prefix) {
		t.Fatalf("GRIMOIRE_TEST_WP_PREFIX %q is not a plain table prefix", prefix)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT option_name, option_value FROM " + prefix + "options WHERE option_name LIKE 'theme\\_mods\\_%' ORDER BY option_name")
	if err != nil {
		t.Fatalf("select theme mods: %v", err)
	}
	defer rows.Close()

	var total, scanned, malformed, repaired, withHeader, withBackground, objectValued, urlChecks, sentinelChecks int

	checkRow := func(name, raw string) {
		entries, ok := scanPHPArray(raw)
		if !ok {
			t.Errorf("%s: unreadable: %s", name, failurePoint(raw))
			return
		}
		if strings.Contains(raw, ";O:") || strings.Contains(raw, "{O:") {
			objectValued++
		}
		byKey := map[string]phpEntry{}
		for _, e := range entries {
			if e.strKey {
				byKey[e.key] = e
			}
		}
		for _, key := range []string{modHeaderImage, modBackgroundImage} {
			e, has := byKey[key]
			if !has {
				continue
			}
			if key == modHeaderImage {
				withHeader++
			} else {
				withBackground++
			}
			val, ok := phpEntryString(raw, e)
			if !ok {
				t.Errorf("%s: %s is not a string entry", name, key)
				continue
			}
			u, perr := url.Parse(val)
			if val == "" || perr != nil || u.Path == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
				sentinelChecks++
				d := decideThemeModCleanup(raw, nil, 4242, "/wp-content/uploads/"+val)
				if d.ModsChanged || d.Mods != raw {
					t.Errorf("%s: %s=%q is not a URL but the cleaner changed the value", name, key, val)
				}
				continue
			}
			urlChecks++
			d := decideThemeModCleanup(raw, nil, 4242, u.Path)
			if !d.ModsChanged {
				t.Errorf("%s: %s=%q was not matched by its own path %q", name, key, val, u.Path)
				continue
			}
			remove := map[string]bool{key: true}
			if key == modHeaderImage {
				remove[modHeaderImageData] = true
			}
			var kept strings.Builder
			left := 0
			for _, en := range entries {
				if en.strKey && remove[en.key] {
					continue
				}
				kept.WriteString(raw[en.start:en.end])
				left++
			}
			want := ""
			if left > 0 {
				want = "a:" + strconv.Itoa(left) + ":{" + kept.String() + "}"
			}
			if d.Mods != want {
				t.Errorf("%s: %s cleanup removed more or less than the expected spans", name, key)
			}
			if left > 0 {
				if _, ok := scanPHPArray(d.Mods); !ok {
					t.Errorf("%s: cleaned value is no longer a well-formed serialized array", name)
				}
			}
			if d2 := decideThemeModCleanup(raw, nil, 4242, u.Path+"x"); d2.ModsChanged || d2.Mods != raw {
				t.Errorf("%s: a different path changed %s", name, key)
			}
		}
	}

	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		total++
		if _, ok := scanPHPArray(raw); ok {
			scanned++
			checkRow(name, raw)
			continue
		}
		if !hasLengthMismatch(raw) {
			t.Errorf("%s: scanner could not read a real WordPress value and no string length mismatch explains it: %s", name, failurePoint(raw))
			continue
		}
		malformed++
		t.Logf("%s: skipped as malformed (a string's declared length does not match its bytes; PHP could not unserialize it either): %s", name, failurePoint(raw))
		d := decideThemeModCleanup(raw, nil, 4242, "/wp-content/uploads/anything.png")
		if d.ModsChanged || d.Mods != raw {
			t.Errorf("%s: a malformed value must be left byte-identical", name)
		}
		fixed := repairStringLengths(raw)
		if _, ok := scanPHPArray(fixed); !ok {
			t.Errorf("%s: still unreadable after repairing string lengths in memory: %s", name, failurePoint(fixed))
			continue
		}
		repaired++
		checkRow(name+" (lengths repaired in memory)", fixed)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if total == 0 {
		t.Skip("no theme_mods_* rows in this database")
	}
	t.Logf("M23 real-DB theme-mod census: prefix=%s rows=%d scanned=%d malformed_skipped=%d repaired_copies_checked=%d with_header_image=%d with_background_image=%d object_valued=%d url_match_checks=%d sentinel_checks=%d",
		prefix, total, scanned, malformed, repaired, withHeader, withBackground, objectValued, urlChecks, sentinelChecks)
}

var serializedStr = regexp.MustCompile(`s:(\d+):"([^"]*)"`)

// hasLengthMismatch reports whether some quote-free serialized string declares
// a length that differs from its byte length.
func hasLengthMismatch(raw string) bool {
	for _, m := range serializedStr.FindAllStringSubmatch(raw, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n != len(m[2]) {
			return true
		}
	}
	return false
}

// repairStringLengths rewrites every quote-free serialized string's length
// prefix to its byte length. It is only used on an in-memory copy.
func repairStringLengths(raw string) string {
	return serializedStr.ReplaceAllStringFunc(raw, func(s string) string {
		m := serializedStr.FindStringSubmatch(s)
		return "s:" + strconv.Itoa(len(m[2])) + `:"` + m[2] + `"`
	})
}

// failurePoint reports where scanning a top-level array first breaks, so a
// failure on real data is diagnosable without dumping the whole option.
func failurePoint(raw string) string {
	n, pos, ok := phpOpenAggregate(raw, 0, 'a')
	if !ok {
		return "not an a:N:{ array header"
	}
	for i := 0; i < n; i++ {
		_, next, ok := phpScanEntry(raw, pos, 1)
		if !ok {
			end := pos + 60
			if end > len(raw) {
				end = len(raw)
			}
			return "entry " + strconv.Itoa(i) + " of " + strconv.Itoa(n) + " fails at byte " + strconv.Itoa(pos) + ": " + strconv.Quote(raw[pos:end])
		}
		pos = next
	}
	return "all entries scan; trailing bytes or missing closing brace at " + strconv.Itoa(pos)
}
