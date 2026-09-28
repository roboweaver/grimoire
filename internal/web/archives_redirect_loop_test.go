package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Req 2.4's explicit no-loop test, and the archive counterpart to
// TestCanonicalPathDoesNotRedirect in permalinks_test.go.
//
// The status-code table in archives_test.go asserts a 200 at each canonical path
// and an exact Location for each redirecting form. Neither statement rules out a
// loop on its own: a handler that recomputes the canonical path from the term it
// just resolved could emit a Location the next request redirects away from
// again, and the table would still pass every row it checks. So these tests
// assert the property the table cannot — that the canonical path is a **fixed
// point**, and that following the handler's own Location lands on a rendered
// page rather than on another redirect.
//
// The nested-category case is the one that earns the separate test (design.md,
// "Redirect loops"): its redirect target comes from a walk through the taxonomy
// graph rather than from a field read, so a walk that disagreed with itself on
// the second pass would loop on every category URL on the site. The
// three-level chain is included because a walk that resolves one hop and stops
// would satisfy the two-level case.

// maxRedirectHops bounds the chain follower. The canonical model allows exactly
// one hop — a recognised non-canonical form redirects to the canonical path,
// which renders — so anything beyond a small bound is a defect whichever shape
// it takes, and the bound keeps a genuine cycle from hanging the test run.
const maxRedirectHops = 4

// redirectChain follows the handler's own Location headers starting at path. It
// returns every path visited, path first, and the response that finally did not
// redirect. It fails the test on a repeated path (a cycle), on a chain longer
// than maxRedirectHops (a cycle the repeat check cannot see because each hop
// differs), and on a redirect that names no target.
//
// Any 3xx carrying a Location counts as a hop rather than as a terminal
// response, so a loop built out of 302s is still reported as a loop. The
// requirement that archive redirects are specifically 301 belongs to the
// status-code table, not here.
func redirectChain(t *testing.T, srv http.Handler, path string) ([]string, *httptest.ResponseRecorder) {
	t.Helper()
	visited := []string{path}
	seen := map[string]bool{path: true}
	current := path
	for {
		rec := get(t, srv, current)
		loc := rec.Header().Get("Location")
		if rec.Code < 300 || rec.Code > 399 {
			if loc != "" {
				t.Fatalf("GET %s = %d with Location %q: a response that does not "+
					"redirect must not name a redirect target", current, rec.Code, loc)
			}
			return visited, rec
		}
		if loc == "" {
			t.Fatalf("GET %s = %d with no Location header", current, rec.Code)
		}
		if seen[loc] {
			t.Fatalf("redirect loop: %s", strings.Join(append(visited, loc), " -> "))
		}
		if len(visited) > maxRedirectHops {
			t.Fatalf("redirect chain exceeded %d hops, which no canonical form "+
				"needs: %s", maxRedirectHops, strings.Join(append(visited, loc), " -> "))
		}
		seen[loc] = true
		visited = append(visited, loc)
		current = loc
	}
}

// assertRendersWithoutRedirect is the fixed-point assertion: the path renders on
// the first request, so the chain has no hops at all.
func assertRendersWithoutRedirect(t *testing.T, srv http.Handler, path string) {
	t.Helper()
	chain, rec := redirectChain(t, srv, path)
	if len(chain) > 1 {
		t.Errorf("canonical path %s redirected: %s; a request already at the "+
			"canonical path must render", path, strings.Join(chain, " -> "))
		return
	}
	if rec.Code != http.StatusOK {
		t.Errorf("GET %s = %d, want 200 (body: %.200s)", path, rec.Code, rec.Body.String())
	}
}

// assertRedirectsOnceToARenderedPage is the no-loop assertion for the forms that
// are supposed to redirect: exactly one hop, and the target renders rather than
// redirecting again.
func assertRedirectsOnceToARenderedPage(t *testing.T, srv http.Handler, path string) {
	t.Helper()
	chain, rec := redirectChain(t, srv, path)
	if len(chain) != 2 {
		t.Errorf("GET %s took %d hops (%s), want exactly one redirect to a "+
			"rendered page", path, len(chain)-1, strings.Join(chain, " -> "))
		return
	}
	if rec.Code != http.StatusOK {
		t.Errorf("redirect target %s = %d, want 200 (chain: %s, body: %.200s)",
			chain[len(chain)-1], rec.Code, strings.Join(chain, " -> "), rec.Body.String())
	}
}

