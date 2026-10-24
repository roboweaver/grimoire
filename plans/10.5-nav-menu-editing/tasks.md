# Implementation Plan: M10.F — Navigation Menu Editing

## Overview

> **This refines roadmap group 10.F only** — navigation menu editing, folded in
> from M4's deferral ("Menu editing deferred", no milestone attached). It is the
> **first write path** over grimoire's existing read-only navigation-menu
> surface: a `content.NavMenuService` (`internal/content/menus.go`) over the
> read-only `domain.NavMenuRepository` port
> (`internal/domain/repository.go:243`), implemented by `wprepo.NavMenuRepo`
> (`internal/storage/wprepo/menus.go`) and surfaced on the admin API as
> `GET /admin/api/menus` and `GET /admin/api/menus/{id}` inside the `edit_posts`
> group (`internal/web/adminroutes.go:84-90`). Roadmap group **10.G** (WordPress
> `$wp$` hash issuance) is explicitly **out of scope**. This milestone depends on
> M10a (roadmap group 10.A), whose `sanitize.Policy`
> (`internal/sanitize/policy.go`) it reuses without modification, and its test
> posture traces to roadmap **Requirement 19.2**.
>
> **No migration.** Every table the write path touches —
> `{prefix}terms`, `{prefix}term_taxonomy`, `{prefix}posts`, `{prefix}postmeta`,
> `{prefix}term_relationships` — already exists and is already read by
> `NavMenuRepo`; this milestone adds no file under `internal/storage/migrations`,
> and task 5.1 verifies that explicitly (`git status --porcelain
> internal/storage/migrations`), as M10a/M10.B/M10.C did (Req 6.4).
>
> **The nil-gated builder posture (D4).** The write surface follows the pattern
> M10.B/M10.C established: one new domain port (`domain.NavMenuWriter`), one new
> `content.NavMenuWriteService` holding the single shared `*sanitize.Policy`, six
> `wprepo` write methods on the existing `NavMenuRepo`, and one nil-gated web
> registration keyed on a new `s.navMenuWrite` field. A `Server` built without
> `WithNavMenuWrites` keeps serving the existing `GET` read routes unchanged and
> registers no write verbs rather than panicking (Req 3.3). The read service,
> read port, and read repo are untouched (Req 3.1, 9.4).
>
> **Sanitize at the write boundary (D5).** Sanitization lives in exactly one
> place — `NavMenuWriteService`. A menu `name` and an item `label` are reduced to
> plain text via `s.policy.Sanitize(sanitize.PostTitle, sanitize.For(principal),
> in)`; a `custom` item `url` is validated with `net/url` against a small scheme
> allow-list (`http`/`https`/`mailto`/`tel`/`ftp`, plus relative paths) rather
> than run through the Policy as markup. Every policy or validation error fails
> closed → `400`, persisting nothing (Req 4, 9.1, 9.3).
>
> **Capability gate (D1).** Every write is gated on `edit_theme_options`
> (administrator-only by default) via a new route group cloning the term-write
> group at `internal/web/adminroutes.go:119-126` (`manage_categories` +
> `csrfJSONMiddleware`), substituting `edit_theme_options`. The existing read
> routes keep their `edit_posts` gate (`adminroutes.go:84`) **UNCHANGED**
> (Req 7.4), so this milestone tightens writes above reads without touching the
> read gate.
>
> **Standing gate convention.** Keep `gofmt -l .` empty and `go vet ./...`,
> `go build ./...`, `go test ./...` green after every phase, following the
> close-out discipline of the previous M10 milestones (Req 10.3).
>
> **Strict-TDD spine.** Every behavior-adding sub-task is preceded by the
> sub-task that writes its failing test, matching how M5–M10.D were run and how
> 10.3 (`../10.3-rest-content-rendered`) is ordered. The one exception is the
> pure-interface/struct additions of Phase 1 (no behavior to test in isolation);
> their consumers' tests in Phases 2–4 exercise them, mirroring how 10.3 treats
> pure additions. The `var _ domain.NavMenuWriter = (*NavMenuRepo)(nil)`
> compile-time assertion lands with the `wprepo` implementation (Phase 2), not
> the port declaration.

## Tasks

