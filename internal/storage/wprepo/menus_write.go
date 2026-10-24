package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
)

// NavMenuRepo is both the read implementation (domain.NavMenuRepository, asserted
// in menus.go) and — via the methods below — the write implementation. The write
// side mirrors, in reverse, the storage mapping menus.go's read path assembles:
// a menu is a {prefix}terms row + a {prefix}term_taxonomy row (taxonomy="nav_menu");
// an item is a {prefix}posts row (post_type="nav_menu_item") linked to the menu
// term through {prefix}term_relationships, with its remaining fields in the five
// _menu_item_* {prefix}postmeta keys. Every method runs in RunInTx and reuses the
// existing write helpers (insertReturningID, upsertPostMeta, errNotFoundIfZero,
// vendorOf, formatTS), matching TermRepo.Create/Delete and PostRepo.Create.
var _ domain.NavMenuWriter = (*NavMenuRepo)(nil)

// CreateMenu inserts one nav_menu term (a {prefix}terms row plus a
// {prefix}term_taxonomy row, taxonomy="nav_menu") and returns its term_id,
// mirroring TermRepo.Create. The slug-uniqueness re-check runs inside the same
// transaction — SELECT by slug under taxonomy="nav_menu", appending -2/-3/… on
// collision — so two concurrent creates cannot both claim one slug even though
// the service has already resolved the slug once (Req 1.1).
func (r *NavMenuRepo) CreateMenu(ctx context.Context, name, slug string) (int64, error) {
	vendor := vendorOf(r.db)
	var termID int64
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		unique, err := r.uniqueMenuSlug(ctx, tx, slug)
		if err != nil {
			return err
		}
		id, err := insertReturningID(ctx, tx, vendor, r.prefix+"terms",
			[]string{"name", "slug"}, "term_id", name, unique)
		if err != nil {
			return err
		}
		termID = id
		q := "INSERT INTO " + r.prefix + "term_taxonomy " +
			"(term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?)"
		_, err = tx.ExecContext(ctx, q, termID, "nav_menu", "", 0, 0)
		return err
	})
	if err != nil {
		return 0, err
	}
	return termID, nil
}

// uniqueMenuSlug returns slug, or slug-2/slug-3/… if an existing nav_menu term
// already carries it, re-checking inside the caller's transaction.
func (r *NavMenuRepo) uniqueMenuSlug(ctx context.Context, tx bun.Tx, slug string) (string, error) {
	q := "SELECT COUNT(*) FROM " + r.prefix + "terms t " +
		"JOIN " + r.prefix + "term_taxonomy tt ON tt.term_id = t.term_id " +
		"WHERE tt.taxonomy = ? AND t.slug = ?"
	candidate := slug
	for n := 2; ; n++ {
		var count int
		if err := tx.QueryRowContext(ctx, q, "nav_menu", candidate).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", slug, n)
	}
}

// UpdateMenu applies a partial update to an existing nav_menu term's name/slug;
// a nil patch field is left unchanged (Req 1.2). It is EXISTS-guarded so it can
// only touch a nav_menu term, returning domain.ErrNotFound when id resolves to
// no such term (Req 1.4). errNotFoundIfMissing (not errNotFoundIfZero) is used
// for the same MySQL RowsAffected caveat TermRepo.Update documents: a no-op
// same-value rename reports 0 rows affected on MySQL and must not 404.
func (r *NavMenuRepo) UpdateMenu(ctx context.Context, id int64, patch domain.NavMenuPatch) error {
	if patch.Name == nil && patch.Slug == nil {
		// Nothing to change; still honor the not-found contract.
		ok, err := r.menuTermExists(ctx, r.db, id)
		if err != nil {
			return err
		}
		if !ok {
			return domain.ErrNotFound
		}
		return nil
	}
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		upd := tx.NewUpdate().
			TableExpr("?", bun.Ident(r.prefix+"terms")).
			Where("term_id = ?", id).
			Where("term_id IN (SELECT term_id FROM ? WHERE taxonomy = ?)",
				bun.Ident(r.prefix+"term_taxonomy"), "nav_menu")
		if patch.Name != nil {
			upd = upd.Set("name = ?", *patch.Name)
		}
		if patch.Slug != nil {
			upd = upd.Set("slug = ?", *patch.Slug)
		}
		res, err := upd.Exec(ctx)
		if err != nil {
			return err
		}
		return errNotFoundIfMissing(ctx, res, func(ctx context.Context) (bool, error) {
			return r.menuTermExists(ctx, tx, id)
		})
	})
}

