# README & Public-Release Metadata Refresh: Design

## Overview

The refresh is a coordinated edit of three public-release metadata surfaces:

| File | Responsibility |
|------|----------------|
| `README.md` | Project identity, current capabilities, running guidance, milestone links, and concise license rationale |
| `LICENSE` | Canonical, unmodified Apache License 2.0 legal text |
| `plans/README.md` | Authoritative milestone-status table and resolved/open project decisions |

`plans/readme-public-release/` specifies the work but is not itself a numbered
milestone. No Go, TypeScript, configuration, generated frontend output, brand
asset, or dependency file participates in the implementation.

## README structure

### H1 and existing icon

Use the existing 64-pixel repository icon directly in the H1:

```markdown
# <img src="./assets/icons/icon-64.png" alt="" width="68"> grimoire
```

The 68-pixel width is inside the approved 64-72 pixel range. An empty alt value
makes the image decorative, while the adjacent `grimoire` text remains the
accessible heading. Do not create a banner, regenerate an icon, or add badges.

### Opening and current-state block

Replace the future-tense opening description with present-tense language. Place
an early block after the short project explanation and before Goals:

```markdown
> **Current state:** Milestones M1-M7 are implemented, including the embedded
> Adobe React Spectrum admin, WordPress-compatible content/auth/REST surfaces,
> revisions and autosave, and scheduled publishing.
```

The surrounding prose may be tightened during implementation, but it must not
claim REST media or user write parity; those writes remain deferred.

### Goals and Running

Update the Goals bullet for the admin to say that the Spectrum SPA is embedded
in the Go binary and operational. Rename `## Running (M1)` to `## Running`.
Describe both current binaries:

- `grimoire`: HTTP server, public rendering, admin, and REST endpoints.
- `grimoire-cli`: `migrate`, `seed`, `createadmin`, and `sessions gc`
  operational commands.

Preserve the vendor-specific and existing-database instructions unless a
sentence must change to remove stale milestone framing.

### Milestones

Keep seven top-level entries and turn each label into a repository-relative
link:

| Entry | Link |
|-------|------|
| M1 | `./plans/01-content-core-read-rendering` |
| M2 | `./plans/02-users-auth-roles` |
| M3 | `./plans/03-spectrum-admin` |
| M4 | `./plans/04-comments-media-menus` |
| M5 | `./plans/05-extensions-rest-api` |
| M6 | `./plans/06-admin-crud-editor` |
| M7 | `./plans/07-revisions-scheduler` |

Every entry begins with `✅` and summarizes delivered behavior in the present
tense. M2.1 and M2.2 remain represented in the detailed plans index; they do
not need new top-level entries in the root README.

## Apache-2.0 adoption

Create `LICENSE` by copying the canonical text from
`https://www.apache.org/licenses/LICENSE-2.0.txt` without adding a project
header, copyright line, or commentary. Put rationale in documentation rather
than modifying the legal text.

The root README's licensing note should be concise and cover four points:

1. Apache-2.0 supports broad commercial and open-source adoption.
2. Apache-2.0 provides an explicit patent grant.
3. grimoire does not incorporate GPLv2-only WordPress PHP code.
4. WordPress compatibility is schema/API interoperability, not source-code
   incorporation.

This rationale explains the choice without attempting to provide legal advice.

## Plans index changes

In `plans/README.md`:

1. Change rows 01, 02, 02.1, 02.2, and 03 to `✅ Implemented`; preserve the
   implemented status of rows 04-07.
2. Remove License from `## Open decisions`.
3. Add a `## Resolved decisions` section before Open decisions and record
   Apache-2.0 with the same four-part rationale as the README.
4. Keep the remaining open decisions unchanged.
5. Do not add `readme-public-release` to the milestone table.

## Security Considerations

- The README must not present deferred REST writes as available, because
  overstated interfaces can lead integrators to design against nonexistent
  behavior.
- Local links use repository-relative paths and no new external image host,
  script, tracking pixel, or badge service.
- The license rationale distinguishes interoperability from code
  incorporation; it does not authorize copying GPLv2-only source.
- The implementation changes documentation and legal metadata only and cannot
  alter authentication, authorization, runtime, or dependency behavior.

## SEO Considerations

- Retain one textual H1 (`grimoire`); the decorative inline icon does not add a
  competing heading or duplicate accessible name.
- Use direct, present-tense capability language near the beginning so repository
  and search previews do not surface obsolete “soon” or scaffold descriptions.
- Do not add a generated hero image or badge row that displaces the descriptive
  opening text.
- Keep existing README section anchors stable where practical; only the stale
  `Running (M1)` heading intentionally becomes `Running`.

## Validation strategy

### Paths and Markdown links

Verify the icon and every milestone destination explicitly:

```bash
test -f assets/icons/icon-64.png
test -d plans/01-content-core-read-rendering
test -d plans/02-users-auth-roles
test -d plans/03-spectrum-admin
test -d plans/04-comments-media-menus
test -d plans/05-extensions-rest-api
test -d plans/06-admin-crud-editor
test -d plans/07-revisions-scheduler
```

Expected result: every command exits zero. Inspect all changed Markdown links in
`git diff -- README.md plans/README.md` and run the equivalent `test -e` check
for each local target.

### Canonical license text

```bash
curl -fsSL https://www.apache.org/licenses/LICENSE-2.0.txt | cmp - LICENSE
```

Expected result: exit zero and no output.

### Status and stale language

```bash
rg -n -i 'soon|current M1|M1 \(current spec\)|Early scaffold|Adobe React Spectrum admin \(later milestone\)|License selection is an open decision' README.md plans/README.md
rg -n '📝 Specified|🚧 In progress' plans/README.md
rg -n 'Apache-2.0' README.md plans/README.md
```

Expected result: the first two commands produce no output; the third finds the
resolved license in both files.

### Scope and Markdown hygiene

```bash
git diff --check
git status --short
git diff --name-only
```

Expected result: no whitespace errors, and the implementation changes exactly
`README.md`, `LICENSE`, and `plans/README.md`.

## Traceability

| Requirement | Design section |
|-------------|----------------|
| 1 | README structure / H1 and existing icon |
| 2 | README structure / Opening and current-state block |
| 3 | README structure / Milestones |
| 4 | README structure / Goals and Running |
| 5 | Apache-2.0 adoption |
| 6 | Plans index changes |
| 7 | Validation strategy; Security Considerations; SEO Considerations |
