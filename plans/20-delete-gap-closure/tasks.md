# Implementation Plan: M20 — Delete gap closure
## Overview
> **This milestone closes four gaps M19 recorded as open.** Item 1: deleting a menu
> item (by the menu editor or by deleting its page) leaves its children pointing at
> the deleted id, and grimoire's tree builder then hides them. Item 2: the menu
> editor's item delete does not recount the menu. Item 3: deleting a media item
> leaves its menu items, comments, featured-image references and term counts. Item 4:
> deleting a post leaves its relationships in unmanaged taxonomies. No migration, no
> capability name, no dependency. PR 1 adds no port; PR 2 adds one sibling port.
>
> **Two pull requests, each a complete increment, each with its own `buildHandler`
> test and a final gate task with cross-vendor verification.**
>
> | PR | Phases | Increment |
> |---|---|---|
> | 1 (menus) | 1-4 | One transactional item-delete helper that re-parents children and recounts, used by `PurgeMenuItemsOf` and `NavMenuRepo.DeleteItem`; contracts proven on SQLite, MySQL and PostgreSQL; docs; gate |
> | 2 (media and relationships) | 5-8 | `MediaRepo.Delete` cleans menu items, comments, featured-image references and relationships with recounts; `PostTermRelationshipPurger` removes the remaining relationships of a deleted post; one wired option; contracts on three databases; docs; gate |
>
> PR 2 branches from `main` after PR 1 merges, because it reuses the item-delete
> helper. PR 1 stands alone.
>
> **What the code and source check changed.** WordPress core does not re-parent child
> menu items on the server; its nav-menus editor does, in JavaScript. The change is kept
> because grimoire's `buildMenuTree` hides orphans (`design.md` Overview 1). The admin
> API has no media delete, so the media cleanup extends `MediaRepo.Delete`'s existing
> transaction and needs no service option. "Delete relationships over all taxonomies"
> is unsafe as stated: `link_category` relationships use link ids, so the step excludes
> `domain.NonPostObjectTaxonomies` (`design.md` Overview 5).
>
> **Strict-TDD spine.** Every behavior-adding implementation task is preceded by the
> task that writes its failing test, and that test is run and seen to fail **for the
> stated reason** before the implementation starts. A test that fails only because a new
> symbol does not compile counts as RED, and the implementation task makes it compile
> and pass. The documentation tasks, the final gates and the wiring-proof task 3.2 have
> no RED step and say so.
>
> **Storage handling.** The M18 and M19 contracts took `NewReposWithPrefixFunc` after a
> hardcoded prefix failed CI on MySQL and PostgreSQL. Every M20 contract takes
> `storagetest.NewReposWithPrefixFunc`, reads the vendor and prefix it returns, holds no
> literal prefix, vendor name or placeholder, and is run against throwaway MySQL 8.0 and
> PostgreSQL 16 containers in the implementation task and again in the final gate of
> each PR (`design.md` Testing strategy has the commands).
>
> **Standing gate convention.** After every phase: `gofmt -l .` empty, and
> `go vet ./...`, `go build ./...`, `go test ./...` green. `TestBuiltinGolden` runs in
> every gate and no golden file changes in either PR; a failure there is a stop, not an
> `-update`.
>
> **Fixtures.** Assembly tests extend `cmd/grimoire/assembly_hardening_fixture_test.go`
> (`seedRawPost`, `seedRawMeta`, `seedRawTerm`, `seedRawTermRelationship`,
> `seedRawMenuItem`, `rowSnapshot`) and `assembly_custom_fixture_test.go`
> (`newCustomWriteHandler(t, withRegistry)`, `doJSON`, the `customWriteEnv` actors,
> `Repos` and `Prefix`). The M19 `assembly_menuitem_delete_test.go` is the model.
> Storage contracts follow `postmenuitems_contract.go` and `postdependents_contract.go`.
## Tasks
## Phase 1 — Storage: menu contracts first (`internal/storage`, TDD) — PR 1
- [x] 1.1 Write the failing storage tests first.
      (a) In `internal/storage/storagetest/postmenuitems_contract.go` change the
      M19 assertion at "Req 10.6" (child `I3`'s `_menu_item_menu_item_parent` stays
      `I1`) to the new rule, and say so in the commit message. Extend the fixture and
      assertions with the rows in `design.md` "Contracts" (`I7`, `I8`, `I9`, `I13`,
      `I14`, `I15`, `I16`, `I17`, `I18`, `I19`, `I20` and the stray-key post `X`), with
      ids far above `SeedFixtures` (9300 and up) and chosen so one chain has its parent
      at a lower id and one at a higher id. Expected: `I3` becomes a child of `I7`;
      `I8` stays a child of `I3`; `I9` becomes top level; `I14` becomes a child of
      `I7`; `I16` becomes top level; `I19` becomes a child of `I20`; `X` keeps its
      value; `M1` and `M2` counts equal the surviving relationship counts; a second
      call changes nothing. Re-parented values are compared as the decimal strings
      the helper writes.
      (b) Add `RunNavMenuDeleteItemContract(t, newRepos NewReposWithPrefixFunc)` in
      `internal/storage/storagetest/navmenuitemdelete_contract.go`, taking the writer
      from `repos.NavMenus.(domain.NavMenuWriter)`, and register it beside
      `RunPostMenuItemPurgerContract` in `sqlite_test.go`, `mysql_test.go` and
      `postgres_test.go`, each passing the same vendor `newRepos` closure the neighbor
      passes. Cases: a child moves to the deleted item's parent; a child of a top-level
      deleted item becomes `0`; the menu's `count` equals the remaining relationship
      count after the delete; an item of another menu returns `domain.ErrNotFound` with
      every row unchanged; an item with relationships in two `nav_menu` `term_taxonomy`
      rows is removed with both rows and both counts recomputed.
      (c) In `internal/storage/wprepo/postmenuitems_test.go` and
      `internal/storage/wprepo/menus_write_test.go` add SQLite atomicity tests: drop
      `term_taxonomy`, call the method, assert it returns an error and every item,
      postmeta (including the children's parents) and relationship row is unchanged.
      Neither contract nor test holds a literal prefix, vendor name or placeholder.
      Files: `internal/storage/storagetest/postmenuitems_contract.go`,
      `internal/storage/storagetest/navmenuitemdelete_contract.go`,
      `internal/storage/storagetest/sqlite_test.go`, `mysql_test.go`, `postgres_test.go`,
      `internal/storage/wprepo/postmenuitems_test.go`, `internal/storage/wprepo/menus_write_test.go`
      Verify: `go test ./internal/storage/... -run 'PostMenuItem|NavMenuDeleteItem|DeleteItem'`
      — fails: the children still name the deleted ids, and `DeleteItem` leaves `count`
      unchanged. `grep -n '"wp_"' internal/storage/storagetest/navmenuitemdelete_contract.go`
      finds nothing. _(Req 1.1-1.6, 2.1-2.4, 3.2, 4.1-4.4, 5.1, 5.2, 5.5, 5.6, 5.7; D1-D3, D10)_
## Phase 2 — Storage: the item-delete helper (`internal/storage`, TDD) — PR 1
- [x] 2.1 Implement the helper and use it in `PurgeMenuItemsOf`. Create
      `internal/storage/wprepo/menuitemtx.go` with the unexported `matchMenuItemsTx`,
      `deleteMenuItemTx`, `reparentChildrenTx` and `recountTx` of `design.md` "Menu
      items". `matchMenuItemsTx` is the existing matching `SELECT` moved out of
      `PurgeMenuItemsOf`, unchanged. `deleteMenuItemTx` reads the item's menu parent
      first (absent, non-integer, negative or equal to the item's id gives `0`),
      re-points the children with the single `UPDATE` of `design.md` (restricted to
      `nav_menu_item` rows by a subquery on `posts`), collects every
      `term_taxonomy_id` of the item's relationships, then deletes its postmeta, all of
      its relationships and its row, and returns the collected ids. In
      `postmenuitems.go`, `PurgeMenuItemsOf` collects the matched ids, de-duplicates and
      sorts them ascending, calls `deleteMenuItemTx` for each and `recountTx` once over
      the union, all in the one existing `RunInTx`. Build every statement with the
      repo's prefix and `rebind.Ident(vendorOf(r.db), "ID")` exactly as the existing
      code does; add no literal vendor branch. Update the doc comment of
      `domain.PostMenuItemPurger` (children of a deleted item get the deleted item's
      menu parent; nothing else changes).
      Files: `internal/storage/wprepo/menuitemtx.go`, `internal/storage/wprepo/postmenuitems.go`,
      `internal/domain/repository.go`
      Verify: `go test ./internal/storage/... -run PostMenuItem` — the 1.1 purger contract
      and the purger atomicity test pass on SQLite; every M19 case still passes except the
      assertion 1.1 changed; `go build ./...` green. _(Req 1.1-1.7, 2.1-2.6, 3.1-3.4; D1, D2)_
- [x] 2.2 Use the helper in `NavMenuRepo.DeleteItem`, then verify on all three databases.
      In `internal/storage/wprepo/menus_write.go` keep the belongs-to-menu guard and the
      `domain.ErrNotFound` result, and replace the three hand-written deletes with
      `deleteMenuItemTx` and `recountTx` in the same `RunInTx`. Update the doc comments of
      `NavMenuRepo.DeleteItem` and `domain.NavMenuWriter.DeleteItem`. Leave `DeleteMenu`
      unchanged (Req 15.6). **Cross-vendor verification (Requirement 5.4), not
      optional:** start throwaway containers with the CI image tags and DSNs using the
      commands in `design.md` "Testing strategy" (adjust host ports if 3306 or 5432 is
      busy), run `go test ./internal/storage/... -count=1 -v` with both `GRIMOIRE_TEST_*`
      variables set, confirm the `PostMenuItem` and `NavMenuDeleteItem` subtests **pass
      under `mysql` and `postgres`** and are not skipped, then `docker rm -f` both
      containers. Paste the commands and the pass lines into the PR description. If a
      vendor fails, fix the statement (not the test) and re-run all three.
      Files: `internal/storage/wprepo/menus_write.go`, `internal/domain/repository.go`
      Verify: `go test ./internal/storage/...` — 1.1 passes on SQLite; the container run
      passes on MySQL 8.0 and PostgreSQL 16; every existing contract and
      `TestNavMenuRepoDeleteItem` pass; `docker ps -a` shows no leftover `g20-*` container.
      _(Req 1.3, 3.1-3.4, 4.1-4.4, 5.3, 5.4; D1, D3)_
## Phase 3 — Assembly: drive PR 1 through `buildHandler` (`cmd/grimoire`, TDD) — PR 1
- [x] 3.1 Write the failing assembly tests first. Add
      `seedRawMenuItemWithParent(t, env, menuTermID, objectType, objectID, parentItemID)`
      to `cmd/grimoire/assembly_hardening_fixture_test.go` (the existing helper with the
      parent postmeta set) and `cmd/grimoire/assembly_menuitem_reparent_test.go` over
      `newCustomWriteHandler`. (a) Menu `M1` with a page `P` item that has two children
      and a grandchild, and a top-level custom link; `DELETE /admin/api/posts/{P}` answers
      `204`; `GET /admin/api/menus/{M1}` lists the children as top-level items in their
      original order with the grandchild still under its parent, and the public menu
      render of `M1` shows them (Req 6.2, 1.8). (b) The same shape where the deleted item
      has a parent: its children appear under that parent. (c) `DELETE
      /admin/api/menus/{M1}/items/{item}` re-parents the same way, `GET` shows the tree,
      and `M1`'s `term_taxonomy.count` equals the remaining relationship count; the same
      delete of an item of another menu answers `404` with rows unchanged (Req 4.3).
      (d) A failing purger and a failing menu writer (wrapped through the `assemblyHandler`
      builder, as M19's 11.1 did) answer `500` and leave every row unchanged. No built-in
      golden file changes.
      Files: `cmd/grimoire/assembly_hardening_fixture_test.go`,
      `cmd/grimoire/assembly_menuitem_reparent_test.go`
      Verify: `go test ./cmd/grimoire -run MenuItemReparent` — fails: the children are
      missing from the menu read and the count is stale. _(Req 1.1-1.3, 1.8, 3.2, 4.1, 4.3,
      6.1, 6.2; D1, D3)_
- [x] 3.2 Prove the wiring. PR 1 needs **no `assembly.go` change**: both delete paths are
      reached through ports `buildHandler` already wires (`repos.PostMenuItems` and
      `repos.NavMenus`). This is a verification task with no new code. Revert
      `internal/storage/wprepo/menuitemtx.go`'s re-parenting call locally (comment out
      the `UPDATE` in `reparentChildrenTx`); `TestBuildHandlerMenuItemReparent` must fail
      on the menu-read assertion; restore. Then restore the old hand-written deletes in
      `DeleteItem` locally; the recount assertion must fail; restore. Record both results
      in the PR description. Run `go test ./cmd/grimoire` green, with `TestBuiltinGolden`.
      Files: (verification only)
      Verify: both reverts fail the named test; the final tree is green. _(Req 6.1, 6.3, 6.4)_
## Phase 4 — Documentation and PR 1 gate — PR 1
- [x] 4.1 Update the documents to match PR 1. `docs/compatibility.md`: remove the M19 PR 3
      "Known gaps" bullets "Child menu items keep a dangling `_menu_item_menu_item_parent`…
      The menu editor's item delete does the same." and "`NavMenuRepo.DeleteItem` does not
      recount the menu's `count`."; change the title line to include M20; add "M20 — Delete
      gap closure (PR 1)": children of a deleted item take the deleted item's menu parent
      (nearest surviving ancestor for chains, `0` for a top-level item or a self-parent),
      on both the page-delete and the menu-editor paths, with the corrected rationale
      (WordPress core re-parents only in its editor and shows orphans at the top level;
      grimoire's tree builder hides them), the `DeleteItem` recount, the helper's single
      transaction, idempotence, the relationship-count rule, and the gaps still open (media
      delete, unmanaged-taxonomy relationships, trash, hooks and the object cache). Fix the
      M19 PR 3 "What is deleted" paragraph's statement that children are left alone.
      `plans/post-m10-gap-analysis/README.md`: edit the "Shipped in M19 (PR 3)" paragraph's
      sentence "Child menu items are not re-parented, as in the menu editor's delete" and
      add the "Shipped in M20 (PR 1)" paragraph. `plans/README.md`: add the row
      `| 20 | [`20-delete-gap-closure`](./20-delete-gap-closure) | 🟡 In progress (1 of 2 shipped) | … |`
      with a summary. Run `grep -rn "dangling\|not re-parented\|does not recount" docs README.md plans`
      and fix each statement this PR makes stale, leaving `plans/17-*`, `plans/18-*` and
      `plans/19-*` untouched. `plans/wordpress-core-parity-roadmap/tasks.md` names none of
      these gaps; add nothing unless the grep finds one.
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`, `plans/README.md`
      Verify: `grep -n "keep a dangling" docs/compatibility.md` finds nothing; the plans README
      row reads 1 of 2. _(Req 17.1, 17.3-17.5)_
- [-] 4.2 Final gate for PR 1, with explicit cross-vendor verification. Confirm: no new file
      under `internal/storage/migrations`; `git diff main -- internal/domain/repository.go`
      shows doc-comment changes only (Req 3.3, 6.4); no new capability string; `go.mod` and
      `go.sum` unchanged; no new `GRIMOIRE_TEST_*` variable; `git diff main --
      cmd/grimoire/testdata/builtin_golden` empty (Req 15.1). Run `gofmt -l .` (empty),
      `go vet ./...`, `go build ./...`, `go test ./...`, and `go test -race ./internal/content
      ./internal/web ./internal/storage/... ./cmd/grimoire`. **Cross-vendor:** start fresh
      throwaway MySQL 8.0 and PostgreSQL 16 containers as in task 2.2, run the **whole**
      storage suite (`go test ./internal/storage/... -count=1 -v` with both variables set),
      confirm no `PostMenuItem`, `NavMenuDeleteItem` or `PostDependents` subtest is skipped and
      every vendor passes, record the final lines in the PR description, remove the
      containers, and confirm the `storage-contract` CI job is green on the PR before
      merging. Fix anything red in the phase that owns it. Ensure all tests pass, ask the user
      if questions arise.
      Files: (verification only)
      Verify: all gate commands green on SQLite, MySQL and PostgreSQL; the git checks show no
      violation; `docker ps -a` shows no leftover container. _(Req 3.3, 5.4, 6.3, 6.4, 15.1,
      16.2, 16.5)_
## Phase 5 — Storage: PR 2 contracts first (`internal/storage`, TDD) — PR 2
- [~] 5.1 Write the failing storage tests first. (a)
      `RunPostTermRelationshipPurgerContract(t, newRepos NewReposWithPrefixFunc)` in
      `internal/storage/storagetest/postrelationships_contract.go`, registered in
      `sqlite_test.go`, `mysql_test.go` and `postgres_test.go`. Fixture per `design.md`
      "Contract": post `P` related to a `category`, a `post_tag`, a `product_cat` and a
      `post_format` term; post `Q` related to the same terms; a `link_category` term with a
      relationship whose `object_id` equals `P`; stale `count` on every term; ids far above
      `SeedFixtures`. Assert (Req 12.2): `PurgeTermRelationshipsOf(P)` removes all four of
      `P`'s relationships and leaves `Q`'s; the `link_category` row stays; each affected
      term's `count` equals its remaining relationship count and the unaffected terms'
      counts are unchanged; `terms` and `term_taxonomy` rows are untouched; a second call
      changes nothing; a call for an id with no relationships and for a nonexistent id
      return nil. (b) `RunMediaDeleteContract(t, newRepos NewReposWithPrefixFunc)` in
      `internal/storage/storagetest/media_delete_contract.go`, registered in the three
      vendor files, with the fixture of `design.md` "Contract" (attachment `A`; relationships
      in `category`, `product_cat` and `post_format`; two comments, one pending, with
      `commentmeta`; matching menu item `MI` with a child; a non-matching item for
      attachment `B`; posts `P1` with `_thumbnail_id = A` and `P2` with `_thumbnail_id = B`;
      child attachment `C` with `post_parent = A`; a `link_category` relationship with
      `object_id = A`; a page `G`). Assert (Req 8, 9, 12.3): `A`, its postmeta, relationships,
      comments and `commentmeta` are gone; `MI` is gone and its child is re-parented; the
      non-matching item stays; `P1`'s `_thumbnail_id` row is gone and `P2`'s stays; `C` stays
      with `post_parent` still `A`; the `link_category` row stays; counts are recomputed;
      `Delete(G)` and `Delete(unknown)` return `domain.ErrNotFound` with every row of `G`,
      its meta and its comments unchanged. (c) SQLite atomicity tests in
      `internal/storage/wprepo/postrelationships_test.go` and
      `internal/storage/wprepo/media_write_test.go`: drop `term_taxonomy`, call the method,
      assert an error and no row changed. No literal prefix, vendor name or placeholder in
      any contract.
      Files: `internal/storage/storagetest/postrelationships_contract.go`,
      `internal/storage/storagetest/media_delete_contract.go`,
      `internal/storage/storagetest/sqlite_test.go`, `mysql_test.go`, `postgres_test.go`,
      `internal/storage/wprepo/postrelationships_test.go`, `internal/storage/wprepo/media_write_test.go`
      Verify: `go test ./internal/storage/... -run 'PostTermRelationship|MediaDelete|MediaRepoDelete'`
      — fails (compile: `repos.PostRelationships` and the port do not exist; once stubbed, the
      comments, menu item, `_thumbnail_id` row and `product_cat` relationship remain, and a
      page id is deleted by `MediaRepo.Delete`). `grep -n '"wp_"'` on both new contracts finds
      nothing. _(Req 7.1-7.4, 8.1-8.6, 9.1-9.4, 10.2, 10.4-10.6, 11.1, 11.2, 11.5, 12.1-12.3; D4-D7, D10)_
## Phase 6 — Storage: relationships and media (`internal/domain`, `internal/storage`, TDD) — PR 2
- [~] 6.1 Implement the port and the shared relationship helper. In
      `internal/domain/repository.go` add `NonPostObjectTaxonomies` and
      `PostTermRelationshipPurger` with the doc comments of `design.md`. Create
      `internal/storage/wprepo/postrelationships.go` with the unexported
      `purgeRelationshipsTx(ctx, tx, prefix, objectID)` (the `SELECT` joined to `term_taxonomy`
      with `taxonomy NOT IN` placeholders expanded from `domain.NonPostObjectTaxonomies`, then
      one `DELETE` per distinct `term_taxonomy_id`, then `recountTx`) and
      `(*PostRepo) PurgeTermRelationshipsOf` in one `RunInTx`. Add
      `_ domain.PostTermRelationshipPurger = (*PostRepo)(nil)` beside the other assertions in
      `wprepo/writers.go`. In `internal/storage/factory.go` add `PostRelationships
      domain.PostTermRelationshipPurger` to `Set` and set it to `posts` beside `PostMenuItems`.
      The denylist stays in `domain` and is the only place that names `link_category`.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/postrelationships.go`,
      `internal/storage/wprepo/writers.go`, `internal/storage/factory.go`
      Verify: `go test ./internal/storage/... -run PostTermRelationship` — the 5.1(a) contract
      and atomicity test pass on SQLite; `go build ./...` shows no other `Set` construction
      breaks. _(Req 10.1-10.6, 11.1, 11.2, 11.4, 11.5; D6, D7)_
- [~] 6.2 Extend `MediaRepo.Delete`, then verify on all three databases. In
      `internal/storage/wprepo/media.go` keep the one `RunInTx` and run, in order:
      `matchMenuItemsTx(…, id, "attachment")` then `deleteMenuItemTx` per id ascending then
      `recountTx`; `DELETE FROM {p}postmeta WHERE meta_key = '_thumbnail_id' AND meta_value = ?`
      with the decimal id; the `commentmeta` and `comments` deletes written with `bun.Ident` for
      `comment_ID` and `comment_post_ID` as `PurgePostDependents` does; `purgeRelationshipsTx`
      in place of the unconditional relationship delete; the postmeta delete; and the row delete
      with the added `post_type = 'attachment'` predicate, zero rows returning
      `domain.ErrNotFound` and rolling the transaction back. Update the doc comments of
      `MediaRepo.Delete` and `domain.MediaWriter.Delete`. Do not touch `MediaWriteService`,
      the on-disk unlink or the child attachments' `post_parent`. **Cross-vendor verification
      (Requirement 5.4), not optional:** as in task 2.2, run `go test ./internal/storage/...
      -count=1 -v` against fresh `g20-*` containers, confirm `PostTermRelationship` and
      `MediaDelete` subtests pass under `mysql` and `postgres` and are not skipped, remove the
      containers, and paste the commands and pass lines into the PR description. If a vendor
      fails, fix the statement and re-run all three; the mixed-case `comment_ID` columns and
      the `ID` key are the likely PostgreSQL traps.
      Files: `internal/storage/wprepo/media.go`, `internal/domain/repository.go`
      Verify: `go test ./internal/storage/...` — 5.1 passes on SQLite; the container run passes
      on MySQL 8.0 and PostgreSQL 16; `TestMediaRepoDeleteRemovesRowMetaRelationships` and
      `TestMediaRepoDeleteUnknownReturnsNotFound` stay green; no leftover container.
      _(Req 7.1-7.4, 8.1-8.6, 9.1-9.4, 12.4; D4-D6)_
## Phase 7 — Content and assembly: the delete step and the media route (`internal/content`, `cmd/grimoire`, TDD) — PR 2
- [~] 7.1 Write the failing service tests first. Add
      `internal/content/relationships_delete_test.go` over the `PostWriteService` fakes with a
      recording `PostTermRelationshipPurger`, detacher, option ports, dependents purger,
      menu-item purger and `PostWriter`: deleting a `post`, a `page` and a registered-type row
      calls `PurgeTermRelationshipsOf(id)` once (Req 10.7); the recorded order is revisions,
      managed term detach, relationship purge, unstick (post only), dependents purge,
      menu-item purge, row delete (D8); a purger error returns it, records no later step and
      no row delete, and a second `Delete` with a working purger completes (10.8); an
      unwritable type, a forbidden actor and a missing id call nothing; with no purger
      configured the delete behaves as before M20 (10.9). Extend the existing delete-order tests
      only by inserting the new step, and say so in the commit.
      Files: `internal/content/relationships_delete_test.go`
      Verify: `go test ./internal/content -run RelationshipsDelete` — fails (compile:
      `WithPostRelationshipPurger` does not exist; once stubbed, no purge call is recorded).
      _(Req 10.7-10.9; D8)_
- [~] 7.2 Implement the delete step. In `internal/content/writeservices.go` add
      `WithPostRelationshipPurger(p domain.PostTermRelationshipPurger) PostWriteOption` and the
      call `PurgeTermRelationshipsOf(ctx, cur.ID)` in `Delete` after the managed term detach and
      before the unstick step, returning its error. Update the `Delete` doc comment's step list.
      Files: `internal/content/writeservices.go`
      Verify: `go test ./internal/content` — 7.1 passes, every existing test green;
      `go test ./cmd/grimoire -run BuiltinGolden` green. _(Req 10.7-10.9; D7, D8)_
- [~] 7.3 Write the failing assembly tests first. Add
      `cmd/grimoire/assembly_delete_cleanup_test.go` over `newCustomWriteHandler`, with
      `seedRawTerm`, `seedRawTermRelationship` and the menu helpers. (a) `DELETE
      /wp-json/wp/v2/pages/{P}`, the posts route and a registered-type route (with
      `withRegistry`) for rows related to `category`, `product_cat` and `post_format` terms:
      each answers `200 {deleted, previous}` and the relationships are gone for the deleted id
      only, other posts' relationships remain, each affected term's `count` equals its
      remaining relationship count, and a `link_category` row with the same `object_id` stays
      (Req 10.2, 11.1, 11.2, 11.5). (b) `DELETE /wp-json/wp/v2/media/{A}?force=true` as an
      administrator for an attachment with comments, a matching menu item with a child, a
      `_thumbnail_id` reference on a post and a `product_cat` relationship: answers `200`, the
      comments, `commentmeta`, menu item, reference and relationship are gone, the child
      is re-parented and shown by `GET /admin/api/menus/{M}`, another attachment's reference and
      items stay, and a child attachment keeps its `post_parent` (Req 7, 8, 9, 13.1, 13.4).
      (c) A failing relationship purger (wrapped through the `assemblyHandler` builder) answers
      `500` on a post delete, the row still exists with its relationships, and a retry with the
      working purger succeeds (10.8). (d) A forbidden actor and an unknown media id answer `403`
      and `404` with rows unchanged. No built-in golden file changes.
      Files: `cmd/grimoire/assembly_delete_cleanup_test.go`
      Verify: `go test ./cmd/grimoire -run DeleteCleanup` — fails: the `product_cat`
      relationship and its count remain after a post delete, and the media comments, menu item and
      `_thumbnail_id` row remain after a media delete.
      _(Req 7-11, 13.1, 13.4; D4-D8)_
- [~] 7.4 Wire the purger and prove the wiring. In `cmd/grimoire/assembly.go` add
      `content.WithPostRelationshipPurger(repos.PostRelationships)` to the `NewPostWriteService`
      call, after `WithPostMenuItemPurger`. No other assembly change. Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 7.3 passes. **Proof of wiring:** remove
      `WithPostRelationshipPurger` locally; the post subtests of `TestBuildHandlerDeleteCleanup`
      must fail on the relationship assertion; restore. **Proof for the media change, which needs
      no option:** comment out the `_thumbnail_id` delete in `MediaRepo.Delete` locally; the media
      subtest must fail on the reference assertion; restore. Record both results in the PR
      description. Golden green. _(Req 10.10, 13.2, 13.3)_
## Phase 8 — Documentation and PR 2 gate — PR 2
- [~] 8.1 Update the documents to match PR 2. `docs/compatibility.md`: remove the M19 PR 3
      "Known gaps" bullets "Deleting a media item does not clean menu items." and
      "Relationships in unmanaged taxonomies stay on a deleted post."; add "M20 — Delete gap
      closure (PR 2)": what a media delete now removes (menu items with children re-parented and
      counts recomputed, comments and `commentmeta`, `_thumbnail_id` references on other posts,
      relationships in every taxonomy except `link_category` with recounts) and what it leaves
      (child attachments' `post_parent`, theme-mod references, intermediate image files); what the
      post delete's new relationship step removes, why `link_category` is excluded and the
      residual plugin-taxonomy risk; the corrected delete sequence (revisions, managed detach,
      remaining-relationship purge, unstick, dependents purge, menu-item purge, row delete); the
      `post_type` guard on the media row delete; the relationship-count rule; and the gaps still
      open (trash and restore, hooks and the object cache, theme mods, image sizes, published-only
      counts, plugin taxonomies of non-post objects, the `sticky_posts` read-modify-write limit).
      Fix the M18 PR 2 "Delete cleanup" sequence to include the new step. `plans/post-m10-gap-analysis/README.md`:
      remove the entries for media-delete menu cleanup and unmanaged-taxonomy relationships and add
      the "Shipped in M20 (PR 2)" paragraph. `plans/README.md`: set the row to
      "✅ Implemented (2 of 2 shipped)" with the final summary. Run `grep -rn "Deleting a media
      item does not\|stay on a deleted post\|unmanaged taxonomies stay" docs README.md plans` and fix
      each statement this PR makes stale, leaving `plans/17-*`, `plans/18-*` and `plans/19-*`
      untouched.
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`, `plans/README.md`
      Verify: `grep -n "does not clean menu items" docs/compatibility.md` finds nothing; the plans
      README row reads Implemented. _(Req 17.2-17.5)_
- [~] 8.2 Final gate for PR 2, with explicit cross-vendor verification. Confirm: no new file under
      `internal/storage/migrations`; `git diff main -- internal/domain/repository.go` adds
      exactly one interface (`PostTermRelationshipPurger`) and one variable
      (`NonPostObjectTaxonomies`) and changes no method of an existing interface (Req 14.3); no new
      capability string; `go.mod` and `go.sum` unchanged; no new `GRIMOIRE_TEST_*` variable; `git diff
      <PR 1 merge base> -- cmd/grimoire/testdata/builtin_golden` empty (Req 14.2). Run `gofmt -l .`
      (empty), `go vet ./...`, `go build ./...`, `go test ./...`, and `go test -race
      ./internal/content ./internal/web ./internal/storage/... ./cmd/grimoire`. **Cross-vendor:** start
      fresh throwaway MySQL 8.0 and PostgreSQL 16 containers as in task 2.2, run the **whole**
      storage suite with both variables set, confirm no `PostTermRelationship`, `MediaDelete`,
      `PostMenuItem`, `NavMenuDeleteItem` or `PostDependents` subtest is skipped and every vendor
      passes, record the final lines in the PR description, remove the containers, and confirm the
      `storage-contract` CI job is green on the PR before merging. Fix anything red in the phase
      that owns it. Ensure all tests pass, ask the user if questions arise.
      Files: (verification only)
      Verify: all gate commands green on SQLite, MySQL and PostgreSQL; the git checks show no
      violation; `docker ps -a` shows no leftover container. _(Req 5.4, 12.4, 14.2, 14.3, 16.2,
      16.5)_
## Notes
- Tasks marked with `*` are optional; **no sub-task here is optional**. The re-parenting
  matrix, the media and relationship contracts, the cross-vendor runs and the assembly tests are
  the milestone's done-definition (Requirement 16).
- **No property-based tests.** The behaviors are enumerable branches over fixed fixtures with
  named orderings and one denylist entry. See `design.md` Testing strategy.
- **TDD exceptions are explicit:** 3.2 (wiring proof, no new code), 4.1 and 8.1 (docs), 4.2 and
  8.2 (gates).
- **Wiring is tested where it is wired.** PR 1 adds no option, and task 3.2 proves the tests fail
  when the storage change is reverted. Task 7.4 pairs the one new option with a removal proof and
  proves the media change by reverting one statement.
- **Task 7.3 runs before 6.2 and 7.4 on purpose**, so the post and media subtests are both seen RED
  first; the task records the failing subtests at its own run.
- **Golden files.** Neither PR changes them. If `TestBuiltinGolden` fails, stop and find the
  cause; do not pass `-update`.
- **Stale-assertion edits are narrow.** The one known stale assertion is the M19 contract's
  "child's parent is not rewritten" (task 1.1). If another test asserts that a child keeps a deleted
  parent or that a media delete leaves comments or menu items, change only that assertion in the phase
  that owns the behavior and say so in the commit.
- **Storage tests never hardcode a prefix, a vendor or a placeholder.** They take
  `NewReposWithPrefixFunc`, use the vendor and prefix it returns, and run on MySQL and PostgreSQL
  containers before each PR opens (tasks 2.2, 4.2, 6.2 and 8.2).
- **Open sign-off items** are listed in `design.md` "Decisions awaiting sign-off". Defaults are
  implemented as written; reversing one is local to the task named in each item.
- **Not done here, recorded as gaps:** trash and restore (its own future spec), WordPress hooks and
  the object cache, theme-mod references to a deleted attachment, intermediate image files,
  published-only term counts, and plugin taxonomies of non-post objects.
## Task Dependency Graph
```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "3.1"] },
    { "id": 1, "tasks": ["2.1"] },
    { "id": 2, "tasks": ["2.2"] },
    { "id": 3, "tasks": ["3.2"] },
    { "id": 4, "tasks": ["4.1"] },
    { "id": 5, "tasks": ["4.2"] },
    { "id": 6, "tasks": ["5.1", "7.1", "7.3"] },
    { "id": 7, "tasks": ["6.1"] },
    { "id": 8, "tasks": ["6.2", "7.2"] },
    { "id": 9, "tasks": ["7.4"] },
    { "id": 10, "tasks": ["8.1"] },
    { "id": 11, "tasks": ["8.2"] }
  ]
}
```
