package content

import (
	"context"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/roboweaver/grimoire/internal/domain"
)

// pruneItem builds a menu item; a negative id marks it TargetTrashed (id = -id).
func pruneItem(id int64, children ...domain.NavMenuItem) domain.NavMenuItem {
	it := domain.NavMenuItem{ID: id, Label: "item", Children: children}
	if id < 0 {
		it.ID = -id
		it.TargetTrashed = true
	}
	return it
}

// pruneShape renders ids with nesting, e.g. [1 [2 3] 4], so trees compare readably.
func pruneShape(items []domain.NavMenuItem) any {
	out := []any{}
	for _, it := range items {
		if len(it.Children) == 0 {
			out = append(out, it.ID)
			continue
		}
		out = append(out, []any{it.ID, pruneShape(it.Children)})
	}
	return out
}

func TestPruneInvalidItemsTrees(t *testing.T) {
	cases := []struct {
		name string
		in   []domain.NavMenuItem
		want []domain.NavMenuItem
	}{
		{"none removed", []domain.NavMenuItem{pruneItem(1, pruneItem(2)), pruneItem(3)}, []domain.NavMenuItem{pruneItem(1, pruneItem(2)), pruneItem(3)}},
		{"top-level removed keeps its children at top level in place",
			[]domain.NavMenuItem{pruneItem(1), pruneItem(-2, pruneItem(3), pruneItem(4)), pruneItem(5)},
			[]domain.NavMenuItem{pruneItem(1), pruneItem(3), pruneItem(4), pruneItem(5)}},
		{"nested removed re-parents children to the grandparent in place",
			[]domain.NavMenuItem{pruneItem(1, pruneItem(2), pruneItem(-3, pruneItem(4), pruneItem(5)), pruneItem(6))},
			[]domain.NavMenuItem{pruneItem(1, pruneItem(2), pruneItem(4), pruneItem(5), pruneItem(6))}},
		{"a chain of removed items",
			[]domain.NavMenuItem{pruneItem(1, pruneItem(-2, pruneItem(-3, pruneItem(4))))},
			[]domain.NavMenuItem{pruneItem(1, pruneItem(4))}},
		{"all removed", []domain.NavMenuItem{pruneItem(-1, pruneItem(-2)), pruneItem(-3)}, []domain.NavMenuItem{}},
		{"children order is kept",
			[]domain.NavMenuItem{pruneItem(-1, pruneItem(2), pruneItem(3), pruneItem(4))},
			[]domain.NavMenuItem{pruneItem(2), pruneItem(3), pruneItem(4)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pruneInvalidItems(c.in)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(pruneShape(got), pruneShape(c.want)) {
				t.Fatalf("pruned = %v, want %v", pruneShape(got), pruneShape(c.want))
			}
			for _, it := range pruneFlatten(got) {
				if it.TargetTrashed {
					t.Errorf("flagged item %d survived", it.ID)
				}
			}
		})
	}
}

func pruneFlatten(items []domain.NavMenuItem) []domain.NavMenuItem {
	var out []domain.NavMenuItem
	for _, it := range items {
		out = append(out, it)
		out = append(out, pruneFlatten(it.Children)...)
	}
	return out
}

// pruneGenTree builds a random tree with unique ids from a counter.
func pruneGenTree(rng *rand.Rand, next *int64, depth int) []domain.NavMenuItem {
	var out []domain.NavMenuItem
	for i := 0; i < rng.Intn(4); i++ {
		*next++
		it := domain.NavMenuItem{ID: *next, TargetTrashed: rng.Intn(3) == 0}
		if depth < 4 {
			it.Children = pruneGenTree(rng, next, depth+1)
		}
		out = append(out, it)
	}
	return out
}

// pruneExpected is the oracle: the nearest-unflagged-ancestor of every
// unflagged item, in preorder.
func pruneExpected(items []domain.NavMenuItem, parent int64, order *[]int64, parents map[int64]int64) {
	for _, it := range items {
		p := parent
		if !it.TargetTrashed {
			*order = append(*order, it.ID)
			parents[it.ID] = parent
			p = it.ID
		}
		pruneExpected(it.Children, p, order, parents)
	}
}

