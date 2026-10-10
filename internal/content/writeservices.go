package content

import (
	"context"
	"errors"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// ErrForbidden is returned by the write services when the acting Principal lacks
// the capability required for the requested operation. It is deliberately
// generic so callers do not leak which capability was missing.
var ErrForbidden = errors.New("content: operation not permitted")

// ErrTitleEmpty is returned by PostWriteService.Create/Update when the caller's
// post_title sanitizes to an empty string (e.g. a markup-only "<em></em>" that
// the title plain-text path reduces to nothing). Both transports require a
// non-empty title, so the service fails closed rather than persisting a
// title-less post (design "Emptied fields"; Req 1.8, 4.2).
var ErrTitleEmpty = errors.New("content: post title empty after sanitization")

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
	w         domain.PostWriter
	revisions revisionSnapshotter
	// policy is the write-boundary content policy applied to the caller's
	// title/content/excerpt before they reach the writer (Req 4). It defaults
	// to sanitize.New() in NewPostWriteService and is NEVER nil: an unwired
	// service sanitizes rather than silently passing input through, so the
	// fail-closed guarantee (Req 1.8) holds even for a call site that forgets
	// WithContentPolicy.
	policy *sanitize.Policy
}

// revisionSnapshotter is the narrow capability PostWriteService.Update needs
// from a *RevisionWriteService: snapshot the pre-edit post as a new revision
// (Req 1.1), enforcing whatever retention policy that service was configured
// with. It is intentionally much narrower than domain.RevisionWriter so
// PostWriteService depends only on the one call it actually makes.
type revisionSnapshotter interface {
	Snapshot(ctx context.Context, cur domain.Post, actorID int64) error
}

// noopRevisionSnapshotter is the default revisionSnapshotter used when a
// PostWriteService is constructed without WithRevisionSnapshotter (every
// M6-era call site that predates M7 and has no interest in revisions). Its
// Snapshot is a deliberate no-op so PostWriteService.Update can call
// s.revisions.Snapshot unconditionally, matching design.md's pseudocode,
// without a nil check and without altering M6 behavior for those callers.
type noopRevisionSnapshotter struct{}

func (noopRevisionSnapshotter) Snapshot(context.Context, domain.Post, int64) error { return nil }

// PostWriteOption configures optional PostWriteService dependencies that most
// callers don't need, keeping NewPostWriteService's required single-argument
// signature backward compatible with every pre-M7 call site.
type PostWriteOption func(*PostWriteService)

// WithRevisionSnapshotter wires a revision-snapshotting dependency (in
// production, a *RevisionWriteService) into PostWriteService.Update so every
// save creates a revision per the configured retention policy (Req 1.1, 5.1).
func WithRevisionSnapshotter(rs revisionSnapshotter) PostWriteOption {
	return func(s *PostWriteService) { s.revisions = rs }
}

// WithContentPolicy wires the write-boundary content policy (in production, the
// process-wide sanitize.New()) into PostWriteService so Create/Update sanitize
// the caller's title/content/excerpt at the actor's tier (Req 4). It follows
// the WithRevisionSnapshotter option style. When omitted, NewPostWriteService's
// sanitize.New() default still applies, so the service never silently skips
// sanitization (fail closed, Req 1.8).
func WithContentPolicy(p *sanitize.Policy) PostWriteOption {
	return func(s *PostWriteService) { s.policy = p }
}

// NewPostWriteService constructs a PostWriteService over a PostWriter. By
// default Update's revision-snapshot hook is a no-op; pass
// WithRevisionSnapshotter to wire in real revisioning (Req 1.1). The content
// policy defaults to sanitize.New() (never nil / fail closed); pass
// WithContentPolicy to share the process-wide policy (Req 4, 1.8).
func NewPostWriteService(w domain.PostWriter, opts ...PostWriteOption) *PostWriteService {
	s := &PostWriteService{w: w, revisions: noopRevisionSnapshotter{}, policy: sanitize.New()}
	for _, opt := range opts {
		opt(s)
	}
	return s
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
	// Req 4.2: sanitize the caller's values at the actor's tier, after the
	// auth gate (never spend the Policy on input an unauthorized caller is not
	// entitled to) and before the writer. Create has no stored record, so all
	// three fields are sanitized unconditionally (sanitizeIncoming against a
	// zero cur: byte-identity only matches when a field is itself empty, which
	// is a no-op). On any policy error or an empty title, fail closed without
	// calling the writer.
	clean, err := s.sanitizeIncoming(actor, p, domain.Post{})
	if err != nil {
		return 0, err
	}
	p.Title = clean.Title
	p.Content = clean.Content
	p.Excerpt = clean.Excerpt
	return s.w.Create(ctx, p)
}

