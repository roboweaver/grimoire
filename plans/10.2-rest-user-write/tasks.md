# Implementation Plan: M10.C — REST User Write Parity

## Overview

Implementation checklist for M10.C (roadmap group **10.C only**). Tasks are
ordered so each builds on the last, and each references the exact files, the
exact symbols named in `design.md`'s "Components and Interfaces" / "Data Models"
sections, and the Requirement/AC plus the Finding (1–3) and Inherited decision
(D1–D8) it satisfies. Keep `gofmt -l .` empty and `go vet ./...`,
`go build ./...`, `go test ./...` green after every phase.

Strict TDD: within each phase the failing test is written **before** the
implementation that passes it, matching how M5–M10.B were run. The ordering below
makes that spine visible — every behavior-adding sub-task is preceded by the
sub-task that writes its failing test (Req 12.3).

> **This refines roadmap group 10.C only.** It is the next consumer of the REST
> write pattern M10.B established, one surface over, tracing to roadmap
> **Requirement 17**. Roadmap groups **10.D/10.E/10.F/10.G remain open** and are
> explicitly out of scope: `content.rendered` improvements (10.D), menu editing
> (10.F), and WordPress `$wp$` hash issuance (10.G) are untouched, and
> `DELETE /users[/{id}]` stays a `501` stub (Req 8). It reuses M10a's
> `internal/sanitize` Policy without modification and mirrors M10.B's
> `content.MediaWriteService` posture: load the stored row, authorize against
> that row (never the body), sanitize before persist (fail closed), and nil-gate
> the surface into the web layer.
>
> **No task here authorizes a migration.** Every target field already lives in an
> existing column (`display_name`/`user_email`/`user_url`/`user_nicename` on
> `{prefix}users`) or in the already-used `{prefix}usermeta` table (the prefixless
> `description` key, plus `{prefix}capabilities`/`{prefix}user_level` via the
> existing `writeRoles`). Task 7.3 verifies explicitly that no file is added under
> `internal/storage/migrations`, as M8/M9a/M9b/M10a/M10.B did (Req 3.2, 9.2).
>
> **Three design Findings change scope and are reflected in these tasks.**
> *Finding 1* — `domain.UserRepository` has no `ByEmail`, so the duplicate-email
> check Req 1.5 names is not implementable as-is; the fix adds one additive read
> method `ByEmail(ctx, email)` → `(User, error)` mirroring `ByLogin`, so the
> create handler can check `ByLogin` first (`existing_user_login`) then `ByEmail`
> (`existing_user_email`) and return on the first conflict (tasks 1.2, 2.x, 5.x).
> *Finding 2* — no `sanitize.FieldKind` names a user field, so `description`
> reuses `sanitize.PostContent` (which already gives tier B to a non-unfiltered
> writer and tier-C passthrough to an `unfiltered_html` writer — exactly D8) with
> no new enum value and no change to the already-fuzzed `sanitize` package; the
> realistic policy-error surface for `description` is empty (`PostContent` returns
> `(string, nil)`), so the fail-closed `→ 400` mapping is a guard against future
> change, not a live path (tasks 4.x, 5.x). *Finding 3* — `UserService.SetRoles`
> gates on `edit_users` only, so the handler performs the full
> `edit_users AND promote_users AND not-self` decision *before* delegating to
> `SetRoles`; `SetRoles` is left unchanged and its inner `edit_users` check is a
> harmless defense-in-depth redundancy (tasks 3.x, 5.x).
>
> **Deliberate reuse postures carried from M10.B.** The content policy on
> `UserService` defaults to `sanitize.New()` and is **never nil** (an unwired
> service sanitizes rather than silently passing input through, fail closed). The
> create path reuses `UserService.Create` directly — no wrapper — and a supplied
> `description` is persisted via `UserService.UpdateProfile` on the freshly
> created ID, so create and update share one description-sanitize path (Req 1.7).
> Password reuses `SetPassword` → `password.Hash` (bcrypt) and roles reuse
> `SetRoles`; no second hash scheme and no new capability name is introduced
> (D1, Req 6.4). The self-vs-others decision is `id == actor.UserID` against the
> `{id}` path segment and the resolved principal, never a body identity.
>
> **Interface growth breaks callers until updated.** Adding `UpdateProfile` and
> `ByEmail` to the `domain.UserRepository` interface (task 1.2) breaks every
> fake/concrete `UserRepository` (the `wprepo` concrete repo, and the fakes in
> `internal/content` and `internal/web` tests) until each gains the methods;
> those are resolved in later phases (wprepo in Phase 2, the content fake in
> Phase 4, the web fakes in Phase 6). Adding the `description`/policy field and
> `UpdateProfile` method on `content.UserService` is additive to its callers
> because the constructor stays backward-compatible (variadic option, task 4.2).
> Each task that introduces such a break notes it inline, as M10.B's tasks did.

