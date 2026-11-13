# Requirements Document

M12 — custom post types and taxonomies (read-first)

## Introduction

Real WordPress sites register custom post types (CPTs) and custom taxonomies
(CTs). grimoire serves only `post`/`page` and `category`/`post_tag`, and those
limits are hard-coded in many places. `AdminPostFilter.Types` defaults to
`{"post","page"}` (`internal/storage/wprepo/adminreads.go:20-25`), `Authors()`
hard-codes `post_type IN ('post','page')` (`adminreads.go:118`), `RecentPosts`
filters `post_type = 'post'` (`internal/storage/wprepo/repo.go:147`),
`PostService.BySlug`/`PublishedByID` hard-code `"post","page"`
(`internal/content/post.go:73,85`), and `registerRESTPosts` loops over
`[]string{"post","page"}` (`internal/web/rest_posts.go:30`). Taxonomies are
the same: `content.TaxonomyCategory` and `taxonomyPostTag` are fixed, and
`registerRESTTerms` maps only `category` and `post_tag`
(`internal/web/rest_terms.go`, `restTermBase`).

Much of the lower layers is already parameterized. `BySlug` and
`PublishedByID` accept a `types ...string` argument (`repo.go:159-209`).
`domain.ArchiveFilter` already carries `Taxonomy`, `TermIDs` and `Types`
(`internal/domain/repository.go`). `domain.TermRepository.BySlug(ctx, taxonomy,
slug)` and `domain.TermReader.ListByTaxonomy(ctx, taxonomy)` take the taxonomy
as an argument, and `content.LoadTermHierarchy(ctx, reader, taxonomy)` is
already taxonomy-parameterized. `domain.Term.ParentID` is populated from
`term_taxonomy.parent` for any taxonomy. M12 therefore wires a registry through
existing seams rather than rewriting them.

M12 is the largest parity gap in the post-M10 gap analysis
(`../post-m10-gap-analysis/README.md`, entry "M12 — Custom post types and custom
taxonomies"). This spec is the READ-FIRST half: roadmap group 12.A (registry,
routing, read) and 12.B (REST read). The WRITE half, 12.C, is a separate later
spec. In this spec every write verb on a CPT or CT REST route answers 501
through the existing `restNotImplemented` convention, and the admin-API write
path keeps rejecting types other than `post`/`page`
(`internal/web/adminapi_posts.go:103`).

The registry is the core design decision. It is built at startup from operator
CONFIGURATION ONLY. The database is used to validate and diagnose, never to
expose. WordPress stores no "public" flag in the database, and plugins keep
private data under custom post types (`shop_order`, `wpcf7_contact_form`,
`acf-field-group`, `wp_template`, `customize_changeset`). Auto-exposing every
distinct `post_type` found in `{prefix}posts` would publish that data.

M12 also validates the `type` query parameter of the admin-API list, which today
is forwarded unvalidated (`internal/web/adminapi.go:226`) so an authenticated
caller can list rows of any post type, including private plugin types such as
`shop_order`; this closes an existing gap and is a deliberate tightening of an
authenticated endpoint.

M12 deliberately EXPANDS the compatibility boundary. Custom post types and
taxonomies move from "out of scope" to "a scoped milestone", as roadmap
Requirement 25.3 and the roadmap's compatibility-boundary note require.

M12 refines roadmap Requirements 23, 24 and 25 and task groups 12.A and 12.B
(`../wordpress-core-parity-roadmap/requirements.md`,
`../wordpress-core-parity-roadmap/tasks.md`).

## Glossary

- **CPT** — a custom post type: a `{prefix}posts` `post_type` value other than
  `post`/`page`, declared in configuration.
- **CT** — a custom taxonomy: a `{prefix}term_taxonomy.taxonomy` value other
  than `category`/`post_tag`, declared in configuration.
- **Registry** — the in-memory set of declared CPT and CT entries, built once at
  startup from the `content:` configuration section and validated against
  Requirement 3. It is immutable after startup.
- **Registry entry** — one declared post type or taxonomy with its resolved
  fields (name, `rest_base`, rewrite slug, `with_front`, `hierarchical`,
  `has_archive`, `capability_type`, attached taxonomies or object types).
- **`rest_base`** — the path segment under `/wp-json/wp/v2/` that serves an
  entry. It defaults to the entry's name.
