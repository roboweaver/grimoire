package content

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
)

// PostService orchestrates post/page reads for the web layer.
type PostService struct {
	posts domain.PostRepository
}

// NewPostService constructs a PostService over a PostRepository.
func NewPostService(p domain.PostRepository) *PostService {
	return &PostService{posts: p}
}

// Recent returns published posts for a 1-based page, clamping the page size to
// [1, MaxPerPage] with DefaultPerPage when unset.
func (s *PostService) Recent(ctx context.Context, page, perPage int) ([]domain.Post, error) {
	limit, offset, _ := clamp(page, perPage)
	return s.posts.RecentPosts(ctx, limit, offset)
}

// BySlug resolves a single published post or page by slug. domain.ErrNotFound
// is propagated for unknown or non-published slugs.
func (s *PostService) BySlug(ctx context.Context, slug string) (domain.Post, error) {
	return s.posts.BySlug(ctx, slug, "post", "page")
}
