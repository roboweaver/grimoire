# Requirements Document

M10.B — REST Media Write Parity

## Introduction

M10a shipped the write-boundary content policy. `internal/sanitize` now holds a
capability-aware allow-list Policy with three tiers (A/B/C) and four field kinds
(`PostContent`, `PostExcerpt`, `PostTitle`, `CommentContent`), and
`content.PostWriteService` / `content.CommentService` route every byte they
persist through it, failing closed on any policy error. What M10a deliberately
did **not** do was enable any new REST write route; the roadmap's
`tasks.md:788-792` makes that ordering load-bearing — the Policy had to exist
and be proven first.

This milestone (10.B) is the first consumer to turn on a new REST write surface.
The WordPress media endpoints are live for reads but every write verb is a
`501` stub today. `internal/web/rest_media.go` registers `GET /media` and
`GET /media/{id}` against real handlers, then registers `restNotImplemented`
stubs for `POST /media`, `PUT`/`PATCH`/`DELETE /media`, and
`POST`/`PUT`/`PATCH`/`DELETE /media/{id}` — including, deliberately,
`POST /media/{id}`, because WordPress treats `POST` on a single item as an
update. 10.B replaces exactly two of those stub groups with real handlers:

| Route | Verb(s) | 10.B behavior |
|---|---|---|
| `/media` | `POST` | **Create** an attachment from a raw binary upload |
| `/media/{id}` | `PUT`, `PATCH`, `POST` | **Update** an attachment's editable metadata |
| `/media`, `/media/{id}` | `DELETE` | **Unchanged** — stays `501` (out of scope) |

Every write routes through M10a's existing Policy before any byte is persisted,
and every capability decision is evaluated against the stored attachment row,
never a forged request body — the same posture `content.PostWriteService`
already enforces (`internal/content/writeservices.go`).

This refines **roadmap group 10.B only**, tracing to roadmap **Requirement 16**
(`../wordpress-core-parity-roadmap/requirements.md:521`). Groups 10.C
(REST user write), 10.D (`content.rendered` improvements), 10.E (M10 test
coverage), 10.F and 10.G are explicitly out of scope (see "Out of scope").

## Glossary

- **The Policy** — M10a's `sanitize.Policy` (`internal/sanitize/policy.go`), the
  single sanitization component. `Policy.Sanitize(kind, writer, in)` returns the
  value that may be persisted at the tier `TierFor` selects, and fails closed:
  on any non-nil error the caller MUST abandon the write.
- **Media_Write_Service** — the capability-checked application service this
  milestone adds, over `content.MediaService` and the media writer ports. It is
  the media analogue of `content.PostWriteService`: it owns the capability
  checks, the Policy calls, and the ownership evaluation against the stored row.
- **MediaService** — the existing `content.MediaService`
  (`internal/content/media.go`). Its `Store` method handles binary upload,
  filename sanitization, year/month directory layout, filename de-duplication,
  the MIME allow-list default, and DB-rollback (file removal) on a failed
  insert. This milestone REUSES `Store` for create and does not reinvent upload
  handling.
- **MediaWriter** — the `domain.MediaWriter` port
  (`internal/domain/repository.go:222`). Today it has `Create` and `SetParent`
  only. This milestone requires a NEW metadata-update method on this port (see
  Requirement 3).
- **Attachment** — a WordPress media item: a `{prefix}posts` row with
  `post_type = "attachment"`, backed by `_wp_attached_file` postmeta. Modeled by
  `domain.Media` (`internal/domain/entities.go:126`).
- **Editable metadata set** — the attachment fields this milestone allows a
  caller to update: **title**, **caption**, **alt_text**, **description**, and
  **post** (parent). These map to the attachment's `post_title`, `post_excerpt`,
  `_wp_attachment_image_alt` postmeta, `post_content`, and `post_parent`
  respectively.
- **The REST writes** — `POST /media` (create) and
  `PUT`/`PATCH`/`POST /media/{id}` (update), the only two write surfaces this
  milestone enables.
