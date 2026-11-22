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

// TestDefaultThemeArchivePaginationUsesHandlerLinks asserts the default theme's
// archive.tmpl renders the PrevURL and NextURL the handler built, verbatim, and
// never builds a ?page= link itself. The handler derives both from
// routing.Structure.PageLinks, so the theme must not re-derive them from
// .BaseURL or .Pagination. A Previous link is rendered only when PrevURL is set
// and a Next link only when NextURL is set, independent of the page numbers.
//
// Validates: Requirements 7.4
func TestDefaultThemeArchivePaginationUsesHandlerLinks(t *testing.T) {
	e := defaultEngine(t)
	cases := []struct {
		name       string
		page       int
		total      int
		prev, next string
		wantHref   []string
		wantText   []string
		notText    []string
	}{
		{
			name: "first page links next only", page: 1, total: 3,
			next:     "/sections/news/local/page/2/",
			wantHref: []string{`href="/sections/news/local/page/2/"`},
			wantText: []string{"Next"},
			notText:  []string{"Previous"},
		},
		{
			name: "page two previous is the base, never page 1", page: 2, total: 3,
			prev: "/sections/news/local/", next: "/sections/news/local/page/3/",
			wantHref: []string{`href="/sections/news/local/"`, `href="/sections/news/local/page/3/"`},
			wantText: []string{"Previous", "Next"},
			notText:  []string{"/page/1"},
		},
		{
			name: "middle page links both directions", page: 3, total: 5,
			prev: "/sections/news/local/page/2/", next: "/sections/news/local/page/4/",
			wantHref: []string{`href="/sections/news/local/page/2/"`, `href="/sections/news/local/page/4/"`},
			wantText: []string{"Previous", "Next"},
		},
		{
			name: "last page links previous only", page: 3, total: 3,
			prev:     "/sections/news/local/page/2/",
			wantHref: []string{`href="/sections/news/local/page/2/"`},
			wantText: []string{"Previous"},
			notText:  []string{"Next"},
		},
	}
	for _, kind := range archiveFallbackKinds {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					data := defaultThemeArchiveData(kind, tc.page, tc.total)
					data.PrevURL, data.NextURL = tc.prev, tc.next
					var buf bytes.Buffer
					if err := e.Render(&buf, kind, data); err != nil {
						t.Fatalf("render %q: %v", kind, err)
					}
					got := buf.String()
					for _, want := range append(tc.wantHref, tc.wantText...) {
						if !strings.Contains(got, want) {
							t.Fatalf("kind %q: output missing %s, got:\n%s", kind, want, got)
						}
					}
					for _, unwanted := range tc.notText {
						if strings.Contains(got, unwanted) {
							t.Fatalf("kind %q: output must not contain %s, got:\n%s", kind, unwanted, got)
						}
					}
					if strings.Contains(got, "?page=") {
						t.Fatalf("kind %q: the default theme must not build a ?page= link, got:\n%s", kind, got)
					}
				})
			}
		})
	}
}

// TestDefaultThemeArchiveHidesLinksWhenURLsEmpty asserts the links follow
// PrevURL and NextURL, not the page counters: with both empty on a middle page
// the pagination block still renders but neither Previous nor Next does.
//
// Validates: Requirements 7.4
func TestDefaultThemeArchiveHidesLinksWhenURLsEmpty(t *testing.T) {
	e := defaultEngine(t)
	for _, kind := range archiveFallbackKinds {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			if err := e.Render(&buf, kind, defaultThemeArchiveData(kind, 2, 3)); err != nil {
				t.Fatalf("render %q: %v", kind, err)
			}
			got := buf.String()
			for _, unwanted := range []string{"Previous", "Next", "?page="} {
				if strings.Contains(got, unwanted) {
					t.Fatalf("kind %q: empty PrevURL and NextURL must render no %q, got:\n%s", kind, unwanted, got)
				}
			}
		})
	}
}

// TestDefaultThemeCategoryRendersHandlerLinks is the same assertion for
// category.tmpl, which the category kind selects directly.
//
// Validates: Requirements 7.4
func TestDefaultThemeCategoryRendersHandlerLinks(t *testing.T) {
	e := defaultEngine(t)
	data := defaultThemeArchiveData("category", 2, 3)
	data.PrevURL, data.NextURL = "/sections/news/local/", "/sections/news/local/page/3/"
	var buf bytes.Buffer
	if err := e.Render(&buf, "category", data); err != nil {
		t.Fatalf("render category: %v", err)
	}
	got := buf.String()
	for _, want := range []string{`href="/sections/news/local/"`, `href="/sections/news/local/page/3/"`, "Previous", "Next"} {
		if !strings.Contains(got, want) {
			t.Fatalf("category output missing %s, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "?page=") {
		t.Fatalf("category.tmpl must not build a ?page= link, got:\n%s", got)
	}

	data.PrevURL, data.NextURL = "", ""
	buf.Reset()
	if err := e.Render(&buf, "category", data); err != nil {
		t.Fatalf("render category: %v", err)
	}
	for _, unwanted := range []string{"Previous", "Next", "?page="} {
		if strings.Contains(buf.String(), unwanted) {
			t.Fatalf("category: empty PrevURL and NextURL must render no %q, got:\n%s", unwanted, buf.String())
		}
	}
}

// TestDefaultThemeIndexRendersHandlerLinks asserts index.tmpl renders the
// PrevURL and NextURL the home handler supplies, and nothing else.
//
// Validates: Requirements 7.4
func TestDefaultThemeIndexRendersHandlerLinks(t *testing.T) {
	e := defaultEngine(t)
	base := IndexData{
		SiteTitle:  "grimoire",
		Tagline:    "A Go-native CMS",
		Posts:      []PostView{{Slug: "hello-world", Title: "Hello World", Excerpt: "First post.", Date: fixedDate}},
		Pagination: content.Page{Page: 2, PerPage: 10, Total: 30, TotalPages: 3},
	}
	render := func(t *testing.T, d IndexData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.Render(&buf, "index", d); err != nil {
			t.Fatalf("render index: %v", err)
		}
		return buf.String()
	}

	t.Run("both set", func(t *testing.T) {
		d := base
		d.PrevURL, d.NextURL = "/", "/page/3/"
		got := render(t, d)
		for _, want := range []string{`href="/" class="spectrum-Link">Previous`, `href="/page/3/" class="spectrum-Link">Next`} {
			if !strings.Contains(got, want) {
				t.Fatalf("index output missing %s, got:\n%s", want, got)
			}
		}
		if strings.Contains(got, "?page=") {
			t.Fatalf("index.tmpl must not build a ?page= link, got:\n%s", got)
		}
	})
	t.Run("both empty", func(t *testing.T) {
		got := render(t, base)
		for _, unwanted := range []string{"Previous", "Next", "?page="} {
			if strings.Contains(got, unwanted) {
				t.Fatalf("empty PrevURL and NextURL must render no %q, got:\n%s", unwanted, got)
			}
		}
	})
	t.Run("next only", func(t *testing.T) {
		d := base
		d.NextURL = "/page/3/"
		got := render(t, d)
		if !strings.Contains(got, `href="/page/3/"`) || strings.Contains(got, "Previous") {
			t.Fatalf("want Next without Previous, got:\n%s", got)
		}
	})
	t.Run("prev only", func(t *testing.T) {
		d := base
		d.PrevURL = "/"
		got := render(t, d)
		if !strings.Contains(got, "Previous") || strings.Contains(got, "Next") {
			t.Fatalf("want Previous without Next, got:\n%s", got)
		}
	})
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
