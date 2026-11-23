package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// M22 task 4.1 -- RED tests for the REST trash transport (Req 7.1-7.10,
// 8.1-8.6, 14.2; D1; sign-off 1, 2).
//
// The write service and the trash service are fakes (rtrWriter, rtrTrasher) so
// the tests pin only what the handler does with them: which method it calls
// and how it maps the result. The post readers are real (SQLite), so the
// response bodies come through the production mapper. The collection listing
// is wrapped by rtrAdminPosts so the explicit Statuses reaching the store can
// be asserted.
//
// RED today: web.Server has no WithPostTrash (compile failure). Once stubbed,
// a non-force DELETE calls Delete (permanent), the forbidden code is
// rest_cannot_edit, an update with status "trash" answers 400, and the
// collection ignores ?status=.
//
// Existing tests that asserted the pre-M22 behavior were edited in the same
// change (sign-off 2): forbidden post deletes now expect rest_cannot_delete
// and every permanent delete in a pre-M22 test now says ?force=true.
//
// Media, user, comment and term DELETE (Req 7.9) are pinned by their own
// existing tests (rest_media_delete_test.go, rest_users_delete_test.go,
// rest_comments_test.go, rest_terms_test.go) and are not touched here.

// rtrWriter is a recording postAdminWriter.
type rtrWriter struct {
	updated   []domain.Post
	deleted   []int64
	deleteErr error
	updateErr error
}

func (w *rtrWriter) Create(_ context.Context, _ auth.Principal, _ domain.Post) (int64, error) {
	return 0, errors.New("rtrWriter: Create must not be reached")
}

func (w *rtrWriter) Update(_ context.Context, _ auth.Principal, p domain.Post, _ time.Time) error {
	w.updated = append(w.updated, p)
	return w.updateErr
}

func (w *rtrWriter) Delete(_ context.Context, _ auth.Principal, p domain.Post) error {
	w.deleted = append(w.deleted, p.ID)
	return w.deleteErr
}

// rtrTrasher is a recording postTrashWriter. A successful Trash flips the
// stored row to "trash" so the handler's re-read shows the trashed object.
type rtrTrasher struct {
	repo     domain.PostWriter
	trashed  []int64
	restored []int64
	err      error
	enabled  bool
}

func (f *rtrTrasher) Trash(ctx context.Context, _ auth.Principal, id int64) error {
	f.trashed = append(f.trashed, id)
	if f.err != nil {
		return f.err
	}
	p, err := f.repo.ByID(ctx, id)
	if err != nil {
		return err
	}
	p.Status = "trash"
	return f.repo.Update(ctx, p)
}

func (f *rtrTrasher) Restore(_ context.Context, _ auth.Principal, id int64, _ string) error {
	f.restored = append(f.restored, id)
	return f.err
}

func (f *rtrTrasher) TrashEnabled() bool { return f.enabled }

// rtrAdminPosts records the filters the collection handler sends.
type rtrAdminPosts struct {
	domain.AdminPostRepository
	lists  []domain.AdminPostFilter
	counts []domain.AdminPostFilter
}

func (r *rtrAdminPosts) ListForAdmin(ctx context.Context, f domain.AdminPostFilter) ([]domain.Post, error) {
	r.lists = append(r.lists, f)
	return r.AdminPostRepository.ListForAdmin(ctx, f)
}

func (r *rtrAdminPosts) CountForAdmin(ctx context.Context, f domain.AdminPostFilter) (int, error) {
	r.counts = append(r.counts, f)
	return r.AdminPostRepository.CountForAdmin(ctx, f)
}

type rtrEnv struct {
	repos   *storage.Repositories
	cfg     config.DatabaseConfig
	reg     *registry.Registry
	st      routing.Structure
	writer  *rtrWriter
	trasher *rtrTrasher
	admin   *rtrAdminPosts

	postID, pageID, productID int64 // live rows
	trashedPost               int64 // a row already in the trash
}

