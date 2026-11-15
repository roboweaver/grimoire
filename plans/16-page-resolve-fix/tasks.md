# Implementation Plan

## Overview

> Bugfix for `PageService.Resolve` (`internal/content/pagehier.go`). Strict TDD: RED tests first, then the fix, then the gate. Every task's first line is unique and must not be reworded. Keep `gofmt -l .` empty and `go vet ./...`, `go build ./...`, `go test ./...` green at the end of each task except the RED task, which fails by design.

## Tasks

- [x] 1. RED: write bug condition exploration tests for page leaf confirmation and unpublished redirect
  - **Property 1: Bug Condition** - Resolve confirms the walked leaf by ID and never discloses unpublished pages
  - **CRITICAL**: These tests MUST FAIL on unfixed code. Failure confirms the bugs exist. Do NOT fix the code or weaken the tests when they fail.
  - **GOAL**: Surface counterexamples for Defects A, B and C
  - Add `internal/content/pagehier_resolve_test.go` using `pageNode`, `newPageReader` and `assertSlugs` from `pagehier_test.go`, and a small per-ID, per-type fake `domain.PostRepository` in the style of `cptRepo` (`custompost_test.go`). `BySlug` on that fake must return a deliberately wrong same-slug row (the other page, or a post), so slug-based confirmation is observable.
  - Test same-slug leaves: graph `a`(1)>`x`(11), `b`(2)>`x`(12), both published. `Resolve([a x])` returns post 11 with ancestry `[a x]`; `Resolve([b x])` returns post 12 with ancestry `[b x]`.
  - Test post does not shadow page: a same-slug post exists; `Resolve` returns the page (Type `page`), asserts `PublishedByID` was called with `(leaf.ID, "page")` and `BySlug` was not called.
  - Test draft flat URL and draft wrong-ancestor URL: `Resolve([secret])` and `Resolve([wrong secret])` return `domain.ErrNotFound` and `errors.As(err, *PageMovedError)` is false.
  - Test published flat URL still moves: `Resolve([onboarding])` returns `*PageMovedError` whose `Ancestry` is `[handbook onboarding]` (guard; passes before and after).
  - Run `go test ./internal/content -run PageServiceResolve` on UNFIXED code
  - **EXPECTED OUTCOME**: same-slug, post-shadow and draft-redirect tests FAIL (this proves the bugs exist); the published-redirect guard passes
  - Document the counterexamples (for example, `Resolve([b x])` returned page 11; `Resolve([secret])` returned `*PageMovedError`)
  - Mark complete when the tests are written, run, and the failures are documented
  - _Requirements: 1.1, 1.2, 1.3, 2.1, 2.2, 2.3_

- [x] 2. RED: write preservation property tests for non-buggy Resolve behaviour (before the fix)
  - **Property 2: Preservation** - Non-buggy Resolve behaviour is unchanged
  - **IMPORTANT**: Follow the observation-first methodology
  - Observe on UNFIXED code, for inputs where `isBugCondition` is false (unique slugs, no competing post, published or genuinely absent pages): unique published leaf returns that page and root-first ancestry; published flat or wrong-ancestor path returns `*PageMovedError` with the canonical ancestry; unknown final segment and empty segments return `ErrNotFound`; a draft leaf found by the walk returns `ErrNotFound`; a non-NotFound repository error is propagated
  - Write property-style tests in `internal/content/pagehier_resolve_test.go` (or `pagehier_preserve_test.go`) over generated unique-slug graphs with a seeded `math/rand` loop (add no dependency unless `testing/quick` or `rapid` is already used in `internal/content`). Because the unfixed code confirms by slug and unique slugs make slug and ID coincide, the fake repo must answer both `BySlug` and `PublishedByID` consistently
  - Confirm the existing `TestPageServiceResolvePublishedLeaf` and `TestPageServiceResolveDraftLeafIsNotFound` and all `PageHierarchy` tests pass unchanged on unfixed code
  - Run `go test ./internal/content` on UNFIXED code
  - **EXPECTED OUTCOME**: preservation tests PASS (this confirms the baseline to preserve)
  - Mark complete when the tests are written, run, and passing on unfixed code
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6_

