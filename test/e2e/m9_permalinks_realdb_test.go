package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/web"
)

// Gating and discovery for the real-WordPress end-to-end check. The DSN
// variable is the same one internal/storage/storagetest's TestRealWordPressDB
// and internal/routing's TestRealWordPressPermalinks use, so one exported
// variable enables every real-database check in the repo.
const (
	realWPEnvDSN    = "GRIMOIRE_TEST_WP_DSN"
	realWPEnvPrefix = "GRIMOIRE_TEST_WP_PREFIX"

	// realWPDefaultPrefix matches the podman stack in accuweaverllc/scripts
	// (podman-wordpress-pods.sh sets TABLE_PREFIX=accuweaver), which is the
	// fixture Req 7.4 names.
	realWPDefaultPrefix = "accuweaver"
	// realWPWordPressDefaultPrefix is the prefix Req 7.4 requires this fixture
	// NOT to run against, because a wp_ target would leave the non-default
	// prefix claim unexercised.
	realWPWordPressDefaultPrefix = "wp_"

	// realWPScanPosts is how many recent posts are read to find candidates;
	// realWPCheckPosts is how many are actually driven over HTTP. Scanning wider
	// than it checks lets the loop skip posts whose slugs are percent-encoded
	// without running out of candidates.
	realWPScanPosts  = 25
	realWPCheckPosts = 5
)

