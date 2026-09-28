package content

// This file holds the TermService test fake. Its own tests are gone: the two
// methods they covered, Category and CategoryPage, were removed in M9b, and the
// service's remaining reads are asserted in archive_test.go. The fake stays here
// rather than moving, because every TermService test constructs one and
// post_test.go keeps fakePostRepo the same way.
import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
)

type fakeTermRepo struct {
	tax, slug string
	term      domain.Term
	err       error
	called    bool
}

// domain.TermRepository is what TermService holds, and BySlug is now the whole
// of that interface: CountPublishedByTermSlug lost its last content-layer caller
// with CategoryPage (M9b task 7.7) and has since been removed from the port.

func (f *fakeTermRepo) BySlug(ctx context.Context, taxonomy, slug string) (domain.Term, error) {
	f.called = true
	f.tax, f.slug = taxonomy, slug
	return f.term, f.err
}

// TermService.Category and TermService.CategoryPage are both gone. Category had
// no production caller (M9b task 5.7); CategoryPage's only one was web.category,
// which task 7.7 replaces with the four archive handlers, so it went with it.
// Their cases are covered by CategoryArchive and TagArchive in archive_test.go,
// which assert the same resolve-then-list and not-found-skips-query behavior
// against the segment walk that superseded the slug lookup (Req 2.10).
