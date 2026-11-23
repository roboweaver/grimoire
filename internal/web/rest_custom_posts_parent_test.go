package web_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// M12c Phase 14.1 -- RED tests for `parent` on REST custom post writes and the
// nested response link (Req 11.1-11.5, 11.8, 11.9; D7).
//
// Reuses the Phase 9.1 fixture (newRCPWEnv: flat `product`, hierarchical
// `guide`, seeded guide "g-one"). Helper prefix: rcpp.
//
// RED today: `parent` is ignored on REST and the response link is flat.

var rcppCaps = func() map[string]bool {
	m := rcpwCapSet()
	for _, c := range []string{
		"edit_guides", "edit_others_guides", "edit_published_guides",
		"publish_guides", "delete_guides", "delete_others_guides", "delete_published_guides",
		"edit_posts", "edit_others_posts", "edit_published_posts", "publish_posts",
		"edit_pages", "edit_others_pages", "edit_published_pages", "publish_pages",
	} {
		m[c] = true
	}
	return m
}()

func rcppGuidePath(id int64) string { return "/wp-json/wp/v2/guides/" + strconv.FormatInt(id, 10) }

func rcppID(t *testing.T, m map[string]any) int64 {
	t.Helper()
	f, _ := m["id"].(float64)
	if f == 0 {
		t.Fatalf("no id in response: %v", m)
	}
	return int64(f)
}

func rcppParent(t *testing.T, e *rcpwEnv, id int64) int64 {
	t.Helper()
	return rcpwStored(t, e, id).ParentID
}

// Req 11.1, 11.8: create with parent stores and echoes it; link is nested.
func TestRESTCustomParentCreateStoresAndNestsLink(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcppCaps)
	body := `{"title":"Child","slug":"child","status":"publish","parent":` + strconv.FormatInt(e.guideID, 10) + `}`
	rec := rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/guides", body, nil)
	rcpwWant(t, rec, http.StatusCreated, "")
	got := rcpwObj(t, rec)
	if p, _ := got["parent"].(float64); int64(p) != e.guideID {
		t.Errorf("response parent = %v, want %d", got["parent"], e.guideID)
	}
	if link, _ := got["link"].(string); !strings.Contains(link, "/guide/g-one/child") {
		t.Errorf("link = %q, want nested /guide/g-one/child", link)
	}
	if p := rcppParent(t, e, rcppID(t, got)); p != e.guideID {
		t.Errorf("stored parent = %d, want %d", p, e.guideID)
	}
}

// Req 11.5, 11.1, 11.8: omitted parent on PATCH is unchanged; parent 0 makes
// the row top-level; the update response link follows the ancestry.
func TestRESTCustomParentUpdateOmittedAndZero(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcppCaps)
	rec := rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/guides",
		`{"title":"Kid","slug":"kid","status":"publish","parent":`+strconv.FormatInt(e.guideID, 10)+`}`, nil)
	rcpwWant(t, rec, http.StatusCreated, "")
	id := rcppID(t, rcpwObj(t, rec))

	rec = rcpwDo(h, http.MethodPatch, rcppGuidePath(id), `{"title":"Kid 2"}`, nil)
	rcpwWant(t, rec, http.StatusOK, "")
	if p := rcppParent(t, e, id); p != e.guideID {
		t.Errorf("omitted parent changed stored parent to %d", p)
	}
	if link, _ := rcpwObj(t, rec)["link"].(string); !strings.Contains(link, "/guide/g-one/kid") {
		t.Errorf("update link = %q, want nested /guide/g-one/kid", link)
	}

	rec = rcpwDo(h, http.MethodPatch, rcppGuidePath(id), `{"parent":0}`, nil)
	rcpwWant(t, rec, http.StatusOK, "")
	if p := rcppParent(t, e, id); p != 0 {
		t.Errorf("parent:0 left stored parent %d, want 0", p)
	}
	if link, _ := rcpwObj(t, rec)["link"].(string); strings.Contains(link, "g-one/kid") || !strings.Contains(link, "/guide/kid") {
		t.Errorf("top-level link = %q, want /guide/kid", link)
	}
}

