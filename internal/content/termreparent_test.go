package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// --- M13: TermWriteService parent validation (task 2.1, failing-first) -------
//
// These tests pin the hierarchy-write validation M13 adds to
// TermWriteService.Create/Update, written failing-first (strict TDD): they
// reference symbols task 2.2 adds and that do not yet exist --
// content.ErrTermParentCycle, content.ErrTermParentInvalid, and the private
// TermWriteService.validateParent the service calls between the
// manage_categories gate and the writer. Until 2.2 lands, this file fails to
// COMPILE (the two sentinels are undefined) and, once it compiles, fails
// because Create/Update still call the writer unconditionally after the gate.
//
// The fake is the existing combined domain.TermWriter+domain.TermReader
// fakeTermWriter (writeservices_test.go): its byTaxonomy map backs
// ListByTaxonomy so LoadTermHierarchy/DescendantIDs/byID resolve, its
// created/updated fields record whether the writer ran, and its lastTaxonomyArg
// records whether a hierarchy was ever loaded. The seeded graph and the
// actor(role, id) helper are reused as-is -- this is a deliberate reuse of the
// shared fake, extending its seeded data only, not its shape.
//
// Actors: auth.RoleEditor holds manage_categories (CanManageTerms true, matching
// the existing TestTermWriteAuthz/TestTermWriteUpdateAuthz term-write tests);
// auth.RoleAuthor does not, so it exercises the ErrForbidden gate.

// termReparentGraph seeds a category chain A -> B -> C -> D plus a sibling S of
// A, and a lone post_tag term, into a fresh fakeTermWriter. The category graph
// makes DescendantIDs(A) == {A, B, C, D} so the transitive-cycle case
// (re-parenting A under its own descendant C) is reachable, and the post_tag
// term exercises the cross-taxonomy case (it exists only outside the category
// graph, so category's byID never contains it).
//
// IDs: A=1, B=2 (child of A), C=3 (child of B), D=4 (child of C), S=5 (root);
// tag T=9 under post_tag.
func termReparentGraph() *fakeTermWriter {
	return &fakeTermWriter{
		byTaxonomy: map[string][]domain.Term{
			TaxonomyCategory: {
				{ID: 1, Name: "A", Slug: "a", Taxonomy: TaxonomyCategory, ParentID: 0},
				{ID: 2, Name: "B", Slug: "b", Taxonomy: TaxonomyCategory, ParentID: 1},
				{ID: 3, Name: "C", Slug: "c", Taxonomy: TaxonomyCategory, ParentID: 2},
				{ID: 4, Name: "D", Slug: "d", Taxonomy: TaxonomyCategory, ParentID: 3},
				{ID: 5, Name: "S", Slug: "s", Taxonomy: TaxonomyCategory, ParentID: 0},
			},
			taxonomyPostTag: {
				{ID: 9, Name: "T", Slug: "t", Taxonomy: taxonomyPostTag, ParentID: 0},
			},
		},
		byIDs: map[int64]domain.Term{
			1: {ID: 1, Name: "A", Slug: "a", Taxonomy: TaxonomyCategory, ParentID: 0},
			2: {ID: 2, Name: "B", Slug: "b", Taxonomy: TaxonomyCategory, ParentID: 1},
			3: {ID: 3, Name: "C", Slug: "c", Taxonomy: TaxonomyCategory, ParentID: 2},
			4: {ID: 4, Name: "D", Slug: "d", Taxonomy: TaxonomyCategory, ParentID: 3},
			5: {ID: 5, Name: "S", Slug: "s", Taxonomy: TaxonomyCategory, ParentID: 0},
			9: {ID: 9, Name: "T", Slug: "t", Taxonomy: taxonomyPostTag, ParentID: 0},
		},
	}
}

// TestTermReparentUpdateSelfParentCycle: an Update whose parent equals the
// term's own id is a self-cycle (Req 3.1) -> ErrTermParentCycle, writer NOT
// called.
func TestTermReparentUpdateSelfParentCycle(t *testing.T) {
	w := termReparentGraph()
	svc := NewTermWriteService(w)
	// Term A (id 1) re-parented under itself.
	term := domain.Term{ID: 1, Name: "A", Slug: "a", Taxonomy: TaxonomyCategory, ParentID: 1}

	err := svc.Update(context.Background(), actor(auth.RoleEditor, 1), term)
	if !errors.Is(err, ErrTermParentCycle) {
		t.Fatalf("self-parent update: err = %v, want ErrTermParentCycle", err)
	}
	if w.updated != nil {
		t.Error("writer.Update must not be called on a cycle")
	}
}

