# Implementation Plan: M10.E — M10 Write-Route Test Coverage

## Overview

Implementation checklist for M10.E (roadmap group **10.E only**). Tasks are
ordered so each builds on the last, and each references the exact files, the
exact symbols named in `design.md`'s "Test designs per route" / "The coverage
matrix" sections, and the Requirement/AC plus the Finding (1–3) and Inherited
decision (D1–D4) it satisfies. Keep `gofmt -l .` empty and `go vet ./...`,
`go build ./...`, `go test ./...` green after every phase.

> **This refines roadmap group 10.E only.** It is the **test-coverage close-out**
> for M10, tracing to roadmap **Requirement 19.2** (every new write endpoint
> covered by a capability-matrix test and a content-safety test before it is
> considered done). Unlike its write-parity siblings M10.B
> (`../10.1-rest-media-write`), M10.C (`../10.2-rest-user-write`), and M10.D
> (`../10.3-rest-content-rendered`), 10.E adds **no feature, no new write surface,
> no capability gate, no route, and no `restNotImplemented` change.** It is an
> **audit plus gap-fill**: it inventories the six M10 write routes, cites the
> tests that already cover routes 5–6 by exact name, and adds only what is
> missing — **four new capability-matrix tests and ZERO new content-safety tests.**
>
> **10.E is TEST-ONLY plus one coverage document (D4).** The milestone adds only
> test functions in existing `*_test.go` files and one committed deliverable,
> `plans/10.4-m10-test-coverage/coverage.md` (Decision A). It modifies **no**
> production source under `internal/`, adds **no** database migration (task 3.1
> verifies none appears under `internal/storage/migrations`, as
> M10a/M10.B/M10.C/M10.D did, Req 6.2), changes **no** `restNotImplemented`
> registration (every `501` stub stays `501` and is a recorded excluded cell,
> Req 6.3), and introduces **no** new `GRIMOIRE_TEST_*` env var (Req 7.1). It
> reuses `internal/sanitize.Policy` and the `internal/auth` capability model
> without modification, asserting their behavior rather than altering it (Req 6.4).
>
> **This is NOT the usual test-first TDD spine, and that is deliberate.** M10.D
> and its siblings wrote a failing test *before* the production code that passed
> it, because they added new behavior. 10.E adds **no new production code** —
> there is nothing to drive red→green. Each of the four new tests asserts
> **already-shipped** behavior: the post/admin-post/admin-media/comment routes
> already enforce these authorization contracts today (the scattered forbidden
> tests being consolidated pass as written). So each matrix is written to **PASS
> against the current handler** the moment it lands. This is intentional for a
> coverage-audit milestone and does not weaken the "tests prove the contract"
> posture — the matrices still prove the real capability model via the real auth
> path, they simply confirm an existing contract rather than introduce one.
>
> **Three design Findings shape what gets built (none is a defect).** *Finding 1*
> — Requirement 3 (transport content-safety on the admin-post and comment paths)
> is **already satisfied** by existing stored-state proofs:
> `TestAdminPostCreate/UpdateSanitizesContentExcerptAndTitleEndToEnd` read back
> through `memPostWriter` via `mem.get(id)` and assert stored
> `Content`/`Excerpt`/`Title`;
> `TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter` reads
> `repos.Comments.List(...)` and asserts stored `Content`. Both drive the REAL
> service over the REAL store, so a transport bypassing the Policy could not pass.
> 10.E **cites** these, adds **no** new content-safety test (D1, D3). *Finding 2*
> — REST comment create has **no forbidden capability** (create is
> anonymous-allowed; `moderate_comments` gates only comment *status*, never
> creation), so `TestRESTCommentCreateCapabilityMatrix` is really an
> *authorization-contract* matrix — anonymous/subscriber/editor→`201`,
> empty-after-sanitize→`400`, closed→`403`, missing-CSRF→`403` — annotated as such
> in `coverage.md` so it is not misread as an allowed-vs-forbidden split.
> *Finding 3* — the admin-media upload route is gated by `edit_posts` at the
> router (`requireCapabilityJSON("edit_posts")`, `adminroutes.go:84`), **not**
> `upload_files`; `TestAdminMediaUploadCapabilityMatrix` splits allowed vs
> forbidden on `edit_posts`.
>
> **Two auth mechanisms, respected per route (structural, not a defect).** REST
> routes (1, 2) authenticate through a **session fake + CSRF** (`requireSession
> CSRFREST`): the matrix carries a session cookie and a matching `X-CSRF-Token`,
> and the anonymous cell sends neither. Admin-API routes (3, 4) authenticate
> through a **context-injected principal** (`principalCtx`/`withAuth`, or
> `SessionMiddleware(srv.adminAPIRouter())`) with no CSRF synchronizer in the
> handler path. The four tasks respect this asymmetry rather than papering over
> it — the REST matrices carry cookies/CSRF, the admin matrices inject principals
> directly.
>
> **Decision B is a contingency that is not expected to fire.** If a Req 5 Finding
> ever authorized a production change, the Finding-linked test would assert
> current behavior with a pointer to `coverage.md`. But the audit confirms routes
> 1–4 behave correctly, so the expectation is **zero** Findings beyond the three
> documentation findings above — no such test is planned.

