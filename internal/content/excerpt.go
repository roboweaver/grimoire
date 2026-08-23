package content

import (
	"regexp"
	"strings"

	"github.com/roboweaver/grimoire/internal/domain"
)

// excerptWords is the WordPress default auto-excerpt length (the_excerpt →
// wp_trim_excerpt → excerpt_length filter defaults to 55 words).
const excerptWords = 55

// ellipsis is appended to a truncated auto-excerpt. WordPress uses " […]"; the
// M2.2 brief specifies the single "…" glyph.
const ellipsis = "…"

var (
	// Gutenberg block-delimiter comments: <!-- wp:paragraph --> and its
	// closing <!-- /wp:paragraph -->. Non-greedy, dot-matches-newline.
	blockCommentRE = regexp.MustCompile(`(?s)<!--\s*/?\s*wp:.*?-->`)
	// Shortcodes such as [gallery ids="1,2"]. Stripped before tags so their
	// attributes never leak.
	shortcodeRE = regexp.MustCompile(`\[[^\]]*\]`)
	// Any remaining HTML/XML tag.
	tagRE = regexp.MustCompile(`(?s)<[^>]*>`)
	// Runs of whitespace, collapsed to a single space.
	wsRE = regexp.MustCompile(`\s+`)
)

// Excerpt returns the list-view summary for a post, matching WordPress
// the_excerpt() semantics.
//
// A manual excerpt (non-empty post_excerpt) is returned verbatim as trusted
// WordPress HTML — it already renders as authored and is not re-run through
// wpautop. An empty post_excerpt is auto-generated from post_content the way
// wp_trim_excerpt does: strip Gutenberg block comments, shortcodes and HTML
// tags, collapse whitespace, trim to excerptWords words (appending the ellipsis
// only when truncated), and wrap the plain text in a single <p>…</p> (minimal
// wpautop). Escaped entities are left encoded (matching wp_trim_excerpt, which
// does not html-decode): the result is emitted raw as template.HTML, so
// decoding would turn stored `&lt;script&gt;` text into live markup. Empty
// content with an empty excerpt yields "" — no stray <p></p>.
//
// The returned string is trusted HTML. Callers cast it to template.HTML at the
// web trust boundary (see internal/web/view.go); this package deliberately does
// not import html/template so the trust decision lives in one place.
func Excerpt(p domain.Post) string {
	if strings.TrimSpace(p.Excerpt) != "" {
		return p.Excerpt
	}

	text := blockCommentRE.ReplaceAllString(p.Content, "")
	text = shortcodeRE.ReplaceAllString(text, "")
	text = tagRE.ReplaceAllString(text, "")
	text = strings.TrimSpace(wsRE.ReplaceAllString(text, " "))
	if text == "" {
		return ""
	}

	words := strings.Fields(text)
	if len(words) > excerptWords {
		text = strings.Join(words[:excerptWords], " ") + ellipsis
	}

	return "<p>" + text + "</p>"
}