// TestTermReparentUpdateDescendantParentCycle: an Update whose parent is a
// member of the term's descendant set is a transitive cycle (Req 3.2) ->
// ErrTermParentCycle, writer NOT called. A (id 1) under C (id 3): C is a
// descendant of A (A -> B -> C), so DescendantIDs(1) contains 3.
func TestTermReparentUpdateDescendantParentCycle(t *testing.T) {
	w := termReparentGraph()
	svc := NewTermWriteService(w)
	term := domain.Term{ID: 1, Name: "A", Slug: "a", Taxonomy: TaxonomyCategory, ParentID: 3}

	err := svc.Update(context.Background(), actor(auth.RoleEditor, 1), term)
	if !errors.Is(err, ErrTermParentCycle) {
		t.Fatalf("descendant-parent update: err = %v, want ErrTermParentCycle", err)
	}
	if w.updated != nil {
		t.Error("writer.Update must not be called on a cycle")
	}
}

// TestTermReparentNonexistentParentInvalid: a non-zero parent naming no term in
// the category graph (Req 4.1) -> ErrTermParentInvalid, writer NOT called,
// asserted on both Update and Create.
func TestTermReparentNonexistentParentInvalid(t *testing.T) {
	t.Run("update", func(t *testing.T) {
		w := termReparentGraph()
		svc := NewTermWriteService(w)
		term := domain.Term{ID: 2, Name: "B", Slug: "b", Taxonomy: TaxonomyCategory, ParentID: 404}

		err := svc.Update(context.Background(), actor(auth.RoleEditor, 1), term)
		if !errors.Is(err, ErrTermParentInvalid) {
			t.Fatalf("nonexistent-parent update: err = %v, want ErrTermParentInvalid", err)
		}
		if w.updated != nil {
			t.Error("writer.Update must not be called on an invalid parent")
		}
	})
	t.Run("create", func(t *testing.T) {
		w := termReparentGraph()
		svc := NewTermWriteService(w)
		term := domain.Term{Name: "New", Slug: "new", Taxonomy: TaxonomyCategory, ParentID: 404}

		_, err := svc.Create(context.Background(), actor(auth.RoleEditor, 1), term)
		if !errors.Is(err, ErrTermParentInvalid) {
			t.Fatalf("nonexistent-parent create: err = %v, want ErrTermParentInvalid", err)
		}
		if w.created != nil {
			t.Error("writer.Create must not be called on an invalid parent")
		}
	})
}

// TestTermReparentCrossTaxonomyParentInvalid: a parent id that exists only in a
// DIFFERENT taxonomy (seeded under post_tag, id 9) is absent from the category
// graph's byID, so validating a category write against it (Req 4.2) ->
// ErrTermParentInvalid, writer NOT called.
func TestTermReparentCrossTaxonomyParentInvalid(t *testing.T) {
	w := termReparentGraph()
	svc := NewTermWriteService(w)
	// Category B (id 2) pointed at the post_tag term id 9.
	term := domain.Term{ID: 2, Name: "B", Slug: "b", Taxonomy: TaxonomyCategory, ParentID: 9}

	err := svc.Update(context.Background(), actor(auth.RoleEditor, 1), term)
	if !errors.Is(err, ErrTermParentInvalid) {
		t.Fatalf("cross-taxonomy-parent update: err = %v, want ErrTermParentInvalid", err)
	}
	if w.updated != nil {
		t.Error("writer.Update must not be called on a cross-taxonomy parent")
	}
}

// TestTermReparentPostTagNonZeroParentInvalid: a post_tag term (flat taxonomy)
// with any non-zero parent (Req 4.3) -> ErrTermParentInvalid, writer NOT called,
// on both Create and Update.
func TestTermReparentPostTagNonZeroParentInvalid(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		w := termReparentGraph()
		svc := NewTermWriteService(w)
		term := domain.Term{Name: "Tag", Slug: "tag", Taxonomy: taxonomyPostTag, ParentID: 9}

		_, err := svc.Create(context.Background(), actor(auth.RoleEditor, 1), term)
		if !errors.Is(err, ErrTermParentInvalid) {
			t.Fatalf("post_tag non-zero parent create: err = %v, want ErrTermParentInvalid", err)
		}
		if w.created != nil {
			t.Error("writer.Create must not be called for a flat-taxonomy parent")
		}
	})
	t.Run("update", func(t *testing.T) {
		w := termReparentGraph()
		svc := NewTermWriteService(w)
		term := domain.Term{ID: 9, Name: "T", Slug: "t", Taxonomy: taxonomyPostTag, ParentID: 1}

		err := svc.Update(context.Background(), actor(auth.RoleEditor, 1), term)
		if !errors.Is(err, ErrTermParentInvalid) {
			t.Fatalf("post_tag non-zero parent update: err = %v, want ErrTermParentInvalid", err)
		}
		if w.updated != nil {
			t.Error("writer.Update must not be called for a flat-taxonomy parent")
		}
	})
}

