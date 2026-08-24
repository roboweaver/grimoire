# Requirements — M6: Admin CRUD Editor

## Introduction

M1–M5 gave grimoire a full **read** story: public rendering (M1), users/auth/
roles/CSRF (M2), a read-only React Spectrum admin (M3), comments/media/menus
with the first CSRF-validated admin writes (M4), and a read-mostly REST API
that explicitly deferred every post/page write to this milestone (M5, `501
Not Implemented` on every `wp/v2` posts/pages write verb, both collection and
single-item routes). M6 closes grimoire's last major gap: **writing content**.
It gives the Spectrum admin a real post/page editor — create, update, delete,
inline category/tag management, and a WordPress-shaped status lifecycle — and
wires the same underlying write services into the M5 REST surface so the
`501`s become real `200`/`201`/`204` responses.

**What already exists (M2–M5) and is reused, not rebuilt.** `internal/content`
already has a capability-checked `PostWriteService` (`Create`/`Update`/
`Delete`) and `TermWriteService` (`Create`/`Delete`) built in M2, wired to
`auth.CanCreatePost`/`CanEditPost`/`CanDeletePost`/`CanManageTerms` — the same
WordPress meta-capability rules (`edit_posts` vs `edit_others_posts` vs
`edit_published_posts` vs `edit_private_posts`, and the `publish_*`/
`delete_*` families) that already gate M2's internal write API. M6 does
**not** invent new authorization rules; it is the first milestone to put a UI
and a REST surface in front of the write services that already enforce them.
`domain.Post` already carries `Modified`/`ModifiedGMT` (added by M5's `0004`
migration) and `CommentStatus` — present in the struct today but not yet
threaded through `PostRepo.Create`/`Update`, a gap this milestone closes
alongside adding `post_modified`/`post_modified_gmt` maintenance on every
write (today's `Update` does not touch them at all).

**What M6 adds that is genuinely new** (not just wiring):

1. **A rich-text editor** in the Spectrum SPA. grimoire's admin is a React
   Spectrum SPA, not a PHP/Gutenberg block editor — Gutenberg is
   PHP-and-React-block-specific tooling tied to the classic `wp-admin` shell
   and is explicitly out of scope for a no-PHP project. `post_content` is
   stored and rendered as **trusted raw HTML** (established in M1/M2/M4), so
   the editor must be an HTML in/HTML out surface, not a proprietary
   JSON-document editor requiring a lossy custom export step. This milestone
   selects and embeds **TipTap** (a ProseMirror-based, MIT-licensed,
   framework-agnostic rich-text editor with first-class React bindings whose
   native content model *is* HTML — `editor.getHTML()` / `setContent(html)`)
   with a toolbar built from React Spectrum `ActionButton`/`ToggleButton`/
   `Picker` components so the editor surface reads as a native part of the
   Spectrum admin even though no Spectrum-native rich-text component exists.
2. **Term-relationship writes.** No write port for
   `{prefix}term_relationships` exists anywhere before M6 (M5 added a
   **read-only** `PostTermsRepository.TermsForPost`); assigning
   categories/tags to a post has never been possible. M6 adds a
   `PostTermsWriter` port and wires it into the post editor.
3. **`TermWriter.Update`.** Today's `TermWriter` only has `Create`/`Delete` —
   renaming an existing category/tag has never been possible. M6 adds
   `Update` so the editor's inline term-management UI can rename as well as
   create.
4. **Concurrency detection.** WordPress's own REST API has **no** native
   optimistic-concurrency mechanism (confirmed: last-write-wins is documented
   WordPress behavior; `post_modified` is read-only in REST responses and
   never compared server-side). grimoire's Spectrum editor has no
   autosave/heartbeat/"someone else is editing this" indicator to compensate,
   so M6 adds a deliberately lightweight guard on top of the existing
   `Modified` column: the client submits the `modified` timestamp it loaded
   the post with, and an update whose stored `Modified` no longer matches is
   rejected with `409 Conflict` rather than silently overwritten.
5. **Status lifecycle completion.** `draft`/`pending`/`publish`/`private`/
   `future` are WordPress's real, stored status vocabulary and already
   authorized correctly by `auth.CanCreatePost`/`CanEditPost` (which already
   special-case `publish`/`private`). M6 is the first milestone to expose all
   five from the editor. `future` (scheduled publish) is stored exactly as
   WordPress stores it (`post_status = 'future'` with a future `post_date`)
   but grimoire has no WP-Cron-equivalent scheduler anywhere in the project
   to flip it to `publish` automatically at that time — this is an explicit,
   documented limitation, not a silent gap (Req 5).

**Explicitly out of scope, deferred to M7+:** post revisions (a real,
separate feature — WordPress stores them as ordinary `{prefix}posts` rows
with `post_type='revision'`, so no new table is needed, but a revision
browser/diff UI and `/wp/v2/posts/{id}/revisions` REST sub-resource is
meaningfully more surface than one milestone should absorb alongside the
base editor); autosave; an actual scheduled-publish poller/cron that flips
`future` posts to `publish` at their scheduled time; REST endpoints for
categories/tags (`/wp-json/wp/v2/categories`, `/tags` — M5 left these fully
unbuilt, not merely `501`'d, and M6 keeps term writes admin-API-only rather
than growing the REST taxonomy surface in the same milestone); REST writes
for media or users (both still `501` from M5, for a milestone after this
one); custom fields/`meta` REST registration; and any change to the M4
`X-CSRF-Token` contract, which M6 reuses byte-for-byte.

## Requirements

### Requirement 1 — Post/page create, update, and delete via the admin API

**User Story:** As an author using the Spectrum admin, I want to create,
edit, and delete posts and pages, so that I can manage content without a
database console.

#### Acceptance Criteria

1. THE system SHALL expose `POST /admin/api/posts` accepting a JSON body
   (`title`, `content`, `excerpt`, `slug`, `status`, `type` — `"post"` or
   `"page"` — `date`, `commentStatus`, and an optional `termIds` map keyed by
   taxonomy) and, on success, `201 Created` with the created post's full
   detail representation (Req 4) including its generated `id`.
2. THE system SHALL expose `PUT /admin/api/posts/{id}` accepting the same
   body shape plus a required `modified` field (the `Modified` timestamp the
   client last loaded, ISO-8601) and, on success, `200 OK` with the updated
   detail representation reflecting the new `modified` timestamp.
3. THE system SHALL expose `DELETE /admin/api/posts/{id}` and, on success,
   `204 No Content`.
4. ALL three endpoints SHALL require the `edit_posts` capability (session
   login gate, matching the existing `/admin/api/posts` read routes) AND a
   valid `X-CSRF-Token` header (Req 8) — missing/invalid token SHALL respond
   `403 Forbidden` and make no change.
5. WHEN the acting user lacks the specific WordPress meta-capability the
   requested operation needs (e.g. `edit_others_posts` to edit another
   user's post, `publish_posts` to set `status: "publish"`, `delete_
   published_posts` to delete a live post) THEN the system SHALL respond
   `403 Forbidden` with a generic message that does not reveal which
   capability was missing, reusing `content.ErrForbidden` from the existing
   `PostWriteService`.
6. WHEN `PUT`/`DELETE` targets a post/page ID that does not exist THEN the
   system SHALL respond `403 Forbidden` (not `404`) — reusing
   `PostWriteService`'s existing `ByID`-then-authorize pattern, which already
   maps a missing record to the generic `content.ErrForbidden` *before* any
   capability check runs, so an unauthorized caller cannot distinguish
   "doesn't exist" from "exists but you can't touch it." The admin handler
   SHALL NOT introduce a new, more specific "not found" response for this
   case; this is a deliberate existing-behavior preservation, not an
   oversight (contrast with Req 6.1's REST 404s, which follow WordPress's
   own REST convention of confirming existence before authorizing).
7. WHEN the request body fails basic validation (empty `title` for a
   non-draft status, `type` not one of `"post"`/`"page"`, `status` not one of
   `draft`/`pending`/`publish`/`private`/`future`) THEN the system SHALL
   respond `400 Bad Request` without calling the write service.
8. THE `POST`/`PUT` handlers SHALL default `status` to `"draft"` and `type`
   to `"post"` when omitted, matching `PostWriteService.Create`'s existing
   defaulting behavior.

### Requirement 2 — Category/tag assignment and inline term management

**User Story:** As an author, I want to assign existing categories/tags to a
post and create a new one inline while editing, so that I don't have to
leave the editor to manage taxonomy.

#### Acceptance Criteria

1. THE system SHALL expose a new `PostTermsWriter` domain port,
   `SetPostTerms(ctx, postID int64, taxonomy string, termIDs []int64) error`,
   that replaces a post's term relationships for the given taxonomy with
   exactly the supplied term IDs (an empty slice clears all terms of that
   taxonomy from the post) and maintains each affected `term_taxonomy.count`.
   THE system SHALL also expose a new **read** port needed to resolve term
   IDs to display objects — `ListByTaxonomy(ctx, taxonomy string)
   ([]Term, error)` (for Req 2.4's term picker) and `TermsByIDs(ctx, ids
   []int64) ([]Term, error)` (for resolving a post's assigned term IDs, from
   the existing `PostTermsRepository.TermsForPost`, into the `{id,name,
   slug}` objects Req 4.1's `terms` field requires) — neither of which any
   existing port provides (`TermRepository.BySlug` resolves one term by
   slug, not a taxonomy listing or a bulk ID lookup).
2. THE `POST`/`PUT /admin/api/posts` handlers (Req 1) SHALL, when the request
   body includes a `termIds` map, call `SetPostTerms` once per taxonomy key
   present in the map, after the post write itself succeeds; a failure here
   SHALL NOT roll back the already-committed post write and SHALL be reported
   as a distinct `partial` field in the response body so the client can
   retry just the term assignment.
3. THE system SHALL expose `POST /admin/api/terms` (create), `PUT
   /admin/api/terms/{id}` (rename — new; `TermWriter` previously had no
   `Update`), and `DELETE /admin/api/terms/{id}`, all requiring
   `manage_categories` (`auth.CanManageTerms`) AND `X-CSRF-Token`.
4. THE system SHALL expose `GET /admin/api/terms?taxonomy=category|post_tag`
   returning every term of that taxonomy (id, name, slug) for the editor's
   term picker, requiring only `edit_posts` (read access, no CSRF).
5. WHEN a `PUT /admin/api/terms/{id}` targets a nonexistent term THEN the
   system SHALL respond `404 Not Found`.
6. WHEN `SetPostTerms` is called with a taxonomy the post's type does not
   use (e.g. `post_tag` on a `page`) THEN the system SHALL NOT reject the
   call — grimoire, like WordPress, does not enforce taxonomy-to-post-type
   registration at the storage layer — but the Spectrum editor (Req 6)
   SHALL only ever present the category/tag picker for posts, never pages.

### Requirement 3 — Optimistic concurrency on update

**User Story:** As an author, I don't want my edit to silently overwrite a
colleague's edit made moments earlier, so that no one's work is lost without
at least a warning.

#### Acceptance Criteria

1. `PUT /admin/api/posts/{id}` (Req 1.2) SHALL require a `modified` field in
   the request body (ISO-8601 timestamp, matching what the corresponding
   `GET /admin/api/posts/{id}` response returned when the client loaded the
   post).
2. WHEN the stored post's current `Modified` value does not exactly equal
   the submitted `modified` value THEN the system SHALL respond `409
   Conflict` with a JSON body `{"error": "conflict", "currentModified":
   "<ISO-8601>"}` carrying the current stored `Modified` value (sourced from
   a concrete `content.ConflictError{CurrentModified time.Time}` type
   returned by `PostWriteService.Update`, not a plain sentinel error — see
   design.md) and SHALL make no change to the post.
3. WHEN the update is authorized and the `modified` values match THEN the
   system SHALL apply the update and SHALL set the stored post's `Modified`/
   `ModifiedGMT` to the current time as part of the same write (`PostRepo.
   Update` does not do this today and SHALL be extended to).
4. `PostRepo.Create` SHALL likewise set `Modified`/`ModifiedGMT` (and
   `DateGMT` when not separately supplied) to the current time, so every
   post has a meaningful `Modified` value from creation, not the `0004`
   migration's `'1970-01-01 00:00:00'` default.
5. THIS conflict check is an **addition on top of** WordPress's own
   documented behavior (no native REST optimistic concurrency exists in real
   WordPress — last-write-wins is standard); it applies only to grimoire's
   own `/admin/api/posts` write path in this milestone, not retroactively to
   any REST write (Req 6 defines REST behavior separately, and by design
   requires the same `If-Unmodified-Since`-equivalent contract for
   consistency — see Req 6.4).

### Requirement 4 — Admin API post detail shape

**User Story:** As the Spectrum admin frontend, I need a single detail
response shape covering both read and write responses, so that the editor
can load a post once and re-render it identically after every save.

#### Acceptance Criteria

1. `GET`, `POST`, and `PUT /admin/api/posts[/{id}]` SHALL all return the
   same JSON object shape: `id`, `title`, `slug`, `type`, `status`, `author`,
   `date`, `modified`, `excerpt`, `content`, `commentStatus`, and `terms`
   (a map of taxonomy → array of `{id, name, slug}`), extending the read-only
   detail shape M3 already defined (`id,title,slug,type,status,author,date,
   excerpt,content`) with `modified`, `commentStatus`, and `terms`.
2. Dates SHALL be formatted `2006-01-02T15:04:05Z07:00`, matching the
   existing M3/M4 admin API date formatting convention exactly.

### Requirement 5 — Status lifecycle, including scheduled ("future") posts

**User Story:** As an author, I want to save a draft, submit for review,
publish immediately, keep something private, or schedule it for later, so
that my workflow matches how WordPress already works.

#### Acceptance Criteria

1. THE `status` field on create/update SHALL accept exactly
   `draft`/`pending`/`publish`/`private`/`future`, matching WordPress's
   stored vocabulary; any other value SHALL `400`.
2. WHEN `status: "future"` is submitted with a `date` in the past or equal
   to the current time THEN the system SHALL respond `400 Bad Request` (a
   "scheduled" post must be scheduled for the future) — **except** when the
   submitted `date` is unchanged from the post's current stored `date` (an
   edit that does not touch the schedule, e.g. fixing a typo in the body of
   an already-past-due `future` post that the absent scheduler never
   flipped to `publish`, per 5.3 below). Such unchanged-date resubmits SHALL
   be allowed through unmodified, so correcting an unrelated field never
   forces the author to also resolve the stale schedule as a side effect.
3. THE system SHALL store a `future`-status post exactly as WordPress does
   (`post_status = 'future'`, `post_date` set to the requested future time)
   with **no** automatic transition to `publish` when that time arrives —
   grimoire has no cron/scheduler component in any milestone through M6, and
   this SHALL be documented in `design.md` as an explicit, known limitation
   rather than an implied guarantee.
4. Publishing (`status: "publish"`, immediately) and every other status
   transition SHALL continue to be authorized exactly as `auth.
   CanCreatePost`/`CanEditPost` already require (`publish_posts` for publish,
   `edit_published_posts` to edit an already-published item, `edit_private_
   posts` to edit another user's private item) — M6 adds no new capability
   rules.

### Requirement 6 — REST API parity: posts and pages become writable

**User Story:** As a developer using WordPress REST tooling against grimoire,
I want `POST`/`PUT`/`PATCH`/`DELETE` on `/wp-json/wp/v2/posts` and `/pages`
to actually work, so that my existing WP REST client doesn't need
special-casing for grimoire.

#### Acceptance Criteria

1. `POST /wp-json/wp/v2/posts` and `/wp-json/wp/v2/pages` SHALL create a
   post/page from a WordPress-shaped request body (`title`, `content`,
   `excerpt`, `slug`, `status`, `date`, `comment_status`) and respond `201
   Created` with the WordPress-shaped single-item representation M5 already
   defined for `GET`, replacing the `501` stub M5 registered for this
   route/verb. Consistent with the "REST is admin-API-only for terms" scope
   (see "Out of scope"), the REST create/update bodies SHALL NOT accept
   `categories`/`tags` term-ID arrays in this milestone — a REST-created
   post/page has no term assignments; assigning categories/tags to it
   requires the admin API (Req 2).
2. `PUT`/`PATCH /wp-json/wp/v2/posts/{id}` and `/pages/{id}` SHALL update the
   item and respond `200 OK` with the updated representation, replacing the
   `501` stub.
3. `DELETE /wp-json/wp/v2/posts/{id}` and `/pages/{id}` SHALL delete the
   item and respond `200 OK` with the deleted item's last representation and
   `"deleted": true` (matching real WordPress's REST delete response shape),
   replacing the `501` stub.
4. THE REST write handlers SHALL require the caller to be authenticated via
   an Application Password (M5) with the appropriate capability — the same
   `PostWriteService`/`auth.Can*Post` checks the admin API uses — and SHALL
   support the same `modified`-timestamp conflict check as Req 3, expressed
   as an `If-Unmodified-Since` request header (an HTTP-native equivalent of
   the admin API's body-carried `modified` field); a mismatch SHALL respond
   `409 Conflict` with a WordPress-shaped REST error body
   (`{code, message, data:{status}}`).
5. `If-Unmodified-Since` SHALL be optional on REST writes (unlike the
   admin API's Req 3.1, which requires it) — a REST client that omits it
   SHALL get plain last-write-wins, matching real WordPress's actual,
   documented REST behavior (no native concurrency check) exactly; grimoire
   only requires the stricter check from its own first-party Spectrum admin.
6. EVERY OTHER `wp/v2` write verb not covered by 1–3 (media, users,
   categories, tags, comments beyond the existing M5 create) SHALL continue
   to respond `501 Not Implemented` with the existing M5 "deferred" error
   body, unchanged by this milestone.
7. THE REST write handlers SHALL enforce the same TLS/loopback-only
   Application-Password posture M5 already established (Req covering M5's
   Req 8.9) — no relaxation for writes.
8. WHEN `PUT`/`PATCH`/`DELETE` targets a post/page ID that does not exist
   THEN the system SHALL respond `404 Not Found` using M5's existing
   `rest_post_invalid_id` error code — unchanged REST convention (M5's `GET`
   already 404s a missing ID the same way). This is a deliberate, pre-
   existing REST/admin-API asymmetry, not new to M6: the REST surface
   confirms existence before authorizing (matching real WordPress), while
   the admin API does not (Req 1.6) to avoid existence leakage on a surface
   with per-post ownership-dependent authorization.

### Requirement 7 — Rich-text post editor in the Spectrum admin

**User Story:** As an author, I want a WYSIWYG editor for post/page content
instead of a raw HTML textarea, so that authoring feels like a real CMS.

#### Acceptance Criteria

1. THE Spectrum admin SHALL embed a TipTap-based rich-text editor
   (`@tiptap/react` + `@tiptap/starter-kit`) on the post/page editor view,
   bound to the post's `content` field, supporting at minimum: bold, italic,
   headings (H2–H4), bullet/numbered lists, blockquote, link, and image
   (via an `<img>` tag referencing an already-uploaded M4 media URL). Image
   insertion SHALL use a **new** `MediaPicker` dialog component backed by
   the existing M4 media-list admin API (`GET /admin/api/media`) — no such
   reusable picker exists yet (M4's `Media.tsx` is a standalone full-page
   list view with no selection callback), so this dialog is new work
   introduced by this requirement, not a reuse of existing UI. No new
   *upload* UI is introduced: the picker only selects among already-uploaded
   media.
2. THE editor's toolbar SHALL be built from React Spectrum `ActionButton`/
   `ToggleButton`/`Picker` components reflecting the TipTap editor's active
   mark/node state (e.g. the bold button SHALL show a pressed/selected state
   when the cursor is inside bold text).
3. ON save, THE editor view SHALL call `editor.getHTML()` and send that
   string as the `content` field of the create/update request (Req 1) — no
   client-side sanitization is applied (content remains a trusted-author
   value, consistent with M1/M2/M4's existing trust boundary for
   `post_content`).
4. ON load (edit an existing post), THE editor SHALL call `editor.
   commands.setContent(html)` with the post's stored `content` HTML.
5. THE Pages editor view SHALL reuse the identical rich-text editor
   component as the Posts editor view (no separate implementation for
   pages).

### Requirement 8 — CSRF contract reuse (no changes)

**User Story:** As the system, I want every new unsafe admin-API endpoint to
be protected exactly the way M4 already protects comment moderation and
media upload, so that adding a write surface doesn't add a CSRF hole.

#### Acceptance Criteria

1. EVERY new unsafe `/admin/api` endpoint introduced by this milestone
   (`POST`/`PUT`/`DELETE /admin/api/posts[/{id}]`, `POST`/`PUT`/`DELETE
   /admin/api/terms[/{id}]`) SHALL require the existing `requireSessionCSRF`
   check: an `X-CSRF-Token` header equal, in constant time, to
   `domain.Session.CSRFToken`.
2. WHEN the header is missing or does not match THEN the system SHALL
   respond `403 Forbidden` and make no change — identical to the existing
   M4 comment-moderation/media-upload behavior.
3. THE Spectrum SPA SHALL continue to read `csrfToken` from `GET
   /admin/api/session` (unchanged since M3) and attach it as `X-CSRF-Token`
   on every unsafe request added by this milestone.
4. THIS milestone SHALL NOT modify `requireSessionCSRF`, the M2 session
   cookie contract, or the M4 anonymous double-submit comment-form token —
   all are reused byte-for-byte.

### Requirement 9 — Spectrum admin editor views

**User Story:** As an author, I want dedicated create/edit views for posts
and pages reachable from the existing Posts/Pages list, so that the whole
authoring workflow lives inside the Spectrum admin.

#### Acceptance Criteria

1. THE existing `PostsList.tsx` view SHALL gain a "New post" action
   navigating to a new post editor route, and each row SHALL gain an "Edit"
   action navigating to the editor pre-loaded with that post's current
   detail (Req 4) and a "Delete" action (with a Spectrum `AlertDialog`
   confirmation) calling `DELETE /admin/api/posts/{id}`.
2. THE editor view SHALL present: a title field (Spectrum `TextField`), the
   rich-text body editor (Req 7), an excerpt field, a status `Picker`
   restricted to the five values in Req 5.1, a slug field, a comment-status
   toggle, a category/tag picker (posts only, Req 2), and Save/Delete
   actions.
3. WHEN a save request returns `409 Conflict` (Req 3.2) THEN the editor
   view SHALL present a Spectrum `Dialog` telling the user the post changed
   since they loaded it, offering "reload latest" (discarding local edits)
   or "keep editing" (the user must manually reconcile before saving again);
   it SHALL NOT silently retry the save.
4. WHEN a save request returns `403 Forbidden` (missing capability or CSRF)
   THEN the editor SHALL present the existing M3/M4 generic error banner
   pattern.
5. THE Pages list/editor SHALL reuse the same views as Posts, parameterized
   by `type`, rather than duplicating the UI.

## Out of scope (deferred)

- **Revisions** (M7): no revision rows, no revision browser, no
  `/wp/v2/posts/{id}/revisions` REST sub-resource.
- **Autosave** (M7+): no periodic background save while editing.
- **Scheduled-publish execution** (M7+): `future` status is stored correctly
  (Req 5.3) but nothing in grimoire transitions it to `publish` when the
  scheduled time arrives.
- **REST `categories`/`tags` endpoints** (M7+): M5 left `/wp-json/wp/v2/
  categories` and `/tags` completely unbuilt (not even `501`'d); M6 does not
  add them. Term writes in this milestone are admin-API-only.
- **REST writes for media and users** (still `501`, unchanged from M5).
- **Custom fields / `meta` REST registration.**
- **Any change to the M4 CSRF contract, M2 session contract, or M5
  Application Password auth contract.**
