# Requirements Document

M14 — pre-M10a content sanitization backfill

## Introduction

M10a closed grimoire's write boundary: the `sanitize` package decides what
markup may be persisted, and every content value *written* from M10a onward is
filtered at the writer's tier before it reaches the database
(`internal/sanitize/sanitize.go:1-14`, "the single place that decides what
markup may be persisted"; `internal/sanitize/policy.go:121` `SanitizeAt`). But
M10a "closes the inflow; it does not drain the pool"
(`../post-m10-gap-analysis/README.md:68`): `post_content`/`post_excerpt` stored
*before* M10a — imported from an adopted WordPress database, or written by the
M4–M9 content paths that predate the write boundary — is never retroactively
sanitized. A pre-M10a stored `<script>` in a post body is inert *for comments*
(M10a's render backstop filters at display for the comment tier) but remains
*present in post bodies*: it was never passed through the post-tier policy on
write and nothing has rewritten it since
(`../post-m10-gap-analysis/README.md:68-73`).

M14 is the opt-in CLI sweep that drains that pool. It adds a new `grimoire-cli`
subcommand that mirrors the existing subcommand pattern in `main()` —
`migrate`/`seed`/`createadmin`/`sessions`, each dispatched from the top-level
switch, each parsing its own `flag.NewFlagSet` with `-config`, each loading
`config.Load(-config)` and opening storage
(`cmd/grimoire-cli/main.go:30-55`, `:76-93`, `:200-220`) — and walks every
`{prefix}posts` row, applying M10a's tier-B policy to `post_content` and
`post_excerpt` via `SanitizeAt(sanitize.PostContent, sanitize.TierB, …)` and
`SanitizeAt(sanitize.PostExcerpt, sanitize.TierB, …)`
(`internal/sanitize/policy.go:121-148`; `internal/sanitize/sanitize.go:17-33`).
Tier B is the broad WordPress `$allowedposttags` post list
(`internal/sanitize/sanitize.go:27-28`) — the same tier M10a applies to a
non-`unfiltered_html` writer's post content (`TierFor` returns `TierB` for a
`PostContent` field when the writer lacks `unfiltered_html`,
`internal/sanitize/sanitize.go:69-77`). The command is dry-run by default and
rewrites nothing without an explicit `-apply` flag; it never runs as part of
normal server operation, because it mutates a database grimoire may not own.

M14 refines roadmap **Requirement 28 / 29**
(`../wordpress-core-parity-roadmap/requirements.md`, "M14 — Pre-M10a Content
Sanitization Backfill Requirements") and roadmap task group **14.A — Opt-in
backfill CLI** (`../wordpress-core-parity-roadmap/tasks.md`). It reuses M10a's
sanitization policy unchanged, adds no new web surface, and is the first
security-close-out of the post-M10 gap analysis
(`../post-m10-gap-analysis/README.md:66-73`, `:110-112`, "closes the one
standing security caveat from M10a; CLI-only. Next.") taken up after M11 (REST
delete parity) and M13 (term re-parenting).

## Glossary

- **Tier B / `$allowedposttags`** — the broad WordPress post allow-list; one of
  the three `sanitize` tiers (`internal/sanitize/sanitize.go:23-31`). A tier-B
  `Sanitize` over `PostContent`/`PostExcerpt` is a bluemonday allow-list pass
  that strips disallowed markup (e.g. `<script>`) while keeping allow-listed
  post markup and Gutenberg block-comment delimiters (tier B is built with
  `AllowComments`). It is the exact tier M10a applies to a
  non-`unfiltered_html` writer's post content.
- **`SanitizeAt`** — `Policy.SanitizeAt(k FieldKind, t Tier, in string)
  (string, error)` (`internal/sanitize/policy.go:121`). Given an explicit
  `FieldKind` and `Tier`, it returns the sanitized value or a fail-closed error;
  it applies the named tier UNCONDITIONALLY (the tier is an argument, not
  derived from a writer's capability), which is exactly what the backfill needs.
- **`sanitize.New()`** — the `Policy` constructor
  (`internal/sanitize/policy.go:29`); M14 builds one policy and reuses it for
  every row.
- **The backfill** — the M14 sweep: read every targeted `{prefix}posts` row,
  compute its tier-B-sanitized `post_content`/`post_excerpt`, and rewrite only
  the rows whose sanitized value differs.
- **Dry run vs apply** — the default run computes and reports what it *would*
  change and writes nothing; a run with the explicit `-apply` flag performs the
  writes. Mirrors `migrate -check`'s read-only-report posture
  (`cmd/grimoire-cli/main.go:82-85`, `:139-205`).
- **Idempotent fixed point** — a tier-B-sanitized value is a fixed point of the
  tier-B policy: sanitizing it again yields the same bytes. A row already clean
  is therefore skipped, and a second apply is a no-op (roadmap Req 28.3).
- **The targeted content-only update** — a narrow write that sets ONLY
  `post_content`/`post_excerpt` on one row by `ID`, as distinct from
  `PostWriter.Update` (`internal/domain/repository.go:447-457`), which replaces
  the whole post row and unconditionally bumps `post_modified`/`post_modified_gmt`
  (`internal/storage/wprepo/writers.go:64-104`).
- **The report** — the audit trail emitted by both dry-run and apply: per-row
  lines (by `ID`, `post_type`, which field(s) changed, and a size/diff
  indicator) plus totals, with NO full post bodies dumped to stdout.
- **The paginated walk** — a dedicated backfill read port that streams targeted
  rows page by page so a large `{prefix}posts` table is never loaded wholly into
  memory. No such "iterate every post across all statuses/types" read exists
  today — every `PostRepo` read is status/type-filtered
  (`internal/storage/wprepo/repo.go:86-158`, `:180-251`).
- **"Drain the pool"** — M10a's own framing of the standing gap
  (`../post-m10-gap-analysis/README.md:68`): M10a stopped new unsanitized
  content flowing in; M14 removes the unsanitized content already stored.

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | **A new `grimoire-cli` subcommand, dry-run by default.** Add a `backfill-content` subcommand dispatched from `main()` (`cmd/grimoire-cli/main.go:36-48`) like `migrate`/`seed`/`createadmin`/`sessions`, parsing its own `flag.NewFlagSet` with `-config` (`cmd/grimoire-cli/main.go:76-80`, `:200-205`). The default is a DRY RUN that reports which rows/fields it WOULD rewrite and changes nothing; a write happens ONLY with an explicit `-apply` flag. The command NEVER runs as part of normal server operation. | Requirements 1, 6, 8. Mirrors `migrate -check`'s read-only report (`cmd/grimoire-cli/main.go:82-85`). |
| D2 | **Apply tier B unconditionally via `SanitizeAt`.** For each row compute `SanitizeAt(sanitize.PostContent, sanitize.TierB, post_content)` and `SanitizeAt(sanitize.PostExcerpt, sanitize.TierB, post_excerpt)` (`internal/sanitize/policy.go:121-148`). Tier B — the broad `$allowedposttags` post list (`internal/sanitize/sanitize.go:27-28`), the same tier M10a applies to a non-`unfiltered_html` writer's post content — is chosen deliberately over tier A/C so the backfill neither over-strips legitimate post markup (tier A, the tight comment list) nor leaves content unfiltered (tier C). A `SanitizeAt` error on a row fails that row (reported) and never silently writes. | Requirements 2, 9. `SanitizeAt` applies the named tier unconditionally, independent of any writer capability. |
| D3 | **Walk ALL posts, every status, over a pinned post_type set, streamed/paginated.** The walk covers every `post_status` (publish/draft/pending/private/future/inherit/trash). It targets the post_type set `{post, page, revision}` for both `post_content` and `post_excerpt`, because those rows carry user-authored post bodies that could hold pre-M10a markup; it EXCLUDES `attachment` and `nav_menu_item`, whose `post_content`/`post_excerpt` are not user-authored post markup in the same sense (an attachment's `post_content` is a caption/description and a nav_menu_item's fields hold menu-item metadata). The walk is paginated through a new dedicated backfill read port so a large table is not loaded wholly into memory. | Requirement 3. No existing read iterates all statuses/types (`internal/storage/wprepo/repo.go:86-158`); the port is additive. |
| D4 | **Change-detection: only rewrite a row whose sanitized value differs, via a narrow content-only update.** A row where both `post_content` and `post_excerpt` already equal their tier-B-sanitized value is skipped (no write). A changed row is written with a narrow update that sets ONLY `post_content`/`post_excerpt` by `ID` — NOT `PostWriter.Update` (`internal/domain/repository.go:447-457`), which replaces the whole row and always bumps `post_modified`/`post_modified_gmt` (`internal/storage/wprepo/writers.go:72-100`). M14 does NOT bump `post_modified`/`post_modified_gmt`: a sanitization backfill is a data-safety correction, not an editorial edit, and bumping it would reorder "recently modified" views. Trade-off: an operator auditing by `post_modified` will not see the backfill there — the report (D5) is the audit trail instead. | Requirements 4, 5. The narrow update cannot clobber other columns. |
| D5 | **Auditable, reversible-aware report.** Both dry-run and apply produce a report naming every row that would change / did change — by `ID`, `post_type`, which field(s), and a size/diff indicator — plus totals (rows scanned, rows changed, per-field change counts). Full post bodies are NEVER dumped to stdout (they may be huge or sensitive). M14 does not itself snapshot or undo; the report is the audit trail, and the command's help/usage SHALL tell the operator to take a database backup before `-apply`. | Requirements 5, 7. Reversibility is operator-owned (a DB backup), surfaced in usage. |
| D6 | **No new web surface, no new capability, no migration.** M14 is CLI + storage read/update-port additions + reuse of the M10a policy. It changes no HTTP handler, no render path, and adds no table or column (`post_content`/`post_excerpt` already exist, `internal/storage/wprepo/repo.go:20-25`). The new read/update ports are additive to `domain` and `wprepo`. | Requirement 8. |
| D7 | **Tests: cross-vendor contract (walk, tier-B application, idempotence, dry-run-makes-no-changes) + command report, pure Go/SQLite, no new dependency.** Cross-vendor `storagetest` coverage for the backfill read/update ports: the paginated walk returns all targeted rows (and no excluded type); a row with a disallowed `<script>` in `post_content` is rewritten to its tier-B-clean value on apply; an already-clean row is untouched; a dry run changes nothing; a second apply is a no-op. A command-level test (SQLite) covers dry-run vs `-apply` behavior and the report counts. SQLite-only Go tests, env-gated MySQL/Postgres through the existing harness (`NewReposFunc`/`SeedFixtures`, `internal/storage/storagetest/sqlite_test.go:13-40`); no new env var or dependency; `gofmt`/`go vet`/`go build ./...`/`go test ./...` green at the end of every task. | Requirement 10. |

## Requirements

### Requirement 1: Opt-in backfill subcommand, dry-run default, explicit apply, never automatic

**User Story:** As a site owner who imported a WordPress database or ran
grimoire before M10a, I want an opt-in CLI command that retroactively sanitizes
stored post content, so that a pre-M10a `<script>` in a post body is removed
rather than left inert-but-present — and I want it to never run on its own.

#### Acceptance Criteria

1. THE `grimoire-cli` command SHALL gain a new backfill subcommand (e.g.
   `backfill-content`) dispatched from the `main()` switch
   (`cmd/grimoire-cli/main.go:36-48`) alongside `migrate`/`seed`/`createadmin`/
   `sessions`, parsing its own `flag.NewFlagSet` and accepting `-config`
   pointing at a grimoire YAML, exactly as the sibling subcommands do
   (`cmd/grimoire-cli/main.go:76-80`, `:200-205`).
2. WHEN an operator runs the backfill subcommand WITHOUT the `-apply` flag, THE
   command SHALL perform a DRY RUN: it SHALL report the rows and fields it would
   rewrite and SHALL NOT modify any row.
3. WHEN an operator runs the backfill subcommand WITH an explicit `-apply` flag,
   THE command SHALL perform the writes described by Requirements 2 and 4.
4. THE backfill SHALL NEVER run as part of normal server operation: it SHALL be
   reachable only by explicitly invoking the subcommand, never from an HTTP
   handler, a scheduler, or server startup, because it mutates a database
   grimoire may not own.
5. THE subcommand's `usage`/help text SHALL state that the operator should take
   a database backup before running with `-apply`, because M14 provides no undo
   (D5).
6. WHEN `config.Load(-config)` or opening storage fails, THE command SHALL
   return a non-nil error and write nothing, matching the error posture of the
   sibling subcommands (`cmd/grimoire-cli/main.go:49-53`, `:90-99`).

### Requirement 2: Tier-B sanitization of content and excerpt via SanitizeAt

**User Story:** As a maintainer, I want the backfill to apply exactly M10a's
post-tier policy to each row, so that it neither over-strips legitimate post
markup nor leaves content unfiltered, and so it stays consistent with the live
write boundary.

#### Acceptance Criteria

1. WHEN the backfill processes a row, THE command SHALL compute the row's new
   `post_content` as `SanitizeAt(sanitize.PostContent, sanitize.TierB,
   post_content)` and its new `post_excerpt` as `SanitizeAt(sanitize.PostExcerpt,
   sanitize.TierB, post_excerpt)` using a single `sanitize.New()` policy
   (`internal/sanitize/policy.go:29`, `:121`;
   `internal/sanitize/sanitize.go:17-31`).
2. THE backfill SHALL use tier B specifically — the broad `$allowedposttags`
   post list (`internal/sanitize/sanitize.go:27-28`) — and SHALL NOT use tier A
   (the tight comment list) or tier C (the unfiltered bypass), because tier B is
   the tier M10a applies to a non-`unfiltered_html` writer's post content
   (`internal/sanitize/sanitize.go:69-77`).
3. THE tier passed to `SanitizeAt` SHALL be the constant `sanitize.TierB`
   unconditionally for every row, NOT a tier derived from any writer, capability,
   or post field, since the backfill has no acting principal.
4. WHEN a row's stored `post_content` contains disallowed markup such as
   `<script>`, THE tier-B result SHALL have that markup stripped while
   allow-listed post markup and Gutenberg block-comment delimiters are preserved
   (the tier-B allow-list behavior, `AllowComments` at tier B).

### Requirement 3: Target post/page/revision across all statuses, paginated, with a stated rationale

**User Story:** As a maintainer, I want the backfill to cover every row that can
hold pre-M10a user post markup — regardless of its publication status — without
loading the whole table into memory, so that nothing unsanitized is missed and a
large WordPress table does not exhaust memory.

#### Acceptance Criteria

1. THE backfill SHALL target `{prefix}posts` rows whose `post_type` is one of
   `post`, `page`, or `revision`, and SHALL EXCLUDE `attachment` and
   `nav_menu_item` rows (D3). The rationale SHALL be recorded: `post`/`page` and
   their `revision` rows carry user-authored post bodies, while `attachment` and
   `nav_menu_item` `post_content`/`post_excerpt` are not user-authored post
   markup in the same sense.
2. THE backfill SHALL include a targeted row regardless of its `post_status`
   (publish, draft, pending, private, future, inherit, trash), because pre-M10a
   unsanitized content can sit in any status — unlike every existing `PostRepo`
   read, which filters to `post_status = 'publish'`
   (`internal/storage/wprepo/repo.go:92-93`, `:113-116`, `:207-208`).
3. THE backfill SHALL read targeted rows through a new dedicated, paginated read
   port (additive to `domain` and `wprepo`) that returns rows page by page, so
   the whole `{prefix}posts` table is NOT loaded into memory at once.
4. THE paginated walk SHALL return every targeted row exactly once across the
   full run (no row skipped and no row visited twice), under a stable ordering
   (e.g. by `ID`) so pagination boundaries do not drop or duplicate rows.
5. THE new read port SHALL select at minimum each targeted row's `ID`,
   `post_type`, `post_content`, and `post_excerpt` — the fields the backfill
   needs — and SHALL NOT require loading every post column the way a full
   `PostWriter.ByID`/`Update` round-trip would.

### Requirement 4: Change-detection and narrow content-only update; post_modified not bumped

**User Story:** As a maintainer, I want the backfill to rewrite only rows that
actually change and to touch only the two content fields, so that it cannot
clobber other columns or reorder "recently modified" views, and so its report
counts genuine changes only.

#### Acceptance Criteria

1. WHEN a row's tier-B-sanitized `post_content` equals the stored `post_content`
   AND its tier-B-sanitized `post_excerpt` equals the stored `post_excerpt`, THE
   command SHALL skip the row and SHALL NOT write it (no-op for clean rows).
2. WHEN at least one of a row's two fields differs from its tier-B-sanitized
   value AND `-apply` is set, THE command SHALL write the row through a narrow
   update that sets ONLY `post_content` and `post_excerpt` by `ID`.
3. THE narrow update SHALL NOT be `PostWriter.Update`
   (`internal/domain/repository.go:447-457`), because `PostRepo.Update`
   replaces the whole row and unconditionally bumps `post_modified`/
   `post_modified_gmt` (`internal/storage/wprepo/writers.go:72-100`); M14 adds a
   dedicated content-only update port instead.
4. THE narrow update SHALL NOT modify `post_modified` or `post_modified_gmt`,
   nor any column other than `post_content`/`post_excerpt`, so a sanitization
   correction does not reorder "recently modified" views (D4).
5. WHEN only one of the two fields differs, THE narrow update SHALL still write
   the clean value for both fields on that row (both are tier-B fixed points, so
   rewriting the unchanged one is a no-op in value), and the report SHALL name
   which field(s) actually changed.

### Requirement 5: Idempotence — a second run is a no-op

**User Story:** As an operator, I want a second backfill run to change nothing
after the first apply, so that I can re-run it safely to confirm the pool is
drained.

#### Acceptance Criteria

1. WHEN the backfill has been applied once and is then run again with `-apply`,
   THE command SHALL make no further changes, because every targeted row's
   fields are now tier-B fixed points (`SanitizeAt(PostContent, TierB, x) == x`
   once `x` is tier-B-clean).
2. WHEN the backfill is run a second time, THE report SHALL show zero rows
   changed and per-field change counts of zero.
3. THE idempotence guarantee SHALL hold for a dry run following an apply as
   well: a dry run after a completed apply SHALL report zero rows to change.

### Requirement 6: Dry run changes nothing

**User Story:** As an operator, I want the default (no-`-apply`) run to be
guaranteed read-only, so that I can preview the backfill against a production
database without risk.

#### Acceptance Criteria

1. WHEN the backfill runs WITHOUT `-apply`, THE command SHALL issue no write to
   `{prefix}posts` (or any other table) and SHALL leave every row's
   `post_content`/`post_excerpt` byte-identical to its pre-run value.
2. WHEN the backfill runs WITHOUT `-apply`, THE command SHALL still read and
   tier-B-evaluate every targeted row so the report accurately names the rows
   and fields it WOULD rewrite.
3. THE dry-run report SHALL be identical in the rows/fields it names to what a
   subsequent `-apply` run on the unchanged database would change, so the
   preview faithfully predicts the apply.

### Requirement 7: Auditable report — per-row lines and totals, no full-body dumps

**User Story:** As an operator, I want a report that shows exactly which rows and
fields the backfill touched (without dumping huge or sensitive post bodies), so
that the operation is auditable and I retain a record of what the apply did.

#### Acceptance Criteria

1. WHEN the backfill (dry-run or apply) processes the table, THE command SHALL
   emit a per-row report line for every row that would change / did change,
   naming the row's `ID`, its `post_type`, which field(s) (`post_content` and/or
   `post_excerpt`) changed, and a size/diff indicator (e.g. before/after byte
   lengths or a change marker).
2. THE report SHALL NOT dump full `post_content`/`post_excerpt` bodies to
   stdout, because stored bodies may be large or sensitive.
3. WHEN the run completes, THE command SHALL emit totals: rows scanned, rows
   changed, and a per-field change count for `post_content` and `post_excerpt`.
4. THE report SHALL make an apply run's effect auditable after the fact (D5) —
   it is the audit trail, since M14 does not snapshot or undo and does not bump
   `post_modified` (Requirement 4.4).

### Requirement 8: No new web surface, no new capability, no migration

**User Story:** As a maintainer, I want M14 to be a CLI-and-storage-port change
that reuses M10a's policy, so that it introduces no new attack surface, no new
authorization, and no schema churn.

#### Acceptance Criteria

1. THE milestone SHALL change NO HTTP handler and NO render path; it adds only a
   `grimoire-cli` subcommand and additive storage read/update ports, reusing
   M10a's `sanitize` policy unchanged.
2. THE milestone SHALL add NO file under `internal/storage/migrations` and SHALL
   add NO table or column, because `post_content` and `post_excerpt` already
   exist (`internal/storage/wprepo/repo.go:20-25`); the task list SHALL verify
   this explicitly, as the sibling specs' task lists do.
3. THE milestone SHALL introduce NO new capability name; the backfill has no
   acting principal and makes no authorization decision (it is operator-gated by
   access to the CLI and the database).
4. THE new read and content-only update ports SHALL be additive to `domain` and
   `wprepo` and SHALL NOT alter the behavior of existing `PostRepo` read methods
   or `PostWriter` (`internal/storage/wprepo/repo.go:86-251`;
   `internal/storage/wprepo/writers.go:64-104`).

### Requirement 9: A SanitizeAt error on a row is reported, and that row is not written (fail-safe)

**User Story:** As a maintainer, I want a sanitization failure on one row to be
surfaced and to skip that row's write, so that the backfill never silently
persists an unverified value and never aborts the whole sweep over one bad row.

#### Acceptance Criteria

1. IF `SanitizeAt` returns a non-nil error for a row's `post_content` or
   `post_excerpt`, THEN THE command SHALL NOT write that row (even under
   `-apply`), because `SanitizeAt` returns no persistable value together with an
   error (`internal/sanitize/policy.go:139-148`).
2. WHEN a row's sanitization errors, THE command SHALL report that row as failed
   (by `ID` and the failing field), distinct from rows that changed and rows
   that were clean.
3. WHEN a row's sanitization errors, THE command SHALL continue processing the
   remaining rows rather than aborting the sweep, and SHALL reflect the failed
   row(s) in the totals.
4. THE command SHALL exit with a non-zero status WHEN any row failed
   sanitization during an `-apply` run, so the failure is not lost in the
   report.

### Requirement 10: Cross-vendor contract and command test coverage

**User Story:** As a maintainer, I want every new behavior proven before it is
considered done, so that the backfill cannot become a data-integrity hazard or a
silent no-op.

#### Acceptance Criteria

1. THE milestone SHALL cover the new paginated read port with a cross-vendor
   `storagetest` contract test proving it returns all targeted rows across
   statuses, returns no excluded (`attachment`/`nav_menu_item`) row, and
   paginates without dropping or duplicating a row (SQLite always; MySQL/Postgres
   env-gated through the existing harness, `internal/storage/storagetest/
   sqlite_test.go:13-40`, with no new env var).
2. THE milestone SHALL cover the new content-only update port with a cross-vendor
   contract test proving a row whose `post_content` holds a disallowed `<script>`
   is rewritten to its tier-B-clean value on apply, an already-clean row is left
   untouched, and the update changes no column other than `post_content`/
   `post_excerpt` (notably not `post_modified`).
3. THE milestone SHALL cover idempotence and the dry-run guarantee at the
   storage/contract level: a dry run changes nothing and a second apply is a
   no-op.
4. THE milestone SHALL cover the subcommand with a command-level test (SQLite)
   proving dry-run vs `-apply` behavior and the report counts (rows scanned,
   rows changed, per-field counts, failed rows).
5. THE tests SHALL be pure Go over SQLite with NO new dependency, and `gofmt`,
   `go vet`, `go build ./...`, and `go test ./...` SHALL be green at the end of
   every task (D7).

## Out of scope

- **Comment content backfill.** M10a already backstops comment markup at render,
  and a comment-content sweep is a separate concern with its own tier (tier A,
  the comment list) and target table (`{prefix}comments`). OUT of M14; it could
  be a follow-up milestone.
- **`attachment` and `nav_menu_item` post types.** Excluded per D3 /
  Requirement 3.1 — their `post_content`/`post_excerpt` are not user-authored
  post markup in the same sense.
- **Title backfill (`post_title`).** `post_title` is a plain-text reduction at a
  different transform (tier A/B route `PostTitle` through `titleText`,
  `internal/sanitize/policy.go:135-146`), not a tier-B post-markup sanitize. M14
  is content/excerpt only, matching roadmap Req 28.1's exact wording ("each
  row's `post_content` and `post_excerpt`"). OUT of M14.
- **User-profile and term fields** (e.g. user descriptions, term descriptions).
  OUT of M14; not post bodies.
- **Any undo/snapshot/rollback feature.** M14 provides no undo; the operator
  takes a database backup before `-apply` (D5, Requirement 1.5). The report is
  the audit trail.
- **Any automatic, scheduled, or server-triggered run.** The backfill is
  explicit-invocation-only (Requirement 1.4); it is never wired into startup, a
  scheduler, or an HTTP handler.
- **New schema, columns, tables, routes, or capabilities** (D6 / Requirement 8).

## Traceability

This spec refines roadmap **Requirement 28 (Opt-in Sanitization Backfill)** and
**Requirement 29 (M14 Exclusions and Test Coverage)**
(`../wordpress-core-parity-roadmap/requirements.md`, "M14 — Pre-M10a Content
Sanitization Backfill Requirements") and roadmap task group **14.A — Opt-in
backfill CLI** (`../wordpress-core-parity-roadmap/tasks.md`). It reuses **M10a**'s
sanitization policy unchanged — `sanitize.New()`, `Policy.SanitizeAt`, and
tier B (`$allowedposttags`) (`internal/sanitize/policy.go:29`, `:121-148`;
`internal/sanitize/sanitize.go:17-77`) — and mirrors the `grimoire-cli`
subcommand pattern (`cmd/grimoire-cli/main.go:30-55`, `:76-205`). It is the M14
entry of the post-M10 gap analysis
(`../post-m10-gap-analysis/README.md:66-73`, `:110-112`), the first
security-close-out taken up after M11 (REST delete parity) and M13 (term
re-parenting).
