# Implementation Plan: M10.B — REST Media Write Parity

## Overview

Implementation checklist for M10.B (roadmap group **10.B only**). Tasks are
ordered so each builds on the last, and each references the exact files, the
exact symbols named in `design.md`'s "Components and Interfaces" / "Data Models"
sections, and the Requirement/AC plus the Finding (1–3) and Inherited decision
(D1–D7) it satisfies. Keep `gofmt -l .` empty and `go vet ./...`,
`go build ./...`, `go test ./...` green after every phase.

Strict TDD: within each phase the failing test is written **before** the
implementation that passes it, matching how M5–M10a were run. The ordering below
makes that spine visible — every behavior-adding sub-task is preceded by the
sub-task that writes its failing test (Req 11).

> **This refines roadmap group 10.B only.** It is the first consumer to turn on
> a new REST write surface, tracing to roadmap **Requirement 16**. Roadmap
> groups **10.C–10.G remain open** and are explicitly out of scope: REST user
> writes stay `501`, `content.rendered` is untouched (Req 10.2), and
> `DELETE /media[/{id}]` stays `501` (Req 9). It reuses M10a's `internal/sanitize`
> Policy without modification.
>
> **No task here authorizes a migration.** Every target field already lives in
> an existing column (`post_title`/`post_excerpt`/`post_content`/`post_author`
> on `{prefix}posts`, present since migration `0001`) or in the already-used
> `{prefix}postmeta` table (the `_wp_attachment_image_alt` key). Task 8.3
> verifies explicitly that no file is added under `internal/storage/migrations`,
> as M8/M9a/M9b/M10a did (Req 3.3). Unlike M10a, this milestone **does** add a
> cross-vendor storage contract test, because `UpdateMeta` is vendor-touching SQL
> (tasks 2.x).
>
> **Three design Findings change scope and are reflected in these tasks.**
> *Finding 1* — `http.MaxBytesReader` is a transport construct, so the size cap
> is enforced in the handler, not in `MediaWriteService`; the `ErrUploadTooLarge`
> sentinel is a belt-and-braces service check with no HTTP caller today (tasks
> 3.x, 6.x). *Finding 2* — `domain.Media` carries none of the editable metadata
> fields today, so the write input is a dedicated `domain.MediaMeta` value object
> **and** `domain.Media` grows read-side `Caption`/`AltText`/`Description` so an
> update is observable on a later GET (tasks 1.x, 2.x, 5.x). *Finding 3* —
> `RESTMapper.Media` does not surface caption/description/alt today, so the read
> path is extended end to end (repo read + mapper emission) to satisfy Req 10.3
> (tasks 2.x, 5.x); this is the one place 10.B touches the read surface and it is
> additive (new JSON keys), so no existing read assertion regresses (Req 10.1).
>
> **Deliberate reuse postures carried from M10a.** The content policy defaults to
> `sanitize.New()` and is **never nil** (an unwired service sanitizes rather than
> silently passing input through, fail closed). The update path reuses
> `PostWriteService`'s byte-identity skip and stored-row capability evaluation.
> Create is binary-only (D1): the Policy is **not** invoked on create (Req 1.3) —
> the one caller-supplied string is the `Content-Disposition` filename, which
> `MediaService.Store` already sanitizes. No new capability name is introduced
> (D4); `auth.CanEditPost(actor, "post", "inherit", stored.AuthorID)` is the
> ownership helper, used as-is.
>
> **Interface growth breaks callers until updated.** Adding `UpdateMeta` to the
> `domain.MediaWriter` interface (task 1.2) breaks every fake/stub implementing
> that port until they gain the method; adding the `Author` field to
> `content.MediaUpload` (task 1.4) is additive and breaks nothing; adding the
> read-side `Media` fields (task 1.1) is additive. Each task that introduces such
> a break notes it inline, as M10a's tasks did.

## Tasks

## Phase 1 — Domain port and value objects (pure, no DB, no HTTP)

