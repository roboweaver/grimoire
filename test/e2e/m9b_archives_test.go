package e2e_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// The configuration this test boots the stack with, and every path it asserts.
//
// The structure carries a front ("blog") and the category base is overridden, so
// the two halves of WordPress's with_front asymmetry (Req 4.8) are both live at
// once: category paths drop the front because category_base was provided, while
// the tag, date and author paths keep it because tag_base was not and the author
// and date structures concatenate the front unconditionally. That is the
// combination an implementation prefixing the front once, for every kind, gets
// wrong -- and it is invisible under the default bases the handler tests use.
//
// Every path below is written out in full rather than built from
// Structure.CategoryPath and friends. The constructors are what this test is
// checking the router and the handlers agree with, so deriving the expectations
// from them would let the whole test pass against a structure that advertises
// URLs the router never registered.
const (
	e2eArchiveStructure = "/blog/%year%/%monthnum%/%day%/%postname%/"
	e2eArchiveCatBase   = "sections"

	// The nested chain SeedNestedCategories creates: Tech (top-level) -> Go ->
	// Generics, one published post each.
	e2eCatTechCanonical     = "/sections/tech/"
	e2eCatGoCanonical       = "/sections/tech/go/"
	e2eCatGenericsCanonical = "/sections/tech/go/generics/"

	// The flat forms a pre-M9b site published, which are now redirects. "go" is
	// the zero-ancestor instance of a failed walk; "generics" is the same failure
	// two levels deep.
	e2eCatGoFlat       = "/sections/go/"
	e2eCatGenericsFlat = "/sections/generics/"

	// tag_base is unset, so the tag archive carries the front; the date and
	// author archives carry it regardless.
	e2eTagCanonical    = "/blog/tag/golang/"
	e2eDateCanonical   = "/blog/2024/05/"
	e2eAuthorCanonical = "/blog/author/admin/"

	// Post titles, as the post-card partial renders them. The parent's archive
	// has to carry the child's and grandchild's, which is the
	// descendant-inclusive claim (Req 3.1).
	e2eTechPostTitle     = "Tech Post"
	e2eGoPostTitle       = "Go Post"
	e2eGenericsPostTitle = "Generics Post"
	// hello-1 is SeedFixtures' only post carrying the "golang" tag.
	e2eTaggedPostTitle = "Hello One"
)

