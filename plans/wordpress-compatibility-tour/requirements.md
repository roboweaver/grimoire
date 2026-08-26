# WordPress Compatibility Visual Tour: Requirements

## Introduction

This documentation-only follow-up gives prospective users a focused visual
comparison of WordPress and Grimoire using the same existing content. It follows
the README and Apache-2.0 refresh specified by commit
`313535333eb43d87dd97ee02397d7bcfdb36a876`; that work must be implemented and
merged before this tour begins, and the two efforts must use separate pull
requests.

This is not milestone M8. The spec commit contains only
`plans/wordpress-compatibility-tour/{requirements,design,tasks}.md`. The later
implementation changes documentation and screenshot artifacts only.

## Requirements

### Requirement 1 — Audience and compatibility framing

**User Story:** As a prospective user, I want an honest visual comparison, so
that I can evaluate Grimoire's WordPress compatibility.

#### Acceptance Criteria

1. THE tour SHALL explain that public parity means the same data, routes,
   publishing state, and semantics, not pixel-identical themes.
2. THE tour SHALL compare equivalent admin workflows while labeling Grimoire's
   Adobe React Spectrum UI as an intentional difference.
3. THE tour SHALL NOT imply complete WordPress theme, plugin, editor, or REST
   parity.

### Requirement 2 — Seven paired sections

**User Story:** As an evaluator, I want representative public, admin, media, and
API comparisons, so that I can assess compatibility quickly.

#### Acceptance Criteria

1. THE tour SHALL contain, in order: Public home, Single published post,
   Category archive, Admin dashboard, Posts list/editor, Media library, and
   Representative REST response.
2. EACH section SHALL show separate WordPress and Grimoire PNGs in a responsive
   two-column Markdown table.
3. EACH section SHALL include concise `What matches`, `Intentional differences`,
   and `Current limits` notes.
4. EACH image SHALL use descriptive alt text and a repository-local path from
   the exact manifest in `design.md`.

### Requirement 3 — Shared capture context

**User Story:** As an evaluator, I want both products shown with the same
content, so that differences are meaningful.

#### Acceptance Criteria

1. DURING local capture, WordPress SHALL run at `http://127.0.0.1:8080` and
   Grimoire SHALL run at `http://127.0.0.1:8081`.
2. BOTH applications SHALL use the same user-selected local MySQL/MariaDB
   database and uploads.
3. THE public pairs SHALL use the same published post and category.
4. ADMIN content lists SHALL be filtered to published content before capture.
5. THE final public documentation SHALL NOT present the shared live database or
   localhost capture arrangement as a production recommendation.

### Requirement 4 — Safe, private capture

**User Story:** As the database owner, I want observation-only screenshots, so
that documentation work does not change content or disclose private data.

#### Acceptance Criteria

1. BEFORE capture, THE operator SHALL verify that no scheduled post is due and
   SHALL NOT run migration, seed, restore, or test-fixture commands.
2. DURING capture, THE operator SHALL NOT invoke create, update, delete, upload,
   attach, restore, autosave, publish, schedule, or settings actions.
3. THE operator SHALL log in manually before capture using an existing account;
   IF interaction is required, THEN capture SHALL pause while the operator
   completes login in the visible browser.
4. LOGIN forms and credentials SHALL NOT be screenshotted, stored, or committed;
   session cookies and tokens SHALL remain in browser storage and SHALL NOT be
   exposed, captured, exported, or committed.
5. BEFORE a screenshot file is written, THE operator SHALL replace visible
   usernames, emails, tokens, filesystem paths, and incidental draft/private
   content in the rendered DOM while keeping the selected published content
   visible.
6. THE capture workflow SHALL write only the sanitized final PNG, never an
   unredacted temporary image.
7. IF an unexpected write, route error, sensitive value, or content mismatch is
   observed, THEN capture SHALL stop and the issue SHALL be reported.

### Requirement 5 — Deterministic images and artifacts

**User Story:** As a reviewer, I want a predictable artifact set, so that every
comparison can be checked.

#### Acceptance Criteria

1. EACH screenshot SHALL use a 1440x900 desktop viewport in light mode.
2. EACH final PNG SHALL be 1440x900 unless a maintainer-approved crop is
   documented beside the affected comparison.
3. THE implementation SHALL create exactly the 14 PNG files listed in
   `design.md` under `docs/images/compatibility/`.
4. THE implementation SHALL NOT add raw captures, alternate formats, thumbnails,
   a marketing banner, external image hosts, or unredacted artifacts.

### Requirement 6 — Honest failure handling

**User Story:** As an evaluator, I want missing surfaces stated plainly, so that
the tour does not fabricate compatibility.

#### Acceptance Criteria

1. BEFORE capture, EACH route SHALL return the expected content and publishing
   state.
2. IF either product lacks a requested surface, THEN the implementation SHALL
   document the limitation instead of substituting or fabricating parity.
3. IF the exact 14-image contract cannot be completed safely, THEN implementation
   SHALL pause for maintainer direction before changing the manifest.

### Requirement 7 — Documentation integration

**User Story:** As a repository visitor, I want current compatibility guidance
and visual evidence to be discoverable.

#### Acceptance Criteria

1. THE implementation SHALL create `docs/wordpress-compatibility-tour.md`.
2. THE implementation SHALL refresh `docs/compatibility.md` from M1/read-only
   claims to accurate M7 behavior and remaining limits.
3. THE two compatibility documents SHALL cross-link.
4. THE root `README.md` SHALL link to both compatibility documents.
5. WHEN complete, THE documentation SHALL NOT describe Grimoire as M1-only or
   globally read-only.

### Requirement 8 — Validation and exclusions

**User Story:** As a maintainer, I want objective checks and a narrow diff, so
that the tour is safe to publish.

#### Acceptance Criteria

1. ALL local Markdown and image links SHALL resolve.
2. THE image directory SHALL contain exactly the 14 expected PNGs at the approved
   dimensions.
3. THE tour SHALL render correctly in GitHub-flavored Markdown.
4. EVERY image SHALL be manually inspected before staging for sensitive data,
   unpublished content, wrong pairing, loading states, and errors.
5. PNG metadata and newly added Markdown SHALL be scanned for localhost capture
   details, credentials, tokens, emails, usernames, and filesystem paths.
6. THE implementation diff SHALL contain only `README.md`,
   `docs/compatibility.md`, `docs/wordpress-compatibility-tour.md`, the 14 PNGs,
   and checklist updates in this spec's `tasks.md`.
7. THE implementation SHALL NOT change application behavior, source code,
   dependencies, configuration, database data, or generated application assets.
