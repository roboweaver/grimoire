# WordPress Core Parity Roadmap — Requirements

## Introduction

This spec picks up from `main` after PR #21 (the WordPress compatibility visual
tour docs). M1–M7 delivered content core, auth, the read-only and then
CRUD-capable Spectrum admin, comments/media/menus, the REST API, and
revisions/scheduling. `docs/compatibility.md` and
`docs/wordpress-compatibility-tour.md` now document, in one place, what still
diverges from a real WordPress site. This roadmap turns that documentation
into an ordered set of milestones that close the highest-value gaps first.

**Priority order (approved product decision): UI parity first, then deeper
compatibility.** Visitors and editors feel workflow and layout gaps
immediately; routing/taxonomy and REST-write gaps matter once a site is being
run day-to-day and integrated with more of the WordPress ecosystem. The
milestones below are ordered accordingly:

- **M8 — Content Browsing Parity**: no schema changes; closes the public
  pagination and admin/media filtering-and-display gaps identified in the
  current code.
- **M9 — Routing & Taxonomy Parity**: permalink structure/base options,
  canonical redirects, tag/date/author archives, nested categories.
- **M10 — REST Write & Content Safety Parity**: a write-boundary content
  safety policy first, then REST media/user writes, then `content.rendered`
  rendering improvements.

### Milestone numbering note

`plans/07-revisions-scheduler`'s own summary in `plans/README.md` says "REST
media/user writes explicitly deferred to M8." That forward reference predates
this roadmap. Per the approved UI-parity-first ordering, this roadmap assigns
**M8 to content browsing parity** and moves REST media/user writes into
**M10** instead. There is no remaining M7-era work named "M8"; the README
milestone-index row for this spec supersedes that forward reference.

### Specification depth

- **M8 is fully specified** in this document and in `design.md`/`tasks.md`:
  requirements are implementation-ready, and `tasks.md` breaks M8 into
  concrete, ordered engineering tasks.
- **M9 and M10 are specified at roadmap depth**: each requirement below is
  still testable and EARS-worded, and `design.md` gives each milestone an
  architecture sketch and a gap-to-rationale mapping, but neither milestone's
  task list is bite-sized. Both explicitly require their own follow-up spec
  (new `requirements.md`/`design.md`/`tasks.md`, e.g. under
  `plans/09-routing-taxonomy-parity` and
  `plans/10-rest-write-content-safety-parity`) before implementation begins.

### Compatibility boundary (applies to every requirement below)

- In scope: WordPress **core** data (the `wp_*`/`{prefix}*` tables grimoire
  already models), core routes, and core admin/editor workflows.
- Out of scope, explicitly: arbitrary third-party plugins and themes, custom
  post types/taxonomies beyond what grimoire already models (`post`, `page`,
  `category`, `post_tag`), and full Gutenberg block-editor rendering. A
  requirement that would require executing untrusted plugin/theme PHP is out
  of scope by definition, since grimoire has no PHP runtime.
- UI acceptance is **behavioral parity with the WordPress workflow**,
  evaluated with a side-by-side visual review at desktop and mobile sizes —
  not pixel-identical output, and Adobe React Spectrum remains the admin
  visual language.
- The primary scenario is running an existing WordPress site's data safely.
  Every requirement that touches storage is additive: no destructive schema
  change, and no requirement here depends on migrating data out of its
  current WordPress-compatible shape.

---

## M8 — Content Browsing Parity Requirements

### Requirement 1: Public Home Page Pagination

**User Story:** As a site visitor browsing the public home page, I want to
move between pages of posts and know how many pages exist, so that I can find
older content without guessing whether more exists.

#### Acceptance Criteria

1. WHEN a visitor requests `GET /` or `GET /?page=N` THE system SHALL return
   the total count of published posts and the resulting total page count
   alongside the requested page's posts, computed with `content.DefaultPerPage`
   as the page size.
2. WHEN the home page renders THE system SHALL make the total post count and
   total page count available to the `index` template (via a new
   `render.IndexData` field, or fields, carrying page/total/total pages)
   without changing the existing `SiteTitle`/`Tagline`/`Posts` fields.
3. WHEN the current page is not the last page THE index template SHALL render
   a link to the next page with the correct `?page=` value; WHEN the current
   page is not the first page THE index template SHALL render a link to the
   previous page.
