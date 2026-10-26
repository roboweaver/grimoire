# Implementation Plan: M15 — hierarchical page URLs

## Overview

> **This milestone retargets M9b's already-proven nested-walk machine from
> `term_taxonomy.parent` to `post_parent`, serving a `page` under its parent-page
> ancestry instead of under the permalink structure's post shape.** M9b built the
> whole pipeline for categories — `Structure.Classify`
> (`internal/routing/classify.go:65`) → `resolve` (`internal/web/handlers.go:65`)
> → `categoryArchive` (`handlers.go:300`) → `TermService.CategoryArchive`
> (`internal/content/term.go`) → `LoadTermHierarchy` / `ResolvePath` / `Ancestry`
> / `recoverPath` / `CategoryMovedError` (`internal/content/archive.go:94`, `:149`,
> `:251`, `:167`, `:49`), 301ing via `redirectCanonical` (`handlers.go:137`) from
> `CategoryPath` (`internal/routing/routing.go:537`). M15 instantiates each of
> these over `post_parent` on `post_type = 'page'` rows. It invents no new
> algorithm; it mirrors a validated one, closing the one place M9a's canonical can
> differ from the published URL (`docs/compatibility.md:119-125`, `:324`).
>
> **What this adds — and only this.** One `KindPage` classifier constant appended
> to the `Kind` block (`internal/routing/classify.go:11-22`) and one precedence
> row (two-or-more segments, after the archive rows and the post-shape row 7,
> before the flat `/{slug}` row 8). One pure path constructor
> `Structure.PagePath(ancestry)` beside `CategoryPath` (`routing.go:537`) plus a
> one-line page guard at the top of `Structure.Canonical` (`routing.go:408`)
> reading an ancestry carried transiently on the resolved `domain.Post`. A content
> `PageHierarchy` + `PageService` + `PageMovedError` mirroring the term equivalents
> (`internal/content/archive.go`), in a new `internal/content/pagehier.go`. A
> `resolve` `KindPage` arm and a `single()` page branch converging on ONE
> page-canonical computation (`internal/web/handlers.go:65`, `:103`). One additive
> `domain.PageReader` / `PostRepo.ListPages` read port (beside the M14 ports at
> `internal/domain/repository.go:476`/`:492`) implemented on the single existing
> `*PostRepo` and exposed as one new `Set` field (`internal/storage/factory.go`).
> One page wildcard route from `contentPatterns` plus a `requireArchiveDeps`-style
> startup guard (`internal/web/router.go:225-277`). A REST page `link` via the same
> `Canonical` page branch, fed by a `WithPages` reader on the mapper
> (`internal/content/rest.go:205-227`, `:234`). It reuses M9b's walk; it adds NO
> migration, NO column, NO capability, and NO second permalink construction site.
>
> **No migration, no new column, no new capability, no second permalink builder.**
> `post_parent` and `post_name` already live in `postColumns`
> (`internal/storage/wprepo/repo.go:17-24`); the `PageReader` port is additive to
> `domain` and `wprepo` and alters no existing `PostRepo` read; page routing reads
> published pages only through the existing `BySlug`/`PublishedByID` filters
> (`internal/storage/wprepo/repo.go:132`, `:166`) and makes no authorization
> decision; `Structure.Canonical` remains the single construction site (the REST
> `link` updates for free because `postLink` reads it). A top-level page stays at
> `/{slug}` exactly as today — no top-level page URL changes (Req 1.2).
>
> **Standing gate convention.** Keep `gofmt -l .` empty and `go vet ./...`,
> `go build ./...`, `go test ./...` green after every phase, following the
> close-out discipline of M10a/M10.B/M10.C/M10.F/M11/M13/M14.
>
> **Strict-TDD spine.** Every behavior-adding sub-task is preceded by the sub-task
> that writes its failing test (1.1→1.2, 2.1→2.2, 3.1→3.2, 4.1→4.2, 5.1→5.2),
> matching how M5–M14 were run. The routing, storage, content, web, and REST edits
> live in largely different files — `internal/routing/{classify,routing}.go`, the
> cross-vendor contract in `internal/storage/storagetest/contract.go`, the content
> walk in a new `internal/content/pagehier.go`, the handler/router edits in
> `internal/web/{handlers,router}.go`, and the REST link in
> `internal/content/rest.go` — so the test and impl tasks pair across consecutive
> waves without two tasks in one wave ever editing the same file.
>
> **The transient ancestry field is a routing/transport convenience, not a port
> change.** `Structure.Canonical` stays a pure function of its single `domain.Post`
> argument; the page's walked ancestry rides on a transient, non-persisted field
> populated by the page resolution path and read via `pageAncestryFor(p)`,
> degrading to `[]string{p.Slug}` for a top-level page. This field lives in
> `internal/domain/entities.go` (the `domain.Post` definition), DISTINCT from the
> new `domain.PageReader`/`PageNode` ports in `internal/domain/repository.go`, so
> the two domain edits (1.2 and 2.2) touch different files and never collide
> (Req 8.4; D4).

