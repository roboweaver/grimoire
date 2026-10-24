package wprepo

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	sqlitemig "github.com/roboweaver/grimoire/internal/storage/migrations/sqlite"
	sqliteadapter "github.com/roboweaver/grimoire/internal/storage/sqlite"
)

// newNavMenuRepo builds a migrated SQLite-backed *NavMenuRepo for the nav-menu
// write contract tests. It mirrors newUserRepo in users_test.go exactly: SQLite
// always runs (no GRIMOIRE_TEST_* gate), the backend is a fresh temp-file
// database per test migrated with the same greenfield set the production path
// and the storagetest SQLite runner use, and the repo is built directly via
// NewNavMenuRepo (storage imports this package, so importing storage here would
// be an import cycle). The same *NavMenuRepo is both the read implementation
// (domain.NavMenuRepository) and — once task 2.2 lands — the write
// implementation (domain.NavMenuWriter), so a written menu round-trips through
// the existing read path on the one instance.
func newNavMenuRepo(t *testing.T) *NavMenuRepo {
	t.Helper()
	ctx := context.Background()
	const prefix = "wp_"
	cfg := config.DatabaseConfig{
		Vendor:      "sqlite",
		DSN:         filepath.Join(t.TempDir(), "grimoire.db"),
		TablePrefix: prefix,
	}
	db, err := sqliteadapter.Open(cfg)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := migrate.Apply(ctx, db, sqlitemig.FS(), cfg.Vendor, prefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	return NewNavMenuRepo(sqliteadapter.NewBunDB(db), prefix)
}

// countRows returns the number of rows in table matching the supplied WHERE
// clause (args bound with ? placeholders — the form Bun inlines, per
// placeholders_test.go). Used to assert clean teardown leaves no orphan.
func countRows(t *testing.T, repo *NavMenuRepo, table, where string, args ...any) int {
	t.Helper()
	q := "SELECT COUNT(*) FROM " + repo.prefix + table
	if where != "" {
		q += " WHERE " + where
	}
	var n int
	if err := repo.db.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("countRows(%s, %q): %v", table, where, err)
	}
	return n
}

// termTaxonomyID returns the term_taxonomy_id of the nav_menu term for menuID,
// so relationship rows can be counted by the id the link actually carries.
func termTaxonomyID(t *testing.T, repo *NavMenuRepo, menuID int64) int64 {
	t.Helper()
	q := "SELECT term_taxonomy_id FROM " + repo.prefix +
		"term_taxonomy WHERE term_id = ? AND taxonomy = ?"
	var ttID int64
	if err := repo.db.QueryRowContext(context.Background(), q, menuID, "nav_menu").Scan(&ttID); err != nil {
		t.Fatalf("termTaxonomyID(%d): %v", menuID, err)
	}
	return ttID
}

// findItem walks a menu's item tree (top-level + children) for an item whose
// resolved Label matches, returning it and whether it was found.
func findItem(items []domain.NavMenuItem, label string) (domain.NavMenuItem, bool) {
	for _, it := range items {
		if it.Label == label {
			return it, true
		}
		if child, ok := findItem(it.Children, label); ok {
			return child, true
		}
	}
	return domain.NavMenuItem{}, false
}

