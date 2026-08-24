package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// canModerateComments reports whether the request's resolved principal (if
// any) has the "moderate_comments" capability (Req 3.3).
func (s *Server) canModerateComments(r *http.Request) bool {
	p, ok := PrincipalFrom(r.Context())
	return ok && p.Can("moderate_comments")
}

// restCommentStatuses maps the wp/v2 "status" query parameter vocabulary
// (hold/spam/trash/any) to raw {prefix}comments.comment_approved storage
// values (Req 3.3). ok is false for an unrecognized value, in which case
// the caller should ignore it and fall back to the approved-only default.
func restCommentStatuses(status string) (statuses []string, any bool, ok bool) {
	switch status {
	case "hold":
		return []string{"0"}, false, true
	case "approved", "":
		return []string{"1"}, false, true
	case "spam":
		return []string{"spam"}, false, true
	case "trash":
		return []string{"trash"}, false, true
	case "any":
		return nil, true, true
	default:
		return nil, false, false
	}
}

// registerRESTComments registers GET .../comments[/{id}] (Req 3.1, 3.3),
// POST .../comments (Req 7.1-7.4), and 501 stubs for every other write
// method (Req 7.5), including on route/method combinations not otherwise
// planned (e.g. POST on {id}, PUT/PATCH/DELETE on the bare collection) so
// they don't fall through to chi's MethodNotAllowed 405.
func (s *Server) registerRESTComments(r chi.Router) {
	r.Method(http.MethodGet, "/comments", s.handleRESTCommentsCollection())
	r.Method(http.MethodGet, "/comments/{id}", s.handleRESTCommentSingle())
	r.Method(http.MethodPost, "/comments", s.handleRESTCommentCreate())
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Method(m, "/comments", restNotImplemented("rest_cannot_edit", "Updating or deleting a comment"))
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Method(m, "/comments/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting a comment"))
	}
}

// handleRESTCommentsCollection serves GET .../comments: approved-only for an
// anonymous or uncapable caller (Req 3.1), optionally filtered by ?post= and,
// for a moderate_comments-capable caller, by ?status=hold|spam|trash|any
// (Req 3.3).
func (s *Server) handleRESTCommentsCollection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		paging := parseRESTPaging(r, s.restPerPageMax)
		filter := domain.CommentFilter{Limit: paging.PerPage, Offset: paging.Offset}
		if raw := q.Get("post"); raw != "" {
			postID, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || postID < 0 {
				writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid post parameter.")
				return
			}
			filter.PostID = postID
		}

		if s.canModerateComments(r) {
			statuses, matchAny, ok := restCommentStatuses(q.Get("status"))
			if !ok {
				writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid status parameter.")
				return
			}
			if !matchAny {
				filter.Statuses = statuses
			}
		} else {
			filter.Statuses = []string{"1"}
		}

		items, total, err := s.comments.List(r.Context(), filter)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not list comments.")
			return
		}
		out := make([]any, 0, len(items))
		for _, c := range items {
			enveloped, err := s.mapRESTComment(r, c)
			if err != nil {
				writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not encode comment #"+strconv.FormatInt(c.ID, 10)+".")
				return
			}
			out = append(out, enveloped)
		}
		setRESTPaginationHeaders(w, total, paging.PerPage)
		_ = writeRESTResponse(w, r, http.StatusOK, out)
	}
}

// handleRESTCommentSingle serves GET .../comments/{id} (Req 3.1). A
// not-approved comment is visible only to a moderate_comments-capable
// caller; otherwise it 404s exactly like an unknown ID, never distinguishing
// the two cases to an unprivileged caller.
func (s *Server) handleRESTCommentSingle() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeRESTError(w, http.StatusNotFound, "rest_comment_invalid_id", "Invalid comment ID.")
			return
		}
		c, err := s.comments.ByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeRESTError(w, http.StatusNotFound, "rest_comment_invalid_id", "Invalid comment ID.")
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not look up comment.")
			return
		}
		if c.Status != "1" && !s.canModerateComments(r) {
			writeRESTError(w, http.StatusNotFound, "rest_comment_invalid_id", "Invalid comment ID.")
			return
		}
		enveloped, err := s.mapRESTComment(r, c)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not encode comment.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, enveloped)
	}
}

