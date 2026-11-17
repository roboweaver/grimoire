package storagetest

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the menu-item purger contract. They sit far above SeedFixtures'
// ids so the seeded rows never collide with the base fixtures.
const (
	miP   int64 = 9300 // page whose menu items are purged
	miQ   int64 = 9301 // another page, also linked from a menu
	miM1  int64 = 9310 // nav_menu term M1
	miM2  int64 = 9311 // nav_menu term M2
	miTT1 int64 = 9320 // M1 term_taxonomy_id
	miTT2 int64 = 9321 // M2 term_taxonomy_id
	miI1  int64 = 9330 // post_type/page/P in M1: deleted
	miI2  int64 = 9331 // post_type/page/P in M2: deleted
	miI3  int64 = 9332 // post_type/page/Q in M1, child of I1: kept
	miI4  int64 = 9333 // post_type/post/P in M1: wrong object, kept
	miI5  int64 = 9334 // taxonomy/category/P in M1: wrong type, kept
	miI6  int64 = 9335 // custom/custom/0 in M1: kept
	miX   int64 = 9340 // plain post carrying _menu_item_object_id = P: kept
)

// RunPostMenuItemPurgerContract covers PostMenuItemPurger.PurgeMenuItemsOf: the
// nav_menu_item rows whose _menu_item_type is post_type, _menu_item_object is the
// given type and _menu_item_object_id is the post are deleted with their postmeta
// and term_relationships rows (Req 10.2), term_taxonomy.count of their menus is
// recomputed (10.3), nothing else changes (10.4-10.6), and the call is idempotent
// (9.5). Atomicity (9.6) has no portable failure injection and is covered on
// SQLite in internal/storage/wprepo/postmenuitems_test.go.
//
// The vendor and table prefix come from newRepos; the contract holds no literal
// prefix and no vendor-specific placeholder (Req 11.1).
func RunPostMenuItemPurgerContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	setup := func(t *testing.T) (db *sql.DB, vendor, prefix string, purge func(postID int64, objectType string) error, cleanup func()) {
		t.Helper()
		var repos *storage.Repositories
		repos, vendor, prefix, cleanup = newRepos(t)
		db = repos.DB()
		seedPostMenuItems(ctx, t, db, vendor, prefix)
		purge = func(postID int64, objectType string) error {
			return repos.PostMenuItems.PurgeMenuItemsOf(ctx, postID, objectType)
		}
		return db, vendor, prefix, purge, cleanup
	}

	t.Run("deletes the matching items with their meta and relationships", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(miP, "page"); err != nil {
			t.Fatalf("PurgeMenuItemsOf: %v", err)
		}
		for name, id := range map[string]int64{"I1": miI1, "I2": miI2} {
			if n := miPostRows(ctx, t, db, vendor, prefix, id); n != 0 {
				t.Errorf("%s post rows = %d, want 0", name, n)
			}
			if n := miMetaRows(ctx, t, db, vendor, prefix, id); n != 0 {
				t.Errorf("%s postmeta rows = %d, want 0", name, n)
			}
			if n := miRelRows(ctx, t, db, vendor, prefix, id); n != 0 {
				t.Errorf("%s term_relationships rows = %d, want 0", name, n)
			}
		}
	})

	t.Run("keeps items of other targets, objects, types and the non-menu post", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(miP, "page"); err != nil {
			t.Fatalf("PurgeMenuItemsOf: %v", err)
		}
		for name, id := range map[string]int64{"I3": miI3, "I4": miI4, "I5": miI5, "I6": miI6} {
			if n := miPostRows(ctx, t, db, vendor, prefix, id); n != 1 {
				t.Errorf("%s post rows = %d, want 1", name, n)
			}
			if n := miMetaRows(ctx, t, db, vendor, prefix, id); n != 4 {
				t.Errorf("%s postmeta rows = %d, want 4", name, n)
			}
			if n := miRelRows(ctx, t, db, vendor, prefix, id); n != 1 {
				t.Errorf("%s term_relationships rows = %d, want 1", name, n)
			}
		}
		if n := miPostRows(ctx, t, db, vendor, prefix, miX); n != 1 {
			t.Errorf("non-menu post X rows = %d, want 1", n)
		}
		if n := miMetaRows(ctx, t, db, vendor, prefix, miX); n != 1 {
			t.Errorf("non-menu post X postmeta rows = %d, want 1", n)
		}
		// Req 10.6: a child's parent pointer is not rewritten.
		if got := miMetaValue(ctx, t, db, vendor, prefix, miI3, "_menu_item_menu_item_parent"); got != strconv.FormatInt(miI1, 10) {
			t.Errorf("I3 _menu_item_menu_item_parent = %q, want %d (unchanged)", got, miI1)
		}
		// Req 9.4, 10.5: the pages and the menu terms survive.
		for name, id := range map[string]int64{"P": miP, "Q": miQ} {
			if n := miPostRows(ctx, t, db, vendor, prefix, id); n != 1 {
				t.Errorf("page %s rows = %d, want 1", name, n)
			}
		}
		for name, id := range map[string]int64{"M1": miM1, "M2": miM2} {
			if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`terms WHERE term_id = ?`, id); n != 1 {
				t.Errorf("menu term %s rows = %d, want 1", name, n)
			}
		}
		for name, id := range map[string]int64{"M1": miTT1, "M2": miTT2} {
			if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_taxonomy WHERE term_taxonomy_id = ?`, id); n != 1 {
				t.Errorf("menu term_taxonomy %s rows = %d, want 1", name, n)
			}
		}
	})

	t.Run("recomputes the count of each affected menu from its relationships", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		// Seeded counts are stale on purpose (M1 3, M2 1): a recount, not a
		// decrement, is what makes them match the relationships.
		if got := miCount(ctx, t, db, vendor, prefix, miTT1); got != 3 {
			t.Fatalf("seed: M1 count = %d, want 3", got)
		}
		if err := purge(miP, "page"); err != nil {
			t.Fatalf("PurgeMenuItemsOf: %v", err)
		}
		// M1 keeps I3, I4, I5 and I6; M2 keeps nothing.
		wantM1 := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE term_taxonomy_id = ?`, miTT1)
		if wantM1 != 4 {
			t.Fatalf("M1 relationships after purge = %d, want 4", wantM1)
		}
		if got := miCount(ctx, t, db, vendor, prefix, miTT1); got != wantM1 {
			t.Errorf("M1 count = %d, want %d (recounted)", got, wantM1)
		}
		if got := miCount(ctx, t, db, vendor, prefix, miTT2); got != 0 {
			t.Errorf("M2 count = %d, want 0", got)
		}
	})

	t.Run("calling the same purge twice succeeds and changes nothing", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(miP, "page"); err != nil {
			t.Fatalf("first PurgeMenuItemsOf: %v", err)
		}
		posts := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`posts`)
		meta := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`postmeta`)
		rels := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships`)
		c1, c2 := miCount(ctx, t, db, vendor, prefix, miTT1), miCount(ctx, t, db, vendor, prefix, miTT2)
		if err := purge(miP, "page"); err != nil {
			t.Fatalf("second PurgeMenuItemsOf: %v", err)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`posts`); n != posts {
			t.Errorf("posts after repeat = %d, want %d", n, posts)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`postmeta`); n != meta {
			t.Errorf("postmeta after repeat = %d, want %d", n, meta)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships`); n != rels {
			t.Errorf("term_relationships after repeat = %d, want %d", n, rels)
		}
		if got := miCount(ctx, t, db, vendor, prefix, miTT1); got != c1 {
			t.Errorf("M1 count after repeat = %d, want %d", got, c1)
		}
		if got := miCount(ctx, t, db, vendor, prefix, miTT2); got != c2 {
			t.Errorf("M2 count after repeat = %d, want %d", got, c2)
		}
	})

	t.Run("no items and nonexistent post id both return nil", func(t *testing.T) {
		_, _, _, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(miQ+500, "page"); err != nil {
			t.Errorf("purge of a post with no items = %v, want nil", err)
		}
		if err := purge(999999, "page"); err != nil {
			t.Errorf("purge of a nonexistent id = %v, want nil", err)
		}
	})

	t.Run("a different object type deletes nothing", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		posts := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`posts`)
		meta := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`postmeta`)
		rels := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships`)
		if err := purge(miP, "attachment"); err != nil {
			t.Fatalf("PurgeMenuItemsOf: %v", err)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`posts`); n != posts {
			t.Errorf("posts = %d, want %d", n, posts)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`postmeta`); n != meta {
			t.Errorf("postmeta = %d, want %d", n, meta)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships`); n != rels {
			t.Errorf("term_relationships = %d, want %d", n, rels)
		}
	})
}

