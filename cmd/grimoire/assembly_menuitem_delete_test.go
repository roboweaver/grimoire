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

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
)

// M19 task 11.1: RED assembly tests for the menu-item purge on post delete
// (Req 10.1-10.10, 13.1, 13.2). Every assertion goes through buildHandler as
// main wires it. They fail until task 11.2 passes
// content.WithPostMenuItemPurger(repos.PostMenuItems) to NewPostWriteService:
// without it a deleted post's nav-menu items remain and the menu read lists them.

// menuDeleteCase is one delete surface: how the subject rows are created, which
// post type they are, and how the subject is deleted.
type menuDeleteCase struct {
	name       string
	typ        string // stored post_type and _menu_item_object of the items
	createPath string // REST collection that creates the rows
	actor      func(customWriteEnv) *customWriteActor
	deletePath func(id int64) string
	wantStatus int
}

func menuDeleteCases() []menuDeleteCase {
	admin := func(e customWriteEnv) *customWriteActor { return &e.Admin }
	products := func(e customWriteEnv) *customWriteActor { return &e.Products }
	adminDel := func(id int64) string { return fmt.Sprintf("/admin/api/posts/%d?force=true", id) }
	restDel := func(base string) func(int64) string {
		return func(id int64) string { return fmt.Sprintf("%s/%d?force=true", base, id) }
	}
	return []menuDeleteCase{
		{"admin page", "page", "/wp-json/wp/v2/pages", admin, adminDel, http.StatusNoContent},
		{"admin post", "post", "/wp-json/wp/v2/posts", admin, adminDel, http.StatusNoContent},
		{"rest page", "page", "/wp-json/wp/v2/pages", admin, restDel("/wp-json/wp/v2/pages"), http.StatusOK},
		{"rest post", "post", "/wp-json/wp/v2/posts", admin, restDel("/wp-json/wp/v2/posts"), http.StatusOK},
		{"rest registered type", "product", "/wp-json/wp/v2/product", products, restDel("/wp-json/wp/v2/product"), http.StatusOK},
	}
}

// menuDeleteFixture is the seeded scenario: subject P linked from M1 (twice) and
// M2, plus rows that must survive.
type menuDeleteFixture struct {
	m1, m2         int64
	subject, other int64
	subjectItems1  []int64 // two items of P in M1
	subjectItem2   int64   // the item of P in M2
	custom, tax    int64   // custom link and taxonomy item (object id = P) in M1
	otherItem      int64   // item for Q in M2
	label          map[int64]string
}

func (f menuDeleteFixture) subjectItems() []int64 {
	return append(append([]int64{}, f.subjectItems1...), f.subjectItem2)
}

func createMenu(t *testing.T, env customWriteEnv, name, slug string) int64 {
	t.Helper()
	rec := doJSON(t, env.Handler, &env.Admin, http.MethodPost, "/admin/api/menus",
		fmt.Sprintf(`{"name":%q,"slug":%q}`, name, slug))
	if rec.Code != http.StatusCreated {
		t.Fatalf("menu create %s: status = %d, want 201 (body %s)", name, rec.Code, rec.Body.String())
	}
	var m struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.ID == 0 {
		t.Fatalf("menu create body %s: id %d, err %v", rec.Body.String(), m.ID, err)
	}
	return m.ID
}

