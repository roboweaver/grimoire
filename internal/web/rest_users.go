package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
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
	// POST /users routes to the real create handler once the write-capable
	// UserService is wired (Req 1, 9.3); without it the 501 stub stands. The
	// remaining write verbs below are completed by task 5.5's full nil-gate.
	if s.userWrite != nil {
		r.Method(http.MethodPost, "/users", s.handleRESTUserCreate())
	} else {
		r.Method(http.MethodPost, "/users", restNotImplemented("rest_cannot_create", "Creating a user"))
	}
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Method(m, "/users", restNotImplemented("rest_cannot_edit", "Updating or deleting a user"))
	}
	// PUT/PATCH/POST /users/{id} route to the real update handler once the
	// write-capable UserService is wired (Req 7.1); without it the 501 stub
	// stands. DELETE /users/{id} remains a 501 stub (task 5.5 finalizes the
	// nil-gate layout).
	if s.userWrite != nil {
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
			r.Method(m, "/users/{id}", s.handleRESTUserUpdate())
		}
		r.Method(http.MethodDelete, "/users/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting a user"))
	} else {
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			r.Method(m, "/users/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting a user"))
		}
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

// restUserCreateBody is the POST .../users request body in WordPress's own REST
// JSON vocabulary (Req 1.1). username/email/password are required (Req 1.3);
// roles is a plain slice whose mere presence triggers the role-assignment gate
// (Req 6.1); every other field is optional. Pointers let the handler
// distinguish an omitted field from one explicitly set to its zero value, the
// same posture restPostWriteRequest uses.
type restUserCreateBody struct {
	Username    *string  `json:"username"`
	Email       *string  `json:"email"`
	Password    *string  `json:"password"`
	Name        *string  `json:"name"`        // display_name
	Slug        *string  `json:"slug"`        // user_nicename
	URL         *string  `json:"url"`         // user_url
	Description *string  `json:"description"` // sanitized via UpdateProfile (Req 1.7)
	Roles       []string `json:"roles"`
}

