package routing_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
)

// TestRealWordPressPermalinks validates the permalink resolver against a
// restored, REAL WordPress database (read-only), mirroring the gating of
// internal/storage/storagetest's TestRealWordPressDB so CI without the database
// still passes.
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress?parseTime=true' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/routing/ -run TestRealWordPressPermalinks -v -count=1
//
// Why this exists as a committed test rather than a one-off check: the whole
// point of M9a is that a real site's existing URLs keep resolving. Synthetic
// fixtures prove the grammar is implemented, but only real rows prove it is
// implemented the way WordPress actually wrote them — the zero-padding, the
// stored-date timezone, and the exact trailing-slash form all come from data,
// not from the spec.
//
// The database is strictly READ-ONLY here: the test never migrates and never
// writes. It reads the site's own permalink_structure option rather than
// assuming one, so it validates against whatever the target site is configured
// for.
func TestRealWordPressPermalinks(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress permalink validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if prefix == "" {
		prefix = "accuweaver"
	}

	ctx := context.Background()
	repos, err := storage.New(config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix})
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer repos.Close()

	// Read the site's real configuration instead of assuming a structure.
	structure, err := repos.Options.Get(ctx, "permalink_structure")
	if err != nil {
		t.Fatalf("read permalink_structure: %v", err)
	}
	categoryBase, _ := repos.Options.Get(ctx, "category_base")
	tagBase, _ := repos.Options.Get(ctx, "tag_base")
	t.Logf("site permalink_structure = %q", structure)

	s, err := routing.Parse(structure, categoryBase, tagBase)
	if err != nil {
		t.Fatalf("Parse(%q): %v — this site's structure is not yet supported", structure, err)
	}
	if s.Flat {
		t.Skipf("site uses plain permalinks (%q); nothing to validate", structure)
	}

	posts, err := repos.Posts.RecentPosts(ctx, 25, 0)
	if err != nil {
		t.Fatalf("RecentPosts: %v", err)
	}
	if len(posts) == 0 {
		t.Skip("no published posts in the target database")
	}

	for _, p := range posts {
		t.Run(p.Slug, func(t *testing.T) {
			got := s.Canonical(p)

			// Derive the expectation independently of Canonical, so this is a
			// real cross-check rather than the implementation agreeing with
			// itself. Only the dated presets are derived here; anything else
			// falls through to the round-trip assertions below.
			switch structure {
			case "/%year%/%monthnum%/%day%/%postname%/":
				want := fmt.Sprintf("/%04d/%02d/%02d/%s/",
					p.Date.Year(), int(p.Date.Month()), p.Date.Day(), p.Slug)
				if got != want {
					t.Fatalf("Canonical = %q, want %q", got, want)
				}
			case "/%year%/%monthnum%/%postname%/":
				want := fmt.Sprintf("/%04d/%02d/%s/",
					p.Date.Year(), int(p.Date.Month()), p.Slug)
				if got != want {
					t.Fatalf("Canonical = %q, want %q", got, want)
				}
			}

			// The canonical path must match the structure it came from, and
			// resolve back to this same post. If it did not, the handler would
			// redirect a canonical URL again — a redirect loop on every post.
			params, ok := s.ParamsFromPath(got)
			if !ok {
				t.Fatalf("ParamsFromPath rejected the path Canonical produced: %q", got)
			}
			ref, ok := s.Match(params)
			if !ok {
				t.Fatalf("Match rejected the path Canonical produced: %q (params %v)", got, params)
			}
			if ref.Slug != "" && ref.Slug != p.Slug {
				t.Errorf("round-tripped slug = %q, want %q", ref.Slug, p.Slug)
			}
			if ref.ID != 0 && ref.ID != p.ID {
				t.Errorf("round-tripped id = %d, want %d", ref.ID, p.ID)
			}
			// Date components must agree with the stored date, which is the
			// check that catches a timezone shift on real data.
			if ref.HasDate() {
				if ref.Year != p.Date.Year() {
					t.Errorf("round-tripped year = %d, want %d", ref.Year, p.Date.Year())
				}
				if ref.Month != 0 && ref.Month != int(p.Date.Month()) {
					t.Errorf("round-tripped month = %d, want %d", ref.Month, int(p.Date.Month()))
				}
				if ref.Day != 0 && ref.Day != p.Date.Day() {
					t.Errorf("round-tripped day = %d, want %d", ref.Day, p.Date.Day())
				}
			}
		})
	}
	t.Logf("validated %d real permalinks against structure %q", len(posts), structure)
}
