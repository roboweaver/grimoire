# Implementation Plan: M12 — custom post types and taxonomies (read-first)

## Overview

> **This milestone lets an operator declare custom post types (CPTs) and custom
> taxonomies (CTs) in configuration and serves them read-only: public HTML
> routing, hierarchical variants, and REST GET.** The lower layers are already
> parameterized — `PostRepo.BySlug`/`PublishedByID` take `types ...string`
> (`internal/storage/wprepo/repo.go:160`, `:194`), `ArchiveFilter` carries
> `Taxonomy`/`TermIDs`/`Types` (`internal/domain/repository.go`, applied by
> `archiveWhere`, `repo.go:256`), and `LoadTermHierarchy` is taxonomy-parameterized
> (`internal/content/archive.go:94`). M12 wires a registry through those seams. It
> adds: a `content:` config section (`internal/config/content.go`); a leaf
> `internal/registry` package (all D4 validation); three classifier Kinds, `Target.TypeName`
> and `Structure.WithCustomTypes` with four pure builders (`internal/routing`); one
> additive `PostNodeReader` port plus `AuthorsOfTypes` and two names-and-counts
> diagnostic reads (`internal/domain`, `internal/storage/wprepo`); `CustomPostService`
> and `TermService.TaxonomyArchive` (`internal/content`); three handlers, one
> registration block, and REST read registration (`internal/web`); an
> `auth.CapabilitySuffix` helper (`internal/auth/policy.go:11`); a `migrate -check`
> diagnostic (`cmd/grimoire-cli`); and a `buildHandler(serverDeps)` assembly function
> with its own test (`cmd/grimoire`). It adds NO migration, NO column, NO table, NO
> capability name, NO dependency, and NO second permalink construction site.
>
> **Security posture (D2): configuration declares, the database never exposes.** The
> registry is built at startup from operator configuration ONLY. A post type or
> taxonomy is served only if it is declared; rows in `{prefix}posts` such as
> `shop_order`, `wpcf7_contact_form` or `acf-field-group` are never auto-exposed. The
> database is used to validate and diagnose (`migrate -check` reports undeclared types
> by name and count only), never to expose. The admin-API list `type` parameter, which
> forwards any string today (`internal/web/adminapi.go:226`), is tightened to
> `{post, page}` plus registered names (`400 invalid_type`).
>
> **12.C (write parity) is a separate later spec (D1).** Every POST/PUT/PATCH/DELETE
> on a CPT or CT REST route answers 501 through `restNotImplemented`
> (`internal/web/rest_router.go:133`), and the admin write path keeps rejecting types
> other than `post`/`page` (`internal/web/adminapi_posts.go:103`). No admin SPA change.
>
> **Standing gate convention.** Keep `gofmt -l .` empty and `go vet ./...`,
> `go build ./...`, `go test ./...` green after every phase, following the close-out
> discipline of M10a/M10.B/M10.C/M10.F/M11/M13/M14/M15.
>
> **Delivery in three pull requests.** PR 1 is Phase 1 only (the behavior-preserving
> assembly extraction) plus the spec files; it is already implemented. PR 2 is Phases 2
> through 9: custom post types and taxonomies served on the site as HTML. It carries
> the `content:` config section, the auth capability suffix, routing, the registry, the
> storage node-reader port, `TermService.TaxonomyArchive`, `CustomPostService`, the web
> wiring with the three HTML handlers, and the production wiring of the registry in
> `buildHandler` and `main()`. Declaring a type in `content:` serves its single,
> archive and term pages from the real binary, and with no `content:` section nothing
> changes. PR 3 is Phases 10 through 14: `AuthorsOfTypes` and the diagnostic reads,
> `WithAuthorTypes`, the REST view model, admin-list `type` validation, the REST CPT and
> CT routes with the route index and their assembly check, the `migrate -check`
> diagnostic, the real-DB fixture, the docs edit and the final gate. Each pull request
> is a complete, working increment. Splitting by layer was rejected because layers
> merged without a caller are dead code.
>
> **Strict-TDD spine.** Every behavior-adding sub-task is preceded by the sub-task that
> writes its failing test (1.1→1.2, 2.1→2.2, 3.1→3.2, 4.1→4.2, 4.3→4.4, 4.5→4.6,
> 4.7→4.8, 5.1→5.2, 5.3→5.4, 6.1→6.2, 7.1→7.2, 7.3→7.4, 8.1→8.2, 8.3→8.4, 9.1→9.2,
> 10.1→10.2, 11.1→11.2, 11.3→11.4, 12.1→12.2, 12.3→12.4, 12.5→12.6, 12.7→12.8,
> 13.1→13.2). A test task and its implementation task are in consecutive waves.
>
> **File ownership.** No two tasks in one wave edit the same file. Anything touching
> `internal/routing/{classify,routing,notes}.go`, `internal/web/{router,handlers,
> adminapi,rest_posts,rest_terms,rest_router}.go`, `internal/content/{term,rest}.go`,
> `internal/storage/storagetest/contract.go`, or `cmd/grimoire/{assembly,assembly_test,main}.go` is
> serialized. Only genuinely independent chains in different packages (config, auth,
> storage contract/impl, the routing test chain) share early waves, and the CLI, real-DB
> fixture and docs tasks share their own waves with the PR 3 chains.
>
> **The M15 lesson: assembly is extracted FIRST (PR #74).** M15 shipped with
> `Server.WithPages` and `RESTMapper.WithPages` never called from
> `cmd/grimoire/main.go`, so nested pages 404ed in the real binary while every test
> passed, because each test builds its own Server. Phase 1 extracts the construction
> into `buildHandler(serverDeps)` BEFORE any M12 feature exists, behavior-preserving, with
> a test that drives it; every later wiring change (`WithRegistry`, the registry-aware
> mapper) then lands inside a function whose test fails if a call is removed (Req 13).

## Tasks

## Phase 1 — Assembly extraction FIRST, behavior-preserving (`cmd/grimoire`, TDD)

> Ships in PR 1.

- [x] 1.1 Write the failing assembly test first. Add `cmd/grimoire/assembly_test.go`
      that calls the not-yet-existing `buildHandler(serverDeps)` over a migrated
      SQLite database, built as `storagetest`'s `newSQLiteRepos` and
      `backfill_cmd_test.go`'s `newBackfillSQLite` do (`storage.New` over
      `config.DatabaseConfig{Vendor:"sqlite", DSN:<t.TempDir()>/grimoire.db,
      TablePrefix:"wp_"}`, then `migrate.Apply(ctx, repos.DB(), storage.MigrationsFS,
      vendor, prefix)`), seeded through `repos.DB()` with plain SQL (a user row first)
      with a top-level page `about` and a nested page `team` (parent `about`). Supply
      the dependencies `main()` holds today: `render.Load` over `../../themes` with the
      `default` theme, an `auth.SessionManager` over the same repos, the permalink
      Structure parsed from `/%postname%/`, `sanitize.New()`, and an `slog` logger. At
      this phase `serverDeps` carries only `Repos`, `Config`, `Log`, `Permalinks`,
      `Sessions`, `ContentPolicy`, `Engine` and `ThemesDir` (the `Registry` field
      arrives in 9.2). Through `httptest` assert: `GET /about/team/` → 200; `GET
      /team/` → 301 to `/about/team/` (the nested page's flat leaf); `GET /about/` →
      200; `GET /wp-json/wp/v2/pages` → 200 and the nested item's REST `link` ends in
      the nested path `/about/team/`. Carry a comment table naming the call each
      assertion guards (`Server.WithPages` for the HTML rows, `restMapper.WithPages`
      for the REST `link`), so removing either from `buildHandler` fails `go test`
      (the PR #74 regression). Assert through HTTP behavior only; no reflection into
      unexported fields. RED is a compile failure naming `buildHandler`/`serverDeps`.
      Files: `cmd/grimoire/assembly_test.go`
      Verify: `go test ./cmd/grimoire -run BuildHandler` — fails to compile
      (`buildHandler`, `serverDeps` undefined). _(Req 13.3, 13.4; D10)_

- [x] 1.2 Extract the assembly from `main()` into `cmd/grimoire/assembly.go`, per
      design Component 11. Define `serverDeps{Repos, Config, Log, Permalinks,
      Sessions, ContentPolicy, Engine, ThemesDir}` and `buildHandler(deps serverDeps)
      (http.Handler, *content.PostWriteService, error)`. Move into it everything
      between the process-level steps and `http.Server`: the comment, media, nav-menu
      and option services; `uploadsBaseURL`; `PageService`; the REST mapper with
      `WithPermalinks`, `WithUploadsBaseURL` and `WithPages`; the featured-image
      service, application passwords, the term read/write adapter (`termReadWriter`
      moves beside its only user), the revision, autosave, post, media, user, term,
      post-terms and nav-menu write services; `posts`/`terms`; and `web.NewServer(...)`
      with the whole `With*` chain ending in `.Routes()`. The returned
      `PostWriteService` is the same instance the Server's admin writes use, handed to
      `scheduler.New`. The two panicking type assertions (e.g.
      `repos.NavMenus.(domain.NavMenuWriter)`, `main.go:155`) become `, ok` errors
      returned to `main()`, which logs and `os.Exit(1)`. `main()` keeps flags, `slog`,
      `config.Load`, `CheckUploadsDir`, `storage.New`/`defer repos.Close()`,
      `render.Load`, the `auth.SessionManager` literal, `resolvePermalinks`,
      `sanitize.New()`, the `http.Server`, the scheduler and signal handling, and calls
      `buildHandler` exactly once. No behavior change: same constructors, same order,
      same arguments.
      Files: `cmd/grimoire/assembly.go`, `cmd/grimoire/main.go`
      Verify: `go test ./cmd/grimoire -run BuildHandler` green; `go build ./...`;
      `go vet ./...`; `go test ./...` unchanged (no existing test edited);
      `grep -rn "web.NewServer(" cmd/grimoire/*.go` shows only `assembly.go` plus the
      existing throwaway in `main_test.go`'s `TestSinglePolicyWiring`;
      `grep -c "buildHandler" cmd/grimoire/main.go` shows one call; `main.go` no longer
      contains `.WithPages(`, `content.NewRESTMapper(` or `.Routes()`.
      _(Req 13.1, 13.2, 13.4; D10)_


## Phase 2 — Config `content:` section (`internal/config`, TDD)

> Ships in PR 2.

- [ ] 2.1 Write the failing config tests first. Add `internal/config/content_test.go`
      asserting: every Req 1.2 post type default (`public=true`, `rest_base=name`,
      `has_archive=false`, `slug=name`, `with_front=true`, `hierarchical=false`,
      `capability_type=post`) and every Req 1.3 taxonomy default; an explicit
      `public: false` / `with_front: false` survives defaulting (the `*bool` omitted-vs-false
      case); `has_archive` as `true` (Enabled, empty Slug), `false`, and a slug string
      (Enabled, Slug set) (Req 1.4); `has_archive: yes`, `no`, `on`, `off`, `y`, `n`,
      and the quoted strings `"true"`/`"false"` (any case) are load errors; a misspelt
      key inside `content:` (e.g. `hierarchial: true`) is a load error naming the field
      while a misspelt key under another section (e.g. `server:`) still loads
      (lenient elsewhere); an absent `content:` section yields the zero `ContentConfig`
      (Req 1.5); `applyDefaults` leaves an empty `Name` empty so `Slug`/`RESTBase` stay
      empty (`registry.Build` reports it) and performs no cross-entry check.
      Files: `internal/config/content_test.go`
      Verify: `go test ./internal/config -run Content` — fails (`ContentConfig`,
      `HasArchive` undefined). _(Req 1.1, 1.2, 1.3, 1.4, 1.5; D3, D4)_

- [ ] 2.2 Implement the `content:` section. New `internal/config/content.go` with
      `ContentConfig{PostTypes, Taxonomies}`, `PostTypeConfig`, `TaxonomyConfig`
      (`*bool` for `Public`/`WithFront`, plain `bool` for `Hierarchical`), and
      `HasArchive{Enabled bool; Slug string}` exactly per design Component 1.
      `ContentConfig.UnmarshalYAML` re-encodes its node and decodes it with
      `yaml.Decoder.KnownFields(true)`; `HasArchive.UnmarshalYAML` accepts a `!!bool`
      scalar or a non-empty `!!str` and rejects (case-insensitively) `yes`, `no`, `on`,
      `off`, `y`, `n`, `true`, `false`. Add `Content ContentConfig \`yaml:"content"\``
      to `Config` (`internal/config/config.go:20-28`) and call a
      `ContentConfig.applyDefaults()` from `applyDefaults` (`config.go:186`) setting
      only the scalar defaults. `config.Load`'s plain `yaml.Unmarshal` (`config.go:128`)
      stays lenient for every other section.
      Files: `internal/config/content.go`, `internal/config/config.go`
      Verify: `go test ./internal/config` green; `go build ./...`; `go vet ./...`.
      _(Req 1.1, 1.2, 1.3, 1.4, 1.5; D3, D4)_


## Phase 3 — Auth capability suffix (`internal/auth`, TDD)

> Ships in PR 2.

- [ ] 3.1 Write the failing auth tests first. In `internal/auth` add a test (a new
      `policy_capsuffix_test.go`) asserting `CapabilitySuffix("")` → `"posts"`,
      `CapabilitySuffix("post")` → `"posts"`, `CapabilitySuffix("page")` → `"pages"`,
      `CapabilitySuffix("event")` → `"events"`, `CapabilitySuffix("story")` → `"storys"`
      (WordPress's string-form rule, no irregular plurals); and that `capSuffix`
      returns byte-identical results for `post` and `page` and the unknown-type default
      by re-running the existing `CanCreatePost`/`CanEditPost`/`CanDeletePost` policy
      vectors unchanged (Req 8.3).
      Files: `internal/auth/policy_capsuffix_test.go`
      Verify: `go test ./internal/auth -run CapabilitySuffix` — fails
      (`CapabilitySuffix` undefined). _(Req 8.2, 8.3; D7)_

- [ ] 3.2 Add `auth.CapabilitySuffix(capabilityType string) string` (returns
      `capabilityType + "s"`, `"posts"` for empty) in `internal/auth/policy.go` and
      rewrite `capSuffix(postType)` (`policy.go:11`) to call it with `"page"` for
      `page` and `"post"` otherwise. No new capability name, no new write-policy
      function.
      Files: `internal/auth/policy.go`
      Verify: `go test ./internal/auth` green (the pre-existing policy vectors pass
      unedited); `go build ./...`; `go vet ./...`. _(Req 8.2, 8.3; D7)_


## Phase 4 — Routing: custom kinds, constructors, patterns, classifier rows (`internal/routing`, pure, TDD, serialized)

> Ships in PR 2.

- [ ] 4.1 Write the failing tests for the route value types and helpers (4a). Add
      `internal/routing/custom_types_test.go` (`package routing`) asserting:
      `Structure.WithCustomTypes(types, taxes)` returns a copy and leaves the receiver
      unmodified; `WithCustomTypes(nil, nil)` is observably identical (same
      `Classify` over a corpus, same `ArchivePatterns`, same `Notes()`); `Front()`
      returns a defensive copy of the front segments; `ReservedBases()` returns the
      resolved category, tag and author sequences built by the same `archivePrefix` +
      `baseSegments` helpers, with a custom `category_base` with and without the front;
      `CustomBase(slug, withFront)` prepends the front only when `withFront` and is a
      no-op on a Flat structure; and the `frontIf` refactor of `archivePrefix` changes
      no archive output — re-assert the existing category/tag/author/date archive
      `Classify`, `CategoryPath`/`TagPath`/`AuthorPath` and `ArchivePatterns` vectors.
      Files: `internal/routing/custom_types_test.go`
      Verify: `go test ./internal/routing -run CustomTypes` — fails to compile
      (`TypeRoute`, `TaxRoute`, `WithCustomTypes`, `Front`, `ReservedBases`,
      `CustomBase` undefined). _(Req 4.4, 7.2, 7.3; D5, D6)_

- [ ] 4.2 Implement the route value types and helpers. In
      `internal/routing/routing.go` add `TypeRoute{Name, Slug, ArchiveSlug, WithFront,
      Hierarchical}` (+ `HasArchive()`), `TaxRoute{Name, Slug, WithFront,
      Hierarchical}`, the unexported `customTypes`/`customTaxes` fields beside `notes`,
      `Structure.WithCustomTypes` (value copy, `slices.Clone` both slices), `Front()`,
      `ReservedBases()`, `CustomBase()`, and extract `frontIf(withFront bool) []string`,
      which both `archivePrefix` (`routing.go:480-500`) and `CustomBase` call so the
      "which prefixes carry the front" decision stays single-sited. `archivePrefix`
      output is unchanged. `routing` imports only `domain`; it must not import
      `registry` or `config`.
      Files: `internal/routing/routing.go`
      Verify: `go test ./internal/routing -run CustomTypes` green; `go test
      ./internal/routing` green (existing archive vectors unchanged); `go build ./...`;
      `go vet ./...`. _(Req 4.4, 7.2, 7.3; D5, D6)_

- [ ] 4.3 Write the failing tests for the path constructors and patterns (4b). Add
      `internal/routing/custom_path_test.go` asserting: `CustomPostPath(typeName,
      ancestry)` builds `/{slug}/{postname}` under a slash-less structure and
      `/{slug}/{postname}/` under a trailing-slash one, prepends the front when
      `with_front` (`/blog/books/dune/`), uses NO dated permalink tokens (Req 4.5),
      nests for a hierarchical type (`/volumes/vol-1/part-one/`), and returns `""` for
      an unregistered type, an empty ancestry, and a non-hierarchical type given more
      than one segment; `CustomArchivePath(typeName)` returns the archive path
      (including a distinct `ArchiveSlug`, e.g. `/library/`) and `""` unless the type
      is registered with an archive; `CustomTermPath(taxName, ancestry)` mirrors
      `CustomPostPath`; a Flat structure yields `/books/dune` regardless of
      `with_front`; `CustomPatterns()` is nil when nothing is registered, otherwise
      contains for each public type `joinPattern(base,"*")` and, when `has_archive`,
      the bare archive in both slash forms (the chi probe: `/products/*` does not
      match bare `/products`, so a front-ful archive needs it explicit), and for each
      taxonomy `joinPattern(base,"*")` with NO bare pattern; the set is duplicate-free
      (chi overwrites a duplicate silently), asserted like `ArchivePatterns`' test.
      Files: `internal/routing/custom_path_test.go`
      Verify: `go test ./internal/routing -run CustomPath` — fails to compile
      (`CustomPostPath`, `CustomArchivePath`, `CustomTermPath`, `CustomPatterns`
      undefined). _(Req 4.4, 4.5, 4.6, 5.1, 6.2, 6.5, 7.5; D5)_

- [ ] 4.4 Implement the constructors and `CustomPatterns`. In
      `internal/routing/routing.go` add `CustomPostPath`, `CustomArchivePath`,
      `CustomTermPath` (the only places a custom URL is built, beside
      `CategoryPath`/`TagPath`/`AuthorPath`, reusing `archivePath` for the
      trailing-slash rule, `routing.go:653`) with the `""` "no servable canonical"
      guards, and `CustomPatterns()` (beside `ArchivePatterns`, `routing.go:698`)
      derived from the same `CustomBase`. No second builder is added.
      Files: `internal/routing/routing.go`
      Verify: `go test ./internal/routing -run CustomPath` green; `go test
      ./internal/routing`; `go build ./...`; `go vet ./...`.
      _(Req 4.4, 4.5, 4.6, 5.1, 6.2, 6.5, 7.5; D5)_

- [ ] 4.5 Write the failing classifier tests (4c). Add
      `internal/routing/classify_custom_test.go` asserting, for the new Kinds
      `KindCustomPost`, `KindCustomArchive`, `KindCustomTerm` and `Target.TypeName`
      (the registry key, distinct from `Slug`): single `/{slug}/{postname}/` →
      `KindCustomPost` with `Segments`; archive `/{archive-slug}/` →
      `KindCustomArchive`; term `/{slug}/{term}/` → `KindCustomTerm`; hierarchical
      entries claim arbitrary depth; a non-hierarchical entry with a deeper path is
      `KindNone` (not a fall-through to `KindPage`); the BARE base for a type without
      an archive and for any taxonomy is NOT claimed and falls through to row 8
      (`KindPost`), so a page slugged `genre` stays reachable; precedence — the archive
      row sits after the category/tag/author/date rows (a year-shaped archive slug
      loses to the date row) and BEFORE the post-shape row, so under `/%postname%/`
      and `/blog/%postname%/` the archive slug classifies as `KindCustomArchive` and
      not `KindPost` (Req 7.8); a post or page slugged like the archive slug is
      unreachable (7.9); singles and terms sit after row 7 and before the M15
      `KindPage` row, so a page whose path starts with a registered base is claimed
      by the custom row (7.4); the classifier/constructor fixed point over the finite
      matrix structures (flat, `/%postname%/`, `/blog/%postname%/`,
      `/%year%/%monthnum%/%postname%/`, trailing-slash and slash-less variants,
      Numeric preset) × entries (flat, hierarchical, with/without front, archive
      on/off) × slash forms — `Classify(constructor(...))` yields the matching Kind,
      `TypeName` and `Segments` equal to the ancestry, and a constructor never returns
      a path `Classify` declines; and the zero-registry regression — a Structure that
      never saw `WithCustomTypes` yields the SAME `Target` for every path in the
      existing classify vectors (Req 7.3). Classification is asserted pure and total
      (no DB; Req 7.2) and independent of chi order (7.7).
      Files: `internal/routing/classify_custom_test.go`
      Verify: `go test ./internal/routing -run ClassifyCustom` — fails to compile
      (`KindCustomPost`, `KindCustomArchive`, `KindCustomTerm`, `Target.TypeName`
      undefined). _(Req 4.1, 4.2, 5.1, 6.1, 7.1, 7.2, 7.3, 7.4, 7.7, 7.8, 7.9; D5, D6)_

- [ ] 4.6 Implement the Kinds, `Target.TypeName` and the two classifier insertions.
      In `internal/routing/classify.go` append `KindCustomPost`, `KindCustomArchive`,
      `KindCustomTerm` to the `Kind` block (`classify.go:11-22`, after `KindPage` so
      existing values keep their numbers), add `TypeName string` to `Target`
      (`classify.go:28-46`), and insert exactly per design Component 3: row 6.5 (custom
      archive, exact match against `CustomBase(tr.ArchiveSlug, tr.WithFront)`) between
      the date row and row 7; row 7.2 (custom single over `customTypes`, then custom
      term over `customTaxes`, via `trimSegmentPrefix`; bare base not claimed;
      non-hierarchical with `len(rest) != 1` returns `KindNone`; segments cloned) after
      row 7 and before the M15 `KindPage` row 7.5. The classifier stays pure and total.
      Files: `internal/routing/classify.go`
      Verify: `go test ./internal/routing -run ClassifyCustom` green; `go test
      ./internal/routing` green (every pre-existing classify vector unchanged); `go
      build ./...`; `go vet ./...`. _(Req 4.1, 4.2, 5.1, 6.1, 7.1, 7.2, 7.3, 7.4, 7.7,
      7.8, 7.9; D5, D6)_

- [ ] 4.7 Write the failing shadow-diagnostic tests (4d). Add
      `internal/routing/custom_notes_test.go` asserting `Structure.CustomNotes()`
      reports (a) a custom archive path equal to the structure's own post shape (the
      row 6.5 override, stating the archive wins and a post slugged like it is
      unreachable) and (b) an archive or single base whose first segment is a
      four-digit year (claimed first by the date row); is empty for a clean structure;
      and that the existing `Notes()` output and its tests are byte-identical
      (`CustomNotes` is a separate method).
      Files: `internal/routing/custom_notes_test.go`
      Verify: `go test ./internal/routing -run CustomNotes` — fails to compile
      (`CustomNotes` undefined). _(Req 7.4, 7.8, 7.9; D6)_

- [ ] 4.8 Implement `CustomNotes`. In `internal/routing/notes.go` (beside the existing
      note builders, `notes.go:39`, `:121`) add the unexported `shadowNotes()` and the
      exported `CustomNotes() []string`; in `routing.go` add the `customNotes` field and
      have `WithCustomTypes` set `out.customNotes = out.shadowNotes()`. `Notes()` is
      untouched.
      Files: `internal/routing/notes.go`, `internal/routing/routing.go`
      Verify: `go test ./internal/routing -run CustomNotes` green; `go test
      ./internal/routing` green; `go build ./...`; `go vet ./...`; `gofmt -l .` empty
      (phase gate). _(Req 7.4, 7.8, 7.9; D6)_


## Phase 5 — Registry (`internal/registry`, TDD)

> Ships in PR 2.

- [ ] 5.1 Write the failing `Build` rejection tests first. Add
      `internal/registry/build_test.go`, table-driven with ONE case per rule, each
      asserting the error text names the config path (e.g.
      `content.post_types[1].name`) and the offending value, and that the database is
      never consulted (`Build` takes no handle): R1 empty name; R2 characters outside
      `^[a-z0-9_-]+$`, 21-char post type and 33-char taxonomy, plus a bad
      `capability_type`; R3 each of the fourteen reserved post types and five reserved
      taxonomies; R4 duplicate names within a kind (a post type and a taxonomy MAY share
      a name); R5 (public entries only) multi-segment or malformed slug and archive slug,
      collision with another entry's resolved sequence including the prefix/equality
      rule (`blog/x` vs front-less `blog`), collision with `Structure.ReservedBases()`
      (category/tag/author, custom `category_base` with and without the front), equality
      with the structure's front or a prefix of it, and each first-segment reserved route
      (`admin`, `wp-json`, `wp-admin`, `wp-content`, `wp-includes`, `assets`, `theme`,
      `login`, `logout`, `comment`, `healthz`, `favicon.ico`, `feed`); R6 `rest_base`
      malformed, duplicated across ALL entries (post types and taxonomies share
      `/wp/v2/`), or equal to `posts`, `pages`, `media`, `comments`, `users`,
      `categories`, `tags` or the unimplemented core set (`types`, `taxonomies`,
      `statuses`, `settings`, `search`, `blocks`, `themes`, `plugins`), and a taxonomy
      `rest_base` equal to a `restItemCommon` JSON key (`id`, `date`, `slug`, `status`,
      `type`, `link`, `title`, `content`, `excerpt`, `author`, `featured_media`, …); R7
      unknown or reserved-non-served `taxonomies`/`object_types` references (e.g.
      `nav_menu`); `errors.Join` reports all violations of a three-mistake file in one
      pass; `public: false` entries are excluded from the slug-collision checks but
      included in the `rest_base` checks; a declared type with zero rows is not an error
      (R8).
      Files: `internal/registry/build_test.go`
      Verify: `go test ./internal/registry -run Build` — fails (package has no
      implementation). _(Req 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.9; D2, D4)_

- [ ] 5.2 Implement `registry.Build` and the reserved sets. New files
      `internal/registry/types.go` (`PostType`, `Taxonomy`, the `Registry` struct with
      ordered slices and name/`rest_base` indexes), `internal/registry/reserved.go`
      (`ReservedPostTypes()`, `ReservedTaxonomies()` exported so `migrate -check`
      shares them, and the reserved-route and reserved-rest-base lists) and
      `internal/registry/build.go`: `Build(cfg config.ContentConfig, st routing.Structure)
      (*Registry, error)` running R1–R8 in the design's order and returning
      `errors.Join` of every violation. Validation calls the SAME pure helpers
      `Classify`/the constructors use (`Structure.ReservedBases`, `CustomBase`,
      `Front`) so the validator cannot drift. `Build` computes the EFFECTIVE
      `PostType.Taxonomies` / `Taxonomy.ObjectTypes` as the bidirectional union in
      declaration order. `registry` imports `config` and `routing` only; no storage,
      HTTP or content imports.
      Files: `internal/registry/types.go`, `internal/registry/reserved.go`,
      `internal/registry/build.go`
      Verify: `go test ./internal/registry -run Build` green; `go build ./...`; `go vet
      ./...`. _(Req 3.1–3.9; D2, D4)_

- [ ] 5.3 Write the failing registry behavior tests. Add
      `internal/registry/registry_test.go` asserting: every lookup is nil-receiver safe
      and reports `ok=false` / empty on a nil `*Registry` (`Empty`, `PostTypes`,
      `Taxonomies`, `PostType`, `Taxonomy`, `PostTypeByRESTBase`, `TaxonomyByRESTBase`,
      `TaxonomyRESTBase`); an absent `content:` section builds an empty (non-nil)
      registry (Req 1.5); the bidirectional effective-set union (`book.Taxonomies ==
      [shelf]` and `shelf.ObjectTypes == [book]` whichever side declared it,
      deduplicated, declaration order); `TaxonomyRESTBase` resolves built-ins
      (`category` → `categories`, `post_tag` → `tags`) and registered names;
      `PostType.HasArchive()` and the archive-slug resolution (`has_archive: true` uses
      the rewrite slug, a string uses itself, `public: false` ignores it); `Heading()`
      replaces `-`/`_` with spaces and upper-cases the first letter; `Routes()` projects
      PUBLIC entries only into `routing.TypeRoute`/`TaxRoute`; `Notes()` reports a
      taxonomy attached to no object type (non-fatal, Finding 9); and the config set
      never grows from database state (D2, Req 1.6).
      Files: `internal/registry/registry_test.go`
      Verify: `go test ./internal/registry -run Registry` — fails to compile (lookup
      methods, `Routes`, `Notes`, `Heading`, `HasArchive` undefined). _(Req 1.5, 1.6,
      2.7, 3.9, 4.8, 9.4; D2, D3)_

- [ ] 5.4 Implement the registry lookups. New `internal/registry/registry.go` with
      `Empty`, `PostTypes`, `Taxonomies`, `PostType`, `Taxonomy`, `PostTypeByRESTBase`,
      `TaxonomyByRESTBase`, `TaxonomyRESTBase`, `Routes`, `Notes`, and the `PostType`
      methods `HasArchive`/`Heading`, all nil-receiver safe.
      Files: `internal/registry/registry.go`
      Verify: `go test ./internal/registry` green; `go build ./...`; `go vet ./...`;
      `gofmt -l .` empty (phase gate). _(Req 1.5, 1.6, 2.7, 3.9, 4.8, 9.4; D2, D3)_


## Phase 6 — Storage: `PostNodeReader` (`internal/domain`, `internal/storage/wprepo`, factory, `storagetest`, TDD)

> Ships in PR 2.

- [ ] 6.1 Write the failing cross-vendor node-reader contract first. In
      `internal/storage/storagetest/contract.go` add `runPostNodeReaderContract`
      attached to `RunContract` beside `runPageReaderContract` (`contract.go:1915`);
      SQLite always, MySQL/Postgres env-gated through the existing harness (NO new env
      var), with a CPT fixture in its own ID band seeded via the extra-seed hook. Assert
      for `Set.PostNodeReader.ListPostNodes(ctx, postType, afterID, limit)`: rows of ONLY
      the requested type (a `page` row never appears for `book`); EVERY status present
      (publish, draft, private, trash …); ascending-`ID` keyset order; each node exactly
      once under a small `limit` including across a page boundary (no drop, no
      duplicate); the lean projection (ID, slug, parent, status); orphan (`post_parent`
      naming an absent ID) and cycle fixtures returned verbatim for the walk to degrade;
      and `ListPostNodes(..., "page", ...)` equals `ListPages` on the same fixture
      (`ListPages` itself untouched).
      Files: `internal/storage/storagetest/contract.go`
      Verify: `go test ./internal/storage/storagetest -run SQLite` — fails to compile
      (`Set.PostNodeReader` undefined). _(Req 2.4, 2.5, 5.2, 5.6, 14.1; D9, D10)_

- [ ] 6.2 Implement the node-reader port. In `internal/domain/repository.go` (beside
      `PageNode`/`PageReader`, `:502-530`) add `type PostNode = PageNode`
      and `PostNodeReader` with doc comments naming each requirement (the
      `AuthorTypeReader` and auditor ports arrive with their implementations in 10.2). In
      `internal/storage/wprepo/repo.go` add `(*PostRepo).ListPostNodes` — the body of
      `ListPages` (`repo.go:351`) with `Where("post_type = ?", postType)`, same
      projection, `bun.Ident("ID")` keyset, NO `post_status` filter; `ListPages` is left
      exactly as is. In `internal/storage/factory.go` add ONE `Set.PostNodeReader`
      field assigned `posts` in `New` (beside `PageReader`, `factory.go:69`, `:204`).
      Add a compile-time `var _` guard.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/repo.go`,
      `internal/storage/factory.go`
      Verify: `go test ./internal/storage/storagetest -run SQLite` green for the node
      reader; `go build ./...`; `go vet ./...`; `postColumns` (`repo.go:17-24`)
      unchanged; no file added under `internal/storage/migrations`. _(Req 2.4, 2.5,
      2.6, 5.2, 5.6, 14.1; D9)_


## Phase 7 — Content services for HTML (`internal/content`, TDD)

> Ships in PR 2.

- [ ] 7.1 Write the failing `TaxonomyArchive` tests (7a). Add
      `internal/content/taxonomy_archive_test.go` over fakes asserting
      `TermService.TaxonomyArchive(ctx, tax registry.Taxonomy, segments, page, perPage)`:
      hierarchical — `LoadTermHierarchy(ctx, reader, tax.Name)` + `ResolvePath`, filter
      `ArchiveFilter{Taxonomy, TermIDs: DescendantIDs(term.ID), Types: tax.ObjectTypes}`,
      result `Ancestry` from `h.Ancestry`; wrong ancestry returns the reused
      `*CategoryMovedError` carrying the canonical ancestry; flat — `len(segments) != 1`
      → `domain.ErrNotFound`, otherwise `terms.BySlug(ctx, tax.Name, seg)`, `TermIDs:
      {term.ID}`, nil `Ancestry`; the correctness trap — a dedicated test proving a
      term attached to CPT rows lists the CPT rows (`ArchiveFilter.Types` set to the
      taxonomy's object types; an empty `Types` would default to `{"post"}`,
      `repo.go:257-260`); empty `tax.ObjectTypes` returns an empty archive WITHOUT a
      query; a term with no published items is an empty archive with `Total` 0 (not a
      404); pagination via `clamp`/`newPage`; `Heading` is the term name; and
      `CategoryArchive`/`TagArchive` are byte-for-byte unchanged (re-run their existing
      tests).
      Files: `internal/content/taxonomy_archive_test.go`
      Verify: `go test ./internal/content -run TaxonomyArchive` — fails to compile
      (`TaxonomyArchive` undefined). _(Req 2.7, 6.1, 6.2, 6.3, 6.4, 6.6; D5)_

- [ ] 7.2 Implement `TermService.TaxonomyArchive`. In `internal/content/term.go`
      (beside `CategoryArchive`, `term.go:81`) add the method per design Component 6a,
      reusing `TermHierarchy`, `ResolvePath`, `recoverPath`, `CategoryMovedError`
      (naming debt documented in a doc-comment line, Finding 7) and `clamp`/`newPage`.
      `CategoryArchive` and `TagArchive` are NOT re-pointed.
      Files: `internal/content/term.go`
      Verify: `go test ./internal/content -run TaxonomyArchive` green; `go test
      ./internal/content`; `go build ./...`; `go vet ./...`. _(Req 2.7, 6.1, 6.2, 6.3,
      6.4, 6.6; D5)_

- [ ] 7.3 Write the failing `CustomPostService` tests (7b). Add
      `internal/content/custompost_test.go` over fakes asserting: non-hierarchical
      `Resolve` — `len(segments) != 1` → `ErrNotFound`; `BySlug` scoped to the type (a
      `post` of the same slug does not resolve for `book`, and the reverse); ancestry
      `[]string{post.Slug}`; no node read. Hierarchical `Resolve` via the
      post-type-binding `PageReader` adapter (`nodeReaderFor`) reusing
      `LoadPageHierarchy`/`PageHierarchy` — 200 for a nested published leaf with the
      fixed-point ancestry; wrong ancestry → `*PageMovedError` carrying the canonical
      ancestry; orphan parent degrades to the shorter resolvable path; a parent cycle
      terminates; the node graph is read ONCE (count `ListPostNodes` calls, Req 5.6); a
      draft leaf → `ErrNotFound`; a recovered node that is not published → `ErrNotFound`
      (no draft-slug disclosure, Finding 6); the leaf is confirmed by
      `PublishedByID(leaf.ID, typeName)`, NOT `BySlug` — two leaves sharing a slug under
      different parents each serve their own body (Finding 5). `Hierarchy` returns nil
      for a non-hierarchical type. `Archive` lists only the type's published items,
      newest first with a stable `post_date DESC, ID DESC` order, no item dropped or
      duplicated across pages, `Heading: pt.Heading()`. `NewCustomPostService` panics on
      a nil argument. `AncestryFor` returns the same ancestry as `Resolve`. M15's
      `PageService` tests remain green and unedited.
      Files: `internal/content/custompost_test.go`
      Verify: `go test ./internal/content -run CustomPost` — fails to compile
      (`CustomPostService` undefined). _(Req 4.1, 4.2, 4.3, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6,
      8.1; D5, D7)_

- [ ] 7.4 Implement `CustomPostService`. New `internal/content/custompost.go` per
      design Component 6b: `CustomPostService{nodes domain.PostNodeReader, posts
      domain.PostRepository}`, `NewCustomPostService` (panics on nil), `Resolve`,
      `AncestryFor`, `Hierarchy`, `Archive`, and the unexported `nodeReaderFor` adapter
      whose `ListPages` calls `ListPostNodes(ctx, a.postType, …)`. `PageHierarchy.Ancestry`,
      `ResolvePath` and `recoverPath` are reused unchanged; `PageMovedError` is reused
      (doc-comment line records the naming debt). `internal/content/pagehier.go` is NOT
      edited.
      Files: `internal/content/custompost.go`
      Verify: `go test ./internal/content -run CustomPost` green; `go test
      ./internal/content`; `go build ./...`; `go vet ./...`. _(Req 4.1, 4.2, 4.3, 5.1–5.6,
      8.1; D5, D7)_


## Phase 8 — Web: server wiring and HTML handlers (`internal/web`, TDD, serialized)

> Ships in PR 2.

- [ ] 8.1 Write the failing server-wiring tests (8a). Add
      `internal/web/router_custom_test.go` asserting: `Server.WithRegistry(reg,
      customPosts)` returns the Server for chaining; `contentPatterns` appends
      `st.CustomPatterns()` after `ArchivePatterns()` and before `"/{slug}"`
      (`router.go:402-406`) so the patterns exist iff the Structure carries entries and
      a Server on a plain Structure registers nothing new (the
      `routes_regression_test.go` corpus stays green); a Structure with custom patterns
      and a Server with no registry or no `CustomPostService` makes `Routes()` panic
      with the named message ("call Server.WithRegistry(…)"); with a registry but no
      taxonomy reader (`!s.terms.HasHierarchy()`) the taxonomy-specific panic fires
      (the `requireArchiveDeps` style, Req 7.6); the M15 `/{slug}/*` registration is
      unaffected; correctness does not depend on chi registration order (every custom
      pattern dispatches to `s.resolve`, Req 7.7); and a bare `/blog/products` archive
      resolves because its bare form is registered explicitly.
      Files: `internal/web/router_custom_test.go`
      Verify: `go test ./internal/web -run RouterCustom` — fails to compile
      (`WithRegistry` undefined). _(Req 7.5, 7.6, 7.7; D6)_

- [ ] 8.2 Implement the server wiring. In `internal/web/router.go` add the `registry
      *registry.Registry` and `customPosts *content.CustomPostService` fields (beside
      the existing optional services, `router.go:165`), `WithRegistry`, a
      `requireCustomDeps(st)` guard invoked from `Routes()` next to `requireArchiveDeps`
      (`router.go:306`), and the `st.CustomPatterns()` append in `contentPatterns`.
      "Register only when wired" is expressed by the Structure: no custom entries means
      nothing registered and nothing panics. `resolve` arms land in 8.4.
      Files: `internal/web/router.go`
      Verify: `go test ./internal/web -run RouterCustom` green; `go test ./internal/web`
      (existing suites unchanged); `go build ./...`; `go vet ./...`. _(Req 7.5, 7.6,
      7.7; D6)_

- [ ] 8.3 Write the failing handler tests (8b). Add `internal/web/handlers_custom_test.go`
      driving a Server wired with a registry over fakes/SQLite for `customPost`,
      `customArchive`, `customTermArchive`: 200 at the canonical; 301 from every
      non-canonical form (missing/extra trailing slash, wrong ancestry, flat leaf of a
      nested item) with the query string preserved RAW (`redirectCanonical` appends
      `r.URL.RawQuery`); 404 for unknown type/term/item, a draft, and a slug of another
      type; past-the-last-page 404 and `?page=N` pagination links built from the
      canonical; nested hierarchical CPT 200 with orphan degradation (shorter path) and
      cycle termination; hierarchical CT 200 and 301 via the reused
      `*CategoryMovedError`; flat CT 200 using the one-element resolved-term ancestry;
      a `public: false` entry unrouted; the defensive nil-registry 404 on each new
      `resolve` arm; the `render.post_html` filter fires for a CPT single; a CPT row
      renders `single.tmpl` and archives render kind `archive` (no template or engine
      change; an unknown render kind would fall back to `index`, `engine.go:88-93`).
      Files: `internal/web/handlers_custom_test.go`
      Verify: `go test ./internal/web -run HandlersCustom` — fails (arms and handlers
      absent: the new Kinds hit the default `resolve` branch). _(Req 4.1, 4.2, 4.3, 4.4,
      4.7, 4.8, 5.1, 5.2, 5.3, 5.4, 5.5, 6.1, 6.2, 6.3, 6.4, 6.5, 14.4; D5, D6)_

- [ ] 8.4 Implement the resolve arms and three handlers. In `internal/web/handlers.go`
      add to `resolve` (`handlers.go:65`) the `KindCustomPost`, `KindCustomArchive`,
      `KindCustomTerm` arms, each with the defensive `s.registry.Empty()` → `ErrNotFound`
      guard, and add `customPost` (mirrors `single()`/`page()`: resolve, canonical from
      the RESOLVED ancestry via `CustomPostPath`, `*content.PageMovedError` →
      `redirectCanonical`, then `renderSingle`), `customArchive` (mirrors `dateArchive`,
      `CustomArchivePath`, `pageParam`, `content.DefaultPerPage`) and `customTermArchive`
      (mirrors `categoryArchive`; `*content.CategoryMovedError` →
      `redirectCanonical(CustomTermPath(...))`). Update the `render.ArchiveData.Kind`
      doc comment in `internal/render/view.go:66` to list `"archive"`. No template
      change.
      Files: `internal/web/handlers.go`, `internal/render/view.go`
      Verify: `go test ./internal/web -run HandlersCustom` green; `go test
      ./internal/web ./internal/render`; `go build ./...`; `go vet ./...`. _(Req 4.1–4.8,
      5.1–5.5, 6.1–6.5, 14.4; D5, D6)_


## Phase 9 — Registry production wiring + assembly test extension (`cmd/grimoire`, TDD)

> Ships in PR 2.

- [ ] 9.1 Extend the failing assembly test (HTML behavior only). In
      `cmd/grimoire/assembly_test.go` seed a `book` post type (`slug: books`,
      `has_archive: true`, `taxonomies: [shelf]`) with row `dune`, a hierarchical
      `volume` (`slug: volumes`) with `vol-1` and child `part-one`, and a `shelf`
      taxonomy (`slug: shelves`, `object_types: [book]`) with term `fiction` attached to
      `dune`; build the registry with `registry.Build` from a literal
      `config.ContentConfig` over the `/%postname%/` Structure and pass it as
      `serverDeps.Registry`. Assert through the handler: `/books/dune/` 200 (CPT
      single); `/books/` 200 (CPT archive); `/volumes/vol-1/part-one/` 200
      (hierarchical CPT single) and `/volumes/part-one/` 301 to
      `/volumes/vol-1/part-one/` (wrong ancestry); `/shelves/fiction/` 200 (custom
      taxonomy term archive); an unregistered path (e.g. `/movies/alien/`) still 404;
      plus the Phase 1 page rows. Carry a table mapping EACH assertion to the wiring
      call it guards: `Server.WithRegistry` for the single, archive, hierarchical,
      301 and term rows, and `Structure.WithCustomTypes` inside `buildHandler` for the
      classifier/pattern registration behind every custom row and the unregistered 404.
      Add the empty-registry case: a handler built over an empty registry (no `content:`
      section) still serves `/about/team/` 200, `/team/` 301 and `/about/` 200, and
      returns 404 for `/books/dune/` without panicking (a site without `content:` is
      unchanged, Req 1.5). NO `/wp-json` custom-type assertion appears here; those land
      in 12.7. RED is a compile failure naming `serverDeps.Registry`.
      Files: `cmd/grimoire/assembly_test.go`
      Verify: `go test ./cmd/grimoire -run BuildHandler` — fails to compile
      (`serverDeps.Registry` undefined). _(Req 1.5, 13.3; D10)_


- [ ] 9.2 Wire the registry inside `buildHandler`. In `cmd/grimoire/assembly.go` add
      `Registry *registry.Registry` to `serverDeps` (nil-safe; empty when no `content:`
      section). Inside `buildHandler`, before any consumer is built: `types, taxes :=
      deps.Registry.Routes()`, `permalinks := deps.Permalinks.WithCustomTypes(types,
      taxes)`, log `permalinks.CustomNotes()` at WARN; use that single augmented
      Structure for BOTH the REST mapper (`.WithPermalinks(permalinks)`) and the Server;
      add `.WithRegistry(deps.Registry, content.NewCustomPostService(repos.PostNodeReader,
      repos.Posts))` to the Server chain. The REST mapper's registry
      (`.WithRegistry(deps.Registry)`) and the admin author list
      (`WithAuthorTypes`) are wired in 12.8 (PR 3), where their consumers land.
      `web.NewServer(` stays only in `assembly.go`.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire -run BuildHandler` green; `go test ./cmd/grimoire`;
      `go build ./...`; `go vet ./...`. _(Req 1.5, 7.5, 7.6, 13.1, 13.3; D4, D10)_


- [ ] 9.3 Build the registry in `main()`. In `cmd/grimoire/main.go`, immediately after
      `resolvePermalinks` (`main.go:104`) and before `buildHandler`, call
      `registry.Build(cfg.Content, permalinks)`; on error log `"invalid content
      configuration"` and `os.Exit(1)`; log each `reg.Notes()` at WARN; pass the result
      as `serverDeps.Registry`. After the repos exist, log at INFO each declared type
      with zero rows using the existing `CountByStatus` (informational; never a failure,
      Req 3.9). `main()` still calls `buildHandler` exactly once and contains no
      `WithRegistry(`/`WithCustomTypes(` calls.
      Files: `cmd/grimoire/main.go`
      Verify: `go build ./...`; `go vet ./...`; `go test ./cmd/grimoire` green;
      `grep -rn "web.NewServer(" cmd/grimoire/*.go` shows only `assembly.go` plus the
      `main_test.go` throwaway; `grep -c "buildHandler" cmd/grimoire/main.go` shows one
      call; `gofmt -l .` empty (phase gate; end of PR 2: declaring a type in `content:` serves its single, archive and term pages from the real binary, and with no `content:` section nothing changes). _(Req 3.1–3.9, 7.5, 7.6, 13.2; D2, D4)_


## Phase 10 — Storage: `AuthorsOfTypes` and diagnostic reads (`internal/domain`, `internal/storage/wprepo`, `storagetest`, TDD)

> Ships in PR 3.

- [ ] 10.1 Write the failing `AuthorsOfTypes` and diagnostic-read contract assertions.
      In `internal/storage/storagetest/contract.go` (serialized after 6.1/6.2, PR 2) add
      assertions: `AuthorsOfTypes(ctx, {"post","page"})` equals `Authors(ctx)` on the
      same fixture; `AuthorsOfTypes(ctx, {"post","page","book"})` additionally includes
      an author whose only rows are `book`; an author of only `shop_order` rows is never
      listed; `PostTypeAuditor.DistinctPostTypes` returns every distinct `post_type`
      with its row count across all statuses, sorted by name, and `TaxonomyAuditor.
      DistinctTaxonomies` the `term_taxonomy.taxonomy` twin — names and counts only
      (obtained by type assertion on `Set.AdminPosts` / the term repo, the
      `repos.NavMenus.(domain.NavMenuWriter)` precedent).
      Files: `internal/storage/storagetest/contract.go`
      Verify: `go test ./internal/storage/storagetest -run SQLite` — fails
      (`AuthorsOfTypes`, `DistinctPostTypes`, `DistinctTaxonomies` not implemented).
      _(Req 2.3, 11.1, 11.2, 14.1; D9)_

- [ ] 10.2 Implement `AuthorsOfTypes` and the two diagnostic reads. In `internal/domain/repository.go` (beside `PostNodeReader`) add `AuthorTypeReader`, `TypeCount`, `PostTypeAuditor` and `TaxonomyAuditor` with doc comments naming each requirement. In `internal/storage/wprepo/adminreads.go` add `(*PostRepo).AuthorsOfTypes(ctx,
      types)` — the body of `Authors` (`adminreads.go:109`, hard-coded list at `:118`)
      with the type list as a parameter — and make `Authors(ctx)` call
      `AuthorsOfTypes(ctx, []string{"post","page"})`, producing the identical statement.
      In a new `internal/storage/wprepo/audit.go` add `(*PostRepo).DistinctPostTypes`
      and `(*TermRepo).DistinctTaxonomies`: one `SELECT post_type, COUNT(*) … GROUP BY
      … ORDER BY` each (and the `term_taxonomy.taxonomy` twin), names and counts only,
      no content column. Compile-time `var _` guards for `AuthorTypeReader`,
      `PostTypeAuditor`, `TaxonomyAuditor`. `RecentPosts`, `adminTypes` default,
      `BySlug`/`PublishedByID` defaults and `archiveWhere` are deliberately unchanged.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/adminreads.go`, `internal/storage/wprepo/audit.go`
      Verify: `go test ./internal/storage/...` green; `go build ./...`; `go vet ./...`;
      `gofmt -l .` empty (phase gate). _(Req 2.1, 2.2, 2.3, 11.1, 11.2, 14.1; D9)_


## Phase 11 — Content services for the API side (`internal/content`, TDD)

> Ships in PR 3.

- [ ] 11.1 Write the failing `AdminService.WithAuthorTypes` tests. Add
      `internal/content/adminread_authortypes_test.go` asserting: when
      `WithAuthorTypes(reader, types)` is set, `Authors` calls
      `reader.AuthorsOfTypes(ctx, types)`; without it `Authors` behaves exactly as today
      (existing tests unchanged).
      Files: `internal/content/adminread_authortypes_test.go`
      Verify: `go test ./internal/content -run AuthorTypes` — fails to compile.
      _(Req 2.3; D9)_

- [ ] 11.2 Implement `WithAuthorTypes`. In `internal/content/adminread.go` (near `:112`)
      add `AdminService.WithAuthorTypes(r domain.AuthorTypeReader, types []string)` and
      route `Authors` through it when set.
      Files: `internal/content/adminread.go`
      Verify: `go test ./internal/content -run AuthorTypes` green; `go test
      ./internal/content`; `go build ./...`; `go vet ./...`. _(Req 2.3; D9)_

- [ ] 11.3 Write the failing REST mapper tests (7c). Add
      `internal/content/rest_custom_test.go` asserting `RESTMapper.WithRegistry(reg)` and
      `RESTMapper.CustomPost(ctx, p, pt, ancestry)`: `type` equals the CPT name; `link`
      equals `Structure.CustomPostPath(pt.Name, ancestry)` (never the post-shape
      `Canonical`); when `CustomPostPath` is `""` (a `public: false` type) `link` falls
      back to `/?p={id}&post_type={name}` and is never empty (`restAbs("")` returns the
      site root, `rest_envelope.go:77-80`); per attached taxonomy a term-id array keyed
      by that taxonomy's `rest_base` (`shelf`, and built-in `categories`), `[]` not null
      when empty; `RESTCustomPost.MarshalJSON` flattens `Terms` next to the common
      fields without overwriting any key; `featured_media` reuses the meta lookup; and
      `postLink` for `post` rows and the M15 page guard are unchanged (existing mapper
      vectors re-asserted).
      Files: `internal/content/rest_custom_test.go`
      Verify: `go test ./internal/content -run RESTCustom` — fails to compile
      (`RESTCustomPost`, `WithRegistry`, `CustomPost` undefined). _(Req 4.6, 9.3, 9.4;
      D8)_

- [ ] 11.4 Implement the REST view model. In `internal/content/rest.go` (beside
      `:337`) add `RESTCustomPost{restItemCommon; Terms map[string][]int64}` with
      `MarshalJSON`, `RESTMapper.WithRegistry`, and `RESTMapper.CustomPost` per design
      Component 6c: call `restCommon`, overwrite `Link` from `CustomPostPath` with the
      plain-permalink fallback (comment names the `postLink`/`Canonical` trap), and fill
      one term-id array per `pt.Taxonomies` via `TermsForPost` keyed by
      `reg.TaxonomyRESTBase(name)`. `postLink` and `Canonical` are unchanged.
      Files: `internal/content/rest.go`
      Verify: `go test ./internal/content -run RESTCustom` green; `go test
      ./internal/content`; `go build ./...`; `go vet ./...`; `gofmt -l .` empty (phase
      gate). _(Req 4.6, 9.3, 9.4; D8)_


## Phase 12 — Web: admin `type` validation, REST CPT/CT routes, route index and REST assembly check (`internal/web`, `cmd/grimoire`, TDD, serialized)

> Ships in PR 3.

- [ ] 12.1 Write the failing admin-list `type` validation tests (8d). Add
      `internal/web/adminapi_type_test.go` asserting `GET` on the admin posts list: a
      `type` that is not `post`, not `page` and not a registered name responds 400 with
      code `invalid_type` and reads NOTHING (the fake repo records zero calls); a
      registered type (public or not) serves that type's rows; an empty or absent `type`
      keeps the default `{post, page}`; `shop_order` is rejected; the write handlers
      still reject non-`post`/`page` (`adminapi_posts.go:103`) and
      `adminKnownTaxonomies` is unchanged (Req 12.2). Any pre-existing test that passes
      an arbitrary `type` is updated in the same change (Finding 2) — name it in the
      commit.
      Files: `internal/web/adminapi_type_test.go`
      Verify: `go test ./internal/web -run AdminAPIType` — fails (unvalidated `type`
      forwarded). _(Req 2.1, 2.2, 2.8, 2.9, 2.10, 12.2, 12.3; D1, D9)_

- [ ] 12.2 Implement the `type` validation. In `internal/web/adminapi.go` (`adminPosts`,
      `:213`, forwarding at `:226`) reject a `type` outside `{post, page}` plus
      `s.registry` names with 400 `invalid_type` before any read (the `invalid_status`
      precedent, `:222`). Edit any pre-existing test the tightening breaks. Write
      handlers, `parsePostWrite` and the admin SPA are untouched.
      Files: `internal/web/adminapi.go`
      Verify: `go test ./internal/web -run AdminAPIType` green; `go test ./internal/web`;
      `go build ./...`; `go vet ./...`. _(Req 2.1, 2.2, 2.8, 2.9, 2.10, 12.2, 12.3; D1,
      D9)_

- [ ] 12.3 Write the failing REST CPT read tests (8c, posts). Add
      `internal/web/rest_custom_posts_test.go` asserting for a registered CPT at
      `/wp-json/wp/v2/{rest_base}`: GET collection in the posts/pages envelope with the
      same pagination headers and the `slug` filter; GET single; `type` equals the CPT
      name, `link` equals the CPT canonical (nested for a hierarchical type, loaded from
      one graph read per request), per-taxonomy term-id arrays keyed by `rest_base`;
      a row of a different post type → 404; a non-published row → 404
      `rest_post_invalid_id` for anonymous AND for an authenticated caller lacking
      `edit_<suffix>`, 200 for a holder of `edit_`+`CapabilitySuffix(capability_type)`
      including a non-default `capability_type: event` → `edit_events`; a published row
      200 for everyone; POST on the collection and PUT/PATCH/DELETE (and POST on the
      single) → 501 via `restNotImplemented` with `rest_cannot_create`/`rest_cannot_edit`
      codes and nothing written; no pluralization (`rest_base: library` routes at
      `/library`, not `/librarys`); `post` and `page` REST JSON is byte-identical to
      before for the same fixtures (the five existing `mapRESTPost` call sites pass a
      nil hierarchy).
      Files: `internal/web/rest_custom_posts_test.go`
      Verify: `go test ./internal/web -run RESTCustomPosts` — fails (routes
      unregistered: 404). _(Req 8.1, 8.2, 8.4, 8.5, 8.6, 9.1, 9.2, 9.3, 9.4, 9.5, 9.6,
      12.1, 14.5; D1, D7, D8)_

- [ ] 12.4 Implement REST CPT registration. New `internal/web/rest_custom_posts.go` with
      `registerRESTCustomPosts` (for each `s.registry.PostTypes()`, public or not: GET
      collection → `handleRESTPostsCollection(pt.Name)`, GET single →
      `handleRESTPostSingle(pt.Name)`, every write verb → `restNotImplemented`). In
      `internal/web/rest_posts.go` generalize inside the shared handlers, each
      behavior-preserving for `post`/`page`: `mapRESTPost` (`rest_posts.go:493`) takes a
      trailing `hier *content.PageHierarchy` (existing call sites pass nil), its link base
      uses `s.restPostRoute(typ)` (`/posts`, `/pages`, or `/`+RESTBase — never `typ+"s"`,
      `:47`/`:494`), and a registered-CPT arm precedes the `default` Post arm
      (`:500-520`) calling `restMapper.CustomPost`; `restCommonFeaturedMedia` (`:558`)
      gains a `RESTCustomPost` case; `handleRESTPostSingle` swaps `canEditPosts` for
      `canReadUnpublished(r, typ)` (`edit_`+`CapabilitySuffix` for a registered type,
      `edit_posts` otherwise, `rest_posts.go:164`, `:184-187`); the collection and single
      handlers load the hierarchy once for a hierarchical CPT. In
      `internal/web/rest_router.go:63` add the `s.registerRESTCustomPosts(nr)` call.
      Files: `internal/web/rest_custom_posts.go`, `internal/web/rest_posts.go`,
      `internal/web/rest_router.go`
      Verify: `go test ./internal/web -run RESTCustomPosts` green; `go test
      ./internal/web`; `go build ./...`; `go vet ./...`. _(Req 8.1, 8.2, 8.4, 8.5, 8.6,
      9.1–9.6, 12.1, 14.5; D1, D7, D8)_

- [ ] 12.5 Write the failing REST CT read and route-index tests (8c, terms). Add
      `internal/web/rest_custom_terms_test.go` asserting for a registered CT: GET
      collection and single in the categories/tags shape (`id`, `count`, `link`, `name`,
      `slug`, `taxonomy`, `parent`; `count` the documented literal 0; `description` stays
      absent per Finding 12); a term of another taxonomy → 404; `link` equals
      `CustomTermPath` (nested for a hierarchical CT, one graph read per request, flat
      one-element slug); `parent` is `Term.ParentID`; a CT is NOT registered at `/tags`
      (the `restTermBase` default trap, `rest_terms.go:66-72`) and `category`/`post_tag`
      output is byte-identical to before; POST on the collection and
      PUT/PATCH/DELETE on the single → 501; the CT routes are gated on `s.termWrite !=
      nil` like `registerRESTTerms` (`rest_terms.go:50`); the new Server method
      `restRouteIndex()` adds `/wp/v2/{rest_base}` and `/wp/v2/{rest_base}/(?P<id>[\d]+)`
      with `GET` for each registered type and taxonomy, `GET /wp-json/` and `GET
      /wp-json/wp/v2/` list them, the package function `restRoutes()` output is
      byte-identical when no registry is wired, and `/wp/v2/types` and
      `/wp/v2/taxonomies` are NOT served (Req 10.6).
      Files: `internal/web/rest_custom_terms_test.go`
      Verify: `go test ./internal/web -run RESTCustomTerms` — fails. _(Req 6.6, 10.1,
      10.2, 10.3, 10.4, 10.5, 10.6, 12.1, 14.5; D1, D8)_

- [ ] 12.6 Implement REST CT registration and the route index. New
      `internal/web/rest_custom_terms.go` with `registerRESTCustomTerms` (own
      `"/"+RESTBase` path computation; GET → `handleRESTTermsCollection(tax.Name)` /
      `handleRESTTermSingle(tax.Name)`; write verbs → `restNotImplemented`). In
      `internal/web/rest_terms.go` add the registered-taxonomy arm to `restTermLink`
      (`:90`, before the tag fall-through) and generalize `restTermCategoryGraph`
      (`:129`) to `restTermGraph(r, taxonomy)` returning a hierarchy for `category` and
      hierarchical registered taxonomies; `restTermBase` is NOT generalized. In
      `internal/web/rest_router.go` add the `registerRESTCustomTerms(nr)` call, the
      `(s *Server) restRouteIndex()` method wrapping `restRoutes()` (`:150`), and have
      `handleRESTIndex`/`handleRESTNamespaceIndex` (`:175-193`) call it.
      Files: `internal/web/rest_custom_terms.go`, `internal/web/rest_terms.go`,
      `internal/web/rest_router.go`
      Verify: `go test ./internal/web -run RESTCustomTerms` green; `go test
      ./internal/web`; `go build ./...`; `go vet ./...`; `gofmt -l .` empty (phase
      gate). _(Req 6.6, 10.1–10.6, 12.1, 14.5; D1, D8)_

- [ ] 12.7 Extend the failing assembly test with the REST rows. In
      `cmd/grimoire/assembly_test.go`, reusing the 9.1 `book`/`shelf` fixture and
      registry, assert through the handler: `/wp-json/wp/v2/books` 200 with one item
      whose `type` is `book` and whose `link` ends `/books/dune/`, carrying the `shelf`
      term-id array keyed `shelves`; `/wp-json/wp/v2/shelves` 200 with one term; `GET
      /wp-json/wp/v2/` lists both routes; and, in the empty-registry case,
      `/wp-json/wp/v2/books` 404. Carry a table mapping each assertion to the wiring
      call it guards: the REST registration driven by `Server.WithRegistry` for the
      route rows and 404, `restMapper.WithRegistry` for the `type`, `link` and term
      array, so removing either call from `buildHandler` fails `go test`. RED is a
      behavioral failure (the mapper carries no registry yet), not a compile error.
      Files: `cmd/grimoire/assembly_test.go`
      Verify: `go test ./cmd/grimoire -run BuildHandler` — fails on the REST rows.
      _(Req 9.1, 9.3, 9.4, 10.1, 10.2, 13.3; D8, D10)_


- [ ] 12.8 Wire the API-side registry consumers inside `buildHandler`. In
      `cmd/grimoire/assembly.go` add `.WithRegistry(deps.Registry)` to the REST mapper
      chain (after `.WithPages(pageSvc)`) and wire `content.NewAdminService(...).
      WithAuthorTypes(authorReader, registeredTypeNames(deps.Registry))` where
      `authorReader` is `repos.AdminPosts.(domain.AuthorTypeReader)` checked with `, ok`
      and returned as an error; the type list is `{"post","page"}` plus every registered
      name. `main()` is untouched and `web.NewServer(` stays only in `assembly.go`.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire -run BuildHandler` green; `go test ./...`;
      `go build ./...`; `go vet ./...`; `gofmt -l .` empty (phase gate). _(Req 2.3,
      9.1, 9.3, 9.4, 13.3; D8, D9, D10)_



## Phase 13 — CLI diagnostic (`cmd/grimoire-cli`, TDD)

> Ships in PR 3.

- [ ] 13.1 Write the failing diagnostic tests first. Add
      `cmd/grimoire-cli/contenttypes_test.go` over fakes, as
      `TestReportDuplicateNicenamesNamesEachConflict` does for its auditor, asserting
      `reportUndeclaredContentTypes(ctx, w, postTypes, taxonomies, declared)`: names each
      undeclared non-reserved `post_type` with its row count and the exact key to add
      (`content.post_types`); names each undeclared non-reserved taxonomy with
      `content.taxonomies`; skips every name in `registry.ReservedPostTypes()` /
      `ReservedTaxonomies()` and every declared name; notes a declared type with no rows
      (Req 11.4); is silent when there is nothing to say; calls each auditor exactly
      once; absorbs a read error as a `Could not check …` line; NEVER returns an error
      or alters the exit status (Req 11.3); prints names and counts only (no title,
      slug or body is read). Also assert `reportPreflight` accepts the declared
      `config.ContentConfig` and `runMigrate` passes `cfg.Content`.
      Files: `cmd/grimoire-cli/contenttypes_test.go`
      Verify: `go test ./cmd/grimoire-cli -run UndeclaredContentTypes` — fails to compile
      (`reportUndeclaredContentTypes` undefined). _(Req 11.1, 11.2, 11.3, 11.4; D2, D9)_

- [ ] 13.2 Implement the report. New `cmd/grimoire-cli/contenttypes.go` with
      `reportUndeclaredContentTypes` (pure subtraction in a small function the test
      drives; read-only; uses `registry.ReservedPostTypes()`/`ReservedTaxonomies()`),
      mirroring `reportDuplicateNicenames` (`permalinks.go:167`). In
      `cmd/grimoire-cli/main.go` give `reportPreflight` (`:156`) the extra
      `declared config.ContentConfig` parameter, have `runMigrate` (`:111`) pass
      `cfg.Content` (config is already loaded, `:93`), and call the report from
      `reportPreflight` after `reportDuplicateNicenames`, constructing the auditors from
      the same `bunDB` (`wprepo.NewPostRepo(bunDB, prefix)`, `wprepo.NewTermRepo`).
      `internal/storage/migrate/preflight.go` (`RequiredSchema`, `Preflight`) is
      unchanged: it is a schema probe with no access to configuration or repositories.
      Files: `cmd/grimoire-cli/contenttypes.go`, `cmd/grimoire-cli/main.go`
      Verify: `go test ./cmd/grimoire-cli` green; `go build ./...`; `go vet ./...`.
      _(Req 11.1, 11.2, 11.3, 11.4; D2, D9)_


## Phase 14 — Real-DB fixture, compatibility doc, and final gate

> Ships in PR 3.

- [ ] 14.1 Add the real-WordPress-DB custom-type fixture test. New
      `internal/routing/realdb_custom_test.go` with `TestRealWordPressCustomTypes`,
      modeled on `realdb_pages_test.go` (reuse its DSN/prefix helpers): gated by
      `GRIMOIRE_TEST_WP_DSN` with the same prefix variable (NO new `GRIMOIRE_TEST_*`
      variable, per the design). Read-only, it DISCOVERS the distinct non-reserved
      `post_type` and `taxonomy` values from the restored database, declares the first
      CPT and CT found as `routing.TypeRoute` / `routing.TaxRoute` (package `routing`
      cannot import `registry`; hierarchy taken from the raw `post_parent` /
      `term_taxonomy.parent` census), derives each expected path by walking the raw rows
      INSIDE the test (not through `PageHierarchy`/`TermHierarchy`), builds the
      Structure via `WithCustomTypes`, and round-trips every derived path through
      `Classify` (Kind, `TypeName`, `Segments`). It skips with a precise precondition
      message naming the missing piece when the fixture has no custom post type or no
      custom taxonomy (the page analogue of `realWPNestedCensus`).
      Files: `internal/routing/realdb_custom_test.go`
      Verify: `go test ./internal/routing -run RealWordPressCustomTypes` — green, or
      skips cleanly with its precondition message when the fixture is absent; `go vet
      ./...` clean. _(Req 14.6, 14.7; D10)_

- [ ] 14.2 Record the compatibility-boundary expansion. Edit `docs/compatibility.md` to
      move custom post types and taxonomies from "out of scope" to "a scoped milestone
      (read-first, M12)", stating the config-only registry (D2), the READ-only scope
      with 501 on write verbs (D1), the documented consequences (a page or post slugged
      like a registered custom base/archive slug becomes unreachable at that URL, 7.4 /
      7.9; multi-segment rewrite slugs are rejected), and the `migrate -check`
      diagnostic. Documentation only; roadmap Requirement 25.3 requires it.
      Files: `docs/compatibility.md`
      Verify: the file renders; `gofmt -l .` empty; `go build ./...` unaffected.
      _(Roadmap Req 25.3; D1, D2, D6)_

- [ ] 14.3 Final gate — verification only. Confirm: NO file added under
      `internal/storage/migrations` (`git status --porcelain internal/storage/migrations`
      empty); the `postColumns` var (`internal/storage/wprepo/repo.go:17-24`) unchanged;
      NO new capability NAME (only `auth.CapabilitySuffix`); `ListPages` and every
      existing read (`RecentPosts`, `BySlug`/`PublishedByID` defaults, `adminTypes`
      default, `archiveWhere`) unchanged; a `Structure` without `WithCustomTypes`
      behaves identically (the pre-existing routing, classify, archive and
      `routes_regression_test.go` suites green and unedited); `grep -rn "web.NewServer("
      cmd/grimoire/*.go` shows only `assembly.go` plus the `main_test.go` throwaway;
      `routing` imports only `domain` (`go list -f '{{.Imports}}' ./internal/routing`);
      NO new dependency (`git diff go.mod go.sum` empty); and NO new `GRIMOIRE_TEST_*`
      variable beyond the existing ones. Run `gofmt -l .` (empty), `go vet ./...`,
      `go build ./...`, `go test ./...` and fix anything red.
      Files: (verification only) `internal/storage/migrations/`,
      `internal/storage/wprepo/repo.go`, `internal/routing/`, `cmd/grimoire/`, `go.mod`
      Verify: all four gate commands green; the grep and `git` invariants above hold.
      _(Req 2.4, 2.6, 7.3, 8.3, 13.2, 14.8; D9, D10)_

## Notes

- Each task cites the exact Requirement/AC and the Inherited decision (D1–D10) for
  traceability back to `requirements.md` and `design.md`. Phases map to design
  Components: 1→11, 2→1, 3→7, 4→3, 5→2, 6→4, 7→6a/6b, 8→8 (HTML), 9→9/11, 10→4/5,
  11→6c and the admin read, 12→8 (admin and REST) and 11, 13→10, 14→fixture and
  docs. PR 1 is Phase 1, PR 2 is Phases 2–9, PR 3 is Phases 10–14. The
  compatibility doc edit in 14.2 is design Finding 13.
- Checkpoints are the per-phase green gate: `gofmt -l .` empty and `go vet ./...`,
  `go build ./...`, `go test ./...` green after every phase (Req 14.8). No phase
  intentionally leaves the tree red at its end: each test→impl pair lands before the next
  phase begins, and RED states exist only between a test task and its immediately
  following implementation task.
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the sub-task
  that writes its failing test, in consecutive waves. 14.1 is the real-DB fixture, 14.2 the
  documentation edit and 14.3 the final gate.
- **Phase 1 first (the M15 lesson).** The assembly is extracted before any M12 feature so
  that `WithRegistry` and the registry-aware mapper are born inside a function whose test
  fails if a wiring call is removed. `serverDeps` gains its `Registry` field in 9.2 (PR 2); the registry-aware mapper and
  author types are wired in 12.8 (PR 3), guarded by the REST rows added in 12.7.
- **No property-based tests.** M12 is routing, startup validation and wiring logic over
  small, enumerable case spaces (archive / single / nested single / term / nested term /
  wrong ancestry / orphan / cycle / not routed / write verb → 501; rules R1–R8), the same
  rationale as the design and as M10.F/M11/M13/M14/M15. The classifier/constructor fixed
  point is checked by table over the finite product of structures × entries × slash forms.
  Coverage is example + contract + fixture + assembly tests — pure Go, SQLite always,
  MySQL/Postgres env-gated through the existing harness, no new env var, no new
  dependency.
- **No sub-task is optional** (none is postfixed with `*`). The config defaults and
  rejections, registry rules, routing precedence and fixed point, cross-vendor
  `PostNodeReader` contract, content service matrix, handler 200/301/404 with raw query
  preservation, REST read/501/route-index coverage, the assembly test, the CLI diagnostic,
  the real-DB fixture and the final gate are the milestone's done-definition (Req 14).

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2"] },
    { "id": 2, "tasks": ["2.1", "3.1", "4.1", "6.1"] },
    { "id": 3, "tasks": ["2.2", "3.2", "4.2", "6.2"] },
    { "id": 4, "tasks": ["4.3"] },
    { "id": 5, "tasks": ["4.4"] },
    { "id": 6, "tasks": ["4.5"] },
    { "id": 7, "tasks": ["4.6"] },
    { "id": 8, "tasks": ["4.7"] },
    { "id": 9, "tasks": ["4.8"] },
    { "id": 10, "tasks": ["5.1"] },
    { "id": 11, "tasks": ["5.2"] },
    { "id": 12, "tasks": ["5.3"] },
    { "id": 13, "tasks": ["5.4"] },
    { "id": 14, "tasks": ["7.1", "7.3"] },
    { "id": 15, "tasks": ["7.2", "7.4"] },
    { "id": 16, "tasks": ["8.1"] },
    { "id": 17, "tasks": ["8.2"] },
    { "id": 18, "tasks": ["8.3"] },
    { "id": 19, "tasks": ["8.4"] },
    { "id": 20, "tasks": ["9.1"] },
    { "id": 21, "tasks": ["9.2"] },
    { "id": 22, "tasks": ["9.3"] },
    { "id": 23, "tasks": ["10.1", "11.3", "12.1"] },
    { "id": 24, "tasks": ["10.2", "11.4", "12.2"] },
    { "id": 25, "tasks": ["11.1", "12.3", "13.1"] },
    { "id": 26, "tasks": ["11.2", "12.4", "13.2"] },
    { "id": 27, "tasks": ["12.5"] },
    { "id": 28, "tasks": ["12.6"] },
    { "id": 29, "tasks": ["12.7"] },
    { "id": 30, "tasks": ["12.8"] },
    { "id": 31, "tasks": ["14.1", "14.2"] },
    { "id": 32, "tasks": ["14.3"] }
  ]
}
```
