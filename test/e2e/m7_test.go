package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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
	"github.com/roboweaver/grimoire/internal/scheduler"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/seed"
	"github.com/roboweaver/grimoire/internal/web"
)

// m7Env wires the full M7 surface exactly as cmd/grimoire/main.go does: the
// M1-M6 stack (mirrored from m6Env) plus WithAdminRevisions (Req 1-3), using
// a postWrite constructed with content.WithRevisionSnapshotter -- the exact
// wiring task 7.1's discovery found missing from main.go and this session
// fixed. Every test in this file exercises the real HTTP boundary
// (httptest.Server) against a fully wired server, per Phase 7's "confirm no
// gap exists between what unit/contract tests exercise and what the real
// server wires up" intent.
type m7Env struct {
	t             *testing.T
	ts            *httptest.Server
	client        *http.Client
	csrf          string
	repos         *storage.Repositories
	postWrite     *content.PostWriteService
	revisionWrite *content.RevisionWriteService
	autosave      *content.AutosaveService
}

func newM7Env(t *testing.T) *m7Env {
	t.Helper()
	ctx := t.Context()
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
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	menus := content.NewNavMenuService(repos.NavMenus, "default")
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: t.TempDir(), BaseURL: "/wp-content/uploads"})

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}

	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	restMapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, dbcfg.TablePrefix)
	appPasswords := &auth.ApplicationPasswords{Users: repos.Users, Meta: repos.UserMeta, Prefix: dbcfg.TablePrefix}

	termRW := m6TermReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader}
	// revisionWrite/autosave + WithRevisionSnapshotter mirror
	// cmd/grimoire/main.go's wiring exactly (maxPerPost -1 == unlimited,
	// Req 5.1's "defaulting to unlimited when unset" -- M7 adds no config
	// knob for this).
	revisionWrite := content.NewRevisionWriteService(repos.Revisions, repos.PostWriter, -1)
	autosave := content.NewAutosaveService(repos.Revisions, repos.PostWriter)
	postWrite := content.NewPostWriteService(repos.PostWriter, content.WithRevisionSnapshotter(revisionWrite))
	termWrite := content.NewTermWriteService(termRW)
	postTermsWrite := content.NewPostTermsWriteService(repos.PostWriter, repos.PostTermsWriter)

	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithContentFeatures(comments, media, menus).
		WithAuth(sm, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(postWrite, termWrite, postTermsWrite, repos.PostTerms).
		WithAdminRevisions(revisionWrite, autosave).
		WithREST(restMapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(appPasswords, true, "")

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	client := ts.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	env := &m7Env{
		t: t, ts: ts, client: client, repos: repos,
		postWrite: postWrite, revisionWrite: revisionWrite, autosave: autosave,
	}
	env.login(adminLogin, adminPass)
	return env
}

// login mirrors m6Env.login exactly.
func (e *m7Env) login(loginName, password string) {
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

// adminJSON mirrors m6Env.adminJSON exactly.
func (e *m7Env) adminJSON(method, path string, body any) *http.Response {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-CSRF-Token", e.csrf)
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// adminAuthedJSON mirrors m6Env.adminAuthedJSON exactly (used only for the
// self-service Application Password create call in the REST term scenario).
func (e *m7Env) adminAuthedJSON(method, path string, body []byte) *http.Response {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-CSRF-Token", e.csrf)
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func createM7Post(t *testing.T, env *m7Env, title, content, excerpt, slug string) int64 {
	t.Helper()
	resp := env.adminJSON(http.MethodPost, "/admin/api/posts", map[string]any{
		"title": title, "content": content, "excerpt": excerpt, "slug": slug,
		"status": "draft", "type": "post", "commentStatus": "open",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create post = %d, want 201 (body %q)", resp.StatusCode, string(body))
	}
	var created struct {
		ID int64 `json:"id"`
	}
	decodeJSON(t, resp, &created)
	if created.ID == 0 {
		t.Fatal("create post returned zero id")
	}
	return created.ID
}

// ---------------------------------------------------------------------------
// 7.1: revisions E2E -- create a post, save it twice with different content,
// confirm exactly one new revision exists carrying the first save's content,
// restore it, confirm both the restore and a fresh pre-restore snapshot are
// present afterward (Req 1, 2).
// ---------------------------------------------------------------------------

// TestM7RevisionsE2E exercises the entire revision-history story end-to-end
// over real HTTP against the actual wired-up admin API. It is what caught
// this milestone's main.go wiring gap (WithAdminRevisions was never called,
// and postWrite had no revision snapshotter, so revisions/autosave were
// entirely unreachable/inert in the real server despite passing every
// lower-level unit/contract/handler test).
func TestM7RevisionsE2E(t *testing.T) {
	env := newM7Env(t)

	postID := createM7Post(t, env, "Revisions post", "<p>Version one.</p>", "Excerpt one.", "revisions-post")

	// First save (update) with different content: PostWriteService.Update
	// snapshots the PRE-update state as a revision before applying the new
	// fields (design.md's Update pseudocode / task 2.3), so this call
	// creates a revision carrying "Version one." (the state that existed
	// before this call).
	getResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10), nil)
	var beforeFirstUpdate struct {
		Modified string `json:"modified"`
	}
	decodeJSON(t, getResp, &beforeFirstUpdate)

	updateResp1 := env.adminJSON(http.MethodPut, "/admin/api/posts/"+strconv.FormatInt(postID, 10), map[string]any{
		"title": "Revisions post", "content": "<p>Version two.</p>", "excerpt": "Excerpt one.",
		"slug": "revisions-post", "status": "draft", "type": "post", "commentStatus": "open",
		"modified": beforeFirstUpdate.Modified,
	})
	if updateResp1.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResp1.Body)
		updateResp1.Body.Close()
		t.Fatalf("first update = %d, want 200 (body %q)", updateResp1.StatusCode, string(body))
	}
	var afterFirstUpdate struct {
		Modified string `json:"modified"`
	}
	decodeJSON(t, updateResp1, &afterFirstUpdate)

	// Second save with yet different content, so the revisions list should
	// now contain exactly one entry (the pre-first-update snapshot); the
	// second update's pre-state ("Version two.") is snapshotted too.
	updateResp2 := env.adminJSON(http.MethodPut, "/admin/api/posts/"+strconv.FormatInt(postID, 10), map[string]any{
		"title": "Revisions post", "content": "<p>Version three.</p>", "excerpt": "Excerpt one.",
		"slug": "revisions-post", "status": "draft", "type": "post", "commentStatus": "open",
		"modified": afterFirstUpdate.Modified,
	})
	if updateResp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResp2.Body)
		updateResp2.Body.Close()
		t.Fatalf("second update = %d, want 200 (body %q)", updateResp2.StatusCode, string(body))
	}
	updateResp2.Body.Close()

	// GET .../revisions: exactly two entries now (one per update), newest
	// first.
	listResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10)+"/revisions", nil)
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		listResp.Body.Close()
		t.Fatalf("list revisions = %d, want 200 (body %q)", listResp.StatusCode, string(body))
	}
	var revisions []struct {
		ID int64 `json:"id"`
	}
	decodeJSON(t, listResp, &revisions)
	if len(revisions) != 2 {
		t.Fatalf("revisions list = %d entries, want 2: %+v", len(revisions), revisions)
	}

	// The oldest (last in the newest-first list) revision carries "Version
	// one." -- the pre-state of the FIRST update.
	oldestRevisionID := revisions[len(revisions)-1].ID
	detailResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10)+"/revisions/"+strconv.FormatInt(oldestRevisionID, 10), nil)
	if detailResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(detailResp.Body)
		detailResp.Body.Close()
		t.Fatalf("get revision detail = %d, want 200 (body %q)", detailResp.StatusCode, string(body))
	}
	var detail struct {
		Content string `json:"content"`
	}
	decodeJSON(t, detailResp, &detail)
	if detail.Content != "<p>Version one.</p>" {
		t.Fatalf("oldest revision content = %q, want %q", detail.Content, "<p>Version one.</p>")
	}

	// Restore the oldest revision: the live post's content should become
	// "Version one." again.
	restoreResp := env.adminJSON(http.MethodPost, "/admin/api/posts/"+strconv.FormatInt(postID, 10)+"/revisions/"+strconv.FormatInt(oldestRevisionID, 10)+"/restore", nil)
	if restoreResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(restoreResp.Body)
		restoreResp.Body.Close()
		t.Fatalf("restore revision = %d, want 200 (body %q)", restoreResp.StatusCode, string(body))
	}
	var restored struct {
		Content string `json:"content"`
	}
	decodeJSON(t, restoreResp, &restored)
	if restored.Content != "<p>Version one.</p>" {
		t.Fatalf("restored post content = %q, want %q", restored.Content, "<p>Version one.</p>")
	}

	// The restore itself snapshots the pre-restore state ("Version
	// three.") as a fresh revision, so the list should now have three
	// entries: the two original saves plus the pre-restore snapshot.
	finalListResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10)+"/revisions", nil)
	var finalRevisions []struct {
		ID int64 `json:"id"`
	}
	decodeJSON(t, finalListResp, &finalRevisions)
	if len(finalRevisions) != 3 {
		t.Fatalf("revisions list after restore = %d entries, want 3: %+v", len(finalRevisions), finalRevisions)
	}
}