- **Rewrite slug** — the URL base under which an entry's single items, archive
  or terms are served. It defaults to the entry's name.
- **Front** — the permalink structure's fixed prefix. `archivePrefix(kind)`
  prepends it to category and tag bases only when the base option is unset, and
  to date and author bases always (`internal/routing/classify.go`).
- **`Structure.Classify` / `Target`** — the pure, total classifier with a
  first-match precedence table (`internal/routing/classify.go`). Kinds are
  `KindNone`/`KindPost`/`KindCategory`/`KindTag`/`KindAuthor`/`KindDate`/
  `KindPage`.
- **CPT archive / CPT single / CT term archive** — the three new route shapes:
  `/{archive-slug}/`, `/{slug}/{postname}/`, and `/{slug}/{term}/`. The
  hierarchical variants carry arbitrary-depth ancestry.
- **`PostNode`** — the lightweight row (ID, slug, parent, status) returned by
  the new additive node-reader port for a given `post_type`. It generalizes
  M15's page-only `ListPages`.
- **`TermHierarchy`** — the term-ancestry walk over `term_taxonomy.parent`
  (`internal/content/archive.go:251`), reused for hierarchical CTs.
- **`restNotImplemented`** — the existing convention that answers 501 for a REST
  verb that is registered but not yet supported.
- **Fixed point** — a canonical built from the RESOLVED row's own data, so the
  handler's comparison against `r.URL.Path` is stable.
- **`redirectCanonical`** — the handler helper that issues a 301 and appends
  `r.URL.RawQuery` RAW (`internal/web/handlers.go`).
