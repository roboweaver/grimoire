# Implementation Plan: M17 — Custom post type and taxonomy write (roadmap 12.C)

## Overview

> **This milestone enables create, update and delete for registered custom post
> types and custom taxonomies, over REST and the admin API.** M12 served them
> read-only and answers every write verb with `501`
> (`internal/web/rest_custom_posts.go:22-27`,
> `internal/web/rest_custom_terms.go:23-25`) and rejects any admin `type` other
> than `post`/`page` (`internal/web/adminapi_posts.go:102-104`). The write chain
> underneath is already generic by type or taxonomy, so the work is registration,
> capability derivation, hierarchy and slug guards, vocabulary checks and
> wiring. No migration, no new capability name, no new port method, no new
> dependency (Requirement 16).
>
> **Four pull requests, each a complete increment, each with its own
> `buildHandler` test.** M15 shipped once with its wiring missing from the real
> binary because its tests built their own `web.Server` (PR #74). So every PR
> below ends with an assembly edit in `cmd/grimoire/assembly.go` and a test that
> drives `buildHandler`, and the milestone ends with a built-binary smoke test.
>
> | PR | Phases | Increment |
> |---|---|---|
> | A | 1-6 | Custom taxonomy term writes: REST, admin API, hierarchy, slug/name/vocabulary rules |
> | B | 7-12 | Custom post writes: REST, admin API, derived capabilities, sanitization, slugs, delete cleanup, scheduled publish |
> | C | 13-18 | `post_parent` writes with cycle guard, nested links, term assignment to registered taxonomies |
> | D | 19-21 | End-to-end and built-binary tests, docs, roadmap ticks, final gate |
>
> PR A is independent. PR B follows the baseline (Phase 1) and may land before or
> after A. PR C needs B. PR D needs A, B and C. Within a PR the phases are
> ordered; the dependency graph at the end serializes any two tasks that write
> the same file.
>
> **Strict-TDD spine.** Every behavior-adding implementation task is preceded by
> the task that writes its failing test, and that test is run and seen to fail
> **for the stated reason** (route answers `501`, capability missing, `parent`
> ignored) before the implementation starts. A test that fails only because a
> new symbol does not compile counts as RED, and the implementation task makes
> it compile and pass. Two tasks are characterization or verification tasks
> with no RED step and say so: 1.1 (a golden snapshot of built-in behavior
> taken from unmodified code) and 19.1/19.2 (tests of behavior already wired by
> earlier phases, whose job is to catch a wiring omission).
>
> **Built-in behavior is pinned first.** Task 1.1 records the current responses
> for `post`, `page`, `category` and `post_tag` writes and reads from the
> unmodified tree, with and without a `content:` registry. It runs in every later
> phase's gate, so Requirement 15 is enforced continuously rather than
> asserted once at the end.
>
> **Standing gate convention.** After every phase: `gofmt -l .` empty, and
> `go vet ./...`, `go build ./...`, `go test ./...` green, following the
> close-out discipline of M10a, M11 and M13.
>
> **Fixtures.** Service tests build on the existing fakes
> (`fakeTermWriter`, `internal/content/writeservices_test.go:143`); handler and
> assembly tests use the SQLite helper `newAssemblySQLite` and the registry
> fixtures of `cmd/grimoire/assembly_test.go`. A registry with one flat type
> (`product`, `capability_type: product`), one hierarchical type (`guide`,
> `capability_type: guide`, `public: true`), one non-public type (`secret`,
> `public: false`), one hierarchical taxonomy (`genre`, attached to `product` and
> `guide`) and one flat taxonomy (`badge`) is declared once in a shared test
> helper (task 1.1) and reused by every later test.

## Tasks

## Phase 1 — Baseline: pin built-in behavior (`cmd/grimoire`, characterization)

- [x] 1.1 Record the built-in golden snapshot and the shared registry fixture
      **before any production change.** Add `cmd/grimoire/assembly_custom_fixture_test.go`
      with `customWriteRegistry(t)` (the five-entry registry described in the
      Overview, built through `registry.Build`), `newCustomWriteHandler(t, withRegistry bool)`
      (migrated SQLite through `newAssemblySQLite`, `buildHandler`, an admin user
      with `administrator`, an editor, an author, a contributor and a custom role
      holding only `edit_products`, session or Application-Password helpers
      matching `assembly_test.go`), and a `doJSON` request helper that attaches the
      CSRF header for session callers. Add
      `cmd/grimoire/assembly_builtin_golden_test.go` that, for each of
      `withRegistry` false and true, drives through `buildHandler`: REST and admin
      create, update, delete and read of a post and a page, REST create, update,
      delete and read of a category (with `parent`) and a tag, plus an admin
      `terms` list, and compares each status, relevant headers and normalized body
      against files under `cmd/grimoire/testdata/builtin_golden/`. Normalize only
      volatile values (timestamps, `modified`, `guid` host); never normalize keys.
      Generate the golden files **from the unmodified tree** (run once with a
      `-update` flag, review, commit) so they describe pre-M17 behavior. The
      with-registry run may differ from the without-registry run only where the
      route index and admin `terms` maps list registered entries; record that
      difference in the second golden set rather than sharing files.
      This is a characterization task: it passes on the unmodified tree, and its
      value is that it must keep passing.
      Files: `cmd/grimoire/assembly_custom_fixture_test.go`,
      `cmd/grimoire/assembly_builtin_golden_test.go`,
      `cmd/grimoire/testdata/builtin_golden/`
      Verify: `go test ./cmd/grimoire -run BuiltinGolden` green on a clean tree;
      `git stash` of any later production change makes no golden diff.
      _(Req 15.1, 15.2, 17.3; D16)_

## Phase 2 — Content: term service for registered taxonomies (`internal/content`, TDD) — PR A

- [x] 2.1 Write the failing service tests first. Add
      `internal/content/termcustom_test.go` over the existing `fakeTermWriter`,
      extending only its seeded data. Build the service with a registry holding
      hierarchical `genre` and flat `badge`. Assert, each also asserting the
      writer was **not** called: `Create` in `genre` with a valid same-taxonomy
      parent writes `ParentID` (Req 3.1); `Create`/`Update` in `badge` with a
      non-zero parent → `ErrTermParentInvalid` (3.2); `Update` of a `genre` term
      with parent = self and with a descendant → `ErrTermParentCycle` (3.3);
      parent absent from `genre` and parent that exists only in `category` →
      `ErrTermParentInvalid` (3.4); the hierarchy read uses the term's own
      taxonomy (3.5); `Create` in an unregistered, non-built-in taxonomy →
      `ErrTaxonomyUnknown` (4.6), while `category` and `post_tag` creates are
      accepted exactly as before (4.7); a name of `<script>x</script>` is written
      as plain text for a registered taxonomy, a name that sanitizes to empty →
      `ErrTermNameEmpty` (4.1, 4.2), and a `category` name is written untouched
      (4.7); slug outside `^[a-z0-9][a-z0-9_-]*$` → `ErrTermSlugInvalid` (4.3);
      duplicate slug among siblings → `ErrTermSlugTaken`, same slug under a
      different parent in `genre` accepted, duplicate in flat `badge` rejected
      regardless of parent (4.4); update that keeps its own slug accepted (4.5);
      caller without `manage_categories` → `ErrForbidden` before any read (the
      fake records zero reads). Also assert a nil registry and no policy option
      reproduce today's results for `category`/`post_tag` (15.3).
      Files: `internal/content/termcustom_test.go`
      Verify: `go test ./internal/content -run TermCustom` — fails (compile: the
      options and sentinels do not exist; once stubbed, `badge` parents are
      accepted and `genre` parents rejected). _(Req 3.1-3.5, 4.1-4.7, 15.3; D4,
      D6, D8, D9)_

- [x] 2.2 Implement the term service changes. In
      `internal/content/writeservices.go`: add the sentinels
      `ErrTermSlugInvalid`, `ErrTermSlugTaken`, `ErrTermNameEmpty`,
      `ErrTaxonomyUnknown` beside `ErrTermParentCycle`; add
      `type TermWriteOption func(*TermWriteService)` with `WithTermRegistry` and
      `WithTermContentPolicy`; make `NewTermWriteService` variadic over the
      options (existing call sites compile unchanged) and default the policy to
      `sanitize.New()`; add `hierarchical(taxonomy)` and replace the
      `t.Taxonomy != TaxonomyCategory` test in `validateParent` with
      `!s.hierarchical(t.Taxonomy)`; add the registered-only `cleanName` and
      `checkSlug` (one `ListByTaxonomy` read, sibling-set rule) and the
      `ErrTaxonomyUnknown` gate in `Create`, in the order the design fixes
      (capability, vocabulary, registered block, parent, write). Add the
      `registryTaxonomy(name)` helper that returns `ok` only for entries that are
      not `category`/`post_tag`. Built-in taxonomies must never enter
      `cleanName`/`checkSlug`.
      Files: `internal/content/writeservices.go`
      Verify: `go test ./internal/content` — 2.1 passes, the existing
      `termreparent_test.go` and `writeservices_test.go` still pass;
      `go test ./cmd/grimoire -run BuiltinGolden` green. _(Req 3, 4, 15.3; D4,
      D6, D8, D9)_

## Phase 3 — Web REST: custom taxonomy writes (`internal/web`, TDD) — PR A

- [x] 3.1 Write the failing REST tests first. Add
      `internal/web/rest_custom_terms_write_test.go` over a real chi router built
      with the registry fixture and fake term/post services, in the shape of
      `rest_terms_test.go` and `rest_custom_terms_test.go`. Assert: `POST
      /wp-json/wp/v2/genre` by a `manage_categories` holder → `201` with
      `taxonomy: "genre"`, `parent`, and a `link` equal to
      `Structure.CustomTermPath` (1.1); omitted slug derived by `termSlugify`
      (1.2); blank name → `400 rest_invalid_param` (1.3); no `manage_categories`
      → `403 rest_cannot_create` and no write (1.4); session without CSRF token
      → the existing term CSRF failure, Application Password skips it (1.5);
      `PUT`/`PATCH` partial update with omitted vs explicit `parent` 0 vs id
      (2.1); `DELETE` → `200 {deleted:true, previous}` and the term detached
      (2.2); unknown id and a term of another taxonomy → `404 rest_term_invalid_id`
      before the capability check (2.3); `403 rest_cannot_edit` (2.4); `POST
      /genre/{id}` and collection-level `PUT`/`PATCH`/`DELETE` still `501` (2.5);
      `ErrTermSlugTaken` → `400 term_exists`, `ErrTermSlugInvalid`,
      `ErrTermNameEmpty`, `ErrTaxonomyUnknown`, parent sentinels → `400
      rest_invalid_param` (3.2-3.4, 4.2-4.4, 4.6); `restRouteIndex` lists `POST` on
      each collection and `GET/PUT/PATCH/DELETE` on each single (2.6); nil
      `termWrite` or nil registry registers no write route (1.6).
      Files: `internal/web/rest_custom_terms_write_test.go`
      Verify: `go test ./internal/web -run RESTCustomTermWrite` — fails with `501`
      for every write verb. _(Req 1.1-1.6, 2.1-2.6, 3.2-3.4, 4.2-4.6; D1, D17)_

- [x] 3.2 Implement REST custom term writes. In
      `internal/web/rest_custom_terms.go` replace the `501` bindings for `POST
      /{base}` and `PUT`/`PATCH`/`DELETE /{base}/{id}` with
      `handleRESTTermCreate/Update/Delete(tax.Name)`; keep `501` for `POST
      /{base}/{id}` and collection-level `PUT`/`PATCH`/`DELETE`. In
      `internal/web/rest_terms.go` add the new sentinels to the create/update
      error switches (`ErrTermSlugTaken` → `400 term_exists`; the others → `400
      rest_invalid_param`). In `internal/web/rest_router.go` extend
      `restRouteIndex` with the write methods for registered taxonomies. Update the
      doc comment on `registerRESTCustomTerms`, which says "read-only".
      Files: `internal/web/rest_custom_terms.go`, `internal/web/rest_terms.go`,
      `internal/web/rest_router.go`
      Verify: `go test ./internal/web` — 3.1 passes and the M12 read tests
      (`rest_custom_terms_test.go`, `rest_router_test.go`) pass after updating
      only their stale "501 for writes" assertions to the new behavior, each edit
      noted in the commit message; `go test ./cmd/grimoire -run BuiltinGolden`
      green. _(Req 1, 2, 3.2-3.4, 4; D1, D17)_

## Phase 4 — Web admin: custom taxonomy writes (`internal/web`, TDD) — PR A

- [x] 4.1 Write the failing admin tests first. Add
      `internal/web/adminapi_terms_custom_test.go`: `POST /admin/api/terms` with
      `taxonomy: "genre"` → `201` with the stored taxonomy and parent applied
      (5.1); `PUT /admin/api/terms/{id}` on a `genre` term load-and-merges and
      echoes `taxonomy: "genre"` (5.2); `GET /admin/api/terms?taxonomy=genre`
      lists terms, `?taxonomy=nonsense` still `400 unknown taxonomy` (5.3);
      `taxonomy: "nonsense"` on create → `400`, and `category`/`post_tag` creates
      unchanged (4.6, 4.7); no `manage_categories` → `403`, no write (5.4);
      session without CSRF → existing failure, route gate order unchanged (5.5);
      slug-taken, invalid-slug, empty-name and parent sentinels → `400
      bad_request` (3, 4).
      Files: `internal/web/adminapi_terms_custom_test.go`
      Verify: `go test ./internal/web -run AdminTermCustom` — fails (`genre` list
      is `400 unknown taxonomy`; sentinels surface as `500`). _(Req 3, 4.2-4.6,
      5.1-5.5; D9)_

- [x] 4.2 Implement admin custom term writes. In `internal/web/adminapi_posts.go`
      turn `isKnownAdminTaxonomy` into a `(*Server)` method that also accepts
      `s.registry.Taxonomy(name)`, and update its caller in
      `internal/web/adminapi_terms.go`. In `adminTermCreate`/`adminTermUpdate`
      map `ErrTermSlugTaken`, `ErrTermSlugInvalid`, `ErrTermNameEmpty`,
      `ErrTaxonomyUnknown` to `badRequestError`, beside the existing parent
      sentinels. Do not change `adminKnownTaxonomies` (still the built-in pair,
      used by `resolvePostTerms` until Phase 16).
      Files: `internal/web/adminapi_terms.go`, `internal/web/adminapi_posts.go`
      Verify: `go test ./internal/web` — 4.1 passes, `adminapi_terms_test.go` and
      `adminapi_terms_parent_test.go` unchanged and green;
      `go test ./cmd/grimoire -run BuiltinGolden` green. _(Req 4, 5; D9)_

## Phase 5 — Assembly: wire terms and drive them through `buildHandler` (`cmd/grimoire`, TDD) — PR A

- [x] 5.1 Write the failing assembly test first. Add
      `cmd/grimoire/assembly_custom_write_test.go` with
      `TestBuildHandlerCustomTaxonomyWrites` using `newCustomWriteHandler(t, true)`:
      create a `genre` term over REST as an administrator (`201`), create a child
      under it, re-parent it, attempt a cycle (`400`), create a duplicate slug
      (`400 term_exists`), create in `badge` with a parent (`400`), delete the
      parent (`200`); repeat create/update through `/admin/api/terms`; confirm a
      user without `manage_categories` gets `403` on both surfaces; confirm the
      new term's public archive `/genre/{slug}/` serves (M12 route) and the nested
      path serves for the child. Add a comment table mapping each assertion to the
      `buildHandler` line that makes it reachable, as `assembly_test.go` does.
      Files: `cmd/grimoire/assembly_custom_write_test.go`
      Verify: `go test ./cmd/grimoire -run CustomTaxonomyWrites` — fails: the
      handler answers `501` (the service has no registry option set, so even with
      3.2/4.2 the cycle, slug and vocabulary checks are absent and a `badge` parent
      is accepted). _(Req 17.1, 17.3; D3, D16)_

- [x] 5.2 Wire the term options in `buildHandler`. In
      `cmd/grimoire/assembly.go` change
      `content.NewTermWriteService(termRW)` to pass
      `content.WithTermRegistry(deps.Registry)` and
      `content.WithTermContentPolicy(contentPolicy)`. No other edit.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 5.1 and the golden test pass; revert the
      two options locally and confirm 5.1 fails (proves the test guards the wiring).
      _(Req 17.1, 17.3; D3)_

- [x] 6. Checkpoint — PR A gate
  - Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`.
  - Confirm the PR A diff touches no file under `internal/storage` and no
    migration. Ensure all tests pass, ask the user if questions arise.

## Phase 7 — Auth: suffix-taking capability functions (`internal/auth`, TDD) — PR B

- [x] 7.1 Write the failing policy tests first. Add
      `internal/auth/policy_suffix_test.go`: table tests of
      `CanCreatePostSuffix`, `CanEditPostSuffix`, `CanDeletePostSuffix` for suffix
      `"products"` against principals built with `NewPrincipal` from each role in
      `roles.go` plus explicit `edit_products` / `publish_products` /
      `edit_others_products` / `edit_published_products` /
      `delete_*_products` grants, across statuses `draft`, `publish`, `private`
      and own vs other author, asserting the same decision matrix the existing
      `CanCreatePost`/`CanEditPost`/`CanDeletePost` tests assert for posts but
      driven by the `*_products` family (7.1); `edit_posts` alone is not enough
      (the Finding 2 case); and an equivalence test asserting
      `CanXPost(a, t, …) == CanXPostSuffix(a, capSuffix(t), …)` for `post`,
      `page` and `"shop_order"` across every role (15.3, 7.4).
      Files: `internal/auth/policy_suffix_test.go`
      Verify: `go test ./internal/auth -run Suffix` — fails (undefined symbols).
      _(Req 7.1, 7.4, 15.3; D2)_

- [x] 7.2 Implement the suffix variants. In `internal/auth/policy.go` move the
      three bodies into `CanCreatePostSuffix`, `CanEditPostSuffix`,
      `CanDeletePostSuffix` and make `CanCreatePost`, `CanEditPost`,
      `CanDeletePost` call them with `capSuffix(postType)`. No behavior change for
      existing callers.
      Files: `internal/auth/policy.go`
      Verify: `go test ./internal/auth ./internal/content ./internal/web` green;
      the existing `policy_test.go` is untouched and passes. _(Req 7.1, 15.3; D2)_

## Phase 8 — Content: `PostWriteService` for registered types (`internal/content`, TDD) — PR B

- [x] 8.1 Write the failing service tests first. Add
      `internal/content/postwrite_custom_test.go` with an in-memory fake that is a
      `domain.PostWriter` + `domain.PostNodeReader` + `domain.PostTermsWriter`
      (records calls and order). Service built with the registry, node reader and
      detacher options. Assert, each also asserting no writer call on rejection:
      capability matrix for `product` per verb and per status using the
      `*_products` family, `edit_posts` without `edit_products` → `ErrForbidden`
      on create, update and delete (7.1, 7.3); a registered type with
      `capability_type: post` decides like `post` (7.2); an unregistered stored
      type keeps the legacy suffix (7.4); sanitization: a non-`unfiltered_html`
      author's `<script>` in content/excerpt and tags in title are reduced (tier
      B), an editor's content is byte-identical (tier C), an empty-after-sanitize
      title → `ErrTitleEmpty`, a byte-identical resubmitted field is not
      re-sanitized (8.1-8.4); slug: derived from the sanitized title, invalid
      charset → `ErrSlugInvalid`, `-2`/`-3` on collision, own slug excluded on
      update, hierarchical type scopes by parent, flat type scopes by type (9.1-9.4);
      `post` and `page` slugs untouched (9.5); delete order is revisions, then
      `SetPostTerms(nil)` per attached taxonomy, then the row, a detach failure
      leaves the row (13.2, 13.3), `post`/`page` delete calls no detacher (13.5).
      Also a unit test for `Slugify` and a parity table asserting it equals the
      behavior of `termSlugify` (kept in `internal/web`, task 9.1).
      Files: `internal/content/postwrite_custom_test.go`
      Verify: `go test ./internal/content -run PostWriteCustom` — fails (compile,
      then `edit_posts` holders may write `product`). _(Req 7.1-7.4, 8.1-8.4,
      9.1-9.5, 13.2, 13.3, 13.5; D2, D4, D5, D11)_

- [x] 8.2 Implement the post service changes. In
      `internal/content/writeservices.go` add `WithPostRegistry`,
      `WithPostNodeReader`, `WithTermDetacher`, the sentinel `ErrSlugInvalid`,
      `registered(typ)`, `suffixFor(typ)`, and route `Create`/`Update`/`Delete`
      through `auth.Can*PostSuffix`. Add the registered-only `resolveSlug` and the
      delete detach step. New file `internal/content/slug.go` with exported
      `Slugify` (same algorithm as `termSlugify`) and the `slugRE`. In
      `internal/content/pagehier.go` add the unexported `slugTaken` helper. The
      parent-related code is **not** added here (Phase 13). Built-in types must
      never enter the new branches.
      Files: `internal/content/writeservices.go`, `internal/content/slug.go`,
      `internal/content/pagehier.go`
      Verify: `go test ./internal/content` — 8.1 passes, existing
      `writeservices_test.go` and `pagehier_test.go` unchanged and green;
      `go test ./cmd/grimoire -run BuiltinGolden` green. _(Req 7, 8, 9, 13.2-13.5;
      D2, D4, D5, D11)_

## Phase 9 — Web REST: custom post writes (`internal/web`, TDD) — PR B

- [x] 9.1 Write the failing REST tests first. Add
      `internal/web/rest_custom_posts_write_test.go` over a real router with the
      registry fixture, in the shape of `rest_posts_write_test.go` and
      `rest_custom_posts_test.go`. Assert: `POST /wp-json/wp/v2/product` by a
      holder of `edit_products` → `201` with `type: "product"` and the shape `GET`
      serves (6.1); `edit_posts` without `edit_products` → `403 rest_cannot_create`
      (7.7); `publish` status needs `publish_products` (7.1); `PUT`/`PATCH`
      partial update, `If-Unmodified-Since` conflict `409` (6.2); wrong-type id
      and unknown id → `404 rest_post_invalid_id` before the capability (6.3);
      invalid status, missing title, future date rules identical to `/posts` (6.4);
      collection-level `PUT`/`PATCH`/`DELETE` and `POST /{id}` still `501` (6.5);
      session without CSRF rejected, Application Password accepted (6.6); route
      index advertises the write methods (6.7); `DELETE` → `200 {deleted,
      previous}`, `?force` ignored, the row gone (13.1); non-`unfiltered_html`
      author's content sanitized through the REST path, editor's byte-identical
      (8.1, 8.2); slug collision yields `-2` (9.3), invalid slug `400
      rest_invalid_param` (9.2); `nil postWrite` keeps every verb `501`. A parity
      table asserts `termSlugify(x) == content.Slugify(x)`.
      Files: `internal/web/rest_custom_posts_write_test.go`
      Verify: `go test ./internal/web -run RESTCustomPostWrite` — fails with `501`.
      _(Req 6.1-6.7, 7.1, 7.7, 8.1-8.3, 9.2, 9.3, 13.1; D1, D2, D17)_

- [x] 9.2 Implement REST custom post writes. In
      `internal/web/rest_custom_posts.go` bind `handleRESTPostCreate(pt.Name)` to
      `POST /{base}` and `handleRESTPostUpdate`/`handleRESTPostDelete` to
      `PUT`/`PATCH`/`DELETE /{base}/{id}`, only when `s.postWrite != nil`; keep the
      remaining `501` bindings. In `internal/web/rest_posts.go` add
      `ErrSlugInvalid` to the create/update error switches (`400
      rest_invalid_param`). In `internal/web/rest_router.go` extend `restRouteIndex`
      for registered post types. Update the doc comments that say "read-only".
      Files: `internal/web/rest_custom_posts.go`, `internal/web/rest_posts.go`,
      `internal/web/rest_router.go`
      Verify: `go test ./internal/web` — 9.1 passes; `rest_custom_posts_test.go`
      and `rest_posts_write_test.go` green after updating only stale `501`
      assertions; golden green. _(Req 6, 7, 8, 9, 13.1; D1, D17)_

## Phase 10 — Web admin: custom post writes (`internal/web`, TDD) — PR B

- [x] 10.1 Write the failing admin tests first. Add
      `internal/web/adminapi_posts_custom_test.go`: `POST /admin/api/posts` with
      `type: "product"` → `201` writing `post_type = product` (10.1); `type:
      "nonsense"` → `400`, and with no registry the message is unchanged (10.2);
      `PUT` with a mismatched `type` keeps the stored type (10.6); `DELETE` →
      `204` (10.3); `edit_posts` + no `edit_products` → `403` (7.7); a user
      without `edit_posts` is stopped by the route gate (10.5); CSRF required
      (10.5); slug and sanitization behavior through the admin path (8, 9); the
      response `type` is the stored type. Built-in `post`/`page` requests are
      unchanged.
      Files: `internal/web/adminapi_posts_custom_test.go`
      Verify: `go test ./internal/web -run AdminPostCustom` — fails (`type must be
      "post" or "page"`). _(Req 7.7, 8, 9, 10.1-10.6; D13)_

- [x] 10.2 Implement admin custom post writes. In
      `internal/web/adminapi_posts.go` make the `parsePostWrite` type check accept
      registered types (message unchanged with no registry), map `ErrSlugInvalid`
      to `badRequestError` in create and update. Leave `adminKnownTaxonomies`,
      `resolvePostTerms` and the DTOs for Phases 14-16.
      Files: `internal/web/adminapi_posts.go`
      Verify: `go test ./internal/web` — 10.1 passes, `adminapi_posts_test.go`,
      `adminapi_type_test.go` green; golden green. _(Req 10; D13)_

## Phase 11 — Assembly: wire posts and the scheduler (`cmd/grimoire`, `internal/scheduler`, TDD) — PR B

- [x] 11.1 Write the failing wiring tests first. Extend
      `cmd/grimoire/assembly_custom_write_test.go` with
      `TestBuildHandlerCustomPostWrites`: through `buildHandler`, an editor holding
      `edit_products`/`publish_products` creates, updates and deletes a `product`
      over REST and the admin API; a plain editor without those capabilities gets
      `403` on both; a contributor's content is sanitized; a duplicate title gets
      the `-2` slug; the created row appears on its public URL when `public: true`
      and a non-public `secret` row answers `404` on every public route and is
      writable over REST/admin (14.1, 14.2); deleting a `product` with terms
      decrements `term_taxonomy.count` (13.3, seeded through SQL since term
      assignment lands in Phase 15). Add
      `internal/scheduler/scheduler_suffix_test.go`: a due `future` `product` row
      is published by a scheduler built with `WithCapabilitySuffixes("products")`
      through a real `PostWriteService`, and the same scheduler without the option
      logs and leaves it `future` (7.6). Add `TestBuildSchedulerPublishesCustomTypes`
      in `cmd/grimoire` calling `buildScheduler(deps, postWrite)` (17.2).
      Files: `cmd/grimoire/assembly_custom_write_test.go`,
      `internal/scheduler/scheduler_suffix_test.go`
      Verify: `go test ./cmd/grimoire ./internal/scheduler -run
      'CustomPostWrites|Suffix|BuildScheduler'` — fails (every custom write
      answers `403` or `501`: no registry on the service; `buildScheduler` and the
      option do not exist). _(Req 7.6, 13.3, 14.1, 14.2, 17.1-17.3; D3, D12, D14)_

- [x] 11.2 Wire the post options and the scheduler. In
      `internal/scheduler/scheduler.go` add `Option`, `WithCapabilitySuffixes` and
      the per-scheduler `principal` (copy of `systemPrincipal` plus the four
      capabilities per suffix); make `New` variadic. In
      `cmd/grimoire/assembly.go` pass `content.WithPostRegistry(deps.Registry)`,
      `content.WithPostNodeReader(repos.PostNodeReader)` and
      `content.WithTermDetacher(repos.PostTermsWriter)` to
      `NewPostWriteService`, add `registeredSuffixes(reg)` (via
      `auth.CapabilitySuffix`) and `buildScheduler`, and replace the inline
      `scheduler.New` at `cmd/grimoire/main.go:159` with it.
      Files: `internal/scheduler/scheduler.go`, `cmd/grimoire/assembly.go`,
      `cmd/grimoire/main.go`
      Verify: `go test ./cmd/grimoire ./internal/scheduler` — 11.1 passes; remove
      one option locally and confirm the matching 11.1 test fails; the existing
      `m7_scheduler_test.go` e2e still passes; golden green. _(Req 7.6, 13.3, 14,
      17.1, 17.2; D3, D14)_

- [x] 12. Checkpoint — PR B gate
  - Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`.
  - Confirm no file under `internal/storage` or any migration changed. Ensure all
    tests pass, ask the user if questions arise.

## Phase 13 — Content: hierarchical `post_parent` (`internal/content`, TDD) — PR C

- [x] 13.1 Write the failing hierarchy tests first. Add
      `internal/content/postparent_test.go` over the Phase 8 fake with a seeded
      `guide` graph (root, child, grandchild, a sibling) and a `product` row:
      `Create` and `Update` with a valid same-type parent write `ParentID` (11.1);
      non-zero parent on flat `product` → `ErrPostParentInvalid` (11.2); parent
      absent, and parent that is a `product` row while writing a `guide` →
      `ErrPostParentInvalid` (11.3); parent = self and parent = descendant →
      `ErrPostParentCycle` (11.4); `PageHierarchy.isAncestorOrSelf` terminates on a
      corrupt stored cycle; update with an unchanged parent is **not** validated
      even when the stored graph is inconsistent (11.6); an unauthorized caller
      gets `ErrForbidden` before `ErrPostParentCycle` (11.7); `post` and `page`
      updates never change `ParentID` (11.9); a nil node reader with a registered
      hierarchical type and a non-zero parent returns an explicit error rather
      than skipping validation.
      Files: `internal/content/postparent_test.go`
      Verify: `go test ./internal/content -run PostParent` — fails (compile, then
      the parent is ignored). _(Req 11.1-11.4, 11.6, 11.7, 11.9; D7)_

- [x] 13.2 Implement the parent guard. In `internal/content/writeservices.go` add
      `ErrPostParentInvalid`, `ErrPostParentCycle`, `validatePostParent` as
      designed, call it from `Create` and from `Update` only when
      `p.ParentID != cur.ParentID` and only for a registered type, and assign
      `cur.ParentID = p.ParentID` under the same condition. In
      `internal/content/pagehier.go` add `isAncestorOrSelf` with a visited set.
      `resolveSlug`'s `parentID` argument now receives the validated parent.
      Files: `internal/content/writeservices.go`, `internal/content/pagehier.go`
      Verify: `go test ./internal/content` — 13.1 and 8.1 pass; golden green.
      _(Req 11, 9.3; D7)_

## Phase 14 — Web: `parent` on REST and admin, nested links (`internal/web`, `internal/content`, TDD) — PR C

- [x] 14.1 Write the failing handler and mapper tests first. Add
      `internal/web/rest_custom_posts_parent_test.go`: REST create of a `guide`
      with `parent` → stored and echoed, omitted `parent` on `PATCH` unchanged,
      `parent: 0` makes top-level (11.1, 11.5); the response `link` is the nested
      path built from ancestry after create, update and delete's `previous`
      (11.8); a `parent` on a flat `product` → `400 rest_invalid_param` (11.2);
      cycle and absent-parent → `400` (11.3, 11.4); `parent` on `/posts` and
      `/pages` bodies stays ignored and the stored parent is untouched (11.9).
      Add `internal/web/adminapi_posts_parent_test.go`: admin create/update with
      `parent`, omitted on `PUT` keeps the stored parent, detail carries `parent`
      for registered types only (11.1, 11.5). Add
      `internal/content/rest_custom_parent_test.go`: `RESTMapper.CustomPost`
      serializes `parent` for a hierarchical type and omits it for a flat one.
      Files: `internal/web/rest_custom_posts_parent_test.go`,
      `internal/web/adminapi_posts_parent_test.go`,
      `internal/content/rest_custom_parent_test.go`
      Verify: `go test ./internal/web ./internal/content -run 'Parent'` — fails
      (parent ignored; link flat). _(Req 11.1-11.5, 11.8, 11.9; D7)_

- [x] 14.2 Implement parent handling and nested links. In
      `internal/web/rest_posts.go`: add `Parent *int64 json:"parent"` to
      `restPostWriteRequest`; apply it to the domain post only when the type is
      registered; map the two parent sentinels to `400 rest_invalid_param`; pass
      `s.restHierarchy(r, typ)` instead of `nil` in `writeRESTPostResult` and the
      delete `previous`. In `internal/web/adminapi_posts.go`: add `Parent *int64`
      to `postWriteRequest` and `Parent int64 json:"parent,omitempty"` to
      `postDetailResponse` (set for registered types only), thread it through
      `parsePostWrite` (omitted on update → `current.ParentID`), map the sentinels
      to `badRequestError`. In `internal/content/rest.go` add
      `RESTCustomPost.Parent *int64` set only when `pt.Hierarchical` and emit it in
      `MarshalJSON`.
      Files: `internal/web/rest_posts.go`, `internal/web/adminapi_posts.go`,
      `internal/content/rest.go`
      Verify: `go test ./internal/web ./internal/content` — 14.1 passes; M12 read
      tests updated only where a hierarchical custom item now carries `parent`;
      golden green. _(Req 11; D7)_

## Phase 15 — Content: term assignment to registered taxonomies (`internal/content`, TDD) — PR C

- [x] 15.1 Write the failing service tests first. Add
      `internal/content/postterms_custom_test.go`: `SetPostTerms` on a `product`
      row with an attached taxonomy and ids that belong to it writes (12.1);
      a taxonomy not attached to the type → `ErrTaxonomyNotAttached` (12.4); an id
      that is a term of another taxonomy or does not exist → `ErrTermInvalid`
      (12.3); empty ids clears the taxonomy; capability is the post's edit
      capability under the type's suffix, `manage_categories` is irrelevant (12.5);
      `CheckAssignable` before the post exists applies the same rules with a
      would-be draft authored by the actor and returns `ErrForbidden` before any
      read for an unauthorized caller; `post`/`page`/unregistered rows behave
      exactly as before, including assignment to a taxonomy that is attached to
      nothing (12.7).
      Files: `internal/content/postterms_custom_test.go`
      Verify: `go test ./internal/content -run PostTermsCustom` — fails (compile).
      _(Req 12.1-12.5, 12.7; D10)_

- [x] 15.2 Implement the assignment guards. In `internal/content/postterms.go`
      make `NewPostTermsWriteService` variadic over `PostTermsOption`
      (`WithPostTermsRegistry`, `WithPostTermsReader`), add `ErrTaxonomyNotAttached`
      and `ErrTermInvalid`, use `suffixFor`-equivalent logic (share one exported
      helper in `internal/content` rather than duplicating it), add
      `CheckAssignable`, and apply the registered-only rules in `SetPostTerms`.
      Files: `internal/content/postterms.go`
      Verify: `go test ./internal/content` — 15.1 passes, existing tests green;
      golden green. _(Req 12; D10)_

## Phase 16 — Web: term assignment on REST and admin (`internal/web`, TDD) — PR C

- [x] 16.1 Write the failing handler tests first. Add
      `internal/web/rest_custom_posts_terms_test.go`: REST create of a `product`
      with `"genre": [1,2]` assigns both and the response lists them under the
      `genre` key (12.1, 12.6); a non-array or non-positive value → `400
      rest_invalid_param` with nothing written (12.2); an id from another taxonomy
      → `400` **before** the post exists (no row created, 12.3); a key for a
      taxonomy not attached to the type is ignored as an unknown body field (it is
      not a recognized key); `PATCH` with `"genre": []` clears; a caller lacking
      the type's edit capability gets `403` before any term read (12.5); the same
      body on `/posts` is ignored (12.7). Add
      `internal/web/adminapi_posts_terms_test.go`: `termIds` with an attached
      taxonomy assigns, an unattached one appears in `partial` and the post write
      still succeeds, the detail `terms` map lists the type's attached taxonomies
      for a registered type and `category`/`post_tag` for built-ins unchanged
      (12.4, 12.6).
      Files: `internal/web/rest_custom_posts_terms_test.go`,
      `internal/web/adminapi_posts_terms_test.go`
      Verify: `go test ./internal/web -run 'CustomPostTerms|AdminPostTerms'` —
      fails (keys ignored, `terms` map built-in only). _(Req 12; D10)_

- [x] 16.2 Implement the handler changes. In `internal/web/rest_posts.go` read the
      body once into bytes, decode the typed request and, for registered types, a
      `map[string]json.RawMessage` for the `rest_base` keys of `pt.Taxonomies`
      (reverse lookup through `registry.TaxonomyRESTBase`); call
      `CheckAssignable` (through an optional-interface assertion on
      `s.postTermsWrite`) before `Create`/`Update` and `SetPostTerms` after, mapping
      a post-write term failure to `500` per the design. In
      `internal/web/adminapi_posts.go` make `resolvePostTerms` iterate
      `pt.Taxonomies` for registered types and report the new sentinels in
      `applyTermIDs`'s `partial` map.
      Files: `internal/web/rest_posts.go`, `internal/web/adminapi_posts.go`
      Verify: `go test ./internal/web` — 16.1 passes, `adminapi_posts_test.go`
      and `rest_posts_write_test.go` unchanged and green; golden green.
      _(Req 12; D10)_

## Phase 17 — Assembly: wire PR C and drive it through `buildHandler` (`cmd/grimoire`, TDD) — PR C

- [x] 17.1 Write the failing assembly tests first. Extend
      `cmd/grimoire/assembly_custom_write_test.go` with
      `TestBuildHandlerCustomHierarchyAndTerms`: create a `guide` tree through REST
      and admin with `parent`; the child serves at its nested public URL; a cycle
      is `400`; renaming the parent's slug moves the child's canonical path and
      the old nested path answers the M15 `301` (14.3); assign `genre` terms to a
      `product` over REST and admin and read `term_taxonomy.count`; assignment to
      an unattached taxonomy fails as specified; delete decrements the count
      (13.3); a contributor lacking the type capability is `403` throughout.
      Files: `cmd/grimoire/assembly_custom_write_test.go`
      Verify: `go test ./cmd/grimoire -run CustomHierarchyAndTerms` — fails at the
      term-assignment steps (the post-terms service has no registry or term reader
      set in `buildHandler`). _(Req 11, 12, 13.3, 14.3, 17.1, 17.3; D3, D7, D10)_

- [x] 17.2 Wire the post-terms options. In `cmd/grimoire/assembly.go` pass
      `content.WithPostTermsRegistry(deps.Registry)` and
      `content.WithPostTermsReader(repos.TermReader)` to
      `NewPostTermsWriteService`.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 17.1 passes; remove one option locally and
      confirm 17.1 fails; golden green. _(Req 12, 17.1; D3)_

- [x] 18. Checkpoint — PR C gate
  - Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`.
  - Confirm no storage port, migration or capability name was added
    (`git diff --stat main -- internal/storage internal/domain`). Ensure all tests
    pass, ask the user if questions arise.

## Phase 19 — End-to-end and built-binary verification (`cmd/grimoire`, `test/e2e`) — PR D

- [~] 19.1 Add the consolidated lifecycle test through `buildHandler`. In
      `cmd/grimoire/assembly_custom_lifecycle_test.go`, one table-driven test per
      acceptance-criteria group of Requirements 1-14, each row naming the
      requirement it covers, drives REST and admin side by side for create, read,
      update, delete, with sessions (CSRF) and Application Passwords, on both
      `public: true` and `public: false` types, and asserts the built-in golden
      files still match with the registry present. This task asserts behavior
      already wired by Phases 2-17; its purpose is to fail loudly if any
      requirement lacks an end-to-end path. Any row that fails is fixed in the
      phase that owns it, not here.
      Files: `cmd/grimoire/assembly_custom_lifecycle_test.go`
      Verify: `go test ./cmd/grimoire -run CustomLifecycle` green; the row table
      lists Requirements 1-14 with no gaps (a test asserts the set of covered
      requirement ids). _(Req 17.3, 15, 14, 1-13; D16)_

- [~] 19.2 Add the built-binary smoke test. In
      `test/e2e/m17_custom_write_binary_test.go`, `go build` `./cmd/grimoire` into
      `t.TempDir()`, write a config with a SQLite database and a `content:` section
      declaring `product`, `guide` and `genre`, run the binary on a free port with
      the migrated database and a seeded administrator, then over real HTTP: log
      in, create a `genre` term, create a `product` with that term, read it by REST
      and by its public URL, update it, delete it and the term, and assert each
      status. Skip under `-short` and when `go build` is unavailable. No database
      server, network or `GRIMOIRE_TEST_*` variable is needed. Also assert the
      process logs no scheduler error for a due custom `future` row.
      Files: `test/e2e/m17_custom_write_binary_test.go`
      Verify: `go test ./test/e2e -run M17Binary` green; run once with the 11.2
      `main.go` edit reverted and confirm the scheduler assertion fails.
      _(Req 17.2, 17.4; D14, D16)_

## Phase 20 — Documentation and roadmap (docs only) — PR D

- [~] 20.1 Update the documentation to match the shipped behavior.
      `docs/compatibility.md`: retitle the M12 section's "(read-first)" note and
      replace the "REST read, 501 write" bullet and the admin-write sentence in
      "Admin type validation" with the shipped behavior; add an "M17 — Custom type
      write" section covering: the write verbs and their `501` remainders, capability
      derivation from `capability_type` (and that taxonomy writes use
      `manage_categories`), sanitization tiers for registered types and the
      built-in term-name divergence, slug rules (post auto-suffix, term reject,
      charset), `parent` for hierarchical types with cycle prevention, term
      assignment keyed by `rest_base` (REST) and `termIds` (admin), permanent
      delete and the term cleanup for registered types only, `public: false`
      writability, the `edit_posts` floor on the admin API, and the scheduled
      publish behavior. `plans/wordpress-core-parity-roadmap/tasks.md`: tick task
      **12.C** with a shipped note in the style of 13.A, and tick the write
      portions of the M12 group. `plans/wordpress-core-parity-roadmap/requirements.md`:
      mark Requirements 23.4, 23 and 24 write portions shipped (M17) and amend the
      "write-parity half… remains open" note. `plans/post-m10-gap-analysis/README.md`:
      update the M12 entry (write shipped, remaining gaps from `design.md`
      Findings 3, 4, 5, 7) and the candidate list. `plans/README.md`: add the
      `17-custom-types-write` row and update the M12 row's "12.C separate" text.
      Search the repository (`grep -rn "501" docs README.md plans/12-custom-types-read`
      and "12.C") and fix every statement this milestone makes stale, leaving
      historical statements inside `plans/12-custom-types-read` untouched.
      Files: `docs/compatibility.md`,
      `plans/wordpress-core-parity-roadmap/tasks.md`,
      `plans/wordpress-core-parity-roadmap/requirements.md`,
      `plans/post-m10-gap-analysis/README.md`, `plans/README.md`
      Verify: `grep -n "12.C" plans/wordpress-core-parity-roadmap/tasks.md` shows
      the box ticked; `grep -rn "answer .501." docs/compatibility.md` finds no
      statement that custom writes return `501` except the documented remainders;
      `go build ./...` unaffected. _(Req 18.3)_

## Phase 21 — Final gate — PR D

- [~] 21.1 Final gate. Confirm: no new file under `internal/storage/migrations`
      (`git status --porcelain internal/storage/migrations` empty, Req 16.1); no
      change to any interface in `internal/domain/repository.go` (Req 16.3); no
      new capability string introduced anywhere
      (`git diff main | grep -E '^\+.*"(manage|edit|publish|delete)_[a-z_]+"'`
      reviewed: only suffix-derived names built at runtime); `go.mod`/`go.sum`
      unchanged (Req 16.2); no new `GRIMOIRE_TEST_*` variable (Req 16.4); the
      built-in golden files unchanged since task 1.1 (`git log --follow` shows a
      single commit). Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`,
      `go test ./...` and, once, `go test -race ./internal/content ./internal/web
      ./cmd/grimoire`. Fix anything red in the phase that owns it.
      Files: (verification only) `internal/storage/migrations/`,
      `internal/domain/repository.go`, `go.mod`, `go.sum`,
      `cmd/grimoire/testdata/builtin_golden/`
      Verify: all gate commands green; the four `git` checks above show no
      violation. _(Req 15, 16, 18.1)_

## Notes

- Tasks marked with `*` are optional; **no sub-task here is optional**. The
  capability matrices, the cycle and slug branches, the sanitization tiers, the
  assembly tests and the built-binary test are the milestone's done-definition
  (Requirement 18), matching M10.F, M11 and M13.
- **No property-based tests.** The guards are enumerable branches and fixed
  fixtures; the one wide string transform, `Slugify`, is pinned by a parity table
  against `termSlugify`. See `design.md` Testing strategy.
- **TDD exceptions are explicit:** 1.1 (characterization, passes on unmodified
  code), 19.1 and 19.2 (verify behavior wired in earlier phases).
- **Wiring is tested where it is wired.** Tasks 5.1/5.2, 11.1/11.2 and 17.1/17.2
  each pair a failing `buildHandler` test with the `assembly.go` edit, and each
  `Verify` line requires reverting the option once to prove the test guards it.
- **Stale-assertion edits are narrow.** Tasks 3.2 and 9.2 may change existing
  M12 tests only where they assert that a write verb answers `501` for a route
  that now serves it; the remaining `501` assertions stay.
- **Open sign-off items** are listed in `design.md` "Decisions awaiting
  sign-off". Defaults are implemented as written; reversing one is local to the
  phase named in each item.
- **No work item rewrites built-in behavior.** Where a built-in gap was found
  (page `parent` write, built-in delete cleanup, built-in term-name
  sanitization, edits to unregistered stored types) it is recorded in `design.md`
  Findings and left for a separate spec.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["2.1", "3.1", "4.1", "5.1"] },
    { "id": 2, "tasks": ["2.2"] },
    { "id": 3, "tasks": ["3.2", "4.2"] },
    { "id": 4, "tasks": ["5.2"] },
    { "id": 5, "tasks": ["7.1", "8.1", "9.1", "10.1", "11.1"] },
    { "id": 6, "tasks": ["7.2"] },
    { "id": 7, "tasks": ["8.2"] },
    { "id": 8, "tasks": ["9.2", "10.2"] },
    { "id": 9, "tasks": ["11.2"] },
    { "id": 10, "tasks": ["13.1", "14.1", "15.1", "16.1", "17.1"] },
    { "id": 11, "tasks": ["13.2"] },
    { "id": 12, "tasks": ["14.2", "15.2"] },
    { "id": 13, "tasks": ["16.2"] },
    { "id": 14, "tasks": ["17.2"] },
    { "id": 15, "tasks": ["19.1", "19.2"] },
    { "id": 16, "tasks": ["20.1"] },
    { "id": 17, "tasks": ["21.1"] }
  ]
}
```
