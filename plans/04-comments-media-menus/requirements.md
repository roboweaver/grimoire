# Requirements — M4: Comments, Media Library, Navigation Menus

## Introduction

This milestone extends grimoire from a read-only content and admin platform into
one that also handles **comments**, a **media library**, and **navigation menus**
— three of the pillars of a working WordPress site. It builds directly on M1's
content core, M2's authentication/roles/capabilities/CSRF substrate, and M3's
Adobe React Spectrum admin SPA.

M4 is the milestone where grimoire first serves **state-changing HTTP requests**:

- a **public visitor** can read a post's approved comments and **submit** a new
  comment (which enters a moderation queue rather than publishing immediately);
- an **editor** can **moderate** comments (approve / unapprove / spam / trash) in
  the Spectrum admin;
- an **author/editor** can **upload media** and **attach** it to a post; and
- the site can **read and render WordPress navigation menus** in the public theme,
  with a **read-only** menu view in the admin.

Because comment submission is grimoire's first **anonymous** write path and admin
moderation/upload are its first **authenticated** write paths, this milestone also
**activates the CSRF contract** that M3 designed but never exercised: authenticated
unsafe requests validate the per-session synchronizer token via an
`X-CSRF-Token` header, and the anonymous public comment form uses a double-submit
token so the first write path is not a CSRF hole.

WordPress compatibility remains a **schema/behavior contract**. Comments read and
write the WordPress `{prefix}comments` + `{prefix}commentmeta` tables; media are
`{prefix}posts` rows with `post_type='attachment'` plus the `_wp_attached_file`
postmeta; navigation menus are the `nav_menu` taxonomy over `{prefix}terms`,
`{prefix}term_taxonomy`, and `{prefix}term_relationships` with `nav_menu_item`
posts. Reads overlay an existing, populated WordPress database with **no schema
change**; only greenfield (grimoire-provisioned) databases get an additive
migration that creates the comments tables (`IF NOT EXISTS`, exactly like M2's
`{prefix}sessions` migration contract) **and** adds the `{prefix}posts` columns —
`comment_status`, `post_parent`, `post_mime_type`, `menu_order` — that M1's
greenfield schema omits but M4's comment-closed check, media fields, and
menu-item ordering all depend on, mirroring M2's `{prefix}users` column-migration
contract (`ALTER TABLE ADD COLUMN`, never pointed at an overlaid live WordPress
database, which already has every core column).

Out of scope for M4: **creating or editing** navigation menus (read-only this
milestone; the drag-and-drop menu editor is deferred to a later menus milestone);
threaded-comment depth limits, comment editing by the public, and permanent
(hard) comment deletion (`trash` is a soft-delete status only, restorable via
untrash — Req 4.7–4.9); nested media folders / cloud object-store drivers (a
single configurable on-disk uploads root plus a documented proxy alternative is
the M4 surface); image resizing / thumbnail generation; the full WordPress REST
API surface (milestone 05); and the rich post editor / post create-update-delete
(milestone 06).

## Requirements

### Requirement 1 — Public comment list on a post

**User Story:** As a site visitor, I want to read the approved comments on a post,
so that I can see the discussion beneath the content.

