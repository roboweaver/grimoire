package wprepo

import (
	"context"
	"math/rand"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/quick"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	sqlitemig "github.com/roboweaver/grimoire/internal/storage/migrations/sqlite"
	sqliteadapter "github.com/roboweaver/grimoire/internal/storage/sqlite"
)

// The pure decision logic behind OptionRepo.CleanAttachmentReferences is
// exercised through decideThemeModCleanup:
//
//	func decideThemeModCleanup(rawMods string, optionValues map[string]string,
//	    attachmentID int64, attachmentURL string) themeModDecision
//
// rawMods is the stored theme_mods_{stylesheet} value, "" when absent. The
// value is edited by byte span, never decoded and re-encoded, so a result is
// compared as raw bytes: the original with exactly the removed top-level
// entries cut out and the a:N: count fixed.
//
// The helpers below build ordered serialized values for fixtures and for the
// reference, independently of the scanner under test.

// tmdKV is one entry of an ordered string-keyed PHP array.
type tmdKV struct {
	k string
	v any
}

// tmdList is an ordered string-keyed PHP array. nil is "absent".
type tmdList []tmdKV

// tmdIntList is a PHP list (integer keys 0..n-1).
type tmdIntList []any

// tmdRawV is a serialized fragment emitted verbatim (objects, odd shapes).
type tmdRawV string

// tmdFloat is a PHP float emitted as d:repr;.
type tmdFloat string

func tmdStr(s string) string { return "s:" + strconv.Itoa(len(s)) + `:"` + s + `";` }

// tmdEnc serializes v. Strings, ints, bools, nil and the types above are supported.
func tmdEnc(v any) string {
	switch x := v.(type) {
	case nil:
		return "N;"
	case bool:
		if x {
			return "b:1;"
		}
		return "b:0;"
	case int:
		return "i:" + strconv.Itoa(x) + ";"
	case string:
		return tmdStr(x)
	case tmdFloat:
		return "d:" + string(x) + ";"
	case tmdRawV:
		return string(x)
	case tmdIntList:
		var b strings.Builder
		b.WriteString("a:" + strconv.Itoa(len(x)) + ":{")
		for i, e := range x {
			b.WriteString("i:" + strconv.Itoa(i) + ";" + tmdEnc(e))
		}
		b.WriteString("}")
		return b.String()
	case tmdList:
		var b strings.Builder
		b.WriteString("a:" + strconv.Itoa(len(x)) + ":{")
		for _, kv := range x {
			b.WriteString(tmdStr(kv.k) + tmdEnc(kv.v))
		}
		b.WriteString("}")
		return b.String()
	}
	panic("tmdEnc: unsupported type")
}

// tmdMods builds an ordered mods list from alternating key/value arguments.
func tmdMods(kv ...any) tmdList {
	m := tmdList{}
	for i := 0; i+1 < len(kv); i += 2 {
		m = append(m, tmdKV{kv[i].(string), kv[i+1]})
	}
	return m
}

func tmdWithout(m tmdList, keys ...string) tmdList {
	out := tmdList{}
	for _, e := range m {
		drop := false
		for _, k := range keys {
			drop = drop || e.k == k
		}
		if !drop {
			out = append(out, e)
		}
	}
	return out
}

func tmdRaw(m tmdList) string {
	if m == nil {
		return ""
	}
	return tmdEnc(m)
}

const (
	tmdRelURL = "/wp-content/uploads/2024/05/logo.png"
	tmdURL    = "https://example.test" + tmdRelURL
)

