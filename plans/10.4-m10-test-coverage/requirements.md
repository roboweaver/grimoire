# Requirements Document

M10.E — M10 Write-Route Test Coverage

## Introduction

M10a shipped the write-boundary content policy (`internal/sanitize`), M10.B
(spec `../10.1-rest-media-write`) turned on the first new REST write surface
over it, M10.C (spec `../10.2-rest-user-write`) added REST user writes in the
same shape, and M10.D (spec `../10.3-rest-content-rendered`) transformed the
read-path `content.rendered` field. This milestone (10.E) is a different kind of
work again. It is the **test-coverage close-out** for M10: it adds **no feature
and no new write surface**, and it exists solely to prove that roadmap
**Requirement 19.2** holds across **every** M10 write route — each one is backed
by a capability-matrix test (allowed vs. forbidden caller) and a content-safety
test (the Policy is applied before persistence).

The feature siblings each enabled exactly one surface: 10.B over media, 10.C
over users, and 10.D (a read-path transform, not a write). Each of those shipped
with its own tests. 10.E is not another surface — it is an **audit plus
gap-fill**. It inventories the full set of M10 write routes, confirms the two
required tests exist for each, reuses the ones that already exist by citing them
by name, and adds **only** the missing ones. It produces an auditable coverage
matrix — route × verb × {capability-matrix test, content-safety test} — so that
"covered" is a concrete checklist rather than a claim. The milestone is
**test-only**: no production code is expected to change. If the audit surfaces a
genuine coverage gap that masks a real bug, that is reported as a Finding for an
explicit decision, not silently patched.

The M10 write routes under audit are:

| # | Route | Verb(s) | Handler(s) | Enabled by |
|---|---|---|---|---|
| 1 | REST posts/pages | `POST` (create), `PUT`/`PATCH` (update), `DELETE` | `internal/web/rest_posts.go` `handleRESTPostCreate`/`Update`/`Delete` | M10a |
| 2 | REST comments | `POST` (create) | `internal/web/rest_comments.go` `handleRESTCommentCreate` | M10a |
| 3 | Admin posts | `POST`/`PUT`/`DELETE` `/admin/api/posts` | `internal/web/adminapi_posts.go` `adminPostCreate`/`Update`/`Delete` | M10a |
| 4 | Admin media | `POST` `/admin/api/media` | `internal/web/adminapi_media.go` `adminMediaUpload` | M10a (author seam in 10.B) |
| 5 | REST media | `POST` (create), `PUT`/`PATCH`/`POST` (update) | `internal/web/rest_media.go` | 10.B |
| 6 | REST users | `POST` (create), `PUT`/`PATCH`/`POST` (update) | `internal/web/rest_users.go` | 10.C |

The audit's finding — the gap 10.E closes — is that routes 5 and 6 (REST media,
REST users) already carry **both** a consolidated capability-matrix test **and**
a content-safety test, so 10.E only inventories and cross-references them. Routes
1–4 (the M10a post/admin/comment/admin-media surfaces) carry content-safety
proofs today, but a **consolidated allowed-vs-forbidden capability-matrix** in
the table-driven style 10.B/10.C used is **missing** — only scattered
forbidden-case transport tests exist (for example `TestAdminPostUpdateForbidden`,
`TestAdminPostDeleteMissingRecordIsForbidden`). Supplying those matrices, and
confirming (adding where missing) a transport-level content-safety proof on the
pre-existing admin post create/update and comment create paths, is the primary
work of this milestone.

This refines **roadmap group 10.E only**, tracing to roadmap **Requirement 19.2**
(`../wordpress-core-parity-roadmap/requirements.md`, the M10 done-definition:
"Every new write endpoint SHALL be covered by a capability-matrix test (allowed
vs. forbidden caller) and a content-safety test proving sanitization is applied,
before it is considered done"). The plugin-surface exclusion folds in roadmap
**Requirement 19.1**. Roadmap groups 10.F (menu editing) and 10.G (WordPress
`$wp$` hash issuance) remain open and are out of scope (see "Out of scope"); the
read-path render tests already shipped in 10.D (roadmap Requirement 18) are not
re-done here.

