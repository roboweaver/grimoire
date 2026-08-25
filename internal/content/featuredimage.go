package content

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
)

// FeaturedImageService resolves a post's WordPress featured image (the
// "_thumbnail_id" postmeta, set via wp-admin's Featured Image panel) to its
// attachment URL, for display in card and single-post views.
//
// This mirrors the featured-media lookup RESTMapper already performs for the
// wp-json API (internal/content/rest.go); it exists as a separate, minimal
// service so the public theme-rendering path does not need to depend on the
// REST-only wiring.
type FeaturedImageService struct {
	meta  domain.PostMetaRepository
	media domain.MediaRepository
}

// NewFeaturedImageService constructs a FeaturedImageService over the given
// repositories.
func NewFeaturedImageService(meta domain.PostMetaRepository, media domain.MediaRepository) *FeaturedImageService {
	return &FeaturedImageService{meta: meta, media: media}
}

// URL returns the featured image URL for postID, or "" when the post has no
// featured image set, the referenced attachment no longer exists, or any
// error occurs. A missing/misconfigured featured image is absent-as-empty,
// like OptionService.Get, so it never breaks a page render.
func (s *FeaturedImageService) URL(ctx context.Context, postID int64) string {
	if s == nil || s.meta == nil || s.media == nil {
		return ""
	}
	mediaID, err := s.meta.FeaturedMediaID(ctx, postID)
	if err != nil || mediaID == 0 {
		return ""
	}
	m, err := s.media.ByID(ctx, mediaID)
	if err != nil {
		return ""
	}
	return m.URL
}
