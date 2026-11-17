# Implementation Plan: M19 — Term-side write gate and delete parity

## Overview

> **This milestone closes four gaps M18 recorded as open.** Item 1: a holder of
> `manage_categories` can rename or delete terms of taxonomies grimoire does not
> manage (an imported `nav_menu`, `product_cat`, `link_category`), and the admin
> `termIds` map can write post relationships in any taxonomy. Item 2: REST page
> items omit `parent` when it is `0`, where WordPress always sends it. Item 3:
> deleting a sticky post leaves its id in `sticky_posts`. Item 4: deleting a post,
> page or custom-type row leaves the nav-menu items that point at it. No
> migration, no capability name, no dependency. PR 3 adds one storage port
> (Requirement 9). PR 1 and PR 2 add none.
>
> **Three pull requests, each a complete increment, each with its own
> `buildHandler` test and a final gate task.**
>
> | PR | Phases | Increment |
> |---|---|---|
> | 1 (security) | 1-4 | Taxonomy gate on term update, term delete and post-term assignment, in the service layer; REST and menu-editor behavior pinned; docs; gate |
> | 2 (parity) | 5-8 | `sticky_posts` cleanup on post delete through the existing option ports; `parent` always on REST pages with a deliberate, diff-checked golden regeneration; docs; gate |
> | 3 (nav menus) | 9-12 | Menu items of a deleted post removed with their meta and relationships, menu counts recomputed, through one additive port with a contract proven on SQLite, MySQL and PostgreSQL; docs; gate |
>
> PR 2 branches from `main` after PR 1 merges, and PR 3 after PR 2, because they
> edit `internal/content/writeservices.go` and `cmd/grimoire/assembly.go` in turn.
> PR 1 stands alone.
>
> **What the code check changed.** `TermWriteService.Create` already refuses an
> unmanaged taxonomy (`ErrTaxonomyUnknown`, `400`), and every REST term route
> already answers `404 rest_term_invalid_id` for a term of another taxonomy. The
> holes are term update and delete by id on the admin API, and post-term
> assignment for built-in rows, so the fix is in `internal/content` and REST is
> pinned (task 1.1), not changed. `sticky_posts` needs no new port: the option
> ports exist. Only the menu-item cleanup needs one (`design.md` Overview 1-7).
>
> **Strict-TDD spine.** Every behavior-adding implementation task is preceded by
> the task that writes its failing test, and that test is run and seen to fail
> **for the stated reason** before the implementation starts. A test that fails
> only because a new symbol does not compile counts as RED, and the
> implementation task makes it compile and pass. The characterization and
> verification tasks have no RED step and say so: 1.1, the docs tasks and the
> final gates.
>
> **Golden handling.** `cmd/grimoire/testdata/builtin_golden/` is expected not to
> change in PR 1 and PR 3. In PR 2 it changes **once**, in its own commit, and the
> only allowed diff is an added `"parent": 0` line on page objects (task 6.1
> writes the check, task 6.2 regenerates and runs it). `TestBuiltinGolden` runs in
> every phase gate. Any other diff is a stop, not a regeneration. `-update` is
> never run to turn a red test green.
>
> **Storage handling (PR 3).** The M18 purger contract first hardcoded a table
> prefix and failed CI on MySQL and PostgreSQL after passing on SQLite. The M19
> contract takes `storagetest.NewReposWithPrefixFunc`, reads the vendor and
> prefix it returns, and is verified against throwaway MySQL 8.0 and PostgreSQL 16
> containers before the PR opens (task 9.2 and the PR 3 gate).
>
> **Standing gate convention.** After every phase: `gofmt -l .` empty, and
> `go vet ./...`, `go build ./...`, `go test ./...` green.
>
> **Fixtures.** Assembly tests extend `cmd/grimoire/assembly_hardening_fixture_test.go`
> (`seedRawPost`, `seedRawMeta`, `seedRawComment`, `rowSnapshot`) and
> `assembly_custom_fixture_test.go` (`newCustomWriteHandler(t, withRegistry)`,
> `doJSON`, the `customWriteEnv` actors, `Repos` and `Prefix`). M19 adds
> `seedRawTerm`, `seedRawTermRelationship` and `seedRawMenuItem` there. Service
> tests extend the fakes in `internal/content/writeservices_test.go`,
> `postwrite_gate_test.go` and `postterms_gate_test.go`.

## Tasks

## Phase 1 — Baseline: fixture, inventory and characterization (`cmd/grimoire`) — PR 1

