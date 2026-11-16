package web_test

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// M17 task 3.1 -- RED tests for REST custom-taxonomy term writes
// (Req 1.1-1.6, 2.1-2.6, 3.2-3.4, 4.2-4.6; D1, D17).
//
// They reuse the M12 rctEnv fixture (rest_custom_terms_test.go): a real SQLite
// store, a real content.TermWriteService on WithAdminWrites and the registry:
//
//	genre (genres)  flat taxonomy,         term 501 "scifi"
//	topic (topics)  hierarchical taxonomy, term 511 "fiction" > 512 "hard-sf"
//
// RED today: every write verb on a registered taxonomy answers 501.
//
// Not covered here: Application-Password CSRF skip (Req 1.5, second half) needs
// an app-password wiring this fixture lacks; it is exercised by the shared
// requireRESTTermCSRF path that the built-in term tests already cover.

// rctwDo issues a request as a session-authenticated caller. csrf=false omits
// the X-CSRF-Token header.
func rctwDo(h http.Handler, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "example.test"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	if csrf {
		req.Header.Set("X-CSRF-Token", "tok")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rctwWant(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if code != "" {
		if got := decodeRESTErrCode(t, rec); got != code {
			t.Fatalf("error code = %q, want %q: %s", got, code, rec.Body.String())
		}
	}
}

const rctwBase = "/wp-json/wp/v2/"

func TestRESTCustomTermWriteCreate(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true, "manage_categories")

	// 1.1: 201, taxonomy, parent and a CustomTermPath link.
	rec := rctwDo(h, http.MethodPost, rctwBase+"genres", `{"name":"Horror","slug":"horror"}`, true)
	rctwWant(t, rec, http.StatusCreated, "")
	m := rcpDecodeObj(t, rec)
	if m["taxonomy"] != "genre" || m["slug"] != "horror" || m["name"] != "Horror" {
		t.Errorf("created = %v, want genre/horror/Horror", m)
	}
	if m["parent"] != float64(0) {
		t.Errorf("parent = %v, want 0", m["parent"])
	}
	if want := rcpHost + e.st.CustomTermPath("genre", []string{"horror"}); m["link"] != want {
		t.Errorf("link = %v, want %q", m["link"], want)
	}

	// 1.1 hierarchical: parent is written and the link nests.
	rec = rctwDo(h, http.MethodPost, rctwBase+"topics", `{"name":"Space Opera","slug":"space-opera","parent":511}`, true)
	rctwWant(t, rec, http.StatusCreated, "")
	m = rcpDecodeObj(t, rec)
	if m["taxonomy"] != "topic" || m["parent"] != float64(rctFiction) {
		t.Errorf("created topic = %v, want taxonomy topic, parent 511", m)
	}
	if want := rcpHost + e.st.CustomTermPath("topic", []string{"fiction", "space-opera"}); m["link"] != want {
		t.Errorf("nested link = %v, want %q", m["link"], want)
	}

	// 1.2: omitted slug is derived from the name by termSlugify.
	rec = rctwDo(h, http.MethodPost, rctwBase+"genres", `{"name":"Dark Fantasy"}`, true)
	rctwWant(t, rec, http.StatusCreated, "")
	if got := rcpDecodeObj(t, rec)["slug"]; got != "dark-fantasy" {
		t.Errorf("derived slug = %v, want dark-fantasy", got)
	}
}

func TestRESTCustomTermWriteCreateRejections(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true, "manage_categories")

	// 1.3: blank or missing name.
	for _, body := range []string{`{"name":"   "}`, `{"slug":"x"}`} {
		rctwWant(t, rctwDo(h, http.MethodPost, rctwBase+"genres", body, true), http.StatusBadRequest, "rest_invalid_param")
	}
	// 3.2: a duplicate slug in the same taxonomy is term_exists.
	rctwWant(t, rctwDo(h, http.MethodPost, rctwBase+"genres", `{"name":"Again","slug":"scifi"}`, true),
		http.StatusBadRequest, "term_exists")
	// 4.2-4.4: parent rules. A flat taxonomy takes no parent; an unknown
	// parent and a parent from another taxonomy are invalid.
	for _, tc := range []struct{ path, body string }{
		{"genres", `{"name":"Child","parent":501}`},
		{"topics", `{"name":"Orphan","parent":999999}`},
		{"topics", `{"name":"Cross","parent":501}`},
	} {
		rctwWant(t, rctwDo(h, http.MethodPost, rctwBase+tc.path, tc.body, true), http.StatusBadRequest, "rest_invalid_param")
	}
}

