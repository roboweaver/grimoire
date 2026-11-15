# Page Resolve Fix Bugfix Design

## Overview

`PageService.Resolve` (`internal/content/pagehier.go`, ~line 259) walks the page graph to a leaf, then confirms the leaf with `posts.BySlug(ctx, leaf.Slug)`. A slug is not an identity: it ignores the walked path (same-slug pages under different parents) and the default types `{post, page}` let a post win. On a failed walk it returns `h.recoverPath(segments)`, whose `*PageMovedError` names a page without any published check, so the handler's 301 discloses a draft or private page's canonical path.

The fix copies the already-correct pattern in `CustomPostService.Resolve` (`internal/content/custompost.go` lines ~54-85): confirm by ID with `PublishedByID(ctx, leaf.ID, "page")`, and gate the moved-page redirect on `PublishedByID(ctx, moved.Page.ID, "page")`. No port, storage, handler or REST change is needed.

## Glossary

- **Bug_Condition (C)**: A `Resolve` input where the slug-based leaf confirmation returns a different row than the walked leaf (Defects A, B), or where the recovery path names an unpublished page (Defect C).
- **Property (P)**: `Resolve` returns exactly the walked leaf page (by ID, type `page`), and never redirects to or discloses an unpublished page.
- **Preservation**: All behaviour for inputs outside C, including 200 for a unique published page, 301 for a published flat or wrong-ancestor URL, and 404 for unknown or unpublished leaves.
- **PageService.Resolve**: The function in `internal/content/pagehier.go` that walks segments and confirms the leaf.
- **PublishedByID(ctx, id, types...)**: `domain.PostRepository` read in `internal/domain/repository.go`. It returns the post only if `post_status='publish'` and its type is in `types` (default `{post,page}`), else `domain.ErrNotFound`. Implemented in `internal/storage/wprepo/repo.go`.
- **PageMovedError**: Error type carrying `Page domain.PageNode` and root-first `Ancestry`; the web `page()` handler maps it to a 301.
- **recoverPath**: `PageHierarchy` method that looks up the final segment anywhere in the graph (lowest ID wins).

## Bug Details

### Bug Condition

The bug manifests when the leaf is confirmed by slug rather than by ID, or when the recovery redirect is returned without a published check.

**Formal Specification:**
```
FUNCTION isBugCondition(input)
  INPUT: input of type PageResolveInput { segments, graph, posts }
  OUTPUT: boolean

  leaf <- walk(input.graph, input.segments)
  IF leaf exists THEN
    // Defects A and B: BySlug(leaf.Slug) is not the row the walk selected
    RETURN bySlug(leaf.Slug).ID != leaf.ID
  ELSE
    // Defect C: recovery names a non-published page
    page <- lowestIDPageWithSlug(input.graph, last(input.segments))
    RETURN page exists AND NOT isPublishedPage(page.ID)
  END IF
END FUNCTION
```

### Examples

- Published pages `/a/x` (ID 11) and `/b/x` (ID 12). `GET /b/x/` expected: page 12 with ancestry `[b, x]`. Actual: whichever row `BySlug("x")` returns, for example page 11, served at `/b/x/`.
- Published page `/team` (ID 5) and published post `team` (ID 9). `GET /about/team/` where `team` is nested expected: the page. Actual: `BySlug("team")` may return post 9.
- Draft page `handbook/secret` (ID 7). `GET /secret/` expected: 404. Actual: 301 to `/handbook/secret/`, revealing the draft exists and where.
- Wrong-ancestor path `/wrong/secret/` for the same draft: expected 404, actual 301.
- Published page `handbook/onboarding`. `GET /onboarding/` expected and actual: 301 to `/handbook/onboarding/` (unchanged).

## Expected Behavior

### Preservation Requirements

**Unchanged Behaviors:**
- A unique published page resolves with HTTP 200 and its root-first ancestry.
- A published page requested at a flat or wrong-ancestor URL returns `*PageMovedError` and 301s to its canonical path.
- A draft leaf found by the walk, an unknown final segment, and empty segments each return `domain.ErrNotFound`.
- `PageService.AncestryFor`, `Server.single` flat-leaf 301, and `RESTMapper` page `link` are unchanged.

**Scope:**
All inputs outside C are unaffected. This includes top-level pages, unique-slug nested pages, REST reads, post/term/CPT routes, and every non-`PageService` consumer of `BySlug`. `BySlug` itself is not modified.

