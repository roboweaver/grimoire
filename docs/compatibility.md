# WordPress Compatibility (M1-M12, M16-M20, M22, M23)

grimoire replicates the WordPress **database schema, authentication model,
and REST API surface** — not its GPL PHP source — so it can read, render,
and (for supported content types) manage an existing WordPress site through
a WordPress-compatible admin and API. See
[`./wordpress-compatibility-tour.md`](./wordpress-compatibility-tour.md) for
a visual, side-by-side comparison.

## WordPress tables used

With the configured `table_prefix` (default `wp_`):

| Table | Used for |
|-------|----------|
| `posts` | posts + pages (public read: `post_status='publish'`; admin/REST: full CRUD across statuses, revisions, and scheduling) |
| `postmeta` | read + one narrow write — featured-image (`_thumbnail_id`) and attachment metadata (`_wp_attachment_metadata`) are read only; the single `_wp_attached_file` key is the only key ever written, and only when a new attachment is created |
| `options` | `blogname`, `blogdescription`, … site settings |
| `terms` | category and tag names + slugs (read + write: create, rename, delete) |
| `term_taxonomy` | taxonomy rows (`category`, `post_tag`) + counts (read + write; counts are recomputed whenever a post's term assignments change) |
| `term_relationships` | post ↔ taxonomy links (read + write, via category/tag assignment) |
| `users` | read for authentication (`user_login` is looked up and `user_pass` verified — login is never by email) and profile display (`user_email` is display/profile data, not part of login); writes: new-user creation (`create_users`-authorized calls and CLI bootstrap) and password-hash upgrades on successful login against a legacy hash |
| `usermeta` | read + written — role/capability assignment (`{prefix}capabilities`, legacy `{prefix}user_level`) and other single-valued user meta |
| `comments` | comment storage and moderation workflow (read + write) |
| `commentmeta` | comment metadata (read + write) |

grimoire also has its own additive `sessions` table (not part of the native
WordPress schema) for its own session/CSRF-token management. Unlike the
read-only verification recipe below, this table **is** created (via
`grimoire-cli migrate`, `IF NOT EXISTS`) and written to on every login —
inside whatever database grimoire is configured against, under that
database's own `table_prefix`. If grimoire is pointed at a real WordPress
database, this additive table is created and written there too; it has no
WordPress-native counterpart and does not overlap with any WordPress core
table's columns or rows.

Type mappings from the WordPress MySQL schema are translated per vendor
(`BIGINT(20) UNSIGNED`→`BIGSERIAL`/`INTEGER`, `LONGTEXT`→`TEXT`,
`DATETIME`→`TIMESTAMP`/ISO-8601 `TEXT`, prefix-length keys→plain indexes). See
`internal/storage/migrations/<vendor>/0001_init.up.sql`.

## What's implemented (M1-M9b)

- **M1 — Content core:** switchable database vendor (MySQL/PostgreSQL/
  SQLite), WordPress-compatible schema, public read rendering of posts,
  pages, categories, and archives.
- **M2 — Users, auth, roles:** WordPress-compatible user accounts, sessions,
  and role-aware access control.
- **M3 — Embedded admin:** the Adobe React Spectrum admin shell for managing
  site content.
- **M4 — Comments, media, menus:** comment workflows, media handling (the
  embedded admin UI supports uploading new attachments and reassigning their
  parent post, in addition to reading rows from the database; see
  `media.uploads_dir` in the README), and navigation menus.
- **M5 — Extensions and REST API:** the compiled `pkg/extensions` hook
  registry and a WordPress-compatible REST API at `/wp-json/wp/v2/*` — read
  parity for posts/pages/comments/media/users, plus write endpoints for
  comments, posts/pages, and categories/tags. At M5 the REST media and user
  endpoints offered read only; their write verbs returned `501`. Media
  create/update and user create/update were later enabled by M10.B and
  M10.C respectively (see the M10 bullets below); only the `DELETE` verb
  for media and users still returns `501`. Authorized write paths also
  exist outside REST (M4's admin UI for media, and `create_users`-gated
  calls / CLI bootstrap for users).
  Non-anonymous REST auth uses WordPress Application Passwords.
- **M6 — Admin CRUD editor:** full content editing, status transitions, and
  optimistic concurrency in the admin UI.
- **M7 — Revisions and scheduler:** revision history, autosave, and
  scheduled publishing.
- **M8 — Content browsing parity:** WordPress-equivalent pagination and
  total-page navigation on the public home page and category archives (a
  page number past the last available page returns `404` once the site has
  at least one published post); admin post list `search`/`status`/`author`
  filters with pagination; and an admin media library with
  `search`/`type`/`after`/`before` (upload-date range)/`parentId` filters,
  pagination, and a mutually exclusive grid/list view toggle. Routing and
  taxonomy (nested categories, permalink tokens) and the REST media/user
  write endpoints are unchanged by this milestone; permalinks are addressed
  by M9a below.
- **M9a — Permalinks and canonical routing:** `permalink_structure`,
  `category_base` and `tag_base` are read from the site's own `options`
  table at startup, and single posts and pages are served at the structure
  those options describe instead of only at a flat `/{slug}` path. The
  supported tokens are `%postname%`, `%post_id%`, `%year%`, `%monthnum%`
  and `%day%`, in any order and combination, which covers WordPress's
  three presets ("Day and name", "Month and name", "Post name") and their
  numeric forms. Date components are zero-padded exactly as WordPress
  writes them, and a date path that contradicts the post's own
  `post_date` returns `404` rather than serving the post at someone
  else's date. Every non-canonical form that still identifies the post —
  the flat `/{slug}` path, the wrong trailing-slash form — returns `301`
  to the one canonical path, preserving the query string; the canonical
  trailing slash follows whether `permalink_structure` itself ends in
  `/`. The REST `link` field now returns that same canonical permalink
  (the web layer still absolutises it against the request's scheme and
  host). Zero schema changes: this milestone reads two additional option
  rows and adds no column, table or index.

  Deliberate edges:

  - An empty `permalink_structure` (WordPress's "plain" setting) keeps the
    pre-M9a flat `/{slug}` behavior, with no canonical redirects at all.
  - A structure containing an unsupported token (`%category%`,
    `%author%`) or no post-identifying token at all falls back to the flat
    route and says so, loudly: a `WARN` at startup naming the offending
    token and stating that published URLs will not resolve while the
    fallback is active, plus a line in `grimoire-cli migrate -check` so
    the condition is discoverable before the server is started. grimoire
    reads a database it does not own, so an unparseable structure degrades
    to a working flat site rather than refusing to boot.
  - `commentLink` in the REST API keeps its plain-permalink-shaped
    fallback (`/?p={id}#comment-{id}`), because no comment route exists to
    point it at. `userLink` kept its `/?author={id}` fallback for the same
    reason until M9b added the author archive; it now returns
    `/author/{nicename}`.

  Divergent or still open after M9a:

  - **Pages follow the post structure, where WordPress exempts them.**
    ✅ Resolved in M15. Through M9a–M9b the configured structure was applied to
    `page` rows as well as `post` rows, so with a dated structure a page's
    canonical was `/2024/05/17/about/`. M15 makes a `page` exempt from
    `permalink_structure`: its canonical is now its nested `post_name` ancestry
    over `post_parent` (`/{parent}/{child}/`), regardless of structure, and the
    flat and old dated forms `301` to it — matching WordPress, and closing the
    one place M9a's canonical URL could differ from the URL the site published.
  - Changing `permalink_structure` in WordPress requires restarting
    grimoire, since the options are read once at startup rather than per
    request. This mirrors WordPress's own rewrite-rule flush.

  M9a additionally listed three gaps here — no tag/date/author archive
  routes (roadmap group 9.C), no nested category paths (9.D), and
  `category_base`/`tag_base` read and resolved but honored by no route.
  All three are closed by M9b below.

- **M9b — Archives and nested categories:** the four archive routes M9a
  left unserved — nested category, tag, author and date — are served, and
  `category_base`/`tag_base` now take effect. Zero schema changes:
  `term_taxonomy.parent` already exists in every vendor's
  `0001_init.up.sql` and is populated by WordPress itself, so this
  milestone reads a column it does not create.

  - **Nested category archives are canonical.** A category is served at
    its base segment followed by its full ancestry, root first
    (`/category/news/local`). The path is resolved by walking the
    taxonomy graph — first segment against the taxonomy's root terms, each
    later segment against the children of the term the previous one
    matched — from a single read of the taxonomy's terms per request, so
    two categories sharing a slug under different parents are each
    reachable at their own path and neither shadows the other. A flat,
    wrong-ancestor or skipped-level path whose final segment names a
    category `301`s to that category's canonical path, preserving the
    query string; a top-level category's canonical path is the flat path,
    so it renders `200` rather than redirecting to itself. A final segment
    naming no category `404`s, and so does the bare base segment
    (`/category`) — WordPress serves no category index.
  - **A category archive lists and counts its descendants' posts.** The
    listing and the pagination total describe the same
    descendant-inclusive set, and a post filed under both a parent and one
    of its descendants appears once and is counted once (an `EXISTS`
    semi-join, not a join).
  - **Tag archives** serve at `/{tag_base}/{slug}`. `post_tag` is flat in
    WordPress, so there is no ancestry to walk and `term_taxonomy.parent`
    is ignored for that taxonomy.
  - **Author archives** serve at `/author/{user_nicename}`, resolving by
    `user_nicename` and rendering `display_name`. `user_login` is never
    rendered, so the archive publishes no login name the site had not
    already exposed. The base segment is the literal `author`: WordPress
    stores no `author_base` option — it is a `WP_Rewrite` property, not a
    row in `options` — so there is nothing to read and nothing to
    configure.
  - **Date archives** serve at year, year/month and year/month/day
    granularity, with WordPress's own widths (4-digit year, zero-padded
    2-digit month and day, so a 1-digit month does not match). The range
    is a half-open `[start, end)` comparison on `post_date` built in UTC —
    no vendor-specific date function, so one statement serves MySQL,
    PostgreSQL and SQLite — and a path that names no real calendar date
    (`2024/02/30`, `2024/13/01`) `404`s without querying.
  - **`category_base` and `tag_base` are honored** in route registration,
    path classification, the theme's archive and pagination links and the
    REST `link` fields. Each value is normalized first — surrounding
    whitespace trimmed, leading and trailing slashes trimmed, repeated
    internal slashes collapsed — because WordPress's
    `options-permalink.php` prefixes the submitted base with `/` before
    storing it, so `get_option( 'category_base' )` plausibly returns
    `/topics`. `grimoire-cli migrate -check` now reports both resolved
    bases and whether each came from the option or from the WordPress
    default, replacing the pre-M9b silence that was argued on the grounds
    that no route honored either.
  - **Collisions between bases degrade loudly rather than silently.** If
    `category_base` and `tag_base` resolve to the same segment, or either
    resolves to `author`, the category meaning wins; if a base equals a
    leading literal of the permalink structure, the post meaning wins (see
    the divergences below). Every such case is named in a startup `WARN`
    and in `migrate -check`, and none of them is an error from
    `routing.Parse` — a renamed base is no reason to stop serving
    permalinks.
  - **Pagination keeps M8's contract.** Every archive returns the
    same `Page`/`PerPage`/`Total`/`TotalPages` shape, selected by `/page/{n}/`
    or `?page=N` (M16, below) with the same page size as the home page. An archive whose entity
    exists but holds no published posts renders empty with `200`; a page
    number past the last page `404`s, matching the home and category rule.
    Theme pagination links are built from the archive's canonical path
    rather than from a hard-coded `/category/{slug}`, which was wrong the
    moment a category was nested or a base was overridden.
  - **Route precedence lives in one pure, exported function**,
    `routing.Structure.Classify`, with a documented precedence table and a
    unit test per row — not in chi registration order, which resolves a
    parameter-node collision silently in favour of whichever pattern was
    registered last.
  - **REST parity for the new surfaces.** `parent` on a term now carries
    `term_taxonomy.parent` instead of a hard-coded `0`; a category's and a
    tag's `link` are the real archive paths (a category's carrying its full
    ancestry, so the advertised URL is the one that returns `200` rather
    than the flat one that `301`s); a user's `link` is
    `/author/{nicename}`. `commentLink` deliberately stays on
    `/?p={id}#comment-{id}`, because no comment route exists and changing
    it would advertise a URL that `404`s.

  Matches WordPress rather than diverging — listed because the behavior
  looks arbitrary without the provenance:

  - **The permalink front is applied asymmetrically.** The structure's
    leading literal segment(s) — `blog` in
    `/blog/%year%/%monthnum%/%postname%/` — are prepended to date and
    author archives always, but to category and tag archives only when the
    corresponding base option is unset. This is WordPress's own condition,
    `with_front => ! get_option( '{taxonomy}_base' )`. "Unset" means empty,
    matching WordPress's truthiness test on `get_option()`, so a base set
    explicitly to the same string as the default (`category_base` =
    `category`) counts as **set** and drops the front.
  - **Date archives move under a `date/` segment when `%post_id%` appears
    among the structure's first three tokens**, so a bare `/%post_id%/`
    structure serves `/date/2024` and leaves `/2024` to post 2024's own
    canonical permalink. This is
    `WP_Rewrite::get_date_permastruct()`'s rule verbatim, keyed on the
    token index rather than on path segments, and it fires for no other
    structure — `/%postname%/` and `/%year%/%monthnum%/%day%/%postname%/`
    keep serving date archives at their bare paths, exactly as WordPress
    does.
  - **A date archive beats a post whose slug has a date's shape.** Under a
    `/%postname%/` structure a post slugged `2024` is unreachable at
    `/2024`, and under `/%year%/%monthnum%/%postname%/` a post slugged with
    a bare 2-digit number is unreachable at the day position. WordPress's
    rewrite-rule ordering resolves both the same way.
  - **An archive base beats a post slugged identically to it.** Under a
    single-segment structure, a post slugged exactly `category`, `tag`,
    `author` or a configured base is unreachable, because a path rooted at
    a base segment names that archive or names nothing and never falls
    through to the post interpretation.
  - **Date archives are not served under plain permalinks.** With an empty
    `permalink_structure` no date routes are registered and no path
    classifies as a date archive, which is what WordPress does — its
    plain-permalink date archives are the `?m=2024` query argument, not
    `/2024` — and it is what keeps `/2024` resolving a post slugged `2024`.
    The `?m=` query argument is served on `GET /` (M16, PR 2, below), so a
    plain-permalink site reaches its date archives through `?m=` as in
    WordPress.
  Divergent or still open after M9b:

  - **A base that collides with the permalink structure's leading literal
    keeps the post meaning.** `category_base` = `archives` against
    WordPress's "Numeric" preset `/archives/%post_id%` — a rename an
    operator makes precisely *because* their URLs live under `/archives/` —
    would otherwise classify `/archives/123` as a category, resolve
    nothing, and `404` every post URL on the site from its own canonical
    permalink. grimoire keeps every published post URL resolving and lets
    the archive at that base be the surface that degrades. The condition is
    reported at startup and by `migrate -check`, which is also the surface
    that explains why that archive's advertised REST `link` does not
    return `200`.
  - **A duplicate `user_nicename` resolves to the lowest `ID`.**
    `user_nicename` carries a **non-unique** index in WordPress's schema
    (verified against the live database: `Non_unique = 1`), so duplicates
    are schema-legal even though `wp_insert_user()` suffixes a colliding
    nicename on write — direct SQL, imports, multisite merges and
    pre-suffix-era WordPress all produce them. WordPress's own read
    resolves such a duplicate **arbitrarily**: a `LIMIT 1` with no
    `ORDER BY`, behind an object cache, so the winner can flip on a cache
    flush. grimoire orders by `ID` ascending instead, so the three vendors
    agree with each other and a contract test has something stable to
    assert. **The impact is identical either way:** one
    `/author/{nicename}` URL can surface only one of the colliding authors,
    so the other's posts are unreachable at that URL. There is no error and
    no wrong data — one author is invisible there.
    `grimoire-cli migrate -check` reports the condition, naming each
    duplicated nicename, how many rows share it and which `ID` wins, so it
    is discoverable before the site serves rather than after.
  - **A duplicate term slug within a taxonomy resolves deterministically
    too, and the category archive does not depend on the question at all.**
    WordPress enforces within-taxonomy slug uniqueness on write
    (`wp_unique_term_slug()` suffixes a collision) but the schema does not:
    `terms.slug` carries a non-unique index. Category archives resolve by
    walking the segment path, so a duplicate slug under a different parent
    costs nothing; where the walk genuinely cannot separate two candidates —
    sibling terms sharing a slug, or the redirect recovery that matches a
    path's final segment — the lowest `term_id` wins. Tags have no path to
    walk and so resolve by slug, also to the lowest `term_id`, with the
    same impact shape as the nicename case: one of the colliding tags is
    unreachable at that URL. (The reference WordPress database has no
    duplicate slug within a taxonomy.)
  - **The category archive now lists `post` rows only.** `ByTermSlug` and
    `CountPublishedByTermSlug` applied no post-type predicate before M9b,
    so a published `page` assigned to a category appeared in that
    category's archive and no longer does — matching `RecentPosts` and
    WordPress's own archive queries. This is a user-visible removal on a
    route that already shipped, and it is invisible on the reference
    database (only `post` rows carry categories there), which is why it is
    recorded here rather than observed.
  - **No index on `user_nicename` in grimoire's own migrations** (a known
    limitation, not a pending fix). The only `users` index any of the three
    vendors' `0001_init.up.sql` creates is on `user_login`, so
    `/author/{nicename}` is a full table scan on a **grimoire-created**
    database. WordPress-created databases carry WordPress's own index, so
    the reference site and every real deployment this milestone targets are
    unaffected; adding one would break the zero-migration property, which
    is a worse trade than a scan over the `users` table of a database only
    grimoire's own tests and fresh installs create.
  - The `%category%`/`%author%` permalink tokens and plugin-registered rewrite
    rules remain out of scope, unchanged by this milestone. Custom taxonomies
    beyond `category` and `post_tag`, and custom post types, were out of scope
    here and are now a scoped milestone (M12 read, M17 write; below). (Term-hierarchy
    *editing* shipped in M13 and hierarchical *page* URLs in M15; this M9b note
    predates all three.)

  Validated read-only against a reference WordPress database (a local
  Podman dev stack, MySQL 8.0, `permalink_structure`
  `/%year%/%monthnum%/%day%/%postname%/`, `category_base` empty): **33
  category terms, 28 of them nested**, every one of the 33 derived paths
  round-tripping back through the classifier to the same segments and the
  same trailing-slash form, and five nested archives checked end to end
  over real HTTP — canonical path `200`, flat form `301` to it, query
  string preserved.

