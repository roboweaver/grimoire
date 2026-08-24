# Requirements — M5: Extensions & REST API Parity

## Introduction

This milestone gives grimoire two things real WordPress sites take for granted:
a **REST API** (`/wp-json/wp/v2/*`) that existing WordPress REST tooling can
point at unmodified, and a **native Go extension mechanism** that gives
grimoire *some* meaningful extensibility story without pretending to be a PHP
runtime. It builds on M1's content core, M2's auth/roles/capabilities/CSRF
substrate, M3's read-only admin, and M4's comments/media/menus (M4's
requirements explicitly deferred "the full WordPress REST API surface" to this
milestone).

**REST API scope.** M5 ships read (list + single) parity for the four
`wp/v2` collections a WordPress REST client most commonly needs — `posts`,
`pages`, `comments`, `media`, and `users` — with response shapes that match
real WordPress closely enough for existing WP REST clients/tooling to work
against grimoire: WordPress field names and JSON structure (`title.rendered`,
`content.rendered`, etc.), HAL-style `_links`, `?_embed` support producing
`_embedded`, and the `X-WP-Total`/`X-WP-TotalPages` pagination headers. The
**one** REST write endpoint in M5 is `POST /wp-json/wp/v2/comments`, wired to
the *existing* M4 `CommentService.Create` and spam filter — no new comment
business logic, just a REST-shaped adapter over it. **Every other REST write**
— creating, updating, or deleting posts, pages, or media via the REST API —
is explicitly **out of scope** and deferred to M6 (`admin-crud-editor`), which
owns grimoire's write path for content.

**REST authentication.** Real WordPress REST clients authenticate with
**Application Passwords** (introduced in WordPress 5.6): a per-user, revocable
random token presented as the password half of HTTP Basic auth. M5
implements this natively in Go — no PHP, no `wp-login.php` cookie-and-nonce
dance required for API clients. Browser-context requests (the admin SPA)
continue to use the M2 session cookie; the one REST write endpoint still
enforces the M4 `X-CSRF-Token` double-submit contract when authenticated by
session cookie, mirroring WordPress's own model where Application Password
requests are exempt from cookie-nonce/CSRF checks (there is no browser
session to forge). Because an Application Password is a long-lived bearer
credential sent on **every** request over HTTP Basic auth (not a short-lived
signed cookie), M5 requires TLS for it by default, mirroring real
WordPress's own behavior of refusing Application Passwords over a
non-`https` request unless the host resolves to `localhost` (Req 8.9).

**Extension system.** WordPress's plugin architecture is PHP hooks (`add_action`/
`add_filter`) woven through every code path, backed by a runtime that can load
arbitrary `.php` files dropped into `wp-content/plugins/`. That does not
translate to a single static Go binary, and grimoire does not attempt it. M5
instead adds `pkg/extensions`: a compile-time Go hook registry with
**actions** (fire-and-forget notifications) and **filters** (value-transforming
pipelines), modeled on WordPress's actions/filters vocabulary but Go-native —
typed callbacks registered via `init()`-time calls, compiled directly into the
binary. M5 wires three concrete extension points into existing code paths:
a post-render filter, REST request/response filters, and a comment-submit
action. This spec is deliberately explicit about what this **is not**: it is
**not** compatible with real wordpress.org plugins (no PHP, ever; no dynamic
loading; no plugin marketplace; no plugin activation/deactivation UI). What it
**is**: a native, statically-linked Go extension mechanism at a small number of
well-defined points, published at the importable `pkg/extensions` path so
that first-party, vendored, or external Go modules can all register hooks the
same way (a blank-import package compiled into the grimoire binary at build
time — still no dynamic loading at runtime).

