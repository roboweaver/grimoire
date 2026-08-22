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

## Milestones

- **M1 (current spec):** content core + switchable DB + WP-compatible schema + public read rendering + default theme.
- **M2:** users / auth / roles + internal content API.
- **M3:** React Spectrum admin SPA (CRUD posts / pages / media).
- **M4:** comments, media library, nav menus.
- **M5:** extension/plugin system + REST API parity.

## Status

🚧 Early scaffold. M1 is specified in [`plans/01-content-core-read-rendering`](./plans/01-content-core-read-rendering).

## Licensing note

grimoire replicates WordPress's **schema and behavior**, not its GPL PHP source, so
the project is free to choose its own license. License selection is an open
decision tracked in the plans index.
