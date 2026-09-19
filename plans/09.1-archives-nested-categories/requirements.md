# M9b — Archives & Nested Categories: Requirements

## Introduction

grimoire serves exactly one archive route: the flat, single-segment
`/category/{slug}` registered at `internal/web/router.go`. There is no tag
archive, no date archive and no author archive, and a category's place in the
taxonomy hierarchy is invisible — `domain.Term` (`internal/domain/entities.go`)
carries only `ID`, `Name`, `Slug` and `Taxonomy`, and no query in
`internal/storage/wprepo` reads `term_taxonomy.parent`.

That last gap is the sharpest one. The WordPress database this project is
developed against — the `accuweaverllc/scripts` podman stack M9a validated
against — has **33 categories, 28 of which have `parent <> 0`**. Flat-only
category archives therefore misrepresent the great majority of that site's
taxonomy: every nested category is served as if it were top-level, its URL
omits its ancestry, and a parent category's archive silently excludes the posts
its children hold.

This milestone refines groups **9.C**, **9.D** and **9.E** of
[`../wordpress-core-parity-roadmap`](../wordpress-core-parity-roadmap) into
implementable requirements. It is the follow-on that
[`../09-permalinks-canonical-routing`](../09-permalinks-canonical-routing) (M9a)
deferred these groups to, and it inherits two decisions M9a already resolved on
its behalf rather than relitigating them (see "Inherited decisions").

It also finishes a job M9a deliberately left half-done. `category_base` and
`tag_base` are already parsed and exposed on `routing.Structure`, but **no route
honors either one**, because the category archive stayed flat. M9a's own docs and
`grimoire-cli migrate -check` say so on purpose today; this milestone is where
those options take effect, and where those statements have to be corrected.

Traces to roadmap Requirements 12, 13 and 14.

## Inherited decisions

Both were resolved in M9a's `requirements.md` "Out of scope" table and are
treated here as settled input, not open questions:

| Decision | Consequence for this milestone |
|---|---|
| A parent category's archive **does** include posts from descendant categories. | Requires a descendant-aware listing **and** count so M8's pagination totals stay correct (Requirement 3). |
| Nested category routes are **canonical**; the flat `/category/{slug}` `301`s to the nested path. | Extends M9a's one-canonical-URL-per-resource model from posts to category archives (Requirement 2). |

## Resolved decisions

Three questions were open when this spec was first written. The project lead has
answered all three, and the answers are now acceptance criteria rather than
recommendations. The table records where each landed so the reasoning is
traceable from the decision to the criterion that implements it.

| Question | Answer | Criteria |
|---|---|---|
| Do archives inherit a permalink "front" — the structure's leading literal segment(s), e.g. `blog` in `/blog/%year%/%monthnum%/%postname%/`? | **Yes, replicating WordPress's asymmetry.** The front is prepended to date and author archives always, and to category and tag archives only when the corresponding base option is unset. | 4.7–4.10 |
| What happens to date archives when `%post_id%` leads the structure? | **Replicate WordPress's `date/` disambiguation prefix.** When `%post_id%` appears among the structure's first three tokens, date archives move under an extra `date/` segment. | 6.7 |
| Does the author archive need a `user_nicename` uniqueness assumption? | **No assumption; deterministic resolution instead.** `ByNicename` resolves to the lowest `ID`, and `grimoire-cli migrate -check` reports duplicates so the condition is discoverable before serving. | 7.6–7.8 |

## Requirements

### Requirement 1 — Read the taxonomy hierarchy

**User Story:** As a site owner whose categories are nested in WordPress, I want
grimoire to see that nesting, so my hierarchy is not flattened away.

#### Acceptance Criteria

1. `domain.Term` SHALL gain a `ParentID int64` field, sourced from
   `term_taxonomy.parent`, where `0` means "no parent" — matching both
   WordPress's own sentinel and the zero-value-means-unset convention already
   used by `domain.Post.ParentID` and `domain.MediaFilter.ParentID`.
2. Every existing read that returns a `domain.Term` SHALL populate `ParentID`:
   `TermRepo.BySlug`, `TermRepo.ListByTaxonomy` and `TermRepo.TermsByIDs` in
   `internal/storage/wprepo/repo.go`. A term read through one path and the same
   term read through another SHALL NOT disagree about its parent.
