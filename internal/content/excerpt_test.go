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

// TestExcerptPreservesEscapedEntities: WordPress stores literal/dangerous markup
// HTML-escaped so it renders as text. The auto-excerpt path must NOT decode
// entities — the result is emitted raw (template.HTML), so decoding would turn
// stored `&lt;script&gt;` into a live <script>. Entities must pass through intact
// and render as literal glyphs. Re-adding html.UnescapeString makes this fail.
func TestExcerptPreservesEscapedEntities(t *testing.T) {
	content := "Here is code: &lt;script&gt;alert(1)&lt;/script&gt; and more text follows here."
	got := Excerpt(domain.Post{Excerpt: "", Content: content})
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("escaped entities must be preserved; got %q", got)
	}
	if strings.Contains(got, "<script>") {
		t.Fatalf("auto excerpt must not emit live markup from escaped content; got %q", got)
	}
}

// TestStripBlockComments: the package-visible StripBlockComments removes
// Gutenberg block-delimiter comments (opening and closing, including the
// whitespace variants the existing blockCommentRE matches) while preserving the
// inner block content, and leaves comment-free content unchanged.
//
// This documents the helper that Excerpt will delegate to; the existing Excerpt
// tests remain the real guard that the forthcoming refactor is
// behavior-preserving.
func TestStripBlockComments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "opening and closing delimiters removed, inner preserved",
			in:   "<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->",
			want: "<p>Hi</p>",
		},
		{
			name: "closing delimiter alone removed",
			in:   "<p>Hi</p><!-- /wp:paragraph -->",
			want: "<p>Hi</p>",
		},
		{
			name: "no-space whitespace variant removed",
			in:   "<!--wp:image--><img/>",
			want: "<img/>",
		},
		{
			name: "extra-space whitespace variant removed",
			in:   "<!--  wp:list  --><ul></ul>",
			want: "<ul></ul>",
		},
		{
			name: "self-closing/void block comment removed",
			in:   "before<!-- wp:spacer /-->after",
			want: "beforeafter",
		},
		{
			name: "comment-free content unchanged",
			in:   "<p>plain</p>",
			want: "<p>plain</p>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripBlockComments(tt.in); got != tt.want {
				t.Fatalf("StripBlockComments(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
