package wprepo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// TestBunFormatsQuestionMarkPlaceholdersItself pins the Bun behavior that the
// raw SQL in this package depends on, and that once broke every PostgreSQL
// write path here.
//
// Bun does not hand arguments to the driver. Its ExecContext/QueryContext/
// QueryRowContext substitute them into the SQL text first and then call the
// driver with no arguments at all. Two consequences follow, and this test holds
// Bun to both:
//
//   - A `?` placeholder is rewritten to the dialect's own form with the argument
//     inlined. That is why the raw queries in this package keep their `?`.
//   - A query containing no `?` is returned verbatim and its arguments are
//     DISCARDED, because Bun's formatter short-circuits on
//     `strings.IndexByte(query, '?') == -1`. Pre-rebinding `?` to `$N` therefore
//     strips the arguments and the server answers `there is no parameter $1`.
//
// If a future Bun release starts passing arguments through to the driver, this
// test fails and the comments on execQuerier and in the rebind package need
// revisiting -- that is the point of pinning it.
func TestBunFormatsQuestionMarkPlaceholdersItself(t *testing.T) {
	gen := bun.NewDB(nil, pgdialect.New()).QueryGen()

	const query = "INSERT INTO t (a, b) VALUES (?, ?)"

	// The form this package uses: `?` survives to Bun, which inlines the args.
	got := gen.FormatQuery(query, 7, "x")
	if strings.Contains(got, "?") {
		t.Errorf("Bun left a ? unsubstituted: %q", got)
	}
	for _, want := range []string{"7", "'x'"} {
		if !strings.Contains(got, want) {
			t.Errorf("Bun dropped argument %s from %q", want, got)
		}
	}

	// The bug: rebinding first leaves no `?`, so Bun returns the text unchanged
	// and silently drops both arguments.
	rebound := rebind.Rebind("postgres", query)
	if !strings.Contains(rebound, "$1") {
		t.Fatalf("precondition: rebind did not produce $N placeholders: %q", rebound)
	}
	reboundGot := gen.FormatQuery(rebound, 7, "x")
	if reboundGot != rebound {
		t.Fatalf("expected Bun to return a ?-free query verbatim, got %q", reboundGot)
	}
	if strings.Contains(reboundGot, "7") || strings.Contains(reboundGot, "'x'") {
		t.Errorf("expected arguments to be dropped for a ?-free query, got %q", reboundGot)
	}
}

// TestPackageDoesNotRebindPlaceholders guards the rule the test above explains:
// everything in this package executes through Bun, so nothing here may call
// rebind.Rebind.
//
// This is a source-level check rather than a behavioral one because the bug it
// prevents is invisible on SQLite and MySQL, where rebind.Rebind is a no-op. It
// only shows up on PostgreSQL, and a misuse reintroduced here would otherwise
// sit undetected until someone ran the Postgres contract suite.
func TestPackageDoesNotRebindPlaceholders(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parsing package: %v", err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if name == "placeholders_test.go" {
				continue // this file references it deliberately
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Rebind" {
					return true
				}
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "rebind" {
					t.Errorf("%s calls rebind.Rebind; queries in this package go "+
						"through Bun and must keep their ? placeholders (see "+
						"TestBunFormatsQuestionMarkPlaceholdersItself)",
						fset.Position(sel.Pos()))
				}
				return true
			})
		}
	}
}
