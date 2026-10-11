package domain

import (
	"reflect"
	"testing"
)

// TestMediaCarriesEditableMetadataFields asserts domain.Media carries the
// read-side editable-metadata fields (Caption, AltText, Description) as strings,
// zero-valued for an existing reader that does not set them (Finding 2/3,
// Req 10.3). The fields are additive: the pre-10.B fields remain readable.
func TestMediaCarriesEditableMetadataFields(t *testing.T) {
	// Zero-valued for a reader that constructs Media the pre-10.B way.
	m := Media{ID: 5, ParentID: 6, Filename: "2026/08/pic.jpg", MimeType: "image/jpeg"}
	if m.Caption != "" {
		t.Fatalf("default Media.Caption = %q, want empty", m.Caption)
	}
	if m.AltText != "" {
		t.Fatalf("default Media.AltText = %q, want empty", m.AltText)
	}
	if m.Description != "" {
		t.Fatalf("default Media.Description = %q, want empty", m.Description)
	}

	// Each field is a settable string.
	m.Caption = "a caption"
	m.AltText = "alt text"
	m.Description = "a description"
	if m.Caption != "a caption" || m.AltText != "alt text" || m.Description != "a description" {
		t.Fatal("Media Caption/AltText/Description not wired as string fields")
	}

	// Verify the field kinds explicitly so a future retype is caught.
	mt := reflect.TypeOf(Media{})
	for _, name := range []string{"Caption", "AltText", "Description"} {
		f, ok := mt.FieldByName(name)
		if !ok {
			t.Fatalf("domain.Media missing field %q", name)
		}
		if f.Type.Kind() != reflect.String {
			t.Fatalf("domain.Media.%s kind = %s, want string", name, f.Type.Kind())
		}
	}
}

// TestMediaMetaHasExactlyFourStringFields asserts domain.MediaMeta is the
// write-side value object with EXACTLY Title, Caption, AltText, Description as
// string fields and nothing else — in particular NO parent field, which is
// excluded by design and continues to flow through SetParent (Req 3.5, D2).
func TestMediaMetaHasExactlyFourStringFields(t *testing.T) {
	mt := reflect.TypeOf(MediaMeta{})

	want := map[string]struct{}{
		"Title":       {},
		"Caption":     {},
		"AltText":     {},
		"Description": {},
	}

	if got := mt.NumField(); got != len(want) {
		t.Fatalf("domain.MediaMeta has %d fields, want exactly %d", got, len(want))
	}

	for i := 0; i < mt.NumField(); i++ {
		f := mt.Field(i)
		if _, ok := want[f.Name]; !ok {
			t.Fatalf("domain.MediaMeta has unexpected field %q (parent and others must not be present)", f.Name)
		}
		if f.Type.Kind() != reflect.String {
			t.Fatalf("domain.MediaMeta.%s kind = %s, want string", f.Name, f.Type.Kind())
		}
	}

	// Guard the exclusion explicitly: no parent field under any common name.
	for _, forbidden := range []string{"Parent", "ParentID", "Post", "PostID"} {
		if _, ok := mt.FieldByName(forbidden); ok {
			t.Fatalf("domain.MediaMeta must not carry a parent field, found %q", forbidden)
		}
	}

	// Fields are settable strings.
	mm := MediaMeta{Title: "t", Caption: "c", AltText: "a", Description: "d"}
	if mm.Title != "t" || mm.Caption != "c" || mm.AltText != "a" || mm.Description != "d" {
		t.Fatal("MediaMeta fields not wired as strings")
	}
}
