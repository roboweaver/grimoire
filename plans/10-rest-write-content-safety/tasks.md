# Tasks — M10a: Write-Boundary Content Safety

Implementation checklist for M10a (roadmap group **10.A only**). Tasks are
ordered so each builds on the last, and each references the exact files, the
exact symbols named in `design.md`'s API section, and the Requirement/AC plus
the correctness property (P1–P9) and Finding (1–7) it satisfies. Keep
`gofmt -l .` empty and `go vet ./...`, `go build ./...`, `go test ./...` green
after every phase.

Strict TDD: within each phase the failing test is written **before** the
implementation that passes it, matching how M5–M9b were run. The ordering below
makes that spine visible — every behavior-adding sub-task is preceded by the
sub-task that writes its failing test (Requirements 1.10, 9.1).

> **This refines roadmap group 10.A only.** It is the capability-aware
> write-boundary content policy of roadmap Requirement 15. Roadmap groups
> **10.B–10.G remain open** and are explicitly out of scope: no new REST write
> route is enabled, `/wp-json/wp/v2/media` and `/wp-json/wp/v2/users` writes stay
> `501` (`internal/web/rest_media.go`, `internal/web/rest_users.go`
> `restNotImplemented`). The roadmap `tasks.md` group 10.A box is ticked by
> task 9.5 with a note that 10.B–10.G remain open.
>
> **No task here authorizes a migration.** The Policy operates on strings in
> memory above storage (Requirement 1.3). Task 8.1 verifies explicitly that no
> file is added under `internal/storage/migrations`, as M8's, M9a's and M9b's
> task lists did (Requirements 1.4, 9.11). No cross-vendor storage contract test
> is added either — there is nothing vendor-specific about allow-listing HTML.
>
> **Three design Findings change scope and are reflected in these tasks.**
> *Finding 1* — a single-pass title function fails property P1, so the title
> path iterates to a fixed point bounded at 16 passes and fails closed beyond it
> (tasks 2.x, `titleText`/`ErrTitleNotConverging`). *Finding 2* — removing the
> comment render-time escape (Requirement 6.1) would expose pre-M10a stored
> comment scripts, so a tier-A render backstop is **in scope** at both comment
> render sites (tasks 6.x, 7.x) and the residual limitation is documented
> (task 9.x). *Finding 3* — `Update`'s "caller-supplied" is not observable, and
> re-sanitizing a merged base value violates Requirement 5.2, so
> `sanitizeIncoming` skips any field byte-identical to the stored value (tasks
> 5.x). *Finding 4* makes property **P7 conditional**: echo/stored agreement
> holds only when the GET carries the submitter's principal; the non-agreeing
> cases are pinned as unconditional safety assertions (tasks 6.x, 7.x).
>
> **Deliberate divergences and defect fixes.** D8 (bluemonday drops bare
> attribute-less allowed elements) is a construction-time defect fixed with
> `AllowNoAttrs` on every element, not a documented divergence. The comment
> double-escape removal **intentionally inverts**
> `internal/web/comments_public_test.go:104` (task 7.x). Findings 5, 6, 7 are
> corrections carried into `design.md` and `docs/compatibility.md`
> (the tier-A enumeration's four missing attribute grants, the WordPress **7.1**
> provenance, and the tight-list title filter at 14 elements).

## Phase 0 — Dependencies

- [x] 0.1 Add the two pinned dependencies and vendor the module graph so the
      new package compiles. Run `go get github.com/microcosm-cc/bluemonday@v1.0.27`
      and `go get golang.org/x/net@v0.59.0`, then `go mod tidy`. `golang.org/x/net`
      is pinned **forward as a direct requirement** rather than inherited at
      bluemonday's `v0.26.0`, because `golang.org/x/net/html` is the actual parser
      standing between this system and stored XSS, and because the P2 closure test
      re-tokenizes with it (tasks 3.2, 3.4). The four new modules are `bluemonday`,
      `golang.org/x/net`, `github.com/aymerick/douceur` and
      `github.com/gorilla/css`; the latter two are reachable only through the
      tier-B `style` path.
      Files: `go.mod`, `go.sum`
      Verify: `go build ./...` and `go mod verify` succeed; `go.mod` lists
      `github.com/microcosm-cc/bluemonday v1.0.27` and `golang.org/x/net v0.59.0`
      as direct requirements. _(Req 8.1, 8.4, 8.6; design "Pinned versions")_

