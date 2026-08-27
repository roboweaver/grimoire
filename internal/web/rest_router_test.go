package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// newRESTRouter builds the full chi router with auth + admin + REST wired,
// backed by a seeded SQLite database (see storagetest.SeedFixtures for the
// exact fixture shape), plus the fakeSessions so tests can drive the
// principal.
func newRESTRouter(t *testing.T, fake *fakeSessions) http.Handler {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
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
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, "wp_")
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	posts := content.NewPostService(repos.Posts).WithCounter(repos.PostCounter)
	srv := web.NewServer(
		posts,
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithContentFeatures(comments, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	return srv.Routes()
}

func decodeRESTErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode REST error body %q: %v", rec.Body.String(), err)
	}
	return body.Code
}

func TestRESTIndex(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/ = %d, want 200", rec.Code)
	}
	var body struct {
		Name       string         `json:"name"`
		Namespaces []string       `json:"namespaces"`
		Routes     map[string]any `json:"routes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "grimoire test" {
		t.Fatalf("name = %q, want %q", body.Name, "grimoire test")
	}
	if len(body.Namespaces) != 1 || body.Namespaces[0] != "wp/v2" {
		t.Fatalf("namespaces = %v, want [wp/v2]", body.Namespaces)
	}
	if _, ok := body.Routes["/wp/v2/posts"]; !ok {
		t.Fatalf("routes missing /wp/v2/posts: %v", body.Routes)
	}
}

func TestRESTNamespaceIndex(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/ = %d, want 200", rec.Code)
	}
	var body struct {
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Namespace != "wp/v2" {
		t.Fatalf("namespace = %q, want wp/v2", body.Namespace)
	}
}

func TestRESTPostsCollectionPublishedNewestFirst(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/posts = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-WP-Total"); got != "3" {
		t.Fatalf("X-WP-Total = %q, want 3", got)
	}
	if got := rec.Header().Get("X-WP-TotalPages"); got != "1" {
		t.Fatalf("X-WP-TotalPages = %q, want 1", got)
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3 (draft excluded)", len(items))
	}
	if items[0]["slug"] != "hello-3" {
		t.Fatalf("items[0].slug = %v, want hello-3 (newest first)", items[0]["slug"])
	}
	links, ok := items[0]["_links"].(map[string]any)
	if !ok {
		t.Fatalf("_links missing or wrong shape: %v", items[0]["_links"])
	}
	for _, rel := range []string{"self", "collection", "author", "replies", "wp:attachment", "wp:featuredmedia"} {
		if _, ok := links[rel]; !ok {
			t.Fatalf("hello-3 _links missing relation %q: %v", rel, links)
		}
	}
	if _, ok := items[0]["_embedded"]; ok {
		t.Fatalf("_embedded present without ?_embed: %v", items[0])
	}
	// hello-2, the middle post, has no featured image (Req seed comment).
	links2 := items[1]["_links"].(map[string]any)
	if _, ok := links2["wp:featuredmedia"]; ok {
		t.Fatalf("hello-2 unexpectedly has wp:featuredmedia: %v", links2)
	}
}

func TestRESTPostsCollectionEmbed(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts?_embed", nil))
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	embedded, ok := items[0]["_embedded"].(map[string]any)
	if !ok {
		t.Fatalf("_embedded missing on ?_embed: %v", items[0])
	}
	authorList, ok := embedded["author"].([]any)
	if !ok || len(authorList) != 1 {
		t.Fatalf("_embedded.author = %v, want a 1-item array", embedded["author"])
	}
	media, ok := embedded["wp:featuredmedia"].([]any)
	if !ok || len(media) != 1 {
		t.Fatalf("_embedded.wp:featuredmedia = %v, want a 1-item array for hello-3", embedded["wp:featuredmedia"])
	}
}

func TestRESTPostsCollectionSearchOrderPaging(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts?search=Hello+Two", nil))
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || items[0]["slug"] != "hello-2" {
		t.Fatalf("search=Hello+Two -> %v, want just hello-2", items)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts?orderby=id&order=asc", nil))
	items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if items[0]["slug"] != "hello-1" {
		t.Fatalf("orderby=id&order=asc first = %v, want hello-1", items[0]["slug"])
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts?per_page=1&page=2", nil))
	items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || items[0]["slug"] != "hello-2" {
		t.Fatalf("per_page=1&page=2 = %v, want just hello-2", items)
	}
	if got := rec.Header().Get("X-WP-TotalPages"); got != "3" {
		t.Fatalf("X-WP-TotalPages = %q, want 3", got)
	}
}

func TestRESTPostsCollectionSlugFilter(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts?slug=hello-1", nil))
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || items[0]["slug"] != "hello-1" {
		t.Fatalf("slug=hello-1 = %v, want just hello-1", items)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts?slug=does-not-exist", nil))
	items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("slug=does-not-exist = %v, want empty", items)
	}
}

func TestRESTPostSingleDraftHiddenFromAnonymous404(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts/4", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET draft post anon = %d, want 404", rec.Code)
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_post_invalid_id" {
		t.Fatalf("code = %q, want rest_post_invalid_id", code)
	}
}

func TestRESTPostSingleDraftVisibleWithEditPosts(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(7, "editor", []string{"editor"})}
	h := newRESTRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts/4", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET draft post editor = %d, want 200", rec.Code)
	}
}

func TestRESTPostSingleWrongTypeIs404(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	// post 5 ("about") is a page, not a post.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts/5", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /posts/5 (a page) = %d, want 404", rec.Code)
	}
}

func TestRESTPagesCollectionAndSingle(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/pages", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/pages = %d, want 200", rec.Code)
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || items[0]["slug"] != "about" {
		t.Fatalf("pages = %v, want just about", items)
	}
	if _, ok := items[0]["categories"]; ok {
		t.Fatalf("page unexpectedly has categories field: %v", items[0])
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/pages/5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/pages/5 = %d, want 200", rec.Code)
	}
}

func TestRESTMediaCollectionAndParentFilter(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/media", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/media = %d, want 200", rec.Code)
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("media collection len = %d, want 2", len(items))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/media?parent=1", nil))
	items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || items[0]["slug"] != "photo" {
		t.Fatalf("media?parent=1 = %v, want just photo (attachment 201)", items)
	}
}

func TestRESTMediaSingleDetailsAndEmptyFallback(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/media/201", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/media/201 = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	details, ok := body["media_details"].(map[string]any)
	if !ok {
		t.Fatalf("media_details missing: %v", body)
	}
	if details["width"] != float64(800) || details["height"] != float64(600) {
		t.Fatalf("media_details = %v, want width 800 height 600", details)
	}
	// up: attachment 201 is attached to post 3 (see seed comment).
	links := body["_links"].(map[string]any)
	if _, ok := links["up"]; !ok {
		t.Fatalf("attachment 201 _links missing up: %v", links)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/media/202", nil))
	body = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	details = body["media_details"].(map[string]any)
	if len(details) != 0 {
		t.Fatalf("attachment 202 media_details = %v, want {}", details)
	}
	links = body["_links"].(map[string]any)
	if _, ok := links["up"]; ok {
		t.Fatalf("attachment 202 (unattached) unexpectedly has _links.up: %v", links)
	}
}

func TestRESTMediaSingleNotFound(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/media/9999", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET missing media = %d, want 404", rec.Code)
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_post_invalid_id" {
		t.Fatalf("code = %q, want rest_post_invalid_id", code)
	}
}

func TestRESTUsersCollectionViewContextForAnonymous(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/users = %d, want 200", rec.Code)
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("users = %v, want 1 seeded user", items)
	}
	if _, ok := items[0]["email"]; ok {
		t.Fatalf("anonymous view context unexpectedly has email: %v", items[0])
	}
	if _, ok := items[0]["name"]; !ok {
		t.Fatalf("view context missing name: %v", items[0])
	}
}

func TestRESTUserSingleEditContextWithListUsers(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(7, "editor", []string{"administrator"})}
	h := newRESTRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users/1", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/users/1 admin = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["email"]; !ok {
		t.Fatalf("edit context (list_users) missing email: %v", body)
	}
}

func TestRESTUserMeRequiresAuth(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /wp-json/wp/v2/users/me anon = %d, want 401", rec.Code)
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_not_logged_in" {
		t.Fatalf("code = %q, want rest_not_logged_in", code)
	}
}

func TestRESTUserMeAuthenticated(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(1, "admin", []string{"subscriber"})}
	h := newRESTRouter(t, fake)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users/me", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /wp-json/wp/v2/users/me = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["email"]; !ok {
		t.Fatalf("own /users/me record missing edit-context email: %v", body)
	}
}

// --- 501 deferred-write coverage (Req 2.5, 3.3, 4.5, 5.5, 7.5) ---

// TestRESTWritesNotImplemented covers every wp/v2 write verb still deferred
// to a later milestone (Req 7.5). Posts/pages writes are no longer part of
// this list as of M6 Phase 3 (Req 6) -- see rest_posts_write_test.go for
// their now-implemented coverage.
func TestRESTWritesNotImplemented(t *testing.T) {
	fake := &fakeSessions{authPrincipal: auth.NewPrincipal(7, "editor", []string{"administrator"})}
	h := newRESTRouter(t, fake)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/wp-json/wp/v2/media"},
		{http.MethodPut, "/wp-json/wp/v2/media/201"},
		{http.MethodPost, "/wp-json/wp/v2/users"},
		{http.MethodPut, "/wp-json/wp/v2/users/1"},
		{http.MethodPut, "/wp-json/wp/v2/comments/101"},
		{http.MethodPatch, "/wp-json/wp/v2/comments/101"},
		{http.MethodDelete, "/wp-json/wp/v2/comments/101"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authed(httptest.NewRequest(tc.method, tc.path, nil)))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s = %d, want 501", tc.method, tc.path, rec.Code)
		}
	}
}

func TestRESTUnknownRoute404(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown wp/v2 route = %d, want 404", rec.Code)
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_no_route" {
		t.Fatalf("code = %q, want rest_no_route", code)
	}
}

func TestRESTMethodNotAllowed(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	// TRACE is not registered for any wp/v2 route (unlike DELETE, which
	// this milestone now registers everywhere as a 501 deferred-write
	// stub per Req 7.5), so it's a genuinely unplanned method and must
	// still hit chi's MethodNotAllowed -> 405.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodTrace, "/wp-json/wp/v2/posts", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("TRACE /wp-json/wp/v2/posts = %d, want 405", rec.Code)
	}
}

// TestRESTDeferredWritesReturn501NotMethodNotAllowed covers write
// method/route combinations that were previously unregistered and so fell
// through to chi's generic 405 MethodNotAllowed, contradicting Req 7.5's
// "SHALL NOT silently 404 or 405 a write it plans to support later." Every
// one of these must now hit the explicit 501 deferred-write stub instead.
func TestRESTDeferredWritesReturn501NotMethodNotAllowed(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/wp-json/wp/v2/posts/1"},
		{http.MethodPut, "/wp-json/wp/v2/posts"},
		{http.MethodPatch, "/wp-json/wp/v2/posts"},
		{http.MethodDelete, "/wp-json/wp/v2/posts"},
		{http.MethodPost, "/wp-json/wp/v2/pages/1"},
		{http.MethodDelete, "/wp-json/wp/v2/pages"},
		{http.MethodPost, "/wp-json/wp/v2/media/1"},
		{http.MethodPut, "/wp-json/wp/v2/media"},
		{http.MethodDelete, "/wp-json/wp/v2/media"},
		{http.MethodPost, "/wp-json/wp/v2/users/1"},
		{http.MethodPut, "/wp-json/wp/v2/users"},
		{http.MethodDelete, "/wp-json/wp/v2/users"},
		{http.MethodPost, "/wp-json/wp/v2/comments/1"},
		{http.MethodPut, "/wp-json/wp/v2/comments"},
		{http.MethodDelete, "/wp-json/wp/v2/comments"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s %s = %d, want 501", tc.method, tc.path, rec.Code)
			}
		})
	}
}

func TestRESTDoesNotShadowPublicSlug(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /hello-1 = %d, want 200 (public post, not shadowed by /wp-json)", rec.Code)
	}
}

func TestRESTAbsoluteLinksUseRequestHost(t *testing.T) {
	h := newRESTRouter(t, &fakeSessions{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts/3", nil)
	req.Host = "example.test"
	h.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	link, _ := body["link"].(string)
	if link != "http://example.test/hello-3" {
		t.Fatalf("link = %q, want http://example.test/hello-3", link)
	}
}
