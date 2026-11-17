# Requirements Document
M18 — Write hardening: type-safe writes, built-in term sanitization, delete cleanup, page parent
## Introduction
M17 (`../17-custom-types-write`) shipped custom post type and taxonomy writes and
recorded four gaps it deliberately left alone (`../17-custom-types-write/design.md`,
Findings 3, 4, 5 and 7). Each is a defect or omission in the **built-in** write
path, and each is visible on a site that shares its database with a live
WordPress install. M18 closes all four.

| Item | M17 finding | Gap in the code at `main` `bda2db1` |
|---|---|---|
| 1 | 5 | `PostWriteService.Update/Delete` evaluate the stored row's `post_type` against the legacy `*_posts` capabilities when the type is neither built-in nor registered (`CapabilitySuffixFor`, `internal/content/postterms.go:25-38`). `PUT`/`DELETE /admin/api/posts/{id}` address rows by id with no type check (`internal/web/adminapi_posts.go:306,365`), so an imported `shop_order`, `attachment` or `nav_menu_item` row is editable and deletable by any holder of `edit_posts`/`delete_posts`. The revision, autosave and term-assignment services share the same legacy decision (`internal/content/revisions.go:59-72`, `autosave.go:37-50`, `postterms.go:132-152`). |
| 3 | 7 | `TermWriteService` sanitizes names only for registered taxonomies (`prepareRegistered`, `writeservices.go:573-588`). `category` and `post_tag` names reach `{prefix}terms.name` verbatim over REST and the admin API. |
| 2 | 4 | `PostRepo.Delete` is a single `DELETE FROM {prefix}posts` (`internal/storage/wprepo/writers.go:105-115`). The M17 term detach runs only for registered types (`writeservices.go:472-478`). A deleted post or page leaves `term_relationships`, a stale `term_taxonomy.count`, `postmeta`, `comments` and `commentmeta`, and leaves its children and attachments pointing at a missing parent. |
| 4 | 3 | No path writes `post_parent` for `page`. `parsePostWrite` and `parseRESTPostWrite` apply `parent` to registered types only (`adminapi_posts.go:152`, `rest_posts.go:307`), and `PostWriteService.Update` ignores `p.ParentID` for built-ins. M15 made nested page URLs readable, but a page tree can only be created by editing the database. |

**One finding changes the brief.** The brief asks whether the REST routes
`/wp/v2/posts/{id}` and `/wp/v2/pages/{id}` share the hole in item 1. They do
not. `handleRESTPostUpdate` and `handleRESTPostDelete` load the row and answer
`404 rest_post_invalid_id` when `current.Type != typ`
(`internal/web/rest_posts.go:450` and `:538`), before any capability
check. The hole is the admin API, and the services behind the admin API's
sibling routes (revisions, autosave, term assignment). M18 therefore fixes it
in the shared service layer, which also protects the REST custom-type routes,
the scheduler and any future caller, and pins the REST behavior with
characterization tests rather than changing it.

**A second finding raises the stakes of item 3.** `sanitize.PostTitle` at tier B
decodes entities: `R&amp;D` becomes `R&D`, and `A < B` becomes `A  B`. WordPress
stores term names with `&amp;`. Re-sanitizing a stored name on an unrelated
update (a slug-only or parent-only change, which the REST and admin handlers
implement by merging the stored name into the request) would silently rewrite
imported data. Requirement 6 therefore makes "a name identical to the stored
name is not a caller value" a requirement, as M10a Finding 3 did for post
fields.

M18 is delivered as **two pull requests**, each a complete working increment
(see `tasks.md`):

- **PR 1, security (items 1 and 3):** Requirements 1-6 and the cross-cutting
  Requirements 15-19 as they apply.
- **PR 2, completeness (items 2 and 4):** Requirements 7-14 and the same
  cross-cutting requirements.

