# <img src="./assets/icons/icon-64.png" alt="" width="68"> grimoire

**A Go-native, WordPress-schema-compatible CMS with a working embedded Adobe React Spectrum admin — no PHP, a swappable database backend, and WordPress-compatible content, auth, and REST surfaces.**

> **Current state:** M1-M10 are implemented. The public site, embedded Adobe React Spectrum admin, WordPress-compatible content/auth/REST surfaces, revisions/autosave, scheduled publishing, WordPress-equivalent content browsing (public and admin pagination, admin post/media filters), WordPress permalinks with canonical redirects, and nested category, tag, author and date archives all work today. REST write coverage includes posts/pages, comments, taxonomy terms, media, and users; a capability-aware HTML sanitizer guards every write boundary, and grimoire issues WordPress 6.8 `$wp$` password hashes.

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
- **Embedded Adobe React Spectrum admin.** The admin is working today as an
  embedded first-class UI for managing content and site operations.

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

See [`plans/`](./plans) for the full, Kiro-format specifications. For a
deeper look at WordPress interoperability, see
[`docs/compatibility.md`](./docs/compatibility.md) (the compatibility guide)
and [`docs/wordpress-compatibility-tour.md`](./docs/wordpress-compatibility-tour.md)
(a visual, side-by-side tour).

## Repository layout

| Path | Purpose |
|------|---------|
| `cmd/grimoire` | HTTP server entrypoint |
| `cmd/grimoire-cli` | migrations, seeding, admin bootstrap, session GC |
| `internal/domain` | entities + repository interfaces (ports) |
| `internal/content` | Post / Term / Option services |
| `internal/storage/{mysql,postgres,sqlite}` | database adapters |
| `internal/storage/migrations/<vendor>` | per-dialect migrations |
| `internal/web` | router, handlers, middleware |
| `internal/render` | template engine + WP template hierarchy |
| `themes/default` | default theme |
| `web/admin` | embedded Adobe React Spectrum admin |
| `plans/` | Kiro specs (requirements / design / tasks) |

## Running

grimoire ships two binaries: `grimoire` (the HTTP server) and `grimoire-cli`
(`migrate`, `seed`, `createadmin`, `sessions gc`). Configuration is a YAML file
(see [`configs/`](./configs)); every field can be overridden by an environment
variable (`GRIMOIRE_SERVER_ADDR`, `GRIMOIRE_THEME`,
`GRIMOIRE_DATABASE_VENDOR`, `GRIMOIRE_DATABASE_DSN`,
`GRIMOIRE_DATABASE_TABLE_PREFIX`).

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
GRIMOIRE_ADMIN_PASSWORD=... go run ./cmd/grimoire-cli createadmin -config configs/grimoire.sqlite.yaml -login admin -email admin@example.com
go run ./cmd/grimoire-cli sessions gc -config configs/grimoire.sqlite.yaml
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
GRIMOIRE_ADMIN_PASSWORD=... go run ./cmd/grimoire-cli createadmin -config configs/grimoire.mysql.yaml -login admin -email admin@example.com
go run ./cmd/grimoire-cli sessions gc -config configs/grimoire.mysql.yaml
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
even though the database rows resolve fine. Every shipped config in
[`configs/`](./configs) documents a `media.uploads_dir` field with this
default value spelled out — edit it in place to point at the real
directory.

### PostgreSQL

Edit `configs/grimoire.postgres.yaml` (DSN form
`postgres://user:pass@127.0.0.1:5432/wordpress?sslmode=disable`), then run the
same `migrate` / `seed` / `createadmin` / `sessions gc` / server commands with that config.

### Local Podman dev stack (`grimoire-cli podman`)

For running grimoire next to a stock WordPress container against **one shared
MySQL database**, `grimoire-cli podman` deploys and manages the whole stack. It
shells out to the `podman` binary (no SDK dependency) and keeps every
site-specific value configurable, so it is reusable beyond one site. See
[`docs/podman-dev-stack.md`](./docs/podman-dev-stack.md) for the full guide.