// TestM9PermalinksRealDBE2E boots the whole stack -- storage repositories, the
// content services, the render engine and the real chi router behind an HTTP
// server -- against a restored, REAL WordPress database and asserts over real
// HTTP that each published post's canonical URL renders 200, the flat /{slug}
// URL 301s to it, and an unrelated path 404s (M9a Req 7.4).
//
// It is gated behind GRIMOIRE_TEST_WP_DSN in the manner of
// ../../internal/storage/storagetest's TestRealWordPressDB, so the default
// hermetic `go test ./...` skips it and CI needs no database.
//
// Run it against the podman stack in accuweaverllc/scripts with:
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress?parseTime=true' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./test/e2e/ -run TestM9PermalinksRealDBE2E -v -count=1
//
// # How this differs from the two tests that already exist
//
// TestM9PermalinksE2E (task 7.1) drives the same stack but over a seeded SQLite
// database with wp_ tables and a structure this repo wrote itself, so it cannot
// catch anything that depends on how WordPress actually populated the rows.
// TestRealWordPressPermalinks (task 1.9) does use real rows, but only at
// resolver level: it never builds a router, a handler or a response, so a
// structure that resolves correctly and still fails to reach chi -- or a post
// that resolves and then fails to render -- passes it. This test is the
// full-stack counterpart, which is why task 1.9's note kept 7.2 open.
//
// # Read-only
//
// storage.New opens the pool without migrating, and nothing here writes: the
// permalink structure is READ from the target site's own options rather than
// inserted as 7.1 does, and every assertion is a GET. grimoire does not own
// this database.
//
// # Expectations are derived, not borrowed
//
// The expected canonical path is built by substituting tokens into the site's
// own permalink_structure string (realWPExpandPermalink), never by calling
// routing.Canonical. So the 200 on that path is a genuine cross-check: if
// Canonical disagreed with the derivation by even a trailing slash, the handler
// would 301 the derived path instead of rendering it and this test would fail.
func TestM9PermalinksRealDBE2E(t *testing.T) {
	dsn := os.Getenv(realWPEnvDSN)
	if dsn == "" {
		t.Skipf("set %s to run the real WordPress permalink e2e validation", realWPEnvDSN)
	}
	prefix := os.Getenv(realWPEnvPrefix)
	if prefix == "" {
		prefix = realWPDefaultPrefix
	}
	// Req 7.4 requires this fixture to exercise a non-default table prefix. A
	// wp_ target would still exercise the permalink paths, but it would report
	// success for a claim it never checked, so it skips rather than passing
	// under false colours.
	if prefix == realWPWordPressDefaultPrefix {
		t.Skipf("%s=%q is the WordPress default; this fixture must exercise a "+
			"non-default table prefix (the podman stack in accuweaverllc/scripts uses %q)",
			realWPEnvPrefix, prefix, realWPDefaultPrefix)
	}

	ctx := t.Context()
	cfg := config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix}
	repos, err := storage.New(cfg) // opens the pool WITHOUT migrating
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	// The same read cmd/grimoire's resolvePermalinks performs: three options,
	// one OptionService, parsed together. resolvePermalinks lives in package
	// main and cannot be imported, so the read and parse are reproduced.
	options := content.NewOptionService(repos.Options)
	raw := options.Get(ctx, content.OptionPermalinkStructure)
	permalinks, err := routing.Parse(
		raw,
		options.Get(ctx, content.OptionCategoryBase),
		options.Get(ctx, content.OptionTagBase),
	)
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v — this site's structure is not yet supported", raw, err)
	}
	if permalinks.Flat {
		t.Skipf("site uses plain permalinks (%q); there is no canonical path to serve or redirect to", raw)
	}
	if !realWPIsDated(raw) {
		t.Skipf("site structure %q carries no date tokens; this fixture must exercise a "+
			"dated structure (the podman stack in accuweaverllc/scripts uses "+
			"/%%year%%/%%monthnum%%/%%day%%/%%postname%%/)", raw)
	}
	t.Logf("target site: prefix=%q permalink_structure=%q trailing_slash=%v",
		prefix, raw, permalinks.TrailingSlash)

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		options,
		eng,
		nil,
	).WithPermalinks(permalinks)

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	// ts.Client() returns the same *http.Client every call, so the override
	// below would leak into any later use of it. Two separate clients: one that
	// stops at the redirect so its status and Location can be asserted, one that
	// follows it so a loop cannot hide behind a single hop.
	stop := &http.Client{Transport: ts.Client().Transport}
	stop.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	follow := &http.Client{Transport: ts.Client().Transport}

	posts, err := repos.Posts.RecentPosts(ctx, realWPScanPosts, 0)
	if err != nil {
		t.Fatalf("RecentPosts: %v (a MySQL DSN needs parseTime=true for the date columns)", err)
	}
	if len(posts) == 0 {
		t.Skip("no published posts in the target database")
	}

	checked := 0
	for _, p := range posts {
		if checked == realWPCheckPosts {
			break
		}
		// Real sites carry percent-encoded slugs for non-Latin titles. Those
		// are a URL-escaping question, not a permalink-structure one, and
		// asserting on them here would test the wrong thing.
		if !realWPPathSafeSlug(p.Slug) {
			t.Logf("skipping post %d: slug %q is not a plain URL token", p.ID, p.Slug)
			continue
		}
		checked++

		canonical := realWPExpandPermalink(raw, p)
		flat := "/" + p.Slug

		t.Run(p.Slug, func(t *testing.T) {
			// Canonical URL renders. This also carries the cross-check: the
			// path is derived from the structure string, so a Canonical that
			// built anything else would redirect here instead of rendering.
			resp, body := realWPGet(t, stop, ts.URL+canonical)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (Location %q, body %.200s)",
					canonical, resp.StatusCode, resp.Header.Get("Location"), body)
			}
			// The rendered date proves the post behind the URL is the one whose
			// date built it. single.tmpl emits it in a machine-readable
			// attribute, so this needs no assumptions about HTML escaping of
			// real titles.
			marker := fmt.Sprintf("datetime=%q", p.Date.Format("2006-01-02"))
			if !strings.Contains(body, marker) {
				t.Errorf("GET %s body missing %s", canonical, marker)
			}

			// The flat path is what grimoire served before M9a and what its own
			// REST API advertised, so it must redirect rather than 404 or serve
			// a second copy.
			resp, body = realWPGet(t, stop, ts.URL+flat)
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("GET %s = %d, want 301 (body %.200s)", flat, resp.StatusCode, body)
			}
			if got := resp.Header.Get("Location"); got != canonical {
				t.Fatalf("GET %s Location = %q, want %q", flat, got, canonical)
			}

			// Query strings survive the redirect (Req 3.4) -- losing them would
			// silently break every campaign and tracking link into the site.
			const query = "?utm_source=grimoire-e2e"
			resp, _ = realWPGet(t, stop, ts.URL+flat+query)
			if got := resp.Header.Get("Location"); got != canonical+query {
				t.Errorf("GET %s Location = %q, want %q", flat+query, got, canonical+query)
			}

			// Following the redirect must land on a rendering page, not another
			// redirect: a loop here would take out every published URL on the
			// site (Req 3.5).
			resp, body = realWPGet(t, follow, ts.URL+flat)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s following redirects = %d, want 200 (body %.200s)",
					flat, resp.StatusCode, body)
			}
			if got := resp.Request.URL.Path; got != canonical {
				t.Errorf("GET %s followed to %q, want %q", flat, got, canonical)
			}
		})
	}
	if checked == 0 {
		t.Skipf("none of the %d most recent posts had a plain URL-token slug", len(posts))
	}

	t.Run("unrelated path 404s", func(t *testing.T) {
		const path = "/grimoire-m9-realdb-no-such-post"
		resp, body := realWPGet(t, stop, ts.URL+path)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 (body %.200s)", path, resp.StatusCode, body)
		}
	})

	t.Logf("validated %d real permalinks end-to-end against structure %q", checked, raw)
}

