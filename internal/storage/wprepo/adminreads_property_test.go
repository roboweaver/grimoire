package wprepo

import (
	"context"
	"math/rand"
	"sort"
	"testing"
	"testing/quick"

	"github.com/roboweaver/grimoire/internal/domain"
)

// TestPropertyAdminListExcludesTrash checks Property 9.
//
// Feature: m22-trash-and-restore, Property 9: The default admin list excludes trash
//
// Validates: Requirements 10.1
func TestPropertyAdminListExcludesTrash(t *testing.T) {
	ctx := context.Background()
	repo := newPostDependentsRepo(t)
	types := []string{"post", "page"}
	statuses := []string{"publish", "draft", "pending", "private", "future", "trash"}
	iter := 0
	ids := func(posts []domain.Post) []int64 {
		out := make([]int64, 0, len(posts))
		for _, p := range posts {
			out = append(out, p.ID)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	same := func(a, b []int64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	prop := func(seed int64) bool {
		iter++
		rng := rand.New(rand.NewSource(seed))
		author := int64(7000 + iter) // one author per iteration isolates its rows
		base := int64(600000 + iter*100)
		var live, trashed []int64
		for i := 0; i < rng.Intn(12); i++ {
			id := base + int64(i)
			status := statuses[rng.Intn(len(statuses))]
			if _, err := repo.db.ExecContext(ctx,
				`INSERT INTO `+repo.prefix+`posts ("ID", post_author, post_type, post_status) VALUES (?, ?, ?, ?)`,
				id, author, types[rng.Intn(len(types))], status); err != nil {
				t.Fatalf("insert: %v", err)
			}
			if status == "trash" {
				trashed = append(trashed, id)
			} else {
				live = append(live, id)
			}
		}
		for _, c := range []struct {
			statuses []string
			want     []int64
		}{{nil, live}, {[]string{"trash"}, trashed}} {
			f := domain.AdminPostFilter{Author: author, Statuses: c.statuses}
			posts, err := repo.ListForAdmin(ctx, f)
			if err != nil {
				t.Fatalf("ListForAdmin: %v", err)
			}
			n, err := repo.CountForAdmin(ctx, f)
			if err != nil {
				t.Fatalf("CountForAdmin: %v", err)
			}
			if !same(ids(posts), c.want) || n != len(c.want) {
				t.Logf("seed %d statuses %v: list %v count %d, want %v", seed, c.statuses, ids(posts), n, c.want)
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100, Rand: rand.New(rand.NewSource(9))}); err != nil {
		t.Error(err)
	}
}