3. THE system SHALL require **no schema change** to do this.
   `term_taxonomy.parent` already exists in every vendor's
   `0001_init.up.sql` and is already populated in real WordPress databases; the
   task list SHALL verify explicitly that no new migration file is added.
4. WHEN a term's `parent` refers to a term that does not exist THE system SHALL
   treat that term as top-level from that point up, rather than erroring.
   Imported databases contain orphaned rows, and a broken hierarchy SHALL
   degrade to a shorter path rather than a `500`.
5. IF the parent chain contains a cycle THEN THE system SHALL terminate the walk
   on the first already-visited term. A corrupted hierarchy SHALL NOT hang a
   request.
6. THE resolved ancestry and descendant sets SHALL be derived from a single read
   of the taxonomy's terms per request, not one query per level. Caching is
   explicitly not introduced: unlike M9a's permalink options, terms are mutable
   at runtime through M6's `TermWriteService`, so a cache would need
   invalidation this milestone does not need to own.

### Requirement 2 — Nested category archive routes are canonical

**User Story:** As a site visitor, I want `/category/news/local` to work, and I
want the one URL for a category to be the one that reflects its place in the
hierarchy.

#### Acceptance Criteria

1. THE system SHALL serve a category archive at the path formed by the archive
   base segment followed by the category's full ancestry, root first, ending in
   the category's own slug — for example `/category/news/local` for "Local"
   nested under "News".
2. THE canonical path for a category SHALL be constructed in exactly one place,
   `routing.Structure`, alongside `Structure.Canonical` — so the redirect
   target, the theme's pagination links and the REST `link` field cannot
   diverge. This preserves the single-construction-site property M9a
   established.
3. WHEN a request arrives at a category path whose final segment resolves to a
   category but whose preceding segments are not that category's ancestry THE
   system SHALL respond `301` with `Location` set to the canonical nested path.
   This covers the flat `/category/{slug}` case, which is simply the zero-ancestor
   instance of it, and a wrong-ancestor path such as `/category/sport/local`.
4. WHEN a category is top-level THE canonical path SHALL equal the flat path,
   and the request SHALL render `200` with no redirect. The canonical path SHALL
   be a fixed point for every category, and this SHALL have an explicit test.
5. WHEN the final path segment resolves to no category in the `category`
   taxonomy THE system SHALL respond `404`.
6. WHEN a category exists but has no published posts THE system SHALL render an
   empty archive with `200`, not `404` — matching today's behavior and roadmap
   AC 12.4.
7. THE trailing-slash form of a category path SHALL follow the same rule M9a
   applies to posts: whichever form `permalink_structure` implies is canonical
   and the other `301`s to it. WHEN `permalink_structure` is empty the canonical
   category path SHALL have no trailing slash, preserving today's
   `/category/{slug}` exactly.
8. A request for the bare base segment (`/category`, `/category/`) SHALL respond
   `404`. WordPress serves no such route, and inventing a category index here
   would be a surface this milestone has not specified.
9. Redirects SHALL be `301` and SHALL preserve the query string, matching M9a.

### Requirement 3 — Category archives include descendants

**User Story:** As a site visitor browsing "News", I want to see the posts filed
under "News > Local" too, the way WordPress shows them.

#### Acceptance Criteria

1. THE category archive for a category SHALL list published posts assigned to
   that category **or to any of its descendants**, newest first.
2. THE pagination total for that archive SHALL count the same descendant-inclusive
   set, so `Page.Total` and `Page.TotalPages` describe the rows actually listed.
   A page number past the last page SHALL `404`, unchanged from M8.
3. A post assigned to both a parent and one of its descendants SHALL appear
   **once** in the listing and be counted **once** in the total. The join from
   posts to terms multiplies rows, so both the listing and the count SHALL be
   distinct on post ID.
4. THE descendant-inclusive read SHALL filter to `post_status='publish'`, and
   that filter SHALL NOT be expressible as "off" by any caller. The public read
   guarantee documented in `docs/compatibility.md` applies unchanged.
5. THE archive SHALL list `post_type='post'` rows only, matching
   `PostRepository.RecentPosts` and WordPress's own archive queries.

### Requirement 4 — `category_base` and `tag_base` take effect

**User Story:** As a site owner who renamed my category base in WordPress, I want
grimoire to serve my archives at that base rather than at a hard-coded one.

#### Acceptance Criteria

