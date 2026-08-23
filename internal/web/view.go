package web

import (
	"html/template"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
)

// postView maps a domain.Post to its template-facing view.
//
// TRUST BOUNDARY: both post_content and the derived Excerpt are emitted verbatim
// as template.HTML, bypassing html/template auto-escaping. Content is the raw
// post_content; Excerpt is either a manual post_excerpt or an auto-derived
// summary from content.Excerpt (which strips tags/shortcodes/block comments).
// This is safe in M1/M2 ONLY because grimoire reads a trusted, read-only
// WordPress database whose content was authored/sanitized by WordPress. Any
// future write/admin path (or ingestion of untrusted content) MUST sanitize
// post_content and post_excerpt (e.g. bluemonday) before they reach these casts.
// See docs/compatibility.md.
func postView(p domain.Post) render.PostView {
	return render.PostView{
		Slug:    p.Slug,
		Title:   p.Title,
		Excerpt: template.HTML(content.Excerpt(p)), // trusted excerpt HTML — see trust boundary note above
		Content: template.HTML(p.Content),          // trusted DB HTML — see trust boundary note above
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