## Phase 1 — `internal/sanitize` types and tier selection (pure, no DB, no HTTP)

- [x] 1.1 Write failing tests for `FieldKind`, `Tier`, `Writer`,
      `Anonymous`, `For` and `TierFor` in a new
      `internal/sanitize/sanitize_test.go`. Assert the four `FieldKind` constants
      (`PostContent`, `PostExcerpt`, `PostTitle`, `CommentContent`) and three
      `Tier` constants (`TierA`, `TierB`, `TierC`); that `Anonymous()` yields a
      zero-value `Writer` reporting `Authenticated() == false` and selecting a
      non-C tier; that `For(auth.Principal{})` and `For` of a principal holding
      `CapUnfilteredHTML` differ only in the `unfiltered` bit; and the full
      `TierFor(kind, writer)` truth table — `CommentContent` without
      `unfiltered_html` → `TierA`, `PostContent`/`PostExcerpt`/`PostTitle` without
      it → `TierB`, any kind with it → `TierC`. Build principals through the real
      `auth.CapabilitiesForRoles`/`auth.NewPrincipal`, not a hand-written map, so
      a `roles.go` change surfaces here.
      Files: `internal/sanitize/sanitize_test.go` (new)
      Verify: `go test ./internal/sanitize` — the new test fails to compile (types
      not yet defined), confirming it is a failing test first.
      _(Req 1.5, 1.6, 1.7, 2.4, 2.5; P6; Finding 5)_

- [x] 1.2 Implement the type layer in a new `internal/sanitize/sanitize.go`:
      `FieldKind` + its four constants, `Tier` + its three constants, the `Writer`
      struct (unexported `authenticated`, `unfiltered` bits), `Anonymous`, `For`,
      `Authenticated`, `TierFor`, and the `CapUnfilteredHTML = "unfiltered_html"`
      constant. `For` reads the capability through `auth.Principal.Can`
      (`internal/auth/principal.go:25`) — no second capability mechanism. A
      zero-value `Writer` is the fail-closed anonymous caller and never reaches
      `TierC`. The package imports `internal/auth` and nothing else from the tree.
      Files: `internal/sanitize/sanitize.go` (new)
      Verify: `go test ./internal/sanitize` — the 1.1 tests pass; `go vet ./...`
      clean. _(Req 1.5, 1.6, 1.7, 2.4, 2.5; P6)_

## Phase 2 — Tier tables, Policy construction, and `post_title` (pure)

- [x] 2.1 Write failing table-driven tests for tier A and tier B element/attribute
      behavior in `internal/sanitize/tables_test.go` (no database, Requirement
      9.2). For each tier and relevant field kind: one case per allow-listed
      element and one per allow-listed attribute drawn from `design.md`'s "Tier A"
      and "Tier B" tables, plus a rejection case each for `<script>`, `<style>`,
      `<iframe>`, `<form>`, `<input>`, an `on*` handler, a `javascript:` URL and a
      `data:` URL. Include the construction-specific cases: a bare `<a>text</a>`
      **surviving** tier B and tier A (the D8 fix), a Gutenberg block delimiter
      `<!-- wp:paragraph -->` surviving tier B and **stripped** at tier A, and a
      `style` attribute carrying one allowed and one forbidden property. Assert
      `RequireParseableURLs` is in force indirectly: every `javascript:`/`data:`
      case must be rejected (removing the flag would silently pass them).
      Files: `internal/sanitize/tables_test.go` (new)
      Verify: `go test ./internal/sanitize` — fails (tables/`Policy` not built).
      _(Req 2.1, 2.2, 2.9, 2.10; D8, D14)_

