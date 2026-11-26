package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// M22 task 7.1 -- RED tests for the admin API trash transport (Req 10.2,
// 15-17; D1; sign-off 5, 6).
//
// Everything runs over fakes (atr* prefix). The handlers under test are the
// ones task 7.2 changes or adds: adminPostDelete, adminPostRestore,
// adminPostEmptyTrash, adminPosts (status=trash), adminSession (trashEnabled)
// and the adminAPIRouter registrations.
//
// Assumption pinned here (design.md "Admin API"): empty-trash pages ids through
// the Server's domain.AdminPostRepository (the field REST also uses,
// Server.restPosts) with ListForAdmin, Statuses ["trash"], OrderBy "id",
// 100 per page, Types [type].
//
// RED today: Server has no postTrash-aware admin handlers, so this file does
// not compile (adminPostRestore and adminPostEmptyTrash do not exist). Once
// stubbed, DELETE calls Delete, "trash" is an invalid status and the routes
// are absent.

// atrTrasher is a recording postTrashWriter.
type atrTrasher struct {
	enabled  bool
	trashErr error
	restErr  error
	trashed  []int64
	restored []int64
	statuses []string
}

func (f *atrTrasher) Trash(_ context.Context, _ auth.Principal, id int64) error {
	f.trashed = append(f.trashed, id)
	return f.trashErr
}

func (f *atrTrasher) Restore(_ context.Context, _ auth.Principal, id int64, status string) error {
	f.restored = append(f.restored, id)
	f.statuses = append(f.statuses, status)
	if f.restErr != nil {
		return f.restErr
	}
	switch status {
	case "", "previous", "publish", "future", "draft", "pending", "private":
		return nil
	}
	return content.ErrRestoreStatusInvalid
}

func (f *atrTrasher) TrashEnabled() bool { return f.enabled }

// atrLister is a static domain.AdminPostRepository of trashed rows that
// honors Limit, Offset and Order, and records every filter it receives.
type atrLister struct {
	ids     []int64 // trashed ids, any order
	filters []domain.AdminPostFilter
}

func (l *atrLister) ListForAdmin(_ context.Context, f domain.AdminPostFilter) ([]domain.Post, error) {
	l.filters = append(l.filters, f)
	ids := append([]int64(nil), l.ids...)
	sort.Slice(ids, func(i, j int) bool {
		if f.Order == "desc" {
			return ids[i] > ids[j]
		}
		return ids[i] < ids[j]
	})
	if f.Offset > len(ids) {
		ids = nil
	} else {
		ids = ids[f.Offset:]
	}
	if f.Limit > 0 && f.Limit < len(ids) {
		ids = ids[:f.Limit]
	}
	out := make([]domain.Post, len(ids))
	for i, id := range ids {
		out[i] = domain.Post{ID: id, Status: "trash", Type: "post"}
	}
	return out, nil
}

func (l *atrLister) CountForAdmin(context.Context, domain.AdminPostFilter) (int, error) {
	return len(l.ids), nil
}

func (l *atrLister) Authors(context.Context) ([]domain.AuthorOption, error) { return nil, nil }

func atrIDs(from, to int64) []int64 {
	var out []int64
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func atrDetail(status string) *fakeAdmin {
	return &fakeAdmin{detail: func(id int64) (domain.Post, error) {
		return domain.Post{ID: id, Title: "T", Slug: "t", Type: "post", Status: status, Modified: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)}, nil
	}}
}

func atrServer(a adminReader, pw postAdminWriter, tr *atrTrasher) *Server {
	s := testWriteServer(a, pw, nil, nil, nil)
	if tr != nil {
		s.postTrash = tr
	}
	return s
}

func atrReq(method, path, body, idParam string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(principalCtx("edit_posts"))
	if idParam != "" {
		req = withURLParam(req, "id", idParam)
	}
	return req
}

func atrErrorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("unmarshal error body: %v (%s)", err, rec.Body.String())
	}
	return b.Error.Message
}

