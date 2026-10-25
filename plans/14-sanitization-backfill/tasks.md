# Implementation Plan: M14 — pre-M10a content sanitization backfill

## Overview

> **This milestone drains the pool M10a left behind** — the
> `post_content`/`post_excerpt` stored *before* M10a's write boundary, imported
> from an adopted WordPress database or written by the M4–M9 paths that predate
> the sanitizer, never retroactively filtered
> (`../post-m10-gap-analysis/README.md:68-73`). M10a's `sanitize` policy is "the
> single place that decides what markup may be persisted"
> (`internal/sanitize/sanitize.go:1-14`) and `Policy.SanitizeAt`
> (`internal/sanitize/policy.go:121`) applies a tier passed as an argument, not
> derived from any writer — a ready-made, unconditional, fail-safe sanitizer M14
> simply calls. M14 is the opt-in CLI sweep that reads every targeted
> `{prefix}posts` row, applies tier B (`$allowedposttags`, the tier M10a applies
> to a non-`unfiltered_html` writer's post content,
> `internal/sanitize/sanitize.go:69-77`) to both content fields, and rewrites only
> the rows whose sanitized value differs. It closes exactly that gap and nothing
> more: no new route, no new capability, no schema/migration.
>
> **What this adds — and only this.** One `grimoire-cli` subcommand
> (`backfill-content`, dispatched from `main()`'s switch alongside
> `migrate`/`seed`/`createadmin`/`sessions`, `cmd/grimoire-cli/main.go:30-55`,
> dry-run by default, writes only under an explicit `-apply` flag). Two additive
> domain ports implemented as new methods on the single existing `*PostRepo`
> (`internal/storage/wprepo/repo.go:75-80`): `PostContentBackfill.BackfillPage`
> — a projection SELECT with an `ID > ?` keyset predicate, a `post_type IN (…)`
> over `{post, page, revision}`, NO `post_status` filter, `ORDER BY ID`, and a
> `LIMIT`, returning only `ID`/`post_type`/`post_content`/`post_excerpt`; and
> `PostContentUpdater.UpdateContentFields` — a two-column `NewUpdate` keyed on the
> quoted primary key with `errNotFoundIfZero`, deliberately NOT `PostWriter.Update`
> (`internal/domain/repository.go:447`, `internal/storage/wprepo/writers.go:63-104`)
> which replaces the whole row and bumps `post_modified`/`post_modified_gmt`. Two
> new additive `Set` fields in `internal/storage/factory.go` wire both ports from
> the single `posts := wprepo.NewPostRepo(…)` instance. One orchestration helper
> (`runBackfill`) plus a report writer (`printReport`) in the `cmd/grimoire-cli`
> package. M14 reuses M10a's `sanitize` policy unchanged — it adds no policy code.
>
> **No migration, no table, no column, no web surface, no capability.**
> `post_content`/`post_excerpt` already exist in `postColumns`
> (`internal/storage/wprepo/repo.go:17-24`); the two new ports are additive to
> `domain` and `wprepo` and alter no existing `PostRepo` read or `PostWriter`
> behavior (Req 8.4); the sweep has no acting principal and makes no authorization
> decision (Req 8.3). The report is the whole observability story: because M14
> does not bump `post_modified` (D4) and offers no undo (D5), a per-row line (by
> `ID`, `post_type`, which field(s), a byte-length indicator) plus totals is the
> audit trail, with no full post bodies dumped to stdout (Req 7).
>
> **Standing gate convention.** Keep `gofmt -l .` empty and `go vet ./...`,
> `go build ./...`, `go test ./...` green after every phase, following the
> close-out discipline of M10a/M10.B/M10.C/M10.F/M11/M13.
>
> **Strict-TDD spine.** Every behavior-adding sub-task is preceded by the
> sub-task that writes its failing test (1.1→1.2, 2.1→2.2, 3.1→3.2), matching how
> M5–M13 were run. The storage ports, the orchestrator, and the subcommand live in
> largely different files — the cross-vendor contract in
> `internal/storage/storagetest/contract.go`, the orchestration unit tests and
> `runBackfill`/`printReport` in `cmd/grimoire-cli/backfill.go`, the command-level
> test in a sibling `backfill_cmd_test.go`, and the subcommand arm in
> `cmd/grimoire-cli/main.go` — so the test and impl tasks pair across consecutive
> waves without two tasks in one wave ever editing the same file.

