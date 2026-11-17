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
	// M20 rows (re-parenting). Ids chosen so one deleted chain has its child
	// at a lower id than its parent (I17 < I18) and one has the child above
	// the parent (I13 > I1).
	miI7  int64 = 9350 // page Q item in M1, top level: kept, becomes I1's parent
	miI8  int64 = 9351 // custom item in M1, child of I3: kept, unchanged
	miI9  int64 = 9352 // page Q item in M2, child of I2 (top-level deleted): kept
	miI13 int64 = 9353 // object P item in M1, child of I1, id above I1: deleted
	miI14 int64 = 9354 // custom item in M1, child of I13: kept
	miI16 int64 = 9356 // custom item in M1, child of I15: kept
	miI20 int64 = 9357 // custom item in M1, top level: kept
	miI17 int64 = 9360 // object P item in M1, child of I18, id below I18: deleted
	miI18 int64 = 9361 // object P item in M1, child of I20: deleted
	miI19 int64 = 9362 // custom item in M1, child of I17: kept
	miI15 int64 = 9399 // object P item in M1, its own parent, id above all: deleted
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
		if n := miMetaRows(ctx, t, db, vendor, prefix, miX); n != 2 {
			t.Errorf("non-menu post X postmeta rows = %d, want 2", n)
		}
		// M20: the child of a deleted item takes the deleted item's menu parent
		// (replaces the M19 Req 10.6 assertion that I3's parent stayed I1).
		if got := miMetaValue(ctx, t, db, vendor, prefix, miI3, "_menu_item_menu_item_parent"); got != strconv.FormatInt(miI7, 10) {
			t.Errorf("I3 _menu_item_menu_item_parent = %q, want %d (I1's parent)", got, miI7)
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

	t.Run("re-parents children of deleted items to the nearest surviving ancestor", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(miP, "page"); err != nil {
			t.Fatalf("PurgeMenuItemsOf: %v", err)
		}
		dec := func(n int64) string { return strconv.FormatInt(n, 10) }
		for _, c := range []struct {
			name string
			id   int64
			want string
		}{
			{"I3 (child of I1)", miI3, dec(miI7)},
			{"I8 (grandchild, unchanged)", miI8, dec(miI3)},
			{"I9 (child of top-level I2)", miI9, "0"},
			{"I14 (child of I13, parent-first chain)", miI14, dec(miI7)},
			{"I16 (child of self-parented I15)", miI16, "0"},
			{"I19 (child of I17, child-first chain)", miI19, dec(miI20)},
			{"I7 (top level, unchanged)", miI7, "0"},
			{"I20 (top level, unchanged)", miI20, "0"},
		} {
			if got := miMetaValue(ctx, t, db, vendor, prefix, c.id, "_menu_item_menu_item_parent"); got != c.want {
				t.Errorf("%s _menu_item_menu_item_parent = %q, want %q", c.name, got, c.want)
			}
		}
		for name, id := range map[string]int64{"I13": miI13, "I15": miI15, "I17": miI17, "I18": miI18} {
			if n := miPostRows(ctx, t, db, vendor, prefix, id); n != 0 {
				t.Errorf("%s post rows = %d, want 0", name, n)
			}
			if n := miMetaRows(ctx, t, db, vendor, prefix, id); n != 0 {
				t.Errorf("%s postmeta rows = %d, want 0", name, n)
			}
		}
		// X is not a menu item: its stray parent key keeps its value.
		if got := miMetaValue(ctx, t, db, vendor, prefix, miX, "_menu_item_menu_item_parent"); got != dec(miI1) {
			t.Errorf("X _menu_item_menu_item_parent = %q, want %d (unchanged)", got, miI1)
		}
	})
	t.Run("recomputes the count of each affected menu from its relationships", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		// Seeded counts are stale on purpose (M1 3, M2 5): a recount, not a
		// decrement, is what makes them match the relationships.
		if got := miCount(ctx, t, db, vendor, prefix, miTT1); got != 3 {
			t.Fatalf("seed: M1 count = %d, want 3", got)
		}
		if err := purge(miP, "page"); err != nil {
			t.Fatalf("PurgeMenuItemsOf: %v", err)
		}
		// M1 keeps I3-I8, I14, I16, I19 and I20 (ten items); M2 keeps I9.
		wantM1 := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE term_taxonomy_id = ?`, miTT1)
		if wantM1 != 10 {
			t.Fatalf("M1 relationships after purge = %d, want 10", wantM1)
		}
		if got := miCount(ctx, t, db, vendor, prefix, miTT1); got != wantM1 {
			t.Errorf("M1 count = %d, want %d (recounted)", got, wantM1)
		}
		wantM2 := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE term_taxonomy_id = ?`, miTT2)
		if wantM2 != 1 {
			t.Fatalf("M2 relationships after purge = %d, want 1", wantM2)
		}
		if got := miCount(ctx, t, db, vendor, prefix, miTT2); got != wantM2 {
			t.Errorf("M2 count = %d, want %d (recounted)", got, wantM2)
		}
	})

	t.Run("calling the same purge twice succeeds and changes nothing", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(miP, "page"); err != nil {
			t.Fatalf("first PurgeMenuItemsOf: %v", err)
		}
		parents := func() string {
			var out string
			for _, id := range []int64{miI3, miI7, miI8, miI9, miI14, miI16, miI19, miI20} {
				out += miMetaValue(ctx, t, db, vendor, prefix, id, "_menu_item_menu_item_parent") + ","
			}
			return out
		}
		parentsBefore := parents()
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
		if got := parents(); got != parentsBefore {
			t.Errorf("parents after repeat = %q, want %q", got, parentsBefore)
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
// counts 3 and 5), menu items I1..I20 with their four _menu_item_* meta rows and
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
		{miM1, miTT1, "mi-menu-1", 3}, {miM2, miTT2, "mi-menu-2", 5},
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
	item(miI1, "post_type", "page", miP, miTT1, id(miI7))
	item(miI2, "post_type", "page", miP, miTT2, "0")
	item(miI3, "post_type", "page", miQ, miTT1, id(miI1))
	item(miI4, "post_type", "post", miP, miTT1, "0")
	item(miI5, "taxonomy", "category", miP, miTT1, "0")
	item(miI6, "custom", "custom", 0, miTT1, "0")

	item(miI7, "post_type", "page", miQ, miTT1, "0")
	item(miI8, "custom", "custom", 0, miTT1, id(miI3))
	item(miI9, "post_type", "page", miQ, miTT2, id(miI2))
	item(miI13, "post_type", "page", miP, miTT1, id(miI1))
	item(miI14, "custom", "custom", 0, miTT1, id(miI13))
	item(miI16, "custom", "custom", 0, miTT1, id(miI15))
	item(miI20, "custom", "custom", 0, miTT1, "0")
	item(miI17, "post_type", "page", miP, miTT1, id(miI18))
	item(miI18, "post_type", "page", miP, miTT1, id(miI20))
	item(miI19, "custom", "custom", 0, miTT1, id(miI17))
	item(miI15, "post_type", "page", miP, miTT1, id(miI15))
	post(miX, "mi-x", "post")
	meta(miX, "_menu_item_object_id", id(miP))
	meta(miX, "_menu_item_menu_item_parent", id(miI1))
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