**Caller review (verified against the code):**
- `internal/web/handlers.go` `Server.page` (~line 463): the only caller of `Resolve`. It maps `*PageMovedError` to a 301 and other errors through the normal error path, and renders the returned leaf. It needs no change: a 404 now arises through the existing `domain.ErrNotFound` handling.
- `internal/web/handlers.go` `Server.single` (~line 155): calls `AncestryFor(post.ID)` for a post already confirmed published by the post path. Unaffected.
- `internal/content/rest.go` `RESTMapper.pages`: uses `AncestryFor` only. Unaffected.
- `cmd/grimoire/assembly.go` (~line 79): sole construction site, `NewPageService(repos.PageReader, repos.Posts)`. The signature is unchanged.

## Hypothesized Root Cause

1. **Slug used as identity (A, B)**: M15 reused `PostService.BySlug`'s published filter as a cheap confirmation, since it already existed, without noticing it is slug-keyed and type-defaulted. The graph already supplies `leaf.ID`, and `PublishedByID` accepts it with a type filter. This is confirmed by `CustomPostService.Resolve`, whose comment says the leaf is "confirmed by ID, never by BySlug" for exactly this reason.
2. **Recovery path not gated (C)**: `recoverPath` operates on the all-status graph (`ListPages` applies no `post_status` filter by design, so a published leaf can sit below a draft ancestor). M15 relied on the leaf check for non-disclosure but the redirect branch bypasses it. M12 fixed this in `CustomPostService.Resolve` (its comment: "Never disclose an unpublished node by redirecting to it"); M15 was not back-ported.

## Correctness Properties

Property 1: Bug Condition - Resolve confirms the walked leaf and never discloses unpublished pages

_For any_ input where the bug condition holds (isBugCondition returns true), the fixed `PageService.Resolve` SHALL return the published page whose ID equals the walked leaf's ID, with type `page` and that leaf's own root-first ancestry (never a same-slug page under another parent and never a same-slug post), and where the walk fails and the final segment names an unpublished page SHALL return `domain.ErrNotFound` and not a `*PageMovedError`.

**Validates: Requirements 2.1, 2.2, 2.3**

Property 2: Preservation - Non-buggy Resolve behaviour is unchanged

_For any_ input where the bug condition does NOT hold (isBugCondition returns false), the fixed `Resolve` SHALL produce the same result as the original function, preserving 200 for unique published pages, `*PageMovedError` for published flat or wrong-ancestor URLs, `ErrNotFound` for unpublished leaves, unknown slugs and empty segments, and unchanged `AncestryFor` output.

**Validates: Requirements 3.1, 3.2, 3.3, 3.4, 3.5, 3.6**

## Fix Implementation

### Changes Required

**File**: `internal/content/pagehier.go`

**Function**: `PageService.Resolve` (plus doc comments on `PageService` and `Resolve`)

**Specific Changes**:
1. **Gate the recovery path**: replace `return domain.Post{}, nil, h.recoverPath(segments)` with: `rerr := h.recoverPath(segments)`; `var moved *PageMovedError`; if `errors.As(rerr, &moved)` then `if _, err := s.posts.PublishedByID(ctx, moved.Page.ID, "page"); err != nil { return domain.Post{}, nil, err }`; then `return domain.Post{}, nil, rerr`. A non-published page yields `ErrNotFound` from `PublishedByID`, which is the not-found error the handler already maps to 404. Other repository errors propagate as before.
2. **Confirm the leaf by ID and type**: replace `s.posts.BySlug(ctx, leaf.Slug)` with `s.posts.PublishedByID(ctx, leaf.ID, "page")`.
3. **Imports**: add `errors` if not already imported in `pagehier.go`.
4. **Comments**: update the `PageService` and `Resolve` doc comments, which currently say `BySlug`, to say `PublishedByID` by ID and `"page"`, and note that the redirect is gated on published status.
5. **Existing test fixtures**: `TestPageServiceResolvePublishedLeaf` in `internal/content/pagehier_test.go` stubs `bySlugPost`; it must stub `byIDPost` instead, since the confirmation read changes. This is a fixture edit that preserves the test's intent and assertions, not a behaviour change.

No change to `domain`, `storage`, `internal/web`, `RESTMapper`, or `cmd/grimoire`.

## Testing Strategy

### Validation Approach

Write failing regression tests first (RED) on unfixed code, then apply the fix, then re-run the same tests plus the full suite. The tests use the `fakePostRepo` in `internal/content/post_test.go` (it only supports one canned `byIDPost`/`bySlugPost`), so per-ID and per-type answers need a small page-aware fake in the new test file, in the style of `cptRepo` in `custompost_test.go`.

### Exploratory Bug Condition Checking

**Goal**: Surface counterexamples on UNFIXED code to confirm the root cause.

