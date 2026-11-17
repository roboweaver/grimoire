# Requirements Document
M19 — Term-side write gate and delete parity: unmanaged taxonomies, REST page `parent`, `sticky_posts`, nav-menu cleanup
## Introduction
M18 (`../18-write-hardening`) gated writes to post rows of types grimoire does
not manage and made post delete mirror `wp_delete_post` for the dependents the
existing ports could reach. It recorded the gaps it left in `docs/compatibility.md`
(M18 PR 1 "Known gap", M18 PR 2 "Known gaps") and in `../18-write-hardening/design.md`
(Findings 5 and 6). M19 closes four of them. Each is visible on a site that shares
its database with a live WordPress install.

| Item | M18 gap | Behavior at `main` `332548a` |
|---|---|---|
| 1 | PR 1 "Known gap" and design Finding 5 | `TermWriteService.Update` and `Delete` check only `manage_categories`. `PUT`/`DELETE /admin/api/terms/{id}` take any term id, so a holder of `manage_categories` can rename or delete a term of an imported `nav_menu`, `link_category` or `product_cat` taxonomy (`internal/content/writeservices.go:809,837`). `PostTermsWriteService.SetPostTerms` validates the taxonomy only for registered post types (`postterms.go:149`), so the admin `termIds` map on a `post` or `page` can write `term_relationships` rows in any taxonomy. |
| 2 | PR 2 known gap 1 | `RESTPage.Parent` has `omitempty` (`internal/content/rest.go:101`), so a top-level page has no `parent` key. WordPress sends `"parent":0`. |
| 3 | PR 2 known gap 2 | `PostWriteService.Delete` never touches the `sticky_posts` option, so a deleted sticky post keeps its id there. `wp_delete_post` calls `unstick_post`. |
| 4 | PR 2 known gap 3 | Deleting a post, page or custom-type row leaves the `nav_menu_item` rows whose `_menu_item_object_id` points at it. WordPress deletes them through `_wp_delete_post_menu_item`. |

**What reading the code at `332548a` changed in the brief.** Three facts shape
the requirements; `design.md` Overview has the evidence.

- `TermWriteService.Create` already refuses an unmanaged taxonomy: it returns
  `ErrTaxonomyUnknown`, which both handlers map to `400`. The hole is update and
  delete, which address a term by id. The REST term routes are bound to one
  taxonomy per route and already answer `404` for a term of another taxonomy
  (`restTermByID`), so REST is safe today and is pinned, not changed.
- The widest term-side hole is not term update or delete. It is post-term
  assignment on built-in rows: `SetPostTerms` on a `post` or `page` row writes
  whatever taxonomy name the caller supplies. M19 gates it in the same service
  layer.
- `OptionRepository` (`Get`) and `OptionWriter` (`Set`) already exist and the
  `php` package already serializes and parses PHP arrays, so `sticky_posts`
  needs **no new storage port**. Only the nav-menu cleanup needs one, because no
  existing port deletes `postmeta`, `term_relationships` and `posts` rows for
  menu items in one transaction.

M19 is delivered as **three pull requests**, each a complete working increment
(see `tasks.md`):

- **PR 1, security (item 1):** Requirements 1-5 and the cross-cutting
  Requirements 12-15 as they apply.
- **PR 2, two parity fixes (items 2 and 3):** Requirements 6-8 and the same
  cross-cutting requirements.
- **PR 3, nav-menu cleanup (item 4):** Requirements 9-11 and the same
  cross-cutting requirements.

