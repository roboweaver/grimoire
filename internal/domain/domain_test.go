package domain

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrNotFoundIsWrappable(t *testing.T) {
	wrapped := fmt.Errorf("repo: %w", ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatal("errors.Is should match wrapped ErrNotFound")
	}
}

// Compile-time assurance that entity fields exist as referenced elsewhere.
func TestEntitiesConstructable(t *testing.T) {
	p := Post{ID: 1, Type: "post", Slug: "hello", Status: "publish"}
	tm := Term{ID: 2, Taxonomy: "category", Slug: "news"}
	o := Option{Name: "blogname", Value: "grimoire"}
	if p.Slug != "hello" || tm.Slug != "news" || o.Name != "blogname" {
		t.Fatal("entity fields not wired correctly")
	}
}

func TestCommentAndMediaFilterZeroValuesDegradeGracefully(t *testing.T) {
	cf := CommentFilter{}
	if cf.PostID != 0 {
		t.Fatalf("default CommentFilter.PostID = %d, want 0", cf.PostID)
	}
	if cf.Statuses != nil && len(cf.Statuses) != 0 {
		t.Fatalf("default CommentFilter.Statuses = %v, want nil/empty", cf.Statuses)
	}
	mf := MediaFilter{}
	if mf.ParentID != 0 {
		t.Fatalf("default MediaFilter.ParentID = %d, want 0", mf.ParentID)
	}
}

func TestM4EntitiesConstructable(t *testing.T) {
	c := Comment{ID: 1, PostID: 2, Status: "1", Parent: 3, UserID: 4}
	cm := CommentMeta{CommentID: 1, Key: "_wp_trash_meta_status", Value: "0"}
	m := Media{ID: 5, ParentID: 6, Filename: "2026/08/pic.jpg", URL: "/wp-content/uploads/2026/08/pic.jpg", MimeType: "image/jpeg"}
	menu := NavMenu{ID: 7, Slug: "primary", Items: []NavMenuItem{{ID: 8, ParentID: 0, Order: 1}}}
	if c.Status != "1" || cm.Key == "" || m.ParentID != 6 || menu.Items[0].ID != 8 {
		t.Fatal("M4 entity fields not wired correctly")
	}
}
