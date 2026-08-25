package content

import (
	"context"
	"errors"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// RevisionWriteService owns revision-snapshot creation and pruning, plus the
// admin-facing read/restore operations over those snapshots. Snapshot is
// called internally by PostWriteService.Update -- RevisionWriteService is not
// exposed as its own public "write a post" surface; callers never construct a
// revision directly. List/Get/Restore are the admin API's entry points for
// revision history (Req 2).
type RevisionWriteService struct {
	revisions  domain.RevisionWriter
	posts      domain.PostWriter
	maxPerPost int // Requirement 5.1; 0 disables, negative/unset = unlimited
}

// NewRevisionWriteService constructs a RevisionWriteService. maxPerPost is
// the retention policy (Req 5): 0 disables revisioning entirely, a positive
// value caps the number of retained (non-autosave) revisions per post, and a
// negative/unset value means unlimited retention.
func NewRevisionWriteService(revisions domain.RevisionWriter, posts domain.PostWriter, maxPerPost int) *RevisionWriteService {
	return &RevisionWriteService{revisions: revisions, posts: posts, maxPerPost: maxPerPost}
}

// Snapshot records cur (the post's state immediately before an update is
// applied) as a new, non-autosave revision attributed to actorID, then
// enforces the configured retention policy (Req 1.1, 5.1-5.3).
//
// maxPerPost == 0 disables revisioning entirely: Snapshot does nothing, not
// even a CreateRevision call (Req 5.3). A positive maxPerPost creates the
// revision and then prunes down to that count via PruneRevisions, which is
// already a no-op when the post is under the limit (task 1.6/1.9), so no
// separate count check is needed here. A negative/unset maxPerPost creates
// the revision and never prunes (unlimited retention).
func (s *RevisionWriteService) Snapshot(ctx context.Context, cur domain.Post, actorID int64) error {
	if s.maxPerPost == 0 {
		return nil
	}
	if _, err := s.revisions.CreateRevision(ctx, cur.ID, actorID, cur, false); err != nil {
		return err
	}
	if s.maxPerPost > 0 {
		return s.revisions.PruneRevisions(ctx, cur.ID, s.maxPerPost)
	}
	return nil
}

// authorizeParent loads the parent post by parentID and verifies actor may
// edit it, per the same capability List/Get/Restore all require. A missing
// parent and an unauthorized actor both return domain.ErrNotFound -- never a
// distinct forbidden error -- so internal/web maps both to 404 uniformly and
// never leaks a post's existence to a caller who can't edit it (Req 2.5).
func (s *RevisionWriteService) authorizeParent(ctx context.Context, actor auth.Principal, parentID int64) (domain.Post, error) {
	parent, err := s.posts.ByID(ctx, parentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Post{}, domain.ErrNotFound
		}
		return domain.Post{}, err
	}
	if !auth.CanEditPost(actor, parent.Type, parent.Status, parent.Author) {
		return domain.Post{}, domain.ErrNotFound
	}
	return parent, nil
}

// revisionOf loads the revision by id and verifies it actually belongs to
// parentID, returning domain.ErrNotFound (indistinguishable from "no such
// revision") when it belongs to a different post (Req 2.5).
func (s *RevisionWriteService) revisionOf(ctx context.Context, parentID, revisionID int64) (domain.Post, error) {
	rev, err := s.revisions.RevisionByID(ctx, revisionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Post{}, domain.ErrNotFound
		}
		return domain.Post{}, err
	}
	if rev.ParentID != parentID {
		return domain.Post{}, domain.ErrNotFound
	}
	return rev, nil
}

// List returns every non-autosave revision of parentID, newest first, once
// actor is confirmed able to edit the parent post (Req 2.1, 2.5).
func (s *RevisionWriteService) List(ctx context.Context, actor auth.Principal, parentID int64) ([]domain.RevisionMeta, error) {
	if _, err := s.authorizeParent(ctx, actor, parentID); err != nil {
		return nil, err
	}
	return s.revisions.ListRevisions(ctx, parentID)
}

// Get returns the full content/title/excerpt of a single revision belonging
// to parentID, once actor is confirmed able to edit the parent post (Req 2.2,
// 2.5).
func (s *RevisionWriteService) Get(ctx context.Context, actor auth.Principal, parentID, revisionID int64) (domain.Post, error) {
	if _, err := s.authorizeParent(ctx, actor, parentID); err != nil {
		return domain.Post{}, err
	}
	return s.revisionOf(ctx, parentID, revisionID)
}

// Restore applies a past revision's title/content/excerpt back onto its
// parent post. It first snapshots the post's CURRENT (pre-restore) state as a
// new revision via Snapshot -- reusing that method's own retention/pruning
// logic rather than duplicating it -- so the state being replaced is never
// lost, then persists the named revision's fields (Req 2.3, 2.5).
func (s *RevisionWriteService) Restore(ctx context.Context, actor auth.Principal, parentID, revisionID int64) (domain.Post, error) {
	cur, err := s.authorizeParent(ctx, actor, parentID)
	if err != nil {
		return domain.Post{}, err
	}
	rev, err := s.revisionOf(ctx, parentID, revisionID)
	if err != nil {
		return domain.Post{}, err
	}
	if err := s.Snapshot(ctx, cur, actor.UserID); err != nil {
		return domain.Post{}, err
	}
	cur.Title = rev.Title
	cur.Content = rev.Content
	cur.Excerpt = rev.Excerpt
	if err := s.posts.Update(ctx, cur); err != nil {
		return domain.Post{}, err
	}
	return cur, nil
}