// slashless derives the flat structure's canonical form from the trailing-slash
// constant in archives_test.go, so the two forms of one archive path cannot
// drift apart in the fixtures.
func slashless(path string) string { return strings.TrimSuffix(path, "/") }

// TestArchiveCanonicalPathsDoNotRedirect asserts the fixed-point property for
// every archive kind under a trailing-slash structure (Req 2.4).
func TestArchiveCanonicalPathsDoNotRedirect(t *testing.T) {
	srv := newArchiveServer(t, structDayAndName)
	for _, tc := range []struct {
		name string
		path string
	}{
		// Req 2.4 names this case: a top-level category's canonical path is the
		// flat path, so the flat-to-nested redirect must not fire on it.
		{"top-level category", catNewsCanonical},
		{"category with children", catTechCanonical},
		{"nested category", catGoCanonical},
		{"three-level nested category", catGenericsCanonical},
		{"category with no posts", catIdleCanonical},
		{"tag", tagGolangCanonical},
		{"tag with no posts", tagUnusedCanonical},
		{"author", authorAdminCanonical},
		{"author with no posts", authorGhostCanonical},
		{"year archive", "/2024/"},
		{"year and month archive", "/2024/05/"},
		{"year, month and day archive", "/2024/05/17/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRendersWithoutRedirect(t, srv, tc.path)
		})
	}
}

// TestArchiveRedirectTargetsRender follows each redirecting form's own Location
// and asserts the target renders. This is where a walk that disagreed with
// itself would show up: the flat and wrong-ancestor cases reach their target
// through the Req 2.11 recovery, and the target is then resolved again by the
// walk of Req 2.10 on the second request. The two have to agree.
func TestArchiveRedirectTargetsRender(t *testing.T) {
	srv := newArchiveServer(t, structDayAndName)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"flat path of a nested category", "/category/go/"},
		{"flat path of a three-level nested category", "/category/generics/"},
		{"wrong-ancestor path", "/category/news/go/"},
		{"path skipping a level", "/category/tech/generics/"},
		{"nested category missing the trailing slash", slashless(catGoCanonical)},
		{"three-level nested category missing the trailing slash", slashless(catGenericsCanonical)},
		{"top-level category missing the trailing slash", slashless(catNewsCanonical)},
		{"tag missing the trailing slash", slashless(tagGolangCanonical)},
		{"author missing the trailing slash", slashless(authorAdminCanonical)},
		{"year archive missing the trailing slash", "/2024"},
		{"year and month archive missing the trailing slash", "/2024/05"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRedirectsOnceToARenderedPage(t, srv, tc.path)
		})
	}
}

// TestArchiveNoRedirectLoopFlatStructure repeats both properties with
// permalink_structure empty, where the canonical archive path carries no
// trailing slash (Req 2.7). The slash direction reverses, so a handler that
// hard-coded one form would pass one of these two tests and loop under the
// other. Date archives are absent deliberately: a flat structure registers none
// (Req 6.8).
func TestArchiveNoRedirectLoopFlatStructure(t *testing.T) {
	srv := newArchiveServer(t, "")

	t.Run("canonical paths render", func(t *testing.T) {
		for _, path := range []string{
			slashless(catNewsCanonical),
			slashless(catGoCanonical),
			slashless(catGenericsCanonical),
			slashless(tagGolangCanonical),
			slashless(authorAdminCanonical),
		} {
			t.Run(path, func(t *testing.T) {
				assertRendersWithoutRedirect(t, srv, path)
			})
		}
	})

	t.Run("redirect targets render", func(t *testing.T) {
		for _, path := range []string{
			catNewsCanonical,
			catGoCanonical,
			catGenericsCanonical,
			tagGolangCanonical,
			authorAdminCanonical,
			// The flat-to-nested redirect is a statement about taxonomy shape
			// rather than about permalink structure, so its target must still
			// be a fixed point here.
			"/category/go",
			"/category/generics",
			"/category/news/go",
		} {
			t.Run(path, func(t *testing.T) {
				assertRedirectsOnceToARenderedPage(t, srv, path)
			})
		}
	})
}