## Tasks

## Phase 1 — Routing: `KindPage` + `PagePath` + the `Canonical` page guard (`internal/routing`, pure, TDD)

- [x] 1.1 Write the failing routing unit tests first. Add
      `internal/routing/pagepath_test.go` (new file, `package routing`, pure, no
      DB), asserting, per the design's Testing strategy:
      `Structure.PagePath` — a one-element ancestry yields `/{slug}` under a flat
      structure and `/{slug}/` under a trailing-slash structure (Req 1.2, 4.5); a
      multi-element ancestry yields the nested path with NO front and NO structure
      tokens (Req 1.1, 4.1); an empty ancestry yields `""` (the "no servable
      canonical" guard, mirroring `CategoryPath`'s empty-ancestry case).
      `Structure.Canonical` page branch — a `page` (built with the transient
      ancestry field set, read via `pageAncestryFor(p)`) returns its nested
      ancestry path and NEVER a dated/post-shape path, under flat, dated,
      day-and-name, and custom structures (Req 1.4, 4.1); a top-level page with no
      attached ancestry degrades to `/{slug}` (Req 1.2, 4.5); a `post` is
      byte-for-byte unchanged, asserted against the existing `Canonical` vectors
      (Req 4.2). `Classify` row placement — a two-plus-segment non-archive
      non-post path → `KindPage` with `Target.Segments` set (Req 6.3 outcome via
      the walk); an archive base path still classifies as its archive Kind
      (Req 6.4); a post-shape path still → `KindPost` (Req 6.5); a one-segment path
      still → `KindPost` (Req 6.1); the existing per-row category/tag/author/date/
      post tests in `classify_test.go` still pass unchanged (Req 6 regression
      guard). The `Canonical` page branch assumes the transient-ancestry seam
      (`pageAncestryFor(p)` reading a field on `domain.Post`) — document that the
      test depends on that seam landing in 1.2.
      Files: `internal/routing/pagepath_test.go`
      Verify: `go test ./internal/routing -run PagePath` — fails (`KindPage`,
      `Structure.PagePath`, the `Canonical` page guard, and the transient
      `domain.Post` ancestry field do not exist yet). _(Req 1.1, 1.2, 1.4, 4.1,
      4.2, 4.5, 6.1, 6.3, 6.4, 6.5; D1, D4, D5)_

- [x] 1.2 Implement `KindPage`, the classifier row, `PagePath`, and the
      `Canonical` page guard. Exactly per design: in
      `internal/routing/classify.go` append `KindPage` to the `Kind` const block
      (`classify.go:11-22`, appended after `KindDate` so the existing constants
      keep their values), and add row 7.5 to `Classify` — after every archive base
      row (category/tag/author/date) and after row 7 (the structure's post shape
      via `ParamsFromPath`+`Match`), before row 8's flat `/{slug}` fallback —
      firing only for `len(segs) >= 2`, setting `t.Kind = KindPage` and
      `t.Segments = slices.Clone(segs)` and returning; the classifier stays PURE
      and TOTAL and makes no claim the segments name a real page (Req 6.2). In
      `internal/routing/routing.go` add `Structure.PagePath(ancestry []string)`
      beside `CategoryPath` (`routing.go:537`) — a pure builder joining the
      root-first ancestry slugs with NO front and NO archive base, appending the
      structure's trailing slash, returning `""` for an empty ancestry — and add
      the one-line page guard at the top of `Structure.Canonical` (`routing.go:408`):
      `if p.Type == "page" { return s.PagePath(pageAncestryFor(p)) }`, leaving the
      post branch byte-for-byte unchanged (Req 4.2), where `pageAncestryFor(p)`
      reads the transient ancestry off `domain.Post` and degrades to
      `[]string{p.Slug}` when absent (Req 1.2, 4.5). Add the transient,
      non-persisted page-ancestry field to the `domain.Post` struct in
      `internal/domain/entities.go` (NOT `repository.go`), documented as a
      routing/transport convenience never written to storage and never part of the
      post reader/writer method set (Req 8.4).
      Files: `internal/routing/classify.go`, `internal/routing/routing.go`,
      `internal/domain/entities.go`
      Verify: `go test ./internal/routing -run PagePath` — the 1.1 tests pass;
      `go test ./internal/routing` green (existing classify/canonical vectors
      unchanged); `go build ./...` compiles; `go vet ./...` clean. _(Req 1.1, 1.2,
      1.3, 1.4, 4.1, 4.2, 4.5, 6.1, 6.2, 6.3, 6.4, 6.5, 9.3; D1, D4, D5)_

## Phase 2 — Storage: `domain.PageReader` + `PostRepo.ListPages` + `Set` wiring (`internal/domain` + `internal/storage/wprepo` + `internal/storage/factory.go`, TDD, cross-vendor contract)

- [x] 2.1 Write the failing cross-vendor storage contract assertions first. In
      `internal/storage/storagetest/contract.go`, attach new assertions to
      `RunContract`'s body (run by `sqlite_test.go` always and
      `mysql_test.go`/`postgres_test.go` env-gated through the existing harness —
      no new env var), seeding a nested-page fixture in its own ID band mirroring
      the nested-category fixture pattern (`nested_fixtures.go`; add a sibling
      `nested_page_fixtures.go` if the term pattern uses a dedicated file).
      Assert for `PostRepo.ListPages`: it returns `page` rows ONLY — no `post`,
      `attachment`, or `nav_menu_item` row appears (Req 7.1, 7.2); each returned
      `domain.PageNode` carries `ID`, slug (`post_name`), `post_parent`, and
      `post_status` (Req 7.1); under a small `limit` it paginates by ascending-`ID`
      keyset returning every page exactly once with none dropped or duplicated —
      including a page mutated in place between pages still appearing exactly once
      (Req 7.3); and it INCLUDES unpublished pages (draft/pending/private), because
      the graph must climb THROUGH a non-published ancestor to reach a published
      leaf (Req 3.4 posture, design Finding 7 / D6).
      Files: `internal/storage/storagetest/contract.go` (plus a
      `nested_page_fixtures.go` if the term fixtures use a dedicated file)
      Verify: `go test ./internal/storage/storagetest -run SQLite` — fails
      (`domain.PageReader`, `PostRepo.ListPages`, `domain.PageNode`, and the
      `PageReader` `Set` field do not exist yet). _(Req 7.1, 7.2, 7.3, 7.4, 10.1;
      D6, D7)_