// TestNavMenuRepoRoundTrip creates a menu, then one item of each type (custom,
// post_type, taxonomy) with an explicit Order plus a nested child under the
// custom item, and asserts the menu read back through NavMenuRepo's own read
// path (MenuByID) matches on order (menu_order), nesting
// (_menu_item_menu_item_parent), and the custom label/URL resolved by
// resolveMenuTarget. This is the "mirror in reverse" round-trip of Req 6.1.
func TestNavMenuRepoRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	menuID, err := repo.CreateMenu(ctx, "Primary", "primary")
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	customID, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label:  "Home",
		URL:    "https://example.com/home",
		Type:   "custom",
		Object: "custom",
		Order:  1,
	})
	if err != nil {
		t.Fatalf("CreateItem custom: %v", err)
	}

	if _, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label:    "Blog",
		Type:     "post_type",
		Object:   "page",
		ObjectID: 4242, // read path resolves against a referenced post; see dangling test
		Order:    2,
	}); err != nil {
		t.Fatalf("CreateItem post_type: %v", err)
	}

	if _, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label:    "News",
		Type:     "taxonomy",
		Object:   "category",
		ObjectID: 7777,
		Order:    3,
	}); err != nil {
		t.Fatalf("CreateItem taxonomy: %v", err)
	}

	// A nested child under the custom item (parent = customID).
	if _, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label:    "Child",
		URL:      "https://example.com/child",
		Type:     "custom",
		Object:   "custom",
		ParentID: customID,
		Order:    1,
	}); err != nil {
		t.Fatalf("CreateItem nested child: %v", err)
	}

	menu, err := repo.MenuByID(ctx, menuID)
	if err != nil {
		t.Fatalf("MenuByID: %v", err)
	}
	if menu.ID != menuID || menu.Name != "Primary" || menu.Slug != "primary" {
		t.Fatalf("menu header = {%d %q %q}, want {%d Primary primary}", menu.ID, menu.Name, menu.Slug, menuID)
	}

	// Three top-level items ordered by menu_order: Home(1), Blog(2), News(3).
	if len(menu.Items) != 3 {
		t.Fatalf("top-level items = %d, want 3", len(menu.Items))
	}
	wantOrder := []string{"Home", "Blog", "News"}
	for i, want := range wantOrder {
		if menu.Items[i].Label != want {
			t.Errorf("top-level item[%d].Label = %q, want %q (order via menu_order)", i, menu.Items[i].Label, want)
		}
	}

	// The custom item resolves its own label/URL verbatim (resolveMenuTarget
	// custom branch) and carries the nested child.
	home := menu.Items[0]
	if home.URL != "https://example.com/home" {
		t.Errorf("custom item URL = %q, want %q", home.URL, "https://example.com/home")
	}
	if len(home.Children) != 1 {
		t.Fatalf("custom item children = %d, want 1 (nesting via _menu_item_menu_item_parent)", len(home.Children))
	}
	if home.Children[0].Label != "Child" || home.Children[0].ParentID != customID {
		t.Errorf("nested child = {%q parent=%d}, want {Child parent=%d}", home.Children[0].Label, home.Children[0].ParentID, customID)
	}
}

// TestNavMenuRepoDanglingObjectFallsBackToLabel covers Finding 2: a
// post_type/taxonomy item whose _menu_item_object_id references no row falls
// back to the stored post_title through resolveMenuTarget rather than erroring.
func TestNavMenuRepoDanglingObjectFallsBackToLabel(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	menuID, err := repo.CreateMenu(ctx, "Dangling", "dangling")
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	// ObjectID 999999 references no {prefix}posts row, so resolveMenuTarget
	// must fall back to the stored label ("Orphan Link") and not error.
	if _, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label:    "Orphan Link",
		Type:     "post_type",
		Object:   "page",
		ObjectID: 999999,
		Order:    1,
	}); err != nil {
		t.Fatalf("CreateItem dangling: %v", err)
	}

	menu, err := repo.MenuByID(ctx, menuID)
	if err != nil {
		t.Fatalf("MenuByID: %v", err)
	}
	if _, ok := findItem(menu.Items, "Orphan Link"); !ok {
		t.Fatalf("dangling post_type item did not fall back to stored label %q; items=%+v", "Orphan Link", menu.Items)
	}
}

// TestNavMenuRepoUpdateMenuPartial covers Req 1.2: supplying only some patch
// fields leaves the unsupplied fields unchanged.
func TestNavMenuRepoUpdateMenuPartial(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	menuID, err := repo.CreateMenu(ctx, "Original Name", "original-slug")
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	// Supply only Name; Slug must stay "original-slug".
	newName := "Renamed"
	if err := repo.UpdateMenu(ctx, menuID, domain.NavMenuPatch{Name: &newName}); err != nil {
		t.Fatalf("UpdateMenu name-only: %v", err)
	}

	menu, err := repo.MenuByID(ctx, menuID)
	if err != nil {
		t.Fatalf("MenuByID: %v", err)
	}
	if menu.Name != "Renamed" {
		t.Errorf("Name = %q, want %q (the one supplied field must change)", menu.Name, "Renamed")
	}
	if menu.Slug != "original-slug" {
		t.Errorf("Slug = %q, want %q (an omitted field must be left unchanged)", menu.Slug, "original-slug")
	}
}

