package domain

import (
	"context"
	"time"
)

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

// PostTermsRepository resolves the taxonomy terms related to a post. It is
// additive and read-only: no schema change beyond the {prefix}posts columns
// added by the 0004 migration is required, since it reads the existing
// {prefix}term_relationships/{prefix}term_taxonomy/{prefix}terms tables.
type PostTermsRepository interface {
	// TermsForPost returns the term IDs related to postID under taxonomy
	// (e.g. "category" or "post_tag"), in name-ascending order (not insertion
	// order), so REST responses are deterministic across vendors. A post with
	// no matching terms, or a nonexistent post ID, returns an empty slice and
	// a nil error.
	TermsForPost(ctx context.Context, postID int64, taxonomy string) ([]int64, error)
}

// AdminPostFilter selects content for the read-only admin list. Unlike the
// public read path it is not limited to published posts, so it can surface
// drafts and pages. All fields are additive read filters; nothing here alters
// schema, so the existing-WordPress-DB overlay is unaffected.
type AdminPostFilter struct {
	Types    []string // e.g. {"post","page"}; empty = both post and page
	Statuses []string // e.g. {"publish","draft","pending","private"}; empty = all
	Limit    int      // page size; <=0 means "no limit"
	Offset   int      // rows to skip

	// Search, when non-empty, restricts results to posts whose title or
	// content contains the term (case-insensitive substring match).
	Search string
	// OrderBy selects the sort column: "date" (default) or "id".
	OrderBy string
	// Order selects sort direction: "desc" (default) or "asc".
	Order string
}

// AdminPostRepository lists and counts content for the admin, including drafts
// and pages. Both methods are pure reads (SELECT / COUNT); neither writes.
type AdminPostRepository interface {
	// ListForAdmin returns posts matching the filter ordered newest first
	// (post_date DESC, ID DESC).
	ListForAdmin(ctx context.Context, f AdminPostFilter) ([]Post, error)
	// CountForAdmin returns the total number of posts matching the filter,
	// ignoring Limit/Offset (used for pagination totals).
	CountForAdmin(ctx context.Context, f AdminPostFilter) (int, error)
}

// PostCounter counts posts for the dashboard. Additive; pure COUNT(*).
type PostCounter interface {
	// CountByStatus counts posts of the given post_type and post_status. An
	// empty typ matches any type; an empty status matches any status.
	CountByStatus(ctx context.Context, typ, status string) (int, error)
}

// UserCounter counts user rows for the dashboard. Additive; pure COUNT(*).
type UserCounter interface {
	// CountUsers returns the number of rows in {prefix}users.
	CountUsers(ctx context.Context) (int, error)
}

// TermCounter counts taxonomy terms for the dashboard. Additive; pure COUNT(*).
type TermCounter interface {
	// CountTerms returns the number of terms in the given taxonomy (e.g.
	// "category").
	CountTerms(ctx context.Context, taxonomy string) (int, error)
}

// CommentFilter selects comments for the public list or admin queue. Empty
// Statuses means all statuses; PostID 0 means across all posts.
type CommentFilter struct {
	PostID   int64
	Statuses []string
	Limit    int
	Offset   int
}

// CommentRepository reads comments from {prefix}comments.
type CommentRepository interface {
	List(ctx context.Context, f CommentFilter) ([]Comment, error)
	Count(ctx context.Context, f CommentFilter) (int, error)
	ByID(ctx context.Context, id int64) (Comment, error)
}

// CommentWriter inserts and moderates comments.
type CommentWriter interface {
	Create(ctx context.Context, c Comment) (int64, error)
	UpdateStatus(ctx context.Context, id int64, status string) error
}

// CommentMetaRepository reads and writes {prefix}commentmeta.
type CommentMetaRepository interface {
	Get(ctx context.Context, commentID int64, key string) (string, error)
	Set(ctx context.Context, commentID int64, key, value string) error
	ByComment(ctx context.Context, commentID int64) (map[string]string, error)
	Delete(ctx context.Context, commentID int64, key string) error
}

// MediaFilter selects attachments for the media library. ParentID 0 means any.
type MediaFilter struct {
	ParentID int64
	Limit    int
	Offset   int
}

// MediaRepository lists and reads attachments.
type MediaRepository interface {
	List(ctx context.Context, f MediaFilter) ([]Media, error)
	Count(ctx context.Context, f MediaFilter) (int, error)
	ByID(ctx context.Context, id int64) (Media, error)
}

