package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// Plan 18 Phase 12.1 -- RED tests for `parent` on admin API page writes
// (Req 12.1-12.5). Helper prefix: aapg.
//
// A recording spy sits in front of the real content.PostWriteService so the
// domain.Post the handler passes is observable and errors can be injected.
//
// RED today: parsePostWrite ignores `parent` for built-in types, so a page
// write hands the service the stored parent (0 on create), and postDetail
// never serializes a page's parent.
type aapgSpy struct {
	inner      *content.PostWriteService
	createErr  error
	updateErr  error
	lastCreate domain.Post
	lastUpdate domain.Post
}

func (s *aapgSpy) Create(ctx context.Context, a auth.Principal, p domain.Post) (int64, error) {
	s.lastCreate = p
	if s.createErr != nil {
		return 0, s.createErr
	}
	return s.inner.Create(ctx, a, p)
}

func (s *aapgSpy) Update(ctx context.Context, a auth.Principal, p domain.Post, exp time.Time) error {
	s.lastUpdate = p
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.inner.Update(ctx, a, p, exp)
}

func (s *aapgSpy) Delete(ctx context.Context, a auth.Principal, p domain.Post) error {
	return s.inner.Delete(ctx, a, p)
}

func newAAPGRouter(t *testing.T) (http.Handler, *storage.Repositories, *aapgSpy) {
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
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	spy := &aapgSpy{inner: content.NewPostWriteService(repos.PostWriter, content.WithPostNodeReader(repos.PostNodeReader))}
	sessions := &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "aapg-user", []string{auth.RoleEditor}),
		authSession:   domain.Session{ID: "aapg-s", UserID: 1, CSRFToken: apcCSRF},
	}
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(sessions, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(spy, nil, nil, nil)
	return srv.Routes(), repos, spy
}

func aapgMake(t *testing.T, repos *storage.Repositories, typ, slug string, parent int64) int64 {
	t.Helper()
	id, err := repos.PostWriter.Create(context.Background(), domain.Post{
		Author: 1, Date: time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), Content: "<p>x</p>",
		Title: "T-" + slug, Status: "draft", Slug: slug, Type: typ, CommentStatus: "closed", ParentID: parent,
	})
	if err != nil {
		t.Fatalf("create %s/%s: %v", typ, slug, err)
	}
	return id
}

func aapgItoa(id int64) string { return strconv.FormatInt(id, 10) }

func aapgGet(t *testing.T, h http.Handler, id int64) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/"+aapgItoa(id), nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "session-token"})
	h.ServeHTTP(rec, req)
	apcWant(t, rec, http.StatusOK)
	return apcDecode(t, rec)
}

func aapgPut(t *testing.T, h http.Handler, id int64, fields string) *httptest.ResponseRecorder {
	t.Helper()
	mod, _ := aapgGet(t, h, id)["modified"].(string)
	body := `{"title":"Edited","status":"draft",` + fields + `"modified":"` + mod + `"}`
	return apcDo(t, h, http.MethodPut, "/admin/api/posts/"+aapgItoa(id), body, true)
}

func aapgErrMessage(t *testing.T, rec *httptest.ResponseRecorder) (code, msg string) {
	t.Helper()
	e, _ := apcDecode(t, rec)["error"].(map[string]any)
	code, _ = e["code"].(string)
	msg, _ = e["message"].(string)
	return code, msg
}

// Req 12.1: POST and PUT with type page and parent pass it to the service.
func TestAdminPagesParentCreateAndUpdatePassParent(t *testing.T) {
	h, repos, spy := newAAPGRouter(t)
	parent := aapgMake(t, repos, "page", "aapg-top", 0)
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts",
		`{"title":"Kid","type":"page","status":"draft","parent":`+aapgItoa(parent)+`}`, true)
	apcWant(t, rec, http.StatusCreated)
	if spy.lastCreate.ParentID != parent {
		t.Errorf("Create got ParentID %d, want %d", spy.lastCreate.ParentID, parent)
	}
	id := aapgMake(t, repos, "page", "aapg-kid", 0)
	rec = aapgPut(t, h, id, `"type":"page","parent":`+aapgItoa(parent)+`,`)
	apcWant(t, rec, http.StatusOK)
	if spy.lastUpdate.ParentID != parent {
		t.Errorf("Update got ParentID %d, want %d", spy.lastUpdate.ParentID, parent)
	}
}