4. WHEN the current page is the first page THE index template SHALL NOT
   render a "previous" link; WHEN the current page is the last page (or there
   is only one page) THE index template SHALL NOT render a "next" link.
5. WHEN the three conditions `page > 1`, `result.Total > 0`, and
   `page > result.TotalPages` are all true THE system SHALL respond
   `404 Not Found` instead of rendering an empty page, matching WordPress's
   own out-of-range pagination behavior. This exact three-part condition
   (not just "`page` exceeds `TotalPages`") is required so that a
   zero-post site never 404s regardless of the requested page (Acceptance
   Criterion 6 governs that case) and `page=1` never 404s even on an
   empty/one-page site.
6. WHEN zero posts are published THE system SHALL render the existing empty
   home page (unchanged) without an error and without pagination controls.
7. IF `?page=` is missing, non-numeric, zero, or negative THEN THE system
   SHALL treat the request as page 1, preserving `pageParam`'s current
   defaulting behavior.

### Requirement 2: Public Category Archive Pagination

**User Story:** As a site visitor browsing a category archive, I want the
same page-count-aware pagination available on the home page, so that category
browsing works the same way site-wide.

#### Acceptance Criteria

1. WHEN a visitor requests `GET /category/{slug}` or
   `GET /category/{slug}?page=N` THE system SHALL return the total count of
   published posts in that category and the resulting total page count
   alongside the requested page's posts.
2. WHEN the category page renders THE system SHALL make the total post count
   and total page count available to the `category` template (via new
   `render.CategoryData` fields) without changing the existing
   `SiteTitle`/`Tagline`/`Term`/`Posts` fields.
3. THE category template SHALL apply the same previous/next link rules as
   Requirement 1 (Acceptance Criteria 3–4), scoped to the category.
4. WHEN `page > 1 && result.Total > 0 && page > result.TotalPages` (the same
   three-part condition as Requirement 1.5) THE system SHALL respond
   `404 Not Found`.
5. WHEN the requested category slug does not exist THE system SHALL continue
   to respond `404 Not Found`, unchanged from current behavior, regardless of
   the `page` value.
6. WHEN the requested category slug exists but has zero published posts THE
   system SHALL render the existing empty category archive (unchanged)
   without an error and without pagination controls, for any `page` value —
   the Acceptance Criterion 4 condition's `result.Total > 0` clause already
   guarantees this, matching Requirement 1.6's zero-post-home-page rule.

### Requirement 3: Admin Content List Filtering

**User Story:** As a content editor, I want to filter the admin content list
by status, free-text search, and author, so that I can find items needing
action without paging through the entire list.

#### Acceptance Criteria

1. WHEN `GET /admin/api/posts` is requested with a `status` query parameter
   THE system SHALL restrict results to that status, reusing the existing
   `AdminPostFilter.Statuses` filter (already read by
   `AdminService.List`/`AdminPostRepository.ListForAdmin`).
2. WHEN `GET /admin/api/posts` is requested with a `search` query parameter
   THE system SHALL restrict results to posts whose title or content contains
   the term (case-insensitive substring match). The underlying
   `AdminPostFilter.Search` field and its query-building
   (`applyAdminSearch` in `internal/storage/wprepo/adminreads.go`, already
   called by both `ListForAdmin` and `CountForAdmin`) already exist and are
   already exercised by the REST admin-post read path; this milestone only
   has to wire the `search` query parameter through `AdminService.List`,
   the `adminPosts` HTTP handler, and `PostsList.tsx` — no repository-layer
   implementation work is required for search.
3. WHEN `GET /admin/api/posts` is requested with an `author` query parameter
   (a numeric user ID) THE system SHALL restrict results to posts authored by
   that user, via a new `AdminPostFilter.Author` field plumbed through
   `AdminService.List`, `AdminPostRepository.ListForAdmin`, and
   `AdminPostRepository.CountForAdmin` across all three storage vendors.
4. WHEN `status`, `search`, and `author` are combined with each other and with
   the existing `type`/`page`/`perPage` parameters THE system SHALL apply all
   of them together (logical AND), matching how `type`/`status` already
   combine today.
5. THE admin content list view (`PostsList.tsx`) SHALL expose UI controls for
   status, free-text search, and author, reflect their current values in the
   URL query string (alongside the existing `page` parameter), and re-fetch
   from page 1 whenever a filter value changes. The author control SHALL be
   a Spectrum `Picker` or `ComboBox` populated with author names (see
   Acceptance Criterion 7), not a raw numeric-ID `NumberField`, so an editor
   never has to know or type a user ID to filter by author.
