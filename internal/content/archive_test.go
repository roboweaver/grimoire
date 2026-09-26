package content

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// Hierarchy resolution tests (task 5.1), covering the two directions design.md
// derives from one TermReader.ListByTaxonomy read: ancestry up through
// ParentID (root-first, for the canonical path) and a breadth-first descendant
// expansion down a child index (for the listing and the count).
//
// A fake reader is used rather than a storagetest contract case because the two
// degenerate graphs the requirements name -- a parent pointing at a term that
// is not in the set (Req 1.4) and a parent cycle (Req 1.5) -- are three lines
// of Go here and awkward to seed in SQL on three vendors.
//
// fakeTermWriter (writeservices_test.go) already satisfies domain.TermReader
// through its byTaxonomy map, so these tests reuse it instead of declaring a
// second fake reader in the same package.

// catTerm builds a category term whose Name equals its Slug, so
// ListByTaxonomy's documented name ordering and the slug assertions below read
// the same way.
func catTerm(id, parentID int64, slug string) domain.Term {
	return domain.Term{ID: id, Name: slug, Slug: slug, Taxonomy: TaxonomyCategory, ParentID: parentID}
}

// categoryGraph returns a fake domain.TermReader serving terms as the
// category taxonomy's complete graph.
func categoryGraph(terms ...domain.Term) *fakeTermWriter {
	return &fakeTermWriter{byTaxonomy: map[string][]domain.Term{TaxonomyCategory: terms}}
}

func loadCategoryHierarchy(t *testing.T, r domain.TermReader) *TermHierarchy {
	t.Helper()
	h, err := LoadTermHierarchy(context.Background(), r, TaxonomyCategory)
	if err != nil {
		t.Fatalf("LoadTermHierarchy: %v", err)
	}
	return h
}

func assertSlugs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ancestry = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ancestry = %v, want %v", got, want)
		}
	}
}

func assertIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// A three-level chain resolves root first and includes the term's own slug, so
// the result is exactly what routing.Structure.CategoryPath consumes.
func TestTermHierarchyAncestryThreeLevelChainIsRootFirst(t *testing.T) {
	// Listed in name order, as ListByTaxonomy documents.
	r := categoryGraph(
		catTerm(4, 2, "council"),
		catTerm(2, 1, "local"),
		catTerm(1, 0, "news"),
	)

	h := loadCategoryHierarchy(t, r)

	assertSlugs(t, h.Ancestry(4), []string{"news", "local", "council"})
	assertSlugs(t, h.Ancestry(2), []string{"news", "local"})
	assertSlugs(t, h.Ancestry(1), []string{"news"})

	if r.lastTaxonomyArg != TaxonomyCategory {
		t.Fatalf("ListByTaxonomy taxonomy = %q, want %q", r.lastTaxonomyArg, TaxonomyCategory)
	}
}

// Req 1.4: an imported database contains orphaned rows, so a ParentID naming a
// term that is absent from the set terminates the walk and yields the shorter
// path rather than an error.
func TestTermHierarchyAncestryOrphanedParentTerminatesWalk(t *testing.T) {
	r := categoryGraph(
		catTerm(4, 2, "council"),
		catTerm(2, 99, "local"), // parent 99 was never imported
	)

	h := loadCategoryHierarchy(t, r)

	assertSlugs(t, h.Ancestry(4), []string{"local", "council"})
	assertSlugs(t, h.Ancestry(2), []string{"local"})
}

// Req 1.5: a corrupted parent cycle terminates on the first already-visited
// term rather than hanging the request.
func TestTermHierarchyAncestryCycleTerminatesOnFirstRepeatedTerm(t *testing.T) {
	r := categoryGraph(
		catTerm(1, 2, "alpha"),
		catTerm(2, 1, "beta"),
	)

	h := loadCategoryHierarchy(t, r)

	// beta -> alpha -> beta, which is already visited, so the walk stops with
	// each term reported once.
	assertSlugs(t, h.Ancestry(2), []string{"alpha", "beta"})
	assertSlugs(t, h.Ancestry(1), []string{"beta", "alpha"})
}

