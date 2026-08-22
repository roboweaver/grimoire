package web

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/render"
)

// csrfCookieMaxAge bounds the login-form CSRF cookie lifetime (seconds).
const csrfCookieMaxAge = 3600

// setSessionCookie writes the session cookie with the security attributes M2
// mandates: HttpOnly, SameSite=Lax, Path=/, Secure-when-configured, and a
// Max-Age mirroring the server-side rolling expiry.
func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.authCfg.cookieName(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.authCfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   s.authCfg.maxAge(),
	})
}

// clearSessionCookie expires the session cookie on the client.
func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.authCfg.cookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.authCfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// setCSRFCookie writes the login-form double-submit token cookie.
func (s *Server) setCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.authCfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   csrfCookieMaxAge,
	})
}

// clearCSRFCookie expires the login-form CSRF cookie.
func (s *Server) clearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.authCfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// loginForm renders GET /login: it mints a fresh double-submit CSRF token,
// sets it as a cookie, and embeds the same value in the form.
func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) error {
	// Already authenticated: send them home rather than showing the form.
	if _, ok := PrincipalFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return nil
	}
	csrf := randToken()
	s.setCSRFCookie(w, csrf)
	title, _ := s.options.SiteInfo(r.Context())
	data := render.LoginData{
		SiteTitle: title,
		CSRFToken: csrf,
		Error:     r.URL.Query().Get("error") != "",
		Redirect:  safeRedirect(r.URL.Query().Get("redirect")),
	}
	return s.renderLogin(w, http.StatusOK, data)
}

// loginSubmit handles POST /login. It enforces the double-submit CSRF check,
// authenticates the credentials, and on success sets the session cookie and
// redirects. Failures re-render the generic form with no user enumeration.
func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return nil
	}
	if !s.verifyLoginCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil
	}
	redirect := safeRedirect(r.PostFormValue("redirect"))
	token, _, err := s.auth.Login(r.Context(), r.PostFormValue("log"), r.PostFormValue("pwd"))
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			csrf := randToken()
			s.setCSRFCookie(w, csrf)
			title, _ := s.options.SiteInfo(r.Context())
			return s.renderLogin(w, http.StatusUnauthorized, render.LoginData{
				SiteTitle: title,
				CSRFToken: csrf,
				Error:     true,
				Redirect:  redirect,
			})
		}
		return err
	}
	s.setSessionCookie(w, token)
	s.clearCSRFCookie(w)
	http.Redirect(w, r, redirect, http.StatusSeeOther)
	return nil
}

// logoutSubmit handles POST /logout. It requires the per-session synchronizer
// token, revokes the session server-side, clears the cookie, and redirects home.
func (s *Server) logoutSubmit(w http.ResponseWriter, r *http.Request) error {
	if !s.requireSessionCSRF(w, r) {
		return nil
	}
	if c, err := r.Cookie(s.authCfg.cookieName()); err == nil && c.Value != "" {
		if err := s.auth.Logout(r.Context(), c.Value); err != nil {
			s.log.Error("logout", "err", err)
		}
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
	return nil
}

// verifyLoginCSRF checks the login double-submit token: the csrf_token field
// must equal the CSRF cookie value (constant-time).
func (s *Server) verifyLoginCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return constantTimeEqual(r.PostFormValue(csrfFieldName), c.Value)
}

// renderLogin renders the login view into a buffer, then writes status + body so
// a template error surfaces before any bytes reach the client.
func (s *Server) renderLogin(w http.ResponseWriter, status int, data render.LoginData) error {
	var buf bytes.Buffer
	if err := s.render.Render(&buf, "login", data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := w.Write(buf.Bytes())
	return err
}
