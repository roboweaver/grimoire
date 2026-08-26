# WordPress Compatibility (M1-M7)

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
| `postmeta` | read and written — featured-image and attachment metadata, and other post/page meta |
| `options` | `blogname`, `blogdescription`, … site settings |
| `terms` | category names + slugs |
| `term_taxonomy` | taxonomy rows (`category`) + counts |
| `term_relationships` | post ↔ taxonomy links |
| `users` | full WordPress-compatible auth: `user_pass`, `user_email`, and related columns are read and checked for login, not just displayed |
| `usermeta` | user metadata (schema present, WordPress-compatible) |
| `comments` | comment storage and moderation workflow (read + write) |
| `commentmeta` | comment metadata (read + write) |

grimoire also has its own native `sessions` table (not part of the WordPress
schema) for its own session/CSRF-token management; it is never read from or
written to an existing WordPress database.

Type mappings from the WordPress MySQL schema are translated per vendor
(`BIGINT(20) UNSIGNED`→`BIGSERIAL`/`INTEGER`, `LONGTEXT`→`TEXT`,
`DATETIME`→`TIMESTAMP`/ISO-8601 `TEXT`, prefix-length keys→plain indexes). See
`internal/storage/migrations/<vendor>/0001_init.up.sql`.

## What's implemented (M1-M7)

- **M1 — Content core:** switchable database vendor (MySQL/PostgreSQL/
  SQLite), WordPress-compatible schema, public read rendering of posts,
  pages, categories, and archives.
- **M2 — Users, auth, roles:** WordPress-compatible user accounts, sessions,
  and role-aware access control.
- **M3 — Embedded admin:** the Adobe React Spectrum admin shell for managing
  site content.
- **M4 — Comments, media, menus:** comment workflows, media handling
  (reading rows from the database; see `media.uploads_dir` in the README),
  and navigation menus.
- **M5 — Extensions and REST API:** the compiled `pkg/extensions` hook
  registry and a WordPress-compatible REST API at `/wp-json/wp/v2/*` — read
  parity for posts/pages/comments/media/users, plus write endpoints for
  comments, posts/pages, and categories/tags. Media and user writes still
  return `501`. Non-anonymous REST auth uses WordPress Application
  Passwords.
- **M6 — Admin CRUD editor:** full content editing, status transitions, and
  optimistic concurrency in the admin UI.
- **M7 — Revisions and scheduler:** revision history, autosave, and
  scheduled publishing.

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
- `GET /<post-slug>` and `GET /<page-slug>` render single post / page HTML.
- `GET /category/<slug>` renders that category's published posts.
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
