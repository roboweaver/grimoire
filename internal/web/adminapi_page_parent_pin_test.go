package web

import (
	"encoding/json"
	"testing"
)

// M19 Req 6.6 pin (passes today, must keep passing): the admin page detail
// DTO still omits `parent` when it is 0, and carries it when non-zero. Only the
// REST page shape changes in M19.
func TestAdminPageDetailParentPins(t *testing.T) {
	raw, err := json.Marshal(postDetailResponse{ID: 1, Type: "page", Parent: 0})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := obj["parent"]; ok {
		t.Errorf("admin page detail carries parent = %s when 0, want omitted", v)
	}
	raw, _ = json.Marshal(postDetailResponse{ID: 2, Type: "page", Parent: 5})
	obj = nil
	_ = json.Unmarshal(raw, &obj)
	if string(obj["parent"]) != "5" {
		t.Errorf("admin page detail parent = %s, want 5", obj["parent"])
	}
}