The M15 lesson (PR #74) and M17's practice apply: a service option that is not
set in `buildHandler` does not exist in the shipped binary. Every PR ships its
assembly edit and a test that drives `buildHandler` and fails if the wiring is
removed.
## Glossary
- **Built-in type** — `post` or `page`. **Built-in taxonomy** — `category` or
  `post_tag`. **Registered type / taxonomy** — an entry of the `content:`
  section held by `registry.Registry` (M12).
- **Writable type** — a post type the write services may modify: `post`,
  `page`, any registered type, and the empty string (treated as `post`).
  Every other `post_type` value found on a stored row is an **unwritable
  type**, for example an imported `shop_order`, `attachment`, `nav_menu_item`
  or `revision`, or a type whose registry entry was later removed.
- **Type gate** — the check `content.WritableType(registry, type)` that the
  write services run on the stored row's type before the capability check.
- **Stored name** — `domain.Term.Name` as returned by `TermReader.TermsByIDs`
  for the term being updated.
- **Caller name** — the `name` value supplied by the request after the handler
  has merged omitted fields from the stored term.
- **Dependents of a post** — the rows that reference a post id and are not the
  post row itself: `term_relationships`, `postmeta`, `comments`, `commentmeta`
  (of those comments), revisions (`post_type = 'revision'`, `post_parent = id`),
  child rows of the same hierarchical type, and attachments whose `post_parent`
  is the post.
- **Purger** — the additive storage port `domain.PostDependentsPurger`
  introduced by PR 2 to delete a post's comments, comment meta and postmeta and
  to re-point its children and attachments in one transaction.
- **Page hierarchy** — `content.PageHierarchy` (M15), the page graph over
  `post_parent` loaded through `domain.PageReader`; `PageService.Resolve` is the
  M16-fixed path walk.
- **Page parent write** — a `parent` value accepted on `POST` and `PUT`/`PATCH`
  `/wp-json/wp/v2/pages` and on `POST`/`PUT /admin/api/posts` with `type`
  `page`.
- **Plain-text name** — the result of `Policy.SanitizeAt(sanitize.PostTitle,
  sanitize.TierB, name)` followed by `strings.TrimSpace`, the same reduction M17
  applies to registered taxonomies (`cleanName`).
## Inherited decisions
Settled on the owner's brief and treated as fixed input. Decisions marked
**(sign-off)** are listed again in `design.md` "Decisions awaiting sign-off".
| # | Decision | Consequence |
|---|---|---|
| D1 | **The type gate lives in the content services, not in the handlers.** `PostWriteService`, `PostTermsWriteService`, `RevisionWriteService` and `AutosaveService` refuse an unwritable stored type. One gate covers admin API, REST, scheduler and future callers. | Requirements 1, 2, 4. |
| D2 | **Response for an unwritable type: `403` on the admin write routes, `404` on the revision and autosave routes, `404` on REST by-id routes.** Each follows the convention its surface already uses: `PostWriteService` returns `ErrForbidden` for a missing id (existence is not leaked), the revision and autosave services return `ErrNotFound` for an unauthorized parent, and REST answers `404 rest_post_invalid_id` for a row of another type. A row of an unwritable type is therefore indistinguishable from "you may not touch this" and no new status code or error body appears. **(sign-off)** | Requirements 1, 2, 3. |
| D3 | **Reads are unchanged.** `GET /admin/api/posts/{id}`, the REST `GET` routes, public routes, `migrate -check` and every list keep serving unwritable types exactly as before. | Requirements 1.6, 3. |
| D4 | **Revision and autosave services adopt the derived capability suffix.** Adding the type gate to them naturally replaces `auth.CanEditPost` with `auth.CanEditPostSuffix(CapabilitySuffixFor(...))`, closing the same legacy-suffix gap M17 closed for the post service. **(sign-off)** | Requirement 2. |
| D5 | **Term names: one rule for every taxonomy.** Every taxonomy name goes through the plain-text reduction at tier B regardless of the actor's tier, empty-after-reduction is rejected with `ErrTermNameEmpty`, and a caller name identical to the stored name is stored as is. Slug rules stay registered-only. **(sign-off)** | Requirements 5, 6. |
| D6 | **Delete mirrors `wp_delete_post` for the dependents the existing ports can reach, plus one additive port for the rest.** Comments are deleted (with their meta), not left; postmeta is deleted; term relationships are detached with counts recomputed through the existing `SetPostTerms`; children of a hierarchical type and attachments are re-pointed to the deleted post's own parent. Revisions are already deleted first (M7). **(sign-off)** | Requirements 7-10. |
| D7 | **One new storage port, additive, implemented once.** `domain.PostDependentsPurger` is implemented by `wprepo.PostRepo` over `bun`, so MySQL, PostgreSQL and SQLite share the implementation; the shared contract suite runs it on every vendor. No existing port gains a method. | Requirements 10, 16. |
| D8 | **Page parent reuses the M17 post-parent guard with `page` treated as a hierarchical type.** Valid parent of the same type, not self, no cycle, omitted keeps the stored value, `0` clears. `post` keeps ignoring `parent`. | Requirements 11-13. |
| D9 | **A page that changes parent gets a unique slug among its new siblings; no charset rule.** The uniqueness step auto-suffixes (`-2`, `-3`), as M17 does for registered types and WordPress does on every update. The charset rule is not applied, because imported page slugs can be percent-encoded. Pages whose parent does not change keep today's behavior. **(sign-off)** | Requirement 13. |
| D10 | **Delete stays permanent.** No trash status, no `?force` semantics, no change to REST `200 {deleted, previous}` or admin `204`. | Requirements 7-10. |
| D11 | **Built-in golden files are expected not to change and are never regenerated to make a test pass.** If any byte changes, the change is a reviewed, separate commit that names the cause. | Requirement 15. |
| D12 | **Tests: TDD, pure Go over SQLite, no new dependency, no property-based tests.** A RED test task precedes every implementation task. The contract suite for the new port runs on SQLite always and on MySQL/PostgreSQL under the existing `GRIMOIRE_TEST_*` variables. | Requirements 18. |
## Requirements
### Requirement 1: Writes to unwritable post types are refused
**User Story:** As a site owner who shares a database with WordPress and its
plugins, I want grimoire's admin API to refuse to edit or delete rows of post
types it does not manage, so that an editor cannot alter orders, attachments or
menu items through the post editor.
#### Acceptance Criteria
1. WHEN `PostWriteService.Update` loads a stored row whose `post_type` is an unwritable type, THE system SHALL return `ErrForbidden` before evaluating any capability, taking a revision snapshot, sanitizing or writing.
2. WHEN `PostWriteService.Delete` loads a stored row whose `post_type` is an unwritable type, THE system SHALL return `ErrForbidden` before evaluating any capability, deleting revisions, detaching terms or deleting the row.
3. WHEN `PostWriteService.Create` receives a post whose `Type` is an unwritable type, THE system SHALL return `ErrForbidden` and write nothing.
4. WHEN `PUT /admin/api/posts/{id}` or `DELETE /admin/api/posts/{id}` addresses a row of an unwritable type, THE system SHALL answer `403 forbidden`, the same response a missing id and a missing capability produce, and SHALL leave the row unchanged.
5. THE system SHALL treat `post`, `page`, the empty string and every registry entry as writable types, and SHALL evaluate their capabilities exactly as before M18.
6. THE system SHALL serve `GET /admin/api/posts/{id}`, `GET /admin/api/posts` and every public and REST read of a row of an unwritable type with the same status, headers and body as before M18.
7. WHILE no `content:` section is configured, THE system SHALL treat every `post_type` other than `post` and `page` as unwritable.
### Requirement 2: The sibling by-id admin paths apply the same gate
**User Story:** As a site owner, I want revisions, autosaves and term
assignment to refuse unwritable rows too, so that closing the editor route does
not leave a side door.
#### Acceptance Criteria
1. WHEN `PostTermsWriteService.SetPostTerms` loads a stored row of an unwritable type, THE system SHALL return `ErrForbidden` and write no `term_relationships` row.
2. WHEN `RevisionWriteService.List`, `Get` or `Restore` is called for a parent row of an unwritable type, THE system SHALL return `domain.ErrNotFound`, so the routes answer `404`, and `Restore` SHALL write nothing.
3. WHEN `AutosaveService.Save` or `Newer` is called for a parent row of an unwritable type, THE system SHALL return `domain.ErrNotFound` and write nothing.
4. WHEN a revision or autosave route addresses a registered type, THE system SHALL authorize with `auth.CanEditPostSuffix` and the suffix `CapabilitySuffixFor` returns for that type, so `edit_posts` alone does not permit restoring a revision of a `capability_type: product` row.
5. THE system SHALL evaluate revision and autosave capabilities for `post` and `page` rows exactly as before M18.
6. THE system SHALL construct `RevisionWriteService`, `AutosaveService` and `PostTermsWriteService` with the registry option in `buildHandler`.
### Requirement 3: REST by-id routes keep refusing rows of another type
**User Story:** As a maintainer, I want the REST behavior the brief asked about
pinned by tests, so that a later refactor cannot open the same hole there.
#### Acceptance Criteria
1. WHEN `PUT`, `PATCH` or `DELETE /wp-json/wp/v2/posts/{id}` addresses a row whose `post_type` is not `post`, THE system SHALL answer `404 rest_post_invalid_id` and leave the row unchanged.
2. WHEN `PUT`, `PATCH` or `DELETE /wp-json/wp/v2/pages/{id}` addresses a row whose `post_type` is not `page`, THE system SHALL answer `404 rest_post_invalid_id` and leave the row unchanged.
3. WHEN a REST custom-type route addresses a row of a different type, including an unwritable one, THE system SHALL answer `404 rest_post_invalid_id` and leave the row unchanged.
4. THE system SHALL keep the REST by-id check ahead of the capability check, as before M18.
5. THE system SHALL change no REST `GET` response.
### Requirement 4: The scheduler does not retry unwritable rows
**User Story:** As an operator, I want the publish scheduler to skip rows the
write service refuses, so that an imported `future` row of an unmanaged type
does not log an error on every tick.
#### Acceptance Criteria
1. WHEN `DueScheduled` returns a row of an unwritable type, THE system SHALL skip it without calling `Update` and SHALL log it at debug level only.
2. WHEN `DueScheduled` returns a row of a writable type, THE system SHALL publish it exactly as before M18.
3. THE `PostWriteService` SHALL expose `WritableType(typ string) bool` and the scheduler SHALL use it through an optional interface assertion, so a fake `postUpdater` without the method behaves as before.
### Requirement 5: Built-in term names are reduced to plain text on write
**User Story:** As a site owner, I want category and tag names sanitized at the
write boundary like every other field, so that markup cannot be stored in a name
that themes and clients may render unescaped.
#### Acceptance Criteria
1. WHEN `TermWriteService.Create` receives a term in `category` or `post_tag`, THE system SHALL reduce `name` to a plain-text name before writing.
2. WHEN `TermWriteService.Update` receives a term in `category` or `post_tag` whose caller name differs from the stored name, THE system SHALL reduce `name` to a plain-text name before writing.
3. IF the plain-text name is empty, THEN THE system SHALL return `ErrTermNameEmpty` and write nothing, on REST (`400 rest_invalid_param`) and on the admin API (`400 bad_request`).
4. THE system SHALL reduce names at tier B for every actor, so a holder of `unfiltered_html` does not reach the tier-C bypass.
5. WHEN the write succeeds, THE system SHALL return the stored (reduced) name in the REST and admin responses.
6. THE system SHALL leave slug handling for `category` and `post_tag` unchanged: no charset rule, no sibling-uniqueness rule.
7. THE system SHALL apply the same reduction to registered taxonomies it applies today, through one code path.
8. THE `TermWriteService` SHALL run the name step after the capability and vocabulary checks and before `validateParent`, so an unauthorized caller sees `403` before any `400`.
### Requirement 6: Stored term names stay readable and untouched
**User Story:** As a site owner with an imported WordPress database, I want
existing term names left exactly as stored, so that adopting grimoire never
rewrites my taxonomy.
#### Acceptance Criteria
1. WHEN `TermWriteService.Update` receives a caller name byte-identical to the stored name, THE system SHALL write the stored name unchanged and SHALL NOT apply the reduction, so a slug-only or parent-only update never rewrites `News &amp; Events` to `News & Events`.
2. WHEN a stored name contains markup or entities, THE system SHALL return it unchanged on every read, including `GET /wp-json/wp/v2/categories`, `GET /admin/api/terms` and the public term archives.
3. WHEN `TermWriteService.Update` cannot read the stored term, THE system SHALL return the read error and write nothing.
4. THE system SHALL perform the stored-name read once per update.
5. THE system SHALL NOT run a backfill, migration or write-back over stored term names.
### Requirement 7: Deleting a post or page detaches its terms and corrects counts
**User Story:** As a site owner, I want a deleted post's tag and category links
removed, so that term counts and archives stay correct for both grimoire and
WordPress.
#### Acceptance Criteria
1. WHEN `PostWriteService.Delete` removes a row of type `post` or `page`, THE system SHALL detach the post from `category` and `post_tag` through `SetPostTerms(ctx, id, taxonomy, nil)` before deleting the row.
2. WHEN the row is of a registered type, THE system SHALL detach every taxonomy in the registry's effective `Taxonomies` for that type, as M17 does.
3. WHEN the row is of a built-in type, THE system SHALL also detach every registered taxonomy whose `ObjectTypes` names that type.
4. WHEN the detach completes, THE system SHALL have recomputed `term_taxonomy.count` for every affected term through `SetPostTerms`, so no term count includes the deleted post.
5. THE system SHALL delete terms, archives and other posts' relationships for the same terms never.
6. WHILE no detacher is configured, THE system SHALL skip the step, so a service built without the option behaves as before M18.
### Requirement 8: Deleting a post or page removes its postmeta and comments
**User Story:** As a site owner, I want a deleted post's metadata and comments
removed, so that the shared database does not accumulate rows no screen can
reach.
#### Acceptance Criteria
1. WHEN `PostWriteService.Delete` removes a row of any writable type, THE system SHALL delete every `{prefix}postmeta` row with `post_id` equal to the post id.
2. WHEN the post has comments, THE system SHALL delete every `{prefix}comments` row with `comment_post_ID` equal to the post id in every `comment_approved` state (approved, pending, spam, trash), as `wp_delete_post` does.
3. WHEN comments are deleted, THE system SHALL delete the `{prefix}commentmeta` rows of exactly those comments first.
4. THE system SHALL leave the comments, comment meta and postmeta of every other post unchanged.
5. THE system SHALL perform the postmeta, commentmeta and comment deletes in one database transaction.
6. THE system SHALL NOT change `PostRepo.Delete`; the purge is a separate port call made by the service.
### Requirement 9: Deleting a post re-points its children and attachments
**User Story:** As a site owner, I want the children of a deleted page and the
media attached to a deleted post to follow WordPress, so that no row keeps a
`post_parent` that names a missing row.
#### Acceptance Criteria
1. WHEN `PostWriteService.Delete` removes a row of a hierarchical type (`page` or a registered hierarchical type), THE system SHALL set `post_parent` of every row of the same type whose `post_parent` is the deleted id to the deleted row's own `post_parent`.
2. WHEN any writable type is deleted, THE system SHALL set `post_parent` of every `attachment` row whose `post_parent` is the deleted id to the deleted row's own `post_parent`.
3. THE system SHALL NOT change `post_parent` of rows of a non-hierarchical type, of revisions, or of a row of a different type than the deleted one other than attachments.
4. THE system SHALL NOT change `post_modified` or any other column of a re-pointed row.
5. THE system SHALL perform both re-point statements in the same transaction as Requirement 8.
6. WHEN the deleted row has `post_parent` 0, THE system SHALL re-point children and attachments to 0.
### Requirement 10: Delete ordering, failure behavior and the additive port
**User Story:** As a maintainer, I want the delete sequence defined so that a
failure never leaves a half-deleted post that cannot be retried.
#### Acceptance Criteria
1. THE `PostWriteService.Delete` SHALL run its steps in this order after the capability check: revisions, term detach, purge, row delete.
2. IF any step fails, THEN THE system SHALL return the error and SHALL NOT run the later steps, so the post row remains and the delete can be retried.
3. THE system SHALL make every step idempotent, so a retry after a partial failure completes the delete.
4. THE system SHALL add `domain.PostDependentsPurger` with one method, `PurgePostDependents(ctx, postID, parentID int64, reparentType string) error`, where an empty `reparentType` skips the same-type re-point.
5. THE `wprepo.PostRepo` SHALL implement `PostDependentsPurger`, and `storage.Set` SHALL expose it, for every database vendor through the one implementation.
6. THE `PurgePostDependents` method SHALL return no error when the post has no dependents and SHALL NOT require the post row to exist.
7. THE shared storage contract suite SHALL cover the port on SQLite always and on MySQL and PostgreSQL when their `GRIMOIRE_TEST_*` variables are set.
8. WHILE no purger is configured, THE `PostWriteService` SHALL skip the purge step.
### Requirement 11: REST page parent write
**User Story:** As an editor using a WordPress REST client, I want to set a
page's parent over `/wp-json/wp/v2/pages`, so that I can build page trees
without touching the database.
#### Acceptance Criteria
1. WHEN a caller who may create pages sends `POST /wp-json/wp/v2/pages` with `parent` naming a page, THE system SHALL store it in `post_parent` and answer `201` with `parent` equal to the stored value.
2. WHEN a caller who may edit the page sends `PUT` or `PATCH /wp-json/wp/v2/pages/{id}` with `parent`, THE system SHALL store it and answer `200` with the stored value.
3. WHEN the request omits `parent`, THE system SHALL keep the stored parent.
4. WHEN the request sets `parent` to `0`, THE system SHALL make the page top-level.
5. IF the parent is invalid or would create a cycle, THEN THE system SHALL answer `400 rest_invalid_param` and write nothing.
6. THE system SHALL answer `403 rest_cannot_edit` before any parent validation when the caller lacks the capability, per M17's check order.
7. THE system SHALL ignore `parent` on `/wp-json/wp/v2/posts`, as before M18.
### Requirement 12: Admin API page parent write
**User Story:** As an editor using the admin UI, I want the same page parent
write on the admin API, so that both surfaces agree.
#### Acceptance Criteria
1. WHEN `POST /admin/api/posts` or `PUT /admin/api/posts/{id}` carries `parent` and the stored or requested type is `page`, THE system SHALL store it under the same rules as Requirement 11.
2. WHEN `PUT /admin/api/posts/{id}` addresses a stored page and the body omits `type`, THE system SHALL apply `parent` using the stored type, not the defaulted `post`.
3. WHEN the post is a page, THE admin post detail response SHALL carry `parent`, omitted when `0`, as for registered types.
4. IF the parent is invalid or would create a cycle, THEN THE system SHALL answer `400 bad_request` with `invalid parent` and write nothing.
5. THE system SHALL ignore `parent` for `post`, as before M18.
### Requirement 13: The page parent guard
**User Story:** As a site owner, I want page parent writes validated, so that no
write can create a cycle or point at a row that is not a page.
#### Acceptance Criteria
1. WHEN a page write supplies a non-zero parent, THE system SHALL accept it only if a row of type `page` with that id exists, using `PostNodeReader` for type `page`.
2. IF the parent is the page itself, THEN THE system SHALL reject it with `ErrPostParentCycle`.
3. IF the parent is a descendant of the page, THEN THE system SHALL reject it with `ErrPostParentCycle`, using `PageHierarchy.isAncestorOrSelf`.
4. IF the parent names no page, including a row of another type, THEN THE system SHALL reject it with `ErrPostParentInvalid`.
5. WHEN an update leaves `parent` equal to the stored value, THE system SHALL NOT validate it, so an imported page with a dangling stored parent stays editable.
6. WHEN a page create or an update changes the parent, THE system SHALL make the slug unique among the new siblings by appending `-2`, `-3` and so on, without applying a charset rule.
7. WHEN an update does not change the parent, THE system SHALL leave the slug exactly as the caller supplied it, as before M18.
8. WHILE no node reader is configured, THE system SHALL fail a non-zero parent write with an error rather than skip the check.
9. THE system SHALL run the guard after the capability check and the content sanitization, as for registered types.
### Requirement 14: Page URLs follow the parent write
**User Story:** As a visitor and as a site owner, I want a moved page to serve at
its new nested URL and its old URL to redirect, so that links keep working.
#### Acceptance Criteria
1. WHEN a page is given a parent, THE REST response `link` SHALL be the nested URL built from `PageService.AncestryFor`, the same computation the public route uses.
2. WHEN a published page is moved, THE system SHALL serve it at its new nested path with `200` and SHALL answer its previous path with the M15 `301` to the new path.
3. WHEN a moved page is not published, THE system SHALL answer `404` for its previous path and never a redirect, per the M16 `Resolve` fix.
4. WHEN two published pages share a slug under different parents, THE system SHALL serve each at its own path with its own body, per the M16 fix.
5. THE system SHALL load the hierarchy per request and add no cache, so a move takes effect on the next read.
### Requirement 15: Built-in behavior is otherwise unchanged
**User Story:** As a maintainer, I want everything M18 does not name to behave
as before, so that the hardening is the only visible change.
#### Acceptance Criteria
1. THE system SHALL leave every status, header and body of the built-in golden files under `cmd/grimoire/testdata/builtin_golden/` byte-identical, with and without a `content:` registry.
2. IF a golden file changes, THEN THE change SHALL be a separate commit that states the cause and is reviewed before the PR merges; a golden file SHALL NOT be regenerated to make a failing test pass.
3. THE system SHALL leave the signatures of `CanCreatePost`, `CanEditPost`, `CanDeletePost` and `NewPostWriteService`, `NewTermWriteService`, `NewRevisionWriteService`, `NewAutosaveService` and `NewPostTermsWriteService` compatible, adding only options.
4. THE system SHALL leave `post` write behavior unchanged except for the delete cleanup of Requirements 7-10.
5. THE system SHALL apply M18 rules to rows and terms only at write time and SHALL NOT rewrite stored data.
### Requirement 16: No schema change; one additive port
**User Story:** As a maintainer, I want M18 to add no migration and no capability.
#### Acceptance Criteria
1. THE system SHALL add no migration, column, table or index.
2. THE system SHALL add no capability name and no dependency.
3. THE system SHALL add no method to an existing storage port, and PR 2 SHALL add exactly one new port, `PostDependentsPurger`.
4. PR 1 SHALL add no storage port.
5. THE system SHALL add no `GRIMOIRE_TEST_*` environment variable.
### Requirement 17: Wiring through the real assembly
**User Story:** As a maintainer, I want every M18 behavior reachable in the
shipped binary, so that M15's wiring omission cannot recur.
#### Acceptance Criteria
1. THE `buildHandler` function SHALL construct the revision and autosave services with the registry option and the post-terms service with the registry (already present), so the type gate runs in the shipped binary.
2. THE `buildHandler` function SHALL construct the post write service with the purger from PR 2 onward.
3. FOR EACH behavior in Requirements 1-14, a test SHALL drive it through `buildHandler`, not through a hand-built `web.Server`.
4. FOR EACH new `buildHandler` option, a task SHALL state that removing the option makes a named test fail, and SHALL record that the removal was tried.
5. THE term name behavior of Requirements 5 and 6 SHALL need no new option, because `buildHandler` already passes the shared policy; the `buildHandler` test SHALL still fail if the service change is reverted.
### Requirement 18: Tests and delivery
**User Story:** As a maintainer, I want the milestone delivered as two complete,
verified increments.
#### Acceptance Criteria
1. THE system SHALL have a RED test task before each implementation task, with the failure reason stated.
2. THE system SHALL run `gofmt -l .`, `go vet ./...`, `go build ./...` and `go test ./...` green after every phase.
3. THE system SHALL cover: the type gate for every stored type listed in the Glossary, on every service and route in Requirements 1-4; REST by-id 404s; name reduction for every term write path and the unchanged-name pass-through; delete cleanup for `post`, `page` and a registered type including the failure-ordering cases; the purger contract; the page parent guard branches (top-level, self, descendant, absent, other type, unchanged-not-validated, dangling stored parent); and the moved-page redirects.
4. PR 1 SHALL be mergeable without PR 2, and PR 2 SHALL be mergeable on top of PR 1 only.
5. EACH PR SHALL end with a final gate task that runs the full checks and the `git` checks that prove Requirement 16.
### Requirement 19: Documentation
**User Story:** As an operator and as a reader of the plans, I want the documents
to say what shipped and no longer list closed gaps as open.
#### Acceptance Criteria
1. WHEN PR 1 merges, THE `docs/compatibility.md` M17 "Known gaps" and "Sanitization" bullets SHALL describe the shipped type gate and built-in term sanitization.
2. WHEN PR 2 merges, THE `docs/compatibility.md` "Parent" and "Delete" bullets SHALL describe page parent writes and the delete cleanup.
3. EACH PR SHALL remove from `plans/post-m10-gap-analysis/README.md` ("Still deferred after M17") and from the deferred note under roadmap task 12.C in `plans/wordpress-core-parity-roadmap/tasks.md` exactly the entries it closes.
4. THE `plans/README.md` SHALL gain an `18-write-hardening` row, marked partially shipped after PR 1 and implemented after PR 2.
5. THE documents SHALL record what remains open: trash and `?force`, `sticky_posts` cleanup, nav-menu items that reference a deleted post, term-side unwritable taxonomies, and the WordPress object-cache and action-hook limits of a shared database.
## Out of scope
- **Unwritable taxonomies on term update and delete.** `PUT`/`DELETE /admin/api/terms/{id}` take the taxonomy from the stored term and `TermWriteService.Delete` checks none, so an imported `nav_menu`, `link_category` or unregistered term is editable by a `manage_categories` holder. It is the term-side twin of item 1. It is recorded as a sign-off candidate in `design.md`, not built here.
- **Trash, `?force`, undelete.** Delete stays permanent (D10).
- **`sticky_posts` cleanup and nav-menu items that point at a deleted post.** `wp_delete_post` also unsticks the post and deletes menu items whose `_menu_item_object_id` is the post. Both need option or menu writes outside the post service and are recorded as gaps.
- **Deleting attachments' files or comments through `MediaWriteService`.** The media delete path is separate and unchanged.
- **WordPress hooks and object cache.** grimoire does not fire `delete_post`, `deleted_post_meta` or `wp_delete_comment` actions, and a live WordPress object cache can hold stale entries until it expires. This holds for every grimoire write.
- **`parent` on `post`.** WordPress allows it; grimoire keeps ignoring it.
- **Page slug charset and uniqueness on writes that do not change the parent.**
- **Re-parenting of terms, menu items or comments.**
- **Admin SPA changes.** The UI is unchanged; M18 is API and storage only.
## Traceability
| Item | Requirements |
|---|---|
| 1 Unregistered stored types | 1, 2, 3, 4, 15, 17 |
| 3 Built-in term name sanitization | 5, 6, 15, 17 |
| 2 Built-in delete cleanup | 7, 8, 9, 10, 16, 17 |
| 4 Page `parent` writes | 11, 12, 13, 14, 17 |
| Delivery, tests, docs | 15-19 |
| M17 Finding 5 | 1, 2, 3 |
| M17 Finding 7 | 5, 6 |
| M17 Finding 4 | 7, 8, 9, 10 |
| M17 Finding 3 | 11, 12, 13, 14 |
