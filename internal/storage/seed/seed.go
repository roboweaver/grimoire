// Package seed inserts sample content into a migrated grimoire database so a
// fresh install has something to render. Run is idempotent: a second call makes
// no further changes.
package seed

import (
	"context"
	"database/sql"
	"fmt"
)

type stmt struct {
	q    string
	args []any
}

// Run inserts sample options, a user, posts, a page, and a category. It is
// idempotent, guarded by the presence of the "hello-world" post.
func Run(ctx context.Context, db *sql.DB, prefix string) error {
	seeded, err := alreadySeeded(ctx, db, prefix)
	if err != nil {
		return err
	}
	if seeded {
		return nil
	}

	stmts := []stmt{
		{`INSERT INTO ` + prefix + `options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			[]any{"blogname", "grimoire", "yes"}},
		{`INSERT INTO ` + prefix + `options (option_name, option_value, autoload) VALUES (?, ?, ?)`,
			[]any{"blogdescription", "A Go-native CMS", "yes"}},
		{`INSERT INTO ` + prefix + `users (ID, user_login, user_nicename, display_name) VALUES (?, ?, ?, ?)`,
			[]any{1, "admin", "admin", "Admin"}},
		post(prefix, 1, "hello-world", "Hello, World", "post", "publish", "2024-01-01 09:00:00",
			"<p>Welcome to grimoire, a Go-native, WordPress-compatible CMS.</p>", "The first post."),
		post(prefix, 2, "second-post", "Second Post", "post", "publish", "2024-01-02 09:00:00",
			"<p>Another article rendered server-side from the database.</p>", "More content."),
		post(prefix, 3, "third-post", "Third Post", "post", "publish", "2024-01-03 09:00:00",
			"<p>The third of three seeded posts.</p>", "Even more."),
		post(prefix, 4, "about", "About", "page", "publish", "2024-01-04 09:00:00",
			"<p>grimoire is a single-binary CMS with a switchable database backend.</p>", "About this site."),
		{`INSERT INTO ` + prefix + `terms (term_id, name, slug) VALUES (?, ?, ?)`,
			[]any{1, "News", "news"}},
		{`INSERT INTO ` + prefix + `term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{1, 1, "category", "", 0, 2}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{1, 1, 0}},
		{`INSERT INTO ` + prefix + `term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`,
			[]any{2, 1, 0}},
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s.q, s.args...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("seed %q: %w", s.q, err)
		}
	}
	return tx.Commit()
}

func alreadySeeded(ctx context.Context, db *sql.DB, prefix string) (bool, error) {
	var n int
	row := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+prefix+`posts WHERE post_name = ?`, "hello-world")
	if err := row.Scan(&n); err != nil {
		return false, fmt.Errorf("seed guard: %w", err)
	}
	return n > 0, nil
}

func post(prefix string, id int64, slug, title, ptype, status, date, content, excerpt string) stmt {
	return stmt{
		q: `INSERT INTO ` + prefix + `posts ` +
			`(ID, post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_type) ` +
			`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		args: []any{id, 1, date, content, title, excerpt, status, slug, ptype},
	}
}
