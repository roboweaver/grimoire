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
	"github.com/roboweaver/grimoire/internal/storage/seed"
	"github.com/roboweaver/grimoire/internal/web"
)

// WordPress's "Day and name" preset, as stored in the permalink_structure
// option, and the canonical path it produces for the seeded "hello-world" post
// (seed.Run inserts it as a published post dated 2024-01-01 09:00:00).
const (
	e2eDayAndName      = "/%year%/%monthnum%/%day%/%postname%/"
	e2eHelloCanonical  = "/2024/01/01/hello-world/"
	e2eHelloFlat       = "/hello-world"
	e2eHelloBodyMarker = "Welcome to grimoire"
)

// TestM9PermalinksE2E boots the real stack on SQLite -- migrate, seed, storage
// repositories, the render engine and the actual chi router behind an HTTP
// server -- with a dated permalink_structure written into the database, and
// asserts over real HTTP that the canonical URL renders, the flat URL it
// replaced redirects to it, and an unrelated path still 404s (M9a Req 7.2).
//
// This differs from the Phase 3 handler tests in what it can catch: those drive
// a server built in-process from handler-level fixtures, so they cannot detect a
// structure that never reaches the router, an option row the startup path fails
// to read back, or a redirect that a real net/http client would not follow the
// way the recorder suggests. Here the structure travels the production route --
// an {prefix}options row, read through content.OptionService, parsed by
// routing.Parse, handed to the server -- exactly as cmd/grimoire's
// resolvePermalinks does it.
func TestM9PermalinksE2E(t *testing.T) {
	ctx := t.Context()
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
	if err := seed.Run(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("seed.Run: %v", err)
	}

	// The structure is written into the database rather than handed to the
	// server directly, so the option read is part of what this test covers. A
	// mis-spelled option name or a broken OptionService read would leave the
	// site flat here, which the Flat check below turns into a failure instead
	// of a confusing pile of 404s.
	if _, err := repos.DB().ExecContext(ctx,
		`INSERT INTO `+cfg.TablePrefix+`options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
		content.OptionPermalinkStructure, e2eDayAndName, "yes",
	); err != nil {
		t.Fatalf("write permalink_structure option: %v", err)
	}

	options := content.NewOptionService(repos.Options)

	// Mirrors cmd/grimoire's resolvePermalinks: the three options are read
	// once, from one OptionService, and parsed together. resolvePermalinks
	// itself lives in package main and cannot be imported, so the read and
	// parse are reproduced rather than called.
	permalinks, err := routing.Parse(
		options.Get(ctx, content.OptionPermalinkStructure),
		options.Get(ctx, content.OptionCategoryBase),
		options.Get(ctx, content.OptionTagBase),
	)
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", e2eDayAndName, err)
	}
	if permalinks.Flat {
		t.Fatalf("permalink structure resolved as flat; the %soptions row was not "+
			"read back, so the rest of this test would assert nothing", cfg.TablePrefix)
	}

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

	client := ts.Client()
	// Without this the client follows the 301 and the redirect assertion would
	// only ever see the final 200, which is the one thing it must not do.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	cases := []struct {
		name         string
		path         string
		wantStatus   int
		wantLocation string
		wantBody     string
	}{
		{
			name:       "canonical URL renders",
			path:       e2eHelloCanonical,
			wantStatus: http.StatusOK,
			wantBody:   e2eHelloBodyMarker,
		},
		{
			name:         "flat URL redirects to the canonical URL",
			path:         e2eHelloFlat,
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: e2eHelloCanonical,
		},
		{
			name:       "unrelated path 404s",
			path:       "/not-a-real-path",
			wantStatus: http.StatusNotFound,
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
				t.Fatalf("GET %s = %d, want %d (body %.200s)",
					tc.path, resp.StatusCode, tc.wantStatus, body)
			}
			if got := resp.Header.Get("Location"); got != tc.wantLocation {
				t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.wantLocation)
			}
			if tc.wantBody != "" && !strings.Contains(string(body), tc.wantBody) {
				t.Errorf("GET %s body missing %q", tc.path, tc.wantBody)
			}
		})
	}

	// Following the redirect must land on a rendering page rather than another
	// redirect: a loop here would take out every published URL on the site
	// (Req 3.5). The default client follows redirects, so this also proves the
	// Location header is one a real browser can act on.
	// A fresh client, because ts.Client() hands back the same *http.Client
	// every call -- including the CheckRedirect override installed above.
	follow := &http.Client{Transport: ts.Client().Transport}
	resp, err := follow.Get(ts.URL + e2eHelloFlat)
	if err != nil {
		t.Fatalf("GET %s following redirects: %v", e2eHelloFlat, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s following redirects = %d, want 200 (body %.200s)",
			e2eHelloFlat, resp.StatusCode, body)
	}
	if got := resp.Request.URL.Path; got != e2eHelloCanonical {
		t.Errorf("GET %s followed to %q, want %q", e2eHelloFlat, got, e2eHelloCanonical)
	}
}
