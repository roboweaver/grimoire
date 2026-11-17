package wprepo

import (
	"context"

	"github.com/uptrace/bun"
)

// PurgeMenuItemsOf implements domain.PostMenuItemPurger. One transaction: find
// the nav_menu_item rows that point at (postID, objectType), delete each item's
// postmeta, term_relationships and post row (children are re-pointed to the
// deleted item's menu parent), then recompute term_taxonomy.count for every
// taxonomy the items belonged to.
func (r *PostRepo) PurgeMenuItemsOf(ctx context.Context, postID int64, objectType string) error {
	vendor := vendorOf(r.db)
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ids, err := matchMenuItemsTx(ctx, tx, r.prefix, vendor, postID, objectType)
		if err != nil {
			return err
		}
		var ttIDs []int64
		for _, id := range sortedUniqueInt64(ids) {
			tts, err := deleteMenuItemTx(ctx, tx, r.prefix, vendor, id)
			if err != nil {
				return err
			}
			ttIDs = append(ttIDs, tts...)
		}
		return recountTx(ctx, tx, r.prefix, ttIDs)
	})
}

// scanInt64s runs a single-column query and returns every value.
func scanInt64s(ctx context.Context, tx bun.Tx, q string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// dedupInt64 returns ids without repeats, keeping first-seen order.
func dedupInt64(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
