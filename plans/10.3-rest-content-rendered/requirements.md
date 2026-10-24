# Requirements Document

M10.D — REST `content.rendered` Improvements

## Introduction

M10a shipped the write-boundary content policy (`internal/sanitize`), M10.B
(spec `../10.1-rest-media-write`) turned on the first REST write surface over
it, and M10.C (spec `../10.2-rest-user-write`) added REST user writes in the
same shape. This milestone (10.D) is a different kind of work. It is the first
**read-path** milestone in M10, and the first requirement anywhere in grimoire
that **transforms** the REST `content.rendered` string rather than passing a
stored value straight through.

The write-parity siblings all share one shape: a capability-checked write
service over domain ports, a write boundary, the Policy applied before
persistence, WordPress-faithful REST error codes, and a nil-gated builder.
**None of that applies here.** 10.D adds no new write surface, no capability
gate, no write boundary, and no new domain port. It touches exactly one field
on the way **out** — REST `content.rendered` — to make it look like
WordPress's own rendered output: Gutenberg block-comment delimiters stripped,
and media-library `<img>` tags given responsive and lazy-loading markup. Stored
`post_content` is never rewritten, and the public-facing HTML templates
(`internal/render`, `themes/default/templates/*.tmpl`) are not modified, so no
SEO behavior changes.

Today, `content.rendered` is produced at a single point.
`(*RESTMapper).restCommon` (`internal/content/rest.go`, line ~258) sets
`Content: RESTContentRendered{Rendered: p.Content, Protected: p.Password != ""}`,
passing the raw stored `p.Content` through with no transform. Both `RESTPost`
and `RESTPage` flow through `restCommon`, so that one assignment is the single
transform site this milestone changes. 10.D inserts a render step there and
nowhere else.

The pieces 10.D needs already exist in the tree. A battle-tested Gutenberg
block-comment stripper lives in `internal/content/excerpt.go` —
`blockCommentRE = regexp.MustCompile(`(?s)<!--\s*/?\s*wp:.*?-->`)`, used by
`Excerpt` — and 10.D reuses that exact pattern (factored to a shared helper)
rather than inventing a second one. The mapper already reaches attachment
metadata: `(*RESTMapper).Media` calls
`m.meta.AttachmentMetadata(ctx, media.ID)`
(`domain.PostMetaRepository.AttachmentMetadata`, which returns the raw
PHP-serialized `_wp_attachment_metadata`, `""` when unset —
`internal/storage/wprepo/postmeta.go`), and `mediaDetailsFromSerialized`
(`internal/content/rest.go`) already decodes that blob for width/height. The
same blob carries a `sizes` sub-map (keyed by size name — thumbnail, medium,
large, … — each with a `file` basename, `width`, and `height`) and a top-level
`file` (e.g. `2024/03/photo.jpg`), which is what `srcset` candidates are built
from. The uploads URL convention is `URL = TrimRight(BaseURL,"/") + "/" +
relPath`, `BaseURL` defaulting to `/wp-content/uploads` (`internal/content/media.go`),
so a size-variant URL is the attachment URL's directory plus the size `file`
basename. `golang.org/x/net/html` is already a module dependency (used by
`internal/sanitize`), available for HTML-aware `<img>` rewriting.

This refines **roadmap group 10.D only**, tracing to roadmap **Requirement 18**
(`../wordpress-core-parity-roadmap/requirements.md`, "`content.rendered`
Improvements"), and folds in roadmap **Requirement 19.1** (arbitrary plugin
REST fields / meta boxes / custom blocks out of scope) and roadmap
**Requirement 19.2/19.3** (per-endpoint test discipline) as a testability
requirement. Roadmap groups 10.E (M10 test coverage), 10.F (menu editing), and
10.G (WordPress `$wp$` hash issuance) remain open and are out of scope (see
"Out of scope").

## Glossary

- **The Transform** — the single `content.rendered` rendering step this
  milestone adds: block-comment stripping plus responsive-image rewriting,
  applied to the `rendered` string inside `(*RESTMapper).restCommon`
  (`internal/content/rest.go`). It is a pure string→string function of the input
  rendered HTML and the available attachment metadata; it performs no write.
- **Block-comment stripping** — removal of Gutenberg block-delimiter comments
  (`<!-- wp:paragraph -->`, `<!-- /wp:paragraph -->`, and every `wp:` variant)
  from the rendered HTML, using the existing `blockCommentRE` pattern from
  `internal/content/excerpt.go` (D2).