// handleRESTUserCreate serves POST .../users (Req 1). Auth matches
// rest_posts.go/rest_media.go verbatim: an Application Password skips CSRF
// (isAppPasswordAuth); a session enforces M4 CSRF via requireSessionCSRFREST →
// 403 rest_forbidden (Req 10.1, 10.2). Required fields (username/email/password)
// are validated before any store call (Req 1.3); the role gate runs before any
// write (Req 6.1); duplicate login/email are reported in that order, returning
// on the first conflict so a both-duplicate request reports only
// existing_user_login (Req 1.4, 1.5; Finding 1). The reused UserService.Create
// owns the create_users gate, the bcrypt provision, and the role write; a
// supplied description is sanitized + persisted via UserService.UpdateProfile on
// the new ID so create and update share one description path (Req 1.7). On
// success it re-reads the stored user and responds 201 with the same
// edit-context envelope GET /users/{id} returns (Req 1.2).
func (s *Server) handleRESTUserCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAppPasswordAuth(r.Context()) {
			if _, hasSession := sessionFrom(r.Context()); hasSession {
				if !s.requireSessionCSRFREST(w, r) {
					return
				}
			}
		}
		principal, _ := PrincipalFrom(r.Context())

		var body restUserCreateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid request body.") // Req 7.5
			return
		}

		// Required fields (Req 1.3).
		if body.Username == nil || *body.Username == "" ||
			body.Email == nil || *body.Email == "" ||
			body.Password == nil || *body.Password == "" {
			writeRESTError(w, http.StatusBadRequest, "rest_missing_callback_param",
				"Missing parameter(s): username, email, password.")
			return
		}

		// Role gate BEFORE any write (Req 6.1): a create carrying roles requires
		// edit_users AND promote_users. A create is never a self-record, so the
		// no-self-promote rule does not apply here.
		if len(body.Roles) > 0 && (!auth.CanEditUsers(principal) || !auth.CanPromoteUsers(principal)) {
			writeRESTError(w, http.StatusForbidden, "rest_cannot_edit_roles",
				"Sorry, you are not allowed to assign roles to users.")
			return
		}

		// Duplicate detection, username first (Req 1.4) then email (Req 1.5),
		// returning on the first conflict so a both-duplicate request reports
		// only existing_user_login (Finding 1).
		if _, err := s.restUsers.ByLogin(r.Context(), *body.Username); err == nil {
			writeRESTError(w, http.StatusBadRequest, "existing_user_login",
				"Sorry, that username already exists!")
			return
		} else if !errors.Is(err, domain.ErrNotFound) {
			writeRESTError(w, http.StatusInternalServerError, "rest_user_create_failed", "Could not look up user by login.")
			return
		}
		if _, err := s.restUsers.ByEmail(r.Context(), *body.Email); err == nil {
			writeRESTError(w, http.StatusBadRequest, "existing_user_email",
				"Sorry, that email address is already used!")
			return
		} else if !errors.Is(err, domain.ErrNotFound) {
			writeRESTError(w, http.StatusInternalServerError, "rest_user_create_failed", "Could not look up user by email.")
			return
		}

		// Derive user_nicename and display_name sensibly: an explicit slug/name
		// wins, else fall back to the login, matching WordPress's own create
		// defaults.
		nicename := *body.Username
		if body.Slug != nil {
			nicename = *body.Slug
		}
		display := *body.Username
		if body.Name != nil {
			display = *body.Name
		}

		id, err := s.userWrite.Create(r.Context(), principal, domain.User{
			Login:       *body.Username,
			Nicename:    nicename,
			Email:       *body.Email,
			DisplayName: display,
			URL:         derefOr(body.URL, ""),
		}, *body.Password, body.Roles...)
		if err != nil {
			if errors.Is(err, content.ErrForbidden) {
				writeRESTError(w, http.StatusForbidden, "rest_cannot_create",
					"Sorry, you are not allowed to create users.") // Req 5.1, 5.6
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_user_create_failed", "Could not create user.")
			return
		}

		// A supplied description is sanitized + persisted via UpdateProfile on the
		// new ID, so create and update share one description path (Req 1.7). The
		// actor is the creator; a policy error maps to 400, otherwise 500.
		if body.Description != nil {
			if _, err := s.userWrite.UpdateProfile(r.Context(), principal, id, content.UserProfileUpdate{Description: body.Description}); err != nil {
				if errors.Is(err, content.ErrForbidden) {
					writeRESTError(w, http.StatusForbidden, "rest_cannot_create",
						"Sorry, you are not allowed to create users.")
					return
				}
				if errors.Is(err, domain.ErrNotFound) {
					writeRESTError(w, http.StatusInternalServerError, "rest_user_create_failed", "Could not persist user description.")
					return
				}
				writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid description.") // policy error (Req 4.4)
				return
			}
		}

		u, err := s.restUsers.ByID(r.Context(), id)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_user_create_failed", "Could not look up user after create.")
			return
		}
		enveloped, err := s.mapRESTUser(r, u, content.RESTContextEdit)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map user.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusCreated, enveloped)
	}
}

// derefOr returns *p when p is non-nil, else def. It keeps the create handler's
// optional-field handoff to domain.User a single expression.
func derefOr(p *string, def string) string {
	if p != nil {
		return *p
	}
	return def
}

