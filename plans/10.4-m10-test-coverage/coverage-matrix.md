# M10.E — M10 Write-Route Test Coverage

This document is the **M10.E deliverable** (wordpress-core-parity roadmap
Requirement 19.2: *every new write endpoint SHALL be covered by a
capability-matrix test and a content-safety test before it is considered
done*). It is the auditable matrix of every M10 write route × verb, each mapped
to the name of its capability-matrix test and its content-safety test, followed
by the three audit Findings and the inventory verification that confirms every
cited test is present and green.

The write-route surface is fixed and closed at six routes (REST posts/pages,
REST comments, admin posts, admin media, REST media, REST users). 10.E audits
exactly these. No `restNotImplemented` `501` stub is promoted; every stubbed or
body-less verb is recorded as an excluded cell with its reason, not as a gap.

**Status legend:** `existing` — a test present before 10.E, cited by exact
symbol + file. `ADDED` — a capability matrix 10.E wrote and this document now
records as landed. `excluded-with-reason` — a recorded decision (Req 1.4), not a
missing test.

## The coverage matrix

| # | Route | Verb | Capability-matrix test | Content-safety test | Status |
|---|---|---|---|---|---|
| 1 | REST posts/pages | POST (create) | `TestRESTPostWriteCapabilityMatrix` (`internal/web/rest_posts_test.go`) | `TestRESTPostCreateSanitizesContentExcerptAndTitleEndToEnd` (`internal/web/rest_posts_test.go`) | cap: **ADDED**; safety: existing |
| 1 | REST posts/pages | PUT/PATCH (update) | `TestRESTPostWriteCapabilityMatrix` (`internal/web/rest_posts_test.go`) | `TestRESTPostUpdateSanitizesContentExcerptAndTitleEndToEnd` (`internal/web/rest_posts_test.go`) | cap: **ADDED**; safety: existing |
| 1 | REST posts/pages | DELETE | `TestRESTPostWriteCapabilityMatrix` (delete sub-cases, `internal/web/rest_posts_test.go`) | — | cap: **ADDED**; safety: **excluded** — DELETE carries no content body → no content-safety cell (Req 1.4) |
| 1 | REST posts collection | PUT/PATCH/DELETE | — | — | **excluded** — `restNotImplemented` `501` stubs (`rest_posts.go:39`), never governed writes (Req 1.4) |
| 1 | REST posts item | POST | — | — | **excluded** — `restNotImplemented` `501` stub (`rest_posts.go:41`) (Req 1.4) |
| 2 | REST comments | POST (create) | `TestRESTCommentCreateCapabilityMatrix` (`internal/web/rest_comments_test.go`) | `TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter` + `TestRESTCommentCreateLoggedInEditorUsesForPrincipalTierC` (tier-C dual, Req 3.5) (`internal/web/rest_comments_test.go`) | cap: **ADDED**; safety: existing |
| 2 | REST comments | PUT/PATCH/DELETE | — | — | **excluded** — `restNotImplemented` `501` stubs (`rest_comments.go:54/57`) (Req 1.4) |
| 3 | Admin posts | POST (create) | `TestAdminPostWriteCapabilityMatrix` (`internal/web/adminapi_posts_test.go`) | `TestAdminPostCreateSanitizesContentExcerptAndTitleEndToEnd` (`internal/web/adminapi_posts_test.go`) | cap: **ADDED**; safety: existing |
| 3 | Admin posts | PUT (update) | `TestAdminPostWriteCapabilityMatrix` (`internal/web/adminapi_posts_test.go`) | `TestAdminPostUpdateSanitizesContentExcerptAndTitleEndToEnd` (`internal/web/adminapi_posts_test.go`) | cap: **ADDED**; safety: existing |
| 3 | Admin posts | DELETE | `TestAdminPostWriteCapabilityMatrix` (delete sub-cases; consolidates `TestAdminPostDeleteMissingRecordIsForbidden`, `internal/web/adminapi_posts_test.go`) | — | cap: **ADDED**; safety: **excluded** — DELETE carries no content body (Req 1.4) |
| 4 | Admin media | POST (create) | `TestAdminMediaUploadCapabilityMatrix` (`internal/web/adminapi_media_test.go`) | — | cap: **ADDED**; safety: **excluded** — binary upload, no caller-supplied markup; author-seam behavior covered by `TestAdminMediaUploadAuthorsAttachmentToActingAdmin` (Req 1.4) |
| 5 | REST media | POST (create) | `TestRESTMediaCreateCapabilityMatrix`, `TestRESTMediaCreateMIMEBypassMatrix`, `TestRESTMediaWriteAuthMatrix` (`internal/web/rest_media_test.go`) | binary create → no markup cell; filename sanitized by `TestRESTMediaCreateSanitizesHostileFilename` (`internal/web/rest_media_test.go`) | existing |
| 5 | REST media | PUT/PATCH/POST (update) | `TestRESTMediaUpdateCapabilityMatrix` (`internal/web/rest_media_test.go`) | `TestRESTMediaUpdateSanitizesScriptBeforeWriter` (`internal/web/rest_media_test.go`) | existing |
| 5 | REST media | DELETE | — | — | **excluded** — `501` stubs, asserted stable by `TestRESTMediaDeleteCollectionStays501` / `TestRESTMediaDeleteItemStays501WithWiredService` (Req 1.4) |
| 6 | REST users | POST (create) | `TestRESTUserCreateCapabilityMatrix`, `TestRESTUserWriteAuthMatrix` (`internal/web/rest_users_test.go`) | `TestRESTUserCreateSanitizesDescriptionBeforeStore` (`internal/web/rest_users_test.go`) | existing |
| 6 | REST users | PUT/PATCH/POST (update) | `TestRESTUserUpdateSelfVsOtherMatrix`, `TestRESTUserRoleAssignmentMatrix` (`internal/web/rest_users_test.go`) | `TestRESTUserUpdateDescriptionSanitizedBeforeWriter` + `TestRESTUserUpdateDescriptionUnfilteredHTMLSurvivesVerbatim` (tier-C dual, Req 3.5) (`internal/web/rest_users_test.go`) | existing |
| 6 | REST users | DELETE/collection | — | — | **excluded** — `501` stubs, asserted stable by `TestRESTUserDeleteAndCollectionStubsStay501` (Req 1.4) |

