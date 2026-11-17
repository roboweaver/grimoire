package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// --- M17: TermWriteService for registered taxonomies (task 2.1, RED) ---------
//
// Written failing-first. They reference symbols task 2.2 adds that do not yet
// exist: WithTermRegistry, WithTermContentPolicy, ErrTermSlugInvalid,
// ErrTermSlugTaken, ErrTermNameEmpty, ErrTaxonomyUnknown. Until 2.2 lands this
// file fails to compile; once stubbed, flat `badge` parents are accepted and
// hierarchical `genre` parents are rejected.
//
// Graph: genre G1(1) -> G2(2) -> G3(3), root G4(4); badge B1(20), B2(21); a
// category C1(30) exists only in category (cross-taxonomy parent case).

func customTermRegistry(t *testing.T) *registry.Registry {
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

func customTermGraph() *fakeTermWriter {
	genre := []domain.Term{
		{ID: 1, Name: "G1", Slug: "g1", Taxonomy: "genre"},
		{ID: 2, Name: "G2", Slug: "g2", Taxonomy: "genre", ParentID: 1},
		{ID: 3, Name: "G3", Slug: "g3", Taxonomy: "genre", ParentID: 2},
		{ID: 4, Name: "G4", Slug: "g4", Taxonomy: "genre"},
	}
	badge := []domain.Term{
		{ID: 20, Name: "B1", Slug: "b1", Taxonomy: "badge"},
		{ID: 21, Name: "B2", Slug: "b2", Taxonomy: "badge", ParentID: 20},
	}
	cat := []domain.Term{{ID: 30, Name: "C1", Slug: "c1", Taxonomy: TaxonomyCategory}}
	f := &fakeTermWriter{
		byTaxonomy: map[string][]domain.Term{"genre": genre, "badge": badge, TaxonomyCategory: cat},
		byIDs:      map[int64]domain.Term{},
	}
	for _, ts := range f.byTaxonomy {
		for _, tm := range ts {
			f.byIDs[tm.ID] = tm
		}
	}
	return f
}

func customTermSvc(t *testing.T, w *fakeTermWriter) *TermWriteService {
	t.Helper()
	return NewTermWriteService(w,
		WithTermRegistry(customTermRegistry(t)),
		WithTermContentPolicy(sanitize.New()))
}

var termEditor = func() auth.Principal { return actor(auth.RoleEditor, 1) }

func wantNotWritten(t *testing.T, w *fakeTermWriter) {
	t.Helper()
	if w.created != nil || w.updated != nil {
		t.Errorf("writer was called: created=%v updated=%v", w.created, w.updated)
	}
}

func TestTermCustomCreateGenreValidParentWritesParentID(t *testing.T) { // 3.1
	w := customTermGraph()
	_, err := customTermSvc(t, w).Create(context.Background(), termEditor(),
		domain.Term{Name: "New", Slug: "new", Taxonomy: "genre", ParentID: 2})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.created == nil || w.created.ParentID != 2 {
		t.Fatalf("created = %+v, want ParentID 2", w.created)
	}
}

func TestTermCustomFlatBadgeRejectsParent(t *testing.T) { // 3.2
	w := customTermGraph()
	svc := customTermSvc(t, w)
	_, err := svc.Create(context.Background(), termEditor(),
		domain.Term{Name: "N", Slug: "n", Taxonomy: "badge", ParentID: 20})
	if !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("create: err = %v, want ErrTermParentInvalid", err)
	}
	err = svc.Update(context.Background(), termEditor(),
		domain.Term{ID: 21, Name: "B2", Slug: "b2", Taxonomy: "badge", ParentID: 20})
	if !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("update: err = %v, want ErrTermParentInvalid", err)
	}
	wantNotWritten(t, w)
}

func TestTermCustomGenreCycle(t *testing.T) { // 3.3
	for name, parent := range map[string]int64{"self": 1, "descendant": 3} {
		w := customTermGraph()
		err := customTermSvc(t, w).Update(context.Background(), termEditor(),
			domain.Term{ID: 1, Name: "G1", Slug: "g1", Taxonomy: "genre", ParentID: parent})
		if !errors.Is(err, ErrTermParentCycle) {
			t.Errorf("%s: err = %v, want ErrTermParentCycle", name, err)
		}
		wantNotWritten(t, w)
	}
}