- [x] 1.1 Add the raw-term fixture, take the term write-path inventory and pin
      today's behavior **before any production change.** In
      `cmd/grimoire/assembly_hardening_fixture_test.go` add
      `seedRawTerm(t, env, name, slug, taxonomy string, parent int64) int64` (INSERT
      into `{prefix}terms` and `{prefix}term_taxonomy` through `env.Repos.DB()`
      and `env.Prefix`), `seedRawTermRelationship(t, env, postID, termID int64)`,
      `termSnapshot(t, env, termID)` returning the `terms`, `term_taxonomy` and
      relationship rows of a term for byte comparison, and a menu helper
      `seedRawMenuItem(t, env, menuTermID int64, objectType string, objectID int64) int64`
      (used from PR 3). Run `grep -rn "termWrite\.\|SetPostTerms\|TermWriter" cmd internal
      --include=*.go` and compare the call sites with the inventory table in
      `design.md` Overview 4; add any site the table misses to the table in
      this task's commit. In
      `cmd/grimoire/assembly_termgate_pins_test.go` add `TestBuildHandlerTermGatePins`
      over `newCustomWriteHandler(t, true)` and a second subtest with `false`, with
      raw-seeded `nav_menu`, `product_cat`, `link_category` and `post_format` terms
      and a managed `category` term: (a) REST `PUT`, `PATCH` and `DELETE` of each
      unmanaged term through `/wp-json/wp/v2/categories/{id}` and `/tags/{id}`, and
      of a managed category through `/tags/{id}`, as administrator, answer `404
      rest_term_invalid_id` with `termSnapshot` unchanged, and the same as an
      unprivileged `contrib` answers the same `404`, not `403` (Req 3.1, 3.4); the
      same through a registered taxonomy route when a registry is present (3.2);
      (b) `POST /admin/api/terms` and `POST /wp-json/wp/v2/categories` with a body
      naming an unmanaged taxonomy answer `400` and create no row (Req 2.1); (c)
      every `GET` of an unmanaged term through `/admin/api/terms?taxonomy=…` and the
      public archives returns what it does today (Req 4.4); (d) a menu created,
      edited and deleted through `/admin/api/menus` still works (Req 4.2); (e) an M13
      parent change of a managed category to an unmanaged term's id answers `400`
      (Req 1.8). Record the current admin results for `PUT`/`DELETE
      /admin/api/terms/{id}` of an unmanaged term and for the admin `termIds` map
      naming `nav_menu` as comments, not assertions: they currently succeed, which
      Phase 2 turns RED.
      This is a characterization task: every assertion passes on the unmodified
      tree. Run `go test ./cmd/grimoire -run 'BuiltinGolden|TermGatePins'` green and
      keep the golden test in every later gate.
      Files: `cmd/grimoire/assembly_hardening_fixture_test.go`,
      `cmd/grimoire/assembly_termgate_pins_test.go`, `plans/19-term-gate-and-delete-parity/design.md`
      (inventory table, only if the grep finds a missed site)
      Verify: both tests pass on the unmodified tree; `git status` shows only these
      files. _(Req 1.8, 2.1, 3.1-3.5, 4.1, 4.2, 4.4, 12.1; D3, D4)_

## Phase 2 — Content: the taxonomy gate (`internal/content`, TDD) — PR 1

- [x] 2.1 Write the failing service tests first. Add
      `internal/content/termgate_test.go` over the existing term fakes (a
      recording `TermWriter` and `TermReader`) and a recording `PostTermsWriter`.
      Table over the stored taxonomies `nav_menu`, `link_category`, `product_cat`,
      `post_format` and a taxonomy registered in one registry and absent from the
      one under test. With an administrator principal assert: `Update` and
      `Delete` return `ErrForbidden` and the fake recorded zero writes (Req 1.1,
      1.2); `Update` with a caller `t.Taxonomy` of `category` for a stored
      `nav_menu` term is still refused, because the stored taxonomy wins (1.1);
      `Delete` of a missing id returns `domain.ErrNotFound` (1.3); a principal
      without `manage_categories` gets `ErrForbidden` for every term, managed or
      not, with no read recorded (1.5); `category`, `post_tag` and a registered
      taxonomy behave exactly as before (1.6); the same unmanaged taxonomies are
      refused with a nil registry (1.7); `Update` reads the stored term once and a
      read error is returned with nothing written; `Create` of an unmanaged
      taxonomy returns `ErrTaxonomyUnknown` (2.1). For `SetPostTerms` on `post`,
      `page` and a registered type: an unmanaged taxonomy returns
      `ErrTaxonomyUnknown` with no recorded write, including an empty id list
      (2.2); the type gate and capability check still run first, so an
      unwritable row returns `ErrForbidden` and an unauthorized caller returns
      `ErrForbidden` for an unmanaged taxonomy (2.5); a built-in assignment with
      an id of another taxonomy returns `ErrTermInvalid` and records no write, and
      with a valid id succeeds, and with no term reader configured behaves as
      before (2.4). Add a table test for `ManagedTaxonomy(reg, taxonomy)` (`category`,
      `post_tag`, registered, absent, empty string, nil registry), and a test that
      `PostWriteService.Delete` still detaches only managed taxonomies (2.6).
      Files: `internal/content/termgate_test.go`
      Verify: `go test ./internal/content -run 'TermGate|ManagedTaxonomy'` — fails
      (compile: `ManagedTaxonomy` does not exist; once stubbed, `Update` and
      `Delete` of a `nav_menu` term succeed and `SetPostTerms` forwards
      `nav_menu`). _(Req 1.1-1.3, 1.5-1.7, 2.1, 2.2, 2.4-2.6; D1, D2)_

- [x] 2.2 Implement the gate. In `internal/content/typegate.go` add
      `ManagedTaxonomy(reg *registry.Registry, taxonomy string) bool` as in
      `design.md`. In `internal/content/writeservices.go`: `TermWriteService.Create`
      calls `ManagedTaxonomy` instead of its inline test (same result); `Update`
      returns `ErrForbidden` right after the not-found check when
      `!ManagedTaxonomy(s.reg, stored.Taxonomy)` and then sets `t.Taxonomy =
      stored.Taxonomy`; `Delete` runs the capability check, one `TermsByIDs([id])`
      read (error returned, empty result `domain.ErrNotFound`), the gate, then
      `s.w.Delete`. In `internal/content/postterms.go`, after the capability check,
      `SetPostTerms` returns `ErrTaxonomyUnknown` for an unmanaged taxonomy; extract
      the membership loop of `checkTerms` into `checkMembership(ctx, taxonomy, ids)`
      and call it for built-in rows when `len(termIDs) > 0` and a term reader is
      configured. No handler changes: the admin handlers already map `ErrForbidden`
      to `403` and `ErrTaxonomyUnknown` to `400`, and `applyTermIDs` already
      reports a failed taxonomy in `partial`.
      Files: `internal/content/typegate.go`, `internal/content/writeservices.go`,
      `internal/content/postterms.go`
      Verify: `go test ./internal/content` — 2.1 passes and every existing
      `writeservices_*`, `postterms_*`, `termparent_*` and `postwrite_*` test is
      unchanged and green; `go test ./cmd/grimoire -run 'BuiltinGolden|TermGatePins'`
      green. _(Req 1.1-1.8, 2.1-2.6; D1, D2)_

