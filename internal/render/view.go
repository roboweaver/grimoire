package render

import (
	"html/template"
	"time"
)

// PostView is the template-facing shape of a post or page. Content is rendered
// as trusted HTML because M1 is read-only against a trusted WordPress database.
type PostView struct {
	Slug    string
	Title   string
	Excerpt string
	Content template.HTML
	Date    time.Time
	Author  int64
}

// TermView is the template-facing shape of a taxonomy term.
type TermView struct {
	Name     string
	Slug     string
	Taxonomy string
}

// IndexData backs the home/index template.
type IndexData struct {
	SiteTitle string
	Tagline   string
	Posts     []PostView
}

// SingleData backs the single/page templates.
type SingleData struct {
	SiteTitle string
	Tagline   string
	Post      PostView
}

// CategoryData backs the category/archive templates.
type CategoryData struct {
	SiteTitle string
	Tagline   string
	Term      TermView
	Posts     []PostView
}

// LoginData backs the login template. CSRFToken is embedded as a hidden form
// field for the double-submit check; Error is set (without detail) after a
// failed attempt so the form shows a generic message without enumerating users;
// Redirect carries the post-login destination.
type LoginData struct {
	SiteTitle string
	CSRFToken string
	Error     bool
	Redirect  string
}
