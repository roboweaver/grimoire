package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// M12c Phase 16.1 -- RED tests for term assignment through the admin JSON API
// (Req 12.4, 12.6; D10).
//
// Registry fixture: product (genre attached), guide (badge attached); terms
// 9301/9302 in genre, 9303 in badge. A shop manager holds the product family.
//
// RED today: content.WithPostTermsRegistry / WithPostTermsReader (task 15.2)
// do not compile; once they exist, an unattached taxonomy is not reported in
// `partial` through the new sentinels and the detail `terms` map lists only
// category and post_tag for a registered type.
//
// Helper prefix: apte.

const (
	apteCSRF   = "apte-csrf-token"
	apteGenreA = int64(9301)
	apteGenreB = int64(9302)
	apteBadge  = int64(9303)
)

func apteConfig() config.ContentConfig {
	yes := true
	return config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "product", Public: &yes, Slug: "product", RESTBase: "products", CapabilityType: "product", Taxonomies: []string{"genre"}},
			{Name: "guide", Public: &yes, Slug: "guide", RESTBase: "guides", CapabilityType: "guide", Taxonomies: []string{"badge"}},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", RESTBase: "genre", ObjectTypes: []string{"product"}},
			{Name: "badge", Public: &yes, Slug: "badge", RESTBase: "badge", ObjectTypes: []string{"guide"}},
		},
	}
}

func apteRouter(t *testing.T) (http.Handler, *storage.Repositories) {
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
	for _, tm := range []struct {
		id         int64
		name, slug string
		taxonomy   string
		ttID       int64
	}{
		{apteGenreA, "Sci-Fi", "apte-scifi", "genre", 9401},
		{apteGenreB, "Noir", "apte-noir", "genre", 9402},
		{apteBadge, "Staff Pick", "apte-pick", "badge", 9403},
	} {
		if _, err := repos.DB().ExecContext(ctx, "INSERT INTO "+p+"terms (term_id, name, slug) VALUES (?, ?, ?)", tm.id, tm.name, tm.slug); err != nil {
			t.Fatalf("seed term: %v", err)
		}
		if _, err := repos.DB().ExecContext(ctx,
			"INSERT INTO "+p+"term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)",
			tm.ttID, tm.id, tm.taxonomy, "", 0, 0); err != nil {
			t.Fatalf("seed term_taxonomy: %v", err)
		}
	}
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	reg, err := registry.Build(apteConfig(), st)
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
	pw := content.NewPostWriteService(repos.PostWriter,
		content.WithPostRegistry(reg),
		content.WithPostNodeReader(repos.PostNodeReader),
		content.WithTermDetacher(repos.PostTermsWriter),
	)
	ptw := content.NewPostTermsWriteService(repos.PostWriter, repos.PostTermsWriter,
		content.WithPostTermsRegistry(reg),
		content.WithPostTermsReader(repos.TermReader),
	)
	termWrite := content.NewTermWriteService(
		termReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader},
		content.WithTermRegistry(reg),
	)
	keys := append([]string{auth.RoleEditor},
		"edit_products", "edit_others_products", "edit_published_products",
		"publish_products", "delete_products", "delete_others_products", "delete_published_products",
		"edit_guides", "edit_others_guides", "edit_published_guides",
		"publish_guides", "delete_guides", "delete_others_guides", "delete_published_guides")
	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "apte-user", keys),
		authSession:   domain.Session{ID: "apte-s", UserID: 1, CSRFToken: apteCSRF},
	}
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(pw, termWrite, ptw, repos.PostTerms).
		WithRegistry(reg, content.NewCustomPostService(repos.PostNodeReader, repos.Posts))
	return srv.Routes(), repos
}