// realWPGet performs one GET and returns the response with its body already
// read, so callers can assert on both without leaking the body.
func realWPGet(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", url, err)
	}
	return resp, string(body)
}

// realWPExpandPermalink builds the path a post should be served at by
// substituting tokens into the raw permalink_structure directly.
//
// It deliberately does not call routing.Canonical: deriving the expectation
// independently is what makes this a cross-check of the implementation against
// the site's configuration rather than the implementation agreeing with itself.
// The raw structure is used verbatim, so its leading and trailing slashes carry
// through exactly as WordPress stored them.
func realWPExpandPermalink(raw string, p domain.Post) string {
	return strings.NewReplacer(
		"%year%", fmt.Sprintf("%04d", p.Date.Year()),
		"%monthnum%", fmt.Sprintf("%02d", int(p.Date.Month())),
		"%day%", fmt.Sprintf("%02d", p.Date.Day()),
		"%postname%", p.Slug,
		"%post_id%", strconv.FormatInt(p.ID, 10),
	).Replace(raw)
}

// realWPIsDated reports whether a structure carries any date token, which Req
// 7.4 requires of this fixture's target site.
func realWPIsDated(raw string) bool {
	return strings.Contains(raw, "%year%") ||
		strings.Contains(raw, "%monthnum%") ||
		strings.Contains(raw, "%day%")
}

