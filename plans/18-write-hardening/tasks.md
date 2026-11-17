# Implementation Plan: M18 — Write hardening

## Overview

> **This milestone closes four gaps M17 recorded in its Findings 3, 4, 5 and 7,
> all in the built-in write path.** Item 1: rows of an unmanaged post type
> (an imported `shop_order`, an `attachment`, a `nav_menu_item`) can be edited
> and deleted through the admin API, and through revisions, autosave and term
> assignment, by holders of the legacy post capabilities. Item 3: `category` and
> `post_tag` names are stored verbatim. Item 2: deleting a post or page leaves
> `term_relationships`, stale term counts, `postmeta`, `comments`, `commentmeta`
> and dangling `post_parent` values behind. Item 4: a page's `parent` cannot be
> written, so nested page URLs (M15, M16) can only be built in the database.
> No migration, no capability name, no dependency. PR 2 adds one storage port
> (Requirement 16).
>
> **Two pull requests, each a complete increment, each with its own
> `buildHandler` test and a final gate task.**
>
> | PR | Phases | Increment |
> |---|---|---|
> | 1 (security) | 1-8 | Type gate on admin post writes, revisions, autosave, term assignment and the scheduler; plain-text category and tag names with stored names untouched; docs; gate |
> | 2 (completeness) | 9-15 | Delete cleanup (terms and counts, postmeta, comments, re-pointed children and attachments) through one additive port; page `parent` writes over REST and the admin API with guard, nested links and redirects; docs; gate |
>
> PR 2 branches from `main` after PR 1 merges, because both edit
> `internal/content/writeservices.go` and `cmd/grimoire/assembly.go`. PR 1 stands
> alone.
>
> **What the code check changed.** The REST `PUT`/`PATCH`/`DELETE` by-id routes
> for `/posts`, `/pages` and custom types already answer `404
> rest_post_invalid_id` for a row of another type (`internal/web/rest_posts.go:450,538`).
> The hole is the admin API and the services behind it, so the fix is in
> `internal/content` and the REST behavior is pinned by characterization tests
> (task 1.1), not changed. Term names also needed an unchanged-name exception:
> `sanitize.PostTitle` at tier B turns `R&amp;D` into `R&D`, and the term update
> handlers carry the stored name forward on slug-only edits (`design.md`
> Overview 3).
>
> **Strict-TDD spine.** Every behavior-adding implementation task is preceded by
> the task that writes its failing test, and that test is run and seen to fail
> **for the stated reason** before the implementation starts. A test that fails
> only because a new symbol does not compile counts as RED, and the
> implementation task makes it compile and pass. Three tasks are
> characterization or verification tasks with no RED step and say so: 1.1
> (REST pins, fixture, golden confirmation on the unmodified tree), and the two
> docs tasks and final gates.
>
> **Built-in golden handling is explicit.** `cmd/grimoire/testdata/builtin_golden/`
> is expected not to change in either PR: the names in it (`Admin Cat`,
> `Admin Child 2`, …) are fixed points of the name reduction, no golden request
> touches an unmanaged row, and no golden request deletes a post with
> dependents. `TestBuiltinGolden` runs in every phase gate. A diff is a stop. If
> a diff is real and intended, it goes in its own commit, with the cause in the
> message, and is reviewed before merge (Requirement 15.2); `-update` is never
> run to turn a red test green. The new behaviors are covered by new tests, not
> by new golden cases, so the files stay byte-identical across both PRs.
>
> **Standing gate convention.** After every phase: `gofmt -l .` empty, and
> `go vet ./...`, `go build ./...`, `go test ./...` green, following M10a, M11,
> M13 and M17.
>
> **Fixtures.** Assembly tests extend `cmd/grimoire/assembly_custom_fixture_test.go`
> (`newCustomWriteHandler(t, withRegistry)`, `doJSON`, the `customWriteEnv`
> actors and `Repos`/`Prefix` fields). Raw rows are inserted with the SQL
> handle `env.Repos.DB()` and `env.Prefix`, so the tests can create types the
> API cannot. Service tests extend the fakes in
> `internal/content/postwrite_custom_test.go`, `postparent_test.go` and
> `writeservices_test.go`.

## Tasks

## Phase 1 — Baseline: fixture and characterization (`cmd/grimoire`) — PR 1

