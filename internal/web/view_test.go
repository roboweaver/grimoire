package web

import (
	"html/template"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// TestPostViewManualExcerptRendersHTML: a manual post_excerpt is carried through
// to the view as raw HTML (template.HTML), not escaped.
func TestPostViewManualExcerptRendersHTML(t *testing.T) {
	v := postView(domain.Post{Excerpt: "<p>manual</p>", Content: "<p>body</p>"})
	if v.Excerpt != template.HTML("<p>manual</p>") {
		t.Fatalf("manual excerpt should map to raw HTML; got %q", v.Excerpt)
	}
}

// TestPostViewEmptyExcerptAutoDerives: an empty post_excerpt is auto-derived
// from content, with block comments and tags stripped, wrapped in <p>.
func TestPostViewEmptyExcerptAutoDerives(t *testing.T) {
	content := "<!-- wp:paragraph --><p>Body text here</p><!-- /wp:paragraph -->"
	v := postView(domain.Post{Excerpt: "", Content: content})
	if v.Excerpt != template.HTML("<p>Body text here</p>") {
		t.Fatalf("empty excerpt should auto-derive from content; got %q", v.Excerpt)
	}
}
