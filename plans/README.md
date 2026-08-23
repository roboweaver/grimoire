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
| 02 | [`02-users-auth-roles`](./02-users-auth-roles) | 🚧 In progress | Users + WordPress-compatible auth (phpass→bcrypt), server-side sessions, 5 default roles/capabilities, CSRF, internal content write API, minimal login UI |
| 02.1 | [`02.1-wp-hash-real-db`](./02.1-wp-hash-real-db) | 🚧 In progress | WordPress 6.8 `$wp$` (HMAC-SHA384→bcrypt) password verification + capabilities scalar-truthiness lock-in + env-gated real-WordPress-DB validation |
| 02.2 | [`02.2-excerpt-rendering`](./02.2-excerpt-rendering) | 🚧 In progress | WordPress-faithful excerpts on list views — manual excerpts render as HTML (not escaped), empty excerpts auto-generate from content (`wp_trim_excerpt`: strip Gutenberg block comments/shortcodes/tags, ~55-word trim + `…`), extend trusted-content boundary to `Excerpt` |
| 03 | [`03-spectrum-admin`](./03-spectrum-admin) | 📝 Specified | Adobe React Spectrum **read-only** admin SPA — served by the Go binary via `go:embed` (no Node at runtime), reusing M2 session auth + a read-only `/admin/api` (session, dashboard counts, posts/pages list + detail). CRUD (create/update/delete, editor, media) deferred to milestone 06. |
| 04 | [`04-comments-media-menus`](./04-comments-media-menus) | 📝 Specified | Comments (public list + moderation-queue submission + admin approve/spam/trash), media library (attachment listing, traversal-safe `/wp-content/uploads/` serving, multipart upload, attach-to-post), and **read-only** navigation menus (`nav_menu` taxonomy read + public theme render + admin tree). grimoire's first write paths: activates the M3-designed `X-CSRF-Token` contract for authenticated admin writes and adds a double-submit token + pluggable spam filter for anonymous comment submits. Overlay-safe (only an additive greenfield `{prefix}comments`/`commentmeta` migration; media/menus reuse existing tables). Menu **editing** deferred. |
| 05 | _(planned)_ extensions-rest-api | ⬜ Not started | Extension/plugin system + REST API parity |
| 06 | _(planned)_ admin-crud-editor | ⬜ Not started | Admin write path — create/update/delete posts & pages, rich editor, media uploads; extends the M3 read-only Spectrum admin with the M4-style CSRF-validated unsafe-request contract (X-CSRF-Token). |

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
- **Adopt `$wp$` as grimoire's own new-password format.** M2.1 verifies WordPress
  6.8 `$wp$` hashes in place but still issues **bcrypt** for new passwords.
  Adopting `$wp$` (HMAC-SHA384→bcrypt) for new/rehashed passwords would maximize
  WordPress round-trip compatibility and length-safety. _Deferred from M2.1;
  owner: project lead. Target: a later auth milestone, with its own
  `NeedsRehash` upgrade policy + tests._
