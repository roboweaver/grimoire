package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage"
)

// M22 task 9.1: assembly tests for the admin trash routes, through the real
// buildHandler. Each fails if the admin route registration or the trash wiring
// of assembly.go (WithPostTrasher, WithTrashDays, Server.WithPostTrash) is
// removed: the admin DELETE then answers 409 trash_unsupported (or deletes
// permanently), and the restore and empty-trash routes answer 404.

const adminPostsBase = "/admin/api/posts"

// adminErrorCode returns error.code of an admin API error body.
func adminErrorCode(t *testing.T, rec *httptest.ResponseRecorder) any {
	t.Helper()
	e, _ := trashDecode(t, rec)["error"].(map[string]any)
	return e["code"]
}

func adminPostPath(id int64) string { return fmt.Sprintf("%s/%d", adminPostsBase, id) }

// adminDelete sends DELETE /admin/api/posts/{id}[?force=true] as actor.
func adminDelete(t *testing.T, env customWriteEnv, actor *customWriteActor, id int64, force bool) *httptest.ResponseRecorder {
	t.Helper()
	path := adminPostPath(id)
	if force {
		path += "?force=true"
	}
	return doJSON(t, env.Handler, actor, http.MethodDelete, path, "")
}

// adminRestore sends POST /admin/api/posts/{id}/restore with body as actor.
func adminRestore(t *testing.T, env customWriteEnv, actor *customWriteActor, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, env.Handler, actor, http.MethodPost, adminPostPath(id)+"/restore", body)
}

// adminEmptyTrash sends POST /admin/api/posts/empty-trash for typ as actor.
func adminEmptyTrash(t *testing.T, env customWriteEnv, actor *customWriteActor, typ string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, env.Handler, actor, http.MethodPost, adminPostsBase+"/empty-trash", fmt.Sprintf(`{"type":%q}`, typ))
}

// adminListIDs returns the ids of GET /admin/api/posts?type=typ[&status=status].
func adminListIDs(t *testing.T, env customWriteEnv, actor *customWriteActor, typ, status string) map[int64]bool {
	t.Helper()
	path := fmt.Sprintf("%s?type=%s&perPage=100", adminPostsBase, typ)
	if status != "" {
		path += "&status=" + status
	}
	rec := doJSON(t, env.Handler, actor, http.MethodGet, path, "")
	trashWant(t, rec, http.StatusOK, "admin list "+path)
	var out struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	decodeInto(t, rec, &out)
	ids := map[int64]bool{}
	for _, it := range out.Items {
		ids[it.ID] = true
	}
	return ids
}

func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

// assertRestored pins the restored state of fixture f: slug back, comments
// back, own meta kept and every trash meta row gone.
func assertRestored(t *testing.T, env customWriteEnv, f trashFixture, wantStatus string) {
	t.Helper()
	status, name := trashStatusName(t, env, f.id)
	if status != wantStatus || name != "subject" {
		t.Errorf("restored row = (%s, %s), want (%s, subject)", status, name, wantStatus)
	}
	if got := trashComment(t, env, f.comment1); got != "1" {
		t.Errorf("approved comment = %q, want 1", got)
	}
	if got := trashComment(t, env, f.comment0); got != "0" {
		t.Errorf("pending comment = %q, want 0", got)
	}
	if got := trashMeta(t, env, f.id, "keep_key"); got != "keep" {
		t.Errorf("keep_key = %q, want keep", got)
	}
	if got := trashMetaRows(t, env, f.id); got != 0 {
		t.Errorf("trash meta rows left = %d, want 0", got)
	}
}