// TestM9bArchivesE2E boots the real stack on SQLite -- migrate, seed, storage
// repositories, the render engine and the actual chi router behind an HTTP
// server -- against a nested category chain and a non-default category_base,
// then asserts over real HTTP that the nested category path renders, the flat
// path it replaced redirects to it, a parent's archive lists its descendants'
// posts, and the tag, date and author archives each render.
//
// This catches what the internal/web handler tests cannot. Those build a server
// in-process from a structure handed straight to WithPermalinks, so they cannot
// detect a category_base that never survives the option read, an archive pattern
// the router fails to register for an overridden base, or a redirect a real
// net/http client would not act on. Here the configuration travels the
// production route -- two {prefix}options rows, read through
// content.OptionService, parsed by routing.Parse, handed to the server --
// exactly as cmd/grimoire's resolvePermalinks does it.
func TestM9bArchivesE2E(t *testing.T) {
	ctx := t.Context()
	// testDSN rather than a bare temp path, so this test inherits the
	// busy_timeout every e2e database carries.
	dsn := testDSN(t)
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}

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
	// SeedFixtures rather than seed.Run, because SeedNestedCategories layers on
	// it: the chain's posts are authored by user 1, and the "golang" tag this
	// test's tag archive reads comes from that fixture set.
	if err := storagetest.SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}
	if err := storagetest.SeedNestedCategories(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedNestedCategories: %v", err)
	}

	// Both options are written into the database rather than handed to the
	// server directly, so the option reads are part of what this test covers. A
	// category_base that never reaches routing.Parse would leave every path
	// below under /category/, which the guards after the parse turn into a
	// failure naming the cause instead of a pile of 404s.
	for _, opt := range []struct{ name, value string }{
		{content.OptionPermalinkStructure, e2eArchiveStructure},
		{content.OptionCategoryBase, e2eArchiveCatBase},
	} {
		if _, err := repos.DB().ExecContext(ctx,
			`INSERT INTO `+cfg.TablePrefix+`options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			opt.name, opt.value, "yes",
		); err != nil {
			t.Fatalf("write %s option: %v", opt.name, err)
		}
	}

	options := content.NewOptionService(repos.Options)

	// Mirrors cmd/grimoire's resolvePermalinks: the three options are read once,
	// from one OptionService, and parsed together. resolvePermalinks itself
	// lives in package main and cannot be imported, so the read and parse are
	// reproduced rather than called. tag_base is read and left unset, which is
	// what keeps the front on the tag archive.
	permalinks, err := routing.Parse(
		options.Get(ctx, content.OptionPermalinkStructure),
		options.Get(ctx, content.OptionCategoryBase),
		options.Get(ctx, content.OptionTagBase),
	)
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", e2eArchiveStructure, err)
	}
	if permalinks.Flat {
		t.Fatalf("permalink structure resolved as flat; the %soptions row was not "+
			"read back, so the rest of this test would assert nothing", cfg.TablePrefix)
	}
	if permalinks.CategoryBase != e2eArchiveCatBase || !permalinks.CategoryBaseSet {
		t.Fatalf("category base resolved as %q (set=%t), want %q (set=true); the "+
			"non-default base is the point of this test",
			permalinks.CategoryBase, permalinks.CategoryBaseSet, e2eArchiveCatBase)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	// WithHierarchy supplies the taxonomy graph the category archive walks and
	// WithAuthors the nicename lookup the author archive needs. Without either
	// the matching handler nil-panics into a 500, so a harness missing them
	// reports a handler bug that is really a wiring gap.
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		options,
		eng,
		nil,
	).WithPermalinks(permalinks)

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	client := ts.Client()
	// Without this the client follows the 301 and the redirect assertions would
	// only ever see the final 200, which is the one thing they must not do.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	cases := []struct {
		name         string
		path         string
		wantStatus   int
		wantLocation string
		// wantBody are substrings the response must contain; wantNotBody are
		// substrings it must not. Both are asserted on the rendered body rather
		// than on a reconstructed path, so "the parent's archive includes the
		// child's post" is checked against what a visitor would actually see.
		wantBody    []string
		wantNotBody []string
	}{
		{
			// Req 2.1, 4.1, 4.8: the nested path under the overridden base, with
			// the front dropped because the base was provided.
			name:       "nested category renders at its canonical path",
			path:       e2eCatGoCanonical,
			wantStatus: http.StatusOK,
			wantBody:   []string{"Go", e2eGoPostTitle, e2eGenericsPostTitle},
			// The child's archive must not reach up to the parent's post.
			wantNotBody: []string{e2eTechPostTitle},
		},
		{
			// Three levels, because a walk that handles one hop and stops would
			// pass the two-level case above.
			name:       "grandchild category renders at its canonical path",
			path:       e2eCatGenericsCanonical,
			wantStatus: http.StatusOK,
			wantBody:   []string{"Generics", e2eGenericsPostTitle},
		},
		{
			// Req 3.1: the descendant-inclusive listing. The parent's archive
			// carries the child's and the grandchild's posts, not only its own.
			name:       "a parent's archive lists its descendants' posts",
			path:       e2eCatTechCanonical,
			wantStatus: http.StatusOK,
			wantBody: []string{
				"Tech",
				e2eTechPostTitle,
				e2eGoPostTitle,
				e2eGenericsPostTitle,
			},
		},
		{
			// Req 2.3: the flat path is the zero-ancestor instance of a failed
			// walk, and it is the URL the site published before this milestone.
			name:         "flat category path redirects to the nested canonical path",
			path:         e2eCatGoFlat,
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: e2eCatGoCanonical,
		},
		{
			name:         "flat path of a grandchild redirects to its full ancestry",
			path:         e2eCatGenericsFlat,
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: e2eCatGenericsCanonical,
		},
		{
			// Req 4.8's other half: tag_base is unset, so the tag archive keeps
			// the front the category archive dropped.
			name:       "tag archive renders under the front",
			path:       e2eTagCanonical,
			wantStatus: http.StatusOK,
			wantBody:   []string{"Golang", e2eTaggedPostTitle},
		},
		{
			// Req 4.7, 6.1: the date archive carries the front unconditionally.
			// May 2024 is the month the nested chain's three posts sit in.
			name:       "date archive renders under the front",
			path:       e2eDateCanonical,
			wantStatus: http.StatusOK,
			wantBody: []string{
				"May 2024",
				e2eTechPostTitle,
				e2eGoPostTitle,
				e2eGenericsPostTitle,
			},
		},
		{
			// Req 7.1, 7.4: the author archive carries the front too, and its
			// heading is the display_name rather than the login.
			name:       "author archive renders under the front",
			path:       e2eAuthorCanonical,
			wantStatus: http.StatusOK,
			wantBody:   []string{"Admin", e2eTechPostTitle},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d (body %.300s)",
					tc.path, resp.StatusCode, tc.wantStatus, body)
			}
			// An empty wantLocation asserts the header's absence, so a 200 that
			// also redirects cannot pass.
			if got := resp.Header.Get("Location"); got != tc.wantLocation {
				t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.wantLocation)
			}
			for _, want := range tc.wantBody {
				if !strings.Contains(string(body), want) {
					t.Errorf("GET %s body missing %q", tc.path, want)
				}
			}
			for _, unwanted := range tc.wantNotBody {
				if strings.Contains(string(body), unwanted) {
					t.Errorf("GET %s body unexpectedly contains %q", tc.path, unwanted)
				}
			}
		})
	}

	// Following the flat category redirect must land on a rendering page rather
	// than another redirect: a loop here would take out every category URL the
	// site published. The default client follows redirects, so this also proves
	// the Location header is one a real browser can act on.
	//
	// A fresh client, because ts.Client() hands back the same *http.Client every
	// call -- including the CheckRedirect override installed above.
	follow := &http.Client{Transport: ts.Client().Transport}
	resp, err := follow.Get(ts.URL + e2eCatGoFlat)
	if err != nil {
		t.Fatalf("GET %s following redirects: %v", e2eCatGoFlat, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s following redirects = %d, want 200 (body %.300s)",
			e2eCatGoFlat, resp.StatusCode, body)
	}
	if got := resp.Request.URL.Path; got != e2eCatGoCanonical {
		t.Errorf("GET %s followed to %q, want %q", e2eCatGoFlat, got, e2eCatGoCanonical)
	}
}
