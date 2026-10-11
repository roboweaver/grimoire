# Requirements Document

M10.C — REST User Write Parity

## Introduction

M10a shipped the write-boundary content policy (`internal/sanitize`), and M10.B
(spec `../10.1-rest-media-write`, merged as PR #54) turned on the first new REST
write surface over it: a capability-checked content write service
(`content.MediaWriteService`) over domain ports, nil-gated into the web layer by
a `WithMediaWrites` builder, with WordPress-faithful REST error codes, every
capability decision evaluated against the stored row, the Policy applied before
persistence, `DELETE` left as a `501` stub, and no migration. This milestone
(10.C) is the next consumer and mirrors that pattern exactly, one surface over:
the WordPress users endpoints.

The WordPress user endpoints are live for reads but every write verb is a `501`
stub today. `internal/web/rest_users.go` registers `GET /users`,
`GET /users/me`, and `GET /users/{id}` against real handlers
(`handleRESTUsersCollection`, `handleRESTUserMe`, `handleRESTUserSingle`), then
registers `restNotImplemented` stubs for `POST /users`,
`PUT`/`PATCH`/`DELETE /users`, and `POST`/`PUT`/`PATCH`/`DELETE /users/{id}` —
including, deliberately, `POST /users/{id}`, because WordPress treats `POST` on a
single item as an update. 10.C replaces exactly two of those stub groups with
real handlers:

| Route | Verb(s) | 10.C behavior |
|---|---|---|
| `/users` | `POST` | **Create** a user |
| `/users/{id}` | `PUT`, `PATCH`, `POST` | **Update** a user's profile fields, password, and roles |
| `/users`, `/users/{id}` | `DELETE` | **Unchanged** — stays `501` (out of scope) |

Every write routes through M10a's existing Policy before any free-text profile
field is persisted, every capability decision is evaluated against the stored
user row (never a forged request body), and created/updated passwords are hashed
through the existing `internal/auth/password.Hash` (bcrypt) — the same posture
`content.MediaWriteService` and `content.PostWriteService` already enforce. The
chief new work is a general profile-field update, which `content.UserService`
does not have today: it owns `Create`, `Bootstrap`, `SetRoles`, and
`SetPassword`, but no method that updates `name`/`email`/`url`/`description`/
`slug`. That gap — a new `content.UserService` update method and a new
`domain.UserRepository` field-update method — is the center of this milestone.

This refines **roadmap group 10.C only**, tracing to roadmap **Requirement 17**
(`../wordpress-core-parity-roadmap/requirements.md`, "REST User Write Parity").
Groups 10.D (`content.rendered` improvements, roadmap Requirement 18), 10.E (M10
test coverage), 10.F (menu editing), and 10.G (WordPress `$wp$` hash issuance)
are explicitly out of scope (see "Out of scope").

## Glossary

- **The Policy** — M10a's `sanitize.Policy` (`internal/sanitize/policy.go`), the
  single sanitization component. `Policy.Sanitize(kind, writer, in)` returns the
  value that may be persisted at the tier `sanitize.TierFor` selects, and fails
  closed: on any non-nil error the caller MUST abandon the write.
- **User_Write_Service** — the capability-checked application behavior this
  milestone adds for profile updates, realized as a new method (or methods) on
  the existing `content.UserService` (`internal/content/userservice.go`). It is
  the user analogue of `content.MediaWriteService`: it owns the capability
  checks, the Policy calls, and the self-vs-others evaluation against the stored
  row. `UserService` already owns the create, role, and password paths it reuses.
- **UserService** — the existing `content.UserService`
  (`internal/content/userservice.go`). It already has `Create(ctx, actor, u,
  plainPassword, roles...)` gated on `auth.CanCreateUsers`; `Bootstrap`
  (no-auth, CLI only); `SetRoles(ctx, actor, userID, roles...)` gated on
  `auth.CanEditUsers`; and `SetPassword(ctx, actor, userID, plainPassword)`
  permitted when `actor.UserID == userID` OR `auth.CanEditUsers`. Its
  `provision` helper hashes with `password.Hash` and writes role meta via
  `writeRoles`. It REUSES these methods and gains only the profile-update gap.
- **UserRepository** — the `domain.UserRepository` port
  (`internal/domain/repository.go`). Today it has `ByLogin`, `ByNicename`,
  `ByID`, `Create`, `UpdatePass`, `List`, and `Count`. It has no general
  field-update method; this milestone requires a NEW one (see Requirement 3).
- **Profile field set** — the user fields this milestone allows a caller to
  update: **name** (`display_name`), **email** (`user_email`), **url**
  (`user_url`), **description**, **slug** (`user_nicename`), **password**, and
  **roles**. All but `description` and `password` and `roles` map to columns on
  `domain.User` (`DisplayName`, `Email`, `URL`, `Nicename`); `description` lives
  in `{prefix}usermeta`; `password` flows through `SetPassword`; `roles` flow
  through `SetRoles`.
- **The REST writes** — `POST /users` (create) and
  `PUT`/`PATCH`/`POST /users/{id}` (update), the only two write surfaces this
  milestone enables.
- **create_users / edit_users / promote_users** — the WordPress user-management
  capabilities, administrator-only by default (`internal/auth/roles.go`).
  Helpers `auth.CanCreateUsers` (`create_users`) and `auth.CanEditUsers`
  (`edit_users`) exist in `internal/auth/policy.go`; there is no
  `CanPromoteUsers` helper yet (see Requirement 5).

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | Passwords **reuse bcrypt now**. Created/updated passwords are hashed through the existing `internal/auth/password.Hash` (bcrypt), exactly as roadmap Req 17.2 requires ("never introduce a second hash scheme"). | No second hashing scheme is added. Issuing WordPress `$wp$` hashes plus a `NeedsRehash` upgrade policy is deferred to roadmap group 10.G and is a non-goal here (Requirement 2, Out of scope). |
| D2 | **Self-edit boundary.** A caller MAY update their OWN user record's profile fields (including password) WITHOUT `edit_users`; updating ANOTHER user's record requires `edit_users`. Create requires `create_users`. | Mirrors the existing `UserService.SetPassword` rule (self OR `edit_users`). The capability check is evaluated against the resolved caller's user ID versus the `{id}` path segment, not a request-body field (Requirement 5). |
| D3 | **Field set + roles.** Update accepts `name`, `email`, `url`, `description`, `slug`, `password`, and `roles`. `description` is free-text profile content and MUST pass through the Requirement 15 (M10a sanitize) Policy before persistence (roadmap Req 17.4). Role assignment via REST requires `edit_users` AND `promote_users`; a caller may NOT change their own roles (no self-promote). | `description` routes through the Policy at the broad rich-text field kind (D8); roles route through the existing `UserService.SetRoles`; promote gating needs a `promote_users` check (Requirement 6). |
| D4 | **DELETE out of scope.** `DELETE /users` and `DELETE /users/{id}` keep their untouched `restNotImplemented` `501` stubs. Roadmap Requirement 17 has no delete acceptance criterion. | Delete is NOT reinterpreted as a write this milestone (Requirement 9). |
| D5 | Policy applies BEFORE any write/persist, same fail-closed posture as M10a and M10.B. | A Policy error means no write happens; it maps to a `400` (Requirement 4). |
| D6 | Unauthenticated callers match the existing REST write surfaces (posts/comments/media return `403` for a caller lacking capability, including anonymous). | Anonymous create/update → `403` with WordPress-faithful error codes (Requirement 5). No distinct `401` for the write verbs. |
| D7 | **`description` usermeta key is the prefixless `description`.** It is stored in `{prefix}usermeta` under `meta_key` = `description` (no table prefix), unlike `{prefix}capabilities`/`{prefix}user_level`. | `UserService` writes/reads it via `domain.UserMetaRepository.Set`/the usermeta read with key `description`; the design confirms the key against the target WordPress version (Requirement 3.4, 4.1). |
| D8 | **`description` maps to the broad rich-text sanitize kind.** The `PostContent`-equivalent (`$allowedposttags`/tier-B, tier-C passthrough) kind, not the plain-text `PostTitle` reduction, mirroring WordPress's `wp_filter_kses` on user bio for callers lacking `unfiltered_html`. | The Policy allows an HTML subset for tier-A/B writers and preserves a tier-C (`unfiltered_html`) writer's `description` byte-identically; the one shared `*sanitize.Policy` governs this surface (single-instance invariant) (Requirement 4.1, 4.2). |

## Requirements

### Requirement 1: Create a user via POST /users

**User Story:** As a site administrator using WordPress REST tooling, I want to
create a user with `POST /wp-json/wp/v2/users`, so that existing WordPress
user-management clients work against grimoire unchanged.

#### Acceptance Criteria

1. WHEN a caller holding `create_users` sends `POST /users` with a valid
   `username`, `email`, and `password`, THE User_Write_Service SHALL provision
   the user through the existing `content.UserService.Create` and SHALL create
   one user, replacing the `restNotImplemented` `501` stub for `POST /users`.
2. WHEN a `POST /users` create succeeds, THE REST users endpoint SHALL respond
   `201` with the same envelope shape `GET /users/{id}` returns in edit context
   (the `mapRESTUser` pipeline over `content.RESTMapper.User` producing a
   `content.RESTUserEdit`, plus `_links` for `self` and `collection`).
3. IF a `POST /users` request omits `username`, `email`, or `password`, THEN THE
   REST users endpoint SHALL respond `400` with code `rest_missing_callback_param`
   and SHALL NOT create a user, matching WordPress's required-field validation
   for user creation.
4. IF a `POST /users` request supplies a `username` that already exists, THEN THE
   REST users endpoint SHALL respond `400` with code `existing_user_login` and
   SHALL NOT create a user. WHEN a single request carries both a duplicate
   `username` AND a duplicate `email`, THE REST users endpoint SHALL check and
   report the `username` conflict first (`existing_user_login`), and SHALL NOT
   also report the `email` conflict in that response.
5. IF a `POST /users` request supplies an `email` that already exists AND the
   `username` is not itself a duplicate, THEN THE REST users endpoint SHALL
   respond `400` with code `existing_user_email` and SHALL NOT create a user.
6. WHEN a `POST /users` request supplies one or more `roles`, THE
   User_Write_Service SHALL assign those roles through the existing
   `content.UserService` role path (`writeRoles`/`SetRoles` semantics), subject
   to the role-assignment capability gating of Requirement 6.
7. WHEN a `POST /users` request supplies a `description`, THE User_Write_Service
   SHALL sanitize that value through the Policy at its mapped free-text field
   kind before persisting it to `{prefix}usermeta`, per Requirement 4.

### Requirement 2: Password handling reuses bcrypt

**User Story:** As a security reviewer, I want created and updated passwords to
use the project's one existing hashing scheme, so that no second password format
is introduced on the REST write path.

#### Acceptance Criteria

1. WHEN a user is created or updated with a `password` through The REST writes,
   THE User_Write_Service SHALL hash that password through the existing
   `internal/auth/password.Hash` (bcrypt), reusing `content.UserService`'s
   `provision`/`SetPassword` path, and SHALL NOT introduce a second hashing
   scheme (roadmap Req 17.2).
2. THE REST users endpoints SHALL NEVER return a password hash, session token,
   application-password record, or CSRF secret in any create or update response,
   because the response envelope is `content.RESTUserEdit`, which carries none of
   those fields.
3. THE milestone SHALL NOT issue WordPress `$wp$`-format hashes and SHALL NOT add
   a `NeedsRehash` upgrade policy on the write path; that work is deferred to
   roadmap group 10.G (D1), and this deferral is a stated non-goal rather than an
   omission.

### Requirement 3: New UserRepository profile-update port

**User Story:** As a maintainer, I want user profile-field updates to flow
through a dedicated domain port, so that the storage layer owns the write and the
service layer stays persistence-agnostic.

#### Acceptance Criteria

1. THE `domain.UserRepository` interface (`internal/domain/repository.go`) SHALL
   gain one profile-update method that accepts a user ID and the updatable
   users-table fields (`display_name`, `user_email`, `user_url`,
   `user_nicename`) and persists them to the user's `{prefix}users` row.
2. THE new method SHALL be additive and SHALL NOT require a database migration,
   because every target field already lives in an existing `{prefix}users`
   column (`display_name`, `user_email`, `user_url`, `user_nicename`).
3. WHEN the user ID does not exist, THE new method SHALL return
   `domain.ErrNotFound`, matching the existing `UpdatePass` contract.
4. THE `description` field SHALL be persisted through
   `domain.UserMetaRepository.Set` (its own `{prefix}usermeta` row), NOT through
   the new users-table update method, because `description` is not a
   `{prefix}users` column — it is user metadata, parallel to how `writeRoles`
   writes the capabilities meta. The `meta_key` SHALL be the prefixless
   `description` (D7).
5. THE password update SHALL continue to use the existing
   `UserService.SetPassword`/`UserRepository.UpdatePass` path, and role
   assignment SHALL continue to use the existing `UserService.SetRoles` path,
   rather than being folded into the new profile-update method (D3).

### Requirement 4: Sanitize-policy field-kind mapping for free-text fields

**User Story:** As a security reviewer, I want every free-text user profile field
sanitized before persistence, so that user writes carry no weaker a safety
guarantee than post, comment, and media writes.

#### Acceptance Criteria

1. WHEN a create or update supplies a `description`, THE User_Write_Service SHALL
   call the Policy on that value, at the broad rich-text field kind (the
   `PostContent`-equivalent `$allowedposttags`/tier-B path, D8), before the
   value reaches persistence, satisfying roadmap Req 17.4.
2. WHERE the acting caller holds `unfiltered_html`, THE Policy SHALL select tier
   C for `description` exactly as it does for post content elsewhere, because the
   Policy's tier selection reads `unfiltered_html` through `sanitize.For` and
   takes no user-specific input.
3. THE User_Write_Service SHALL build the Policy's writer argument from the
   resolved principal via `sanitize.For(principal)`, and SHALL use
   `sanitize.Anonymous()` only when no principal is present.
4. IF the Policy returns a non-nil error for `description`, THEN THE REST users
   endpoint SHALL respond `400` and SHALL NOT persist any value for the request
   (fail closed), matching M10a's and M10.B's sentinel-to-`400` mapping.
5. THE `name`, `slug`, `email`, and `url` fields SHALL be handled as WordPress
   handles them on the user write path (plain-text/identifier normalization and
   format validation), and THE design SHALL state, per field, whether the value
   is routed through the Policy (as a plain-text field kind) or validated without
   markup allow-listing; `description` is the only field roadmap Req 17.4 names
   as requiring Policy sanitization, and no field SHALL be persisted with
   un-vetted markup.

### Requirement 5: Capability model for create and self-vs-others update

**User Story:** As a site administrator, I want user writes gated by the same
WordPress capabilities my roles already grant, so that authorization behaves
exactly as it does in WordPress.

#### Acceptance Criteria

1. WHEN a caller lacking `create_users` sends `POST /users`, THE REST users
   endpoint SHALL respond `403` with code `rest_cannot_create` and SHALL NOT
   create a user.
2. WHEN a caller holding `create_users` sends `POST /users`, THE
   User_Write_Service SHALL permit the create (subject to the remaining
   requirements).
3. WHEN a caller sends `PUT`/`PATCH`/`POST /users/{id}` for a `{id}` equal to the
   caller's own resolved user ID, THE User_Write_Service SHALL permit the update
   of that caller's own profile fields and password WITHOUT requiring
   `edit_users` (D2).
4. WHEN a caller sends `PUT`/`PATCH`/`POST /users/{id}` for a `{id}` that differs
   from the caller's own resolved user ID, THE User_Write_Service SHALL require
   `edit_users` and SHALL respond `403` with code `rest_cannot_edit` if the
   caller lacks it.
5. THE User_Write_Service SHALL evaluate the self-vs-others decision against the
   `{id}` path segment and the resolved principal's user ID, and SHALL NEVER use
   an identity value taken from the request body, matching
   `content.UserService.SetPassword` and `content.PostWriteService.Update`.
6. IF an unauthenticated caller (no session, no Application Password) sends any of
   The REST writes, THEN THE REST users endpoint SHALL respond `403` — with code
   `rest_cannot_create` for create and `rest_cannot_edit` for update — matching
   the forbidden-write convention of `internal/web/rest_posts.go` and
   `internal/web/rest_media.go` (a capability failure returns
   `http.StatusForbidden` rather than a distinct `401`), and SHALL NOT create or
   modify any user.

### Requirement 6: Role assignment gating and no self-promote

**User Story:** As a site administrator, I want role changes restricted to
callers WordPress would allow, so that a user cannot escalate their own
privileges through the REST API.

#### Acceptance Criteria

1. WHEN a create or update supplies `roles`, THE User_Write_Service SHALL require
   the caller to hold BOTH `edit_users` AND `promote_users` and SHALL respond
   `403` with code `rest_cannot_edit_roles` if the caller lacks either, and SHALL
   NOT change any role assignment.
2. WHEN a caller sends `PUT`/`PATCH`/`POST /users/{id}` with `roles` for a `{id}`
   equal to the caller's own resolved user ID, THE User_Write_Service SHALL
   reject the role change with `403` and code `rest_cannot_edit_roles` and SHALL
   NOT change the caller's own roles (no self-promote), even when the caller
   holds `promote_users`.
3. WHEN role assignment is permitted, THE User_Write_Service SHALL apply it
   through the existing `content.UserService.SetRoles`, which already gates on
   `auth.CanEditUsers` and writes `{prefix}capabilities` and `{prefix}user_level`
   meta via `writeRoles`.
4. THE `promote_users` check SHALL resolve the capability through the specific
   existing `promote_users` capability path — `Principal.Can("promote_users")`,
   optionally wrapped as a new `auth.CanPromoteUsers` helper added alongside
   `CanEditUsers` that itself calls `Principal.Can("promote_users")` — and SHALL
   NOT be satisfied by any other generic capability-resolution mechanism,
   introducing no second capability mechanism and no new role table.

### Requirement 7: Partial update semantics and the response envelope

**User Story:** As an integrator using WordPress REST tooling, I want to update a
subset of a user's fields and get back the user's current edit-context
representation, so that partial updates behave like WordPress's.

#### Acceptance Criteria

1. WHEN a caller authorized per Requirement 5 sends `PUT`/`PATCH`/`POST
   /users/{id}` with any subset of `name`, `email`, `url`, `description`, `slug`,
   `password`, and `roles`, THE User_Write_Service SHALL load the stored user,
   apply the Policy to each supplied free-text field, persist the changed fields,
   and respond `200` with the same edit-context envelope `GET /users/{id}`
   returns.
2. WHILE a request omits a given updatable field, THE User_Write_Service SHALL
   leave that field's stored value unchanged (partial update), matching the REST
   post and media write semantics in `internal/web/rest_posts.go` and
   `internal/web/rest_media.go`.
3. WHEN a `PUT`/`PATCH`/`POST /users/{id}` request supplies a `slug`, THE
   User_Write_Service SHALL update the user's `user_nicename` through the new
   profile-update port (Requirement 3).
4. IF the `{id}` path segment does not resolve to an existing user, THEN THE REST
   users endpoint SHALL respond `404` with code `rest_user_invalid_id` and SHALL
   NOT persist any change, matching the existing `handleRESTUserSingle` behavior.
5. IF a request body is malformed (not decodable to the expected shape, or a
   field carries a value of the wrong type), THEN THE REST users endpoint SHALL
   respond `400` with code `rest_invalid_param` and SHALL NOT persist any change.
6. WHEN a user is created or updated through The REST writes, THE subsequent
   `GET /users/{id}` SHALL return the sanitized stored values, so the read path
   reflects exactly what the write path persisted.

### Requirement 8: DELETE stays 501

**User Story:** As a maintainer, I want user delete to remain explicitly
unimplemented, so that disabling it is a visible contract rather than a silent
404 or 405.

#### Acceptance Criteria

1. THE registration for `DELETE /users` SHALL remain the existing
   `restNotImplemented` stub and SHALL respond `501`.
2. THE registration for `DELETE /users/{id}` SHALL remain the existing
   `restNotImplemented` stub and SHALL respond `501`.
3. THE DELETE routes SHALL NOT be reinterpreted as writes and SHALL NOT invoke
   the User_Write_Service.

### Requirement 9: No read-path, migration, or rendering regression

**User Story:** As a REST API consumer, I want existing user reads and the rest of
the system to behave exactly as before, so that enabling writes does not change
what GET returns.

#### Acceptance Criteria

1. THE `GET /users`, `GET /users/me`, and `GET /users/{id}` handlers SHALL
   continue to return the same collection and single-item responses, with
   unchanged `_links`, pagination headers, status codes, and view/edit context
   selection (`restUserContext`).
2. THE milestone SHALL require NO database migration, because every target field
   already lives in an existing column (`display_name`, `user_email`,
   `user_url`, `user_nicename` on `{prefix}users`) or in the already-used
   `{prefix}usermeta` table (`description`, `{prefix}capabilities`,
   `{prefix}user_level`). The task list SHALL verify explicitly that no file is
   added under `internal/storage/migrations`, as M10a's and M10.B's task lists
   did.
3. THE User_Write_Service SHALL be nil-gated into the web layer by a builder
   method (the user analogue of `WithMediaWrites`), so a `Server` constructed
   without it keeps serving the existing `501` stubs rather than panicking.

### Requirement 10: Authentication consistent with M10a/M10.B REST writes

**User Story:** As an integrator, I want user writes to authenticate exactly like
the post, comment, and media REST writes, so that one auth integration covers
every REST write surface.

#### Acceptance Criteria

1. WHEN a request to any of The REST writes is authenticated by an Application
   Password, THE REST users endpoint SHALL skip the CSRF check, matching
   `internal/web/rest_posts.go`, `internal/web/rest_comments.go`, and
   `internal/web/rest_media.go`.
2. WHEN a request to any of The REST writes is authenticated by a session cookie,
   THE REST users endpoint SHALL enforce the M4 CSRF synchronizer token via
   `requireSessionCSRFREST` and SHALL respond `403` with code `rest_forbidden`
   when the token is missing or invalid.
3. THE REST users endpoints SHALL perform no per-handler Policy call beyond what
   the User_Write_Service performs, so sanitization lives in exactly one place on
   the write path.

### Requirement 11: Correctness and safety invariants

**User Story:** As a security reviewer, I want the user write path to carry the
same end-to-end guarantees M10a and M10.B established, so that the new surface
cannot become a sanitization or authorization bypass.

#### Acceptance Criteria

1. THE User_Write_Service SHALL sanitize every supplied `description` value at
   the writer's tier before that value reaches persistence; no create or update
   SHALL persist an unsanitized caller-supplied `description`.
2. THE User_Write_Service SHALL evaluate every capability decision — create,
   self-vs-others update, and role assignment — against the resolved principal
   and the stored user identity, never a request-body identity or role field, so
   a forged body cannot escalate privileges.
3. IF the Policy returns an error OR a capability check fails OR a required-field
   or uniqueness check fails, THEN THE User_Write_Service SHALL perform no write
   and the stored state SHALL be unchanged (fail closed).
4. THE created or updated user's password SHALL be stored only as a bcrypt hash
   produced by `password.Hash`; no code path SHALL persist a plaintext password
   or a non-bcrypt hash.

### Requirement 12: Capability-matrix and content-safety test coverage

**User Story:** As a maintainer, I want every new write endpoint proven by an
authorization matrix and a sanitization test before it is considered done, so
that the roadmap's M10 done-definition is met.

#### Acceptance Criteria

1. THE milestone SHALL cover each of The REST writes with a capability-matrix
   test exercising allowed and forbidden callers — including anonymous,
   self-edit, other-user edit, create with and without `create_users`, and role
   assignment with and without `edit_users` + `promote_users` (including the
   self-promote rejection) — satisfying roadmap Req 19.2.
2. THE milestone SHALL cover The REST writes with a content-safety test proving
   the Policy is applied to `description` before persistence (a tier-A/B writer's
   markup is reduced and a tier-C writer's `description` is preserved
   byte-identically), satisfying roadmap Req 19.2.
3. THE work SHALL follow the standing convention of the previous milestones:
   `gofmt`, `go vet`, `go build ./...`, and `go test ./...` SHALL be green at the
   end of every task.

## Out of scope

- `DELETE /users` and `DELETE /users/{id}` (D4) — remain `501`.
- WordPress `$wp$`-format hash issuance and `NeedsRehash` upgrade policy (D1) —
  deferred to roadmap group 10.G.
- Roadmap group 10.D (`content.rendered` improvements, roadmap Requirement 18).
- Roadmap group 10.F (navigation menu editing).
- Arbitrary plugin-contributed REST fields and user meta beyond `description`,
  `{prefix}capabilities`, and `{prefix}user_level` (roadmap Req 19.1).
- Any database migration (Requirement 9.2 confirms none is needed).
- User self-registration flows and email-verification/activation
  (`user_activation_key`) — not part of the create/update capability surface.

## Resolved design decisions

Resolved by the spec owner; recorded here and reflected in the decisions and
acceptance criteria above:

1. **The `description` usermeta key is the prefixless `description`.** WordPress
   stores a user's biography in `{prefix}usermeta` under the prefixless
   `meta_key` `description` (not `{prefix}description`), unlike
   `{prefix}capabilities` / `{prefix}user_level` which carry the prefix. The
   design still confirms the exact key against the target WordPress version
   before wiring `UserMetaRepository.Set`; Requirement 3.4 and Requirement 4.1
   depend on it (D7).
2. **The sanitize field kind for `description` is the broad rich-text kind.**
   `description` maps to the broad `PostContent`-equivalent kind (the
   `$allowedposttags`/tier-B, tier-C passthrough path), not the plain-text
   `PostTitle` reduction, mirroring WordPress's `wp_filter_kses` on the user bio
   for callers lacking `unfiltered_html`. The single shared `*sanitize.Policy`
   instance governs this write surface too (the single-instance invariant from
   M10a and M10.B) (D8).

## Traceability

This spec refines **roadmap group 10.C** and traces to roadmap **Requirement 17**
(`../wordpress-core-parity-roadmap/requirements.md`, "REST User Write Parity"),
with its test-coverage requirement tracing to roadmap **Requirement 19.2**. It
depends on M10a (roadmap group 10.A / Requirement 15), whose `internal/sanitize`
Policy it reuses without modification, and it mirrors the pattern established by
M10.B (`../10.1-rest-media-write`, roadmap group 10.B / Requirement 16).
