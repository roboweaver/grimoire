# Implementation Plan: M10.D — REST content.rendered Improvements

## Overview

Implementation checklist for M10.D (roadmap group **10.D only**). Tasks are
ordered so each builds on the last, and each references the exact files, the
exact symbols named in `design.md`'s "Components and Interfaces" / "Data Models"
sections, and the Requirement/AC plus the Finding (1–4) and Inherited decision
(D1–D7) it satisfies. Keep `gofmt -l .` empty and `go vet ./...`,
`go build ./...`, `go test ./...` green after every phase.

Strict TDD: within each phase the failing test is written **before** the
implementation that passes it, matching how M5–M10.C were run. The ordering
below makes that spine visible — every behavior-adding sub-task is preceded by
the sub-task that writes its failing test (Req 8.1).

> **This refines roadmap group 10.D only.** It is the first **read-path**
> milestone in M10 and the first requirement anywhere in grimoire to *transform*
> the REST `content.rendered` string rather than passing a stored value straight
> through, tracing to roadmap **Requirement 18**. Roadmap groups
> **10.E/10.F/10.G remain open** and are explicitly out of scope: M10 test
> coverage (10.E), navigation menu editing (10.F), and WordPress `$wp$` hash
> issuance (10.G) are untouched. 10.D adds **no write surface, no capability
> gate, no write boundary, and no new domain port** — unlike its write-parity
> siblings M10.B (`../10.1-rest-media-write`) and M10.C
> (`../10.2-rest-user-write`). It reuses M10a's `golang.org/x/net/html`
> dependency through its parse-tree API without modifying the write-boundary
> `internal/sanitize` Policy, and reuses the existing `blockCommentRE` from
> `internal/content/excerpt.go` rather than inventing a second regex (D2).
>
> **No task here authorizes a migration.** The Transform runs on the REST
> response path inside `(*RESTMapper).restCommon` and never calls a writer:
> stored `post_content` is never rewritten, no row in `{prefix}posts` changes,
> and the public HTML templates (`internal/render`,
> `themes/default/templates/*.tmpl`) are untouched, so no SEO behavior changes
> (Req 1.4, 4.1, 4.2, D1). Task 6.2 verifies explicitly that no file is added
> under `internal/storage/migrations`, as M8/M9a/M9b/M10a/M10.B/M10.C's task
> lists did (Req 4.2). The only emitted field that changes is
> `RESTContentRendered.Rendered` (Req 4.4).
>
> **Three design Findings change what gets built and are reflected in these
> tasks.** *Finding 1* — `restCommon` (`internal/content/rest.go:258`) has no
> `context.Context`, but The Transform's per-`<img>` metadata lookup
> (`m.meta.AttachmentMetadata(ctx, id)`) needs one; the fix threads `ctx` through
> `restCommon` — `restCommon(ctx, p, featured)` — and `Post`/`Page` pass their
> existing `ctx`. This is the one signature change 10.D makes to existing code
> (tasks 5.1, 5.2). *Finding 2* — `m.meta` is a *required* `NewRESTMapper`
> argument, not the "opt-in dependency" D6 describes, so there is no "not wired"
> state through the constructor; the design honors D6's *intent* (never panic,
> degrade gracefully) by **nil-guarding `m.meta`** at the top of `renderContent`
> and again in `sizesFromMetadata`, so a hand-built test mapper with nil `m.meta`
> degrades to stripping-only (tasks 2.2, 4.2). *Finding 3* — the attachment base
> URL for `srcset` is not on the mapper, so each candidate is anchored on
> `dir(src)` (the `<img>`'s own full-size URL, already in the content) as the
> primary, with **one additive opt-in field** — `uploadsBaseURL`, defaulted to
> `/wp-content/uploads` via a `WithUploadsBaseURL` builder in the `WithPermalinks`
> style — as the fallback; this is the concrete thing D6's "opt-in dependency"
> language actually fits (tasks 4.2, 6.1).
>
> **Finding 4 governs the test fixtures.** The shared `storagetest` contract
> seeds attachment 201 with a width/height-only `_wp_attachment_metadata` and
> **no `sizes` sub-map**, so the responsive path is tested against a **fake
> `domain.PostMetaRepository`** supplying richer blobs built with the `php`
> package (or hand-written `a:…{}` literals, mirroring the fixture at
> `internal/storage/storagetest/contract.go:176`), keeping every Transform test
> pure Go, SQLite-free, and env-var-free (Req 8.2). The shared cross-vendor
> fixture is **not** edited.
>
> **Deliberate reuse postures.** Block-comment stripping is one shared helper
> (`content.StripBlockComments` over the existing `blockCommentRE`), and
> `Excerpt` is refactored to call it — behavior-preserving, no second regex (D2).
> The strip step runs **first**, before any HTML parse, and a no-`<img>`
> short-circuit keeps comment-free, image-free content off the parser entirely so
> its `content.rendered` is byte-identical to today (Req 4.5, D6). Image
> rewriting is HTML-aware via `html.ParseFragment`/`html.Render` with a `<body>`
> context node (no `<html>/<head>/<body>` wrapper synthesized), never a regex
> (D3), and idempotent via `setIfAbsent` (Req 2.6, 5.4). Every failure degrades
> gracefully to a less-enhanced but valid string — the Transform never errors the
> REST response (Req 5.3, 6.1, 6.2).
>
> **One signature change breaks nothing outside this package.** Adding `ctx` to
> the unexported `restCommon` (task 5.2) touches exactly its two callers, `Post`
> and `Page`, both in `internal/content/rest.go`; there are no other callers.
> Every other addition — `StripBlockComments`, `renderContent`, `sizeVariant`,
> `sizesFromMetadata`, the rewriting helpers, and the `uploadsBaseURL` field +
> `WithUploadsBaseURL` builder — is additive and breaks no caller (Req 6.3).

## Tasks

## Phase 1 — Shared block-comment helper (`internal/content`, behavior-preserving refactor)

- [x] 1.1 Write the failing helper test first. In `internal/content` (extend
      `excerpt_test.go` or add a focused `*_test.go`) assert that a new
      package-visible `StripBlockComments(s string) string` removes Gutenberg
      block-delimiter comments while preserving inner block content
      (`<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->` → `<p>Hi</p>`,
      Req 1.1, 1.3) and leaves comment-free content unchanged (Req 1.5); cover the
      `/wp:` closing and whitespace-variant delimiters the existing `blockCommentRE`
      matches. Add/extend an assertion that `Excerpt`'s existing behavior is
      unchanged after the forthcoming refactor (reuse the existing `excerpt_test.go`
      expectations — the refactor is behavior-preserving).
      Files: `internal/content/excerpt_test.go`
      Verify: `go test ./internal/content` — fails (`StripBlockComments` absent).
      _(Req 1.1, 1.3, 1.5; D2)_

- [x] 1.2 Add `StripBlockComments(s string) string` in
      `internal/content/excerpt.go` as `blockCommentRE.ReplaceAllString(s, "")`,
      and refactor `Excerpt`'s first line to call it (BEFORE:
      `blockCommentRE.ReplaceAllString(p.Content, "")`; AFTER:
      `StripBlockComments(p.Content)`). `blockCommentRE` stays declared in
      `excerpt.go`, so there is one pattern and one place to change it; the helper
      introduces no second block-comment regex (D2). Behavior-preserving: the
      pattern and the `ReplaceAllString(…, "")` replacement are unchanged.
      Files: `internal/content/excerpt.go`
      Verify: `go test ./internal/content` — the 1.1 assertions pass and the
      existing `Excerpt` tests still pass; `go vet ./...` clean. _(Req 1.2, 1.3; D2)_

