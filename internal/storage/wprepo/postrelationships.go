package wprepo

import (
	"context"
	"strings"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

// PurgeTermRelationshipsOf implements domain.PostTermRelationshipPurger. One
// transaction: delete every relationship of postID outside
// domain.NonPostObjectTaxonomies, then recount each affected term_taxonomy.
func (r *PostRepo) PurgeTermRelationshipsOf(ctx context.Context, postID int64) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return purgeRelationshipsTx(ctx, tx, r.prefix, postID)
	})
}

// purgeRelationshipsTx deletes the term_relationships rows of objectID whose
// taxonomy is not in domain.NonPostObjectTaxonomies (link_category ids name
// links, not posts), one DELETE per distinct term_taxonomy_id, then recounts
// those term_taxonomy rows. Relationships whose term_taxonomy row is missing
// are not matched by the join and are left alone. It must run inside tx.
func purgeRelationshipsTx(ctx context.Context, tx bun.Tx, prefix string, objectID int64) error {
	q := "SELECT tr.term_taxonomy_id FROM " + prefix + "term_relationships tr " +
		"JOIN " + prefix + "term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id " +
		"WHERE tr.object_id = ?"
	args := []any{objectID}
	if n := len(domain.NonPostObjectTaxonomies); n > 0 {
		q += " AND tt.taxonomy NOT IN (?" + strings.Repeat(", ?", n-1) + ")"
		for _, tax := range domain.NonPostObjectTaxonomies {
			args = append(args, tax)
		}
	}
	ids, err := scanInt64s(ctx, tx, q, args...)
	if err != nil {
		return err
	}
	ids = sortedUniqueInt64(ids)
	for _, tt := range ids {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM "+prefix+"term_relationships WHERE object_id = ? AND term_taxonomy_id = ?",
			objectID, tt); err != nil {
			return err
		}
	}
	return recountTx(ctx, tx, prefix, ids)
}