func TestTermCustomGenreParentMustBeInGenre(t *testing.T) { // 3.4
	for name, parent := range map[string]int64{"absent": 999, "category-only": 30} {
		w := customTermGraph()
		_, err := customTermSvc(t, w).Create(context.Background(), termEditor(),
			domain.Term{Name: "N", Slug: "n", Taxonomy: "genre", ParentID: parent})
		if !errors.Is(err, ErrTermParentInvalid) {
			t.Errorf("%s: err = %v, want ErrTermParentInvalid", name, err)
		}
		wantNotWritten(t, w)
	}
}

func TestTermCustomHierarchyReadUsesOwnTaxonomy(t *testing.T) { // 3.5
	w := customTermGraph()
	_, err := customTermSvc(t, w).Create(context.Background(), termEditor(),
		domain.Term{Name: "N", Slug: "n", Taxonomy: "genre", ParentID: 1})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.lastTaxonomyArg != "genre" {
		t.Errorf("last ListByTaxonomy arg = %q, want genre", w.lastTaxonomyArg)
	}
}

func TestTermCustomUnknownTaxonomyRejectedBuiltinsAccepted(t *testing.T) { // 4.6, 4.7
	w := customTermGraph()
	svc := customTermSvc(t, w)
	_, err := svc.Create(context.Background(), termEditor(),
		domain.Term{Name: "N", Slug: "n", Taxonomy: "nope"})
	if !errors.Is(err, ErrTaxonomyUnknown) {
		t.Errorf("unknown: err = %v, want ErrTaxonomyUnknown", err)
	}
	wantNotWritten(t, w)
	for _, tax := range []string{TaxonomyCategory, taxonomyPostTag} {
		if _, err := svc.Create(context.Background(), termEditor(),
			domain.Term{Name: "N", Slug: "n", Taxonomy: tax}); err != nil {
			t.Errorf("%s create: %v", tax, err)
		}
	}
}

