package storagetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
)

// altMetaKey is the WordPress-standard postmeta key the Media Library "Alt
// Text" field and wp_update_attachment_metadata use. UpdateMeta writes alt_text
// to exactly this key; the literal is pinned here so a drift in the repo
// implementation is caught by this contract rather than silently writing to the
// wrong key.
const altMetaKey = "_wp_attachment_image_alt"

// NewReposWithPrefixFunc builds a migrated + seeded Repositories bundle and
// returns, alongside the usual cleanup, the vendor and table prefix the backend
// was built against. The media-write contract needs those two extra facts
// because MediaRepo.ByID does not yet surface caption/alt_text/description (that
// is a later task), so UpdateMeta's writes to post_excerpt/post_content and the
// _wp_attachment_image_alt postmeta row must be verified by a direct, prefixed
// SQL read against the same *sql.DB — exactly how RunMigrationContract reads
// raw columns through its OpenRawDB builder. The ordinary read-port assertions
// still go through the bundle; only the not-yet-surfaced write-backs use the
// raw read.
type NewReposWithPrefixFunc func(t *testing.T) (repos *storage.Repositories, vendor, prefix string, cleanup func())

// RunMediaWriteMetaContract pins the cross-vendor contract of
// MediaRepo.UpdateMeta against a freshly built, seeded backend. It runs under
// the same storagetest harness every other media test uses (SQLite always;
// MySQL/Postgres env-gated), reusing SeedFixtures' attachment 201 (an
// attachment with no alt postmeta row) and post 1 (a non-attachment row) rather
// than inventing new fixtures.
//
// title write-back is observable through the existing MediaRepo.ByID
// (post_title -> Media.Title); caption/description/alt_text are not surfaced by
// ByID yet, so they are verified by a direct prefixed SQL read against the
// bundle's *sql.DB, mirroring RunMigrationContract's raw-column reads.
func RunMediaWriteMetaContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("UpdateMeta writes posts-row fields and upserts the alt postmeta row", func(t *testing.T) {
		repos, vendor, prefix, cleanup := newRepos(t)
		defer cleanup()
		db := repos.DB()

		const attID int64 = 201

		// Insert branch: attachment 201 has no _wp_attachment_image_alt row in
		// the fixtures, so the first UpdateMeta must INSERT it.
		meta := domain.MediaMeta{
			Title:       "Edited Title",
			Caption:     "Edited caption",
			AltText:     "edited alt text",
			Description: "Edited description body",
		}
		if err := repos.MediaWriter.UpdateMeta(ctx, attID, meta); err != nil {
			t.Fatalf("UpdateMeta insert-alt branch: %v", err)
		}

		// post_title is observable through the existing read port.
		got, err := repos.Media.ByID(ctx, attID)
		if err != nil {
			t.Fatalf("ByID after UpdateMeta: %v", err)
		}
		if got.Title != "Edited Title" {
			t.Errorf("post_title (via ByID) = %q, want %q", got.Title, "Edited Title")
		}

		// post_excerpt / post_content are not surfaced by ByID yet, so read the
		// row directly through the same *sql.DB and table prefix.
		excerpt, content := readPostExcerptContent(t, ctx, db, vendor, prefix, attID)
		if excerpt != "Edited caption" {
			t.Errorf("post_excerpt = %q, want %q", excerpt, "Edited caption")
		}
		if content != "Edited description body" {
			t.Errorf("post_content = %q, want %q", content, "Edited description body")
		}

		// The alt postmeta row was inserted exactly once with the pinned key.
		if n := altMetaCount(t, ctx, db, vendor, prefix, attID); n != 1 {
			t.Fatalf("%s rows after insert = %d, want exactly 1", altMetaKey, n)
		}
		if v := altMetaValue(t, ctx, db, vendor, prefix, attID); v != "edited alt text" {
			t.Errorf("%s value after insert = %q, want %q", altMetaKey, v, "edited alt text")
		}

		// Overwrite branch: a second UpdateMeta with a different alt_text must
		// overwrite the existing row in place, not append a duplicate.
		meta.AltText = "replacement alt text"
		meta.Caption = "replacement caption"
		if err := repos.MediaWriter.UpdateMeta(ctx, attID, meta); err != nil {
			t.Fatalf("UpdateMeta overwrite-alt branch: %v", err)
		}
		if n := altMetaCount(t, ctx, db, vendor, prefix, attID); n != 1 {
			t.Fatalf("%s rows after overwrite = %d, want exactly 1 (upsert, not append)", altMetaKey, n)
		}
		if v := altMetaValue(t, ctx, db, vendor, prefix, attID); v != "replacement alt text" {
			t.Errorf("%s value after overwrite = %q, want %q", altMetaKey, v, "replacement alt text")
		}
	})

	t.Run("ByID surfaces caption/description/alt_text round-tripped through UpdateMeta", func(t *testing.T) {
		// This is the read-port complement of the write-verification above:
		// task 2.1 verified UpdateMeta's writes via DIRECT, prefixed SQL because
		// MediaRepo.ByID did not yet surface caption/description/alt_text. This
		// subtest instead asserts the SAME values are observable through the
		// read port — MediaRepo.ByID populating Media.Caption (post_excerpt),
		// Media.Description (post_content) and Media.AltText (the
		// _wp_attachment_image_alt postmeta). It pins the read extension that
		// task 2.4 implements; until then ByID returns those three fields empty
		// and these assertions fail (the expected TDD-red). (Req 10.3)
		repos, _, _, cleanup := newRepos(t)
		defer cleanup()

		const attID int64 = 201

		meta := domain.MediaMeta{
			Title:       "Read Title",
			Caption:     "read caption",
			AltText:     "read alt text",
			Description: "read description body",
		}
		if err := repos.MediaWriter.UpdateMeta(ctx, attID, meta); err != nil {
			t.Fatalf("UpdateMeta before read-back: %v", err)
		}

		got, err := repos.Media.ByID(ctx, attID)
		if err != nil {
			t.Fatalf("ByID after UpdateMeta: %v", err)
		}
		if got.Caption != "read caption" {
			t.Errorf("ByID Caption = %q, want %q (post_excerpt via the read port)", got.Caption, "read caption")
		}
		if got.Description != "read description body" {
			t.Errorf("ByID Description = %q, want %q (post_content via the read port)", got.Description, "read description body")
		}
		if got.AltText != "read alt text" {
			t.Errorf("ByID AltText = %q, want %q (%s postmeta via the read port)", got.AltText, "read alt text", altMetaKey)
		}
	})

	t.Run("ByID reads AltText as empty for an attachment with no alt postmeta row", func(t *testing.T) {
		// Attachment 202 carries _wp_attached_file (so ByID surfaces it) but no
		// _wp_attachment_image_alt row, so the extended read must report
		// AltText == "" rather than fail or inherit a stale value. Also pins the
		// task 2.4 read extension. (Req 10.3)
		repos, _, _, cleanup := newRepos(t)
		defer cleanup()

		const noAltID int64 = 202

		got, err := repos.Media.ByID(ctx, noAltID)
		if err != nil {
			t.Fatalf("ByID attachment with no alt row: %v", err)
		}
		if got.AltText != "" {
			t.Errorf("ByID AltText = %q, want \"\" for an attachment with no %s row", got.AltText, altMetaKey)
		}
	})

	t.Run("UpdateMeta returns ErrNotFound for an unknown ID", func(t *testing.T) {
		repos, _, _, cleanup := newRepos(t)
		defer cleanup()
		err := repos.MediaWriter.UpdateMeta(ctx, 9999, domain.MediaMeta{Title: "nope"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("UpdateMeta unknown id err = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdateMeta refuses a non-attachment row and never mutates it", func(t *testing.T) {
		repos, vendor, prefix, cleanup := newRepos(t)
		defer cleanup()
		db := repos.DB()

		// Post 1 is a published post (post_type='post'), not an attachment.
		const postID int64 = 1

		beforeTitle, beforeContent := readPostTitleContent(t, ctx, db, vendor, prefix, postID)

		err := repos.MediaWriter.UpdateMeta(ctx, postID, domain.MediaMeta{
			Title:       "hijacked title",
			Caption:     "hijacked caption",
			AltText:     "hijacked alt",
			Description: "hijacked description",
		})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("UpdateMeta non-attachment err = %v, want ErrNotFound", err)
		}

		afterTitle, afterContent := readPostTitleContent(t, ctx, db, vendor, prefix, postID)
		if afterTitle != beforeTitle || afterContent != beforeContent {
			t.Errorf("non-attachment row mutated: title %q->%q, content %q->%q",
				beforeTitle, afterTitle, beforeContent, afterContent)
		}
		// The refusal must also leave no stray alt postmeta row behind.
		if n := altMetaCount(t, ctx, db, vendor, prefix, postID); n != 0 {
			t.Errorf("%s rows for refused non-attachment = %d, want 0", altMetaKey, n)
		}
	})
}

// twoParam fills a template carrying exactly two "%s" verbs with two
// vendor-correct, independently-numbered placeholders. The shared placeholders
// helper collapses all n placeholders into a single "%s" (its "VALUES (%s)"
// list form), so it cannot address two separate "col = %s AND col = %s" slots;
// the alt-postmeta lookups need one placeholder per column predicate.
func twoParam(vendor, template string) string {
	if vendor == "postgres" {
		return fmt.Sprintf(template, "$1", "$2")
	}
	return fmt.Sprintf(template, "?", "?")
}

// idCol returns the vendor-correct, quoted reference to the mixed-case posts
// primary key column (ID). Postgres folds an unquoted ID to lowercase, which
// does not exist; SQLite/MySQL accept the bare name.
func idCol(vendor string) string {
	if vendor == "postgres" {
		return `"ID"`
	}
	return "ID"
}

// readPostExcerptContent reads post_excerpt and post_content for a posts-row id
// through the bundle's raw *sql.DB (the fields MediaRepo.ByID does not surface
// yet).
func readPostExcerptContent(t *testing.T, ctx context.Context, db *sql.DB, vendor, prefix string, id int64) (string, string) {
	t.Helper()
	var excerpt, content string
	row := db.QueryRowContext(ctx, placeholders(vendor,
		`SELECT post_excerpt, post_content FROM `+prefix+`posts WHERE `+idCol(vendor)+` = %s`, 1),
		id)
	if err := row.Scan(&excerpt, &content); err != nil {
		t.Fatalf("scan post_excerpt/post_content for id %d: %v", id, err)
	}
	return excerpt, content
}

// readPostTitleContent reads post_title and post_content for a posts-row id,
// used to prove the non-attachment refusal leaves the row untouched.
func readPostTitleContent(t *testing.T, ctx context.Context, db *sql.DB, vendor, prefix string, id int64) (string, string) {
	t.Helper()
	var title, content string
	row := db.QueryRowContext(ctx, placeholders(vendor,
		`SELECT post_title, post_content FROM `+prefix+`posts WHERE `+idCol(vendor)+` = %s`, 1),
		id)
	if err := row.Scan(&title, &content); err != nil {
		t.Fatalf("scan post_title/post_content for id %d: %v", id, err)
	}
	return title, content
}

// altMetaCount counts the _wp_attachment_image_alt postmeta rows for a post id,
// so insert-vs-overwrite can be told apart (a correct upsert keeps exactly one).
func altMetaCount(t *testing.T, ctx context.Context, db *sql.DB, vendor, prefix string, postID int64) int {
	t.Helper()
	var n int
	row := db.QueryRowContext(ctx, twoParam(vendor,
		`SELECT COUNT(*) FROM `+prefix+`postmeta WHERE post_id = %s AND meta_key = %s`),
		postID, altMetaKey)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count %s rows for id %d: %v", altMetaKey, postID, err)
	}
	return n
}

// altMetaValue returns the single _wp_attachment_image_alt postmeta value for a
// post id. It assumes the caller has already asserted exactly one row exists.
func altMetaValue(t *testing.T, ctx context.Context, db *sql.DB, vendor, prefix string, postID int64) string {
	t.Helper()
	var v string
	row := db.QueryRowContext(ctx, twoParam(vendor,
		`SELECT meta_value FROM `+prefix+`postmeta WHERE post_id = %s AND meta_key = %s`),
		postID, altMetaKey)
	if err := row.Scan(&v); err != nil {
		t.Fatalf("read %s value for id %d: %v", altMetaKey, postID, err)
	}
	return v
}
