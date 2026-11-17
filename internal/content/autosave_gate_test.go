package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
)

// --- Plan 18 task 3.1 (RED): type gate on AutosaveService (Req 2.3, 2.4, 2.5)
//
// Written failing-first. They reference WithAutosaveRegistry, which task 3.2
// adds. Shares gate3* helpers with revisions_gate_test.go.

func gate3AutoSvc(t *testing.T, reg *registry.Registry, parent domain.Post) (*AutosaveService, *fakePostWriter, *fakeRevisionWriter) {
	t.Helper()
	posts := &fakePostWriter{store: map[int64]domain.Post{parent.ID: parent}}
	rw := &fakeRevisionWriter{
		nextRevisionID: 77,
		autosaveFound:  true,
		autosavePost:   domain.Post{ID: 100, ParentID: parent.ID},
	}
	if reg == nil {
		return NewAutosaveService(rw, posts), posts, rw
	}
	return NewAutosaveService(rw, posts, WithAutosaveRegistry(reg)), posts, rw
}

func gate3AutoNoWrites(t *testing.T, posts *fakePostWriter, rw *fakeRevisionWriter) {
	t.Helper()
	if posts.created != nil || posts.updated != nil {
		t.Errorf("parent row written: created=%v updated=%v", posts.created, posts.updated)
	}
	if len(rw.createRevisionCalls) != 0 {
		t.Errorf("CreateRevision calls = %d, want 0", len(rw.createRevisionCalls))
	}
	if rw.lastUpdateAutosave.revisionID != 0 {
		t.Errorf("UpdateAutosave called for revision %d, want none", rw.lastUpdateAutosave.revisionID)
	}
}

func TestAutosaveGateUnwritableParentIsNotFound(t *testing.T) {
	fields := AutosaveFields{Title: "T", Content: "C"}
	for _, withReg := range []bool{true, false} {
		for _, typ := range gate3Unwritable {
			var reg *registry.Registry
			if withReg {
				reg = ptcRegistry(t)
			}
			svc, posts, rw := gate3AutoSvc(t, reg, domain.Post{ID: 5, Type: typ, Status: "draft", Author: 1})
			if _, err := svc.Save(context.Background(), gate3Admin(), 5, fields); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("type=%s reg=%v Save err = %v, want ErrNotFound", typ, withReg, err)
			}
			if _, _, err := svc.Newer(context.Background(), gate3Admin(), 5); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("type=%s reg=%v Newer err = %v, want ErrNotFound", typ, withReg, err)
			}
			gate3AutoNoWrites(t, posts, rw)
		}
	}
}

func TestAutosaveGateRegisteredTypeUsesDerivedCapability(t *testing.T) {
	reg := ptcRegistry(t)
	parent := domain.Post{ID: 5, Type: "product", Status: "draft", Author: 5}
	fields := AutosaveFields{Title: "T"}
	ctx := context.Background()

	svc, posts, rw := gate3AutoSvc(t, reg, parent)
	wrong := gate3Principal(5, gate3PostCaps)
	if _, err := svc.Save(ctx, wrong, 5, fields); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Save with edit_posts only: err = %v, want ErrNotFound", err)
	}
	if _, _, err := svc.Newer(ctx, wrong, 5); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Newer with edit_posts only: err = %v, want ErrNotFound", err)
	}
	gate3AutoNoWrites(t, posts, rw)

	svc, _, _ = gate3AutoSvc(t, reg, parent)
	right := gate3Principal(5, gate3ProdCaps)
	if _, err := svc.Save(ctx, right, 5, fields); err != nil {
		t.Errorf("Save with edit_products: %v", err)
	}
	if _, _, err := svc.Newer(ctx, right, 5); err != nil {
		t.Errorf("Newer with edit_products: %v", err)
	}
}

func TestAutosaveGateBuiltinParentsUnchanged(t *testing.T) {
	for _, withReg := range []bool{true, false} {
		for _, typ := range []string{"post", "page", ""} {
			var reg *registry.Registry
			if withReg {
				reg = ptcRegistry(t)
			}
			svc, _, _ := gate3AutoSvc(t, reg, domain.Post{ID: 5, Type: typ, Status: "draft", Author: 5})
			ctx := context.Background()
			author := actor(auth.RoleAuthor, 5)
			if typ == "page" {
				// Pages need page capabilities (Req 2.5: built-ins behave as before).
				if _, err := svc.Save(ctx, author, 5, AutosaveFields{Title: "T"}); !errors.Is(err, domain.ErrNotFound) {
					t.Errorf("reg=%v post-only principal on page: err = %v, want ErrNotFound", withReg, err)
				}
				author = actor(auth.RoleEditor, 5)
			}
			if _, err := svc.Save(ctx, author, 5, AutosaveFields{Title: "T"}); err != nil {
				t.Errorf("type=%q reg=%v Save: %v", typ, withReg, err)
			}
			if _, _, err := svc.Newer(ctx, author, 5); err != nil {
				t.Errorf("type=%q reg=%v Newer: %v", typ, withReg, err)
			}
			if _, err := svc.Save(ctx, actor(auth.RoleContributor, 99), 5, AutosaveFields{}); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("type=%q reg=%v unauthorized Save err = %v, want ErrNotFound", typ, withReg, err)
			}
		}
	}
}
