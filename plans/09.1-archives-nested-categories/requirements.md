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

Three further questions were raised by the spec review on PR #51 — each one an
assumption the first draft made silently rather than a question it asked. The
project lead has answered all three, and they are acceptance criteria here too:

| Question | Answer | Criteria |
|---|---|---|
| How is a category archive resolved: leaf slug first, or the whole segment path? | **Walk the full segment path through the taxonomy graph.** The handler already loads the whole taxonomy per category request; matching segment-by-segment through that graph removes the unstated dependency on term slugs being unique within a taxonomy and drops `TermRepo.BySlug` from the category path. Tags still resolve by slug — `post_tag` is flat, so there is no path to walk — and that resolution is made deterministic instead. | 1.7, 2.10, 2.11, 5.6 |
| Do date archives apply when `permalink_structure` is empty? | **No — they are gated off entirely.** WordPress with plain permalinks registers no rewrite rules at all; its date archives are `?m=2024`, not `/2024`. Gating is therefore the WordPress-faithful choice *and* it preserves Requirement 9.6 and today's flat `/{slug}` behavior, under which `/2024` serves a post slugged `2024`. | 6.8, 9.8 |
| What wins when a base segment equals a leading literal of the permalink structure? | **The post interpretation, with a loud `Notes()` entry.** `category_base` = `archives` against WordPress's Numeric preset `/archives/%post_id%` would otherwise classify `/archives/123` as a category, resolve nothing, and `404` every post URL on the site. The milestone's premise is that an existing site's published URLs keep working, so a post URL is not sacrificed to an archive base. | 4.4, 9.7 |

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
   `internal/storage/wprepo/repo.go`. THE agreement guarantee — a term read
   through one path and the same term read through another SHALL NOT disagree
   about its parent — applies to the two **taxonomy-scoped** reads, `BySlug` and
   `ListByTaxonomy`, and SHALL NOT be claimed for `TermsByIDs`.
   `TermsByIDs` joins `term_taxonomy` on `term_id` alone
   (`internal/storage/wprepo/repo.go:264-272`) and the table is unique on
   `(term_id, taxonomy)`, so a `term_id` registered in both `category` and
   `post_tag` yields two rows with different `taxonomy` **and** different
   `parent`. `ParentID` is a property of the `(term_id, taxonomy)` pair, and
   `TermsByIDs`'s signature cannot express which pair the caller means.
   THE requirement is therefore **narrowed** rather than given a deterministic
   tie-break, for two reasons: a rule such as "lowest `term_taxonomy_id` wins"
   would read as a guarantee about a value that is simply the wrong term's
   parent, and no path in this milestone reads `ParentID` through `TermsByIDs` —
   category ancestry and descendants come from `ListByTaxonomy` (Requirement
   1.6, 2.10). `TermsByIDs` SHALL still select `parent`, and its doc comment
   SHALL state the ambiguity for shared term IDs so a future caller does not
   assume otherwise.
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
7. THE term-slug uniqueness situation SHALL be stated explicitly rather than
   assumed either way, because two archive kinds depend on it differently:
   - WordPress enforces within-taxonomy slug uniqueness **on write**
     (`wp_unique_term_slug()` suffixes a colliding slug), so a database
     WordPress created normally has no duplicates.
   - THE schema does **not** enforce it. `{prefix}terms.slug` carries a
     **non-unique** index
     (`internal/storage/migrations/sqlite/0001_init.up.sql:40`, with the
     MySQL/Postgres equivalents), and `{prefix}term_taxonomy` is unique only on
     `(term_id, taxonomy)`, so two `category` terms slugged `local` are
     schema-legal.
   - THE live reference WordPress database has **no** duplicate slug within a
     taxonomy (verified).
   THE consequence SHALL be that the category archive's `200` path does not
   depend on slug uniqueness at all — it resolves by walking the segment path
   through the taxonomy graph (Requirement 2.10) — while tag archives, which
   have no path to walk, resolve by slug **deterministically** (Requirement
   5.6). Both SHALL be recorded in `docs/compatibility.md` (Requirement 13.6).

