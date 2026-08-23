package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
)

// newTestCommentAdminServer builds a *Server backed by a real migrated and
// fixture-seeded SQLite database, wired with a live *content.CommentService
// exactly as production does (WithContentFeatures), so the HTTP-boundary
// status-vocabulary translation in adminComments is exercised against the
// real comment_approved storage values ("0"/"1"/"spam"/"trash") rather than a
// fake. storagetest fixtures seed comment 101 (post 1, approved/"1"), 102
// (post 1, held/"0"), 103 (post 2, spam), 104 (post 2, trash).
func newTestCommentAdminServer(t *testing.T) *Server {
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
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	return &Server{log: slog.Default(), comments: comments}
}

func decodeCommentsResponse(t *testing.T, rec *httptest.ResponseRecorder) commentsResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body commentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec.Body.String())
	}
	return body
}

// TestAdminCommentsFilterByStatusPending proves GET
// /admin/api/comments?status=pending maps the SPA's "pending" vocabulary to
// the storage layer's held value ("0") before filtering, so it returns
// exactly the held comments instead of always empty.
func TestAdminCommentsFilterByStatusPending(t *testing.T) {
	s := newTestCommentAdminServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/comments?status=pending", nil).
		WithContext(principalCtx("moderate_comments"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminComments).ServeHTTP(rec, req)

	body := decodeCommentsResponse(t, rec)
	if len(body.Items) != 1 || body.Items[0].ID != 102 {
		t.Fatalf("status=pending items = %+v, want exactly [id=102]", body.Items)
	}
	if body.Items[0].Status != "pending" {
		t.Fatalf("item status = %q, want %q", body.Items[0].Status, "pending")
	}
}

// TestAdminCommentsFilterByStatusApproved proves GET
// /admin/api/comments?status=approved maps to the storage layer's approved
// value ("1") and returns exactly the approved comments.
func TestAdminCommentsFilterByStatusApproved(t *testing.T) {
	s := newTestCommentAdminServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/comments?status=approved", nil).
		WithContext(principalCtx("moderate_comments"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminComments).ServeHTTP(rec, req)

	body := decodeCommentsResponse(t, rec)
	if len(body.Items) != 1 || body.Items[0].ID != 101 {
		t.Fatalf("status=approved items = %+v, want exactly [id=101]", body.Items)
	}
	if body.Items[0].Status != "approved" {
		t.Fatalf("item status = %q, want %q", body.Items[0].Status, "approved")
	}
}

// TestAdminCommentDTOStatusIsSemanticNeverRaw proves the outbound comment DTO
// always serializes status as one of the SPA's semantic values
// ("pending"/"approved"/"spam"/"trash") and never leaks the raw
// comment_approved storage values ("0"/"1") that internal/content/comments.go
// stores internally.
func TestAdminCommentDTOStatusIsSemanticNeverRaw(t *testing.T) {
	s := newTestCommentAdminServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/comments", nil).
		WithContext(principalCtx("moderate_comments"))
	rec := httptest.NewRecorder()
	s.jsonHandler(s.adminComments).ServeHTTP(rec, req)

	body := decodeCommentsResponse(t, rec)
	if len(body.Items) != 4 {
		t.Fatalf("items = %+v, want 4 fixture comments", body.Items)
	}
	allowed := map[string]bool{"pending": true, "approved": true, "spam": true, "trash": true}
	for _, item := range body.Items {
		if !allowed[item.Status] {
			t.Fatalf("comment %d status = %q, want one of pending/approved/spam/trash", item.ID, item.Status)
		}
		if item.Status == "0" || item.Status == "1" {
			t.Fatalf("comment %d status leaked raw storage value %q", item.ID, item.Status)
		}
	}
}
