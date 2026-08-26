# WordPress Core Parity Roadmap: Tasks

This file is the actionable roadmap for M8/M9/M10. M8's tasks are
implementation-ready: each cites exact files, exact existing symbols, and
the requirement/AC it satisfies. M9 and M10 are broken into concrete task
*groups* that name the exact files/interfaces involved, but each group
explicitly requires its own dedicated spec (`requirements.md`/`design.md`/
`tasks.md` refinement) before implementation begins — this file does not
authorize writing M9/M10 application code.

No task in this file authorizes running a database migration; M8 needs
none (see `design.md`'s "M8 — Migration safety" section), and M9/M10's
migration needs (if any) are deferred to their own specs.

---

## M8 — Content Browsing Parity (implementation-ready)

### Task 1: Add the new domain filter fields and the term-scoped post counter

**Requirements:** 3.1–3.4, 5.1–5.4, 8.1, 8.3

- [ ] 1.1 In `internal/domain/repository.go`, add `Author int64` to
      `AdminPostFilter` (zero = unfiltered, matching the existing
      zero-value convention already used by `ParentID` on `MediaFilter`).
- [ ] 1.2 In `internal/domain/repository.go`, add `Search string`,
      `Type string`, `After time.Time`, `Before time.Time` to
      `MediaFilter` (zero values unfiltered; `Type` is one of
      `image`/`video`/`audio`/`document`, validated at the HTTP layer in
      Task 6, not in the domain struct itself).
- [ ] 1.3 Add a new method to resolve a term-scoped published-post count.
      Add it to `domain.TermRepository` as
      `CountByTermSlug(ctx context.Context, taxonomy, slug string) (int, error)`,
      documented as: "returns the number of published posts
      (`post_status = 'publish'`) attached to the given taxonomy/slug pair,
      or 0 if the term does not exist." (A term that does not exist is not
      an error here — the caller already resolves term existence via
      `BySlug` before needing the count.)