// Req 3.1: the descendant set backing the listing and the count is collected
// breadth-first and includes the term itself, so an archive lists its own posts
// plus its descendants'. The expected order distinguishes breadth-first from
// depth-first: depth-first would yield 1, 2, 4, 3, 5.
func TestTermHierarchyDescendantIDsAreBreadthFirstAcrossTwoLevels(t *testing.T) {
	r := categoryGraph(
		catTerm(4, 2, "council"),
		catTerm(5, 3, "fixtures"),
		catTerm(2, 1, "local"),
		catTerm(1, 0, "news"),
		catTerm(3, 1, "sport"),
	)

	h := loadCategoryHierarchy(t, r)

	assertIDs(t, h.DescendantIDs(1), []int64{1, 2, 3, 4, 5})
	assertIDs(t, h.DescendantIDs(2), []int64{2, 4})
	assertIDs(t, h.DescendantIDs(5), []int64{5})
}

// TermService.CategoryArchive tests (task 5.3). The service resolves a
// category by walking the request's segment path through the taxonomy graph
// (Req 2.10) and lists the descendant-inclusive published set (Req 3.1) with
// the shared Page contract (Req 8.1).
//
// The graph below is the one the hierarchy tests above use, so the descendant
// expectations read the same way:
//
//	news (1)
//	├── local (2)
//	│   └── council (4)
//	└── sport (3)
//	    └── fixtures (5)
func archiveCategoryGraph() *fakeTermWriter {
	return categoryGraph(
		catTerm(4, 2, "council"),
		catTerm(5, 3, "fixtures"),
		catTerm(2, 1, "local"),
		catTerm(1, 0, "news"),
		catTerm(3, 1, "sport"),
	)
}

// The canonical nested path resolves the term the last segment names, reports
// its root-first ancestry, and asks the post repository for the term's full
// descendant set -- not just the term itself, and not the whole taxonomy.
func TestTermServiceCategoryArchiveWalksSegmentsAndIncludesDescendants(t *testing.T) {
	terms := &fakeTermRepo{}
	posts := &fakePostRepo{
		archivePosts: []domain.Post{{ID: 11, Slug: "a"}, {ID: 12, Slug: "b"}},
		archiveCount: 2,
	}
	svc := NewTermService(terms, posts).WithHierarchy(archiveCategoryGraph())

	got, err := svc.CategoryArchive(context.Background(), []string{"news", "local"}, 1, 10)
	if err != nil {
		t.Fatalf("CategoryArchive: %v", err)
	}

	if got.Term.ID != 2 || got.Term.Slug != "local" {
		t.Fatalf("term = %+v, want the term named by the final segment (id 2, local)", got.Term)
	}
	if got.Heading != got.Term.Name {
		t.Fatalf("heading = %q, want the term name %q", got.Heading, got.Term.Name)
	}
	assertSlugs(t, got.Ancestry, []string{"news", "local"})
	if len(got.Posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(got.Posts))
	}

	// Req 3.1: the listing selects the term *and* its descendants, so a
	// parent's archive shows the posts its children hold. council (4) is
	// local's child; sport and fixtures are not beneath local and must not
	// appear.
	if posts.archiveFilter.Taxonomy != TaxonomyCategory {
		t.Fatalf("filter taxonomy = %q, want %q", posts.archiveFilter.Taxonomy, TaxonomyCategory)
	}
	assertIDs(t, posts.archiveFilter.TermIDs, []int64{2, 4})
	assertIDs(t, posts.archiveCountFilter.TermIDs, []int64{2, 4})

	// Req 2.10: the category path resolves through the graph, so the
	// single-slug read is not on it at all.
	if terms.called {
		t.Fatalf("TermRepository.BySlug must not be used on the category path")
	}
}

