package content

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// ErrForbidden is returned by the write services when the acting Principal lacks
// the capability required for the requested operation. It is deliberately
// generic so callers do not leak which capability was missing.
var ErrForbidden = errors.New("content: operation not permitted")

// ErrTermParentCycle is returned by TermWriteService.Create/Update when the
// requested parent would make the term a descendant of itself -- the parent is
// the term's own id (Req 3.1) or a member of the term's descendant set
// (Req 3.2). Nothing is written. Handlers map it to 400.
var ErrTermParentCycle = errors.New("content: term parent would create a cycle")

// ErrTermParentInvalid is returned when the requested non-zero parent does not
// resolve to an existing term in the term's own taxonomy (Req 4.1, 4.2) or when
// a non-zero parent targets a flat (non-hierarchical) taxonomy such as post_tag
// (Req 4.3). Nothing is written. Handlers map it to 400.
var ErrTermParentInvalid = errors.New("content: term parent is invalid")

// ErrTermSlugInvalid is returned when a registered-taxonomy term slug does not
// match ^[a-z0-9][a-z0-9_-]*$. Nothing is written. Handlers map it to 400.
var ErrTermSlugInvalid = errors.New("content: term slug is invalid")

// ErrTermSlugTaken is returned when a registered-taxonomy term slug duplicates
// that of a sibling (same parent in a hierarchical taxonomy, any term in a flat
// one). Nothing is written. Handlers map it to 400 term_exists.
var ErrTermSlugTaken = errors.New("content: term slug already used among siblings")

// ErrTermNameEmpty is returned when a registered-taxonomy term name sanitizes
// to an empty string. Nothing is written.
var ErrTermNameEmpty = errors.New("content: term name empty after sanitization")

// ErrTaxonomyUnknown is returned by TermWriteService.Create for a taxonomy that
// is neither category, post_tag nor registered. Nothing is written.
var ErrTaxonomyUnknown = errors.New("content: unknown taxonomy")

// termSlugRE is the allowed slug shape for registered-taxonomy terms.
var termSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

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

	// reg, nodes and detacher are the M17 registered-type collaborators. All
	// are optional; with reg nil no type is "registered" and every code path
	// below behaves exactly as before M17 (built-in types never enter them).
	reg      *registry.Registry
	nodes    domain.PostNodeReader
	detacher domain.PostTermsWriter
	// purger removes a deleted post's dependents (comments, meta) and
	// re-points its children. Optional; nil skips the purge step.
	purger domain.PostDependentsPurger
}

// ErrSlugInvalid is returned when a registered-type post slug does not match
// ^[a-z0-9][a-z0-9_-]*$ or when no slug can be derived from the title. Nothing
// is written. Handlers map it to 400.
var ErrSlugInvalid = errors.New("content: post slug is invalid")

// ErrPostParentInvalid is returned when a registered-type post names a parent
// that is not allowed: any non-zero parent on a flat type, or a parent that is
// absent or of another type on a hierarchical one. Nothing is written.
var ErrPostParentInvalid = errors.New("content: post parent is invalid")

// ErrPostParentCycle is returned when the requested parent is the post itself
// or one of its descendants. Nothing is written.
var ErrPostParentCycle = errors.New("content: post parent would create a cycle")

// errPostNodesUnavailable reports a parent validation that cannot run because
// no PostNodeReader was wired. It fails the write rather than skipping the
// check.
var errPostNodesUnavailable = errors.New("content: post parent cannot be validated without a node reader")

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

// WithPostRegistry enables writes to registered custom post types. A nil
// registry leaves built-in behavior unchanged.
func WithPostRegistry(r *registry.Registry) PostWriteOption {
	return func(s *PostWriteService) { s.reg = r }
}

// WithPostNodeReader wires the post-node read used to detect slug collisions
// among registered-type posts.
func WithPostNodeReader(n domain.PostNodeReader) PostWriteOption {
	return func(s *PostWriteService) { s.nodes = n }
}

// WithTermDetacher wires the writer used to detach a registered-type post's
// terms before the row is deleted.
func WithTermDetacher(w domain.PostTermsWriter) PostWriteOption {
	return func(s *PostWriteService) { s.detacher = w }
}

