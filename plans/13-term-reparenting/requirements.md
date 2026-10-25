# Requirements Document

M13 — Term re-parenting & hierarchy writes

## Introduction

M9b made `term_taxonomy.parent` *readable*. It added `domain.Term.ParentID`
(`internal/domain/entities.go:52-62`), populated it from term reads, and used it
to serve nested category archives with descendant-aware post counts: the
`EXISTS` semi-join in `PostRepo.archiveWhere` derives a parent category's post
set from the descendant term set live from `term_taxonomy.parent`
(`internal/storage/wprepo/repo.go:186-224`), and `content.TermHierarchy`
resolves the parent/child graph from a single `ListByTaxonomy` read
(`internal/content/archive.go:61-130`), exposing `DescendantIDs`
(`internal/content/archive.go:277-285`, "id followed by every term beneath it,
breadth-first") and `Ancestry`.

What M9b did **not** do is let anything *write* the parent. `TermRepo.Create`
INSERTs `term_taxonomy (term_id, taxonomy, description, parent, count)` with
`parent` hardcoded to `0`, ignoring `t.ParentID`
(`internal/storage/wprepo/writers.go:121`), and `TermRepo.Update` sets only
`name`/`slug` on `{prefix}terms` and never touches `term_taxonomy.parent`
(`internal/storage/wprepo/writers.go:146`). The hierarchy is therefore
read-only, exactly as `entities.go` documents it: "Read-only in this milestone:
term reads populate it, and no write path sets it"
(`internal/domain/entities.go:57-58`).

M13 closes that write gap. It threads a WordPress `parent` field through the
*existing* term write chain — the REST `restTermWriteRequest`
(`internal/web/rest_terms.go:33`) behind `POST /wp-json/wp/v2/categories`
(`internal/web/rest_terms.go:56-58`) and the admin-API term write body behind
`adminTermCreate`/`adminTermUpdate`
(`internal/web/adminapi_terms.go:72`, `:99`) — down through
`content.TermWriteService` (`internal/content/writeservices.go:303`) and into
`term_taxonomy.parent`, with cycle prevention and taxonomy validation. No new
route, port method, or capability is introduced; M13 fills the read-only
`ParentID` write gap and nothing more.

M13 refines roadmap **Requirement 26 / 27**
(`../wordpress-core-parity-roadmap/requirements.md`, "M13 — Term Re-parenting &
Hierarchy Writes Requirements") and roadmap task group **13.A**
(`../wordpress-core-parity-roadmap/tasks.md`). It builds directly on M9b's
`TermHierarchy` read model and the M10.F / M6 term-write-group pattern
(`content.TermWriteService`, the `manage_categories` gate). It is the first of
the post-M10 gap-analysis candidates taken up after M11.

## Glossary

- **`term_taxonomy.parent`** — the WordPress column on `{prefix}term_taxonomy`
  that names a term's parent term (the `term_id` of the parent), `0` for a
  top-level term. M9b reads it; M13 writes it.
- **`domain.Term.ParentID`** — the Go field backing `term_taxonomy.parent`
  (`internal/domain/entities.go:52-62`); `0` means "no parent", matching
  WordPress's own sentinel. Read-only before M13.
- **Re-parenting** — changing an existing term's `term_taxonomy.parent` to a
  different value (another term, or `0` for top-level) through an update.
- **Cycle** — a `parent` assignment that would make a term a descendant of
  itself, directly (a term named as its own parent) or transitively (a term's
  parent set to one of the term's own descendants).
- **Descendant set** — a term followed by every term beneath it in its
  taxonomy's graph, resolved by `TermHierarchy.DescendantIDs(id)`
  (`internal/content/archive.go:277-285`); self-inclusive and breadth-first.
- **Top-level (`parent 0`)** — a term with no parent; `parent: 0` is always a
  valid target and makes a term top-level.
- **Hierarchical vs flat taxonomy** — `category` is hierarchical (a category's
  parent is another category); `post_tag` is flat (non-hierarchical in
  WordPress), so a tag has no meaningful parent and MUST reject any non-zero
  `parent`.
- **`manage_categories`** — the WordPress capability gating term create/update/
  delete, resolved through `auth.CanManageTerms(actor)`
  (`internal/auth/policy.go:78`); `TermWriteService` already enforces it.
- **The `EXISTS` semi-join / descendant-aware count** — M9b's term predicate in
  `PostRepo.archiveWhere` (`internal/storage/wprepo/repo.go:186-224`) and
  `CountByTermSlug`, which derive a parent category's post set from its
  descendant term set live from `term_taxonomy.parent`, yielding one row per
  post so pagination totals and single-post-appears-once hold.

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | **Thread `parent` end to end through the existing chain, no new write surface.** Add a `Parent *int64 json:"parent"` field to `restTermWriteRequest` (`internal/web/rest_terms.go:33`) and to the admin term write body; map it into `domain.Term.ParentID`; `TermWriteService.Create`/`Update` (`internal/content/writeservices.go:303`) carry it; `TermRepo.Create` writes `t.ParentID` into `term_taxonomy.parent` instead of the hardcoded `0` (`internal/storage/wprepo/writers.go:121`); `TermRepo.Update` also updates `term_taxonomy.parent` (`internal/storage/wprepo/writers.go:146`). No new route, port method, or capability is added. | Requirements 1, 2, 8. M13 fills the read-only `ParentID` write gap (`internal/domain/entities.go:57-58`). |
| D2 | **Partial update semantics.** On update, `parent` is a pointer: omitted (`nil`) leaves the existing parent unchanged; `parent: 0` explicitly sets "no parent" (top-level); `parent: <id>` re-parents. This matches the omitted-vs-explicit-zero posture the sibling term write DTO already uses for `Name`/`Slug` (`internal/web/rest_terms.go:33`). | Requirement 2. The `nil` pointer carries "unchanged" distinctly from the value `0`. |
| D3 | **Cycle prevention in the service, fail closed.** `TermWriteService.Update` (and `Create`, though a brand-new term has no descendants) rejects a `parent` that would create a cycle: the proposed parent must not be the term itself, nor any member of the term's descendant set (`TermHierarchy.DescendantIDs(termID)` loaded for the term's taxonomy, `internal/content/archive.go:277-285`). On a cycle, return a sentinel error → the handler maps it to `400`; the stored hierarchy is left unchanged. | Requirement 3. Reuses M9b's existing graph; no new cycle logic in storage. |
| D4 | **Parent must exist and share the taxonomy.** A non-zero `parent` MUST resolve to an existing term in the SAME taxonomy (a category's parent is a category). `post_tag` is flat and MUST reject any non-zero parent. An invalid, cross-taxonomy, or nonexistent parent → `400`, no write. Parent `0` is always valid (top-level). | Requirement 4. |
| D5 | **Capability gate unchanged.** Writes stay gated on `manage_categories` via `auth.CanManageTerms` (`internal/auth/policy.go:78`), which `TermWriteService` already enforces (`internal/content/writeservices.go:303`); a caller lacking it → the same forbidden-write status its REST/admin term write siblings return (REST: `403` rest-forbidden shape; admin: the admin-JSON `403`). No new capability. | Requirement 5. |
| D6 | **Descendant counts/archives stay correct by construction.** M9b's `EXISTS` semi-join and `CountByTermSlug` derive the descendant set live from `term_taxonomy.parent` (`internal/storage/wprepo/repo.go:186-224`), so a re-parent is reflected on the next read with no extra write-side bookkeeping (no `term_taxonomy.count` cache to update beyond what already happens). M13 adds tests proving that after re-parenting, a parent archive's pagination `Total`/`TotalPages` and the single-post-appears-once guarantee still hold. No schema change, no migration. | Requirements 6, 7. M13 PROVES the existing read stays correct; it does not re-implement it. |
| D7 | **Tests: cross-vendor repo contract + service cycle/parent-validation + REST/admin handler gate + count-after-reparent, pure Go/SQLite, no new dependency.** Cover `TermRepo.Create`/`Update` write/read-back of `parent` (cross-vendor contract); the service rejecting self-parent, descendant-parent (cycle), cross-taxonomy/nonexistent parent, and a `post_tag` non-zero parent; the `manage_categories` `403` gate on REST + admin; and a count-correctness-after-reparent test (parent archive total + appears-once). SQLite-only for the Go tests, env-gated MySQL/Postgres as the existing contract harness does; no new env var or dependency; `gofmt`/`go vet`/`go build ./...`/`go test ./...` green at the end of every task. | Requirement 9. |

## Requirements

### Requirement 1: Create a term with a parent writes term_taxonomy.parent

**User Story:** As a content editor, I want to create a category under an
existing parent category through the admin API and `POST /wp-json/wp/v2/categories`,
so that a new category is nested in my taxonomy from the moment it is created,
the way WordPress lets me.

#### Acceptance Criteria

1. THE `restTermWriteRequest` type (`internal/web/rest_terms.go:33`) and the
   admin term write body SHALL each gain a `Parent *int64` field tagged
   `json:"parent"`, mapped into `domain.Term.ParentID` before the value reaches
   `TermWriteService.Create` (D1).
2. WHEN a caller holding `manage_categories` creates a `category` through
   `POST /wp-json/wp/v2/categories` or the admin term create handler with a
   non-zero `parent` that satisfies Requirement 4, THE system SHALL write that
   value into the new term's `term_taxonomy.parent`, replacing the hardcoded `0`
   in `TermRepo.Create` (`internal/storage/wprepo/writers.go:121`) with
   `t.ParentID`.
3. WHEN a caller creates a term and omits `parent` (the field is `nil`) or sends
   `parent: 0`, THE system SHALL store `term_taxonomy.parent = 0` (top-level),
   preserving the pre-M13 default behavior.
4. WHEN `TermRepo.Create` writes the taxonomy row, THE system SHALL continue to
   write the row in the same single transaction (`RunInTx`) with the existing
   `description`/`count` defaults, changing only the `parent` value written.

### Requirement 2: Update re-parents with partial-update pointer semantics

**User Story:** As a content editor, I want to move an existing category to a
different parent, or promote it to top-level, without disturbing its name or
slug, so that I can re-organize my taxonomy incrementally.

#### Acceptance Criteria

1. WHEN a caller holding `manage_categories` updates a `category` and supplies
   `parent: <id>` that satisfies Requirements 3 and 4, THE system SHALL set that
   term's `term_taxonomy.parent` to that id, extending `TermRepo.Update`
   (`internal/storage/wprepo/writers.go:146`), which currently sets only
   `name`/`slug` and never touches `term_taxonomy.parent`.
2. WHEN a caller updates a term and omits `parent` (the field is `nil`), THE
   system SHALL leave the term's existing `term_taxonomy.parent` unchanged.
3. WHEN a caller updates a term and supplies `parent: 0`, THE system SHALL set
   that term's `term_taxonomy.parent` to `0`, making the term top-level.
4. THE system SHALL distinguish an omitted `parent` (`nil` pointer, "unchanged")
   from an explicit `parent: 0` ("top-level") using the `*int64` pointer, so a
   partial update that renames a term without a `parent` key does not silently
   reset the term to top-level.
5. WHEN `TermRepo.Update` updates `term_taxonomy.parent`, THE system SHALL return
   `domain.ErrNotFound` for a `term_id` that names no term, preserving the
   existing `errNotFoundIfMissing` behavior and its MySQL matched-but-unchanged
   no-op caveat (`internal/storage/wprepo/writers.go:146`).

### Requirement 3: Cycle prevention rejects self-parent and descendant-parent

**User Story:** As a maintainer, I want the service to refuse any re-parent that
would make a category its own ancestor, so that the taxonomy graph can never
contain a cycle that would break ancestry walks and archive listings.

#### Acceptance Criteria

1. IF an update supplies a `parent` equal to the term's own id, THEN THE
   `TermWriteService` SHALL reject the write with a sentinel cycle error and
   SHALL leave `term_taxonomy.parent` unchanged.
2. IF an update supplies a `parent` that is a member of the term's descendant
   set — `TermHierarchy.DescendantIDs(termID)` loaded for the term's taxonomy
   (`internal/content/archive.go:277-285`) — THEN THE `TermWriteService` SHALL
   reject the write with the same sentinel cycle error and SHALL leave
   `term_taxonomy.parent` unchanged.
3. WHEN the `TermWriteService` returns the cycle sentinel error, THE REST term
   write handler SHALL respond `400` and the admin term write handler SHALL
   respond with the admin-JSON `400`, and neither SHALL alter any term's parent.
4. WHEN a term is *created* (no existing descendants), THE `TermWriteService`
   SHALL apply the same cycle check for consistency, which a brand-new term
   trivially passes for any parent other than itself.
5. THE cycle check SHALL reuse the existing `TermHierarchy` graph rather than
   introduce a new descendant walk in the storage layer (D3).

### Requirement 4: Parent must exist and share the taxonomy; post_tag rejects any parent

**User Story:** As a maintainer, I want a parent to be a real term in the same
taxonomy, and tags to reject parents entirely, so that the hierarchy stays
well-formed and matches WordPress's hierarchical-vs-flat taxonomy model.

#### Acceptance Criteria

1. IF a create or update supplies a non-zero `parent` that does not resolve to
   an existing term, THEN THE system SHALL reject the write with a `400` (REST
   and admin) and SHALL NOT write `term_taxonomy.parent`.
2. IF a create or update supplies a non-zero `parent` that resolves to a term in
   a DIFFERENT taxonomy than the term being written, THEN THE system SHALL reject
   the write with a `400` and SHALL NOT write `term_taxonomy.parent`.
3. IF a create or update targets a `post_tag` term and supplies a non-zero
   `parent`, THEN THE system SHALL reject the write with a `400`, because
   `post_tag` is flat (non-hierarchical) and has no meaningful parent.
4. WHEN a create or update supplies `parent: 0` (or omits `parent` on create),
   THE system SHALL treat it as a valid top-level assignment for any taxonomy,
   including `post_tag`.
5. THE parent-existence and same-taxonomy checks SHALL be evaluated against the
   stored taxonomy graph / term read, NEVER against a taxonomy value taken from
   the request for the parent.

### Requirement 5: manage_categories gate on REST and admin, unchanged

**User Story:** As a site administrator, I want re-parenting gated by the same
capability that already gates term writes, so that no new authorization surface
is introduced by hierarchy writes.

#### Acceptance Criteria

1. IF a caller lacking `manage_categories` sends a term create or update carrying
   a `parent` through `POST /wp-json/wp/v2/categories` or the single-item
   `PUT`/`PATCH`, THEN THE REST term write handler SHALL respond `403` in the
   rest-forbidden shape its existing term write siblings use and SHALL NOT alter
   any term's parent.
2. IF a caller lacking `manage_categories` sends a term create or update carrying
   a `parent` through the admin term create/update handler
   (`internal/web/adminapi_terms.go:72`, `:99`), THEN the admin handler SHALL
   respond with the admin-JSON `403` and SHALL NOT alter any term's parent.
3. THE capability decision SHALL continue to be made by
   `auth.CanManageTerms(actor)` (`internal/auth/policy.go:78`) inside
   `TermWriteService.Create`/`Update` (`internal/content/writeservices.go:303`),
   which returns `ErrForbidden` before any write; M13 SHALL NOT add a new
   capability name.
4. WHEN `s.termWrite` is `nil`, THE REST term routes SHALL remain unregistered
   exactly as today (`internal/web/rest_terms.go`), so threading `parent` through
   does not change the nil-gating of the write surface.

### Requirement 6: REST, admin, and archive reads reflect the new parent after write

**User Story:** As an integrator, I want a category's `parent` as read back
through the REST view and the admin read to match what I just wrote, so that a
write/read round-trip is consistent.

#### Acceptance Criteria

1. WHEN a term's `term_taxonomy.parent` is written per Requirement 1 or 2, THE
   REST single/collection view `restTerm.parent`
   (`internal/web/rest_terms.go:23-32`) SHALL report the new value on the next
   read, since it already renders `domain.Term.ParentID`.
2. WHEN a term's `term_taxonomy.parent` is written, THE admin term read SHALL
   report the new parent on the next read.
3. WHEN a term is re-parented, THE nested-archive route SHALL resolve the term's
   ancestry and descendant listing from the new `term_taxonomy.parent` via a
   freshly loaded `TermHierarchy`, because `TermHierarchy` is loaded per request
   from the live column and is deliberately not cached across requests
   (`internal/content/archive.go:61-130`).
4. THE round-trip SHALL hold for both `parent: <id>` (re-parent) and `parent: 0`
   (promote to top-level).

### Requirement 7: Descendant-aware counts and archives stay correct after re-parent

**User Story:** As a maintainer, I want a parent category's post counts and
archive pagination to stay correct after I move a child category, so that
re-parenting cannot corrupt the totals M9b established.

#### Acceptance Criteria

1. WHEN a child category is re-parented under a different parent category, THE
   parent archive's post set SHALL be recomputed on the next read from the live
   `term_taxonomy.parent` via M9b's `EXISTS` semi-join
   (`internal/storage/wprepo/repo.go:186-224`), with no additional write-side
   bookkeeping (D6).
2. WHEN a parent archive is listed after a re-parent, THE pagination `Total` and
   `TotalPages` SHALL reflect the new descendant set, and a single post filed
   under both a parent and a descendant SHALL appear exactly once (the
   appears-once guarantee the `EXISTS` semi-join provides).
3. WHEN `CountByTermSlug` is read after a re-parent, THE descendant-aware count
   SHALL reflect the new descendant set, consistent with the archive listing for
   the same term.
4. THE milestone SHALL NOT update or rely on a `term_taxonomy.count` cache beyond
   what the existing write path already does, because the descendant-aware counts
   are computed live (D6).

### Requirement 8: No schema change, no migration, no new capability; read ports unchanged

**User Story:** As a maintainer, I want M13 to be a pure write-gap fill over the
existing schema and ports, so that it carries no migration or port churn.

#### Acceptance Criteria

1. THE milestone SHALL add NO file under `internal/storage/migrations`, because
   `{prefix}term_taxonomy` and its `parent` column already exist; the task list
   SHALL verify this explicitly, as the sibling specs' task lists do.
2. THE `domain.TermWriter` interface
   (`internal/domain/repository.go`, `Create`/`Update`/`Delete`) and the
   `domain.TermReader` interface (`ListByTaxonomy`/`TermsByIDs`) SHALL be
   unchanged by M13; the `parent` value flows through the existing `domain.Term`
   field `ParentID`, adding no new port method.
3. THE milestone SHALL introduce NO new capability name, keeping the
   `manage_categories` gate (Requirement 5).
4. THE milestone SHALL add NO new `wp-json` or admin route; it only extends the
   bodies and write methods of the existing term create/update handlers (D1).
5. THE read ports and the M9b read model (`TermHierarchy`, the `EXISTS`
   semi-join, `CountByTermSlug`) SHALL be unchanged in behavior; M13 proves they
   stay correct under re-parenting rather than modifying them.

### Requirement 9: Cross-vendor, service, handler, and count test coverage

**User Story:** As a maintainer, I want every new behavior proven before it is
considered done, so that the hierarchy write cannot become a data-integrity or
authorization bypass.

#### Acceptance Criteria

1. THE milestone SHALL cover `TermRepo.Create` and `TermRepo.Update` writing and
   reading back `term_taxonomy.parent` as a cross-vendor repository contract test
   (SQLite always; MySQL/Postgres env-gated through the existing contract
   harness, with no new env var).
2. THE milestone SHALL cover `TermWriteService` rejecting a self-parent, a
   descendant-parent (cycle), a cross-taxonomy parent, a nonexistent parent, and
   a `post_tag` non-zero parent, each leaving the stored hierarchy unchanged.
3. THE milestone SHALL cover the `manage_categories` `403` gate on both the REST
   term write handler and the admin term write handler for a `parent`-carrying
   create/update.
4. THE milestone SHALL cover a count-correctness-after-reparent test: after a
   child category is re-parented, a parent archive's pagination `Total` and the
   single-post-appears-once guarantee still hold (Requirement 7).
5. THE tests SHALL be pure Go over SQLite with NO new dependency, and `gofmt`,
   `go vet`, `go build ./...`, and `go test ./...` SHALL be green at the end of
   every task (D7).

## Out of scope

- **Term create/update for taxonomies beyond `category`.** `post_tag` is flat
  and only ever rejects a non-zero parent (Requirement 4.3); custom hierarchical
  taxonomies are M12's taxonomy registry, not M13. M13's hierarchy write is
  scoped to the already-modeled hierarchical taxonomy, `category`.
- **Term DELETE re-parenting of orphaned children.** WordPress re-parents a
  deleted term's children to its grandparent on delete. M13 does NOT implement
  this; it is a deliberate non-goal. Term delete behavior stays as it is
  (`TermRepo.Delete`), and orphaned children remain treated as top-level by the
  existing `TermHierarchy` orphan rule (`internal/content/archive.go`).
  Recommendation: OUT of M13.
- **Bulk re-parent** (moving many terms in one request).
- **A `term_taxonomy.count` cache rewrite.** Counts are computed live from the
  descendant set (D6); M13 adds no count-caching maintenance.
- **Admin-UI term-hierarchy editing surfaces** beyond passing `parent` through
  the existing create/update handlers — no new picker, drag-and-drop, or
  tree-editing UI.

## Traceability

This spec refines roadmap **Requirement 26 (Term Parent Write)** and
**Requirement 27 (M13 Exclusions and Test Coverage)**
(`../wordpress-core-parity-roadmap/requirements.md`, "M13 — Term Re-parenting &
Hierarchy Writes Requirements") and roadmap task group **13.A — Term parent
write** (`../wordpress-core-parity-roadmap/tasks.md`). It builds directly on
**M9b**'s read model — `domain.Term.ParentID`, `content.TermHierarchy`
(`internal/content/archive.go:61-130`), the `EXISTS` semi-join and
`CountByTermSlug` (`internal/storage/wprepo/repo.go:186-224`) — and on the
**M10.F / M6** term write-group pattern (`content.TermWriteService`,
`internal/content/writeservices.go:303`; the `manage_categories` gate,
`internal/auth/policy.go:78`). It is the first post-M10 gap-analysis candidate
taken up after M11.
