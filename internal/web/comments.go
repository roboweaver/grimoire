package web

import (
	"html"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
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
	if p, ok := PrincipalFrom(r.Context()); ok {
		c.UserID = p.UserID
	}

	comment, post, err := s.comments.Create(r.Context(), c)
	if err != nil {
		if err == domain.ErrNotFound {
			http.NotFound(w, r)
			return nil
		}
		if err == content.ErrCommentsClosed {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return nil
		}
		return err
	}
	loc := "/" + url.PathEscape(post.Slug) + "?comment=pending&author=" + url.QueryEscape(comment.Author) + "&content=" + url.QueryEscape(comment.Content)
	http.Redirect(w, r, loc, http.StatusSeeOther)
	return nil
}

func commentView(c domain.Comment) render.CommentView {
	return render.CommentView{Author: c.Author, AuthorURL: c.AuthorURL, Date: c.Date, Content: html.EscapeString(c.Content)}
}
