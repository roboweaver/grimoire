package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/wprepo"
)

// M20 task 3.1: RED assembly tests for re-parenting the children of a deleted
// menu item (Req 1.1-1.3, 1.8, 3.2, 4.1, 4.3, 6.1, 6.2). They fail until the
// item-delete helper of tasks 2.1/2.2 re-points the children and recounts: today
// the children keep naming the deleted id, buildMenuTree never reaches them, and
// the menu editor's item delete leaves term_taxonomy.count stale.

type reparentFixture struct {
	menu      int64
	page      int64 // the page the deleted item points at
	top       int64 // top-level custom link, menu_order 0
	outer     int64 // optional parent of the deleted item (0 when top level)
	deleted   int64 // the page item that is deleted
	childA    int64 // child of deleted, menu_order 5
	childB    int64 // child of deleted, menu_order 4 (lists before childA)
	grandchld int64 // child of childA
}

func setMenuOrder(t *testing.T, env customWriteEnv, id int64, order int) {
	t.Helper()
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"UPDATE "+env.Prefix+"posts SET menu_order = ?, post_title = ? WHERE "+postsIDColumn+" = ?",
		order, fmt.Sprintf("Item%d", id), id); err != nil {
		t.Fatalf("setMenuOrder %d: %v", id, err)
	}
}

func customLink(t *testing.T, env customWriteEnv, menu, parent int64, order int) int64 {
	t.Helper()
	id := seedRawMenuItemWithParent(t, env, menu, "custom", 0, parent)
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"UPDATE "+env.Prefix+"postmeta SET meta_value = 'custom' WHERE post_id = ? AND meta_key = '_menu_item_type'", id); err != nil {
		t.Fatalf("make custom link: %v", err)
	}
	setMenuOrder(t, env, id, order)
	return id
}

// seedReparentFixture builds menu M1 with a page item (optionally nested under
// outer) that has two children and a grandchild, plus a top-level custom link.
func seedReparentFixture(t *testing.T, env customWriteEnv, withOuter bool) reparentFixture {
	t.Helper()
	f := reparentFixture{}
	f.menu = createMenu(t, env, "Menu One", "menu-one")
	f.page = createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/pages", "Subject")
	f.top = customLink(t, env, f.menu, 0, 0)
	var outerParent int64
	if withOuter {
		f.outer = customLink(t, env, f.menu, 0, 1)
		outerParent = f.outer
	}
	f.deleted = seedRawMenuItemWithParent(t, env, f.menu, "page", f.page, outerParent)
	setMenuOrder(t, env, f.deleted, 2)
	f.childA = customLink(t, env, f.menu, f.deleted, 5)
	f.childB = customLink(t, env, f.menu, f.deleted, 4)
	f.grandchld = customLink(t, env, f.menu, f.childA, 6)
	return f
}

type reparentNode struct {
	ID       int64          `json:"id"`
	ParentID int64          `json:"parentId"`
	Children []reparentNode `json:"children"`
}