- [x] 1.1 Add the raw-row fixture and pin today's REST and read behavior
      **before any production change.** In
      `cmd/grimoire/assembly_hardening_fixture_test.go` add
      `seedRawPost(t, env, typ, status string, parent int64) int64` (INSERT into
      `{prefix}posts` with fixed dates and an author), `seedRawMeta`,
      `seedRawComment(t, env, postID int64, approved string) int64` (with one
      `commentmeta` row), and `seedRawTermRelationship(t, env, postID, termID)`,
      plus `rowSnapshot(t, env, id)` returning every `{prefix}posts` column of a
      row for byte comparison. In `cmd/grimoire/assembly_hardening_pins_test.go`
      add `TestBuildHandlerRESTByIDTypePins` over `newCustomWriteHandler(t, true)`
      and a second subtest with `false`: for a seeded `shop_order`, `attachment`
      and `nav_menu_item` row, `PUT`, `PATCH` and `DELETE` through
      `/wp-json/wp/v2/posts/{id}` and `/wp-json/wp/v2/pages/{id}`; for a seeded
      `post` row through `/pages/{id}`, for a seeded `page` row through
      `/posts/{id}`, and for a seeded `shop_order` row through the `product`
      route (Req 3.1-3.3), as administrator: each answers `404
      rest_post_invalid_id` and `rowSnapshot` is unchanged (3.4 asserted by
      sending the same request as an unprivileged `contrib` and expecting the
      same `404`, not `403`). Add `TestBuildHandlerUnwritableReadsPin`: `GET
      /admin/api/posts/{id}` for each seeded row returns `200` with its stored
      `type`, `GET /admin/api/posts` lists them where it does today, and no REST
      `GET` of a built-in route changes (Req 1.6, 3.5). Record the current admin
      results as comments, not assertions, for `PUT`/`DELETE` (they currently
      succeed; that is what Phase 6 turns RED).
      This is a characterization task: every assertion passes on the unmodified
      tree. Run `go test ./cmd/grimoire -run 'BuiltinGolden|TypePins|ReadsPin'`
      green and keep the golden test in every later gate.
      Files: `cmd/grimoire/assembly_hardening_fixture_test.go`,
      `cmd/grimoire/assembly_hardening_pins_test.go`
      Verify: the three tests pass on a clean tree; `git status` shows only the
      two new files. _(Req 1.6, 3.1-3.5, 15.1; D3, D11)_

## Phase 2 — Content: the type gate on post writes (`internal/content`, TDD) — PR 1

- [x] 2.1 Write the failing service tests first. Add
      `internal/content/postwrite_gate_test.go` over a recording `PostWriter`
      fake (counts `Create`/`Update`/`Delete`/`ByID`) and a recording
      `revisionSnapshotter`. Table over the stored types `shop_order`,
      `attachment`, `nav_menu_item`, `revision`, and a type that is registered in
      one registry and absent from the one under test. For each, with an
      administrator principal, assert `Update` and `Delete` return `ErrForbidden`
      and the fake recorded zero snapshots, zero writes and zero deletes (Req
      1.1, 1.2), that `Create` with `Type: "shop_order"` returns `ErrForbidden`
      and writes nothing (1.3), and that the same rows are refused with a nil
      registry (1.7). Assert `post`, `page`, an empty type and a registered
      `product` row still take the existing capability path with unchanged results
      (1.5), and that `PostWriteService.WritableType` agrees with
      `content.WritableType` for every case in the table. Add a table test for
      `WritableType(reg, typ)` itself (`""`, `post`, `page`, registered, absent,
      nil registry).
      Files: `internal/content/postwrite_gate_test.go`
      Verify: `go test ./internal/content -run PostWriteGate` — fails (compile:
      `WritableType` does not exist; once stubbed, `Update` on a `shop_order`
      row succeeds for an administrator). _(Req 1.1-1.3, 1.5, 1.7, 4.3)_

- [x] 2.2 Implement the gate. Create `internal/content/typegate.go` with
      `WritableType(reg *registry.Registry, typ string) bool` as in `design.md`.
      In `internal/content/writeservices.go` add the exported method
      `(*PostWriteService).WritableType(typ string) bool`, and in `Create`
      (after the empty-type default), `Update` and `Delete` (after the `ByID`
      load and empty-type default) return `ErrForbidden` when the type is
      unwritable, before any capability check, snapshot or sanitization. No
      handler changes: the admin handlers already map `ErrForbidden` to `403`.
      Files: `internal/content/typegate.go`, `internal/content/writeservices.go`
      Verify: `go test ./internal/content` — 2.1 passes, every existing
      `postwrite_*`, `writeservices_*` and `postparent_*` test is unchanged and
      green; `go test ./cmd/grimoire -run BuiltinGolden` green. _(Req 1.1-1.3,
      1.5, 1.7; D1)_

## Phase 3 — Content: the sibling by-id services (`internal/content`, TDD) — PR 1

- [x] 3.1 Write the failing tests first. Add
      `internal/content/revisions_gate_test.go`,
      `internal/content/autosave_gate_test.go` and
      `internal/content/postterms_gate_test.go`. Revisions: for a parent row of
      an unwritable type, `List`, `Get` and `Restore` return
      `domain.ErrNotFound` and the recording writer saw no `Update` and no
      `CreateRevision` (Req 2.2); for a registered `capability_type: product`
      parent, a principal holding `edit_posts` but not `edit_products` gets
      `ErrNotFound` and one holding `edit_products` succeeds (2.4); `post` and
      `page` parents behave as before with and without the option (2.5).
      Autosave: the same table for `Save` and `Newer` (2.3, 2.4, 2.5). Term
      assignment: `SetPostTerms` on an unwritable row returns `ErrForbidden` and
      the `PostTermsWriter` fake recorded no call (2.1); built-in and registered
      rows unchanged.
      Files: `internal/content/revisions_gate_test.go`,
      `internal/content/autosave_gate_test.go`,
      `internal/content/postterms_gate_test.go`
      Verify: `go test ./internal/content -run 'RevisionGate|AutosaveGate|PostTermsGate'`
      — fails (compile: the registry options do not exist; once stubbed, an
      unwritable parent is restorable). _(Req 2.1-2.5; D1, D4)_

