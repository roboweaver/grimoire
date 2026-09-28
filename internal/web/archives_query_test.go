package web_test

import (
	"net/http"
	"testing"
)

// Req 2.9's query-string half, and the archive counterpart to
// TestRedirectPreservesQueryString in permalinks_test.go. The two read the same
// way on purpose: one table of redirecting request forms, one set of query
// strings, and the assertion that the Location is the canonical path with the
// **raw** query appended unchanged.
//
// The status-code table in archives_test.go asserts each redirecting form's
// exact Location, but every one of its cases is queryless, so a handler that
// built its target from the path alone would pass all of them. That handler
// would silently send a visitor reading page 3 of an archive back to page 1 —
// the failure this file exists to catch — and it would do it on every
// redirecting archive URL at once, which is why the coverage here is the full
// cross product of forms and queries rather than one representative case.
//
// Three properties are asserted together, because they fail independently:
//
//   - the query survives at all (?page=);
//   - a multi-parameter query survives whole, in the order it arrived;
//   - the query is passed through **raw** rather than parsed and re-serialised.
//
// The last one needs its own cases because url.Values.Encode() round-trips most
// queries convincingly while changing others: it sorts keys, rewrites %20 as +,
// and gives a valueless parameter an "=". Each of the queries below is chosen so
// that a re-serialising handler produces a different string and fails, rather
// than one that happens to be stable under Encode.
//
// Everything here stays red until 7.6 registers ArchivePatterns() and 7.7
// implements the four archive handlers: with no archive routes, the nested,
// tag, author and date forms do not reach a handler that can redirect at all.

// archiveRedirectQueries are the raw query strings each redirecting form is
// asked to carry. "want" is deliberately not derived from "query" by any
// encode/decode step — it is the same literal, so the test asserts
// byte-for-byte passthrough rather than agreement between two encoders.
var archiveRedirectQueries = []struct {
	name  string
	query string
}{
	{
		// The case that matters most: dropping this moves a visitor from page 3
		// to page 1 with a 301 that looks correct in the network tab.
		name:  "page number",
		query: "page=3",
	},
	{
		// Unsorted keys, so a handler that rebuilt the query with
		// url.Values.Encode() would emit "a=1&b=2&page=3" and fail.
		name:  "several parameters keep their order",
		query: "page=3&b=2&a=1",
	},
	{
		// %20 rather than +, and an encoded slash in a value: Encode() would
		// rewrite the first as "+" and leave the second alone, so this
		// distinguishes raw passthrough from a re-serialised query.
		name:  "percent-encoded characters",
		query: "s=caf%C3%A9%20au%20lait&next=%2Fcategory%2Ftech%2F",
	},
	{
		// A valueless parameter and an empty value. Encode() writes "flag=" for
		// the first, so the raw form is the only one that survives.
		name:  "valueless and empty parameters",
		query: "flag&a=",
	},
}

// redirectForm is one non-canonical archive URL and the canonical path it must
// redirect to. The path carries no query; the test appends each of
// archiveRedirectQueries to it in turn.
type redirectForm struct {
	name string
	path string
	want string
}

// assertQueryPreserved runs every query variant against every redirecting form,
// plus the queryless control — without it, a handler that unconditionally
// appended "?" would pass every case above while emitting "/category/tech/go/?"
// for an ordinary link.
func assertQueryPreserved(t *testing.T, srv http.Handler, forms []redirectForm) {
	t.Helper()
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			for _, q := range archiveRedirectQueries {
				t.Run(q.name, func(t *testing.T) {
					assertRedirectLocation(t, srv, form.path+"?"+q.query, form.want+"?"+q.query)
				})
			}
			t.Run("no query string adds no ?", func(t *testing.T) {
				assertRedirectLocation(t, srv, form.path, form.want)
			})
		})
	}
}

func assertRedirectLocation(t *testing.T, srv http.Handler, path, want string) {
	t.Helper()
	rec := get(t, srv, path)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET %s = %d, want 301 (body: %.200s)", path, rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("GET %s Location = %q, want %q", path, got, want)
	}
}

