# WordPress Compatibility Visual Tour: Design

## Overview

The tour is a documentation-only comparison for prospective users. WordPress and
Grimoire are observed against the same existing local MySQL/MariaDB database and
uploads. Public comparisons emphasize data, route, and publishing semantics;
admin comparisons emphasize equivalent workflows and explicitly label UI
differences.

This follow-up starts only after the README/Apache-2.0 refresh from commit
`313535333eb43d87dd97ee02397d7bcfdb36a876` is implemented and merged. It is a
separate pull request and is not M8.

## Files

| Path | Responsibility |
|------|----------------|
| `docs/wordpress-compatibility-tour.md` | Seven paired comparisons and concise parity notes |
| `docs/images/compatibility/*.png` | Exact sanitized screenshot manifest below |
| `docs/compatibility.md` | Current M7 compatibility guide and remaining limits |
| `README.md` | Links to both compatibility documents |
| `plans/wordpress-compatibility-tour/tasks.md` | Implementation checklist tracking |

No application, configuration, dependency, test-data, or generated-app file is
part of this work.

## Tour structure

Open with a short audience statement, define behavioral rather than pixel parity,
state that both products use the same existing content during local capture, and
link to `./compatibility.md`.

Use this Markdown shape for every section:

```markdown
## Public home

| WordPress | Grimoire |
|---|---|
| ![WordPress public home showing published posts](./images/compatibility/public-home-wordpress.png) | ![Grimoire public home showing the same published posts](./images/compatibility/public-home-grimoire.png) |

**What matches:** Both views use the same published records and routes.

**Intentional differences:** The products use different themes.

**Current limits:** This pair does not claim plugin or theme-rendering parity.
```

### Content outline

| Section | What to demonstrate | Intentional difference / limit |
|---------|---------------------|--------------------------------|
| Public home | Same published set, order, titles, excerpts, and links | Different themes; no pixel-parity claim |
| Single published post | Same slug, title, body, date, and categories | Typography and theme chrome differ; plugin rendering is not implied |
| Category archive | Same category slug, membership, order, and post links | Archive styling and controls may differ |
| Admin dashboard | Equivalent entry points for managing the same site | WordPress admin and Spectrum use different navigation and widgets |
| Posts list/editor | Published list and entry point to the same post's editor | Controls differ; capture does not demonstrate saving, autosave, or Gutenberg parity |
| Media library | Same existing attachments and uploads | Layout and metadata presentation differ; no upload or REST-write claim |
| Representative REST response | Same selected published post from `GET /wp-json/wp/v2/posts?slug=<selected-slug>&status=publish` | Key order and optional fields may differ; one GET is not total REST parity |

If a current UI lacks a safe published-only filter, remove incidental
non-published rows from the in-memory DOM for privacy and state that UI
limitation under `Current limits`; do not describe DOM redaction as product
parity.

## Exact screenshot manifest

| Section | WordPress | Grimoire |
|---------|-----------|----------|
| Public home | `public-home-wordpress.png` | `public-home-grimoire.png` |
| Single published post | `single-published-post-wordpress.png` | `single-published-post-grimoire.png` |
| Category archive | `category-archive-wordpress.png` | `category-archive-grimoire.png` |
| Admin dashboard | `admin-dashboard-wordpress.png` | `admin-dashboard-grimoire.png` |
| Posts list/editor | `posts-list-editor-wordpress.png` | `posts-list-editor-grimoire.png` |
| Media library | `media-library-wordpress.png` | `media-library-grimoire.png` |
| Representative REST response | `rest-response-wordpress.png` | `rest-response-grimoire.png` |

All files live in `docs/images/compatibility/`. Do not create contact sheets,
raw captures, redacted-copy duplicates, thumbnails, or alternate formats.

## Capture workflow

