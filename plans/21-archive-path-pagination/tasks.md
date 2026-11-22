# Implementation Plan: M16 — Archive path-form pagination and the `?m=` date query
## Overview
> **This milestone adds WordPress's `/page/{n}/` form beside grimoire's `?page=N` on
> every paginated archive, and makes `?m=YYYYMM` resolve to a date archive.** It is
> roadmap task 16.A, started because the project owner explicitly asked for it
> (`requirements.md` Requirement 1, roadmap Requirement 33.1). No migration, no
> column, no capability, no port, no dependency, no new `GRIMOIRE_TEST_*` variable.
>
> **Two pull requests, each a complete increment, each with its own `buildHandler`
> test and a final gate task.**
>
> | PR | Phases | Increment |
> |---|---|---|
> | 1 (path-form pagination) | 1-5 | `Classify` peels a trailing `page/{n}` pair when the remainder is a paginatable archive; `PaginatedPath`, `PageLinks`, `PaginationPatterns`; `renderArchive` and the home handler serve and canonicalise the path form; `?page=N` and `?paged=N` stay `200` and gain a `Link` canonical header; the default theme renders handler-built links; real-database check; docs; gate |
> | 2 (`?m=`) | 6-8 | `ParseDateQuery` and `DateQuery`; `GET /?m=YYYY[MM[DD]]` serves the date archive in place; invalid values ignored; docs, roadmap tick, gate |
>
> PR 2 branches from `main` after PR 1 merges. PR 1 stands alone.
>
> **What the code check changed in the brief.** `/page/2` is already classified at
> `main` `a9110f4`: it is `KindPage` (M15) and 404s, so the pagination rule has to
> run before that row and needs its own chi patterns. The `/{slug}/*` page wildcard
> exists only when `WithPages` is wired, so path-form pagination cannot rely on it.
> The theme's `?page=` links are hand-built in three templates and cannot know the
> trailing-slash policy, so the handler supplies `PrevURL` and `NextURL`. The
> built-in goldens are REST and admin only and are not affected. No feed or sitemap
> route exists. `?page=` has an unbounded upper end that overflows the offset
> arithmetic; `MaxPageNumber` closes it (`design.md` Findings).
>
> **Strict-TDD spine.** Every behavior-adding implementation task is preceded by
> the task that writes its failing test, and that test is run and seen to fail
> **for the stated reason** before the implementation starts. A test that fails only
> because a new symbol does not compile counts as RED, and the implementation task
> makes it compile and pass. Documentation tasks, real-database tasks (gated, no RED
> possible), wiring-proof tasks and gates have no RED step and say so.
>
> **Wiring is tested where it is wired.** M15 once shipped with its wiring missing
> from the real binary. Tasks 3.2 and 7.1 write the `buildHandler` tests RED, before
> the implementation, and tasks 4.2 and 7.4 revert each wired change once and
> require the named test to fail. The result is recorded in the PR description.
>
> **Standing gate convention.** After every implementation task (a RED task leaves its own
> failing tests until the implementation task that follows): `gofmt -l .` empty, and
> `go vet ./...`, `go build ./...`, `go test ./...` green. `TestBuiltinGolden` runs
> in every gate and no golden file changes in either PR; a failure there is a stop,
> not an `-update`. `internal/render/testdata/golden/*.html` stay byte-identical.
>
> **Fixtures.** Assembly tests follow `cmd/grimoire/assembly_test.go`
> (`newAssemblySQLite`, `buildHandler(serverDeps{...})`, `routing.Parse`, the real
> `themes/` directory) and `assembly_custom_fixture_test.go` for a registry with a
> custom archive and a hierarchical taxonomy. Seed enough posts for three pages
> (`content.DefaultPerPage` is 10). Handler tests follow `internal/web/archives_test.go`
> and `archives_query_test.go`. Routing tests follow `archive_fixedpoint_test.go`
> and `archive_patterns_test.go`.
## Tasks
## Phase 1 — Routing: classifier wrapper, path constructors, patterns, notes (`internal/routing`, pure, TDD) — PR 1
- [x] 1.1 Write the failing routing tests first. Add `internal/routing/pagination_test.go`
      (`package routing_test`, pure, no DB) asserting, per `design.md`:
      (a) `ParsePageNumber`: accepts `1`, `2`, `10`, `999999999`; rejects `""`, `0`, `02`, `+2`,
      `-2`, `2a`, ` 2`, `1000000000`, `99999999999999999999`.
      (b) `Classify` over the **disambiguation matrix** of `design.md`, one subtest per row,
      across the structures of Requirement 13.1 (Flat, `/%postname%/`,
      `/%year%/%monthnum%/%postname%/`, `/blog/%year%/%monthnum%/%postname%/`,
      `/%postname%/%post_id%/`, `/%post_id%/`, with and without a trailing slash, a renamed
      `category_base`, a registry with a custom archive and a hierarchical custom taxonomy):
      kind, `Segments`/`Slug`/`Date`/`TypeName`, `Page` and `TrailingSlash`. `/` is still
      `KindNone`. A path with no `page/{n}` suffix has `Page == 0`.
      (c) The fixed-point loop: for every paginatable kind, `n` in {2, 3, 10, 999999999} and
      both slash forms, `Classify(PaginatedPath(base, n))` has the same kind and entity as
      `Classify(base)` with `Page == n`, and `PaginatedPath(Path(entity), Page)` rebuilds the
      request path.
      (d) `PaginatedPath` vectors: base `/category/news/` and `/category/news`, `/`, n of 0, 1
      (returns base), 2, with and without `TrailingSlash`; empty base returns `""`.
      (e) `PageLinks`: first page, page 2 (prev is the base, never `/page/1/`), a middle page,
      the last page, a single page, `totalPages == 0`, empty base.
      (f) `PaginationPatterns` as an exact, duplicate-free set, both slash forms, for each
      structure in (b), including no date patterns when Flat, custom archive patterns, and no
      category or custom-term patterns.
      (g) Notes: `category_base` or `tag_base` resolving to `page` adds one note naming the
      option; a custom entry slugged `page` adds one `CustomNotes()` entry; every other
      configuration's `Notes()` and `CustomNotes()` are byte-identical to the values the
      existing tests already pin.
      Files: `internal/routing/pagination_test.go`
      Verify: `go test ./internal/routing -count=1` fails to build for the missing symbols
      (`ParsePageNumber`, `PaginatedPath`, `PageLinks`, `PaginationPatterns`, `KindHome`,
      `Target.Page`, `MaxPageNumber`). Record that; the existing tests are untouched.
      _(Req 2, 3, 7.1-7.3, 9.1, 9.2, 9.6-9.8, 13)_
