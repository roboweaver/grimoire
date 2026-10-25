package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This file drives the live wp-json term write router (newRESTTermsWriteRouter
// in rest_terms_test.go) to prove M13's parent round-trip and validation
// behavior. Everything here is EXPECTED TO FAIL until task 3.2 threads the
// Parent *int64 field through restTermWriteRequest and the create/update
// handlers and maps the two content parent sentinels to 400 rest_invalid_param.
//
// Fixture categories seeded by storagetest.SeedFixtures (see
// internal/storage/storagetest/contract.go): term_id 10 = "news", 11 = "zeta",
// 12 = "alpha" -- all category, all top-level (parent 0). term_id 13 = "golang"
// is a post_tag. The "admin" Application-Password principal holds
// manage_categories; noCapAppPassword mints a powerless principal for the 403
// cases.

// restTermParentGet reads a single term through the live GET route and returns
// its decoded JSON body. It fails the test on any non-200.
func restTermParentGet(t *testing.T, h http.Handler, path string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, body = %s", path, rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET %s: unmarshal: %v", path, err)
	}
	return got
}

// restTermParentCreate POSTs a category/tag create body and returns the
// recorder, leaving status assertions to the caller.
func restTermParentCreate(t *testing.T, h http.Handler, secret, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doRESTWrite(t, h, http.MethodPost, path, secret, body, nil)
}

// --- Create with parent round-trips (Req 1.2, 6.1) ---

func TestRESTTermParentCreateRoundTrip(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	// Create "Child" nested under the seeded "news" category (term_id 10).
	body := `{"name":"Child","parent":10}`
	rec := restTermParentCreate(t, h, secret, "/wp-json/wp/v2/categories", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create: unmarshal: %v", err)
	}
	// The create response body itself must carry the written parent.
	if created["parent"].(float64) != 10 {
		t.Fatalf("create response parent = %v, want 10: %+v", created["parent"], created)
	}
	// And a fresh read of the new term must report the same parent.
	newID := int64(created["id"].(float64))
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/"+itoa(newID))
	if got["parent"].(float64) != 10 {
		t.Fatalf("read-back parent = %v, want 10: %+v", got["parent"], got)
	}
}

// --- Update re-parents (Req 2.1, 6.4) ---

func TestRESTTermParentUpdateReparents(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	// Re-parent "alpha" (term_id 12, top-level) under "news" (term_id 10).
	body := `{"parent":10}`
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/12", secret, body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/12")
	if got["parent"].(float64) != 10 {
		t.Fatalf("read-back parent = %v, want 10: %+v", got["parent"], got)
	}
}

// --- Partial-update pointer semantics (Req 2.2, 2.3) ---

// A PUT that renames but omits "parent" must leave the stored parent untouched.
// We first re-parent "alpha" (12) under "news" (10), then rename it with no
// parent key and assert the parent survived.
func TestRESTTermParentOmittedLeavesParentUnchanged(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	// Seed a child: re-parent alpha (12) under news (10).
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/12", secret, `{"parent":10}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed re-parent: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Rename it with NO parent key.
	rec = doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/12", secret, `{"name":"Renamed"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/12")
	if got["name"] != "Renamed" {
		t.Fatalf("name = %v, want Renamed", got["name"])
	}
	if got["parent"].(float64) != 10 {
		t.Fatalf("parent after rename = %v, want 10 (unchanged): %+v", got["parent"], got)
	}
}

// A PUT with explicit parent:0 promotes a child back to top-level.
func TestRESTTermParentExplicitZeroPromotesToTopLevel(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	// Seed a child: re-parent alpha (12) under news (10).
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/12", secret, `{"parent":10}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed re-parent: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Promote back to top-level.
	rec = doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/12", secret, `{"parent":0}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/12")
	if got["parent"].(float64) != 0 {
		t.Fatalf("parent after promote = %v, want 0: %+v", got["parent"], got)
	}
}

// --- Cycle -> 400 rest_invalid_param (Req 3.3) ---

// A term named as its own parent is a self-cycle.
func TestRESTTermParentSelfCycleRejected(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	// term_id 10 ("news") parented to itself.
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/10", secret, `{"parent":10}`, nil)
	assertRESTBadRequest(t, rec, "rest_invalid_param")

	// The write must not have taken: news stays top-level.
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/10")
	if got["parent"].(float64) != 0 {
		t.Fatalf("parent after rejected self-cycle = %v, want 0 (unchanged): %+v", got["parent"], got)
	}
}

