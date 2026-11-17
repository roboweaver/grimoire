package storagetest

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the post-dependents contract. They sit far above SeedFixtures'
// ids so the seeded rows never collide with the base fixtures.
const (
	pdG        int64 = 9100 // P's own parent page
	pdP        int64 = 9101 // the page whose dependents are purged
	pdChildA   int64 = 9102 // child page of P
	pdChildB   int64 = 9103 // child page of P
	pdChildPst int64 = 9104 // child post (other type) of P
	pdAttach   int64 = 9105 // attachment of P
	pdQ        int64 = 9106 // unrelated page with its own dependents
	pdQChild   int64 = 9107 // child page of Q

	pdModified    = "2001-02-03 04:05:06"
	pdModifiedGMT = "2001-02-03 04:05:07"
)

// pdPostCols are every posts column the re-pointing must leave alone, i.e. all
// of them except post_parent. post_modified and post_modified_gmt are listed so
// Req 9.4 (a re-point does not touch the modified time) is pinned.
var pdPostCols = []string{
	"post_author", "post_date", "post_content", "post_title", "post_excerpt",
	"post_status", "post_name", "post_type", "comment_status", "post_mime_type",
	"menu_order", "post_modified", "post_modified_gmt", "guid",
}

// runPostDependentsPurgerContract covers PostDependentsPurger.PurgePostDependents:
// children and attachments are re-pointed to the grandparent (Req 9), the post's
// comments, commentmeta and postmeta are deleted (Req 8), the post itself and
// every other post's rows are left alone, and the call is idempotent (Req 10).
// Atomicity (Req 8.5, 9.5) has no portable failure injection and is covered on
// SQLite in internal/storage/wprepo/postdependents_test.go.
func runPostDependentsPurgerContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	// setup builds a fresh backend and seeds the page tree and its dependents.
	setup := func(t *testing.T) (db *sql.DB, vendor string, purge func(postID, parentID int64, reparentType string) error, cleanup func()) {
		t.Helper()
		repos, cl := newRepos(t)
		db = repos.DB()
		vendor = detectVendor(ctx, t, db)
		seedPostDependents(ctx, t, db, vendor)
		purge = func(postID, parentID int64, reparentType string) error {
			return repos.PostDependents.PurgePostDependents(ctx, postID, parentID, reparentType)
		}
		return db, vendor, purge, cl
	}

	t.Run("re-points child pages and attachments to the grandparent and leaves other types", func(t *testing.T) {
		db, vendor, purge, cleanup := setup(t)
		defer cleanup()
		beforeA := pdSnapshot(ctx, t, db, vendor, pdChildA)
		beforeB := pdSnapshot(ctx, t, db, vendor, pdChildB)
		beforeAtt := pdSnapshot(ctx, t, db, vendor, pdAttach)
		beforePost := pdSnapshot(ctx, t, db, vendor, pdChildPst)

		if err := purge(pdP, pdG, "page"); err != nil {
			t.Fatalf("PurgePostDependents: %v", err)
		}
		for name, id := range map[string]int64{"child page A": pdChildA, "child page B": pdChildB, "attachment": pdAttach} {
			if got := pdParent(ctx, t, db, vendor, id); got != pdG {
				t.Errorf("%s post_parent = %d, want %d (grandparent)", name, got, pdG)
			}
		}
		if got := pdParent(ctx, t, db, vendor, pdChildPst); got != pdP {
			t.Errorf("child post of another type post_parent = %d, want %d (unchanged)", got, pdP)
		}
		// Req 9.4: every other column, post_modified included, is unchanged.
		for name, c := range map[string]struct {
			id     int64
			before []any
		}{"child page A": {pdChildA, beforeA}, "child page B": {pdChildB, beforeB}, "attachment": {pdAttach, beforeAtt}, "child post": {pdChildPst, beforePost}} {
			if after := pdSnapshot(ctx, t, db, vendor, c.id); !reflect.DeepEqual(after, c.before) {
				t.Errorf("%s columns changed:\n before %v\n after  %v", name, c.before, after)
			}
		}
		if got := pdParent(ctx, t, db, vendor, pdQChild); got != pdQ {
			t.Errorf("unrelated page's child post_parent = %d, want %d", got, pdQ)
		}
	})

	t.Run("deletes the post's comments, commentmeta and postmeta only", func(t *testing.T) {
		db, vendor, purge, cleanup := setup(t)
		defer cleanup()
		if n := pdCommentCount(ctx, t, db, vendor, pdP); n != 4 {
			t.Fatalf("seed: comments of P = %d, want 4", n)
		}
		if err := purge(pdP, pdG, "page"); err != nil {
			t.Fatalf("PurgePostDependents: %v", err)
		}
		if n := pdCommentCount(ctx, t, db, vendor, pdP); n != 0 {
			t.Errorf("comments of P = %d, want 0 (every approval state)", n)
		}
		if n := pdCommentMetaCount(ctx, t, db, vendor, pdP); n != 0 {
			t.Errorf("commentmeta of P's comments = %d, want 0", n)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM wp_postmeta WHERE post_id = ?`, pdP); n != 0 {
			t.Errorf("postmeta of P = %d, want 0", n)
		}
		// Q is untouched.
		if n := pdCommentCount(ctx, t, db, vendor, pdQ); n != 1 {
			t.Errorf("comments of Q = %d, want 1", n)
		}
		if n := pdCommentMetaCount(ctx, t, db, vendor, pdQ); n != 1 {
			t.Errorf("commentmeta of Q's comments = %d, want 1", n)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM wp_postmeta WHERE post_id = ?`, pdQ); n != 1 {
			t.Errorf("postmeta of Q = %d, want 1", n)
		}
		// The base fixtures' comments and commentmeta are untouched too.
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM wp_commentmeta WHERE comment_id NOT IN (9201, 9202, 9203, 9204)`); n < 1 {
			t.Errorf("commentmeta outside P = %d, want the fixtures' rows to remain", n)
		}
	})

	t.Run("the purged post itself still exists", func(t *testing.T) {
		db, vendor, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(pdP, pdG, "page"); err != nil {
			t.Fatalf("PurgePostDependents: %v", err)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM wp_posts WHERE `+rebind.Ident(vendor, "ID")+` = ?`, pdP); n != 1 {
			t.Errorf("post P rows = %d, want 1 (the purge never deletes the post)", n)
		}
	})

	t.Run("empty reparentType keeps child pages and still re-points attachments", func(t *testing.T) {
		db, vendor, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(pdP, pdG, ""); err != nil {
			t.Fatalf("PurgePostDependents: %v", err)
		}
		for name, id := range map[string]int64{"child page A": pdChildA, "child page B": pdChildB} {
			if got := pdParent(ctx, t, db, vendor, id); got != pdP {
				t.Errorf("%s post_parent = %d, want %d (kept, no reparent type)", name, got, pdP)
			}
		}
		if got := pdParent(ctx, t, db, vendor, pdAttach); got != pdG {
			t.Errorf("attachment post_parent = %d, want %d (always re-pointed)", got, pdG)
		}
		if n := pdCommentCount(ctx, t, db, vendor, pdP); n != 0 {
			t.Errorf("comments of P = %d, want 0 (dependents still purged)", n)
		}
	})

	t.Run("parent 0 re-points to the top level", func(t *testing.T) {
		db, vendor, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(pdP, 0, "page"); err != nil {
			t.Fatalf("PurgePostDependents: %v", err)
		}
		for name, id := range map[string]int64{"child page A": pdChildA, "child page B": pdChildB, "attachment": pdAttach} {
			if got := pdParent(ctx, t, db, vendor, id); got != 0 {
				t.Errorf("%s post_parent = %d, want 0", name, got)
			}
		}
	})

	t.Run("no dependents and nonexistent id both return nil", func(t *testing.T) {
		_, _, purge, cleanup := setup(t)
		defer cleanup()
		// pdQChild is a leaf page with no children, comments or meta of its own.
		if err := purge(pdQChild, pdQ, "page"); err != nil {
			t.Errorf("purge of a post with no dependents = %v, want nil", err)
		}
		if err := purge(999999, 0, "page"); err != nil {
			t.Errorf("purge of a nonexistent id = %v, want nil", err)
		}
	})

	t.Run("calling the same purge twice succeeds", func(t *testing.T) {
		db, vendor, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(pdP, pdG, "page"); err != nil {
			t.Fatalf("first PurgePostDependents: %v", err)
		}
		if err := purge(pdP, pdG, "page"); err != nil {
			t.Fatalf("second PurgePostDependents: %v", err)
		}
		if got := pdParent(ctx, t, db, vendor, pdChildA); got != pdG {
			t.Errorf("child page A post_parent after repeat = %d, want %d", got, pdG)
		}
		if n := pdCommentCount(ctx, t, db, vendor, pdP); n != 0 {
			t.Errorf("comments of P after repeat = %d, want 0", n)
		}
	})
}