## Phase 3 — Assembly: drive PR 1 through `buildHandler` (`cmd/grimoire`, TDD) — PR 1

- [x] 3.1 Write the failing assembly tests first. Add
      `cmd/grimoire/assembly_termgate_test.go` over `newCustomWriteHandler(t, true)`
      and `(t, false)`, with the raw-seeded terms of 1.1 and a post with a
      relationship in `category`. As administrator: `PUT` and `DELETE
      /admin/api/terms/{id}` of each unmanaged term answer `403 forbidden` and
      `termSnapshot` is unchanged, including a `PUT` whose body claims a different
      taxonomy (Req 1.4); the same calls on a managed `category`, a `post_tag` and
      (with a registry) a registered taxonomy still answer `200`/`204` (1.6); as
      `contrib` (no `manage_categories`) both answer `403` for managed and
      unmanaged terms alike (1.5); `PUT /admin/api/posts/{id}` with `termIds`
      `{"nav_menu":[menuTerm],"category":[cat]}` answers `200`, `partial` names
      `nav_menu` only, no `nav_menu` relationship exists and the category
      relationship does (Req 2.3); `termIds` `{"category":[navMenuTermID]}` is
      reported in `partial` and writes nothing (2.4); a menu created, edited and
      deleted through `/admin/api/menus` still works while a generic `PUT
      /admin/api/terms/{menuTerm}` answers `403` (Req 4.2, 4.3); with the registry
      built without the `product` taxonomy, a previously registered taxonomy's terms
      answer `403` and their rows are untouched (Req 4.5); `GET` of every term
      listing is unchanged (4.4); REST pins of task 1.1 still pass (3.1-3.5).
      Files: `cmd/grimoire/assembly_termgate_test.go`
      Verify: `go test ./cmd/grimoire -run TermGate$` — fails on the unmodified
      production tree (admin `PUT`/`DELETE` of a `nav_menu` term answer `200`/`204`, and
      `nav_menu` appears in the relationships), because this task runs in the same wave as
      2.1 and before 2.2. Record the observed failure in the commit message. _(Req 1.4-1.6, 2.3, 2.4,
      3.1-3.5, 4.2-4.5, 5.1; D2, D4)_

- [x] 3.2 Prove the wiring. `buildHandler` needs **no new option** for PR 1:
      `NewTermWriteService` already receives `WithTermRegistry(deps.Registry)`
      (`assembly.go:124`) and `NewPostTermsWriteService` already receives the
      registry and `WithPostTermsReader` (`assembly.go:125-127`). Confirm that by
      reading those lines and record it. Then prove the guards, each once, and
      restore: (a) revert only the `Update` and `Delete` gate in
      `writeservices.go` — `TestBuildHandlerTermGate` must fail on the admin `403`
      assertions; (b) revert only the `SetPostTerms` gate — it must fail on the
      `partial`/relationship assertions; (c) remove
      `content.WithTermRegistry(deps.Registry)` from the `NewTermWriteService` call
      — it must fail on the registered-taxonomy `200` assertions (the terms of a
      registered taxonomy become unmanaged); (d) remove `content.WithPostTermsReader`
      — it must fail on the `termIds` membership assertion. Record all four in the
      PR description. Golden green. No code change is expected in this task; if a
      proof does not fail, strengthen the test in 3.1 and say so.
      Files: `cmd/grimoire/assembly_termgate_test.go` (only if a proof is weak)
      Verify: `go test ./cmd/grimoire` green with everything restored; the four
      reverted runs failed as stated. _(Req 5.1, 5.2, 13.3, 13.4)_

## Phase 4 — Documentation and PR 1 gate — PR 1

- [x] 4.1 Update the documents to match PR 1. `docs/compatibility.md`: remove the
      M18 PR 1 **Known gap** bullet (term update and delete not gated) and add an
      "M19 — Term gate and delete parity (PR 1)" section after the M18 PR 2 section:
      managed versus unmanaged taxonomy, the responses table (admin term update and
      delete `403`, create and assignment name-addressed `400`/`partial`, REST `404`
      unchanged), the built-in membership check on assignment, the `nav_menu`
      coexistence rule (the menu editor works, a generic term edit of a menu is
      refused), and the registry-removal rule (terms become unwritable until the
      taxonomy is registered again). Update the page title line
      `# WordPress Compatibility (M1-M12, M17, M18)` to include M19.
      `plans/post-m10-gap-analysis/README.md`: remove "unwritable taxonomies on term
      update and delete" from the "Shipped in M18 (PR 1)" open list and add the
      "Shipped in M19 (PR 1)" paragraph. `plans/wordpress-core-parity-roadmap/tasks.md`:
      no entry names this gap; add nothing unless `grep -n "unwritable taxonomies"`
      finds one. `plans/README.md`: add the `19-term-gate-and-delete-parity` row as
      "🟡 In progress (1 of 3 shipped)". Search `grep -rn "unwritable taxonom\|term update
      and delete" docs README.md plans` and fix each statement this PR makes stale,
      leaving `plans/17-custom-types-write` and `plans/18-write-hardening`
      untouched (they are historical records).
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`,
      `plans/README.md`
      Verify: `grep -n "Term update and delete are not yet gated" docs/compatibility.md`
      finds nothing; the plans README row exists; `go build ./...` unaffected.
      _(Req 15.1, 15.4-15.6)_

- [x] 4.2 Final gate for PR 1. Confirm: no new file under
      `internal/storage/migrations` (Req 5.4); `git diff main -- internal/domain/repository.go`
      empty (no port); no new capability string (`git diff main | grep -n 'Cap[A-Z]\|"manage_\|"edit_'`
      reviewed); `go.mod` and `go.sum` unchanged; the built-in golden files unchanged
      (`git diff main -- cmd/grimoire/testdata/builtin_golden` empty, Req 5.3, 12.1).
      Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`,
      and `go test -race ./internal/content ./internal/web ./cmd/grimoire`. Fix
      anything red in the phase that owns it. Ensure all tests pass, ask the user if
      questions arise.
      Files: (verification only)
      Verify: all gate commands green; the git checks show no violation.
      _(Req 5.3, 5.4, 12.1, 14.2, 14.5)_