func apteDo(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", apteCSRF)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "session-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func apteAssigned(t *testing.T, repos *storage.Repositories, postID int64, taxonomy string) []int64 {
	t.Helper()
	rows, err := repos.DB().QueryContext(context.Background(),
		"SELECT tt.term_id FROM wp_term_relationships tr JOIN wp_term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id "+
			"WHERE tr.object_id = ? AND tt.taxonomy = ? ORDER BY tt.term_id", postID, taxonomy)
	if err != nil {
		t.Fatalf("query assigned: %v", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// apteTermIDs returns the term ids listed under taxonomy in a detail response's
// terms map, and whether the key is present.
func apteTermIDs(t *testing.T, m map[string]any, taxonomy string) ([]int64, bool) {
	t.Helper()
	terms, _ := m["terms"].(map[string]any)
	raw, ok := terms[taxonomy]
	if !ok {
		return nil, false
	}
	arr, _ := raw.([]any)
	var out []int64
	for _, x := range arr {
		o, _ := x.(map[string]any)
		f, _ := o["id"].(float64)
		out = append(out, int64(f))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, true
}

func apteCreate(t *testing.T, h http.Handler, body string) map[string]any {
	t.Helper()
	rec := apteDo(t, h, http.MethodPost, "/admin/api/posts", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	return apcDecode(t, rec)
}

// Req 12.4, 12.6: termIds for an attached taxonomy assigns, and the detail
// terms map lists it.
func TestAdminPostTermsAttachedAssigns(t *testing.T) {
	h, repos := apteRouter(t)
	m := apteCreate(t, h, `{"title":"Widget","type":"product","status":"draft","termIds":{"genre":[9301,9302]}}`)
	id := apcID(t, m)
	if p, _ := m["partial"].(map[string]any); len(p) != 0 {
		t.Errorf("partial = %v, want none", p)
	}
	got := apteAssigned(t, repos, id, "genre")
	if len(got) != 2 || got[0] != apteGenreA || got[1] != apteGenreB {
		t.Errorf("stored genre terms = %v, want [9301 9302]", got)
	}
	if ids, ok := apteTermIDs(t, m, "genre"); !ok || len(ids) != 2 {
		t.Errorf("create response terms[genre] = %v (present=%v), want two ids", ids, ok)
	}
	rec := apteDo(t, h, http.MethodGet, "/admin/api/posts/"+strconv.FormatInt(id, 10), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", rec.Code, rec.Body.String())
	}
	if ids, ok := apteTermIDs(t, apcDecode(t, rec), "genre"); !ok || len(ids) != 2 {
		t.Errorf("detail terms[genre] = %v (present=%v), want two ids", ids, ok)
	}
}

// Req 12.4: a taxonomy not attached to the type is reported in partial,
// nothing is assigned for it, and the post write still succeeds.
func TestAdminPostTermsUnattachedReportedInPartial(t *testing.T) {
	h, repos := apteRouter(t)
	m := apteCreate(t, h, `{"title":"Widget","type":"product","status":"draft","termIds":{"genre":[9301],"badge":[9303]}}`)
	id := apcID(t, m)
	partial, _ := m["partial"].(map[string]any)
	if _, ok := partial["badge"]; !ok {
		t.Errorf("partial = %v, want a badge entry", partial)
	}
	if _, ok := partial["genre"]; ok {
		t.Errorf("partial reports the attached taxonomy genre: %v", partial)
	}
	if got := apteAssigned(t, repos, id, "badge"); len(got) != 0 {
		t.Errorf("badge assigned to a product: %v", got)
	}
	if got := apteAssigned(t, repos, id, "genre"); len(got) != 1 || got[0] != apteGenreA {
		t.Errorf("genre terms = %v, want [9301] (attached taxonomy still applied)", got)
	}
	if typ, _, _, _, ok := apcRow(t, repos, id); !ok || typ != "product" {
		t.Errorf("post row missing or wrong type %q (found=%v)", typ, ok)
	}
}

// Req 12.4: an id belonging to another taxonomy is reported in partial for the
// attached taxonomy and the post write still succeeds.
func TestAdminPostTermsForeignIDReportedInPartial(t *testing.T) {
	h, repos := apteRouter(t)
	m := apteCreate(t, h, `{"title":"Widget","type":"product","status":"draft","termIds":{"genre":[9303]}}`)
	id := apcID(t, m)
	partial, _ := m["partial"].(map[string]any)
	if _, ok := partial["genre"]; !ok {
		t.Errorf("partial = %v, want a genre entry", partial)
	}
	if got := apteAssigned(t, repos, id, "genre"); len(got) != 0 {
		t.Errorf("genre assigned despite a foreign id: %v", got)
	}
}

// Req 12.6: the detail terms map lists the type's attached taxonomies for a
// registered type and category/post_tag for built-ins, unchanged.
func TestAdminPostTermsDetailMapShape(t *testing.T) {
	h, _ := apteRouter(t)

	prod := apteCreate(t, h, `{"title":"Widget","type":"product","status":"draft"}`)
	if _, ok := apteTermIDs(t, prod, "genre"); !ok {
		t.Errorf("product terms lacks genre: %v", prod["terms"])
	}
	for _, k := range []string{"category", "post_tag", "badge"} {
		if _, ok := apteTermIDs(t, prod, k); ok {
			t.Errorf("product terms has %q, want only the attached taxonomies: %v", k, prod["terms"])
		}
	}

	guide := apteCreate(t, h, `{"title":"How-to","type":"guide","status":"draft"}`)
	if _, ok := apteTermIDs(t, guide, "badge"); !ok {
		t.Errorf("guide terms lacks badge: %v", guide["terms"])
	}
	if _, ok := apteTermIDs(t, guide, "genre"); ok {
		t.Errorf("guide terms has genre: %v", guide["terms"])
	}

	post := apteCreate(t, h, `{"title":"Plain","type":"post","status":"draft"}`)
	for _, k := range []string{"category", "post_tag"} {
		if _, ok := apteTermIDs(t, post, k); !ok {
			t.Errorf("built-in post terms lacks %q: %v", k, post["terms"])
		}
	}
	for _, k := range []string{"genre", "badge"} {
		if _, ok := apteTermIDs(t, post, k); ok {
			t.Errorf("built-in post terms has %q: %v", k, post["terms"])
		}
	}
}
