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
	"time"

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
		{postInsert(prefix), postArgs(1, "hello-1", "Hello One", "post", "publish", "2024-01-01 00:00:00", "open", 0, "", 0)},
		{postInsert(prefix), postArgs(2, "hello-2", "Hello Two", "post", "publish", "2024-01-02 00:00:00", "open", 0, "", 0)},
		{postInsert(prefix), postArgs(3, "hello-3", "Hello Three", "post", "publish", "2024-01-03 00:00:00", "open", 0, "", 0)},
		{postInsert(prefix), postArgs(4, "secret", "Secret Draft", "post", "draft", "2024-01-04 00:00:00", "closed", 0, "", 0)},
		{postInsert(prefix), postArgs(5, "about", "About", "page", "publish", "2024-01-05 00:00:00", "open", 0, "", 0)},
		{postInsert(prefix), postArgs(201, "photo", "Photo", "attachment", "inherit", "2024-01-06 00:00:00", "closed", 1, "image/jpeg", 0)},
		{postInsert(prefix), postArgs(202, "asset", "Asset", "attachment", "inherit", "2024-01-07 00:00:00", "closed", 0, "image/png", 0)},
		{postInsert(prefix), postArgs(301, "menu-home", "Home", "nav_menu_item", "publish", "2024-01-08 00:00:00", "closed", 0, "", 1)},
		{postInsert(prefix), postArgs(302, "", "", "nav_menu_item", "publish", "2024-01-08 00:01:00", "closed", 0, "", 2)},
		{postInsert(prefix), postArgs(303, "old-news", "", "nav_menu_item", "publish", "2024-01-08 00:02:00", "closed", 0, "", 3)},
		{postInsert(prefix), postArgs(304, "sub-home", "Sub Home", "nav_menu_item", "publish", "2024-01-08 00:03:00", "closed", 0, "", 4)},
		{`INSERT INTO ` + prefix + `terms (term_id, name, slug) VALUES (?, ?, ?)`,
			[]any{10, "News", "news"}},
		{`INSERT INTO ` + prefix + `terms (term_id, name, slug) VALUES (?, ?, ?)`,
			[]any{30, "Primary", "primary"}},
		{`INSERT INTO ` + prefix + `term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{20, 10, "category", "", 0, 2}},
		{`INSERT INTO ` + prefix + `term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{40, 30, "nav_menu", "", 0, 4}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{3, 20, 0}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{2, 20, 0}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{301, 40, 0}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{302, 40, 0}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{303, 40, 0}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{304, 40, 0}},
		{`INSERT INTO ` + prefix + `options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			[]any{"blogname", "grimoire test", "yes"}},
		{`INSERT INTO ` + prefix + `options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			[]any{"blogdescription", "tagline", "yes"}},
		{`INSERT INTO ` + prefix + `options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			[]any{"stylesheet", "twentytwentyfive", "yes"}},
		{`INSERT INTO ` + prefix + `options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			[]any{"theme_mods_twentytwentyfive", `a:1:{s:18:"nav_menu_locations";a:1:{s:7:"primary";i:30;}}`, "yes"}},
		{`INSERT INTO ` + prefix + `comments (comment_ID, comment_post_ID, comment_author, comment_author_email, comment_author_url, comment_author_IP, comment_date, comment_date_gmt, comment_content, comment_approved, comment_agent, comment_parent, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{101, 1, "Alice", "alice@example.com", "https://alice.example.com", "198.51.100.1", "2024-01-01 10:00:00", "2024-01-01 10:00:00", "approved comment", "1", "Browser A", 0, 0}},
		{`INSERT INTO ` + prefix + `comments (comment_ID, comment_post_ID, comment_author, comment_author_email, comment_author_url, comment_author_IP, comment_date, comment_date_gmt, comment_content, comment_approved, comment_agent, comment_parent, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{102, 1, "Bob", "bob@example.com", "", "198.51.100.2", "2024-01-02 10:00:00", "2024-01-02 10:00:00", "held comment", "0", "Browser B", 0, 0}},
		{`INSERT INTO ` + prefix + `comments (comment_ID, comment_post_ID, comment_author, comment_author_email, comment_author_url, comment_author_IP, comment_date, comment_date_gmt, comment_content, comment_approved, comment_agent, comment_parent, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{103, 2, "Spammer", "spam@example.com", "", "198.51.100.3", "2024-01-03 10:00:00", "2024-01-03 10:00:00", "spam comment", "spam", "SpamBot", 0, 0}},
		{`INSERT INTO ` + prefix + `comments (comment_ID, comment_post_ID, comment_author, comment_author_email, comment_author_url, comment_author_IP, comment_date, comment_date_gmt, comment_content, comment_approved, comment_agent, comment_parent, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{104, 2, "Trashed", "trash@example.com", "", "198.51.100.4", "2024-01-04 10:00:00", "2024-01-04 10:00:00", "trash comment", "trash", "Browser C", 0, 0}},
		{`INSERT INTO ` + prefix + `commentmeta (comment_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{101, "_seed", "comment-101"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{201, "_wp_attached_file", "2024/01/photo.jpg"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{202, "_wp_attached_file", "2024/01/asset.png"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{301, "_menu_item_type", "custom"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{301, "_menu_item_object", "custom"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{301, "_menu_item_object_id", "0"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{301, "_menu_item_menu_item_parent", "0"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{301, "_menu_item_url", "/home-custom"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{302, "_menu_item_type", "post_type"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{302, "_menu_item_object", "page"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{302, "_menu_item_object_id", "5"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{302, "_menu_item_menu_item_parent", "0"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{302, "_menu_item_url", "/stale-about"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{303, "_menu_item_type", "taxonomy"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{303, "_menu_item_object", "category"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{303, "_menu_item_object_id", "10"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{303, "_menu_item_menu_item_parent", "0"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{303, "_menu_item_url", "/stale-news"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{304, "_menu_item_type", "custom"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{304, "_menu_item_object", "custom"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{304, "_menu_item_object_id", "0"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{304, "_menu_item_menu_item_parent", "301"}},
		{`INSERT INTO ` + prefix + `postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`,
			[]any{304, "_menu_item_url", "/home-custom/sub"}},
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
		`(ID, post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_type, comment_status, post_parent, post_mime_type, menu_order) ` +
		`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func postArgs(id int64, slug, title, ptype, status, date, commentStatus string, parentID int64, mimeType string, menuOrder int) []any {
	return []any{id, 1, date, "<p>body</p>", title, "excerpt", status, slug, ptype, commentStatus, parentID, mimeType, menuOrder}
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

	runUserContract(t, newRepos)
	runSessionContract(t, newRepos)
	runWriterContract(t, newRepos)
	runAdminContract(t, newRepos)
	runCommentsContract(t, newRepos)
	runMediaContract(t, newRepos)
	runMenusContract(t, newRepos)
}

// runUserContract covers UserRepository + UserMetaRepository, including the
// PHP-serialized {prefix}capabilities round-trip that carries a user's roles.
func runUserContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("UserRepository ByLogin, ByID, and not-found", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		u, err := repos.Users.ByLogin(ctx, "admin")
		if err != nil {
			t.Fatalf("ByLogin: %v", err)
		}
		if u.ID != 1 || u.Login != "admin" {
			t.Errorf("unexpected user: %+v", u)
		}
		byID, err := repos.Users.ByID(ctx, 1)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if byID.Login != "admin" {
			t.Errorf("ByID login = %q, want admin", byID.Login)
		}
		if _, err := repos.Users.ByLogin(ctx, "ghost"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("unknown login err = %v, want ErrNotFound", err)
		}
		if _, err := repos.Users.ByID(ctx, 9999); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("unknown id err = %v, want ErrNotFound", err)
		}
	})

	t.Run("UserRepository Create returns id and round-trips", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		reg := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
		id, err := repos.Users.Create(ctx, domain.User{
			Login:       "editor",
			Nicename:    "editor",
			DisplayName: "Ed Editor",
			Pass:        "$2a$10$abcdefghijklmnopqrstuv",
			Email:       "ed@example.com",
			URL:         "https://ed.example.com",
			Registered:  reg,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if id <= 1 {
			t.Fatalf("Create id = %d, want > 1", id)
		}
		got, err := repos.Users.ByID(ctx, id)
		if err != nil {
			t.Fatalf("ByID after create: %v", err)
		}
		if got.Login != "editor" || got.Email != "ed@example.com" || got.DisplayName != "Ed Editor" {
			t.Errorf("created user mismatch: %+v", got)
		}
		if !got.Registered.Equal(reg) {
			t.Errorf("registered = %v, want %v", got.Registered, reg)
		}
	})

	t.Run("UserRepository UpdatePass and not-found", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		const newHash = "$2a$10$0000000000000000000000000000000000000000000000000000"
		if err := repos.Users.UpdatePass(ctx, 1, newHash); err != nil {
			t.Fatalf("UpdatePass: %v", err)
		}
		got, err := repos.Users.ByID(ctx, 1)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.Pass != newHash {
			t.Errorf("pass = %q, want %q", got.Pass, newHash)
		}
		if err := repos.Users.UpdatePass(ctx, 9999, newHash); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("UpdatePass missing err = %v, want ErrNotFound", err)
		}
	})

	t.Run("UserMetaRepository Set/Get upsert, capabilities round-trip, ByUser", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		// PHP-serialized capabilities array, exactly as WordPress stores roles.
		const caps = `a:1:{s:13:"administrator";b:1;}`
		capsKey := "wp_capabilities"
		levelKey := "wp_user_level"
		if err := repos.UserMeta.Set(ctx, 1, capsKey, caps); err != nil {
			t.Fatalf("Set caps: %v", err)
		}
		if err := repos.UserMeta.Set(ctx, 1, levelKey, "10"); err != nil {
			t.Fatalf("Set level: %v", err)
		}
		got, err := repos.UserMeta.Get(ctx, 1, capsKey)
		if err != nil {
			t.Fatalf("Get caps: %v", err)
		}
		if got != caps {
			t.Errorf("caps round-trip = %q, want %q", got, caps)
		}
		// Set again on the same key must update in place, not duplicate.
		const caps2 = `a:1:{s:6:"editor";b:1;}`
		if err := repos.UserMeta.Set(ctx, 1, capsKey, caps2); err != nil {
			t.Fatalf("Set caps2: %v", err)
		}
		got2, err := repos.UserMeta.Get(ctx, 1, capsKey)
		if err != nil {
			t.Fatalf("Get caps2: %v", err)
		}
		if got2 != caps2 {
			t.Errorf("caps update = %q, want %q", got2, caps2)
		}
		all, err := repos.UserMeta.ByUser(ctx, 1)
		if err != nil {
			t.Fatalf("ByUser: %v", err)
		}
		if all[capsKey] != caps2 || all[levelKey] != "10" {
			t.Errorf("ByUser map = %+v", all)
		}
		if _, err := repos.UserMeta.Get(ctx, 1, "no_such_key"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("missing meta err = %v, want ErrNotFound", err)
		}
	})
}

