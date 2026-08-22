package web

import (
	"bytes"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
)

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
	posts, err := s.posts.Recent(ctx, pageParam(r), content.DefaultPerPage)
	if err != nil {
		return err
	}
	data := render.IndexData{SiteTitle: title, Tagline: tagline, Posts: postViews(posts)}
	return s.renderHTML(w, "index", data)
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
	data := render.SingleData{SiteTitle: title, Tagline: tagline, Post: postView(post)}
	return s.renderHTML(w, kind, data)
}

func (s *Server) category(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")
	term, posts, err := s.terms.Category(ctx, slug, pageParam(r), content.DefaultPerPage)
	if err != nil {
		return err
	}
	title, tagline := s.options.SiteInfo(ctx)
	data := render.CategoryData{SiteTitle: title, Tagline: tagline, Term: termView(term), Posts: postViews(posts)}
	return s.renderHTML(w, "category", data)
}

// renderHTML renders kind with data into a buffer first, so a template
// execution error surfaces before any bytes reach the client. This lets the
// error middleware map failures to a clean 404/500 instead of appending an
// error to a partially written 200 response.
func (s *Server) renderHTML(w http.ResponseWriter, kind string, data any) error {
	var buf bytes.Buffer
	if err := s.render.Render(&buf, kind, data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(buf.Bytes())
	return err
}