// Req 2.5 / Req 3.2: an unknown final segment is ErrNotFound, and it is
// reported before any post read -- the same not-found-skips-count behavior
// CategoryPage has today, so a 404 costs no listing query and no count query.
func TestTermServiceCategoryArchiveUnknownFinalSegmentSkipsPostQueries(t *testing.T) {
	posts := &fakePostRepo{}
	svc := NewTermService(&fakeTermRepo{}, posts).WithHierarchy(archiveCategoryGraph())

	_, err := svc.CategoryArchive(context.Background(), []string{"news", "nope"}, 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if posts.archiveFilter.Taxonomy != "" || len(posts.archiveFilter.TermIDs) != 0 || posts.archiveLimit != 0 {
		t.Fatalf("PublishedArchive must not be called for an unknown segment: filter=%+v limit=%d",
			posts.archiveFilter, posts.archiveLimit)
	}
	if posts.archiveCountFilter.Taxonomy != "" || len(posts.archiveCountFilter.TermIDs) != 0 {
		t.Fatalf("CountPublishedArchive must not be called for an unknown segment: filter=%+v",
			posts.archiveCountFilter)
	}
}

// Req 2.6: a category that exists but holds no published posts is an empty
// archive, not a not-found -- the term resolved, so there is nothing absent.
func TestTermServiceCategoryArchiveExistingCategoryWithNoPostsIsEmpty(t *testing.T) {
	posts := &fakePostRepo{}
	svc := NewTermService(&fakeTermRepo{}, posts).WithHierarchy(archiveCategoryGraph())

	got, err := svc.CategoryArchive(context.Background(), []string{"news", "sport", "fixtures"}, 1, 10)
	if err != nil {
		t.Fatalf("CategoryArchive: %v", err)
	}
	if got.Term.ID != 5 {
		t.Fatalf("term = %+v, want fixtures (id 5)", got.Term)
	}
	if len(got.Posts) != 0 {
		t.Fatalf("posts = %d, want 0", len(got.Posts))
	}
	if got.Page.Total != 0 || got.Page.TotalPages != 0 {
		t.Fatalf("page = %+v, want zero Total and zero TotalPages", got.Page)
	}
}

// Req 8.1: pagination comes from the shared clamp/newPage helpers, so the
// limit/offset handed to the repository and the returned Page are the same
// contract the home and category pages already use -- no second shape.
func TestTermServiceCategoryArchivePaginationUsesSharedPageHelpers(t *testing.T) {
	posts := &fakePostRepo{
		archivePosts: []domain.Post{{ID: 21}, {ID: 22}},
		archiveCount: 12,
	}
	svc := NewTermService(&fakeTermRepo{}, posts).WithHierarchy(archiveCategoryGraph())

	got, err := svc.CategoryArchive(context.Background(), []string{"news"}, 2, 5)
	if err != nil {
		t.Fatalf("CategoryArchive: %v", err)
	}
	if posts.archiveLimit != 5 || posts.archiveOffset != 5 {
		t.Fatalf("paging limit=%d offset=%d, want 5/5", posts.archiveLimit, posts.archiveOffset)
	}
	if got.Page.Page != 2 || got.Page.PerPage != 5 || got.Page.Total != 12 || got.Page.TotalPages != 3 {
		t.Fatalf("page = %+v, want {2 5 12 3}", got.Page)
	}
	// Req 3.2: the total counts the same descendant-inclusive set the listing
	// selected, so Page.Total describes the rows actually listed.
	assertIDs(t, posts.archiveCountFilter.TermIDs, []int64{1, 2, 3, 4, 5})
}

// TermService.TagArchive tests (task 5.5). Tags are flat: post_tag is
// non-hierarchical in WordPress, so a tag archive resolves by slug (Req 5.1),
// carries no ancestry and expands to no descendants (Req 5.2), and
// term_taxonomy.parent is ignored for the taxonomy even though the column
// exists for every taxonomy and can legally carry a non-zero value.
//
// These read the same way the CategoryArchive tests above do -- same fakes,
// same helpers -- so the contrast between the two kinds is visible in the
// assertions rather than only in the prose.

// tagTerm builds a post_tag term whose Name equals its Slug, mirroring
// catTerm. parentID is a parameter rather than a hard-coded 0 precisely so the
// ignored-parent case can seed a non-zero one.
func tagTerm(id, parentID int64, slug string) domain.Term {
	return domain.Term{ID: id, Name: slug, Slug: slug, Taxonomy: taxonomyPostTag, ParentID: parentID}
}

// tagGraph returns a fake domain.TermReader serving terms as the post_tag
// taxonomy's graph. Nothing on the tag path should ever read it; it exists so
// the tests below can assert that.
func tagGraph(terms ...domain.Term) *fakeTermWriter {
	return &fakeTermWriter{byTaxonomy: map[string][]domain.Term{taxonomyPostTag: terms}}
}

// Req 5.1 / 5.2: a tag resolves by slug through the single-slug term read, and
// the archive it returns has no ancestry and selects exactly one term.
func TestTermServiceTagArchiveResolvesBySlugWithNoAncestry(t *testing.T) {
	terms := &fakeTermRepo{term: tagTerm(13, 0, "golang")}
	posts := &fakePostRepo{
		archivePosts: []domain.Post{{ID: 31, Slug: "a"}, {ID: 32, Slug: "b"}},
		archiveCount: 2,
	}
	svc := NewTermService(terms, posts)

	got, err := svc.TagArchive(context.Background(), "golang", 1, 10)
	if err != nil {
		t.Fatalf("TagArchive: %v", err)
	}

	if !terms.called {
		t.Fatalf("TagArchive must resolve the tag through TermRepository.BySlug")
	}
	if terms.tax != taxonomyPostTag || terms.slug != "golang" {
		t.Fatalf("BySlug(%q, %q), want (%q, %q)", terms.tax, terms.slug, taxonomyPostTag, "golang")
	}
	if got.Term.ID != 13 || got.Term.Slug != "golang" {
		t.Fatalf("term = %+v, want the resolved tag (id 13, golang)", got.Term)
	}
	if got.Heading != got.Term.Name {
		t.Fatalf("heading = %q, want the term name %q", got.Heading, got.Term.Name)
	}
	if got.Ancestry != nil {
		t.Fatalf("ancestry = %v, want nil: post_tag is flat (Req 5.2)", got.Ancestry)
	}
	if len(got.Posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(got.Posts))
	}
	if posts.archiveFilter.Taxonomy != taxonomyPostTag {
		t.Fatalf("filter taxonomy = %q, want %q", posts.archiveFilter.Taxonomy, taxonomyPostTag)
	}
	assertIDs(t, posts.archiveFilter.TermIDs, []int64{13})
	assertIDs(t, posts.archiveCountFilter.TermIDs, []int64{13})
}

