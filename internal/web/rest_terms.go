package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// restTerm is the WP-shaped view of a taxonomy term (Req 6.1): the minimum
// field set WordPress's own /categories and /tags endpoints return. count
// and parent have no backing data source in this milestone -- domain.Term
// carries neither a post-count nor a parent-term-id field, and design.md
// states domain.TermReader/content.TermWriteService stay unchanged from M6
// -- so both are rendered as the literal placeholder 0, matching task 4.1's
// explicit "placeholder parent: 0" and mirroring the same
// documented-placeholder pattern.
type restTerm struct {
	ID       int64  `json:"id"`
	Count    int    `json:"count"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Taxonomy string `json:"taxonomy"`
	Link     string `json:"link"`
	Parent   int64  `json:"parent"`
}

// restTermWriteRequest is the wp-json request body for creating/updating a
// category or tag (Req 6.2/6.3). Slug is optional on create -- an empty
// value is auto-derived from Name (Req 6.2) -- and on update either field
// may be omitted to leave it unchanged (Req 6.3's "name and/or slug").
type restTermWriteRequest struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}

// registerRESTTerms registers the GET/POST .../categories, .../tags
// endpoints and their single-item GET/PUT/PATCH/DELETE counterparts (Req
// 6.1-6.7), reusing content.TermWriteService (M6, unchanged) for writes and
// domain.TermReader (M6, unchanged) for reads. When s.termWrite is nil (no
// WithAdminWrites call configured it) these routes are not registered, same
// as every other optional Server feature.
func (s *Server) registerRESTTerms(r chi.Router) {
	if s.termWrite == nil {
		return
	}
	for _, taxonomy := range []string{content.TaxonomyCategory, taxonomyPostTag} {
		path, single := restTermBase(taxonomy)
		r.Method(http.MethodGet, path, s.handleRESTTermsCollection(taxonomy))
		r.Method(http.MethodGet, single, s.handleRESTTermSingle(taxonomy))
		r.Method(http.MethodPost, path, s.handleRESTTermCreate(taxonomy))
		r.Method(http.MethodPut, single, s.handleRESTTermUpdate(taxonomy))
		r.Method(http.MethodPatch, single, s.handleRESTTermUpdate(taxonomy))
		r.Method(http.MethodDelete, single, s.handleRESTTermDelete(taxonomy))
	}
}

// restTermBase returns the collection and single-item route paths for a
// taxonomy ("category" -> "/categories", "post_tag" -> "/tags").
func restTermBase(taxonomy string) (collection, single string) {
	switch taxonomy {
	case content.TaxonomyCategory:
		return "/categories", "/categories/{id}"
	default:
		return "/tags", "/tags/{id}"
	}
}

// restTermLink builds the term's "link" field (Req 6.1). Categories have a
// real public route (GET /category/{slug}); no equivalent per-tag page
// route exists in this milestone, so tags fall back to a WordPress-style
// query-param link, mirroring content/rest.go's userLink fallback
// convention for entities lacking a dedicated route.
func restTermLink(r *http.Request, taxonomy, slug string) string {
	if taxonomy == content.TaxonomyCategory {
		return restAbs(r, "/category/"+slug)
	}
	return restAbs(r, "/?tag="+slug)
}

// termToREST maps a domain.Term to its wp-json view (Req 6.1).
func termToREST(r *http.Request, t domain.Term) restTerm {
	return restTerm{
		ID:       t.ID,
		Count:    0,
		Name:     t.Name,
		Slug:     t.Slug,
		Taxonomy: t.Taxonomy,
		Link:     restTermLink(r, t.Taxonomy, t.Slug),
		Parent:   0,
	}
}

// termSlugify derives a URL slug from a term name (Req 6.2's "slug
// auto-derived or supplied"), for the create handler when the request body
// omits slug: lowercase, non-alphanumeric runs collapsed to a single
// hyphen, leading/trailing hyphens trimmed. content.TermWriteService and
// TermRepo do no such derivation themselves (design.md's "M6, unchanged"),
// so this lives here, at the REST write handler, matching Req 6.2's
// REST-specific auto-derivation behavior.
func termSlugify(name string) string {
	var b strings.Builder
	prevHyphen := true // trims a leading hyphen
	for _, ch := range strings.ToLower(name) {
		switch {
		case ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9':
			b.WriteRune(ch)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// handleRESTTermsCollection serves GET .../categories, GET .../tags: every
// term of the given taxonomy (Req 6.1). No authentication is required.
func (s *Server) handleRESTTermsCollection(taxonomy string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		terms, err := s.termWrite.ListByTaxonomy(r.Context(), taxonomy)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_terms_failed", "Could not list terms.")
			return
		}
		out := make([]restTerm, len(terms))
		for i, t := range terms {
			out[i] = termToREST(r, t)
		}
		_ = writeRESTResponse(w, r, http.StatusOK, out)
	}
}

// restTermByID resolves id to a domain.Term of the given taxonomy, writing
// a 404 and returning ok=false on a nonexistent ID or a taxonomy/type
// mismatch (Req 6.6, e.g. a category ID requested via /tags/{id}).
func (s *Server) restTermByID(w http.ResponseWriter, r *http.Request, taxonomy string) (domain.Term, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeRESTError(w, http.StatusNotFound, "rest_term_invalid_id", "Invalid term ID.")
		return domain.Term{}, false
	}
	terms, err := s.termWrite.TermsByIDs(r.Context(), []int64{id})
	if err != nil {
		writeRESTError(w, http.StatusInternalServerError, "rest_term_invalid_id", "Could not look up term.")
		return domain.Term{}, false
	}
	if len(terms) == 0 || terms[0].Taxonomy != taxonomy {
		writeRESTError(w, http.StatusNotFound, "rest_term_invalid_id", "Invalid term ID.")
		return domain.Term{}, false
	}
	return terms[0], true
}

// handleRESTTermSingle serves GET .../categories/{id}, GET .../tags/{id}
// (Req 6.1). No authentication is required.
func (s *Server) handleRESTTermSingle(taxonomy string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, ok := s.restTermByID(w, r, taxonomy)
		if !ok {
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, termToREST(r, t))
	}
}

// requireRESTTermCSRF applies the same Application-Password-skips-CSRF
// pattern every other REST write handler uses (Req 7.4/8.7): an
// Application Password skips the CSRF check entirely; a
// session-cookie-authenticated request enforces the M4 X-CSRF-Token
// contract. Returns false (having already written the response) when the
// request must stop here.
func (s *Server) requireRESTTermCSRF(w http.ResponseWriter, r *http.Request) bool {
	if isAppPasswordAuth(r.Context()) {
		return true
	}
	if _, hasSession := sessionFrom(r.Context()); hasSession {
		return s.requireSessionCSRFREST(w, r)
	}
	return true
}

// handleRESTTermCreate handles POST .../categories, POST .../tags (Req
// 6.2). Every write requires manage_categories (Req 6.5); insufficient
// capability is 403, not 404 -- terms have no per-item existence to leak.
func (s *Server) handleRESTTermCreate(taxonomy string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireRESTTermCSRF(w, r) {
			return
		}
		principal, _ := PrincipalFrom(r.Context())
		var body restTermWriteRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid request body.")
			return
		}
		if body.Name == nil || strings.TrimSpace(*body.Name) == "" {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "name is required.")
			return
		}
		slug := ""
		if body.Slug != nil {
			slug = *body.Slug
		}
		if slug == "" {
			slug = termSlugify(*body.Name)
		}
		id, err := s.termWrite.Create(r.Context(), principal, domain.Term{Name: *body.Name, Slug: slug, Taxonomy: taxonomy})
		if err != nil {
			if errors.Is(err, content.ErrForbidden) {
				writeRESTError(w, http.StatusForbidden, "rest_cannot_create", "Sorry, you are not allowed to create terms as this user.")
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_create_failed", "Could not create term.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusCreated, termToREST(r, domain.Term{ID: id, Name: *body.Name, Slug: slug, Taxonomy: taxonomy}))
	}
}

// handleRESTTermUpdate handles PUT/PATCH .../categories/{id},
// .../tags/{id} (Req 6.3). Existence and taxonomy are checked before
// authorization (matching rest_posts.go's documented REST/admin-API
// asymmetry); name and/or slug may be omitted to leave that field
// unchanged.
func (s *Server) handleRESTTermUpdate(taxonomy string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireRESTTermCSRF(w, r) {
			return
		}
		current, ok := s.restTermByID(w, r, taxonomy)
		if !ok {
			return
		}
		var body restTermWriteRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid request body.")
			return
		}
		updated := current
		if body.Name != nil {
			updated.Name = *body.Name
		}
		if body.Slug != nil {
			updated.Slug = *body.Slug
		}
		principal, _ := PrincipalFrom(r.Context())
		if err := s.termWrite.Update(r.Context(), principal, updated); err != nil {
			switch {
			case errors.Is(err, content.ErrForbidden):
				writeRESTError(w, http.StatusForbidden, "rest_cannot_edit", "Sorry, you are not allowed to update this term.")
			case errors.Is(err, domain.ErrNotFound):
				writeRESTError(w, http.StatusNotFound, "rest_term_invalid_id", "Invalid term ID.")
			default:
				writeRESTError(w, http.StatusInternalServerError, "rest_update_failed", "Could not update term.")
			}
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, termToREST(r, updated))
	}
}

// handleRESTTermDelete handles DELETE .../categories/{id},
// .../tags/{id} (Req 6.3/6.7). Existence and taxonomy are checked before
// authorization, same as update. Deleting a term detaches it from any
// posts without deleting those posts (Req 6.7; already guaranteed by
// TermRepo.Delete's existing transactional behavior, unchanged in this
// milestone). Matches WP REST parity: 200 with the deleted item echoed
// back in "previous", same convention rest_posts.go's delete handler uses.
func (s *Server) handleRESTTermDelete(taxonomy string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireRESTTermCSRF(w, r) {
			return
		}
		current, ok := s.restTermByID(w, r, taxonomy)
		if !ok {
			return
		}
		principal, _ := PrincipalFrom(r.Context())
		if err := s.termWrite.Delete(r.Context(), principal, current.ID); err != nil {
			switch {
			case errors.Is(err, content.ErrForbidden):
				writeRESTError(w, http.StatusForbidden, "rest_cannot_edit", "Sorry, you are not allowed to delete this term.")
			case errors.Is(err, domain.ErrNotFound):
				writeRESTError(w, http.StatusNotFound, "rest_term_invalid_id", "Invalid term ID.")
			default:
				writeRESTError(w, http.StatusInternalServerError, "rest_delete_failed", "Could not delete term.")
			}
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, map[string]any{
			"deleted":  true,
			"previous": termToREST(r, current),
		})
	}
}
