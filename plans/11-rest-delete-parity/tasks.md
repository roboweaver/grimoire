# Implementation Plan: M11 — REST delete parity (media + users)

## Overview

> **This milestone closes the last two REST write stubs** — `DELETE /media/{id}`
> (`internal/web/rest_media.go:51`) and `DELETE /users/{id}`
> (`internal/web/rest_users.go:50`), both still `restNotImplemented` `501`s —
> and is the first entry beyond the completed M1–M10 WordPress-core-parity
> roadmap (`../post-m10-gap-analysis/README.md`, "M11 — REST delete parity
> (media + users)"). It builds directly on M10.B (`../10.1-rest-media-write`)
> and M10.C (`../10.2-rest-user-write`), which turned on REST create/update for
> attachments and users and deliberately left `DELETE` behind; `DELETE` is the
> one remaining deferred write verb for both endpoints.
>
> **What this adds — and only this.** Two additive domain `Delete` ports
> (`MediaWriter.Delete(ctx, id)` and `UserRepository.Delete(ctx, id, reassignTo)`,
> D1) plus their `wprepo` implementations; one new `Delete` method on each
> existing content service (`MediaWriteService.Delete`, `UserService.Delete`);
> and two nil-gated REST handlers (`handleRESTMediaDelete`, `handleRESTUserDelete`)
> replacing the two `501` stubs. Delete follows WordPress's own force/trash
> contract: neither attachments nor users trash, so both routes require
> `?force=true` (its absence is `400 rest_trash_not_supported`, never a trash,
> Req 2/6). The media hard delete removes the attachment's `{prefix}posts` row,
> its `{prefix}postmeta`, its `{prefix}term_relationships`, **and the on-disk
> file** (`_wp_attached_file` under `UploadsDir`) — a best-effort unlink,
> DB-delete-first, with a missing file non-fatal (Req 4, D2). The user hard
> delete requires a `reassign` parameter and performs the reassign-or-delete of
> the user's authored content plus the `{prefix}users`/`{prefix}usermeta` removal
> in one `wprepo` transaction (Req 9, D4/D6); `reassign=false` cascades each
> authored post's `{prefix}postmeta` and `{prefix}term_relationships` so the
> delete branch leaves no orphan.
>
> **No migration, no new capability, read ports unchanged.** Every affected table
> already exists (Req 10.3); media delete reuses `auth.CanDeletePost` keyed off
> the stored `post_author`, user delete reuses `auth.CanDeleteUsers` (Req 3/8,
> D3/D5) — no new capability name (Req 11.5). The read ports
> (`domain.MediaRepository`, and `UserRepository`'s read methods) are untouched
> (Req 10.4); the only interface additions are the two `Delete` methods, and the
> existing `var _ domain.MediaWriter = (*MediaRepo)(nil)` /
> `var _ domain.UserRepository = (*UserRepo)(nil)` compile assertions stay
> satisfied once the impls land (no new assertion needed, Finding 1). The delete
> handlers are nil-gated exactly as create/update are: a `Server` built without a
> write-capable media/user service keeps serving the `501` stub rather than
> panicking (Req 10.5).
>
> **Standing gate convention.** Keep `gofmt -l .` empty and `go vet ./...`,
> `go build ./...`, `go test ./...` green after every phase, following the
> close-out discipline of M10a/M10.B/M10.C/M10.D/M10.F.
>
> **Strict-TDD spine.** Every behavior-adding sub-task is preceded by the
> sub-task that writes its failing test, matching how M5–M10.F were run and how
> 10.3 (`../10.3-rest-content-rendered`) and 10.5
> (`../10.5-nav-menu-editing`) are ordered. The one exception is the pure
> interface additions of Phase 1 (no behavior to test in isolation); their
> consumers' tests in Phases 2–4 exercise them, mirroring how 10.3/10.5 treat
> pure port additions.
>
> **The Phase-1 compile break, handled deliberately.** Adding `Delete` to
> `domain.MediaWriter` makes `wprepo.MediaRepo` fail its interface assertion AND
> breaks the existing `fakeMediaWriter` in `internal/content/media_test.go`
> (which implements `domain.MediaWriter`) until each gains a `Delete` method; the
> user track has no fake writer to update (its service tests use the real
> `wprepo.UserRepo` over SQLite). A failing-test task that cannot compile because
> a fake lacks the new method is **not** the intended TDD red — so the media
> wprepo impl task (2.3) adds `fakeMediaWriter.Delete` alongside the real impl,
> and Phase 1 is sequenced in its own early wave with the wprepo impls landing in
> the very next waves so the tree compiles again. This is called out on the tasks
> below.

## Tasks

## Phase 1 — Domain Delete ports (`internal/domain`, pure additions)

- [ ] 1.1 Add the two `Delete` port methods. In `internal/domain/repository.go`
      add `Delete(ctx context.Context, id int64) error` to the `MediaWriter`
      interface, beside the existing `Create`/`SetParent`/`UpdateMeta`, and
      `Delete(ctx context.Context, id int64, reassignTo int64) error` to the
      `UserRepository` interface, beside the existing
      `ByLogin`/`ByNicename`/`ByID`/`Create`/`UpdatePass`/`UpdateProfile`/`ByEmail`/
      `List`/`Count`. Use the design's exact doc comments: `MediaWriter.Delete`
      hard-deletes the attachment's `{prefix}term_relationships`, `{prefix}postmeta`,
      and `{prefix}posts` rows in one transaction and returns `domain.ErrNotFound`
      for an unknown id (it does NOT touch the on-disk file — that is the
      service's job); `UserRepository.Delete` reassigns (`reassignTo > 0`) or
      deletes (`reassignTo == 0`) the user's authored content BEFORE removing the
      user's usermeta and users rows, all transactional, and returns
      `domain.ErrNotFound` for an unknown id. Add NO new `var _` assertion (the
      existing `MediaWriter`/`UserRepository` assertions in
      `internal/storage/wprepo/media.go:14`/`users.go:13` already cover the
      concrete types once their impls land, Finding 1). The read ports
      (`MediaRepository`, `UserRepository`'s read methods) are left UNCHANGED
      (Req 10.4). No behavior to test in isolation; the consuming tests arrive in
      Phases 2–4. **NOTE:** this breaks compilation of `wprepo.MediaRepo`,
      `wprepo.UserRepo`, and the `fakeMediaWriter` in
      `internal/content/media_test.go` until Phase 2's impls (and 2.3's fake
      update) land — sequenced immediately after in the waves below.
      Files: `internal/domain/repository.go`
      Verify: (compile-only; the tree is intentionally red until 2.2/2.4 — do not
      run `go build` as a gate for this task, it gates at the end of Phase 2.)
      _(Req 10.1, 10.2, 10.4; D1)_

## Phase 2 — `wprepo` Delete implementations (`internal/storage/wprepo`, TDD, SQLite)

- [ ] 2.1 Write the failing media storage test first. Add
      `internal/storage/wprepo/media_write_test.go` (or extend the existing media
      test) exercising `MediaRepo.Delete` against an in-memory SQLite DB via the
      existing wprepo test harness (pure Go, no new env var). Seed an attachment:
      a `{prefix}posts` row (`post_type="attachment"`), its `_wp_attached_file`
      and `_wp_attachment_image_alt` `{prefix}postmeta` rows, and a
      `{prefix}term_relationships` row (`object_id` = the attachment's post ID,
      Finding 5). Assert, per the design's Testing strategy: after `Delete(id)`,
      zero `{prefix}posts`, zero `{prefix}postmeta`, and zero
      `{prefix}term_relationships` rows remain for the id and a read-back returns
      `domain.ErrNotFound` (Req 4.5); a second `Delete` of the same id returns
      `domain.ErrNotFound` (Req 4.4); and the delete is transactional (no partial
      teardown survives a failure).
      Files: `internal/storage/wprepo/media_write_test.go`
      Verify: `go test ./internal/storage/wprepo` — fails (`MediaRepo.Delete`
      absent). _(Req 4.1, 4.4, 4.5, 12.5)_

- [ ] 2.2 Write the failing user storage test first. Add
      `internal/storage/wprepo/users_write_test.go` exercising `UserRepo.Delete`
      against an in-memory SQLite DB via the existing wprepo harness. Seed a user
      with authored `{prefix}posts` rows (each with their `{prefix}postmeta` and
      `{prefix}term_relationships`) and the user's `{prefix}usermeta`. Assert, per
      the design's Testing strategy: `Delete(id, target>0)` moves every authored
      post's `post_author` to `target` and leaves those posts' meta/relationships
      intact, and removes the `{prefix}users` row and its `{prefix}usermeta`
      (Req 9.1); `Delete(id, 0)` deletes the authored posts AND their
      `{prefix}postmeta` and `{prefix}term_relationships` (no orphans — the
      cascade decision) and removes the user + usermeta (Req 9.2); the whole
      reassign/delete + user removal is one transaction — an injected
      mid-transaction failure (e.g. forced error on the final users delete) rolls
      everything back so the user, usermeta, and authored content are exactly as
      before (Req 9.3, 9.4); and `Delete` of an unknown id returns
      `domain.ErrNotFound` (Req 9.5).
      Files: `internal/storage/wprepo/users_write_test.go`
      Verify: `go test ./internal/storage/wprepo` — fails (`UserRepo.Delete`
      absent). _(Req 9.1, 9.2, 9.3, 9.4, 9.5, 12.5)_

- [ ] 2.3 Implement `MediaRepo.Delete` and update the fake writer. Add
      `MediaRepo.Delete` in `internal/storage/wprepo/media.go` (or a sibling
      `media_write.go`) exactly per design, mirroring `TermRepo.Delete`
      (`writers.go:172`): one `RunInTx`, raw `tx.ExecContext` with `?`
      placeholders, deepest-dependency-first — (1) `DELETE FROM {prefix}term_relationships
      WHERE object_id = ?`, (2) `DELETE FROM {prefix}postmeta WHERE post_id = ?`,
      (3) `DELETE FROM {prefix}posts WHERE {ID} = ?` with
      `{ID} = rebind.Ident(vendorOf(r.db), "ID")` and `errNotFoundIfZero(res)` on
      the final delete (Req 4.1, 4.4). The posts delete is keyed on the primary
      key alone (no `post_type='attachment'` guard), matching `PostWriter.Delete`;
      the `term_relationships`/`postmeta` deletes filter on
      `object_id`/`post_id` and need no `"ID"` rebinding. **Also add
      `fakeMediaWriter.Delete` in `internal/content/media_test.go`** (record the
      deleted id / return a canned error, mirroring the existing
      `Create`/`SetParent`/`UpdateMeta` fakes) so `domain.MediaWriter` is
      satisfied by the fake again — without it the Phase-3 media service test
      cannot compile (this is the deliberate fake update, not a behavior change).
      Files: `internal/storage/wprepo/media.go`, `internal/content/media_test.go`
      Verify: `go test ./internal/storage/wprepo` — the 2.1 tests pass;
      `go build ./...` compiles again (interface assertions + fake satisfied);
      `go vet ./...` clean. _(Req 4.1, 4.4, 4.5, 10.1; D1, D2)_

- [ ] 2.4 Implement `UserRepo.Delete`. Add `UserRepo.Delete` in
      `internal/storage/wprepo/users.go` (or a sibling `users_write.go`) exactly
      per design: one `RunInTx` with the branches in the design's order. For
      `reassignTo > 0`: `UPDATE {prefix}posts SET post_author = ? WHERE
      post_author = ?` (Req 9.1). For `reassignTo == 0`: `SELECT {ID} FROM
      {prefix}posts WHERE post_author = ?` (with `{ID} = rebind.Ident(vendorOf(r.db),
      "ID")`), collect the post IDs, then for each delete its
      `{prefix}term_relationships` (`object_id`) and `{prefix}postmeta`
      (`post_id`), then `DELETE FROM {prefix}posts WHERE post_author = ?`
      (Req 9.2, the no-orphan cascade). In both branches then `DELETE FROM
      {prefix}usermeta WHERE user_id = ?`, and finally `DELETE FROM {prefix}users
      WHERE {ID} = ?` (again `rebind.Ident(...,"ID")`) with
      `errNotFoundIfZero(res)` (Req 9.5). `post_author`/`user_id`/`object_id`/
      `post_id` are lowercase columns needing no rebinding; only the two `"ID"`
      references are rebound. Add NO new `var _` assertion (the existing
      `UserRepository` assertion covers `*UserRepo`).
      Files: `internal/storage/wprepo/users.go`
      Verify: `go test ./internal/storage/wprepo` — the 2.2 tests pass;
      `go build ./...` compiles; `go vet ./...` clean. _(Req 9.1, 9.2, 9.3, 9.4,
      9.5, 10.2; D1, D6)_

## Phase 3 — Content service Delete methods (`internal/content`, TDD)

- [ ] 3.1 Write the failing media service test first. In
      `internal/content/mediawriteservice_test.go` (reusing the shared
      `fakeMediaWriter`/`fakeMediaRepo` and a real `content.MediaService` over a
      temp uploads dir, as the existing update tests do) add table-driven tests
      for `MediaWriteService.Delete(ctx, actor, id)`. Assert, per the design's
      Testing strategy: an own-attachment delete is allowed with `delete_posts`
      and forbidden without it (`ErrForbidden`, nothing deleted) — ownership keyed
      off the STORED `post_author` seeded in `fakeMediaRepo.byID`, status
      `"inherit"` (Req 3.1); an other's-attachment delete requires
      `delete_others_posts` (Req 3.2); `domain.ErrNotFound` bubbles from an
      unknown id (Req 1.4/4.4); the returned `previous` equals the loaded stored
      row, captured BEFORE the delete (Req 1.3); on success the on-disk file at
      `filepath.Join(UploadsDir, FromSlash(cur.Filename))` is unlinked (Req 4.2);
      a pre-removed file is non-fatal — the delete still succeeds (Req 4.3); and
      the DB delete runs BEFORE the unlink — a forced `fakeMediaWriter` delete
      error returns the error and leaves the on-disk file untouched (the
      DB-delete-first order).
      Files: `internal/content/mediawriteservice_test.go`
      Verify: `go test ./internal/content` — fails (`MediaWriteService.Delete`
      absent). _(Req 1.3, 1.4, 3.1, 3.2, 3.4, 4.2, 4.3, 4.4; D2, D3)_

- [ ] 3.2 Write the failing user service test first. In
      `internal/content/userservice_test.go` (building a `UserService` over the
      real `wprepo.UserRepo`/`UserMetaRepo` on an in-memory SQLite DB, as the
      existing user tests do — there is no user-repo fake to update) add tests for
      `UserService.Delete(ctx, actor, id, reassignTo)`. Assert, per the design's
      Testing strategy: the `delete_users` gate allows an administrator and
      forbids a non-administrator (`ErrForbidden`, nothing deleted, Req 8.1 —
      `delete_users` is administrator-only, Finding 6); `domain.ErrNotFound`
      bubbles from an unknown id (Req 5.4/9.5); the returned `previous` equals the
      loaded stored user, captured BEFORE the delete (Req 5.3); and `reassignTo`
      is passed through to the repo unchanged for both the `reassign=false` (0)
      and `reassign=<id>` (>0) branches (observed via the authored-content
      outcome). The service owns ONLY the `delete_users` gate and the
      `previous`-capture; self-delete and reassign-target existence are handler
      concerns (Finding 4), not asserted here.
      Files: `internal/content/userservice_test.go`
      Verify: `go test ./internal/content` — fails (`UserService.Delete` absent).
      _(Req 5.3, 5.4, 8.1, 9.5; D4, D5)_

- [ ] 3.3 Implement `MediaWriteService.Delete`. Add the `Delete` method on
      `MediaWriteService` in `internal/content/mediawriteservice.go`, beside
      `Create`/`Update`, reusing the struct's existing `media`/`reader`/`writer`
      fields (no constructor or option change). Exactly per design: load the
      stored row via `s.reader.ByID(ctx, id)` (bubbling `domain.ErrNotFound`,
      Req 1.4); authorize against the STORED `post_author` with
      `auth.CanDeletePost(actor, "post", "inherit", cur.AuthorID)` → `ErrForbidden`
      on failure (fail closed, nothing deleted, Req 3); DB delete FIRST via
      `s.writer.Delete(ctx, id)`; THEN best-effort unlink — when
      `cur.Filename != ""`, `os.Remove(filepath.Join(s.media.Config().UploadsDir,
      filepath.FromSlash(cur.Filename)))`, treating `os.IsNotExist` (and any
      unlink error after the committed DB delete) as non-fatal (Req 4.2, 4.3);
      return `cur` as the `previous` object (Req 1.3). Capture `cur` (and thus
      `Filename`) BEFORE the delete, since `MediaWriter.Delete` removes the
      `_wp_attached_file` postmeta row (Finding 3). Import `os` and `path/filepath`
      as needed.
      Files: `internal/content/mediawriteservice.go`
      Verify: `go test ./internal/content` — the 3.1 tests pass; `go vet ./...`
      clean. _(Req 1.3, 1.4, 3.1, 3.2, 3.3, 3.4, 4.1, 4.2, 4.3, 4.4; D2, D3)_

- [ ] 3.4 Implement `UserService.Delete`. Add the `Delete` method on
      `UserService` in `internal/content/userservice.go`, beside
      `Create`/`UpdateProfile`/`SetPassword`/`SetRoles`, reusing the existing
      `users domain.UserRepository` field. Exactly per design: gate on
      `auth.CanDeleteUsers(actor)` → `ErrForbidden` on failure (nothing deleted,
      Req 8.1); load `prev` via `s.users.ByID(ctx, id)` (bubbling
      `domain.ErrNotFound`, Req 5.4) BEFORE the delete; delegate the transactional
      reassign/delete to `s.users.Delete(ctx, id, reassignTo)` passing the
      already-validated `reassignTo` straight through (Req 9); return `prev` as
      the `previous` object (Req 5.3). The service does NOT re-derive identity,
      check self-delete, or validate reassign-target existence — those are handler
      concerns (Finding 4).
      Files: `internal/content/userservice.go`
      Verify: `go test ./internal/content` — the 3.2 tests pass; `go vet ./...`
      clean. _(Req 5.3, 5.4, 8.1, 9.5; D4, D5)_

## Phase 4 — Web handlers + shared helpers (`internal/web`, TDD, nil-gated)

- [ ] 4.1 Write the failing media handler + wiring tests first. Add
      `internal/web/rest_media_delete_test.go` and a nil-gated wiring assertion
      (extend the existing media wiring test, modeled on
      `mediawrite_wiring_test.go`). Drive the live chi router with real content
      services over SQLite. Assert, per the design's Testing strategy: the
      capability matrix — an own-attachment delete with/without `delete_posts` and
      an other's-attachment delete with/without `delete_others_posts`, plus an
      anonymous caller → `403 rest_cannot_delete` (Req 3, 12.1);
      `force`-required — `DELETE /media/{id}` without `force=true` → `400
      rest_trash_not_supported`, stored state unchanged (Req 2.1, 12.2); a
      non-positive / non-integer `{id}` → `404 rest_post_invalid_id` and an
      unknown id → `404 rest_post_invalid_id`, nothing deleted (Req 1.4, 11.3); a
      successful delete returns `200` with `{"deleted": true, "previous": <object>}`
      whose `previous` matches the `mapRESTMedia` envelope `GET /media/{id}`
      returned immediately before the delete (Req 1.2, 12.4); CSRF — a session
      request without a valid `X-CSRF-Token` → `403 rest_forbidden` and an
      Application Password request skips CSRF (Req 11.1, 11.2); and the nil-gate —
      a `Server` built WITHOUT `WithMediaWrites` still serves the `501` stub on
      `DELETE /media/{id}` (Req 10.5).
      Files: `internal/web/rest_media_delete_test.go`,
      `internal/web/mediawrite_wiring_test.go`
      Verify: `go test ./internal/web` — fails (`handleRESTMediaDelete`,
      `restForceDelete`, `restDeleteEnvelope` absent). _(Req 1.2, 1.4, 2.1, 3.1,
      3.2, 3.4, 10.5, 11.1, 11.2, 11.3, 12.1, 12.2, 12.4)_

- [ ] 4.2 Write the failing user handler + wiring tests first. Add
      `internal/web/rest_users_delete_test.go` and a nil-gated wiring assertion
      (extend the existing user wiring test, modeled on
      `userwrite_wiring_test.go`). Drive the live chi router with real content
      services over SQLite; use an administrator principal for the allowed case
      (Finding 6). Assert, per the design's Testing strategy: the capability
      matrix — a delete with `delete_users` (admin) allowed, without it (non-admin)
      → `403 rest_user_cannot_delete`, a self-delete (`{id}` == caller's id) →
      `403 rest_user_cannot_delete` even for an admin, and anonymous → `403`
      (Req 8, 12.1); `force`-required — `DELETE /users/{id}` without `force=true`
      → `400 rest_trash_not_supported`, stored state unchanged (Req 6.1, 12.2);
      the `reassign` contract — a missing `reassign`, an unparseable `reassign`,
      and `reassign=<non-existent id>` each → `400 rest_user_invalid_reassign`,
      while `reassign=false` and `reassign=<existing id>` each succeed (Req 7,
      12.3); a non-positive / non-integer `{id}` and an unknown id →
      `404 rest_user_invalid_id`, nothing deleted (Req 5.4, 11.4); a successful
      delete returns `200` with `{"deleted": true, "previous": <object>}` whose
      `previous` matches the edit-context `mapRESTUser` envelope `GET /users/{id}`
      returned before the delete (Req 5.2, 12.4); CSRF parity as for media
      (Req 11.1, 11.2); and the nil-gate — a `Server` built WITHOUT
      `WithUserWrites` still serves the `501` stub on `DELETE /users/{id}`
      (Req 10.5).
      Files: `internal/web/rest_users_delete_test.go`,
      `internal/web/userwrite_wiring_test.go`
      Verify: `go test ./internal/web` — fails (`handleRESTUserDelete`,
      `parseRESTReassign` absent). _(Req 5.2, 5.4, 6.1, 7.1, 7.2, 7.3, 7.4, 7.5,
      8.1, 8.3, 8.4, 10.5, 11.1, 11.2, 11.4, 12.1, 12.2, 12.3, 12.4)_

- [ ] 4.3 Implement the shared delete helpers. Add
      `internal/web/rest_delete.go` holding the three shared transport constructs
      exactly per design's Data Models, in ONE file so neither handler task
      redefines them: `restForceDelete(r *http.Request) bool` (true only for a
      `force` query value of `"true"` or `"1"`, read from the query string only,
      Req 2.1, 2.3, 6.1); `parseRESTReassign(r *http.Request) (int64, bool)`
      (`""` → `(0, false)` required; `"false"` → `(0, true)` delete-content; a
      parseable `n > 0` → `(n, true)` candidate id; anything else incl. `"true"`,
      `0`, negative, non-integer → `(0, false)` invalid — the bool is "valid", not
      "present", Req 7.1, 7.2, 7.4, 7.5); and
      `restDeleteEnvelope(previous map[string]any) map[string]any` returning
      `{"deleted": true, "previous": previous}` as a `map[string]any` so the
      nested envelope serializes unchanged through `writeRESTResponse` (Req 1.2,
      5.2). Import `net/http` and `strconv`.
      Files: `internal/web/rest_delete.go`
      Verify: `go build ./...` compiles; `go vet ./...` clean. (The 4.1/4.2 tests
      still fail until the handlers land in 4.4/4.5.) _(Req 1.2, 2.1, 2.3, 5.2,
      6.1, 7.1, 7.2, 7.4, 7.5)_

- [ ] 4.4 Implement the media delete handler + nil-gate. Add
      `handleRESTMediaDelete` in `internal/web/rest_media.go` exactly per design:
      the create/update auth preamble verbatim (Application Password skips CSRF;
      a session enforces `requireSessionCSRFREST`); `PrincipalFrom(r.Context())`;
      parse `{id}` (non-positive/non-integer → `404 rest_post_invalid_id`,
      Req 11.3); `restForceDelete` gate (absent → `400 rest_trash_not_supported`,
      Req 2.1); load the stored attachment via `s.restMedia.ByID` and map it with
      `s.mapRESTMedia(r, cur)` to compute `previous` BEFORE the delete (a
      `domain.ErrNotFound` here → `404 rest_post_invalid_id`, Req 1.3, 1.4);
      call `s.mediaWrite.Delete(r.Context(), principal, id)` mapping
      `domain.ErrNotFound` → `404 rest_post_invalid_id`, `content.ErrForbidden` →
      `403 rest_cannot_delete` (Req 3, 3.4), else `500 rest_delete_failed`; on
      success `writeRESTResponse(w, r, http.StatusOK, restDeleteEnvelope(previous))`
      (Req 1.2). Then edit `registerRESTMedia`: replace the unconditional
      `DELETE /media/{id}` `501` stub (`rest_media.go:51`) with a nil-gate —
      `if s.mediaWrite != nil { r.Method(http.MethodDelete, "/media/{id}",
      s.handleRESTMediaDelete()) } else { ...the existing restNotImplemented
      stub... }` (fold into the existing `if s.mediaWrite != nil` block or keep it
      adjacent; either satisfies Req 10.5).
      Files: `internal/web/rest_media.go`
      Verify: `go test ./internal/web` — the 4.1 tests pass; `go vet ./...` clean.
      _(Req 1.1, 1.2, 1.3, 1.4, 2.1, 2.2, 2.3, 3.1, 3.2, 3.4, 10.5, 11.1, 11.2,
      11.3; D2, D3)_

- [ ] 4.5 Implement the user delete handler + nil-gate. Add
      `handleRESTUserDelete` in `internal/web/rest_users.go` exactly per design:
      the create/update auth preamble verbatim; `PrincipalFrom(r.Context())`;
      parse `{id}` (non-positive/non-integer → `404 rest_user_invalid_id`,
      Req 11.4); `restForceDelete` gate (absent → `400 rest_trash_not_supported`,
      Req 6.1); self-delete guard — `id == principal.UserID` →
      `403 rest_user_cannot_delete` regardless of capability, decided against
      `{id}` and the resolved principal (Req 8.3, 8.5); `parseRESTReassign` —
      `!ok` → `400 rest_user_invalid_reassign` (Req 7.1, 7.2); reassign-target
      existence — when `reassignTo > 0`, `s.restUsers.ByID(r.Context(), reassignTo)`,
      a `domain.ErrNotFound` → `400 rest_user_invalid_reassign` (Req 7.3), other
      errors → `500 rest_user_delete_failed`; load the stored user via
      `s.restUsers.ByID` and map with `s.mapRESTUser(r, cur, content.RESTContextEdit)`
      to compute `previous` BEFORE the delete (a `domain.ErrNotFound` →
      `404 rest_user_invalid_id`, Req 5.3, 5.4); call
      `s.userWrite.Delete(r.Context(), principal, id, reassignTo)` mapping
      `content.ErrForbidden` → `403 rest_user_cannot_delete` (Req 8.1),
      `domain.ErrNotFound` → `404 rest_user_invalid_id`, else
      `500 rest_user_delete_failed`; on success
      `writeRESTResponse(w, r, http.StatusOK, restDeleteEnvelope(previous))`
      (Req 5.2). Then edit `registerRESTUsers`: replace the `DELETE /users/{id}`
      `501` stub inside the existing `if s.userWrite != nil` block
      (`rest_users.go:50`) with `r.Method(http.MethodDelete, "/users/{id}",
      s.handleRESTUserDelete())`; the `else` arm already keeps the stub (Req 10.5).
      Files: `internal/web/rest_users.go`
      Verify: `go test ./internal/web` — the 4.2 tests pass; `go vet ./...` clean.
      _(Req 5.1, 5.2, 5.3, 5.4, 6.1, 6.2, 7.1, 7.2, 7.3, 7.4, 7.5, 8.1, 8.2, 8.3,
      8.4, 8.5, 10.5, 11.1, 11.2, 11.4; D4, D5, D6, D7)_

## Phase 5 — Main wiring check + final gates (`cmd/grimoire`)

- [ ] 5.1 Verify wiring and run the full gate suite. Confirm the two delete
      handlers are reachable through the already-wired `mediaWrite`/`userWrite`
      services — `cmd/grimoire/main.go` already constructs
      `content.NewMediaWriteService(...)` and `content.NewUserService(...)` and
      chains `.WithMediaWrites(...).WithUserWrites(...)` onto `web.NewServer(...)`,
      so the new `Delete` methods ride the existing wiring and NO `main.go` change
      is expected — read `main.go` to confirm and note it (if a wiring gap is
      found, close it minimally, injecting the same services). Then verify the
      no-regression invariants and run the gate: confirm NO new file under
      `internal/storage/migrations` (`git status --porcelain
      internal/storage/migrations` shows none, Req 10.3, mirroring M10.B/M10.C);
      confirm the read ports are unchanged — `domain.MediaRepository` and
      `UserRepository`'s read methods, and `wprepo`'s read paths, gained only the
      two `Delete` methods (Req 10.4); confirm no new capability name was
      introduced and no `wp-json` route beyond the two `DELETE` verbs (Req 11.5).
      Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`
      and fix anything red, confirming no test added by this milestone needs a
      database, network, live WordPress, or a new `GRIMOIRE_TEST_*` env var, and
      no new dependency (pure Go, SQLite-only, Req 12.6).
      Files: (verification only) `cmd/grimoire/main.go`,
      `internal/storage/migrations/`, `internal/domain/repository.go`
      Verify: `go build ./...` and `go test ./cmd/...` green; `git status
      --porcelain internal/storage/migrations` shows no new files; all four gate
      commands green with no new `GRIMOIRE_TEST_*` env var and no new dependency.
      _(Req 10.3, 10.4, 10.5, 11.5, 12.6; D1, D7)_

## Notes

- Each task cites the exact Requirement/AC, the Inherited decision (D1–D8), and
  the design Finding it satisfies, for traceability back to `requirements.md`
  and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt -l .` empty and `go vet
  ./...`, `go build ./...`, `go test ./...` green after every phase, matching
  M10a/M10.B/M10.C/M10.D/M10.F's close-out discipline (Req 12.6). The one
  exception is Phase 1 itself, which intentionally leaves the tree red (the port
  additions break `wprepo` and `fakeMediaWriter` until Phase 2's impls + 2.3's
  fake update land); the gate resumes at the end of Phase 2.
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the
  sub-task that writes its failing test (2.1→2.3, 2.2→2.4, 3.1→3.3, 3.2→3.4,
  4.1→4.4, 4.2→4.5; the shared helpers in 4.3 land before the handlers that use
  them). Phase 1 is the sole pure-additions task (two interface methods, no
  isolated behavior); its consumers' tests in Phases 2–4 exercise it, and the
  existing `var _ domain.MediaWriter`/`var _ domain.UserRepository` compile
  assertions cover the impls once they land — no new assertion is added,
  mirroring how 10.3/10.5 treat pure port additions.
- A failing-test task that cannot compile because a fake lacks the new method is
  NOT the intended TDD red. The Phase-1 addition breaks `fakeMediaWriter` in
  `internal/content/media_test.go`; task 2.3 adds `fakeMediaWriter.Delete`
  alongside the real wprepo impl so the Phase-3 media service test compiles. The
  user track has no fake writer to update — its service tests use the real
  `wprepo.UserRepo` over SQLite — so no user-side fake change is needed.
- **No sub-task is optional** (none is postfixed with `*`). The capability-matrix,
  force-contract, reassign-contract, envelope, and storage round-trip tests are
  the milestone's done-definition for the two delete surfaces (Req 12), and the
  nil-gated wiring tests prove the fail-closed, no-orphan, and read-unchanged
  invariants — each is a core task, matching M10.B/M10.C/M10.F, whose tests were
  likewise unmarked.
- **No property-based tests.** Both delete surfaces are fixed-verb endpoints
  whose behavior is a small set of enumerable branches (force present/absent;
  own/others/anonymous capability; reassign false/valid-id/invalid/missing;
  self vs other; file present/absent), not a pure function over a large input
  space — there is no meaningful "for all inputs X, P(X)" to generate against.
  Coverage is example + integration tests per the design's Testing strategy —
  pure Go, SQLite-only, no new env var or external dependency — matching the
  no-PBT decision in M10.B/M10.C.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["2.1", "2.2"] },
    { "id": 2, "tasks": ["2.3", "2.4"] },
    { "id": 3, "tasks": ["3.1", "3.2"] },
    { "id": 4, "tasks": ["3.3", "3.4"] },
    { "id": 5, "tasks": ["4.1", "4.2"] },
    { "id": 6, "tasks": ["4.3"] },
    { "id": 7, "tasks": ["4.4", "4.5"] },
    { "id": 8, "tasks": ["5.1"] }
  ]
}
```
