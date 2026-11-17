package web_test

import (
	"context"
	"encoding/json"
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

// Plan 18 Phase 12.1 -- RED tests for `parent` on REST page writes
// (Req 11.1-11.7). Helper prefix: rppp.
//
// The post-write service is a recording spy in front of the real
// content.PostWriteService, so the handler's output (the domain.Post it hands
// the service) is observable and errors can be injected per case.
//
// RED today: parseRESTPostWrite honours `parent` only for registered custom
// types, so a page create/update hands the service the stored parent (0 on
// create) instead of the requested one.
type ropppSpy struct {
	inner      *content.PostWriteService
	createErr  error
	updateErr  error
	lastCreate domain.Post
	lastUpdate domain.Post
}

func (s *ropppSpy) Create(ctx context.Context, a auth.Principal, p domain.Post) (int64, error) {
	s.lastCreate = p
	if s.createErr != nil {
		return 0, s.createErr
	}
	return s.inner.Create(ctx, a, p)
}

func (s *ropppSpy) Update(ctx context.Context, a auth.Principal, p domain.Post, exp time.Time) error {
	s.lastUpdate = p
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.inner.Update(ctx, a, p, exp)
}

func (s *ropppSpy) Delete(ctx context.Context, a auth.Principal, p domain.Post) error {
	return s.inner.Delete(ctx, a, p)
}

func newRPPPRouter(t *testing.T) (http.Handler, *storage.Repositories, *auth.ApplicationPasswords, *ropppSpy) {
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
	caps, err := auth.SerializeCapabilities(auth.RoleAdministrator)
	if err != nil {
		t.Fatalf("SerializeCapabilities: %v", err)
	}
	if err := repos.UserMeta.Set(ctx, 1, cfg.TablePrefix+"capabilities", caps); err != nil {
		t.Fatalf("seed capabilities: %v", err)
	}
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, cfg.TablePrefix)
	ap := &auth.ApplicationPasswords{Users: repos.Users, Meta: repos.UserMeta, Prefix: cfg.TablePrefix}
	spy := &ropppSpy{inner: content.NewPostWriteService(repos.PostWriter, content.WithPostNodeReader(repos.PostNodeReader))}
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(&fakeSessions{}, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(spy, nil, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(ap, false, "")
	return srv.Routes(), repos, ap, spy
}

func ropppMake(t *testing.T, repos *storage.Repositories, typ, slug string, parent int64) int64 {
	t.Helper()
	id, err := repos.PostWriter.Create(context.Background(), domain.Post{
		Author: 1, Date: time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), Content: "<p>x</p>",
		Title: "T-" + slug, Status: "publish", Slug: slug, Type: typ, CommentStatus: "closed", ParentID: parent,
	})
	if err != nil {
		t.Fatalf("create %s/%s: %v", typ, slug, err)
	}
	return id
}

func ropppObj(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	return m
}

func ropppWantCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, status, rec.Body.String())
	}
	if code != "" {
		if got, _ := ropppObj(t, rec)["code"].(string); got != code {
			t.Fatalf("code = %q, want %q", got, code)
		}
	}
}

func ropppItoa(id int64) string { return strconv.FormatInt(id, 10) }

// Req 11.1: POST pages with parent passes it to Create and echoes it.
func TestRESTPagesParentCreatePassesParent(t *testing.T) {
	h, repos, ap, spy := newRPPPRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	parent := ropppMake(t, repos, "page", "rppp-top", 0)
	rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/pages", secret,
		`{"title":"Kid","slug":"rppp-kid","status":"draft","parent":`+ropppItoa(parent)+`}`, nil)
	ropppWantCode(t, rec, http.StatusCreated, "")
	if spy.lastCreate.ParentID != parent {
		t.Errorf("Create got ParentID %d, want %d", spy.lastCreate.ParentID, parent)
	}
	if p, _ := ropppObj(t, rec)["parent"].(float64); int64(p) != parent {
		t.Errorf("response parent = %v, want %d", p, parent)
	}
}

