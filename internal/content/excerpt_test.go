package content

import (
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// TestExcerptManualHTMLPreserved: a non-empty post_excerpt is returned verbatim
// so its HTML renders (not escaped) on list views.
func TestExcerptManualHTMLPreserved(t *testing.T) {
	got := Excerpt(domain.Post{Excerpt: "<p>x</p>", Content: "<p>ignored body</p>"})
	if got != "<p>x</p>" {
		t.Fatalf("manual excerpt should be preserved verbatim; got %q", got)
	}
}

// TestExcerptAutoStripsBlockCommentsAndTags: an empty excerpt derives from
// content with Gutenberg block comments and tags stripped, wrapped in <p>.
func TestExcerptAutoStripsBlockCommentsAndTags(t *testing.T) {
	content := "<!-- wp:paragraph --><p>Hello <b>world</b></p><!-- /wp:paragraph -->"
	got := Excerpt(domain.Post{Excerpt: "", Content: content})
	if got != "<p>Hello world</p>" {
		t.Fatalf("auto excerpt = %q, want %q", got, "<p>Hello world</p>")
	}
	if strings.Contains(got, "wp:") {
		t.Fatalf("auto excerpt leaked a block-comment token: %q", got)
	}
	if strings.Contains(got, "<!--") {
		t.Fatalf("auto excerpt leaked a comment marker: %q", got)
	}
}

// TestExcerptTruncatesOver55Words: content over 55 words is trimmed to 55 words
// with a trailing ellipsis.
func TestExcerptTruncatesOver55Words(t *testing.T) {
	words := make([]string, 60)
	for i := range words {
		words[i] = "word"
	}
	got := Excerpt(domain.Post{Content: strings.Join(words, " ")})
	if !strings.HasSuffix(got, "…</p>") {
		t.Fatalf("truncated excerpt should end with the ellipsis; got %q", got)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(got, "<p>"), "…</p>")
	if n := len(strings.Fields(inner)); n != 55 {
		t.Fatalf("truncated excerpt should keep 55 words; got %d (%q)", n, got)
	}
}

// TestExcerptNoTruncateUnder55Words: content at or under 55 words is not
// truncated and gains no trailing ellipsis.
func TestExcerptNoTruncateUnder55Words(t *testing.T) {
	got := Excerpt(domain.Post{Content: "just a few short words here"})
	if got != "<p>just a few short words here</p>" {
		t.Fatalf("short content should be wrapped without ellipsis; got %q", got)
	}
	if strings.Contains(got, "…") {
		t.Fatalf("short content should not gain an ellipsis; got %q", got)
	}
}

// TestExcerptEmptyContentAndExcerpt: no excerpt and no content yields "" — no
// stray <p></p>, no panic. Tags-only content also collapses to "".
func TestExcerptEmptyContentAndExcerpt(t *testing.T) {
	if got := Excerpt(domain.Post{}); got != "" {
		t.Fatalf("empty post should yield empty excerpt; got %q", got)
	}
	if got := Excerpt(domain.Post{Content: "<p></p>"}); got != "" {
		t.Fatalf("tags-only content should yield empty excerpt; got %q", got)
	}
	if got := Excerpt(domain.Post{Excerpt: "   "}); got != "" {
		t.Fatalf("whitespace-only excerpt with empty content should yield empty; got %q", got)
	}
}