#### Acceptance Criteria
1. THE system SHALL read comments for a post from the WordPress `{prefix}comments` table via an additive, read-only port, ordered oldest-first by `comment_date` (matching WordPress's default thread order).
2. THE public single-post view SHALL render only **approved** comments (`comment_approved = '1'`); held (`'0'`), spam (`'spam'`), and trashed (`'trash'`) comments SHALL NOT appear to the public.
3. THE rendered comment SHALL display at least the author display name, the comment date, and the comment content, with the author URL (when present) linked safely (`rel="nofollow ugc"`).
4. WHEN a post has no approved comments, THEN the view SHALL render an empty-comments state (e.g. "No comments yet") rather than omitting the section or erroring.
5. THE comment content SHALL be treated as **untrusted** input and SHALL be HTML-escaped (or run through an allowlist sanitizer) before rendering, unlike trusted post content — an anonymous commenter SHALL NOT be able to inject markup or script.
6. THE comment count SHALL reflect only approved comments and SHALL be available to the template for display (e.g. "3 Comments").

### Requirement 2 — Public comment submission into a moderation queue

**User Story:** As a site visitor, I want to submit a comment on a post, so that I
can join the discussion.

#### Acceptance Criteria
1. THE system SHALL expose `POST /comments` (or `POST /{post}/comments`) accepting a comment form with fields for author name, author email, optional author URL, and comment content, plus the target post ID.
2. WHEN a comment is submitted, THEN the system SHALL persist it to `{prefix}comments` with `comment_approved = '0'` (held for moderation) by default, so nothing a stranger writes is published without review.
3. IF the target post does not exist, is not published, or has comments closed (`comment_status = 'closed'`), THEN the system SHALL reject the submission with an appropriate status (`404` for missing/unpublished, `403` for closed) and SHALL NOT create a comment row.
4. IF required fields (author name, author email, non-empty content) are missing or the email is malformed, THEN the system SHALL reject the submission with `400` and a human-readable message, re-rendering the form with the entered values preserved (for the server-rendered path).
5. THE system SHALL record the commenter's `comment_author_IP`, `comment_agent`, and `comment_date`/`comment_date_gmt`, and SHALL associate the comment with the authenticated user id (`user_id`) when the submitter is logged in, or `0` when anonymous.
6. WHEN a comment is stored successfully, THEN the system SHALL respond with a redirect (`303`) back to the post with a "comment awaiting moderation" indication (server-rendered path) OR a `201` JSON body (API path), and SHALL NOT echo the raw submitted HTML back unescaped.
7. THE submission handler SHALL apply the spam-filter hook (Req 3) and the anti-CSRF token check (Req 12) before persisting.

### Requirement 3 — Spam filtering hook

**User Story:** As a site owner, I want submitted comments screened for spam, so
that obvious junk is caught before it reaches the moderation queue.

#### Acceptance Criteria
1. THE system SHALL define a **spam-filter hook** — a Go interface (e.g. `CommentSpamFilter`) evaluated for every public submission before the comment is stored — so anti-spam strategies are pluggable without touching the handler.
2. THE default filter SHALL implement at least: a **honeypot** field check (a hidden field that must stay empty), a basic **rate limit** per IP, and a configurable **link-count / keyword** heuristic; a submission the filter marks as spam SHALL be stored with `comment_approved = 'spam'` (not silently dropped) so a moderator can review false positives.
3. WHEN the filter marks a submission as spam, THEN the system SHALL still return a success-shaped response to the client (to avoid signaling spammers) while quarantining the row as `'spam'`.
4. THE filter interface SHALL receive enough context (author, email, URL, content, IP, user-agent, target post) to make a decision, and SHALL be able to return `approve` / `hold` / `spam` outcomes.
5. THE default filter SHALL be safe against a live WordPress DB (read/insert only) and SHALL NOT require any schema beyond `{prefix}comments`/`{prefix}commentmeta`.

### Requirement 4 — Admin comment moderation

**User Story:** As an editor, I want to review and moderate comments in the admin,
so that I can approve legitimate discussion and remove spam.

#### Acceptance Criteria
1. THE system SHALL expose `GET /admin/api/comments` returning a paginated JSON list of comments across all posts, filterable by status (`hold`, `approved`, `spam`, `trash`) and by post, with fields sufficient for a moderation table: `id`, `postId`, `postTitle`, `author`, `authorEmail`, `date`, `status`, and an excerpt of the content.
2. THE moderation list endpoint SHALL require the `moderate_comments` capability; an authenticated user lacking it SHALL receive `403` (JSON), and an anonymous request SHALL receive `401` (JSON, never a redirect).
3. THE system SHALL expose an unsafe endpoint to change a comment's state — approve, unapprove (hold), mark spam, and trash/untrash — e.g. `POST /admin/api/comments/{id}/status` with the target status in the body; it SHALL require `moderate_comments` AND a valid `X-CSRF-Token` (Req 12).
4. WHEN a moderator approves a held comment, THEN the change SHALL be persisted to `{prefix}comments` (`comment_approved = '1'`) and the comment SHALL immediately appear in the public list for its post; WHEN trashed, it SHALL disappear from the public list.
5. WHEN no comment matches the given id, THEN the status endpoint SHALL return `404` with a JSON error body.
6. THE moderation endpoints SHALL return identical shapes/behavior across MySQL, PostgreSQL, and SQLite, and SHALL NOT leak SQL text, driver errors, or secrets.
7. WHEN a comment is trashed (`status` transitions to `'trash'`), THEN the system SHALL first save its current `comment_approved` value to `{prefix}commentmeta` (`_wp_trash_meta_status`, plus a `_wp_trash_meta_time` timestamp) before overwriting `comment_approved` to `'trash'`, matching WordPress's `wp_trash_comment()` contract, so the comment can later be restored to its exact prior state.
8. WHEN a trashed comment is untrashed (`status = 'untrash'`, or any target status transition away from `'trash'`), THEN the system SHALL restore `comment_approved` from the saved `_wp_trash_meta_status` value (defaulting to `'0'`/held if none was saved) and SHALL remove the `_wp_trash_meta_status`/`_wp_trash_meta_time` commentmeta keys, matching WordPress's `wp_untrash_comment()` contract.
9. `trash` SHALL remain a **soft-delete** status only: the comment row is never physically deleted by M4. A permanent hard-delete action is **out of scope** for this milestone (deferred, consistent with the menu-editing deferral in Req 11.6).

### Requirement 5 — Comments schema and existing-WP-DB overlay

**User Story:** As a developer, I want comments to use the real WordPress comment
schema, so that grimoire reads and writes an existing WordPress database faithfully.

#### Acceptance Criteria
1. THE comment data model SHALL map to the WordPress `{prefix}comments` columns (`comment_ID`, `comment_post_ID`, `comment_author`, `comment_author_email`, `comment_author_url`, `comment_author_IP`, `comment_date`, `comment_date_gmt`, `comment_content`, `comment_approved`, `comment_agent`, `comment_parent`, `user_id`) and to `{prefix}commentmeta` (`meta_id`, `comment_id`, `meta_key`, `meta_value`).
2. WHEN grimoire overlays an existing, populated WordPress database, THEN comment reads and moderation writes SHALL operate on its existing `{prefix}comments`/`{prefix}commentmeta` tables **without any migration or schema change**.
3. FOR a greenfield (grimoire-provisioned) database only, THE system SHALL provide an additive `IF NOT EXISTS` migration (per vendor: MySQL, PostgreSQL, SQLite) creating `{prefix}comments` and `{prefix}commentmeta`, following the M2 `{prefix}sessions` migration contract (a live WordPress DB already has these tables and is not re-migrated).
4. THE `comment_approved` field SHALL be modeled as WordPress stores it — a string enum (`'0'`, `'1'`, `'spam'`, `'trash'`) — not a boolean, so round-trips are lossless.
5. THE new comment ports SHALL be defined in `internal/domain` and implemented per-vendor in `internal/storage/wprepo`, with **no database driver imported** by the domain, content, or web layers.

### Requirement 6 — Admin media library listing

**User Story:** As an editor, I want to browse uploaded media in the admin, so that
I can see and reuse the site's images and files.

#### Acceptance Criteria
1. THE system SHALL expose `GET /admin/api/media` returning a paginated JSON list of attachments — `{prefix}posts` rows where `post_type = 'attachment'` — newest first, with fields: `id`, `title`, `filename`, `url`, `mimeType`, `date`, and `parentId` (the post it is attached to, or `0`).
2. THE media list endpoint SHALL require the `upload_files` capability (author and above); an authenticated user lacking it SHALL receive `403` (JSON), anonymous SHALL receive `401` (JSON).
3. THE public URL for each attachment SHALL be derived from its `_wp_attached_file` postmeta joined to the configured uploads base URL, matching how WordPress resolves attachment URLs.
4. THE listing SHALL be computed via additive, read-only `SELECT`/`COUNT` queries introducing **no** schema change, and SHALL work unchanged against an existing WordPress database's attachments.
5. THE endpoint SHALL return pagination metadata (`page`, `perPage`, `total`, `totalPages`) consistent with the M3 posts listing.

### Requirement 7 — Public serving of `/wp-content/uploads/`

**User Story:** As a site visitor, I want images embedded in posts to load, so that
the rendered pages look complete.

#### Acceptance Criteria
1. THE system SHALL serve files under the `/wp-content/uploads/` URL path from a **configurable on-disk uploads directory** (default `wp-content/uploads`), so `<img>` tags already present in post content resolve without a separate web server.
2. THE upload file handler SHALL be **read-only**, SHALL set an appropriate `Content-Type` from the file extension, and SHALL set a cache-friendly `Cache-Control` for immutable-by-convention upload assets.
3. THE handler SHALL reject any path that escapes the uploads root (path traversal, `..`, absolute paths, symlink escape) with `404`/`400` and SHALL NOT serve files outside the configured directory.
4. WHEN a requested upload file does not exist, THEN the handler SHALL return `404 Not Found`.
5. THE uploads route SHALL be registered so it does not shadow `/admin`, `/admin/api`, or other framework routes, and SHALL sit ahead of the public catch-all `/{slug}`.
6. THE design SHALL document a **proxy/redirect alternative** (for deployments whose uploads live in a remote object store/CDN) selectable by configuration, without changing the public URL contract.

### Requirement 8 — Media upload from the admin (multipart)

**User Story:** As an author, I want to upload an image or file from the admin, so
that I can add media to the site.

#### Acceptance Criteria
1. THE system SHALL expose `POST /admin/api/media` accepting a `multipart/form-data` file upload, requiring the `upload_files` capability AND a valid `X-CSRF-Token` (Req 12).
2. WHEN a file is uploaded, THEN the system SHALL write it into the configured uploads directory under a WordPress-style `YYYY/MM/` subpath, avoiding overwrite by de-duplicating the filename, and SHALL sanitize the stored filename (no path separators, control chars, or traversal).
3. WHEN the file is stored, THEN the system SHALL insert a `{prefix}posts` row with `post_type='attachment'`, `post_mime_type` set from the detected content type, `post_title` derived from the filename, and a `_wp_attached_file` postmeta pointing at the relative stored path — so the upload is a first-class WordPress attachment.
4. THE upload handler SHALL enforce a configurable **maximum size** and an **allowed MIME/extension allowlist**, rejecting disallowed or oversized uploads with `400`/`413` and not persisting a row or file.
5. WHEN an upload succeeds, THEN the endpoint SHALL return `201` with the created attachment's JSON (`id`, `url`, `filename`, `mimeType`), suitable for the admin to display immediately.
6. THE upload handler SHALL detect content type from the file bytes (not trust the client-supplied type) and SHALL store the file with non-executable permissions.
7. IF the database insert for the attachment row (or its `_wp_attached_file` postmeta) fails after the file has already been written to disk, THEN the system SHALL delete the just-written file before returning an error, so a failed upload never leaves an orphaned file with no corresponding attachment; conversely, IF the file write itself fails, THEN the system SHALL NOT attempt the database insert, so no attachment row is ever created without a backing file.

### Requirement 9 — Attaching media to a post

**User Story:** As an editor, I want to attach an uploaded file to a specific post,
so that it is associated with the content it belongs to.

#### Acceptance Criteria
1. THE system SHALL expose an unsafe endpoint to set an attachment's parent post — e.g. `POST /admin/api/media/{id}/attach` with a `parentId` — requiring `upload_files` (or `edit_posts` for the target) AND a valid `X-CSRF-Token`.
2. WHEN a valid parent is set, THEN the attachment's `post_parent` SHALL be updated in `{prefix}posts`, and the media listing (Req 6) SHALL reflect the new `parentId`.
3. IF the target attachment or parent post does not exist, THEN the endpoint SHALL return `404` and make no change.
4. THE attach operation SHALL be a pure metadata update (no file movement) and SHALL be reversible by attaching to `0` (unattached).
5. THE endpoint SHALL return identical behavior across all supported vendors.

### Requirement 10 — Reading WordPress navigation menus

**User Story:** As a theme, I want to read a site's navigation menus, so that I can
render the site's real menu structure.

#### Acceptance Criteria
1. THE system SHALL read navigation menus as the WordPress `nav_menu` taxonomy: each menu is a term in `{prefix}terms` + `{prefix}term_taxonomy` where `taxonomy='nav_menu'`, resolvable by slug or by a theme location assigned in the `theme_mods_{theme}` / `nav_menu_locations` option.
2. THE system SHALL read a menu's items as `{prefix}posts` rows with `post_type='nav_menu_item'` related to the menu term via `{prefix}term_relationships`, ordered by `menu_order`.
3. FOR each menu item, THE system SHALL resolve its target URL, label, type (`custom`, `post_type`, `taxonomy`), object id, and parent from the `{prefix}postmeta` keys (`_menu_item_url`, `_menu_item_type`, `_menu_item_object`, `_menu_item_object_id`, `_menu_item_menu_item_parent`), and SHALL assemble items into a **parent/child tree** using `_menu_item_menu_item_parent`.
4. THE menu read ports SHALL be additive, read-only `SELECT`s, introducing **no** schema change and working unchanged against an existing WordPress database's menus.
5. WHEN a requested menu (by slug or location) does not exist, THEN the reader SHALL return an empty menu (no items) rather than an error, so a theme referencing an unconfigured location degrades gracefully.
6. THE new nav-menu ports SHALL be defined in `internal/domain` and implemented per-vendor in `internal/storage/wprepo`, with no driver import above the storage layer.
7. WHEN resolving a **theme location**, THE system SHALL read the `option_value` for the `{prefix}options` row where `option_name = 'theme_mods_' + <active theme>` (the theme configured for the site), decode it with the existing PHP-serialize decoder (`internal/php`, first built for M2's `{prefix}capabilities`), and look up `nav_menu_locations[location]` for the assigned term id; a missing options row, a missing `nav_menu_locations` key for the location, or an undecodable value SHALL each be treated as "no menu assigned to this location" (Req 10.5's empty-menu degradation) rather than an error.
8. FOR a menu item of type `custom`, THE label and URL SHALL be read directly from the item's own `post_title` and `_menu_item_url`. FOR a menu item of type `post_type` or `taxonomy`, THE label SHALL fall back to the referenced post's `post_title` (or term's `name`) when the item's own `post_title` is empty, and THE URL SHALL be derived by resolving the referenced object's current permalink/term link from `_menu_item_object_id` (+ `_menu_item_object`) rather than trusted verbatim from `_menu_item_url`, which WordPress does not keep in sync when the target is renamed or moved — matching real WordPress `wp_setup_nav_menu_item()` behavior.

### Requirement 11 — Public nav-menu rendering and admin read-only view

**User Story:** As a visitor, I want to see the site's navigation, and as an editor
I want to inspect the configured menus, so that navigation is usable and auditable.

#### Acceptance Criteria
1. THE default theme SHALL render a navigation menu (by theme location or slug) as a semantic nested `<ul>`/`<li>` list in the site header, reflecting the parent/child tree from Req 10, with the current page marked (e.g. an `aria-current`/active class).
2. THE menu rendering SHALL be available to templates via the render engine (a template function or injected view data), consistent with the existing template-hierarchy approach, and SHALL escape all labels and URLs as untrusted-ish data.
3. WHEN a site has no menu assigned to the requested location, THEN the theme SHALL render nothing (or a minimal fallback) rather than erroring.
4. THE system SHALL expose `GET /admin/api/menus` (and `GET /admin/api/menus/{id}`) returning the menus and their item trees as JSON, requiring the `edit_posts` capability.
5. THE admin SHALL present a **read-only** menu view — a Spectrum tree/list of menus and their nested items — with **no** create/edit/delete controls this milestone.
6. THE design SHALL explicitly record that menu **editing** (reordering, adding/removing items, assigning locations) is **deferred** to a later menus milestone.

### Requirement 12 — CSRF and anti-abuse posture for the new write paths

**User Story:** As a security reviewer, I want grimoire's first write endpoints to
follow a coherent CSRF and abuse-resistance model, so that adding writes does not
open holes.

#### Acceptance Criteria
1. THE system SHALL validate the per-session synchronizer token on **authenticated** unsafe admin requests (comment moderation, media upload, media attach): the request SHALL carry an `X-CSRF-Token` header equal, in constant time, to the session's stored CSRF token, extending the existing `requireSessionCSRF` (M2 form-field) to also accept the header — M4 is the first milestone to actually validate it.
2. WHEN an authenticated unsafe admin request is missing or has a mismatched CSRF token, THEN the system SHALL respond `403 Forbidden` and make no change.
3. THE **anonymous** public comment form SHALL be protected against CSRF by a **double-submit token**: a per-render token is placed both in a hidden form field and a short-lived cookie (or an HMAC-signed token), and the submission is rejected (`403`) if they do not match — so the first anonymous write path is not a blind CSRF sink.
4. THE system SHALL NOT weaken any M2 cookie attribute (`HttpOnly`, `SameSite=Lax`, `Secure`-when-TLS) to accommodate comment submission or uploads.
5. THE admin SPA SHALL read `csrfToken` from `GET /admin/api/session` (already exposed in M3) and attach it as `X-CSRF-Token` on every unsafe admin request.
6. THE public comment endpoint SHALL apply the spam filter (Req 3) and reject oversized bodies, and SHALL rate-limit submissions per IP to blunt automated abuse.

### Requirement 13 — DB-vendor-agnostic, existing-WP-DB-overlay compatibility

**User Story:** As a developer, I want M4 to preserve grimoire's core constraints,
so that comments, media, and menus work on any supported vendor and against a live
WordPress DB.

#### Acceptance Criteria
1. THE M4 read paths (comment list, media listing, menu read) SHALL introduce **no** schema change against a live WordPress database; all reads SHALL be additive `SELECT`/`COUNT` ports.
2. THE only new **tables** created by migration SHALL be the greenfield-only, additive `IF NOT EXISTS` `{prefix}comments`/`{prefix}commentmeta` (Req 5.3); media and menus SHALL reuse the existing `{prefix}posts`/`{prefix}postmeta`/`{prefix}terms`/`{prefix}term_taxonomy`/`{prefix}term_relationships` tables with **no** new table.
3. THE new ports SHALL return identical shapes and behavior across MySQL, PostgreSQL, and SQLite, verified by the shared vendor-parameterized contract suite; any real-WP-DB validation SHALL be environment-gated like M2.1.
4. THE new read/write ports SHALL be defined in the domain layer and implemented per-vendor in the storage layer, with no database driver imported by the domain, content, render, or web layers.
5. WHEN grimoire overlays an existing WordPress database, THEN comment moderation, media upload rows, and menu reads SHALL interoperate with data authored by real WordPress without corruption.
6. BECAUSE the M1 greenfield `{prefix}posts` schema omits `comment_status`, `post_parent`, `post_mime_type`, and `menu_order` — columns Req 2.3's closed-post check, Req 6/8/9's media fields, and Req 10.2's menu-item ordering all depend on — THE same greenfield-only migration (Req 5.3) SHALL also `ALTER TABLE {prefix}posts ADD COLUMN` those four columns with WordPress-compatible defaults (`comment_status` `'open'`, `post_parent`/`menu_order` `0`, `post_mime_type` `''`), following the M2 `{prefix}users` column-migration contract: it runs only against a grimoire-provisioned schema and is never pointed at an overlaid live WordPress database, which already has every core `wp_posts` column.

### Requirement 14 — React Spectrum admin experience for the new tabs

**User Story:** As an Adobe employee, I want the comments, media, and menus admin to
look and behave like an Adobe product, so that it is consistent and accessible.

#### Acceptance Criteria
1. THE admin SHALL add three navigable views built with `@adobe/react-spectrum` components, `@spectrum-icons/workflow` icons, and Spectrum design tokens only: **Comments** (a `TableView` with moderation actions), **Media** (a grid/gallery with an upload control), and **Menus** (a read-only tree/list).
2. THE Comments view SHALL present a Spectrum `TableView` with status filtering and per-row/bulk moderation actions (approve, unapprove, spam, trash) that call the Req 4 endpoints with the CSRF header and reflect results optimistically or on refresh.
3. THE Media view SHALL present uploaded items in a Spectrum grid, provide a Spectrum-based upload affordance (e.g. `DropZone` + `FileTrigger`) that posts multipart to Req 8, and show upload progress plus success/error states.
4. THE Menus view SHALL present menus and their nested items as a Spectrum read-only tree/list, clearly indicating that editing is deferred.
5. EACH new data-driven view SHALL render explicit loading, empty, and error states, and SHALL handle `401` (→ `/login?redirect=…`) and `403` (→ insufficient-permissions state) exactly as the M3 views do.
6. THE new UI SHALL NOT introduce hardcoded hex colors, off-scale spacing, ad-hoc component libraries, the bundled Adobe Clean font, or scraped brand logos; it SHALL consume Spectrum tokens/components and remain keyboard-navigable and screen-reader-labeled.

### Requirement 15 — API error handling, observability, and no leakage

**User Story:** As an operator, I want the new comment/media/menu endpoints to fail
safely and legibly, so that clients get consistent errors and logs aid debugging
without leaking secrets.

#### Acceptance Criteria
1. THE new APIs SHALL return errors as the consistent JSON envelope `{ "error": { "code": "...", "message": "..." } }` with appropriate status codes (`400`, `401`, `403`, `404`, `405`, `413`, `500`).
2. THE APIs SHALL NEVER include SQL text, stack traces, driver errors, filesystem paths, password hashes, or session/CSRF secrets in a client response.
3. THE APIs SHALL log server-side failures via the existing structured logger (`slog`) with enough context to debug (e.g. post id, comment id, attachment id) without logging secrets, raw comment bodies at large size, or full uploaded file contents.
4. THE APIs SHALL set `Content-Type: application/json` on JSON responses and reject unsupported methods with `405 Method Not Allowed`; the public comment endpoint MAY additionally support a server-rendered form path returning HTML redirects.

## Implementation deviations

_None yet — this section will record any spec-consistent clarifications made
during implementation (mirroring `plans/03-spectrum-admin`), for example the exact
public comment endpoint shape (nested `/{post}/comments` vs flat `/comments`), the
chosen spam-heuristic thresholds, the selected Spectrum upload primitive, and
whether uploads are served directly or proxied in the reference deployment._
