package wprepo

import (
	"context"
	"sort"
	"strconv"

	"github.com/uptrace/bun"

	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// matchMenuItemsTx returns the ids of the nav_menu_item rows that point at
// (postID, objectType) as a post_type item. Callers de-duplicate and sort.
func matchMenuItemsTx(ctx context.Context, tx bun.Tx, prefix, vendor string, postID int64, objectType string) ([]int64, error) {
	p := prefix
	pID := rebind.Ident(vendor, "ID")
	matchQ := "SELECT p." + pID + " FROM " + p + "posts p " +
		"JOIN " + p + "postmeta o ON o.post_id = p." + pID + " AND o.meta_key = '_menu_item_object_id' AND o.meta_value = ? " +
		"JOIN " + p + "postmeta t ON t.post_id = p." + pID + " AND t.meta_key = '_menu_item_type' AND t.meta_value = 'post_type' " +
		"JOIN " + p + "postmeta j ON j.post_id = p." + pID + " AND j.meta_key = '_menu_item_object' AND j.meta_value = ? " +
		"WHERE p.post_type = 'nav_menu_item'"
	return scanInt64s(ctx, tx, matchQ, strconv.FormatInt(postID, 10), objectType)
}

// sortedUniqueInt64 returns ids de-duplicated and sorted ascending.
func sortedUniqueInt64(ids []int64) []int64 {
	out := dedupInt64(ids)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// deleteMenuItemTx deletes one menu item inside tx and returns the
// term_taxonomy_ids its relationships belonged to, so the caller can recount
// them after every item is gone. Children of the item are first re-pointed to
// the item's own menu parent.
func deleteMenuItemTx(ctx context.Context, tx bun.Tx, prefix, vendor string, itemID int64) ([]int64, error) {
	p := prefix
	pID := rebind.Ident(vendor, "ID")

	newParent, err := menuItemParentTx(ctx, tx, p, itemID)
	if err != nil {
		return nil, err
	}
	if err := reparentChildrenTx(ctx, tx, p, vendor, itemID, newParent); err != nil {
		return nil, err
	}
	ttIDs, err := scanInt64s(ctx, tx,
		"SELECT term_taxonomy_id FROM "+p+"term_relationships WHERE object_id = ?", itemID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+p+"postmeta WHERE post_id = ?", itemID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+p+"term_relationships WHERE object_id = ?", itemID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+p+"posts WHERE "+pID+" = ?", itemID); err != nil {
		return nil, err
	}
	return ttIDs, nil
}

// menuItemParentTx reads itemID's menu parent. Absent, non-integer, negative or
// self-referencing values give 0.
func menuItemParentTx(ctx context.Context, tx bun.Tx, p string, itemID int64) (int64, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT meta_value FROM "+p+"postmeta WHERE post_id = ? AND meta_key = '_menu_item_menu_item_parent' ORDER BY meta_id LIMIT 1",
		itemID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, rows.Err()
	}
	var raw *string
	if err := rows.Scan(&raw); err != nil {
		return 0, err
	}
	if raw == nil {
		return 0, nil
	}
	v, err := strconv.ParseInt(*raw, 10, 64)
	if err != nil || v < 0 || v == itemID {
		return 0, nil
	}
	return v, nil
}

// reparentChildrenTx sets _menu_item_menu_item_parent of the nav_menu_item
// children of itemID to newParent.
func reparentChildrenTx(ctx context.Context, tx bun.Tx, prefix, vendor string, itemID, newParent int64) error {
	_, err := tx.ExecContext(ctx,
		"UPDATE "+prefix+"postmeta SET meta_value = ? "+
			"WHERE meta_key = '_menu_item_menu_item_parent' AND meta_value = ? "+
			"AND post_id IN (SELECT "+rebind.Ident(vendor, "ID")+" FROM "+prefix+"posts WHERE post_type = 'nav_menu_item')",
		strconv.FormatInt(newParent, 10), strconv.FormatInt(itemID, 10))
	return err
}

// recountTx sets term_taxonomy.count to the relationship count for each
// distinct id.
func recountTx(ctx context.Context, tx bun.Tx, prefix string, ttIDs []int64) error {
	for _, tt := range dedupInt64(ttIDs) {
		if _, err := tx.ExecContext(ctx,
			"UPDATE "+prefix+"term_taxonomy SET count = "+
				"(SELECT COUNT(*) FROM "+prefix+"term_relationships WHERE term_taxonomy_id = ?) "+
				"WHERE term_taxonomy_id = ?", tt, tt); err != nil {
			return err
		}
	}
	return nil
}
