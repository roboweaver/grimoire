package sanitize

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This is the import-set arm of property P6 (design "Mechanism per property",
// P6 arm (c); Req 1.2, 2.8, 9.9). The other two P6 arms -- sequential-output
// determinism and the concurrent -race run -- live in properties_test.go; this
// file enforces "no clock read / no network / no filesystem" structurally,
// rather than hoping it stays true, by reading the package's OWN import list
// off disk and asserting it.
//
// Mechanism (design "Purity ... a test enforces the import set so a future
// convenience import has to be argued for"): parse every NON-TEST .go file in
// this package directory with go/parser, collect the import paths the package
// itself declares, and assert that set is
//
//   - disjoint from a deny-list of impure packages (net, os, time,
//     database/sql, and the internal content/web/render/storage packages plus
//     the bun database driver), and
//   - a subset of a small allow-list, so adding ANY new import to the package
//     forces a deliberate edit to this allow-list -- "a future convenience
//     import has to be argued for."
//
// Only non-test files are parsed: the import constraint is on PRODUCTION code.
// Test files legitimately import testing, testing/quick, math/rand, reflect,
// go/parser, os, etc. (this file imports os and go/parser), and sweeping those
// in would make the assertion meaningless. net/url, reached transitively
// through bluemonday, is NOT a direct import of this package and so never
// appears in the set this test examines -- the deny-list names bare "net", and
// the package's own imports exclude it (design "No network or filesystem
// access": net/url parses, it does not dial).

// packageOwnImports parses the non-test .go source files in dir and returns the
// sorted, de-duplicated set of import paths the files declare. It walks the
// files with go/parser (ImportsOnly keeps it to the import clause) rather than
// shelling out to `go list`, so the test has no build-environment dependency
// and stays as pure as the package it guards.
func packageOwnImports(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir %q: %v", dir, err)
	}

	fset := token.NewFileSet()
	seen := map[string]struct{}{}
	parsedProduction := false

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Production .go files only: skip test files (import constraints are on
		// production code; test files legitimately import testing, os, etc.).
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %q: %v", path, err)
		}
		parsedProduction = true

		for _, spec := range f.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %q in %q: %v", spec.Path.Value, path, err)
			}
			seen[p] = struct{}{}
		}
	}

	// Guard against a silently-passing test: if the directory layout ever
	// changes so no production file is parsed, the empty import set would
	// vacuously satisfy both assertions. Fail loudly instead.
	if !parsedProduction {
		t.Fatalf("no non-test .go files found in %q; the import-set test parsed nothing", dir)
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestImportSetPurity asserts the package's own production imports are free of
// anything that would make it impure -- no clock, no network, no filesystem, no
// database -- and are confined to a small allow-list so a new import cannot slip
// in unargued (Req 1.2, 2.8; P6 arm (c)).
func TestImportSetPurity(t *testing.T) {
	imports := packageOwnImports(t, ".")

	// Deny-list: packages whose presence would, on its face, break purity.
	// The design names net, os, time and database/sql explicitly (P6 arm (c));
	// the remaining entries pin the package boundaries design "Where the Policy
	// lives" draws -- sanitize imports internal/auth and nothing else from the
	// tree, and never a storage/bun database driver.
	denied := map[string]string{
		"net":                            "network access (no net.Dial in a pure policy)",
		"net/http":                       "HTTP client/server (purity: no network)",
		"os":                             "filesystem/process access (purity: no filesystem)",
		"time":                           "clock read (purity: deterministic, no clock)",
		"database/sql":                   "database access (Policy operates on strings in memory, Req 1.3)",
		"path/filepath":                  "filesystem path handling (purity: no filesystem)",
		"io/ioutil":                      "filesystem access (purity: no filesystem)",
		"os/exec":                        "process execution (purity)",
		"math/rand":                      "nondeterminism (purity: deterministic output, Req 1.9)",
		"github.com/uptrace/bun":         "database driver (Req 1.3)",
		"github.com/go-sql-driver/mysql": "database driver (Req 1.3)",
		"github.com/roboweaver/grimoire/internal/content": "content package (boundary: content imports sanitize, not the reverse)",
		"github.com/roboweaver/grimoire/internal/web":     "web/transport package (boundary: web imports sanitize)",
		"github.com/roboweaver/grimoire/internal/render":  "render package (boundary)",
		"github.com/roboweaver/grimoire/internal/storage": "storage package (Req 1.3)",
	}

	// Allow-list: the complete, intended production import set. Everything the
	// package legitimately depends on -- internal/auth (for Principal),
	// bluemonday (the policies), and the pure standard-library packages the
	// title path uses (errors, html, strings). golang.org/x/net/html is used by
	// the P2 closure TEST, not by production code, so it is intentionally absent
	// here. Adding a production import means adding it to this list on purpose.
	allowed := map[string]struct{}{
		"errors":                             {},
		"html":                               {},
		"strings":                            {},
		"github.com/microcosm-cc/bluemonday": {},
		"github.com/roboweaver/grimoire/internal/auth": {},
	}

	for _, imp := range imports {
		if why, bad := denied[imp]; bad {
			t.Errorf("internal/sanitize must not import %q: %s", imp, why)
		}
		if _, ok := allowed[imp]; !ok {
			t.Errorf("internal/sanitize imports %q, which is not on the purity allow-list; "+
				"if this import is intentional and pure (no clock/network/filesystem/db), add it to the allow-list in importset_test.go with a one-line justification", imp)
		}
	}

	// Also assert the two load-bearing members are actually present, so a
	// refactor that drops internal/auth or bluemonday (and would change tier
	// selection or the compiled policies) is caught here too.
	for _, must := range []string{
		"github.com/microcosm-cc/bluemonday",
		"github.com/roboweaver/grimoire/internal/auth",
	} {
		found := false
		for _, imp := range imports {
			if imp == must {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("internal/sanitize no longer imports %q; expected it in the production import set", must)
		}
	}
}