## Tasks

## Phase 1 — Domain port and value object (pure, no DB, no HTTP)

- [x] 1.1 Write the failing value-object test first. In `internal/domain`
      (a new or existing `*_test.go`, e.g. `entities_test.go`) assert that a new
      `domain.UserProfileFields` value object exists with exactly four pointer
      fields — `DisplayName`, `Email`, `URL`, `Nicename` (all `*string`) and
      nothing else — and that its `any()` method reports `false` for an all-nil
      value and `true` when any one field is non-nil. Additive only: nothing is
      removed or retyped, so no existing `domain` reader regresses.
      Files: `internal/domain/entities_test.go`
      Verify: `go test ./internal/domain` — fails (`UserProfileFields`/`any()`
      absent). _(Req 3.1; Finding — none; D-none)_

- [x] 1.2 Add `domain.UserProfileFields` (+ the `any()` helper) in
      `internal/domain/entities.go`, and grow the `domain.UserRepository`
      interface (`internal/domain/repository.go`) with two methods beside
      `UpdatePass`: `UpdateProfile(ctx context.Context, id int64, f UserProfileFields) error`
      and `ByEmail(ctx context.Context, email string) (User, error)`. Pin the
      contracts in doc comments: `UpdateProfile` writes only the non-nil fields to
      the user's `{prefix}users` row (`display_name`/`user_email`/`user_url`/
      `user_nicename`), returns `domain.ErrNotFound` on an unknown ID (matching the
      `UpdatePass` contract), and **never** touches `description` (usermeta,
      Req 3.4) or the password (`UpdatePass`, Req 3.5); `ByEmail` returns the row
      or `domain.ErrNotFound` when no user has that email, mirroring `ByLogin`
      (Finding 1). Both are additive and require no migration (every target column
      already exists on `{prefix}users`).
      **Note — breaks callers:** growing the interface breaks every fake/concrete
      `UserRepository` (the `wprepo` repo, the `internal/content` service fakes,
      the `internal/web` transport fakes) until each gains both methods; those are
      resolved in later phases (wprepo in Phase 2, content fake in Phase 4, web
      fakes in Phase 6).
      Files: `internal/domain/entities.go`, `internal/domain/repository.go`
      Verify: `go test ./internal/domain` — the 1.1 assertions pass;
      `go build ./...` now fails on the un-updated fakes (expected — resolved by
      later tasks). _(Req 3.1, 3.2, 3.3; Finding 1)_

## Phase 2 — Storage: `wprepo.UserRepo` (`internal/storage/wprepo`)

- [x] 2.1 Write failing contract tests for `UserRepo.UpdateProfile` and
      `UserRepo.ByEmail` first, in `internal/storage/wprepo/users_test.go`, through
      the existing storage test harness the other user reads use (SQLite always,
      no new `GRIMOIRE_TEST_*` vars). Assert: `UpdateProfile` writes
      `display_name`, `user_email`, `user_url`, `user_nicename` on the user's
      `{prefix}users` row; writes **only** the columns whose `UserProfileFields`
      pointer is non-nil (an omitted field leaves the stored column untouched,
      Req 7.2); returns `domain.ErrNotFound` for an unknown ID (Req 3.3). Assert
      `ByEmail` returns the row for a stored `user_email` and `domain.ErrNotFound`
      when absent, round-tripping against a row written by `Create`/`UpdateProfile`
      (Finding 1).
      Files: `internal/storage/wprepo/users_test.go`
      Verify: `go test ./internal/storage/wprepo` — fails
      (`UpdateProfile`/`ByEmail` not implemented). _(Req 3.1, 3.2, 3.3; Finding 1)_