- **M10a — Write-boundary content safety:** grimoire's first HTML
  sanitizer. A single capability-aware policy sits above storage in front
  of the write paths that already shipped (public comment submit, admin/
  REST post create and update), selecting one of three allow-list tiers by
  field kind and `unfiltered_html`: a tight comment list, the broad
  post-content list for writers lacking `unfiltered_html`, and a
  byte-identical bypass for `unfiltered_html` holders (WordPress's `kses`
  exemption). `post_title` is reduced to plain text, and comment content is
  backstopped at the tight tier on render as well as on write. The
  guarantee is scoped: every value written from M10a onward is sanitized at
  the writer's tier; pre-M10a stored content is not retroactively
  sanitized. M10a added no new REST write route (see the REST section
  above).
- **M10.B — REST media write:** `POST /wp-json/wp/v2/media` (binary upload
  create) and `PUT`/`PATCH`/`POST /media/{id}` (metadata update) over the
  existing `domain.MediaWriter`, gated on `upload_files` and sanitized
  through M10a's policy; a new attachment's `post_author` is the uploader.
  `DELETE /media/{id}` stays `501`.
- **M10.C — REST user write:** `POST /wp-json/wp/v2/users` (create) and
  `PUT`/`PATCH`/`POST /users/{id}` (profile/password/role update) over a new
  `domain.UserRepository.UpdateProfile`, gated on `create_users`/
  `edit_users`/`promote_users` and sanitized through the policy.
  `DELETE /users/{id}` stays `501`.
- **M10.D — `content.rendered` improvements:** the REST `content.rendered`
  field strips Gutenberg `<!-- wp:* -->` block-comment delimiters and adds
  `srcset`/`sizes`/`loading`/`decoding` to rendered `<img>` tags. This
  changes the REST response field only — the public HTML templates are
  untouched and stored `post_content` is never rewritten.
- **M10.E — M10 write-route test coverage:** capability-matrix tests
  (allowed vs. forbidden caller) for each write route plus a consolidated
  content-safety coverage matrix; test-only, no behavior change.
- **M10.F — Navigation menu editing:** the first menu write path —
  create/update/delete of menus and items, reorder via `menu_order`, nest
  via parent item — in a new admin-API route group gated on
  `edit_theme_options` with CSRF, item labels/names/custom-URLs sanitized
  and validated through M10a's policy. Menu reads keep their existing
  `edit_posts` gate; nav-menu editing was deferred read-only from M4.
- **M10.G — `$wp$` as grimoire's issued password format:** grimoire now
  *issues* WordPress 6.8 `$wp$` hashes (HMAC-SHA384 pre-hash → bcrypt), not
  bare bcrypt, and `NeedsRehash` upgrades a legacy bcrypt or phpass row to
  `$wp$` on the user's next successful login. A password changed in grimoire
  now round-trips to a live WordPress 6.8 install and back.