// TestBuildHandlerTrashAdminDelete (Req 15.1-15.3): the admin DELETE trashes by
// default, the lists follow, and ?force=true removes live and trashed rows.
func TestBuildHandlerTrashAdminDelete(t *testing.T) {
	for _, c := range trashCases() {
		t.Run(c.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			actor := c.actor(env)
			f := seedTrashFixture(t, env, c)
			revisions := trashRevisions(t, env, f.id)

			rec := adminDelete(t, env, actor, f.id, false)
			trashWant(t, rec, http.StatusNoContent, "admin DELETE")
			assertTrashed(t, env, f, c, revisions)

			if adminListIDs(t, env, actor, c.typ, "")[f.id] {
				t.Errorf("unfiltered admin list still holds trashed row %d", f.id)
			}
			if !adminListIDs(t, env, actor, c.typ, "trash")[f.id] {
				t.Errorf("status=trash admin list lacks row %d", f.id)
			}
			detail := doJSON(t, env.Handler, actor, http.MethodGet, adminPostPath(f.id), "")
			trashWant(t, detail, http.StatusOK, "admin GET of a trashed row")
			if got := trashDecode(t, detail)["status"]; got != "trash" {
				t.Errorf("detail status = %v, want trash", got)
			}

			before := rowSnapshot(t, env, f.id)
			rec = adminDelete(t, env, actor, f.id, false)
			trashWant(t, rec, http.StatusConflict, "second admin DELETE")
			if code := adminErrorCode(t, rec); code != "already_trashed" {
				t.Errorf("code = %v, want already_trashed", code)
			}
			if after := rowSnapshot(t, env, f.id); after != before {
				t.Errorf("a refused trash changed the row\nbefore: %s\nafter:  %s", before, after)
			}

			// Force removes the trashed row through the M18-M20 sequence.
			trashWant(t, adminDelete(t, env, actor, f.id, true), http.StatusNoContent, "force delete of a trashed row")
			assertPurged(t, env, f, c)
		})
	}
}

// TestBuildHandlerTrashAdminForceLive (Req 15.2): ?force=true on a live row
// runs the permanent-delete sequence.
func TestBuildHandlerTrashAdminForceLive(t *testing.T) {
	for _, c := range trashCases() {
		t.Run(c.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			f := seedTrashFixture(t, env, c)
			trashWant(t, adminDelete(t, env, c.actor(env), f.id, true), http.StatusNoContent, "force delete of a live row")
			assertPurged(t, env, f, c)
		})
	}
}

// TestBuildHandlerTrashAdminRestore (Req 16): the restore route returns the row
// to draft, or to its previous status, with slug, comments and meta restored.
func TestBuildHandlerTrashAdminRestore(t *testing.T) {
	for _, c := range trashCases() {
		for _, tc := range []struct{ name, body, want string }{
			{"empty body restores to draft", "", "draft"},
			{"empty status restores to draft", `{"status":""}`, "draft"},
			{"previous restores to the recorded status", `{"status":"previous"}`, "publish"},
			{"explicit status", `{"status":"publish"}`, "publish"},
		} {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				env := newCustomWriteHandler(t, true)
				actor := c.actor(env)
				f := seedTrashFixture(t, env, c)
				trashWant(t, adminDelete(t, env, actor, f.id, false), http.StatusNoContent, "admin DELETE")

				rec := adminRestore(t, env, actor, f.id, tc.body)
				trashWant(t, rec, http.StatusOK, "restore")
				body := trashDecode(t, rec)
				if body["status"] != tc.want || body["slug"] != "subject" {
					t.Errorf("response = (%v, %v), want (%s, subject)", body["status"], body["slug"], tc.want)
				}
				assertRestored(t, env, f, tc.want)
				if !adminListIDs(t, env, actor, c.typ, "")[f.id] {
					t.Errorf("restored row %d is missing from the unfiltered admin list", f.id)
				}
			})
		}
	}

	t.Run("a live row is not in the trash", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		id := createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/posts", "Live")
		before := rowSnapshot(t, env, id)
		trashWant(t, adminRestore(t, env, &env.Admin, id, ""), http.StatusBadRequest, "restore of a live row")
		if after := rowSnapshot(t, env, id); after != before {
			t.Errorf("a refused restore changed the row\nbefore: %s\nafter:  %s", before, after)
		}
	})
	t.Run("invalid status", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		id := createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/posts", "Gone")
		trashWant(t, adminDelete(t, env, &env.Admin, id, false), http.StatusNoContent, "admin DELETE")
		trashWant(t, adminRestore(t, env, &env.Admin, id, `{"status":"bogus"}`), http.StatusBadRequest, "restore with a bad status")
		if status, _ := trashStatusName(t, env, id); status != "trash" {
			t.Errorf("status after a refused restore = %s, want trash", status)
		}
	})
}

