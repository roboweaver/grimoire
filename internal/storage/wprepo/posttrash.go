package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/php"
	"github.com/uptrace/bun"
)

// trashSuffix is the marker WordPress appends to the post_name of a trashed
// row.
const trashSuffix = "__trashed"

// maxSlugBytes is the width of the post_name column.
const maxSlugBytes = 191

// truncateSlug cuts name to at most max bytes. When the cut would land inside
// a percent escape (%XX) it cuts before the '%' so no escape is split.
func truncateSlug(name string, max int) string {
	if max < 0 {
		max = 0
	}
	if len(name) <= max {
		return name
	}
	for _, i := range []int{max - 1, max - 2} {
		if i >= 0 && name[i] == '%' && isHexByte(name[i+1]) && isHexByte(name[i+2]) {
			return name[:i]
		}
	}
	return name[:max]
}

func isHexByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// trashedName returns the post_name WordPress writes for a trashed row.
func trashedName(name string) string {
	return truncateSlug(name, maxSlugBytes) + trashSuffix
}

// Trash meta keys WordPress writes.
const (
	metaTrashStatus   = "_wp_trash_meta_status"
	metaTrashTime     = "_wp_trash_meta_time"
	metaDesiredSlug   = "_wp_desired_post_slug"
	metaTrashComments = "_wp_trash_meta_comments_status"
)

// maxNameBytes is the longest post_name uniqueName may produce.
const maxNameBytes = 200

// commentChunk bounds the id list of one comment UPDATE.
const commentChunk = 500

// TrashPost implements domain.PostTrasher. See the interface for the contract;
// the statements follow wp_trash_post and wp_trash_post_comments. One
// transaction.
func (r *PostRepo) TrashPost(ctx context.Context, id int64, at time.Time) (string, error) {
	var previous string
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		posts := bun.Ident(r.prefix + "posts")
		postmeta := bun.Ident(r.prefix + "postmeta")
		comments := bun.Ident(r.prefix + "comments")
		idCol := bun.Ident("ID")

		var name string
		err := tx.QueryRowContext(ctx, "SELECT post_status, post_name FROM ? WHERE ? = ?", posts, idCol, id).
			Scan(&previous, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if previous == "trash" {
			return domain.ErrAlreadyTrashed
		}

		// A stale pair would be read first by get_post_meta(..., true).
		if _, err := tx.ExecContext(ctx, "DELETE FROM ? WHERE post_id = ? AND meta_key IN (?, ?)",
			postmeta, id, metaTrashStatus, metaTrashTime); err != nil {
			return err
		}
		if err := insertMetaTx(ctx, tx, postmeta, id, metaTrashStatus, previous); err != nil {
			return err
		}
		if err := insertMetaTx(ctx, tx, postmeta, id, metaTrashTime, strconv.FormatInt(at.Unix(), 10)); err != nil {
			return err
		}

		newName := name
		if !strings.HasSuffix(name, trashSuffix) {
			if err := insertMetaTx(ctx, tx, postmeta, id, metaDesiredSlug, name); err != nil {
				return err
			}
			newName = trashedName(name)
		}
		res, err := tx.ExecContext(ctx,
			"UPDATE ? SET post_status = 'trash', post_name = ?, post_modified = ?, post_modified_gmt = ? "+
				"WHERE ? = ? AND post_status <> 'trash'",
			posts, newName, formatTS(at), formatTS(at.UTC()), idCol, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return domain.ErrAlreadyTrashed
		}

		// wp_trash_post_comments: record each comment's state, then hide them.
		rows, err := tx.QueryContext(ctx, "SELECT ?, comment_approved FROM ? WHERE ? = ?",
			bun.Ident("comment_ID"), comments, bun.Ident("comment_post_ID"), id)
		if err != nil {
			return err
		}
		statuses := map[int]string{}
		for rows.Next() {
			var cid int64
			var approved sql.NullString
			if err := rows.Scan(&cid, &approved); err != nil {
				rows.Close()
				return err
			}
			statuses[int(cid)] = approved.String
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(statuses) > 0 {
			ser, err := php.Serialize(statuses)
			if err != nil {
				return err
			}
			if err := insertMetaTx(ctx, tx, postmeta, id, metaTrashComments, ser); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE ? SET comment_approved = 'post-trashed' WHERE ? = ?",
				comments, bun.Ident("comment_post_ID"), id); err != nil {
				return err
			}
		}

		ids, err := relatedTermTaxonomyIDsTx(ctx, tx, r.prefix, id)
		if err != nil {
			return err
		}
		return recountTx(ctx, tx, r.prefix, vendorOf(r.db), ids)
	})
	if err != nil {
		return "", err
	}
	return previous, nil
}