- [x] 1.1 Add the read-side fields and the write-side value object to the domain,
      failing-test-first. Write failing tests in `internal/domain/entities_test.go`
      (new or existing) asserting `domain.Media` carries `Caption`, `AltText`, and
      `Description` string fields (zero-valued for an existing reader), and that a
      new `domain.MediaMeta` value object exists with exactly `Title`, `Caption`,
      `AltText`, `Description` string fields and nothing else (no parent — Req 3.5,
      D2). Then implement both: add the three read-side fields to `domain.Media`
      (`internal/domain/entities.go:126`) and declare `domain.MediaMeta`
      (`internal/domain/entities.go`). Additive only — no field is removed or
      retyped, so no existing reader regresses (Req 10.1).
      Files: `internal/domain/entities.go`, `internal/domain/entities_test.go`
      Verify: `go test ./internal/domain` — the new assertions pass; `go vet ./...`
      clean. _(Req 3.1, 10.1, 10.3; Findings 2, 3; D2)_

- [x] 1.2 Add the `UpdateMeta` method to the `domain.MediaWriter` port
      (`internal/domain/repository.go:222`): `UpdateMeta(ctx context.Context,
      id int64, m MediaMeta) error`, with the doc comment specifying the four
      storage targets (title→`post_title`, caption→`post_excerpt`,
      description→`post_content` on the attachment's `{prefix}posts` row;
      alt_text→the `_wp_attachment_image_alt` `{prefix}postmeta` row), the
      `domain.ErrNotFound`-on-unknown-ID contract matching `SetParent` (Req 3.4),
      and that parent stays on `SetParent` (Req 3.5). The `Create` signature is
      left unchanged — `Create(ctx, Media)` already carries `Media.AuthorID`, so
      the author seam needs no port change (Req 3.6).
      **Note — breaks callers:** growing the interface breaks every fake/stub
      `MediaWriter` (in `internal/content`, `internal/web`, storage tests) until
      each gains an `UpdateMeta` method; those fakes are updated in the tasks that
      own them (2.x storage repo, 4.x service fakes, 6.x transport fakes).
      Files: `internal/domain/repository.go`
      Verify: `go build ./internal/domain` succeeds; `go build ./...` now fails on
      the un-updated fakes (expected — resolved by later tasks). _(Req 3.1, 3.4,
      3.5, 3.6; Finding 2; D2, D7)_

## Phase 2 — Storage: `wprepo.MediaRepo` (`internal/storage/wprepo`)

- [x] 2.1 Write failing cross-vendor contract tests for `MediaRepo.UpdateMeta`
      through the existing `storagetest`/`GRIMOIRE_TEST_WP_DSN` harness the other
      media reads use (sqlite/mysql/postgres), in
      `internal/storage/wprepo/media_test.go` (or the storagetest contract file the
      media reads already live in). Assert: `UpdateMeta` writes `post_title`,
      `post_excerpt`, `post_content` on the attachment's posts row and upserts the
      `_wp_attachment_image_alt` postmeta row (insert when absent, overwrite when
      present); it returns `domain.ErrNotFound` for an unknown ID (Req 3.4); it
      refuses a non-attachment row via the `post_type='attachment'` guard (an
      UPDATE against a non-attachment ID matches nothing → `ErrNotFound`, never
      touches that row). Pin the alt key to the literal `_wp_attachment_image_alt`.
      Files: `internal/storage/wprepo/media_test.go`
      Verify: `go test ./internal/storage/wprepo` — fails (`UpdateMeta`/`upsertPostMeta`
      not implemented). _(Req 3.1, 3.2, 3.4)_