1. THE category archive base segment SHALL be `Structure.CategoryBase` —
   already resolved by `routing.Parse` from the `category_base` option, already
   defaulting to `category` (`routing.DefaultCategoryBase`).
2. THE tag archive base segment SHALL be `Structure.TagBase`, defaulting to
   `tag` (`routing.DefaultTagBase`).
3. THE author archive base segment SHALL be the literal `author`. WordPress
   stores no `author_base` option — it is a `WP_Rewrite` property, not a row in
   `{prefix}options` — so there is nothing to read and nothing to make
   configurable here.
4. IF `category_base` and `tag_base` resolve to the same segment, or either
   resolves to `author`, THEN THE system SHALL keep the *category* meaning for
   the colliding segment and emit a startup `WARN` naming the collision.
   Degrading loudly matches M9a Requirement 4's posture: grimoire reads a
   database it does not own.
5. THE collision from 4.4 SHALL NOT be reported through `routing.Parse`'s error
   return. M9a gives that error one meaning — "unsupported structure, fall back
   to the flat route" — and a renamed base is no reason to stop serving
   permalinks. It SHALL be surfaced as a separate, non-fatal diagnostic on the
   returned `Structure` that the startup path and `migrate -check` both read.
6. `grimoire-cli migrate -check` SHALL report the resolved category and tag
   bases, and the collision when one exists. Its current source comment and
   output deliberately omit the bases on the grounds that no route honors
   either; that reasoning expires with this milestone and SHALL be corrected
   rather than left in place.
7. THE system SHALL prepend the permalink structure's **front** — its leading
   literal segment(s), such as `blog` in
   `/blog/%year%/%monthnum%/%postname%/` — to **date archives and author
   archives, always**. `routing.Parse` already accepts literal segments, so
   such a structure is already supported; nothing consumes its leading literals
   yet.
8. THE system SHALL prepend the front to **category and tag archives only when
   the corresponding base option is unset**. IF `category_base` is set THEN the
   category archive path SHALL NOT carry the front; likewise `tag_base` and the
   tag archive path. This replicates WordPress's own asymmetry
   (`create_initial_taxonomies()` registers both taxonomies with
   `with_front => ! get_option( '{taxonomy}_base' )`), and the point of the
   milestone is that an existing site's published URLs keep working.
9. "Unset" SHALL mean the option value is empty, matching WordPress's own
   truthiness test on `get_option()` and `content.OptionService`'s documented
   mapping of an absent option to the empty string. A base option set
   explicitly to the **same string as the default** (`category_base` =
   `category`) therefore counts as **set**, and drops the front. THE system
   SHALL therefore distinguish "this base is the default because no option was
   provided" from "this base was explicitly provided and happens to equal the
   default" — a distinction `routing.Parse` cannot currently express, because it
   collapses an empty base to the default and keeps no record of which case it
   saw.
10. WordPress's `with_front` condition carries a second disjunct,
    `|| $wp_rewrite->using_index_permalinks()`, which restores the front for
    `/index.php/...`-style permalinks. grimoire serves no index permalinks —
    M9a's token set and `ChiPatterns` have no such form — so that disjunct is
    unreachable here and SHALL NOT be implemented. THE reduction SHALL be stated
    in the design rather than left as a silent omission.

### Requirement 5 — Tag archives

**User Story:** As a site visitor, I want to browse posts by tag the way I can
browse by category.

#### Acceptance Criteria

1. THE system SHALL serve a tag archive at `/{TagBase}/{slug}`, listing
   published posts related to that `post_tag` term, newest first.
2. Tags SHALL be treated as flat. `post_tag` is non-hierarchical in WordPress,
   so no ancestry path and no descendant inclusion apply, and
   `term_taxonomy.parent` SHALL be ignored for this taxonomy.
3. WHEN the slug resolves to no `post_tag` term THE system SHALL respond `404`.
4. WHEN the tag exists but has no published posts THE system SHALL render an
   empty archive with `200` (roadmap AC 12.4).
5. THE tag archive SHALL render through the `tag` template kind, which M9a
   already registered in `render.hierarchy` as `tag` → `archive` → `index`. No
   template-resolution change is in scope.

### Requirement 6 — Date archives

**User Story:** As a site visitor, I want `/2024/`, `/2024/05/` and
`/2024/05/17/` to list what was published then.