// Req 11.8: the delete response's `previous` carries the nested link.
func TestRESTCustomParentDeletePreviousLinkNested(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcppCaps)
	rec := rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/guides",
		`{"title":"Doomed","slug":"doomed","status":"publish","parent":`+strconv.FormatInt(e.guideID, 10)+`}`, nil)
	rcpwWant(t, rec, http.StatusCreated, "")
	id := rcppID(t, rcpwObj(t, rec))

	rec = rcpwDo(h, http.MethodDelete, rcppGuidePath(id)+"?force=true", "", nil)
	rcpwWant(t, rec, http.StatusOK, "")
	prev, _ := rcpwObj(t, rec)["previous"].(map[string]any)
	if link, _ := prev["link"].(string); !strings.Contains(link, "/guide/g-one/doomed") {
		t.Errorf("previous.link = %q, want nested /guide/g-one/doomed", link)
	}
}

// Req 11.2: parent on a flat type -> 400 rest_invalid_param, nothing written.
func TestRESTCustomParentFlatTypeRejected(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcppCaps)
	rec := rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/products",
		`{"title":"P","status":"draft","parent":`+strconv.FormatInt(e.guideID, 10)+`}`, nil)
	rcpwWant(t, rec, http.StatusBadRequest, "rest_invalid_param")
	rec = rcpwDo(h, http.MethodPatch, rcpwPath(e.existing), `{"parent":`+strconv.FormatInt(e.guideID, 10)+`}`, nil)
	rcpwWant(t, rec, http.StatusBadRequest, "rest_invalid_param")
	if p := rcppParent(t, e, e.existing); p != 0 {
		t.Errorf("rejected parent still stored: %d", p)
	}
}

// Req 11.3, 11.4: cycle and absent parent -> 400 rest_invalid_param.
func TestRESTCustomParentCycleAndAbsent(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcppCaps)
	rec := rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/guides",
		`{"title":"Leaf","slug":"leaf","status":"publish","parent":`+strconv.FormatInt(e.guideID, 10)+`}`, nil)
	rcpwWant(t, rec, http.StatusCreated, "")
	leaf := rcppID(t, rcpwObj(t, rec))

	// g-one -> parent leaf would make g-one its own ancestor.
	rec = rcpwDo(h, http.MethodPatch, rcppGuidePath(e.guideID), `{"parent":`+strconv.FormatInt(leaf, 10)+`}`, nil)
	rcpwWant(t, rec, http.StatusBadRequest, "rest_invalid_param")
	// self parent
	rec = rcpwDo(h, http.MethodPatch, rcppGuidePath(e.guideID), `{"parent":`+strconv.FormatInt(e.guideID, 10)+`}`, nil)
	rcpwWant(t, rec, http.StatusBadRequest, "rest_invalid_param")
	// absent parent
	rec = rcpwDo(h, http.MethodPatch, rcppGuidePath(leaf), `{"parent":999999}`, nil)
	rcpwWant(t, rec, http.StatusBadRequest, "rest_invalid_param")
	rec = rcpwDo(h, http.MethodPost, "/wp-json/wp/v2/guides", `{"title":"Orphan","status":"draft","parent":999999}`, nil)
	rcpwWant(t, rec, http.StatusBadRequest, "rest_invalid_param")
	if p := rcppParent(t, e, e.guideID); p != 0 {
		t.Errorf("rejected cycle changed stored parent to %d", p)
	}
}

// Req 11.9: `parent` on /posts bodies is ignored; stored parent kept. (/pages
// honours parent since the write-hardening milestone; see rest_pages_parent_test.go.)
func TestRESTCustomParentIgnoredOnPosts(t *testing.T) {
	e := newRCPWEnv(t)
	h := e.handler(t, true, rcppCaps)
	ctx := context.Background()
	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	mk := func(typ, slug string, parent int64) int64 {
		t.Helper()
		id, err := e.repos.PostWriter.Create(ctx, domain.Post{
			Author: 1, Date: base, Content: "<p>x</p>", Title: "T-" + slug, Status: "publish",
			Slug: slug, Type: typ, CommentStatus: "closed", ParentID: parent,
		})
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		return id
	}
	post := mk("post", "pp-post", e.guideID)

	for _, c := range []struct {
		path string
		id   int64
		want int64
	}{
		{"/wp-json/wp/v2/posts/" + strconv.FormatInt(post, 10), post, e.guideID},
	} {
		for _, body := range []string{`{"parent":0}`, `{"parent":999999}`} {
			rec := rcpwDo(h, http.MethodPatch, c.path, body, nil)
			if rec.Code >= 500 {
				t.Fatalf("PATCH %s %s = %d: %s", c.path, body, rec.Code, rec.Body.String())
			}
			if p := rcppParent(t, e, c.id); p != c.want {
				t.Errorf("PATCH %s %s changed stored parent to %d, want %d", c.path, body, p, c.want)
			}
		}
	}
}