## Glossary

- **Write route** — a REST or admin-API endpoint-and-verb pairing that creates,
  updates, or deletes persisted state, enabled or governed by M10 as enumerated
  in the Introduction table. The set is closed for this milestone: routes 1–6
  above and no others.
- **Capability-matrix test** — a table-driven transport-level test that, for a
  single write route, exercises both **allowed** and **forbidden** callers
  across the roles relevant to that route's gating capability (anonymous,
  subscriber, contributor, author, editor, administrator as applicable),
  asserting that an authorized caller is permitted and every unauthorized caller
  receives the WordPress-faithful forbidden response (`403` with the route's
  error code) with no state change. Principals are built through the real
  `auth.CapabilitiesForRoles` / `auth.NewPrincipal` path (`internal/auth`), never
  a hand-forged capability set.
- **Content-safety test** — a transport-level test that drives a write route with
  caller-supplied markup and proves the Policy ran **before persistence** by
  asserting on the **stored / writer-visible** state (the writer or store saw
  inert content), not merely on an escaped HTTP response — the "writer saw inert
  content" posture 10.B and 10.C established. For a tier-A/B writer the markup is
  reduced; for a tier-C (`unfiltered_html`) writer the value is preserved.
- **The coverage matrix** — the auditable deliverable this milestone produces: a
  table mapping every write route × verb to the **name** of its capability-matrix
  test and the **name** of its content-safety test. A cell that already has a
  test cites it by name; a cell that is missing a test is filled by a test 10.E
  adds. "Done" for this milestone is: the matrix is complete and every cell is
  backed by a named, passing test.
- **The Policy** — M10a's `sanitize.Policy` (`internal/sanitize/policy.go`), the
  single sanitization component applied on every M10 write path before
  persistence. 10.E asserts its application; it does not modify it.
- **Existing matrices** — the table-driven capability-matrix tests already
  present for routes 5–6: `TestRESTMediaCreateCapabilityMatrix`,
  `TestRESTMediaUpdateCapabilityMatrix`, `TestRESTMediaCreateMIMEBypassMatrix`,
  `TestRESTMediaWriteAuthMatrix` (`internal/web/rest_media_test.go`);
  `TestRESTUserCreateCapabilityMatrix`, `TestRESTUserUpdateSelfVsOtherMatrix`,
  `TestRESTUserRoleAssignmentMatrix`, `TestRESTUserWriteAuthMatrix`
  (`internal/web/rest_users_test.go`).