#### Acceptance Criteria

1. THE system SHALL serve date archives at three granularities: year
   (`/{YYYY}`), year/month (`/{YYYY}/{MM}`) and year/month/day
   (`/{YYYY}/{MM}/{DD}`), each listing published posts in that range, newest
   first.
2. Date components SHALL use WordPress's widths — 4-digit year, zero-padded
   2-digit month and day — reusing the shape rules `routing.Structure.Match`
   already enforces. A 1-digit month SHALL NOT match.
3. IF the components do not form a real calendar date (for example `2024/02/30`,
   `2024/13/01`) THEN THE system SHALL respond `404` rather than querying.
4. THE date range SHALL be applied as a half-open `[start, end)` comparison on
   `post_date`, not through a vendor-specific date function, following the
   precedent already set by `domain.MediaFilter`'s `After`/`Before` and
   `formatTS` in `internal/storage/wprepo`. Dates SHALL be compared in the
   post's stored local time with no timezone conversion, for the same reason
   M9a's `Canonical` does not convert: shifting to UTC would move a post
   published just after local midnight into the previous day.
5. WHEN a date archive matches zero published posts THE system SHALL render an
   empty archive with `200`. There is no entity to not-exist, so `404` has no
   meaning here.
6. THE date archive SHALL render through the `date` template kind already
   registered by M9a.
7. WHEN `%post_id%` appears among the permalink structure's **first three
   tokens** THE system SHALL serve date archives under an additional `date/`
   segment, placed after the front (Requirement 4.7) and before the date
   components — so a bare `/%post_id%/` structure yields `/date/2024`,
   `/date/2024/05` and `/date/2024/05/17`. This replicates WordPress's own
   disambiguation (`WP_Rewrite::get_date_permastruct()` moves the date structure
   to `$front . 'date/'` when it finds `%post_id%` at token index ≤ 3) and
   exists to prevent a concrete collision: without it, `/2024` under a bare
   `/%post_id%/` structure classifies as a year archive (Requirement 9.3) and
   post 2024 becomes unreachable by its own canonical permalink. THE `date/`
   segment SHALL NOT be added for any other structure, so the common cases
   (`/%postname%/`, `/%year%/%monthnum%/%day%/%postname%/`) keep serving date
   archives at their bare paths exactly as WordPress does.

### Requirement 7 — Author archives

**User Story:** As a site visitor, I want to browse everything one author
published.

#### Acceptance Criteria

1. THE system SHALL serve an author archive at `/author/{nicename}`, resolving
   the author by `user_nicename`, listing that author's published posts newest
   first.
2. `domain.UserRepository` SHALL gain a `ByNicename(ctx, nicename) (User, error)`
   read returning `domain.ErrNotFound` when absent. The interface currently has
   `ByLogin`, `ByID`, `Create`, `UpdatePass`, `List` and `Count`, and no
   nicename lookup exists.
3. WHEN the nicename resolves to no user THE system SHALL respond `404`; when it
   resolves to a user with no published posts THE system SHALL render an empty
   archive with `200` (roadmap AC 12.4).
4. THE archive heading SHALL use the author's `display_name`. `user_login` SHALL
   NOT be rendered, so the archive does not publish a login name the site had
   not already exposed.
5. THE REST `link` field for a user SHALL become `/author/{nicename}`.
   `content.rest.go`'s `userLink` currently returns the plain-permalink
   fallback `/?author={id}`, which M9a Requirement 6.4 kept **explicitly
   because no author route existed yet**. That condition ends here.
6. WHEN more than one user row shares a `user_nicename` THE system SHALL
   resolve `ByNicename` **deterministically to the lowest `ID`**, and SHALL NOT
   treat the duplicate as an error. `user_nicename` carries a **non-unique**
   index in WordPress's schema (verified against the live WordPress database:
   `Non_unique = 1`), so duplicates are permitted by the schema even though
   `wp_insert_user()` prevents them on write by appending numeric suffixes
   (`alice`, `alice-2`). Any path that bypasses `wp_insert_user` — direct SQL,
   imports and migrations, multisite merges, pre-suffix-era WordPress, plugins —
   can produce them. This is a deliberate divergence from WordPress, whose own
   read is arbitrary, and SHALL be documented as one (Requirement 13.5).