// TestTermReparentTopLevelParentZeroValid: parent 0 is a valid top-level
// assignment for any taxonomy including post_tag (Req 4.4) -> no validation
// error, writer IS called with ParentID == 0, on both Create and Update.
func TestTermReparentTopLevelParentZeroValid(t *testing.T) {
	t.Run("category update", func(t *testing.T) {
		w := termReparentGraph()
		svc := NewTermWriteService(w)
		term := domain.Term{ID: 2, Name: "B", Slug: "b", Taxonomy: TaxonomyCategory, ParentID: 0}

		if err := svc.Update(context.Background(), actor(auth.RoleEditor, 1), term); err != nil {
			t.Fatalf("top-level category update: %v", err)
		}
		if w.updated == nil {
			t.Fatal("writer.Update must be called for a valid top-level parent")
		}
		if w.updated.ParentID != 0 {
			t.Errorf("updated.ParentID = %d, want 0", w.updated.ParentID)
		}
	})
	t.Run("post_tag create", func(t *testing.T) {
		w := termReparentGraph()
		svc := NewTermWriteService(w)
		term := domain.Term{Name: "Tag", Slug: "tag", Taxonomy: taxonomyPostTag, ParentID: 0}

		if _, err := svc.Create(context.Background(), actor(auth.RoleEditor, 1), term); err != nil {
			t.Fatalf("top-level post_tag create: %v", err)
		}
		if w.created == nil {
			t.Fatal("writer.Create must be called for a valid top-level parent")
		}
		if w.created.ParentID != 0 {
			t.Errorf("created.ParentID = %d, want 0", w.created.ParentID)
		}
	})
}

// TestTermReparentValidSameTaxonomyParent: creating a category under an existing
// category parent (Req 1.2, 3.4 -- a new term trivially passes the cycle check)
// -> writer called with the right ParentID.
func TestTermReparentValidSameTaxonomyParent(t *testing.T) {
	w := termReparentGraph()
	svc := NewTermWriteService(w)
	// New category under B (id 2), an existing category.
	term := domain.Term{Name: "New", Slug: "new", Taxonomy: TaxonomyCategory, ParentID: 2}

	if _, err := svc.Create(context.Background(), actor(auth.RoleEditor, 1), term); err != nil {
		t.Fatalf("valid same-taxonomy create: %v", err)
	}
	if w.created == nil {
		t.Fatal("writer.Create must be called for a valid parent")
	}
	if w.created.ParentID != 2 {
		t.Errorf("created.ParentID = %d, want 2", w.created.ParentID)
	}
}

// TestTermReparentCapabilityGatePrecedesValidation: the manage_categories gate
// runs before validateParent, so a caller lacking it gets ErrForbidden even when
// the parent is also invalid, and neither the hierarchy is loaded (the fake's
// ListByTaxonomy is never called, so lastTaxonomyArg stays "") nor the writer is
// called (Req 5.3). Asserted on both Create and Update.
func TestTermReparentCapabilityGatePrecedesValidation(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		w := termReparentGraph()
		svc := NewTermWriteService(w)
		// Parent 404 is invalid, but authz must win before validation runs.
		term := domain.Term{Name: "New", Slug: "new", Taxonomy: TaxonomyCategory, ParentID: 404}

		_, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 1), term)
		if err != ErrForbidden {
			t.Fatalf("unauthorized create: err = %v, want ErrForbidden", err)
		}
		if w.created != nil {
			t.Error("writer.Create must not be called on denial")
		}
		if w.lastTaxonomyArg != "" {
			t.Errorf("hierarchy loaded before authz gate: ListByTaxonomy(%q) called", w.lastTaxonomyArg)
		}
	})
	t.Run("update", func(t *testing.T) {
		w := termReparentGraph()
		svc := NewTermWriteService(w)
		term := domain.Term{ID: 1, Name: "A", Slug: "a", Taxonomy: TaxonomyCategory, ParentID: 404}

		err := svc.Update(context.Background(), actor(auth.RoleAuthor, 1), term)
		if err != ErrForbidden {
			t.Fatalf("unauthorized update: err = %v, want ErrForbidden", err)
		}
		if w.updated != nil {
			t.Error("writer.Update must not be called on denial")
		}
		if w.lastTaxonomyArg != "" {
			t.Errorf("hierarchy loaded before authz gate: ListByTaxonomy(%q) called", w.lastTaxonomyArg)
		}
	})
}
