package web

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
)

// TestTermSlugifyMatchesContentSlugify pins the two slugifiers to the same
// algorithm (M17 task 9.1).
func TestTermSlugifyMatchesContentSlugify(t *testing.T) {
	for _, in := range []string{
		"", "Hello World", "  leading and trailing  ", "A--B__C", "ÀÉ café", "Go 1.22!",
		"---", "already-a-slug", "MiXeD CaSe 123", "日本語", "a   b", "x!",
	} {
		if got, want := termSlugify(in), content.Slugify(in); got != want {
			t.Errorf("termSlugify(%q) = %q, content.Slugify = %q", in, got, want)
		}
	}
}
