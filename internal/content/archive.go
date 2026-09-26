package content

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/roboweaver/grimoire/internal/domain"
)

// Archive is the shape every archive read returns: the category, tag, author
// and date archives all render from it, so the web handlers and the templates
// see one type rather than four.
type Archive struct {
	// Heading is the term name, the author's display_name, or the formatted
	// date, depending on the archive kind.
	Heading string
	// Term is the resolved taxonomy term for category and tag archives, and
	// the zero value for date and author archives.
	Term domain.Term
	// Ancestry is the root-first category slug path including the term's own
	// slug, as routing.Structure.CategoryPath consumes it. It is nil for every
	// other archive kind, including tags: post_tag is flat (Req 5.2).
	Ancestry []string
	// Posts is the page of published posts the archive lists, newest first.
	Posts []domain.Post
	// Page is the pagination contract shared by every paginated read path.
	Page Page
}

// CategoryMovedError reports that a category path's segment walk failed while
// its final segment names a category somewhere in the taxonomy, and carries that
// category's canonical ancestry so the caller can redirect to it (Req 2.3,
// 2.11).
//
// It is the third outcome of resolving a category path, alongside "resolved" and
// domain.ErrNotFound, and it exists because the three need different HTTP
// answers: 200, 301 and 404. The published URLs it recovers are real ones — the
// flat /category/{slug} grimoire served before this milestone is the
// zero-ancestor instance of it, and a wrong-ancestor or skipped-level path is
// the same failure one hop further along.
//
// Deliberately **not** wrapping domain.ErrNotFound. A caller that inspects only
// errors.Is(err, domain.ErrNotFound) would then 404 every legacy category URL on
// the site while looking correct, which is the failure this type exists to
// prevent; unwrapped, the same oversight surfaces as a loud 500 instead. Callers
// match it with errors.As.
type CategoryMovedError struct {
	// Term is the category the final segment named.
	Term domain.Term
	// Ancestry is Term's root-first slug path, ending in its own slug — exactly
	// what routing.Structure.CategoryPath consumes to build the redirect target.
	Ancestry []string
}

func (e *CategoryMovedError) Error() string {
	return "content: category " + strings.Join(e.Ancestry, "/") + " requested at a non-canonical path"
}

// TermHierarchy is a taxonomy's parent/child graph, resolved from a single
// TermReader.ListByTaxonomy read (Req 1.6). It answers both directions the
// archives need: Ancestry walks up for the canonical path, DescendantIDs walks
// down for the descendant-inclusive listing and count (Req 3.1).
//
// A TermHierarchy is immutable once loaded and its methods do not mutate it, so
// one graph can be loaded per request and reused across every term in the
// response — which is what keeps a /wp-json/wp/v2/categories listing from
// issuing one graph read per row. Reuse is a plain value copy of the pointer;
// the walks allocate only their own results.
//
// Deliberately not cached across requests. Unlike M9a's permalink options,
// terms are mutable at runtime through M6's TermWriteService, so a cache would
// need an invalidation story this milestone does not own (Req 1.6). The read
// costs one query returning one row per term in the taxonomy — 33 rows on the
// reference database.
type TermHierarchy struct {
	taxonomy string
	byID     map[int64]domain.Term
	// children maps a parent term ID to its child term IDs, ordered by term
	// ID so the breadth-first expansion is identical on all three vendors
	// regardless of the order ListByTaxonomy returned.
	children map[int64][]int64
	// roots holds the term IDs the segment walk starts from -- ParentID == 0,
	// plus every term whose parent is absent from the graph, which Req 1.4
	// treats as top-level. Ordered by term ID, like children, so the walk's
	// tie-break is the same on all three vendors.
	roots []int64
}

