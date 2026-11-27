package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/uptrace/bun"
)

// Option and theme-mod names that WordPress core's
// _delete_attachment_theme_mod() and WP_Site_Icon::delete_attachment_data()
// act on. Nothing else is touched.
const (
	themeModsPrefix       = "theme_mods_"
	modCustomLogo         = "custom_logo"
	modHeaderText         = "header_text"
	modHeaderImage        = "header_image"
	modHeaderImageData    = "header_image_data"
	modBackgroundImage    = "background_image"
	optionSiteLogo        = "site_logo"
	optionSiteIcon        = "site_icon"
	themeModOptionLookups = 2
)

// themeModDecision is the outcome of the pure decision logic.
type themeModDecision struct {
	// Mods is the theme_mods value to store. It equals the input byte for
	// byte unless ModsChanged, and is "" when DeleteModsRow.
	Mods string
	// ModsChanged is true iff at least one top-level key was removed.
	ModsChanged bool
	// DeleteModsRow is true iff ModsChanged and no keys remain.
	DeleteModsRow bool
	// DeleteOptions lists the options to delete, ascending: a subset of
	// "site_icon", "site_logo".
	DeleteOptions []string
}

// decideThemeModCleanup applies core's matching rules.
//
// rawMods is the stored theme_mods_{stylesheet} value; "" means the option is
// absent, and a value that is not exactly one well-formed serialized array is
// skipped without error (Requirement 1.8). The value is never decoded and
// re-encoded: the top-level entries are located by byte span, the scalar
// values the rules need are read, and whole key/value spans are cut out, with
// only the array count rewritten. Every other byte is preserved, including
// objects, floats and integer-keyed arrays that a decode would lose.
//
// optionValues maps "site_logo" / "site_icon" to the stored option_value; a
// missing key means the row is absent.
func decideThemeModCleanup(rawMods string, optionValues map[string]string, attachmentID int64, attachmentURL string) themeModDecision {
	d := themeModDecision{Mods: rawMods}

	if entries, ok := scanPHPArray(rawMods); ok {
		// For a duplicated key the last entry wins, as PHP's unserialize does.
		last := map[string]phpEntry{}
		for _, e := range entries {
			if e.strKey {
				last[e.key] = e
			}
		}
		remove := map[string]bool{}
		if e, ok := last[modCustomLogo]; ok {
			if n, ok := phpEntryInt(rawMods, e); ok && n == attachmentID {
				remove[modCustomLogo], remove[modHeaderText] = true, true
			}
		}
		if e, ok := last[modHeaderImage]; ok {
			if v, ok := phpEntryString(rawMods, e); ok && sameUploadsURL(v, attachmentURL) {
				remove[modHeaderImage], remove[modHeaderImageData] = true, true
			}
		}
		if e, ok := last[modBackgroundImage]; ok {
			if v, ok := phpEntryString(rawMods, e); ok && sameUploadsURL(v, attachmentURL) {
				remove[modBackgroundImage] = true
			}
		}

		removed := 0
		var kept strings.Builder
		for _, e := range entries {
			if e.strKey && remove[e.key] {
				removed++
				continue
			}
			kept.WriteString(rawMods[e.start:e.end])
		}
		if removed > 0 {
			d.ModsChanged = true
			if left := len(entries) - removed; left == 0 {
				d.DeleteModsRow = true
				d.Mods = ""
			} else {
				d.Mods = "a:" + strconv.Itoa(left) + ":{" + kept.String() + "}"
			}
		}
	}

	for _, name := range []string{optionSiteIcon, optionSiteLogo} {
		raw, present := optionValues[name]
		if !present {
			continue
		}
		if n, ok := parseOptionInt(raw); ok && n == attachmentID {
			d.DeleteOptions = append(d.DeleteOptions, name)
		}
	}
	return d
}

// urlPath reduces a URL to its path component for theme-mod matching, or
// reports false when the value cannot be compared.
//
// Accepted forms: a path starting with a single "/" (grimoire builds attachment
// URLs this way), or that path preceded by a lower-case "http://" or
// "https://" scheme and a non-empty authority (host, optional port), which is
// what WordPress's wp_get_attachment_url stores. The scheme and authority are
// stripped and the path is kept byte for byte: no case folding, no
// percent-decoding. Everything else is rejected: empty and non-URL values,
// other schemes, protocol-relative "//host/..." values, upper-case schemes, and
// any value with a query or fragment.
func urlPath(u string) (string, bool) {
	rest := u
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(u, scheme) {
			rest = u[len(scheme):]
			i := strings.IndexAny(rest, "/?#")
			if i <= 0 {
				return "", false // no authority, or no path
			}
			rest = rest[i:]
			break
		}
	}
	if !strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "//") || strings.ContainsAny(rest, "?#") {
		return "", false
	}
	return rest, true
}

