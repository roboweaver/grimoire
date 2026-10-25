# Requirements Document

M11 — REST delete parity (media + users)

## Introduction

M10.B (spec `../10.1-rest-media-write`) and M10.C (spec `../10.2-rest-user-write`)
turned on REST *create* and *update* for attachments and users: a
capability-checked `content.MediaWriteService`
(`internal/content/mediawriteservice.go`) and the write-capable
`content.UserService` (`internal/content/userservice.go`), nil-gated into the web
layer, each evaluating every capability decision against the stored row and
routing free-text through M10a's Policy. Those milestones deliberately left
`DELETE` behind: `DELETE /media/{id}` is still a `501` stub
(`r.Method(http.MethodDelete, "/media/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting media"))`,
`internal/web/rest_media.go:51`), and `DELETE /users/{id}` is likewise a `501`
stub (`r.Method(http.MethodDelete, "/users/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting a user"))`,
`internal/web/rest_users.go:50`). `DELETE` is the one remaining deferred REST
write verb for both endpoints.

This milestone is the first beyond the completed WordPress-core-parity roadmap
(M1–M10) and the first entry of the post-M10 gap analysis
(`../post-m10-gap-analysis/README.md`, "M11 — REST delete parity (media + users)",
flagged there as the "Smallest, most self-contained candidate — natural immediate
next step"). It closes that last write-verb gap by replacing exactly the two
`DELETE /{resource}/{id}` stubs above with real handlers, over two new domain
`Delete` ports, mirroring the existing `PostWriter.Delete(ctx, id)` precedent
(`internal/domain/repository.go:440`).

Delete follows WordPress's own force/trash contract. WordPress trashes most
content by default but **does not trash attachments or users**: both require
`?force=true` and are hard-deleted. The media hard delete removes the
attachment's `{prefix}posts` row, its `{prefix}postmeta`, its
`{prefix}term_relationships`, **and the file on disk** — the
`_wp_attached_file` path under `MediaConfig.UploadsDir`
(`internal/content/media.go:18`, joined at `internal/content/media.go:84`). The
on-disk unlink is an intentional full-WordPress-parity choice, gated behind
`force=true`, not an optional extra. The user hard delete removes the
`{prefix}users` row and its `{prefix}usermeta`, and first applies the required
`reassign` parameter to the user's authored content, all in one transaction. No
migration is required (every target table already exists), and no new capability
name is introduced.

## Glossary

- **`force`** — the WordPress REST query parameter that opts into a permanent
  (non-trash) delete. For attachments and users WordPress supports no trash, so
  `force=true` is mandatory; its absence is an error, not a trash.
- **trash-not-supported** — the WordPress response, `400` with code
  `rest_trash_not_supported`, returned when a `DELETE` is requested without
  `force=true` for a resource type that has no trash lifecycle (attachments and
  users).
- **The delete envelope** — the JSON body WordPress returns on a successful
  force delete: `{"deleted": true, "previous": <object>}`, where `previous` is
  the full resource representation that a `GET` of the same id would have
  returned immediately before deletion (for media, the `mapRESTMedia` envelope;
  for users, the edit-context `mapRESTUser` envelope).
- **`reassign`** — the WordPress query parameter on `DELETE /users/{id}` that
  decides the fate of the deleted user's authored content: either a valid
  existing user ID (reassign authored `{prefix}posts` rows to that user) or
  `reassign=false` (delete the user's authored content). It is required; a
  missing or unresolvable value is an error.
- **Per-attachment ownership (`post_author`)** — the media capability model:
  deleting an attachment is gated on the attachment's stored `post_author`
  column, the same stored-row ownership media *update* uses
  (`auth.CanEditPost(actor, "post", "inherit", cur.AuthorID)`,
  `internal/content/mediawriteservice.go`). Delete mirrors it with
  `auth.CanDeletePost` (`internal/auth/policy.go:59`): `delete_posts` for the
  caller's own attachment, `delete_others_posts` for another user's.
- **`delete_users`** — the WordPress user-deletion capability, administrator-only
  by default (`internal/auth/roles.go:134`), resolved through
  `auth.CanDeleteUsers(actor)` (`internal/auth/policy.go:98`).
- **`MediaWriter.Delete`** — the new domain port method
  `Delete(ctx, id int64) error` on the `domain.MediaWriter` interface
  (`internal/domain/repository.go:223`), added alongside the existing
  `Create`/`SetParent`/`UpdateMeta`. It hard-deletes the attachment's row,
  postmeta, and term relationships.
- **`UserRepository.Delete`** — the new domain port method
  `Delete(ctx, id int64, reassignTo int64) error` on the
  `domain.UserRepository` interface (`internal/domain/repository.go:326`), added
  alongside the existing `ByLogin`/`ByNicename`/`ByID`/`Create`/`UpdatePass`/
  `UpdateProfile`/`ByEmail`. `reassignTo == 0` means "delete the user's authored
  content"; a positive id means "reassign it to that user".
- **The media file unlink** — removing the on-disk file backing an attachment on
  hard delete: the relative path in the attachment's `_wp_attached_file`
  postmeta resolved under `MediaConfig.UploadsDir`
  (`internal/content/media.go:18`, `:84`). It is part of the media hard delete,
  not a separate operation.
- **The REST deletes** — `DELETE /media/{id}` and `DELETE /users/{id}`, the only
  two surfaces this milestone enables.

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | **New domain `Delete` methods.** Add `MediaWriter.Delete(ctx, id int64) error` (`internal/domain/repository.go:223`) and `UserRepository.Delete(ctx, id int64, reassignTo int64) error` (`internal/domain/repository.go:326`); `reassignTo == 0` means "delete the user's authored content", a positive id means "reassign to that user". `wprepo` implements both, mirroring the existing `PostWriter.Delete` pattern (`internal/domain/repository.go:440`). The read ports are unchanged. | Two additive writer ports plus their `wprepo` implementations; no read-port change (Requirement 10). |
| D2 | **Media delete is a hard delete with WordPress-faithful `force`.** WordPress does not trash attachments: `DELETE /media/{id}` requires `?force=true`; without it respond `400 rest_trash_not_supported`. The hard delete removes the attachment's `{prefix}posts` row, its `{prefix}postmeta`, any `{prefix}term_relationships`, and **the on-disk file** (the `_wp_attached_file` path under `UploadsDir`). On success respond `200` with `{"deleted": true, "previous": <the attachment object GET would have returned>}`. | Requirements 1, 2, 4. The file unlink is mandatory, not optional. |
| D3 | **Media delete capability is per-attachment ownership.** Gate on `auth.CanDeletePost(actor, "post", "inherit", storedAuthorID)` (`internal/auth/policy.go:59`) — the same stored-`post_author` ownership media update uses (`delete_posts` for own, `delete_others_posts` for others'). Forbidden → `403 rest_cannot_delete`. | Requirement 3. Capability is read from the stored row, never the request. |
| D4 | **User delete requires `force=true` + `reassign`.** WordPress has no user trash: `DELETE /users/{id}` requires `?force=true` (else `400 rest_trash_not_supported`) and a `reassign` parameter — a valid existing user ID, or `reassign=false` to delete the user's content. Missing or invalid `reassign` (unparseable, or an id that does not resolve to an existing user) → `400 rest_user_invalid_reassign`. On success respond `200` with `{"deleted": true, "previous": <the user object GET /users/{id} returns in edit context>}`. | Requirements 5, 6, 7. |
| D5 | **User delete capability is `delete_users`, no self-delete.** Gate on `auth.CanDeleteUsers(actor)` (`internal/auth/policy.go:98`; administrator-only by default, `internal/auth/roles.go:134`). A caller deleting their own record → `403 rest_user_cannot_delete`. A reassign target that is an id must resolve to an existing user (else the `rest_user_invalid_reassign` 400). | Requirement 8. |
| D6 | **Reassign semantics (the destructive branch).** When `reassign=<id>`, the deleted user's authored `{prefix}posts` rows get `post_author` set to that id BEFORE the user row and its `{prefix}usermeta` are removed; when `reassign=false` (reassignTo == 0), the user's authored posts are deleted. The storage layer performs this in a single transaction so a half-reassigned/half-deleted state cannot persist. | Requirement 9. The transaction boundary lives in `wprepo`. |
| D7 | **Auth/CSRF parity with the existing create/update handlers.** Same pattern: an Application Password skips CSRF (`isAppPasswordAuth`); a session enforces the M4 `X-CSRF-Token` contract (`requireSessionCSRFREST`). No new capability names are introduced; no `wp-json` route beyond the two `DELETE` verbs is added. | Requirement 11. |
| D8 | **Tests: capability matrix + behavior, pure Go/SQLite, no new dependency.** Capability-matrix tests (allowed vs forbidden) per route; `force`-required → 400; media per-attachment ownership; user `reassign` required/invalid → 400; self-delete → 403; the `{"deleted":true,"previous":...}` envelope shape; and storage round-trip. SQLite-only, no new env var or dependency; `gofmt`/`go vet`/`go build ./...`/`go test ./...` green at the end of every task. | Requirement 12. |

## Requirements

### Requirement 1: Delete an attachment via DELETE /media/{id}

**User Story:** As an integrator using WordPress REST tooling, I want to delete an
attachment with `DELETE /wp-json/wp/v2/media/{id}?force=true`, so that existing
WordPress media clients can remove media against grimoire unchanged.

#### Acceptance Criteria

1. WHEN a caller authorized per Requirement 3 sends `DELETE /media/{id}` with
   `force=true` for an existing attachment, THE REST media endpoint SHALL hard
   delete that attachment and SHALL respond `200`, replacing the
   `restNotImplemented` `501` stub for `DELETE /media/{id}`
   (`internal/web/rest_media.go:51`).
2. WHEN a `DELETE /media/{id}` force delete succeeds, THE REST media endpoint
   SHALL respond with the delete envelope `{"deleted": true, "previous": <object>}`,
   where `previous` is the exact `mapRESTMedia` envelope a `GET /media/{id}`
   would have returned for that attachment immediately before deletion.
3. THE REST media endpoint SHALL compute the `previous` object by loading and
   mapping the stored attachment BEFORE the delete executes, so the response body
   reflects the attachment as it existed, not an empty or post-delete read.
4. IF the `{id}` path segment does not resolve to an existing attachment, THEN
   THE REST media endpoint SHALL respond `404` with code `rest_post_invalid_id`
   and SHALL NOT delete anything, matching `handleRESTMediaSingle`.

### Requirement 2: Media delete requires force=true (no trash)

**User Story:** As a maintainer, I want media delete to require `force=true`
exactly as WordPress does, so that clients cannot silently rely on a trash
behavior grimoire does not implement.

#### Acceptance Criteria

1. IF a `DELETE /media/{id}` request omits `force=true` (the parameter is absent,
   empty, or any value other than `true`/`1`), THEN THE REST media endpoint SHALL
   respond `400` with code `rest_trash_not_supported` and SHALL NOT delete the
   attachment, its postmeta, its term relationships, or its on-disk file.
2. WHEN `force=true` is present, THE REST media endpoint SHALL proceed to the
   capability gate (Requirement 3) and, if authorized, the hard delete
   (Requirement 4).
3. THE REST media endpoint SHALL evaluate `force` from the request query string
   only and SHALL NOT read it from the request body, because `DELETE` carries no
   JSON body on this route.

### Requirement 3: Media delete capability is per-attachment ownership

**User Story:** As a site administrator, I want attachment deletion gated by the
same per-attachment ownership WordPress uses, so that a user can delete only the
media they are allowed to.

#### Acceptance Criteria

1. WHEN a caller sends `DELETE /media/{id}?force=true` for an attachment whose
   stored `post_author` equals the caller's user ID, THE Media_Write_Service
   SHALL require `delete_posts` and SHALL respond `403` with code
   `rest_cannot_delete` if the caller lacks it.
2. WHEN a caller sends `DELETE /media/{id}?force=true` for an attachment whose
   stored `post_author` differs from the caller's user ID, THE
   Media_Write_Service SHALL require `delete_others_posts` and SHALL respond
   `403` with code `rest_cannot_delete` if the caller lacks it.
3. THE Media_Write_Service SHALL evaluate the capability decision through
   `auth.CanDeletePost(actor, "post", "inherit", storedAuthorID)`
   (`internal/auth/policy.go:59`), reading `post_author` from the stored
   attachment row loaded by ID, and SHALL NEVER use an author value taken from
   the request, mirroring the ownership check `MediaWriteService.Update`
   performs with `auth.CanEditPost` (`internal/content/mediawriteservice.go`).
4. IF an unauthenticated caller (no session, no Application Password) sends
   `DELETE /media/{id}?force=true`, THEN THE REST media endpoint SHALL respond
   `403` with code `rest_cannot_delete` and SHALL NOT delete anything, matching
   the forbidden-write convention of the create/update handlers.

### Requirement 4: Media hard delete removes file, row, postmeta, and relationships

**User Story:** As a security reviewer, I want a media hard delete to leave no
orphaned row, meta, relationship, or file, so that a deleted attachment is fully
gone exactly as in WordPress.

#### Acceptance Criteria

1. WHEN a media hard delete executes, THE storage layer SHALL remove the
   attachment's `{prefix}posts` row, every `{prefix}postmeta` row for that
   attachment, and every `{prefix}term_relationships` row for that attachment,
   through the new `MediaWriter.Delete(ctx, id)` port (D1).
2. WHEN a media hard delete executes, THE Media_Write_Service SHALL unlink the
   on-disk file backing the attachment — the relative path stored in the
   attachment's `_wp_attached_file` postmeta, resolved under
   `MediaConfig.UploadsDir` (`internal/content/media.go:18`, `:84`) — using the
   uploads directory owned by the `*MediaService` the Media_Write_Service holds.
3. IF the on-disk file is already absent when the delete runs, THEN THE
   Media_Write_Service SHALL treat the missing file as a non-fatal condition and
   SHALL still complete the row/meta/relationship removal and respond `200`, so a
   pre-removed file does not block deletion.
4. WHEN `MediaWriter.Delete` is asked to delete an id that no attachment has, THE
   new port SHALL return `domain.ErrNotFound`, matching the `UpdateMeta`/
   `SetParent` contract and the existing `PostWriter.Delete` contract
   (`internal/domain/repository.go:440`).
5. THE media hard delete SHALL remove the database rows and the on-disk file such
   that a subsequent `GET /media/{id}` returns `404` and no `{prefix}postmeta` or
   `{prefix}term_relationships` row for the deleted id remains (verified by the
   Requirement 12 storage round-trip test).

### Requirement 5: Delete a user via DELETE /users/{id}

**User Story:** As a site administrator using WordPress REST tooling, I want to
delete a user with `DELETE /wp-json/wp/v2/users/{id}?force=true&reassign=<id|false>`,
so that existing WordPress user-management clients can remove users against
grimoire unchanged.

#### Acceptance Criteria

1. WHEN a caller authorized per Requirement 8 sends `DELETE /users/{id}` with
   `force=true` and a valid `reassign` for an existing user, THE REST users
   endpoint SHALL delete that user and SHALL respond `200`, replacing the
   `restNotImplemented` `501` stub for `DELETE /users/{id}`
   (`internal/web/rest_users.go:50`).
2. WHEN a `DELETE /users/{id}` delete succeeds, THE REST users endpoint SHALL
   respond with the delete envelope `{"deleted": true, "previous": <object>}`,
   where `previous` is the exact edit-context `mapRESTUser` envelope a
   `GET /users/{id}` would have returned in edit context immediately before
   deletion.
3. THE REST users endpoint SHALL compute the `previous` object by loading and
   mapping the stored user BEFORE the delete executes, so the response body
   reflects the user as it existed.
4. IF the `{id}` path segment does not resolve to a positive integer or to an
   existing user, THEN THE REST users endpoint SHALL respond `404` with code
   `rest_user_invalid_id` and SHALL NOT delete anything, matching
   `handleRESTUserSingle`.

### Requirement 6: User delete requires force=true (no trash)

**User Story:** As a maintainer, I want user delete to require `force=true`
exactly as WordPress does, so that clients cannot rely on a user-trash behavior
grimoire does not implement.

#### Acceptance Criteria

1. IF a `DELETE /users/{id}` request omits `force=true` (the parameter is absent,
   empty, or any value other than `true`/`1`), THEN THE REST users endpoint SHALL
   respond `400` with code `rest_trash_not_supported` and SHALL NOT delete the
   user, its usermeta, or reassign or delete any authored content.
2. WHEN `force=true` is present, THE REST users endpoint SHALL proceed to the
   `reassign` validation (Requirement 7) and the capability gate
   (Requirement 8).

### Requirement 7: User delete requires a valid reassign

**User Story:** As a site administrator, I want to decide what happens to a
deleted user's content through `reassign`, so that authored content is either
re-owned or removed deliberately, never orphaned.

#### Acceptance Criteria

1. IF a `DELETE /users/{id}?force=true` request omits `reassign`, THEN THE REST
   users endpoint SHALL respond `400` with code `rest_user_invalid_reassign` and
   SHALL NOT delete the user.
2. IF a `DELETE /users/{id}?force=true` request supplies a `reassign` value that
   is neither `false` nor a parseable positive integer, THEN THE REST users
   endpoint SHALL respond `400` with code `rest_user_invalid_reassign` and SHALL
   NOT delete the user.
3. IF a `DELETE /users/{id}?force=true` request supplies `reassign=<id>` for an
   id that does not resolve to an existing user, THEN THE REST users endpoint
   SHALL respond `400` with code `rest_user_invalid_reassign` and SHALL NOT
   delete the user.
4. WHEN a request supplies `reassign=false`, THE REST users endpoint SHALL map it
   to `reassignTo == 0` ("delete the user's authored content") on the
   `UserRepository.Delete(ctx, id, reassignTo)` port (D1, D6).
5. WHEN a request supplies `reassign=<id>` for an existing user, THE REST users
   endpoint SHALL pass that id as `reassignTo` to `UserRepository.Delete`, so the
   user's authored content is reassigned to that user (D6).

### Requirement 8: User delete capability is delete_users with no self-delete

**User Story:** As a site administrator, I want user deletion restricted to
administrators and never applicable to one's own record, so that the REST API
cannot be used to lock an account out of its own management.

#### Acceptance Criteria

1. WHEN a caller lacking `delete_users` sends `DELETE /users/{id}?force=true`,
   THE REST users endpoint SHALL respond `403` with code `rest_user_cannot_delete`
   and SHALL NOT delete anything, resolving the capability through
   `auth.CanDeleteUsers(actor)` (`internal/auth/policy.go:98`;
   administrator-only by default, `internal/auth/roles.go:134`).
2. WHEN a caller holding `delete_users` sends `DELETE /users/{id}?force=true` for
   a `{id}` that differs from the caller's own resolved user ID, THE REST users
   endpoint SHALL permit the delete (subject to the remaining requirements).
3. IF a caller sends `DELETE /users/{id}?force=true` for a `{id}` equal to the
   caller's own resolved user ID, THEN THE REST users endpoint SHALL respond
   `403` with code `rest_user_cannot_delete` and SHALL NOT delete that user, even
   when the caller holds `delete_users`.
4. IF an unauthenticated caller (no session, no Application Password) sends
   `DELETE /users/{id}?force=true`, THEN THE REST users endpoint SHALL respond
   `403` with code `rest_user_cannot_delete` and SHALL NOT delete anything.
5. THE REST users endpoint SHALL evaluate the self-delete decision against the
   `{id}` path segment and the resolved principal's user ID, and SHALL NEVER use
   an identity value taken from the request.

### Requirement 9: Reassign/delete of authored content is transactional

**User Story:** As a security reviewer, I want the reassign-or-delete of a user's
content and the user-row removal to be all-or-nothing, so that a failure cannot
leave a half-reassigned, half-deleted database.

#### Acceptance Criteria

1. WHEN `UserRepository.Delete(ctx, id, reassignTo)` runs with `reassignTo > 0`,
   THE storage layer SHALL set `post_author = reassignTo` on every
   `{prefix}posts` row authored by the deleted user BEFORE removing the user's
   `{prefix}users` row and its `{prefix}usermeta` rows (D6).
2. WHEN `UserRepository.Delete(ctx, id, reassignTo)` runs with `reassignTo == 0`,
   THE storage layer SHALL delete every `{prefix}posts` row authored by the
   deleted user BEFORE removing the user's `{prefix}users` row and its
   `{prefix}usermeta` rows (D6).
3. THE storage layer SHALL perform the content reassign-or-delete and the user
   removal within a single database transaction, so a half-reassigned or
   half-deleted state cannot persist (D6).
4. IF any step of the transaction fails, THEN THE storage layer SHALL roll the
   transaction back and return an error, leaving the user, its usermeta, and its
   authored content exactly as they were before the call (fail closed).
5. WHEN `UserRepository.Delete` is asked to delete an id that no user has, THE
   new port SHALL return `domain.ErrNotFound`, matching the `UpdatePass`/
   `UpdateProfile` contract (`internal/domain/repository.go:326`).

### Requirement 10: New domain Delete ports; read ports unchanged

**User Story:** As a maintainer, I want the delete writes to flow through
dedicated domain ports, so that the storage layer owns the delete and the service
layer stays persistence-agnostic.

#### Acceptance Criteria

1. THE `domain.MediaWriter` interface (`internal/domain/repository.go:223`) SHALL
   gain `Delete(ctx context.Context, id int64) error`, added alongside the
   existing `Create`/`SetParent`/`UpdateMeta`, and `wprepo` SHALL implement it
   mirroring the existing `PostWriter.Delete` pattern
   (`internal/domain/repository.go:440`).
2. THE `domain.UserRepository` interface (`internal/domain/repository.go:326`)
   SHALL gain `Delete(ctx context.Context, id int64, reassignTo int64) error`,
   added alongside the existing `ByLogin`/`ByNicename`/`ByID`/`Create`/
   `UpdatePass`/`UpdateProfile`/`ByEmail`, and `wprepo` SHALL implement it
   performing the Requirement 9 transaction.
3. THE two new methods SHALL be additive and SHALL require NO database migration,
   because every affected table (`{prefix}posts`, `{prefix}postmeta`,
   `{prefix}term_relationships`, `{prefix}users`, `{prefix}usermeta`) already
   exists; the task list SHALL verify explicitly that no file is added under
   `internal/storage/migrations`, as M10.B's and M10.C's task lists did.
4. THE read ports (`domain.MediaRepository`, and `UserRepository`'s read methods)
   SHALL be unchanged by this milestone; the only additions are the two `Delete`
   methods.
5. THE delete services SHALL be nil-gated into the web layer the same way the
   create/update services are, so a `Server` constructed without a write-capable
   media/user service keeps serving the existing `DELETE` `501` stubs rather than
   panicking.

### Requirement 11: Auth/CSRF parity, invalid-id handling

**User Story:** As an integrator, I want the delete routes to authenticate and
validate ids exactly like the existing create/update routes, so that one auth
integration covers every REST write surface.

#### Acceptance Criteria

1. WHEN a request to either of The REST deletes is authenticated by an
   Application Password, THE REST endpoint SHALL skip the CSRF check
   (`isAppPasswordAuth`), matching the create/update handlers in
   `internal/web/rest_media.go` and `internal/web/rest_users.go`.
2. WHEN a request to either of The REST deletes is authenticated by a session
   cookie, THE REST endpoint SHALL enforce the M4 `X-CSRF-Token` contract via
   `requireSessionCSRFREST` and SHALL respond `403` with code `rest_forbidden`
   when the token is missing or invalid.
3. IF a `DELETE /media/{id}` request supplies an `{id}` that does not parse to a
   positive integer, THEN THE REST media endpoint SHALL respond `404` with code
   `rest_post_invalid_id`, matching `handleRESTMediaSingle`/`handleRESTMediaUpdate`.
4. IF a `DELETE /users/{id}` request supplies an `{id}` that does not parse to a
   positive integer, THEN THE REST users endpoint SHALL respond `404` with code
   `rest_user_invalid_id`, matching `handleRESTUserSingle`/`handleRESTUserUpdate`.
5. THE milestone SHALL introduce no new capability name and SHALL add no
   `wp-json` route beyond the two `DELETE` verbs on `/media/{id}` and
   `/users/{id}` (D7).

### Requirement 12: Capability-matrix and behavior test coverage

**User Story:** As a maintainer, I want every new delete endpoint proven by an
authorization matrix and a behavior test before it is considered done, so that
the delete surface cannot become an authorization or data-integrity bypass.

#### Acceptance Criteria

1. THE milestone SHALL cover each of The REST deletes with a capability-matrix
   test exercising allowed and forbidden callers: for media, an own-attachment
   delete with/without `delete_posts` and an others'-attachment delete
   with/without `delete_others_posts`, plus anonymous; for users, a delete
   with/without `delete_users`, the self-delete `403 rest_user_cannot_delete`,
   and anonymous.
2. THE milestone SHALL cover the `force`-required contract: `DELETE /media/{id}`
   and `DELETE /users/{id}` without `force=true` each return `400`
   `rest_trash_not_supported` and leave stored state unchanged.
3. THE milestone SHALL cover the user `reassign` contract: a missing `reassign`,
   an unparseable `reassign`, and a `reassign=<id>` for a non-existent user each
   return `400 rest_user_invalid_reassign`; `reassign=false` and
   `reassign=<existing id>` each succeed.
4. THE milestone SHALL cover the delete envelope shape for both routes: a
   successful delete returns `{"deleted": true, "previous": <object>}` whose
   `previous` matches the corresponding `GET` representation (media envelope;
   user edit-context envelope).
5. THE milestone SHALL cover the storage round-trip: for media, the
   `{prefix}posts` row, every `{prefix}postmeta` row, every
   `{prefix}term_relationships` row, and the on-disk file for the id are gone
   after delete; for users, `reassign=<id>` moves authored `post_author` to that
   id, `reassign=false` deletes the authored posts, and the `{prefix}users` row
   and its `{prefix}usermeta` rows are gone — all in one transaction.
6. THE tests SHALL be pure Go over SQLite with NO new environment variable and
   NO new dependency, and `gofmt`, `go vet`, `go build ./...`, and
   `go test ./...` SHALL be green at the end of every task (D8).

## Out of scope

- `DELETE` for posts, pages, terms, comments, and nav menus — posts/pages
  already have `PostWriter.Delete` (`internal/domain/repository.go:440`) and
  menus have `NavMenuWriter.DeleteMenu`/`DeleteItem`; term and comment REST
  delete are not part of this milestone.
- A trash/restore lifecycle for media or users — WordPress trashes neither, so a
  trash state, `untrash`, and `status=trash` reads are not applicable (N/A by
  D2/D4).
- Bulk delete (deleting multiple media or users in one request).
- The admin-UI delete surfaces (`internal/web/adminapi_*`); this milestone enables
  only the two `wp-json` REST `DELETE` verbs.
- Any new capability name (Requirement 11.5) and any database migration
  (Requirement 10.3 confirms none is needed).
- `multipart`/body-carried parameters on delete — `force` and `reassign` are
  read from the query string only.

## Traceability

This spec refines **M11** of the post-M10 gap analysis
(`../post-m10-gap-analysis/README.md`, "M11 — REST delete parity (media + users)"),
the first milestone beyond the completed WordPress-core-parity roadmap (M1–M10).
It builds directly on **M10.B** (`../10.1-rest-media-write`, REST media create +
update) and **M10.C** (`../10.2-rest-user-write`, REST user create + update),
closing the one write verb — `DELETE` — those milestones deferred for both the
media and users endpoints.
