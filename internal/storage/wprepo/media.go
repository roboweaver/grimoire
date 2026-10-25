package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
)

var (
	_ domain.MediaRepository = (*MediaRepo)(nil)
	_ domain.MediaWriter     = (*MediaRepo)(nil)
)

type mediaRow struct {
	ID          int64  `bun:"ID"`
	Title       string `bun:"post_title"`
	Filename    string `bun:"filename"`
	MimeType    string `bun:"post_mime_type"`
	Date        string `bun:"post_date"`
	ParentID    int64  `bun:"post_parent"`
	Slug        string `bun:"post_name"`
	AuthorID    int64  `bun:"post_author"`
	Caption     string `bun:"post_excerpt"`
	Description string `bun:"post_content"`
	// AltText is read from the _wp_attachment_image_alt postmeta row via a
	// LEFT JOIN; the SELECT wraps its meta_value in COALESCE(..., '') so an
	// attachment with no alt row scans as "" rather than a NULL-scan error.
	AltText string `bun:"alt_text"`
}

func (r mediaRow) toDomain() domain.Media {
	return domain.Media{
		ID:          r.ID,
		Title:       r.Title,
		Filename:    r.Filename,
		URL:         "/wp-content/uploads/" + r.Filename,
		MimeType:    r.MimeType,
		Date:        parseTS(r.Date),
		ParentID:    r.ParentID,
		Slug:        r.Slug,
		AuthorID:    r.AuthorID,
		Caption:     r.Caption,
		Description: r.Description,
		AltText:     r.AltText,
	}
}

type MediaRepo struct {
	db     *bun.DB
	prefix string
}

func NewMediaRepo(db *bun.DB, prefix string) *MediaRepo { return &MediaRepo{db: db, prefix: prefix} }

// mediaWhere applies every domain.MediaFilter predicate identically to both
// listQuery and Count, so filtered item and count results can never diverge
// (Requirement 8, Requirement 5 ACs 1-4). Search matches the attachment
// title (p.post_title) or its WordPress-standard stored filename
// (_wp_attached_file postmeta), both case-insensitively, so a search for a
// file extension or generated filename works the same as a title search
// (Requirement 5.1). Type maps to a post_mime_type prefix family; "document"
// is defined as "not image/video/audio" since WordPress has no reserved
// document/* MIME prefix. After/Before compare against post_date using the
// same formatTS format already used by post/term reads in this package.
func mediaWhere(q *bun.SelectQuery, f domain.MediaFilter) *bun.SelectQuery {
	if f.ParentID != 0 {
		q = q.Where("p.post_parent = ?", f.ParentID)
	}
	if f.Search != "" {
		like := "%" + strings.ToLower(f.Search) + "%"
		q = q.Where("(LOWER(p.post_title) LIKE ? OR LOWER(pm.meta_value) LIKE ?)", like, like)
	}
	switch f.Type {
	case "image", "video", "audio":
		q = q.Where("LOWER(p.post_mime_type) LIKE ?", f.Type+"/%")
	case "document":
		q = q.Where("LOWER(p.post_mime_type) NOT LIKE ? AND LOWER(p.post_mime_type) NOT LIKE ? AND LOWER(p.post_mime_type) NOT LIKE ?",
			"image/%", "video/%", "audio/%")
	}
	if !f.After.IsZero() {
		q = q.Where("p.post_date >= ?", formatTS(f.After))
	}
	if !f.Before.IsZero() {
		// Before is the inclusive end of a calendar day (Requirement 5.3
		// treats after/before as ISO-8601 dates), so compare against the
		// start of the following day rather than excluding same-day rows.
		q = q.Where("p.post_date < ?", formatTS(f.Before.AddDate(0, 0, 1)))
	}
	return q
}

