package wprepo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
)

var (
	_ domain.CommentRepository     = (*CommentRepo)(nil)
	_ domain.CommentWriter         = (*CommentRepo)(nil)
	_ domain.CommentMetaRepository = (*CommentMetaRepo)(nil)
)

var commentColumns = []string{
	"comment_ID", "comment_post_ID", "comment_author", "comment_author_email",
	"comment_author_url", "comment_author_IP", "comment_date", "comment_date_gmt",
	"comment_content", "comment_approved", "comment_agent", "comment_parent", "user_id",
}

type commentRow struct {
	ID          int64  `bun:"comment_ID"`
	PostID      int64  `bun:"comment_post_ID"`
	Author      string `bun:"comment_author"`
	AuthorEmail string `bun:"comment_author_email"`
	AuthorURL   string `bun:"comment_author_url"`
	AuthorIP    string `bun:"comment_author_IP"`
	Date        string `bun:"comment_date"`
	DateGMT     string `bun:"comment_date_gmt"`
	Content     string `bun:"comment_content"`
	Status      string `bun:"comment_approved"`
	Agent       string `bun:"comment_agent"`
	Parent      int64  `bun:"comment_parent"`
	UserID      int64  `bun:"user_id"`
}

func (r commentRow) toDomain() domain.Comment {
	return domain.Comment{
		ID:          r.ID,
		PostID:      r.PostID,
		Author:      r.Author,
		AuthorEmail: r.AuthorEmail,
		AuthorURL:   r.AuthorURL,
		AuthorIP:    r.AuthorIP,
		Date:        parseTS(r.Date),
		DateGMT:     parseTS(r.DateGMT),
		Content:     r.Content,
		Status:      r.Status,
		Agent:       r.Agent,
		Parent:      r.Parent,
		UserID:      r.UserID,
	}
}

type CommentRepo struct {
	db     *bun.DB
	prefix string
}

func NewCommentRepo(db *bun.DB, prefix string) *CommentRepo {
	return &CommentRepo{db: db, prefix: prefix}
}

func (r *CommentRepo) List(ctx context.Context, f domain.CommentFilter) ([]domain.Comment, error) {
	var rows []commentRow
	q := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"comments")).
		Column(commentColumns...)
	if f.PostID != 0 {
		q = q.Where("comment_post_ID = ?", f.PostID)
	}
	if len(f.Statuses) > 0 {
		q = q.Where("comment_approved IN (?)", bun.In(f.Statuses))
	}
	if f.PostID != 0 {
		q = q.OrderExpr("comment_date ASC, ? ASC", bun.Ident("comment_ID"))
	} else {
		q = q.OrderExpr("comment_date DESC, ? DESC", bun.Ident("comment_ID"))
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	if f.Offset > 0 {
		q = q.Offset(f.Offset)
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.Comment, len(rows))
	for i, row := range rows {
		out[i] = row.toDomain()
	}
	return out, nil
}

func (r *CommentRepo) Count(ctx context.Context, f domain.CommentFilter) (int, error) {
	q := r.db.NewSelect().TableExpr("?", bun.Ident(r.prefix+"comments"))
	if f.PostID != 0 {
		q = q.Where("comment_post_ID = ?", f.PostID)
	}
	if len(f.Statuses) > 0 {
		q = q.Where("comment_approved IN (?)", bun.In(f.Statuses))
	}
	return q.Count(ctx)
}

func (r *CommentRepo) ByID(ctx context.Context, id int64) (domain.Comment, error) {
	var row commentRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"comments")).
		Column(commentColumns...).
		Where("comment_ID = ?", id).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Comment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Comment{}, err
	}
	return row.toDomain(), nil
}

func (r *CommentRepo) Create(ctx context.Context, c domain.Comment) (int64, error) {
	cols := []string{
		"comment_post_ID", "comment_author", "comment_author_email", "comment_author_url",
		"comment_author_IP", "comment_date", "comment_date_gmt", "comment_content",
		"comment_approved", "comment_agent", "comment_parent", "user_id",
	}
	args := []any{
		c.PostID, c.Author, c.AuthorEmail, c.AuthorURL,
		c.AuthorIP, formatTS(c.Date), formatTS(c.DateGMT), c.Content,
		c.Status, c.Agent, c.Parent, c.UserID,
	}
	return insertReturningID(ctx, r.db, vendorOf(r.db), r.prefix+"comments", cols, `"comment_ID"`, args...)
}

func (r *CommentRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"comments")).
		Set("comment_approved = ?", status).
		Where("comment_ID = ?", id).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}

type CommentMetaRepo struct {
	db     *bun.DB
	prefix string
}

func NewCommentMetaRepo(db *bun.DB, prefix string) *CommentMetaRepo {
	return &CommentMetaRepo{db: db, prefix: prefix}
}

func (r *CommentMetaRepo) Get(ctx context.Context, commentID int64, key string) (string, error) {
	var value string
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"commentmeta")).
		Column("meta_value").
		Where("comment_id = ?", commentID).
		Where("meta_key = ?", key).
		OrderExpr("meta_id DESC").
		Limit(1).
		Scan(ctx, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func (r *CommentMetaRepo) Set(ctx context.Context, commentID int64, key, value string) error {
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"commentmeta")).
		Set("meta_value = ?", value).
		Where("comment_id = ?", commentID).
		Where("meta_key = ?", key).
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
	q := "INSERT INTO " + r.prefix + "commentmeta (comment_id, meta_key, meta_value) VALUES (?, ?, ?)"
	_, err = r.db.ExecContext(ctx, rebind.Rebind(vendorOf(r.db), q), commentID, key, value)
	return err
}

func (r *CommentMetaRepo) ByComment(ctx context.Context, commentID int64) (map[string]string, error) {
	var rows []struct {
		Key   sql.NullString `bun:"meta_key"`
		Value sql.NullString `bun:"meta_value"`
	}
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"commentmeta")).
		Column("meta_key", "meta_value").
		Where("comment_id = ?", commentID).
		OrderExpr("meta_id ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		if !row.Key.Valid {
			continue
		}
		out[row.Key.String] = row.Value.String
	}
	return out, nil
}

func (r *CommentMetaRepo) Delete(ctx context.Context, commentID int64, key string) error {
	res, err := r.db.NewDelete().
		TableExpr("?", bun.Ident(r.prefix+"commentmeta")).
		Where("comment_id = ?", commentID).
		Where("meta_key = ?", key).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}