// ---------------------------------------------------------------------------
// 7.2: autosave E2E -- autosave a post's in-progress edits without touching
// the published/live version, confirm GET .../autosave surfaces the newer
// draft, and confirm a fresh explicit save clears the "newer autosave"
// signal (Req 3).
// ---------------------------------------------------------------------------

// TestM7AutosaveE2E exercises the autosave story end-to-end over real HTTP.
func TestM7AutosaveE2E(t *testing.T) {
	env := newM7Env(t)

	postID := createM7Post(t, env, "Autosave post", "<p>Published body.</p>", "Excerpt.", "autosave-post")

	// No autosave yet: 404.
	noneResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10)+"/autosave", nil)
	noneResp.Body.Close()
	if noneResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET autosave before any save = %d, want 404", noneResp.StatusCode)
	}

	// grimoire truncates Modified timestamps to whole seconds, so an
	// autosave created in the same second as the post itself would tie
	// rather than beat it, and Newer's "strictly after" check (Req 3.4)
	// would then (correctly) report nothing to offer. Cross a second
	// boundary first, mirroring m6_test.go's identical workaround for
	// If-Unmodified-Since.
	time.Sleep(1100 * time.Millisecond)

	// Autosave in-progress edits.
	saveResp := env.adminJSON(http.MethodPost, "/admin/api/posts/"+strconv.FormatInt(postID, 10)+"/autosave", map[string]string{
		"title": "Autosave post", "content": "<p>In-progress draft body.</p>", "excerpt": "Excerpt.",
	})
	if saveResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(saveResp.Body)
		saveResp.Body.Close()
		t.Fatalf("POST autosave = %d, want 200 (body %q)", saveResp.StatusCode, string(body))
	}
	saveResp.Body.Close()

	// The live post is untouched: still "Published body.".
	liveResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10), nil)
	var live struct {
		Content string `json:"content"`
	}
	decodeJSON(t, liveResp, &live)
	if live.Content != "<p>Published body.</p>" {
		t.Fatalf("live post content after autosave = %q, want unchanged %q", live.Content, "<p>Published body.</p>")
	}

	// GET .../autosave now surfaces the in-progress draft (newer than the
	// live post's own last-modified time).
	getResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10)+"/autosave", nil)
	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResp.Body)
		getResp.Body.Close()
		t.Fatalf("GET autosave = %d, want 200 (body %q)", getResp.StatusCode, string(body))
	}
	var autosaved struct {
		Content string `json:"content"`
	}
	decodeJSON(t, getResp, &autosaved)
	if autosaved.Content != "<p>In-progress draft body.</p>" {
		t.Fatalf("autosave content = %q, want %q", autosaved.Content, "<p>In-progress draft body.</p>")
	}

	// Cross another second boundary so the upcoming manual save's Modified
	// strictly postdates the autosave row saved above.
	time.Sleep(1100 * time.Millisecond)

	// Manually save the post for real (the write endpoint requires the
	// current "modified" for its If-Unmodified-Since-style conflict check).
	preManualResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10), nil)
	var preManual struct {
		Modified string `json:"modified"`
	}
	decodeJSON(t, preManualResp, &preManual)

	updateResp := env.adminJSON(http.MethodPut, "/admin/api/posts/"+strconv.FormatInt(postID, 10), map[string]string{
		"title": "Autosave post", "content": "<p>Manually saved body.</p>", "excerpt": "Excerpt.",
		"modified": preManual.Modified,
	})
	if updateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResp.Body)
		updateResp.Body.Close()
		t.Fatalf("manual save = %d, want 200 (body %q)", updateResp.StatusCode, string(body))
	}
	updateResp.Body.Close()

	// The autosave endpoint no longer reports a "newer" autosave: the
	// manual save's Modified now postdates the stale autosave row (Req 3.4).
	afterSaveResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10)+"/autosave", nil)
	afterSaveResp.Body.Close()
	if afterSaveResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET autosave after manual save = %d, want 404 (stale autosave should no longer be reported)", afterSaveResp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 7.3: scheduled-publish E2E -- create a post with status "future" and a
