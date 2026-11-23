package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
)

// M22 task 6.1: assembly tests for trash and restore, through the real
// buildHandler. They fail until task 6.2 wires WithPostTrasher, WithTrashDays
// and Server.WithPostTrash in assembly.go: a non-force REST DELETE then answers
// 501 rest_trash_not_supported instead of moving the row to the trash.

// trashCase is one REST surface that trashes: a built-in post, a built-in page
// and a registered-type row.
type trashCase struct {
	name, typ, base string
	actor           func(customWriteEnv) *customWriteActor
}

func trashCases() []trashCase {
	admin := func(e customWriteEnv) *customWriteActor { return &e.Admin }
	products := func(e customWriteEnv) *customWriteActor { return &e.Products }
	return []trashCase{
		{"post", "post", "/wp-json/wp/v2/posts", admin},
		{"page", "page", "/wp-json/wp/v2/pages", admin},
		{"registered type", "product", "/wp-json/wp/v2/product", products},
	}
}

// trashFixture is the row under test and everything that hangs off it.
type trashFixture struct {
	id, cat, comment1, comment0, child, menu, item int64
}

// seedTrashFixture creates a published row "Subject" (slug subject) and gives
// it a category relationship (count 1), an approved and a pending comment, an
// unrelated postmeta row, a child, a menu item and a revision.
func seedTrashFixture(t *testing.T, env customWriteEnv, c trashCase) trashFixture {
	t.Helper()
	var f trashFixture
	f.id = createRESTRow(t, env, c.actor(env), c.base, "Subject")
	f.cat = seedRawTerm(t, env, "Cat", "cat", "category", 0)
	seedRawTermRelationship(t, env, f.id, f.cat)
	trashExec(t, env, "UPDATE "+env.Prefix+"term_taxonomy SET count = 1 WHERE term_taxonomy_id = ?", f.cat)
	f.comment1 = seedRawComment(t, env, f.id, "1")
	f.comment0 = seedRawComment(t, env, f.id, "0")
	seedRawMeta(t, env, f.id, "keep_key", "keep")
	f.child = seedRawPost(t, env, c.typ, "publish", f.id)
	f.menu = createMenu(t, env, "Trash Menu", "trash-menu")
	f.item = seedRawMenuItem(t, env, f.menu, c.typ, f.id)
	// An update snapshots a revision of the row.
	trashWant(t, doJSON(t, env.Handler, c.actor(env), http.MethodPatch, fmt.Sprintf("%s/%d", c.base, f.id), `{"title":"Subject"}`),
		http.StatusOK, "seed revision")
	if c.typ == "post" {
		stickySeedOption(t, env, stickyList(f.id))
	}
	return f
}

func trashExec(t *testing.T, env customWriteEnv, query string, args ...any) {
	t.Helper()
	if _, err := env.Repos.DB().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// trashString returns the first column of a one-row query, or "<none>".
func trashString(t *testing.T, env customWriteEnv, query string, args ...any) string {
	t.Helper()
	var v sql.NullString
	err := env.Repos.DB().QueryRowContext(context.Background(), query, args...).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "<none>"
	}
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return v.String
}

func trashStatusName(t *testing.T, env customWriteEnv, id int64) (status, name string) {
	t.Helper()
	status = trashString(t, env, "SELECT post_status FROM "+env.Prefix+"posts WHERE "+postsIDColumn+" = ?", id)
	name = trashString(t, env, "SELECT post_name FROM "+env.Prefix+"posts WHERE "+postsIDColumn+" = ?", id)
	return status, name
}

func trashMeta(t *testing.T, env customWriteEnv, id int64, key string) string {
	t.Helper()
	return trashString(t, env, "SELECT meta_value FROM "+env.Prefix+"postmeta WHERE post_id = ? AND meta_key = ?", id, key)
}

// trashMetaRows counts the rows trash writes: _wp_trash_meta_* and
// _wp_desired_post_slug.
func trashMetaRows(t *testing.T, env customWriteEnv, id int64) int {
	t.Helper()
	return hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+
		"postmeta WHERE post_id = ? AND (meta_key LIKE '\\_wp\\_trash\\_meta%' ESCAPE '\\' OR meta_key = '_wp_desired_post_slug')", id)
}

func trashComment(t *testing.T, env customWriteEnv, id int64) string {
	t.Helper()
	return trashString(t, env, "SELECT comment_approved FROM "+env.Prefix+"comments WHERE comment_ID = ?", id)
}

func trashRevisions(t *testing.T, env customWriteEnv, id int64) int {
	t.Helper()
	return hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"posts WHERE post_type = 'revision' AND post_parent = ?", id)
}

func trashWant(t *testing.T, rec *httptest.ResponseRecorder, status int, what string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("%s: status = %d, want %d; body %s", what, rec.Code, status, rec.Body.String())
	}
}

func trashDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

