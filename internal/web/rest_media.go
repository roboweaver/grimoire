package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// registerRESTMedia registers GET .../media[/{id}] (Req 4.1, 4.4) and 501
// stubs for every write method (Req 4.5).
func (s *Server) registerRESTMedia(r chi.Router) {
	r.Method(http.MethodGet, "/media", s.handleRESTMediaCollection())
	r.Method(http.MethodGet, "/media/{id}", s.handleRESTMediaSingle())
	r.Method(http.MethodPost, "/media", restNotImplemented("rest_cannot_create", "Uploading media"))
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Method(m, "/media/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting media"))
	}
}

// handleRESTMediaCollection serves GET .../media: a paginated collection of
// attachments, optionally filtered to a single post's attachments via
// ?parent= (Req 4.1, 4.4).
func (s *Server) handleRESTMediaCollection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var parent int64
		if raw := q.Get("parent"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid parent parameter.")
				return
			}
			parent = parsed
		}
		paging := parseRESTPaging(r, s.restPerPageMax)
		f := domain.MediaFilter{ParentID: parent, Limit: paging.PerPage, Offset: paging.Offset}
		items, err := s.restMedia.List(r.Context(), f)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not list media.")
			return
		}
		total, err := s.restMedia.Count(r.Context(), domain.MediaFilter{ParentID: parent})
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not count media.")
			return
		}
		out := make([]any, 0, len(items))
		for _, m := range items {
			enveloped, err := s.mapRESTMedia(r, m)
			if err != nil {
				writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map media #"+strconv.FormatInt(m.ID, 10)+".")
				return
			}
			out = append(out, enveloped)
		}
		setRESTPaginationHeaders(w, total, paging.PerPage)
		_ = writeRESTResponse(w, r, http.StatusOK, out)
	}
}

// handleRESTMediaSingle serves GET .../media/{id} (Req 4.1).
func (s *Server) handleRESTMediaSingle() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid media ID.")
			return
		}
		m, err := s.restMedia.ByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid media ID.")
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not look up media.")
			return
		}
		enveloped, err := s.mapRESTMedia(r, m)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map media.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, enveloped)
	}
}

// mapRESTMedia maps a domain.Media through content.RESTMapper.Media, applies
// restAbs to its link/source_url (both relative content-layer paths, Req
// 6.6), and builds its _links (+ _embedded, on ?_embed): self, collection,
// author, and "up" (parent post) when the attachment has one (Req 6.2).
func (s *Server) mapRESTMedia(r *http.Request, m domain.Media) (map[string]any, error) {
	mapped, err := s.restMapper.Media(r.Context(), m)
	if err != nil {
		return nil, err
	}
	mapped.Link = restAbs(r, mapped.Link)
	mapped.SourceURL = restAbs(r, mapped.SourceURL)

	b := newRESTLinks()
	base := "/wp-json/wp/v2/media"
	b.add("self", restAbs(r, base+"/"+strconv.FormatInt(m.ID, 10)))
	b.add("collection", restAbs(r, base))

	embed := embedRequested(r)
	embedded := map[string]any{}
	if m.AuthorID != 0 {
		b.addEmbeddable("author", restAbs(r, "/wp-json/wp/v2/users/"+strconv.FormatInt(m.AuthorID, 10)))
		if embed {
			if u, err := s.restUsers.ByID(r.Context(), m.AuthorID); err == nil {
				if view, err := s.restMapper.User(r.Context(), u, content.RESTContextView); err == nil {
					embedded["author"] = []any{view}
				}
			}
		}
	}
	if m.ParentID != 0 {
		b.addEmbeddable("up", restAbs(r, "/wp-json/wp/v2/posts/"+strconv.FormatInt(m.ParentID, 10)))
		if embed {
			if p, err := s.restPostByID.ByID(r.Context(), m.ParentID); err == nil {
				var mappedParent any
				var perr error
				if p.Type == "page" {
					mappedParent, perr = s.restMapper.Page(r.Context(), p)
				} else {
					mappedParent, perr = s.restMapper.Post(r.Context(), p)
				}
				if perr == nil {
					embedded["up"] = []any{mappedParent}
				}
			}
		}
	}

	if len(embedded) == 0 {
		return withEnvelope(mapped, b.links, nil)
	}
	return withEnvelope(mapped, b.links, embedded)
}