The M15 lesson (PR #74) and the M17 and M18 practice apply: a service option that
is not set in `buildHandler` does not exist in the shipped binary. Every PR ships
its assembly edit, or states why it needs none, and a test that drives
`buildHandler` and fails if the change is removed. The M18 PR 2 lesson applies to
storage: a contract that hardcodes a table prefix passes on SQLite and fails in
CI on MySQL and PostgreSQL. Every storage contract in M19 takes
`NewReposWithPrefixFunc` and is run against throwaway MySQL and PostgreSQL
containers before the PR is opened.
## Glossary
- **Built-in taxonomy** — `category` or `post_tag`. **Registered taxonomy** — an
  entry of the `content:` section held by `registry.Registry` (M12).
- **Managed taxonomy** — a built-in taxonomy or a registered taxonomy. Every
  other `term_taxonomy.taxonomy` value found in the database is an **unmanaged
  taxonomy**, for example an imported `nav_menu`, `link_category`, `product_cat`
  or `post_format`, or a taxonomy whose registry entry was later removed.
- **Taxonomy gate** — the check `content.ManagedTaxonomy(registry, taxonomy)`
  that the term services run before the capability-passed write.
- **Stored term** — the `domain.Term` that `TermReader.TermsByIDs` returns for
  the term id being updated or deleted.
- **Id-addressed write** — a write that names an existing term by id: term
  update, term delete. **Name-addressed write** — a write that names a taxonomy
  in the request: term create, post-term assignment.
- **Term write paths** — every call that can create, change or delete a
  `terms`, `term_taxonomy` or `term_relationships` row for a post or term:
  `TermWriteService.Create/Update/Delete`, the M13 parent re-parenting inside
  `Create`/`Update`, `PostTermsWriteService.SetPostTerms`,
  `PostWriteService.Delete`'s term detach, `NavMenuWriteService` and the REST
  term routes.
- **Sticky option** — the `{prefix}options` row named `sticky_posts`, a
  PHP-serialized list of post ids.
- **Menu item of a post** — a `{prefix}posts` row with `post_type`
  `nav_menu_item` whose `_menu_item_type` postmeta is `post_type`, whose
  `_menu_item_object` postmeta equals the type of the post, and whose
  `_menu_item_object_id` postmeta equals the post id.
- **Menu-item purger** — the additive storage port
  `domain.PostMenuItemPurger` introduced by PR 3.
- **Dependents purger** — `domain.PostDependentsPurger` (M18), unchanged.
## Inherited decisions
Settled on the owner's brief and the M18 precedent, and treated as fixed input.
Decisions marked **(sign-off)** are listed again in `design.md` "Decisions
awaiting sign-off".
| # | Decision | Consequence |
|---|---|---|
| D1 | **The taxonomy gate lives in the content services, not in the handlers.** `TermWriteService`, and `PostTermsWriteService` share one helper, so admin API, REST and any future caller get one decision. | Requirements 1, 2, 3. |
| D2 | **Response by addressing mode.** An id-addressed write to an unmanaged term answers `403` on the admin API (`content.ErrForbidden`), the status M18 chose for an id-addressed write to an unwritable post row. A name-addressed write that names an unmanaged taxonomy keeps `ErrTaxonomyUnknown`, which both term create handlers already map to `400`; post-term assignment adopts the same sentinel. REST by-id routes keep `404`. No new status or error body. **(sign-off)** | Requirements 1, 2, 3. |
| D3 | **Reads are unchanged.** `GET /admin/api/terms`, the REST term `GET` routes, public term archives, navigation menu reads and every list keep serving unmanaged taxonomies exactly as before. | Requirements 1.7, 4. |
| D4 | **`NavMenuWriteService` is not an ungated path.** It is grimoire's own managed menu editor (M10.5), owns `nav_menu` terms through `NavMenuRepo` and its own capability, and never goes through `TermWriteService`. It stays as it is and a test pins that menu writes still work while a generic term update of a `nav_menu` term answers `403`. | Requirement 4. |
| D5 | **REST page `parent` is always emitted; posts and custom types are unchanged.** WordPress includes `parent` in `/wp/v2/pages` items. It does not include it in `/wp/v2/posts` items, and for a hierarchical custom type grimoire already emits it including `0`. Only `RESTPage` changes. **(sign-off)** | Requirements 6, 7. |
| D6 | **The golden regeneration is deliberate and bounded.** The built-in golden files change in exactly one way: `"parent": 0` appears on page objects that lack it. The regeneration is its own commit, produced by the repository's `-update` flag, and a diff check task proves no other line changed. | Requirement 6. |
| D7 | **Unstick reuses the option ports; no new port.** `PostWriteService` gains an option that takes an `OptionRepository` and an `OptionWriter`. Unstick runs only for `post` rows, only when the id is present, and never fails the delete because the option is absent or unparseable. **(sign-off)** | Requirement 8. |
| D8 | **Delete order after M19.** After the capability check: revisions, term detach, unstick, dependents purge, menu-item purge, row delete. Each step is idempotent; the first failure stops the sequence and leaves the row so the delete can be retried. **(sign-off)** | Requirements 8, 10. |
| D9 | **The menu-item purge is a sibling port, not a new method on `PostDependentsPurger`.** Adding a method to an existing interface breaks every fake and vendor adapter; M18 chose a narrow separate interface for the same reason. One implementation on `wprepo.PostRepo` serves all three vendors. **(sign-off)** | Requirements 9, 11. |
| D10 | **Menu-item cleanup mirrors WordPress for the rows it deletes and leaves child menu items alone.** The deleted item's `postmeta`, its `term_relationships` and its post row go; `term_taxonomy.count` of the affected `nav_menu` terms is recounted. A child menu item whose `_menu_item_menu_item_parent` named a deleted item keeps that value, as `NavMenuRepo.DeleteItem` does today; re-parenting menu items is recorded as a gap. **(sign-off)** | Requirement 10. |
| D11 | **Delete stays permanent.** Trash and restore semantics are a separate future spec. No change to REST `200 {deleted, previous}` or admin `204`. | Requirements 8, 10. |
| D12 | **Tests: TDD, pure Go over SQLite, no new dependency, no property-based tests.** A RED test task precedes every implementation task. Storage contracts take `NewReposWithPrefixFunc`. | Requirement 14. |
## Requirements
### Requirement 1: Term update and delete refuse unmanaged taxonomies
**User Story:** As a site owner who shares a database with WordPress and its
plugins, I want grimoire's term editor to refuse to rename or delete terms of
taxonomies it does not manage, so that a category manager cannot damage menus,
product categories or link categories through the admin API.
#### Acceptance Criteria
1. WHEN `TermWriteService.Update` loads a stored term whose taxonomy is an unmanaged taxonomy, THE system SHALL return `ErrForbidden` after the capability check and before sanitizing, validating a parent or writing.
2. WHEN `TermWriteService.Delete` receives the id of a term whose taxonomy is an unmanaged taxonomy, THE system SHALL read the stored term once and SHALL return `ErrForbidden` without deleting any row.
3. WHEN `TermWriteService.Delete` receives an id that matches no term, THE system SHALL return `domain.ErrNotFound`.
4. WHEN `PUT /admin/api/terms/{id}` or `DELETE /admin/api/terms/{id}` addresses a term of an unmanaged taxonomy, THE system SHALL answer `403 forbidden` and SHALL leave the `terms`, `term_taxonomy` and `term_relationships` rows unchanged.
5. THE system SHALL evaluate the capability check before the taxonomy gate, so a caller without `manage_categories` receives the same `403` for every term.
6. THE system SHALL treat `category`, `post_tag` and every registry taxonomy as managed, and SHALL evaluate their capabilities and results exactly as before M19.
7. WHILE no `content:` section is configured, THE system SHALL treat every taxonomy other than `category` and `post_tag` as unmanaged.
8. WHEN a managed term update changes `parent`, THE system SHALL keep the M13 rule that the parent must be a term of the same taxonomy, so a managed term cannot be re-parented under an unmanaged term.
### Requirement 2: Name-addressed term writes refuse unmanaged taxonomies
**User Story:** As a site owner, I want term creation and post-term assignment to
refuse an unmanaged taxonomy name, so that no write path can add rows to a
taxonomy grimoire does not manage.
#### Acceptance Criteria
1. WHEN `TermWriteService.Create` receives a term of an unmanaged taxonomy, THE system SHALL return `ErrTaxonomyUnknown` and write nothing, and both term create handlers SHALL keep answering `400`.
2. WHEN `PostTermsWriteService.SetPostTerms` receives an unmanaged taxonomy for a row of any writable type, THE system SHALL return `ErrTaxonomyUnknown` and write no `term_relationships` row, including when the term id list is empty.
3. WHEN the admin `termIds` map of a post write names an unmanaged taxonomy, THE system SHALL keep the post write, report that taxonomy in the response `partial` map and write no relationship for it.
4. WHEN `SetPostTerms` receives term ids for a built-in taxonomy and a term reader is configured, THE system SHALL return `ErrTermInvalid` for an id that is not a term of that taxonomy and SHALL write nothing.
5. THE system SHALL run the type gate, then the capability check, then the taxonomy gate, then the term membership check, so an unauthorized caller sees `403` before any `400`.
6. WHEN `PostWriteService.Delete` detaches terms, THE system SHALL continue to detach only managed taxonomies.
### Requirement 3: REST term routes keep refusing terms of another taxonomy
**User Story:** As a maintainer, I want the REST behavior pinned by tests, so that
a later refactor cannot open the same hole there.
#### Acceptance Criteria
1. WHEN `PUT`, `PATCH` or `DELETE /wp-json/wp/v2/categories/{id}` or `/tags/{id}` addresses a term whose taxonomy differs from the route taxonomy, including an unmanaged taxonomy, THE system SHALL answer `404 rest_term_invalid_id` and SHALL leave the row unchanged.
2. WHEN `PUT`, `PATCH` or `DELETE` addresses a term through a registered taxonomy route and the term belongs to another taxonomy, THE system SHALL answer `404 rest_term_invalid_id`.
3. WHEN `POST /wp-json/wp/v2/categories` or `/tags` is sent, THE system SHALL create a term of the route taxonomy only.
4. THE system SHALL keep the REST by-id taxonomy check ahead of the capability check, as before M19.
5. THE system SHALL change no REST `GET` response.
### Requirement 4: Other term write paths are inventoried and consistent
**User Story:** As a maintainer, I want every term write entry point listed and
tested, so that the gate cannot be bypassed by a path nobody remembered.
#### Acceptance Criteria
1. THE `design.md` SHALL list every caller of `TermWriter.Create/Update/Delete` and `PostTermsWriter.SetPostTerms` with its gate or its reason for needing none.
2. WHEN `NavMenuWriteService` creates, updates or deletes a menu or a menu item, THE system SHALL keep working exactly as before M19.
3. WHEN a generic term update or delete addresses a `nav_menu` term, THE system SHALL answer `403`, even though `NavMenuWriteService` can edit the same term through the menu routes.
4. THE system SHALL serve `GET /admin/api/terms`, REST term reads and public term archives for terms of unmanaged taxonomies with the same status, headers and body as before M19.
5. WHEN a registered taxonomy is removed from `content:`, THE system SHALL treat its existing terms as unwritable until it is registered again, and SHALL leave the rows untouched.
### Requirement 5: PR 1 delivery
**User Story:** As a reviewer, I want PR 1 to be a complete, mergeable security
increment.
#### Acceptance Criteria
1. THE `buildHandler` function SHALL need no new option for the term gate, because `TermWriteService` and `PostTermsWriteService` already receive the registry; a `buildHandler` test SHALL fail if the service change is reverted.
2. WHEN the registry option is removed from the term service construction in `buildHandler`, THE existing registered-taxonomy write tests SHALL fail, and the task SHALL record that the removal was tried.
3. THE system SHALL leave the built-in golden files byte-identical in PR 1.
4. PR 1 SHALL add no storage port, no migration and no capability name.
### Requirement 6: REST page `parent` is always emitted
**User Story:** As a client of a WordPress REST API, I want page items to carry
`parent` even when it is `0`, so that generic clients that read the field do not
special-case grimoire.
#### Acceptance Criteria
1. WHEN grimoire serves a page through `GET /wp-json/wp/v2/pages`, `GET /wp-json/wp/v2/pages/{id}`, the page create response or the page update response, THE `RESTPage` item SHALL contain the key `parent`, including the value `0`.
2. THE system SHALL keep the value of `parent` for a nested page equal to the stored `post_parent`, as before M19.
3. THE regenerated built-in golden files SHALL differ from `main` only by added lines of the form `"parent": 0` on page objects.
4. THE system SHALL carry the golden regeneration in its own commit with the cause named in the commit message.
5. THE system SHALL change no other REST field, header or status.
6. THE system SHALL leave the admin API page detail shape unchanged, where `parent` stays omitted when `0`.
### Requirement 7: Posts and custom types keep their parent behavior
**User Story:** As a maintainer, I want the parent field decision for the other
types recorded and pinned, so that the page fix does not leak into them.
#### Acceptance Criteria
1. THE system SHALL NOT add `parent` to `/wp-json/wp/v2/posts` items, because WordPress posts have no parent field.
2. WHEN a registered hierarchical type is served, THE system SHALL keep emitting `parent`, including `0`, and for a registered flat type THE system SHALL keep omitting it.
3. THE system SHALL pin the three behaviors with tests that fail if `parent` is added to posts or removed from hierarchical custom types.
### Requirement 8: Deleting a sticky post removes it from `sticky_posts`
**User Story:** As a site owner, I want a deleted post to leave the sticky list,
so that WordPress and grimoire do not carry a dangling id.
#### Acceptance Criteria
1. WHEN `PostWriteService.Delete` removes a row of type `post` whose id is in the sticky option, THE system SHALL write the option back without that id, preserving the order of the remaining ids.
2. WHEN the deleted post's id is not in the sticky option, THE system SHALL NOT write the option.
3. WHEN the sticky option does not exist, THE system SHALL skip the step and SHALL NOT create the option.
4. IF the sticky option is not a parsable PHP list of integers, THEN THE system SHALL leave it unchanged, SHALL log the condition and SHALL continue the delete.
5. WHEN the last sticky id is removed, THE system SHALL write the empty PHP array `a:0:{}`, as WordPress does.
6. THE system SHALL write the option in WordPress's canonical form: a PHP array with integer keys `0..n-1` and integer values.
7. THE system SHALL run the unstick step after the term detach and before the dependents purge, as one step of the M19 delete order of decision D8.
8. IF reading or writing the option fails, THEN THE system SHALL return the error, SHALL run no later step and SHALL leave the row, so a retry converges.
9. THE system SHALL apply the step to rows of type `post` only, and SHALL skip it for pages and custom types.
10. WHILE no option ports are configured, THE system SHALL skip the step, so a service built without the option behaves as before M19.
11. THE `php` package SHALL serialize a list of integers with integer keys, and a round trip of serialize then unserialize SHALL return the same ids in the same order.
### Requirement 9: The menu-item purge port
**User Story:** As a maintainer, I want one narrow, additive storage port for the
menu-item cleanup that every database vendor implements the same way.
#### Acceptance Criteria
1. THE system SHALL add `domain.PostMenuItemPurger` with one method, `PurgeMenuItemsOf(ctx, postID int64, objectType string) error`.
2. WHEN `PurgeMenuItemsOf` runs, THE implementation SHALL select the menu items of the post by the three postmeta conditions of the Glossary and the row type `nav_menu_item`, and SHALL delete nothing else.
3. THE `wprepo.PostRepo` SHALL implement the port, and `storage.Set` SHALL expose it as `PostMenuItems`, so MySQL, PostgreSQL and SQLite share one implementation.
4. THE `PurgeMenuItemsOf` method SHALL return no error when the post has no menu items and SHALL NOT require the post row to exist.
5. THE `PurgeMenuItemsOf` method SHALL be idempotent.
6. THE `PurgeMenuItemsOf` method SHALL perform all of its deletes and recounts in one transaction.
7. THE system SHALL add no migration, column, table or index, and no method to an existing port.
### Requirement 10: Deleting a post removes the menu items that point at it
**User Story:** As a site owner, I want a deleted page to disappear from my
navigation menus, so that no menu shows a link to a missing row.
#### Acceptance Criteria
1. WHEN `PostWriteService.Delete` removes a row of any writable type, THE system SHALL call `PurgeMenuItemsOf(ctx, id, type)` with the row's stored type, after the dependents purge and before the row delete.
2. WHEN a menu item of the post exists, THE system SHALL delete its `postmeta` rows, its `term_relationships` rows and its `{prefix}posts` row.
3. WHEN a menu item is deleted, THE system SHALL recompute `term_taxonomy.count` for each `nav_menu` term taxonomy it was attached to, as the number of remaining relationships.
4. THE system SHALL NOT delete a menu item whose `_menu_item_type` is not `post_type`, whose `_menu_item_object` differs from the deleted row's type, or whose `_menu_item_object_id` names another id.
5. THE system SHALL NOT delete a custom link, a taxonomy menu item or a menu item of another post, and SHALL NOT delete the menu term itself.
6. THE system SHALL NOT change `_menu_item_menu_item_parent` of any other menu item (decision D10).
7. IF the purge fails, THEN THE system SHALL return the error, SHALL NOT delete the row, and a retry SHALL complete the cleanup.
8. WHILE no menu-item purger is configured, THE system SHALL skip the step, so a service built without the option behaves as before M19.
9. WHEN the menu item deletion succeeds, THE navigation menu read for the affected menu SHALL no longer return the item, through `buildHandler`.
10. THE system SHALL serve `GET` responses that do not involve a deleted post exactly as before M19.
### Requirement 11: Storage contract across vendors
**User Story:** As a maintainer, I want the menu-item port proven on SQLite,
MySQL and PostgreSQL, so that the M18 CI failure does not recur.
#### Acceptance Criteria
1. THE storage contract for `PurgeMenuItemsOf` SHALL take `storagetest.NewReposWithPrefixFunc` and SHALL read the table prefix and vendor it returns, and SHALL NOT hardcode a table prefix or a placeholder style.
2. THE contract SHALL seed rows through vendor-aware, prefix-aware helpers in the style of `postdependents_contract.go`.
3. THE contract SHALL run on SQLite always and on MySQL and PostgreSQL when `GRIMOIRE_TEST_MYSQL_DSN` and `GRIMOIRE_TEST_POSTGRES_DSN` are set.
4. BEFORE opening PR 3, THE author SHALL run the contract against a throwaway MySQL 8.0 container and a throwaway PostgreSQL 16 container started with the CI image tags, SHALL record the commands and results in the PR, and SHALL remove the containers.
5. THE contract SHALL cover: a matching item deleted with its meta and relationships; a non-matching type, object and object id each kept; a custom link and a taxonomy item kept; two menus; the recount; an unrelated post's items kept; no items; a nonexistent post id; and a second call.
6. THE system SHALL add no `GRIMOIRE_TEST_*` variable.
### Requirement 12: Built-in behavior is otherwise unchanged
**User Story:** As a maintainer, I want everything M19 does not name to behave as
before.
#### Acceptance Criteria
1. THE system SHALL leave every status, header and body of the built-in golden files under `cmd/grimoire/testdata/builtin_golden/` byte-identical in PR 1 and PR 3.
2. THE system SHALL change the built-in golden files in PR 2 only as Requirement 6 states.
3. IF a golden file changes in any other way, THEN THE change SHALL be a separate commit that states the cause and is reviewed before the PR merges, and a golden file SHALL NOT be regenerated to make a failing test pass.
4. THE system SHALL leave the signatures of `NewTermWriteService`, `NewPostTermsWriteService` and `NewPostWriteService` compatible, adding only options.
5. THE system SHALL apply M19 rules at write time only and SHALL NOT rewrite stored data.
6. THE system SHALL fire no WordPress hook and touch no object cache; this limit applies to every grimoire write.
### Requirement 13: Wiring through the real assembly
**User Story:** As a maintainer, I want every M19 behavior reachable in the
shipped binary.
#### Acceptance Criteria
1. THE `buildHandler` function SHALL construct the post write service with the sticky option ports in PR 2, and with the menu-item purger in PR 3.
2. FOR EACH behavior in Requirements 1-10, a test SHALL drive it through `buildHandler`, not through a hand-built `web.Server`.
3. FOR EACH new `buildHandler` option, a task SHALL state that removing the option makes a named test fail, and SHALL record that the removal was tried.
4. FOR the term gate, the REST page `parent` and the other behaviors that need no option, THE `buildHandler` test SHALL fail if the service or mapper change is reverted, and the task SHALL record that the revert was tried.
### Requirement 14: Tests and delivery
**User Story:** As a maintainer, I want the milestone delivered as three
complete, verified increments.
#### Acceptance Criteria
1. THE system SHALL have a RED test task before each implementation task, with the failure reason stated, except the golden regeneration whose RED step is the diff check and the documentation and gate tasks.
2. THE system SHALL run `gofmt -l .`, `go vet ./...`, `go build ./...` and `go test ./...` green after every phase.
3. THE system SHALL cover: the taxonomy gate for every unmanaged taxonomy named in the Glossary on every term write path of Requirement 4; REST by-id 404 pins; the `nav_menu` coexistence case; the sticky branches (present, absent, last, no option, unparsable, non-post type, failure ordering); the golden diff check; the menu-item matching matrix and its cross-vendor contract; and the delete order with failure at each step.
4. PR 1 SHALL be mergeable without PR 2 and PR 3, PR 2 SHALL be mergeable on top of PR 1 only, and PR 3 SHALL be mergeable on top of PR 2 only.
5. EACH PR SHALL end with a final gate task that runs the full checks and the `git` checks that prove its schema and port claims, and PR 3 SHALL also run the cross-vendor verification of Requirement 11.4.
### Requirement 15: Documentation
**User Story:** As an operator and as a reader of the plans, I want the documents
to say what shipped and to no longer list closed gaps as open.
#### Acceptance Criteria
1. WHEN PR 1 merges, THE `docs/compatibility.md` M18 PR 1 "Known gap" SHALL be removed and replaced by an M19 PR 1 description of the term taxonomy gate, its responses and the `nav_menu` coexistence rule.
2. WHEN PR 2 merges, THE `docs/compatibility.md` M18 PR 2 known gaps for REST page `parent` and `sticky_posts` SHALL be removed and replaced by an M19 PR 2 description.
3. WHEN PR 3 merges, THE `docs/compatibility.md` M18 PR 2 known gap for nav-menu items SHALL be removed and replaced by an M19 PR 3 description, including the child-menu-item gap.
4. EACH PR SHALL remove from `plans/post-m10-gap-analysis/README.md` and `plans/wordpress-core-parity-roadmap/tasks.md` exactly the entries it closes, and SHALL add its shipped paragraph.
5. THE `plans/README.md` SHALL gain a `19-term-gate-and-delete-parity` row, marked partially shipped after PR 1 and PR 2 and implemented after PR 3.
6. THE documents SHALL record what remains open: trash and restore, term-side post meta, WordPress hooks and the object cache, child menu-item re-parenting, relationships in unmanaged taxonomies left on a deleted post, and the media delete path.
## Out of scope
- **Trash, restore, `?force`.** Delete stays permanent (D11). Trash and restore semantics are their own future spec.
- **Term-side post meta.** `termmeta` is not read or written by any M19 path.
- **WordPress hooks and object cache.** grimoire fires no `delete_post`, `unstick_post` or `wp_delete_nav_menu_item` actions, and a live WordPress object cache can serve stale values until they expire.
- **Re-parenting child menu items** when their parent item is deleted (D10), and any change to `NavMenuRepo.DeleteItem`.
- **Detaching a deleted post from unmanaged taxonomies.** A `post` row attached to a `product_cat` term keeps that relationship on delete, as before.
- **The media delete path** (`MediaWriteService`), which does not use `PostWriteService.Delete`.
- **Unstick on trash, status change or type change.** Only delete unsticks.
- **Writing `sticky_posts` from any grimoire surface other than delete.**
- **Admin SPA changes.** M19 is API and storage only.
## Traceability
| Item | Requirements |
|---|---|
| 1 Term-side write gate | 1, 2, 3, 4, 5, 12, 13 |
| 2 REST page `parent` | 6, 7, 12, 13 |
| 3 `sticky_posts` on delete | 8, 12, 13 |
| 4 Nav-menu cleanup on delete | 9, 10, 11, 12, 13 |
| Delivery, tests, docs | 12-15 |
| M18 PR 1 known gap, design Finding 5 | 1, 2, 3, 4 |
| M18 PR 2 known gap 1 | 6, 7 |
| M18 PR 2 known gap 2 | 8 |
| M18 PR 2 known gap 3 | 9, 10, 11 |