// menuTermExists reports whether id is a nav_menu term, using the supplied
// querier so it can run inside or outside a transaction.
func (r *NavMenuRepo) menuTermExists(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, id int64) (bool, error) {
	sqlText := "SELECT COUNT(*) FROM " + r.prefix + "term_taxonomy WHERE term_id = ? AND taxonomy = ?"
	var count int
	if err := q.QueryRowContext(ctx, sqlText, id, "nav_menu").Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// DeleteMenu removes the menu term, its term_taxonomy row, every linked
// nav_menu_item post, those items' _menu_item_* postmeta rows, and the
// term_relationships rows for the menu term, all in one transaction, leaving no
// orphan (Req 6.3). A post_type/taxonomy item's referenced (foreign) post or
// term is never touched. Returns domain.ErrNotFound when id is not a nav_menu
// term (Req 1.4).
func (r *NavMenuRepo) DeleteMenu(ctx context.Context, id int64) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		pID := rebind.Ident(vendorOf(r.db), "ID")

		// (1) Collect the item post IDs linked to the menu term.
		collect := "SELECT p." + pID + " FROM " + r.prefix + "posts p " +
			"JOIN " + r.prefix + "term_relationships tr ON tr.object_id = p." + pID + " " +
			"JOIN " + r.prefix + "term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id " +
			"WHERE tt.taxonomy = ? AND tt.term_id = ? AND p.post_type = ?"
		rows, err := tx.QueryContext(ctx, collect, "nav_menu", id, "nav_menu_item")
		if err != nil {
			return err
		}
		var itemIDs []int64
		for rows.Next() {
			var itemID int64
			if err := rows.Scan(&itemID); err != nil {
				rows.Close()
				return err
			}
			itemIDs = append(itemIDs, itemID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		// (2) DELETE those items' postmeta, then (3) their posts.
		for _, itemID := range itemIDs {
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM "+r.prefix+"postmeta WHERE post_id = ?", itemID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM "+r.prefix+"posts WHERE "+pID+" = ?", itemID); err != nil {
				return err
			}
		}

		// (4) DELETE the term_relationships rows for the menu's term_taxonomy_id.
		relQ := "DELETE FROM " + r.prefix + "term_relationships WHERE term_taxonomy_id IN " +
			"(SELECT term_taxonomy_id FROM " + r.prefix + "term_taxonomy WHERE term_id = ? AND taxonomy = ?)"
		if _, err := tx.ExecContext(ctx, relQ, id, "nav_menu"); err != nil {
			return err
		}

		// (5) DELETE the term_taxonomy row.
		ttQ := "DELETE FROM " + r.prefix + "term_taxonomy WHERE term_id = ? AND taxonomy = ?"
		if _, err := tx.ExecContext(ctx, ttQ, id, "nav_menu"); err != nil {
			return err
		}

		// (6) DELETE the terms row; no row removed → domain.ErrNotFound.
		termQ := "DELETE FROM " + r.prefix + "terms WHERE term_id = ?"
		res, err := tx.ExecContext(ctx, termQ, id)
		if err != nil {
			return err
		}
		return errNotFoundIfZero(res)
	})
}

