package web_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadsRouteServesFilesAndRejectsTraversal(t *testing.T) {
	h, root := newCommentServer(t)
	if err := os.MkdirAll(filepath.Join(root, "2026", "08"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "2026", "08", "demo.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, httptest.NewRequest(http.MethodGet, "/wp-content/uploads/2026/08/demo.txt", nil))
	if ok.Code != http.StatusOK {
		t.Fatalf("serve status = %d", ok.Code)
	}
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/wp-content/uploads/../secrets.txt", nil))
	if bad.Code != http.StatusBadRequest && bad.Code != http.StatusNotFound {
		t.Fatalf("traversal status = %d", bad.Code)
	}
	if strings.Contains(bad.Body.String(), root) {
		t.Fatalf("body leaked path: %s", bad.Body.String())
	}
}