- [x] 2.2 Implement the declarative tier tables and the three-policy construction
      in `internal/sanitize/tables.go` and `internal/sanitize/policy.go`. Add
      `tierAElements`, `tierBElements`, `tierBGlobalAttrs`, `tierBStyleProps` and
      `allowedSchemes` as package-level data exactly per `design.md`'s "Tier A",
      "Tier B", "URL schemes" (the 22 `wp_allowed_protocols`) and "Tier B `style`
      properties" (the 117-property intersection). Add the `Policy` struct
      (`tierA`, `tierB`, `strict *bluemonday.Policy`) and `New() *Policy`, building
      each tier from `bluemonday.NewPolicy()` — never `UGCPolicy`/`StrictPolicy`
      as a tier. `New` must call `AllowNoAttrs().OnElements(...)` for **every**
      element in the tier's table (the D8 defect fix), `RequireParseableURLs(true)`
      and `AllowRelativeURLs(true)` (scheme check is gated on the former),
      `AllowURLSchemes(allowedSchemes...)`, and for tier B `AllowAttrs(...global
      19...).Globally()`, `AllowDataAttributes()`, `AllowStyles(tierBStyleProps...)
      .Globally()` and `AllowComments()` (tier B only; tier A leaves comments
      stripped). `strict` is `bluemonday.StrictPolicy()` for titles.
      Files: `internal/sanitize/tables.go` (new), `internal/sanitize/policy.go` (new)
      Verify: `go test ./internal/sanitize` — the 2.1 tests pass.
      _(Req 2.1, 2.2, 2.9, 2.10, 2.13; D8; design "The tables are data, not code")_

- [x] 2.3 Write a failing test for `Sanitize`/`SanitizeAt` dispatch and the
      tier-C bypass in `internal/sanitize/policy_test.go`: `SanitizeAt(k, TierC, in)`
      returns `in` byte-identically for every field kind including `PostTitle` and
      including malformed input, without re-serializing; an unrecognised
      `FieldKind` or `Tier` returns a non-nil error (fail closed, never a
      permissive default); and `Sanitize(k, w, in)` routes through `TierFor` to the
      right tier.
      Files: `internal/sanitize/policy_test.go` (new)
      Verify: `go test ./internal/sanitize` — fails (methods not implemented).
      _(Req 1.8, 2.3; P4)_

- [x] 2.4 Implement `Sanitize` and `SanitizeAt` on `*Policy`. `SanitizeAt(k, TierC,
      in)` returns `(in, nil)` before touching any table (byte-identity, no
      tokenize). Tiers A/B dispatch to the compiled policy; `PostTitle` below C
      dispatches to `titleText`. On an unrecognised `FieldKind`/`Tier` return an
      error — a `switch` with a permissive `default` is the realistic break and is
      why `SanitizeAt` returns an error at all. No path returns a non-nil error
      together with a persistable value, and no path returns unsanitized bytes for
      tier A/B. `Sanitize` derives the tier via `TierFor` and delegates.
      Files: `internal/sanitize/policy.go`
      Verify: `go test ./internal/sanitize` — the 2.3 tests pass.
      _(Req 1.2, 1.8, 2.3, 2.7; P4)_

- [x] 2.5 Write failing tests for the `post_title` fixed-point path in
      `internal/sanitize/title_test.go`: the three worked examples from
      `design.md` (`<em>Hello</em>` → `Hello`; `5 &amp; 6` → `5 & 6`;
      `&amp;lt;b&amp;gt;` → ``), that the output contains no `<`, no `>` and no
      entity sequence, and that a crafted input exceeding the 16-pass bound returns
      `ErrTitleNotConverging` and no value (fail closed).
      Files: `internal/sanitize/title_test.go` (new)
      Verify: `go test ./internal/sanitize` — fails (`titleText` not implemented).
      _(Req 3.1, 3.2, 3.3, 3.4, 3.8; P5, P9; Finding 1)_

- [x] 2.6 Implement `titleText` and `ErrTitleNotConverging` plus the
      `deleteAngles` helper and `titleMaxPasses = 16` constant in
      `internal/sanitize/title.go`. Each pass is `deleteAngles(stdhtml.
      UnescapeString(p.strict.Sanitize(cur)))`, looped until a pass is a no-op or
      the bound is hit; exceeding the bound returns `("", ErrTitleNotConverging)`.
      `StrictPolicy` alone does not satisfy Requirement 3.3 (it escapes surviving
      text), which is why the unescape+delete steps follow and iterate. No regex is
      used on the tag-splitting path (Requirement 8.7).
      Files: `internal/sanitize/title.go` (new)
      Verify: `go test ./internal/sanitize` — the 2.5 tests pass.
      _(Req 3.1, 3.2, 3.3, 3.4, 3.8, 8.7; P5, P9; Finding 1)_

## Phase 3 — Pure-package correctness: table coverage, matrix, properties, fuzz, parity

