package content

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// --- 18-write-hardening: page post_parent guard (task 11.1, RED) --------------
//
// Written failing-first. Today a page's ParentID is silently ignored on Update
// and never validated on Create, so every rejection case below writes and every
// move case keeps the stored parent. Reuses pwcFake / pwcActor / pwcRegistry
// from postwrite_custom_test.go; all helpers here are prefixed ppg.
//
// Seeded graph (type page unless noted):
//
//	1 A (top, slug a)
//	├── 2 B (slug b)
//	│   └── 3 C (slug c)
//	└── 5 E (slug x)       genuine sibling of B: same parent, 1
//	4 D (top, slug x)
//	6 P  type post
//	7    type product
//	8 F  page whose stored parent is 9999 (dangling)

var ppgAdminCaps = []string{"edit_pages", "publish_pages", "edit_others_pages",
	"edit_published_pages", "delete_pages", "edit_private_pages"}

func ppgSeed() *pwcFake {
	return newPwcFake(
		domain.Post{ID: 1, Type: "page", Slug: "a", Title: "A", Status: "publish", Author: 5},
		domain.Post{ID: 2, Type: "page", Slug: "b", Title: "B", Status: "publish", Author: 5, ParentID: 1},
		domain.Post{ID: 3, Type: "page", Slug: "c", Title: "C", Status: "publish", Author: 5, ParentID: 2},
		domain.Post{ID: 4, Type: "page", Slug: "x", Title: "D", Status: "publish", Author: 5},
		domain.Post{ID: 5, Type: "page", Slug: "x", Title: "E", Status: "publish", Author: 5, ParentID: 1},
		domain.Post{ID: 6, Type: "post", Slug: "p", Title: "P", Status: "publish", Author: 5},
		domain.Post{ID: 7, Type: "product", Slug: "widget", Title: "Widget", Status: "publish", Author: 5},
		domain.Post{ID: 8, Type: "page", Slug: "f", Title: "F", Status: "publish", Author: 5, ParentID: 9999},
	)
}

func ppgService(t *testing.T, f *pwcFake) *PostWriteService {
	t.Helper()
	return pwcService(t, f)
}

// ppgUpdate updates page id keeping its stored title, with the given slug and parent.
func ppgUpdate(t *testing.T, f *pwcFake, id int64, slug string, parent int64) error {
	t.Helper()
	cur := f.store[id]
	return ppgService(t, f).Update(context.Background(), pwcActor(5, ppgAdminCaps...),
		domain.Post{ID: id, Type: "page", Title: cur.Title, Slug: slug, ParentID: parent}, time.Time{})
}

func ppgCreate(t *testing.T, f *pwcFake, slug string, parent int64) (int64, error) {
	t.Helper()
	return ppgService(t, f).Create(context.Background(), pwcActor(5, ppgAdminCaps...),
		domain.Post{Type: "page", Title: "New " + slug, Slug: slug, Status: "draft", ParentID: parent})
}

func ppgNoWrites(t *testing.T, f *pwcFake) {
	t.Helper()
	if n := f.writerCalls(); n != 0 {
		t.Fatalf("writer called %d times on rejection", n)
	}
}

// 13.1: a valid parent is stored on create; parent 0 is top level.
func TestPageParent_CreateStoresParent(t *testing.T) {
	t.Run("under A", func(t *testing.T) {
		f := ppgSeed()
		id, err := ppgCreate(t, f, "fresh", 1)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := f.store[id].ParentID; got != 1 {
			t.Fatalf("stored ParentID = %d, want 1", got)
		}
	})
	t.Run("top level", func(t *testing.T) {
		f := ppgSeed()
		id, err := ppgCreate(t, f, "fresh", 0)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := f.store[id].ParentID; got != 0 {
			t.Fatalf("stored ParentID = %d, want 0", got)
		}
	})
}

// 13.1: a valid update moves the page.
func TestPageParent_UpdateStoresParent(t *testing.T) {
	f := ppgSeed()
	if err := ppgUpdate(t, f, 3, "c", 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := f.store[3].ParentID; got != 1 {
		t.Fatalf("stored ParentID = %d, want 1", got)
	}
}

// 13.2, 13.3: self and descendant parents are cycles.
func TestPageParent_CycleRejected(t *testing.T) {
	cases := []struct {
		name   string
		id     int64
		parent int64
	}{
		{"self", 1, 1},
		{"grandchild", 1, 3},
		{"child", 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ppgSeed()
			slug := f.store[tc.id].Slug
			if err := ppgUpdate(t, f, tc.id, slug, tc.parent); !errors.Is(err, ErrPostParentCycle) {
				t.Fatalf("err = %v, want ErrPostParentCycle", err)
			}
			ppgNoWrites(t, f)
		})
	}
}

// 13.4: a post, a product row and an absent id are not valid page parents.
func TestPageParent_InvalidParentRejected(t *testing.T) {
	cases := []struct {
		name   string
		parent int64
	}{
		{"post", 6},
		{"product row", 7},
		{"absent", 9999},
	}
	for _, tc := range cases {
		t.Run("create/"+tc.name, func(t *testing.T) {
			f := ppgSeed()
			if _, err := ppgCreate(t, f, "fresh", tc.parent); !errors.Is(err, ErrPostParentInvalid) {
				t.Fatalf("err = %v, want ErrPostParentInvalid", err)
			}
			ppgNoWrites(t, f)
		})
		t.Run("update/"+tc.name, func(t *testing.T) {
			f := ppgSeed()
			if err := ppgUpdate(t, f, 4, "x", tc.parent); !errors.Is(err, ErrPostParentInvalid) {
				t.Fatalf("err = %v, want ErrPostParentInvalid", err)
			}
			ppgNoWrites(t, f)
		})
	}
}