func pruneActualParents(items []domain.NavMenuItem, parent int64, order *[]int64, parents map[int64]int64) {
	for _, it := range items {
		*order = append(*order, it.ID)
		parents[it.ID] = parent
		pruneActualParents(it.Children, it.ID, order, parents)
	}
}

// TestPropertyPruneInvalidItems checks Property 8.
//
// Feature: m22-trash-and-restore, Property 8: Pruning trashed menu items keeps the rest in place
//
// Validates: Requirements 9.2
func TestPropertyPruneInvalidItems(t *testing.T) {
	prop := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		var next int64
		tree := pruneGenTree(rng, &next, 0)
		wantOrder, wantParents := []int64{}, map[int64]int64{}
		pruneExpected(tree, 0, &wantOrder, wantParents)

		got := pruneInvalidItems(tree)
		gotOrder, gotParents := []int64{}, map[int64]int64{}
		pruneActualParents(got, 0, &gotOrder, gotParents)

		if !reflect.DeepEqual(wantOrder, gotOrder) || !reflect.DeepEqual(wantParents, gotParents) {
			t.Logf("seed %d: order %v parents %v, want order %v parents %v", seed, gotOrder, gotParents, wantOrder, wantParents)
			return false
		}
		for _, it := range pruneFlatten(got) {
			if it.TargetTrashed {
				return false
			}
		}
		again := pruneInvalidItems(got)
		return reflect.DeepEqual(pruneShape(again), pruneShape(got))
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestNavMenuServicePrunesPublicReadsOnly(t *testing.T) {
	menu := domain.NavMenu{ID: 1, Items: []domain.NavMenuItem{pruneItem(1), pruneItem(-2, pruneItem(3)), pruneItem(4)}}
	repo := &fakeNavMenuRepo{
		menus:      []domain.NavMenu{menu},
		byLocation: map[string]domain.NavMenu{"primary": menu},
		bySlug:     map[string]domain.NavMenu{"primary": menu},
	}
	svc := NewNavMenuService(repo, "theme")
	ctx := context.Background()
	wantPruned := []any{int64(1), int64(3), int64(4)}

	byLoc, err := svc.ByLocation(ctx, "primary")
	if err != nil {
		t.Fatalf("ByLocation: %v", err)
	}
	if got := pruneShape(byLoc.Items); !reflect.DeepEqual(got, any(wantPruned)) {
		t.Errorf("ByLocation items = %v, want %v", got, wantPruned)
	}
	bySlug, err := svc.BySlug(ctx, "primary")
	if err != nil {
		t.Fatalf("BySlug: %v", err)
	}
	if got := pruneShape(bySlug.Items); !reflect.DeepEqual(got, any(wantPruned)) {
		t.Errorf("BySlug items = %v, want %v", got, wantPruned)
	}

	wantFull := pruneShape(menu.Items)
	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v %v", list, err)
	}
	if got := pruneShape(list[0].Items); !reflect.DeepEqual(got, wantFull) {
		t.Errorf("List must not prune: items = %v, want %v", got, wantFull)
	}
}

// pruneByIDRepo returns a menu from MenuByID so Get can be exercised.
type pruneByIDRepo struct {
	fakeNavMenuRepo
	menu domain.NavMenu
}

func (r *pruneByIDRepo) MenuByID(context.Context, int64) (domain.NavMenu, error) { return r.menu, nil }

func TestNavMenuServiceGetDoesNotPrune(t *testing.T) {
	menu := domain.NavMenu{ID: 1, Items: []domain.NavMenuItem{pruneItem(1), pruneItem(-2, pruneItem(3))}}
	svc := NewNavMenuService(&pruneByIDRepo{menu: menu}, "theme")
	got, err := svc.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := pruneShape(menu.Items); !reflect.DeepEqual(pruneShape(got.Items), want) {
		t.Errorf("Get must not prune: items = %v, want %v", pruneShape(got.Items), want)
	}
}
