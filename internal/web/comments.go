package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"

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

func (s *Server) commentSubmit(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return nil
	}
	if !s.verifyCommentCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil
	}
	postID, _ := strconv.ParseInt(r.PostFormValue("post_id"), 10, 64)
	comment, err := s.comments.Create(r.Context(), domain.Comment{
		PostID:      postID,
		Author:      r.PostFormValue("author"),
		AuthorEmail: r.PostFormValue("email"),
		AuthorURL:   r.PostFormValue("url"),
		Content:     r.PostFormValue("content"),
	})
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
	loc := fmt.Sprintf("/hello-1?comment=pending&author=%s&content=%s", url.QueryEscape(comment.Author), url.QueryEscape(comment.Content))
	http.Redirect(w, r, loc, http.StatusSeeOther)
	return nil
}

func commentView(c domain.Comment) render.CommentView {
	return render.CommentView{Author: c.Author, AuthorURL: c.AuthorURL, Date: c.Date, Content: html.EscapeString(c.Content)}
}
