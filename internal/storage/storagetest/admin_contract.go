package storagetest

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// runAdminContract covers the additive read/count ports backing the M3
// read-only admin: AdminPostRepository (list + count including drafts and
// pages), PostCounter, UserCounter, and TermCounter. Every method is a pure
// read; none of these queries writes or alters schema.
func runAdminContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("ListForAdmin default includes drafts and pages, newest-first", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		posts, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{})
		if err != nil {
			t.Fatalf("ListForAdmin: %v", err)
		}
		// about(01-05), secret(01-04), hello-3(01-03), hello-2(01-02), hello-1(01-01)
		want := []string{"about", "secret", "hello-3", "hello-2", "hello-1"}
		if len(posts) != len(want) {
			t.Fatalf("want %d posts, got %d", len(want), len(posts))
		}
		for i, w := range want {
			if posts[i].Slug != w {
				t.Errorf("post[%d] slug = %q, want %q", i, posts[i].Slug, w)
			}
		}
	})

	t.Run("ListForAdmin pagination via limit/offset", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		page1, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{Limit: 2, Offset: 0})
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		page2, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{Limit: 2, Offset: 2})
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(page1) != 2 || len(page2) != 2 {
			t.Fatalf("page sizes: page1=%d page2=%d", len(page1), len(page2))
		}
		if page1[0].Slug != "about" || page1[1].Slug != "secret" {
			t.Errorf("page1 order wrong: %q / %q", page1[0].Slug, page1[1].Slug)
		}
		if page2[0].Slug != "hello-3" || page2[1].Slug != "hello-2" {
			t.Errorf("page2 order wrong: %q / %q", page2[0].Slug, page2[1].Slug)
		}
	})

	t.Run("ListForAdmin filters by type and status", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		onlyPosts, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{Types: []string{"post"}})
		if err != nil {
			t.Fatalf("onlyPosts: %v", err)
		}
		if len(onlyPosts) != 4 {
			t.Fatalf("post-only count = %d, want 4", len(onlyPosts))
		}
		for _, p := range onlyPosts {
			if p.Type != "post" {
				t.Errorf("unexpected type %q in post-only filter", p.Type)
			}
		}
		drafts, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{
			Types:    []string{"post"},
			Statuses: []string{"draft"},
		})
		if err != nil {
			t.Fatalf("drafts: %v", err)
		}
		if len(drafts) != 1 || drafts[0].Slug != "secret" {
			t.Fatalf("draft filter = %+v, want [secret]", drafts)
		}
	})

	t.Run("CountForAdmin honors filter and ignores paging", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		total, err := repos.AdminPosts.CountForAdmin(ctx, domain.AdminPostFilter{Limit: 2, Offset: 1})
		if err != nil {
			t.Fatalf("CountForAdmin: %v", err)
		}
		if total != 5 {
			t.Errorf("CountForAdmin default = %d, want 5", total)
		}
		posts, err := repos.AdminPosts.CountForAdmin(ctx, domain.AdminPostFilter{Types: []string{"post"}})
		if err != nil {
			t.Fatalf("CountForAdmin posts: %v", err)
		}
		if posts != 4 {
			t.Errorf("CountForAdmin post-only = %d, want 4", posts)
		}
	})

	t.Run("PostCounter CountByStatus", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		cases := []struct {
			typ, status string
			want        int
		}{
			{"post", "publish", 3},
			{"post", "draft", 1},
			{"page", "", 1}, // page of any status
			{"", "publish", 8},
		}
		for _, c := range cases {
			got, err := repos.PostCounter.CountByStatus(ctx, c.typ, c.status)
			if err != nil {
				t.Fatalf("CountByStatus(%q,%q): %v", c.typ, c.status, err)
			}
			if got != c.want {
				t.Errorf("CountByStatus(%q,%q) = %d, want %d", c.typ, c.status, got, c.want)
			}
		}
	})

	t.Run("TermCounter and UserCounter", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		cats, err := repos.TermCounter.CountTerms(ctx, "category")
		if err != nil {
			t.Fatalf("CountTerms: %v", err)
		}
		if cats != 1 {
			t.Errorf("CountTerms(category) = %d, want 1", cats)
		}
		users, err := repos.UserCounter.CountUsers(ctx)
		if err != nil {
			t.Fatalf("CountUsers: %v", err)
		}
		if users != 1 {
			t.Errorf("CountUsers = %d, want 1", users)
		}
	})
}