// atrRouter builds the full session + adminAPIRouter stack for route and CSRF
// tests. The caller is an administrator whose session token is "token".
func atrRouter(s *Server) http.Handler {
	s.auth = fakeSessions{p: auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	return s.SessionMiddleware(s.adminAPIRouter())
}

func atrRouterReq(method, path, body string, csrf bool) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-CSRF-Token", "token")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	return req
}

// --- (a) DELETE -----------------------------------------------------------

func TestAdminTrashDeleteWithoutForceTrashes(t *testing.T) {
	pw := &fakePostWrite{}
	tr := &atrTrasher{enabled: true}
	s := atrServer(&fakeAdmin{}, pw, tr)
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostDelete).ServeHTTP(rec, atrReq(http.MethodDelete, "/admin/api/posts/3", "", "3"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if len(tr.trashed) != 1 || tr.trashed[0] != 3 {
		t.Errorf("Trash calls = %v, want [3]", tr.trashed)
	}
	if pw.lastDelete.ID != 0 {
		t.Errorf("Delete reached with id=%d; a non-force DELETE must not delete", pw.lastDelete.ID)
	}
}

func TestAdminTrashDeleteForceDeletes(t *testing.T) {
	for _, q := range []string{"?force=true", "?force=1"} {
		pw := &fakePostWrite{}
		tr := &atrTrasher{enabled: true}
		s := atrServer(&fakeAdmin{}, pw, tr)
		rec := httptest.NewRecorder()
		s.jsonHandler(s.adminPostDelete).ServeHTTP(rec, atrReq(http.MethodDelete, "/admin/api/posts/3"+q, "", "3"))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want 204, body=%s", q, rec.Code, rec.Body.String())
		}
		if pw.lastDelete.ID != 3 {
			t.Errorf("%s: Delete id = %d, want 3", q, pw.lastDelete.ID)
		}
		if len(tr.trashed) != 0 {
			t.Errorf("%s: Trash called %v; force must not trash", q, tr.trashed)
		}
	}
}

func TestAdminTrashDeleteErrors(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		trashErr error
		delErr   error
		idParam  string
		trasher  bool
		wantCode int
		wantErr  string
		wantMsg  string
	}{
		{name: "already trashed", trashErr: domain.ErrAlreadyTrashed, idParam: "3", trasher: true, wantCode: 409, wantErr: "already_trashed"},
		{name: "trash unsupported", trashErr: content.ErrTrashUnsupported, idParam: "3", trasher: true, wantCode: 409, wantErr: "trash_unsupported", wantMsg: "trash is disabled; use force=true"},
		{name: "trash unsupported, no trasher wired", idParam: "3", trasher: false, wantCode: 409, wantErr: "trash_unsupported", wantMsg: "trash is disabled; use force=true"},
		{name: "forbidden on trash", trashErr: content.ErrForbidden, idParam: "3", trasher: true, wantCode: 403, wantErr: "forbidden"},
		{name: "forbidden on force delete", query: "?force=true", delErr: content.ErrForbidden, idParam: "3", trasher: true, wantCode: 403, wantErr: "forbidden"},
		{name: "not found on trash", trashErr: domain.ErrNotFound, idParam: "3", trasher: true, wantCode: 404, wantErr: "not_found"},
		{name: "bad id", idParam: "abc", trasher: true, wantCode: 400, wantErr: "bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pw := &fakePostWrite{del: func(domain.Post) error { return tc.delErr }}
			var tr *atrTrasher
			if tc.trasher {
				tr = &atrTrasher{enabled: true, trashErr: tc.trashErr}
			}
			s := atrServer(&fakeAdmin{}, pw, tr)
			rec := httptest.NewRecorder()
			s.jsonHandler(s.adminPostDelete).ServeHTTP(rec, atrReq(http.MethodDelete, "/admin/api/posts/"+tc.idParam+tc.query, "", tc.idParam))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			assertJSONError(t, rec, tc.wantErr)
			if tc.wantMsg != "" {
				if got := atrErrorMessage(t, rec); got != tc.wantMsg {
					t.Errorf("message = %q, want %q", got, tc.wantMsg)
				}
			}
			if tc.trashErr != nil || !tc.trasher {
				if pw.lastDelete.ID != 0 {
					t.Errorf("Delete reached for a failed non-force DELETE")
				}
			}
		})
	}
}