## Tasks

## Phase 1 — Storage: the two additive ports on `*PostRepo` (`internal/domain` + `internal/storage/wprepo` + `internal/storage/factory.go`, TDD, cross-vendor contract)

- [x] 1.1 Write the failing cross-vendor storage contract assertions first. In
      `internal/storage/storagetest/contract.go`, attach new assertions to
      `RunContract`'s body (run by `sqlite_test.go` always and
      `mysql_test.go`/`postgres_test.go` env-gated through the existing harness —
      no new env var), seeding script-bearing and clean rows via the extra-seed
      hook (`newSQLiteRepos(t, extraSeeds…)`, `sqlite_test.go:13-40`). Assert for
      `PostContentBackfill.BackfillPage`: it returns every `{post, page, revision}`
      row across ALL statuses (publish/draft/pending/private/future/inherit/trash)
      and returns NO `attachment`/`nav_menu_item` row (Req 3.1, 3.2); under a small
      `limit` it paginates by ascending-`ID` keyset returning every targeted row
      exactly once with none skipped or duplicated — including a row mutated in
      place between pages still appearing exactly once (Req 3.4); and it projects
      only `ID`/`post_type`/`post_content`/`post_excerpt` (Req 3.5). Assert for
      `PostContentUpdater.UpdateContentFields`: a row whose `post_content` holds a
      disallowed `<script>` is rewritten to its tier-B value, `post_excerpt` is
      written, `post_modified`/`post_modified_gmt` are byte-identical to their
      pre-update values and NO other column is touched (Req 4.2-4.4, 10.2); an
      unknown `ID` returns `domain.ErrNotFound`; and a `BackfillPage`-only pass
      (no update call) leaves every row byte-identical while an already-clean row
      passed its own values is a no-op in content (Req 10.3).
      Files: `internal/storage/storagetest/contract.go`
      Verify: `go test ./internal/storage/storagetest -run SQLite` — fails
      (`BackfillPage`/`UpdateContentFields` and the two `Set` fields do not exist
      yet). _(Req 3.1, 3.2, 3.4, 3.5, 4.2, 4.3, 4.4, 10.1, 10.2, 10.3; D3, D4, D7)_