// runSessionContract covers SessionRepository create/lookup/rolling-refresh/
// revoke/garbage-collect against the {prefix}sessions table.
func runSessionContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("SessionRepository Create, ByID, Touch, Delete", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		expires := created.Add(14 * 24 * time.Hour)
		s := domain.Session{ID: "hash-a", UserID: 1, CSRFToken: "csrf-a", Created: created, Expires: expires}
		if err := repos.Sessions.Create(ctx, s); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repos.Sessions.ByID(ctx, "hash-a")
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.UserID != 1 || got.CSRFToken != "csrf-a" {
			t.Errorf("session mismatch: %+v", got)
		}
		if !got.Expires.Equal(expires) {
			t.Errorf("expires = %v, want %v", got.Expires, expires)
		}
		newExpiry := expires.Add(24 * time.Hour)
		if err := repos.Sessions.Touch(ctx, "hash-a", newExpiry); err != nil {
			t.Fatalf("Touch: %v", err)
		}
		touched, err := repos.Sessions.ByID(ctx, "hash-a")
		if err != nil {
			t.Fatalf("ByID after touch: %v", err)
		}
		if !touched.Expires.Equal(newExpiry) {
			t.Errorf("touched expires = %v, want %v", touched.Expires, newExpiry)
		}
		if err := repos.Sessions.Touch(ctx, "missing", newExpiry); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Touch missing err = %v, want ErrNotFound", err)
		}
		if err := repos.Sessions.Delete(ctx, "hash-a"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repos.Sessions.ByID(ctx, "hash-a"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ByID after delete err = %v, want ErrNotFound", err)
		}
		// Delete is idempotent: removing a missing session is not an error.
		if err := repos.Sessions.Delete(ctx, "hash-a"); err != nil {
			t.Errorf("Delete idempotent err = %v, want nil", err)
		}
	})

	t.Run("SessionRepository DeleteByUser and DeleteExpired", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		mk := func(id string, exp time.Time) domain.Session {
			return domain.Session{ID: id, UserID: 1, CSRFToken: "c", Created: base, Expires: exp}
		}
		if err := repos.Sessions.Create(ctx, mk("old-1", base.Add(1*time.Hour))); err != nil {
			t.Fatalf("create old-1: %v", err)
		}
		if err := repos.Sessions.Create(ctx, mk("old-2", base.Add(2*time.Hour))); err != nil {
			t.Fatalf("create old-2: %v", err)
		}
		if err := repos.Sessions.Create(ctx, mk("fresh", base.Add(100*time.Hour))); err != nil {
			t.Fatalf("create fresh: %v", err)
		}
		// GC everything expiring before base+3h: removes old-1 and old-2 only.
		n, err := repos.Sessions.DeleteExpired(ctx, base.Add(3*time.Hour))
		if err != nil {
			t.Fatalf("DeleteExpired: %v", err)
		}
		if n != 2 {
			t.Errorf("DeleteExpired count = %d, want 2", n)
		}
		if _, err := repos.Sessions.ByID(ctx, "fresh"); err != nil {
			t.Errorf("fresh should survive GC: %v", err)
		}
		// Revoke-all removes the remaining session for the user.
		removed, err := repos.Sessions.DeleteByUser(ctx, 1)
		if err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		if removed != 1 {
			t.Errorf("DeleteByUser count = %d, want 1", removed)
		}
	})
}