// trashDelete sends DELETE base/id[?force=true] as actor.
func trashDelete(t *testing.T, env customWriteEnv, actor *customWriteActor, base string, id int64, force bool) *httptest.ResponseRecorder {
	t.Helper()
	path := fmt.Sprintf("%s/%d", base, id)
	if force {
		path += "?force=true"
	}
	return doJSON(t, env.Handler, actor, http.MethodDelete, path, "")
}

// trashPatch sends PATCH base/id with body as actor.
func trashPatch(t *testing.T, env customWriteEnv, actor *customWriteActor, base string, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, env.Handler, actor, http.MethodPatch, fmt.Sprintf("%s/%d", base, id), body)
}

// trashInsertPost inserts a posts row with an explicit title, slug, date,
// author and parent.
func trashInsertPost(t *testing.T, env customWriteEnv, typ, status, title, slug, date string, author, parent int64) int64 {
	t.Helper()
	res, err := env.Repos.DB().ExecContext(context.Background(),
		"INSERT INTO "+env.Prefix+"posts (post_author, post_date, post_date_gmt, post_modified, post_modified_gmt, "+
			"post_content, post_title, post_excerpt, post_status, post_name, post_type, post_parent, guid) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		author, date, date, date, date, "<p>body</p>", title, "", status, slug, typ, parent, "")
	if err != nil {
		t.Fatalf("trashInsertPost %s: %v", title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("trashInsertPost %s id: %v", title, err)
	}
	return id
}

func trashUserID(t *testing.T, env customWriteEnv, login string) int64 {
	t.Helper()
	var id int64
	if err := env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT "+postsIDColumn+" FROM "+env.Prefix+"users WHERE user_login = ?", login).Scan(&id); err != nil {
		t.Fatalf("user id of %s: %v", login, err)
	}
	return id
}

// trashRESTIDs returns the "id" of every object in a JSON array body.
func trashRESTIDs(t *testing.T, rec *httptest.ResponseRecorder) map[int64]bool {
	t.Helper()
	var items []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode list %q: %v", rec.Body.String(), err)
	}
	ids := map[int64]bool{}
	for _, it := range items {
		ids[it.ID] = true
	}
	return ids
}

// assertTrashed pins the WordPress representation of design.md "Data Models"
// for fixture f, a row trashed from the publish status.
func assertTrashed(t *testing.T, env customWriteEnv, f trashFixture, c trashCase, revisions int) {
	t.Helper()
	status, name := trashStatusName(t, env, f.id)
	if status != "trash" || name != "subject__trashed" {
		t.Errorf("row = (%s, %s), want (trash, subject__trashed)", status, name)
	}
	for key, want := range map[string]string{
		"_wp_trash_meta_status": "publish",
		"_wp_desired_post_slug": "subject",
		"keep_key":              "keep",
	} {
		if got := trashMeta(t, env, f.id, key); got != want {
			t.Errorf("postmeta %s = %q, want %q", key, got, want)
		}
	}
	if got := trashMeta(t, env, f.id, "_wp_trash_meta_time"); got == "<none>" || strings.Trim(got, "0123456789") != "" {
		t.Errorf("_wp_trash_meta_time = %q, want Unix seconds", got)
	}
	wantComments := fmt.Sprintf(`a:2:{i:%d;s:1:"1";i:%d;s:1:"0";}`, f.comment1, f.comment0)
	if got := trashMeta(t, env, f.id, "_wp_trash_meta_comments_status"); got != wantComments {
		t.Errorf("_wp_trash_meta_comments_status = %q, want %q", got, wantComments)
	}
	for _, cid := range []int64{f.comment1, f.comment0} {
		if got := trashComment(t, env, cid); got != "post-trashed" {
			t.Errorf("comment %d approved = %q, want post-trashed", cid, got)
		}
	}
	if got := hardeningTermCount(t, env, f.cat); got != 0 {
		t.Errorf("category count = %d, want 0 (trashed object recounted)", got)
	}
	if got := hardeningParent(t, env, f.child); got != f.id {
		t.Errorf("child parent = %d, want %d (children untouched)", got, f.id)
	}
	if got := trashRevisions(t, env, f.id); got != revisions {
		t.Errorf("revisions = %d, want %d (kept)", got, revisions)
	}
	if !menuItemIDs(t, env, env.Handler, f.menu)[f.item] {
		t.Errorf("admin menu read lost item %d (menu items kept)", f.item)
	}
	if c.typ == "post" {
		stickyWantOption(t, env, "a:0:{}", "sticky_posts after trash")
	}
}

