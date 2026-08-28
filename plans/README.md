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
| 01 | [`01-content-core-read-rendering`](./01-content-core-read-rendering) | ✅ Implemented | Content core + switchable DB (MySQL/Postgres/SQLite) + WordPress-compatible schema + public read rendering + default theme |
| 02 | [`02-users-auth-roles`](./02-users-auth-roles) | ✅ Implemented | Users + WordPress-compatible auth (phpass→bcrypt), server-side sessions, 5 default roles/capabilities, CSRF, internal content write API, minimal login UI |
| 02.1 | [`02.1-wp-hash-real-db`](./02.1-wp-hash-real-db) | ✅ Implemented | WordPress 6.8 `$wp$` (HMAC-SHA384→bcrypt) password verification + capabilities scalar-truthiness lock-in + env-gated real-WordPress-DB validation |
| 02.2 | [`02.2-excerpt-rendering`](./02.2-excerpt-rendering) | ✅ Implemented | WordPress-faithful excerpts on list views — manual excerpts render as HTML (not escaped), empty excerpts auto-generate from content (`wp_trim_excerpt`: strip Gutenberg block comments/shortcodes/tags, ~55-word trim + `…`), extend trusted-content boundary to `Excerpt` |
| 03 | [`03-spectrum-admin`](./03-spectrum-admin) | ✅ Implemented | Adobe React Spectrum **read-only** admin SPA — served by the Go binary via `go:embed` (no Node at runtime), reusing M2 session auth + a read-only `/admin/api` (session, dashboard counts, posts/pages list + detail). CRUD (create/update/delete, editor, media) deferred to milestone 06. |
| 04 | [`04-comments-media-menus`](./04-comments-media-menus) | ✅ Implemented | Comments (public list + moderation-queue submission + admin approve/spam/trash), media library (attachment listing, traversal-safe `/wp-content/uploads/` serving, multipart upload, attach-to-post), and **read-only** navigation menus (`nav_menu` taxonomy read + public theme render + admin tree, incl. theme-location resolution). grimoire's first write paths: activates the M3-designed `X-CSRF-Token` contract for authenticated admin writes and adds a double-submit token + pluggable spam filter for anonymous comment submits. Overlay-safe (only an additive greenfield `{prefix}comments`/`commentmeta` migration plus `{prefix}posts` column additions the M1 greenfield schema omits; media/menus reuse existing tables). Menu **editing** deferred. |
| 05 | [`05-extensions-rest-api`](./05-extensions-rest-api) | ✅ Implemented | WordPress REST API parity (`/wp-json/wp/v2/*` read for posts/pages/comments/media/users, WP-shaped `_links`/`_embedded`/pagination headers, `$generic$`/`wp_fast_hash` Application Passwords auth over TLS/loopback) plus one write endpoint (`POST .../comments`, reusing M4's `CommentService.Create`) — all other REST writes deferred to M6 (`501`, not `404`/`405`). A native Go extension mechanism (`pkg/extensions`: compiled action/filter hook registry, no PHP, no dynamic loading, externally importable) wired at three points: post-render, REST request/response, comment-submit. One additive greenfield-only migration (`0004_rest_post_fields`; Postgres dialect is a safe no-op if re-run, MySQL/SQLite dialects error if run against an already-overlaid DB and so are simply never run there) plus additive post→term-IDs/postmeta read ports and `UserRepository`/`AdminPostFilter` extensions — no other schema change. |
| 06 | [`06-admin-crud-editor`](./06-admin-crud-editor) | ✅ Implemented | Admin write path for posts/pages: create/update/delete via `/admin/api` (reusing M2's `PostWriteService`, M4's `X-CSRF-Token` contract unchanged) **and** REST parity at `/wp-json/wp/v2/posts`/`/pages`, closing the `501`s M5 deferred here. Adds inline category/tag management (new `TermWriter.Update` + `PostTermsWriter` write port), a full draft/pending/publish/private/future status lifecycle (scheduled-publish execution documented as a known, deferred limitation — no cron exists), lightweight optimistic concurrency (`modified`-timestamp check, admin API required / REST `If-Unmodified-Since` optional, matching WordPress's own lack of native REST concurrency), and a TipTap-based rich-text editor embedded in Spectrum-styled toolbar chrome (HTML-native, matching `post_content`). No new migration. Revisions, autosave, scheduled-publish execution, and REST categories/tags/media/user writes deferred to M7+. |
| 07 | [`07-revisions-scheduler`](./07-revisions-scheduler) | ✅ Implemented | Closes M6's four deferrals: WordPress-compatible post/page revision history (reuses `{prefix}posts` with `post_type='revision'` + new `post_parent` column, admin-API list/diff/restore, configurable retention/pruning), autosave (additive revision rows, read-time "newer autosave" notice rather than a write-time lock against M6's `ConflictError`), scheduled-publish execution (a new `internal/scheduler` ticker goroutine sharing the existing server-shutdown lifecycle, flipping `future` → `publish` via an unexported internal system principal, unreachable from any route), and REST write parity for categories/tags (`internal/web/rest_terms.go`, full CRUD, reusing M6's `TermWriteService` unchanged). One additive migration (`0005_post_parent`, all 3 vendors). REST media/user writes explicitly deferred to M8. |
| 08–10 | [`wordpress-core-parity-roadmap`](./wordpress-core-parity-roadmap) | ✅ Implemented (M8) / 📝 Specified (M9/M10 are roadmap-level and each require their own spec before implementation) | UI-parity-first roadmap closing the gaps `docs/compatibility.md`/`docs/wordpress-compatibility-tour.md` document. **M8 Content Browsing Parity** (implementation-ready): public home/category pagination totals + out-of-range 404s; admin post search/status/author filters with 400 invalid-filter handling; media library search/type/date/parent filters, a mutually-exclusive grid/list toggle (replacing today's simultaneous Grid+Table render), and pagination; shared vendor-neutral filter/pagination contracts. No schema change — every new filter/count reuses an existing column (`post_author`, `post_date`, `post_mime_type`, `post_parent`) or existing counter (`PostCounter.CountByStatus`). Supersedes M07's README note that REST media/user writes were deferred to "M8" — this roadmap reassigns them to M10. **M9 Routing & Taxonomy Parity** (roadmap-level): `permalink_structure`/`category_base`/`tag_base` options, core permalink tokens + canonical redirects, tag/date/author archives, nested categories via the already-populated but unread `term_taxonomy.parent`. **M10 REST Write & Content Safety Parity** (roadmap-level): a capability-aware write-boundary HTML sanitization policy — introduced for the first time, since no sanitizer exists in the codebase today — landing before REST `/media`/`/users` writes are enabled (closing their `501`s) and before `content.rendered` fidelity improvements (Gutenberg delimiter stripping, responsive images). |

## Guiding principles

- **No PHP.** Pure Go, single static binary.
- **WordPress compatibility is a schema/behavior contract**, not a code port. We
  replicate the data model (`wp_*` tables) so grimoire can read an existing
  WordPress database.
- **Vendor switchability is a first-class requirement.** Repository interfaces in
  the domain; one adapter package per database vendor; the *same* contract test
  suite runs against every vendor.
- **Each milestone gets its own spec → plan → implementation cycle.**

## Resolved decisions

- **License.** grimoire is licensed under Apache-2.0. That choice supports broad
  commercial and open-source adoption, includes an explicit patent grant,
  avoids incorporating GPLv2-only WordPress PHP source, and keeps the project
  focused on schema/API interoperability rather than source-code reuse. This is
  not legal advice.

## Open decisions

These are tracked here and must be resolved before or during the relevant
milestone:

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
