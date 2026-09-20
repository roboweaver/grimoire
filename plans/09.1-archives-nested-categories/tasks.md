# Tasks — M9b: Archives & Nested Categories

Implementation checklist for M9b (roadmap groups 9.C, 9.D, 9.E). Tasks are
ordered so each builds on the last, and each references the requirement it
satisfies. Keep `gofmt -l .` empty and `go vet ./...`, `go build ./...`,
`go test ./...` green after every phase.

Strict TDD: within each phase the failing test is written **before** the
implementation, matching how M5–M9a were run.

> **Inherited, not re-decided.** A parent category's archive includes its
> descendants, and nested category routes are canonical with the flat
> `/category/{slug}` `301`ing to them. Both were settled in M9a's
> `requirements.md` "Out of scope" table; see this spec's "Inherited decisions".
>
> **All three questions answered; nothing is blocked.** The project lead has
> resolved the permalink front, WordPress's `date/` disambiguation prefix and
> duplicate `user_nicename` handling. They are now acceptance criteria — the
> front's asymmetry in Requirement 4.7–4.10, the `date/` prefix in 6.7, and
> deterministic lowest-`ID` resolution plus `migrate -check` reporting in
> 7.6–7.8 — and `requirements.md` records where each landed under "Resolved
> decisions". One scope addition came with the third answer:
> `grimoire-cli migrate -check` reports duplicate nicenames (tasks 4.3–4.4 and
> 9.4–9.5).

## Phase 0 — Spec

- [x] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and add the row to the
      `plans/README.md` milestone index (status → Specified).
  - _Acceptance:_ All three files exist and the index links them.
- [x] 0.2 Resolve the three open questions in `requirements.md` and fold the
      answers into `design.md`'s precedence table and constructor descriptions,
      removing the "Open questions" section once each is answered.
      _(Req 4.7–4.10, 6.7, 7.6–7.8)_
- [~] 0.3 Open the spec PR and obtain review approval before writing code.
  - _Acceptance:_ PR open, CI green, approved.

## Phase 1 — `internal/routing` archive grammar (pure, no DB, no HTTP)

- [~] 1.1 Write failing table-driven tests for `routing.DateRef.Range`: year-only,
      year+month and year+month+day granularities each produce the correct
      half-open `[start, end)` interval; `2024/02/30`, `2024/13/01` and
      `2023/02/29` return `ok == false`; `2024/02/29` (a real leap day) returns
      `ok == true`. _(Req 6.1, 6.3, 6.4)_
- [~] 1.2 Implement `DateRef` and `Range` in `internal/routing/routing.go`,
      building the interval in a fixed zone carrying naive wall-clock time — the
      same no-conversion basis `Canonical` already reads `post.Date` on.
      _(Req 6.4)_
- [~] 1.3 Write failing tests for `Parse` recording base **provision**, not just
      resolution: `Parse(s, "", "")` yields `CategoryBaseSet`/`TagBaseSet`
      `false`; `Parse(s, "sections", "topics")` yields both `true`; and
      `Parse(s, "category", "tag")` — the bases set explicitly to the **default
      strings** — also yields both `true`. That last case is the whole point:
      WordPress tests `get_option()`'s truthiness, not inequality with the
      default, and an implementation comparing against `DefaultCategoryBase`
      passes the first two cases and fails this one. Also assert the front is
      derived as the maximal run of leading literal segments, and is empty for a
      `Flat` structure. _(Req 4.7, 4.8, 4.9)_
- [~] 1.4 Implement `CategoryBaseSet`, `TagBaseSet` and the derived front in
      `routing.Parse`. **Keep `Parse`'s signature** —
      `(structure, categoryBase, tagBase string)`: `content.OptionService`
      already maps an absent option to the empty string, so the empty string
      arriving here *is* WordPress's falsy `get_option()`, and an options struct
      or `*string` parameters would churn every one of the twenty-odd call sites
      in `cmd/`, `internal/web`, `internal/content`, `internal/routing` and
      `test/e2e` for no behavioral gain. Do **not** implement WordPress's
      `using_index_permalinks()` disjunct on `with_front`: grimoire serves no
      index permalinks, so it is unreachable. _(Req 4.9, 4.10)_
