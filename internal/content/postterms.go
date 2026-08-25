package content

import (
	"context"
	"errors"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// PostTermsWriteService assigns taxonomy terms to a post. Assigning terms is
// authorized as part of editing the post itself (auth.CanEditPost against the
// target post's current stored record, loaded via PostWriter.ByID) — design.md
// deliberately does not gate this behind the separate manage_categories
// capability that term Create/Update/Delete require, since choosing which of
// the site's existing terms apply to a post is an editing action on the post,
// not a taxonomy-management action.
type PostTermsWriteService struct {
	posts postByID
	w     domain.PostTermsWriter
}

// NewPostTermsWriteService constructs a PostTermsWriteService from a post
// reader (used only to load the authoritative record for authorization) and a
// PostTermsWriter.
func NewPostTermsWriteService(posts postByID, w domain.PostTermsWriter) *PostTermsWriteService {
	return &PostTermsWriteService{posts: posts, w: w}
}

// SetPostTerms authorizes against postID's current stored record and, if
// permitted, replaces its taxonomy term relationships with exactly termIDs. A
// missing post returns the generic ErrForbidden (existence is not leaked),
// matching PostWriteService.Update/Delete's convention.
func (s *PostTermsWriteService) SetPostTerms(ctx context.Context, actor auth.Principal, postID int64, taxonomy string, termIDs []int64) error {
	cur, err := s.posts.ByID(ctx, postID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ErrForbidden
		}
		return err
	}
	if cur.Type == "" {
		cur.Type = "post"
	}
	if !auth.CanEditPost(actor, cur.Type, cur.Status, cur.Author) {
		return ErrForbidden
	}
	return s.w.SetPostTerms(ctx, postID, taxonomy, termIDs)
}