// Req 12.2: a PUT for a stored page whose body omits `type` and carries
// `parent` still passes it (the effective type is the stored row's).
func TestAdminPagesParentUpdateWithoutTypeUsesStoredType(t *testing.T) {
	h, repos, spy := newAAPGRouter(t)
	parent := aapgMake(t, repos, "page", "aapg-top", 0)
	id := aapgMake(t, repos, "page", "aapg-kid", 0)
	rec := aapgPut(t, h, id, `"parent":`+aapgItoa(parent)+`,`)
	apcWant(t, rec, http.StatusOK)
	if spy.lastUpdate.ParentID != parent {
		t.Errorf("Update got ParentID %d, want %d", spy.lastUpdate.ParentID, parent)
	}
}

// Req 12.3: the page detail carries `parent`, omitted when 0.
func TestAdminPagesParentDetailCarriesParentOmittedWhenZero(t *testing.T) {
	h, repos, _ := newAAPGRouter(t)
	parent := aapgMake(t, repos, "page", "aapg-top", 0)
	kid := aapgMake(t, repos, "page", "aapg-kid", parent)
	if p, _ := aapgGet(t, h, kid)["parent"].(float64); int64(p) != parent {
		t.Errorf("child page detail parent = %v, want %d", p, parent)
	}
	if _, has := aapgGet(t, h, parent)["parent"]; has {
		t.Error("top-level page detail carries `parent`; want it omitted when 0")
	}
}

// Req 12.4: the sentinels map to 400 bad_request with "invalid parent".
func TestAdminPagesParentSentinelsMapTo400(t *testing.T) {
	h, repos, spy := newAAPGRouter(t)
	id := aapgMake(t, repos, "page", "aapg-kid", 0)
	for _, e := range []error{content.ErrPostParentInvalid, content.ErrPostParentCycle} {
		spy.createErr, spy.updateErr = e, e
		rec := apcDo(t, h, http.MethodPost, "/admin/api/posts",
			`{"title":"X","type":"page","status":"draft","parent":999999}`, true)
		apcWant(t, rec, http.StatusBadRequest)
		if c, m := aapgErrMessage(t, rec); c != "bad_request" || m != "invalid parent" {
			t.Errorf("create: error = %q/%q, want bad_request/invalid parent", c, m)
		}
		rec = aapgPut(t, h, id, `"type":"page","parent":999999,`)
		apcWant(t, rec, http.StatusBadRequest)
		if c, m := aapgErrMessage(t, rec); c != "bad_request" || m != "invalid parent" {
			t.Errorf("update: error = %q/%q, want bad_request/invalid parent", c, m)
		}
	}
}

// Req 12.5: `type: post` ignores `parent`.
func TestAdminPagesParentIgnoredForPostType(t *testing.T) {
	h, repos, spy := newAAPGRouter(t)
	page := aapgMake(t, repos, "page", "aapg-top", 0)
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts",
		`{"title":"P","type":"post","status":"draft","parent":`+aapgItoa(page)+`}`, true)
	apcWant(t, rec, http.StatusCreated)
	if spy.lastCreate.ParentID != 0 {
		t.Errorf("post Create got ParentID %d, want 0", spy.lastCreate.ParentID)
	}
	post := aapgMake(t, repos, "post", "aapg-post", 0)
	rec = aapgPut(t, h, post, `"type":"post","parent":`+aapgItoa(page)+`,`)
	apcWant(t, rec, http.StatusOK)
	if spy.lastUpdate.ParentID != 0 {
		t.Errorf("post Update got ParentID %d, want 0", spy.lastUpdate.ParentID)
	}
}
