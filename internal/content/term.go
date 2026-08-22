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
}

// NewTermService constructs a TermService.
func NewTermService(t domain.TermRepository, p domain.PostRepository) *TermService {
	return &TermService{terms: t, posts: p}
}

// Category resolves a category term by slug and returns it with its published
// posts for the requested page. domain.ErrNotFound is returned (and the post
// repository is not queried) when the term does not exist.
func (s *TermService) Category(ctx context.Context, slug string, page, perPage int) (domain.Term, []domain.Post, error) {
	term, err := s.terms.BySlug(ctx, TaxonomyCategory, slug)
	if err != nil {
		return domain.Term{}, nil, err
	}
	limit, offset := clamp(page, perPage)
	posts, err := s.posts.ByTermSlug(ctx, TaxonomyCategory, slug, limit, offset)
	if err != nil {
		return domain.Term{}, nil, err
	}
	return term, posts, nil
}
