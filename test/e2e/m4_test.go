package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	"github.com/roboweaver/grimoire/internal/storage/seed"
	"github.com/roboweaver/grimoire/internal/web"
)

// m4Env bundles everything a M4 e2e test needs: a migrated+seeded SQLite DB,
// a fully wired *web.Server (auth + comments + media + menus), an httptest
// server around it, and an authenticated admin client + CSRF token.
type m4Env struct {
	t        *testing.T
	ctx      context.Context
	repos    *storage.Repositories
	dbcfg    config.DatabaseConfig
	sm       *auth.SessionManager
	comments *content.CommentService
	media    *content.MediaService
	menus    *content.NavMenuService
	ts       *httptest.Server
	client   *http.Client
	csrf     string
	uploads  string
}

func newM4Env(t *testing.T) *m4Env {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	dbcfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}

	repos, err := storage.New(dbcfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	migFS, err := storage.MigrationsFS(dbcfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, dbcfg.Vendor, dbcfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := seed.Run(ctx, repos.DB(), dbcfg.Vendor, dbcfg.TablePrefix); err != nil {
		t.Fatalf("seed.Run: %v", err)
	}

	const adminLogin, adminPass = "root", "correct-horse-battery-staple"
	users := content.NewUserService(repos.Users, repos.UserMeta, dbcfg.TablePrefix)
	if _, err := users.Bootstrap(ctx, domain.User{
		Login: adminLogin, Nicename: adminLogin, DisplayName: "Admin", Email: "admin@example.test",
	}, adminPass, auth.RoleAdministrator); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}

	sm := &auth.SessionManager{Users: repos.Users, Meta: repos.UserMeta, Sessions: repos.Sessions, Prefix: dbcfg.TablePrefix}

	uploadsDir := t.TempDir()
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	menus := content.NewNavMenuService(repos.NavMenus, "default")
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: uploadsDir, BaseURL: "/wp-content/uploads"})

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}

	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithContentFeatures(comments, media, menus).
		WithAuth(sm, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdmin(admin.Handler("/admin"), content.NewAdminService(
			repos.AdminPosts, repos.PostWriter, repos.PostCounter,
			repos.UserCounter, repos.TermCounter, repos.Users,
		))

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	env := &m4Env{t: t, ctx: ctx, repos: repos, dbcfg: dbcfg, sm: sm, comments: comments, media: media, menus: menus, ts: ts, client: client, uploads: uploadsDir}
	env.login(adminLogin, adminPass)
	return env
}