- [x] 2.2 Implement `UserRepo.UpdateProfile` and `UserRepo.ByEmail` in
      `internal/storage/wprepo/users.go`. `UpdateProfile` builds a single
      `UPDATE {prefix}users` that sets only the columns whose pointer is non-nil,
      keyed by `ID`, and returns `domain.ErrNotFound` when the update matches no
      row — the same `errNotFoundIfMissing`-style check `UpdatePass` already uses;
      it must no-op safely if called with no field set (the service's `any()` guard
      means this is unreachable in practice, but the method stays safe). `ByEmail`
      is a `SELECT` on `user_email` mirroring `ByLogin` exactly, returning
      `domain.ErrNotFound` when absent. Both use the existing rebind pattern.
      **Note — resolves the task-1.2 break for storage:** this makes
      `*wprepo.UserRepo` satisfy the grown `domain.UserRepository` interface.
      Files: `internal/storage/wprepo/users.go`
      Verify: `go test ./internal/storage/wprepo` — the 2.1 tests pass. _(Req 3.1,
      3.2, 3.3; Finding 1)_

## Phase 3 — `auth` helper

- [x] 3.1 Write the failing test first in `internal/auth` (e.g.
      `policy_test.go`) asserting `auth.CanPromoteUsers(actor)` returns `true` iff
      the principal holds `promote_users` and `false` otherwise (built through the
      real capability resolution, e.g. a principal with/without `promote_users`).
      Files: `internal/auth/policy_test.go`
      Verify: `go test ./internal/auth` — fails (`CanPromoteUsers` absent).
      _(Req 6.4)_

- [x] 3.2 Add `auth.CanPromoteUsers(actor Principal) bool` in
      `internal/auth/policy.go`, beside `CanEditUsers`, as nothing more than
      `actor.Can("promote_users")` — no second capability mechanism and no new
      role table, so the `promote_users` decision reads as a named function at the
      same level as its siblings.
      Files: `internal/auth/policy.go`
      Verify: `go test ./internal/auth` — the 3.1 test passes; `go vet ./...`
      clean. _(Req 6.4)_

## Phase 4 — `content.UserService`: policy field + `UpdateProfile`

- [x] 4.1 Write failing unit tests first in
      `internal/content/userservice_test.go` against a fake
      `domain.UserRepository`/`domain.UserMetaRepository` and the **real**
      `sanitize.Policy`, asserting on what reached the ports. Cover: the new
      `policy` field is defaulted by the variadic `NewUserService` to
      `sanitize.New()` and is never nil, `WithUserContentPolicy(p)` overrides it,
      and the existing three-argument `NewUserService(users, meta, prefix)` call
      sites still compile and get the default; `UpdateProfile` permits a
      self-record edit without `edit_users` and requires `edit_users` for another
      record (`content.ErrForbidden` otherwise), evaluated against the `id` versus
      `actor.UserID`, never a body value; a missing id bubbles up
      `domain.ErrNotFound`; only supplied (non-nil) fields reach
      `UserRepository.UpdateProfile` (partial update) and a description-only update
      issues **no** users-table `UpdateProfile` call (the `fields.any()` guard);
      `description` is sanitized at `PostContent` (a tier-B writer's `<script>` is
      stripped) and persisted via `UserMetaRepository.Set` with key `description`
      (D7); and a tier-C (`unfiltered_html`) writer's `description` survives
      byte-identically (input the Policy demonstrably alters at tier B).
      Files: `internal/content/userservice_test.go`
      Verify: `go test ./internal/content` — fails (field/option/`UpdateProfile`
      absent). _(Req 3.4, 3.5, 4.1, 4.2, 4.3, 4.4, 5.3, 5.4, 5.5, 7.2, 7.3, 7.6,
      11.1, 11.3; Finding 2; D7, D8)_

