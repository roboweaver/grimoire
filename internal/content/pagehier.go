package content

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/roboweaver/grimoire/internal/domain"
)

// pageHierarchyPageSize is the keyset page size LoadPageHierarchy reads the page
// graph with. Large enough that a modest pages table loads in one round-trip,
// small enough that a very large one paginates rather than materialising every
// row at once -- the walk is complete when a short (or empty) page arrives.
const pageHierarchyPageSize = 500

// PageMovedError reports that a page path's segment walk failed while its final
// segment names a page somewhere in the graph, and carries that page's canonical
// ancestry so the caller redirects to it (Req 5.1, 5.2). It is the page analogue
// of CategoryMovedError (internal/content/archive.go): callers match it with
// errors.As.
//
// Deliberately **not** wrapping domain.ErrNotFound, by the same reasoning
// CategoryMovedError spells out: a caller inspecting only errors.Is(err,
// domain.ErrNotFound) would 404 every legacy page URL while looking correct;
// unwrapped, the same oversight surfaces as a loud 500 instead.
type PageMovedError struct {
	// Page is the page the final segment named.
	Page domain.PageNode
	// Ancestry is Page's root-first slug path, ending in its own slug -- exactly
	// what routing.Structure.PagePath consumes to build the redirect target.
	Ancestry []string
}

func (e *PageMovedError) Error() string {
	return "content: page " + strings.Join(e.Ancestry, "/") + " requested at a non-canonical path"
}

// PageHierarchy indexes the page graph for the M15 ancestry and segment walks,
// the page analogue of TermHierarchy (internal/content/archive.go) over
// post_parent on page rows. It is built from a single PageReader read
// (Req 2.5, 7.3), never a per-level query, and its methods do not mutate it, so
// one graph can be loaded per request and reused.
type PageHierarchy struct {
	// byID holds every page row, keyed by ID.
	byID map[int64]domain.PageNode
	// children maps a parent page ID to its child page IDs, ordered by page ID
	// so the walk's tie-break is deterministic regardless of the order
	// ListPages returned.
	children map[int64][]int64
	// roots holds the page IDs the segment walk starts from -- post_parent == 0,
	// plus every page whose parent is absent from the graph, which Req 2.3
	// treats as top-level. Ordered by page ID, like children.
	roots []int64
}