func rtrNewEnv(t *testing.T) *rtrEnv {
	t.Helper()
	ctx := context.Background()
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: filepath.Join(t.TempDir(), "grimoire.db"), TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })
	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	reg, err := registry.Build(rcpwConfig(), st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	types, taxes := reg.Routes()
	e := &rtrEnv{
		repos: repos, cfg: cfg, reg: reg, st: st.WithCustomTypes(types, taxes),
		writer:  &rtrWriter{},
		trasher: &rtrTrasher{repo: repos.PostWriter, enabled: true},
		admin:   &rtrAdminPosts{AdminPostRepository: repos.AdminPosts},
	}
	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	mk := func(typ, slug, status string) int64 {
		t.Helper()
		id, err := repos.PostWriter.Create(ctx, domain.Post{
			Author: 1, Date: base, Content: "<p>body " + slug + "</p>", Title: "T-" + slug,
			Status: status, Slug: slug, Type: typ, CommentStatus: "closed",
		})
		if err != nil {
			t.Fatalf("create %s/%s: %v", typ, slug, err)
		}
		return id
	}
	e.postID = mk("post", "rtr-live-post", "publish")
	e.pageID = mk("page", "rtr-live-page", "publish")
	e.productID = mk("product", "rtr-live-product", "publish")
	e.trashedPost = mk("post", "rtr-gone__trashed", "trash")
	mk("post", "rtr-gone-two__trashed", "trash")
	return e
}

// rtrCaps is the capability set of a full holder for posts, pages and the
// registered "product" type.
func rtrCaps() map[string]bool {
	caps := map[string]bool{}
	for k, v := range auth.CapabilitiesForRoles(auth.RoleAdministrator) {
		caps[k] = v
	}
	for _, c := range rcpwFullCaps {
		caps[c] = true
	}
	return caps
}

// rtrHandler builds the router. withTrash=false leaves the server's trash port
// nil (Req 14.2). caps nil means an anonymous caller.
func (e *rtrEnv) rtrHandler(t *testing.T, withTrash bool, caps map[string]bool) http.Handler {
	t.Helper()
	repos := e.repos
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, e.cfg.TablePrefix).
		WithPermalinks(e.st).WithRegistry(e.reg)
	fake := &fakeSessions{}
	if caps != nil {
		fake = &fakeSessions{
			authPrincipal: auth.Principal{UserID: 1, Login: "u", Roles: []string{"administrator"}, Caps: caps},
			authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: "tok"},
		}
	}
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithPermalinks(e.st).
		WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(e.writer, nil, nil, nil).
		WithREST(mapper, e.admin, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithRegistry(e.reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	if withTrash {
		srv = srv.WithPostTrash(e.trasher)
	}
	return srv.Routes()
}

func rtrDo(h http.Handler, method, path, body string, session bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "example.test"
	req.Header.Set("Content-Type", "application/json")
	if session {
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		req.Header.Set("X-CSRF-Token", "tok")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rtrObj(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return m
}

func rtrWantErr(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, status, rec.Body.String())
	}
	if got := rtrObj(t, rec)["code"]; got != code {
		t.Fatalf("code = %v, want %q (body=%s)", got, code, rec.Body.String())
	}
}

func rtrPath(base string, id int64) string {
	return "/wp-json/wp/v2/" + base + "/" + strconv.FormatInt(id, 10)
}

// Req 7.1: a non-force DELETE trashes and answers the post object.
func TestRESTTrashDeleteTrashesEachType(t *testing.T) {
	cases := []struct {
		name, base string
		id         func(*rtrEnv) int64
	}{
		{"post", "posts", func(e *rtrEnv) int64 { return e.postID }},
		{"page", "pages", func(e *rtrEnv) int64 { return e.pageID }},
		{"registered type", "products", func(e *rtrEnv) int64 { return e.productID }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := rtrNewEnv(t)
			h := e.rtrHandler(t, true, rtrCaps())
			id := tc.id(e)
			rec := rtrDo(h, http.MethodDelete, rtrPath(tc.base, id), "", true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
			}
			got := rtrObj(t, rec)
			if _, has := got["deleted"]; has {
				t.Errorf("trash response carries a deleted key: %v", got)
			}
			if got["status"] != "trash" {
				t.Errorf("status = %v, want trash", got["status"])
			}
			if gotID, _ := got["id"].(float64); int64(gotID) != id {
				t.Errorf("id = %v, want %d", got["id"], id)
			}
			if !slices.Equal(e.trasher.trashed, []int64{id}) {
				t.Errorf("Trash calls = %v, want [%d]", e.trasher.trashed, id)
			}
			if len(e.writer.deleted) != 0 {
				t.Errorf("a non-force DELETE called Delete: %v", e.writer.deleted)
			}
		})
	}
}

