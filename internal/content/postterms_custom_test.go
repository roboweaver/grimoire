package content

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

// --- M17: PostTermsWriteService guards for registered types (task 15.1, RED) --
//
// Written failing-first. They reference symbols task 15.2 adds that do not yet
// exist: the variadic NewPostTermsWriteService options WithPostTermsRegistry and
// WithPostTermsReader, CheckAssignable, ErrTaxonomyNotAttached and
// ErrTermInvalid. Until 15.2 lands this file fails to compile.
//
// Registry: product (capability_type product, taxonomies genre+badge), note
// (capability_type post, no taxonomies). Unregistered stored type: shop_order.
// Terms: 1,2 in genre; 3 in badge; 4 in category; id 99 does not exist.

// ptcFake is an in-memory postByID, PostTermsWriter and TermReader that logs
// every call so tests can assert read/write ordering.
type ptcFake struct {
	posts map[int64]domain.Post
	terms map[int64]domain.Term
	log   []string
	sets  []ptcSet
}

type ptcSet struct {
	postID   int64
	taxonomy string
	ids      []int64
}

func newPtcFake(posts ...domain.Post) *ptcFake {
	f := &ptcFake{posts: map[int64]domain.Post{}, terms: map[int64]domain.Term{
		1: {ID: 1, Name: "Rock", Slug: "rock", Taxonomy: "genre"},
		2: {ID: 2, Name: "Jazz", Slug: "jazz", Taxonomy: "genre"},
		3: {ID: 3, Name: "New", Slug: "new", Taxonomy: "badge"},
		4: {ID: 4, Name: "News", Slug: "news", Taxonomy: "category"},
	}}
	for _, p := range posts {
		f.posts[p.ID] = p
	}
	return f
}

func (f *ptcFake) ByID(_ context.Context, id int64) (domain.Post, error) {
	f.log = append(f.log, "byid")
	p, ok := f.posts[id]
	if !ok {
		return domain.Post{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *ptcFake) SetPostTerms(_ context.Context, postID int64, taxonomy string, ids []int64) error {
	f.log = append(f.log, "set")
	f.sets = append(f.sets, ptcSet{postID, taxonomy, append([]int64(nil), ids...)})
	return nil
}

func (f *ptcFake) ListByTaxonomy(_ context.Context, taxonomy string) ([]domain.Term, error) {
	f.log = append(f.log, "list")
	return nil, nil
}

func (f *ptcFake) TermsByIDs(_ context.Context, ids []int64) ([]domain.Term, error) {
	f.log = append(f.log, "terms")
	var out []domain.Term
	for _, id := range ids {
		if t, ok := f.terms[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func ptcRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	yes := true
	reg, err := registry.Build(config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "product", Public: &yes, Slug: "product", WithFront: &yes,
				CapabilityType: "product", Taxonomies: []string{"genre", "badge"}},
			{Name: "note", Public: &yes, Slug: "note", WithFront: &yes,
				CapabilityType: "post"},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", WithFront: &yes,
				Hierarchical: true, ObjectTypes: []string{"product"}},
			{Name: "badge", Public: &yes, Slug: "badge", WithFront: &yes,
				ObjectTypes: []string{"product"}},
		},
	}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

func ptcService(t *testing.T, f *ptcFake) *PostTermsWriteService {
	t.Helper()
	return NewPostTermsWriteService(f, f,
		WithPostTermsRegistry(ptcRegistry(t)),
		WithPostTermsReader(f))
}

func ptcActor(id int64, caps ...string) auth.Principal {
	return auth.NewPrincipal(id, "u", caps)
}

var (
	ptcProdAuthor = []string{"edit_products", "publish_products", "edit_published_products"}
	ptcProdEditor = append(append([]string{}, ptcProdAuthor...), "edit_others_products", "edit_private_products")
	ptcPostsOnly  = []string{"edit_posts", "publish_posts", "edit_others_posts", "edit_published_posts", "manage_categories"}
)

func ptcProduct(id, author int64) domain.Post {
	return domain.Post{ID: id, Type: "product", Status: "draft", Author: author}
}

// --- 12.1: registered row, attached taxonomy, member ids writes --------------

func TestPostTermsCustom_AttachedTaxonomyWrites(t *testing.T) {
	f := newPtcFake(ptcProduct(10, 5))
	err := ptcService(t, f).SetPostTerms(context.Background(), ptcActor(5, ptcProdAuthor...), 10, "genre", []int64{1, 2})
	if err != nil {
		t.Fatalf("SetPostTerms: %v", err)
	}
	want := []ptcSet{{10, "genre", []int64{1, 2}}}
	if !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("sets = %+v, want %+v", f.sets, want)
	}
}

// --- 12.4: taxonomy not attached to the type ---------------------------------

func TestPostTermsCustom_UnattachedTaxonomyRejected(t *testing.T) {
	cases := []struct {
		name string
		post domain.Post
		tax  string
		ids  []int64
	}{
		{"built-in category on product", ptcProduct(10, 5), "category", []int64{4}},
		{"registered taxonomy on type without it", domain.Post{ID: 11, Type: "note", Status: "draft", Author: 5}, "genre", []int64{1}},
		{"unknown taxonomy on product", ptcProduct(10, 5), "nosuch", []int64{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPtcFake(tc.post)
			err := ptcService(t, f).SetPostTerms(context.Background(),
				ptcActor(5, append(append([]string{}, ptcProdAuthor...), "edit_posts")...), tc.post.ID, tc.tax, tc.ids)
			// M19 Req 2.2: an unmanaged name is refused by the taxonomy gate
			// before the attachment check, with ErrTaxonomyUnknown (also 400).
			want := ErrTaxonomyNotAttached
			if tc.tax == "nosuch" {
				want = ErrTaxonomyUnknown
			}
			if !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
			if len(f.sets) != 0 {
				t.Fatalf("wrote %+v, want nothing", f.sets)
			}
		})
	}
}

// --- 12.3: ids that are not terms of the taxonomy ----------------------------

func TestPostTermsCustom_InvalidIDsRejected(t *testing.T) {
	cases := []struct {
		name string
		ids  []int64
	}{
		{"id of another taxonomy", []int64{1, 3}},
		{"nonexistent id", []int64{1, 99}},
		{"only nonexistent id", []int64{99}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPtcFake(ptcProduct(10, 5))
			err := ptcService(t, f).SetPostTerms(context.Background(), ptcActor(5, ptcProdAuthor...), 10, "genre", tc.ids)
			if !errors.Is(err, ErrTermInvalid) {
				t.Fatalf("err = %v, want ErrTermInvalid", err)
			}
			if len(f.sets) != 0 {
				t.Fatalf("wrote %+v, want nothing", f.sets)
			}
		})
	}
}