- **Existing content-safety proofs** — the sanitization tests already present for
  routes 1, 3, and 5: `TestRESTPostCreateSanitizesContentExcerptAndTitleEndToEnd`,
  `TestRESTPostUpdateSanitizesContentExcerptAndTitleEndToEnd`
  (`internal/web/rest_posts_test.go`);
  `TestAdminPostCreateSanitizesContentExcerptAndTitleEndToEnd`,
  `TestAdminPostUpdateSanitizesContentExcerptAndTitleEndToEnd`
  (`internal/web/adminapi_posts_test.go`);
  `TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter`
  (`internal/web/rest_comments_test.go`); the REST-media description-sanitized
  e2e in `internal/web/rest_media_test.go`; the REST-user description-sanitized
  e2e in `internal/web/rest_users_test.go`; plus the service-level wiring proofs
  `internal/content/writeservices_test.go` (`PostWriteService`) and
  `internal/content/comments_test.go` (`CommentService.Create`).

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | **Audit + gap-fill, not rewrite.** 10.E inventories every M10 write route, confirms each has a capability-matrix test and a content-safety test, and adds ONLY the missing ones. Existing 10.B/10.C matrices and M10a content-safety proofs are reused and cross-referenced, never duplicated. | The milestone's scope is bounded by the audit: routes 5–6 are inventoried as already covered; the new tests target routes 1–4 (post/admin/comment/admin-media capability matrices, and transport content-safety on admin post create/update and comment create where only a service-level proof exists). |
| D2 | **The coverage matrix is a deliverable.** The implementation produces a coverage matrix (route × verb × {capability-matrix test name, content-safety test name}) so "covered" is a concrete, auditable checklist. Where a cell already has a test it is cited by name; where it is missing, 10.E adds it. | Requirement 1 makes the matrix an explicit artifact; every other requirement feeds a named test into exactly one cell. "Done" = matrix complete, every cell backed by a named passing test. |
| D3 | **Pre-existing admin/comment paths are included.** Content-safety is verified end-to-end (transport level, asserting the Policy ran before persistence — the writer/store saw inert content, not merely an escaped response) on admin post create/update and comment create, matching the "writer saw inert content" posture of 10.B/10.C. A consolidated capability-matrix (allowed vs. forbidden) is added for the post/admin/comment routes in the table-driven style where only scattered forbidden-case tests exist today. | Requirements 2 and 3 cover these routes explicitly; the roadmap's naming of "the pre-existing admin/comment write paths" is satisfied by a transport-level assertion on stored/writer-visible state, not only the existing service-level wiring proofs. |
| D4 | **Test-only; no production change expected.** 10.E adds tests and a coverage document only. A genuine gap that masks a bug is surfaced as a Finding for explicit decision, not silently patched. The work is pure Go, SQLite-only, adds no new `GRIMOIRE_TEST_*` environment variable, and keeps all gates green. | Requirement 6 forbids production-code change as the default and defines the Finding path; Requirement 7 fixes the close-out discipline (gofmt/vet/build/test green, pure Go, SQLite-only, no new env var). |

## Requirements

### Requirement 1: The coverage matrix is a complete, auditable deliverable

**User Story:** As a maintainer, I want a single auditable matrix enumerating
every M10 write route and the named tests that cover it, so that "M10 is tested"
is a concrete checklist rather than an assertion.

#### Acceptance Criteria

1. THE milestone SHALL produce a coverage matrix enumerating every M10 write
   route × verb from the Introduction table (REST posts/pages create/update/
   delete, REST comments create, admin posts create/update/delete, admin media
   create, REST media create/update, REST users create/update), mapping each
   cell to the **name** of its capability-matrix test and the **name** of its
   content-safety test, satisfying roadmap Req 19.2.
2. WHERE a cell is already covered by an existing test, THE coverage matrix SHALL
   cite that test by its exact function name and file path (the Existing matrices
   and Existing content-safety proofs named in the Glossary) and SHALL NOT add a
   duplicate test for that cell (D1).
3. WHERE a cell is not yet covered by a named test, THE milestone SHALL add a
   test that fills that cell and THE coverage matrix SHALL cite the added test by
   name.
4. THE coverage matrix SHALL mark any route × verb that is intentionally not a
   governed write (for example a `DELETE` verb that is a `501` stub or a
   delete-by-capability path with no content body) as such, so that an empty cell
   is a recorded decision rather than an unexplained gap.
5. THE milestone SHALL be considered done only WHEN the coverage matrix is
   complete AND every non-excluded cell is backed by a named, passing test (D2).

### Requirement 2: Capability-matrix tests for the M10a post/admin/comment routes

**User Story:** As a maintainer, I want the pre-existing post, admin-post, and
comment write routes covered by a consolidated allowed-vs-forbidden capability
matrix in the same table-driven style as the media and user routes, so that
authorization coverage is uniform across every M10 write route.

#### Acceptance Criteria

