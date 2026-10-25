# Post-M10 Gap Analysis — Candidate Milestones (M11+)

> **Status: 📝 Proposal (M11 ✅ shipped).** This document is a gap analysis, not a
> committed roadmap. The WordPress-core-parity roadmap
> ([`../wordpress-core-parity-roadmap`](../wordpress-core-parity-roadmap)) is
> complete through its final group (M10.G). Each candidate below is a documented,
> code-verified limitation of the current implementation, written up so a future
> milestone can refine it into its own `requirements.md`/`design.md`/`tasks.md`.
> **M11 (REST delete parity, PR #64) and M13 (term re-parenting, PR #67) have
> since been specified and merged.** The remaining candidates (M12, M14, M15,
> M16) are still open; pick one and spec it first.

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

### M12 — Custom post types and custom taxonomies

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

### M13 — Term re-parenting and hierarchy writes  ✅ shipped (`../13-term-reparenting`, PR #67)

**Gap.** M9b *reads* `term_taxonomy.parent` into `domain.Term.ParentID`, but
nothing *writes* it — no admin or REST path re-parents or nests a term
(README "Term hierarchy is read-only... nothing re-parents a term"). WordPress
lets an editor set a category's parent.

**Scope sketch.** A `parent` field on term create/update (admin API + REST
`wp/v2/categories`), cycle prevention, count/`EXISTS`-semijoin correctness under
re-parenting, and the capability gate (`manage_categories`). Small-to-medium;
builds directly on M9b's read model and M10.F's write-group pattern.

### M14 — One-time sanitization backfill for pre-M10a content

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

### M15 — Hierarchical page URLs

**Gap.** Pages are served at the configured permalink structure (dated/flat)
rather than under their parent-page ancestry (`/parent/child`), a documented
divergence (compatibility.md "Pages follow the post structure"; README
"Hierarchical page URLs remain out of scope"). WordPress serves a child page at
`/{parent-slug}/{child-slug}` regardless of `permalink_structure`.

**Scope sketch.** Walk `post_parent` on `page` rows to build the nested path,
serve pages there as canonical, `301` the flat/dated form to it, and reconcile
the M9a "pages follow post structure" divergence. Medium; parallels M9b's nested
category walk but over `post_parent` instead of `term_taxonomy.parent`.

### M16 — Archive/pagination path-form parity (lower priority)

**Gap.** Archive pagination uses `?page=N` where WordPress uses `/page/{n}/`,
and the `?m=YYYYMM` date-query form and date archives under a plain permalink
are not served (compatibility.md:250–258). These are **deliberate, documented
divergences**, not bugs — `/category/news/page/2` is ambiguous with a child
category slugged `page`.

**Scope sketch.** Only worth a milestone if `/page/{n}/` parity is explicitly
wanted; it requires disambiguating the `page` segment (e.g. reserving it, or a
structure-aware classifier rule). Lowest priority — current behavior is correct
and documented.

## Recommended ordering

1. ~~**M11 (REST delete)**~~ — ✅ shipped (PR #64).
2. ~~**M13 (term re-parenting)**~~ — ✅ shipped (PR #67).
3. **M14 (sanitization backfill)** — closes the one standing security caveat
   from M10a; CLI-only. **Next.**
4. **M15 (hierarchical page URLs)** — completes WordPress routing parity.
5. **M12 (custom post types/taxonomies)** — largest; the biggest real-site
   parity win, best tackled once the smaller gaps are closed.
6. **M16 (pagination path form)** — optional polish.