// Req 11.2: PUT and PATCH with parent pass it to Update.
func TestRESTPagesParentUpdatePassesParent(t *testing.T) {
	h, repos, ap, spy := newRPPPRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	parent := ropppMake(t, repos, "page", "rppp-top", 0)
	for _, m := range []string{http.MethodPut, http.MethodPatch} {
		id := ropppMake(t, repos, "page", "rppp-"+m, 0)
		rec := doRESTWrite(t, h, m, "/wp-json/wp/v2/pages/"+ropppItoa(id), secret,
			`{"parent":`+ropppItoa(parent)+`}`, nil)
		ropppWantCode(t, rec, http.StatusOK, "")
		if spy.lastUpdate.ParentID != parent {
			t.Errorf("%s: Update got ParentID %d, want %d", m, spy.lastUpdate.ParentID, parent)
		}
	}
}

// Req 11.3, 11.4: omitted parent passes the stored value; parent 0 passes 0.
func TestRESTPagesParentOmittedKeepsZeroClears(t *testing.T) {
	h, repos, ap, spy := newRPPPRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	parent := ropppMake(t, repos, "page", "rppp-top", 0)
	id := ropppMake(t, repos, "page", "rppp-kid", parent)
	path := "/wp-json/wp/v2/pages/" + ropppItoa(id)
	rec := doRESTWrite(t, h, http.MethodPatch, path, secret, `{"title":"Renamed"}`, nil)
	ropppWantCode(t, rec, http.StatusOK, "")
	if spy.lastUpdate.ParentID != parent {
		t.Errorf("omitted parent: Update got %d, want stored %d", spy.lastUpdate.ParentID, parent)
	}
	rec = doRESTWrite(t, h, http.MethodPatch, path, secret, `{"parent":0}`, nil)
	ropppWantCode(t, rec, http.StatusOK, "")
	if spy.lastUpdate.ParentID != 0 {
		t.Errorf("parent:0: Update got %d, want 0", spy.lastUpdate.ParentID)
	}
}

// Req 11.5: the service's parent sentinels map to 400 rest_invalid_param.
func TestRESTPagesParentSentinelsMapTo400(t *testing.T) {
	h, repos, ap, spy := newRPPPRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	id := ropppMake(t, repos, "page", "rppp-kid", 0)
	for _, e := range []error{content.ErrPostParentInvalid, content.ErrPostParentCycle} {
		spy.createErr, spy.updateErr = e, e
		rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/pages", secret,
			`{"title":"X","status":"draft","parent":999999}`, nil)
		ropppWantCode(t, rec, http.StatusBadRequest, "rest_invalid_param")
		rec = doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/pages/"+ropppItoa(id), secret,
			`{"parent":999999}`, nil)
		ropppWantCode(t, rec, http.StatusBadRequest, "rest_invalid_param")
	}
}

// Req 11.6: ErrForbidden maps to 403 rest_cannot_edit on a page update.
func TestRESTPagesParentForbiddenMapsTo403(t *testing.T) {
	h, repos, ap, spy := newRPPPRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	parent := ropppMake(t, repos, "page", "rppp-top", 0)
	id := ropppMake(t, repos, "page", "rppp-kid", 0)
	spy.updateErr = content.ErrForbidden
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/pages/"+ropppItoa(id), secret,
		`{"parent":`+ropppItoa(parent)+`}`, nil)
	ropppWantCode(t, rec, http.StatusForbidden, "rest_cannot_edit")
}