func TestRESTCustomTermWriteCreateForbiddenAndCSRF(t *testing.T) {
	e := newRCTEnv(t)

	// 1.4: no manage_categories -> 403 rest_cannot_create and nothing written.
	h := e.handler(t, true, true, "edit_posts")
	rctwWant(t, rctwDo(h, http.MethodPost, rctwBase+"genres", `{"name":"Nope","slug":"nope"}`, true),
		http.StatusForbidden, "rest_cannot_create")
	rec := rcpGet(h, rctwBase+"genres", false)
	rcpWantOK(t, rec, "/genres")
	if n := len(rcpDecodeList(t, rec)); n != 1 {
		t.Errorf("genres after forbidden create = %d, want 1 (no write)", n)
	}

	// 1.5: a session request without the CSRF token fails like built-in terms.
	hOK := e.handler(t, true, true, "manage_categories")
	rec = rctwDo(hOK, http.MethodPost, rctwBase+"genres", `{"name":"Nocsrf","slug":"nocsrf"}`, false)
	rctwWant(t, rec, http.StatusForbidden, "rest_forbidden")
}

func TestRESTCustomTermWriteUpdate(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true, "manage_categories")
	hard := rctwBase + "topics/" + rctID(rctHardSF)

	// 2.1: PATCH name only leaves slug and parent untouched.
	rec := rctwDo(h, http.MethodPatch, hard, `{"name":"Harder SF"}`, true)
	rctwWant(t, rec, http.StatusOK, "")
	m := rcpDecodeObj(t, rec)
	if m["name"] != "Harder SF" || m["slug"] != "hard-sf" || m["parent"] != float64(rctFiction) || m["taxonomy"] != "topic" {
		t.Errorf("after name-only PATCH = %v", m)
	}

	// 2.1: explicit parent 0 re-roots; the link follows.
	rec = rctwDo(h, http.MethodPut, hard, `{"parent":0}`, true)
	rctwWant(t, rec, http.StatusOK, "")
	m = rcpDecodeObj(t, rec)
	if m["parent"] != float64(0) {
		t.Errorf("parent after explicit 0 = %v, want 0", m["parent"])
	}
	if want := rcpHost + e.st.CustomTermPath("topic", []string{"hard-sf"}); m["link"] != want {
		t.Errorf("re-rooted link = %v, want %q", m["link"], want)
	}

	// 2.1: explicit parent id re-parents.
	rec = rctwDo(h, http.MethodPatch, hard, `{"parent":511,"slug":"hard-science"}`, true)
	rctwWant(t, rec, http.StatusOK, "")
	m = rcpDecodeObj(t, rec)
	if m["parent"] != float64(rctFiction) || m["slug"] != "hard-science" {
		t.Errorf("after re-parent = %v", m)
	}

	// 4.5/4.6: a cycle (fiction under its own child) is rejected.
	rctwWant(t, rctwDo(h, http.MethodPatch, rctwBase+"topics/"+rctID(rctFiction), `{"parent":512}`, true),
		http.StatusBadRequest, "rest_invalid_param")
}

func TestRESTCustomTermWriteUpdateRejections(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true, "manage_categories")

	// 2.3: unknown id and a term of another taxonomy are 404.
	for _, path := range []string{"genres/999999", "genres/" + rctID(rctFiction), "topics/" + rctID(rctGenre)} {
		rctwWant(t, rctwDo(h, http.MethodPatch, rctwBase+path, `{"name":"X"}`, true), http.StatusNotFound, "rest_term_invalid_id")
	}
	// 3.3: a slug already used among the new siblings is term_exists (slugs are
	// unique per parent, Req 4.4/D6): re-rooting hard-sf next to root "fiction".
	rctwWant(t, rctwDo(h, http.MethodPatch, rctwBase+"topics/"+rctID(rctHardSF), `{"parent":0,"slug":"fiction"}`, true),
		http.StatusBadRequest, "term_exists")
	// 3.4: a blank name is invalid.
	rctwWant(t, rctwDo(h, http.MethodPatch, rctwBase+"genres/"+rctID(rctGenre), `{"name":" "}`, true),
		http.StatusBadRequest, "rest_invalid_param")

	// 2.4: no manage_categories -> 403 rest_cannot_edit; 404 still wins for an
	// unknown id because existence is checked before the capability.
	hNo := e.handler(t, true, true, "edit_posts")
	rctwWant(t, rctwDo(hNo, http.MethodPatch, rctwBase+"genres/"+rctID(rctGenre), `{"name":"Hacked"}`, true),
		http.StatusForbidden, "rest_cannot_edit")
	rctwWant(t, rctwDo(hNo, http.MethodPatch, rctwBase+"genres/999999", `{"name":"Hacked"}`, true),
		http.StatusNotFound, "rest_term_invalid_id")
	rec := rcpGet(hNo, rctwBase+"genres/"+rctID(rctGenre), false)
	rcpWantOK(t, rec, "genres/501")
	if got := rcpDecodeObj(t, rec)["name"]; got != "Science Fiction" {
		t.Errorf("name after forbidden update = %v, want unchanged", got)
	}
}