- **M12 — Custom post types and taxonomies (read):** custom post types
  and taxonomies move from "out of scope" to a scoped milestone. M12 is the
  **read** half; the write half shipped as M17 (see "M17 — Custom type write"
  below).

  - **Config-only registry.** Types are declared in a top-level `content:`
    section of the configuration file, with `post_types:` and `taxonomies:`
    lists. Configuration declares, the database validates and informs. grimoire
    never auto-exposes a `post_type` or taxonomy merely because rows exist in
    the database, so private data under types such as `shop_order` or
    `wpcf7_contact_form` is never published. Executing plugin or theme PHP to
    discover types stays permanently out of scope.
    - Post type keys: `name` (required), `public` (default `true`; `false`
      registers the type for REST and admin read only, with no public route),
      `rest_base` (default `name`), `has_archive` (`true`, `false`, or a slug;
      default `false`), `slug` (default `name`), `with_front` (default `true`),
      and `hierarchical`.
    - Taxonomy keys: `name` (required), `object_types`, `public` (default
      `true`), `rest_base` (default `name`), `slug` (default `name`),
      `with_front` (default `true`), and `hierarchical`.
    - The config is validated at startup and grimoire refuses to start on an
      invalid entry: an empty or malformed name (lowercase alphanumerics, dash
      and underscore; at most 20 characters for post types and 32 for
      taxonomies), a built-in or duplicate name, a rewrite slug or `rest_base`
      that collides with another entry or a built-in base, or a reference to an
      unregistered taxonomy or object type. Multi-segment rewrite slugs are
      rejected. Database state is never a startup failure.
  - **Public routes (published rows only).** A custom post type single is served
    at `/{slug}/{postname}/` (nested ancestry for a hierarchical type), its
    archive at `/{archive-slug}/` when `has_archive` is set, and a custom
    taxonomy term archive at `/{slug}/{term}/` (nested when hierarchical).
    Archive pagination is `/page/{n}/` or `?page=N` (M16). Custom post type URLs do not use the
    dated permalink tokens. Custom post type date archives and feeds are out of
    scope.
  - **REST read.** `GET /wp-json/wp/v2/{rest_base}` (collection and
    single) is served for each registered post type and taxonomy, in the same
    envelope and pagination headers as posts and pages. Custom post type items
    carry a term-id array per attached taxonomy, keyed by that taxonomy's
    `rest_base`. The write verbs are covered under "M17 — Custom type write".
    `/wp/v2/types` and `/wp/v2/taxonomies` are not implemented.
    A non-published custom post row requested by id is returned only to a
    caller holding the edit capability derived from the type's
    `capability_type` (default `post`); every other caller receives `404
    rest_post_invalid_id`, as for posts and pages. No new capability names are
    introduced.
  - **Admin type validation.** The admin-API post list accepts `type` values
    that are `post`, `page`, or the name of a registered post type, and answers
    `400` with error code `invalid_type` for any other value, without reading
    the database. With no `type` the default stays `post` and `page`. The
    admin-API write paths accept `post`, `page` and registered types (M17); the
    admin SPA UI is unchanged. The admin author list includes authors of
    registered types.
  - **`migrate -check` diagnostic.** `grimoire-cli migrate -check` reports,
    read only, each distinct `post_type` in `{prefix}posts` and each taxonomy
    in `{prefix}term_taxonomy` that is neither built in nor declared, naming
    the config key to add (`content.post_types` or `content.taxonomies`). It
    also notes a declared type that has no rows. Nothing is exposed as a result
    of this report.
  - **Documented consequences.** Registering a custom base or archive slug
    takes that URL space: a page or post slugged like a registered custom
    base or archive slug becomes unreachable at that URL, because the custom
    route is classified first. Precedence is fixed in
    `routing.Structure.Classify`; see the M12 design for the full table.
- **M17 — Custom type write:** REST and admin-API create, update and delete
  for registered custom post types and taxonomies (roadmap 12.C), over the
  existing writer ports and M10a's sanitization policy. No schema change, no
  migration, no new capability string.
  - **REST verbs.** For each registered post type, `POST /wp-json/wp/v2/{rest_base}`,
    `PUT`/`PATCH /{rest_base}/{id}` and `DELETE /{rest_base}/{id}` are served;
    for each registered taxonomy the same verbs serve terms. Still `501`:
    `POST /{rest_base}/{id}` and collection-level `PUT`/`PATCH`/`DELETE`
    (and every write verb on the post-type routes when no post-write service is
    configured). Admin-API post and term writes accept a registered `type` or
    `taxonomy`; the term-create endpoint now rejects an unknown taxonomy.
  - **Capabilities.** Post writes derive from the type's `capability_type`
    (default `post`): `edit_{cap}s`, `edit_others_{cap}s`, `publish_{cap}s`,
    `edit_published_{cap}s`, `edit_private_{cap}s` and `delete_{cap}s`, so
    `edit_posts` alone does not permit writing a `capability_type: product`
    type. Unregistered stored types keep the legacy `*_posts` decision.
    Taxonomy writes use `manage_categories`, as for categories and tags.
  - **Sanitization.** Registered post types use the same tiers as posts: title
    to plain text, content and excerpt by tier B or C depending on
    `unfiltered_html`. Term names of every taxonomy, built-in `category` and
    `post_tag` included, are reduced to plain text on write (a fixed tier B, so
    `unfiltered_html` holders get no bypass), and the response reflects the
    stored (sanitized) name. A name that reduces to nothing is rejected. On
    update, a name byte-identical to the stored one is written unchanged, so an
    update that does not touch the name never rewrites a legacy name such as
    `News &amp; Events`. A changed name is reduced, and the reduction decodes
    entities, so `A &amp; B` submitted as a new name is stored as `A & B`
    (see M18 below).
  - **Slugs.** Post slugs must match `^[a-z0-9][a-z0-9_-]*$`, are derived from
    the title when absent, and get a `-2`, `-3` suffix on collision (among
    siblings for a hierarchical type). Term slugs use the same charset and are
    rejected, not suffixed, when a sibling already uses them (`term_exists`).
    Percent-encoded non-ASCII slugs that an imported database can hold are
    readable but cannot be written through the API.
  - **Parent.** A hierarchical post type accepts `parent` (REST and admin API; a
    non-hierarchical type rejects a non-zero parent), validated to exist, be of the same type, and not be the post or one of its
    descendants. A hierarchical taxonomy accepts term `parent` with the M13
    cycle prevention. Page `parent` writes arrived in M18 PR 2 (below).
  - **Term assignment.** REST post bodies for a registered type may carry
    arrays of term ids keyed by the taxonomy's `rest_base`; the admin API uses
    the `termIds` map keyed by taxonomy name. A taxonomy not attached to the
    type, or a term belonging to another taxonomy, is rejected (`400` on REST,
    a `partial` entry on the admin API).
  - **Delete.** Since M22 PR 1, `DELETE` without `?force=true` moves the row
    to the trash; with `?force=true` it is the permanent delete described
    under M18 PR 2 below (see "M22 — Trash and restore (PR 1)").
  - **Other behavior.** `public: false` types are writable: `public` governs
    the public route only. The admin API keeps the `edit_posts` floor, so a role
    holding only `edit_products` can use REST but not the admin API for that
    type. The publish scheduler also holds the derived capabilities, so a
    scheduled custom post publishes on time.

- **M18 — Write hardening (PR 1):** closes the M17 hole where stored rows of
  unmanaged types stayed writable. No schema change, no migration, no new
  capability string.
  - **Writable-type rule.** A stored row is writable only if its type is
    `post`, `page` (or an empty type, treated as `post`) or a type registered
    under `content:` in the configuration. Any other stored type, for example
    an imported `shop_order`, is unwritable for every caller, administrators
    included. Reads are unchanged: public and admin reads still follow the
    registry as before.
  - **Responses.** Admin-API update, delete and term assignment on an
    unwritable row answer `403`, including term assignment with an empty
    `termIds`. Admin-API revision list, show and restore, and autosave read and
    write, on an unwritable parent answer `404`, and nothing is written.
    Creating a row of an unmanaged type stays `400`. The REST `/wp-json/wp/v2`
    write routes are only served for registered types, so an unmanaged type has
    no REST route and answers `404`.
  - **Registry removal.** Removing a type from `content:` makes its existing
    rows unwritable at the next start. The rows and their data are untouched
    and become writable again when the type is registered again.
  - **Scheduler.** The publish scheduler skips due `future` rows of an
    unwritable type, logging at debug level. They stay `future`.
  - **Revisions and autosave.** Both now decide capability from the parent's
    registered `capability_type`, as post writes do, instead of the post and
    page `*_posts` family. A registered `capability_type: product` row is
    revisable only by a caller holding the product capabilities.
  - **Term names.** Names of all taxonomies are reduced to plain text at tier B
    on create and update, with the unchanged-name exemption described under
    M17 Sanitization. Slug rules still apply to registered taxonomies only.
- **M18 — Write hardening (PR 2):** delete cleanup and page `parent` writes.
  No schema change, no migration, no new capability string. One additive
  storage port (`PostDependentsPurger`).
  - **Delete cleanup.** Deleting a `post`, a `page` or a row of a registered
    type now runs these steps in order, and the first failure stops the
    sequence: revisions and autosaves, then term detach, then the
    remaining-relationship purge (M20 PR 2), then (for a `post` only) removal
    from `sticky_posts`, then the dependents purge, then the nav-menu item
    purge (M19 PR 3), then the row delete.
    - *Terms.* The row's `term_relationships` are detached for `category` and
      `post_tag` (built-in `post` and `page`), for any registered taxonomy
      whose `object_types` names the type, and for a registered type's own
      `taxonomies`. Term counts are recounted by the detach.
    - *Removed.* The row's `comments`, their `commentmeta`, and its `postmeta`.
      Other posts' rows are untouched.
    - *Re-pointed.* Child pages (and children of a registered hierarchical
      type) and attachments get the deleted row's own `post_parent`, so a
      child of a top-level page becomes top-level. Children of other types
      keep their `post_parent`. `post_modified` is not changed.
    - *Atomicity and retries.* The purge is one transaction. Every step before
      the final delete is idempotent, so a retry after a failure converges.
    - Delete is permanent with `?force=true` on REST and on the admin API.
      `DELETE` without `force` trashes since M22 PR 1 (REST) and M22 PR 2
      (admin API).
  - **Page `parent`.** `parent` is writable on pages over REST
    (`/wp-json/wp/v2/pages`) and the admin API. An admin update body without
    `type` is treated as the stored row's type. `post` rows ignore `parent`.
    - *Guard.* A parent that is the page itself or one of its descendants is
      rejected (cycle), as is a parent that does not exist or is not a page.
      Both answer `400` and leave the row unchanged. `parent: 0` makes the page
      top-level. An update that does not change the parent skips the check.
    - *Slug.* When a page is created under a parent or moved to a new parent,
      a slug already used by a sibling gets a `-2`, `-3` suffix. An update that
      does not change the parent keeps the caller's slug.
    - *URLs.* The page's `link` is its nested path (`/a/b/`). A published page
      requested at its old path answers `301` to the new one, a draft answers
      `404` on both paths, and same-slug pages under different parents each
      serve their own body.
  - **Known gaps (PR 2).**
    - `DELETE` trashes by default on REST (M22 PR 1) and on the admin API
      (M22 PR 2).
    - WordPress hooks and the object cache are not involved.

- **M19 — Term gate and delete parity (PR 1):** the term-side counterpart of
  the M18 writable-type rule. No schema change, no migration, no new
  capability string.
  - **Managed taxonomy.** A taxonomy is managed if it is `category`,
    `post_tag`, or registered under `taxonomies:` in the configuration. A term
    is writable only if its stored taxonomy is managed. This applies to every
    caller, administrators included. The stored taxonomy decides, not the
    taxonomy in the request body.
  - **Responses.**

    | Operation | On an unmanaged taxonomy |
    |---|---|
    | Admin-API term update | `403`, nothing written |
    | Admin-API term delete | `403`, nothing written |
    | Admin-API term create | `400` (unchanged) |
    | Term assignment (`termIds` on post create or update) | that taxonomy is reported in `partial`; the post write is kept |
    | REST term routes | `404` (unchanged; no route exists for an unregistered taxonomy) |

    A term id that does not exist still answers `404` on update and delete.
  - **Built-in assignment check.** Assigning terms to the built-in
    `category` and `post_tag` taxonomies now checks that every id is a term of
    that taxonomy, as registered types already did. An id that belongs to
    another taxonomy, or does not exist, is rejected with a term-invalid error:
    `400` on the REST post routes, an entry in `partial` on the admin API.
  - **`nav_menu` coexistence.** The menu editor (`/admin/api/menus`, gated by
    `edit_theme_options`) writes its own `nav_menu` terms and does not go
    through the term service, so it keeps working. A generic term update or
    delete of a menu term (`manage_categories`) is refused with `403`, as are
    `link_category`, `post_format` and other stored taxonomies that are not
    managed.
  - **Registry removal.** Removing a taxonomy from `taxonomies:` makes its
    existing terms unwritable at the next start. The terms and their
    relationships are untouched and become writable again when the taxonomy is
    registered again.