### Requirement 2 — Nested category archive routes are canonical

**User Story:** As a site visitor, I want `/category/news/local` to work, and I
want the one URL for a category to be the one that reflects its place in the
hierarchy.

#### Acceptance Criteria

1. THE system SHALL serve a category archive at the path formed by the archive
   base segment followed by the category's full ancestry, root first, ending in
   the category's own slug — for example `/category/news/local` for "Local"
   nested under "News". THAT path SHALL be resolved by walking the segments
   through the taxonomy graph (Requirement 2.10), not by resolving the final
   segment and checking its ancestry afterwards.
2. THE canonical path for a category SHALL be constructed in exactly one place,
   `routing.Structure`, alongside `Structure.Canonical` — so the redirect
   target, the theme's pagination links and the REST `link` field cannot
   diverge. This preserves the single-construction-site property M9a
   established.
3. WHEN the segment walk of Requirement 2.10 fails but the path's final segment
   names a category somewhere in the taxonomy THE system SHALL respond `301`
   with `Location` set to that category's canonical nested path (Requirement
   2.11). This covers the flat `/category/{slug}` case, which is simply the
   zero-ancestor instance of it, and a wrong-ancestor path such as
   `/category/sport/local`. THE redirect is therefore a direct consequence of
   the walk failing rather than of an ancestry comparison performed after a leaf
   lookup.
4. WHEN a category is top-level THE canonical path SHALL equal the flat path,
   and the request SHALL render `200` with no redirect. The canonical path SHALL
   be a fixed point for every category, and this SHALL have an explicit test.
5. WHEN neither the walk nor the recovery of Requirement 2.11 finds a category —
   that is, the final path segment names no term in the `category` taxonomy — THE
   system SHALL respond `404`.
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
10. THE category archive's segment path SHALL be resolved by **walking the
    taxonomy graph**: the first segment SHALL be matched against the slugs of the
    taxonomy's **root** terms (`ParentID == 0`, or a parent absent from the
    graph per Requirement 1.4), each subsequent segment against the slugs of the
    **children of the term matched by the previous segment**, and the archive
    SHALL be the term the last segment matched. THE walk SHALL read the taxonomy
    graph exactly once per request (Requirement 1.6) — the same read the
    ancestry and descendant sets come from — so this costs no extra query.
    `TermRepo.BySlug` SHALL NOT be used on the category path at all. THE
    consequences SHALL be that:
    - THE `200` path does not depend on term slugs being unique within a
      taxonomy (Requirement 1.7): two `category` terms slugged `local` under
      different parents are each reachable at their own canonical path, and
      neither shadows the other.
    - WHEN two sibling terms under the same parent share a slug — the only case
      the walk cannot separate — THE walk SHALL select the **lowest `term_id`**,
      deterministically and identically on all three vendors.
    - A wrong-ancestor path fails the walk by construction rather than by a
      comparison performed afterwards (Requirement 2.3).
