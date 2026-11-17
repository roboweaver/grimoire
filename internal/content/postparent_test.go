package content

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// --- M17: hierarchical post_parent guard (task 13.1, RED) ---------------------
//
// Written failing-first. They reference symbols task 13.2 adds that do not yet
// exist: ErrPostParentInvalid, ErrPostParentCycle and
// PageHierarchy.isAncestorOrSelf. Until 13.2 lands this file fails to compile;
// once stubbed, the parent is ignored.
//
// Seeded `guide` graph (hierarchical, registered by pwcRegistry):
//
//	1 root
//	├── 2 child
//	│   └── 3 grandchild
//	└── 4 sibling   (a genuine sibling of 2: same parent, 1)
//
// plus a flat `product` row (50) and a `product` row (51) used as the "other
// type" parent. Reuses the pwcFake / pwcRegistry fixtures from
// postwrite_custom_test.go.

var pparGuideCaps = []string{"edit_guides", "publish_guides", "edit_published_guides",
	"delete_guides", "delete_published_guides", "edit_others_guides", "delete_others_guides"}

func pparSeed(extra ...domain.Post) *pwcFake {
	base := []domain.Post{
		{ID: 1, Type: "guide", Slug: "root", Title: "Root", Status: "publish", Author: 5},
		{ID: 2, Type: "guide", Slug: "child", Title: "Child", Status: "publish", Author: 5, ParentID: 1},
		{ID: 3, Type: "guide", Slug: "grandchild", Title: "Grandchild", Status: "publish", Author: 5, ParentID: 2},
		{ID: 4, Type: "guide", Slug: "sibling", Title: "Sibling", Status: "publish", Author: 5, ParentID: 1},
		{ID: 50, Type: "product", Slug: "widget", Title: "Widget", Status: "publish", Author: 5},
		{ID: 51, Type: "product", Slug: "gadget", Title: "Gadget", Status: "publish", Author: 5},
	}
	return newPwcFake(append(base, extra...)...)
}

func pparUpdateGuide(t *testing.T, f *pwcFake, id, parent int64) error {
	t.Helper()
	cur := f.store[id]
	return pwcService(t, f).Update(context.Background(), pwcActor(5, pparGuideCaps...),
		domain.Post{ID: id, Type: "guide", Title: cur.Title, Slug: cur.Slug, ParentID: parent}, time.Time{})
}

func pparCreate(t *testing.T, f *pwcFake, typ, slug string, parent int64) (int64, error) {
	t.Helper()
	caps := pparGuideCaps
	if typ == "product" {
		caps = pwcProdAuthor
	}
	return pwcService(t, f).Create(context.Background(), pwcActor(5, caps...),
		domain.Post{Type: typ, Title: "New " + slug, Slug: slug, Status: "draft", ParentID: parent})
}

// 11.1: a valid same-type parent is written on create and update.
func TestPostParent_ValidParentWritten(t *testing.T) {
	t.Run("create under child", func(t *testing.T) {
		f := pparSeed()
		id, err := pparCreate(t, f, "guide", "fresh", 2)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := f.store[id].ParentID; got != 2 {
			t.Fatalf("stored ParentID = %d, want 2", got)
		}
	})
	t.Run("update moves child under sibling", func(t *testing.T) {
		f := pparSeed()
		if err := pparUpdateGuide(t, f, 2, 4); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got := f.store[2].ParentID; got != 4 {
			t.Fatalf("stored ParentID = %d, want 4", got)
		}
	})
	t.Run("update to top level", func(t *testing.T) {
		f := pparSeed()
		if err := pparUpdateGuide(t, f, 2, 0); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got := f.store[2].ParentID; got != 0 {
			t.Fatalf("stored ParentID = %d, want 0", got)
		}
	})
}