- [x] 2.2 Implement the `PageReader` port, `PageNode`, `PostRepo.ListPages`, and
      the `Set` wiring. Exactly per design: in `internal/domain/repository.go`,
      beside the M14 `PostContentBackfill`/`PostContentUpdater` ports
      (`:476`/`:492`), add the additive `PageReader` interface
      (`ListPages(ctx context.Context, afterID int64, limit int) ([]PageNode, error)`)
      and the lean `PageNode` projection struct (`ID int64` // ID, `Slug string`
      // post_name, `ParentID int64` // post_parent (0 == top-level),
      `Status string` // post_status), with the design's doc comments naming each
      requirement. In `internal/storage/wprepo/repo.go` implement `ListPages` as a
      new method on the existing `*PostRepo` (reusing its `db`/`prefix` fields, no
      new constructor): a projection `NewSelect` over `bun.Ident(prefix+"posts")`
      with `Column("ID","post_name","post_parent","post_status")`,
      `Where("post_type = ?", "page")`, an `ID > ?` keyset predicate via
      `bun.Ident("ID")`, `OrderExpr("? ASC", bun.Ident("ID"))`, and `Limit(limit)`
      — NO `post_status` filter (the graph must include unpublished ancestors,
      Finding 7) — plus the unexported `pageNodeRow` scan struct
      (`bun:"ID"`/`bun:"post_name"`/`bun:"post_parent"`/`bun:"post_status"`) and
      `toDomainPageNodes`, mirroring how `postContentRow` maps to
      `domain.PostContentRow` (`repo.go`). `post_parent`/`post_name` come straight
      from the existing `postColumns` (`repo.go:17-24`); no new column (Req 7.5,
      8.1). In `internal/storage/factory.go` add the additive `PageReader`
      `Set` field and assign it from the single `posts := wprepo.NewPostRepo(...)`
      already built in `New`, leaving the existing assignments undisturbed (Req
      7.4, 8.4).
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/repo.go`,
      `internal/storage/factory.go`
      Verify: `go test ./internal/storage/storagetest -run SQLite` — the 2.1
      assertions pass; `go build ./...` compiles; `go vet ./...` clean. _(Req 7.1,
      7.2, 7.3, 7.4, 7.5, 8.1, 8.4; D6)_

## Phase 3 — Content: `PageHierarchy` + `PageMovedError` + `PageService` (`internal/content`, TDD)

- [x] 3.1 Write the failing content unit tests first. Add
      `internal/content/pagehier_test.go` (new file, `package content`, beside
      `archive_test.go`) driving the page walk over a fake `domain.PageReader` (and
      a fake/real post reader for the published-leaf confirmation), seeding a
      parent→child→grandchild chain, a sibling, a shared-slug pair under different
      parents, an orphan-parented page, and a cycle. Assert, per the design's
      Testing strategy, mirroring the `TermHierarchy` tests:
      `PageHierarchy.Ancestry` — root-first self-inclusive slug path (Req 2.2); an
      orphan parent (`post_parent` absent from the graph) terminates yielding the
      shorter resolvable path (Req 2.3); a parent cycle terminates on the first
      repeated page (Req 2.4). `ResolvePath` — a good path resolves the leaf; two
      pages sharing a slug under different parents are each reachable at their own
      path (resolving by path, not final slug). `recoverPath`/`PageMovedError` — a
      flat or wrong-ancestor path returns a `*PageMovedError` carrying the
      canonical ancestry (Req 5.1, 5.2); a final segment naming nothing returns
      `domain.ErrNotFound` (Req 3.3); a cycle-trapped page's equal-ancestry
      recovery returns `ErrNotFound` rather than 301ing to itself.
      `PageService.Resolve`/`AncestryFor` — a published leaf confirmed via
      `BySlug`/`PublishedByID` resolves (Req 3.2, 3.4); a draft leaf present in the
      graph resolves to not-found (Req 3.4).
      Files: `internal/content/pagehier_test.go`
      Verify: `go test ./internal/content -run PageHier` — fails (`PageHierarchy`,
      `LoadPageHierarchy`, `PageMovedError`, `PageService` absent). _(Req 2.2, 2.3,
      2.4, 2.5, 3.2, 3.3, 3.4, 3.5, 5.1, 5.2; D2, D3)_

- [x] 3.2 Implement `PageHierarchy`, `PageMovedError`, and `PageService`. Add
      `internal/content/pagehier.go` (new file, `package content`, so `archive.go`
      stays term-focused), exactly per design: `PageHierarchy{byID
      map[int64]domain.PageNode; children map[int64][]int64; roots []int64}`;
      `LoadPageHierarchy(ctx, r domain.PageReader)` reading every page row via one
      keyset-paginated `ListPages` walk (never per-level), indexing `byID`,
      ID-ordered `children`, and `roots` (`post_parent == 0` plus any page whose
      parent is absent — the orphan-as-root rule), mirroring `LoadTermHierarchy`
      (`archive.go:94`); `Ancestry(id)` returning the root-first self-inclusive
      slug path with the SAME visited-set degradation as
      `TermHierarchy.Ancestry` (`archive.go:251`) — orphan parent → shorter path,
      cycle → first-repeat terminate; `ResolvePath(segments)` walking from roots
      matching each segment's slug at the current level (`archive.go:149`);
      `recoverPath(segments)` returning a `*PageMovedError` with the final
      segment's canonical ancestry or `domain.ErrNotFound` (`archive.go:167`),
      including the equal-ancestry cycle guard that 404s rather than self-301ing.
      Add `PageMovedError{Page domain.PageNode; Ancestry []string}` beside the
      `CategoryMovedError` analogue (`archive.go:49`), NOT wrapping
      `domain.ErrNotFound`. Add `PageService{pages domain.PageReader; posts
      domain.PostRepository}` with `Resolve(ctx, segments) (domain.Post, []string,
      error)` (load graph once, `ResolvePath`, on `!ok` return
      `recoverPath(segments)`, else `h.Ancestry(leaf.ID)`; confirm the leaf
      published via `posts.BySlug`/`PublishedByID`, Req 3.4) and `AncestryFor(ctx,
      id) ([]string, error)` (the single place `single()` and the REST mapper
      obtain the nested canonical, Req 3.5, 4.4, 5.1).
      Files: `internal/content/pagehier.go`
      Verify: `go test ./internal/content -run PageHier` — the 3.1 tests pass;
      `go test ./internal/content` green; `go build ./...` compiles;
      `go vet ./...` clean. _(Req 2.1, 2.2, 2.3, 2.4, 2.5, 3.1, 3.2, 3.3, 3.4, 3.5,
      4.3, 5.1, 5.2; D2, D3, D4)_

## Phase 4 — Web handlers: `resolve` `KindPage` arm + `page()` + `single()` page branch (`internal/web`, TDD)

- [x] 4.1 Write the failing handler tests first. Add
      `internal/web/pages_nested_test.go` driving the live chi router with a real
      `content.PageService` over SQLite (the existing handler harness). Assert, per
      the design's Testing strategy: a nested page's canonical path renders 200 (a
      fixed point, no redirect, Req 3.5, 5.4); the flat `/{leaf}` form of a nested
      page 301s (permanent) to the nested canonical (Req 5.1); the old dated form
      (e.g. `/2024/05/17/about/`) 301s to the nested canonical (Req 5.2); each
      redirect asserts the request's query string is preserved RAW across the 301
      (`redirectCanonical`, `handlers.go:137`, Req 5.3); an orphan-parent page and
      a cycle page still resolve to a servable canonical rather than erroring or
      looping (Req 10.3); and a multi-segment path that names no page → 404
      (Req 6.3 outcome via the walk). Also assert the page wildcard route is
      registered and dispatches through `s.resolve`, and that `single()` 301s a
      nested page's flat leaf.
      Files: `internal/web/pages_nested_test.go`
      Verify: `go test ./internal/web -run PagesNested` — fails (the `KindPage`
      arm, `page()`, the `single()` page branch, the page wildcard route, and the
      `s.pages` wiring are absent). _(Req 3.1, 3.2, 3.3, 3.5, 5.1, 5.2, 5.3, 5.4,
      5.5, 6.3, 9.1, 9.2, 9.4, 9.5, 10.2, 10.3; D5)_

- [x] 4.2 Implement the `resolve` arm, `page()`, the `single()` page branch, the
      wildcard route, and the startup guard. Exactly per design: in
      `internal/web/handlers.go` add the `case routing.KindPage: return
      s.page(w, r, t)` arm to `resolve` (`handlers.go:65`); add
      `page(w, r, t)` mirroring `categoryArchive` (`handlers.go:300`) —
      `s.pages.Resolve(r.Context(), t.Segments)`, `errors.As(err, &moved)` for a
      `*content.PageMovedError` → `s.redirectCanonical(w, r,
      s.permalinks.PagePath(moved.Ancestry))`, else build
      `canonical := s.permalinks.PagePath(ancestry)` from the RESOLVED leaf's own
      ancestry and 301 via `redirectCanonical` when `r.URL.Path != canonical` else
      `renderSingle` (Req 3.5, 5.1-5.5); add the `single()` page branch
      (`handlers.go:103`) so `post.Type == "page"` → `s.pages.AncestryFor(ctx,
      post.ID)` → `canonical := s.permalinks.PagePath(ancestry)` → 301-or-render,
      leaving the unchanged post path through `s.permalinks.Canonical(post)` (Req
      5.1, 5.4). In `internal/web/router.go` register the page wildcard from
      `contentPatterns` pointed at `s.resolve` (`router.go:225-277`) and extend the
      `requireArchiveDeps`-style guard with an `s.pages == nil` startup panic
      naming the `WithPages(repos.PageReader)` fix. Add the `Server.pages` field
      and a `WithPages`-style wiring option (in the server-wiring file where the
      `Server` struct and options live — e.g. `server.go` — if distinct from
      `router.go`).
      Files: `internal/web/handlers.go`, `internal/web/router.go` (plus the
      server-wiring file, e.g. `internal/web/server.go`, if the `Server` struct and
      options live there)
      Verify: `go test ./internal/web -run PagesNested` — the 4.1 tests pass;
      `go test ./internal/web` green (the full handler suite); `go build ./...`
      compiles; `go vet ./...` clean. _(Req 3.1, 3.2, 3.3, 3.5, 5.1, 5.2, 5.3, 5.4,
      5.5, 9.1, 9.2, 9.3, 9.4, 9.5; D5)_

## Phase 5 — REST: page `link` via `Canonical` + `WithPages` reader (`internal/content`, TDD)

- [x] 5.1 Write the failing REST link test first. Add
      `internal/content/rest_page_link_test.go` (new file, `package content`, or
      extend the existing `rest_permalink_test.go` REST link harness) asserting,
      per the design's Testing strategy: a nested page's REST `link` field reports
      the nested canonical (via the mapper's page-hierarchy reader populating the
      transient ancestry before `postLink` calls `Canonical`, Req 4.4); a top-level
      page's `link` is `/{slug}` (Req 4.5); and a `post`'s `link` is unchanged
      (Req 4.2).
      Files: `internal/content/rest_page_link_test.go`
      Verify: `go test ./internal/content -run RESTPageLink` — fails (the mapper's
      `WithPages` reader and the page-ancestry population are absent). _(Req 4.2,
      4.4, 4.5, 8.5; D4)_

- [x] 5.2 Implement the `WithPages` mapper reader and page-ancestry population. In
      `internal/content/rest.go`, exactly per design: give `RESTMapper` a
      page-hierarchy reader via a `WithPages` option mirroring
      `WithPermalinks`/`WithUploadsBaseURL` (`rest.go:205-227`), and populate a
      page row's transient ancestry before `postLink` (`rest.go:234`) delegates to
      `m.permalinks.Canonical(p)`, so a page's `link` is the nested canonical and a
      top-level page's is `/{slug}`. A `post`'s `postLink` path is unchanged, and
      no second page-URL builder is introduced — the link is still constructed at
      the single `Canonical` site (Req 4.3, 8.5).
      Files: `internal/content/rest.go`
      Verify: `go test ./internal/content -run RESTPageLink` — the 5.1 test passes;
      `go test ./internal/content` green; `go build ./...` compiles;
      `go vet ./...` clean. _(Req 4.3, 4.4, 4.5, 8.5; D4)_

## Phase 6 — Real-WordPress-DB fixture + final gate

- [x] 6.1 Write the real-WP-DB nested-page fixture test. Add
      `TestRealWordPressNestedPages` to `internal/routing/realdb_test.go` (or a
      sibling `realdb_pages_test.go`), modeled on
      `TestRealWordPressNestedCategories`: read the restored WordPress database
      read-only, walk `post_parent` over the raw `page` rows to derive the expected
      nested page path INDEPENDENTLY of the implementation (the page analogue of
      `realWPAncestry`), round-trip each derived nested page path through
      `Classify`, and skip with a precise precondition message when the fixture has
      no nested page (the page analogue of `realWPNestedCensus`, so an all-top-level
      fixture skips rather than falsely passing).
      Files: `internal/routing/realdb_test.go` (or a sibling
      `internal/routing/realdb_pages_test.go`)
      Verify: `go test ./internal/routing -run RealWordPressNestedPages` — green,
      or skips cleanly with its precondition message when the fixture is absent;
      `go vet ./...` clean. _(Req 10.4; D7)_

- [x] 6.2 Final gate — verify the no-regression invariants and run the full suite.
      Confirm NO new file under `internal/storage/migrations`
      (`git status --porcelain internal/storage/migrations` shows none, Req 8.1,
      mirroring the sibling specs) and that `postColumns`
      (`internal/storage/wprepo/repo.go:17-24`) is unchanged — no new table or
      column, `post_parent`/`post_name` already exist; confirm NO new capability
      name (Req 8.2), NO second permalink construction site — `Canonical`/`PagePath`
      only (Req 4.3, 8.5) — and that the existing `PostRepo` reads
      (`BySlug`/`PublishedByID`) and `Canonical`-for-posts are unchanged in behavior
      (Req 4.2, 7.4, 8.4). Run `gofmt -l .` (empty), `go vet ./...`,
      `go build ./...`, `go test ./...` and fix anything red, confirming no test
      added by this milestone needs a new `GRIMOIRE_TEST_*` env var and no new
      dependency (pure Go, SQLite-only for the Go tests, Req 10.5).
      Files: (verification only) `internal/storage/migrations/`,
      `internal/storage/wprepo/repo.go`, `internal/routing/routing.go`
      Verify: all four gate commands green; `git status --porcelain
      internal/storage/migrations` shows no new files; no new `GRIMOIRE_TEST_*`
      env var and no new dependency. _(Req 8.1, 8.2, 8.3, 8.4, 8.5, 10.5; D6, D7)_

## Notes

- Each task cites the exact Requirement/AC, the Inherited decision (D1–D7), and
  (where it clarifies intent) the design Finding it satisfies, for traceability
  back to `requirements.md` and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt -l .` empty and `go vet
  ./...`, `go build ./...`, `go test ./...` green after every phase, matching
  M10a/M10.B/M10.C/M10.F/M11/M13/M14's close-out discipline (Req 10.5). No phase
  intentionally leaves the tree red: Phase 1's routing + `domain.Post` field edits
  land together so the package compiles once `KindPage`/`PagePath`/the guard
  exist, Phase 2's domain/wprepo/factory edits land together, and each later phase
  gate is a true green gate.
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the
  sub-task that writes its failing test (1.1→1.2 routing, 2.1→2.2 storage,
  3.1→3.2 content, 4.1→4.2 web, 5.1→5.2 REST; 6.1 is the real-DB fixture and 6.2
  the final gate). There is no pure-interface phase to except — the `PageReader`
  port and the `KindPage`/`PagePath`/guard behaviors in 1.2 and 2.2 are each the
  first behavior and each has its failing test in the preceding sub-task.
- **No property-based tests.** M15 is routing-and-walk logic over a small,
  enumerable case space — top-level page / nested page / flat-leaf-of-a-nested-page
  / dated-legacy-form / orphan parent / parent cycle / non-page multi-segment →
  404 — mirroring code M9b already proved, not a pure function over a large input
  space, so there is no meaningful "for all inputs X, P(X)" to generate against;
  the interesting cases are the branch boundaries, which the routing unit tests,
  the content walk tests, the cross-vendor `ListPages` contract, the handler
  200/301/degradation tests, and the real-DB nested-page fixture cover
  exhaustively. Coverage is example + contract + fixture tests per the design's
  Testing strategy — pure Go, SQLite-only, no new env var or external dependency —
  matching the no-PBT decision in M10.F/M11/M13/M14.
- **No sub-task is optional** (none is postfixed with `*`). The routing
  classify/canonical vectors, the cross-vendor `ListPages` contract, the content
  ancestry/resolve/recover + published-leaf matrix, the handler
  200/301/query-preserved/degradation coverage, the REST page-link round-trip, the
  real-WordPress-DB nested-page fixture, and the final no-regression gate are the
  milestone's done-definition for nested page routing (Req 10); each is a core
  task, matching M10.F/M11/M13/M14, whose tests were likewise unmarked.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2"] },
    { "id": 2, "tasks": ["2.1"] },
    { "id": 3, "tasks": ["2.2"] },
    { "id": 4, "tasks": ["3.1"] },
    { "id": 5, "tasks": ["3.2"] },
    { "id": 6, "tasks": ["4.1"] },
    { "id": 7, "tasks": ["4.2"] },
    { "id": 8, "tasks": ["5.1"] },
    { "id": 9, "tasks": ["5.2"] },
    { "id": 10, "tasks": ["6.1"] },
    { "id": 11, "tasks": ["6.2"] }
  ]
}
```
