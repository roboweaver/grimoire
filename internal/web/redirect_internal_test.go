package web

import "testing"

func TestSafeRedirect(t *testing.T) {
	// Malicious or non-local targets must fall back to "/". The backslash cases
	// matter because browsers normalize "\" to "/", so "/\evil.com" becomes
	// "//evil.com" (a scheme-relative URL to an external host).
	unsafe := []string{
		"",
		`/\evil.com`,
		`/\/evil.com`,
		`\\evil.com`,
		"//evil.com",
		"https://evil.com",
		"http:evil.com",
		"javascript:alert(1)",
		`/\\evil.com`,
	}
	for _, in := range unsafe {
		if got := safeRedirect(in); got != "/" {
			t.Errorf("safeRedirect(%q) = %q, want %q", in, got, "/")
		}
	}

	// Legitimate relative paths pass through unchanged.
	safe := []string{
		"/",
		"/wp-admin/",
		"/posts/5?x=1#frag",
		"/a/b/c",
	}
	for _, in := range safe {
		if got := safeRedirect(in); got != in {
			t.Errorf("safeRedirect(%q) = %q, want unchanged", in, got)
		}
	}
}

// TestRandTokenNonEmptyDistinct is a light sanity check that randToken produces
// non-empty, distinct tokens. The crypto/rand error path is effectively
// unreachable in normal operation; this just guards the happy path.
func TestRandTokenNonEmptyDistinct(t *testing.T) {
	a, err := randToken()
	if err != nil {
		t.Fatalf("randToken: %v", err)
	}
	b, err := randToken()
	if err != nil {
		t.Fatalf("randToken: %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("randToken returned empty token")
	}
	if a == b {
		t.Fatal("randToken returned identical tokens")
	}
}
