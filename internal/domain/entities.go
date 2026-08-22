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
