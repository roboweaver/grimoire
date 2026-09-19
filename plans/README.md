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
| 09a | [`09-permalinks-canonical-routing`](./09-permalinks-canonical-routing) | ✅ Implemented | Honors `permalink_structure`/`category_base`/`tag_base` from the existing database, serves the core token set (`%postname%`, `%post_id%`, `%year%`, `%monthnum%`, `%day%`) via a new pure `internal/routing` package, and `301`s every non-canonical form — including the flat `/{slug}` path — to one canonical URL per post. Closes the gap where a site on any non-plain permalink structure 404s every URL it has published ([#23](https://github.com/roboweaver/grimoire/issues/23)). An unsupported or unparseable structure degrades to the flat route with a `WARN` at startup rather than refusing to boot. Also extends the existing `render.hierarchy` map with `tag`/`author`/`date` kinds (roadmap 9.F, folded in from M1's template deferral) so the follow-on archive spec adds handlers only. Zero schema changes. **Refines roadmap groups 9.A/9.B/9.F only** — 9.C (tag/date/author archives) and 9.D (nested categories) are deferred to a follow-on spec, which inherits two decisions already resolved in this one: parent category archives include descendants, and nested category routes are canonical with flat `301`ing to them. Two divergences are documented in `docs/compatibility.md`: the configured structure applies to `page` rows as well as `post` rows, where WordPress exempts pages (so a page's canonical URL is dated and `/about` `301`s to it); and `category_base`/`tag_base` are read and resolved but not yet honored by any route, since the category route stays flat until 9.C/9.D. The real-WordPress-database checks (`internal/routing/realdb_test.go`, `test/e2e/m9_permalinks_realdb_test.go`) are gated on `GRIMOIRE_TEST_WP_DSN` and have not yet been run against a live WordPress database. |
| 08–10 | [`wordpress-core-parity-roadmap`](./wordpress-core-parity-roadmap) | ✅ Implemented (M8, plus M9 groups 9.A/9.B/9.F via M9a) / 📝 Specified (M9 groups 9.C/9.D/9.E and M10 are roadmap-level and each require their own spec before implementation) | UI-parity-first roadmap closing the gaps `docs/compatibility.md`/`docs/wordpress-compatibility-tour.md` document. **M8 Content Browsing Parity** (implementation-ready): public home/category pagination totals + out-of-range 404s; admin post search/status/author filters with 400 invalid-filter handling; media library search/type/date/parent filters, a mutually-exclusive grid/list toggle (replacing today's simultaneous Grid+Table render), and pagination; shared vendor-neutral filter/pagination contracts. No schema change — every new filter/count reuses an existing column (`post_author`, `post_date`, `post_mime_type`, `post_parent`) or existing counter (`PostCounter.CountByStatus`). Supersedes M07's README note that REST media/user writes were deferred to "M8" — this roadmap reassigns them to M10. **M9 Routing & Taxonomy Parity** (part implemented): the `permalink_structure`/`category_base`/`tag_base` options, core permalink tokens + canonical redirects, and template-hierarchy fidelity (groups 9.A/9.B/9.F) were refined into [`09-permalinks-canonical-routing`](./09-permalinks-canonical-routing) and shipped there; tag/date/author archives (9.C), nested categories via the already-populated but unread `term_taxonomy.parent` (9.D) and the remaining cross-vendor `Term.ParentID` coverage (9.E) still need a follow-on spec. **M10 REST Write & Content Safety Parity** (roadmap-level): a capability-aware write-boundary HTML sanitization policy — introduced for the first time, since no sanitizer exists in the codebase today — landing before REST `/media`/`/users` writes are enabled (closing their `501`s) and before `content.rendered` fidelity improvements (Gutenberg delimiter stripping, responsive images). |

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
- **How much WordPress template-hierarchy fidelity to support.** M1 shipped a
  pragmatic subset (`index`, `single`, `page`, `archive`, `category`) and
  deferred the rest. Settled in M9a (roadmap **9.F**): the existing
  `render.hierarchy` map gains `tag`, `author` and `date` kinds, each resolving
  `{kind}` → `archive` → `index`, and no second resolution path is introduced.
  Slug- and entity-specific candidates (`category-{slug}`, `tag-{slug}`,
  `author-{nicename}`) are deliberately out of scope — no in-tree theme uses
  them, and the existing mechanism makes them cheap to add if one does.

## Open decisions

These are tracked here and must be resolved before or during the relevant
milestone:

- **Query builder dependency.** M1 design selects [Bun](https://bun.uptrace.dev)
  for multi-dialect SQL over `database/sql`. Revisit if a vendor Bun does not
  support is required.
- **Adopt `$wp$` as grimoire's own new-password format.** M2.1 verifies WordPress
  6.8 `$wp$` hashes in place but still issues **bcrypt** for new passwords.
  Adopting `$wp$` (HMAC-SHA384→bcrypt) for new/rehashed passwords would maximize
  WordPress round-trip compatibility and length-safety. _Deferred from M2.1;
  owner: project lead. Now assigned: **M10.G**, since M10.C is the first point
  where grimoire writes password hashes over a public API. Carries its own
  `NeedsRehash` upgrade policy + tests, and must reconcile M10.C's "never
  introduce a second hash scheme" wording._

### Deferrals assigned to a milestone

Previously these sat in milestone prose with no owning task, which is how they
went unscheduled:

| Deferred from | Item | Now |
|---|---|---|
| M1 | Template-hierarchy fidelity | **M9.F** — ✅ landed in M9a |
| M2.1 | `$wp$` for issued passwords | **M10.G** |
| M4 | Navigation menu **editing** (read-only shipped) | **M10.F** |

Still unassigned by design: the Bun dependency question above is a watch item,
not scheduled work — it only becomes a task if a vendor Bun cannot support is
actually required.