func TestThemeModDecisionExamples(t *testing.T) {
	cases := []struct {
		name       string
		mods       tmdList
		opts       map[string]string
		id         int64
		url        string
		wantMods   tmdList
		wantChange bool
		wantDelRow bool
		wantOpts   []string
	}{
		{
			name:       "custom_logo id match removes custom_logo and header_text only",
			mods:       tmdMods("custom_logo", 42, "header_text", "blank", "header_image", "https://x/y.png", "color", "red"),
			id:         42,
			url:        tmdURL,
			wantMods:   tmdMods("header_image", "https://x/y.png", "color", "red"),
			wantChange: true,
		},
		{
			name:       "custom_logo stored as a numeric string matches",
			mods:       tmdMods("custom_logo", "42", "header_text", false, "keep", 1),
			id:         42,
			url:        tmdURL,
			wantMods:   tmdMods("keep", 1),
			wantChange: true,
		},
		{
			name: "custom_logo for a different id changes nothing, header_text included",
			mods: tmdMods("custom_logo", 420, "header_text", "x"),
			id:   42,
			url:  tmdURL,
		},
		{
			name:       "header_image URL match removes header_image and header_image_data only",
			mods:       tmdMods("header_image", tmdURL, "header_image_data", tmdMods("attachment_id", 9), "header_text", "x", "custom_logo", 7),
			id:         42,
			url:        tmdURL,
			wantMods:   tmdMods("header_text", "x", "custom_logo", 7),
			wantChange: true,
		},
		{
			name: "header_image_data survives when header_image does not match",
			mods: tmdMods("header_image", "https://x/other.png", "header_image_data", tmdMods("attachment_id", 42)),
			id:   42,
			url:  tmdURL,
		},
		{
			name: "header_image_data alone is never a match",
			mods: tmdMods("header_image_data", tmdMods("url", tmdURL, "attachment_id", 42)),
			id:   42,
			url:  tmdURL,
		},
		{
			name:       "background_image URL match removes only background_image",
			mods:       tmdMods("background_image", tmdURL, "background_color", "fff", "header_image", "https://x/h.png"),
			id:         42,
			url:        tmdURL,
			wantMods:   tmdMods("background_color", "fff", "header_image", "https://x/h.png"),
			wantChange: true,
		},
		{
			name: "a query, or an upper-case scheme and path, does not match",
			mods: tmdMods("header_image", tmdURL+"?x=1", "background_image", "HTTPS://EXAMPLE.TEST/wp-content/uploads/2024/05/logo.png"),
			id:   42,
			url:  tmdURL,
		},
		{
			name:       "absolute https header_image matches a relative attachment URL",
			mods:       tmdMods("header_image", tmdURL, "header_image_data", "d", "keep", 1),
			id:         42,
			url:        tmdRelURL,
			wantMods:   tmdMods("keep", 1),
			wantChange: true,
		},
		{
			name:       "absolute http background_image with a port matches a relative attachment URL",
			mods:       tmdMods("background_image", "http://example.test:8080"+tmdRelURL, "keep", 1),
			id:         42,
			url:        tmdRelURL,
			wantMods:   tmdMods("keep", 1),
			wantChange: true,
		},
		{
			name:       "relative header_image matches an absolute attachment URL",
			mods:       tmdMods("header_image", tmdRelURL, "header_image_data", "d", "keep", 1),
			id:         42,
			url:        tmdURL,
			wantMods:   tmdMods("keep", 1),
			wantChange: true,
		},
		{
			name:       "absolute values on different hosts both match the same path",
			mods:       tmdMods("header_image", "https://a.test"+tmdRelURL, "background_image", "http://b.test:81"+tmdRelURL, "keep", 1),
			id:         42,
			url:        "https://c.test" + tmdRelURL,
			wantMods:   tmdMods("keep", 1),
			wantChange: true,
		},
		{
			name: "a different path never matches, absolute or relative",
			mods: tmdMods("header_image", "https://example.test/wp-content/uploads/2024/05/other.png", "background_image", "/wp-content/uploads/2024/06/logo.png"),
			id:   42,
			url:  tmdRelURL,
		},
		{
			name: "the same basename in another directory does not match",
			mods: tmdMods("header_image", "https://example.test/wp-content/uploads/2023/01/logo.png", "background_image", "/uploads/logo.png"),
			id:   42,
			url:  tmdRelURL,
		},
		{
			name: "a value that is not a URL does not match",
			mods: tmdMods("header_image", "logo.png", "background_image", "ftp://example.test"+tmdRelURL),
			id:   42,
			url:  tmdRelURL,
		},
		{
			name: "an empty value or an empty attachment URL does not match",
			mods: tmdMods("header_image", "", "background_image", tmdURL),
			id:   42,
			url:  "",
		},
		{
			name: "a prefix or suffix overlap does not match",
			mods: tmdMods("header_image", "/uploads/xa.jpg", "background_image", "https://example.test/uploads/a.jpg.bak"),
			id:   42,
			url:  "/uploads/a.jpg",
		},
		{
			name: "a value or attachment URL with a query or fragment does not match",
			mods: tmdMods("header_image", tmdURL+"?v=1", "background_image", tmdURL+"#top"),
			id:   42,
			url:  tmdRelURL,
		},
		{
			name: "an attachment URL with a query does not match a clean value",
			mods: tmdMods("header_image", tmdURL, "background_image", tmdRelURL),
			id:   42,
			url:  tmdRelURL + "?v=1",
		},
		{
			name: "path case and percent-encoding differences do not match",
			mods: tmdMods("header_image", "https://example.test/wp-content/uploads/2024/05/LOGO.png", "background_image", "https://example.test/wp-content/uploads/2024/05/my%20logo.png"),
			id:   42,
			url:  "/wp-content/uploads/2024/05/my logo.png",
		},
		{
			name: "a protocol-relative value does not match",
			mods: tmdMods("header_image", "//example.test"+tmdRelURL),
			id:   42,
			url:  tmdRelURL,
		},
		{
			name: "empty attachment URL never matches an empty header_image or background_image",
			mods: tmdMods("header_image", "", "background_image", "", "header_image_data", "d"),
			id:   42,
			url:  "",
		},
		{
			name: "a non-string header_image is not a URL match",
			mods: tmdMods("header_image", 42, "background_image", true),
			id:   42,
			url:  "42",
		},
		{
			name:       "one attachment matching several mods clears all of them",
			mods:       tmdMods("custom_logo", 42, "header_text", "x", "header_image", tmdURL, "header_image_data", "d", "background_image", tmdURL, "other", "keep"),
			id:         42,
			url:        tmdURL,
			wantMods:   tmdMods("other", "keep"),
			wantChange: true,
		},
		{
			name:       "emptying the map signals row deletion",
			mods:       tmdMods("custom_logo", 42, "header_text", "x"),
			id:         42,
			url:        tmdURL,
			wantMods:   tmdMods(),
			wantChange: true,
			wantDelRow: true,
		},
		{
			name:       "emptying through a URL match signals row deletion",
			mods:       tmdMods("background_image", tmdURL),
			id:         42,
			url:        tmdURL,
			wantMods:   tmdMods(),
			wantChange: true,
			wantDelRow: true,
		},
		{
			name: "an already-empty map with no match is not a deletion",
			mods: tmdMods(),
			id:   42,
			url:  tmdURL,
		},
		{
			name: "core does not touch site_icon or site_logo keys inside theme mods",
			mods: tmdMods("site_icon", 42, "site_logo", 42),
			id:   42,
			url:  tmdURL,
		},
		{
			name:     "site_logo option id match deletes the option",
			mods:     tmdMods("keep", 1),
			opts:     map[string]string{"site_logo": "42"},
			id:       42,
			url:      tmdURL,
			wantMods: tmdMods("keep", 1),
			wantOpts: []string{"site_logo"},
		},
		{
			name:     "site_icon option id match deletes the option",
			opts:     map[string]string{"site_icon": "42"},
			id:       42,
			url:      tmdURL,
			wantOpts: []string{"site_icon"},
		},
		{
			name:     "both options matching are both deleted, in ascending order",
			opts:     map[string]string{"site_icon": "42", "site_logo": "42"},
			id:       42,
			url:      tmdURL,
			wantOpts: []string{"site_icon", "site_logo"},
		},
		{
			name:     "a PHP-serialized integer option value matches",
			opts:     map[string]string{"site_logo": "i:42;", "site_icon": "i:7;"},
			id:       42,
			url:      tmdURL,
			wantOpts: []string{"site_logo"},
		},
		{
			name: "option values that are a different id or not an integer are left alone",
			opts: map[string]string{"site_logo": "420", "site_icon": "abc"},
			id:   42,
			url:  tmdURL,
		},
		{
			name: "empty and whitespace-only option values are not parsable",
			opts: map[string]string{"site_logo": "", "site_icon": "  "},
			id:   42,
			url:  tmdURL,
		},
		{
			name: "an unmatched option does not depend on the theme mod match",
			mods: tmdMods("custom_logo", 42),
			opts: map[string]string{"site_icon": "7"},
			id:   42,
			url:  tmdURL,
			// custom_logo matches, site_icon does not.
			wantMods:   tmdMods(),
			wantChange: true,
			wantDelRow: true,
		},
		{
			name: "no match anywhere changes nothing",
			mods: tmdMods("custom_logo", 7, "header_text", "x", "header_image", "https://x/y.png", "background_image", "https://x/b.png"),
			opts: map[string]string{"site_logo": "8", "site_icon": "9"},
			id:   42,
			url:  tmdURL,
		},
		{
			name:     "absent or malformed theme mods (nil) is a no-op for mods but options still clean",
			mods:     nil,
			opts:     map[string]string{"site_icon": "42"},
			id:       42,
			url:      tmdURL,
			wantMods: nil,
			wantOpts: []string{"site_icon"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tmdRaw(tc.mods)
			got := decideThemeModCleanup(raw, tc.opts, tc.id, tc.url)

			wantMods := raw // unchanged, byte for byte
			if tc.wantChange {
				wantMods = ""
				if !tc.wantDelRow {
					wantMods = tmdRaw(tc.wantMods)
				}
			}
			if got.Mods != wantMods {
				t.Errorf("Mods = %q, want %q", got.Mods, wantMods)
			}
			if got.ModsChanged != tc.wantChange {
				t.Errorf("ModsChanged = %v, want %v", got.ModsChanged, tc.wantChange)
			}
			if got.DeleteModsRow != tc.wantDelRow {
				t.Errorf("DeleteModsRow = %v, want %v", got.DeleteModsRow, tc.wantDelRow)
			}
			if len(got.DeleteOptions) != len(tc.wantOpts) || (len(tc.wantOpts) > 0 && !reflect.DeepEqual(got.DeleteOptions, tc.wantOpts)) {
				t.Errorf("DeleteOptions = %v, want %v", got.DeleteOptions, tc.wantOpts)
			}
		})
	}
}

