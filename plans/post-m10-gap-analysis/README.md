# Post-M10 Gap Analysis — Candidate Milestones (M11+)

> **Status: 📝 Proposal (M11, M12 incl. write, M13, M14, M15, M16 ✅ shipped; M17-M22 shipped as follow-ups).** This document is a gap analysis, not a
> committed roadmap. The WordPress-core-parity roadmap
> ([`../wordpress-core-parity-roadmap`](../wordpress-core-parity-roadmap)) is
> complete through its final group (M10.G). Each candidate below is a documented,
> code-verified limitation of the current implementation, written up so a future
> milestone can refine it into its own `requirements.md`/`design.md`/`tasks.md`.
> **M11 (REST delete parity, PR #64), M13 (term re-parenting, PR #67), and M14
> (sanitization backfill, PR #70) have since been specified and merged.** M12 is
> fully shipped: the read half (12.A/12.B, PRs #75/#76/#77) and the write half
> (12.C, spec `17-custom-types-write`), as are M15 (PR #72) and M16
> (`21-archive-path-pagination`, both PRs). Trash and restore shipped as M22
> (`22-trash-and-restore`, 3 of 3). **No planned milestones remain.** Still open,
> unscheduled: theme mods that reference a deleted attachment, intermediate image
> sizes and files on disk, child attachments, term-side post meta, WordPress hooks
> and the object cache, a comment Trash view, a per-type trash opt-out, a
> configurable purge cadence, and the `?m=` hour/minute/second forms.

Every gap is traced to where it is already acknowledged in
[`../../docs/compatibility.md`](../../docs/compatibility.md) or the root
`README.md`, and to the code that makes it true.

## Candidate milestones

### M11 — REST delete parity (media + users)  ✅ shipped (`../11-rest-delete-parity`, PR #64)

**Gap.** `DELETE /wp-json/wp/v2/media/{id}` and `DELETE /wp-json/wp/v2/users/{id}`
still answer `501` (`restNotImplemented`, `internal/web/rest_media.go:51`,
`internal/web/rest_users.go`). M10.B/M10.C enabled REST *create* and *update* for
both; delete is the one write verb group 10 deliberately deferred (README
"Known gaps → REST deletes").

**Scope sketch.** Real `DELETE` handlers over the existing writers, WordPress's
`force`/trash semantics (WordPress trashes by default and hard-deletes with
`?force=true`; media is always hard-deleted), the `reassign` parameter on user
delete (reassign authored content to another user), capability gates
(`delete_posts`/`delete_others_posts` for media per attachment ownership;
`delete_users` for users), and sanitize/CSRF parity with the create/update
routes. Likely adds a `domain.MediaWriter.Delete` / `domain.UserRepository.Delete`.
**Smallest, most self-contained candidate — natural immediate next step.**

**Shipped in M11:** `MediaWriter.Delete`/`UserRepository.Delete` ports + wprepo impls, `MediaWriteService.Delete`/`UserService.Delete`, and the two REST handlers — `force=true` required, media per-attachment `CanDeletePost` gate with on-disk file unlink, user `reassign` (id or `false`) with self-delete/`delete_users` guards, transactional reassign-or-delete, `{"deleted":true,"previous":...}` envelope. Collection-level DELETE stays `501`.

### M12 — Custom post types and custom taxonomies  ✅ shipped (read: `../12-custom-types-read`, PRs #75/#76/#77; write: `../17-custom-types-write`)

**Gap.** Only `post`/`page` rows and the `category`/`post_tag` taxonomies are
served; admin reads filter `post_type IN ('post','page')`
(`internal/storage/wprepo/adminreads.go:118`) and public reads filter
`post_type = 'post'` (`internal/storage/wprepo/repo.go:93`). Custom post types
and custom taxonomies are out of scope today (compatibility.md:322, README
"Taxonomies and post types"). **This is the largest parity gap against real
WordPress sites**, which routinely register CPTs/CTs.

**Scope sketch.** A registry of known post types/taxonomies (read from the
database's own rows and/or configuration, since grimoire has no PHP
`register_post_type`), public + admin + REST read/write for them, archive and
single routing, and the capability model. Large; would likely split into
read-first then write-parity sub-milestones.
**Shipped in M12 (read half, 12.A/12.B):** a custom post type/taxonomy registry
sourced from the database's own rows and grimoire configuration, replacing the
hard-coded `post_type IN ('post','page')` / `post_type = 'post'` filters;
archive/single and term-archive routing through the existing permalink,
nested-hierarchy, and pagination machinery; `/wp-json/wp/v2/{rest_base}` REST
reads in the existing envelopes; admin type validation; and a migrate diagnostic.
Delivered across PRs #75, #76, #77.
**Shipped in M17 (write half, 12.C):** REST and admin-API create, update and
delete for registered custom post types and taxonomies, with capabilities
derived from `capability_type`, sanitization, slug and `parent` guards, term
assignment and delete cleanup (`plans/17-custom-types-write`).
**Deferred after M17, closed by M22:** delete was permanent, with no trash. M22 added trash and restore to REST (PR 1) and the admin (PR 2), and `content.trash_days` with the automatic purge (PR 3); see `plans/22-trash-and-restore`.
**Shipped in M18 (PR 1, `plans/18-write-hardening`):** stored rows of types not
registered under `content:` are unwritable (`403` on the admin API, `404` for
revisions and autosave), the scheduler skips them, revisions and autosave honor
`capability_type`, and term names of every taxonomy are reduced to plain text on
write (an unchanged name is stored as is).
**Shipped in M19 (PR 1, `plans/19-term-gate-and-delete-parity`):** term update and
delete are gated on the stored taxonomy being managed (`category`, `post_tag` or
registered), so an unmanaged taxonomy such as `nav_menu` or `product_cat` answers
`403` on the admin API; create stays `400`, name-addressed assignment is reported
in `partial`, and assignment to the built-in taxonomies now checks term membership.
**Shipped in M18 (PR 2, `plans/18-write-hardening`):** deleting a post or page
detaches its terms (counts recounted), deletes its comments, commentmeta and
postmeta, and re-points child pages and attachments to the deleted row's parent.
Page `parent` is writable over REST and the admin API with a cycle and type
guard, and a page's slug is suffixed on a parent change. Honest gap at the time: trash was
unhandled (nav-menu items were closed in M19 PR 3; trash in M22).
**Shipped in M19 (PR 2, `plans/19-term-gate-and-delete-parity`):** REST page items
always carry `parent`, including `0`. Deleting a post removes its id from
`sticky_posts` (post rows only; a malformed option is left alone and, because the
`content` package has no logger, nothing is logged).
**Shipped in M19 (PR 3, `plans/19-term-gate-and-delete-parity`):** deleting a post,
page or registered-type row deletes the nav-menu items that point at it (type
`post_type`, object equal to the deleted type, object id equal to the id) with their
postmeta and relationships, and recomputes the menu term counts from the true
relationship count. WordPress counts only published and draft items, so a count for
imported items in other statuses can differ until WordPress recomputes it. Child menu
items were left pointing at the deleted id; M20 PR 1 closed that. The remaining open
gap after M19 was trash, which M22 closed.
**Shipped in M20 (PR 1, `plans/20-delete-gap-closure`):** children of a deleted menu
item are re-pointed to the item's own menu parent (nearest surviving ancestor for
chains, `0` for a top-level item), in both the post-delete purge and the menu editor
delete, and `NavMenuRepo.DeleteItem` now recounts the menu. WordPress core does not do
this on the server (only its editor JavaScript does); grimoire does because its tree
builder hides orphans. Media delete cleanup and unmanaged-taxonomy relationships
were open after PR 1 and closed in PR 2.
**Shipped in M20 (PR 2, `plans/20-delete-gap-closure`):** deleting a post, page or
registered-type row removes its relationships in every taxonomy except
`link_category` (whose object ids are link ids), recounting each affected term;
residual risk is a plugin taxonomy of non-post objects with a colliding id. Deleting
a media item now removes its menu items (children re-parented as in PR 1), comments
and commentmeta, `_thumbnail_id` references on other posts and relationships, plus
orphan `term_relationships` rows with no `term_taxonomy`, and the row delete is
guarded by `post_type = 'attachment'` (any other id is not found). WordPress itself
removes relationships only for taxonomies registered for the object type. Still open:
trash and restore, theme mods (`custom_logo`, `header_image`), intermediate image
sizes, re-pointing child attachments, hooks and the object cache.
**M15 follow-up (found while building M12) — fixed in PR #79.** M15's
`PageService.Resolve` confirmed the leaf with `BySlug(leaf.Slug)`, which was
ambiguous for same-slug leaves under different parents, and its recovery path
could disclose unpublished pages. `Resolve` now confirms the leaf by ID via
`PublishedByID(..., "page")` and gates the moved-page redirect on the page being
published (spec `plans/16-page-resolve-fix`).
**M12 real-DB verification:** the real-database fixture
(`internal/routing/realdb_custom_test.go`) ran green against the dev Docker
WordPress database (custom post type `boxes`, taxonomy `box-sets`, permalink
structure `/%year%/%monthnum%/%day%/%postname%/`).
### M13 — Term re-parenting and hierarchy writes  ✅ shipped (`../13-term-reparenting`, PR #67)

**Gap.** M9b *reads* `term_taxonomy.parent` into `domain.Term.ParentID`, but
nothing *writes* it — no admin or REST path re-parents or nests a term
(README "Term hierarchy is read-only... nothing re-parents a term"). WordPress
lets an editor set a category's parent.

**Scope sketch.** A `parent` field on term create/update (admin API + REST
`wp/v2/categories`), cycle prevention, count/`EXISTS`-semijoin correctness under
re-parenting, and the capability gate (`manage_categories`). Small-to-medium;
builds directly on M9b's read model and M10.F's write-group pattern.

### M14 — One-time sanitization backfill for pre-M10a content  ✅ shipped (`../14-sanitization-backfill`, PR #70)

**Gap.** M10a "closes the inflow; it does not drain the pool"
(compatibility.md:470). Every value *written* from M10a onward is sanitized, but
post content stored before M10a (imported from WordPress, or written by M4–M9
paths) is never retroactively sanitized. A stored pre-M10a `<script>` in post
content is inert for comments (render backstop) but not for post bodies.

**Scope sketch.** An opt-in `grimoire-cli` command that walks `{prefix}posts`,
applies the M10a tier-B policy to `post_content`/`post_excerpt`, and rewrites
rows — idempotent, dry-run-first, with a report. Careful: it mutates a database
grimoire may not own, so it must be explicit, reversible-aware, and never
automatic. Medium; mostly a CLI + the existing policy, no new web surface.

**Shipped in M14:** the `grimoire-cli backfill-content` subcommand (dry-run by
default, `-apply` to write, never automatic) walking post_type {post, page,
revision} across all statuses via a new keyset-paginated backfill read port;
tier-B `SanitizeAt` applied to `post_content`/`post_excerpt` with change-detection
so only differing rows are rewritten through a new narrow content-only update
port that does **not** bump `post_modified`; a body-free auditable report (per-row
ID/type/field(s)/byte-length + totals); fail-safe per-row sanitize errors; and a
cross-vendor storagetest contract + command-level coverage. No migration, no web
surface, no new capability.

### M15 — Hierarchical page URLs  ✅ shipped (`../15-hierarchical-page-urls`, PR #72)

**Gap.** Pages are served at the configured permalink structure (dated/flat)
rather than under their parent-page ancestry (`/parent/child`), a documented
divergence (compatibility.md "Pages follow the post structure"; README
"Hierarchical page URLs remain out of scope"). WordPress serves a child page at
`/{parent-slug}/{child-slug}` regardless of `permalink_structure`.

**Scope sketch.** Walk `post_parent` on `page` rows to build the nested path,
serve pages there as canonical, `301` the flat/dated form to it, and reconcile
the M9a "pages follow post structure" divergence. Medium; parallels M9b's nested
category walk but over `post_parent` instead of `term_taxonomy.parent`.

**Shipped in M15:** a page is now exempt from `permalink_structure` and served at
its nested `post_name` ancestry over `post_parent` (`/{parent}/{child}/`); the
flat and old dated forms `301` to it (query preserved raw). A new `KindPage`
classifier row + pure `Structure.PagePath` + a `Canonical` page guard (reading a
transient `domain.Post.PageAncestry` so `Canonical` stays the single pure URL
builder); a content `PageHierarchy`/`PageService`/`PageMovedError` mirroring the
term equivalents over `post_parent` with the same orphan/cycle degradation; an
additive `domain.PageReader`/`PostRepo.ListPages` port; a `/{slug}/*` wildcard
route; and the page REST `link`. Cross-vendor contract + handler + content +
real-WordPress-DB nested-page fixture coverage. No migration, no column, no new
capability, no second permalink construction site.

### M16 — Archive/pagination path-form parity  ✅ shipped (`../21-archive-path-pagination`, 2 of 2)
**Shipped in M16 (PR 1, `plans/21-archive-path-pagination`):** `/page/{n}/` on
the home page and every paginated archive (nested category, tag, author, date,
custom archive, custom term), through a classifier that peels a trailing
`page/{n}` and reuses the existing rules, so `page` is not reserved. `?page=N`
and `?paged=N` still answer `200` with a relative canonical `Link` header;
`/page/1/` and the wrong slash form `301` with the raw query kept. Themes get
handler-built `PrevURL` and `NextURL`. Three rare shapes are shadowed, with no
fallback (documented in compatibility.md, M16); the development real-database
check found none. No schema change.
**Shipped in M22 (PR 1, PR 2, PR 3, `plans/22-trash-and-restore`):** REST and admin
`DELETE` of a post, page or registered-type row trashes by default, in the rows
WordPress writes, and `?force=true` is the permanent delete; `PUT`/`PATCH` trash and
restore; the admin has a Trash view with Restore, Undo and Empty Trash.
`content.trash_days` (default 30, `0` turns trash off) and an hourly scheduler purge
delete managed-type rows trashed longer than the window, with the full delete
cleanup. Still open: no per-type opt-out, no comment Trash view, purge cadence not
configurable, media and users have no trash (as in WordPress).

**Shipped in M16 (PR 2):** `GET /?m=YYYY[MM[DD]]` serves the date archive in
place with `200`. The first `m` wins, an invalid value is ignored (the home page
renders as before, with no date read), and `m` is ignored on every other route.
Structured sites send a path-form canonical `Link` header and path-form
pagination links; Flat sites use `/?m=…&page=N` links and no header. The
real-database check ran green against the development WordPress database (75
`?m=` strings from 25 posts). Caveat: under Flat, `BaseURL` is `/?m=…`, so a
custom theme must use `PrevURL` and `NextURL`. Still open: the hour, minute and
second forms of `m`, and `?m=` on routes other than `/`.
## Recommended ordering

1. ~~**M11 (REST delete)**~~ — ✅ shipped (PR #64).
2. ~~**M13 (term re-parenting)**~~ — ✅ shipped (PR #67).
3. ~~**M14 (sanitization backfill)**~~ — ✅ shipped (PR #70); closed the one
   standing security caveat from M10a, CLI-only.
4. ~~**M15 (hierarchical page URLs)**~~ — ✅ shipped (PR #72); completed
   WordPress routing parity for pages.
5. ~~**M12 (custom post types/taxonomies, read and write)**~~ — ✅ shipped
   (read PRs #75/#76/#77; write via `17-custom-types-write`).
6. ~~**M16 (pagination path form and `?m=`)**~~ — ✅ shipped
   (`21-archive-path-pagination`, 2 of 2).
**Recommended next:** none. No planned milestones remain. M22
(`22-trash-and-restore`) shipped in 3 of 3 PRs: REST trash and restore (PR 1),
the admin API and SPA (PR 2), and `content.trash_days` with the automatic purge
(PR 3). The trash gap is closed. What is still open is the unscheduled list in
the status banner at the top and the "Honest gap" notes in the milestone sections
above.