- **Responsive-image rewriting** — HTML-aware injection of `loading="lazy"` and
  `decoding="async"` on media-library `<img>` tags, plus `srcset`/`sizes` when
  size variants are resolvable, matching WordPress's own
  `wp_filter_content_tags` output (Req 18.2).
- **`blockCommentRE`** — the existing
  `regexp.MustCompile(`(?s)<!--\s*/?\s*wp:.*?-->`)` in
  `internal/content/excerpt.go`, non-greedy and dot-matches-newline, already
  used by `Excerpt`. 10.D factors it into a shared helper and reuses it.
- **`restCommon`** — `(*RESTMapper).restCommon` (`internal/content/rest.go`,
  line ~258), the single builder of `restItemCommon` through which both
  `RESTPost` (`(*RESTMapper).Post`) and `RESTPage` (`(*RESTMapper).Page`) flow.
  It is the one site where `content.rendered` is assigned, and therefore the one
  site The Transform is inserted.
- **`RESTContentRendered`** — the content sub-object shape
  `{Rendered string; Protected bool}` (`internal/content/rest.go`). 10.D changes
  only the `Rendered` string; `Protected` and every other emitted field are
  untouched.
- **`_wp_attachment_metadata`** — the PHP-serialized attachment-metadata blob
  returned raw by `domain.PostMetaRepository.AttachmentMetadata`
  (`internal/storage/wprepo/postmeta.go`), `""` when unset. It carries a
  top-level `file`, width/height, and a `sizes` sub-map of size-name → `{file,
  width, height}`.
- **Size variants** — the entries of the `_wp_attachment_metadata` `sizes`
  sub-map. Each yields a `srcset` candidate: the attachment URL's directory +
  the size's `file` basename + a width descriptor (`{width}w`).
- **The `wp-image-{id}` convention** — WordPress tags a content image with
  `class="wp-image-{id}"`, naming the attachment the `<img>` refers to. 10.D
  resolves an `<img>` to its attachment id through this class only (D4).

## Inherited decisions

Settled by the spec owner and treated here as fixed input, not open questions:

| # | Decision | Consequence |
|---|---|---|
| D1 | **Single transform point, REST response only.** The Transform is one render step in `restCommon` (covering `RESTPost` + `RESTPage`), applied to the REST `content.rendered` string only. Stored `post_content` and the public HTML templates (`internal/render`, `themes/default/templates/*.tmpl`) are untouched (roadmap Req 18.1, 18.3). | One site changes (`internal/content/rest.go` line ~258); no storage write, no template edit, no SEO regression. Comments' and media's `rendered` fields and the excerpt/title fields are not routed through The Transform (Requirement 4). |
| D2 | **Reuse the existing block-comment regex.** Block-comment stripping reuses `blockCommentRE` from `internal/content/excerpt.go`, factored into a shared helper, not a second regex. | `Excerpt` and The Transform share one definition of "Gutenberg block comment"; a single place to change the pattern. |
| D3 | **HTML-aware image rewriting.** The `<img>` enhancement parses and rewrites the HTML (via the `golang.org/x/net/html` the `internal/sanitize` package already uses), never a fragile regex, and must not corrupt surrounding markup. | The design pins the parse/serialize approach; Requirement 5 forbids markup corruption and requires idempotence. |
| D4 | **Attachment resolution via `class="wp-image-{id}"`.** An `<img>` is matched to its attachment through the `wp-image-{id}` class convention only. An `<img>` with no resolvable `wp-image-{id}`, or whose attachment has no size variants, gets `loading="lazy"`/`decoding="async"` only — never a wrong `srcset`/`sizes` (roadmap Req 18.2). | No guessing of attachment identity from `src`; graceful degradation to lazy/decoding-only (Requirement 2, Requirement 3). |
| D5 | **Size variants from the `sizes` sub-array.** `srcset` candidates come from parsing the `sizes` sub-map of `_wp_attachment_metadata` (via a new sibling of `mediaDetailsFromSerialized`); each `srcset` URL is the attachment URL's directory + the size's `file` basename + a `{width}w` descriptor; the `sizes` attribute uses WordPress's default `(max-width: {w}px) 100vw, {w}px` form (the design pins the exact string). | The metadata decoder gains a `sizes` reader; the mapper builds candidate URLs from the uploads convention (`internal/content/media.go`). |
| D6 | **Nil-safe / opt-in / additive.** If the content-render dependency (attachment-metadata access) is not wired, The Transform falls back to block-comment-stripping only — it never panics. A post with no Gutenberg comments and no `<img>` tags renders byte-identically to today. | Requirement 6's fallback; Requirement 4's byte-identical invariant for comment-free, image-free content. |
| D7 | **Scope guard.** The Transform does delimiter stripping + image attributes only (no block re-rendering, roadmap Req 18.4). It applies to posts and pages only; comment `content.rendered` (HTML-escaped) and media fields are untouched. The Transform is idempotent: applying it to already-rendered content is a no-op (stripping removes nothing new; re-adding `loading`/`decoding`/`srcset` to an `<img>` that already has them does not duplicate them). | Requirement 5 (idempotence), Requirement 7 (out-of-scope exclusions), roadmap Req 18.4 / 19.1 traceability. |

