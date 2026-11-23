package storagetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the post-trasher contract, far above SeedFixtures.
const (
	tzrP       int64 = 9800 // published post "tzr-doc": the row under test
	tzrSib     int64 = 9801 // published sibling in the same category
	tzrRev     int64 = 9802 // revision of P
	tzrChild   int64 = 9803 // child page of P
	tzrMenu    int64 = 9804 // nav_menu_item pointing at P
	tzrDraft   int64 = 9805 // draft named "draft-one"
	tzrEmpty   int64 = 9806 // draft with an empty name
	tzrLong    int64 = 9807 // published, 195-byte name
	tzrDone    int64 = 9808 // published, already named "x__trashed"
	tzrStale   int64 = 9809 // published, stale _wp_trash_meta_* rows
	tzrNoCom   int64 = 9810 // published, no comments
	tzrCom     int64 = 9811 // published, comments spam / post-trashed / 1 / 0
	tzrPct     int64 = 9812 // published, WordPress-style percent-encoded name
	tzrInfoDup int64 = 9813 // published, duplicate trash meta rows

	tzrTTCat  int64 = 9820 // category term_taxonomy, stale count
	tzrTTPC   int64 = 9821 // product_cat term_taxonomy, stale count
	tzrTTMenu int64 = 9822 // nav_menu term_taxonomy, stale count

	tzrCmt1 int64 = 9830 // P, approved "1"
	tzrCmt0 int64 = 9831 // P, approved "0"
	tzrCmtS int64 = 9832 // Sib, approved "1"
	tzrCmtA int64 = 9833 // tzrCom, spam
	tzrCmtB int64 = 9834 // tzrCom, post-trashed
	tzrCmtC int64 = 9835 // tzrCom, "1"
	tzrCmtD int64 = 9836 // tzrCom, "0"

	tzrFirstOther int64 = 9850 // ids at and above this are created by individual cases

	tzrStaleCount = 99
	tzrMenuCount  = 50
)

var (
	tzrAt = time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	// tzrRestoreAt is the restore time, one hour after the trash.
	tzrRestoreAt = tzrAt.Add(time.Hour)
)

const (
	tzrAtText        = "2024-05-06 07:08:09"
	tzrRestoreAtText = "2024-05-06 08:08:09"
	tzrSeedModified  = "2001-02-03 04:05:06"
)

// tzrPostCols are the posts columns every case compares. post_modified and
// post_modified_gmt are listed so the cases can name them as the only columns
// a trash or restore may touch besides post_status and post_name.
var tzrPostCols = []string{
	"post_author", "post_date", "post_date_gmt", "post_content", "post_title", "post_excerpt",
	"post_status", "post_name", "post_type", "comment_status", "ping_status", "post_password",
	"post_parent", "post_mime_type", "menu_order", "post_modified", "post_modified_gmt", "guid",
}

// tzrTrashKeys are the postmeta keys the trasher owns.
var tzrTrashKeys = []string{
	"_wp_trash_meta_status", "_wp_trash_meta_time", "_wp_desired_post_slug", "_wp_trash_meta_comments_status",
}

