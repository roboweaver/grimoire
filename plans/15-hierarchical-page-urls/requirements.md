# Requirements Document

M15 — hierarchical page URLs

## Introduction

M9a made grimoire's permalink routing structure-driven: `Structure.Classify`
turns a request path into a `Target` (`internal/routing/classify.go`), and
`Structure.Canonical` is the single place a post's permalink is constructed
(`internal/routing/routing.go:408`). M9b then taught the handler to walk a
*nested* path for categories — `categoryArchive` takes `Target.Segments`, calls
`terms.CategoryArchive`, handles `content.CategoryMovedError` to 301 onto the
resolved term's canonical ancestry, and builds the canonical from the RESOLVED
term's own ancestry as a fixed point (`internal/web/handlers.go`). The nested
walk itself is `content.TermHierarchy.Ancestry` over `term_taxonomy.parent`
(`internal/content/archive.go:251`): a root-first slug path ending in the term's
own slug, with a visited-set that degrades gracefully on a corrupt graph.

What neither milestone did is serve a `page` under its parent-page ancestry.
WordPress treats a `page` as EXEMPT from `permalink_structure`: a page's
canonical URL is its nested `post_name` ancestry over `post_parent`
(`/{parent-slug}/{child-slug}/`), independent of whether the site runs a dated,
day-and-name, or flat permalink structure. grimoire does not do this yet, and
the gap is recorded verbatim in the compatibility notes
(`docs/compatibility.md:119-125`): with a dated structure a page's canonical
grimoire URL is `/2024/05/17/about/` and `/about` 301s to it — "whereas
WordPress serves pages at `/about` regardless of permalink_structure (and nests
them under their parent page). This is the one place M9a's canonical URL can
differ from the URL the site published." The same notes list "hierarchical page
URLs … remain out of scope" as of M9b (`docs/compatibility.md:324`).

M15 closes that gap. It reconciles BOTH divergences at once — a page is no
longer routed by the structure's post shape (so no dated canonical for a page),
and a child page is nested under its parent — by giving `Structure.Canonical` a
page branch that returns the page's nested `post_name` ancestry, and by teaching
the handler to resolve a one-or-more-segment page path by walking `post_parent`
to the leaf. It parallels M9b's nested-category walk exactly, but over
`post_parent` on `page` rows instead of `term_taxonomy.parent` on term rows. A
top-level page (no parent) is served at `/{slug}` exactly as today
(`internal/routing/classify.go` row 8, the flat `/{slug}` fallback), so no
top-level page URL changes.

M15 refines roadmap **Requirement 30 (Nested Page Routing)** and
**Requirement 31 (M15 Exclusions and Test Coverage)**
(`../wordpress-core-parity-roadmap/requirements.md`) and roadmap task group
**15.A** (`../wordpress-core-parity-roadmap/tasks.md`). It builds directly on
M9a's classifier/canonical split (`internal/routing/classify.go`,
`internal/routing/routing.go:408`) and M9b's nested-walk precedent
(`content.TermHierarchy.Ancestry`, `internal/content/archive.go:251`;
`categoryArchive` and `CategoryMovedError` in `internal/web/handlers.go`),
mirroring the term precedents over `post_parent` on pages.

## Glossary

- **`page` / `post_parent`** — a `page` is a `{prefix}posts` row with
  `post_type = 'page'`; `post_parent` names its parent page's `ID` (`0` for a
  top-level page). WordPress nests a page under its `post_parent` ancestry for
  routing. `post_parent` and `post_name` already exist in `postColumns`
  (`internal/storage/wprepo/repo.go`), so M15 adds no column.
- **Nested page path / page ancestry** — the root-first slug path over
  `post_parent` ending in the leaf page's own slug (`/{parent}/…/{page}/`), the
  page analogue of `content.TermHierarchy.Ancestry`
  (`internal/content/archive.go:251`).
- **`PageHierarchy`** — the new page-ancestry walk model over `post_parent` on
  `page` rows, mirroring `content.TermHierarchy` in shape (an `Ancestry(id)`
  returning the root-first self-inclusive slug path, built from a single
  paginated/graph read of page rows).