// restCommentCreateBody is the JSON request body for POST .../comments (Req
// 7.1), matching the WordPress REST comment-create contract's field names.
type restCommentCreateBody struct {
	Post        int64  `json:"post"`
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	AuthorURL   string `json:"author_url"`
	Content     string `json:"content"`
}

// handleRESTCommentCreate serves POST .../comments (Req 7.1-7.4). It
// delegates to the existing, unmodified content.CommentService.Create — no
// new comment business logic is introduced, only the request/response shape
// differs from the server-rendered form path (Req 7.2). Auth: a valid
// Application Password skips the CSRF check entirely (Req 8.7); a
// session-cookie-authenticated request enforces the M4 X-CSRF-Token contract
// (Req 7.4); an anonymous request (no session, no Application Password) is
// accepted and protected only by the M4 spam filter/moderation defaults,
// exactly like the public comment form (Req 7.4).
func (s *Server) handleRESTCommentCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAppPasswordAuth(r.Context()) {
			if _, hasSession := sessionFrom(r.Context()); hasSession {
				if !s.requireSessionCSRFREST(w, r) {
					return
				}
			}
		}

		var body restCommentCreateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Malformed request body.")
			return
		}
		if body.Post <= 0 || body.AuthorName == "" || body.AuthorEmail == "" || body.Content == "" {
			writeRESTError(w, http.StatusBadRequest, "rest_comment_content_invalid", "post, author_name, author_email, and content are required.")
			return
		}

		c := domain.Comment{
			PostID:      body.Post,
			Author:      body.AuthorName,
			AuthorEmail: body.AuthorEmail,
			AuthorURL:   body.AuthorURL,
			AuthorIP:    commentClientIP(r),
			Agent:       r.UserAgent(),
			Content:     body.Content,
		}
		if p, ok := PrincipalFrom(r.Context()); ok {
			c.UserID = p.UserID
		}

		comment, _, err := s.comments.Create(r.Context(), c)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeRESTError(w, http.StatusNotFound, "rest_comment_invalid_post_id", "Sorry, you are not allowed to create a comment on this post.")
				return
			}
			if errors.Is(err, content.ErrCommentsClosed) {
				writeRESTError(w, http.StatusForbidden, "rest_comment_closed", "Sorry, comments are closed for this item.")
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_comment_failed", "Could not create the comment.")
			return
		}
		enveloped, err := s.mapRESTComment(r, comment)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not encode comment.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusCreated, enveloped)
	}
}

// requireSessionCSRFREST is the wp-json counterpart of
// requireSessionCSRFJSON: the same synchronizer-token check, rendered as
// the flat wp-json error envelope (403 rest_forbidden) rather than the
// admin API's nested {error:{...}} shape.
func (s *Server) requireSessionCSRFREST(w http.ResponseWriter, r *http.Request) bool {
	return s.checkSessionCSRF(r, func(status int) {
		writeRESTError(w, status, "rest_forbidden", "Sorry, you are not allowed to do that.")
	})
}

// mapRESTComment maps a domain.Comment through content.Comment and builds
// its _links: self, collection, and "up" (the parent post) when the comment
// has one (Req 3.2, 6.2). Comments have no _embedded content this milestone.
func (s *Server) mapRESTComment(r *http.Request, c domain.Comment) (map[string]any, error) {
	mapped := content.Comment(c)
	mapped.Link = restAbs(r, mapped.Link)

	b := newRESTLinks()
	base := "/wp-json/wp/v2/comments"
	b.add("self", restAbs(r, base+"/"+strconv.FormatInt(c.ID, 10)))
	b.add("collection", restAbs(r, base))
	if c.PostID != 0 {
		b.add("up", restAbs(r, "/wp-json/wp/v2/posts/"+strconv.FormatInt(c.PostID, 10)))
	}
	return withEnvelope(mapped, b.links, nil)
}