- [x] 3.1 Write and keep green the tier-selection **matrix** test in
      `internal/sanitize/matrix_test.go` (Requirement 9.3): anonymous, subscriber,
      contributor, author, editor and administrator × the four field kinds,
      asserting the selected `Tier`, built through the real
      `auth.CapabilitiesForRoles`/`auth.NewPrincipal`. Include a **custom role
      granted `unfiltered_html`** selecting `TierC`, and assert that **subscriber,
      contributor and author commenting all select `TierA`** (the cell an earlier
      spec draft got wrong). Assert `Anonymous()` and `For(auth.Principal{})`
      select the same tier while remaining distinguishable via `Authenticated()`.
      Files: `internal/sanitize/matrix_test.go` (new)
      Verify: `go test ./internal/sanitize` passes. _(Req 2.4, 2.5, 9.3)_

- [x] 3.2 Add the `markupish` generator and the `testing/quick`-based property
      tests P1, P2 (unit arm), P3 and P6 in `internal/sanitize/properties_test.go`.
      `type markupish string` implements `quick.Generator`, assembling inputs from a
      fragment corpus (allow-listed/disallowed/nested/unclosed tags, mixed-case
      attrs, `on*` handlers, hostile schemes, doubled encodings, stray `<`,
      unbalanced quotes, entities), driven by `quick.Config{MaxCount: 1000, Rand:
      rand.New(rand.NewSource(1))}` (fixed seed). **P1** (idempotence) quantifies
      over all field kinds **except `PostTitle`** (Finding 1). **P2** (allow-list
      closure) re-tokenizes the output with `golang.org/x/net/html` and checks every
      element/attribute/URL-scheme against the tier's own table — asserted on the
      parsed tree, not by substring. **P3** (monotonicity) is set containment over
      `tierAElements`/`tierBElements` plus a generated survival clause, with the
      comment-token carve-out asserted separately. **P6** is 1000 sequential
      invocations yielding one output, 64 goroutines × 100 invocations under
      `-race`, and the import-set test.
      Files: `internal/sanitize/properties_test.go` (new)
      Verify: `go test -race ./internal/sanitize` passes.
      _(Req 1.9, 2.6, 2.7, 9.9; P1, P2, P3, P6)_

- [x] 3.3 Add the import-set purity test in
      `internal/sanitize/importset_test.go` asserting `internal/sanitize` imports
      nothing from `net` (other than `net/url` reached transitively through
      bluemonday — assert the package's **own** import list excludes `net`, `os`,
      `time`, `database/sql`), so "no clock read / no network / no filesystem" is
      enforced rather than hoped for (Requirement 2.8, part of P6).
      Files: `internal/sanitize/importset_test.go` (new)
      Verify: `go test ./internal/sanitize` passes. _(Req 1.2, 2.8; P6)_

- [x] 3.4 Add the three fuzz targets with committed seed corpora: `FuzzTierCIdentity`
      (asserts `SanitizeAt(k, TierC, string(b)) == string(b)` for all four kinds,
      seeds `\xff\xfe`, lone `\xc3`, truncated surrogate, `<b` with no `>`, `<<<`, a
      64-level nested `<div>` chain, a `\x00` byte), `FuzzTierClosure` (re-tokenizes
      and checks P2 closure over `[]byte` input) and `FuzzTitleText` (no `<`, no
      `>`, output is a fixed point of `UnescapeString`, and `titleText(titleText(x))
      == titleText(x)`). Write the failing targets first, then confirm the seed
      corpus passes under plain `go test` (no `-fuzz`). Add a companion table test
      asserting tier C does not route through bluemonday at all.
      Files: `internal/sanitize/fuzz_test.go` (new),
      `internal/sanitize/testdata/fuzz/FuzzTierCIdentity/*`,
      `internal/sanitize/testdata/fuzz/FuzzTierClosure/*`,
      `internal/sanitize/testdata/fuzz/FuzzTitleText/*` (new seed corpora)
      Verify: `go test ./internal/sanitize` runs the seed corpora green;
      `go test -fuzz=FuzzTierCIdentity ./internal/sanitize` is available for
      campaigns. _(Req 9.9; P2, P4, P5, P9)_