// runWriterContract covers the content writer ports: Post/Term/Option
// create/update/delete, verified through the existing read ports.
func runWriterContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("PostWriter Create, Update, Delete", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		id, err := repos.PostWriter.Create(ctx, domain.Post{
			Author:  1,
			Date:    time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
			Content: "<p>new</p>",
			Title:   "Fresh Post",
			Excerpt: "ex",
			Status:  "publish",
			Slug:    "fresh-post",
			Type:    "post",
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if id == 0 {
			t.Fatal("Create returned zero id")
		}
		got, err := repos.Posts.BySlug(ctx, "fresh-post")
		if err != nil {
			t.Fatalf("BySlug after create: %v", err)
		}
		if got.Title != "Fresh Post" {
			t.Errorf("title = %q, want Fresh Post", got.Title)
		}
		got.Title = "Edited Post"
		got.Content = "<p>edited</p>"
		if err := repos.PostWriter.Update(ctx, got); err != nil {
			t.Fatalf("Update: %v", err)
		}
		reread, err := repos.Posts.BySlug(ctx, "fresh-post")
		if err != nil {
			t.Fatalf("BySlug after update: %v", err)
		}
		if reread.Title != "Edited Post" || reread.Content != "<p>edited</p>" {
			t.Errorf("update not applied: %+v", reread)
		}
		if err := repos.PostWriter.Delete(ctx, id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repos.Posts.BySlug(ctx, "fresh-post"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("BySlug after delete err = %v, want ErrNotFound", err)
		}
		if err := repos.PostWriter.Update(ctx, domain.Post{ID: 999999, Slug: "x"}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Update missing err = %v, want ErrNotFound", err)
		}
		if err := repos.PostWriter.Delete(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Delete missing err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TermWriter Create and Delete", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		id, err := repos.TermWriter.Create(ctx, domain.Term{Name: "Go", Slug: "go", Taxonomy: "post_tag"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if id == 0 {
			t.Fatal("Create returned zero term_id")
		}
		got, err := repos.Terms.BySlug(ctx, "post_tag", "go")
		if err != nil {
			t.Fatalf("BySlug after create: %v", err)
		}
		if got.Name != "Go" || got.Taxonomy != "post_tag" {
			t.Errorf("term mismatch: %+v", got)
		}
		if err := repos.TermWriter.Delete(ctx, id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repos.Terms.BySlug(ctx, "post_tag", "go"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("BySlug after delete err = %v, want ErrNotFound", err)
		}
		if err := repos.TermWriter.Delete(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Delete missing err = %v, want ErrNotFound", err)
		}
	})

	t.Run("OptionWriter Set upsert and Delete", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		if err := repos.OptionWriter.Set(ctx, "siteurl", "https://a.example"); err != nil {
			t.Fatalf("Set insert: %v", err)
		}
		v, err := repos.Options.Get(ctx, "siteurl")
		if err != nil {
			t.Fatalf("Get after insert: %v", err)
		}
		if v != "https://a.example" {
			t.Errorf("siteurl = %q", v)
		}
		if err := repos.OptionWriter.Set(ctx, "siteurl", "https://b.example"); err != nil {
			t.Fatalf("Set update: %v", err)
		}
		v2, err := repos.Options.Get(ctx, "siteurl")
		if err != nil {
			t.Fatalf("Get after update: %v", err)
		}
		if v2 != "https://b.example" {
			t.Errorf("siteurl updated = %q", v2)
		}
		if err := repos.OptionWriter.Delete(ctx, "siteurl"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repos.Options.Get(ctx, "siteurl"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Get after delete err = %v, want ErrNotFound", err)
		}
		if err := repos.OptionWriter.Delete(ctx, "no_such_option"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Delete missing err = %v, want ErrNotFound", err)
		}
	})
}
