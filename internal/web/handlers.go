package web

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/pkg/extensions"
	"html"
)

// hookRenderPostHTML is the "render.post_html" filter hook (Req 11.1):
// fired with the fully-rendered HTML buffer for a public single/page view,
// immediately before it is written to the response.
const hookRenderPostHTML = "render.post_html"

// pageParam parses a ?page= query value, defaulting to 1 for missing/invalid.
func pageParam(r *http.Request) int {
	p, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || p < 1 {
		return 1
	}
	return p
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	title, tagline := s.options.SiteInfo(ctx)
	page := pageParam(r)
	posts, pg, err := s.posts.RecentPage(ctx, page, content.DefaultPerPage)
	if err != nil {
		return err
	}
	if page > 1 && pg.Total > 0 && page > pg.TotalPages {
		return domain.ErrNotFound
	}
	data := render.IndexData{
		SiteTitle:  title,
		Tagline:    tagline,
		Posts:      postViews(ctx, posts, s.options.BaseURLs(ctx), s.featured),
		Pagination: pg,
	}
	return s.renderHTML(w, r, "index", data)
}

func (s *Server) single(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")
	post, err := s.posts.BySlug(ctx, slug)
	if err != nil {
		return err
	}
	kind := "single"
	if post.Type == "page" {
		kind = "page"
	}
	title, tagline := s.options.SiteInfo(ctx)
	comments := []render.CommentView{}
	commentCount := 0
	var pending *render.CommentView
	commentToken := ""
	menu := render.NavMenuView{}
	if s.comments != nil {
		items, total, err := s.comments.List(ctx, domain.CommentFilter{PostID: post.ID, Statuses: []string{"1"}})
		if err != nil {
			return err
		}
		for _, c := range items {
			comments = append(comments, commentView(c))
		}
		commentCount = total
		if r.URL.Query().Get("comment") == "pending" {
			p := render.CommentView{Author: r.URL.Query().Get("author"), Content: r.URL.Query().Get("content"), Date: time.Now(), PendingEcho: true}
			p.Content = html.EscapeString(p.Content)
			pending = &p
		}
		tok, err := randToken()
		if err == nil {
			s.setCommentCSRFCookie(w, tok)
			commentToken = tok
		}
	}
	if s.menus != nil {
		m, err := s.menus.ByLocation(ctx, "primary")
		if err != nil {
			return err
		}
		menu = navMenuView(m)
	}
	data := render.SingleData{SiteTitle: title, Tagline: tagline, Post: postView(ctx, post, s.options.BaseURLs(ctx), s.featured), Comments: comments, CommentCount: commentCount, PendingComment: pending, CommentToken: commentToken, Menu: menu}
	return s.renderHTML(w, r, kind, data)
}

func (s *Server) category(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")
	term, posts, err := s.terms.Category(ctx, slug, pageParam(r), content.DefaultPerPage)
	if err != nil {
		return err
	}
	title, tagline := s.options.SiteInfo(ctx)
	data := render.CategoryData{SiteTitle: title, Tagline: tagline, Term: termView(term), Posts: postViews(ctx, posts, s.options.BaseURLs(ctx), s.featured)}
	return s.renderHTML(w, r, "category", data)
}

// renderHTML renders kind with data into a buffer first, so a template
// execution error surfaces before any bytes reach the client. This lets the
// error middleware map failures to a clean 404/500 instead of appending an
// error to a partially written 200 response.
//
// For the public single/page views specifically (Req 11.1), the fully
// rendered HTML buffer is passed through the "render.post_html" filter
// immediately before it is written to the response, letting a registered
// extension transform the final markup without touching the render engine
// or this handler. r may be nil (as in tests exercising only the
// render-error path); the filter is applied only when r is non-nil, since
// extensions.ApplyFilters needs a context to run under.
func (s *Server) renderHTML(w http.ResponseWriter, r *http.Request, kind string, data any) error {
	var buf bytes.Buffer
	if err := s.render.Render(&buf, kind, data); err != nil {
		return err
	}
	out := buf.Bytes()
	if r != nil && (kind == "single" || kind == "page") {
		filtered, err := extensions.ApplyFilters(r.Context(), hookRenderPostHTML, out)
		if err != nil {
			return err
		}
		out = filtered
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(out)
	return err
}
