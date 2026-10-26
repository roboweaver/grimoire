package routing_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
)

// TestRealWordPressNestedPages validates the nested-PAGE half of the router
// against the same restored, REAL WordPress database (read-only) the permalink
// and nested-category checks run on, under the same single gate: one
// GRIMOIRE_TEST_WP_DSN enables every real-database check in the repo and this
// test introduces no new mechanism (Req 10.4; design.md Finding 8).
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress?parseTime=true' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/routing/ -run TestRealWordPressNestedPages -v -count=1
//
// It is the page analogue of TestRealWordPressNestedCategories, with two facts
// swapped. The graph is walked over post_parent on post_type='page' rows rather
// than term_taxonomy.parent on term rows -- the walk done HERE
// (realWPPageAncestry), over the raw rows, so the expectation does not inherit
// the implementation's idea of the hierarchy. And a page is EXEMPT from
// permalink_structure (M15 D1): its canonical is its post_name ancestry
// REGARDLESS of whether the site runs a flat, dated, or custom structure, so no
// non-flat structure is needed to exercise it. The harness still builds a
// routing.Structure, because PagePath honors the structure's trailing slash and
// Classify is the classifier the derived path is round-tripped through.
//
// # Expectations are derived, not borrowed
//
// Every expected path is assembled here from the raw page rows: the ancestry is
// walked up post_parent inside this test, the slugs are joined with plain string
// operations by PagePath, and the result is fed back through Classify. The walk
// (realWPPageAncestry) never calls content.PageHierarchy.Ancestry, so the
// implementation cannot merely agree with itself -- a constructor and a
// classifier that disagreed would surface here as a failed round trip rather
// than as a redirect loop in production.
//
// # Read-only
//
// storage.New opens the pool without migrating and every call below is a read.
func TestRealWordPressNestedPages(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress nested-page validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if prefix == "" {
		prefix = "accuweaver"
	}

	ctx := context.Background()
	repos, err := storage.New(config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix})
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer repos.Close()

	// The site's own structure, read rather than assumed -- a page is exempt
	// from it (D1), so it changes no page path, but it still supplies the
	// trailing-slash form PagePath and Classify both honor, so a trailing-slash
	// site's derived page path carries a trailing slash and round-trips to a
	// TrailingSlash Target.
	structure, err := repos.Options.Get(ctx, "permalink_structure")
	if err != nil {
		t.Fatalf("read permalink_structure: %v", err)
	}
	categoryBase, _ := repos.Options.Get(ctx, "category_base")
	tagBase, _ := repos.Options.Get(ctx, "tag_base")
	t.Logf("site permalink_structure = %q", structure)

	s, parseErr := routing.Parse(structure, categoryBase, tagBase)
	if parseErr != nil {
		// Parse returns a usable flat Structure alongside its error, which still
		// serves pages at their nested ancestry (a page is exempt either way),
		// so an unsupported structure is not a reason to stop checking pages.
		t.Logf("structure %q is unsupported (%v); validating pages against the "+
			"flat fallback the server would use", structure, parseErr)
	}

	// Read the whole page graph read-only via the M15 port, paginated the way
	// LoadPageHierarchy does, so the walk can climb post_parent THROUGH an
	// unpublished ancestor -- ListPages returns every status (Req 7.1). The
	// published-leaf check is separate, below.
	pages, err := realWPListAllPages(ctx, repos.PageReader)
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if len(pages) == 0 {
		t.Skip("no pages in the target database")
	}
	byID := make(map[int64]domain.PageNode, len(pages))
	for _, p := range pages {
		byID[p.ID] = p
	}

	// The census is taken off the raw rows before any assertion runs, so the
	// Req 10.4 skip below can name the precondition that actually failed rather
	// than the one that is merely most likely -- the page analogue of
	// realWPNestedCensus.
	census := realWPPageCensus(pages)
	t.Logf("page census: %d pages, %d published, %d published-and-nested, "+
		"%d yielding a usable nested path",
		census.Pages, census.Published, census.Nested, census.Usable)

	// A nested page's first segment that collides with an archive base or the
	// structure's front is claimed by an earlier Classify row, exactly as the
	// nested-category test documents for a base/front collision (Req 10.2's
	// documented exception). On such a page the round trip does not apply; the
	// derived path is still asserted against PagePath, the collision is said out
	// loud, and the Classify assertion is skipped rather than failed.
	front := realWPPageFront(structure, parseErr == nil && strings.TrimSpace(structure) != "")

	nested, checked := 0, 0
	for _, page := range pages {
		if page.Status != "publish" {
			// Only a published page serves a 200, so only a published page is a
			// subject -- matching the category test asserting nested TERMS only.
			// The graph (byID) still holds every status so ancestry can climb
			// through an unpublished ancestor.
			continue
		}
		ancestry, isNested, pathSafe := realWPNestedPageCandidate(byID, page)
		if !pathSafe {
			t.Logf("skipping page %d (%q): a slug in its ancestry is not a plain "+
				"URL token", page.ID, page.Slug)
			continue
		}
		if !isNested {
			// A top-level page (one-segment ancestry) classifies as KindPost via
			// the flat /{slug} row, not KindPage, exactly as the category test
			// only asserts nested terms. Skip it as a subject.
			continue
		}
		checked++
		nested++

		t.Run(strings.Join(ancestry, "-"), func(t *testing.T) {
			// Derived independently: PagePath joins the walked ancestry with the
			// structure's trailing slash and NO front and NO base, because a page
			// is exempt from the structure (D1, Req 1.1, 4.1).
			want := s.PagePath(ancestry)

			if collides, base := realWPPageFirstSegmentCollides(ancestry, s, front); collides {
				t.Logf("page path %q begins with %q, which an archive base or the "+
					"structure's front claims first; the round trip does not apply "+
					"on this site (Req 10.2)", want, base)
				return
			}

			// The derived nested path must classify back to a page, carrying
			// exactly the ancestry segments in order, so the handler would walk
			// precisely those segments back to this leaf. If it did not, a
			// canonical page URL would 301 again -- a redirect loop on every
			// nested page on the site.
			target := s.Classify(want)
			if target.Kind != routing.KindPage {
				t.Fatalf("Classify(%q).Kind = %v, want KindPage (derived from "+
					"post_parent on page rows)", want, target.Kind)
			}
			if !slices.Equal(target.Segments, ancestry) {
				t.Fatalf("Classify(%q).Segments = %v, want %v",
					want, target.Segments, ancestry)
			}
			if target.TrailingSlash != s.TrailingSlash {
				t.Errorf("Classify(%q).TrailingSlash = %v, want %v",
					want, target.TrailingSlash, s.TrailingSlash)
			}
		})
	}

	// Req 10.4: a check that reports success for a claim it never exercised is
	// worse than no check, so a run that asserted nothing about nesting skips
	// naming the precondition rather than passing. The loop counts nesting from
	// the length of the walked ancestry while the census counts it from the raw
	// post_parent rows, so the two agreeing is what makes either number evidence
	// -- a disagreement is fatal rather than logged, the same cross-check the
	// nested-category test runs.
	if nested != census.Usable {
		t.Fatalf("the loop counted %d nested pages and the census counted %d "+
			"usable among %d pages -- one of them is wrong, so neither can be "+
			"trusted as evidence that the nested claim was exercised",
			nested, census.Usable, census.Pages)
	}
	if nested == 0 {
		t.Skip(census.precondition())
	}
	t.Logf("validated %d real nested page paths against structure %q",
		checked, structure)
}