- [x] 1.2 Implement the two additive ports, their `*PostRepo` methods, and the
      `Set` wiring. In `internal/domain/repository.go`, exactly per design, add
      the two additive interfaces beside the existing post ports —
      `PostContentBackfill` with
      `BackfillPage(ctx context.Context, afterID int64, limit int, types []string) ([]PostContentRow, error)`
      and `PostContentUpdater` with
      `UpdateContentFields(ctx context.Context, id int64, content, excerpt string) error`
      — plus the minimal `PostContentRow` projection struct
      (`ID int64`, `Type string`, `Content string`, `Excerpt string`), with the
      design's doc comments naming each requirement. In
      `internal/storage/wprepo/repo.go`, implement both as new methods on the
      existing `*PostRepo` (reusing its `db`/`prefix` fields, no new constructor):
      `BackfillPage` a projection SELECT (`Column("ID","post_type",
      "post_content","post_excerpt")`) with an `ID > ?` keyset predicate
      (`bun.Ident("ID")`), `post_type IN (?)` via `bun.In(types)`, NO
      `post_status` filter, `OrderExpr("? ASC", bun.Ident("ID"))`, and `Limit`;
      `UpdateContentFields` a two-`Set` `NewUpdate` (`post_content`,
      `post_excerpt` only) keyed on `bun.Ident("ID")` returning
      `errNotFoundIfZero(res)` — NO `post_modified`/`post_modified_gmt` bump,
      mirroring `PostWriter.Delete`'s existence posture (`writers.go:100`, `:113`).
      Add the unexported `postContentRow` scan struct (`bun:"ID"`/`bun:"post_type"`/
      `bun:"post_content"`/`bun:"post_excerpt"` tags) and `toDomainContentRows`,
      mirroring how `postRow` maps to `domain.Post` (`repo.go:46-73`). In
      `internal/storage/factory.go`, add the two additive `Set` fields
      (`PostContentBackfill`, `PostContentUpdater`) and assign both from the single
      `posts := wprepo.NewPostRepo(bunDB, prefix)` already built in `New`, leaving
      the existing five assignments (`PostWriter`/`AdminPosts`/`PostCounter`/
      `Revisions`/`Scheduled`) undisturbed.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/repo.go`,
      `internal/storage/factory.go`
      Verify: `go test ./internal/storage/storagetest -run SQLite` — the 1.1
      assertions pass; `go build ./...` compiles; `go vet ./...` clean.
      _(Req 3.1, 3.2, 3.3, 3.4, 3.5, 4.2, 4.3, 4.4, 8.2, 8.4; D3, D4, D6)_

## Phase 2 — cmd orchestration: `runBackfill` sweep + report (`cmd/grimoire-cli`, TDD over fake ports)

- [x] 2.1 Write the failing orchestration unit tests first. Add
      `cmd/grimoire-cli/backfill_test.go` (new file, `package main`) driving
      `runBackfill` over fake `domain.PostContentBackfill`/
      `domain.PostContentUpdater` implementations and a real `sanitize.New()`
      policy (or a fake policy for the error case), table-driven, each case
      asserting the `Report` counts and whether the writer was called. Assert:
      a clean row (both fields already tier-B fixed points) → `Scanned++`, no row
      result appended, the writer NOT called (Req 4.1, 5.1); content-only /
      excerpt-only / both-changed rows → the correct `ContentChanges`/
      `ExcerptChanges`/`Changed` totals and field markers, and (apply) the writer
      called with BOTH clean values (Req 4.2, 4.5); a dry run (`apply=false`)
      records the changed rows in the `Report` but NEVER calls the writer (Req 6.1,
      6.2); dry-run and apply over the same seeded fake name the identical
      rows/fields (Req 6.3); a `SanitizeAt` error row → counted in `Failed`, named
      by `ID`/field, the writer not called for it, and the walk continues to the
      next row (Req 9.1-9.3); and a second apply over a seeded fake is a no-op
      (`Changed == 0`, both per-field counts `0`), with a dry run after an apply
      reporting zero (Req 5.1-5.3).
      Files: `cmd/grimoire-cli/backfill_test.go`
      Verify: `go test ./cmd/grimoire-cli -run Backfill` — fails (`runBackfill`,
      `Report`, `printReport` absent). _(Req 4.1, 4.2, 4.5, 5.1, 5.2, 5.3, 6.1,
      6.2, 6.3, 9.1, 9.2, 9.3; D1, D2)_

- [x] 2.2 Implement the orchestration helper, report structs, and report writer.
      Add `cmd/grimoire-cli/backfill.go` (new file, `package main`), exactly per
      design:
      `runBackfill(ctx context.Context, reader domain.PostContentBackfill, writer domain.PostContentUpdater, policy *sanitize.Policy, apply bool) (Report, error)`
      — a page walk under an `afterID int64` keyset cursor advanced to every row's
      `ID`, `const pageSize = 500`, `types := []string{"post","page","revision"}`
      (D3 / Req 3.1, excludes `attachment`/`nav_menu_item`); per row
      `SanitizeAt(sanitize.PostContent, sanitize.TierB, row.Content)` and
      `SanitizeAt(sanitize.PostExcerpt, sanitize.TierB, row.Excerpt)`; a
      `SanitizeAt` error marks the row failed (`recordFailed`) and the loop
      continues (Req 9.1-9.3); both-fixed-point rows are skipped with no write
      (Req 4.1, 5.1); a changed row is recorded (`recordChanged`) and, under
      `apply`, written via `writer.UpdateContentFields` with BOTH clean values
      (Req 4.2-4.5); a `BackfillPage`/`UpdateContentFields` error aborts the run
      with the error (distinct from a per-row sanitize error). Add the `Report`
      and `backfillRowResult` structs (lengths and a field marker only, NO bodies,
      Req 7.1, 7.2), the `recordChanged`/`recordFailed` methods, and
      `printReport(w io.Writer, rep Report, apply bool)` emitting a per-row line
      (`ID`, `post_type`, changed field(s), before/after byte lengths) and a totals
      block (scanned/changed/per-field/failed), differing from dry-run only in a
      header verb (Req 5, 7).
      Files: `cmd/grimoire-cli/backfill.go`
      Verify: `go test ./cmd/grimoire-cli -run Backfill` — the 2.1 tests pass;
      `go build ./...` compiles; `go vet ./...` clean. _(Req 2.1, 2.2, 2.3, 2.4,
      4.1, 4.2, 4.3, 4.4, 4.5, 5.1, 5.2, 5.3, 6.1, 6.2, 6.3, 7.1, 7.2, 7.3, 9.1,
      9.2, 9.3; D2, D4, D5)_

## Phase 3 — cmd subcommand: `backfill-content` wiring + command-level test (`cmd/grimoire-cli`, TDD)

- [x] 3.1 Write the failing command-level test first. Add
      `cmd/grimoire-cli/backfill_cmd_test.go` (new file, `package main`, a sibling
      to 2.1's file so the two can run in different waves without a shared-file
      collision) driving `runBackfill` (and, where the signature allows,
      `runBackfillContent`) over a SQLite-backed `storage.Set` (migrated via
      `MigrationsFS` + `migrate.Apply`, seeded with some rows carrying a
      disallowed `<script>` and some already clean). Assert: a dry run
      (`apply=false`) leaves the database byte-identical afterward and the report
      names the dirty rows (Req 6.1, 6.2); an apply (`apply=true`) cleans the dirty
      rows, leaves the clean rows untouched, and leaves `post_modified` unbumped on
      the cleaned rows (Req 4.4, 10.4); the report counts (`Scanned`, `Changed`,
      `ContentChanges`, `ExcerptChanges`, `Failed`) match the seeded fixture
      (Req 10.4); and a seeded sanitize-failure row yields a non-zero outcome from
      `runBackfillContent` under apply (the `report.Failed > 0` return, Req 9.4)
      while the same under a dry run reports the failure but returns nil.
      Files: `cmd/grimoire-cli/backfill_cmd_test.go`
      Verify: `go test ./cmd/grimoire-cli -run BackfillContent` — fails
      (`runBackfillContent` and the `main()` switch arm absent). _(Req 1.2, 1.3,
      4.4, 6.1, 6.2, 9.4, 10.4; D1, D5)_

- [x] 3.2 Implement the subcommand and wire it into `main()`. In
      `cmd/grimoire-cli/backfill.go` add
      `runBackfillContent(args []string) error`, exactly per design: its own
      `flag.NewFlagSet("backfill-content", flag.ExitOnError)` with a `-config`
      default string and an `-apply` bool defaulting `false` whose help states
      "TAKE A DATABASE BACKUP before running with -apply: M14 provides no undo"
      (Req 1.5); `config.Load(*cfgPath)` then `storage.New(cfg.Database)` with
      `defer repos.Close()`, each failure returning a non-nil error having written
      nothing (Req 1.6); one `sanitize.New()` policy (Req 2.1); a single
      `runBackfill(context.Background(), repos.PostContentBackfill,
      repos.PostContentUpdater, policy, *apply)` call; `printReport(os.Stdout,
      report, *apply)`; and a non-zero return
      (`fmt.Errorf("backfill-content: %d row(s) failed sanitization", …)`) only
      when `*apply && report.Failed > 0` so a failure is not lost in the report,
      a dry run that merely observed failures exiting 0 (Req 9.4). In
      `cmd/grimoire-cli/main.go` add the `case "backfill-content":
      err = runBackfillContent(…)` arm to the `main()` switch (`main.go:36-48`)
      alongside the siblings, and add a `usage()` line naming the subcommand with
      the backup caveat (e.g. `backfill-content  sanitize stored post content
      (dry run unless -apply; back up the DB first)`).
      Files: `cmd/grimoire-cli/backfill.go`, `cmd/grimoire-cli/main.go`
      Verify: `go test ./cmd/grimoire-cli -run BackfillContent` — the 3.1 tests
      pass; `go test ./cmd/grimoire-cli` green; `go build ./...` compiles;
      `go vet ./...` clean. _(Req 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 2.1, 9.4; D1, D5)_

## Phase 4 — Final gate: no-regression invariants + full suite

- [x] 4.1 Verify the no-regression invariants and run the full suite. Confirm NO
      new file under `internal/storage/migrations`
      (`git status --porcelain internal/storage/migrations` shows none, Req 8.2,
      mirroring the sibling specs) and that `postColumns`
      (`internal/storage/wprepo/repo.go:17-24`) is unchanged — no new table or
      column, `post_content`/`post_excerpt` already exist; confirm NO new HTTP
      handler, render path, capability name, or `wp-json`/admin route was added
      (Req 8.1, 8.3), that the backfill is reachable only via the subcommand
      (Req 1.4), and that the existing `PostRepo` read methods and `PostWriter`
      are unchanged in behavior — the two new ports are purely additive (Req 8.4).
      Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`
      and fix anything red, confirming no test added by this milestone needs a
      database, network, live WordPress, or a new `GRIMOIRE_TEST_*` env var, and
      no new dependency (pure Go, SQLite-only for the Go tests, Req 10.5).
      Files: (verification only) `internal/storage/migrations/`,
      `internal/domain/repository.go`, `internal/storage/wprepo/repo.go`
      Verify: all four gate commands green; `git status --porcelain
      internal/storage/migrations` shows no new files; no new `GRIMOIRE_TEST_*`
      env var and no new dependency. _(Req 8.1, 8.2, 8.3, 8.4, 10.5; D6, D7)_

