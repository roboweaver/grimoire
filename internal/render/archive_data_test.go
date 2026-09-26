package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
)

// archiveDataKinds are the four render kinds ArchiveData backs. M9a already
// registered every one of them in the hierarchy map, so what is under test here
// is the data shape they render with, not the resolution.
var archiveDataKinds = []string{"category", "tag", "author", "date"}

// archiveDataContent is a content template that reads every field of
// ArchiveData, so an execution-time "can't evaluate field" error on any one of
// them fails the test. The resolved: marker identifies which file on disk won
// the hierarchy lookup, matching the convention in hierarchy_test.go.
func archiveDataContent(name string) string {
	return `{{define "content"}}<p>resolved:` + name + `</p>` +
		`<h1>heading:{{.Heading}}</h1>` +
		`<p>kind:{{.Kind}}</p>` +
		`<a href="{{.BaseURL}}?page={{add .Pagination.Page 1}}">next</a>` +
		`<p>term:{{.Term.Name}}</p>` +
		`{{range .Posts}}<article>post:{{.Title}}</article>{{end}}` +
		`<p>pages:{{.Pagination.TotalPages}}</p>` +
		`{{end}}`
}

// archiveDataFixture is the ArchiveData a nested category archive would supply:
// a heading, the canonical base path pagination links are built from, the term,
// the post list and the content.Page (Req 11.1).
func archiveDataFixture(kind string) ArchiveData {
	return ArchiveData{
		SiteTitle:  "grimoire",
		Tagline:    "A Go-native CMS",
		Kind:       kind,
		Heading:    "Local",
		BaseURL:    "/category/news/local",
		Term:       TermView{Name: "Local", Slug: "local", Taxonomy: "category"},
		Posts:      []PostView{{Slug: "hello-world", Title: "Hello World", Excerpt: "First post.", Date: fixedDate}},
		Pagination: content.Page{Page: 1, PerPage: 10, Total: 12, TotalPages: 2},
	}
}

// TestRenderArchiveDataThroughArchiveKinds asserts that ArchiveData renders
// through the category, tag, author and date kinds by way of the real template
// resolution path, and that a template reading Heading, BaseURL, Kind, Term,
// Posts and Pagination off it executes without a missing-field error.
//
// Validates: Requirements 11.1
func TestRenderArchiveDataThroughArchiveKinds(t *testing.T) {
	for _, kind := range archiveDataKinds {
		t.Run(kind, func(t *testing.T) {
			root, theme := writeTheme(t, map[string]string{
				"base.tmpl":    miniBase,
				"index.tmpl":   kindMarker("index"),
				kind + ".tmpl": archiveDataContent(kind),
			})
			e, err := Load(root, theme)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			var buf bytes.Buffer
			if err := e.Render(&buf, kind, archiveDataFixture(kind)); err != nil {
				t.Fatalf("Render %q with ArchiveData: %v", kind, err)
			}
			got := buf.String()
			for _, want := range []string{
				"resolved:" + kind,
				"heading:Local",
				"kind:" + kind,
				`href="/category/news/local?page=2"`,
				"term:Local",
				"post:Hello World",
				"pages:2",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("kind %q: rendered output missing %q, got %q", kind, want, got)
				}
			}
		})
	}
}

// TestRenderArchiveDataFallsBackToArchiveTemplate asserts the same for a theme
// that provides only archive.tmpl — the case the default theme is in for tag,
// author and date, none of which has a template of its own (Req 11.3).
//
// Validates: Requirements 11.1, 11.4
func TestRenderArchiveDataFallsBackToArchiveTemplate(t *testing.T) {
	root, theme := writeTheme(t, map[string]string{
		"base.tmpl":    miniBase,
		"index.tmpl":   kindMarker("index"),
		"archive.tmpl": archiveDataContent("archive"),
	})
	e, err := Load(root, theme)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, kind := range archiveDataKinds {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			if err := e.Render(&buf, kind, archiveDataFixture(kind)); err != nil {
				t.Fatalf("Render %q through archive.tmpl: %v", kind, err)
			}
			got := buf.String()
			if !strings.Contains(got, "resolved:archive") {
				t.Fatalf("kind %q: want the archive fallback, got %q", kind, got)
			}
			if !strings.Contains(got, "kind:"+kind) {
				t.Fatalf("kind %q: archive template must see Kind, got %q", kind, got)
			}
		})
	}
}

// TestCategoryDataIsAnArchiveDataAlias is the compile-time half of Req 11.2:
// CategoryData must be an alias for ArchiveData, not a distinct named type, so
// existing call sites and theme templates keep working. A named type would
// still render, but it would not be assignable in either direction, and the
// handlers that build one value and the templates that read another would drift
// apart silently.
//
// Validates: Requirements 11.2
func TestCategoryDataIsAnArchiveDataAlias(t *testing.T) {
	var fromArchive CategoryData = ArchiveData{Kind: "category"}
	var fromCategory ArchiveData = CategoryData{Kind: "category"}
	if fromArchive.Kind != fromCategory.Kind {
		t.Fatalf("alias assignment lost Kind: %q vs %q", fromArchive.Kind, fromCategory.Kind)
	}
}

// TestRenderCategoryDataPreM9bFields asserts a value constructed the pre-M9b
// way — SiteTitle, Tagline, Term, Posts, Pagination and nothing else — still
// compiles and still renders through the category kind. html/template resolves
// field names at execution time, so dropping or renaming a field the existing
// category.tmpl references would turn a working page into a 500 that no
// compile step catches; this is the test that says so.
//
// Validates: Requirements 11.2
func TestRenderCategoryDataPreM9bFields(t *testing.T) {
	data := CategoryData{
		SiteTitle:  "grimoire",
		Tagline:    "A Go-native CMS",
		Term:       TermView{Name: "News", Slug: "news", Taxonomy: "category"},
		Posts:      []PostView{{Slug: "hello-world", Title: "Hello World", Excerpt: "First post.", Date: fixedDate}},
		Pagination: content.Page{Page: 1, PerPage: 10, Total: 1, TotalPages: 1},
	}

	// The pre-M9b category.tmpl shape: the term name as the heading, the post
	// list, and pagination read off .Term.Slug.
	root, theme := writeTheme(t, map[string]string{
		"base.tmpl":  miniBase,
		"index.tmpl": kindMarker("index"),
		"category.tmpl": `{{define "content"}}<h1>{{.Term.Name}}</h1>` +
			`{{range .Posts}}<article>post:{{.Title}}</article>{{end}}` +
			`<p>page:{{.Pagination.Page}}/{{.Pagination.TotalPages}}</p>` +
			`<a href="/category/{{.Term.Slug}}?page=1">1</a>{{end}}`,
	})
	e, err := Load(root, theme)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "category", data); err != nil {
		t.Fatalf("Render category with a pre-M9b CategoryData: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"<h1>News</h1>", "post:Hello World", "page:1/1", `href="/category/news?page=1"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output missing %q, got %q", want, got)
		}
	}
}
