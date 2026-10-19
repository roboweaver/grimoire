package content

import (
	"context"
	"sort"
	"testing"

	"github.com/roboweaver/grimoire/internal/php"
)

// serializeMeta builds a PHP-serialized _wp_attachment_metadata blob from a Go
// map, delegating byte-exact string-length counting to php.Serialize so the
// fixtures can never drift (Finding 4). It mirrors the shape of the shared
// storagetest contract fixture (internal/storage/storagetest/contract.go:176)
// — a width/height-only blob — extended here with a "sizes" sub-map that the
// cross-vendor fixture deliberately omits.
func serializeMeta(t *testing.T, v map[string]any) string {
	t.Helper()
	blob, err := php.Serialize(v)
	if err != nil {
		t.Fatalf("php.Serialize: %v", err)
	}
	return blob
}

// variantKey normalizes a sizeVariant to a comparable key so a decoded slice,
// whose order is nondeterministic (the "sizes" sub-map is a Go map), can be
// asserted as a set rather than a fixed sequence (task 2.1 note; the stable
// ascending-by-width ordering is srcsetFromSizes's job in task 3.x, not
// sizesFromMetadata's).
func variantKey(v sizeVariant) [3]int {
	// File is folded into the key via its length+content through a cheap hash
	// avoided here: instead sort+compare the full structs below. variantKey is
	// retained only for the width ordering used in sortVariants.
	return [3]int{v.Width, v.Height, len(v.File)}
}

func sortVariants(vs []sizeVariant) {
	sort.Slice(vs, func(i, j int) bool {
		a, b := variantKey(vs[i]), variantKey(vs[j])
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		if a[1] != b[1] {
			return a[1] < b[1]
		}
		if a[2] != b[2] {
			return a[2] < b[2]
		}
		return vs[i].File < vs[j].File
	})
}

