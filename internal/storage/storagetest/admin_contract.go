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
		if cats != 3 {
			t.Errorf("CountTerms(category) = %d, want 3", cats)
		}
		users, err := repos.UserCounter.CountUsers(ctx)
		if err != nil {
			t.Fatalf("CountUsers: %v", err)
		}
		if users != 1 {
			t.Errorf("CountUsers = %d, want 1", users)
		}
	})

	t.Run("ListForAdmin Search matches title and content, case-insensitively", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		byTitle, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{Search: "hello two"})
		if err != nil {
			t.Fatalf("Search by title: %v", err)
		}
		if len(byTitle) != 1 || byTitle[0].Slug != "hello-2" {
			t.Fatalf("Search(%q) = %+v, want [hello-2]", "hello two", byTitle)
		}
		byContent, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{Search: "BODY"})
		if err != nil {
			t.Fatalf("Search by content: %v", err)
		}
		if len(byContent) == 0 {
			t.Fatalf("Search(%q) matched nothing, want all seeded posts sharing body content", "BODY")
		}
		none, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{Search: "no-such-term-anywhere"})
		if err != nil {
			t.Fatalf("Search no match: %v", err)
		}
		if len(none) != 0 {
			t.Errorf("Search(no-such-term-anywhere) = %+v, want empty", none)
		}
	})

	t.Run("CountForAdmin honors Search", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		n, err := repos.AdminPosts.CountForAdmin(ctx, domain.AdminPostFilter{Search: "hello two"})
		if err != nil {
			t.Fatalf("CountForAdmin search: %v", err)
		}
		if n != 1 {
			t.Errorf("CountForAdmin(Search=hello two) = %d, want 1", n)
		}
	})

	t.Run("ListForAdmin OrderBy/Order override the post_date DESC default", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		byIDAsc, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{
			Types:   []string{"post"},
			OrderBy: "id",
			Order:   "asc",
		})
		if err != nil {
			t.Fatalf("OrderBy id asc: %v", err)
		}
		wantSlugs := []string{"hello-1", "hello-2", "hello-3", "secret"}
		if len(byIDAsc) != len(wantSlugs) {
			t.Fatalf("want %d posts, got %d", len(wantSlugs), len(byIDAsc))
		}
		for i, w := range wantSlugs {
			if byIDAsc[i].Slug != w {
				t.Errorf("post[%d] slug = %q, want %q", i, byIDAsc[i].Slug, w)
			}
		}
	})
}