// LoadTermHierarchy reads every term of taxonomy in one
// TermReader.ListByTaxonomy call and indexes it for the ancestry and descendant
// walks. Exactly one read happens per call, not one per hierarchy level.
func LoadTermHierarchy(ctx context.Context, r domain.TermReader, taxonomy string) (*TermHierarchy, error) {
	terms, err := r.ListByTaxonomy(ctx, taxonomy)
	if err != nil {
		return nil, err
	}
	h := &TermHierarchy{
		taxonomy: taxonomy,
		byID:     make(map[int64]domain.Term, len(terms)),
		children: make(map[int64][]int64),
	}
	for _, t := range terms {
		h.byID[t.ID] = t
	}
	for _, t := range terms {
		if t.ParentID == 0 {
			h.roots = append(h.roots, t.ID)
			continue
		}
		// An orphaned parent contributes no edge: the term is top-level from
		// that point up (Req 1.4), which also makes it a root for the walk.
		if _, ok := h.byID[t.ParentID]; !ok {
			h.roots = append(h.roots, t.ID)
			continue
		}
		h.children[t.ParentID] = append(h.children[t.ParentID], t.ID)
	}
	for parent := range h.children {
		ids := h.children[parent]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	sort.Slice(h.roots, func(i, j int) bool { return h.roots[i] < h.roots[j] })
	return h, nil
}

// Taxonomy reports the taxonomy this graph was loaded for, so a caller reusing
// one graph across a request can assert it holds the right one.
func (h *TermHierarchy) Taxonomy() string { return h.taxonomy }

// ResolvePath walks a request's segment path through the graph and returns the
// term the final segment names (Req 2.10): the first segment is matched against
// the slugs of the taxonomy's root terms, each subsequent segment against the
// slugs of the children of the term the previous segment matched. It reports
// false when any segment matches nothing, and for an empty segment list —
// there is no such thing as the archive of no path (Req 2.8).
//
// Resolving by path rather than by final slug is what keeps the 200 path
// independent of slugs being unique within a taxonomy (Req 1.7): two category
// terms slugged "local" under different parents are each reachable at their own
// path, and neither shadows the other. A wrong-ancestor path such as
// sport/local fails here by construction, rather than by an ancestry
// comparison performed after a leaf lookup.
//
// WHEN two siblings under the same parent share a slug — the one case the walk
// cannot separate — the lowest term ID wins, because roots and children are
// both ordered by term ID, so the choice is identical on all three vendors.
func (h *TermHierarchy) ResolvePath(segments []string) (domain.Term, bool) {
	if len(segments) == 0 {
		return domain.Term{}, false
	}
	candidates := h.roots
	var current domain.Term
	for _, segment := range segments {
		id, ok := h.lowestBySlug(candidates, segment)
		if !ok {
			return domain.Term{}, false
		}
		current = h.byID[id]
		candidates = h.children[id]
	}
	return current, true
}

// recoverPath is what a failed ResolvePath means (Req 2.11). It looks the
// **final segment only** up by slug anywhere in the graph and returns either a
// *CategoryMovedError naming that term's canonical ancestry, so a published flat
// or wrong-ancestor URL redirects rather than 404ing, or domain.ErrNotFound when
// the final segment names no term at all (Req 2.5).
//
// Recovery is confined to the final segment on purpose: it is the segment that
// names the category, and the earlier ones are precisely what the request got
// wrong. /category/news/go recovering to tech/go is the whole point — the
// ancestry in the URL is discarded rather than reconciled.
//
// The equal-ancestry arm is a corruption guard, not a reachable path on a sane
// graph: a term whose canonical ancestry *is* the requested path would have been
// found by the walk. It is reachable only for a term the walk cannot see from any
// root — one inside a parent cycle — where returning the same path as a redirect
// target would 301 that URL to itself forever. A 404 for a corrupted hierarchy is
// the same degradation Req 1.4 and 1.5 already choose over a 500 and a hang.
func (h *TermHierarchy) recoverPath(segments []string) error {
	if len(segments) == 0 {
		return domain.ErrNotFound
	}
	term, ok := h.FindBySlug(segments[len(segments)-1])
	if !ok {
		return domain.ErrNotFound
	}
	ancestry := h.Ancestry(term.ID)
	if slices.Equal(ancestry, segments) {
		return domain.ErrNotFound
	}
	return &CategoryMovedError{Term: term, Ancestry: ancestry}
}

// lowestBySlug returns the first ID in ids whose term carries slug. ids is
// always a term-ID-ordered index (roots or a child list), so "first" is
// "lowest term_id".
func (h *TermHierarchy) lowestBySlug(ids []int64, slug string) (int64, bool) {
	for _, id := range ids {
		if h.byID[id].Slug == slug {
			return id, true
		}
	}
	return 0, false
}

// FindBySlug returns the term carrying slug **anywhere** in the graph,
// regardless of where it sits in the hierarchy, and reports whether one was
// found. It is the recovery lookup of Req 2.11 and nothing else: the 200 path
// goes through ResolvePath, which is what keeps it independent of slugs being
// unique within a taxonomy (Req 1.7).
//
// WHEN more than one term shares the slug the **lowest term ID** wins,
// deterministically and identically on all three vendors. Slug ambiguity is
// therefore confined to this one method, whose alternative outcome is a 404 --
// no path that returns 200 depends on it.
//
// The scan is over the graph the caller already loaded, so recovery costs no
// second query. It is linear in the taxonomy's size (33 rows on the reference
// database), which is why no slug index is built: it would be paid for by every
// category request to serve the ones that are about to redirect.
func (h *TermHierarchy) FindBySlug(slug string) (domain.Term, bool) {
	var found domain.Term
	ok := false
	for id, term := range h.byID {
		if term.Slug != slug {
			continue
		}
		// min over the matching set rather than first-match, because map
		// iteration order is deliberately randomised in Go -- first-match would
		// pick a different term between two runs of the same request.
		if !ok || id < found.ID {
			found, ok = term, true
		}
	}
	return found, ok
}

// Ancestry returns the root-first slug path for id, ending in id's own slug —
// exactly what the canonical nested category path is built from. It returns nil
// when id names no term in the graph.
//
// The walk degrades rather than erroring on the two corrupt shapes an imported
// database can carry: a ParentID naming a term absent from the graph terminates
// the walk and yields the shorter path (Req 1.4), and a visited set terminates a
// parent cycle on the first already-visited term rather than hanging the
// request (Req 1.5).
func (h *TermHierarchy) Ancestry(id int64) []string {
	term, ok := h.byID[id]
	if !ok {
		return nil
	}
	visited := map[int64]bool{}
	var up []string
	for {
		if visited[term.ID] {
			break
		}
		visited[term.ID] = true
		up = append(up, term.Slug)
		parent, ok := h.byID[term.ParentID]
		if !ok {
			break
		}
		term = parent
	}
	// up is leaf-first; the canonical path wants root-first.
	for i, j := 0, len(up)-1; i < j; i, j = i+1, j-1 {
		up[i], up[j] = up[j], up[i]
	}
	return up
}

// DescendantIDs returns id followed by every term beneath it, breadth-first.
// The set is self-inclusive because a parent category's archive lists its own
// posts as well as its descendants' (Req 3.1), and it feeds
// domain.ArchiveFilter.TermIDs directly. It returns nil when id names no term
// in the graph.
//
// The same visited set guards the walk, so a cycle in the graph terminates
// instead of expanding forever (Req 1.5).
func (h *TermHierarchy) DescendantIDs(id int64) []int64 {
	if _, ok := h.byID[id]; !ok {
		return nil
	}
	visited := map[int64]bool{id: true}
	out := []int64{id}
	for i := 0; i < len(out); i++ {
		for _, child := range h.children[out[i]] {
			if visited[child] {
				continue
			}
			visited[child] = true
			out = append(out, child)
		}
	}
	return out
}