func TestRESTCustomTermWriteDelete(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true, "manage_categories")
	path := rctwBase + "genres/" + rctID(rctGenre)

	// 2.4 first: the wrong capability does not delete.
	hNo := e.handler(t, true, true, "edit_posts")
	rctwWant(t, rctwDo(hNo, http.MethodDelete, path, "", true), http.StatusForbidden, "rest_cannot_edit")

	// 2.2: 200 {deleted:true, previous}, then the term is gone.
	rec := rctwDo(h, http.MethodDelete, path, "", true)
	rctwWant(t, rec, http.StatusOK, "")
	m := rcpDecodeObj(t, rec)
	if m["deleted"] != true {
		t.Errorf("deleted = %v, want true: %v", m["deleted"], m)
	}
	prev, _ := m["previous"].(map[string]any)
	if prev == nil || prev["id"] != float64(rctGenre) || prev["taxonomy"] != "genre" || prev["slug"] != "scifi" {
		t.Errorf("previous = %v, want genre term 501", m["previous"])
	}
	rctwWant(t, rcpGet(h, path, false), http.StatusNotFound, "rest_term_invalid_id")

	// 2.3: unknown id and another taxonomy's term are 404.
	rctwWant(t, rctwDo(h, http.MethodDelete, rctwBase+"genres/"+rctID(rctFiction), "", true), http.StatusNotFound, "rest_term_invalid_id")
	rctwWant(t, rctwDo(h, http.MethodDelete, rctwBase+"genres/999999", "", true), http.StatusNotFound, "rest_term_invalid_id")
}

// 2.5: only the verbs the plan binds are live; the rest stay 501.
func TestRESTCustomTermWriteUnboundVerbsStill501(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true, "manage_categories")
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "genres/" + rctID(rctGenre)},
		{http.MethodPut, "genres"},
		{http.MethodPatch, "genres"},
		{http.MethodDelete, "genres"},
		{http.MethodPost, "topics/" + rctID(rctFiction)},
	} {
		rec := rctwDo(h, c.method, rctwBase+c.path, `{"name":"x"}`, true)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501: %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// 2.6: the route index advertises the write methods for registered taxonomies.
func TestRESTCustomTermWriteRouteIndex(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, true)
	rec := rcpGet(h, "/wp-json/", false)
	rcpWantOK(t, rec, "/wp-json/")
	routes, _ := rcpDecodeObj(t, rec)["routes"].(map[string]any)
	for _, tax := range []string{"genres", "topics"} {
		for path, want := range map[string][]string{
			"/wp/v2/" + tax:                    {"GET", "POST"},
			"/wp/v2/" + tax + `/(?P<id>[\d]+)`: {"DELETE", "GET", "PATCH", "PUT"},
		} {
			entry, _ := routes[path].(map[string]any)
			var got []string
			for _, m := range entry["methods"].([]any) {
				got = append(got, m.(string))
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("route %s methods = %v, want %v", path, got, want)
			}
		}
	}
}

// 1.6: without a term-write service the write routes are not registered, so
// the verbs do not reach a write handler (the read routes are not registered
// either, matching registerRESTCustomTerms' guard).
func TestRESTCustomTermWriteAbsentWithoutTermWrite(t *testing.T) {
	e := newRCTEnv(t)
	h := e.handler(t, true, false, "manage_categories")
	rec := rctwDo(h, http.MethodPost, rctwBase+"genres", `{"name":"X","slug":"x"}`, true)
	if rec.Code == http.StatusCreated || rec.Code == http.StatusNotImplemented {
		t.Errorf("POST /genres without termWrite = %d, want an unrouted response", rec.Code)
	}
}
