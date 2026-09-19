package web

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
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

// single resolves and renders one post or page.
//
// With no permalink structure configured the flat /{slug} path is canonical and
// this is a slug lookup, exactly as it was before M9a. With a structure
// configured the handler additionally owns canonicalisation: it resolves the
// request by whichever token the structure identifies posts with, rejects date
// components that contradict the post, and permanently redirects any recognised
// path that is not the post's one canonical path (Req 2.3, 2.5, 3.1-3.4).
func (s *Server) single(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	post, err := s.resolveSingle(ctx, r)
	if err != nil {
		return err
	}

	// Canonical is the single construction site for a permalink, so the redirect
	// target here cannot drift from the one the REST API reports. It returns the
	// flat path when Flat, which makes this comparison a no-op rather than a
	// special case (Req 3.6).
	if canonical := s.permalinks.Canonical(post); r.URL.Path != canonical {
		target := canonical
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		// 301 rather than 302, matching WordPress's redirect_canonical: these
		// URLs are permanent, and the accumulated link equity should follow.
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return nil
	}

	return s.renderSingle(w, r, post)
}

// resolveSingle turns a request into the post it addresses, or
// domain.ErrNotFound.
//
// Components come from the request path via ParamsFromPath rather than from
// chi's parameter names. Both forms of a single-segment structure such as
// /%postname%/ reach this handler, but chi collapses that pattern against the
// flat /{slug} route, so the same post arrives as "postname" on one form and
// "slug" on the other. Reading the path is independent of which route won.
func (s *Server) resolveSingle(ctx context.Context, r *http.Request) (domain.Post, error) {
	ref, ok := s.refFrom(r)
	if !ok {
		return domain.Post{}, domain.ErrNotFound
	}

	var post domain.Post
	var err error
	switch {
	case ref.Slug != "":
		post, err = s.posts.BySlug(ctx, ref.Slug)
	case ref.ID != 0:
		post, err = s.posts.PublishedByID(ctx, ref.ID)
	default:
		return domain.Post{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Post{}, err
	}

	// Req 2.5: the date in the URL must be the post's own. Only the components
	// the structure actually carries are compared, so a month-and-name structure
	// does not assert anything about the day. Dates are compared in the post's
	// stored local time, the same basis Canonical builds from.
	d := post.Date
	if (ref.Year != 0 && ref.Year != d.Year()) ||
		(ref.Month != 0 && ref.Month != int(d.Month())) ||
		(ref.Day != 0 && ref.Day != d.Day()) {
		return domain.Post{}, domain.ErrNotFound
	}

	return post, nil
}

// refFrom extracts the identifying components of a request, preferring the
// configured structure and falling back to the flat slug route.
func (s *Server) refFrom(r *http.Request) (routing.Ref, bool) {
	if params, ok := s.permalinks.ParamsFromPath(r.URL.Path); ok {
		// The path has this structure's shape. Match still has to accept it:
		// chi patterns cannot express digit widths, so a path like
		// /2024/1/01/hello-1/ routes here and is rejected only now (Req 2.2).
		return s.permalinks.Match(params)
	}
	// Either no structure is configured, or the request arrived on the flat
	// /{slug} route with a segment count the structure does not produce -- in
	// which case resolving it is what lets the canonical redirect happen.
	if slug := chi.URLParam(r, "slug"); slug != "" {
		return routing.Ref{Slug: slug}, true
	}
	return routing.Ref{}, false
}

func (s *Server) renderSingle(w http.ResponseWriter, r *http.Request, post domain.Post) error {
	ctx := r.Context()
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
	page := pageParam(r)
	term, posts, pg, err := s.terms.CategoryPage(ctx, slug, page, content.DefaultPerPage)
	if err != nil {
		return err
	}
	if page > 1 && pg.Total > 0 && page > pg.TotalPages {
		return domain.ErrNotFound
	}
	title, tagline := s.options.SiteInfo(ctx)
	data := render.CategoryData{SiteTitle: title, Tagline: tagline, Term: termView(term), Posts: postViews(ctx, posts, s.options.BaseURLs(ctx), s.featured), Pagination: pg}
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