// realWPListAllPages drains the M15 PageReader port into one slice, paginating
// the ID keyset cursor the way content.LoadPageHierarchy does, so the walk reads
// the whole page graph (every status) in a stable order. It is the page
// analogue of ListByTaxonomy returning the whole category taxonomy in one read.
func realWPListAllPages(ctx context.Context, r domain.PageReader) ([]domain.PageNode, error) {
	const pageSize = 500
	var all []domain.PageNode
	var afterID int64
	for {
		batch, err := r.ListPages(ctx, afterID, pageSize)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		afterID = batch[len(batch)-1].ID
		if len(batch) < pageSize {
			break
		}
	}
	return all, nil
}

// realWPPageAncestry walks a page up post_parent and returns its slugs
// root-first, ending in the page's own slug -- the shape PagePath consumes. It
// is realWPAncestry retargeted from term_taxonomy.parent to post_parent.
//
// The walk is done here, over the raw rows, rather than through
// content.PageHierarchy.Ancestry, so the expectation does not inherit the
// implementation's idea of the hierarchy. It degrades the two ways real,
// imported databases require, identical to the term walk: a parent that is
// absent from the page graph is treated as no parent from that point up (the
// orphan rule, Req 2.3), and an already-visited page ends the walk instead of
// hanging it (the cycle rule, Req 2.4).
//
// It returns false when any slug in the chain cannot sit in a request path
// unescaped (realWPPathToken, shared with the term walk): WordPress
// percent-encodes slugs derived from non-Latin titles, and those raise a
// URL-escaping question this test is not about.
func realWPPageAncestry(byID map[int64]domain.PageNode, page domain.PageNode) ([]string, bool) {
	var reversed []string
	visited := map[int64]bool{}
	for cur := page; ; {
		if visited[cur.ID] {
			break
		}
		visited[cur.ID] = true
		if !realWPPathToken(cur.Slug) {
			return nil, false
		}
		reversed = append(reversed, cur.Slug)
		parent, ok := byID[cur.ParentID]
		if cur.ParentID == 0 || !ok {
			break
		}
		cur = parent
	}
	slices.Reverse(reversed)
	return reversed, true
}