- [~] 1.5 Write failing tests for the four archive path constructors
      `CategoryPath`, `TagPath`, `AuthorPath`, `DatePath`: a top-level category's
      path equals the flat path; a nested category's path carries its full
      ancestry root-first; a non-default `category_base`/`tag_base` replaces the
      default segment; the author base is the literal `author`; the trailing
      slash follows `Structure.TrailingSlash`; a `Flat` structure yields no
      trailing slash. Then the front, per `design.md`'s "The front" table: with
      structure `/blog/%year%/%monthnum%/%postname%/`, `DatePath` and
      `AuthorPath` carry `blog` **always**; `CategoryPath`/`TagPath` carry it
      only when the corresponding base option was unset, and drop it when the
      base was provided — including when provided as the default string.
      _(Req 2.1, 2.7, 4.1, 4.2, 4.3, 4.7, 4.8, 4.9, 5.1, 6.1, 7.1)_
- [~] 1.6 Write failing tests for `DatePath`'s `date/` disambiguation prefix over
      token positions: `/%post_id%/`, `/%year%/%post_id%/` and
      `/%year%/%monthnum%/%post_id%/` (indices 1, 2, 3) each yield
      `date/`-prefixed date paths; `/%year%/%monthnum%/%day%/%post_id%/` (index 4)
      does **not**; `/%postname%/` and `/%year%/%monthnum%/%day%/%postname%/` do
      not. Assert the prefix lands **after** the front, so
      `/blog/%post_id%/` yields `/blog/date/2024`. WordPress keys this on the
      token index rather than on whether a front exists, so the index boundary is
      what has to be tested. _(Req 6.7)_
- [~] 1.7 Implement the four constructors next to `Canonical`, so archive URLs
      have the same single-construction-site property posts already have, with
      the front and the `date/` prefix computed by one unexported helper that
      `Classify` also calls — that shared helper is what makes the round-trip
      property in 1.8 hold for front-carrying structures and not only for
      front-less ones. _(Req 2.2, 4.7, 6.7)_
- [~] 1.8 Write a failing test for the **canonical fixed-point property** of each
      archive kind: feed each constructor's output back through `Classify` and
      re-construct, asserting the identical string. Include a three-level nested
      category, since its target is derived through a graph walk rather than a
      field read and a walk that disagreed on the second pass would loop on every
      category URL. Run the whole table a second time against a front-carrying
      structure with `category_base` set and again with it unset, so a front
      applied on construction but not on classification (or vice versa) shows up
      as a failing round trip rather than as a redirect loop in Phase 7.
      _(Req 2.4, 4.7, 4.8)_
- [~] 1.9 Write failing table-driven tests for `Structure.Classify` covering
      **every row** of `design.md`'s precedence table, plus each ambiguous shape
      named in Requirement 9: a bare 4-digit segment under `/%postname%/`
      classifying as a date and not a post; a 3-numeric-segment path under
      `/%year%/%monthnum%/%postname%/` classifying as a date; a path whose first
      segment is the category/tag/author base classifying as that archive ahead
      of any post reading; the bare base segment classifying as `KindNone`; a
      multi-segment tag path classifying as `KindNone`; and `/` classifying as
      `KindNone`. Add the per-row **expected prefix** cases: under
      `/blog/%year%/%monthnum%/%postname%/` with `category_base` unset,
      `/blog/category/news` classifies as `KindCategory` and `/category/news`
      does not; with `category_base` set to `sections`, `/sections/news`
      classifies and `/blog/sections/news` does not; `/blog/author/alice` and
      `/blog/2024` classify in both configurations. Under `/%post_id%/`,
      `/date/2024` classifies as `KindDate` and `/2024` as `KindPost`.
      _(Req 9.2, 9.3, 9.4, 2.8, 5.2, 4.7, 4.8, 6.7)_
