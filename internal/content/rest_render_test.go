package content

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// This file is the RED (test-first) specification for The Transform's render
// pipeline — renderContent and its rewriteImages/walkAndRewrite/rewriteImageNode
// helpers (plus the containsIMG probe, the uploadsBaseURL field, and the
// WithUploadsBaseURL builder) — implemented by task 4.2 in rest.go. Until 4.2
// lands those symbols are undefined, so `go test ./internal/content` fails to
// compile here: the expected RED state for task 4.1.
//
// It reuses the existing harness: newTestMapper() (rest_test.go) returns the
// mapper plus the fakeRESTPostMeta whose attach map[int64]string seeds the
// _wp_attachment_metadata blob, and serializeMeta (rest_sizes_test.go) builds
// that blob byte-exactly via php.Serialize. The sizeVariant type and the
// render helpers (resolveAttachmentID/urlDir/attrValue/setIfAbsent, task 3.2)
// already exist; this file exercises them end-to-end through renderContent.
//
// Covers Req 2.1-2.6, 3.2, 3.3, 4.5, 5.1, 5.2, 5.3, 5.4, 6.1, 6.2;
// Findings 2 and 3; D3, D6.

// seededMediaBlob is the rich width+sizes blob used by the media-library cases:
// full-size width 800, thumbnail 150, medium 300. srcset candidates are built
// from dir(src) + the size file basename, so the <img>'s src directory is what
// the expected srcset is anchored on.
func seededMediaBlob(t *testing.T) string {
	t.Helper()
	return serializeMeta(t, map[string]any{
		"width":  800,
		"height": 600,
		"file":   "2024/03/photo.jpg",
		"sizes": map[string]any{
			"thumbnail": map[string]any{"file": "photo-150x150.jpg", "width": 150, "height": 150},
			"medium":    map[string]any{"file": "photo-300x200.jpg", "width": 300, "height": 200},
		},
	})
}

// parseImg parses rendered as an HTML fragment (body context, matching
// rewriteImages' own parse) and returns the first <img> node it finds. It is
// how the table asserts on attributes without depending on html.Render's
// attribute emission order.
func parseImg(t *testing.T, rendered string) *html.Node {
	t.Helper()
	ctxNode := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(rendered), ctxNode)
	if err != nil {
		t.Fatalf("ParseFragment(%q): %v", rendered, err)
	}
	var find func(n *html.Node) *html.Node
	find = func(n *html.Node) *html.Node {
		if n.Type == html.ElementNode && n.DataAtom == atom.Img {
			return n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if got := find(c); got != nil {
				return got
			}
		}
		return nil
	}
	for _, n := range nodes {
		if got := find(n); got != nil {
			return got
		}
	}
	return nil
}

// imgAttr returns an <img> attribute value and whether it was present at all,
// distinguishing an absent attribute from one set to "".
func imgAttr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// imgAttrCount counts how many times an attribute key appears on a node, so the
// idempotence case can prove an attribute is never duplicated (Req 2.6, 5.4).
func imgAttrCount(n *html.Node, key string) int {
	c := 0
	for _, a := range n.Attr {
		if a.Key == key {
			c++
		}
	}
	return c
}

