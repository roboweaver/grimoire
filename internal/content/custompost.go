package content

import (
	"context"
	"errors"

	"github.com/roboweaver/grimoire/internal/domain"
)

// typedNodeReader binds a post type to ListPostNodes so the M15 hierarchy
// loader, which reads through domain.PageReader, is reused unchanged for any
// registered post type.
type typedNodeReader struct {
	nodes    domain.PostNodeReader
	typeName string
}

// ListPages satisfies domain.PageReader by reading nodes of the bound type.
func (r typedNodeReader) ListPages(ctx context.Context, afterID int64, limit int) ([]domain.PageNode, error) {
	return r.nodes.ListPostNodes(ctx, r.typeName, afterID, limit)
}

// CustomPostService resolves and lists posts of a registered custom post type.
// It holds no type registry: callers pass the type name (and whether the type
// is hierarchical), and the handler owns the registry entry.
type CustomPostService struct {
	nodes domain.PostNodeReader
	posts domain.PostRepository
}

// NewCustomPostService constructs a CustomPostService. It panics on a nil
// dependency, matching the other services' fail-fast wiring.
func NewCustomPostService(nodes domain.PostNodeReader, posts domain.PostRepository) *CustomPostService {
	if nodes == nil {
		panic("content: NewCustomPostService requires a non-nil PostNodeReader")
	}
	if posts == nil {
		panic("content: NewCustomPostService requires a non-nil PostRepository")
	}
	return &CustomPostService{nodes: nodes, posts: posts}
}

func (s *CustomPostService) load(ctx context.Context, typeName string) (*PageHierarchy, error) {
	return LoadPageHierarchy(ctx, typedNodeReader{nodes: s.nodes, typeName: typeName})
}

// Resolve returns the published post of typeName named by segments, with its
// root-first slug ancestry. Non-hierarchical types take exactly one segment.
// Hierarchical types walk the type's node graph; the leaf is confirmed by ID so
// sibling-slug leaves under different parents each resolve to their own post.
// A failed walk whose final segment names a published node returns a
// *PageMovedError; anything non-published is domain.ErrNotFound.
func (s *CustomPostService) Resolve(ctx context.Context, typeName string, hierarchical bool, segments []string) (domain.Post, []string, error) {
	if !hierarchical {
		if len(segments) != 1 {
			return domain.Post{}, nil, domain.ErrNotFound
		}
		post, err := s.posts.BySlug(ctx, segments[0], typeName)
		if err != nil {
			return domain.Post{}, nil, err
		}
		return post, []string{segments[0]}, nil
	}

	h, err := s.load(ctx, typeName)
	if err != nil {
		return domain.Post{}, nil, err
	}
	leaf, ok := h.ResolvePath(segments)
	if !ok {
		rerr := h.recoverPath(segments)
		var moved *PageMovedError
		if errors.As(rerr, &moved) {
			// Never disclose an unpublished node by redirecting to it.
			if _, err := s.posts.PublishedByID(ctx, moved.Page.ID, typeName); err != nil {
				return domain.Post{}, nil, err
			}
		}
		return domain.Post{}, nil, rerr
	}
	post, err := s.posts.PublishedByID(ctx, leaf.ID, typeName)
	if err != nil {
		return domain.Post{}, nil, err
	}
	return post, h.Ancestry(leaf.ID), nil
}

// AncestryFor returns the root-first slug ancestry of a post of typeName by ID,
// or nil when the ID is not in the type's graph.
func (s *CustomPostService) AncestryFor(ctx context.Context, typeName string, id int64) ([]string, error) {
	h, err := s.load(ctx, typeName)
	if err != nil {
		return nil, err
	}
	return h.Ancestry(id), nil
}

// Archive returns one page of published posts of typeName. Heading is left for
// the handler to set from the registry entry; an empty archive is Total 0.
func (s *CustomPostService) Archive(ctx context.Context, typeName string, page, perPage int) (Archive, error) {
	filter := domain.ArchiveFilter{Types: []string{typeName}}
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.PublishedArchive(ctx, filter, limit, offset)
	if err != nil {
		return Archive{}, err
	}
	total, err := s.posts.CountPublishedArchive(ctx, filter)
	if err != nil {
		return Archive{}, err
	}
	return Archive{Posts: posts, Page: newPage(clampedPage, limit, total)}, nil
}