// Req 5.2: term_taxonomy.parent is schema-legal for every taxonomy -- the
// column exists on the row whatever the taxonomy is -- so a post_tag row can
// carry a non-zero parent, and an imported database may well have one. The tag
// archive must ignore it in both directions: no ancestry upward from the
// parent, and no descendant expansion downward to the child. The TermIDs handed
// to the repository are the single tag, never a set.
//
// The taxonomy graph is wired here as well, holding the parent and the child,
// so an implementation that reached for LoadTermHierarchy on the tag path
// fails this case rather than passing it by having nothing to read.
func TestTermServiceTagArchiveIgnoresTermTaxonomyParent(t *testing.T) {
	// The resolved tag itself carries parent 7, as the row legally may.
	terms := &fakeTermRepo{term: tagTerm(13, 7, "golang")}
	posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 41}}, archiveCount: 1}
	hier := tagGraph(
		tagTerm(7, 0, "languages"), // would be golang's ancestor if parent applied
		tagTerm(13, 7, "golang"),
		tagTerm(21, 13, "generics"), // would be golang's descendant if parent applied
	)
	svc := NewTermService(terms, posts).WithHierarchy(hier)

	got, err := svc.TagArchive(context.Background(), "golang", 1, 10)
	if err != nil {
		t.Fatalf("TagArchive: %v", err)
	}

	if got.Ancestry != nil {
		t.Fatalf("ancestry = %v, want nil: the parent must not produce an ancestry for post_tag", got.Ancestry)
	}
	assertIDs(t, posts.archiveFilter.TermIDs, []int64{13})
	assertIDs(t, posts.archiveCountFilter.TermIDs, []int64{13})
	if hier.lastTaxonomyArg != "" {
		t.Fatalf("ListByTaxonomy(%q) called, want no taxonomy graph read on the flat tag path",
			hier.lastTaxonomyArg)
	}
}

