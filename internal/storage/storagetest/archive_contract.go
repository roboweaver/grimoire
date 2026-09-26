package storagetest

import (
	"context"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// archiveLimit is a page size larger than the fixture set, so a case that means
// "everything matching" is not also quietly asserting a page boundary.
const archiveLimit = 100

// RunArchiveContract covers PostRepository.PublishedArchive and
// CountPublishedArchive — the descendant-inclusive listing and its count
// (Req 3.1-3.5, 6.4, 12.2).
//
// newArchiveRepos MUST build a backend carrying SeedArchiveFixtures on top of
// SeedFixtures. It is a separate backend from RunContract's for the reason
// SeedNestedCategories documents: several assertions there are absolute
// (exactly 3 published posts in a fixed slug order, exactly 3 category terms, a
// user Count of 3), and this suite needs four more posts and a second user.
//
// Every case asserts the count alongside the listing. That pairing is the point
// rather than thoroughness: Req 3.2 requires Page.Total to describe the rows
// actually listed, so a count that agrees with the listing on the duplicate,
// draft, page and boundary rows is the only evidence that M8's pagination totals
// survive descendant inclusion.
func RunArchiveContract(t *testing.T, newArchiveRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	// assertArchive runs one filter and checks the listing's exact slug order
	// and that the count agrees with it.
	assertArchive := func(t *testing.T, repo domain.PostRepository, label string,
		f domain.ArchiveFilter, wantSlugs []string) {
		t.Helper()
		posts, err := repo.PublishedArchive(ctx, f, archiveLimit, 0)
		if err != nil {
			t.Fatalf("%s: PublishedArchive: %v", label, err)
		}
		got := archiveSlugs(posts)
		if !sameSlugs(got, wantSlugs) {
			t.Errorf("%s: PublishedArchive slugs = %v, want %v", label, got, wantSlugs)
		}
		n, err := repo.CountPublishedArchive(ctx, f)
		if err != nil {
			t.Fatalf("%s: CountPublishedArchive: %v", label, err)
		}
		if n != len(wantSlugs) {
			t.Errorf("%s: CountPublishedArchive = %d, want %d — the count and the listing must describe the same set",
				label, n, len(wantSlugs))
		}
	}

	// Req 3.1: a term set selects the parent's posts and its descendants',
	// newest first. Req 3.3's duplicate row is in every set that contains Tech
	// or Go, so it is exercised here as well as in its own case below.
	t.Run("term set selects parent and descendants newest-first", func(t *testing.T) {
		repos, cleanup := newArchiveRepos(t)
		defer cleanup()

		cases := []struct {
			label   string
			termIDs []int64
			want    []string
		}{
			{
				// Tech + Go + Generics: the whole chain.
				label:   "Tech and all descendants",
				termIDs: []int64{NestedParentTermID, NestedChildTermID, NestedGrandchildTermID},
				want: []string{
					ArchiveDualPostSlug,      // 2024-05-18
					NestedGrandchildPostSlug, // 2024-05-17
					NestedChildPostSlug,      // 2024-05-16
					NestedParentPostSlug,     // 2024-05-15
				},
			},
			{
				// A mid-chain category with one descendant: the parent's own
				// post must NOT appear, so "descendants" is asserted rather
				// than "the whole taxonomy".
				label:   "Go and its descendant",
				termIDs: []int64{NestedChildTermID, NestedGrandchildTermID},
				want: []string{
					ArchiveDualPostSlug,
					NestedGrandchildPostSlug,
					NestedChildPostSlug,
				},
			},
			{
				label:   "Generics leaf only",
				termIDs: []int64{NestedGrandchildTermID},
				want:    []string{NestedGrandchildPostSlug},
			},
			{
				// The parent alone, without its descendants in the set: the
				// query must not walk the hierarchy itself. Resolving the
				// descendant set is the caller's job (Req 1.6); the repository
				// takes the ids it is given.
				label:   "Tech alone",
				termIDs: []int64{NestedParentTermID},
				want:    []string{ArchiveDualPostSlug, NestedParentPostSlug},
			},
		}
		for _, c := range cases {
			assertArchive(t, repos.Posts, c.label, domain.ArchiveFilter{
				Taxonomy: "category",
				TermIDs:  c.termIDs,
			}, c.want)
		}
	})

	// Req 3.3. The join from posts to terms multiplies rows, so a post filed
	// under both a parent and a child matches the term predicate twice. Listing
	// it twice would be visible; counting it twice would silently inflate
	// Page.Total and Page.TotalPages.
	t.Run("a post in both a parent and a child appears once and counts once", func(t *testing.T) {
		repos, cleanup := newArchiveRepos(t)
		defer cleanup()

		f := domain.ArchiveFilter{
			Taxonomy: "category",
			TermIDs:  []int64{NestedParentTermID, NestedChildTermID},
		}
		posts, err := repos.Posts.PublishedArchive(ctx, f, archiveLimit, 0)
		if err != nil {
			t.Fatalf("PublishedArchive: %v", err)
		}
		occurrences := 0
		for _, p := range posts {
			if p.ID == ArchiveDualPostID {
				occurrences++
			}
		}
		if occurrences != 1 {
			t.Errorf("post %d (%s) appears %d times in %v, want exactly 1 — it is filed under both Tech and Go",
				ArchiveDualPostID, ArchiveDualPostSlug, occurrences, archiveSlugs(posts))
		}

		n, err := repos.Posts.CountPublishedArchive(ctx, f)
		if err != nil {
			t.Fatalf("CountPublishedArchive: %v", err)
		}
		if n != len(posts) {
			t.Errorf("CountPublishedArchive = %d, listing len = %d — a double-counted post breaks M8 pagination totals",
				n, len(posts))
		}
		if n != 3 {
			t.Errorf("CountPublishedArchive = %d, want 3 (tech-post, go-post, dual-post once)", n)
		}
	})

	// Req 3.4: post_status='publish' is applied unconditionally. The draft sits
	// in the child category, so the term predicate matches it and only the
	// status filter can exclude it.
	t.Run("an unpublished post in a matching category is excluded", func(t *testing.T) {
		repos, cleanup := newArchiveRepos(t)
		defer cleanup()

		// The exclusion only means something if the row is there to exclude.
		drafts, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{
			Statuses: []string{"draft"},
		})
		if err != nil {
			t.Fatalf("list drafts: %v", err)
		}
		found := false
		for _, d := range drafts {
			if d.ID == ArchiveDraftPostID {
				found = true
			}
		}
		if !found {
			t.Fatalf("fixture draft %d (%s) is missing; the exclusion cannot be verified",
				ArchiveDraftPostID, ArchiveDraftPostSlug)
		}

		assertArchive(t, repos.Posts, "Go category", domain.ArchiveFilter{
			Taxonomy: "category",
			TermIDs:  []int64{NestedChildTermID},
		}, []string{ArchiveDualPostSlug, NestedChildPostSlug})
	})

	// Author selection (Req 12.2). The second author owns exactly one published
	// post, so an ignored AuthorID cannot coincidentally produce the right
	// answer.
	t.Run("author selection", func(t *testing.T) {
		repos, cleanup := newArchiveRepos(t)
		defer cleanup()

		assertArchive(t, repos.Posts, "second author", domain.ArchiveFilter{
			AuthorID: ArchiveOtherAuthorID,
		}, []string{ArchiveOtherAuthorPostSlug})

		// The first author's archive is the complement: it must contain the
		// fixture posts and must not contain the second author's.
		posts, err := repos.Posts.PublishedArchive(ctx, domain.ArchiveFilter{AuthorID: 1},
			archiveLimit, 0)
		if err != nil {
			t.Fatalf("PublishedArchive(author 1): %v", err)
		}
		got := archiveSlugs(posts)
		for _, want := range []string{NestedParentPostSlug, ArchiveDualPostSlug, "hello-3"} {
			if !containsSlug(got, want) {
				t.Errorf("author 1 archive = %v, missing %q", got, want)
			}
		}
		if containsSlug(got, ArchiveOtherAuthorPostSlug) {
			t.Errorf("author 1 archive = %v, must not contain %q", got, ArchiveOtherAuthorPostSlug)
		}
		n, err := repos.Posts.CountPublishedArchive(ctx, domain.ArchiveFilter{AuthorID: 1})
		if err != nil {
			t.Fatalf("CountPublishedArchive(author 1): %v", err)
		}
		if n != len(posts) {
			t.Errorf("CountPublishedArchive(author 1) = %d, listing len = %d", n, len(posts))
		}
	})

	// Req 6.4: the date bound is half-open, [Start, End). The two boundary rows
	// are asserted by name rather than only by count, because an inclusive upper
	// bound and an exclusive lower bound both yield 2 rows here for a set of
	// three daily posts — only naming them tells those mistakes apart.
	t.Run("date range is half-open on Start and End", func(t *testing.T) {
		repos, cleanup := newArchiveRepos(t)
		defer cleanup()

		// tech-post is exactly on Start and must be included; generics-post is
		// exactly on End and must be excluded. Built in UTC, which is the basis
		// post_date is read and written on.
		start := time.Date(2024, 5, 15, 0, 0, 0, 0, time.UTC)
		end := time.Date(2024, 5, 17, 0, 0, 0, 0, time.UTC)

		assertArchive(t, repos.Posts, "[2024-05-15, 2024-05-17)", domain.ArchiveFilter{
			Start: start,
			End:   end,
		}, []string{NestedChildPostSlug, NestedParentPostSlug})

		// Unbounded on one side: Start alone keeps everything from the lower
		// bound on, so an implementation that treats a zero End as "nothing
		// after" is caught.
		posts, err := repos.Posts.PublishedArchive(ctx,
			domain.ArchiveFilter{Start: start}, archiveLimit, 0)
		if err != nil {
			t.Fatalf("PublishedArchive(Start only): %v", err)
		}
		got := archiveSlugs(posts)
		for _, want := range []string{
			NestedParentPostSlug, NestedChildPostSlug, NestedGrandchildPostSlug,
			ArchiveDualPostSlug, ArchiveOtherAuthorPostSlug,
		} {
			if !containsSlug(got, want) {
				t.Errorf("Start-only archive = %v, missing %q", got, want)
			}
		}
		if containsSlug(got, "hello-3") {
			t.Errorf("Start-only archive = %v, must not contain a January fixture post", got)
		}
	})

	// Req 3.5: Types defaults to {"post"}, so a published page assigned to a
	// category does not leak into that category's archive. This is a deliberate
	// change from the pre-M9b flat category read, which applied no post-type
	// predicate (see docs/compatibility.md).
	t.Run("Types defaults to post so pages do not leak into archives", func(t *testing.T) {
		repos, cleanup := newArchiveRepos(t)
		defer cleanup()

		techOnly := domain.ArchiveFilter{
			Taxonomy: "category",
			TermIDs:  []int64{NestedParentTermID},
		}
		assertArchive(t, repos.Posts, "Tech with Types unset", techOnly,
			[]string{ArchiveDualPostSlug, NestedParentPostSlug})

		// The page is there: an explicit Types must reach it, so the default is
		// shown to be a default rather than a hard-coded post_type.
		pageFilter := techOnly
		pageFilter.Types = []string{"page"}
		assertArchive(t, repos.Posts, "Tech with Types=[page]", pageFilter,
			[]string{ArchivePagePostSlug})

		bothFilter := techOnly
		bothFilter.Types = []string{"post", "page"}
		assertArchive(t, repos.Posts, "Tech with Types=[post page]", bothFilter,
			[]string{ArchivePagePostSlug, ArchiveDualPostSlug, NestedParentPostSlug})
	})

	// A non-empty Taxonomy with an empty TermIDs means "no terms", not
	// "unfiltered". Degrading to unfiltered would serve the whole site at a
	// category URL whose descendant set came back empty.
	t.Run("non-empty Taxonomy with empty TermIDs matches nothing", func(t *testing.T) {
		repos, cleanup := newArchiveRepos(t)
		defer cleanup()

		for _, f := range []domain.ArchiveFilter{
			{Taxonomy: "category", TermIDs: nil},
			{Taxonomy: "category", TermIDs: []int64{}},
		} {
			posts, err := repos.Posts.PublishedArchive(ctx, f, archiveLimit, 0)
			if err != nil {
				t.Fatalf("PublishedArchive(%+v): %v", f, err)
			}
			if len(posts) != 0 {
				t.Errorf("PublishedArchive(%+v) = %v, want empty — an empty term set is not unfiltered",
					f, archiveSlugs(posts))
			}
			n, err := repos.Posts.CountPublishedArchive(ctx, f)
			if err != nil {
				t.Fatalf("CountPublishedArchive(%+v): %v", f, err)
			}
			if n != 0 {
				t.Errorf("CountPublishedArchive(%+v) = %d, want 0", f, n)
			}
		}

		// The mirror image: a zero-value filter is unfiltered, and must not be
		// confused with the empty-term-set case above.
		all, err := repos.Posts.PublishedArchive(ctx, domain.ArchiveFilter{}, archiveLimit, 0)
		if err != nil {
			t.Fatalf("PublishedArchive(zero filter): %v", err)
		}
		if len(all) == 0 {
			t.Error("PublishedArchive(zero filter) = empty, want every published post")
		}
	})
}

func archiveSlugs(posts []domain.Post) []string {
	slugs := make([]string, 0, len(posts))
	for _, p := range posts {
		slugs = append(slugs, p.Slug)
	}
	return slugs
}

func sameSlugs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsSlug(slugs []string, want string) bool {
	for _, s := range slugs {
		if s == want {
			return true
		}
	}
	return false
}
