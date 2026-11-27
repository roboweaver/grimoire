package storagetest

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/php"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture values for the ThemeModCleaner contract. Every helper here carries
// the tmc prefix so it cannot collide with another contract in this package.
const (
	tmcAttachmentID  int64 = 4242
	tmcAttachmentURL       = "https://example.test/wp-content/uploads/2024/05/tmc-logo.png"
	tmcOtherURL            = "https://example.test/wp-content/uploads/2024/05/tmc-other.png"
	tmcSheet               = "tmc-theme-a" // the stylesheet passed to the call under test
	tmcSheetB              = "tmc-theme-b" // a second theme whose mods must never be touched
	tmcModsOption          = "theme_mods_" + tmcSheet
)

// tmcSerialize PHP-serializes v, failing the test on error.
func tmcSerialize(t *testing.T, v any) string {
	t.Helper()
	s, err := php.Serialize(v)
	if err != nil {
		t.Fatalf("php.Serialize(%v): %v", v, err)
	}
	return s
}

// tmcDecode decodes a stored theme_mods value into an associative map.
func tmcDecode(t *testing.T, raw string) map[string]any {
	t.Helper()
	v, err := php.Unserialize(raw)
	if err != nil {
		t.Fatalf("stored value %q is not PHP-serialized: %v", raw, err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("stored value %q decodes to %T, want an associative array", raw, v)
	}
	return m
}

// tmcMods builds a theme_mods map holding non-string values of every kind the
// php package round-trips (ints, bools, null, nested arrays) next to strings,
// so an in-place edit that coerces a type is caught. extra entries override.
func tmcMods(extra map[string]any) map[string]any {
	m := map[string]any{
		"color_scheme":       "dark",
		"sidebar_width":      300,
		"show_tagline":       true,
		"hide_footer":        false,
		"legacy":             nil,
		"nav_menu_locations": map[string]any{"primary": 12, "footer": 0},
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// tmcStr is a PHP-serialized string.
func tmcStr(s string) string { return "s:" + strconv.Itoa(len(s)) + `:"` + s + `";` }

// tmcArr assembles a serialized array from already-serialized entries, each
// one a key immediately followed by its value.
func tmcArr(entries ...string) string {
	out := "a:" + strconv.Itoa(len(entries)) + ":{"
	for _, p := range entries {
		out += p
	}
	return out + "}"
}

// tmcWithout returns a copy of m minus the named keys.
func tmcWithout(m map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

// RunThemeModCleanerContract pins ThemeModCleaner.CleanAttachmentReferences,
// the storage half of WordPress's _delete_attachment_theme_mod() and
// WP_Site_Icon::delete_attachment_data(). Scope is exactly core's five
// targets:
//
//   - theme mod custom_logo (id match) removes custom_logo and header_text;
//   - theme mod header_image (URL match) removes header_image and
//     header_image_data;
//   - theme mod background_image (URL match) removes background_image;
//   - option site_logo (id match) is deleted;
//   - option site_icon (id match) is deleted.
//
// Emptying theme_mods_{stylesheet} deletes the row instead of writing back an
// empty array. The stylesheet is an argument; the port does not read the
// "stylesheet" option. Everything else, including other themes' theme mods,
// stays byte-identical.
//
// The vendor and table prefix come from newRepos; the contract holds no
// literal prefix and no vendor-specific placeholder. Options are read and
// seeded through the bundle's *sql.DB. The base fixture's own "stylesheet"
// (twentytwentyfive) and theme_mods_twentytwentyfive rows are left in place:
// the stylesheet argument, not that option, selects the row.
//
// Transactionality limit: a vendor-neutral contract cannot inject a failure
// between the theme-mods write and the option deletes, so atomicity is pinned
// only for the failure it can cause portably (a cancelled context leaves every
// row untouched). A white-box rollback test belongs with the implementation.
func RunThemeModCleanerContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	type fixture struct {
		cleaner domain.ThemeModCleaner
		db      *sql.DB
		vendor  string
		prefix  string
	}

	setup := func(t *testing.T, options map[string]string) (*fixture, func()) {
		t.Helper()
		r, vendor, prefix, cl := newRepos(t)
		db := r.DB()
		for name, value := range options {
			if _, err := db.ExecContext(ctx, rebind.Rebind(vendor,
				`INSERT INTO `+prefix+`options (option_name, option_value, autoload) VALUES (?, ?, ?)`),
				name, value, "yes"); err != nil {
				cl()
				t.Fatalf("seed option %q: %v", name, err)
			}
		}
		if r.ThemeMods == nil {
			cl()
			t.Fatalf("Repositories.ThemeMods is nil")
		}
		return &fixture{cleaner: r.ThemeMods, db: db, vendor: vendor, prefix: prefix}, cl
	}

	// snapshot returns every option row, including the base fixture's.
	snapshot := func(t *testing.T, f *fixture) map[string]string {
		t.Helper()
		rows, err := f.db.QueryContext(ctx, `SELECT option_name, option_value FROM `+f.prefix+`options`)
		if err != nil {
			t.Fatalf("snapshot options: %v", err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var n, v string
			if err := rows.Scan(&n, &v); err != nil {
				t.Fatalf("scan option: %v", err)
			}
			out[n] = v
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate options: %v", err)
		}
		return out
	}

	clean := func(t *testing.T, f *fixture, id int64, url, sheet string) {
		t.Helper()
		if err := f.cleaner.CleanAttachmentReferences(ctx, id, url, sheet); err != nil {
			t.Fatalf("CleanAttachmentReferences(%d, %q, %q): %v", id, url, sheet, err)
		}
	}

	// expectState asserts the whole options table: every row in want (exact
	// bytes) is present and equal, every name in wantMods holds a theme-mods
	// array deep-equal to the given map (types included), and no other row
	// exists or is missing.
	expectState := func(t *testing.T, f *fixture, want map[string]string, wantMods map[string]map[string]any) {
		t.Helper()
		got := snapshot(t, f)
		for name, w := range want {
			g, ok := got[name]
			if !ok {
				t.Errorf("option %q was deleted, want it kept as %q", name, w)
				continue
			}
			if g != w {
				t.Errorf("option %q = %q, want byte-identical %q", name, g, w)
			}
		}
		for name, w := range wantMods {
			g, ok := got[name]
			if !ok {
				t.Errorf("option %q was deleted, want it kept with %v", name, w)
				continue
			}
			if gm := tmcDecode(t, g); !reflect.DeepEqual(gm, w) {
				t.Errorf("option %q decodes to %#v, want %#v", name, gm, w)
			}
		}
		for name := range got {
			if _, ok := want[name]; ok {
				continue
			}
			if _, ok := wantMods[name]; ok {
				continue
			}
			t.Errorf("unexpected option row %q = %q remains", name, got[name])
		}
	}

	// base returns the pre-call snapshot split the way expectState wants it:
	// every row exact, minus the names the caller then overrides.
	exactExcept := func(before map[string]string, names ...string) map[string]string {
		out := make(map[string]string, len(before))
		for k, v := range before {
			out[k] = v
		}
		for _, n := range names {
			delete(out, n)
		}
		return out
	}

	// withRow returns m with name set to value (m is not modified).
	withRow := func(m map[string]string, name, value string) map[string]string {
		out := make(map[string]string, len(m)+1)
		for k, v := range m {
			out[k] = v
		}
		out[name] = value
		return out
	}

	idStr := "4242"

	t.Run("custom_logo id match removes custom_logo and header_text and keeps every other key and type", func(t *testing.T) {
		mods := tmcMods(map[string]any{"custom_logo": int(tmcAttachmentID), "header_text": "blank", "header_image": tmcOtherURL, "header_image_data": map[string]any{"attachment_id": 9}})
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods)})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, exactExcept(before, tmcModsOption),
			map[string]map[string]any{tmcModsOption: tmcWithout(mods, "custom_logo", "header_text")})
	})

	t.Run("custom_logo stored as a numeric string matches by id", func(t *testing.T) {
		mods := tmcMods(map[string]any{"custom_logo": idStr, "header_text": "x"})
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods)})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, exactExcept(before, tmcModsOption),
			map[string]map[string]any{tmcModsOption: tmcWithout(mods, "custom_logo", "header_text")})
	})

	t.Run("header_image URL match removes header_image and header_image_data only", func(t *testing.T) {
		mods := tmcMods(map[string]any{"header_image": tmcAttachmentURL, "header_image_data": map[string]any{"attachment_id": 7, "width": 1200}, "header_text": "shown", "custom_logo": 1})
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods)})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, exactExcept(before, tmcModsOption),
			map[string]map[string]any{tmcModsOption: tmcWithout(mods, "header_image", "header_image_data")})
	})

	t.Run("background_image URL match removes only background_image", func(t *testing.T) {
		mods := tmcMods(map[string]any{"background_image": tmcAttachmentURL, "background_color": "ffffff", "header_image": tmcOtherURL})
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods)})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, exactExcept(before, tmcModsOption),
			map[string]map[string]any{tmcModsOption: tmcWithout(mods, "background_image")})
	})

	t.Run("an absolute URL value matches the relative attachment URL by path, a different path is untouched", func(t *testing.T) {
		const rel = "/wp-content/uploads/2024/05/tmc-logo.png"
		mods := tmcMods(map[string]any{
			"header_image": "https://shared.example.test:8443" + rel, "header_image_data": map[string]any{"attachment_id": 7},
			"background_image": "https://shared.example.test/wp-content/uploads/2024/05/tmc-other.png", "header_text": "shown",
		})
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods)})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, rel, tmcSheet)
		expectState(t, f, exactExcept(before, tmcModsOption),
			map[string]map[string]any{tmcModsOption: tmcWithout(mods, "header_image", "header_image_data")})
	})
	t.Run("one attachment referenced by every target clears all of them in one call", func(t *testing.T) {
		mods := tmcMods(map[string]any{
			"custom_logo": int(tmcAttachmentID), "header_text": "x",
			"header_image": tmcAttachmentURL, "header_image_data": "d",
			"background_image": tmcAttachmentURL,
		})
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods), "site_logo": idStr, "site_icon": idStr})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, exactExcept(before, tmcModsOption, "site_logo", "site_icon"),
			map[string]map[string]any{tmcModsOption: tmcWithout(mods, "custom_logo", "header_text", "header_image", "header_image_data", "background_image")})
	})

	t.Run("emptying theme_mods deletes the row instead of writing an empty array", func(t *testing.T) {
		mods := map[string]any{"custom_logo": int(tmcAttachmentID), "header_text": "x", "background_image": tmcAttachmentURL}
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods)})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, exactExcept(before, tmcModsOption), nil)
		var n int
		if err := f.db.QueryRowContext(ctx, rebind.Rebind(f.vendor,
			`SELECT COUNT(*) FROM `+f.prefix+`options WHERE option_name = ?`), tmcModsOption).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 0 {
			t.Errorf("%s rows = %d, want 0 (a:0:{} must not be written back)", tmcModsOption, n)
		}
	})

	t.Run("a mods array that is already empty and does not match is left as is", func(t *testing.T) {
		f, cl := setup(t, map[string]string{tmcModsOption: "a:0:{}"})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, before, nil)
	})

	t.Run("site_logo id match deletes the option, plain and PHP-serialized integers", func(t *testing.T) {
		for _, raw := range []string{idStr, "i:4242;"} {
			f, cl := setup(t, map[string]string{"site_logo": raw})
			before := snapshot(t, f)
			clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
			expectState(t, f, exactExcept(before, "site_logo"), nil)
			cl()
		}
	})

	t.Run("site_icon id match deletes the option, plain and PHP-serialized integers", func(t *testing.T) {
		for _, raw := range []string{idStr, "i:4242;"} {
			f, cl := setup(t, map[string]string{"site_icon": raw})
			before := snapshot(t, f)
			clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
			expectState(t, f, exactExcept(before, "site_icon"), nil)
			cl()
		}
	})

	t.Run("an unrelated attachment leaves every option byte-identical", func(t *testing.T) {
		mods := tmcMods(map[string]any{
			"custom_logo": 4243, "header_text": "x",
			"header_image": tmcOtherURL, "header_image_data": map[string]any{"attachment_id": int(tmcAttachmentID)},
			"background_image": tmcOtherURL + "?v=1",
		})
		// Ids that merely contain, or differ by one from, the attachment id.
		f, cl := setup(t, map[string]string{
			tmcModsOption: tmcSerialize(t, mods),
			"site_logo":   "42420",
			"site_icon":   "424",
			"custom_logo": idStr, // an option, not a theme mod: core never reads it
		})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, before, nil)
	})

	t.Run("URL matching is exact and an empty URL matches nothing", func(t *testing.T) {
		mods := map[string]any{"header_image": "", "background_image": "", "header_image_data": "d", "color": "red"}
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods)})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, "", tmcSheet)
		expectState(t, f, before, nil)
		clean(t, f, tmcAttachmentID, "HTTPS://EXAMPLE.TEST/wp-content/uploads/2024/05/TMC-LOGO.PNG", tmcSheet)
		expectState(t, f, before, nil)
	})

	t.Run("core does not clear site_icon or site_logo keys, nor custom_logo and header_image options", func(t *testing.T) {
		mods := map[string]any{"site_icon": int(tmcAttachmentID), "site_logo": int(tmcAttachmentID), "keep": 1}
		f, cl := setup(t, map[string]string{
			tmcModsOption: tmcSerialize(t, mods),
			"custom_logo": idStr, "header_image": tmcAttachmentURL, "background_image": tmcAttachmentURL,
		})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, before, nil)
	})

	t.Run("only the stylesheet passed in is edited, other themes and look-alike names are untouched", func(t *testing.T) {
		mods := tmcMods(map[string]any{"custom_logo": int(tmcAttachmentID), "header_text": "x"})
		ser := tmcSerialize(t, mods)
		f, cl := setup(t, map[string]string{
			tmcModsOption:                         ser,
			"theme_mods_" + tmcSheetB:             ser, // another theme, same logo
			"theme_mods_" + tmcSheet + "-child":   ser, // name extending the stylesheet
			"theme_mods_x" + tmcSheet:             ser, // name ending with the stylesheet
			"theme_mods_" + "TMC-THEME-A-upper":   ser,
			"theme_mods_twentytwentyfive-sibling": ser,
		})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, exactExcept(before, tmcModsOption),
			map[string]map[string]any{tmcModsOption: tmcWithout(mods, "custom_logo", "header_text")})
		if before["theme_mods_twentytwentyfive"] == "" {
			t.Errorf("base fixture lost its theme_mods_twentytwentyfive row before the call")
		}
	})

	t.Run("a differently cased stylesheet never edits another theme's row, even where the collation ignores case", func(t *testing.T) {
		mods := tmcMods(map[string]any{"custom_logo": int(tmcAttachmentID), "header_text": "x"})
		ser := tmcSerialize(t, mods)
		upper := "theme_mods_" + strings.ToUpper(tmcSheet)
		// MySQL's default collation makes option_name unique case-insensitively,
		// so only the upper-cased row is seeded: the call for the lower-cased
		// stylesheet must leave it byte-identical on every vendor.
		f, cl := setup(t, map[string]string{upper: ser})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, before, nil)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, strings.ToUpper(tmcSheet))
		expectState(t, f, exactExcept(before, upper),
			map[string]map[string]any{upper: tmcWithout(mods, "custom_logo", "header_text")})
	})
	t.Run("an empty stylesheet and LIKE wildcards in the stylesheet never reach another theme's row", func(t *testing.T) {
		mods := tmcMods(map[string]any{"custom_logo": int(tmcAttachmentID), "header_text": "x"})
		ser := tmcSerialize(t, mods)
		f, cl := setup(t, map[string]string{tmcModsOption: ser})
		defer cl()
		before := snapshot(t, f)
		for _, sheet := range []string{"", "%", "tmc-theme-%", "tmc_theme_a", "tmc-theme-_", "*"} {
			clean(t, f, tmcAttachmentID, tmcAttachmentURL, sheet)
			expectState(t, f, before, nil)
		}
	})

	t.Run("an absent theme_mods option is not an error and is not created", func(t *testing.T) {
		f, cl := setup(t, map[string]string{"site_logo": idStr})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, exactExcept(before, "site_logo"), nil) // the option half still ran
	})

	t.Run("a malformed theme_mods value is not an error and stays byte-identical, options still clean", func(t *testing.T) {
		for _, raw := range []string{"", "not serialized", `s:3:"abc";`, "i:4242;", "N;", "a:2:{s:11:\"custom_logo\";i:4242;", `a:1:{s:11:"custom_logo";O:8:"stdClass":0:{}}`} {
			f, cl := setup(t, map[string]string{tmcModsOption: raw, "site_icon": idStr})
			before := snapshot(t, f)
			clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
			expectState(t, f, exactExcept(before, "site_icon"), nil)
			cl()
		}
	})

	t.Run("an absent or non-integer site_logo and site_icon is not an error and is left alone", func(t *testing.T) {
		for _, raw := range []string{"", "abc", " ", "42.5", "4242abc", "a:0:{}", "N;"} {
			f, cl := setup(t, map[string]string{"site_logo": raw, "site_icon": raw})
			before := snapshot(t, f)
			clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
			expectState(t, f, before, nil)
			cl()
		}
		// Neither option exists: nothing is created.
		f, cl := setup(t, nil)
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, before, nil)
	})

	t.Run("a second call after a match is a no-op and not an error", func(t *testing.T) {
		mods := tmcMods(map[string]any{"custom_logo": int(tmcAttachmentID), "header_text": "x", "background_image": tmcAttachmentURL})
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods), "site_logo": idStr, "site_icon": idStr})
		defer cl()
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		after1 := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, after1, nil)
	})

	t.Run("a second call after the row was emptied and deleted is a no-op", func(t *testing.T) {
		mods := map[string]any{"custom_logo": int(tmcAttachmentID)}
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods)})
		defer cl()
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		after1 := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, after1, nil)
	})

	t.Run("an edited row keeps objects, floats, integer-keyed arrays and odd strings byte-identical", func(t *testing.T) {
		// Shapes real WordPress stores and a decode/re-encode would lose or
		// rewrite: header_image_data as stdClass, a float, a list with
		// integer keys, and strings holding braces and semicolons.
		obj := `O:8:"stdClass":3:{s:13:"attachment_id";i:7;s:3:"url";` + tmcStr(tmcAttachmentURL) + `s:5:"width";d:1200.5;}`
		list := tmcArr("i:0;"+tmcStr("a}b;c"), "i:5;"+`a:2:{i:0;N;i:3;d:-1.0E+25;}`)
		pre1 := tmcStr("zoom") + "d:1.5;"
		pre2 := tmcStr("nav_menu_locations") + list
		post1 := tmcStr("note") + tmcStr(`};{"s:1:"x";`)
		post2 := tmcStr("flag") + "b:1;"
		in := tmcArr(
			pre1, pre2,
			tmcStr("header_image")+tmcStr(tmcAttachmentURL),
			tmcStr("header_image_data")+obj,
			post1, post2,
		)
		want := tmcArr(pre1, pre2, post1, post2)
		f, cl := setup(t, map[string]string{tmcModsOption: in})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, withRow(exactExcept(before, tmcModsOption), tmcModsOption, want), nil)
	})

	t.Run("an edited row keeps multibyte text and, where the column can hold them, NUL bytes byte-identical", func(t *testing.T) {
		// PHP delimits protected and private object property names with NUL
		// bytes. PostgreSQL text columns cannot store NUL, so that part of the
		// fixture is not required there; every other vendor must keep it.
		keepText := tmcStr("title") + tmcStr("Zoë – 日本語 😀")
		keep := []string{keepText}
		f, cl := setup(t, nil)
		defer cl()
		if f.vendor != "postgres" {
			keep = append(keep, tmcStr("obj")+"O:3:\"Foo\":1:{s:7:\"\x00*\x00prop\";i:7;}")
		}
		in := tmcArr(append(append([]string{}, keep...), tmcStr("custom_logo")+"i:"+strconv.FormatInt(tmcAttachmentID, 10)+";")...)
		want := tmcArr(keep...)
		if _, err := f.db.ExecContext(ctx, rebind.Rebind(f.vendor,
			`INSERT INTO `+f.prefix+`options (option_name, option_value, autoload) VALUES (?, ?, ?)`),
			tmcModsOption, in, "yes"); err != nil {
			t.Fatalf("seed option: %v", err)
		}
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, withRow(exactExcept(before, tmcModsOption), tmcModsOption, want), nil)
	})
	t.Run("a custom_logo match with an object neighbour removes only its two spans", func(t *testing.T) {
		keep1 := tmcStr("widget") + `O:3:"Foo":1:{s:4:"prop";i:7;}`
		keep2 := tmcStr("ints") + tmcArr("i:0;i:1;", "i:7;d:2.5;")
		in := tmcArr(tmcStr("custom_logo")+"i:"+strconv.FormatInt(tmcAttachmentID, 10)+";", keep1, tmcStr("header_text")+"b:0;", keep2)
		f, cl := setup(t, map[string]string{tmcModsOption: in})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		want := tmcArr(keep1, keep2)
		expectState(t, f, withRow(exactExcept(before, tmcModsOption), tmcModsOption, want), nil)
	})

	t.Run("an object-valued custom_logo or header_image never matches and the row is untouched", func(t *testing.T) {
		in := tmcArr(tmcStr("custom_logo")+`O:8:"stdClass":0:{}`, tmcStr("header_image")+"d:1.5;", tmcStr("background_image")+"i:4242;")
		f, cl := setup(t, map[string]string{tmcModsOption: in})
		defer cl()
		before := snapshot(t, f)
		clean(t, f, tmcAttachmentID, tmcAttachmentURL, tmcSheet)
		expectState(t, f, before, nil)
	})

	t.Run("a cancelled context fails and leaves every option untouched", func(t *testing.T) {
		mods := tmcMods(map[string]any{"custom_logo": int(tmcAttachmentID), "header_text": "x"})
		f, cl := setup(t, map[string]string{tmcModsOption: tmcSerialize(t, mods), "site_logo": idStr, "site_icon": idStr})
		defer cl()
		before := snapshot(t, f)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if err := f.cleaner.CleanAttachmentReferences(cctx, tmcAttachmentID, tmcAttachmentURL, tmcSheet); err == nil {
			t.Error("CleanAttachmentReferences with a cancelled context returned nil, want an error")
		}
		expectState(t, f, before, nil)
	})
}