// A parent -> child chain, then re-parenting the parent under its own child is
// a transitive cycle. We first make alpha (12) a child of news (10), then try
// to set news's parent to alpha.
func TestRESTTermParentDescendantCycleRejected(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	// Make alpha (12) a child of news (10).
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/12", secret, `{"parent":10}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed child: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Now try to set news's (10) parent to its own descendant alpha (12).
	rec = doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/10", secret, `{"parent":12}`, nil)
	assertRESTBadRequest(t, rec, "rest_invalid_param")

	// news must still be top-level.
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/10")
	if got["parent"].(float64) != 0 {
		t.Fatalf("parent after rejected descendant-cycle = %v, want 0 (unchanged): %+v", got["parent"], got)
	}
}

// --- Invalid (nonexistent) parent -> 400 (Req 4.1) ---

func TestRESTTermParentNonexistentOnCreateRejected(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	rec := restTermParentCreate(t, h, secret, "/wp-json/wp/v2/categories", `{"name":"Orphan","parent":999999}`)
	assertRESTBadRequest(t, rec, "rest_invalid_param")
}

func TestRESTTermParentNonexistentOnUpdateRejected(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/12", secret, `{"parent":999999}`, nil)
	assertRESTBadRequest(t, rec, "rest_invalid_param")

	// alpha (12) must stay top-level.
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/12")
	if got["parent"].(float64) != 0 {
		t.Fatalf("parent after rejected invalid parent = %v, want 0 (unchanged): %+v", got["parent"], got)
	}
}

// --- post_tag non-zero parent -> 400 (Req 4.3) ---

func TestRESTTermParentPostTagRejectedOnCreate(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	// A tag cannot have a parent; post_tag is flat. parent names the seeded
	// "news" category (10), but any non-zero parent must be rejected.
	rec := restTermParentCreate(t, h, secret, "/wp-json/wp/v2/tags", `{"name":"T","parent":10}`)
	assertRESTBadRequest(t, rec, "rest_invalid_param")
}

func TestRESTTermParentPostTagRejectedOnUpdate(t *testing.T) {
	h, _, ap := newRESTTermsWriteRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)

	// term_id 13 ("golang") is a post_tag; a non-zero parent must be rejected.
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/tags/13", secret, `{"parent":10}`, nil)
	assertRESTBadRequest(t, rec, "rest_invalid_param")
}

// --- manage_categories gate -> 403 (Req 5.1) ---

// A caller lacking manage_categories sending a parent-carrying CREATE gets 403
// rest_cannot_create (the existing term-create forbidden code).
func TestRESTTermParentCreateForbidden(t *testing.T) {
	h, repos, ap := newRESTTermsWriteRouter(t)
	secret := noCapAppPassword(t, repos, ap)

	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/categories", strings.NewReader(`{"name":"Nope","parent":10}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("nocap", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertRESTForbidden(t, rec, "rest_cannot_create")
}

// A caller lacking manage_categories sending a parent-carrying UPDATE gets 403
// rest_cannot_edit (the existing term-update forbidden code).
func TestRESTTermParentUpdateForbidden(t *testing.T) {
	h, repos, ap := newRESTTermsWriteRouter(t)
	secret := noCapAppPassword(t, repos, ap)

	req := httptest.NewRequest(http.MethodPut, "/wp-json/wp/v2/categories/12", strings.NewReader(`{"parent":10}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("nocap", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertRESTForbidden(t, rec, "rest_cannot_edit")

	// Authorization wins over the (also valid) parent: nothing altered.
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/12")
	if got["parent"].(float64) != 0 {
		t.Fatalf("parent after forbidden update = %v, want 0 (unchanged): %+v", got["parent"], got)
	}
}

// --- shared assertion helpers ---

func assertRESTBadRequest(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal error body: %v, raw = %s", err, rec.Body.String())
	}
	if got["code"] != wantCode {
		t.Fatalf("error code = %v, want %q: %+v", got["code"], wantCode, got)
	}
}

func assertRESTForbidden(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal error body: %v, raw = %s", err, rec.Body.String())
	}
	if got["code"] != wantCode {
		t.Fatalf("error code = %v, want %q: %+v", got["code"], wantCode, got)
	}
}
