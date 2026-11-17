package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
)

// M19 task 3.1: assembly tests for the term-side write gate, driven through
// buildHandler. They are RED on the tree before the gate (task 2.2): the admin
// term routes write terms of unmanaged taxonomies and the admin termIds map
// writes relationships in any taxonomy.

// termGateAdminTerm is PUT/DELETE /admin/api/terms/{id}.
func termGateAdminTerm(id int64) string { return fmt.Sprintf("/admin/api/terms/%d", id) }

// termGateRelCount counts term_relationships rows of object id in taxonomy.
func termGateRelCount(t *testing.T, env customWriteEnv, id int64, taxonomy string) int {
	t.Helper()
	return hardeningCount(t, env,
		"SELECT COUNT(*) FROM "+env.Prefix+"term_relationships r JOIN "+env.Prefix+
			"term_taxonomy x ON x.term_taxonomy_id = r.term_taxonomy_id WHERE r.object_id = ? AND x.taxonomy = ?",
		id, taxonomy)
}

func TestBuildHandlerTermGate(t *testing.T) {
	for _, withRegistry := range []bool{true, false} {
		name := "with registry"
		if !withRegistry {
			name = "without registry"
		}
		t.Run(name, func(t *testing.T) {
			env := newCustomWriteHandler(t, withRegistry)
			h := env.Handler
			unmanaged := map[string]int64{}
			for _, tax := range termGateUnmanaged {
				unmanaged[tax] = seedRawTerm(t, env, "Raw "+tax, "raw-"+tax, tax, 0)
			}
			cat := hardeningSeedTerm(t, env, "Managed Cat", "managed-cat", "category")
			post := seedRawPost(t, env, "post", "publish", 0)
			seedRawTermRelationship(t, env, post, cat)

			t.Run("admin PUT and DELETE of unmanaged terms are 403", func(t *testing.T) {
				for _, tax := range termGateUnmanaged {
					id := unmanaged[tax]
					for _, body := range []string{
						`{"name":"Hijacked"}`,
						`{"name":"Hijacked","taxonomy":"category"}`, // claims a different taxonomy (Req 1.4)
					} {
						before := termSnapshot(t, env, id)
						rec := doJSON(t, h, &env.Admin, http.MethodPut, termGateAdminTerm(id), body)
						if rec.Code != http.StatusForbidden {
							t.Errorf("PUT %s term %s: status = %d, want 403 (body %s)", tax, body, rec.Code, rec.Body.String())
						} else if c := termGateErrCode(t, rec.Body.Bytes()); c != "forbidden" {
							t.Errorf("PUT %s term: code = %q, want forbidden", tax, c)
						}
						if after := termSnapshot(t, env, id); after != before {
							t.Errorf("PUT %s term changed it\nbefore: %s\nafter:  %s", tax, before, after)
						}
					}
					before := termSnapshot(t, env, id)
					rec := doJSON(t, h, &env.Admin, http.MethodDelete, termGateAdminTerm(id), "")
					if rec.Code != http.StatusForbidden {
						t.Errorf("DELETE %s term: status = %d, want 403 (body %s)", tax, rec.Code, rec.Body.String())
					}
					if after := termSnapshot(t, env, id); after != before {
						t.Errorf("DELETE %s term changed it\nbefore: %s\nafter:  %s", tax, before, after)
					}
				}
			})

			t.Run("managed terms still write", func(t *testing.T) {
				managed := map[string]int64{
					"category": hardeningSeedTerm(t, env, "Cat Two", "cat-two", "category"),
					"post_tag": hardeningSeedTerm(t, env, "Tag One", "tag-one", "post_tag"),
				}
				if withRegistry {
					managed["genre"] = hardeningSeedTerm(t, env, "Genre One", "genre-one", "genre")
				}
				for tax, id := range managed {
					rec := doJSON(t, h, &env.Admin, http.MethodPut, termGateAdminTerm(id), `{"name":"Renamed"}`)
					if rec.Code != http.StatusOK {
						t.Errorf("PUT managed %s term: status = %d, want 200 (body %s)", tax, rec.Code, rec.Body.String())
					}
					rec = doJSON(t, h, &env.Admin, http.MethodDelete, termGateAdminTerm(id), "")
					if rec.Code != http.StatusNoContent {
						t.Errorf("DELETE managed %s term: status = %d, want 204 (body %s)", tax, rec.Code, rec.Body.String())
					}
				}
			})

			t.Run("registry-less taxonomy terms are unmanaged", func(t *testing.T) {
				if withRegistry {
					t.Skip("covered by the without-registry run: genre is unmanaged there (Req 4.5)")
				}
				id := seedRawTerm(t, env, "Former Genre", "former-genre", "genre", 0)
				before := termSnapshot(t, env, id)
				if rec := doJSON(t, h, &env.Admin, http.MethodPut, termGateAdminTerm(id), `{"name":"Hijacked"}`); rec.Code != http.StatusForbidden {
					t.Errorf("PUT genre term without registry: status = %d, want 403", rec.Code)
				}
				if rec := doJSON(t, h, &env.Admin, http.MethodDelete, termGateAdminTerm(id), ""); rec.Code != http.StatusForbidden {
					t.Errorf("DELETE genre term without registry: status = %d, want 403", rec.Code)
				}
				if after := termSnapshot(t, env, id); after != before {
					t.Errorf("genre term changed\nbefore: %s\nafter:  %s", before, after)
				}
			})

			t.Run("contributor is 403 for managed and unmanaged terms", func(t *testing.T) {
				ids := []int64{cat, unmanaged["nav_menu"], unmanaged["product_cat"]}
				for _, id := range ids {
					before := termSnapshot(t, env, id)
					if rec := doJSON(t, h, &env.Contrib, http.MethodPut, termGateAdminTerm(id), `{"name":"Hijacked"}`); rec.Code != http.StatusForbidden {
						t.Errorf("contrib PUT term %d: status = %d, want 403", id, rec.Code)
					}
					if rec := doJSON(t, h, &env.Contrib, http.MethodDelete, termGateAdminTerm(id), ""); rec.Code != http.StatusForbidden {
						t.Errorf("contrib DELETE term %d: status = %d, want 403", id, rec.Code)
					}
					if after := termSnapshot(t, env, id); after != before {
						t.Errorf("contrib changed term %d\nbefore: %s\nafter:  %s", id, before, after)
					}
				}
			})

			t.Run("admin termIds map skips unmanaged taxonomies", func(t *testing.T) {
				path := fmt.Sprintf("/admin/api/posts/%d", post)
				var detail struct {
					Modified string `json:"modified"`
				}
				rec := doJSON(t, h, &env.Admin, http.MethodGet, path, "")
				if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.Modified == "" {
					t.Fatalf("GET %s: modified missing (%v); body %s", path, err, rec.Body.String())
				}
				put := func(termIDs string) (*struct{ Partial map[string]string }, int) {
					rec := doJSON(t, h, &env.Admin, http.MethodPut, path,
						fmt.Sprintf(`{"title":"Raw post","status":"publish","modified":%q,"termIds":%s}`, detail.Modified, termIDs))
					var out struct{ Partial map[string]string }
					_ = json.Unmarshal(rec.Body.Bytes(), &out)
					if rec.Code == http.StatusOK {
						// Refresh the optimistic-lock stamp for the next call.
						var d struct {
							Modified string `json:"modified"`
						}
						if g := doJSON(t, h, &env.Admin, http.MethodGet, path, ""); json.Unmarshal(g.Body.Bytes(), &d) == nil && d.Modified != "" {
							detail.Modified = d.Modified
						}
					}
					return &out, rec.Code
				}

				// (2.3) nav_menu is reported in partial and not written; category is.
				out, code := put(fmt.Sprintf(`{"nav_menu":[%d],"category":[%d]}`, unmanaged["nav_menu"], cat))
				if code != http.StatusOK {
					t.Fatalf("PUT termIds: status = %d, want 200", code)
				}
				if _, ok := out.Partial["nav_menu"]; !ok || len(out.Partial) != 1 {
					t.Errorf("partial = %v, want only nav_menu", out.Partial)
				}
				if n := termGateRelCount(t, env, post, "nav_menu"); n != 0 {
					t.Errorf("nav_menu relationships = %d, want 0", n)
				}
				if n := termGateRelCount(t, env, post, "category"); n != 1 {
					t.Errorf("category relationships = %d, want 1", n)
				}

				// (2.4) a category key naming an unmanaged-taxonomy term id is
				// reported in partial and writes nothing.
				before := termSnapshot(t, env, unmanaged["nav_menu"])
				out, _ = put(fmt.Sprintf(`{"category":[%d]}`, unmanaged["nav_menu"]))
				if len(out.Partial) == 0 {
					t.Errorf("partial empty for category key naming a nav_menu term, want an entry")
				}
				// The reason must be the service's membership check
				// (content.WithPostTermsReader), not the repository's fallback
				// error: it proves the reader is wired.
				if got, want := out.Partial["category"], content.ErrTermInvalid.Error(); got != want {
					t.Errorf("partial[category] = %q, want %q (term reader not wired?)", got, want)
				}
				if after := termSnapshot(t, env, unmanaged["nav_menu"]); after != before {
					t.Errorf("nav_menu term gained a relationship\nbefore: %s\nafter:  %s", before, after)
				}
				if n := termGateRelCount(t, env, post, "nav_menu"); n != 0 {
					t.Errorf("nav_menu relationships after category key = %d, want 0", n)
				}
			})

			t.Run("menus keep working, generic terms route is 403 for them", func(t *testing.T) {
				createMenu := func(slug string) int64 {
					rec := doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/menus",
						fmt.Sprintf(`{"name":%q,"slug":%q}`, slug, slug))
					if rec.Code != http.StatusCreated {
						t.Fatalf("menu create: status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
					}
					var menu struct {
						ID int64 `json:"id"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &menu); err != nil || menu.ID == 0 {
						t.Fatalf("menu create body %s: %v", rec.Body.String(), err)
					}
					return menu.ID
				}
				// The menu editor path (separate menu, so a RED generic write cannot hide it).
				editor := createMenu("gate-menu-editor")
				menuPath := fmt.Sprintf("/admin/api/menus/%d", editor)
				if rec := doJSON(t, h, &env.Admin, http.MethodPut, menuPath, `{"name":"Gate Menu 2"}`); rec.Code != http.StatusOK {
					t.Errorf("menu update: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
				}
				if rec := doJSON(t, h, &env.Admin, http.MethodDelete, menuPath, ""); rec.Code != http.StatusNoContent {
					t.Errorf("menu delete: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
				}
				// The generic term routes refuse a menu term.
				target := createMenu("gate-menu-generic")
				before := termSnapshot(t, env, target)
				if rec := doJSON(t, h, &env.Admin, http.MethodPut, termGateAdminTerm(target), `{"name":"Hijacked"}`); rec.Code != http.StatusForbidden {
					t.Errorf("generic PUT of menu term: status = %d, want 403", rec.Code)
				}
				if after := termSnapshot(t, env, target); after != before {
					t.Errorf("menu term changed through the generic PUT\nbefore: %s\nafter:  %s", before, after)
				}
				if rec := doJSON(t, h, &env.Admin, http.MethodDelete, termGateAdminTerm(target), ""); rec.Code != http.StatusForbidden {
					t.Errorf("generic DELETE of menu term: status = %d, want 403", rec.Code)
				}
			})

			t.Run("term reads are unchanged", func(t *testing.T) {
				for _, tax := range termGateUnmanaged {
					if rec := doJSON(t, h, &env.Admin, http.MethodGet, "/admin/api/terms?taxonomy="+tax, ""); rec.Code != http.StatusBadRequest {
						t.Errorf("admin list %s: status = %d, want 400", tax, rec.Code)
					}
				}
				if rec := doJSON(t, h, &env.Admin, http.MethodGet, "/admin/api/terms?taxonomy=category", ""); rec.Code != http.StatusOK {
					t.Errorf("admin list category: status = %d, want 200", rec.Code)
				}
			})
		})
	}
}
