package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// M12c Phase 4.1 -- RED tests for custom-taxonomy writes through the admin JSON
// API (/admin/api/terms) (Req 3, 4.2-4.7, 5.1-5.5; D9).
//
// Registered here: genre (hierarchical) and badge (flat). The live
// content.TermWriteService is built with content.WithTermRegistry, so the
// custom-taxonomy vocabulary, slug and name rules apply. Seeded rows (clear of
// the storagetest fixture ids):
//
//	term 701 "Fiction" genre top-level (tt 801)
//	term 702 "Poetry"  genre, child of 701 (tt 802)
//	term 703 "Hot"     badge  (tt 803)
//
// Auth reuses manageCategoriesFake/noManageCategoriesFake/doAdminTermWrite from
// adminapi_terms_parent_test.go. The RED today: content.WithTermRegistry does
// not exist (compile failure); once stubbed, `genre` lists 400 "unknown
// taxonomy" and the new sentinels surface as 500.

const (
	atcFiction = int64(701)
	atcPoetry  = int64(702)
	atcHot     = int64(703)
)

func atcConfig() config.ContentConfig {
	yes := true
	return config.ContentConfig{
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", RESTBase: "genres", Hierarchical: true},
			{Name: "badge", Public: &yes, Slug: "badge", RESTBase: "badges"},
		},
	}
}

func newATCRouter(t *testing.T, fake *fakeSessions) (http.Handler, *storage.Repositories) {
	t.Helper()
	ctx := context.Background()
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: filepath.Join(t.TempDir(), "grimoire.db"), TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })
	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}
	p := cfg.TablePrefix
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repos.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed sql %q: %v", q, err)
		}
	}
	for _, tm := range []struct {
		id         int64
		name, slug string
	}{{atcFiction, "Fiction", "fiction"}, {atcPoetry, "Poetry", "poetry"}, {atcHot, "Hot", "hot"}} {
		exec("INSERT INTO "+p+"terms (term_id, name, slug) VALUES (?, ?, ?)", tm.id, tm.name, tm.slug)
	}
	for _, tt := range []struct {
		ttID, termID int64
		taxonomy     string
		parent       int64
	}{{801, atcFiction, "genre", 0}, {802, atcPoetry, "genre", atcFiction}, {803, atcHot, "badge", 0}} {
		exec("INSERT INTO "+p+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
			tt.ttID, tt.termID, tt.taxonomy, "", tt.parent, 0)
	}

	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	reg, err := registry.Build(atcConfig(), st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	termWrite := content.NewTermWriteService(
		termReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader},
		content.WithTermRegistry(reg),
	)
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(nil, termWrite, nil, nil).
		WithRegistry(reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	return srv.Routes(), repos
}

func atcCount(t *testing.T, repos *storage.Repositories, taxonomy string) int {
	t.Helper()
	terms, err := repos.TermReader.ListByTaxonomy(context.Background(), taxonomy)
	if err != nil {
		t.Fatalf("ListByTaxonomy(%s): %v", taxonomy, err)
	}
	return len(terms)
}

func atcDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	return m
}

func atcWantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, want, rec.Body.String())
	}
}

// Req 5.1: create in genre -> 201, stored taxonomy and parent applied.
func TestAdminTermCustomCreateAppliesTaxonomyAndParent(t *testing.T) {
	h, repos := newATCRouter(t, manageCategoriesFake())
	rec := doAdminTermWrite(t, h, http.MethodPost, "/admin/api/terms",
		`{"name":"Drama","slug":"drama","taxonomy":"genre","parent":701}`)
	atcWantStatus(t, rec, http.StatusCreated)
	body := atcDecode(t, rec)
	if body["taxonomy"] != "genre" {
		t.Fatalf("taxonomy = %v, want genre; body=%s", body["taxonomy"], rec.Body.String())
	}
	id, _ := body["id"].(float64)
	if id == 0 {
		t.Fatalf("no created id: %s", rec.Body.String())
	}
	if got := termParentByID(t, repos, int64(id)); got != atcFiction {
		t.Fatalf("stored parent = %d, want %d", got, atcFiction)
	}
	terms, err := repos.TermReader.TermsByIDs(context.Background(), []int64{int64(id)})
	if err != nil || len(terms) != 1 || terms[0].Taxonomy != "genre" {
		t.Fatalf("stored term = %+v err=%v, want taxonomy genre", terms, err)
	}
}

// Req 5.2: PUT on a genre term load-and-merges and echoes taxonomy genre.
func TestAdminTermCustomUpdateEchoesTaxonomy(t *testing.T) {
	h, repos := newATCRouter(t, manageCategoriesFake())
	rec := doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/702", `{"name":"Verse"}`)
	atcWantStatus(t, rec, http.StatusOK)
	body := atcDecode(t, rec)
	if body["taxonomy"] != "genre" || body["name"] != "Verse" || body["slug"] != "poetry" {
		t.Fatalf("response = %s, want taxonomy genre, name Verse, slug poetry (merged)", rec.Body.String())
	}
	if got := termParentByID(t, repos, atcPoetry); got != atcFiction {
		t.Fatalf("parent = %d, want unchanged %d", got, atcFiction)
	}
}