WordPress compatibility remains a **schema/behavior contract**. Almost all
REST reads are additive `SELECT`s over data M1–M4 already model; comment
creation via REST reuses M4's write path verbatim. The one exception is
`date_gmt`/`modified`/`modified_gmt`/`ping_status`/`guid.rendered`/
`content.protected` on posts/pages (Req 2.2): a greenfield grimoire database
never populated `post_date_gmt`/`post_modified`/`post_modified_gmt`/
`ping_status`/`post_password`/`guid`, because nothing before M5 read them, so
M5 adds **one** small additive, greenfield-only migration (following the
exact M4 `0003` column-migration contract) to add those six columns — an
overlaid, populated WordPress database already has them and is read as-is,
unaffected by the migration. Application Passwords reuse WordPress's own
`{prefix}usermeta` mechanism (meta_key `_application_passwords`, a
PHP-serialized array of password records — the same structure and location
real WordPress uses), so **no new table** is required for them. WordPress
6.8+ hashes Application Passwords with `wp_fast_hash()` (a keyed BLAKE2b hash,
prefixed `$generic$`, **not** the phpass/`$wp$`/bcrypt formats
`internal/auth/password` handles, and — critically — keyed by a fixed literal
string baked into WordPress core, not a per-site secret, so it needs no
site-specific configuration to reproduce) — grimoire issues and verifies
Application Passwords the same way, with a phpass/`$wp$` fallback path (via
the existing `internal/auth/password.Verify`) for Application Passwords
created by WordPress installs older than 6.8. The only new **read** port
grimoire adds beyond this is a small, honestly-scoped set of additive reads
needed for REST fields M4's ports don't already expose: a post's category/tag
term IDs (over the existing `{prefix}term_relationships` table), user
listing/counting, post search/ordering, and the `featured_media`/
`media_details` postmeta fields (Req 14.2 enumerates all of them) — every one
of these is a pure additive `SELECT`, none require a new table.

Out of scope for M5: REST create/update/delete for posts, pages, and media
(milestone 06); a rich-text/editor REST surface; WordPress meta-box/custom-field
REST registration (`meta` fields are omitted); revisions, autosaves, and the
REST `/settings` endpoint; taxonomy REST endpoints (`/wp-json/wp/v2/categories`,
`/tags`) beyond the term-ID arrays embedded on posts; OAuth/JWT-style REST auth
(Application Passwords cover the pure-Go, no-third-party-dependency case);
dynamic plugin loading of any kind, in any language; and any compatibility
promise with actual wordpress.org plugin `.php` files.

## Requirements

### Requirement 1 — REST API discovery and namespace routing

**User Story:** As a developer integrating with grimoire, I want a standard
WordPress REST discovery response, so that existing WP REST tooling can find
and use the API without custom configuration.

#### Acceptance Criteria

1. THE system SHALL expose `GET /wp-json/` returning a WordPress-shaped API
   index (`name`, `description`, `url`, `namespaces: ["wp/v2"]`, and a
   `routes` map listing the supported `wp/v2` collection and single-item
   routes with their supported HTTP methods).
2. THE system SHALL expose `GET /wp-json/wp/v2/` returning the namespace
   index for `wp/v2` (routes scoped to this namespace only).
3. ALL `wp/v2` routes SHALL be mounted under `/wp-json/wp/v2/*`, registered so
   they take precedence over the public catch-all `/{slug}` route and do not
   collide with `/admin`, `/admin/api`, or `/wp-content/uploads/`.
4. THE system SHALL set `Content-Type: application/json; charset=UTF-8` on
   every `wp-json` response and reject unsupported methods on a known route
   with `405 Method Not Allowed`.
5. WHEN a request targets an unknown `wp/v2` path, THEN the system SHALL
   return `404` with the standard WordPress-shaped REST error body (Req 13).

### Requirement 2 — Posts and pages: list and single read

**User Story:** As a developer, I want to read posts and pages through the
standard WordPress REST endpoints, so that existing WP REST clients can
display grimoire content unmodified.

#### Acceptance Criteria

1. THE system SHALL expose `GET /wp-json/wp/v2/posts` and
   `GET /wp-json/wp/v2/pages` returning paginated, newest-first collections of
   published posts/pages respectively, and `GET /wp-json/wp/v2/posts/{id}` /
   `GET /wp-json/wp/v2/pages/{id}` returning a single published item by
   numeric ID.
2. THE per-item JSON SHALL include, at minimum, the WordPress field names and
   shapes: `id`, `date`, `date_gmt`, `modified`, `modified_gmt`, `slug`,
   `status`, `type`, `link`, `guid.rendered`, `title.rendered`,
   `content.rendered`, `content.protected` (boolean, true when the post has a
   non-empty password), `excerpt.rendered`, `author` (user ID),
   `comment_status`, `ping_status`, `categories` (array of term IDs; posts
   only), `tags` (array of term IDs; posts only), and `featured_media` (the
   attached image's attachment ID from the `_thumbnail_id` postmeta key, or
   `0`).