// Req 11.7: `parent` on /posts is ignored on create and update.
func TestRESTPagesParentIgnoredOnPosts(t *testing.T) {
	h, repos, ap, spy := newRPPPRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	page := ropppMake(t, repos, "page", "rppp-top", 0)
	rec := doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/posts", secret,
		`{"title":"P","status":"draft","parent":`+ropppItoa(page)+`}`, nil)
	ropppWantCode(t, rec, http.StatusCreated, "")
	if spy.lastCreate.ParentID != 0 {
		t.Errorf("post Create got ParentID %d, want 0", spy.lastCreate.ParentID)
	}
	post := ropppMake(t, repos, "post", "rppp-post", 0)
	rec = doRESTWrite(t, h, http.MethodPatch, "/wp-json/wp/v2/posts/"+ropppItoa(post), secret,
		`{"parent":`+ropppItoa(page)+`}`, nil)
	ropppWantCode(t, rec, http.StatusOK, "")
	if spy.lastUpdate.ParentID != 0 {
		t.Errorf("post Update got ParentID %d, want 0", spy.lastUpdate.ParentID)
	}
}

// M19 Req 6.1 (RED until task 6.2): every REST page response carries the key
// `parent`, including 0 for a top-level page.
func ropppWantParent(t *testing.T, obj map[string]any, want int64, where string) {
	t.Helper()
	v, ok := obj["parent"]
	if !ok {
		t.Fatalf("%s: key parent absent, want %d", where, want)
	}
	if f, _ := v.(float64); int64(f) != want {
		t.Fatalf("%s: parent = %v, want %d", where, v, want)
	}
}

func TestRESTPageParentAlwaysPresent(t *testing.T) {
	h, repos, ap, _ := newRPPPRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	top := ropppMake(t, repos, "page", "rppk-top", 0)
	kid := ropppMake(t, repos, "page", "rppk-kid", top)

	rec := doRESTWrite(t, h, http.MethodGet, "/wp-json/wp/v2/pages/"+ropppItoa(top), secret, "", nil)
	ropppWantCode(t, rec, http.StatusOK, "")
	ropppWantParent(t, ropppObj(t, rec), 0, "GET /pages/{top}")

	rec = doRESTWrite(t, h, http.MethodGet, "/wp-json/wp/v2/pages/"+ropppItoa(kid), secret, "", nil)
	ropppWantCode(t, rec, http.StatusOK, "")
	ropppWantParent(t, ropppObj(t, rec), top, "GET /pages/{kid}")

	rec = doRESTWrite(t, h, http.MethodGet, "/wp-json/wp/v2/pages", secret, "", nil)
	ropppWantCode(t, rec, http.StatusOK, "")
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) == 0 {
		t.Fatalf("list body: %v (%s)", err, rec.Body.String())
	}
	for _, it := range list {
		if _, ok := it["parent"]; !ok {
			t.Fatalf("GET /pages item %v lacks parent", it["id"])
		}
	}

	rec = doRESTWrite(t, h, http.MethodPost, "/wp-json/wp/v2/pages", secret,
		`{"title":"Top","slug":"rppk-new","status":"draft"}`, nil)
	ropppWantCode(t, rec, http.StatusCreated, "")
	ropppWantParent(t, ropppObj(t, rec), 0, "POST /pages 201")

	rec = doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/pages/"+ropppItoa(top), secret,
		`{"title":"Renamed"}`, nil)
	ropppWantCode(t, rec, http.StatusOK, "")
	ropppWantParent(t, ropppObj(t, rec), 0, "PUT /pages/{top} 200")
}

// M19 Req 7.1 pin (passes today, must keep passing): a post item has no parent.
func TestRESTPostParentPins(t *testing.T) {
	h, repos, ap, _ := newRPPPRouter(t)
	secret := mintAppPassword(t, context.Background(), ap)
	post := ropppMake(t, repos, "post", "rppk-post", 0)
	rec := doRESTWrite(t, h, http.MethodGet, "/wp-json/wp/v2/posts/"+ropppItoa(post), secret, "", nil)
	ropppWantCode(t, rec, http.StatusOK, "")
	if v, ok := ropppObj(t, rec)["parent"]; ok {
		t.Fatalf("post item carries parent = %v, want key absent", v)
	}
}