// Req 5.3: list accepts a registered taxonomy; unknown still 400.
func TestAdminTermCustomList(t *testing.T) {
	h, _ := newATCRouter(t, manageCategoriesFake())
	rec := doAdminTermWrite(t, h, http.MethodGet, "/admin/api/terms?taxonomy=genre", "")
	atcWantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"fiction"`) || !strings.Contains(rec.Body.String(), `"poetry"`) {
		t.Fatalf("genre list missing seeded terms: %s", rec.Body.String())
	}
	rec = doAdminTermWrite(t, h, http.MethodGet, "/admin/api/terms?taxonomy=nonsense", "")
	atcWantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "unknown taxonomy") {
		t.Fatalf("body = %s, want unknown taxonomy", rec.Body.String())
	}
}

// Req 4.6, 4.7: unknown taxonomy -> 400, nothing written; built-ins unchanged.
func TestAdminTermCustomUnknownTaxonomyAndBuiltins(t *testing.T) {
	h, repos := newATCRouter(t, manageCategoriesFake())
	rec := doAdminTermWrite(t, h, http.MethodPost, "/admin/api/terms",
		`{"name":"X","slug":"x","taxonomy":"nonsense"}`)
	atcWantStatus(t, rec, http.StatusBadRequest)
	if n := atcCount(t, repos, "nonsense"); n != 0 {
		t.Fatalf("unknown taxonomy wrote %d rows", n)
	}
	for _, tax := range []string{"category", "post_tag"} {
		rec = doAdminTermWrite(t, h, http.MethodPost, "/admin/api/terms",
			`{"name":"Plain `+tax+`","slug":"plain-`+tax+`","taxonomy":"`+tax+`"}`)
		atcWantStatus(t, rec, http.StatusCreated)
		if got := atcDecode(t, rec)["taxonomy"]; got != tax {
			t.Fatalf("%s create echoed taxonomy %v", tax, got)
		}
	}
}

// Req 5.4: no manage_categories -> 403 and nothing written.
func TestAdminTermCustomForbiddenWithoutCapability(t *testing.T) {
	h, repos := newATCRouter(t, noManageCategoriesFake())
	before := atcCount(t, repos, "genre")
	rec := doAdminTermWrite(t, h, http.MethodPost, "/admin/api/terms",
		`{"name":"Drama","slug":"drama","taxonomy":"genre"}`)
	atcWantStatus(t, rec, http.StatusForbidden)
	rec = doAdminTermWrite(t, h, http.MethodPut, "/admin/api/terms/702", `{"name":"Verse"}`)
	atcWantStatus(t, rec, http.StatusForbidden)
	if after := atcCount(t, repos, "genre"); after != before {
		t.Fatalf("genre terms %d -> %d on forbidden write", before, after)
	}
}

// Req 5.5: a session without the CSRF header keeps the existing failure (403).
func TestAdminTermCustomRequiresCSRF(t *testing.T) {
	h, repos := newATCRouter(t, manageCategoriesFake())
	before := atcCount(t, repos, "genre")
	req := httptest.NewRequest(http.MethodPost, "/admin/api/terms",
		strings.NewReader(`{"name":"Drama","slug":"drama","taxonomy":"genre"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "session-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	atcWantStatus(t, rec, http.StatusForbidden)
	if after := atcCount(t, repos, "genre"); after != before {
		t.Fatalf("genre terms %d -> %d without CSRF", before, after)
	}
}

// Req 3, 4: the term-service sentinels map to 400, not 500, and write nothing.
func TestAdminTermCustomSentinelsAre400(t *testing.T) {
	cases := []struct {
		name, method, path, body string
	}{
		{"slug taken create", http.MethodPost, "/admin/api/terms", `{"name":"Dup","slug":"fiction","taxonomy":"genre"}`},
		{"slug taken update", http.MethodPut, "/admin/api/terms/702", `{"parent":0,"slug":"fiction"}`},
		{"invalid slug", http.MethodPost, "/admin/api/terms", `{"name":"Bad","slug":"Bad Slug!","taxonomy":"genre"}`},
		{"empty name after sanitize", http.MethodPost, "/admin/api/terms", `{"name":"<b></b>","slug":"empty","taxonomy":"genre"}`},
		{"parent cycle", http.MethodPut, "/admin/api/terms/701", `{"parent":702}`},
		{"parent on flat taxonomy", http.MethodPost, "/admin/api/terms", `{"name":"Hotter","slug":"hotter","taxonomy":"badge","parent":703}`},
		{"parent not in taxonomy", http.MethodPost, "/admin/api/terms", `{"name":"Odd","slug":"odd","taxonomy":"genre","parent":703}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, repos := newATCRouter(t, manageCategoriesFake())
			before := atcCount(t, repos, "genre") + atcCount(t, repos, "badge")
			rec := doAdminTermWrite(t, h, tc.method, tc.path, tc.body)
			atcWantStatus(t, rec, http.StatusBadRequest)
			assertAdminJSONErrorCode(t, rec, "bad_request")
			if after := atcCount(t, repos, "genre") + atcCount(t, repos, "badge"); after != before {
				t.Fatalf("rejected write changed term count %d -> %d", before, after)
			}
		})
	}
}
