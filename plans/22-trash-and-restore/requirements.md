# Requirements Document
M22 — Trash and restore: recoverable deletes in WordPress's own representation, with auto-purge
## Introduction
Until M21, every grimoire delete of a post, page or registered-type row is permanent
(`docs/compatibility.md` M17 "Delete", M18 PR 2 "Delete cleanup", M19 PR 3, M20). WordPress
moves those rows to the `trash` status first and removes them only on `?force=true`, on
"Empty Trash", or after `EMPTY_TRASH_DAYS` (30). A site that shares its database with a live
WordPress install therefore sees two different delete behaviors, and a client written for
WordPress loses data when it calls `DELETE` expecting a recoverable result. M22 adds trash,
restore and auto-purge, and keeps the M18-M20 permanent-delete sequence for force delete and
for emptying the trash.

**Corrections to the brief, from reading WordPress core (trunk) and the code at `main`
`25fd4f6`.** `design.md` Overview has the evidence and links.

| # | Brief said | WordPress and grimoire at `25fd4f6` |
|---|---|---|
| 1 | "Restore goes to the status stored in `_wp_trash_meta_status`." | `wp_untrash_post` restores to **`draft`** by default (`wp_untrash_post_status` filter, since 5.6). Only the admin list's "Undo" link applies `wp_untrash_post_set_previous_status`. A REST `PUT`/`PATCH` of `status` sets whatever status the client sends and, in core, leaves the `_wp_trash_meta_*` rows behind (`wp_scheduled_delete` later cleans them). M22 offers all three: explicit status, `draft` by default on the admin restore route, and `previous` as an opt-in. |
| 2 | "Trash by default for registered custom types, per the `supports trash` rule." | The REST controller trashes **every** post type by default (`supports_trash = EMPTY_TRASH_DAYS > 0`, attachments additionally need `MEDIA_TRASH`). The "post or page only" rule is in `wp_delete_post( $id, false )`, which the REST route does not use for trashing. Registered types therefore trash by default over REST. |
| 3 | "`delete_posts` for trashing, a stronger cap for force." | `check_delete_permission` (`delete_post`) gates both. Force needs no extra capability. M22 uses one gate. |
| 4 | "Restore requires the edit capability." | True for REST (`PUT`/`PATCH` is an update). The WP admin Restore link checks `delete_post`. For a trashed row both capability checks are evaluated against the status the row had **before** it was trashed (`_wp_trash_meta_status`; `publish` and `future` need the `*_published_*` capabilities). M22 uses the edit capability, evaluated that way. |
| 5 | "WordPress unsticks on trash and trashes the post's comments." | Confirmed. `wp_trash_post` fires the action that runs `unstick_post` (and, for pages, resets `page_on_front`/`page_for_posts`, which grimoire does not read), and `wp_trash_post_comments` sets every comment of the post to `comment_approved = 'post-trashed'` and stores the old values in post meta `_wp_trash_meta_comments_status`. |
| 6 | "A trashed post's menu items stop rendering." | Confirmed for the front end only: `wp_setup_nav_menu_item` marks a `post_type` item `_invalid` when its target is trashed, and `wp_get_nav_menu_items` filters invalid items **only on the front end**. The menu editor still shows them. |
| 7 | "Term counts count only published posts." | Confirmed for WordPress (`_update_post_term_count`, statuses `publish`). grimoire's `SetPostTerms`, `PurgeMenuItemsOf` and the M20 purger use a relationship count that includes drafts. M22 keeps that rule and excludes relationships of **trashed** posts from it. |
| 8 | "Do not purge rows of unmanaged types." | Required, plus a second guard: WordPress only purges rows that carry `_wp_trash_meta_time` older than the cutoff, and so does grimoire. |

M22 is delivered as **three pull requests**, each a complete working increment (`tasks.md`):

- **PR 1, core and REST:** storage port, trash and restore rows exactly as WordPress writes
  them, comments, term counts, the service, REST `DELETE`/`PUT`/`PATCH`/`GET` for posts, pages
  and registered types, public and admin-list reads that hide trash, built-in goldens.
  Requirements 1-14 and 24-27.
- **PR 2, admin API and SPA:** `DELETE /admin/api/posts/{id}` trashes by default, a restore
  route, an empty-trash route, the Trash view in the React admin. Requirements 15-19, 24-27.
- **PR 3, auto-purge and documentation:** config key `content.trash_days`, the trashed-row
  lister, the scheduler purge, user guide and final documentation. Requirements 20-23, 24-28.