- [ ] 1.4 Implement `CountByTermSlug` on `*TermRepo` in
      `internal/storage/wprepo/repo.go`, alongside the existing `TermRepo`
      methods already defined there (`BySlug` at line 170,
      `ListByTaxonomy` at line 190, `TermsByIDs` at line 211) — as a live
      `COUNT(*)` over `{prefix}posts` joined to `{prefix}term_relationships`
      and `{prefix}term_taxonomy`, filtered by `taxonomy`, `slug`, and
      `post_status = 'publish'` — mirroring `CountForAdmin`'s existing
      join shape in `adminreads.go`, not the cached `term_taxonomy.count`
      column (see `design.md`'s rationale for this choice). Note this is
      distinct from the existing `TermRepo.CountTerms` in `adminreads.go`
      (line 119), which counts *terms* in a taxonomy, not *posts* in one
      term — do not conflate the two.
- [ ] 1.5 Add a cross-vendor contract test for `CountByTermSlug` in the
      existing `internal/storage/storagetest/termreader_contract.go`,
      covering: a term with N published + M draft posts (expect N), a term
      with zero posts (expect 0), and an unknown slug (expect 0, no error).
- [ ] 1.6 Run `go test ./internal/storage/... ./internal/domain/...` and
      confirm the new contract cases pass on every configured vendor.

### Task 2: Add pagination totals to `PostService.Recent` and `TermService.Category`

**Requirements:** 1.1–1.5, 2.1–2.5, 8.1

- [ ] 2.1 In `internal/content/post.go`, change `NewPostService` to accept
      a second parameter `pc domain.PostCounter`, storing it alongside the
      existing `posts domain.PostRepository` field.
- [ ] 2.2 Add a small shared result type (e.g. in `pagination.go`, since
      both `PostService` and `TermService` need the same shape) —
      `type Page struct { Total int; TotalPages int }` — and change
      `PostService.Recent`'s return type from `([]domain.Post, error)` to
      `([]domain.Post, Page, error)`, computing `Total` via
      `s.pc.CountByStatus(ctx, "post", "publish")` and `TotalPages` via a
      new small exported `TotalPages(total, perPage int) int` function in
      `pagination.go`, computing `(total + perPage - 1) / perPage`
      (`perPage <= 0` returns `0`) — this is the same ceiling-division
      formula `AdminService.List` already computes inline in
      `adminread.go` (`totalPages := (total + limit - 1) / limit`);
      extracting it here gives `PostService`, `TermService`,
      `AdminService`, and the new media service (Task 6) one shared
      implementation instead of a fourth inline duplicate. Update
      `AdminService.List` in `adminread.go` to call the new shared
      `TotalPages` helper in place of its existing inline formula, as a
      pure refactor (no behavior change — same tests must still pass).
- [ ] 2.3 In `internal/content/term.go`, `TermService` already has both
      `terms domain.TermRepository` and `posts` dependencies from its
      existing constructor — no constructor signature change is needed here
      (unlike `PostService` in Task 2.1). Add the term-count call inside
      `Category` using the new `domain.TermRepository.CountByTermSlug` from
      Task 1, returning the same `Page` shape as `PostService.Recent`.
- [ ] 2.4 Update `internal/content/post.go`'s `NewPostService` signature
      itself, then every call site, to pass the new second argument.
      `NewPostService(` appears 20 times across 18 Go files today
      (confirmed via `grep -rn "NewPostService(" --include="*.go" .`); one
      of those 20 is the function definition in `post.go` itself, leaving
      19 actual call sites across the other 17 files: the production
      wiring at `cmd/grimoire/main.go:108`
      (`content.NewPostService(repos.Posts)` →
      `content.NewPostService(repos.Posts, repos.PostCounter)`, where
      `PostCounter domain.PostCounter` is a field on `storage.Set`
      (`internal/storage/factory.go:51`), and `storage.Repositories`
      embeds `Set` anonymously (`internal/storage/factory.go:67`), so
      `repos.PostCounter` is accessed via Go's embedded-field promotion,
      not as a field declared directly on `Repositories`); 9 call sites in
      `internal/web/*_test.go` (one each in `handlers_test.go`,
      `auth_test.go`, `rest_terms_test.go`,
      `rest_apppassword_comments_test.go`, `rest_router_test.go`,
      `adminroutes_test.go`, `admine2e_test.go`, `rest_posts_write_test.go`,
      `comments_public_test.go`), same `repos.Posts`/`repos.PostCounter`
      substitution; 7 call sites across 6 files in `test/e2e/`
      (`auth_test.go` has 2, `m4_test.go`/`m5_test.go`/`m6_test.go`/
      `m7_test.go`/`smoke_test.go` have 1 each), same substitution; and 2
      call sites in `internal/content/post_test.go` using a fake
      `PostRepository` directly, each needing a fake `PostCounter` argument
      too. Fix any other call site the compiler flags after this change.
- [ ] 2.5 `internal/content/post_test.go` and `internal/content/term_test.go`
      both already exist today (confirmed via `ls internal/content/`); this
      task extends them rather than creating new files. Also note: Task
      1.3 adds `CountByTermSlug` to the `domain.TermRepository` interface,
      so `term_test.go`'s existing `fakeTermRepo` (currently implements
      only `BySlug`) stops satisfying `domain.TermRepository` — and thus
      stops compiling as an argument to `NewTermService(t
      domain.TermRepository, ...)` — the moment Task 1.3 lands; add a
      `CountByTermSlug` method to `fakeTermRepo` (a settable
      `count int`/`err error` pair returned from the method, matching its
      existing `term`/`err` field style) as part of this task, not left for
      the compiler to discover later. In both test files, using a fake
      `PostRepository`/`PostCounter`/`TermRepository`: assert `Recent`/
      `Category` return the correct `Page.Total`/`Page.TotalPages` for a
      known fixture of posts, and that a page number beyond `TotalPages`
      still returns an empty slice (not an error) — the out-of-range→404
      decision belongs to the HTTP handler (Task 3), not the service.
- [ ] 2.6 Run `go test ./internal/content/...` and confirm all pass.

### Task 3: Wire public handlers to 404 on out-of-range pages and render totals

**Requirements:** 1.1–1.5, 2.1–2.5

- [ ] 3.1 In `internal/render/view.go`, add `Page`, `TotalPages`, `Total int`
      fields to both `IndexData` (line 32) and `CategoryData` (line 51).
- [ ] 3.2 In `internal/web/handlers.go`'s `home` handler: after calling
      `PostService.Recent`, if `page > 1 && result.Total > 0 && page >
      result.TotalPages`, return `domain.ErrNotFound` so the existing
      `s.handler()` middleware wrapper (`internal/web/middleware.go:18-29`)
      renders the standard plain-text `404 Not Found` — the same mechanism
      already used for an unknown slug, no new helper is introduced;
      otherwise populate the new `IndexData` fields. A zero-post site
      (`result.Total == 0`) must render normally (empty list, no 404) for
      any page value, per Requirement 1.6.
- [ ] 3.3 Apply the same change to the `category` handler using
      `CategoryData` and `TermService.Category`'s new `Page` return value:
      an unknown category slug continues to 404 via the existing
      `domain.ErrNotFound` path (unchanged), an out-of-range page for a
      *known* category slug with `Total > 0` now also returns
      `domain.ErrNotFound` (same condition as 3.2), and a known category
      with zero posts renders empty for any page value, per Requirement 2.6.