func TestTermCustomNameSanitizedForRegisteredOnly(t *testing.T) { // 4.1, 4.2, 4.7
	w := customTermGraph()
	svc := customTermSvc(t, w)
	if _, err := svc.Create(context.Background(), termEditor(),
		domain.Term{Name: "<b>x</b>", Slug: "x", Taxonomy: "genre"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.created.Name != "x" {
		t.Errorf("name = %q, want plain text %q", w.created.Name, "x")
	}
	w = customTermGraph()
	svc = customTermSvc(t, w)
	_, err := svc.Create(context.Background(), termEditor(),
		domain.Term{Name: "<script>alert(1)</script>", Slug: "y", Taxonomy: "genre"})
	if !errors.Is(err, ErrTermNameEmpty) {
		t.Errorf("empty-after-sanitize: err = %v, want ErrTermNameEmpty", err)
	}
	wantNotWritten(t, w)
	const raw = "<b>Cat</b> & co"
	if _, err := svc.Create(context.Background(), termEditor(),
		domain.Term{Name: raw, Slug: "cat2", Taxonomy: TaxonomyCategory}); err != nil {
		t.Fatalf("category create: %v", err)
	}
	// M18 (Req 5.1): built-in taxonomies reduce the name too.
	if w.created.Name != "Cat & co" {
		t.Errorf("category name = %q, want reduced %q", w.created.Name, "Cat & co")
	}
}

func TestTermCustomSlugCharset(t *testing.T) { // 4.3
	for _, slug := range []string{"Upper", "has space", "-lead", "a/b", ""} {
		w := customTermGraph()
		_, err := customTermSvc(t, w).Create(context.Background(), termEditor(),
			domain.Term{Name: "N", Slug: slug, Taxonomy: "genre"})
		if !errors.Is(err, ErrTermSlugInvalid) {
			t.Errorf("slug %q: err = %v, want ErrTermSlugInvalid", slug, err)
		}
		wantNotWritten(t, w)
	}
}

func TestTermCustomSlugUniquenessAmongSiblings(t *testing.T) { // 4.4, 4.5
	ctx := context.Background()
	// genre: g4 is a root; a new root with slug g4 is a duplicate sibling.
	w := customTermGraph()
	svc := customTermSvc(t, w)
	_, err := svc.Create(ctx, termEditor(), domain.Term{Name: "D", Slug: "g4", Taxonomy: "genre"})
	if !errors.Is(err, ErrTermSlugTaken) {
		t.Errorf("duplicate sibling: err = %v, want ErrTermSlugTaken", err)
	}
	wantNotWritten(t, w)
	// Same slug under a different parent in genre is accepted.
	if _, err := svc.Create(ctx, termEditor(),
		domain.Term{Name: "D", Slug: "g4", Taxonomy: "genre", ParentID: 1}); err != nil {
		t.Errorf("same slug, different parent: %v", err)
	}
	// Flat badge: duplicate rejected regardless of parent (0 here).
	w = customTermGraph()
	_, err = customTermSvc(t, w).Create(ctx, termEditor(),
		domain.Term{Name: "D", Slug: "b2", Taxonomy: "badge"})
	if !errors.Is(err, ErrTermSlugTaken) {
		t.Errorf("flat duplicate: err = %v, want ErrTermSlugTaken", err)
	}
	wantNotWritten(t, w)
	// Update keeping its own slug is accepted.
	w = customTermGraph()
	if err := customTermSvc(t, w).Update(ctx, termEditor(),
		domain.Term{ID: 4, Name: "G4 renamed", Slug: "g4", Taxonomy: "genre"}); err != nil {
		t.Errorf("update own slug: %v", err)
	}
}

func TestTermCustomForbiddenBeforeAnyRead(t *testing.T) { // capability gate
	w := customTermGraph()
	svc := customTermSvc(t, w)
	author := actor(auth.RoleAuthor, 2)
	_, err := svc.Create(context.Background(), author,
		domain.Term{Name: "N", Slug: "n", Taxonomy: "genre", ParentID: 1})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("create: err = %v, want ErrForbidden", err)
	}
	err = svc.Update(context.Background(), author,
		domain.Term{ID: 4, Name: "N", Slug: "n", Taxonomy: "genre", ParentID: 1})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("update: err = %v, want ErrForbidden", err)
	}
	wantNotWritten(t, w)
	if w.lastTaxonomyArg != "" || w.lastIDsArg != nil {
		t.Errorf("reads happened before the gate: taxonomy=%q ids=%v", w.lastTaxonomyArg, w.lastIDsArg)
	}
}

func TestTermCustomNilRegistryKeepsBuiltinBehavior(t *testing.T) { // 15.3
	ctx := context.Background()
	w := termReparentGraph()
	svc := NewTermWriteService(w) // no registry, no policy
	// Same slug as a sibling and raw markup in the name: untouched for built-ins.
	const raw = "<i>x</i>"
	if _, err := svc.Create(ctx, termEditor(),
		domain.Term{Name: raw, Slug: "a", Taxonomy: TaxonomyCategory}); err != nil {
		t.Fatalf("category create: %v", err)
	}
	// M18 (Req 5.1): the name is reduced for built-ins; the slug stays untouched.
	if w.created.Name != "x" || w.created.Slug != "a" {
		t.Errorf("created = %+v, want reduced name and untouched slug", w.created)
	}
	if _, err := svc.Create(ctx, termEditor(),
		domain.Term{Name: "t2", Slug: "t", Taxonomy: taxonomyPostTag}); err != nil {
		t.Errorf("post_tag create: %v", err)
	}
	if _, err := svc.Create(ctx, termEditor(),
		domain.Term{Name: "t3", Slug: "t3", Taxonomy: taxonomyPostTag, ParentID: 9}); !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("post_tag parent: err = %v, want ErrTermParentInvalid", err)
	}
	if _, err := svc.Create(ctx, termEditor(),
		domain.Term{Name: "x", Slug: "x", Taxonomy: "genre"}); !errors.Is(err, ErrTaxonomyUnknown) {
		t.Errorf("nil registry, custom taxonomy: err = %v, want ErrTaxonomyUnknown", err)
	}
}