## Requirements

### Requirement 1: Strip Gutenberg block-comment delimiters from `content.rendered`

**User Story:** As a REST API consumer reading `content.rendered` from a
grimoire site, I want Gutenberg block-comment delimiters removed, so that the
rendered HTML matches what WordPress's own REST response returns.

#### Acceptance Criteria

1. WHEN a post's or page's stored `post_content` contains Gutenberg
   block-comment delimiters (`<!-- wp:paragraph -->`, `<!-- /wp:paragraph -->`,
   and every `wp:` variant) THE Transform SHALL remove those delimiters from the
   REST `content.rendered` string, satisfying roadmap Req 18.1.
2. THE Transform SHALL perform block-comment stripping using the existing
   `blockCommentRE` pattern from `internal/content/excerpt.go`, factored into a
   shared helper and reused by both `Excerpt` and The Transform, and SHALL NOT
   introduce a second block-comment regular expression (D2).
3. THE block-comment stripping SHALL remove the delimiter comments only and
   SHALL preserve the inner block content between a block's opening and closing
   delimiters, so `<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->` yields
   `<p>Hi</p>`.
4. THE Transform SHALL NOT alter the stored `post_content`: no write to
   `{prefix}posts`, no migration, and no change to any repository, because The
   Transform runs on the REST response path inside `(*RESTMapper).restCommon`
   and never calls a writer (roadmap Req 18.1, 18.3; D1).
5. WHEN stored `post_content` contains no Gutenberg block-comment delimiters THE
   block-comment stripping step SHALL leave the content unchanged.

### Requirement 2: Add responsive and lazy-loading markup to media-library images

**User Story:** As a REST API consumer, I want `<img>` tags in
`content.rendered` to carry WordPress's responsive and lazy-loading attributes,
so that images display and load correctly in clients that assume WordPress's
rendering conventions.

#### Acceptance Criteria

1. WHEN the REST `content.rendered` contains an `<img>` tag referencing a
   media-library attachment (resolvable per Requirement 3) and that attachment
   has one or more size variants THE Transform SHALL add `srcset` and `sizes`
   attributes to that `<img>`, satisfying roadmap Req 18.2.
2. THE `srcset` value SHALL be built from the attachment's `_wp_attachment_metadata`
   `sizes` sub-map (D5): each candidate URL SHALL be the attachment URL's
   directory joined with that size's `file` basename, followed by a `{width}w`
   width descriptor, consistent with the uploads URL convention in
   `internal/content/media.go`.
3. THE `sizes` attribute SHALL use WordPress's default form
   `(max-width: {w}px) 100vw, {w}px` (the design pins the exact string against
   the target WordPress version).
4. WHEN the REST `content.rendered` contains an `<img>` tag referencing a
   media-library attachment THE Transform SHALL add `loading="lazy"` and
   `decoding="async"` to that `<img>`, satisfying roadmap Req 18.2.
5. IF a resolvable media-library `<img>` has no available size variants THEN THE
   Transform SHALL omit `srcset` and `sizes` from that `<img>` — never emitting
   an incorrect value — while STILL adding `loading="lazy"` and
   `decoding="async"` (roadmap Req 18.2).
6. WHEN an `<img>` already carries a `srcset`, `sizes`, `loading`, or `decoding`
   attribute THE Transform SHALL leave that existing attribute's value in place
   rather than duplicating or overwriting it, so the step is idempotent
   (Requirement 5.4; D7).

### Requirement 3: Resolve attachments via the `wp-image-{id}` convention and degrade gracefully

**User Story:** As a maintainer, I want image-to-attachment resolution to use
WordPress's own class convention, so that resolution is predictable and an
unresolvable image never produces a wrong responsive value.

#### Acceptance Criteria

1. THE Transform SHALL resolve an `<img>` to its media-library attachment id
   through the `class="wp-image-{id}"` convention only, extracting `{id}` from
   the image's `class` attribute (D4), and SHALL NOT infer attachment identity
   from the image's `src` URL.