- [~] 1.10 Implement `Kind`, `Target`, `AuthorBase` and `Classify`. `Classify` is
      total: an unrecognised path yields `KindNone` rather than an error. Each
      archive row tests its own expected prefix through the shared helper from
      1.7 — **not** a single strip-the-front-first step, which would be wrong
      whenever a base option is set and the front therefore does not apply to
      that kind. _(Req 9.2, 4.8)_
- [~] 1.11 Write failing tests for `Structure.ArchivePatterns`: the expected chi
      patterns for the default bases and for overridden ones, both slash forms,
      the wildcard form for the category base, and the front-prefixed forms —
      including the asymmetric case where the date and author patterns carry the
      front and the category/tag patterns do not because their bases were set,
      and the `date/` form under a `%post_id%`-leading structure.
      _(Req 4.1, 4.2, 4.7, 4.8, 6.7, 9.5)_
- [~] 1.12 Implement `ArchivePatterns`. _(Req 9.5)_
- [~] 1.13 Write a failing test for base collisions: `category_base` equal to
      `tag_base`, and either equal to `author`, each surface a note from
      `Structure.Notes()` naming the collision, while `Parse` returns a
      **nil error** and a fully usable non-flat `Structure` whose colliding
      segment keeps the category meaning. Assert the nil error explicitly — M9a's
      callers treat any error from `Parse` as "fall back to flat", so reporting a
      renamed base that way would turn a cosmetic clash into a permalink outage.
      _(Req 4.4, 4.5)_
- [~] 1.14 Implement `Structure.Notes()` and the collision check in `Parse`.
      Degrade loudly but keep serving, the same posture M9a Requirement 4 takes.
      _(Req 4.4, 4.5)_

## Phase 2 — `Term.ParentID` read path (cross-vendor)

- [~] 2.1 Extend `storagetest.SeedFixtures` with a nested category chain —
      parent, child, grandchild, each with a post — since every existing
      `term_taxonomy` row is seeded with `parent` `0` and a `ParentID` assertion
      against them would pass for a repository that hard-coded `0`.
      _(Req 12.1)_
- [~] 2.2 Write failing `storagetest` contract cases asserting `ParentID` is
      populated by **all three** term reads — `TermRepo.BySlug`,
      `TermRepo.ListByTaxonomy`, `TermRepo.TermsByIDs` — and that a top-level
      term reports `0`. Runs on SQLite, MySQL and Postgres.
      _(Req 1.2, 12.1)_
- [~] 2.3 Add `ParentID int64` to `domain.Term` in
      `internal/domain/entities.go`, documented as backing
      `term_taxonomy.parent` with `0` meaning no parent, and read-only in this
      milestone. _(Req 1.1)_
- [~] 2.4 Add `parent` to `termRow` and to all three term selects in
      `internal/storage/wprepo/repo.go`. Each already joins `term_taxonomy`, so
      this is one extra column and no new join. _(Req 1.2)_
- [~] 2.5 Confirm `term_taxonomy.parent` already exists in every vendor's
      `internal/storage/migrations/<vendor>/0001_init.up.sql` and that no
      migration file is added. _(Req 1.3, 12.8)_

## Phase 3 — Descendant-aware published-post query (cross-vendor)

- [~] 3.1 Write failing `storagetest` contract cases for
      `PostRepository.PublishedArchive` and `CountPublishedArchive`:
      term-set selection returns the parent's **and** descendants' posts; a post
      assigned to both a parent and a child appears **once** and counts **once**;
      an unpublished post in a matching category is excluded; author selection;
      a half-open date range including a post exactly on the lower bound and
      excluding one exactly on the upper; ordering newest-first; `Types`
      defaulting to `{"post"}` so pages do not leak into archives; and a
      non-empty `Taxonomy` with empty `TermIDs` returning empty/zero rather than
      everything. Runs on all three vendors. _(Req 3.1–3.5, 6.4, 12.2)_