11. WHEN the walk of Requirement 2.10 fails THE system SHALL attempt **recovery
    on the final segment only**, so a published flat or wrong-ancestor URL still
    redirects rather than `404`ing: it SHALL look for terms in the already-loaded
    taxonomy graph whose slug equals the final segment, and
    - IF exactly one matches THEN it SHALL `301` to that term's canonical path;
    - IF more than one matches THEN it SHALL `301` to the canonical path of the
      one with the **lowest `term_id`**, deterministically on all three vendors;
    - IF none matches THEN it SHALL `404` (Requirement 2.5).
    Slug ambiguity is therefore confined to the redirect-recovery path, where the
    alternative is a `404`, and is absent from every path that returns `200`.

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
   `PostRepository.RecentPosts` (`internal/storage/wprepo/repo.go:93`) and
   WordPress's own archive queries. THIS IS a **user-visible change to the
   existing category archive**, not only a rule for the new ones, and SHALL be
   recorded as such: `ByTermSlug` (`repo.go:168`) and
   `CountPublishedByTermSlug` (`repo.go:230`) apply **no** post-type predicate
   today, so a published `page` assigned to a category appears in the category
   archive now and will stop appearing. THE change SHALL be listed in
   `docs/compatibility.md` (Requirement 13.7). THE live reference database is
   unaffected — only `post` rows carry categories there (verified) — so the
   change is invisible on the site this milestone validates against, which is
   exactly why it has to be written down rather than observed.

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
4. THE collision check SHALL cover three cases, and SHALL emit a startup `WARN`
   naming the collision in each:
   a. IF `category_base` and `tag_base` resolve to the same segment, or either
      resolves to `author`, THEN THE system SHALL keep the *category* meaning for
      the colliding segment.
   b. IF a resolved base equals a **leading literal segment of the permalink
      structure** — the front, or its first segment when the front is longer than
      one segment — THEN THE system SHALL keep the **post** meaning for that
      segment. THE concrete case is WordPress's own "Numeric" preset,
      `/archives/%post_id%`, on a site whose `category_base` is `archives` — a
      rename an operator makes precisely *because* their URLs live under
      `/archives/`. Giving the base priority there would classify
      `/archives/123` as a category, resolve no category, and `404` **every post
      URL on the site** from its own canonical permalink. THE milestone's premise
      is that an existing site's published URLs keep working, so the post
      interpretation wins and the archive base is the surface that degrades.
      chi does **not** catch this: all colliding pattern pairs — including
      `/archives/{post_id}` alongside `/archives/*` — register without panicking
      (probed directly), so the failure is silent unless the classifier and the
      note make it loud.
   c. THE precedence in (a) and (b) SHALL be decided by the classifier
      (Requirement 9.7), not by chi registration order.
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
11. `routing.Parse` SHALL **normalize** each base value before resolving it:
    trim surrounding whitespace, trim leading and trailing slashes, and collapse
    repeated internal slashes. `firstNonEmpty`
    (`internal/routing/routing.go:351`) trims whitespace only, which was inert
    while no route consumed a base and becomes load-bearing here — the base is
    now the seat of chi pattern registration, `Classify`'s segment comparison,
    path construction and the REST `link` field. `/topics` is a realistic stored
    value, not a hypothetical: WordPress's `options-permalink.php` prefixes the
    submitted base with `/` before `update_option`, so `get_option(
    'category_base' )` plausibly returns `/topics` on any site where the base was
    set through the admin UI — and unnormalized it would emit `//topics/*`
    patterns. THE live reference database stores **both bases empty** (verified),
    so nothing in the reference site exercises this today, and the in-tree tests
    use `"sections"` — which is why normalization has to be specified rather than
    discovered.
12. IF a base normalizes to **more than one segment** (`topics/news`) THEN THE
    system SHALL emit a `Notes()` entry naming it, and SHALL use the normalized
    multi-segment value consistently in the patterns, the classifier and the path
    constructors — so an unusual base degrades to a reported oddity rather than to
    a category archive that `404`s while `CategoryPath` advertises it.