2. IF an `<img>` carries no `wp-image-{id}` class, OR the extracted id does not
   resolve to a media-library attachment, THEN THE Transform SHALL add
   `loading="lazy"` and `decoding="async"` only, and SHALL NOT add `srcset` or
   `sizes` to that `<img>` (D4, Requirement 2.5).
3. WHEN a resolvable attachment's `_wp_attachment_metadata` is empty, missing,
   or malformed THE Transform SHALL treat the attachment as having no size
   variants (adding `loading`/`decoding` only), mirroring the empty-value
   tolerance of the existing `mediaDetailsFromSerialized`
   (`internal/content/rest.go`).
4. THE size-variant decoding SHALL be performed by a new sibling of
   `mediaDetailsFromSerialized` that reads the `sizes` sub-map of
   `_wp_attachment_metadata`, and SHALL NOT change `mediaDetailsFromSerialized`'s
   existing width/height behavior used by the media endpoint (D5).

### Requirement 4: No regression in storage, templates, or sibling rendered fields

**User Story:** As an operator with an imported WordPress database and existing
public pages, I want everything except REST `content.rendered` to behave exactly
as before, so that enabling The Transform changes nothing I can see outside the
REST post/page response.

#### Acceptance Criteria

1. THE public-facing HTML templates — `internal/render` and
   `themes/default/templates/*.tmpl` — SHALL NOT be modified by this milestone,
   so public page output and SEO behavior are unchanged (roadmap Req 18.1,
   18.3; D1).
2. THE stored `post_content` SHALL be unchanged by The Transform: no row in
   `{prefix}posts` is rewritten and no migration is added. The task list SHALL
   verify explicitly that no file is added under `internal/storage/migrations`,
   as M10a's, M10.B's, and M10.C's task lists did.
3. THE Transform SHALL apply to the `content.rendered` field of posts and pages
   only. The comment `content.rendered` field (which is HTML-escaped elsewhere),
   the media `caption`/`description`/`alt_text` fields, and the post/page
   `excerpt.rendered`, `title.rendered`, and `guid.rendered` fields SHALL NOT be
   routed through The Transform (D7).
4. THE `RESTContentRendered.Protected` flag and every other field emitted by
   `restCommon` (ID, dates, slug, status, type, link, guid, title, excerpt,
   author, featured_media, comment_status, ping_status) SHALL be unchanged; The
   Transform changes only the `RESTContentRendered.Rendered` string
   (`internal/content/rest.go`).
5. WHEN a post's or page's stored `post_content` contains no Gutenberg
   block-comment delimiters and no `<img>` tags THE Transform SHALL produce a
   `content.rendered` string byte-identical to the value `restCommon` produces
   today (D6).

### Requirement 5: HTML-aware, robust, idempotent rewriting

**User Story:** As a maintainer, I want image rewriting to be HTML-aware and
idempotent, so that The Transform cannot corrupt markup and re-applying it is
always safe.

#### Acceptance Criteria

1. THE responsive-image rewriting SHALL be HTML-aware — parsing and
   re-serializing the content with the `golang.org/x/net/html` package the
   `internal/sanitize` package already uses — and SHALL NOT rewrite `<img>`
   attributes by regular-expression substitution (D3).
2. THE Transform SHALL preserve the surrounding markup: non-`<img>` elements,
   text nodes, existing `<img>` attributes other than those Requirement 2 adds,
   and document structure SHALL be unchanged apart from block-comment removal
   and the added image attributes, and the output SHALL remain well-formed HTML.
3. WHEN The Transform encounters content it cannot parse as HTML it SHALL return
   the block-comment-stripped string without image rewriting rather than
   returning corrupted markup or panicking.
4. THE Transform SHALL be idempotent: applying it to an already-transformed
   `content.rendered` string SHALL return an equivalent string — block-comment
   stripping removes nothing further, and `loading`/`decoding`/`srcset`/`sizes`
   already present on an `<img>` are not duplicated (Requirement 2.6; D7).

### Requirement 6: Nil-safe, opt-in dependency wiring

**User Story:** As a maintainer, I want The Transform to work without the
attachment-metadata dependency wired, so that a mapper constructed without it
still returns clean `content.rendered` instead of panicking.

#### Acceptance Criteria

1. WHERE the content-render dependency that supplies attachment metadata is not
   wired into the `RESTMapper` THE Transform SHALL fall back to block-comment
   stripping only — adding no image attributes — and SHALL NOT panic (D6).
2. THE responsive-image path SHALL be nil-safe at every attachment-metadata
   access: a nil metadata source, an unresolved attachment, or an empty metadata
   blob SHALL each degrade to the lazy/decoding-only or stripping-only behavior
   (Requirements 2.5, 3.2, 3.3) rather than erroring the REST response.