- [x] 1.2 Implement the routing changes. In `internal/routing/classify.go`: add
      `MaxPageNumber`, `KindHome` (appended after `KindCustomTerm`), `Target.Page`,
      `ParsePageNumber`; rename the current `Classify` body to `classifyPath` with its comments
      unchanged; add the new `Classify`, `splitPageSuffix` and `classifyPaginated` as in the design
      (accept the remainder only for `KindCategory`, `KindTag`, `KindAuthor`, `KindDate`,
      `KindCustomArchive`, `KindCustomTerm`; set `TrailingSlash` from the original path); add one
      sentence to `KindPage`'s doc comment. In `internal/routing/routing.go`: add
      `PaginatedPath`, `PageLinks` and `PaginationPatterns` using `archivePrefix`, `CustomBase`,
      `joinPattern`, `bothSlashForms` and `servesDateArchives`. In `internal/routing/notes.go`:
      add the `page` base note to `baseOptionNotes` and the custom-entry note to `shadowNotes`.
      No other behavior changes, and `ArchivePatterns` and `CustomPatterns` are not edited.
      Files: `internal/routing/classify.go`, `internal/routing/routing.go`,
      `internal/routing/notes.go`
      Verify: `go test ./internal/routing -count=1` passes, including every pre-existing test
      unmodified; `go vet ./internal/routing`. _(Req 2, 3, 7.1-7.3, 9.1, 9.2, 9.6-9.8, 12.6)_
