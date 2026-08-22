package web

import (
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
func (s *Server) requireSessionCSRF(w http.ResponseWriter, r *http.Request) bool {
	sess, ok := sessionFrom(r.Context())
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return false
	}
	if sess.CSRFToken == "" || !constantTimeEqual(r.PostFormValue(csrfFieldName), sess.CSRFToken) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	return true
}