- [x] 2.2 Implement `MediaRepo.UpdateMeta` and the `upsertPostMeta` helper in
      `internal/storage/wprepo/media.go`. `UpdateMeta` runs in a single
      transaction (`db.RunInTx`): an `UPDATE {prefix}posts SET post_title/
      post_excerpt/post_content WHERE ID=? AND post_type='attachment'` guarded so
      it can never touch a non-attachment row, then `errNotFoundIfMissingTx`
      (the transaction-scoped twin of the existing `errNotFoundIfMissing` that
      `SetParent` uses) → `domain.ErrNotFound` when no attachment matched (Req 3.4),
      then `upsertPostMeta(ctx, tx, prefix, id, "_wp_attachment_image_alt",
      m.AltText)`. `upsertPostMeta` writes a single-valued postmeta row
      (DELETE-then-INSERT, or `INSERT ... ON CONFLICT` where the vendor supports
      it) following the vendor switch `MediaRepo.Create` already uses via
      `insertReturningID`/the rebind pattern — the write counterpart of the
      single-valued postmeta read. Add `errNotFoundIfMissingTx` if it does not yet
      exist, mirroring `errNotFoundIfMissing`.
      **Note — resolves the task-1.2 break for storage:** this makes `MediaRepo`
      satisfy the grown `MediaWriter` interface.
      Files: `internal/storage/wprepo/media.go`
      Verify: `go test ./internal/storage/wprepo` — the 2.1 tests pass across every
      configured vendor. _(Req 3.1, 3.2, 3.4)_

- [x] 2.3 Write failing contract tests for the extended read path in
      `internal/storage/wprepo/media_test.go`: `MediaRepo.ByID` populates
      `Media.Caption` (from `post_excerpt`), `Media.Description` (from
      `post_content`), and `Media.AltText` (from the `_wp_attachment_image_alt`
      postmeta), round-tripping a row written by `UpdateMeta`; an attachment with
      no alt postmeta reads back `AltText == ""`.
      Files: `internal/storage/wprepo/media_test.go`
      Verify: `go test ./internal/storage/wprepo` — fails (reads not extended).
      _(Req 10.3; Findings 2, 3)_

- [x] 2.4 Extend `mediaRow` and the `ByID` read in
      `internal/storage/wprepo/media.go` to select `post_excerpt` and
      `post_content` into `Caption`/`Description`, and read `AltText` from the
      `_wp_attachment_image_alt` postmeta (either a `LEFT JOIN` on that meta_key
      alongside the existing `_wp_attached_file` join, or a secondary single-valued
      postmeta lookup in `ByID`). `ByID` — the single-item read the update handler
      re-reads through — must populate all three; the collection `List` read may
      populate them too for consistency. Cross-vendor via the existing rebind
      pattern.
      Files: `internal/storage/wprepo/media.go`
      Verify: `go test ./internal/storage/wprepo` — the 2.3 tests pass across every
      configured vendor; existing media read tests still green (Req 10.1).
      _(Req 10.1, 10.3; Findings 2, 3)_

## Phase 3 — `MediaService.Store` author seam (`internal/content`)

- [x] 3.1 Write failing tests in `internal/content/media_test.go` asserting that
      `MediaUpload` gains an `Author int64` field and that `MediaService.Store`
      writes it into the constructed `domain.Media.AuthorID` (assert on what
      reached the fake `domain.MediaWriter.Create`): a non-zero `Author` is
      threaded to `post_author`, and the zero-value path (no `Author` set) still
      stores author `0`, preserving every pre-10.B caller's behavior (Req 3.6, D7).
      Files: `internal/content/media_test.go`
      Verify: `go test ./internal/content` — fails (field absent).
      _(Req 3.6, 9.x N/A; D7)_

- [x] 3.2 Add the `Author int64` field to `MediaUpload`
      (`internal/content/media.go:28`) and set `AuthorID: upload.Author` in the
      `domain.Media` that `Store` constructs (`internal/content/media.go`), leaving
      every other `Store` behavior (filename sanitization, year/month layout,
      de-duplication, MIME, insert, file-removal-on-failed-insert rollback)
      unchanged. Additive — breaks no caller (zero value preserved).
      Files: `internal/content/media.go`
      Verify: `go test ./internal/content` — the 3.1 tests pass. _(Req 3.6, 11.4; D7)_

