package content

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// PageHierarchy / PageService tests (task 3.1), the page analogue of the
// TermHierarchy tests in archive_test.go. The walk is over domain.PageNode and
// post_parent, mirroring TermHierarchy.Ancestry/ResolvePath/recoverPath and
// CategoryMovedError exactly (design.md: Components and Interfaces ->
// content.PageHierarchy / content.PageMovedError / content.PageService; D2, D3).
//
// A fake domain.PageReader serves seeded PageNodes honoring the keyset
// (ID > afterID) + limit cursor LoadPageHierarchy pages with, so the two
// corrupt shapes the requirements name -- an orphaned parent (post_parent
// absent from the graph, Req 2.3) and a parent cycle (Req 2.4) -- are a few
// lines of Go here rather than awkward cross-vendor SQL. The published-leaf
// confirmation reuses fakePostRepo (post_test.go), which already satisfies
// domain.PostRepository through BySlug/PublishedByID, rather than declaring a
// second fake in the same package.
//
// PageService constructor shape (documented for task 3.2): PageService holds
// unexported fields (pages domain.PageReader; posts domain.PostRepository), so
// from package content these tests construct it with a struct literal
// &PageService{pages: ..., posts: ...}. Task 3.2 must keep those two field
// names (pages, posts) so this literal compiles; it may additionally offer a
// NewPageService(pages, posts) constructor, but the literal is the authoritative
// shape the test depends on.

// pageNode builds a lean page row. status defaults to "publish" via the helper
// variants below so the graph-shape tests read cleanly.
func pageNode(id, parentID int64, slug, status string) domain.PageNode {
	return domain.PageNode{ID: id, Slug: slug, ParentID: parentID, Status: status}
}

// fakePageReader serves a fixed set of PageNodes as the complete page graph,
// honoring the ID>afterID keyset cursor and the limit so LoadPageHierarchy's
// paginated walk is exercised rather than bypassed. It records how many
// ListPages calls the load issued so a test can assert the walk pages rather
// than reading the whole table in one shot when a small limit is used.
type fakePageReader struct {
	nodes []domain.PageNode
	calls int
	err   error
}

func newPageReader(nodes ...domain.PageNode) *fakePageReader {
	cp := make([]domain.PageNode, len(nodes))
	copy(cp, nodes)
	sort.Slice(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })
	return &fakePageReader{nodes: cp}
}