**Test Plan**: Add tests in `internal/content/pagehier_resolve_test.go` using `pageNode(...)`/`newPageReader(...)` from `pagehier_test.go` and a per-key fake repo. Run on unfixed code and observe failures.

**Test Cases**:
1. **Same-slug leaves**: graph `a`(1) > `x`(11), `b`(2) > `x`(12), both published. `Resolve([a x])` returns post 11 and ancestry `[a x]`; `Resolve([b x])` returns post 12 and ancestry `[b x]` (fails on unfixed code).
2. **Post does not shadow page**: a published post and published page both have slug `team`; `Resolve([about team])` returns the page, and a `BySlug` stub that returns the post must not be consulted (fails on unfixed code).
3. **Draft flat URL**: draft page `secret` under `handbook`; `Resolve([secret])` and `Resolve([wrong secret])` return `ErrNotFound` and not `*PageMovedError` (fails on unfixed code).
4. **Published flat URL**: published `onboarding` under `handbook`; `Resolve([onboarding])` returns `*PageMovedError` with ancestry `[handbook onboarding]` (passes on unfixed code; kept as a guard).

**Expected Counterexamples**:
- `Resolve([b x])` returns the page that `BySlug("x")` selected, with the wrong ID.
- `Resolve([secret])` returns `*PageMovedError` for a draft.
- Possible causes: slug-keyed confirmation; unconditional `recoverPath` return.

### Fix Checking

**Goal**: For all inputs where the bug condition holds, the fixed function produces the expected behaviour.

**Pseudocode:**
```
FOR ALL input WHERE isBugCondition(input) DO
  result := Resolve_fixed(input)
  ASSERT (leaf exists AND result.post.ID = leaf.ID AND result.post.Type = "page"
          AND result.ancestry = Ancestry(leaf.ID))
      OR (NOT leaf AND errors.Is(result.err, ErrNotFound) AND NOT errors.As(result.err, *PageMovedError))
END FOR
```

### Preservation Checking

**Goal**: For all inputs where the bug condition does NOT hold, the fixed function matches the original.

**Pseudocode:**
```
FOR ALL input WHERE NOT isBugCondition(input) DO
  ASSERT Resolve_original(input) = Resolve_fixed(input)
END FOR
```

**Testing Approach**: Property-based testing is recommended for preservation because it generates many graphs and paths automatically and catches edge cases a hand-written table misses. Check whether the repo already uses `testing/quick` or `pgregory.net/rapid` before choosing; if neither is used in `internal/content`, use a seeded table-driven loop over generated graphs from `math/rand` rather than adding a dependency.

**Test Plan**: Observe `Resolve` on UNFIXED code for graphs with unique slugs and all-published pages, then assert the same outputs after the fix. Compare against a reference model: unique-slug published leaf returns that page, otherwise recovery per the published status.

**Test Cases**:
1. **Unique published leaf**: 200-path result (post and ancestry) is identical for random unique-slug graphs.
2. **Published flat/wrong-ancestor**: `*PageMovedError` with the same canonical ancestry.
3. **Unknown slug and empty segments**: `ErrNotFound`.
4. **Draft leaf found by walk**: `ErrNotFound`.
5. **Repository error propagation**: a non-NotFound error from `PublishedByID` is returned unchanged.

### Unit Tests

- Same-slug leaves under different parents resolve to their own page and ancestry.
- Same-slug post does not shadow the page; `PublishedByID` is called with `(leaf.ID, "page")` and `BySlug` is not called.
- Draft or private page via flat URL and via wrong-ancestor URL returns `ErrNotFound`.
- Published page via flat and wrong-ancestor URL returns `*PageMovedError` with the canonical ancestry.
- Existing `PageService` and `PageHierarchy` tests stay green.

### Property-Based Tests

- Random page graphs with unique slugs: Resolve output is unchanged by the fix (preservation).
- Random graphs with duplicate slugs: Resolve returns the walked leaf's ID and ancestry for every published path.
- Random publish/draft assignments: a `*PageMovedError` is returned only when the moved page is published.

### Integration Tests

- Web handler test (`internal/web`): with a real or fake-backed Server, `GET /a/x/` and `GET /b/x/` serve distinct bodies (200); a draft's flat URL returns 404 with no `Location` header; a published nested page's flat URL returns 301 to its canonical.
- REST: `/wp-json/wp/v2/pages` `link` for nested pages is unchanged (calls `AncestryFor` only).
- Real-DB contract: a SQLite-backed `PageService` over the storagetest fixtures covers both same-slug cases against actual `PublishedByID` filters (type and status), if a fixture helper exists; otherwise note the gap in the final gate report.
