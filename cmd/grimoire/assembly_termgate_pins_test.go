package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Characterization pins for M19 task 1.1. Every assertion here passes on the
// unmodified tree; the term-gate phases must keep them green.
//
// Recorded, not asserted (they succeed today and Phase 2 turns them RED):
//   - PUT /admin/api/terms/{id} of a nav_menu, product_cat or link_category
//     term renames it (200).
//   - DELETE /admin/api/terms/{id} of such a term deletes it (204).
//   - The admin post `termIds` map naming nav_menu is passed to SetPostTerms
//     and writes a relationship.

// termGateUnmanaged lists the raw-seeded unmanaged taxonomies.
var termGateUnmanaged = []string{"nav_menu", "product_cat", "link_category", "post_format"}

func termGateErrCode(t *testing.T, body []byte) string {
	t.Helper()
	// REST errors carry a top-level "code"; admin API errors nest it under
	// "error" ({"error":{"code":...,"message":...}}).
	var e struct {
		Code  string          `json:"code"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	if e.Code == "" && len(e.Error) > 0 {
		var nested struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(e.Error, &nested) == nil {
			return nested.Code
		}
	}
	return e.Code
}

func TestBuildHandlerTermGatePins(t *testing.T) {
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
			managed := hardeningSeedTerm(t, env, "Managed Cat", "managed-cat", "category")
			post := seedRawPost(t, env, "post", "publish", 0)
			seedRawTermRelationship(t, env, post, unmanaged["nav_menu"])

			// (a) REST by-id writes of a term of another taxonomy: 404, row unchanged.
			type restCase struct {
				label string
				route string
				id    int64
			}
			var cases []restCase
			for _, tax := range termGateUnmanaged {
				cases = append(cases,
					restCase{tax + " via categories", "/wp-json/wp/v2/categories", unmanaged[tax]},
					restCase{tax + " via tags", "/wp-json/wp/v2/tags", unmanaged[tax]})
			}
			cases = append(cases, restCase{"category via tags", "/wp-json/wp/v2/tags", managed})
			if withRegistry {
				for _, tax := range termGateUnmanaged {
					cases = append(cases, restCase{tax + " via genre", "/wp-json/wp/v2/genre", unmanaged[tax]})
				}
				cases = append(cases, restCase{"category via badge", "/wp-json/wp/v2/badge", managed})
			}
			for _, c := range cases {
				for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
					for _, actor := range []*customWriteActor{&env.Admin, &env.Contrib} {
						body := ""
						if m != http.MethodDelete {
							body = `{"name":"Hijacked"}`
						}
						before := termSnapshot(t, env, c.id)
						path := fmt.Sprintf("%s/%d", c.route, c.id)
						rec := doJSON(t, h, actor, m, path, body)
						label := fmt.Sprintf("%s %s %s as %s", c.label, m, path, actor.Login)
						if rec.Code != http.StatusNotFound {
							t.Fatalf("%s: status = %d, want 404 (body %s)", label, rec.Code, rec.Body.String())
						}
						if code := termGateErrCode(t, rec.Body.Bytes()); code != "rest_term_invalid_id" {
							t.Fatalf("%s: code = %q, want rest_term_invalid_id", label, code)
						}
						if after := termSnapshot(t, env, c.id); after != before {
							t.Fatalf("%s: term changed\nbefore: %s\nafter:  %s", label, before, after)
						}
					}
				}
			}

			// (b) Name-addressed create of an unmanaged taxonomy: 400, no row.
			countTax := func(tax string) int {
				return hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_taxonomy WHERE taxonomy = ?", tax)
			}
			for _, tax := range termGateUnmanaged {
				before := countTax(tax)
				rec := doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
					fmt.Sprintf(`{"name":"New","slug":"new-%s","taxonomy":%q}`, tax, tax))
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("admin create %s: status = %d, want 400 (body %s)", tax, rec.Code, rec.Body.String())
				}
				// The REST route fixes the taxonomy to the route's; a body
				// "taxonomy" key is ignored and never creates an unmanaged row.
				doJSON(t, h, &env.Admin, http.MethodPost, "/wp-json/wp/v2/categories",
					fmt.Sprintf(`{"name":"New REST %s","slug":"new-rest-%s","taxonomy":%q}`, tax, tax, tax))
				if got := countTax(tax); got != before {
					t.Fatalf("create naming %s changed its term_taxonomy rows: %d -> %d", tax, before, got)
				}
			}

			// (c) Reads of an unmanaged term are unchanged: the admin list of an
			// unknown taxonomy answers 400, and public archives and REST by-id
			// reads of the unmanaged term are not served as terms.
			for _, tax := range termGateUnmanaged {
				rec := doJSON(t, h, &env.Admin, http.MethodGet, "/admin/api/terms?taxonomy="+tax, "")
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("admin list %s: status = %d, want 400", tax, rec.Code)
				}
				for _, route := range []string{"categories", "tags"} {
					path := fmt.Sprintf("/wp-json/wp/v2/%s/%d", route, unmanaged[tax])
					if rec := doJSON(t, h, nil, http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
						t.Fatalf("GET %s: status = %d, want 404", path, rec.Code)
					}
				}
			}
			if rec := doJSON(t, h, &env.Admin, http.MethodGet, "/admin/api/terms?taxonomy=category", ""); rec.Code != http.StatusOK {
				t.Fatalf("admin list category: status = %d, want 200", rec.Code)
			}
			if rec := doJSON(t, h, nil, http.MethodGet, "/category/managed-cat/", ""); rec.Code != http.StatusOK {
				t.Fatalf("public archive of managed category: status = %d, want 200", rec.Code)
			}

			// (d) Menus created, edited and deleted through the menu editor keep working.
			rec := doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/menus", `{"name":"Pin Menu","slug":"pin-menu"}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("menu create: status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
			}
			var menu struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &menu); err != nil || menu.ID == 0 {
				t.Fatalf("menu create body %s: id %d, err %v", rec.Body.String(), menu.ID, err)
			}
			menuPath := fmt.Sprintf("/admin/api/menus/%d", menu.ID)
			if rec := doJSON(t, h, &env.Admin, http.MethodPut, menuPath, `{"name":"Pin Menu 2"}`); rec.Code != http.StatusOK {
				t.Fatalf("menu update: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			if rec := doJSON(t, h, &env.Admin, http.MethodDelete, menuPath, ""); rec.Code != http.StatusNoContent {
				t.Fatalf("menu delete: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}

			// (e) M13 parent change of a managed category to an unmanaged term's id: 400.
			for _, tax := range termGateUnmanaged {
				before := termSnapshot(t, env, managed)
				rec := doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("/admin/api/terms/%d", managed),
					fmt.Sprintf(`{"parent":%d}`, unmanaged[tax]))
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("re-parent under %s: status = %d, want 400 (body %s)", tax, rec.Code, rec.Body.String())
				}
				if after := termSnapshot(t, env, managed); after != before {
					t.Fatalf("re-parent under %s changed the term\nbefore: %s\nafter:  %s", tax, before, after)
				}
			}
		})
	}
}