7. THE impact of 7.6 SHALL be the same as WordPress's and SHALL be stated
   rather than implied: one `/author/{nicename}` URL can surface only one of the
   colliding authors, so the other's posts are unreachable by that route. There
   is no error and no wrong data — one author is invisible at that URL.
8. `grimoire-cli migrate -check` SHALL report duplicate `user_nicename` values,
   naming each duplicated nicename, how many rows share it, and which `ID` wins
   under 7.6 — so the condition is discoverable before the site serves rather
   than leaving an author silently unreachable. This matches the posture M9a
   took for unsupported permalink structures (Requirement 4.6): `-check` is the
   surface an operator runs first. THE check SHALL be read-only and SHALL run
   once. THE system SHALL NOT perform a per-request duplicate check on the
   author archive route: that would add a `COUNT` to every author archive hit
   for a condition that is nearly always absent, and the reporting path already
   makes it discoverable.

### Requirement 8 — Pagination on every new archive

#### Acceptance Criteria

1. Every archive route SHALL return M8's `content.Page` contract
   (`Page`, `PerPage`, `Total`, `TotalPages`) built by the existing
   `newPage`/`clamp`/`TotalPages` helpers in `internal/content/pagination.go`.
   No second pagination shape SHALL be introduced.
2. THE page selector SHALL be the existing `?page=N` query parameter read by
   `web.pageParam`, with `content.DefaultPerPage` as the page size — identical
   to the home and category pages.
3. WHEN `page` exceeds `TotalPages` AND `Total` is greater than zero THE system
   SHALL respond `404`, matching the rule the home and category handlers
   already apply.
4. Pagination links rendered by the theme SHALL be built from the archive's
   canonical path (Requirement 2.2), not by string-concatenating a base. The
   existing `category.tmpl` hard-codes `/category/{{.Term.Slug}}?page=`, which
   becomes wrong the moment a category is nested or `category_base` is
   overridden, and SHALL be corrected.

### Requirement 9 — Route precedence is decided in one tested place

**User Story:** As a maintainer, I want to know which route wins when two shapes
collide, without reading chi's internals.

#### Acceptance Criteria

1. THE new routes SHALL be registered through `internal/routing` and the
   existing `internal/web` chi router. No second routing or dispatch mechanism
   SHALL be introduced.
