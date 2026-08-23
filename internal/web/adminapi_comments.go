package web

import (
	"context"
	"net/http"
	"strconv"

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

func (s *Server) adminComments(w http.ResponseWriter, r *http.Request) error {
	items, _, err := s.comments.List(r.Context(), domain.CommentFilter{})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminCommentAction(w http.ResponseWriter, r *http.Request) error {
	if !s.requireSessionCSRF(w, r) {
		return nil
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid comment id"}
	}
	action := chi.URLParam(r, "action")
	switch action {
	case "approve":
		err = s.comments.Approve(r.Context(), id)
	case "unapprove":
		err = s.comments.Unapprove(r.Context(), id)
	case "spam":
		err = s.comments.MarkSpam(r.Context(), id)
	case "not-spam":
		err = s.comments.NotSpam(r.Context(), id)
	case "trash":
		err = s.comments.Trash(r.Context(), id)
	case "untrash":
		err = s.comments.Untrash(r.Context(), id)
	default:
		return badRequestError{msg: "invalid comment action"}
	}
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": action})
}