- [x] 3.2 Implement the gates. In `internal/content/revisions.go` and
      `autosave.go` add `WithRevisionRegistry(reg)` and `WithAutosaveRegistry(reg)`
      with variadic option parameters on `NewRevisionWriteService` and
      `NewAutosaveService` (existing call sites compile unchanged), and rewrite
      both `authorizeParent` methods to call `WritableType` then
      `CapabilitySuffixFor` and `auth.CanEditPostSuffix`, returning
      `domain.ErrNotFound` for either failure. In `internal/content/postterms.go`
      make `SetPostTerms` return `ErrForbidden` for an unwritable stored type
      after the load. Leave `CheckAssignable` unchanged.
      Files: `internal/content/revisions.go`, `internal/content/autosave.go`,
      `internal/content/postterms.go`
      Verify: `go test ./internal/content` — 3.1 passes, existing
      `revisions_test.go`, `autosave_test.go` and `postterms_custom_test.go` green;
      golden green. _(Req 2.1-2.5; D1, D4)_

## Phase 4 — Scheduler: skip unwritable rows (`internal/scheduler`, TDD) — PR 1

- [x] 4.1 Write the failing tests first. Add
      `internal/scheduler/scheduler_gate_test.go`: a fake `postUpdater` that also
      implements `WritableType(string) bool` returning false for `shop_order`; a
      tick over `DueScheduled` rows `{post, shop_order, product}` calls `Update`
      for `post` and `product` only, logs the skipped row at debug level and never
      at error (Req 4.1, 4.2); a fake without the method receives `Update` for
      every row, as before (4.3).
      Files: `internal/scheduler/scheduler_gate_test.go`
      Verify: `go test ./internal/scheduler -run Gate` — fails: `Update` is called
      for the `shop_order` row. _(Req 4.1-4.3)_

- [x] 4.2 Implement the skip. In `internal/scheduler/scheduler.go` `tick`, before
      `Update`, assert `s.posts.(interface{ WritableType(string) bool })`; when it
      holds and returns false, log at debug with the post id and type and
      `continue`. Do not import `internal/content`.
      Files: `internal/scheduler/scheduler.go`
      Verify: `go test ./internal/scheduler` — 4.1 passes, `scheduler_test.go` and
      `scheduler_suffix_test.go` green. _(Req 4.1-4.3)_

## Phase 5 — Content: term names for every taxonomy (`internal/content`, TDD) — PR 1

- [x] 5.1 Write the failing service tests first. Add
      `internal/content/termname_test.go` over the existing `fakeTermWriter`
      (extend only its seed data and add a read counter). Cases for `category`,
      `post_tag` and one registered taxonomy, each asserting what the writer
      received: `Create` of `<script>x</script>` → `ErrTermNameEmpty`, writer not
      called (Req 5.3); `Create` of `<b>Bold</b> & co` → stored `Bold & co`
      (5.1); an actor holding `unfiltered_html` gets the same reduction (5.4);
      `Update` with a changed name is reduced (5.2); **`Update` with a caller name
      byte-identical to the stored `News &amp; Events` writes `News &amp; Events`
      and the writer received it unchanged (6.1)**, including a parent-only and a
      slug-only update; `Update` whose stored read fails returns that error and
      the writer is not called (6.3); the stored read happens exactly once per
      update (6.4); an unauthorized caller gets `ErrForbidden` with zero reads and
      zero writes before any `ErrTermNameEmpty` (5.8); `category` and `post_tag`
      slugs are accepted exactly as before, including an uppercase and a
      percent-encoded slug and a duplicate sibling slug (5.6); the registered
      taxonomy's charset and sibling-uniqueness rules still apply (5.7). Add a
      table test that `Create` for `category` with a plain name such as `Admin
      Cat` is written byte-identically (Requirement 15.1).
      Files: `internal/content/termname_test.go`
      Verify: `go test ./internal/content -run TermName` — fails: `<script>x</script>`
      is written verbatim for `category`, and the update path performs no stored
      read. _(Req 5.1-5.8, 6.1, 6.3, 6.4; D5)_

- [x] 5.2 Implement the term service change. In
      `internal/content/writeservices.go` replace `prepareRegistered` with
      `prepare(ctx, t, stored *domain.Term)` as in `design.md`: reduce the name for
      every taxonomy unless `stored != nil && t.Name == stored.Name`, then run
      `checkSlug` for registered taxonomies only. `Create` calls `prepare(ctx, t,
      nil)`; `Update` performs one `TermsByIDs([]int64{t.ID})` after the
      capability check, returns `domain.ErrNotFound` for no row and the read error
      otherwise, then calls `prepare(ctx, t, &stored)`. Keep the check order
      (capability, vocabulary, name and slug, parent, write). Do not touch the web
      handlers or `buildHandler`: the handlers already map `ErrTermNameEmpty` to
      `400` and echo `storedTerm`.
      Files: `internal/content/writeservices.go`
      Verify: `go test ./internal/content` — 5.1 passes, `termcustom_test.go`,
      `termreparent_test.go` and `writeservices_test.go` green;
      `go test ./cmd/grimoire -run BuiltinGolden` **green with no diff** (a diff
      is a stop; see Overview). _(Req 5, 6, 15.1; D5, D11)_

## Phase 6 — Assembly: wire PR 1 and drive it through `buildHandler` (`cmd/grimoire`, TDD) — PR 1