## Phase 5 — `sticky_posts` on delete (`internal/php`, `internal/content`, TDD) — PR 2

- [x] 5.1 Write the failing tests first. In `internal/php/php_test.go` add a table
      for `Serialize([]int{...})`: `[]int{5, 9}` → `a:2:{i:0;i:5;i:1;i:9;}`,
      `[]int{}` → `a:0:{}`, `[]int{7}` → `a:1:{i:0;i:7;}`, and a round trip
      `Unserialize(Serialize(x))` returning a map with keys `"0".."n-1"` and the same
      values in order (Req 8.6, 8.11). Add `internal/content/sticky_test.go` over a
      recording fake `OptionRepository` and `OptionWriter` and the existing
      `PostWriteService` fakes (recording `PostWriter`, detacher and purger so the
      order is observable): deleting a `post` whose id is in `a:3:{i:0;i:4;i:1;i:9;i:2;i:12;}`
      writes `a:2:{i:0;i:4;i:1;i:12;}`, order kept (8.1); an id not in the list writes
      nothing (8.2); no option (`domain.ErrNotFound`) writes nothing and creates
      nothing (8.3); an unparsable value, a non-array value and an array holding a
      non-integer leave the option unchanged, log, and the delete still completes
      (8.4); removing the only id writes `a:0:{}` (8.5); a `page` and a registered
      type delete never read the option (8.9); the recorded call order is revisions,
      term detach, option get/set, purge, row delete (8.7); a `Get` error (not
      `ErrNotFound`) and a `Set` error each return the error, record no purge and no
      row delete, and a second `Delete` with a working store completes (8.8, D8);
      with no option ports configured the delete behaves as before (8.10).
      Files: `internal/php/php_test.go`, `internal/content/sticky_test.go`
      Verify: `go test ./internal/php ./internal/content -run 'Serialize|Sticky'` —
      fails (compile: `[]int` is an unsupported type, `WithStickyOptions` does not
      exist; once stubbed, the option still lists the deleted id). _(Req 8.1-8.11;
      D7, D8)_

- [x] 5.2 Implement it. In `internal/php/serialize.go` add `case []int:` writing
      `a:N:{i:0;i:v;…}` with integer keys and values, and update the doc comment's
      supported-types list. In `internal/content/writeservices.go` add
      `WithStickyOptions(r domain.OptionRepository, w domain.OptionWriter)
      PostWriteOption`, the `unstick(ctx, id)` helper of `design.md`, and the call in
      `Delete` after the term detach and before the purger, for `cur.Type == "post"`
      only; update the `Delete` doc comment's step list. Use the package's existing
      logger convention for the warning (check how `content` logs elsewhere before
      adding an import; if it has no logger, pass nothing and return nil silently,
      and say so in the commit).
      Files: `internal/php/serialize.go`, `internal/content/writeservices.go`
      Verify: `go test ./internal/php ./internal/content` — 5.1 passes, every
      existing test green; `go test ./cmd/grimoire -run BuiltinGolden` green.
      _(Req 8.1-8.11; D7, D8)_

## Phase 6 — REST page `parent` always emitted (`internal/content`, `cmd/grimoire`) — PR 2

- [x] 6.1 Write the failing tests and the golden diff check first. In
      `internal/content/rest_test.go` (or the file holding the `RESTPage` tests) add:
      `RESTMapper.Page` for a top-level page marshals with the key `parent` equal to
      `0`, and for a nested page with its id (Req 6.1, 6.2). In `internal/web` add
      handler tests asserting the key `parent` is present, including `0`, on `GET
      /wp-json/wp/v2/pages`, `GET /pages/{id}`, the `POST /pages` `201` and the `PUT
      /pages/{id}` `200` (6.1), and pins that fail on drift: a `post` item has no
      `parent` key, a registered hierarchical type emits `parent` including `0`, a
      registered flat type has no `parent` key (Req 7.1-7.3), and the admin page
      detail still omits `parent` when `0` (6.6). Add the script
      `scripts/check-golden-parent.sh` (executable, `set -eu`) that implements the
      three checks of `design.md` "Golden regeneration": no removed lines; every added
      line matches `^\+[[:space:]]+"parent": 0,?$`; and the added-line count per file
      equals the number of page objects in `git show main:<file>` that have no
      `parent` key (counted with `jq`). Before regenerating, run the count on the
      unmodified goldens and write the expected number per file in the task's commit
      message, so a reviewer can compare it with the diff.
      Files: `internal/content/rest_test.go`, `internal/web/rest_pages_parent_test.go`,
      `scripts/check-golden-parent.sh`
      Verify: `go test ./internal/content ./internal/web -run 'RESTPageParent|ParentPins'`
      — the page tests fail (the key is absent for a top-level page, because of
      `omitempty`); the post, custom-type and admin pins pass on the unmodified tree.
      `scripts/check-golden-parent.sh` run now exits `0` with "no changes to check"
      (nothing diverges from `main` yet) and is run again in 6.2. _(Req 6.1-6.3,
      6.5, 6.6, 7.1-7.3; D5, D6)_

