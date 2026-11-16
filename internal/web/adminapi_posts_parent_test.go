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
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// M12c Phase 14.1 -- RED tests for `parent` on the admin JSON API
// (/admin/api/posts) (Req 11.1, 11.5; D7). Helper prefix: aapp.
//
// Registry: product (flat), guide (hierarchical). RED today: `parent` is not
// accepted or serialized by the admin API.

func aappSessions() *fakeSessions {
	keys := []string{auth.RoleEditor}
	for _, typ := range []string{"products", "guides"} {
		for _, p := range []string{"edit_", "edit_others_", "edit_published_", "publish_", "delete_", "delete_others_", "delete_published_"} {
			keys = append(keys, p+typ)
		}
	}
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "aapp-user", keys),
		authSession:   domain.Session{ID: "aapp-s", UserID: 1, CSRFToken: apcCSRF},
	}
}

func newAAPPRouter(t *testing.T) (http.Handler, *storage.Repositories) {
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
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	yes := true
	reg, err := registry.Build(config.ContentConfig{PostTypes: []config.PostTypeConfig{
		{Name: "product", Public: &yes, Slug: "product", RESTBase: "products", CapabilityType: "product"},
		{Name: "guide", Public: &yes, Slug: "guide", RESTBase: "guides", Hierarchical: true, CapabilityType: "guide"},
	}}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	postWrite := content.NewPostWriteService(repos.PostWriter,
		content.WithPostRegistry(reg), content.WithPostNodeReader(repos.PostNodeReader))
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(aappSessions(), web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(postWrite, nil, nil, nil).
		WithRegistry(reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	return srv.Routes(), repos
}

func aappCreate(t *testing.T, h http.Handler, body string) map[string]any {
	t.Helper()
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts", body, true)
	apcWant(t, rec, http.StatusCreated)
	return apcDecode(t, rec)
}

func aappStoredParent(t *testing.T, repos *storage.Repositories, id int64) int64 {
	t.Helper()
	var p int64
	if err := repos.DB().QueryRowContext(context.Background(),
		"SELECT post_parent FROM wp_posts WHERE ID = ?", id).Scan(&p); err != nil {
		t.Fatalf("read post_parent(%d): %v", id, err)
	}
	return p
}

func aappGet(t *testing.T, h http.Handler, id int64) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(id, 10), nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "session-token"})
	h.ServeHTTP(rec, req)
	apcWant(t, rec, http.StatusOK)
	return apcDecode(t, rec)
}

// Req 11.1: create a guide with a parent; stored and echoed in the detail.
func TestAdminPostParentCreate(t *testing.T) {
	h, repos := newAAPPRouter(t)
	top := aappCreate(t, h, `{"title":"Top","type":"guide","status":"draft"}`)
	topID := apcID(t, top)
	body := `{"title":"Sub","type":"guide","status":"draft","parent":` + strconv.FormatInt(topID, 10) + `}`
	m := aappCreate(t, h, body)
	if p, _ := m["parent"].(float64); int64(p) != topID {
		t.Fatalf("create response parent = %v, want %d", m["parent"], topID)
	}
	id := apcID(t, m)
	if got := aappStoredParent(t, repos, id); got != topID {
		t.Fatalf("stored parent = %d, want %d", got, topID)
	}
	if p, _ := aappGet(t, h, id)["parent"].(float64); int64(p) != topID {
		t.Fatalf("detail parent = %v, want %d", p, topID)
	}
}

// Req 11.5, 11.1: PUT with parent omitted keeps the stored parent; an
// explicit parent 0 makes the row top-level.
func TestAdminPostParentUpdateOmittedKeepsThenZeroClears(t *testing.T) {
	h, repos := newAAPPRouter(t)
	topID := apcID(t, aappCreate(t, h, `{"title":"Top","type":"guide","status":"draft"}`))
	m := aappCreate(t, h, `{"title":"Sub","type":"guide","status":"draft","parent":`+strconv.FormatInt(topID, 10)+`}`)
	id := apcID(t, m)

	put := `{"title":"Sub 2","type":"guide","status":"draft","modified":"` + m["modified"].(string) + `"}`
	rec := apcDo(t, h, http.MethodPut, "/admin/api/posts/"+apcItoa(id), put, true)
	apcWant(t, rec, http.StatusOK)
	if got := aappStoredParent(t, repos, id); got != topID {
		t.Fatalf("omitted parent changed stored parent to %d, want %d", got, topID)
	}
	m2 := apcDecode(t, rec)

	put = `{"title":"Sub 3","type":"guide","status":"draft","parent":0,"modified":"` + m2["modified"].(string) + `"}`
	rec = apcDo(t, h, http.MethodPut, "/admin/api/posts/"+apcItoa(id), put, true)
	apcWant(t, rec, http.StatusOK)
	if got := aappStoredParent(t, repos, id); got != 0 {
		t.Fatalf("parent:0 left stored parent %d, want 0", got)
	}
}

// Req 11.2-11.4: invalid parents answer 400 through the admin API too.
func TestAdminPostParentInvalidRejected(t *testing.T) {
	h, _ := newAAPPRouter(t)
	topID := apcID(t, aappCreate(t, h, `{"title":"Top","type":"guide","status":"draft"}`))
	rec := apcDo(t, h, http.MethodPost, "/admin/api/posts",
		`{"title":"P","type":"product","status":"draft","parent":`+strconv.FormatInt(topID, 10)+`}`, true)
	apcWant(t, rec, http.StatusBadRequest)
	rec = apcDo(t, h, http.MethodPost, "/admin/api/posts",
		`{"title":"G","type":"guide","status":"draft","parent":999999}`, true)
	apcWant(t, rec, http.StatusBadRequest)
}

// Req 11.1: the detail carries `parent` for registered types only; a built-in
// post with a stored parent does not expose one.
func TestAdminPostParentDetailRegisteredTypesOnly(t *testing.T) {
	h, repos := newAAPPRouter(t)
	topID := apcID(t, aappCreate(t, h, `{"title":"Top","type":"guide","status":"draft"}`))
	id, err := repos.PostWriter.Create(context.Background(), domain.Post{
		Author: 1, Date: time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), Content: "<p>x</p>", Title: "Plain",
		Status: "publish", Slug: "aapp-plain", Type: "post", CommentStatus: "closed", ParentID: topID,
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	if _, has := aappGet(t, h, id)["parent"]; has {
		t.Fatal("built-in post detail carries `parent`; only registered types should")
	}
}
