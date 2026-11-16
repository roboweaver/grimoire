# Requirements Document
M17 — Custom post type and taxonomy write (roadmap 12.C)
## Introduction
M12 (`../12-custom-types-read`, PRs #75/#76/#77) let an operator declare custom
post types (CPTs) and custom taxonomies (CTs) in the `content:` configuration
section and serve them **read-only**: public routes plus `GET
/wp-json/wp/v2/{rest_base}`. Every write verb on those REST routes still answers
`501` through `restNotImplemented`
(`internal/web/rest_custom_posts.go:22-27`, `internal/web/rest_custom_terms.go:23-25`),
and the admin API still rejects any post `type` other than `post` and `page`
(`internal/web/adminapi_posts.go:102-104`). M17 is roadmap task **12.C**, the
write-parity half M12 deliberately deferred.

Most of the write chain already exists and is parameterized by type or
taxonomy, which is why M17 is mostly wiring and guards rather than new
machinery:

- `handleRESTPostCreate/Update/Delete(typ)` (`internal/web/rest_posts.go:349,398,471`)
  and `handleRESTTermCreate/Update/Delete(taxonomy)`
  (`internal/web/rest_terms.go:254,305,359`) take the type or taxonomy as an
  argument. The REST term handlers already carry M13's `parent` field.
- `content.PostWriteService` (`internal/content/writeservices.go:55`) sanitizes
  title, content and excerpt by the actor's tier for any `Post.Type`
  (`sanitizeIncoming`, `:170`). `content.TermWriteService` (`:315`) already
  validates a `parent` (`validateParent`, `:351`).
- `PostRepo.Create`/`Update` already write `post_type` and `post_parent`
  (`internal/storage/wprepo/writers.go:54-59,95`), and `PostTermsRepo.SetPostTerms`
  already maintains `term_taxonomy.count` (`internal/storage/wprepo/postterms.go`).

Five things in the current code are **not** type-aware and are what M17 changes,
for registered types only:

1. **Capability suffixes.** `auth.CanCreatePost/CanEditPost/CanDeletePost` map
   every non-page type onto the `*_posts` family (`capSuffix`,
   `internal/auth/policy.go:11-16`). `auth.CapabilitySuffix`
   (`policy.go:21`) exists (M12) but only the REST read gate uses it
   (`internal/web/rest_posts.go:208-216`).
2. **Hierarchy validation.** `TermWriteService.validateParent` treats only
   `category` as hierarchical (`writeservices.go:358`). No write path sets
   `post_parent` for any post type: `PostWriteService.Update` keeps
   `cur.ParentID` (`writeservices.go:236-272`), and neither REST nor admin
   request bodies carry a parent.
3. **Taxonomy and type vocabularies.** The admin term create accepts any
   `taxonomy` string (`adminapi_terms.go:86`), `isKnownAdminTaxonomy` is the
   hardcoded `category`/`post_tag` pair (`adminapi_posts.go:25-44`),
   `applyTermIDs` accepts any taxonomy key (`:212-226`), and
   `PostTermsWriteService.SetPostTerms` validates neither taxonomy nor
   attachment (`internal/content/postterms.go:35-51`).
4. **Post delete leaves term rows behind.** `PostRepo.Delete` removes only the
   `{prefix}posts` row (`writers.go:104-114`); `term_relationships` rows and
   `term_taxonomy.count` are left stale.
5. **Assembly.** `buildHandler` (`cmd/grimoire/assembly.go:48-148`) constructs
   `NewPostWriteService`, `NewTermWriteService` and `NewPostTermsWriteService`
   with no registry. The M15 lesson (PR #74) applies: a service option that is
   not set in `buildHandler` does not exist in the shipped binary, however well
   its own tests pass. Every PR here therefore ships its assembly edit and a
   `buildHandler`-driven test.

M17 refines roadmap **Requirement 23.4** ("gate every custom-post-type read and
write on the capability model derived from that type's registry entry") and the
write halves of **Requirements 23 and 24**
(`../wordpress-core-parity-roadmap/requirements.md`, "M12 — Custom Post Types &
Taxonomies Requirements"), and roadmap task **12.C**
(`../wordpress-core-parity-roadmap/tasks.md`, "M12" group). It follows the
M10.B/M10.C/M10.F write-group pattern, reuses M10a's write-boundary sanitization
policy, and reuses M13's term-parent guard and M15's page-graph walk. It is
delivered as four reviewable pull requests (see `tasks.md`).
## Glossary
- **Registered type / registered taxonomy** — a `registry.PostType` or
  `registry.Taxonomy` (`internal/registry/types.go`) built from the `content:`
  section. `built-in` means `post`, `page`, `category` and `post_tag`.
- **Custom type write surface** — `POST /wp-json/wp/v2/{rest_base}`,
  `PUT`/`PATCH`/`DELETE /wp-json/wp/v2/{rest_base}/{id}` for a registered post
  type, the same verbs for a registered taxonomy, `POST /admin/api/posts`,
  `PUT`/`DELETE /admin/api/posts/{id}` with a registered `type`, and
  `POST`/`PUT /admin/api/terms` with a registered `taxonomy`.
- **Capability suffix** — `auth.CapabilitySuffix(capability_type)`: the type plus
  `s` (`"posts"` for the empty type). The type's capability family is
  `edit_<suffix>`, `edit_others_<suffix>`, `edit_published_<suffix>`,
  `edit_private_<suffix>`, `publish_<suffix>`, `delete_<suffix>`,
  `delete_others_<suffix>`, `delete_published_<suffix>`,
  `delete_private_<suffix>`.
- **Legacy suffix** — the suffix `auth.capSuffix` returns today: `"pages"` for
  `page`, `"posts"` for every other type. An **unregistered** type name found on
  a stored row keeps the legacy suffix.
- **Hierarchical type** — a registered post type with `Hierarchical == true`;
  its rows form a graph over `post_parent`, loaded as a `content.PageHierarchy`
  through `domain.PostNodeReader` (M12).
- **Post parent cycle** — a `post_parent` assignment that makes a post its own
  ancestor, directly or through a descendant.
- **Sibling set** — for a hierarchical object, the rows of the same type or
  taxonomy sharing one parent; for a flat object, every row of that type or
  taxonomy.
- **Write boundary** — the `content` service method that receives an actor and a
  value, and is the only place M10a's `sanitize.Policy` runs.
- **Tier** — one of M10a's three allow-list tiers (A, B, C), chosen from
  `sanitize.For(actor)`; tier C is the byte-identical bypass for
  `unfiltered_html` holders.
- **Legacy-allowed set** — the behavior of the built-in post/page and
  category/tag write paths before M17, which M17 does not change.
## Inherited decisions
Settled by the spec author on the owner's brief and treated as fixed input for
the requirements below. The ones the owner is asked to confirm are listed at the
end of the chat summary that accompanied this spec and again in `design.md`
"Decisions awaiting sign-off".
| # | Decision | Consequence |
|---|---|---|
| D1 | **Reuse the parameterized handlers and services; add no new route shape.** The REST custom routes register the existing `handleRESTPost*(typ)` and `handleRESTTerm*(taxonomy)` handlers for the verbs that return `501` today. The `501` stubs that built-in `post`/`page` keep for `POST /{id}` and for collection-level `PUT`/`PATCH`/`DELETE` are kept for custom routes too. | Requirements 1, 2, 6. |
| D2 | **Capabilities derive from the registry entry.** For a registered post type, create/edit/delete decisions use `auth.CapabilitySuffix(pt.CapabilityType)` through new suffix-taking policy functions; the existing `CanCreatePost/CanEditPost/CanDeletePost` keep their signatures and results and delegate to them with the legacy suffix. Taxonomy writes stay on `manage_categories` (the registry carries no taxonomy capability). No new capability name. | Requirement 7. |
| D3 | **Registry reaches services through functional options; nil means today.** `PostWriteService`, `TermWriteService` and `PostTermsWriteService` gain options that carry the `*registry.Registry`. A service built without them behaves exactly as before. | Requirements 7, 15, 17. |
| D4 | **Sanitization is the existing policy, proven for custom types.** Post title, content and excerpt are already sanitized in `PostWriteService` for any type; M17 adds tests, not code, for that. Registered-taxonomy term names are reduced to plain text with `sanitize.PostTitle` through a new `WithTermContentPolicy` option; built-in category/tag names are not touched. | Requirement 8. |
| D5 | **Post slugs: derive, constrain, auto-suffix (registered types only).** An empty slug with a non-empty title is derived from the title; an explicit slug must match `^[a-z0-9][a-z0-9_-]*$`; a slug that collides in the sibling set gets `-2`, `-3`, … appended, WordPress's `wp_unique_post_slug` behavior and the precedent of `NavMenuWriteService`. | Requirement 9. |
| D6 | **Term slugs: constrain and reject duplicates (registered taxonomies only).** An explicit or derived term slug must match the same charset and be unique in its sibling set, otherwise the write is rejected with `400` (`term_exists` on REST). | Requirement 4. |
| D7 | **Hierarchical `post_parent` writes via a service guard.** A `parent` field is accepted on REST and admin bodies for registered post types only. `PostWriteService` validates it against the type's node graph (same type, not self, no cycle) with two new sentinels, only when the value changes, after the capability check. | Requirement 11. |
| D8 | **Custom taxonomy hierarchy reuses M13.** `validateParent` treats a registered taxonomy as hierarchical when its registry entry says so and rejects any non-zero parent on a flat one. | Requirement 3. |
| D9 | **Unknown taxonomies are rejected on term create.** `TermWriteService.Create` returns a new `ErrTaxonomyUnknown` for a taxonomy that is neither built-in nor registered, mapped to `400`. | Requirement 4. |
| D10 | **Term assignment is limited to attached taxonomies for registered types.** REST bodies for registered post types accept term-id arrays keyed by each attached taxonomy's `rest_base`; admin bodies keep `termIds` keyed by taxonomy name. Every named taxonomy must be attached to the post's type, and every id must be a term of that taxonomy, validated before the post is written. Built-in `post`/`page` assignment is unchanged. | Requirement 12. |
| D11 | **Delete matches posts and pages, plus term cleanup for registered types.** Delete is permanent (no trash status exists; `?force` is ignored, as for posts today), returns REST `200 {deleted, previous}` and admin `204`, and does not re-parent children. For registered types only, the post's term relationships are detached first so `term_taxonomy.count` stays correct. | Requirement 13. |
| D12 | **`public: false` types are writable.** Public visibility governs routes, not the REST/admin write surface. A non-public type gets the same capability-gated writes and never gains a public route. | Requirement 14. |
| D13 | **The admin API keeps its `edit_posts` floor.** Admin routes stay behind `requireCapabilityJSON("edit_posts")` (`internal/web/adminroutes.go:84`), matching the M12 read side; the type-derived capability is enforced in the service on top. REST has no such floor. | Requirement 10. |
| D14 | **Scheduled publishing works for registered types.** The scheduler's internal principal receives the capability families of every registered suffix through a scheduler option, wired in `buildHandler`'s sibling `buildScheduler`. | Requirement 7. |
| D15 | **No cache work.** grimoire has no content or permalink cache; hierarchy is loaded per request from live columns. A slug or parent change takes effect on the next read, and M15's `PageMovedError` 301 covers a moved hierarchical post. M17 proves this rather than building invalidation. | Requirement 14. |
| D16 | **Tests: TDD, pure Go over SQLite, no new dependency, no property-based tests.** RED test task before every implementation task; each PR ends with a `buildHandler` test; the milestone ends with a built-binary smoke test. | Requirement 18. |
## Requirements
### Requirement 1: REST create for a registered taxonomy
**User Story:** As a content editor, I want to create a term in a custom taxonomy
through `POST /wp-json/wp/v2/{rest_base}`, so that custom term-driven content can
be managed over the same API as categories and tags.
#### Acceptance Criteria
1. WHEN a caller holding `manage_categories` sends `POST /wp-json/wp/v2/{rest_base}`
   for a registered taxonomy with a valid body, THE system SHALL create the term
   with `term_taxonomy.taxonomy` equal to the registry entry's `Name` and answer
   `201` with the same `restTerm` shape `GET` serves, including `taxonomy`,
   `parent` and a `link` built by `Structure.CustomTermPath`.
2. WHEN the request omits `slug`, THE system SHALL derive it from `name` with the
   existing `termSlugify`.
3. IF `name` is missing or blank, THEN THE system SHALL answer `400
   rest_invalid_param` and write nothing.
4. IF the caller lacks `manage_categories`, THEN THE system SHALL answer `403
   rest_cannot_create` and write nothing.
5. WHILE the request carries a session cookie rather than an Application
   Password, THE system SHALL require the `X-CSRF-Token` the existing term write
   handlers require.
6. WHEN `s.registry` or `s.termWrite` is `nil`, THE system SHALL register no
   custom term write route, exactly as `registerRESTCustomTerms` does for reads.
### Requirement 2: REST update and delete for a registered taxonomy
**User Story:** As a content editor, I want to rename, re-slug and delete custom
taxonomy terms over REST, so that I can maintain them without the admin UI.
#### Acceptance Criteria
1. WHEN a caller holding `manage_categories` sends `PUT` or `PATCH
   /wp-json/wp/v2/{rest_base}/{id}` for a term of that taxonomy, THE system SHALL
   apply the supplied `name`, `slug` and `parent` and leave omitted fields
   unchanged, with M13's omitted-versus-explicit-zero `parent` semantics.
2. WHEN a caller holding `manage_categories` sends `DELETE
   /wp-json/wp/v2/{rest_base}/{id}`, THE system SHALL delete the term, detach it
   from every post, and answer `200` with `{"deleted": true, "previous": <term>}`.
3. IF `{id}` names no term, or a term of a different taxonomy, THEN THE system
   SHALL answer `404 rest_term_invalid_id` before evaluating the capability, as
   the category and tag handlers do.
4. IF the caller lacks `manage_categories`, THEN THE system SHALL answer `403
   rest_cannot_edit`.
5. THE system SHALL keep `POST /{rest_base}/{id}` and collection-level
   `PUT`/`PATCH`/`DELETE` answering `501`, as for categories and tags today.
6. THE REST route index (`restRouteIndex`) SHALL advertise `POST` on each
   registered taxonomy collection and `GET`, `PUT`, `PATCH`, `DELETE` on each
   single route.
### Requirement 3: Custom taxonomy hierarchy writes
**User Story:** As a content editor, I want hierarchical custom taxonomies to
accept a parent, so that nested custom term archives can be built from the API.
#### Acceptance Criteria
1. WHEN a write to a hierarchical registered taxonomy supplies a non-zero
   `parent` naming a term of the same taxonomy, THE system SHALL store it in
   `term_taxonomy.parent` through the existing `TermRepo.Create`/`Update`.
2. IF a write to a flat registered taxonomy supplies a non-zero `parent`, THEN
   THE system SHALL reject it with `ErrTermParentInvalid` (`400`) and write
   nothing.
3. IF a write supplies a `parent` that is the term itself or one of its
   descendants, THEN THE system SHALL reject it with `ErrTermParentCycle`
   (`400`) and write nothing, using `TermHierarchy.DescendantIDs`.
4. IF a write supplies a `parent` that names no term of the same taxonomy,
   including a term of another taxonomy, THEN THE system SHALL reject it with
   `ErrTermParentInvalid` and write nothing.
5. THE system SHALL read the term hierarchy for the validation from the
   term's own taxonomy through `LoadTermHierarchy`, never from a taxonomy named
   in the request.
6. WHEN a term of a hierarchical registered taxonomy is re-parented, THE system
   SHALL serve its archive and `link` from the new ancestry on the next read.
### Requirement 4: Term slug uniqueness, name sanitization and taxonomy vocabulary
**User Story:** As a site owner, I want custom term names and slugs validated at
the write boundary, so that custom taxonomies cannot hold unsafe names, duplicate
slugs or terms in taxonomies that do not exist.
#### Acceptance Criteria
1. WHEN a term is created or updated in a registered taxonomy, THE system SHALL
   reduce the supplied `name` to plain text with M10a's `sanitize.PostTitle`
   field kind for the actor's tier before writing.
2. IF the sanitized `name` is empty, THEN THE system SHALL reject the write with
   `ErrTermNameEmpty` (`400`) and write nothing.
3. IF a supplied or derived `slug` does not match `^[a-z0-9][a-z0-9_-]*$`, THEN
   THE system SHALL reject the write with `ErrTermSlugInvalid` (`400`).
4. IF a registered taxonomy already holds a different term with the same `slug`
   in the new term's sibling set, THEN THE system SHALL reject the write with
   `ErrTermSlugTaken` (`400`, REST code `term_exists`) and write nothing.
5. WHEN an update leaves a term's own `slug` unchanged, THE system SHALL NOT
   treat the term as colliding with itself.
6. IF `TermWriteService.Create` receives a taxonomy that is neither built-in nor
   registered, THEN THE system SHALL return `ErrTaxonomyUnknown` and write
   nothing; the REST and admin handlers SHALL map it to `400`.
7. THE system SHALL leave `name` and `slug` handling for `category` and
   `post_tag` writes exactly as before.
### Requirement 5: Admin-API term writes for registered taxonomies
**User Story:** As an administrator using the admin API, I want to create and
update terms in custom taxonomies, so that the SPA and scripts can manage them.
#### Acceptance Criteria
1. WHEN `POST /admin/api/terms` names a registered `taxonomy`, THE system SHALL
   create the term under Requirements 3 and 4 and answer `201` with the existing
   `termDetailResponse`.
2. WHEN `PUT /admin/api/terms/{id}` targets a term of a registered taxonomy, THE
   system SHALL update it with the load-and-merge behavior M13 added, so the
   response `taxonomy` is the stored taxonomy.
3. WHEN `GET /admin/api/terms?taxonomy=` names a registered taxonomy, THE system
   SHALL list its terms; IF it names a taxonomy that is neither built-in nor
   registered, THEN THE system SHALL keep answering `400 unknown taxonomy`.
4. IF the caller lacks `manage_categories`, THEN THE system SHALL answer `403`
   and write nothing.
5. THE system SHALL keep the `manage_categories` and CSRF gates on the term
   write routes exactly as registered today.
### Requirement 6: REST create, update and delete for a registered post type
**User Story:** As a content editor, I want to create, update and delete custom
post type items through `/wp-json/wp/v2/{rest_base}`, so that a WordPress client
can write the custom content it can already read.
#### Acceptance Criteria
1. WHEN an authorized caller sends `POST /wp-json/wp/v2/{rest_base}` for a
   registered post type, THE system SHALL create a row with `post_type` equal to
   the registry entry's `Name` and answer `201` with the item shape `GET`
   serves, built by `mapRESTPost` with the type's `rest_base` links.
2. WHEN an authorized caller sends `PUT` or `PATCH
   /wp-json/wp/v2/{rest_base}/{id}`, THE system SHALL apply the supplied
   `title`, `content`, `excerpt`, `slug`, `status`, `date` and `comment_status`,
   leave omitted fields unchanged, and honor the optional `If-Unmodified-Since`
   header exactly as the post and page handlers do.
3. IF `{id}` names a row whose `post_type` differs from the route's type, or no
   row, THEN THE system SHALL answer `404 rest_post_invalid_id` before
   evaluating the capability.
4. THE system SHALL reuse the status vocabulary, title rule, date parsing and
   future-date rule of `parseRESTPostWrite`.
5. THE system SHALL keep `POST /{rest_base}/{id}` and collection-level
   `PUT`/`PATCH`/`DELETE` answering `501`, as for posts and pages today.
6. WHILE the request carries a session cookie rather than an Application
   Password, THE system SHALL require the `X-CSRF-Token`.
7. THE REST route index SHALL advertise `GET`, `POST` on each registered post
   type collection and `GET`, `PUT`, `PATCH`, `DELETE` on each single route.
### Requirement 7: Capability gating derived from the registry entry
**User Story:** As a site administrator, I want a custom type's writes gated by
its own capability family, so that a role that can edit posts is not
automatically able to edit a type whose `capability_type` I set to something else.
#### Acceptance Criteria
1. THE system SHALL gate create, update and delete of a registered post type on
   the capability family of `auth.CapabilitySuffix(pt.CapabilityType)`: `edit_`,
   `edit_others_`, `edit_published_`, `edit_private_`, `publish_`, `delete_`,
   `delete_others_`, `delete_published_`, `delete_private_`, with the same
   author, status and ownership rules `CanCreatePost`, `CanEditPost` and
   `CanDeletePost` apply to posts.
2. WHEN a registered type's `capability_type` is `post`, THE system SHALL produce
   the same decisions as for built-in `post`.
3. THE system SHALL evaluate the capability against the stored row's type on
   update and delete, never a type taken from the request.
4. IF a stored row's `post_type` is not registered and is not `post` or `page`,
   THEN THE system SHALL keep the legacy suffix for it, unchanged.
5. THE system SHALL NOT introduce a new capability name; a role holds a custom
   family only if the database's `capabilities` already grants it.
6. WHEN a registered type has a `future`-status row that comes due, THE
   scheduler SHALL publish it, using a system principal that carries the
   registered suffixes' capabilities.
7. IF a caller lacks the derived capability, THEN THE system SHALL answer `403`
   (`rest_cannot_create` on create, `rest_cannot_edit` on update and delete; the
   admin-JSON `403` on the admin API) and write nothing.
### Requirement 8: Write-boundary sanitization for registered types
**User Story:** As a site owner, I want custom post content sanitized the same way
as post content, so that adding a post type cannot open an unsanitized write path.
#### Acceptance Criteria
1. WHEN a caller without `unfiltered_html` writes a registered type's `title`,
   `content` or `excerpt` through REST or the admin API, THE system SHALL pass
   the value through M10a's policy at the actor's tier before it reaches
   `domain.PostWriter`.
2. WHEN a caller with `unfiltered_html` writes the same fields, THE system SHALL
   store them byte-identically (tier C).
3. WHEN a title sanitizes to empty, THE system SHALL answer `400` as the post and
   page handlers do (`ErrTitleEmpty`).
4. WHEN an update resubmits a field byte-identical to the stored value, THE
   system SHALL pass it through unsanitized (`sanitizeIncoming`'s finding 3), so
   an imported custom row is never rewritten by an unrelated PATCH.
5. THE system SHALL sanitize a registered taxonomy's term `name` under
   Requirement 4.1.
### Requirement 9: Post slug derivation and uniqueness for registered types
**User Story:** As a content editor, I want custom post slugs generated and kept
unique, so that two items never share one public URL.
#### Acceptance Criteria
1. WHEN a create or update for a registered type supplies an empty `slug` and a
   non-empty title, THE system SHALL derive the slug from the sanitized title.
2. IF a supplied `slug` does not match `^[a-z0-9][a-z0-9_-]*$`, THEN THE system
   SHALL answer `400` (`rest_invalid_param` on REST, `bad_request` on admin) and
   write nothing.
3. WHEN the slug collides with another row of the same type in the sibling set,
   in any status, THE system SHALL append `-2`, `-3`, … until it is unique.
4. WHEN an update leaves a row's own slug unchanged, THE system SHALL NOT treat
   the row as colliding with itself.
5. THE system SHALL leave slug handling for `post` and `page` exactly as before.
### Requirement 10: Admin-API post writes for registered types
**User Story:** As an administrator using the admin API, I want to create and
update custom type items, so that the SPA and scripts reach them.
#### Acceptance Criteria
1. WHEN `POST /admin/api/posts` or `PUT /admin/api/posts/{id}` carries `type` equal
   to a registered post type, THE system SHALL accept it and write through
   `PostWriteService` under Requirements 7 to 9.
2. IF `type` is neither `post`, `page` nor registered, THEN THE system SHALL
   answer `400`, keeping the existing wording when no registry is configured.
3. WHEN `DELETE /admin/api/posts/{id}` targets a registered type's row, THE system
   SHALL delete it under Requirement 13 and answer `204`.
4. THE response SHALL carry the stored `type`, and `terms` keyed by the type's
   attached taxonomies (Requirement 12.6).
5. THE system SHALL keep the `edit_posts` route gate and CSRF check on every
   admin post write route (D13).
6. WHEN `PUT` supplies a `type` that differs from the stored row's, THE system
   SHALL ignore it and keep the stored type, as today.
### Requirement 11: Hierarchical post_parent writes
**User Story:** As a content editor, I want hierarchical custom types to accept a
parent, so that nested custom URLs can be created through the API.
#### Acceptance Criteria
1. WHEN a create or update for a hierarchical registered type supplies `parent`
   naming a row of the same type, THE system SHALL store it in `post_parent`.
2. IF a write supplies a non-zero `parent` for a non-hierarchical registered
   type, THEN THE system SHALL reject it with `ErrPostParentInvalid` (`400`).
3. IF `parent` names no row, or a row of another type, THEN THE system SHALL
   reject it with `ErrPostParentInvalid`.
4. IF `parent` is the row itself or one of its descendants, THEN THE system SHALL
   reject it with `ErrPostParentCycle` (`400`), using the type's node graph.
5. WHEN `parent` is omitted on update, THE system SHALL leave `post_parent`
   unchanged; WHEN it is `0`, THE system SHALL make the row top-level. THE
   system SHALL distinguish the two with a `*int64`.
6. WHEN an update does not change `post_parent`, THE system SHALL NOT validate it,
   so a row with imported, inconsistent hierarchy stays editable.
7. THE system SHALL run the parent validation after the capability check, so an
   unauthorized caller receives `403` before any `400`.
8. THE REST item for a hierarchical registered type SHALL carry `parent`, and its
   `link` SHALL be built from the nested ancestry after create, update and delete.
9. THE system SHALL accept and honor `parent` for registered types only; a
   `parent` key on a `post` or `page` request SHALL stay ignored.
### Requirement 12: Term assignment to registered taxonomies only
**User Story:** As a content editor, I want to file a custom post under terms of
its custom taxonomies, and only those, so that a post cannot be attached to a
taxonomy that does not apply to it.
#### Acceptance Criteria
1. WHEN a REST create or update for a registered post type carries a key equal to
   the `rest_base` of a taxonomy attached to that type, with an array of term
   ids, THE system SHALL replace the post's terms in that taxonomy with exactly
   those ids and maintain `term_taxonomy.count`.
2. IF a key names a taxonomy attached to the type but the value is not an array
   of positive integers, THEN THE system SHALL answer `400 rest_invalid_param`
   and write nothing.
3. IF any id is not an existing term of that taxonomy, THEN THE system SHALL
   answer `400 rest_invalid_param` before the post is written.
4. WHEN an admin `termIds` map for a registered post type names a taxonomy not
   attached to the type, THE system SHALL report it in the response `partial`
   map and assign nothing for it, without failing the post write.
5. THE system SHALL authorize term assignment as an edit of the post under
   Requirement 7, not under `manage_categories`.
6. THE admin post detail for a registered type SHALL list `terms` for the type's
   effective attached taxonomies, and the REST item SHALL keep its `rest_base`
   keyed term-id arrays (M12).
7. THE system SHALL leave term assignment for built-in `post` and `page` exactly
   as before.
### Requirement 13: Delete semantics
**User Story:** As a content editor, I want deleting a custom item to behave like
deleting a post, without leaving stale term counts behind.
#### Acceptance Criteria
1. WHEN an authorized caller deletes a registered type's row over REST, THE
   system SHALL delete it permanently, answer `200` with `{"deleted": true,
   "previous": <item>}`, and ignore `?force`, as for posts and pages.
2. THE system SHALL delete the row's revisions before the row, as for posts.
3. WHEN a registered type's row is deleted, THE system SHALL first detach it from
   every attached taxonomy so each affected `term_taxonomy.count` is recomputed.
4. WHEN a hierarchical row with children is deleted, THE system SHALL leave the
   children's `post_parent` unchanged; the M15 orphan rule serves them as
   top-level.
5. THE system SHALL leave `post` and `page` deletion exactly as before.
### Requirement 14: Non-public types, permalinks and caching
**User Story:** As a site owner, I want private custom types writable but never
public, and URL changes to take effect immediately.
#### Acceptance Criteria
1. WHEN a registered type has `public: false`, THE system SHALL accept the same
   capability-gated REST and admin writes and SHALL NOT register or serve a
   public route for it.
2. WHEN a published row of a non-public type exists, THE system SHALL answer `404`
   for every URL that could resolve it publicly.
3. WHEN a slug or parent change alters a hierarchical row's canonical path, THE
   system SHALL serve the new path on the next read, and the previous nested
   path SHALL resolve through the M15 moved-page redirect.
4. THE system SHALL add no content, permalink or hierarchy cache.
### Requirement 15: Built-in behavior unchanged
**User Story:** As a maintainer, I want M17 provably not to change posts, pages,
categories and tags.
#### Acceptance Criteria
1. WHILE no `content:` section is configured, THE system SHALL produce the same
   status, headers and body for every post, page, category and tag write and
   read as before M17.
2. WHILE a `content:` section is configured, THE system SHALL produce the same
   results for built-in writes as before M17, except that the route index and
   `terms` response maps may list registered entries.
3. THE existing `CanCreatePost`, `CanEditPost`, `CanDeletePost` and
   `validateParent` signatures and built-in results SHALL be unchanged.
### Requirement 16: No schema change, additive ports only
**User Story:** As a maintainer, I want M17 to add no migration and no capability.
#### Acceptance Criteria
1. THE system SHALL add no migration, column, table or index.
2. THE system SHALL add no capability name and no dependency.
3. THE system SHALL add no method to an existing storage port; reads reuse
   `domain.PostNodeReader`, `domain.PostTermsWriter`, `domain.TermReader` and
   `domain.PostWriter`.
4. THE system SHALL add no `GRIMOIRE_TEST_*` environment variable.
### Requirement 17: Wiring through the real assembly
**User Story:** As a maintainer, I want every M17 behavior reachable in the shipped
binary, so that M15's wiring omission cannot recur.
#### Acceptance Criteria
1. THE `buildHandler` function SHALL construct the post, term and post-term write
   services with the registry options of D3 and the policy of D4.
2. THE system SHALL pass the registered suffixes to the scheduler through a
   function `main` calls and a test can call.
3. FOR EACH write behavior in Requirements 1 to 14, a test SHALL drive it through
   `buildHandler`, not through a hand-built `web.Server`.
4. A built-binary smoke test SHALL create, read, update and delete a custom item
   and term over HTTP against a SQLite database.
### Requirement 18: Test coverage and documentation
**User Story:** As a maintainer, I want the milestone verified and documented.
#### Acceptance Criteria
1. THE system SHALL have a RED test task before each implementation task and
   `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./...` green after
   every phase.
2. THE system SHALL cover capability matrices per verb for a custom
   `capability_type`, the cycle and invalid-parent branches, slug collision,
   sanitization tiers A/B/C for registered types, term attachment validation,
   delete count correction and the `public: false` cases.
3. THE system SHALL update `docs/compatibility.md`, tick roadmap task 12.C and the
   write portions of Requirements 23, 23.4 and 24, update the post-M10
   gap-analysis, and add the milestone to `plans/README.md`.
## Out of scope
- **Built-in changes.** Page `parent` writes, REST term assignment for built-in
  posts, trash status, `?force` semantics for posts, child re-parenting on delete,
  and `term_relationships` cleanup on built-in post delete. Each is a gap in the
  current code recorded in `design.md` Findings and proposed as follow-up work.
- **Fields beyond M6's body.** `author`, `menu_order`, `template`, `format`,
  `sticky`, `meta`, `featured_media`, `password` and `ping_status` on REST custom
  bodies.
- **Term `description`**, and term meta.
- **Taxonomy-specific capabilities** (`manage_terms`, `edit_terms`, `assign_terms`,
  `delete_terms`); the registry carries none.
- **`/wp/v2/types`, `/wp/v2/taxonomies`**, bulk operations, `POST /{id}` update,
  batch.
- **Admin SPA changes.** The UI is unchanged; M17 is API-only.
- **Executing PHP to register types**, permanently out of scope (Requirement 25.1).
## Traceability
| Roadmap | Covered by |
|---|---|
| 12.C "REST and admin-API create/update for registered custom post types and taxonomies" | 1, 2, 5, 6, 10 |
| 12.C "passed through M10a's sanitization policy at the write boundary" | 4.1, 8 |
| 12.C "following the M10.B/M10.C/M10.F write-group pattern" | D1, D3, 17 |
| Requirement 23.4 write half | 7 |
| Requirement 24 write half (24.2 hierarchy, 24.4 parent) | 3, 5 |
| Requirement 23.2/24.2 permalink machinery on written data | 14 |
