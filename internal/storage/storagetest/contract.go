// Package storagetest provides a vendor-parameterized contract suite that every
// storage adapter must satisfy, plus deterministic fixture seeding via plain
// SQL. SQLite runs always; MySQL/Postgres runners are env-gated.
package storagetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// NewReposFunc builds a migrated + seeded Repositories bundle and a cleanup.
type NewReposFunc func(t *testing.T) (*storage.Repositories, func())

// SeedFixtures inserts deterministic content via plain SQL:
//   - 3 published posts (newest first: hello-3, hello-2, hello-1)
//   - 1 draft post (excluded from reads)
//   - 1 published page (slug "about")
//   - category term "news" related to hello-3 and hello-2
//   - options blogname + blogdescription
func SeedFixtures(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO ` + prefix + `users (ID, user_login, user_nicename, display_name) VALUES (?, ?, ?, ?)`,
			[]any{1, "admin", "admin", "Admin"}},
		{postInsert(prefix), postArgs(1, "hello-1", "Hello One", "post", "publish", "2024-01-01 00:00:00")},
		{postInsert(prefix), postArgs(2, "hello-2", "Hello Two", "post", "publish", "2024-01-02 00:00:00")},
		{postInsert(prefix), postArgs(3, "hello-3", "Hello Three", "post", "publish", "2024-01-03 00:00:00")},
		{postInsert(prefix), postArgs(4, "secret", "Secret Draft", "post", "draft", "2024-01-04 00:00:00")},
		{postInsert(prefix), postArgs(5, "about", "About", "page", "publish", "2024-01-05 00:00:00")},
		{`INSERT INTO ` + prefix + `terms (term_id, name, slug) VALUES (?, ?, ?)`,
			[]any{10, "News", "news"}},
		{`INSERT INTO ` + prefix + `term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{20, 10, "category", "", 0, 2}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{3, 20, 0}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{2, 20, 0}},
		{`INSERT INTO ` + prefix + `options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			[]any{"blogname", "grimoire test", "yes"}},
		{`INSERT INTO ` + prefix + `options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			[]any{"blogdescription", "tagline", "yes"}},
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, s.q), s.args...); err != nil {
			return fmt.Errorf("seed %q: %w", s.q, err)
		}
	}
	return nil
}

func postInsert(prefix string) string {
	return `INSERT INTO ` + prefix + `posts ` +
		`(ID, post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_type) ` +
		`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func postArgs(id int64, slug, title, ptype, status, date string) []any {
	return []any{id, 1, date, "<p>body</p>", title, "excerpt", status, slug, ptype}
}

// RunContract executes the full read contract against a freshly built backend.
func RunContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("RecentPosts newest-first excludes draft", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		posts, err := repos.Posts.RecentPosts(ctx, 10, 0)
		if err != nil {
			t.Fatalf("RecentPosts: %v", err)
		}
		if len(posts) != 3 {
			t.Fatalf("want 3 posts, got %d", len(posts))
		}
		wantSlugs := []string{"hello-3", "hello-2", "hello-1"}
		for i, w := range wantSlugs {
			if posts[i].Slug != w {
				t.Errorf("post[%d] slug = %q, want %q", i, posts[i].Slug, w)
			}
		}
	})

	t.Run("RecentPosts pagination", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		page1, err := repos.Posts.RecentPosts(ctx, 2, 0)
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		page2, err := repos.Posts.RecentPosts(ctx, 2, 2)
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(page1) != 2 || len(page2) != 1 {
			t.Fatalf("pagination sizes: page1=%d page2=%d", len(page1), len(page2))
		}
		if page1[0].Slug != "hello-3" || page2[0].Slug != "hello-1" {
			t.Errorf("pagination order wrong: %q / %q", page1[0].Slug, page2[0].Slug)
		}
	})

	t.Run("BySlug post, page, and not-found", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		p, err := repos.Posts.BySlug(ctx, "hello-2")
		if err != nil {
			t.Fatalf("BySlug post: %v", err)
		}
		if p.Title != "Hello Two" || p.Type != "post" {
			t.Errorf("unexpected post: %+v", p)
		}
		page, err := repos.Posts.BySlug(ctx, "about")
		if err != nil {
			t.Fatalf("BySlug page: %v", err)
		}
		if page.Type != "page" {
			t.Errorf("about type = %q, want page", page.Type)
		}
		if _, err := repos.Posts.BySlug(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("unknown slug err = %v, want ErrNotFound", err)
		}
		if _, err := repos.Posts.BySlug(ctx, "secret"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("draft slug err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ByTermSlug related published posts newest-first", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		posts, err := repos.Posts.ByTermSlug(ctx, "category", "news", 10, 0)
		if err != nil {
			t.Fatalf("ByTermSlug: %v", err)
		}
		if len(posts) != 2 {
			t.Fatalf("want 2 related posts, got %d", len(posts))
		}
		if posts[0].Slug != "hello-3" || posts[1].Slug != "hello-2" {
			t.Errorf("related order wrong: %q / %q", posts[0].Slug, posts[1].Slug)
		}
	})

	t.Run("TermRepository BySlug and not-found", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		term, err := repos.Terms.BySlug(ctx, "category", "news")
		if err != nil {
			t.Fatalf("Terms.BySlug: %v", err)
		}
		if term.Name != "News" || term.Taxonomy != "category" {
			t.Errorf("unexpected term: %+v", term)
		}
		if _, err := repos.Terms.BySlug(ctx, "category", "nope"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("unknown term err = %v, want ErrNotFound", err)
		}
	})

	t.Run("OptionRepository Get and not-found", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		v, err := repos.Options.Get(ctx, "blogname")
		if err != nil {
			t.Fatalf("Options.Get: %v", err)
		}
		if v != "grimoire test" {
			t.Errorf("blogname = %q", v)
		}
		if _, err := repos.Options.Get(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("missing option err = %v, want ErrNotFound", err)
		}
	})
}