// RestorePost implements domain.PostTrasher, following wp_untrash_post and
// wp_untrash_post_comments. One transaction.
func (r *PostRepo) RestorePost(ctx context.Context, id int64, req domain.RestoreRequest) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		posts := bun.Ident(r.prefix + "posts")
		postmeta := bun.Ident(r.prefix + "postmeta")
		comments := bun.Ident(r.prefix + "comments")
		idCol := bun.Ident("ID")

		var status, name, typ string
		var parent int64
		err := tx.QueryRowContext(ctx, "SELECT post_status, post_name, post_type, post_parent FROM ? WHERE ? = ?",
			posts, idCol, id).Scan(&status, &name, &typ, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "trash" {
			return domain.ErrNotTrashed
		}

		desired, hasDesired, err := firstMetaTx(ctx, tx, postmeta, id, metaDesiredSlug)
		if err != nil {
			return err
		}
		newName := req.Slug
		if newName == "" {
			if hasDesired {
				newName = desired
			} else {
				newName = strings.TrimSuffix(name, trashSuffix)
			}
		}
		if newName != "" {
			newName, err = uniqueName(ctx, tx, r.prefix, newName, typ, id, parent, req.HierarchicalScope)
			if err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, "DELETE FROM ? WHERE post_id = ? AND meta_key IN (?, ?, ?)",
			postmeta, id, metaTrashStatus, metaTrashTime, metaDesiredSlug); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			"UPDATE ? SET post_status = ?, post_name = ?, post_modified = ?, post_modified_gmt = ? "+
				"WHERE ? = ? AND post_status = 'trash'",
			posts, req.Status, newName, formatTS(req.At), formatTS(req.At.UTC()), idCol, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return domain.ErrNotTrashed
		}

		// wp_untrash_post_comments.
		raw, hasRaw, err := firstMetaTx(ctx, tx, postmeta, id, metaTrashComments)
		if err != nil {
			return err
		}
		if hasRaw {
			if err := restoreCommentsTx(ctx, tx, comments, id, raw); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM ? WHERE post_id = ? AND meta_key = ?",
				postmeta, id, metaTrashComments); err != nil {
				return err
			}
		}

		ids, err := relatedTermTaxonomyIDsTx(ctx, tx, r.prefix, id)
		if err != nil {
			return err
		}
		return recountTx(ctx, tx, r.prefix, vendorOf(r.db), ids)
	})
}

// TrashInfo implements domain.PostTrasher.
func (r *PostRepo) TrashInfo(ctx context.Context, id int64) (domain.TrashInfo, error) {
	var info domain.TrashInfo
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var one int64
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM ? WHERE ? = ?",
			bun.Ident(r.prefix+"posts"), bun.Ident("ID"), id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		postmeta := bun.Ident(r.prefix + "postmeta")
		if info.PreviousStatus, info.HasPreviousStatus, err = firstMetaTx(ctx, tx, postmeta, id, metaTrashStatus); err != nil {
			return err
		}
		if info.DesiredSlug, info.HasDesiredSlug, err = firstMetaTx(ctx, tx, postmeta, id, metaDesiredSlug); err != nil {
			return err
		}
		raw, ok, err := firstMetaTx(ctx, tx, postmeta, id, metaTrashTime)
		if err != nil {
			return err
		}
		if ok {
			if secs, perr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); perr == nil {
				info.TrashedAt = time.Unix(secs, 0).UTC()
				info.HasTrashedAt = true
			}
		}
		return nil
	})
	if err != nil {
		return domain.TrashInfo{}, err
	}
	return info, nil
}

func insertMetaTx(ctx context.Context, tx bun.Tx, postmeta bun.Ident, id int64, key, value string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO ? (post_id, meta_key, meta_value) VALUES (?, ?, ?)",
		postmeta, id, key, value)
	return err
}

// firstMetaTx reads the value of the lowest meta_id row for key.
func firstMetaTx(ctx context.Context, tx bun.Tx, postmeta bun.Ident, id int64, key string) (string, bool, error) {
	var v sql.NullString
	err := tx.QueryRowContext(ctx,
		"SELECT meta_value FROM ? WHERE post_id = ? AND meta_key = ? ORDER BY meta_id LIMIT 1",
		postmeta, id, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v.String, true, nil
}

// uniqueName returns name, or name-2, name-3, ... for the first one no live
// (non-trash) row of the same type holds. With hierarchical set the check is
// scoped to the same post_parent. The row itself is excluded.
func uniqueName(ctx context.Context, tx bun.Tx, prefix, name, typ string, id, parent int64, hierarchical bool) (string, error) {
	posts := bun.Ident(prefix + "posts")
	candidate := name
	for n := 2; ; n++ {
		q := "SELECT 1 FROM ? WHERE post_name = ? AND post_type = ? AND ? <> ? AND post_status <> 'trash'"
		args := []any{posts, candidate, typ, bun.Ident("ID"), id}
		if hierarchical {
			q += " AND post_parent = ?"
			args = append(args, parent)
		}
		q += " LIMIT 1"
		var one int
		err := tx.QueryRowContext(ctx, q, args...).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		suffix := "-" + strconv.Itoa(n)
		candidate = truncateSlug(name, maxNameBytes-len(suffix)) + suffix
	}
}

// restoreCommentsTx sets each recorded comment back to the status it had; a
// recorded post-trashed becomes 0. A value that does not unserialize to an
// array sets the post's post-trashed comments to 0.
func restoreCommentsTx(ctx context.Context, tx bun.Tx, comments bun.Ident, postID int64, raw string) error {
	idCol := bun.Ident("comment_ID")
	val, err := php.Unserialize(raw)
	recorded, ok := val.(map[string]any)
	if err != nil || !ok {
		_, err := tx.ExecContext(ctx,
			"UPDATE ? SET comment_approved = '0' WHERE ? = ? AND comment_approved = 'post-trashed'",
			comments, bun.Ident("comment_post_ID"), postID)
		return err
	}
	byStatus := map[string][]int64{}
	for k, v := range recorded {
		cid, perr := strconv.ParseInt(k, 10, 64)
		s, isStr := v.(string)
		if perr != nil || !isStr {
			continue
		}
		if s == "post-trashed" {
			s = "0"
		}
		byStatus[s] = append(byStatus[s], cid)
	}
	for s, ids := range byStatus {
		ids = sortedUniqueInt64(ids)
		for start := 0; start < len(ids); start += commentChunk {
			end := start + commentChunk
			if end > len(ids) {
				end = len(ids)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE ? SET comment_approved = ? WHERE ? IN (?)",
				comments, s, idCol, bun.In(ids[start:end])); err != nil {
				return err
			}
		}
	}
	return nil
}
