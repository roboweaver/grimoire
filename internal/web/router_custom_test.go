package web_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/web"
)

// M12 Phase 8.1 -- the RED server-wiring tests (design Component 8a).
//
// API ASSUMED (task 8.2 must match exactly, design.md 8a):
//
//	func (s *Server) WithRegistry(reg *registry.Registry, posts *content.CustomPostService) *Server
//	func (s *Server) requireCustomDeps(st routing.Structure)   // called from Routes()
//
// contentPatterns appends st.CustomPatterns() after ArchivePatterns() and before
// "/{slug}". The Server's permalinks carry the custom routes because the test
// applies them itself: WithPermalinks(st.WithCustomTypes(reg.Routes())).
// "Register only when wired" is expressed by the Structure: no custom entries
// means nothing registered and nothing panics.
//
// requireCustomDeps panics (Req 7.6) iff st.CustomPatterns() is non-empty and
//   - the registry is empty/nil or the CustomPostService is nil: the message
//     contains "call Server.WithRegistry(", or
//   - the TermService has no hierarchy: the message contains "custom taxonomy"
//     and "WithHierarchy". requireArchiveDeps already panics for a missing
//     hierarchy with a "category archive" message, so for the custom-specific
//     message to be observable requireCustomDeps must run BEFORE
//     requireArchiveDeps in Routes().
//
// These tests compile-fail until WithRegistry exists (undefined: WithRegistry).

// customWiring selects which dependencies the harness wires.
type customWiring struct {
	hierarchy   bool // TermService.WithHierarchy
	withReg     bool // call WithRegistry at all
	customPosts bool // pass a non-nil CustomPostService to WithRegistry
}

// customRegistry holds a public type `product` (archive `products`) and a
// taxonomy `genre` attached to it.
func customRegistry(t *testing.T, st routing.Structure) *registry.Registry {
	t.Helper()
	yes := true
	reg, err := registry.Build(config.ContentConfig{
		PostTypes: []config.PostTypeConfig{{
			Name:       "product",
			Public:     &yes,
			Slug:       "product",
			HasArchive: config.HasArchive{Enabled: true, Slug: "products"},
			Taxonomies: []string{"genre"},
		}},
		Taxonomies: []config.TaxonomyConfig{{
			Name:        "genre",
			Public:      &yes,
			Slug:        "genre",
			ObjectTypes: []string{"product"},
		}},
	}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

// buildCustomServer builds a Server over an empty migrated SQLite database. The
// permalinks carry the custom routes of reg when applyRoutes is true.
func buildCustomServer(t *testing.T, reg *registry.Registry, applyRoutes bool, w customWiring) *web.Server {
	t.Helper()
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
	if _, err := migrate.Apply(t.Context(), repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse("", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	if applyRoutes {
		types, taxes := reg.Routes()
		st = st.WithCustomTypes(types, taxes)
	}
	terms := content.NewTermService(repos.Terms, repos.Posts)
	if w.hierarchy {
		terms = terms.WithHierarchy(repos.TermReader)
	}
	posts := content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users)
	srv := web.NewServer(posts, terms, content.NewOptionService(repos.Options), eng, nil).
		WithThemeStatic(filepath.Join("..", "..", "themes"), "default").
		WithPermalinks(st)
	if w.withReg {
		var cps *content.CustomPostService
		if w.customPosts {
			cps = content.NewCustomPostService(repos.PostNodeReader, repos.Posts)
		}
		got := srv.WithRegistry(reg, cps)
		if got != srv {
			t.Fatalf("WithRegistry must return the same *Server for chaining")
		}
	}
	return srv
}

// routePatterns lists every "METHOD pattern" the handler registers.
func routePatterns(t *testing.T, h http.Handler) []string {
	t.Helper()
	var out []string
	err := chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	slices.Sort(out)
	return out
}

// routeMatches reports whether a GET for path matches a registered route.
func routeMatches(h http.Handler, path string) bool {
	return h.(chi.Routes).Match(chi.NewRouteContext(), http.MethodGet, path)
}

func mustPanicWith(t *testing.T, srv *web.Server, wants ...string) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatalf("Routes() did not panic; want a panic containing %q", wants)
		}
		msg, _ := r.(string)
		for _, want := range wants {
			if !strings.Contains(msg, want) {
				t.Fatalf("panic %q does not contain %q", msg, want)
			}
		}
	}()
	srv.Routes()
}

// A Server that never saw WithRegistry, on a plain Structure, serves exactly
// today's route set and 404s /products/x (Req 7.5).
func TestRouterCustomPlainServerRegistersNothing(t *testing.T) {
	plain, _ := routing.Parse("", "", "")
	reg := customRegistry(t, plain)
	w := customWiring{hierarchy: true}
	h := buildCustomServer(t, reg, false, w).Routes()

	for _, route := range routePatterns(t, h) {
		for _, p := range []string{"/products", "/product/", "/genre/"} {
			if strings.Contains(route, p) {
				t.Fatalf("plain server registered custom-looking route %q", route)
			}
		}
	}
	if routeMatches(h, "/products/x") {
		t.Errorf("/products/x matched a route on a server with no registry")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/products/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /products/x = %d, want 404", rec.Code)
	}

	// Wiring a registry onto a Structure that carries no custom entries changes
	// nothing: the route set is identical (the structure expresses the opt-in).
	w = customWiring{hierarchy: true, withReg: true, customPosts: true}
	wired := buildCustomServer(t, reg, false, w).Routes()
	if !slices.Equal(routePatterns(t, h), routePatterns(t, wired)) {
		t.Errorf("route set changed by WithRegistry on a plain Structure:\n%v\nvs\n%v",
			routePatterns(t, h), routePatterns(t, wired))
	}
}

