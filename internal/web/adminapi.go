package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// adminReader is the read-only surface the admin JSON API depends on.
// *content.AdminService satisfies it; tests inject a fake.
type adminReader interface {
	List(ctx context.Context, page, perPage int, typ, status string) (content.AdminList, error)
	Detail(ctx context.Context, id int64) (domain.Post, error)
	Stats(ctx context.Context) (content.Stats, error)
	DisplayName(ctx context.Context, userID int64) (string, error)
}

// badRequestError marks a client input error (e.g. a non-numeric id) that
// jsonHandler maps to 400 rather than 500.
type badRequestError struct{ msg string }

func (e badRequestError) Error() string { return e.msg }

// jsonHandler adapts an error-returning handler into an http.Handler that emits
// JSON errors: ErrNotFound -> 404, badRequestError -> 400, anything else -> 500
// (logged without leaking internal detail to the client).
func (s *Server) jsonHandler(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		switch {
		case errors.Is(err, domain.ErrNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "resource not found")
		case errors.As(err, &badRequestError{}):
			writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
		default:
			s.log.Error("admin api error", "method", r.Method, "path", r.URL.Path, "err", err)
			writeJSONError(w, http.StatusInternalServerError, "internal", "internal server error")
		}
	})
}

// requireLoginJSON rejects anonymous requests with 401 JSON. Unlike RequireLogin
// it never redirects, so API clients get a machine-readable status.
func (s *Server) requireLoginJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFrom(r.Context()); !ok {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireCapabilityJSON rejects anonymous requests with 401 JSON and
// authenticated-but-uncapable requests with 403 JSON (no capability name
// leaked). It never redirects.
func (s *Server) requireCapabilityJSON(capability string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			if !p.Can(capability) {
				writeJSONError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- response shapes (JSON contracts) ---

type sessionResponse struct {
	ID           int64    `json:"id"`
	Login        string   `json:"login"`
	DisplayName  string   `json:"displayName"`
	Roles        []string `json:"roles"`
	Capabilities []string `json:"capabilities"`
	CSRFToken    string   `json:"csrfToken"`
}

type statsResponse struct {
	Posts struct {
		Published int `json:"published"`
		Draft     int `json:"draft"`
	} `json:"posts"`
	Pages      int `json:"pages"`
	Categories int `json:"categories"`
	Users      int `json:"users"`
}

type postListItem struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Slug   string `json:"slug"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Author int64  `json:"author"`
	Date   string `json:"date"`
}

type postsResponse struct {
	Items      []postListItem `json:"items"`
	Page       int            `json:"page"`
	PerPage    int            `json:"perPage"`
	Total      int            `json:"total"`
	TotalPages int            `json:"totalPages"`
}

type postDetailResponse struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Author  int64  `json:"author"`
	Date    string `json:"date"`
	Excerpt string `json:"excerpt"`
	Content string `json:"content"`
}

// --- handlers ---

// adminSession returns the current principal's identity, roles, capabilities,
// and the per-session CSRF token. The token is exposed for milestone 06's write
// contract (X-CSRF-Token header); M3 is GET-only and does not validate it.
func (s *Server) adminSession(w http.ResponseWriter, r *http.Request) error {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		// requireLoginJSON gates this route; defensive only.
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return nil
	}
	name, err := s.admin.DisplayName(r.Context(), p.UserID)
	if err != nil {
		// The session is valid even if the user row can't be read; degrade to
		// the login rather than leak the lookup failure (Req 2.6).
		name = p.Login
	}
	var csrf string
	if sess, ok := sessionFrom(r.Context()); ok {
		csrf = sess.CSRFToken
	}
	return writeJSON(w, http.StatusOK, sessionResponse{
		ID:           p.UserID,
		Login:        p.Login,
		DisplayName:  name,
		Roles:        nonNil(p.Roles),
		Capabilities: sortedCaps(p),
		CSRFToken:    csrf,
	})
}

// adminStats returns the dashboard counts.
func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) error {
	st, err := s.admin.Stats(r.Context())
	if err != nil {
		return err
	}
	var resp statsResponse
	resp.Posts.Published = st.PostsPublished
	resp.Posts.Draft = st.PostsDraft
	resp.Pages = st.Pages
	resp.Categories = st.Categories
	resp.Users = st.Users
	return writeJSON(w, http.StatusOK, resp)
}

// adminPosts returns a paginated content list (posts and pages, incl. drafts).
func (s *Server) adminPosts(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	perPage := atoiDefault(q.Get("perPage"), 0)
	list, err := s.admin.List(r.Context(), page, perPage, q.Get("type"), q.Get("status"))
	if err != nil {
		return err
	}
	items := make([]postListItem, 0, len(list.Items))
	for _, p := range list.Items {
		items = append(items, postListItem{
			ID:     p.ID,
			Title:  p.Title,
			Slug:   p.Slug,
			Type:   p.Type,
			Status: p.Status,
			Author: p.Author,
			Date:   p.Date.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return writeJSON(w, http.StatusOK, postsResponse{
		Items:      items,
		Page:       list.Page,
		PerPage:    list.PerPage,
		Total:      list.Total,
		TotalPages: list.TotalPages,
	})
}

// adminPost returns a single item by id.
func (s *Server) adminPost(w http.ResponseWriter, r *http.Request) error {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return badRequestError{msg: "invalid post id"}
	}
	p, err := s.admin.Detail(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, postDetailResponse{
		ID:      p.ID,
		Title:   p.Title,
		Slug:    p.Slug,
		Type:    p.Type,
		Status:  p.Status,
		Author:  p.Author,
		Date:    p.Date.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Excerpt: p.Excerpt,
		Content: p.Content,
	})
}

// --- helpers ---

// writeJSON writes v as a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// writeJSONError writes the standard error envelope {error:{code,message}}.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

// sortedCaps returns the principal's held capabilities in stable order, always a
// non-nil slice so the JSON encodes an array.
func sortedCaps(p auth.Principal) []string {
	caps := make([]string, 0, len(p.Caps))
	for c, ok := range p.Caps {
		if ok {
			caps = append(caps, c)
		}
	}
	sort.Strings(caps)
	return caps
}

// nonNil coerces a possibly-nil slice into an empty slice so JSON encodes [].
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// atoiDefault parses s as an int, returning def on any parse failure.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// clampPage normalizes a requested page/perPage pair into a safe (page,
// limit, offset) triple: page floors to 1, perPage floors to
// content.DefaultPerPage when unset/non-positive and ceils to
// content.MaxPerPage, preventing an unbounded LIMIT on the admin list
// endpoints (Req 11).
func clampPage(page, perPage int) (normPage, limit, offset int) {
	if page < 1 {
		page = 1
	}
	if perPage <= 0 {
		perPage = content.DefaultPerPage
	}
	if perPage > content.MaxPerPage {
		perPage = content.MaxPerPage
	}
	return page, perPage, (page - 1) * perPage
}
