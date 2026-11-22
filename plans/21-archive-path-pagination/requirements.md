# Requirements Document
M16 — Archive path-form pagination (`/page/{n}/`) and the `?m=` date query
## Introduction
grimoire paginates every archive with `?page=N`. WordPress paginates with
`/page/{n}/` and, under plain permalinks, with `?paged=N`. It also resolves the
date query `?m=YYYYMM`. `docs/compatibility.md` records both as deliberate
divergences (M8's "Pagination is M8's contract, unchanged", and M9a/M9b's
"Pagination is `?page=N`, where WordPress uses `/page/{n}/`" and "The `?m=`
query-argument form is not implemented either").
This milestone is roadmap task **16.A**, which refines roadmap Requirements 32
and 33 (`../wordpress-core-parity-roadmap/requirements.md`, "M16"). Requirement
33.1 allows the work only if `/page/{n}/` parity is explicitly wanted. **The
project owner has now explicitly asked for it, which satisfies that condition**
(Requirement 1 records it). The roadmap marks M16 optional and lowest priority;
that status is unchanged, and the explicit request is the only reason it starts.
The reason `?page=N` was chosen is real. `/category/news/page/2` cannot be told
from a nested category `news` with a child slugged `page` and a grandchild
slugged `2` by looking at the path alone. WordPress resolves the same ambiguity
by rewrite-rule order: `/category/news/page/2/` is pagination, and a term slugged
`page` is reachable only when it is not followed by a number. M16 adopts that
rule exactly and states every URL it affects (Requirement 3).
**What the code at `main` `a9110f4` showed, and what it changed in the brief.**
- **`Structure.Classify` already sends `/page/2` somewhere.** `/page/2` is a
  two-segment path that no archive row claims, so it is `KindPage` (M15) and the
  page walk 404s it unless a page slugged `page` has a child slugged `2`. The
  pagination rule therefore has to run **before** the M15 row and before the
  structure's post row, and it has to say what happens to that page.
- **The `/{slug}/*` page wildcard is registered only when `WithPages` is wired**
  (`internal/web/router.go`). Path-form pagination cannot lean on it, so the
  patterns are registered explicitly (design Components).
- **Every archive handler ends in `renderArchive`**, which compares
  `r.URL.Path` with the archive's canonical path and 301s on a difference. A
  path ending in `/page/2/` would 301 to page 1 unless the comparison learns
  about the page. Today that same function silently drops nothing, because
  `?page=N` is a query and `redirectCanonical` carries the raw query across.
- **The theme builds pagination links by hand** (`themes/default/templates/
  index.tmpl`, `category.tmpl`, `archive.tmpl`: `{{.BaseURL}}?page={{add ...}}`
  and a literal `/?page=`). `BaseURL` is the archive's canonical path. The
  template has no access to the trailing-slash policy, so the links need a
  handler-built value (Requirement 7).
- **Built-in goldens are REST and admin only.** `cmd/grimoire/testdata/
  builtin_golden/*.json` snapshot `/wp-json` and `/admin/api` responses. No
  archive HTML is in them, so M16 does not touch them (Requirement 12).
- **`internal/render/testdata/golden/*.html` contain no pagination links.**
  They stay byte-identical (design Findings 4).
- **There are no feed or sitemap routes.** `internal/web` registers none
  (`feed` appears only in `internal/registry/reserved.go` so a custom base cannot
  take it). The brief's "sitemap/feeds if any exist" is answered: none do.
- **`?page=` has an unguarded upper end.** `strconv.Atoi` accepts 18 digits and
  `clamp` computes `(page-1)*perPage`, which overflows `int64` for the largest
  values. M16 bounds the number (Requirement 8) because path-form adds a second
  way to supply one.
M16 is delivered as **two pull requests**, each a complete working increment
(see `tasks.md`):
- **PR 1, path-form pagination:** Requirements 1-9, 12-16 as they apply.
- **PR 2, the `?m=` date query:** Requirements 10, 11 and the cross-cutting
  requirements as they apply.
## Glossary
- **Path-form pagination** — a request path whose last two segments are the
  literal `page` and a page number, such as `/category/news/page/2/`.
- **Query-form pagination** — `?page=N`, grimoire's existing form, plus the
  `?paged=N` alias this milestone adds.
- **Base path** — the archive's canonical page-1 path as built by a
  `routing.Structure` constructor (`CategoryPath`, `TagPath`, `AuthorPath`,
  `DatePath`, `CustomArchivePath`, `CustomTermPath`), or `/` for the home page.
- **Page number** — an ASCII decimal integer from 1 to 999999999 written without
  a sign, a leading zero or any other character (`MaxPageNumber`).
- **Paginatable kind** — a classification that has a base path and a post list:
  `KindCategory`, `KindTag`, `KindAuthor`, `KindDate`, `KindCustomArchive`,
  `KindCustomTerm`, and the home page.
- **`Structure.Classify` / `Target`** — the pure, total classifier in
  `internal/routing/classify.go`. M16 adds `Target.Page` and `KindHome`.
- **`KindHome`** — a new `Kind`, returned only for a paginated home path
  (`/page/{n}`). `/` itself stays `KindNone` and stays owned by the `/` route.
- **`PaginatedPath(base, n)`** — the single construction site for a path-form
  URL, a pure `Structure` method that applies the trailing-slash rule.
- **Canonical form** — the one URL an archive page is addressed by for
  link-building and for the `Link: rel="canonical"` header. For page 2 and above
  it is the path form. For page 1 it is the base path.
- **Shadowed URL** — a URL that resolved to something before M16 and resolves to
  pagination after it (Requirement 3).
- **Date query** — the `m` query argument: `m=YYYY`, `m=YYYYMM` or `m=YYYYMMDD`.
- **Flat structure** — a `Structure` with `Flat` set: WordPress's plain
  permalinks, or the fallback for an unsupported `permalink_structure`.
- **Built-in goldens** — `cmd/grimoire/testdata/builtin_golden/*.json`.
## Inherited decisions
Settled here as fixed input to the requirements and the design.
| # | Decision | Consequence |
|---|---|---|
| D1 | **Pagination is path-form where WordPress is path-form, and a trailing `page/{n}` pair is pagination whenever the remainder is a paginatable archive.** The rule is WordPress's rewrite-rule order, applied in `Classify` before the post and page rows. | Requirements 2, 3. |
| D2 | **Both forms answer `200`. `?page=N` does not redirect.** The path form is canonical for page 2 and above; the query form carries a `Link: <path>; rel="canonical"` response header. | Requirement 6. Chosen because Requirement 32.3 and 33.2 say `?page=N` "continues to work unchanged", and because a `301` is cached by browsers and caches and would break a rollback. |
| D3 | **`/page/1/` redirects `301` to the base path.** Page 1 never has a `/page/1/` canonical. | Requirement 6. WordPress does the same. |
| D4 | **Theme links come from the handler**, as `PrevURL` and `NextURL` on `ArchiveData` and `IndexData`. | Requirement 7. `BaseURL` is unchanged so themes that build `{{.BaseURL}}?page=N` keep working. |
| D5 | **`?paged=N` is accepted as an alias** of `?page=N` on every paginated archive. `?page=N` wins when both are present. | Requirement 8. WordPress serves `?paged=N` under every structure, and plain-permalink sites publish those links. |
| D6 | **Path-form is served for every archive kind the structure serves.** Under a Flat structure that is home, category, tag, author and custom archives and terms. Date archives stay unserved under Flat. | Requirement 9. |
| D7 | **`?m=` is honored on `GET /` only, serves the date archive in place with `200`, and an invalid value is ignored.** | Requirements 10, 11. No URL that serves today changes status. |
| D8 | **No migration, no column, no capability, no port, no new dependency, no new `GRIMOIRE_TEST_*` variable.** | Requirement 12. |
| D9 | **Two PRs.** | Requirement 15. |
## Requirements
### Requirement 1: M16 is authorized and bounded
**User Story:** As the project owner, I want the optional M16 work recorded as
explicitly requested, so that roadmap Requirement 33.1 is visibly satisfied.
#### Acceptance Criteria
1. THE `requirements.md` SHALL record that the project owner explicitly requested `/page/{n}/` path-form pagination parity, satisfying roadmap Requirement 33.1.
2. THE system SHALL limit M16 to archive pagination and the `?m=` date query, and SHALL leave feeds, the REST API, sitemaps, comment pagination, the `<!--nextpage-->` page split inside a post, and every other query argument unchanged.
3. THE system SHALL keep roadmap Requirements 32.3 and 33.2 true: the `?page=N` form remains supported and behaves as it did, except for the bounds in Requirement 8.
### Requirement 2: Classify path-form pagination
**User Story:** As a visitor following a WordPress-style link, I want
`/category/news/page/2/` to show page 2 of the news category, so that links
written for WordPress resolve without a redirect.
#### Acceptance Criteria
1. WHEN a path ends in the segment `page` followed by a page number and the path without those two segments classifies as a paginatable kind, THE `Structure.Classify` SHALL return that kind's `Target` with `Page` set to the number and every other field exactly as the unpaginated path would give it.
2. WHEN a path is `/page/{n}` with a page number and nothing before it, THE `Structure.Classify` SHALL return `Target{Kind: KindHome, Page: n}`.
3. THE `Structure.Classify` SHALL evaluate the pagination rule before the M15 page row and before the structure's post row, so that a pagination path is never read as a post or a nested page.
4. IF the segment after `page` is not a page number (it has a sign, a leading zero, a non-digit, more than nine digits, or is `0`), THEN THE `Structure.Classify` SHALL NOT treat the path as path-form pagination and SHALL classify the whole path exactly as it did before M16.
5. IF the path without the `page/{n}` pair does not classify as a paginatable kind, THEN THE `Structure.Classify` SHALL classify the whole path exactly as it did before M16.
6. THE `Structure.Classify` SHALL set `Page` to `0` for every path that is not path-form pagination, and SHALL record the arriving slash form in `TrailingSlash` for a paginated path as it does for any other.
7. THE `Structure.Classify` SHALL return `KindNone` for `/` exactly as before, so the `/` route keeps owning the home page.
8. THE `Structure.Classify` SHALL stay pure and total: it SHALL read no database and SHALL never return an error.
### Requirement 3: Disambiguate the `page` segment and name every shadowed URL
**User Story:** As a site owner with a category called "page", I want to know
exactly which of my URLs the new rule can affect, so that nothing breaks
silently.
#### Acceptance Criteria
1. WHEN a path ends in `page` with no number after it, THE `Structure.Classify` SHALL classify it as before, so a term, page or post slugged `page` stays reachable at `/…/page/`.
2. WHEN a term slugged `page` is followed by a segment that is not a page number, THE `Structure.Classify` SHALL classify it as before, so `/category/news/page/archive/` still addresses the grandchild `archive`.
3. WHEN the path is a base followed by `page/{n}` and nothing between (for example `/category/page/2/` or `/tag/page/2/`), THE `Structure.Classify` SHALL classify it as before, because a bare base is not a paginatable kind. A category slugged `page` with a child slugged `2` therefore stays reachable at `/category/page/2/`.
4. THE system SHALL document, in `docs/compatibility.md`, that a URL whose last two segments are `page` and a page number now means pagination in these cases, matching WordPress's own rule order and with no fallback: a category or custom-taxonomy term whose parent is slugged `page` and whose own slug is a page number under a real category (`/category/news/page/2/`); a top-level page slugged `page` with a child slugged by a page number (`/page/2/`); and a post whose canonical path under the configured structure is exactly `/page/{n}/`.
5. IF a requested page number is past the last page of an archive that has posts, THEN THE system SHALL respond `404`, and SHALL NOT fall back to the shadowed interpretation.
6. THE system SHALL NOT reserve the slug `page` for terms, pages or posts at write time, because WordPress does not.
### Requirement 4: Paginate the home page at `/page/{n}/`
**User Story:** As a visitor, I want `/page/2/` to show the second page of the
home list, so that the home page paginates the WordPress way.
#### Acceptance Criteria
1. WHEN a request path classifies as `KindHome` with a page number 2 or above that is within range, THE system SHALL respond `200` with the home template and that page of the recent posts.
2. WHEN a request path classifies as `KindHome` with page number 1, THE system SHALL respond `301` to `/`, carrying the raw query string.
3. IF the page number is above the last page and the home list is not empty, THEN THE system SHALL respond `404`.
4. WHEN the home list is empty, THE system SHALL respond `200` for any page number, matching the existing empty-archive rule.
5. WHEN a request path is `/page/{n}` and the structure's trailing-slash form is the other one, THE system SHALL respond `301` to the canonical slash form, carrying the raw query string.
6. THE `/` route SHALL keep answering `GET /` and `GET /?page=N` exactly as before, within the bounds of Requirement 8.
### Requirement 5: Paginate every archive kind
**User Story:** As a visitor, I want path-form pagination on every archive, so
that no archive is left on the old form.
#### Acceptance Criteria
1. WHEN a request path is path-form pagination of a category archive, including a nested category, THE system SHALL respond with that page of the category archive, resolved through the same walk as page 1.
2. WHEN a request path is path-form pagination of a tag archive, an author archive, a year, month or day archive, a custom post type archive or a custom taxonomy term archive, THE system SHALL respond with that page of that archive, using the same service call and the same page size as page 1.
3. THE system SHALL render each paginated archive with the same template as its first page.
4. IF the page number is above the last page of an archive that has posts, THEN THE system SHALL respond `404`.
5. WHEN an archive has no posts, THE system SHALL respond `200` for any page number, matching the existing rule.
6. IF the archive entity does not exist (an unknown tag, author, term or category path), THEN THE system SHALL respond `404` before it considers the page number.
7. WHEN a category walk fails but the final segment names a category elsewhere in the taxonomy, THE system SHALL respond `301` to that category's canonical path with the same page form, so `/category/old/page/2/` redirects to `/category/new/old/page/2/`.
8. WHEN a custom taxonomy walk fails and recovers, THE system SHALL redirect the same way as Requirement 5.7.
9. THE system SHALL leave a hierarchical custom post type's single and a nested page path unaffected unless the path matches Requirement 2.1 or 2.2.
### Requirement 6: Canonical URL policy
**User Story:** As a site owner who cares about search ranking and old links, I
want one canonical URL per archive page and no breakage of existing links, so
that both forms are safe to publish.
#### Acceptance Criteria
1. WHEN a request carries `?page=N` or `?paged=N` and no path-form page, THE system SHALL respond as it did before M16 (`200`, or `404` past the last page, or the redirect an archive already issued), and SHALL NOT redirect to the path form.
2. WHEN a paginated archive responds `200` to a request whose query contains a `page` or `paged` argument, THE system SHALL add a `Link` response header with the relation `canonical` whose target is the archive page's canonical form: the path form for page 2 and above, and the base path for page 1.
3. THE `Link` header target SHALL be a relative path reference, built by `routing.Structure`, with no query string and no site-URL prefix.
4. WHEN a request carries both a path-form page and a `page` or `paged` query argument, THE system SHALL use the path-form number and ignore the query number.
5. WHEN a request path is path-form pagination with page number 1, THE system SHALL respond `301` to the base path with the raw query string, so page 1 never has a `/page/1/` canonical.
6. WHEN a request path is path-form pagination in the non-canonical trailing-slash form, THE system SHALL respond `301` to the canonical form with the raw query string.
7. THE `301` redirects of this milestone SHALL append the request's raw query string, as `redirectCanonical` does.
8. THE canonical form of page 1 of every archive SHALL be the base path, so M16 changes no REST `link` value and no existing redirect target.
9. WHEN a request path is not path-form pagination, THE system SHALL NOT add a `Link` header unless the query contains a `page` or `paged` argument.
### Requirement 7: Theme pagination links use the path form
**User Story:** As a site owner, I want the default theme's Previous and Next
links to point at the canonical URLs, so that crawlers and visitors see one URL
per page.
#### Acceptance Criteria
1. THE system SHALL build the previous and next page URLs of every paginated archive and of the home page in one `routing.Structure` method, from the base path, the current page and the page count, applying the structure's trailing-slash rule.
2. WHEN the current page is 2, THE previous URL SHALL be the base path and SHALL NOT contain `/page/1/`.
3. WHEN the current page is the first or the last, THE system SHALL leave the previous or next URL empty.
4. THE `render.ArchiveData` and `render.IndexData` SHALL carry `PrevURL` and `NextURL`, and THE default theme's `index.tmpl`, `category.tmpl` and `archive.tmpl` SHALL render them and SHALL NOT build a `?page=` link.
5. THE system SHALL keep `BaseURL` equal to the archive's canonical base path, so a custom theme that builds `{{.BaseURL}}?page=N` keeps producing working links.
6. THE system SHALL leave `render.ArchiveData` and `render.IndexData` fields other than the two additions unchanged, and SHALL leave `internal/render/testdata/golden/*.html` byte-identical.
### Requirement 8: Query-form pagination stays correct and bounded
**User Story:** As an operator, I want existing `?page=N` links to behave as
before, and no way to overflow the offset calculation.
#### Acceptance Criteria
1. THE system SHALL read the query page number from `page`, and, when `page` is absent, from `paged`.
2. WHEN the query page value is absent, empty, not an integer, zero or negative, THE system SHALL use page 1, as before.
3. WHEN the query page value is a positive decimal integer of any length above `MaxPageNumber`, including one too large for a 64-bit integer, THE system SHALL use `MaxPageNumber`, so the existing out-of-range rule answers `404` for an archive with posts and no offset arithmetic can overflow.
4. THE system SHALL apply Requirement 8 to the home page and to every archive kind.
5. THE REST API's `page` parameter and its pagination headers SHALL stay unchanged and SHALL NOT read `paged`.
### Requirement 9: Structure, slash and plain-permalink interaction
**User Story:** As a site owner, I want path-form pagination to follow my
permalink settings, so that it matches what WordPress would emit for my site.
#### Acceptance Criteria
1. WHILE the structure's `TrailingSlash` is set, THE `PaginatedPath` SHALL end in `/page/{n}/`, and WHILE it is not, THE `PaginatedPath` SHALL end in `/page/{n}`.
2. THE system SHALL register chi patterns for both slash forms of every path-form pagination route, so a handler can issue the redirect of Requirement 6.6 instead of the router answering `404`.
3. WHILE the structure is Flat, THE system SHALL serve path-form pagination for the home page and for the category, tag, author, custom archive and custom term archives, which a Flat structure already serves, and SHALL NOT serve date archives, as before.
4. WHILE the structure carries a front, THE pagination of an archive SHALL follow that archive's own prefix rule, because `PaginatedPath` takes the base path from the archive's constructor.
5. THE `category_base`, `tag_base` and author base rules SHALL stay as M9a and M9b defined them.
6. IF `category_base` or `tag_base` resolves to `page`, THEN THE `Structure.Notes()` SHALL include a diagnostic naming the option and stating that `/page/{n}` is home pagination and that a term slugged by a page number under that base is unreachable.
7. IF a registered custom post type or taxonomy has `page` as its slug or archive slug, THEN THE `Structure.CustomNotes()` SHALL include a diagnostic with the same statement.
8. THE diagnostics of Requirements 9.6 and 9.7 SHALL be decidable from the structure alone and SHALL read no database.
### Requirement 10: The `?m=` date query
**User Story:** As a visitor following a plain-permalink WordPress link such as
`/?m=202405`, I want the May 2024 archive, so that date links written for
WordPress resolve.
#### Acceptance Criteria
1. WHEN a `GET /` request carries a `m` argument of 4, 6 or 8 ASCII digits that names a real calendar date at that granularity (year, year and month, year and month and day), THE system SHALL respond `200` with the date archive for it, in place, with no redirect.
2. THE system SHALL read the same granularity and calendar rules as the date path form, so `m=202402` is February 2024 and `m=20240230` is not a date.
3. WHEN the structure serves date archives, THE system SHALL add the `Link` canonical header of Requirement 6.2 pointing at the date archive's path form, and SHALL build the previous and next page URLs from the path form.
4. WHILE the structure is Flat, THE system SHALL build the previous and next page URLs of a `?m=` archive as `/?m={value}&page={n}`, with `m` in the width the request used.
5. WHEN a `?m=` archive is paginated, THE system SHALL read the page from `page` or `paged` and SHALL apply the out-of-range rule of Requirement 5.4.
6. WHEN a request path is not exactly `/`, THE system SHALL ignore a `m` argument.
7. WHEN a request is path-form home pagination (`/page/{n}/`), THE system SHALL ignore a `m` argument.
### Requirement 11: Invalid `?m=` is safe
**User Story:** As an operator, I want a malformed `m` argument to do no harm and
change no URL that works today.
#### Acceptance Criteria
1. IF the `m` argument is empty, contains a character other than an ASCII digit, has a length other than 4, 6 or 8, or names no real calendar date, THEN THE system SHALL ignore it and serve the home page exactly as it would without it.
2. WHEN the request carries more than one `m` argument, THE system SHALL read only the first.
3. THE system SHALL NOT query the database for an invalid `m` argument.
4. THE system SHALL ignore the hour, minute and second forms of `m` that WordPress accepts (10, 12 or 14 digits), as in Requirement 11.1, and SHALL document that.
### Requirement 12: What M16 does not change
**User Story:** As a maintainer, I want everything M16 does not name to behave as
before.
#### Acceptance Criteria
1. THE system SHALL leave every status, header and body of the built-in golden files under `cmd/grimoire/testdata/builtin_golden/` byte-identical.
2. IF a golden file changes, THEN THE change SHALL be a separate commit that states the cause and is reviewed before the PR merges, and a golden file SHALL NOT be regenerated to make a failing test pass.
3. THE system SHALL add no migration, no column, no capability name, no domain port, no dependency and no `GRIMOIRE_TEST_*` variable.
4. THE system SHALL leave `go.mod` and `go.sum` unchanged.
5. THE system SHALL leave the REST API, the admin API and the `/wp-json` routes unchanged.
6. THE system SHALL leave the signatures of `Structure.Parse`, `Structure.ArchivePatterns`, `Structure.CustomPatterns` and `Structure.Classify` compatible, adding only fields, constants and methods.
### Requirement 13: Routing tests
**User Story:** As a maintainer, I want the classifier and the path constructors
covered by tables, so that a regression in the rule order is caught where it
happens.
#### Acceptance Criteria
1. THE `internal/routing` tests SHALL cover `Classify` over a matrix of structures (Flat, `/%postname%/`, `/%year%/%monthnum%/%postname%/`, a front such as `/blog/%year%/%monthnum%/%postname%/`, `/%post_id%/`, a trailing-slash and a no-trailing-slash form, a renamed `category_base`) against every row of the disambiguation matrix in `design.md`.
2. THE tests SHALL cover the fixed-point round trip: for every paginatable kind and page number `n` from 2, `Classify(PaginatedPath(base, n))` returns the same kind and entity as `Classify(base)` with `Page == n`, and `PaginatedPath` of that target's base returns the same path.
3. THE tests SHALL cover `PageLinks` at the first page, a middle page, the last page, a single page and an empty page count.
4. THE tests SHALL cover `PaginationPatterns` as an exact, duplicate-free set for each structure in the matrix, in both slash forms.
5. THE tests SHALL cover the notes of Requirements 9.6 and 9.7 and SHALL assert that `Notes()` and `CustomNotes()` stay byte-identical for a configuration with no `page` base.
6. THE tests SHALL cover the `?m=` parser (PR 2) over valid widths, the leap day, month and day zero, the wrong widths, a sign, a space and a non-digit.
### Requirement 14: Handler, wiring and real-database tests
**User Story:** As a maintainer, I want the behavior proved through the real
router and the real binary assembly, because M15 once shipped with its wiring
missing from the binary.
#### Acceptance Criteria
1. THE `internal/web` tests SHALL cover each archive kind's path form for `200`, past-the-end `404`, page 1 `301`, wrong slash `301`, an unknown entity `404`, and the raw query surviving each redirect.
2. THE `internal/web` tests SHALL cover the `Link` header on `?page=N` and `?paged=N`, its absence otherwise, the path-form number winning over a query number, the `?page=N` response being unchanged, and the router regression table for the new patterns against `/page`, `/page/x`, `/page/2/3` and a page slugged `page`.
3. THE `cmd/grimoire` tests SHALL drive every archive kind's path form, the `301` for page 1, the `Link` header, and the theme's rendered `Next` link through `buildHandler` over migrated SQLite, and THE tests SHALL fail if the pagination route registration, the classifier rule or the default theme change is removed.
4. EACH PR's task record SHALL state that the removal proof was run and what failed, as M20 does.
5. WHERE `GRIMOIRE_TEST_WP_DSN` is set, THE `internal/routing` real-database tests SHALL derive real category, tag, author, date and nested-page paths from the database rows, SHALL assert that each classifies with the new rule as it did before and that its `page/2` form classifies as the same kind with `Page == 2`, and SHALL count the shadowed URLs of Requirement 3.4 in the real data.
6. THE real-database tests SHALL use the existing gating and SHALL add no environment variable.
### Requirement 15: Delivery and TDD
**User Story:** As a reviewer, I want two small complete increments built test
first.
#### Acceptance Criteria
1. THE system SHALL be delivered as PR 1 (Requirements 2-9) and PR 2 (Requirements 10-11), and each PR SHALL leave `main` complete and working with no dead code.
2. THE PR 1 SHALL be mergeable without PR 2, and THE PR 2 SHALL be mergeable on top of PR 1 only.
3. THE tasks SHALL have a RED test task before each implementation task, with the failure reason stated, except documentation, wiring-proof and gate tasks.
4. THE system SHALL run `gofmt -l .` (empty), `go vet ./...`, `go build ./...` and `go test ./...` green after every phase.
5. EACH PR SHALL end with a final gate task that runs the full checks and the `git` checks that prove its no-migration, no-port and no-golden-change claims.
### Requirement 16: Documentation
**User Story:** As an operator and a reader of the plans, I want the documents to
state what shipped and to stop listing the closed divergence as open.
#### Acceptance Criteria
1. WHEN PR 1 merges, THE `docs/compatibility.md` Pagination passages (the M8 "Pagination is M8's contract" bullet, the M9b "Pagination is `?page=N`, where WordPress uses `/page/{n}/`" bullet, and the M12 "Archive pagination stays `?page=N`" sentence) SHALL be rewritten to describe both forms, the canonical policy, the shadowed URLs of Requirement 3.4, the `?paged=N` alias, the bound of Requirement 8 and the Flat behavior.
2. WHEN PR 2 merges, THE `docs/compatibility.md` sentence "The `?m=` query-argument form is not implemented either; grimoire has no query-argument route" SHALL be replaced by a description of the date query, including the ignored forms of Requirement 11.
3. THE `docs/user-guide.md` divergence bullet about pagination SHALL be updated by PR 1.
4. THE `plans/README.md` SHALL gain a `21-archive-path-pagination` row, partially shipped after PR 1 and implemented after PR 2.
5. WHEN PR 2 merges, THE roadmap task `16.A` in `plans/wordpress-core-parity-roadmap/tasks.md` SHALL be ticked and `plans/post-m10-gap-analysis/README.md` SHALL drop its M16 section and mark the ordering entry shipped.
6. THE documents SHALL record what stays open: feeds, REST, comment pagination, `<!--nextpage-->`, the hour, minute and second forms of `m`, and `?m=` on any path other than `/`.
## Out of scope
- **Feeds and sitemaps.** Neither exists in grimoire.
- **REST pagination.** `/wp-json` keeps `page`, `per_page` and the `X-WP-*` headers.
- **Comment pagination and `<!--nextpage-->`.** WordPress's `/comment-page-{n}/` and `/{post}/2/` are separate features.
- **Search results (`?s=`)** and **post-type archives by `?post_type=`.**
- **Making `page` a reserved slug.** WordPress does not, and Requirement 3 documents the consequence instead.
- **A fallback to the shadowed interpretation** when a page number is out of range (sign-off item 2 in `design.md`).
- **Changing the page size.** `content.DefaultPerPage` stays 10.
## Traceability
| Item | Requirements |
|---|---|
| Explicit-request condition (roadmap 33.1) | 1 |
| `/page/{n}/` classification (roadmap 32.1) | 2, 3, 4, 5, 9 |
| Disambiguation with a term slugged `page` (roadmap 32.1, 33.2) | 2, 3 |
| Canonical URL policy | 6, 7 |
| `?page=N` stays correct (roadmap 32.3) | 6, 8 |
| `?m=` (roadmap 32.2) | 10, 11 |
| Trailing slash, plain permalinks, REST, feeds | 9, 12 |
| Tests (roadmap 33.2) | 13, 14 |
| Delivery, docs | 15, 16 |