The M15 lesson (PR #74) and the M17-M20 practice apply. A service option that `buildHandler`
does not set does not exist in the shipped binary, so each option ships with its assembly edit and
a `cmd/grimoire` test that fails if the edit is removed. A storage contract that hardcodes a
prefix or a vendor passes on SQLite and fails on MySQL and PostgreSQL, so every storage
contract takes `NewReposWithPrefixFunc` and every PR that touches storage is run against
throwaway `mysql:8.0` and `postgres:16` containers before it opens.
## Glossary
- **Trash**: the `post_status` value `trash`.
- **Trashed row**: a `{prefix}posts` row with `post_status = 'trash'`.
- **Managed type**: `post`, `page`, or a post type registered under `content:` (the M18 write
  gate, `content.WritableType`). Every other `post_type` is an unmanaged type.
- **Trash meta**: the three postmeta keys WordPress writes when it trashes a row:
  `_wp_trash_meta_status` (the status before trashing), `_wp_trash_meta_time` (Unix seconds,
  decimal string) and `_wp_desired_post_slug` (the `post_name` before the suffix).
- **Comment-status meta**: postmeta `_wp_trash_meta_comments_status`, a PHP-serialized array
  `comment_ID => comment_approved`, written only when the row has comments.
- **Trash suffix**: the string `__trashed` that WordPress appends to `post_name` of a trashed row.
- **Effective status**: for a trashed row, the value of `_wp_trash_meta_status` (`draft` when the
  meta is absent); for any other row, its `post_status`. Capability checks use it.
- **Trash_Store**: the additive storage port `domain.PostTrasher`, implemented by `wprepo.PostRepo`.
- **Trash_Lister**: the additive storage port `domain.TrashedPostLister`, added in PR 3.
- **Post_Write_Service**: `content.PostWriteService`.
- **REST_API**: the handlers under `/wp-json/wp/v2` for posts, pages and registered types.
- **Admin_API**: the JSON routes under `/admin/api`.
- **Admin_SPA**: the React Spectrum admin under `web/admin`, embedded from `internal/admin/dist`.
- **Scheduler**: `internal/scheduler`, started by `cmd/grimoire/main.go`.
- **Config_Loader**: `internal/config`.
- **Public_Reader**: every public route, theme template and menu render.
- **Trash days**: `content.trash_days`, grimoire's `EMPTY_TRASH_DAYS`.
- **Trash enabled**: Trash days greater than 0 and the Trash_Store wired.
- **Permanent-delete sequence**: `PostWriteService.Delete` as shipped by M18-M20: revisions, managed
  term detach, remaining-relationship purge, unstick, dependents purge, menu-item purge, row delete.
- **Relationship count**: `term_taxonomy.count` set to the number of `term_relationships` rows of the
  term, excluding rows whose object is a trashed row (M22 redefinition, Requirement 5).
## Inherited decisions
Settled on the owner's brief and the M17-M20 precedent, treated as fixed input. Decisions marked
**(sign-off)** are listed again in `design.md` "Decisions awaiting sign-off".
| # | Decision | Consequence |
|---|---|---|
| D1 | **REST `DELETE` trashes by default and deletes permanently only with `?force=true`**, for posts, pages and registered types. Response bodies follow WordPress. **(sign-off: behavior change)** | Req 7, 24. |
| D2 | **Storage writes exactly WordPress's representation**: `post_status='trash'`, the three trash meta keys, the trash suffix, comment-status meta and `post-trashed` comments. A live WordPress on the same database reads it natively. | Req 1, 2, 4. |
| D3 | **One new port, `PostTrasher`, in one transaction per operation.** Existing ports gain no method. | Req 3. |
| D4 | **`PostWriteService.Delete` stays the permanent delete**, unchanged in signature and sequence. Trash is a new method. | Req 6, 13. |
| D5 | **Restore is an update that leaves the trash.** It runs the WordPress untrash steps, so trash meta and comments are cleaned, which core's REST update does not do. | Req 2, 6. |
| D6 | **`content.trash_days` mirrors `EMPTY_TRASH_DAYS`.** Default 30. `0` disables trash: non-force `DELETE` answers `501 rest_trash_not_supported`, and nothing is purged. **(sign-off)** | Req 20. |
| D7 | **Auto-purge runs in the existing Scheduler, calls the permanent-delete sequence, skips unmanaged types, and ignores rows without `_wp_trash_meta_time`**, as WordPress does. | Req 21, 22. |
| D8 | **Comments are trashed and restored with the post**, in WordPress's representation. **(sign-off)** | Req 4. |
| D9 | **Media is not trashed.** `DELETE /media/{id}` keeps requiring `force=true`. | Req 25. |
| D10 | **Tests: TDD, pure Go over SQLite plus the cross-vendor containers, no new dependency.** Property tests use the standard library's `testing/quick`, as `internal/sanitize` does, for the pure functions and the trash/restore round trip. The SPA uses its existing Vitest setup. | Req 26. |
## Requirements
### Requirement 1: Trashing a row writes WordPress's representation
**User Story:** As a site owner who shares a database with WordPress, I want a row that grimoire
trashes to look exactly like a row WordPress trashed, so that either system can list, restore or
purge it.
#### Acceptance Criteria
1. WHEN the Trash_Store trashes a row whose status is not `trash`, THE Trash_Store SHALL, in one transaction, delete any existing `_wp_trash_meta_status` and `_wp_trash_meta_time` postmeta rows of the row, then insert `_wp_trash_meta_status` with the row's previous status and `_wp_trash_meta_time` with the trash time as a decimal Unix-seconds string.
2. WHEN the Trash_Store trashes a row whose `post_name` does not end in the trash suffix, THE Trash_Store SHALL insert `_wp_desired_post_slug` with the current `post_name` and set `post_name` to that value truncated to 191 bytes, without splitting a percent-escape, followed by the trash suffix.
3. WHEN the Trash_Store trashes a row whose `post_name` already ends in the trash suffix, THE Trash_Store SHALL leave `post_name` unchanged and SHALL NOT insert `_wp_desired_post_slug`.
4. WHEN the Trash_Store trashes a row, THE Trash_Store SHALL set `post_status` to `trash` and `post_modified` and `post_modified_gmt` to the trash time, and SHALL leave `post_date`, `post_date_gmt`, `post_author`, `post_parent`, `post_title`, `post_content`, `post_excerpt`, `post_type` and `comment_status` unchanged.
5. WHEN the Trash_Store trashes a row, THE Trash_Store SHALL leave the row's term relationships, revisions, children, attachments, menu items and other postmeta unchanged.
6. IF the row does not exist, THEN THE Trash_Store SHALL return `domain.ErrNotFound` and SHALL change nothing.
7. IF the row's status is already `trash`, THEN THE Trash_Store SHALL return `domain.ErrAlreadyTrashed` and SHALL change nothing.
8. THE Trash_Store SHALL return the status the row had before trashing.
9. THE Trash_Store SHALL accept the trash time from the caller, so a test fixes the clock.
### Requirement 2: Restoring a row runs the WordPress untrash steps
**User Story:** As an editor, I want a restored row to be a clean live row again, with its slug,
comments and meta back, whichever system trashed it.
#### Acceptance Criteria
1. WHEN the Trash_Store restores a trashed row to status S, THE Trash_Store SHALL, in one transaction, set `post_status` to S, delete every `_wp_trash_meta_status` and `_wp_trash_meta_time` postmeta row of the row, and set `post_modified` and `post_modified_gmt` to the restore time.
2. WHEN a trashed row carries `_wp_desired_post_slug`, THE Trash_Store SHALL set `post_name` to that value, made unique under Requirement 2.4, and delete every `_wp_desired_post_slug` row of the row.
3. WHEN a trashed row has no `_wp_desired_post_slug` and its `post_name` ends in the trash suffix, THE Trash_Store SHALL set `post_name` to the name without the suffix, made unique under Requirement 2.4.
4. WHEN the restored `post_name` is held by another row of the same `post_type` (and, for a hierarchical type, the same `post_parent`) whose status is not `trash`, THE Trash_Store SHALL append `-2`, `-3`, and so on until the name is free, as `wp_unique_post_slug` does, and SHALL NOT exceed the 200-byte column.
5. WHERE the caller supplies an explicit slug, THE Trash_Store SHALL use it in place of the stored name, apply Requirement 2.4 to it, and still delete the `_wp_desired_post_slug` rows.
6. IF the resulting `post_name` is empty, THEN THE Trash_Store SHALL leave the name empty and the Post_Write_Service SHALL derive it from the title with the existing slug rule before calling the store.
7. IF the row does not exist, THEN THE Trash_Store SHALL return `domain.ErrNotFound`; IF the row's status is not `trash`, THEN THE Trash_Store SHALL return `domain.ErrNotTrashed`; in both cases it SHALL change nothing.
8. THE Trash_Store SHALL expose `TrashInfo(ctx, id)` returning the previous status, the trash time and the desired slug of a row, each reported absent when its meta is missing, and `ErrNotFound` for a missing row. WHEN a meta key has several rows, THE Trash_Store SHALL use the one with the lowest `meta_id`, as `get_post_meta( ..., true )` does.
9. THE Trash_Store SHALL NOT apply the slug charset rule of M17 to a restored name, so a name written by WordPress restores unchanged.
### Requirement 3: Trash storage is atomic, idempotent and portable
**User Story:** As a maintainer, I want trash and restore proven on SQLite, MySQL and PostgreSQL
and safe to retry.
#### Acceptance Criteria
1. IF any statement of a trash or restore call fails, THEN THE Trash_Store SHALL roll back, leaving every row, meta row, comment and count unchanged.
2. THE Trash_Store SHALL add no migration, column, table or index, and no method to an existing port.
3. THE contracts for the Trash_Store SHALL take `storagetest.NewReposWithPrefixFunc`, SHALL read the vendor and prefix it returns, and SHALL NOT hardcode a table prefix, a vendor name or a placeholder style.
4. THE contracts SHALL run on SQLite always and on MySQL and PostgreSQL when `GRIMOIRE_TEST_MYSQL_DSN` and `GRIMOIRE_TEST_POSTGRES_DSN` are set, and SHALL add no `GRIMOIRE_TEST_*` variable other than the existing ones.
5. THE trash contract SHALL cover: a published row, a draft row, a row with an empty `post_name`, a row whose name is 195 bytes, a row already carrying the suffix, a row with stale `_wp_trash_meta_*` rows, a row with no comments, a missing id, and an already-trashed id.
6. THE restore contract SHALL cover: restore with the desired-slug meta, restore without it, a slug taken by a live row of the same type, a slug held by a live row of another type (no suffix), a hierarchical row whose name is held under a different parent (no suffix), an explicit slug, a row restored to `publish` and to `draft`, a row that is not trashed, and a missing id.
7. BEFORE opening each PR that touches storage, THE author SHALL run the whole storage suite against throwaway MySQL 8.0 and PostgreSQL 16 containers started with the CI image tags, SHALL record the commands and results in the PR, and SHALL remove the containers.
8. WHERE `GRIMOIRE_TEST_WP_DSN` is set, THE real-database test SHALL read the dev WordPress database without writing and SHALL check that every trashed row satisfies the representation of Requirements 1.1-1.3 (trash meta present, suffix present exactly when the desired-slug meta is present).
### Requirement 4: Comments follow the post into and out of the trash
**User Story:** As a site owner, I want a trashed post's comments hidden everywhere and restored with
the post, as WordPress does.
#### Acceptance Criteria
1. WHEN the Trash_Store trashes a row that has comments, THE Trash_Store SHALL, in the same transaction, insert postmeta `_wp_trash_meta_comments_status` holding the PHP-serialized array of `comment_ID => comment_approved` for every comment of the row, then set `comment_approved` to `post-trashed` for every comment of the row, in every prior state.
2. WHEN the Trash_Store trashes a row that has no comments, THE Trash_Store SHALL NOT insert `_wp_trash_meta_comments_status`.
3. WHEN the Trash_Store restores a row that carries `_wp_trash_meta_comments_status`, THE Trash_Store SHALL, in the same transaction, set each listed comment to its recorded `comment_approved`, treating a recorded `post-trashed` as `0`, then delete the meta rows.
4. WHEN a comment listed in `_wp_trash_meta_comments_status` no longer exists, THE Trash_Store SHALL skip it.
5. THE `php.Serialize` function SHALL serialize an integer-keyed map with `i:` keys in ascending key order, so WordPress reads the array back; `php.Unserialize` of the output SHALL yield the same pairs.
6. THE comment repository SHALL exclude `comment_approved = 'post-trashed'` from `List` and `Count` whenever the filter names no status, so no admin list, count or REST `status=any` includes comments of trashed posts.
7. THE system SHALL leave comments that were individually trashed or marked spam before the post was trashed in that state after a restore.
### Requirement 5: Term counts do not include trashed rows
**User Story:** As a visitor, I want category and tag counts to match the posts I can see.
#### Acceptance Criteria
1. THE Relationship count SHALL be the number of `term_relationships` rows of the `term_taxonomy_id` whose `object_id` does not name a row with `post_status = 'trash'`.
2. THE system SHALL compute that count in one shared statement used by `SetPostTerms`, `PurgeMenuItemsOf`, `NavMenuRepo.DeleteItem`, `PurgeTermRelationshipsOf`, and the trash and restore calls.
3. WHEN the Trash_Store trashes or restores a row, THE Trash_Store SHALL, in the same transaction, recount every `term_taxonomy_id` the row is related to, excluding the taxonomies in `domain.NonPostObjectTaxonomies`.
4. THE system SHALL count a relationship whose `object_id` names no row in `{prefix}posts` (for example a link category) as before.
5. THE `docs/compatibility.md` SHALL state that drafts and other non-published rows still count, unlike WordPress.
### Requirement 6: The write service trashes, restores and deletes under the right capabilities
**User Story:** As a maintainer, I want one authorization path for every trash transition.
#### Acceptance Criteria
1. THE Post_Write_Service SHALL provide `Trash(ctx, actor, id)`, `Restore(ctx, actor, id, status)` and the existing `Delete(ctx, actor, p)`; `Delete` SHALL keep its signature and run the Permanent-delete sequence unchanged.
2. WHEN `Trash` or `Delete` runs for a trashed row, THE Post_Write_Service SHALL evaluate `auth.CanDeletePostSuffix` with the row's Effective status; WHEN `Update` or `Restore` runs for a trashed row, THE Post_Write_Service SHALL evaluate `auth.CanEditPostSuffix` with the Effective status.
3. THE Post_Write_Service SHALL return `ErrForbidden` for a missing row, an unmanaged type or a failed capability check, before any other error, as `Delete` does today.
4. IF the Trash_Store is not wired or Trash days is 0, THEN `Trash` SHALL return `ErrTrashUnsupported` after the authorization check.
5. IF the row is already trashed, THEN `Trash` SHALL return `ErrAlreadyTrashed` after the authorization check.
6. WHEN `Trash` succeeds, THE Post_Write_Service SHALL, in this order: unstick a row of type `post` (as `Delete` does), then call `TrashPost`. Revisions, children, menu items, term relationships and attachments SHALL be left in place.
7. WHEN `Update` is called with a status other than `trash` for a trashed row, THE Post_Write_Service SHALL apply the field changes, then call `RestorePost` with that status and the slug rule of Requirement 2.5 and 2.6, and SHALL authorize the new status with the existing publish check.
8. WHEN `Update` is called with status `trash` for a row that is not trashed, THE Post_Write_Service SHALL apply the field changes with the status unchanged, then run `Trash`, which additionally requires the delete capability.
9. WHEN `Update` is called with status `trash` for a trashed row, THE Post_Write_Service SHALL apply the field changes and leave the row trashed.
10. WHEN `Restore` is called with status `previous`, THE Post_Write_Service SHALL use the recorded previous status, or `draft` when none is recorded; WHEN the status is empty it SHALL use `draft`.
11. THE Post_Write_Service SHALL apply `uniquePageSlug`, `resolveSlug` and `slugTaken` to live rows only, so a row with status `trash` never blocks a slug.
12. THE Post_Write_Service SHALL NOT trash a row of an unmanaged type, and SHALL NOT change the M18 gate.
### Requirement 7: REST `DELETE` follows the WordPress semantics
**User Story:** As a developer with a WordPress client, I want `DELETE` to move content to the
trash and `?force=true` to remove it, with WordPress's responses and errors.
#### Acceptance Criteria
1. WHEN `DELETE /wp-json/wp/v2/{posts|pages|rest_base}/{id}` has no `force=true` and trash is enabled, THE REST_API SHALL trash the row and answer `200` with the post object as `GET` returns it for the same actor, with `status` `trash`.
2. WHEN the request has `force=true` (or `1`), THE REST_API SHALL run the Permanent-delete sequence and answer `200 {"deleted": true, "previous": <object as before deletion>}`.
3. IF the row is already trashed and the request has no force, THEN THE REST_API SHALL answer `410` with code `rest_already_trashed` and message `The post has already been deleted.`.
4. IF trash is disabled and the request has no force, THEN THE REST_API SHALL answer `501` with code `rest_trash_not_supported` and message `The post does not support trashing. Set 'force=true' to delete.`.
5. IF the actor may not delete the row, THEN THE REST_API SHALL answer `403` with code `rest_cannot_delete`, before the answers of 7.3 and 7.4.
6. IF the id is unknown or names a row of another type, THEN THE REST_API SHALL answer `404 rest_post_invalid_id`, as before.
7. THE REST_API SHALL answer `force=true` on an already-trashed row with the permanent delete and `200 {deleted, previous}`.
8. WHEN `PUT` or `PATCH` carries `status` other than `trash` for a trashed row, THE REST_API SHALL restore it under Requirement 6.7 and answer `200` with the updated object; WHEN it carries `status: "trash"` for a live row, THE REST_API SHALL trash it under Requirement 6.8.
9. THE REST_API SHALL leave `DELETE` of media, users, comments and terms unchanged.
10. THE REST_API SHALL answer unmanaged-type rows exactly as before (`404`), and every write SHALL keep its CSRF and Application-Password handling.
### Requirement 8: REST reads understand trash
**User Story:** As an API consumer, I want to list and read trashed content when I may edit it.
#### Acceptance Criteria
1. WHEN `GET .../{id}` names a trashed row, THE REST_API SHALL answer `200` with `status` `trash` to an actor with the edit capability of the type, and `404 rest_post_invalid_id` to every other caller.
2. THE REST_API SHALL accept a `status` query parameter on post, page and registered-type collections: a comma-separated list of `publish`, `future`, `draft`, `pending`, `private`, `trash` and `any`, defaulting to `publish`.
3. WHEN the `status` parameter names any value other than `publish` and the caller lacks the edit capability of the type, THE REST_API SHALL answer `403` with code `rest_forbidden_status`.
4. THE REST_API SHALL treat `any` as every status except `trash`.
5. IF the `status` parameter names an unknown value, THEN THE REST_API SHALL answer `400 rest_invalid_param`.
6. THE REST_API SHALL compute `X-WP-Total` and `X-WP-TotalPages` from the same status set as the listing.
7. THE REST_API SHALL render the `link` of a trashed row as WordPress does, from its stored `post_name` including the trash suffix.
8. THE REST_API SHALL leave the default (no `status`) collection response of existing clients unchanged.
### Requirement 9: Public reads never show trash
**User Story:** As a visitor, I want trashed content to disappear from the site at once.
#### Acceptance Criteria
1. THE Public_Reader SHALL answer `404` for the permalink, `?p=`, preview and slug routes of a trashed row, and SHALL NOT list it in any home, archive, category, tag, author, date or paginated listing, or in any count behind them.
2. THE Public_Reader SHALL render a nav-menu item of type `post_type` whose target row is trashed as absent, and SHALL attach that item's children to the item's own parent, so they stay visible at the nearest rendered ancestor, as the WordPress `Walker` shows orphans.
3. THE Admin_API menu reads (`GET /admin/api/menus` and `/menus/{id}`) SHALL keep returning such an item, so the menu editor can still edit or delete it.
4. THE system SHALL leave stored menu rows, postmeta and relationships unchanged by a trash or restore, so a restored row's menu items render again.
5. THE page hierarchy and URL logic SHALL keep every status, including `trash`, in the page graph, so a published child of a trashed page builds its path from the stored (suffixed) ancestor name, as `get_page_uri` does.
6. THE system SHALL NOT change any public read query, because every one already filters `post_status = 'publish'`; a test SHALL pin that a trashed row appears in none of them.
### Requirement 10: Admin reads treat trash as WordPress's admin lists do
**User Story:** As an editor, I want "All" to hide the trash and a Trash view to show it.
#### Acceptance Criteria
1. WHEN an admin list or count filter names no status, THE storage SHALL exclude rows with status `trash`; WHEN the filter names `trash` explicitly, THE storage SHALL return the trashed rows.
2. THE `GET /admin/api/posts` route SHALL accept `status=trash`, answer `400 invalid_status` for other unknown values as before, and exclude trashed rows from an unfiltered list and its `total`.
3. THE dashboard `pages` count SHALL exclude trashed rows; `CountByStatus` with an empty status SHALL mean every status except `trash`, and with status `trash` SHALL count trashed rows.
4. THE Admin_API SHALL include a trashed row in `GET /admin/api/posts/{id}` with status `trash`.
5. THE `AdminPostFilter` SHALL gain no method on `AdminPostRepository`; the exclusion SHALL be the documented meaning of an empty `Statuses`.
6. THE author filter source (`AuthorsOfTypes`) SHALL be unchanged.
### Requirement 11: Sticky posts and revisions on trash
**User Story:** As a maintainer, I want trash to behave like WordPress for sticky posts and revisions.
#### Acceptance Criteria
1. WHEN `Trash` succeeds for a row of type `post`, THE Post_Write_Service SHALL have removed its id from `sticky_posts` using the existing `unstick` step, and SHALL NOT restore it on restore.
2. THE Post_Write_Service SHALL NOT delete revisions or autosaves on trash.
3. WHEN the Permanent-delete sequence runs for a trashed row, THE system SHALL delete its revisions, its `_wp_trash_meta_*`, `_wp_desired_post_slug` and `_wp_trash_meta_comments_status` rows and its comments in every state including `post-trashed`, through the existing steps, with no new step.
4. THE system SHALL NOT read or write `page_on_front`, `show_on_front` or `page_for_posts`, which grimoire does not use.
### Requirement 12: The scheduler does not publish trashed rows
**User Story:** As a site owner, I want a trashed scheduled post to stay trashed.
#### Acceptance Criteria
1. THE publish Scheduler SHALL continue to select only rows with status `future`, so a trashed scheduled row is never published.
2. WHEN a trashed row whose previous status was `future` is restored to `future` with a past date, THE Scheduler SHALL publish it on its next tick like any due row.
### Requirement 13: The permanent-delete sequence is unchanged and covers trashed rows
**User Story:** As a maintainer, I want force delete and emptying the trash to run the tested
M18-M20 sequence.
#### Acceptance Criteria
1. THE REST_API force path, the Admin_API force path, the empty-trash route and the Scheduler purge SHALL all call `PostWriteService.Delete`.
2. THE system SHALL NOT change the step order, the ports or the error handling of `PostWriteService.Delete`, except that its capability check uses the Effective status (Requirement 6.2).
3. THE existing M18-M20 delete tests SHALL pass unchanged apart from the assertions Requirement 7 makes stale, each edited in the phase that owns it with the commit saying so.
### Requirement 14: PR 1 wiring and goldens
**User Story:** As a reviewer, I want PR 1 reachable in the shipped binary and its golden changes
limited to what the brief names.
#### Acceptance Criteria
1. THE `buildHandler` function SHALL construct the post write service with `WithPostTrasher(repos.PostTrash)` and the server with the trash configuration, and a `cmd/grimoire` test through `buildHandler` SHALL fail if either is removed.
2. WHEN the Post_Write_Service is built without a Trash_Store, THE REST_API SHALL answer a non-force `DELETE` with `501 rest_trash_not_supported` and SHALL NOT delete the row.
3. THE built-in golden entries `rest post delete`, `rest post get deleted` and `rest page delete` in both `cmd/grimoire/testdata/builtin_golden/with_registry.json` and `without_registry.json` SHALL change as `design.md` "Golden changes" specifies, and no other existing entry SHALL change.
4. THE golden change SHALL be its own commit, produced with the existing `-update` flag and verified by `scripts/check-golden-trash.sh`, which fails on any other difference.
5. THE PR 1 test steps appended to the golden script SHALL be pure additions at the end of both files.
### Requirement 15: Admin API delete trashes by default
**User Story:** As an editor using the admin, I want Delete to be recoverable.
#### Acceptance Criteria
1. WHEN `DELETE /admin/api/posts/{id}` has no `force=true`, THE Admin_API SHALL trash the row and answer `204`.
2. WHEN the request has `force=true`, THE Admin_API SHALL run the Permanent-delete sequence and answer `204`.
3. IF the row is already trashed and the request has no force, THEN THE Admin_API SHALL answer `409` with code `already_trashed`.
4. IF trash is disabled and the request has no force, THEN THE Admin_API SHALL answer `409` with code `trash_unsupported` and message `trash is disabled; use force=true`.
5. IF the actor may not delete the row, THEN THE Admin_API SHALL answer `403`, as before.
6. THE Admin_API SHALL keep `PUT` of status `trash` and of a status that leaves the trash working through Requirement 6.
7. THE `GET /admin/api/session` response SHALL include `trashEnabled`, true when trash is enabled.
### Requirement 16: Restore route
**User Story:** As an editor, I want a Restore action that does not need the editor form.
#### Acceptance Criteria
1. THE Admin_API SHALL provide `POST /admin/api/posts/{id}/restore` with an optional JSON body `{"status": "<status>"}`, gated by the edit capability and the CSRF check like the other post writes.
2. WHEN the body is empty or `status` is empty, THE Admin_API SHALL restore to `draft`; WHEN `status` is `previous`, THE Admin_API SHALL restore to the recorded previous status; WHEN `status` is another valid status, THE Admin_API SHALL restore to it under the publish check.
3. THE Admin_API SHALL answer `200` with the post detail as `GET /admin/api/posts/{id}` returns it.
4. IF the row is not trashed, THEN THE Admin_API SHALL answer `400 bad_request` with message `post is not in the trash`.
5. IF the actor may not edit the row (Effective status), THEN THE Admin_API SHALL answer `403`.
6. IF the status is invalid, THEN THE Admin_API SHALL answer `400 bad_request`.
### Requirement 17: Empty-trash route
**User Story:** As an editor, I want to empty the trash of one content type in one action.
#### Acceptance Criteria
1. THE Admin_API SHALL provide `POST /admin/api/posts/empty-trash` with JSON body `{"type": "<post|page|registered type>"}`, gated and CSRF-checked like the other post writes.
2. WHEN called, THE Admin_API SHALL run the Permanent-delete sequence for up to 1000 trashed rows of that type, oldest id first, that the actor may delete, and SHALL answer `200 {"deleted": n, "skipped": m, "more": b}`, where `skipped` counts rows the actor may not delete and `more` is true when the type has trashed rows beyond the 1000 examined.
3. IF a row's delete fails for a reason other than authorization, THEN THE Admin_API SHALL stop, answer `500`, and report nothing as deleted beyond what was removed; a retry SHALL continue.
4. IF `type` is missing, unknown or unmanaged, THEN THE Admin_API SHALL answer `400 bad_request`.
5. THE route SHALL collect ids 100 per page and SHALL NOT load every trashed row's content at once.
### Requirement 18: The Trash view in the React admin
**User Story:** As an editor, I want to see, restore and permanently delete trashed content.
#### Acceptance Criteria
1. THE Admin_SPA SHALL add a `Trash` entry to the status picker of the post and page lists, shown after `Scheduled`, which requests `status=trash`.
2. WHILE the Trash status is selected, THE Admin_SPA SHALL show for each row `Restore` and `Delete permanently` actions in place of Edit and Delete, and an `Empty Trash` button when the list is not empty.
3. WHILE any other status or `All` is selected and `trashEnabled` from `GET /admin/api/session` is true, THE Admin_SPA SHALL label the row action `Move to Trash`, ask no confirmation, and show an inline notice with an `Undo` action that restores the row to its previous status; WHILE `trashEnabled` is false, THE Admin_SPA SHALL label it `Delete permanently` and ask for confirmation as in criterion 4.
4. WHEN the editor chooses `Delete permanently` or `Empty Trash`, THE Admin_SPA SHALL ask for confirmation in a destructive dialog stating that the action cannot be undone.
5. WHEN `Restore` is chosen, THE Admin_SPA SHALL call the restore route with no body and reload the list.
6. THE Admin_SPA SHALL show a trashed row opened by id as read-only with a notice and a Restore action, and SHALL NOT offer Save.
7. THE Admin_SPA SHALL show the dashboard `pages` count without trashed rows.
8. THE Admin_SPA components SHALL have Vitest tests, and `internal/admin/dist` SHALL be rebuilt in the same commit as the source, as M8 did.
### Requirement 19: PR 2 wiring and goldens
**User Story:** As a reviewer, I want PR 2 reachable in the shipped binary and its golden changes
pure additions.
#### Acceptance Criteria
1. THE `buildHandler` function SHALL give the admin routes the trash-capable write service, and a `cmd/grimoire` test SHALL fail if that is removed.
2. THE built-in golden files SHALL gain only appended entries in PR 2, verified by `scripts/check-golden-trash.sh` in its additions-only mode, and no existing entry SHALL change.
3. THE PR 2 SHALL add no storage port and no migration.
### Requirement 20: The `content.trash_days` configuration key
**User Story:** As an operator, I want to set how long trash is kept, as WordPress's constant does.
#### Acceptance Criteria
1. THE Config_Loader SHALL accept `content.trash_days` as a non-negative integer, defaulting to 30 when absent.
2. WHERE `content.trash_days` is 0, THE system SHALL run with trash disabled: non-force REST `DELETE` answers `501`, non-force admin `DELETE` answers `409 trash_unsupported`, and the purge does not run.
3. IF `content.trash_days` is negative or not an integer, THEN THE Config_Loader SHALL fail to load with an error naming the key.
4. WHERE the environment variable `GRIMOIRE_CONTENT_TRASH_DAYS` is set to an integer, THE Config_Loader SHALL use it in place of the file value.
5. THE Config_Loader SHALL keep rejecting unknown keys inside `content:`.
6. THE four files under `configs/` SHALL document the key in a comment and leave the default in place.
### Requirement 21: The trashed-row lister
**User Story:** As a maintainer, I want one storage read that serves the purge and Empty Trash.
#### Acceptance Criteria
1. THE Trash_Lister SHALL provide `TrashedPosts(ctx, types []string, afterID int64, limit int)` returning rows with status `trash` and `post_type` in `types`, ordered by `ID` ascending after `afterID`, each with its trash time from `_wp_trash_meta_time` and a flag reporting whether that meta exists.
2. THE Trash_Lister SHALL return an empty slice for empty `types`.
3. THE Trash_Lister SHALL read the trash time as the first `_wp_trash_meta_time` row by `meta_id` and SHALL parse it in Go, so a non-numeric stored value yields "absent" and never a SQL error on any vendor.
4. THE Trash_Lister SHALL NOT write, and SHALL add no method to an existing port.
5. THE contract SHALL take `NewReposWithPrefixFunc`, run on all three vendors, and cover: managed and unmanaged types, a row without the meta, a non-numeric meta, pagination across the cursor, and an empty `types`.
### Requirement 22: Auto-purge
**User Story:** As an operator, I want trash emptied after Trash days, through the safe delete.
#### Acceptance Criteria
1. WHILE Trash days is greater than 0, THE Scheduler SHALL, at most once per hour and on its first tick, list trashed rows of managed types in batches of 100 and permanently delete each whose trash time is earlier than now minus Trash days, by calling `PostWriteService.Delete` with its internal principal.
2. THE Scheduler SHALL NOT delete a row of an unmanaged type, a row without `_wp_trash_meta_time`, or a row whose trash time is not earlier than the cutoff.
3. THE Scheduler principal SHALL hold the four `delete_` capability verbs for `posts`, `pages` and each registered capability suffix, and no other new capability.
4. WHEN one delete fails, THE Scheduler SHALL log it and continue with the next row, as the publish loop does.
5. WHEN the row no longer exists or is no longer trashed by the time it is deleted, THE Scheduler SHALL skip it without logging an error.
6. THE Scheduler SHALL log the number of rows purged per run at info level and nothing when it purged none.
7. WHEN Trash days is 0, THE Scheduler SHALL NOT list trashed rows.
### Requirement 23: PR 3 wiring
**User Story:** As a reviewer, I want the purge reachable in the shipped binary.
#### Acceptance Criteria
1. THE `buildScheduler` function SHALL pass the lister, the delete service, Trash days and the delete capability suffixes to the Scheduler, and a `cmd/grimoire` test SHALL fail if any is removed.
2. A `cmd/grimoire` test through `buildHandler`, `buildScheduler` and one `tick` SHALL show an expired trashed post and page and a registered-type row purged with the M18-M20 cleanup, and a fresh trashed row, an unmanaged-type trashed row and a row without the meta kept.
3. THE `test/e2e` scheduler test SHALL gain one purge case over the real binary assembly.
### Requirement 24: The behavior change is declared and documented
**User Story:** As an operator with existing clients, I want to know what changes and how to keep the
old behavior.
#### Acceptance Criteria
1. THE `docs/compatibility.md` SHALL state under an `M22` heading that `DELETE` without `force=true` now trashes, and that clients which relied on a permanent delete must send `?force=true`.
2. THE document SHALL list the new response bodies, the error codes `rest_already_trashed`, `rest_trash_not_supported` and `rest_cannot_delete`, and the changed code for a forbidden delete (`rest_cannot_edit` before M22).
3. THE document SHALL give a migration note: `content.trash_days: 0` turns trash off, after which a non-force `DELETE` answers `501`, not the pre-M22 permanent delete.
4. THE document SHALL replace every statement that says delete is permanent or that there is no trash (M17 "Delete", M18 PR 2, M19 PR 3, M20 PR 1 and PR 2 known gaps).
5. THE sign-off list in `design.md` SHALL name the behavior change first.
### Requirement 25: Types and routes M22 leaves alone
**User Story:** As a maintainer, I want the scope fenced.
#### Acceptance Criteria
1. THE system SHALL keep `DELETE /wp-json/wp/v2/media/{id}` requiring `force=true` with its present answers, because WordPress's `MEDIA_TRASH` is false.
2. THE system SHALL keep terms, users, comments and menu items permanent deletes.
3. THE system SHALL NOT trash, restore, purge or otherwise write a row of an unmanaged type.
4. THE system SHALL NOT fire WordPress hooks or touch an object cache; this limit applies to every grimoire write.
5. THE system SHALL write the status `trash` only, never `auto-draft` or `inherit` transitions.
### Requirement 26: Tests and delivery
**User Story:** As a maintainer, I want three complete, verified increments.
#### Acceptance Criteria
1. THE system SHALL have a RED test task before each implementation task, with the failure reason stated, except documentation, verification and gate tasks.
2. FOR EACH property in `design.md` "Correctness Properties", THE system SHALL have one property test using `testing/quick` with a seeded generator and at least 100 iterations, tagged with its property number.
3. THE system SHALL run `gofmt -l .`, `go vet ./...`, `go build ./...` and `go test ./...` green after every phase, and `go test -count=2 ./...` in each final gate.
4. THE system SHALL drive every behavior in Requirements 7-12, 15-17, 22 and 23 through `buildHandler` or `buildScheduler`, not through a hand-built `web.Server`.
5. FOR EACH option or wiring edit, a task SHALL state which named test fails when it is removed and SHALL record that the removal was tried.
6. EACH PR SHALL end with a final gate that runs the full checks, the `git` checks that prove its port, schema and capability claims, and, when it touches storage, the cross-vendor run of Requirement 3.7.
7. PR 2 SHALL be mergeable on top of PR 1 only, PR 3 on top of PR 2 only, and each SHALL leave `main` releasable.
### Requirement 27: Real-database confirmation
**User Story:** As a maintainer, I want the representation checked against a real WordPress database
where I have one.
#### Acceptance Criteria
1. WHERE `GRIMOIRE_TEST_WP_DSN` is set, THE PR 1 real-database test SHALL be read-only and SHALL skip at its first line without the variable.
2. THE test SHALL report the number of trashed rows found per post type, check Requirement 3.8, and check that grimoire's REST mapper and `ByID` read each trashed row without error.
3. THE test SHALL check that every trashed row's `_wp_trash_meta_status` is a known status, and SHALL log, not fail on, rows that violate the representation, so a site's legacy data cannot break CI.
### Requirement 28: Documentation
**User Story:** As an operator and a reader of the plans, I want the documents to say what shipped.
#### Acceptance Criteria
1. EACH PR SHALL update `docs/compatibility.md` for what it ships and SHALL leave no sentence that contradicts the shipped behavior.
2. PR 3 SHALL add the `22-trash-and-restore` row to `plans/README.md` (partially shipped after PR 1 and PR 2, implemented after PR 3), update `plans/post-m10-gap-analysis/README.md` (remove the trash entries, add shipped paragraphs and fix "Recommended next"), update `plans/wordpress-core-parity-roadmap/tasks.md` where it records the old delete behavior, and update the REST sentence in `docs/user-guide.md`.
3. THE documents SHALL record what remains open: trashed comments as a first-class admin view, trash for media, hooks and the object cache, published-only term counts, `wp_add_trashed_suffix_to_post_name_for_trashed_posts` for unsuffixed legacy trashed rows, and bulk trash.
## Out of scope
- **Trash for media, users, terms, comments and menu items.**
- **Bulk trash and bulk restore** beyond Empty Trash.
- **WordPress hooks, the object cache and `wp_scheduled_delete` as an external cron.** grimoire's Scheduler replaces the cron on its own.
- **Renaming unsuffixed legacy trashed rows** (`wp_add_trashed_suffix_to_post_name_for_trashed_posts`).
- **Published-only term counts.**
- **A first-class trashed-comments admin view.** Comments are hidden and restored with the post only.
- **A configurable purge cadence.** The cadence is one hour.
- **Per-type opt-out of trash** (`rest_{type}_trashable` filter).
## Traceability
| Brief item | Requirements |
|---|---|
| REST trash by default, `force`, bodies and errors | 7, 14, 24 |
| Media not trashed | 25 |
| Restore and slug (`__trashed`, `_wp_desired_post_slug`) | 2, 6, 7.8 |
| Admin delete, restore, UI, list filter | 10, 15, 16, 17, 18 |
| Public reads, REST collections, counts, menus, sticky, comments, revisions, scheduler | 4, 5, 8, 9, 10, 11, 12 |
| Permanent-delete sequence for force and empty | 13, 17 |
| Auto-purge, config, unmanaged types | 20, 21, 22, 23 |
| Capabilities and M17 derived capabilities | 6, 22.3 |
| Shared-database representation | 1, 2, 3.8, 4, 27 |
| Behavior change, migration note, goldens | 14, 19, 24 |
| TDD, wiring, cross-vendor, docs | 3, 23, 26, 28 |