## Tasks

## Phase 1 — The four capability-matrix tests (test-only; four distinct files, mutually independent)

Each task writes one table-driven capability-matrix test that asserts the route's
**current** contract and **passes** against the live handler the moment it lands
(there is no new production code to drive). Each mirrors the style of
`TestRESTMediaCreateCapabilityMatrix` (`rest_media_test.go:1028`): a `cases` slice
of `{name, fake/ctx, wantCode, wantErr}`, a `t.Run` per cell, a
status-and-error-code assertion, and a no-state-change assertion on every
forbidden cell. Every principal is built through the real
`auth.CapabilitiesForRoles` / `auth.NewPrincipal` path (Req 2.6) — never a
hand-forged capability set.

- [x] 1.1 Write `TestRESTPostWriteCapabilityMatrix` in
      `internal/web/rest_posts_test.go`, asserting the REST post/page
      create/update/delete authorization contract as it stands today.
      **Harness/helpers reused:** `newRESTPostWriteRouter(t, fake)`
      (`rest_posts_test.go:57`, wiring the REAL `content.PostWriteService` over the
      live SQLite `PostWriter`), per-role fakes following the `authorFake` shape
      (varying role and `UserID`), `decodeRESTErrCode`, and
      `repos.AdminPosts.ListForAdmin` / `repos.PostWriter.ByID` for the no-write
      readback. **Auth mechanism:** session fake + CSRF — each non-anonymous cell
      carries the session cookie and matching `X-CSRF-Token`; the anonymous cell
      sends neither. **Allowed/forbidden table.** CREATE (POST
      `/wp-json/wp/v2/posts`, draft so `publish_posts` is not required):
      anonymous → `403 rest_cannot_create`; subscriber → `403 rest_cannot_create`
      (lacks `edit_posts`); contributor/author/editor/administrator (stored author,
      UserID 1) → `201`; publish sub-case: contributor → `403` (lacks
      `publish_posts`), author+ → `201`. UPDATE (PUT `/posts/1`, seeded author 1):
      anonymous/subscriber → `403 rest_cannot_edit`; author-as-1 (own) → `200`;
      author-as-7 (other) → `403 rest_cannot_edit` (lacks `edit_others_posts`);
      editor-as-7 / admin-as-7 → `200`. DELETE (DELETE `/posts/1`): same shape as
      update, forbidden code `403 rest_cannot_edit` (handler reuses the edit code,
      `rest_posts.go:458`). **Gating asserted:** `CanCreatePost`/`CanEditPost`/
      `CanDeletePost` against the stored author. **No-state-change on forbidden:**
      create — assert no new post carries the submitted title via
      `repos.AdminPosts.ListForAdmin(...)`; update — snapshot `repos.PostWriter.
      ByID(1)` and assert `Title`/`Content` unchanged; delete — assert
      `repos.PostWriter.ByID(1)` still resolves (not `domain.ErrNotFound`).
      Files: `internal/web/rest_posts_test.go`
      Verify: `go test ./internal/web -run TestRESTPostWriteCapabilityMatrix` green
      against current code; `gofmt -l internal/web/rest_posts_test.go` empty,
      `go vet ./internal/web` clean. _(Req 2.1, 2.2, 2.6, 2.7; D1, D2)_