// login authenticates the admin client and captures the session CSRF token
// via the real /admin/api/session endpoint (mirrors the SPA bootstrap).
func (e *m4Env) login(loginName, password string) {
	e.t.Helper()
	resp, err := e.client.Get(e.ts.URL + "/login")
	if err != nil {
		e.t.Fatalf("GET /login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := formInputs(string(body))["csrf_token"]
	if csrf == "" {
		e.t.Fatal("GET /login missing csrf_token")
	}
	resp, err = e.client.PostForm(e.ts.URL+"/login", url.Values{"log": {loginName}, "pwd": {password}, "csrf_token": {csrf}, "redirect": {"/"}})
	if err != nil {
		e.t.Fatalf("POST /login: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("POST /login = %d, want 303", resp.StatusCode)
	}

	resp, err = e.client.Get(e.ts.URL + "/admin/api/session")
	if err != nil {
		e.t.Fatalf("GET /admin/api/session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET /admin/api/session = %d, want 200", resp.StatusCode)
	}
	var sess struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		e.t.Fatalf("decode session: %v", err)
	}
	if sess.CSRFToken == "" {
		e.t.Fatal("admin session missing csrfToken")
	}
	e.csrf = sess.CSRFToken
}

func (e *m4Env) adminJSON(method, path string, body io.Reader) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, body)
	if err != nil {
		e.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("X-CSRF-Token", e.csrf)
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// Comment submission -> moderation -> trash -> untrash, with CSRF.
// ---------------------------------------------------------------------------

func TestM4CommentSubmitModerateTrashUntrash(t *testing.T) {
	env := newM4Env(t)

	// 1. Anonymous GET on the single post page: extract the double-submit
	// comment CSRF token (cookie + hidden form field) and confirm the comment
	// form is present.
	resp, err := env.client.Get(env.ts.URL + "/hello-world")
	if err != nil {
		t.Fatalf("GET /hello-world: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /hello-world = %d, want 200", resp.StatusCode)
	}
	inputs := formInputs(string(body))
	commentToken := inputs["comment_csrf_token"]
	if commentToken == "" {
		t.Fatal("single page missing comment_csrf_token")
	}
	u, _ := url.Parse(env.ts.URL)
	var cookieToken string
	for _, c := range env.client.Jar.Cookies(u) {
		if c.Name == "grimoire_comment_csrf" {
			cookieToken = c.Value
		}
	}
	if cookieToken == "" || cookieToken != commentToken {
		t.Fatalf("comment CSRF cookie = %q, form token = %q, want equal & non-empty", cookieToken, commentToken)
	}

	// 2. Anonymous POST /comment without the CSRF token is rejected.
	badResp, err := env.client.PostForm(env.ts.URL+"/comment", url.Values{
		"post_id": {"1"}, "author": {"Eve"}, "email": {"eve@example.test"}, "content": {"no token"},
	})
	if err != nil {
		t.Fatalf("POST /comment (no csrf): %v", err)
	}
	io.Copy(io.Discard, badResp.Body)
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /comment without csrf = %d, want 403", badResp.StatusCode)
	}

	// 3. Anonymous POST /comment with the valid double-submit token succeeds.
	// The seeded post's default spam filter (BasicCommentSpamFilter with an
	// empty config) auto-approves a normal, link-free, first-time comment.
	resp, err = env.client.PostForm(env.ts.URL+"/comment", url.Values{
		"post_id": {"1"}, "author": {"Alice"}, "email": {"alice@example.test"}, "content": {"Great post!"},
		"comment_csrf_token": {commentToken},
	})
	if err != nil {
		t.Fatalf("POST /comment: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /comment = %d, want 303", resp.StatusCode)
	}

	comments, total, err := env.comments.List(env.ctx, domain.CommentFilter{PostID: 1})
	if err != nil {
		t.Fatalf("comments.List: %v", err)
	}
	if total != 1 || len(comments) != 1 {
		t.Fatalf("comments after submit = %d, want 1", total)
	}
	id := comments[0].ID
	if comments[0].Status != "1" {
		t.Fatalf("new comment status = %q, want auto-approved (1)", comments[0].Status)
	}

	// The freshly approved comment renders immediately on the public page.
	resp, err = env.client.Get(env.ts.URL + "/hello-world")
	if err != nil {
		t.Fatalf("GET /hello-world (post-submit): %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bodyContains(body, "Great post!") {
		t.Fatal("auto-approved comment must render publicly")
	}

	// 4. Admin unapproves (holds) the comment via the authenticated JSON API,
	// guarded by the synchronizer CSRF header (Req: authenticated writes reuse
	// the CSRF header contract).
	unapproveResp := env.adminJSON(http.MethodPost, fmt.Sprintf("/admin/api/comments/%d/unapprove", id), nil)
	io.Copy(io.Discard, unapproveResp.Body)
	unapproveResp.Body.Close()
	if unapproveResp.StatusCode != http.StatusOK {
		t.Fatalf("unapprove comment = %d, want 200", unapproveResp.StatusCode)
	}

	// Admin actions without a CSRF header are rejected.
	noCSRFReq, _ := http.NewRequest(http.MethodPost, env.ts.URL+fmt.Sprintf("/admin/api/comments/%d/approve", id), nil)
	noCSRFResp, err := env.client.Do(noCSRFReq)
	if err != nil {
		t.Fatalf("approve (no csrf): %v", err)
	}
	io.Copy(io.Discard, noCSRFResp.Body)
	noCSRFResp.Body.Close()
	if noCSRFResp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin action without csrf = %d, want 403", noCSRFResp.StatusCode)
	}

	comment, err := env.repos.Comments.ByID(env.ctx, id)
	if err != nil {
		t.Fatalf("Comments.ByID after unapprove: %v", err)
	}
	if comment.Status != "0" {
		t.Fatalf("status after unapprove = %q, want 0 (held)", comment.Status)
	}

	// The held comment no longer renders on the public page.
	resp, err = env.client.Get(env.ts.URL + "/hello-world")
	if err != nil {
		t.Fatalf("GET /hello-world (post-unapprove): %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if bodyContains(body, "Great post!") {
		t.Fatal("held comment must not render publicly")
	}

	// 5. Admin re-approves the comment (valid CSRF header, this time
	// succeeding), restoring public visibility.
	statusResp := env.adminJSON(http.MethodPost, fmt.Sprintf("/admin/api/comments/%d/approve", id), nil)
	io.Copy(io.Discard, statusResp.Body)
	statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("approve comment = %d, want 200", statusResp.StatusCode)
	}
	comment, err = env.repos.Comments.ByID(env.ctx, id)
	if err != nil {
		t.Fatalf("Comments.ByID after approve: %v", err)
	}
	if comment.Status != "1" {
		t.Fatalf("status after approve = %q, want 1 (approved)", comment.Status)
	}

	// The approved comment is now visible on the public page.
	resp, err = env.client.Get(env.ts.URL + "/hello-world")
	if err != nil {
		t.Fatalf("GET /hello-world (post-approve): %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bodyContains(body, "Great post!") {
		t.Fatal("approved comment must render publicly")
	}

	// 5. Admin trashes the comment.
	trashResp := env.adminJSON(http.MethodPost, fmt.Sprintf("/admin/api/comments/%d/trash", id), nil)
	io.Copy(io.Discard, trashResp.Body)
	trashResp.Body.Close()
	if trashResp.StatusCode != http.StatusOK {
		t.Fatalf("trash comment = %d, want 200", trashResp.StatusCode)
	}
	comment, err = env.repos.Comments.ByID(env.ctx, id)
	if err != nil {
		t.Fatalf("Comments.ByID after trash: %v", err)
	}
	if comment.Status != "trash" {
		t.Fatalf("status after trash = %q, want trash", comment.Status)
	}

	// Trashed comments disappear from the public page.
	resp, err = env.client.Get(env.ts.URL + "/hello-world")
	if err != nil {
		t.Fatalf("GET /hello-world (post-trash): %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if bodyContains(body, "Great post!") {
		t.Fatal("trashed comment must not render publicly")
	}

	// 6. Admin untrashes the comment and it returns to its pre-trash status
	// (approved), restoring public visibility.
	untrashResp := env.adminJSON(http.MethodPost, fmt.Sprintf("/admin/api/comments/%d/untrash", id), nil)
	io.Copy(io.Discard, untrashResp.Body)
	untrashResp.Body.Close()
	if untrashResp.StatusCode != http.StatusOK {
		t.Fatalf("untrash comment = %d, want 200", untrashResp.StatusCode)
	}
	comment, err = env.repos.Comments.ByID(env.ctx, id)
	if err != nil {
		t.Fatalf("Comments.ByID after untrash: %v", err)
	}
	if comment.Status != "1" {
		t.Fatalf("status after untrash = %q, want 1 (restored to approved)", comment.Status)
	}
	resp, err = env.client.Get(env.ts.URL + "/hello-world")
	if err != nil {
		t.Fatalf("GET /hello-world (post-untrash): %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bodyContains(body, "Great post!") {
		t.Fatal("untrashed (restored) comment must render publicly again")
	}
}

// ---------------------------------------------------------------------------
// Media upload -> attach -> serve, plus rollback-on-failure.
// ---------------------------------------------------------------------------

func TestM4MediaUploadAttachServe(t *testing.T) {
	env := newM4Env(t)

	// 1. Authenticated multipart upload via the admin API (CSRF-guarded).
	const fileContent = "not really a jpeg, just test bytes"
	var buf multipartBuf
	buf.writeFile(t, "file", "photo.jpg", "image/jpeg", []byte(fileContent))

	req, err := http.NewRequest(http.MethodPost, env.ts.URL+"/admin/api/media", &buf.body)
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	req.Header.Set("Content-Type", buf.writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", env.csrf)
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/api/media: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d, want 201; body=%s", resp.StatusCode, body)
	}
	var created domain.Media
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created media: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("created media has no ID")
	}
	if created.URL == "" || created.Filename == "" {
		t.Fatalf("created media missing URL/Filename: %+v", created)
	}

	// The file must actually exist under the uploads dir (YYYY/MM layout).
	onDisk := filepath.Join(env.uploads, filepath.FromSlash(created.Filename))
	data, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("uploaded file missing on disk at %s: %v", onDisk, err)
	}
	if string(data) != fileContent {
		t.Fatalf("uploaded file content = %q, want %q", data, fileContent)
	}

	// 2. Attach the media to a post (parent linkage).
	if err := env.media.Attach(env.ctx, created.ID, 1); err != nil {
		t.Fatalf("media.Attach: %v", err)
	}
	attached, err := env.media.Get(env.ctx, created.ID)
	if err != nil {
		t.Fatalf("media.Get after attach: %v", err)
	}
	if attached.ParentID != 1 {
		t.Fatalf("attached media ParentID = %d, want 1", attached.ParentID)
	}

	// 3. Serve the file publicly via /wp-content/uploads/*, no auth required.
	anonClient := &http.Client{}
	serveResp, err := anonClient.Get(env.ts.URL + created.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", created.URL, err)
	}
	served, _ := io.ReadAll(serveResp.Body)
	serveResp.Body.Close()
	if serveResp.StatusCode != http.StatusOK {
		t.Fatalf("serve upload = %d, want 200", serveResp.StatusCode)
	}
	if string(served) != fileContent {
		t.Fatalf("served content = %q, want %q", served, fileContent)
	}

	// 4. Rollback-on-failure: a Store() call whose DB insert fails after the
	// file has been written to disk must not leave an orphaned file behind.
	// This exercises internal/content.MediaService.Store directly (the seam
	// where the rollback lives) with a writer that always errors, wired to
	// the same on-disk uploads directory the e2e server already serves from.
	failingWriter := &rollbackFailWriter{err: fmt.Errorf("simulated db insert failure")}
	failingMedia := content.NewMediaService(env.repos.Media, failingWriter, content.MediaConfig{UploadsDir: env.uploads, BaseURL: "/wp-content/uploads"})
	_, err = failingMedia.Store(env.ctx, strings.NewReader("rollback me"), content.MediaUpload{Filename: "rollback.txt", MimeType: "text/plain"})
	if err == nil {
		t.Fatal("expected Store to fail when the writer errors")
	}
	rolledBackPath := filepath.Join(env.uploads, time.Now().UTC().Format("2006"), time.Now().UTC().Format("01"), "rollback.txt")
	if _, statErr := os.Stat(rolledBackPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected rollback to remove %s, stat err = %v", rolledBackPath, statErr)
	}
}

// rollbackFailWriter always fails Create, simulating a DB error after the
// file has already been written, to prove MediaService.Store rolls the file
// back rather than leaving an orphan on disk.
type rollbackFailWriter struct{ err error }

func (w *rollbackFailWriter) Create(context.Context, domain.Media) (int64, error) {
	return 0, w.err
}
func (w *rollbackFailWriter) SetParent(context.Context, int64, int64) error { return nil }

// ---------------------------------------------------------------------------
// Nav menu resolution against theme_mods -> public rendering.
// ---------------------------------------------------------------------------

func TestM4NavMenuResolutionAndRendering(t *testing.T) {
	env := newM4Env(t)
	prefix := env.dbcfg.TablePrefix
	db := env.repos.DB()

	// Seed a nav_menu term "Primary" with one custom item, then assign it to
	// the "primary" location for the "default" theme via theme_mods, mirroring
	// WordPress's serialized theme_mods_{theme} option.
	if _, err := db.ExecContext(env.ctx, `INSERT INTO `+prefix+`terms (term_id, name, slug) VALUES (?, ?, ?)`, 900, "Primary", "primary-menu"); err != nil {
		t.Fatalf("insert term: %v", err)
	}
	if _, err := db.ExecContext(env.ctx, `INSERT INTO `+prefix+`term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`, 900, 900, "nav_menu", "", 0, 1); err != nil {
		t.Fatalf("insert term_taxonomy: %v", err)
	}
	if _, err := db.ExecContext(env.ctx, `INSERT INTO `+prefix+`posts (ID, post_title, post_name, post_type, post_status, post_date, post_content, comment_status, post_parent, post_mime_type, menu_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		950, "Home Custom", "", "nav_menu_item", "publish", "2024-01-08 00:00:00", "", "closed", 0, "", 1); err != nil {
		t.Fatalf("insert nav_menu_item post: %v", err)
	}
	if _, err := db.ExecContext(env.ctx, `INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, 950, 900, 0); err != nil {
		t.Fatalf("insert term_relationships: %v", err)
	}
	metaRows := []struct {
		key, val string
	}{
		{"_menu_item_type", "custom"},
		{"_menu_item_object", "custom"},
		{"_menu_item_object_id", "0"},
		{"_menu_item_menu_item_parent", "0"},
		{"_menu_item_url", "/home-custom"},
	}
	for _, m := range metaRows {
		if _, err := db.ExecContext(env.ctx, `INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, 950, m.key, m.val); err != nil {
			t.Fatalf("insert postmeta %s: %v", m.key, err)
		}
	}
	themeMods := `a:1:{s:18:"nav_menu_locations";a:1:{s:7:"primary";i:900;}}`
	if _, err := db.ExecContext(env.ctx, `INSERT INTO `+prefix+`options (option_name, option_value, autoload) VALUES (?, ?, ?)`, "theme_mods_default", themeMods, "yes"); err != nil {
		t.Fatalf("insert theme_mods option: %v", err)
	}

	// 1. Service-level resolution: ByLocation("primary") must find the menu
	// via theme_mods for the "default" theme.
	menu, err := env.menus.ByLocation(env.ctx, "primary")
	if err != nil {
		t.Fatalf("menus.ByLocation: %v", err)
	}
	if menu.Name != "Primary" {
		t.Fatalf("resolved menu name = %q, want Primary", menu.Name)
	}
	if len(menu.Items) != 1 || menu.Items[0].Label != "Home Custom" || menu.Items[0].URL != "/home-custom" {
		t.Fatalf("resolved menu items = %+v, want one Home Custom -> /home-custom", menu.Items)
	}

	// 2. Public page rendering: the theme must render the resolved menu.
	resp, err := env.client.Get(env.ts.URL + "/hello-world")
	if err != nil {
		t.Fatalf("GET /hello-world: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /hello-world = %d, want 200", resp.StatusCode)
	}
	if !bodyContains(body, "Home Custom") || !bodyContains(body, "/home-custom") {
		t.Fatal("rendered page must include the resolved primary menu item")
	}

	// 3. Admin JSON API exposes the same menu tree.
	menusResp := env.adminJSON(http.MethodGet, "/admin/api/menus", nil)
	menusBody, _ := io.ReadAll(menusResp.Body)
	menusResp.Body.Close()
	if menusResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/api/menus = %d, want 200", menusResp.StatusCode)
	}
	if !bodyContains(menusBody, "Primary") {
		t.Fatal("admin menus API must list the seeded Primary menu")
	}

	// 4. Empty-degradation: an unassigned location resolves to an empty menu
	// without error, and the theme still renders (no menu items, no crash).
	empty, err := env.menus.ByLocation(env.ctx, "footer")
	if err != nil {
		t.Fatalf("menus.ByLocation(footer) unassigned: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("unassigned location resolved non-empty menu: %+v", empty)
	}
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

func bodyContains(body []byte, needle string) bool {
	return len(body) > 0 && indexOf(string(body), needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

type multipartBuf struct {
	body   bytes.Buffer
	writer *multipart.Writer
}

func (b *multipartBuf) writeFile(t *testing.T, field, filename, contentType string, data []byte) {
	t.Helper()
	b.writer = multipart.NewWriter(&b.body)
	fw, err := b.writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := b.writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
}