// seedPostMenuItems inserts pages P and Q, nav_menu terms M1 and M2 (stale
// counts 3 and 1), menu items I1..I6 with their four _menu_item_* meta rows and
// one relationship each, and a plain post X carrying a stray
// _menu_item_object_id meta row equal to P.
func seedPostMenuItems(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	post := func(id int64, slug, ptype string) {
		t.Helper()
		exec(postInsert(vendor, prefix), postArgs(id, slug, "T "+slug, ptype, "publish", "2000-01-01 00:00:00", "closed", 0, "", 0)...)
	}
	meta := func(postID int64, key, value string) {
		t.Helper()
		exec(`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, postID, key, value)
	}
	id := func(n int64) string { return strconv.FormatInt(n, 10) }

	post(miP, "mi-p", "page")
	post(miQ, "mi-q", "page")
	for _, m := range []struct {
		term, tt int64
		slug     string
		count    int
	}{
		{miM1, miTT1, "mi-menu-1", 3}, {miM2, miTT2, "mi-menu-2", 1},
	} {
		exec(`INSERT INTO `+prefix+`terms (term_id, name, slug) VALUES (?, ?, ?)`, m.term, m.slug, m.slug)
		exec(`INSERT INTO `+prefix+`term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`,
			m.tt, m.term, "nav_menu", "", 0, m.count)
	}
	item := func(itemID int64, typ, object string, objectID, tt int64, parent string) {
		t.Helper()
		post(itemID, "mi-item-"+id(itemID), "nav_menu_item")
		meta(itemID, "_menu_item_type", typ)
		meta(itemID, "_menu_item_object", object)
		meta(itemID, "_menu_item_object_id", id(objectID))
		meta(itemID, "_menu_item_menu_item_parent", parent)
		exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, itemID, tt, 0)
	}
	item(miI1, "post_type", "page", miP, miTT1, "0")
	item(miI2, "post_type", "page", miP, miTT2, "0")
	item(miI3, "post_type", "page", miQ, miTT1, id(miI1))
	item(miI4, "post_type", "post", miP, miTT1, "0")
	item(miI5, "taxonomy", "category", miP, miTT1, "0")
	item(miI6, "custom", "custom", 0, miTT1, "0")

	post(miX, "mi-x", "post")
	meta(miX, "_menu_item_object_id", id(miP))
}

func miPostRows(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, id int64) int {
	t.Helper()
	return pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`posts WHERE `+rebind.Ident(vendor, "ID")+` = ?`, id)
}

func miMetaRows(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, id int64) int {
	t.Helper()
	return pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`postmeta WHERE post_id = ?`, id)
}

func miRelRows(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, id int64) int {
	t.Helper()
	return pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE object_id = ?`, id)
}

func miCount(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, tt int64) int {
	t.Helper()
	return pdCount(ctx, t, db, vendor, `SELECT count FROM `+prefix+`term_taxonomy WHERE term_taxonomy_id = ?`, tt)
}

func miMetaValue(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, id int64, key string) string {
	t.Helper()
	var v string
	q := rebind.Rebind(vendor, `SELECT meta_value FROM `+prefix+`postmeta WHERE post_id = ? AND meta_key = ?`)
	if err := db.QueryRowContext(ctx, q, id, key).Scan(&v); err != nil {
		t.Fatalf("read %s of %d: %v", key, id, err)
	}
	return v
}
