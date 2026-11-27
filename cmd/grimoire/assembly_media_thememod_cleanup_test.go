package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

// M23 task 10.3: RED assembly wiring test for theme-mod and option cleanup. The
// REST media DELETE ?force=true reaches content.MediaWriteService.Delete, whose
// domain.ThemeModCleaner and MediaConfig.Theme are optional. Until task 10.4
// passes content.WithMediaThemeModCleaner and Theme: cfg.Theme in assembly.go,
// nothing is cleaned. mfcHandler sets cfg.Theme = "default", so the row is
// theme_mods_default.

func tmcSetOption(t *testing.T, env customWriteEnv, name, value string) {
	t.Helper()
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"INSERT INTO "+env.Prefix+"options (option_name, option_value, autoload) VALUES (?, ?, ?)",
		name, value, "yes"); err != nil {
		t.Fatalf("seed option %s: %v", name, err)
	}
}

// tmcGetOption returns the option's value and whether the row exists.
func tmcGetOption(t *testing.T, env customWriteEnv, name string) (string, bool) {
	t.Helper()
	var v string
	err := env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT option_value FROM "+env.Prefix+"options WHERE option_name = ?", name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read option %s: %v", name, err)
	}
	return v, true
}

func tmcStr(s string) string { return fmt.Sprintf("s:%d:%q;", len(s), s) }

func TestBuildHandlerMediaThemeModCleanup(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	uploads := filepath.Join(t.TempDir(), "uploads")
	h := mfcHandler(t, env, uploads)

	att := seedRawPost(t, env, "attachment", "inherit", 0)
	other := seedRawPost(t, env, "attachment", "inherit", 0)
	unrelated := seedRawPost(t, env, "attachment", "inherit", 0)
	seedRawMeta(t, env, att, "_wp_attached_file", "2024/05/photo.jpg")
	seedRawMeta(t, env, other, "_wp_attached_file", "2024/05/other.jpg")
	seedRawMeta(t, env, unrelated, "_wp_attached_file", "2024/05/unrelated.jpg")

	attURL := "/wp-content/uploads/2024/05/photo.jpg"
	otherURL := "/wp-content/uploads/2024/05/other.jpg"
	headerData := fmt.Sprintf(`O:8:"stdClass":2:{%s%s%si:%d;}`,
		tmcStr("url"), tmcStr(attURL), tmcStr("attachment_id"), att)

	entry := func(k, v string) string { return tmcStr(k) + v }
	mods := fmt.Sprintf("a:7:{%s%s%s%s%s%s%s}",
		entry("color_scheme", tmcStr("dark")),
		entry("custom_logo", fmt.Sprintf("i:%d;", att)),
		entry("header_text", "b:0;"),
		entry("header_image", tmcStr(attURL)),
		entry("header_image_data", headerData),
		entry("background_image", tmcStr(otherURL)),
		entry("ratio", "d:0.5;"))
	wantMods := fmt.Sprintf("a:3:{%s%s%s}",
		entry("color_scheme", tmcStr("dark")),
		entry("background_image", tmcStr(otherURL)),
		entry("ratio", "d:0.5;"))

	const modsName = "theme_mods_default"
	tmcSetOption(t, env, modsName, mods)
	tmcSetOption(t, env, "site_logo", fmt.Sprint(att))
	tmcSetOption(t, env, "site_icon", fmt.Sprint(att))

	del := func(id int64) {
		t.Helper()
		rec := doJSON(t, h, &env.Admin, http.MethodDelete, fmt.Sprintf("/wp-json/wp/v2/media/%d?force=true", id), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE media %d: status = %d, want 200; body %s", id, rec.Code, rec.Body.String())
		}
	}

	// An unrelated attachment's delete leaves every option byte-identical.
	del(unrelated)
	for name, want := range map[string]string{modsName: mods, "site_logo": fmt.Sprint(att), "site_icon": fmt.Sprint(att)} {
		if got, ok := tmcGetOption(t, env, name); !ok || got != want {
			t.Fatalf("unrelated delete changed %s: got %q (exists=%v), want %q", name, got, ok, want)
		}
	}

	// The referenced attachment's delete removes exactly the matching entries.
	del(att)
	if rowExists(t, env, att) {
		t.Fatalf("attachment %d still exists", att)
	}
	if got, ok := tmcGetOption(t, env, modsName); !ok || got != wantMods {
		t.Errorf("%s = %q (exists=%v), want %q", modsName, got, ok, wantMods)
	}
	for _, name := range []string{"site_logo", "site_icon"} {
		if got, ok := tmcGetOption(t, env, name); ok {
			t.Errorf("option %s survived the delete with value %q", name, got)
		}
	}
}
