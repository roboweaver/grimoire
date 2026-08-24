package web

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
)

// registerRESTAppPasswords registers the three Application Password
// self-service routes under .../users/me/application-passwords (Req 9).
// These are distinct, more-specific static path segments than
// .../users/{id}; chi always prefers a static match over a parameterized
// one regardless of registration order, so this can be called independently
// of registerRESTUsers without shadowing concerns (see that function's own
// doc comment).
func (s *Server) registerRESTAppPasswords(r chi.Router) {
	r.Method(http.MethodGet, "/users/me/application-passwords", s.handleRESTAppPasswordsList())
	r.Method(http.MethodPost, "/users/me/application-passwords", s.handleRESTAppPasswordsCreate())
	r.Method(http.MethodDelete, "/users/me/application-passwords/{uuid}", s.handleRESTAppPasswordsRevoke())
}

// requireSelfServiceSession resolves the caller's own user ID, requiring
// authentication via an M2 session cookie specifically — never an
// Application Password (Req 9.4: a credential that can mint or revoke its
// own replacements would defeat the point of being able to revoke it). Both
// an anonymous request and a valid-but-Application-Password-authenticated
// request are rejected the same way real WordPress rejects any
// not-currently-logged-in-via-cookie request to this endpoint family: 401
// rest_not_logged_in.
func (s *Server) requireSelfServiceSession(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if isAppPasswordAuth(r.Context()) {
		writeRESTError(w, http.StatusUnauthorized, "rest_not_logged_in", "You are not currently logged in.")
		return 0, false
	}
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		writeRESTError(w, http.StatusUnauthorized, "rest_not_logged_in", "You are not currently logged in.")
		return 0, false
	}
	return p.UserID, true
}

// restAppPassword is the wp-json shape of a stored Application Password
// record: uuid/app_id/name/created/last_used/last_ip, matching real
// WordPress's own field names. It never includes the hash or the plaintext
// secret (Req 9.1).
type restAppPassword struct {
	UUID     string  `json:"uuid"`
	AppID    string  `json:"app_id"`
	Name     string  `json:"name"`
	Created  int64   `json:"created"`
	LastUsed *int64  `json:"last_used"`
	LastIP   *string `json:"last_ip"`
}

// restAppPasswordCreated embeds restAppPassword plus the plaintext secret,
// returned exactly once, only in the creation response (Req 9.2).
type restAppPasswordCreated struct {
	restAppPassword
	Password string `json:"password"`
}

func mapRESTAppPassword(rec auth.ApplicationPassword) restAppPassword {
	out := restAppPassword{
		UUID:    rec.UUID,
		AppID:   rec.AppID,
		Name:    rec.Name,
		Created: rec.Created.Unix(),
	}
	if rec.LastUsed != nil {
		ts := rec.LastUsed.Unix()
		out.LastUsed = &ts
	}
	out.LastIP = rec.LastIP
	return out
}

// handleRESTAppPasswordsList serves GET .../users/me/application-passwords:
// the caller's own records, hash/secret never included (Req 9.1).
func (s *Server) handleRESTAppPasswordsList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireSelfServiceSession(w, r)
		if !ok {
			return
		}
		records, err := s.appPasswords.List(r.Context(), userID)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not list Application Passwords.")
			return
		}
		out := make([]restAppPassword, 0, len(records))
		for _, rec := range records {
			out = append(out, mapRESTAppPassword(rec))
		}
		_ = writeRESTResponse(w, r, http.StatusOK, out)
	}
}

// restAppPasswordCreateBody is the JSON request body for POST
// .../users/me/application-passwords (Req 9.2).
type restAppPasswordCreateBody struct {
	Name string `json:"name"`
}

// handleRESTAppPasswordsCreate serves POST
// .../users/me/application-passwords: creates a new Application Password
// and returns the plaintext secret exactly once, in this response only
// (Req 9.2). Requires the M2 session + a matching X-CSRF-Token (Req 9.4).
func (s *Server) handleRESTAppPasswordsCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireSelfServiceSession(w, r)
		if !ok {
			return
		}
		if !s.requireSessionCSRFREST(w, r) {
			return
		}
		var body restAppPasswordCreateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Malformed request body.")
			return
		}
		if body.Name == "" {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "name is required.")
			return
		}
		rec, secret, err := s.appPasswords.Create(r.Context(), userID, body.Name)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_application_password_create_failed", "Could not create the Application Password.")
			return
		}
		out := restAppPasswordCreated{restAppPassword: mapRESTAppPassword(rec), Password: secret}
		_ = writeRESTResponse(w, r, http.StatusCreated, out)
	}
}

// handleRESTAppPasswordsRevoke serves DELETE
// .../users/me/application-passwords/{uuid}: revokes the named credential,
// requiring the M2 session + a matching X-CSRF-Token (Req 9.4). Acting on a
// UUID that is not one of the caller's own (whether it never existed, or
// belongs to another user entirely) 404s (Req 9.3/9.5) rather than silently
// no-op-succeeding the way auth.ApplicationPasswords.Revoke itself does when
// scoped to a caller who never had that UUID to begin with.
func (s *Server) handleRESTAppPasswordsRevoke() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireSelfServiceSession(w, r)
		if !ok {
			return
		}
		if !s.requireSessionCSRFREST(w, r) {
			return
		}
		uuid := chi.URLParam(r, "uuid")
		records, err := s.appPasswords.List(r.Context(), userID)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not look up Application Passwords.")
			return
		}
		found := false
		for _, rec := range records {
			if rec.UUID == uuid {
				found = true
				break
			}
		}
		if !found {
			writeRESTError(w, http.StatusNotFound, "rest_application_password_not_found", "That Application Password does not exist.")
			return
		}
		if err := s.appPasswords.Revoke(r.Context(), userID, uuid); err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_application_password_revoke_failed", "Could not revoke the Application Password.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, map[string]any{"deleted": true})
	}
}
