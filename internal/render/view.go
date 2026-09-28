package render

import (
	"html/template"
	"time"

	"github.com/roboweaver/grimoire/internal/content"
)

// PostView is the template-facing shape of a post or page. Content and Excerpt
// are rendered as trusted HTML because M1/M2 are read-only against a trusted
// WordPress database. Excerpt carries either a manual post_excerpt or an
// auto-derived summary (see internal/content.Excerpt); the template.HTML cast is
// applied at the web trust boundary (internal/web/view.go).
type PostView struct {
	ID               int64
	Slug             string
	Title            string
	Excerpt          template.HTML
	Content          template.HTML
	Date             time.Time
	Author           int64
	FeaturedImageURL string
}

// TermView is the template-facing shape of a taxonomy term.
type TermView struct {
	Name     string
	Slug     string
	Taxonomy string
}

// IndexData backs the home/index template.
type IndexData struct {
	SiteTitle  string
	Tagline    string
	Posts      []PostView
	Pagination content.Page
}

// SingleData backs the single/page templates.
type SingleData struct {
	SiteTitle      string
	Tagline        string
	Post           PostView
	Comments       []CommentView
	CommentCount   int
	PendingComment *CommentView
	CommentToken   string
	Menu           NavMenuView
}

// ArchiveData backs the category, tag, author and date templates — the four
// archive kinds already registered in the hierarchy map (each {kind} → archive
// → index), so no template-resolution change accompanies it.
//
// Kind names the archive being rendered so a shared archive.tmpl can vary on
// it. Heading is the archive title the handler resolved (a term name, an author
// display name, a formatted date range). BaseURL is the canonical archive path:
// pagination links are built from it rather than reconstructed from Term.Slug,
// which is wrong for a nested category and wrong for an overridden
// category_base. Term is empty for the author and date kinds.
type ArchiveData struct {
	SiteTitle  string
	Tagline    string
	Kind       string // "category" | "tag" | "author" | "date"
	Heading    string
	BaseURL    string
	Term       TermView
	Posts      []PostView
	Pagination content.Page
}

// CategoryData backs the category template. It is an alias rather than a
// distinct type so existing call sites and theme templates keep working:
// html/template resolves field names at execution time, so a renamed or dropped
// field would turn a working page into a 500 that no compile step catches.
// Adding fields to ArchiveData is additive for every template.
type CategoryData = ArchiveData

// LoginData backs the login template. CSRFToken is embedded as a hidden form
// field for the double-submit check; Error is set (without detail) after a
// failed attempt so the form shows a generic message without enumerating users;
// Redirect carries the post-login destination. Tagline mirrors the other page
// data types so the shared base.tmpl masthead can render it uniformly across
// every page (it is left empty here; the login page has no tagline of its own).
type LoginData struct {
	SiteTitle string
	Tagline   string
	CSRFToken string
	Error     bool
	Redirect  string
}