// assertPurged pins the permanent-delete result for fixture f (M18-M20).
func assertPurged(t *testing.T, env customWriteEnv, f trashFixture, c trashCase) {
	t.Helper()
	if got := rowSnapshot(t, env, f.id); got != "<missing>" {
		t.Errorf("row %d still exists: %s", f.id, got)
	}
	if got := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"postmeta WHERE post_id = ?", f.id); got != 0 {
		t.Errorf("postmeta rows left = %d, want 0 (trash meta included)", got)
	}
	if got := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"comments WHERE comment_post_ID = ?", f.id); got != 0 {
		t.Errorf("comments left = %d, want 0 (post-trashed included)", got)
	}
	if got := trashRevisions(t, env, f.id); got != 0 {
		t.Errorf("revisions left = %d, want 0", got)
	}
	if got := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE object_id = ?", f.id); got != 0 {
		t.Errorf("relationships left = %d, want 0", got)
	}
	if got := hardeningTermCount(t, env, f.cat); got != 0 {
		t.Errorf("category count = %d, want 0", got)
	}
	// Only hierarchical types re-point children (M18); the others keep the parent id.
	wantParent := f.id
	if c.typ == "page" {
		wantParent = 0
	}
	if got := hardeningParent(t, env, f.child); got != wantParent {
		t.Errorf("child parent = %d, want %d", got, wantParent)
	}
	if menuItemIDs(t, env, env.Handler, f.menu)[f.item] {
		t.Errorf("menu item %d survived the delete", f.item)
	}
}

// TestBuildHandlerTrash covers the REST write side of trash (Req 6, 7, 11, 13).
func TestBuildHandlerTrash(t *testing.T) {
	t.Run("DELETE trashes by default", func(t *testing.T) {
		for _, c := range trashCases() {
			t.Run(c.name, func(t *testing.T) {
				env := newCustomWriteHandler(t, true)
				f := seedTrashFixture(t, env, c)
				revisions := trashRevisions(t, env, f.id)
				rec := trashDelete(t, env, c.actor(env), c.base, f.id, false)
				trashWant(t, rec, http.StatusOK, "DELETE")
				body := trashDecode(t, rec)
				if body["status"] != "trash" || body["deleted"] != nil {
					t.Fatalf("response is not the trashed post object: %s", rec.Body.String())
				}
				if body["slug"] != "subject__trashed" {
					t.Errorf("response slug = %v, want subject__trashed", body["slug"])
				}
				assertTrashed(t, env, f, c, revisions)
			})
		}
	})
	t.Run("DELETE of a trashed row without force answers 410", func(t *testing.T) {
		c := trashCases()[0]
		env := newCustomWriteHandler(t, true)
		f := seedTrashFixture(t, env, c)
		trashWant(t, trashDelete(t, env, c.actor(env), c.base, f.id, false), http.StatusOK, "first DELETE")
		before := rowSnapshot(t, env, f.id)
		rec := trashDelete(t, env, c.actor(env), c.base, f.id, false)
		trashWant(t, rec, http.StatusGone, "second DELETE")
		if code := trashDecode(t, rec)["code"]; code != "rest_already_trashed" {
			t.Errorf("code = %v, want rest_already_trashed", code)
		}
		if after := rowSnapshot(t, env, f.id); after != before {
			t.Errorf("a refused second trash changed the row\nbefore: %s\nafter:  %s", before, after)
		}
	})
	t.Run("force delete of a live row", func(t *testing.T) {
		for _, c := range trashCases() {
			t.Run(c.name, func(t *testing.T) {
				env := newCustomWriteHandler(t, true)
				f := seedTrashFixture(t, env, c)
				rec := trashDelete(t, env, c.actor(env), c.base, f.id, true)
				trashWant(t, rec, http.StatusOK, "DELETE force")
				if d, _ := trashDecode(t, rec)["deleted"].(bool); !d {
					t.Errorf("not marked deleted: %s", rec.Body.String())
				}
				assertPurged(t, env, f, c)
			})
		}
	})
	t.Run("force delete of a trashed row", func(t *testing.T) {
		for _, c := range trashCases() {
			t.Run(c.name, func(t *testing.T) {
				env := newCustomWriteHandler(t, true)
				f := seedTrashFixture(t, env, c)
				trashWant(t, trashDelete(t, env, c.actor(env), c.base, f.id, false), http.StatusOK, "trash")
				if trashMetaRows(t, env, f.id) == 0 {
					t.Fatalf("precondition: the trash wrote no trash meta")
				}
				rec := trashDelete(t, env, c.actor(env), c.base, f.id, true)
				trashWant(t, rec, http.StatusOK, "DELETE force on trashed")
				if d, _ := trashDecode(t, rec)["deleted"].(bool); !d {
					t.Errorf("not marked deleted: %s", rec.Body.String())
				}
				assertPurged(t, env, f, c)
				if got := trashMetaRows(t, env, f.id); got != 0 {
					t.Errorf("trash meta rows left = %d, want 0", got)
				}
			})
		}
	})
	t.Run("PATCH status draft restores", func(t *testing.T) {
		for _, c := range trashCases() {
			t.Run(c.name, func(t *testing.T) {
				env := newCustomWriteHandler(t, true)
				f := seedTrashFixture(t, env, c)
				trashWant(t, trashDelete(t, env, c.actor(env), c.base, f.id, false), http.StatusOK, "trash")
				rec := trashPatch(t, env, c.actor(env), c.base, f.id, `{"status":"draft"}`)
				trashWant(t, rec, http.StatusOK, "restore")
				if got := trashDecode(t, rec); got["status"] != "draft" || got["slug"] != "subject" {
					t.Errorf("response = (%v, %v), want (draft, subject)", got["status"], got["slug"])
				}
				if status, name := trashStatusName(t, env, f.id); status != "draft" || name != "subject" {
					t.Errorf("row = (%s, %s), want (draft, subject)", status, name)
				}
				if got := trashMetaRows(t, env, f.id); got != 0 {
					t.Errorf("trash meta rows left = %d, want 0", got)
				}
				if got := trashMeta(t, env, f.id, "keep_key"); got != "keep" {
					t.Errorf("unrelated postmeta = %q, want keep", got)
				}
				if a, b := trashComment(t, env, f.comment1), trashComment(t, env, f.comment0); a != "1" || b != "0" {
					t.Errorf("comments restored to (%s, %s), want (1, 0)", a, b)
				}
				if got := hardeningTermCount(t, env, f.cat); got != 1 {
					t.Errorf("category count = %d, want 1 after restore", got)
				}
			})
		}
	})
	t.Run("restore finds the slug taken", func(t *testing.T) {
		for _, c := range trashCases() {
			t.Run(c.name, func(t *testing.T) {
				env := newCustomWriteHandler(t, true)
				a := createRESTRow(t, env, c.actor(env), c.base, "Subject")
				trashWant(t, trashDelete(t, env, c.actor(env), c.base, a, false), http.StatusOK, "trash")
				b := createRESTRow(t, env, c.actor(env), c.base, "Subject") // the live row takes the name
				if _, name := trashStatusName(t, env, b); name != "subject" {
					t.Fatalf("live row slug = %q, want subject (a trashed row must not block it)", name)
				}
				trashWant(t, trashPatch(t, env, c.actor(env), c.base, a, `{"status":"draft"}`), http.StatusOK, "restore")
				if _, name := trashStatusName(t, env, a); name != "subject-2" {
					t.Errorf("restored slug = %q, want subject-2", name)
				}
			})
		}
	})
	t.Run("sparse PATCH keeps a trashed row trashed", func(t *testing.T) {
		c := trashCases()[0]
		env := newCustomWriteHandler(t, true)
		f := seedTrashFixture(t, env, c)
		trashWant(t, trashDelete(t, env, c.actor(env), c.base, f.id, false), http.StatusOK, "trash")
		trashWant(t, trashPatch(t, env, c.actor(env), c.base, f.id, `{"title":"Renamed"}`), http.StatusOK, "title PATCH")
		if status, name := trashStatusName(t, env, f.id); status != "trash" || name != "subject__trashed" {
			t.Errorf("row = (%s, %s), want (trash, subject__trashed)", status, name)
		}
	})
	t.Run("PATCH status trash trashes a live row", func(t *testing.T) {
		for _, c := range trashCases() {
			t.Run(c.name, func(t *testing.T) {
				env := newCustomWriteHandler(t, true)
				f := seedTrashFixture(t, env, c)
				revisions := trashRevisions(t, env, f.id)
				rec := trashPatch(t, env, c.actor(env), c.base, f.id, `{"status":"trash"}`)
				trashWant(t, rec, http.StatusOK, "PATCH trash")
				// The update itself may snapshot one more revision; the trash deletes none.
				after := trashRevisions(t, env, f.id)
				if after < revisions {
					t.Errorf("revisions fell from %d to %d", revisions, after)
				}
				assertTrashed(t, env, f, c, after)
			})
		}
	})
}