- **unfiltered_upload** — the WordPress capability held only by the
  administrator role (`internal/auth/roles.go:95`). A holder bypasses the MIME
  allow-list on upload.

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | Create accepts a **raw binary request body only** (`Content-Type: <mime>` + `Content-Disposition: attachment; filename="..."`), WordPress's primary REST upload path. Create is **binary-only**: it carries no editable metadata (title/caption/alt_text/description) inline — those are set on a follow-up update (Requirement 2), matching WordPress's REST media flow. | `multipart/form-data` is OUT of scope for 10.B. The body streams through `MediaService.Store`; metadata is edit-on-update. |
| D7 | Create **threads the uploading principal** so a new attachment's `post_author` is the uploader's user ID (today `MediaService.Store` leaves it 0). | The uploader can edit their own media under `edit_posts` rather than every edit falling to the `edit_others_posts` branch (Requirement 5). `MediaService.Store`/`MediaWriter.Create` gain an author parameter; no DB migration (`post_author` is an existing column). |
| D2 | Update covers **title, caption, alt_text, description, post (parent)**. | A new `domain.MediaWriter` metadata-update method is required (Requirement 3). |
| D3 | Field-kind mapping reuses M10a kinds exactly: title → `PostTitle`, caption → `PostExcerpt`, description → `PostContent`, alt_text → plain-text strip (title path). | No new field kinds; `unfiltered_html` holders get tier C for the markup fields exactly as elsewhere. |
| D4 | Capability model is WordPress-faithful and reuses existing M2 capabilities: `upload_files` gates create; own-attachment update needs `edit_posts`; other-user update needs `edit_others_posts`; `unfiltered_upload` bypasses the MIME allow-list. | No new capability names are introduced. |
| D5 | `DELETE /media` and `DELETE /media/{id}` keep their existing `501` stubs. | Delete is NOT reinterpreted as a write this milestone. |
| D6 | Policy applies BEFORE any write/persist, same fail-closed posture as M10a. | A policy error means no write happens; it maps to a `400`. |

## Requirements

### Requirement 1: Create an attachment via raw binary upload

**User Story:** As an integrator using WordPress REST tooling, I want to upload a
file with `POST /wp-json/wp/v2/media` using a raw binary body, so that existing
WordPress media-upload clients work against grimoire unchanged.

#### Acceptance Criteria

1. WHEN a caller holding `upload_files` sends `POST /media` with a raw binary
   body, a `Content-Type` header, and a `Content-Disposition: attachment;
   filename="..."` header, THE Media_Write_Service SHALL stream the body through
   `content.MediaService.Store` and SHALL create one attachment.
2. WHEN a `POST /media` create succeeds, THE REST media endpoint SHALL respond
   `201` with the same envelope shape `GET /media/{id}` returns (the
   `mapRESTMedia` pipeline: mapped item plus `_links` for `self`, `collection`,
   and `author`, plus `up` when the attachment has a parent).
3. THE `POST /media` create path SHALL be **binary-only**: it SHALL NOT accept
   inline editable metadata (`title`, `caption`, `alt_text`, `description`) in
   the request, and SHALL derive the attachment's initial `title` from the
   `Content-Disposition` filename the way `MediaService.Store` already does.
   Editing those fields is done on a follow-up update (Requirement 2). Because no
   caller-supplied metadata string enters on create, the Policy is not invoked on
   the create path (the derived title is a filename, not user markup); all
   metadata sanitization happens on update per Requirement 4.
4. WHEN a `POST /media` create succeeds, THE Media_Write_Service SHALL set the
   new attachment's `post_author` to the uploading caller's resolved user ID
   (D7), so a later update by that same caller is authorized through the
   own-attachment (`edit_posts`) branch of Requirement 5 rather than the
   others' (`edit_others_posts`) branch.
5. IF a `POST /media` request omits the `Content-Disposition` filename, THEN THE
   REST media endpoint SHALL respond `400` with code `rest_upload_no_content_disposition`
   and SHALL NOT create an attachment.
6. IF a `POST /media` request body is empty, THEN THE REST media endpoint SHALL
   respond `400` with code `rest_upload_no_data` and SHALL NOT create an
   attachment.

### Requirement 2: Update an attachment's editable metadata

**User Story:** As an integrator using WordPress REST tooling, I want to update an
attachment's title, caption, alt text, description, and parent with
`PUT`/`PATCH`/`POST /wp-json/wp/v2/media/{id}`, so that media metadata editing
works through the REST API.

#### Acceptance Criteria