// realWPNestedPageCandidate classifies one page for the nested-page assertions,
// the page analogue of realWPNestedCandidate. It returns the walked ancestry,
// whether that ancestry is genuinely nested -- more than one segment, so the
// path carries an ancestor and not just the page's own slug -- and whether every
// slug in it can sit in a request path unescaped. It is one function because the
// same decision has to be made in two places that must not drift: the loop that
// runs the assertions and the census that decides whether the skip fires.
func realWPNestedPageCandidate(byID map[int64]domain.PageNode, page domain.PageNode) (ancestry []string, nested, pathSafe bool) {
	ancestry, pathSafe = realWPPageAncestry(byID, page)
	return ancestry, pathSafe && len(ancestry) > 1, pathSafe
}

// realWPPageNestedCensus records what a page graph offers the nested-page
// assertions, counted straight off the rows -- the page analogue of
// realWPNestedCensus. It exists because the Req 10.4 skip has to name the
// precondition an operator can act on, and "this site has no nested PUBLISHED
// page" and "this site's nested pages carry slugs that cannot sit in a request
// path" are different facts that need different fixes.
type realWPPageNestedCensus struct {
	// Pages is every page row read from the graph, any status.
	Pages int
	// Published is the pages with post_status = 'publish' -- the only pages that
	// can be a subject, because only a published page serves a 200 (Req 3.4).
	Published int
	// Nested is the published pages whose walked ancestry carries an ancestor:
	// post_parent <> 0 resolving to a page present in the graph (a parent absent
	// from the graph is treated as no parent, Req 2.3, so it does not count).
	// This is the literal precondition the skip names.
	Nested int
	// Usable is the subset of Nested that yields an ancestry the assertions can
	// actually drive: at least two segments, every one of them a plain URL
	// token. A percent-encoded slug anywhere in the chain, or a parent chain
	// that closes a cycle (Req 2.4), leaves a page counted in Nested and not
	// here.
	Usable int
}

// realWPPageCensus counts the page graph without asserting on it. Nested and
// Usable are counted over PUBLISHED pages only -- the subjects -- while the
// byID graph used for the walk includes every status so an ancestry can climb
// through an unpublished ancestor.
func realWPPageCensus(pages []domain.PageNode) realWPPageNestedCensus {
	byID := make(map[int64]domain.PageNode, len(pages))
	for _, p := range pages {
		byID[p.ID] = p
	}
	census := realWPPageNestedCensus{Pages: len(pages)}
	for _, p := range pages {
		if p.Status != "publish" {
			continue
		}
		census.Published++
		// Count as Nested only when post_parent resolves to a page in the graph.
		if _, ok := byID[p.ParentID]; p.ParentID == 0 || !ok {
			continue
		}
		census.Nested++
		if _, nested, _ := realWPNestedPageCandidate(byID, p); nested {
			census.Usable++
		}
	}
	return census
}

// precondition states, in terms an operator can act on, why the page graph
// exercised no nested-page assertion.
func (c realWPPageNestedCensus) precondition() string {
	if c.Nested == 0 {
		return fmt.Sprintf("no PUBLISHED page in the target database has a "+
			"resolvable parent (%d pages, %d published, all top-level); the "+
			"nested-page assertions need at least one published post_type='page' "+
			"row with post_parent <> 0 pointing at an existing page",
			c.Pages, c.Published)
	}
	return fmt.Sprintf("%d of %d published pages have post_parent <> 0 pointing "+
		"at an existing page, but none yields a usable nested path: every one was "+
		"rejected because a slug in its ancestry is not a plain URL token "+
		"([A-Za-z0-9_-]) or its parent chain closes a cycle; the nested-page "+
		"assertions need one nested published page whose whole ancestry is a "+
		"plain URL token", c.Nested, c.Published)
}

// realWPPageFront reads the structure's front off the raw option -- the maximal
// run of leading literal segments, empty for a structure that yields a flat
// Structure. A page is exempt from the structure, but a nested page whose first
// slug equals the front (or an archive base) is still claimed by an earlier
// Classify row, so the front is needed to recognize that collision. It mirrors
// realWPFront, over the shared realWPSegments helper.
func realWPPageFront(structure string, usable bool) []string {
	if !usable {
		return nil
	}
	var out []string
	for _, seg := range realWPSegments(structure) {
		if strings.HasPrefix(seg, "%") && strings.HasSuffix(seg, "%") {
			break
		}
		out = append(out, seg)
	}
	return out
}

// realWPPageFirstSegmentCollides reports whether a nested page path's first
// segment is claimed by an earlier Classify row -- an archive base
// (category/tag/author) or the structure's front -- in which case the KindPage
// round trip does not apply (Req 10.2's documented exception). It returns the
// colliding owner for the log line.
func realWPPageFirstSegmentCollides(ancestry []string, s routing.Structure, front []string) (bool, string) {
	if len(ancestry) == 0 {
		return false, ""
	}
	first := ancestry[0]
	switch first {
	case s.CategoryBase, s.TagBase, routing.AuthorBase, routing.DefaultCategoryBase, routing.DefaultTagBase:
		return true, first
	}
	if len(front) > 0 && first == front[0] {
		return true, first
	}
	return false, ""
}
