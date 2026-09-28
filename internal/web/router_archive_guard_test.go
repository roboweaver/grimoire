package web_test

import (
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/web"
)

// This file asserts the startup guard Routes() applies before it registers the
// content patterns: a Server about to register archive routes it cannot serve
// must refuse to start, and one that can serve them must not be refused.
//
// Both halves matter. The first is the guard's reason for existing -- without it
// the missing dependency surfaces as a 500 from /category/* on some later
// request, attributed to the server rather than to the wiring. The second is
// what keeps the guard from being a liability: a check that over-fires turns
// every correctly wired embedder into a startup crash, which is a worse failure
// than the one it replaces.
//
// The services are built over nil repositories deliberately. Routes() only
// registers handlers -- it never issues a query -- so the repositories are never
// dereferenced, and the guard reads exactly two facts: whether WithHierarchy
// supplied a taxonomy reader and whether WithAuthors supplied a user reader.
// Feeding it a real database would exercise storage, not the predicate.

// stubTermReader satisfies domain.TermReader by embedding it. The embedded
// interface is nil, so any call panics; nothing here calls one, and that is the
// point -- the guard must be satisfied by the presence of a reader, not by a
// working one.
type stubTermReader struct{ domain.TermReader }

// stubUserRepository is stubTermReader's twin for the author archive's
// dependency.
type stubUserRepository struct{ domain.UserRepository }

// guardStructures is a flat structure and a non-flat one. The flat entry is
// included because it is the case an assumption gets wrong: a flat structure
// registers no date archives (Req 6.8) but still registers the category, tag and
// author patterns -- /category/{slug} has shipped since M1 -- so the guard fires
// for it exactly as it does for a structure with a permalink pattern. The
// subtests below assert ArchivePatterns is non-empty for both rather than taking
// that on trust, because if it were ever empty the guard would return early and
// every panic expectation here would become vacuous.
var guardStructures = []struct {
	name      string
	structure string
}{
	{"flat", ""},
	{"postname", structPostName},
}

// newGuardServer builds the smallest Server whose Routes() reaches the guard,
// with each archive dependency independently present or absent.
func newGuardServer(t *testing.T, structure string, hierarchy, authors bool) *web.Server {
	t.Helper()
	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", structure, err)
	}
	if len(st.ArchivePatterns()) == 0 {
		t.Fatalf("routing.Parse(%q).ArchivePatterns() is empty; the guard cannot fire "+
			"and every expectation in this file would pass vacuously", structure)
	}
	posts := content.NewPostService(nil)
	if authors {
		posts = posts.WithAuthors(stubUserRepository{})
	}
	terms := content.NewTermService(nil, nil)
	if hierarchy {
		terms = terms.WithHierarchy(stubTermReader{})
	}
	return web.NewServer(posts, terms, nil, nil, nil).WithPermalinks(st)
}

// routesPanic calls Routes() and returns the panic value's message, or "" when
// Routes() returned normally.
func routesPanic(srv *web.Server) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			if s, ok := r.(string); ok {
				msg = s
				return
			}
			if err, ok := r.(error); ok {
				msg = err.Error()
				return
			}
			msg = "non-string panic value"
		}
	}()
	srv.Routes()
	return ""
}

// TestRoutesRequiresArchiveDeps is the guard's specification: each missing
// dependency produces a panic that names the call to add, and a server with both
// starts.
//
// The message substring asserted is the constructor call, not the prose around
// it, because that is the part an operator acts on: a rewording of the
// explanation should not fail this test, but dropping the name of the call to
// make should.
func TestRoutesRequiresArchiveDeps(t *testing.T) {
	for _, gs := range guardStructures {
		t.Run(gs.name, func(t *testing.T) {
			t.Run("no hierarchy panics naming WithHierarchy", func(t *testing.T) {
				msg := routesPanic(newGuardServer(t, gs.structure, false, false))
				if msg == "" {
					t.Fatal("Routes() did not panic with no taxonomy reader wired")
				}
				if !strings.Contains(msg, "WithHierarchy") {
					t.Errorf("panic message does not name WithHierarchy: %q", msg)
				}
			})

			t.Run("hierarchy but no authors panics naming WithAuthors", func(t *testing.T) {
				msg := routesPanic(newGuardServer(t, gs.structure, true, false))
				if msg == "" {
					t.Fatal("Routes() did not panic with no user reader wired")
				}
				if !strings.Contains(msg, "WithAuthors") {
					t.Errorf("panic message does not name WithAuthors: %q", msg)
				}
				// The hierarchy check passed, so this must be the author
				// message and not the category one repeated.
				if strings.Contains(msg, "WithHierarchy") {
					t.Errorf("panic message is the taxonomy one though a reader was wired: %q", msg)
				}
			})

			t.Run("both wired does not panic", func(t *testing.T) {
				if msg := routesPanic(newGuardServer(t, gs.structure, true, true)); msg != "" {
					t.Fatalf("Routes() panicked on a correctly wired server: %q", msg)
				}
			})
		})
	}
}