2. WHERE two path shapes are ambiguous — a date archive whose segment count and
   parameter shape match the configured permalink structure, or a single numeric
   segment that also matches the flat `/{slug}` route — THE precedence SHALL be
   decided by a pure, exported classifier in `internal/routing`, not by chi
   registration order. chi resolves a parameter-node collision silently in
   favour of whichever pattern was registered last rather than panicking (as
   M9a discovered and recorded in `router.go`'s route-registration comment), so
   precedence that depends on registration order is unreviewable.
3. THE classifier SHALL resolve a date archive **before** a post, matching
   WordPress's own rewrite-rule ordering. THE consequence — that a post whose
   slug is a bare 4-digit number is unreachable under a `/%postname%/`
   structure, and a post slugged with a bare 2-digit number is unreachable at
   the day position of a `/%year%/%monthnum%/%postname%/` structure — SHALL be
   documented in `docs/compatibility.md` as matching WordPress rather than left
   to be discovered.
4. THE classifier SHALL resolve a path whose first segment is the category base,
   the tag base or `author` as that archive kind, ahead of any post
   interpretation. THE consequence — that a post slugged identically to one of
   those base segments is unreachable under a single-segment structure — SHALL
   likewise be documented.
5. Nested category paths have a variable segment count, which a chi pattern
   cannot express. THE route registration SHALL use a wildcard rooted at the
   base segment, and the handler SHALL derive the segments from `r.URL.Path`,
   the same approach `web.resolveSingle` already takes via
   `Structure.ParamsFromPath` for exactly this reason.
6. Registering the new routes SHALL NOT change the behavior of any existing
   route: `/`, `/login`, `/comment`, `/healthz`, `/admin/*`, `/wp-json/*`,
   `/wp-content/uploads/*`, the permalink patterns and the flat `/{slug}`
   fallback. THE task list SHALL assert this with tests, not by inspection.

### Requirement 10 — REST parity for the new surfaces

#### Acceptance Criteria

1. `restTerm.Parent` in `internal/web/rest_terms.go` is currently hard-coded to
   `0`. It SHALL carry `Term.ParentID`, so a REST consumer sees the same
   hierarchy the archive routes serve.
2. `restTermLink` currently hard-codes `restAbs(r, "/category/"+slug)` for
   categories and `/?tag=` for tags. Both SHALL be built from
   `routing.Structure`'s archive-path constructors, so an advertised link is a
   URL that returns `200` rather than one that `301`s or `404`s.
3. `userLink` SHALL return the author archive path (Requirement 7.5).
4. `commentLink` SHALL remain on its `/?p={id}#comment-{id}` fallback. No route
   is added for it in this milestone, so changing it would advertise a URL that
   `404`s — the same reasoning M9a applied.

### Requirement 11 — Theme data for the new archives

#### Acceptance Criteria

1. THE render layer SHALL supply the new archive kinds with a data shape
   carrying the archive heading, the canonical base path for pagination links,
   the post list and the `content.Page`.
2. `render.CategoryData` SHALL keep working for any theme that references
   `.Term.Name` or `.Posts`, and `themes/default/templates/category.tmpl` SHALL
   keep rendering. A field-name change that silently breaks a theme's template
   execution is not acceptable, since `html/template` resolves fields at
   execution time and the failure would surface as a `500` on a page that
   previously worked.
3. `themes/default/templates/archive.tmpl` — today a heading of the literal word
   "Archive", a post grid and no pagination — SHALL render the supplied heading
   and pagination, since `tag`, `author` and `date` all fall back to it and none
   of the three has its own template in the default theme.
4. No new template-resolution mechanism SHALL be added. The `hierarchy` map in
   `internal/render/engine.go` already resolves all four kinds, courtesy of M9a.

### Requirement 12 — Test coverage (9.E)

#### Acceptance Criteria

1. `Term.ParentID` SHALL be covered by the cross-vendor contract suite in
   `internal/storage/storagetest`, exercised on SQLite, MySQL and Postgres, for
   each of the three term reads named in Requirement 1.2. `SeedFixtures`
   currently inserts every `term_taxonomy` row with `parent` `0`, so the fixture
   SHALL gain a nested category — a parent, a child and a grandchild — to make
   the assertion meaningful.
2. THE descendant-inclusive listing and count SHALL be covered by cross-vendor
   contract cases, including the duplicate-assignment case from Requirement 3.3
   and the unpublished-post exclusion from Requirement 3.4.
3. `UserRepository.ByNicename` SHALL be covered by a cross-vendor contract case,
   mirroring how `ByLogin` is already covered.
4. Path classification, ancestry-path construction and date-range derivation
   SHALL be covered by pure unit tests in `internal/routing` with no database,
   including every ambiguous shape named in Requirement 9 and the canonical
   fixed-point property from Requirement 2.4.
5. Each archive route SHALL have handler tests asserting exact status codes and
   `Location` values for: canonical path `200`, flat-to-nested `301`,
   wrong-ancestor `301`, wrong-trailing-slash `301`, unknown entity `404`,
   existing-but-empty `200`, out-of-range page `404`, and impossible date `404`.
6. THE nested-category half of the real-WordPress-database check SHALL be added
   by **extending the existing env-gated tests** —
   `internal/routing/realdb_test.go` and
   `test/e2e/m9_permalinks_realdb_test.go`, both gated on
   `GRIMOIRE_TEST_WP_DSN` with the prefix from `GRIMOIRE_TEST_WP_PREFIX`. No new
   gating mechanism SHALL be introduced, so one DSN continues to enable every
   real-database check in the repo. The check SHALL read the target site's own
   `category_base` and hierarchy rather than assuming either, and SHALL derive
   expected paths independently of the constructor under test, as M9a's checks
   do.
7. IF the target database has no nested category THEN the nested-category
   assertions SHALL **skip** with a message naming the precondition, rather than
   pass. A check that reports success for a claim it never exercised is worse
   than no check.
8. No new migration SHALL be added (Requirement 1.3). The task list SHALL
   verify this explicitly, as M8's and M9a's did.
9. THE front's asymmetry (Requirement 4.7–4.9) and the `date/` disambiguation
   prefix (Requirement 6.7) SHALL be covered by pure unit tests in
   `internal/routing`: a structure with a front yields front-prefixed date and
   author paths; the same structure yields a front-prefixed category path with
   `category_base` unset and an unprefixed one with `category_base` set
   explicitly — **including when it is set to the default string**, which is the
   case that distinguishes 4.9 from a naive implementation; and a
   `%post_id%`-leading structure yields `date/`-prefixed date paths while a
   `%postname%`-leading one does not.
10. Duplicate-`user_nicename` resolution SHALL be covered on both surfaces: a
    cross-vendor `storagetest` case seeding two users with the same nicename and
    asserting the lowest `ID` wins on SQLite, MySQL and Postgres (Requirement
    7.6), and a test asserting `migrate -check` reports the duplicate
    (Requirement 7.8).

### Requirement 13 — Correct the statements this milestone invalidates

**User Story:** As someone evaluating grimoire, I want the compatibility docs to
describe what it does now, not what it did one milestone ago.

#### Acceptance Criteria

1. `docs/compatibility.md`'s M9a section SHALL be updated: the three
   still-open items "No tag, date or author archive routes", "No nested category
   paths" and "`category_base` and `tag_base` are read and resolved but not yet
   honored by any route" SHALL move into "What's implemented", and the new
   divergences from Requirement 9.3/9.4 SHALL be recorded.
2. THE root `README.md` "Status" section and its M9-remainder bullet SHALL be
   updated; both currently name these gaps.
3. `cmd/grimoire-cli/permalinks.go`'s comment explaining why the resolved bases
   are deliberately **not** reported SHALL be replaced along with the behavior
   (Requirement 4.6).
4. `../wordpress-core-parity-roadmap/tasks.md` SHALL have groups 9.C, 9.D and
   9.E ticked with a note naming what shipped, and `../README.md`'s milestone
   index SHALL gain this milestone's row. Checkboxes SHALL be ticked as the work
   lands, not retroactively.
5. `docs/compatibility.md` SHALL record the duplicate-`user_nicename`
   divergence: WordPress resolves a duplicate arbitrarily, grimoire resolves it
   to the lowest `ID`, the impact is identical either way (one of the colliding
   authors is unreachable at that URL), and `grimoire-cli migrate -check`
   reports the condition.

## Out of scope

- **Arbitrary custom taxonomies** beyond `category` and `post_tag`, and custom
  post types — unchanged from the roadmap's compatibility boundary.
- **Plugin-registered rewrite rules**, and the `%category%`/`%author%` permalink
  tokens. Those tokens require resolving a term or user *during* post-path
  matching; this milestone resolves terms and users for their own archive
  routes, which is a strictly smaller problem, and it does not change M9a's
  token set.
- **WordPress's `/page/{n}/` pagination paths.** grimoire keeps `?page=N`
  (Requirement 8.2). Adopting path-based pagination would collide with nested
  category slugs — `/category/news/page/2` is indistinguishable from a child
  category slugged `page` — so it would require reserving `page` as a category
  slug. That is a deliberate divergence to document, not to fix here.
- **Hierarchical *page* URLs.** WordPress nests pages under their parent page
  via `post_parent`. M9a already documents the related divergence (grimoire
  applies the permalink structure to `page` rows, where WordPress exempts them);
  this milestone nests *categories*, not pages, and does not change that.
- **Slug- and entity-specific templates** (`category-{slug}.tmpl`,
  `tag-{slug}.tmpl`, `author-{nicename}.tmpl`). Ruled out in M9a and recorded
  under "Resolved decisions" in [`../README.md`](../README.md); the existing
  mechanism makes them cheap to add if an in-tree theme ever needs one.
- **Term-hierarchy *editing*.** M6's `TermWriteService` creates and renames
  terms without setting a parent, and it stays that way: this milestone adds a
  read path. Making `ParentID` writable is a separate, capability-gated change.
- **Caching the term graph.** Deliberately excluded by Requirement 1.6.
- **Index permalinks** (`/index.php/%postname%/`). grimoire has never served
  them, so WordPress's `using_index_permalinks()` disjunct on `with_front` is
  unreachable here (Requirement 4.10).
- **Per-request duplicate-`user_nicename` detection.** Reporting moves to
  `migrate -check` (Requirement 7.8); the request path stays a single
  `ORDER BY ID ASC LIMIT 1` read with no extra `COUNT`.
- **De-duplicating `user_nicename`.** grimoire reads a database it does not own;
  it reports the condition and resolves it deterministically rather than
  rewriting user rows.