```bash
go run ./cmd/grimoire-cli podman build        # build localhost/grimoire:dev from the Containerfile
go run ./cmd/grimoire-cli podman up           # create/start the full stack in dependency order
go run ./cmd/grimoire-cli podman status       # pod/container state + endpoint probes
go run ./cmd/grimoire-cli podman down         # stop/remove pods, keep all data
go run ./cmd/grimoire-cli podman autostart install    # macOS auto-start (reboot/podman restart)
```

> **Shared-fate warning.** WordPress and grimoire share the same database tables
> and the same `wp-content` volume with **no isolation** — a write in one is
> immediately visible in the other, and the only way back from a bad write is a
> restore from backups. Treat the pair as one dev environment.

`up` encodes the invariants that otherwise bite, and prints them as it runs:

- **Table prefix is concatenated with no separator.** The default `accuweaver`
  means tables like `accuweaverposts` — **no trailing underscore**. Set it wrong
  and both apps quietly offer a fresh-install wizard over a full database.
- **DB credentials come from podman secrets** mounted at `/run/secrets/*`, never
  plaintext env vars and never visible in `podman inspect`.
- **Schema setup is the additive overlay migration** (`migrate -overlay`):
  grimoire-owned tables only, guarded by `IF NOT EXISTS`, no `ALTER TABLE`. The
  greenfield `migrate` is never run against the populated WordPress database.
- **Startup is gated on DB health**: MySQL must report healthy before WordPress
  or grimoire start.
- **Cross-pod DNS**: separate pods are separate network namespaces, so WordPress
  reaches the DB by pod name (`wp-db-pod:3306`), not localhost.

Every site-specific value is a flag (`-table-prefix`, `-grimoire-port`,
`-wordpress-port`, `-network`, `-image`, `-uploads-mount`, `-machine`, …) with a
documented default, and the most machine-varying ones also read
`GRIMOIRE_PODMAN_*` env vars (precedence: flag > env > default). Run any
subcommand with `-h` for its full guidance text. On macOS, `autostart` installs
a LaunchAgent that fires at **user login** (not the pre-login boot screen).

> **Why Podman and not Docker locally?** Licensing. Docker Desktop needs a paid
> subscription for commercial use in larger organizations, while Podman is
> Apache-2.0 and free for any use, with a daemonless/rootless model. These
> commands target `podman`; the Docker equivalents are a portability concern
> only. (Content was rephrased for compliance with licensing restrictions.)

### Testing

```bash
go test ./...            # SQLite unit + contract + e2e tests, no external services
go test -count=2 ./...   # isolation probe: catches tests that leak process-global state
```

The cross-vendor contract suite runs against MySQL and PostgreSQL only when their
DSNs are provided; otherwise those runners skip. CI supplies both, so all three
vendors are exercised on every push.

`multiStatements=true` is required on MySQL because the migration runner executes
multi-statement `.sql` files, and `parseTime=true` is required for `DATETIME`
columns to scan into `time.Time`:

```bash
GRIMOIRE_TEST_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/grimoire_test?parseTime=true&multiStatements=true' \
GRIMOIRE_TEST_POSTGRES_DSN='postgres://user:pass@127.0.0.1:5432/grimoire_test?sslmode=disable' \
  go test ./internal/storage/storagetest/... -v
```

## REST API & extensions

grimoire also exposes a WordPress-compatible REST API at `/wp-json/wp/v2/*`
(read parity for posts/pages/comments/media/users, real WP response shape:
`_links`, `?_embed`, `X-WP-Total*` pagination headers) plus write endpoints
for comments, posts/pages, categories/tags, media, and users (M10.B/M10.C
closed the former media/user `501`s). Authentication for non-anonymous REST requests is via WordPress
**Application Passwords** (HTTP Basic, `wp_fast_hash`/`$generic$`,
phpass/`$wp$`/bcrypt fallback); self-service management lives at
`/wp-json/wp/v2/users/me/application-passwords`.

A small, first-class Go package, [`pkg/extensions`](./pkg/extensions),
provides a compiled action/filter hook registry (no PHP, no dynamic
loading) wired at three points: post-render, REST request/response, and
comment-submit. See
[`plans/05-extensions-rest-api`](./plans/05-extensions-rest-api) for the
full specification.

