package content

import (
	"context"
	"encoding/json"
	"testing"
)

// M12c Phase 14.1 -- RED test: RESTMapper.CustomPost serializes `parent` for a
// hierarchical type and omits it for a flat one (Req 11.1; D7).
//
// Reuses restCustomFixture: `manual` is hierarchical, `book` is flat.
// Helper prefix: rcpar. RED today: RESTCustomPost has no parent.

func rcparParent(t *testing.T, typ, slug string, id, parentID int64) (json.RawMessage, bool) {
	t.Helper()
	m, reg, _, _ := restCustomFixture(t)
	row := restCustomRow(typ, slug, id)
	row.ParentID = parentID
	got, err := m.CustomPost(context.Background(), row, restCustomPT(t, reg, typ), []string{slug})
	if err != nil {
		t.Fatalf("CustomPost: %v", err)
	}
	v, ok := restCustomJSON(t, got)["parent"]
	return v, ok
}

func TestRESTCustomPostParentSerializedForHierarchicalType(t *testing.T) {
	v, ok := rcparParent(t, "manual", "ch-1", 8, 5)
	if !ok || string(v) != "5" {
		t.Fatalf("parent = %s (present=%v), want 5", v, ok)
	}
	v, ok = rcparParent(t, "manual", "top", 9, 0)
	if !ok || string(v) != "0" {
		t.Fatalf("top-level parent = %s (present=%v), want 0", v, ok)
	}
}

func TestRESTCustomPostParentOmittedForFlatType(t *testing.T) {
	if v, ok := rcparParent(t, "book", "dune", 7, 5); ok {
		t.Fatalf("flat type carries parent = %s, want key omitted", v)
	}
}
