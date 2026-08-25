package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

// --- PostRepo revision/autosave/scheduler methods (domain.RevisionWriter,
// domain.ScheduledPostFinder) ---
//
// Revisions and autosave rows are ordinary rows in the same {prefix}posts
// table Create/Update/ByID already use (post_type='revision'); there is no
// separate repository or table. Naming follows WordPress's own convention so
// a pre-existing WordPress database's revision/autosave rows are recognized
// without translation: normal revisions are post_name "{parentID}-revision-vN"
// (N incrementing per post); the single autosave row per (post, author) is
// post_name "{parentID}-autosave-v1", found via a LIKE '{parentID}-autosave%'
// predicate combined with post_parent/post_author.

// autosaveNamePrefix is the post_name prefix that marks a revision row as the
// autosave for parentID (WordPress's own "{id}-autosave-vN" convention).
func autosaveNamePrefix(parentID int64) string {
	return fmt.Sprintf("%d-autosave", parentID)
}

// CreateRevision inserts a snapshot of a post as a new revision row. Normal
// revisions get the next "{parentID}-revision-vN" name; autosave rows always
// get "{parentID}-autosave-v1" (callers wanting the upsert-in-place semantics
// of Requirement 3.2 must first check AutosaveFor and call UpdateAutosave
// instead of calling CreateRevision again).
func (r *PostRepo) CreateRevision(ctx context.Context, parentID, authorID int64, snapshot domain.Post, autosave bool) (int64, error) {
	now := time.Now()
	var name string
	if autosave {
		name = autosaveNamePrefix(parentID) + "-v1"
	} else {
		n, err := r.nextRevisionVersion(ctx, parentID)
		if err != nil {
			return 0, err
		}
		name = fmt.Sprintf("%d-revision-v%d", parentID, n)
	}
	cols := []string{
		"post_author", "post_date", "post_content", "post_title",
		"post_excerpt", "post_status", "post_name", "post_type", "comment_status",
		"post_date_gmt", "post_modified", "post_modified_gmt", "post_parent",
	}
	args := []any{
		authorID, formatTS(now), snapshot.Content, snapshot.Title,
		snapshot.Excerpt, "inherit", name, "revision", "closed",
		formatTS(now), formatTS(now), formatTS(now), parentID,
	}
	return insertReturningID(ctx, r.db, vendorOf(r.db), r.prefix+"posts", cols, `"ID"`, args...)
}

// nextRevisionVersion returns 1 + the number of existing non-autosave
// revisions already stored for parentID. Retention pruning can therefore
// reuse a cosmetic vN suffix; revision IDs and modified timestamps remain the
// authoritative identity and ordering, and no code looks revisions up by name.
func (r *PostRepo) nextRevisionVersion(ctx context.Context, parentID int64) (int, error) {
	var count int
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		ColumnExpr("COUNT(*)").
		Where("post_type = ?", "revision").
		Where("post_parent = ?", parentID).
		Where("post_name NOT LIKE ?", "%-autosave%").
		Scan(ctx, &count)
	if err != nil {
		return 0, err
	}
	return count + 1, nil
}

// ListRevisions returns newest-first summaries (no content body) of every
// non-autosave revision belonging to parentID.
func (r *PostRepo) ListRevisions(ctx context.Context, parentID int64) ([]domain.RevisionMeta, error) {
	var rows []postRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("post_type = ?", "revision").
		Where("post_parent = ?", parentID).
		Where("post_name NOT LIKE ?", "%-autosave%").
		OrderExpr("post_modified DESC, ? DESC", bun.Ident("ID")).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	metas := make([]domain.RevisionMeta, len(rows))
	for i, row := range rows {
		metas[i] = domain.RevisionMeta{
			ID:       row.ID,
			ParentID: row.ParentID,
			Author:   row.Author,
			Modified: row.Modified,
			Autosave: false,
		}
	}
	return metas, nil
}

// RevisionByID returns the full content/title/excerpt for a single revision
// (or autosave) row by its own ID, or ErrNotFound. Revision rows live in the
// same posts table an ordinary ByID already reads, so it is reused as-is.
func (r *PostRepo) RevisionByID(ctx context.Context, id int64) (domain.Post, error) {
	return r.ByID(ctx, id)
}

// AutosaveFor returns the single autosave row for (parentID, authorID), or
// (Post{}, false, nil) when none exists yet.
func (r *PostRepo) AutosaveFor(ctx context.Context, parentID, authorID int64) (domain.Post, bool, error) {
	var row postRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("post_type = ?", "revision").
		Where("post_parent = ?", parentID).
		Where("post_author = ?", authorID).
		Where("post_name LIKE ?", autosaveNamePrefix(parentID)+"%").
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Post{}, false, nil
	}
	if err != nil {
		return domain.Post{}, false, err
	}
	return row.toDomain(), true, nil
}

// UpdateAutosave overwrites an existing autosave row's snapshot fields in
// place by its revision ID, or ErrNotFound.
func (r *PostRepo) UpdateAutosave(ctx context.Context, revisionID int64, snapshot domain.Post) error {
	now := time.Now()
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Set("post_title = ?", snapshot.Title).
		Set("post_content = ?", snapshot.Content).
		Set("post_excerpt = ?", snapshot.Excerpt).
		Set("post_modified = ?", formatTS(now)).
		Set("post_modified_gmt = ?", formatTS(now)).
		Where("? = ?", bun.Ident("ID"), revisionID).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}

// PruneRevisions deletes the oldest non-autosave revisions for parentID
// beyond the newest keep, never touching the autosave row. A no-op when
// there are keep or fewer revisions already.
func (r *PostRepo) PruneRevisions(ctx context.Context, parentID int64, keep int) error {
	if keep < 0 {
		keep = 0
	}
	list, err := r.ListRevisions(ctx, parentID)
	if err != nil {
		return err
	}
	if len(list) <= keep {
		return nil
	}
	// list is newest-first, so the excess to delete is everything past the
	// first `keep` entries.
	toDelete := list[keep:]
	ids := make([]int64, len(toDelete))
	for i, m := range toDelete {
		ids[i] = m.ID
	}
	_, err = r.db.NewDelete().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Where("? IN (?)", bun.Ident("ID"), bun.In(ids)).
		Exec(ctx)
	return err
}

// DeleteRevisionsOf deletes every revision row (including the autosave, if
// present) for parentID, leaving other posts' revisions untouched.
func (r *PostRepo) DeleteRevisionsOf(ctx context.Context, parentID int64) error {
	_, err := r.db.NewDelete().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Where("post_type = ?", "revision").
		Where("post_parent = ?", parentID).
		Exec(ctx)
	return err
}

// DueScheduled returns every post with status="future" whose post_date has
// already passed asOf, oldest-due first.
func (r *PostRepo) DueScheduled(ctx context.Context, asOf time.Time) ([]domain.Post, error) {
	var rows []postRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"posts")).
		Column(postColumns...).
		Where("post_status = ?", "future").
		Where("post_date <= ?", formatTS(asOf)).
		OrderExpr("post_date ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	return toDomainPosts(rows), nil
}

// compile-time interface checks.
var (
	_ domain.RevisionWriter      = (*PostRepo)(nil)
	_ domain.ScheduledPostFinder = (*PostRepo)(nil)
)