// --- (b) PUT / POST status trash -------------------------------------------

func TestAdminTrashPutStatusTrashReachesService(t *testing.T) {
	pw := &fakePostWrite{}
	s := atrServer(atrDetail("publish"), pw, &atrTrasher{enabled: true})
	body := `{"title":"T","status":"trash","modified":"2024-01-02T03:04:05Z"}`
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, atrReq(http.MethodPut, "/admin/api/posts/7", body, "7"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if pw.lastUpdate.ID != 7 || pw.lastUpdate.Status != "trash" {
		t.Errorf("Update got %+v, want id 7 status trash", pw.lastUpdate)
	}
}

func TestAdminTrashPutLeavingStatusReachesService(t *testing.T) {
	pw := &fakePostWrite{}
	s := atrServer(atrDetail("trash"), pw, &atrTrasher{enabled: true})
	body := `{"title":"T","status":"draft","modified":"2024-01-02T03:04:05Z"}`
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostUpdate).ServeHTTP(rec, atrReq(http.MethodPut, "/admin/api/posts/7", body, "7"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if pw.lastUpdate.ID != 7 || pw.lastUpdate.Status != "draft" {
		t.Errorf("Update got %+v, want id 7 status draft", pw.lastUpdate)
	}
}

func TestAdminTrashCreateWithTrashStatusIsRejected(t *testing.T) {
	pw := &fakePostWrite{}
	s := atrServer(&fakeAdmin{}, pw, &atrTrasher{enabled: true})
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostCreate).ServeHTTP(rec, atrReq(http.MethodPost, "/admin/api/posts", `{"title":"x","status":"trash"}`, ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	assertJSONError(t, rec, "bad_request")
	if got := atrErrorMessage(t, rec); got != "invalid status" {
		t.Errorf("message = %q, want %q", got, "invalid status")
	}
	if pw.lastCreate.Title != "" {
		t.Errorf("Create reached with %+v", pw.lastCreate)
	}
}

// --- (c) restore ------------------------------------------------------------

func TestAdminTrashRestoreStatusForwarding(t *testing.T) {
	cases := []struct {
		name, body, wantStatus string
	}{
		{"empty body", ``, ""},
		{"empty status", `{"status":""}`, ""},
		{"no status key", `{}`, ""},
		{"previous", `{"status":"previous"}`, "previous"},
		{"explicit publish", `{"status":"publish"}`, "publish"},
		{"explicit pending", `{"status":"pending"}`, "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &atrTrasher{enabled: true}
			s := atrServer(atrDetail("draft"), &fakePostWrite{}, tr)
			rec := httptest.NewRecorder()
			s.jsonHandler(s.adminPostRestore).ServeHTTP(rec, atrReq(http.MethodPost, "/admin/api/posts/9/restore", tc.body, "9"))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
			}
			if len(tr.restored) != 1 || tr.restored[0] != 9 {
				t.Fatalf("Restore calls = %v, want [9]", tr.restored)
			}
			// "" means draft and "previous" means the recorded status; both are
			// resolved by the service (Restore doc), so the handler must hand
			// them over unchanged.
			if tr.statuses[0] != tc.wantStatus {
				t.Errorf("Restore status = %q, want %q", tr.statuses[0], tc.wantStatus)
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp["id"].(float64) != 9 || resp["status"] != "draft" || resp["type"] != "post" {
				t.Errorf("response is not the post detail: %v", resp)
			}
		})
	}
}

func TestAdminTrashRestoreErrors(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		restErr  error
		idParam  string
		wantCode int
		wantErr  string
		wantMsg  string
	}{
		{name: "live row", restErr: domain.ErrNotTrashed, idParam: "9", wantCode: 400, wantErr: "bad_request", wantMsg: "post is not in the trash"},
		{name: "forbidden", restErr: content.ErrForbidden, idParam: "9", wantCode: 403, wantErr: "forbidden"},
		{name: "invalid status", body: `{"status":"bogus"}`, idParam: "9", wantCode: 400, wantErr: "bad_request"},
		{name: "trash is not a restore status", body: `{"status":"trash"}`, idParam: "9", wantCode: 400, wantErr: "bad_request"},
		{name: "bad id", idParam: "abc", wantCode: 400, wantErr: "bad_request"},
		{name: "malformed body", body: `{"status":`, idParam: "9", wantCode: 400, wantErr: "bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &atrTrasher{enabled: true, restErr: tc.restErr}
			s := atrServer(atrDetail("draft"), &fakePostWrite{}, tr)
			rec := httptest.NewRecorder()
			s.jsonHandler(s.adminPostRestore).ServeHTTP(rec, atrReq(http.MethodPost, "/admin/api/posts/"+tc.idParam+"/restore", tc.body, tc.idParam))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			assertJSONError(t, rec, tc.wantErr)
			if tc.wantMsg != "" {
				if got := atrErrorMessage(t, rec); got != tc.wantMsg {
					t.Errorf("message = %q, want %q", got, tc.wantMsg)
				}
			}
		})
	}
}

