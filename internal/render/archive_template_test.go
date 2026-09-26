package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
)

// archiveFallbackKinds are the kinds that reach themes/default's archive.tmpl.
// The default theme ships no tag.tmpl, author.tmpl or date.tmpl, so all three
// fall back to archive.tmpl (each {kind} → archive → index), and "archive"
// selects it directly. That is why archive.tmpl has to render the supplied
// heading and pagination rather than a literal word: it is the only template
// three of the four archive kinds ever get (Req 11.3).
var archiveFallbackKinds = []string{"archive", "tag", "author", "date"}

// defaultThemeArchiveData is the ArchiveData a handler supplies for a nested
// category archive on a site whose category_base is overridden to "sections".
// BaseURL is deliberately unreconstructable from Term.Slug — "/sections/news/local"
// shares no prefix with "/category/" and carries two ancestry segments — so a
// template that string-concatenates a base instead of reading .BaseURL cannot
// accidentally produce the right link (Req 8.4).
func defaultThemeArchiveData(kind string, page, totalPages int) ArchiveData {
	return ArchiveData{
		SiteTitle: "grimoire",
		Tagline:   "A Go-native CMS",
		Kind:      kind,
		Heading:   "Local",
		BaseURL:   "/sections/news/local",
		Term:      TermView{Name: "Local", Slug: "local", Taxonomy: "category"},
		Posts: []PostView{
			{Slug: "hello-world", Title: "Hello World", Excerpt: "First post.", Date: fixedDate},
		},
		Pagination: content.Page{Page: page, PerPage: 10, Total: totalPages * 10, TotalPages: totalPages},
	}
}

// TestDefaultThemeArchiveRendersHeading asserts themes/default's archive.tmpl
// renders the heading the handler supplied as the page's <h1>, rather than the
// literal word "Archive" it hard-codes today. This is asserted against the
// shipped theme, not a synthetic one, because the shipped theme is what tag,
// author and date archives are served with.
//
// Validates: Requirements 11.3
func TestDefaultThemeArchiveRendersHeading(t *testing.T) {
	e := defaultEngine(t)
	for _, kind := range archiveFallbackKinds {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			if err := e.Render(&buf, kind, defaultThemeArchiveData(kind, 1, 1)); err != nil {
				t.Fatalf("render %q through the default theme: %v", kind, err)
			}
			body := bodyOnly(t, buf.String())
			if h1 := singleH1(t, body); !strings.Contains(h1, "Local") {
				t.Fatalf("kind %q: want the supplied .Heading in the <h1>, got %q", kind, h1)
			}
			if strings.Contains(body, "Archive") {
				t.Fatalf("kind %q: archive.tmpl must not render a literal %q heading; body was:\n%s", kind, "Archive", body)
			}
		})
	}
}

// TestDefaultThemeArchivePaginationUsesBaseURL asserts archive.tmpl renders
// pagination when TotalPages > 1, and that every link is built from .BaseURL —
// the canonical archive path the handler resolved — rather than by concatenating
// a hard-coded base with .Term.Slug the way category.tmpl does today. The
// hard-coded form is wrong for a nested category and wrong for an overridden
// category_base, and Term is empty entirely for the author and date kinds.
//
// Validates: Requirements 8.4, 11.3
func TestDefaultThemeArchivePaginationUsesBaseURL(t *testing.T) {
	e := defaultEngine(t)
	cases := []struct {
		name     string
		page     int
		total    int
		wantHref []string
		notHref  []string
	}{
		{
			name:     "first page links next only",
			page:     1,
			total:    3,
			wantHref: []string{`href="/sections/news/local?page=2"`},
			notHref:  []string{`?page=0"`},
		},
		{
			name:     "middle page links both directions",
			page:     2,
			total:    3,
			wantHref: []string{`href="/sections/news/local?page=1"`, `href="/sections/news/local?page=3"`},
		},
		{
			name:     "last page links previous only",
			page:     3,
			total:    3,
			wantHref: []string{`href="/sections/news/local?page=2"`},
			notHref:  []string{`?page=4"`},
		},
	}
	for _, kind := range archiveFallbackKinds {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					var buf bytes.Buffer
					if err := e.Render(&buf, kind, defaultThemeArchiveData(kind, tc.page, tc.total)); err != nil {
						t.Fatalf("render %q: %v", kind, err)
					}
					got := buf.String()
					for _, want := range tc.wantHref {
						if !strings.Contains(got, want) {
							t.Fatalf("kind %q: pagination missing %s, got:\n%s", kind, want, got)
						}
					}
					for _, unwanted := range tc.notHref {
						if strings.Contains(got, unwanted) {
							t.Fatalf("kind %q: pagination must not link %s, got:\n%s", kind, unwanted, got)
						}
					}
					// A link built from a hard-coded base plus the term slug is
					// the defect Req 8.4 names; assert it is absent rather than
					// only that the right link is present, since both can hold.
					if strings.Contains(got, `href="/category/`) {
						t.Fatalf("kind %q: pagination must be built from .BaseURL, not a hard-coded /category/ base, got:\n%s", kind, got)
					}
				})
			}
		})
	}
}

// TestDefaultThemeArchiveOmitsPaginationOnSinglePage asserts the pagination block
// is absent when there is only one page, matching category.tmpl's existing
// `if gt .Pagination.TotalPages 1` guard so the two templates agree.
//
// Validates: Requirements 8.4, 11.3
func TestDefaultThemeArchiveOmitsPaginationOnSinglePage(t *testing.T) {
	e := defaultEngine(t)
	for _, kind := range archiveFallbackKinds {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			if err := e.Render(&buf, kind, defaultThemeArchiveData(kind, 1, 1)); err != nil {
				t.Fatalf("render %q: %v", kind, err)
			}
			got := buf.String()
			if strings.Contains(got, "theme-pagination") {
				t.Fatalf("kind %q: want no pagination nav for a single page, got:\n%s", kind, got)
			}
			if strings.Contains(got, "?page=") {
				t.Fatalf("kind %q: want no page links for a single page, got:\n%s", kind, got)
			}
		})
	}
}
