package content

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
)

// PostService orchestrates post/page reads for the web layer.
type PostService struct {
	posts domain.PostRepository
	pc    domain.PostCounter // optional; set via WithCounter for RecentPage
}

// NewPostService constructs a PostService over a PostRepository. Unchanged
// signature -- all 20 existing call sites are untouched.
func NewPostService(p domain.PostRepository) *PostService {
	return &PostService{posts: p}
}

// WithCounter opts a PostService into RecentPage support by attaching a
// PostCounter. Returns the receiver so it can be chained at construction time
// (e.g. content.NewPostService(repos.Posts).WithCounter(repos.PostCounter)).
func (s *PostService) WithCounter(pc domain.PostCounter) *PostService {
	s.pc = pc
	return s
}

// Recent returns published posts for a 1-based page, clamping the page size to
// [1, MaxPerPage] with DefaultPerPage when unset. Unchanged behavior.
func (s *PostService) Recent(ctx context.Context, page, perPage int) ([]domain.Post, error) {
	limit, offset, _ := clamp(page, perPage)
	return s.posts.RecentPosts(ctx, limit, offset)
}

// RecentPage is Recent plus a Page pagination contract (Req 8.1), for callers
// that need total/out-of-range information (the public home page). Requires
// WithCounter to have been called; panics on a nil pc, matching Go's normal
// nil-pointer-dereference behavior for an unwired dependency rather than
// silently returning a zero Page.
func (s *PostService) RecentPage(ctx context.Context, page, perPage int) ([]domain.Post, Page, error) {
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.RecentPosts(ctx, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	total, err := s.pc.CountByStatus(ctx, "post", "publish")
	if err != nil {
		return nil, Page{}, err
	}
	return posts, newPage(clampedPage, limit, total), nil
}

// BySlug resolves a single published post or page by slug. domain.ErrNotFound
// is propagated for unknown or non-published slugs.
func (s *PostService) BySlug(ctx context.Context, slug string) (domain.Post, error) {
	return s.posts.BySlug(ctx, slug, "post", "page")
}
