package content

import (
	"database/sql"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// TestRealWordPressMediaMetadata runs the M23 media file-candidate logic over
// the REAL _wp_attachment_metadata and _wp_attachment_backup_sizes rows of a
// WordPress database, read-only, under the same single gate as the other
// real-database tests: GRIMOIRE_TEST_WP_DSN and GRIMOIRE_TEST_WP_PREFIX.
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/content -run TestRealWordPressMediaMetadata -v -count=1
//
// For every attachment it asserts that the metadata decodes, or that an
// undecodable value has a string whose declared length does not match its
// bytes (PHP could not unserialize it either, so skipping it is correct). It
// then checks the candidates: none is empty or contains a NUL byte, and any that
// contains a path separator or ".." is counted and logged (WordPress stores
// size files as bare names next to the main file; deleteMediaFiles contains
// anything else inside the uploads directory regardless). Only SELECTs are run.
func TestRealWordPressMediaMetadata(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress media metadata validation")
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

	q := "SELECT p.ID, m.meta_value, COALESCE((SELECT b.meta_value FROM " + prefix + "postmeta b WHERE b.post_id=p.ID AND b.meta_key='_wp_attachment_backup_sizes' ORDER BY b.meta_id LIMIT 1), '')" +
		" FROM " + prefix + "posts p JOIN " + prefix + "postmeta m ON m.post_id=p.ID AND m.meta_key='_wp_attachment_metadata'" +
		" WHERE p.post_type='attachment' ORDER BY p.ID"
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("select metadata: %v", err)
	}
	defer rows.Close()

	lenMismatch := regexp.MustCompile(`s:(\d+):"([^"]*)"`)
	mismatch := func(raw string) bool {
		for _, m := range lenMismatch.FindAllStringSubmatch(raw, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n != len(m[2]) {
				return true
			}
		}
		return false
	}

	hasFloat := regexp.MustCompile(`;d:-?[0-9]`)
	var undecodableFloat int
	var total, decoded, undecodable, withSizes, withBackup, noCandidates, odd, totalCandidates, maxCandidates int
	oddSamples := []string{}
	for rows.Next() {
		var id int64
		var meta, backup string
		if err := rows.Scan(&id, &meta, &backup); err != nil {
			t.Fatalf("scan: %v", err)
		}
		total++
		top, ok := mediaDecodeMap(meta)
		if !ok {
			undecodable++
			if hasFloat.MatchString(meta) {
				undecodableFloat++
			}
			if !mismatch(meta) {
				t.Errorf("attachment %d: metadata does not decode and no string length mismatch explains it (%d bytes)", id, len(meta))
			}
			continue
		}
		decoded++
		if _, has := top["sizes"].(map[string]any); has {
			withSizes++
		}
		if backup != "" {
			withBackup++
			if _, ok := mediaDecodeMap(backup); !ok && !mismatch(backup) {
				t.Errorf("attachment %d: backup sizes do not decode and no string length mismatch explains it", id)
			}
		}
		c := mediaFileCandidates(meta, backup)
		totalCandidates += len(c)
		if len(c) > maxCandidates {
			maxCandidates = len(c)
		}
		if sizes, has := top["sizes"].(map[string]any); has && len(sizes) > 0 && len(c) == 0 {
			noCandidates++
		}
		for _, f := range c {
			if f == "" || strings.ContainsRune(f, 0) {
				t.Errorf("attachment %d: empty or NUL-containing candidate %q", id, f)
			}
			if strings.ContainsAny(f, `/\`) || strings.Contains(f, "..") {
				odd++
				if len(oddSamples) < 5 {
					oddSamples = append(oddSamples, strconv.Quote(f))
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if total == 0 {
		t.Skip("no attachment metadata rows in this database")
	}
	if noCandidates > 0 {
		t.Errorf("%d attachments have a non-empty sizes map but yielded no candidates", noCandidates)
	}
	t.Logf("M23 real-DB media census: prefix=%s attachments_with_metadata=%d decoded=%d undecodable=%d (with_float=%d) with_sizes=%d with_backup_sizes=%d candidates_total=%d max_per_attachment=%d candidates_with_separator_or_dotdot=%d %v",
		prefix, total, decoded, undecodable, undecodableFloat, withSizes, withBackup, totalCandidates, maxCandidates, odd, oddSamples)
}
