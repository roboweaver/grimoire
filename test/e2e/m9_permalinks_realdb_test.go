package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter),
		content.NewTermService(repos.Terms, repos.Posts),
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
