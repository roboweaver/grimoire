package wprepo

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

// DistinctPostTypes returns every post_type with its row count across all
// post_status values, ordered by name. Names and counts only; no content
// column is read (M12 Req 11.1, 14.1; D9). Pure SELECT.
func (r *PostRepo) DistinctPostTypes(ctx context.Context) ([]domain.TypeCount, error) {
	var rows []struct {
		Name  string `bun:"name"`
		Count int    `bun:"cnt"`
	}
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		ColumnExpr("post_type AS name, COUNT(*) AS cnt").
		GroupExpr("post_type").
		OrderExpr("post_type ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TypeCount, len(rows))
	for i, row := range rows {
		out[i] = domain.TypeCount{Name: row.Name, Count: row.Count}
	}
	return out, nil
}

// DistinctTaxonomies returns every term_taxonomy.taxonomy with its row count,
// ordered by name (M12 Req 11.2, 14.1; D9). Pure SELECT.
func (r *TermRepo) DistinctTaxonomies(ctx context.Context) ([]domain.TypeCount, error) {
	var rows []struct {
		Name  string `bun:"name"`
		Count int    `bun:"cnt"`
	}
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"term_taxonomy")).
		ColumnExpr("taxonomy AS name, COUNT(*) AS cnt").
		GroupExpr("taxonomy").
		OrderExpr("taxonomy ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TypeCount, len(rows))
	for i, row := range rows {
		out[i] = domain.TypeCount{Name: row.Name, Count: row.Count}
	}
	return out, nil
}
