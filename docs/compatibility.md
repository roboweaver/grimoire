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
- Post content is emitted as trusted `template.HTML` (matching WordPress's stored
  HTML); this is safe only because M1 reads a trusted database and never accepts
  user input.

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
