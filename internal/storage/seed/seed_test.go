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
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	return repos
}

func TestRunSeedsContent(t *testing.T) {
	ctx := context.Background()
	repos := newMigratedRepos(t)

	if err := seed.Run(ctx, repos.DB(), "wp_"); err != nil {
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

	catPosts, err := repos.Posts.ByTermSlug(ctx, "category", "news", 10, 0)
	if err != nil {
		t.Fatalf("ByTermSlug: %v", err)
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

	if err := seed.Run(ctx, repos.DB(), "wp_"); err != nil {
		t.Fatalf("first seed.Run: %v", err)
	}
	if err := seed.Run(ctx, repos.DB(), "wp_"); err != nil {
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
