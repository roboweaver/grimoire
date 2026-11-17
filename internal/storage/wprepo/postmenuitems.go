package wprepo

import (
	"context"
	"strconv"

	"github.com/uptrace/bun"

	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// PurgeMenuItemsOf implements domain.PostMenuItemPurger. One transaction: find
// the nav_menu_item rows that point at (postID, objectType), delete each item's
// postmeta, term_relationships and post row, then recompute term_taxonomy.count
// for every nav_menu taxonomy the items belonged to. Other items'
// _menu_item_menu_item_parent values are left alone, as WordPress leaves them.
func (r *PostRepo) PurgeMenuItemsOf(ctx context.Context, postID int64, objectType string) error {
	p := r.prefix
	pID := rebind.Ident(vendorOf(r.db), "ID")

	matchQ := "SELECT p." + pID + " FROM " + p + "posts p " +
		"JOIN " + p + "postmeta o ON o.post_id = p." + pID + " AND o.meta_key = '_menu_item_object_id' AND o.meta_value = ? " +
		"JOIN " + p + "postmeta t ON t.post_id = p." + pID + " AND t.meta_key = '_menu_item_type' AND t.meta_value = 'post_type' " +
		"JOIN " + p + "postmeta j ON j.post_id = p." + pID + " AND j.meta_key = '_menu_item_object' AND j.meta_value = ? " +
		"WHERE p.post_type = 'nav_menu_item'"
	ttQ := "SELECT tr.term_taxonomy_id FROM " + p + "term_relationships tr " +
		"JOIN " + p + "term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id " +
		"WHERE tr.object_id = ? AND tt.taxonomy = 'nav_menu'"

	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ids, err := scanInt64s(ctx, tx, matchQ, strconv.FormatInt(postID, 10), objectType)
		if err != nil {
			return err
		}
		ids = dedupInt64(ids)

		var ttIDs []int64
		for _, id := range ids {
			tts, err := scanInt64s(ctx, tx, ttQ, id)
			if err != nil {
				return err
			}
			ttIDs = append(ttIDs, tts...)
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+p+"postmeta WHERE post_id = ?", id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+p+"term_relationships WHERE object_id = ?", id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+p+"posts WHERE "+pID+" = ?", id); err != nil {
				return err
			}
		}

		for _, tt := range dedupInt64(ttIDs) {
			if _, err := tx.ExecContext(ctx,
				"UPDATE "+p+"term_taxonomy SET count = "+
					"(SELECT COUNT(*) FROM "+p+"term_relationships WHERE term_taxonomy_id = ?) "+
					"WHERE term_taxonomy_id = ?", tt, tt); err != nil {
				return err
			}
		}
		return nil
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