3. THE Transform SHALL be additive: existing `RESTMapper` construction sites that
   do not opt into the responsive-image dependency SHALL keep producing
   block-comment-stripped `content.rendered` with no change to their call shape.

### Requirement 7: Out-of-scope exclusions

**User Story:** As a maintainer, I want 10.D's boundary stated as acceptance
criteria, so that block re-rendering and plugin REST surface area are not pulled
in under cover of "rendering improvements."

#### Acceptance Criteria

1. THE milestone SHALL NOT render arbitrary Gutenberg core or plugin blocks'
   visual output; it performs delimiter stripping and responsive-image markup
   only (roadmap Req 18.4).
2. THE milestone SHALL NOT add, read, or render arbitrary plugin-contributed
   REST fields, meta boxes, or custom Gutenberg blocks (roadmap Req 19.1).
3. THE milestone SHALL NOT add any REST write route or change any
   `restNotImplemented` registration; it is a read-path change to
   `content.rendered` only.
4. THE milestone SHALL NOT modify the public HTML templates, the stored
   `post_content`, or the comment/media/excerpt/title/guid rendered fields
   (Requirement 4).

### Requirement 8: Test coverage and close-out discipline

**User Story:** As a maintainer, I want The Transform proven by table-driven
render tests before it is considered done, so that the roadmap's M10
done-definition and testing discipline are met.

#### Acceptance Criteria

1. THE milestone SHALL cover The Transform with table-driven tests exercising:
   block-comment stripping (delimiters removed, inner content preserved,
   comment-free content unchanged); responsive markup when size variants exist
   (`srcset` candidates, `sizes` string, `loading`/`decoding` added); the
   no-variant case (`loading`/`decoding` only, `srcset`/`sizes` omitted); an
   unresolvable `<img>` (`wp-image-{id}` absent or non-resolving); idempotence
   (second application is a no-op); the nil-safe fallback (metadata dependency
   unwired → stripping only); and the byte-identical invariant for comment-free,
   image-free content — satisfying roadmap Req 19.2.
2. THE tests SHALL be pure Go, SHALL run under the project's SQLite-only test
   path, and SHALL introduce no new environment variable and no network access,
   mirroring the close-out discipline of M10a, M10.B, and M10.C (roadmap
   Req 19.3).
3. THE work SHALL follow the standing convention of the previous milestones:
   `gofmt`, `go vet`, `go build ./...`, and `go test ./...` SHALL be green at the
   end of every task.

## Out of scope

- Full Gutenberg block *editing* / re-rendering — rendering arbitrary core or
  plugin blocks' visual output (roadmap Req 18.4).
- Arbitrary plugin-contributed REST fields, meta boxes, and custom Gutenberg
  blocks (roadmap Req 19.1).
- The public-facing HTML templates (`internal/render`,
  `themes/default/templates/*.tmpl`) — unchanged, no SEO regression (D1,
  Requirement 4.1).
- Any rewrite of the stored `post_content`, and any database migration
  (Requirement 4.2 confirms none is needed).
- The comment `content.rendered`, media `caption`/`description`/`alt_text`, and
  post/page `excerpt.rendered`/`title.rendered`/`guid.rendered` fields
  (Requirement 4.3).
- Any new REST write route or capability gate — 10.D is a read-path milestone
  (Requirement 7.3).
- Roadmap group 10.E (M10 test coverage), group 10.F (navigation menu editing),
  and group 10.G (WordPress `$wp$` hash issuance) — remain open.

## Traceability

This spec refines **roadmap group 10.D** and traces to roadmap **Requirement 18**
(`../wordpress-core-parity-roadmap/requirements.md`, "`content.rendered`
Improvements"): Requirement 1 ↔ roadmap 18.1, Requirements 2–3 ↔ roadmap 18.2,
Requirement 4 ↔ roadmap 18.3, and Requirement 7.1 ↔ roadmap 18.4. The
out-of-scope plugin surface area (Requirement 7.2) traces to roadmap
**Requirement 19.1**, and the test-coverage and close-out discipline
(Requirement 8) trace to roadmap **Requirement 19.2 / 19.3**. It builds on the
read-path infrastructure established in M1–M2.2 (`internal/content/rest.go`,
`internal/content/excerpt.go`, `internal/content/media.go`) and reuses the
`golang.org/x/net/html` dependency introduced for `internal/sanitize` in M10a,
without modifying the write-boundary Policy. Groups 10.B (`../10.1-rest-media-write`)
and 10.C (`../10.2-rest-user-write`) are its write-parity siblings; 10.D is the
first M10 milestone to transform `content.rendered`.