// restUserUpdateBody is the PUT/PATCH/POST .../users/{id} request body in
// WordPress's own REST JSON vocabulary (Req 7). Pointers distinguish an omitted
// field from one explicitly set to its zero value, giving partial-update
// semantics (Req 7.2); roles is a plain string slice whose presence triggers the
// promote gate (Req 6). A wrong-typed JSON value for any *string or []string
// field makes json.Decode fail → 400 rest_invalid_param (Req 7.5), so a
// non-array roles value is rejected before parseRESTUserUpdate runs.
type restUserUpdateBody struct {
	Name        *string  `json:"name"`        // display_name
	Email       *string  `json:"email"`       // user_email
	URL         *string  `json:"url"`         // user_url
	Slug        *string  `json:"slug"`        // user_nicename (Req 7.3)
	Description *string  `json:"description"` // sanitized via UpdateProfile (Req 4.1)
	Password    *string  `json:"password"`    // SetPassword (Req 2.1)
	Roles       []string `json:"roles"`       // SetRoles, promote-gated (Req 6)
}

// parseRESTUserUpdate validates the supplied field formats and builds the
// content.UserProfileUpdate the service owns (name/email/url/slug/description) —
// password and roles are handled separately by the handler (Req 3.5). It mirrors
// rest_media.go's typed-body validation: a malformed slug/email/url yields
// ("rest_invalid_param", error) → 400 (Req 7.5); roles-as-string-array is already
// enforced by JSON decode, so it is not re-checked here. On success it returns
// the mapped update and an empty code with a nil error.
func parseRESTUserUpdate(body restUserUpdateBody) (content.UserProfileUpdate, string, error) {
	// slug must be a non-empty identifier (reject empty/whitespace, Req 7.3).
	if body.Slug != nil && strings.TrimSpace(*body.Slug) == "" {
		return content.UserProfileUpdate{}, "rest_invalid_param", fmt.Errorf("Invalid slug: must be a non-empty identifier.")
	}
	// email must be well-formed when supplied.
	if body.Email != nil {
		if _, err := mail.ParseAddress(*body.Email); err != nil {
			return content.UserProfileUpdate{}, "rest_invalid_param", fmt.Errorf("Invalid email address.")
		}
	}
	// url, when non-empty, must be parseable as an absolute URL.
	if body.URL != nil && *body.URL != "" {
		if u, err := url.Parse(*body.URL); err != nil || u.Scheme == "" || u.Host == "" {
			return content.UserProfileUpdate{}, "rest_invalid_param", fmt.Errorf("Invalid URL.")
		}
	}
	return content.UserProfileUpdate{
		Name:        body.Name,
		Email:       body.Email,
		URL:         body.URL,
		Slug:        body.Slug,
		Description: body.Description,
	}, "", nil
}

