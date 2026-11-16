package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBuildHandlerCustomTaxonomyWrites drives term writes for the registered
// taxonomies genre (hierarchical) and badge (flat) through the production
// handler, over both the REST surface and /admin/api/terms. It is the RED test
// for tasks 5.1/5.2: until buildHandler hands the term service the registry,
// the REST routes for genre/badge are not registered, and the service has no
// vocabulary, cycle, slug or flat-taxonomy checks.
//
// Each assertion is reachable only through the buildHandler wiring named here
// (as assembly_test.go does):
//
//	assertion                                      guards
//	---------------------------------------------  ------------------------------------------
//	POST /wp-json/wp/v2/genre        -> 201        REST term routes (per
//	  with parent applied                          registered taxonomy) + content.WithTermRegistry
//	PUT  /wp-json/wp/v2/genre/{id}   -> 200        term-route wiring + WithTermRegistry (re-parent)
//	PUT  parent = descendant         -> 400        WithTermRegistry hierarchy read (ErrTermParentCycle)
//	POST duplicate sibling slug      -> 400        WithTermRegistry (ErrTermSlugTaken) mapped to
//	  code term_exists                             REST code term_exists
//	POST /wp-json/wp/v2/badge parent -> 400        WithTermRegistry flat taxonomy (ErrTermParentInvalid)
//	DELETE /wp-json/wp/v2/genre/{id} -> 200        term delete route wiring
//	POST/PUT /admin/api/terms genre  -> 201/200    isKnownAdminTaxonomy via Server registry
//	  and cycle -> 400                             (WithRegistry) + WithTermRegistry
//	no manage_categories -> 403 on both surfaces   termWrite capability gate, unchanged
//	GET /genre/{slug}/ and nested    -> 200        WithRegistry + WithCustomTypes (M12 archive)
func TestBuildHandlerCustomTaxonomyWrites(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	h := env.Handler

	type termResp struct {
		ID     int64  `json:"id"`
		Slug   string `json:"slug"`
		Parent int64  `json:"parent"`
		Code   string `json:"code"`
	}
	decode := func(rec *httptest.ResponseRecorder) termResp {
		t.Helper()
		var out termResp
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		return out
	}
	want := func(rec *httptest.ResponseRecorder, status int, what string) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("%s: status = %d, want %d; body: %s", what, rec.Code, status, rec.Body.String())
		}
	}

	const restGenre = "/wp-json/wp/v2/genre"

	// Create a root and a child over REST as the administrator.
	rec := doJSON(t, h, &env.Admin, http.MethodPost, restGenre, `{"name":"Fiction","slug":"fiction"}`)
	want(rec, http.StatusCreated, "create genre root")
	root := decode(rec)

	rec = doJSON(t, h, &env.Admin, http.MethodPost, restGenre,
		fmt.Sprintf(`{"name":"Novels","slug":"novels","parent":%d}`, root.ID))
	want(rec, http.StatusCreated, "create genre child")
	child := decode(rec)
	if child.Parent != root.ID {
		t.Fatalf("child parent = %d, want %d", child.Parent, root.ID)
	}

	// Public archives for the new terms (M12 route), nested path for the child.
	for _, p := range []string{"/genre/fiction/", "/genre/fiction/novels/"} {
		want(doJSON(t, h, nil, http.MethodGet, p, ""), http.StatusOK, "GET "+p)
	}

	// Re-parent the child under a second root, then back.
	rec = doJSON(t, h, &env.Admin, http.MethodPost, restGenre, `{"name":"Poetry","slug":"poetry"}`)
	want(rec, http.StatusCreated, "create second root")
	other := decode(rec)
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("%s/%d", restGenre, child.ID),
		fmt.Sprintf(`{"parent":%d}`, other.ID))
	want(rec, http.StatusOK, "re-parent child")
	if got := decode(rec); got.Parent != other.ID {
		t.Fatalf("re-parented parent = %d, want %d", got.Parent, other.ID)
	}
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("%s/%d", restGenre, child.ID),
		fmt.Sprintf(`{"parent":%d}`, root.ID))
	want(rec, http.StatusOK, "re-parent child back")

	// Cycle: the root under its own child.
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("%s/%d", restGenre, root.ID),
		fmt.Sprintf(`{"parent":%d}`, child.ID))
	want(rec, http.StatusBadRequest, "cycle")

	// Duplicate sibling slug.
	rec = doJSON(t, h, &env.Admin, http.MethodPost, restGenre, `{"name":"Fiction 2","slug":"fiction"}`)
	want(rec, http.StatusBadRequest, "duplicate slug")
	if got := decode(rec); got.Code != "term_exists" {
		t.Fatalf("duplicate slug code = %q, want term_exists", got.Code)
	}

	// A flat taxonomy rejects any parent.
	rec = doJSON(t, h, &env.Admin, http.MethodPost, "/wp-json/wp/v2/badge",
		fmt.Sprintf(`{"name":"Staff Pick","slug":"staff-pick","parent":%d}`, root.ID))
	want(rec, http.StatusBadRequest, "badge with parent")

	// The same flows through /admin/api/terms.
	rec = doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
		fmt.Sprintf(`{"name":"Essays","slug":"essays","taxonomy":"genre","parent":%d}`, root.ID))
	want(rec, http.StatusCreated, "admin create genre child")
	adminChild := decode(rec)
	rec = doJSON(t, h, &env.Admin, http.MethodPut, fmt.Sprintf("/admin/api/terms/%d", root.ID),
		fmt.Sprintf(`{"name":"Fiction","slug":"fiction","parent":%d}`, adminChild.ID))
	want(rec, http.StatusBadRequest, "admin cycle")
	rec = doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
		`{"name":"Fiction 3","slug":"fiction","taxonomy":"genre"}`)
	want(rec, http.StatusBadRequest, "admin duplicate slug")
	rec = doJSON(t, h, &env.Admin, http.MethodPost, "/admin/api/terms",
		fmt.Sprintf(`{"name":"Gold","slug":"gold","taxonomy":"badge","parent":%d}`, root.ID))
	want(rec, http.StatusBadRequest, "admin badge with parent")

	// A user without manage_categories is refused on both surfaces.
	for _, actor := range []*customWriteActor{&env.Author, &env.Contrib, &env.Products} {
		want(doJSON(t, h, actor, http.MethodPost, restGenre, `{"name":"Nope","slug":"nope"}`),
			http.StatusForbidden, actor.Login+" REST create")
		want(doJSON(t, h, actor, http.MethodPost, "/admin/api/terms",
			`{"name":"Nope","slug":"nope","taxonomy":"genre"}`),
			http.StatusForbidden, actor.Login+" admin create")
	}

	// Delete the parent term.
	want(doJSON(t, h, &env.Admin, http.MethodDelete, fmt.Sprintf("%s/%d", restGenre, root.ID), ""),
		http.StatusOK, "delete parent")
}