func (r *MediaRepo) listQuery(ctx context.Context, f domain.MediaFilter) *bun.SelectQuery {
	q := r.db.NewSelect().
		TableExpr("? AS p", bun.Ident(r.prefix+"posts")).
		ColumnExpr("p.?", bun.Ident("ID")).
		ColumnExpr("p.post_title, pm.meta_value AS filename, p.post_mime_type, p.post_date, p.post_parent, p.post_name, p.post_author, p.post_excerpt, p.post_content").
		ColumnExpr("COALESCE(alt.meta_value, '') AS alt_text").
		Join("JOIN ? AS pm ON pm.post_id = p.? AND pm.meta_key = ?", bun.Ident(r.prefix+"postmeta"), bun.Ident("ID"), "_wp_attached_file").
		Join("LEFT JOIN ? AS alt ON alt.post_id = p.? AND alt.meta_key = ?", bun.Ident(r.prefix+"postmeta"), bun.Ident("ID"), "_wp_attachment_image_alt").
		Where("p.post_type = ?", "attachment").
		OrderExpr("p.post_date DESC, p.? DESC", bun.Ident("ID"))
	q = mediaWhere(q, f)
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	if f.Offset > 0 {
		q = q.Offset(f.Offset)
	}
	_ = ctx
	return q
}

func (r *MediaRepo) List(ctx context.Context, f domain.MediaFilter) ([]domain.Media, error) {
	var rows []mediaRow
	if err := r.listQuery(ctx, f).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.Media, len(rows))
	for i, row := range rows {
		out[i] = row.toDomain()
	}
	return out, nil
}

func (r *MediaRepo) Count(ctx context.Context, f domain.MediaFilter) (int, error) {
	q := r.db.NewSelect().
		TableExpr("? AS p", bun.Ident(r.prefix+"posts")).
		Join("JOIN ? AS pm ON pm.post_id = p.? AND pm.meta_key = ?", bun.Ident(r.prefix+"postmeta"), bun.Ident("ID"), "_wp_attached_file").
		Where("p.post_type = ?", "attachment")
	q = mediaWhere(q, f)
	return q.Count(ctx)
}

func (r *MediaRepo) ByID(ctx context.Context, id int64) (domain.Media, error) {
	var row mediaRow
	err := r.db.NewSelect().
		TableExpr("? AS p", bun.Ident(r.prefix+"posts")).
		ColumnExpr("p.?", bun.Ident("ID")).
		ColumnExpr("p.post_title, pm.meta_value AS filename, p.post_mime_type, p.post_date, p.post_parent, p.post_name, p.post_author, p.post_excerpt, p.post_content").
		ColumnExpr("COALESCE(alt.meta_value, '') AS alt_text").
		Join("JOIN ? AS pm ON pm.post_id = p.? AND pm.meta_key = ?", bun.Ident(r.prefix+"postmeta"), bun.Ident("ID"), "_wp_attached_file").
		Join("LEFT JOIN ? AS alt ON alt.post_id = p.? AND alt.meta_key = ?", bun.Ident(r.prefix+"postmeta"), bun.Ident("ID"), "_wp_attachment_image_alt").
		Where("p.post_type = ?", "attachment").
		Where("p.? = ?", bun.Ident("ID"), id).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Media{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Media{}, err
	}
	return row.toDomain(), nil
}

