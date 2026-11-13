package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// TermService.TaxonomyArchive tests (M12 task 7.1). TaxonomyArchive is the
// registry-driven sibling of CategoryArchive/TagArchive for custom taxonomies.
//
// Assumed signature (design Component 6a, decoupled from the registry package
// so internal/content takes primitives):
//
//	func (s *TermService) TaxonomyArchive(ctx context.Context, taxonomy string,
//	    hierarchical bool, objectTypes []string, segments []string,
//	    page, perPage int) (Archive, error)
//
// The graph mirrors archiveCategoryGraph, under a custom "genre" taxonomy
// attached to the "book" type:
//
//	fiction (1)
//	├── scifi (2)
//	│   └── cyberpunk (4)
//	└── fantasy (3)
//	    └── epic (5)
const genreTaxonomy = "genre"

func genreTerm(id, parentID int64, slug string) domain.Term {
	return domain.Term{ID: id, Name: slug, Slug: slug, Taxonomy: genreTaxonomy, ParentID: parentID}
}

func genreGraph() *fakeTermWriter {
	return &fakeTermWriter{byTaxonomy: map[string][]domain.Term{genreTaxonomy: {
		genreTerm(4, 2, "cyberpunk"),
		genreTerm(5, 3, "epic"),
		genreTerm(2, 1, "scifi"),
		genreTerm(1, 0, "fiction"),
		genreTerm(3, 1, "fantasy"),
	}}}
}

func assertNoPostQueries(t *testing.T, posts *fakePostRepo) {
	t.Helper()
	if posts.archiveFilter.Taxonomy != "" || len(posts.archiveFilter.TermIDs) != 0 || posts.archiveLimit != 0 {
		t.Fatalf("PublishedArchive must not be called: filter=%+v limit=%d", posts.archiveFilter, posts.archiveLimit)
	}
	if posts.archiveCountFilter.Taxonomy != "" || len(posts.archiveCountFilter.TermIDs) != 0 {
		t.Fatalf("CountPublishedArchive must not be called: filter=%+v", posts.archiveCountFilter)
	}
}

func TestTaxonomyArchiveHierarchicalWalksSegmentsAndIncludesDescendants(t *testing.T) {
	terms := &fakeTermRepo{}
	posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 11}, {ID: 12}}, archiveCount: 2}
	svc := NewTermService(terms, posts).WithHierarchy(genreGraph())

	got, err := svc.TaxonomyArchive(context.Background(), genreTaxonomy, true, []string{"book"}, []string{"fiction", "scifi"}, 1, 10)
	if err != nil {
		t.Fatalf("TaxonomyArchive: %v", err)
	}
	if got.Term.ID != 2 || got.Heading != "scifi" {
		t.Fatalf("term=%+v heading=%q, want scifi (id 2)", got.Term, got.Heading)
	}
	assertSlugs(t, got.Ancestry, []string{"fiction", "scifi"})
	if posts.archiveFilter.Taxonomy != genreTaxonomy || posts.archiveCountFilter.Taxonomy != genreTaxonomy {
		t.Fatalf("filter taxonomy = %q/%q, want %q", posts.archiveFilter.Taxonomy, posts.archiveCountFilter.Taxonomy, genreTaxonomy)
	}
	assertIDs(t, posts.archiveFilter.TermIDs, []int64{2, 4})
	assertIDs(t, posts.archiveCountFilter.TermIDs, []int64{2, 4})
	if terms.called {
		t.Fatalf("BySlug must not be used on the hierarchical path")
	}
}

// The wrong-ancestry path reuses *CategoryMovedError deliberately, and must not
// wrap ErrNotFound (a handler checking only ErrNotFound would 404 it).
func TestTaxonomyArchiveWrongAncestryReturnsMovedErrorNotNotFound(t *testing.T) {
	posts := &fakePostRepo{}
	svc := NewTermService(&fakeTermRepo{}, posts).WithHierarchy(genreGraph())

	_, err := svc.TaxonomyArchive(context.Background(), genreTaxonomy, true, []string{"book"}, []string{"fiction", "fantasy", "cyberpunk"}, 1, 10)
	var moved *CategoryMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("want *CategoryMovedError, got %v", err)
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("CategoryMovedError must not wrap domain.ErrNotFound")
	}
	assertSlugs(t, moved.Ancestry, []string{"fiction", "scifi", "cyberpunk"})
	if moved.Term.ID != 4 {
		t.Fatalf("moved term = %+v, want cyberpunk (id 4)", moved.Term)
	}
	assertNoPostQueries(t, posts)
}

