package seed_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/seed"
)

func newMigratedRepos(t *testing.T) *storage.Repositories {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
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
	return repos
}

func TestRunSeedsContent(t *testing.T) {
	ctx := context.Background()
	repos := newMigratedRepos(t)

	if err := seed.Run(ctx, repos.DB(), "sqlite", "wp_"); err != nil {
		t.Fatalf("seed.Run: %v", err)
	}

	posts, err := repos.Posts.RecentPosts(ctx, 10, 0)
	if err != nil {
		t.Fatalf("RecentPosts: %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("want 3 recent posts, got %d", len(posts))
	}
	if posts[0].Slug != "third-post" {
		t.Errorf("want newest post third-post first, got %q", posts[0].Slug)
	}

	page, err := repos.Posts.BySlug(ctx, "about", "page")
	if err != nil {
		t.Fatalf("BySlug about: %v", err)
	}
	if page.Type != "page" {
		t.Errorf("want about type page, got %q", page.Type)
	}

	// The seeded term_relationships rows are what this asserts: two of the three
	// seeded posts are filed under the "news" category. Read back through
	// PublishedArchive, the surviving non-recursive term read -- the seed inserts
	// no child categories, so a single-term filter with no descendant expansion
	// describes exactly the same set the removed ByTermSlug did.
	newsTerm, err := repos.Terms.BySlug(ctx, "category", "news")
	if err != nil {
		t.Fatalf("Terms.BySlug news: %v", err)
	}
	catPosts, err := repos.Posts.PublishedArchive(ctx, domain.ArchiveFilter{
		Taxonomy: "category",
		TermIDs:  []int64{newsTerm.ID},
	}, 10, 0)
	if err != nil {
		t.Fatalf("PublishedArchive news: %v", err)
	}
	if len(catPosts) != 2 {
		t.Errorf("want 2 posts in news category, got %d", len(catPosts))
	}

	name, err := repos.Options.Get(ctx, "blogname")
	if err != nil {
		t.Fatalf("Options.Get blogname: %v", err)
	}
	if name != "grimoire" {
		t.Errorf("want blogname grimoire, got %q", name)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repos := newMigratedRepos(t)

	if err := seed.Run(ctx, repos.DB(), "sqlite", "wp_"); err != nil {
		t.Fatalf("first seed.Run: %v", err)
	}
	if err := seed.Run(ctx, repos.DB(), "sqlite", "wp_"); err != nil {
		t.Fatalf("second seed.Run: %v", err)
	}

	posts, err := repos.Posts.RecentPosts(ctx, 100, 0)
	if err != nil {
		t.Fatalf("RecentPosts: %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("want 3 posts after double seed, got %d", len(posts))
	}

	if _, err := repos.Options.Get(ctx, "missing-key"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("want ErrNotFound for missing option, got %v", err)
	}
}