// --- empty ids clears the taxonomy -------------------------------------------

func TestPostTermsCustom_EmptyIDsClears(t *testing.T) {
	f := newPtcFake(ptcProduct(10, 5))
	if err := ptcService(t, f).SetPostTerms(context.Background(), ptcActor(5, ptcProdAuthor...), 10, "badge", nil); err != nil {
		t.Fatalf("SetPostTerms: %v", err)
	}
	if len(f.sets) != 1 || f.sets[0].postID != 10 || f.sets[0].taxonomy != "badge" || len(f.sets[0].ids) != 0 {
		t.Fatalf("sets = %+v, want one empty write to badge on 10", f.sets)
	}
}

// --- 12.5: capability is the post's edit capability under the type's suffix --

func TestPostTermsCustom_CapabilityIsTypeSuffixNotManageCategories(t *testing.T) {
	cases := []struct {
		name   string
		caps   []string
		author int64
		want   bool
	}{
		{"edit_products on own draft", ptcProdAuthor, 5, true},
		{"edit_posts + manage_categories only", ptcPostsOnly, 5, false},
		{"edit_products on another's draft", ptcProdAuthor, 99, false},
		{"edit_others_products on another's draft", ptcProdEditor, 99, true},
		{"no capabilities", nil, 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPtcFake(ptcProduct(10, tc.author))
			err := ptcService(t, f).SetPostTerms(context.Background(), ptcActor(5, tc.caps...), 10, "genre", []int64{1})
			if tc.want {
				if err != nil || len(f.sets) != 1 {
					t.Fatalf("err = %v sets = %+v, want a write", err, f.sets)
				}
				return
			}
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if len(f.sets) != 0 {
				t.Fatalf("wrote %+v, want nothing", f.sets)
			}
		})
	}
}

// --- CheckAssignable: would-be draft authored by the actor -------------------