- [x] 3.3 Update the admin upload caller to thread its principal:
      in `adminMediaUpload` (`internal/web/adminapi_media.go:143`) resolve the
      principal via `PrincipalFrom(r.Context())` and pass `Author: authorID` on the
      `content.MediaUpload` handed to `Store`, so an admin-uploaded attachment is
      authored consistently with the REST path. Zero when no principal is present.
      Add/extend a test in `internal/web/adminapi_media_test.go` asserting the
      stored attachment's author equals the acting admin's user ID.
      Files: `internal/web/adminapi_media.go`, `internal/web/adminapi_media_test.go`
      Verify: `go test ./internal/web` — the admin author assertion passes; existing
      admin-upload tests still green. _(Req 3.6; D7)_

## Phase 4 — `content.MediaWriteService` (`internal/content`)

- [x] 4.1 Write failing **create** unit tests in
      `internal/content/mediawriteservice_test.go` (new) against a fake
      `domain.MediaWriter`/`domain.MediaRepository` and the **real**
      `sanitize.Policy`, asserting on what reached the writer port: `Create`
      requires `upload_files` (absent → `ErrForbidden`, no write); MIME allowed /
      disallowed (`ErrUnsupportedMediaType`, no write) / `unfiltered_upload` bypass
      (disallowed MIME permitted); an empty `AllowedMIMEs` is unrestricted for
      every caller (Req 6.3); the author is threaded into the stored
      `Media.AuthorID` (D7). Assert the two new sentinels `ErrUnsupportedMediaType`
      and `ErrUploadTooLarge` exist and that `ErrForbidden` is reused.
      Files: `internal/content/mediawriteservice_test.go`
      Verify: `go test ./internal/content` — fails (service/sentinels absent).
      _(Req 1.1, 1.4, 5.1, 5.2, 6.1, 6.2, 6.3, 11.1; D7)_

- [x] 4.2 Write failing **update** unit tests in
      `internal/content/mediawriteservice_test.go` against the fakes and the real
      Policy: ownership is evaluated against the **stored** `post_author` loaded by
      ID (own → `edit_posts`, other → `edit_others_posts`), never a body value
      (Req 5.3–5.5, 11.2); each supplied field is sanitized at its mapped kind — a
      `<script>` in caption or description is stripped, a markup title and alt_text
      are reduced to plain text (title path); an `unfiltered_html` holder's
      caption/description survives with markup intact (tier C — input tier B would
      demonstrably alter); the byte-identity skip (re-sending the stored caption
      verbatim leaves it unchanged, Req 2.4); omitted (nil) fields leave the stored
      value untouched (Req 2.3); a policy error (`sanitize.ErrTitleNotConverging`
      from a pathological title/alt) returns the error unchanged and performs no
      write (fail closed, Req 4.7, 11.3); a missing ID returns `domain.ErrNotFound`
      (Req 2.5); a supplied parent routes through `SetParent` (Req 2.2); the
      service re-reads via `ByID` so the returned `Media` reflects stored values
      (Req 10.3).
      Files: `internal/content/mediawriteservice_test.go`
      Verify: `go test ./internal/content` — fails (`Update` absent).
      _(Req 2.1–2.5, 4.1–4.7, 5.3–5.5, 10.3, 11.1–11.3)_

