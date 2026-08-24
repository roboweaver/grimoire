// Package wprepo implements the domain repository ports over a WordPress-shaped
// schema using the Bun query builder. A single implementation serves every
// SQL vendor; each vendor package supplies only the driver + dialect wiring and
// the table prefix.
package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

// postColumns are the posts columns selected into a postRow, in WP order.
// post_date_gmt, post_modified, post_modified_gmt, ping_status,
// post_password, and guid come from the 0004 migration.
var postColumns = []string{
	"ID", "post_author", "post_date", "post_content",
	"post_title", "post_excerpt", "post_status", "post_name", "post_type", "comment_status",
	"post_date_gmt", "post_modified", "post_modified_gmt", "ping_status", "post_password", "guid",
}

type postRow struct {
	ID            int64     `bun:"ID"`
	Author        int64     `bun:"post_author"`
	Date          time.Time `bun:"post_date"`
	Content       string    `bun:"post_content"`
	Title         string    `bun:"post_title"`
	Excerpt       string    `bun:"post_excerpt"`
	Status        string    `bun:"post_status"`
	Slug          string    `bun:"post_name"`
	Type          string    `bun:"post_type"`
	CommentStatus string    `bun:"comment_status"`
	DateGMT       time.Time `bun:"post_date_gmt"`
	Modified      time.Time `bun:"post_modified"`
	ModifiedGMT   time.Time `bun:"post_modified_gmt"`
	PingStatus    string    `bun:"ping_status"`
	Password      string    `bun:"post_password"`
	GUID          string    `bun:"guid"`
}

func (r postRow) toDomain() domain.Post {
	return domain.Post{
		ID:            r.ID,
		Author:        r.Author,
		Date:          r.Date,
		Content:       r.Content,
		Title:         r.Title,
		Excerpt:       r.Excerpt,
		Status:        r.Status,
		Slug:          r.Slug,
		Type:          r.Type,
		CommentStatus: r.CommentStatus,
		DateGMT:       r.DateGMT,
		Modified:      r.Modified,
		ModifiedGMT:   r.ModifiedGMT,
		PingStatus:    r.PingStatus,
		Password:      r.Password,
		GUID:          r.GUID,
	}
}

func toDomainPosts(rows []postRow) []domain.Post {
	posts := make([]domain.Post, len(rows))
	for i, r := range rows {
		posts[i] = r.toDomain()
	}
	return posts
}

// PostRepo reads posts and pages.
type PostRepo struct {
	db     *bun.DB
	prefix string
}

// NewPostRepo builds a PostRepo bound to db and the table prefix.
func NewPostRepo(db *bun.DB, prefix string) *PostRepo { return &PostRepo{db: db, prefix: prefix} }

// RecentPosts returns published posts (post_type "post") newest first.
func (r *PostRepo) RecentPosts(ctx context.Context, limit, offset int) ([]domain.Post, error) {
	var rows []postRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("post_status = ?", "publish").
		Where("post_type = ?", "post").
		OrderExpr("post_date DESC, ? DESC", bun.Ident("ID")).
		Limit(limit).
		Offset(offset).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	return toDomainPosts(rows), nil
}

// BySlug returns a single published post/page by slug. When types is empty it
// defaults to {"post", "page"}.
func (r *PostRepo) BySlug(ctx context.Context, slug string, types ...string) (domain.Post, error) {
	if len(types) == 0 {
		types = []string{"post", "page"}
	}
	var row postRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("post_status = ?", "publish").
		Where("post_name = ?", slug).
		Where("post_type IN (?)", bun.In(types)).
		OrderExpr("post_date DESC").
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

// ByTermSlug returns published posts related to a taxonomy term, newest first.
func (r *PostRepo) ByTermSlug(ctx context.Context, taxonomy, termSlug string, limit, offset int) ([]domain.Post, error) {
	var rows []postRow
	err := r.db.NewSelect().
		TableExpr("? AS p", bun.Ident(r.prefix+"posts")).
		ColumnExpr("p.?", bun.Ident("ID")).
		ColumnExpr("p.post_author, p.post_date, p.post_content, p.post_title, p.post_excerpt, p.post_status, p.post_name, p.post_type, p.comment_status").
		ColumnExpr("p.post_date_gmt, p.post_modified, p.post_modified_gmt, p.ping_status, p.post_password, p.guid").
		Join("JOIN ? AS tr ON tr.object_id = p.?", bun.Ident(r.prefix+"term_relationships"), bun.Ident("ID")).
		Join("JOIN ? AS tt ON tt.term_taxonomy_id = tr.term_taxonomy_id", bun.Ident(r.prefix+"term_taxonomy")).
		Join("JOIN ? AS t ON t.term_id = tt.term_id", bun.Ident(r.prefix+"terms")).
		Where("tt.taxonomy = ?", taxonomy).
		Where("t.slug = ?", termSlug).
		Where("p.post_status = ?", "publish").
		OrderExpr("p.post_date DESC, p.? DESC", bun.Ident("ID")).
		Limit(limit).
		Offset(offset).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	return toDomainPosts(rows), nil
}

// TermRepo resolves taxonomy terms.
type TermRepo struct {
	db     *bun.DB
	prefix string
}

// NewTermRepo builds a TermRepo bound to db and the table prefix.
func NewTermRepo(db *bun.DB, prefix string) *TermRepo { return &TermRepo{db: db, prefix: prefix} }

type termRow struct {
	ID       int64  `bun:"term_id"`
	Name     string `bun:"name"`
	Slug     string `bun:"slug"`
	Taxonomy string `bun:"taxonomy"`
}

// BySlug returns the term for a taxonomy/slug pair, or ErrNotFound.
func (r *TermRepo) BySlug(ctx context.Context, taxonomy, slug string) (domain.Term, error) {
	var row termRow
	err := r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		ColumnExpr("t.term_id, t.name, t.slug, tt.taxonomy").
		Join("JOIN ? AS tt ON tt.term_id = t.term_id", bun.Ident(r.prefix+"term_taxonomy")).
		Where("tt.taxonomy = ?", taxonomy).
		Where("t.slug = ?", slug).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Term{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Term{}, err
	}
	return domain.Term{ID: row.ID, Name: row.Name, Slug: row.Slug, Taxonomy: row.Taxonomy}, nil
}

// OptionRepo reads site options.
type OptionRepo struct {
	db     *bun.DB
	prefix string
}

// NewOptionRepo builds an OptionRepo bound to db and the table prefix.
func NewOptionRepo(db *bun.DB, prefix string) *OptionRepo { return &OptionRepo{db: db, prefix: prefix} }

// Get returns an option value by name, or ErrNotFound.
func (r *OptionRepo) Get(ctx context.Context, name string) (string, error) {
	var value string
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"options")).
		Column("option_value").
		Where("option_name = ?", name).
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