// TestArchiveRedirectsPreserveQueryString covers every redirecting archive form
// under a trailing-slash structure (Req 2.9). The category cases are split by
// *why* the walk failed — flat, wrong ancestor, skipped level, wrong slash —
// because the four take different paths through the handler: the first three
// reach their target through the Req 2.11 recovery and the fourth does not, so
// a query appended in the recovery branch only would pass three of them.
func TestArchiveRedirectsPreserveQueryString(t *testing.T) {
	srv := newArchiveServer(t, structDayAndName)
	assertQueryPreserved(t, srv, []redirectForm{
		{
			name: "flat path of a nested category",
			path: "/category/go/",
			want: catGoCanonical,
		},
		{
			name: "flat path of a three-level nested category",
			path: "/category/generics/",
			want: catGenericsCanonical,
		},
		{
			name: "wrong-ancestor path",
			path: "/category/news/go/",
			want: catGoCanonical,
		},
		{
			name: "path skipping a level",
			path: "/category/tech/generics/",
			want: catGenericsCanonical,
		},
		{
			name: "nested category missing the trailing slash",
			path: slashless(catGoCanonical),
			want: catGoCanonical,
		},
		{
			// Req 2.4 makes this the canonical path's own slash form rather than
			// an ancestry failure, so it exercises the slash branch alone.
			name: "top-level category missing the trailing slash",
			path: slashless(catNewsCanonical),
			want: catNewsCanonical,
		},
		{
			// Both wrong at once: flat ancestry and the wrong slash form. A
			// handler that fixed one and redirected would lose the query on the
			// hop it did not expect to take.
			name: "flat path missing the trailing slash",
			path: "/category/go",
			want: catGoCanonical,
		},
		{
			name: "tag missing the trailing slash",
			path: slashless(tagGolangCanonical),
			want: tagGolangCanonical,
		},
		{
			name: "author missing the trailing slash",
			path: slashless(authorAdminCanonical),
			want: authorAdminCanonical,
		},
		{
			name: "year archive missing the trailing slash",
			path: "/2024",
			want: "/2024/",
		},
		{
			name: "year and month archive missing the trailing slash",
			path: "/2024/05",
			want: "/2024/05/",
		},
		{
			name: "year, month and day archive missing the trailing slash",
			path: "/2024/05/17",
			want: "/2024/05/17/",
		},
	})
}

// TestArchiveRedirectsPreserveQueryStringFlatStructure repeats the property with
// permalink_structure empty, where the canonical archive path carries **no**
// trailing slash (Req 2.7), so the slash direction reverses. A handler that
// preserved the query in one slash branch only would pass one of these two tests
// and drop pagination under the other. Date archives are absent deliberately: a
// flat structure registers none (Req 6.8).
func TestArchiveRedirectsPreserveQueryStringFlatStructure(t *testing.T) {
	srv := newArchiveServer(t, "")
	assertQueryPreserved(t, srv, []redirectForm{
		{
			name: "top-level category carrying a trailing slash",
			path: catNewsCanonical,
			want: slashless(catNewsCanonical),
		},
		{
			name: "nested category carrying a trailing slash",
			path: catGoCanonical,
			want: slashless(catGoCanonical),
		},
		{
			name: "flat path of a nested category",
			path: "/category/go",
			want: slashless(catGoCanonical),
		},
		{
			name: "flat path of a three-level nested category",
			path: "/category/generics",
			want: slashless(catGenericsCanonical),
		},
		{
			name: "wrong-ancestor path",
			path: "/category/news/go",
			want: slashless(catGoCanonical),
		},
		{
			name: "path skipping a level",
			path: "/category/tech/generics",
			want: slashless(catGenericsCanonical),
		},
		{
			name: "flat path carrying a trailing slash",
			path: "/category/go/",
			want: slashless(catGoCanonical),
		},
		{
			name: "tag carrying a trailing slash",
			path: tagGolangCanonical,
			want: slashless(tagGolangCanonical),
		},
		{
			name: "author carrying a trailing slash",
			path: authorAdminCanonical,
			want: slashless(authorAdminCanonical),
		},
	})
}