- [x] 6.1 Write the failing assembly tests first. Add
      `cmd/grimoire/assembly_hardening_test.go` with
      `TestBuildHandlerUnwritableTypes` over `newCustomWriteHandler` (registry on,
      and a second run with no registry for Req 1.7): for seeded `shop_order`,
      `attachment` and `nav_menu_item` rows, as administrator, `PUT` and `DELETE
      /admin/api/posts/{id}` answer `403` and `rowSnapshot` is unchanged and the
      row still exists (Req 1.4); `POST /admin/api/posts` with `type: shop_order`
      is `400` (existing); `PUT` with `termIds` leaves `term_relationships`
      unchanged; `GET /admin/api/posts/{id}/revisions`, `.../revisions/{rid}`,
      `POST .../revisions/{rid}/restore`, `GET`/`POST .../autosave` answer `404`
      and write no row (Req 2.1-2.3); a `capability_type: product` row's revisions
      are `404` for the `edit_posts`-only caller and succeed for the `products`
      actor (2.4); `post` and `page` rows behave as before. Add
      `TestBuildHandlerSchedulerSkipsUnwritable`: a `future` `shop_order` row with
      a past date and a `future` `post` row are both due; a scheduler built like
      `main` publishes the post, leaves the order untouched and logs no error
      (Req 4.1, 4.2). Add `TestBuildHandlerBuiltinTermNames`: REST `POST
      /wp-json/wp/v2/categories` and `/tags` with `<b>Bold</b> & co` return `201`
      with name `Bold & co`; with `<em></em>` return `400 rest_invalid_param`;
      admin `POST /admin/api/terms` the same with `400 bad_request`; a seeded
      category whose stored name is `News &amp; Events` stays `News &amp; Events`
      after a REST slug-only `PATCH`, an admin parent-only `PUT`, and is returned
      verbatim by `GET /wp-json/wp/v2/categories`, `GET /admin/api/terms` and its
      public archive page (Req 5, 6.1, 6.2); a `contrib` caller gets `403` before
      any `400` (5.8). No built-in golden file is changed.
      Files: `cmd/grimoire/assembly_hardening_test.go`
      Verify: `go test ./cmd/grimoire -run 'UnwritableTypes|SchedulerSkips|BuiltinTermNames'`
      — the term and scheduler tests pass or fail per Phases 2-5; the revision and
      autosave subtests of `TestBuildHandlerUnwritableTypes` fail (`200`) because
      `buildHandler` builds those two services without the registry. _(Req 1, 2,
      4, 5, 6, 17.1, 17.3, 17.5; D1)_

- [x] 6.2 Wire the registry options. In `cmd/grimoire/assembly.go` pass
      `content.WithRevisionRegistry(deps.Registry)` to
      `NewRevisionWriteService` and `content.WithAutosaveRegistry(deps.Registry)`
      to `NewAutosaveService`. No other assembly change is needed: the post write
      service and the post-terms service already carry the registry, and the term
      service already carries the policy (Requirement 17.5).
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 6.1 passes. **Proof of wiring:** remove
      each of the two options locally in turn; `TestBuildHandlerUnwritableTypes`
      must fail on the revision subtest (first option) and the autosave subtest
      (second); restore both and record the result in the PR description. Revert
      the 5.2 service change locally once and confirm
      `TestBuildHandlerBuiltinTermNames` fails (Requirement 17.5). Golden green.
      _(Req 2.6, 17.1, 17.4, 17.5; D3)_

## Phase 7 — Documentation for PR 1 (docs only) — PR 1

