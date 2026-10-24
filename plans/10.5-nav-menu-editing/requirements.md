# Requirements Document

M10.F — Navigation Menu Editing

## Introduction

M4 shipped navigation menus **read-only** and recorded "Menu editing deferred"
with no milestone attached. The read path exists and is stable: a
`content.NavMenuService` (`internal/content/menus.go`) over a
`domain.NavMenuRepository` read port (`internal/domain/repository.go:243`),
implemented by `wprepo.NavMenuRepo` (`internal/storage/wprepo/menus.go`), and
surfaced on the admin API as `GET /admin/api/menus` and
`GET /admin/api/menus/{id}` (`internal/web/adminroutes.go:89-90`), both inside
the `edit_posts` capability group. There is **no write handler anywhere** — the
service, the domain port, and the storage repo are all reads only.

This milestone (10.F) completes the M4 deferral: it is the **first write path**
for the existing read-only menu surface. It belongs in M10 rather than M9
because it is a write path, not a routing one — menu-item titles and custom URLs
are user-supplied content crossing a write boundary, so they must land **after**
10.A (M10a) and pass through the same sanitization Policy before persistence.
10.F adds write verbs to the existing admin-API menu surface in a **new route
group gated above the read path**, mirroring the term-write group that M-series
milestones already established:

| Route | Verb(s) | 10.F behavior |
|---|---|---|
| `/admin/api/menus` | `POST` | **Create** a menu (name, slug) |
| `/admin/api/menus/{id}` | `PUT`, `DELETE` | **Update** / **delete** a menu, with clean teardown |
| `/admin/api/menus/{id}/items` | `POST` | **Create** a menu item (custom, or `post_type`/`taxonomy` reference) |
| `/admin/api/menus/{id}/items/{itemId}` | `PUT`, `DELETE` | **Update** / **delete** a menu item (incl. reorder + nest) |
| `/admin/api/menus`, `/admin/api/menus/{id}` | `GET` | **Unchanged** — stays `edit_posts`-gated read |

Every write routes through M10a's existing `sanitize.Policy`
(`internal/sanitize/policy.go`) before any user-supplied label, menu name, or
custom URL is persisted, failing closed on any policy error. The write surface
follows the nil-gated builder pattern M10.B/M10.C established: a new
`domain.NavMenuWriter` port, a new `content.NavMenuWriteService` holding the one
shared `*sanitize.Policy`, `wprepo` write methods, and a nil-gated web
registration so a `Server` constructed without the writer keeps serving the
existing read routes unchanged. No database migration is required: every table
the write path touches (`{prefix}terms`, `{prefix}term_taxonomy`,
`{prefix}posts`, `{prefix}postmeta`, `{prefix}term_relationships`) already
exists and is already used by the read path.