1. THE milestone SHALL add a capability-matrix test for REST post/page create
   (`internal/web/rest_posts.go` `handleRESTPostCreate`) exercising allowed and
   forbidden callers across the roles relevant to the route's gating capability
   (at minimum anonymous and a role lacking the create capability as forbidden,
   and a role holding it as allowed), asserting a `403` with the route's
   WordPress-faithful error code for each forbidden caller and no post is
   created, satisfying roadmap Req 19.2.
2. THE milestone SHALL add a capability-matrix test for REST post/page update and
   delete (`handleRESTPostUpdate`, `handleRESTPostDelete`) exercising allowed and
   forbidden callers, asserting the forbidden callers receive the route's `403`
   error code and the stored post is unchanged / not deleted.
3. THE milestone SHALL add a capability-matrix test for admin post create,
   update, and delete (`internal/web/adminapi_posts.go` `adminPostCreate`/
   `Update`/`Delete`) that consolidates the scattered forbidden-case tests (for
   example `TestAdminPostUpdateForbidden`,
   `TestAdminPostDeleteMissingRecordIsForbidden`) into a single table-driven
   allowed-vs-forbidden matrix, asserting the permitted caller succeeds and each
   forbidden caller receives the admin-API forbidden response with no state
   change.
4. THE milestone SHALL add a capability-matrix test for admin media create
   (`internal/web/adminapi_media.go` `adminMediaUpload`) exercising an authorized
   uploader (allowed) against callers lacking the upload capability (forbidden),
   asserting no media row is created for a forbidden caller.
5. THE milestone SHALL add a capability-matrix test for REST comment create
   (`internal/web/rest_comments.go` `handleRESTCommentCreate`) that covers the
   route's actual authorization contract — including the anonymous-allowed create
   path and the `moderate_comments`-gated status/moderation seam
   (`p.Can("moderate_comments")`) — asserting allowed and forbidden outcomes with
   no comment created where forbidden.
6. THE capability-matrix tests SHALL build every principal through the real
   `auth.CapabilitiesForRoles` and `auth.NewPrincipal` path (`internal/auth`),
   using the role names relevant to each route (anonymous / subscriber /
   contributor / author / editor / administrator as applicable), and SHALL NOT
   hand-forge a capability set that bypasses that path.
7. THE capability-matrix tests SHALL assert on the transport-level response
   (status code and WordPress-faithful error code) AND on the absence of a
   persisted state change for every forbidden caller, so that a forbidden caller
   provably mutates nothing.

### Requirement 3: Transport-level content-safety proofs on the pre-existing admin and comment write paths

**User Story:** As a security reviewer, I want the pre-existing admin post and
comment write paths proven to sanitize at the transport level before
persistence, so that every M10 write route's content-safety guarantee is
asserted end-to-end, not only through service-layer wiring.

#### Acceptance Criteria

1. THE milestone SHALL confirm, and add where missing, a transport-level
   content-safety proof for admin post create and update
   (`internal/web/adminapi_posts.go` `adminPostCreate`/`Update`) that drives the
   endpoint with caller-supplied markup and asserts the **stored / writer-visible**
   content was sanitized (the writer saw inert content), satisfying roadmap
   Req 19.2 for "the pre-existing admin … write paths."