- [x] 1.2 Write `TestRESTCommentCreateCapabilityMatrix` in
      `internal/web/rest_comments_test.go`, tabling the route's **actual**
      authorization contract (Finding 2 — comment create is anonymous-allowed and
      has no forbidden *capability*; `moderate_comments` gates only comment status,
      never creation). **Harness/helpers reused:**
      `newAppPasswordRESTRouter(t, fake, false, "")`
      (`rest_apppassword_comments_test.go:34`), `decodeRESTErrCode`, and
      `repos.Comments.List(ctx, domain.CommentFilter{PostID: 1})` for the no-write
      check — the same readback the existing comment proofs use. **Auth mechanism:**
      session fake + CSRF for the logged-in cells; a bare request (no cookie, no
      CSRF) for the anonymous-allowed cell, matching
      `TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter`. **Outcome table
      (the outcomes the route genuinely distinguishes):** anonymous (unique email)
      → `201`, persisted; subscriber (session+CSRF) → `201`, persisted; editor
      (`moderate_comments`) → `201`, persisted; markup-only body that sanitizes to
      empty at tier A → `400 rest_comment_content_invalid`, no comment persisted
      (consolidates `TestRESTCommentCreateMarkupOnlyBodyMapsErrCommentEmptyTo400`);
      closed-comments post → `403 rest_comment_closed`, no comment persisted;
      session cell with missing/mismatched CSRF → `403 rest_forbidden`, no comment
      persisted (the CSRF seam). **No-state-change on forbidden:** for each
      non-`201` cell, scan `repos.Comments.List(...)` by the cell's unique
      `author_email` and assert it is absent. Annotate the test (and its
      `coverage.md` cell) as an **authorization-contract** matrix, not an
      allowed-vs-forbidden capability split (Finding 2).
      Files: `internal/web/rest_comments_test.go`
      Verify: `go test ./internal/web -run TestRESTCommentCreateCapabilityMatrix`
      green against current code; `gofmt`/`go vet ./internal/web` clean.
      _(Req 2.5, 2.6, 2.7; Finding 2; D1)_

- [x] 1.3 Write `TestAdminPostWriteCapabilityMatrix` in
      `internal/web/adminapi_posts_test.go` (package `web`, white-box), asserting
      the admin post create/update/delete authorization contract and consolidating
      the scattered `TestAdminPostUpdateForbidden` /
      `TestAdminPostDeleteMissingRecordIsForbidden` cases into one table-driven
      matrix. **Harness/helpers reused:** `newEndToEndWriteServer(mem)` +
      `memPostWriter` (wiring the REAL `content.PostWriteService` over an in-memory
      `domain.PostWriter` and a `fakeAdmin` reading back from the same store),
      `withURLParam`, `principalCtx` / the `authorCtx`/`withAuth` context-principal
      helpers, and `s.jsonHandler(s.adminPostCreate/Update/Delete)`. **Auth
      mechanism:** **context-injected principal** (`principalCtx(...)`/`withAuth
      (...)`), NOT a session fake. The matrix drives the handler + real
      `PostWriteService` (so the ownership decision is genuine, not a stub's canned
      return), asserting the handler's `content.ErrForbidden` → `403 "forbidden"`
      mapping (`adminapi.go:47-48`); it does not re-run the already-proven
      router-level `requireCapabilityJSON` gate. **Allowed/forbidden table**
      (principals via `auth.NewPrincipal(uid, name, roles)`, attached with
      `withAuth`): CREATE — subscriber-principal → `403 forbidden`; author-as-1 →
      `201`. UPDATE (stored post author 1 in `mem`): author-as-1 (own) → `200`;
      author-as-7 (other) → `403 forbidden`; editor-as-7 → `200`. DELETE — same
      ownership shape, `204` on success, `403 forbidden` on the
      other-author-without-`delete_others_posts` cell; the missing-record cell
      asserts `403 forbidden` (`PostWriteService` maps a missing record to
      `ErrForbidden`, never `ErrNotFound` — consolidating
      `TestAdminPostDeleteMissingRecordIsForbidden`). **Gating asserted:**
      `CanCreatePost`/`CanEditPost`/`CanDeletePost` through the real service;
      forbidden status `403`, error code `forbidden` (admin-API JSON envelope).
      **No-state-change on forbidden:** read `mem.get(id)` (or `mem.ByID`) and
      assert the stored post is unchanged for update, still present for a forbidden
      delete, and `mem` gained no row for a forbidden create.
      Files: `internal/web/adminapi_posts_test.go`
      Verify: `go test ./internal/web -run TestAdminPostWriteCapabilityMatrix`
      green against current code; `gofmt`/`go vet ./internal/web` clean.
      _(Req 2.3, 2.6, 2.7; D1)_

