package content

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

// --- M17: PostWriteService for registered types (task 8.1, RED) --------------
//
// Written failing-first. They reference symbols task 8.2 adds that do not yet
// exist: WithPostRegistry, WithPostNodeReader, WithTermDetacher, ErrSlugInvalid
// and Slugify. Until 8.2 lands this file fails to compile; once stubbed, an
// edit_posts holder can write a `product` (capability_type: product).
//
// Registry: product (flat, capability_type product, taxonomies genre+badge),
// guide (hierarchical, capability_type guide), note (flat, capability_type
// post). Unregistered stored type: shop_order.

// pwcFake is one in-memory PostWriter, PostNodeReader and PostTermsWriter that
// logs call order.
type pwcFake struct {
	domain.RevisionWriter // nil: only DeleteRevisionsOf is overridden

	store   map[int64]domain.Post
	nextID  int64
	log     []string
	creates []domain.Post
	updates []domain.Post
	deletes []int64

	nodeCalls int
	detachErr error
}

func newPwcFake(posts ...domain.Post) *pwcFake {
	f := &pwcFake{store: map[int64]domain.Post{}, nextID: 100}
	for _, p := range posts {
		f.store[p.ID] = p
	}
	return f
}

func (f *pwcFake) ByID(_ context.Context, id int64) (domain.Post, error) {
	p, ok := f.store[id]
	if !ok {
		return domain.Post{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *pwcFake) Create(_ context.Context, p domain.Post) (int64, error) {
	f.nextID++
	p.ID = f.nextID
	f.store[p.ID] = p
	f.creates = append(f.creates, p)
	f.log = append(f.log, "create")
	return p.ID, nil
}

func (f *pwcFake) Update(_ context.Context, p domain.Post) error {
	f.store[p.ID] = p
	f.updates = append(f.updates, p)
	f.log = append(f.log, "update")
	return nil
}

func (f *pwcFake) Delete(_ context.Context, id int64) error {
	delete(f.store, id)
	f.deletes = append(f.deletes, id)
	f.log = append(f.log, "row")
	return nil
}

func (f *pwcFake) DeleteRevisionsOf(_ context.Context, id int64) error {
	f.log = append(f.log, "revisions")
	return nil
}

func (f *pwcFake) SetPostTerms(_ context.Context, postID int64, taxonomy string, ids []int64) error {
	if f.detachErr != nil {
		return f.detachErr
	}
	if len(ids) != 0 {
		return errors.New("pwcFake: detach must pass no term ids")
	}
	f.log = append(f.log, "terms:"+taxonomy)
	return nil
}

func (f *pwcFake) ListPostNodes(_ context.Context, postType string, afterID int64, limit int) ([]domain.PostNode, error) {
	if afterID == 0 {
		f.nodeCalls++
	}
	var out []domain.PostNode
	for _, p := range f.store {
		if p.Type == postType && p.ID > afterID {
			out = append(out, domain.PostNode{ID: p.ID, Slug: p.Slug, ParentID: p.ParentID, Status: p.Status})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *pwcFake) writerCalls() int { return len(f.creates) + len(f.updates) + len(f.deletes) }

func pwcRegistry(t *testing.T) *registry.Registry {
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
			{Name: "guide", Public: &yes, Slug: "guide", WithFront: &yes,
				Hierarchical: true, CapabilityType: "guide"},
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

func pwcService(t *testing.T, f *pwcFake) *PostWriteService {
	t.Helper()
	return NewPostWriteService(f,
		WithPostRegistry(pwcRegistry(t)),
		WithPostNodeReader(f),
		WithTermDetacher(f))
}

// pwcActor builds a Principal holding exactly the listed capabilities.
func pwcActor(id int64, caps ...string) auth.Principal {
	return auth.NewPrincipal(id, "u", caps)
}

var (
	pwcPostsOnly = []string{"edit_posts", "publish_posts", "edit_others_posts",
		"edit_published_posts", "delete_posts", "delete_others_posts", "delete_published_posts"}
	pwcContributor = []string{"edit_products", "delete_products"}
	pwcProdAuthor  = []string{"edit_products", "publish_products", "edit_published_products",
		"delete_products", "delete_published_products"}
	pwcProdEditor = append(append([]string{}, pwcProdAuthor...),
		"edit_others_products", "delete_others_products", "edit_private_products", "delete_private_products")
)

// --- 7.1, 7.3: capability matrix over the *_products family -------------------

func TestPostWriteCustom_CreateCapabilityMatrix(t *testing.T) {
	cases := []struct {
		name   string
		caps   []string
		status string
		author int64 // 0 = actor
		want   bool
	}{
		{"edit_posts only, draft", pwcPostsOnly, "draft", 0, false},
		{"contributor draft own", pwcContributor, "draft", 0, true},
		{"contributor publish own", pwcContributor, "publish", 0, false},
		{"author publish own", pwcProdAuthor, "publish", 0, true},
		{"author draft for other", pwcProdAuthor, "draft", 99, false},
		{"editor draft for other", pwcProdEditor, "draft", 99, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPwcFake()
			svc := pwcService(t, f)
			_, err := svc.Create(context.Background(), pwcActor(5, tc.caps...),
				domain.Post{Type: "product", Title: "Widget", Status: tc.status, Author: tc.author})
			if tc.want {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if n := f.writerCalls(); n != 0 {
				t.Fatalf("writer called %d times on rejection", n)
			}
		})
	}
}

func TestPostWriteCustom_UpdateAndDeleteCapabilityMatrix(t *testing.T) {
	cases := []struct {
		name   string
		caps   []string
		status string
		author int64
		want   bool
	}{
		{"edit_posts only, own draft", pwcPostsOnly, "draft", 5, false},
		{"contributor own draft", pwcContributor, "draft", 5, true},
		{"contributor own published", pwcContributor, "publish", 5, false},
		{"author own published", pwcProdAuthor, "publish", 5, true},
		{"author other's draft", pwcProdAuthor, "draft", 99, false},
		{"editor other's draft", pwcProdEditor, "draft", 99, true},
		{"editor other's private", pwcProdEditor, "private", 99, true},
	}
	for _, tc := range cases {
		t.Run("update/"+tc.name, func(t *testing.T) {
			f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "w", Title: "W",
				Status: tc.status, Author: tc.author})
			svc := pwcService(t, f)
			err := svc.Update(context.Background(), pwcActor(5, tc.caps...),
				domain.Post{ID: 10, Type: "product", Title: "W2", Slug: "w"}, time.Time{})
			checkPwcDecision(t, f, err, tc.want)
		})
		t.Run("delete/"+tc.name, func(t *testing.T) {
			f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "w", Title: "W",
				Status: tc.status, Author: tc.author})
			svc := pwcService(t, f)
			err := svc.Delete(context.Background(), pwcActor(5, tc.caps...), domain.Post{ID: 10})
			checkPwcDecision(t, f, err, tc.want)
		})
	}
}

func checkPwcDecision(t *testing.T, f *pwcFake, err error, want bool) {
	t.Helper()
	if want {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if n := f.writerCalls(); n != 0 {
		t.Fatalf("writer called %d times on rejection", n)
	}
}

// 7.3: the capability is read from the stored row's type, not the request's.
func TestPostWriteCustom_UpdateUsesStoredType(t *testing.T) {
	f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "w", Title: "W", Status: "draft", Author: 5})
	svc := pwcService(t, f)
	// Request claims type "post"; actor holds only the *_posts family.
	err := svc.Update(context.Background(), pwcActor(5, pwcPostsOnly...),
		domain.Post{ID: 10, Type: "post", Title: "W2", Slug: "w"}, time.Time{})
	checkPwcDecision(t, f, err, false)
}