- **Fixture test** — a test against an imported real-WordPress-database export,
  gated by `GRIMOIRE_TEST_WP_DSN` and skipping cleanly without it
  (`internal/routing/realdb_pages_test.go`, `realdb_test.go`).

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | **Scope split.** This spec is 12.A + 12.B (read, routing, REST read). 12.C (write) is a separate later spec. Write verbs on CPT/CT REST routes answer 501 via `restNotImplemented`; the admin-API write paths keep rejecting non-post/page types (`internal/web/adminapi_posts.go:103`). No admin SPA UI change. | Requirements 9, 12. Write parity is not specified here. |
| D2 | **Security-driven registry source.** The registry is built at startup from operator configuration ONLY. The database is used to VALIDATE and DIAGNOSE, never to EXPOSE. A post type or taxonomy is served ONLY if it is declared in configuration. This still satisfies roadmap Req 23.1/24.1: configuration declares, database rows validate and inform `grimoire-cli migrate -check`. Executing plugin/theme PHP to discover types stays permanently out of scope (roadmap Req 25.1). | Requirements 1, 3, 11. No auto-exposure of `shop_order` and similar private types. |
| D3 | **Config schema.** A new top-level `content:` section with `post_types:` and `taxonomies:` lists. Post type fields: `name` (required), `public` (default true, because a declared entry is intended to be served; `false` registers it for REST/admin read only), `rest_base` (default name), `has_archive` (bool or string slug, default false), `slug` (default name), `with_front` (default true), `hierarchical` (default false), `capability_type` (default `post`), `taxonomies` (attached taxonomy names). Taxonomy fields: `name` (required), `object_types`, `public` (default true), `rest_base` (default name), `slug` (default name), `with_front` (default true), `hierarchical` (default false). Defaults mirror WordPress `register_post_type`/`register_taxonomy` where sensible. | Requirement 1. `Config{Server,Theme,Database,Session,Media,REST,Scheduler}` has no such section today (`internal/config/config.go`). |
| D4 | **Fail-fast validation at startup.** Refuse to start on: an empty name; a name that is not a valid WordPress key (lowercase alphanumerics, dash, underscore; max 20 chars for post types, 32 for taxonomies); a name colliding with a built-in key; a duplicate name; a rewrite slug or `rest_base` colliding with another entry or with a built-in REST/route base or the configured category/tag/author base, or ambiguous with the structure's front; an unregistered taxonomy or object type reference. Database state is NOT a validation failure. | Requirement 3. Operator misconfiguration is loud, never silently different URLs. |
| D5 | **Routing shapes (WordPress defaults).** CPT single `/{slug}/{postname}/`; CPT archive `/{archive-slug}/` when `has_archive`; archive pagination stays `?page=N`; hierarchical CPT single `/{slug}/{parent}/{child}/` (nested ancestry over `post_parent`, same orphan/cycle degradation as M9b/M15); CT term archive `/{slug}/{term}/`, nested `/{slug}/{parent}/{child}/` when hierarchical (reusing `TermHierarchy`). `with_front=true` prepends the structure's front exactly as `archivePrefix` does. Trailing slash follows the structure. A CPT URL does NOT use the dated permalink tokens. CPT date archives and feeds are out of scope. | Requirements 4, 5, 6, 7. |
| D6 | **Classifier precedence.** New Kinds for CPT archive, CPT single and CT term archive are placed as follows: the custom ARCHIVE row is evaluated AFTER the built-in archive base rows (category/tag/author/date) and BEFORE the structure's own post-shape row, because under WordPress's "Post name" preset (`/%postname%/`, and `/blog/%postname%/`) the post-shape row claims any single-segment path, so an archive at `/products/` would otherwise be unreachable (WordPress itself gives custom post type rewrite rules priority over the post structure); the custom SINGLE and custom TERM rows are evaluated AFTER the post-shape row and BEFORE the M15 `KindPage` row. A page whose slug equals a registered CPT/CT slug becomes unreachable at that URL (documented consequence). The canonical for each is built from the RESOLVED row's own data (fixed point), with 301 from non-canonical forms preserving the query string RAW. | Requirements 4, 5, 6, 7. |
| D7 | **Public visibility.** CPT public routes and REST collections list and serve PUBLISHED rows only. A CPT with `public=false` is not routed publicly. A non-published CPT row requested by id is served only to a caller holding the edit capability derived from the entry's `capability_type` (default `post`, the exact analogue of today's `edit_posts` check in `handleRESTPostSingle`, `internal/web/rest_posts.go`), through an `auth.CapabilitySuffix` helper; EVERY other caller, anonymous or authenticated, receives `404 rest_post_invalid_id`, byte-for-byte consistent with posts and pages today. No new capability NAMES. | Requirements 8, 9, 12. `auth.capSuffix(postType)` today returns `pages` for `page`, else `posts` (`internal/auth/policy.go`) and SHALL stay behaviorally identical for `post` and `page`. |
| D8 | **REST read.** `/wp-json/wp/v2/{rest_base}` GET collection and GET single for each registered CPT, in the posts/pages envelope and pagination headers, with `type` = CPT name, `link` = CPT canonical, and per attached taxonomy a term-id array keyed by that taxonomy's `rest_base`. The same for each registered CT, in the categories/tags shape (id, count, description, link, name, slug, taxonomy, parent). POST/PUT/PATCH/DELETE answer 501 (D1). `restRoutes` lists the registered routes. `/wp/v2/types` and `/wp/v2/taxonomies` are OUT of scope. | Requirements 9, 10. |
| D9 | **Additive storage/ports only.** No migration, no new column, no new table, no new capability name. Reuse `BySlug(types...)`, `PublishedByID(types...)`, `ArchiveFilter.Types/Taxonomy`, `ListByTaxonomy`, `TermHierarchy`. ONE additive port returns `PostNode`s for a given `post_type`; `ListPages` and every existing read stay unchanged. Admin-API READ accepts registered type names via `AdminPostFilter.Types` (default remains `{post,page}`), and the admin-API list rejects any other `type` value with 400 `invalid_type`; `Authors()` is extended to registered types. `grimoire-cli migrate -check` REPORTS (read-only) database types and taxonomies not declared in configuration, naming the config key to add. | Requirements 2, 11, 12. |
| D10 | **Tests.** Cross-vendor `storagetest` contract for the node-reader port; routing unit tests for the new Kinds, precedence, collisions and path constructors (fixed point); config tests for defaults and every D4 rejection; content tests for the generalized taxonomy archive and CPT single/archive services; handler tests for 200/301/404 and query preservation; REST tests for CPT and CT collection, single, 501 and route index; at least one fixture-based test against an imported real-WordPress-database export containing a CPT and a CT, mirroring `realdb_pages_test.go`; and an assembly test in `cmd/grimoire` that drives the production server assembly function (Requirement 13). Pure Go/SQLite, env-gated MySQL/Postgres, no new dependency; `gofmt`/`go vet`/`go build ./...`/`go test ./...` green at the end of every task. | Requirements 13, 14. |

