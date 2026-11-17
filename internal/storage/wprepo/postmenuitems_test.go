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
