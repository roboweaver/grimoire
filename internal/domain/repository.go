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

// AdminPostFilter selects content for the read-only admin list. Unlike the
// public read path it is not limited to published posts, so it can surface
// drafts and pages. All fields are additive read filters; nothing here alters
// schema, so the existing-WordPress-DB overlay is unaffected.
type AdminPostFilter struct {
	Types    []string // e.g. {"post","page"}; empty = both post and page
	Statuses []string // e.g. {"publish","draft","pending","private"}; empty = all
	Limit    int      // page size; <=0 means "no limit"
	Offset   int      // rows to skip
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

// OptionRepository reads site options.
type OptionRepository interface {
	// Get returns an option value by name, or ErrNotFound.
	Get(ctx context.Context, name string) (string, error)
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

// TermWriter creates and deletes taxonomy terms ({prefix}terms +
// {prefix}term_taxonomy).
type TermWriter interface {
	// Create inserts a term and its taxonomy row, returning the term_id.
	Create(ctx context.Context, t Term) (int64, error)
	// Delete removes a term and its taxonomy rows by term_id, or ErrNotFound.
	Delete(ctx context.Context, id int64) error
}

// OptionWriter sets and deletes site options ({prefix}options).
type OptionWriter interface {
	// Set upserts an option value by name.
	Set(ctx context.Context, name, value string) error
	// Delete removes an option by name, or ErrNotFound.
	Delete(ctx context.Context, name string) error
}