func TestAdminTrashRestoreRouteRequiresCSRF(t *testing.T) {
	tr := &atrTrasher{enabled: true}
	h := atrRouter(atrServer(atrDetail("draft"), &fakePostWrite{}, tr))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, atrRouterReq(http.MethodPost, "/posts/9/restore", ``, false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without CSRF: status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
	if len(tr.restored) != 0 {
		t.Fatalf("Restore reached without a CSRF token")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, atrRouterReq(http.MethodPost, "/posts/9/restore", ``, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("with CSRF: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if len(tr.restored) != 1 || tr.restored[0] != 9 {
		t.Errorf("Restore calls = %v, want [9]", tr.restored)
	}
}

func TestAdminTrashRevisionRestoreRouteUnaffected(t *testing.T) {
	restored := domain.Post{ID: 7, Title: "R", Status: "publish", Type: "post"}
	ra := &fakeRevisionAdmin{restore: func(int64, int64) (domain.Post, error) { return restored, nil }}
	tr := &atrTrasher{enabled: true}
	s := testRevisionServer(&fakeAdmin{detail: func(int64) (domain.Post, error) { return restored, nil }}, ra)
	s.postWrite = &fakePostWrite{}
	s.postTrash = tr
	h := atrRouter(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, atrRouterReq(http.MethodPost, "/posts/7/revisions/1/restore", ``, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("revision restore status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if len(tr.restored) != 0 {
		t.Errorf("revision restore reached the trash Restore: %v", tr.restored)
	}
}

// --- (d) empty-trash --------------------------------------------------------

func atrEmptyServer(ids []int64, del func(domain.Post) error) (*Server, *atrLister, *[]int64) {
	var deleted []int64
	pw := &fakePostWrite{del: func(p domain.Post) error {
		deleted = append(deleted, p.ID)
		if del != nil {
			return del(p)
		}
		return nil
	}}
	s := atrServer(&fakeAdmin{}, pw, &atrTrasher{enabled: true})
	l := &atrLister{ids: ids}
	s.restPosts = l
	return s, l, &deleted
}

func atrEmptyTrash(s *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPostEmptyTrash).ServeHTTP(rec, atrReq(http.MethodPost, "/admin/api/posts/empty-trash", body, ""))
	return rec
}

type atrEmptyResp struct {
	Deleted *int  `json:"deleted"`
	Skipped *int  `json:"skipped"`
	More    *bool `json:"more"`
}

func atrDecodeEmpty(t *testing.T, rec *httptest.ResponseRecorder) (deleted, skipped int, more bool) {
	t.Helper()
	var r atrEmptyResp
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if r.Deleted == nil || r.Skipped == nil || r.More == nil {
		t.Fatalf("response must carry deleted, skipped and more: %s", rec.Body.String())
	}
	return *r.Deleted, *r.Skipped, *r.More
}

func TestAdminTrashEmptyTrashDeletesAndSkips(t *testing.T) {
	s, l, deleted := atrEmptyServer(atrIDs(1, 5), func(p domain.Post) error {
		if p.ID == 2 || p.ID == 4 { // the actor lacks delete capability for these rows
			return content.ErrForbidden
		}
		return nil
	})
	rec := atrEmptyTrash(s, `{"type":"post"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	d, sk, more := atrDecodeEmpty(t, rec)
	if d != 3 || sk != 2 || more {
		t.Errorf("deleted=%d skipped=%d more=%v, want 3 2 false", d, sk, more)
	}
	if len(*deleted) != 5 {
		t.Errorf("Delete called for %v, want every trashed id once", *deleted)
	}
	if len(l.filters) == 0 {
		t.Fatal("the lister was never called")
	}
	for _, f := range l.filters {
		if len(f.Statuses) != 1 || f.Statuses[0] != "trash" {
			t.Errorf("filter Statuses = %v, want [trash]", f.Statuses)
		}
		if len(f.Types) != 1 || f.Types[0] != "post" {
			t.Errorf("filter Types = %v, want [post]", f.Types)
		}
		if f.OrderBy != "id" || f.Order == "desc" {
			t.Errorf("filter order = %q/%q, want id ascending (oldest first)", f.OrderBy, f.Order)
		}
	}
}

func TestAdminTrashEmptyTrashCapsAtOneThousand(t *testing.T) {
	s, l, deleted := atrEmptyServer(atrIDs(1, 1050), nil)
	rec := atrEmptyTrash(s, `{"type":"post"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	d, sk, more := atrDecodeEmpty(t, rec)
	if d != 1000 || sk != 0 || !more {
		t.Errorf("deleted=%d skipped=%d more=%v, want 1000 0 true", d, sk, more)
	}
	if len(*deleted) != 1000 {
		t.Fatalf("examined %d rows, want exactly 1000", len(*deleted))
	}
	if (*deleted)[0] != 1 || (*deleted)[999] != 1000 {
		t.Errorf("rows examined run %d..%d, want the oldest ids 1..1000", (*deleted)[0], (*deleted)[999])
	}
	// Req 17.5: ids come 100 per page, never all at once.
	for i, f := range l.filters {
		if f.Limit <= 0 || f.Limit > 100 {
			t.Errorf("list call %d Limit = %d, want 1..100", i, f.Limit)
		}
	}
	if len(l.filters) < 10 {
		t.Errorf("only %d list calls for 1000 ids at 100 per page", len(l.filters))
	}
}

func TestAdminTrashEmptyTrashExactlyOneThousandHasNoMore(t *testing.T) {
	s, _, deleted := atrEmptyServer(atrIDs(1, 1000), nil)
	rec := atrEmptyTrash(s, `{"type":"post"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	d, _, more := atrDecodeEmpty(t, rec)
	if d != 1000 || more || len(*deleted) != 1000 {
		t.Errorf("deleted=%d more=%v calls=%d, want 1000 false 1000", d, more, len(*deleted))
	}
}

func TestAdminTrashEmptyTrashStopsOnNonAuthorizationFailure(t *testing.T) {
	s, _, deleted := atrEmptyServer(atrIDs(1, 6), func(p domain.Post) error {
		if p.ID == 3 {
			return errors.New("disk on fire")
		}
		return nil
	})
	rec := atrEmptyTrash(s, `{"type":"post"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
	assertJSONError(t, rec, "internal")
	if got := *deleted; len(got) != 3 || got[2] != 3 {
		t.Errorf("Delete calls = %v, want to stop at id 3", got)
	}
}

func TestAdminTrashEmptyTrashRejectsBadType(t *testing.T) {
	for name, body := range map[string]string{
		"missing":     `{}`,
		"empty":       `{"type":""}`,
		"unknown":     `{"type":"widget"}`,
		"unmanaged":   `{"type":"attachment"}`,
		"nav menu":    `{"type":"nav_menu_item"}`,
		"revision":    `{"type":"revision"}`,
		"bad json":    `{"type":`,
		"empty input": ``,
	} {
		t.Run(name, func(t *testing.T) {
			s, l, deleted := atrEmptyServer(atrIDs(1, 3), nil)
			rec := atrEmptyTrash(s, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
			}
			assertJSONError(t, rec, "bad_request")
			if len(*deleted) != 0 || len(l.filters) != 0 {
				t.Errorf("a rejected type must not list or delete (deleted=%v lists=%d)", *deleted, len(l.filters))
			}
		})
	}
}

func TestAdminTrashEmptyTrashPageType(t *testing.T) {
	s, l, _ := atrEmptyServer(atrIDs(1, 2), nil)
	rec := atrEmptyTrash(s, `{"type":"page"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if len(l.filters) == 0 || len(l.filters[0].Types) != 1 || l.filters[0].Types[0] != "page" {
		t.Errorf("filters = %+v, want Types [page]", l.filters)
	}
}

func TestAdminTrashEmptyTrashRouteIsCSRFGatedAndNotShadowed(t *testing.T) {
	s, _, deleted := atrEmptyServer(atrIDs(1, 2), nil)
	h := atrRouter(s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, atrRouterReq(http.MethodPost, "/posts/empty-trash", `{"type":"post"}`, false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without CSRF: status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
	if len(*deleted) != 0 {
		t.Fatalf("Delete reached without a CSRF token")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, atrRouterReq(http.MethodPost, "/posts/empty-trash", `{"type":"post"}`, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("with CSRF: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if d, _, _ := atrDecodeEmpty(t, rec); d != 2 {
		t.Errorf("deleted = %d, want 2", d)
	}

	// POST /posts/{id} does not exist, so "empty-trash" cannot be read as an id.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, atrRouterReq(http.MethodPost, "/posts/7", `{}`, true))
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Errorf("POST /posts/7: status = %d, want 404 or 405", rec.Code)
	}
}

// --- (e) list status and session --------------------------------------------

func TestAdminTrashListAcceptsTrashStatus(t *testing.T) {
	var got content.AdminListFilter
	a := &fakeAdmin{list: func(_, _ int, f content.AdminListFilter) (content.AdminList, error) {
		got = f
		return content.AdminList{Page: 1}, nil
	}}
	s := testAdminServer(a)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?status=trash", nil).WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got.Status != "trash" {
		t.Errorf("filter Status = %q, want trash", got.Status)
	}
}

func TestAdminTrashListStillRejectsUnknownStatus(t *testing.T) {
	s := testAdminServer(&fakeAdmin{list: func(_, _ int, _ content.AdminListFilter) (content.AdminList, error) {
		return content.AdminList{}, nil
	}})
	for _, status := range []string{"bogus", "any", "trashed", "publish,trash"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?status="+status, nil).WithContext(principalCtx("edit_posts"))
		rec := httptest.NewRecorder()
		s.jsonHandler(s.adminPosts).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status=%s: code = %d, want 400", status, rec.Code)
			continue
		}
		assertJSONError(t, rec, "invalid_status")
	}
}

func TestAdminTrashSessionReportsTrashEnabled(t *testing.T) {
	cases := []struct {
		name    string
		trasher *atrTrasher
		want    bool
	}{
		{"enabled", &atrTrasher{enabled: true}, true},
		{"disabled", &atrTrasher{enabled: false}, false},
		{"not wired", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAdmin{displayName: func(int64) (string, error) { return "Ada", nil }}
			s := &Server{log: slog.Default(), admin: a}
			if tc.trasher != nil {
				s.postTrash = tc.trasher
			}
			req := httptest.NewRequest(http.MethodGet, "/admin/api/session", nil).WithContext(principalCtx("edit_posts"))
			rec := httptest.NewRecorder()
			s.jsonHandler(s.adminSession).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			v, ok := body["trashEnabled"]
			if !ok {
				t.Fatalf("session body has no trashEnabled: %v", body)
			}
			if v != tc.want {
				t.Errorf("trashEnabled = %v, want %v", v, tc.want)
			}
		})
	}
}