## Requirements

### Requirement 1: Operator-declared registry from a content configuration section

**User Story:** As a site operator, I want to declare exactly which custom post
types and taxonomies grimoire serves, so that private plugin data in my
WordPress database is never published by accident.

#### Acceptance Criteria

1. THE Config loader SHALL accept a top-level `content:` section containing
   `post_types:` and `taxonomies:` lists, in addition to the existing sections
   (`internal/config/config.go`).
2. WHEN a post type entry omits optional fields, THE Config loader SHALL apply
   the defaults `public=true`, `rest_base=name`, `has_archive=false`,
   `slug=name`, `with_front=true`, `hierarchical=false`, `capability_type=post`.
3. WHEN a taxonomy entry omits optional fields, THE Config loader SHALL apply
   the defaults `public=true`, `rest_base=name`, `slug=name`, `with_front=true`,
   `hierarchical=false`.
4. WHEN `has_archive` is a string, THE Config loader SHALL use that string as the
   archive slug; WHEN it is `true`, THE Config loader SHALL use the entry's
   rewrite slug as the archive slug.
5. WHEN the `content:` section is absent, THE System SHALL behave exactly as it
   does before M12, serving only `post`/`page` and `category`/`post_tag`.
6. THE System SHALL build the registry once at startup from configuration only,
   and SHALL NOT add any post type or taxonomy to the registry because its rows
   exist in the database (D2).

### Requirement 2: Registry-driven reads replace hard-coded type lists

**User Story:** As a maintainer, I want every hard-coded `post`/`page` and
`category`/`post_tag` filter to read from the registry, so that a declared type
flows through existing reads without per-type code paths.

#### Acceptance Criteria

1. WHEN an admin-API list read supplies `AdminPostFilter.Types` naming registered
   types, THE System SHALL return rows of those types
   (`internal/storage/wprepo/adminreads.go:20-25`).
2. WHEN `AdminPostFilter.Types` is empty, THE System SHALL keep the default
   `{"post","page"}`.
3. THE `Authors()` read SHALL include rows whose `post_type` is any registered
   type in addition to `post` and `page` (`adminreads.go:118`).
4. THE System SHALL add ONE additive port that returns `PostNode` values (ID,
   slug, parent, status) for a given `post_type`, generalizing M15's `ListPages`
   and leaving `ListPages` and every existing read unchanged.
5. WHEN the node-reader port returns a large set, THE port SHALL return rows in a
   stable order by ID, each exactly once.
6. THE milestone SHALL add NO migration, NO column, NO table and NO capability
   name (D9).
7. WHEN a taxonomy archive is requested for a registered CT, THE content service
   SHALL read terms through `TermReader.ListByTaxonomy` and `TermHierarchy` for
   that taxonomy, generalizing `CategoryArchive`, which hard-codes
   `TaxonomyCategory` (`internal/content/term.go`).
8. WHEN the admin-API list receives a `type` value that is not `post`, not
   `page`, and not the name of a registered post type, THE System SHALL respond
   400 with error code `invalid_type` and read nothing
   (`internal/web/adminapi.go:226`).
9. WHEN the admin-API list receives a `type` value naming a registered post type,
   public or not, THE System SHALL serve that type's rows.
10. WHEN the admin-API list receives an empty or absent `type`, THE System SHALL
    keep today's default of `post` and `page`.

### Requirement 3: Fail-fast validation of the registry at startup

**User Story:** As a site operator, I want a misconfigured registry to stop
startup with a clear message, so that I never run a site whose URLs silently
differ from what I intended.

#### Acceptance Criteria

1. IF a post type or taxonomy `name` is empty, THEN THE System SHALL refuse to
   start and report the offending entry.
2. IF a name contains characters other than lowercase alphanumerics, dash and
   underscore, or exceeds 20 characters for a post type or 32 for a taxonomy,
   THEN THE System SHALL refuse to start.
3. IF a post type name equals one of `post`, `page`, `attachment`, `revision`,
   `nav_menu_item`, `custom_css`, `customize_changeset`, `oembed_cache`,
   `user_request`, `wp_block`, `wp_template`, `wp_template_part`,
   `wp_global_styles`, `wp_navigation`, THEN THE System SHALL refuse to start.