// RunPostTrasherContract covers domain.PostTrasher: TrashPost writes exactly
// the rows WordPress's wp_trash_post writes (status and name, three or four
// postmeta rows, the comments' approval state) and recomputes the term counts,
// RestorePost undoes them, and TrashInfo reads what was recorded. Atomicity has
// no portable failure injection and is covered on SQLite in
// internal/storage/wprepo/posttrash_test.go.
//
// The vendor and table prefix come from newRepos; the contract holds no literal
// prefix, vendor name or placeholder.
func RunPostTrasherContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	type env struct {
		db      *sql.DB
		vendor  string
		prefix  string
		trasher domain.PostTrasher
		cleanup func()
	}
	setup := func(t *testing.T) env {
		t.Helper()
		repos, vendor, prefix, cleanup := newRepos(t)
		e := env{db: repos.DB(), vendor: vendor, prefix: prefix, trasher: repos.PostTrash, cleanup: cleanup}
		tzrSeed(ctx, t, e.db, vendor, prefix)
		return e
	}
	// trashed builds a backend and trashes P at tzrAt.
	trashed := func(t *testing.T) env {
		t.Helper()
		e := setup(t)
		if _, err := e.trasher.TrashPost(ctx, tzrP, tzrAt); err != nil {
			e.cleanup()
			t.Fatalf("TrashPost(P): %v", err)
		}
		return e
	}
	exec := func(t *testing.T, e env, q string, args ...any) {
		t.Helper()
		if _, err := e.db.ExecContext(ctx, rebind.Rebind(e.vendor, q), args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	meta := func(t *testing.T, e env, id int64, key string) []string {
		t.Helper()
		return tzrMetaValues(ctx, t, e.db, e.vendor, e.prefix, id, key)
	}
	row := func(t *testing.T, e env, id int64) map[string]string {
		t.Helper()
		return tzrPostRow(ctx, t, e.db, e.vendor, e.prefix, id)
	}
	count := func(t *testing.T, e env, tt int64) string {
		t.Helper()
		return tzrScalar(ctx, t, e.db, e.vendor, `SELECT count FROM `+e.prefix+`term_taxonomy WHERE term_taxonomy_id = ?`, tt)
	}
	approved := func(t *testing.T, e env, id int64) string {
		t.Helper()
		return tzrScalar(ctx, t, e.db, e.vendor,
			`SELECT comment_approved FROM `+e.prefix+`comments WHERE `+rebind.Ident(e.vendor, "comment_ID")+` = ?`, id)
	}
	wantMeta := func(t *testing.T, e env, id int64, key string, want ...string) {
		t.Helper()
		got := meta(t, e, id, key)
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != len(want) {
			t.Errorf("postmeta(%d, %s) = %q, want %q", id, key, got, want)
		}
	}
	// diffRow reports every column of id that differs from before except the
	// named ones.
	diffRow := func(t *testing.T, label string, before, after map[string]string, except ...string) {
		t.Helper()
		skip := map[string]bool{}
		for _, c := range except {
			skip[c] = true
		}
		for _, c := range tzrPostCols {
			if !skip[c] && before[c] != after[c] {
				t.Errorf("%s: column %s = %q, want %q (unchanged)", label, c, after[c], before[c])
			}
		}
	}

	t.Run("TrashPost writes exactly the WordPress rows", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		before := row(t, e, tzrP)
		prev, err := e.trasher.TrashPost(ctx, tzrP, tzrAt)
		if err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		if prev != "publish" {
			t.Errorf("previous status = %q, want publish", prev)
		}
		after := row(t, e, tzrP)
		if after["post_status"] != "trash" {
			t.Errorf("post_status = %q, want trash", after["post_status"])
		}
		if after["post_name"] != "tzr-doc__trashed" {
			t.Errorf("post_name = %q, want tzr-doc__trashed", after["post_name"])
		}
		if after["post_modified"] != tzrAtText || after["post_modified_gmt"] != tzrAtText {
			t.Errorf("post_modified/_gmt = %q/%q, want %q", after["post_modified"], after["post_modified_gmt"], tzrAtText)
		}
		diffRow(t, "P", before, after, "post_status", "post_name", "post_modified", "post_modified_gmt")
		wantMeta(t, e, tzrP, "_wp_trash_meta_status", "publish")
		wantMeta(t, e, tzrP, "_wp_trash_meta_time", strconv.FormatInt(tzrAt.Unix(), 10))
		wantMeta(t, e, tzrP, "_wp_desired_post_slug", "tzr-doc")
		wantMeta(t, e, tzrP, "_wp_trash_meta_comments_status",
			fmt.Sprintf(`a:2:{i:%d;s:1:"1";i:%d;s:1:"0";}`, tzrCmt1, tzrCmt0))
		for _, id := range []int64{tzrCmt1, tzrCmt0} {
			if got := approved(t, e, id); got != "post-trashed" {
				t.Errorf("comment %d comment_approved = %q, want post-trashed", id, got)
			}
		}
		if got := approved(t, e, tzrCmtS); got != "1" {
			t.Errorf("sibling's comment comment_approved = %q, want 1 (unchanged)", got)
		}
	})

	t.Run("TrashPost changes nothing else", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		before := tzrDump(ctx, t, e.db, e.vendor, e.prefix, tzrP)
		beforeOther := meta(t, e, tzrP, "tzr_other")
		if _, err := e.trasher.TrashPost(ctx, tzrP, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		if after := tzrDump(ctx, t, e.db, e.vendor, e.prefix, tzrP); after != before {
			t.Errorf("rows outside the trash representation changed:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		wantMeta(t, e, tzrP, "tzr_other", beforeOther...)
		if len(beforeOther) != 1 {
			t.Fatalf("seed: tzr_other rows = %d, want 1", len(beforeOther))
		}
		// The revision, child page and menu item are neither trashed nor deleted.
		for name, id := range map[string]int64{"revision": tzrRev, "child page": tzrChild, "menu item": tzrMenu} {
			if n := tzrScalar(ctx, t, e.db, e.vendor,
				`SELECT COUNT(*) FROM `+e.prefix+`posts WHERE `+rebind.Ident(e.vendor, "ID")+` = ? AND post_status <> 'trash'`, id); n != "1" {
				t.Errorf("%s %d: live rows = %s, want 1", name, id, n)
			}
		}
		if got := row(t, e, tzrChild)["post_parent"]; got != strconv.FormatInt(tzrP, 10) {
			t.Errorf("child page post_parent = %q, want %d", got, tzrP)
		}
	})

	t.Run("TrashPost recomputes the counts of the row's terms only", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		if _, err := e.trasher.TrashPost(ctx, tzrP, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		// category: P is trashed, the sibling remains. product_cat: only P.
		if got := count(t, e, tzrTTCat); got != "1" {
			t.Errorf("category count = %s, want 1", got)
		}
		if got := count(t, e, tzrTTPC); got != "0" {
			t.Errorf("product_cat count = %s, want 0 (every taxonomy, not only category and post_tag)", got)
		}
		if got := count(t, e, tzrTTMenu); got != strconv.Itoa(tzrMenuCount) {
			t.Errorf("nav_menu count = %s, want %d (not a term of P, untouched)", got, tzrMenuCount)
		}
	})

	t.Run("TrashPost returns the row's own status as the previous status", func(t *testing.T) {
		for i, status := range []string{"publish", "draft", "pending", "private", "future"} {
			e := setup(t)
			id := tzrFirstOther + int64(i)
			tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, id, "st-"+status, "post", status, 0)
			prev, err := e.trasher.TrashPost(ctx, id, tzrAt)
			if err != nil {
				e.cleanup()
				t.Fatalf("TrashPost(%s): %v", status, err)
			}
			if prev != status {
				t.Errorf("previous status = %q, want %q", prev, status)
			}
			wantMeta(t, e, id, "_wp_trash_meta_status", status)
			wantMeta(t, e, id, "_wp_desired_post_slug", "st-"+status)
			e.cleanup()
		}
	})

	t.Run("TrashPost on a draft", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		prev, err := e.trasher.TrashPost(ctx, tzrDraft, tzrAt)
		if err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		if prev != "draft" {
			t.Errorf("previous status = %q, want draft", prev)
		}
		if got := row(t, e, tzrDraft)["post_name"]; got != "draft-one__trashed" {
			t.Errorf("post_name = %q, want draft-one__trashed", got)
		}
		wantMeta(t, e, tzrDraft, "_wp_trash_meta_status", "draft")
	})

	t.Run("an empty name becomes __trashed and is recorded as empty", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		if _, err := e.trasher.TrashPost(ctx, tzrEmpty, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		if got := row(t, e, tzrEmpty)["post_name"]; got != "__trashed" {
			t.Errorf("post_name = %q, want __trashed", got)
		}
		wantMeta(t, e, tzrEmpty, "_wp_desired_post_slug", "")
	})

	t.Run("a 195-byte name is cut to 191 bytes before the suffix and recorded whole", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		long := strings.Repeat("a", 195)
		if _, err := e.trasher.TrashPost(ctx, tzrLong, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		got := row(t, e, tzrLong)["post_name"]
		if want := strings.Repeat("a", 191) + "__trashed"; got != want {
			t.Errorf("post_name = %q (%d bytes), want 191 bytes + __trashed", got, len(got))
		}
		if len(got) > 200 {
			t.Errorf("post_name is %d bytes, want at most 200", len(got))
		}
		wantMeta(t, e, tzrLong, "_wp_desired_post_slug", long)
	})

	t.Run("a name already ending in __trashed is kept and no desired slug is written", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		if _, err := e.trasher.TrashPost(ctx, tzrDone, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		r := row(t, e, tzrDone)
		if r["post_name"] != "x__trashed" || r["post_status"] != "trash" {
			t.Errorf("name/status = %q/%q, want x__trashed/trash", r["post_name"], r["post_status"])
		}
		wantMeta(t, e, tzrDone, "_wp_desired_post_slug")
		wantMeta(t, e, tzrDone, "_wp_trash_meta_status", "publish")
	})

	t.Run("stale trash meta rows are replaced, not duplicated", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		if _, err := e.trasher.TrashPost(ctx, tzrStale, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		wantMeta(t, e, tzrStale, "_wp_trash_meta_status", "publish")
		wantMeta(t, e, tzrStale, "_wp_trash_meta_time", strconv.FormatInt(tzrAt.Unix(), 10))
		wantMeta(t, e, tzrStale, "_wp_desired_post_slug", "stale")
		info, err := e.trasher.TrashInfo(ctx, tzrStale)
		if err != nil {
			t.Fatalf("TrashInfo: %v", err)
		}
		if info.PreviousStatus != "publish" || !info.TrashedAt.Equal(tzrAt) {
			t.Errorf("TrashInfo = %+v, want publish at %v (the stale first rows must not win)", info, tzrAt)
		}
	})

	t.Run("a row without comments gets no comment-status meta", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		before := tzrScalar(ctx, t, e.db, e.vendor, `SELECT COUNT(*) FROM `+e.prefix+`comments`)
		if _, err := e.trasher.TrashPost(ctx, tzrNoCom, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		wantMeta(t, e, tzrNoCom, "_wp_trash_meta_comments_status")
		if after := tzrScalar(ctx, t, e.db, e.vendor, `SELECT COUNT(*) FROM `+e.prefix+`comments`); after != before {
			t.Errorf("comment rows = %s, want %s", after, before)
		}
	})

	t.Run("a missing id returns ErrNotFound and changes nothing", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		before := tzrDump(ctx, t, e.db, e.vendor, e.prefix, 0)
		if _, err := e.trasher.TrashPost(ctx, 999999, tzrAt); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("TrashPost(missing) = %v, want domain.ErrNotFound", err)
		}
		if after := tzrDump(ctx, t, e.db, e.vendor, e.prefix, 0); after != before {
			t.Errorf("rows changed after ErrNotFound:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("an already trashed id returns ErrAlreadyTrashed and changes nothing", func(t *testing.T) {
		e := trashed(t)
		defer e.cleanup()
		before := tzrDump(ctx, t, e.db, e.vendor, e.prefix, 0)
		_, err := e.trasher.TrashPost(ctx, tzrP, tzrAt.Add(24*time.Hour))
		if !errors.Is(err, domain.ErrAlreadyTrashed) {
			t.Fatalf("second TrashPost = %v, want domain.ErrAlreadyTrashed", err)
		}
		if errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ErrAlreadyTrashed must be distinct from ErrNotFound")
		}
		if after := tzrDump(ctx, t, e.db, e.vendor, e.prefix, 0); after != before {
			t.Errorf("rows changed after ErrAlreadyTrashed:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("RestorePost restores a row trashed with the desired slug", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		before := row(t, e, tzrP)
		if _, err := e.trasher.TrashPost(ctx, tzrP, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		if err := e.trasher.RestorePost(ctx, tzrP, domain.RestoreRequest{Status: "publish", At: tzrRestoreAt}); err != nil {
			t.Fatalf("RestorePost: %v", err)
		}
		after := row(t, e, tzrP)
		if after["post_status"] != "publish" || after["post_name"] != "tzr-doc" {
			t.Errorf("status/name = %q/%q, want publish/tzr-doc", after["post_status"], after["post_name"])
		}
		if after["post_modified"] != tzrRestoreAtText || after["post_modified_gmt"] != tzrRestoreAtText {
			t.Errorf("post_modified/_gmt = %q/%q, want %q", after["post_modified"], after["post_modified_gmt"], tzrRestoreAtText)
		}
		diffRow(t, "P", before, after, "post_modified", "post_modified_gmt")
		for _, k := range tzrTrashKeys {
			wantMeta(t, e, tzrP, k)
		}
		wantMeta(t, e, tzrP, "tzr_other", "keep")
		if got := approved(t, e, tzrCmt1); got != "1" {
			t.Errorf("comment %d comment_approved = %q, want 1", tzrCmt1, got)
		}
		if got := approved(t, e, tzrCmt0); got != "0" {
			t.Errorf("comment %d comment_approved = %q, want 0", tzrCmt0, got)
		}
		if got := count(t, e, tzrTTCat); got != "2" {
			t.Errorf("category count = %s, want 2", got)
		}
		if got := count(t, e, tzrTTPC); got != "1" {
			t.Errorf("product_cat count = %s, want 1", got)
		}
		if got := count(t, e, tzrTTMenu); got != strconv.Itoa(tzrMenuCount) {
			t.Errorf("nav_menu count = %s, want %d (untouched)", got, tzrMenuCount)
		}
	})

	t.Run("RestorePost restores to the status given", func(t *testing.T) {
		for _, status := range []string{"publish", "draft"} {
			e := trashed(t)
			if err := e.trasher.RestorePost(ctx, tzrP, domain.RestoreRequest{Status: status, At: tzrRestoreAt}); err != nil {
				e.cleanup()
				t.Fatalf("RestorePost(%s): %v", status, err)
			}
			if got := row(t, e, tzrP)["post_status"]; got != status {
				t.Errorf("post_status = %q, want %q", got, status)
			}
			e.cleanup()
		}
	})

	t.Run("RestorePost without the desired-slug meta strips the suffix", func(t *testing.T) {
		e := trashed(t)
		defer e.cleanup()
		exec(t, e, `DELETE FROM `+e.prefix+`postmeta WHERE post_id = ? AND meta_key = ?`, tzrP, "_wp_desired_post_slug")
		if err := e.trasher.RestorePost(ctx, tzrP, domain.RestoreRequest{Status: "draft", At: tzrRestoreAt}); err != nil {
			t.Fatalf("RestorePost: %v", err)
		}
		if got := row(t, e, tzrP)["post_name"]; got != "tzr-doc" {
			t.Errorf("post_name = %q, want tzr-doc", got)
		}
	})

	slugCases := []struct {
		name  string
		setup func(t *testing.T, e env)
		req   domain.RestoreRequest
		want  string
	}{
		{
			name: "a live row of the same type holds the slug",
			setup: func(t *testing.T, e env) {
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+20, "tzr-doc", "post", "publish", 0)
			},
			req:  domain.RestoreRequest{Status: "publish", At: tzrRestoreAt},
			want: "tzr-doc-2",
		},
		{
			name: "the first suffix is taken as well",
			setup: func(t *testing.T, e env) {
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+20, "tzr-doc", "post", "publish", 0)
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+21, "tzr-doc-2", "post", "draft", 0)
			},
			req:  domain.RestoreRequest{Status: "publish", At: tzrRestoreAt},
			want: "tzr-doc-3",
		},
		{
			name: "a live row of another type holds the slug",
			setup: func(t *testing.T, e env) {
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+20, "tzr-doc", "page", "publish", 0)
			},
			req:  domain.RestoreRequest{Status: "publish", At: tzrRestoreAt},
			want: "tzr-doc",
		},
		{
			name: "another trashed row holds the bare slug",
			setup: func(t *testing.T, e env) {
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+20, "tzr-doc", "post", "trash", 0)
			},
			req:  domain.RestoreRequest{Status: "publish", At: tzrRestoreAt},
			want: "tzr-doc",
		},
		{
			name: "a hierarchical scope ignores a live row under another parent",
			setup: func(t *testing.T, e env) {
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+20, "tzr-doc", "post", "publish", tzrChild)
			},
			req:  domain.RestoreRequest{Status: "publish", HierarchicalScope: true, At: tzrRestoreAt},
			want: "tzr-doc",
		},
		{
			name: "a hierarchical scope counts a live row under the same parent",
			setup: func(t *testing.T, e env) {
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+20, "tzr-doc", "post", "publish", 0)
			},
			req:  domain.RestoreRequest{Status: "publish", HierarchicalScope: true, At: tzrRestoreAt},
			want: "tzr-doc-2",
		},
		{
			name: "a flat scope counts a live row under any parent",
			setup: func(t *testing.T, e env) {
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+20, "tzr-doc", "post", "publish", tzrChild)
			},
			req:  domain.RestoreRequest{Status: "publish", At: tzrRestoreAt},
			want: "tzr-doc-2",
		},
		{
			name: "an explicit slug replaces the desired slug",
			setup: func(t *testing.T, e env) {
			},
			req:  domain.RestoreRequest{Status: "draft", Slug: "chosen-slug", At: tzrRestoreAt},
			want: "chosen-slug",
		},
		{
			name: "an explicit slug is made unique too",
			setup: func(t *testing.T, e env) {
				tzrInsertPost(ctx, t, e.db, e.vendor, e.prefix, tzrFirstOther+20, "chosen-slug", "post", "publish", 0)
			},
			req:  domain.RestoreRequest{Status: "publish", Slug: "chosen-slug", At: tzrRestoreAt},
			want: "chosen-slug-2",
		},
	}
	for _, tc := range slugCases {
		t.Run("RestorePost slug: "+tc.name, func(t *testing.T) {
			e := trashed(t)
			defer e.cleanup()
			tc.setup(t, e)
			if err := e.trasher.RestorePost(ctx, tzrP, tc.req); err != nil {
				t.Fatalf("RestorePost: %v", err)
			}
			if got := row(t, e, tzrP)["post_name"]; got != tc.want {
				t.Errorf("post_name = %q, want %q", got, tc.want)
			}
			wantMeta(t, e, tzrP, "_wp_desired_post_slug")
		})
	}

	t.Run("RestorePost keeps a WordPress-style percent-encoded name unchanged", func(t *testing.T) {
		for _, dropDesired := range []bool{false, true} {
			e := setup(t)
			if _, err := e.trasher.TrashPost(ctx, tzrPct, tzrAt); err != nil {
				e.cleanup()
				t.Fatalf("TrashPost: %v", err)
			}
			if dropDesired {
				exec(t, e, `DELETE FROM `+e.prefix+`postmeta WHERE post_id = ? AND meta_key = ?`, tzrPct, "_wp_desired_post_slug")
			}
			if err := e.trasher.RestorePost(ctx, tzrPct, domain.RestoreRequest{Status: "publish", At: tzrRestoreAt}); err != nil {
				e.cleanup()
				t.Fatalf("RestorePost: %v", err)
			}
			if got := row(t, e, tzrPct)["post_name"]; got != "caf%c3%a9-%e2%80%94" {
				t.Errorf("dropDesired=%v: post_name = %q, want the original percent-encoded name", dropDesired, got)
			}
			e.cleanup()
		}
	})

	t.Run("RestorePost restores each comment to the status recorded for it", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		if _, err := e.trasher.TrashPost(ctx, tzrCom, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		for _, id := range []int64{tzrCmtA, tzrCmtB, tzrCmtC, tzrCmtD} {
			if got := approved(t, e, id); got != "post-trashed" {
				t.Fatalf("after trash: comment %d = %q, want post-trashed", id, got)
			}
		}
		// A recorded comment that has since been deleted is skipped.
		exec(t, e, `DELETE FROM `+e.prefix+`comments WHERE `+rebind.Ident(e.vendor, "comment_ID")+` = ?`, tzrCmtD)
		if err := e.trasher.RestorePost(ctx, tzrCom, domain.RestoreRequest{Status: "publish", At: tzrRestoreAt}); err != nil {
			t.Fatalf("RestorePost: %v", err)
		}
		for id, want := range map[int64]string{tzrCmtA: "spam", tzrCmtB: "0", tzrCmtC: "1"} {
			if got := approved(t, e, id); got != want {
				t.Errorf("comment %d comment_approved = %q, want %q", id, got, want)
			}
		}
		if n := tzrScalar(ctx, t, e.db, e.vendor, `SELECT COUNT(*) FROM `+e.prefix+`comments WHERE `+rebind.Ident(e.vendor, "comment_ID")+` = ?`, tzrCmtD); n != "0" {
			t.Errorf("deleted comment was recreated: rows = %s", n)
		}
		wantMeta(t, e, tzrCom, "_wp_trash_meta_comments_status")
	})

	t.Run("RestorePost with unreadable comment-status meta sets the post-trashed comments to 0", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		if _, err := e.trasher.TrashPost(ctx, tzrCom, tzrAt); err != nil {
			t.Fatalf("TrashPost: %v", err)
		}
		exec(t, e, `UPDATE `+e.prefix+`postmeta SET meta_value = ? WHERE post_id = ? AND meta_key = ?`,
			"not serialized", tzrCom, "_wp_trash_meta_comments_status")
		if err := e.trasher.RestorePost(ctx, tzrCom, domain.RestoreRequest{Status: "publish", At: tzrRestoreAt}); err != nil {
			t.Fatalf("RestorePost: %v", err)
		}
		for _, id := range []int64{tzrCmtA, tzrCmtB, tzrCmtC, tzrCmtD} {
			if got := approved(t, e, id); got != "0" {
				t.Errorf("comment %d comment_approved = %q, want 0", id, got)
			}
		}
		wantMeta(t, e, tzrCom, "_wp_trash_meta_comments_status")
	})

	t.Run("RestorePost on a live row returns ErrNotTrashed and changes nothing", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		before := tzrDump(ctx, t, e.db, e.vendor, e.prefix, 0)
		err := e.trasher.RestorePost(ctx, tzrSib, domain.RestoreRequest{Status: "publish", At: tzrRestoreAt})
		if !errors.Is(err, domain.ErrNotTrashed) {
			t.Fatalf("RestorePost(live) = %v, want domain.ErrNotTrashed", err)
		}
		if errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ErrNotTrashed must be distinct from ErrNotFound")
		}
		if after := tzrDump(ctx, t, e.db, e.vendor, e.prefix, 0); after != before {
			t.Errorf("rows changed after ErrNotTrashed:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("RestorePost on a missing id returns ErrNotFound", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		err := e.trasher.RestorePost(ctx, 999999, domain.RestoreRequest{Status: "publish", At: tzrRestoreAt})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("RestorePost(missing) = %v, want domain.ErrNotFound", err)
		}
	})

	t.Run("TrashInfo reads what TrashPost recorded", func(t *testing.T) {
		e := trashed(t)
		defer e.cleanup()
		info, err := e.trasher.TrashInfo(ctx, tzrP)
		if err != nil {
			t.Fatalf("TrashInfo: %v", err)
		}
		if !info.HasPreviousStatus || info.PreviousStatus != "publish" {
			t.Errorf("previous status = %q (has %v), want publish", info.PreviousStatus, info.HasPreviousStatus)
		}
		if !info.HasTrashedAt || info.TrashedAt.Unix() != tzrAt.Unix() {
			t.Errorf("trashed at = %v (has %v), want %v", info.TrashedAt, info.HasTrashedAt, tzrAt)
		}
		if !info.HasDesiredSlug || info.DesiredSlug != "tzr-doc" {
			t.Errorf("desired slug = %q (has %v), want tzr-doc", info.DesiredSlug, info.HasDesiredSlug)
		}
	})

	t.Run("TrashInfo reports each absent value as absent", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		info, err := e.trasher.TrashInfo(ctx, tzrSib)
		if err != nil {
			t.Fatalf("TrashInfo(live row): %v", err)
		}
		if info != (domain.TrashInfo{}) {
			t.Errorf("TrashInfo(live row) = %+v, want the zero value", info)
		}
		if _, err := e.trasher.TrashInfo(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("TrashInfo(missing) = %v, want domain.ErrNotFound", err)
		}
	})

	t.Run("TrashInfo reads the lowest meta_id when a key repeats", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		info, err := e.trasher.TrashInfo(ctx, tzrInfoDup)
		if err != nil {
			t.Fatalf("TrashInfo: %v", err)
		}
		if info.PreviousStatus != "draft" || !info.HasPreviousStatus {
			t.Errorf("previous status = %q, want draft (first row)", info.PreviousStatus)
		}
		if !info.HasTrashedAt || info.TrashedAt.Unix() != 1700000000 {
			t.Errorf("trashed at = %v, want unix 1700000000 (first row)", info.TrashedAt)
		}
		if info.DesiredSlug != "first-slug" || !info.HasDesiredSlug {
			t.Errorf("desired slug = %q, want first-slug (first row)", info.DesiredSlug)
		}
	})
}

