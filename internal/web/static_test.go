package web_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestFaviconServed(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/favicon.ico")
	if rec.Code != http.StatusOK {
		t.Fatalf("favicon status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Fatalf("favicon content-type = %q, want image/*", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("favicon body empty")
	}
}

func TestIconAssetServed(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/assets/icons/favicon-32x32.png")
	if rec.Code != http.StatusOK {
		t.Fatalf("icon asset status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Fatalf("icon asset content-type = %q, want image/*", ct)
	}
}

func TestHomeHasFaviconLinks(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/")
	body := rec.Body.String()
	wants := []string{
		`<link rel="icon" href="/favicon.ico" sizes="any">`,
		`href="/assets/icons/favicon-32x32.png"`,
		`href="/assets/icons/favicon-16x16.png"`,
		`rel="apple-touch-icon"`,
		`href="/assets/icons/site.webmanifest"`,
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Fatalf("home HTML missing %q; body:\n%s", w, body)
		}
	}
}