func createRESTRow(t *testing.T, env customWriteEnv, actor *customWriteActor, path, title string) int64 {
	t.Helper()
	rec := doJSON(t, env.Handler, actor, http.MethodPost, path, fmt.Sprintf(`{"title":%q,"slug":%q,"status":"publish"}`, title, strings.ToLower(title)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s %q: status = %d, want 201 (body %s)", path, title, rec.Code, rec.Body.String())
	}
	var r struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || r.ID == 0 {
		t.Fatalf("create body %s: id %d, err %v", rec.Body.String(), r.ID, err)
	}
	return r.ID
}

// seedMenuDeleteFixture builds the scenario for case c. Menu term counts start
// at the true relationship counts so a recount is observable.
func seedMenuDeleteFixture(t *testing.T, env customWriteEnv, c menuDeleteCase) menuDeleteFixture {
	t.Helper()
	f := menuDeleteFixture{label: map[int64]string{}}
	f.m1 = createMenu(t, env, "Menu One", "menu-one")
	f.m2 = createMenu(t, env, "Menu Two", "menu-two")
	actor := c.actor(env)
	f.subject = createRESTRow(t, env, actor, c.createPath, "Subject")
	f.other = createRESTRow(t, env, actor, c.createPath, "Other")

	item := func(menu int64, typ string, objectID int64, label string) int64 {
		id := seedRawMenuItem(t, env, menu, typ, objectID)
		if _, err := env.Repos.DB().ExecContext(context.Background(),
			"UPDATE "+env.Prefix+"posts SET post_title = ? WHERE "+postsIDColumn+" = ?", label, id); err != nil {
			t.Fatalf("label item %d: %v", id, err)
		}
		f.label[id] = label
		return id
	}
	f.subjectItems1 = []int64{item(f.m1, c.typ, f.subject, "SubjectLinkA"), item(f.m1, c.typ, f.subject, "SubjectLinkB")}
	f.custom = item(f.m1, "custom", 0, "CustomLink")
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"UPDATE "+env.Prefix+"postmeta SET meta_value = 'custom' WHERE post_id = ? AND meta_key = '_menu_item_type'", f.custom); err != nil {
		t.Fatalf("make custom link: %v", err)
	}
	f.tax = item(f.m1, "category", f.subject, "TaxonomyItem")
	f.subjectItem2 = item(f.m2, c.typ, f.subject, "SubjectLinkM2")
	f.otherItem = item(f.m2, c.typ, f.other, "OtherPageItem")

	for menu, n := range map[int64]int{f.m1: 4, f.m2: 2} {
		if _, err := env.Repos.DB().ExecContext(context.Background(),
			"UPDATE "+env.Prefix+"term_taxonomy SET count = ? WHERE term_taxonomy_id = ?", n, menu); err != nil {
			t.Fatalf("seed menu count: %v", err)
		}
	}
	return f
}