func (r *MediaRepo) Create(ctx context.Context, m domain.Media) (int64, error) {
	vendor := vendorOf(r.db)
	var id int64
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		created, err := insertReturningID(ctx, tx, vendor, r.prefix+"posts",
			[]string{"post_author", "post_date", "post_content", "post_title", "post_excerpt", "post_status", "post_name", "post_type", "comment_status", "post_parent", "post_mime_type", "menu_order"},
			`"ID"`,
			m.AuthorID, formatTS(m.Date), "", m.Title, "", "inherit", m.Slug, "attachment", "closed", m.ParentID, m.MimeType, 0,
		)
		if err != nil {
			return err
		}
		id = created
		q := "INSERT INTO " + r.prefix + "postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)"
		_, err = tx.ExecContext(ctx, q, id, "_wp_attached_file", m.Filename)
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (r *MediaRepo) SetParent(ctx context.Context, id, parentID int64) error {
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Set("post_parent = ?", parentID).
		Where("? = ?", bun.Ident("ID"), id).
		Where("post_type = ?", "attachment").
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfMissing(ctx, res, func(ctx context.Context) (bool, error) {
		return r.db.NewSelect().
			TableExpr("?", bun.Ident(r.prefix+"posts")).
			Where("? = ?", bun.Ident("ID"), id).
			Where("post_type = ?", "attachment").
			Exists(ctx)
	})
}

// UpdateMeta writes the editable attachment metadata in a single transaction:
// an UPDATE of post_title/post_excerpt/post_content on the attachment's posts
// row (guarded by post_type='attachment' so it can never touch a non-attachment
// row), followed by an upsert of the _wp_attachment_image_alt postmeta row.
// ErrNotFound is returned when the posts UPDATE matched no attachment (Req 3.4),
// decided exactly as SetParent does via errNotFoundIfMissing — only here the
// Exists check runs against the enclosing transaction so the "0 rows affected"
// disambiguation sees the same snapshot as the UPDATE.
func (r *MediaRepo) UpdateMeta(ctx context.Context, id int64, m domain.MediaMeta) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().
			TableExpr("?", bun.Ident(r.prefix+"posts")).
			Set("post_title = ?", m.Title).
			Set("post_excerpt = ?", m.Caption).
			Set("post_content = ?", m.Description).
			Where("? = ?", bun.Ident("ID"), id).
			Where("post_type = ?", "attachment").
			Exec(ctx)
		if err != nil {
			return err
		}
		if err := errNotFoundIfMissing(ctx, res, func(ctx context.Context) (bool, error) {
			return tx.NewSelect().
				TableExpr("?", bun.Ident(r.prefix+"posts")).
				Where("? = ?", bun.Ident("ID"), id).
				Where("post_type = ?", "attachment").
				Exists(ctx)
		}); err != nil {
			return err // ErrNotFound when no attachment matched (Req 3.4)
		}
		return upsertPostMeta(ctx, tx, r.prefix, id, "_wp_attachment_image_alt", m.AltText)
	})
}

// upsertPostMeta keeps exactly one postmeta row for (post_id, meta_key): it
// DELETEs any existing rows for the pair then INSERTs a single row with value.
// DELETE-then-INSERT is the simplest cross-vendor-correct single-valued upsert
// and reuses the raw parameterized ?-placeholder style MediaRepo.Create already
// uses for the _wp_attached_file row (Bun rebinds the placeholders per vendor —
// see execQuerier). tx satisfies execQuerier, so the two writes share the
// caller's transaction.
func upsertPostMeta(ctx context.Context, tx bun.Tx, prefix string, postID int64, key, value string) error {
	del := "DELETE FROM " + prefix + "postmeta WHERE post_id = ? AND meta_key = ?"
	if _, err := tx.ExecContext(ctx, del, postID, key); err != nil {
		return err
	}
	ins := "INSERT INTO " + prefix + "postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)"
	_, err := tx.ExecContext(ctx, ins, postID, key, value)
	return err
}

// Delete hard-deletes an attachment's {prefix}term_relationships rows, its
// {prefix}postmeta rows, and finally its {prefix}posts row, in one transaction,
// deepest-dependency-first so no orphan survives a partial failure (Req 4.1).
// It returns domain.ErrNotFound when the posts delete affected no row (Req 4.4),
// matching PostWriter.Delete. It does NOT touch the on-disk file.
func (r *MediaRepo) Delete(ctx context.Context, id int64) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM "+r.prefix+"term_relationships WHERE object_id = ?", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM "+r.prefix+"postmeta WHERE post_id = ?", id); err != nil {
			return err
		}
		pID := rebind.Ident(vendorOf(r.db), "ID")
		res, err := tx.ExecContext(ctx,
			"DELETE FROM "+r.prefix+"posts WHERE "+pID+" = ?", id)
		if err != nil {
			return err
		}
		return errNotFoundIfZero(res)
	})
}
