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
	"github.com/roboweaver/grimoire/internal/registry"
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

// seedAssemblyCustom adds published custom-type rows and terms (plain SQL,
// SQLite quoting): book dune (genre scifi, shelf classics under fiction), a
// hierarchical volume vol-1 > part-one, and the term rows themselves.
func seedAssemblyCustom(t *testing.T, repos *storage.Repositories, prefix string) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repos.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed sql %q: %v", q, err)
		}
	}
	const ins = `INSERT INTO ` + "wp_" + `posts ` +
		`(ID, post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_type, comment_status, post_parent, post_mime_type, menu_order) ` +
		`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	rows := [][]any{
		{20, 1, "2024-02-01 00:00:00", "<p>dune body</p>", "Dune", "", "publish", "dune", "book", "closed", 0, "", 0},
		{21, 1, "2024-02-02 00:00:00", "<p>vol one</p>", "Vol One", "", "publish", "vol-1", "volume", "closed", 0, "", 0},
		{22, 1, "2024-02-03 00:00:00", "<p>part one</p>", "Part One", "", "publish", "part-one", "volume", "closed", 21, "", 0},
	}
	for _, r := range rows {
		exec(ins, r...)
	}
	term := func(termID, ttID int64, name, slug, taxonomy string, parentTermID int64) {
		exec("INSERT INTO "+prefix+"terms (term_id, name, slug) VALUES (?, ?, ?)", termID, name, slug)
		exec("INSERT INTO "+prefix+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
			ttID, termID, taxonomy, "", parentTermID, 0)
	}
	term(501, 601, "Science Fiction", "scifi", "genre", 0)
	term(503, 603, "Fiction Shelf", "fiction", "shelf", 0)
	term(504, 604, "Classics Shelf", "classics", "shelf", 503)
	for _, rel := range [][2]int64{{20, 601}, {20, 604}} {
		exec("INSERT INTO "+prefix+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)", rel[0], rel[1], 0)
	}
}

// TestBuildHandlerServesCustomTypes drives buildHandler with a registry and
// asserts HTML through httptest only (the /wp-json custom-type assertions land
// in PR 3). The test passes the RAW parsed Structure: buildHandler itself must
// apply Structure.WithCustomTypes, so dropping that call fails every custom row.
//
//	assertion                                       guards
//	----------------------------------------------  -------------------------------------------
//	GET /book/dune/            -> 200               deps.Registry -> Server.WithRegistry +
//	                                                CustomPostService + WithCustomTypes (single)
//	GET /library/              -> 200               Server.WithRegistry (archive route/resolve);
//	                                                removing WithRegistry makes this 404
//	GET /volume/vol-1/part-one/-> 200               WithRegistry + WithCustomTypes (hierarchical)
//	GET /volume/part-one/      -> 301 canonical     CustomPostService ancestry (wrong-ancestry 301)
//	GET /genre/scifi/          -> 200               WithRegistry + WithCustomTypes + TermService
//	GET /shelf/fiction/classics/ -> 200             hierarchy (nested term walk)
//	GET /movies/alien/         -> 404               unregistered path stays 404
//	empty registry: /about/team/ 200, /team/ 301,   a site without content: is unchanged
//	  /about/ 200, /book/dune/ 404, /library/ 404   (Req 1.5): nothing custom registered
func TestBuildHandlerServesCustomTypes(t *testing.T) {
	repos, dbCfg := newAssemblySQLite(t)
	seedAssemblyPages(t, repos, dbCfg.TablePrefix)
	seedAssemblyCustom(t, repos, dbCfg.TablePrefix)

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
	// RAW structure: no custom routes. buildHandler must add them.
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	yes := true
	reg, err := registry.Build(config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "book", Public: &yes, Slug: "book", WithFront: &yes,
				HasArchive: config.HasArchive{Enabled: true, Slug: "library"},
				Taxonomies: []string{"genre", "shelf"}},
			{Name: "volume", Public: &yes, Slug: "volume", WithFront: &yes, Hierarchical: true},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", WithFront: &yes, ObjectTypes: []string{"book"}},
			{Name: "shelf", Public: &yes, Slug: "shelf", WithFront: &yes, Hierarchical: true, ObjectTypes: []string{"book"}},
		},
	}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	emptyReg, err := registry.Build(config.ContentConfig{}, st)
	if err != nil {
		t.Fatalf("registry.Build (empty): %v", err)
	}

	build := func(r *registry.Registry) http.Handler {
		t.Helper()
		h, _, err := buildHandler(serverDeps{
			Repos:      repos,
			Config:     cfg,
			Log:        slog.New(slog.NewTextHandler(testWriter{t}, nil)),
			Permalinks: st,
			Registry:   r,
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
		return h
	}
	getFrom := func(h http.Handler, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	full := build(reg)
	for _, path := range []string{
		"/book/dune/", "/library/", "/volume/vol-1/part-one/",
		"/genre/scifi/", "/shelf/fiction/", "/shelf/fiction/classics/",
	} {
		t.Run("200 "+path, func(t *testing.T) {
			if rec := getFrom(full, path); rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", path, rec.Code)
			}
		})
	}
	t.Run("wrong-ancestry volume 301s to canonical", func(t *testing.T) {
		rec := getFrom(full, "/volume/part-one/")
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("GET /volume/part-one/ = %d, want 301", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/volume/vol-1/part-one/" {
			t.Fatalf("Location = %q, want /volume/vol-1/part-one/", loc)
		}
	})
	t.Run("unregistered path 404", func(t *testing.T) {
		if rec := getFrom(full, "/movies/alien/"); rec.Code != http.StatusNotFound {
			t.Fatalf("GET /movies/alien/ = %d, want 404", rec.Code)
		}
	})
	t.Run("page rows still served", func(t *testing.T) {
		if rec := getFrom(full, "/about/team/"); rec.Code != http.StatusOK {
			t.Fatalf("GET /about/team/ = %d, want 200", rec.Code)
		}
	})

	// A site without a content: section is unchanged (Req 1.5).
	empty := build(emptyReg)
	for path, want := range map[string]int{
		"/about/team/": http.StatusOK,
		"/about/":      http.StatusOK,
		"/book/dune/":  http.StatusNotFound,
		"/library/":    http.StatusNotFound,
	} {
		t.Run("empty registry "+path, func(t *testing.T) {
			if rec := getFrom(empty, path); rec.Code != want {
				t.Fatalf("GET %s = %d, want %d", path, rec.Code, want)
			}
		})
	}
	t.Run("empty registry flat leaf 301", func(t *testing.T) {
		rec := getFrom(empty, "/team/")
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/about/team/" {
			t.Fatalf("GET /team/ = %d Location %q, want 301 /about/team/", rec.Code, rec.Header().Get("Location"))
		}
	})
}

// testWriter routes slog output into the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
