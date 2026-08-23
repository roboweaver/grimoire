package render

import "time"

type CommentView struct {
	Author      string
	AuthorURL   string
	Date        time.Time
	Content     string
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
