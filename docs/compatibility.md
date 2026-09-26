# WordPress Compatibility (M1-M9b)

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
  comments, posts/pages, and categories/tags. The REST media and user
  endpoints specifically still return `501` for every write verb — that
  501 is scoped to the REST API surface only; media uploads and user
  creation both have working, authorized write paths elsewhere (M4's admin
  UI for media, and `create_users`-gated calls / CLI bootstrap for users).
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

  - **Pages follow the post structure, where WordPress exempts them.** The
    configured structure is applied to `page` rows as well as `post` rows,
    so with a dated structure a page's canonical grimoire URL is
    `/2024/05/17/about/` and `/about` `301`s to it — whereas WordPress
    serves pages at `/about` regardless of `permalink_structure` (and
    nests them under their parent page). This is the one place M9a's
    canonical URL can differ from the URL the site published.
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
  - **Pagination is M8's contract, unchanged.** Every archive returns the
    same `Page`/`PerPage`/`Total`/`TotalPages` shape, selected by `?page=N`
    with the same page size as the home page. An archive whose entity
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
    The `?m=` query-argument form is not implemented either; grimoire has
    no query-argument route.

  Divergent or still open after M9b:

  - **Pagination is `?page=N`, where WordPress uses `/page/{n}/`.** This is
    deliberate rather than pending: `/category/news/page/2` is
    indistinguishable from a child category slugged `page`, so adopting
    WordPress's path form would mean reserving `page` as a category slug.
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
  - Custom taxonomies beyond `category` and `post_tag`, custom post types,
    the `%category%`/`%author%` permalink tokens, plugin-registered rewrite
    rules, hierarchical *page* URLs and term-hierarchy *editing* remain out
    of scope, unchanged by this milestone. Editing a term's parent is still
    not possible: M9b adds a read path.

  Validated read-only against the reference WordPress database (the
  `accuweaverllc/scripts` podman stack, MySQL 8.0, `permalink_structure`
  `/%year%/%monthnum%/%day%/%postname%/`, `category_base` empty): **33
  category terms, 28 of them nested**, every one of the 33 derived paths
  round-tripping back through the classifier to the same segments and the
  same trailing-slash form, and five nested archives checked end to end
  over real HTTP — canonical path `200`, flat form `301` to it, query
  string preserved.

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

`post_content` is emitted verbatim as `template.HTML` (`internal/web/view.go`,
`postView`), bypassing `html/template` auto-escaping — matching WordPress, which
stores already-rendered post HTML.

The list-view **`Excerpt`** is emitted the same way (also `template.HTML`, cast in
`postView`). A manual `post_excerpt` is trusted WP HTML; an empty one is
auto-derived by `internal/content.Excerpt`, which strips tags, shortcodes and
Gutenberg block comments before wrapping the plain text — so the auto path emits
no untrusted markup. Both fields therefore sit inside the same trust boundary.

This was safe by construction in M1/M2, when grimoire only read a *trusted*
WordPress database whose content was authored and sanitized upstream by
WordPress and accepted no user input on the serving path.

As of M5-M7, write paths exist: the REST API accepts writes for posts/pages,
comments, and categories/tags, and the admin UI supports full content CRUD.
Content submitted through these paths is rendered through the same
`template.HTML` cast described above, with no additional HTML sanitization
at the render layer today. Operators exposing these write paths to
less-trusted users (in particular the public comment-submission endpoint)
should evaluate their own moderation/sanitization needs — the same
operator-trust assumption WordPress itself relies on. The cast sites still
carry a `TRUST BOUNDARY` comment; adding sanitization (e.g. `bluemonday`) at
those sites remains the recommended hardening path if this assumption does
not hold for a given deployment.

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
