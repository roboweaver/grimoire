package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Characterization pins for M18 task 1.1. Every assertion here passes on the
// unmodified tree; the hardening phases must keep them green.

// TestBuildHandlerRESTByIDTypePins pins that the REST by-id write routes answer
// 404 rest_post_invalid_id for a row of another type, whatever the caller's
// privileges (Req 3.1-3.4), and leave the row untouched.
func TestBuildHandlerRESTByIDTypePins(t *testing.T) {
	for _, withRegistry := range []bool{true, false} {
		name := "with registry"
		if !withRegistry {
			name = "without registry"
		}
		t.Run(name, func(t *testing.T) {
			env := newCustomWriteHandler(t, withRegistry)
			h := env.Handler

			type routeCase struct {
				label string
				route string // collection route, e.g. /wp-json/wp/v2/posts
				row   int64
			}
			var cases []routeCase
			for _, typ := range []string{"shop_order", "attachment", "nav_menu_item"} {
				id := seedRawPost(t, env, typ, "publish", 0)
				cases = append(cases,
					routeCase{typ + " via posts", "/wp-json/wp/v2/posts", id},
					routeCase{typ + " via pages", "/wp-json/wp/v2/pages", id})
			}
			postRow := seedRawPost(t, env, "post", "publish", 0)
			pageRow := seedRawPost(t, env, "page", "publish", 0)
			cases = append(cases,
				routeCase{"post via pages", "/wp-json/wp/v2/pages", postRow},
				routeCase{"page via posts", "/wp-json/wp/v2/posts", pageRow})
			if withRegistry {
				orderRow := seedRawPost(t, env, "shop_order", "publish", 0)
				cases = append(cases, routeCase{"shop_order via product", "/wp-json/wp/v2/product", orderRow})
			}

			for _, c := range cases {
				for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
					for _, actor := range []*customWriteActor{&env.Admin, &env.Contrib} {
						body := ""
						if m != http.MethodDelete {
							body = `{"title":"Hijacked"}`
						}
						before := rowSnapshot(t, env, c.row)
						path := fmt.Sprintf("%s/%d", c.route, c.row)
						rec := doJSON(t, h, actor, m, path, body)
						label := fmt.Sprintf("%s %s %s as %s", c.label, m, path, actor.Login)
						if rec.Code != http.StatusNotFound {
							t.Fatalf("%s: status = %d, want 404 (body %s)", label, rec.Code, rec.Body.String())
						}
						var e struct {
							Code string `json:"code"`
						}
						if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Code != "rest_post_invalid_id" {
							t.Fatalf("%s: code = %q (err %v), want rest_post_invalid_id; body %s", label, e.Code, err, rec.Body.String())
						}
						if after := rowSnapshot(t, env, c.row); after != before {
							t.Fatalf("%s: row changed\nbefore: %s\nafter:  %s", label, before, after)
						}
					}
				}
			}
		})
	}
}

// TestBuildHandlerUnwritableReadsPin pins the read side for rows of unmanaged
// types (Req 1.6, 3.5): the admin detail answers 200 with the stored type, and
// REST reads of the built-in routes do not return those rows by id.
//
// Characterization notes (recorded, not asserted): today PUT and DELETE on
// /admin/api/posts/{id} SUCCEED for these rows. Phase 6 turns that RED.
func TestBuildHandlerUnwritableReadsPin(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	h := env.Handler

	rows := map[string]int64{}
	for _, typ := range []string{"shop_order", "attachment", "nav_menu_item"} {
		rows[typ] = seedRawPost(t, env, typ, "publish", 0)
	}

	for typ, id := range rows {
		rec := doJSON(t, h, &env.Admin, http.MethodGet, fmt.Sprintf("/admin/api/posts/%d", id), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /admin/api/posts/%d (%s): status = %d, want 200 (body %s)", id, typ, rec.Code, rec.Body.String())
		}
		var d struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatalf("decode detail %s: %v", typ, err)
		}
		if d.ID != id || d.Type != typ {
			t.Fatalf("detail %s = {id %d type %q}, want {id %d type %q}", typ, d.ID, d.Type, id, typ)
		}

		// REST reads of the built-in routes keep answering as they do today.
		for _, route := range []string{"/wp-json/wp/v2/posts", "/wp-json/wp/v2/pages"} {
			rec := doJSON(t, h, &env.Admin, http.MethodGet, fmt.Sprintf("%s/%d", route, id), "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s/%d (%s): status = %d, want 404 (body %s)", route, id, typ, rec.Code, rec.Body.String())
			}
		}
	}

	// The unfiltered admin list is a read and keeps answering 200.
	rec := doJSON(t, h, &env.Admin, http.MethodGet, "/admin/api/posts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/posts: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	listed := map[int64]string{}
	for _, it := range list.Items {
		listed[it.ID] = it.Type
	}
	for typ, id := range rows {
		if got, ok := listed[id]; ok && got != typ {
			t.Fatalf("list row %d type = %q, want %q", id, got, typ)
		}
		t.Logf("characterization: unfiltered admin list includes %s row %d: %v", typ, id, listed[id] != "")
	}
}