- **`Structure.Classify` / `Target`** — the PURE, TOTAL classifier
  (`internal/routing/classify.go`): a request path yields a `Target{Kind, Post
  Ref, Segments, Slug, Date, TrailingSlash}`. Kinds are
  `KindNone`/`KindPost`/`KindCategory`/`KindTag`/`KindAuthor`/`KindDate`. The
  precedence table is evaluated top-to-bottom, first match wins; the classifier
  makes NO claim that a segment names anything — the handler's walk produces the
  301 or 404.
- **`Structure.Canonical`** — the single permalink construction site
  (`internal/routing/routing.go:408`); the REST `link` field reads it via
  `postLink → permalinks.Canonical` (`internal/content/rest.go:234`).
- **`resolve` / `single` / `categoryArchive`** — the handler dispatch on
  `Classify`'s Kind (`internal/web/handlers.go`). `single` resolves the
  post/page and 301s via `redirectCanonical` when `r.URL.Path` differs from
  `Canonical(post)`. `categoryArchive` is the nested-walk precedent that M15
  mirrors for pages.
- **`redirectCanonical`** — the handler's permanent-redirect helper; it issues a
  301 to the canonical path and appends `r.URL.RawQuery` RAW, preserving the
  query string unchanged (`internal/web/handlers.go`).
- **The flat and dated forms** — the two non-canonical URLs that must 301 to a
  page's nested canonical: the flat `/{slug}` form and the old
  permalink-structure form (e.g. the dated `/2024/05/17/about/` that M9a
  produced for a page under a dated structure).
- **Orphan page / parent cycle** — a page whose `post_parent` names an `ID`
  absent from the page graph (orphan → shorter resolvable path), or a
  `post_parent` chain that revisits a page (cycle → terminate on the first
  repeat) — the exact corrupt-shape degradation `TermHierarchy.Ancestry`
  already defines (`internal/content/archive.go:251`).
