package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
)

// --- Plan 18 task 3.1 (RED): type gate on RevisionWriteService (Req 2.2, 2.4, 2.5)
//
// Written failing-first. They reference WithRevisionRegistry, which task 3.2
// adds; until then this package's tests fail to compile.

// gate3Unwritable are stored types that no write service may touch: built-in
// non-editable types, plus one absent from the registry under test.
var gate3Unwritable = []string{"shop_order", "attachment", "nav_menu_item", "revision", "ghost"}

var (
	gate3ProdCaps = []string{"edit_products", "publish_products", "edit_published_products"}
	gate3PostCaps = []string{"edit_posts", "publish_posts", "edit_published_posts"}
)

func gate3Admin() auth.Principal { return actor(auth.RoleAdministrator, 1) }

func gate3Principal(id int64, caps []string) auth.Principal {
	return auth.NewPrincipal(id, "u", caps)
}

// gate3RevSvc builds the service under test with a seeded parent row and a
// revision (id 10) belonging to it.
func gate3RevSvc(t *testing.T, reg *registry.Registry, parent domain.Post) (*RevisionWriteService, *fakePostWriter, *fakeRevisionWriter) {
	t.Helper()
	posts := &fakePostWriter{store: map[int64]domain.Post{parent.ID: parent}}
	rw := &fakeRevisionWriter{
		listRevisions: []domain.RevisionMeta{{ID: 10, Author: parent.Author}},
		revisionByID:  domain.Post{ID: 10, ParentID: parent.ID, Title: "old", Content: "old body"},
	}
	if reg == nil {
		return NewRevisionWriteService(rw, posts, -1), posts, rw
	}
	return NewRevisionWriteService(rw, posts, -1, WithRevisionRegistry(reg)), posts, rw
}

func gate3NoWrites(t *testing.T, posts *fakePostWriter, rw *fakeRevisionWriter) {
	t.Helper()
	if posts.updated != nil {
		t.Errorf("posts.Update was called: %+v", posts.updated)
	}
	if len(rw.createRevisionCalls) != 0 {
		t.Errorf("CreateRevision calls = %d, want 0", len(rw.createRevisionCalls))
	}
}

func TestRevisionGateUnwritableParentIsNotFound(t *testing.T) {
	for _, withReg := range []bool{true, false} {
		for _, typ := range gate3Unwritable {
			var reg *registry.Registry
			if withReg {
				reg = ptcRegistry(t)
			}
			svc, posts, rw := gate3RevSvc(t, reg, domain.Post{ID: 9, Type: typ, Status: "draft", Author: 1})
			ctx := context.Background()
			if _, err := svc.List(ctx, gate3Admin(), 9); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("type=%s reg=%v List err = %v, want ErrNotFound", typ, withReg, err)
			}
			if _, err := svc.Get(ctx, gate3Admin(), 9, 10); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("type=%s reg=%v Get err = %v, want ErrNotFound", typ, withReg, err)
			}
			if _, err := svc.Restore(ctx, gate3Admin(), 9, 10); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("type=%s reg=%v Restore err = %v, want ErrNotFound", typ, withReg, err)
			}
			gate3NoWrites(t, posts, rw)
		}
	}
}

func TestRevisionGateRegisteredTypeUsesDerivedCapability(t *testing.T) {
	reg := ptcRegistry(t)
	parent := domain.Post{ID: 9, Type: "product", Status: "draft", Author: 5}
	ctx := context.Background()

	// edit_posts without edit_products: refused as ErrNotFound.
	svc, posts, rw := gate3RevSvc(t, reg, parent)
	wrong := gate3Principal(5, gate3PostCaps)
	if _, err := svc.List(ctx, wrong, 9); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("List with edit_posts only: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(ctx, wrong, 9, 10); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get with edit_posts only: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Restore(ctx, wrong, 9, 10); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Restore with edit_posts only: err = %v, want ErrNotFound", err)
	}
	gate3NoWrites(t, posts, rw)

	// edit_products: succeeds.
	svc, posts, _ = gate3RevSvc(t, reg, parent)
	right := gate3Principal(5, gate3ProdCaps)
	if _, err := svc.List(ctx, right, 9); err != nil {
		t.Errorf("List with edit_products: %v", err)
	}
	if _, err := svc.Get(ctx, right, 9, 10); err != nil {
		t.Errorf("Get with edit_products: %v", err)
	}
	if _, err := svc.Restore(ctx, right, 9, 10); err != nil {
		t.Errorf("Restore with edit_products: %v", err)
	}
	if posts.updated == nil {
		t.Errorf("Restore with edit_products did not update the parent")
	}
}

func TestRevisionGateBuiltinParentsUnchanged(t *testing.T) {
	for _, withReg := range []bool{true, false} {
		for _, typ := range []string{"post", "page", ""} {
			var reg *registry.Registry
			if withReg {
				reg = ptcRegistry(t)
			}
			svc, posts, _ := gate3RevSvc(t, reg, domain.Post{ID: 9, Type: typ, Status: "draft", Author: 5})
			ctx := context.Background()
			author := actor(auth.RoleAuthor, 5)
			if typ == "page" {
				// Pages need page capabilities (Req 2.5: built-ins behave as before).
				if _, err := svc.List(ctx, author, 9); !errors.Is(err, domain.ErrNotFound) {
					t.Errorf("reg=%v post-only principal on page: err = %v, want ErrNotFound", withReg, err)
				}
				author = actor(auth.RoleEditor, 5)
			}
			if _, err := svc.List(ctx, author, 9); err != nil {
				t.Errorf("type=%q reg=%v List: %v", typ, withReg, err)
			}
			if _, err := svc.Get(ctx, author, 9, 10); err != nil {
				t.Errorf("type=%q reg=%v Get: %v", typ, withReg, err)
			}
			if _, err := svc.Restore(ctx, author, 9, 10); err != nil {
				t.Errorf("type=%q reg=%v Restore: %v", typ, withReg, err)
			}
			if posts.updated == nil {
				t.Errorf("type=%q reg=%v Restore did not update parent", typ, withReg)
			}
			// A contributor still cannot edit someone else's draft.
			if _, err := svc.List(ctx, actor(auth.RoleContributor, 99), 9); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("type=%q reg=%v unauthorized List err = %v, want ErrNotFound", typ, withReg, err)
			}
		}
	}
}