1. WHEN a caller authorized per Requirement 5 sends `PUT`, `PATCH`, or
   `POST /media/{id}` with any subset of `title`, `caption`, `alt_text`,
   `description`, and `post`, THE Media_Write_Service SHALL load the stored
   attachment, apply the Policy to each supplied field at its mapped field kind,
   persist the changed metadata via the new `domain.MediaWriter` update method,
   and respond `200` with the same envelope shape `GET /media/{id}` returns.
2. WHEN a `PUT`/`PATCH`/`POST /media/{id}` request supplies `post` (the parent
   post ID), THE Media_Write_Service SHALL set the attachment's parent via the
   existing `domain.MediaWriter.SetParent` port.
3. WHILE a request omits a given editable field, THE Media_Write_Service SHALL
   leave that field's stored value unchanged (partial update), matching the REST
   post write semantics in `internal/web/rest_posts.go`.
4. WHEN a request supplies a field whose value is byte-identical to the stored
   value, THE Media_Write_Service SHALL pass that field through to persistence
   unchanged and SHALL NOT re-sanitize it, so pre-existing or imported attachment
   metadata is never rewritten in place (matching
   `PostWriteService.sanitizeIncoming`).
5. IF the `{id}` path segment does not resolve to an existing attachment, THEN
   THE REST media endpoint SHALL respond `404` with code `rest_post_invalid_id`
   and SHALL NOT persist any change.
6. IF a `PUT`/`PATCH`/`POST /media/{id}` request supplies a `post` parent ID that
   is non-numeric or negative, THEN THE REST media endpoint SHALL respond `400`
   with code `rest_invalid_param` and SHALL NOT persist any change.

### Requirement 3: New MediaWriter metadata-update port

**User Story:** As a maintainer, I want attachment metadata updates to flow
through a dedicated domain port, so that the storage layer owns the write and the
service layer stays persistence-agnostic.

#### Acceptance Criteria

1. THE `domain.MediaWriter` interface (`internal/domain/repository.go:222`) SHALL
   gain one metadata-update method that accepts an attachment ID and the editable
   metadata set (title, caption, alt_text, description) and persists them to the
   attachment's existing storage locations.
2. THE new method SHALL write `title` to `post_title`, `caption` to
   `post_excerpt`, `description` to `post_content` on the attachment's
   `{prefix}posts` row, and `alt_text` to the `_wp_attachment_image_alt`
   `{prefix}postmeta` row for that attachment.
3. THE new method SHALL be additive and SHALL NOT require a database migration,
   because every target field already lives in an existing column
   (`post_title`, `post_excerpt`, `post_content` on `{prefix}posts`, present
   since migration `0001`) or in the already-used `{prefix}postmeta` table.
4. WHEN the attachment ID does not exist, THE new method SHALL return
   `domain.ErrNotFound`, matching the existing `SetParent` contract.
5. THE parent (`post`) update SHALL continue to use the existing `SetParent`
   method rather than being folded into the new method, matching D2.
6. THE `domain.MediaWriter.Create` signature (and `content.MediaService.Store`)
   SHALL thread the uploading caller's user ID so a created attachment's
   `post_author` is set to it (D7), rather than defaulting to `0`. This is
   additive and requires no migration (`post_author` is an existing
   `{prefix}posts` column). Existing non-REST callers that pass no principal
   retain today's behavior (author `0`).

### Requirement 4: Sanitize-policy field-kind mapping

**User Story:** As a security reviewer, I want each editable attachment field
sanitized at the same tier M10a applies to the equivalent WordPress field, so
that media writes carry no weaker a safety guarantee than post and comment
writes.

#### Acceptance Criteria

1. WHEN sanitizing a `title`, THE Media_Write_Service SHALL call the Policy with
   field kind `sanitize.PostTitle` (plain-text reduction).
2. WHEN sanitizing a `caption`, THE Media_Write_Service SHALL call the Policy
   with field kind `sanitize.PostExcerpt`.
3. WHEN sanitizing a `description`, THE Media_Write_Service SHALL call the Policy
   with field kind `sanitize.PostContent`.
4. WHEN sanitizing `alt_text`, THE Media_Write_Service SHALL call the Policy with
   field kind `sanitize.PostTitle`, so alt text is reduced to plain text with no
   surviving markup, matching the title path.
