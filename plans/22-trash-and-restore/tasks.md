# Implementation Plan: M22 — Trash and restore
## Overview
> **This milestone makes deletes recoverable and WordPress-compatible.** Today `DELETE` on a post,
> page or registered-type row is permanent. After M22 the REST `DELETE` and the admin `DELETE` move
> the row to the `trash` status in the exact representation WordPress writes, `?force=true` keeps
> the permanent delete (the M18-M20 sequence, unchanged), a status update restores, the React admin
> gets a Trash view, and a scheduler task purges rows after `content.trash_days` (30). No
> migration, no capability name, no dependency. Two additive storage ports.
>
> **Three pull requests, each a complete increment, each with its own `buildHandler` or
> `buildScheduler` test and a final gate task.**
>
> | PR | Phases | Increment |
> |---|---|---|
> | 1 (core and REST) | 1-6 | `PostTrasher` port and `PostRepo` implementation (trash rows, slug suffix, comments, term counts), `PostWriteService.Trash`/`Restore` and capability checks on the previous status, REST `DELETE`/`PUT`/`PATCH`/`GET ?status=`, reads that hide trash, public menu pruning, goldens, real-database check, docs, gate |
> | 2 (admin API and SPA) | 7-9 | Admin `DELETE` trashes by default, `?force=true`, restore and empty-trash routes, `trashEnabled`, the Trash view in the React admin, goldens (additions only), docs, gate |
> | 3 (purge, config, docs) | 10-12 | `content.trash_days`, `TrashedPostLister`, hourly purge through the permanent-delete sequence, final documentation and gap analysis, gate |
>
> PR 2 branches from `main` after PR 1 merges, PR 3 after PR 2.
>
> **BEHAVIOR CHANGE.** After PR 1, a `DELETE` without `?force=true` on a post, page or registered
> type is no longer permanent. This is the one sign-off item that changes existing clients
> (`design.md` "Decisions awaiting sign-off", item 1). The migration note is task 6.4.
>
> **What the source check changed in the brief** (`requirements.md` Introduction): restore defaults
> to `draft`, not to the previous status; registered types trash by default over REST; one capability
> gates trash and force; capabilities on a trashed row use its previous status; `future` is treated
> like a draft by the existing policy.
>
> **Strict-TDD spine.** Every behavior-adding implementation task is preceded by the task that writes
> its failing test, and that test is run and seen to fail **for the stated reason** before the
> implementation starts. A test that fails only because a new symbol does not compile counts as RED.
> Documentation, verification and gate tasks have no RED step and say so.
>
> **Storage handling.** Every contract takes `storagetest.NewReposWithPrefixFunc`, reads the vendor
> and prefix it returns, and holds no literal prefix, vendor name or placeholder. Each PR that
> touches storage runs the whole storage suite against throwaway MySQL 8.0 and PostgreSQL 16
> containers in its implementation task and again in its final gate (`design.md` Testing strategy has
> the commands).
>
> **Standing gate convention.** After every phase: `gofmt -l .` empty, and `go vet ./...`,
> `go build ./...`, `go test ./...` green. `TestBuiltinGolden` runs in every gate; a failure there is
> a stop, not an `-update`, except in the golden tasks 6.3 and 9.3.
>
> **Fixtures.** Assembly tests extend `cmd/grimoire/assembly_hardening_fixture_test.go`
> (`seedRawPost`, `seedRawMeta`, `seedRawTerm`, `seedRawTermRelationship`, `seedRawMenuItem`,
> `rowSnapshot`) and `assembly_custom_fixture_test.go` (`newCustomWriteHandler(t, withRegistry)`,
> `newCustomWriteHandlerWith`, `doJSON`, the `customWriteEnv` actors, `Repos`, `Prefix`). The M20
> `assembly_delete_cleanup_test.go` is the model. Storage contracts follow
> `postrelationships_contract.go` and `postdependents_contract.go`. Property tests follow
> `internal/sanitize/properties_test.go` (`testing/quick`, seeded `rand`).
## Tasks
## Phase 1 — Pure foundations (`internal/domain`, `internal/php`, `internal/storage/wprepo`, TDD) — PR 1
- [x] 1.1 Write the failing pure tests first. (a) `internal/php/serialize_test.go`: serialize
      `map[int]string{12:"1", 7:"0", 30:"spam"}` and assert the exact bytes
      `a:3:{i:7;s:1:"0";i:12;s:1:"1";i:30;s:4:"spam";}`; an empty map gives `a:0:{}`; and
      **Property 5** (`TestPropertySerializeIntMapRoundTrip`, `testing/quick`, seeded, `MaxCount: 200`,
      tag `// Feature: trash-and-restore, Property 5: Integer-keyed PHP arrays round-trip`): for any
      `map[int]string`, `php.Unserialize(php.Serialize(m))` returns the same pairs with keys compared as
      decimal strings, and the keys appear in ascending numeric order in the output. (b)
      `internal/storage/wprepo/posttrash_test.go`: examples for `trashedName` (empty gives
      `__trashed`; `hello` gives `hello__trashed`; a 191-byte name is kept whole; a 192-byte name is cut
      to 191; a name whose byte 190-192 is `%E2` cuts before the `%`; a multibyte name never splits a
      percent escape) and **Property 1** (`TestPropertyTrashedName`, tag `Property 1: Trashed names have
      the WordPress shape`): for any string, the result ends in `__trashed`, the part before it is a
      byte-prefix of the input at most 191 bytes long, ends inside no `%XX` escape of the input, and
      equals the input when the input is at most 191 bytes. (c) `internal/domain/errors_test.go` (or the
      existing errors test): `errors.Is(domain.ErrAlreadyTrashed, domain.ErrNotFound)` is false and the
      two new errors are distinct.
      Files: `internal/php/serialize_test.go`, `internal/storage/wprepo/posttrash_test.go`,
      `internal/domain/errors_test.go`
      Verify: `go test ./internal/php ./internal/storage/wprepo ./internal/domain -run
      'IntMap|TrashedName|Trashed'` — fails (compile: `trashedName`, `domain.ErrAlreadyTrashed` do not
      exist; once stubbed, `php.Serialize` returns `unsupported type map[int]string`).
      _(Req 1.2, 4.5, 26.1, 26.2)_
- [x] 1.2 Implement the foundations. In `internal/php/serialize.go` add a `map[int]string` case that
      writes `a:N:{i:KEY;s:LEN:"VALUE";...}` with keys sorted ascending. In
      `internal/domain/errors.go` add `ErrAlreadyTrashed` and `ErrNotTrashed`. In
      `internal/storage/wprepo/posttrash.go` add the unexported pure `truncateSlug(name string, max int)
      string` (cut at `max` bytes; when the cut lands inside a `%XX` escape, cut before the `%`) and
      `trashedName(name string) string` (`truncateSlug(name, 191) + "__trashed"`).
      Files: `internal/php/serialize.go`, `internal/domain/errors.go`,
      `internal/storage/wprepo/posttrash.go`
      Verify: `go test ./internal/php ./internal/storage/wprepo ./internal/domain` — 1.1 passes;
      `go build ./...` green. _(Req 1.2, 4.5)_
