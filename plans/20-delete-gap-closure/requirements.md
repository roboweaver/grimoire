# Requirements Document
M20 — Delete gap closure: menu-item re-parenting and recount, media delete cleanup, relationships in unmanaged taxonomies
## Introduction
M19 (`../19-term-gate-and-delete-parity`) made post delete remove the nav-menu
items that point at the deleted row and recorded the gaps it left in
`docs/compatibility.md` (M19 PR 3 "Known gaps") and in
`../19-term-gate-and-delete-parity/design.md` (Findings 5, 6 and 8). M20 closes four
of them. Each is visible on a site that shares its database with a live WordPress
install.

| Item | Recorded gap | Behavior at `main` `508923e` |
|---|---|---|
| 1 | M19 PR 3 "Child menu items keep a dangling `_menu_item_menu_item_parent`" | `PostRepo.PurgeMenuItemsOf` and `NavMenuRepo.DeleteItem` delete an item and leave the `_menu_item_menu_item_parent` postmeta of its children pointing at the deleted id. `buildMenuTree` (`internal/storage/wprepo/menus.go:258`) attaches children only under reachable parents, so those children vanish from every rendered menu. |
| 2 | M19 PR 3 "`NavMenuRepo.DeleteItem` does not recount" | `PurgeMenuItemsOf` sets `term_taxonomy.count` of the affected menu; `DeleteItem` does not (`internal/storage/wprepo/menus_write.go:355`), so the two delete paths leave different counts. |
| 3 | M19 PR 3 "Deleting a media item does not clean menu items" and design Finding 8 | `MediaWriteService.Delete` calls `MediaRepo.Delete`, which removes the attachment's `term_relationships`, `postmeta` and row only (`internal/storage/wprepo/media.go:259`). Menu items for the attachment, its comments and `commentmeta`, the `_thumbnail_id` references on other posts and the term counts are left behind. |
| 4 | M19 PR 3 "Relationships in unmanaged taxonomies stay on a deleted post" and design Finding 6 | `PostWriteService.Delete` detaches only managed taxonomies (`deleteTaxonomies`). `term_relationships` rows in other taxonomies (`product_cat`, `post_format`, `wp_theme`) remain for the deleted id, and their term counts stay stale. |

**What reading the code and the WordPress source at `508923e` changed in the brief.**
`design.md` Overview has the evidence.

- **Item 1: WordPress core does not re-parent on the server.** `wp_delete_post`
  and `_wp_delete_post_menu_item` leave child menu items alone. The one
  re-parenting in WordPress is client-side: the nav-menus editor
  (`nav-menu.js`, `removeMenuItem`) moves the children of a removed item up one
  level and saves the new parents with the menu. M20 implements that editor
  behavior in storage because grimoire's tree builder drops orphans, where
  WordPress's `Walker` shows them at the top level. The item is real, safe and
  kept; the premise "WordPress re-parents" is corrected, not followed.
- **Item 3: there is one media delete path, and it needs no new port.** The admin
  API has no media delete route. `MediaService.Delete` is an unused stub.
  Everything runs in `MediaRepo.Delete`, which is already one transaction, so the
  cleanup extends that transaction and adds no option to `buildHandler`.
- **Item 4: deleting relationships "over all taxonomies" is unsafe as stated.**
  `term_relationships.object_id` is not always a post id. Core's `link_category`
  relationships point at `wp_links` ids, so a delete by `object_id` alone can
  remove a link's categories when a link and a post share a number.
  `MediaRepo.Delete` already does exactly that today. M20 excludes
  `link_category` and records the residual risk.
- **Media in WordPress** (`wp_delete_attachment`) does not re-point child
  attachments. M20 does not either, unlike the post delete of M18.

M20 is delivered as **two pull requests**, each a complete working increment
(see `tasks.md`):

- **PR 1, menus (items 1 and 2):** Requirements 1-6 and the cross-cutting
  Requirements 15-17 as they apply. Storage only, no new port.
- **PR 2, media and relationships (items 3 and 4):** Requirements 7-14 and the
  same cross-cutting requirements. One additive port.