## Milestones

- ✅ **M1:** [Content core + switchable DB + WP-compatible schema + public read rendering](./plans/01-content-core-read-rendering) — delivers the domain core, vendor adapters, default theme, and public rendering path.
- ✅ **M2:** [Users, authentication, and roles](./plans/02-users-auth-roles) — delivers user accounts, application-password auth, and role-aware access control.
- ✅ **M3:** [Adobe React Spectrum admin](./plans/03-spectrum-admin) — delivers the embedded working admin shell for managing site content.
- ✅ **M4:** [Comments, media, and menus](./plans/04-comments-media-menus) — delivers comment workflows, media handling, and navigation menus.
- ✅ **M5:** [Extensions and REST API](./plans/05-extensions-rest-api) — delivers the compiled hook system and WordPress-compatible REST surface.
- ✅ **M6:** [Admin CRUD editor](./plans/06-admin-crud-editor) — delivers full content editing, status transitions, and optimistic concurrency in admin.
- ✅ **M7:** [Revisions and scheduler](./plans/07-revisions-scheduler) — delivers revision history, autosave, and scheduled publishing.
- ✅ **M8:** [Content browsing parity](./plans/wordpress-core-parity-roadmap) — delivers public and admin pagination with out-of-range 404s, admin post `search`/`status`/`author` filters, and a media library with filters and a grid/list toggle.
- ✅ **M9a:** [Permalinks and canonical routing](./plans/09-permalinks-canonical-routing) — delivers `permalink_structure`/`category_base`/`tag_base` reads, the `%postname%`/`%post_id%`/`%year%`/`%monthnum%`/`%day%` token set (WordPress's three presets), `301` canonical redirects from every other recognised form, canonical REST `link` values, and the `tag`/`author`/`date` template kinds. Unsupported structures fall back to the flat route with a startup `WARN`.
- ✅ **M9b:** [Archives and nested categories](./plans/09.1-archives-nested-categories) — delivers the four archive routes M9a left unserved: nested category archives at their full ancestry (`/category/news/local`), with `301` recovery from the flat and wrong-ancestor forms, plus tag, author and date archives. `category_base`/`tag_base` are honored in route registration, path classification, theme links and REST `link` values; a category archive lists and counts its descendants' posts; REST `parent` carries `term_taxonomy.parent`, category/tag `link` values are real archive paths, and a user's `link` is `/author/{nicename}`. Zero schema changes.
- ✅ **M10a:** [Write-boundary content safety](./plans/10-rest-write-content-safety) — grimoire's first HTML sanitizer. A single capability-aware allow-list policy sanitizes comment content and post content/excerpt/title at the write boundary before storage (three tiers mirroring WordPress's `kses` lists, selected by field kind and `unfiltered_html`), reduces `post_title` to plain text, and backstops comment content at render. Covers the write paths that already shipped in M4/M6; adds **no** new REST write route (the media/user write routes were opened later by M10.B/M10.C). Pre-M10a stored post content is not retroactively sanitized.
- ✅ **M10.B:** [REST media write](./plans/10.1-rest-media-write) — `POST /media` binary-upload create and `PUT`/`PATCH`/`POST /media/{id}` metadata update over `domain.MediaWriter`, gated on `upload_files`, sanitized through M10a's policy; `DELETE` stays `501`.
- ✅ **M10.C:** [REST user write](./plans/10.2-rest-user-write) — `POST /users` create and `PUT`/`PATCH`/`POST /users/{id}` profile/password/role update, gated on `create_users`/`edit_users`/`promote_users`; `DELETE` stays `501`.
- ✅ **M10.D:** [content.rendered improvements](./plans/10.3-rest-content-rendered) — Gutenberg block-comment stripping and `srcset`/`sizes`/`loading`/`decoding` on rendered `<img>` in the REST `content.rendered` field only.
- ✅ **M10.E:** [M10 write-route test coverage](./plans/10.4-m10-test-coverage) — capability matrices for every write route plus a consolidated content-safety coverage doc.
- ✅ **M10.F:** [Navigation menu editing](./plans/10.5-nav-menu-editing) — the first menu write path (create/update/delete, reorder, nest) in an `edit_theme_options` + CSRF admin-API group, sanitized through M10a's policy; menu reads keep their `edit_posts` gate.
- ✅ **M10.G:** [`$wp$` issued password format](./plans/10.6-wp-hash-issuance) — `password.Hash` now issues WordPress 6.8 `$wp$` hashes and `NeedsRehash` upgrades legacy bcrypt/phpass rows to `$wp$` on login, so passwords round-trip with a live WordPress 6.8 database.