- [x] 4.3 Implement `MediaWriteService` in
      `internal/content/mediawriteservice.go` (new): the struct (`media
      *MediaService`, `reader domain.MediaRepository`, `writer domain.MediaWriter`,
      `policy *sanitize.Policy`), `MediaWriteOption`, `WithMediaContentPolicy`,
      and `NewMediaWriteService` whose policy **defaults to `sanitize.New()` and is
      never nil** (fail closed). Declare `ErrUnsupportedMediaType` and
      `ErrUploadTooLarge`; reuse the existing `content.ErrForbidden`. Add
      `MediaCreateInput`, `MediaMetaUpdate`, and the `mimeAllowedForMedia`
      predicate (content's copy of the transport's `mimeAllowed`: empty allow-list
      means unrestricted; base-MIME comparison stripping a `; charset=…` suffix).
      Implement `Create` (upload_files check → `ErrForbidden`; MIME allow-list with
      `unfiltered_upload` bypass → `ErrUnsupportedMediaType`; delegate to
      `s.media.Store` threading `Author: actor.UserID`, D7). Implement `Update`
      (load stored row via `reader.ByID` — `domain.ErrNotFound` bubbles up;
      `auth.CanEditPost(actor, "post", "inherit", cur.AuthorID)` → `ErrForbidden`;
      `writer := sanitize.For(actor)`; per-field `policy.Sanitize` at
      `PostTitle`/`PostExcerpt`/`PostTitle`(alt)/`PostContent`, returning the error
      unchanged on failure; persist via `writer.UpdateMeta` then `SetParent` for a
      supplied parent; re-read via `reader.ByID`). No sanitize call on `Create`
      (Req 1.3).
      Files: `internal/content/mediawriteservice.go`
      Verify: `go test ./internal/content` — the 4.1 and 4.2 tests pass; `go vet
      ./...` clean. _(Req 1.1, 1.3, 1.4, 2.1–2.5, 4.1–4.7, 5.1–5.5, 6.1–6.3,
      11.1–11.4; D1, D7)_

## Phase 5 — REST mapper read-path extension (`internal/content`)

- [x] 5.1 Write failing tests in `internal/content/rest_test.go` asserting
      `RESTMapper.Media` emits `caption` and `description` as WordPress
      `{"rendered": "..."}` sub-objects and `alt_text` as a bare string, populated
      from `domain.Media.Caption`/`Description`/`AltText`; and that an attachment
      with those fields empty emits empty-but-present keys (shape stable). Confirm
      the existing emitted fields (`id`, `date`, `slug`, `type`, `link`, `title`,
      `author`, `mime_type`, `source_url`, `post`, `media_details`) are unchanged
      (Req 10.1) and that `content.rendered` is not introduced for media (Req 10.2).
      Files: `internal/content/rest_test.go`
      Verify: `go test ./internal/content` — fails (RESTMedia fields absent).
      _(Req 10.1, 10.2, 10.3; Finding 3)_

- [x] 5.2 Add `Caption RESTRendered`, `AltText string`, `Description RESTRendered`
      to `content.RESTMedia` and populate them in `RESTMapper.Media`
      (`internal/content/rest.go`) straight from the now-populated `domain.Media`.
      `caption`/`description` use the `{"rendered": …}` shape (as `title` does);
      `alt_text` is a bare string, matching WordPress's media response. Additive
      JSON keys only — no existing key changes (Req 10.1).
      Files: `internal/content/rest.go`
      Verify: `go test ./internal/content` — the 5.1 tests pass. _(Req 10.1, 10.3;
      Finding 3)_

## Phase 6 — REST transport handlers (`internal/web`)

- [x] 6.1 Write failing **create** transport tests in
      `internal/web/rest_media_test.go` end-to-end through the router with a real
      `MediaWriteService` wired: `201` envelope on success whose shape matches
      `GET /media/{id}` (`mapRESTMedia`, Req 1.2); missing `Content-Disposition`
      filename → `400 rest_upload_no_content_disposition` (Req 1.5); empty body →
      `400 rest_upload_no_data` (Req 1.6); body over `MaxUploadSize` →
      `413 rest_upload_file_too_big` via `MaxBytesReader` (Req 7); disallowed MIME
      without `unfiltered_upload` → `415 rest_upload_unsupported_media_type`
      (Req 6.1); caller lacking `upload_files` and anonymous caller →
      `403 rest_cannot_create` (Req 5.1, 5.6); the MIME decision uses the
      server-sniffed type, not the client `Content-Type` header (Req 6.4). Assert
      end-to-end that a hostile filename cannot escape `Store`'s sanitization.
      Files: `internal/web/rest_media_test.go`
      Verify: `go test ./internal/web` — fails (`handleRESTMediaCreate` absent).
      _(Req 1.1, 1.2, 1.5, 1.6, 5.1, 5.6, 6.1, 6.4, 7.1, 7.2)_

