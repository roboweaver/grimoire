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
