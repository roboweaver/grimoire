# Implementation Plan: M13 — Term re-parenting & hierarchy writes

## Overview

> **This milestone threads a WordPress `parent` through the already-complete
> term write chain** — `restTermWriteRequest` behind `POST /wp-json/wp/v2/categories`
> and the single-item `PUT`/`PATCH` (`internal/web/rest_terms.go:33`), and the
> admin-API `termWriteRequest` behind `adminTermCreate`/`adminTermUpdate`
> (`internal/web/adminapi_terms.go:29`, `:72`, `:99`) — down through
> `content.TermWriteService` (`internal/content/writeservices.go:303`) and into
> `term_taxonomy.parent`. M9b made `term_taxonomy.parent` *readable*
> (`domain.Term.ParentID`, documented "Read-only in this milestone… no write
> path sets it", `internal/domain/entities.go:52-62`); M13 is the write path that
> paragraph anticipated. It closes exactly that gap and nothing more: no new
> route, no new port method, no new capability, no schema/migration.
>
> **What this adds — and only this.** Two storage edits in one file
> (`internal/storage/wprepo/writers.go`): `TermRepo.Create` replaces the
> hardcoded literal `0` parent arg in its `term_taxonomy` INSERT with
> `t.ParentID`; `TermRepo.Update`, which today touches only `{prefix}terms`
> name/slug via a single `NewUpdate`, now wraps a two-statement `RunInTx` that
> updates `{prefix}terms` name/slug (keeping the `errNotFoundIfMissing` existence
> semantics and its MySQL matched-but-unchanged no-op caveat) **and**
> `{prefix}term_taxonomy` `parent = ?` for the same term. Two new `content`
> sentinels beside `ErrForbidden` — `ErrTermParentCycle` and
> `ErrTermParentInvalid` — plus a private `TermWriteService.validateParent`
> (`internal/content/writeservices.go`) that reuses M9b's taxonomy-scoped
> `TermHierarchy`: flat-taxonomy reject (`post_tag`), self-parent check,
> `LoadTermHierarchy` + `byID` membership for the nonexistent/cross-taxonomy
> cases collapsed into one check, and `DescendantIDs` for the transitive cycle.
> `validateParent` is called in `Create` and `Update` between the
> `CanManageTerms` gate and the writer, so a cycle/invalid parent is `400` while
> an unauthorized caller stays `403` (the authorization decision wins). The REST
> create/update handlers gain a `Parent *int64 json:"parent"` field and pre-read
> the term on update to resolve the omitted-vs-explicit-zero distinction
> (omit → current, `0` → top-level, `id` → re-parent) and learn its taxonomy; the
> admin side adds the same field and **relaxes `adminTermUpdate`'s unconditional
> Name/Slug-required rule via load-and-merge** — pre-reading the current term
> (which also yields its taxonomy for validation and its current parent for
> "unchanged"), so a parent-only update now succeeds.
>
> **No schema, no migration, no new capability, read ports unchanged.** Every
> affected table and column already exists; `domain.TermWriter`/`TermReader` are
> untouched — `parent` rides on the existing `domain.Term.ParentID` field — and
> the `manage_categories` gate (`auth.CanManageTerms`, `internal/auth/policy.go:78`)
> is reused with no new capability name (Req 8). M9b's descendant-aware counts
> (the `EXISTS` semi-join and `CountByTermSlug`,
> `internal/storage/wprepo/repo.go:186-224`) and `TermHierarchy` (loaded
> per-request, deliberately uncached, `internal/content/archive.go:61-130`) read
> `term_taxonomy.parent` live, so a re-parent is reflected on the next read with
> no write-side bookkeeping; M13 proves this stays correct under re-parenting
> rather than modifying it (Req 6, 7).
>
> **Standing gate convention.** Keep `gofmt -l .` empty and `go vet ./...`,
> `go build ./...`, `go test ./...` green after every phase, following the
> close-out discipline of M10a/M10.B/M10.C/M10.F/M11.
>
> **Strict-TDD spine.** Every behavior-adding sub-task is preceded by the
> sub-task that writes its failing test, matching how M5–M11 were run and how 10.5
> (`../10.5-nav-menu-editing`) and 11 (`../11-rest-delete-parity`) are ordered.
> The storage, service, REST, and admin edits live in largely different files, so
> their test tasks pair in one wave and their impl tasks in the next — but Phases
> 3 and 4 (the REST and admin handlers that map the parent sentinels) depend on
> Phase 2's service landing first, so the service impl is sequenced before the two
> handler impls.
>
> **The service-test fake is reused, not re-authored.** The service tests build
> over the existing `fakeTermWriter` in
> `internal/content/writeservices_test.go`, which is already the combined
> `domain.TermWriter`+`domain.TermReader` the service holds and already serves a
> seeded graph through its `byTaxonomy`/`byIDs` maps (the archive hierarchy tests
> reuse it the same way). Phase 2's service test extends that fake's seeded data,
> not its shape — a deliberate, called-out reuse, not a drive-by change to a
> shared helper.

## Tasks

## Phase 1 — Storage: `TermRepo.Create`/`Update` write `term_taxonomy.parent` (`internal/storage/wprepo`, TDD, cross-vendor contract)

- [x] 1.1 Write the failing storage contract assertions first. Extend the
      existing `"TermWriter Create, Update, and Delete"` subtest in
      `internal/storage/storagetest/contract.go` (the cross-vendor term write
      contract run by `sqlite_test.go` always and `mysql_test.go`/`postgres_test.go`
      env-gated through the existing harness — no new env var) with parent
      coverage, reading parent back through the term read path the
      `termparent_contract.go` suite already uses (`repos.Terms.BySlug` /
      `repos.TermReader.TermsByIDs`, which populate `domain.Term.ParentID`).
      Assert: `TermWriter.Create` of a `category` term with a non-zero `ParentID`
      (parented under a seeded fixture category such as `"news"`) writes
      `term_taxonomy.parent` and reads back with `ParentID` equal to the seeded
      parent id (Req 1.2); a `Create` with `ParentID == 0` reads back top-level
      (Req 1.3); `TermWriter.Update` changing `ParentID` from one value to another
      (and to `0`) reads back the new parent **and** still applies the new
      name/slug, with an unrelated fixture term's parent untouched (Req 2.1, 2.3);
      and `Update` of an unknown `term_id` still returns `domain.ErrNotFound`,
      while a no-op same-value update still succeeds (the MySQL
      matched-but-unchanged caveat, Req 2.5). Keep the existing create/update/
      delete assertions intact.
      Files: `internal/storage/storagetest/contract.go`
      Verify: `go test ./internal/storage/storagetest -run SQLite` — fails
      (`Create` still writes parent `0`; `Update` never touches
      `term_taxonomy.parent`). _(Req 1.2, 1.3, 2.1, 2.3, 2.5, 9.1; D1, D7)_

- [x] 1.2 Implement the two-table parent writes in `TermRepo.Create` and
      `TermRepo.Update`. In `internal/storage/wprepo/writers.go`, exactly per
      design: in `Create` (`writers.go:121`) change the fourth argument of the
      `term_taxonomy` INSERT (`term_id, taxonomy, description, parent, count`)
      from the literal `0` to `t.ParentID`, leaving the surrounding `RunInTx`, the
      `insertReturningID` on `{prefix}terms`, and the `description`/`count`
      defaults (the fifth `?` stays literal `0`) unchanged (Req 1.2, 1.3, 1.4). In
      `Update` (`writers.go:146`) replace the single `NewUpdate` with a `RunInTx`
      wrapping two statements: first the existing `{prefix}terms` name/slug
      `NewUpdate` keyed on `term_id` with the **unchanged** `errNotFoundIfMissing`
      existence check (now run via `tx.NewSelect(...).Exists` inside the
      transaction so the Exists check sees the same snapshot, returning
      `domain.ErrNotFound` and rolling back before `term_taxonomy` is touched),
      then a `tx.NewUpdate` on `{prefix}term_taxonomy` setting `parent = ?` keyed
      on `term_id = ?` (lowercase columns, plain `?` placeholders — no
      `rebind.Ident("ID")` quoting needed; a 0-rows result here is not re-treated
      as `ErrNotFound`, the `{prefix}terms` row is the source of truth). Both
      updates in one transaction so a rename+re-parent cannot half-apply (Req 2.1,
      2.5).
      Files: `internal/storage/wprepo/writers.go`
      Verify: `go test ./internal/storage/storagetest -run SQLite` — the 1.1
      assertions pass; `go build ./...` compiles; `go vet ./...` clean.
      _(Req 1.2, 1.3, 1.4, 2.1, 2.3, 2.5, 8.2; D1)_

## Phase 2 — Content: sentinels + `validateParent` + Create/Update wiring (`internal/content`, TDD)

- [x] 2.1 Write the failing service tests first. Add
      `internal/content/termreparent_test.go` (new file beside
      `writeservices_test.go`, same `package content`) driving
      `TermWriteService.Create`/`Update` over the **existing** `fakeTermWriter`
      (`writeservices_test.go:143`), the combined `domain.TermWriter`+`TermReader`
      the service already holds — seed its `byTaxonomy[TaxonomyCategory]` with a
      parent→child→grandchild chain and a sibling so `LoadTermHierarchy`/
      `DescendantIDs`/`byID` resolve, and seed a `post_tag` entry in
      `byTaxonomy[taxonomyPostTag]` for the cross-taxonomy case (extend the fake's
      seeded data only, not its shape — this is a deliberate reuse). Assert, per
      the design's Testing strategy, each case asserting the sentinel AND that the
      writer was NOT called (`f.created`/`f.updated` nil, stored hierarchy
      unchanged): `Update` with `parent == t.ID` → `ErrTermParentCycle` (Req 3.1);
      `Update` with a parent in `DescendantIDs(t.ID)` (set the chain's parent's
      parent to its grandchild) → `ErrTermParentCycle` (Req 3.2); a non-zero
      parent naming no term in the category graph → `ErrTermParentInvalid` (Req
      4.1); a parent id that exists only in the `post_tag` graph (absent from the
      category graph) → `ErrTermParentInvalid` (Req 4.2); a `post_tag` term with a
      non-zero parent → `ErrTermParentInvalid` (Req 4.3); `parent == 0` on any
      taxonomy (incl. `post_tag`) → nil, writer called with `ParentID == 0` (Req
      4.4); `Create` with a valid non-zero same-taxonomy parent → writer called
      with that `ParentID` (trivially passes the cycle check, Req 3.4, 1.2); and
      an actor lacking `manage_categories` → `ErrForbidden` even when the parent
      is also invalid, with no `ListByTaxonomy` read performed (the gate precedes
      `validateParent`, Req 5.3).
      Files: `internal/content/termreparent_test.go`
      Verify: `go test ./internal/content -run TermReparent` — fails
      (`validateParent`, `ErrTermParentCycle`, `ErrTermParentInvalid` absent).
      _(Req 3.1, 3.2, 3.4, 4.1, 4.2, 4.3, 4.4, 5.3, 9.2; D3, D4)_

- [x] 2.2 Implement the sentinels, `validateParent`, and the Create/Update wiring.
      In `internal/content/writeservices.go`, exactly per design: add the two
      package-level sentinels beside `ErrForbidden` —
      `var ErrTermParentCycle = errors.New("content: term parent would create a cycle")`
      and `var ErrTermParentInvalid = errors.New("content: term parent is invalid")`,
      with the design's doc comments naming the requirement each covers. Add the
      private `(s *TermWriteService) validateParent(ctx context.Context, t domain.Term, parent int64) error`:
      return nil for `parent == 0` (top-level valid for any taxonomy, Req 4.4);
      return `ErrTermParentInvalid` when `t.Taxonomy != content.TaxonomyCategory`
      (flat-taxonomy reject, Req 4.3); return `ErrTermParentCycle` for
      `parent == t.ID` (Req 3.1); `LoadTermHierarchy(ctx, s.w, t.Taxonomy)`, then
      `ErrTermParentInvalid` when `parent` is absent from `h.byID` (the
      nonexistent/cross-taxonomy cases in one membership check, reading `byID`
      directly since `validateParent` is in-package, Req 4.1/4.2), then
      `ErrTermParentCycle` when `parent` is in `h.DescendantIDs(t.ID)` (transitive
      cycle, Req 3.2, 3.5). Call `validateParent(ctx, t, t.ParentID)` in both
      `Create` and `Update` **after** the `auth.CanManageTerms` gate and **before**
      `s.w.Create`/`s.w.Update`, returning its error unchanged so nothing is
      written on failure. The service never inspects a request pointer and reads
      `t.Taxonomy` (the subject term's own taxonomy), never a request taxonomy for
      the parent (Req 4.5).
      Files: `internal/content/writeservices.go`
      Verify: `go test ./internal/content -run TermReparent` — the 2.1 tests pass;
      `go test ./internal/content` green (existing `TestTermWriteAuthz`/
      `TestTermWriteUpdateAuthz` still pass, the gate still precedes validation);
      `go vet ./...` clean. _(Req 3.1, 3.2, 3.4, 3.5, 4.1, 4.2, 4.3, 4.4, 4.5, 5.3,
      8.5; D3, D4)_

## Phase 3 — Web REST: thread `parent` through `restTermWriteRequest` + handlers (`internal/web`, TDD)

- [x] 3.1 Write the failing REST handler tests first. Add
      `internal/web/rest_terms_parent_test.go` driving the live chi router with a
      real `content.TermWriteService` over SQLite (the existing REST term write
      harness — a `manage_categories`-holding principal for the allowed cases).
      Assert, per the design's Testing strategy: `POST /wp-json/wp/v2/categories`
      with `parent: <id>` creates the term nested, and a subsequent
      `GET /categories/{id}` reports the written value via `restTerm.parent`
      (round-trip, Req 1.2, 6.1); `PUT`/`PATCH /categories/{id}` with `parent: <id>`
      re-parents and with `parent: 0` promotes to top-level, each reflected on the
      next read (Req 2.1, 2.3, 6.4); a `PUT` that renames but omits `parent`
      leaves the stored parent unchanged (partial-update pointer semantics, Req
      2.2); a self/descendant cycle and a nonexistent/cross-taxonomy parent each →
      `400 rest_invalid_param`, nothing written (Req 3.3, 4.1, 4.2); a
      `post_tag` non-zero parent via `POST`/`PUT /tags` → `400` (Req 4.3); and a
      caller lacking `manage_categories` → `403 rest_cannot_create` (create) /
      `rest_cannot_edit` (update), no parent altered (Req 5.1).
      Files: `internal/web/rest_terms_parent_test.go`
      Verify: `go test ./internal/web -run RESTTermParent` — fails (`Parent` field
      and the `400` mapping absent). _(Req 1.2, 2.1, 2.2, 2.3, 3.3, 4.1, 4.2, 4.3,
      5.1, 6.1, 6.4, 9.3; D1, D2)_

- [x] 3.2 Implement the REST parent field + handler wiring. In
      `internal/web/rest_terms.go`, exactly per design: add
      `Parent *int64 json:"parent"` to `restTermWriteRequest` (`rest_terms.go:33`),
      beside `Name`/`Slug`. In `handleRESTTermCreate(taxonomy)` resolve the field
      into the `domain.Term` it already builds — `var parentID int64;
      if body.Parent != nil { parentID = *body.Parent }` (omitted/`0` → top-level,
      Req 1.3) — and set `ParentID: parentID` on the `Create` call (taxonomy from
      the route closure). In `handleRESTTermUpdate(taxonomy)` the handler already
      pre-reads the term via `restTermByID` into `current` (yielding
      `current.Taxonomy` and `current.ParentID`): resolve on the `updated` copy it
      already builds — `if body.Parent != nil { updated.ParentID = *body.Parent }`,
      otherwise leaving `updated.ParentID` at `current.ParentID` (omit → unchanged,
      Req 2.2; `0` → top-level, Req 2.3; `id` → re-parent, Req 2.1). In both
      handlers add `case errors.Is(err, content.ErrTermParentCycle),
      errors.Is(err, content.ErrTermParentInvalid):` arms mapping to
      `writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", ...)` (Req
      3.3, 4.1-4.3), leaving the existing `ErrForbidden` → `403` and (on update)
      `ErrNotFound` → `404` arms unchanged.
      Files: `internal/web/rest_terms.go`
      Verify: `go test ./internal/web -run RESTTermParent` — the 3.1 tests pass;
      `go test ./internal/web` green; `go vet ./...` clean. _(Req 1.1, 1.2, 1.3,
      2.1, 2.2, 2.3, 3.3, 4.1, 4.2, 4.3, 5.1, 6.1, 6.4; D1, D2)_

## Phase 4 — Web admin: thread `parent` + relax Name/Slug rule via load-and-merge (`internal/web`, TDD)

- [x] 4.1 Write the failing admin handler tests first. Add
      `internal/web/adminapi_terms_parent_test.go` (or extend the existing admin
      term test) driving the admin `jsonHandler` surface with a real
      `content.TermWriteService` over SQLite (a `manage_categories`-holding
      principal for the allowed cases). Assert, per the design's Testing strategy:
      `adminTermCreate` with `parent` writes it, reflected on the next admin/REST
      read (Req 1.2, 6.2); `adminTermUpdate` with a **parent-only** body (no
      name/slug) now succeeds (load-and-merge keeps the current name/slug,
      re-parents) (Req 2.1, 2.2); `adminTermUpdate` of an unknown id still returns
      the admin-JSON `404` (`domain.ErrNotFound`, Req 2.5); a cycle/invalid/
      cross-taxonomy/flat parent on create or update → admin-JSON `400` (via
      `badRequestError`, Req 3.3, 4.1-4.3); a request supplying none of
      name/slug/parent → `400`; and a caller lacking `manage_categories` →
      admin-JSON `403`, no parent altered (Req 5.2).
      Files: `internal/web/adminapi_terms_parent_test.go`
      Verify: `go test ./internal/web -run AdminTermParent` — fails (`Parent`
      field, load-and-merge, and the `badRequestError` wrap absent). _(Req 1.2,
      2.1, 2.2, 2.5, 3.3, 4.1, 4.2, 4.3, 5.2, 6.2, 9.3; D1, D2)_

- [x] 4.2 Implement the admin parent field, load-and-merge update, and sentinel
      wrap. In `internal/web/adminapi_terms.go`, exactly per design: add
      `Parent *int64 json:"parent"` to `termWriteRequest` (`adminapi_terms.go:29`),
      leaving `Name`/`Slug`/`Taxonomy` as non-pointer strings. In `adminTermCreate`
      keep the existing Name/Slug/Taxonomy-required check (a create names a new
      term), map the field — `var parentID int64; if body.Parent != nil {
      parentID = *body.Parent }` — onto `ParentID` of the `domain.Term` it builds
      (taxonomy from the body), and wrap the `Create` call so
      `content.ErrTermParentCycle`/`ErrTermParentInvalid` become
      `badRequestError{msg: "invalid parent term"}` (→ `400` via `jsonHandler`),
      otherwise returning the error as-is (`ErrForbidden` → `403` unchanged, Req
      5.2). Relax `adminTermUpdate` to load-and-merge: pre-read the current term
      via `s.termWrite.TermsByIDs(r.Context(), []int64{id})` (empty → return
      `domain.ErrNotFound` for the admin-JSON `404`, Req 2.5); replace the
      unconditional `if body.Name == "" || body.Slug == ""` rejection with
      `if body.Name == "" && body.Slug == "" && body.Parent == nil { return
      badRequestError{...} }`; build `merged := cur` (carrying `cur.Taxonomy` and
      `cur.ParentID`), overlay supplied `Name`/`Slug` (non-empty) and
      `ParentID` (`if body.Parent != nil`, else unchanged, Req 2.2/2.3); call
      `Update(merged)` with the same `badRequestError` wrap for the two parent
      sentinels; and echo the merged `Name`/`Slug`/`Taxonomy` (the real pre-read
      taxonomy, not the request body) in `termDetailResponse` (Req 6.2).
      Files: `internal/web/adminapi_terms.go`
      Verify: `go test ./internal/web -run AdminTermParent` — the 4.1 tests pass;
      `go test ./internal/web` green; `go vet ./...` clean. _(Req 1.1, 1.2, 1.3,
      2.1, 2.2, 2.3, 2.5, 3.3, 4.1, 4.2, 4.3, 5.2, 6.2; D1, D2)_

## Phase 5 — Count-after-reparent integration + final gate

- [x] 5.1 Write the count-after-reparent integration test. Add a SQLite
      integration test (at the `internal/web` level over the live chi router, or
      the `storagetest`/archive level, whichever the existing archive-after-write
      harness supports) that seeds a parent category `A`, a second parent category
      `B`, a child category `C` under `A`, and posts filed under `C` — including
      one post filed under **both** `A` and `C`. Read each parent archive, then
      re-parent `C` under `B` through the now-wired update path (REST
      `PUT /categories/{C}` with `parent: B`, or `adminTermUpdate`), and assert on
      the next read: `A`'s archive pagination `Total` drops the moved child's
      posts; `B`'s archive `Total` gains them; the post filed under both `A` and a
      descendant appears **exactly once** (the `EXISTS` semi-join's appears-once
      guarantee); and `CountByTermSlug` for each parent agrees with its archive
      listing. This asserts behavior already wired by M9b, made reachable by
      Phases 1–4 — it adds no production code, proving M9b's descendant-aware
      counts stay correct under re-parent with no write-side bookkeeping.
      Files: `internal/web/rest_terms_parent_test.go` (or a sibling
      `*_reparent_test.go` at the level the archive harness lives)
      Verify: `go test ./internal/web -run Reparent` (or the chosen package's
      archive-reparent test) — green; `go vet ./...` clean. _(Req 6.3, 7.1, 7.2,
      7.3, 7.4, 9.4; D6)_

- [x] 5.2 Final gate — verify no-regression invariants and run the full suite.
      Confirm NO new file under `internal/storage/migrations`
      (`git status --porcelain internal/storage/migrations` shows none, Req 8.1,
      mirroring the sibling specs); confirm `domain.TermWriter`/`domain.TermReader`
      are unchanged — `parent` rides on the existing `domain.Term.ParentID` with
      no new port method (Req 8.2) — and that no new capability name and no new
      `wp-json`/admin route were added (Req 8.3, 8.4); confirm the M9b read model
      (`TermHierarchy`, the `EXISTS` semi-join, `CountByTermSlug`) is unchanged in
      behavior, only proven (Req 8.5). Run `gofmt -l .` (empty), `go vet ./...`,
      `go build ./...`, `go test ./...` and fix anything red, confirming no test
      added by this milestone needs a database, network, live WordPress, or a new
      `GRIMOIRE_TEST_*` env var, and no new dependency (pure Go, SQLite-only for
      the Go tests, Req 9.5).
      Files: (verification only) `internal/storage/migrations/`,
      `internal/domain/repository.go`, `internal/domain/entities.go`
      Verify: `go build ./...` and `go test ./...` green; `git status --porcelain
      internal/storage/migrations` shows no new files; all four gate commands green
      with no new `GRIMOIRE_TEST_*` env var and no new dependency. _(Req 8.1, 8.2,
      8.3, 8.4, 8.5, 9.5; D1, D6, D7)_

## Notes

- Each task cites the exact Requirement/AC, the Inherited decision (D1–D7), and
  (where it clarifies intent) the design Finding it satisfies, for traceability
  back to `requirements.md` and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt -l .` empty and `go vet
  ./...`, `go build ./...`, `go test ./...` green after every phase, matching
  M10a/M10.B/M10.C/M10.F/M11's close-out discipline (Req 9.5). Unlike M11's
  Phase 1, no phase here intentionally leaves the tree red: Phase 1's storage
  edits keep the tree compiling and only change the value written, so every phase
  gate is a true green gate.
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the
  sub-task that writes its failing test (1.1→1.2, 2.1→2.2, 3.1→3.2, 4.1→4.2, and
  5.1 asserts already-wired behavior made reachable by 1–4). There is no
  pure-interface phase to except — M13 adds no port method; the storage change in
  1.2 is the first behavior and it has its failing contract assertion in 1.1.
- The Phase-2 service test reuses the existing combined
  `TermWriter`+`TermReader` `fakeTermWriter` (`writeservices_test.go`), extending
  its seeded `byTaxonomy`/`byIDs` data — a deliberate, called-out reuse of a
  shared fake, not a change to its shape or a drive-by edit, so the archive
  hierarchy tests that also use it stay green.
- **No sub-task is optional** (none is postfixed with `*`). The cross-vendor
  parent round-trip, the service cycle/parent-validation matrix, the REST + admin
  handler `403`/`400` coverage, and the count-after-reparent integration are the
  milestone's done-definition for the hierarchy write (Req 9); each is a core task,
  matching M10.F/M11, whose tests were likewise unmarked.
- **No property-based tests.** The parent rule is a fixed, enumerable set of
  validation branches — top-level (`0`), self-cycle, descendant-cycle,
  nonexistent, cross-taxonomy, flat-taxonomy (`post_tag`), and omitted-unchanged —
  plus a two-table transactional write; there is no meaningful "for all inputs X,
  P(X)" over a large input space to generate against, the interesting cases being
  the branch boundaries which example + integration tests cover exhaustively.
  Coverage is example + integration tests per the design's Testing strategy — pure
  Go, SQLite-only, no new env var or external dependency — matching the no-PBT
  decision in M10.F/M11.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2", "2.1"] },
    { "id": 2, "tasks": ["2.2"] },
    { "id": 3, "tasks": ["3.1", "4.1"] },
    { "id": 4, "tasks": ["3.2", "4.2"] },
    { "id": 5, "tasks": ["5.1"] },
    { "id": 6, "tasks": ["5.2"] }
  ]
}
```
