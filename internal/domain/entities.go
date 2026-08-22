package domain

import "time"

// Post is a WordPress-compatible content item (post, page, or other post_type).
type Post struct {
	ID      int64
	Author  int64
	Date    time.Time
	Content string
	Title   string
	Excerpt string
	Status  string
	Slug    string
	Type    string
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