2. THE milestone SHALL confirm, and add where missing, a transport-level
   content-safety proof for REST comment create
   (`internal/web/rest_comments.go` `handleRESTCommentCreate`) that asserts the
   comment content reaching the writer / store was sanitized before persistence
   (a tier-A/B caller's markup reduced), satisfying roadmap Req 19.2 for "the
   pre-existing … comment write paths."
3. WHERE a transport-level content-safety proof already exists for a route (for
   example `TestAdminPostCreateSanitizesContentExcerptAndTitleEndToEnd`,
   `TestAdminPostUpdateSanitizesContentExcerptAndTitleEndToEnd`,
   `TestRESTCommentCreateAnonymousSanitizesTierABeforeWriter`), THE milestone
   SHALL cite it in the coverage matrix and SHALL NOT duplicate it (D1).
4. THE content-safety proofs SHALL assert the Policy ran BEFORE persistence by
   inspecting the stored or writer-visible value (not merely an HTML-escaped
   response body), matching the "writer saw inert content" posture 10.B and 10.C
   established (D3).
5. WHERE a route governs a tier-C (`unfiltered_html`) caller, THE content-safety
   proof SHOULD assert the tier-C passthrough (value preserved) alongside the
   tier-A/B reduction, mirroring the dual-tier assertion the user and media
   content-safety tests already make.

### Requirement 4: Inventory and cross-reference the already-covered media and user routes

**User Story:** As a maintainer, I want the already-covered REST media and REST
user routes inventoried by name rather than re-tested, so that 10.E records their
coverage without duplicating work.

#### Acceptance Criteria

1. THE coverage matrix SHALL cite the existing REST media capability-matrix tests
   (`TestRESTMediaCreateCapabilityMatrix`, `TestRESTMediaUpdateCapabilityMatrix`,
   `TestRESTMediaCreateMIMEBypassMatrix`, `TestRESTMediaWriteAuthMatrix`,
   `internal/web/rest_media_test.go`) for the REST media create/update cells and
   SHALL NOT add replacements for them (D1).
2. THE coverage matrix SHALL cite the existing REST user capability-matrix tests
   (`TestRESTUserCreateCapabilityMatrix`, `TestRESTUserUpdateSelfVsOtherMatrix`,
   `TestRESTUserRoleAssignmentMatrix`, `TestRESTUserWriteAuthMatrix`,
   `internal/web/rest_users_test.go`) for the REST user create/update cells and
   SHALL NOT add replacements for them (D1).
3. THE coverage matrix SHALL cite the existing REST media and REST user
   content-safety proofs (the description-sanitized-before-store e2e in
   `internal/web/rest_media_test.go` and `internal/web/rest_users_test.go`) for
   those routes' content-safety cells and SHALL NOT add replacements for them.
4. IF the inventory reveals that a media or user cell believed covered is in fact
   not backed by a passing test, THEN THE milestone SHALL treat that as a gap
   under Requirement 1.3 (add the missing test) rather than assuming prior
   coverage.

### Requirement 5: Findings for gaps that mask real behavior

**User Story:** As a maintainer, I want any coverage gap that would mask a real
authorization or sanitization defect surfaced as an explicit Finding, so that a
latent bug is a decision point rather than an invisible silent patch.

#### Acceptance Criteria

1. IF adding a capability-matrix or content-safety test reveals that a write
   route's actual behavior violates its intended authorization or sanitization
   contract, THEN THE milestone SHALL record a Finding describing the route, the
   expected behavior, and the observed behavior, and SHALL NOT silently change
   production code to make the test pass (D4).
2. WHEN a Finding is recorded, THE milestone SHALL leave the production code
   unchanged pending an explicit decision, and the newly added test SHALL either
   encode the intended (correct) behavior as a documented failing/skipped case
   tied to the Finding, or assert the current behavior with a comment pointing to
   the Finding — the design SHALL pin which of these two forms is used.
3. THE Findings SHALL be captured in the milestone's coverage document alongside
   the coverage matrix, so the audit's conclusions travel with its checklist.

### Requirement 6: No production-code change as the default

**User Story:** As a reviewer, I want 10.E to be test-and-document only, so that a
test-coverage milestone cannot quietly alter runtime behavior.

#### Acceptance Criteria

1. THE milestone SHALL add only test files and a coverage/traceability document,
   and SHALL NOT modify any production source file under `internal/` except as
   explicitly authorized by a decision taken against a Requirement 5 Finding
   (D4).
2. THE milestone SHALL add NO database migration, because it enables no new write
   surface; the task list SHALL verify explicitly that no file is added under
   `internal/storage/migrations`, as M10a's, M10.B's, M10.C's, and M10.D's task
   lists did.
3. THE milestone SHALL add NO new REST or admin-API route and SHALL change NO
   `restNotImplemented` registration, because it enables no new write surface.
4. THE milestone SHALL reuse the existing `internal/sanitize` Policy and the
   existing `internal/auth` capability model without modification, asserting their
   behavior rather than altering it.

### Requirement 7: Close-out discipline and no regression

**User Story:** As a maintainer, I want 10.E to follow the same close-out
discipline as every prior M10 milestone, so that the test-coverage close-out
leaves the tree green and introduces no new test-environment surface.

#### Acceptance Criteria

1. THE added tests SHALL be pure Go, SHALL run under the project's SQLite-only
   test path, and SHALL introduce no new environment variable (no new
   `GRIMOIRE_TEST_*`) and no network access, mirroring the close-out discipline
   of M10a, M10.B, M10.C, and M10.D (D4).
2. THE work SHALL follow the standing convention of the previous milestones:
   `gofmt`, `go vet`, `go build ./...`, and `go test ./...` SHALL be green at the
   end of every task.
3. THE existing behavior of every M10 write route SHALL be unchanged by this
   milestone, because 10.E adds tests and a document only; the capability-matrix
   and content-safety tests assert the routes' current contracts (subject to any
   Requirement 5 Finding).

