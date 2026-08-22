package domain

import "context"

// PostRepository reads posts and pages from the backing store. Implementations
// live under internal/storage and must return ErrNotFound when a single record
// is missing.
type PostRepository interface {
	// RecentPosts returns published posts (post_type "post") newest first.
	RecentPosts(ctx context.Context, limit, offset int) ([]Post, error)
	// BySlug returns a single published post/page by its slug (post_name).
	// When types is empty, implementations default to {"post", "page"}.
	BySlug(ctx context.Context, slug string, types ...string) (Post, error)
	// ByTermSlug returns published posts related to a taxonomy term, newest first.
	ByTermSlug(ctx context.Context, taxonomy, termSlug string, limit, offset int) ([]Post, error)
}

// TermRepository resolves taxonomy terms.
type TermRepository interface {
	// BySlug returns the term for a taxonomy/slug pair, or ErrNotFound.
	BySlug(ctx context.Context, taxonomy, slug string) (Term, error)
}

// OptionRepository reads site options.
type OptionRepository interface {
	// Get returns an option value by name, or ErrNotFound.
	Get(ctx context.Context, name string) (string, error)
}
