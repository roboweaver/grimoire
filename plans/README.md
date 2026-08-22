# grimoire — Plans

This folder holds grimoire's specifications in **[Kiro](https://kiro.dev) spec
format**. Each milestone is a self-contained spec directory with three files:

| File | Purpose | Style |
|------|---------|-------|
| `requirements.md` | *What* to build — user stories + acceptance criteria | EARS (`WHEN`/`IF … THEN … SHALL`) |
| `design.md` | *How* to build it — architecture, components, data flow, testing | Technical prose + diagrams |
| `tasks.md` | *Step-by-step* implementation breakdown | Checklist with acceptance criteria |

> **grimoire** — a wizard's book of spells and knowledge: one authoritative tome
> that stores what you know and the incantations to bring it to life. The CMS is
> that for the web — a single Go binary holding your content and the templates
> (*incantations*) that render it into a living site, with a database layer you
> can swap like changing the ink.

## Milestone index

| # | Spec | Status | Summary |
|---|------|--------|---------|
| 01 | [`01-content-core-read-rendering`](./01-content-core-read-rendering) | 📝 Specified | Content core + switchable DB (MySQL/Postgres/SQLite) + WordPress-compatible schema + public read rendering + default theme |
| 02 | _(planned)_ users-auth-roles | ⬜ Not started | Users, authentication, roles, internal content API |
| 03 | _(planned)_ spectrum-admin | ⬜ Not started | Adobe React Spectrum admin SPA (CRUD posts/pages/media) |
| 04 | _(planned)_ comments-media-menus | ⬜ Not started | Comments, media library, navigation menus |
| 05 | _(planned)_ extensions-rest-api | ⬜ Not started | Extension/plugin system + REST API parity |

## Guiding principles

- **No PHP.** Pure Go, single static binary.
- **WordPress compatibility is a schema/behavior contract**, not a code port. We
  replicate the data model (`wp_*` tables) so grimoire can read an existing
  WordPress database.
- **Vendor switchability is a first-class requirement.** Repository interfaces in
  the domain; one adapter package per database vendor; the *same* contract test
  suite runs against every vendor.
- **Each milestone gets its own spec → plan → implementation cycle.**

## Open decisions

These are tracked here and must be resolved before or during the relevant
milestone:

- **License.** grimoire replicates WordPress's schema/behavior, not its GPL PHP
  source, so the project may choose its own license (candidates: Apache-2.0, or a
  GPLv2-compatible license). _Owner: project lead. Target: before first public
  release._
- **Query builder dependency.** M1 design selects [Bun](https://bun.uptrace.dev)
  for multi-dialect SQL over `database/sql`. Revisit if a vendor Bun does not
  support is required.
- **Full WordPress template-hierarchy fidelity.** M1 implements a pragmatic
  subset (`index`, `single`, `page`, `archive`, `category`). Broader hierarchy
  parity is deferred.