// TestNavMenuRepoUpdateItemPartial covers Req 2.2: a partial item update rewrites
// only the supplied fields and leaves the unsupplied ones unchanged.
func TestNavMenuRepoUpdateItemPartial(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	menuID, err := repo.CreateMenu(ctx, "Items", "items")
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	itemID, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label:  "Before",
		URL:    "https://example.com/before",
		Type:   "custom",
		Object: "custom",
		Order:  5,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// Supply only Label; URL and Order must be unchanged.
	newLabel := "After"
	if err := repo.UpdateItem(ctx, menuID, itemID, domain.NavMenuItemPatch{Label: &newLabel}); err != nil {
		t.Fatalf("UpdateItem label-only: %v", err)
	}

	menu, err := repo.MenuByID(ctx, menuID)
	if err != nil {
		t.Fatalf("MenuByID: %v", err)
	}
	it, ok := findItem(menu.Items, "After")
	if !ok {
		t.Fatalf("updated item label %q not found; items=%+v", "After", menu.Items)
	}
	if it.URL != "https://example.com/before" {
		t.Errorf("URL = %q, want %q (an omitted field must be left unchanged)", it.URL, "https://example.com/before")
	}
	if it.Order != 5 {
		t.Errorf("Order = %d, want 5 (an omitted field must be left unchanged)", it.Order)
	}
}

// TestNavMenuRepoDeleteMenuNoOrphan covers Req 6.3: DeleteMenu leaves zero rows
// in {prefix}posts (the item posts), {prefix}postmeta (their meta), and
// {prefix}term_relationships for the menu term, while a foreign referenced post
// and term are left untouched.
func TestNavMenuRepoDeleteMenuNoOrphan(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	// A foreign post and term that the menu items reference but that DeleteMenu
	// must NOT remove (they are not nav_menu_item posts / the nav_menu term).
	foreignPostID, err := insertReturningID(ctx, repo.db, vendorOf(repo.db), repo.prefix+"posts",
		[]string{"post_title", "post_type", "post_status"}, `"ID"`, "Real Page", "page", "publish")
	if err != nil {
		t.Fatalf("seed foreign post: %v", err)
	}
	foreignTermID, err := insertReturningID(ctx, repo.db, vendorOf(repo.db), repo.prefix+"terms",
		[]string{"name", "slug"}, "term_id", "Real Category", "real-category")
	if err != nil {
		t.Fatalf("seed foreign term: %v", err)
	}

	menuID, err := repo.CreateMenu(ctx, "Teardown", "teardown")
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	ttID := termTaxonomyID(t, repo, menuID)

	// Two items: one custom, one post_type referencing the foreign post.
	if _, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label: "Home", URL: "https://example.com/", Type: "custom", Object: "custom", Order: 1,
	}); err != nil {
		t.Fatalf("CreateItem custom: %v", err)
	}
	if _, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label: "Page", Type: "post_type", Object: "page", ObjectID: foreignPostID, Order: 2,
	}); err != nil {
		t.Fatalf("CreateItem post_type: %v", err)
	}

	// Preconditions: two nav_menu_item posts, their postmeta, and two
	// relationship rows for the menu term exist before deletion.
	if got := countRows(t, repo, "posts", "post_type = ?", "nav_menu_item"); got != 2 {
		t.Fatalf("precondition nav_menu_item posts = %d, want 2", got)
	}
	if got := countRows(t, repo, "term_relationships", "term_taxonomy_id = ?", ttID); got != 2 {
		t.Fatalf("precondition term_relationships = %d, want 2", got)
	}
	if got := countRows(t, repo, "postmeta", "", nil); got == 0 {
		t.Fatalf("precondition postmeta rows = 0, want > 0")
	}

	if err := repo.DeleteMenu(ctx, menuID); err != nil {
		t.Fatalf("DeleteMenu: %v", err)
	}

	// No orphan: zero item posts, zero postmeta, zero relationship rows.
	if got := countRows(t, repo, "posts", "post_type = ?", "nav_menu_item"); got != 0 {
		t.Errorf("after delete: nav_menu_item posts = %d, want 0 (orphan)", got)
	}
	if got := countRows(t, repo, "postmeta", "meta_key LIKE ?", "_menu_item_%"); got != 0 {
		t.Errorf("after delete: _menu_item_* postmeta rows = %d, want 0 (orphan)", got)
	}
	if got := countRows(t, repo, "term_relationships", "term_taxonomy_id = ?", ttID); got != 0 {
		t.Errorf("after delete: term_relationships for menu = %d, want 0 (orphan)", got)
	}

	// The foreign post and term are untouched.
	if got := countRows(t, repo, "posts", `"ID" = ?`, foreignPostID); got != 1 {
		t.Errorf("foreign post removed by DeleteMenu: count = %d, want 1 (must be untouched)", got)
	}
	if got := countRows(t, repo, "terms", "term_id = ?", foreignTermID); got != 1 {
		t.Errorf("foreign term removed by DeleteMenu: count = %d, want 1 (must be untouched)", got)
	}
}

