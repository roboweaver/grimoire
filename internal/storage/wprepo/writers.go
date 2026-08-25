package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
)

// --- PostRepo write methods (domain.PostWriter) ---

// ByID returns the stored post/page by primary key regardless of status or
// type, or ErrNotFound. Write services use it to load the authoritative record
// before running capability checks, so authorization never trusts caller input.
func (r *PostRepo) ByID(ctx context.Context, id int64) (domain.Post, error) {
	var row postRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("? = ?", bun.Ident("ID"), id).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Post{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Post{}, err
	}
	return row.toDomain(), nil
}

// Create inserts a new post and returns its generated ID. The autoincrement
// "ID" column is omitted from the insert, so the unquoted SQL is vendor-safe.
// Modified/ModifiedGMT are always set to the current time (Req 3.4); DateGMT
// defaults to the current time only when the caller left it zero, mirroring
// post_date's own zero-value default handling via formatTS.
func (r *PostRepo) Create(ctx context.Context, p domain.Post) (int64, error) {
	now := time.Now()
	dateGMT := p.DateGMT
	if dateGMT.IsZero() {
		dateGMT = now
	}
	cols := []string{
		"post_author", "post_date", "post_content", "post_title",
		"post_excerpt", "post_status", "post_name", "post_type", "comment_status",
		"post_date_gmt", "post_modified", "post_modified_gmt",
	}
	args := []any{
		p.Author, formatTS(p.Date), p.Content, p.Title,
		p.Excerpt, p.Status, p.Slug, p.Type, p.CommentStatus,
		formatTS(dateGMT), formatTS(now), formatTS(now),
	}
	return insertReturningID(ctx, r.db, vendorOf(r.db), r.prefix+"posts", cols, `"ID"`, args...)
}

// Update replaces an existing post's mutable fields by ID, or ErrNotFound.
// Modified/ModifiedGMT are always bumped to the current time (Req 1.1/1.2),
// overriding whatever the caller passed in p.Modified/p.ModifiedGMT.
func (r *PostRepo) Update(ctx context.Context, p domain.Post) error {
	now := time.Now()
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Set("post_author = ?", p.Author).
		Set("post_date = ?", formatTS(p.Date)).
		Set("post_content = ?", p.Content).
		Set("post_title = ?", p.Title).
		Set("post_excerpt = ?", p.Excerpt).
		Set("post_status = ?", p.Status).
		Set("post_name = ?", p.Slug).
		Set("post_type = ?", p.Type).
		Set("comment_status = ?", p.CommentStatus).
		Set("post_modified = ?", formatTS(now)).
		Set("post_modified_gmt = ?", formatTS(now)).
		Where("? = ?", bun.Ident("ID"), p.ID).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}

// Delete removes a post by ID, or ErrNotFound.
func (r *PostRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.NewDelete().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Where("? = ?", bun.Ident("ID"), id).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}

// --- TermRepo write methods (domain.TermWriter) ---

// Create inserts a term and its taxonomy row in one transaction, returning the
// term_id. The taxonomy row carries WordPress defaults (empty description,
// parent 0, count 0).
func (r *TermRepo) Create(ctx context.Context, t domain.Term) (int64, error) {
	vendor := vendorOf(r.db)
	var termID int64
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		id, err := insertReturningID(ctx, tx, vendor, r.prefix+"terms",
			[]string{"name", "slug"}, "term_id", t.Name, t.Slug)
		if err != nil {
			return err
		}
		termID = id
		q := "INSERT INTO " + r.prefix + "term_taxonomy " +
			"(term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?)"
		_, err = tx.ExecContext(ctx, rebind.Rebind(vendor, q), termID, t.Taxonomy, "", 0, 0)
		return err
	})
	if err != nil {
		return 0, err
	}
	return termID, nil
}

// Update renames an existing term's name/slug by term_id, or ErrNotFound.
// Taxonomy is immutable via Update (Req 1.1/1.2 only describe rename); a
// caller wanting to move a term to a different taxonomy would Delete and
// re-Create.
func (r *TermRepo) Update(ctx context.Context, t domain.Term) error {
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"terms")).
		Set("name = ?", t.Name).
		Set("slug = ?", t.Slug).
		Where("term_id = ?", t.ID).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}

// Delete removes a term, its taxonomy rows, and any term relationships pointing
// at those taxonomy rows, all in one transaction. It returns ErrNotFound when no
// term has the given term_id.
func (r *TermRepo) Delete(ctx context.Context, id int64) error {
	vendor := vendorOf(r.db)
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		relQ := "DELETE FROM " + r.prefix + "term_relationships WHERE term_taxonomy_id IN " +
			"(SELECT term_taxonomy_id FROM " + r.prefix + "term_taxonomy WHERE term_id = ?)"
		if _, err := tx.ExecContext(ctx, rebind.Rebind(vendor, relQ), id); err != nil {
			return err
		}
		ttQ := "DELETE FROM " + r.prefix + "term_taxonomy WHERE term_id = ?"
		if _, err := tx.ExecContext(ctx, rebind.Rebind(vendor, ttQ), id); err != nil {
			return err
		}
		termQ := "DELETE FROM " + r.prefix + "terms WHERE term_id = ?"
		res, err := tx.ExecContext(ctx, rebind.Rebind(vendor, termQ), id)
		if err != nil {
			return err
		}
		return errNotFoundIfZero(res)
	})
}

// --- OptionRepo write methods (domain.OptionWriter) ---

// Set upserts an option value by name. An existing row is updated in place;
// otherwise a new autoloaded row is inserted.
func (r *OptionRepo) Set(ctx context.Context, name, value string) error {
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"options")).
		Set("option_value = ?", value).
		Where("option_name = ?", name).
		Exec(ctx)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	q := "INSERT INTO " + r.prefix + "options (option_name, option_value, autoload) VALUES (?, ?, ?)"
	_, err = r.db.ExecContext(ctx, rebind.Rebind(vendorOf(r.db), q), name, value, "yes")
	return err
}

// Delete removes an option by name, or ErrNotFound.
func (r *OptionRepo) Delete(ctx context.Context, name string) error {
	res, err := r.db.NewDelete().
		TableExpr("?", bun.Ident(r.prefix+"options")).
		Where("option_name = ?", name).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}

// compile-time interface checks.
var (
	_ domain.PostWriter   = (*PostRepo)(nil)
	_ domain.TermWriter   = (*TermRepo)(nil)
	_ domain.TermReader   = (*TermRepo)(nil)
	_ domain.OptionWriter = (*OptionRepo)(nil)
)
