package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
)

// Shared fixture for the M17 (custom type and taxonomy write) tests. Task 1.1
// introduces it; every later phase reuses it.

// customWritePassword is the password every fixture user is created with.
const customWritePassword = "correct-horse-battery-staple"

// customWriteRegistry builds the five-entry registry the M17 tests share: one
// flat type (product, capability_type product), one hierarchical type (guide,
// capability_type guide, public), one non-public type (secret), one
// hierarchical taxonomy (genre, attached to product and guide) and one flat
// taxonomy (badge, attached to product).
func customWriteRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	yes, no := true, false
	reg, err := registry.Build(config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "product", Public: &yes, Slug: "product", WithFront: &yes,
				CapabilityType: "product", Taxonomies: []string{"genre", "badge"}},
			{Name: "guide", Public: &yes, Slug: "guide", WithFront: &yes,
				Hierarchical: true, CapabilityType: "guide", Taxonomies: []string{"genre"}},
			{Name: "secret", Public: &no, CapabilityType: "secret"},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", WithFront: &yes,
				Hierarchical: true, ObjectTypes: []string{"product", "guide"}},
			{Name: "badge", Public: &yes, Slug: "badge", WithFront: &yes,
				ObjectTypes: []string{"product"}},
		},
	}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

// customWriteActor is one logged-in fixture user.
type customWriteActor struct {
	Login string
	Token string // session cookie value
	CSRF  string // X-CSRF-Token value
}

// customWriteEnv is the handler plus the fixture users.
type customWriteEnv struct {
	Handler http.Handler
	Admin   customWriteActor
	Editor  customWriteActor
	Author  customWriteActor
	Contrib customWriteActor
	// Products holds only edit_products (and read): the custom-role user.
	Products customWriteActor
	// Curator holds the full capability family of product, secret and guide
	// (task 17.1): the only fixture user that can write hierarchical guides.
	Curator customWriteActor
	// Repos and Prefix give a test direct SQL access to the same database.
	Repos  *storage.Repositories
	Prefix string
}

// productsCaps is the custom-role grant: read plus the whole capability family
// of the product and secret capability types (edit, edit others, edit
// published, publish, delete, delete others, delete published), and nothing
// from the built-in post family except the edit_posts admin floor (D13).
func productsCaps() []string { return capsFor("products", "secrets") }

// capsFor is read, the edit_posts admin floor and the seven-verb capability
// family of each plural suffix.
func capsFor(suffixes ...string) []string {
	caps := []string{"read", "edit_posts"}
	for _, s := range suffixes {
		for _, v := range []string{"edit_", "edit_others_", "edit_published_", "publish_", "delete_", "delete_others_", "delete_published_"} {
			caps = append(caps, v+s)
		}
	}
	return caps
}

// newCustomWriteHandler builds the production handler (buildHandler through
// assemblyHandler) over a migrated SQLite database with an administrator, an
// editor, an author, a contributor and a user holding only edit_products.
// withRegistry selects the five-entry registry or none.
func newCustomWriteHandler(t *testing.T, withRegistry bool) customWriteEnv {
	t.Helper()
	ctx := context.Background()
	repos, dbCfg := newAssemblySQLite(t)
	users := content.NewUserService(repos.Users, repos.UserMeta, dbCfg.TablePrefix)
	mk := func(login string, roles ...string) {
		t.Helper()
		if _, err := users.Bootstrap(ctx, domain.User{
			Login: login, Nicename: login, DisplayName: login, Email: login + "@example.test",
		}, customWritePassword, roles...); err != nil {
			t.Fatalf("bootstrap %s: %v", login, err)
		}
	}
	mk("admin", auth.RoleAdministrator)
	mk("editor", auth.RoleEditor)
	mk("author", auth.RoleAuthor)
	mk("contrib", auth.RoleContributor)
	mk("products", productsCaps()...)
	mk("curator", capsFor("products", "secrets", "guides")...)

	var reg *registry.Registry
	if withRegistry {
		reg = customWriteRegistry(t)
	}
	handler, sm := assemblyHandler(t, repos, dbCfg, reg)
	login := func(name string) customWriteActor {
		t.Helper()
		token, _, err := sm.Login(ctx, name, customWritePassword)
		if err != nil {
			t.Fatalf("login %s: %v", name, err)
		}
		_, sess, err := sm.Authenticate(ctx, token)
		if err != nil {
			t.Fatalf("authenticate %s: %v", name, err)
		}
		return customWriteActor{Login: name, Token: token, CSRF: sess.CSRFToken}
	}
	return customWriteEnv{
		Handler:  handler,
		Admin:    login("admin"),
		Editor:   login("editor"),
		Author:   login("author"),
		Contrib:  login("contrib"),
		Products: login("products"),
		Curator:  login("curator"),
		Repos:    repos,
		Prefix:   dbCfg.TablePrefix,
	}
}

// doJSON sends a request as actor (nil for anonymous) through h. A non-empty
// body is sent as application/json. Session callers always carry the CSRF
// header, which read requests ignore.
func doJSON(t *testing.T, h http.Handler, actor *customWriteActor, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != "" {
		rd = bytes.NewReader([]byte(body))
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if actor != nil {
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: actor.Token})
		req.Header.Set("X-CSRF-Token", actor.CSRF)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