// 11.2: a non-zero parent on a flat registered type is rejected.
func TestPostParent_FlatTypeRejectsParent(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		f := pparSeed()
		if _, err := pparCreate(t, f, "product", "fresh", 50); !errors.Is(err, ErrPostParentInvalid) {
			t.Fatalf("err = %v, want ErrPostParentInvalid", err)
		}
		if n := len(f.creates); n != 0 {
			t.Fatalf("writer called %d times on rejection", n)
		}
	})
	t.Run("update", func(t *testing.T) {
		f := pparSeed()
		err := pwcService(t, f).Update(context.Background(), pwcActor(5, pwcProdAuthor...),
			domain.Post{ID: 50, Type: "product", Title: "Widget", Slug: "widget", ParentID: 51}, time.Time{})
		if !errors.Is(err, ErrPostParentInvalid) {
			t.Fatalf("err = %v, want ErrPostParentInvalid", err)
		}
		if n := len(f.updates); n != 0 {
			t.Fatalf("writer called %d times on rejection", n)
		}
	})
}

// 11.3: an absent parent, or one of another type, is rejected.
func TestPostParent_AbsentOrOtherTypeRejected(t *testing.T) {
	cases := []struct {
		name   string
		parent int64
	}{
		{"absent", 999},
		{"product row for a guide", 50},
	}
	for _, tc := range cases {
		t.Run("create/"+tc.name, func(t *testing.T) {
			f := pparSeed()
			if _, err := pparCreate(t, f, "guide", "fresh", tc.parent); !errors.Is(err, ErrPostParentInvalid) {
				t.Fatalf("err = %v, want ErrPostParentInvalid", err)
			}
			if n := len(f.creates); n != 0 {
				t.Fatalf("writer called %d times on rejection", n)
			}
		})
		t.Run("update/"+tc.name, func(t *testing.T) {
			f := pparSeed()
			if err := pparUpdateGuide(t, f, 4, tc.parent); !errors.Is(err, ErrPostParentInvalid) {
				t.Fatalf("err = %v, want ErrPostParentInvalid", err)
			}
			if n := len(f.updates); n != 0 {
				t.Fatalf("writer called %d times on rejection", n)
			}
		})
	}
}

// 11.4: parent == self and parent == descendant are cycles.
func TestPostParent_CycleRejected(t *testing.T) {
	cases := []struct {
		name   string
		id     int64
		parent int64
	}{
		{"self", 2, 2},
		{"child", 1, 2},
		{"grandchild", 1, 3},
		{"own grandchild from middle", 2, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := pparSeed()
			if err := pparUpdateGuide(t, f, tc.id, tc.parent); !errors.Is(err, ErrPostParentCycle) {
				t.Fatalf("err = %v, want ErrPostParentCycle", err)
			}
			if n := len(f.updates); n != 0 {
				t.Fatalf("writer called %d times on rejection", n)
			}
		})
	}
}

// isAncestorOrSelf(ancestor, id): true when ancestor is id or above it, and it
// terminates on a stored cycle.
func TestPostParent_IsAncestorOrSelf(t *testing.T) {
	h := &PageHierarchy{byID: map[int64]domain.PageNode{
		1: {ID: 1}, 2: {ID: 2, ParentID: 1}, 3: {ID: 3, ParentID: 2}, 4: {ID: 4, ParentID: 1},
	}}
	cases := []struct {
		name         string
		ancestor, id int64
		want         bool
	}{
		{"self", 2, 2, true},
		{"parent", 1, 2, true},
		{"grandparent", 1, 3, true},
		{"descendant is not ancestor", 3, 1, false},
		{"sibling is not ancestor", 4, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.isAncestorOrSelf(tc.ancestor, tc.id); got != tc.want {
				t.Fatalf("isAncestorOrSelf(%d, %d) = %v, want %v", tc.ancestor, tc.id, got, tc.want)
			}
		})
	}

	t.Run("terminates on a corrupt stored cycle", func(t *testing.T) {
		corrupt := &PageHierarchy{byID: map[int64]domain.PageNode{
			1: {ID: 1, ParentID: 2}, 2: {ID: 2, ParentID: 3}, 3: {ID: 3, ParentID: 1},
		}}
		done := make(chan bool, 1)
		go func() { done <- corrupt.isAncestorOrSelf(99, 1) }()
		select {
		case got := <-done:
			if got {
				t.Fatalf("isAncestorOrSelf(99, 1) = true over a cycle not containing 99")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("isAncestorOrSelf did not terminate on a stored cycle")
		}
	})
}