// CreateItem inserts one nav_menu_item post linked to menuID's term, writes its
// _menu_item_* postmeta, and returns the new item's post ID (Req 2.1, 6.1). The
// item's object_id is stored as supplied without an existence check, matching
// the read path's tolerance of a dangling reference (Finding 2). Returns
// domain.ErrNotFound when menuID is not a nav_menu term (Req 2.4).
func (r *NavMenuRepo) CreateItem(ctx context.Context, menuID int64, item domain.NavMenuItemInput) (int64, error) {
	vendor := vendorOf(r.db)
	var itemID int64
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ttID, err := r.menuTermTaxonomyID(ctx, tx, menuID)
		if err != nil {
			return err
		}

		id, err := r.insertItemPost(ctx, tx, vendor, item.Label, item.Order)
		if err != nil {
			return err
		}
		itemID = id

		relQ := "INSERT INTO " + r.prefix + "term_relationships " +
			"(object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)"
		if _, err := tx.ExecContext(ctx, relQ, itemID, ttID, 0); err != nil {
			return err
		}

		return r.writeItemMeta(ctx, tx, itemID, item.Type, item.Object, item.ObjectID, item.ParentID, item.URL, item.Type == "custom")
	})
	if err != nil {
		return 0, err
	}
	return itemID, nil
}

// insertItemPost inserts the nav_menu_item {prefix}posts row (post_title=label,
// post_type="nav_menu_item", post_status="publish", menu_order=order, dates via
// formatTS), returning the new post ID. It mirrors PostRepo.Create's column
// shape, defaulting the remaining mutable columns to WordPress-compatible
// empties so a nav_menu_item row round-trips through the read path.
func (r *NavMenuRepo) insertItemPost(ctx context.Context, tx bun.Tx, vendor, label string, order int) (int64, error) {
	now := formatTS(time.Now())
	cols := []string{
		"post_author", "post_date", "post_content", "post_title",
		"post_excerpt", "post_status", "post_name", "post_type", "comment_status",
		"post_date_gmt", "post_modified", "post_modified_gmt", "post_parent", "menu_order",
	}
	args := []any{
		0, now, "", label,
		"", "publish", "", "nav_menu_item", "closed",
		now, now, now, 0, order,
	}
	return insertReturningID(ctx, tx, vendor, r.prefix+"posts", cols, `"ID"`, args...)
}

// writeItemMeta upserts the four always-present _menu_item_* keys, plus
// _menu_item_url only when writeURL is true (type=custom). It is the single
// place the read path's five-key pivot (menus.go) is produced in reverse.
func (r *NavMenuRepo) writeItemMeta(ctx context.Context, tx bun.Tx, itemID int64, typ, object string, objectID, parentID int64, url string, writeURL bool) error {
	if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_type", typ); err != nil {
		return err
	}
	if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_object", object); err != nil {
		return err
	}
	if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_object_id", fmt.Sprintf("%d", objectID)); err != nil {
		return err
	}
	if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_menu_item_parent", fmt.Sprintf("%d", parentID)); err != nil {
		return err
	}
	if writeURL {
		if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_url", url); err != nil {
			return err
		}
	}
	return nil
}