## Phase 2 — Render: handler-built links in the default theme (`internal/render`, TDD) — PR 1
- [x] 2.1 Write the failing render tests first. Edit `internal/render/archive_template_test.go`
      and `internal/render/archive_data_test.go`: every case that today asserts
      `href="…?page=N"` from `BaseURL` instead sets `PrevURL` and `NextURL` on `ArchiveData` (and a
      new `IndexData` case) and asserts the real default theme renders those exact hrefs, renders
      no `Previous` link when `PrevURL` is empty and no `Next` link when `NextURL` is empty, and
      contains no `?page=` anywhere in the output. Keep one case where a custom inline template
      still builds `{{.BaseURL}}?page=…` from `BaseURL` and assert it still renders, which proves
      `BaseURL` is unchanged (Req 7.5). Say in the commit that these assertions are updated because
      the default theme no longer hand-builds `?page=` links.
      Files: `internal/render/archive_template_test.go`, `internal/render/archive_data_test.go`
      Verify: `go test ./internal/render -count=1` fails to build or fails on `PrevURL`/`NextURL`
      (missing fields) and on the missing links. Record that.
      _(Req 7.4, 7.5, 7.6)_
- [x] 2.2 Add `PrevURL, NextURL string` to `IndexData` and `ArchiveData` in
      `internal/render/view.go` (with a comment saying the handler builds them from
      `routing.Structure.PageLinks`). Replace the hand-built `?page=` pairs in
      `themes/default/templates/index.tmpl`, `category.tmpl` and `archive.tmpl` with the
      `{{if .PrevURL}}` and `{{if .NextURL}}` anchors of the design. Leave the `Page N of M`
      paragraph and the `{{if gt .Pagination.TotalPages 1}}` guard as they are.
      Files: `internal/render/view.go`, `themes/default/templates/index.tmpl`,
      `themes/default/templates/category.tmpl`, `themes/default/templates/archive.tmpl`
      Verify: `go test ./internal/render -count=1` passes, including `golden_test.go` with
      `internal/render/testdata/golden/*.html` byte-identical (`git diff --stat` on that folder is
      empty). _(Req 7.4-7.6)_
## Phase 3 — Web: handlers, router and the real binary (`internal/web`, `cmd/grimoire`, TDD) — PR 1
- [x] 3.1 Write the failing handler and router tests first. Add
      `internal/web/pagination_archives_test.go` and `internal/web/pagination_link_test.go`
      (`package web_test`), seeded SQLite, default theme, covering for the home page, a nested
      category, a tag, an author, a year, month and day archive, a custom post type archive and a
      custom taxonomy term (a registry fixture as in `router_custom_test.go`): path-form `200` with
      the right page of posts; page above the last `404`; page 1 `301` to the base with the raw
      query kept (`?utm=a%20b&x`); the other slash form `301` with the raw query kept; an unknown
      entity `404`; an empty archive `200` on page 2; a moved category and a moved custom term
      `301` to the new path **with** `/page/2/`; a category slugged `page`
      (`/category/page/` still a category, `/category/page/page/2/` is page 2 of it,
      `/category/page/2/` is the category walk `[page 2]`); a top-level page slugged `page`
      (`/page/` still renders it). Link header cases from the design's canonical-policy table:
      `?page=2`, `?paged=2`, `?page=1`, `?page=2&page=3`, path form with and without a query,
      and no header on a plain archive. `?page=` bound cases (Req 8): `?page=99999999999999999999`
      answers `404` on an archive with posts and does not panic, `?page=abc`, `?page=0`,
      `?page=-3` serve page 1, `?paged=2` serves page 2, `?page=3&paged=2` serves page 3. The
      rendered `Next` link in the body is the path form. Extend `internal/web/routes_regression_test.go`
      with the probe table of `design.md` Finding 2 for `/page/{pagenum}` against `/page`,
      `/page/x`, `/page/2/3` (with and without `WithPages`), the date patterns against a post slugged
      `page`, and `/{slug}/*`.
      Files: `internal/web/pagination_archives_test.go`, `internal/web/pagination_link_test.go`,
      `internal/web/routes_regression_test.go`
      Verify: `go test ./internal/web -count=1` fails: `404` where `200` is expected, page 1
      not redirected, no `Link` header. The 20-digit `?page=` case fails because `Atoi` rejects it and the handler serves page 1 with `200`.
      Record the failing subtests. Do not edit any pre-existing assertion except one that asserts a
      `?page=` href built by the default theme, and say so in the commit.
      _(Req 4, 5, 6, 7, 8, 9.2, 14.1, 14.2)_