- [x] 6.2 Implement `handleRESTMediaCreate` and `parseContentDispositionFilename`
      in `internal/web/rest_media.go`. Auth preamble matches
      `rest_posts.go`/`rest_comments.go` (AppPassword skips CSRF; session enforces
      M4 CSRF via `requireSessionCSRFREST`, Req 8.1, 8.2). Resolve the principal via
      `PrincipalFrom`. Require the `Content-Disposition` filename
      (`parseContentDispositionFilename` via `mime.ParseMediaType` → `400
      rest_upload_no_content_disposition`). Cap the body with
      `http.MaxBytesReader(w, r.Body, s.media.Config().MaxUploadSize)`, map a
      `*http.MaxBytesError` to `413 rest_upload_file_too_big`, an unreadable body
      to `400 rest_invalid_param`, and an empty body to `400 rest_upload_no_data`.
      Sniff MIME via `http.DetectContentType` on the first ≤512 bytes (Req 6.4).
      Call `s.mediaWrite.Create`; map `content.ErrForbidden`→`403 rest_cannot_create`,
      `content.ErrUnsupportedMediaType`→`415 rest_upload_unsupported_media_type`,
      default→`500 rest_upload_failed`; on success envelope via `s.mapRESTMedia` →
      `201` (Req 1.2).
      Files: `internal/web/rest_media.go`
      Verify: `go test ./internal/web` — the 6.1 tests pass. _(Req 1.1–1.6, 5.1,
      5.6, 6.1, 6.4, 7.1, 7.2, 8.1, 8.2, 8.3; Finding 1)_

- [x] 6.3 Write failing **update** transport tests in
      `internal/web/rest_media_test.go`: `200` envelope on success (shape matches
      GET, Req 2.1); invalid/non-positive `{id}` → `404 rest_post_invalid_id`
      (Req 2.5); unknown attachment (`domain.ErrNotFound`) → `404 rest_post_invalid_id`
      (Req 2.5); malformed JSON body → `400 rest_invalid_param`; negative/non-numeric
      `post` parent → `400 rest_invalid_param` (Req 2.6); partial update leaves
      omitted fields unchanged (Req 2.3); `403 rest_cannot_edit` evaluated against
      the stored author for own-vs-others and for anonymous (Req 5.3–5.6); policy
      error (`sanitize.ErrTitleNotConverging`) → `400 rest_invalid_param`, never
      `500` (Req 4.7); `POST /media/{id}` routes to **update**, not create.
      Files: `internal/web/rest_media_test.go`
      Verify: `go test ./internal/web` — fails (`handleRESTMediaUpdate` absent).
      _(Req 2.1–2.6, 4.7, 5.3–5.6)_

- [x] 6.4 Implement `handleRESTMediaUpdate`, `restMediaUpdateBody`, and
      `parseRESTMediaUpdate` in `internal/web/rest_media.go`. Same auth preamble as
      create. Parse `{id}` (invalid/≤0 → `404 rest_post_invalid_id`). Decode the
      JSON body with pointer fields (`title`/`caption`/`alt_text`/`description`/`post`)
      for partial-update semantics (Req 2.3); malformed JSON → `400 rest_invalid_param`.
      `parseRESTMediaUpdate` validates a negative `post` → `400 rest_invalid_param`
      (a non-numeric `post` is caught by JSON decode into `*int64`, Req 2.6).
      Call `s.mediaWrite.Update`; map `domain.ErrNotFound`→`404 rest_post_invalid_id`,
      `content.ErrForbidden`→`403 rest_cannot_edit`,
      `sanitize.ErrTitleNotConverging`→`400 rest_invalid_param`,
      default→`500 rest_update_failed`; on success envelope via `s.mapRESTMedia` →
      `200` (Req 2.1). No per-handler Policy call (Req 8.3).
      Files: `internal/web/rest_media.go`
      Verify: `go test ./internal/web` — the 6.3 tests pass. _(Req 2.1–2.6, 4.7,
      5.3–5.6, 8.1–8.3, 10.3)_