13. THE emptiness test that decides whether a base fell back to its default
    (`firstNonEmpty`) and the test that decides `CategoryBaseSet`/`TagBaseSet`
    (Requirement 4.9) SHALL be **one shared test**, applied after the
    normalization of 4.11. Otherwise a base of `" "` both resolves to the default
    *and* counts as provided, dropping the front for a base that was effectively
    unset.

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
6. Tags resolve **by slug**, because `post_tag` is flat and there is no path to
   walk — so the slug-uniqueness question Requirement 1.7 removes from the
   category path does not disappear for tags. WHEN more than one `post_tag` term
   shares a slug THE system SHALL resolve **deterministically to the lowest
   `term_id`**, and SHALL NOT treat the duplicate as an error. `TermRepo.BySlug`
   (`internal/storage/wprepo/repo.go:208`) is a `LIMIT 1` with **no `ORDER BY`**
   today, so its winner can differ between SQLite, MySQL and Postgres; it SHALL
   gain `ORDER BY t.term_id ASC`. THIS mirrors exactly how `ByNicename` is
   handled (Requirement 7.6): the schema permits the duplicate, WordPress's own
   read is arbitrary, grimoire picks a rule so that a cross-vendor contract test
   can assert something. THE impact SHALL be stated rather than implied — one
   `/{TagBase}/{slug}` URL can surface only one of the colliding tags, so the
   other's posts are unreachable by that route — and SHALL be recorded in
   `docs/compatibility.md` (Requirement 13.6).

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
   `post_date`, not through a vendor-specific date function. THE interval SHALL
   be built **in UTC**, so that `formatTS` — which is
   `return t.UTC().Format(tsLayout)`
   (`internal/storage/wprepo/helpers.go:27`) — is the **identity** on it. That is
   what "no timezone conversion" actually requires given that helper: `post.Date`
   is UTC-based wall-clock time, because `parseTS` reads it with
   `time.ParseInLocation(layout, s, time.UTC)` and then `.UTC()`
   (`helpers.go:52`), which is the same basis M9a's `Canonical` reads it on.
   Building the interval in any other zone would silently shift every boundary by
   that zone's offset: on a UTC-7 host a May 2024 archive would query
   `2024-05-01 07:00:00` to `2024-06-01 07:00:00`, missing the first seven hours
   of May 1 and wrongly including the last seven of April 30 — plausible-looking
   output, reported months later.
   `domain.MediaFilter` SHALL NOT be cited as the half-open precedent, because it
   is not one: `mediaWhere` compares against `formatTS(f.Before.AddDate(0, 0,
   1))` (`internal/storage/wprepo/media.go:79-83`, with a comment saying so), so
   its convention is `[After, Before+1day)` — an inclusive day-end. THE archive
   filter's bounds SHALL therefore be named **`Start`/`End`** rather than
   `After`/`Before`, so `internal/domain` does not carry two filter types whose
   identically-named fields mean opposite things.
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
8. WHEN `permalink_structure` is empty — a `Flat` `Structure` — THE system SHALL
   register **no** date archive routes and SHALL classify no path as a date
   archive. Date archives are gated entirely off, for three reasons that agree:
   - **It is what WordPress does.** With plain permalinks WordPress registers no
     rewrite rules at all; its date archives are `?m=2024`, `?m=202405`, not
     `/2024`.
   - **It preserves Requirement 9.6.** A flat `Structure` has no front, so every
     archive prefix is empty, and an ungated date row would claim `/2024` as a
     year archive ahead of the flat `/{slug}` route that 9.6 lists among the
     routes whose behavior must not change — and that task 7.4 asserts by test.
   - **It preserves a real URL.** Today `/2024` on a plain-permalink site
     resolves a post slugged `2024`, which is the slug WordPress generates for a
     post titled "2024". Ungated, that post becomes unreachable.
   THE classifier SHALL therefore classify `/2024` under a `Flat` structure as
   `KindPost` (Requirement 9.8), and `ArchivePatterns` SHALL emit no date
   patterns for a `Flat` structure. Category, tag and author archives are **not**
   gated — grimoire already serves `/category/{slug}` under plain permalinks, and
   that behavior is preserved.

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
9. THE absence of an index on `user_nicename` in grimoire's own migrations SHALL
   be recorded as a **known limitation**, not fixed. The column is declared at
   `internal/storage/migrations/sqlite/0001_init.up.sql:65`, `mysql:69` and
   `postgres:65`, and the only `users` index any of the three creates is on
   `user_login` — so `/author/{nicename}` is a full table scan per request on a
   **grimoire-created** database. WordPress-created databases carry WordPress's
   own index, so the reference site and every real deployment this milestone
   targets are unaffected. Adding an index would break the zero-migration
   property this milestone leans on (Requirement 1.3), which is a worse trade than
   a scan over the `users` table of a database that only grimoire's own tests and
   fresh installs create. THE limitation SHALL be recorded in
   `docs/compatibility.md` (Requirement 13.8).

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
7. WHERE a resolved archive base equals a leading literal segment of the
   permalink structure (Requirement 4.4b) THE classifier SHALL resolve the path
   as `KindPost`, and the archive interpretation of that segment SHALL be
   unreachable. THE post row of the precedence table SHALL therefore be
   **reachable** for a path whose first segment is a base that collides with the
   structure's front — which is a property of the table's ordering and SHALL have
   its own test row, since chi registers the colliding patterns without
   complaint and would never surface the problem.
