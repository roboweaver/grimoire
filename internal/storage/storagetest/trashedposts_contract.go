package storagetest

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the trashed-post lister contract, far above SeedFixtures and
// the other contracts' ranges.
const (
	tplPost     int64 = 9900 // trashed post, meta 1700000000
	tplPage     int64 = 9901 // trashed page, meta 1700000100
	tplProduct  int64 = 9902 // trashed product (a managed custom type), meta 1700000200
	tplOrder    int64 = 9903 // trashed shop_order (not in the managed list), meta 1700000300
	tplNoMeta   int64 = 9904 // trashed post without _wp_trash_meta_time
	tplBadMeta  int64 = 9905 // trashed post whose meta value is not a number
	tplDupMeta  int64 = 9906 // trashed post with two meta rows, lowest meta_id first
	tplLive     int64 = 9907 // published post that carries a stale trash time
	tplDraft    int64 = 9908 // draft post that carries a stale trash time
	tplLiveDone int64 = 9909 // pending post, no meta
)

// RunTrashedPostListerContract covers domain.TrashedPostLister: it returns
// trashed rows of the requested types in ascending id order after a cursor,
// each with the lowest-meta_id _wp_trash_meta_time parsed in Go (a value that is
// not a number yields HasTrashedAt false and no error), and never returns a row
// whose status is not trash.
//
// The vendor and table prefix come from newRepos; the contract holds no literal
// prefix, vendor name or placeholder.
func RunTrashedPostListerContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()
	managed := []string{"post", "page", "product"}

	type env struct {
		lister  domain.TrashedPostLister
		cleanup func()
	}
	setup := func(t *testing.T) env {
		t.Helper()
		repos, vendor, prefix, cleanup := newRepos(t)
		tplSeed(ctx, t, repos.DB(), vendor, prefix)
		return env{lister: repos.TrashedPosts, cleanup: cleanup}
	}
	ids := func(rows []domain.TrashedPost) []int64 {
		out := make([]int64, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Post.ID)
		}
		return out
	}
	byID := func(rows []domain.TrashedPost, id int64) (domain.TrashedPost, bool) {
		for _, r := range rows {
			if r.Post.ID == id {
				return r, true
			}
		}
		return domain.TrashedPost{}, false
	}

	t.Run("managed types only, ascending, non-trashed never returned", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		got, err := e.lister.TrashedPosts(ctx, managed, 0, 100)
		if err != nil {
			t.Fatalf("TrashedPosts: %v", err)
		}
		want := []int64{tplPost, tplPage, tplProduct, tplNoMeta, tplBadMeta, tplDupMeta}
		if !reflect.DeepEqual(ids(got), want) {
			t.Fatalf("ids = %v, want %v", ids(got), want)
		}
		for _, r := range got {
			if r.Post.Status != "trash" {
				t.Fatalf("row %d status = %q, want trash", r.Post.ID, r.Post.Status)
			}
		}
	})

	t.Run("unmanaged type is returned only when asked for", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		got, err := e.lister.TrashedPosts(ctx, []string{"shop_order"}, 0, 100)
		if err != nil {
			t.Fatalf("TrashedPosts: %v", err)
		}
		if !reflect.DeepEqual(ids(got), []int64{tplOrder}) {
			t.Fatalf("ids = %v, want [%d]", ids(got), tplOrder)
		}
	})

	t.Run("trash time parsed, missing and non-numeric meta, lowest meta_id wins", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		got, err := e.lister.TrashedPosts(ctx, managed, 0, 100)
		if err != nil {
			t.Fatalf("TrashedPosts: %v", err)
		}
		cases := []struct {
			id  int64
			has bool
			at  int64
		}{
			{tplPost, true, 1700000000},
			{tplPage, true, 1700000100},
			{tplProduct, true, 1700000200},
			{tplNoMeta, false, 0},
			{tplBadMeta, false, 0},
			{tplDupMeta, true, 1700000001},
		}
		for _, c := range cases {
			r, ok := byID(got, c.id)
			if !ok {
				t.Fatalf("row %d missing from %v", c.id, ids(got))
			}
			if r.HasTrashedAt != c.has {
				t.Fatalf("row %d HasTrashedAt = %v, want %v", c.id, r.HasTrashedAt, c.has)
			}
			if c.has && !r.TrashedAt.Equal(time.Unix(c.at, 0)) {
				t.Fatalf("row %d TrashedAt = %v, want unix %d", c.id, r.TrashedAt, c.at)
			}
		}
		if r, _ := byID(got, tplPage); r.Post.Type != "page" {
			t.Fatalf("page row type = %q, want page", r.Post.Type)
		}
	})

	t.Run("pagination across the cursor with limit 2", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		var all []int64
		var after int64
		for i := 0; i < 10; i++ {
			page, err := e.lister.TrashedPosts(ctx, managed, after, 2)
			if err != nil {
				t.Fatalf("TrashedPosts after %d: %v", after, err)
			}
			if len(page) == 0 {
				break
			}
			if len(page) > 2 {
				t.Fatalf("page has %d rows, limit is 2", len(page))
			}
			all = append(all, ids(page)...)
			after = page[len(page)-1].Post.ID
		}
		want := []int64{tplPost, tplPage, tplProduct, tplNoMeta, tplBadMeta, tplDupMeta}
		if !reflect.DeepEqual(all, want) {
			t.Fatalf("paged ids = %v, want %v", all, want)
		}
	})

	t.Run("cursor is exclusive", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		got, err := e.lister.TrashedPosts(ctx, managed, tplProduct, 100)
		if err != nil {
			t.Fatalf("TrashedPosts: %v", err)
		}
		if want := []int64{tplNoMeta, tplBadMeta, tplDupMeta}; !reflect.DeepEqual(ids(got), want) {
			t.Fatalf("ids = %v, want %v", ids(got), want)
		}
	})

	t.Run("empty types returns nothing and no error", func(t *testing.T) {
		e := setup(t)
		defer e.cleanup()
		got, err := e.lister.TrashedPosts(ctx, nil, 0, 100)
		if err != nil {
			t.Fatalf("TrashedPosts(nil): %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("rows = %v, want none", ids(got))
		}
	})
}