// TestBuildHandlerTrashAdminEmptyTrash (Req 17): empty-trash removes the
// trashed rows of one type, skips those the actor may not delete and caps at
// 1000.
func TestBuildHandlerTrashAdminEmptyTrash(t *testing.T) {
	t.Run("one type only", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		var posts, pages, products []int64
		for i := 0; i < 2; i++ {
			posts = append(posts, createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/posts", fmt.Sprintf("Post%d", i)))
			pages = append(pages, createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/pages", fmt.Sprintf("Page%d", i)))
			products = append(products, createRESTRow(t, env, &env.Products, "/wp-json/wp/v2/product", fmt.Sprintf("Product%d", i)))
		}
		live := createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/posts", "Live")
		for _, id := range append(append([]int64{}, posts...), pages...) {
			trashWant(t, adminDelete(t, env, &env.Admin, id, false), http.StatusNoContent, "trash")
		}
		for _, id := range products {
			trashWant(t, adminDelete(t, env, &env.Products, id, false), http.StatusNoContent, "trash product")
		}
		rec := adminEmptyTrash(t, env, &env.Admin, "post")
		trashWant(t, rec, http.StatusOK, "empty-trash post")
		if got := trashDecode(t, rec); got["deleted"] != float64(2) || got["skipped"] != float64(0) || got["more"] != false {
			t.Errorf("response = %s, want deleted 2, skipped 0, more false", rec.Body.String())
		}
		for _, id := range posts {
			if got := rowSnapshot(t, env, id); got != "<missing>" {
				t.Errorf("trashed post %d survived empty-trash", id)
			}
		}
		for _, id := range append(append([]int64{}, pages...), products...) {
			if status, _ := trashStatusName(t, env, id); status != "trash" {
				t.Errorf("row %d of another type has status %s, want trash", id, status)
			}
		}
		if status, _ := trashStatusName(t, env, live); status != "publish" {
			t.Errorf("live post status = %s, want publish", status)
		}

		rec = adminEmptyTrash(t, env, &env.Products, "product")
		trashWant(t, rec, http.StatusOK, "empty-trash product")
		if got := trashDecode(t, rec); got["deleted"] != float64(2) {
			t.Errorf("product response = %s, want deleted 2", rec.Body.String())
		}
	})

	t.Run("skips rows the actor may not delete", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		contrib := trashUserID(t, env, "contrib")
		author := trashUserID(t, env, "author")
		own := trashInsertPost(t, env, "post", "draft", "Own Draft", "own-draft", hardeningRawDate, contrib, 0)
		foreign := trashInsertPost(t, env, "post", "publish", "Foreign", "foreign", hardeningRawDate, author, 0)
		published := trashInsertPost(t, env, "post", "publish", "Own Published", "own-published", hardeningRawDate, contrib, 0)
		for _, id := range []int64{own, foreign, published} {
			trashWant(t, adminDelete(t, env, &env.Admin, id, false), http.StatusNoContent, "admin trash")
		}
		rec := adminEmptyTrash(t, env, &env.Contrib, "post")
		trashWant(t, rec, http.StatusOK, "contributor empty-trash")
		if got := trashDecode(t, rec); got["deleted"] != float64(1) || got["skipped"] != float64(2) || got["more"] != false {
			t.Errorf("response = %s, want deleted 1, skipped 2, more false", rec.Body.String())
		}
		if got := rowSnapshot(t, env, own); got != "<missing>" {
			t.Errorf("the contributor's own draft survived empty-trash")
		}
		for _, id := range []int64{foreign, published} {
			if status, _ := trashStatusName(t, env, id); status != "trash" {
				t.Errorf("row %d status = %s, want trash (skipped)", id, status)
			}
		}
	})

	t.Run("caps at 1000 and reports more", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		admin := trashUserID(t, env, "admin")
		const total = 1001
		for i := 0; i < total; i++ {
			trashInsertPost(t, env, "post", "trash", fmt.Sprintf("T%d", i), fmt.Sprintf("t%d__trashed", i), hardeningRawDate, admin, 0)
		}
		rec := adminEmptyTrash(t, env, &env.Admin, "post")
		trashWant(t, rec, http.StatusOK, "first empty-trash")
		if got := trashDecode(t, rec); got["deleted"] != float64(1000) || got["skipped"] != float64(0) || got["more"] != true {
			t.Fatalf("first response = %s, want deleted 1000, skipped 0, more true", rec.Body.String())
		}
		rec = adminEmptyTrash(t, env, &env.Admin, "post")
		trashWant(t, rec, http.StatusOK, "second empty-trash")
		if got := trashDecode(t, rec); got["deleted"] != float64(1) || got["more"] != false {
			t.Errorf("second response = %s, want deleted 1, more false", rec.Body.String())
		}
	})

	t.Run("bad type", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		for _, typ := range []string{"", "shop_order", "nope"} {
			trashWant(t, adminEmptyTrash(t, env, &env.Admin, typ), http.StatusBadRequest, "empty-trash type "+typ)
		}
	})
}

