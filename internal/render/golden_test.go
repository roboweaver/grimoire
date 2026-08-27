package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "update golden files")

// themesRoot resolves the repo's themes/ directory relative to this package.
func themesRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "themes")
}

func goldenCompare(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func defaultEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := Load(themesRoot(t), "default")
	if err != nil {
		t.Fatalf("Load default theme: %v", err)
	}
	return e
}

var fixedDate = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

func TestGoldenIndex(t *testing.T) {
	e := defaultEngine(t)
	data := IndexData{
		SiteTitle: "grimoire",
		Tagline:   "A Go-native CMS",
		Posts: []PostView{
			{Slug: "hello-world", Title: "Hello World", Excerpt: "First post.", Date: fixedDate},
			{Slug: "second", Title: "Second Post", Excerpt: "Another one.", Date: fixedDate},
		},
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "index", data); err != nil {
		t.Fatalf("render index: %v", err)
	}
	goldenCompare(t, "index.html", buf.Bytes())
}

func TestGoldenSingle(t *testing.T) {
	e := defaultEngine(t)
	data := SingleData{
		SiteTitle: "grimoire",
		Post:      PostView{ID: 1, Slug: "hello-world", Title: "Hello World", Content: "<p>Body <em>html</em>.</p>", Date: fixedDate},
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "single", data); err != nil {
		t.Fatalf("render single: %v", err)
	}
	goldenCompare(t, "single.html", buf.Bytes())
}

func TestGoldenCategory(t *testing.T) {
	e := defaultEngine(t)
	data := CategoryData{
		SiteTitle: "grimoire",
		Term:      TermView{Name: "News", Slug: "news", Taxonomy: "category"},
		Posts: []PostView{
			{Slug: "hello-world", Title: "Hello World", Excerpt: "First post.", Date: fixedDate},
		},
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "category", data); err != nil {
		t.Fatalf("render category: %v", err)
	}
	goldenCompare(t, "category.html", buf.Bytes())
}

// TestSiteIdentityRendersOnce guards against regressing to two competing
// site-identity elements on a single page (e.g. a persistent header link
// plus a duplicate heading in the page content). Every page template must
// render the site title exactly once in the visible body; the <title>
// element in <head> is intentionally excluded since it legitimately repeats
// the site title for the browser tab/window.
func TestSiteIdentityRendersOnce(t *testing.T) {
	e := defaultEngine(t)
	cases := []struct {
		name     string
		template string
		data     any
	}{
		{
			name:     "index",
			template: "index",
			data: IndexData{
				SiteTitle: "grimoire",
				Tagline:   "A Go-native CMS",
				Posts: []PostView{
					{Slug: "hello-world", Title: "Hello World", Excerpt: "First post.", Date: fixedDate},
				},
			},
		},
		{
			name:     "single",
			template: "single",
			data: SingleData{
				SiteTitle: "grimoire",
				Post:      PostView{ID: 1, Slug: "hello-world", Title: "Hello World", Content: "<p>Body.</p>", Date: fixedDate},
			},
		},
		{
			name:     "category",
			template: "category",
			data: CategoryData{
				SiteTitle: "grimoire",
				Term:      TermView{Name: "News", Slug: "news", Taxonomy: "category"},
				Posts: []PostView{
					{Slug: "hello-world", Title: "Hello World", Excerpt: "First post.", Date: fixedDate},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := e.Render(&buf, tc.template, tc.data); err != nil {
				t.Fatalf("render %s: %v", tc.name, err)
			}
			body := bodyOnly(t, buf.String())
			if n := strings.Count(body, "grimoire"); n != 1 {
				t.Fatalf("expected site title %q to appear exactly once in <body> for %s, got %d occurrences:\n%s", "grimoire", tc.name, n, body)
			}
		})
	}
}

// bodyOnly returns the header+main region of the rendered page: everything
// after the closing </head> tag and before the opening <footer> tag. This
// excludes both the <title> element (which legitimately repeats the site
// title for the browser tab/window) and the footer's "Powered by grimoire"
// attribution (which legitimately repeats the site name outside of any
// site-identity/branding element).
func bodyOnly(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "</head>")
	if start == -1 {
		t.Fatalf("rendered HTML has no </head>:\n%s", html)
	}
	start += len("</head>")
	end := strings.Index(html, "<footer")
	if end == -1 {
		t.Fatalf("rendered HTML has no <footer>:\n%s", html)
	}
	return html[start:end]
}
