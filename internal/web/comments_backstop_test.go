package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// newBackstopServer builds the same full server as newCommentServer (real
// migrated+seeded SQLite store, live CommentService) but ALSO returns the
// *storage.Repositories so the test can reach the raw {prefix}comments table.
// Req 9.15 requires a comment row "inserted directly into storage, simulating a
// pre-M10a row that never passed through the Policy", which is exactly a direct
// SQL insert that bypasses CommentService.Create's write-boundary sanitization.
func newBackstopServer(t *testing.T) (http.Handler, *storage.Repositories, config.DatabaseConfig) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: root + "/grimoire.db", TablePrefix: "wp_"}
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
	eng, err := render.Load("../../themes", "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())
	menus := content.NewNavMenuService(repos.NavMenus, "default")
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: root + "/uploads", BaseURL: "/wp-content/uploads"})
	h := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithContentFeatures(comments, media, menus).Routes()
	return h, repos, cfg
}

// seedRawComment inserts an approved ("1") comment straight into
// {prefix}comments with raw SQL, no CommentService and therefore no
// sanitize.Policy on the way in. This is the pre-M10a row of Req 9.15: a row
// that was approved before the write-boundary policy existed and still carries
// whatever markup it was stored with.
func seedRawComment(t *testing.T, repos *storage.Repositories, cfg config.DatabaseConfig, id, postID int64, contentHTML string) {
	t.Helper()
	q := `INSERT INTO ` + cfg.TablePrefix + `comments ` +
		`(` + rebind.IdentList(cfg.Vendor, []string{
		"comment_ID", "comment_post_ID", "comment_author", "comment_author_email",
		"comment_author_url", "comment_author_IP", "comment_date", "comment_date_gmt",
		"comment_content", "comment_approved", "comment_agent", "comment_parent", "user_id",
	}) + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := []any{
		id, postID, "PreM10a", "pre@example.com", "", "198.51.100.9",
		"2024-02-01 10:00:00", "2024-02-01 10:00:00", contentHTML, "1", "Browser Z", 0, 0,
	}
	if _, err := repos.DB().ExecContext(context.Background(), rebind.Rebind(cfg.Vendor, q), args...); err != nil {
		t.Fatalf("seed raw comment: %v", err)
	}
}

// readRawCommentContent reads comment_content back out of storage with raw SQL,
// so the "no write-back" assertion compares against the actual stored bytes
// rather than anything the render path returned.
func readRawCommentContent(t *testing.T, repos *storage.Repositories, cfg config.DatabaseConfig, id int64) string {
	t.Helper()
	q := `SELECT comment_content FROM ` + cfg.TablePrefix + `comments WHERE ` +
		rebind.Ident(cfg.Vendor, "comment_ID") + ` = ?`
	var got string
	if err := repos.DB().QueryRowContext(context.Background(), rebind.Rebind(cfg.Vendor, q), id).Scan(&got); err != nil {
		t.Fatalf("read raw comment: %v", err)
	}
	return got
}

// TestStoredCommentRenderBackstopSanitizesTierA is the Req 9.15 safety-critical
// backstop proof (Req 6.9, 6.10, 6.11; Finding 2).
//
// A comment row is inserted DIRECTLY into storage with comment_approved "1" and
// content carrying both a disallowed <script> and an allow-listed <em>,
// simulating a pre-M10a approved row that never passed through the
// write-boundary sanitize.Policy. The post it is attached to (post 1, slug
// "hello-1") is published and lists approved comments
// (handlers.go: Statuses []string{"1"}), so the row reaches commentView on the
// read path.
//
// The backstop (task 6.4) makes commentView sanitize the stored content at
// tier A UNCONDITIONALLY -- independent of the rendering request's principal and
// of the tier the value was written at, because provenance is not tracked. The
// assertions:
//
//  1. No live <script> element survives in the rendered page (the backstop
//     stripped it) -- this is the behavior that html.EscapeString provided
//     before M10a and that removing it (Req 6.1) would otherwise lose, letting a
//     pre-M10a <script> render as live markup.
//  2. The allow-listed <em> SURVIVES as real markup -- the backstop is tier A,
//     not a blanket escape.
//  3. The stored bytes are UNCHANGED after rendering: the backstop sanitizes on
//     the READ path only and performs no write-back / repair of the row.
//
// It is written RED first (task 6.3): internal/web does not yet compile (tasks
// 6.4/7.2/7.5/8.2 pending) and, once compiling, commentView still escapes the
// stored content (pre-6.4) so <em> is escaped rather than surviving. Task 6.4
// lands the backstop that turns this green.
func TestStoredCommentRenderBackstopSanitizesTierA(t *testing.T) {
	h, repos, cfg := newBackstopServer(t)

	const stored = `<script>alert(1)</script><em>keep</em>`
	const commentID int64 = 900
	const postID int64 = 1 // fixture slug "hello-1", published, comments open
	seedRawComment(t, repos, cfg, commentID, postID, stored)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /hello-1 status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// 1. The <script> must NOT survive as a live element. A verbatim passthrough
	//    (the wrong behavior) would leave "<script>alert(1)</script>" intact; the
	//    tier-A backstop removes the script element entirely.
	if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, "<script>") {
		t.Errorf("rendered body contains a live <script>; tier-A backstop did not strip it:\n%s", body)
	}

	// 2. The allow-listed <em> must survive as REAL markup (not escaped). A
	//    blanket html.EscapeString would emit "&lt;em&gt;keep&lt;/em&gt;" and
	//    this assertion would fail -- that is the intended RED before task 6.4.
	if !strings.Contains(body, "<em>keep</em>") {
		t.Errorf("rendered body missing surviving allow-listed <em>keep</em>:\n%s", body)
	}

	// 3. No write-back: the backstop is a read-path transform only. The stored
	//    row must be byte-identical to what was seeded.
	if after := readRawCommentContent(t, repos, cfg, commentID); after != stored {
		t.Errorf("stored comment_content changed after render (write-back occurred):\n got  %q\n want %q", after, stored)
	}
}
