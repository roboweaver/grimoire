# README & Public-Release Metadata Refresh: Requirements

## Introduction

This documentation-only spec prepares grimoire's repository metadata for a
public release. It updates the root README to describe the software that exists
after milestones M1-M7, formally records Apache-2.0 as the project's license,
and brings the plans index into agreement with the implemented state.

This work is not milestone 08. It changes only `README.md`, the canonical
top-level `LICENSE`, and `plans/README.md`; it does not change application
behavior, create brand assets, or add badges.

## Requirements

### Requirement 1 — README identity and icon

**User Story:** As a repository visitor, I want the README to present a clear,
recognizable project identity, so that I can identify grimoire without a large
promotional banner.

#### Acceptance Criteria

1. THE root README SHALL keep `grimoire` as its only H1 and SHALL display the
   existing `assets/icons/icon-64.png` inline with that heading at a rendered
   width between 64 and 72 pixels.
2. THE inline icon SHALL be decorative (`alt=""`) so assistive technology reads
   the project name once rather than repeating it.
3. THE refresh SHALL NOT generate or add a banner, logo variant, badge, or any
   other brand asset.

### Requirement 2 — Accurate current-state summary

**User Story:** As a prospective user, I want the README to state what is
implemented now, so that I do not mistake a working project for an early
scaffold.

#### Acceptance Criteria

1. THE README SHALL include an early current-state block stating that milestones
   M1-M7 are implemented.
2. THE current-state block SHALL mention the working Adobe React Spectrum admin
   and the supported public/admin/WordPress-compatible REST surfaces without
   implying that deferred media or user REST writes are implemented.
3. WHEN the refresh is complete, THEN stale statements including “soon,”
   “current M1,” “M1 (current spec),” “Early scaffold,” and “Adobe React
   Spectrum admin (later milestone)” SHALL NOT remain in the README.

### Requirement 3 — Linked implemented milestones

**User Story:** As a contributor, I want each completed milestone linked from
the README, so that I can move directly from the project summary to its
authoritative specification.

#### Acceptance Criteria

1. THE root README milestone list SHALL contain linked entries for M1, M2, M3,
   M4, M5, M6, and M7.
2. EACH milestone entry SHALL link to its existing directory under `plans/` and
   SHALL carry a `✅` implemented marker.
3. THE milestone summaries SHALL describe the delivered scope rather than the
   scope anticipated before implementation.
4. THE documentation-only `plans/readme-public-release/` directory SHALL NOT be
   added to the numbered milestone list or represented as milestone 08.

### Requirement 4 — Current Goals and Running guidance

**User Story:** As an evaluator, I want the Goals and Running sections to match
the working product, so that I can understand and run the current system
without interpreting obsolete roadmap language.

#### Acceptance Criteria

1. THE Goals section SHALL describe the Adobe React Spectrum admin as a working,
   embedded admin rather than a later milestone.
2. THE Running heading SHALL remove the M1 qualifier and SHALL describe the
   current `grimoire` server and `grimoire-cli` `migrate`, `seed`,
   `createadmin`, and `sessions gc` capabilities.
3. THE existing SQLite, MySQL/MariaDB, PostgreSQL, existing-WordPress-database,
   media uploads, testing, REST API, and extension guidance SHALL remain
   accurate and reachable.

### Requirement 5 — Formal Apache-2.0 adoption

**User Story:** As a user or contributor, I want an explicit canonical license,
so that I can understand the permissions and obligations for using grimoire.

#### Acceptance Criteria

1. THE repository SHALL contain a top-level `LICENSE` whose bytes match the
   canonical Apache License 2.0 text published at
   `https://www.apache.org/licenses/LICENSE-2.0.txt`.
2. THE README SHALL identify the project license as Apache-2.0 and SHALL give a
   concise rationale: broad commercial adoption, an explicit patent grant, no
   incorporation of GPLv2-only WordPress code, and interoperability limited to
   WordPress schema and API behavior.
3. THE licensing language SHALL NOT imply that schema/API interoperability
   grants permission to copy WordPress PHP source into grimoire.

### Requirement 6 — Plans index consistency

**User Story:** As a project contributor, I want the plans index to agree with
the README and license file, so that the repository has one coherent public
release status.

#### Acceptance Criteria

1. WHEN Apache-2.0 is adopted, THEN `plans/README.md` SHALL move licensing from
   Open decisions to a Resolved decisions section and SHALL record the same
   rationale used by the root README.
2. THE status cells for 01, 02, 02.1, 02.2, and 03 SHALL change to
   `✅ Implemented`.
3. THE existing `✅ Implemented` status cells for M4-M7 SHALL remain unchanged.
4. THE plans index SHALL continue to list only numbered milestone specs; this
   documentation-only spec SHALL remain discoverable by its directory path but
   SHALL NOT receive a milestone row.

### Requirement 7 — Documentation-only validation and scope control

**User Story:** As a maintainer, I want objective validation of the refresh, so
that public-release metadata does not ship with broken links, altered license
text, or contradictory status claims.

#### Acceptance Criteria

1. WHEN validation runs, THEN every new or changed local Markdown link in
   `README.md` and `plans/README.md` SHALL resolve to an existing repository
   path, including the inline icon path.
2. WHEN validation compares `LICENSE` with the Apache Software Foundation's
   canonical text, THEN the files SHALL match byte-for-byte.
3. WHEN `README.md` and `plans/README.md` are compared, THEN both SHALL identify
   Apache-2.0 as resolved and M1-M7 as implemented.
4. WHEN the stale-phrase scan runs, THEN it SHALL find none of the obsolete
   status phrases enumerated in Requirement 2.3 and no `📝 Specified` or
   `🚧 In progress` status for milestones through M7.
5. THE implementation diff SHALL be limited to `README.md`, `LICENSE`, and
   `plans/README.md`, with no application, generated asset, or dependency
   changes.