- **Wildcard route** — a chi route rooted at a base that carries an arbitrary
  segment count, because a nested path's segment count is inexpressible in a chi
  pattern; the nested-category archive is already registered this way
  (`internal/web/router.go:225-277`), and a nested page path has the same
  arbitrary-segment-count property.

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | **A page is EXEMPT from `permalink_structure`; its canonical is its nested `post_name` ancestry over `post_parent`** (`/{parent-slug}/…/{page-slug}/`, trailing slash per the structure), INDEPENDENT of `permalink_structure`. This reconciles BOTH M9a divergences at `docs/compatibility.md:119-125` at once: a page is no longer routed by the structure's post shape (no dated page canonical), and a child page nests under its parent. A top-level page (no parent) is served at `/{slug}` exactly as today — no top-level page URL changes (roadmap Req 30.4). | Requirements 1, 2, 4. Resolves the one place M9a's canonical can differ from the published URL. |
| D2 | **A new page-ancestry walk (`PageHierarchy` or equivalent) over `post_parent` on `page` rows, mirroring `content.TermHierarchy.Ancestry`** (`internal/content/archive.go:251`): root-first slug path, self-inclusive leaf, with the SAME corrupt-shape degradation — an orphaned parent (`post_parent` absent from the page graph) terminates the walk yielding the shorter resolvable path, and a parent cycle terminates on the first repeated page (roadmap Req 30.3). Reuse the walk shape; do not reinvent it. | Requirements 2, 3, 7. The visited-set and orphan/cycle rules are M9b's, retargeted to `post_parent`. |
| D3 | **Resolution: a one-or-more-segment path that classifies as a page is resolved by walking its segments through the page graph to the leaf page; the flat `/{slug}` form AND the old permalink-structure form (the dated `/2024/05/17/about/`) both 301 (permanent) to the nested canonical, query string preserved RAW** (mirroring `redirectCanonical`, `internal/web/handlers.go`), paralleling M9b's `CategoryMovedError` 301 recovery (roadmap Req 30.2). | Requirements 3, 5. The 301 is permanent; `r.URL.RawQuery` is appended raw. |
| D4 | **`Structure.Canonical` is the single construction site** (`internal/routing/routing.go:408`): it returns the NESTED page path for a page (built from the resolved page's OWN ancestry — a fixed point) and is UNCHANGED for a post (`post_type = 'post'`). Because the REST `link` field reads `Canonical` (`internal/content/rest.go:234`), a page's REST `link` updates for free. | Requirements 4, 8. One site changes; posts and REST link construction are otherwise untouched. |
| D5 | **Classifier disambiguation.** A nested page path has an arbitrary segment count (like a nested category), so it is served via a wildcard route and classified by `Structure.Classify`; the precedence table gains page handling WITHOUT disturbing the existing rows — the category/tag/author/date bases still win, and top-level pages still resolve as today (`internal/routing/classify.go`). The design MUST resolve the subtle collisions: a page whose slug equals an archive base; a multi-segment path that is neither a nested page nor an archive stays `KindNone` → 404; and precedence relative to `KindPost` for posts. | Requirements 6, 9. Precedence lives in `Classify`, not chi registration order (chi resolves parameter collisions silently, last-registered wins, `internal/web/router.go:225-277`). |
| D6 | **Storage: a new additive paginated/graph read port for page rows** (`ID`, `slug`, `post_parent`, `status` — the fields the walk needs), mirroring the term reader, additive to `domain` and `wprepo`, with a cross-vendor contract. NO migration, NO new column (`post_parent`/`post_name` already exist in `postColumns`, `internal/storage/wprepo/repo.go`), NO new capability, and NO new web surface beyond the page wildcard route pattern. | Requirements 7, 8. The port is additive; existing `PostRepo` reads are unchanged. |
| D7 | **Tests: cross-vendor `storagetest` contract for the `post_parent` page walk; handler tests for the canonical 200, the flat-and-dated 301 (query preserved), and the corrupted-hierarchy degradation (orphan parent, cycle); AND at least one fixture-based test against an imported real-WordPress-database export**, mirroring M9b's nested-category validation (roadmap Req 31.2). Pure Go/SQLite, env-gated MySQL/Postgres through the existing harness, no new dependency; `gofmt`/`go vet`/`go build ./...`/`go test ./...` green at the end of every task. | Requirement 10. |

## Requirements

### Requirement 1: A page is routed by its nested post_name ancestry, independent of permalink_structure

**User Story:** As a site owner who imported a WordPress database, I want my
pages served at their nested slug paths exactly as WordPress serves them, so that
`/about/` and `/about/team/` work regardless of my permalink structure and I do
not lose the one place grimoire's canonical URL could differ from the published
URL.

#### Acceptance Criteria

1. THE routing SHALL treat a `page` (`post_type = 'page'`) as EXEMPT from
   `permalink_structure`: a page's canonical URL SHALL be its nested `post_name`
   ancestry over `post_parent` (`/{parent-slug}/…/{page-slug}/`), independent of
   whether the configured structure is flat, dated, day-and-name, or custom
   (`docs/compatibility.md:119-125`).
2. WHEN a page has no parent (`post_parent = 0`), THE routing SHALL serve it at
   `/{slug}` exactly as today, with no change to any top-level page URL (roadmap
   Req 30.4).
3. WHEN a page has a parent, THE page's canonical URL SHALL nest the page under
   its parent-page ancestry, so a child page's URL carries every ancestor slug
   root-first before its own slug.
4. THE routing SHALL NOT apply the permalink structure's post shape (e.g. a
   dated `/{year}/{month}/{day}/` prefix) to a page, so a page under a dated
   structure SHALL NO LONGER have a dated canonical such as `/2024/05/17/about/`.

### Requirement 2: Page ancestry walk mirrors TermHierarchy.Ancestry over post_parent

**User Story:** As a maintainer, I want the page ancestry computed by the same
walk shape M9b already proved for terms, so that nested page routing reuses a
validated algorithm rather than a new one.

#### Acceptance Criteria

1. THE milestone SHALL add a page-ancestry model (`PageHierarchy` or equivalent)
   that computes a page's ancestry by walking `post_parent` on `page` rows,
   mirroring `content.TermHierarchy.Ancestry`
   (`internal/content/archive.go:251`).
2. THE page ancestry for a page SHALL be a root-first slug path that ends in the
   page's own slug (self-inclusive leaf), matching the term ancestry shape.
3. WHEN a page's `post_parent` names an `ID` absent from the page graph (an
   orphan), THE walk SHALL terminate at that point and yield the shorter
   resolvable path, matching the term walk's orphan rule (roadmap Req 30.3).
4. WHEN a page's `post_parent` chain revisits a page already seen in the walk (a
   parent cycle), THE walk SHALL terminate on the first repeated page, matching
   the term walk's cycle rule, so a corrupt graph cannot loop.
5. THE page ancestry walk SHALL be built from a single read of the page rows it
   needs (per Requirement 7), not from a per-level database round-trip.