- [~] 3.2 Add `ArchiveFilter` to `internal/domain/repository.go` following
      `AdminPostFilter`/`MediaFilter`'s zero-value-means-unfiltered convention,
      and add `PublishedArchive`/`CountPublishedArchive` to `PostRepository`.
      `post_status='publish'` is deliberately **not** a field, so no caller can
      disable it. Name the date bounds **`Start`/`End`**, not `After`/`Before`,
      and say why in a field comment: `domain.MediaFilter` in the same package
      already has `After`/`Before` and its `Before` is an inclusive *day-end*
      (`media.go:79-83` compares against `formatTS(f.Before.AddDate(0, 0, 1))`),
      so two identically-named date fields meaning opposite things would be a
      trap for whoever writes the third filter. `MediaFilter` itself is left
      alone. _(Req 3.4, 6.4)_
- [~] 3.3 Implement both in `internal/storage/wprepo/repo.go` using an `EXISTS`
      semi-join for the term predicate — not a join with `DISTINCT` — so one row
      per post is guaranteed by construction and the count cannot disagree with
      the listing. Date bounds are the half-open `[Start, End)` range formatted
      through the existing `formatTS` helper — the same formatting route
      `MediaFilter` takes, but not its semantics, which is why the fields are
      named `Start`/`End` — so one statement serves all three vendors. Build the
      `ID` references with `bun.Ident("ID")`, never bare `p.ID`: Postgres
      declares the column as `"ID"`, so unquoted it folds to `p.id` and fails on
      that vendor alone (issues #40/#43 were both this defect).
      _(Req 3.1, 3.3, 6.4)_
- [~] 3.4 Leave `ByTermSlug`/`CountPublishedByTermSlug` untouched. They are the
      single-slug, non-descendant reads and remain correct precisely because a
      single `t.slug` matches at most one `term_taxonomy` row per post; this
      milestone adds a superset rather than rewriting a tested method.

## Phase 4 — Author lookup (cross-vendor)

- [~] 4.1 Write a failing `storagetest` contract case for
      `UserRepository.ByNicename`: resolves a seeded user, returns
      `domain.ErrNotFound` for an unknown nicename, and — seeding **two** users
      that share a nicename — resolves to the **lowest `ID`** on SQLite, MySQL and
      Postgres. Seed the higher `ID` first so a repository that relied on
      insertion order rather than `ORDER BY` fails the case. Mirrors the existing
      `ByLogin` contract case. _(Req 7.2, 7.6, 12.3, 12.10)_
- [~] 4.2 Add `ByNicename` to `domain.UserRepository` and implement it in
      `internal/storage/wprepo/users.go` as
      `WHERE user_nicename = ? ORDER BY ID ASC LIMIT 1`. The `ORDER BY` is a
      deliberate divergence from WordPress, which issues the same `LIMIT 1` with
      none: `user_nicename` carries a non-unique index (verified on the live
      database: `Non_unique = 1`), so duplicates are schema-legal even though
      `wp_insert_user()` prevents them on write by suffixing. Record the
      divergence in the method's doc comment, not only in `design.md`.
      _(Req 7.2, 7.6)_
- [~] 4.3 Write a failing `storagetest` contract case for
      `NicenameAuditor.DuplicateNicenames`: reports the pair seeded in 4.1 with
      the correct `Count` and a `WinnerID` equal to what `ByNicename` returns,
      reports **nothing** when no nicename is shared, and runs on all three
      vendors. Asserting `WinnerID` against `ByNicename`'s own result is what
      keeps the report from claiming a winner the read does not actually pick.
      _(Req 7.8, 12.10)_
- [~] 4.4 Add `domain.NicenameConflict` and
      a **new narrow interface** `domain.NicenameAuditor` carrying only
      `DuplicateNicenames(ctx) ([]NicenameConflict, error)` — **not** a method on
      `UserRepository` — and implement it as
      `SELECT user_nicename, COUNT(*), MIN(ID) ... GROUP BY user_nicename HAVING
      COUNT(*) > 1 ORDER BY user_nicename` — one aggregate, identical on all
      three vendors. Declare it alongside `UserRepository`, following the
      package's existing opt-in pattern (`TermReader`, `PostCounter`,
      `MediaWriter`), and state the reasoning in a comment: `UserRepository` is
      held by `auth.Sessions`, `auth.ApplicationPasswords`, `content.UserService`
      and the REST users path — every request path — so an operator-only method
      there would also force both existing fakes
      (`internal/auth/session_test.go:30`,
      `internal/content/userservice_test.go:22`) to grow a method neither ever
      calls. `*wprepo.UserRepo` satisfies **both** interfaces, so no new concrete
      type is needed — only the declaration and the method. `ByNicename` stays on
      `UserRepository` (task 4.2); it has a request-path caller. The auditor is
      consumed **only** by `grimoire-cli migrate -check` (tasks 9.4–9.5) and by
      no request path: a per-request duplicate check would add a `COUNT` to every
      author archive hit for a condition that is nearly always absent, and the
      handler must still serve one of the two authors either way. _(Req 7.8)_