// Req 5.3: an unknown slug is ErrNotFound, reported before any post read --
// the same not-found-skips-query behavior CategoryArchive and CategoryPage
// have, so a 404 costs no listing query and no count query.
func TestTermServiceTagArchiveUnknownSlugSkipsPostQueries(t *testing.T) {
	terms := &fakeTermRepo{err: domain.ErrNotFound}
	posts := &fakePostRepo{}
	svc := NewTermService(terms, posts)

	_, err := svc.TagArchive(context.Background(), "nope", 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if posts.archiveFilter.Taxonomy != "" || len(posts.archiveFilter.TermIDs) != 0 || posts.archiveLimit != 0 {
		t.Fatalf("PublishedArchive must not be called for an unknown tag: filter=%+v limit=%d",
			posts.archiveFilter, posts.archiveLimit)
	}
	if posts.archiveCountFilter.Taxonomy != "" || len(posts.archiveCountFilter.TermIDs) != 0 {
		t.Fatalf("CountPublishedArchive must not be called for an unknown tag: filter=%+v",
			posts.archiveCountFilter)
	}
}

// Req 5.4: a tag that exists but holds no published posts is an empty archive
// with a zero Total, not a not-found -- the term resolved, so nothing is
// absent.
func TestTermServiceTagArchiveExistingTagWithNoPostsIsEmpty(t *testing.T) {
	terms := &fakeTermRepo{term: tagTerm(13, 0, "golang")}
	posts := &fakePostRepo{}
	svc := NewTermService(terms, posts)

	got, err := svc.TagArchive(context.Background(), "golang", 1, 10)
	if err != nil {
		t.Fatalf("TagArchive: %v", err)
	}
	if got.Term.ID != 13 {
		t.Fatalf("term = %+v, want the resolved tag (id 13)", got.Term)
	}
	if len(got.Posts) != 0 {
		t.Fatalf("posts = %d, want 0", len(got.Posts))
	}
	if got.Page.Total != 0 || got.Page.TotalPages != 0 {
		t.Fatalf("page = %+v, want zero Total and zero TotalPages", got.Page)
	}
}

// PostService.AuthorArchive and PostService.DateArchive tests (task 5.8).
// These two archives are post listings with one extra predicate rather than
// taxonomy reads, which is why they live on the service that already owns
// RecentPage: the author archive adds ArchiveFilter.AuthorID, the date archive
// adds the half-open [Start, End) interval, and neither carries a term.
//
// The Archive they return therefore has a zero Term and a nil Ancestry, and its
// Heading is the only place the author's or the date's identity appears
// (design.md, internal/content). That is load-bearing for Req 7.4 rather than
// incidental -- see assertNoLoginDisclosure.

// archiveAuthorLogin is the resolved author's user_login. It is deliberately a
// value nothing else in the fixture carries and no other field could plausibly
// hold, so the disclosure walk below can look for it anywhere in the returned
// Archive and a hit means a real leak rather than a coincidence.
const archiveAuthorLogin = "jdoe-login-never-rendered"

// archiveAuthor is the author the tests resolve: a user whose display_name,
// user_nicename and user_login are three distinct strings, so an assertion
// about one of them cannot pass by accident on another.
func archiveAuthor() domain.User {
	return domain.User{
		ID:          7,
		Login:       archiveAuthorLogin,
		Nicename:    "jane-doe",
		DisplayName: "Jane Doe",
	}
}

// recordingUserRepo records what ByNicename was asked for and how often, and
// delegates the lookup itself to fakeUserRepo (userservice_test.go) -- which
// already implements the lowest-ID-wins resolution of Req 7.6 -- rather than
// declaring a second domain.UserRepository fake in the same package.
type recordingUserRepo struct {
	*fakeUserRepo
	nicenameArg   string
	nicenameCalls int
}

func newRecordingUserRepo(users ...domain.User) *recordingUserRepo {
	f := newFakeUserRepo()
	for _, u := range users {
		f.byID[u.ID] = u
	}
	return &recordingUserRepo{fakeUserRepo: f}
}

func (r *recordingUserRepo) ByNicename(ctx context.Context, nicename string) (domain.User, error) {
	r.nicenameArg = nicename
	r.nicenameCalls++
	return r.fakeUserRepo.ByNicename(ctx, nicename)
}

// assertNoLoginDisclosure fails when login appears in any exported string
// reachable from the returned Archive.
//
// Req 7.4 is a disclosure guarantee about the whole result, not about Heading
// alone, so it is asserted that way. design.md's Archive carries no user field
// at all -- Heading holds the display_name and nothing else holds the user --
// and this walk is what keeps that true: a later change adding a
// domain.User-shaped field to satisfy some handler's need for the display name
// would surface Login through it and fail here, rather than quietly publishing
// a login name on a public archive page.
//
// Only exported fields are walked, which is both cheaper and more accurate:
// html/template reads exported fields only, so an unexported one cannot reach a
// rendered page, and skipping them keeps the walk out of time.Time's internals.
func assertNoLoginDisclosure(t *testing.T, got Archive, login string) {
	t.Helper()
	assertNoStringContains(t, reflect.ValueOf(got), login, "Archive")
}

func assertNoStringContains(t *testing.T, v reflect.Value, needle, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		if strings.Contains(v.String(), needle) {
			t.Fatalf("%s = %q discloses user_login %q; the author archive must render display_name only (Req 7.4)",
				path, v.String(), needle)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			assertNoStringContains(t, v.Field(i), needle, path+"."+f.Name)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			assertNoStringContains(t, v.Index(i), needle, fmt.Sprintf("%s[%d]", path, i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			assertNoStringContains(t, v.MapIndex(k), needle, fmt.Sprintf("%s[%v]", path, k))
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			assertNoStringContains(t, v.Elem(), needle, path)
		}
	}
}

// Req 7.1 / 7.4: the author is resolved by user_nicename, the heading is the
// display_name, and the listing filters by the resolved user's ID -- not by the
// nicename string, which is a URL component rather than a foreign key.
func TestPostServiceAuthorArchiveResolvesByNicenameAndHeadsWithDisplayName(t *testing.T) {
	users := newRecordingUserRepo(archiveAuthor())
	posts := &fakePostRepo{
		archivePosts: []domain.Post{{ID: 51, Slug: "a"}, {ID: 52, Slug: "b"}},
		archiveCount: 2,
	}
	svc := NewPostService(posts).WithAuthors(users)

	got, err := svc.AuthorArchive(context.Background(), "jane-doe", 2, 5)
	if err != nil {
		t.Fatalf("AuthorArchive: %v", err)
	}

	if users.nicenameCalls != 1 || users.nicenameArg != "jane-doe" {
		t.Fatalf("ByNicename calls=%d arg=%q, want 1 call for %q (Req 7.1)",
			users.nicenameCalls, users.nicenameArg, "jane-doe")
	}
	if got.Heading != "Jane Doe" {
		t.Fatalf("heading = %q, want the author's display_name %q (Req 7.4)", got.Heading, "Jane Doe")
	}
	if len(got.Posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(got.Posts))
	}

	// The author archive is a post listing with one extra predicate: the
	// resolved user's ID, and no taxonomy and no date bounds.
	if posts.archiveFilter.AuthorID != 7 {
		t.Fatalf("filter AuthorID = %d, want the resolved user's ID 7", posts.archiveFilter.AuthorID)
	}
	if posts.archiveCountFilter.AuthorID != 7 {
		t.Fatalf("count filter AuthorID = %d, want the resolved user's ID 7", posts.archiveCountFilter.AuthorID)
	}
	if posts.archiveFilter.Taxonomy != "" || len(posts.archiveFilter.TermIDs) != 0 {
		t.Fatalf("filter = %+v, want no taxonomy predicate on the author archive", posts.archiveFilter)
	}
	if !posts.archiveFilter.Start.IsZero() || !posts.archiveFilter.End.IsZero() {
		t.Fatalf("filter = %+v, want unbounded dates on the author archive", posts.archiveFilter)
	}

	// The archive carries no term and no ancestry: there is no taxonomy here.
	if got.Term != (domain.Term{}) {
		t.Fatalf("term = %+v, want the zero value for an author archive", got.Term)
	}
	if got.Ancestry != nil {
		t.Fatalf("ancestry = %v, want nil for an author archive", got.Ancestry)
	}

	// Req 8.1: one pagination shape, from the shared clamp/newPage helpers.
	if posts.archiveLimit != 5 || posts.archiveOffset != 5 {
		t.Fatalf("paging limit=%d offset=%d, want 5/5", posts.archiveLimit, posts.archiveOffset)
	}
	if got.Page.Page != 2 || got.Page.PerPage != 5 || got.Page.Total != 2 || got.Page.TotalPages != 1 {
		t.Fatalf("page = %+v, want {2 5 2 1}", got.Page)
	}
}

// Req 7.4: user_login is not rendered, so the archive does not publish a login
// name the site had not already exposed. Asserted over the whole returned
// value, because the guarantee is about disclosure rather than about one field
// -- see assertNoLoginDisclosure for why the walk exists rather than a single
// Heading comparison.
func TestPostServiceAuthorArchiveNeverExposesUserLogin(t *testing.T) {
	users := newRecordingUserRepo(archiveAuthor())
	posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 61, Slug: "a"}}, archiveCount: 1}
	svc := NewPostService(posts).WithAuthors(users)

	got, err := svc.AuthorArchive(context.Background(), "jane-doe", 1, 10)
	if err != nil {
		t.Fatalf("AuthorArchive: %v", err)
	}

	assertNoLoginDisclosure(t, got, archiveAuthorLogin)
}

