package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// termAdminService is the write+read surface adminTerms/adminTermCreate/
// Update/Delete depend on; *content.TermWriteService satisfies it.
type termAdminService interface {
	Create(ctx context.Context, actor auth.Principal, t domain.Term) (int64, error)
	Update(ctx context.Context, actor auth.Principal, t domain.Term) error
	Delete(ctx context.Context, actor auth.Principal, id int64) error
	ListByTaxonomy(ctx context.Context, taxonomy string) ([]domain.Term, error)
	TermsByIDs(ctx context.Context, ids []int64) ([]domain.Term, error)
}

// termWriteRequest is the JSON body shape POST/PUT /admin/api/terms accept
// (Req 2.3). Taxonomy is required on create and ignored on update/rename,
// since TermWriteService.Update (like the underlying TermRepo) only ever
// renames name/slug — taxonomy is immutable once a term exists (moving a
// term to a different taxonomy requires delete-and-recreate).
type termWriteRequest struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Taxonomy string `json:"taxonomy"`
	// Parent is a *int64 so an omitted key (nil) is distinct from an explicit
	// 0 (top-level), matching the omitted-vs-explicit-zero posture the REST DTO
	// uses (M13 D2, Req 1.1/2.4). On create nil/0 both mean top-level; on
	// update nil leaves the stored parent unchanged (Req 2.2) and 0 promotes to
	// top-level (Req 2.3).
	Parent *int64 `json:"parent"`
}

// termDetailResponse is the response shape for term create/update/list (Req
// 2.3/2.4). Taxonomy is populated from the create body and, on update, from the
// load-and-merge pre-read of the stored term (M13) rather than the request body,
// so it reflects the term's real taxonomy.
type termDetailResponse struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Taxonomy string `json:"taxonomy,omitempty"`
}

type termsListResponse struct {
	Items []termSummary `json:"items"`
}

// adminTerms handles GET /admin/api/terms?taxonomy=... (Req 2.4).
func (s *Server) adminTerms(w http.ResponseWriter, r *http.Request) error {
	taxonomy := r.URL.Query().Get("taxonomy")
	if taxonomy == "" {
		return badRequestError{msg: "taxonomy query parameter is required"}
	}
	if !isKnownAdminTaxonomy(taxonomy) {
		return badRequestError{msg: "unknown taxonomy"}
	}
	terms, err := s.termWrite.ListByTaxonomy(r.Context(), taxonomy)
	if err != nil {
		return err
	}
	items := make([]termSummary, 0, len(terms))
	for _, t := range terms {
		items = append(items, termSummary{ID: t.ID, Name: t.Name, Slug: t.Slug})
	}
	return writeJSON(w, http.StatusOK, termsListResponse{Items: items})
}

// adminTermCreate handles POST /admin/api/terms (Req 2.3).
func (s *Server) adminTermCreate(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	var body termWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	if body.Name == "" || body.Slug == "" || body.Taxonomy == "" {
		return badRequestError{msg: "name, slug, and taxonomy are required"}
	}
	var parentID int64 // top-level when omitted or 0 (Req 1.3, 4.4)
	if body.Parent != nil {
		parentID = *body.Parent
	}
	id, err := s.termWrite.Create(r.Context(), principal, domain.Term{
		Name:     body.Name,
		Slug:     body.Slug,
		Taxonomy: body.Taxonomy,
		ParentID: parentID,
	})
	if err != nil {
		if errors.Is(err, content.ErrTermParentCycle) || errors.Is(err, content.ErrTermParentInvalid) {
			return badRequestError{msg: "invalid parent term"} // -> 400 via jsonHandler (Req 3.3, 4.1-4.3)
		}
		return err // ErrForbidden -> 403 unchanged (Req 5.2)
	}
	return writeJSON(w, http.StatusCreated, termDetailResponse{
		ID:       id,
		Name:     body.Name,
		Slug:     body.Slug,
		Taxonomy: body.Taxonomy,
	})
}

// adminTermUpdate handles PUT /admin/api/terms/{id} (Req 2.3; 404 on a
// nonexistent term per Req 2.5).
func (s *Server) adminTermUpdate(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return badRequestError{msg: "invalid term id"}
	}
	var body termWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	// Load-and-merge (M13 Finding 3): pre-read the current term. This one read
	// serves three needs -- the term's taxonomy (for validateParent, Req 4), its
	// current parent (for omitted-parent "unchanged", Req 2.2), and its current
	// name/slug (so a parent-only update keeps them). An unknown id -> 404 via
	// jsonHandler (Req 2.5).
	terms, err := s.termWrite.TermsByIDs(r.Context(), []int64{id})
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		return domain.ErrNotFound
	}
	cur := terms[0]

	// Relaxed rule: a parent-only update is allowed; require at least one of
	// name / slug / parent, otherwise the request changes nothing (Req 2.1/2.2).
	if body.Name == "" && body.Slug == "" && body.Parent == nil {
		return badRequestError{msg: "at least one of name, slug, or parent is required"}
	}

	merged := cur // carries cur.Taxonomy and cur.ParentID
	if body.Name != "" {
		merged.Name = body.Name
	}
	if body.Slug != "" {
		merged.Slug = body.Slug
	}
	if body.Parent != nil {
		merged.ParentID = *body.Parent // explicit (incl. 0); else unchanged (Req 2.2/2.3)
	}
	if err := s.termWrite.Update(r.Context(), principal, merged); err != nil {
		if errors.Is(err, content.ErrTermParentCycle) || errors.Is(err, content.ErrTermParentInvalid) {
			return badRequestError{msg: "invalid parent term"} // -> 400 via jsonHandler (Req 3.3, 4.1-4.3)
		}
		return err // ErrForbidden -> 403, ErrNotFound -> 404, else 500
	}
	// Echo the merged fields -- the real pre-read taxonomy, not the request body
	// (Req 6.2).
	return writeJSON(w, http.StatusOK, termDetailResponse{
		ID: id, Name: merged.Name, Slug: merged.Slug, Taxonomy: merged.Taxonomy,
	})
}

// adminTermDelete handles DELETE /admin/api/terms/{id} (Req 2.3).
func (s *Server) adminTermDelete(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return badRequestError{msg: "invalid term id"}
	}
	if err := s.termWrite.Delete(r.Context(), principal, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
