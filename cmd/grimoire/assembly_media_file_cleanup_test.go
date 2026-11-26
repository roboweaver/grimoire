package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/php"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// M23 task 6.3: RED assembly wiring test for media file cleanup. The REST media
// DELETE ?force=true is the only media delete entry point (M20: no admin delete
// route); it reaches content.MediaWriteService.Delete, whose metadata reader and
// logger are optional options. Until task 6.4 passes content.WithMediaMetaReader
// (and the logger) in assembly.go, only the main file is removed.

// mfcHandler builds the production handler over env's database with uploadsDir
// as media.uploads_dir. env's session tokens stay valid: sessions live in the
// shared database.
func mfcHandler(t *testing.T, env customWriteEnv, uploadsDir string) http.Handler {
	t.Helper()
	cfg := config.Config{
		Theme:    "default",
		Database: config.DatabaseConfig{Vendor: "sqlite", TablePrefix: env.Prefix},
		Session:  config.SessionConfig{CookieName: "grimoire_session", TTLHours: 24},
		Media:    config.MediaConfig{UploadsDir: uploadsDir},
		REST:     config.RESTConfig{PerPageMax: 100},
	}
	themesDir := filepath.Join("..", "..", "themes")
	eng, err := render.Load(themesDir, cfg.Theme)
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	sm := &auth.SessionManager{
		Users: env.Repos.Users, Meta: env.Repos.UserMeta, Sessions: env.Repos.Sessions,
		TTL: cfg.Session.TTL(), Prefix: cfg.Database.TablePrefix,
	}
	h, _, err := buildHandler(serverDeps{
		Repos:         env.Repos,
		Config:        cfg,
		Log:           slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		Permalinks:    st,
		Registry:      customWriteRegistry(t),
		Sessions:      sm,
		ContentPolicy: sanitize.New(),
		Engine:        eng,
		ThemesDir:     themesDir,
	})
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	return h
}

func mfcWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mfcExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func mfcSerialize(t *testing.T, v any) string {
	t.Helper()
	s, err := php.Serialize(v)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return s
}

// TestBuildHandlerMediaFileCleanup (Req 2, 3, 4, 5.1): a forced REST media delete
// through buildHandler removes the main file, every size variant, original_image
// and the backup sizes from the configured uploads dir, keeps an unrelated file,
// a thumb shared with another attachment and everything outside the uploads dir,
// and does not follow metadata entries that escape the uploads dir.
func TestBuildHandlerMediaFileCleanup(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	base := t.TempDir()
	uploads := filepath.Join(base, "uploads")
	dir := filepath.Join(uploads, "2024", "05")
	outsideRel := filepath.Join(base, "outside-rel.txt")
	outsideAbs := filepath.Join(base, "outside-abs.txt")
	h := mfcHandler(t, env, uploads)

	att := seedRawPost(t, env, "attachment", "inherit", 0)
	other := seedRawPost(t, env, "attachment", "inherit", 0)
	seedRawMeta(t, env, att, "_wp_attached_file", "2024/05/photo.jpg")
	seedRawMeta(t, env, other, "_wp_attached_file", "2024/05/other.jpg")

	files := map[string]string{
		"main":      filepath.Join(dir, "photo.jpg"),
		"thumbnail": filepath.Join(dir, "photo-150x150.jpg"),
		"medium":    filepath.Join(dir, "photo-300x200.jpg"),
		"original":  filepath.Join(dir, "photo-original.jpg"),
		"backup":    filepath.Join(dir, "photo-backup-300x200.jpg"),
	}
	unrelated := filepath.Join(dir, "unrelated.jpg")
	sharedThumb := filepath.Join(dir, "shared-thumb.jpg")
	otherMain := filepath.Join(dir, "other.jpg")
	for _, p := range files {
		mfcWrite(t, p)
	}
	for _, p := range []string{unrelated, sharedThumb, otherMain, outsideRel, outsideAbs} {
		mfcWrite(t, p)
	}

	meta := map[string]any{
		"file":           "2024/05/photo.jpg",
		"original_image": "photo-original.jpg",
		"thumb":          "shared-thumb.jpg",
		"sizes": map[string]any{
			"thumbnail": map[string]any{"file": "photo-150x150.jpg"},
			"medium":    map[string]any{"file": "photo-300x200.jpg"},
			"escape":    map[string]any{"file": "../../../outside-rel.txt"},
			"absolute":  map[string]any{"file": outsideAbs},
		},
	}
	backup := map[string]any{"medium-orig": map[string]any{"file": "photo-backup-300x200.jpg"}}
	seedRawMeta(t, env, att, "_wp_attachment_metadata", mfcSerialize(t, meta))
	seedRawMeta(t, env, att, "_wp_attachment_backup_sizes", mfcSerialize(t, backup))
	seedRawMeta(t, env, other, "_wp_attachment_metadata", mfcSerialize(t, map[string]any{
		"file": "2024/05/other.jpg", "thumb": "shared-thumb.jpg",
	}))

	rec := doJSON(t, h, &env.Admin, http.MethodDelete, fmt.Sprintf("/wp-json/wp/v2/media/%d?force=true", att), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE media: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if rowExists(t, env, att) {
		t.Fatalf("attachment %d still exists", att)
	}

	for name, p := range files {
		if mfcExists(p) {
			t.Errorf("%s file %s survived the delete", name, p)
		}
	}
	for name, p := range map[string]string{
		"unrelated file":               unrelated,
		"thumb shared with other":      sharedThumb,
		"other attachment's main file": otherMain,
		"relative path-escape target":  outsideRel,
		"absolute path-escape target":  outsideAbs,
	} {
		if !mfcExists(p) {
			t.Errorf("%s %s was removed", name, p)
		}
	}
	if !rowExists(t, env, other) {
		t.Errorf("other attachment %d was removed", other)
	}
}