The M15 lesson (PR #74) and the M17-M19 practice apply: a service option that is
not set in `buildHandler` does not exist in the shipped binary, so a change that
needs one ships its assembly edit and a test that fails if the edit is removed. A
change that needs none ships a `buildHandler` test that fails if the storage
change is reverted. The M18 and M19 storage lesson applies: a contract that
hardcodes a table prefix or a vendor passes on SQLite and fails in CI on MySQL
and PostgreSQL. Every storage contract in M20 takes `NewReposWithPrefixFunc`, reads
the vendor and prefix it returns, and is run against throwaway MySQL and
PostgreSQL containers before the PR is opened.
## Glossary
- **Menu item** — a `{prefix}posts` row with `post_type` `nav_menu_item`, linked
  to its menu by a `term_relationships` row in a `nav_menu` taxonomy, carrying the
  `_menu_item_type`, `_menu_item_object`, `_menu_item_object_id` and
  `_menu_item_menu_item_parent` postmeta.
- **Menu parent** — the integer in an item's `_menu_item_menu_item_parent`
  postmeta. `0` or absent means top level.
- **Deleted item** — a menu item removed by `PostMenuItemPurger.PurgeMenuItemsOf`
  or by `NavMenuWriter.DeleteItem`.
- **Child of a deleted item** — a `nav_menu_item` row whose menu parent equals the
  deleted item's id.
- **Re-parenting** — setting each child's menu parent to the deleted item's own
  menu parent, which is `0` when the deleted item was top level.
- **Item-delete helper** — the unexported `wprepo` function that deletes one menu
  item inside a caller's transaction, shared by `PurgeMenuItemsOf`, `DeleteItem`
  and `MediaRepo.Delete`. Introduced by PR 1.
- **Recount** — setting `term_taxonomy.count` of a `term_taxonomy_id` to the
  number of its `term_relationships` rows. This is the rule `SetPostTerms` and
  `PurgeMenuItemsOf` already use. WordPress counts only published (and, for
  menus, draft) objects, so a count for imported rows in other statuses can differ
  until WordPress recomputes it.
- **Managed taxonomy / unmanaged taxonomy** — as in M19: `category`, `post_tag`
  and registry taxonomies are managed; every other `term_taxonomy.taxonomy` value
  is unmanaged.
- **Non-post-object taxonomy** — a taxonomy whose relationships point at objects
  that are not posts. In WordPress core that is `link_category` only. Listed in
  `domain.NonPostObjectTaxonomies`.
- **Relationship purger** — the additive storage port
  `domain.PostTermRelationshipPurger` introduced by PR 2.
- **Featured-image reference** — a `postmeta` row of any post with `meta_key`
  `_thumbnail_id` and `meta_value` equal to an attachment id.
- **Media delete path** — `MediaWriteService.Delete` and `MediaRepo.Delete`, reached
  by `DELETE /wp-json/wp/v2/media/{id}`.
## Inherited decisions
Settled on the owner's brief and the M18-M19 precedent, treated as fixed input.
Decisions marked **(sign-off)** are listed again in `design.md` "Decisions awaiting
sign-off".
| # | Decision | Consequence |
|---|---|---|
| D1 | **Re-parent to the deleted item's own parent, resolved in storage, in the same transaction as the delete.** Both delete paths use one helper. **(sign-off)** | Requirements 1, 2, 3. |
| D2 | **Order independence.** When several deleted items form a chain, the final parent of a surviving child does not depend on the order in which the items are processed. A self-parent or cycle in stored data resolves to `0` and never loops. | Requirement 2. |
| D3 | **`DeleteItem` recounts exactly as `PurgeMenuItemsOf` does** (relationship count), in the same transaction. | Requirement 4. |
| D4 | **Media cleanup extends `MediaRepo.Delete`'s transaction; no port, no option.** The menu-item step reuses the item-delete helper with object type `attachment`. | Requirements 7, 8. |
| D5 | **Media cleanup scope: menu items, comments and `commentmeta`, term relationships with recount, featured-image references.** Child attachments are not re-pointed (WordPress does not). Theme-mod references, intermediate image files and hooks are recorded as gaps. **(sign-off)** | Requirements 7, 8, 9. |
| D6 | **Relationship removal covers every taxonomy except non-post-object taxonomies, with a recount per affected `term_taxonomy_id`.** The exclusion list is `domain.NonPostObjectTaxonomies`, held in one place and used by post delete and media delete. **(sign-off)** | Requirements 9, 10, 11. |
| D7 | **The relationship step is a sibling port, not a new method on an existing interface**, as M18 and M19 chose for the same reason. One implementation on `wprepo.PostRepo` serves all three vendors. **(sign-off)** | Requirement 10. |
| D8 | **Delete order after PR 2.** After the capability check: revisions, managed term detach, remaining-relationship purge, unstick, dependents purge, menu-item purge, row delete. Each step is idempotent; the first failure stops the sequence and leaves the row so the delete can be retried. | Requirements 10, 11. |
| D9 | **Delete stays permanent.** Trash and restore are a separate future spec. No change to REST `200 {deleted, previous}` or admin `204`. | All. |
| D10 | **Tests: TDD, pure Go over SQLite, no new dependency, no property-based tests.** A RED test task precedes every implementation task. Storage contracts take `NewReposWithPrefixFunc`. | Requirement 16. |
## Requirements
### Requirement 1: Children of a deleted menu item are re-parented
**User Story:** As a site owner, I want the sub-items of a deleted menu item to stay
in the menu, so that deleting one item does not make its children disappear.
#### Acceptance Criteria
1. WHEN `PurgeMenuItemsOf` deletes a menu item, THE system SHALL set the `_menu_item_menu_item_parent` of every `nav_menu_item` row whose menu parent equals the deleted item's id to the deleted item's own menu parent.
2. WHEN the deleted item was top level (menu parent `0`, absent or not an integer), THE system SHALL set its children's menu parent to `0`.
3. WHEN `NavMenuWriter.DeleteItem` deletes a menu item, THE system SHALL apply the same re-parenting as acceptance criteria 1 and 2.
4. THE system SHALL change only the `_menu_item_menu_item_parent` postmeta value of children, and SHALL NOT change their `menu_order`, title, other postmeta, relationships or row.
5. THE system SHALL NOT change the menu parent of any `nav_menu_item` row whose menu parent differs from the deleted item's id, including grandchildren.
6. THE system SHALL select children only among rows with `post_type` `nav_menu_item`, so a non-menu post carrying a stray `_menu_item_menu_item_parent` key is untouched.
7. THE system SHALL store the new value as the decimal string of the integer, as `writeItemMeta` and WordPress do.
8. THE system SHALL keep the read behavior of `GET /admin/api/menus/{id}` and public menu rendering unchanged, so re-parented children appear under their new parent through the existing tree builder.
### Requirement 2: Several deleted items and corrupt parents resolve deterministically
**User Story:** As a maintainer, I want chained and corrupt parent data to resolve to
one answer, so that retries and ordering never change the result.
#### Acceptance Criteria
1. WHEN `PurgeMenuItemsOf` deletes two items where one is the menu parent of the other, THE system SHALL leave every surviving descendant with the nearest surviving ancestor as menu parent, or `0` when none survives.
2. THE system SHALL produce the same final menu parents whichever order the matched items are processed in.
3. IF a deleted item's stored menu parent equals its own id, THEN THE system SHALL treat that parent as `0`.
4. IF the stored menu parents of deleted items form a cycle, THEN THE system SHALL terminate and SHALL leave no surviving child with a menu parent that names a deleted item.
5. THE system SHALL read the deleted item's menu parent before removing its postmeta.
6. WHEN a second `PurgeMenuItemsOf` call follows a completed one, THE system SHALL find nothing to delete or re-parent and SHALL change no row.
### Requirement 3: Re-parenting is atomic with the delete
**User Story:** As a maintainer, I want a failed delete to leave menus exactly as
they were.
#### Acceptance Criteria
1. THE system SHALL perform the re-parenting, the item delete and the recount of one `PurgeMenuItemsOf` or `DeleteItem` call in one transaction.
2. IF any statement of the call fails, THEN THE system SHALL roll back, leaving every item, postmeta row, relationship and count unchanged.
3. THE system SHALL add no migration, column, table or index, and no method to an existing port.
4. THE system SHALL update the doc comments of `domain.PostMenuItemPurger` and `domain.NavMenuWriter.DeleteItem` to state the re-parenting.
### Requirement 4: `DeleteItem` recounts the menu
**User Story:** As a site owner, I want the menu's item count to be right after I
delete an item in the menu editor, as it is after I delete a page.
#### Acceptance Criteria
1. WHEN `NavMenuWriter.DeleteItem` deletes an item, THE system SHALL set `term_taxonomy.count` of every `term_taxonomy_id` the item belonged to to the number of its remaining relationships.
2. THE system SHALL leave the count of every other `term_taxonomy` row unchanged.
3. WHEN the item does not belong to the menu, THE system SHALL return `domain.ErrNotFound` and SHALL change nothing, as before.
4. THE system SHALL delete all of the item's `term_relationships` rows, not only the row for the given menu, so no relationship is left for a deleted row.
### Requirement 5: Cross-vendor storage contract for the menu paths
**User Story:** As a maintainer, I want both menu delete paths proven on SQLite,
MySQL and PostgreSQL.
#### Acceptance Criteria
1. THE contracts for `PurgeMenuItemsOf` and `DeleteItem` SHALL take `storagetest.NewReposWithPrefixFunc`, SHALL read the vendor and prefix it returns, and SHALL NOT hardcode a table prefix, a vendor name or a placeholder style.
2. THE contracts SHALL seed rows through vendor-aware, prefix-aware helpers in the style of `postmenuitems_contract.go`.
3. THE contracts SHALL run on SQLite always and on MySQL and PostgreSQL when `GRIMOIRE_TEST_MYSQL_DSN` and `GRIMOIRE_TEST_POSTGRES_DSN` are set.
4. BEFORE opening each PR that touches storage, THE author SHALL run the whole storage suite against a throwaway MySQL 8.0 container and a throwaway PostgreSQL 16 container started with the CI image tags, SHALL record the commands and results in the PR, and SHALL remove the containers.
5. THE `PurgeMenuItemsOf` contract SHALL cover: a child moved to the deleted item's parent; a child of a top-level deleted item moved to `0`; a grandchild unchanged; two deleted items in a chain, in both processing orders; a self-parenting deleted item; a non-menu post with a stray parent key untouched; the recount; a second call; and every M19 case that still applies.
6. THE `DeleteItem` contract SHALL cover: a child moved to the deleted item's parent; the recount; an item of another menu refused with `ErrNotFound` and nothing changed; and an item with a relationship in a second `nav_menu` taxonomy row removed completely.
7. THE system SHALL add no `GRIMOIRE_TEST_*` variable.
### Requirement 6: PR 1 delivery
**User Story:** As a reviewer, I want PR 1 to be a complete, mergeable increment.
#### Acceptance Criteria
1. THE `buildHandler` function SHALL need no new option, because both menu delete paths run in storage; a `buildHandler` test SHALL delete a menu item through `DELETE /admin/api/menus/{id}/items/{itemId}` and a page through `DELETE /admin/api/posts/{id}`, and SHALL fail if the storage change is reverted.
2. WHEN a page is deleted that has a menu item with children, THE system SHALL show the children under the item's parent in `GET /admin/api/menus/{id}` and in the public menu render.
3. THE system SHALL leave the built-in golden files byte-identical.
4. PR 1 SHALL add no port, no migration and no capability name.
### Requirement 7: Media delete removes menu items of the attachment
**User Story:** As a site owner, I want a deleted image to disappear from my
navigation menus, so that no menu links to a missing attachment.
#### Acceptance Criteria
1. WHEN `MediaRepo.Delete` deletes an attachment, THE system SHALL delete the menu items whose `_menu_item_type` is `post_type`, whose `_menu_item_object` is `attachment` and whose `_menu_item_object_id` equals the attachment id, with their postmeta and relationships, through the item-delete helper.
2. WHEN such an item is deleted, THE system SHALL re-parent its children and recount its menus under Requirements 1, 2 and 4.
3. THE system SHALL NOT delete a menu item whose type, object or object id differs.
4. THE system SHALL perform the menu-item step in the same transaction as the attachment delete.
### Requirement 8: Media delete removes comments and featured-image references
**User Story:** As a site owner who shares a database with WordPress, I want a deleted
image to leave no comments and no featured-image pointers, as WordPress's own delete
does.
#### Acceptance Criteria
1. WHEN `MediaRepo.Delete` deletes an attachment, THE system SHALL delete the attachment's `commentmeta`, then its `comments`, in every `comment_approved` state.
2. WHEN `MediaRepo.Delete` deletes an attachment, THE system SHALL delete every `postmeta` row, of any post, whose `meta_key` is `_thumbnail_id` and whose `meta_value` equals the attachment id.
3. THE system SHALL NOT delete a `_thumbnail_id` row that names another attachment, nor any other meta key.
4. THE system SHALL NOT change `post_parent` of other attachments, as `wp_delete_attachment` does not.
5. THE system SHALL perform these steps in the same transaction as the attachment delete.
6. WHEN no row has the id, or the row with the id is not an attachment, THE system SHALL return `domain.ErrNotFound` and SHALL change nothing, so the new cleanup steps can never run for a page or a post.
### Requirement 9: Media delete removes every relationship with a recount
**User Story:** As a site owner, I want term counts to fall when a media item with
terms is deleted.
#### Acceptance Criteria
1. WHEN `MediaRepo.Delete` deletes an attachment, THE system SHALL delete its `term_relationships` rows in every taxonomy except those in `domain.NonPostObjectTaxonomies`.
2. WHEN relationships are deleted, THE system SHALL recount every `term_taxonomy_id` they belonged to.
3. THE system SHALL leave `link_category` relationships whose `object_id` equals the attachment id unchanged.
4. THE system SHALL run the relationship step before the attachment row delete, in the same transaction.
### Requirement 10: Post delete removes the remaining relationships
**User Story:** As a site owner who shares a database with WordPress and WooCommerce,
I want a deleted post to leave no relationships and correct term counts in
taxonomies grimoire does not manage.
#### Acceptance Criteria
1. THE system SHALL add `domain.PostTermRelationshipPurger` with one method, `PurgeTermRelationshipsOf(ctx, postID int64) error`.
2. WHEN `PurgeTermRelationshipsOf` runs, THE implementation SHALL, in one transaction, delete every `term_relationships` row of `postID` whose taxonomy is not in `domain.NonPostObjectTaxonomies`, and recount each `term_taxonomy_id` it removed a row from.
3. THE `wprepo.PostRepo` SHALL implement the port and `storage.Set` SHALL expose it as `PostRelationships`.
4. THE method SHALL return no error when the post has no relationships and SHALL NOT require the post row to exist.
5. THE method SHALL be idempotent.
6. THE system SHALL leave the `term_taxonomy` and `terms` rows themselves, and every other object's relationships, unchanged.
7. WHEN `PostWriteService.Delete` removes a row of any writable type, THE system SHALL call `PurgeTermRelationshipsOf(ctx, id)` after the managed term detach and before the `sticky_posts` step.
8. IF the purge fails, THEN THE system SHALL return the error, SHALL NOT delete the row, and a retry SHALL complete the cleanup.
9. WHILE no relationship purger is configured, THE system SHALL skip the step, so a service built without the option behaves as before M20.
10. THE `buildHandler` function SHALL construct the post write service with `WithPostRelationshipPurger(repos.PostRelationships)`.
### Requirement 11: Relationship removal is safe on a shared database
**User Story:** As an operator, I want the cleanup to remove only rows that belong to
the deleted post.
#### Acceptance Criteria
1. THE system SHALL select rows by the deleted row's id only, never by term, taxonomy or term id alone.
2. THE system SHALL NOT remove a `link_category` relationship whose `object_id` equals the deleted post id.
3. THE `design.md` SHALL name the residual risk that a plugin taxonomy of non-post objects with a colliding numeric id is not excluded, and SHALL name the alternative for the owner to accept or reject.
4. WHEN a managed taxonomy was already detached by the existing term detach, THE system SHALL find no remaining rows for it and SHALL recount nothing for it in this step.
5. WHEN a relationship exists in an unmanaged taxonomy for the deleted post, THE system SHALL remove it and SHALL set that term's count to its remaining relationship count.
### Requirement 12: Cross-vendor storage contract for PR 2
**User Story:** As a maintainer, I want the new storage behavior proven on SQLite,
MySQL and PostgreSQL.
#### Acceptance Criteria
1. THE contracts for `PurgeTermRelationshipsOf` and for `MediaWriter.Delete` SHALL meet Requirements 5.1-5.3 and 5.7.
2. THE `PurgeTermRelationshipsOf` contract SHALL cover: relationships in a managed and two unmanaged taxonomies removed; a `link_category` row with the same id kept; another post's relationships kept; recounts; no relationships; a nonexistent post id; and a second call.
3. THE `MediaWriter.Delete` contract SHALL cover: the attachment, its postmeta and its relationships removed; its comments and `commentmeta` removed; a matching menu item removed with its children re-parented; non-matching menu items kept; `_thumbnail_id` references to it removed and references to another attachment kept; child attachments' `post_parent` unchanged; recounts; a `link_category` row with the same id kept; an unknown id returning `ErrNotFound` with nothing changed.
4. BEFORE opening PR 2, THE author SHALL run Requirement 5.4's cross-vendor verification.
### Requirement 13: Wiring through the real assembly
**User Story:** As a maintainer, I want every PR 2 behavior reachable in the shipped
binary.
#### Acceptance Criteria
1. FOR EACH behavior in Requirements 7-11, a test SHALL drive it through `buildHandler`, not through a hand-built `web.Server`.
2. FOR the `WithPostRelationshipPurger` option, a task SHALL state that removing it makes a named test fail and SHALL record that the removal was tried.
3. FOR the media cleanup, which needs no option, THE test SHALL fail if the storage change is reverted, and the task SHALL record that the revert was tried.
4. THE `buildHandler` tests SHALL use `DELETE /wp-json/wp/v2/media/{id}?force=true` and the post, page and registered-type delete routes.
### Requirement 14: PR 2 delivery
**User Story:** As a reviewer, I want PR 2 to be a complete, mergeable increment.
#### Acceptance Criteria
1. PR 2 SHALL be mergeable on top of PR 1 only, because it reuses the item-delete helper.
2. THE system SHALL leave the built-in golden files byte-identical.
3. PR 2 SHALL add one interface to `internal/domain/repository.go`, no method to an existing interface, no migration and no capability name.
4. THE system SHALL keep REST `DELETE /wp-json/wp/v2/media/{id}` answering `200 {deleted, previous}` and the on-disk file removal unchanged.
### Requirement 15: Built-in behavior is otherwise unchanged
**User Story:** As a maintainer, I want everything M20 does not name to behave as
before.
#### Acceptance Criteria
1. THE system SHALL leave every status, header and body of the built-in golden files under `cmd/grimoire/testdata/builtin_golden/` byte-identical.
2. IF a golden file changes, THEN THE change SHALL be a separate commit that states the cause and is reviewed before the PR merges, and a golden file SHALL NOT be regenerated to make a failing test pass.
3. THE system SHALL leave the signatures of `NewPostWriteService`, `NewMediaWriteService` and `NewNavMenuWriteService` compatible, adding only options.
4. THE system SHALL apply M20 rules at write time only and SHALL NOT rewrite stored data.
5. THE system SHALL fire no WordPress hook and touch no object cache; this limit applies to every grimoire write.
6. THE system SHALL leave `NavMenuRepo.DeleteMenu`, which removes a whole menu and all of its items, unchanged.
### Requirement 16: Tests and delivery
**User Story:** As a maintainer, I want the milestone delivered as two complete,
verified increments.
#### Acceptance Criteria
1. THE system SHALL have a RED test task before each implementation task, with the failure reason stated, except the documentation and gate tasks.
2. THE system SHALL run `gofmt -l .`, `go vet ./...`, `go build ./...` and `go test ./...` green after every phase.
3. THE system SHALL update the M19 contract assertion that a child's parent is unchanged, and any other existing assertion this change makes stale, in the phase that owns the behavior, and the commit SHALL say so.
4. PR 1 SHALL be mergeable without PR 2, and PR 2 SHALL be mergeable on top of PR 1 only.
5. EACH PR SHALL end with a final gate task that runs the full checks and the `git` checks that prove its schema and port claims, and SHALL run the cross-vendor verification of Requirement 5.4.
### Requirement 17: Documentation
**User Story:** As an operator and as a reader of the plans, I want the documents to
say what shipped and to no longer list closed gaps as open.
#### Acceptance Criteria
1. WHEN PR 1 merges, THE `docs/compatibility.md` M19 PR 3 "Known gaps" for the dangling menu parent and the `DeleteItem` recount SHALL be removed and replaced by an M20 PR 1 description, and the M19 PR 3 statement that the menu editor's delete does the same SHALL be corrected.
2. WHEN PR 2 merges, THE `docs/compatibility.md` M19 PR 3 "Known gaps" for media delete and unmanaged-taxonomy relationships SHALL be removed and replaced by an M20 PR 2 description, and the delete sequence of M18 PR 2 SHALL include the remaining-relationship step.
3. EACH PR SHALL remove from `plans/post-m10-gap-analysis/README.md` exactly the entries it closes and SHALL add its shipped paragraph.
4. THE `plans/README.md` SHALL gain a `20-delete-gap-closure` row, marked partially shipped after PR 1 and implemented after PR 2.
5. THE documents SHALL record what remains open: trash and restore, WordPress hooks and the object cache, theme-mod references to a deleted attachment, intermediate image files of a deleted attachment, the published-only count rule, plugin taxonomies of non-post objects, and the read-modify-write limit of `sticky_posts`.
## Out of scope
- **Trash, restore, `?force`.** Delete stays permanent (D9). Trash and restore are their own future spec.
- **WordPress hooks and object cache.** grimoire fires no `delete_post`, `delete_attachment` or `wp_delete_nav_menu_item` actions.
- **Theme-mod references** (`custom_logo`, `header_image`, `background_image`) to a deleted attachment, which WordPress clears through `_delete_attachment_theme_mod`.
- **Intermediate image sizes and backups** of a deleted attachment. `MediaWriteService.Delete` removes the main file only, as before.
- **Re-pointing child attachments** of a deleted attachment. WordPress does not.
- **Published-only term counts.** The recount stays the relationship count.
- **Re-parenting on menu item update or move.** Only delete changes parents.
- **Admin SPA changes.** M20 is storage and API behavior only; the menu editor already reloads the menu after a delete.
## Traceability
| Item | Requirements |
|---|---|
| 1 Child menu items re-parented | 1, 2, 3, 5, 6, 15, 17 |
| 2 `DeleteItem` recount | 3, 4, 5, 6, 15, 17 |
| 3 Media delete cleanup | 7, 8, 9, 12, 13, 14, 15, 17 |
| 4 Relationships in unmanaged taxonomies | 9, 10, 11, 12, 13, 14, 15, 17 |
| Delivery, tests, docs | 15, 16, 17 |
| M19 PR 3 known gap 1 | 1, 2, 3 |
| M19 PR 3 known gap 2 | 4 |
| M19 PR 3 known gap 3, design Finding 8 | 7, 8, 9 |
| M19 PR 3 known gap 4, design Finding 6 | 10, 11 |