// menuItemIDs returns the ids GET /admin/api/menus/{id} lists, flattened.
func menuItemIDs(t *testing.T, env customWriteEnv, h http.Handler, menu int64) map[int64]bool {
	t.Helper()
	rec := doJSON(t, h, &env.Admin, http.MethodGet, fmt.Sprintf("/admin/api/menus/%d", menu), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("menu read %d: status = %d, want 200 (body %s)", menu, rec.Code, rec.Body.String())
	}
	type node struct {
		ID       int64  `json:"id"`
		Children []node `json:"children"`
	}
	var m struct {
		Items []node `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("menu read body %s: %v", rec.Body.String(), err)
	}
	ids := map[int64]bool{}
	var walk func([]node)
	walk = func(ns []node) {
		for _, n := range ns {
			ids[n.ID] = true
			walk(n.Children)
		}
	}
	walk(m.Items)
	return ids
}

func rowExists(t *testing.T, env customWriteEnv, id int64) bool {
	t.Helper()
	return hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"posts WHERE "+postsIDColumn+" = ?", id) == 1
}

// itemRelCount counts postmeta plus relationship rows of the given items.
func itemRelCount(t *testing.T, env customWriteEnv, ids ...int64) (meta, rels int) {
	t.Helper()
	for _, id := range ids {
		meta += hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"postmeta WHERE post_id = ?", id)
		rels += hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE object_id = ?", id)
	}
	return meta, rels
}

// TestBuildHandlerMenuItemDelete (Req 10.1-10.5, 10.9): deleting a post removes
// its menu items from the menu read and the database, and nothing else.
func TestBuildHandlerMenuItemDelete(t *testing.T) {
	for _, c := range menuDeleteCases() {
		t.Run(c.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			f := seedMenuDeleteFixture(t, env, c)

			// Before the delete the menu read lists every seeded item.
			for _, id := range append(f.subjectItems(), f.custom, f.tax, f.otherItem) {
				menu := f.m1
				if id == f.subjectItem2 || id == f.otherItem {
					menu = f.m2
				}
				if !menuItemIDs(t, env, env.Handler, menu)[id] {
					t.Fatalf("precondition: menu %d read does not list item %d", menu, id)
				}
			}
			keptMeta, keptRels := itemRelCount(t, env, f.custom, f.tax, f.otherItem)

			rec := doJSON(t, env.Handler, c.actor(env), http.MethodDelete, c.deletePath(f.subject), "")
			if rec.Code != c.wantStatus {
				t.Fatalf("DELETE subject: status = %d, want %d (body %s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			if rowExists(t, env, f.subject) {
				t.Fatalf("subject row still exists")
			}

			// Menu read (Req 10.9): P's items gone, the others still listed.
			m1, m2 := menuItemIDs(t, env, env.Handler, f.m1), menuItemIDs(t, env, env.Handler, f.m2)
			for _, id := range f.subjectItems1 {
				if m1[id] {
					t.Errorf("menu M1 read still lists deleted post's item %d", id)
				}
			}
			if m2[f.subjectItem2] {
				t.Errorf("menu M2 read still lists deleted post's item %d", f.subjectItem2)
			}
			for _, id := range []int64{f.custom, f.tax} {
				if !m1[id] {
					t.Errorf("menu M1 read lost unrelated item %d", id)
				}
			}
			if !m2[f.otherItem] {
				t.Errorf("menu M2 read lost the other post's item %d", f.otherItem)
			}

			// SQL (Req 10.2-10.5): rows, postmeta and relationships gone; others intact.
			for _, id := range f.subjectItems() {
				if rowExists(t, env, id) {
					t.Errorf("menu item row %d still exists", id)
				}
			}
			if meta, rels := itemRelCount(t, env, f.subjectItems()...); meta != 0 || rels != 0 {
				t.Errorf("deleted items left %d postmeta and %d relationship rows", meta, rels)
			}
			for _, id := range []int64{f.custom, f.tax, f.otherItem} {
				if !rowExists(t, env, id) {
					t.Errorf("unrelated menu item row %d was deleted", id)
				}
			}
			if meta, rels := itemRelCount(t, env, f.custom, f.tax, f.otherItem); meta != keptMeta || rels != keptRels {
				t.Errorf("unrelated items' postmeta/relationships = %d/%d, want %d/%d", meta, rels, keptMeta, keptRels)
			}
			if got := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"terms WHERE term_id IN (?, ?)", f.m1, f.m2); got != 2 {
				t.Errorf("menu terms remaining = %d, want 2", got)
			}

			// Recount (Req 10.3): count equals the remaining relationship count.
			for menu, want := range map[int64]int{f.m1: 2, f.m2: 1} {
				rels := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE term_taxonomy_id = ?", menu)
				if rels != want {
					t.Errorf("menu %d remaining relationships = %d, want %d", menu, rels, want)
				}
				if got := hardeningTermCount(t, env, menu); got != rels {
					t.Errorf("menu %d term_taxonomy.count = %d, want %d (remaining relationships)", menu, got, rels)
				}
			}
		})
	}
}

// failingMenuPurger is a PostMenuItemPurger that always fails.
type failingMenuPurger struct{}

func (failingMenuPurger) PurgeMenuItemsOf(context.Context, int64, string) error {
	return errors.New("menu purge unavailable")
}

// TestBuildHandlerMenuItemDeletePurgeFailure (Req 10.7): a failing purger answers
// 500, keeps the post row and its menu items, and a retry with the working purger
// succeeds. The purger is swapped in through reflection so the test compiles
// before the PostMenuItems port exists.
func TestBuildHandlerMenuItemDeletePurgeFailure(t *testing.T) {
	var original reflect.Value
	env := newCustomWriteHandlerWith(t, true, func(repos *storage.Repositories) {
		f := reflect.ValueOf(repos).Elem().FieldByName("PostMenuItems")
		if !f.IsValid() {
			t.Fatalf("storage.Repositories has no PostMenuItems field (design: PR 3)")
		}
		if !reflect.TypeOf(failingMenuPurger{}).AssignableTo(f.Type()) {
			t.Fatalf("PostMenuItems has type %s, which failingMenuPurger does not implement", f.Type())
		}
		original = reflect.New(f.Type()).Elem()
		original.Set(f)
		f.Set(reflect.ValueOf(failingMenuPurger{}))
	})
	c := menuDeleteCases()[0]
	f := seedMenuDeleteFixture(t, env, c)
	path := c.deletePath(f.subject)

	rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, path, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE with failing purger: status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	if !rowExists(t, env, f.subject) {
		t.Fatalf("post row removed although the menu purger failed")
	}
	for _, id := range f.subjectItems() {
		if !rowExists(t, env, id) {
			t.Errorf("menu item %d removed although the menu purger failed", id)
		}
	}

	reflect.ValueOf(env.Repos).Elem().FieldByName("PostMenuItems").Set(original)
	h, _ := assemblyHandler(t, env.Repos, config.DatabaseConfig{Vendor: "sqlite", TablePrefix: env.Prefix}, customWriteRegistry(t))
	rec = doJSON(t, h, &env.Admin, http.MethodDelete, path, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retry DELETE: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if rowExists(t, env, f.subject) {
		t.Errorf("post row still exists after retry")
	}
	for _, id := range f.subjectItems() {
		if rowExists(t, env, id) {
			t.Errorf("menu item %d still exists after retry", id)
		}
	}
}

// TestBuildHandlerMenuItemDeletePublicRender (Req 10.9): a public page render of
// the primary menu no longer includes the deleted page's item.
func TestBuildHandlerMenuItemDeletePublicRender(t *testing.T) {
	c := menuDeleteCases()[2] // rest page
	env := newCustomWriteHandler(t, true)
	f := seedMenuDeleteFixture(t, env, c)
	createRESTRow(t, env, &env.Admin, c.createPath, "Keeper")
	// Point the default theme's primary location at M1.
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"INSERT INTO "+env.Prefix+"options (option_name, option_value, autoload) VALUES (?, ?, ?)",
		"theme_mods_default", fmt.Sprintf(`a:1:{s:18:"nav_menu_locations";a:1:{s:7:"primary";i:%d;}}`, f.m1), "yes"); err != nil {
		t.Fatalf("seed theme mods: %v", err)
	}
	render := func() string {
		rec := doJSON(t, env.Handler, nil, http.MethodGet, "/keeper/", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /keeper/: status = %d, want 200", rec.Code)
		}
		return rec.Body.String()
	}
	if body := render(); !strings.Contains(body, "SubjectLinkA") || !strings.Contains(body, "CustomLink") {
		t.Fatalf("precondition: public page does not render the seeded menu")
	}
	if rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, c.deletePath(f.subject), ""); rec.Code != http.StatusOK {
		t.Fatalf("DELETE subject: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := render()
	for _, gone := range []string{"SubjectLinkA", "SubjectLinkB"} {
		if strings.Contains(body, gone) {
			t.Errorf("public page still renders deleted page's menu item %q", gone)
		}
	}
	if !strings.Contains(body, "CustomLink") {
		t.Errorf("public page lost the unrelated custom link")
	}
}

// TestBuildHandlerMenuItemDeleteUnrelatedGETs (Req 10.10): GET responses that do
// not involve the deleted post are unchanged by the delete.
func TestBuildHandlerMenuItemDeleteUnrelatedGETs(t *testing.T) {
	c := menuDeleteCases()[2]
	env := newCustomWriteHandler(t, true)
	f := seedMenuDeleteFixture(t, env, c)
	paths := []string{
		fmt.Sprintf("/wp-json/wp/v2/pages/%d", f.other),
		fmt.Sprintf("/admin/api/posts/%d", f.other),
		"/admin/api/menus",
	}
	before := map[string]string{}
	for _, p := range paths {
		before[p] = doJSON(t, env.Handler, &env.Admin, http.MethodGet, p, "").Body.String()
	}
	if rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, c.deletePath(f.subject), ""); rec.Code != http.StatusOK {
		t.Fatalf("DELETE subject: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// The menu list legitimately changes (it lists the deleted items); the other
	// post's own reads must not.
	for _, p := range paths[:2] {
		if got := doJSON(t, env.Handler, &env.Admin, http.MethodGet, p, "").Body.String(); got != before[p] {
			t.Errorf("GET %s changed after deleting an unrelated post\nbefore: %s\nafter:  %s", p, before[p], got)
		}
	}
}
