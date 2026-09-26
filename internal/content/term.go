package content

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
)

// Taxonomy names understood by the content layer in M1.
const TaxonomyCategory = "category"

// TermService resolves taxonomy terms and lists their published posts.
type TermService struct {
	terms domain.TermRepository
	posts domain.PostRepository
	// hier is optional; set via WithHierarchy for CategoryArchive.
	hier domain.TermReader
}

// NewTermService constructs a TermService. Unchanged signature -- every
// existing call site is untouched.
func NewTermService(t domain.TermRepository, p domain.PostRepository) *TermService {
	return &TermService{terms: t, posts: p}
}

// WithHierarchy opts a TermService into CategoryArchive support by attaching
// the taxonomy reader its segment walk, ancestry and descendant sets are
// resolved from. Returns the receiver so it can be chained at construction time
// (e.g. content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader)),
// following PostService.WithCounter rather than changing a constructor
// signature.
func (s *TermService) WithHierarchy(r domain.TermReader) *TermService {
	s.hier = r
	return s
}

// HasHierarchy reports whether WithHierarchy supplied the taxonomy reader
// CategoryArchive needs.
//
// It exists so a router can refuse to start rather than register a category
// archive route that would panic on its first request: CategoryArchive's nil
// dereference is deliberate, but it is only a good failure mode for a caller who
// chose to call it, and /category/* is registered unconditionally from the
// permalink structure. A predicate is the minimum that makes the wiring gap
// checkable from another package -- exporting hier itself, or reaching for it
// through reflection, would turn an implementation detail into API for no gain.
func (s *TermService) HasHierarchy() bool {
	return s.hier != nil
}

// CategoryArchive resolves the category a request's segment path names and
// returns its descendant-inclusive page of published posts.
//
// The path is resolved by walking the segments through the taxonomy graph
// (Req 2.10) rather than by resolving the final segment's slug, so
// TermRepository.BySlug is not on this path at all: the walk reads the graph
// from the one TermReader.ListByTaxonomy call this method already needs for the
// ancestry and the descendant set (Req 1.6), and costs no extra query.
//
// The returned Archive carries the term's name as its Heading, its root-first
// Ancestry — what routing.Structure's category path constructor consumes — and
// the posts of the term *and every term beneath it* (Req 3.1), counted over the
// same set so Page.Total describes the rows actually listed (Req 3.2).
//
// A walk that fails has two outcomes rather than one, and both are reported
// before any post query runs, so neither costs a listing or a count. When the
// path's final segment names a category somewhere in the taxonomy the result is
// a *CategoryMovedError carrying that category's canonical ancestry, which is
// how a published flat, wrong-ancestor or skipped-level URL redirects instead of
// 404ing (Req 2.3, 2.11); when it names nothing, it is domain.ErrNotFound (Req
// 2.5). The recovery reads the graph this method already loaded, so it adds no
// query.
//
// A category that exists but holds no published posts is an empty archive with a
// zero Total, not a not-found (Req 2.6): the term resolved, so nothing is absent.
//
// Requires WithHierarchy to have been called; panics on a nil reader, matching
// PostService.RecentPage's handling of an unwired PostCounter — Go's normal
// nil-dereference behavior for a dependency that was never supplied, rather
// than a zero Archive that looks like an empty category.
func (s *TermService) CategoryArchive(ctx context.Context, segments []string, page, perPage int) (Archive, error) {
	h, err := LoadTermHierarchy(ctx, s.hier, TaxonomyCategory)
	if err != nil {
		return Archive{}, err
	}
	term, ok := h.ResolvePath(segments)
	if !ok {
		return Archive{}, h.recoverPath(segments)
	}
	filter := domain.ArchiveFilter{
		Taxonomy: TaxonomyCategory,
		TermIDs:  h.DescendantIDs(term.ID),
	}
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.PublishedArchive(ctx, filter, limit, offset)
	if err != nil {
		return Archive{}, err
	}
	total, err := s.posts.CountPublishedArchive(ctx, filter)
	if err != nil {
		return Archive{}, err
	}
	return Archive{
		Heading:  term.Name,
		Term:     term,
		Ancestry: h.Ancestry(term.ID),
		Posts:    posts,
		Page:     newPage(clampedPage, limit, total),
	}, nil
}

// TagArchive resolves a post_tag term by slug and returns its page of
// published posts.
//
// Tags are flat (Req 5.2): post_tag is non-hierarchical in WordPress, so there
// is no segment path to walk and no ancestry to report. That makes this the one
// archive whose resolution is a single-slug read — TermRepository.BySlug, whose
// deterministic lowest-term_id winner is what makes a duplicate slug pick the
// same tag on all three vendors (Req 5.6) — and it is why the taxonomy graph is
// never loaded here even when WithHierarchy supplied a reader:
// term_taxonomy.parent is schema-legal on a post_tag row and an imported
// database may carry a non-zero one, but it means nothing for this taxonomy, in
// neither direction. So Ancestry stays nil and ArchiveFilter.TermIDs is the
// single resolved tag rather than a descendant set.
//
// domain.ErrNotFound is returned, before any post query, when the slug names no
// post_tag term (Req 5.3). A tag that exists but holds no published posts is an
// empty archive with a zero Total, not a not-found (Req 5.4): the term
// resolved, so nothing is absent.
func (s *TermService) TagArchive(ctx context.Context, slug string, page, perPage int) (Archive, error) {
	term, err := s.terms.BySlug(ctx, taxonomyPostTag, slug)
	if err != nil {
		return Archive{}, err
	}
	filter := domain.ArchiveFilter{
		Taxonomy: taxonomyPostTag,
		TermIDs:  []int64{term.ID},
	}
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.PublishedArchive(ctx, filter, limit, offset)
	if err != nil {
		return Archive{}, err
	}
	total, err := s.posts.CountPublishedArchive(ctx, filter)
	if err != nil {
		return Archive{}, err
	}
	return Archive{
		Heading: term.Name,
		Term:    term,
		Posts:   posts,
		Page:    newPage(clampedPage, limit, total),
	}, nil
}
