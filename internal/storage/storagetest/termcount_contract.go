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

// Fixture ids for the term-count contract, far above SeedFixtures and the
// other purger contracts.
const (
	tcSubject int64 = 9700 // published post with no relationship yet
	tcPub     int64 = 9701 // published post related to the category
	tcDraft   int64 = 9702 // draft post related to the category
	tcTrash   int64 = 9703 // trashed post related to the category
	tcObj     int64 = 9704 // object id of no post (a link_category link): related, counted
	tcTTCat   int64 = 9710 // category term_taxonomy_id (term_id equal)

	tcMenu   int64 = 9720 // nav_menu term_id
	tcTTMenu int64 = 9721 // nav_menu term_taxonomy_id
	tcPage   int64 = 9730 // page the target menu item points at
	tcItemT  int64 = 9740 // menu item of the page: removed by the operation
	tcItemP  int64 = 9741 // published custom menu item
	tcItemD  int64 = 9742 // draft custom menu item
	tcItemX  int64 = 9743 // trashed custom menu item
	tcItemO  int64 = 9744 // menu relationship whose object is no post

	tcStale = 99 // stale term_taxonomy.count seeded on every term
)

// RunTermCountContract covers the term count rule (Requirement 5): after
// SetPostTerms, PurgeMenuItemsOf, NavMenuWriter.DeleteItem and
// PurgeTermRelationshipsOf, term_taxonomy.count equals the term's
// relationships minus those whose object is a trashed post. A draft and an
// object that is not a post (a link_category link) still count.
//
// The vendor and table prefix come from newRepos; the contract holds no
// literal prefix, vendor name or placeholder.
func RunTermCountContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	setup := func(t *testing.T, seed func(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string)) (repos *storage.Repositories, db *sql.DB, vendor, prefix string, cleanup func()) {
		t.Helper()
		repos, vendor, prefix, cleanup = newRepos(t)
		db = repos.DB()
		seed(ctx, t, db, vendor, prefix)
		return repos, db, vendor, prefix, cleanup
	}
	// want checks the count of tt equals its relationships minus trashed
	// objects, computed independently of the code under test, and equals n.
	want := func(t *testing.T, db *sql.DB, vendor, prefix string, tt int64, n int, what string) {
		t.Helper()
		rels := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE term_taxonomy_id = ?`, tt)
		trashed := pdCount(ctx, t, db, vendor,
			`SELECT COUNT(*) FROM `+prefix+`term_relationships tr JOIN `+prefix+`posts p ON p.`+rebind.Ident(vendor, "ID")+` = tr.object_id `+
				`WHERE tr.term_taxonomy_id = ? AND p.post_status = 'trash'`, tt)
		if rels-trashed != n {
			t.Fatalf("%s: fixture has %d relationships and %d trashed, want %d counted", what, rels, trashed, n)
		}
		if got := miCount(ctx, t, db, vendor, prefix, tt); got != n {
			t.Errorf("%s: term_taxonomy.count = %d, want %d (relationships %d minus trashed %d)", what, got, n, rels, trashed)
		}
	}

	t.Run("SetPostTerms does not count the trashed object", func(t *testing.T) {
		repos, db, vendor, prefix, cleanup := setup(t, tcSeedPosts)
		defer cleanup()
		if err := repos.PostTermsWriter.SetPostTerms(ctx, tcSubject, "category", []int64{tcTTCat}); err != nil {
			t.Fatalf("SetPostTerms: %v", err)
		}
		// Relationships: subject, pub, draft, trash, obj. Counted: all but trash.
		want(t, db, vendor, prefix, tcTTCat, 4, "after SetPostTerms")
	})

	t.Run("PurgeTermRelationshipsOf does not count the trashed object", func(t *testing.T) {
		repos, db, vendor, prefix, cleanup := setup(t, tcSeedPosts)
		defer cleanup()
		if err := repos.PostRelationships.PurgeTermRelationshipsOf(ctx, tcPub); err != nil {
			t.Fatalf("PurgeTermRelationshipsOf: %v", err)
		}
		// Left: draft, trash, obj. Counted: draft and obj.
		want(t, db, vendor, prefix, tcTTCat, 2, "after purging the published post")
	})

	t.Run("PurgeTermRelationshipsOf of the trashed post counts the rest", func(t *testing.T) {
		repos, db, vendor, prefix, cleanup := setup(t, tcSeedPosts)
		defer cleanup()
		if err := repos.PostRelationships.PurgeTermRelationshipsOf(ctx, tcTrash); err != nil {
			t.Fatalf("PurgeTermRelationshipsOf: %v", err)
		}
		// Left: pub, draft, obj, none trashed.
		want(t, db, vendor, prefix, tcTTCat, 3, "after purging the trashed post")
	})

	t.Run("PurgeMenuItemsOf does not count the trashed menu item", func(t *testing.T) {
		repos, db, vendor, prefix, cleanup := setup(t, tcSeedMenu)
		defer cleanup()
		if err := repos.PostMenuItems.PurgeMenuItemsOf(ctx, tcPage, "page"); err != nil {
			t.Fatalf("PurgeMenuItemsOf: %v", err)
		}
		// Left: pub, draft, trash, obj. Counted: all but trash.
		want(t, db, vendor, prefix, tcTTMenu, 3, "after PurgeMenuItemsOf")
	})

	t.Run("NavMenuWriter.DeleteItem does not count the trashed menu item", func(t *testing.T) {
		repos, db, vendor, prefix, cleanup := setup(t, tcSeedMenu)
		defer cleanup()
		w, ok := repos.NavMenus.(domain.NavMenuWriter)
		if !ok {
			t.Fatalf("repos.NavMenus (%T) does not implement domain.NavMenuWriter", repos.NavMenus)
		}
		if err := w.DeleteItem(ctx, tcMenu, tcItemT); err != nil {
			t.Fatalf("DeleteItem: %v", err)
		}
		want(t, db, vendor, prefix, tcTTMenu, 3, "after DeleteItem")
	})
}

// tcExec returns a fixture statement runner bound to db and vendor.
func tcExec(ctx context.Context, t *testing.T, db *sql.DB, vendor string) func(q string, args ...any) {
	t.Helper()
	return func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
}

// tcSeedPosts inserts one category term (stale count) and the posts subject
// (unrelated), pub, draft and trash, each of the last three related to the
// term, plus a relationship whose object id matches no post.
func tcSeedPosts(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	exec := tcExec(ctx, t, db, vendor)
	seedTermTaxonomy(ctx, t, db, vendor, prefix, tcTTCat, "category", tcStale)
	for _, p := range []struct {
		id     int64
		status string
	}{{tcSubject, "publish"}, {tcPub, "publish"}, {tcDraft, "draft"}, {tcTrash, "trash"}} {
		slug := "tc-" + strconv.FormatInt(p.id, 10)
		exec(postInsert(vendor, prefix), postArgs(p.id, slug, "T "+slug, "post", p.status, "2000-01-01 00:00:00", "open", 0, "", 0)...)
	}
	for _, obj := range []int64{tcPub, tcDraft, tcTrash, tcObj} {
		exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, obj, tcTTCat, 0)
	}
}

// tcSeedMenu inserts a nav_menu term (stale count), the page tcPage and the
// menu items: tcItemT points at the page, the others are custom items that are
// published, draft and trashed, and one relationship has no post behind it.
func tcSeedMenu(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	exec := tcExec(ctx, t, db, vendor)
	dec := func(n int64) string { return strconv.FormatInt(n, 10) }
	exec(`INSERT INTO `+prefix+`terms (term_id, name, slug) VALUES (?, ?, ?)`, tcMenu, "tc-menu", "tc-menu")
	exec(`INSERT INTO `+prefix+`term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`,
		tcTTMenu, tcMenu, "nav_menu", "", 0, tcStale)
	exec(postInsert(vendor, prefix), postArgs(tcPage, "tc-page", "T tc-page", "page", "publish", "2000-01-01 00:00:00", "closed", 0, "", 0)...)
	item := func(id int64, status, typ, object string, objectID int64) {
		t.Helper()
		slug := "tc-item-" + dec(id)
		exec(postInsert(vendor, prefix), postArgs(id, slug, "T "+slug, "nav_menu_item", status, "2000-01-01 00:00:00", "closed", 0, "", 0)...)
		for _, kv := range [][2]string{
			{"_menu_item_type", typ}, {"_menu_item_object", object},
			{"_menu_item_object_id", dec(objectID)}, {"_menu_item_menu_item_parent", "0"},
		} {
			exec(`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, id, kv[0], kv[1])
		}
		exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, id, tcTTMenu, 0)
	}
	item(tcItemT, "publish", "post_type", "page", tcPage)
	item(tcItemP, "publish", "custom", "custom", tcItemP)
	item(tcItemD, "draft", "custom", "custom", tcItemD)
	item(tcItemX, "trash", "custom", "custom", tcItemX)
	exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, tcItemO, tcTTMenu, 0)
}