- **M19 — Term gate and delete parity (PR 2):** REST page `parent` and
  `sticky_posts` cleanup. No schema change, no migration, no new port, no new
  capability string.
  - **REST page `parent`.** Every REST page item (list, get, create and update
    responses) carries `parent`, including `0` for a top-level page, as
    WordPress does. Other types are unchanged: `post` items have no `parent`,
    a registered hierarchical type emits `parent` (including `0`), and a
    registered flat type omits it. The admin API page detail is unchanged and
    still omits `parent` when it is `0`. The built-in golden files gained only
    `"parent": 0` lines on page objects.
  - **`sticky_posts` on delete.** Deleting a `post` row removes its id from the
    `sticky_posts` option.
    - *Scope.* Post rows only. Deleting a page or a registered-type row never
      reads the option.
    - *Writes.* The option is written only when the id is present. A missing
      option is never created. The order of the remaining ids is kept, and an
      empty list is stored as `a:0:{}`. The list is written as a PHP array of
      integers (`php.Serialize` now accepts `[]int`).
    - *Malformed option.* A value that does not parse, is not an array, or holds
      a non-integer is left unchanged and the delete continues. Nothing is
      logged: the `content` package has no logger, so this departs from the
      design, which called for a warning.
    - *Order and failure.* The step runs after the term detach and before the
      dependents purge. A read or write error other than "not found" is returned,
      the purge and row delete do not run, and a retry converges.

- **M19 — Term gate and delete parity (PR 3):** nav-menu item cleanup on
  delete. No schema change, no migration, no capability string. One additive
  storage port (`PostMenuItemPurger`, implemented by `PostRepo`).
  - **What is deleted.** Deleting a `post`, a `page` or a row of a registered
    type deletes the `nav_menu_item` rows that point at it, with each item's
    `postmeta` and its `term_relationships`. Children of a deleted item are
    re-pointed to the item's own menu parent (M20 PR 1, below).
  - **What matches.** An item matches when its row type is `nav_menu_item`,
    `_menu_item_type` is `post_type`, `_menu_item_object` equals the deleted
    row's type and `_menu_item_object_id` equals its id. Items for taxonomy
    terms, custom links, other objects or other types are kept, as are the
    menu terms themselves and every other post.
  - **Menu count.** For each `nav_menu` term taxonomy the deleted items
    belonged to, `term_taxonomy.count` is set to the true number of
    relationships. WordPress counts only published and draft items; grimoire
    creates items as `publish`, so the two agree for items grimoire wrote. For
    imported items in other statuses the count can differ from WordPress until
    WordPress recomputes it on the next menu edit.
  - **Order.** The purge runs after the dependents purge and before the row
    delete. It is one transaction, a read or write error stops the sequence
    before the row delete, and a retry converges (a second purge finds
    nothing and changes nothing).
  - **Verified on** SQLite, MySQL 8.0 and PostgreSQL 16 by one shared contract
    (`RunPostMenuItemPurgerContract`). MySQL and PostgreSQL run when the
    `GRIMOIRE_TEST_*` DSNs are set, as CI does.
  - **Known gaps (PR 3).**
    - Trash and restore arrived in M22 PR 1 (REST) and PR 2 (admin). (The media-delete menu-item gap and the
      unmanaged-taxonomy relationship gap listed here were closed in M20 PR 2.)
    - Term-side post meta, WordPress hooks and the object cache are not involved.
- **M20 — Delete gap closure (PR 1):** child menu items and the menu count on
  delete. No schema change, no migration, no capability string.
  - **Children are re-pointed.** When a menu item is deleted, every
    `nav_menu_item` whose `_menu_item_menu_item_parent` equals the deleted id
    gets the deleted item's own menu parent. A child of a top-level item
    becomes top-level (`0`). A deleted item whose own parent value is absent,
    non-numeric, negative or its own id counts as top-level.
  - **Both paths.** The same helper runs when a page, post or registered-type
    row is deleted (the M19 PR 3 purge) and when the menu editor deletes an
    item (`NavMenuRepo.DeleteItem`).
  - **Chains.** Items are handled one at a time, in ascending id order. Each
    deletion re-points that item's children first, and the next deletion reads
    the parent value as already updated. When a whole chain (A, then B under A,
    then C under B) is deleted, a surviving child ends up under the nearest
    surviving ancestor, or at top level if none survives.
  - **Menu count.** `NavMenuRepo.DeleteItem` now recounts `term_taxonomy.count`
    of the menus the item belonged to, using the same relationship-count rule
    as the purge (true number of relationships; WordPress counts only published
    and draft items, so counts for imported items in other statuses can differ
    until WordPress recomputes them).
  - **Transaction and retries.** The re-point, the item delete and the recount
    share one transaction, so a failure leaves nothing half done. A repeat
    finds no matching children or items and changes nothing.
  - **Why grimoire does this.** WordPress core does **not** re-parent children
    on the server: deleting a menu item with `wp_delete_post` leaves the
    children's parent meta pointing at the deleted id. Only the nav-menus
    editor's JavaScript re-parents them, before saving. grimoire does it
    server-side because its menu tree builder hides items whose parent does not
    exist, so a dangling child would vanish from the rendered menu instead of
    showing at the top level.
  - **Known gaps.** The media-delete and unmanaged-taxonomy gaps were closed in
    M20 PR 2 (below). Trash and restore shipped in M22. Term-side post meta,
    WordPress hooks and the object cache remain open.