// tplSeed inserts the lister fixtures through plain SQL.
func tplSeed(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	post := func(id int64, ptype, status string) {
		t.Helper()
		name := "tpl-" + strconv.FormatInt(id, 10)
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, postInsert(vendor, prefix)),
			postArgs(id, name, "T "+name, ptype, status, "2000-01-01 00:00:00", "open", 0, "", 0)...); err != nil {
			t.Fatalf("seed post %d: %v", id, err)
		}
	}
	meta := func(id int64, value string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor,
			`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`),
			id, "_wp_trash_meta_time", value); err != nil {
			t.Fatalf("seed meta %d: %v", id, err)
		}
	}
	post(tplPost, "post", "trash")
	post(tplPage, "page", "trash")
	post(tplProduct, "product", "trash")
	post(tplOrder, "shop_order", "trash")
	post(tplNoMeta, "post", "trash")
	post(tplBadMeta, "post", "trash")
	post(tplDupMeta, "post", "trash")
	post(tplLive, "post", "publish")
	post(tplDraft, "post", "draft")
	post(tplLiveDone, "post", "pending")

	meta(tplPost, "1700000000")
	meta(tplPage, "1700000100")
	meta(tplProduct, "1700000200")
	meta(tplOrder, "1700000300")
	meta(tplBadMeta, "not-a-number")
	// Duplicate rows: the lowest meta_id is inserted first.
	meta(tplDupMeta, "1700000001")
	meta(tplDupMeta, "1800000000")
	// Rows that are not trashed carry a trash time that must not surface them.
	meta(tplLive, "1600000000")
	meta(tplDraft, "1600000000")
}