// Req 7.2, 7.7: force=true and force=1 run the permanent delete and answer
// {deleted, previous}; force on an already-trashed row deletes it.
func TestRESTTrashDeleteForceDeletes(t *testing.T) {
	cases := []struct {
		name, base, query string
		id                func(*rtrEnv) int64
		prevStatus        string
	}{
		{"post force=true", "posts", "?force=true", func(e *rtrEnv) int64 { return e.postID }, "publish"},
		{"post force=1", "posts", "?force=1", func(e *rtrEnv) int64 { return e.postID }, "publish"},
		{"page force=true", "pages", "?force=true", func(e *rtrEnv) int64 { return e.pageID }, "publish"},
		{"registered type force=true", "products", "?force=true", func(e *rtrEnv) int64 { return e.productID }, "publish"},
		{"trashed row force=true", "posts", "?force=true", func(e *rtrEnv) int64 { return e.trashedPost }, "trash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := rtrNewEnv(t)
			h := e.rtrHandler(t, true, rtrCaps())
			id := tc.id(e)
			rec := rtrDo(h, http.MethodDelete, rtrPath(tc.base, id)+tc.query, "", true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
			}
			got := rtrObj(t, rec)
			if got["deleted"] != true {
				t.Errorf("deleted = %v, want true", got["deleted"])
			}
			prev, _ := got["previous"].(map[string]any)
			if prev == nil || prev["status"] != tc.prevStatus {
				t.Errorf("previous = %v, want the object with status %q", got["previous"], tc.prevStatus)
			}
			if !slices.Equal(e.writer.deleted, []int64{id}) {
				t.Errorf("Delete calls = %v, want [%d]", e.writer.deleted, id)
			}
			if len(e.trasher.trashed) != 0 {
				t.Errorf("a force DELETE called Trash: %v", e.trasher.trashed)
			}
		})
	}
}

// Req 7.3-7.6, 14.2: error mapping on both paths. ErrForbidden is checked
// before the 501 and 410 errors, so a joined error maps to 403.
func TestRESTTrashDeleteErrors(t *testing.T) {
	cases := []struct {
		name   string
		force  bool
		err    error
		status int
		code   string
	}{
		{"already trashed", false, domain.ErrAlreadyTrashed, http.StatusGone, "rest_already_trashed"},
		{"trash unsupported", false, content.ErrTrashUnsupported, http.StatusNotImplemented, "rest_trash_not_supported"},
		{"forbidden", false, content.ErrForbidden, http.StatusForbidden, "rest_cannot_delete"},
		{"forbidden before unsupported", false, errors.Join(content.ErrForbidden, content.ErrTrashUnsupported), http.StatusForbidden, "rest_cannot_delete"},
		{"forbidden before already trashed", false, errors.Join(content.ErrForbidden, domain.ErrAlreadyTrashed), http.StatusForbidden, "rest_cannot_delete"},
		{"other", false, errors.New("boom"), http.StatusInternalServerError, "rest_delete_failed"},
		{"force forbidden", true, content.ErrForbidden, http.StatusForbidden, "rest_cannot_delete"},
		{"force other", true, errors.New("boom"), http.StatusInternalServerError, "rest_delete_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := rtrNewEnv(t)
			if tc.force {
				e.writer.deleteErr = tc.err
			} else {
				e.trasher.err = tc.err
			}
			h := e.rtrHandler(t, true, rtrCaps())
			q := ""
			if tc.force {
				q = "?force=true"
			}
			rec := rtrDo(h, http.MethodDelete, rtrPath("posts", e.postID)+q, "", true)
			rtrWantErr(t, rec, tc.status, tc.code)
			msg, _ := rtrObj(t, rec)["message"].(string)
			switch tc.code {
			case "rest_already_trashed":
				if msg != "The post has already been deleted." {
					t.Errorf("message = %q", msg)
				}
			case "rest_trash_not_supported":
				if msg != "The post does not support trashing. Set 'force=true' to delete." {
					t.Errorf("message = %q", msg)
				}
			}
		})
	}
}

// Req 7.6: unknown id and wrong type answer 404 on both paths; nothing is
// called.
func TestRESTTrashDeleteUnknownAndWrongType404(t *testing.T) {
	e := rtrNewEnv(t)
	h := e.rtrHandler(t, true, rtrCaps())
	for _, q := range []string{"", "?force=true"} {
		rtrWantErr(t, rtrDo(h, http.MethodDelete, rtrPath("posts", 999999)+q, "", true), http.StatusNotFound, "rest_post_invalid_id")
		rtrWantErr(t, rtrDo(h, http.MethodDelete, rtrPath("posts", e.pageID)+q, "", true), http.StatusNotFound, "rest_post_invalid_id")
	}
	if len(e.trasher.trashed) != 0 || len(e.writer.deleted) != 0 {
		t.Errorf("a 404 reached a service: trashed=%v deleted=%v", e.trasher.trashed, e.writer.deleted)
	}
}

