# M7 — Revisions, Autosave, Scheduler & REST Term Parity: Requirements

## Introduction

M6 (`plans/06-admin-crud-editor`) shipped the Spectrum-based admin CRUD editor for
posts and pages: create/update/delete through both the admin API and the
`/wp-json/wp/v2/*` REST API, optimistic concurrency via a `post_modified`-keyed
`ConflictError`, the `future` post status with schedule-date validation, and
admin-API-only term (category/tag) writes. Its `requirements.md` and PR #16
explicitly deferred four areas to a later milestone. M7 delivers all four:

1. **Post/page revision history** — automatic revision snapshots on every
   save, an admin history/diff/restore UI and API, and a retention/pruning
   policy, reusing WordPress's own `post_type='revision'` storage convention
   so grimoire stays compatible with revisions already present in a
   pre-existing WordPress database it is pointed at.
2. **Autosave** — a periodic, low-friction draft snapshot distinct from a
   full manual save, stored the same way WordPress stores it (as a
   special-cased revision row), read-time-aware of M6's optimistic
   concurrency rather than fighting it at write time.
3. **Scheduled-publish execution** — the background component that actually
   flips a `future`-status post to `publish` once its scheduled date
   arrives. M6 added the status and its validation only; nothing in
   grimoire today transitions a post out of `future` on its own.
4. **REST API write parity for categories and tags** — `POST`/`PUT`/`PATCH`/
   `DELETE` on `/wp-json/wp/v2/categories` and `/wp-json/wp/v2/tags`, built
   on the `content.TermWriteService` M6 already introduced for the
   admin-API-only term editor.

Everything in M7 is additive wiring on top of M1-M6 infrastructure: the
existing `{prefix}posts` table and its repository, the existing
capability/authorization model (`internal/auth`), the existing CSRF
contract for session-authenticated writes, the existing Application
Password auth for REST, and the existing HTML sanitizer. M7 introduces
exactly one new schema element (a `post_parent` column) and exactly one new
runtime component (a background scheduler goroutine); it does not introduce
a job queue, a new auth model, or a new content-sanitization path.

This is also the first milestone under a standing project convention:
`design.md` must include explicit **Security Considerations** and **SEO
Considerations** sections (not folded into a generic "Security" aside), and
every diagram in `design.md` must be Mermaid. Both apply to every milestone
after this one as well.

## Requirements

### Requirement 1 — Automatic revision snapshots on save

**User Story:** As an author, when I save changes to a post or page, I want
the system to keep a copy of what it looked like beforehand, so that I can
recover an earlier version if a later edit turns out to be a mistake.

#### Acceptance Criteria

1. WHEN a post or page is successfully updated through
   `content.PostWriteService.Update` — whether the caller is the admin API
   or the `/wp-json/wp/v2/*` REST API — THE system SHALL snapshot the
   post's title, content, and excerpt as they were **immediately before**
   the update into a new revision row before applying the update.
2. THE system SHALL store a revision as an ordinary row in the same table
   as posts and pages (`{prefix}posts`), with `post_type = 'revision'`,
   `post_parent` set to the ID of the post it belongs to, and
   `post_status = 'inherit'` — matching WordPress's own revision-storage
   convention so that revisions already present in a pre-existing
   WordPress database grimoire is pointed at are read, listed, and
   restorable without any migration of existing data.
3. THE system SHALL record the ID of the user who made the change that
   triggered the snapshot as the revision row's author, independent of the
   parent post's own author.
4. A revision snapshot SHALL be created on every successful update
   regardless of whether the title, content, or excerpt actually changed,
   matching WordPress's default per-save revisioning behavior.
5. Creating a new post (as opposed to updating an existing one) SHALL NOT
   create a revision row; only updates to an already-existing post do.
6. Deleting a post or page SHALL delete all of its revision rows
   (including its autosave row, if any — Requirement 3) along with it, so
   no orphaned revision rows accumulate.
7. WHEN any existing admin-listing or REST-listing query runs (post/page
   collections, `/wp-json/wp/v2/posts`, `/wp-json/wp/v2/pages`, admin
   dashboards, feeds, sitemaps, or any future public content surface) THE
   system SHALL continue to exclude `post_type = 'revision'` rows by
   construction — i.e., by the existing default post-type allowlist
   (`["post", "page"]`) that already applies wherever posts are listed —
   with no new per-query filtering logic required. Revision rows SHALL NOT
   be reachable through any public or `/wp-json` read route, published or
   not, under any post status.

### Requirement 2 — Revision history, diff, and restore (admin API)

**User Story:** As an author or editor, I want to see the history of a
post's saved versions, compare an earlier version against the current one,
and restore an earlier version, so that I can recover from unwanted edits
without redoing my work from memory.