- [x] 3.5 Add the committed parity fixture set and its offline test (Requirement
      9.10, property P8). Author `internal/sanitize/testdata/parity/fixtures.json`
      (each entry: `id`, `kind`, `tier`, `input`, `wordpress_of_input`, `grimoire`,
      `wordpress_of_grimoire`, `divergence`), `provenance.json` (WordPress **7.1**,
      the three array counts, the oracle calls, script sha256) and the
      committed-but-not-run `scripts/capture-kses-fixtures.php`. The test asserts
      per fixture `SanitizeAt(kind, tier, input) == grimoire` (regression pin) and
      `wordpress_of_grimoire == grimoire` (**this is P8**), plus the invariant that
      every fixture with `wordpress_of_input != grimoire` carries a non-`"none"`
      `divergence`. Seed the set with D1–D15, the seven design-verified behaviors,
      one fixture per tier-A element and a scheme/`style`-weighted tier-B sample.
      Files: `internal/sanitize/parity_test.go` (new),
      `internal/sanitize/testdata/parity/fixtures.json`,
      `internal/sanitize/testdata/parity/provenance.json`,
      `internal/sanitize/testdata/parity/inputs.txt`,
      `scripts/capture-kses-fixtures.php` (all new; the `.php` is not run by CI)
      Verify: `go test ./internal/sanitize` passes offline (no PHP, no DB, no net).
      _(Req 2.11, 2.12, 2.14, 9.10, 9.12; P8; Findings 6)_

## Phase 4 — Comment write path (`internal/content`)

- [x] 4.1 Write failing tests in `internal/content/comments_test.go` asserting
      that `CommentService.Create` sanitizes `c.Content` at the submitter's tier
      **before** persistence, with assertions made on **what reached the writer
      port** (fake `CommentWriter`): a tier-A submitter's `<script>` is stripped
      and an allow-listed `<em>` survives; a body that sanitizes to empty returns
      the new `ErrCommentEmpty`; and the signature now takes `actor sanitize.Writer`.
      Files: `internal/content/comments_test.go`
      Verify: `go test ./internal/content` — fails (signature/sentinel absent).
      _(Req 4.1, 4.8, 9.4)_

- [x] 4.2 Change `CommentService.Create` (`internal/content/comments.go:80`) to
      take `actor sanitize.Writer`, add the `policy *sanitize.Policy` field +
      constructor argument to `NewCommentService` (positional, required), and
      insert `clean, err := s.policy.Sanitize(sanitize.CommentContent, actor,
      c.Content)` **before** the spam evaluation and persistence, returning
      `ErrCommentEmpty` when `clean == ""` and the input was not. Declare
      `var ErrCommentEmpty = errors.New(...)`. The Policy does not reject content;
      this is the calling path applying required-field validation to the sanitized
      value (Requirement 4.8).
      Files: `internal/content/comments.go`
      Verify: `go test ./internal/content` — the 4.1 tests pass.
      _(Req 1.1, 1.6, 4.1, 4.8)_

## Phase 5 — Post write path (`internal/content`)

- [x] 5.1 Write failing tests in `internal/content/writeservices_test.go` for
      `PostWriteService.Create`: `post_content` and `post_excerpt` are sanitized at
      tier B for a writer lacking `unfiltered_html`, `post_title` is reduced to
      plain text, a title that sanitizes to empty returns the new `ErrTitleEmpty`,
      and tier-C input from an editor/administrator is persisted **byte-identically**
      using input tier B would demonstrably alter. Assertions on the fake
      `PostWriter` port.
      Files: `internal/content/writeservices_test.go`
      Verify: `go test ./internal/content` — fails (policy/option/sentinel absent).
      _(Req 4.2, 9.4, 9.6)_

- [x] 5.2 Write failing tests for `PostWriteService.Update` ordering and the
      unchanged-field rule: the revision snapshot holds the **pre-edit stored**
      value while the post-update row holds the **sanitized new** value
      (Requirement 9.7); and a caller value **byte-identical** to the stored value
      is passed through unsanitized — a sparse update of the title alone leaves a
      stored `<script>` in `content` byte-identical (Requirement 9.14, Finding 3).
      Files: `internal/content/writeservices_test.go`
      Verify: `go test ./internal/content` — fails.
      _(Req 4.3, 4.10, 9.7, 9.14; Finding 3)_

