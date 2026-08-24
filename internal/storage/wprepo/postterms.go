package wprepo

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

var _ domain.PostTermsRepository = (*PostTermsRepo)(nil)

// PostTermsRepo resolves the taxonomy terms related to a post.
type PostTermsRepo struct {
	db     *bun.DB
	prefix string
}

// NewPostTermsRepo builds a PostTermsRepo bound to db and the table prefix.
func NewPostTermsRepo(db *bun.DB, prefix string) *PostTermsRepo {
	return &PostTermsRepo{db: db, prefix: prefix}
}

// TermsForPost returns the term IDs related to postID under taxonomy, in
// name-ascending order (not insertion order) so REST responses are
// deterministic across vendors. A post with no matching terms, or a
// nonexistent post ID, returns an empty slice and a nil error.
func (r *PostTermsRepo) TermsForPost(ctx context.Context, postID int64, taxonomy string) ([]int64, error) {
	var ids []int64
	err := r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		ColumnExpr("t.term_id").
		Join("JOIN ? AS tt ON tt.term_id = t.term_id", bun.Ident(r.prefix+"term_taxonomy")).
		Join("JOIN ? AS tr ON tr.term_taxonomy_id = tt.term_taxonomy_id", bun.Ident(r.prefix+"term_relationships")).
		Where("tr.object_id = ?", postID).
		Where("tt.taxonomy = ?", taxonomy).
		OrderExpr("t.name ASC, t.term_id ASC").
		Scan(ctx, &ids)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []int64{}
	}
	return ids, nil
}