func sameVariantSet(got, want []sizeVariant) bool {
	if len(got) != len(want) {
		return false
	}
	g := append([]sizeVariant(nil), got...)
	w := append([]sizeVariant(nil), want...)
	sortVariants(g)
	sortVariants(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

// TestSizesFromMetadataRichBlob asserts a blob with a top-level width and a
// sizes sub-map decodes to the expected []sizeVariant plus the full-size width
// (Req 2.2, 3.4; D5). The expected sizeVariant{File, Width, Height} shape is
// defined by comparing against literals; the type itself is declared by task
// 2.2 (RED until then).
func TestSizesFromMetadataRichBlob(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	const id int64 = 201
	meta.attach[id] = serializeMeta(t, map[string]any{
		"width":  800,
		"height": 600,
		"sizes": map[string]any{
			"thumbnail": map[string]any{"file": "photo-150x150.jpg", "width": 150, "height": 150},
			"medium":    map[string]any{"file": "photo-300x200.jpg", "width": 300, "height": 200},
		},
	})

	variants, fullW := mapper.sizesFromMetadata(context.Background(), id)

	if fullW != 800 {
		t.Errorf("fullWidth = %d, want 800", fullW)
	}
	want := []sizeVariant{
		{File: "photo-150x150.jpg", Width: 150, Height: 150},
		{File: "photo-300x200.jpg", Width: 300, Height: 200},
	}
	if !sameVariantSet(variants, want) {
		t.Errorf("variants = %+v, want set %+v", variants, want)
	}
}

// TestSizesFromMetadataEmptyBlob: an empty blob yields (nil, 0) (Req 3.3). The
// fake returns "" for an unseeded id, exactly like the real
// PostMetaRepository.AttachmentMetadata when _wp_attachment_metadata is unset.
func TestSizesFromMetadataEmptyBlob(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	const id int64 = 202
	meta.attach[id] = ""

	variants, fullW := mapper.sizesFromMetadata(context.Background(), id)
	if variants != nil || fullW != 0 {
		t.Errorf("empty blob => (%+v, %d), want (nil, 0)", variants, fullW)
	}
}

// TestSizesFromMetadataMissingBlob: an id the repository has never seen also
// yields (nil, 0) — same "no variants" tolerance as the empty case (Req 3.3).
func TestSizesFromMetadataMissingBlob(t *testing.T) {
	mapper, _, _, _ := newTestMapper()

	variants, fullW := mapper.sizesFromMetadata(context.Background(), 9999)
	if variants != nil || fullW != 0 {
		t.Errorf("missing blob => (%+v, %d), want (nil, 0)", variants, fullW)
	}
}

// TestSizesFromMetadataMalformedBlob: a blob that is not valid PHP serialize()
// yields (nil, 0) rather than an error or panic (Req 3.3).
func TestSizesFromMetadataMalformedBlob(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	const id int64 = 203
	meta.attach[id] = "not-valid-php"

	variants, fullW := mapper.sizesFromMetadata(context.Background(), id)
	if variants != nil || fullW != 0 {
		t.Errorf("malformed blob => (%+v, %d), want (nil, 0)", variants, fullW)
	}
}

// TestSizesFromMetadataNoSizesKey: a blob with a top-level width but no "sizes"
// key yields (nil, fullWidth) — the full-size width is preserved so the caller
// can still omit srcset while keeping lazy/decoding (Req 2.5). This mirrors the
// shared storagetest contract fixture, which seeds width/height only.
func TestSizesFromMetadataNoSizesKey(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	const id int64 = 204
	meta.attach[id] = serializeMeta(t, map[string]any{"width": 800, "height": 600})

	variants, fullW := mapper.sizesFromMetadata(context.Background(), id)
	if variants != nil {
		t.Errorf("variants = %+v, want nil (no sizes sub-map)", variants)
	}
	if fullW != 800 {
		t.Errorf("fullWidth = %d, want 800 (preserved despite no sizes)", fullW)
	}
}

// TestSizesFromMetadataNilMeta: a mapper with nil m.meta degrades to (nil, 0)
// — the nil-guard that honors D6's intent (never panic) through a required
// constructor arg (Finding 2). The mapper is built with NewRESTMapper's meta
// positional arg set to nil.
func TestSizesFromMetadataNilMeta(t *testing.T) {
	terms := &fakeRESTPostTerms{byPost: map[int64]map[string][]int64{}}
	users := &fakeRESTUserMeta{values: map[int64]map[string]string{}}
	mapper := NewRESTMapper(terms, nil, users, "wp_")

	variants, fullW := mapper.sizesFromMetadata(context.Background(), 201)
	if variants != nil || fullW != 0 {
		t.Errorf("nil m.meta => (%+v, %d), want (nil, 0)", variants, fullW)
	}
}

// TestSizesFromMetadataSkipsIncompleteEntries: entries missing "file" or with
// width <= 0 are skipped rather than emitted as wrong srcset candidates; only
// the complete entry is returned, alongside the full-size width (Finding 4;
// design "skip entries with empty file or width<=0").
func TestSizesFromMetadataSkipsIncompleteEntries(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	const id int64 = 205
	meta.attach[id] = serializeMeta(t, map[string]any{
		"width": 500,
		"sizes": map[string]any{
			"good":      map[string]any{"file": "photo-300x200.jpg", "width": 300, "height": 200},
			"nofile":    map[string]any{"width": 150, "height": 150},
			"zerowidth": map[string]any{"file": "photo-bad.jpg", "width": 0, "height": 0},
		},
	})

	variants, fullW := mapper.sizesFromMetadata(context.Background(), id)
	if fullW != 500 {
		t.Errorf("fullWidth = %d, want 500", fullW)
	}
	want := []sizeVariant{{File: "photo-300x200.jpg", Width: 300, Height: 200}}
	if !sameVariantSet(variants, want) {
		t.Errorf("variants = %+v, want only the complete entry %+v", variants, want)
	}
}
