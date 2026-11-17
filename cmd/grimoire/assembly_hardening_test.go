package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// M18 task 6.1: RED assembly tests for PR 1. Every assertion is reachable only
// through buildHandler / buildScheduler as main wires them. The revision and
// autosave subtests fail until task 6.2 passes the registry to those two
// services; the term and scheduler tests fail until Phases 2-5 land.

// hardeningCountPosts returns the number of rows in the posts table.
func hardeningCountPosts(t *testing.T, env customWriteEnv) int {
	t.Helper()
	var n int
	if err := env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM "+env.Prefix+"posts").Scan(&n); err != nil {
		t.Fatalf("count posts: %v", err)
	}
	return n
}

// hardeningCountRelationships returns the term_relationships rows of object id.
func hardeningCountRelationships(t *testing.T, env customWriteEnv, id int64) int {
	t.Helper()
	var n int
	if err := env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE object_id = ?", id).Scan(&n); err != nil {
		t.Fatalf("count relationships: %v", err)
	}
	return n
}

// hardeningSeedTerm inserts a term and its taxonomy row (term_taxonomy_id =
// term_id) and returns the ID.
func hardeningSeedTerm(t *testing.T, env customWriteEnv, name, slug, taxonomy string) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := env.Repos.DB().ExecContext(ctx,
		"INSERT INTO "+env.Prefix+"terms (name, slug) VALUES (?, ?)", name, slug)
	if err != nil {
		t.Fatalf("seed term %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed term id: %v", err)
	}
	if _, err := env.Repos.DB().ExecContext(ctx,
		"INSERT INTO "+env.Prefix+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
		id, id, taxonomy, "", 0, 0); err != nil {
		t.Fatalf("seed term taxonomy %q: %v", name, err)
	}
	return id
}

// hardeningStoredTermName reads the stored name of term id straight from SQL.
func hardeningStoredTermName(t *testing.T, env customWriteEnv, id int64) string {
	t.Helper()
	var name string
	if err := env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT name FROM "+env.Prefix+"terms WHERE term_id = ?", id).Scan(&name); err != nil {
		t.Fatalf("stored term name %d: %v", id, err)
	}
	return name
}