// MediaWriter creates attachments and updates attachment parents.
type MediaWriter interface {
	Create(ctx context.Context, m Media) (int64, error)
	SetParent(ctx context.Context, id, parentID int64) error
}

// NavMenuRepository reads menus and their items.
type NavMenuRepository interface {
	Menus(ctx context.Context) ([]NavMenu, error)
	MenuBySlug(ctx context.Context, slug string) (NavMenu, error)
	MenuByID(ctx context.Context, id int64) (NavMenu, error)
	MenuByLocation(ctx context.Context, theme, location string) (NavMenu, error)
}

// CommentSpamFilter classifies anonymous comment submissions before persistence.
type CommentSpamFilter interface {
	Evaluate(ctx context.Context, c Comment, post Post) (string, error)
}

// OptionRepository reads site options.
type OptionRepository interface {
	// Get returns an option value by name, or ErrNotFound.
	Get(ctx context.Context, name string) (string, error)
}

// PostMetaRepository reads REST-relevant single-valued postmeta ({prefix}postmeta)
// used to populate a post's featured_media and media_details fields. It is
// additive and read-only: no schema change beyond 0004 is required, since it
// reads the existing postmeta table.
type PostMetaRepository interface {
	// FeaturedMediaID returns the post's featured image attachment ID from
	// its "_thumbnail_id" postmeta value, or 0 if unset or unparsable.
	FeaturedMediaID(ctx context.Context, postID int64) (int64, error)
	// AttachmentMetadata returns the raw (PHP-serialized) value of an
	// attachment post's "_wp_attachment_metadata" postmeta, or "" if unset.
	AttachmentMetadata(ctx context.Context, postID int64) (string, error)
}

// UserRepository reads and writes users ({prefix}users).
type UserRepository interface {
	// ByLogin returns a user by user_login, or ErrNotFound.
	ByLogin(ctx context.Context, login string) (User, error)
	// ByID returns a user by ID, or ErrNotFound.
	ByID(ctx context.Context, id int64) (User, error)
	// Create inserts a new user and returns its generated ID.
	Create(ctx context.Context, u User) (int64, error)
	// UpdatePass replaces the stored password hash for a user. It returns
	// ErrNotFound when no user has the given ID.
	UpdatePass(ctx context.Context, id int64, passHash string) error
	// List returns users ordered by ID ascending, for REST /users pagination.
	List(ctx context.Context, limit, offset int) ([]User, error)
	// Count returns the total number of users, ignoring limit/offset (used
	// for pagination totals).
	Count(ctx context.Context) (int64, error)
}

// UserMetaRepository reads and writes user metadata ({prefix}usermeta). It
// models single-valued meta (the last write for a key wins), which is how
// grimoire stores {prefix}capabilities and {prefix}user_level.
type UserMetaRepository interface {
	// Get returns the value for a user's meta key, or ErrNotFound.
	Get(ctx context.Context, userID int64, key string) (string, error)
	// Set upserts a single-valued meta row for the user/key pair.
	Set(ctx context.Context, userID int64, key, value string) error
	// ByUser returns all single-valued meta for a user keyed by meta_key.
	ByUser(ctx context.Context, userID int64) (map[string]string, error)
}