// 7.2: capability_type "post" decides exactly like built-in post.
func TestPostWriteCustom_CapabilityTypePostDecidesLikePost(t *testing.T) {
	f := newPwcFake()
	svc := pwcService(t, f)
	if _, err := svc.Create(context.Background(), pwcActor(5, "edit_posts"),
		domain.Post{Type: "note", Title: "N", Status: "draft"}); err != nil {
		t.Fatalf("edit_posts holder creating note: %v", err)
	}
	f2 := newPwcFake()
	svc2 := pwcService(t, f2)
	_, err := svc2.Create(context.Background(), pwcActor(5, "edit_notes"),
		domain.Post{Type: "note", Title: "N", Status: "draft"})
	checkPwcDecision(t, f2, err, false)
}

// 7.4 (M17), superseded by M18 Req 1.1: a stored type that is not registered
// is unwritable. Neither the legacy *_posts family nor a family derived from
// the type name authorizes an update.
func TestPostWriteCustom_UnregisteredStoredTypeUnwritable(t *testing.T) {
	row := domain.Post{ID: 10, Type: "shop_order", Slug: "o", Title: "O", Status: "draft", Author: 5}

	f := newPwcFake(row)
	err := pwcService(t, f).Update(context.Background(), pwcActor(5, "edit_posts"),
		domain.Post{ID: 10, Title: "O2", Slug: "o"}, time.Time{})
	checkPwcDecision(t, f, err, false)

	f2 := newPwcFake(row)
	err = pwcService(t, f2).Update(context.Background(), pwcActor(5, "edit_shop_orders"),
		domain.Post{ID: 10, Title: "O2", Slug: "o"}, time.Time{})
	checkPwcDecision(t, f2, err, false)
}