func TestPostTermsCustom_CheckAssignableAppliesSameRules(t *testing.T) {
	ctx := context.Background()
	actor := ptcActor(5, ptcProdAuthor...)

	t.Run("valid", func(t *testing.T) {
		f := newPtcFake()
		if err := ptcService(t, f).CheckAssignable(ctx, actor, "product", "genre", []int64{1, 2}); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(f.sets) != 0 {
			t.Fatalf("CheckAssignable wrote %+v, want no writes", f.sets)
		}
	})
	t.Run("empty ids valid", func(t *testing.T) {
		if err := ptcService(t, newPtcFake()).CheckAssignable(ctx, actor, "product", "genre", nil); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
	t.Run("not attached", func(t *testing.T) {
		err := ptcService(t, newPtcFake()).CheckAssignable(ctx, actor, "product", "category", []int64{4})
		if !errors.Is(err, ErrTaxonomyNotAttached) {
			t.Fatalf("err = %v, want ErrTaxonomyNotAttached", err)
		}
	})
	t.Run("wrong-taxonomy id", func(t *testing.T) {
		err := ptcService(t, newPtcFake()).CheckAssignable(ctx, actor, "product", "genre", []int64{3})
		if !errors.Is(err, ErrTermInvalid) {
			t.Fatalf("err = %v, want ErrTermInvalid", err)
		}
	})
	t.Run("nonexistent id", func(t *testing.T) {
		err := ptcService(t, newPtcFake()).CheckAssignable(ctx, actor, "product", "genre", []int64{99})
		if !errors.Is(err, ErrTermInvalid) {
			t.Fatalf("err = %v, want ErrTermInvalid", err)
		}
	})
	t.Run("forbidden before any read", func(t *testing.T) {
		f := newPtcFake()
		err := ptcService(t, f).CheckAssignable(ctx, ptcActor(5, ptcPostsOnly...), "product", "genre", []int64{1})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if len(f.log) != 0 {
			t.Fatalf("calls before ErrForbidden = %v, want none", f.log)
		}
	})
	t.Run("forbidden outranks invalid taxonomy", func(t *testing.T) {
		f := newPtcFake()
		err := ptcService(t, f).CheckAssignable(ctx, ptcActor(5), "product", "category", []int64{99})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if len(f.log) != 0 {
			t.Fatalf("calls before ErrForbidden = %v, want none", f.log)
		}
	})
	t.Run("would-be draft needs no publish capability", func(t *testing.T) {
		// edit_products alone covers a draft authored by the actor.
		if err := ptcService(t, newPtcFake()).CheckAssignable(ctx, ptcActor(5, "edit_products"), "product", "genre", []int64{1}); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
}

// --- 12.7: post/page/unregistered rows behave exactly as before --------------

func TestPostTermsCustom_BuiltinAndUnregisteredRowsUnchanged(t *testing.T) {
	cases := []struct {
		name string
		post domain.Post
		caps []string
	}{
		{"post", domain.Post{ID: 20, Type: "post", Status: "draft", Author: 5}, []string{"edit_posts"}},
		{"empty type is post", domain.Post{ID: 21, Type: "", Status: "draft", Author: 5}, []string{"edit_posts"}},
		{"page", domain.Post{ID: 22, Type: "page", Status: "draft", Author: 5}, []string{"edit_pages"}},
		// M18 Req 2.1 supersedes M17 here: an unregistered stored type is
		// unwritable and SetPostTerms refuses it (see postterms_gate_test.go).
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A managed taxonomy attached to nothing passes straight through:
			// no attachment check on built-in rows. M19 Req 2.4 adds a
			// membership check when a term reader is configured, so the ids
			// are terms of genre (foreign ids are covered in termgate_test.go).
			f := newPtcFake(tc.post)
			err := ptcService(t, f).SetPostTerms(context.Background(), ptcActor(5, tc.caps...), tc.post.ID, "genre", []int64{1, 2})
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(f.sets) != 1 || !reflect.DeepEqual(f.sets[0].ids, []int64{1, 2}) {
				t.Fatalf("sets = %+v, want the ids written verbatim", f.sets)
			}
			for _, c := range f.log {
				if c == "list" {
					t.Fatalf("taxonomy listed for built-in row: %v", f.log)
				}
			}
		})
	}
}

func TestPostTermsCustom_BuiltinRowStillGatedByPostCapability(t *testing.T) {
	f := newPtcFake(domain.Post{ID: 20, Type: "post", Status: "draft", Author: 5})
	// edit_products must not authorize a built-in post.
	err := ptcService(t, f).SetPostTerms(context.Background(), ptcActor(5, ptcProdAuthor...), 20, "category", []int64{4})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestPostTermsCustom_MissingPostIsForbidden(t *testing.T) {
	err := ptcService(t, newPtcFake()).SetPostTerms(context.Background(), ptcActor(5, ptcProdAuthor...), 404, "genre", []int64{1})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}
