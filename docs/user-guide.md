# grimoire user guide

A tour of what grimoire is, how it is put together, and how to run it — for
operators and developers evaluating or adopting it. For the exact command and
flag surface see [`cli.md`](./cli.md); for WordPress-interoperability detail see
[`compatibility.md`](./compatibility.md) and the side-by-side
[`wordpress-compatibility-tour.md`](./wordpress-compatibility-tour.md). This
guide describes M1–M10 as implemented and does not claim more than that.

---

## What grimoire is

grimoire is a **Go-native, WordPress-schema-compatible CMS**. It replicates the
WordPress database schema, authentication model, and REST API surface — not the
GPL PHP source — so it can read, render, and (for supported content types)
manage an existing WordPress site through a WordPress-compatible admin and API.

- **No PHP.** Pure Go, shipped as a single static binary.
- **WordPress-compatible schema.** It reads and writes the WordPress data model
  (`posts`, `options`, `terms`, the taxonomy join tables, and so on) under the
  site's own `table_prefix`.
- **Swappable database backend.** The same binary runs on **MySQL/MariaDB**,
  **PostgreSQL**, or **SQLite**; the vendor is a config choice, not a rebuild.

Adding a new database vendor means adding one adapter package — vendor SQL never
leaks into the rest of the system.

## Architecture (ports & adapters)

grimoire is built as ports and adapters around a stable domain core, so the
database vendor and the render target are both swappable without touching the
business logic:

```
web (net/http + chi) → content services → domain (entities + repository ports)
                                                    ↑
                         storage adapters: mysql | postgres | sqlite
```

- **`internal/domain`** owns the entities and the **repository interfaces**
  (ports). Nothing above it knows which database is in use.
- **`internal/storage/<vendor>`** provides the adapters (`sqlite`, `mysql`,
  `postgres`); each speaks its own dialect behind the same ports.
- **`internal/content`** holds the Post / Term / Option (and related) services
  that the web layer calls.
- **`internal/render`** turns domain data into HTML via themes — folders of Go
  `html/template` files implementing a subset of the WordPress template
  hierarchy.
- **`internal/web`** is the router, handlers and middleware.

The repository layout and the full Kiro-format specifications live in the
[`README.md`](../README.md) and [`plans/`](../plans).

## Running grimoire

grimoire ships two binaries — `grimoire` (the server) and `grimoire-cli` (ops).
Configuration is a YAML file in [`configs/`](../configs); fields can be
overridden by `GRIMOIRE_*` environment variables and finally by command-line
flags. See [`cli.md`](./cli.md) for the complete flag and environment reference
and exact commands.

### On SQLite

The zero-dependency path: a local file database, no external services. Apply the
greenfield schema, optionally seed sample content, bootstrap an administrator,
and serve. The exact commands are in
[`cli.md`'s SQLite development loop](./cli.md#sqlite-development-loop).

### Against an existing WordPress database

Point the config's DSN at the WordPress database and set `table_prefix` to match
the site. **Do not run bare `migrate`** — that greenfield path issues
`ALTER TABLE` against WordPress-owned tables and can corrupt a populated
database. Instead run `migrate -check` (read-only preflight) and then
`migrate -overlay` (additive, grimoire-owned tables only). The commands are in
[`cli.md`'s adoption workflow](./cli.md#adopting-an-existing-wordpress-database).

> **The `media.uploads_dir` caveat.** grimoire reads media rows from the
> database but never copies the underlying upload files. When pointing at an
> existing WordPress site you must set `media.uploads_dir` to that site's real
> `wp-content/uploads` directory on disk. It defaults to the relative path
> `wp-content/uploads` (resolved against grimoire's own working directory),
> which will not exist for an external install — leaving it unset makes
> `/wp-content/uploads/*` requests and admin media thumbnails return `404` even
> though the database rows resolve fine. grimoire logs a startup warning when the
> directory is missing.

## The embedded admin

grimoire embeds a working **Adobe React Spectrum** admin as a first-class UI for
managing content and site operations — compiled into the binary, served under
`/admin`. It covers content editing with status transitions and optimistic
concurrency, revision history and autosave, comment and media workflows
(including uploading new attachments), navigation-menu editing, and the
filtered/paginated post and media lists. Authentication is a server-managed
session cookie.

## The REST API

grimoire exposes a WordPress-compatible REST API at **`/wp-json/wp/v2/*`** with
the real WordPress response shape — `_links`, `?_embed`, and `X-WP-Total*`
pagination headers. It offers read parity for posts, pages, comments, media and
users, plus write endpoints for comments, posts/pages, categories/tags, media,
and users — including `DELETE` for media and users (which require `force=true`,
matching WordPress's no-trash semantics for those types). Every write passes
through a capability-aware HTML sanitizer at the write boundary (see
[`compatibility.md`](./compatibility.md) for the tier model).

Non-anonymous REST requests authenticate with WordPress **Application
Passwords** over HTTP Basic auth; self-service management lives at
`/wp-json/wp/v2/users/me/application-passwords`. By default grimoire refuses
Application Passwords over a plain, non-loopback connection, matching WordPress;
`GRIMOIRE_REST_REQUIRE_TLS_FOR_APPLICATION_PASSWORDS` and
`GRIMOIRE_REST_TRUSTED_PROXY_HEADER` (see [`cli.md`](./cli.md)) control that gate
for local development or behind a TLS-terminating proxy.

## Permalinks and archives

grimoire reads `permalink_structure`, `category_base` and `tag_base` from the
site's own `options` table at startup and serves content at those paths.

- **Permalinks (M9a).** The supported structure tokens are `%postname%`,
  `%post_id%`, `%year%`, `%monthnum%` and `%day%`, in any order — covering
  WordPress's three presets. Non-canonical forms that still identify the post
  (the flat `/{slug}` path, the wrong trailing-slash form) `301`-redirect to the
  one canonical URL, preserving the query string. A structure using an
  unsupported token (`%category%`, `%author%`) or no post-identifying token
  falls back to the flat route and warns loudly at startup and in
  `migrate -check`. Changing `permalink_structure` requires a restart, since the
  options are read once at startup.
- **Archives (M9b).** Nested category archives serve at their full ancestry
  (`/category/news/local`), with `301` recovery from the flat and wrong-ancestor
  forms; tag, author (`/author/{nicename}`) and date archives are served too.
  `category_base` and `tag_base` take effect everywhere a path is built or
  classified.

**Known divergences worth knowing** (full detail in
[`compatibility.md`](./compatibility.md)):

- grimoire applies the permalink structure to **pages** as well as posts, where
  WordPress serves pages at `/{slug}` regardless.
- Archive pagination is **`?page=N`** rather than WordPress's `/page/{n}/`,
  deliberately — `/category/news/page/2` is otherwise indistinguishable from a
  child category slugged `page`.

## Themes

The public site renders through themes: folders of Go `html/template` files
under the themes directory (default `themes`, override with the server's
`-themes` flag or `GRIMOIRE_THEME` for the active theme name). The engine
implements a subset of the WordPress template hierarchy; the shipped
`themes/default` is the reference.

## Known gaps

Worth knowing before adopting an existing WordPress site (the authoritative,
fully annotated list is in [`compatibility.md`](./compatibility.md) and the
[`README.md`](../README.md)):

- **Only `category` and `post_tag` taxonomies, and only `post` archives.**
  Custom taxonomies and custom post types are out of scope, and archives list
  `post` rows only.
- **Term hierarchy is read-only.** M9b reads `term_taxonomy.parent` to resolve
  nested category paths, but nothing re-parents a term — editing a term's parent
  is not possible.