## Phase 2 — Size-variant metadata decoder (`internal/content`, pure, no HTTP)

- [x] 2.1 Write failing decoder tests first in `internal/content/rest_test.go`
      (or a focused content test file) for
      `(*RESTMapper).sizesFromMetadata(ctx, id) ([]sizeVariant, int)`, against a
      **fake `domain.PostMetaRepository`** that returns raw PHP-serialized
      `_wp_attachment_metadata` blobs (build them with the existing `php` package
      or hand-written `a:…{}` literals, mirroring the fixture at
      `internal/storage/storagetest/contract.go:176` extended with a `sizes`
      sub-map — Finding 4). Define the expected `sizeVariant{File, Width, Height}`
      shape in the test. Assert: a rich blob with a top-level `width` and a `sizes`
      sub-map decodes to the expected `[]sizeVariant` plus the full-size width; an
      empty/missing/malformed blob → `(nil, 0)` (Req 3.3); a blob with no `sizes`
      key → `(nil, fullWidth)` (Req 2.5); nil `m.meta` → `(nil, 0)` (Finding 2);
      incomplete entries (missing `file`, or `width <= 0`) are skipped rather than
      emitted as wrong candidates.
      Files: `internal/content/rest_test.go`
      Verify: `go test ./internal/content` — fails (`sizesFromMetadata`/
      `sizeVariant` absent). _(Req 2.5, 3.3, 3.4; Finding 2, 4; D5)_