// LoadPageHierarchy reads every page row via the additive PageReader port and
// indexes it, mirroring LoadTermHierarchy (archive.go). It walks ListPages under
// a stable ID keyset cursor so a large pages table loads in exactly one
// paginated read, not one query per hierarchy level (Req 2.5, 7.3). A page whose
// post_parent is absent from the graph is a root -- the orphan rule, identical
// to the term loader's (Req 2.3).
func LoadPageHierarchy(ctx context.Context, r domain.PageReader) (*PageHierarchy, error) {
	h := &PageHierarchy{
		byID:     make(map[int64]domain.PageNode),
		children: make(map[int64][]int64),
	}
	var pages []domain.PageNode
	var afterID int64
	for {
		batch, err := r.ListPages(ctx, afterID, pageHierarchyPageSize)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		pages = append(pages, batch...)
		afterID = batch[len(batch)-1].ID
		if len(batch) < pageHierarchyPageSize {
			break
		}
	}
	for _, p := range pages {
		h.byID[p.ID] = p
	}
	for _, p := range pages {
		if p.ParentID == 0 {
			h.roots = append(h.roots, p.ID)
			continue
		}
		// An orphaned parent contributes no edge: the page is top-level from
		// that point up (Req 2.3), which also makes it a root for the walk.
		if _, ok := h.byID[p.ParentID]; !ok {
			h.roots = append(h.roots, p.ID)
			continue
		}
		h.children[p.ParentID] = append(h.children[p.ParentID], p.ID)
	}
	for parent := range h.children {
		ids := h.children[parent]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	sort.Slice(h.roots, func(i, j int) bool { return h.roots[i] < h.roots[j] })
	return h, nil
}

// Ancestry returns the root-first slug path for a page ID, ending in the page's
// own slug (self-inclusive leaf, Req 2.2). It is TermHierarchy.Ancestry
// (archive.go) retargeted to post_parent: the SAME visited-set degradation -- an
// orphan parent terminates the walk yielding the shorter resolvable path
// (Req 2.3), and a parent cycle terminates on the first already-visited page
// (Req 2.4). Returns nil when id names no page in the graph.
func (h *PageHierarchy) Ancestry(id int64) []string {
	page, ok := h.byID[id]
	if !ok {
		return nil
	}
	visited := map[int64]bool{}
	var up []string
	for {
		if visited[page.ID] {
			break
		}
		visited[page.ID] = true
		up = append(up, page.Slug)
		parent, ok := h.byID[page.ParentID]
		if !ok {
			break
		}
		page = parent
	}
	// up is leaf-first; the canonical path wants root-first.
	for i, j := 0, len(up)-1; i < j; i, j = i+1, j-1 {
		up[i], up[j] = up[j], up[i]
	}
	return up
}

// ResolvePath walks a request's segments through the graph to the leaf page,
// matching each segment against the slugs of the current level's pages, starting
// from roots -- the page analogue of TermHierarchy.ResolvePath (archive.go).
// Resolving by path (not by final slug) is what lets two pages share a slug
// under different parents, each reachable at its own path. It reports false for
// an empty segment list and for any segment that matches nothing.
//
// WHEN two siblings under the same parent share a slug -- the one case the walk
// cannot separate -- the lowest page ID wins, because roots and children are
// both ordered by page ID, so the choice is deterministic.
func (h *PageHierarchy) ResolvePath(segments []string) (domain.PageNode, bool) {
	if len(segments) == 0 {
		return domain.PageNode{}, false
	}
	candidates := h.roots
	var current domain.PageNode
	for _, segment := range segments {
		id, ok := h.lowestBySlug(candidates, segment)
		if !ok {
			return domain.PageNode{}, false
		}
		current = h.byID[id]
		candidates = h.children[id]
	}
	return current, true
}

// recoverPath is what a failed ResolvePath means. It looks the **final segment
// only** up by slug anywhere in the graph and returns either a *PageMovedError
// naming that page's canonical ancestry, so a published flat or wrong-ancestor
// URL redirects rather than 404ing, or domain.ErrNotFound when the final segment
// names no page at all (Req 3.3) -- the page analogue of
// TermHierarchy.recoverPath (archive.go).
//
// The equal-ancestry arm is a corruption guard: a page whose canonical ancestry
// *is* the requested path would have been found by the walk, so this is
// reachable only for a page the walk cannot see from any root -- one inside a
// parent cycle -- where returning the same path as a redirect target would 301
// that URL to itself forever. A 404 for a corrupted hierarchy is the same
// degradation Req 2.3 and 2.4 already choose over a 500 and a hang.
func (h *PageHierarchy) recoverPath(segments []string) error {
	if len(segments) == 0 {
		return domain.ErrNotFound
	}
	page, ok := h.FindBySlug(segments[len(segments)-1])
	if !ok {
		return domain.ErrNotFound
	}
	ancestry := h.Ancestry(page.ID)
	if slices.Equal(ancestry, segments) {
		return domain.ErrNotFound
	}
	return &PageMovedError{Page: page, Ancestry: ancestry}
}

// lowestBySlug returns the first ID in ids whose page carries slug. ids is
// always a page-ID-ordered index (roots or a child list), so "first" is
// "lowest ID".
func (h *PageHierarchy) lowestBySlug(ids []int64, slug string) (int64, bool) {
	for _, id := range ids {
		if h.byID[id].Slug == slug {
			return id, true
		}
	}
	return 0, false
}

// FindBySlug returns the page carrying slug **anywhere** in the graph,
// regardless of where it sits, and reports whether one was found. It is the
// recovery lookup and nothing else: the 200 path goes through ResolvePath, which
// keeps it independent of slugs being unique.
//
// WHEN more than one page shares the slug the **lowest ID** wins,
// deterministically. Slug ambiguity is confined to this one method, whose
// alternative outcome is a 404 -- no path that returns 200 depends on it. The
// min over the matching set (rather than first-match) is required because Go
// map iteration order is deliberately randomised.
func (h *PageHierarchy) FindBySlug(slug string) (domain.PageNode, bool) {
	var found domain.PageNode
	ok := false
	for id, page := range h.byID {
		if page.Slug != slug {
			continue
		}
		if !ok || id < found.ID {
			found, ok = page, true
		}
	}
	return found, ok
}

// PageService resolves nested page paths and page ancestries over post_parent.
// It holds a PageReader (the additive page-graph read port) the way TermService
// holds a TermReader for CategoryArchive, and a PostRepository for the
// published-leaf confirmation. Published-only: the leaf is confirmed through the
// existing BySlug/PublishedByID published filters (Req 3.4). It is the single
// home of the page walk for both handler entry points and the REST mapper, so
// there is one page-ancestry computation (Req 4.3).
type PageService struct {
	pages domain.PageReader     // the new additive read port (Req 7)
	posts domain.PostRepository // BySlug/PublishedByID for the published leaf
}

// NewPageService constructs a PageService over a PageReader and a
// PostRepository. The struct fields are unexported; this constructor is the
// exported way to build one from outside the package.
func NewPageService(pages domain.PageReader, posts domain.PostRepository) *PageService {
	return &PageService{pages: pages, posts: posts}
}

// Resolve walks segments through the page graph to the published leaf page and
// returns the leaf together with its root-first ancestry (the fixed point the
// canonical is built from, Req 3.5). A failed walk whose final segment names a
// page returns a *PageMovedError carrying that page's ancestry (Req 5.1, 5.2); a
// final segment naming nothing returns domain.ErrNotFound (Req 3.3). The leaf
// node from the graph is confirmed published via posts.BySlug before a 200 is
// possible, so a draft/private page in the graph resolves to not-found exactly
// as today (Req 3.4).
func (s *PageService) Resolve(ctx context.Context, segments []string) (domain.Post, []string, error) {
	h, err := LoadPageHierarchy(ctx, s.pages)
	if err != nil {
		return domain.Post{}, nil, err
	}
	leaf, ok := h.ResolvePath(segments)
	if !ok {
		return domain.Post{}, nil, h.recoverPath(segments)
	}
	ancestry := h.Ancestry(leaf.ID)
	// Confirm the leaf is published through the existing published-only filter.
	// BySlug defaults to {"post","page"} with post_status='publish', so a draft
	// leaf present in the graph returns ErrNotFound here (Req 3.4).
	post, err := s.posts.BySlug(ctx, leaf.Slug)
	if err != nil {
		return domain.Post{}, nil, err
	}
	return post, ancestry, nil
}

// AncestryFor returns the root-first slug ancestry of an already-resolved page
// (by ID), for the flat-leaf 301 path in single() and for the REST link. It is
// the one place single() and the REST mapper obtain the nested canonical, so
// both converge on the same computation as Resolve (Req 3.5, 4.4, 5.1).
func (s *PageService) AncestryFor(ctx context.Context, id int64) ([]string, error) {
	h, err := LoadPageHierarchy(ctx, s.pages)
	if err != nil {
		return nil, err
	}
	return h.Ancestry(id), nil
}