- [x] 5.3 Implement the post-write wiring in
      `internal/content/writeservices.go`: add the `WithContentPolicy` option and a
      `policy *sanitize.Policy` field whose **default is `sanitize.New()`**, never
      nil, so an unwired service sanitizes rather than silently not sanitizing
      (fail closed). Add `sanitizeIncoming(actor auth.Principal, p, cur domain.Post)
      (domain.Post, error)` skipping any field byte-identical to the stored value
      (Finding 3). In `Create` (`writeservices.go:88`) sanitize all three fields
      after `auth.CanCreatePost` and before `s.w.Create`. In `Update`
      (`writeservices.go:122`) insert the `sanitizeIncoming` call **between** the
      existing `s.revisions.Snapshot(...)` line and the `cur.Title = ...` merge,
      failing closed on error before the merge. Declare `var ErrTitleEmpty` and
      return it when the title sanitizes to empty.
      Files: `internal/content/writeservices.go`
      Verify: `go test ./internal/content` — the 5.1 and 5.2 tests pass.
      _(Req 4.2, 4.3, 4.10, 1.8; Finding 3)_

## Phase 6 — Render data and the single comment cast site (`internal/render`, `internal/web`)

- [x] 6.1 Write a failing test in `internal/render/comments_test.go` asserting
      `render.CommentView.Content` is `template.HTML` (markup passes through) while
      `render.CommentView.Author` stays an auto-escaped `string`.
      Files: `internal/render/comments_test.go` (new or existing)
      Verify: `go test ./internal/render` — fails (field still `string`).
      _(Req 6.1, 6.5)_

- [x] 6.2 Change `render.CommentView.Content` to `template.HTML` in
      `internal/render/comments.go:9`, keeping `Author`/`AuthorURL` as `string`,
      and add the doc comment naming `sanitize.Policy` as its only sanctioned
      source (Requirement 6.7).
      Files: `internal/render/comments.go`
      Verify: `go test ./internal/render` — the 6.1 test passes.
      _(Req 6.1, 6.5, 6.7)_

- [x] 6.3 Write a failing test in `internal/web/comments_test.go` asserting the
      tier-A **render backstop** at the stored-comment site: a comment seeded into
      the in-memory comment fake containing `<script>` renders inert (no `<script>`
      element) while an allow-listed `<em>` survives, and the stored bytes are
      **unchanged** after rendering (no write-back). Simulates a pre-M10a row.
      Files: `internal/web/comments_test.go`
      Verify: `go test ./internal/web` — fails (`commentView` still escapes).
      _(Req 6.9, 6.10, 6.11, 9.15; Finding 2)_