// 13.5: an update that leaves the parent unchanged never reads the node graph,
// even when the stored parent is dangling.
func TestPageParent_UnchangedParentNotValidated(t *testing.T) {
	t.Run("valid stored parent", func(t *testing.T) {
		f := ppgSeed()
		if err := ppgUpdate(t, f, 2, "b", 1); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if f.nodeCalls != 0 {
			t.Fatalf("node reader called %d times, want 0", f.nodeCalls)
		}
	})
	t.Run("dangling stored parent", func(t *testing.T) {
		f := ppgSeed()
		if err := ppgUpdate(t, f, 8, "f", 9999); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if f.nodeCalls != 0 {
			t.Fatalf("node reader called %d times, want 0", f.nodeCalls)
		}
		if got := f.store[8].ParentID; got != 9999 {
			t.Fatalf("stored ParentID = %d, want 9999", got)
		}
	})
}

// 13.6: moving D (slug x) under A, where sibling E already holds x, stores x-2.
func TestPageParent_MoveSuffixesCollidingSlug(t *testing.T) {
	f := ppgSeed()
	if err := ppgUpdate(t, f, 4, "x", 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := f.store[4]
	if got.ParentID != 1 {
		t.Fatalf("stored ParentID = %d, want 1", got.ParentID)
	}
	if got.Slug != "x-2" {
		t.Fatalf("stored slug = %q, want x-2", got.Slug)
	}
}

// 13.6: creating under A with slug x collides with sibling E and stores x-2.
func TestPageParent_CreateSuffixesCollidingSlug(t *testing.T) {
	f := ppgSeed()
	id, err := ppgCreate(t, f, "x", 1)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := f.store[id].Slug; got != "x-2" {
		t.Fatalf("stored slug = %q, want x-2", got)
	}
}

// 13.7: an update that does not change the parent keeps the caller's slug even
// when it duplicates a sibling (B and E share parent A; E holds x).
func TestPageParent_UnchangedParentKeepsCallerSlug(t *testing.T) {
	f := ppgSeed()
	if err := ppgUpdate(t, f, 2, "x", 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := f.store[2].Slug; got != "x" {
		t.Fatalf("stored slug = %q, want x (not suffixed)", got)
	}
}

// 13.6: no charset rule applies; a percent-encoded slug is accepted on a move.
func TestPageParent_MoveAcceptsPercentEncodedSlug(t *testing.T) {
	f := ppgSeed()
	if err := ppgUpdate(t, f, 4, "caf%C3%A9", 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := f.store[4]
	if got.ParentID != 1 {
		t.Fatalf("stored ParentID = %d, want 1", got.ParentID)
	}
	if got.Slug != "caf%C3%A9" {
		t.Fatalf("stored slug = %q, want caf%%C3%%A9", got.Slug)
	}
}

// 13.8: a non-zero parent with no node reader is an explicit error, nothing written.
func TestPageParent_NilNodeReaderIsExplicitError(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		f := ppgSeed()
		svc := NewPostWriteService(f, WithPostRegistry(pwcRegistry(t)))
		_, err := svc.Create(context.Background(), pwcActor(5, ppgAdminCaps...),
			domain.Post{Type: "page", Title: "Fresh", Slug: "fresh", Status: "draft", ParentID: 1})
		if err == nil {
			t.Fatal("Create with a parent and no node reader succeeded")
		}
		ppgNoWrites(t, f)
	})
	t.Run("update", func(t *testing.T) {
		f := ppgSeed()
		svc := NewPostWriteService(f, WithPostRegistry(pwcRegistry(t)))
		err := svc.Update(context.Background(), pwcActor(5, ppgAdminCaps...),
			domain.Post{ID: 4, Type: "page", Title: "D", Slug: "x", ParentID: 1}, time.Time{})
		if err == nil {
			t.Fatal("Update with a new parent and no node reader succeeded")
		}
		ppgNoWrites(t, f)
	})
}

// 13.9: the capability check precedes parent validation.
func TestPageParent_ForbiddenBeforeInvalidParent(t *testing.T) {
	contributor := []string{"edit_pages"}
	t.Run("update", func(t *testing.T) {
		f := ppgSeed()
		err := ppgService(t, f).Update(context.Background(), pwcActor(5, contributor...),
			domain.Post{ID: 4, Type: "page", Title: "D", Slug: "x", ParentID: 9999}, time.Time{})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		ppgNoWrites(t, f)
	})
	t.Run("create", func(t *testing.T) {
		f := ppgSeed()
		_, err := ppgService(t, f).Create(context.Background(), pwcActor(5, contributor...),
			domain.Post{Type: "page", Title: "Fresh", Slug: "fresh", Status: "publish", ParentID: 9999})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		ppgNoWrites(t, f)
	})
}

// D8: a post ignores a parent change.
func TestPageParent_PostIgnoresParent(t *testing.T) {
	f := ppgSeed()
	err := ppgService(t, f).Update(context.Background(), pwcActor(5, pwcPostsOnly...),
		domain.Post{ID: 6, Type: "post", Title: "P", Slug: "p", ParentID: 1}, time.Time{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := f.store[6].ParentID; got != 0 {
		t.Fatalf("stored ParentID = %d, want 0 (ignored)", got)
	}
}
