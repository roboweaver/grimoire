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