- [x] 6.2 Implement it, then regenerate the goldens in their own commit. **Commit
      A (code):** in `internal/content/rest.go` change `Parent int64
      \`json:"parent,omitempty"\`` to `json:"parent"` and replace its doc comment;
      change only the stale `omitempty`-era assertions in existing tests, narrowly,
      and say so in the commit message. At this commit `TestBuiltinGolden` is
      expected to fail on page objects only; record that. **Commit B (goldens only):**
      run `go test ./cmd/grimoire -run TestBuiltinGolden -update` once, then
      `scripts/check-golden-parent.sh`, which must exit `0`; paste its output and the
      expected-versus-actual counts into the commit message, which names the cause
      ("RESTPage.parent no longer omitted when 0"). Commit B changes only the two
      files under `cmd/grimoire/testdata/builtin_golden/`. Re-run
      `go test ./cmd/grimoire -run TestBuiltinGolden` without `-update`: green.
      Files: `internal/content/rest.go`, existing tests with stale assertions,
      `cmd/grimoire/testdata/builtin_golden/with_registry.json`,
      `cmd/grimoire/testdata/builtin_golden/without_registry.json`
      Verify: 6.1 passes; `scripts/check-golden-parent.sh` exits `0`;
      `git diff main --stat -- cmd/grimoire/testdata/builtin_golden` shows only
      insertions; `go test ./...` green. If the script fails, revert commit B and
      find the cause; do not edit the goldens by hand. _(Req 6.1-6.5, 12.2, 12.3;
      D5, D6)_

## Phase 7 — Assembly: wire sticky and drive PR 2 through `buildHandler` (`cmd/grimoire`, TDD) — PR 2

- [x] 7.1 Write the failing assembly tests first. Add
      `cmd/grimoire/assembly_sticky_test.go` over `newCustomWriteHandler`, with the
      `sticky_posts` option seeded through `env.Repos.DB()`
      (`a:2:{i:0;i:<A>;i:1;i:<B>;}`) and two posts `A` and `B`, a page `P`, and a
      registered-type row: `DELETE /admin/api/posts/{A}` answers `204` and the stored
      option is `a:1:{i:0;i:<B>;}` (Req 8.1); `DELETE /wp-json/wp/v2/posts/{B}` then
      leaves `a:0:{}` (8.5); deleting a post that is not sticky leaves the option
      byte-identical (8.2); a site with no `sticky_posts` row stays without one
      (8.3); a malformed option (`not-serialized`) survives a post delete unchanged
      and the delete answers `204` (8.4); deleting a page and the registered row
      leaves the option byte-identical (8.9); a failing option writer (wrap
      `repos.OptionWriter` with a failing fake through the `assemblyHandler` builder)
      answers `500`, the post still exists and a retry with the working writer
      succeeds (8.8). Add the through-`buildHandler` REST page checks of Phase 6:
      create a top-level page and a child over REST and read both back, `parent` is
      `0` and the parent id (Req 6.1, 6.2). No built-in golden file changes beyond
      commit B of task 6.2.
      Files: `cmd/grimoire/assembly_sticky_test.go`
      Verify: `go test ./cmd/grimoire -run Sticky` — the sticky tests fail (the option
      still lists the deleted id) because `buildHandler` builds the post service
      without the option ports; the page assertions fail until task 6.2 lands. _(Req 6.1,
      6.2, 8.1-8.5, 8.8, 8.9, 13.1, 13.2; D7)_

- [x] 7.2 Wire the option ports. In `cmd/grimoire/assembly.go` add
      `content.WithStickyOptions(repos.Options, repos.OptionWriter)` to the
      `NewPostWriteService` call. No other assembly change.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 7.1 passes. **Proof of wiring:** remove
      `WithStickyOptions` locally; `TestBuildHandlerSticky` must fail on the first
      assertion; restore. **Proof of the mapper change:** revert the `json` tag locally;
      the page assertions in `TestBuildHandlerSticky` and the Phase 6 tests must fail;
      restore. Record both in the PR description. `TestBuiltinGolden` green.
      _(Req 13.1, 13.3, 13.4; D7)_

## Phase 8 — Documentation and PR 2 gate — PR 2

- [x] 8.1 Update the documents to match PR 2. `docs/compatibility.md`: remove the
      two M18 PR 2 known-gap bullets "REST page `parent` is omitted…" and
      "`sticky_posts` is not updated…"; add "M19 (PR 2)" to the M19 section: REST page
      `parent` is always present (posts have none, hierarchical custom types
      emit it, flat ones omit it; the admin detail is unchanged); `sticky_posts`
      behavior (post rows only, only when the id is present, the option is never
      created, a malformed option is left alone, order of ids kept, empty list stored
      as `a:0:{}`, step order in the delete sequence, failure and retry). Fix the
      delete-sequence description in the M18 PR 2 section to include the unstick step.
      `plans/post-m10-gap-analysis/README.md`: remove "REST page `parent` is omitted
      when `0`" and "`sticky_posts`" from the M18 PR 2 open list and add the "Shipped in
      M19 (PR 2)" paragraph. `plans/wordpress-core-parity-roadmap/tasks.md`: no entry
      names these gaps; add nothing unless `grep -n "sticky_posts\|parent.*omitted"` finds
      one. `plans/README.md`: set the row to "🟡 In progress (2 of 3 shipped)".
      Search `grep -rn "sticky_posts\|omitted when .0" docs README.md plans` and fix each
      statement this PR makes stale, leaving `plans/17-*` and `plans/18-*` untouched.
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`,
      `plans/README.md`
      Verify: `grep -n "sticky_posts. is not updated" docs/compatibility.md` finds
      nothing; the plans README row reads 2 of 3. _(Req 15.2, 15.4-15.6)_

- [x] 8.2 Final gate for PR 2. Confirm: no new file under
      `internal/storage/migrations`; `git diff main -- internal/domain/repository.go`
      empty (Req 12.4, no port); no new capability string; `go.mod` and `go.sum`
      unchanged; `scripts/check-golden-parent.sh` exits `0` against `main` and the
      golden diff is only the commit-B insertions (`git diff main --stat --
      cmd/grimoire/testdata/builtin_golden` shows insertions only); no `GRIMOIRE_TEST_*`
      variable added. Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`,
      `go test ./...`, and `go test -race ./internal/content ./internal/php
      ./internal/web ./cmd/grimoire`. Fix anything red in the phase that owns it.
      Ensure all tests pass, ask the user if questions arise.
      Files: (verification only)
      Verify: all gate commands green; the git checks show no violation.
      _(Req 6.3, 6.4, 12.2, 14.2, 14.5)_