// TestBuildHandlerUnwritableTypes (Req 1, 2, 17.1): rows of an unmanaged type
// cannot be edited, deleted, revised or autosaved through the admin API, with
// and without a registry; post/page and capability_type rows keep working.
func TestBuildHandlerUnwritableTypes(t *testing.T) {
	for _, withRegistry := range []bool{true, false} {
		name := "with registry"
		if !withRegistry {
			name = "without registry"
		}
		t.Run(name, func(t *testing.T) {
			env := newCustomWriteHandler(t, withRegistry)
			h := env.Handler
			ctx := context.Background()
			cat := hardeningSeedTerm(t, env, "Cat", "cat", "category")

			for _, typ := range []string{"shop_order", "attachment", "nav_menu_item"} {
				id := seedRawPost(t, env, typ, "publish", 0)
				seedRawTermRelationship(t, env, id, cat)
				rev, err := env.Repos.Revisions.CreateRevision(ctx, id, 1,
					domain.Post{Title: "old", Content: "old", Status: "inherit", Type: "revision"}, false)
				if err != nil {
					t.Fatalf("seed revision %s: %v", typ, err)
				}
				if _, err := env.Repos.Revisions.CreateRevision(ctx, id, 1,
					domain.Post{Title: "auto", Content: "auto", Status: "inherit", Type: "revision"}, true); err != nil {
					t.Fatalf("seed autosave %s: %v", typ, err)
				}
				path := fmt.Sprintf("/admin/api/posts/%d", id)

				t.Run(typ+" write", func(t *testing.T) {
					before := rowSnapshot(t, env, id)
					rels := hardeningCountRelationships(t, env, id)
					// Send the stored modified stamp so the only reason to refuse is the type.
					rec := doJSON(t, h, &env.Admin, http.MethodGet, path, "")
					var detail struct {
						Modified string `json:"modified"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.Modified == "" {
						t.Fatalf("GET %s: modified missing (%v); body %s", path, err, rec.Body.String())
					}
					rec = doJSON(t, h, &env.Admin, http.MethodPut, path,
						fmt.Sprintf(`{"title":"Hijacked","status":"publish","modified":%q,"termIds":{"category":[%d]}}`, detail.Modified, cat))
					if rec.Code != http.StatusForbidden {
						t.Fatalf("PUT %s: status = %d, want 403; body %s", path, rec.Code, rec.Body.String())
					}
					rec = doJSON(t, h, &env.Admin, http.MethodPut, path, fmt.Sprintf(`{"title":"Hijacked","status":"publish","modified":%q,"termIds":{"category":[]}}`, detail.Modified))
					if rec.Code != http.StatusForbidden {
						t.Fatalf("PUT %s (empty termIds): status = %d, want 403; body %s", path, rec.Code, rec.Body.String())
					}
					if got := hardeningCountRelationships(t, env, id); got != rels {
						t.Fatalf("PUT changed term_relationships: %d -> %d", rels, got)
					}
					if after := rowSnapshot(t, env, id); after != before {
						t.Fatalf("PUT changed row\nbefore: %s\nafter:  %s", before, after)
					}
					rec = doJSON(t, h, &env.Admin, http.MethodDelete, path, "")
					if rec.Code != http.StatusForbidden {
						t.Fatalf("DELETE %s: status = %d, want 403; body %s", path, rec.Code, rec.Body.String())
					}
					if after := rowSnapshot(t, env, id); after != before {
						t.Fatalf("DELETE changed or removed row\nbefore: %s\nafter:  %s", before, after)
					}
				})

				t.Run(typ+" revisions and autosave", func(t *testing.T) {
					rows := hardeningCountPosts(t, env)
					for _, c := range []struct{ method, p, body string }{
						{http.MethodGet, path + "/revisions", ""},
						{http.MethodGet, fmt.Sprintf("%s/revisions/%d", path, rev), ""},
						{http.MethodPost, fmt.Sprintf("%s/revisions/%d/restore", path, rev), ""},
						{http.MethodGet, path + "/autosave", ""},
						{http.MethodPost, path + "/autosave", `{"title":"t","content":"c","excerpt":""}`},
					} {
						rec := doJSON(t, h, &env.Admin, c.method, c.p, c.body)
						if rec.Code != http.StatusNotFound {
							t.Fatalf("%s %s: status = %d, want 404; body %s", c.method, c.p, rec.Code, rec.Body.String())
						}
					}
					if got := hardeningCountPosts(t, env); got != rows {
						t.Fatalf("posts rows %d -> %d, want no row written", rows, got)
					}
					if got := rowSnapshot(t, env, id); strings.Contains(got, "old") {
						t.Fatalf("restore overwrote parent: %s", got)
					}
				})
			}

			// Creating an unmanaged type stays a 400 (existing behaviour).
			rec := doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/posts", `{"title":"Order","type":"shop_order","status":"publish"}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST type shop_order: status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}

			// post and page rows behave as before.
			for _, typ := range []string{"post", "page"} {
				id := seedRawPost(t, env, typ, "publish", 0)
				if _, err := env.Repos.Revisions.CreateRevision(ctx, id, 1,
					domain.Post{Title: "old", Content: "old", Status: "inherit", Type: "revision"}, false); err != nil {
					t.Fatalf("seed revision %s: %v", typ, err)
				}
				path := fmt.Sprintf("/admin/api/posts/%d", id)
				rec := doJSON(t, h, &env.Admin, http.MethodGet, path+"/revisions", "")
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s/revisions: status = %d, want 200; body %s", path, rec.Code, rec.Body.String())
				}
				rec = doJSON(t, h, &env.Admin, http.MethodPost, path+"/autosave", `{"title":"t","content":"c","excerpt":""}`)
				if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
					t.Fatalf("POST %s/autosave: status = %d, want 2xx; body %s", path, rec.Code, rec.Body.String())
				}
			}

			if !withRegistry {
				return
			}

			// A capability_type: product row is revisable only by a caller
			// holding the product capabilities.
			rec = doJSON(t, h, &env.Products, http.MethodPost, "/wp-json/wp/v2/product", `{"title":"Widget","status":"publish"}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("create product: status = %d; body %s", rec.Code, rec.Body.String())
			}
			var created struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == 0 {
				t.Fatalf("decode product: %v; body %s", err, rec.Body.String())
			}
			rev, err := env.Repos.Revisions.CreateRevision(ctx, created.ID, 1,
				domain.Post{Title: "old", Content: "old", Status: "inherit", Type: "revision"}, false)
			if err != nil {
				t.Fatalf("seed product revision: %v", err)
			}
			path := fmt.Sprintf("/admin/api/posts/%d", created.ID)
			for _, p := range []string{path + "/revisions", fmt.Sprintf("%s/revisions/%d", path, rev)} {
				if rec := doJSON(t, h, &env.Contrib, http.MethodGet, p, ""); rec.Code != http.StatusNotFound {
					t.Fatalf("contrib GET %s: status = %d, want 404; body %s", p, rec.Code, rec.Body.String())
				}
				if rec := doJSON(t, h, &env.Products, http.MethodGet, p, ""); rec.Code != http.StatusOK {
					t.Fatalf("products GET %s: status = %d, want 200; body %s", p, rec.Code, rec.Body.String())
				}
			}
			if rec := doJSON(t, h, &env.Contrib, http.MethodPost, path+"/autosave", `{"title":"t","content":"c","excerpt":""}`); rec.Code != http.StatusNotFound {
				t.Fatalf("contrib POST autosave: status = %d, want 404; body %s", rec.Code, rec.Body.String())
			}
			if rec := doJSON(t, h, &env.Products, http.MethodPost, path+"/autosave", `{"title":"t","content":"c","excerpt":""}`); rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
				t.Fatalf("products POST autosave: status = %d, want 2xx; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// lockedBuffer is a goroutine-safe log sink.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestBuildHandlerSchedulerSkipsUnwritable (Req 4.1, 4.2): a scheduler built
// like main publishes a due future post, leaves a due future shop_order alone
// and logs no error.
func TestBuildHandlerSchedulerSkipsUnwritable(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	deps := customWriteDeps(t, env, customWriteRegistry(t))
	logs := &lockedBuffer{}
	deps.Log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_, postWrite, err := buildHandler(deps)
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	ctx := context.Background()
	order := seedRawPost(t, env, "shop_order", "future", 0)
	past := time.Now().Add(-time.Hour)
	post, err := env.Repos.PostWriter.Create(ctx, domain.Post{
		Author: 1, Title: "Scheduled post", Status: "future", Type: "post",
		Slug: "scheduled-post", Date: past, DateGMT: past,
	})
	if err != nil {
		t.Fatalf("create future post: %v", err)
	}
	before := rowSnapshot(t, env, order)

	sched := buildScheduler(deps, postWrite)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { sched.Run(runCtx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	published := false
	for time.Now().Before(deadline) {
		got, err := env.Repos.PostWriter.ByID(ctx, post)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.Status == "publish" {
			published = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	if !published {
		t.Fatalf("future post was not published")
	}
	if after := rowSnapshot(t, env, order); after != before {
		t.Fatalf("scheduler touched the shop_order row\nbefore: %s\nafter:  %s", before, after)
	}
	if out := logs.String(); strings.Contains(out, "level=ERROR") {
		t.Fatalf("scheduler logged an error:\n%s", out)
	}
}

// TestBuildHandlerBuiltinTermNames (Req 5, 6.1, 6.2): built-in category and
// post_tag names are reduced to plain text on create, a name the caller did not
// change is stored as is, and the contrib caller is refused before any 400.
func TestBuildHandlerBuiltinTermNames(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	h := env.Handler

	type termResp struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Code string `json:"code"`
	}
	decode := func(body string) termResp {
		t.Helper()
		var out termResp
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("decode %q: %v", body, err)
		}
		return out
	}

	// Create: markup reduced, empty-after-reduction rejected, both surfaces.
	for _, c := range []struct{ rest, tax string }{
		{"/wp-json/wp/v2/categories", "category"},
		{"/wp-json/wp/v2/tags", "post_tag"},
	} {
		rec := doJSON(t, h, &env.Admin, http.MethodPost, c.rest, `{"name":"<b>Bold</b> & co","slug":"rest-`+c.tax+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("REST create %s: status = %d, want 201; body %s", c.tax, rec.Code, rec.Body.String())
		}
		if got := hardeningStoredTermName(t, env, decode(rec.Body.String()).ID); got != "Bold & co" {
			t.Fatalf("REST create %s: stored name = %q, want %q", c.tax, got, "Bold & co")
		}
		rec = doJSON(t, h, &env.Admin, http.MethodPost, c.rest, `{"name":"<em></em>","slug":"rest-empty-`+c.tax+`"}`)
		if rec.Code != http.StatusBadRequest || decode(rec.Body.String()).Code != "rest_invalid_param" {
			t.Fatalf("REST empty %s: status = %d code = %q, want 400 rest_invalid_param; body %s",
				c.tax, rec.Code, decode(rec.Body.String()).Code, rec.Body.String())
		}
		rec = doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
			fmt.Sprintf(`{"name":"<b>Bold</b> & co","slug":"admin-%s","taxonomy":%q}`, c.tax, c.tax))
		if rec.Code != http.StatusCreated {
			t.Fatalf("admin create %s: status = %d, want 201; body %s", c.tax, rec.Code, rec.Body.String())
		}
		if got := hardeningStoredTermName(t, env, decode(rec.Body.String()).ID); got != "Bold & co" {
			t.Fatalf("admin create %s: stored name = %q, want %q", c.tax, got, "Bold & co")
		}
		rec = doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
			fmt.Sprintf(`{"name":"<em></em>","slug":"admin-empty-%s","taxonomy":%q}`, c.tax, c.tax))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("admin empty %s: status = %d, want 400; body %s", c.tax, rec.Code, rec.Body.String())
		}

		// A caller without manage_categories gets 403, never the 400.
		for _, p := range []struct{ path, body string }{
			{c.rest, `{"name":"<em></em>","slug":"c-rest"}`},
			{"/admin/api/terms", fmt.Sprintf(`{"name":"<em></em>","slug":"c-admin","taxonomy":%q}`, c.tax)},
		} {
			rec = doJSON(t, h, &env.Contrib, http.MethodPost, p.path, p.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("contrib POST %s: status = %d, want 403; body %s", p.path, rec.Code, rec.Body.String())
			}
		}
	}

	// An unchanged stored name survives slug-only and parent-only updates.
	const stored = "News &amp; Events"
	news := hardeningSeedTerm(t, env, stored, "news-events", "category")
	parent := hardeningSeedTerm(t, env, "Parent", "parent-cat", "category")

	rec := doJSON(t, h, &env.Admin, http.MethodPatch, fmt.Sprintf("/wp-json/wp/v2/categories/%d", news), `{"slug":"news-and-events"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("REST slug-only PATCH: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := hardeningStoredTermName(t, env, news); got != stored {
		t.Fatalf("after REST slug-only PATCH name = %q, want %q", got, stored)
	}
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("/admin/api/terms/%d", news), fmt.Sprintf(`{"parent":%d}`, parent))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin parent-only PUT: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := hardeningStoredTermName(t, env, news); got != stored {
		t.Fatalf("after admin parent-only PUT name = %q, want %q", got, stored)
	}

	// Reads return the stored name verbatim.
	rec = doJSON(t, h, nil, http.MethodGet, fmt.Sprintf("/wp-json/wp/v2/categories/%d", news), "")
	if rec.Code != http.StatusOK || decode(rec.Body.String()).Name != stored {
		t.Fatalf("REST GET category: status = %d name = %q, want 200 %q; body %s",
			rec.Code, decode(rec.Body.String()).Name, stored, rec.Body.String())
	}
	rec = doJSON(t, h, &env.Admin, http.MethodGet, "/admin/api/terms?taxonomy=category", "")
	var list struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode admin terms: %v; body %s", err, rec.Body.String())
	}
	found := false
	for _, it := range list.Items {
		if it.ID == news {
			found = true
			if it.Name != stored {
				t.Fatalf("admin terms list name = %q, want %q", it.Name, stored)
			}
		}
	}
	if !found {
		t.Fatalf("admin terms list does not contain term %d: %s", news, rec.Body.String())
	}
	rec = doJSON(t, h, nil, http.MethodGet, "/category/parent-cat/news-and-events/", "")
	// The theme HTML-escapes the stored value, so the stored text appears escaped once.
	if want := html.EscapeString(stored); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("public archive: status = %d, want 200 with %q in body", rec.Code, want)
	}
}