- [x] 4.2 Implement the `UserService` policy seam and `UpdateProfile` in
      `internal/content/userservice.go`: add the never-nil `policy *sanitize.Policy`
      field; add `UserServiceOption`, `WithUserContentPolicy`, and change
      `NewUserService` to the variadic form that defaults `policy` to
      `sanitize.New()` and applies options (so the CLI/e2e three-arg call sites
      compile unchanged and get the default); declare `content.UserProfileUpdate`
      (name/email/url/slug/description pointers); and implement `UpdateProfile`
      exactly as the design specifies — load the stored row via `users.ByID`
      (`domain.ErrNotFound` bubbles up), evaluate self-vs-others
      (`actor.UserID != id && !auth.CanEditUsers(actor)` → `ErrForbidden`), map
      the supplied pointers into `domain.UserProfileFields`, sanitize a supplied
      `description` via `policy.Sanitize(sanitize.PostContent, sanitize.For(actor),
      *desc)` (returning the policy error unchanged, fail closed), persist the
      users-table fields via `users.UpdateProfile` only when `fields.any()` and a
      changed `description` via `meta.Set(ctx, id, "description", …)`, then re-read
      via `users.ByID` so the caller envelopes the stored values. Document the
      `PostContent`-for-a-user-bio wrinkle at the `Sanitize` call (Finding 2, D8).
      **Note — resolves the task-1.2 break for the content fake:** update the
      `internal/content` test fake `UserRepository` to implement
      `UpdateProfile`/`ByEmail`.
      Files: `internal/content/userservice.go`
      Verify: `go test ./internal/content` — the 4.1 tests pass; `go vet ./...`
      clean. _(Req 3.4, 3.5, 4.1, 4.2, 4.3, 4.4, 5.3, 5.4, 5.5, 7.2, 7.3, 7.6,
      11.1, 11.3; Finding 2; D7, D8)_

## Phase 5 — REST transport handlers (`internal/web/rest_users.go`)

- [x] 5.1 Write failing **create** transport tests first in
      `internal/web/rest_users_test.go`, end-to-end through the router with a real
      `content.UserService` wired via `WithUserWrites`: `201` edit-context envelope
      on success whose shape matches `GET /users/{id}` (via `mapRESTUser` →
      `RESTUserEdit`, Req 1.2); missing `username`/`email`/`password` →
      `400 rest_missing_callback_param` and no write (Req 1.3); duplicate
      `username` → `400 existing_user_login` (Req 1.4); duplicate `email` with a
      non-duplicate `username` → `400 existing_user_email` (Req 1.5); a request
      carrying **both** a duplicate username and a duplicate email reports only
      `existing_user_login` (duplicate-first ordering, Req 1.4); a caller lacking
      `create_users` and an anonymous caller → `403 rest_cannot_create`
      (Req 5.1, 5.6); a create carrying `roles` without `edit_users`+
      `promote_users` → `403 rest_cannot_edit_roles` (Req 6.1); malformed JSON body
      → `400 rest_invalid_param` (Req 7.5); and a `description` with markup is
      sanitized before it reaches the store (assert the fake/store saw inert
      content, Req 1.7). Use the real `UserService` so a transport that bypassed
      the service could not pass.
      Files: `internal/web/rest_users_test.go`
      Verify: `go test ./internal/web` — fails (`handleRESTUserCreate` absent).
      _(Req 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 2.1, 2.2, 5.1, 5.6, 6.1, 7.5, 10.1,
      10.2; Finding 1)_