// near-future post_date, run the server with a short scheduler interval,
// confirm it becomes publicly visible at publish status shortly after its
// post_date passes, with an unchanged slug/GUID (Req 4.6).
// ---------------------------------------------------------------------------

// TestM7ScheduledPublishE2E wires the scheduler alongside the real HTTP
// server (mirroring cmd/grimoire/main.go's goroutine arrangement) and
// confirms a "future" post becomes visible on the real public HTTP route
// once its post_date passes, with slug and GUID unchanged by the publish
// transition.
func TestM7ScheduledPublishE2E(t *testing.T) {
	env := newM7Env(t)
	ctx := context.Background()

	dueAt := time.Now().Add(150 * time.Millisecond)
	postID, err := env.repos.PostWriter.Create(ctx, domain.Post{
		Author: 1, Title: "Scheduled post", Content: "<p>Scheduled content.</p>",
		Status: "future", Type: "post", Slug: "scheduled-e2e-post",
		Date: dueAt, DateGMT: dueAt, GUID: "https://example.test/?p=scheduled-e2e-post",
	})
	if err != nil {
		t.Fatalf("create scheduled post: %v", err)
	}
	before, err := env.repos.PostWriter.ByID(ctx, postID)
	if err != nil {
		t.Fatalf("ByID before scheduling: %v", err)
	}

	// Not yet publicly visible: it is still "future".
	preResp, err := env.client.Get(env.ts.URL + "/" + before.Slug)
	if err != nil {
		t.Fatalf("GET /%s before publish: %v", before.Slug, err)
	}
	preResp.Body.Close()
	if preResp.StatusCode == http.StatusOK {
		t.Fatalf("GET /%s before post_date = 200, want non-200 (not yet published)", before.Slug)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(env.repos.Scheduled, env.postWrite, 50*time.Millisecond, log)
	schedCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go sched.Run(schedCtx)

	deadline := time.Now().Add(3 * time.Second)
	var pubResp *http.Response
	for time.Now().Before(deadline) {
		pubResp, err = env.client.Get(env.ts.URL + "/" + before.Slug)
		if err != nil {
			t.Fatalf("GET /%s: %v", before.Slug, err)
		}
		if pubResp.StatusCode == http.StatusOK {
			break
		}
		pubResp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	if pubResp == nil || pubResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /%s after waiting for scheduler = %v, want 200", before.Slug, pubResp)
	}
	pubBody, _ := io.ReadAll(pubResp.Body)
	pubResp.Body.Close()
	if !bytes.Contains(pubBody, []byte("Scheduled post")) {
		t.Errorf("public page missing published title; body: %s", pubBody)
	}

	after, err := env.repos.PostWriter.ByID(ctx, postID)
	if err != nil {
		t.Fatalf("ByID after publish: %v", err)
	}
	if after.Status != "publish" {
		t.Fatalf("post status after scheduler = %q, want %q", after.Status, "publish")
	}
	if after.Slug != before.Slug {
		t.Fatalf("post slug changed by publish: before %q, after %q", before.Slug, after.Slug)
	}
	if after.GUID != before.GUID {
		t.Fatalf("post GUID changed by publish: before %q, after %q", before.GUID, after.GUID)
	}
}

// ---------------------------------------------------------------------------
// 7.4: REST term write E2E -- create a category and a tag via the wp-json
// REST API using an Application Password, assign them to a post via the
// admin API, update both via REST, then delete the in-use category via REST
// and confirm it detaches from the post without deleting the post (Req 6).
// ---------------------------------------------------------------------------

// TestM7RESTTermWriteE2E exercises the REST term write-parity story
// end-to-end: Basic-auth (Application Password) REST clients creating,
// assigning, updating, and deleting categories/tags, exactly as an external
// integrator would.
func TestM7RESTTermWriteE2E(t *testing.T) {
	env := newM7Env(t)

	createAppPwBody, _ := json.Marshal(map[string]string{"name": "e2e-m7-rest-terms"})
	appPwResp := env.adminAuthedJSON(http.MethodPost, "/wp-json/wp/v2/users/me/application-passwords", createAppPwBody)
	if appPwResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(appPwResp.Body)
		appPwResp.Body.Close()
		t.Fatalf("create application password = %d, want 201 (body %q)", appPwResp.StatusCode, string(body))
	}
	var appPw struct {
		Password string `json:"password"`
	}
	decodeJSON(t, appPwResp, &appPw)
	if appPw.Password == "" {
		t.Fatal("create application password returned empty secret")
	}

	restClient := env.ts.Client()
	restDo := func(method, path string, body []byte) *http.Response {
		t.Helper()
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, env.ts.URL+path, reader)
		if err != nil {
			t.Fatalf("new request %s %s: %v", method, path, err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.SetBasicAuth("root", appPw.Password)
		resp, err := restClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// Create a category and a tag via REST.
	catBody, _ := json.Marshal(map[string]string{"name": "REST Category"})
	catResp := restDo(http.MethodPost, "/wp-json/wp/v2/categories", catBody)
	if catResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(catResp.Body)
		catResp.Body.Close()
		t.Fatalf("REST create category = %d, want 201 (body %q)", catResp.StatusCode, string(body))
	}
	var cat struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	decodeJSON(t, catResp, &cat)
	if cat.ID == 0 || cat.Slug == "" {
		t.Fatalf("REST create category returned incomplete entity: %+v", cat)
	}

	tagBody, _ := json.Marshal(map[string]string{"name": "REST Tag"})
	tagResp := restDo(http.MethodPost, "/wp-json/wp/v2/tags", tagBody)
	if tagResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(tagResp.Body)
		tagResp.Body.Close()
		t.Fatalf("REST create tag = %d, want 201 (body %q)", tagResp.StatusCode, string(body))
	}
	var tag struct {
		ID int64 `json:"id"`
	}
	decodeJSON(t, tagResp, &tag)
	if tag.ID == 0 {
		t.Fatal("REST create tag returned zero id")
	}

	// Assign both to a post via the admin API's termIds map.
	postID := createM7Post(t, env, "Term parity post", "<p>Body.</p>", "Excerpt.", "term-parity-post")
	getResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10), nil)
	var current struct {
		Modified string `json:"modified"`
	}
	decodeJSON(t, getResp, &current)
	assignResp := env.adminJSON(http.MethodPut, "/admin/api/posts/"+strconv.FormatInt(postID, 10), map[string]any{
		"title": "Term parity post", "content": "<p>Body.</p>", "excerpt": "Excerpt.",
		"slug": "term-parity-post", "status": "publish", "type": "post", "commentStatus": "open",
		"modified": current.Modified,
		"termIds":  map[string][]int64{"category": {cat.ID}, "post_tag": {tag.ID}},
	})
	if assignResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(assignResp.Body)
		assignResp.Body.Close()
		t.Fatalf("assign terms via admin API = %d, want 200 (body %q)", assignResp.StatusCode, string(body))
	}
	assignResp.Body.Close()

	// Update both terms via REST (name and/or slug).
	catUpdateBody, _ := json.Marshal(map[string]string{"name": "REST Category (renamed)"})
	catUpdateResp := restDo(http.MethodPut, "/wp-json/wp/v2/categories/"+strconv.FormatInt(cat.ID, 10), catUpdateBody)
	if catUpdateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(catUpdateResp.Body)
		catUpdateResp.Body.Close()
		t.Fatalf("REST update category = %d, want 200 (body %q)", catUpdateResp.StatusCode, string(body))
	}
	catUpdateResp.Body.Close()

	tagUpdateBody, _ := json.Marshal(map[string]string{"name": "REST Tag (renamed)"})
	tagUpdateResp := restDo(http.MethodPut, "/wp-json/wp/v2/tags/"+strconv.FormatInt(tag.ID, 10), tagUpdateBody)
	if tagUpdateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tagUpdateResp.Body)
		tagUpdateResp.Body.Close()
		t.Fatalf("REST update tag = %d, want 200 (body %q)", tagUpdateResp.StatusCode, string(body))
	}
	tagUpdateResp.Body.Close()

	// Delete the in-use category via REST: 200, and the post must survive
	// with the category detached (Req 6.7).
	catDeleteResp := restDo(http.MethodDelete, "/wp-json/wp/v2/categories/"+strconv.FormatInt(cat.ID, 10), nil)
	catDeleteBody, _ := io.ReadAll(catDeleteResp.Body)
	catDeleteResp.Body.Close()
	if catDeleteResp.StatusCode != http.StatusOK {
		t.Fatalf("REST delete in-use category = %d, want 200 (body %q)", catDeleteResp.StatusCode, string(catDeleteBody))
	}

	postAfterResp := env.adminJSON(http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(postID, 10), nil)
	if postAfterResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(postAfterResp.Body)
		postAfterResp.Body.Close()
		t.Fatalf("GET post after deleting in-use category = %d, want 200 (post must survive) (body %q)", postAfterResp.StatusCode, string(body))
	}
	var postAfter struct {
		Terms map[string][]struct {
			ID int64 `json:"id"`
		} `json:"terms"`
	}
	decodeJSON(t, postAfterResp, &postAfter)
	if cats := postAfter.Terms["category"]; len(cats) != 0 {
		t.Fatalf("post categories after deleting the assigned category = %+v, want empty (detached)", cats)
	}
	if tags := postAfter.Terms["post_tag"]; len(tags) != 1 || tags[0].ID != tag.ID {
		t.Fatalf("post tags after unrelated category delete = %+v, want [%d] (untouched)", tags, tag.ID)
	}
}
