package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/roboweaver/grimoire/internal/domain"
)

type commentAdminService interface {
	List(ctx context.Context, filter domain.CommentFilter) ([]domain.Comment, int, error)
	Approve(ctx context.Context, id int64) error
	Unapprove(ctx context.Context, id int64) error
	Trash(ctx context.Context, id int64) error
	Untrash(ctx context.Context, id int64) error
	MarkSpam(ctx context.Context, id int64) error
	NotSpam(ctx context.Context, id int64) error
}

// commentExcerptLen bounds the preview text returned alongside a comment's
// full content; design.md does not mandate a derivation algorithm, so this
// mirrors WordPress's plain truncate-to-length behavior.
const commentExcerptLen = 120

func commentExcerpt(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= commentExcerptLen {
		return content
	}
	return strings.TrimSpace(string(runes[:commentExcerptLen])) + "…"
}

// commentItemResponse is the camelCase wire shape web/admin/src/api/types.ts
// expects for a single comment (Req 3).
type commentItemResponse struct {
	ID          int64  `json:"id"`
	PostID      int64  `json:"postId"`
	PostTitle   string `json:"postTitle"`
	Author      string `json:"author"`
	AuthorEmail string `json:"authorEmail"`
	AuthorURL   string `json:"authorUrl"`
	Content     string `json:"content"`
	Excerpt     string `json:"excerpt"`
	Status      string `json:"status"`
	Date        string `json:"date"`
}

type commentsResponse struct {
	Items      []commentItemResponse `json:"items"`
	Page       int                   `json:"page"`
	PerPage    int                   `json:"perPage"`
	Total      int                   `json:"total"`
	TotalPages int                   `json:"totalPages"`
}

// adminComments returns a paginated, capability-gated comment list (Req 3,
// Req 11). Query params: page, perPage, status, postId — mirroring adminPosts.
func (s *Server) adminComments(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	page, perPage, offset := clampPage(atoiDefault(q.Get("page"), 1), atoiDefault(q.Get("perPage"), 0))
	filter := domain.CommentFilter{Limit: perPage, Offset: offset}
	if status := q.Get("status"); status != "" {
		filter.Statuses = []string{status}
	}
	if postID := atoiDefault(q.Get("postId"), 0); postID > 0 {
		filter.PostID = int64(postID)
	}
	items, total, err := s.comments.List(r.Context(), filter)
	if err != nil {
		return err
	}
	// Comment has no PostTitle field; resolve it via the admin post reader,
	// memoized per request so N comments sharing a post cost one lookup.
	titles := map[int64]string{}
	out := make([]commentItemResponse, 0, len(items))
	for _, c := range items {
		title, seen := titles[c.PostID]
		if !seen {
			if s.admin != nil {
				if post, err := s.admin.Detail(r.Context(), c.PostID); err == nil {
					title = post.Title
				}
			}
			titles[c.PostID] = title
		}
		out = append(out, commentItemResponse{
			ID:          c.ID,
			PostID:      c.PostID,
			PostTitle:   title,
			Author:      c.Author,
			AuthorEmail: c.AuthorEmail,
			AuthorURL:   c.AuthorURL,
			Content:     c.Content,
			Excerpt:     commentExcerpt(c.Content),
			Status:      c.Status,
			Date:        c.Date.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	return writeJSON(w, http.StatusOK, commentsResponse{
		Items:      out,
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
	})
}

// applyCommentAction runs the moderation action named by action (the same
// vocabulary the SPA sends both as the legacy {action} path segment and the
// {"status": ...} JSON body of /status) against the comment with the given
// id. Shared by adminCommentAction and adminCommentStatus (Req 4).
func (s *Server) applyCommentAction(ctx context.Context, id int64, action string) error {
	switch action {
	case "approve":
		return s.comments.Approve(ctx, id)
	case "unapprove":
		return s.comments.Unapprove(ctx, id)
	case "spam":
		return s.comments.MarkSpam(ctx, id)
	case "not-spam":
		return s.comments.NotSpam(ctx, id)
	case "trash":
		return s.comments.Trash(ctx, id)
	case "untrash":
		return s.comments.Untrash(ctx, id)
	default:
		return badRequestError{msg: "invalid comment action"}
	}
}

// adminCommentAction is the legacy POST /comments/{id}/{action} route, kept
// for back-compat alongside the newer /status route.
func (s *Server) adminCommentAction(w http.ResponseWriter, r *http.Request) error {
	if !s.requireSessionCSRFJSON(w, r) {
		return nil
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid comment id"}
	}
	action := chi.URLParam(r, "action")
	if err := s.applyCommentAction(r.Context(), id, action); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": action})
}

// adminCommentStatus handles POST /admin/api/comments/{id}/status, the SPA's
// primary moderation route: JSON body {"status": "<action>"} (Req 4).
func (s *Server) adminCommentStatus(w http.ResponseWriter, r *http.Request) error {
	if !s.requireSessionCSRFJSON(w, r) {
		return nil
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid comment id"}
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	if err := s.applyCommentAction(r.Context(), id, body.Status); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": body.Status})
}