- [x] 7.1 Update the documents to match PR 1. `docs/compatibility.md`: in the
      "M17 — Custom type write" section, rewrite the **Sanitization** bullet (built-in
      term names are now reduced to plain text, an unchanged name is stored as is,
      entity decoding note) and the **Known gaps** bullet (remove "stays editable
      through the admin API"; keep the page `parent` clause for PR 2); add an
      "M18 — Write hardening (PR 1)" section covering: the writable-type rule and
      the three surfaces' responses (`403` admin write, `404` revisions and
      autosave, `404` REST), reads unchanged, the scheduler skip, a registry entry
      removed from `content:` makes its rows unwritable, revisions and autosave now
      honor `capability_type`, term-name reduction and its tier, and the term-side
      gap (unwritable taxonomies on term update/delete) as still open.
      `plans/post-m10-gap-analysis/README.md`: in "Still deferred after M17" remove
      the entries "unregistered stored types remain editable with post capabilities
      through the admin API" and "built-in `category`/`post_tag` term names are not
      sanitized on write" and add a one-paragraph "Shipped in M18 (PR 1)" note.
      `plans/wordpress-core-parity-roadmap/tasks.md`: in the 12.C shipped note,
      delete "unregistered stored types stay editable with post capabilities" and
      "built-in term name sanitizing" from the Deferred list and append "(closed by
      `18-write-hardening`)". `plans/README.md`: add the `18-write-hardening` row
      after `17-custom-types-write`, status "PR 1 of 2 shipped", with the one-line
      summary. Search `grep -rn "shop_order\|not sanitized\|stays editable" docs
      README.md plans` and fix every statement this PR makes stale, leaving
      historical statements inside `plans/17-custom-types-write` untouched.
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`,
      `plans/wordpress-core-parity-roadmap/tasks.md`, `plans/README.md`
      Verify: `grep -n "not sanitized at the write" docs/compatibility.md` finds
      nothing; `grep -n "18-write-hardening" plans/README.md` shows the row;
      `go build ./...` unaffected. _(Req 19.1, 19.3, 19.4, 19.5)_

## Phase 8 — PR 1 final gate

- [x] 8.1 Final gate for PR 1. Confirm: no new file under
      `internal/storage/migrations` (`git status --porcelain
      internal/storage/migrations` empty, Req 16.1); no change to
      `internal/domain/repository.go` and no new file under `internal/storage`
      (Req 16.3, 16.4); no new capability string
      (`git diff main | grep -E '^\+.*"(manage|edit|publish|delete)_[a-z_]+"'`
      reviewed: only suffix-derived names built at runtime); `go.mod` and `go.sum`
      unchanged (16.2); no new `GRIMOIRE_TEST_*` variable (16.5); the built-in
      golden files unchanged since task 1.1 (`git diff main --
      cmd/grimoire/testdata/builtin_golden` empty, 15.1). Run `gofmt -l .` (empty),
      `go vet ./...`, `go build ./...`, `go test ./...` and, once, `go test -race
      ./internal/content ./internal/web ./internal/scheduler ./cmd/grimoire`. Fix
      anything red in the phase that owns it. Ensure all tests pass, ask the user
      if questions arise.
      Files: (verification only)
      Verify: all gate commands green; the git checks show no violation.
      _(Req 15, 16, 18.2, 18.5)_

## Phase 9 — Storage: the post-dependents port (`internal/domain`, `internal/storage`, TDD) — PR 2

- [x] 9.1 Write the failing contract tests first. Add
      `internal/storage/storagetest/postdependents_contract.go` with
      `runPostDependentsPurgerContract(t, newRepos)` and register it in
      `contract.go` beside `runPostNodeReaderContract`. Seed through raw SQL
      helpers in the storagetest style: a parent page `P` (with its own parent
      `G`), two child pages of `P`, a child post of `P` (other type), an
      attachment of `P`, comments on `P` in `comment_approved` `1`, `0`, `spam`
      and `trash` each with a `commentmeta` row, three `postmeta` rows on `P`, and
      a second page `Q` with its own comment, commentmeta and postmeta. Assert, for
      `PurgePostDependents(ctx, P, G, "page")`: both child pages and the attachment
      now have `post_parent = G` (Req 9.1, 9.2); the child post still has
      `post_parent = P` (9.3); `post_modified` and every other column of the
      re-pointed rows are unchanged (9.4); `P`'s comments, commentmeta and
      postmeta are gone (8.1-8.3); `Q`'s are untouched (8.4); `P` itself still
      exists (8.6); with `reparentType == ""` the child pages keep `post_parent = P`
      and the attachment is still re-pointed (10.4); a purge of an id with no
      dependents and of a nonexistent id returns nil (10.6); calling the same purge
      twice succeeds (10.3); with `parentID == 0` rows are re-pointed to `0` (9.6);
      Atomicity (8.5, 9.5) has no portable failure injection across the three
      engines, so it is tested on SQLite only, in `internal/storage/wprepo/postdependents_test.go`:
      drop the `commentmeta` table, call the method, and assert it returns an error and
      the re-pointed children, the comments and the postmeta of `P` are all unchanged
      (the statement order puts both updates before the failing delete). The suite runs on SQLite through
      `sqlite_test.go` and on MySQL and PostgreSQL through the existing env-gated
      `mysql_test.go` and `postgres_test.go` (10.7).
      Files: `internal/storage/storagetest/postdependents_contract.go`,
      `internal/storage/storagetest/contract.go`,
      `internal/storage/wprepo/postdependents_test.go`
      Verify: `go test ./internal/storage/storagetest ./internal/storage/wprepo -run PostDependents` — fails
      (compile: `repos.PostDependents` and the port do not exist). _(Req 8.1-8.6,
      9.1-9.6, 10.3-10.7; D7, D12)_

- [x] 9.2 Implement the port. In `internal/domain/repository.go` add
      `PostDependentsPurger` as in `design.md` with its doc comment. Create
      `internal/storage/wprepo/postdependents.go` with `(*PostRepo)
      PurgePostDependents` in one `RunInTx` using raw statements in the style of
      `SetPostTerms`: the two `UPDATE … SET post_parent` statements, then
      `DELETE FROM {p}commentmeta WHERE comment_id IN (SELECT comment_ID FROM
      {p}comments WHERE comment_post_ID = ?)`, `DELETE FROM {p}comments WHERE
      comment_post_ID = ?`, `DELETE FROM {p}postmeta WHERE post_id = ?`. Add
      `_ domain.PostDependentsPurger = (*PostRepo)(nil)` beside
      `wprepo/writers.go:259`. In `internal/storage/factory.go` add
      `PostDependents domain.PostDependentsPurger` to `Set` and set it to `posts`
      next to `PostNodeReader`.
      Files: `internal/domain/repository.go`,
      `internal/storage/wprepo/postdependents.go`,
      `internal/storage/wprepo/writers.go`, `internal/storage/factory.go`
      Verify: `go test ./internal/storage/...` — 9.1 passes on SQLite; with
      `GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN` set locally, the
      same contract passes on both (record the run or state they were not
      available); every existing contract is unchanged. `go build ./...` shows no
      other `Set` construction breaks. _(Req 8, 9, 10.4-10.7, 16.3; D7)_

## Phase 10 — Content: the delete sequence (`internal/content`, TDD) — PR 2

- [x] 10.1 Write the failing service tests first. Add
      `internal/content/postdelete_cascade_test.go` over a call-recording
      `PostWriter` that also implements `RevisionWriter`, a recording
      `PostTermsWriter` (detacher) and a recording `PostDependentsPurger`, all
      appending to one ordered log. For `post`, `page`, a registered flat type and
      a registered hierarchical type, assert the log is exactly: capability →
      `DeleteRevisionsOf` → one `SetPostTerms(id, tax, nil)` per expected taxonomy
      → `PurgePostDependents(id, cur.ParentID, reparentType)` → `Delete` (Req
      10.1). Expected taxonomies: `category` and `post_tag` for `post` and `page`
      plus any registered taxonomy whose `ObjectTypes` names the type (7.1, 7.3);
      the registry's effective `Taxonomies` for a registered type (7.2); never a
      duplicate. Expected `reparentType`: `page` and the registered hierarchical
      type, empty for `post` and the flat type (9.1, 9.3). For each step in turn
      make it fail and assert the later steps are not in the log and the error is
      returned (10.2). With no detacher configured the term step is skipped and
      with no purger the purge is skipped, and the remaining steps still run
      (7.6, 10.8). An unwritable row is refused before any step (1.2, regression).
      Files: `internal/content/postdelete_cascade_test.go`
      Verify: `go test ./internal/content -run DeleteCascade` — fails (compile:
      `WithPostDependentsPurger` does not exist; once stubbed, a `post` delete logs
      no term detach). _(Req 7.1-7.6, 9.1, 9.3, 10.1, 10.2, 10.8)_

- [x] 10.2 Implement the delete sequence. In `internal/content/writeservices.go`
      add `WithPostDependentsPurger(domain.PostDependentsPurger) PostWriteOption`,
      the `purger` field, `deleteTaxonomies(typ)` and `hierarchicalType(typ)`, and
      rewrite the tail of `Delete`: revisions (existing), then every taxonomy of
      `deleteTaxonomies(cur.Type)` through `s.detacher` (replacing the
      registered-only branch), then `PurgePostDependents(ctx, cur.ID, cur.ParentID,
      reparent)`, then `s.w.Delete`. Update the doc comment of `Delete` to state
      the order and the idempotence.
      Files: `internal/content/writeservices.go`
      Verify: `go test ./internal/content` — 10.1 passes; the M17 delete tests in
      `postwrite_custom_test.go` pass unchanged (registered types still detach
      their `Taxonomies`); golden green. _(Req 7, 8, 9, 10.1-10.3; D6, D10)_

## Phase 11 — Content: the page parent guard (`internal/content`, TDD) — PR 2

- [x] 11.1 Write the failing service tests first. Add
      `internal/content/pageparent_test.go` over an in-memory `PostWriter` plus
      `PostNodeReader` fake seeded with pages `A` (top), `B` (parent `A`), `C`
      (parent `B`), `D` (top, slug `x`), `E` (parent `A`, slug `x`), a post `P` and
      a `product` row. Assert, with an administrator and `WithPostNodeReader`:
      `Create` of a page with parent `A` stores it (Req 13.1); parent `0` is
      top-level; `Update` of `A` with parent `A` → `ErrPostParentCycle` (13.2);
      `Update` of `A` with parent `C` → `ErrPostParentCycle` (13.3); parent `P`
      (a post) and parent `product` row and parent `9999` → `ErrPostParentInvalid`
      (13.4); an update whose parent equals the stored value does not read the node
      reader, including a page whose stored parent is `9999` (13.5); an update that
      moves `D` under `A` while `E` already holds slug `x` stores `x-2` (13.6); a
      create under `A` with slug `x` stores `x-2`; an update that does not change
      the parent keeps the caller's slug even when it duplicates a sibling (13.7);
      a percent-encoded slug (`caf%C3%A9`) is accepted on a move (no charset rule,
      13.6); a non-zero parent with no node reader returns an error and writes
      nothing (13.8); the check runs after the capability check, so a contributor
      gets `ErrForbidden` before `ErrPostParentInvalid` (13.9); `post` ignores a
      parent change (D8); the writer is not called on any rejection.
      Files: `internal/content/pageparent_test.go`
      Verify: `go test ./internal/content -run PageParent` — fails: a page parent
      change is silently ignored, so every rejection case writes. _(Req 13.1-13.9;
      D8, D9)_

- [x] 11.2 Implement the guard. In `internal/content/writeservices.go` add
      `parentRules(typ)` (synthetic `registry.PostType{Name: "page",
      Hierarchical: true}` for `page`, else `registered`) and use it in `Create`
      and `Update` for the parent step only; add the uniqueness-only slug step
      (`slugTaken` over `loadPostNodes(…, "page")`, suffix loop, no `slugRE`) run
      when a page's parent changes or a page is created with a non-zero parent.
      Leave the M17 `registered` branches for slug and detach untouched so `page`
      never enters the charset path. Reuse `validatePostParent` unchanged.
      Files: `internal/content/writeservices.go`
      Verify: `go test ./internal/content` — 11.1 passes; `postparent_test.go` (M17)
      and every other `content` test unchanged and green; golden green.
      _(Req 13; D8, D9)_

## Phase 12 — Web: page parent on REST and the admin API (`internal/web`, TDD) — PR 2

- [x] 12.1 Write the failing handler tests first. Add
      `internal/web/rest_pages_parent_test.go` and
      `internal/web/adminapi_pages_parent_test.go` over real chi routers with the
      existing fake post services. REST: `POST /wp-json/wp/v2/pages` with `parent`
      passes it to `Create` and answers `201` with the stored `parent` (Req 11.1);
      `PUT`/`PATCH` with `parent` passes it (11.2); an omitted `parent` passes the
      stored value (11.3); `parent: 0` passes `0` (11.4); the service's
      `ErrPostParentInvalid` and `ErrPostParentCycle` map to `400 rest_invalid_param`
      (11.5); `ErrForbidden` maps to `403 rest_cannot_edit` (11.6); `parent` on
      `/wp-json/wp/v2/posts` is ignored (11.7). Admin: `POST` and `PUT` with `type:
      page` and `parent` pass it (12.1); **a `PUT` for a stored page whose body
      omits `type` and carries `parent` still passes it** (12.2); the detail
      response for a page carries `parent`, omitted when `0` (12.3); the sentinels
      map to `400 bad_request` with `invalid parent` (12.4); `type: post` ignores
      `parent` (12.5).
      Files: `internal/web/rest_pages_parent_test.go`,
      `internal/web/adminapi_pages_parent_test.go`
      Verify: `go test ./internal/web -run 'PagesParent'` — fails: the fake service
      receives the stored parent, not the requested one, on every page case.
      _(Req 11.1-11.7, 12.1-12.5)_

- [x] 12.2 Implement the handler changes. In `internal/web/rest_posts.go`
      `parseRESTPostWrite` replace the registered-only parent test with
      `typ == "page" || (s.isRegisteredPostType(typ) && typ != "post")`. In
      `internal/web/adminapi_posts.go` `parsePostWrite` compute the effective type
      as `current.Type` when `current != nil` and `typ` otherwise, apply
      `body.Parent` when the effective type is `page` or registered, and extend the
      `postDetail` parent population (`:187`) to `page`.
      Files: `internal/web/rest_posts.go`, `internal/web/adminapi_posts.go`
      Verify: `go test ./internal/web` — 12.1 passes; `adminapi_posts_parent_test.go`,
      `rest_posts_write_test.go` and `rest_custom_posts_write_test.go` unchanged
      and green; golden green. _(Req 11, 12; D8)_

## Phase 13 — Assembly: wire PR 2 and drive it through `buildHandler` (`cmd/grimoire`, TDD) — PR 2

- [x] 13.1 Write the failing assembly tests first. Add
      `cmd/grimoire/assembly_hardening_delete_test.go` and
      `assembly_hardening_pageparent_test.go`. **Delete:** seed a published post
      with two category/tag assignments, three postmeta rows, comments in four
      states with commentmeta, and an attachment child; delete it through `DELETE
      /admin/api/posts/{id}` and `DELETE /wp-json/wp/v2/posts/{id}` (two posts):
      afterwards no `term_relationships` row, every touched `term_taxonomy.count`
      reduced by one, no `postmeta`, `comments` or `commentmeta` row for it, the
      attachment's `post_parent` equals the deleted post's parent, and a second
      post's rows are untouched (Req 7.1, 7.4, 8, 9.2). Repeat for a page with two
      child pages and a child post: children re-pointed to the page's parent, the
      child post unchanged (9.1, 9.3), through admin and REST. Repeat for a
      registered hierarchical `guide` row. A deleted row with a failing purger
      (wrap `repos.PostDependents` with a failing fake in a second subtest built
      through `assemblyHandler`) answers `500` and the post row still exists, and a
      retry with the working purger succeeds (10.2, 10.3). **Page parent:** create
      pages `a` and `b` as published; `PUT` `b` with `parent` `a` over REST: `200`,
      response `parent` is `a`'s id and `link` is `/a/b/` (14.1); `GET /a/b/` →
      `200`; `GET /b/` → `301` to `/a/b/` (14.2); a draft page moved the same way
      answers `404` on both paths (14.3); two published pages `x` under `a` and
      `x` under `c` each serve their own body (14.4); a cycle, a post as parent and
      a missing parent are `400` on both surfaces with the row unchanged (11.5,
      12.4); the same moves through the admin API with a body that omits `type`
      (12.2); `parent: 0` returns the page to the top level and `/b/` serves `200`;
      a contributor gets `403`. No built-in golden file is changed.
      Files: `cmd/grimoire/assembly_hardening_delete_test.go`,
      `cmd/grimoire/assembly_hardening_pageparent_test.go`
      Verify: `go test ./cmd/grimoire -run 'HardeningDelete|HardeningPageParent'` —
      the delete test fails (term rows, meta and comments remain; children keep a
      dangling parent) because `buildHandler` builds the post service without the
      purger; the page-parent test passes or fails per Phases 11-12 (the node
      reader is already wired). _(Req 7-14, 17.2, 17.3; D6, D8)_

- [x] 13.2 Wire the purger. In `cmd/grimoire/assembly.go` add
      `content.WithPostDependentsPurger(repos.PostDependents)` to the
      `NewPostWriteService` call. No other assembly change: `WithTermDetacher` and
      `WithPostNodeReader` are already present.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 13.1 passes. **Proof of wiring:** remove
      `WithPostDependentsPurger` locally; the delete test must fail on the
      comments/meta assertions; restore. Remove `WithPostNodeReader` locally; the
      page-parent test must fail on the `400`-not-`500` assertions (the guard fails
      closed with `errPostNodesUnavailable`); restore. Record both in the PR
      description. Golden green. _(Req 17.2, 17.4; D3)_

## Phase 14 — Documentation for PR 2 (docs only) — PR 2

- [x] 14.1 Update the documents to match PR 2. `docs/compatibility.md`: in the M17
      section remove "Page `parent` writes are not part of this milestone", the
      sentence "built-in post and page deletion keeps its current behavior (no term
      cleanup, postmeta or comments left behind)" and the remaining clause of
      **Known gaps** (page `parent` is not writable); extend the M18 section with
      "PR 2": delete cleanup (what is removed, what is re-pointed, the order, the
      idempotence, permanent delete unchanged), page `parent` (REST and admin,
      guard, slug suffix on a move, `post` ignores `parent`, URL behavior: nested
      link, `301` for a published page, `404` for a draft, same-slug pages), and
      the gaps still open: `sticky_posts`, nav-menu items that reference a deleted
      post, WordPress hooks and object cache, trash. `plans/post-m10-gap-analysis/README.md`:
      remove "page `parent` has no write path" and "built-in post delete does not
      clean up `term_relationships`, counts, postmeta or comments" from "Still
      deferred after M17", leaving only "delete is permanent, with no trash or
      child re-parenting" reworded to "no trash" (children are now re-pointed), and
      add the "Shipped in M18 (PR 2)" paragraph. `plans/wordpress-core-parity-roadmap/tasks.md`:
      remove "page `parent` writes" and "built-in delete cleanup" from the 12.C
      Deferred list and reword "trash/re-parent delete semantics" to "trash".
      `plans/README.md`: set the `18-write-hardening` row to "✅ Implemented".
      Search `grep -rn "re-parent\|page .parent\|left behind" docs README.md plans`
      and fix each statement this PR makes stale, leaving `plans/17-custom-types-write`
      untouched.
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`,
      `plans/wordpress-core-parity-roadmap/tasks.md`, `plans/README.md`
      Verify: `grep -n "Page .parent. writes are not" docs/compatibility.md` finds
      nothing; the plans README row reads Implemented; `go build ./...` unaffected.
      _(Req 19.2-19.5)_