4. IF a taxonomy name equals one of `category`, `post_tag`, `nav_menu`,
   `link_category`, `post_format`, THEN THE System SHALL refuse to start.
5. IF two entries of the same kind share a name, THEN THE System SHALL refuse to
   start.
6. IF an entry's rewrite slug or `rest_base` equals another entry's, or a
   built-in REST/route base (`posts`, `pages`, `media`, `comments`, `users`,
   `categories`, `tags`), or the configured category, tag or author base, THEN
   THE System SHALL refuse to start.
7. IF an entry's rewrite slug is ambiguous with the permalink structure's front,
   THEN THE System SHALL refuse to start.
8. IF a taxonomy `object_types` entry or a post type `taxonomies` entry names an
   unregistered post type or taxonomy, THEN THE System SHALL refuse to start.
9. WHEN a declared type has zero rows in the database, THE System SHALL start
   normally and log that the type has no rows; database state SHALL NOT be a
   validation failure.

### Requirement 4: Custom post type single and archive routing

**User Story:** As a visitor, I want a custom post type's items and archive at
the URLs WordPress would use, so that links from the original site keep working.

#### Acceptance Criteria

1. WHEN a request path equals `/{slug}/{postname}/` for a registered public CPT,
   THE handler SHALL render the published item with that `post_name`.
2. WHERE a CPT declares `has_archive`, WHEN a request path equals
   `/{archive-slug}/`, THE handler SHALL render the paginated archive of that
   CPT's published items, using `?page=N` for pagination, with a stable ordering
   and no item dropped or duplicated across pages.
3. IF a request resolves to no published item or no archive, THEN THE handler
   SHALL respond 404.
4. WHERE a CPT declares `with_front=true`, THE canonical SHALL prepend the
   permalink structure's front exactly as `archivePrefix` does for category and
   tag bases.
5. THE CPT canonical SHALL NOT use the dated permalink_structure tokens.
6. THE System SHALL construct each CPT canonical at a single routing construction
   site, parallel to `Structure.Canonical` (`internal/routing/routing.go:408`),
   and SHALL add no second builder.
7. WHEN a request path differs from the CPT canonical, THE handler SHALL respond
   301 to the canonical, appending `r.URL.RawQuery` RAW.
8. WHEN a CPT entry has `public=false`, THE System SHALL NOT route it publicly.

### Requirement 5: Hierarchical custom post type routing

**User Story:** As a visitor, I want nested custom post type URLs to resolve, so
that a hierarchical type behaves like WordPress pages.

#### Acceptance Criteria

1. WHERE a CPT declares `hierarchical=true`, THE canonical SHALL be
   `/{slug}/{parent}/{child}/` built from the resolved item's own ancestry over
   `post_parent` (a fixed point).
2. WHEN a request path walks its segments through the CPT's `PostNode` graph to a
   published leaf, THE handler SHALL render that leaf (200).
3. WHEN a request names a non-canonical ancestry for a resolvable leaf, THE
   handler SHALL respond 301 to the canonical, appending `r.URL.RawQuery` RAW.
4. WHEN a `post_parent` names an ID absent from the node graph (an orphan), THE
   walk SHALL terminate and yield the shorter resolvable path, as
   `TermHierarchy.Ancestry` does (`internal/content/archive.go:251`).
5. WHEN a `post_parent` chain revisits a node (a cycle), THE walk SHALL terminate
   on the first repeat, so a corrupt graph cannot loop.
6. THE ancestry walk SHALL be built from a single read of the CPT's nodes, not a
   per-level database round-trip.

### Requirement 6: Custom taxonomy term archive routing

**User Story:** As a visitor, I want custom taxonomy term archives at
WordPress's URLs, so that term-scoped navigation keeps working.

#### Acceptance Criteria

1. WHEN a request path equals `/{slug}/{term}/` for a registered public CT, THE
   handler SHALL render the paginated archive of published items attached to
   that term, filtering through `ArchiveFilter.Taxonomy` and `TermIDs`
   (`internal/domain/repository.go`).
2. WHERE a CT declares `hierarchical=true`, THE canonical SHALL be
   `/{slug}/{parent}/{child}/` built from the resolved term's own ancestry via
   `TermHierarchy` over `term_taxonomy.parent`.
