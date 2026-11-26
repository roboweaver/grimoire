package wprepo

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

// TrashedPosts implements domain.TrashedPostLister. The trash time is read with
// one postmeta query per page and parsed in Go, because casting text to an
// integer in SQL errors on PostgreSQL for a non-numeric value.
func (r *PostRepo) TrashedPosts(ctx context.Context, types []string, afterID int64, limit int) ([]domain.TrashedPost, error) {
	if len(types) == 0 || limit <= 0 {
		return nil, nil
	}
	var rows []postRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("? = ?", bun.Ident("post_status"), "trash").
		Where("? IN (?)", bun.Ident("post_type"), bun.In(types)).
		Where("? > ?", bun.Ident("ID"), afterID).
		OrderExpr("? ASC", bun.Ident("ID")).
		Limit(limit).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.toDomain().ID
	}
	times, err := r.trashTimes(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TrashedPost, 0, len(rows))
	for _, row := range rows {
		p := row.toDomain()
		tp := domain.TrashedPost{Post: p}
		if raw, ok := times[p.ID]; ok {
			if secs, perr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); perr == nil {
				tp.TrashedAt = time.Unix(secs, 0).UTC()
				tp.HasTrashedAt = true
			}
		}
		out = append(out, tp)
	}
	return out, nil
}

// trashTimes returns the raw _wp_trash_meta_time value of the lowest meta_id
// row per post id.
func (r *PostRepo) trashTimes(ctx context.Context, ids []int64) (map[int64]string, error) {
	rs, err := r.db.QueryContext(ctx,
		"SELECT post_id, meta_value FROM ? WHERE meta_key = ? AND post_id IN (?) ORDER BY meta_id",
		bun.Ident(r.prefix+"postmeta"), metaTrashTime, bun.In(ids))
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	times := make(map[int64]string, len(ids))
	for rs.Next() {
		var id int64
		var v sql.NullString
		if err := rs.Scan(&id, &v); err != nil {
			return nil, err
		}
		if _, seen := times[id]; !seen {
			times[id] = v.String
		}
	}
	return times, rs.Err()
}
