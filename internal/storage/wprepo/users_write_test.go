package wprepo

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// seedAuthoredPost inserts a {prefix}posts row authored by author, with one
// {prefix}postmeta row and one {prefix}term_relationships row (object_id = the
// post id), so the reassign=false cascade can be asserted to leave no orphan.
// Returns the post id.
func seedAuthoredPost(t *testing.T, repo *UserRepo, author int64, title string) int64 {
	t.Helper()
	ctx := context.Background()
	vendor := vendorOf(repo.db)
	id, err := insertReturningID(ctx, repo.db, vendor, repo.prefix+"posts",
		[]string{"post_author", "post_title", "post_type", "post_status"},
		`"ID"`, author, title, "post", "publish")
	if err != nil {
		t.Fatalf("seed authored post: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx,
		"INSERT INTO "+repo.prefix+"postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)",
		id, "_some_key", "v"); err != nil {
		t.Fatalf("seed postmeta: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx,
		"INSERT INTO "+repo.prefix+"term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)",
		id, 1, 0); err != nil {
		t.Fatalf("seed term_relationships: %v", err)
	}
	return id
}

func seedUsermeta(t *testing.T, repo *UserRepo, userID int64, key, val string) {
	t.Helper()
	if _, err := repo.db.ExecContext(context.Background(),
		"INSERT INTO "+repo.prefix+"usermeta (user_id, meta_key, meta_value) VALUES (?, ?, ?)",
		userID, key, val); err != nil {
		t.Fatalf("seed usermeta %s: %v", key, err)
	}
}

func userCount(t *testing.T, repo *UserRepo, table, where string, args ...any) int {
	t.Helper()
	q := "SELECT COUNT(*) FROM " + repo.prefix + table
	if where != "" {
		q += " WHERE " + where
	}
	var n int
	if err := repo.db.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestUserRepoDeleteReassign covers Req 9.1: Delete(id, target>0) moves every
// authored post's post_author to target, leaves those posts' meta and
// relationships intact, and removes the users row and its usermeta.
func TestUserRepoDeleteReassign(t *testing.T) {
	ctx := context.Background()
	repo := newUserRepo(t)
	victim := seedUser(t, repo, domain.User{Login: "victim", Nicename: "victim", Pass: "$2a$10$x", Email: "v@example.com"})
	target := seedUser(t, repo, domain.User{Login: "heir", Nicename: "heir", Pass: "$2a$10$y", Email: "h@example.com"})
	seedUsermeta(t, repo, victim, "wp_capabilities", `a:1:{s:6:"author";b:1;}`)
	p1 := seedAuthoredPost(t, repo, victim, "one")
	p2 := seedAuthoredPost(t, repo, victim, "two")

	if err := repo.Delete(ctx, victim, target); err != nil {
		t.Fatalf("Delete(reassign): %v", err)
	}

	// Authored posts reassigned, meta + relationships intact.
	if got := userCount(t, repo, "posts", "post_author = ?", target); got != 2 {
		t.Errorf("reassigned posts for target = %d, want 2", got)
	}
	if got := userCount(t, repo, "posts", "post_author = ?", victim); got != 0 {
		t.Errorf("posts still owned by victim = %d, want 0", got)
	}
	for _, pid := range []int64{p1, p2} {
		if got := userCount(t, repo, "postmeta", "post_id = ?", pid); got != 1 {
			t.Errorf("post %d postmeta = %d, want 1 (reassign must not cascade)", pid, got)
		}
		if got := userCount(t, repo, "term_relationships", "object_id = ?", pid); got != 1 {
			t.Errorf("post %d term_relationships = %d, want 1 (reassign must not cascade)", pid, got)
		}
	}
	// User + usermeta gone.
	if got := userCount(t, repo, "users", `"ID" = ?`, victim); got != 0 {
		t.Errorf("victim users row = %d, want 0", got)
	}
	if got := userCount(t, repo, "usermeta", "user_id = ?", victim); got != 0 {
		t.Errorf("victim usermeta = %d, want 0", got)
	}
}

// TestUserRepoDeleteNoReassignCascades covers Req 9.2: Delete(id, 0) deletes the
// authored posts AND their postmeta and term_relationships (no orphan), and
// removes the user + usermeta.
func TestUserRepoDeleteNoReassignCascades(t *testing.T) {
	ctx := context.Background()
	repo := newUserRepo(t)
	victim := seedUser(t, repo, domain.User{Login: "victim", Nicename: "victim", Pass: "$2a$10$x", Email: "v@example.com"})
	seedUsermeta(t, repo, victim, "wp_capabilities", `a:1:{s:6:"author";b:1;}`)
	p1 := seedAuthoredPost(t, repo, victim, "one")

	if err := repo.Delete(ctx, victim, 0); err != nil {
		t.Fatalf("Delete(no reassign): %v", err)
	}

	if got := userCount(t, repo, "posts", "post_author = ?", victim); got != 0 {
		t.Errorf("authored posts after reassign=false = %d, want 0", got)
	}
	if got := userCount(t, repo, "postmeta", "post_id = ?", p1); got != 0 {
		t.Errorf("deleted post's postmeta = %d, want 0 (cascade, no orphan)", got)
	}
	if got := userCount(t, repo, "term_relationships", "object_id = ?", p1); got != 0 {
		t.Errorf("deleted post's term_relationships = %d, want 0 (cascade, no orphan)", got)
	}
	if got := userCount(t, repo, "users", `"ID" = ?`, victim); got != 0 {
		t.Errorf("victim users row = %d, want 0", got)
	}
	if got := userCount(t, repo, "usermeta", "user_id = ?", victim); got != 0 {
		t.Errorf("victim usermeta = %d, want 0", got)
	}
}

// TestUserRepoDeleteUnknownReturnsNotFound covers Req 9.5.
func TestUserRepoDeleteUnknownReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	repo := newUserRepo(t)
	if err := repo.Delete(ctx, 999999, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete(unknown) = %v, want domain.ErrNotFound", err)
	}
}