#### Acceptance Criteria

1. WHEN an authorized caller requests
   `GET /admin/api/posts/{id}/revisions` THE system SHALL return a
   newest-first list of the post's revisions (excluding its autosave row,
   Requirement 3), each summarized by revision ID, author, and modified
   timestamp, without the full title/content/excerpt body.
2. WHEN an authorized caller requests
   `GET /admin/api/posts/{id}/revisions/{revisionId}` THE system SHALL
   return that single revision's full title, content, and excerpt, for
   client-side diffing against the parent post's current values or against
   another revision.
3. WHEN an authorized caller issues
   `POST /admin/api/posts/{id}/revisions/{revisionId}/restore` THE system
   SHALL first snapshot the post's current (pre-restore) title, content,
   and excerpt as a new revision (matching WordPress's own "restoring
   creates a revision of what was there before" behavior — Requirement 1
   still applies to a restore, since a restore is itself an update), and
   THEN overwrite the post's title, content, and excerpt with the named
   revision's values.
4. Authorization for every route in this Requirement SHALL be identical to
   the authorization already required to edit the parent post itself
   (`edit_posts`/`edit_others_posts`/`edit_published_posts` as applicable,
   per `auth.CanEditPost`) — a revision inherits its parent's access
   control; there is no separate "revisions" capability.
5. IF the caller lacks authorization to edit the parent post, OR the
   parent post does not exist, OR the requested revision ID does not
   belong to the requested parent post, THEN THE system SHALL respond with
   `404 Not Found` in every case, with no distinguishing detail in the
   response body — mirroring M6 Requirement 1.6's existence-leak
   prevention: an unauthorized or absent caller cannot tell "forbidden"
   apart from "does not exist" apart from "revision ID belongs to a
   different post."
6. `POST /admin/api/posts/{id}/revisions/{revisionId}/restore` is a
   state-changing request and SHALL require the same session-cookie CSRF
   token as every other M4/M6 admin-API write route; Application-Password
   authenticated callers are exempt, matching the existing CSRF contract
   exactly.

### Requirement 3 — Autosave

**User Story:** As an author, I want my in-progress edits to be saved
automatically in the background while I write, so that a crashed browser
tab or an accidental navigation away doesn't lose my unsaved work.

#### Acceptance Criteria

1. WHEN an authorized caller issues `POST /admin/api/posts/{id}/autosave`
   with a title, content, and excerpt body THE system SHALL store it as a
   revision row (`post_type = 'revision'`, `post_parent = {id}`,
   `post_status = 'inherit'`) distinguishable from a normal revision
   (Requirement 1) by an autosave marker.
2. THE system SHALL keep **at most one** autosave row per (post, author)
   pair: a repeated autosave call from the same author for the same post
   SHALL update the existing autosave row in place rather than inserting a
   new one, bounding autosave storage growth regardless of how frequently
   the client autosaves.