// TestThemeModBytePreservation proves that only the removed spans change.
func TestThemeModBytePreservation(t *testing.T) {
	const u = "/u/a"
	cases := []struct {
		name, in, want string
		id             int64
	}{
		{
			name: "object, float, integer-keyed and brace/semicolon strings survive around a removed pair",
			in:   `a:4:{s:1:"f";d:0.5;s:12:"header_image";s:4:"/u/a";s:17:"header_image_data";O:8:"stdClass":1:{s:1:"a";i:1;}s:1:"k";a:2:{i:5;s:3:"};{";i:9;N;}}`,
			want: `a:2:{s:1:"f";d:0.5;s:1:"k";a:2:{i:5;s:3:"};{";i:9;N;}}`,
			id:   1,
		},
		{
			name: "object-valued header_image_data is cut whole, with its nested braces",
			in:   `a:3:{s:17:"header_image_data";O:8:"stdClass":2:{s:3:"url";s:5:"a}b;c";s:1:"n";a:1:{i:0;O:8:"stdClass":0:{}}}s:12:"header_image";s:4:"/u/a";s:1:"z";d:-1.0E+25;}`,
			want: `a:1:{s:1:"z";d:-1.0E+25;}`,
			id:   1,
		},
		{
			name: "custom_logo match leaves an object-valued neighbour byte-identical",
			in:   `a:4:{s:11:"custom_logo";s:2:"42";s:6:"widget";O:3:"Foo":1:{s:4:"` + "\x00*\x00" + `p";i:7;}s:11:"header_text";b:0;s:4:"ints";a:3:{i:0;i:1;i:2;d:2.5;i:7;a:0:{}}}`,
			want: `a:2:{s:6:"widget";O:3:"Foo":1:{s:4:"` + "\x00*\x00" + `p";i:7;}s:4:"ints";a:3:{i:0;i:1;i:2;d:2.5;i:7;a:0:{}}}`,
			id:   42,
		},
		{
			name: "background_image removal keeps entry order and unrelated header_image",
			in:   `a:3:{s:12:"header_image";s:3:"x/y";s:16:"background_image";s:4:"/u/a";s:1:"k";i:9;}`,
			want: `a:2:{s:12:"header_image";s:3:"x/y";s:1:"k";i:9;}`,
			id:   1,
		},
		{
			name: "integer keys named like a mod are not matched",
			in:   `a:2:{i:0;s:4:"/u/a";s:1:"k";i:9;}`,
			want: `a:2:{i:0;s:4:"/u/a";s:1:"k";i:9;}`,
			id:   1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideThemeModCleanup(tc.in, nil, tc.id, u)
			if got.Mods != tc.want {
				t.Errorf("Mods = %q\nwant   %q", got.Mods, tc.want)
			}
			if wantChange := tc.in != tc.want; got.ModsChanged != wantChange {
				t.Errorf("ModsChanged = %v, want %v", got.ModsChanged, wantChange)
			}
		})
	}
}