6. WHEN the admin content list view is loaded from a URL that already
   contains `status`, `search`, or `author` query parameters THE view SHALL
   initialize its filter controls from those parameters (shareable/bookmarkable
   filtered views).
7. THE system SHALL expose a narrow author-options data source for the
   Acceptance Criterion 5 control: a new `GET /admin/api/authors` endpoint,
   gated by the same `edit_posts` capability already required by
   `GET /admin/api/posts`, backed by a new `AdminPostRepository.Authors`
   repository method that returns only the distinct `(id, display_name)`
   pairs for users who have authored at least one `post`/`page` (joined
   from `posts.post_author` to `users`) — never the full user list from
   `domain.UserRepository.List`, which is unscoped and would leak every
   account (including ones with no authored content) to any caller with
   only `edit_posts`, not full user-management capability. This mirrors
   WordPress's own author-dropdown behavior, which is scoped to users with
   content, not all registered accounts.

### Requirement 4: Admin Content List Invalid-Filter Handling

**User Story:** As a developer integrating with the admin API, I want an
invalid filter value to fail clearly, so that a typo does not silently return
an empty or unfiltered list.

#### Acceptance Criteria

1. WHEN `GET /admin/api/posts` is requested with a `status` value that is not
   one of the statuses grimoire's post lifecycle recognizes
   (`publish`, `draft`, `pending`, `private`, `future`) THE system SHALL
   respond `400 Bad Request` using the existing admin API error envelope
   (`{"error":{"code":"...","message":"..."}}`, written by `writeJSONError`
   in `internal/web/adminapi.go`), instead of the current behavior of
   silently returning zero rows.
2. WHEN `GET /admin/api/posts` is requested with an `author` value that is
   not a positive integer THE system SHALL respond `400 Bad Request` using
   the same `writeJSONError` envelope.
3. WHEN `GET /admin/api/media` is requested with a `type` value that is not
   one of the supported mime-type families (see Requirement 5) THE system
   SHALL respond `400 Bad Request` using the same `writeJSONError` envelope.
4. WHEN `GET /admin/api/media` is requested with a `parentId` value that is
   present but not a non-negative integer THE system SHALL respond
   `400 Bad Request` (today, `atoiDefault` silently treats it as `0`/unset)
   using the same `writeJSONError` envelope.
5. A missing or empty filter parameter SHALL NOT trigger a `400` — only a
   present-but-invalid value does. This negative case (parameter absent,
   or present-but-empty-string) SHALL be covered by an explicit test for
   each of `status`/`author`/`type`/`parentId`, alongside the positive
   `400` tests for Acceptance Criteria 1–4 (see Requirement 9.2).

### Requirement 5: Media Library Filtering

**User Story:** As a content editor with a growing media library, I want to
search and filter attachments by type, date, and parent post, so that I can
find a specific file without scrolling through everything.

#### Acceptance Criteria

1. WHEN `GET /admin/api/media` is requested with a `search` query parameter
   THE system SHALL restrict results to attachments whose title or filename
   contains the term (case-insensitive substring match).
2. WHEN `GET /admin/api/media` is requested with a `type` query parameter of
   `image`, `video`, `audio`, or `document` THE system SHALL restrict results
   to attachments whose `post_mime_type` prefix matches that family (e.g.
   `image/*` for `image`).
3. WHEN `GET /admin/api/media` is requested with `after`/`before` query
   parameters (ISO-8601 dates) THE system SHALL restrict results to
   attachments whose `post_date` falls within the given bound(s).
4. THE `GET /admin/api/media` `parentId` filter (already accepted by the
   handler) SHALL continue to restrict results to attachments attached to
   that post, unchanged, and SHALL compose with `search`/`type`/`after`/
   `before`.
5. THE new filter fields SHALL be added to `domain.MediaFilter` and
   implemented once in `internal/storage/wprepo` (the shared Bun-based query
   layer), so all three storage vendors (SQLite, MySQL, Postgres) gain the
   capability from a single implementation plus per-vendor contract tests.
6. THE media library view (`Media.tsx`) SHALL expose UI controls for search,
   type, date range, and parent-post filters, reflect their current values in
   the URL query string, and re-fetch from page 1 whenever a filter value
   changes.