## Phase 9 — Storage: the menu-item purger port (`internal/domain`, `internal/storage`, TDD) — PR 3

- [~] 9.1 Write the failing contract tests first. Add
      `internal/storage/storagetest/postmenuitems_contract.go` with
      `RunPostMenuItemPurgerContract(t *testing.T, newRepos NewReposWithPrefixFunc)`
      and register it beside `RunPostDependentsPurgerContract` in
      `sqlite_test.go:107`, `mysql_test.go:80` and `postgres_test.go:80`, each
      passing the same vendor `newRepos` closure the neighbor passes. The function
      takes `repos, vendor, prefix, cleanup` from `newRepos(t)` and uses **no
      literal table prefix and no literal placeholder**: seed through helpers in the
      style of `seedPostDependents` (`rebind` for placeholders, `rebind.Ident(vendor,
      "ID")` for the posts key, `prefix` from the closure). Fixture ids sit far above
      `SeedFixtures` (for example 9300+). Seed: page `P` and page `Q`; two `nav_menu`
      terms `M1` and `M2` with `term_taxonomy` rows and `count` 3 and 1; menu items
      `I1` (type `post_type`, object `page`, object id `P`, in `M1`), `I2` (same
      target, in `M2`), `I3` (`post_type`, object `page`, object id `Q`, in `M1`),
      `I4` (`post_type`, object `post`, object id `P`, in `M1`: right id, wrong object),
      `I5` (`taxonomy`, object `category`, object id `P`, in `M1`: right id, wrong
      type), `I6` (`custom`, object `custom`, object id `0`, in `M1`), and a plain
      `post` row `X` carrying `_menu_item_object_id` = `P` postmeta (not a
      `nav_menu_item`). Every item gets the four `_menu_item_*` postmeta rows plus a
      `_menu_item_menu_item_parent` of `I1` on `I3` (a child of the deleted item).
      Assert for `PurgeMenuItemsOf(ctx, P, "page")`: `I1` and `I2` are gone with all
      their postmeta and relationship rows (Req 10.2); `I3`, `I4`, `I5`, `I6` and `X`
      still exist with their meta (10.4, 10.5); `I3`'s `_menu_item_menu_item_parent`
      is still `I1` (10.6); `P`, `Q` and both menu terms still exist (9.4, 10.5);
      `M1` count is 2 and `M2` count is 0 (10.3); a second call returns nil and
      changes nothing (9.5); a call for an id with no items, and for a nonexistent
      post id, return nil (9.4); a call with a different `objectType` for `P`
      deletes nothing. Atomicity (9.6) has no portable failure injection, so it is
      tested on SQLite only in `internal/storage/wprepo/postmenuitems_test.go`:
      drop `term_taxonomy`, call the method, assert it returns an error and every
      item, meta and relationship row is unchanged (the recount is the last
      statement, so the deletes roll back).
      Files: `internal/storage/storagetest/postmenuitems_contract.go`,
      `internal/storage/storagetest/sqlite_test.go`, `mysql_test.go`, `postgres_test.go`,
      `internal/storage/wprepo/postmenuitems_test.go`
      Verify: `go test ./internal/storage/storagetest ./internal/storage/wprepo -run
      PostMenuItem` — fails (compile: `repos.PostMenuItems` and the port do not
      exist). `grep -n "prefix\b.*:=.*\"wp_\"\|\"wp_\"" internal/storage/storagetest/postmenuitems_contract.go`
      finds nothing (Req 11.1). _(Req 9.2, 9.4-9.6, 10.2-10.6, 11.1, 11.2, 11.5, 11.6;
      D9, D10, D12)_

