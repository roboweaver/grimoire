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

// TestUserProfileFieldsHasExactlyFourPointerFields asserts domain.UserProfileFields
// is the write-side value object with EXACTLY DisplayName, Email, URL, Nicename as
// *string fields and nothing else, mirroring domain.MediaMeta from M10.B. The keyed
// literal below guards rename/removal at compile time (an added, removed, or renamed
// field breaks this file); the reflect block guards "nothing else" and the *string
// kind, matching the TestMediaMetaHasExactlyFourStringFields precedent (Req 3.1).
func TestUserProfileFieldsHasExactlyFourPointerFields(t *testing.T) {
	// Keyed literal: naming all four fields makes an add/remove/rename a compile
	// error here, which is the "exactly four fields and nothing else" intent.
	display, email, url, nicename := "d", "e@example.com", "https://example.com", "n"
	_ = UserProfileFields{
		DisplayName: &display,
		Email:       &email,
		URL:         &url,
		Nicename:    &nicename,
	}

	ut := reflect.TypeOf(UserProfileFields{})

	want := map[string]struct{}{
		"DisplayName": {},
		"Email":       {},
		"URL":         {},
		"Nicename":    {},
	}

	if got := ut.NumField(); got != len(want) {
		t.Fatalf("domain.UserProfileFields has %d fields, want exactly %d", got, len(want))
	}

	for i := 0; i < ut.NumField(); i++ {
		f := ut.Field(i)
		if _, ok := want[f.Name]; !ok {
			t.Fatalf("domain.UserProfileFields has unexpected field %q", f.Name)
		}
		if f.Type.Kind() != reflect.Ptr || f.Type.Elem().Kind() != reflect.String {
			t.Fatalf("domain.UserProfileFields.%s type = %s, want *string", f.Name, f.Type)
		}
	}
}

// TestUserProfileFieldsAny asserts the unexported any() helper reports false for an
// all-nil value object and true as soon as any single field is non-nil (one subtest
// per field), matching the MediaMeta/any() posture the service's partial-update guard
// relies on (Req 3.1).
func TestUserProfileFieldsAny(t *testing.T) {
	if (UserProfileFields{}).any() {
		t.Fatal("UserProfileFields{}.any() = true, want false for an all-nil value")
	}

	v := "x"
	cases := map[string]UserProfileFields{
		"DisplayName": {DisplayName: &v},
		"Email":       {Email: &v},
		"URL":         {URL: &v},
		"Nicename":    {Nicename: &v},
	}
	for field, f := range cases {
		f := f
		t.Run(field, func(t *testing.T) {
			if !f.any() {
				t.Fatalf("UserProfileFields{%s set}.any() = false, want true", field)
			}
		})
	}
}
