package domain

import "time"

// Post is a WordPress-compatible content item (post, page, or other post_type).
type Post struct {
	ID            int64
	Author        int64
	Date          time.Time
	Content       string
	Title         string
	Excerpt       string
	Status        string
	Slug          string
	Type          string
	CommentStatus string

	// DateGMT, Modified, and ModifiedGMT back the REST API's
	// date/date_gmt/modified/modified_gmt fields (added by the 0004
	// migration; default '1970-01-01 00:00:00' matching post_date).
	DateGMT     time.Time
	Modified    time.Time
	ModifiedGMT time.Time

	// PingStatus, Password, and GUID back post_ping_status/post_password/guid
	// (added by the 0004 migration; defaults 'open', '', '' respectively).
	PingStatus string
	Password   string
	GUID       string

	// ParentID backs post_parent (added by migration 0003, not a new M7
	// migration -- see plans/07-revisions-scheduler/tasks.md 1.1/1.2). For
	// post_type='revision' rows this is the owning post's ID; it is also
	// used generically for WordPress-style hierarchical parenting.
	ParentID int64
}

// RevisionMeta is the summary shape returned by revision listing (no
// content/title/excerpt body -- Requirement 2.1). Declared now (task 1.4)
// as a pure data shape so ListRevisions' contract test can compile before
// task 1.9 implements the method that returns it.
type RevisionMeta struct {
	ID       int64
	ParentID int64
	Author   int64
	Modified time.Time
	Autosave bool
}

// Term is a taxonomy term (e.g. a category or tag) resolved together with the
// taxonomy it belongs to.
type Term struct {
	ID       int64
	Name     string
	Slug     string
	Taxonomy string

	// ParentID backs term_taxonomy.parent; 0 means "no parent", matching both
	// WordPress's own sentinel and the zero-value-means-unset convention
	// already used by Post.ParentID and MediaFilter.ParentID.
	//
	// Read-only in this milestone: term reads populate it, and no write path
	// sets it.
	ParentID int64
}

// Option is a single row from the WordPress options table.
type Option struct {
	Name  string
	Value string
}

// User is a WordPress-compatible user record ({prefix}users). Pass holds the
// stored password hash (phpass or bcrypt); it is never logged or rendered.
type User struct {
	ID            int64
	Login         string
	Nicename      string
	DisplayName   string
	Pass          string
	Email         string
	URL           string
	Registered    time.Time
	ActivationKey string
	Status        int
}

// Session is a server-side authenticated session ({prefix}sessions). ID is the
// hex SHA-256 of the opaque cookie token; the raw token is never stored.
type Session struct {
	ID        string
	UserID    int64
	CSRFToken string
	Created   time.Time
	Expires   time.Time
}

// Comment maps a WordPress {prefix}comments row.
type Comment struct {
	ID          int64
	PostID      int64
	Author      string
	AuthorEmail string
	AuthorURL   string
	AuthorIP    string
	Date        time.Time
	DateGMT     time.Time
	Content     string
	Status      string
	Agent       string
	Parent      int64
	UserID      int64
	// Honeypot carries the hidden anti-bot field's submitted value. It is
	// evaluated by CommentSpamFilter and never persisted to {prefix}comments.
	Honeypot string
}

// CommentMeta maps a {prefix}commentmeta row with single-valued semantics.
type CommentMeta struct {
	CommentID int64
	Key       string
	Value     string
}

// Media is a WordPress attachment ({prefix}posts row + _wp_attached_file meta).
type Media struct {
	ID       int64
	Title    string
	Filename string
	URL      string
	MimeType string
	Date     time.Time
	ParentID int64

	// Slug and AuthorID back the REST API's slug/author fields (post_name and
	// post_author on the attachment's {prefix}posts row). M4's own upload path
	// (MediaService.Store) always writes a real Slug; AuthorID is 0 for
	// attachments created through that path (M4 never threaded an uploading
	// principal through), but is read correctly for attachments already
	// present in an overlaid, pre-existing WordPress database.
	Slug     string
	AuthorID int64

	// Caption, AltText, and Description back the attachment's editable
	// metadata so an updated value is observable on a subsequent GET: caption
	// maps to post_excerpt, alt text to the _wp_attachment_image_alt postmeta
	// row, and description to post_content. Additive and zero-valued for every
	// existing reader that does not populate them.
	Caption     string
	AltText     string
	Description string
}

// MediaMeta is the editable attachment metadata set: a write-side value object
// distinct from Media so the update port names exactly the fields it persists
// and nothing else. Parent is excluded by design -- it continues to flow
// through MediaWriter.SetParent rather than this value object.
type MediaMeta struct {
	Title       string // post_title
	Caption     string // post_excerpt
	AltText     string // _wp_attachment_image_alt postmeta
	Description string // post_content
}

// UserProfileFields is the editable user-profile set: a write-side value object
// distinct from User so the update port names exactly the fields it persists and
// nothing else. Every field is a *string so a nil pointer means "leave this
// column unchanged" and a non-nil pointer (including a pointer to "") means
// "write this value" -- the partial-update posture MediaMeta serves positionally
// but which users need per-field because any single column may be updated alone.
//
// Each field maps to a column on the user's {prefix}users row:
//
//	DisplayName -> display_name
//	Email       -> user_email
//	URL         -> user_url
//	Nicename    -> user_nicename
//
// description (usermeta), the password (UpdatePass), and roles (usermeta) are
// deliberately absent: this value object never reaches them.
type UserProfileFields struct {
	DisplayName *string // display_name
	Email       *string // user_email
	URL         *string // user_url
	Nicename    *string // user_nicename
}

// any reports whether at least one field is set (non-nil). The update service's
// partial-update guard uses it to reject a no-op write before touching storage,
// matching the posture MediaMeta/any relies on.
func (f UserProfileFields) any() bool {
	return f.DisplayName != nil || f.Email != nil || f.URL != nil || f.Nicename != nil
}

// NavMenu is a nav_menu taxonomy term plus its item tree.
type NavMenu struct {
	ID    int64
	Name  string
	Slug  string
	Items []NavMenuItem
}

// NavMenuItem is a nav_menu_item post resolved from its _menu_item_* postmeta.
type NavMenuItem struct {
	ID       int64
	Label    string
	URL      string
	Type     string
	Object   string
	ObjectID int64
	ParentID int64
	Order    int
	Children []NavMenuItem
}

// NavMenuPatch is a partial menu update; a nil field is left unchanged (Req 1.2).
type NavMenuPatch struct {
	Name *string
	Slug *string
}

// NavMenuItemInput is a fully-specified item for CreateItem (the service has
// sanitized Label, validated URL for type=custom, validated Type, and validated
// ParentID). It maps 1:1 to the five _menu_item_* keys plus post_title/menu_order
// the read path reads back (Req 6.1).
type NavMenuItemInput struct {
	Label    string // -> post_title (sanitized)
	URL      string // -> _menu_item_url (custom only; validated)
	Type     string // -> _menu_item_type (custom|post_type|taxonomy)
	Object   string // -> _menu_item_object
	ObjectID int64  // -> _menu_item_object_id (stored as-is)
	ParentID int64  // -> _menu_item_menu_item_parent (0 = top-level)
	Order    int    // -> menu_order
}

// NavMenuItemPatch is a partial item update; a nil field is left unchanged
// (Req 2.2). Only the supplied _menu_item_* keys / post columns are rewritten.
type NavMenuItemPatch struct {
	Label    *string
	URL      *string
	Type     *string
	Object   *string
	ObjectID *int64
	ParentID *int64
	Order    *int
}
