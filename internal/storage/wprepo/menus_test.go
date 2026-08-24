package wprepo

import (
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// TestBuildMenuTreeSelfParentGuard proves a self-parenting menu item (its own
// _menu_item_menu_item_parent equal to its own id, reachable from the real
// root by a duplicate/corrupt row) terminates instead of recursing forever.
// Without the visited-set guard in buildMenuTree this hangs rather than
// failing cleanly, so the assertion runs the call on a goroutine with a
// bounded timeout instead of calling it inline.
func TestBuildMenuTreeSelfParentGuard(t *testing.T) {
	flat := []domain.NavMenuItem{
		{ID: 1, ParentID: 0, Label: "Top"},
		{ID: 1, ParentID: 1, Label: "Corrupt self-parent copy"},
	}
	got := buildMenuTreeWithTimeout(t, flat)
	if len(got) != 1 {
		t.Fatalf("expected 1 top-level item, got %d", len(got))
	}
	// The self-parent copy is attached one level down (as its own "child")
	// before the guard fires and cuts off any further descent.
	if len(got[0].Children) != 1 {
		t.Fatalf("expected 1 self-referencing child, got %d", len(got[0].Children))
	}
	if got[0].Children[0].Children != nil {
		t.Fatalf("expected recursion to be cut off, got %d grandchildren", len(got[0].Children[0].Children))
	}
}

// TestBuildMenuTreeCyclicGuard proves a two-item cycle (A parents B, B parents
// A) reachable from root terminates instead of recursing forever.
func TestBuildMenuTreeCyclicGuard(t *testing.T) {
	flat := []domain.NavMenuItem{
		{ID: 1, ParentID: 0, Label: "A-root-copy"},
		{ID: 1, ParentID: 2, Label: "A-cyclic-copy"},
		{ID: 2, ParentID: 1, Label: "B"},
	}
	got := buildMenuTreeWithTimeout(t, flat)
	if len(got) != 1 {
		t.Fatalf("expected 1 top-level item, got %d", len(got))
	}
}

func buildMenuTreeWithTimeout(t *testing.T, flat []domain.NavMenuItem) []domain.NavMenuItem {
	t.Helper()
	done := make(chan []domain.NavMenuItem, 1)
	go func() { done <- buildMenuTree(flat) }()
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("buildMenuTree did not terminate: infinite recursion on cyclic/self-parenting data")
		return nil
	}
}