// Req 7.3: an unknown nicename is ErrNotFound, reported before any post read --
// the same not-found-skips-query behavior CategoryArchive and TagArchive have,
// so a 404 costs no listing query and no count query.
func TestPostServiceAuthorArchiveUnknownNicenameSkipsPostQueries(t *testing.T) {
	users := newRecordingUserRepo(archiveAuthor())
	posts := &fakePostRepo{}
	svc := NewPostService(posts).WithAuthors(users)

	_, err := svc.AuthorArchive(context.Background(), "nobody", 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if posts.archiveFilter.AuthorID != 0 || posts.archiveLimit != 0 {
		t.Fatalf("PublishedArchive must not be called for an unknown nicename: filter=%+v limit=%d",
			posts.archiveFilter, posts.archiveLimit)
	}
	if posts.archiveCountFilter.AuthorID != 0 {
		t.Fatalf("CountPublishedArchive must not be called for an unknown nicename: filter=%+v",
			posts.archiveCountFilter)
	}
}

// Req 7.3: an author who exists but has published nothing is an empty archive
// with a zero Total, not a not-found -- the user resolved, so nothing is
// absent. The heading is still the display_name, so the page has something to
// render.
func TestPostServiceAuthorArchiveExistingAuthorWithNoPostsIsEmpty(t *testing.T) {
	users := newRecordingUserRepo(archiveAuthor())
	posts := &fakePostRepo{}
	svc := NewPostService(posts).WithAuthors(users)

	got, err := svc.AuthorArchive(context.Background(), "jane-doe", 1, 10)
	if err != nil {
		t.Fatalf("AuthorArchive: %v", err)
	}
	if got.Heading != "Jane Doe" {
		t.Fatalf("heading = %q, want %q even with no posts", got.Heading, "Jane Doe")
	}
	if len(got.Posts) != 0 {
		t.Fatalf("posts = %d, want 0", len(got.Posts))
	}
	if got.Page.Total != 0 || got.Page.TotalPages != 0 {
		t.Fatalf("page = %+v, want zero Total and zero TotalPages", got.Page)
	}
	// The read still happened -- an empty archive is a query that matched
	// nothing, not a skipped query.
	if posts.archiveFilter.AuthorID != 7 {
		t.Fatalf("filter AuthorID = %d, want 7: an empty archive still issues its read",
			posts.archiveFilter.AuthorID)
	}
}

// The date archive's heading is formatted in content rather than in the
// template, at the granularity the DateRef carries (design.md, internal/content).
func TestPostServiceDateArchiveHeadingIsFormattedPerGranularity(t *testing.T) {
	cases := []struct {
		name string
		ref  routing.DateRef
		want string
	}{
		{"year", routing.DateRef{Year: 2024}, "2024"},
		{"year and month", routing.DateRef{Year: 2024, Month: 5}, "May 2024"},
		{"year month and day", routing.DateRef{Year: 2024, Month: 5, Day: 17}, "17 May 2024"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			posts := &fakePostRepo{archivePosts: []domain.Post{{ID: 71}}, archiveCount: 1}
			svc := NewPostService(posts)

			got, err := svc.DateArchive(context.Background(), tc.ref, 1, 10)
			if err != nil {
				t.Fatalf("DateArchive: %v", err)
			}
			if got.Heading != tc.want {
				t.Fatalf("heading = %q, want %q", got.Heading, tc.want)
			}
			if got.Term != (domain.Term{}) || got.Ancestry != nil {
				t.Fatalf("archive = %+v, want a zero Term and a nil Ancestry for a date archive", got)
			}
		})
	}
}

