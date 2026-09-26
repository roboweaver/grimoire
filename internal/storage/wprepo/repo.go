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
	"post_parent",
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
	ParentID      int64     `bun:"post_parent"`
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
		ParentID:      r.ParentID,
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

// PublishedByID returns a single published post/page by primary key. When types
// is empty it defaults to {"post", "page"}.
//
// Mirrors BySlug's filters exactly, including post_status = 'publish'. That
// status filter is the point of the method: it backs the %post_id% permalink
// token, so the id arrives from a visitor's URL and an unfiltered lookup would
// serve drafts, private and trashed rows to anonymous callers.
//
// Deliberately NOT named ByID: this same type also implements PostWriter.ByID,
// which is status-blind by design for the editor. Two same-named lookups with
// opposite disclosure properties would be a trap.
func (r *PostRepo) PublishedByID(ctx context.Context, id int64, types ...string) (domain.Post, error) {
	if len(types) == 0 {
		types = []string{"post", "page"}
	}
	// Guard before querying: WordPress ids start at 1, so a non-positive id can
	// never match and should not become a database round trip.
	if id <= 0 {
		return domain.Post{}, domain.ErrNotFound
	}
	var row postRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("post_status = ?", "publish").
		Where("? = ?", bun.Ident("ID"), id).
		Where("post_type IN (?)", bun.In(types)).
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

// archiveEmptyTermSet reports whether f names a taxonomy but no terms, which
// means "no terms" and matches nothing rather than being unfiltered
// (domain.ArchiveFilter's field comment, Req 3.1). Both reads short-circuit on
// it instead of issuing a query: `IN ()` is not valid SQL on any of the three
// vendors, and degrading an empty descendant set to "unfiltered" would serve the
// whole site at a category URL.
func archiveEmptyTermSet(f domain.ArchiveFilter) bool {
	return f.Taxonomy != "" && len(f.TermIDs) == 0
}

// archiveWhere applies every domain.ArchiveFilter predicate, identically for the
// listing and the count, so the two can never describe different sets (Req 3.2).
//
// post_status = 'publish' is applied here unconditionally and is not derived
// from f: ArchiveFilter carries no status field, so no caller can turn it off
// (Req 3.4). An empty f.Types defaults to {"post"}, matching RecentPosts and
// WordPress's own archive queries, so a published page filed under a category
// does not leak into that category's archive (Req 3.5).
//
// The term predicate is an EXISTS semi-join rather than a join: once the
// predicate is a term *set* (a category plus its descendants), a post filed
// under both a parent and a child matches twice, and a join would list it twice
// and count it twice, breaking M8's pagination totals. EXISTS yields one row per
// post by construction, so no DISTINCT over the LONGTEXT post_content column is
// needed (Req 3.3).
//
// The date bounds are the half-open range [Start, End) formatted by formatTS —
// the same formatting route mediaWhere takes, deliberately not the same
// semantics (MediaFilter.Before is an inclusive day-end), which is why these
// fields are named Start/End. No YEAR()/EXTRACT()/strftime(), so one statement
// serves MySQL, PostgreSQL and SQLite (Req 6.4).
//
// Every reference to the mixed-case ID column goes through bun.Ident("ID"):
// PostgreSQL declares it as "ID", so a bare p.ID folds to p.id and fails on that
// vendor alone.
func (r *PostRepo) archiveWhere(q *bun.SelectQuery, f domain.ArchiveFilter) *bun.SelectQuery {
	types := f.Types
	if len(types) == 0 {
		types = []string{"post"}
	}
	q = q.Where("p.post_status = ?", "publish").
		Where("p.post_type IN (?)", bun.In(types))

	if len(f.TermIDs) > 0 {
		sub := r.db.NewSelect().
			ColumnExpr("1").
			TableExpr("? AS tr", bun.Ident(r.prefix+"term_relationships")).
			Join("JOIN ? AS tt ON tt.term_taxonomy_id = tr.term_taxonomy_id", bun.Ident(r.prefix+"term_taxonomy")).
			Where("tr.object_id = p.?", bun.Ident("ID")).
			Where("tt.term_id IN (?)", bun.In(f.TermIDs))
		// Taxonomy narrows the ids to one taxonomy. It is applied only when
		// set, so term ids supplied without a taxonomy still filter rather than
		// being silently dropped; the taxonomy-without-ids direction is handled
		// by archiveEmptyTermSet.
		if f.Taxonomy != "" {
			sub = sub.Where("tt.taxonomy = ?", f.Taxonomy)
		}
		q = q.Where("EXISTS (?)", sub)
	}

	if f.AuthorID != 0 {
		q = q.Where("p.post_author = ?", f.AuthorID)
	}
	if !f.Start.IsZero() {
		q = q.Where("p.post_date >= ?", formatTS(f.Start))
	}
	if !f.End.IsZero() {
		q = q.Where("p.post_date < ?", formatTS(f.End))
	}
	return q
}

// PublishedArchive returns published posts matching f, newest first, for one
// page of an archive listing (Req 3.1, 3.3, 6.4).
func (r *PostRepo) PublishedArchive(ctx context.Context, f domain.ArchiveFilter, limit, offset int) ([]domain.Post, error) {
	if archiveEmptyTermSet(f) {
		return []domain.Post{}, nil
	}
	q := r.db.NewSelect().
		TableExpr("? AS p", bun.Ident(r.prefix+"posts")).
		ColumnExpr("p.?", bun.Ident("ID")).
		ColumnExpr("p.post_author, p.post_date, p.post_content, p.post_title, p.post_excerpt, p.post_status, p.post_name, p.post_type, p.comment_status").
		ColumnExpr("p.post_date_gmt, p.post_modified, p.post_modified_gmt, p.ping_status, p.post_password, p.guid, p.post_parent").
		OrderExpr("p.post_date DESC, p.? DESC", bun.Ident("ID"))
	q = r.archiveWhere(q, f)
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	var rows []postRow
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return toDomainPosts(rows), nil
}

// CountPublishedArchive counts the posts PublishedArchive lists, under the
// identical predicate, so the total and the listing cannot disagree (Req 3.2,
// 3.3).
func (r *PostRepo) CountPublishedArchive(ctx context.Context, f domain.ArchiveFilter) (int, error) {
	if archiveEmptyTermSet(f) {
		return 0, nil
	}
	q := r.db.NewSelect().TableExpr("? AS p", bun.Ident(r.prefix+"posts"))
	return r.archiveWhere(q, f).Count(ctx)
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
	ParentID int64  `bun:"parent"`
}

// BySlug returns the term for a taxonomy/slug pair, or ErrNotFound.
func (r *TermRepo) BySlug(ctx context.Context, taxonomy, slug string) (domain.Term, error) {
	var row termRow
	err := r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		ColumnExpr("t.term_id, t.name, t.slug, tt.taxonomy, tt.parent").
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
	return domain.Term{ID: row.ID, Name: row.Name, Slug: row.Slug, Taxonomy: row.Taxonomy, ParentID: row.ParentID}, nil
}

// ListByTaxonomy returns every term of the given taxonomy, ordered by name.
func (r *TermRepo) ListByTaxonomy(ctx context.Context, taxonomy string) ([]domain.Term, error) {
	var rows []termRow
	err := r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		ColumnExpr("t.term_id, t.name, t.slug, tt.taxonomy, tt.parent").
		Join("JOIN ? AS tt ON tt.term_id = t.term_id", bun.Ident(r.prefix+"term_taxonomy")).
		Where("tt.taxonomy = ?", taxonomy).
		OrderExpr("t.name ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	terms := make([]domain.Term, len(rows))
	for i, row := range rows {
		terms[i] = domain.Term{ID: row.ID, Name: row.Name, Slug: row.Slug, Taxonomy: row.Taxonomy, ParentID: row.ParentID}
	}
	return terms, nil
}

// TermsByIDs bulk-resolves term IDs to full Term objects. Unknown IDs are
// silently omitted; an empty ids slice returns an empty result.
//
// ParentID is populated, but it is NOT unambiguous here: the join is on
// term_id alone, while term_taxonomy is unique on (term_id, taxonomy), so a
// term_id registered in both category and post_tag yields two rows with
// different taxonomy and different parent. ParentID is a property of the
// (term_id, taxonomy) pair and this signature cannot express which pair the
// caller means. Use ListByTaxonomy when the parent matters.
func (r *TermRepo) TermsByIDs(ctx context.Context, ids []int64) ([]domain.Term, error) {
	if len(ids) == 0 {
		return []domain.Term{}, nil
	}
	var rows []termRow
	err := r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		ColumnExpr("t.term_id, t.name, t.slug, tt.taxonomy, tt.parent").
		Join("JOIN ? AS tt ON tt.term_id = t.term_id", bun.Ident(r.prefix+"term_taxonomy")).
		Where("t.term_id IN (?)", bun.In(ids)).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	terms := make([]domain.Term, len(rows))
	for i, row := range rows {
		terms[i] = domain.Term{ID: row.ID, Name: row.Name, Slug: row.Slug, Taxonomy: row.Taxonomy, ParentID: row.ParentID}
	}
	return terms, nil
}

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
