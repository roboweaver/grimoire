package web

import (
	"context"
	"html/template"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// TestPostViewManualExcerptRendersHTML: a manual post_excerpt is carried through
// to the view as raw HTML (template.HTML), not escaped.
func TestPostViewManualExcerptRendersHTML(t *testing.T) {
	v := postView(context.Background(), domain.Post{Excerpt: "<p>manual</p>", Content: "<p>body</p>"}, nil, nil)
	if v.Excerpt != template.HTML("<p>manual</p>") {
		t.Fatalf("manual excerpt should map to raw HTML; got %q", v.Excerpt)
	}
}

// TestPostViewEmptyExcerptAutoDerives: an empty post_excerpt is auto-derived
// from content, with block comments and tags stripped, wrapped in <p>.
func TestPostViewEmptyExcerptAutoDerives(t *testing.T) {
	body := "<!-- wp:paragraph --><p>Body text here</p><!-- /wp:paragraph -->"
	v := postView(context.Background(), domain.Post{Excerpt: "", Content: body}, nil, nil)
	if v.Excerpt != template.HTML("<p>Body text here</p>") {
		t.Fatalf("empty excerpt should auto-derive from content; got %q", v.Excerpt)
	}
}

// TestPostViewRewritesSelfURLs: absolute URLs matching the site's own
// siteurl/home baked into post_content (a common WordPress behavior — see
// rewriteSelfURLs) are rewritten to relative paths, so pages/media served by
// grimoire itself don't depend on the original host being reachable.
func TestPostViewRewritesSelfURLs(t *testing.T) {
	body := `<img src="http://127.0.0.1:8080/wp-content/uploads/2026/07/foo.png"> ` +
		`<a href="http://127.0.0.1:8080/?p=1">link</a>`
	v := postView(context.Background(), domain.Post{Content: body}, []string{"http://127.0.0.1:8080"}, nil)
	want := template.HTML(`<img src="/wp-content/uploads/2026/07/foo.png"> <a href="/?p=1">link</a>`)
	if v.Content != want {
		t.Fatalf("Content = %q, want %q", v.Content, want)
	}
}

// TestPostViewRewritesSelfURLsSchemeVariant: BaseURLs expands both http and
// https forms, so content authored under one scheme still rewrites cleanly
// when the option is stored under the other.
func TestPostViewRewritesSelfURLsSchemeVariant(t *testing.T) {
	body := `<img src="https://example.com/wp-content/uploads/foo.png">`
	v := postView(context.Background(), domain.Post{Content: body}, []string{"http://example.com", "https://example.com"}, nil)
	want := template.HTML(`<img src="/wp-content/uploads/foo.png">`)
	if v.Content != want {
		t.Fatalf("Content = %q, want %q", v.Content, want)
	}
}

// TestPostViewFeaturedImageURL verifies postView resolves the featured
// image via the injected content.FeaturedImageService, and that a nil
// service (unwired dependency) leaves FeaturedImageURL empty rather than
// panicking.
func TestPostViewFeaturedImageURL(t *testing.T) {
	meta := &viewTestPostMetaRepo{featuredMediaID: map[int64]int64{7: 100}}
	media := &viewTestMediaRepo{byID: map[int64]domain.Media{100: {ID: 100, URL: "/wp-content/uploads/foo.png"}}}
	featured := content.NewFeaturedImageService(meta, media)

	v := postView(context.Background(), domain.Post{ID: 7}, nil, featured)
	if v.FeaturedImageURL != "/wp-content/uploads/foo.png" {
		t.Fatalf("FeaturedImageURL = %q, want the attachment URL", v.FeaturedImageURL)
	}

	v = postView(context.Background(), domain.Post{ID: 7}, nil, nil)
	if v.FeaturedImageURL != "" {
		t.Fatalf("FeaturedImageURL = %q, want empty with nil service", v.FeaturedImageURL)
	}
}

type viewTestPostMetaRepo struct{ featuredMediaID map[int64]int64 }

func (f *viewTestPostMetaRepo) FeaturedMediaID(ctx context.Context, postID int64) (int64, error) {
	return f.featuredMediaID[postID], nil
}

func (f *viewTestPostMetaRepo) AttachmentMetadata(ctx context.Context, postID int64) (string, error) {
	return "", nil
}

type viewTestMediaRepo struct{ byID map[int64]domain.Media }

func (f *viewTestMediaRepo) List(ctx context.Context, filter domain.MediaFilter) ([]domain.Media, error) {
	return nil, nil
}

func (f *viewTestMediaRepo) Count(ctx context.Context, filter domain.MediaFilter) (int, error) {
	return 0, nil
}

func (f *viewTestMediaRepo) ByID(ctx context.Context, id int64) (domain.Media, error) {
	m, ok := f.byID[id]
	if !ok {
		return domain.Media{}, domain.ErrNotFound
	}
	return m, nil
}
