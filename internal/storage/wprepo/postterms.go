package wprepo

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
)

var (
	_ domain.PostTermsRepository = (*PostTermsRepo)(nil)
	_ domain.PostTermsWriter     = (*PostTermsRepo)(nil)
)

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

// SetPostTerms replaces postID's term relationships for taxonomy with exactly
// termIDs, in one transaction, per design.md's three-step SQL: (1) delete the
// post's existing relationships scoped to this taxonomy's term_taxonomy_ids,
// (2) insert a fresh relationship row for each termID that resolves to a
// term_taxonomy_id under this taxonomy, (3) recompute term_taxonomy.count for
// every taxonomy row touched by either step — both the rows newly assigned
// and any row the post was previously related to (which may have lost its
// only relationship without being replaced).
//
// Neither requirements.md's Req 2 acceptance criteria nor design.md's SQL
// description specify an error path for a nonexistent postID or a termID
// that doesn't resolve to this taxonomy: this mirrors Req 2.6's storage-layer
// non-enforcement of taxonomy/post-type mismatches, so an unknown postID is a
// silent no-op (steps 1-2 delete/insert zero rows) and an unresolvable termID
// is silently skipped from step 2 (the same convention as TermReader.TermsByIDs).
func (r *PostTermsRepo) SetPostTerms(ctx context.Context, postID int64, taxonomy string, termIDs []int64) error {
	vendor := vendorOf(r.db)
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Capture the term_taxonomy_ids currently related to postID under
		// this taxonomy before clearing them, so their counts still get
		// recomputed even if the new set no longer includes them.
		var priorTTIDs []int64
		err := tx.NewSelect().
			TableExpr("? AS tt", bun.Ident(r.prefix+"term_taxonomy")).
			ColumnExpr("tt.term_taxonomy_id").
			Join("JOIN ? AS tr ON tr.term_taxonomy_id = tt.term_taxonomy_id", bun.Ident(r.prefix+"term_relationships")).
			Where("tt.taxonomy = ?", taxonomy).
			Where("tr.object_id = ?", postID).
			Scan(ctx, &priorTTIDs)
		if err != nil {
			return err
		}

		// Step 1: clear this taxonomy's existing relationships for postID.
		delQ := "DELETE FROM " + r.prefix + "term_relationships WHERE object_id = ? AND term_taxonomy_id IN " +
			"(SELECT term_taxonomy_id FROM " + r.prefix + "term_taxonomy WHERE taxonomy = ?)"
		if _, err := tx.ExecContext(ctx, rebind.Rebind(vendor, delQ), postID, taxonomy); err != nil {
			return err
		}

		// Resolve term_taxonomy_ids for the requested termIDs under this
		// taxonomy; unresolvable IDs are silently skipped.
		var newTTIDs []int64
		if len(termIDs) > 0 {
			err := tx.NewSelect().
				TableExpr("?", bun.Ident(r.prefix+"term_taxonomy")).
				Column("term_taxonomy_id").
				Where("taxonomy = ?", taxonomy).
				Where("term_id IN (?)", bun.In(termIDs)).
				Scan(ctx, &newTTIDs)
			if err != nil {
				return err
			}
		}

		// Step 2: insert a fresh relationship row per resolved taxonomy ID.
		insQ := "INSERT INTO " + r.prefix + "term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, 0)"
		for _, ttID := range newTTIDs {
			if _, err := tx.ExecContext(ctx, rebind.Rebind(vendor, insQ), postID, ttID); err != nil {
				return err
			}
		}

		// Step 3: recompute count for the union of previously- and
		// newly-related taxonomy rows.
		countQ := "UPDATE " + r.prefix + "term_taxonomy SET count = " +
			"(SELECT COUNT(*) FROM " + r.prefix + "term_relationships WHERE term_taxonomy_id = ?) " +
			"WHERE term_taxonomy_id = ?"
		seen := make(map[int64]bool, len(priorTTIDs)+len(newTTIDs))
		for _, ttID := range append(priorTTIDs, newTTIDs...) {
			if seen[ttID] {
				continue
			}
			seen[ttID] = true
			if _, err := tx.ExecContext(ctx, rebind.Rebind(vendor, countQ), ttID, ttID); err != nil {
				return err
			}
		}
		return nil
	})
}