## Phase 2 — Storage: counts, admin reads, comments, then the trasher (`internal/storage`, TDD) — PR 1
- [x] 2.1 Write the failing tests for the count rule, the admin reads and the comment filter. (a)
      `RunTermCountContract(t, newRepos NewReposWithPrefixFunc)` in
      `internal/storage/storagetest/termcount_contract.go`, registered in `sqlite_test.go`,
      `mysql_test.go` and `postgres_test.go`: a term related to a published, a draft and a trashed post
      and to a `link_category` object whose id is not a post; assert that `SetPostTerms`,
      `PurgeMenuItemsOf`, `NavMenuRepo.DeleteItem` and `PurgeTermRelationshipsOf` each leave `count`
      equal to the relationships minus the trashed object (the draft and the non-post object count),
      and that the M19 and M20 fixtures' expected counts are unchanged. (b) **Property 6**
      (`TestPropertyRecountIgnoresTrashed` in `internal/storage/wprepo/termcount_property_test.go`, tag
      `Property 6: The term count ignores trashed objects`; 100 iterations over one migrated SQLite
      database, fresh high ids per iteration): generate posts with statuses {`publish`, `draft`,
      `trash`}, relationships and non-post relationships, call `recountTx`, compare to the Go model.
      (c) Extend `admin_contract.go`: `ListForAdmin` and `CountForAdmin` with empty `Statuses` omit
      trashed rows, with `Statuses: ["trash"]` return only them; `CountByStatus(page, "")` omits
      trashed rows and `CountByStatus(page, "trash")` counts them; and **Property 9**
      (`TestPropertyAdminListExcludesTrash`, tag `Property 9: The default admin list excludes trash`).
      (d) Extend `comments_contract.go`: a comment with `comment_approved = 'post-trashed'` is absent
      from `List` and `Count` with empty `Statuses` and present when `Statuses` names it. No literal
      prefix, vendor or placeholder in any contract; `grep -n '"wp_"'` on the new contract finds nothing.
      Files: `internal/storage/storagetest/termcount_contract.go`,
      `internal/storage/storagetest/admin_contract.go`, `internal/storage/storagetest/comments_contract.go`,
      `internal/storage/storagetest/sqlite_test.go`, `mysql_test.go`, `postgres_test.go`,
      `internal/storage/wprepo/termcount_property_test.go`,
      `internal/storage/wprepo/adminreads_property_test.go`
      Verify: `go test ./internal/storage/... -run 'TermCount|AdminPost|PostCounter|Comments|Property'` —
      fails: the trashed object is counted, "All" returns the trashed row, `CountByStatus(page, "")`
      includes it, and the `post-trashed` comment is listed. _(Req 4.6, 5.1, 5.2, 5.4, 10.1, 10.3, 10.5;
      D2, D8; sign-off 8)_
- [x] 2.2 Implement the count rule, the admin read filter and the comment filter. Create
      `internal/storage/wprepo/termcount.go` with the shared statement of `design.md` "Term count
      rule" and make `recountTx` call it; add a `vendor` parameter to `recountTx` and update its callers
      in `menuitemtx.go`, `postrelationships.go` and `media.go`; make `SetPostTerms` in `postterms.go`
      use the same statement. In `adminreads.go` add `applyAdminStatuses` (explicit statuses, else
      `post_status <> 'trash'`) used by `ListForAdmin` and `CountForAdmin`, and make `CountByStatus`
      with an empty status exclude `trash`. In `comments.go` add `comment_approved <> 'post-trashed'` to
      `List` and `Count` when `f.Statuses` is empty. Update the doc comments of
      `domain.AdminPostFilter.Statuses` and `domain.PostCounter.CountByStatus`. Build identifiers with
      `rebind.Ident(vendorOf(r.db), "ID")` and add no literal vendor branch.
      Files: `internal/storage/wprepo/termcount.go`, `menuitemtx.go`, `postrelationships.go`, `media.go`,
      `postterms.go`, `adminreads.go`, `comments.go`, `internal/domain/repository.go`
      Verify: `go test ./internal/storage/...` — 2.1 passes on SQLite and every M18-M20 contract still
      passes. _(Req 4.6, 5.1, 5.2, 10.1, 10.3, 10.5)_
- [x] 2.3 Write the failing tests for the trasher. (a) `RunPostTrasherContract(t, newRepos
      NewReposWithPrefixFunc)` in `internal/storage/storagetest/posttrash_contract.go`, registered in
      the three vendor files. Seed (ids far above `SeedFixtures`, vendor- and prefix-aware helpers):
      a published post `P` with name `hello`, a `category` and a `product_cat` relationship with stale
      counts, a published sibling in the same category, comments `7` (`1`) and `8` (`0`) with commentmeta,
      a revision, a child page, a menu item, and a draft, a row with an empty name, a row with a
      195-byte name, a row already named `x__trashed`, a row with stale `_wp_trash_meta_*` rows, and a
      row with no comments. Assert after `TrashPost(P, T)` exactly the six rows of `design.md` "Data
      Models" ("WordPress representation, row by row") and that relationships, revision, child, menu
      item, other postmeta and every other column are unchanged, counts recomputed, previous status
      returned, a missing id returns `ErrNotFound`, an already-trashed id returns `ErrAlreadyTrashed`
      with nothing changed. Restore cases (Req 3.6): with desired meta; without it (suffix stripped);
      slug taken by a live row of the same type (`-2`); taken by a live row of another type (no suffix);
      taken under another parent for a hierarchical scope (no suffix); an explicit slug; restore to
      `publish` and `draft`; comments restored (a recorded `post-trashed` becomes `0`, a missing comment
      skipped, a spam comment stays spam); a non-trashed row returns `ErrNotTrashed`; a missing id
      returns `ErrNotFound`; a WordPress-style percent-encoded name restores unchanged. `TrashInfo`:
      all present, all absent, duplicate meta rows (lowest `meta_id`). (b) Atomicity in
      `internal/storage/wprepo/posttrash_test.go`: drop `comments`, call `TrashPost`, assert an error and
      that every row, meta row and count is unchanged; the same for `RestorePost` with `term_taxonomy`
      dropped. (c) Properties in `internal/storage/wprepo/posttrash_property_test.go` (100 iterations
      each, one migrated SQLite database, fresh high ids): **Property 2**
      `TestPropertyTrashRestoreRoundTrip` (tag `Property 2: Trash then restore round-trips a live row`),
      **Property 3** `TestPropertySecondTrashChangesNothing`, **Property 4** `TestPropertyRestoredNameIsFree`.
      No literal prefix, vendor or placeholder; `grep -n '"wp_"'` on the new contract finds nothing.
      Files: `internal/storage/storagetest/posttrash_contract.go`,
      `internal/storage/storagetest/sqlite_test.go`, `mysql_test.go`, `postgres_test.go`,
      `internal/storage/wprepo/posttrash_test.go`, `internal/storage/wprepo/posttrash_property_test.go`
      Verify: `go test ./internal/storage/... -run 'PostTrasher|PostTrash|Property'` — fails (compile:
      `repos.PostTrash` and the port do not exist; once stubbed, the row is not renamed, no meta row is
      written, the comments stay approved). _(Req 1.1-1.9, 2.1-2.9, 3.1, 3.3-3.6, 4.1-4.4, 5.3; D2, D3,
      D5, D8; sign-off 7)_