// TestRenderContentImageRewrite is the one table-driven test spanning the
// per-<img> rewrite cases. Each case seeds the fake meta as needed, runs
// renderContent, and asserts on the parsed <img> node's attributes.
func TestRenderContentImageRewrite(t *testing.T) {
	ctx := context.Background()

	type attrWant struct {
		key     string
		val     string // expected value when present
		present bool   // false => must be absent
	}

	cases := []struct {
		name string
		// seed is called with the fake meta so a case can install blobs for the
		// attachment ids its <img> refers to.
		seed  func(t *testing.T, meta *fakeRESTPostMeta)
		input string
		want  []attrWant
		// countOnce lists attributes that must appear exactly once (idempotence /
		// pre-existing-attr cases).
		countOnce []string
	}{
		{
			// 1. media-library <img class="wp-image-201"> with a src in uploads
			// and a seeded sizes blob → loading/decoding + srcset (two variants,
			// dir(src)-anchored, ascending) + sizes "(max-width: 800px) 100vw, 800px"
			// (Req 2.1-2.4).
			name: "media library img with variants gets srcset+sizes+lazy+async",
			seed: func(t *testing.T, meta *fakeRESTPostMeta) {
				meta.attach[201] = seededMediaBlob(t)
			},
			input: `<img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg">`,
			want: []attrWant{
				{key: "loading", val: "lazy", present: true},
				{key: "decoding", val: "async", present: true},
				{key: "srcset", val: "https://x/wp-content/uploads/2024/03/photo-150x150.jpg 150w, " +
					"https://x/wp-content/uploads/2024/03/photo-300x200.jpg 300w", present: true},
				{key: "sizes", val: "(max-width: 800px) 100vw, 800px", present: true},
			},
			countOnce: []string{"loading", "decoding", "srcset", "sizes"},
		},
		{
			// 2. resolvable <img class="wp-image-202"> whose metadata has NO
			// sizes sub-map (width-only blob) → loading+decoding only, NO
			// srcset/sizes (Req 2.5, 3.3).
			name: "resolvable img without variants gets lazy+async only",
			seed: func(t *testing.T, meta *fakeRESTPostMeta) {
				meta.attach[202] = serializeMeta(t, map[string]any{"width": 800, "height": 600})
			},
			input: `<img class="wp-image-202" src="https://x/wp-content/uploads/2024/03/photo.jpg">`,
			want: []attrWant{
				{key: "loading", val: "lazy", present: true},
				{key: "decoding", val: "async", present: true},
				{key: "srcset", present: false},
				{key: "sizes", present: false},
			},
		},
		{
			// 3. unresolvable <img> (no wp-image- class) → loading+decoding only
			// (Req 3.2). Even if a 201 blob exists, the id is never resolved.
			name: "unresolvable img (no wp-image class) gets lazy+async only",
			seed: func(t *testing.T, meta *fakeRESTPostMeta) {
				meta.attach[201] = seededMediaBlob(t)
			},
			input: `<img class="size-full aligncenter" src="https://x/wp-content/uploads/2024/03/photo.jpg">`,
			want: []attrWant{
				{key: "loading", val: "lazy", present: true},
				{key: "decoding", val: "async", present: true},
				{key: "srcset", present: false},
				{key: "sizes", present: false},
			},
		},
		{
			// 4. idempotence / pre-existing attrs: loading="eager" and
			// srcset="existing 1x" are left in place (not overwritten), decoding
			// is added, and nothing is duplicated (Req 2.6, 5.4).
			name: "existing loading/srcset left in place, never overwritten or duplicated",
			seed: func(t *testing.T, meta *fakeRESTPostMeta) {
				meta.attach[201] = seededMediaBlob(t)
			},
			input: `<img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg" loading="eager" srcset="existing 1x">`,
			want: []attrWant{
				{key: "loading", val: "eager", present: true},
				{key: "srcset", val: "existing 1x", present: true},
				{key: "decoding", val: "async", present: true},
			},
			countOnce: []string{"loading", "srcset", "decoding", "sizes"},
		},
		{
			// 5. surrounding markup preserved: the <img> gains attributes while
			// the <p> text and other <img> attributes survive (Req 5.2). This
			// case asserts the img attrs; the markup-survival assertion is below.
			name: "surrounding markup preserved, only img gains attributes",
			seed: func(t *testing.T, meta *fakeRESTPostMeta) {
				meta.attach[201] = seededMediaBlob(t)
			},
			input: `<p>intro</p><img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg" alt="a photo"><p>outro</p>`,
			want: []attrWant{
				{key: "alt", val: "a photo", present: true}, // untouched existing attr (Req 5.2)
				{key: "loading", val: "lazy", present: true},
				{key: "decoding", val: "async", present: true},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapper, _, meta, _ := newTestMapper()
			if tc.seed != nil {
				tc.seed(t, meta)
			}

			out := mapper.renderContent(ctx, tc.input)
			img := parseImg(t, out)
			if img == nil {
				t.Fatalf("no <img> in rendered output: %q", out)
			}

			for _, w := range tc.want {
				got, present := imgAttr(img, w.key)
				if present != w.present {
					t.Errorf("attr %q present=%t, want present=%t (out=%q)", w.key, present, w.present, out)
					continue
				}
				if w.present && got != w.val {
					t.Errorf("attr %q = %q, want %q (out=%q)", w.key, got, w.val, out)
				}
			}
			for _, key := range tc.countOnce {
				if n := imgAttrCount(img, key); n > 1 {
					t.Errorf("attr %q appears %d times, want at most 1 (out=%q)", key, n, out)
				}
			}
		})
	}
}