### Requirement 6: Media Library Grid/List View Toggle

**User Story:** As a content editor, I want to switch the media library
between a grid (thumbnail) view and a list (detail) view, so that I can pick
the layout suited to what I'm looking for, without both views competing for
space on screen.

#### Acceptance Criteria

1. THE media library view SHALL render **either** the grid view **or** the
   list view for a given set of results, never both at once — replacing the
   current behavior of `Media.tsx` unconditionally rendering both a `Grid`
   and a `TableView` for the same items.
2. THE media library view SHALL provide a control to switch between grid and
   list view.
3. WHEN the view mode changes THE system SHALL preserve the current page and
   active filters (no data re-fetch is required purely for a view-mode
   change).
4. THE selected view mode SHALL persist across a page reload within the same
   browser (e.g. via the URL query string or local storage), so a user's
   preference is not reset on every navigation.
5. Both view modes SHALL remain usable and readable at common desktop and
   mobile viewport widths, per the approved UI acceptance criterion
   (behavioral/responsive parity, not pixel identity).

### Requirement 7: Media Library Pagination

**User Story:** As a content editor, I want the media library to be paginated
like the rest of the admin, so that a large library loads quickly and I can
navigate it predictably.

#### Acceptance Criteria

1. `Media.tsx` SHALL request and render one page of results at a time (not
   the current unconditional single unpaged fetch), passing `page` and
   `perPage` to `api.media(...)`.
2. `Media.tsx` SHALL maintain the current page in the URL query string,
   following the same pattern already used by `PostsList.tsx`.
3. `Media.tsx` SHALL render pagination controls (previous/next, current page,
   total pages, total item count) once results are loaded, matching the
   pagination presentation already used by `PostsList.tsx`.
4. THE frontend API client's `media()` method SHALL forward `parentId` (which
   the backend already accepts) plus the new `search`/`type`/`after`/`before`
   parameters from Requirement 5, in addition to the `page`/`perPage`
   parameters it forwards today.

### Requirement 8: Shared Paginated Result Contracts and Vendor-Neutral Filters

**User Story:** As a developer extending grimoire's list endpoints, I want a
single, consistent shape for "a page of results plus pagination metadata," so
that public and admin endpoints stay easy to reason about and the storage
layer stays vendor-neutral.

#### Acceptance Criteria

1. Every list endpoint touched by this milestone (public home, public
   category, admin posts, admin media) SHALL report pagination metadata using
   the same field shape already used by `AdminList`/`postsResponse`/
   `mediaListResponse` (`Page`, `PerPage`, `Total`, `TotalPages`), extended
   to the public `render.IndexData`/`render.CategoryData` structs rather than
   introducing a second, differently-shaped pagination contract. `TotalPages`
   SHALL be `0` when `Total` is `0`, at every one of these four endpoints
   (matching `AdminService.List`'s existing `(total+limit-1)/limit` integer
   division, which already yields `0` for zero results) — `adminMediaList`'s
   current behavior of clamping `TotalPages` to a minimum of `1` for zero
   results SHALL be corrected to match this shared contract.
2. Every new or extended filter (`domain.AdminPostFilter.Author`,
   `domain.MediaFilter.Search`/`Type`/`After`/`Before`, the new
   `AdminPostRepository.Authors` author-options read, and the new public
   post/category count reads) SHALL be expressed as a `domain` package
   interface/struct extension implemented once in
   `internal/storage/wprepo`, not duplicated per storage vendor.
3. No new database table or column SHALL be introduced by this milestone; all
   new filters and counts SHALL query columns that already exist in
   grimoire's WordPress-compatible schema (`post_status`, `post_author`,
   `post_mime_type`, `post_date`, `post_parent`, `post_title`,
   `post_content`, `postmeta.meta_value` — already `JOIN`ed in
   `internal/storage/wprepo/media.go`'s existing `listQuery`/`Count` queries
   to resolve the `_wp_attached_file` filename, so Requirement 5.1's
   title-or-filename media search reuses this existing join rather than
   adding a new one).

### Requirement 9: M8 Test and Review Coverage

**User Story:** As a maintainer, I want M8's new behavior verified the same
way every prior milestone was, so that vendor parity and UI behavior are both
protected by tests, not just manual inspection.