## Status

✅ M1-M10 are implemented. The WordPress-core-parity roadmap
([`plans/wordpress-core-parity-roadmap`](./plans/wordpress-core-parity-roadmap))
is complete through its final group (M10.G); every M10 group (10.A–10.G) shipped
via its own spec under `plans/10.*`.

As of M10a, write-path content is sanitized at the boundary by a
capability-aware allow-list policy before it is stored: comment content and
post content/excerpt from writers without `unfiltered_html` are filtered against
allow-lists mirroring WordPress's `kses` comment and post lists, `unfiltered_html`
holders (default editor/administrator) bypass filtering as WordPress exempts them,
`post_title` is reduced to plain text, and comment content is also filtered at
render as a backstop. The guarantee is that every value written from M10a onward
is sanitized at the writer's tier — not that already-stored content is safe:
pre-M10a post content is not retroactively sanitized. M10a is the write-boundary
content policy only; it does **not** add any new REST write route.

Known gaps worth knowing before adopting an existing WordPress site:

- **REST deletes.** `DELETE /wp-json/wp/v2/media/{id}` and `.../users/{id}`
  are not exposed (they return `501`). REST *create* and *update* for media and
  users shipped in M10.B/M10.C; only the delete verb remains deferred.
- **Taxonomies and post types.** Only `category` and `post_tag` are served, and
  archives list `post` rows only — custom taxonomies and custom post types are
  out of scope. Term hierarchy is read-only: M9b reads `term_taxonomy.parent`,
  but nothing re-parents a term.

Permalinks are no longer a gap. M9a serves `permalink_structure` — the
`%postname%`, `%post_id%`, `%year%`, `%monthnum%` and `%day%` tokens, covering
WordPress's three presets — and `301`s the flat `/{slug}` path and the
non-canonical trailing-slash form to the canonical URL, which closed
[#23](https://github.com/roboweaver/grimoire/issues/23). A structure using
`%category%` or `%author%`, or one with no post-identifying token, is not
supported: grimoire falls back to the flat route and warns at startup that
published URLs will not resolve, and `grimoire-cli migrate -check` reports the
same before the server is started. Changing `permalink_structure` requires a
restart, since the options are read once at startup. One divergence to know:
grimoire applies the structure to pages as well as posts, where WordPress serves
pages at `/{slug}` regardless — see
[`docs/compatibility.md`](./docs/compatibility.md).

Archives and nested categories are no longer gaps either. M9b serves a category
at its full ancestry (`/category/news/local`), `301`s the flat and
wrong-ancestor forms to it, lists and counts its descendants' posts, and adds
tag, author and date archives; `category_base` and `tag_base` now take effect
everywhere a path is built or classified, and a base that collides with another
base or with the permalink structure is named in a startup `WARN` and by
`grimoire-cli migrate -check`. One divergence to know: archive pagination is
`?page=N` rather than WordPress's `/page/{n}/`, deliberately —
`/category/news/page/2` is indistinguishable from a child category slugged
`page`. Precedence is worth knowing too: a path rooted at an archive base, or
shaped like a date, wins over a post slugged the same way, as it does in
WordPress. Hierarchical *page* URLs remain out of scope.
[`docs/compatibility.md`](./docs/compatibility.md) has the full list, including
the behaviors grimoire matches that look arbitrary without the provenance.

## Licensing note

grimoire is licensed under Apache-2.0. That choice supports broad commercial and
open-source adoption, includes an explicit patent grant, avoids incorporating
GPLv2-only WordPress PHP source, and keeps the project focused on schema/API
interoperability rather than source-code reuse. This is not legal advice.