// TestBuildHandlerTrashCapabilities (Req 6.2, 7.8): the trash and restore gates
// use the row's previous status and the delete capability.
func TestBuildHandlerTrashCapabilities(t *testing.T) {
	const base = "/wp-json/wp/v2/posts"
	t.Run("contributor cannot restore or delete a formerly published row", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		id := trashInsertPost(t, env, "post", "publish", "Contrib Post", "contrib-post", hardeningRawDate, trashUserID(t, env, "contrib"), 0)
		rec := trashDelete(t, env, &env.Contrib, base, id, false)
		trashWant(t, rec, http.StatusForbidden, "contributor trash of a published row")
		if code := trashDecode(t, rec)["code"]; code != "rest_cannot_delete" {
			t.Errorf("code = %v, want rest_cannot_delete", code)
		}
		trashWant(t, trashDelete(t, env, &env.Admin, base, id, false), http.StatusOK, "admin trash")
		before := rowSnapshot(t, env, id)
		trashWant(t, trashDelete(t, env, &env.Contrib, base, id, true), http.StatusForbidden, "contributor force delete")
		trashWant(t, trashPatch(t, env, &env.Contrib, base, id, `{"status":"draft"}`), http.StatusForbidden, "contributor restore")
		if after := rowSnapshot(t, env, id); after != before {
			t.Errorf("a refused write changed the trashed row\nbefore: %s\nafter:  %s", before, after)
		}
	})
	t.Run("author can restore, trash and delete their formerly published row", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		id := trashInsertPost(t, env, "post", "publish", "Author Post", "author-post", hardeningRawDate, trashUserID(t, env, "author"), 0)
		trashWant(t, trashDelete(t, env, &env.Admin, base, id, false), http.StatusOK, "admin trash")
		trashWant(t, trashPatch(t, env, &env.Author, base, id, `{"status":"draft"}`), http.StatusOK, "author restore")
		if status, name := trashStatusName(t, env, id); status != "draft" || name != "author-post" {
			t.Errorf("restored row = (%s, %s), want (draft, author-post)", status, name)
		}
		trashWant(t, trashPatch(t, env, &env.Author, base, id, `{"status":"publish"}`), http.StatusOK, "author publish")
		trashWant(t, trashDelete(t, env, &env.Author, base, id, false), http.StatusOK, "author trash")
		trashWant(t, trashDelete(t, env, &env.Author, base, id, true), http.StatusOK, "author force delete")
		if got := rowSnapshot(t, env, id); got != "<missing>" {
			t.Errorf("row survived a permitted force delete: %s", got)
		}
	})
	t.Run("edit_posts alone cannot trash through status trash", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		id := trashInsertPost(t, env, "post", "draft", "Smith Draft", "smith-draft", hardeningRawDate, trashUserID(t, env, "termsmith"), 0)
		trashWant(t, trashPatch(t, env, &env.Termsmith, base, id, `{"title":"Edited"}`), http.StatusOK, "control edit")
		before := rowSnapshot(t, env, id)
		trashWant(t, trashPatch(t, env, &env.Termsmith, base, id, `{"status":"trash"}`), http.StatusForbidden, "PATCH status trash")
		if after := rowSnapshot(t, env, id); after != before {
			t.Errorf("a refused trash changed the row\nbefore: %s\nafter:  %s", before, after)
		}
	})
}

