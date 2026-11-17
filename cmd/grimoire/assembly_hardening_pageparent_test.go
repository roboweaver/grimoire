package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// M18 task 13.1: assembly tests for page parent writes (Req 11, 12, 14). They
// pass or fail per Phases 11-12: the node reader is already wired, so the guard
// and the URL behavior are reachable through buildHandler today.

// pageParentSurface moves or creates pages over one write surface.
type pageParentSurface struct {
	name string
	// create makes a page and returns its id.
	create func(t *testing.T, env customWriteEnv, title, slug, content, status string) int64
	// move sets the parent of page id as actor and returns the response.
	move func(t *testing.T, env customWriteEnv, actor *customWriteActor, id, parent int64) *httptest.ResponseRecorder
}

// restPageBody decodes the fields of a REST page response the tests read.
type restPageBody struct {
	ID     int64  `json:"id"`
	Parent int64  `json:"parent"`
	Link   string `json:"link"`
}

func pageParentSurfaces() []pageParentSurface {
	return []pageParentSurface{
		{
			name: "rest",
			create: func(t *testing.T, env customWriteEnv, title, slug, content, status string) int64 {
				t.Helper()
				body := fmt.Sprintf(`{"title":%q,"slug":%q,"content":%q,"status":%q}`, title, slug, content, status)
				rec := doJSON(t, env.Handler, &env.Admin, http.MethodPost, "/wp-json/wp/v2/pages", body)
				if rec.Code != http.StatusCreated {
					t.Fatalf("REST create page %s: status = %d; body %s", slug, rec.Code, rec.Body.String())
				}
				var out restPageBody
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == 0 {
					t.Fatalf("REST create page %s: decode %q: %v", slug, rec.Body.String(), err)
				}
				return out.ID
			},
			move: func(t *testing.T, env customWriteEnv, actor *customWriteActor, id, parent int64) *httptest.ResponseRecorder {
				t.Helper()
				return doJSON(t, env.Handler, actor, http.MethodPut, fmt.Sprintf("/wp-json/wp/v2/pages/%d", id),
					fmt.Sprintf(`{"parent":%d}`, parent))
			},
		},
		{
			name: "admin",
			create: func(t *testing.T, env customWriteEnv, title, slug, content, status string) int64 {
				t.Helper()
				body := fmt.Sprintf(`{"title":%q,"slug":%q,"content":%q,"status":%q,"type":"page"}`, title, slug, content, status)
				rec := doJSON(t, env.Handler, &env.Admin, http.MethodPost, "/admin/api/posts", body)
				if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
					t.Fatalf("admin create page %s: status = %d; body %s", slug, rec.Code, rec.Body.String())
				}
				var out struct {
					ID int64 `json:"id"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == 0 {
					t.Fatalf("admin create page %s: decode %q: %v", slug, rec.Body.String(), err)
				}
				return out.ID
			},
			move: func(t *testing.T, env customWriteEnv, actor *customWriteActor, id, parent int64) *httptest.ResponseRecorder {
				t.Helper()
				path := fmt.Sprintf("/admin/api/posts/%d", id)
				// The body omits "type" on purpose (Req 12.2); the stored type governs.
				rec := doJSON(t, env.Handler, &env.Admin, http.MethodGet, path, "")
				var cur map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &cur); err != nil {
					t.Fatalf("admin GET %s: decode %q: %v", path, rec.Body.String(), err)
				}
				body, err := json.Marshal(map[string]any{
					"title":    cur["title"],
					"content":  cur["content"],
					"excerpt":  cur["excerpt"],
					"slug":     cur["slug"],
					"status":   cur["status"],
					"modified": cur["modified"],
					"parent":   parent,
				})
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				return doJSON(t, env.Handler, actor, http.MethodPut, path, string(body))
			},
		},
	}
}

func hardeningGet(t *testing.T, env customWriteEnv, path string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, env.Handler, nil, http.MethodGet, path, "")
}

func hardeningWantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s: status = %d, want %d; body %s", what, rec.Code, want, rec.Body.String())
	}
}

// TestBuildHandlerHardeningPageParent (Req 11.5, 12.2, 12.4, 14.1-14.4): a page
// moved under a parent serves at the nested URL, its old URL redirects (published)
// or 404s (draft), same-slug pages keep their own bodies, a bad parent answers
// 400 and writes nothing, and parent 0 returns the page to the top level.
func TestBuildHandlerHardeningPageParent(t *testing.T) {
	for _, s := range pageParentSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			a := s.create(t, env, "A", "a", "body-a", "publish")
			b := s.create(t, env, "B", "b", "body-b", "publish")

			rec := s.move(t, env, &env.Admin, b, a)
			hardeningWantStatus(t, rec, http.StatusOK, "move b under a")
			if s.name == "rest" {
				var out restPageBody
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
					t.Fatalf("decode %q: %v", rec.Body.String(), err)
				}
				if out.Parent != a {
					t.Errorf("response parent = %d, want %d", out.Parent, a)
				}
				if !strings.HasSuffix(out.Link, "/a/b/") {
					t.Errorf("response link = %q, want suffix /a/b/", out.Link)
				}
			}
			if got := hardeningParent(t, env, b); got != a {
				t.Fatalf("stored post_parent = %d, want %d", got, a)
			}

			rec = hardeningGet(t, env, "/a/b/")
			hardeningWantStatus(t, rec, http.StatusOK, "GET /a/b/")
			if !strings.Contains(rec.Body.String(), "body-b") {
				t.Errorf("GET /a/b/ body lacks page b content")
			}
			rec = hardeningGet(t, env, "/b/")
			hardeningWantStatus(t, rec, http.StatusMovedPermanently, "GET /b/")
			if loc := rec.Header().Get("Location"); !strings.HasSuffix(loc, "/a/b/") {
				t.Errorf("GET /b/ Location = %q, want suffix /a/b/", loc)
			}

			// A draft moved the same way answers 404 on both paths (14.3).
			d := s.create(t, env, "D", "d", "body-d", "draft")
			hardeningWantStatus(t, s.move(t, env, &env.Admin, d, a), http.StatusOK, "move draft under a")
			hardeningWantStatus(t, hardeningGet(t, env, "/d/"), http.StatusNotFound, "GET /d/")
			hardeningWantStatus(t, hardeningGet(t, env, "/a/d/"), http.StatusNotFound, "GET /a/d/")

			// Same slug under different parents (14.4).
			c := s.create(t, env, "C", "c", "body-c", "publish")
			x1 := s.create(t, env, "X one", "x", "body-x-one", "publish")
			hardeningWantStatus(t, s.move(t, env, &env.Admin, x1, a), http.StatusOK, "move x1 under a")
			x2 := s.create(t, env, "X two", "x", "body-x-two", "publish")
			hardeningWantStatus(t, s.move(t, env, &env.Admin, x2, c), http.StatusOK, "move x2 under c")
			for path, want := range map[string]string{"/a/x/": "body-x-one", "/c/x/": "body-x-two"} {
				rec = hardeningGet(t, env, path)
				hardeningWantStatus(t, rec, http.StatusOK, "GET "+path)
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("GET %s body lacks %q", path, want)
				}
			}

			// Rejections: 400 and the row is unchanged (11.5, 12.4).
			post := seedRawPost(t, env, "post", "publish", 0)
			for _, c := range []struct {
				what   string
				id     int64
				parent int64
			}{
				{"cycle (a under its descendant b)", a, b},
				{"self parent", b, b},
				{"post as parent", b, post},
				{"missing parent", b, 999999},
			} {
				before := rowSnapshot(t, env, c.id)
				hardeningWantStatus(t, s.move(t, env, &env.Admin, c.id, c.parent), http.StatusBadRequest, c.what)
				if after := rowSnapshot(t, env, c.id); after != before {
					t.Errorf("%s changed the row\nbefore: %s\nafter:  %s", c.what, before, after)
				}
			}

			// A contributor may not move a page.
			before := rowSnapshot(t, env, b)
			hardeningWantStatus(t, s.move(t, env, &env.Contrib, b, 0), http.StatusForbidden, "contributor move")
			if after := rowSnapshot(t, env, b); after != before {
				t.Errorf("contributor move changed the row")
			}

			// parent 0 returns the page to the top level.
			hardeningWantStatus(t, s.move(t, env, &env.Admin, b, 0), http.StatusOK, "move b to top level")
			if got := hardeningParent(t, env, b); got != 0 {
				t.Errorf("stored post_parent = %d, want 0", got)
			}
			hardeningWantStatus(t, hardeningGet(t, env, "/b/"), http.StatusOK, "GET /b/ after top-level move")
		})
	}
}