8. WHERE the structure is `Flat` THE classifier SHALL never return `KindDate`
   (Requirement 6.8). `/2024` SHALL classify as `KindPost` with
   `Ref{Slug: "2024"}`, exactly as it resolves today, and this SHALL have an
   explicit `Classify` test case rather than being inferred from the gate.

### Requirement 10 — REST parity for the new surfaces

#### Acceptance Criteria

1. `restTerm.Parent` in `internal/web/rest_terms.go` is currently hard-coded to
   `0`. It SHALL carry `Term.ParentID`, so a REST consumer sees the same
   hierarchy the archive routes serve.
2. `restTermLink` currently hard-codes `restAbs(r, "/category/"+slug)` for
   categories and `/?tag=` for tags. Both SHALL be built from
   `routing.Structure`'s archive-path constructors, so an advertised link is a
   URL that returns `200` rather than one that `301`s or `404`s. A category's
   link SHALL carry its full ancestry, resolved through the same graph walk the
   archive route uses (Requirement 2.10), so the link is the canonical path
   rather than the flat one that `301`s.
   **One exception, stated rather than left to contradict Requirement 4.4:** when
   `tag_base` collides with `category_base` the colliding segment keeps the
   category meaning (4.4a), so `TagPath` advertises a link that classifies as a
   category and does not return `200`; likewise a base that collides with the
   structure's front keeps the post meaning (4.4b), so that archive's advertised
   link does not return `200` either. In both cases the collision is already
   reported by `Structure.Notes()` at startup and by `migrate -check`
   (Requirement 4.6), which is the surface that tells an operator why. THE
   `200` guarantee therefore holds for every configuration that `Notes()` reports
   as clean.
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
   currently inserts every `term_taxonomy` row with `parent` `0`, so a nested
   category — a parent, a child and a grandchild — SHALL be seeded to make the
   assertion meaningful. IT SHALL be seeded by a **separate helper** rather than
   by extending `SeedFixtures`, and that helper SHALL add **no posts and no
   category terms to the set `SeedFixtures` creates**, because several existing
   assertions are absolute rather than relative: `contract.go:224-228` asserts
   exactly 3 posts *and* their exact slug order, `contract.go:573` asserts a count
   of 3, `internal/web/rest_terms_test.go:115` asserts "SeedFixtures seeds 3
   category terms", and `internal/web/permalinks_test.go:17` documents the fixture
   post set its expectations derive from. `SeedFixtures` is shared by
   `internal/web/handlers_test.go:49`, `auth_test.go:77`, `rest_terms_test.go:62`,
   `rest_router_test.go:56` and `rest_apppassword_comments_test.go:51`, so
   changing what it seeds turns this phase into a cross-package test-update phase
   for no gain.
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
   `%postname%`-leading one does not. THE `date/` table SHALL include at least
   one **literal-carrying** structure, because WordPress's rule counts `%...%`
   **tokens** rather than path segments: `/archives/%year%/%monthnum%/%post_id%/`
   has `%post_id%` at token 3 and therefore **does** fire the prefix, while an
   implementation that counted segments would place it at 4 and not fire. A table
   of token-only structures cannot catch that mistake.
10. Duplicate-`user_nicename` resolution SHALL be covered on both surfaces: a
    cross-vendor `storagetest` case seeding two users with the same nicename and
    asserting the lowest `ID` wins on SQLite, MySQL and Postgres (Requirement
    7.6), and a test asserting `migrate -check` reports the duplicate
    (Requirement 7.8).
11. Base **normalization** (Requirement 4.11–4.13) SHALL be covered by pure unit
    tests in `internal/routing`: `/topics`, `topics/`, `//topics//` and
    `" topics "` all resolve to the same base and to `CategoryBaseSet == true`;
    `" "` resolves to the default **and** to `CategoryBaseSet == false`, which is
    the case a second emptiness test gets wrong; a base normalizing to
    `topics/news` yields a `Notes()` entry and still produces consistent patterns,
    classification and constructed paths.
