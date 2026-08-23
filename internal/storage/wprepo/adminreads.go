package wprepo

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

// compile-time interface checks for the M3 additive read/count ports.
var (
	_ domain.AdminPostRepository = (*PostRepo)(nil)
	_ domain.PostCounter         = (*PostRepo)(nil)
	_ domain.UserCounter         = (*UserRepo)(nil)
	_ domain.TermCounter         = (*TermRepo)(nil)
)

// adminTypes resolves the effective post_type set for an admin query. An empty
// filter defaults to both posts and pages, matching the public read path's
// BySlug default while allowing any status.
func adminTypes(f domain.AdminPostFilter) []string {
	if len(f.Types) == 0 {
		return []string{"post", "page"}
	}
	return f.Types
}

// ListForAdmin returns posts matching the filter ordered newest first
// (post_date DESC, ID DESC). Unlike RecentPosts it does not constrain
// post_status, so drafts and pages are included. Pure SELECT; no writes.
func (r *PostRepo) ListForAdmin(ctx context.Context, f domain.AdminPostFilter) ([]domain.Post, error) {
	var rows []postRow
	q := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("post_type IN (?)", bun.In(adminTypes(f)))
	if len(f.Statuses) > 0 {
		q = q.Where("post_status IN (?)", bun.In(f.Statuses))
	}
	q = q.OrderExpr("post_date DESC, ? DESC", bun.Ident("ID"))
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	if f.Offset > 0 {
		q = q.Offset(f.Offset)
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return toDomainPosts(rows), nil
}

// CountForAdmin returns the number of posts matching the filter, ignoring
// Limit/Offset (used for pagination totals). Pure COUNT(*).
func (r *PostRepo) CountForAdmin(ctx context.Context, f domain.AdminPostFilter) (int, error) {
	q := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Where("post_type IN (?)", bun.In(adminTypes(f)))
	if len(f.Statuses) > 0 {
		q = q.Where("post_status IN (?)", bun.In(f.Statuses))
	}
	return q.Count(ctx)
}

// CountByStatus counts posts of the given post_type and post_status. An empty
// typ matches any type; an empty status matches any status. Pure COUNT(*).
func (r *PostRepo) CountByStatus(ctx context.Context, typ, status string) (int, error) {
	q := r.db.NewSelect().TableExpr("?", bun.Ident(r.prefix+"posts"))
	if typ != "" {
		q = q.Where("post_type = ?", typ)
	}
	if status != "" {
		q = q.Where("post_status = ?", status)
	}
	return q.Count(ctx)
}

// CountUsers returns the number of rows in {prefix}users. Pure COUNT(*).
func (r *UserRepo) CountUsers(ctx context.Context) (int, error) {
	return r.db.NewSelect().TableExpr("?", bun.Ident(r.prefix+"users")).Count(ctx)
}

// CountTerms returns the number of terms in the given taxonomy (e.g.
// "category"). It joins terms to term_taxonomy so the count reflects the
// taxonomy, matching how WordPress scopes a term to a taxonomy. Pure COUNT(*).
func (r *TermRepo) CountTerms(ctx context.Context, taxonomy string) (int, error) {
	return r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		Join("JOIN ? AS tt ON tt.term_id = t.term_id", bun.Ident(r.prefix+"term_taxonomy")).
		Where("tt.taxonomy = ?", taxonomy).
		Count(ctx)
}
