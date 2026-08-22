package web

import (
	"html/template"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
)

// postView maps a domain.Post to its template-facing view. Content is trusted
// HTML from the database (read-only M1).
func postView(p domain.Post) render.PostView {
	return render.PostView{
		Slug:    p.Slug,
		Title:   p.Title,
		Excerpt: p.Excerpt,
		Content: template.HTML(p.Content),
		Date:    p.Date,
		Author:  p.Author,
	}
}

func postViews(ps []domain.Post) []render.PostView {
	out := make([]render.PostView, 0, len(ps))
	for _, p := range ps {
		out = append(out, postView(p))
	}
	return out
}

func termView(t domain.Term) render.TermView {
	return render.TermView{Name: t.Name, Slug: t.Slug, Taxonomy: t.Taxonomy}
}