## Out of scope

- Any new feature, REST write route, admin-API route, or capability gate — 10.E
  enables no new write surface (Requirement 6.3).
- Read-path `content.rendered` render/transform tests — already shipped in M10.D
  (`../10.3-rest-content-rendered`, roadmap Requirement 18); not re-done here.
- Roadmap group 10.F (navigation menu editing).
- Roadmap group 10.G (WordPress `$wp$`-format hash issuance and `NeedsRehash`
  upgrade policy).
- Arbitrary plugin-contributed REST fields, meta boxes, and custom blocks
  (roadmap Req 19.1) — no plugin write surface is audited because none is a
  governed M10 route.
- Any production-code change, except as explicitly authorized by a decision taken
  against a Requirement 5 Finding (Requirement 6.1).
- Any database migration (Requirement 6.2 confirms none is needed).
- `DELETE` reinterpretation — delete verbs that are `501` stubs today remain so
  and are recorded as excluded cells (Requirement 1.4), not enabled.

## Traceability

This spec refines **roadmap group 10.E** and traces to roadmap **Requirement
19.2** (`../wordpress-core-parity-roadmap/requirements.md`, the M10
done-definition that every new write endpoint be covered by a capability-matrix
test and a content-safety test before it is considered done): Requirement 1 ↔ the
coverage-matrix deliverable that makes roadmap 19.2 auditable; Requirement 2 ↔
the capability-matrix half of roadmap 19.2 for the M10a post/admin/comment
routes; Requirement 3 ↔ the content-safety half of roadmap 19.2 for "the
pre-existing admin/comment write paths"; Requirement 4 ↔ roadmap 19.2 satisfied
already for the 10.B/10.C media and user routes (inventoried, not duplicated).
The out-of-scope plugin surface area traces to roadmap **Requirement 19.1**, and
the pure-Go/SQLite-only/no-new-env close-out discipline (Requirement 7) mirrors
roadmap **Requirement 19.3**. It depends on M10a (roadmap group 10.A /
Requirement 15, the `internal/sanitize` Policy), M10.B
(`../10.1-rest-media-write`, roadmap group 10.B / Requirement 16), and M10.C
(`../10.2-rest-user-write`, roadmap group 10.C / Requirement 17), whose tests it
inventories and whose patterns it mirrors; it reuses the `internal/auth`
capability model (`auth.CapabilitiesForRoles` / `auth.NewPrincipal`) and the
`internal/sanitize` Policy without modifying either. 10.E is the test-coverage
close-out of the M10 write-parity arc that 10.B–10.D built.