1. **Preflight**
   - Confirm the predecessor refresh is merged and the worktree has no unrelated
     changes.
   - Select one existing published post and one category containing it.
   - Query the shared database read-only for due `future` posts. If any scheduled
     publish time is due or will occur during the capture session, stop and
     report it without exposing private content.
   - Do not run migrate, seed, restore, imports, fixtures, or tests against the
     selected database/uploads.
2. **Start the local comparison**
   - Run WordPress at `http://127.0.0.1:8080` and Grimoire at
     `http://127.0.0.1:8081`.
   - Point both to the same user-selected local MySQL/MariaDB database and
     uploads. This is capture context only, never production guidance.
3. **Establish admin sessions**
   - Log in manually before capture with an existing account.
   - If operator interaction is needed, pause and ask the operator to complete
     login in the visible browser.
   - Never screenshot or store the login form or credentials. Keep session
     cookies/tokens in browser storage; never expose, capture, export, or commit
     them.
4. **Normalize**
   - Set a 1440x900 desktop viewport, light mode, consistent zoom, and fully
     loaded local assets.
   - Use the same published post/category on both products.
   - Filter admin content lists to published content.
5. **Verify each surface**
   - Confirm expected status, content type, selected content, and publishing
     state before taking a screenshot.
   - If a route is missing, errors, or shows mismatched content, stop that pair
     and document the limitation. Do not substitute another route.
6. **Observe only**
   - Do not click or invoke create, update, delete, upload, attach, restore,
     autosave, publish, schedule, bulk-action, or settings controls.
   - If an unexpected write or state change appears in the browser, application
     logs, database, or uploads, stop capture and report it.
7. **Redact before writing**
   - In the rendered DOM, replace usernames, emails, tokens, filesystem paths,
     and incidental draft/private content with neutral values.
   - Keep the selected published content visible.
   - Inspect the viewport, including account menus, form values, titles, and
     accessible labels.
   - Write directly to the final sanitized filename; never write an unredacted
     temporary PNG.
8. **Inspect immediately**
   - Open each final PNG at full resolution and verify product, route, pairing,
     dimensions, light mode, published-only content, complete rendering, and
     absence of sensitive data.

## Compatibility guide refresh

Rewrite `docs/compatibility.md` around current M7 behavior:

- WordPress schema/API interoperability, not PHP source reuse.
- Current public posts/pages/categories/archives/excerpts/uploads.
- Users, sessions, roles, and capabilities.
- Embedded Spectrum admin; comments, media, and menus.
- Extension hooks and supported WordPress-compatible REST reads/writes.
- Admin post/page CRUD, revisions, autosave, scheduled publishing, and REST term
  parity.
- Remaining limits, including deferred media/user REST writes and no PHP
  plugin/theme execution.
- Updated trusted-content-boundary guidance that acknowledges authenticated
  write paths.
- A link to `./wordpress-compatibility-tour.md`.

Remove stale statements such as `WordPress Compatibility (M1)`, `M1 is strictly
read-only`, and claims that the server never writes to content tables.

Add concise root README links to `./docs/compatibility.md` and
`./docs/wordpress-compatibility-tour.md` without adding a banner or M8 entry.

## Failure handling

| Condition | Response |
|-----------|----------|
| Predecessor not merged | Do not begin this follow-up |
| Login required | Pause and ask the operator to log in visibly; do not capture the login |
| Due scheduled post | Stop capture and report the post identifier/time |
| Unexpected write | Stop both captures and report; do not continue |
| Route error or mismatched content | Do not capture that pair; document the limit |
| Redaction cannot be completed before file creation | Do not write the image |
| Required surface unavailable | Do not fabricate parity; pause before changing the manifest |

## Security and privacy

- The shared live local database/uploads are observation-only during capture.
- Published content stays visible; incidental non-public and operator data does
  not.
- Redaction happens in the DOM before the only image file is written.
- PNGs contain no text/EXIF metadata, credentials, localhost capture details, or
  local paths.