// realWPPathSafeSlug reports whether a slug can be placed in a request path
// unescaped. WordPress percent-encodes slugs derived from non-Latin titles, and
// those raise a URL-escaping question this test is not about.
func realWPPathSafeSlug(slug string) bool {
	if slug == "" {
		return false
	}
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// realWPCheckCategories is how many nested categories are driven over HTTP. The
// reference database has 28 of them; validating every one would add a minute of
// requests for no extra coverage, because they differ only in their slugs.
const realWPCheckCategories = 5

// TestM9bNestedCategoriesRealDBE2E boots the same whole stack as
// TestM9PermalinksRealDBE2E above -- storage repositories, the content services,
// the render engine and the real chi router behind an HTTP server -- against a
// restored, REAL WordPress database, and asserts over real HTTP that a genuinely
// nested category's canonical path renders 200 and its flat path 301s to it
// (Req 12.6).
//
// Same gating as every other real-database check in the repo: GRIMOIRE_TEST_WP_DSN
// with the prefix from GRIMOIRE_TEST_WP_PREFIX. No new mechanism, so one exported
// variable still enables the lot.
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress?parseTime=true' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./test/e2e/ -run TestM9bNestedCategoriesRealDBE2E -v -count=1
//
// # How this differs from the two tests that already exist
//
// TestM9bArchivesE2E (task 10.1) drives the same stack over a seeded SQLite
// database whose nested chain this repo wrote itself, so it cannot catch anything
// that depends on how WordPress actually populated term_taxonomy.
// TestRealWordPressNestedCategories (task 10.2) does use real rows, but only at
// resolver level: it never builds a router, a handler or a response, so a
// category whose path is constructed and classified correctly and still fails to
// reach chi -- the nested route is a wildcard, not a fixed-arity pattern -- passes
// it. This is the full-stack counterpart, and it is why Req 12.6 names both files.
//
// # It does not inherit this file's non-default-prefix skip
//
// TestM9PermalinksRealDBE2E declines to run against a wp_ target because Req 7.4
// makes the non-default table prefix part of what that fixture claims. Req 12.6
// makes no such claim, so refusing a wp_ database here would skip a check that
// would have run correctly.
//
// # Expectations are derived, not borrowed
//
// Every expected path is assembled below from the raw rows and the raw options:
// the ancestry is walked up term_taxonomy.parent inside this test, the base is
// normalized from the site's own category_base, the front is read off the raw
// permalink_structure, and the segments are joined with plain string operations.
// Structure.CategoryPath is never consulted. So the 200 on the derived canonical
// path is a genuine cross-check: if CategoryPath disagreed by even a trailing
// slash, the handler would 301 the derived path instead of rendering it and this
// test would fail.
//
// # Read-only
//
// storage.New opens the pool without migrating, nothing here writes, and every
// assertion is a GET. grimoire does not own this database.
func TestM9bNestedCategoriesRealDBE2E(t *testing.T) {
	dsn := os.Getenv(realWPEnvDSN)
	if dsn == "" {
		t.Skipf("set %s to run the real WordPress nested-category e2e validation", realWPEnvDSN)
	}
	prefix := os.Getenv(realWPEnvPrefix)
	if prefix == "" {
		prefix = realWPDefaultPrefix
	}

	ctx := t.Context()
	cfg := config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix}
	repos, err := storage.New(cfg) // opens the pool WITHOUT migrating
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	// The same read cmd/grimoire's resolvePermalinks performs, reproduced because
	// it lives in package main. Its error is non-fatal there -- the returned
	// Structure is used as-is, and that fallback still serves
	// /{CategoryBase}/{slug} -- so an unsupported structure is logged and the
	// category checks continue against exactly what the server would run with.
	options := content.NewOptionService(repos.Options)
	raw := options.Get(ctx, content.OptionPermalinkStructure)
	rawCategoryBase := options.Get(ctx, content.OptionCategoryBase)
	permalinks, parseErr := routing.Parse(raw, rawCategoryBase, options.Get(ctx, content.OptionTagBase))
	if parseErr != nil {
		t.Logf("structure %q is unsupported (%v); validating categories against "+
			"the flat fallback cmd/grimoire would serve", raw, parseErr)
	}
	t.Logf("target site: prefix=%q permalink_structure=%q category_base=%q",
		prefix, raw, rawCategoryBase)

	// "Usable" is the only fact about the structure the derivation needs. An empty
	// option (WordPress's plain permalinks) and an unsupported one both leave a
	// structure carrying neither a front nor a canonical trailing slash, so
	// today's /category/{slug} form is preserved byte for byte (Req 2.7).
	usable := parseErr == nil && strings.TrimSpace(raw) != ""
	front := realWPStructureFront(raw, usable)
	trailingSlash := usable && strings.HasSuffix(strings.TrimSpace(raw), "/")

	// The base, normalized from the raw option the way WordPress's admin UI could
	// have stored it ("/topics"), and whether it was provided at all -- which is
	// what decides whether the front applies to a category path (Req 4.8).
	base := strings.Join(realWPPathSegments(rawCategoryBase), "/")
	baseProvided := base != ""
	if !baseProvided {
		base = routing.DefaultCategoryBase
	}
	categoryFront := front
	if baseProvided {
		categoryFront = nil
	}
	// A base colliding with a leading literal of the structure keeps the post
	// meaning (Req 4.4b/9.7), so on such a site the classifier deliberately
	// declines the very paths CategoryPath advertises and every assertion below
	// would 404 for a documented reason. Saying so and skipping beats reporting a
	// failure that is really a configuration the milestone resolved on purpose.
	if len(front) > 0 && (base == front[0] || base == strings.Join(front, "/")) {
		t.Skipf("category base %q collides with the structure's front %q; the post "+
			"meaning wins for that segment (Req 4.4b), so this site serves no "+
			"category archive to assert on", base, strings.Join(front, "/"))
	}

	terms, err := repos.TermReader.ListByTaxonomy(ctx, "category")
	if err != nil {
		t.Fatalf("ListByTaxonomy(category): %v", err)
	}
	if len(terms) == 0 {
		t.Skip("no category terms in the target database")
	}
	byID := make(map[int64]domain.Term, len(terms))
	slugCount := make(map[string]int, len(terms))
	for _, term := range terms {
		byID[term.ID] = term
		slugCount[term.Slug]++
	}
	// Taken from the raw rows before a single request is made, so the Req 12.7
	// skip below can name the precondition that actually failed rather than the
	// one that is merely most likely.
	census := realWPNestedCensusOf(terms)
	t.Logf("category census: %d terms, %d with a resolvable parent, %d yielding a usable nested path",
		census.Terms, census.Nested, census.Usable)

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	// WithHierarchy supplies the taxonomy graph the category archive walks; a
	// harness missing it turns every assertion below into a 500 that looks like a
	// handler bug and is really a wiring gap. WithAuthors is the same story for
	// the author archive, and is kept so this harness matches the one above.
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		options,
		eng,
		nil,
	).WithPermalinks(permalinks)

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	// ts.Client() returns the same *http.Client every call, so a CheckRedirect
	// override on it would leak. Two separate clients: one that stops at the
	// redirect so its status and Location can be asserted, one that follows it so
	// a loop cannot hide behind a single hop.
	stop := &http.Client{Transport: ts.Client().Transport}
	stop.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	follow := &http.Client{Transport: ts.Client().Transport}

	// checked counts only the nested categories actually driven over HTTP, so it
	// is both the cap below and the evidence the Req 12.7 skip tests for.
	checked := 0
	for _, term := range terms {
		if checked == realWPCheckCategories {
			break
		}
		ancestry, nested, pathSafe := realWPNestedCandidate(byID, term)
		if !pathSafe {
			t.Logf("skipping category %d (%q): a slug in its ancestry is not a "+
				"plain URL token", term.ID, term.Slug)
			continue
		}
		// Only genuinely nested categories are the point here: a top-level
		// category's canonical path is its flat path, so it could not exercise
		// either half of the claim. Task 10.1 covers the top-level case on the
		// seeded stack. `checked` is incremented strictly after this filter, which
		// is what keeps it a count of nested archives actually driven over HTTP
		// rather than of categories examined — counted before it, an
		// all-top-level site would reach the cap above and make the Req 12.7 skip
		// unreachable.
		if !nested {
			continue
		}
		checked++

		canonical := realWPCategoryPath(categoryFront, base, ancestry, trailingSlash)
		flat := realWPCategoryPath(categoryFront, base, ancestry[len(ancestry)-1:], trailingSlash)

		t.Run(strings.Join(ancestry, "-"), func(t *testing.T) {
			// The canonical nested path renders. This carries the cross-check:
			// the path is derived from term_taxonomy.parent and the site's own
			// category_base, so a CategoryPath that built anything else would
			// redirect here instead of rendering (Req 2.1, 2.4, 4.1, 4.8).
			resp, body := realWPGet(t, stop, ts.URL+canonical)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (Location %q, body %.200s)",
					canonical, resp.StatusCode, resp.Header.Get("Location"), body)
			}
			// category.tmpl renders the term's name as the archive heading, so
			// this proves the category behind the URL is the one whose ancestry
			// built it. Real names carry ampersands and apostrophes that
			// html/template escapes, and asserting on that escaping would test
			// the wrong thing, so a non-plain name is logged instead.
			if realWPPlainName(term.Name) {
				if !strings.Contains(body, term.Name) {
					t.Errorf("GET %s body missing the category name %q", canonical, term.Name)
				}
			} else {
				t.Logf("category %d name %q is not plain text; not asserting on the "+
					"rendered heading", term.ID, term.Name)
			}

			// The flat path is what grimoire served for this category before
			// M9b -- the zero-ancestor instance of a failed segment walk -- so
			// it must redirect to the full ancestry rather than 404 or serve a
			// second copy (Req 2.3, 2.11).
			//
			// Recovery resolves the final segment against the whole taxonomy and
			// picks the lowest term_id when more than one matches, so a
			// duplicated slug could legitimately redirect to a different
			// category. The reference database has no duplicate category slug,
			// and where one exists the redirect target is not this term's
			// canonical path by specification, not by defect.
			if slugCount[term.Slug] > 1 {
				t.Logf("skipping the flat-path assertion: %d categories share the "+
					"slug %q, so Req 2.11 resolves it to the lowest term_id rather "+
					"than to this term", slugCount[term.Slug], term.Slug)
				return
			}
			resp, body = realWPGet(t, stop, ts.URL+flat)
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("GET %s = %d, want 301 (body %.200s)", flat, resp.StatusCode, body)
			}
			if got := resp.Header.Get("Location"); got != canonical {
				t.Fatalf("GET %s Location = %q, want %q", flat, got, canonical)
			}

			// Query strings survive the redirect (Req 2.9) -- losing them would
			// silently break every campaign and tracking link into the archive.
			const query = "?utm_source=grimoire-e2e"
			resp, _ = realWPGet(t, stop, ts.URL+flat+query)
			if got := resp.Header.Get("Location"); got != canonical+query {
				t.Errorf("GET %s Location = %q, want %q", flat+query, got, canonical+query)
			}

			// Following the redirect must land on a rendering page, not another
			// redirect: a loop here would take out every category URL the site
			// published.
			resp, body = realWPGet(t, follow, ts.URL+flat)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s following redirects = %d, want 200 (body %.200s)",
					flat, resp.StatusCode, body)
			}
			if got := resp.Request.URL.Path; got != canonical {
				t.Errorf("GET %s followed to %q, want %q", flat, got, canonical)
			}
		})
	}

	// Req 12.7: a check that reports success for a claim it never exercised is
	// worse than no check, so a run that drove no nested archive over HTTP skips
	// naming the precondition rather than passing.
	//
	// `checked` is what the skip is decided on, so it has to be worth deciding on.
	// The census counts nesting from term_taxonomy.parent while the loop counts
	// what it actually requested, and `checked` exceeding the census means the
	// loop counted categories it never drove — which would make the skip
	// unreachable on an all-top-level site, the exact failure Req 12.7 exists to
	// prevent. The cap above is why this is an inequality rather than an equality:
	// a site with more nested categories than realWPCheckCategories legitimately
	// drives fewer than the census found.
	if checked > census.Usable {
		t.Fatalf("drove %d nested category archives but the census found only %d "+
			"usable among %d terms — `checked` is counting categories it did not "+
			"exercise, so it cannot be evidence that the nested claim was tested",
			checked, census.Usable, census.Terms)
	}
	if checked == 0 {
		if census.Usable > 0 {
			t.Fatalf("no nested category archive was driven over HTTP although the "+
				"census found %d usable nested path(s) among %d category terms — the "+
				"loop above skipped work it should have done", census.Usable, census.Terms)
		}
		t.Skip(census.precondition())
	}
	t.Logf("validated %d nested category archives end-to-end against base %q and structure %q",
		checked, base, raw)
}

