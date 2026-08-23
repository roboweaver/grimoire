# Requirements — M2.2: WordPress-faithful excerpts (fix escaped HTML in list views)

## Introduction

M1 renders post/page content on list views (home/index, category, archive) and
single/page views. A defect surfaced **live during the M2 demo** against a
restored real WordPress database: on **list views** grimoire displays a post's
excerpt as **literal text**. The newest real post renders

```
<p>Run multiple GitHub accounts on one machine: route each r…
```

verbatim on the homepage instead of a rendered paragraph.

### Root cause

`internal/web/view.go` (`postView`) maps the raw WordPress `post_excerpt` string
into `render.PostView.Excerpt`, which is typed **`string`** (`internal/render/view.go`).
The list templates emit it with `{{.Excerpt}}`
(`themes/default/templates/index.tmpl`, `category.tmpl`, `archive.tmpl`), so Go's
`html/template` **auto-escapes** any HTML in it → `<p>` becomes the visible text
`&lt;p&gt;`. Single/page views are unaffected because `PostView.Content` is
`template.HTML` and renders raw (trusted DB HTML — see the trust-boundary note in
`internal/web/view.go`).

### Real-DB evidence

MariaDB `wordpress`, prefix `accuweaver`, read-only. The newest post's
`post_excerpt` is a 154-char string beginning `<p>Run multiple GitHub accounts…`
(contains HTML); **most other posts have an empty `post_excerpt`** (length 0), for
which grimoire currently renders **nothing** on list views.

### What WordPress does — `the_excerpt()`

1. **Manual excerpt** (`post_excerpt` non-empty): WordPress runs it through
   `wpautop` and renders it **as HTML**, not escaped. A manual excerpt containing
   `<p>…</p>` renders as a real paragraph.
2. **Auto excerpt** (`post_excerpt` empty): WordPress generates one from
   `post_content` via `wp_trim_excerpt` — strip shortcodes, strip HTML tags, trim
   to ~55 words, and append an ellipsis (`[…]` in WP).
3. Gutenberg block-delimiter comments (`<!-- wp:… -->` / `<!-- /wp:… -->`) must not
   leak into a content-derived excerpt.

M2.2 closes this gap: excerpts render WordPress-faithfully on list views, empty
excerpts are auto-generated from content, and the trusted-content boundary is
extended to cover the excerpt.

### Out of scope (unchanged from M1/M2)

Admin/write paths, comments, media, the REST API, and the separate
`grimoire-cli migrate` greenfield-only limitation (0002 `ALTER TABLE users` lacks
`IF NOT EXISTS`, so it cannot overlay an existing WP DB) — that is tracked
independently and is **not** addressed here. M2.2 introduces **no untrusted-content
path**.

## Requirements

### Requirement 1 — Manual excerpts render as HTML on list views

**User Story:** As a reader, I want a post's manually-authored excerpt to render
as formatted HTML on the homepage and archives, so that I see a real summary
paragraph instead of raw `<p>` markup as text.

#### Acceptance Criteria
1. WHEN a post has a non-empty `post_excerpt`, THE system SHALL render that excerpt as **HTML** on list views (index, category, archive) — HTML tags in the excerpt SHALL be interpreted, not escaped to visible `&lt;…&gt;` text.
2. WHEN a manual excerpt contains block markup such as `<p>…</p>`, THE rendered list view SHALL contain the literal substring `<p>…</p>` (rendered element) AND SHALL NOT contain the escaped form `&lt;p&gt;`.
3. THE single/page view rendering of `Content` SHALL remain unchanged.

### Requirement 2 — Auto-generated excerpt when `post_excerpt` is empty

**User Story:** As a reader, I want a useful summary even when the author wrote no
excerpt, so that list views are not blank for the majority of real posts.

#### Acceptance Criteria
1. WHEN a post's `post_excerpt` is empty (or whitespace-only), THE system SHALL derive an excerpt from `post_content`.
2. WHEN deriving from `post_content`, THE system SHALL strip shortcodes (`[…]`), strip HTML tags, and collapse runs of whitespace to single spaces, producing a plain-text summary.
3. WHEN the derived plain-text summary exceeds ~55 words, THE system SHALL truncate it to 55 words AND append a single ellipsis `…`.
4. WHEN the derived plain-text summary is 55 words or fewer, THE system SHALL NOT truncate it AND SHALL NOT append a trailing `…`.
5. THE auto-generated summary SHALL be wrapped in a single `<p>…</p>` element (minimal `wpautop`) so it renders as a paragraph consistent with WordPress.

### Requirement 3 — Strip Gutenberg block-delimiter comments

**User Story:** As a reader, I want block-editor plumbing hidden, so that
`<!-- wp:paragraph -->` scaffolding never appears in a summary.

#### Acceptance Criteria
1. WHEN deriving an excerpt from `post_content` that contains Gutenberg block comments (`<!-- wp:… -->` and `<!-- /wp:… -->`), THE system SHALL remove those comments before computing the summary.
2. THE resulting summary SHALL contain neither the block-comment markers nor the literal token `wp:`.

### Requirement 4 — Empty content and empty excerpt yields empty output

**User Story:** As a maintainer, I want the empty case handled cleanly, so that a
post with neither an excerpt nor content produces no stray markup and never panics.

#### Acceptance Criteria
1. WHEN a post has an empty `post_excerpt` AND empty `post_content`, THE system SHALL produce an empty excerpt (empty string) — NOT a stray `<p></p>`.
2. THE derivation SHALL NOT panic on empty, whitespace-only, tags-only, or comment-only input.

### Requirement 5 — Extend the trusted-content boundary to the excerpt

**User Story:** As a maintainer, I want the excerpt's raw-HTML rendering to carry
the same documented trust boundary as `Content`, so that a future untrusted/admin
write path cannot silently emit unsanitized excerpt HTML.

#### Acceptance Criteria
1. WHEN `render.PostView.Excerpt` is emitted as raw HTML, THE cast to `template.HTML` SHALL occur at the web view boundary (`internal/web/view.go`) alongside the `Content` cast, under a single `TRUST BOUNDARY` comment covering both fields.
2. THE derivation helper SHALL NOT import `html/template`; the trust decision SHALL live solely at the web boundary.
3. `docs/compatibility.md` SHALL document the excerpt semantics AND record that the trusted-content boundary now covers `Excerpt`, requiring sanitization (e.g. bluemonday) before the cast on any future untrusted/write path.

### Requirement 6 — Quality gate & compatibility

**User Story:** As a maintainer, I want M2.2 to uphold M1/M2/M2.1's engineering
guarantees, so that the fix is safe and portable.

#### Acceptance Criteria
1. THE change SHALL keep `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` succeeding, and `go test -count=1 ./...` green (SQLite unconditional; MySQL/Postgres and real-DB tests gated on their env vars and skipping without them).
2. THE work SHALL follow strict TDD: a failing test precedes each behavior change, including a template-level test proving list views no longer escape a manual HTML excerpt (reverting the fix SHALL make that test fail).
3. THE excerpt-derivation helper SHALL remain free of database-driver imports AND SHALL NOT hardcode real database DSNs or password hashes.
4. THE change SHALL introduce no untrusted-content path and SHALL preserve M1's read-only security posture.