- [x] 3. Fix PageService.Resolve to confirm the leaf by ID and gate the moved-page redirect

  - [x] 3.1 Implement the fix in `internal/content/pagehier.go`
    - Replace `s.posts.BySlug(ctx, leaf.Slug)` with `s.posts.PublishedByID(ctx, leaf.ID, "page")` and return its error unchanged
    - Replace `return domain.Post{}, nil, h.recoverPath(segments)` with: capture `rerr := h.recoverPath(segments)`; if `errors.As(rerr, &moved)` for `*PageMovedError`, call `s.posts.PublishedByID(ctx, moved.Page.ID, "page")` and return its error if non-nil; otherwise return `rerr` (mirror `CustomPostService.Resolve`, `custompost.go` ~lines 70-81)
    - Add the `errors` import if missing; update the `PageService` and `Resolve` doc comments that mention `BySlug`
    - Update the existing `TestPageServiceResolvePublishedLeaf` fixture to stub `byIDPost` instead of `bySlugPost` (intent and assertions unchanged); do not touch any other existing test
    - No change to `domain`, `storage`, `internal/web`, `RESTMapper` or `cmd/grimoire`
    - _Bug_Condition: isBugCondition(input) from design (slug-confirmed leaf differs from the walked leaf, or recovery names an unpublished page)_
    - _Expected_Behavior: expectedBehavior(result) from design (leaf returned by ID and type page; unpublished moved page returns ErrNotFound)_
    - _Preservation: Preservation Requirements from design_
    - _Requirements: 2.1, 2.2, 2.3, 3.1, 3.2, 3.3, 3.4, 3.5, 3.6_

  - [x] 3.2 Verify bug condition exploration tests now pass
    - **Property 1: Expected Behavior** - Resolve confirms the walked leaf by ID and never discloses unpublished pages
    - **IMPORTANT**: Re-run the SAME tests from task 1. Do NOT write new tests
    - Run `go test ./internal/content -run PageServiceResolve`
    - **EXPECTED OUTCOME**: tests PASS (confirms the bugs are fixed)
    - _Requirements: 2.1, 2.2, 2.3_

  - [x] 3.3 Verify preservation tests still pass
    - **Property 2: Preservation** - Non-buggy Resolve behaviour is unchanged
    - **IMPORTANT**: Re-run the SAME tests from task 2. Do NOT write new tests
    - Run `go test ./internal/content`
    - **EXPECTED OUTCOME**: tests PASS (confirms no regressions)
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6_

- [x] 4. Verify web and REST callers of PageService behave the same
  - Add handler-level regression coverage in `internal/web` (next to the existing M15 page tests; find them with `grep -rn "WithPages" internal/web/*_test.go`): `GET /a/x/` and `GET /b/x/` each return 200 with their own page body; a draft page's flat URL and wrong-ancestor URL return 404 with no `Location` header; a published nested page's flat URL still returns 301 to its canonical
  - Confirm by reading and by test that `Server.single` (`AncestryFor`) and `RESTMapper` page `link` are unchanged: run the existing M15 REST and `cmd/grimoire` assembly tests (`go test ./internal/web ./internal/content ./cmd/grimoire`) with no edits to them
  - If `cmd/grimoire` assembly tests cover nested pages over SQLite, extend them with a same-slug pair only if cheap; otherwise record the gap in the task 5 report
  - These tests PASS after task 3; they are regression coverage, not RED
  - _Requirements: 2.1, 2.3, 3.1, 3.2, 3.5_

- [x] 5. Final gate: full build, vet, format and test suite
  - Run `gofmt -l .` (must print nothing), `go vet ./...`, `go build ./...`, `go test ./...`
  - Confirm `git diff --stat` touches only `internal/content/pagehier.go`, the new and edited test files, and `plans/16-page-resolve-fix/`
  - Confirm `grep -n "BySlug" internal/content/pagehier.go` shows no code use (comments only if any)
  - Confirm no existing test was modified other than the `bySlugPost` to `byIDPost` fixture change in `TestPageServiceResolvePublishedLeaf`
  - Report any gap recorded in task 4
  - _Requirements: 2.1, 2.2, 2.3, 3.1, 3.2, 3.3, 3.4, 3.5, 3.6_

## Notes

- Remember to write the exploration test (task 1) BEFORE the fix and run it on UNFIXED code to see it fail. Follow the observation-first method for task 2.
- Mirror `CustomPostService.Resolve` in `internal/content/custompost.go` (~lines 54-85).
- `domain.PostRepository.PublishedByID(ctx, id, types...)` returns `domain.ErrNotFound` for a non-published row or a type outside `types`.
- Tasks 1 and 2 touch the same new test file, so they are serialized.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1"] },
    { "id": 1, "tasks": ["2"] },
    { "id": 2, "tasks": ["3.1"] },
    { "id": 3, "tasks": ["3.2", "3.3"] },
    { "id": 4, "tasks": ["4"] },
    { "id": 5, "tasks": ["5"] }
  ]
}
```