- [x] 3.2 Write the failing assembly test first. Add `cmd/grimoire/assembly_pagination_test.go`
      driving `buildHandler` over migrated SQLite with the real `themes/` directory: structures
      `/%postname%/`, `/%year%/%monthnum%/%postname%/` with a trailing slash, and Flat; a registry
      with a custom archive and a hierarchical custom taxonomy (use `newCustomWriteHandler` or the
      fixture in `assembly_custom_fixture_test.go`). Seed 25 published posts across categories,
      a tag, an author and two months. Assert through the handler: every kind's `/page/2/` form
      answers `200`; `/page/1/` answers `301` with the base `Location`; `?page=2` answers `200`
      with a `Link` canonical header; the home page 1 body contains an `href` to `/page/2/` (or
      `/page/2` under Flat); page 2's `Previous` link is the base and not `/page/1/`; the
      `/category/x/page/3/` body has no `Next` link when it is the last page.
      Files: `cmd/grimoire/assembly_pagination_test.go`
      Verify: `go test ./cmd/grimoire -run TestBuildHandlerPagination -count=1` fails with `404`s
      on every path-form subtest. Record that. _(Req 14.3)_
- [x] 3.3 Implement the web changes. `internal/web/handlers.go`: `pageParam` reads `page`, then
      `paged`, treats garbage, zero and negatives as 1 and clamps any decimal integer above
      `routing.MaxPageNumber`, including one beyond `int64` (`strconv.ErrRange` on a positive value);
      add `pageFor`; change `home` to call `serveHome(w, r, t)` and add the `routing.KindHome` arm to
      `resolve`; `serveHome` redirects a non-canonical path-form request through
      `redirectCanonical` and otherwise renders `index` with `PrevURL` and `NextURL` from
      `PageLinks("/", …)`; `renderArchive` takes the target, compares `r.URL.Path` with
      `PaginatedPath(canonical, t.Page)`, applies the existing past-the-end rule with `pageFor`, and
      fills `BaseURL`, `PrevURL` and `NextURL`; add `setCanonicalLink` (relative path through
      `url.URL.EscapedPath`, only when the query has `page` or `paged`) and call it from
      `renderArchive` and `serveHome`; replace every `pageParam(r)` in an archive handler with
      `pageFor(r, t)`; pass `PaginatedPath(…, t.Page)` to the moved-category and moved-custom-term
      redirects. `internal/web/router.go`: append `st.PaginationPatterns()` in `contentPatterns`
      before `"/{slug}"` and update its comment. Nothing else changes, and no `WithX` option is
      added.
      Files: `internal/web/handlers.go`, `internal/web/router.go`
      Verify: `go test ./internal/web ./cmd/grimoire -count=1` passes: 3.1 and 3.2 green, the
      pre-existing suites green, `TestBuiltinGolden` green and unchanged. `go vet ./...`.
      _(Req 4-9, 14.1-14.3)_
