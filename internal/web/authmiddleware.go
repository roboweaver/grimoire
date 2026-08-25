package web

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"

	"github.com/roboweaver/grimoire/internal/auth"
)

// SessionMiddleware loads the session cookie, authenticates it, and — on success
// — injects the principal and session into the request context. Anonymous or
// invalid/expired cookies pass through unauthenticated (no error surfaced to the
// client). On a valid session it refreshes the cookie's Max-Age to mirror the
// server-side rolling expiry.
func (s *Server) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Application Password auth (ApplicationPasswordAuth, mounted ahead
		// of this middleware on /wp-json routes) already resolved a
		// principal for this request. Do NOT let a session cookie also
		// present on the request overwrite it: rest_comments.go's CSRF
		// branch trusts isAppPasswordAuth(ctx) to mean "the effective
		// principal is Application-Password-authenticated, exempt from
		// CSRF" (Req 8.7) — if this middleware clobbered principalKey with
		// the session's principal while leaving appPasswordAuthKey set, a
		// request presenting both a valid Application Password AND an
		// ambient session cookie would run as the *session* principal (its
		// identity/capabilities) yet still skip CSRF, defeating the CSRF
		// contract for session auth.
		if isAppPasswordAuth(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(s.authCfg.cookieName())
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		principal, sess, err := s.auth.Authenticate(r.Context(), c.Value)
		if err != nil {
			// Invalid/expired token: clear the stale cookie and continue anonymous.
			if errors.Is(err, auth.ErrNoSession) {
				s.clearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}
			// Unexpected backend error: fail closed as anonymous but log it.
			s.log.Error("session authenticate", "err", err)
			next.ServeHTTP(w, r)
			return
		}
		s.setSessionCookie(w, c.Value)
		ctx := withAuth(r.Context(), principal, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireLogin rejects anonymous requests. Safe (GET/HEAD) requests are
// redirected to /login with a redirect back-link; others get 401.
func (s *Server) RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFrom(r.Context()); !ok {
			s.denyUnauthenticated(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireCapability rejects requests whose principal lacks capability. Anonymous
// requests are treated like RequireLogin; authenticated-but-unauthorized
// requests get 403 (generic, no capability name leaked).
func (s *Server) RequireCapability(capability string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				s.denyUnauthenticated(w, r)
				return
			}
			if !p.Can(capability) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// denyUnauthenticated redirects safe methods to the login form and 401s the rest.
func (s *Server) denyUnauthenticated(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		dest := "/login?redirect=" + url.QueryEscape(r.URL.Path)
		http.Redirect(w, r, dest, http.StatusSeeOther)
		return
	}
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

// requireSessionCSRF enforces the per-session synchronizer token on an
// authenticated unsafe request: the submitted csrf_token field must equal the
// session's stored CSRF token. Returns true when the request may proceed.
// Failures render the plain-text error body used by the non-JSON login/logout
// flow (authhandlers.go); JSON API routes use requireSessionCSRFJSON instead.
func (s *Server) requireSessionCSRF(w http.ResponseWriter, r *http.Request) bool {
	return s.checkSessionCSRF(r, func(status int) {
		http.Error(w, http.StatusText(status), status)
	})
}

// requireSessionCSRFJSON is the JSON-API counterpart of requireSessionCSRF: it
// applies the same CSRF check but renders failures as the standard
// {error:{code,message}} envelope instead of a plain-text body (Req 14).
func (s *Server) requireSessionCSRFJSON(w http.ResponseWriter, r *http.Request) bool {
	return s.checkSessionCSRF(r, func(status int) {
		switch status {
		case http.StatusBadRequest:
			writeJSONError(w, status, "bad_request", "malformed request")
		default:
			writeJSONError(w, status, "forbidden", "CSRF validation failed")
		}
	})
}

// csrfJSONMiddleware is a route-group-level chi middleware adapter around
// requireSessionCSRFJSON (unchanged since M4). M6 is the first milestone with
// two separate capability-gated groups (posts, terms) that each need the same
// CSRF gate, so it is applied via .Use() here rather than the per-handler
// inline call adminapi_comments.go/adminapi_media.go still use — a pure
// code-organization change, not a new CSRF mechanism (Req 8.4).
func (s *Server) csrfJSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requireSessionCSRFJSON(w, r) { // existing M4 helper, unchanged
			return // requireSessionCSRFJSON already wrote the 403 response
		}
		next.ServeHTTP(w, r)
	})
}

// checkSessionCSRF is the shared validation core for requireSessionCSRF and
// requireSessionCSRFJSON: it looks up the session, accepts either the
// X-CSRF-Token header or a csrf_token form field, and invokes fail with the
// appropriate status on any failure. Returns true when the request may
// proceed.
func (s *Server) checkSessionCSRF(r *http.Request, fail func(status int)) bool {
	sess, ok := sessionFrom(r.Context())
	if !ok {
		fail(http.StatusForbidden)
		return false
	}
	if token := r.Header.Get("X-CSRF-Token"); token != "" {
		if sess.CSRFToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) == 1 {
			return true
		}
	}
	if err := r.ParseForm(); err != nil {
		fail(http.StatusBadRequest)
		return false
	}
	if sess.CSRFToken == "" || !constantTimeEqual(r.PostFormValue(csrfFieldName), sess.CSRFToken) {
		fail(http.StatusForbidden)
		return false
	}
	return true
}
