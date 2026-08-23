# WordPress Compatibility (M1)

grimoire replicates the WordPress **database schema and data model** — not its
GPL PHP source — so it can read and render an existing WordPress site's content.
M1 is strictly **read-only**: grimoire issues only `SELECT` statements against the
content tables and never writes to or migrates a database it did not create.

## Tables replicated in M1

With the configured `table_prefix` (default `wp_`):

| Table | Used for |
|-------|----------|
| `posts` | posts + pages (`post_type` in `post`,`page`; `post_status='publish'`) |
| `postmeta` | reserved (schema present; not read in M1) |
| `options` | `blogname`, `blogdescription`, … site settings |
| `terms` | category names + slugs |
| `term_taxonomy` | taxonomy rows (`category`) + counts |
| `term_relationships` | post ↔ taxonomy links |
| `users` | author display (schema present) |

Type mappings from the WordPress MySQL schema are translated per vendor
(`BIGINT(20) UNSIGNED`→`BIGSERIAL`/`INTEGER`, `LONGTEXT`→`TEXT`,
`DATETIME`→`TIMESTAMP`/ISO-8601 `TEXT`, prefix-length keys→plain indexes). See
`internal/storage/migrations/<vendor>/0001_init.up.sql`.

## Read-only guarantees

- Repository SQL filters to `post_status='publish'`, so drafts, private, trashed,
  and auto-draft rows (including WordPress's zero-date `0000-00-00` non-published
  rows) are never rendered.
- No `INSERT`/`UPDATE`/`DELETE` is issued against content tables by the server.
  `grimoire-cli migrate`/`seed` are opt-in operator commands, not part of serving.

## Trusted-content boundary

`post_content` is emitted verbatim as `template.HTML` (`internal/web/view.go`,
`postView`), bypassing `html/template` auto-escaping — matching WordPress, which
stores already-rendered post HTML.

The list-view **`Excerpt`** is emitted the same way (also `template.HTML`, cast in
`postView`). A manual `post_excerpt` is trusted WP HTML; an empty one is
auto-derived by `internal/content.Excerpt`, which strips tags, shortcodes and
Gutenberg block comments before wrapping the plain text — so the auto path emits
no untrusted markup. Both fields therefore sit inside the same trust boundary.

This is safe **only** because M1/M2 are read-only readers of a *trusted* WordPress
database whose content was authored and sanitized upstream by WordPress. grimoire
accepts no user input on the serving path.

The moment that assumption changes — a future write/admin path, comment
ingestion, or importing content from an untrusted source — `post_content`,
`post_excerpt`, and any other stored HTML MUST be sanitized (e.g. with
`bluemonday`) before they are cast to `template.HTML`. The cast sites carry a
matching `TRUST BOUNDARY` comment so they are not copied blindly into a write
path.

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