## Phase 4 — Real-database check and wiring proof (`internal/routing`, `cmd/grimoire`) — PR 1
- [x] 4.1 Add the real-WordPress-database check. Create `internal/routing/realdb_pagination_test.go`
      as `TestRealWordPressPagination`, gated exactly as `TestRealWordPressNestedCategories` is
      (`GRIMOIRE_TEST_WP_DSN`, `GRIMOIRE_TEST_WP_PREFIX`, read-only, `storage.New` without
      migrating, skips when unset). Read `permalink_structure`, `category_base`, `tag_base`. From raw
      rows, by string operations and not through the constructors, derive: every category's
      ancestry path under the site's category base and front, every tag, every author nicename, the
      year and month of the 25 newest posts, and every page's nested path (reuse the helpers
      `realdb_pages_test.go` already uses). For each path assert `Classify` returns the kind and
      entity the existing real-database tests assert, with `Page == 0`; that `path + "/page/2"`
      classifies to the same kind and entity with `Page == 2`; and that `PaginatedPath` rebuilds it.
      Run the shadow census of Requirement 3.4 over the real rows (a category or term whose parent
      is slugged `page` and whose slug is a page number, a top-level page slugged `page` with a
      numeric child, a post whose canonical path is `/page/{n}`), log the counts, and fail with
      the URL list when any is non-zero. Add no environment variable.
      Files: `internal/routing/realdb_pagination_test.go`
      Verify: `go test ./internal/routing -run TestRealWordPressPagination -count=1 -v` skips
      cleanly without the DSN. With `GRIMOIRE_TEST_WP_DSN` set against the reference database it
      passes and logs the census counts; record the counts in the PR description, or record that no
      database was available. No RED step: the test is environment-gated and cannot fail before
      the implementation exists in a way CI can observe. _(Req 14.5, 14.6)_
- [x] 4.2 Prove the wiring. No new code. Revert each of these once, run
      `go test ./cmd/grimoire -run TestBuildHandlerPagination -count=1`, confirm the named
      failure, and restore: (a) remove the `st.PaginationPatterns()` append in `contentPatterns`
      (every path-form subtest fails with `404`); (b) make `Classify` call `classifyPath` directly
      (home and every archive path-form subtest fails); (c) restore the old `?page=` anchors in
      `themes/default/templates/index.tmpl` (the `Next` link subtest fails). Run
      `git diff --stat` to confirm everything is restored. Record the three results in the PR
      description.
      Files: (verification only)
      Verify: each revert fails the stated subtest, and the restored tree passes. _(Req 14.3, 14.4)_
## Phase 5 — Documentation and PR 1 gate — PR 1
- [x] 5.1 Update the documents to match PR 1. `docs/compatibility.md`: rewrite the M8 "Pagination
      is M8's contract, unchanged" bullet, the M9b "Pagination is `?page=N`, where WordPress uses
      `/page/{n}/`" bullet (remove it from the divergent list and describe the new rule), and the M12
      sentence "Archive pagination stays `?page=N`"; add "M16 — Archive pagination (PR 1)": the
      path form on home, category (nested), tag, author, date, custom archive and custom term; the
      peel rule and that `page` is not reserved; the three shadowed shapes of Requirement 3.4 and that
      there is no fallback; `?page=N` and `?paged=N` unchanged, `?page=` winning over `?paged=`, the
      `MaxPageNumber` clamp; the `Link` relative canonical header and why both forms answer `200`;
      `/page/1/` redirecting; Flat behavior; the notes for a `page` base; and that `?m=` is still not
      implemented until PR 2 (leave that sentence in place). `docs/user-guide.md`: replace the
      "Archive pagination is `?page=N`…" divergence bullet and document `PrevURL` and `NextURL` in the
      themes section. `plans/README.md`: add the row `21 | 21-archive-path-pagination | 🚧 Partially
      implemented (1 of 2 shipped)` with the PR 1 summary. `plans/post-m10-gap-analysis/README.md`:
      add a "Shipped in M16 (PR 1)" paragraph under the M16 section and narrow its gap statement to
      `?m=` and plain-permalink date archives. Leave roadmap `16.A` unticked. Run
      `grep -rn "?page=N\|/page/{n}" docs README.md plans` and fix each statement PR 1 makes stale,
      leaving `plans/09.1-*`, `plans/12-*` and the roadmap requirements untouched (they are
      historical).
      Files: `docs/compatibility.md`, `docs/user-guide.md`, `plans/README.md`,
      `plans/post-m10-gap-analysis/README.md`
      Verify: the grep shows no remaining claim that pagination is query-only; the plans README row
      reads partially implemented. _(Req 16.1, 16.3, 16.4, 16.6)_