#### Acceptance Criteria

1. THE new/extended repository behavior (public post/category counts, admin
   `Author` filter, the new `Authors` author-options read, media
   `Search`/`Type`/`After`/`Before` filters) SHALL be covered by cross-vendor
   contract tests in `internal/storage/storagetest`, run against SQLite,
   MySQL, and Postgres via the existing `NewReposFunc` pattern.
2. THE new/changed HTTP handlers (`home`, `category`, `adminPosts`,
   `adminMediaList`, `adminAuthors`) SHALL be covered by Go handler tests
   for: a valid filtered/paginated request; an invalid-filter `400`
   (Requirement 4.1–4.4); a missing/empty-filter non-`400` (Requirement
   4.5); a zero-result `TotalPages == 0` response (Requirement 8.1); and an
   out-of-range-page `404` (Requirements 1.5, 2.4) alongside a zero-post-site
   non-`404` (Requirements 1.6, 2.6).
3. THE new/changed React views (`PostsList.tsx`, `Media.tsx`, and any
   extracted shared pagination/filter components) SHALL be covered by
   interaction tests (filter changes trigger a re-fetch with the right
   params and reset to page 1; view-mode toggle switches rendered content)
   and accessibility checks (keyboard operability and labeling of filter
   controls and the grid/list toggle).
4. Before this milestone is considered done, the public pages and the admin
   content/media list views SHALL be reviewed side by side against current
   `main` at both a desktop and a mobile viewport width, confirming
   behavioral parity with the WordPress workflow being matched — not pixel
   identity.

---

## M9 — Routing & Taxonomy Parity Requirements

> Roadmap-depth requirements. Each is testable, but implementation does not
> start from this document alone — see "Specification depth" above.

### Requirement 10: Permalink Structure and Base Options

**User Story:** As a site owner who has already configured permalinks in
WordPress, I want grimoire to honor those settings when it reads my existing
database, so that my site's URLs do not change when I move to grimoire.

#### Acceptance Criteria

1. WHEN grimoire reads the `{prefix}options` table THE system SHALL read the
   `permalink_structure`, `category_base`, and `tag_base` option values
   (grimoire already models the options table via `domain.Option`/
   `OptionsService`; these three keys are not currently read).
