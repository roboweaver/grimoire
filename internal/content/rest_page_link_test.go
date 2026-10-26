package content

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// This is the TDD RED test for M15 task 5.1. It asserts the gap task 5.2 closes:
// a NESTED page's REST "link" field is the FULL root-first ancestry path
// (/handbook/onboarding/), which requires the RESTMapper to walk the page graph
// to populate the resolved page's transient PageAncestry BEFORE postLink calls
// routing.Structure.Canonical (Req 4.4). Canonical stays the single permalink
// construction site (Req 4.3, 8.5): postLink still delegates to Canonical; all
// 5.2 adds is feeding the walked ancestry in.
//
// CHOSEN SEAM (task 5.2 MUST implement exactly this so these tests go GREEN):
//
//   - RESTMapper.WithPages(p *PageService) *RESTMapper
//         -- the new wiring option, mirroring WithPermalinks/WithUploadsBaseURL
//            (rest.go:205-227). It hands the mapper a *PageService, which already
//            exposes AncestryFor(ctx, id) ([]string, error) (pagehier.go), the
//            single home of the page-ancestry walk. Referencing WithPages here is
//            the RED signal: the method does not exist yet, so this file FAILS TO
//            COMPILE naming WithPages until 5.2 adds it.
//
//   - postLink gains a ctx parameter -- postLink(ctx, p) -- threaded from
//            restCommon (rest.go:270), which ALREADY holds ctx and is postLink's
//            only caller. When p.Type == "page" and a page service is wired,
//            postLink sets p.PageAncestry = pages.AncestryFor(ctx, p.ID) before
//            m.permalinks.Canonical(p), so Canonical's page guard (task 1.2)
//            builds the nested path. A post's link, and a page link when no page
//            service is wired, are unchanged.
//
// These tests depend ONLY on the public surface: mapper.Page(ctx, p).Link and
// mapper.Post(ctx, p).Link. They do not assert the ctx-threading or the field
// population directly, so 5.2 is free to implement the seam above however it
// reaches the same observable links.

// restPageService builds a *PageService over the nested page graph
//
//	handbook (ID 1, parent 0)
//	  └─ onboarding (ID 2, parent 1)
//
// so AncestryFor(ctx, 2) returns ["handbook", "onboarding"] -- the walked,
// root-first, self-inclusive ancestry the mapper must feed Canonical for the
// nested link. It reuses the package's existing fakes: newPageReader/pageNode
// (pagehier_test.go) for the graph and fakePostRepo (post_test.go) for the
// domain.PostRepository argument NewPageService requires (postLink's page path
// reads only the ancestry, so the post repo's returns do not gate the link).
func restPageService() *PageService {
	reader := newPageReader(
		pageNode(1, 0, "handbook", "publish"),
		pageNode(2, 1, "onboarding", "publish"),
	)
	return NewPageService(reader, &fakePostRepo{})
}

// restNestedPage is the leaf page handed to mapper.Page: its ID matches the
// graph node AncestryFor walks (2), its Type is "page", and its Slug is the
// leaf slug. It carries NO PageAncestry itself -- proving the MAPPER populates
// it from the page service, not the caller.
func restNestedPage() domain.Post {
	p := samplePost()
	p.ID = 2
	p.Type = "page"
	p.Slug = "onboarding"
	return p
}

// TestRESTPageLinkNestedPageIsNestedCanonical is the core RED assertion: under a
// dated structure (which a page is EXEMPT from), a nested page's link is the
// full ancestry path /handbook/onboarding/, NOT the /{slug} degrade and NOT the
// dated post shape. This only holds once 5.2 wires WithPages so the mapper walks
// the graph to populate PageAncestry before Canonical (Req 4.4).
func TestRESTPageLinkNestedPageIsNestedCanonical(t *testing.T) {
	st, err := routing.Parse(restStructDayAndName, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", restStructDayAndName, err)
	}
	mapper := newTestRESTMapper().WithPermalinks(st).WithPages(restPageService())

	page, err := mapper.Page(context.Background(), restNestedPage())
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if want := "/handbook/onboarding/"; page.Link != want {
		t.Errorf("nested page Link = %q, want %q (full ancestry, exempt from the dated structure)", page.Link, want)
	}
}

// TestRESTPageLinkWithoutPagesDegradesToSlug documents that the page-hierarchy
// reader is what ENABLES nesting: a nested page mapped by a mapper WITHOUT
// WithPages has no walked ancestry, so Canonical's page guard degrades to
// []string{p.Slug} and the link is just /{slug}. 5.2 must leave this fallback
// intact (a page link is unchanged when no page service is wired).
func TestRESTPageLinkWithoutPagesDegradesToSlug(t *testing.T) {
	st, err := routing.Parse(restStructDayAndName, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", restStructDayAndName, err)
	}
	mapper := newTestRESTMapper().WithPermalinks(st) // no WithPages

	page, err := mapper.Page(context.Background(), restNestedPage())
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if want := "/onboarding/"; page.Link != want {
		t.Errorf("nested page Link without WithPages = %q, want the %q degrade", page.Link, want)
	}
}

// TestRESTPageLinkTopLevelPageIsSlug asserts a top-level page's link is /{slug}
// even with WithPages wired (Req 4.5): AncestryFor of a root page is the
// one-element [slug], so its canonical is /{slug} under the dated structure's
// trailing slash -- a fixed point, never the dated post shape.
func TestRESTPageLinkTopLevelPageIsSlug(t *testing.T) {
	st, err := routing.Parse(restStructDayAndName, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", restStructDayAndName, err)
	}
	mapper := newTestRESTMapper().WithPermalinks(st).WithPages(restPageService())

	p := samplePost()
	p.ID = 1
	p.Type = "page"
	p.Slug = "handbook"
	page, err := mapper.Page(context.Background(), p)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if want := "/handbook/"; page.Link != want {
		t.Errorf("top-level page Link = %q, want %q", page.Link, want)
	}
}

// TestRESTPageLinkPostUnchangedWithPages asserts a POST's link is unchanged by
// the page wiring (Req 4.2): under the dated structure a post still links to its
// dated canonical /2024/03/05/hello-world/, proving the page path never affects
// posts and no second URL builder is introduced.
func TestRESTPageLinkPostUnchangedWithPages(t *testing.T) {
	st, err := routing.Parse(restStructDayAndName, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", restStructDayAndName, err)
	}
	mapper := newTestRESTMapper().WithPermalinks(st).WithPages(restPageService())

	post, err := mapper.Post(context.Background(), samplePost())
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if want := "/2024/03/05/hello-world/"; post.Link != want {
		t.Errorf("post Link = %q, want %q (unchanged by page wiring)", post.Link, want)
	}
}

// newTestRESTMapper builds a RESTMapper with the package's existing REST fakes
// (rest_test.go) for terms/meta/users -- the inputs NewRESTMapper and the Page/
// Post mappers read for featured_media etc. -- without a permalink structure or
// page service, so each test adds exactly the wiring it exercises. It mirrors
// newTestMapperWithPermalinks (rest_permalink_test.go) minus the permalink step.
func newTestRESTMapper() *RESTMapper {
	terms := &fakeRESTPostTerms{byPost: map[int64]map[string][]int64{}}
	meta := &fakeRESTPostMeta{featured: map[int64]int64{}, attach: map[int64]string{}}
	users := &fakeRESTUserMeta{values: map[int64]map[string]string{}}
	return NewRESTMapper(terms, meta, users, "wp_")
}