// TestThemeModMalformedIsSkipped covers values the scanner must refuse.
func TestThemeModMalformedIsSkipped(t *testing.T) {
	for _, raw := range []string{
		"", " ", "not serialized", `s:3:"abc";`, "i:42;", "N;", "a:0:{}x",
		`a:2:{s:11:"custom_logo";i:42;}`,                   // count too high
		`a:1:{s:11:"custom_logo";i:42;`,                    // no closing brace
		`a:1:{s:11:"custom_logo";i:42;}trailing`,           // trailing bytes
		`a:1:{s:12:"custom_logo";i:42;}`,                   // wrong length prefix
		`a:1:{s:11:"custom_logo";q:42;}`,                   // unknown marker
		`a:1:{b:1;i:42;}`,                                  // bad key type
		`a:1:{s:11:"custom_logo";O:8:"stdClass":1:{i:0;}}`, // truncated object
	} {
		got := decideThemeModCleanup(raw, map[string]string{"site_logo": "42"}, 42, tmdURL)
		if got.ModsChanged || got.DeleteModsRow || got.Mods != raw {
			t.Errorf("%q: decision %+v, want the value untouched", raw, got)
		}
		if !reflect.DeepEqual(got.DeleteOptions, []string{"site_logo"}) {
			t.Errorf("%q: DeleteOptions = %v, options must still be cleaned", raw, got.DeleteOptions)
		}
	}
}