- [x] 1.4 Write `TestAdminMediaUploadCapabilityMatrix` in
      `internal/web/adminapi_media_test.go` (package `web`, white-box), asserting
      the admin media upload authorization contract. The route's effective gate is
      `edit_posts`, **not** `upload_files` (Finding 3 — the handler only enforces
      CSRF, the size cap, and the MIME sniff; the `/admin/api/*` surface is gated
      once at the router by `requireCapabilityJSON("edit_posts")`,
      `adminroutes.go:84`). **Harness/helpers reused:** the `newMediaTestServer` /
      `TestAdminMediaUploadAuthorsAttachmentToActingAdmin` shape — a `Server` with
      `srv.auth = fakeSessions{p:..., s:...}` and
      `srv.media = content.NewMediaService(repo, writer, MediaConfig{...})`, driven
      through `SessionMiddleware(srv.adminAPIRouter())`; a multipart body built with
      `multipart.NewWriter` carrying the PNG signature so
      `http.DetectContentType` sniffs `image/png`; an `authorCapturingMediaWriter`
      (or a counting `capturingMediaRepo`) observing whether a row was created.
      **Auth mechanism:** **context-injected principal** via the full admin router
      (`SessionMiddleware` resolves the `fakeSessions` principal into the request
      context, then `requireCapabilityJSON("edit_posts")` runs). **Allowed/forbidden
      table:** administrator (holds `edit_posts`) with a valid CSRF token → `201`
      and `writer.created` length 1; subscriber-principal (lacks `edit_posts`) →
      `403 forbidden`, `writer.created` empty; anonymous (no session) →
      `401 unauthorized`, no row. **Gating asserted:** `edit_posts` (Finding 3);
      forbidden status `403`/`401`, admin JSON envelope code
      `forbidden`/`unauthorized`. **No-state-change on forbidden:** assert the
      capturing writer recorded zero `Create` calls (`len(writer.created) == 0`),
      so no media row was created for a forbidden caller.
      Files: `internal/web/adminapi_media_test.go`
      Verify: `go test ./internal/web -run TestAdminMediaUploadCapabilityMatrix`
      green against current code; `gofmt`/`go vet ./internal/web` clean.
      _(Req 2.4, 2.6, 2.7; Finding 3; D1)_

## Phase 2 — Coverage document + inventory verification

- [x] 2.1 Create `plans/10.4-m10-test-coverage/coverage.md` — the milestone's
      durable deliverable (Decision A, Req 1 / Req 5.3) — holding the full coverage
      matrix and the Findings section. **Matrix:** every M10 write route × verb from
      the design's coverage matrix appears exactly once, as a Markdown table of
      _route # · route · verb · capability-matrix test · content-safety test ·
      status (`existing` | `to-add` | `excluded-with-reason`)_. Existing cells cite
      the test by its **exact** function name **and** file — the eight route-5/6
      capability matrices (`TestRESTMediaCreateCapabilityMatrix`,
      `TestRESTMediaUpdateCapabilityMatrix`, `TestRESTMediaCreateMIMEBypassMatrix`,
      `TestRESTMediaWriteAuthMatrix` in `internal/web/rest_media_test.go`;
      `TestRESTUserCreateCapabilityMatrix`, `TestRESTUserUpdateSelfVsOtherMatrix`,
      `TestRESTUserRoleAssignmentMatrix`, `TestRESTUserWriteAuthMatrix` in
      `internal/web/rest_users_test.go`) and every content-safety proof
      (`TestRESTPostCreate/UpdateSanitizesContentExcerptAndTitleEndToEnd`,
      `TestAdminPostCreate/UpdateSanitizesContentExcerptAndTitleEndToEnd`,
      `TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter` +
      `...LoggedInEditorUsesForPrincipalTierC`, the REST-media/REST-user
      description-sanitized e2e). The four new cells name
      `TestRESTPostWriteCapabilityMatrix`, `TestRESTCommentCreateCapabilityMatrix`,
      `TestAdminPostWriteCapabilityMatrix`, `TestAdminMediaUploadCapabilityMatrix`.
      Excluded cells (every `501` stub DELETE/collection/item path, the DELETE
      no-content-body cells, the binary-upload no-markup cell) carry their recorded
      reason (Req 1.4). **Findings section:** Findings 1–3 from the design,
      verbatim-in-substance (Req 3 already satisfied → cite not duplicate; comment
      create is an authorization-contract matrix; admin-media gate is `edit_posts`).
      **Inventory verification:** confirm every EXISTING cited test actually exists
      and passes — run them by name (`go test ./internal/web -run '<Name>'`) and
      record the pass; if any believed-covered media/user cell is in fact not
      backed by a passing test, treat it as a gap under Req 1.3 rather than
      assuming coverage.
      Files: `plans/10.4-m10-test-coverage/coverage.md`
      Verify: `coverage.md` lists every route×verb once with each existing cell
      cited by exact name+file and the four new cells named; every cited existing
      test runs green under `go test ./internal/web -run '<Name>'`; the Findings
      section carries Findings 1–3.
      _(Req 1.1, 1.2, 1.3, 1.4, 1.5, 3.3, 4.1, 4.2, 4.3, 4.4, 5.3; D1, D2)_