- [x] 2.2 Implement the `sizeVariant` type and `(*RESTMapper).sizesFromMetadata`
      in `internal/content/rest.go` exactly per design: declare the unexported
      `sizeVariant struct{ File string; Width int; Height int }` (Data Models), and
      implement `sizesFromMetadata` as a new, independent sibling of
      `mediaDetailsFromSerialized` — nil-guard `m.meta` → `(nil, 0)` (Finding 2);
      `m.meta.AttachmentMetadata(ctx, id)` with an error/empty blob → `(nil, 0)`
      (treat an `AttachmentMetadata` error as "no variants", not a surfaced error,
      Req 3.3); `php.Unserialize` + the existing `phpInt` helper to read the
      top-level `width` and the `sizes` sub-map; a missing `sizes` key →
      `(nil, fullWidth)`; skip entries with empty `file` or `width <= 0`. Do **not**
      change `mediaDetailsFromSerialized`'s width/height behavior (Req 3.4).
      Files: `internal/content/rest.go`
      Verify: `go test ./internal/content` — the 2.1 tests pass; `go vet ./...`
      clean. _(Req 2.2, 3.3, 3.4; Finding 2, 4; D5)_

## Phase 3 — srcset/sizes construction + attachment resolution (`internal/content`, pure helpers)

- [x] 3.1 Write failing tests first in `internal/content/rest_test.go` for
      `srcsetFromSizes(base string, variants []sizeVariant, fullWidth int) (srcset, sizes string)`,
      `resolveAttachmentID(n *html.Node) (int64, bool)`, and the tiny helpers
      `urlDir`, `attrValue`, `setIfAbsent` (construct `*html.Node` values via
      `html.ParseFragment` or node literals). Assert: `srcset` is `dir(base)` +
      each variant's `file` + `" {width}w"`, candidates joined by `", "` and sorted
      **ascending by width** (Req 2.2, D5); `sizes` is
      `"(max-width: {w}px) 100vw, {w}px"` when `fullWidth > 0` and `""` when
      `fullWidth == 0` (never a `0px` form, Req 2.3, 2.5 spirit);
      `resolveAttachmentID` extracts `{id}` from a `class` **token**
      `wp-image-{digits}` (token scan via `strings.Fields`, not a substring match —
      `not-a-wp-image-5-thing` must not match), returns `(0, false)` when the token
      is absent/non-numeric/zero, and **never** consults `src` (Req 3.1); `urlDir`
      returns everything through the final `/` (or `""`); `attrValue` returns the
      attribute value or `""`; `setIfAbsent` appends only when the attribute is
      absent (idempotence, Req 2.6).
      Files: `internal/content/rest_test.go`
      Verify: `go test ./internal/content` — fails (helpers absent). _(Req 2.2,
      2.3, 2.5, 2.6, 3.1, 3.2; D4, D5)_