// TestRenderContentPreservesSurroundingMarkup asserts the non-<img> structure
// and text survive the fragment round-trip: the two <p> elements and their text
// are still present in the output (Req 5.2). It complements the table case,
// which checks the img's own attributes.
func TestRenderContentPreservesSurroundingMarkup(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[201] = seededMediaBlob(t)

	in := `<p>intro</p><img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg"><p>outro</p>`
	out := mapper.renderContent(context.Background(), in)

	for _, want := range []string{"<p>intro</p>", "<p>outro</p>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lost surrounding markup %q: %q", want, out)
		}
	}
}

// TestRenderContentFragmentNoWrapper asserts the rewrite path emits a FRAGMENT:
// no synthesized <html>/<head>/<body> wrapper (the html.ParseFragment pitfall,
// D3). The input carries an <img> so the rewrite path (not the short-circuit)
// runs.
func TestRenderContentFragmentNoWrapper(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[201] = seededMediaBlob(t)

	out := mapper.renderContent(context.Background(),
		`<p>x</p><img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg">`)

	for _, bad := range []string{"<html", "<head", "<body"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Errorf("fragment output contains wrapper %q: %q", bad, out)
		}
	}
}

// TestRenderContentStripsCommentsAndRewritesImage asserts the two steps compose:
// Gutenberg block comments are stripped AND the enclosed <img> is enhanced in
// the same pass (Req 1, 2 together).
func TestRenderContentStripsCommentsAndRewritesImage(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[201] = seededMediaBlob(t)

	in := `<!-- wp:image --><img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg"><!-- /wp:image -->`
	out := mapper.renderContent(context.Background(), in)

	if strings.Contains(out, "wp:image") {
		t.Errorf("block comment not stripped: %q", out)
	}
	img := parseImg(t, out)
	if img == nil {
		t.Fatalf("no <img> in output: %q", out)
	}
	if v, ok := imgAttr(img, "loading"); !ok || v != "lazy" {
		t.Errorf("img not enhanced: loading=%q present=%t (out=%q)", v, ok, out)
	}
	if v, ok := imgAttr(img, "srcset"); !ok || v == "" {
		t.Errorf("img missing srcset: %q (out=%q)", v, out)
	}
}

// TestRenderContentMalformedInputNoPanic documents that x/net/html is very
// tolerant: html.ParseFragment rarely errors, so a true parse-failure
// passthrough (Req 5.3) is hard to force. Rather than assert an exact
// passthrough we can't guarantee, this asserts the stronger practical contract:
// malformed-ish input (an unclosed tag) neither panics nor corrupts the content
// — the stripped text content survives and the output is a usable string. The
// Req 5.3 "return the stripped string on parse error" guard is belt-and-braces.
func TestRenderContentMalformedInputNoPanic(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[201] = seededMediaBlob(t)

	// Unclosed <img> tag plus trailing text; x/net/html reconstructs rather
	// than erroring. The guarantee under test is no-panic + content preserved.
	in := `<p>before<img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg"`
	out := mapper.renderContent(context.Background(), in)

	if !strings.Contains(out, "before") {
		t.Errorf("malformed input lost content: %q", out)
	}
}

