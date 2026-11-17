package wprepo

import (
	"context"
	"testing"
)

// TestPurgeMenuItemsOfAtomicOnFailure covers Req 9.6: when a statement fails
// (term_taxonomy is dropped), the method returns an error and no item, postmeta
// or relationship row is changed. The recount is the last statement, so the
// deletes must be rolled back. SQLite only: no portable failure injection exists.
func TestPurgeMenuItemsOfAtomicOnFailure(t *testing.T) {
	ctx := context.Background()
	repo := newPostDependentsRepo(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	// Page 1, menu term_taxonomy 7, one menu item 2 pointing at page 1.
	exec(`INSERT INTO wp_posts ("ID", post_type, post_status) VALUES (1, 'page', 'publish')`)
	exec(`INSERT INTO wp_posts ("ID", post_type, post_status) VALUES (2, 'nav_menu_item', 'publish')`)
	for _, m := range [][2]string{
		{"_menu_item_type", "post_type"}, {"_menu_item_object", "page"},
		{"_menu_item_object_id", "1"}, {"_menu_item_menu_item_parent", "0"},
	} {
		exec(`INSERT INTO wp_postmeta (post_id, meta_key, meta_value) VALUES (2, ?, ?)`, m[0], m[1])
	}
	exec(`INSERT INTO wp_terms (term_id, name, slug) VALUES (6, 'm', 'm')`)
	exec(`INSERT INTO wp_term_taxonomy (term_taxonomy_id, term_id, taxonomy, count) VALUES (7, 6, 'nav_menu', 1)`)
	exec(`INSERT INTO wp_term_relationships (object_id, term_taxonomy_id) VALUES (2, 7)`)
	exec(`DROP TABLE wp_term_taxonomy`)

	if err := repo.PurgeMenuItemsOf(ctx, 1, "page"); err == nil {
		t.Fatal("PurgeMenuItemsOf with term_taxonomy dropped = nil, want an error")
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := repo.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM wp_posts WHERE "ID" = 2`); n != 1 {
		t.Errorf("menu item rows = %d, want 1 (rolled back)", n)
	}
	if n := count(`SELECT COUNT(*) FROM wp_postmeta WHERE post_id = 2`); n != 4 {
		t.Errorf("menu item postmeta rows = %d, want 4 (rolled back)", n)
	}
	if n := count(`SELECT COUNT(*) FROM wp_term_relationships WHERE object_id = 2`); n != 1 {
		t.Errorf("menu item relationship rows = %d, want 1 (rolled back)", n)
	}
}

// TestPurgeMenuItemsOfReparentAtomicOnFailure covers M20 atomicity: a deleted
// item has a child, so the purge re-points the child before its recount fails
// (term_taxonomy is dropped). The error must roll the re-parent back, leaving the
// child's parent, every item, postmeta and relationship row unchanged. SQLite
// only: no portable failure injection exists.
func TestPurgeMenuItemsOfReparentAtomicOnFailure(t *testing.T) {
	ctx := context.Background()
	repo := newPostDependentsRepo(t)
	p := repo.prefix
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	// Page 1; item 2 points at page 1 (top level); item 3 is a custom child of 2.
	exec(`INSERT INTO ` + p + `posts ("ID", post_type, post_status) VALUES (1, 'page', 'publish')`)
	for _, id := range []string{"2", "3"} {
		exec(`INSERT INTO ` + p + `posts ("ID", post_type, post_status) VALUES (` + id + `, 'nav_menu_item', 'publish')`)
	}
	for _, m := range [][3]string{
		{"2", "_menu_item_type", "post_type"}, {"2", "_menu_item_object", "page"},
		{"2", "_menu_item_object_id", "1"}, {"2", "_menu_item_menu_item_parent", "0"},
		{"3", "_menu_item_type", "custom"}, {"3", "_menu_item_object", "custom"},
		{"3", "_menu_item_object_id", "0"}, {"3", "_menu_item_menu_item_parent", "2"},
	} {
		exec(`INSERT INTO `+p+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, m[0], m[1], m[2])
	}
	exec(`INSERT INTO ` + p + `terms (term_id, name, slug) VALUES (6, 'm', 'm')`)
	exec(`INSERT INTO ` + p + `term_taxonomy (term_taxonomy_id, term_id, taxonomy, count) VALUES (7, 6, 'nav_menu', 2)`)
	exec(`INSERT INTO ` + p + `term_relationships (object_id, term_taxonomy_id) VALUES (2, 7)`)
	exec(`INSERT INTO ` + p + `term_relationships (object_id, term_taxonomy_id) VALUES (3, 7)`)
	exec(`DROP TABLE ` + p + `term_taxonomy`)

	if err := repo.PurgeMenuItemsOf(ctx, 1, "page"); err == nil {
		t.Fatal("PurgeMenuItemsOf with term_taxonomy dropped = nil, want an error")
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := repo.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM ` + p + `posts WHERE "ID" IN (2, 3)`); n != 2 {
		t.Errorf("menu item rows = %d, want 2 (rolled back)", n)
	}
	if n := count(`SELECT COUNT(*) FROM ` + p + `postmeta WHERE post_id IN (2, 3)`); n != 8 {
		t.Errorf("menu item postmeta rows = %d, want 8 (rolled back)", n)
	}
	if n := count(`SELECT COUNT(*) FROM ` + p + `term_relationships WHERE object_id IN (2, 3)`); n != 2 {
		t.Errorf("menu item relationship rows = %d, want 2 (rolled back)", n)
	}
	if n := count(`SELECT COUNT(*) FROM ` + p + `postmeta WHERE post_id = 3 AND meta_key = '_menu_item_menu_item_parent' AND meta_value = '2'`); n != 1 {
		t.Errorf("child still names its original parent: rows = %d, want 1 (re-parent rolled back)", n)
	}
}
