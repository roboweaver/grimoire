package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

type fakeTermRepo struct {
	tax, slug      string
	term           domain.Term
	err            error
	called         bool
	countCalled    bool
	countPublished int
}

func (f *fakeTermRepo) BySlug(ctx context.Context, taxonomy, slug string) (domain.Term, error) {
	f.called = true
	f.tax, f.slug = taxonomy, slug
	return f.term, f.err
}

func (f *fakeTermRepo) CountPublishedByTermSlug(ctx context.Context, taxonomy, termSlug string) (int, error) {
	f.countCalled = true
	return f.countPublished, f.err
}

func TestTermServiceCategoryUnknownReturnsNotFound(t *testing.T) {
	terms := &fakeTermRepo{err: domain.ErrNotFound}
	posts := &fakePostRepo{}
	svc := NewTermService(terms, posts)

	_, _, err := svc.Category(context.Background(), "nope", 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if posts.termTax != "" || posts.termSlug != "" {
		t.Fatalf("posts repo should not be called on unknown term")
	}
	if terms.tax != "category" {
		t.Fatalf("taxonomy=%q, want category", terms.tax)
	}
}

func TestTermServiceCategoryKnownListsPosts(t *testing.T) {
	terms := &fakeTermRepo{term: domain.Term{ID: 10, Slug: "news", Taxonomy: "category"}}
	posts := &fakePostRepo{termPosts: []domain.Post{{Slug: "a"}, {Slug: "b"}}}
	svc := NewTermService(terms, posts)

	term, list, err := svc.Category(context.Background(), "news", 2, 5)
	if err != nil {
		t.Fatalf("Category: %v", err)
	}
	if term.Slug != "news" || len(list) != 2 {
		t.Fatalf("term=%v posts=%d", term, len(list))
	}
	if posts.termTax != "category" || posts.termSlug != "news" {
		t.Fatalf("forwarded tax=%q slug=%q", posts.termTax, posts.termSlug)
	}
	if posts.termLimit != 5 || posts.termOffset != 5 {
		t.Fatalf("paging limit=%d offset=%d, want 5/5", posts.termLimit, posts.termOffset)
	}
}

func TestTermServiceCategoryPageReturnsTotal(t *testing.T) {
	terms := &fakeTermRepo{term: domain.Term{ID: 10, Slug: "news"}, countPublished: 25}
	posts := &fakePostRepo{termPosts: []domain.Post{{ID: 1}, {ID: 2}}}
	svc := NewTermService(terms, posts)

	_, got, page, err := svc.CategoryPage(context.Background(), "news", 2, 10)
	if err != nil {
		t.Fatalf("CategoryPage: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("posts = %d, want 2", len(got))
	}
	if page.Page != 2 || page.PerPage != 10 || page.Total != 25 || page.TotalPages != 3 {
		t.Fatalf("page = %+v, want {2 10 25 3}", page)
	}
}

func TestTermServiceCategoryPageNotFoundSkipsCount(t *testing.T) {
	terms := &fakeTermRepo{err: domain.ErrNotFound}
	posts := &fakePostRepo{}
	svc := NewTermService(terms, posts)
	_, _, _, err := svc.CategoryPage(context.Background(), "missing", 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if terms.countCalled {
		t.Fatalf("CountPublishedByTermSlug should not be called when term lookup fails")
	}
}
