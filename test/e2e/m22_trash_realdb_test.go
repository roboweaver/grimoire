package e2e_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
)

// TestM22TrashRealDBE2E is a READ-ONLY census of trash in a real WordPress
// database (M22 task 6.4a). It uses the same GRIMOIRE_TEST_WP_DSN and
// GRIMOIRE_TEST_WP_PREFIX as the other real-database checks and skips without
// the DSN.
//
// For every row with post_status = 'trash' it checks the representation of
// Requirements 1.1-1.3: the trash meta is present, the "__trashed" suffix is
// present exactly when the desired-slug meta is present, and the recorded
// previous status is a known status. It then reads the row through
// PostRepo.ByID, PostTrasher.TrashInfo and the REST mapper and requires those
// reads not to error.
//
// Representation violations are logged, not failed: the database belongs to a
// real site and WordPress itself leaves such rows behind. A read error fails,
// because the code under test is then unable to read a row WordPress wrote.
//
// Nothing is written. Only SELECT statements run, and TrashInfo opens a
// transaction that contains only SELECTs.
func TestM22TrashRealDBE2E(t *testing.T) {
	dsn := os.Getenv(realWPEnvDSN)
	if dsn == "" {
		t.Skipf("set %s to run the real WordPress trash check", realWPEnvDSN)
	}
	prefix := os.Getenv(realWPEnvPrefix)
	if prefix == "" {
		prefix = realWPDefaultPrefix
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(prefix) {
		t.Fatalf("%s=%q is not a plain table prefix", realWPEnvPrefix, prefix)
	}

	ctx := t.Context()
	repos, err := storage.New(config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix})
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })
	db := repos.DB()

	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, prefix)

	known := map[string]bool{
		"publish": true, "draft": true, "pending": true, "private": true,
		"future": true, "auto-draft": true, "inherit": true,
	}

	type trashed struct {
		id   int64
		typ  string
		name string
	}
	rows, err := db.QueryContext(ctx,
		"SELECT ID, post_type, post_name FROM `"+prefix+"posts` WHERE post_status = 'trash' ORDER BY ID")
	if err != nil {
		t.Fatalf("list trashed rows: %v", err)
	}
	var all []trashed
	for rows.Next() {
		var r trashed
		if err := rows.Scan(&r.id, &r.typ, &r.name); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("list trashed rows: %v", err)
	}
	rows.Close()

	perType := map[string]int{}
	violations := 0
	readErrors := 0
	for _, r := range all {
		perType[r.typ]++
		violate := func(format string, args ...any) {
			violations++
			t.Logf("violation: post %d (%s): %s", r.id, r.typ, fmt.Sprintf(format, args...))
		}

		info, err := repos.PostTrash.TrashInfo(ctx, r.id)
		if err != nil {
			readErrors++
			t.Errorf("TrashInfo(%d): %v", r.id, err)
			continue
		}
		post, err := repos.PostWriter.ByID(ctx, r.id)
		if err != nil {
			readErrors++
			t.Errorf("PostRepo.ByID(%d): %v", r.id, err)
			continue
		}
		if err := realWPMapTrashed(ctx, mapper, post); err != nil {
			readErrors++
			t.Errorf("REST mapper(%d, %s): %v", r.id, r.typ, err)
		}

		if !info.HasPreviousStatus {
			violate("_wp_trash_meta_status missing")
		} else if !known[info.PreviousStatus] {
			violate("_wp_trash_meta_status %q is not a known status", info.PreviousStatus)
		}
		if !info.HasTrashedAt {
			violate("_wp_trash_meta_time missing or not an integer")
		}
		suffixed := strings.HasSuffix(r.name, "__trashed")
		if suffixed != info.HasDesiredSlug {
			violate("post_name %q suffix=%v but _wp_desired_post_slug present=%v",
				r.name, suffixed, info.HasDesiredSlug)
		}
	}

	// Residue that WordPress or an earlier tool left on rows that are not in the
	// trash. Counted, never failed.
	liveSuffixed := realWPCount(t, db,
		"SELECT COUNT(*) FROM `"+prefix+"posts` WHERE post_status <> 'trash' AND post_name LIKE '%\\_\\_trashed'")
	liveMeta := realWPCount(t, db,
		"SELECT COUNT(*) FROM `"+prefix+"postmeta` m JOIN `"+prefix+"posts` p ON p.ID = m.post_id "+
			"WHERE p.post_status <> 'trash' AND m.meta_key LIKE '\\_wp\\_trash\\_meta\\_%'")

	types := make([]string, 0, len(perType))
	for typ := range perType {
		types = append(types, typ)
	}
	sort.Strings(types)
	parts := make([]string, 0, len(types))
	for _, typ := range types {
		parts = append(parts, fmt.Sprintf("%s=%d", typ, perType[typ]))
	}
	if len(parts) == 0 {
		parts = append(parts, "none")
	}
	t.Logf("M22 real-DB trash census: prefix=%s trashed=%d [%s] violations=%d read_errors=%d "+
		"non_trash_with___trashed_slug=%d non_trash_with_trash_meta=%d",
		prefix, len(all), strings.Join(parts, " "), violations, readErrors, liveSuffixed, liveMeta)
}

// realWPMapTrashed runs a trashed row through the REST mapper. A page uses the
// page mapping; every other type uses the post mapping, which maps any
// domain.Post (registered custom types need a registry this check does not
// build).
func realWPMapTrashed(ctx context.Context, m *content.RESTMapper, p domain.Post) error {
	if p.Type == "page" {
		_, err := m.Page(ctx, p)
		return err
	}
	_, err := m.Post(ctx, p)
	return err
}

func realWPCount(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
