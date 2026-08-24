package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

var _ domain.PostMetaRepository = (*PostMetaRepo)(nil)

// PostMetaRepo reads REST-relevant single-valued postmeta.
type PostMetaRepo struct {
	db     *bun.DB
	prefix string
}

// NewPostMetaRepo builds a PostMetaRepo bound to db and the table prefix.
func NewPostMetaRepo(db *bun.DB, prefix string) *PostMetaRepo {
	return &PostMetaRepo{db: db, prefix: prefix}
}

func (r *PostMetaRepo) get(ctx context.Context, postID int64, key string) (string, error) {
	var value string
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"postmeta")).
		Column("meta_value").
		Where("post_id = ?", postID).
		Where("meta_key = ?", key).
		OrderExpr("meta_id DESC").
		Limit(1).
		Scan(ctx, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// FeaturedMediaID returns the post's featured image attachment ID from its
// "_thumbnail_id" postmeta value, or 0 if unset or unparsable.
func (r *PostMetaRepo) FeaturedMediaID(ctx context.Context, postID int64) (int64, error) {
	raw, err := r.get(ctx, postID, "_thumbnail_id")
	if err != nil {
		return 0, err
	}
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, nil
	}
	return id, nil
}

// AttachmentMetadata returns the raw (PHP-serialized) value of an attachment
// post's "_wp_attachment_metadata" postmeta, or "" if unset.
func (r *PostMetaRepo) AttachmentMetadata(ctx context.Context, postID int64) (string, error) {
	return r.get(ctx, postID, "_wp_attachment_metadata")
}