// WithPostDependentsPurger wires the port that removes a deleted post's
// comments and meta and re-points its children before the row is deleted.
func WithPostDependentsPurger(p domain.PostDependentsPurger) PostWriteOption {
	return func(s *PostWriteService) { s.purger = p }
}

// deleteTaxonomies returns the taxonomies whose relationships are detached
// when a row of typ is deleted, without duplicates. Built-in post and page
// get category and post_tag, then every registered taxonomy whose ObjectTypes
// names typ. A registered type gets its effective Taxonomies.
func (s *PostWriteService) deleteTaxonomies(typ string) []string {
	var out []string
	add := func(n string) {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if pt, ok := s.registered(typ); ok {
		for _, n := range pt.Taxonomies {
			add(n)
		}
		return out
	}
	if typ == "post" || typ == "page" {
		add("category")
		add("post_tag")
		if s.reg != nil {
			for _, t := range s.reg.Taxonomies() {
				if slices.Contains(t.ObjectTypes, typ) {
					add(t.Name)
				}
			}
		}
	}
	return out
}

// hierarchicalType returns the type whose children are re-parented when a row
// of typ is deleted: "page", a registered hierarchical type, else "".
func (s *PostWriteService) hierarchicalType(typ string) string {
	if typ == "page" {
		return "page"
	}
	if pt, ok := s.registered(typ); ok && pt.Hierarchical {
		return typ
	}
	return ""
}

// registered returns the registry entry for typ. Built-in types are never
// registered (the registry reserves their names).
func (s *PostWriteService) registered(typ string) (registry.PostType, bool) {
	if s.reg == nil || typ == "" || typ == "post" || typ == "page" {
		return registry.PostType{}, false
	}
	return s.reg.PostType(typ)
}

// parentRules returns the parent rules for typ: a synthetic hierarchical type
// for the built-in page, else the registered type. Used only for the parent
// step; slug charset and detach branches keep using registered, so page never
// enters the charset path.
func (s *PostWriteService) parentRules(typ string) (registry.PostType, bool) {
	if typ == "page" {
		return registry.PostType{Name: "page", Hierarchical: true}, true
	}
	return s.registered(typ)
}

// uniquePageSlug suffixes slug (-2, -3, ...) until it is free among the
// siblings under parent. No charset rule applies. selfID excludes the page
// being updated. Without a node reader the slug is returned unchanged.
func (s *PostWriteService) uniquePageSlug(ctx context.Context, slug string, parent, selfID int64) (string, error) {
	if s.nodes == nil {
		return slug, nil
	}
	nodes, err := loadPostNodes(ctx, s.nodes, "page")
	if err != nil {
		return "", err
	}
	cand := slug
	for n := 2; slugTaken(nodes, cand, true, parent, selfID); n++ {
		cand = slug + "-" + strconv.Itoa(n)
	}
	return cand, nil
}

// WritableType reports whether this service may modify a stored row of typ.
func (s *PostWriteService) WritableType(typ string) bool {
	return WritableType(s.reg, typ)
}

// suffixFor returns the capability suffix that decides writes of typ: the
// registered type's capability_type family, else the legacy page/post family.
func (s *PostWriteService) suffixFor(typ string) string {
	suf, _, _ := CapabilitySuffixFor(s.reg, typ)
	return suf
}

// resolveSlug validates, derives and de-duplicates the slug of a
// registered-type post. title is the already-sanitized title. selfID excludes
// the post being updated; parent scopes hierarchical types.
func (s *PostWriteService) resolveSlug(ctx context.Context, pt registry.PostType, slug, title string, parent, selfID int64) (string, error) {
	if slug == "" {
		slug = Slugify(title)
	}
	if !slugRE.MatchString(slug) {
		return "", ErrSlugInvalid
	}
	if s.nodes == nil {
		return slug, nil
	}
	nodes, err := loadPostNodes(ctx, s.nodes, pt.Name)
	if err != nil {
		return "", err
	}
	cand := slug
	for n := 2; slugTaken(nodes, cand, pt.Hierarchical, parent, selfID); n++ {
		cand = slug + "-" + strconv.Itoa(n)
	}
	return cand, nil
}

// validatePostParent checks the parent of a registered-type post. id is 0 on
// create. The node reader is type-scoped, so membership in the loaded graph
// means "exists and is of this type".
func (s *PostWriteService) validatePostParent(ctx context.Context, pt registry.PostType, id, parent int64) error {
	if parent == 0 {
		return nil
	}
	if !pt.Hierarchical {
		return ErrPostParentInvalid
	}
	if id != 0 && parent == id {
		return ErrPostParentCycle
	}
	if s.nodes == nil {
		return errPostNodesUnavailable
	}
	nodes, err := loadPostNodes(ctx, s.nodes, pt.Name)
	if err != nil {
		return err
	}
	h := &PageHierarchy{byID: make(map[int64]domain.PageNode, len(nodes))}
	for _, n := range nodes {
		h.byID[n.ID] = n
	}
	if _, ok := h.byID[parent]; !ok {
		return ErrPostParentInvalid
	}
	if id != 0 && h.isAncestorOrSelf(id, parent) {
		return ErrPostParentCycle
	}
	return nil
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
	if !s.WritableType(p.Type) {
		return 0, ErrForbidden
	}
	if p.Date.IsZero() {
		p.Date = time.Now()
	}
	if !auth.CanCreatePostSuffix(actor, s.suffixFor(p.Type), p.Status, p.Author) {
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
	if pt, ok := s.parentRules(p.Type); ok {
		if err := s.validatePostParent(ctx, pt, 0, p.ParentID); err != nil {
			return 0, err
		}
		if p.Type == "page" {
			if p.ParentID != 0 {
				slug, err := s.uniquePageSlug(ctx, p.Slug, p.ParentID, 0)
				if err != nil {
					return 0, err
				}
				p.Slug = slug
			}
			return s.w.Create(ctx, p)
		}
		slug, err := s.resolveSlug(ctx, pt, p.Slug, p.Title, p.ParentID, 0)
		if err != nil {
			return 0, err
		}
		p.Slug = slug
	}
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
	if !s.WritableType(cur.Type) {
		return ErrForbidden
	}
	suffix := s.suffixFor(cur.Type)
	if !auth.CanEditPostSuffix(actor, suffix, cur.Status, cur.Author) {
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
	if pt, ok := s.parentRules(cur.Type); ok {
		moved := p.ParentID != cur.ParentID
		if moved {
			if err := s.validatePostParent(ctx, pt, cur.ID, p.ParentID); err != nil {
				return err
			}
			cur.ParentID = p.ParentID
		}
		if cur.Type == "page" {
			if moved {
				slug, err := s.uniquePageSlug(ctx, p.Slug, cur.ParentID, cur.ID)
				if err != nil {
					return err
				}
				cur.Slug = slug
			}
		} else {
			slug, err := s.resolveSlug(ctx, pt, p.Slug, cur.Title, cur.ParentID, cur.ID)
			if err != nil {
				return err
			}
			cur.Slug = slug
		}
	}
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
		if !auth.CanEditPostSuffix(actor, suffix, p.Status, cur.Author) {
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
//
// The full order is: revisions, term detach (deleteTaxonomies, via the
// detacher when configured), dependents purge (via the purger when configured,
// with the row's own parent id and hierarchicalType), then the row delete. The
// first failing step returns its error and later steps do not run. Every
// earlier step is idempotent, so a retry after a failure converges.
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
	if !s.WritableType(cur.Type) {
		return ErrForbidden
	}
	if !auth.CanDeletePostSuffix(actor, s.suffixFor(cur.Type), cur.Status, cur.Author) {
		return ErrForbidden
	}
	if rw, ok := s.w.(domain.RevisionWriter); ok {
		if err := rw.DeleteRevisionsOf(ctx, cur.ID); err != nil {
			return err
		}
	}
	if s.detacher != nil {
		for _, tax := range s.deleteTaxonomies(cur.Type) {
			if err := s.detacher.SetPostTerms(ctx, cur.ID, tax, nil); err != nil {
				return err
			}
		}
	}
	if s.purger != nil {
		if err := s.purger.PurgePostDependents(ctx, cur.ID, cur.ParentID, s.hierarchicalType(cur.Type)); err != nil {
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
	reg    *registry.Registry
	policy *sanitize.Policy
}

// TermWriteOption configures a TermWriteService.
type TermWriteOption func(*TermWriteService)

// WithTermRegistry enables writes to registered custom taxonomies. A nil
// registry leaves built-in behavior unchanged.
func WithTermRegistry(r *registry.Registry) TermWriteOption {
	return func(s *TermWriteService) { s.reg = r }
}

// WithTermContentPolicy sets the policy used to sanitize registered-taxonomy
// term names. A nil policy is ignored (the default stays).
func WithTermContentPolicy(p *sanitize.Policy) TermWriteOption {
	return func(s *TermWriteService) {
		if p != nil {
			s.policy = p
		}
	}
}

// registryTaxonomy returns the registered entry for name, ok only for entries
// that are not the built-in category/post_tag.
func (s *TermWriteService) registryTaxonomy(name string) (registry.Taxonomy, bool) {
	if s.reg == nil || name == TaxonomyCategory || name == taxonomyPostTag {
		return registry.Taxonomy{}, false
	}
	return s.reg.Taxonomy(name)
}

// hierarchical reports whether terms of the taxonomy may have a parent.
func (s *TermWriteService) hierarchical(name string) bool {
	if name == TaxonomyCategory {
		return true
	}
	rt, ok := s.registryTaxonomy(name)
	return ok && rt.Hierarchical
}

// cleanName reduces a term name to plain text.
//
// The tier is fixed (TierB) rather than derived from the actor: a holder of
// unfiltered_html would otherwise hit the tier-C bypass and store raw markup in
// a name that is rendered as plain text.
func (s *TermWriteService) cleanName(name string) (string, error) {
	out, err := s.policy.SanitizeAt(sanitize.PostTitle, sanitize.TierB, name)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", ErrTermNameEmpty
	}
	return out, nil
}

// checkSlug validates the slug charset and sibling uniqueness for a
// registered taxonomy (one ListByTaxonomy read).
func (s *TermWriteService) checkSlug(ctx context.Context, t domain.Term, hier bool) error {
	if !termSlugRE.MatchString(t.Slug) {
		return ErrTermSlugInvalid
	}
	terms, err := s.w.ListByTaxonomy(ctx, t.Taxonomy)
	if err != nil {
		return err
	}
	for _, o := range terms {
		if o.ID == t.ID && t.ID != 0 {
			continue
		}
		if o.Slug == t.Slug && (!hier || o.ParentID == t.ParentID) {
			return ErrTermSlugTaken
		}
	}
	return nil
}

// prepare reduces the term name for every taxonomy and then applies the slug
// rules of registered taxonomies. When stored is non-nil and the caller's name
// is byte-identical to the stored one, the name is passed through unchanged so
// an update that does not touch the name never rewrites it.
func (s *TermWriteService) prepare(ctx context.Context, t domain.Term, stored *domain.Term) (domain.Term, error) {
	if stored == nil || t.Name != stored.Name {
		name, err := s.cleanName(t.Name)
		if err != nil {
			return t, err
		}
		t.Name = name
	}
	if rt, ok := s.registryTaxonomy(t.Taxonomy); ok {
		if err := s.checkSlug(ctx, t, rt.Hierarchical); err != nil {
			return t, err
		}
	}
	return t, nil
}

// NewTermWriteService constructs a TermWriteService over a combined
// TermWriter+TermReader.
func NewTermWriteService(w interface {
	domain.TermWriter
	domain.TermReader
}, opts ...TermWriteOption) *TermWriteService {
	s := &TermWriteService{w: w, policy: sanitize.New()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// validateParent enforces the hierarchy rules for a term write before any row
// is written. t carries the term's taxonomy (and, on Update, its id); parent is
// the already-resolved authoritative parent value the handler computed from the
// *int64 request field (0 == top-level). It returns:
//
//   - nil when parent == 0 (top-level is valid for every taxonomy, Req 4.4), or
//     when a positive parent passes every check below.
//   - ErrTermParentInvalid when t.Taxonomy is flat (post_tag) and parent != 0
//     (Req 4.3), or when parent names no term in t.Taxonomy (Req 4.1/4.2).
//   - ErrTermParentCycle when parent == t.ID (Req 3.1) or parent is in the
//     term's descendant set (Req 3.2). On Create, t.ID is 0 and the term has no
//     descendants, so only the self check can fire and only for a parent of 0,
//     which is already short-circuited -- Create trivially passes (Req 3.4).
//
// The hierarchy is loaded once here, for t.Taxonomy only, via the combined
// reader. Because the graph is taxonomy-scoped, membership in byID is exactly
// "exists in this taxonomy", so the nonexistent and cross-taxonomy cases collapse
// into one check (Finding 2). The check NEVER consults a taxonomy value taken
// from the request for the parent (Req 4.5): it reads t.Taxonomy (the term being
// written's own taxonomy) and the stored graph.
func (s *TermWriteService) validateParent(ctx context.Context, t domain.Term, parent int64) error {
	if parent == 0 {
		return nil // top-level, valid for any taxonomy (Req 4.4)
	}
	// Flat taxonomies have no meaningful parent (Req 4.3). category and
	// registered hierarchical taxonomies allow one; anything else is rejected.
	if !s.hierarchical(t.Taxonomy) {
		return ErrTermParentInvalid
	}
	if parent == t.ID {
		return ErrTermParentCycle // self-parent (Req 3.1)
	}
	h, err := LoadTermHierarchy(ctx, s.w, t.Taxonomy)
	if err != nil {
		return err
	}
	if _, ok := h.byID[parent]; !ok {
		// Absent from this taxonomy's graph == nonexistent OR cross-taxonomy
		// (Req 4.1, 4.2) -- one check, because the graph is taxonomy-scoped.
		return ErrTermParentInvalid
	}
	// Transitive cycle: the proposed parent is at or beneath the term itself
	// (Req 3.2). DescendantIDs is self-inclusive, so it already contains t.ID;
	// the explicit self check above is kept for clarity and because on Create
	// (t.ID == 0, not in the graph) DescendantIDs returns nil.
	for _, d := range h.DescendantIDs(t.ID) {
		if d == parent {
			return ErrTermParentCycle
		}
	}
	return nil
}

// Create authorizes (manage_categories) and inserts a term.
func (s *TermWriteService) Create(ctx context.Context, actor auth.Principal, t domain.Term) (int64, error) {
	if !auth.CanManageTerms(actor) {
		return 0, ErrForbidden // Req 5.3, fail closed before any read
	}
	if t.Taxonomy != TaxonomyCategory && t.Taxonomy != taxonomyPostTag {
		if _, ok := s.registryTaxonomy(t.Taxonomy); !ok {
			return 0, ErrTaxonomyUnknown
		}
	}
	t, err := s.prepare(ctx, t, nil)
	if err != nil {
		return 0, err
	}
	// t.ParentID is the authoritative value the handler resolved (0 == top-level
	// on create, Req 1.3). A new term has no descendants, so only a flat-taxonomy
	// or nonexistent/cross-taxonomy parent can fail here (Req 3.4, 4).
	if err := s.validateParent(ctx, t, t.ParentID); err != nil {
		return 0, err // ErrTermParentCycle / ErrTermParentInvalid, nothing written
	}
	return s.w.Create(ctx, t)
}

// Update authorizes (manage_categories) and renames a term.
func (s *TermWriteService) Update(ctx context.Context, actor auth.Principal, t domain.Term) error {
	if !auth.CanManageTerms(actor) {
		return ErrForbidden // Req 5.3
	}
	rows, err := s.w.TermsByIDs(ctx, []int64{t.ID})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return domain.ErrNotFound
	}
	stored := rows[0]
	t, err = s.prepare(ctx, t, &stored)
	if err != nil {
		return err
	}
	// t carries the authoritative ParentID the handler resolved from the pre-read
	// (omitted -> current value, Req 2.2; explicit 0 -> top-level, Req 2.3;
	// positive -> re-parent, Req 2.1) and the term's real taxonomy from the same
	// pre-read. validateParent runs the self/descendant cycle check (Req 3) and
	// the membership/flat check (Req 4) against the stored graph.
	if err := s.validateParent(ctx, t, t.ParentID); err != nil {
		return err // nothing written (Req 3.1-3.2, 4)
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
