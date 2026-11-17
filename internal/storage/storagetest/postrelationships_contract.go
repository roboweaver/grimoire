package storagetest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the post-relationship purger contract, far above SeedFixtures.
const (
	prP     int64 = 9500 // post whose relationships are purged
	prQ     int64 = 9501 // another post related to the same terms
	prTTCat int64 = 9520 // category term_taxonomy_id
	prTTTag int64 = 9521 // post_tag
	prTTPC  int64 = 9522 // product_cat (unmanaged taxonomy)
	prTTPF  int64 = 9523 // post_format (unmanaged taxonomy)
	prTTLnk int64 = 9524 // link_category: relationship object_id equals P
	prTTOth int64 = 9525 // category term related to Q only (unaffected)

	prStale = 99 // stale term_taxonomy.count seeded on every term
)

// RunPostTermRelationshipPurgerContract covers PostTermRelationshipPurger.
// PurgeTermRelationshipsOf: every relationship of the post is removed except
// those in a non-post-object taxonomy (link_category), the counts of the
// affected terms are recomputed from their remaining relationships, other
// rows are untouched, and the call is idempotent (Req 12.2). Atomicity has no
// portable failure injection and is covered on SQLite in
// internal/storage/wprepo/postrelationships_test.go.
//
// The vendor and table prefix come from newRepos; the contract holds no
// literal prefix and no vendor-specific placeholder (Req 5.1).
func RunPostTermRelationshipPurgerContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	setup := func(t *testing.T) (db *sql.DB, vendor, prefix string, purge func(postID int64) error, cleanup func()) {
		t.Helper()
		var repos *storage.Repositories
		repos, vendor, prefix, cleanup = newRepos(t)
		db = repos.DB()
		seedPostRelationships(ctx, t, db, vendor, prefix)
		purge = func(postID int64) error {
			return repos.PostRelationships.PurgeTermRelationshipsOf(ctx, postID)
		}
		return db, vendor, prefix, purge, cleanup
	}

	t.Run("removes the post's relationships in managed and unmanaged taxonomies", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(prP); err != nil {
			t.Fatalf("PurgeTermRelationshipsOf: %v", err)
		}
		for name, tt := range map[string]int64{"category": prTTCat, "post_tag": prTTTag, "product_cat": prTTPC, "post_format": prTTPF} {
			if n := trRel(ctx, t, db, vendor, prefix, prP, tt); n != 0 {
				t.Errorf("P relationship in %s = %d, want 0", name, n)
			}
			if n := trRel(ctx, t, db, vendor, prefix, prQ, tt); n != 1 {
				t.Errorf("Q relationship in %s = %d, want 1 (kept)", name, n)
			}
		}
	})

	t.Run("keeps the link_category row that shares the object id", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(prP); err != nil {
			t.Fatalf("PurgeTermRelationshipsOf: %v", err)
		}
		if n := trRel(ctx, t, db, vendor, prefix, prP, prTTLnk); n != 1 {
			t.Errorf("link_category relationship with object_id = P rows = %d, want 1", n)
		}
	})

	t.Run("recomputes the count of each affected term and leaves the others", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(prP); err != nil {
			t.Fatalf("PurgeTermRelationshipsOf: %v", err)
		}
		for name, tt := range map[string]int64{"category": prTTCat, "post_tag": prTTTag, "product_cat": prTTPC, "post_format": prTTPF} {
			want := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE term_taxonomy_id = ?`, tt)
			if want != 1 {
				t.Fatalf("%s relationships after purge = %d, want 1", name, want)
			}
			if got := miCount(ctx, t, db, vendor, prefix, tt); got != want {
				t.Errorf("%s count = %d, want %d (recounted)", name, got, want)
			}
		}
		for name, tt := range map[string]int64{"link_category": prTTLnk, "unaffected category": prTTOth} {
			if got := miCount(ctx, t, db, vendor, prefix, tt); got != prStale {
				t.Errorf("%s count = %d, want %d (unchanged)", name, got, prStale)
			}
		}
	})

	t.Run("leaves terms and term_taxonomy rows untouched", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		terms := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`terms`)
		taxes := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_taxonomy`)
		if err := purge(prP); err != nil {
			t.Fatalf("PurgeTermRelationshipsOf: %v", err)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`terms`); n != terms {
			t.Errorf("terms = %d, want %d", n, terms)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_taxonomy`); n != taxes {
			t.Errorf("term_taxonomy = %d, want %d", n, taxes)
		}
	})

	t.Run("a second call changes nothing", func(t *testing.T) {
		db, vendor, prefix, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(prP); err != nil {
			t.Fatalf("first PurgeTermRelationshipsOf: %v", err)
		}
		rels := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships`)
		counts := make([]int, 0, 6)
		tts := []int64{prTTCat, prTTTag, prTTPC, prTTPF, prTTLnk, prTTOth}
		for _, tt := range tts {
			counts = append(counts, miCount(ctx, t, db, vendor, prefix, tt))
		}
		if err := purge(prP); err != nil {
			t.Fatalf("second PurgeTermRelationshipsOf: %v", err)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships`); n != rels {
			t.Errorf("term_relationships after repeat = %d, want %d", n, rels)
		}
		for i, tt := range tts {
			if got := miCount(ctx, t, db, vendor, prefix, tt); got != counts[i] {
				t.Errorf("count of term_taxonomy %d after repeat = %d, want %d", tt, got, counts[i])
			}
		}
	})

	t.Run("no relationships and nonexistent post id both return nil", func(t *testing.T) {
		_, _, _, purge, cleanup := setup(t)
		defer cleanup()
		if err := purge(prQ + 500); err != nil {
			t.Errorf("purge of a post with no relationships = %v, want nil", err)
		}
		if err := purge(999999); err != nil {
			t.Errorf("purge of a nonexistent id = %v, want nil", err)
		}
	})
}

// seedPostRelationships inserts posts P and Q, one term per taxonomy (category,
// post_tag, product_cat, post_format, link_category, and a second category
// related to Q only), each with a stale count, P and Q related to the first
// four, and a link_category relationship whose object_id equals P.
func seedPostRelationships(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	for _, p := range []struct {
		id   int64
		slug string
	}{{prP, "pr-p"}, {prQ, "pr-q"}} {
		exec(postInsert(vendor, prefix), postArgs(p.id, p.slug, "T "+p.slug, "post", "publish", "2000-01-01 00:00:00", "open", 0, "", 0)...)
	}
	for _, tx := range []struct {
		tt       int64
		taxonomy string
	}{
		{prTTCat, "category"}, {prTTTag, "post_tag"}, {prTTPC, "product_cat"},
		{prTTPF, "post_format"}, {prTTLnk, "link_category"}, {prTTOth, "category"},
	} {
		seedTermTaxonomy(ctx, t, db, vendor, prefix, tx.tt, tx.taxonomy, prStale)
	}
	for _, obj := range []int64{prP, prQ} {
		for _, tt := range []int64{prTTCat, prTTTag, prTTPC, prTTPF} {
			exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, obj, tt, 0)
		}
	}
	exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, prP, prTTLnk, 0)
	exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, prQ, prTTOth, 0)
}

// seedTermTaxonomy inserts a term and its term_taxonomy row, using the
// term_taxonomy_id as the term_id and a slug derived from it.
func seedTermTaxonomy(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, tt int64, taxonomy string, count int) {
	t.Helper()
	slug := taxonomy + "-" + itoa64(tt)
	if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, `INSERT INTO `+prefix+`terms (term_id, name, slug) VALUES (?, ?, ?)`), tt, slug, slug); err != nil {
		t.Fatalf("seed term %d: %v", tt, err)
	}
	if _, err := db.ExecContext(ctx, rebind.Rebind(vendor,
		`INSERT INTO `+prefix+`term_taxonomy (term_taxonomy_id, term_id, taxonomy, description, parent, count) VALUES (?, ?, ?, ?, ?, ?)`),
		tt, tt, taxonomy, "", 0, count); err != nil {
		t.Fatalf("seed term_taxonomy %d: %v", tt, err)
	}
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func trRel(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string, object, tt int64) int {
	t.Helper()
	return pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE object_id = ? AND term_taxonomy_id = ?`, object, tt)
}