// TestNavMenuRepoDeleteItem covers Req 2.3/6.3: DeleteItem tears down only that
// one item's posts/postmeta/relationship rows, leaving the menu's other item
// intact.
func TestNavMenuRepoDeleteItem(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	menuID, err := repo.CreateMenu(ctx, "DeleteOne", "delete-one")
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	ttID := termTaxonomyID(t, repo, menuID)

	keepID, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label: "Keep", URL: "https://example.com/keep", Type: "custom", Object: "custom", Order: 1,
	})
	if err != nil {
		t.Fatalf("CreateItem keep: %v", err)
	}
	dropID, err := repo.CreateItem(ctx, menuID, domain.NavMenuItemInput{
		Label: "Drop", URL: "https://example.com/drop", Type: "custom", Object: "custom", Order: 2,
	})
	if err != nil {
		t.Fatalf("CreateItem drop: %v", err)
	}

	if err := repo.DeleteItem(ctx, menuID, dropID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	// The dropped item's post, postmeta, and relationship rows are gone.
	if got := countRows(t, repo, "posts", `"ID" = ?`, dropID); got != 0 {
		t.Errorf("dropped item post = %d, want 0", got)
	}
	if got := countRows(t, repo, "postmeta", "post_id = ?", dropID); got != 0 {
		t.Errorf("dropped item postmeta = %d, want 0", got)
	}
	if got := countRows(t, repo, "term_relationships", "object_id = ? AND term_taxonomy_id = ?", dropID, ttID); got != 0 {
		t.Errorf("dropped item relationship = %d, want 0", got)
	}

	// The kept item is intact and still reachable through the read path.
	if got := countRows(t, repo, "posts", `"ID" = ?`, keepID); got != 1 {
		t.Errorf("kept item post = %d, want 1 (must be untouched)", got)
	}
	menu, err := repo.MenuByID(ctx, menuID)
	if err != nil {
		t.Fatalf("MenuByID: %v", err)
	}
	if _, ok := findItem(menu.Items, "Keep"); !ok {
		t.Errorf("kept item not reachable through read path; items=%+v", menu.Items)
	}
	if _, ok := findItem(menu.Items, "Drop"); ok {
		t.Errorf("dropped item still reachable through read path")
	}
}

