package content

import (
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// This file holds the RED (test-first) specification for the content.rendered
// srcset/sizes rendering helpers implemented by task 3.2 in rest.go:
//
//   srcsetFromSizes(base string, variants []sizeVariant, fullWidth int) (srcset, sizes string)
//   resolveAttachmentID(n *html.Node) (int64, bool)
//   urlDir(s string) string
//   attrValue(n *html.Node, key string) string
//   setIfAbsent(n *html.Node, key, val string)
//
// Until 3.2 lands these symbols are undefined, so `go test ./internal/content`
// fails to compile here — the expected RED state. The sizeVariant type itself
// already exists (rest.go, task 2.2).
//
// Covers Req 2.2 (srcset ascending by width), 2.3/2.5 (sizes form, no 0px),
// 2.6 (setIfAbsent idempotence), 3.1 (token-scan attachment id, never src).

// imgWithClass builds an <img> element node carrying exactly the given class
// attribute value (and nothing else), the minimal shape resolveAttachmentID
// inspects. Building the node literally (rather than via html.ParseFragment)
// keeps the helper tests independent of the HTML parser's reconstruction.
func imgWithClass(class string) *html.Node {
	return &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Img,
		Data:     "img",
		Attr:     []html.Attribute{{Key: "class", Val: class}},
	}
}

// imgWithAttrs builds an <img> element node with an arbitrary attribute set,
// used to prove resolveAttachmentID never consults src.
func imgWithAttrs(attrs ...html.Attribute) *html.Node {
	return &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Img,
		Data:     "img",
		Attr:     attrs,
	}
}

// TestSrcsetFromSizesSortsAscendingByWidth: variants supplied OUT of width
// order must emerge sorted ascending by width, each candidate rendered as
// urlDir(base)+file+" {width}w" and joined by ", " (Req 2.2, D5).
func TestSrcsetFromSizesSortsAscendingByWidth(t *testing.T) {
	base := "https://x/wp-content/uploads/2024/03/photo.jpg"
	variants := []sizeVariant{
		{File: "photo-300x200.jpg", Width: 300, Height: 200},
		{File: "photo-150x150.jpg", Width: 150, Height: 150},
	}

	srcset, _ := srcsetFromSizes(base, variants, 0)

	want := "https://x/wp-content/uploads/2024/03/photo-150x150.jpg 150w, " +
		"https://x/wp-content/uploads/2024/03/photo-300x200.jpg 300w"
	if srcset != want {
		t.Errorf("srcset =\n  %q\nwant\n  %q", srcset, want)
	}
}

// TestSrcsetFromSizesSizesFullWidthPositive: a positive fullWidth yields the
// WordPress sizes attribute "(max-width: {w}px) 100vw, {w}px" (Req 2.3).
func TestSrcsetFromSizesSizesFullWidthPositive(t *testing.T) {
	_, sizes := srcsetFromSizes("https://x/a/photo.jpg", nil, 800)

	want := "(max-width: 800px) 100vw, 800px"
	if sizes != want {
		t.Errorf("sizes = %q, want %q", sizes, want)
	}
}

// TestSrcsetFromSizesSizesFullWidthZero: fullWidth == 0 yields an EMPTY sizes
// string — never a "(max-width: 0px) 100vw, 0px" form (Req 2.5 spirit).
func TestSrcsetFromSizesSizesFullWidthZero(t *testing.T) {
	_, sizes := srcsetFromSizes("https://x/a/photo.jpg", nil, 0)

	if sizes != "" {
		t.Errorf("sizes = %q, want %q (no 0px form)", sizes, "")
	}
}

// TestSrcsetFromSizesBaseWithoutSlash documents the edge where base has no
// "/": urlDir(base) is "" so each candidate is just file+" {w}w".
func TestSrcsetFromSizesBaseWithoutSlash(t *testing.T) {
	variants := []sizeVariant{
		{File: "photo-150x150.jpg", Width: 150, Height: 150},
		{File: "photo-300x200.jpg", Width: 300, Height: 200},
	}

	srcset, _ := srcsetFromSizes("photo.jpg", variants, 0)

	want := "photo-150x150.jpg 150w, photo-300x200.jpg 300w"
	if srcset != want {
		t.Errorf("srcset = %q, want %q", srcset, want)
	}
}

