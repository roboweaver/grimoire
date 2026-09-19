package render

import (
	"bytes"
	"strings"
	"testing"
)

// archiveKinds are the render kinds added by roadmap group 9.F. No route serves
// them yet (they arrive with 9.C), so the hierarchy is what is under test here,
// not any handler.
var archiveKinds = []string{"tag", "author", "date"}

// kindMarker returns a content template whose body identifies which file on disk
// won the hierarchy lookup, so a fallback that picks the wrong candidate is
// distinguishable from one that picks the right one.
func kindMarker(name string) string {
	return `{{define "content"}}<p>resolved:` + name + `</p>{{end}}`
}

// TestRenderArchiveKindHierarchy asserts that the tag, author and date kinds
// resolve through {kind} -> archive -> index: the kind's own template wins when
// present, archive wins when it is not, and index is used when neither is
// present. Requirement 5.3 is why these are asserted now rather than alongside
// the routes that will use them.
//
// Validates: Requirements 5.2, 5.3
func TestRenderArchiveKindHierarchy(t *testing.T) {
	for _, kind := range archiveKinds {
		t.Run(kind, func(t *testing.T) {
			cases := []struct {
				name  string
				files map[string]string
				want  string
			}{
				{
					name: "kind template wins over archive and index",
					files: map[string]string{
						kind + ".tmpl": kindMarker(kind),
						"archive.tmpl": kindMarker("archive"),
						"index.tmpl":   kindMarker("index"),
					},
					want: kind,
				},
				{
					name: "archive wins when the kind template is absent",
					files: map[string]string{
						"archive.tmpl": kindMarker("archive"),
						"index.tmpl":   kindMarker("index"),
					},
					want: "archive",
				},
				{
					name: "index is used when neither kind nor archive is present",
					files: map[string]string{
						"index.tmpl": kindMarker("index"),
					},
					want: "index",
				},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					files := map[string]string{"base.tmpl": miniBase}
					for name, body := range tc.files {
						files[name] = body
					}
					root, theme := writeTheme(t, files)
					e, err := Load(root, theme)
					if err != nil {
						t.Fatalf("Load: %v", err)
					}
					var buf bytes.Buffer
					if err := e.Render(&buf, kind, CategoryData{SiteTitle: "grimoire"}); err != nil {
						t.Fatalf("Render %q: %v", kind, err)
					}
					if got := buf.String(); !strings.Contains(got, "resolved:"+tc.want) {
						t.Fatalf("kind %q: want the %q template to win, got %q", kind, tc.want, got)
					}
				})
			}
		})
	}
}

// TestRenderArchiveKindFallsBackToIndex asserts that a theme providing none of a
// kind's candidate templates still renders through index rather than returning
// an error, preserving the behavior that exists for the kinds already in the
// hierarchy.
//
// Validates: Requirements 5.5
func TestRenderArchiveKindFallsBackToIndex(t *testing.T) {
	root, theme := writeTheme(t, map[string]string{
		"base.tmpl":  miniBase,
		"index.tmpl": kindMarker("index"),
	})
	e, err := Load(root, theme)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, kind := range archiveKinds {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			if err := e.Render(&buf, kind, CategoryData{SiteTitle: "grimoire"}); err != nil {
				t.Fatalf("Render %q must fall back to index, got error: %v", kind, err)
			}
			if got := buf.String(); !strings.Contains(got, "resolved:index") {
				t.Fatalf("kind %q: want the index fallback, got %q", kind, got)
			}
		})
	}
}
