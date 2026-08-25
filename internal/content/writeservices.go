package content

import (
	"context"
	"errors"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// ErrForbidden is returned by the write services when the acting Principal lacks
// the capability required for the requested operation. It is deliberately
// generic so callers do not leak which capability was missing.
var ErrForbidden = errors.New("content: operation not permitted")

// ConflictError is returned by PostWriteService.Update when the caller's
// expectedModified argument does not match the post's current stored Modified
// timestamp — i.e. someone else wrote the post since the caller last read it.
// It carries the current value so callers (the admin/REST handlers) can
// surface it for a reconcile-and-retry flow, which the plain ErrForbidden
// sentinel cannot do.
type ConflictError struct {
	CurrentModified time.Time
}

// Error implements the error interface for ConflictError.
func (e *ConflictError) Error() string {
	return "content: post modified since last read"
}

// PostWriteService performs capability-checked create/update/delete of posts and
// pages. The acting Principal is passed per call; the service enforces the
// WordPress-style ownership and status capabilities before touching the writer.
type PostWriteService struct {
	w domain.PostWriter
}

// NewPostWriteService constructs a PostWriteService over a PostWriter.
func NewPostWriteService(w domain.PostWriter) *PostWriteService {
	return &PostWriteService{w: w}
}

// Create authorizes and inserts a new post. When p.Author is zero it defaults to
// the actor; when p.Type is empty it defaults to "post"; when p.Date is zero it
// defaults to the current time (matching WordPress's own new-post behavior),
// so a caller that omits date never gets an unsorted, epoch-dated post out of
// the writer. Returns ErrForbidden (and does not call the writer) if the actor
// may not create the post.
func (s *PostWriteService) Create(ctx context.Context, actor auth.Principal, p domain.Post) (int64, error) {
	if p.Author == 0 {
		p.Author = actor.UserID
	}
	if p.Type == "" {
		p.Type = "post"
	}
	if p.Date.IsZero() {
		p.Date = time.Now()
	}
	if !auth.CanCreatePost(actor, p.Type, p.Status, p.Author) {
		return 0, ErrForbidden
	}
	return s.w.Create(ctx, p)
}

// Update authorizes and replaces an existing post. It loads the authoritative
// stored record by ID and evaluates the edit capability against THAT record's
// Type, Status, and Author — never the caller-supplied struct — so a forged
// Author/Status cannot escalate into editing another user's post. The caller's
// mutable content fields (title, content, excerpt, slug, and a non-zero date)
// are then applied to the stored record; identity and ownership (ID, Author,
// Type) always come from the store. A status change is authorized against the
// resulting state, so publishing still requires the publish capability. A
// missing record returns the generic ErrForbidden (existence is not leaked).
//
// expectedModified, when non-zero, is compared against the stored record's
// Modified timestamp AFTER authorization but BEFORE the field merge/write —
// an unauthorized caller must never learn the current Modified value via a
// 409, only ever a 403 (design.md's "Sequence — update with optimistic-
// concurrency conflict"). A zero expectedModified skips the check entirely;
// this is the escape hatch REST callers use when the request omitted
// If-Unmodified-Since (Req 6.5), while the admin API always supplies a
// non-zero value (Req 3.1 makes modified a required field).
func (s *PostWriteService) Update(ctx context.Context, actor auth.Principal, p domain.Post, expectedModified time.Time) error {
	cur, err := s.w.ByID(ctx, p.ID)
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
	if !expectedModified.IsZero() && !cur.Modified.Equal(expectedModified) {
		return &ConflictError{CurrentModified: cur.Modified}
	}
	cur.Title = p.Title
	cur.Content = p.Content
	cur.Excerpt = p.Excerpt
	cur.Slug = p.Slug
	cur.CommentStatus = p.CommentStatus
	if !p.Date.IsZero() {
		cur.Date = p.Date
	}
	if p.Status != "" && p.Status != cur.Status {
		if !auth.CanEditPost(actor, cur.Type, p.Status, cur.Author) {
			return ErrForbidden
		}
		cur.Status = p.Status
	}
	return s.w.Update(ctx, cur)
}

// Delete authorizes and removes a post. It loads the authoritative stored record
// by ID and evaluates the delete capability against THAT record's Type, Status,
// and Author, so a forged struct cannot delete another user's post. A missing
// record returns the generic ErrForbidden (existence is not leaked).
func (s *PostWriteService) Delete(ctx context.Context, actor auth.Principal, p domain.Post) error {
	cur, err := s.w.ByID(ctx, p.ID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ErrForbidden
		}
		return err
	}
	if cur.Type == "" {
		cur.Type = "post"
	}
	if !auth.CanDeletePost(actor, cur.Type, cur.Status, cur.Author) {
		return ErrForbidden
	}
	return s.w.Delete(ctx, cur.ID)
}