// handleRESTUserUpdate serves PUT/PATCH/POST .../users/{id} (Req 7). Auth matches
// handleRESTUserCreate verbatim: an Application Password skips CSRF
// (isAppPasswordAuth); a session enforces M4 CSRF via requireSessionCSRFREST →
// 403 rest_forbidden (Req 10.1, 10.2). {id} must parse to a positive int, else
// 404 rest_user_invalid_id (Req 7.4). The role gate runs FIRST, before any
// profile write (Req 6, 11.3): a roles change on the caller's own record is a
// self-promote → 403 rest_cannot_edit_roles (Req 6.2); otherwise it requires
// edit_users AND promote_users → 403 rest_cannot_edit_roles (Req 6.1). The
// handler then orchestrates UpdateProfile (profile fields + description),
// SetPassword (a supplied password), and SetRoles (permitted roles), re-reads via
// s.restUsers.ByID, and envelopes through mapRESTUser in edit context → 200
// (Req 7.1, 7.6). UpdateProfile is always called — even for a password-only or
// roles-only body — so the stored-row load, the self-vs-others gate, and the 404
// apply uniformly; it issues no users-table write when no profile field is set.
func (s *Server) handleRESTUserUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAppPasswordAuth(r.Context()) {
			if _, hasSession := sessionFrom(r.Context()); hasSession {
				if !s.requireSessionCSRFREST(w, r) {
					return
				}
			}
		}
		principal, _ := PrincipalFrom(r.Context())

		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeRESTError(w, http.StatusNotFound, "rest_user_invalid_id", "Invalid user ID.") // Req 7.4
			return
		}

		var body restUserUpdateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid request body.") // Req 7.5
			return
		}

		upd, code, verr := parseRESTUserUpdate(body)
		if verr != nil {
			writeRESTError(w, http.StatusBadRequest, code, verr.Error())
			return
		}

		// Role gate FIRST, before any profile write (Req 6, 11.3): a roles change
		// on the caller's own record is a self-promote (Req 6.2); otherwise it
		// requires edit_users AND promote_users (Req 6.1). Running this ahead of
		// UpdateProfile keeps a forbidden role change a clean fail-closed 403 with
		// no partial profile write.
		if len(body.Roles) > 0 {
			if id == principal.UserID { // no self-promote (Req 6.2)
				writeRESTError(w, http.StatusForbidden, "rest_cannot_edit_roles",
					"Sorry, you are not allowed to edit your own roles.")
				return
			}
			if !auth.CanEditUsers(principal) || !auth.CanPromoteUsers(principal) { // Req 6.1
				writeRESTError(w, http.StatusForbidden, "rest_cannot_edit_roles",
					"Sorry, you are not allowed to assign roles to users.")
				return
			}
		}

		// Profile fields (name/email/url/slug/description). UpdateProfile loads the
		// stored row (404 for an unknown id), decides self-vs-others against {id}
		// vs the actor, sanitizes description, and persists — always called so a
		// password-only or roles-only request still gets the load + gate + 404.
		if _, err := s.userWrite.UpdateProfile(r.Context(), principal, id, upd); err != nil {
			switch {
			case errors.Is(err, domain.ErrNotFound):
				writeRESTError(w, http.StatusNotFound, "rest_user_invalid_id", "Invalid user ID.") // Req 7.4
			case errors.Is(err, content.ErrForbidden):
				writeRESTError(w, http.StatusForbidden, "rest_cannot_edit",
					"Sorry, you are not allowed to edit this user.") // Req 5.3-5.6
			default:
				// A policy error (e.g. a rejected description) fails closed → 400
				// (Finding 2, Req 4.4).
				writeRESTError(w, http.StatusBadRequest, "rest_invalid_param",
					"The supplied profile fields could not be accepted.")
			}
			return
		}

		// Password: self OR edit_users, enforced inside SetPassword (Req 2.1, 5.4).
		if body.Password != nil {
			if err := s.userWrite.SetPassword(r.Context(), principal, id, *body.Password); err != nil {
				switch {
				case errors.Is(err, content.ErrForbidden):
					writeRESTError(w, http.StatusForbidden, "rest_cannot_edit",
						"Sorry, you are not allowed to edit this user.")
				case errors.Is(err, domain.ErrNotFound):
					writeRESTError(w, http.StatusNotFound, "rest_user_invalid_id", "Invalid user ID.")
				default:
					writeRESTError(w, http.StatusInternalServerError, "rest_user_update_failed", "Could not update password.")
				}
				return
			}
		}

		// Roles: already gated above (edit_users+promote_users+not-self); SetRoles
		// re-checks edit_users as defense in depth (Finding 3).
		if len(body.Roles) > 0 {
			if err := s.userWrite.SetRoles(r.Context(), principal, id, body.Roles...); err != nil {
				if errors.Is(err, content.ErrForbidden) {
					writeRESTError(w, http.StatusForbidden, "rest_cannot_edit_roles",
						"Sorry, you are not allowed to assign roles to users.")
					return
				}
				writeRESTError(w, http.StatusInternalServerError, "rest_user_update_failed", "Could not update roles.")
				return
			}
		}

		// Re-read so the envelope reflects every write (profile + password + roles),
		// matching the shape GET /users/{id} returns in edit context (Req 7.6).
		u, err := s.restUsers.ByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeRESTError(w, http.StatusNotFound, "rest_user_invalid_id", "Invalid user ID.")
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_user_update_failed", "Could not look up user after update.")
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