// TestBuildHandlerTrashAdminSession (Req 15.7): the session response reports
// trashEnabled.
func TestBuildHandlerTrashAdminSession(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	rec := doJSON(t, env.Handler, &env.Admin, http.MethodGet, "/admin/api/session", "")
	trashWant(t, rec, http.StatusOK, "session")
	if got := trashDecode(t, rec)["trashEnabled"]; got != true {
		t.Errorf("trashEnabled = %v, want true", got)
	}
}

// TestBuildHandlerTrashAdminCapabilities (Req 15.5, 16.5): the admin routes
// authorize a trashed row as its previous status.
func TestBuildHandlerTrashAdminCapabilities(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	published := trashInsertPost(t, env, "post", "publish", "Foreign", "foreign", hardeningRawDate, trashUserID(t, env, "author"), 0)

	before := rowSnapshot(t, env, published)
	trashWant(t, adminDelete(t, env, &env.Contrib, published, false), http.StatusForbidden, "contributor trash")
	if after := rowSnapshot(t, env, published); after != before {
		t.Errorf("a refused trash changed the row\nbefore: %s\nafter:  %s", before, after)
	}

	trashWant(t, adminDelete(t, env, &env.Admin, published, false), http.StatusNoContent, "admin trash")
	before = rowSnapshot(t, env, published)
	trashWant(t, adminRestore(t, env, &env.Contrib, published, ""), http.StatusForbidden, "contributor restore")
	trashWant(t, adminDelete(t, env, &env.Contrib, published, true), http.StatusForbidden, "contributor force delete")
	if after := rowSnapshot(t, env, published); after != before {
		t.Errorf("a refused write changed the trashed row\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestBuildHandlerTrashAdminCSRF (Req 15, 16.1, 17.1): the trash routes are
// CSRF-checked like the other post writes.
func TestBuildHandlerTrashAdminCSRF(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	bad := env.Admin
	bad.CSRF = "not-the-token"
	live := createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/posts", "Live")
	trashed := createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/posts", "Trashed")
	trashWant(t, adminDelete(t, env, &env.Admin, trashed, false), http.StatusNoContent, "admin trash")
	liveBefore, trashedBefore := rowSnapshot(t, env, live), rowSnapshot(t, env, trashed)

	trashWant(t, adminDelete(t, env, &bad, live, false), http.StatusForbidden, "DELETE with a bad token")
	trashWant(t, adminDelete(t, env, &bad, live, true), http.StatusForbidden, "force DELETE with a bad token")
	trashWant(t, adminRestore(t, env, &bad, trashed, ""), http.StatusForbidden, "restore with a bad token")
	trashWant(t, adminEmptyTrash(t, env, &bad, "post"), http.StatusForbidden, "empty-trash with a bad token")

	if after := rowSnapshot(t, env, live); after != liveBefore {
		t.Errorf("the live row changed\nbefore: %s\nafter:  %s", liveBefore, after)
	}
	if after := rowSnapshot(t, env, trashed); after != trashedBefore {
		t.Errorf("the trashed row changed\nbefore: %s\nafter:  %s", trashedBefore, after)
	}
}

// TestBuildHandlerTrashAdminUnwired (Req 15.4, 19.1): a service built without
// the trasher answers a non-force admin DELETE with 409 trash_unsupported and
// keeps the row; force still deletes.
func TestBuildHandlerTrashAdminUnwired(t *testing.T) {
	env := newCustomWriteHandlerWith(t, true, func(r *storage.Repositories) { r.PostTrash = nil })
	id := createRESTRow(t, env, &env.Admin, "/wp-json/wp/v2/posts", "Subject")
	before := rowSnapshot(t, env, id)

	rec := adminDelete(t, env, &env.Admin, id, false)
	trashWant(t, rec, http.StatusConflict, "admin DELETE without a trasher")
	if code := adminErrorCode(t, rec); code != "trash_unsupported" {
		t.Errorf("code = %v, want trash_unsupported", code)
	}
	if after := rowSnapshot(t, env, id); after != before {
		t.Errorf("a refused trash changed the row\nbefore: %s\nafter:  %s", before, after)
	}

	session := doJSON(t, env.Handler, &env.Admin, http.MethodGet, "/admin/api/session", "")
	trashWant(t, session, http.StatusOK, "session")
	if got := trashDecode(t, session)["trashEnabled"]; got != false {
		t.Errorf("trashEnabled = %v, want false", got)
	}

	trashWant(t, adminDelete(t, env, &env.Admin, id, true), http.StatusNoContent, "admin force DELETE without a trasher")
	if got := rowSnapshot(t, env, id); got != "<missing>" {
		t.Errorf("force delete left the row: %s", got)
	}
}
