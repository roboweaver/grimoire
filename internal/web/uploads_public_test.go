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

// TestUploadsRouteMissingRootReturns404NotServerError proves that on a fresh
// install where no upload has ever happened (the uploads directory itself
// doesn't exist on disk yet), the handler surfaces a clean 404 instead of the
// 500 that an unguarded filepath.EvalSymlinks(root) error used to produce.
func TestUploadsRouteMissingRootReturns404NotServerError(t *testing.T) {
	h, root := newCommentServer(t)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("precondition: uploads root %q should not exist yet, stat err = %v", root, err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-content/uploads/2026/08/demo.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestUploadsRouteRejectsSymlinkEscapingRoot proves the symlink-escape guard
// works end-to-end: a symlink physically planted inside the uploads root
// that points outside it must not be served, even though the naive
// joined/cleaned request path looks contained within the root.
func TestUploadsRouteRejectsSymlinkEscapingRoot(t *testing.T) {
	h, root := newCommentServer(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	secretDir := filepath.Dir(root)
	secret := filepath.Join(secretDir, "secret-outside-root.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-content/uploads/escape.txt", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("symlink escape was served: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 404 or 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "top secret") {
		t.Fatalf("symlink escape leaked target content: %s", rec.Body.String())
	}
}
