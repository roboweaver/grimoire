package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Shared fixture for the M18 (write hardening) tests. Task 1.1 introduces it.
// Raw rows are inserted with the SQL handle so tests can create rows the API
// cannot (an imported shop_order, an attachment, a nav_menu_item).

// hardeningRawDate is the fixed date every raw row carries.
const hardeningRawDate = "2024-01-02 03:04:05"

// seedRawPost inserts a posts row of the given type, status and parent with
// fixed dates and author 1, and returns its ID.
func seedRawPost(t *testing.T, env customWriteEnv, typ, status string, parent int64) int64 {
	t.Helper()
	res, err := env.Repos.DB().ExecContext(context.Background(),
		"INSERT INTO "+env.Prefix+"posts (post_author, post_date, post_date_gmt, post_modified, post_modified_gmt, "+
			"post_content, post_title, post_excerpt, post_status, post_name, post_type, post_parent, guid) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		1, hardeningRawDate, hardeningRawDate, hardeningRawDate, hardeningRawDate,
		"raw content", "Raw "+typ, "", status, "raw-"+typ, typ, parent, "")
	if err != nil {
		t.Fatalf("seedRawPost %s: %v", typ, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seedRawPost %s id: %v", typ, err)
	}
	return id
}

// seedRawMeta inserts one postmeta row for postID.
func seedRawMeta(t *testing.T, env customWriteEnv, postID int64, key, value string) {
	t.Helper()
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"INSERT INTO "+env.Prefix+"postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)",
		postID, key, value); err != nil {
		t.Fatalf("seedRawMeta: %v", err)
	}
}

// seedRawComment inserts a comment on postID with one commentmeta row and
// returns the comment ID.
func seedRawComment(t *testing.T, env customWriteEnv, postID int64, approved string) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := env.Repos.DB().ExecContext(ctx,
		"INSERT INTO "+env.Prefix+"comments (comment_post_ID, comment_author, comment_date, comment_date_gmt, comment_content, comment_approved) "+
			"VALUES (?, ?, ?, ?, ?, ?)",
		postID, "Raw Commenter", hardeningRawDate, hardeningRawDate, "raw comment", approved)
	if err != nil {
		t.Fatalf("seedRawComment: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seedRawComment id: %v", err)
	}
	if _, err := env.Repos.DB().ExecContext(ctx,
		"INSERT INTO "+env.Prefix+"commentmeta (comment_id, meta_key, meta_value) VALUES (?, ?, ?)",
		id, "raw_key", "raw_value"); err != nil {
		t.Fatalf("seedRawComment meta: %v", err)
	}
	return id
}

// seedRawTermRelationship inserts a term_relationships row linking postID to
// the term_taxonomy row termID.
func seedRawTermRelationship(t *testing.T, env customWriteEnv, postID, termID int64) {
	t.Helper()
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"INSERT INTO "+env.Prefix+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)",
		postID, termID, 0); err != nil {
		t.Fatalf("seedRawTermRelationship: %v", err)
	}
}

// rowSnapshot returns every posts column of row id as one comparable string
// (column=value pairs, in column order), or "<missing>" if the row is gone.
func rowSnapshot(t *testing.T, env customWriteEnv, id int64) string {
	t.Helper()
	rows, err := env.Repos.DB().QueryContext(context.Background(),
		"SELECT * FROM "+env.Prefix+"posts WHERE "+postsIDColumn+" = ?", id)
	if err != nil {
		t.Fatalf("rowSnapshot query: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("rowSnapshot columns: %v", err)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatalf("rowSnapshot: %v", err)
		}
		return "<missing>"
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		t.Fatalf("rowSnapshot scan: %v", err)
	}
	var b strings.Builder
	for i, c := range cols {
		v := vals[i]
		if bs, ok := v.([]byte); ok {
			v = string(bs)
		}
		fmt.Fprintf(&b, "%s=%v;", c, v)
	}
	return b.String()
}

// postsIDColumn is the quoted primary-key column of the posts table.
const postsIDColumn = `"ID"`

// M19 additions (task 1.1): raw term and menu-item seeding and a term snapshot.