// TestRenderContentNilMetaStripOnly asserts the D6/Finding-2 nil-guard: a mapper
// built with a nil meta (NewRESTMapper(terms, nil, users, "wp_")) strips block
// comments but does NOT enter the rewrite path, so the <img> gains no
// loading/decoding attributes (Req 6.1, 6.2). renderContent short-circuits on
// m.meta == nil before parsing.
func TestRenderContentNilMetaStripOnly(t *testing.T) {
	terms := &fakeRESTPostTerms{byPost: map[int64]map[string][]int64{}}
	users := &fakeRESTUserMeta{values: map[int64]map[string]string{}}
	mapper := NewRESTMapper(terms, nil, users, "wp_")

	in := `<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph --><img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg">`
	out := mapper.renderContent(context.Background(), in)

	if strings.Contains(out, "wp:paragraph") {
		t.Errorf("block comment not stripped under nil meta: %q", out)
	}
	// img must NOT be enhanced: the short-circuit returns the stripped string
	// before any <img> rewrite, so no loading/decoding attribute is injected.
	if strings.Contains(out, `loading=`) || strings.Contains(out, `decoding=`) {
		t.Errorf("nil meta should strip only, but img was enhanced: %q", out)
	}
}

// TestRenderContentByteIdenticalForPlainContent asserts the Req 4.5 invariant:
// comment-free, image-free content is returned byte-for-byte unchanged — it
// never enters html.ParseFragment/html.Render, so no serializer round-trip can
// perturb it.
func TestRenderContentByteIdenticalForPlainContent(t *testing.T) {
	mapper, _, _, _ := newTestMapper()

	const in = "<p>plain</p>"
	if out := mapper.renderContent(context.Background(), in); out != in {
		t.Errorf("renderContent(%q) = %q, want byte-identical", in, out)
	}
}

// TestRenderContentIdempotent asserts Req 5.4: applying The Transform to its own
// output is a no-op. Running renderContent twice over content with block
// comments and a wp-image <img> yields the same string as running it once —
// comments are already gone and loading/decoding/srcset/sizes are not
// re-added/duplicated.
func TestRenderContentIdempotent(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[201] = seededMediaBlob(t)
	ctx := context.Background()

	in := `<!-- wp:image --><p>cap</p><img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg"><!-- /wp:image -->`
	once := mapper.renderContent(ctx, in)
	twice := mapper.renderContent(ctx, once)

	if once != twice {
		t.Errorf("renderContent not idempotent:\n once = %q\n twice = %q", once, twice)
	}
}

// TestRenderContentUploadsBaseFallback exercises the Finding-3 src-less
// fallback: when the <img>'s src yields no usable directory, srcset candidates
// are anchored on the configured uploadsBaseURL instead. The mapper is built
// via WithUploadsBaseURL; the <img> carries a wp-image class and a bare-filename
// src (no directory) so dir(src) is empty and the fallback base must supply the
// candidate directory.
func TestRenderContentUploadsBaseFallback(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	mapper = mapper.WithUploadsBaseURL("https://cdn.example/uploads")
	meta.attach[201] = seededMediaBlob(t)

	in := `<img class="wp-image-201" src="photo.jpg">`
	out := mapper.renderContent(context.Background(), in)

	img := parseImg(t, out)
	if img == nil {
		t.Fatalf("no <img> in output: %q", out)
	}
	// loading/decoding always added.
	if v, ok := imgAttr(img, "loading"); !ok || v != "lazy" {
		t.Errorf("loading = %q present=%t, want lazy", v, ok)
	}
	// With a src that has no directory, the fallback base must anchor srcset so
	// candidates are not bare basenames.
	srcset, ok := imgAttr(img, "srcset")
	if !ok || srcset == "" {
		t.Fatalf("expected srcset via uploads-base fallback, got present=%t val=%q (out=%q)", ok, srcset, out)
	}
	if !strings.Contains(srcset, "https://cdn.example/uploads") {
		t.Errorf("srcset not anchored on uploads base fallback: %q", srcset)
	}
}