- [~] 9.2 Implement the port, then verify it on all three databases. In
      `internal/domain/repository.go` add `PostMenuItemPurger` with the doc comment of
      `design.md`. Create `internal/storage/wprepo/postmenuitems.go` with
      `(*PostRepo) PurgeMenuItemsOf` in one `RunInTx`: the matching `SELECT`, a Go-side
      de-duplication of ids, then per id the `nav_menu` `term_taxonomy_id` read, the
      three deletes, and finally the recount per distinct `term_taxonomy_id`
      (statements as in `design.md`). Wrap the `ID` column with
      `rebind.Ident(vendorOf(r.db), "ID")` and build every statement with the repo's
      prefix, exactly as `PurgePostDependents` and `NavMenuRepo.DeleteItem` do. Add
      `_ domain.PostMenuItemPurger = (*PostRepo)(nil)` beside the other assertions in
      `wprepo/writers.go`. In `internal/storage/factory.go` add `PostMenuItems
      domain.PostMenuItemPurger` to `Set` and set it to `posts` beside `PostDependents`.
      **Cross-vendor verification (Requirement 11.4), not optional:** start throwaway
      containers with the CI image tags and DSNs, using the commands in `design.md`
      "Cross-vendor verification" (adjust host ports if 3306 or 5432 is busy), run
      `go test ./internal/storage/storagetest/ -count=1 -v -run 'PostMenuItem|PostDependents'`
      with both `GRIMOIRE_TEST_*` variables set, confirm the output shows
      `PostMenuItem` subtests **passing under `mysql` and `postgres`** and not
      skipped, then `docker rm -f` both containers. Paste the commands and the
      pass lines into the PR description. If a vendor fails, fix the statement
      (not the test) and re-run all three; a failure that only the container run
      finds is the case the M18 fix was written for.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/postmenuitems.go`,
      `internal/storage/wprepo/writers.go`, `internal/storage/factory.go`
      Verify: `go test ./internal/storage/...` — 9.1 passes on SQLite; the container run
      passes on MySQL 8.0 and PostgreSQL 16; every existing contract is unchanged;
      `go build ./...` shows no other `Set` construction breaks; `docker ps -a` shows
      no leftover `g19-*` container. _(Req 9.1-9.7, 10.2-10.6, 11.3, 11.4; D9, D10)_

## Phase 10 — Content: the delete step (`internal/content`, TDD) — PR 3

- [~] 10.1 Write the failing service tests first. Add
      `internal/content/menuitems_delete_test.go` over the `PostWriteService` fakes
      with a recording `PostMenuItemPurger`, detacher, option ports, dependents purger
      and `PostWriter`: deleting a `post`, a `page` and a registered-type row calls
      `PurgeMenuItemsOf(id, type)` once with the stored type (Req 10.1); the recorded
      order is revisions, term detach, unstick (post only), dependents purge, menu-item
      purge, row delete (D8); a purger error returns it, records no row delete, and a
      second `Delete` with a working purger completes (10.7); a failing earlier step
      records no menu-item purge; an unwritable type, a forbidden actor and a missing
      id call nothing; with no purger configured the delete behaves as before M19
      (10.8). Extend the existing delete-order tests only by inserting the new step,
      and say so.
      Files: `internal/content/menuitems_delete_test.go`
      Verify: `go test ./internal/content -run MenuItemsDelete` — fails (compile:
      `WithPostMenuItemPurger` does not exist; once stubbed, no purge call is
      recorded). _(Req 10.1, 10.7, 10.8; D8)_

- [~] 10.2 Implement the delete step. In `internal/content/writeservices.go` add
      `WithPostMenuItemPurger(p domain.PostMenuItemPurger) PostWriteOption` and the
      call `PurgeMenuItemsOf(ctx, cur.ID, cur.Type)` in `Delete` after the dependents
      purge and before `s.w.Delete`, returning its error. Update the `Delete` doc
      comment's step list.
      Files: `internal/content/writeservices.go`
      Verify: `go test ./internal/content` — 10.1 passes, every existing test green;
      `go test ./cmd/grimoire -run BuiltinGolden` green. _(Req 10.1, 10.7, 10.8; D8, D9)_

## Phase 11 — Assembly: wire PR 3 and drive it through `buildHandler` (`cmd/grimoire`, TDD) — PR 3

- [~] 11.1 Write the failing assembly tests first. Add
      `cmd/grimoire/assembly_menuitem_delete_test.go` over `newCustomWriteHandler`, with
      menus created through `/admin/api/menus` and raw-seeded items through
      `seedRawMenuItem`: a page `P` linked from menu `M1` (twice, two items) and from
      `M2`, a custom link, a taxonomy item and an item for another page `Q`. `DELETE
      /admin/api/posts/{P}` answers `204`; the menu read `GET /admin/api/menus/{M1}`
      no longer lists the items for `P` and still lists the others (Req 10.9); the
      rows, their postmeta and relationships are gone by SQL, `M1` and `M2` terms and
      `Q`'s item are unchanged, and the `M1` `term_taxonomy.count` equals the
      remaining relationship count (10.2-10.5). Repeat through REST `DELETE
      /wp-json/wp/v2/pages/{P}`, for a `post` through the posts route and for a
      registered-type row through its route with `object` set to the registry name.
      A failing purger (wrap `repos.PostMenuItems` with a failing fake through the
      `assemblyHandler` builder) answers `500`, the post row still exists, its menu
      items still exist, and a retry with the working purger succeeds (10.7). A
      public page render of a menu that held the deleted page's item no longer
      includes it. `GET` responses that do not involve the deleted post are unchanged
      (10.10). No built-in golden file changes.
      Files: `cmd/grimoire/assembly_menuitem_delete_test.go`
      Verify: `go test ./cmd/grimoire -run MenuItemDelete` — fails (the menu items
      remain, the `GET` still lists them) because `buildHandler` builds the post
      service without the purger. _(Req 10.1-10.10, 13.1, 13.2; D9, D10)_

- [~] 11.2 Wire the purger. In `cmd/grimoire/assembly.go` add
      `content.WithPostMenuItemPurger(repos.PostMenuItems)` to the `NewPostWriteService`
      call. No other assembly change.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 11.1 passes. **Proof of wiring:** remove
      `WithPostMenuItemPurger` locally; `TestBuildHandlerMenuItemDelete` must fail on the
      menu-read assertion; restore. Record it in the PR description. Golden green.
      _(Req 13.1, 13.3; D9)_

## Phase 12 — Documentation and PR 3 gate — PR 3

- [~] 12.1 Update the documents to match PR 3. `docs/compatibility.md`: remove the M18
      PR 2 known-gap bullet "Nav-menu items that reference a deleted post are not
      removed or updated" and the "There is no trash" bullet's stale company if any
      (keep the trash gap, it is still open); add "M19 (PR 3)" to the M19 section: what
      is deleted (matching items, their postmeta and relationships), what
      matches (`_menu_item_type` `post_type`, `_menu_item_object` equal to the deleted
      type, `_menu_item_object_id` equal to the id, row type `nav_menu_item`), that
      menu terms and other items are untouched, that the menu `count` is recomputed,
      the step position in the delete sequence, idempotence and retry, and the gaps still
      open: child menu items keep a dangling `_menu_item_menu_item_parent` (the menu
      editor's delete does the same), `NavMenuRepo.DeleteItem` does not recount, the media
      delete path does not clean menu items, relationships in unmanaged taxonomies stay on
      a deleted post, trash and restore, term-side post meta, WordPress hooks and the
      object cache. `plans/post-m10-gap-analysis/README.md`: remove the nav-menu entry
      from the M18 PR 2 open list and add the "Shipped in M19 (PR 3)" paragraph.
      `plans/wordpress-core-parity-roadmap/tasks.md`: no entry names this gap; add
      nothing unless `grep -n "nav-menu items\|menu items that reference"` finds one.
      `plans/README.md`: set the row to "✅ Implemented (3 of 3 shipped)" with the final
      summary. Search `grep -rn "nav-menu items\|menu items that" docs README.md plans` and fix
      each statement this PR makes stale, leaving `plans/17-*` and `plans/18-*` untouched.
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`,
      `plans/README.md`
      Verify: `grep -n "Nav-menu items that reference a deleted post are not" docs/compatibility.md`
      finds nothing; the plans README row reads Implemented. _(Req 15.3-15.6)_