3. WHEN a request names a non-canonical ancestry for a resolvable term, THE
   handler SHALL respond 301 to the canonical, mirroring `CategoryMovedError`
   handling in `categoryArchive`, appending `r.URL.RawQuery` RAW.
4. IF a request resolves to no term, THEN THE handler SHALL respond 404.
5. WHERE a CT declares `with_front=true`, THE canonical SHALL prepend the
   structure's front as `archivePrefix` does.
6. WHEN a term's `term_taxonomy.parent` is read for a CT, THE System SHALL
   populate `domain.Term.ParentID` exactly as it does for categories (roadmap
   Req 24.4).

### Requirement 7: Classifier precedence and chi registration for the new shapes

**User Story:** As a maintainer, I want the new URL shapes classified purely and
deterministically, so that M12 cannot regress M9a/M9b/M15 routing.

#### Acceptance Criteria

1. THE `Structure.Classify` precedence table SHALL gain new Kinds for CPT
   archive, CPT single and CT term archive; THE CPT archive row SHALL be
   evaluated AFTER the category, tag, author and date base rows and BEFORE the
   structure's post-shape row, and THE CPT single and CT term archive rows SHALL
   be evaluated AFTER the structure's post-shape row and BEFORE the `KindPage`
   row (D6).
2. THE classifier SHALL remain PURE and TOTAL, making no database read and no
   claim that a segment names a real item.
3. WHEN no registry entry is configured, THE classifier SHALL yield the same
   Target for every path as it does before M12.
4. WHEN a page's slug equals a registered CPT or CT slug, THE System SHALL route
   that URL to the CPT or CT, and the page SHALL be unreachable at that URL
   (documented consequence).
5. THE System SHALL register chi patterns for the new shapes only when the
   registry has entries, following M15's register-only-when-wired posture, and
   SHALL dispatch every one to `s.resolve` (`internal/web/router.go`).
6. IF the registry has entries but the wiring they require (the node reader or
   term hierarchy) is missing, THEN THE System SHALL panic at startup, as
   `requireArchiveDeps` does.
7. THE routing correctness SHALL NOT depend on chi registration order.
8. WHILE the permalink structure is `/%postname%/`, WHEN a request path equals
   the archive slug of a registered CPT that declares `has_archive`, THE System
   SHALL serve that CPT's archive at its archive slug.
9. WHEN a post or page slug equals a registered CPT archive slug, THE System
   SHALL route that single-segment URL to the archive, and the post or page
   SHALL be unreachable at that URL (documented consequence).

### Requirement 8: Capability and visibility for custom post types

**User Story:** As a site operator, I want anonymous visitors to see only
published custom content, so that drafts and private items stay private.

#### Acceptance Criteria

1. THE CPT public routes and REST collections SHALL list and serve rows with
   `post_status = 'publish'` only.
2. WHEN a non-published CPT row is requested by id as a single REST item, THE
   System SHALL serve it only to a caller holding the edit capability derived
   from the entry's `capability_type` (default `post`, the exact analogue of
   today's `edit_posts` check in `handleRESTPostSingle`,
   `internal/web/rest_posts.go`).
3. THE System SHALL derive the capability suffix from the registry entry through
   an `auth.CapabilitySuffix` helper extending `auth.capSuffix(postType)`
   (`internal/auth/policy.go`), SHALL keep `auth.capSuffix` behaviorally
   identical for `post` and `page`, and SHALL introduce NO new capability names.
4. IF the caller lacks the derived edit capability, THEN THE System SHALL respond
   `404 rest_post_invalid_id`, for anonymous and authenticated callers alike,
   byte-for-byte consistent with posts and pages today.
5. IF the requested id names a row of a different post type, THEN THE System
   SHALL respond 404.
6. WHEN a CPT row is published, THE System SHALL serve it to every caller.

### Requirement 9: REST read for custom post types

**User Story:** As an API client, I want custom post types under
`/wp-json/wp/v2/{rest_base}`, so that headless clients read custom content as
they read posts.

#### Acceptance Criteria

1. WHEN a client sends GET to `/wp-json/wp/v2/{rest_base}` for a registered CPT,
   THE System SHALL return the published collection in the posts/pages envelope
   with the same pagination headers.
2. WHEN a client sends GET to `/wp-json/wp/v2/{rest_base}/{id}`, THE System SHALL
   return the single item in the same envelope, subject to Requirement 8.