## Phase 5 — Content services

- [~] 5.1 Write failing tests for ancestry and descendant resolution against a
      fake `domain.TermReader`: a three-level chain yields root-first ancestry;
      a parent pointing at a term not in the set terminates the walk and yields
      the shorter path rather than an error; a cycle terminates on the first
      repeated term; descendants are collected breadth-first across two levels.
      A fake is used because orphans and cycles are cheap to construct in Go and
      awkward to seed in SQL. _(Req 1.4, 1.5, 3.1)_
- [~] 5.2 Add `content.Archive` (in a new `internal/content/archive.go`) and
      implement the ancestry/descendant walks over a **single**
      `TermReader.ListByTaxonomy` read per request, with the visited-set guard.
      No cache: terms are mutable at runtime through M6's `TermWriteService`, so
      a cache would need an invalidation story this milestone does not own.
      _(Req 1.6)_
- [~] 5.3 Write failing tests for `TermService.CategoryArchive`: returns the
      term, its root-first ancestry, its descendant-inclusive posts and a
      `Page`; propagates `domain.ErrNotFound` for an unknown final segment
      **before** querying posts, matching `CategoryPage`'s existing
      not-found-skips-count behavior; an existing category with no posts yields
      an empty list with a zero `Total`. _(Req 2.5, 2.6, 3.1, 3.2, 8.1)_
- [~] 5.4 Add `TermService.WithHierarchy(domain.TermReader)` and implement
      `CategoryArchive`, following `PostService.WithCounter`'s opt-in-dependency
      pattern rather than changing a constructor signature. _(Req 1.6, 3.1)_
- [~] 5.5 Write failing tests for `TermService.TagArchive`: flat resolution, no
      ancestry, `term_taxonomy.parent` ignored for `post_tag`, `ErrNotFound` for
      an unknown slug, empty archive for an existing tag with no posts.
      _(Req 5.1–5.4)_
- [~] 5.6 Implement `TagArchive`. _(Req 5.1, 5.2)_
- [~] 5.7 Remove `TermService.CategoryPage` and `TermService.Category`.
      `CategoryPage`'s only production caller is `web.category`, which this
      milestone replaces; `Category` has no production caller at all. Update
      `internal/content/term_test.go` accordingly. _(Req 3.1)_
- [~] 5.8 Write failing tests for `PostService.AuthorArchive` and
      `DateArchive`: author resolved by nicename with `display_name` as the
      heading and `user_login` never present in the result; `ErrNotFound` for an
      unknown nicename; an existing author with no posts yielding an empty
      archive; date headings formatted per granularity (`2024`, `May 2024`,
      `17 May 2024`); an empty date archive yielding `200`-shaped data rather
      than `ErrNotFound`. _(Req 6.5, 7.1, 7.3, 7.4)_
- [~] 5.9 Add `PostService.WithAuthors(domain.UserRepository)` and implement
      `AuthorArchive`/`DateArchive`. Every archive read goes through the existing
      `clamp`/`newPage` helpers in `pagination.go`, so there is exactly one
      pagination shape. _(Req 8.1)_

## Phase 6 — Render data and the default theme

- [~] 6.1 Write a failing test asserting `render.ArchiveData` renders through the
      `category`, `tag`, `author` and `date` kinds, and that a value constructed
      as `render.CategoryData{...}` with the pre-M9b field set still compiles and
      renders — the alias is what stops a theme template failing at execution
      time on a field that no longer exists. _(Req 11.1, 11.2)_