- [x] 6.5 Update `registerRESTMedia` (`internal/web/rest_media.go:18`) to replace
      exactly the `POST /media` stub and the `PUT`/`PATCH`/`POST /media/{id}` stubs
      with the real handlers, **only when `s.mediaWrite != nil`** (falling back to
      the existing `restNotImplemented` stubs otherwise, mirroring
      `registerRESTTerms`'s `s.termWrite != nil` gate so every harness that omits
      the write service still answers `501`). Leave `PUT`/`PATCH`/`DELETE /media`
      (collection) and `DELETE /media/{id}` as their existing `restNotImplemented`
      registrations (Req 9.1, 9.2). Write a failing test first in
      `internal/web/rest_media_test.go` asserting the four DELETE/collection stubs
      still answer `501` and never invoke the service (Req 9.3), and that with
      `mediaWrite == nil` the write verbs answer `501`.
      Files: `internal/web/rest_media.go`, `internal/web/rest_media_test.go`
      Verify: `go test ./internal/web` — the 501-stub and nil-fallback tests pass.
      _(Req 9.1, 9.2, 9.3)_

## Phase 7 — Server + main wiring

- [x] 7.1 Add the `mediaWrite *content.MediaWriteService` field to the Server
      (`internal/web/router.go`, beside `s.media`) and extend the existing
      `WithContentFeatures` builder (`internal/web/contentfeatures.go`) to accept
      and store it, so all media wiring stays together. Write/extend a wiring test
      in `internal/web` (or `cmd/grimoire/main_test.go`) asserting the service is
      reachable and that the same `*sanitize.Policy` instance governs post writes,
      comment writes, and the media write service (single-instance invariant,
      Req 11.1).
      **Note — resolves the task-1.2 break for web fakes:** update any fake
      `MediaWriter` in `internal/web` test helpers to implement `UpdateMeta`.
      Files: `internal/web/router.go`, `internal/web/contentfeatures.go`,
      `internal/web/*_test.go`
      Verify: `go build ./...` succeeds; `go test ./internal/web` green. _(Req 8.3,
      11.1)_

- [x] 7.2 Construct `MediaWriteService` in `cmd/grimoire/main.go` next to
      `postWrite`, over the existing `media` service and the `repos.Media`/
      `repos.MediaWriter` ports, handed the shared `contentPolicy` via
      `content.WithMediaContentPolicy(contentPolicy)` (`cmd/grimoire/main.go:89`
      region), and pass it to the extended `WithContentFeatures(comments, media,
      mediaWrite, menus)` call so `registerRESTMedia` wires the real handlers.
      Files: `cmd/grimoire/main.go`
      Verify: `go build ./...` and `go test ./cmd/...` green; the transport tests
      from tasks 6.1/6.3 pass against the real wiring. _(Req 8.3, 11.1)_

## Phase 8 — Capability matrix, observability, and final gates

- [x] 8.1 Add the capability-matrix transport test in
      `internal/web/rest_media_test.go` (one place, table-driven): across
      anonymous, subscriber, contributor, author, editor, administrator (built
      through the real `auth.CapabilitiesForRoles`/`auth.NewPrincipal`), assert
      create is permitted exactly for `upload_files` holders (author/editor/admin)
      and `403 rest_cannot_create` otherwise; update own-attachment is permitted
      for `edit_posts` holders and other-attachment for `edit_others_posts`
      holders, `403 rest_cannot_edit` otherwise, always against the stored author;
      the MIME allow-list is bypassed only for the `unfiltered_upload` holder
      (administrator). Include the auth-matrix cells: Application Password skips
      CSRF; a session without a valid CSRF token → `403 rest_forbidden`.
      Files: `internal/web/rest_media_test.go`
      Verify: `go test ./internal/web` passes. _(Req 5.1–5.6, 6.1, 6.2, 8.1, 8.2)_