// realWPCategoryAncestry walks a term up term_taxonomy.parent and returns its
// slugs root-first, ending in the term's own slug.
//
// The walk is done here, over the rows, rather than through content.Archive, so
// the expectation does not inherit the implementation's idea of the hierarchy. It
// degrades the two ways real, imported databases require: a parent absent from
// the taxonomy is treated as no parent from that point up (Req 1.4), and an
// already-visited term ends the walk instead of hanging it (Req 1.5).
//
// It returns false when any slug in the chain cannot sit in a request path
// unescaped, which is the same question realWPPathSafeSlug answers for post
// slugs and the same reason: percent-encoding is not what this test is about.
func realWPCategoryAncestry(byID map[int64]domain.Term, term domain.Term) ([]string, bool) {
	var reversed []string
	visited := map[int64]bool{}
	for cur := term; ; {
		if visited[cur.ID] {
			break
		}
		visited[cur.ID] = true
		if !realWPPathSafeSlug(cur.Slug) {
			return nil, false
		}
		reversed = append(reversed, cur.Slug)
		parent, ok := byID[cur.ParentID]
		if cur.ParentID == 0 || !ok {
			break
		}
		cur = parent
	}
	slices.Reverse(reversed)
	return reversed, true
}

// realWPNestedCandidate classifies one term for the nested-category assertions.
//
// It returns the walked ancestry, whether that ancestry is genuinely nested —
// more than one segment, so the path carries an ancestor and not just the term's
// own slug — and whether every slug in it can sit in a request path unescaped.
//
// This is one function rather than two expressions in the loop because the same
// decision has to be made in three places that must not drift: the loop that
// drives the requests, the census that decides whether Req 12.7's skip fires,
// and the database-free tests in m9b_realdb_census_test.go that are the only
// demonstration anyone can run of the skip firing when it should.
func realWPNestedCandidate(byID map[int64]domain.Term, term domain.Term) (ancestry []string, nested, pathSafe bool) {
	ancestry, pathSafe = realWPCategoryAncestry(byID, term)
	return ancestry, pathSafe && len(ancestry) > 1, pathSafe
}