- [x] 2.4 Implement the port and the trasher. In `internal/domain/repository.go` add `TrashInfo`,
      `RestoreRequest` and `PostTrasher` with the doc comments of `design.md`. In
      `internal/storage/wprepo/posttrash.go` implement `(*PostRepo) TrashPost`, `RestorePost` and
      `TrashInfo` with the statements of `design.md` "TrashPost statements" and "RestorePost
      statements", each in one `RunInTx`, with the unexported `relatedTermTaxonomyIDsTx` (the
      relationship select of `purgeRelationshipsTx`, factored out and shared), `uniqueName` and the
      comment helpers. Add `_ domain.PostTrasher = (*PostRepo)(nil)` in `wprepo/writers.go` and
      `PostTrash domain.PostTrasher` to `storage.Set` in `internal/storage/factory.go`, set to the
      `posts` repo beside `PostRelationships`. Use `time.Time` supplied by the caller for every
      timestamp and `formatTS` as `PostRepo.Update` does.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/posttrash.go`,
      `internal/storage/wprepo/postrelationships.go`, `internal/storage/wprepo/writers.go`,
      `internal/storage/factory.go`
      Verify: `go test ./internal/storage/...` — 2.3 passes on SQLite (contract, atomicity and
      Properties 2-4); `go build ./...` shows no other `Set` construction breaks. _(Req 1, 2, 3.1, 3.2,
      4.1-4.4, 5.3; D2, D3, D5)_
- [x] 2.5 Verify on all three databases. **Cross-vendor verification (Requirement 3.7), not
      optional:** start throwaway containers with the CI image tags and DSNs using the commands in
      `design.md` "Testing strategy" (names `g22-mysql`, `g22-pg`; adjust host ports if 3306 or 5432 is
      busy), run `go test ./internal/storage/... -count=1 -v` with both `GRIMOIRE_TEST_*` variables
      set, confirm the `PostTrasher`, `TermCount`, `AdminPost`, `Comments` and every M18-M20 subtest
      **pass under `mysql` and `postgres`** and are not skipped, then `docker rm -f` both containers.
      Paste the commands and the pass lines into the PR description. If a vendor fails, fix the
      statement (not the test) and re-run all three; the likely traps are the mixed-case
      `comment_ID` columns on PostgreSQL, `longtext` meta compares on MySQL and the `"ID"` quoting in
      the count subquery. No RED step (verification).
      Files: (verification only)
      Verify: the container run passes on MySQL 8.0 and PostgreSQL 16; `docker ps -a` shows no
      leftover `g22-*` container. _(Req 3.3, 3.4, 3.7; sign-off 12)_
## Phase 3 — Service: Trash, Restore, Update, Delete (`internal/content`, TDD) — PR 1
- [x] 3.1 Write the failing service tests first, in `internal/content/trash_test.go` over the
      `PostWriteService` fakes with a recording `PostTrasher`, option ports and `PostWriter`. (a)
      `Trash` of a `post`, a `page` and a registered type calls `TrashPost` once; order is capability,
      enabled check, already-trashed check, `unstick` (type `post` only), `TrashPost`; a failing
      `unstick` records no store call; no revision, dependent, menu or relationship step runs (Req 6.6,
      11.1, 11.2). (b) Errors: a missing row, an unmanaged type and a forbidden actor return
      `ErrForbidden` before `ErrTrashUnsupported` (no store or trash days 0) and before
      `domain.ErrAlreadyTrashed` (Req 6.3-6.5, 6.12). (c) Update transition table (Req 6.7-6.9): live to
      live; live to `trash` (fields applied with status unchanged, then `Trash`; requires the delete
      capability, so an actor with only the edit capability gets `ErrForbidden`); `trash` to `trash`;
      `trash` to `draft` and to `publish` (fields applied first, then `RestorePost` with the
      `RestoreRequest` slug rule: empty when the request kept the stored suffixed name, explicit when
      it differs; `publish` needs the publish capability). (d) `Restore` status resolution: empty gives
      `draft`, `previous` gives the recorded status or `draft` when absent, `ErrNotTrashed` for a live
      row. (e) `Delete` of a trashed row evaluates the delete capability on the previous status and still
      runs the M18-M20 steps in the same order; the order test is extended only by this case. (f)
      `slugTaken` ignores nodes with status `trash` (unit test in `pagehier_test.go`). (g) **Property 7**
      (`TestPropertyTrashedRowAuthorizedAsPreviousStatus` in `trash_property_test.go`, tag `Property 7: A
      trashed row is authorized as its previous status`): over generated capability sets, ownership and
      previous statuses, the decision for a trashed row equals the decision for a live row of that
      status, for `Delete` and for `Update`-out-of-trash.
      Files: `internal/content/trash_test.go`, `internal/content/trash_property_test.go`,
      `internal/content/pagehier_test.go`
      Verify: `go test ./internal/content -run 'Trash|Restore|TrashedRow|SlugTaken'` — fails (compile:
      `WithPostTrasher`, `Trash`, `Restore` do not exist; once stubbed, `Update` to `trash` writes the
      status directly and a trashed row is authorized as `trash`). _(Req 6.1-6.12, 11.1-11.3, 13.2; D4,
      D5; sign-off 4)_
- [x] 3.2 Implement the service. In `internal/content/writeservices.go` add `ErrTrashUnsupported`,
      `WithPostTrasher`, `WithTrashDays` (default 0), `TrashEnabled`, `Trash`, `Restore`, the private
      `effectiveStatus` and `restoreRow`, the `Update` transitions of `design.md` "`Update`
      transitions", a `ByID` pass-through to the writer, and the `Delete` capability line using
      `effectiveStatus`. Do not edit the step order, ports or error handling of `Delete`. In
      `internal/content/pagehier.go` make `slugTaken` skip `trash` nodes. Update the doc comments of
      `Update` and `Delete`.
      Files: `internal/content/writeservices.go`, `internal/content/pagehier.go`
      Verify: `go test ./internal/content` — 3.1 passes and every existing test (including the M18-M20
      delete-order tests) passes unchanged; `go test ./cmd/grimoire -run BuiltinGolden` green. _(Req 6,
      11, 13.2; D4, D5)_
## Phase 4 — REST: delete, restore, reads (`internal/web`, TDD) — PR 1
- [x] 4.1 Write the failing handler tests first, in `internal/web/rest_trash_test.go` over fakes for
      `postTrashWriter` and the post readers. (a) `DELETE` without force trashes and answers `200` with
      the post object (`status: "trash"`) for `posts`, `pages` and a registered `rest_base`; with
      `force=true` and with `force=1` it calls `Delete` and answers `200 {deleted, previous}`; force on
      a trashed row deletes (Req 7.1, 7.2, 7.7). (b) Errors: `410 rest_already_trashed`, `501
      rest_trash_not_supported` (service error and nil `postTrash`), `403 rest_cannot_delete` for a
      forbidden actor on both paths, `404 rest_post_invalid_id`, `500 rest_delete_failed`; the order
      403 before 501/410 (Req 7.3-7.6, 14.2). (c) `PUT`/`PATCH` with `status: trash` on a live row and
      with another status on a trashed row reach the service; `POST` with `status: trash` is rejected;
      a sparse `PATCH` on a trashed row keeps it trashed (Req 7.8). (d) `parseRESTStatuses` table test:
      absent and `publish` give `["publish"]`; `any` gives the five live statuses; `trash`, lists with
      commas; an unknown token gives `400 rest_invalid_param`; a non-edit caller with any other value
      gives `403 rest_forbidden_status` (Req 8.2-8.5). (e) The collection passes explicit `Statuses` to
      both `ListForAdmin` and `CountForAdmin` and the headers agree (Req 8.6). (f) Media, user, comment
      and term `DELETE` are unchanged. Update, in the same commit and saying so, every existing test
      that asserts `rest_cannot_edit` for a forbidden post delete (sign-off 2).
      Files: `internal/web/rest_trash_test.go`, the existing `internal/web/rest_*_test.go` files that
      assert the old forbidden code
      Verify: `go test ./internal/web -run 'RESTTrash|RESTDelete|RESTStatuses'` — fails (compile:
      `WithPostTrash` and `parseRESTStatuses` do not exist; once stubbed, the row is deleted, the
      forbidden code is `rest_cannot_edit`, and `status` is ignored). _(Req 7.1-7.10, 8.2-8.6, 14.2;
      D1; sign-off 1, 2)_
- [x] 4.2 Implement REST. In `internal/web/rest_delete.go` or `rest_posts.go` add the `postTrashWriter`
      interface (`Trash`, `Restore`, `TrashEnabled`), `Server.WithPostTrash`, and rewrite the tail of
      `handleRESTPostDelete` per `design.md` "`DELETE` flow", with the error mapping and messages
      listed there. Add `validUpdateStatuses` (the valid set plus `trash`) used by `parseRESTPostWrite`
      for updates only, `parseRESTStatuses`, and the `status` handling in `handleRESTPostsCollection`
      (also reached by registered types through `rest_custom_posts.go`). Leave media, users, comments,
      terms and the single-item `GET` code unchanged. A nil `postTrash` answers a non-force `DELETE`
      with `501` and deletes nothing.
      Files: `internal/web/rest_posts.go`, `internal/web/rest_custom_posts.go`,
      `internal/web/rest_delete.go`, `internal/web/adminroutes.go`
      Verify: `go test ./internal/web` — 4.1 passes, every existing test green; `go test ./cmd/grimoire
      -run BuiltinGolden` fails only on the three entries of `design.md` "Golden changes" (expected; the
      golden task is 6.3, do not update here). _(Req 7, 8.2-8.6, 14.2; D1)_
## Phase 5 — Public menus (`internal/domain`, `internal/storage`, `internal/content`, TDD) — PR 1
- [x] 5.1 Write the failing tests first. (a) `internal/content/menus_prune_test.go`:
      `pruneInvalidItems` trees (top-level removed, nested removed, a chain of removed items, all
      removed, none removed, children order kept) and **Property 8**
      (`TestPropertyPruneInvalidItems`, tag `Property 8: Pruning trashed menu items keeps the rest in
      place`). (b) `NavMenuService` tests: `ByLocation` and `BySlug` prune; `List` and `Get` do not (Req
      9.2, 9.3). (c) Extend `menus_contract.go` (`NewReposWithPrefixFunc`): a menu with items for a
      published page, a trashed page and a custom link; the repo marks `TargetTrashed` only for the
      trashed target, returns the item with its stored label, and a restore (status back to `publish`)
      clears the mark; no stored row changes (Req 9.4). Add `NavMenuItem.TargetTrashed bool` to
      `entities.go` in this task so the tests compile.
      Files: `internal/content/menus_prune_test.go`, `internal/storage/storagetest/menus_contract.go`,
      `internal/storage/storagetest/sqlite_test.go`, `mysql_test.go`, `postgres_test.go`,
      `internal/domain/entities.go`
      Verify: `go test ./internal/content ./internal/storage/... -run 'Prune|NavMenu|Menus'` — fails
      (`pruneInvalidItems` does not exist; once stubbed, the repo never sets `TargetTrashed` and the
      public menu still links to the suffixed name). _(Req 9.2-9.4; sign-off 9)_
- [x] 5.2 Implement. In `wprepo/menus.go` extend the `resolveMenuTarget` query with `post_status` and
      set `TargetTrashed` when it is `trash`; in `internal/content/menus.go` add `pruneInvalidItems`
      and call it from `ByLocation` and `BySlug` only.
      Files: `internal/storage/wprepo/menus.go`, `internal/content/menus.go`
      Verify: `go test ./internal/content ./internal/storage/...` — 5.1 passes on SQLite, every menu
      contract still passes. _(Req 9.2-9.4)_
## Phase 6 — Assembly, goldens, real database, documentation and PR 1 gate — PR 1
- [x] 6.1 Write the failing assembly tests first. Add `cmd/grimoire/assembly_trash_test.go` over
      `newCustomWriteHandler` (with and without registry) and raw seeds. Cover, through the real
      `buildHandler`: (a) REST `DELETE` of a post, a page and a registered-type row answers `200` with
      `status: "trash"`, writes the rows of `design.md` "Data Models" (use `rowSnapshot`), recomputes a
      category count, unsticks a sticky post, leaves revisions, children and menu items; `DELETE
      ?force=true` on a live and on a trashed row removes the row, revisions, `_wp_trash_meta_*`,
      `_wp_desired_post_slug`, `_wp_trash_meta_comments_status` and every comment including
      `post-trashed` ones, through the M18-M20 sequence (Req 7, 11, 13). (b) Restore by `PATCH
      {"status":"draft"}`: slug without suffix, trash meta gone, comments restored, a live row holding
      the slug gives `-2`, a contributor cannot restore or delete a formerly published row while an
      author can, an editor with `edit_posts` only cannot trash through `status: trash` (Req 6.2, 7.8).
      (c) Reads: anonymous `404` for a trashed row on the permalink, `?p=`, home, category archive, page
      `/page/2/`, `?m=`; an editor `200` on REST `GET`; `GET ?status=trash` as editor lists it, as
      anonymous answers `403 rest_forbidden_status`; the default collection omits it; `X-WP-Total`
      agrees; the admin list omits it unless `status=trash`, the dashboard `pages` count omits it; REST
      and admin comment lists omit `post-trashed` comments (Req 8-10). (d) A published child of a trashed
      page builds its link from the suffixed ancestor and stops resolving at the old path (Req 9.5). (e)
      The public menu hides a trashed page's item and shows its children at the parent; the admin menu
      read still returns it; a restore brings it back (Req 9.2-9.4). (f) A trashed `future` post is not
      published by a scheduler `tick`; restored to `future` with a past date it is (Req 12). (g)
      `buildHandler` over a service built without the trasher answers non-force `DELETE` with `501` and
      keeps the row (Req 14.2). (h) An unmanaged-type row answers `404` on REST and `ErrForbidden` on
      the service (Req 7.10, 25.3).
      Files: `cmd/grimoire/assembly_trash_test.go`, `cmd/grimoire/assembly_hardening_fixture_test.go`
      (helper `seedRawComment` if absent)
      Verify: `go test ./cmd/grimoire -run Trash` — fails: the row is deleted instead of trashed (the
      option is not wired), counts and comments are untouched. _(Req 6-14, 25.3; D1, D2, D8)_
- [x] 6.2 Wire and prove the wiring. In `cmd/grimoire/assembly.go` add
      `content.WithPostTrasher(repos.PostTrash)` and
      `content.WithTrashDays(<trash days>)` to the `NewPostWriteService` call, and `srv.WithPostTrash(
      postWrite)` to the server construction. Until task 10.2 adds the config key, pass the constant 30
      through one named variable `trashDays := 30` with a comment naming task 10.2 as its replacement.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 6.1 passes. **Proof of wiring, three removals, each recorded in
      the PR description:** remove `WithPostTrasher` locally: the REST trash subtests of
      `TestBuildHandlerTrash` fail (non-force `DELETE` answers `501`); restore. Remove `WithTrashDays`:
      the same subtests fail on `501`; restore. Remove `srv.WithPostTrash`: the same subtests fail on
      `501`; restore. Golden still fails only on the three entries. _(Req 14.1; D1)_
- [x] 6.3 Golden regeneration, its check and the appended steps. (a) Write
      `scripts/check-golden-trash.sh <base-ref> <pr1|pr2>` as specified in `design.md` "Golden changes"
      (jq required; checks 1-6; exit non-zero with a message on any failure), executable. Run it first
      against the current files to see it fail on the three expected entries, no RED step otherwise.
      (b) Append the nine PR 1 steps of `design.md` to `builtinGoldenScript()` after
      `admin term delete` (a `goldenReq` with `actor: ""` for the anonymous step, mapped to a nil actor
      in `runBuiltinGolden`). (c) Regenerate with `go test ./cmd/grimoire -run TestBuiltinGolden
      -update` in **its own commit** whose message states the cause (REST `DELETE` now trashes) and the
      three changed entries. (d) Run `scripts/check-golden-trash.sh main pr1` and paste its output into
      the PR description; it must print `ok` for both files. If it fails, fix the cause in code or in
      the script's expectation with a written reason; do not hand-edit the golden.
      Files: `scripts/check-golden-trash.sh`, `cmd/grimoire/assembly_builtin_golden_test.go`,
      `cmd/grimoire/testdata/builtin_golden/with_registry.json`,
      `cmd/grimoire/testdata/builtin_golden/without_registry.json`
      Verify: `go test ./cmd/grimoire -run TestBuiltinGolden` green after the update;
      `git diff main -- cmd/grimoire/testdata/builtin_golden` shows only entries 7, 8, 13 changed and
      the appended entries; the script passes. _(Req 14.3-14.5; D1)_
- [x] 6.4 Real-database check and PR 1 documentation. (a) `test/e2e/m22_trash_realdb_test.go`,
      patterned on `m9b_realdb_census_test.go`: skip at the first line without `GRIMOIRE_TEST_WP_DSN`;
      read-only; count trashed rows per `post_type`; for each, check the representation of Requirements
      1.1-1.3 (trash meta present, suffix present exactly when the desired-slug meta is present), that
      `_wp_trash_meta_status` is a known status, and that `PostRepo.ByID`, `TrashInfo` and the REST
      mapper read the row without error; log (do not fail) violations. Run it against the dev database
      once and paste the summary line into the PR description. (b) `docs/compatibility.md`: add an
      `M22 — Trash and restore (PR 1)` section (what REST `DELETE` now does, the response bodies and
      error codes, the representation rows of `design.md`, the restore rules including the draft
      default and the slug rules, comments, the count rule and its remaining gap, capability evaluation
      on the previous status, menus, reads, what the admin `DELETE` still does until PR 2, and the
      **migration note**: clients that relied on a permanent delete must send `?force=true`, and
      `trash_days: 0` arrives in PR 3); change the title line to include M22; fix the M17 "Delete"
      bullet, the M18 PR 2 "Delete cleanup" intro, the M19 PR 3 and M20 PR 1 and PR 2 "Known gaps"
      sentences that say there is no trash, and the "Public read guarantees" sentence about trashed rows
      only if it is now inaccurate. (c) Run `grep -rn "no trash\|permanent delete\|permanently" docs
      README.md` and fix each statement PR 1 makes stale, leaving `plans/17-*` to `plans/21-*`
      untouched. (d) `plans/README.md`: add the row `| 22 |
      [`22-trash-and-restore`](./22-trash-and-restore) | 🟡 In progress (1 of 3 shipped) | ... |` with a
      one-paragraph summary.
      Files: `test/e2e/m22_trash_realdb_test.go`, `docs/compatibility.md`, `plans/README.md`
      Verify: the e2e test skips without the variable and passes with it; `grep -n "DELETE.*permanent
      delete with no trash" docs/compatibility.md` finds nothing. _(Req 3.8, 24.1-24.4, 27, 28.1; D2;
      sign-off 1)_