- [x] 3.2 Implement `srcsetFromSizes`, `resolveAttachmentID`, `urlDir`,
      `attrValue`, and `setIfAbsent` in `internal/content/rest.go` per design:
      `srcsetFromSizes` sorts a copy of `variants` ascending by `Width`
      (`sort.Slice`), builds each candidate as `urlDir(base) + v.File + " " +
      strconv.Itoa(v.Width) + "w"`, joins with `", "`, and emits the pinned `sizes`
      string only when `fullWidth > 0`; `resolveAttachmentID` scans
      `strings.Fields(attrValue(n, "class"))`, tests each token with
      `strings.CutPrefix(tok, "wp-image-")` and `strconv.ParseInt(rest, 10, 64)`,
      returning `(id, true)` only for a whole-token numeric `id > 0`, never reading
      `src`; `urlDir` returns `base` up to and including the final `/` (or `""`);
      `attrValue`/`setIfAbsent` iterate `n.Attr`. Import `sort`, `strconv`,
      `strings`, and `golang.org/x/net/html`.
      Files: `internal/content/rest.go`
      Verify: `go test ./internal/content` — the 3.1 tests pass; `go vet ./...`
      clean. _(Req 2.2, 2.3, 2.5, 2.6, 3.1, 3.2; D4, D5)_

## Phase 4 — Image rewriting + The Transform (`internal/content`, HTML-aware)

- [x] 4.1 Write failing tests first in `internal/content/rest_test.go` (one
      table-driven place) for `renderContent`, `rewriteImages`, `walkAndRewrite`,
      and `rewriteImageNode`, built via `NewRESTMapper` with a **fake
      `domain.PostMetaRepository`** supplying `sizes` blobs (and
      `WithUploadsBaseURL` where the `src` fallback is exercised — Finding 3).
      Assert: a media-library `<img>` (`class="wp-image-{id}"`) with size variants
      gets `srcset` + `sizes` + `loading="lazy"` + `decoding="async"` (Req 2.1–2.4);
      a resolvable `<img>` with **no** variants gets `loading`/`decoding` only, no
      `srcset`/`sizes` (Req 2.5, 3.3); an unresolvable `<img>` (no `wp-image-`
      class) gets `loading`/`decoding` only (Req 3.2); existing
      `loading`/`decoding`/`srcset`/`sizes` attributes are left in place, never
      duplicated or overwritten (Req 2.6, 5.4); surrounding markup, text nodes, and
      other `<img>` attributes are preserved (Req 5.2); the output is a **fragment**
      with **no** wrapping `<html>`/`<head>`/`<body>` (explicit assertion, the
      `ParseFragment` pitfall); unparseable input returns the block-comment-stripped
      string, not corrupted markup or a panic (Req 5.3); nil `m.meta` → strip-only
      (Req 6.1, 6.2); a comment-free, image-free string is byte-identical to today's
      stripped output (Req 4.5); and `renderContent(renderContent(x))` equals
      `renderContent(x)` (idempotence, Req 5.4).
      Files: `internal/content/rest_test.go`
      Verify: `go test ./internal/content` — fails (`renderContent`/`rewriteImages`
      absent). _(Req 2.1, 2.4, 2.5, 2.6, 3.2, 4.5, 5.1, 5.2, 5.3, 5.4, 6.1, 6.2;
      Finding 2, 3; D3, D6)_

