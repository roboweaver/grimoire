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
}

// Term is a taxonomy term (e.g. a category or tag) resolved together with the
// taxonomy it belongs to.
type Term struct {
	ID       int64
	Name     string
	Slug     string
	Taxonomy string
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