// Req 6.4: the range is the half-open [Start, End) interval DateRef.Range
// builds, passed through unchanged to both the listing and the count -- so the
// UTC basis Range establishes is not re-derived here and cannot drift from it,
// and the bounds the count applies are the bounds the listing applied.
func TestPostServiceDateArchivePassesDateRefRangeUnchanged(t *testing.T) {
	cases := []routing.DateRef{
		{Year: 2024},
		{Year: 2024, Month: 5},
		{Year: 2024, Month: 5, Day: 17},
	}
	for _, ref := range cases {
		t.Run(fmt.Sprintf("%04d-%02d-%02d", ref.Year, ref.Month, ref.Day), func(t *testing.T) {
			wantStart, wantEnd, ok := ref.Range()
			if !ok {
				t.Fatalf("DateRef%+v.Range() reported an impossible date", ref)
			}

			posts := &fakePostRepo{}
			svc := NewPostService(posts)

			if _, err := svc.DateArchive(context.Background(), ref, 1, 10); err != nil {
				t.Fatalf("DateArchive: %v", err)
			}

			if !posts.archiveFilter.Start.Equal(wantStart) || !posts.archiveFilter.End.Equal(wantEnd) {
				t.Fatalf("filter range = [%s, %s), want [%s, %s)",
					posts.archiveFilter.Start, posts.archiveFilter.End, wantStart, wantEnd)
			}
			if !posts.archiveCountFilter.Start.Equal(wantStart) || !posts.archiveCountFilter.End.Equal(wantEnd) {
				t.Fatalf("count filter range = [%s, %s), want [%s, %s)",
					posts.archiveCountFilter.Start, posts.archiveCountFilter.End, wantStart, wantEnd)
			}
			// A date archive selects by date alone: no taxonomy, no author.
			if posts.archiveFilter.Taxonomy != "" || len(posts.archiveFilter.TermIDs) != 0 || posts.archiveFilter.AuthorID != 0 {
				t.Fatalf("filter = %+v, want only the date predicate", posts.archiveFilter)
			}
		})
	}
}

