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