2. WHEN `permalink_structure` is empty (WordPress's "plain" permalinks) THE
   system SHALL continue to serve the current flat `/{slug}` post routes and
   `/category/{slug}` category routes unchanged.
3. WHEN `permalink_structure` uses a supported core token layout (see
   Requirement 11) THE system SHALL serve posts and categories at the
   corresponding computed path instead of the flat path.
4. WHEN `category_base`/`tag_base` are set to a non-default value THE system
   SHALL use that base segment in category/tag archive URLs instead of the
   hard-coded `category`/`tag` segments.

### Requirement 11: Common Permalink Tokens and Canonical Redirects

**User Story:** As a site visitor following an old link to a post, I want to
land on the post even if the URL uses a permalink format grimoire does not
serve natively, so that links from search engines and other sites keep
working.

#### Acceptance Criteria

1. THE system SHALL support resolving the common WordPress permalink tokens
   `%postname%`, `%year%`, `%monthnum%`, `%day%`, and `%post_id%` in whatever
   combination `permalink_structure` specifies, for post and page routes.
2. WHEN a request matches a post/page by a supported token combination but
   not at that post's canonical computed path THE system SHALL issue a
   `301` redirect to the canonical path, mirroring WordPress's
   `redirect_canonical` behavior.
3. WHEN a request does not match any supported token combination or any
   known route THE system SHALL respond `404 Not Found`, unchanged from
   today.
4. Custom rewrite rules registered by arbitrary plugins are out of scope
   (Compatibility boundary); only the core token set in Acceptance Criterion
   1 is required.

### Requirement 12: Tag, Date, and Author Archives

**User Story:** As a site visitor, I want to browse posts by tag, by
publish date, and by author, the same way I can already browse by category,
so that the site's core archive navigation is complete.

#### Acceptance Criteria

1. THE system SHALL serve a tag archive (analogous to today's category
   archive) at a route derived from `tag_base`, listing published posts
   tagged with that term, with the same total-count-aware pagination
   Requirement 2 adds for categories.
2. THE system SHALL serve a date archive (year, year/month, and
   year/month/day granularity) listing published posts in that date range,
   with the same pagination behavior.
3. THE system SHALL serve an author archive listing published posts by a
   given author, with the same pagination behavior.
4. Each new archive type SHALL respond `404 Not Found` for an archive with
   zero matching published posts only when the referenced entity itself
   (tag, author) does not exist; an existing tag/author with zero published
   posts SHALL render an empty archive, matching the category archive's
   current behavior for an existing-but-empty category.

### Requirement 13: Nested Category Hierarchy

**User Story:** As a site owner with a nested category structure in
WordPress (e.g. "News > Local"), I want grimoire to preserve and expose that
hierarchy, so that child-category URLs and listings work the way they did on
WordPress.

#### Acceptance Criteria

1. THE system SHALL read the `term_taxonomy.parent` column (not read by any
   current query) into the `domain.Term` model as a parent term reference.
2. WHEN a category has a parent THE system SHALL serve it at a nested route
   reflecting its ancestry (e.g. `/category/news/local`), in addition to (or
   instead of, depending on `design.md`'s resolution) today's flat
   `/category/{slug}` route.
3. A category archive for a parent category SHALL include posts assigned
   directly to that category; whether it also includes descendant
   categories' posts (matching WordPress's default `category` archive
   behavior) SHALL be resolved and stated explicitly in this milestone's own
   design document before implementation.
4. Arbitrary custom taxonomies beyond `category`/`post_tag` remain out of
   scope (Compatibility boundary).

### Requirement 14: M9 Exclusions and Test Coverage

#### Acceptance Criteria

1. Arbitrary plugin-registered rewrite rules, custom post types, and custom
   taxonomies SHALL remain explicitly out of scope for M9, consistent with
   the compatibility boundary.
2. M9's routing and taxonomy behavior SHALL be covered by cross-vendor
   repository contract tests and by fixture-based tests using at least one
   imported real-WordPress-database fixture (mirroring the existing
   real-WordPress-DB validation approach from `plans/02.1-wp-hash-real-db`),
   so permalink/taxonomy parity is checked against actual WordPress data
   shapes, not only synthetic fixtures.
3. Before implementation, M9 SHALL have its own `requirements.md`/
   `design.md`/`tasks.md` refining Requirements 10–13 into concrete,
   file-level, bite-sized tasks, per "Specification depth" above.

---

## M10 — REST Write & Content Safety Parity Requirements

> Roadmap-depth requirements. Each is testable, but implementation does not
> start from this document alone — see "Specification depth" above.

### Requirement 15: Capability-Aware Write-Boundary Content Policy

**User Story:** As a site owner, I want untrusted input accepted through the
REST API, the admin API, and public comment submission to be sanitized
before it is stored or rendered, while content already trusted from an
imported WordPress database keeps rendering as HTML, so that opening up more
REST write endpoints does not introduce a new stored-XSS surface.

#### Acceptance Criteria

1. THE system SHALL define a single, capability-aware content-sanitization
   policy applied at the write boundary for REST/admin `post_content`,
   `post_title`, `post_excerpt`, comment content, and (per Requirement 17)
   user profile fields — before this policy exists, no new write surface
   from Requirements 16–17 SHALL be enabled.
2. Content written through this milestone's new write paths SHALL be
   sanitized according to the caller's capabilities (e.g. `unfiltered_html`
   equivalent for the most trusted roles, matching WordPress's own
   capability-gated HTML filtering), never trusted verbatim by default.
3. Content already present in an imported WordPress database (read via M1's
   existing read paths, not written through this milestone's new write
   paths) SHALL continue to render as trusted HTML, unchanged — this
   requirement governs *new* write paths, not existing reads, so it does not
   regress M1–M7's "read WordPress content as-is" behavior.
4. THE policy SHALL be implemented as a single reusable component (not
   duplicated per endpoint), consistent with the codebase's existing
   single-implementation-per-concern pattern (e.g. `pkg/extensions`,
   `internal/storage/wprepo`).

### Requirement 16: REST Media Write Parity

**User Story:** As an integrator using the WordPress REST API against a
grimoire site, I want to create and update media (attachments) through
`/wp-json/wp/v2/media`, so that existing WordPress REST tooling for media
keeps working.

#### Acceptance Criteria