func TestTaxonomyArchiveHierarchicalUnknownFinalSegmentIsNotFoundWithoutQueries(t *testing.T) {
	posts := &fakePostRepo{}
	svc := NewTermService(&fakeTermRepo{}, posts).WithHierarchy(genreGraph())

	_, err := svc.TaxonomyArchive(context.Background(), genreTaxonomy, true, []string{"book"}, []string{"fiction", "nope"}, 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	assertNoPostQueries(t, posts)
}

func TestTaxonomyArchiveFlatResolvesSingleSlugWithNoAncestry(t *testing.T) {
	terms := &fakeTermRepo{term: domain.Term{ID: 13, Name: "Hardcover", Slug: "hardcover", Taxonomy: "format"}}
	posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 31}}, archiveCount: 1}
	// The hierarchy reader is wired but must be ignored for a flat taxonomy,
	// as it is for tags.
	svc := NewTermService(terms, posts).WithHierarchy(genreGraph())

	got, err := svc.TaxonomyArchive(context.Background(), "format", false, []string{"book"}, []string{"hardcover"}, 1, 10)
	if err != nil {
		t.Fatalf("TaxonomyArchive: %v", err)
	}
	if !terms.called || terms.tax != "format" || terms.slug != "hardcover" {
		t.Fatalf("BySlug(%q, %q) called=%v, want (format, hardcover)", terms.tax, terms.slug, terms.called)
	}
	if got.Ancestry != nil {
		t.Fatalf("ancestry = %v, want nil for a flat taxonomy", got.Ancestry)
	}
	if got.Heading != "Hardcover" || got.Term.ID != 13 {
		t.Fatalf("heading=%q term=%+v, want Hardcover (id 13)", got.Heading, got.Term)
	}
	if posts.archiveFilter.Taxonomy != "format" {
		t.Fatalf("filter taxonomy = %q, want format", posts.archiveFilter.Taxonomy)
	}
	assertIDs(t, posts.archiveFilter.TermIDs, []int64{13})
	assertIDs(t, posts.archiveCountFilter.TermIDs, []int64{13})
}

func TestTaxonomyArchiveFlatMultipleSegmentsIsNotFoundWithoutQueries(t *testing.T) {
	terms := &fakeTermRepo{term: domain.Term{ID: 13, Slug: "hardcover"}}
	posts := &fakePostRepo{}
	svc := NewTermService(terms, posts)

	_, err := svc.TaxonomyArchive(context.Background(), "format", false, []string{"book"}, []string{"a", "hardcover"}, 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	assertNoPostQueries(t, posts)
}

func TestTaxonomyArchiveFlatUnknownSlugIsNotFoundWithoutQueries(t *testing.T) {
	terms := &fakeTermRepo{err: domain.ErrNotFound}
	posts := &fakePostRepo{}
	svc := NewTermService(terms, posts)

	_, err := svc.TaxonomyArchive(context.Background(), "format", false, []string{"book"}, []string{"nope"}, 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	assertNoPostQueries(t, posts)
}

func TestTaxonomyArchiveExistingTermWithNoPostsIsEmptyNotNotFound(t *testing.T) {
	posts := &fakePostRepo{}
	svc := NewTermService(&fakeTermRepo{}, posts).WithHierarchy(genreGraph())

	got, err := svc.TaxonomyArchive(context.Background(), genreTaxonomy, true, []string{"book"}, []string{"fiction", "fantasy", "epic"}, 1, 10)
	if err != nil {
		t.Fatalf("TaxonomyArchive: %v", err)
	}
	if got.Term.ID != 5 || len(got.Posts) != 0 {
		t.Fatalf("term=%+v posts=%d, want epic (id 5) with no posts", got.Term, len(got.Posts))
	}
	if got.Page.Total != 0 || got.Page.TotalPages != 0 {
		t.Fatalf("page = %+v, want zero Total and TotalPages", got.Page)
	}
}

func TestTaxonomyArchivePaginationUsesSharedPageHelpers(t *testing.T) {
	posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 21}, {ID: 22}}, archiveCount: 12}
	svc := NewTermService(&fakeTermRepo{}, posts).WithHierarchy(genreGraph())

	got, err := svc.TaxonomyArchive(context.Background(), genreTaxonomy, true, []string{"book"}, []string{"fiction"}, 2, 5)
	if err != nil {
		t.Fatalf("TaxonomyArchive: %v", err)
	}
	if posts.archiveLimit != 5 || posts.archiveOffset != 5 {
		t.Fatalf("paging limit=%d offset=%d, want 5/5", posts.archiveLimit, posts.archiveOffset)
	}
	if got.Page.Page != 2 || got.Page.PerPage != 5 || got.Page.Total != 12 || got.Page.TotalPages != 3 {
		t.Fatalf("page = %+v, want {2 5 12 3}", got.Page)
	}
	assertIDs(t, posts.archiveCountFilter.TermIDs, []int64{1, 2, 3, 4, 5})
}