### Requirement 3: Resolve a nested page path by walking segments to the leaf page

**User Story:** As a visitor, I want a nested page URL to load the correct page,
so that `/{parent}/{child}/` resolves to the child page and the handler knows
which page I asked for.

#### Acceptance Criteria

1. WHEN a request path of one or more segments classifies as a page, THE handler
   SHALL resolve it by walking its segments through the page graph to the leaf
   page, paralleling `categoryArchive`'s nested walk
   (`internal/web/handlers.go`).
2. WHEN the walked segments resolve to a published leaf page, THE handler SHALL
   render that page (a 200), the page analogue of `renderSingle`.
3. IF the walked segments do not resolve to any published page, THEN THE handler
   SHALL respond 404, the same not-found posture `single` and `categoryArchive`
   take when a slug names nothing.
4. THE page resolution SHALL resolve a published page only (via the existing
   `BySlug`/`PublishedByID` filters, `internal/content/post.go`;
   `internal/storage/wprepo/repo.go`), preserving today's published-only
   behavior for pages.
5. THE handler SHALL build the canonical it compares against from the RESOLVED
   leaf page's OWN ancestry (a fixed point), exactly as `categoryArchive` builds
   the canonical from the resolved term's own ancestry.

### Requirement 4: Structure.Canonical returns the nested page path for a page, unchanged for a post

**User Story:** As an integrator, I want a page's canonical permalink and its
REST `link` to be the nested slug path, so that the one permalink construction
site stays authoritative and my REST clients get the right URL without a second
code path.

#### Acceptance Criteria

1. WHEN `Structure.Canonical` (`internal/routing/routing.go:408`) is called for a
   `page`, THE method SHALL return the page's nested `post_name` ancestry path
   (`/{parent-slug}/…/{page-slug}/`, trailing slash per the structure), built
   from the page's own ancestry (D1, D4).
2. WHEN `Structure.Canonical` is called for a `post` (`post_type = 'post'`), THE
   method SHALL return the same permalink it returns today, unchanged by M15.
3. THE page canonical SHALL be constructed at the SINGLE site
   `Structure.Canonical`, and M15 SHALL NOT introduce a second page-permalink
   construction path.
4. WHEN a page's REST view is read, THE REST `link` field SHALL report the
   nested page canonical, because `postLink` reads `permalinks.Canonical`
   (`internal/content/rest.go:234`) — so a page's REST `link` updates for free.
5. WHEN a page is top-level (`post_parent = 0`), THE canonical SHALL be `/{slug}`
   (with trailing slash per the structure), matching Requirement 1.2.

### Requirement 5: Flat and dated page URLs 301 to the nested canonical, query preserved

**User Story:** As a visitor following an old or shortened page link, I want to
land on the page's canonical nested URL with a permanent redirect, so that
bookmarks and the old dated links keep working and search engines learn the
canonical.

#### Acceptance Criteria

1. WHEN a request arrives for a page's flat `/{slug}` form but the page is nested
   under a parent, THE handler SHALL respond 301 (permanent) to the page's
   nested canonical, paralleling M9b's `CategoryMovedError` 301 recovery
   (`internal/web/handlers.go`).
2. WHEN a request arrives for a page's old permalink-structure form (e.g. the
   dated `/2024/05/17/about/` that M9a produced for a page under a dated
   structure), THE handler SHALL respond 301 (permanent) to the page's nested
   canonical.
3. WHEN the handler issues a 301 for a page, THE handler SHALL preserve the
   request's query string by appending `r.URL.RawQuery` RAW, mirroring
   `redirectCanonical` (`internal/web/handlers.go`).
4. WHEN a request path already equals the page's nested canonical, THE handler
   SHALL render the page (a 200) and SHALL NOT redirect, matching `single`'s
   "path equals canonical → render, else 301" rule.
5. THE redirect SHALL be a 301 (permanent), not a 302, consistent with
   `redirectCanonical`'s permanent-redirect status for canonical corrections.

### Requirement 6: Classifier gains page handling without disturbing existing precedence

**User Story:** As a maintainer, I want nested page classification added to the
pure classifier without changing how categories, tags, author, date, or posts
classify, so that M15 cannot regress M9a/M9b routing.

#### Acceptance Criteria

1. THE `Structure.Classify` precedence table (`internal/routing/classify.go`)
   SHALL gain page handling such that the existing category, tag, author, and
   date base rows still win for their bases, and a top-level page still
   classifies as it does today.