// menuTermTaxonomyID returns the term_taxonomy_id of menuID's nav_menu term, or
// domain.ErrNotFound when menuID is not a nav_menu term.
func (r *NavMenuRepo) menuTermTaxonomyID(ctx context.Context, tx bun.Tx, menuID int64) (int64, error) {
	q := "SELECT term_taxonomy_id FROM " + r.prefix +
		"term_taxonomy WHERE term_id = ? AND taxonomy = ?"
	var ttID int64
	err := tx.QueryRowContext(ctx, q, menuID, "nav_menu").Scan(&ttID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return ttID, nil
}

// UpdateItem applies a partial update to itemID (which must be a nav_menu_item
// linked to menuID): post_title/menu_order on the {prefix}posts row and the
// supplied _menu_item_* keys only. A nil patch field is left unchanged (Req 2.2).
// The belongs-to-menu guard runs first; no match → domain.ErrNotFound (Req 2.4).
func (r *NavMenuRepo) UpdateItem(ctx context.Context, menuID, itemID int64, patch domain.NavMenuItemPatch) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ok, err := r.itemBelongsToMenu(ctx, tx, menuID, itemID)
		if err != nil {
			return err
		}
		if !ok {
			return domain.ErrNotFound
		}

		if patch.Label != nil || patch.Order != nil {
			upd := tx.NewUpdate().
				TableExpr("?", bun.Ident(r.prefix+"posts")).
				Where("? = ?", bun.Ident("ID"), itemID).
				Where("post_type = ?", "nav_menu_item")
			if patch.Label != nil {
				upd = upd.Set("post_title = ?", *patch.Label)
			}
			if patch.Order != nil {
				upd = upd.Set("menu_order = ?", *patch.Order)
			}
			if _, err := upd.Exec(ctx); err != nil {
				return err
			}
		}

		if patch.Type != nil {
			if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_type", *patch.Type); err != nil {
				return err
			}
		}
		if patch.Object != nil {
			if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_object", *patch.Object); err != nil {
				return err
			}
		}
		if patch.ObjectID != nil {
			if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_object_id", fmt.Sprintf("%d", *patch.ObjectID)); err != nil {
				return err
			}
		}
		if patch.ParentID != nil {
			if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_menu_item_parent", fmt.Sprintf("%d", *patch.ParentID)); err != nil {
				return err
			}
		}
		if patch.URL != nil {
			if err := upsertPostMeta(ctx, tx, r.prefix, itemID, "_menu_item_url", *patch.URL); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteItem removes itemID's {prefix}posts row, its _menu_item_* {prefix}postmeta
// rows, and its {prefix}term_relationships row for menuID's term, in one
// transaction (Req 2.3, 6.3). The belongs-to-menu guard runs first; no match →
// domain.ErrNotFound (Req 2.4).
func (r *NavMenuRepo) DeleteItem(ctx context.Context, menuID, itemID int64) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ok, err := r.itemBelongsToMenu(ctx, tx, menuID, itemID)
		if err != nil {
			return err
		}
		if !ok {
			return domain.ErrNotFound
		}

		if _, err := tx.ExecContext(ctx,
			"DELETE FROM "+r.prefix+"postmeta WHERE post_id = ?", itemID); err != nil {
			return err
		}

		relQ := "DELETE FROM " + r.prefix + "term_relationships WHERE object_id = ? AND term_taxonomy_id IN " +
			"(SELECT term_taxonomy_id FROM " + r.prefix + "term_taxonomy WHERE term_id = ? AND taxonomy = ?)"
		if _, err := tx.ExecContext(ctx, relQ, itemID, menuID, "nav_menu"); err != nil {
			return err
		}

		pID := rebind.Ident(vendorOf(r.db), "ID")
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM "+r.prefix+"posts WHERE "+pID+" = ?", itemID); err != nil {
			return err
		}
		return nil
	})
}

// ItemBelongsToMenu reports whether itemID is a nav_menu_item linked to menuID's
// term. The service uses it for the parent-item validation of Req 5.4.
func (r *NavMenuRepo) ItemBelongsToMenu(ctx context.Context, menuID, itemID int64) (bool, error) {
	return r.itemBelongsToMenu(ctx, r.db, menuID, itemID)
}

// itemBelongsToMenu is the querier-generic belongs-to-menu check, used both as
// the exported primitive (against r.db) and as the in-transaction guard for
// UpdateItem/DeleteItem (against the tx). It SELECTs EXISTS over {prefix}posts
// JOIN {prefix}term_relationships JOIN {prefix}term_taxonomy. The "ID" quoting is
// the read path's cross-vendor-safe form (menus.go): a bare p.ID folds to p.id
// on PostgreSQL, which does not exist.
func (r *NavMenuRepo) itemBelongsToMenu(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, menuID, itemID int64) (bool, error) {
	pID := "p." + rebind.Ident(vendorOf(r.db), "ID")
	sqlText := "SELECT COUNT(*) FROM " + r.prefix + "posts p " +
		"JOIN " + r.prefix + "term_relationships tr ON tr.object_id = " + pID + " " +
		"JOIN " + r.prefix + "term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id " +
		"WHERE p.post_type = ? AND tt.taxonomy = ? AND tt.term_id = ? AND " + pID + " = ?"
	var count int
	if err := q.QueryRowContext(ctx, sqlText, "nav_menu_item", "nav_menu", menuID, itemID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
