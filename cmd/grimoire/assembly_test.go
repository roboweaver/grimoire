package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// newAssemblySQLite builds a migrated SQLite-backed storage set in a temp dir,
// the same way storagetest's newSQLiteRepos and the CLI's newBackfillSQLite do.
func newAssemblySQLite(t *testing.T) (*storage.Repositories, config.DatabaseConfig) {
	t.Helper()
	ctx := context.Background()
	dbCfg := config.DatabaseConfig{
		Vendor:      "sqlite",
		DSN:         filepath.Join(t.TempDir(), "grimoire.db"),
		TablePrefix: "wp_",
	}
	repos, err := storage.New(dbCfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })
	migFS, err := storage.MigrationsFS(dbCfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, dbCfg.Vendor, dbCfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	return repos, dbCfg
}

// seedAssemblyPages inserts a user, a top-level page "about" and a nested page
// "team" whose post_parent is "about", with plain SQL (SQLite quoting only).
func seedAssemblyPages(t *testing.T, repos *storage.Repositories, prefix string) {
	t.Helper()
	ctx := context.Background()
	if _, err := repos.DB().ExecContext(ctx,
		`INSERT INTO `+prefix+`users (ID, user_login, user_nicename, display_name) VALUES (?, ?, ?, ?)`,
		1, "admin", "admin", "Admin"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	const ins = `INSERT INTO ` + "wp_" + `posts ` +
		`(ID, post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_type, comment_status, post_parent, post_mime_type, menu_order) ` +
		`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	pages := [][]any{
		{10, 1, "2024-01-05 00:00:00", "<p>about body</p>", "About", "", "publish", "about", "page", "closed", 0, "", 0},
		{11, 1, "2024-01-06 00:00:00", "<p>team body</p>", "Team", "", "publish", "team", "page", "closed", 10, "", 0},
	}
	for _, p := range pages {
		if _, err := repos.DB().ExecContext(ctx, ins, p...); err != nil {
			t.Fatalf("seed page %v: %v", p[7], err)
		}
	}
}

// TestBuildHandlerServesNestedPages drives the production server assembly
// function over a migrated, seeded SQLite database and asserts through HTTP
// only. Each assertion guards one wiring call inside buildHandler, so removing
// that call fails this test (the PR #74 regression: WithPages was built but
// never wired, and every per-package test passed because each builds its own
// Server).
//
//	assertion                                  guards
//	-----------------------------------------  ------------------------------
//	GET /about/team/  -> 200                   Server.WithPages (nested route)
//	GET /team/        -> 301 /about/team/      Server.WithPages (flat-leaf 301)
//	GET /about/       -> 200                   Server.WithPages (page service)
//	GET /wp-json/wp/v2/pages -> 200, link ends restMapper.WithPages (REST link)
//	  in /about/team/
func TestBuildHandlerServesNestedPages(t *testing.T) {
	repos, dbCfg := newAssemblySQLite(t)
	seedAssemblyPages(t, repos, dbCfg.TablePrefix)

	cfg := config.Config{
		Theme:    "default",
		Database: dbCfg,
		Session:  config.SessionConfig{CookieName: "grimoire_session", TTLHours: 24},
		Media:    config.MediaConfig{UploadsDir: t.TempDir()},
		REST:     config.RESTConfig{PerPageMax: 100},
	}
	themesDir := filepath.Join("..", "..", "themes")
	eng, err := render.Load(themesDir, cfg.Theme)
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	permalinks, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}

	handler, _, err := buildHandler(serverDeps{
		Repos:      repos,
		Config:     cfg,
		Log:        slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		Permalinks: permalinks,
		Sessions: &auth.SessionManager{
			Users:    repos.Users,
			Meta:     repos.UserMeta,
			Sessions: repos.Sessions,
			TTL:      cfg.Session.TTL(),
			Prefix:   cfg.Database.TablePrefix,
		},
		ContentPolicy: sanitize.New(),
		Engine:        eng,
		ThemesDir:     themesDir,
	})
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	t.Run("nested page serves 200", func(t *testing.T) {
		if rec := get("/about/team/"); rec.Code != http.StatusOK {
			t.Fatalf("GET /about/team/ = %d, want 200", rec.Code)
		}
	})
	t.Run("flat leaf of nested page 301s to nested canonical", func(t *testing.T) {
		rec := get("/team/")
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("GET /team/ = %d, want 301", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/about/team/" {
			t.Fatalf("Location = %q, want /about/team/", loc)
		}
	})
	t.Run("top-level page serves 200", func(t *testing.T) {
		if rec := get("/about/"); rec.Code != http.StatusOK {
			t.Fatalf("GET /about/ = %d, want 200", rec.Code)
		}
	})
	t.Run("REST pages link carries the nested path", func(t *testing.T) {
		rec := get("/wp-json/wp/v2/pages")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /wp-json/wp/v2/pages = %d, want 200", rec.Code)
		}
		var items []struct {
			Slug string `json:"slug"`
			Link string `json:"link"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("decode pages: %v", err)
		}
		var found bool
		for _, it := range items {
			if it.Slug != "team" {
				continue
			}
			found = true
			if !strings.HasSuffix(it.Link, "/about/team/") {
				t.Errorf("team link = %q, want suffix /about/team/", it.Link)
			}
		}
		if !found {
			t.Fatalf("nested page team not in REST pages: %s", rec.Body.String())
		}
	})
}

// testWriter routes slog output into the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