- [~] 6.2 Add `ArchiveData` to `internal/render/view.go` and make `CategoryData`
      a type alias for it. Do **not** touch the `hierarchy` map in
      `engine.go`: M9a already registered all three new kinds. _(Req 11.1, 11.4)_
- [~] 6.3 Write a failing test asserting `archive.tmpl` renders the supplied
      heading and, when `TotalPages > 1`, pagination links built from
      `.BaseURL`. _(Req 8.4, 11.3)_
- [~] 6.4 Update `themes/default/templates/archive.tmpl` to render `.Heading`
      and the pagination block, and switch
      `themes/default/templates/category.tmpl`'s links from the hard-coded
      `/category/{{.Term.Slug}}?page=…` to `.BaseURL` — that hard-coded form is
      wrong the moment a category is nested or `category_base` is overridden.
      Update `internal/render/golden_test.go` expectations. _(Req 8.4, 11.3)_

## Phase 7 — Web routing and handlers

- [~] 7.1 Write failing handler tests covering **every row** of `design.md`'s
      status-code table: nested canonical path `200`; flat path `301` to nested
      with exact `Location`; wrong-ancestor path `301`; wrong-trailing-slash
      `301`; unknown final segment `404`; existing-but-empty category `200`;
      bare base segment `404` in both slash forms; tag/author canonical `200`,
      wrong-slash `301`, unknown `404`, empty `200`; date archive `200` at all
      three granularities; impossible date `404`; and `?page=` past the last page
      `404` on each kind. _(Req 2.3–2.9, 5.3, 5.4, 6.1, 6.3, 6.5, 7.3, 8.3)_
- [~] 7.2 Write a failing test asserting each archive kind's canonical path
      issues **no** redirect, guarding against a loop — the nested-category case
      especially, since its target comes from a graph walk. _(Req 2.4)_
- [~] 7.3 Write a failing test asserting the query string survives every archive
      canonical redirect. _(Req 2.9)_
- [~] 7.4 Write a failing **regression** test asserting the routes registered
      before the dispatcher are unaffected: `/`, `/login`, `/comment`,
      `/healthz`, `/wp-content/uploads/*`, `/admin/api/...`, `/wp-json/...`, the
      M9a permalink patterns and the flat `/{slug}` fallback all return what they
      returned before this milestone. Assert it; do not conclude it by
      inspection. _(Req 9.6)_
- [~] 7.5 Add `Server.resolve` to `internal/web/handlers.go`: call
      `Structure.Classify(r.URL.Path)` once and dispatch on `Kind`. Fold
      `refFrom` into `Classify` so the post path and the archive paths cannot
      disagree about what a path means, and pass the classified `Target` into
      `single` instead of having it re-derive one. `single`'s observable behavior
      must be byte-for-byte unchanged. _(Req 9.1, 9.2)_
- [~] 7.6 In `internal/web/router.go`, replace the hard-coded
      `/category/{slug}` registration with `Structure.ArchivePatterns()`, and
      register every pattern from `ChiPatterns()`, `ArchivePatterns()` and the
      flat `/{slug}` to `s.resolve`. Registering colliding patterns to **one**
      handler is what makes chi's silent last-wins resolution at a parameter node
      unobservable, rather than something the code has to win. Update the
      existing long route-registration comment with the new collision cases
      rather than deleting it — it is still the reason the design looks like
      this. _(Req 9.1, 9.2, 9.5)_
- [~] 7.7 Implement `categoryArchive`, `tagArchive`, `authorArchive` and
      `dateArchive` in `handlers.go`, each following the same four steps: resolve
      the entity (`404` if absent) → compute the canonical path from `Structure`
      → `301` preserving the query string if the request path differs → read the
      page and `404` when out of range → render. Remove the old `category`
      handler. _(Req 2.3, 5.1, 6.1, 7.1, 8.3)_
- [~] 7.8 Wire the new opt-in service dependencies in `cmd/grimoire/main.go`:
      `content.NewTermService(...).WithHierarchy(repos.TermReader)` and
      `content.NewPostService(...).WithCounter(...).WithAuthors(repos.Users)`.
      _(Req 1.6, 7.2)_

