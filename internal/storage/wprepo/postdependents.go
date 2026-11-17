package wprepo

import (
	"context"

	"github.com/uptrace/bun"
)

// PurgePostDependents implements domain.PostDependentsPurger. All five
// statements run in one transaction. The two re-point updates come first, then
// the commentmeta, comments and postmeta deletes. Neither update touches
// post_modified, and the post row itself is never deleted. Identifiers go
// through bun.Ident so the mixed-case WordPress column names (comment_ID,
// comment_post_ID) are quoted on PostgreSQL.
func (r *PostRepo) PurgePostDependents(ctx context.Context, postID, parentID int64, reparentType string) error {
	posts := bun.Ident(r.prefix + "posts")
	comments := bun.Ident(r.prefix + "comments")
	commentmeta := bun.Ident(r.prefix + "commentmeta")
	postmeta := bun.Ident(r.prefix + "postmeta")

	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if reparentType != "" {
			if _, err := tx.ExecContext(ctx,
				"UPDATE ? SET post_parent = ? WHERE post_parent = ? AND post_type = ?",
				posts, parentID, postID, reparentType); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE ? SET post_parent = ? WHERE post_parent = ? AND post_type = 'attachment'",
			posts, parentID, postID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM ? WHERE comment_id IN (SELECT ? FROM ? WHERE ? = ?)",
			commentmeta, bun.Ident("comment_ID"), comments, bun.Ident("comment_post_ID"), postID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM ? WHERE ? = ?",
			comments, bun.Ident("comment_post_ID"), postID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM ? WHERE post_id = ?", postmeta, postID)
		return err
	})
}