// Req 6.3: components that do not form a real calendar date are rejected rather
// than queried. domain.ErrNotFound is the error the web layer already maps to
// 404, which is what makes "404 rather than querying" reachable from here.
//
// Classify rejects these paths before a handler is reached, so in production
// this is a second line of defence -- and that is exactly why it is asserted:
// the guarantee is "no query", and a service that trusted its caller would
// issue one.
func TestPostServiceDateArchiveImpossibleDateIssuesNoQuery(t *testing.T) {
	cases := []struct {
		name string
		ref  routing.DateRef
	}{
		{"day past the end of the month", routing.DateRef{Year: 2024, Month: 2, Day: 30}},
		{"month past december", routing.DateRef{Year: 2024, Month: 13, Day: 1}},
		{"day without a month", routing.DateRef{Year: 2024, Day: 17}},
		{"no year", routing.DateRef{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			posts := &fakePostRepo{}
			svc := NewPostService(posts)

			_, err := svc.DateArchive(context.Background(), tc.ref, 1, 10)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("want ErrNotFound for %+v, got %v", tc.ref, err)
			}
			if !posts.archiveFilter.Start.IsZero() || posts.archiveLimit != 0 {
				t.Fatalf("PublishedArchive must not be called for an impossible date: filter=%+v limit=%d",
					posts.archiveFilter, posts.archiveLimit)
			}
			if !posts.archiveCountFilter.Start.IsZero() {
				t.Fatalf("CountPublishedArchive must not be called for an impossible date: filter=%+v",
					posts.archiveCountFilter)
			}
		})
	}
}

// Req 6.5: a real date that matches no published post is an empty archive with
// a zero Total, not a not-found. There is no entity to be absent here -- a
// month either contains posts or does not -- so the shape the handler renders
// is the same one a populated archive has, with an empty listing.
func TestPostServiceDateArchiveEmptyRangeIsEmptyArchiveNotNotFound(t *testing.T) {
	posts := &fakePostRepo{}
	svc := NewPostService(posts)

	got, err := svc.DateArchive(context.Background(), routing.DateRef{Year: 2024, Month: 5}, 1, 10)
	if err != nil {
		t.Fatalf("DateArchive: %v", err)
	}
	if got.Heading != "May 2024" {
		t.Fatalf("heading = %q, want %q even with no posts", got.Heading, "May 2024")
	}
	if len(got.Posts) != 0 {
		t.Fatalf("posts = %d, want 0", len(got.Posts))
	}
	if got.Page.Page != 1 || got.Page.PerPage != 10 || got.Page.Total != 0 || got.Page.TotalPages != 0 {
		t.Fatalf("page = %+v, want {1 10 0 0}", got.Page)
	}
	// The read happened and matched nothing, rather than being skipped.
	if posts.archiveFilter.Start.IsZero() {
		t.Fatalf("filter = %+v, want the May 2024 range: an empty archive still issues its read",
			posts.archiveFilter)
	}
}