// SessionRepository persists server-side sessions ({prefix}sessions).
type SessionRepository interface {
	// Create inserts a new session row.
	Create(ctx context.Context, s Session) error
	// ByID returns a session by its ID (hashed token), or ErrNotFound.
	ByID(ctx context.Context, id string) (Session, error)
	// Touch extends a session's expiry (rolling refresh). It returns
	// ErrNotFound when no session has the given ID.
	Touch(ctx context.Context, id string, expires time.Time) error
	// Delete removes a single session (logout).
	Delete(ctx context.Context, id string) error
	// DeleteByUser removes all of a user's sessions (revoke-all) and returns
	// the number of rows deleted.
	DeleteByUser(ctx context.Context, userID int64) (int64, error)
	// DeleteExpired removes sessions whose expiry is before the given time
	// (garbage collection) and returns the number of rows deleted.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// PostWriter creates, updates, and deletes posts and pages ({prefix}posts). It
// also reads a single post by ID so write services can authorize against the
// authoritative stored record rather than caller-supplied fields.
type PostWriter interface {
	// ByID returns the stored post/page by primary key regardless of status or
	// type, or ErrNotFound. Used to load the authoritative record for authz.
	ByID(ctx context.Context, id int64) (Post, error)
	// Create inserts a new post and returns its generated ID.
	Create(ctx context.Context, p Post) (int64, error)
	// Update replaces an existing post's fields by ID, or ErrNotFound.
	Update(ctx context.Context, p Post) error
	// Delete removes a post by ID, or ErrNotFound.
	Delete(ctx context.Context, id int64) error
}

// RevisionWriter is the storage port used by content.RevisionWriteService and
// content.AutosaveService. Revisions and autosave rows are ordinary rows in
// the same {prefix}posts table PostWriter already owns (post_type
// "revision"), so no separate repository type exists for them.
type RevisionWriter interface {
	// CreateRevision inserts a snapshot of a post as a new revision row
	// (post_type='revision', post_parent=parentID, post_status='inherit'),
	// attributed to authorID, and returns its generated ID. When autosave is
	// true the row is named/marked so AutosaveFor can find it later.
	CreateRevision(ctx context.Context, parentID, authorID int64, snapshot Post, autosave bool) (int64, error)
	// ListRevisions returns newest-first summaries (no content body) of every
	// non-autosave revision belonging to parentID.
	ListRevisions(ctx context.Context, parentID int64) ([]RevisionMeta, error)
	// RevisionByID returns the full content/title/excerpt for a single
	// revision (or autosave) row by its own ID, or ErrNotFound.
	RevisionByID(ctx context.Context, id int64) (Post, error)
	// AutosaveFor returns the single autosave row for (parentID, authorID),
	// or (Post{}, false, nil) when none exists yet.
	AutosaveFor(ctx context.Context, parentID, authorID int64) (Post, bool, error)
	// UpdateAutosave overwrites an existing autosave row's snapshot fields in
	// place by its revision ID, or ErrNotFound.
	UpdateAutosave(ctx context.Context, revisionID int64, snapshot Post) error
	// PruneRevisions deletes the oldest non-autosave revisions for parentID
	// beyond the newest `keep`, never touching the autosave row.
	PruneRevisions(ctx context.Context, parentID int64, keep int) error
	// DeleteRevisionsOf deletes every revision row (including the autosave,
	// if present) for parentID. Used on post delete (Req 1.6).
	DeleteRevisionsOf(ctx context.Context, parentID int64) error
}

// ScheduledPostFinder is the read port the scheduler polls for due
// scheduled posts.
type ScheduledPostFinder interface {
	// DueScheduled returns every post with status="future" whose post_date
	// has already passed asOf.
	DueScheduled(ctx context.Context, asOf time.Time) ([]Post, error)
}

// TermWriter creates, updates, and deletes taxonomy terms ({prefix}terms +
// {prefix}term_taxonomy).
type TermWriter interface {
	// Create inserts a term and its taxonomy row, returning the term_id.
	Create(ctx context.Context, t Term) (int64, error)
	// Update renames an existing term's name/slug by term_id, or ErrNotFound.
	Update(ctx context.Context, t Term) error
	// Delete removes a term and its taxonomy rows by term_id, or ErrNotFound.
	Delete(ctx context.Context, id int64) error
}

// TermReader lists and bulk-resolves taxonomy terms. It is additive and
// read-only, added by M6 to serve the admin editor's term picker (Req 2.4)
// and post-detail term resolution (Req 4.1) — neither of which
// TermRepository.BySlug (single term by slug) can serve.
type TermReader interface {
	// ListByTaxonomy returns every term of the given taxonomy (e.g.
	// "category", "post_tag"), ordered by name.
	ListByTaxonomy(ctx context.Context, taxonomy string) ([]Term, error)
	// TermsByIDs bulk-resolves term IDs to full {ID, Name, Slug, Taxonomy}
	// objects. Unknown IDs are silently omitted from the result, not an
	// error. An empty ids slice returns an empty result and a nil error.
	TermsByIDs(ctx context.Context, ids []int64) ([]Term, error)
}

// PostTermsWriter replaces a post's taxonomy term relationships
// ({prefix}term_relationships). It is additive: no write path for
// term_relationships existed before M6 (PostTermsRepository, M5, is
// read-only).
type PostTermsWriter interface {
	// SetPostTerms replaces postID's term relationships for taxonomy with
	// exactly termIDs (an empty slice clears that taxonomy's terms from the
	// post), maintaining each affected term_taxonomy.count. Other taxonomies'
	// relationships for the same post are left untouched.
	SetPostTerms(ctx context.Context, postID int64, taxonomy string, termIDs []int64) error
}

// OptionWriter sets and deletes site options ({prefix}options).
type OptionWriter interface {
	// Set upserts an option value by name.
	Set(ctx context.Context, name, value string) error
	// Delete removes an option by name, or ErrNotFound.
	Delete(ctx context.Context, name string) error
}