## Notes

- Each task cites the exact Requirement/AC, the Inherited decision (D1–D7), and
  (where it clarifies intent) the design Finding it satisfies, for traceability
  back to `requirements.md` and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt -l .` empty and `go vet
  ./...`, `go build ./...`, `go test ./...` green after every phase, matching
  M10a/M10.B/M10.C/M10.F/M11/M13's close-out discipline (Req 10.5). No phase
  intentionally leaves the tree red: Phase 1's domain/wprepo/factory edits land
  together so the package compiles once the two methods and `Set` fields exist,
  and each later phase gate is a true green gate.
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the
  sub-task that writes its failing test (1.1→1.2 for the storage ports, 2.1→2.2
  for the orchestrator, 3.1→3.2 for the subcommand). There is no pure-interface
  phase to except — the two new ports in 1.2 are the first behavior and their
  failing cross-vendor contract assertions are in 1.1.
- The orchestration unit tests (2.1) and the command-level test (3.1) live in
  separate files (`backfill_test.go` and `backfill_cmd_test.go`) so neither
  collides on a shared file; `runBackfill`/`printReport` (2.2) and
  `runBackfillContent` (3.2) both live in `backfill.go`, so 2.2 and 3.2 are
  sequenced into different waves by the TDD order.
- **No property-based tests.** The sweep is a fixed, enumerable set of branches —
  row clean / content-only changed / excerpt-only changed / both changed /
  `SanitizeAt`-error, dry-run vs `-apply`, an included type vs an excluded one,
  and the first page / a mid-walk cursor / the final empty page — plus a keyset
  walk and a two-column write; there is no meaningful "for all inputs X, P(X)"
  over a large input space to generate against, the interesting cases being the
  branch boundaries which the cross-vendor contract, the table-driven
  orchestration tests, and the command-level test cover exhaustively. M14 reuses
  M10a's already-tested `sanitize` policy unchanged. Coverage is example +
  contract + command tests per the design's Testing strategy — pure Go,
  SQLite-only, no new env var or external dependency — matching the no-PBT
  decision in M10.F/M11/M13.
- **No sub-task is optional** (none is postfixed with `*`). The cross-vendor
  port contract, the `runBackfill` branch/idempotence/dry-run matrix, the
  command-level dry-run-vs-apply + report-counts + exit-posture coverage, and the
  final no-regression gate are the milestone's done-definition for the backfill
  (Req 10); each is a core task, matching M10.F/M11/M13, whose tests were likewise
  unmarked.

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
    { "id": 6, "tasks": ["4.1"] }
  ]
}
```
