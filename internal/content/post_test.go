package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// fakePostRepo records the arguments it was called with and returns canned data.
type fakePostRepo struct {
	recentLimit, recentOffset int
	recentPosts               []domain.Post
	recentErr                 error

	bySlugSlug  string
	bySlugTypes []string
	bySlugPost  domain.Post
	bySlugErr   error

	termTax, termSlug     string
	termLimit, termOffset int
	termPosts             []domain.Post
	termErr               error
}

func (f *fakePostRepo) RecentPosts(ctx context.Context, limit, offset int) ([]domain.Post, error) {
	f.recentLimit, f.recentOffset = limit, offset
	return f.recentPosts, f.recentErr
}

func (f *fakePostRepo) BySlug(ctx context.Context, slug string, types ...string) (domain.Post, error) {
	f.bySlugSlug, f.bySlugTypes = slug, types
	return f.bySlugPost, f.bySlugErr
}

func (f *fakePostRepo) ByTermSlug(ctx context.Context, taxonomy, termSlug string, limit, offset int) ([]domain.Post, error) {
	f.termTax, f.termSlug, f.termLimit, f.termOffset = taxonomy, termSlug, limit, offset
	return f.termPosts, f.termErr
}

func TestPostServiceRecentClampsPaging(t *testing.T) {
	cases := []struct {
		name                  string
		page, perPage         int
		wantLimit, wantOffset int
	}{
		{"defaults", 0, 0, 10, 0},
		{"cap perPage", 1, 500, 100, 0},
		{"page offset", 3, 10, 10, 20},
		{"negative page", -5, 5, 5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakePostRepo{}
			svc := NewPostService(repo)
			if _, err := svc.Recent(context.Background(), tc.page, tc.perPage); err != nil {
				t.Fatalf("Recent: %v", err)
			}
			if repo.recentLimit != tc.wantLimit || repo.recentOffset != tc.wantOffset {
				t.Fatalf("limit=%d offset=%d, want limit=%d offset=%d",
					repo.recentLimit, repo.recentOffset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}

func TestPostServiceBySlugForwardsTypesAndError(t *testing.T) {
	repo := &fakePostRepo{bySlugErr: domain.ErrNotFound}
	svc := NewPostService(repo)
	_, err := svc.BySlug(context.Background(), "hello")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if repo.bySlugSlug != "hello" {
		t.Fatalf("slug=%q", repo.bySlugSlug)
	}
	if len(repo.bySlugTypes) != 2 || repo.bySlugTypes[0] != "post" || repo.bySlugTypes[1] != "page" {
		t.Fatalf("types=%v, want [post page]", repo.bySlugTypes)
	}
}

type fakePostCounter struct {
	typ, status string
	count       int
	err         error
}

func (f *fakePostCounter) CountByStatus(ctx context.Context, typ, status string) (int, error) {
	f.typ, f.status = typ, status
	return f.count, f.err
}

func TestPostServiceRecentPageReturnsPageWithTotal(t *testing.T) {
	repo := &fakePostRepo{recentPosts: []domain.Post{{ID: 1}, {ID: 2}}}
	counter := &fakePostCounter{count: 25}
	svc := NewPostService(repo).WithCounter(counter)

	posts, page, err := svc.RecentPage(context.Background(), 2, 10)
	if err != nil {
		t.Fatalf("RecentPage: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}
	if page.Page != 2 || page.PerPage != 10 || page.Total != 25 || page.TotalPages != 3 {
		t.Fatalf("page = %+v, want {2 10 25 3}", page)
	}
	if repo.recentLimit != 10 || repo.recentOffset != 10 {
		t.Fatalf("repo called with limit=%d offset=%d, want 10,10", repo.recentLimit, repo.recentOffset)
	}
	if counter.typ != "post" || counter.status != "publish" {
		t.Fatalf("counter called with typ=%q status=%q, want post/publish", counter.typ, counter.status)
	}
}

func TestPostServiceRecentPageZeroTotal(t *testing.T) {
	repo := &fakePostRepo{}
	counter := &fakePostCounter{count: 0}
	svc := NewPostService(repo).WithCounter(counter)
	_, page, err := svc.RecentPage(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("RecentPage: %v", err)
	}
	if page.TotalPages != 0 {
		t.Fatalf("TotalPages = %d, want 0", page.TotalPages)
	}
}