- **M20 — Delete gap closure (PR 2):** media delete cleanup and relationship
  cleanup. No schema change, no migration, no capability string. One additive
  storage port (`PostTermRelationshipPurger`, implemented by `PostRepo`) and one
  exported variable (`domain.NonPostObjectTaxonomies`).
  - **Post, page and registered-type delete: relationships.** The delete now
    removes the row's `term_relationships` in **every** taxonomy except
    `link_category`, then recounts `term_taxonomy.count` of each term it removed
    a row from. This reaches taxonomies grimoire does not manage (for example
    `product_cat`, `post_format`, or a taxonomy of an imported plugin), which the
    managed-taxonomy detach does not.
  - **Why `link_category` is excluded.** The step is a denylist, not an
    allowlist, because grimoire cannot know which stored taxonomies apply to
    posts. `link_category` relationships use `wp_links` ids as `object_id`, so
    deleting a post whose id equals a link id would delete that link's
    categories. `domain.NonPostObjectTaxonomies` is the only place that names it.
    - *Residual risk.* A plugin taxonomy whose objects are not posts (and so
      whose `object_id` is not a post id) is not on the denylist. Deleting a post
      whose id collides with such an object's id removes that object's
      relationships. Add the taxonomy to `NonPostObjectTaxonomies` if you run one.
  - **Media delete.** `MediaRepo.Delete` runs one transaction that removes, in
    order: menu items pointing at the attachment (type `post_type`, object
    `attachment`), with their postmeta and relationships, children re-pointed to
    the item's menu parent (the M20 PR 1 rule) and menu counts recomputed; the
    `_thumbnail_id` postmeta rows on other posts that reference the attachment;
    its comments and `commentmeta` (every approval state); its relationships in
    every taxonomy except `link_category`, with recounts; any `term_relationships`
    rows of the attachment whose `term_taxonomy` row no longer exists (orphans,
    nothing to recount); its postmeta; and its `posts` row.
  - **`post_type` guard.** The row delete is restricted to `post_type =
    'attachment'`. An id that is a post, a page or unknown answers not found
    (`404`) and the transaction rolls back, so none of the earlier steps persist.
  - **Honest note on WordPress.** `wp_delete_attachment` and
    `wp_delete_object_term_relationships` remove relationships only for the
    taxonomies **registered** for that object type at that moment. Rows in a
    taxonomy no plugin registers (for example a deactivated plugin's) stay in
    WordPress. grimoire removes them, because it has no registry of every stored
    taxonomy, so its result is a superset of WordPress's.
  - **Delete sequence (post, page, registered type).** Revisions, managed term
    detach, remaining-relationship purge, unstick (`post` only), dependents
    purge, menu-item purge, row delete. The first failure stops the sequence, and
    every step is idempotent, so a retry converges. With no purger configured the
    relationship step is skipped.
  - **Relationship-count rule.** Recounts set `term_taxonomy.count` to the true
    number of relationships. WordPress counts only published posts for post
    taxonomies (and published or draft menu items), so counts for imported rows in
    other statuses can differ until WordPress recomputes them.
  - **Verified on** SQLite, MySQL 8.0 and PostgreSQL 16 by shared contracts
    (`RunPostTermRelationshipPurgerContract`, `RunMediaDeleteContract`). MySQL and
    PostgreSQL run when the `GRIMOIRE_TEST_*` DSNs are set, as CI does.
  - **Known gaps (M20 PR 2).**
    - Trash and restore arrived in M22 PR 1 (REST) and PR 2 (admin) for posts,
      pages and registered types. Attachments have no trash (WordPress `MEDIA_TRASH` is false).
    - *(Closed in M23 PR 3.)* Media delete now also clears the theme mods and
      options that reference the attachment (`custom_logo` with `header_text`,
      `header_image` with `header_image_data`, `background_image`, and the
      `site_logo` and `site_icon` options); see the M23 theme-mod cleanup entry,
      including its stylesheet and URL-form limits.
    - *(Closed in M23 PR 2.)* Media delete now also removes the image-size
      files, `original_image`, backup sizes and sideload companions, guarded to
      the uploads directory; see the M23 media file cleanup entry. The
      repository itself still removes only database rows.
    - Child attachments keep their `post_parent` pointing at the deleted
      attachment; they are not re-pointed.
    - WordPress hooks and the object cache are not involved.
    - Published-only term counts (above), plugin taxonomies of non-post objects
      (above), and the `sticky_posts` read-modify-write limit are unchanged.
- **M16 — Archive pagination (PR 1):** the WordPress path form
  `/page/{n}/` beside grimoire's `?page=N`, on every paginated archive. No
  schema change, no migration, no new capability, no new option. The `?m=`
  date query is PR 2 (next section).
  - **Where it applies.** The home page (`/page/2/`), categories at any depth
    (`/category/news/page/2/`), tags, authors, year, month and day archives,
    custom post type archives and custom taxonomy terms. The base, the permalink
    front, the `date/` segment and every other archive rule are the existing
    ones; the path form only appends `page/{n}`. Flat structures (no trailing
    slash) use `/page/2`.
  - **The peel rule.** The classifier removes a trailing `page/{n}` pair, where
    `{n}` is a plain integer from 1 to 999999999 (no sign, no leading zero),
    classifies what is left with the existing rules, and accepts it only if it
    is a home, category, tag, author, date, custom archive or custom term path.
    Otherwise the whole path is classified as before. `page` is **not** a
    reserved word: `/category/page/` is still the category slugged `page`,
    `/category/page/page/2/` is page 2 of it, and `/category/page/2/` is the
    category walk `[page, 2]`. A top-level page slugged `page` still renders
    at `/page/`. `/page`, `/page/x`, `/page/0`, `/page/02` and `/page/2/3` are
    not pagination and behave as before.
  - **Shadowed shapes.** Three URL shapes that used to resolve now mean
    pagination, because WordPress's own rewrite order resolves them the same
    way: a term two levels below a parent slugged `page` whose own slug is a
    page number (`/category/news/page/2` as grandchild `2` of child `page`);
    a top-level page slugged `page` with a child slugged by a number
    (`/page/2`); and a post whose canonical path is `/page/{n}/`
    (for example `/page/2/` under `/%postname%/%post_id%/`). There is no
    fallback: when the page number is out of range the answer is `404`, and the
    old interpretation is not retried. The development real-database check
    (`TestRealWordPressPagination`, gated on `GRIMOIRE_TEST_WP_DSN`) found `0`
    of each shape.
  - **`?page=N` and `?paged=N` still work.** Both answer `200` with the
    requested page. `?page=` wins over `?paged=` when both are present. Garbage,
    `0` and negative values mean page 1. A positive integer above
    `MaxPageNumber` (999999999), including one too large for an `int64`, is
    clamped to it, so it answers `404` rather than overflowing the page offset.
  - **Canonical `Link` header.** A query-form request on an archive carries
    `Link: </category/news/page/2/>; rel="canonical"`, a relative path (escaped)
    to the path form of the page actually served. Both forms answer `200` and
    nothing redirects, because a visitor or a theme written for `?page=N`
    must keep working. The header is sent only when the query has `page` or
    `paged`.
  - **Redirects.** `/…/page/1/` answers `301` to the archive's base path.
    The other trailing-slash form of a path-form URL answers `301` to the
    canonical one. A moved category or custom term answers `301` to the new
    path **with** `/page/{n}/`. Every redirect keeps the raw query string
    byte for byte.
  - **Other statuses.** A page above the last answers `404`. An archive whose
    entity exists but has no posts answers `200` on page 2, as it does on page
    1. An unknown entity answers `404`.
  - **Themes.** The handler builds the links: `PrevURL` and `NextURL` on
    `IndexData` and `ArchiveData` (empty when there is no such page; the
    previous link of page 2 is the base path, never `/page/1/`). The default
    theme renders them. `ArchiveData.BaseURL` is unchanged, so a custom theme
    that builds `{{.BaseURL}}?page=N` keeps working.
  - **Startup notes.** A `category_base` or `tag_base` that resolves to `page`,
    or a custom post type or taxonomy whose slug (or archive slug) is `page`,
    is named in the startup `WARN` and by `grimoire-cli migrate -check`: under
    that base `/page/{n}` is home page pagination, so an entry slugged by a page
    number is unreachable. A configuration with no such base produces the same
    notes as before.
- **M16 — Archive pagination (PR 2):** the `?m=` date query on the home
  route. No schema change, no migration, no new capability, no new option.
  - **Accepted values.** `GET /?m=YYYY`, `?m=YYYYMM` and `?m=YYYYMMDD`: exactly
    4, 6 or 8 ASCII digits that name a real calendar date (leap days included,
    `20230229` is not one). The hour, minute and second forms
    (`?m=2024051712...`) are not supported and are ignored like any other
    invalid value.
  - **Served in place.** A valid `?m=` on `GET /` answers `200` with the date
    archive's posts and heading. Nothing redirects. The first `m` value wins
    (`?m=2024&m=x` is the 2024 archive). `?page=N` and `?paged=N` select the
    page, and a page past the last answers `404`.
  - **Invalid values are ignored.** `?m=garbage`, `?m=202413`, `?m=` and the
    like render the ordinary home page with `200`, byte for byte as `/` does,
    and no date-archive read happens. No URL that served before changes.
  - **Only `GET /`.** `?m=` on any other route (`/page/2/?m=202405`,
    `/category/x/?m=202405`) is ignored.
  - **Structured permalinks.** The response carries
    `Link: </2024/05/>; rel="canonical"` (relative, escaped), and its
    pagination links are the path form (`/2024/05/page/2/`).
  - **Flat (plain) permalinks.** There is no date path, so no `Link` header is
    sent and the pagination links stay on the query form
    (`/?m=202405&page=2`). `ArchiveData.BaseURL` is `/?m=202405` in this case.
    A custom theme that builds `{{.BaseURL}}?page=N` itself would produce
    `/?m=202405?page=N` under Flat; such a theme must use `PrevURL` and
    `NextURL`, which the handler builds correctly for both cases.
  - **Verification.** The development real-database check
    (`TestRealWordPressDateQuery`, gated on `GRIMOIRE_TEST_WP_DSN`) ran green
    against the dev WordPress database with 75 `?m=` strings built from the
    dates of 25 posts.
  - **Still open.** The hour, minute and second forms of `m`, `?m=` on routes
    other than `/`, feeds and sitemaps, comment pagination and
    `<!--nextpage-->`.
- **M22 — Trash and restore (PR 1):** REST `DELETE` of a post, a page or a
  registered-type row now trashes it, in the rows WordPress itself writes, and
  `PUT`/`PATCH` can trash and restore. No schema change, no migration, no new
  capability string. One additive storage port (`PostTrasher`). PR 1 ships the
  store, the service and the REST surface; the admin API and SPA are PR 2 (below), and
  `content.trash_days` and the automatic purge are PR 3.
  - **REST `DELETE` (posts, pages, registered types).** Without `force`, the
    row is trashed and the response is `200` with the post object as a `GET`
    returns it (status `trash`). With `?force=true` (or `force=1`) it is the
    permanent delete of M18-M20 and the response is
    `{"deleted": true, "previous": <object>}`, as before. `force` is read from
    the query string only. Media and users are unchanged: they have no trash
    and still require `force=true` (`400 rest_trash_not_supported`).
  - **Error codes.**
    - `404 rest_post_invalid_id`: unknown id or wrong type.
    - `403 rest_cannot_delete`: the caller may not delete the row. This is
      checked first, so a forbidden caller learns nothing about trash state.
      The code was `rest_cannot_edit` before M22 (a placeholder).
    - `501 rest_trash_not_supported`: trash is not enabled or not wired and
      `force` is absent. Nothing is deleted.
    - `410 rest_already_trashed`: the row is already in the trash and `force`
      is absent.
    - `500 rest_delete_failed`: a storage failure; one transaction, so
      nothing changed.
  - **Representation (what a trash writes).** The same rows as WordPress's
    `wp_trash_post`, so a WordPress on the same database reads the result and
    the reverse:
    - `post_status` becomes `trash`; `post_modified` and `post_modified_gmt`
      move to the trash time.
    - `postmeta`: `_wp_trash_meta_status` (the status the row had) and
      `_wp_trash_meta_time` (Unix seconds).
    - `post_name` gets the suffix `__trashed` unless it already ends with it
      (cut at 191 bytes first, never inside a `%XX` escape), and the old name
      is saved as `_wp_desired_post_slug`. An empty name becomes `__trashed`.
    - Nothing else is removed: revisions, children, term relationships, menu
      items and attachments stay. A trashed page's published child resolves at
      `/{parent}__trashed/child/` until the parent is restored, as in
      WordPress.
  - **Restore.** `PUT`/`PATCH` of a trashed row with a `status` other than
    `trash` (`publish`, `future`, `draft`, `pending` or `private`) restores it
    to that status, applying any field changes in the same request. The
    restore rule is WordPress's `wp_untrash_post`: there is no automatic
    return to the previous status, so a client that wants the old status sends
    it. The service-level default for an unspecified restore is `draft`.
    The slug returns to the saved `_wp_desired_post_slug` (or the name without
    `__trashed`) and is made unique among live rows of the same type (`-2`,
    `-3`, ...; scoped to the parent for pages and hierarchical types); a
    request that sends a different slug uses that one. The restore deletes
    `_wp_trash_meta_status`, `_wp_trash_meta_time` and
    `_wp_desired_post_slug`. A `PATCH` without `status` leaves a trashed row
    trashed. `PUT`/`PATCH` with `status: trash` on a live row applies the
    field changes and then trashes it; `POST` with `status: trash` is still
    rejected. A `PUT`/`PATCH` that trashes when trash is unavailable, or on a
    row already trashed, is not mapped to the delete codes: it answers
    `500 rest_update_failed`.
  - **Comments.** Trashing stores each comment's state as
    `_wp_trash_meta_comments_status` (a serialized `id => approved` array,
    integer keys) and sets the post's comments to `post-trashed`;
    `comment_count` is left alone. Restoring puts each comment back (a
    recorded `post-trashed` becomes `0`) and deletes the meta. A value that
    cannot be read sets the post's `post-trashed` comments to `0`. A comment
    list with no status filter no longer returns `post-trashed` comments;
    lists that name a status are unchanged. WordPress's own REST restore leaves
    `post-trashed` comments behind; grimoire's restore does not.
  - **Term counts.** One shared rule now counts the relationships whose object
    is not a trashed post, so a trash or a restore recounts every taxonomy the
    row is in and category pages stop counting trashed posts. WordPress counts
    only published posts for post taxonomies; the remaining gap (drafts count
    in grimoire) predates M22 and is unchanged.
  - **Menus.** Public menus (`NavMenuService.ByLocation` and `BySlug`) drop
    items whose target is trashed and show that item's children in its place.
    Stored menu items are untouched, so a restore brings the item back. The
    admin menu views are unchanged.
  - **Sticky.** Trashing a `post` removes its id from `sticky_posts` first
    (a malformed option is left alone). A restore does not re-stick it, as in
    WordPress.
  - **Capabilities.** A trashed row is authorized as the status it had before
    it was trashed (`_wp_trash_meta_status`), or as a draft when none is
    recorded. A Contributor therefore cannot restore or permanently delete a
    formerly published row. Trash needs the delete capability of the type;
    a restore needs the edit capability on that status and then on the new
    status (the publish check). The policy tests `publish` only, so a
    recorded `future` is treated as a draft; this predates M22.
  - **Reads.** Public routes already filter to `publish`, so a trashed row is
    `404` there (now pinned by tests). `GET` of a single trashed row answers
    `200` to a caller who may read unpublished rows and `404` to anyone else.
    Collections take `?status=` (`publish`, `future`, `draft`, `pending`,
    `private`, `trash`, `any`, or a comma list). An absent value or `publish`
    behaves as before, an unknown value is `400 rest_invalid_param`, and a
    caller who cannot read unpublished rows gets `403 rest_forbidden_status`
    for anything but `publish`. `any` excludes `trash`. The admin list and its
    counts leave trash out unless `trash` is requested.
  - **Migration note (behavior change for existing clients).** A REST client
    that used `DELETE` as a permanent delete now trashes the row instead: the
    row stays in the database, its URL stops resolving and its slug changes to
    `…__trashed`. Send `?force=true` to keep the old behavior. Since M22 PR 3,
    `content.trash_days: 0` turns trash off entirely and a non-force `DELETE`
    then answers `501 rest_trash_not_supported` (see "M22 — Trash and restore
    (PR 3)"); PR 1 and PR 2 alone had trash always on and purged nothing.
    Clients that read the delete
    response should expect the post object rather than `deleted`/`previous`
    when `force` is absent, and `403` bodies now carry `rest_cannot_delete`.
  - **Admin API.** Superseded by M22 PR 2 (below): the admin `DELETE` now
    trashes by default and the admin SPA has a Trash view.
  - **Verified on** SQLite, MySQL 8.0 and PostgreSQL 16 by shared contracts
    (the post-trash and term-count contracts); MySQL and PostgreSQL run when
    the `GRIMOIRE_TEST_*` DSNs are set, as CI does. A read-only real-database
    check (`TestM22TrashRealDBE2E`, gated on `GRIMOIRE_TEST_WP_DSN`) censuses
    trashed rows and reads each through the store and the REST mapper; the
    development database holds no trashed rows, so it passed with nothing to
    read.
  - **Known gaps (PR 1).** Attachments and users have no trash. WordPress
    hooks and the object cache are not involved. Trashing a page does not move
    its children. `trash_days` and the purge shipped in PR 3 (the admin API and
    SPA in PR 2); the gaps that remain are listed under PR 3.

- **M22 — Trash and restore (PR 2):** the admin API and the React admin now
  trash, restore and empty the trash. No schema change, no migration, no new
  capability string, no new storage port: PR 2 calls the `Trash` and `Restore`
  service methods and the permanent delete that PR 1 shipped. The one-release
  disagreement of PR 1 (REST trashed while the admin `DELETE` was permanent) is
  resolved: both surfaces trash by default. `content.trash_days` and the
  automatic purge shipped in PR 3.
  - **`DELETE /admin/api/posts/{id}`.** Without `force` the row is trashed and
    the response is `204` with no body. With `?force=true` (or `force=1`, the
    same parsing as REST) it is the permanent delete and answers `204`. Errors
    are `403` (no capability), `404` and `400` as before, plus:
    - `409 already_trashed`: the row is already in the trash and `force` is
      absent. Send `force=true` to delete it.
    - `409 trash_unsupported`: trash is not wired or not enabled. Send
      `force=true`.
  - **`POST /admin/api/posts/{id}/restore`.** Body `{"status": ...}`, optional
    (an empty body is accepted). `status` is empty or absent (restores as
    `draft`, WordPress's default), `previous` (the status recorded when the row
    was trashed, `draft` when none is recorded) or one of `publish`, `future`,
    `draft`, `pending`, `private`. The response is `200` with the post detail.
    A row that is not in the trash, or any other status value, answers `400`.
    The slug is restored by the same rule as REST (suffix removed and made
    unique among live rows of the type).
  - **Capability on restore.** The caller needs the edit capability of the
    type on the status the row had before it was trashed, and again on the
    status it is restored to (so restoring to `publish` needs the publish
    capability). It is checked before the not-trashed check, so a forbidden
    caller learns nothing about trash state.
  - **`POST /admin/api/posts/empty-trash`.** Body `{"type": "post" | "page" |
    <registered post type>}`; another value is `400`. It permanently deletes
    the type's trashed rows, oldest id first, at most 1000 per call, and
    answers `200 {"deleted": n, "skipped": m, "more": bool}`. `skipped` counts
    rows the caller may not delete; `more` is true when the type had more than
    1000 trashed rows. Any other error stops the call with `500` and leaves
    earlier deletes in place. Call it again while `more` is true and `deleted`
    is greater than zero; the React admin does this.
  - **Route availability.** The restore and empty-trash routes are registered
    only when trash is wired; without it they answer `404`. They sit behind
    the same login, CSRF and `edit_posts` floor as the other admin post writes.
  - **List and session.** `GET /admin/api/posts` accepts `status=trash` (the
    `invalid_status` message now lists it); the default list and the counts
    still leave trash out. `GET /admin/api/session` gains `trashEnabled`, true
    when trash is wired and enabled. `PUT /admin/api/posts/{id}` accepts
    `status: "trash"`, and a trashed row leaves the trash through a `PUT` with
    another status, the same as REST `PUT`/`PATCH`.
  - **React admin (posts, pages, registered types).**
    - The status filter has a `Trash` choice, and a trashed row shows a
      `trash` status badge.
    - In the Trash view each row has `Restore` (restores as a draft) and
      `Delete permanently` (asks for confirmation first).
    - In every other view, when `trashEnabled` is true, the row action is
      `Move to Trash`, which acts at once and shows a notice with `Undo`. Undo
      restores the row to the status it had (`status: "previous"`). When
      `trashEnabled` is false the action is `Delete permanently` with the
      confirmation.
    - In the Trash view a non-empty list shows `Empty Trash`, with a
      confirmation. It calls the empty-trash route again while the server
      reports `more` and the last call deleted something, so a batch that only
      skips rows cannot loop.
    - Opening a trashed row shows a read-only editor with a notice and a
      `Restore` button; the fields cannot be edited until it is restored.
  - **Golden changes.** `TestBuiltinGolden` gains nine appended entries
    (`admin trash create` through `admin empty trash`); the 58 entries that
    existed before are unchanged except one, which the design did not predict:
    `rest trash list status trash` now returns three rows instead of two
    (`X-WP-Total` 3). The admin `post delete` step earlier in the script used
    to delete its row (the post titled "Admin Post 2", id 6); it now trashes it (still `204`, no body), so the row
    appears in the trashed list. `scripts/check-golden-trash.sh` mode `pr2`
    allows exactly that one entry to differ. The same change is in both golden
    files.
  - **Known gaps (PR 2).** Attachments and users have no trash. The retention
    setting and the automatic purge shipped in PR 3 (below).
- **M22 — Trash and restore (PR 3):** the retention setting and the automatic
  purge. No schema change, no migration, no new storage table. One additive
  storage port (`TrashedPostLister`, read-only), one scheduler option
  (`WithTrashPurge`) and one config key.
  - **`content.trash_days`.** Days a trashed row is kept before the purge
    deletes it. Default `30` (WordPress's `EMPTY_TRASH_DAYS`). The environment
    variable `GRIMOIRE_CONTENT_TRASH_DAYS` overrides the file. A negative value
    or a value that is not an integer scalar in the file (`7.5`, `abc`, a
    quoted `"7"`) fails startup with an error naming `content.trash_days`. In
    the environment variable, a negative integer fails startup the same way
    (the check runs after the override), but a value that does not parse as an
    integer is silently ignored and the file value (or the default) applies.
    The shipped `configs/grimoire.*.yaml` examples (sqlite, mysql, postgres,
    podman) carry a commented `content.trash_days` block, added in task 10.2.
  - **`trash_days: 0` turns trash off.** A non-force `DELETE` answers
    `501 rest_trash_not_supported` on REST and `409 trash_unsupported` on the
    admin API. A `PUT`/`PATCH` to `status: trash` is not mapped to the
    delete codes and answers `500 rest_update_failed` on REST, as in PR 1. The purge is off. `?force=true`
    delete still works. Restoring a row that is already in the trash still works
    (the restore path checks that the trasher is wired, not the retention),
    so rows trashed earlier are not stranded. `session.trashEnabled` is false and
    the admin shows `Delete permanently`.
  - **The automatic purge.** The scheduler runs it after each publish pass.
    - *Cadence.* At most once per hour by default. Since M23 PR 1 the spacing is
      `scheduler.trash_purge_interval_seconds` (see the M23 entry below).
      The first pass after startup purges at once, so a restart purges
      without waiting an hour. It runs even when the publish lookup fails.
    - *Which rows.* Rows with `post_status = 'trash'` whose
      `_wp_trash_meta_time` is older than now minus `trash_days` days. A row
      trashed exactly at the cutoff stays. A row with no `_wp_trash_meta_time`,
      or one that is not a number, is never purged. When a row has several
      such meta rows the lowest `meta_id` is used.
    - *Which types.* Managed types only: `post`, `page` and the post types
      registered under `content:`. Unmanaged types (and attachments) are never
      purged, even when trashed in WordPress. A type removed from the
      configuration is no longer purged.
    - *Batching.* The walk pages by id, 100 rows per query, oldest id first.
    - *What a purge does.* For each eligible row it re-reads the row and skips it
      when it is no longer in the trash (it may have been restored since the
      list), then runs the same permanent-delete sequence as `?force=true`
      delete: revisions, term relationships in the managed taxonomies,
      `sticky_posts` for a `post`, dependents (comments, commentmeta, postmeta,
      child re-parenting), then the M18-M20 cleanup of nav-menu items with their
      children and menu counts and of the remaining relationships, then the
      row.
    - *Errors.* A forbidden or not-found result for a row is logged at debug
      level and skipped. Any other error is logged at error level and the walk
      continues with the next row. A purge that deleted rows logs the count at
      info level.
    - *Scheduler principal.* The scheduler's internal identity gained the four
      delete capabilities `delete_{s}`, `delete_others_{s}`, `delete_published_{s}`
      and `delete_private_{s}` for `s` in `posts`, `pages` and the capability
      suffix of every registered post type (for example `delete_products` for a
      type with `capability_type: product`). It gained nothing else, and it is
      not reachable from any HTTP route, session or Application Password.
  - **Verified** by unit tests (config, scheduler with an injected clock,
    including the cutoff boundary and a row restored between list and delete),
    an assembly test (`cmd/grimoire/assembly_trash_purge_test.go`) that runs
    the purge through the real wiring on SQLite for posts, pages and a
    registered type, and the shared lister contract (MySQL and PostgreSQL run
    when the `GRIMOIRE_TEST_*` DSNs are set, as CI does). The purge has not
    been run against a live WordPress database.
  - **Known gaps (remaining after M22).**
    - Terms have no trash (WordPress has none either); deleting a term is
      permanent. Not a gap against WordPress.
    - Media and users have no trash. WordPress does not trash media by default
      (`MEDIA_TRASH` is false) and never trashes users. Media delete requires
      `force=true` (`400 rest_trash_not_supported` without it).
    - Comments: the post-trashed state follows WordPress in storage, and the
      admin's comment moderation can already move a comment to trash and back,
      but there is no separate Trash view or empty-trash action for comments,
      and the purge does not touch comments of rows that are not purged.
    - Trash is on or off for the whole site. There is no per-type opt-out for
      custom post types.
    - The purge batch size (100) is not configurable. The cadence became
      configurable in M23 PR 1 (`scheduler.trash_purge_interval_seconds`, default
      3600, override `GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS`).
    - Retention is counted in whole 24-hour periods from the Unix time stored in
      `_wp_trash_meta_time`; there is no per-site timezone handling.
    - WordPress hooks and the object cache are not involved. Trashing a page
      does not move its children.
- **M23 — Configurable purge cadence (PR 1):** the automatic trash purge no
  longer runs on a fixed one-hour spacing.
  - **Key.** `scheduler.trash_purge_interval_seconds`, a positive integer
    number of seconds. Default `3600` when absent. Override with
    `GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS`; an override that is not an
    integer is ignored and the file value (or default) stands.
  - **Validation.** `0`, a negative integer, `7.5` and a quoted `"30"` fail
    startup with an error naming `scheduler.trash_purge_interval_seconds`, as
    does an environment override that parses to `<= 0`.
  - **Behavior.** The first scheduler pass after startup still purges at once;
    later passes purge only after the configured interval has elapsed. The
    interval is a minimum spacing, so the effective cadence is the larger of the
    interval and the scheduler tick (`scheduler.interval_seconds`). The
    default behavior is unchanged.
  - **Not changed.** The batch size (100) stays fixed. No schema change, no
    migration, no new capability, no response-shape change.
  - The shipped `configs/grimoire.*.yaml` examples carry a commented
    `scheduler.trash_purge_interval_seconds` block.

- **M23 — Media file cleanup (PR 2):** deleting an attachment now removes its
  files from disk as well as its rows. No schema change, no migration, no new
  capability, no new config key, no response-shape change.
  - **What is removed.** The main file (`_wp_attached_file`), every
    `sizes[*].file`, `original_image`, every `_wp_attachment_backup_sizes[*].file`,
    and, forward-compatibly, `source_image`, `animated_video`,
    `animated_video_poster` and `thumb` when present. Entries are resolved
    relative to the main file's directory. A malformed or empty metadata value
    contributes nothing and is not an error.
  - **Order.** The metadata and backup sizes are read before the database delete
    (that delete removes the postmeta). Files are removed only after the database
    delete succeeds, so a failed delete leaves every file in place. File cleanup
    never aborts or fails the delete.
  - **Containment.** Every path is resolved through symlinks and must stay inside
    the uploads directory; a path that escapes (`..`, an absolute path outside,
    or a symlink out) is skipped with a warning. The check lives in the new
    `internal/safepath` package, which the uploads file server
    (`internal/web/uploads.go`) now shares instead of keeping its own copy.
  - **Failure handling.** A missing file is fine. Any other unlink error is
    logged as a warning and does not fail the delete. A failed read of the
    metadata or backup sizes is logged and treated as empty (files are left, the
    main file is still removed).
  - **Shared thumb.** Like WordPress, only the `thumb` entry is checked for
    sharing: it is kept when another attachment's `_wp_attachment_metadata` names
    the same file as its `thumb`. The lookup (`ThumbSharedElsewhere`) narrows
    rows with an escaped `LIKE` and confirms each in Go with a case-sensitive
    match, so a case-insensitive MySQL collation cannot cause a false positive.
    A lookup error keeps the thumb on disk. Size files and the other named
    entries are not checked for sharing.
  - **REST.** `DELETE /wp-json/wp/v2/media/{id}` still requires `?force=true`
    (`400 rest_trash_not_supported` without it); nothing changes for clients.
  - **Verified on** SQLite by unit tests, the `BackupSizes` and
    `ThumbSharedElsewhere` storage contracts, and an assembly test
    (`cmd/grimoire/assembly_media_file_cleanup_test.go`) that runs a forced REST
    delete through `buildHandler` and checks which files remain. The contracts
    are written to also run on MySQL 8.0 and PostgreSQL 16 when the
    `GRIMOIRE_TEST_*` DSNs are set, as CI does.
  - **Not verified, stated plainly.**
    - `WithMediaLogger` is wired in `buildHandler` by convention. No test proves
      the logger reaches the service, so a dropped option would silently lose
      the warnings only; no test would fail.
    - `ThumbSharedElsewhere` is a method on the concrete `wprepo.PostMetaRepo`,
      not on the `domain.PostMetaRepository` port. `MediaWriteService` finds it
      by type assertion, so a repository without it (or a wrapper that hides it)
      silently skips the sharing check and the thumb is deleted. The
      `BackupSizes` method, by contrast, is on the port.
    - The `ThumbSharedElsewhere` contract has no subtest with filenames that
      differ only by case; the case-sensitive confirmation in Go is not covered
      by a contract case.
    - **Real-data check (run after merge).** `TestRealWordPressMediaMetadata`
      (`internal/content`, gated on `GRIMOIRE_TEST_WP_DSN`, read-only) ran over
      the 1,733 attachments of the development WordPress database. It found a
      bug: 80 attachments store a dimension as a PHP double (`d:1600;`), which
      `php.Unserialize` did not decode, so their whole metadata failed to parse
      and a delete left every size file on disk. `php.Unserialize` now decodes
      doubles (`Serialize` still rejects `float64`; no write path re-serializes
      a decoded value). After the fix 1,720 decode; the remaining 13 have a
      string whose declared length does not match its bytes, which PHP cannot
      read either, so they are skipped. No candidate contains a path separator
      or `..`. 10 attachments have backup sizes; 7 have `original_image`.
    - The cleanup has not been run against a live WordPress uploads directory.
    - A MySQL 8.0 / PostgreSQL 16 run of the new contracts is recorded in task
      6.5 of the plan; this documentation task did not re-run it.
  - **Known gaps (M23 PR 2).** *(Theme-mod references closed in M23 PR 3, below.)*
    Child attachments keep their `post_parent` pointing at the deleted
    attachment. WordPress hooks and the object cache are not involved.
    Attachments have no trash.
- **M23 — Theme-mod cleanup on media delete (PR 3):** deleting an attachment now
  also removes the theme mods and options that point at it, as WordPress does.
  No schema change, no migration, no new capability, no new config key, no
  response-shape change.
  - **What is cleaned.** The rules follow core's `_delete_attachment_theme_mod`
    and the site-icon handling, and nothing else is touched. In the
    `theme_mods_{stylesheet}` option (a top-level key only):
    - `custom_logo` equal to the attachment id (an integer or a numeric string)
      removes `custom_logo` and `header_text`.
    - `header_image` equal to the attachment URL removes `header_image` and
      `header_image_data`.
    - `background_image` equal to the attachment URL removes `background_image`.
    - URL equality compares paths: each side is reduced to its path (a leading
      lower-case `http://` or `https://` plus host and optional port is
      stripped) and the paths must be byte-equal, so
      `https://example.com/wp-content/uploads/a.jpg` matches
      `/wp-content/uploads/a.jpg`. The path is not case-folded or
      percent-decoded. A value or URL with a query or fragment, a
      protocol-relative `//host/...` value, any other scheme, or a non-URL
      never matches.
    - An empty value or an empty URL matches nothing, and an
      object-valued `custom_logo` or `header_image` never matches. For a
      duplicated key the last entry wins, as in PHP's `unserialize`.
    - Separately, the `site_logo` and `site_icon` options are deleted when their
      value is the attachment id (a plain decimal or a serialized integer such as
      `i:7;`). A `site_logo` or `site_icon` key inside the theme mods, and a
      `custom_logo` or `header_image` option, are not touched (a storage contract
      case pins this).
  - **Byte-preserving edit.** The serialized value is not decoded and
    re-encoded. A decode and re-encode is lossy for what real WordPress writes:
    integer-keyed arrays, floats and `stdClass` objects (such as
    `header_image_data`) do not round-trip. Instead the top-level entries are
    located by byte span, the scalar values the rules need are read, and only the
    removed key/value spans and the `a:N:` count change. Every other byte,
    including NUL-delimited object property names, is preserved.
  - **Skipped without error.** An absent `theme_mods_{stylesheet}` row, a value
    that is not exactly one well-formed serialized array, and an absent or
    non-integer `site_logo` or `site_icon` are left alone and are not errors. A
    second call after a match is a no-op.
  - **Empty result.** When every top-level key is removed, the row is deleted
    rather than rewritten as `a:0:{}`. A row that was already an empty array and
    matches nothing is left as it is.
  - **Row lookup.** The row is found by exact equality on `option_name` through a
    bound parameter (no `LIKE`, so `%` and `_` in a stylesheet are literal) and
    every write targets the row's `option_id`. MySQL's default collation compares
    `option_name` case-insensitively, so the query can return a row whose name
    differs in case; those rows are filtered out in Go, and the update and delete
    use `option_id`, so another theme's row cannot be edited.
  - **Write path and NUL bytes.** The edited value is written through
    `database/sql` with a bound argument, not through Bun's query builder. Bun
    inlines string arguments into the SQL text and drops NUL bytes, which PHP uses
    in protected and private property names. PostgreSQL text columns cannot store
    NUL, so the NUL-byte fixture runs on SQLite and MySQL only.
  - **Stylesheet.** The row looked up is `theme_mods_` plus `cfg.Theme`, the same
    convention as `NavMenuRepo.MenuByLocation`. The row is found only when
    grimoire's `theme:` setting equals the WordPress stylesheet slug (the
    directory name of the active theme, or the child theme's). When it does not,
    the mods row is silently not found and left alone, while `site_logo` and
    `site_icon` are still cleaned. An empty stylesheet skips theme mods.
  - **Ordering and failure.** The cleanup runs after the database delete
    succeeds, in its own transaction. An error is logged as a warning and does not
    fail the delete or change its response. With no cleaner wired, nothing runs.
  - **Verified on** SQLite by the shared contract
    (`internal/storage/storagetest/thememod_contract.go`), the decision-logic unit
    tests, the service tests, and an assembly test
    (`cmd/grimoire/assembly_media_thememod_cleanup_test.go`) that runs a forced
    REST delete through `buildHandler` and checks the options. The contract is
    written to also run on MySQL 8.0 and PostgreSQL 16 when the `GRIMOIRE_TEST_*`
    DSNs are set, as CI does; the cross-vendor run is recorded in the plan's gate
    task 10.6, and this documentation task did not re-run it.
  - **Not verified, stated plainly.**
    - **URL form.** The attachment URL grimoire builds is relative (the uploads
      base `/wp-content/uploads` plus the file path), while WordPress stores the
      absolute URL from `wp_get_attachment_url` in `header_image` and
      `background_image`. Both sides are therefore reduced to their path
      (scheme and authority stripped, path kept byte for byte) before comparing;
      see the matching rule above. The host is ignored, so on a database shared
      by several sites with the same uploads path a deleted attachment can clear
      a mod that names another site's identical path. Tested with absolute
      http and https values, ports, relative values and the negative cases; not
      tested against a real WordPress-written `theme_mods` row.
    - **Cleaner error path.** The service unit tests cover a failing cleaner, but
      no assembly-level test makes the real cleaner fail and observes that the
      REST delete still answers 200 with a logged warning.
    - **Background mod variants.** Only `background_image` is handled. Other
      custom-background keys (for example `background_image_thumb`) are not
      cleared, and this entry does not claim they match core's behavior.
    - **Real-data check (run after merge).** `TestRealWordPressThemeMods`
      (`internal/storage/wprepo`, gated on `GRIMOIRE_TEST_WP_DSN`, read-only)
      ran over the 11 `theme_mods_*` rows of the development WordPress
      database. Nine scan cleanly, including top-level integer keys
      (`i:0;b:0;`) and a slash in `theme_mods_pub/minileven`. Two
      (`theme_mods_decode`, `theme_mods_the-bootstrap`) hold the real
      `O:8:"stdClass"` `header_image_data`, but their `header_image` strings
      declare the wrong length (a host search-and-replace from
      `wordpress.com` left `s:67:` on a 65-byte URL), so PHP cannot read them
      either and the cleaner correctly leaves them byte-identical. On copies
      with the lengths repaired in memory, the header cleanup removed exactly
      `header_image` and `header_image_data`, kept the object-valued
      neighbours, and left the value well-formed. The non-URL sentinel
      `remove-header` and an empty `background_image` never match. The site
      has no `site_logo` option and `site_icon` is `0`. Real attachment URLs
      are absolute (`https://accuweaver.com/wp-content/uploads/...`), which
      the path match handles.
    - **Case-differing subtest.** The MySQL case-differing-stylesheet contract
      subtest was not observed failing against the earlier implementation, so it
      is not proven to catch that regression.
  - **Still open after M23.** Child attachments keep their `post_parent` pointing
    at the deleted attachment; the REST media `DELETE` without `force=true`
    still answers `400` where WordPress answers `501`; WordPress hooks and the
    object cache are not involved; the purge batch size (100) is fixed; attachments
    have no trash.

## Public read guarantees

- The **public** rendering path (unauthenticated `GET` routes) filters to
  `post_status='publish'`, so drafts, private, trashed, and auto-draft rows
  (including WordPress's zero-date `0000-00-00` non-published rows) are
  never rendered to anonymous visitors.
- Write access is gated behind authentication: the admin UI and REST write
  endpoints require a valid session or Application Password, and role-aware
  access control (from M2) governs which authenticated actions are allowed.
- `grimoire-cli migrate`/`seed` remain opt-in operator commands, not part of
  serving.

## Trusted-content boundary

As of M10a, **every value written through grimoire from M10a onward is sanitized
at the writer's tier.** This is the precise guarantee — it is explicitly *not* a
claim that stored content is safe. A capability-aware allow-list policy
(`internal/sanitize`, built on `bluemonday` with the `golang.org/x/net/html`
parser) runs at the write boundary, above storage, in front of `post_content`,
`post_excerpt`, `post_title` and comment content. It mirrors WordPress's two
`kses` lists as captured from **WordPress 7.1** (`$allowedtags` = 14 elements,
`$allowedposttags` = 124 elements, `wp_allowed_protocols` = 22 schemes; see
`internal/sanitize/testdata/parity/provenance.json`).

### The three tiers

A writer's capability selects the tier; authentication status does not. The field
kind selects the tight/broad list below tier C.

| Tier | Oracle | Applies to | Allows | Granted to |
|---|---|---|---|---|
| **A** | `wp_kses($in, $allowedtags)` | comment content | WordPress's tight `$allowedtags` — 14 inline elements, 4 attribute grants; no `img`, no `class`, no `style` | every writer **without** `unfiltered_html`, authenticated or not |
| **B** | `wp_kses_post($in)` | `post_content`, `post_excerpt` | the `$allowedposttags`-equivalent broad list incl. `img`, `class`, a 117-property `style` allow-list; no `iframe`/`form`/`input`/`on*` | writers without `unfiltered_html` (subscriber, contributor, author) |
| **C** | identity (no filtering) | any field | everything, byte-identical | `unfiltered_html` holders: editor, administrator, and any custom role or explicit grant carrying it |

**Granting `unfiltered_html` grants the ability to store arbitrary HTML,
including script.** That is the capability's meaning in WordPress and here: a
tier-C writer's content is persisted byte-identically, unfiltered, so the decision
of which roles hold `unfiltered_html` is the decision of who may store script.

### `post_title` is plain text

`post_title` is reduced to plain text (`internal/sanitize/title.go`), a deliberate
grimoire decision with its rationale — not a parity gap awaiting a fix.

Stated at its **true width**: WordPress registers
`add_filter('title_save_pre', 'wp_filter_kses')`, which applies the **tight
`$allowedtags` comment list (14 inline elements), not the broad `$allowedposttags`
(124)**. So a WordPress title carries 14 inline elements; grimoire stores none.
Grimoire is not diverging from a 124-element post list it never had.

Two losses follow:

- **`<`/`>` in literal title text** — a title whose text contains `<` or `>`
  (e.g. `5 < 6`) loses those characters. This is **documented behavior, not a
  grimoire-specific divergence**: WordPress loses them too —
  `wp_kses_post("5 < 6 & 7 > 2")` returns `5  2`, because `kses` eats `< 6 & 7 >`
  as a bogus tag.
- **Entity sequences** — because the title path iterates to a fixed point, a
  literal HTML entity sequence in a title's text is also lost
  (`&amp;lt;b&amp;gt;` decodes across passes to `b`). This loss **is
  grimoire-specific** (the fixed-point title path) and is recorded as such.

### Divergences from WordPress `kses` (D1–D15)

Fifteen behaviors the chosen library cannot express exactly as WordPress's
`kses` does. Most are in the **stricter** direction (grimoire removes more than
WordPress would); D7 and D9 are cosmetic fixed-point normalizations, and D8 and
D15 are no divergence at all (grimoire and WordPress agree). Each row names what
grimoire does versus what WordPress does; the set is pinned by the parity fixtures
(`internal/sanitize/testdata/parity/fixtures.json`).

| # | Input | WordPress `kses` | grimoire | Direction |
|---|---|---|---|---|
| D1 | `<script>alert(1)</script>` | `alert(1)` — tags stripped, text kept | `` — content skipped | stricter |
| D2 | `<style>.x{color:red}</style>` | `.x{color:red}` | `` | stricter |
| D3 | `<a href="javascript:alert(1)">x</a>` | `<a href="alert(1)">x</a>` — protocol prefix stripped, remainder kept as a relative URL | `<a>x</a>` — attribute dropped | stricter |
| D4 | `<img src="a.png" longdesc="javascript:alert(1)">` | keeps `longdesc="alert(1)"` | attribute not in tier B at all | stricter |
| D5 | `<video poster="javascript:alert(1)" src="v.mp4">` | keeps `poster="alert(1)"` | attribute not in tier B | stricter |
| D6 | `<object data="…">` | `<object></object>` | element not in tier B | stricter |
| D7 | `<A HREF="http://x/" TITLE="t">y</A>` | `<A href="http://x/" title="t">y</A>` — element name case preserved | `<a href="http://x/" title="t">y</a>` — lowercased by the tokenizer | cosmetic; a fixed point either way |
| D8 | `<a>text</a>` (no attributes) | `<a>text</a>` | `<a>text</a>` — kept (construction-time fix: `AllowNoAttrs` on every element) | none (fixed in construction) |
| D9 | `<a href="/x?a=%41&b">y</a>` | value preserved verbatim | percent-encoding normalized and query keys re-escaped | cosmetic; idempotent |
| D10 | `background-image: url(/rel.png)` | kept | dropped (http/https absolute only) | stricter, user-visible |
| D11 | `style="aspect-ratio:16/9"` and 13 other properties | kept | dropped | stricter |
| D12 | MathML markup | kept (7.1) | dropped | stricter |
| D13 | `<title>a</title>`, `<textarea>a</textarea>` in post content | kept | dropped | stricter |
| D14 | `<!-- wp:paragraph -->` in comment content | kept | dropped at tier A | stricter |
| D15 | non-UTF-8 bytes at tier A/B | `kses` is byte-oriented and largely passes them | passed through byte-identically (D15 fixture: input `61fffe62` → grimoire `61fffe62`, divergence `none`) | none — grimoire and WordPress agree |

### Known limitation: pre-M10a post content (not drained)

The write policy **closes the inflow; it does not drain the pool.** This milestone
performs **no backfill**: no migration, no startup scan, no write-back of
sanitized content over stored rows (and no provenance flag recording when a value
was sanitized).

Scoped to post fields: a site whose pre-M10a authored *post content* or *post
excerpt*, or whose imported WordPress post rows, already contain stored markup are
**rendered verbatim** through the existing `template.HTML` casts
(`internal/web/view.go`). Those two post casts carry pre-M10a and imported content
as-is; they are the casts a future reader must weigh when deciding whether a post
backstop is warranted.

### Comment render backstop (tier A, unconditional)

Comments are the exception: *comment content* is sanitized at **tier A on render**
as well as on write, **unconditionally** — independent of the rendering request's
principal and of the tier the stored value was written at. This is a second
application of the **same** tier-A list, not a second policy or a laxer pass.

So the pre-M10a framing above does **not** apply to comments: a comment stored
before M10a containing `<script>` renders **inert** rather than as live markup —
the stored row is still not repaired (nothing is written back), but it can no
longer reach a reader as live script.

The backstop is a **no-op for two populations**, which bounds its blast radius —
WordPress filters comment content with the **same** tight `$allowedtags` list tier
A mirrors:

- **Imported WordPress comments** were already `kses`-filtered on the way in, so
  they are already tier-A-shaped and pass through **unchanged**.
- **Post-M10a comments** were already tier-A-sanitized on write, so property P1's
  idempotence makes the render-time pass a **no-op** for them.
- The only rows the backstop alters are **pre-M10a grimoire-authored** comments —
  exactly the exposure it closes. (A comment written by an `unfiltered_html`
  holder is still stored byte-identically, but is rendered through tier A like
  every other comment, so tier C buys no additional markup in a rendered comment
  body.)

### Comment double-escape fix (user-visible)

A deliberate user-visible defect fix, not a regression: comments authored with
allow-listed markup now **render as markup instead of doubly-escaped entities**.
Previously comment content was escaped twice — once at the render layer and again
by auto-escaping — so a commenter who typed `<b>Hello</b>` saw the literal
characters. Leaving the escape in place after sanitization would make the
allow-list pointless.

### REST `content.rendered` asymmetry

The public page renders allow-listed comment markup, but the REST API's
`/wp-json/wp/v2/comments` `content.rendered` field keeps its `html.EscapeString`
(`internal/content/rest.go`), **unchanged and deliberately**. REST
`content.rendered` fidelity is roadmap group 10.D, out of scope here; changing it
would widen a REST response contract this milestone has not specified. The
resulting asymmetry is a scoped deferral, not an inconsistency nobody noticed.

### REST write routes (M10a added none; M10.B/M10.C enabled them)

M10a itself enabled **no new REST write route** — it was the capability-aware
write-boundary content policy (roadmap group 10.A) and nothing else. The REST
media and user write routes were enabled afterward: **M10.B** replaced the
`501` stubs in `internal/web/rest_media.go` with real `POST /wp-json/wp/v2/media`
(binary create) and `PUT`/`PATCH`/`POST /media/{id}` (metadata update) handlers,
and **M10.C** did the same in `internal/web/rest_users.go` for `POST
/wp-json/wp/v2/users` (create) and `PUT`/`PATCH`/`POST /users/{id}` (profile/
password/role update). Both pass every caller-supplied field through M10a's
policy. Only the **`DELETE`** verb for media and users still answers `501`
(`restNotImplemented`); REST delete is the one write verb group 10 left
deferred.

## Excerpts (`the_excerpt`)

List views (home/index, category, archive) render a post summary matching
WordPress `the_excerpt()`:

- **Manual excerpt** — a non-empty `post_excerpt` is rendered **as HTML**, not
  escaped, so a stored `<p>…</p>` shows as a real paragraph (WordPress runs manual
  excerpts through `wpautop`).
- **Auto excerpt** — an empty `post_excerpt` is generated from `post_content` the
  way `wp_trim_excerpt` does: strip Gutenberg block-delimiter comments
  (`<!-- wp:… -->` / `<!-- /wp:… -->`), strip shortcodes and HTML tags, collapse
  whitespace, trim to ~55 words, and append `…` when truncated. The plain-text
  result is wrapped in a single `<p>…</p>` (minimal `wpautop`). HTML entities are
  left **encoded** (matching `wp_trim_excerpt`, which does not html-decode): the
  excerpt is emitted raw, so stored `&lt;script&gt;` renders as literal text, not
  live markup.
- Empty `post_excerpt` **and** empty `post_content` render nothing (no stray
  `<p></p>`).

Derivation lives in `internal/content.Excerpt`; the `template.HTML` cast is
applied once, at the web trust boundary.

## Verifying against a real WordPress database

This is a manual, environment-specific check (requires an exported WP MySQL DB):

```bash
export GRIMOIRE_DATABASE_VENDOR=mysql
export GRIMOIRE_DATABASE_DSN='wpuser:wppass@tcp(127.0.0.1:3306)/wordpress?parseTime=true&charset=utf8mb4'
export GRIMOIRE_DATABASE_TABLE_PREFIX=wp_        # match the target site's prefix

# Do NOT run migrate/seed against a real site — just serve it read-only:
go run ./cmd/grimoire -config configs/grimoire.mysql.yaml
```

Then confirm in a browser / with `curl`:

- `GET /` lists recent published posts, newest first.
- `GET /<canonical-permalink>` renders single post / page HTML, where the
  canonical path is whatever the site's own `permalink_structure` describes
  (`/2024/05/17/hello-world/` for the "Day and name" preset, `/hello-world`
  when the structure is plain). The startup log line names the resolved
  structure, and `grimoire-cli migrate -check` reports it without starting
  the server.
- `GET /<post-slug>` on a site with a non-plain structure returns `301` to
  that canonical path rather than rendering, matching WordPress.
- `GET /<category-base>/<ancestry>/<slug>` renders that category's published
  posts, including those filed under its descendants. On a nested category
  the flat `/<category-base>/<slug>` form `301`s to that path instead of
  rendering; on a top-level one the two are the same URL. `migrate -check`
  names the resolved category and tag bases without starting the server.
- `GET /<tag-base>/<slug>` and `GET /author/<user_nicename>` render the tag
  and author archives; `GET /2024/05` (in whichever trailing-slash form the
  structure implies) renders that month's posts, on any site whose
  `permalink_structure` is not plain.
- Draft/private URLs return `404`, and the source database is unchanged.

**Result:** With `parseTime=true` and a matching `table_prefix`, grimoire renders
an unmodified WordPress content database read-only. Publishing state, taxonomy
relationships, and site options resolve identically across SQLite, MySQL, and
PostgreSQL (proven by the cross-vendor contract suite in
`internal/storage/storagetest`).

## See also

[`./wordpress-compatibility-tour.md`](./wordpress-compatibility-tour.md) —
a visual, side-by-side comparison of WordPress and grimoire against the same
content.