// seedRawTerm inserts a terms row and a term_taxonomy row (term_taxonomy_id =
// term_id) for taxonomy with the given parent and returns the term ID. The
// taxonomy need not be managed, so tests can create nav_menu, product_cat and
// link_category terms the API cannot.
func seedRawTerm(t *testing.T, env customWriteEnv, name, slug, taxonomy string, parent int64) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := env.Repos.DB().ExecContext(ctx,
		"INSERT INTO "+env.Prefix+"terms (name, slug) VALUES (?, ?)", name, slug)
	if err != nil {
		t.Fatalf("seedRawTerm %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seedRawTerm %q id: %v", name, err)
	}
	if _, err := env.Repos.DB().ExecContext(ctx,
		"INSERT INTO "+env.Prefix+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
		id, id, taxonomy, "", parent, 0); err != nil {
		t.Fatalf("seedRawTerm %q taxonomy: %v", name, err)
	}
	return id
}

// termSnapshot returns the terms, term_taxonomy and term_relationships rows of
// term termID as one comparable string, or "<missing>" parts for absent rows.
func termSnapshot(t *testing.T, env customWriteEnv, termID int64) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []struct{ label, query string }{
		{"terms", "SELECT * FROM " + env.Prefix + "terms WHERE term_id = ? ORDER BY term_id"},
		{"term_taxonomy", "SELECT * FROM " + env.Prefix + "term_taxonomy WHERE term_id = ? ORDER BY term_taxonomy_id"},
		{"term_relationships", "SELECT * FROM " + env.Prefix + "term_relationships WHERE term_taxonomy_id IN " +
			"(SELECT term_taxonomy_id FROM " + env.Prefix + "term_taxonomy WHERE term_id = ?) ORDER BY object_id, term_taxonomy_id"},
	} {
		fmt.Fprintf(&b, "[%s]", q.label)
		rows, err := env.Repos.DB().QueryContext(context.Background(), q.query, termID)
		if err != nil {
			t.Fatalf("termSnapshot %s: %v", q.label, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatalf("termSnapshot %s columns: %v", q.label, err)
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatalf("termSnapshot %s scan: %v", q.label, err)
			}
			for i, c := range cols {
				v := vals[i]
				if bs, ok := v.([]byte); ok {
					v = string(bs)
				}
				fmt.Fprintf(&b, "%s=%v;", c, v)
			}
			b.WriteString("|")
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("termSnapshot %s rows: %v", q.label, err)
		}
		rows.Close()
	}
	return b.String()
}

// seedRawMenuItem inserts a nav_menu_item post with the _menu_item_* postmeta
// pointing at (objectType, objectID), links it to menuTermID, and returns the
// item's post ID. It is used from PR 3.
func seedRawMenuItem(t *testing.T, env customWriteEnv, menuTermID int64, objectType string, objectID int64) int64 {
	t.Helper()
	item := seedRawPost(t, env, "nav_menu_item", "publish", 0)
	kind := "post_type"
	if objectType == "category" || objectType == "post_tag" {
		kind = "taxonomy"
	}
	seedRawMeta(t, env, item, "_menu_item_type", kind)
	seedRawMeta(t, env, item, "_menu_item_object", objectType)
	seedRawMeta(t, env, item, "_menu_item_object_id", fmt.Sprint(objectID))
	seedRawMeta(t, env, item, "_menu_item_menu_item_parent", "0")
	seedRawTermRelationship(t, env, item, menuTermID)
	return item
}

// seedRawMenuItemWithParent is seedRawMenuItem with _menu_item_menu_item_parent
// set to parentItemID (0 keeps the item top level). M20 task 3.1.
func seedRawMenuItemWithParent(t *testing.T, env customWriteEnv, menuTermID int64, objectType string, objectID, parentItemID int64) int64 {
	t.Helper()
	item := seedRawMenuItem(t, env, menuTermID, objectType, objectID)
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"UPDATE "+env.Prefix+"postmeta SET meta_value = ? WHERE post_id = ? AND meta_key = '_menu_item_menu_item_parent'",
		fmt.Sprint(parentItemID), item); err != nil {
		t.Fatalf("seedRawMenuItemWithParent: set parent of %d: %v", item, err)
	}
	return item
}