- [x] 5.2 Implement `handleRESTUserCreate`, `restUserCreateBody`, and the shared
      auth preamble in `internal/web/rest_users.go`. The preamble matches
      `rest_posts.go`/`rest_media.go` verbatim: an Application Password skips CSRF
      (`isAppPasswordAuth`); a session enforces M4 CSRF via `requireSessionCSRFREST`
      → `403 rest_forbidden`; resolve the principal via `PrincipalFrom`
      (Req 10.1, 10.2). Decode `restUserCreateBody`; validate required
      `username`/`email`/`password` (→ `400 rest_missing_callback_param`); apply
      the role gate before any write (a create carrying `roles` requires
      `edit_users` AND `promote_users`, else `403 rest_cannot_edit_roles`, Req 6.1
      — the no-self-promote rule does not apply to create); check duplicates in
      order via `ByLogin` then `ByEmail`, returning on the first conflict
      (`existing_user_login` / `existing_user_email`, Finding 1); call the reused
      `UserService.Create` (map `content.ErrForbidden` → `403 rest_cannot_create`,
      default → `500 rest_user_create_failed`); then, for a supplied `description`,
      sanitize+persist via `UserService.UpdateProfile` on the new ID; finally
      re-read via `s.restUsers.ByID` and envelope through `mapRESTUser` in edit
      context → `201`.
      Files: `internal/web/rest_users.go`
      Verify: `go test ./internal/web` — the 5.1 tests pass. _(Req 1.1, 1.2, 1.3,
      1.4, 1.5, 1.6, 1.7, 2.1, 2.2, 5.1, 5.6, 6.1, 7.5, 10.1, 10.2; Finding 1)_

- [x] 5.3 Write failing **update** transport tests first in
      `internal/web/rest_users_test.go`: `200` edit-context envelope on success
      (shape matches `GET /users/{id}`, Req 7.1); invalid/non-positive `{id}` →
      `404 rest_user_invalid_id` (Req 7.4); unknown user (`domain.ErrNotFound`) →
      `404 rest_user_invalid_id` (Req 7.4); malformed/wrong-typed body →
      `400 rest_invalid_param` (Req 7.5); a partial update leaves omitted fields
      unchanged (Req 7.2); self-edit permitted without `edit_users` (Req 5.3);
      another user's edit without `edit_users` → `403 rest_cannot_edit`
      (Req 5.4, 5.5); a `password` on another's record without `edit_users` →
      `403 rest_cannot_edit` (Req 2.1, 5.4); `roles` without `edit_users`+
      `promote_users` → `403 rest_cannot_edit_roles` (Req 6.1); a self-promote
      (`roles` on own record) → `403 rest_cannot_edit_roles` (Req 6.2); and
      `POST /users/{id}` routes to **update**, not create (Req 7.1).
      Files: `internal/web/rest_users_test.go`
      Verify: `go test ./internal/web` — fails (`handleRESTUserUpdate` absent).
      _(Req 2.1, 5.3, 5.4, 5.5, 5.6, 6.1, 6.2, 7.1, 7.2, 7.4, 7.5, 7.6, 10.1,
      10.2)_

- [x] 5.4 Implement `handleRESTUserUpdate`, `restUserUpdateBody`, and
      `parseRESTUserUpdate` in `internal/web/rest_users.go`. Same auth preamble as
      create. Parse `{id}` (invalid/≤0 → `404 rest_user_invalid_id`). Decode the
      pointer-field body for partial-update semantics; `parseRESTUserUpdate`
      validates field types/formats (slug identifier shape, email/url format,
      `roles` as a string array) → `400 rest_invalid_param` (Req 7.5). Apply the
      role gate **first**, before any profile write: a `roles` change on the
      caller's own record is a self-promote → `403 rest_cannot_edit_roles`
      (Req 6.2); otherwise require `edit_users` AND `promote_users`, else
      `403 rest_cannot_edit_roles` (Req 6.1). Then orchestrate
      `UserService.UpdateProfile` (profile fields + `description`),
      `UserService.SetPassword` (a supplied `password`), and
      `UserService.SetRoles` (permitted `roles`); re-read via `s.restUsers.ByID`
      and envelope through `mapRESTUser` in edit context → `200`. Map
      `domain.ErrNotFound` → `404 rest_user_invalid_id`, `content.ErrForbidden`
      → `403 rest_cannot_edit`, a policy error → `400 rest_invalid_param`
      (fail-closed guard, Finding 2), default → `500 rest_user_update_failed`.
      Files: `internal/web/rest_users.go`
      Verify: `go test ./internal/web` — the 5.3 tests pass. _(Req 2.1, 5.3, 5.4,
      5.5, 5.6, 6.1, 6.2, 7.1, 7.2, 7.4, 7.5, 7.6, 10.1, 10.2)_