## Phase 1 — Domain port + patch/input structs (`internal/domain`, pure additions)

- [x] 1.1 Add the `domain.NavMenuWriter` port and the patch/input structs.
      In `internal/domain/repository.go` declare a NEW `NavMenuWriter` interface
      **beside** the unchanged read-only `NavMenuRepository`
      (`internal/domain/repository.go:243`, left exactly as-is, Req 3.1), carrying
      the six methods with the exact signatures from design's "Components and
      Interfaces": `CreateMenu(ctx, name, slug string) (int64, error)`,
      `UpdateMenu(ctx, id int64, patch NavMenuPatch) error`,
      `DeleteMenu(ctx, id int64) error`,
      `CreateItem(ctx, menuID int64, item NavMenuItemInput) (int64, error)`,
      `UpdateItem(ctx, menuID, itemID int64, patch NavMenuItemPatch) error`,
      `DeleteItem(ctx, menuID, itemID int64) error`, and
      `ItemBelongsToMenu(ctx, menuID, itemID int64) (bool, error)`. In the file
      where `NavMenu`/`NavMenuItem` live (`internal/domain/entities.go`) add the
      pointer-field partial-update struct `NavMenuPatch{ Name, Slug *string }`,
      the fully-specified `NavMenuItemInput{ Label, URL, Type, Object string;
      ObjectID, ParentID int64; Order int }`, and the partial
      `NavMenuItemPatch{ Label, URL, Type, Object *string; ObjectID, ParentID
      *int64; Order *int }`, mirroring the `MediaMetaUpdate`/`UserProfileFields`
      pointer posture already in the tree. No behavior to test in isolation — the
      `var _ domain.NavMenuWriter = (*NavMenuRepo)(nil)` assertion and the
      consuming tests arrive in Phase 2+.
      Files: `internal/domain/repository.go`, `internal/domain/entities.go`
      Verify: `go build ./...` compiles; `gofmt -l .` empty; `go vet ./...` clean.
      _(Req 3.1; D3, D4)_

## Phase 2 — `wprepo` write methods (`internal/storage/wprepo`, TDD, SQLite)

- [x] 2.1 Write the failing storage tests first. Add
      `internal/storage/wprepo/menus_write_test.go` exercising the six
      `NavMenuWriter` methods against an in-memory SQLite DB via the existing
      wprepo test harness (the same `storagetest`/SQLite setup the current
      `menus_test.go` uses — pure Go, no new env var). Assert, per design's
      "Testing strategy": a full round-trip — `CreateMenu` then `CreateItem` for
      each type (`custom`, `post_type`, `taxonomy`) with `Order` and a nested
      child — read back through `NavMenuRepo`'s read path matches on order
      (`menu_order`), nesting (`_menu_item_menu_item_parent`), and the `custom`
      label/URL via `resolveMenuTarget`; a `post_type`/`taxonomy` item with a
      **dangling** `_menu_item_object_id` falls back to the stored label
      (Finding 2 tolerance); `UpdateMenu`/`UpdateItem` partial updates leave
      unsupplied fields unchanged; `DeleteMenu` leaves **no orphan** — zero rows
      in `{prefix}posts` (the items), `{prefix}postmeta` (their meta), and
      `{prefix}term_relationships` for the menu term, with a foreign referenced
      post/term untouched; `DeleteItem` tears down the one item's posts/postmeta/
      relationship only; a missing menu/item returns `domain.ErrNotFound`;
      `ItemBelongsToMenu` reports true only for an item linked to that menu, false
      for a cross-menu or nonexistent item; `CreateMenu` slug-uniqueness re-check
      inside the transaction yields a `-N` suffix on collision.
      Files: `internal/storage/wprepo/menus_write_test.go`
      Verify: `go test ./internal/storage/wprepo` — fails (the six methods absent).
      _(Req 1.1, 1.2, 1.4, 2.1, 2.2, 2.3, 2.4, 5.1, 5.2, 5.3, 6.1, 6.2, 6.3; D3)_