// tmdReference is an independent restatement of the matching rules, written
// against WordPress core rather than against the implementation. It works on
// the generated structure (the oracle for what the bytes mean), not on the
// serialized text, and re-encodes only the kept entries, so equality with the
// scanner's output also proves every kept byte was preserved.
// tmdURLPathRE is the reference for the URL rule: an optional lower-case
// http(s) scheme and non-empty authority, then a path that starts with one
// "/" and has no query or fragment. Group 1 is the path.
var tmdURLPathRE = regexp.MustCompile(`^(?:https?://[^/?#]+)?(/(?:[^/?#][^?#]*)?)$`)

// tmdSameURL is the reference for "mod value refers to the attachment URL":
// both reduce to a path and the paths are byte-equal. Anything that does not
// reduce (not a URL, empty, query, fragment, protocol-relative) never matches.
func tmdSameURL(a, b string) bool {
	ma, mb := tmdURLPathRE.FindStringSubmatch(a), tmdURLPathRE.FindStringSubmatch(b)
	return ma != nil && mb != nil && ma[1] == mb[1]
}

func tmdReference(mods tmdList, opts map[string]string, id int64, url string) (string, bool, bool, []string) {
	num := func(v any) (int64, bool) {
		switch x := v.(type) {
		case int:
			return int64(x), true
		case string:
			n, err := strconv.ParseInt(x, 10, 64)
			return n, err == nil
		}
		return 0, false
	}
	optNum := func(raw string) (int64, bool) {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return n, true
		}
		if len(raw) > 3 && raw[:2] == "i:" && raw[len(raw)-1] == ';' {
			n, err := strconv.ParseInt(raw[2:len(raw)-1], 10, 64)
			return n, err == nil
		}
		return 0, false
	}
	// last value per key, as PHP resolves duplicates
	val := map[string]any{}
	for _, e := range mods {
		val[e.k] = e.v
	}
	var drop []string
	changed := false
	if mods != nil {
		if n, ok := num(val["custom_logo"]); ok && n == id {
			drop = append(drop, "custom_logo", "header_text")
		}
		if s, ok := val["header_image"].(string); ok && tmdSameURL(s, url) {
			drop = append(drop, "header_image", "header_image_data")
		}
		if s, ok := val["background_image"].(string); ok && tmdSameURL(s, url) {
			drop = append(drop, "background_image")
		}
	}
	out := tmdWithout(mods, drop...)
	changed = mods != nil && len(out) != len(mods)
	raw := tmdRaw(mods)
	delRow := changed && len(out) == 0
	switch {
	case delRow:
		raw = ""
	case changed:
		raw = tmdRaw(out)
	}
	var del []string
	for _, name := range []string{"site_icon", "site_logo"} {
		if r, present := opts[name]; present {
			if n, ok := optNum(r); ok && n == id {
				del = append(del, name)
			}
		}
	}
	return raw, changed, delRow, del
}