3. An autosave write SHALL NOT modify the parent post's own row in any
   way — not its title/content/excerpt, not its `post_modified` timestamp.
   Consequently, an autosave write SHALL NOT be subject to, and SHALL NOT
   trigger, `content.ConflictError` (M6's optimistic-concurrency check),
   because it never writes the row that check guards.
4. WHEN an authorized caller requests `GET /admin/api/posts/{id}/autosave`
   THE system SHALL return that caller's autosave row for the post if one
   exists and its modified timestamp is newer than the parent post's own
   `post_modified`, so the editor can offer "restore this autosave?" —
   mirroring WordPress's own autosave-recovery prompt. If no such newer
   autosave row exists, THE system SHALL respond `404 Not Found`.
5. Authorization and existence-leak behavior for both routes in this
   Requirement SHALL match Requirement 2.4-2.5 exactly (same capability as
   editing the parent post; `404` for any unauthorized-or-absent case).
6. `POST /admin/api/posts/{id}/autosave` is a state-changing request and
   SHALL require the same session-cookie CSRF token as every other M4/M6
   admin-API write route, with the same Application-Password exemption.

### Requirement 4 — Scheduled-publish execution

**User Story:** As an author, when I schedule a post to publish at a future
date and time, I want it to actually become public at that time without my
having to come back and do anything else, so that "schedule" behaves the
way it's supposed to.

#### Acceptance Criteria

1. THE system SHALL run a background component that periodically checks
   for posts/pages whose `post_status = 'future'` and whose `post_date` is
   now in the past, and transitions each one to `post_status = 'publish'`.
2. THE check interval SHALL be configurable (a duration, e.g. via the
   existing `Config` struct/env-var convention) with a default of 60
   seconds.
3. THE background component SHALL start when the grimoire server process
   starts and SHALL stop cleanly as part of the same graceful-shutdown
   sequence already used for the HTTP server (`signal.NotifyContext` +
   context cancellation), with no separate lifecycle to operate.
4. THE publish transition SHALL be performed through the same
   `content.PostWriteService.Update` code path used by every other post
   update (admin API, REST API, this scheduler), so that publish
   authorization/business rules live in exactly one place and apply
   uniformly regardless of who or what triggers a publish.
5. THE scheduler's internal caller identity (its "system principal")
   SHALL hold exactly the capabilities required to publish a post
   (`publish_posts`, `edit_others_posts`) and SHALL NOT be constructible,
   reachable, or impersonable from any HTTP route, Application Password,
   or session — it exists only inside the scheduler component's own
   process memory.
6. Transitioning a post from `future` to `publish` SHALL NOT change its
   ID, slug, or GUID, so the post's canonical URL is identical before and
   after the transition — no link/URL churn is introduced by scheduled
   publishing.
7. IF the scheduler's periodic check encounters an individual post it
   cannot transition (e.g. a concurrent edit already moved it out of
   `future`), THEN THE system SHALL skip that post and continue
   processing the rest of the batch; one post's transition failure SHALL
   NOT abort the tick for every other due post.
8. Transitioning a post from `future` to `publish` SHALL create a revision
   snapshot of its pre-transition state, per Requirement 1.1 (the
   scheduler's write goes through `PostWriteService.Update` exactly like
   any other update, so this happens automatically with no special-case
   code).

### Requirement 5 — Revision retention and pruning policy

**User Story:** As a site operator, I want control over how many old
revisions grimoire keeps per post, so that revision history doesn't grow
without bound on a long-lived, frequently-edited site.

#### Acceptance Criteria

1. THE system SHALL support a configurable maximum number of revisions
   retained per post (mirroring WordPress's own `WP_POST_REVISIONS`
   constant), defaulting to unlimited when unset.
2. WHEN a new revision snapshot is created (Requirement 1.1) for a post
   whose configured maximum is a positive number, AND the post already has
   at least that many revisions (excluding its autosave row), THE system
   SHALL delete the oldest excess revision row(s) so the post's revision
   count never exceeds the configured maximum.
3. Setting the configured maximum to `0` SHALL disable revisioning
   entirely for future saves (no new revision rows created going forward)
   without deleting any revisions that already exist, matching WordPress's
   own semantics for `WP_POST_REVISIONS = 0`.
4. The autosave row (Requirement 3) SHALL NOT count against the configured
   revision maximum and SHALL NOT be pruned by this policy, since at most
   one autosave row per (post, author) ever exists (Requirement 3.2).

### Requirement 6 — REST API write parity: categories and tags

**User Story:** As an integrator using the `/wp-json/wp/v2/*` REST API, I
want to create, update, and delete categories and tags the same way I
already can with posts and pages, so that term management doesn't require
falling back to the admin-only API M6 shipped.

#### Acceptance Criteria

1. THE system SHALL expose `GET /wp-json/wp/v2/categories`,
   `GET /wp-json/wp/v2/categories/{id}`, `GET /wp-json/wp/v2/tags`, and
   `GET /wp-json/wp/v2/tags/{id}` — none of which exist prior to M7 — as
   the read counterparts to the writes below, each WP-shaped with `id`,
   `count`, `name`, `slug`, `taxonomy`, and `link` fields at minimum.
2. THE system SHALL expose `POST /wp-json/wp/v2/categories` and
   `POST /wp-json/wp/v2/tags` to create a term of the corresponding
   taxonomy (`category`/`post_tag`), reusing `content.TermWriteService`'s
   existing validation (name required, slug auto-derived or supplied,
   uniqueness within its taxonomy) unchanged from its M6 admin-API
   behavior.
3. THE system SHALL expose `PUT`/`PATCH /wp-json/wp/v2/categories/{id}`
   and `PUT`/`PATCH /wp-json/wp/v2/tags/{id}` to update a term's name
   and/or slug, and `DELETE` on the same single-item routes to delete it.
4. Every write route in this Requirement SHALL require the
   `manage_categories` capability (`auth.CanManageTerms`), matching the
   authorization already enforced by the M6 admin-API term routes exactly.
5. IF the caller lacks `manage_categories`, THEN THE system SHALL respond
   `403 Forbidden` (terms are not per-object-owned the way posts are, so
   there is no existence-leak concern analogous to Requirement 2.5 — a
   term either exists in a globally-visible taxonomy or it doesn't).
6. IF a write targets a term ID that does not exist, or a taxonomy/type
   mismatch (e.g. `PUT /wp-json/wp/v2/tags/{id}` where `{id}` is actually a
   category), THEN THE system SHALL respond `404 Not Found`.
7. Deleting a term SHALL detach it from every post currently assigned to
   it (matching the existing M6 admin-API delete behavior in
   `content.TermWriteService`) without deleting the posts themselves.

### Requirement 7 — CSRF and authentication contract for new routes

**User Story:** As a security-conscious operator, I want every new
state-changing route this milestone introduces to follow the exact same
authentication and CSRF rules as every existing write route, so that M7
doesn't quietly introduce an inconsistent or weaker trust boundary.

#### Acceptance Criteria

1. Every new admin-API write route (autosave, revision restore) SHALL
   require a valid authenticated session AND, for session-cookie-based
   callers, a valid CSRF token via the same mechanism already used for
   M4/M6 admin-API writes.
2. Every new REST write route (categories, tags) SHALL accept Application
   Password authentication exactly as M6's post/page REST writes already
   do, with no CSRF requirement for Application-Password-authenticated
   requests (matching the existing REST write contract, since Application
   Passwords are not subject to browser same-origin/cookie-based CSRF).
3. THE scheduler's internal publish transition (Requirement 4) is not
   triggered by any inbound HTTP request and therefore has no CSRF surface
   at all; its only "credential" is the unexported system principal
   described in Requirement 4.5, which cannot be presented by an external
   caller.
4. No new route introduced by this milestone SHALL accept an
   unauthenticated request for any state-changing operation.

### Requirement 8 — Spectrum admin UI: revision history, restore, and autosave

**User Story:** As an author using the admin editor, I want to see and act
on revision history and autosave recovery directly in the editor UI, so
that Requirements 1-3 are actually usable rather than API-only.

#### Acceptance Criteria

1. THE post/page editor SHALL show a "Revisions" panel listing the post's
   revision history (Requirement 2.1), each entry showing its author and
   modified timestamp.
2. Selecting a revision in the panel SHALL show a diff between that
   revision's content and the post's current content (client-side diff
   over the two content bodies returned by Requirement 2.2's endpoint; no
   new server-side diff endpoint is required).
3. THE panel SHALL offer a "Restore this revision" action that calls
   Requirement 2.3's restore endpoint and then reloads the editor with the
   post's new (restored) content.
4. THE editor SHALL periodically call the autosave endpoint
   (Requirement 3.1) while a post has unsaved changes, on an interval
   independent of the revision-creation-on-save behavior in Requirement 1
   (autosave never triggers a normal save or a normal revision snapshot).
5. WHEN the editor loads a post and Requirement 3.4's endpoint indicates a
   newer autosave exists, THE editor SHALL show a dismissible notice
   offering to load the autosave's content into the editor, without
   applying it automatically.
6. THE editor SHALL NOT block or refuse manual saving while an autosave
   notice is showing; the notice is informational only, matching the
   read-time (not write-time) conflict-awareness design established in
   Requirement 3.3.

## Out of scope (deferred)

- **Media and user REST write endpoints** (`POST`/`PUT`/`DELETE` on
  `/wp-json/wp/v2/media` and `/wp-json/wp/v2/users`) remain 501/unbuilt.
  Their read (`GET`) routes already exist from earlier milestones. Media
  writes involve materially different multipart/file-upload handling; user
  writes touch password/credential flows that deserve their own
  security-focused milestone. Both are deferred to M8+.
- **A `/wp-json/wp/v2/posts/{id}/revisions` REST sub-resource** (WordPress
  exposes revisions over its REST API in addition to its admin UI).
  Requirement 2's admin API is sufficient for M7's editor UI; a public REST
  surface for revisions is deferred to a later milestone rather than
  expanding this milestone's already-broad scope further.
- **Hierarchical category parents.** `domain.Term` has no `parent` field
  today; Requirement 6's category REST responses render a placeholder
  `parent: 0` for WordPress-shape compatibility without implementing
  actual parent/child category relationships. Adding real hierarchy is
  deferred.
- **Sitemap generation and `robots.txt`.** No such code exists anywhere in
  grimoire today (confirmed by repository-wide search), on any milestone.
  M7 does not introduce it. This is called out explicitly in `design.md`'s
  SEO Considerations section rather than silently assumed away.
- **Per-user autosave conflict merging.** Requirement 3 keeps one autosave
  row per (post, author); it does not attempt to merge concurrent autosave
  content from two different authors editing the same post at once. Real
  multi-author simultaneous editing conflict resolution (beyond M6's
  existing single-writer `ConflictError`) remains out of scope.
- **Configurable scheduler interval exposed in the admin UI.** Requirement
  4.2's interval is a server-side config value (env var/config file); a
  Spectrum settings screen to change it at runtime is not part of M7.
