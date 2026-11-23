package wprepo

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
	"testing/quick"
)

// recountForProperty runs recountTx in its own transaction. It is the single
// place that names recountTx's signature in this file.
func recountForProperty(t *testing.T, repo *PostRepo, ttIDs []int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := recountTx(ctx, tx, repo.prefix, vendorOf(repo.db), ttIDs); err != nil {
		_ = tx.Rollback()
		t.Fatalf("recountTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestPropertyRecountIgnoresTrashed checks Property 6.
//
// Feature: m22-trash-and-restore, Property 6: The term count ignores trashed objects
//
// Validates: Requirements 5.1, 5.4
func TestPropertyRecountIgnoresTrashed(t *testing.T) {
	ctx := context.Background()
	repo := newPostDependentsRepo(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	statuses := []string{"publish", "draft", "trash"}
	iter := 0
	prop := func(seed int64) bool {
		iter++
		rng := rand.New(rand.NewSource(seed))
		base := int64(500000 + iter*100) // fresh high ids per iteration
		tt := base
		exec(`INSERT INTO `+repo.prefix+`terms (term_id, name, slug) VALUES (?, ?, ?)`, tt, "p6", "p6-"+strconv.FormatInt(tt, 10))
		exec(`INSERT INTO `+repo.prefix+`term_taxonomy (term_taxonomy_id, term_id, taxonomy, count) VALUES (?, ?, 'category', 999)`, tt, tt)
		want := 0
		for i := 0; i < rng.Intn(9); i++ {
			id := base + 1 + int64(i)
			status := statuses[rng.Intn(len(statuses))]
			exec(`INSERT INTO `+repo.prefix+`posts ("ID", post_type, post_status) VALUES (?, 'post', ?)`, id, status)
			if rng.Intn(10) < 6 {
				exec(`INSERT INTO `+repo.prefix+`term_relationships (object_id, term_taxonomy_id) VALUES (?, ?)`, id, tt)
				if status != "trash" {
					want++
				}
			}
		}
		for j := 0; j < rng.Intn(4); j++ { // objects that are not posts
			exec(`INSERT INTO `+repo.prefix+`term_relationships (object_id, term_taxonomy_id) VALUES (?, ?)`, base+50+int64(j), tt)
			want++
		}
		recountForProperty(t, repo, []int64{tt})
		var got int
		if err := repo.db.QueryRowContext(ctx, `SELECT count FROM `+repo.prefix+`term_taxonomy WHERE term_taxonomy_id = ?`, tt).Scan(&got); err != nil {
			t.Fatalf("read count: %v", err)
		}
		if got != want {
			t.Logf("seed %d: count = %d, want %d", seed, got, want)
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100, Rand: rand.New(rand.NewSource(6))}); err != nil {
		t.Error(err)
	}
}