This refines **roadmap group 10.F only** (navigation menu editing, folded in
from M4's deferral), tracing to the roadmap's 10.F entry in
`../wordpress-core-parity-roadmap/tasks.md`. It depends on M10a (roadmap group
10.A / Requirement 15,
`../wordpress-core-parity-roadmap/requirements.md:492`), whose Policy it reuses,
and its test posture traces to roadmap **Requirement 19.2**
(`../wordpress-core-parity-roadmap/requirements.md:602`). Roadmap group 10.G
(WordPress `$wp$` hash issuance) is out of scope (see "Out of scope").

## Glossary

- **The Policy** — M10a's `sanitize.Policy` (`internal/sanitize/policy.go`), the
  single sanitization component. `Policy.Sanitize(kind, writer, in)` returns the
  value that may be persisted at the tier `sanitize.TierFor` selects, and fails
  closed: on any non-nil error the caller MUST abandon the write. Its default
  constructor is `sanitize.New()`; the write service holds a non-nil instance and
  never a nil Policy.
- **Menu** — a WordPress navigation menu: a `nav_menu` **taxonomy term**,
  realized as one `{prefix}terms` row plus one `{prefix}term_taxonomy` row with
  `taxonomy = "nav_menu"` (the `term_id` is the menu ID; `name`/`slug` live on the
  term). Modeled by `domain.NavMenu` (`internal/domain/entities.go:196`:
  `ID`, `Name`, `Slug`, `Items`).
- **Menu item** — one entry in a menu's item tree: a `{prefix}posts` row with
  `post_type = "nav_menu_item"`, `post_title` holding the custom label, and
  `menu_order` holding the display order, linked to the menu term through a
  `{prefix}term_relationships` row (`object_id` = the item post ID →
  `term_taxonomy_id` of the `nav_menu` term). Its remaining fields live in
  `{prefix}postmeta`: `_menu_item_type` (`custom`/`post_type`/`taxonomy`),
  `_menu_item_object` (e.g. `page`/`category`/`custom`), `_menu_item_object_id`
  (the referenced object ID), `_menu_item_menu_item_parent` (the parent **item**
  ID, backing nesting), and `_menu_item_url` (a custom URL). Modeled by
  `domain.NavMenuItem` (`internal/domain/entities.go:204`:
  `ID`, `Label`, `URL`, `Type`, `Object`, `ObjectID`, `ParentID`, `Order`,
  `Children`).
- **NavMenu_Write_Service** — the capability-checked application service this
  milestone adds: a new `content.NavMenuWriteService` that owns the Policy calls,
  the capability check, and the create/update/delete orchestration over the new
  domain write port. It is the menu analogue of `content.MediaWriteService` /
  `content.UserService`'s write path, holding the one shared `*sanitize.Policy`
  (D4, D5).
- **NavMenuWriter** — the NEW `domain.NavMenuWriter` port this milestone adds
  alongside the existing read-only `domain.NavMenuRepository`
  (`internal/domain/repository.go:243`). It carries the create/update/delete
  methods for menus and items; the existing read port is unchanged (D4).
- **NavMenuRepo** — the existing `wprepo.NavMenuRepo`
  (`internal/storage/wprepo/menus.go`), today the read implementation. Its
  `resolveMenuTarget` (label/URL resolution per item type) and `buildMenuTree`
  (tree assembly from `_menu_item_menu_item_parent`, ordered by `menu_order`)
  define the shape the write side must mirror in reverse.
- **The edit_theme_options gate** — the WordPress menu-management capability
  `edit_theme_options` (`internal/auth/roles.go:119`), granted to
  `RoleAdministrator` **only** by default. 10.F gates every menu **write** on it
  (D1). The read routes keep their existing `edit_posts` gate
  (`internal/web/adminroutes.go:84`) unchanged.
- **The menu writes** — the create/update/delete verbs this milestone enables on
  menus and menu items (including reorder and nest), the only new write surface.

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | **Capability gate: writes require `edit_theme_options`.** Every menu write (create/update/delete of menus and items, reorder, nest) is gated on `edit_theme_options` (administrator-only by default), matching WordPress's own menu-management capability. The existing READ routes keep their `edit_posts` gate UNCHANGED. | This milestone deliberately tightens writes above reads — menu structure is theme-options-class config, not post content — and resolves the roadmap's flagged open decision. The write route group uses `s.requireCapabilityJSON("edit_theme_options")`; the read group at `internal/web/adminroutes.go:84` is untouched (Requirement 7). |
| D2 | **Surface: admin-API only.** 10.F extends the existing admin-API menu surface (`/admin/api/menus*`) with write verbs in a new `edit_theme_options`-gated + CSRF route group, mirroring the term-write group. It does NOT add `wp-json wp/v2/menus` or `wp/v2/menu-items` REST routes. | The wp-json menu REST surface is out of scope. Auth is session + CSRF + context principal, consistent with the read path and the admin write groups (`adminapi_posts`, `adminapi_media`) (Requirement 8). |
| D3 | **CRUD shape.** Menu create/update/delete (name, slug) and menu-item create/update/delete, with reordering via `menu_order` and nesting via the parent item ID (`domain.NavMenuItem.Order`/`.ParentID`). Item type supports at least `custom` (label + URL — the user-content case) and `post_type`/`taxonomy` (reference by object ID). Deleting a menu removes the menu term, its item posts, their postmeta, and the term_relationships (clean teardown). | No migration — every target table already exists and is already used by the read path (Requirements 1–3, 6). |
| D4 | **Pattern: new domain write port + content write service + storage writer + nil-gated builder**, mirroring M10.B/M10.C/M10a. A `domain.NavMenuWriter` port, a `content.NavMenuWriteService` holding the shared `*sanitize.Policy` (default `sanitize.New()`, never nil, fail closed), `wprepo` write methods, and a nil-gated web builder/registration. | A `Server` built without the write service keeps the read routes and omits the write verbs; the read port, service, and repo are untouched (Requirements 3, 7). |
| D5 | **Sanitize at the write boundary.** A menu-item label is sanitized as plain text (the `sanitize.PostTitle` kind, reduced to plain text like a title); a custom item URL is validated/escaped as a URL (not markup) before persistence; a menu name is likewise plain text. Fail closed on a policy error → `400`. | The design pins exact field-kind mappings per field. No user-supplied label, name, or URL is persisted un-vetted (Requirements 4, 9). |

## Requirements

### Requirement 1: Create, update, and delete a menu

**User Story:** As a site administrator, I want to create, rename, and delete
navigation menus through the admin API, so that I can manage the menus the
existing read path already exposes.

#### Acceptance Criteria

1. WHEN a caller holding `edit_theme_options` sends `POST /admin/api/menus` with a
   `name`, THE NavMenu_Write_Service SHALL create one `nav_menu` taxonomy term
   (a `{prefix}terms` row plus a `{prefix}term_taxonomy` row with
   `taxonomy = "nav_menu"`) and SHALL respond with the created menu in the same
   JSON shape the read path returns (`navMenuDTO`/`navMenuResponse` from
   `internal/web/adminapi_menus.go`).
2. WHEN a caller holding `edit_theme_options` sends `PUT /admin/api/menus/{id}`
   with a `name` and/or `slug`, THE NavMenu_Write_Service SHALL update the named
   fields on the menu's existing `{prefix}terms`/`{prefix}term_taxonomy` rows and
   SHALL leave any field not supplied unchanged (partial update).
3. WHEN a caller holding `edit_theme_options` sends `DELETE /admin/api/menus/{id}`,
   THE NavMenu_Write_Service SHALL delete the menu term and perform the clean
   teardown of Requirement 6, and SHALL respond with a success status carrying no
   menu body.
4. IF the `{id}` path segment does not resolve to an existing `nav_menu` term,
   THEN THE admin menu endpoint SHALL respond `404` (mapping
   `domain.ErrNotFound`, the sentinel the read path's `Get` already returns for an
   unknown ID) and SHALL NOT persist any change.
5. WHEN a menu create or update succeeds, THE subsequent
   `GET /admin/api/menus/{id}` SHALL return the sanitized stored name and slug,
   so the read path reflects exactly what the write path persisted.

### Requirement 2: Create, update, and delete a menu item

**User Story:** As a site administrator, I want to add, edit, and remove items
within a menu, so that I can compose the menu structure the read path renders.

#### Acceptance Criteria

1. WHEN a caller holding `edit_theme_options` sends
   `POST /admin/api/menus/{id}/items` with a valid item payload, THE
   NavMenu_Write_Service SHALL create one `{prefix}posts` row with
   `post_type = "nav_menu_item"`, write the item's `_menu_item_*` postmeta, and
   link the item to the menu term through a `{prefix}term_relationships` row
   (`object_id` = the new item post ID → the `nav_menu` term's
   `term_taxonomy_id`), per Requirement 6.
2. WHEN a caller holding `edit_theme_options` sends
   `PUT /admin/api/menus/{id}/items/{itemId}` with any subset of the item's
   editable fields, THE NavMenu_Write_Service SHALL update exactly the supplied
   fields on the item's `post_title`/`menu_order`/`_menu_item_*` storage and
   SHALL leave any field not supplied unchanged (partial update).
3. WHEN a caller holding `edit_theme_options` sends
   `DELETE /admin/api/menus/{id}/items/{itemId}`, THE NavMenu_Write_Service SHALL
   remove the item's `{prefix}posts` row, its `_menu_item_*` `{prefix}postmeta`
   rows, and its `{prefix}term_relationships` row for the menu term (per
   Requirement 6).
4. IF the `{itemId}` does not resolve to a `nav_menu_item` post linked to the
   `{id}` menu term, THEN THE admin menu endpoint SHALL respond `404` and SHALL
   NOT persist any change.
5. WHEN an item create or update succeeds, THE subsequent
   `GET /admin/api/menus/{id}` SHALL return the item with the sanitized label and
   URL and the stored type/object/order/parent, resolved through the existing
   `resolveMenuTarget`/`buildMenuTree` read path.

### Requirement 3: New NavMenuWriter domain port and nil-gated wiring

**User Story:** As a maintainer, I want menu writes to flow through a dedicated
domain port behind a nil-gated builder, so that the storage layer owns the write
and a server without the write service keeps serving the read routes.

#### Acceptance Criteria

1. THE project SHALL add a NEW `domain.NavMenuWriter` port carrying the menu and
   menu-item create/update/delete methods, and SHALL leave the existing read-only
   `domain.NavMenuRepository` (`internal/domain/repository.go:243`) unchanged.
2. THE `wprepo` package SHALL implement `domain.NavMenuWriter` with methods that
   mirror, in reverse, the read repo's `resolveMenuTarget`/`buildMenuTree`
   mapping, writing to `{prefix}terms`, `{prefix}term_taxonomy`, `{prefix}posts`,
   `{prefix}postmeta`, and `{prefix}term_relationships`.
3. THE `content.NavMenuWriteService` SHALL be registered into the web layer by a
   nil-gated builder method (the menu analogue of the `s.termWrite != nil` guard
   at `internal/web/adminroutes.go:119`), so a `Server` constructed WITHOUT the
   write service SHALL keep serving the existing `GET /admin/api/menus` and
   `GET /admin/api/menus/{id}` read routes and SHALL NOT register the write verbs,
   rather than panicking.
4. THE `content.NavMenuWriteService` SHALL hold a non-nil `*sanitize.Policy`
   (defaulting to `sanitize.New()` when none is injected) and SHALL fail closed
   if a Policy call returns an error (Requirement 4), so no construction path
   leaves the write service with a nil Policy.

### Requirement 4: Sanitize-policy field-kind mapping at the write boundary

**User Story:** As a security reviewer, I want every user-supplied menu field
sanitized before persistence, so that menu writes carry no weaker a safety
guarantee than post, comment, media, and user writes.

#### Acceptance Criteria

1. WHEN a create or update supplies a menu-item `label`, THE NavMenu_Write_Service
   SHALL call the Policy on that value with field kind `sanitize.PostTitle`
   (plain-text reduction, no surviving markup) before the value reaches
   persistence as the item's `post_title`.
2. WHEN a create or update supplies a menu `name`, THE NavMenu_Write_Service SHALL
   call the Policy on that value with field kind `sanitize.PostTitle` (plain-text
   reduction) before the value reaches persistence as the term `name`.
3. WHEN a create or update supplies a `custom`-type item's `url`, THE
   NavMenu_Write_Service SHALL validate and escape that value as a URL (not as
   markup) before persisting it to `_menu_item_url`, rejecting a value that is not
   an acceptable URL rather than storing un-vetted markup.
4. WHERE the acting caller holds `unfiltered_html`, THE Policy's tier selection
   SHALL behave for the menu fields exactly as it does elsewhere, because the
   Policy reads `unfiltered_html` through `sanitize.For` and takes no
   menu-specific input; the plain-text `sanitize.PostTitle` reduction applies to
   the label and name regardless of tier, matching the title path in M10.B/M10.C.
5. THE NavMenu_Write_Service SHALL build the Policy's writer argument from the
   resolved principal via `sanitize.For(principal)`, and SHALL use
   `sanitize.Anonymous()` only when no principal is present.
6. IF the Policy returns a non-nil error for any field, OR a `custom` URL fails
   validation, THEN THE admin menu endpoint SHALL respond `400` and SHALL NOT
   persist any value for the request (fail closed), matching M10a's/M10.B's
   sentinel-to-`400` mapping.

### Requirement 5: Reordering and nesting

**User Story:** As a site administrator, I want to reorder items within a menu and
nest items under one another, so that the menu renders in the structure I define.

#### Acceptance Criteria

1. WHEN a create or update supplies an item's order position, THE
   NavMenu_Write_Service SHALL persist it as the item's `menu_order`, which the
   read path already models as `domain.NavMenuItem.Order` and uses to order the
   tree in `buildMenuTree`.
2. WHEN a create or update supplies an item's parent item ID, THE
   NavMenu_Write_Service SHALL persist it as the item's
   `_menu_item_menu_item_parent` postmeta, which the read path already models as
   `domain.NavMenuItem.ParentID` and uses to build nesting in `buildMenuTree`.
3. WHEN a parent item ID of `0` is supplied, THE NavMenu_Write_Service SHALL treat
   the item as top-level (no parent), matching the read path's `ParentID == 0`
   convention.
4. IF a supplied parent item ID does not resolve to another `nav_menu_item` post
   linked to the same menu term, THEN THE admin menu endpoint SHALL respond `400`
   and SHALL NOT persist the item, so an item cannot be nested under a
   non-existent or cross-menu parent.

### Requirement 6: Storage write maintains the full item model and clean teardown

**User Story:** As a maintainer, I want the storage writer to maintain every table
the read path reads from, so that a written menu round-trips through the read path
and a deleted menu leaves no orphaned rows.

#### Acceptance Criteria

1. WHEN an item is created, THE storage writer SHALL write, consistently for the
   item's declared type: `post_title` (the sanitized label) and `menu_order` on
   the `{prefix}posts` row; `_menu_item_type` (`custom`/`post_type`/`taxonomy`),
   `_menu_item_object`, `_menu_item_object_id`,
   `_menu_item_menu_item_parent`, and (for `custom`) `_menu_item_url` in
   `{prefix}postmeta`; and a `{prefix}term_relationships` row linking the item
   post to the menu term, so the read path's `resolveMenuTarget` reconstructs the
   same label and URL.
2. WHEN an item of type `custom` is written, THE storage writer SHALL persist its
   label (`post_title`) and validated URL (`_menu_item_url`); WHEN an item of type
   `post_type` or `taxonomy` is written, THE storage writer SHALL persist its
   `_menu_item_object` and `_menu_item_object_id` reference so the read path
   derives the URL from the referenced object.
3. WHEN a menu is deleted, THE storage writer SHALL remove the menu's
   `{prefix}terms` and `{prefix}term_taxonomy` rows, every linked item's
   `{prefix}posts` row, those items' `{prefix}postmeta` rows, and the
   `{prefix}term_relationships` rows for the menu term, leaving no orphaned item
   post, postmeta, or relationship row.
4. THE milestone SHALL require NO database migration, because every target table
   (`{prefix}terms`, `{prefix}term_taxonomy`, `{prefix}posts`, `{prefix}postmeta`,
   `{prefix}term_relationships`) already exists and is already used by the read
   path; the task list SHALL verify that no file is added under
   `internal/storage/migrations`, as M10a's/M10.B's/M10.C's task lists did.

### Requirement 7: Capability gate with the read gate unchanged

**User Story:** As a site administrator, I want menu editing restricted to the
WordPress menu-management capability while menu reading keeps its existing gate,
so that authorization behaves as WordPress does and no read regresses.

#### Acceptance Criteria

1. WHEN a caller lacking `edit_theme_options` sends any of The menu writes, THE
   admin menu endpoint SHALL respond `403` and SHALL NOT create, modify, or delete
   any menu or item.
2. WHEN a caller holding `edit_theme_options` (administrator by default) sends a
   menu write, THE NavMenu_Write_Service SHALL permit it, subject to the remaining
   requirements.
3. THE menu write routes SHALL be registered in a new route group that applies
   `s.requireCapabilityJSON("edit_theme_options")` and the CSRF middleware,
   mirroring the term-write group at `internal/web/adminroutes.go:119-126`
   (`manage_categories` + `csrfJSONMiddleware`).
4. THE existing `GET /admin/api/menus` and `GET /admin/api/menus/{id}` routes
   SHALL remain in their current `edit_posts`-gated group
   (`internal/web/adminroutes.go:84-90`) UNCHANGED, so this milestone tightens
   writes above reads without altering the read gate.

### Requirement 8: Authentication consistent with the admin write surfaces

**User Story:** As a site administrator, I want menu writes to authenticate exactly
like the other admin-API writes, so that one admin session integration covers every
admin write surface.

#### Acceptance Criteria

1. THE menu write routes SHALL authenticate via the admin session + context
   principal, consistent with the read path and the `adminapi_posts` /
   `adminapi_media` write handlers, and SHALL NOT add a `wp-json` route.
2. WHEN a session-authenticated caller sends a menu write, THE admin menu endpoint
   SHALL enforce the CSRF synchronizer token via `s.csrfJSONMiddleware` (the same
   middleware the term-write group uses) and SHALL reject a missing or invalid
   token without persisting any change.
3. THE menu write routes SHALL perform no per-handler Policy call beyond what the
   NavMenu_Write_Service performs, so sanitization lives in exactly one place on
   the write path.

### Requirement 9: Correctness and safety invariants

**User Story:** As a security reviewer, I want the menu write path to carry the
same end-to-end guarantees the earlier M10 write surfaces established, so that the
new surface cannot become a sanitization or authorization bypass.

#### Acceptance Criteria

1. THE NavMenu_Write_Service SHALL sanitize every supplied menu name and item
   label at the writer's tier, and validate every supplied `custom` URL, before
   those values reach persistence; no create or update SHALL persist an
   un-vetted caller-supplied label, name, or URL.
2. THE NavMenu_Write_Service SHALL evaluate the `edit_theme_options` capability
   decision against the resolved principal, never a request-body field.
3. IF the Policy returns an error OR the capability check fails OR a referenced
   parent or object is invalid, THEN THE NavMenu_Write_Service SHALL perform no
   write and the stored state SHALL be unchanged (fail closed).
4. THE existing read service, read port, and read repo (`content.NavMenuService`,
   `domain.NavMenuRepository`, `wprepo.NavMenuRepo`) SHALL continue to return the
   same menus and items for unchanged data, so enabling writes does not change
   what the read path returns.

### Requirement 10: Capability-matrix and content-safety test coverage

**User Story:** As a maintainer, I want every new menu write endpoint proven by an
authorization matrix and a sanitization test before it is considered done, so that
the roadmap's M10 done-definition is met.

#### Acceptance Criteria

1. THE milestone SHALL cover each of The menu writes with a capability-matrix test
   exercising allowed and forbidden callers on `edit_theme_options` — including an
   administrator (allowed) and a non-administrator holding only `edit_posts`
   (forbidden → `403`) — satisfying roadmap Req 19.2.
2. THE milestone SHALL cover The menu writes with a content-safety test proving an
   item label (and menu name) containing markup is sanitized to plain text before
   persistence, and a `custom` item URL is validated/escaped before persistence,
   satisfying roadmap Req 19.2 and M10's done-definition.
3. THE tests SHALL be pure Go, SQLite-only, and SHALL introduce no new environment
   variable or external dependency, and `gofmt`, `go vet`, `go build ./...`, and
   `go test ./...` SHALL be green at the end of every task, following the standing
   convention of the previous M10 milestones.

## Out of scope

- `wp-json wp/v2/menus` and `wp/v2/menu-items` REST routes (D2) — a larger
  WordPress surface, not part of this admin-API milestone.
- **Menu-location assignment** (binding a menu to a theme location in
  `theme_mods`) — RECOMMENDED out of scope for this milestone unless trivial,
  because it crosses into theme-options state the read path exposes only through
  `MenuByLocation`; if the design finds it trivial to add behind the same
  `edit_theme_options` gate, it may fold it in, but this requirements document
  does not require it. Flagged as an open question below.
- `DELETE` reinterpretation of the read routes — the read routes keep only `GET`.
- The existing menu read routes and their `edit_posts` gate — unchanged
  (Requirement 7.4).
- Any database migration (Requirement 6.4 confirms none is needed).
- Roadmap group 10.G (WordPress `$wp$` hash issuance).

## Open questions for design

Flagged here, to be resolved in `design.md` rather than left implicit:

1. **Menu-location assignment** — whether binding a menu to a theme location
   (`theme_mods`) is in scope. RECOMMENDATION: out of scope for 10.F unless the
   design judges it trivial under the same `edit_theme_options` gate (see "Out of
   scope").
2. **Slug derivation** — whether a menu's `slug` is auto-derived from its `name`
   (WordPress `sanitize_title` behavior) when the caller omits it, or must be
   caller-supplied. The design pins the rule and the collision behavior.
3. **Item-type validation set** — the exact accepted set of `_menu_item_type`
   values and, for `post_type`/`taxonomy` items, whether `_menu_item_object_id`
   is validated to reference an existing object at write time or stored as-is
   (the read path's `resolveMenuTarget` tolerates a dangling reference).
4. **Reorder/nest payload shape** — whether reordering and re-nesting are
   expressed as per-item `menu_order`/parent updates (the shape this document
   assumes) or as a single bulk "set the whole tree" operation; the design pins
   the request shape.

## Traceability

This spec refines **roadmap group 10.F** (Navigation menu editing, folded in from
M4's deferral) and traces to the roadmap's 10.F entry in
`../wordpress-core-parity-roadmap/tasks.md`. It depends on M10a (roadmap group
10.A / **Requirement 15**,
`../wordpress-core-parity-roadmap/requirements.md:492`), whose
`internal/sanitize` Policy it reuses without modification, and its test-coverage
requirement traces to roadmap **Requirement 19.2**
(`../wordpress-core-parity-roadmap/requirements.md:602`). It mirrors the
write-surface pattern established by M10.B (`../10.1-rest-media-write`) and M10.C
(`../10.2-rest-user-write`).