// TermWriteService performs capability-checked create/update/delete of taxonomy
// terms, plus read-only listing pass-throughs. w satisfies both domain.TermWriter
// (for the authorized write methods) and domain.TermReader (for the read
// pass-throughs); the two ports are combined here rather than held separately
// since every concrete TermRepo implements both.
type TermWriteService struct {
	w interface {
		domain.TermWriter
		domain.TermReader
	}
}

// NewTermWriteService constructs a TermWriteService over a combined
// TermWriter+TermReader.
func NewTermWriteService(w interface {
	domain.TermWriter
	domain.TermReader
}) *TermWriteService {
	return &TermWriteService{w: w}
}

// Create authorizes (manage_categories) and inserts a term.
func (s *TermWriteService) Create(ctx context.Context, actor auth.Principal, t domain.Term) (int64, error) {
	if !auth.CanManageTerms(actor) {
		return 0, ErrForbidden
	}
	return s.w.Create(ctx, t)
}

// Update authorizes (manage_categories) and renames a term.
func (s *TermWriteService) Update(ctx context.Context, actor auth.Principal, t domain.Term) error {
	if !auth.CanManageTerms(actor) {
		return ErrForbidden
	}
	return s.w.Update(ctx, t)
}

// Delete authorizes (manage_categories) and removes a term by ID.
func (s *TermWriteService) Delete(ctx context.Context, actor auth.Principal, id int64) error {
	if !auth.CanManageTerms(actor) {
		return ErrForbidden
	}
	return s.w.Delete(ctx, id)
}

// ListByTaxonomy is a thin, unauthorized read pass-through to the underlying
// TermReader: listing terms for a taxonomy (e.g. to populate an editor's term
// picker) only requires the edit_posts capability already enforced by the web
// layer's route middleware for every admin-API caller of this method — not
// manage_categories, since it performs no write. It takes no actor parameter,
// matching AdminService's established read-only-service convention elsewhere
// in this package.
func (s *TermWriteService) ListByTaxonomy(ctx context.Context, taxonomy string) ([]domain.Term, error) {
	return s.w.ListByTaxonomy(ctx, taxonomy)
}

// TermsByIDs is a thin, unauthorized read pass-through to the underlying
// TermReader, for the same reason as ListByTaxonomy.
func (s *TermWriteService) TermsByIDs(ctx context.Context, ids []int64) ([]domain.Term, error) {
	return s.w.TermsByIDs(ctx, ids)
}

// OptionWriteService performs capability-checked writes of site options.
type OptionWriteService struct {
	w domain.OptionWriter
}

// NewOptionWriteService constructs an OptionWriteService over an OptionWriter.
func NewOptionWriteService(w domain.OptionWriter) *OptionWriteService {
	return &OptionWriteService{w: w}
}

// Set authorizes (manage_options) and upserts an option.
func (s *OptionWriteService) Set(ctx context.Context, actor auth.Principal, name, value string) error {
	if !auth.CanManageOptions(actor) {
		return ErrForbidden
	}
	return s.w.Set(ctx, name, value)
}

// Delete authorizes (manage_options) and removes an option.
func (s *OptionWriteService) Delete(ctx context.Context, actor auth.Principal, name string) error {
	if !auth.CanManageOptions(actor) {
		return ErrForbidden
	}
	return s.w.Delete(ctx, name)
}
