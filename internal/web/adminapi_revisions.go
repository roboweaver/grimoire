package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
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

// autosaveAdminService is the narrow surface the autosave routes depend on;
// *content.AutosaveService satisfies it.
type autosaveAdminService interface {
	Newer(ctx context.Context, actor auth.Principal, parentID int64) (domain.Post, bool, error)
	Save(ctx context.Context, actor auth.Principal, parentID int64, fields content.AutosaveFields) (domain.Post, error)
}

// autosaveResponse is the title/content/excerpt/modified shape both
// GET .../autosave (Req 3.5) and POST .../autosave (Req 3.6) return.
type autosaveResponse struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	Excerpt  string `json:"excerpt"`
	Modified string `json:"modified"`
}

// autosaveWriteRequest is the JSON body shape POST .../autosave accepts
// (Req 3.1: title/content/excerpt only).
type autosaveWriteRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Excerpt string `json:"excerpt"`
}

// adminAutosaveGet handles GET /admin/api/posts/{id}/autosave (Req 3.5): 200
// with the autosave's fields when one exists and is newer than the post,
// otherwise a uniform 404 (no autosave, not newer, unauthorized, and
// nonexistent-post are all indistinguishable).
func (s *Server) adminAutosaveGet(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	parentID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || parentID <= 0 {
		return badRequestError{msg: "invalid post id"}
	}
	autosave, found, err := s.autosave.Newer(r.Context(), principal, parentID)
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrNotFound
	}
	return writeJSON(w, http.StatusOK, autosaveResponse{
		Title:    autosave.Title,
		Content:  autosave.Content,
		Excerpt:  autosave.Excerpt,
		Modified: autosave.Modified.UTC().Format(adminDateLayout),
	})
}

// adminAutosaveSave handles POST /admin/api/posts/{id}/autosave (Req 3.6):
// upserts the caller's single autosave row for the post, so a second call
// for the same post+caller updates the same row rather than appending to
// the revisions list.
func (s *Server) adminAutosaveSave(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	parentID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || parentID <= 0 {
		return badRequestError{msg: "invalid post id"}
	}
	var body autosaveWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	saved, err := s.autosave.Save(r.Context(), principal, parentID, content.AutosaveFields{
		Title:   body.Title,
		Content: body.Content,
		Excerpt: body.Excerpt,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, autosaveResponse{
		Title:    saved.Title,
		Content:  saved.Content,
		Excerpt:  saved.Excerpt,
		Modified: saved.Modified.UTC().Format(adminDateLayout),
	})
}