- [x] 2.2 Implement the six write methods + `ItemBelongsToMenu`. Add
      `internal/storage/wprepo/menus_write.go` implementing `domain.NavMenuWriter`
      on `*NavMenuRepo`, with a `var _ domain.NavMenuWriter =
      (*NavMenuRepo)(nil)` assertion beside the existing read assertion. Each
      method runs in `RunInTx` and reuses `insertReturningID`, `upsertPostMeta`
      (`internal/storage/wprepo/media.go:243`), `errNotFoundIfZero`, `vendorOf`,
      and `formatTS`. **CreateMenu**: two-row INSERT mirroring `TermRepo.Create` —
      `{prefix}terms` (`name`/`slug`) via `insertReturningID` returning `term_id`,
      then `{prefix}term_taxonomy` (`taxonomy="nav_menu"`, `description=""`,
      `parent=0`, `count=0`), with the in-transaction slug-uniqueness re-check
      (SELECT by slug under `taxonomy="nav_menu"`, append `-N`) to close the
      create/create race. **UpdateMenu**: EXISTS-guarded partial UPDATE of
      `{prefix}terms` `name`/`slug` for supplied fields only, scoped to a
      `nav_menu` term; no match → `domain.ErrNotFound` (EXISTS form, per the
      `TermRepo.Update` RowsAffected caveat). **DeleteMenu**: one-transaction
      orphan-free cascade in the design's order — collect item post IDs (JOIN
      `term_relationships`→`term_taxonomy` on `taxonomy="nav_menu"` AND
      `term_id=?`), DELETE their `postmeta`, DELETE their `posts`, DELETE the
      `term_relationships` for the menu's `term_taxonomy_id`, DELETE the
      `term_taxonomy` row, DELETE the `terms` row (`errNotFoundIfZero`); foreign
      referenced objects untouched. **CreateItem**: INSERT `{prefix}posts`
      (`post_title`=label, `post_type="nav_menu_item"`, `post_status="publish"`,
      `menu_order`=order, `post_author`, dates via `formatTS`) returning `ID`;
      INSERT the `{prefix}term_relationships` row (`object_id`=item ID,
      `term_taxonomy_id`=menu term's, `term_order=0`); `upsertPostMeta` for
      `_menu_item_type`, `_menu_item_object`, `_menu_item_object_id`,
      `_menu_item_menu_item_parent`, and — for `custom` — `_menu_item_url`; missing
      menu term → `domain.ErrNotFound`. **UpdateItem**: belongs-to-menu guard
      first, then partial UPDATE of `post_title`/`menu_order` (scoped to
      `post_type="nav_menu_item"`) and `upsertPostMeta` for supplied keys only.
      **DeleteItem**: belongs-to-menu guard, then DELETE the item's `postmeta`,
      its `term_relationships` row for the menu term, and its `posts` row.
      **ItemBelongsToMenu**: SELECT EXISTS over `{prefix}posts` JOIN
      `{prefix}term_relationships` JOIN `{prefix}term_taxonomy` WHERE
      `post_type="nav_menu_item"` AND `taxonomy="nav_menu"` AND `term_id=?` AND
      `"ID"=?` (PostgreSQL `"ID"` quoting as the read path uses).
      Files: `internal/storage/wprepo/menus_write.go`
      Verify: `go test ./internal/storage/wprepo` — the 2.1 tests pass; `go vet
      ./...` clean. _(Req 1.1, 1.2, 1.4, 2.1, 2.2, 2.3, 2.4, 3.2, 5.1, 5.2, 5.3,
      6.1, 6.2, 6.3; D3)_

## Phase 3 — `content.NavMenuWriteService` (`internal/content`, TDD)

- [x] 3.1 Write the failing service tests first. Add
      `internal/content/navmenuwrite_test.go` with a **fake `domain.NavMenuWriter`**
      (recording calls / returning canned IDs and `domain.ErrNotFound`) and a
      fake/real `domain.NavMenuRepository` reader (serving `MenuBySlug`/`MenuByID`
      for slug-uniqueness and read-back), pure Go with no SQLite. Assert:
      `CreateMenu`/item create route the `name`/`label` through
      `sanitize.PostTitle` so markup (`<script>…`, `<b>x</b>`) is reduced to plain
      text before it reaches the writer (Req 4.1, 4.2); `sanitize.For(principal)`
      is the writer arg when a principal is present and `sanitize.Anonymous()`
      only when none is (Req 4.5); a `custom` URL of `javascript:alert(1)` and
      `data:…` is rejected (no writer call), while `http(s)://…`, `mailto:`,
      `tel:`, `ftp:`, and a relative `/path` are accepted and persisted escaped
      (Req 4.3); an item `type` outside `{custom, post_type, taxonomy}` is rejected
      (Req 6.2, open Q3); a non-zero `ParentID` not belonging to the same menu is
      rejected via `ItemBelongsToMenu` with no writer mutation (Req 5.4), while
      `ParentID == 0` is accepted as top-level and skips the check (Req 5.3); slug
      resolution derives from `name` when omitted, normalizes an explicit slug,
      and appends `-2`/`-3` on collision (driving the reader's `MenuBySlug`
      `ID==0` miss); every policy/validation error returns before the writer and
      mutates nothing (fail closed, Req 9.1, 9.3); and `WithNavMenuContentPolicy`
      overrides the default while a service built with no option still holds a
      non-nil `sanitize.New()` Policy (Req 3.4).
      Files: `internal/content/navmenuwrite_test.go`
      Verify: `go test ./internal/content` — fails (`NavMenuWriteService` absent).
      _(Req 3.4, 4.1, 4.2, 4.3, 4.5, 4.6, 5.3, 5.4, 6.2, 9.1, 9.3; D1, D5)_

- [x] 3.2 Implement `NavMenuWriteService`. Add
      `internal/content/navmenuwrite.go` per design: the struct holding `w
      domain.NavMenuWriter`, `reader domain.NavMenuRepository`, and a non-nil
      `policy *sanitize.Policy`; the `NavMenuWriteOption` type and
      `WithNavMenuContentPolicy(p *sanitize.Policy) NavMenuWriteOption`; and
      `NewNavMenuWriteService(w, reader, opts...)` defaulting `policy` to
      `sanitize.New()` (never nil, Req 3.4). Add the content-layer inputs
      `MenuInput{ Name, Slug string }` and `MenuItemInput{ Label, URL, Type,
      Object string; ObjectID, ParentID int64; Order int }`. Implement the six
      methods (`CreateMenu`, `UpdateMenu`, `DeleteMenu`, `CreateItem`,
      `UpdateItem`, `DeleteItem`), each taking the resolved `auth.Principal`, plus
      the helpers: `vetItem` (sanitize `Label` via
      `s.policy.Sanitize(sanitize.PostTitle, sanitize.For(principal), …)`;
      validate `type` ∈ `{custom, post_type, taxonomy}`; for `custom` call
      `validateCustomURL`; validate a non-zero `ParentID` via
      `w.ItemBelongsToMenu`); `validateCustomURL` (`url.Parse` + empty-scheme
      relative accept + scheme allow-list `http`/`https`/`mailto`/`tel`/`ftp`,
      else `errInvalidMenuURL`); `sanitizeTitleSlug` (WordPress `sanitize_title`
      semantics — lowercase, `[\s_]+`→`-`, strip non-`[a-z0-9-]`, collapse `-{2,}`,
      trim `-`); and `resolveSlug` (derive-or-normalize with a stable `"menu"`
      fallback, then uniquify against `reader.MenuBySlug` with the `existing.ID ==
      0` miss check). Create/update methods call the writer then re-read via
      `s.reader.MenuByID` and return the `domain.NavMenu`; delete methods return no
      body. Every error path returns before the writer (fail closed). Add
      `errInvalidMenuURL` and any type-validation sentinel surfaced as a 400 by the
      handler.
      Files: `internal/content/navmenuwrite.go`
      Verify: `go test ./internal/content` — the 3.1 tests pass; `go vet ./...`
      clean. _(Req 1.1, 1.2, 2.1, 2.2, 3.4, 4.1, 4.2, 4.3, 4.5, 4.6, 5.1, 5.2,
      5.3, 5.4, 6.2, 9.1, 9.2, 9.3; D1, D5)_

## Phase 4 — Web layer (`internal/web`, TDD, nil-gated)

- [x] 4.1 Write the failing web tests first. Add
      `internal/web/adminapi_menus_write_test.go` and a nil-gated wiring test
      (`internal/web/navmenuwrite_wiring_test.go`, modeled on
      `mediawrite_wiring_test.go`/`userwrite_wiring_test.go`). Assert the
      **capability matrix** for each of the six write routes: an administrator
      (holds `edit_theme_options`) is allowed and an `edit_posts`-only
      non-administrator is forbidden (`403`) with no row written; the read routes
      (`GET /admin/api/menus`, `/menus/{id}`) still work for an `edit_posts`
      caller after the write group is wired (read gate unmoved, Req 7.4, 9.4).
      Assert **content-safety at the handler boundary**: a `name`/`label` with
      markup round-trips as plain text on a subsequent `GET`, and a
      `javascript:`/`data:` `custom` URL → `400` with no persistence. Assert
      **handler mapping**: a non-numeric `{id}`/`{itemId}` → `400`; an unknown
      `{id}`/`{itemId}` → `404`; create → `201` + `navMenuDTO`, update → `200` +
      `navMenuDTO`, delete → `204` with no body. Assert the **nil-gated wiring**:
      a `Server` built WITHOUT `WithNavMenuWrites` serves `GET` and returns
      `404`/`405` (not a panic) for the write verbs, while a `Server` built WITH it
      registers all six under the `edit_theme_options` + CSRF group.
      Files: `internal/web/adminapi_menus_write_test.go`,
      `internal/web/navmenuwrite_wiring_test.go`
      Verify: `go test ./internal/web` — fails (handlers, `WithNavMenuWrites`,
      and `s.navMenuWrite` absent). _(Req 1.1, 1.3, 1.4, 2.4, 3.3, 7.1, 7.4, 8.2,
      10.1, 10.2; D1, D2, D4)_

- [x] 4.2 Implement the handlers, the builder, and the nil-gated route group.
      Add the `navMenuWrite *content.NavMenuWriteService` field to `Server`
      (beside `mediaWrite`/`userWrite` in `internal/web/contentfeatures.go`) and
      the `WithNavMenuWrites(nmw *content.NavMenuWriteService) *Server` builder
      there, mirroring `WithMediaWrites`/`WithUserWrites`. Add
      `internal/web/adminapi_menus_write.go` with the six handlers —
      `adminMenuCreate`, `adminMenuUpdate`, `adminMenuDelete`,
      `adminMenuItemCreate`, `adminMenuItemUpdate`, `adminMenuItemDelete` — each:
      parse `{id}`/`{itemId}` (non-numeric → `badRequestError` → 400); decode the
      partial-update DTOs `menuCreateRequest`/`menuUpdateRequest`/
      `menuItemCreateRequest`/`menuItemUpdateRequest` (decode failure → 400);
      `PrincipalFrom(r.Context())` for the Policy writer; call `s.navMenuWrite`;
      on create/update re-read via `s.menus.Get(r.Context(), menuID)` and emit
      `navMenuDTO(menu)` (201 create, 200 update); on delete
      `w.WriteHeader(http.StatusNoContent)` with no body. Reuse
      `navMenuDTO`/`navMenuResponse` from `internal/web/adminapi_menus.go`
      verbatim — no new response type. Error mapping is the existing `jsonHandler`
      switch (`domain.ErrNotFound`→404, `content.ErrForbidden`→403,
      `badRequestError`→400). In `internal/web/adminroutes.go` add the nil-gated
      group — `if s.navMenuWrite != nil { r.Group(...) }` — cloning the term-write
      group (`adminroutes.go:119-126`) with `s.requireCapabilityJSON(
      "edit_theme_options")` + `s.csrfJSONMiddleware`, registering
      `POST /menus`, `PUT/DELETE /menus/{id}`, `POST /menus/{id}/items`,
      `PUT/DELETE /menus/{id}/items/{itemId}`. The read group at
      `adminroutes.go:84-90` is left UNCHANGED (Req 7.4).
      Files: `internal/web/adminapi_menus_write.go`,
      `internal/web/contentfeatures.go`, `internal/web/adminroutes.go`
      Verify: `go test ./internal/web` — the 4.1 tests pass; `go vet ./...` clean.
      _(Req 1.1, 1.2, 1.3, 1.4, 1.5, 2.1, 2.2, 2.3, 2.4, 2.5, 3.3, 5.4, 7.1, 7.2,
      7.3, 7.4, 8.1, 8.2, 8.3, 9.2; D1, D2, D4)_

## Phase 5 — main wiring + final gates (`cmd/grimoire`)

- [x] 5.1 Wire the write service and run the full gate suite. In
      `cmd/grimoire/main.go` construct `navMenuWrite :=
      content.NewNavMenuWriteService(repos.<NavMenuWriter>, repos.<NavMenuReader>,
      content.WithNavMenuContentPolicy(contentPolicy))` — injecting the
      process-wide `contentPolicy` as `mediaWrite`/`userWrite` do — and chain
      `.WithNavMenuWrites(navMenuWrite)` onto the `web.NewServer(...)` builder
      beside `.WithMediaWrites(mediaWrite).WithUserWrites(userWrite)`
      (`main.go:161`). Then verify the no-regression invariants and run the gate:
      confirm NO new file under `internal/storage/migrations`
      (`git status --porcelain internal/storage/migrations` shows none, Req 6.4);
      confirm the read service/port/repo are untouched — `content.NavMenuService`,
      `domain.NavMenuRepository`, and `wprepo.NavMenuRepo`'s read methods are
      unchanged (Req 9.4); confirm the content-safety and capability-matrix
      coverage from tasks 3.1/4.1 exists (Req 10.1, 10.2). Run `gofmt -l .`
      (empty), `go vet ./...`, `go build ./...`, `go test ./...` and fix anything
      red, confirming no test added by this milestone needs a database, network,
      live WordPress, or a new `GRIMOIRE_TEST_*` env var (pure Go, SQLite-only,
      Req 10.3).
      Files: `cmd/grimoire/main.go`; (verification only)
      `internal/storage/migrations/`, `internal/content/menus.go`,
      `internal/domain/repository.go`, `internal/storage/wprepo/menus.go`
      Verify: `go build ./...` and `go test ./cmd/...` green; `git status
      --porcelain internal/storage/migrations` shows no new files; all four gate
      commands green with no new `GRIMOIRE_TEST_*` env var. _(Req 6.4, 9.4, 10.1,
      10.2, 10.3; D4)_

## Notes

- Each task cites the exact Requirement/AC, the Inherited decision (D1–D5), and
  the design decision/Finding it satisfies, for traceability back to
  `requirements.md` and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt -l .` empty and `go vet
  ./...`, `go build ./...`, `go test ./...` green after every phase, matching
  M10a/M10.B/M10.C/M10.D's close-out discipline (Req 10.3).
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the
  sub-task that writes its failing test (2.1→2.2, 3.1→3.2, 4.1→4.2). Phase 1 is
  the sole pure-additions task (interface + structs, no isolated behavior); its
  consumers' tests in Phases 2–4 exercise it, and the `var _
  domain.NavMenuWriter` compile assertion lands with the Phase 2 implementation —
  mirroring how 10.3 treats pure additions.
- **No sub-task is optional** (none is postfixed with `*`). The capability-matrix
  and content-safety tests are the roadmap's M10 done-definition for this write
  surface (Req 10.1, 10.2, roadmap Req 19.2), and the storage round-trip/teardown
  and nil-gated wiring tests prove the fail-closed, orphan-free, and
  read-unchanged invariants — each is a core task, matching M10.B/M10.C, whose
  tests were likewise unmarked.
- **No property-based tests.** This milestone is admin-API CRUD over an existing
  storage mapping with no pure-function transformation whose correctness is best
  expressed as a universal property; the create/update/delete orchestration, the
  scheme allow-list, the slug `-N` collision rule, and the degradation/fail-closed
  branches are a fixed, example-enumerable table, not a property. Coverage is
  example + integration tests per the design's Testing strategy — pure Go,
  SQLite-only, no new env var or external dependency — matching the M10.B/M10.C
  write surfaces.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["2.1"] },
    { "id": 2, "tasks": ["2.2"] },
    { "id": 3, "tasks": ["3.1"] },
    { "id": 4, "tasks": ["3.2"] },
    { "id": 5, "tasks": ["4.1"] },
    { "id": 6, "tasks": ["4.2"] },
    { "id": 7, "tasks": ["5.1"] }
  ]
}
```