- [x] 5.5 Write the failing nil-gate/stub test first, then wire
      `registerRESTUsers`. In `internal/web/rest_users_test.go` assert that with
      `userWrite == nil` every write verb (`POST /users`, and
      `POST`/`PUT`/`PATCH /users/{id}`) answers the existing `501`
      `restNotImplemented` stub, and that `DELETE /users`, `DELETE /users/{id}`,
      and the collection `PUT`/`PATCH /users` answer `501` and never invoke the
      service regardless of wiring (Req 8). Then update `registerRESTUsers`
      (`internal/web/rest_users.go`) to register the real `handleRESTUserCreate`
      and `handleRESTUserUpdate` (the `POST`/`PUT`/`PATCH` single-item loop) **only
      when `s.userWrite != nil`**, falling back to the current `restNotImplemented`
      stubs otherwise, and to leave `DELETE /users`, `DELETE /users/{id}`, and the
      collection `PUT`/`PATCH /users` as their existing untouched `501`
      registrations (mirroring `registerRESTMedia`'s `s.mediaWrite != nil` gate).
      Files: `internal/web/rest_users.go`, `internal/web/rest_users_test.go`
      Verify: `go test ./internal/web` — the 501-stub and nil-fallback tests pass.
      _(Req 8.1, 8.2, 8.3, 9.3)_

## Phase 6 — Server + main wiring

- [x] 6.1 Add the `userWrite *content.UserService` field to the `Server`
      (`internal/web/router.go`, beside `mediaWrite`) and the `WithUserWrites`
      builder (`internal/web/contentfeatures.go`, mirroring `WithMediaWrites`) that
      stores it and returns the `Server` for chaining; when `uw` is nil
      `registerRESTUsers` keeps the `501` stubs (Req 9.3). Write/extend a wiring
      test in `internal/web` (mirroring `mediawrite_wiring_test.go`) asserting that
      `WithUserWrites` makes `POST /users` reachable off the `501` stub (the
      non-501 observable signal that `s.userWrite` reached the router) and that the
      one `*sanitize.Policy` instance governs the post, comment, media, and user
      write surfaces (single-instance invariant, Req 11.1, 4.2).
      **Note — resolves the task-1.2 break for web fakes:** update any fake
      `domain.UserRepository` in `internal/web` test helpers to implement
      `UpdateProfile`/`ByEmail`.
      Files: `internal/web/router.go`, `internal/web/contentfeatures.go`,
      `internal/web/*_test.go`
      Verify: `go build ./...` succeeds; `go test ./internal/web` green. _(Req 4.2,
      9.3, 11.1)_

- [x] 6.2 Construct `UserService` in `cmd/grimoire/main.go` next to the post,
      comment, and media services, over `repos.Users`/`repos.UserMeta` and the
      table prefix, handed the shared `contentPolicy` via
      `content.WithUserContentPolicy(contentPolicy)`, and pass it to
      `.WithUserWrites(userWrite)` on the `Server` builder so `registerRESTUsers`
      wires the real handlers. This is new construction — `main.go` builds no
      `UserService` today — so one `*sanitize.Policy` now also governs the user
      `description` surface.
      Files: `cmd/grimoire/main.go`
      Verify: `go build ./...` and `go test ./cmd/...` green; the transport tests
      from 5.1/5.3 pass against the real wiring. _(Req 9.3, 11.1)_

## Phase 7 — Capability matrix, content-safety, and final gates

- [x] 7.1 Add the capability-matrix transport test in
      `internal/web/rest_users_test.go` (one place, table-driven): across
      anonymous, subscriber, contributor, author, editor, administrator (built
      through the real `auth.CapabilitiesForRoles`/`auth.NewPrincipal`), assert
      create is permitted exactly for `create_users` holders and
      `403 rest_cannot_create` otherwise; self-update is permitted without
      `edit_users` and other-user update requires `edit_users` (`403
      rest_cannot_edit` otherwise), always against the stored `{id}` vs the
      principal; role assignment requires `edit_users` AND `promote_users`
      (`403 rest_cannot_edit_roles` otherwise) and a self-promote is rejected even
      for a `promote_users` holder. Include the auth-matrix cells: an Application
      Password skips CSRF; a session without a valid CSRF token →
      `403 rest_forbidden`.
      Files: `internal/web/rest_users_test.go`
      Verify: `go test ./internal/web` passes. _(Req 5.1, 5.2, 5.3, 5.4, 5.5, 5.6,
      6.1, 6.2, 10.1, 10.2, 12.1)_

- [x] 7.2 Add the content-safety and observability end-to-end tests in
      `internal/web/rest_users_test.go`: a submitted `<script>` in `description` is
      inert in storage — assert the writer/store saw the sanitized value, not
      merely that the response escaped it (Req 4.1, 11.1); a
      create → update `description` → `GET /users/{id}` round-trip returns the
      sanitized stored value (Req 7.6); and a tier-C (`unfiltered_html`) writer's
      `description` is preserved byte-identically (input the Policy demonstrably
      alters at tier B, Req 4.2).
      Files: `internal/web/rest_users_test.go`
      Verify: `go test ./internal/web` passes. _(Req 4.1, 4.2, 7.6, 11.1, 12.2)_

- [x] 7.3 Verify the no-regression invariants: confirm no new file exists under
      `internal/storage/migrations/` for this milestone (Req 3.2, 9.2, mirroring
      M8/M9a/M9b/M10a/M10.B); confirm the `GET /users`, `GET /users/me`, and
      `GET /users/{id}` read handlers, `mapRESTUser`, and `restUserContext` are
      unchanged (Req 9.1); confirm `DELETE /users` and `DELETE /users/{id}` still
      answer `501` (Req 8).
      Files: (verification only) `internal/storage/migrations/`,
      `internal/web/rest_users.go`
      Verify: `git status --porcelain internal/storage/migrations` shows no new
      files; `go test ./internal/web` green on the read/DELETE assertions.
      _(Req 3.2, 8.1, 8.2, 8.3, 9.1, 9.2)_

- [x] 7.4 Run the full gate suite and fix anything red: `gofmt -l .` empty,
      `go vet ./...`, `go build ./...`, `go test ./...`. Confirm no test added by
      this milestone requires a database, network, or live WordPress, and that
      `go test ./...` needs no new `GRIMOIRE_TEST_*` env vars beyond the existing
      storage harness (SQLite always).
      Files: (whole tree, verification only)
      Verify: all four commands green; no new `GRIMOIRE_TEST_*` env var required.
      _(Req 9.1, 12.3)_

## Notes

- Each task references the exact Requirement/AC, Finding, and Inherited decision
  it satisfies, for traceability back to `requirements.md` and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt`/`vet`/`build`/`test` after
  every phase, matching M10a's and M10.B's close-out discipline.
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the
  sub-task that writes its failing test (Req 12.3).
- No sub-task is marked optional (`*`): this milestone's tests are the
  fail-closed safety and capability guarantees (Req 11, 12), not nice-to-haves,
  so each is a core task. (This matches M10.B, whose write-boundary tests were
  likewise unmarked.)
- No property-based tests: 10.C is a REST write surface over existing user
  machinery; its sanitization *is* M10a's already-property-tested
  `sanitize.Policy` (its idempotence/allow-list/tier-C-identity properties are
  covered in `internal/sanitize` and are not re-proved here), and the field-kind
  choice (`description` → `PostContent`) is a fixed decision, not a property
  (design "Testing strategy").

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "3.1"] },
    { "id": 1, "tasks": ["1.2", "3.2"] },
    { "id": 2, "tasks": ["2.1"] },
    { "id": 3, "tasks": ["2.2", "4.1"] },
    { "id": 4, "tasks": ["4.2"] },
    { "id": 5, "tasks": ["5.1"] },
    { "id": 6, "tasks": ["5.2"] },
    { "id": 7, "tasks": ["5.3"] },
    { "id": 8, "tasks": ["5.4"] },
    { "id": 9, "tasks": ["5.5"] },
    { "id": 10, "tasks": ["6.1"] },
    { "id": 11, "tasks": ["6.2"] },
    { "id": 12, "tasks": ["7.1", "7.3"] },
    { "id": 13, "tasks": ["7.2"] },
    { "id": 14, "tasks": ["7.4"] }
  ]
}
```
