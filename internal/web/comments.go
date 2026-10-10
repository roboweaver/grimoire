package web

import (
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

const commentCSRFCookieName = "grimoire_comment_csrf"

func (s *Server) setCommentCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: commentCSRFCookieName, Value: token, Path: "/", SameSite: http.SameSiteLaxMode, Secure: s.authCfg.Secure, MaxAge: csrfCookieMaxAge})
}

func (s *Server) verifyCommentCSRF(r *http.Request) bool {
	c, err := r.Cookie(commentCSRFCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return constantTimeEqual(r.PostFormValue("comment_csrf_token"), c.Value)
}

// commentClientIP extracts the caller's IP for comment_author_IP (Req 2.5),
// stripping the port from RemoteAddr. Falls back to the raw value when it
// isn't a host:port pair.
func commentClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) commentSubmit(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return nil
	}
	if !s.verifyCommentCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil
	}

	postID, err := strconv.ParseInt(r.PostFormValue("post_id"), 10, 64)
	if err != nil || postID <= 0 {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return nil
	}

	author := strings.TrimSpace(r.PostFormValue("author"))
	email := strings.TrimSpace(r.PostFormValue("email"))
	commentContent := strings.TrimSpace(r.PostFormValue("content"))
	if author == "" || email == "" || commentContent == "" {
		http.Error(w, "Bad Request: name, email, and comment are required", http.StatusBadRequest)
		return nil
	}
	if _, err := mail.ParseAddress(email); err != nil {
		http.Error(w, "Bad Request: invalid email address", http.StatusBadRequest)
		return nil
	}

	c := domain.Comment{
		PostID:      postID,
		Author:      author,
		AuthorEmail: email,
		AuthorURL:   r.PostFormValue("url"),
		AuthorIP:    commentClientIP(r),
		Agent:       r.UserAgent(),
		Content:     commentContent,
		Honeypot:    r.PostFormValue("website"),
	}
	actor := sanitize.Anonymous()
	if p, ok := PrincipalFrom(r.Context()); ok {
		c.UserID = p.UserID
		actor = sanitize.For(p)
	}

	comment, post, err := s.comments.Create(r.Context(), actor, c)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.NotFound(w, r)
			return nil
		}
		if errors.Is(err, content.ErrCommentsClosed) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return nil
		}
		if errors.Is(err, content.ErrCommentEmpty) {
			http.Error(w, "Bad Request: name, email, and comment are required", http.StatusBadRequest)
			return nil
		}
		return err
	}
	loc := "/" + url.PathEscape(post.Slug) + "?comment=pending&author=" + url.QueryEscape(comment.Author) + "&content=" + url.QueryEscape(comment.Content)
	http.Redirect(w, r, loc, http.StatusSeeOther)
	return nil
}

// TRUST BOUNDARY: CommentView.Content is emitted verbatim as template.HTML,
// bypassing html/template auto-escaping. This is the ONLY comment cast site in
// the tree (the pending-comment echo routes through here too), and it is safe
// because of the tier-A RENDER BACKSTOP applied immediately below: every value
// reaching the cast has been sanitized at tier A — WordPress's tight
// $allowedtags comment list — by the Server's *sanitize.Policy on the render
// path, via s.policy.Sanitize(sanitize.CommentContent, sanitize.Anonymous(), ...).
//
// Anonymous() selects tier A for CommentContent (an anonymous writer lacks
// unfiltered_html → tier A), and the backstop is applied UNCONDITIONALLY —
// independent of the rendering request's principal and of the tier the stored
// value was written at — because provenance is not tracked (Req 5.3). This is
// in addition to any write-time sanitization (Req 6.9). It therefore covers
// EVERY comment row, including pre-M10a and imported rows that were never
// sanitized on write, which is exactly why it must be unconditional: an
// unconditional tight list is the only rule this render site can implement
// correctly (Req 6.9, 6.11). The stored bytes are never written back — this
// method only transforms for rendering (no write-back).
//
// CommentView.Author / AuthorURL stay plain strings and keep being
// auto-escaped by html/template; the trust boundary widens to comment CONTENT
// only (Req 6.5).
func (s *Server) commentView(c domain.Comment) render.CommentView {
	// Fail closed: a tier-A sanitize of CommentContent cannot error (no title
	// path; the only error from SanitizeAt is an unrecognised kind/tier, which
	// cannot happen for CommentContent at tier A). Should it ever error, use
	// empty content rather than the raw bytes so an unsanitized value never
	// reaches the template.HTML cast.
	clean, err := s.policy.Sanitize(sanitize.CommentContent, sanitize.Anonymous(), c.Content)
	if err != nil {
		clean = ""
	}
	return render.CommentView{Author: c.Author, AuthorURL: c.AuthorURL, Date: c.Date, Content: template.HTML(clean)}
}