// 11.6: an update that leaves post_parent unchanged is not validated, so a row
// with an imported, inconsistent hierarchy stays editable.
func TestPostParent_UnchangedParentNotValidated(t *testing.T) {
	t.Run("parent names no row", func(t *testing.T) {
		f := pparSeed(domain.Post{ID: 5, Type: "guide", Slug: "orphan", Title: "Orphan",
			Status: "publish", Author: 5, ParentID: 999})
		if err := pparUpdateGuide(t, f, 5, 999); err != nil {
			t.Fatalf("Update with unchanged inconsistent parent: %v", err)
		}
		if got := f.store[5].ParentID; got != 999 {
			t.Fatalf("stored ParentID = %d, want 999", got)
		}
	})
	t.Run("stored cycle", func(t *testing.T) {
		f := pparSeed(
			domain.Post{ID: 6, Type: "guide", Slug: "loop-a", Title: "A", Status: "publish", Author: 5, ParentID: 7},
			domain.Post{ID: 7, Type: "guide", Slug: "loop-b", Title: "B", Status: "publish", Author: 5, ParentID: 6},
		)
		if err := pparUpdateGuide(t, f, 6, 7); err != nil {
			t.Fatalf("Update with unchanged cyclic parent: %v", err)
		}
	})
}

// 11.7: authorization precedes parent validation.
func TestPostParent_ForbiddenBeforeCycle(t *testing.T) {
	f := pparSeed()
	err := pwcService(t, f).Update(context.Background(), pwcActor(5, pwcPostsOnly...),
		domain.Post{ID: 1, Type: "guide", Title: "Root", Slug: "root", ParentID: 3}, time.Time{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if errors.Is(err, ErrPostParentCycle) {
		t.Fatalf("err matched ErrPostParentCycle; capability must be checked first")
	}
	if n := f.writerCalls(); n != 0 {
		t.Fatalf("writer called %d times on rejection", n)
	}
}

// 11.9: post updates never change ParentID. (The page case moved to
// pageparent_test.go in plan 18 task 11.2: a page's parent is now guarded.)
func TestPostParent_BuiltinUpdatesKeepParent(t *testing.T) {
	cases := []struct {
		typ  string
		caps []string
	}{
		{"post", pwcPostsOnly},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			f := newPwcFake(domain.Post{ID: 20, Type: tc.typ, Slug: "x", Title: "X",
				Status: "publish", Author: 5, ParentID: 7})
			err := pwcService(t, f).Update(context.Background(), pwcActor(5, tc.caps...),
				domain.Post{ID: 20, Type: tc.typ, Title: "X2", Slug: "x", ParentID: 9}, time.Time{})
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if got := f.store[20].ParentID; got != 7 {
				t.Fatalf("stored ParentID = %d, want 7 (unchanged)", got)
			}
		})
	}
}

// A hierarchical registered type with no node reader and a non-zero parent must
// fail explicitly instead of skipping validation.
func TestPostParent_NilNodeReaderIsExplicitError(t *testing.T) {
	f := pparSeed()
	svc := NewPostWriteService(f, WithPostRegistry(pwcRegistry(t))) // no WithPostNodeReader
	_, err := svc.Create(context.Background(), pwcActor(5, pparGuideCaps...),
		domain.Post{Type: "guide", Title: "Fresh", Slug: "fresh", Status: "draft", ParentID: 2})
	if err == nil {
		t.Fatal("Create with a parent and no node reader succeeded; validation was skipped")
	}
	if n := len(f.creates); n != 0 {
		t.Fatalf("writer called %d times on rejection", n)
	}
}
