package storagetest

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

func runMenusContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("NavMenuRepository menus by slug by id and by location", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		menus, err := repos.NavMenus.Menus(ctx)
		if err != nil {
			t.Fatalf("Menus: %v", err)
		}
		if len(menus) != 1 || menus[0].Slug != "primary" {
			t.Fatalf("menus = %+v, want [primary]", menus)
		}
		bySlug, err := repos.NavMenus.MenuBySlug(ctx, "primary")
		if err != nil {
			t.Fatalf("MenuBySlug: %v", err)
		}
		if len(bySlug.Items) != 3 {
			t.Fatalf("top-level items = %d, want 3", len(bySlug.Items))
		}
		if bySlug.Items[0].Label != "Home" || bySlug.Items[0].URL != "/home-custom" {
			t.Fatalf("custom item = %+v", bySlug.Items[0])
		}
		if bySlug.Items[1].Label != "About" || bySlug.Items[1].URL != "/about" {
			t.Fatalf("post_type item = %+v", bySlug.Items[1])
		}
		if bySlug.Items[2].Label != "News" || bySlug.Items[2].URL != "/category/news" {
			t.Fatalf("taxonomy item = %+v", bySlug.Items[2])
		}
		if len(bySlug.Items[0].Children) != 1 || bySlug.Items[0].Children[0].Label != "Sub Home" {
			t.Fatalf("children = %+v", bySlug.Items[0].Children)
		}
		byID, err := repos.NavMenus.MenuByID(ctx, 30)
		if err != nil {
			t.Fatalf("MenuByID: %v", err)
		}
		if byID.Slug != "primary" {
			t.Fatalf("MenuByID slug = %q, want primary", byID.Slug)
		}
		byLoc, err := repos.NavMenus.MenuByLocation(ctx, "twentytwentyfive", "primary")
		if err != nil {
			t.Fatalf("MenuByLocation: %v", err)
		}
		if byLoc.ID != 30 || byLoc.Items[1].URL != "/about" {
			t.Fatalf("MenuByLocation = %+v", byLoc)
		}
	})

	t.Run("NavMenuRepository empty degradation", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		missingSlug, err := repos.NavMenus.MenuBySlug(ctx, "missing")
		if err != nil {
			t.Fatalf("MenuBySlug missing: %v", err)
		}
		if missingSlug.ID != 0 || len(missingSlug.Items) != 0 {
			t.Fatalf("missing slug = %+v, want empty menu", missingSlug)
		}
		missingLoc, err := repos.NavMenus.MenuByLocation(ctx, "twentytwentyfive", "footer")
		if err != nil {
			t.Fatalf("MenuByLocation missing: %v", err)
		}
		if missingLoc.ID != 0 || len(missingLoc.Items) != 0 {
			t.Fatalf("missing location = %+v, want empty menu", missingLoc)
		}
	})
}

// Fixture ids for the trashed-target contract, far above SeedFixtures.
const (
	mttTerm  int64 = 9910 // nav_menu term
	mttTT    int64 = 9911 // its term_taxonomy_id
	mttLive  int64 = 9920 // published page
	mttGone  int64 = 9921 // trashed page, name carries the __trashed suffix
	mttItemL int64 = 9930 // item targeting the published page
	mttItemG int64 = 9931 // item targeting the trashed page
	mttItemC int64 = 9932 // custom link item
)

