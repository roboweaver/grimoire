package registry_test

import (
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

// Build takes already-defaulted configuration in production (config.Load runs
// applyDefaults, which is unexported). These helpers reproduce exactly those
// defaults: Public and WithFront pointers set to true, RESTBase and Slug
// defaulting to Name, CapabilityType defaulting to "post". Mutators then
// override individual fields the way an explicit YAML key would. As in
// applyDefaults, name-derived defaults are applied when Name is empty too (to
// the empty string), so R1 is still reported by Build.

type ptOpt func(*config.PostTypeConfig)
type txOpt func(*config.TaxonomyConfig)

func boolp(b bool) *bool { return &b }

func pt(name string, opts ...ptOpt) config.PostTypeConfig {
	c := config.PostTypeConfig{
		Name:           name,
		Public:         boolp(true),
		WithFront:      boolp(true),
		RESTBase:       name,
		Slug:           name,
		CapabilityType: "post",
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func tx(name string, opts ...txOpt) config.TaxonomyConfig {
	c := config.TaxonomyConfig{
		Name:      name,
		Public:    boolp(true),
		WithFront: boolp(true),
		RESTBase:  name,
		Slug:      name,
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func ptSlug(s string) ptOpt     { return func(c *config.PostTypeConfig) { c.Slug = s } }
func ptRest(s string) ptOpt     { return func(c *config.PostTypeConfig) { c.RESTBase = s } }
func ptCap(s string) ptOpt      { return func(c *config.PostTypeConfig) { c.CapabilityType = s } }
func ptPrivate() ptOpt          { return func(c *config.PostTypeConfig) { c.Public = boolp(false) } }
func ptNoFront() ptOpt          { return func(c *config.PostTypeConfig) { c.WithFront = boolp(false) } }
func ptTaxes(n ...string) ptOpt { return func(c *config.PostTypeConfig) { c.Taxonomies = n } }
func ptArchive(s string) ptOpt {
	return func(c *config.PostTypeConfig) { c.HasArchive = config.HasArchive{Enabled: true, Slug: s} }
}
func ptArchiveTrue() ptOpt {
	return func(c *config.PostTypeConfig) { c.HasArchive = config.HasArchive{Enabled: true} }
}

func txSlug(s string) txOpt       { return func(c *config.TaxonomyConfig) { c.Slug = s } }
func txRest(s string) txOpt       { return func(c *config.TaxonomyConfig) { c.RESTBase = s } }
func txPrivate() txOpt            { return func(c *config.TaxonomyConfig) { c.Public = boolp(false) } }
func txNoFront() txOpt            { return func(c *config.TaxonomyConfig) { c.WithFront = boolp(false) } }
func txObjects(n ...string) txOpt { return func(c *config.TaxonomyConfig) { c.ObjectTypes = n } }

func structure(t *testing.T, permalink, catBase, tagBase string) routing.Structure {
	t.Helper()
	st, err := routing.Parse(permalink, catBase, tagBase)
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", permalink, err)
	}
	return st
}

func cfgPT(p ...config.PostTypeConfig) config.ContentConfig {
	return config.ContentConfig{PostTypes: p}
}

func cfgTX(x ...config.TaxonomyConfig) config.ContentConfig {
	return config.ContentConfig{Taxonomies: x}
}

type buildCase struct {
	name string
	cfg  config.ContentConfig
	st   routing.Structure
	// want lists substrings that must ALL appear in the error text: the config
	// path and the offending value. Empty means Build must succeed.
	want []string
}

func runCases(t *testing.T, cases []buildCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := registry.Build(tc.cfg, tc.st)
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("Build: unexpected error: %v", err)
				}
				if reg == nil {
					t.Fatal("Build: nil registry without error")
				}
				return
			}
			if err == nil {
				t.Fatalf("Build: expected an error containing %q, got none", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
	}
}

func TestBuildHappyPath(t *testing.T) {
	flat := structure(t, "", "", "")
	front := structure(t, "/blog/%postname%/", "", "")
	cfg := config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			pt("book", ptArchiveTrue(), ptTaxes("genre", "category", "post_tag")),
			pt("event", ptArchive("events-list"), ptRest("events"), ptCap("page")),
			pt("hidden", ptPrivate(), ptSlug("admin")),
		},
		Taxonomies: []config.TaxonomyConfig{
			tx("genre", txObjects("book", "post", "page")),
			// A taxonomy may share a name with a post type (R4).
			tx("event", txRest("event-kinds"), txSlug("event-kind")),
		},
	}
	for name, st := range map[string]routing.Structure{"flat": flat, "front": front} {
		t.Run(name, func(t *testing.T) {
			if _, err := registry.Build(cfg, st); err != nil {
				t.Fatalf("Build: %v", err)
			}
		})
	}
}

// R8: Build has no database handle, so a declared type with zero rows cannot
// be an error. The happy path above is the proof; an empty config is the
// other end of it.
func TestBuildEmptyConfig(t *testing.T) {
	if _, err := registry.Build(config.ContentConfig{}, structure(t, "", "", "")); err != nil {
		t.Fatalf("Build(empty): %v", err)
	}
}

func TestBuildR1EmptyName(t *testing.T) {
	st := structure(t, "", "", "")
	runCases(t, []buildCase{
		{"post type", cfgPT(pt("ok"), pt("")), st, []string{"content.post_types[1].name"}},
		{"taxonomy", cfgTX(tx("")), st, []string{"content.taxonomies[0].name"}},
	})
}

func TestBuildR2NameAndCapability(t *testing.T) {
	st := structure(t, "", "", "")
	long21 := strings.Repeat("a", 21)
	long33 := strings.Repeat("b", 33)
	runCases(t, []buildCase{
		{"post type uppercase", cfgPT(pt("ok"), pt("Book")), st, []string{"content.post_types[1].name", "Book"}},
		{"post type space", cfgPT(pt("my book")), st, []string{"content.post_types[0].name", "my book"}},
		{"taxonomy bad char", cfgTX(tx("a.b")), st, []string{"content.taxonomies[0].name", "a.b"}},
		{"post type 21 chars", cfgPT(pt(long21)), st, []string{"content.post_types[0].name", long21}},
		{"post type 20 chars ok", cfgPT(pt(strings.Repeat("a", 20))), st, nil},
		{"taxonomy 33 chars", cfgTX(tx(long33)), st, []string{"content.taxonomies[0].name", long33}},
		{"taxonomy 32 chars ok", cfgTX(tx(strings.Repeat("b", 32))), st, nil},
		{"bad capability_type", cfgPT(pt("book", ptCap("Bad Cap"))), st,
			[]string{"content.post_types[0].capability_type", "Bad Cap"}},
	})
}

func TestBuildR3ReservedNames(t *testing.T) {
	st := structure(t, "", "", "")
	var cases []buildCase
	for _, n := range []string{
		"post", "page", "attachment", "revision", "nav_menu_item", "custom_css",
		"customize_changeset", "oembed_cache", "user_request", "wp_block",
		"wp_template", "wp_template_part", "wp_global_styles", "wp_navigation",
	} {
		cases = append(cases, buildCase{"post type " + n, cfgPT(pt(n)), st,
			[]string{"content.post_types[0].name", n}})
	}
	for _, n := range []string{"category", "post_tag", "nav_menu", "link_category", "post_format"} {
		cases = append(cases, buildCase{"taxonomy " + n, cfgTX(tx(n)), st,
			[]string{"content.taxonomies[0].name", n}})
	}
	runCases(t, cases)

	if got := len(registry.ReservedPostTypes()); got != 14 {
		t.Errorf("ReservedPostTypes: %d entries, want 14", got)
	}
	if got := len(registry.ReservedTaxonomies()); got != 5 {
		t.Errorf("ReservedTaxonomies: %d entries, want 5", got)
	}
}

func TestBuildR4Duplicates(t *testing.T) {
	st := structure(t, "", "", "")
	runCases(t, []buildCase{
		{"duplicate post type", cfgPT(pt("book"), pt("movie"), pt("book", ptSlug("b2"), ptRest("b2"))), st,
			[]string{"content.post_types[2].name", "book"}},
		{"duplicate taxonomy", cfgTX(tx("genre"), tx("genre", txSlug("g2"), txRest("g2"))), st,
			[]string{"content.taxonomies[1].name", "genre"}},
		{"post type and taxonomy may share a name",
			config.ContentConfig{
				PostTypes:  []config.PostTypeConfig{pt("shared")},
				Taxonomies: []config.TaxonomyConfig{tx("shared", txSlug("shared-tx"), txRest("shared-tx"))},
			}, st, nil},
	})
}

func TestBuildR5SlugShape(t *testing.T) {
	st := structure(t, "", "", "")
	runCases(t, []buildCase{
		{"multi-segment slug", cfgPT(pt("book", ptSlug("a/b"))), st,
			[]string{"content.post_types[0].slug", "a/b"}},
		{"uppercase slug", cfgPT(pt("book", ptSlug("Books"))), st,
			[]string{"content.post_types[0].slug", "Books"}},
		{"leading dash slug", cfgPT(pt("book", ptSlug("-books"))), st,
			[]string{"content.post_types[0].slug", "-books"}},
		{"taxonomy multi-segment slug", cfgTX(tx("genre", txSlug("x/y"))), st,
			[]string{"content.taxonomies[0].slug", "x/y"}},
		{"multi-segment archive slug", cfgPT(pt("book", ptArchive("a/b"))), st,
			[]string{"content.post_types[0].has_archive", "a/b"}},
		{"malformed archive slug", cfgPT(pt("book", ptArchive("Bad Slug"))), st,
			[]string{"content.post_types[0].has_archive", "Bad Slug"}},
		{"private entries are not slug-checked", cfgPT(pt("book", ptPrivate(), ptSlug("a/b"))), st, nil},
	})
}

func TestBuildR5Collisions(t *testing.T) {
	flat := structure(t, "", "", "")
	front := structure(t, "/blog/%postname%/", "", "")
	frontCustomCat := structure(t, "/blog/%postname%/", "topics", "")
	flatCustomCat := structure(t, "", "topics", "")
	deepFront := structure(t, "/blog/news/%postname%/", "", "")
	runCases(t, []buildCase{
		{"equal slugs across post types", cfgPT(pt("a", ptSlug("same")), pt("b", ptSlug("same"))), flat,
			[]string{"content.post_types[1].slug", "same"}},
		{"post type slug equals taxonomy slug", config.ContentConfig{
			PostTypes:  []config.PostTypeConfig{pt("book", ptSlug("shelf"))},
			Taxonomies: []config.TaxonomyConfig{tx("genre", txSlug("shelf"))},
		}, flat, []string{"content.taxonomies[0].slug", "shelf"}},
		{"archive slug collides with another slug",
			cfgPT(pt("a", ptArchive("lib")), pt("b", ptSlug("lib"))), flat,
			[]string{"content.post_types[1].slug", "lib"}},
		{"with_front sequence has the front-less one as prefix",
			cfgPT(pt("x"), pt("blogpt", ptSlug("blog"), ptNoFront())), front,
			[]string{"blog"}},
		{"category base equality (default)", cfgPT(pt("a", ptSlug("category"))), flat,
			[]string{"content.post_types[0].slug", "category"}},
		{"tag base equality (default)", cfgTX(tx("a", txSlug("tag"))), flat,
			[]string{"content.taxonomies[0].slug", "tag"}},
		{"author base equality", cfgPT(pt("a", ptSlug("author"))), flat,
			[]string{"content.post_types[0].slug", "author"}},
		{"author base carries the front", cfgPT(pt("a", ptSlug("author"))), front,
			[]string{"content.post_types[0].slug", "author"}},
		{"category base carries the front when unset", cfgPT(pt("a", ptSlug("category"))), front,
			[]string{"content.post_types[0].slug", "category"}},
		{"custom category_base without the front", cfgPT(pt("a", ptSlug("topics"), ptNoFront())), frontCustomCat,
			[]string{"content.post_types[0].slug", "topics"}},
		{"custom category_base on a flat structure", cfgTX(tx("a", txSlug("topics"))), flatCustomCat,
			[]string{"content.taxonomies[0].slug", "topics"}},
		{"slug equals the front", cfgPT(pt("a", ptSlug("blog"), ptNoFront())), front,
			[]string{"content.post_types[0].slug", "blog"}},
		{"slug equals a prefix of the front", cfgPT(pt("a", ptSlug("blog"), ptNoFront())), deepFront,
			[]string{"content.post_types[0].slug", "blog"}},
		{"taxonomy slug equals the front", cfgTX(tx("a", txSlug("blog"), txNoFront())), front,
			[]string{"content.taxonomies[0].slug", "blog"}},
		{"private entries are excluded from collisions",
			cfgPT(pt("a", ptSlug("same")), pt("b", ptSlug("same"), ptPrivate(), ptRest("b-rest")),
				pt("c", ptSlug("category"), ptPrivate(), ptRest("c-rest"))), flat, nil},
	})

	var routes []buildCase
	for _, r := range []string{
		"admin", "wp-json", "wp-admin", "wp-content", "wp-includes", "assets",
		"theme", "login", "logout", "comment", "healthz", "favicon.ico", "feed",
	} {
		routes = append(routes,
			buildCase{"reserved route slug " + r, cfgPT(pt("x", ptSlug(r))), flat,
				[]string{"content.post_types[0].slug", r}},
			buildCase{"reserved route archive " + r, cfgPT(pt("x", ptArchive(r))), flat,
				[]string{"content.post_types[0].has_archive", r}},
		)
	}
	routes = append(routes, buildCase{"reserved route taxonomy slug", cfgTX(tx("x", txSlug("wp-json"))), flat,
		[]string{"content.taxonomies[0].slug", "wp-json"}})
	runCases(t, routes)
}

func TestBuildR6RESTBase(t *testing.T) {
	st := structure(t, "", "", "")
	var cases []buildCase
	cases = append(cases,
		buildCase{"malformed rest_base", cfgPT(pt("book", ptRest("Bad/Base"))), st,
			[]string{"content.post_types[0].rest_base", "Bad/Base"}},
		buildCase{"duplicate across post types", cfgPT(pt("a", ptRest("shared")), pt("b", ptRest("shared"))), st,
			[]string{"content.post_types[1].rest_base", "shared"}},
		buildCase{"duplicate across taxonomies", cfgTX(tx("a", txRest("shared")), tx("b", txRest("shared"))), st,
			[]string{"content.taxonomies[1].rest_base", "shared"}},
		buildCase{"duplicate across kinds", config.ContentConfig{
			PostTypes:  []config.PostTypeConfig{pt("a", ptRest("shared"))},
			Taxonomies: []config.TaxonomyConfig{tx("b", txRest("shared"))},
		}, st, []string{"content.taxonomies[0].rest_base", "shared"}},
		buildCase{"private entries still count for rest_base", config.ContentConfig{
			PostTypes:  []config.PostTypeConfig{pt("a", ptRest("shared"))},
			Taxonomies: []config.TaxonomyConfig{tx("b", txRest("shared"), txPrivate())},
		}, st, []string{"content.taxonomies[0].rest_base", "shared"}},
		buildCase{"private post type rest_base reserved", cfgPT(pt("a", ptPrivate(), ptRest("posts"))), st,
			[]string{"content.post_types[0].rest_base", "posts"}},
	)
	for _, r := range []string{
		"posts", "pages", "media", "comments", "users", "categories", "tags",
		"types", "taxonomies", "statuses", "settings", "search", "blocks", "themes", "plugins",
	} {
		cases = append(cases,
			buildCase{"post type rest_base " + r, cfgPT(pt("x", ptRest(r))), st,
				[]string{"content.post_types[0].rest_base", r}},
			buildCase{"taxonomy rest_base " + r, cfgTX(tx("x", txRest(r))), st,
				[]string{"content.taxonomies[0].rest_base", r}},
		)
	}
	for _, k := range []string{
		"id", "date", "date_gmt", "modified", "modified_gmt", "slug", "status", "type",
		"link", "guid", "title", "content", "excerpt", "author", "featured_media",
		"comment_status", "ping_status",
	} {
		cases = append(cases, buildCase{"taxonomy rest_base json key " + k, cfgTX(tx("x", txRest(k))), st,
			[]string{"content.taxonomies[0].rest_base", k}})
	}
	runCases(t, cases)
}

func TestBuildR7References(t *testing.T) {
	st := structure(t, "", "", "")
	runCases(t, []buildCase{
		{"unknown taxonomy on a post type", cfgPT(pt("book", ptTaxes("nope"))), st,
			[]string{"content.post_types[0].taxonomies", "nope"}},
		{"reserved non-served taxonomy on a post type", cfgPT(pt("book", ptTaxes("nav_menu"))), st,
			[]string{"content.post_types[0].taxonomies", "nav_menu"}},
		{"unknown object type on a taxonomy", cfgTX(tx("genre", txObjects("nope"))), st,
			[]string{"content.taxonomies[0].object_types", "nope"}},
		{"reserved non-served object type on a taxonomy", cfgTX(tx("genre", txObjects("nav_menu_item"))), st,
			[]string{"content.taxonomies[0].object_types", "nav_menu_item"}},
		{"attachment is not an accepted object type", cfgTX(tx("genre", txObjects("attachment"))), st,
			[]string{"content.taxonomies[0].object_types", "attachment"}},
		{"built-in references resolve", config.ContentConfig{
			PostTypes:  []config.PostTypeConfig{pt("book", ptTaxes("category", "post_tag", "genre"))},
			Taxonomies: []config.TaxonomyConfig{tx("genre", txObjects("post", "page", "book"))},
		}, st, nil},
	})
}

func TestBuildJoinsAllViolations(t *testing.T) {
	st := structure(t, "", "", "")
	cfg := config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			pt("book", ptRest("posts")),   // R6
			pt("Bad Name", ptRest("bad")), // R2
		},
		Taxonomies: []config.TaxonomyConfig{
			tx("category"), // R3
		},
	}
	_, err := registry.Build(cfg, st)
	if err == nil {
		t.Fatal("Build: expected an error, got none")
	}
	for _, w := range []string{
		"content.post_types[0].rest_base", "posts",
		"content.post_types[1].name", "Bad Name",
		"content.taxonomies[0].name", "category",
	} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("joined error %q does not contain %q", err.Error(), w)
		}
	}
}