func menuTree(t *testing.T, env customWriteEnv, menu int64) []reparentNode {
	t.Helper()
	rec := doJSON(t, env.Handler, &env.Admin, http.MethodGet, fmt.Sprintf("/admin/api/menus/%d", menu), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("menu read %d: status = %d, want 200 (body %s)", menu, rec.Code, rec.Body.String())
	}
	var m struct {
		Items []reparentNode `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("menu read body %s: %v", rec.Body.String(), err)
	}
	return m.Items
}

func nodeIDs(ns []reparentNode) []int64 {
	ids := []int64{}
	for _, n := range ns {
		ids = append(ids, n.ID)
	}
	return ids
}

func findNode(ns []reparentNode, id int64) *reparentNode {
	for i := range ns {
		if n := findNode(ns[i].Children, id); n != nil {
			return n
		}
		if ns[i].ID == id {
			return &ns[i]
		}
	}
	return nil
}

// assertReparented checks the tree after the deleted item is gone: its two
// children sit, in menu_order, directly under the deleted item's parent (or at
// the top level), and the grandchild is still under its own parent.
func assertReparented(t *testing.T, env customWriteEnv, f reparentFixture) {
	t.Helper()
	tree := menuTree(t, env, f.menu)
	if findNode(tree, f.deleted) != nil {
		t.Errorf("deleted item %d still listed in menu read", f.deleted)
	}
	var level []reparentNode
	var want []int64
	if f.outer != 0 {
		outer := findNode(tree, f.outer)
		if outer == nil {
			t.Fatalf("outer item %d missing from menu read", f.outer)
		}
		level, want = outer.Children, []int64{f.childB, f.childA}
	} else {
		level, want = tree, []int64{f.top, f.childB, f.childA}
	}
	if got := nodeIDs(level); !reflect.DeepEqual(got, want) {
		t.Errorf("children of the deleted item's parent = %v, want %v (original order)", got, want)
	}
	a := findNode(tree, f.childA)
	if a == nil || !reflect.DeepEqual(nodeIDs(a.Children), []int64{f.grandchld}) {
		t.Errorf("grandchild %d is not still under child %d: %+v", f.grandchld, f.childA, a)
	}
	// Storage: the stored parents are the decimal ids (or "0").
	wantParent := fmt.Sprint(f.outer)
	for _, c := range []int64{f.childA, f.childB} {
		if got := metaValue(t, env, c, "_menu_item_menu_item_parent"); got != wantParent {
			t.Errorf("child %d _menu_item_menu_item_parent = %q, want %q", c, got, wantParent)
		}
	}
	if got := metaValue(t, env, f.grandchld, "_menu_item_menu_item_parent"); got != fmt.Sprint(f.childA) {
		t.Errorf("grandchild parent = %q, want %d", got, f.childA)
	}
}

func metaValue(t *testing.T, env customWriteEnv, post int64, key string) string {
	t.Helper()
	var v string
	if err := env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT meta_value FROM "+env.Prefix+"postmeta WHERE post_id = ? AND meta_key = ?", post, key).Scan(&v); err != nil {
		t.Fatalf("metaValue %d %s: %v", post, key, err)
	}
	return v
}

// TestBuildHandlerMenuItemReparentPageDelete (Req 1.1, 1.2, 1.8, 6.2): deleting
// the page re-parents the deleted item's children, with and without a parent.
func TestBuildHandlerMenuItemReparentPageDelete(t *testing.T) {
	for _, withOuter := range []bool{false, true} {
		name := "top level item"
		if withOuter {
			name = "item with parent"
		}
		t.Run(name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			f := seedReparentFixture(t, env, withOuter)
			createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/pages", "Keeper")
			if _, err := env.Repos.DB().ExecContext(context.Background(),
				"INSERT INTO "+env.Prefix+"options (option_name, option_value, autoload) VALUES (?, ?, ?)",
				"theme_mods_default", fmt.Sprintf(`a:1:{s:18:"nav_menu_locations";a:1:{s:7:"primary";i:%d;}}`, f.menu), "yes"); err != nil {
				t.Fatalf("seed theme mods: %v", err)
			}

			rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, fmt.Sprintf("/admin/api/posts/%d", f.page), "")
			if rec.Code != http.StatusNoContent {
				t.Fatalf("DELETE page: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			assertReparented(t, env, f)

			// Public menu render still shows the children (Req 1.8).
			pub := doJSON(t, env.Handler, nil, http.MethodGet, "/keeper/", "")
			if pub.Code != http.StatusOK {
				t.Fatalf("GET /keeper/: status = %d, want 200", pub.Code)
			}
			for _, id := range []int64{f.childA, f.childB, f.grandchld} {
				if label := fmt.Sprintf("Item%d", id); !strings.Contains(pub.Body.String(), label) {
					t.Errorf("public menu render lost %s after the page delete", label)
				}
			}
		})
	}
}

// TestBuildHandlerMenuItemReparentEditorDelete (Req 1.3, 4.1, 4.3): the menu
// editor's item delete re-parents the same way and recounts the menu; an item of
// another menu answers 404 and changes nothing.
func TestBuildHandlerMenuItemReparentEditorDelete(t *testing.T) {
	for _, withOuter := range []bool{false, true} {
		name := "top level item"
		if withOuter {
			name = "item with parent"
		}
		t.Run(name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			f := seedReparentFixture(t, env, withOuter)
			if _, err := env.Repos.DB().ExecContext(context.Background(),
				"UPDATE "+env.Prefix+"term_taxonomy SET count = 99 WHERE term_taxonomy_id = ?", f.menu); err != nil {
				t.Fatalf("seed stale count: %v", err)
			}

			rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete,
				fmt.Sprintf("/admin/api/menus/%d/items/%d", f.menu, f.deleted), "")
			if rec.Code != http.StatusNoContent {
				t.Fatalf("DELETE item: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			assertReparented(t, env, f)
			rels := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE term_taxonomy_id = ?", f.menu)
			if got := hardeningTermCount(t, env, f.menu); got != rels {
				t.Errorf("menu term_taxonomy.count = %d, want %d (remaining relationships)", got, rels)
			}
		})
	}

	t.Run("item of another menu", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		f := seedReparentFixture(t, env, true)
		other := createMenu(t, env, "Menu Two", "menu-two")
		foreign := customLink(t, env, other, 0, 0)
		ids := []int64{f.top, f.outer, f.deleted, f.childA, f.childB, f.grandchld, foreign}
		before := reparentSnapshot(t, env, f.menu, other, ids)

		rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete,
			fmt.Sprintf("/admin/api/menus/%d/items/%d", f.menu, foreign), "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("DELETE foreign item: status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
		}
		if after := reparentSnapshot(t, env, f.menu, other, ids); after != before {
			t.Errorf("rows changed by a 404 delete\nbefore: %s\nafter:  %s", before, after)
		}
	})
}

func reparentSnapshot(t *testing.T, env customWriteEnv, menus1, menu2 int64, ids []int64) string {
	t.Helper()
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "%s|", rowSnapshot(t, env, id))
		rows, err := env.Repos.DB().QueryContext(context.Background(),
			"SELECT meta_key, meta_value FROM "+env.Prefix+"postmeta WHERE post_id = ? ORDER BY meta_id", id)
		if err != nil {
			t.Fatalf("snapshot meta: %v", err)
		}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				rows.Close()
				t.Fatalf("snapshot scan: %v", err)
			}
			fmt.Fprintf(&b, "%s=%s;", k, v)
		}
		rows.Close()
	}
	for _, m := range []int64{menus1, menu2} {
		fmt.Fprintf(&b, "count%d=%d rels%d=%d|", m, hardeningTermCount(t, env, m), m,
			hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE term_taxonomy_id = ?", m))
	}
	return b.String()
}

// failingMenuItemWriter fails the menu editor's item delete.
type failingMenuItemWriter struct{ *wprepo.NavMenuRepo }

func (failingMenuItemWriter) DeleteItem(context.Context, int64, int64) error {
	return errors.New("menu item delete unavailable")
}

// TestBuildHandlerMenuItemReparentFailures (Req 3.2, 6.1): a failing purger and a
// failing menu writer answer 500 and leave every row unchanged.
func TestBuildHandlerMenuItemReparentFailures(t *testing.T) {
	t.Run("failing purger", func(t *testing.T) {
		env := newCustomWriteHandlerWith(t, true, func(repos *storage.Repositories) {
			f := reflect.ValueOf(repos).Elem().FieldByName("PostMenuItems")
			f.Set(reflect.ValueOf(failingMenuPurger{}))
		})
		f := seedReparentFixture(t, env, true)
		ids := []int64{f.top, f.outer, f.deleted, f.childA, f.childB, f.grandchld, f.page}
		before := reparentSnapshot(t, env, f.menu, f.menu, ids)

		rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, fmt.Sprintf("/admin/api/posts/%d", f.page), "")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("DELETE page: status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
		}
		if after := reparentSnapshot(t, env, f.menu, f.menu, ids); after != before {
			t.Errorf("rows changed by a failed delete\nbefore: %s\nafter:  %s", before, after)
		}
	})

	t.Run("failing menu writer", func(t *testing.T) {
		env := newCustomWriteHandlerWith(t, true, func(repos *storage.Repositories) {
			real, ok := repos.NavMenus.(*wprepo.NavMenuRepo)
			if !ok {
				t.Fatalf("repos.NavMenus is %T, want *wprepo.NavMenuRepo", repos.NavMenus)
			}
			repos.NavMenus = failingMenuItemWriter{real}
		})
		f := seedReparentFixture(t, env, true)
		ids := []int64{f.top, f.outer, f.deleted, f.childA, f.childB, f.grandchld}
		before := reparentSnapshot(t, env, f.menu, f.menu, ids)

		rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete,
			fmt.Sprintf("/admin/api/menus/%d/items/%d", f.menu, f.deleted), "")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("DELETE item: status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
		}
		if after := reparentSnapshot(t, env, f.menu, f.menu, ids); after != before {
			t.Errorf("rows changed by a failed delete\nbefore: %s\nafter:  %s", before, after)
		}
	})
}