// RunNavMenuTrashedTargetContract pins that NavMenuRepository marks
// TargetTrashed only on a post_type item whose target page is in the trash,
// keeps the stored label, clears the mark when the target is restored, and
// never changes a stored row (Req 9.4).
//
// The vendor and table prefix come from newRepos; the contract holds no literal
// prefix, vendor name or placeholder.
func RunNavMenuTrashedTargetContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	setup := func(t *testing.T) (repos *storage.Repositories, db *sql.DB, vendor, prefix string, cleanup func()) {
		t.Helper()
		repos, vendor, prefix, cleanup = newRepos(t)
		db = repos.DB()
		seedMenuTrashedTarget(ctx, t, db, vendor, prefix)
		return repos, db, vendor, prefix, cleanup
	}
	byID := func(items []domain.NavMenuItem, id int64) (domain.NavMenuItem, bool) {
		for _, it := range items {
			if it.ID == id {
				return it, true
			}
		}
		return domain.NavMenuItem{}, false
	}

	t.Run("only the trashed target is marked and the stored label is returned", func(t *testing.T) {
		repos, db, vendor, prefix, cleanup := setup(t)
		defer cleanup()
		before := ndSnapshot(ctx, t, db, vendor, prefix)
		menu, err := repos.NavMenus.MenuBySlug(ctx, "mtt-menu")
		if err != nil {
			t.Fatalf("MenuBySlug: %v", err)
		}
		if len(menu.Items) != 3 {
			t.Fatalf("items = %+v, want 3", menu.Items)
		}
		live, okL := byID(menu.Items, mttItemL)
		gone, okG := byID(menu.Items, mttItemG)
		custom, okC := byID(menu.Items, mttItemC)
		if !okL || !okG || !okC {
			t.Fatalf("items missing: live=%v gone=%v custom=%v in %+v", okL, okG, okC, menu.Items)
		}
		if live.TargetTrashed || custom.TargetTrashed {
			t.Errorf("TargetTrashed set on live=%v custom=%v, want false", live.TargetTrashed, custom.TargetTrashed)
		}
		if !gone.TargetTrashed {
			t.Errorf("TargetTrashed = false on the item of the trashed page, want true")
		}
		if gone.Label != "Gone Item" {
			t.Errorf("trashed item label = %q, want the stored label %q", gone.Label, "Gone Item")
		}
		if after := ndSnapshot(ctx, t, db, vendor, prefix); after != before {
			t.Errorf("a read changed stored rows:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("restoring the target clears the mark", func(t *testing.T) {
		repos, db, vendor, prefix, cleanup := setup(t)
		defer cleanup()
		q := `UPDATE ` + prefix + `posts SET post_status = ?, post_name = ? WHERE ` + rebind.Ident(vendor, "ID") + ` = ?`
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), "publish", "mtt-gone", mttGone); err != nil {
			t.Fatalf("restore target: %v", err)
		}
		menu, err := repos.NavMenus.MenuBySlug(ctx, "mtt-menu")
		if err != nil {
			t.Fatalf("MenuBySlug: %v", err)
		}
		gone, ok := byID(menu.Items, mttItemG)
		if !ok {
			t.Fatalf("item %d missing in %+v", mttItemG, menu.Items)
		}
		if gone.TargetTrashed {
			t.Errorf("TargetTrashed still true after the target was restored")
		}
		if gone.URL != "/mtt-gone" {
			t.Errorf("restored item URL = %q, want /mtt-gone", gone.URL)
		}
	})
}

// seedMenuTrashedTarget inserts one nav_menu with three items: one targeting a
// published page, one targeting a trashed page and one custom link.
func seedMenuTrashedTarget(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	dec := func(n int64) string { return strconv.FormatInt(n, 10) }
	exec(`INSERT INTO `+prefix+`terms (term_id, name, slug) VALUES (?, ?, ?)`, mttTerm, "mtt-menu", "mtt-menu")
	exec(`INSERT INTO `+prefix+`term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`,
		mttTT, mttTerm, "nav_menu", "", 0, 3)
	exec(postInsert(vendor, prefix), postArgs(mttLive, "mtt-live", "Live Page", "page", "publish", "2000-01-01 00:00:00", "closed", 0, "", 0)...)
	exec(postInsert(vendor, prefix), postArgs(mttGone, "mtt-gone__trashed", "Gone Page", "page", "trash", "2000-01-01 00:00:00", "closed", 0, "", 0)...)
	item := func(id int64, order int, title, typ, object string, objectID int64, url string) {
		t.Helper()
		exec(postInsert(vendor, prefix), postArgs(id, "mtt-item-"+dec(id), title, "nav_menu_item", "publish", "2000-01-01 00:00:00", "closed", 0, "", order)...)
		kvs := [][2]string{
			{"_menu_item_type", typ}, {"_menu_item_object", object},
			{"_menu_item_object_id", dec(objectID)}, {"_menu_item_menu_item_parent", "0"},
		}
		if url != "" {
			kvs = append(kvs, [2]string{"_menu_item_url", url})
		}
		for _, kv := range kvs {
			exec(`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, id, kv[0], kv[1])
		}
		exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, id, mttTT, 0)
	}
	item(mttItemL, 1, "Live Item", "post_type", "page", mttLive, "")
	item(mttItemG, 2, "Gone Item", "post_type", "page", mttGone, "")
	item(mttItemC, 3, "Custom Item", "custom", "custom", mttItemC, "/mtt-custom")
}
