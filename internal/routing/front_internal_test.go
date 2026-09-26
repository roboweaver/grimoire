// This file is in package routing rather than routing_test, unlike the rest of
// the package's tests, because the front is deliberately unexported: it is
// consumed only by the archive path constructors and Classify inside this
// package, and exporting an accessor purely to test it would publish a field no
// caller has a reason to read. The derivation itself still needs its own test,
// because every archive kind's path is built on top of it.
package routing

import "testing"

// Requirement 4.7: the front is the permalink structure's leading literal
// segment(s) -- the maximal run of literals before the first token. "Maximal
// run" and "leading" are both load-bearing: a structure may put a literal
// *after* a token (/%year%/blog/%postname%/), and that literal belongs to the
// post path only, never to an archive path.
func TestParseDerivesFront(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		want      []string
	}{
		{
			name:      "no leading literal yields no front",
			structure: "/%year%/%monthnum%/%day%/%postname%/",
		},
		{
			name:      "one leading literal",
			structure: "/blog/%year%/%monthnum%/%postname%/",
			want:      []string{"blog"},
		},
		{
			// Maximal run: both literals are part of the front, so an
			// implementation that took only the first segment reports "blog"
			// here and builds /blog/category/news for a site serving
			// /blog/news/category/news.
			name:      "two leading literals are both the front",
			structure: "/blog/news/%postname%/",
			want:      []string{"blog", "news"},
		},
		{
			// WordPress's numeric preset already carries a front; nothing in
			// M9a consumed it.
			name:      "leading literal before an id token",
			structure: "/archives/%post_id%",
			want:      []string{"archives"},
		},
		{
			// A literal that is not leading is not the front. Stripping every
			// literal instead of the leading run would give "blog" here and
			// advertise /blog/category/news on a site that serves
			// /category/news.
			name:      "a literal after a token is not part of the front",
			structure: "/%year%/blog/%postname%/",
		},
		{
			name:      "the run stops at the first token",
			structure: "/blog/%year%/news/%postname%/",
			want:      []string{"blog"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.structure, err)
			}
			assertFront(t, s.front, tc.want)
		})
	}
}

// A Flat structure serves no archive at a front -- it has no parsed segments at
// all, and DatePath returns "" for it (Req 6.8). Both flat paths are asserted:
// the empty option, and an unsupported structure whose *leading segment is a
// literal*, which is the one shape where a front could plausibly leak into the
// fallback Structure the caller keeps serving with.
func TestParseFrontIsEmptyForFlatStructure(t *testing.T) {
	t.Run("empty structure", func(t *testing.T) {
		s, err := Parse("", "", "")
		if err != nil {
			t.Fatalf("Parse(\"\") error: %v", err)
		}
		if !s.Flat {
			t.Fatalf("Flat = false, want true")
		}
		assertFront(t, s.front, nil)
	})

	t.Run("unsupported structure with a leading literal", func(t *testing.T) {
		s, err := Parse("/blog/", "", "")
		if err == nil {
			t.Fatalf("Parse(\"/blog/\") = nil error, want ErrUnsupported")
		}
		if !s.Flat {
			t.Fatalf("Flat = false, want true")
		}
		assertFront(t, s.front, nil)
	})
}

func assertFront(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("front = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("front = %q, want %q", got, want)
		}
	}
}
