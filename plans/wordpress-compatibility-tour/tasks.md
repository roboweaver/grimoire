# Tasks — WordPress Compatibility Visual Tour

This is a standalone documentation follow-up after commit
`313535333eb43d87dd97ee02397d7bcfdb36a876` is implemented and merged. It is not
M8 and must use a separate pull request.

## Phase 0 — Approved spec

- [x] 0.1 Create `requirements.md`, `design.md`, and `tasks.md` under
  `plans/wordpress-compatibility-tour/`.
  - _Acceptance:_ The spec covers EARS requirements, exact artifacts, capture
    safety/privacy, failure handling, validation, and traceability.
- [x] 0.2 Keep the spec commit limited to those three files.
  - _Acceptance:_ No documentation, screenshot, application, data, or
    configuration change is included.

## Phase 1 — Prepare and capture

- [ ] 1.1 Verify prerequisites and select content.
  - _Action:_ Confirm the predecessor is merged, the worktree is isolated, and
    choose one existing published post/category shared by both products.
  - _Acceptance:_ No data is created or changed. _(Req 3, 8)_
- [ ] 1.2 Check the shared environment safely.
  - _Action:_ Confirm both products use the same selected local MySQL/MariaDB
    database/uploads and query read-only for due scheduled posts.
  - _Acceptance:_ No due scheduled post exists; no migrate, seed, restore,
    fixture, or test command runs. _(Req 3, 4)_
- [ ] 1.3 Start both local sites and establish admin sessions.
  - _Action:_ Run WordPress on port 8080 and Grimoire on 8081. Log in manually
    with an existing account before capture; if interaction is required, pause
    for the operator.
  - _Acceptance:_ Both sites show the same content; no login form, credential,
    cookie, or token is exposed, captured, exported, or committed. _(Req 3, 4)_
- [ ] 1.4 Normalize and verify all seven surfaces.
  - _Action:_ Set 1440x900 desktop/light mode, use the same post/category, filter
    admin lists to published content, and verify expected routes/content.
  - _Acceptance:_ Every available surface is correct before capture; unavailable
    or mismatched surfaces follow the failure policy. _(Req 2, 3, 5, 6)_
- [ ] 1.5 Capture the seven WordPress/Grimoire pairs.
  - _Action:_ Observe only; DOM-redact sensitive/non-public values before each
    file; write directly to the 14 final filenames in `design.md`.
  - _Acceptance:_ No mutating control is invoked, no unredacted file is written,
    and capture stops on an unexpected write or error. _(Req 4-6)_
- [ ] 1.6 Inspect every image immediately.
  - _Action:_ Review each PNG at full resolution against `design.md`'s checklist.
  - _Acceptance:_ All images are correctly paired, 1440x900 light-mode captures
    with published content and no sensitive data or error state. _(Req 4, 5, 8)_

## Phase 2 — Author and integrate documentation

- [ ] 2.1 Create `docs/wordpress-compatibility-tour.md`.
  - _Acceptance:_ Exactly seven ordered sections use two-column image tables and
    the three required note blocks, with honest limits and descriptive alt text.
    _(Req 1, 2, 6)_
- [ ] 2.2 Refresh `docs/compatibility.md` to current M7 behavior.
  - _Acceptance:_ Stale M1/read-only claims are removed; current coverage, trust
    boundaries, and remaining limits are accurate; the visual tour is linked.
    _(Req 7)_
- [ ] 2.3 Add README links and cross-links.
  - _Acceptance:_ README links both compatibility documents, the tour links the
    guide, and no banner or M8 entry is added. _(Req 7, 8)_

## Phase 3 — Validate and commit the implementation

- [ ] 3.1 Validate artifacts and links.
  - _Action:_ Run the manifest, dimension, and local-link commands in
    `design.md`.
  - _Acceptance:_ Exactly 14 expected PNGs exist at approved dimensions and all
    links resolve. _(Req 5, 8)_
- [ ] 3.2 Validate privacy and current documentation.
  - _Action:_ Run the privacy and stale-language scans, render GitHub Markdown,
    and complete the manual image review.
  - _Acceptance:_ No sensitive/capture-only value, unpublished content, stale
    M1 claim, broken layout, or unsupported parity claim remains. _(Req 1, 4, 7,
    8)_
- [ ] 3.3 Validate scope and commit.
  - _Action:_ Run `git diff HEAD --check`, inspect `git status --short` and
    `git diff HEAD --name-only`, then stage only the approved files.
  - _Acceptance:_ No code, dependency, configuration, data, generated asset,
    external image, raw capture, or unrelated file is included. _(Req 8)_