- [x] 4.2 Implement `renderContent`, `rewriteImages`, `walkAndRewrite`,
      `rewriteImageNode`, `containsIMG`, and the `uploadsBaseURL` field +
      `WithUploadsBaseURL` builder + the `NewRESTMapper` default in
      `internal/content/rest.go`, exactly per design. `renderContent(ctx, rendered)`:
      strip first via `StripBlockComments`, then short-circuit (`m.meta == nil ||
      !containsIMG(stripped)` → return `stripped`, Req 4.5/6.1/6.2/D6), else delegate
      to `rewriteImages`. `rewriteImages` parses with `html.ParseFragment` over a
      `&html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}`
      context node (no wrapper), returns `s` unchanged on a parse error (Req 5.3),
      walks each top-level node, and re-serializes with a per-node `html.Render`
      into a `bytes.Buffer` (returning `s` on a render error). `walkAndRewrite`
      descends depth-first, calling `rewriteImageNode` on each `atom.Img`.
      `rewriteImageNode` is strip-first-then-short-circuit-then-rewrite:
      `setIfAbsent "loading"/"decoding"` always (Req 2.4), `resolveAttachmentID`
      → return on `!ok` (Req 3.2), `sizesFromMetadata` → return on no variants
      (Req 2.5), then `srcsetFromSizes(attrValue(n,"src"), …)` and `setIfAbsent`
      `srcset`/`sizes` only when `srcset != ""` (dir(src)-anchored, with the
      `uploadsBaseURL` fallback). `containsIMG` is a case-folded
      `strings.Contains(s, "<img")` probe. Add the `uploadsBaseURL` field, the
      `WithUploadsBaseURL(base string) *RESTMapper` builder (WithPermalinks style),
      and the `NewRESTMapper` default `uploadsBaseURL: "/wp-content/uploads"`.
      Import `bytes`, `golang.org/x/net/html`, and `golang.org/x/net/html/atom`.
      Files: `internal/content/rest.go`
      Verify: `go test ./internal/content` — the 4.1 tests pass; `go vet ./...`
      clean. _(Req 2.1, 2.4, 2.5, 2.6, 5.1, 5.2, 5.3, 5.4, 6.1, 6.2, 6.3; Finding
      2, 3; D3, D6)_

## Phase 5 — Wire The Transform into restCommon (`internal/content`, the one edit to existing output)

- [x] 5.1 Write failing (or extend existing) tests first in
      `internal/content/rest_test.go` asserting `(*RESTMapper).Post` and
      `(*RESTMapper).Page` now emit a transformed `content.rendered`: a post whose
      stored `Content` carries block comments + a `wp-image-{id}` `<img>` comes back
      with the comments stripped and the `<img>` enhanced (Req 1.1); the
      no-regression invariants — `RESTContentRendered.Protected` plus every sibling
      field emitted by `restCommon` (id, dates, slug, status, type, link, guid,
      title, excerpt, author, featured_media, comment_status, ping_status) is
      unchanged (Req 4.4); and a comment-free, image-free post's `content.rendered`
      is byte-identical to the pre-10.D value (Req 4.5).
      Files: `internal/content/rest_test.go`
      Verify: `go test ./internal/content` — fails (`restCommon` still passes bare
      `p.Content`). _(Req 1.1, 4.4, 4.5)_

- [x] 5.2 Thread `ctx` through `restCommon` and route `content.rendered` through
      The Transform in `internal/content/rest.go` (Finding 1): change the signature
      BEFORE `restCommon(p domain.Post, featuredMedia int64)` → AFTER
      `restCommon(ctx context.Context, p domain.Post, featuredMedia int64)`, and
      replace the bare `Content: RESTContentRendered{Rendered: p.Content,
      Protected: p.Password != ""}` with `Rendered: m.renderContent(ctx, p.Content)`
      (every other `restCommon` field expression, including `Protected`, is
      unchanged, Req 4.4). Update `Post` and `Page` to pass their existing `ctx`
      (`m.restCommon(ctx, p, featured)`); these are the only two callers (confirm
      no other `restCommon` caller exists). Comment/media/excerpt/title/guid
      rendered fields never reach `restCommon` and so stay untransformed (Req 4.3).
      Files: `internal/content/rest.go`
      Verify: `go test ./internal/content` — the 5.1 tests pass and the existing
      `rest_test.go` `Post`/`Page` tests still pass; `go vet ./...` clean. _(Req
      1.1, 1.4, 4.1, 4.2, 4.4; Finding 1; D1)_