5. WHERE the acting caller holds `unfiltered_html`, THE Policy SHALL select tier
   C for the markup fields (`caption`, `description`) exactly as it does for post
   content and excerpt elsewhere, because the Policy's tier selection reads
   `unfiltered_html` through `Principal.Can` and takes no media-specific input.
6. THE Media_Write_Service SHALL build the Policy's writer argument from the
   resolved principal via `sanitize.For(principal)`, and SHALL use
   `sanitize.Anonymous()` only when no principal is present.
7. IF the Policy returns a non-nil error for any field, THEN THE REST media
   endpoint SHALL respond `400` and SHALL NOT persist any value for the request
   (fail closed), matching M10a's sentinel-to-400 mapping.

### Requirement 5: Capability model for create and update

**User Story:** As a site administrator, I want media writes gated by the same
WordPress capabilities my roles already grant, so that authorization behaves
exactly as it does in WordPress.

#### Acceptance Criteria

1. WHEN a caller lacking `upload_files` sends `POST /media`, THE REST media
   endpoint SHALL respond `403` with code `rest_cannot_create` and SHALL NOT
   create an attachment.
2. WHEN a caller holding `upload_files` (author, editor, or administrator) sends
   `POST /media`, THE Media_Write_Service SHALL permit the create (subject to the
   remaining requirements).
3. WHEN a caller sends `PUT`/`PATCH`/`POST /media/{id}` for an attachment whose
   stored `post_author` equals the caller's user ID, THE Media_Write_Service
   SHALL require `edit_posts` and SHALL respond `403` (code `rest_cannot_edit`)
   if the caller lacks it.
4. WHEN a caller sends `PUT`/`PATCH`/`POST /media/{id}` for an attachment whose
   stored `post_author` differs from the caller's user ID, THE
   Media_Write_Service SHALL require `edit_others_posts` and SHALL respond `403`
   (code `rest_cannot_edit`) if the caller lacks it.
5. THE Media_Write_Service SHALL evaluate every update capability check against
   the stored attachment row's `post_author`, loaded by ID, and SHALL NEVER use
   an author value taken from the request body, matching
   `content.PostWriteService.Update`.
6. IF an unauthenticated caller (no session, no Application Password) sends any
   of The REST writes, THEN THE REST media endpoint SHALL respond `403` (the
   caller holds no capability), with code `rest_cannot_create` for create and
   `rest_cannot_edit` for update, matching the forbidden-write convention in
   `internal/web/rest_posts.go` (which returns `http.StatusForbidden` for a
   capability failure rather than a distinct `401`), and SHALL NOT create or
   modify any attachment.

### Requirement 6: MIME allow-list and unfiltered_upload bypass

**User Story:** As a site administrator, I want uploads restricted to the
configured MIME allow-list for ordinary users while administrators can upload any
type, so that media uploads match WordPress's `unfiltered_upload` behavior.

#### Acceptance Criteria

1. WHEN a caller without `unfiltered_upload` sends `POST /media` with a body
   whose detected MIME type is not in `content.MediaConfig.AllowedMIMEs`, THE
   REST media endpoint SHALL respond `415` with code `rest_upload_unsupported_media_type`
   and SHALL NOT create an attachment.
2. WHERE the acting caller holds `unfiltered_upload` (administrator only, per
   `internal/auth/roles.go:95`), THE Media_Write_Service SHALL skip the MIME
   allow-list check and SHALL permit the upload regardless of detected MIME type.
3. WHEN `content.MediaConfig.AllowedMIMEs` is empty, THE Media_Write_Service
   SHALL treat the upload as unrestricted for every caller, matching the existing
   `mimeAllowed` "no allow-list configured" behavior in
   `internal/web/adminapi_media.go`.
4. THE MIME allow-list decision SHALL be made against the server-detected content
   type (as the admin upload path sniffs via `http.DetectContentType`), not a
   client-asserted `Content-Type` the server trusts blindly.

### Requirement 7: Upload size limit

**User Story:** As a site operator, I want oversized uploads rejected, so that a
single request cannot exhaust server resources.

#### Acceptance Criteria

1. WHEN a `POST /media` request body exceeds
   `content.MediaConfig.MaxUploadSize` (10 MiB default, set by
   `NewMediaService`), THE REST media endpoint SHALL reject the upload with
   `413` and code `rest_upload_file_too_big` and SHALL NOT create an attachment.