3. THE collection endpoints SHALL support the standard WordPress query
   parameters `page`, `per_page` (default 10, capped at a configurable
   maximum), `search`, `slug`, and `orderby=date|id` with `order=asc|desc`.
4. WHEN a requested single post/page ID does not exist or is not published,
   THEN the endpoint SHALL return `404` with the standard REST error body,
   UNLESS the requester is authenticated with a capability that can view
   non-public statuses (Req 8), in which case draft/pending/private items
   SHALL also be resolvable, matching WordPress's `edit_posts`-gated
   `status` query behavior.
5. THE `content.rendered` and `excerpt.rendered` fields SHALL carry the same
   trusted, already-rendered HTML the public HTML views render (no
   re-sanitization, no additional escaping), consistent with M1/M2.2's
   trusted-content contract.
6. THE post/page read ports SHALL reuse the existing `AdminPostRepository`
   (extended with `Search`/`OrderBy`/`Order` filter fields, Req 14.2) plus one
   new additive read port for post metadata (`featured_media` from the
   `_thumbnail_id` postmeta key, and `media_details` assembled from the
   attached media's stored metadata — see Req 14.2), and SHALL return
   identical results whether grimoire is running against a greenfield
   database (using the new 0004 migration's columns, Req 2.2) or an
   overlaid, populated WordPress database (which already has those columns
   populated from real WordPress use).

### Requirement 3 — Comments: list and single read

**User Story:** As a developer, I want to read a post's approved comments
through the REST API, so that a headless or third-party client can display
discussion the same way the built-in theme does.

#### Acceptance Criteria

1. THE system SHALL expose `GET /wp-json/wp/v2/comments` (optionally filtered
   by `?post={id}`) and `GET /wp-json/wp/v2/comments/{id}`, returning only
   **approved** (`comment_approved = '1'`) comments to unauthenticated or
   uncapable requesters, matching the M4 public-visibility contract (Req 3
   of M4).
2. THE per-item JSON SHALL include the WordPress field names: `id`, `post`,
   `parent`, `author_name`, `author_url`, `date`, `date_gmt`,
   `content.rendered`, and `status` (`hold`/`approved`/`spam`/`trash`, mapped
   from the raw `comment_approved` enum for REST-shape parity, matching real
   WordPress's status vocabulary), with `content.rendered` HTML-escaped
   exactly as M4's public comment rendering already treats it (untrusted
   input).
3. WHEN the requester holds the `moderate_comments` capability (Req 8), THEN
   the collection and single-item endpoints SHALL also accept a
   `status=hold|spam|trash|any` filter, matching WordPress's moderator-only
   status query.
4. THE comment read ports SHALL be additive `SELECT`s over the M4
   `{prefix}comments` table, introducing no schema change.

### Requirement 4 — Media: list and single read

**User Story:** As a developer, I want to read the media library through the
REST API, so that a client can resolve and display attachment URLs.

#### Acceptance Criteria

1. THE system SHALL expose `GET /wp-json/wp/v2/media` and
   `GET /wp-json/wp/v2/media/{id}`, returning WordPress-shaped attachment
   JSON: `id`, `date`, `slug`, `type: "attachment"`, `link`, `title.rendered`,
   `author`, `mime_type`, `source_url` (the public upload URL), `post` (the
   attached parent post ID, or `0`), and `media_details` (an object
   including at minimum `width`/`height` for image attachments, sourced from
   the attachment's stored `_wp_attachment_metadata` postmeta, matching
   WordPress's own shape; an empty object `{}` for non-image attachments or
   attachments with no stored metadata).
2. THE media read ports SHALL reuse the M4 `MediaRepository` plus one new
   additive read port for postmeta (`_wp_attachment_metadata`, Req 14.2),
   introducing no schema change.
3. `source_url` SHALL resolve identically to the M4 admin media listing's
   `url` field (uploads base URL + the stored relative path); `link` and
   `source_url` SHALL both follow the URL-absoluteness policy in Req 6.6.

### Requirement 5 — Users: list and single read

**User Story:** As a developer, I want to read author/user information
through the REST API, so that a client can display "by <author>" without a
separate lookup mechanism.

#### Acceptance Criteria

1. THE system SHALL expose `GET /wp-json/wp/v2/users` and
   `GET /wp-json/wp/v2/users/{id}`, returning WordPress-shaped user JSON in
   the unauthenticated **view** context by default: `id`, `name`
   (display name), `slug` (user_nicename), and `link`.
2. WHEN the requester is authenticated with a capability that can list users
   (`list_users`, matching WordPress's `edit_users`-adjacent gate) or is
   requesting their own record, THEN the response SHALL additionally include
   the **edit** context fields: `email`, `url`, and `roles`.
3. THE system SHALL NEVER include a password hash, application-password
   record, session token, or CSRF secret in any user REST response, in any
   context.
4. THE user read ports SHALL be additive `SELECT`s over the existing
   `{prefix}users`/`{prefix}usermeta` tables, extended with a new
   `List`/`Count` capability on `UserRepository` (Req 14.2) to back the
   collection endpoint's pagination, introducing no schema change.
5. THE system SHALL expose `GET /wp-json/wp/v2/users/me` for any
   Application-Password- or session-cookie-authenticated request, returning
   that user's own **edit**-context record (Req 5.2) — the endpoint a real
   WP REST client typically calls first to validate a presented credential.

### Requirement 6 — Response-shape parity: pagination, links, embedding

**User Story:** As a developer using an off-the-shelf WordPress REST client
library, I want grimoire's collection responses to carry the same pagination
and linking metadata real WordPress sends, so that the client's built-in
pagination and relationship-following logic works without modification.

#### Acceptance Criteria

1. EVERY `wp/v2` collection endpoint response SHALL set the
   `X-WP-Total` (total matching items) and `X-WP-TotalPages` (total pages at
   the current `per_page`) response headers.
2. EVERY `wp/v2` item (collection member or single-item response) SHALL
   include a top-level `_links` object with, at minimum, `self` (link to
   the item) and `collection` (link to its collection); posts/pages/media
   SHALL additionally link `author` and, where applicable, `replies`
   (comments) and `wp:attachment`.
3. WHEN a request includes `?_embed` (or `?_embed=author,replies` etc.),
   THEN the response SHALL include a top-level `_embedded` object inlining
   the linked resources named in `_links` (e.g. `_embedded.author` for a
   post's author, `_embedded["wp:featuredmedia"]` when applicable) so a
   client need not issue follow-up requests.
4. THE `_links`/`_embedded` shape SHALL follow the HAL-influenced structure
   WordPress uses (an array of `{href}` objects per relation name under
   `_links`; the corresponding embedded resource objects under
   `_embedded`), not a bespoke grimoire shape.
5. Requests WITHOUT `?_embed` SHALL NOT compute or return `_embedded`, so the
   unembedded path stays as cheap as a plain read.
6. THE `link` field, every `_links[*][].href` URL, and `source_url` SHALL be
   **absolute** URLs (scheme + host + path), constructed from the incoming
   request's scheme and `Host` header at request time (grimoire has no
   site-wide base-URL configuration, per M1–M4 precedent, so there is no
   stored value to build from instead) — matching real WordPress REST
   clients' expectation that these URLs are directly followable without a
   client-supplied base. A deployment behind an untrusted reverse proxy that
   does not set `Host` correctly at the edge SHALL be responsible for
   correcting it before the request reaches grimoire, the same operational
   assumption as any other Host-header-dependent behavior in the system.

### Requirement 7 — Comment creation via the REST API

**User Story:** As a developer building a headless comment form, I want to
submit a comment through the REST API, so that I do not need the
server-rendered form path.

#### Acceptance Criteria

1. THE system SHALL expose `POST /wp-json/wp/v2/comments` accepting a JSON
   body with `post` (post ID), `author_name`, `author_email`, optional
   `author_url`, and `content`, matching the WordPress REST comment-create
   contract's field names.
2. THE endpoint SHALL delegate to the **same** `CommentService.Create` (and
   spam filter) M4 already implements for the server-rendered comment form —
   no new comment business logic is introduced; only the request/response
   shape differs (JSON in, WordPress-shaped comment JSON out with `201`,
   instead of a `303` redirect).
3. THE endpoint SHALL enforce every M4 rule unchanged: default to the
   moderation queue (`comment_approved='0'`), reject a missing/unpublished/
   closed-comments target post (`404`/`403`), reject malformed input
   (`400`), populate `domain.Comment.AuthorIP` from the request the same way
   the server-rendered form path's `commentClientIP(r)` helper does (so the
   per-IP rate limit in M4's spam filter is not silently bypassed for REST
   submissions), and run the spam filter before persisting (Req 3 of M4).
4. THE endpoint SHALL require **either** a valid Application Password (Req 8;
   no CSRF token needed, matching WordPress's own exemption for
   Basic-auth/Application-Password requests) **or**, for a request
   authenticated by session cookie, a valid `X-CSRF-Token` header per the M4
   contract; an **anonymous** request (no session, no Application Password)
   SHALL be accepted and protected the same way M4's public comment form is
   — the double-submit token is REST-inapplicable (no server-rendered hidden
   field), so an anonymous REST comment submission SHALL instead be subject
   to the same spam filter, per-IP rate limit, and moderation-queue default
   as the safety net (Req 3/12 of M4), consistent with real WordPress, which
   also accepts anonymous REST comment submissions on an open-comments post.
5. ALL OTHER `wp/v2` write methods — `POST`/`PUT`/`PATCH`/`DELETE` on `posts`,
   `pages`, `media`, `users`, and `PUT`/`PATCH`/`DELETE` on `comments/{id}`
   (comment moderation via REST) — SHALL return `501 Not Implemented` with a
   REST error body explicitly stating the operation is deferred to a later
   milestone — grimoire SHALL NOT silently 404 or 405 a write it plans to
   support later, so a client can distinguish "not supported yet" from "does
   not exist". This blanket rule explicitly EXCLUDES the
   `/wp-json/wp/v2/users/me/application-passwords*` routes (Req 9), which are
   implemented in this milestone and SHALL NOT be shadowed by the `users`
   501 catch-all.

### Requirement 8 — Application Passwords: storage and verification

**User Story:** As a developer, I want to authenticate REST requests with a
revocable, non-primary-password credential, so that I never have to give a
third-party tool my login password.

#### Acceptance Criteria

1. THE system SHALL support HTTP Basic authentication on any `wp-json`
   request where the "password" half is an **Application Password**: a
   per-user, named, revocable random credential distinct from the user's
   login password.
2. APPLICATION PASSWORDS SHALL be stored as a single `{prefix}usermeta` row
   per user under the WordPress meta key `_application_passwords`, whose
   value is a PHP-serialized array of records (`uuid`, `app_id`, `name`,
   hashed `password`, `created`, `last_used`, `last_ip`), matching
   WordPress's own storage location and structure — introducing **no new
   table**.
3. WHEN grimoire creates a new Application Password, THEN its secret SHALL be
   a cryptographically random token, displayed to the user **exactly once**
   at creation time, and stored only as a `$generic$`-prefixed `wp_fast_hash()`
   hash (WordPress 6.8+'s own Application Password hashing: a keyed BLAKE2b
   hash of the secret, keyed by the fixed literal `wp_fast_hash_6.8+` — not a
   per-site secret, so grimoire reproduces it with no additional
   configuration) — never in plaintext or reversible form.
4. WHEN grimoire verifies an Application Password presented via HTTP Basic
   auth, THEN it SHALL: (a) if the stored hash is `$generic$`-prefixed,
   recompute `wp_fast_hash()` over the presented secret and compare in
   constant time, matching WordPress 6.8+'s `wp_verify_fast_hash()`; (b)
   otherwise, fall back to the same layered verifier
   (`internal/auth/password.Verify`) M2/M2.1 use for login passwords
   (phpass/`$wp$`/bcrypt), covering Application Passwords created by
   WordPress installs older than 6.8 (which hashed them the same way as
   login passwords) — so an Application Password hash already present in an
   **overlaid, populated WordPress database** verifies correctly regardless
   of which WordPress version created it, without any migration.
5. WHEN an Application Password verifies successfully, THEN the system SHALL
   update its `last_used` timestamp and the requester's IP, and SHALL
   authenticate the request as that user's `Principal` with the user's normal
   roles/capabilities (no separate "API-only" capability set).
6. IF a request presents an `Authorization: Basic` credential pair and it does
   **not** verify (unknown username, or a password that fails Req 8.4's
   verification), THEN the system SHALL reject the request with `401` and
   the standard REST error body (`rest_invalid_credentials`) **immediately**
   — for every endpoint, public or not, and regardless of whether a separate,
   valid session cookie is also present on the same request. Presenting a
   credential is an assertion of identity; a request with a valid session
   cookie SHALL simply omit the `Authorization` header rather than send a
   Basic credential it expects to be ignored. This SHALL be the **only**
   invalid-Basic-auth behavior specified by this milestone (superseding any
   other reading); a request with **no** `Authorization` header at all is
   unauthenticated (not invalid) and is evaluated purely on session-cookie
   auth, per Req 7.4.
7. AN APPLICATION PASSWORD REQUEST SHALL NOT require a `X-CSRF-Token` or any
   session cookie — Application Password auth is inherently not
   browser/cookie-based, matching WordPress's own exemption.
8. THE system SHALL NEVER echo an Application Password secret (only its
   name/uuid/timestamps) in any REST response, log line, or error message
   after the one-time creation response.
9. IF an `Authorization: Basic` credential is presented over a connection
   that is neither TLS-terminated (directly, or via a configured trusted
   reverse-proxy header) nor addressed to a loopback host (`localhost`,
   `127.0.0.1`, `::1`), THEN the system SHALL reject the request with `401`
   before attempting verification, matching real WordPress's own refusal to
   accept Application Passwords over a non-`https`, non-local request. This
   check SHALL be configurable (default: enabled) following the same
   operator-declared-trust model as the existing `CookieSecure` session-cookie
   setting, since grimoire cannot itself detect TLS terminated by a reverse
   proxy in front of it.

### Requirement 9 — Application Password self-service management

**User Story:** As a logged-in user, I want to create, list, and revoke my own
Application Passwords, so that I can grant and later cut off API access for a
specific tool without changing my login password.

#### Acceptance Criteria

1. THE system SHALL expose `GET /wp-json/wp/v2/users/me/application-passwords`
   returning the authenticated user's own Application Password records
   (name, uuid, created, last_used, last_ip — **never** the hash or secret).
2. THE system SHALL expose
   `POST /wp-json/wp/v2/users/me/application-passwords` accepting a `name`,
   creating a new Application Password (Req 8.3) and returning the
   plaintext secret **once**, in the creation response only.
3. THE system SHALL expose
   `DELETE /wp-json/wp/v2/users/me/application-passwords/{uuid}` revoking
   (removing) the named credential; subsequent authentication attempts with
   the revoked secret SHALL fail.
4. THESE self-service endpoints SHALL require the requester to be
   authenticated as the target user via the **M2 session cookie** (not
   Application Password auth, avoiding a credential that can mint or revoke
   its own replacements), and the unsafe methods (`POST`/`DELETE`) SHALL
   require a valid `X-CSRF-Token` per the M4 contract.
5. THIS milestone SHALL NOT expose an endpoint for one user (even an
   administrator) to create or list **another** user's Application
   Passwords; that cross-user management surface is deferred alongside the
   rest of admin user-management to a later milestone.

### Requirement 10 — Extension hook registry

**User Story:** As a Go developer extending grimoire, I want a native
extension mechanism analogous to WordPress's actions/filters, so that I can
observe or transform behavior at defined points without forking grimoire's
core code paths.

#### Acceptance Criteria

1. THE system SHALL provide a `pkg/extensions` package (importable by both
   grimoire-internal code and external Go modules — a plain `internal/`
   package cannot be imported outside `github.com/roboweaver/grimoire`,
   Go's own import-visibility rule, so the public hook-registration API
   lives at this importable path; this is grimoire's first `pkg/`
   directory) defining two registration mechanisms: **actions** — a named
   point where zero or more registered callbacks are invoked for their side
   effects, in registration order, with no return value observed by the
   caller — and **filters** — a named point where zero or more registered
   callbacks each receive and return a value of the same type, chained so
   each filter's output becomes the next filter's input (WordPress's
   `do_action`/`apply_filters` vocabulary, Go-typed).
2. THE system SHALL expose `extensions.RegisterAction(hook string, fn ActionFunc)`
   and `extensions.RegisterFilter(hook string, fn FilterFunc)` (or an
   equivalent typed, generics-based API) for extensions to register
   callbacks, intended to be called from a package-level `init()` so
   registration happens at process startup before any request is served.
3. THE system SHALL expose `extensions.DoAction(ctx, hook string, payload any)`
   and `extensions.ApplyFilters(ctx, hook string, value T) (T, error)` (or
   equivalent) for grimoire's own code to fire a hook at a defined point.
4. A FILTER CALLBACK THAT RETURNS AN ERROR SHALL short-circuit the remaining
   chain and SHALL propagate the error to the caller (e.g. aborting an HTTP
   response with a `500`), rather than silently continuing with a
   partially-applied value.
5. BOTH AN ACTION CALLBACK AND A FILTER CALLBACK THAT PANIC SHALL be recovered
   by the registry so one misbehaving extension cannot crash the request or
   the process; the recovered panic SHALL be logged via the existing
   structured logger (`slog`) with the hook name, and (for a filter) the
   chain SHALL short-circuit and return the input value unchanged alongside
   an error, following the same short-circuit contract as Req 10.4.
6. THE hook registry SHALL be safe for concurrent use (registration is
   expected only at `init()` time; invocation happens on every request from
   many goroutines).
7. THE registry SHALL require no dynamic loading, no `.so`/`.dll` plugin
   files, no separate process, and no interpreter: every registered
   callback SHALL be Go code **compiled into the same binary** as grimoire
   core. THIS SHALL be stated explicitly in the design as the boundary of
   grimoire's extensibility — it is not a WordPress-plugin-compatible
   runtime.

### Requirement 11 — Concrete extension points wired into M5 code paths

**User Story:** As a Go developer, I want at least one real, working example
of each hook kind wired into grimoire's actual request handling, so that the
extension mechanism is proven rather than theoretical.

#### Acceptance Criteria

1. THE system SHALL define a **post-render filter** hook (e.g.
   `"render.post_html"`) invoked with the fully-rendered HTML buffer for a
   public `single`/`page` view immediately before it is written to the
   response, allowing a registered filter to transform the final HTML (e.g.
   inject a script tag, rewrite a link) without touching the render engine
   or handler code.
2. THE system SHALL define a **REST request filter/action pair** (e.g. an
   action `"rest.pre_dispatch"` fired after authentication resolution (Basic
   Application-Password verification or session lookup, Req 8) but before
   the route handler runs, and a filter `"rest.response"` applied to the
   assembled JSON-serializable response value before it is marshaled and
   written) for every `wp/v2` route, so an extension can inspect an
   incoming REST request or transform an outgoing REST response body (e.g.
   add a custom field, redact a field) without modifying the route
   handlers. Consistent with M4's established pattern, CSRF verification
   (where required, Req 7.4/9.4) SHALL remain a check **inside** the
   specific write handler that needs it, not a centralized pre-dispatch
   gate — `"rest.pre_dispatch"` fires before that per-handler CSRF check,
   not after it.
3. THE system SHALL define a **comment-submit action** (e.g.
   `"comment.submitted"`) fired by `CommentService.Create` **after** a
   comment is successfully persisted (any status, including `spam`),
   carrying the created `domain.Comment` and its parent `domain.Post`, so an
   extension can react (e.g. send a notification) without changing
   `CommentService`.
4. THE post-render filter SHALL only apply to the public theme render path
   (not the admin SPA, not REST JSON responses), and SHALL leave the
   response unmodified when no filter is registered for the hook (a
   zero-registration hook SHALL be a no-op, never an error).
5. A REGISTERED EXTENSION AT ANY OF THESE THREE POINTS SHALL introduce no
   observable behavior change when it registers no callback for a given
   hook — the hooks SHALL be additive instrumentation points, not required
   participants in the request path.

### Requirement 12 — Explicit extensibility boundary (non-goals)

**User Story:** As a site owner evaluating grimoire, I want an honest
statement of what "extensible" means here, so that I do not assume real
WordPress plugins will work.

#### Acceptance Criteria

1. THE design documentation SHALL explicitly state that grimoire's extension
   mechanism does **not** execute PHP, does **not** load `.php` plugin files
   from a `wp-content/plugins/` directory or any directory, and does **not**
   provide a plugin marketplace, plugin activation/deactivation admin UI, or
   plugin-update mechanism.
2. THE design documentation SHALL explicitly state that an extension must be
   Go source code compiled into the grimoire binary at build time — there is
   no supported mechanism in M5 for installing an extension into a running
   grimoire instance without a rebuild.
3. THE design documentation SHALL explicitly state what compatibility
   **is** provided: a small, defined set of Go hook points (Req 11) that a
   first-party, vendored, or external Go package can register against using
   the importable `pkg/extensions` API (Req 10), sufficient to observe or
   transform behavior at those points without modifying grimoire core.

### Requirement 13 — REST error handling and observability

**User Story:** As a client developer, I want grimoire's REST errors to match
the WordPress REST error shape, so that existing WP-REST-aware error handling
in clients works unmodified.

#### Acceptance Criteria

1. THE `wp-json` API SHALL return errors in the WordPress REST shape:
   `{ "code": "...", "message": "...", "data": { "status": <http-status> } }`
   (distinct from, and not to be confused with, the M3/M4 admin-API envelope
   `{ "error": { "code", "message" } }`, which remains unchanged for
   `/admin/api/*`).
2. THE `wp-json` API SHALL map common conditions to WordPress's own error
   codes where a direct analog exists (e.g. `rest_post_invalid_id` for a
   missing post, `rest_forbidden` for a capability failure, `rest_no_route`
   for an unknown path) with the matching HTTP status (`404`, `403`, `404`
   respectively).
3. THE `wp-json` API SHALL NEVER include SQL text, stack traces, driver
   errors, filesystem paths, password/Application-Password hashes, or
   session/CSRF secrets in a client response.
4. THE `wp-json` API SHALL log server-side failures via the existing
   structured logger (`slog`) with enough context to debug (route, resource
   ID, status) without logging secrets or full request/response bodies.

### Requirement 14 — Vendor-agnostic, existing-WP-DB-overlay compatibility

**User Story:** As a developer, I want M5 to preserve grimoire's core
constraints, so that the REST API and extension points work on any supported
vendor and against a live WordPress database.

#### Acceptance Criteria

1. ALL M5 read paths (posts, pages, comments, media, users) SHALL be additive
   `SELECT` ports introducing **no** schema change when grimoire is running
   against an existing, overlaid WordPress database (which already has every
   column M5 reads, including the six REST-only post columns in Req 2.2 —
   real WordPress has always written them). Against a **greenfield**
   grimoire-provisioned database, M5 adds exactly **one** new additive
   migration (`0004_rest_post_fields`, following the exact M4 `0003`
   column-migration contract) to backfill those same six post columns
   (`post_date_gmt`, `post_modified`, `post_modified_gmt`, `ping_status`,
   `post_password`, `guid`) with WordPress-matching defaults — this is the
   **only** schema change introduced by this milestone, and it SHALL be a
   no-op (already-present columns, left untouched) when applied to an
   overlaid WordPress database.
2. THE full set of new **storage surfaces** M5 introduces, beyond the one
   migration in Req 14.1, is: (a) an additive post→term-IDs read port over
   the existing `{prefix}term_relationships` table, ordered by term name
   ascending (matching WordPress's default term ordering) for cross-vendor
   determinism; (b) reading/writing the `_application_passwords` usermeta
   key via the existing `UserMetaRepository`; (c) `List`/`Count` methods
   added to `UserRepository` to back the `users` collection endpoint's
   pagination (Req 5.4); (d) `Search`, `OrderBy`, and `Order` fields added to
   `AdminPostFilter` to back the `posts`/`pages` collection endpoints'
   `search`/`orderby`/`order` query parameters (Req 2.3); and (e) a new
   additive postmeta read port covering the `_thumbnail_id` (backing
   `featured_media`, Req 2.2) and `_wp_attachment_metadata` (backing
   `media_details`, Req 4.1) meta keys. None of (a)–(e) requires a new table
   or column — every one is a `SELECT` (or, for Application Passwords, a
   read/write of an existing generic-usermeta port) over structures M1–M4
   already created.
3. THE new ports SHALL return identical shapes and behavior across MySQL,
   PostgreSQL, and SQLite, verified by the shared vendor-parameterized
   contract suite.
4. THE new read/write ports SHALL be defined in the domain layer and
   implemented per-vendor in the storage layer, with no database driver
   imported by the domain, content, render, extensions, or web layers.
5. WHEN grimoire overlays an existing WordPress database, THEN REST reads,
   REST comment creation, and Application Password verification SHALL
   interoperate with data authored by real WordPress (including
   WordPress-created Application Passwords) without corruption or schema
   drift.

## Implementation deviations

_None yet — this milestone is spec-only; deviations will be recorded here
once implementation begins._