## Phase 6 — main wiring + final gates

- [x] 6.1 Wire `WithUploadsBaseURL` on the `restMapper` construction in
      `cmd/grimoire/main.go`, from the same uploads config `MediaService` already
      reads (one source of truth for the uploads base, Finding 3), so the mapper's
      `srcset` fallback anchor matches the media endpoint's base. Omitting it would
      leave the `/wp-content/uploads` default; wiring it keeps a single uploads-base
      source.
      Files: `cmd/grimoire/main.go`
      Verify: `go build ./...` and `go test ./cmd/...` green. _(Req 6.3; Finding 3)_

- [x] 6.2 Verify the no-regression invariants and run the full gate suite: confirm
      no new file exists under `internal/storage/migrations/` for this milestone
      (Req 4.2, mirroring M8/M9a/M9b/M10a/M10.B/M10.C); confirm `internal/render`
      and `themes/default/templates/*.tmpl` are untouched (Req 4.1); confirm the
      comment/media/excerpt/title/guid `rendered` fields are untransformed
      (Req 4.3). Then run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`,
      `go test ./...` and fix anything red. Confirm no test added by this milestone
      requires a database, network, or live WordPress, and that `go test ./...`
      needs no new `GRIMOIRE_TEST_*` env vars (pure Go, SQLite-only).
      Files: (verification only) `internal/storage/migrations/`, `internal/render/`,
      `themes/default/templates/`
      Verify: `git status --porcelain internal/storage/migrations` shows no new
      files; all four gate commands green; no new `GRIMOIRE_TEST_*` env var
      required. _(Req 4.1, 4.2, 4.3, 8.2, 8.3)_

## Notes

- Each task references the exact Requirement/AC, Finding, and Inherited decision
  it satisfies, for traceability back to `requirements.md` and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt`/`vet`/`build`/`test` after
  every phase, matching M10a/M10.B/M10.C's close-out discipline.
- The TDD spine is explicit: every behavior-adding sub-task is preceded by the
  sub-task that writes its failing test (Req 8.1).
- No sub-task is marked optional (`*`): this milestone's table-driven render tests
  are the roadmap's M10 done-definition for `content.rendered` (Req 8), including
  the fail-safe degradation, idempotence, and byte-identical invariants — not
  nice-to-haves — so each is a core task. (This matches M10.B and M10.C, whose
  tests were likewise unmarked.)
- No property-based tests: The Transform is a fixed, example-enumerable rewrite —
  a specific set of block-comment and `<img>` cases map to a specific set of
  outputs, and the `wp-image-{id}` resolution, the `srcset`/`sizes` string forms,
  and the degradation branches are a fixed table, not a property. The one
  genuinely universal property — that an HTML parse → rewrite → serialize
  round-trip stays well-formed and closed over the markup — is
  `golang.org/x/net/html`'s own invariant, already relied on where
  `internal/sanitize` consumes the parser, and 10.D does not re-prove it (design
  "Testing strategy"; mirrors the M10.B/M10.C Notes).

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "2.1"] },
    { "id": 1, "tasks": ["1.2", "2.2"] },
    { "id": 2, "tasks": ["3.1"] },
    { "id": 3, "tasks": ["3.2"] },
    { "id": 4, "tasks": ["4.1"] },
    { "id": 5, "tasks": ["4.2"] },
    { "id": 6, "tasks": ["5.1"] },
    { "id": 7, "tasks": ["5.2"] },
    { "id": 8, "tasks": ["6.1"] },
    { "id": 9, "tasks": ["6.2"] }
  ]
}
```