- [x] 6.4 Rewrite `commentView` (`internal/web/comments.go:102`): remove
      `html.EscapeString`, apply the tier-A render backstop (sanitize the stored
      content at tier A unconditionally via the Server's `*sanitize.Policy`), build
      the single `template.HTML` cast, and add the new `TRUST BOUNDARY` comment
      naming the tier and the Policy invocation (Requirement 6.7). This is the
      tree's **only** comment cast site; the pending echo routes through it.
      Files: `internal/web/comments.go`
      Verify: `go test ./internal/web` — the 6.3 test passes.
      _(Req 6.1, 6.7, 6.9, 6.10, 6.11; Finding 2)_

## Phase 7 — Transports, pending-comment echo, and the inverted test (`internal/web`)

- [x] 7.1 Write failing tests in `internal/web/comments_test.go` /
      `internal/web/rest_comments_test.go` asserting both comment transports build
      the `Writer` correctly: `commentSubmit` and `handleRESTCommentCreate` use
      `sanitize.Anonymous()` by default and `sanitize.For(p)` when `PrincipalFrom`
      yields a principal, and map `content.ErrCommentEmpty` onto their existing
      400 branches (byte-identical response bodies). End-to-end assertion that a
      submitted `<script>` never reaches the writer (Requirement 9.5).
      Files: `internal/web/comments_test.go`, `internal/web/rest_comments_test.go`
      Verify: `go test ./internal/web` — fails.
      _(Req 4.1, 4.4, 4.8, 9.5)_

- [x] 7.2 Update `commentSubmit` (`internal/web/comments.go`) and
      `handleRESTCommentCreate` (`internal/web/rest_comments.go:150`) to build the
      `actor sanitize.Writer` via `PrincipalFrom`/`sanitize.For`/`sanitize.Anonymous`
      and pass it to `CommentService.Create`, and map `ErrCommentEmpty` to each
      transport's existing missing-field 400. No per-handler Policy call
      (Requirement 4.4).
      Files: `internal/web/comments.go`, `internal/web/rest_comments.go`
      Verify: `go test ./internal/web` — the 7.1 tests pass. _(Req 4.1, 4.4, 4.8)_

- [x] 7.3 Write failing admin/REST **post** transport tests
      (`internal/web/adminapi_posts_test.go`, `internal/web/rest_posts_test.go`)
      asserting end-to-end that content/excerpt are sanitized and the title is
      plain text through `adminapi_posts.go:72` and `rest_posts.go:207` — so a
      transport that wrote directly, bypassing `PostWriteService`, could not pass —
      and that `ErrTitleEmpty` maps to each transport's existing missing-title 400.
      Files: `internal/web/adminapi_posts_test.go`, `internal/web/rest_posts_test.go`
      Verify: `go test ./internal/web` — fails until wiring (task 8.2) lands.
      _(Req 4.2, 4.4, 9.5)_

- [x] 7.4 Write the two failing pending-echo tests (Requirement 9.8) in
      `internal/web/handlers_test.go`: `?comment=pending&content=<script>alert(1)
      </script>` renders **no** `<script>` element, and `?comment=pending&content=
      <em>hi</em>` renders a **real** `<em>`. Add the conditional-P7 assertions:
      submit through `commentSubmit`, replay the `Location` header on the **same
      session**, assert the echo equals the stored content for an anonymous
      submitter (tier A, agreement by P1 idempotence) and a logged-in editor
      (tier C, agreement by identity); and the safety assertion that an editor's
      tier-C value replayed **anonymously** is tier-A filtered.
      Files: `internal/web/handlers_test.go`
      Verify: `go test ./internal/web` — fails (`pendingEcho` not implemented).
      _(Req 6.4, 6.6, 9.8; P7; Finding 4)_

- [x] 7.5 Implement `pendingEcho(r *http.Request) (*render.CommentView, error)` in
      `internal/web/handlers.go`, replacing lines 209–211: read the GET's own
      principal via `PrincipalFrom`/`sanitize.For` (never a tier from the URL),
      `s.policy.Sanitize(sanitize.CommentContent, actor, query "content")`, fail
      closed on error, build the view through `commentView` (so there is exactly
      one cast), keep `Author` as an auto-escaped `string`, set `PendingEcho`. This
      is the one transport that calls the Policy, because the echo is not a write
      path. The backstop in `commentView` applies here too (Requirement 6.10).
      Files: `internal/web/handlers.go`
      Verify: `go test ./internal/web` — the 7.4 tests pass.
      _(Req 6.4, 6.6, 6.10; P7; Finding 4)_

- [x] 7.6 Invert `internal/web/comments_public_test.go:104`: replace the pinned
      doubly-escaped `&amp;lt;b&amp;gt;Hello&amp;lt;/b&amp;gt;` assertion with the
      post-M10a expectation, and add a comment naming this milestone and the reason
      so the inversion reads as intended, not as a test loosened to pass
      (Requirement 6.3). Confirm `internal/content/rest.go:334` keeps its
      `html.EscapeString` on REST `content.rendered` unchanged (Requirement 6.8).
      Files: `internal/web/comments_public_test.go`
      Verify: `go test ./internal/web` passes. _(Req 6.2, 6.3, 6.8)_

## Phase 8 — Single-instance construction and migration verification

- [x] 8.1 Verify no migration and no cross-vendor contract test were added:
      confirm no new file exists under `internal/storage/migrations/<vendor>/` and
      nothing was added to `internal/storage/storagetest` for this milestone,
      mirroring M8/M9a/M9b.
      Files: (verification only) `internal/storage/migrations/`,
      `internal/storage/storagetest/`
      Verify: `git status --porcelain internal/storage/migrations
      internal/storage/storagetest` shows no new files; `go test ./internal/storage/...`
      green. _(Req 1.3, 1.4, 9.11)_

- [x] 8.2 Wire a **single** `*sanitize.Policy` in `cmd/grimoire/main.go`: build
      `contentPolicy := sanitize.New()` once, pass it positionally to
      `content.NewCommentService(...)`, as `content.WithContentPolicy(contentPolicy)`
      to `content.NewPostWriteService(...)`, and as `WithContentPolicy(contentPolicy)`
      to the `web` Server builder so `pendingEcho` and `commentView` share the same
      instance. Add a test (or assert in an existing wiring test) that the same
      pointer reaches both services and the Server.
      Files: `cmd/grimoire/main.go`, `internal/web/server.go` (Server option),
      `cmd/grimoire/main_test.go` (if a wiring assertion is added)
      Verify: `go build ./...` and `go test ./cmd/... ./internal/web` green; the
      post transport tests from task 7.3 now pass. _(Req 1.1, 1.8, 4.4)_

## Phase 9 — Documentation and roadmap/README updates

- [x] 9.1 Rewrite the `TRUST BOUNDARY` comment at `internal/web/view.go:15-23`:
      state that write paths are sanitized at the boundary as of M10a (tier B for
      post content/excerpt without `unfiltered_html`, tier C for holders, title
      reduced to plain text), that the two casts at `view.go:37-38` now carry only
      sanitized post-M10a writes and pre-M10a/imported content, and that a new
      write path MUST route through `internal/sanitize` rather than adding a cast
      here. The `baseURLs`/`featured` paragraphs are unchanged; the casts stay
      structurally identical (Requirement 4.6).
      Files: `internal/web/view.go`
      Verify: `go build ./...` green; `go vet ./...` clean. _(Req 7.1, 4.6, 5.7)_

- [x] 9.2 Rewrite the "Trusted-content boundary" section of `docs/compatibility.md`
      (line 347) and remove the stale `bluemonday`-recommendation paragraph (line
      371). Carry, in order: the one-sentence guarantee in Requirement 5.5's exact
      terms ("every value written through grimoire from M10a onward is sanitized at
      the writer's tier", **not** "stored content is safe"); the three-tier summary
      table with the sentence that granting `unfiltered_html` grants stored-script
      capability (7.3); the plain-text `post_title` divergence at its true
      **14-element** width with the `<`/`>` loss (documented behavior, **not** a
      grimoire divergence, since WordPress loses them too) and the entity-sequence
      loss (grimoire-specific); divergences **D1–D15** each with direction; the
      pre-M10a **post** known limitation (5.5, 5.6); the comment-only tier-A render
      backstop with its no-op-for-imported-and-post-M10a argument (6.9–6.11, Finding
      2); the comment double-escape user-visible fix (6.2); the REST
      `content.rendered` asymmetry (6.8); and that **no new REST write route** is
      enabled (7.4).
      Files: `docs/compatibility.md`
      Verify: section renders and names all 15 divergences, the 7.1 provenance, and
      the three tiers. _(Req 2.12, 2.14, 3.4, 3.7, 3.8, 5.5, 5.6, 6.2, 6.8,
      6.9–6.11, 7.2, 7.3, 7.4, 7.5; Findings 2, 6, 7)_

- [x] 9.3 Confirm the `internal/render/comments.go` doc comment on
      `CommentView.Content` (added in task 6.2) names the Policy as its only
      sanctioned source, matching Requirement 6.7's convention.
      Files: `internal/render/comments.go`
      Verify: `go vet ./...` clean. _(Req 6.7)_

- [x] 9.4 Add this milestone's row to the specs index
      `.kiro/specs/README.md` and update the root `README.md` "Status" section:
      replace the sentence describing grimoire as having no sanitization, and add
      **no** claim of REST write parity this milestone does not deliver.
      Files: `.kiro/specs/README.md`, `README.md`
      Verify: both files reference M10a / the `10-rest-write-content-safety` spec
      and the Status section no longer says grimoire has no sanitizer.
      _(Req 7.6, 7.7)_

- [x] 9.5 Tick roadmap group **10.A** in
      `.kiro/specs/wordpress-core-parity-roadmap/tasks.md` with a note naming what
      shipped and stating that **10.B–10.G remain open** (so the M10 row does not
      read as complete). Tick as the work lands, not retroactively.
      Files: `.kiro/specs/wordpress-core-parity-roadmap/tasks.md`
      Verify: the 10.A box is checked with the 10.B–10.G-remain-open note present.
      _(Req 7.6)_

## Phase 10 — Final gates

- [x] 10.1 Run the full gate suite and fix anything red: `gofmt -l .` empty,
      `go vet ./...`, `go build ./...`, `go test ./...` (including `-race` on
      `internal/sanitize`). Confirm no test requires a database, network, live
      WordPress or environment gating (Requirement 9.12), and that the fuzz seed
      corpora run under plain `go test`.
      Files: (whole tree, verification only)
      Verify: all four commands green; `go test ./...` needs no `GRIMOIRE_TEST_*`
      env vars. _(Req 1.10, 9.12, 9.13)_