12. THE `Flat` date gate (Requirement 6.8, 9.8) SHALL be covered by an explicit
    `Classify` case asserting `/2024` under a `Flat` structure is `KindPost` with
    `Ref{Slug: "2024"}` and **not** `KindDate`, and by an `ArchivePatterns` case
    asserting a `Flat` structure emits no date patterns.
13. THE base-vs-structure-literal collision (Requirement 4.4b, 9.7) SHALL be
    covered by: a `Notes()` case for `category_base` = `archives` against
    `/archives/%post_id%`; a `Classify` case asserting `/archives/123` under that
    configuration is `KindPost` and not `KindCategory`; and an assertion that
    `Parse` still returns a **nil error** and a usable non-flat `Structure`.
14. Deterministic tag resolution (Requirement 5.6) SHALL be covered by a
    cross-vendor `storagetest` contract case seeding **two `post_tag` terms
    sharing a slug** and asserting `TermRepo.BySlug` returns the lower
    `term_id` on SQLite, MySQL and Postgres. THE higher `term_id` SHALL be
    inserted first, so an implementation relying on insertion order rather than
    `ORDER BY` fails the case — mirroring how the duplicate-nicename case is
    seeded (12.10).
15. THE category segment walk (Requirement 2.10, 2.11) SHALL be covered by
    service-level tests against a fake `TermReader`: a three-level path resolves
    to the leaf; a wrong-ancestor path fails the walk and recovers to a `301`
    target; **two categories slugged identically under different parents are each
    reachable at their own canonical path**, which is the case leaf-first
    resolution gets wrong; two siblings sharing a slug resolve to the lower
    `term_id`; and a final segment matching no term yields `ErrNotFound`. No
    database is needed for any of them, and `TermRepo.BySlug` SHALL NOT appear on
    the category path at all.

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
6. `docs/compatibility.md` SHALL record the **term-slug** posture (Requirement
   1.7): WordPress enforces within-taxonomy slug uniqueness on write and the
   schema does not; category archives do not depend on it, because they resolve by
   walking the segment path (Requirement 2.10); tag archives do, and resolve to
   the lowest `term_id` (Requirement 5.6), with the same impact shape as the
   nicename case — one of the colliding tags is unreachable at that URL.
7. `docs/compatibility.md` SHALL record the **post-type filter change on the
   existing category archive** (Requirement 3.5): a published `page` assigned to
   a category appears in the category archive before this milestone and not
   after. This is a user-visible removal on a route that already shipped, and it
   is invisible on the reference database, so it is recorded rather than observed.
8. `docs/compatibility.md` SHALL record the missing `user_nicename` index
   (Requirement 7.9) as a known limitation: `/author/{nicename}` is a full scan on
   a grimoire-created database, WordPress-created databases carry WordPress's own
   index, and adding one would break the zero-migration property.
9. `docs/compatibility.md` SHALL record the two precedence decisions that shape
   which URL wins, with the right framing in each case:
   - Date archives are **not served under plain permalinks** (Requirement 6.8).
     This **matches** WordPress, whose plain-permalink date archives are `?m=`
     query arguments, and it is what keeps `/2024` resolving a post slugged
     `2024`.
   - A base colliding with the structure's leading literal keeps the **post**
     meaning (Requirement 4.4b), so the archive at that base is unreachable while
     every published post URL still resolves. This is a grimoire decision, made
     because the alternative `404`s an entire site, and `migrate -check` reports
     the condition.

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
  rewriting user rows. The same posture applies to duplicate term slugs
  (Requirement 1.7, 5.6): reported and resolved deterministically, never
  rewritten.
- **Date archives under plain permalinks.** Gated off by Requirement 6.8,
  matching WordPress, whose plain-permalink date archives are `?m=` query
  arguments rather than paths. `?m=` query-argument archives are not implemented
  either — no query-argument route exists in grimoire.
- **An index on `user_nicename`.** Recorded as a known limitation by Requirement
  7.9 rather than fixed, because adding one would break the zero-migration
  property.