// TestBuildHandlerTrashReads (Req 8-10): a trashed row leaves every public
// read, the default REST collection, the admin list, the dashboard count and
// the comment lists, and an editor still reads it from REST.
func TestBuildHandlerTrashReads(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	const title = "Zebra Subject"
	cat := seedRawTerm(t, env, "Cat", "cat", "category", 0)
	subject := trashInsertPost(t, env, "post", "publish", title, "zebra-subject", "2020-01-01 10:00:00", 1, 0)
	seedRawTermRelationship(t, env, subject, cat)
	for i := 1; i <= 10; i++ {
		filler := trashInsertPost(t, env, "post", "publish", fmt.Sprintf("Filler %02d", i), fmt.Sprintf("filler-%02d", i),
			fmt.Sprintf("2024-01-%02d 10:00:00", i), 1, 0)
		seedRawTermRelationship(t, env, filler, cat)
	}
	trashExec(t, env, "UPDATE "+env.Prefix+"term_taxonomy SET count = 11 WHERE term_taxonomy_id = ?", cat)
	page := trashInsertPost(t, env, "page", "publish", "Gone Page", "gone-page", "2020-01-02 10:00:00", 1, 0)
	comment := seedRawComment(t, env, subject, "1")

	get := func(actor *customWriteActor, path string) *httptest.ResponseRecorder {
		return doJSON(t, env.Handler, actor, http.MethodGet, path, "")
	}
	publicPaths := []string{"/zebra-subject/", "/page/2/", "/category/cat/page/2/", "/?m=2020", fmt.Sprintf("/?p=%d", subject)}
	// Controls: the paths that list the subject do so before the trash.
	for _, p := range publicPaths[:4] {
		rec := get(nil, p)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), title) {
			t.Fatalf("control GET %s: status %d, lists the subject = %v", p, rec.Code, strings.Contains(rec.Body.String(), title))
		}
	}
	restTotal := func(actor *customWriteActor, query string) (string, map[int64]bool) {
		rec := get(actor, "/wp-json/wp/v2/posts"+query)
		trashWant(t, rec, http.StatusOK, "REST collection "+query)
		return rec.Header().Get("X-WP-Total"), trashRESTIDs(t, rec)
	}
	if total, ids := restTotal(nil, "?per_page=100"); total != "11" || !ids[subject] {
		t.Fatalf("control REST collection: total %q, lists the subject = %v", total, ids[subject])
	}
	statsPages := func() int {
		rec := get(&env.Admin, "/admin/api/stats")
		trashWant(t, rec, http.StatusOK, "stats")
		return int(trashDecode(t, rec)["pages"].(float64))
	}
	if got := statsPages(); got != 1 {
		t.Fatalf("control stats pages = %d, want 1", got)
	}
	commentIDs := func(path string) map[int64]bool {
		rec := get(&env.Admin, path)
		trashWant(t, rec, http.StatusOK, "comments "+path)
		ids := map[int64]bool{}
		if strings.HasPrefix(path, "/admin/") {
			var body struct {
				Items []struct {
					ID int64 `json:"id"`
				} `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode admin comments: %v", err)
			}
			for _, it := range body.Items {
				ids[it.ID] = true
			}
			return ids
		}
		return trashRESTIDs(t, rec)
	}
	const restComments, adminComments = "/wp-json/wp/v2/comments?status=any&per_page=100", "/admin/api/comments?perPage=100"
	if !commentIDs(restComments)[comment] || !commentIDs(adminComments)[comment] {
		t.Fatalf("control comment lists do not hold comment %d", comment)
	}

	trashWant(t, trashDelete(t, env, &env.Admin, "/wp-json/wp/v2/posts", subject, false), http.StatusOK, "trash post")
	trashWant(t, trashDelete(t, env, &env.Admin, "/wp-json/wp/v2/pages", page, false), http.StatusOK, "trash page")

	// Public reads (Req 9.1, 9.6).
	for _, p := range []string{"/zebra-subject/", "/zebra-subject__trashed/", "/page/2/", "/category/cat/page/2/"} {
		if rec := get(nil, p); rec.Code != http.StatusNotFound {
			t.Errorf("anonymous GET %s = %d, want 404", p, rec.Code)
		}
	}
	for _, p := range publicPaths {
		if rec := get(nil, p); strings.Contains(rec.Body.String(), title) {
			t.Errorf("anonymous GET %s still shows the trashed post", p)
		}
	}
	if body := get(nil, "/").Body.String(); strings.Contains(body, title) {
		t.Errorf("home lists the trashed post")
	}
	if body := get(nil, "/category/cat/").Body.String(); strings.Contains(body, title) {
		t.Errorf("category archive lists the trashed post")
	}

	// REST reads (Req 8).
	rec := get(&env.Editor, fmt.Sprintf("/wp-json/wp/v2/posts/%d", subject))
	trashWant(t, rec, http.StatusOK, "editor GET trashed post")
	if got := trashDecode(t, rec)["status"]; got != "trash" {
		t.Errorf("editor GET status = %v, want trash", got)
	}
	trashWant(t, get(nil, fmt.Sprintf("/wp-json/wp/v2/posts/%d", subject)), http.StatusNotFound, "anonymous GET trashed post")
	if total, ids := restTotal(nil, "?per_page=100"); total != "10" || ids[subject] {
		t.Errorf("anonymous default collection: total %q, lists the subject = %v; want 10, false", total, ids[subject])
	}
	if total, ids := restTotal(&env.Editor, "?per_page=100"); total != "10" || ids[subject] {
		t.Errorf("editor default collection: total %q, lists the subject = %v; want 10, false", total, ids[subject])
	}
	if total, ids := restTotal(&env.Editor, "?status=trash"); total != "1" || len(ids) != 1 || !ids[subject] {
		t.Errorf("editor ?status=trash: total %q, ids %v; want exactly the subject", total, ids)
	}
	rec = get(nil, "/wp-json/wp/v2/posts?status=trash")
	trashWant(t, rec, http.StatusForbidden, "anonymous ?status=trash")
	if code := trashDecode(t, rec)["code"]; code != "rest_forbidden_status" {
		t.Errorf("code = %v, want rest_forbidden_status", code)
	}

	// Admin reads (Req 10).
	rec = get(&env.Admin, "/admin/api/posts?perPage=100")
	trashWant(t, rec, http.StatusOK, "admin list")
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode admin list: %v", err)
	}
	for _, it := range list.Items {
		if it.ID == subject || it.ID == page {
			t.Errorf("admin list shows trashed row %d", it.ID)
		}
	}
	if list.Total != 10 {
		t.Errorf("admin list total = %d, want 10", list.Total)
	}
	if got := statsPages(); got != 0 {
		t.Errorf("stats pages = %d, want 0", got)
	}
	trashed, err := env.Repos.AdminPosts.ListForAdmin(context.Background(),
		domain.AdminPostFilter{Types: []string{"post", "page"}, Statuses: []string{"trash"}})
	if err != nil {
		t.Fatalf("ListForAdmin trash: %v", err)
	}
	if len(trashed) != 2 {
		t.Errorf("explicit trash filter returned %d rows, want the post and the page", len(trashed))
	}
	rec = get(&env.Admin, fmt.Sprintf("/admin/api/posts/%d", subject))
	trashWant(t, rec, http.StatusOK, "admin detail of a trashed row")
	if got := trashDecode(t, rec)["status"]; got != "trash" {
		t.Errorf("admin detail status = %v, want trash", got)
	}

	// Comments of a trashed row are hidden from every list (Req 4.6).
	if commentIDs(restComments)[comment] {
		t.Errorf("REST comment list (status=any) shows a post-trashed comment")
	}
	if commentIDs(adminComments)[comment] {
		t.Errorf("admin comment list shows a post-trashed comment")
	}

	// Restore brings the permalink back.
	trashWant(t, trashPatch(t, env, &env.Admin, "/wp-json/wp/v2/posts", subject, `{"status":"publish"}`), http.StatusOK, "restore")
	if rec := get(nil, "/zebra-subject/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), title) {
		t.Errorf("restored permalink: status %d, shows the post = %v", rec.Code, strings.Contains(rec.Body.String(), title))
	}
	if !commentIDs(restComments)[comment] || !commentIDs(adminComments)[comment] {
		t.Errorf("restore did not bring comment %d back into the lists", comment)
	}
}

// TestBuildHandlerTrashChildLink (Req 9.5): a published child of a trashed page
// builds its link from the suffixed ancestor and stops resolving at the old path.
func TestBuildHandlerTrashChildLink(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	const pages = "/wp-json/wp/v2/pages"
	parent := createRESTRow(t, env, &env.Admin, pages, "Parent")
	rec := doJSON(t, env.Handler, &env.Admin, http.MethodPost, pages,
		fmt.Sprintf(`{"title":"Child","slug":"child","status":"publish","parent":%d}`, parent))
	trashWant(t, rec, http.StatusCreated, "create child")
	child := int64(trashDecode(t, rec)["id"].(float64))
	link := func() string {
		rec := doJSON(t, env.Handler, &env.Admin, http.MethodGet, fmt.Sprintf("%s/%d", pages, child), "")
		trashWant(t, rec, http.StatusOK, "GET child")
		return fmt.Sprint(trashDecode(t, rec)["link"])
	}
	if got := link(); !strings.HasSuffix(got, "/parent/child/") {
		t.Fatalf("control link = %q, want a /parent/child/ path", got)
	}
	trashWant(t, doJSON(t, env.Handler, nil, http.MethodGet, "/parent/child/", ""), http.StatusOK, "control public child")
	trashWant(t, trashDelete(t, env, &env.Admin, pages, parent, false), http.StatusOK, "trash parent")
	if got := link(); !strings.HasSuffix(got, "/parent__trashed/child/") {
		t.Errorf("child link after trashing the parent = %q, want a /parent__trashed/child/ path", got)
	}
	// The old path no longer serves the child (the canonical redirect points at the suffixed path).
	if rec := doJSON(t, env.Handler, nil, http.MethodGet, "/parent/child/", ""); rec.Code == http.StatusOK {
		t.Errorf("GET /parent/child/ after trashing the parent = 200, want it not to resolve")
	}
	trashWant(t, trashPatch(t, env, &env.Admin, pages, parent, `{"status":"publish"}`), http.StatusOK, "restore parent")
	if got := link(); !strings.HasSuffix(got, "/parent/child/") {
		t.Errorf("child link after restoring the parent = %q, want a /parent/child/ path", got)
	}
	trashWant(t, doJSON(t, env.Handler, nil, http.MethodGet, "/parent/child/", ""), http.StatusOK, "restored child path")
}

// TestBuildHandlerTrashPublicMenu (Req 9.2-9.4): the public menu hides a trashed
// page's item and shows its children at the parent; the admin read keeps the
// item; a restore brings it back.
func TestBuildHandlerTrashPublicMenu(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	const pages = "/wp-json/wp/v2/pages"
	subject := createRESTRow(t, env, &env.Admin, pages, "Subject")
	createRESTRow(t, env, &env.Admin, pages, "Keeper")
	menu := createMenu(t, env, "Primary", "primary")
	label := func(id int64, text string) int64 {
		trashExec(t, env, "UPDATE "+env.Prefix+"posts SET post_title = ? WHERE "+postsIDColumn+" = ?", text, id)
		return id
	}
	top := label(seedRawMenuItem(t, env, menu, "page", subject), "SubjectLinkTop")
	kid := label(seedRawMenuItemWithParent(t, env, menu, "custom", 0, top), "KidOfSubject")
	trashExec(t, env, "UPDATE "+env.Prefix+"postmeta SET meta_value = 'custom' WHERE post_id = ? AND meta_key = '_menu_item_type'", kid)
	trashExec(t, env, "INSERT INTO "+env.Prefix+"options (option_name, option_value, autoload) VALUES (?, ?, ?)",
		"theme_mods_default", fmt.Sprintf(`a:1:{s:18:"nav_menu_locations";a:1:{s:7:"primary";i:%d;}}`, menu), "yes")
	render := func() string {
		rec := doJSON(t, env.Handler, nil, http.MethodGet, "/keeper/", "")
		trashWant(t, rec, http.StatusOK, "GET /keeper/")
		return rec.Body.String()
	}
	if body := render(); !strings.Contains(body, "SubjectLinkTop") || !strings.Contains(body, "KidOfSubject") {
		t.Fatalf("precondition: the public page does not render the seeded menu")
	}
	trashWant(t, trashDelete(t, env, &env.Admin, pages, subject, false), http.StatusOK, "trash page")
	body := render()
	if strings.Contains(body, "SubjectLinkTop") {
		t.Errorf("public menu still renders the trashed page's item")
	}
	if !strings.Contains(body, "KidOfSubject") {
		t.Errorf("public menu lost the trashed item's child; it must show at the item's parent")
	}
	ids := menuItemIDs(t, env, env.Handler, menu)
	if !ids[top] || !ids[kid] {
		t.Errorf("admin menu read lost items (top %v, child %v); it must keep them", ids[top], ids[kid])
	}
	trashWant(t, trashPatch(t, env, &env.Admin, pages, subject, `{"status":"publish"}`), http.StatusOK, "restore page")
	if body := render(); !strings.Contains(body, "SubjectLinkTop") || !strings.Contains(body, "KidOfSubject") {
		t.Errorf("restore did not bring the menu item back")
	}
}

// TestBuildHandlerTrashScheduler (Req 12): a trashed future post is not
// published by the scheduler; restored to future with a past date it is.
func TestBuildHandlerTrashScheduler(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	deps := customWriteDeps(t, env, customWriteRegistry(t))
	_, postWrite, err := buildHandler(deps)
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	id, err := env.Repos.PostWriter.Create(ctx, domain.Post{
		Author: 1, Title: "Scheduled widget", Status: "future", Type: "product",
		Slug: "scheduled-widget", Date: past, DateGMT: past,
	})
	if err != nil {
		t.Fatalf("create future product: %v", err)
	}
	const base = "/wp-json/wp/v2/product"
	trashWant(t, trashDelete(t, env, &env.Products, base, id, false), http.StatusOK, "trash future product")
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go buildScheduler(deps, postWrite).Run(runCtx)
	time.Sleep(2500 * time.Millisecond) // two scheduler ticks (interval 1s)
	if status, _ := trashStatusName(t, env, id); status != "trash" {
		t.Fatalf("status after the scheduler ran = %q, want trash", status)
	}
	// REST refuses status future with a past date, so restore through the service.
	admin := auth.NewPrincipal(trashUserID(t, env, "products"), "products", productsCaps())
	if err := postWrite.Restore(ctx, admin, id, "future"); err != nil {
		t.Fatalf("Restore to future: %v", err)
	}
	if status, _ := trashStatusName(t, env, id); status != "future" && status != "publish" {
		t.Fatalf("status after Restore = %q, want future (or already published)", status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status, _ := trashStatusName(t, env, id); status == "publish" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	status, _ := trashStatusName(t, env, id)
	t.Fatalf("restored future product status = %q after waiting for the scheduler, want publish", status)
}

// TestBuildHandlerTrashUnwired (Req 14.2): a handler whose post write service
// has no trasher answers a non-force DELETE with 501 and keeps the row; force
// still deletes.
func TestBuildHandlerTrashUnwired(t *testing.T) {
	env := newCustomWriteHandlerWith(t, true, func(r *storage.Repositories) { r.PostTrash = nil })
	const base = "/wp-json/wp/v2/posts"
	id := createRESTRow(t, env, &env.Admin, base, "Subject")
	before := rowSnapshot(t, env, id)
	rec := trashDelete(t, env, &env.Admin, base, id, false)
	trashWant(t, rec, http.StatusNotImplemented, "DELETE without a trasher")
	if code := trashDecode(t, rec)["code"]; code != "rest_trash_not_supported" {
		t.Errorf("code = %v, want rest_trash_not_supported", code)
	}
	if after := rowSnapshot(t, env, id); after != before {
		t.Errorf("a refused trash changed the row\nbefore: %s\nafter:  %s", before, after)
	}
	trashWant(t, trashDelete(t, env, &env.Admin, base, id, true), http.StatusOK, "DELETE force without a trasher")
	if got := rowSnapshot(t, env, id); got != "<missing>" {
		t.Errorf("force delete left the row: %s", got)
	}
}

// TestBuildHandlerTrashUnmanagedType (Req 7.10, 25.3): a row of an unmanaged
// type answers 404 on REST and ErrForbidden on the service, and is not changed.
func TestBuildHandlerTrashUnmanagedType(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	order := seedRawPost(t, env, "shop_order", "publish", 0)
	before := rowSnapshot(t, env, order)
	for _, base := range []string{"/wp-json/wp/v2/posts", "/wp-json/wp/v2/pages", "/wp-json/wp/v2/product"} {
		trashWant(t, trashDelete(t, env, &env.Admin, base, order, false), http.StatusNotFound, "DELETE shop_order via "+base)
	}
	_, postWrite, err := buildHandler(customWriteDeps(t, env, customWriteRegistry(t)))
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	admin := auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator})
	if err := postWrite.Trash(context.Background(), admin, order); !errors.Is(err, content.ErrForbidden) {
		t.Errorf("Trash of an unmanaged type = %v, want ErrForbidden", err)
	}
	if after := rowSnapshot(t, env, order); after != before {
		t.Errorf("the unmanaged row changed\nbefore: %s\nafter:  %s", before, after)
	}
}