// TestPropertyThemeModCleanupMatchesReference checks Property 1.
//
// Feature: media-delete-and-purge-config, Property 1: Theme-mod and option cleanup matches exactly on a reference, and leaves no empty row
//
// The generated theme mods mix scalars with objects, floats, integer-keyed
// arrays and strings containing braces and semicolons, so the scanner is held
// to byte-exact preservation, not just to the matching rules.
//
// Validates: Requirements 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 1.9
func TestPropertyThemeModCleanupMatchesReference(t *testing.T) {
	urls := []string{
		tmdURL, "https://example.test/other.png", "",
		tmdRelURL, "http://example.test:8080" + tmdRelURL, "https://other.test" + tmdRelURL,
		tmdRelURL + "?v=1", "/wp-content/uploads/2024/05/x.png", "//example.test" + tmdRelURL, "logo.png",
	}
	prop := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		id := int64(1 + rng.Intn(5))
		url := urls[rng.Intn(len(urls))]

		pickVal := func() any {
			switch rng.Intn(12) {
			case 0:
				return int(1 + rng.Intn(5))
			case 1:
				return strconv.Itoa(1 + rng.Intn(5))
			case 2:
				return urls[rng.Intn(len(urls))]
			case 3:
				return rng.Intn(2) == 0
			case 4:
				return nil
			case 5:
				return tmdMods("attachment_id", 1+rng.Intn(5), "w", tmdFloat("1.5"))
			case 6:
				return tmdRawV(`O:8:"stdClass":2:{s:13:"attachment_id";i:3;s:3:"url";s:4:"};{;";}`)
			case 7:
				return tmdIntList{1, "a}b;", tmdFloat("-2.5E+3"), tmdIntList{nil, true}}
			case 8:
				return tmdFloat("0.1")
			case 9:
				return `br{ace};s:1:"x";`
			case 10:
				return tmdRawV(`a:0:{}`)
			default:
				return "abc"
			}
		}
		var mods tmdList
		if rng.Intn(6) != 0 { // sometimes absent
			mods = tmdList{}
			keys := []string{"custom_logo", "header_text", "header_image", "header_image_data", "background_image", "site_icon", "site_logo", "color", "nav_menu_locations", "zz{;"}
			rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
			for _, k := range keys {
				if rng.Intn(2) == 0 {
					mods = append(mods, tmdKV{k, pickVal()})
				}
			}
		}
		optRaw := []string{"1", "2", "3", "4", "5", "i:" + strconv.Itoa(1+rng.Intn(5)) + ";", "abc", "", "  "}
		opts := map[string]string{}
		for _, name := range []string{"site_logo", "site_icon"} {
			if rng.Intn(3) != 0 {
				opts[name] = optRaw[rng.Intn(len(optRaw))]
			}
		}

		wantRaw, wantChanged, wantDelRow, wantDel := tmdReference(mods, opts, id, url)
		got := decideThemeModCleanup(tmdRaw(mods), opts, id, url)

		sort.Strings(wantDel)
		gotDel := append([]string(nil), got.DeleteOptions...)
		sort.Strings(gotDel)
		if len(wantDel) == 0 {
			wantDel = nil
		}
		if len(gotDel) == 0 {
			gotDel = nil
		}
		return got.Mods == wantRaw &&
			got.ModsChanged == wantChanged &&
			got.DeleteModsRow == wantDelRow &&
			reflect.DeepEqual(gotDel, wantDel)
	}
	cfg := &quick.Config{MaxCount: 100, Rand: rand.New(rand.NewSource(23))}
	if err := quick.Check(prop, cfg); err != nil {
		t.Error(err)
	}
}