// realWPNestedCensus records what a category taxonomy offers the
// nested-category assertions, counted straight off the rows.
//
// It exists because Req 12.7's skip has to name the precondition an operator can
// act on, and "this site has no nested category" and "this site's nested
// categories carry slugs that cannot sit in a request path" are different facts
// that need different fixes. A single message asserting the first would send
// whoever reads it to the wrong place on a site where the second is true.
type realWPNestedCensus struct {
	// Terms is every term read from the category taxonomy.
	Terms int
	// Nested is the terms whose parent <> 0 and whose parent is present in the
	// set — the literal precondition Req 12.7 names. A parent absent from the
	// taxonomy is treated as no parent (Req 1.4), so it does not count.
	Nested int
	// Usable is the subset of Nested that yields an ancestry this test can
	// actually drive over HTTP: at least two segments, every one of them a plain
	// URL token. A percent-encoded slug anywhere in the chain, or a parent chain
	// that closes a cycle (Req 1.5), leaves a term counted in Nested and not
	// here.
	Usable int
}

// realWPNestedCensusOf counts the taxonomy without asserting on it or issuing a
// request.
func realWPNestedCensusOf(terms []domain.Term) realWPNestedCensus {
	byID := make(map[int64]domain.Term, len(terms))
	for _, term := range terms {
		byID[term.ID] = term
	}
	census := realWPNestedCensus{Terms: len(terms)}
	for _, term := range terms {
		if _, ok := byID[term.ParentID]; term.ParentID == 0 || !ok {
			continue
		}
		census.Nested++
		if _, nested, _ := realWPNestedCandidate(byID, term); nested {
			census.Usable++
		}
	}
	return census
}

