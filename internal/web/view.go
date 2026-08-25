package web

import (
	"context"
	"html/template"
	"strings"

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
//
// baseURLs (from OptionService.BaseURLs) are the site's own configured
// siteurl/home option values, absolute self-references to which are rewritten
// to relative paths — see rewriteSelfURLs.
//
// featured resolves the post's featured image (may be nil, in which case
// FeaturedImageURL is left empty); its URL is already a grimoire-relative
// path (see wprepo.MediaRepo), so no rewriteSelfURLs pass is needed.
func postView(ctx context.Context, p domain.Post, baseURLs []string, featured *content.FeaturedImageService) render.PostView {
	return render.PostView{
		ID:               p.ID,
		Slug:             p.Slug,
		Title:            p.Title,
		Excerpt:          template.HTML(rewriteSelfURLs(content.Excerpt(p), baseURLs)), // trusted excerpt HTML — see trust boundary note above
		Content:          template.HTML(rewriteSelfURLs(p.Content, baseURLs)),          // trusted DB HTML — see trust boundary note above
		Date:             p.Date,
		Author:           p.Author,
		FeaturedImageURL: featured.URL(ctx, p.ID),
	}
}

func postViews(ctx context.Context, ps []domain.Post, baseURLs []string, featured *content.FeaturedImageService) []render.PostView {
	out := make([]render.PostView, 0, len(ps))
	for _, p := range ps {
		out = append(out, postView(ctx, p, baseURLs, featured))
	}
	return out
}

// rewriteSelfURLs strips any occurrence of the site's own absolute base URLs
// (siteurl/home, in both http/https form) from html, leaving a relative path
// in their place. WordPress commonly bakes an absolute self-referential URL
// into post_content (e.g. media URLs, internal links) at authoring/import
// time; left as-is, every page load would depend on that exact host:port
// being reachable even when grimoire itself already serves the same paths
// (e.g. /wp-content/uploads/*) standalone. This mirrors the search-replace
// step any WordPress migration performs, applied on read instead of at rest.
func rewriteSelfURLs(html string, baseURLs []string) string {
	for _, base := range baseURLs {
		if base == "" {
			continue
		}
		html = strings.ReplaceAll(html, base, "")
	}
	return html
}

func termView(t domain.Term) render.TermView {
	return render.TermView{Name: t.Name, Slug: t.Slug, Taxonomy: t.Taxonomy}
}
