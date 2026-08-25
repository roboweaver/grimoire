# grimoire

**A Go-native, WordPress-schema-compatible CMS — no PHP, a swappable database backend, and (soon) an Adobe React Spectrum admin.**

> ## What's a grimoire?
> A *grimoire* is a wizard's book of spells and knowledge — a single authoritative
> tome that stores what you know and the incantations to bring it to life.
>
> **grimoire the CMS is exactly that for the web:** one Go binary that holds your
> content and the templates (*incantations*) that render it into a living site —
> no PHP runtime required, and a database layer you can swap like changing the ink.

---

## Goals

- **No PHP.** Pure Go. Ships as a single static binary.
- **WordPress-compatible schema.** Replicates the WordPress data model
  (`wp_posts`, `wp_options`, `wp_terms`, taxonomy join tables, …) so grimoire can
  read and render an existing WordPress database.
- **Switchable database vendor.** Repository interfaces with per-dialect adapters.
  MVP targets **MySQL/MariaDB**, **PostgreSQL**, and **SQLite** — adding a new
  vendor means adding one adapter package, nothing else.
- **SEO-friendly public rendering.** Go `html/template` implementing a subset of
  the WordPress template hierarchy.
- **Adobe React Spectrum admin** (later milestone) as a client-side SPA.

## Architecture (ports & adapters)

The database vendor and the render target are both **swappable adapters** around a
stable domain core.

```
web (net/http + chi) → content services → domain (entities + repository ports)
                                                    ↑
                         storage adapters: mysql | postgres | sqlite
```

- `internal/domain` owns entities and **repository interfaces** (ports).
- `internal/storage/<vendor>` provides adapters; vendor SQL never leaks upward.
- `internal/render` turns domain data into HTML via themes (folders of templates).

See [`plans/`](./plans) for the full, Kiro-format specifications.

## Repository layout

| Path | Purpose |
|------|---------|
| `cmd/grimoire` | HTTP server entrypoint |
| `cmd/grimoire-cli` | migrations, seeding, admin bootstrap |
| `internal/domain` | entities + repository interfaces (ports) |
| `internal/content` | Post / Term / Option services |
| `internal/storage/{mysql,postgres,sqlite}` | database adapters |
| `internal/storage/migrations/<vendor>` | per-dialect migrations |
| `internal/web` | router, handlers, middleware |
| `internal/render` | template engine + WP template hierarchy |
| `themes/default` | default theme |
| `web/admin` | (M3) React Spectrum SPA |
| `plans/` | Kiro specs (requirements / design / tasks) |

## Running (M1)

grimoire ships two binaries: `grimoire` (the HTTP server) and `grimoire-cli`
(`migrate` / `seed`). Configuration is a YAML file (see [`configs/`](./configs));
every field can be overridden by an environment variable
(`GRIMOIRE_SERVER_ADDR`, `GRIMOIRE_THEME`, `GRIMOIRE_DATABASE_VENDOR`,
`GRIMOIRE_DATABASE_DSN`, `GRIMOIRE_DATABASE_TABLE_PREFIX`).

### SQLite (zero external services)

```bash
make migrate   # apply embedded schema to grimoire.db
make seed      # insert sample posts, a page, a category, options
make run       # serve on :8080
# then browse http://localhost:8080/  ·  /about  ·  /category/news
```

Or without make:

```bash
go run ./cmd/grimoire-cli migrate -config configs/grimoire.sqlite.yaml
go run ./cmd/grimoire-cli seed    -config configs/grimoire.sqlite.yaml
go run ./cmd/grimoire            -config configs/grimoire.sqlite.yaml
```

### MySQL / MariaDB

Edit `configs/grimoire.mysql.yaml` (or set `GRIMOIRE_DATABASE_DSN`). The DSN
**must** include `parseTime=true` so `DATETIME` columns scan into `time.Time`:

```
user:pass@tcp(127.0.0.1:3306)/wordpress?parseTime=true&charset=utf8mb4
```

```bash
go run ./cmd/grimoire-cli migrate -config configs/grimoire.mysql.yaml
go run ./cmd/grimoire-cli seed    -config configs/grimoire.mysql.yaml
go run ./cmd/grimoire            -config configs/grimoire.mysql.yaml
```

To read an **existing** WordPress database, skip `migrate`/`seed`, point the DSN
at it, and set `table_prefix` to match the site (default `wp_`).

grimoire reads media rows from the database but never copies the underlying
upload files, so you must also set `media.uploads_dir` to the site's real
`wp-content/uploads` directory on disk. It defaults to the relative path
`wp-content/uploads` (resolved against grimoire's own working directory),
which will not exist for an external WordPress install — leaving it unset
causes `/wp-content/uploads/*` requests (and Media admin thumbnails) to 404
even though the database rows resolve fine.

### PostgreSQL

Edit `configs/grimoire.postgres.yaml` (DSN form
`postgres://user:pass@127.0.0.1:5432/wordpress?sslmode=disable`), then run the
same `migrate` / `seed` / server commands with that config.

### Testing

```bash
go test ./...   # SQLite unit + contract + e2e tests, no external services
```

The cross-vendor contract suite runs against MySQL and PostgreSQL only when their
DSNs are provided; otherwise those runners skip:

```bash
GRIMOIRE_TEST_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/grimoire_test?parseTime=true' \
GRIMOIRE_TEST_POSTGRES_DSN='postgres://user:pass@127.0.0.1:5432/grimoire_test?sslmode=disable' \
  go test ./internal/storage/storagetest/... -v
```

## REST API & extensions (M5, write parity from M6)

grimoire also exposes a WordPress-compatible REST API at `/wp-json/wp/v2/*`
(read parity for posts/pages/comments/media/users, real WP response shape:
`_links`, `?_embed`, `X-WP-Total*` pagination headers) plus write endpoints
for comments (M4/M5) and posts/pages (M6: create/update/delete, Basic
auth over Application Passwords, optional `If-Unmodified-Since` optimistic
concurrency); all other post-related writes (categories/tags, media,
users) still return `501` (deferred to a later milestone). Authentication
for non-anonymous REST requests is via WordPress **Application Passwords**
(HTTP Basic, `wp_fast_hash`/`$generic$`, phpass/`$wp$`/bcrypt fallback);
self-service management lives at `/wp-json/wp/v2/users/me/application-passwords`.

A small, first-class Go package, [`pkg/extensions`](./pkg/extensions),
provides a compiled action/filter hook registry (no PHP, no dynamic
loading) wired at three points: post-render, REST request/response, and
comment-submit. See
[`plans/05-extensions-rest-api`](./plans/05-extensions-rest-api) for the
full specification.

## Milestones

- **M1 (current spec):** content core + switchable DB + WP-compatible schema + public read rendering + default theme.
- **M2:** users / auth / roles + internal content API.
- **M3:** React Spectrum admin SPA (CRUD posts / pages / media).
- **M4:** comments, media library, nav menus.
- **M5:** extension/plugin system + REST API parity.
- **M6:** admin CRUD editor — full post/page create/update/delete (rich-text
  editor, inline category/tag management, status lifecycle, optimistic
  concurrency) wired through both `/admin/api` and REST parity.

## Status

🚧 Early scaffold. M1 is specified in [`plans/01-content-core-read-rendering`](./plans/01-content-core-read-rendering).

## Licensing note

grimoire replicates WordPress's **schema and behavior**, not its GPL PHP source, so
the project is free to choose its own license. License selection is an open
decision tracked in the plans index.