- [x] 5.2 Final gate for PR 1. Confirm: no new file under `internal/storage/migrations`;
      `git diff main -- internal/domain` is empty (no port); `go.mod` and `go.sum` unchanged; no new
      `GRIMOIRE_TEST_*` variable (`git diff main | grep GRIMOIRE_TEST_` shows only existing names);
      `git diff main -- cmd/grimoire/testdata/builtin_golden internal/render/testdata/golden` is
      empty; `git diff main -- internal/routing/archive_patterns_test.go` is empty. Run `gofmt -l .`
      (empty), `go vet ./...`, `go build ./...`, `go test ./...`, and `go test -race ./internal/routing
      ./internal/render ./internal/web ./cmd/grimoire`. No storage code changed, so the MySQL and
      PostgreSQL container run of M18-M20 is not required; confirm CI is green on the PR before
      merging. Fix anything red in the phase that owns it. Ensure all tests pass, ask the user if
      questions arise.
      Files: (verification only)
      Verify: all gate commands green; the git checks show no violation. _(Req 12, 15.4, 15.5)_
## Phase 6 — Routing: the date query parser (`internal/routing`, pure, TDD) — PR 2
- [~] 6.1 Write the failing parser tests first. Add `internal/routing/datequery_test.go`
      (`package routing_test`): `ParseDateQuery` accepts `2024`, `202405`, `20240517`, `20240229`
      (leap day), `202402`; rejects `""`, `202`, `20245`, `2024051`, `202405170`, `202413`,
      `202400`, `20240230`, `20240500`, `20230229`, `+20240`, `2024 05`, ` 2024`, `2024-05`,
      `２０２４` (full-width digits), `2024\n`, `2024051712` (hour form), `0000`, which the test pins to whatever `DateRef.Range` decides; `DateQuery`
      renders each accepted value back to the same string (round trip) and a month-only `DateRef`
      to six digits.
      Files: `internal/routing/datequery_test.go`
      Verify: `go test ./internal/routing -count=1` fails to build for `ParseDateQuery` and
      `DateQuery`. Record that. _(Req 10.1, 10.2, 11.1, 13.6)_
- [~] 6.2 Implement `ParseDateQuery` and `DateQuery` in `internal/routing/classify.go` beside
      `dateFromSegments`, reusing `fixedWidthInt` and `DateRef.Range`. They read no database and
      return no error.
      Files: `internal/routing/classify.go`
      Verify: `go test ./internal/routing -count=1` passes. _(Req 10.1, 10.2, 11.1, 11.4)_
## Phase 7 — Web: `?m=` on the home route (`internal/web`, `cmd/grimoire`, TDD) — PR 2
- [~] 7.1 Write the failing handler and assembly tests first. Add `internal/web/datequery_test.go`
      and `cmd/grimoire/assembly_datequery_test.go`. Under `/%postname%/` (trailing slash and not)
      and under Flat, seeded with posts in May 2024 and elsewhere: `/?m=2024`, `/?m=202405`,
      `/?m=20240517` answer `200` with only that range's posts and the date heading; the structured
      response carries `Link: </2024/05/>; rel="canonical"` and its pagination links are the path
      form (`/2024/05/page/2/`); the Flat response has no `Link` and its `Next` link is
      `/?m=202405&page=2`; `/?m=202405&page=2` and `/?m=202405&paged=2` serve page 2; a page past the
      end `404`s; `/?m=garbage`, `/?m=202413`, `/?m=2024051712`, `/?m=` and `/?m=2024&m=x` (first value
      wins, valid) behave as stated, the invalid ones rendering the ordinary home page with `200`
      and the same body as `/`; `/page/2/?m=202405` renders home page 2 and ignores `m`;
      `/category/x/?m=202405` ignores `m`. Assert an invalid `m` makes no date-archive read (use the
      service fake or the query count the fixtures provide).
      Files: `internal/web/datequery_test.go`, `cmd/grimoire/assembly_datequery_test.go`
      Verify: `go test ./internal/web ./cmd/grimoire -run 'DateQuery' -count=1` fails: the valid
      `?m=` requests render the home page instead of the date archive. Record that.
      _(Req 10, 11, 14.3)_