// tzrSeed inserts the post-trasher fixtures: P with relationships, comments,
// a revision, a child page, a menu item and stale term counts, plus the rows of
// the individual cases.
func tzrSeed(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	metaRow := func(id int64, key, value string) {
		t.Helper()
		exec(`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, id, key, value)
	}
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrP, "tzr-doc", "post", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrSib, "tzr-sibling", "post", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrRev, strconv.FormatInt(tzrP, 10)+"-revision-v1", "revision", "inherit", tzrP)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrChild, "tzr-child", "page", "publish", tzrP)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrMenu, strconv.FormatInt(tzrMenu, 10), "nav_menu_item", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrDraft, "draft-one", "post", "draft", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrEmpty, "", "post", "draft", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrLong, strings.Repeat("a", 195), "post", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrDone, "x__trashed", "post", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrStale, "stale", "post", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrNoCom, "no-comments", "post", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrCom, "with-comments", "post", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrPct, "caf%c3%a9-%e2%80%94", "post", "publish", 0)
	tzrInsertPost(ctx, t, db, vendor, prefix, tzrInfoDup, "dup-meta", "post", "publish", 0)

	metaRow(tzrP, "tzr_other", "keep")
	metaRow(tzrMenu, "_menu_item_object_id", strconv.FormatInt(tzrP, 10))
	metaRow(tzrMenu, "_menu_item_object", "post")
	metaRow(tzrStale, "_wp_trash_meta_status", "draft")
	metaRow(tzrStale, "_wp_trash_meta_time", "1")
	// Duplicate rows: the lowest meta_id is inserted first.
	metaRow(tzrInfoDup, "_wp_trash_meta_status", "draft")
	metaRow(tzrInfoDup, "_wp_trash_meta_status", "private")
	metaRow(tzrInfoDup, "_wp_trash_meta_time", "1700000000")
	metaRow(tzrInfoDup, "_wp_trash_meta_time", "1800000000")
	metaRow(tzrInfoDup, "_wp_desired_post_slug", "first-slug")
	metaRow(tzrInfoDup, "_wp_desired_post_slug", "second-slug")

	seedTermTaxonomy(ctx, t, db, vendor, prefix, tzrTTCat, "category", tzrStaleCount)
	seedTermTaxonomy(ctx, t, db, vendor, prefix, tzrTTPC, "product_cat", tzrStaleCount)
	seedTermTaxonomy(ctx, t, db, vendor, prefix, tzrTTMenu, "nav_menu", tzrMenuCount)
	rel := func(obj, tt int64) {
		t.Helper()
		exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, obj, tt, 0)
	}
	rel(tzrP, tzrTTCat)
	rel(tzrP, tzrTTPC)
	rel(tzrSib, tzrTTCat)
	rel(tzrMenu, tzrTTMenu)

	comment := func(id, postID int64, approved string) {
		t.Helper()
		exec(commentInsert(vendor, prefix), id, postID, "A", "a@example.test", "", "", "2000-01-01 00:00:00", "2000-01-01 00:00:00", "c", approved, "", 0, 0)
		exec(`INSERT INTO `+prefix+`commentmeta (comment_id, meta_key, meta_value) VALUES (?, ?, ?)`, id, "tzr_cm", "v"+strconv.FormatInt(id, 10))
	}
	comment(tzrCmt1, tzrP, "1")
	comment(tzrCmt0, tzrP, "0")
	comment(tzrCmtS, tzrSib, "1")
	comment(tzrCmtA, tzrCom, "spam")
	comment(tzrCmtB, tzrCom, "post-trashed")
	comment(tzrCmtC, tzrCom, "1")
	comment(tzrCmtD, tzrCom, "0")
}

// tzrInsertPost inserts a post with a fixed modified time and guid so a changed
// post_modified is visible.
func tzrInsertPost(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, id int64, name, ptype, status string, parent int64) {
	t.Helper()
	q := rebind.Rebind(vendor, postInsert(vendor, prefix))
	if _, err := db.ExecContext(ctx, q, postArgs(id, name, "T "+strconv.FormatInt(id, 10), ptype, status, "2000-01-01 00:00:00", "open", parent, "", 3)...); err != nil {
		t.Fatalf("seed post %d: %v", id, err)
	}
	if _, err := db.ExecContext(ctx, rebind.Rebind(vendor,
		`UPDATE `+prefix+`posts SET post_modified = ?, post_modified_gmt = ?, guid = ? WHERE `+rebind.Ident(vendor, "ID")+` = ?`),
		tzrSeedModified, tzrSeedModified, "http://example.test/?p="+strconv.FormatInt(id, 10), id); err != nil {
		t.Fatalf("seed post %d modified: %v", id, err)
	}
}

// tzrNorm renders a scanned column value as text, so the same comparison holds
// whether the driver returns text, bytes, integers or times.
func tzrNorm(v any) string {
	switch x := v.(type) {
	case nil:
		return "<nil>"
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprint(x)
	}
}

// tzrRows runs q and returns every row as normalised text columns.
func tzrRows(ctx context.Context, t *testing.T, db *sql.DB, vendor, q string, args ...any) [][]string {
	t.Helper()
	rows, err := db.QueryContext(ctx, rebind.Rebind(vendor, q), args...)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns %q: %v", q, err)
	}
	var out [][]string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %q: %v", q, err)
		}
		line := make([]string, len(cols))
		for i, v := range vals {
			line[i] = tzrNorm(v)
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows %q: %v", q, err)
	}
	return out
}

func tzrScalar(ctx context.Context, t *testing.T, db *sql.DB, vendor, q string, args ...any) string {
	t.Helper()
	rows := tzrRows(ctx, t, db, vendor, q, args...)
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("scalar %q returned %v, want one value", q, rows)
	}
	return rows[0][0]
}

func tzrPostRow(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, id int64) map[string]string {
	t.Helper()
	rows := tzrRows(ctx, t, db, vendor,
		`SELECT `+rebind.IdentList(vendor, tzrPostCols)+` FROM `+prefix+`posts WHERE `+rebind.Ident(vendor, "ID")+` = ?`, id)
	if len(rows) != 1 {
		t.Fatalf("post %d: %d rows, want 1", id, len(rows))
	}
	out := make(map[string]string, len(tzrPostCols))
	for i, c := range tzrPostCols {
		out[c] = rows[0][i]
	}
	return out
}

// tzrMetaValues returns the meta_value of every (id, key) row, lowest meta_id first.
func tzrMetaValues(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, id int64, key string) []string {
	t.Helper()
	var out []string
	for _, r := range tzrRows(ctx, t, db, vendor,
		`SELECT meta_value FROM `+prefix+`postmeta WHERE post_id = ? AND meta_key = ? ORDER BY meta_id`, id, key) {
		out = append(out, r[0])
	}
	return out
}

// tzrDump renders every fixture row outside the trash representation of
// exclude: the posts, postmeta, comments, commentmeta, relationships and term
// counts. With exclude 0 it renders everything, so two dumps differ if any
// fixture row changed.
func tzrDump(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, exclude int64) string {
	t.Helper()
	idCol := rebind.Ident(vendor, "ID")
	cid := rebind.Ident(vendor, "comment_ID")
	cpid := rebind.Ident(vendor, "comment_post_ID")
	var b strings.Builder
	section := func(name string, rows [][]string) {
		b.WriteString("[" + name + "]\n")
		for _, r := range rows {
			b.WriteString(strings.Join(r, " | ") + "\n")
		}
	}
	section("posts", tzrRows(ctx, t, db, vendor,
		`SELECT `+idCol+`, `+rebind.IdentList(vendor, tzrPostCols)+` FROM `+prefix+`posts WHERE `+idCol+` >= ? AND `+idCol+` <> ? ORDER BY `+idCol,
		tzrP, exclude))
	keys := "'" + strings.Join(tzrTrashKeys, "', '") + "'"
	section("postmeta", tzrRows(ctx, t, db, vendor,
		`SELECT post_id, meta_key, meta_value FROM `+prefix+`postmeta WHERE post_id >= ? AND NOT (post_id = ? AND meta_key IN (`+keys+`)) ORDER BY post_id, meta_key, meta_value`,
		tzrP, exclude))
	section("comments", tzrRows(ctx, t, db, vendor,
		`SELECT `+cid+`, `+cpid+`, comment_approved, comment_content FROM `+prefix+`comments WHERE `+cid+` >= ? AND `+cpid+` <> ? ORDER BY `+cid,
		tzrCmt1, exclude))
	section("commentmeta", tzrRows(ctx, t, db, vendor,
		`SELECT comment_id, meta_key, meta_value FROM `+prefix+`commentmeta WHERE comment_id >= ? ORDER BY comment_id, meta_key`, tzrCmt1))
	section("relationships", tzrRows(ctx, t, db, vendor,
		`SELECT object_id, term_taxonomy_id FROM `+prefix+`term_relationships WHERE object_id >= ? ORDER BY object_id, term_taxonomy_id`, tzrP))
	counts := `SELECT term_taxonomy_id, count FROM ` + prefix + `term_taxonomy WHERE term_taxonomy_id >= ? ORDER BY term_taxonomy_id`
	if exclude != 0 {
		// P's own terms are recomputed by design; the other terms must not move.
		counts = `SELECT term_taxonomy_id, count FROM ` + prefix + `term_taxonomy WHERE term_taxonomy_id >= ? AND term_taxonomy_id NOT IN (` +
			strconv.FormatInt(tzrTTCat, 10) + `, ` + strconv.FormatInt(tzrTTPC, 10) + `) ORDER BY term_taxonomy_id`
	}
	section("term_taxonomy", tzrRows(ctx, t, db, vendor, counts, tzrTTCat))
	return b.String()
}