1. WHEN an authenticated caller with the appropriate capability
   (`upload_files`, matching WordPress) sends `POST /wp-json/wp/v2/media`
   THE system SHALL create an attachment, reusing M4's existing
   `MediaWriter.Create`/upload validation, and SHALL respond `501` no longer.
2. WHEN an authenticated caller with the appropriate capability sends
   `POST /wp-json/wp/v2/media/{id}` (WordPress's REST update verb) THE
   system SHALL update the attachment's writable fields (at minimum title
   and parent, reusing M4's `MediaWriter.SetParent`).
3. WHEN a caller lacks the required capability THE system SHALL respond
   `403`, matching M5/M6's existing REST capability-gating pattern.
4. Media content (`post_content`/title) written via this endpoint SHALL pass
   through the Requirement 15 sanitization policy before this endpoint may
   return anything other than `501`.

### Requirement 17: REST User Write Parity

**User Story:** As a site administrator using the WordPress REST API, I want
to create and update users through `/wp-json/wp/v2/users`, so that existing
WordPress user-management tooling keeps working.

#### Acceptance Criteria

1. WHEN an authenticated caller with the appropriate capability
   (`create_users`/`edit_users`, matching WordPress) sends
   `POST /wp-json/wp/v2/users` or `POST /wp-json/wp/v2/users/{id}` THE
   system SHALL create or update the user, reusing/extending the existing
   `domain.UserRepository` write surface, and SHALL respond `501` no longer.
2. Password handling for created/updated users SHALL reuse M2/M2.1's
   existing password hashing (bcrypt for new passwords; `$wp$` verification
   already in place for imported hashes) rather than introducing a second
   hashing scheme.
3. WHEN a caller lacks the required capability THE system SHALL respond
   `403`.
4. User profile fields (e.g. `description`) written via this endpoint SHALL
   pass through the Requirement 15 sanitization policy before this endpoint
   may return anything other than `501`.

### Requirement 18: `content.rendered` Improvements

**User Story:** As a REST API consumer reading `content.rendered` from a
grimoire site, I want it to look like WordPress's own rendered output —
without raw Gutenberg block-comment delimiters and with responsive image
markup — so that content displays correctly in REST clients that assume
WordPress's rendering conventions.

#### Acceptance Criteria

1. WHEN a post's stored `post_content` contains Gutenberg block-comment
   delimiters (`<!-- wp:paragraph -->` and similar) THE system SHALL strip
   them from the REST `content.rendered` field, without altering the
   underlying stored `post_content`. This requirement governs the REST API
   response only; the public-facing HTML templates
   (`internal/render`/`themes/default/templates/*.tmpl`) are out of scope
   for this milestone and continue to render `post_content` exactly as they
   do today, so no SEO regression risk is introduced (see design.md's SEO
   Considerations).
2. WHEN the REST `content.rendered` field contains an `<img>` tag
   referencing a media library attachment THE system SHALL add
   `srcset`/`sizes` attributes when size variants are available, and SHALL
   add `loading="lazy"`/`decoding="async"` attributes, matching WordPress's
   own responsive-image and lazy-loading markup; when no size variants are
   available, `srcset`/`sizes` SHALL be omitted from that `<img>` tag rather
   than emitting an incorrect value, while `loading`/`decoding` SHALL still
   be added.
3. This requirement governs *REST response rendering*, not storage:
   `post_content` in the database SHALL NOT be rewritten by this milestone,
   and the public HTML templates SHALL NOT be modified by this milestone
   (Acceptance Criterion 18.1).
4. Full Gutenberg block *editing*/re-rendering beyond delimiter-stripping and
   responsive-image markup (e.g. rendering arbitrary core/plugin blocks'
   visual output) remains out of scope (Compatibility boundary).

### Requirement 19: M10 Exclusions and Test Coverage

#### Acceptance Criteria

1. Arbitrary plugin-contributed REST fields, meta boxes, and custom
   Gutenberg blocks SHALL remain out of scope for M10.
2. Every new write endpoint SHALL be covered by a capability-matrix test
   (allowed vs. forbidden caller) and a content-safety test proving
   sanitization is applied, before it is considered done.
3. Before implementation, M10 SHALL have its own `requirements.md`/
   `design.md`/`tasks.md` refining Requirements 15–18 into concrete,
   file-level, bite-sized tasks, per "Specification depth" above, with the
   content-safety policy (Requirement 15) implemented and reviewed *before*
   Requirements 16–17's write endpoints are enabled.