- [ ] 3.4 Update the public pagination templates at
      `themes/default/templates/index.tmpl` and
      `themes/default/templates/category.tmpl` to add a previous/next
      control, shown only when `TotalPages > 1`, matching the existing
      template styling conventions already used for other list rendering
      in these templates.
- [ ] 3.5 Extend the existing `internal/web/handlers_test.go` with cases
      for: a page within range returns `200` with correct pagination
      fields in the rendered output; a page beyond `TotalPages` on a
      non-empty home/category returns `404`; a zero-post home page and a
      known-but-empty category both return `200` (empty list) for any page
      value; an unknown category slug still returns `404` (regression check
      on existing behavior).
- [ ] 3.6 Run `go test ./internal/web/... ./internal/render/...` and
      confirm all pass.

### Task 4: Extend `AdminService.List` with search/author and add input validation

**Requirements:** 3.1–3.5, 4.1–4.4

- [ ] 4.1 In `internal/content/adminread.go`, change `AdminService.List`
      (`internal/content/adminread.go:72`, currently
      `List(ctx context.Context, page, perPage int, typ, status string)
      (AdminList, error)`) to
      `List(ctx context.Context, page, perPage int, filter domain.AdminPostFilter)
      (AdminList, error)`: `page`/`perPage` stay explicit parameters exactly
      as today (the method still computes `limit, offset := clamp(page,
      perPage)` internally and sets `filter.Limit`/`.Offset` from that
      result, so no clamping logic moves to the caller); `typ, status
      string` are removed as separate parameters and replaced by
      `filter.Types`/`.Statuses` (unchanged `[]string` shape — the caller
      now builds the single-element slice itself instead of `List` doing
      it); the two new values arrive as `filter.Search`/`.Author` (`Author`
      added to `domain.AdminPostFilter` in Task 1.1). This mirrors
      `MediaService.List(ctx, filter domain.MediaFilter)`
      (`internal/content/media.go`) — the existing filter-struct precedent
      in this codebase — while diverging from it deliberately on
      `page, perPage`, since (unlike `MediaService.List`) `AdminService.List`
      is the layer that owns clamping today and this task does not change
      that. Call `s.posts.ListForAdmin(ctx, filter)` and
      `s.posts.CountForAdmin(ctx, filter)` (both already accept the full
      `domain.AdminPostFilter`, so no repository signature change is
      needed). Note what `AdminService.List` does **today**
      (`adminread.go:88`, current code): it does *not* forward the same
      filter to both calls — it rebuilds a brand-new filter literal for the
      `CountForAdmin` call containing only `Types`/`Statuses`
      (`domain.AdminPostFilter{Types: f.Types, Statuses: f.Statuses}`),
      silently dropping every other field. That is harmless today because
      no other field exists yet, but it would silently drop the new
      `Search`/`Author` fields the moment this task adds them — the exact
      class of filtered-totals bug IM1 fixed for media (Task 6.1). This
      task SHALL therefore forward the *same* filter value to both calls,
      differing only in `Limit`/`Offset`, not rebuild a filter literal that
      re-lists a subset of fields: build one local `filter` with
      `Limit`/`Offset` populated for `ListForAdmin`, then pass a second
      value derived from it with `Limit`/`Offset` zeroed to
      `CountForAdmin` (e.g. `countFilter := filter; countFilter.Limit,
      countFilter.Offset = 0, 0`). Also extend
      `internal/storage/wprepo/adminreads.go`'s `ListForAdmin` and
      `CountForAdmin` to apply `f.Author` (unaliased `post_author = ?` when
      `f.Author != 0` — both methods query `{prefix}posts` directly with no
      table alias, per `adminreads.go:36,89`, unlike `media.go`'s aliased
      `p.` queries, so the predicate must not carry a `p.` prefix here)
      identically in both — today neither method references `post_author`
      at all, so without this step the new `Author` field would silently
      have no effect, the same class of bug IM1 fixed for media in
      Task 6.1; apply it via the same `applyAdminSearch`-style shared-call
      shape (both methods already call `applyAdminSearch(q, f.Search)`
      identically at `adminreads.go:42` and `:94` — add the `Author`
      predicate as a second, equally-shared `q.Where(...)` call alongside
      it, not a diverging one-sided addition).
- [ ] 4.2 Update `internal/web/adminapi.go`'s `adminPosts` handler
      (currently `s.admin.List(r.Context(), page, perPage, q.Get("type"),
      q.Get("status"))` at line 211) to build a `domain.AdminPostFilter`
      literal from the query string — `Types: []string{q.Get("type")}` when
      non-empty, `Statuses: []string{q.Get("status")}` likewise, `Search:
      q.Get("search")` (any string, no validation needed — it is a
      substring match), `Author: <parsed author>` (optional; when present,
      must parse as a non-negative base-10 integer) — and call
      `s.admin.List(r.Context(), page, perPage, filter)` with the new
      signature from 4.1. `status`, when non-empty, must validate against
      the fixed allowed set `{"publish", "draft", "pending", "private",
      "future"}` (empty means unfiltered, matching existing behavior).
