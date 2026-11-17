package content

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

// --- M18: type gate on PostWriteService (task 2.1, RED) ----------------------
//
// Written failing-first. They reference content.WritableType and
// (*PostWriteService).WritableType, which task 2.2 adds; until then this file
// fails to compile. Once stubbed, Update/Delete on a shop_order row succeed for
// an administrator because the gate does not exist.

// pwgFake is a recording PostWriter and revisionSnapshotter.
type pwgFake struct {
	domain.PostWriter // nil: only the four methods below are used
	store             map[int64]domain.Post
	byIDs             int
	creates           int
	updates           int
	deletes           int
	snapshots         int
}

func newPwgFake(posts ...domain.Post) *pwgFake {
	f := &pwgFake{store: map[int64]domain.Post{}}
	for _, p := range posts {
		f.store[p.ID] = p
	}
	return f
}

func (f *pwgFake) ByID(_ context.Context, id int64) (domain.Post, error) {
	f.byIDs++
	p, ok := f.store[id]
	if !ok {
		return domain.Post{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *pwgFake) Create(_ context.Context, p domain.Post) (int64, error) {
	f.creates++
	return 500, nil
}

func (f *pwgFake) Update(_ context.Context, p domain.Post) error {
	f.updates++
	return nil
}

func (f *pwgFake) Delete(_ context.Context, id int64) error {
	f.deletes++
	return nil
}

func (f *pwgFake) Snapshot(context.Context, domain.Post, int64) error {
	f.snapshots++
	return nil
}

func (f *pwgFake) writes() int { return f.creates + f.updates + f.deletes }

// pwgRegistry registers product, and guide only when withGuide is true.
func pwgRegistry(t *testing.T, withGuide bool) *registry.Registry {
	t.Helper()
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	yes := true
	types := []config.PostTypeConfig{
		{Name: "product", Public: &yes, Slug: "product", WithFront: &yes, CapabilityType: "product"},
	}
	if withGuide {
		types = append(types, config.PostTypeConfig{Name: "guide", Public: &yes, Slug: "guide",
			WithFront: &yes, Hierarchical: true, CapabilityType: "guide"})
	}
	reg, err := registry.Build(config.ContentConfig{PostTypes: types}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

// pwgAdmin holds every capability family the cases touch, including the
// families a naive implementation would derive for the unwritable types.
func pwgAdmin() auth.Principal {
	var caps []string
	for _, suf := range []string{"posts", "pages", "products", "guides", "shop_orders", "attachments", "nav_menu_items", "revisions"} {
		for _, verb := range []string{"edit", "publish", "delete", "edit_others", "delete_others",
			"edit_published", "delete_published", "edit_private", "delete_private"} {
			caps = append(caps, verb+"_"+suf)
		}
	}
	return auth.NewPrincipal(1, "admin", caps)
}

func pwgService(t *testing.T, f *pwgFake, reg *registry.Registry) *PostWriteService {
	t.Helper()
	opts := []PostWriteOption{WithRevisionSnapshotter(f)}
	if reg != nil {
		opts = append(opts, WithPostRegistry(reg))
	}
	return NewPostWriteService(f, opts...)
}

// Unwritable rows against the service registry (product only).
var pwgUnwritable = []string{"shop_order", "attachment", "nav_menu_item", "revision", "guide"}

func TestPostWriteGate_UnwritableTypesRefused(t *testing.T) {
	for _, typ := range pwgUnwritable {
		for _, regName := range []string{"registry", "nil registry"} {
			t.Run(typ+"/"+regName, func(t *testing.T) {
				var reg *registry.Registry
				if regName == "registry" {
					reg = pwgRegistry(t, false) // guide is registered elsewhere, absent here
				}
				f := newPwgFake(domain.Post{ID: 10, Type: typ, Title: "T", Status: "publish", Author: 1})
				svc := pwgService(t, f, reg)
				ctx := context.Background()

				err := svc.Update(ctx, pwgAdmin(), domain.Post{ID: 10, Type: typ, Title: "T2"}, time.Time{})
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("Update err = %v, want ErrForbidden", err)
				}
				err = svc.Delete(ctx, pwgAdmin(), domain.Post{ID: 10})
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("Delete err = %v, want ErrForbidden", err)
				}
				if f.snapshots != 0 || f.writes() != 0 {
					t.Fatalf("snapshots=%d updates=%d deletes=%d creates=%d, want all zero",
						f.snapshots, f.updates, f.deletes, f.creates)
				}
				if got, want := svc.WritableType(typ), WritableType(reg, typ); got != want || got {
					t.Fatalf("service.WritableType(%q) = %v, helper = %v, want both false", typ, got, want)
				}
			})
		}
	}
}

func TestPostWriteGate_CreateUnwritableRefused(t *testing.T) {
	for _, typ := range []string{"shop_order", "attachment", "nav_menu_item", "revision"} {
		t.Run(typ, func(t *testing.T) {
			f := newPwgFake()
			svc := pwgService(t, f, pwgRegistry(t, false))
			_, err := svc.Create(context.Background(), pwgAdmin(),
				domain.Post{Type: typ, Title: "T", Status: "draft"})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("Create err = %v, want ErrForbidden", err)
			}
			if f.writes() != 0 {
				t.Fatalf("writer called %d times, want 0", f.writes())
			}
		})
	}
}

func TestPostWriteGate_WritableTypesKeepCapabilityPath(t *testing.T) {
	reg := pwgRegistry(t, false)
	for _, typ := range []string{"post", "page", "", "product"} {
		t.Run("allowed/"+typ, func(t *testing.T) {
			f := newPwgFake(domain.Post{ID: 10, Type: typ, Title: "T", Slug: "t", Status: "publish", Author: 1})
			svc := pwgService(t, f, reg)
			ctx := context.Background()
			if err := svc.Update(ctx, pwgAdmin(), domain.Post{ID: 10, Type: typ, Title: "T2", Slug: "t"}, time.Time{}); err != nil {
				t.Fatalf("Update err = %v, want nil", err)
			}
			if f.updates != 1 || f.snapshots != 1 {
				t.Fatalf("updates=%d snapshots=%d, want 1 and 1", f.updates, f.snapshots)
			}
			if err := svc.Delete(ctx, pwgAdmin(), domain.Post{ID: 10}); err != nil {
				t.Fatalf("Delete err = %v, want nil", err)
			}
			if f.deletes != 1 {
				t.Fatalf("deletes=%d, want 1", f.deletes)
			}
			if !svc.WritableType(typ) {
				t.Fatalf("service.WritableType(%q) = false, want true", typ)
			}
		})
		t.Run("capability denied/"+typ, func(t *testing.T) {
			f := newPwgFake(domain.Post{ID: 10, Type: typ, Title: "T", Slug: "t", Status: "publish", Author: 1})
			svc := pwgService(t, f, reg)
			nobody := auth.NewPrincipal(7, "nobody", nil)
			err := svc.Update(context.Background(), nobody, domain.Post{ID: 10, Type: typ, Title: "T2", Slug: "t"}, time.Time{})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("Update err = %v, want ErrForbidden", err)
			}
			if f.writes() != 0 {
				t.Fatalf("writer called %d times, want 0", f.writes())
			}
		})
	}
}

func TestPostWriteGate_WritableTypeHelper(t *testing.T) {
	withProduct := pwgRegistry(t, false)
	cases := []struct {
		name string
		reg  *registry.Registry
		typ  string
		want bool
	}{
		{"empty", withProduct, "", true},
		{"post", withProduct, "post", true},
		{"page", withProduct, "page", true},
		{"registered", withProduct, "product", true},
		{"absent", withProduct, "shop_order", false},
		{"registered elsewhere only", withProduct, "guide", false},
		{"nil registry empty", nil, "", true},
		{"nil registry post", nil, "post", true},
		{"nil registry page", nil, "page", true},
		{"nil registry product", nil, "product", false},
		{"nil registry shop_order", nil, "shop_order", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WritableType(tc.reg, tc.typ); got != tc.want {
				t.Fatalf("WritableType(%q) = %v, want %v", tc.typ, got, tc.want)
			}
			svc := NewPostWriteService(newPwgFake(), WithPostRegistry(tc.reg))
			if got := svc.WritableType(tc.typ); got != tc.want {
				t.Fatalf("PostWriteService.WritableType(%q) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}