// TestThemeModWritePreservesNULBytes proves the edited value reaches SQLite
// with its NUL bytes (PHP's delimiter in protected and private property
// names) intact. Bun inlines string arguments and drops NULs, so the write
// must use a bound argument. SQLite and MySQL store NUL; PostgreSQL text
// cannot, so this is a SQLite-only white-box test.
func TestThemeModWritePreservesNULBytes(t *testing.T) {
	ctx := context.Background()
	const prefix = "wp_"
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: filepath.Join(t.TempDir(), "g.db"), TablePrefix: prefix}
	db, err := sqliteadapter.Open(cfg)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := migrate.Apply(ctx, db, sqlitemig.FS(), cfg.Vendor, prefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	repo := NewOptionRepo(sqliteadapter.NewBunDB(db), prefix)

	keep := tmdStr("widget") + `O:3:"Foo":1:{s:4:"` + "\x00*\x00" + `p";i:7;}`
	in := "a:3:{" + tmdStr("custom_logo") + "i:42;" + tmdStr("header_text") + "b:0;" + keep + "}"
	want := "a:1:{" + keep + "}"
	if _, err := db.ExecContext(ctx, "INSERT INTO wp_options (option_name, option_value, autoload) VALUES (?, ?, 'yes')", "theme_mods_t", in); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.CleanAttachmentReferences(ctx, 42, tmdURL, "t"); err != nil {
		t.Fatalf("CleanAttachmentReferences: %v", err)
	}
	var got string
	if err := db.QueryRowContext(ctx, "SELECT option_value FROM wp_options WHERE option_name = ?", "theme_mods_t").Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != want {
		t.Errorf("stored value = %q, want %q", got, want)
	}
}