// detectVendor identifies the engine behind db. The contract signature carries
// only a NewReposFunc, and the raw seed SQL needs the vendor for identifier
// quoting and placeholder style, so it is probed rather than passed in.
func detectVendor(ctx context.Context, t *testing.T, db *sql.DB) string {
	t.Helper()
	var v string
	if err := db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&v); err == nil {
		return "sqlite"
	}
	if err := db.QueryRowContext(ctx, `SELECT version()`).Scan(&v); err != nil {
		t.Fatalf("detect vendor: %v", err)
	}
	if strings.Contains(strings.ToLower(v), "postgres") {
		return "postgres"
	}
	return "mysql"
}

// seedPostDependents inserts the page tree and dependents described by the
// task: P (parent G) with two child pages, a child post, an attachment, four
// comments (approved 1, 0, spam, trash) each with commentmeta, and three
// postmeta rows; plus an unrelated page Q with a child, a comment, a commentmeta
// row and a postmeta row.
func seedPostDependents(ctx context.Context, t *testing.T, db *sql.DB, vendor string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	post := func(id int64, slug, ptype string, parent int64, mime string) {
		t.Helper()
		exec(postInsert(vendor, "wp_"), postArgs(id, slug, "T "+slug, ptype, "publish", "2000-01-01 00:00:00", "open", parent, mime, 7)...)
		exec(`UPDATE wp_posts SET post_modified = ?, post_modified_gmt = ?, guid = ? WHERE `+rebind.Ident(vendor, "ID")+` = ?`,
			pdModified, pdModifiedGMT, "http://example.test/?p="+slug, id)
	}
	post(pdG, "pd-g", "page", 0, "")
	post(pdP, "pd-p", "page", pdG, "")
	post(pdChildA, "pd-child-a", "page", pdP, "")
	post(pdChildB, "pd-child-b", "page", pdP, "")
	post(pdChildPst, "pd-child-post", "post", pdP, "")
	post(pdAttach, "pd-attach", "attachment", pdP, "image/png")
	post(pdQ, "pd-q", "page", 0, "")
	post(pdQChild, "pd-q-child", "page", pdQ, "")

	comment := func(id, postID int64, approved string) {
		t.Helper()
		exec(commentInsert(vendor, "wp_"), id, postID, "A", "a@example.test", "", "", "2000-01-01 00:00:00", "2000-01-01 00:00:00", "c", approved, "", 0, 0)
		exec(`INSERT INTO wp_commentmeta (comment_id, meta_key, meta_value) VALUES (?, ?, ?)`, id, "pd_key", "pd_val")
	}
	comment(9201, pdP, "1")
	comment(9202, pdP, "0")
	comment(9203, pdP, "spam")
	comment(9204, pdP, "trash")
	comment(9205, pdQ, "1")
	for _, k := range []string{"pd_a", "pd_b", "pd_c"} {
		exec(`INSERT INTO wp_postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, pdP, k, "v")
	}
	exec(`INSERT INTO wp_postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, pdQ, "pd_q", "v")
}

func pdCount(ctx context.Context, t *testing.T, db *sql.DB, vendor, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, rebind.Rebind(vendor, q), args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func pdParent(ctx context.Context, t *testing.T, db *sql.DB, vendor string, id int64) int64 {
	t.Helper()
	var p int64
	q := rebind.Rebind(vendor, `SELECT post_parent FROM wp_posts WHERE `+rebind.Ident(vendor, "ID")+` = ?`)
	if err := db.QueryRowContext(ctx, q, id).Scan(&p); err != nil {
		t.Fatalf("read post_parent of %d: %v", id, err)
	}
	return p
}

func pdCommentCount(ctx context.Context, t *testing.T, db *sql.DB, vendor string, postID int64) int {
	t.Helper()
	return pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM wp_comments WHERE `+rebind.Ident(vendor, "comment_post_ID")+` = ?`, postID)
}

func pdCommentMetaCount(ctx context.Context, t *testing.T, db *sql.DB, vendor string, postID int64) int {
	t.Helper()
	return pdCount(ctx, t, db, vendor,
		`SELECT COUNT(*) FROM wp_commentmeta WHERE comment_id IN (SELECT `+rebind.Ident(vendor, "comment_ID")+
			` FROM wp_comments WHERE `+rebind.Ident(vendor, "comment_post_ID")+` = ?)`, postID)
}

// pdSnapshot reads every posts column the purge must not change (all but
// post_parent) as a comparable slice.
func pdSnapshot(ctx context.Context, t *testing.T, db *sql.DB, vendor string, id int64) []any {
	t.Helper()
	vals := make([]any, len(pdPostCols))
	ptrs := make([]any, len(pdPostCols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	q := rebind.Rebind(vendor, `SELECT `+rebind.IdentList(vendor, pdPostCols)+` FROM wp_posts WHERE `+rebind.Ident(vendor, "ID")+` = ?`)
	if err := db.QueryRowContext(ctx, q, id).Scan(ptrs...); err != nil {
		t.Fatalf("snapshot post %d: %v", id, err)
	}
	for i, v := range vals { // normalise driver byte slices so DeepEqual compares values
		if b, ok := v.([]byte); ok {
			vals[i] = string(b)
		}
	}
	return vals
}