// Req 14.2: with no trash port, a non-force DELETE answers 501 and deletes
// nothing; force still deletes.
func TestRESTTrashDeleteNilTrashPortIs501AndDeletesNothing(t *testing.T) {
	e := rtrNewEnv(t)
	h := e.rtrHandler(t, false, rtrCaps())
	rtrWantErr(t, rtrDo(h, http.MethodDelete, rtrPath("posts", e.postID), "", true), http.StatusNotImplemented, "rest_trash_not_supported")
	rtrWantErr(t, rtrDo(h, http.MethodDelete, rtrPath("products", e.productID), "", true), http.StatusNotImplemented, "rest_trash_not_supported")
	if len(e.writer.deleted) != 0 || len(e.trasher.trashed) != 0 {
		t.Fatalf("a non-force DELETE with no trash port reached a service: deleted=%v trashed=%v", e.writer.deleted, e.trasher.trashed)
	}
	if _, err := e.repos.PostWriter.ByID(context.Background(), e.postID); err != nil {
		t.Errorf("row gone after a refused DELETE: %v", err)
	}
	if rec := rtrDo(h, http.MethodDelete, rtrPath("posts", e.postID)+"?force=true", "", true); rec.Code != http.StatusOK {
		t.Errorf("force with no trash port = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

// Req 7.8: PUT/PATCH with status "trash" on a live row and any other status on
// a trashed row reach the service; POST with status "trash" is rejected; a
// sparse PATCH on a trashed row keeps it trashed.
func TestRESTTrashUpdateStatusReachesService(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		t.Run(method+" trash a live row", func(t *testing.T) {
			e := rtrNewEnv(t)
			h := e.rtrHandler(t, true, rtrCaps())
			rec := rtrDo(h, method, rtrPath("posts", e.postID), `{"title":"T","status":"trash"}`, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
			}
			if len(e.writer.updated) != 1 || e.writer.updated[0].ID != e.postID || e.writer.updated[0].Status != "trash" {
				t.Fatalf("Update calls = %+v, want one for %d with status trash", e.writer.updated, e.postID)
			}
		})
		t.Run(method+" restore a trashed row", func(t *testing.T) {
			e := rtrNewEnv(t)
			h := e.rtrHandler(t, true, rtrCaps())
			rec := rtrDo(h, method, rtrPath("posts", e.trashedPost), `{"title":"T","status":"draft"}`, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
			}
			if len(e.writer.updated) != 1 || e.writer.updated[0].Status != "draft" {
				t.Fatalf("Update calls = %+v, want one with status draft", e.writer.updated)
			}
		})
	}
	t.Run("registered type", func(t *testing.T) {
		e := rtrNewEnv(t)
		h := e.rtrHandler(t, true, rtrCaps())
		rec := rtrDo(h, http.MethodPatch, rtrPath("products", e.productID), `{"title":"T","status":"trash"}`, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if len(e.writer.updated) != 1 || e.writer.updated[0].Status != "trash" {
			t.Fatalf("Update calls = %+v, want one with status trash", e.writer.updated)
		}
	})
	t.Run("sparse PATCH keeps a trashed row trashed", func(t *testing.T) {
		e := rtrNewEnv(t)
		h := e.rtrHandler(t, true, rtrCaps())
		rec := rtrDo(h, http.MethodPatch, rtrPath("posts", e.trashedPost), `{"title":"Renamed"}`, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if len(e.writer.updated) != 1 || e.writer.updated[0].Status != "trash" || e.writer.updated[0].Title != "Renamed" {
			t.Fatalf("Update calls = %+v, want one keeping status trash with the new title", e.writer.updated)
		}
	})
	t.Run("POST with status trash is rejected", func(t *testing.T) {
		e := rtrNewEnv(t)
		h := e.rtrHandler(t, true, rtrCaps())
		rec := rtrDo(h, http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"New","status":"trash"}`, true)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

// Req 8.1: a trashed row is readable by an editor and 404 for everyone else.
func TestRESTTrashSingleGetTrashedRow(t *testing.T) {
	e := rtrNewEnv(t)
	rec := rtrDo(e.rtrHandler(t, true, rtrCaps()), http.MethodGet, rtrPath("posts", e.trashedPost), "", true)
	if rec.Code != http.StatusOK || rtrObj(t, rec)["status"] != "trash" {
		t.Fatalf("editor GET = %d %s, want 200 with status trash", rec.Code, rec.Body.String())
	}
	rtrWantErr(t, rtrDo(e.rtrHandler(t, true, nil), http.MethodGet, rtrPath("posts", e.trashedPost), "", false), http.StatusNotFound, "rest_post_invalid_id")
}

// Req 8.2-8.5: the status parameter, through the collection handler. The
// statuses reach ListForAdmin and CountForAdmin as explicit values.
func TestRESTStatusesParse(t *testing.T) {
	live := []string{"publish", "future", "draft", "pending", "private"}
	cases := []struct {
		query string
		want  []string
	}{
		{"", []string{"publish"}},
		{"?status=publish", []string{"publish"}},
		{"?status=any", live},
		{"?status=trash", []string{"trash"}},
		{"?status=draft,pending", []string{"draft", "pending"}},
		{"?status=publish,trash", []string{"publish", "trash"}},
	}
	for _, tc := range cases {
		t.Run("editor "+tc.query, func(t *testing.T) {
			e := rtrNewEnv(t)
			h := e.rtrHandler(t, true, rtrCaps())
			rec := rtrDo(h, http.MethodGet, "/wp-json/wp/v2/posts"+tc.query, "", true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
			}
			if len(e.admin.lists) != 1 || !slices.Equal(e.admin.lists[0].Statuses, tc.want) {
				t.Errorf("ListForAdmin filters = %+v, want Statuses %v", e.admin.lists, tc.want)
			}
			if len(e.admin.counts) != 1 || !slices.Equal(e.admin.counts[0].Statuses, tc.want) {
				t.Errorf("CountForAdmin filters = %+v, want Statuses %v", e.admin.counts, tc.want)
			}
		})
	}
	t.Run("registered type", func(t *testing.T) {
		e := rtrNewEnv(t)
		h := e.rtrHandler(t, true, rtrCaps())
		rec := rtrDo(h, http.MethodGet, "/wp-json/wp/v2/products?status=trash", "", true)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if len(e.admin.lists) != 1 || !slices.Equal(e.admin.lists[0].Statuses, []string{"trash"}) {
			t.Errorf("ListForAdmin filters = %+v, want Statuses [trash]", e.admin.lists)
		}
	})
	t.Run("unknown token is 400", func(t *testing.T) {
		e := rtrNewEnv(t)
		h := e.rtrHandler(t, true, rtrCaps())
		rtrWantErr(t, rtrDo(h, http.MethodGet, "/wp-json/wp/v2/posts?status=bogus", "", true), http.StatusBadRequest, "rest_invalid_param")
		rtrWantErr(t, rtrDo(h, http.MethodGet, "/wp-json/wp/v2/posts?status=draft,bogus", "", true), http.StatusBadRequest, "rest_invalid_param")
	})
	t.Run("caller without the edit capability", func(t *testing.T) {
		for _, q := range []string{"?status=trash", "?status=draft", "?status=any", "?status=publish,draft"} {
			e := rtrNewEnv(t)
			h := e.rtrHandler(t, true, nil)
			rtrWantErr(t, rtrDo(h, http.MethodGet, "/wp-json/wp/v2/posts"+q, "", false), http.StatusForbidden, "rest_forbidden_status")
			if len(e.admin.lists) != 0 {
				t.Errorf("%s: a forbidden status listed rows: %+v", q, e.admin.lists)
			}
		}
		e := rtrNewEnv(t)
		h := e.rtrHandler(t, true, nil)
		if rec := rtrDo(h, http.MethodGet, "/wp-json/wp/v2/posts?status=publish", "", false); rec.Code != http.StatusOK {
			t.Errorf("anonymous status=publish = %d, want 200", rec.Code)
		}
	})
}

// Req 8.6: the headers come from the same status set as the listing.
func TestRESTStatusesTotalsAgreeWithListing(t *testing.T) {
	e := rtrNewEnv(t)
	h := e.rtrHandler(t, true, rtrCaps())
	rec := rtrDo(h, http.MethodGet, "/wp-json/wp/v2/posts?status=trash&per_page=1", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-WP-Total"); got != "2" {
		t.Errorf("X-WP-Total = %q, want 2 (two trashed posts)", got)
	}
	if got := rec.Header().Get("X-WP-TotalPages"); got != "2" {
		t.Errorf("X-WP-TotalPages = %q, want 2", got)
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 1 || items[0]["status"] != "trash" {
		t.Errorf("items = %v, want one trashed post", items)
	}
}