2. THE classifier SHALL remain PURE and TOTAL: it SHALL make no database read and
   no claim that a segment names a real page — the handler's walk (Requirement 3)
   produces the 301 or the 404.
3. IF a multi-segment request path matches neither a nested page nor an archive
   base nor the structure's post shape, THEN THE classifier SHALL yield
   `KindNone` so the handler responds 404, preserving the `KindNone` escape
   hatch.
4. WHERE a page's slug equals an archive base (e.g. a page slugged like the
   category base), THE design SHALL define and the classifier SHALL honor a
   deterministic precedence so one owner wins the path, and the requirements
   SHALL record which owner wins.
5. THE precedence relative to `KindPost` for posts SHALL be preserved: a request
   that resolves to a post under the configured structure SHALL still classify as
   `KindPost`, and page classification SHALL NOT shadow post resolution.

### Requirement 7: An additive paginated/graph page read port over post_parent

**User Story:** As a maintainer, I want the page walk fed by a dedicated additive
read port rather than loading whole post rows, so that the ancestry walk has
exactly the fields it needs and a large pages table is read efficiently.

#### Acceptance Criteria

1. THE milestone SHALL add an additive read port (to `domain` and `wprepo`) that
   returns `page` rows carrying at minimum each row's `ID`, slug (`post_name`),
   `post_parent`, and status, mirroring the term reader, so the page-ancestry
   walk can build its graph.
2. THE new read port SHALL select `page` rows (`post_type = 'page'`) and SHALL
   NOT require loading every post column the way a full `PostWriter.ByID`
   round-trip would.
3. WHEN the page set is large, THE read port SHALL return rows under a stable
   ordering (e.g. by `ID`) so a paginated read returns every page exactly once
   with no row dropped or duplicated.
4. THE new read port SHALL be additive and SHALL NOT alter the behavior of the
   existing `PostRepo` reads (`BySlug`/`PublishedByID`,
   `internal/storage/wprepo/repo.go`), which continue to default to
   `{"post","page"}` with `post_status = 'publish'`.
5. THE port SHALL read `post_parent` and `post_name` from the existing
   `postColumns` (`internal/storage/wprepo/repo.go`), adding NO new column.

### Requirement 8: No migration, no new column, no new capability, no new web surface beyond the page wildcard route

**User Story:** As a maintainer, I want M15 to be a routing-and-read-port change
over the existing schema, so that it carries no migration, no schema churn, and
no new authorization surface.

#### Acceptance Criteria

1. THE milestone SHALL add NO file under `internal/storage/migrations` and SHALL
   add NO table or column, because `post_parent` and `post_name` already exist in
   `postColumns` (`internal/storage/wprepo/repo.go`); the task list SHALL verify
   this explicitly, as the sibling specs' task lists do.
2. THE milestone SHALL introduce NO new capability name; page routing reads
   published pages only and makes no authorization decision beyond what
   `BySlug`/`PublishedByID` already enforce.
3. THE milestone SHALL add NO new web surface beyond the page wildcard route
   pattern (Requirement 9); all content routes SHALL continue to dispatch through
   the single `s.resolve` entry (`internal/web/router.go:225-277`).
4. THE new page read port SHALL be additive to `domain` and `wprepo` and SHALL
   NOT change the `domain` post reader/writer method set beyond adding the new
   read port.
5. THE only permalink construction site SHALL remain `Structure.Canonical`
   (`internal/routing/routing.go:408`), and M15 SHALL add no second page-URL
   builder (Requirement 4.3).

### Requirement 9: A page wildcard route registered like the nested-category archive

**User Story:** As a maintainer, I want nested page paths served by a wildcard
route the same way nested categories are, so that an arbitrary-depth page path is
routable even though its segment count cannot be written into a chi pattern.

#### Acceptance Criteria

1. THE milestone SHALL register a wildcard route for nested page paths, because a
   nested page path's segment count is arbitrary and inexpressible in a chi
   pattern, exactly as the nested-category archive is a wildcard rooted at its
   base (`internal/web/router.go:225-277`).
2. THE page wildcard route SHALL dispatch to the single `s.resolve` handler, so
   page routing joins the existing dispatch rather than adding a parallel router
   path.
