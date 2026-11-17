package content

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

// --- Plan 19 task 2.1 (RED): taxonomy gate on term update/delete and on
// post-term assignment (Req 1.1-1.3, 1.5-1.7, 2.1, 2.2, 2.4-2.6; D1, D2) ------
//
// Written failing-first. ManagedTaxonomy does not exist yet, so this file does
// not compile until task 2.2. Once a stub exists, Update/Delete of a nav_menu
// term succeed and SetPostTerms forwards nav_menu, which the tests below catch.
//
// Helper prefix: tgate (unique to this file).

// tgateTerms is an in-memory combined TermWriter+TermReader that records every
// read and write, and can fail reads.
type tgateTerms struct {
	byID    map[int64]domain.Term
	reads   int
	writes  []string // "create", "update:<id>:<taxonomy>", "delete:<id>"
	readErr error
}

func (f *tgateTerms) Create(_ context.Context, t domain.Term) (int64, error) {
	f.writes = append(f.writes, "create")
	return 99, nil
}

func (f *tgateTerms) Update(_ context.Context, t domain.Term) error {
	f.writes = append(f.writes, "update:"+itoaTgate(t.ID)+":"+t.Taxonomy)
	return nil
}

func (f *tgateTerms) Delete(_ context.Context, id int64) error {
	f.writes = append(f.writes, "delete:"+itoaTgate(id))
	return nil
}