// With a registry and a Structure carrying its routes, exactly the custom
// patterns are added, and both slash forms of the archive are registered.
func TestRouterCustomRegistersPatternsWhenWired(t *testing.T) {
	plain, _ := routing.Parse("", "", "")
	reg := customRegistry(t, plain)
	types, taxes := reg.Routes()
	customPats := plain.WithCustomTypes(types, taxes).CustomPatterns()
	if len(customPats) == 0 {
		t.Fatal("fixture: registry produced no custom patterns")
	}

	base := routePatterns(t, buildCustomServer(t, reg, false, customWiring{hierarchy: true}).Routes())
	wired := buildCustomServer(t, reg, true, customWiring{hierarchy: true, withReg: true, customPosts: true}).Routes()
	got := routePatterns(t, wired)

	want := slices.Clone(base)
	for _, p := range customPats {
		want = append(want, http.MethodGet+" "+p)
	}
	slices.Sort(want)
	want = slices.Compact(want)
	if !slices.Equal(got, want) {
		t.Errorf("route set = %v\nwant (plain set + CustomPatterns %v) = %v", got, customPats, want)
	}

	for _, p := range []string{"GET /products", "GET /products/", "GET /product/*", "GET /genre/*"} {
		if !slices.Contains(got, p) {
			t.Errorf("route %q not registered; got %v", p, got)
		}
	}
	// Both archive forms and an item path reach the router (Req 7.5, 7.7).
	for _, path := range []string{"/products", "/products/", "/product/x", "/genre/rock"} {
		if !routeMatches(wired, path) {
			t.Errorf("%s does not match a registered route", path)
		}
	}
	// An archive has no sub-paths (items live under the type slug; pagination is ?page=N).
	if routeMatches(wired, "/products/x") {
		t.Errorf("/products/x matched a registered route; archives register only their bare base")
	}
	// Bare "/products" must be an explicit registration, not only the flat
	// /{slug} fallback, and "/products/" must not rely on /{slug}/*.
	if !slices.Contains(got, "GET /products") || !slices.Contains(got, "GET /products/") {
		t.Errorf("archive base not registered in both slash forms: %v", got)
	}
}

// Existing routes are unaffected when a registry is wired.
func TestRouterCustomExistingRoutesUnaffected(t *testing.T) {
	plain, _ := routing.Parse("", "", "")
	reg := customRegistry(t, plain)
	h := buildCustomServer(t, reg, true, customWiring{hierarchy: true, withReg: true, customPosts: true}).Routes()

	for _, path := range []string{"/", "/healthz", "/category/x", "/category/x/y", "/tag/x", "/author/x", "/about"} {
		if !routeMatches(h, path) {
			t.Errorf("existing route %s no longer matches", path)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("/healthz = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
	// A path no registry entry owns still 404s.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nothing/here/at/all", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unowned path = %d, want 404", rec.Code)
	}
}

// The startup guard (Req 7.6), mirroring requireArchiveDeps.
func TestRouterCustomRequireDepsPanics(t *testing.T) {
	plain, _ := routing.Parse("", "", "")
	reg := customRegistry(t, plain)

	t.Run("no WithRegistry", func(t *testing.T) {
		srv := buildCustomServer(t, reg, true, customWiring{hierarchy: true})
		mustPanicWith(t, srv, "call Server.WithRegistry(")
	})
	t.Run("registry but nil CustomPostService", func(t *testing.T) {
		srv := buildCustomServer(t, reg, true, customWiring{hierarchy: true, withReg: true})
		mustPanicWith(t, srv, "call Server.WithRegistry(")
	})
	t.Run("no taxonomy reader", func(t *testing.T) {
		srv := buildCustomServer(t, reg, true, customWiring{withReg: true, customPosts: true})
		mustPanicWith(t, srv, "custom taxonomy", "WithHierarchy")
	})
	t.Run("fully wired does not panic", func(t *testing.T) {
		srv := buildCustomServer(t, reg, true, customWiring{hierarchy: true, withReg: true, customPosts: true})
		srv.Routes()
	})
}

// An empty registry carries no routes: nothing registered, nothing panics, even
// with no CustomPostService.
func TestRouterCustomEmptyRegistryRegistersNothing(t *testing.T) {
	plain, _ := routing.Parse("", "", "")
	empty, err := registry.Build(config.ContentConfig{}, plain)
	if err != nil {
		t.Fatalf("registry.Build(empty): %v", err)
	}
	base := routePatterns(t, buildCustomServer(t, empty, true, customWiring{hierarchy: true}).Routes())
	wired := routePatterns(t, buildCustomServer(t, empty, true, customWiring{hierarchy: true, withReg: true}).Routes())
	if !slices.Equal(base, wired) {
		t.Errorf("empty registry changed the route set:\n%v\nvs\n%v", base, wired)
	}
}
