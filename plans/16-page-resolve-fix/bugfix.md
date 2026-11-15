# Bugfix Requirements Document

## Introduction

`PageService.Resolve` in `internal/content/pagehier.go` (M15, nested page URLs) has two defects. Both are already handled correctly by the M12 sibling `CustomPostService.Resolve` in `internal/content/custompost.go`.

1. **Wrong leaf confirmation.** After the page-graph walk finds the leaf node, `Resolve` confirms it with `posts.BySlug(ctx, leaf.Slug)`. `BySlug` knows nothing about the walked path or the leaf ID. It has two effects:
   - **Defect A (same slug, different parents).** With published pages `/a/x` and `/b/x`, both URLs return whichever row `BySlug` picks, so one URL serves the other page's body, and its ancestry is used for the canonical.
   - **Defect B (post shadows page).** `BySlug` defaults to types `{post, page}`. A published post that shares the leaf's slug can be returned in place of the page.
2. **Unpublished page disclosed by redirect (Defect C).** When the walk fails, `Resolve` returns `h.recoverPath(segments)`. That returns a `*PageMovedError` for the lowest-ID page with the final slug without checking it is published. The handler turns it into a 301 whose `Location` is the page's canonical path, so a draft, private or pending page's existence and path are disclosed to anonymous visitors.

The fix mirrors `CustomPostService.Resolve`: confirm the leaf by ID with `s.posts.PublishedByID(ctx, leaf.ID, "page")`, and on `*PageMovedError` verify `moved.Page.ID` is published through `PublishedByID(ctx, id, "page")` before returning it, otherwise return the not-found error. Public behaviour is otherwise identical.

Other callers of `PageService` were checked. `Server.page` (`internal/web/handlers.go`) calls `Resolve` and is fixed by this change. `Server.single` and `RESTMapper` call only `AncestryFor`, which takes an ID and does not call `BySlug`, so they are unaffected and out of scope. The only construction site is `cmd/grimoire/assembly.go`.

## Bug Analysis

### Current Behavior (Defect)

1.1 WHEN two published pages share a slug under different parents (for example `/a/x` and `/b/x`) and either path is requested THEN the system confirms the leaf by slug alone and returns the single page `BySlug` selects, so both URLs serve the same page and the wrong page's ancestry can be returned

1.2 WHEN a published post shares the leaf page's slug and the page path is requested THEN the system may return that post as the resolved page, because `BySlug` searches types `{post, page}`

1.3 WHEN a page path walk fails and the final segment names a draft, private, pending or trashed page THEN the system returns a `*PageMovedError` for it, so the handler answers 301 to that page's canonical path instead of 404

### Expected Behavior (Correct)

2.1 WHEN two published pages share a slug under different parents and either path is requested THEN the system SHALL return the page at exactly that path, confirmed by the walked leaf's ID, together with that leaf's own ancestry

2.2 WHEN a published post shares the leaf page's slug and the page path is requested THEN the system SHALL return the page and never the post, because the leaf is confirmed with type `"page"` only

2.3 WHEN a page path walk fails and the final segment names a page that is not published THEN the system SHALL return `domain.ErrNotFound` (HTTP 404) and SHALL NOT return a `*PageMovedError`

### Unchanged Behavior (Regression Prevention)

3.1 WHEN a page path resolves to a published leaf with no same-slug competitor THEN the system SHALL CONTINUE TO return that page with its root-first ancestry and render it with HTTP 200 at its canonical path

3.2 WHEN a page path walk fails and the final segment names a published page (a flat leaf such as `/onboarding` or a wrong-ancestor path) THEN the system SHALL CONTINUE TO return a `*PageMovedError` carrying that page's canonical ancestry so the handler answers 301 to the canonical path

3.3 WHEN the walked leaf is present in the graph but not published THEN the system SHALL CONTINUE TO return `domain.ErrNotFound`

3.4 WHEN the final segment names no page at all, or `segments` is empty THEN the system SHALL CONTINUE TO return `domain.ErrNotFound`

3.5 WHEN `AncestryFor` is called (by `Server.single` for the flat-leaf 301, and by `RESTMapper` for a page's nested `link`) THEN the system SHALL CONTINUE TO return the same root-first ancestry, and the top-level page, flat-leaf 301 and REST `link` behaviour SHALL remain unchanged

3.6 WHEN `CustomPostService`, term archives, post routing or any other `PostRepository` consumer is used THEN the system SHALL CONTINUE TO behave exactly as before, with no change to `domain.PostRepository`, `domain.PageReader`, or storage