- [ ] 4.3 On validation failure for `status` or `author`, respond `400`
      using the existing `writeJSONError(w, http.StatusBadRequest, code,
      message)` helper (`internal/web/adminapi.go:264`), which already
      produces the standard envelope
      `{"error":{"code":"...","message":"..."}}` — e.g.
      `writeJSONError(w, http.StatusBadRequest, "invalid_status", "invalid status")`.
- [ ] 4.4 Write Go handler tests in `internal/web/adminapi_test.go`
      (extend the existing file): valid `status`/`search`/`author`
      combinations return `200` with correctly filtered results; each
      invalid value (`status=bogus`, `author=notanumber`, `author=-1`)
      returns `400` with the expected `{"error":{"code":...,"message":...}}`
      body; a filter combination matching zero posts returns `200` with
      `TotalPages: 0` (Requirement 8.1); a request with `status`/`author`
      omitted or passed as an empty string returns `200` unfiltered, not
      `400` (Requirement 4.5); a fixture with posts from two different
      authors asserts that filtering by one author's ID returns only that
      author's posts *and* that `Total`/`TotalPages` match that filtered
      count, not the site's total post count (the same class of
      filtered-totals regression IM1 covers for media); a second fixture
      with posts whose titles differ (e.g. two posts containing "widget",
      one not) asserts that `search=widget` returns `200` with only the
      matching posts *and* that `Total`/`TotalPages` reflect that
      two-post filtered count, not the fixture's full post count — proving
      Task 4.1's filter-forwarding fix (the same `filter` value reaching
      both `ListForAdmin` and `CountForAdmin`) actually applies `Search` to
      the count path and not only the list path. Add a matching
      `"ListForAdmin filters by Author"` / `"CountForAdmin honors Author"`
      subtest pair to the existing `runAdminContract` function in
      `internal/storage/storagetest/admin_contract.go`, following the exact
      pattern already used there for `Search` (lines 147-183), so every
      configured storage vendor is proven to apply `f.Author` identically
      in `ListForAdmin` and `CountForAdmin`.
- [ ] 4.5 Run `go test ./internal/content/... ./internal/web/...` and
      confirm all pass.

### Task 4A: Add a narrow admin author-listing endpoint (Requirement 3.7)

**Requirements:** 3.7

- [ ] 4A.1 In `internal/domain/repository.go`, add
      `type AuthorOption struct { ID int64; Name string }` and extend the
      `AdminPostRepository` interface (currently at lines 59-68, methods
      `ListForAdmin`/`CountForAdmin`) with
      `Authors(ctx context.Context) ([]AuthorOption, error)`, documented as
      "returns only the distinct users who have authored at least one post
      or page — not the full user list — ordered by display name." This
      widens `AdminPostRepository`, so
      `internal/content/adminread_test.go`'s existing `fakeAdminData`
      (`internal/content/adminread_test.go:13-38`, used as the `posts
      domain.AdminPostRepository` argument to `NewAdminService` in every
      test in that file) stops compiling the moment this interface gains a
      new method; add a minimal `Authors` method to `fakeAdminData` in the
      same edit — following its existing closure-per-method style (e.g.
      `authors func() ([]domain.AuthorOption, error)` alongside `list`,
      `count`, `byStatus`, ...), with `func (f *fakeAdminData) Authors(_
      context.Context) ([]domain.AuthorOption, error) { return
      f.authors() }` — rather than leaving it for a later compiler error to
      surface.
- [ ] 4A.2 Implement `Authors` on the type implementing
      `AdminPostRepository` in `internal/storage/wprepo/adminreads.go`
      (alongside `ListForAdmin`/`CountForAdmin`), as a Bun query built the
      same way as this file's neighbors in `media.go` — never a raw SQL
      string with an unquoted `u.ID`, since the mixed-case `ID` primary key
      column must go through `bun.Ident("ID")` on every reference or
      Postgres folds the unquoted identifier to lowercase and misses the
      actual `"ID"` column (`internal/storage/wprepo/helpers.go:118-124`
      documents this; `media.go:58,62,91,104,107,146,155` and `users.go`
      never write `u.ID`/`p.ID` literally, always
      `bun.Ident("ID")`). Mirroring `media.go`'s existing
      `TableExpr`/`ColumnExpr`/`Join` shape:
      `r.db.NewSelect().TableExpr("? AS p", bun.Ident(r.prefix+"posts")).
      ColumnExpr("DISTINCT u.?, u.display_name", bun.Ident("ID")).
      Join("JOIN ? AS u ON u.? = p.post_author", bun.Ident(r.prefix+"users"),
      bun.Ident("ID")).Where("p.post_type IN (?)", bun.In([]string{"post",
      "page"})).OrderExpr("u.display_name ASC")` — a privacy-scoped read
      that never returns a user with zero posts/pages, unlike a full
      `{prefix}users` listing.
