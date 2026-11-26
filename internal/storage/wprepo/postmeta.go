package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

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

// BackupSizes returns the raw (PHP-serialized) value of an attachment post's
// "_wp_attachment_backup_sizes" postmeta, or "" if unset. When the key is
// duplicated the row with the lowest meta_id wins (WordPress's get_post_meta
// single-value behavior), unlike get, which prefers the newest row.
func (r *PostMetaRepo) BackupSizes(ctx context.Context, postID int64) (string, error) {
	var value string
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"postmeta")).
		Column("meta_value").
		Where("post_id = ?", postID).
		Where("meta_key = ?", "_wp_attachment_backup_sizes").
		OrderExpr("meta_id ASC").
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

// likeEscaper escapes the LIKE metacharacters with '!' as the escape
// character. Backslash is deliberately not used: MySQL treats it specially in
// string literals and in ESCAPE, while '!' means the same on SQLite, MySQL and
// PostgreSQL.
var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

// ThumbSharedElsewhere reports whether any attachment other than excludeID
// names filename as the "thumb" entry of its _wp_attachment_metadata. It
// matches the length-prefixed serialized entry (s:5:"thumb";s:N:"name";) so a
// filename that is a substring of another is not a match, and LIKE wildcards in
// filename are compared literally. LIKE only narrows the candidate rows; each
// is then confirmed in Go so a case-insensitive collation (MySQL default)
// cannot produce a false positive. An empty filename is never shared.
func (r *PostMetaRepo) ThumbSharedElsewhere(ctx context.Context, excludeID int64, filename string) (bool, error) {
	if filename == "" {
		return false, nil
	}
	entry := `s:5:"thumb";s:` + strconv.Itoa(len(filename)) + `:"` + filename + `";`
	var values []string
	err := r.db.NewSelect().
		TableExpr("? AS pm", bun.Ident(r.prefix+"postmeta")).
		Column("pm.meta_value").
		Join("JOIN ? AS p ON p.? = pm.post_id", bun.Ident(r.prefix+"posts"), bun.Ident("ID")).
		Where("pm.meta_key = ?", "_wp_attachment_metadata").
		Where("p.post_type = ?", "attachment").
		Where("pm.post_id <> ?", excludeID).
		Where("pm.meta_value LIKE ? ESCAPE '!'", "%"+likeEscaper.Replace(entry)+"%").
		Scan(ctx, &values)
	if err != nil {
		return false, err
	}
	for _, v := range values {
		if strings.Contains(v, entry) {
			return true, nil
		}
	}
	return false, nil
}