// precondition states, in terms an operator can act on, why the taxonomy drove
// no nested-category assertion.
func (c realWPNestedCensus) precondition() string {
	if c.Nested == 0 {
		return fmt.Sprintf("no category in the target database has a resolvable "+
			"parent (%d category terms, all top-level); these assertions need at "+
			"least one term_taxonomy row with parent <> 0 pointing at an existing "+
			"category", c.Terms)
	}
	return fmt.Sprintf("%d of %d category terms have parent <> 0 pointing at an "+
		"existing category, but none yields a usable nested path: every one was "+
		"rejected because a slug in its ancestry is not a plain URL token "+
		"([A-Za-z0-9_-]) or its parent chain closes a cycle; these assertions need "+
		"one nested category whose whole ancestry is a plain URL token",
		c.Nested, c.Terms)
}

// realWPCategoryPath assembles an expected archive path by plain string joining:
// the front where it applies, then the normalized base segments, then the given
// segments, then the trailing slash the structure implies.
//
// This is the independent half of the cross-check, so it must not call
// Structure.CategoryPath or any other exported constructor. Passing the full
// ancestry yields the canonical path; passing only the term's own slug yields the
// flat path a pre-M9b site published.
func realWPCategoryPath(front []string, base string, segments []string, trailingSlash bool) string {
	segs := make([]string, 0, len(front)+len(segments)+2)
	segs = append(segs, front...)
	segs = append(segs, realWPPathSegments(base)...)
	segs = append(segs, segments...)
	path := "/" + strings.Join(segs, "/")
	if trailingSlash {
		path += "/"
	}
	return path
}

// realWPStructureFront reads the structure's front off the raw option: the
// maximal run of leading literal segments. It is empty for a structure that
// yields a flat Structure, because such a site serves no archive under a front at
// all.
func realWPStructureFront(raw string, usable bool) []string {
	if !usable {
		return nil
	}
	var out []string
	for _, seg := range realWPPathSegments(raw) {
		if strings.HasPrefix(seg, "%") && strings.HasSuffix(seg, "%") {
			break
		}
		out = append(out, seg)
	}
	return out
}

// realWPPathSegments reduces a raw option value to its bare segments:
// surrounding whitespace trimmed, leading and trailing slashes dropped, repeated
// internal slashes collapsed (Req 4.11). WordPress's options-permalink.php stores
// a submitted base with a leading slash, so "/topics" is a realistic stored value
// rather than a hypothetical one.
func realWPPathSegments(v string) []string {
	parts := strings.Split(strings.TrimSpace(v), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

// realWPPlainName reports whether a term name can be compared against rendered
// HTML without reasoning about escaping. Real category names carry ampersands,
// apostrophes and angle brackets that html/template rewrites, and an assertion
// about that rewriting would be testing the template engine.
func realWPPlainName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == ' ', r == '-', r == '_', r == '.', r == ',':
		default:
			return false
		}
	}
	return true
}