// TestResolveAttachmentID covers the token-scan contract of
// resolveAttachmentID: a wp-image-{digits} CLASS token (scanned via
// strings.Fields, not a substring match), zero/absent/non-numeric → (0,false),
// the substring trap must not match, and src is never consulted (Req 3.1).
func TestResolveAttachmentID(t *testing.T) {
	cases := []struct {
		name   string
		node   *html.Node
		wantID int64
		wantOK bool
	}{
		{"bare wp-image token", imgWithClass("wp-image-42"), 42, true},
		{"wp-image among other classes", imgWithClass("foo wp-image-7 bar"), 7, true},
		{"no wp-image class", imgWithClass("size-full aligncenter"), 0, false},
		{"no class attribute at all", imgWithAttrs(), 0, false},
		{"wp-image-0 is zero -> false", imgWithClass("wp-image-0"), 0, false},
		{"wp-image-abc non-numeric", imgWithClass("wp-image-abc"), 0, false},
		{"substring trap must not match", imgWithClass("not-a-wp-image-5-thing"), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotOK := resolveAttachmentID(tc.node)
			if gotID != tc.wantID || gotOK != tc.wantOK {
				t.Errorf("resolveAttachmentID = (%d, %t), want (%d, %t)",
					gotID, gotOK, tc.wantID, tc.wantOK)
			}
		})
	}
}

// TestResolveAttachmentIDNeverConsultsSrc: an <img> whose src contains
// "wp-image-99" but whose class has no wp-image token must return (0, false)
// — the id comes only from the class token scan, never from src (Req 3.1).
func TestResolveAttachmentIDNeverConsultsSrc(t *testing.T) {
	node := imgWithAttrs(
		html.Attribute{Key: "src", Val: "https://x/wp-content/uploads/wp-image-99.jpg"},
		html.Attribute{Key: "class", Val: "size-full"},
	)

	gotID, gotOK := resolveAttachmentID(node)
	if gotID != 0 || gotOK != false {
		t.Errorf("resolveAttachmentID consulted src: got (%d, %t), want (0, false)", gotID, gotOK)
	}
}

// TestURLDir: urlDir returns everything up to and including the final "/", or
// "" when there is no slash (including the empty string).
func TestURLDir(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://x/a/b/photo.jpg", "https://x/a/b/"},
		{"photo.jpg", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := urlDir(tc.in); got != tc.want {
			t.Errorf("urlDir(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAttrValue: attrValue returns a present attribute's value and "" when the
// key is absent.
func TestAttrValue(t *testing.T) {
	node := imgWithAttrs(
		html.Attribute{Key: "class", Val: "wp-image-42"},
		html.Attribute{Key: "alt", Val: "a photo"},
	)

	if got := attrValue(node, "alt"); got != "a photo" {
		t.Errorf("attrValue(alt) = %q, want %q", got, "a photo")
	}
	if got := attrValue(node, "loading"); got != "" {
		t.Errorf("attrValue(loading) = %q, want %q (absent)", got, "")
	}
}

// TestSetIfAbsentAppendsWhenAbsent: setIfAbsent adds the attribute exactly once
// when the key is not already present (Req 2.6).
func TestSetIfAbsentAppendsWhenAbsent(t *testing.T) {
	node := imgWithClass("wp-image-42")

	setIfAbsent(node, "loading", "lazy")

	if got := attrValue(node, "loading"); got != "lazy" {
		t.Errorf("after setIfAbsent, loading = %q, want %q", got, "lazy")
	}
	if n := countAttr(node, "loading"); n != 1 {
		t.Errorf("loading appears %d times, want 1", n)
	}
}

// TestSetIfAbsentLeavesExistingUntouched: setIfAbsent must NOT overwrite nor
// duplicate an attribute that is already present (Req 2.6 idempotence).
func TestSetIfAbsentLeavesExistingUntouched(t *testing.T) {
	node := imgWithAttrs(
		html.Attribute{Key: "class", Val: "wp-image-42"},
		html.Attribute{Key: "loading", Val: "eager"},
	)

	setIfAbsent(node, "loading", "lazy")

	if got := attrValue(node, "loading"); got != "eager" {
		t.Errorf("setIfAbsent overwrote existing: loading = %q, want %q", got, "eager")
	}
	if n := countAttr(node, "loading"); n != 1 {
		t.Errorf("setIfAbsent duplicated attribute: loading appears %d times, want 1", n)
	}
}

// countAttr counts how many times an attribute key appears on a node, used to
// assert setIfAbsent neither duplicates nor drops attributes.
func countAttr(n *html.Node, key string) int {
	c := 0
	for _, a := range n.Attr {
		if a.Key == key {
			c++
		}
	}
	return c
}
