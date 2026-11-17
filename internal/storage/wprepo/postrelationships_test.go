package wprepo

import (
	"context"
	"testing"
)

// seedRelationshipPost inserts post 1 related to a category term_taxonomy 7 and
// returns an exec helper and a count helper bound to repo.
func seedRelationshipPost(t *testing.T, repo *PostRepo) (exec func(q string, args ...any), count func(q string) int) {
	t.Helper()
	ctx := context.Background()
	exec = func(q string, args ...any) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	count = func(q string) int {
		t.Helper()
		var n int
		if err := repo.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		return n
	}
	exec(`INSERT INTO wp_posts ("ID", post_type, post_status) VALUES (1, 'post', 'publish')`)
	exec(`INSERT INTO wp_terms (term_id, name, slug) VALUES (6, 'c', 'c')`)
	exec(`INSERT INTO wp_term_taxonomy (term_taxonomy_id, term_id, taxonomy, count) VALUES (7, 6, 'category', 5)`)
	exec(`INSERT INTO wp_term_relationships (object_id, term_taxonomy_id) VALUES (1, 7)`)
	return exec, count
}

// TestPurgeTermRelationshipsOfAtomicOnFailure covers Req 12.2 atomicity:
// when term_taxonomy is dropped the method returns an error and the
// relationship row is unchanged.
func TestPurgeTermRelationshipsOfAtomicOnFailure(t *testing.T) {
	ctx := context.Background()
	repo := newPostDependentsRepo(t)
	exec, count := seedRelationshipPost(t, repo)
	exec(`DROP TABLE wp_term_taxonomy`)

	if err := repo.PurgeTermRelationshipsOf(ctx, 1); err == nil {
		t.Fatal("PurgeTermRelationshipsOf with term_taxonomy dropped = nil, want an error")
	}
	if n := count(`SELECT COUNT(*) FROM wp_term_relationships WHERE object_id = 1`); n != 1 {
		t.Errorf("relationship rows = %d, want 1 (rolled back)", n)
	}
}

// TestPurgeTermRelationshipsOfRollsBackDeletesWhenRecountFails makes the
// recount, the last statement, fail with a trigger, so the relationship delete
// that already ran must be rolled back.
func TestPurgeTermRelationshipsOfRollsBackDeletesWhenRecountFails(t *testing.T) {
	ctx := context.Background()
	repo := newPostDependentsRepo(t)
	exec, count := seedRelationshipPost(t, repo)
	exec(`CREATE TRIGGER wp_block_recount BEFORE UPDATE ON wp_term_taxonomy BEGIN SELECT RAISE(ABORT, 'recount blocked'); END`)

	if err := repo.PurgeTermRelationshipsOf(ctx, 1); err == nil {
		t.Fatal("PurgeTermRelationshipsOf with the recount blocked = nil, want an error")
	}
	if n := count(`SELECT COUNT(*) FROM wp_term_relationships WHERE object_id = 1`); n != 1 {
		t.Errorf("relationship rows = %d, want 1 (rolled back)", n)
	}
	if n := count(`SELECT count FROM wp_term_taxonomy WHERE term_taxonomy_id = 7`); n != 5 {
		t.Errorf("term count = %d, want 5 (unchanged)", n)
	}
}