- [~] 12.2 Final gate for PR 3, with explicit cross-vendor verification. Confirm: no new
      file under `internal/storage/migrations` (Req 9.7); exactly one interface added to
      `internal/domain/repository.go` and no method added to an existing one
      (`git diff main -- internal/domain/repository.go` reviewed, 9.7); no new capability
      string; `go.mod` and `go.sum` unchanged; no new `GRIMOIRE_TEST_*` variable; the
      built-in golden files unchanged since PR 2 (`git diff <PR 2 merge base> --
      cmd/grimoire/testdata/builtin_golden` empty, Req 12.1). Run `gofmt -l .` (empty),
      `go vet ./...`, `go build ./...`, `go test ./...`, and `go test -race
      ./internal/content ./internal/web ./internal/storage/... ./cmd/grimoire`.
      **Cross-vendor:** start fresh throwaway MySQL 8.0 and PostgreSQL 16 containers as in
      task 9.2, run the **whole** storage suite, not only the new contract:
      `go test ./internal/storage/... -count=1 -v` with both `GRIMOIRE_TEST_*` variables
      set, confirm no `PostMenuItem` or `PostDependents` subtest is skipped and every
      vendor passes, record the final lines in the PR description, remove the containers,
      and confirm the `storage-contract` CI job is green on the PR before merging. Fix
      anything red in the phase that owns it. Ensure all tests pass, ask the user if
      questions arise.
      Files: (verification only)
      Verify: all gate commands green on SQLite, MySQL and PostgreSQL; the git checks show
      no violation; `docker ps -a` shows no leftover container. _(Req 9.7, 11.3, 11.4,
      12.1, 14.2, 14.5)_

## Notes

- Tasks marked with `*` are optional; **no sub-task here is optional**. The taxonomy
  gate matrix, the sticky branches, the golden diff check, the menu-item matching
  matrix, the cross-vendor contract and the assembly tests are the milestone's
  done-definition (Requirement 14).
- **No property-based tests.** The behaviors are enumerable branches over fixed
  fixtures; the one serializer change has an exact-output table and a round trip.
  See `design.md` Testing strategy.
- **TDD exceptions are explicit:** 1.1 (characterization and fixture), 3.2 and 7.2
  (wiring proofs that need no new code, 3.2), 4.1, 8.1 and 12.1 (docs), 4.2, 8.2 and
  12.2 (gates), and the golden regeneration commit of 6.2 whose RED step is the diff
  check of 6.1.
- **Wiring is tested where it is wired.** Tasks 7.1/7.2 and 11.1/11.2 each pair a
  failing `buildHandler` test with the `assembly.go` edit and a removal proof. PR 1
  needs no new option, and task 3.2 proves the tests fail when the service change is
  reverted instead.
- **Golden files.** PR 1 and PR 3 change none. PR 2 changes them once, in commit B of
  task 6.2, and `scripts/check-golden-parent.sh` is the evidence. If
  `TestBuiltinGolden` fails anywhere else, stop and find the cause; do not pass
  `-update`.
- **Stale-assertion edits are narrow.** No existing test should need to change except
  those that assert the absence of `parent` on a top-level REST page (task 6.2). If
  another asserts that an unmanaged term is editable or that a delete leaves a menu item,
  change only that assertion in the phase that owns the behavior and say so in the
  commit.
- **Storage tests never hardcode a prefix or a placeholder.** They take
  `NewReposWithPrefixFunc`, use the vendor and prefix it returns, and are run on MySQL
  and PostgreSQL containers before the PR opens (tasks 9.2 and 12.2).
- **Open sign-off items** are listed in `design.md` "Decisions awaiting sign-off".
  Defaults are implemented as written; reversing one is local to the task named in
  each item.
- **Not done here, recorded as gaps:** trash and restore (its own future spec),
  term-side post meta, WordPress hooks and the object cache, child menu-item
  re-parenting and the menu editor's missing recount, detaching a deleted post from
  unmanaged taxonomies, and the media delete path.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["2.1", "3.1"] },
    { "id": 2, "tasks": ["2.2"] },
    { "id": 3, "tasks": ["3.2"] },
    { "id": 4, "tasks": ["4.1"] },
    { "id": 5, "tasks": ["4.2"] },
    { "id": 6, "tasks": ["5.1", "6.1", "7.1"] },
    { "id": 7, "tasks": ["5.2", "6.2"] },
    { "id": 8, "tasks": ["7.2"] },
    { "id": 9, "tasks": ["8.1"] },
    { "id": 10, "tasks": ["8.2"] },
    { "id": 11, "tasks": ["9.1", "10.1", "11.1"] },
    { "id": 12, "tasks": ["9.2"] },
    { "id": 13, "tasks": ["10.2"] },
    { "id": 14, "tasks": ["11.2"] },
    { "id": 15, "tasks": ["12.1"] },
    { "id": 16, "tasks": ["12.2"] }
  ]
}
```