## Phase 15 — PR 2 final gate

- [x] 15.1 Final gate for PR 2. Confirm: no new file under
      `internal/storage/migrations` (Req 16.1); exactly one interface added to
      `internal/domain/repository.go` and no method added to an existing one
      (`git diff main -- internal/domain/repository.go` reviewed, 16.3); no new
      capability string (reviewed as in 8.1); `go.mod` and `go.sum` unchanged;
      no new `GRIMOIRE_TEST_*` variable; the built-in golden files unchanged since
      task 1.1 (`git diff <PR 1 merge base> -- cmd/grimoire/testdata/builtin_golden`
      empty, 15.1). Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`,
      `go test ./...`, and `go test -race ./internal/content ./internal/web
      ./internal/storage/... ./cmd/grimoire`. If a MySQL or PostgreSQL DSN is
      available, run `go test ./internal/storage/storagetest` with the
      `GRIMOIRE_TEST_*` variables set and record the result. Fix anything red in
      the phase that owns it. Ensure all tests pass, ask the user if questions
      arise.
      Files: (verification only)
      Verify: all gate commands green; the git checks show no violation.
      _(Req 15, 16, 18.2, 18.5)_

## Notes

- Tasks marked with `*` are optional; **no sub-task here is optional**. The type
  gate matrix, the term-name rules including the unchanged-name exception, the
  delete order and failure cases, the purger contract, the parent guard branches
  and the assembly tests are the milestone's done-definition (Requirement 18).
- **No property-based tests.** The behaviors are enumerable branches over fixed
  fixtures; the reduction itself is the `sanitize` package's already-tested
  responsibility. See `design.md` Testing strategy.
- **TDD exceptions are explicit:** 1.1 (characterization and fixture), 7.1 and
  14.1 (docs), 8.1 and 15.1 (gates).
- **Wiring is tested where it is wired.** Tasks 6.1/6.2 and 13.1/13.2 each pair a
  failing `buildHandler` test with the `assembly.go` edit, and each `Verify`
  line requires removing the option once to prove the test guards it. Item 3
  needs no new option; 6.2 proves the `buildHandler` test fails if the service
  change is reverted instead.
- **No golden file changes.** If `TestBuiltinGolden` fails in any phase, stop and
  find the cause; do not pass `-update`. A deliberate, reviewed regeneration, if
  one is ever justified, is its own commit.
- **Stale-assertion edits are narrow.** No existing test should need to change.
  If one asserts that a post delete leaves a term relationship, or that a page
  `parent` is ignored, change only that assertion in the phase that owns the
  behavior and say so in the commit.
- **Open sign-off items** are listed in `design.md` "Decisions awaiting
  sign-off". Defaults are implemented as written; reversing one is local to the
  task named in each item.
- **Not done here, recorded as gaps:** unwritable taxonomies on term update and
  delete (the term-side twin of item 1), `sticky_posts` cleanup, nav-menu items
  that reference a deleted post, trash, and moving page validation ahead of the
  revision snapshot.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["2.1", "3.1", "4.1", "5.1", "6.1"] },
    { "id": 2, "tasks": ["2.2", "4.2"] },
    { "id": 3, "tasks": ["3.2", "5.2"] },
    { "id": 4, "tasks": ["6.2"] },
    { "id": 5, "tasks": ["7.1"] },
    { "id": 6, "tasks": ["8.1"] },
    { "id": 7, "tasks": ["9.1", "10.1", "11.1", "12.1", "13.1"] },
    { "id": 8, "tasks": ["9.2"] },
    { "id": 9, "tasks": ["10.2"] },
    { "id": 10, "tasks": ["11.2", "12.2"] },
    { "id": 11, "tasks": ["13.2"] },
    { "id": 12, "tasks": ["14.1"] },
    { "id": 13, "tasks": ["15.1"] }
  ]
}
```