// sameUploadsURL reports whether a stored header_image / background_image value
// refers to the attachment URL.
//
// WordPress stores the absolute URL from wp_get_attachment_url
// ("https://example.com/wp-content/uploads/2024/05/photo.jpg") while grimoire
// builds a relative one (BaseURL + file), so exact string equality would never
// match on a shared database. Both sides are reduced to their path (see
// urlPath) and the paths compared exactly.
//
// Trade-off: the host is ignored, so on a database shared by several sites
// whose uploads use the same path, deleting an attachment can clear a mod
// that points at another site's identical path. That is accepted: the
// stored value then names a file the deleted attachment's path would serve,
// and the mod is only a cached reference. Percent-encoding differences
// ("my%20logo.png" vs "my logo.png") are not normalised and do not match.
func sameUploadsURL(value, attachmentURL string) bool {
	a, ok := urlPath(value)
	if !ok {
		return false
	}
	b, ok := urlPath(attachmentURL)
	return ok && a == b
}

// phpEntryString returns the entry's value when it is a serialized string.
func phpEntryString(s string, e phpEntry) (string, bool) {
	if e.valStart >= len(s) || s[e.valStart] != 's' {
		return "", false
	}
	v, _, ok := phpScanString(s, e.valStart)
	return v, ok
}

// phpEntryInt returns the entry's value when it is a serialized integer or a
// string holding a decimal integer.
func phpEntryInt(s string, e phpEntry) (int64, bool) {
	if e.valStart >= len(s) {
		return 0, false
	}
	switch s[e.valStart] {
	case 'i':
		n, err := strconv.ParseInt(s[e.valStart+2:e.end-1], 10, 64)
		return n, err == nil
	case 's':
		v, _, ok := phpScanString(s, e.valStart)
		if !ok {
			return 0, false
		}
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	return 0, false
}

// parseOptionInt parses a plain decimal option value or a serialized integer.
func parseOptionInt(raw string) (int64, bool) {
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n, true
	}
	if strings.HasPrefix(raw, "i:") && strings.HasSuffix(raw, ";") {
		n, err := strconv.ParseInt(raw[2:len(raw)-1], 10, 64)
		return n, err == nil
	}
	return 0, false
}

// CleanAttachmentReferences implements domain.ThemeModCleaner. See the port
// for the contract. The stylesheet selects the theme_mods row by exact name
// equality through a bound parameter, so no LIKE pattern is involved and
// "%" or "_" in it are literal; an empty stylesheet skips theme mods.
func (r *OptionRepo) CleanAttachmentReferences(ctx context.Context, attachmentID int64, attachmentURL, stylesheet string) error {
	table := bun.Ident(r.prefix + "options")
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// readOption returns the value and option_id of the row whose name
		// equals name byte for byte. MySQL's default collation compares
		// option_name case-insensitively, so the query may return a row with
		// a differently cased name; those are filtered out here, and later
		// writes target option_id, never the name, so they cannot reach them.
		readOption := func(name string) (string, int64, bool, error) {
			var rows []struct {
				ID    int64  `bun:"option_id"`
				Name  string `bun:"option_name"`
				Value string `bun:"option_value"`
			}
			err := tx.NewSelect().
				TableExpr("?", table).
				Column("option_id", "option_name", "option_value").
				Where("option_name = ?", name).
				Scan(ctx, &rows)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", 0, false, err
			}
			for _, row := range rows {
				if row.Name == name {
					return row.Value, row.ID, true, nil
				}
			}
			return "", 0, false, nil
		}
		modsName := themeModsPrefix + stylesheet
		var rawMods string
		var modsID int64
		if stylesheet != "" {
			v, id, _, err := readOption(modsName)
			if err != nil {
				return err
			}
			rawMods, modsID = v, id
		}
		optionValues := make(map[string]string, themeModOptionLookups)
		optionIDs := make(map[string]int64, themeModOptionLookups)
		for _, name := range []string{optionSiteIcon, optionSiteLogo} {
			v, id, present, err := readOption(name)
			if err != nil {
				return err
			}
			if present {
				optionValues[name], optionIDs[name] = v, id
			}
		}

		d := decideThemeModCleanup(rawMods, optionValues, attachmentID, attachmentURL)

		if d.DeleteModsRow {
			if _, err := tx.NewDelete().TableExpr("?", table).Where("option_id = ?", modsID).Exec(ctx); err != nil {
				return err
			}
		} else if d.ModsChanged {
			// The edited value goes through database/sql with a bound
			// argument, not Bun's builder: Bun inlines string arguments into
			// the SQL text and drops NUL bytes, which PHP uses to delimit
			// private and protected object property names and which must
			// survive untouched.
			ident := r.db.QueryGen().FormatQuery("?", table)
			// database/sql placeholders are the driver's own: ? for MySQL and
			// SQLite, $N for PostgreSQL. (Not rebind.Rebind: that is barred in
			// this package, whose Bun paths must keep ?.)
			p1, p2 := "?", "?"
			if vendorOf(r.db) == "postgres" {
				p1, p2 = "$1", "$2"
			}
			q := "UPDATE " + ident + " SET option_value = " + p1 + " WHERE option_id = " + p2
			if _, err := tx.Tx.ExecContext(ctx, q, d.Mods, modsID); err != nil {
				return err
			}
		}
		for _, name := range d.DeleteOptions {
			if _, err := tx.NewDelete().TableExpr("?", table).Where("option_id = ?", optionIDs[name]).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}