- [ ] 4A.3 Add `AdminService.Authors(ctx context.Context)
      ([]domain.AuthorOption, error)` in `internal/content/adminread.go` as
      a thin passthrough to `s.posts.Authors(ctx)`.
- [ ] 4A.4 Add handler `adminAuthors` in `internal/web/adminapi.go`
      returning `writeJSON(w, http.StatusOK, authorListResponse{Items:
      ...})` (a new small DTO type), and register
      `gr.Method(http.MethodGet, "/authors", s.jsonHandler(s.adminAuthors))`
      inside the existing capability-gated route group in
      `internal/web/adminroutes.go` (the same `gr` group already gated by
      `gr.Use(s.requireCapabilityJSON("edit_posts"))` at line 84, which
      already wraps `/posts` at line 86) — no new capability-gating
      mechanism is introduced.
- [ ] 4A.5 Add cross-vendor contract test coverage in
      `internal/storage/storagetest/admin_contract.go` for `Authors`: a
      site with N distinct post/page authors returns exactly those N
      entries; a user with zero posts/pages is excluded; ordering is by
      display name.
- [ ] 4A.6 Write a Go handler test for `adminAuthors` in
      `internal/web/adminapi_test.go`: a caller without `edit_posts`
      receives `403` (matching the existing `adminPosts` capability-gating
      test pattern in the same file); a caller with `edit_posts` receives
      `200` with the expected author list.
- [ ] 4A.7 In `web/admin/src/api/client.ts`, add an `authors()` function
      calling `GET /admin/api/authors` and returning the typed item list,
      following the same request/response pattern `posts()` already uses.
- [ ] 4A.8 Run `go test ./internal/storage/... ./internal/content/...
      ./internal/web/...` and confirm all pass.

### Task 5: Wire `client.ts`/`PostsList.tsx` to the new admin filters

**Requirements:** 3.1–3.5, 3.7, 8.2

- [ ] 5.1 In `web/admin/src/api/client.ts`, extend the `posts()` function's
      parameter type and query-string construction to include optional
      `search` and `author` fields, alongside its existing `page`,
      `perPage`, `type`, `status`.
- [ ] 5.2 In `web/admin/src/views/PostsList.tsx`, add three new controls
      above the existing `TableView`: a Spectrum `Picker` for `status`
      (options: All, Published, Draft, Pending, Private, Scheduled,
      mapping to the same allowed set as Task 4.2), a Spectrum
      `SearchField` for `search`, and a Spectrum `Picker` for `author`
      whose options are loaded from the new `api.authors()` client call
      added in Task 4A.7 (each option's value is the author's numeric ID,
      its label is the author's display name — never a bare numeric-ID
      `NumberField`, per Requirement 3.7).
- [ ] 5.3 Sync all three new controls to the URL query string using the
      same `useSearchParams`/`setParams` pattern `PostsList.tsx` already
      uses for `page`; changing any filter resets `page` to `1` in the
      same `setParams` call (mirroring how the existing pagination
      controls already update the URL).
- [ ] 5.4 Update the data-fetching effect to pass the three new params to
      `api.posts(...)`.
- [ ] 5.5 Write/extend React tests for `PostsList.tsx` (confirm existing
      test file via `glob "web/admin/src/views/PostsList.test.*"`, create
      if absent following the existing test-setup conventions used by
      sibling view tests): changing each filter triggers a re-fetch with
      the expected query params and resets to page 1; loading a URL with
      filters pre-populates the controls; keyboard operability of each new
      control.
