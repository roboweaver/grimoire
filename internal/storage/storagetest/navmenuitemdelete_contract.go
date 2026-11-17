package storagetest

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the nav-menu item delete contract, far above SeedFixtures.
const (
	ndMA  int64 = 9400 // nav_menu term A
	ndMB  int64 = 9401 // nav_menu term B
	ndTTA int64 = 9410 // A term_taxonomy_id
	ndTTB int64 = 9411 // B term_taxonomy_id
	ndD   int64 = 9420 // top-level item of A, deleted
	ndC1  int64 = 9421 // child of D
	ndP0  int64 = 9422 // top-level item of A, kept
	ndD2  int64 = 9423 // child of P0, deleted
	ndC2  int64 = 9424 // child of D2
	ndZ   int64 = 9425 // item of B only
	ndT   int64 = 9426 // item related to both A and B
)

// RunNavMenuDeleteItemContract covers NavMenuWriter.DeleteItem: children of the
// deleted item take its menu parent (0 for a top-level item), every affected
// menu's term_taxonomy.count is recomputed from its relationships, an item that
// does not belong to the menu returns domain.ErrNotFound with every row
// unchanged, and an item related to two nav_menu term_taxonomy rows is removed
// with both relationships and both counts recomputed.
//
// The vendor and table prefix come from newRepos; the contract holds no literal
// prefix, vendor name or placeholder.
func RunNavMenuDeleteItemContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	setup := func(t *testing.T) (db *sql.DB, vendor, prefix string, del func(menuID, itemID int64) error, cleanup func()) {
		t.Helper()
		var repos *storage.Repositories
		repos, vendor, prefix, cleanup = newRepos(t)
		db = repos.DB()
		seedNavMenuDelete(ctx, t, db, vendor, prefix)
		w, ok := repos.NavMenus.(domain.NavMenuWriter)
		if !ok {
			cleanup()
			t.Fatalf("repos.NavMenus (%T) does not implement domain.NavMenuWriter", repos.NavMenus)
		}
		del = func(menuID, itemID int64) error { return w.DeleteItem(ctx, menuID, itemID) }
		return db, vendor, prefix, del, cleanup
	}
	parent := func(db *sql.DB, vendor, prefix string, id int64) string {
		return miMetaValue(ctx, t, db, vendor, prefix, id, "_menu_item_menu_item_parent")
	}
	relCount := func(db *sql.DB, vendor, prefix string, tt int64) int {
		return pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE term_taxonomy_id = ?`, tt)
	}

	t.Run("a child moves to the deleted item's parent", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		if err := del(ndMA, ndD2); err != nil {
			t.Fatalf("DeleteItem: %v", err)
		}
		if got, want := parent(db, vendor, prefix, ndC2), strconv.FormatInt(ndP0, 10); got != want {
			t.Errorf("child parent = %q, want %q", got, want)
		}
		if n := miPostRows(ctx, t, db, vendor, prefix, ndD2); n != 0 {
			t.Errorf("deleted item rows = %d, want 0", n)
		}
		if n := miMetaRows(ctx, t, db, vendor, prefix, ndD2); n != 0 {
			t.Errorf("deleted item postmeta rows = %d, want 0", n)
		}
	})

	t.Run("a child of a top-level deleted item becomes top level", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		if err := del(ndMA, ndD); err != nil {
			t.Fatalf("DeleteItem: %v", err)
		}
		if got := parent(db, vendor, prefix, ndC1); got != "0" {
			t.Errorf("child parent = %q, want \"0\"", got)
		}
	})

	t.Run("the menu count equals the remaining relationship count", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		// Seeded counts are stale on purpose, so only a recount matches.
		if err := del(ndMA, ndD); err != nil {
			t.Fatalf("DeleteItem: %v", err)
		}
		want := relCount(db, vendor, prefix, ndTTA)
		if want != 5 {
			t.Fatalf("A relationships after delete = %d, want 5", want)
		}
		if got := miCount(ctx, t, db, vendor, prefix, ndTTA); got != want {
			t.Errorf("A count = %d, want %d (recounted)", got, want)
		}
	})

	t.Run("an item of another menu returns ErrNotFound and changes nothing", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		before := ndSnapshot(ctx, t, db, vendor, prefix)
		err := del(ndMA, ndZ)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("DeleteItem(other menu's item) = %v, want domain.ErrNotFound", err)
		}
		if after := ndSnapshot(ctx, t, db, vendor, prefix); after != before {
			t.Errorf("rows changed after ErrNotFound:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("an item related to two menus is removed with both relationships and both counts recomputed", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		if err := del(ndMA, ndT); err != nil {
			t.Fatalf("DeleteItem: %v", err)
		}
		if n := miPostRows(ctx, t, db, vendor, prefix, ndT); n != 0 {
			t.Errorf("item post rows = %d, want 0", n)
		}
		if n := miRelRows(ctx, t, db, vendor, prefix, ndT); n != 0 {
			t.Errorf("item relationship rows = %d, want 0 (both menus)", n)
		}
		for name, tt := range map[string]int64{"A": ndTTA, "B": ndTTB} {
			want := relCount(db, vendor, prefix, tt)
			if got := miCount(ctx, t, db, vendor, prefix, tt); got != want {
				t.Errorf("%s count = %d, want %d (recounted)", name, got, want)
			}
		}
	})
}

// seedNavMenuDelete inserts nav_menu terms A and B with stale counts and the
// nav_menu_item rows of the ndXX constants, each with four _menu_item_* meta rows.
func seedNavMenuDelete(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	dec := func(n int64) string { return strconv.FormatInt(n, 10) }
	for _, m := range []struct {
		term, tt int64
		slug     string
	}{{ndMA, ndTTA, "nd-menu-a"}, {ndMB, ndTTB, "nd-menu-b"}} {
		exec(`INSERT INTO `+prefix+`terms (term_id, name, slug) VALUES (?, ?, ?)`, m.term, m.slug, m.slug)
		exec(`INSERT INTO `+prefix+`term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`,
			m.tt, m.term, "nav_menu", "", 0, 99)
	}
	item := func(id int64, parentID int64, tts ...int64) {
		t.Helper()
		exec(postInsert(vendor, prefix), postArgs(id, "nd-item-"+dec(id), "T "+dec(id), "nav_menu_item", "publish", "2000-01-01 00:00:00", "closed", 0, "", 0)...)
		for _, kv := range [][2]string{
			{"_menu_item_type", "custom"}, {"_menu_item_object", "custom"},
			{"_menu_item_object_id", dec(id)}, {"_menu_item_menu_item_parent", dec(parentID)},
		} {
			exec(`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, id, kv[0], kv[1])
		}
		for _, tt := range tts {
			exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, id, tt, 0)
		}
	}
	item(ndD, 0, ndTTA)
	item(ndC1, ndD, ndTTA)
	item(ndP0, 0, ndTTA)
	item(ndD2, ndP0, ndTTA)
	item(ndC2, ndD2, ndTTA)
	item(ndZ, 0, ndTTB)
	item(ndT, 0, ndTTA, ndTTB)
}

// ndSnapshot dumps every row the fixture touches into one comparable string.
func ndSnapshot(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) string {
	t.Helper()
	var out string
	dump := func(q string) {
		t.Helper()
		rows, err := db.QueryContext(ctx, rebind.Rebind(vendor, q))
		if err != nil {
			t.Fatalf("snapshot %q: %v", q, err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("snapshot scan: %v", err)
			}
			for _, v := range vals {
				out += v.String + "|"
			}
			out += "\n"
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("snapshot rows: %v", err)
		}
	}
	dump(`SELECT ` + rebind.Ident(vendor, "ID") + `, post_type FROM ` + prefix + `posts ORDER BY ` + rebind.Ident(vendor, "ID"))
	dump(`SELECT post_id, meta_key, meta_value FROM ` + prefix + `postmeta ORDER BY post_id, meta_key`)
	dump(`SELECT object_id, term_taxonomy_id FROM ` + prefix + `term_relationships ORDER BY object_id, term_taxonomy_id`)
	dump(`SELECT term_taxonomy_id, count FROM ` + prefix + `term_taxonomy ORDER BY term_taxonomy_id`)
	return out
}
