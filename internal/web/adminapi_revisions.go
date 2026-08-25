package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// revisionAdminService is the narrow surface the revision routes depend on;
// *content.RevisionWriteService satisfies it.
type revisionAdminService interface {
	List(ctx context.Context, actor auth.Principal, parentID int64) ([]domain.RevisionMeta, error)
	Get(ctx context.Context, actor auth.Principal, parentID, revisionID int64) (domain.Post, error)
	Restore(ctx context.Context, actor auth.Principal, parentID, revisionID int64) (domain.Post, error)
}

// revisionSummary is the no-content-body shape GET .../revisions returns for
// each entry, newest first (Req 2.1).
type revisionSummary struct {
	ID       int64  `json:"id"`
	Author   int64  `json:"author"`
	Modified string `json:"modified"`
}

// revisionDetail is the full-body shape GET .../revisions/{revisionId}
// returns (Req 2.2).
type revisionDetail struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Excerpt  string `json:"excerpt"`
	Modified string `json:"modified"`
}

// parseRevisionRouteIDs parses the {id} (parent post) and {revisionId} chi
// URL params shared by every revision route.
func parseRevisionRouteIDs(r *http.Request) (parentID, revisionID int64, err error) {
	parentID, err = strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || parentID <= 0 {
		return 0, 0, badRequestError{msg: "invalid post id"}
	}
	revisionID, err = strconv.ParseInt(chi.URLParam(r, "revisionId"), 10, 64)
	if err != nil || revisionID <= 0 {
		return 0, 0, badRequestError{msg: "invalid revision id"}
	}
	return parentID, revisionID, nil
}

// adminRevisionList handles GET /admin/api/posts/{id}/revisions (Req 2.1).
func (s *Server) adminRevisionList(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	parentID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || parentID <= 0 {
		return badRequestError{msg: "invalid post id"}
	}
	metas, err := s.revisions.List(r.Context(), principal, parentID)
	if err != nil {
		return err
	}
	resp := make([]revisionSummary, len(metas))
	for i, m := range metas {
		resp[i] = revisionSummary{ID: m.ID, Author: m.Author, Modified: m.Modified.UTC().Format(adminDateLayout)}
	}
	return writeJSON(w, http.StatusOK, resp)
}

// adminRevisionGet handles GET /admin/api/posts/{id}/revisions/{revisionId}
// (Req 2.2).
func (s *Server) adminRevisionGet(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	parentID, revisionID, err := parseRevisionRouteIDs(r)
	if err != nil {
		return err
	}
	rev, err := s.revisions.Get(r.Context(), principal, parentID, revisionID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, revisionDetail{
		ID:       rev.ID,
		Title:    rev.Title,
		Content:  rev.Content,
		Excerpt:  rev.Excerpt,
		Modified: rev.Modified.UTC().Format(adminDateLayout),
	})
}

// adminRevisionRestore handles POST
// /admin/api/posts/{id}/revisions/{revisionId}/restore (Req 2.3).
func (s *Server) adminRevisionRestore(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	parentID, revisionID, err := parseRevisionRouteIDs(r)
	if err != nil {
		return err
	}
	restored, err := s.revisions.Restore(r.Context(), principal, parentID, revisionID)
	if err != nil {
		return err
	}
	resp, err := s.postDetail(r.Context(), restored)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}