- [~] 7.2 Implement the home branch. In `internal/web/handlers.go` add `serveDateQuery` and call it
      from `serveHome` when the target is the zero target and `ParseDateQuery` accepts the first
      `m` value; follow the design's steps 1-6 (structured: path-form links and an always-set
      `Link` header; Flat: `/?m=…&page=N` links, `BaseURL` `/?m=…`, no header). Keep the past-the-end
      rule. A rejected value falls through to the existing home render. Add a sentence to the
      `ArchiveData` doc comment in `internal/render/view.go` saying `BaseURL` is `/?m=…` for a Flat
      `?m=` archive and themes should use `PrevURL` and `NextURL`.
      Files: `internal/web/handlers.go`, `internal/render/view.go`
      Verify: `go test ./internal/web ./cmd/grimoire ./internal/render -count=1` passes; the
      pre-existing suites and `TestBuiltinGolden` stay green. _(Req 10, 11)_
- [~] 7.3 Extend the real-database check. In `internal/routing/realdb_pagination_test.go` (or a new
      `realdb_datequery_test.go` with the same gating) take the dates of the 25 newest real posts and,
      for each, assert `ParseDateQuery` accepts the year, year-month and year-month-day strings
      built by `fmt.Sprintf` from the row's stored date (no timezone shift), that the resulting
      `DateRef` equals the one `Classify` returns for the same date's path form, and that
      `DateQuery` rebuilds the string. Skips without the DSN; no new variable. No RED step (gated).
      Files: `internal/routing/realdb_pagination_test.go` or `internal/routing/realdb_datequery_test.go`
      Verify: skips cleanly without the DSN; with it, passes against the reference database, and the
      task records whether a database was available. _(Req 14.5, 14.6)_
- [~] 7.4 Prove the wiring. No new code. Revert once and run
      `go test ./cmd/grimoire -run 'TestBuildHandlerDateQuery' -count=1`: (a) remove the `serveDateQuery`
      call in `serveHome` (every valid-`m` subtest fails); (b) make `ParseDateQuery` return false
      (same subtests fail, invalid-`m` subtests still pass); restore and confirm with
      `git diff --stat`. Record both results in the PR description.
      Files: (verification only)
      Verify: each revert fails the stated subtests, and the restored tree passes. _(Req 14.3, 14.4)_
## Phase 8 — Documentation and PR 2 gate — PR 2
- [~] 8.1 Update the documents to match PR 2. `docs/compatibility.md`: replace "The `?m=`
      query-argument form is not implemented either; grimoire has no query-argument route" with the
      date-query description: accepted widths (4, 6, 8 digits), calendar rule, `GET /` only, served
      in place with `200`, the `Link` header on structured sites, plain-permalink pagination links,
      invalid values ignored so no URL that served before changes, the hour, minute and second forms
      not supported; update the "Date archives are not served under plain permalinks" bullet to point
      at `?m=`; add "M16 — Archive pagination (PR 2)". `docs/user-guide.md`: note `?m=`.
      `plans/README.md`: set the row to `✅ Implemented (2 of 2 shipped)` with the final summary.
      `plans/wordpress-core-parity-roadmap/tasks.md`: tick `16.A` with a shipped summary in the style
      of `15.A`. `plans/post-m10-gap-analysis/README.md`: remove the M16 section, mark ordering item 6
      shipped, add the "Shipped in M16 (PR 2)" paragraph. Record what stays open (Requirement 16.6).
      Run `grep -rn "?m=\|not implemented either" docs README.md plans` and fix each statement PR 2
      makes stale, leaving historical plans untouched.
      Files: `docs/compatibility.md`, `docs/user-guide.md`, `plans/README.md`,
      `plans/wordpress-core-parity-roadmap/tasks.md`, `plans/post-m10-gap-analysis/README.md`
      Verify: no document says `?m=` is unimplemented; the roadmap shows `16.A` ticked.
      _(Req 16.2, 16.4, 16.5, 16.6)_
