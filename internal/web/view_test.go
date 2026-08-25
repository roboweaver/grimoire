package web

import (
	"html/template"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// TestPostViewManualExcerptRendersHTML: a manual post_excerpt is carried through
// to the view as raw HTML (template.HTML), not escaped.
func TestPostViewManualExcerptRendersHTML(t *testing.T) {
	v := postView(domain.Post{Excerpt: "<p>manual</p>", Content: "<p>body</p>"}, nil)
	if v.Excerpt != template.HTML("<p>manual</p>") {
		t.Fatalf("manual excerpt should map to raw HTML; got %q", v.Excerpt)
	}
}

// TestPostViewEmptyExcerptAutoDerives: an empty post_excerpt is auto-derived
// from content, with block comments and tags stripped, wrapped in <p>.
func TestPostViewEmptyExcerptAutoDerives(t *testing.T) {
	content := "<!-- wp:paragraph --><p>Body text here</p><!-- /wp:paragraph -->"
	v := postView(domain.Post{Excerpt: "", Content: content}, nil)
	if v.Excerpt != template.HTML("<p>Body text here</p>") {
		t.Fatalf("empty excerpt should auto-derive from content; got %q", v.Excerpt)
	}
}

// TestPostViewRewritesSelfURLs: absolute URLs matching the site's own
// siteurl/home baked into post_content (a common WordPress behavior — see
// rewriteSelfURLs) are rewritten to relative paths, so pages/media served by
// grimoire itself don't depend on the original host being reachable.
func TestPostViewRewritesSelfURLs(t *testing.T) {
	content := `<img src="http://127.0.0.1:8080/wp-content/uploads/2026/07/foo.png"> ` +
		`<a href="http://127.0.0.1:8080/?p=1">link</a>`
	v := postView(domain.Post{Content: content}, []string{"http://127.0.0.1:8080"})
	want := template.HTML(`<img src="/wp-content/uploads/2026/07/foo.png"> <a href="/?p=1">link</a>`)
	if v.Content != want {
		t.Fatalf("Content = %q, want %q", v.Content, want)
	}
}

// TestPostViewRewritesSelfURLsSchemeVariant: BaseURLs expands both http and
// https forms, so content authored under one scheme still rewrites cleanly
// when the option is stored under the other.
func TestPostViewRewritesSelfURLsSchemeVariant(t *testing.T) {
	content := `<img src="https://example.com/wp-content/uploads/foo.png">`
	v := postView(domain.Post{Content: content}, []string{"http://example.com", "https://example.com"})
	want := template.HTML(`<img src="/wp-content/uploads/foo.png">`)
	if v.Content != want {
		t.Fatalf("Content = %q, want %q", v.Content, want)
	}
}