3. WHEN chi resolves a parameter-node collision between the page wildcard and
   another content route, THE routing correctness SHALL NOT depend on chi
   registration order (chi silently lets the last-registered win); precedence
   SHALL be decided in `Structure.Classify` (Requirement 6), not by registration
   order.
4. WHEN the page wildcard route is matched, THE handler SHALL pass the full
   segment path to the page resolution walk (Requirement 3), not a single
   captured slug.
5. THE page wildcard route SHALL coexist with the existing flat `/{slug}`
   fallback so a top-level page continues to resolve through the single-segment
   path exactly as today (`internal/routing/classify.go` row 8).

### Requirement 10: Cross-vendor contract, handler, and fixture test coverage

**User Story:** As a maintainer, I want every new behavior proven before it is
considered done, so that nested page routing cannot silently break page URLs or
regress M9a/M9b.

#### Acceptance Criteria

1. THE milestone SHALL cover the new page read port and `post_parent` walk with a
   cross-vendor `storagetest` contract test proving the ancestry is a root-first
   self-inclusive slug path and that the walk degrades correctly on an orphan
   parent (shorter path) and a parent cycle (terminate on first repeat)
   (Requirement 2; SQLite always, MySQL/Postgres env-gated through the existing
   harness with no new env var).
2. THE milestone SHALL cover the handler with tests for: a nested page canonical
   200, the flat `/{slug}` 301 to the nested canonical, and the old dated-form
   301 to the nested canonical — each asserting the query string is preserved RAW
   across the redirect (Requirement 5).
3. THE milestone SHALL cover the corrupted-hierarchy degradation at the handler
   level: an orphan-parent page and a cycle still resolve to a servable canonical
   rather than erroring or looping (Requirement 2.3, 2.4).
4. THE milestone SHALL include at least one fixture-based test against an imported
   real-WordPress-database export, mirroring M9b's nested-category validation
   (roadmap Req 31.2), proving nested page URLs match WordPress's for a real page
   tree.
5. THE tests SHALL be pure Go over SQLite with NO new dependency, and `gofmt`,
   `go vet`, `go build ./...`, and `go test ./...` SHALL be green at the end of
   every task (D7).

## Out of scope

- **Hierarchical URLs for CUSTOM post types.** A hierarchical custom post type
  follows M12's registry, not M15 (roadmap Req 31.1). M15 is `post_type = 'page'`
  only.
- **Term-hierarchy editing (M13, shipped) and nested-category routing (M9b,
  shipped).** M15 is pages over `post_parent` only; it does not touch
  `term_taxonomy.parent` routing or writes.
- **Changing top-level page URLs or any post (`post_type = 'post'`) URL or
  permalink behavior.** A top-level page stays at `/{slug}` (Requirement 1.2) and
  `Structure.Canonical` is unchanged for a post (Requirement 4.2).
- **The `/page/{n}/` and `?m=` archive path forms.** Those belong to M16, not
  M15.
- **Any new migration, column, table, capability, or REST route** (D6 /
  Requirement 8), beyond the single page wildcard route pattern (Requirement 9).
- **Draft/private page preview routing semantics beyond what `BySlug`/
  `PublishedByID` already do.** M15 serves published pages only, exactly as today
  (Requirement 3.4).

## Traceability

This spec refines roadmap **Requirement 30 (Nested Page Routing)** and
**Requirement 31 (M15 Exclusions and Test Coverage)**
(`../wordpress-core-parity-roadmap/requirements.md`) and roadmap task group
**15.A** (`../wordpress-core-parity-roadmap/tasks.md`). It completes the two M9a
divergences recorded verbatim at `docs/compatibility.md:119-125` ("Pages follow
the post structure, where WordPress exempts them" and the un-nested parent page)
and lifts the "hierarchical page URLs … remain out of scope" note at
`docs/compatibility.md:324`. It builds on **M9a**'s classifier/canonical split
(`internal/routing/classify.go`; `Structure.Canonical`,
`internal/routing/routing.go:408`; the REST `link` read at
`internal/content/rest.go:234`) and mirrors **M9b**'s nested-walk precedent —
`content.TermHierarchy.Ancestry` (`internal/content/archive.go:251`),
`categoryArchive`, `CategoryMovedError`, and `redirectCanonical`
(`internal/web/handlers.go`) — over `post_parent` on `page` rows instead of
`term_taxonomy.parent`, reusing the existing read ports and `postColumns`
(`internal/storage/wprepo/repo.go`).