- Images are repository-local and use no external host or tracking.

## Validation

Run from the repository root after implementation.

### Manifest and dimensions

```bash
python3 - <<'PY'
from pathlib import Path
import struct

root = Path("docs/images/compatibility")
expected = {
    f"{stem}-{product}.png"
    for stem in ("public-home", "single-published-post", "category-archive",
                 "admin-dashboard", "posts-list-editor", "media-library",
                 "rest-response")
    for product in ("wordpress", "grimoire")
}
paths = list(root.glob("*"))
assert {path.name for path in paths} == expected
for path in sorted(paths):
    data = path.read_bytes()
    assert data[:8] == b"\x89PNG\r\n\x1a\n", path
    width, height = struct.unpack(">II", data[16:24])
    assert (width, height) == (1440, 900), (path, width, height)
    offset = 8
    while offset < len(data):
        size = struct.unpack(">I", data[offset:offset + 4])[0]
        kind = data[offset + 4:offset + 8]
        assert kind not in {b"tEXt", b"zTXt", b"iTXt", b"eXIf"}, (path, kind)
        offset += size + 12
PY
```

If a crop is approved, replace the strict dimension assertion for that named
file with its documented approved dimensions before implementation is staged.

### Local links

```bash
python3 - <<'PY'
from pathlib import Path
import re

for source in map(Path, ["README.md", "docs/compatibility.md", "docs/wordpress-compatibility-tour.md"]):
    for target in re.findall(r"!?\[[^\]]*\]\(([^)#]+)", source.read_text()):
        if not target.startswith(("http://", "https://", "mailto:")):
            assert (source.parent / target).resolve().exists(), (source, target)
PY
```

### Privacy, current language, and scope

```bash
python3 - <<'PY'
from pathlib import Path
import re
import subprocess

text = Path("docs/wordpress-compatibility-tour.md").read_text()
diff = subprocess.check_output(
    ["git", "diff", "HEAD", "--unified=0", "--", "README.md", "docs/compatibility.md"],
    text=True,
)
text += "\n".join(
    line[1:] for line in diff.splitlines()
    if line.startswith("+") and not line.startswith("+++")
)
patterns = [
    r"127\.0\.0\.1", r"\blocalhost\b", r"(?:/Users/|/home/)[^\s)]+",
    r"\b[A-Z]:\\[^\s)]+", r"\b[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}\b",
    r"\bBearer\s+\S+", r"\b(?:username|email|token|password)\s*[:=]\s*\S+",
]
for pattern in patterns:
    assert not re.search(pattern, text, re.IGNORECASE), pattern
PY
if rg -n -i 'M1 is strictly read-only|WordPress Compatibility \(M1\)|server never writes' docs/compatibility.md; then exit 1; fi
git diff HEAD --check
git status --short
git diff HEAD --name-only
```

Expected implementation scope is exactly `README.md`, the two compatibility
Markdown files, the 14 manifest PNGs, and
`plans/wordpress-compatibility-tour/tasks.md`.

### Manual review

Before staging:

1. Render the tour with GitHub-flavored Markdown at wide and narrow widths.
2. Inspect every PNG at full resolution for pairing, dimensions, metadata,
   unpublished content, credentials, usernames, emails, tokens, paths, login UI,
   loading states, and errors.
3. Confirm every `What matches` statement is supported by the images or a
   read-only route check.
4. Confirm the compatibility guide describes current M7 behavior.

## Traceability

| Requirement | Design |
|-------------|--------|
| 1 | Overview; Tour structure |
| 2 | Tour structure; Content outline |
| 3 | Capture workflow 1-5 |
| 4 | Capture workflow 3, 6-8; Security and privacy |
| 5 | Exact screenshot manifest; Capture workflow 4, 7-8 |
| 6 | Capture workflow 5-6; Failure handling |
| 7 | Compatibility guide refresh |
| 8 | Files; Validation; Security and privacy |