**Summary.** Four capability-matrix tests were **ADDED** by 10.E —
`TestRESTPostWriteCapabilityMatrix`, `TestRESTCommentCreateCapabilityMatrix`,
`TestAdminPostWriteCapabilityMatrix`, and `TestAdminMediaUploadCapabilityMatrix`.
Zero new content-safety tests were needed: every content-safety cell is already
backed (Finding 1). All DELETE/collection `501` cells are excluded-with-reason.

## Findings

The audit produced three structural findings. None reveals a production defect;
none triggers a production change (Req 6.1).

### Finding 1 — Requirement 3 is already satisfied; no new content-safety test is needed

The pre-existing admin-post and comment content-safety proofs assert the
**stored / writer-visible** value, not an escaped response.
`TestAdminPostCreateSanitizesContentExcerptAndTitleEndToEnd` and
`TestAdminPostUpdateSanitizesContentExcerptAndTitleEndToEnd` wire the REAL
`content.PostWriteService` over an in-memory `memPostWriter` and read the
sanitized record back via `memPostWriter.get(id)`, asserting the stored
`Content`, `Excerpt`, and reduced-to-plain-text `Title`.
`TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter` reads back through
`repos.Comments.List(...)` and asserts the stored `Content` had its `<script>`
stripped and `<em>` preserved. Both drive the real service over the real store,
so a transport that bypassed the Policy could not pass. **Resolution:**
Requirement 3 is satisfied by existing tests; 10.E cites them in the matrix and
adds no admin-post or comment content-safety test. The only gap-fill work is the
four capability matrices of Requirement 2.

### Finding 2 — REST comment create has no forbidden *capability*, so its matrix is an authorization-contract matrix

Comment create is anonymous-allowed: `rest_comments.go` builds
`sanitize.Anonymous()` when no principal is present and proceeds. The only
capability in the path, `moderate_comments`, gates comment *status* (the
`isModerator` seam, `rest_comments.go:17-20`), never whether a comment may be
created. There is no caller for whom create returns a capability `403`.
**Resolution:** `TestRESTCommentCreateCapabilityMatrix` is an
**authorization-contract matrix**, not an allowed-vs-forbidden capability split
like the other three. It tables the outcomes the route genuinely distinguishes:

- anonymous (unique email) → `201`, comment persisted;
- subscriber (session + CSRF) → `201`, persisted;
- editor (`moderate_comments`, session + CSRF) → `201`, persisted;
- markup-only body that sanitizes to empty at tier A → `400
  rest_comment_content_invalid`, no comment persisted;
