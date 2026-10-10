package render

import (
	"html/template"
	"time"
)

type CommentView struct {
	Author    string // stays a string, auto-escaped by html/template (Req 6.5)
	AuthorURL string // stays a string, auto-escaped by html/template (Req 6.5)
	Date      time.Time
	// Content holds pre-sanitized comment markup produced by the write-boundary
	// sanitize.Policy (tier A/B/C applied at write time). It is template.HTML so
	// the template engine renders it without a second auto-escape (Req 6.1).
	//
	// Its ONLY sanctioned source is content that passed through sanitize.Policy
	// at the write boundary — no other code may populate this field with
	// un-sanitized bytes (Req 6.7). render names sanitize.Policy here in prose
	// only and does not import internal/sanitize, keeping render a leaf data
	// package free of a dependency cycle.
	Content     template.HTML
	PendingEcho bool
}

type NavMenuView struct {
	Name  string
	Items []NavMenuItemView
}

type NavMenuItemView struct {
	Label    string
	URL      string
	Children []NavMenuItemView
}
