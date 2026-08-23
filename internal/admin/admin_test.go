package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// fakeDist mimics a Vite build: an index.html entry document plus a
// content-hashed asset under assets/.
func fakeDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":              {Data: []byte("<!doctype html><div id=root>app</div>")},
		"assets/app-abc123.js":    {Data: []byte("console.log('hi')")},
		"assets/style-def456.css": {Data: []byte(".x{color:red}")},
	}
}

func serve(t *testing.T, fsys fstest.MapFS, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	h := handler(fsys, "/admin")
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServesHashedAssetWithImmutableCache(t *testing.T) {
	rec := serve(t, fakeDist(), http.MethodGet, "/admin/assets/app-abc123.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "console.log('hi')" {
		t.Fatalf("body = %q, want the asset contents", got)
	}
	cc := rec.Header().Get("Cache-Control")
	if cc == "" || !containsAll(cc, "max-age=31536000", "immutable") {
		t.Fatalf("Cache-Control = %q, want long-lived immutable", cc)
	}
}

func TestMissingAssetReturns404(t *testing.T) {
	rec := serve(t, fakeDist(), http.MethodGet, "/admin/assets/missing-000.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a missing hashed asset", rec.Code)
	}
}

func TestClientRouteFallsBackToIndex(t *testing.T) {
	rec := serve(t, fakeDist(), http.MethodGet, "/admin/posts/42")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 index fallback", rec.Code)
	}
	if rec.Body.String() != "<!doctype html><div id=root>app</div>" {
		t.Fatalf("body = %q, want index.html", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache for the SPA entry", cc)
	}
}

func TestRootServesIndex(t *testing.T) {
	rec := serve(t, fakeDist(), http.MethodGet, "/admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 at prefix root", rec.Code)
	}
	if rec.Body.String() != "<!doctype html><div id=root>app</div>" {
		t.Fatalf("root body = %q, want index.html", rec.Body.String())
	}
}

func TestExplicitIndexIsNotCached(t *testing.T) {
	rec := serve(t, fakeDist(), http.MethodGet, "/admin/index.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache for index.html", cc)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