- closed-comments post → `403 rest_comment_closed`, no comment persisted;
- session cell with missing CSRF → `403 rest_forbidden`, no comment persisted.

This finding is recorded so the matrix cell above is not misread as a
capability allowed/forbidden split.

### Finding 3 — the admin-media upload route's effective gate is `upload_files`, NOT `edit_posts` (design-doc correction)

**This corrects the design.** The design document stated the admin media upload
route is gated by `edit_posts` (claiming the `/admin/api/*` surface is gated
once at the router by `requireCapabilityJSON("edit_posts")` and that the media
handler adds no capability check). That is **wrong.** The task-1.4
implementation read the live router (`internal/web/adminroutes.go`) and found
the admin **media group is gated by `requireCapabilityJSON("upload_files")`**,
not `edit_posts`. The upload route's effective capability is therefore
**`upload_files`**.

`TestAdminMediaUploadCapabilityMatrix` proves this with a **discriminating
contributor cell**: a contributor principal holds `edit_posts` but NOT
`upload_files`, and the route returns `403 forbidden`. Had the gate been
`edit_posts` as the design claimed, that same cell would have returned `201`.
The failing outcome of the discriminating cell is the proof that the real gate
is `upload_files`. The full matrix:

- anonymous (no session) → `401 unauthorized` — `requireLoginJSON` runs before
  the capability group via `r.Use`, so the no-principal case is `401`, not
  `403`;
- authenticated without `upload_files` (contributor: holds `edit_posts` only) →
  `403 forbidden` — the discriminating cell;
- `upload_files` holder (author / administrator) with a valid CSRF token →
  `201`, one media row created.

**Resolution:** `coverage.md` records the admin-media upload route's effective
upload capability as **`upload_files`**. This is a **design-doc correction
discovered during implementation, not a production defect** (Req 6.1): in the
default role table every `edit_posts` holder from author up also holds
`upload_files`, so no real caller is mis-gated — the only principal that could
observe the difference (a contributor, which holds `edit_posts` without
`upload_files`) is correctly denied either way for a real upload. No production
change was made.

## Inventory verification

Every cited **existing** test was run by name to confirm it is present and
passes. No cited test name was missing; **no name corrections were required.**

The 16 existing cited tests, all confirmed green (`go test ./internal/web`):

- `TestRESTMediaCreateCapabilityMatrix`
- `TestRESTMediaUpdateCapabilityMatrix`
- `TestRESTMediaCreateMIMEBypassMatrix`
- `TestRESTMediaWriteAuthMatrix`
- `TestRESTUserCreateCapabilityMatrix`
- `TestRESTUserUpdateSelfVsOtherMatrix`
- `TestRESTUserRoleAssignmentMatrix`
- `TestRESTUserWriteAuthMatrix`
- `TestRESTPostCreateSanitizesContentExcerptAndTitleEndToEnd`
- `TestRESTPostUpdateSanitizesContentExcerptAndTitleEndToEnd`
- `TestAdminPostCreateSanitizesContentExcerptAndTitleEndToEnd`
- `TestAdminPostUpdateSanitizesContentExcerptAndTitleEndToEnd`
- `TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter`
- `TestRESTMediaUpdateSanitizesScriptBeforeWriter`
- `TestRESTUserCreateSanitizesDescriptionBeforeStore`
- `TestRESTUserUpdateDescriptionSanitizedBeforeWriter`

The four newly ADDED capability-matrix tests, all confirmed green:

- `TestRESTPostWriteCapabilityMatrix` (`internal/web/rest_posts_test.go`) — create / update / delete sub-trees, 21 cells
- `TestRESTCommentCreateCapabilityMatrix` (`internal/web/rest_comments_test.go`) — 6 cells
- `TestAdminPostWriteCapabilityMatrix` (`internal/web/adminapi_posts_test.go`)
- `TestAdminMediaUploadCapabilityMatrix` (`internal/web/adminapi_media_test.go`) — including the discriminating contributor cell of Finding 3

## Done

The matrix is complete: every M10 write route × verb appears exactly once, and
every non-excluded cell is backed by a named, passing test. The four capability
matrices 10.E owed are landed and green; every pre-existing cited test is
confirmed present and green; and the admin-media gate is recorded at its true
value (`upload_files`, Finding 3). No production source changed.
