# Tasks — README & Public-Release Metadata Refresh

Implementation checklist for the documentation-only public-release metadata
refresh. This spec is not milestone 08 and must not be added to the numbered
milestone index. Implementation changes are limited to `README.md`, `LICENSE`,
and `plans/README.md`; no application behavior, generated brand asset, badge, or
dependency change is permitted.

## Phase 0 — Approved spec

- [x] 0.1 Create `plans/readme-public-release/requirements.md`,
  `design.md`, and `tasks.md` in the repository's Kiro format.
  - _Acceptance:_ Requirements use EARS criteria where appropriate; design
    records exact files, content structure, licensing rationale, security/SEO
    considerations, and validation commands; tasks cover the complete later
    implementation.
- [x] 0.2 Record the approved scope without editing implementation targets.
  - _Acceptance:_ The spec commit contains only the three files in
    `plans/readme-public-release/`; `README.md`, `LICENSE`, and
    `plans/README.md` remain unchanged in the spec commit.

## Phase 1 — Refresh the root README

- [ ] 1.1 Update the H1 to display `assets/icons/icon-64.png` inline at
  `width="68"` with `alt=""`, retaining `grimoire` as the only textual H1.
  - _Acceptance:_ The icon renders at the approved 64-72 pixel size from an
    existing repository path; no banner, generated asset, or badge is added.
  - _Requirements:_ 1.1-1.3.
- [ ] 1.2 Rewrite the opening in present tense and add the early current-state
  block stating that M1-M7 are implemented, including the working embedded
  Spectrum admin and current REST/revision/autosave/scheduler scope.
  - _Acceptance:_ The opening does not claim REST media/user writes and contains
    none of the stale phrases in Requirement 2.3.
  - _Requirements:_ 2.1-2.3.
- [ ] 1.3 Update Goals and rename `Running (M1)` to `Running`; describe the
  operational Spectrum admin, `grimoire` server, and the `grimoire-cli`
  `migrate`, `seed`, `createadmin`, and `sessions gc` capabilities.
  - _Acceptance:_ Existing vendor, external WordPress database, upload,
    testing, REST, and extension instructions remain accurate and linked.
  - _Requirements:_ 4.1-4.3.
- [ ] 1.4 Convert M1-M7 into seven `✅` linked entries using the exact plan
  destinations in `design.md`, with summaries of delivered behavior.
  - _Acceptance:_ Every link resolves; M1-M7 are marked implemented;
    `readme-public-release` is not listed as M8 or as a milestone.
  - _Requirements:_ 3.1-3.4.
- [ ] 1.5 Replace the open-ended licensing note with an Apache-2.0 statement and
  the concise four-point rationale from `design.md`.
  - _Acceptance:_ The README mentions broad commercial adoption, explicit
    patent grant, no GPLv2-only source incorporation, and WordPress schema/API
    interoperability only.
  - _Requirements:_ 5.2-5.3.

## Phase 2 — Adopt the license and reconcile the plans index

- [ ] 2.1 Download the canonical Apache License 2.0 text from
  `https://www.apache.org/licenses/LICENSE-2.0.txt` into top-level `LICENSE`
  without any additions or edits.
  - _Acceptance:_ The canonical-license comparison command in `design.md` exits
    zero with no output.
  - _Requirements:_ 5.1.
- [ ] 2.2 In `plans/README.md`, change rows 01, 02, 02.1, 02.2, and 03 to
  `✅ Implemented`, preserving rows 04-07 as implemented.
  - _Acceptance:_ Every numbered spec through M7 is represented as implemented;
    no milestone row is added for this documentation-only spec.
  - _Requirements:_ 6.2-6.4.
- [ ] 2.3 Move License from Open decisions into a new Resolved decisions section
  and record Apache-2.0 with the same rationale as the root README.
  - _Acceptance:_ The remaining open decisions are unchanged; README and plans
    index agree on the license and rationale.
  - _Requirements:_ 5.2-5.3, 6.1.

## Phase 3 — Validate and commit the later implementation

- [ ] 3.1 Validate all changed local Markdown links and the icon path.
  - _Action:_ Run every path command under `design.md`'s “Paths and Markdown
    links” section, inspect `git diff -- README.md plans/README.md`, and run
    `test -e` for each changed local link target.
  - _Acceptance:_ Every command exits zero; no changed local link is unchecked
    or broken.
  - _Requirements:_ 7.1.
- [ ] 3.2 Validate the canonical Apache-2.0 text.
  - _Action:_ Run the canonical-license comparison command in `design.md`.
  - _Acceptance:_ The command exits zero with no output.
  - _Requirements:_ 5.1, 7.2.
- [ ] 3.3 Validate README/plans consistency and scan for stale status phrases.
  - _Action:_ Run all three commands under `design.md`'s “Status and stale
    language” section and inspect the M1-M7 entries in both files.
  - _Acceptance:_ Stale-phrase and incomplete-status scans produce no output;
    `Apache-2.0` is found in both files; M1-M7 are implemented in both views.
  - _Requirements:_ 7.3-7.4.
- [ ] 3.4 Validate scope and Markdown hygiene.
  - _Action:_ Run `git diff --check`, `git status --short`, and
    `git diff --name-only`.
  - _Acceptance:_ There are no whitespace errors and exactly `README.md`,
    `LICENSE`, and `plans/README.md` are changed; no application, asset, or
    dependency file appears.
  - _Requirements:_ 7.5.
- [ ] 3.5 Commit the later implementation as one documentation change.
  - _Action:_ Stage only `README.md`, `LICENSE`, and `plans/README.md`, then
    commit with the approved implementation message and the standard
    `Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`
    trailer selected at implementation time.
  - _Acceptance:_ `git status --short` is clean after the commit and the commit
    contains only the three implementation files.