3. THE response `type` field SHALL equal the CPT name and the `link` field SHALL
   equal the CPT canonical from Requirement 4.
4. FOR EACH taxonomy attached to the CPT, THE response SHALL include an array of
   term IDs keyed by that taxonomy's `rest_base`.
5. WHEN a client sends POST, PUT, PATCH or DELETE to a CPT route, THE System
   SHALL answer 501 through `restNotImplemented`.
6. IF a client requests an item whose type does not match the route's CPT, THEN
   THE System SHALL answer 404.

### Requirement 10: REST read for custom taxonomies and route index

**User Story:** As an API client, I want custom taxonomy terms under their
`rest_base`, and the route index to list them, so that clients can discover and
read them.

#### Acceptance Criteria

1. WHEN a client sends GET to `/wp-json/wp/v2/{rest_base}` for a registered CT,
   THE System SHALL return terms in the categories/tags shape (id, count,
   description, link, name, slug, taxonomy, parent).
2. WHEN a client sends GET to `/wp-json/wp/v2/{rest_base}/{id}`, THE System SHALL
   return the single term in that shape, or 404 if the term belongs to another
   taxonomy.
3. THE term `link` field SHALL equal the CT canonical from Requirement 6.
4. WHEN a client sends POST, PUT, PATCH or DELETE to a CT route, THE System SHALL
   answer 501 through `restNotImplemented`.
5. WHEN a client requests `GET /wp-json/` or `GET /wp-json/wp/v2/`, THE System
   SHALL list each registered CPT and CT route in the route index, in addition
   to the entries `restRoutes()` lists today (`internal/web/rest_router.go`).
6. THE `/wp/v2/types` and `/wp/v2/taxonomies` discovery endpoints SHALL NOT be
   added by this milestone.

### Requirement 11: Diagnostic reporting in migrate -check

**User Story:** As a site operator, I want `grimoire-cli migrate -check` to tell
me which database types are not declared, so that I can decide what to expose
without anything being exposed for me.

#### Acceptance Criteria

1. WHEN `grimoire-cli migrate -check` runs, THE CLI SHALL report, read-only, each
   distinct `post_type` in `{prefix}posts` that is neither built in nor declared
   in configuration, naming the config key (`content.post_types`) to add.
2. WHEN `grimoire-cli migrate -check` runs, THE CLI SHALL report each distinct
   taxonomy in `{prefix}term_taxonomy` that is neither built in nor declared,
   naming `content.taxonomies`.
3. THE report SHALL be informational and SHALL NOT change the registry, the
   exit status for an otherwise clean check, or any served route.
4. WHEN a declared type has no database rows, THE CLI SHALL note it in the
   report.

### Requirement 12: Write verbs remain unsupported

**User Story:** As a maintainer, I want the write half clearly deferred, so that
M12 cannot half-implement writes that 12.C will specify properly.

#### Acceptance Criteria

1. WHEN any POST, PUT, PATCH or DELETE request targets a CPT or CT REST route,
   THE System SHALL answer 501 and SHALL write nothing.
2. WHEN the admin API receives a write for a post type other than `post` or
   `page`, THE System SHALL keep rejecting it (`internal/web/adminapi_posts.go:103`).
3. THE admin SPA SHALL receive NO UI change in this milestone.

### Requirement 13: Production server assembly is a testable function

**User Story:** As a maintainer, I want the server wiring pulled out of `main()`,
so that a test can catch a service that was built but never wired.

M15 shipped with `Server.WithPages` and `RESTMapper.WithPages` never called from
`cmd/grimoire/main.go`, so nested pages returned 404 in the real binary while
every test passed, because each test builds its own Server; this was fixed in
PR #74.

#### Acceptance Criteria

1. THE System SHALL move the construction of the web Server and the REST mapper
   out of `main()` into a function in package `cmd/grimoire` that takes the
   already-opened repositories, the loaded config, the resolved permalink
   Structure and the built registry, and returns the HTTP handler.
2. THE `main()` function SHALL call that function and assemble the Server nowhere
   else.
3. THE `cmd/grimoire` tests SHALL call that function over a migrated SQLite
   database seeded with a nested page and a registered custom post type and
   taxonomy, and SHALL assert through the returned handler that a nested page URL
   returns 200, a custom post type single returns 200, a custom taxonomy term
   archive returns 200, and `/wp-json/wp/v2/{rest_base}` for the registered type
   returns 200, so that removing any `WithPages` or `WithRegistry` call from the
   assembly fails `go test`.