- [x] 8.2 Add the content-safety and observability end-to-end tests in
      `internal/web/rest_media_test.go`: a submitted `<script>` in a caption or
      description is sanitized **before it reaches the writer** (assert the writer
      saw inert content, not that the response merely escaped it); then
      create → update caption/alt_text/description → `GET /media/{id}` returns the
      **sanitized stored values** (Req 10.3); and a `GET` of an unmodified
      attachment is byte-identical to before this milestone (Req 10.1). Add the
      byte-identity acceptance case: a PATCH re-sending the stored caption verbatim
      does not alter it (Req 2.4).
      Files: `internal/web/rest_media_test.go`
      Verify: `go test ./internal/web` passes. _(Req 2.4, 10.1, 10.3, 11.1, 11.3)_

- [x] 8.3 Verify no migration and confirm the no-regression invariants: confirm no
      new file exists under `internal/storage/migrations/<vendor>/` for this
      milestone (Req 3.3, mirroring M8/M9a/M9b/M10a); confirm the `GET /media` and
      `GET /media/{id}` read handlers are unchanged beyond the additive mapper
      fields (Req 10.1), `content.rendered` is untouched (Req 10.2), and
      `DELETE /media[/{id}]` still answers `501` (Req 9).
      Files: (verification only) `internal/storage/migrations/`,
      `internal/web/rest_media.go`
      Verify: `git status --porcelain internal/storage/migrations` shows no new
      files; `go test ./internal/web` green on the read/DELETE assertions. _(Req 3.3,
      9.1, 9.2, 10.1, 10.2)_

- [x] 8.4 Run the full gate suite and fix anything red: `gofmt -l .` empty,
      `go vet ./...`, `go build ./...`, `go test ./...` (including the cross-vendor
      media contract tests through the `storagetest`/`GRIMOIRE_TEST_WP_DSN`
      harness). Confirm no test added by this milestone requires a database,
      network, live WordPress, or environment gating beyond the pre-existing
      cross-vendor storage harness.
      Files: (whole tree, verification only)
      Verify: all four commands green; `go test ./...` needs no new
      `GRIMOIRE_TEST_*` env vars beyond the existing storage harness. _(Req 10.1,
      11.1–11.4)_

## Notes

- Each task references the exact Requirement/AC, Finding, and Inherited decision
  it satisfies, for traceability back to `requirements.md` and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt`/`vet`/`build`/`test` after
  every phase, matching M10a's close-out discipline.
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the
  sub-task that writes its failing test (Req 11).
- No sub-task is marked optional (`*`): this milestone's tests are the fail-closed
  safety and capability guarantees (Req 11), not nice-to-haves, so each is a core
  task. (This matches M10a, whose write-boundary tests were likewise unmarked.)
- No property-based tests: 10.B is a REST write surface over existing upload
  machinery; its sanitization *is* M10a's already-property-tested `sanitize.Policy`,
  and the field-kind mapping is a fixed table, not a property (design "Testing
  strategy").

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "1.2"] },
    { "id": 1, "tasks": ["2.1", "3.1"] },
    { "id": 2, "tasks": ["2.2", "2.3", "3.2"] },
    { "id": 3, "tasks": ["2.4", "3.3", "4.1"] },
    { "id": 4, "tasks": ["4.2", "5.1"] },
    { "id": 5, "tasks": ["4.3", "5.2"] },
    { "id": 6, "tasks": ["6.1"] },
    { "id": 7, "tasks": ["6.2"] },
    { "id": 8, "tasks": ["6.3"] },
    { "id": 9, "tasks": ["6.4"] },
    { "id": 10, "tasks": ["6.5", "7.1"] },
    { "id": 11, "tasks": ["7.2"] },
    { "id": 12, "tasks": ["8.1", "8.3"] },
    { "id": 13, "tasks": ["8.2"] },
    { "id": 14, "tasks": ["8.4"] }
  ]
}
```