func (f *tgateTerms) ListByTaxonomy(_ context.Context, taxonomy string) ([]domain.Term, error) {
	var out []domain.Term
	for _, t := range f.byID {
		if t.Taxonomy == taxonomy {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *tgateTerms) TermsByIDs(_ context.Context, ids []int64) ([]domain.Term, error) {
	f.reads++
	if f.readErr != nil {
		return nil, f.readErr
	}
	var out []domain.Term
	for _, id := range ids {
		if t, ok := f.byID[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func itoaTgate(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// Stored taxonomies the gate must refuse. "genre" is registered in
// tgateRegistry but absent from tgateEmptyRegistry.
var tgateUnmanaged = []string{"nav_menu", "link_category", "product_cat", "post_format"}

func tgateEmptyRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	reg, err := registry.Build(config.ContentConfig{}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

func tgateSeed() *tgateTerms {
	return &tgateTerms{byID: map[int64]domain.Term{
		10: {ID: 10, Name: "Menu", Slug: "menu", Taxonomy: "nav_menu"},
		11: {ID: 11, Name: "Links", Slug: "links", Taxonomy: "link_category"},
		12: {ID: 12, Name: "Shirts", Slug: "shirts", Taxonomy: "product_cat"},
		13: {ID: 13, Name: "Aside", Slug: "aside", Taxonomy: "post_format"},
		14: {ID: 14, Name: "Rock", Slug: "rock", Taxonomy: "genre"},
		20: {ID: 20, Name: "News", Slug: "news", Taxonomy: "category"},
		21: {ID: 21, Name: "Go", Slug: "go", Taxonomy: "post_tag"},
	}}
}

func tgateSvc(f *tgateTerms, reg *registry.Registry) *TermWriteService {
	if reg == nil {
		return NewTermWriteService(f)
	}
	return NewTermWriteService(f, WithTermRegistry(reg))
}

// --- ManagedTaxonomy (D1) ----------------------------------------------------

func TestTgateManagedTaxonomy(t *testing.T) {
	reg := ptcRegistry(t) // registers genre and badge
	for _, tc := range []struct {
		name     string
		reg      *registry.Registry
		taxonomy string
		want     bool
	}{
		{"category", reg, "category", true},
		{"post_tag", reg, "post_tag", true},
		{"registered", reg, "genre", true},
		{"absent", reg, "nav_menu", false},
		{"empty string", reg, "", false},
		{"nil registry category", nil, "category", true},
		{"nil registry post_tag", nil, "post_tag", true},
		{"nil registry registered name", nil, "genre", false},
		{"nil registry empty", nil, "", false},
	} {
		if got := ManagedTaxonomy(tc.reg, tc.taxonomy); got != tc.want {
			t.Errorf("%s: ManagedTaxonomy(%q) = %v, want %v", tc.name, tc.taxonomy, got, tc.want)
		}
	}
}

// --- TermWriteService.Update / Delete (Req 1.1-1.3, 1.5-1.7, 2.1) ------------

func TestTgateTermUpdateAndDeleteRefuseUnmanaged(t *testing.T) {
	ctx := context.Background()
	for _, regName := range []string{"nil", "registry"} {
		var reg *registry.Registry
		if regName == "registry" {
			reg = ptcRegistry(t)
		}
		for _, id := range []int64{10, 11, 12, 13} {
			f := tgateSeed()
			svc := tgateSvc(f, reg)
			err := svc.Update(ctx, gate3Admin(), domain.Term{ID: id, Name: "Renamed", Slug: "renamed"})
			if !errors.Is(err, ErrForbidden) {
				t.Errorf("reg=%s Update(%d) err = %v, want ErrForbidden", regName, id, err)
			}
			err = svc.Delete(ctx, gate3Admin(), id)
			if !errors.Is(err, ErrForbidden) {
				t.Errorf("reg=%s Delete(%d) err = %v, want ErrForbidden", regName, id, err)
			}
			if len(f.writes) != 0 {
				t.Errorf("reg=%s id=%d: writer saw %v, want no writes", regName, id, f.writes)
			}
		}
	}
}

// The stored taxonomy wins over the caller's t.Taxonomy (Req 1.1).
func TestTgateTermUpdateStoredTaxonomyWins(t *testing.T) {
	f := tgateSeed()
	err := tgateSvc(f, nil).Update(context.Background(), gate3Admin(),
		domain.Term{ID: 10, Name: "Renamed", Slug: "renamed", Taxonomy: "category"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Update claiming category for a nav_menu term: err = %v, want ErrForbidden", err)
	}
	if len(f.writes) != 0 {
		t.Errorf("writes = %v, want none", f.writes)
	}
}

// A taxonomy registered elsewhere but absent from this registry is unmanaged.
func TestTgateTermRegisteredElsewhereIsUnmanaged(t *testing.T) {
	f := tgateSeed()
	svc := tgateSvc(f, tgateEmptyRegistry(t))
	if err := svc.Update(context.Background(), gate3Admin(), domain.Term{ID: 14, Name: "R", Slug: "r"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("Update genre without registration: err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(context.Background(), gate3Admin(), 14); !errors.Is(err, ErrForbidden) {
		t.Errorf("Delete genre without registration: err = %v, want ErrForbidden", err)
	}
	if len(f.writes) != 0 {
		t.Errorf("writes = %v, want none", f.writes)
	}
}

func TestTgateTermDeleteMissingIDIsNotFound(t *testing.T) {
	f := tgateSeed()
	err := tgateSvc(f, nil).Delete(context.Background(), gate3Admin(), 404)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete missing: err = %v, want domain.ErrNotFound", err)
	}
	if len(f.writes) != 0 {
		t.Errorf("writes = %v, want none", f.writes)
	}
}

// Without manage_categories every term is ErrForbidden and nothing is read.
func TestTgateTermWithoutCapabilityForbiddenNoRead(t *testing.T) {
	contrib := auth.NewPrincipal(2, "c", []string{"edit_posts"})
	for _, id := range []int64{10, 20, 21, 14} {
		f := tgateSeed()
		svc := tgateSvc(f, ptcRegistry(t))
		if err := svc.Update(context.Background(), contrib, domain.Term{ID: id, Name: "X", Slug: "x"}); !errors.Is(err, ErrForbidden) {
			t.Errorf("Update(%d) err = %v, want ErrForbidden", id, err)
		}
		if err := svc.Delete(context.Background(), contrib, id); !errors.Is(err, ErrForbidden) {
			t.Errorf("Delete(%d) err = %v, want ErrForbidden", id, err)
		}
		if f.reads != 0 || len(f.writes) != 0 {
			t.Errorf("id=%d: reads=%d writes=%v, want none", id, f.reads, f.writes)
		}
	}
}

// category, post_tag and a registered taxonomy behave as before (Req 1.6).
func TestTgateTermManagedUnchanged(t *testing.T) {
	ctx := context.Background()
	f := tgateSeed()
	svc := tgateSvc(f, ptcRegistry(t))
	for _, id := range []int64{20, 21, 14} {
		if err := svc.Update(ctx, gate3Admin(), domain.Term{ID: id, Name: "Renamed", Slug: "renamed-" + itoaTgate(id)}); err != nil {
			t.Errorf("Update(%d): %v", id, err)
		}
		if err := svc.Delete(ctx, gate3Admin(), id); err != nil {
			t.Errorf("Delete(%d): %v", id, err)
		}
	}
	want := []string{
		"update:20:category", "delete:20",
		"update:21:post_tag", "delete:21",
		"update:14:genre", "delete:14",
	}
	if !reflect.DeepEqual(f.writes, want) {
		t.Errorf("writes = %v, want %v", f.writes, want)
	}
}

// Update reads the stored term exactly once; a read error writes nothing.
func TestTgateTermUpdateReadsOnceAndReadErrorWritesNothing(t *testing.T) {
	f := tgateSeed()
	if err := tgateSvc(f, nil).Update(context.Background(), gate3Admin(), domain.Term{ID: 20, Name: "N", Slug: "n"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if f.reads != 1 {
		t.Errorf("Update reads = %d, want 1", f.reads)
	}

	boom := errors.New("read failed")
	g := tgateSeed()
	g.readErr = boom
	svc := tgateSvc(g, nil)
	if err := svc.Update(context.Background(), gate3Admin(), domain.Term{ID: 20, Name: "N", Slug: "n"}); !errors.Is(err, boom) {
		t.Errorf("Update read error: err = %v, want %v", err, boom)
	}
	if err := svc.Delete(context.Background(), gate3Admin(), 20); !errors.Is(err, boom) {
		t.Errorf("Delete read error: err = %v, want %v", err, boom)
	}
	if len(g.writes) != 0 {
		t.Errorf("writes after read error = %v, want none", g.writes)
	}
}

// Create of an unmanaged taxonomy is ErrTaxonomyUnknown (Req 2.1).
func TestTgateTermCreateUnmanagedIsTaxonomyUnknown(t *testing.T) {
	for _, tax := range tgateUnmanaged {
		f := tgateSeed()
		_, err := tgateSvc(f, nil).Create(context.Background(), gate3Admin(),
			domain.Term{Name: "X", Slug: "x", Taxonomy: tax})
		if !errors.Is(err, ErrTaxonomyUnknown) {
			t.Errorf("Create(%s) err = %v, want ErrTaxonomyUnknown", tax, err)
		}
		if len(f.writes) != 0 {
			t.Errorf("Create(%s) writes = %v, want none", tax, f.writes)
		}
	}
}

// --- PostTermsWriteService.SetPostTerms (Req 2.2, 2.4, 2.5) -------------------

// tgateAdminProducts is an administrator that also holds the product
// capability family. The built-in administrator role has no *_products caps, so
// gate3Admin() alone is refused by the capability check on a product row,
// which Req 2.5 orders before the taxonomy gate.
func tgateAdminProducts() auth.Principal {
	return auth.NewPrincipal(1, "u", []string{auth.RoleAdministrator,
		"edit_products", "edit_others_products", "edit_published_products", "edit_private_products",
		"publish_products", "delete_products", "delete_others_products", "delete_published_products",
		"delete_private_products"})
}

func tgatePostTerms(f *ptcFake, reg *registry.Registry, withReader bool) *PostTermsWriteService {
	opts := []PostTermsOption{WithPostTermsRegistry(reg)}
	if withReader {
		opts = append(opts, WithPostTermsReader(f))
	}
	return NewPostTermsWriteService(f, f, opts...)
}

func TestTgateSetPostTermsRefusesUnmanagedTaxonomy(t *testing.T) {
	ctx := context.Background()
	f := newPtcFake(
		domain.Post{ID: 1, Type: "post", Status: "draft", Author: 5},
		domain.Post{ID: 2, Type: "page", Status: "draft", Author: 5},
		ptcProduct(4, 5),
	)
	svc := tgatePostTerms(f, ptcRegistry(t), true)
	for _, id := range []int64{1, 2, 4} {
		for _, tax := range tgateUnmanaged {
			for _, ids := range [][]int64{nil, {}, {1}} {
				err := svc.SetPostTerms(ctx, tgateAdminProducts(), id, tax, ids)
				if !errors.Is(err, ErrTaxonomyUnknown) {
					t.Errorf("post=%d tax=%s ids=%v err = %v, want ErrTaxonomyUnknown", id, tax, ids, err)
				}
			}
		}
	}
	if len(f.sets) != 0 {
		t.Errorf("PostTermsWriter saw %+v, want no writes", f.sets)
	}
}

func TestTgateSetPostTermsRegisteredElsewhereIsUnmanaged(t *testing.T) {
	f := newPtcFake(domain.Post{ID: 1, Type: "post", Status: "draft", Author: 5})
	svc := tgatePostTerms(f, tgateEmptyRegistry(t), true)
	err := svc.SetPostTerms(context.Background(), gate3Admin(), 1, "genre", []int64{1})
	if !errors.Is(err, ErrTaxonomyUnknown) {
		t.Errorf("err = %v, want ErrTaxonomyUnknown", err)
	}
	if len(f.sets) != 0 {
		t.Errorf("writes = %+v, want none", f.sets)
	}
}

// Nil registry: the same unmanaged taxonomies are refused (Req 1.7 analogue).
func TestTgateSetPostTermsNilRegistryRefusesUnmanaged(t *testing.T) {
	f := newPtcFake(domain.Post{ID: 1, Type: "post", Status: "draft", Author: 5})
	svc := tgatePostTerms(f, nil, true)
	for _, tax := range tgateUnmanaged {
		err := svc.SetPostTerms(context.Background(), gate3Admin(), 1, tax, nil)
		if !errors.Is(err, ErrTaxonomyUnknown) {
			t.Errorf("tax=%s err = %v, want ErrTaxonomyUnknown", tax, err)
		}
	}
	if len(f.sets) != 0 {
		t.Errorf("writes = %+v, want none", f.sets)
	}
}

// Type gate and capability check run before the taxonomy gate (Req 2.5).
func TestTgateSetPostTermsAuthRunsBeforeTaxonomyGate(t *testing.T) {
	ctx := context.Background()
	f := newPtcFake(
		domain.Post{ID: 1, Type: "post", Status: "publish", Author: 9},
		domain.Post{ID: 3, Type: "shop_order", Status: "draft", Author: 5},
	)
	svc := tgatePostTerms(f, ptcRegistry(t), true)

	// Unwritable stored type: ErrForbidden, not ErrTaxonomyUnknown.
	if err := svc.SetPostTerms(ctx, gate3Admin(), 3, "nav_menu", []int64{1}); !errors.Is(err, ErrForbidden) {
		t.Errorf("unwritable row: err = %v, want ErrForbidden", err)
	}
	// Caller cannot edit another author's published post: ErrForbidden.
	author := ptcActor(5, "edit_posts")
	if err := svc.SetPostTerms(ctx, author, 1, "nav_menu", []int64{1}); !errors.Is(err, ErrForbidden) {
		t.Errorf("unauthorized caller: err = %v, want ErrForbidden", err)
	}
	if len(f.sets) != 0 {
		t.Errorf("writes = %+v, want none", f.sets)
	}
}

// Built-in rows check term membership when a reader is configured (Req 2.4).
func TestTgateSetPostTermsBuiltinMembership(t *testing.T) {
	ctx := context.Background()
	f := newPtcFake(domain.Post{ID: 1, Type: "post", Status: "draft", Author: 5})
	svc := tgatePostTerms(f, ptcRegistry(t), true)

	// Term 1 belongs to genre, not category.
	if err := svc.SetPostTerms(ctx, gate3Admin(), 1, "category", []int64{1}); !errors.Is(err, ErrTermInvalid) {
		t.Errorf("foreign term: err = %v, want ErrTermInvalid", err)
	}
	if len(f.sets) != 0 {
		t.Fatalf("writes after invalid id = %+v, want none", f.sets)
	}
	// Term 4 is a category.
	if err := svc.SetPostTerms(ctx, gate3Admin(), 1, "category", []int64{4}); err != nil {
		t.Errorf("valid term: %v", err)
	}
	want := []ptcSet{{1, "category", []int64{4}}}
	if !reflect.DeepEqual(f.sets, want) {
		t.Errorf("sets = %+v, want %+v", f.sets, want)
	}
}

// With no term reader configured, built-in assignment behaves as before.
func TestTgateSetPostTermsBuiltinNoReaderUnchanged(t *testing.T) {
	f := newPtcFake(domain.Post{ID: 1, Type: "post", Status: "draft", Author: 5})
	svc := tgatePostTerms(f, ptcRegistry(t), false)
	if err := svc.SetPostTerms(context.Background(), gate3Admin(), 1, "category", []int64{1}); err != nil {
		t.Fatalf("SetPostTerms without reader: %v", err)
	}
	want := []ptcSet{{1, "category", []int64{1}}}
	if !reflect.DeepEqual(f.sets, want) {
		t.Errorf("sets = %+v, want %+v", f.sets, want)
	}
}

// --- PostWriteService.Delete still detaches only managed taxonomies (Req 2.6) --

type tgateDetacher struct{ taxonomies []string }

func (d *tgateDetacher) SetPostTerms(_ context.Context, _ int64, tax string, ids []int64) error {
	if len(ids) != 0 {
		return errors.New("detach must pass no term ids")
	}
	d.taxonomies = append(d.taxonomies, tax)
	return nil
}

func TestTgatePostDeleteDetachesOnlyManagedTaxonomies(t *testing.T) {
	reg := ptcRegistry(t)
	for _, tc := range []struct {
		typ  string
		want []string
	}{
		{"post", []string{"category", "post_tag"}},
		{"page", []string{"category", "post_tag"}},
		{"product", []string{"badge", "genre"}},
	} {
		w := &fakePostWriter{store: map[int64]domain.Post{
			9: {ID: 9, Author: 5, Type: tc.typ, Status: "publish"},
		}}
		d := &tgateDetacher{}
		svc := NewPostWriteService(w, WithPostRegistry(reg), WithTermDetacher(d))
		if err := svc.Delete(context.Background(), tgateAdminProducts(), domain.Post{ID: 9}); err != nil {
			t.Fatalf("%s: delete: %v", tc.typ, err)
		}
		got := append([]string(nil), d.taxonomies...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: detached %v, want %v", tc.typ, got, tc.want)
		}
		for _, tax := range got {
			if !ManagedTaxonomy(reg, tax) {
				t.Errorf("%s: detached unmanaged taxonomy %q", tc.typ, tax)
			}
		}
	}
}
