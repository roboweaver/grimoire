package render

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// TestIndexRendersManualHTMLExcerpt proves the index/list template renders a
// manual HTML excerpt raw rather than escaping it. Reverting PostView.Excerpt
// back to `string` makes this fail (html/template escapes the angle brackets).
func TestIndexRendersManualHTMLExcerpt(t *testing.T) {
	e := defaultEngine(t)
	data := IndexData{
		SiteTitle: "grimoire",
		Tagline:   "A Go-native CMS",
		Posts: []PostView{
			{Slug: "s", Title: "T", Excerpt: template.HTML("<p>x</p>"), Date: fixedDate},
		},
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "index", data); err != nil {
		t.Fatalf("render index: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "<p>x</p>") {
		t.Fatalf("index should render the manual excerpt as raw HTML; got:\n%s", out)
	}
	if strings.Contains(out, "&lt;p&gt;x&lt;/p&gt;") {
		t.Fatalf("index escaped the manual excerpt HTML; got:\n%s", out)
	}
}