- [ ] 5.6 Run the admin frontend test suite (`vitest run`, per
      `web/admin/package.json`'s `test` script) and confirm all pass.

### Task 6: Extend media filtering on the backend

**Requirements:** 5.1–5.4, 4.3–4.4

- [ ] 6.1 `internal/content/media.go` and `MediaService` already exist —
      `MediaService.List(ctx, filter domain.MediaFilter) ([]domain.Media, int, error)`
      (`internal/content/media.go`) already accepts a filter struct and
      already calls both `s.repo.List(ctx, filter)` and
      `s.repo.Count(ctx, filter)` with the same filter, so no new service or
      new file is needed. In `internal/storage/wprepo/media.go`, extract a
      new unexported helper
      `applyMediaFilter(q *bun.SelectQuery, f domain.MediaFilter) *bun.SelectQuery`
      that applies, in order, `f.ParentID` (`p.post_parent = ?`, unchanged
      from today), `f.Search` (non-empty: case-insensitive substring match
      against **both** `p.post_title` and `pm.meta_value` — the
      `_wp_attached_file` filename already `JOIN`ed by both `listQuery` and
      `Count` as `pm` — using
      `LOWER(p.post_title) LIKE ? OR LOWER(pm.meta_value) LIKE ?` so
      Requirement 5.1's approved title-*or*-filename behavior holds), `f.Type`
      (non-empty: `p.post_mime_type LIKE ?` against the major type, e.g.
      `image/%`), and `f.After`/`f.Before` (non-zero: `p.post_date >= ?`/
      `p.post_date <= ?`). This mirrors `internal/storage/wprepo/adminreads.go`'s
      existing `applyAdminSearch(q *bun.SelectQuery, search string) *bun.SelectQuery`
      helper (lines 61-67), which is already called identically from both
      `ListForAdmin` (line 42) and `CountForAdmin` (line 94) — the same
      one-helper-two-call-sites shape this task reuses for media.
      Replace `listQuery`'s existing inline
      `if f.ParentID != 0 { q = q.Where(...) }` block (lines 63-65) with a
      single `q = applyMediaFilter(q, f)` call, and replace `Count`'s
      equivalent inline block (lines 93-95) with the same
      `q = applyMediaFilter(q, f)` call, so `List` and `Count` apply
      **exactly** the same predicate set — today `Count` only applies
      `ParentID` and silently ignores every other filter, which this task
      corrects. `f.Limit`/`f.Offset` stay applied only in `listQuery` (as
      today), never in `Count`, since `Count` must ignore paging by
      definition.
- [ ] 6.2 In `internal/web/adminapi_media.go`'s `adminMediaList`, read
      `search`, `type`, `after`, `before` from the query string (`after`/
      `before` as RFC 3339 dates) and set them on the existing
      `domain.MediaFilter{...}` literal at line 70; validate `type` against
      `{"", "image", "video", "audio", "document"}` and `parentId` (already
      read today) as a non-negative integer if present; malformed
      `after`/`before` dates are also a `400` (Requirement 4.3). Also fix
      the `totalPages` computation at lines 82-85: remove the
      `if totalPages < 1 { totalPages = 1 }` clamp so a zero-result query
      returns `TotalPages: 0`, matching the unified contract in
      Requirement 8.1 and `AdminService.List`'s existing (already-correct)
      behavior. Because `MediaService.List` (unchanged) calls `s.repo.Count`
      with this same filter and 6.1 made `Count` apply the identical
      predicate set, `Total`/`TotalPages` returned here are already
      guaranteed to reflect the filtered result set — no additional handler
      logic is needed to keep them in sync.
- [ ] 6.3 On validation failure, respond `400` using `writeJSONError`
      (same helper and envelope as Task 4.3).
- [ ] 6.4 Write Go handler tests in `internal/web/adminapi_media_test.go`
      (extend or create, matching Task 4.4's structure): valid filter
      combinations return `200` with correctly filtered/paginated results;
      each invalid value returns `400`; a filter combination matching zero
      media items returns `200` with `TotalPages: 0` (regression test for
      the clamp removed in 6.2); a fixture with more matching items than one
      page size asserts `Total` equals the exact count of items matching the
      filter (not the unfiltered collection) and `len(Items)` on the last
      page plus `(TotalPages-1)*PerPage` sums to that same `Total`; a
      `search` value that matches only by filename (not by title) still
      returns that item, and one that matches only by title (not filename)
      also still returns its item, proving both sides of Requirement 5.1's
      title-or-filename match. Add the equivalent assertions as a new
      subtest in the existing `runMediaContract` function in
      `internal/storage/storagetest/media_contract.go` so every configured
      storage vendor is proven to apply `Search`/`Type`/`After`/`Before` to
      `Count` identically to `List` (Requirement 9's cross-vendor coverage).
- [ ] 6.5 Run `go test ./internal/content/... ./internal/web/...
      ./internal/storage/...` and confirm all pass.

### Task 7: Rebuild `Media.tsx` with pagination, filters, and a grid/list toggle

**Requirements:** 5.1–5.4, 6.1–6.4, 7.1–7.3, 8.2

- [ ] 7.1 In `web/admin/src/api/client.ts`, extend `media()` to forward
      `parentId` (currently accepted by the backend but dropped by this
      function — fix this first, it is a one-line bug fix independent of
      the rest of this task) plus the new `search`, `type`, `after`,
      `before` params from Task 6.
- [ ] 7.2 In `web/admin/src/views/Media.tsx`, add URL-backed `page` state
      using the same `useSearchParams` pattern as `PostsList.tsx`
      (Requirement 8.2's shared-pattern requirement), replacing the
      current call `api.media({})` with one that passes `page`, `perPage`,
      and every filter's current value.
- [ ] 7.3 Add filter controls mirroring Task 5.2's approach: a
      `SearchField` for `search`; a `Picker` for `type` (options: All,
      Image, Video, Audio, Document); `@adobe/react-spectrum`'s
      `DateRangePicker` for `after`/`before` (already available at the
      project's installed `^3.47.0` version). `Media.tsx` today has no
      parent-post selector at all — `parentId` is only used elsewhere, by
      `MediaPicker.tsx`/`client.ts`'s `attachMedia` for attaching media
      *to* a post, not for filtering the library *by* parent — so add a
      new `Picker` populated by calling the existing `api.posts({perPage:
      100})` (the same client function `PostsList.tsx` already uses) and
      mapping each `PostListItem`'s `id`/`title` to an option, plus a
      leading "All" option that clears the `parentId` filter.
- [ ] 7.4 Add a Spectrum `ActionButtonGroup` (or `ToggleButton` pair) for
      grid/list mode, synced to the URL query string; render **either**
      the existing `Grid` component **or** the existing `TableView`
      component for the current page's items based on this state — never
      both (fixing the current always-render-both behavior).
- [ ] 7.5 Add previous/next pagination controls and a "Page X of Y · N
      items" text, matching `PostsList.tsx`'s existing presentation
      exactly (same component choices, same copy pattern) so the two
      views are visually and behaviorally consistent (Requirement 8.2).
- [ ] 7.6 Write/extend React tests for `Media.tsx`: filter changes trigger
      a re-fetch with expected query params and reset to page 1; the
      grid/list toggle renders exactly one view at a time; toggling
      persists across a reload via the URL; pagination controls behave
      like `PostsList.tsx`'s equivalent tests from Task 5.5.
- [ ] 7.7 Run the admin frontend test suite and confirm all pass.

### Task 8: Extract shared pagination UI once both views work (optional refactor)

**Requirements:** 8.2

- [ ] 8.1 With Tasks 5 and 7 both complete, diff `PostsList.tsx`'s and
      `Media.tsx`'s pagination-control JSX; if they are substantially
      identical (expected, since Task 7.5 copies the pattern
      deliberately), extract a small shared `PaginationBar` component
      (new file `web/admin/src/components/PaginationBar.tsx`) taking
      `page`, `totalPages`, `total`, and an `onPageChange` callback, and
      use it from both views.
- [ ] 8.2 Update both views' existing tests to exercise the shared
      component instead of duplicating pagination-specific assertions;
      run the admin frontend test suite and confirm all pass.

### Task 9: M8 acceptance pass

**Requirements:** 9.1–9.4

- [ ] 9.1 Run the full Go test suite: `go test ./...` (confirm no vendor
      is skipped by checking whatever environment variables the existing
      `storagetest` harness expects for MySQL/Postgres, per its existing
      `NewReposFunc` setup).
- [ ] 9.2 Run the full admin frontend test suite (`vitest run`) and the
      existing typecheck command (`tsc --noEmit`, i.e. `npm run typecheck`
      in `web/admin`); `web/admin/package.json` has no separate `lint`
      script today, so typecheck is the only additional gate.
- [ ] 9.3 Manually load the public home page, a category archive, the
      admin content list, and the admin media library against a local
      build seeded with more than one page of content, at a common
      desktop width and a common mobile width; compare side-by-side
      against `main` to confirm behavioral parity with the WordPress
      workflow being matched (not pixel identity), per Requirement 9.4.
- [ ] 9.4 Confirm no new migration file was added anywhere in the repo
      (`git status` shows no new file under any `migrations`-style
      directory) — M8 must ship with zero schema changes per its own
      design.

---

## M9 — Routing & Taxonomy Parity (roadmap-level task groups)

Each group below must be expanded into its own
`requirements.md`/`design.md`/`tasks.md` (or added as a new milestone
folder under `plans/`) before any of it is implemented — the specifics of
permalink-token precedence, redirect-status-code choices per token, and
the nested-category descendant-inclusion decision (flagged as open in
`design.md`) all need their own review before code is written.

- [ ] **9.A — Options-driven permalink structure.** Read
      `permalink_structure`/`category_base`/`tag_base` from
      `{prefix}options` (via the options-reading path M1–M7 already have);
      decide and document, in a dedicated design, where the resolver lives
      (`internal/content` vs. a new `internal/routing` package) and how it
      composes with the existing `internal/web` router.
- [ ] **9.B — Core permalink tokens and canonical redirects.** Enumerate
      the exact WordPress token set to support (`%postname%`, `%year%`,
      `%monthnum%`, `%day%`, at minimum, per Requirement 11.1); specify
      exact redirect status codes and canonicalization rules for each
      supported/unsupported combination before implementation.
- [ ] **9.C — Tag, date, and author archives.** Specify the exact route
      shapes and pagination behavior (reusing M8's `Page` result type from
      Task 2.2), and whether/how they compose with M9.A's permalink
      structure.
- [ ] **9.D — Nested category hierarchy.** Add `ParentID` to `domain.Term`
      sourced from `term_taxonomy.parent`; explicitly decide (and record in
      that milestone's own design) whether a parent category's archive
      includes descendant categories' posts before writing the route
      handler.
- [ ] **9.E — M9 test coverage.** Cross-vendor contract tests for the new
      `Term.ParentID` read path; at least one fixture-based test against an
      imported real-WordPress-database export (mirroring
      `plans/02.1-wp-hash-real-db`'s validation approach) for permalink and
      nested-category behavior.

---

## M10 — REST Write & Content Safety Parity (roadmap-level task groups)

Requirement 15 (the sanitization policy) must be fully designed,
implemented, and tested — with its own dedicated spec — **before** any
task in group 10.B or 10.C begins. This ordering is not optional: it is
the load-bearing safety property of this milestone (see `design.md`'s
Security Considerations section).

- [ ] **10.A — Capability-aware write-boundary content policy.** Specify,
      in a dedicated design, the exact allow-listed HTML elements/
      attributes per capability tier, which existing write paths it
      applies to (REST media/user writes once enabled, plus the
      already-existing admin post/page CRUD and public comment submission
      — both currently unsanitized, per `design.md`'s "Existing write
      paths are in scope too" note), and how it composes with the
      existing `TRUST BOUNDARY` comments in `internal/web/view.go`. Choose
      and vet a concrete library (e.g. `bluemonday`, already named as the
      recommended candidate in both `docs/compatibility.md` and
      `view.go`'s existing comments) as part of that design, not before.
- [ ] **10.B — REST media write parity.** Replace
      `restNotImplemented` registrations in `internal/web/rest_media.go`
      for `POST /media` (create) and `PUT`/`PATCH`/`POST /media/{id}`
      (update — WordPress's REST API accepts `POST` as an update verb on
      the single-item route in addition to `PUT`/`PATCH`, and
      `rest_media.go:25` already stubs `POST /media/{id}` for exactly this
      reason) with real handlers over the existing
      `domain.MediaWriter.Create`/`.SetParent`, gated on the existing
      `upload_files` capability (defined at
      `internal/auth/roles.go:54,66,95`), and passed through 10.A's policy
      before any write. `DELETE /media/{id}` is explicitly **out of
      scope** for this milestone (Requirement 16 has no delete acceptance
      criteria) — its `restNotImplemented` registration SHALL remain
      unchanged, continuing to respond `501`, not be silently reinterpreted
      as a create/update path.
- [ ] **10.C — REST user write parity.** Replace `restNotImplemented`
      registrations in `internal/web/rest_users.go` for `POST /users`
      (create) and `PUT`/`PATCH`/`POST /users/{id}` (update — same
      WordPress POST-as-update convention as 10.B, already stubbed at
      `rest_users.go:29`) with real handlers over
      `domain.UserRepository.Create` (existing) plus a new profile-field
      update method (`domain.UserRepository` currently has `Create` and
      `UpdatePass` but no general update for fields like `description`;
      add one), gated on the existing `create_users`/`edit_users`
      capabilities, reusing M2's existing password-hash handling for any
      password field and never introducing a second hash scheme.
      `DELETE /users/{id}` is explicitly **out of scope** for this
      milestone (Requirement 17 has no delete acceptance criteria) — its
      `restNotImplemented` registration SHALL remain unchanged, continuing
      to respond `501`.
- [ ] **10.D — `content.rendered` improvements.** Strip `<!-- wp:* -->`/
      `<!-- /wp:* -->` delimiters and add `srcset`/`sizes`/`loading`/
      `decoding` attributes to rendered `<img>` tags in the REST
      `content.rendered` field only (not the public HTML templates); scope
      explicitly excludes rendering arbitrary block visual output.
- [ ] **10.E — M10 test coverage.** A capability-matrix test (allowed vs.
      forbidden caller) for each of 10.B/10.C's newly enabled write routes;
      a content-safety test proving 10.A's policy is applied on every
      newly enabled write route and on the pre-existing admin/comment
      write paths it now also covers.