- [x] 6.5 Final gate for PR 1, with explicit cross-vendor verification. Confirm: no new file under
      `internal/storage/migrations`; `git diff main -- internal/domain/repository.go` adds exactly one
      interface (`PostTrasher`) and two types (`TrashInfo`, `RestoreRequest`), changes no method of an
      existing interface, and only doc comments of `AdminPostFilter` and `PostCounter`; no new
      capability string; `go.mod` and `go.sum` unchanged; no new `GRIMOIRE_TEST_*` variable;
      `grep -rn '"wp_"' internal/storage/storagetest/posttrash_contract.go
      internal/storage/storagetest/termcount_contract.go` finds nothing; `scripts/check-golden-trash.sh
      main pr1` passes. Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`,
      `go test -count=2 ./...` and `go test -race ./internal/content ./internal/web
      ./internal/storage/... ./cmd/grimoire`. **Cross-vendor:** start fresh throwaway MySQL 8.0 and
      PostgreSQL 16 containers as in task 2.5, run the **whole** storage suite with both variables
      set, confirm no `PostTrasher`, `TermCount`, `AdminPost`, `Comments`, `Menus`, `PostMenuItem`,
      `PostTermRelationship`, `MediaDelete` or `PostDependents` subtest is skipped and every vendor
      passes, record the final lines in the PR description, remove the containers, and confirm the
      `storage-contract` CI job is green on the PR before merging. Fix anything red in the phase that
      owns it. Ensure all tests pass, ask the user if questions arise.
      Files: (verification only)
      Verify: all gate commands green on SQLite, MySQL and PostgreSQL; the git checks show no
      violation; `docker ps -a` shows no leftover container. _(Req 3, 26.3, 26.6; sign-off 12)_
## Phase 7 — Admin API (`internal/web`, TDD) — PR 2
- [x] 7.1 Write the failing handler tests first, in `internal/web/adminapi_trash_test.go` over fakes.
      (a) `DELETE /admin/api/posts/{id}` without force calls `Trash` and answers `204`; with
      `?force=true` calls `Delete` and answers `204`; `409 already_trashed`; `409 trash_unsupported`
      with the message `trash is disabled; use force=true`; `403`; `404`/`400` as before (Req 15.1-15.5).
      (b) `PUT` with `status: trash` and with a leaving status reach the service; `POST` create with
      `trash` is `400 invalid status` (Req 15.6). (c) `POST /admin/api/posts/{id}/restore`: empty body
      and empty `status` restore to `draft`; `previous` uses the recorded status; another valid status
      goes through the publish check; `200` with the post detail; a live row gives `400 bad_request`
      `post is not in the trash`; a forbidden actor `403`; an invalid status `400`; CSRF is required;
      the revision-restore route is unaffected (Req 16; sign-off 5). (d) `POST
      /admin/api/posts/empty-trash`: `{"type":"post"}` deletes the trashed rows the actor may delete
      (fake actor with and without delete capability for some rows), answers `200 {deleted, skipped,
      more}`; more than 1000 trashed rows gives `more: true` and exactly 1000 examined; a non-authorization
      failure stops with `500`; a missing, unknown or unmanaged type gives `400`; ids are collected 100
      per page (Req 17). (e) `GET /admin/api/posts?status=trash` is accepted; other unknown statuses
      still `400 invalid_status`; the session response includes `trashEnabled` (Req 10.2, 15.7).
      Files: `internal/web/adminapi_trash_test.go`
      Verify: `go test ./internal/web -run AdminTrash` — fails (the routes do not exist; `DELETE` calls
      `Delete`; `trash` is an invalid status). _(Req 10.2, 15, 16, 17; D1; sign-off 5, 6)_
- [x] 7.2 Implement the admin routes. In `adminapi_posts.go`: `adminPostDelete` reads `force` with
      `restForceDelete`, calls `Trash` or `Delete`, maps `ErrTrashUnsupported` and
      `domain.ErrAlreadyTrashed` to `409` with codes `trash_unsupported` and `already_trashed`; add
      `adminPostRestore` and `adminPostEmptyTrash`; use `validUpdateStatuses` in the update parser only.
      In `adminapi.go` accept `trash` in `adminPosts`' status list and add `trashEnabled` to the session
      response (through a `TrashEnabled() bool` assertion on `s.postTrash`). In `adminroutes.go` register
      `POST /posts/{id}/restore` and `POST /posts/empty-trash` inside the existing
      `s.postWrite != nil` CSRF group, with `s.postTrash` required for both. Add the two request and
      response structs next to `postWriteRequest`.
      Files: `internal/web/adminapi_posts.go`, `internal/web/adminapi.go`, `internal/web/adminroutes.go`
      Verify: `go test ./internal/web` — 7.1 passes, every existing test green; golden unchanged.
      _(Req 10.2, 15, 16, 17; D1)_
## Phase 8 — React admin: the Trash view (`web/admin`, TDD) — PR 2
- [x] 8.1 Write the failing Vitest tests first. In `web/admin/src/views/PostsList.test.tsx` and a new
      `PostsList.trash.test.tsx` (mocking `api`): the status picker offers `Trash` after `Scheduled` and
      selecting it requests `status=trash`; in the Trash view each row shows `Restore` and `Delete
      permanently` and no Edit; `Restore` calls `api.restorePost(id)` with no status and reloads;
      `Delete permanently` and `Empty Trash` open the destructive dialog and call
      `api.deletePost(id, {force: true})` and `api.emptyTrash(type)` (repeating while the response has
      `more` and `deleted > 0`); outside the Trash view with `trashEnabled` true the row action reads
      `Move to Trash`, asks no confirmation, calls `api.deletePost(id)`, and shows a notice whose `Undo`
      calls `api.restorePost(id, "previous")`; with `trashEnabled` false the action reads `Delete
      permanently` with the dialog; the empty Trash view shows "Trash is empty". In
      `PostEditor.test.tsx`: a loaded row with status `trash` renders the read-only notice and a Restore
      action and no Save. Keep the existing delete test, updated for the new label (Req 18).
      Files: `web/admin/src/views/PostsList.trash.test.tsx`, `web/admin/src/views/PostsList.test.tsx`,
      `web/admin/src/views/PostEditor.test.tsx`
      Verify: `cd web/admin && npm test -- --run PostsList PostEditor` — fails (no Trash option, no
      Restore action, `deletePost` takes no options). _(Req 18.1-18.7)_
- [x] 8.2 Implement the SPA. In `api/client.ts`: `deletePost(id, opts?: {force?: boolean})` appends
      `?force=true`; add `restorePost(id, status?)` and `emptyTrash(type)`; in `api/types.ts` add
      `trashEnabled` to the session type and the empty-trash response type. In `PostsList.tsx`: the
      `Trash` picker item, `STATUS_VARIANT.trash`, the row actions by status, the `Move to Trash` inline
      notice with `Undo`, the destructive dialogs, `Empty Trash`, the empty state. In `PostEditor.tsx`
      and `PageEditor.tsx`: the read-only trashed notice with Restore. Rebuild with `make admin`
      (`cd web/admin && npm ci && npm run build`) and commit `internal/admin/dist` **in the same commit
      as the source**, as M8 did. If Node is unavailable, record that in the PR and do not mark it ready.
      Files: `web/admin/src/api/client.ts`, `web/admin/src/api/types.ts`,
      `web/admin/src/views/PostsList.tsx`, `web/admin/src/views/PostEditor.tsx`,
      `web/admin/src/views/PageEditor.tsx`, `internal/admin/dist/**`
      Verify: `cd web/admin && npm test -- --run && npm run build` — 8.1 passes, `tsc --noEmit` clean;
      `go build ./...` embeds the new bundle; `git status internal/admin/dist` shows the rebuilt files.
      _(Req 18.1-18.8)_
## Phase 9 — Assembly, goldens, documentation and PR 2 gate — PR 2
- [x] 9.1 Write the failing assembly tests first. Add `cmd/grimoire/assembly_trash_admin_test.go` over
      `newCustomWriteHandler`: through the real `buildHandler`, `DELETE /admin/api/posts/{id}` trashes a
      post, a page and a registered-type row (rows as in task 6.1, `204`), the admin list omits them
      and `?status=trash` lists them, `GET /admin/api/posts/{id}` returns `status: "trash"`; `POST
      .../restore` returns the row to `draft` (and to the previous status with `{"status":"previous"}`)
      with the slug, comments and meta restored; `?force=true` removes a live and a trashed row through
      the M18-M20 sequence; `empty-trash` removes every trashed row of one type and no row of another
      type, skips a row the actor may not delete (a contributor against another author's formerly
      published row) and reports counts; `409` for trashing twice; the session response has
      `trashEnabled: true`; a service built without the trasher answers non-force `DELETE` with `409
      trash_unsupported` and keeps the row (Req 15-17, 19.1).
      Files: `cmd/grimoire/assembly_trash_admin_test.go`
      Verify: `go test ./cmd/grimoire -run TrashAdmin` — fails: the admin `DELETE` is permanent, the
      restore and empty-trash routes answer `404`. _(Req 15-17, 19.1)_
- [x] 9.2 Prove the wiring. PR 2 needs no new `assembly.go` option because `WithPostTrash` and the
      write service are already wired by task 6.2; this is a verification task. Remove
      `srv.WithPostTrash` locally: the `TrashAdmin` subtests fail on `409 trash_unsupported` and `404`
      on the new routes; restore. Remove `WithPostTrasher`: the same subtests fail; restore. Record both
      in the PR description. `go test ./cmd/grimoire` green.
      Files: (verification only)
      Verify: both removals fail the named tests; the final tree is green. _(Req 19.1, 26.5)_
- [x] 9.3 Append the PR 2 golden steps. Append the nine PR 2 steps of `design.md` to
      `builtinGoldenScript()` after the PR 1 steps (actor `editor`), regenerate in **its own commit**
      (`-update`), and run `scripts/check-golden-trash.sh main pr2`, which must print `ok` for both
      files and prove that no existing entry changed against the PR 1 base (use the PR 1 merge commit
      as `<base-ref>` if `main` already contains PR 1). Paste the output into the PR description. No
      RED step (the golden compares to itself until regenerated).
      Files: `cmd/grimoire/assembly_builtin_golden_test.go`,
      `cmd/grimoire/testdata/builtin_golden/with_registry.json`,
      `cmd/grimoire/testdata/builtin_golden/without_registry.json`
      Verify: `go test ./cmd/grimoire -run TestBuiltinGolden` green; the script passes in `pr2` mode.
      _(Req 19.2)_
- [x] 9.4 PR 2 documentation. `docs/compatibility.md`: add `M22 — Trash and restore (PR 2)` (admin
      `DELETE` trashes by default and `?force=true` deletes; the restore and empty-trash routes and
      their bodies; `trashEnabled`; the status vocabulary; the Trash view and Undo; the capability on
      restore) and remove the sentence of PR 1 that says the admin `DELETE` is still permanent.
      `docs/user-guide.md`: add a short "Trash" subsection to the admin section. Set the
      `plans/README.md` row to `🟡 In progress (2 of 3 shipped)`.
      Files: `docs/compatibility.md`, `docs/user-guide.md`, `plans/README.md`
      Verify: `grep -n "admin.*still permanent" docs/compatibility.md` finds nothing. _(Req 28.1;
      sign-off 6)_
- [-] 9.5 Final gate for PR 2. Confirm: no new file under `internal/storage/migrations`; `git diff main
      -- internal/domain` empty; no new capability string; `go.mod` and `go.sum` unchanged; the
      built-in golden diff against the PR 1 base is additions only (`scripts/check-golden-trash.sh <PR 1
      base> pr2`). Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`,
      `go test -count=2 ./...`, `go test -race ./internal/web ./cmd/grimoire`, and `cd web/admin && npm
      test -- --run && npm run build`. PR 2 touches no storage, so no container run is required, and the
      task states that. Ensure all tests pass, ask the user if questions arise.
      Files: (verification only)
      Verify: all gate commands green; the git checks show no violation. _(Req 19.3, 26.3, 26.6)_
## Phase 10 — Configuration (`internal/config`, TDD) — PR 3
- [~] 10.1 Write the failing config tests first, in `internal/config/config_test.go`: a file without
      `content.trash_days` loads as 30; `trash_days: 0` loads as 0 (not defaulted to 30);
      `trash_days: 7` loads as 7; `-1` and `abc` and `1.5` fail with an error containing
      `content.trash_days`; an unknown key under `content:` still fails; `GRIMOIRE_CONTENT_TRASH_DAYS=14`
      overrides the file; an unparsable env value is ignored; `ContentConfig.EffectiveTrashDays()` and
      `TrashEnabled()` helpers. Add a test that each file under `configs/` loads and documents the key
      in a comment (grep the file).
      Files: `internal/config/config_test.go`
      Verify: `go test ./internal/config` — fails (the key is rejected as unknown). _(Req 20.1-20.6;
      D6; sign-off 3)_
- [~] 10.2 Implement. In `internal/config/content.go` add `TrashDays *int` (`yaml:"trash_days"`), the
      pre-decode check that produces `content.trash_days must be a non-negative integer`, and
      `EffectiveTrashDays()`; in `config.go` apply the default 30, validate non-negative, and add the
      env override beside `GRIMOIRE_SCHEDULER_INTERVAL_SECONDS`. Add a commented `trash_days` example to
      the four files under `configs/`. In `cmd/grimoire/assembly.go` replace the constant of task 6.2
      with `deps.Config.Content.EffectiveTrashDays()`.
      Files: `internal/config/content.go`, `internal/config/config.go`, `configs/*.yaml`,
      `cmd/grimoire/assembly.go`
      Verify: `go test ./internal/config ./cmd/grimoire` — 10.1 passes; with `trash_days: 0` the
      assembly test of 6.1(g) behavior holds through the real config path. _(Req 20; D6)_
## Phase 11 — Trashed-row lister and the purge (`internal/storage`, `internal/scheduler`, TDD) — PR 3
- [~] 11.1 Write the failing tests first. (a) `RunTrashedPostListerContract(t, newRepos
      NewReposWithPrefixFunc)` in `internal/storage/storagetest/trashedposts_contract.go`, registered in
      the three vendor files: managed and unmanaged types; a row without the meta; a row with a
      non-numeric meta (`HasTrashedAt` false, no error on any vendor); duplicate meta rows (lowest
      `meta_id`); pagination across the cursor with `limit 2`; empty `types`; non-trashed rows never
      returned (Req 21). (b) `internal/scheduler/purge_test.go` over fakes and a fake clock
      (`withClock`): the first tick purges, a second tick within the hour does not, one after the hour
      does; only rows older than the cutoff go; a row without `HasTrashedAt` stays; the `types` list is
      passed to the lister; `days == 0` never lists; one failing delete is logged and the next row is
      processed; `ErrForbidden` is logged at debug and not as an error; a row whose `ByID` shows it is
      no longer `trash` is skipped; the info log appears only when something was purged; the principal
      holds the four `delete_` verbs for `posts`, `pages` and each given suffix and not `delete_users`
      or any other new capability (Req 22; sign-off 10, 11).
      Files: `internal/storage/storagetest/trashedposts_contract.go`,
      `internal/storage/storagetest/sqlite_test.go`, `mysql_test.go`, `postgres_test.go`,
      `internal/scheduler/purge_test.go`
      Verify: `go test ./internal/storage/... ./internal/scheduler -run 'TrashedPost|Purge'` — fails
      (compile: the lister, `WithTrashPurge` and `WithDeleteCapabilitySuffixes` do not exist). _(Req
      21.1-21.5, 22.1-22.7; D7)_
- [~] 11.2 Implement the lister. Add `TrashedPost` and `TrashedPostLister` to
      `internal/domain/repository.go`, `(*PostRepo) TrashedPosts` in `internal/storage/wprepo/trashed.go`
      per `design.md` "Lister", the assertion in `writers.go`, and `TrashedPosts
      domain.TrashedPostLister` on `storage.Set`. **Cross-vendor verification** as in task 2.5 (names
      `g22-*`), pasted into the PR description.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/trashed.go`,
      `internal/storage/wprepo/writers.go`, `internal/storage/factory.go`
      Verify: `go test ./internal/storage/...` — the lister contract passes on SQLite, MySQL 8.0 and
      PostgreSQL 16; no leftover container. _(Req 21; D7)_
- [~] 11.3 Implement the purge. In `internal/scheduler/scheduler.go` add `WithTrashPurge`,
      `WithDeleteCapabilitySuffixes`, `withClock`, `purgeTrash` and its call at the end of `tick`
      (`lastPurge` zero on start, one-hour spacing), the `trashDeleter` interface (`Delete`, `ByID`),
      and the log lines of `design.md` "Scheduler". Do not change the publish loop or the
      `systemPrincipal`'s publish capabilities.
      Files: `internal/scheduler/scheduler.go`
      Verify: `go test ./internal/scheduler` — 11.1(b) passes and the existing scheduler tests are
      unchanged and green. _(Req 22; D7; sign-off 10, 11)_
## Phase 12 — Wiring, end-to-end, final documentation and PR 3 gate — PR 3
- [~] 12.1 Write the failing tests first. (a) `cmd/grimoire/assembly_trash_purge_test.go`: through
      `buildHandler`, `buildScheduler` and one `tick` with a fixed clock: an expired trashed post, page
      and registered-type row are purged with the M18-M20 cleanup (relationships, comments, menu items,
      revisions, meta); a fresh trashed row, an expired trashed row of an unmanaged type
      (`shop_order`), and an expired trashed row without `_wp_trash_meta_time` remain; with
      `trash_days: 0` nothing is purged and a non-force `DELETE` answers `501` (Req 22.2, 23.2, 20.2).
      (b) Add one case to `test/e2e/m7_scheduler_test.go` that runs the real binary assembly with a
      short `trash_days` and an artificially old `_wp_trash_meta_time` row and asserts it disappears
      after the first tick (Req 23.3).
      Files: `cmd/grimoire/assembly_trash_purge_test.go`, `test/e2e/m7_scheduler_test.go`
      Verify: `go test ./cmd/grimoire -run TrashPurge` and `go test ./test/e2e -run Scheduler` — fail:
      nothing is purged because the options are not wired. _(Req 20.2, 22, 23)_
- [~] 12.2 Wire and prove the wiring. In `buildScheduler` pass `scheduler.WithTrashPurge(
      deps.Repos.TrashedPosts, postWrite, deps.Config.Content.EffectiveTrashDays(), managedTypes)` and
      `scheduler.WithDeleteCapabilitySuffixes("posts", "pages", registeredSuffixes(deps.Registry)...)`,
      with `managedTypes` built from `post`, `page` and `deps.Registry.PostTypes()` names.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire ./test/e2e` — 12.1 passes. **Proof of wiring, three removals,
      each recorded in the PR description:** remove `WithTrashPurge`: `TrashPurge` fails (nothing
      purged); restore. Remove `WithDeleteCapabilitySuffixes`: `TrashPurge` fails (every delete is
      forbidden); restore. Pass an empty `managedTypes`: `TrashPurge` fails; restore. _(Req 23.1;
      D7)_
- [~] 12.3 Final documentation. `docs/compatibility.md`: add `M22 — Trash and restore (PR 3)`
      (`content.trash_days`, `0` meaning trash off and the `501`, the hourly purge, what it skips, the
      migration note updated with the config key), replace the M22 PR 1 sentence "trash_days arrives in
      PR 3", and list the remaining gaps of Requirement 28.3. `docs/user-guide.md`: update the REST
      paragraph ("`DELETE` for media and users (which require `force=true`...)") to say posts, pages and
      registered types trash by default. `plans/README.md`: set the row 22 to `✅ Implemented (3 of 3 shipped)` with the final summary. `plans/post-m10-gap-analysis/README.md`: remove the trash entries (lines naming "delete is
      permanent, with no trash" and "trash remains"), add "Shipped in M22 (PR 1, PR 2, PR 3)"
      paragraphs, and fix "Recommended next". `plans/wordpress-core-parity-roadmap/tasks.md`: append a
      note under the M11 delete task that M22 changed posts and pages to trash by default. Run
      `grep -rn "no trash\|delete is permanent\|trash remains" docs README.md plans` and fix each
      statement this milestone makes stale, leaving `plans/17-*` to `plans/21-*` untouched. No RED step
      (documentation).
      Files: `docs/compatibility.md`, `docs/user-guide.md`, `plans/README.md`,
      `plans/post-m10-gap-analysis/README.md`, `plans/wordpress-core-parity-roadmap/tasks.md`
      Verify: the greps find nothing stale; the plans README row reads Implemented. _(Req 24.4, 28.1-28.3)_
- [~] 12.4 Final gate for PR 3, with explicit cross-vendor verification. Confirm: no new file under
      `internal/storage/migrations`; `git diff main -- internal/domain/repository.go` adds exactly one
      interface (`TrashedPostLister`) and one type (`TrashedPost`); no new capability string (the
      scheduler principal's delete verbs are granted per suffix, not new names); `go.mod` and `go.sum`
      unchanged; no new `GRIMOIRE_TEST_*` variable; `scripts/check-golden-trash.sh <PR 2 base> pr2`
      passes (PR 3 adds no golden entry). Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`,
      `go test ./...`, `go test -count=2 ./...`, `go test -race ./internal/content ./internal/web
      ./internal/scheduler ./internal/storage/... ./cmd/grimoire`. **Cross-vendor:** fresh throwaway
      MySQL 8.0 and PostgreSQL 16 containers as in task 2.5, the whole storage suite with both
      variables set, no `TrashedPost` or `PostTrasher` subtest skipped, every vendor passing, final lines
      in the PR description, containers removed, and the `storage-contract` CI job green before
      merging. Ensure all tests pass, ask the user if questions arise.
      Files: (verification only)
      Verify: all gate commands green on SQLite, MySQL and PostgreSQL; the git checks show no violation;
      `docker ps -a` shows no leftover container. _(Req 26.3, 26.6; sign-off 12)_
## Notes
- Tasks marked with `*` are optional; **no sub-task here is optional**. The trasher contract, the
  property tests, the cross-vendor runs, the assembly tests and the golden check are the milestone's
  done-definition (Requirement 26).
- **Property tests** use `testing/quick` with a seeded generator, as `internal/sanitize` does. They are
  inside the task that owns the behavior (1.1, 2.1, 2.3, 3.1, 5.1), not stand-alone tasks.
- **TDD exceptions are explicit:** 2.5, 6.2 (wiring proof), 6.3 (golden), 6.4 and 9.4 and 12.3 (docs and
  the read-only real-database test), 6.5, 9.2, 9.5, 11.2's container step and 12.4 (verification and
  gates).
- **Wiring is tested where it is wired.** Task 6.2 pairs three removals with the named tests, task 9.2
  proves the admin routes through the same wiring, task 12.2 pairs three removals with the purge test.
- **Golden files.** PR 1 changes exactly three existing entries in each file and appends nine (task
  6.3); PR 2 only appends nine (task 9.3); PR 3 changes none. `scripts/check-golden-trash.sh` is the
  proof. If `TestBuiltinGolden` fails anywhere else, stop and find the cause; do not pass `-update`.
- **Stale-assertion edits are narrow.** The known stale assertions are the `rest_cannot_edit` code for a
  forbidden post delete (task 4.1) and the M18-M20 tests that call `DELETE` through REST expecting a
  permanent delete (assembly tests that send `DELETE` without `force` and then assert the row is gone;
  change them to add `?force=true`, in task 6.1, and say so in the commit). Change nothing else.
- **Storage tests never hardcode a prefix, a vendor or a placeholder.** They take
  `NewReposWithPrefixFunc` and run on MySQL and PostgreSQL containers before each storage-touching PR
  opens (tasks 2.5, 6.5, 11.2 and 12.4).
- **Open sign-off items** are listed in `design.md` "Decisions awaiting sign-off", with the task that
  implements each default. Reversing one is local to that task.
- **Not done here, recorded as gaps:** trash for media, users, terms and comments as first-class admin
  views; bulk trash; WordPress hooks and the object cache; published-only term counts; renaming
  unsuffixed legacy trashed rows; `future` treated like `publish` by the capability policy; a
  configurable purge cadence.
## Task Dependency Graph
```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2"] },
    { "id": 2, "tasks": ["2.1"] },
    { "id": 3, "tasks": ["2.2"] },
    { "id": 4, "tasks": ["2.3"] },
    { "id": 5, "tasks": ["2.4"] },
    { "id": 6, "tasks": ["2.5", "3.1"] },
    { "id": 7, "tasks": ["3.2", "5.1"] },
    { "id": 8, "tasks": ["5.2", "4.1"] },
    { "id": 9, "tasks": ["4.2"] },
    { "id": 10, "tasks": ["6.1"] },
    { "id": 11, "tasks": ["6.2"] },
    { "id": 12, "tasks": ["6.3"] },
    { "id": 13, "tasks": ["6.4"] },
    { "id": 14, "tasks": ["6.5"] },
    { "id": 15, "tasks": ["7.1", "8.1"] },
    { "id": 16, "tasks": ["7.2"] },
    { "id": 17, "tasks": ["8.2"] },
    { "id": 18, "tasks": ["9.1"] },
    { "id": 19, "tasks": ["9.2"] },
    { "id": 20, "tasks": ["9.3"] },
    { "id": 21, "tasks": ["9.4"] },
    { "id": 22, "tasks": ["9.5"] },
    { "id": 23, "tasks": ["10.1", "11.1"] },
    { "id": 24, "tasks": ["10.2", "11.2"] },
    { "id": 25, "tasks": ["11.3"] },
    { "id": 26, "tasks": ["12.1"] },
    { "id": 27, "tasks": ["12.2"] },
    { "id": 28, "tasks": ["12.3"] },
    { "id": 29, "tasks": ["12.4"] }
  ]
}
```