// --- 8.1-8.4: sanitization ----------------------------------------------------

const pwcDirty = `<script>alert(1)</script><p>hi</p>`

func TestPostWriteCustom_CreateSanitizesAtTierB(t *testing.T) {
	f := newPwcFake()
	svc := pwcService(t, f)
	_, err := svc.Create(context.Background(), pwcActor(5, pwcProdAuthor...), domain.Post{
		Type: "product", Status: "draft", Title: "<em>Big</em> Widget",
		Content: pwcDirty, Excerpt: pwcDirty,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := f.creates[0]
	if strings.Contains(got.Content, "<script") || strings.Contains(got.Excerpt, "<script") {
		t.Fatalf("script survived: content=%q excerpt=%q", got.Content, got.Excerpt)
	}
	if !strings.Contains(got.Content, "hi") {
		t.Fatalf("benign content lost: %q", got.Content)
	}
	if strings.Contains(got.Title, "<") {
		t.Fatalf("title tags survived: %q", got.Title)
	}
}

func TestPostWriteCustom_CreateTierCIsByteIdentical(t *testing.T) {
	f := newPwcFake()
	svc := pwcService(t, f)
	actor := pwcActor(5, append([]string{"unfiltered_html"}, pwcProdAuthor...)...)
	_, err := svc.Create(context.Background(), actor, domain.Post{
		Type: "product", Status: "draft", Title: "T", Content: pwcDirty, Excerpt: pwcDirty,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := f.creates[0]
	if got.Content != pwcDirty || got.Excerpt != pwcDirty {
		t.Fatalf("tier C altered input: content=%q excerpt=%q", got.Content, got.Excerpt)
	}
}

func TestPostWriteCustom_EmptyTitleAfterSanitizeRejected(t *testing.T) {
	f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "w", Title: "W", Status: "draft", Author: 5})
	svc := pwcService(t, f)
	actor := pwcActor(5, pwcProdAuthor...)

	if _, err := svc.Create(context.Background(), actor,
		domain.Post{Type: "product", Status: "draft", Title: "<em></em>"}); !errors.Is(err, ErrTitleEmpty) {
		t.Fatalf("Create err = %v, want ErrTitleEmpty", err)
	}
	if err := svc.Update(context.Background(), actor,
		domain.Post{ID: 10, Title: "<em></em>", Slug: "w"}, time.Time{}); !errors.Is(err, ErrTitleEmpty) {
		t.Fatalf("Update err = %v, want ErrTitleEmpty", err)
	}
	if n := f.writerCalls(); n != 0 {
		t.Fatalf("writer called %d times on rejection", n)
	}
}

func TestPostWriteCustom_UpdateDoesNotResanitizeIdenticalField(t *testing.T) {
	f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "w", Title: "W", Status: "draft",
		Author: 5, Content: pwcDirty, Excerpt: pwcDirty})
	svc := pwcService(t, f)
	err := svc.Update(context.Background(), pwcActor(5, pwcProdAuthor...), domain.Post{
		ID: 10, Title: "W2", Slug: "w", Content: pwcDirty, Excerpt: pwcDirty,
	}, time.Time{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := f.updates[0]
	if got.Content != pwcDirty || got.Excerpt != pwcDirty {
		t.Fatalf("unchanged fields were rewritten: content=%q excerpt=%q", got.Content, got.Excerpt)
	}
}

// --- 9.1-9.5: slugs -----------------------------------------------------------

func TestPostWriteCustom_Slugify(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!":   "hello-world",
		"  Foo   Bar  ":   "foo-bar",
		"A_B":             "a-b",
		"Big Widget 2":    "big-widget-2",
		"---x---":         "x",
		"Café":            "caf",
		"":                "",
		"!!!":             "",
		"UPPER lower 123": "upper-lower-123",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPostWriteCustom_SlugDerivedFromSanitizedTitle(t *testing.T) {
	f := newPwcFake()
	svc := pwcService(t, f)
	_, err := svc.Create(context.Background(), pwcActor(5, pwcProdAuthor...),
		domain.Post{Type: "product", Status: "draft", Title: "<em>Big</em> Widget"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := f.creates[0]
	if got.Slug == "" || got.Slug != Slugify(got.Title) {
		t.Fatalf("slug = %q, want Slugify(sanitized title %q)", got.Slug, got.Title)
	}
	if strings.Contains(got.Slug, "em") && !strings.Contains(got.Title, "em") {
		t.Fatalf("slug %q derived from the raw title", got.Slug)
	}
}

func TestPostWriteCustom_UpdateDerivesEmptySlugFromTitle(t *testing.T) {
	f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "old", Title: "Old", Status: "draft", Author: 5})
	svc := pwcService(t, f)
	err := svc.Update(context.Background(), pwcActor(5, pwcProdAuthor...),
		domain.Post{ID: 10, Title: "New Title", Slug: ""}, time.Time{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := f.updates[0].Slug; got != "new-title" {
		t.Fatalf("slug = %q, want new-title", got)
	}
}

func TestPostWriteCustom_InvalidSlugRejected(t *testing.T) {
	for _, bad := range []string{"Has Space", "UPPER", "-lead", "_lead", "a/b", "caf%C3%A9", "é"} {
		t.Run(bad, func(t *testing.T) {
			f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "w", Title: "W", Status: "draft", Author: 5})
			svc := pwcService(t, f)
			actor := pwcActor(5, pwcProdAuthor...)
			if _, err := svc.Create(context.Background(), actor,
				domain.Post{Type: "product", Status: "draft", Title: "T", Slug: bad}); !errors.Is(err, ErrSlugInvalid) {
				t.Fatalf("Create err = %v, want ErrSlugInvalid", err)
			}
			if err := svc.Update(context.Background(), actor,
				domain.Post{ID: 10, Title: "T", Slug: bad}, time.Time{}); !errors.Is(err, ErrSlugInvalid) {
				t.Fatalf("Update err = %v, want ErrSlugInvalid", err)
			}
			if n := f.writerCalls(); n != 0 {
				t.Fatalf("writer called %d times on rejection", n)
			}
		})
	}
}

func TestPostWriteCustom_SlugCollisionAutoSuffixes(t *testing.T) {
	// Collisions count in any status; a draft and a trashed row both block.
	f := newPwcFake(
		domain.Post{ID: 10, Type: "product", Slug: "widget", Status: "publish", Author: 5},
		domain.Post{ID: 11, Type: "product", Slug: "widget-2", Status: "draft", Author: 5},
		domain.Post{ID: 12, Type: "note", Slug: "gadget", Status: "publish", Author: 5}, // other type: no clash
	)
	svc := pwcService(t, f)
	actor := pwcActor(5, pwcProdAuthor...)

	if _, err := svc.Create(context.Background(), actor,
		domain.Post{Type: "product", Status: "draft", Title: "T", Slug: "widget"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := f.creates[0].Slug; got != "widget-3" {
		t.Fatalf("slug = %q, want widget-3", got)
	}

	// Derived slug collides too: title "Widget" -> widget -> widget-4.
	if _, err := svc.Create(context.Background(), actor,
		domain.Post{Type: "product", Status: "draft", Title: "Widget"}); err != nil {
		t.Fatalf("Create derived: %v", err)
	}
	if got := f.creates[1].Slug; got != "widget-4" {
		t.Fatalf("derived slug = %q, want widget-4", got)
	}

	// Flat type scopes by type: "gadget" exists only under note.
	if _, err := svc.Create(context.Background(), actor,
		domain.Post{Type: "product", Status: "draft", Title: "T", Slug: "gadget"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := f.creates[2].Slug; got != "gadget" {
		t.Fatalf("slug = %q, want gadget (other type must not collide)", got)
	}
}

func TestPostWriteCustom_UpdateOwnSlugNotACollision(t *testing.T) {
	f := newPwcFake(
		domain.Post{ID: 10, Type: "product", Slug: "widget", Title: "W", Status: "draft", Author: 5},
		domain.Post{ID: 11, Type: "product", Slug: "other", Status: "draft", Author: 5},
	)
	svc := pwcService(t, f)
	actor := pwcActor(5, pwcProdAuthor...)

	if err := svc.Update(context.Background(), actor,
		domain.Post{ID: 10, Title: "W2", Slug: "widget"}, time.Time{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := f.updates[0].Slug; got != "widget" {
		t.Fatalf("own slug rewritten to %q", got)
	}

	// Taking another row's slug is a collision and gets suffixed.
	if err := svc.Update(context.Background(), actor,
		domain.Post{ID: 10, Title: "W2", Slug: "other"}, time.Time{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := f.updates[1].Slug; got != "other-2" {
		t.Fatalf("slug = %q, want other-2", got)
	}
}

func TestPostWriteCustom_HierarchicalTypeScopesSlugsByParent(t *testing.T) {
	f := newPwcFake(
		domain.Post{ID: 5, Type: "guide", Slug: "parent", Status: "publish", Author: 5},
		domain.Post{ID: 20, Type: "guide", Slug: "intro", Status: "publish", Author: 5},            // top level
		domain.Post{ID: 21, Type: "guide", Slug: "setup", ParentID: 5, Status: "draft", Author: 5}, // under 5
	)
	svc := pwcService(t, f)
	actor := pwcActor(5, "edit_guides", "publish_guides", "edit_published_guides")

	// Same parent (top level): collides.
	if _, err := svc.Create(context.Background(), actor,
		domain.Post{Type: "guide", Status: "draft", Title: "T", Slug: "intro"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := f.creates[0].Slug; got != "intro-2" {
		t.Fatalf("slug = %q, want intro-2", got)
	}
	// Different parent: "intro" is free under 5.
	if _, err := svc.Create(context.Background(), actor,
		domain.Post{Type: "guide", Status: "draft", Title: "T", Slug: "intro", ParentID: 5}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := f.creates[1].Slug; got != "intro" {
		t.Fatalf("slug = %q, want intro (different parent)", got)
	}
	// Same non-zero parent: "setup" collides.
	if _, err := svc.Create(context.Background(), actor,
		domain.Post{Type: "guide", Status: "draft", Title: "T", Slug: "setup", ParentID: 5}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := f.creates[2].Slug; got != "setup-2" {
		t.Fatalf("slug = %q, want setup-2", got)
	}
}

func TestPostWriteCustom_BuiltinSlugsUntouched(t *testing.T) {
	f := newPwcFake(domain.Post{ID: 10, Type: "post", Slug: "dup", Status: "publish", Author: 5})
	svc := pwcService(t, f)
	actor := pwcActor(1, "edit_posts", "edit_pages", "publish_posts", "publish_pages")

	cases := []domain.Post{
		{Type: "post", Status: "draft", Title: "Has A Title", Slug: ""},    // not derived
		{Type: "post", Status: "draft", Title: "T", Slug: "Not Valid!"},    // not validated
		{Type: "page", Status: "draft", Title: "T", Slug: "dup"},           // not suffixed
		{Type: "post", Status: "draft", Title: "T", Slug: "dup"},           // not suffixed
		{Type: "page", Status: "draft", Title: "T", Slug: "UPPER/slashes"}, // not validated
	}
	for i, c := range cases {
		if _, err := svc.Create(context.Background(), actor, c); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got := f.creates[i].Slug; got != c.Slug {
			t.Fatalf("case %d: slug = %q, want %q unchanged", i, got, c.Slug)
		}
	}
	if f.nodeCalls != 0 {
		t.Fatalf("node reader consulted %d times for built-in types", f.nodeCalls)
	}
}

// --- 13.2, 13.3, 13.5: delete cleanup -----------------------------------------

func TestPostWriteCustom_DeleteDetachesTermsBetweenRevisionsAndRow(t *testing.T) {
	f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "w", Status: "draft", Author: 5})
	svc := pwcService(t, f)
	if err := svc.Delete(context.Background(), pwcActor(5, pwcProdAuthor...), domain.Post{ID: 10}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(f.log) != 4 || f.log[0] != "revisions" || f.log[3] != "row" {
		t.Fatalf("call order = %v, want revisions, two terms:*, row", f.log)
	}
	mid := []string{f.log[1], f.log[2]}
	sort.Strings(mid)
	if mid[0] != "terms:badge" || mid[1] != "terms:genre" {
		t.Fatalf("detached = %v, want genre and badge", mid)
	}
}

func TestPostWriteCustom_DetachFailureLeavesRow(t *testing.T) {
	f := newPwcFake(domain.Post{ID: 10, Type: "product", Slug: "w", Status: "draft", Author: 5})
	f.detachErr = errors.New("boom")
	svc := pwcService(t, f)
	err := svc.Delete(context.Background(), pwcActor(5, pwcProdAuthor...), domain.Post{ID: 10})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the detach error", err)
	}
	if len(f.deletes) != 0 {
		t.Fatalf("row deleted despite detach failure")
	}
	if _, ok := f.store[10]; !ok {
		t.Fatalf("row missing from store")
	}
}

func TestPostWriteCustom_BuiltinDeleteDoesNotDetach(t *testing.T) {
	f := newPwcFake(
		domain.Post{ID: 10, Type: "post", Slug: "a", Status: "draft", Author: 5},
		domain.Post{ID: 11, Type: "page", Slug: "b", Status: "draft", Author: 5},
	)
	f.detachErr = errors.New("detacher must not be called for built-ins")
	svc := pwcService(t, f)
	actor := pwcActor(5, "delete_posts", "delete_pages")
	for _, id := range []int64{10, 11} {
		if err := svc.Delete(context.Background(), actor, domain.Post{ID: id}); err != nil {
			t.Fatalf("Delete %d: %v", id, err)
		}
	}
	for _, e := range f.log {
		if strings.HasPrefix(e, "terms:") {
			t.Fatalf("built-in delete detached terms: %v", f.log)
		}
	}
}