## Phase 3 — Final gate

- [x] 3.1 Verify the no-production-change invariants and run the full close-out
      gate. Confirm `git diff --name-only` shows **only** `*_test.go` files and
      files under `plans/` (no production source under `internal/` changed, Req 6.1);
      confirm no file is added under `internal/storage/migrations` (Req 6.2,
      mirroring M10a/M10.B/M10.C/M10.D); confirm no `restNotImplemented`
      registration changed and no new REST/admin-API route was added (Req 6.3);
      confirm no new `GRIMOIRE_TEST_*` env var and no network access was introduced
      (Req 7.1). Then run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`,
      `go test ./...` and fix anything red. The four new matrices assert the routes'
      current contracts unchanged (Req 7.3).
      Files: (verification only) `internal/storage/migrations/`, `internal/`,
      `plans/10.4-m10-test-coverage/`
      Verify: `git diff --name-only` lists only `*_test.go` + `plans/` paths;
      `git status --porcelain internal/storage/migrations` shows no new files;
      all four gate commands green; no new `GRIMOIRE_TEST_*` env var required.
      _(Req 6.1, 6.2, 6.3, 6.4, 7.1, 7.2, 7.3)_

## Notes

- Each task references the exact Requirement/AC, Finding, and Inherited decision
  it satisfies, for traceability back to `requirements.md` and `design.md`.
- The final gate (task 3.1) is the close-out green gate: `gofmt`/`vet`/`build`/
  `test` plus the no-production-change checks, matching M10a–M10.D's close-out
  discipline.
- **No sub-task is marked optional (`*`).** The four capability matrices are the
  roadmap's M10 done-definition for Requirement 19.2 (each M10a write route backed
  by an allowed-vs-forbidden matrix), and `coverage.md` is the Req 1 / Req 5.3
  deliverable — none is a nice-to-have. (This matches M10.B/M10.C/M10.D, whose
  tests were likewise unmarked.)
- **This milestone departs from the usual test-first TDD spine, deliberately.**
  M10.B–M10.D wrote a failing test before the production code that passed it,
  because they added behavior. 10.E adds **no new production code** — there is
  nothing to drive red→green. Each of the four tests asserts **already-shipped**
  behavior (the routes already enforce these contracts today), so each is written
  to **PASS** against the current handler the moment it lands. This is intentional
  for a coverage-audit close-out and does not weaken the "tests prove the contract"
  posture: the matrices still prove the real capability model via the real
  `auth.CapabilitiesForRoles`/`auth.NewPrincipal` path, they simply confirm an
  existing contract rather than introduce one.
- **No property-based tests.** A capability matrix is a *fixed, enumerated* role
  table (anonymous / subscriber / contributor / author / editor / administrator
  against a known route), not a universally-quantified property over a large input
  space — 100 random iterations would find no edge case a hand-picked role
  enumeration does not, and the roles are a closed set, not a generator. The one
  property-shaped guarantee ("for all markup, the writer sees inert content") is
  already covered by the existing service- and transport-level proofs, which 10.E
  only cites. This matches the M10a–M10.D precedent, whose designs likewise omit a
  Correctness Properties section.
- **The one allowed production-change exception is not expected to fire.** Req 6.1
  permits a production change only if a Requirement 5 Finding is explicitly decided
  to authorize one. The audit confirms routes 1–4 behave correctly, so the
  expectation is **zero** Findings beyond the three documentation findings — no
  Decision-B Finding-linked test is planned (Req 5.2).

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "1.2", "1.3", "1.4"] },
    { "id": 1, "tasks": ["2.1"] },
    { "id": 2, "tasks": ["3.1"] }
  ]
}
```