// sanitizeIncoming sanitizes the caller's title/content/excerpt at the actor's
// tier. A field whose caller value is byte-identical to the stored value is
// passed through unsanitized, because it is not a caller value in any meaningful
// sense: REST's partial update merges the stored record as its base
// (rest_posts.go parseRESTPostWrite), so a PATCH of {"title":"x"} arrives here
// with p.Content == cur.Content. Re-sanitizing it would rewrite pre-M10a or
// imported content in place, which Requirement 5.2 forbids ("no write-back of
// sanitized content over stored rows"). Skipping it introduces nothing: the value
// is already in the row. See design.md Finding 3.
//
// The title has an additional check: if it sanitizes to empty, sanitizeIncoming
// returns ErrTitleEmpty (fail closed, Req 1.8/4.2) — content and excerpt may be
// empty. Any error from the policy is returned unchanged, also fail-closed.
func (s *PostWriteService) sanitizeIncoming(actor auth.Principal, p, cur domain.Post) (domain.Post, error) {
	writer := sanitize.For(actor)

	if p.Title != cur.Title {
		cleaned, err := s.policy.Sanitize(sanitize.PostTitle, writer, p.Title)
		if err != nil {
			return domain.Post{}, err
		}
		if cleaned == "" {
			return domain.Post{}, ErrTitleEmpty
		}
		p.Title = cleaned
	}
	if p.Content != cur.Content {
		cleaned, err := s.policy.Sanitize(sanitize.PostContent, writer, p.Content)
		if err != nil {
			return domain.Post{}, err
		}
		p.Content = cleaned
	}
	if p.Excerpt != cur.Excerpt {
		cleaned, err := s.policy.Sanitize(sanitize.PostExcerpt, writer, p.Excerpt)
		if err != nil {
			return domain.Post{}, err
		}
		p.Excerpt = cleaned
	}
	return p, nil
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
	// Snapshot and post update use narrow, independently implemented ports, so
	// they cannot share a transaction here. Snapshotting first favors retaining
	// the pre-edit state; if the later update fails, retention may temporarily
	// include that duplicate snapshot, but no historical state is lost.
	if err := s.revisions.Snapshot(ctx, cur, actor.UserID); err != nil { // NEW (Req 1.1)
		return err // snapshots cur BEFORE any field below mutates it
	}
	// Req 4.3: sanitize the CALLER's values here -- after authorization, after
	// the optimistic-concurrency check, after the revision snapshot, and before
	// the merge below. cur is not touched, so the snapshot still holds the
	// historical stored value (Req 9.7). A field byte-identical to the stored
	// value is passed through unsanitized (Finding 3). On error (including
	// ErrTitleEmpty) return before the merge/write: no unsanitized value reaches
	// cur and no write happens (fail closed, Req 1.8).
	clean, err := s.sanitizeIncoming(actor, p, cur)
	if err != nil {
		return err
	}
	cur.Title = clean.Title
	cur.Content = clean.Content
	cur.Excerpt = clean.Excerpt
	cur.Slug = p.Slug
	cur.CommentStatus = p.CommentStatus
	if !p.Date.IsZero() && !p.Date.Equal(cur.Date) {
		cur.Date = p.Date
		// PostRepo.Update only re-derives date_gmt from Date when DateGMT is
		// zero — but cur was loaded via ByID, which always populates
		// DateGMT from the stored row, so that derivation branch would
		// never fire here otherwise, leaving post_date_gmt permanently
		// stuck at its original value across date changes. Zero it out so
		// the repo actually re-derives the GMT value from the new Date.
		cur.DateGMT = time.Time{}
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
//
// When the writer also implements domain.RevisionWriter (true for every
// production PostWriter), revision cleanup runs before deleting the parent.
// The narrow storage ports do not expose a shared transaction, so this order
// ensures a cleanup failure leaves the parent intact rather than orphaning its
// revision/autosave rows (Req 1.6). A type assertion keeps pre-M7 callers that
// only provide PostWriter unaffected.
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
	if rw, ok := s.w.(domain.RevisionWriter); ok {
		if err := rw.DeleteRevisionsOf(ctx, cur.ID); err != nil {
			return err
		}
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