## Phase 8 — REST parity

- [~] 8.1 Write a failing test asserting `restTerm.Parent` carries
      `Term.ParentID` (it is hard-coded `0` today), that a nested category's
      `link` is its canonical nested path, that an overridden `category_base`
      is reflected, that a tag's `link` is `/{TagBase}/{slug}` rather than
      today's `/?tag={slug}`, and that `userLink` is `/author/{nicename}`
      rather than `/?author={id}`. _(Req 10.1, 10.2, 10.3, 7.5)_
- [~] 8.2 Write a failing test asserting `commentLink` is **unchanged** on its
      `/?p={id}#comment-{id}` fallback — no route is added for it, so changing it
      would advertise a URL that `404`s. _(Req 10.4)_
- [~] 8.3 Make `restTermLink` a method on `*Server` so it can reach the
      structure and the ancestry, resolving the term graph **once per REST
      request** and reusing it across every term in the response — a
      `/wp-json/wp/v2/categories` listing must not issue one graph read per row.
      Update `termToREST` to carry `ParentID`. _(Req 10.1, 10.2)_
- [~] 8.4 Point `userLink` in `internal/content/rest.go` at
      `Structure.AuthorPath`. _(Req 7.5, 10.3)_

## Phase 9 — Startup reporting

- [~] 9.1 Write a failing test asserting `grimoire-cli migrate -check` reports
      the resolved category and tag bases, and prints each `Structure.Notes()`
      entry when a base collides. _(Req 4.6)_
- [~] 9.2 Update `cmd/grimoire-cli/permalinks.go` to report both bases and the
      notes — every `Structure.Notes()` entry — in **all three** branches: the
      unsupported branch, the `Flat` branch and the resolved-non-flat branch. And
      **replace** the comment explaining why the bases are deliberately
      omitted — its stated reason ("no route honors either one yet") expires with
      this milestone, and leaving it would be the same misinformation in the
      opposite direction. The three-branch requirement is not stylistic:
      `reportPermalinks` `return`s early in both the unsupported branch
      (`permalinks.go:52`) and the `Flat` branch (`:57`), while
      `routing.Parse` populates `CategoryBase`/`TagBase` **before** either of
      those exits — the error path returns a `flat` copy that already carries
      them (`internal/routing/routing.go:111-123`). A plain-permalink site with
      `category_base` set still serves its archives at that base, so an
      implementation that merely appends the bases after the last branch reports
      nothing for the two most common configurations. _(Req 4.6, 13.3)_
- [~] 9.3 Confirm `cmd/grimoire/permalinks.go`'s startup `INFO` line still names
      the resolved bases, and report them plus each `Structure.Notes()` entry (at
      `WARN`, alongside the structure line) in **all three** branches of
      `resolvePermalinks`: the unsupported branch (`:31-48`), the `Flat` `INFO`
      and the resolved `INFO`. The `Flat`
      and resolved branches already name the bases; the unsupported branch names
      **none** today, even though `Parse` returns them on that path too and the
      site still serves `/{CategoryBase}/{slug}` while the flat fallback is
      active — so that is the branch where an operator most needs to see which
      base is in effect. _(Req 4.4)_
- [~] 9.4 Write a failing test asserting `grimoire-cli migrate -check` reports
      duplicate `user_nicename` values: for each duplicate it names the nicename,
      how many rows share it, and which `ID` wins; it states the consequence —
      that the other author's posts are unreachable at `/author/{nicename}` — so
      the line is actionable rather than trivia; and it prints **nothing** on a
      database with no duplicates, because a clean report that is silent is what
      makes a noisy one worth reading. _(Req 7.8)_
- [~] 9.5 Wire `domain.NicenameAuditor.DuplicateNicenames` into `reportPreflight`
      in `cmd/grimoire-cli/main.go` — the report depends on the narrow
      `NicenameAuditor`, not on `UserRepository`, and `*wprepo.UserRepo` is passed
      as it already is — alongside the existing `reportPermalinks` call
      and after `migrate.Preflight` has reported a clean schema, so
      `{prefix}users` is known to exist. Keep `-check` read-only: this is one
      aggregate query and no write. Do **not** abort the report on a read error —
      the permalink report already declines to, and withholding the
      pending-migration summary over a diagnostic line would be the wrong
      trade. _(Req 7.8)_