// TestNavMenuRepoMissingReturnsNotFound covers Req 1.4/2.4: a missing menu or
// item yields domain.ErrNotFound from the relevant method.
func TestNavMenuRepoMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	name := "Nobody"
	if err := repo.UpdateMenu(ctx, 999999, domain.NavMenuPatch{Name: &name}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateMenu(unknown) = %v, want domain.ErrNotFound", err)
	}
	if err := repo.DeleteMenu(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DeleteMenu(unknown) = %v, want domain.ErrNotFound", err)
	}

	// A real menu with no such item: Update/Delete of a missing item 404s.
	menuID, err := repo.CreateMenu(ctx, "Real", "real")
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	label := "Ghost"
	if err := repo.UpdateItem(ctx, menuID, 888888, domain.NavMenuItemPatch{Label: &label}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateItem(unknown item) = %v, want domain.ErrNotFound", err)
	}
	if err := repo.DeleteItem(ctx, menuID, 888888); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DeleteItem(unknown item) = %v, want domain.ErrNotFound", err)
	}
}

// TestNavMenuRepoItemBelongsToMenu covers the parent-validation primitive
// (Req 2.4/5.4 support): true only for an item linked to that menu, false for a
// cross-menu item and for a nonexistent item.
func TestNavMenuRepoItemBelongsToMenu(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	menuA, err := repo.CreateMenu(ctx, "Menu A", "menu-a")
	if err != nil {
		t.Fatalf("CreateMenu A: %v", err)
	}
	menuB, err := repo.CreateMenu(ctx, "Menu B", "menu-b")
	if err != nil {
		t.Fatalf("CreateMenu B: %v", err)
	}

	itemA, err := repo.CreateItem(ctx, menuA, domain.NavMenuItemInput{
		Label: "In A", URL: "https://example.com/a", Type: "custom", Object: "custom", Order: 1,
	})
	if err != nil {
		t.Fatalf("CreateItem in A: %v", err)
	}

	// itemA belongs to menuA.
	if ok, err := repo.ItemBelongsToMenu(ctx, menuA, itemA); err != nil || !ok {
		t.Errorf("ItemBelongsToMenu(menuA, itemA) = (%v, %v), want (true, nil)", ok, err)
	}
	// itemA does NOT belong to menuB (cross-menu).
	if ok, err := repo.ItemBelongsToMenu(ctx, menuB, itemA); err != nil || ok {
		t.Errorf("ItemBelongsToMenu(menuB, itemA) = (%v, %v), want (false, nil)", ok, err)
	}
	// A nonexistent item belongs to no menu.
	if ok, err := repo.ItemBelongsToMenu(ctx, menuA, 777777); err != nil || ok {
		t.Errorf("ItemBelongsToMenu(menuA, nonexistent) = (%v, %v), want (false, nil)", ok, err)
	}
}

// TestNavMenuRepoCreateMenuSlugUniqueness covers the in-transaction slug
// re-check (Req 1.1): creating two menus whose names derive the same base slug
// yields a -N suffix on the second.
func TestNavMenuRepoCreateMenuSlugUniqueness(t *testing.T) {
	ctx := context.Background()
	repo := newNavMenuRepo(t)

	firstID, err := repo.CreateMenu(ctx, "Main", "main")
	if err != nil {
		t.Fatalf("CreateMenu first: %v", err)
	}
	// A second create that asks for the same base slug must be de-duplicated in
	// the transaction to a -N suffix, not collide.
	secondID, err := repo.CreateMenu(ctx, "Main", "main")
	if err != nil {
		t.Fatalf("CreateMenu second: %v", err)
	}
	if firstID == secondID {
		t.Fatalf("second CreateMenu returned the same term_id %d as the first", firstID)
	}

	first, err := repo.MenuByID(ctx, firstID)
	if err != nil {
		t.Fatalf("MenuByID first: %v", err)
	}
	second, err := repo.MenuByID(ctx, secondID)
	if err != nil {
		t.Fatalf("MenuByID second: %v", err)
	}
	if first.Slug != "main" {
		t.Errorf("first menu slug = %q, want %q", first.Slug, "main")
	}
	if second.Slug == "main" {
		t.Errorf("second menu slug = %q, want a -N suffix (e.g. main-2) on collision", second.Slug)
	}
}
