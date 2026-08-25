package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
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
}

// termDetailResponse is the response shape for term create/update/list (Req
// 2.3/2.4). Taxonomy is only populated where known: always on create/list,
// and echoed back from the request body on update since no single-term
// read-back port exists to confirm it independently.
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
	id, err := s.termWrite.Create(r.Context(), principal, domain.Term{
		Name:     body.Name,
		Slug:     body.Slug,
		Taxonomy: body.Taxonomy,
	})
	if err != nil {
		return err
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
	if body.Name == "" || body.Slug == "" {
		return badRequestError{msg: "name and slug are required"}
	}
	if err := s.termWrite.Update(r.Context(), principal, domain.Term{ID: id, Name: body.Name, Slug: body.Slug}); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, termDetailResponse{ID: id, Name: body.Name, Slug: body.Slug, Taxonomy: body.Taxonomy})
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