## Phase 10 — End-to-end, real database, docs, gates

- [~] 10.1 Add a `test/e2e` test booting the stack with a nested category chain
      and a non-default `category_base`: the nested path renders `200`, the flat
      path `301`s to it, a parent's archive includes a child's post, and a tag,
      date and author archive each render. Build the DSN with the existing
      `testDSN` helper so it inherits the SQLite `busy_timeout` setting.
      _(Req 12.5)_
- [~] 10.2 Extend `internal/routing/realdb_test.go` with the nested-category
      resolver check: read the target site's own `category_base` and category
      hierarchy, derive each expected nested path **independently of
      `CategoryPath`** so the implementation cannot merely agree with itself, and
      round-trip every derived path back through `Classify`. Same
      `GRIMOIRE_TEST_WP_DSN` / `GRIMOIRE_TEST_WP_PREFIX` gating — no new
      mechanism. _(Req 12.6)_
- [~] 10.3 Extend `test/e2e/m9_permalinks_realdb_test.go` with the end-to-end
      nested-category assertions against the same live database: a real nested
      category's canonical path renders `200` and its flat path `301`s to it.
      _(Req 12.6)_
- [~] 10.4 Make both real-database extensions **skip** with a message naming the
      precondition when the target has no category with `parent <> 0`, rather
      than pass. A check that reports success for a claim it never exercised is
      worse than no check. _(Req 12.7)_
- [~] 10.5 Run both env-gated checks against the live WordPress database — the
      `accuweaverllc/scripts` podman stack, from a container on the stack's own
      network since MySQL is not published to the host — and record the observed
      numbers here (category count, nested count, paths validated), as M9a's
      task 7.2 does. _(Req 12.6)_
- [~] 10.6 Update `docs/compatibility.md`: move "No tag, date or author archive
      routes", "No nested category paths" and "`category_base` and `tag_base`
      are read and resolved but not yet honored by any route" out of M9a's
      still-open list into "What's implemented", and record the new divergences —
      the date-archive-beats-post-slug and base-segment-beats-post-slug
      precedence consequences (Req 9.3, 9.4), `?page=` rather than
      WordPress's `/page/{n}/`, and the duplicate-`user_nicename` divergence —
      WordPress resolves arbitrarily (no `ORDER BY`, behind an object cache, so
      the winner can flip on a cache flush), grimoire resolves to the lowest
      `ID`, the impact is identical either way (one colliding author is
      unreachable at that URL), and `migrate -check` reports the condition.
      Record what grimoire matches too: the front's asymmetry between date/author
      and category/tag archives (Req 4.7–4.9) and the `date/` disambiguation
      prefix (Req 6.7) are WordPress behaviors, not divergences, and readers will
      assume otherwise if they are listed without that framing.
      _(Req 13.1, 13.5)_
- [~] 10.7 Update the root `README.md` Status section and its M9-remainder
      bullet; both currently name these gaps. _(Req 13.2)_
- [~] 10.8 Update `plans/README.md`: this milestone → Implemented, and tick
      groups **9.C**, **9.D** and **9.E** in
      `../wordpress-core-parity-roadmap/tasks.md` with a note naming what
      shipped. **Tick the boxes in this file as the work lands** — four
      milestones once shipped with their checkboxes untouched, which is why #29
      existed. _(Req 13.4)_
- [~] 10.9 Confirm no new migration file was added anywhere in the repo
      (`git status` shows no new file under any `migrations`-style directory) —
      this milestone must ship with zero schema changes. _(Req 1.3, 12.8)_
- [~] 10.10 Full gate sweep: `gofmt -l .` empty, `go vet ./...`,
      `go build ./...`, `go test ./...` all green, with the MySQL and Postgres
      contract runs included (the `cross-vendor-test` CI job runs all three).
