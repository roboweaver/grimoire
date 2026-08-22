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