func (r *fakePageReader) ListPages(ctx context.Context, afterID int64, limit int) ([]domain.PageNode, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	var out []domain.PageNode
	for _, n := range r.nodes {
		if n.ID <= afterID {
			continue
		}
		out = append(out, n)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

func loadPageHierarchy(t *testing.T, r domain.PageReader) *PageHierarchy {
	t.Helper()
	h, err := LoadPageHierarchy(context.Background(), r)
	if err != nil {
		t.Fatalf("LoadPageHierarchy: %v", err)
	}
	return h
}

// pageGraph is the fixture the resolution tests share:
//
//	about (1)                     -- top-level
//	└── team (2)
//	    └── history (3)           -- parent -> child -> grandchild chain
//	contact (4)                   -- sibling top-level page
//	news (5)                      -- top-level
//	├── local (6, slug "local")   -- shared slug under news
//	events (7)
//	└── local (8, slug "local")   -- shared slug under events, different path
//
// Pages 6 and 8 share the slug "local" under different parents, so the walk
// must resolve by PATH (news/local vs events/local) and reach each at its own
// path -- the page analogue of TermHierarchy's shared-slug case.
func pageGraph() *fakePageReader {
	return newPageReader(
		pageNode(1, 0, "about", "publish"),
		pageNode(2, 1, "team", "publish"),
		pageNode(3, 2, "history", "publish"),
		pageNode(4, 0, "contact", "publish"),
		pageNode(5, 0, "news", "publish"),
		pageNode(6, 5, "local", "publish"),
		pageNode(7, 0, "events", "publish"),
		pageNode(8, 7, "local", "publish"),
	)
}

// Req 2.2: a parent -> child -> grandchild chain resolves root-first and
// includes the page's own slug, so the result is exactly what
// routing.Structure.PagePath consumes.
func TestPageHierarchyAncestryChainIsRootFirstSelfInclusive(t *testing.T) {
	h := loadPageHierarchy(t, pageGraph())

	assertSlugs(t, h.Ancestry(3), []string{"about", "team", "history"})
	assertSlugs(t, h.Ancestry(2), []string{"about", "team"})
	assertSlugs(t, h.Ancestry(1), []string{"about"})
	// A sibling top-level page is its own one-element ancestry.
	assertSlugs(t, h.Ancestry(4), []string{"contact"})
}

// Req 2.3: an imported database contains orphaned rows, so a post_parent naming
// a page absent from the graph terminates the walk and yields the shorter
// resolvable path rather than an error, matching TermHierarchy's orphan rule.
func TestPageHierarchyAncestryOrphanParentTerminatesWalk(t *testing.T) {
	r := newPageReader(
		pageNode(3, 2, "history", "publish"),
		pageNode(2, 99, "team", "publish"), // parent 99 was never imported
	)
	h := loadPageHierarchy(t, r)

	assertSlugs(t, h.Ancestry(3), []string{"team", "history"})
	assertSlugs(t, h.Ancestry(2), []string{"team"})
}

// Req 2.4: a corrupted parent cycle terminates on the first already-visited
// page rather than hanging the request, matching TermHierarchy's cycle rule.
func TestPageHierarchyAncestryCycleTerminatesOnFirstRepeatedPage(t *testing.T) {
	r := newPageReader(
		pageNode(1, 2, "alpha", "publish"),
		pageNode(2, 1, "beta", "publish"),
	)
	h := loadPageHierarchy(t, r)

	// beta -> alpha -> beta, which is already visited, so the walk stops with
	// each page reported once.
	assertSlugs(t, h.Ancestry(2), []string{"alpha", "beta"})
	assertSlugs(t, h.Ancestry(1), []string{"beta", "alpha"})
}

// ResolvePath: a good path resolves the leaf the final segment names.
func TestPageHierarchyResolvePathResolvesLeaf(t *testing.T) {
	h := loadPageHierarchy(t, pageGraph())

	leaf, ok := h.ResolvePath([]string{"about", "team", "history"})
	if !ok {
		t.Fatalf("ResolvePath(about/team/history) = !ok, want the grandchild page")
	}
	if leaf.ID != 3 || leaf.Slug != "history" {
		t.Fatalf("leaf = %+v, want id 3 slug history", leaf)
	}

	// An empty path has no archive and resolves to nothing.
	if _, ok := h.ResolvePath(nil); ok {
		t.Fatalf("ResolvePath(nil) = ok, want !ok")
	}
}

// Two pages sharing a slug under different parents are each reachable at their
// OWN path -- resolution is by path, not by final slug (the page analogue of
// TermHierarchy's shared-slug case).
func TestPageHierarchyResolvePathSharedSlugUnderDifferentParents(t *testing.T) {
	h := loadPageHierarchy(t, pageGraph())

	underNews, ok := h.ResolvePath([]string{"news", "local"})
	if !ok {
		t.Fatalf("ResolvePath(news/local) = !ok, want the page under news")
	}
	underEvents, ok := h.ResolvePath([]string{"events", "local"})
	if !ok {
		t.Fatalf("ResolvePath(events/local) = !ok, want the page under events")
	}
	if underNews.ID != 6 {
		t.Fatalf("news/local = id %d, want 6", underNews.ID)
	}
	if underEvents.ID != 8 {
		t.Fatalf("events/local = id %d, want 8", underEvents.ID)
	}
	if underNews.ID == underEvents.ID {
		t.Fatalf("shared-slug pages resolved to the same page %d; resolution must be by path", underNews.ID)
	}
}

// recoverPath / PageMovedError (Req 5.1, 5.2): a flat or wrong-ancestor path
// whose final segment names a page somewhere in the graph returns a
// *PageMovedError carrying that page's canonical ancestry, so the published URL
// 301s instead of 404ing. PageMovedError must NOT wrap domain.ErrNotFound, so
// errors.As finds it and errors.Is(ErrNotFound) does not.
func TestPageHierarchyRecoverPathFlatOrWrongAncestorIsMovedError(t *testing.T) {
	h := loadPageHierarchy(t, pageGraph())

	// Flat: /history requested, canonical is about/team/history.
	err := h.recoverPath([]string{"history"})
	var moved *PageMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("recoverPath(history) = %v, want *PageMovedError", err)
	}
	assertSlugs(t, moved.Ancestry, []string{"about", "team", "history"})
	if moved.Page.ID != 3 {
		t.Fatalf("moved.Page.ID = %d, want 3", moved.Page.ID)
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("PageMovedError must not wrap domain.ErrNotFound")
	}

	// Wrong ancestor: /contact/team requested, team's canonical is about/team.
	wrong := h.recoverPath([]string{"contact", "team"})
	var moved2 *PageMovedError
	if !errors.As(wrong, &moved2) {
		t.Fatalf("recoverPath(contact/team) = %v, want *PageMovedError", wrong)
	}
	assertSlugs(t, moved2.Ancestry, []string{"about", "team"})
}

// Req 3.3: a final segment naming nothing in the graph returns
// domain.ErrNotFound, not a *PageMovedError.
func TestPageHierarchyRecoverPathUnknownFinalSegmentIsNotFound(t *testing.T) {
	h := loadPageHierarchy(t, pageGraph())

	err := h.recoverPath([]string{"about", "nope"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("recoverPath(about/nope) = %v, want domain.ErrNotFound", err)
	}
	var moved *PageMovedError
	if errors.As(err, &moved) {
		t.Fatalf("recoverPath(about/nope) = %v, want ErrNotFound not a PageMovedError", err)
	}
}

// A cycle-trapped page's equal-ancestry recovery returns ErrNotFound rather
// than 301ing to itself: a page inside a parent cycle is unreachable from any
// root, so its derived ancestry equals the requested path and a redirect would
// loop forever. The corruption guard 404s instead, matching
// TermHierarchy.recoverPath's equal-ancestry arm.
func TestPageHierarchyRecoverPathCycleEqualAncestryIsNotFound(t *testing.T) {
	r := newPageReader(
		pageNode(1, 2, "alpha", "publish"),
		pageNode(2, 1, "beta", "publish"),
	)
	h := loadPageHierarchy(t, r)

	// beta's derived ancestry is [alpha beta]; requesting exactly that path for
	// a cycle-trapped page must 404 rather than redirect to itself.
	err := h.recoverPath([]string{"alpha", "beta"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("recoverPath(alpha/beta) on a cycle = %v, want domain.ErrNotFound", err)
	}
}

// PageService.Resolve (Req 3.2, 3.4): the walked segments resolve a leaf, the
// leaf is confirmed published via the post reads (BySlug/PublishedByID), and
// Resolve returns the published post together with its root-first ancestry (the
// fixed point the canonical is built from, Req 3.5).
func TestPageServiceResolvePublishedLeaf(t *testing.T) {
	posts := &fakePostRepo{
		bySlugPost: domain.Post{ID: 3, Slug: "history", Type: "page", Status: "publish"},
	}
	svc := &PageService{pages: pageGraph(), posts: posts}

	post, ancestry, err := svc.Resolve(context.Background(), []string{"about", "team", "history"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if post.ID != 3 || post.Type != "page" {
		t.Fatalf("post = %+v, want the published page id 3", post)
	}
	assertSlugs(t, ancestry, []string{"about", "team", "history"})
}

// Req 3.4: a leaf present in the page graph but NOT published (a draft) resolves
// to not-found. The graph carries the row so the segment walk reaches it, but
// the published confirmation (BySlug/PublishedByID returning ErrNotFound for a
// non-published row) is what decides a 200 is impossible -- page resolution
// inherits today's published-only behavior with no new filter.
func TestPageServiceResolveDraftLeafIsNotFound(t *testing.T) {
	r := newPageReader(
		pageNode(1, 0, "about", "publish"),
		pageNode(2, 1, "secret", "draft"), // present in the graph, but a draft
	)
	// The published read refuses the draft leaf.
	posts := &fakePostRepo{bySlugErr: domain.ErrNotFound, byIDErr: domain.ErrNotFound}
	svc := &PageService{pages: r, posts: posts}

	_, _, err := svc.Resolve(context.Background(), []string{"about", "secret"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Resolve(about/secret) draft leaf = %v, want domain.ErrNotFound", err)
	}
}

// PageService.AncestryFor (Req 3.5, 4.4, 5.1): the single place single() and
// the REST mapper obtain a resolved page's nested canonical ancestry by ID, so
// both converge on the same computation as Resolve.
func TestPageServiceAncestryForReturnsRootFirstAncestry(t *testing.T) {
	svc := &PageService{pages: pageGraph(), posts: &fakePostRepo{}}

	ancestry, err := svc.AncestryFor(context.Background(), 3)
	if err != nil {
		t.Fatalf("AncestryFor: %v", err)
	}
	assertSlugs(t, ancestry, []string{"about", "team", "history"})

	// A top-level page is its own one-element ancestry.
	top, err := svc.AncestryFor(context.Background(), 4)
	if err != nil {
		t.Fatalf("AncestryFor(top-level): %v", err)
	}
	assertSlugs(t, top, []string{"contact"})
}
