package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// registerRESTUsers registers GET .../users[/{id}], GET .../users/me (Req
// 5.1, 5.2), and 501 stubs for every write method (Req 5.5) on the plain
// /users and /users/{id} paths only — never on a wildcard prefix — so that
// Phase 7's /users/me/application-passwords* routes can be added later
// without any re-plumbing here (those are more specific static path
// segments and chi always prefers a static match over the {id} parameter,
// regardless of registration order).
func (s *Server) registerRESTUsers(r chi.Router) {
	r.Method(http.MethodGet, "/users", s.handleRESTUsersCollection())
	r.Method(http.MethodGet, "/users/me", s.handleRESTUserMe())
	r.Method(http.MethodGet, "/users/{id}", s.handleRESTUserSingle())
	r.Method(http.MethodPost, "/users", restNotImplemented("rest_cannot_create", "Creating a user"))
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Method(m, "/users", restNotImplemented("rest_cannot_edit", "Updating or deleting a user"))
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Method(m, "/users/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting a user"))
	}
}

// restUserContext resolves the WordPress REST "context" for viewing subject:
// RESTContextEdit when the caller has "list_users", or is viewing their own
// record; RESTContextView otherwise (Req 5.1, 5.2).
func restUserContext(r *http.Request, subject int64) content.RESTContext {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		return content.RESTContextView
	}
	if p.Can("list_users") || p.UserID == subject {
		return content.RESTContextEdit
	}
	return content.RESTContextView
}

// handleRESTUsersCollection serves GET .../users: every user's view-context
// fields, widened to edit-context per-item for a caller with "list_users"
// or their own record (Req 5.1, 5.2).
func (s *Server) handleRESTUsersCollection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		paging := parseRESTPaging(r, s.restPerPageMax)
		users, err := s.restUsers.List(r.Context(), paging.Limit, paging.Offset)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not list users.")
			return
		}
		total, err := s.restUsers.Count(r.Context())
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not count users.")
			return
		}
		out := make([]any, 0, len(users))
		for _, u := range users {
			enveloped, err := s.mapRESTUser(r, u, restUserContext(r, u.ID))
			if err != nil {
				writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map user #"+strconv.FormatInt(u.ID, 10)+".")
				return
			}
			out = append(out, enveloped)
		}
		setRESTPaginationHeaders(w, int(total), paging.PerPage)
		_ = writeRESTResponse(w, r, http.StatusOK, out)
	}
}

// handleRESTUserSingle serves GET .../users/{id} (Req 5.1, 5.2).
func (s *Server) handleRESTUserSingle() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeRESTError(w, http.StatusNotFound, "rest_user_invalid_id", "Invalid user ID.")
			return
		}
		u, err := s.restUsers.ByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeRESTError(w, http.StatusNotFound, "rest_user_invalid_id", "Invalid user ID.")
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not look up user.")
			return
		}
		enveloped, err := s.mapRESTUser(r, u, restUserContext(r, u.ID))
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map user.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, enveloped)
	}
}

// handleRESTUserMe serves GET .../users/me: the caller's own record in edit
// context, requiring authentication (Req 5.2; unauthenticated requests get
// WordPress's own 401 rest_not_logged_in).
func (s *Server) handleRESTUserMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeRESTError(w, http.StatusUnauthorized, "rest_not_logged_in", "You are not currently logged in.")
			return
		}
		u, err := s.restUsers.ByID(r.Context(), p.UserID)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not look up user.")
			return
		}
		enveloped, err := s.mapRESTUser(r, u, content.RESTContextEdit)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map user.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, enveloped)
	}
}

// mapRESTUser maps a domain.User through content.RESTMapper.User and builds
// its _links: self and collection only (Req 6.2 lists no user embeds).
func (s *Server) mapRESTUser(r *http.Request, u domain.User, restContext content.RESTContext) (map[string]any, error) {
	mapped, err := s.restMapper.User(r.Context(), u, restContext)
	if err != nil {
		return nil, err
	}
	switch v := mapped.(type) {
	case content.RESTUser:
		v.Link = restAbs(r, v.Link)
		mapped = v
	case content.RESTUserEdit:
		v.Link = restAbs(r, v.Link)
		mapped = v
	}

	b := newRESTLinks()
	base := "/wp-json/wp/v2/users"
	b.add("self", restAbs(r, base+"/"+strconv.FormatInt(u.ID, 10)))
	b.add("collection", restAbs(r, base))

	return withEnvelope(mapped, b.links, nil)
}