4. THE extraction SHALL change no behavior: the existing tests and the
   real-binary behavior are identical.

### Requirement 14: Test coverage and quality gates

**User Story:** As a maintainer, I want every behavior proven, so that custom
type support cannot silently regress existing routing.

#### Acceptance Criteria

1. THE node-reader port SHALL be covered by a cross-vendor `storagetest` contract
   (SQLite always; MySQL/Postgres env-gated through the existing harness)
   proving stable order, completeness, and correct ancestry, including orphan and
   cycle degradation.
2. THE routing tests SHALL cover the new Kinds, precedence against built-in
   bases and `KindPage`, slug collisions, and the path constructors as fixed
   points.
3. THE config tests SHALL cover every default in Requirement 1 and every
   rejection in Requirement 3.
4. THE content and handler tests SHALL cover CPT single, CPT archive, and CT
   archive for 200, 301 and 404, with query-string preservation across every 301.
5. THE REST tests SHALL cover CPT and CT collection, single, 501 for write verbs,
   and the route index.
6. THE milestone SHALL include at least one fixture-based test against an
   imported real-WordPress-database export containing a CPT and a CT, gated by
   `GRIMOIRE_TEST_WP_DSN`, mirroring `realdb_pages_test.go`.
7. IF the fixture lacks a CPT or CT, THEN THE test SHALL skip with a message
   naming the missing precondition.
8. THE tests SHALL add NO new dependency, and `gofmt`, `go vet`, `go build ./...`
   and `go test ./...` SHALL be green at the end of every task.

## Out of scope

- **12.C write parity.** REST and admin create, update and delete of CPTs and
  CTs belong to a separate later spec, as does any admin SPA UI for CPTs.
- **Auto-discovery or auto-exposure of types from database rows, and executing
  plugin or theme PHP.** Both are permanent exclusions (roadmap Req 25.1; D2).
- **`/wp/v2/types` and `/wp/v2/taxonomies` discovery endpoints** (candidate
  follow-up).
- **CPT date archives, feeds, and author-archive inclusion of CPTs.**
- **Meta and custom fields** (`register_meta`, ACF), custom statuses,
  `show_in_rest` per-field controllers, and custom capability maps beyond the
  `capability_type` suffix family.
- **Term meta, taxonomy-specific term writes, and M13-style re-parenting for
  custom taxonomies** (the write half).
- **Any migration, column, table or new capability name** (D9).

## Traceability

This spec refines roadmap Requirements 23, 24 and 25 and task groups 12.A and
12.B (`../wordpress-core-parity-roadmap/requirements.md`,
`../wordpress-core-parity-roadmap/tasks.md`); group 12.C is deferred to a later
spec. It records the compatibility-boundary expansion that roadmap Requirement
25.3 requires, and it is the M12 entry in the post-M10 gap analysis
(`../post-m10-gap-analysis/README.md`, "M12 — Custom post types and custom
taxonomies"). It builds on the M8/M9a/M9b archive and permalink machinery
(`internal/routing/classify.go`, `internal/routing/routing.go`,
`internal/content/term.go`, `internal/content/archive.go`), M15's nested-page
machinery (`KindPage`, `page()`, `ListPages`), the M5/M10.B REST handlers
(`internal/web/rest_posts.go`, `internal/web/rest_terms.go`), M9b's
`TermHierarchy`, and the real-database fixture pattern in
`internal/routing/realdb_pages_test.go`.

| Requirement | Roadmap / source |
|---|---|
| 1, 2 | Roadmap 23.1, 24.1; 12.A |
| 3 | Roadmap 23.1, 24.1; D4 |
| 4, 5 | Roadmap 23.2; 12.A |
| 6 | Roadmap 24.2, 24.4; 12.A |
| 7 | Roadmap 23.2, 24.2; D6 |
| 8 | Roadmap 23.4; D7 |
| 9, 10 | Roadmap 23.3, 24.3; 12.B |
| 11 | Roadmap 25.1; D2 |
| 12 | D1; roadmap 12.C deferral |
| 13 | M15 wiring regression (PR #74); D10 |
| 14 | Roadmap 25.2; D10 |