// THE TRAP: ArchiveFilter.Types defaults to {"post"} when empty, so a taxonomy
// archive that does not forward the taxonomy's object types would list posts
// instead of the CPT rows the term is attached to (design Finding 3).
func TestTaxonomyArchiveForwardsObjectTypesToListingAndCount(t *testing.T) {
	for _, tc := range []struct {
		name         string
		hierarchical bool
		segments     []string
	}{
		{"hierarchical", true, []string{"fiction"}},
		{"flat", false, []string{"hardcover"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terms := &fakeTermRepo{term: domain.Term{ID: 13, Slug: "hardcover"}}
			posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 1, Type: "book"}}, archiveCount: 1}
			svc := NewTermService(terms, posts).WithHierarchy(genreGraph())

			if _, err := svc.TaxonomyArchive(context.Background(), genreTaxonomy, tc.hierarchical, []string{"book"}, tc.segments, 1, 10); err != nil {
				t.Fatalf("TaxonomyArchive: %v", err)
			}
			assertSlugs(t, posts.archiveFilter.Types, []string{"book"})
			assertSlugs(t, posts.archiveCountFilter.Types, []string{"book"})
		})
	}
}

// Design Finding 3 / task 7.1: an empty object-type set must short-circuit to
// an empty archive WITHOUT a query, never fall through to the {post} default.
func TestTaxonomyArchiveEmptyObjectTypesIsEmptyArchiveWithoutQueries(t *testing.T) {
	for _, hierarchical := range []bool{true, false} {
		terms := &fakeTermRepo{term: domain.Term{ID: 13, Slug: "hardcover"}}
		posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 99}}, archiveCount: 1}
		svc := NewTermService(terms, posts).WithHierarchy(genreGraph())
		seg := []string{"hardcover"}
		if hierarchical {
			seg = []string{"fiction"}
		}

		got, err := svc.TaxonomyArchive(context.Background(), genreTaxonomy, hierarchical, nil, seg, 1, 10)
		if err != nil {
			t.Fatalf("hierarchical=%v: TaxonomyArchive: %v", hierarchical, err)
		}
		if len(got.Posts) != 0 || got.Page.Total != 0 {
			t.Fatalf("hierarchical=%v: posts=%d total=%d, want an empty archive", hierarchical, len(got.Posts), got.Page.Total)
		}
		assertNoPostQueries(t, posts)
	}
}

// CategoryArchive and TagArchive stay as they were: M12 does not re-point them,
// so their filters keep Types unset and list {post} via the default.
func TestCategoryAndTagArchiveFiltersUnchangedByTaxonomyArchive(t *testing.T) {
	posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 1}}, archiveCount: 1}
	terms := &fakeTermRepo{term: tagTerm(13, 0, "golang")}
	svc := NewTermService(terms, posts).WithHierarchy(archiveCategoryGraph())

	if _, err := svc.CategoryArchive(context.Background(), []string{"news"}, 1, 10); err != nil {
		t.Fatalf("CategoryArchive: %v", err)
	}
	if posts.archiveFilter.Taxonomy != TaxonomyCategory || posts.archiveFilter.Types != nil {
		t.Fatalf("category filter = %+v, want Taxonomy category and Types unset", posts.archiveFilter)
	}
	if posts.archiveCountFilter.Types != nil {
		t.Fatalf("category count filter Types = %v, want unset", posts.archiveCountFilter.Types)
	}

	if _, err := svc.TagArchive(context.Background(), "golang", 1, 10); err != nil {
		t.Fatalf("TagArchive: %v", err)
	}
	if posts.archiveFilter.Taxonomy != taxonomyPostTag || posts.archiveFilter.Types != nil {
		t.Fatalf("tag filter = %+v, want Taxonomy post_tag and Types unset", posts.archiveFilter)
	}
	if posts.archiveCountFilter.Types != nil {
		t.Fatalf("tag count filter Types = %v, want unset", posts.archiveCountFilter.Types)
	}
}