2. THE REST media endpoint SHALL enforce the size cap using
   `http.MaxBytesReader` against the configured `MaxUploadSize`, matching the
   admin upload path in `internal/web/adminapi_media.go`.

### Requirement 8: Authentication consistent with M10a REST writes

**User Story:** As an integrator, I want media writes to authenticate exactly
like the comment and post REST writes, so that one auth integration covers every
REST write surface.

#### Acceptance Criteria

1. WHEN a request to any of The REST writes is authenticated by an Application
   Password, THE REST media endpoint SHALL skip the CSRF check, matching
   `internal/web/rest_posts.go` and `internal/web/rest_comments.go`.
2. WHEN a request to any of The REST writes is authenticated by a session cookie,
   THE REST media endpoint SHALL enforce the M4 CSRF synchronizer token via
   `requireSessionCSRFREST` and SHALL respond `403` with code `rest_forbidden`
   when the token is missing or invalid.
3. THE REST media endpoints SHALL perform no per-handler Policy call beyond what
   the Media_Write_Service performs, so sanitization lives in exactly one place
   on the write path.

### Requirement 9: DELETE stays 501

**User Story:** As a maintainer, I want media delete to remain explicitly
unimplemented, so that disabling it is a visible contract rather than a silent
404 or 405.

#### Acceptance Criteria

1. THE registration for `DELETE /media` SHALL remain the existing
   `restNotImplemented` stub and SHALL respond `501`.
2. THE registration for `DELETE /media/{id}` SHALL remain the existing
   `restNotImplemented` stub and SHALL respond `501`.
3. THE DELETE routes SHALL NOT be reinterpreted as writes and SHALL NOT invoke
   the Media_Write_Service.

### Requirement 10: No read-path or rendering regression

**User Story:** As a REST API consumer, I want existing media reads to behave
exactly as before, so that enabling writes does not change what GET returns.

#### Acceptance Criteria

1. THE `GET /media` and `GET /media/{id}` handlers SHALL continue to return the
   same collection and single-item responses, with unchanged `_links`,
   `_embedded`, pagination headers, and status codes.
2. THE existing `content.rendered` behavior and its read/write asymmetry SHALL
   NOT change as a result of this milestone.
3. WHEN an attachment is created or updated through The REST writes, THE
   subsequent `GET /media/{id}` SHALL return the sanitized stored values, so the
   read path reflects exactly what the write path persisted.

### Requirement 11: Correctness and safety invariants

**User Story:** As a security reviewer, I want the media write path to carry the
same end-to-end guarantees M10a established, so that the new surface cannot
become a sanitization or authorization bypass.

#### Acceptance Criteria

1. THE Media_Write_Service SHALL sanitize every title, caption, description, and
   alt_text value at the writer's tier before that value reaches persistence; no
   create or update SHALL persist an unsanitized caller-supplied metadata value.
2. THE Media_Write_Service SHALL evaluate every capability decision against the
   stored attachment row, never a request body, so a forged `author` or `post`
   field cannot escalate privileges.
3. IF the Policy returns an error OR a capability check fails OR the upload
   violates the MIME or size limit, THEN THE Media_Write_Service SHALL perform no
   write and the stored state SHALL be unchanged (fail closed).
4. WHEN a create's underlying `MediaService.Store` fails after writing the file,
   THE existing `Store` rollback (file removal on insert failure) SHALL remain in
   effect, so a failed create leaves neither an orphaned file nor an orphaned
   row.

## Out of scope

- `multipart/form-data` uploads (D1) — a later addition after 10.B.
- `DELETE /media` and `DELETE /media/{id}` (D5) — remain `501`.
- Roadmap group 10.C (REST user write parity, roadmap Requirement 17).
- Roadmap group 10.D (`content.rendered` improvements, roadmap Requirement 18).
- Roadmap groups 10.E, 10.F, 10.G.
- Any database migration (Requirement 3.3 confirms none is needed).
- Image sub-size / thumbnail regeneration and `_wp_attachment_metadata`
  (re)generation — not part of the editable metadata set.

## Traceability

This spec refines **roadmap group 10.B** and traces to roadmap **Requirement 16**
(`../wordpress-core-parity-roadmap/requirements.md:521`). It depends on M10a
(roadmap group 10.A / Requirement 15), whose `internal/sanitize` Policy it reuses
without modification.
