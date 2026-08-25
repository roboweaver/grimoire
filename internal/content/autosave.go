package content

import (
	"context"
	"errors"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// AutosaveFields is the subset of a post's editable content an autosave
// carries (Req 3.1): title, content, and excerpt only -- no status, slug, or
// date, since an autosave never touches the parent post's own row.
type AutosaveFields struct {
	Title   string
	Content string
	Excerpt string
}

// AutosaveService owns the single-row-per-(post,author) upsert semantics
// (Req 3.2) and the "is there a newer autosave than the post itself" read
// used by Requirement 3.4's editor notice.
type AutosaveService struct {
	revisions domain.RevisionWriter
	posts     domain.PostWriter
}

// NewAutosaveService constructs an AutosaveService.
func NewAutosaveService(revisions domain.RevisionWriter, posts domain.PostWriter) *AutosaveService {
	return &AutosaveService{revisions: revisions, posts: posts}
}

// authorizeParent loads the parent post by parentID and verifies actor may
// edit it, mirroring RevisionWriteService's own gate: a missing parent and an
// unauthorized actor both collapse to domain.ErrNotFound (Req 3.5, matching
// Req 2.5's existence-leak rule).
func (s *AutosaveService) authorizeParent(ctx context.Context, actor auth.Principal, parentID int64) (domain.Post, error) {
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

// Save upserts actor's single autosave row for parentID: if one already
// exists it is overwritten in place via UpdateAutosave; otherwise a new
// autosave revision is created via CreateRevision(autosave=true) (Req 3.1,
// 3.2). It never calls posts.Create or posts.Update -- the parent post's own
// row, and the content.ConflictError check that guards it, are never touched
// by an autosave write (Req 3.3).
func (s *AutosaveService) Save(ctx context.Context, actor auth.Principal, parentID int64, fields AutosaveFields) (domain.Post, error) {
	if _, err := s.authorizeParent(ctx, actor, parentID); err != nil {
		return domain.Post{}, err
	}
	existing, found, err := s.revisions.AutosaveFor(ctx, parentID, actor.UserID)
	if err != nil {
		return domain.Post{}, err
	}
	snapshot := domain.Post{
		ID:       parentID,
		Title:    fields.Title,
		Content:  fields.Content,
		Excerpt:  fields.Excerpt,
		ParentID: parentID,
	}
	if found {
		snapshot.ID = existing.ID
		if err := s.revisions.UpdateAutosave(ctx, existing.ID, snapshot); err != nil {
			return domain.Post{}, err
		}
		return snapshot, nil
	}
	id, err := s.revisions.CreateRevision(ctx, parentID, actor.UserID, snapshot, true)
	if err != nil {
		return domain.Post{}, err
	}
	snapshot.ID = id
	return snapshot, nil
}

// Newer returns actor's autosave row for parentID, but only when one exists
// AND its Modified timestamp is strictly after the parent post's own
// Modified (Req 3.4). Otherwise it returns (Post{}, false, nil) -- including
// the "no autosave at all" case -- so the editor's restore prompt can treat
// both uniformly as "nothing to offer".
func (s *AutosaveService) Newer(ctx context.Context, actor auth.Principal, parentID int64) (domain.Post, bool, error) {
	parent, err := s.authorizeParent(ctx, actor, parentID)
	if err != nil {
		return domain.Post{}, false, err
	}
	autosave, found, err := s.revisions.AutosaveFor(ctx, parentID, actor.UserID)
	if err != nil {
		return domain.Post{}, false, err
	}
	if !found || !autosave.Modified.After(parent.Modified) {
		return domain.Post{}, false, nil
	}
	return autosave, true, nil
}