- [~] 8.2 Final gate for PR 2. Confirm: no migration file, `git diff main -- internal/domain` empty,
      `go.mod` and `go.sum` unchanged, no new `GRIMOIRE_TEST_*` variable, `git diff main --
      cmd/grimoire/testdata/builtin_golden internal/render/testdata/golden` empty. Run `gofmt -l .`
      (empty), `go vet ./...`, `go build ./...`, `go test ./...`, and `go test -race ./internal/routing
      ./internal/render ./internal/web ./cmd/grimoire`. Confirm CI is green on the PR before merging.
      Fix anything red in the phase that owns it. Ensure all tests pass, ask the user if questions
      arise.
      Files: (verification only)
      Verify: all gate commands green; the git checks show no violation. _(Req 12, 15.4, 15.5)_
## Notes
- Tasks marked with `*` are optional; **no sub-task here is optional**. The disambiguation matrix,
  the fixed-point loop, the redirect and header tables, the assembly tests and the wiring proofs are
  the milestone's done-definition (Requirement 15).
- **No property-based tests.** The behavior is a finite classification table over named structures
  and enumerable redirect rules; see `design.md` Overview and Testing strategy.
- **TDD exceptions are explicit:** 5.2, 8.2 (gates), 4.1 and 7.3 (environment-gated real
  database), 4.2 and 7.4 (wiring proofs, no new code), 5.1 and 8.1 (docs).
- **Task 3.2 runs before 3.3 on purpose**, so the assembly test is seen RED for the right reason
  (`404` on every path-form request) before any handler changes.
- **Task 2.1 edits two existing test files.** The only assertions it changes are the ones that
  expect the default theme to build `?page=` links itself. If any other test asserts a `?page=` href
  from the theme, change only that assertion in the phase that owns the behavior and say so in the
  commit.
- **Golden files.** Neither PR changes them. If `TestBuiltinGolden` or `golden_test.go` fails, stop
  and find the cause; do not pass `-update`.
- **Shadowed URLs are documented, not fixed** (sign-off item 2). If the real-database census in task
  4.1 finds any, stop and report them before PR 1 merges.
- **Open sign-off items** are listed in `design.md` "Decisions awaiting sign-off". Defaults are
  implemented as written; reversing one is local to the task named in each item.
- **Not done here, recorded as gaps:** feeds and sitemaps (none exist), comment pagination,
  `<!--nextpage-->`, the hour, minute and second forms of `m`, and `?m=` on paths other than `/`.
## Task Dependency Graph
```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2", "2.1"] },
    { "id": 2, "tasks": ["2.2"] },
    { "id": 3, "tasks": ["3.1", "3.2"] },
    { "id": 4, "tasks": ["3.3"] },
    { "id": 5, "tasks": ["4.1"] },
    { "id": 6, "tasks": ["4.2"] },
    { "id": 7, "tasks": ["5.1"] },
    { "id": 8, "tasks": ["5.2"] },
    { "id": 9, "tasks": ["6.1"] },
    { "id": 10, "tasks": ["6.2"] },
    { "id": 11, "tasks": ["7.1"] },
    { "id": 12, "tasks": ["7.2"] },
    { "id": 13, "tasks": ["7.3"] },
    { "id": 14, "tasks": ["7.4"] },
    { "id": 15, "tasks": ["8.1"] },
    { "id": 16, "tasks": ["8.2"] }
  ]
}
```
