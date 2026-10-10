# M10a — Write-Boundary Content Safety: Requirements

## Introduction

grimoire has no HTML sanitizer. Not a weak one, not one wired up in the wrong
place — none. `bluemonday` is named twice in the tree, at
`internal/web/view.go:22` and `docs/compatibility.md:371`, and in both places it
is a recommendation about a library that is not in `go.mod`, not imported and
never called. The module has **12 direct requirements** (`go.mod`), none of
which parses HTML.

Meanwhile three write paths already accept content and store it verbatim:

| Path | Site | Fields | Since |
|---|---|---|---|
| Public comment submission | `CommentService.Create` (`internal/content/comments.go:80`) | comment content | M4 |
| Admin/REST post create | `PostWriteService.Create` (`internal/content/writeservices.go:88`) | `post_content`, `post_title`, `post_excerpt` | M6 |
| Admin/REST post update | `PostWriteService.Update` (`internal/content/writeservices.go:122`) | `post_content`, `post_title`, `post_excerpt` | M6 |

The comment path is anonymous and public. The post path carries arbitrary
markup, is reachable from three transports — the admin API
(`internal/web/adminapi_posts.go:72`), REST posts
(`internal/web/rest_posts.go:207`) and REST comments
(`internal/web/rest_comments.go:150`), each of which carries content as a plain
`string`, unshaped in both directions — and its output reaches two
`template.HTML` casts at `internal/web/view.go:37-38`, under the tree's only
`TRUST BOUNDARY` comment (`view.go:15`). That comment says the quiet part out
loud: the casts are safe "ONLY because grimoire reads a trusted, read-only
WordPress database", and any future write path "MUST sanitize `post_content` and
`post_excerpt` (e.g. bluemonday) before they reach these casts." Those write
paths shipped in M4 and M6. The sanitization did not.

This milestone is where it does. It refines **roadmap group 10.A only** — the
capability-aware write-boundary content policy — from
[`../wordpress-core-parity-roadmap`](../wordpress-core-parity-roadmap),
tracing to roadmap Requirement 15. Groups 10.B, 10.C, 10.D, 10.E, 10.F and 10.G
are explicitly out of scope (see "Out of scope"), and **no requirement here
enables any new REST write route**. That ordering is not a preference: the
roadmap's `tasks.md:788-792` states that Requirement 15 must be designed,
implemented and tested before any task in 10.B or 10.C begins, and that "this
ordering is not optional: it is the load-bearing safety property of this
milestone."

`unfiltered_html` is this milestone's first consumer. The capability is declared
in `internal/auth/roles.go` — line 65 for editor, line 93 for administrator —
and is **checked nowhere in the tree today**. The five default tiers are
subscriber (`read`), contributor (`+edit_posts`, `delete_posts`), author
(`+upload_files`, `publish_posts`), editor (`+unfiltered_html`,
`moderate_comments`, `manage_categories`) and administrator
(`+unfiltered_upload`, `manage_options`, `create_users`).

Three terms are used throughout with fixed meanings:

- **the Policy** — the single sanitization component Requirement 1 defines.
- **field kind** — one of *post content*, *post excerpt*, *post title*,
  *comment content*. The Policy's behavior is a function of the field kind, not
  of the transport that delivered it.
- **tier A / tier B / tier C** — the three filtering tiers Requirement 2
  defines, selected by (field kind, whether the writer holds
  `unfiltered_html`): *comment content* from a writer lacking
  `unfiltered_html`, *post content* or *post excerpt* from a writer lacking it,
  and any field kind from an `unfiltered_html` holder. Authentication status is
  not an input.

## Inherited decisions

Settled at roadmap level and treated here as input, not as open questions:

| Decision | Source | Consequence for this milestone |
|---|---|---|
| The Policy is applied **at the write boundary**, covering `post_content`, `post_title`, `post_excerpt` and comment content — not only the routes 10.B/10.C would add. | Req 15.1; `design.md`'s "Existing write paths are in scope for the new policy too" | Requirement 4 puts the Policy in front of three write paths that already ship, which is the larger part of the work. |
| Sanitization is **capability-aware**, with an `unfiltered_html`-equivalent bypass for the most trusted roles, and never trusts input verbatim by default. | Req 15.2 | Requirement 2's three tiers, in which capability governs only the tier-C bypass and the field kind governs the tight/broad choice. |
| Content already present in an imported WordPress database keeps rendering as trusted HTML, unchanged. Read paths are not write paths. | Req 15.3; `design.md`'s "Imported content stays trusted" | Requirement 5's known limitation, and the reason there is no backfill. It holds for post fields structurally, and for comment content by argument: the tier-A render backstop (Requirements 6.9–6.11) applies the same list WordPress applied on the way in, so an imported comment renders unchanged. |
| The Policy is **one reusable component**, not duplicated per endpoint, matching the single-implementation-per-concern pattern of `pkg/extensions` and `internal/storage/wprepo`. | Req 15.4 | Requirement 1.1, and the placement constraint in 4.3. |
| The library is chosen **as part of the design, not before it**. | roadmap `tasks.md` group 10.A | Requirement 8 states constraints and forbids this document from naming a mandate. |

## Resolved decisions

Six questions were open when this spec was drafted. All six are answered, and a
seventh — raised by the design phase, decided by the project lead — is answered
in the same form. The answers are acceptance criteria below rather than
recommendations, and the table exists so the reasoning stays traceable from the
decision to the criterion that implements it.

| Question | Answer | Criteria |
|---|---|---|
| Write boundary only, or also a render-layer backstop? | **Write boundary only for *post content* and *post excerpt*.** No backstop over post fields, no provenance tracking, no audit command. The `template.HTML` casts at `view.go:37-38` stay structurally unchanged. For post fields the guarantee this milestone buys is therefore exactly "everything written from M10a onward is sanitized" — not "stored content is safe" — and that limit is itself a criterion. | 4.5–4.7, 5.1–5.7 |
| Comment content gains a `template.HTML` cast that did not exist before. Does *that* site get a backstop? | **Yes — a comment-only backstop, tier A, applied on render as well as on write.** Removing `html.EscapeString` from `commentView` (6.1) with no backstop (4.6) would make a comment row approved before M10a and containing `<script>` render as **live markup** where it renders as **inert text** today: `internal/web/handlers.go:201` lists only approved comments (`Statuses: []string{"1"}`) and passes them to `commentView` at `handlers.go:206`, and that escape is currently the only thing neutralizing a stored script in an approved comment. The backstop is a second application of **the same tier**, not a second policy and not a laxer pass. Its blast radius is bounded by the argument in 6.11: WordPress filters comment content with the same tight list tier A mirrors, so imported comments pass through unchanged and post-M10a comments are a no-op by P1. 5.2's no-backfill rule and 5.4's no-audit-command rule stay in force — the backstop makes both unnecessary rather than relaxing them. | 4.5–4.6, 5.6–5.8, 6.9–6.12 |
| How closely do the allow-lists follow WordPress? | **Mirror WordPress's two `kses` lists as closely as the chosen library permits**, so grimoire-authored content round-trips into WordPress unchanged, and select between them **by field kind** exactly as WordPress does: *comment content* from a writer lacking `unfiltered_html` gets the tight `$allowedtags` comment list (tier A), *post content* and *post excerpt* from a writer lacking it get the broad `$allowedposttags`-equivalent (tier B), and an `unfiltered_html` holder gets nothing at all (tier C). Authentication status selects no tier. Behavior and the parity constraint are specified here; the exhaustive per-tier element/attribute enumeration is a **design deliverable** (roadmap task 10.A says so explicitly). | 2.1–2.5, 2.10–2.12 |
| Does `post_title` allow inline markup, as WordPress's does? | **No — titles are plain text.** The Policy strips all markup from `post_title`; render keeps escaping, so `render.PostView.Title` stays a `string`. A deliberate, documented divergence from WordPress — and a **14-element-wide** one, not a 124-element one: WordPress registers `add_filter('title_save_pre', 'wp_filter_kses')`, which is the **tight** `$allowedtags` list, so a WordPress title permits the fourteen inline elements of tier A and not the broad `$allowedposttags` set (3.7). It avoids widening the trust boundary to a third field and avoids auditing every title emission site (`<title>`, archive headings, menu labels). The incoherent middle option — allow-listing title HTML that the renderer would escape into literal text anyway — is explicitly rejected. | 3.1–3.7 |
| Which library? | **Not decided here, by design.** Requirements state only behavioral constraints: WordPress `kses` parity per tier, deterministic output, no network access, allow-list based, maintained, acceptable dependency footprint. The design evaluates **`bluemonday` first** — it is already the named candidate in `view.go:22` and `docs/compatibility.md:371` — against those constraints, and records a fallback if it fails one. Its cost is `golang.org/x/net` plus two CSS packages, none of which is in `go.mod` today. | 8.1–8.7 |
| Comment content is escaped twice today. Does that stay? | **No — it is a defect, and this milestone fixes it.** `html.EscapeString` comes out of `commentView` (`internal/web/comments.go:102`) and `render.CommentView.Content` (`internal/render/comments.go:9`) becomes `template.HTML`; otherwise an allow-listed `<em>` that survived sanitization still reaches the reader as literal text. This **intentionally inverts** `internal/web/comments_public_test.go:104`, which pins the doubly-escaped output. It is a deliberate user-visible defect fix, not a regression. | 6.1–6.8 |
| What happens to the trust-boundary documentation? | **It is rewritten, because it becomes false.** `view.go:15`'s comment and the parallel claim under `docs/compatibility.md:347` "Trusted-content boundary" both assert that write-path content reaches the casts unsanitized and that adding sanitization is a future hardening step. After this milestone, write paths are sanitized and the remaining casts carry only pre-M10a or imported content. | 7.1–7.7 |

## Requirements

### Requirement 1 — One Policy, above storage, with no schema change

**User Story:** As a maintainer, I want exactly one place that decides what
markup is allowed, so a fourth write path cannot be added with a fifth opinion
about safety.

#### Acceptance Criteria

1. THE system SHALL provide the Policy as a **single component** consumed by
   every write path named in Requirement 4, and SHALL NOT duplicate
   allow-listing logic per endpoint, per transport or per field — satisfying
   roadmap Requirement 15.4 and matching the single-implementation-per-concern
   pattern of `pkg/extensions` and `internal/storage/wprepo`.
2. THE Policy SHALL be a **pure function** of (field kind, capability input,
   input string). IT SHALL perform no database access, no file access, no
   network access and no clock read, so every behavioral criterion in this
   document is assertable in a unit test with no database and no environment
   gating.
3. THE Policy SHALL sit **above storage**. No package under
   `internal/storage` and no repository implementation SHALL contain
   sanitization logic, and this milestone SHALL introduce **no cross-vendor
   storage contract requirement** — there is nothing vendor-specific about
   allow-listing HTML, and `internal/storage/storagetest` is the wrong place to
   assert it.
4. THE system SHALL require **no schema change and no migration**. The task
   list SHALL verify explicitly that no file is added under
   `internal/storage/migrations`, as M8's, M9a's and M9b's task lists did.
5. THE Policy's capability input SHALL be derived from the existing
   `auth.Principal` capability set via `Principal.Can`
   (`internal/auth/principal.go`). No second capability mechanism, no second
   role table and no per-endpoint capability string SHALL be introduced.
   `unfiltered_html` (`internal/auth/roles.go:65`, `:93`) becomes its first
   consumer in the tree.
6. THE Policy SHALL accept a **capability-less** caller, because public comment
   submission has no `Principal` at all (`internal/web/comments.go` reads
   `PrincipalFrom` and proceeds when it is absent) and `Principal.Can` SHALL
   still be answerable for that caller. An absent principal SHALL be answerable
   as holding **no capabilities** — and therefore as not holding
   `unfiltered_html` — and SHALL NOT be represented by a zero-value `Principal`
   that a future change could confuse with a logged-in user holding no
   capabilities; the two SHALL be distinguishable at the call site. THE
   accommodation is not about tier selection: an absent principal and a
   logged-in user holding no capabilities land in the **same tier**
   (Requirement 2.5), and it is only a correct capability answer for the absent
   principal that keeps that caller out of tier C.
7. THE Policy SHALL take the **field kind as an explicit argument** and SHALL
   NOT infer it from the input's shape, its length or the calling transport.
   Each field named in Requirement 4 SHALL map to exactly one field kind.
8. IF the Policy cannot process its input for any reason THEN THE system SHALL
   **reject the write** and SHALL NOT persist the unsanitized value. The
   Policy SHALL fail closed; there is no code path in which a sanitization
   failure results in stored content.
9. THE Policy SHALL be **safe for concurrent use** by multiple request
   goroutines, since every write path reaching it is served from the HTTP
   server's own goroutine pool.
10. THE work SHALL follow strict TDD — a failing test precedes each behavior —
    and `gofmt`, `go vet`, `go build ./...` and `go test ./...` SHALL be green
    at the end of every task, matching the standing convention of the previous
    nine milestones.

### Requirement 2 — Three filtering tiers mirroring WordPress's `kses` lists

**User Story:** As a site owner migrating between WordPress and grimoire, I want
content grimoire accepted to be content WordPress would also have accepted, so
the same post looks the same in both and neither rewrites the other's markup.

#### Acceptance Criteria

1. WHERE the field kind is *comment content* and the writer does **not** hold
   `unfiltered_html` THE Policy SHALL apply **tier A**, regardless of whether
   the writer is authenticated — an anonymous commenter and a logged-in
   subscriber, contributor or author all get tier A: an allow-list equivalent to
   WordPress's tight comment list `$allowedtags` — `a` (with `href`, `title`),
   `abbr` (with `title`), `acronym` (with `title`), `b`, `blockquote` (with
   `cite`), `cite`, `code`, `del` (with `datetime`), `em`, `i`, `q` (with
   `cite`), `s`, `strike`, `strong` — with **no `img`** and **no `class`
   attribute** on any element. THE binding phrase is "equivalent to WordPress's
   tight comment list `$allowedtags`"; the enumeration restates that list and
   SHALL NOT be read as narrowing it, so where the two disagree the list
   governs.
2. WHERE the field kind is *post content* or *post excerpt* and the writer does
   **not** hold `unfiltered_html` — including the default subscriber,
   contributor and author roles — THE Policy SHALL apply **tier B**: a broad
   allow-list equivalent to WordPress's
   `$allowedposttags`. Tier B is deliberately permissive: it SHALL allow `img`
   and SHALL allow the `style` attribute, and it SHALL exclude `iframe`,
   `form`, `input` and **every** `on*` event-handler attribute.
3. WHERE the writer holds `unfiltered_html` — the default editor and
   administrator roles — THE Policy SHALL apply **tier C**: no filtering at
   all. THE stored value SHALL be **byte-identical** to the submitted value,
   including when the submitted value is malformed HTML, matching WordPress,
   which exempts `unfiltered_html` holders from `kses` entirely rather than
   running a laxer list over their input.
4. THE **tier-C bypass** SHALL be determined by **capability, not role name**,
   so a custom role or an explicit capability grant carrying `unfiltered_html`
   selects tier C and a future role rename changes nothing.
   `auth.NewPrincipal`/`auth.CapabilitiesForRoles` already resolve explicit
   grants alongside role-derived capabilities, so no new resolution step is
   needed. Capability SHALL NOT select between tier A and tier B; that boundary
   is the field kind (2.1, 2.2, 2.5).
5. THE boundary between the tight list and the broad list SHALL be **the field
   kind**, and `unfiltered_html` SHALL be the **only** capability that changes
   filtering behavior — it selects the tier-C bypass and nothing else.
   Authentication status SHALL NOT be an input to tier selection. THIS is what
   makes grimoire's tier selection **identical to WordPress's**, which runs
   `wp_filter_kses` — the tight comment list — over comment content from anyone
   lacking `unfiltered_html`, runs `wp_filter_post_kses` — the broad list — over
   post content from anyone lacking it, and exempts `unfiltered_html` holders
   from `kses` entirely. A logged-in subscriber's comment SHALL therefore be
   filtered **exactly as an anonymous one is**, and tier selection stays a pure
   function of (field kind, `unfiltered_html`), which is what makes
   Requirement 2.4 and the tier-selection matrix test (Requirement 9.3) simple
   enough to be trustworthy.
6. THE three tiers SHALL be **monotone as allow-lists**: every element and every
   attribute allowed by tier A SHALL be allowed by tier B, and tier C SHALL
   allow everything — `allow(A) ⊆ allow(B) ⊆ allow(C)`. THIS is a containment
   property of the **lists themselves**, not a statement about writers of
   differing capability: since capability no longer moves a writer between tier
   A and tier B, monotonicity is what guarantees the tight list never permits
   markup the broad list forbids, so no input survives tier A only to be
   stripped at tier B.
7. THE Policy SHALL be **deterministic**: for a given (field kind, tier, input)
   the output SHALL be byte-identical on every invocation, in every process,
   independent of map iteration order, host locale, host timezone and wall-clock
   time.
8. THE Policy SHALL make **no network access** while sanitizing. IT SHALL NOT
   resolve a hostname, fetch a remote document or validate a URL by requesting
   it; URL handling SHALL be textual only.
9. IN tiers A and B THE Policy SHALL remove `<script>`, `<style>` elements,
   `<iframe>`, `<form>`, `<input>` and every `on*` event-handler attribute, and
   SHALL restrict every URL-bearing attribute to an allow-listed scheme set
   equivalent to WordPress's `$allowedprotocols` — which admits `http`,
   `https`, `mailto` and similar, and admits neither `javascript:` nor `data:`.
10. WHERE the chosen library supports CSS filtering THE Policy SHALL filter
    tier B `style` attribute values to a property allow-list rather than
    passing the declaration block through unexamined. THE exhaustive property
    list is a design deliverable, like the element/attribute lists.
11. THE **parity constraint** SHALL be that grimoire-sanitized output is a fixed
    point of WordPress's corresponding `kses` list: content grimoire accepted at
    a tier SHALL survive a WordPress save at the equivalent capability
    unchanged, so a site moving content back into WordPress sees no rewrite.
12. WHERE the chosen library cannot express a WordPress `kses` rule THE
    divergence SHALL be **enumerated** in the design and recorded in
    `docs/compatibility.md` (Requirement 7.5), naming the element, attribute or
    protocol and the direction of the difference. An unstated approximation of
    `kses` is not acceptable; a stated one is.
13. THE exhaustive per-tier element and attribute enumeration SHALL be produced
    in `design.md`, per roadmap task 10.A, and SHALL NOT be treated as missing
    from this document.
14. THE enumeration's **verified provenance** SHALL be stated as WordPress
    **7.1** — the version of the container available to this project, and
    therefore the version the `kses` arrays and the parity oracle
    (Requirement 9.10) are captured from, because an oracle that cannot be
    re-run is not an oracle. THE capture SHALL NOT be described as being from
    WordPress 6.x. 7.1's additions over 6.x — `dialog`, `search`, `data`, the
    `command`/`commandfor`/`popovertarget` attributes on `button`, the MathML
    block, the additional SVG presentation properties in `safe_style_css` and
    `--*` custom properties — are each either excluded from tier B by design or
    inert markup that makes grimoire **more** permissive than a 6.x site rather
    than less, so the parity fixed point of Requirement 2.11 is unaffected in
    the direction that matters and holds against 6.x as well. WHERE 6.x parity
    is wanted specifically, the capture SHALL be re-runnable against a 6.x
    image and diffable against the recorded provenance.

### Requirement 3 — `post_title` is plain text

**User Story:** As a maintainer, I want the set of fields that bypass
`html/template` escaping to stay as small as it is today, so reviewing the trust
boundary stays a finite job.

#### Acceptance Criteria

1. THE Policy SHALL **strip all markup** from the *post title* field kind. No
   element and no attribute SHALL be allow-listed for titles at any tier below
   C.
2. WHEN a title contains a tag THE Policy SHALL remove the tag and **preserve
   its text content**, so `<em>Hello</em>` becomes `Hello` rather than an empty
   string.
3. THE Policy's title output SHALL contain **no markup delimiters** — no `<`
   and no `>` — and SHALL **not emit HTML entities** in their place. Emitting
   `&lt;` would be escaped a second time by `html/template` at every title
   emission site and reach the reader as the literal text `&lt;`, which is the
   same class of defect Requirement 6 removes from comments.
4. THE consequence SHALL be stated rather than discovered, in both of its parts.
   First: a title whose *literal text* contains `<` or `>` — `5 < 6` — loses
   those characters. This is deliberate, narrow data loss on a rare input,
   chosen over re-encoding that the renderer would mangle, and it is **not a
   divergence from WordPress**: design verified that
   `wp_kses_post("5 < 6 & 7 > 2")` returns `5  2`, so WordPress already loses
   bare angle brackets in text. IT SHALL therefore be recorded in
   `docs/compatibility.md` (Requirement 7.5) as documented behavior rather than
   as a grimoire-specific divergence. Second: because title normalization
   iterates to a fixed point (3.8), a **literal HTML entity sequence** in a
   title's text is lost too — `&amp;lt;b&amp;gt;` decodes across passes to `b` —
   so the loss is a strict superset of the `<`/`>` case. THE entity loss IS
   grimoire-specific and SHALL be recorded as such (Requirement 7.5).
5. `render.PostView.Title` SHALL **stay a `string`** (`internal/render/view.go`),
   auto-escaped by `html/template` at every emission site. THE trust boundary
   SHALL NOT widen to a third post field, and **no title emission site** —
   `<title>`, archive headings, menu labels, admin views, REST `title.rendered`
   — SHALL be audited or changed by this milestone.
6. THE middle option SHALL be **explicitly rejected**: allow-listing a set of
   inline elements for titles while `PostView.Title` remains an auto-escaped
   `string` would store markup that every reader sees as literal text. It
   combines the cost of an allow-list with none of its benefit, and SHALL NOT be
   implemented as a compromise between 3.1 and WordPress's behavior.
7. THE divergence from WordPress — which permits inline markup in
   `post_title` — SHALL be recorded as a grimoire decision with its rationale
   (Requirement 7.5), not as a parity gap awaiting a fix, and SHALL be stated at
   its **true width**: WordPress registers
   `add_filter('title_save_pre', 'wp_filter_kses')`, which applies the **tight**
   `$allowedtags` list and not `$allowedposttags`, so a WordPress title carries
   **14 inline elements, not 124**. No statement in this document or in
   `docs/compatibility.md` SHALL imply that grimoire is diverging from a
   broad-markup title. THE narrow gap strengthens 3.1 rather than weakening it:
   plain text is a subset of a 14-element list, so the parity fixed point of
   Requirement 2.11 still holds for titles.
8. THE title normalization SHALL **iterate to a fixed point**: it SHALL repeat
   the strip-decode-delete-delimiters step until the output of a pass equals its
   input. THE iteration SHALL be **bounded at 16 passes**, and IF the output has
   not converged within that bound THEN THE Policy SHALL **fail closed** under
   Requirement 1.8 and the write SHALL be rejected. A single pass satisfies
   3.1–3.4 but is **not idempotent** — decoding is not idempotent on text that
   still looks encoded, so `&amp;lt;b&amp;gt;` → `&lt;b&gt;` → `b` — and would
   therefore fail property P1 for exactly the pre-encoding input class P1 exists
   to catch. THE bound, and the closed failure beyond it, SHALL be stated rather
   than left to an unbounded loop over attacker-supplied input.

### Requirement 4 — Which write paths the Policy covers

**User Story:** As a site owner, I want the content that already reaches my
database through the admin UI and the public comment form to be sanitized, not
just whatever routes a future milestone adds.

#### Acceptance Criteria

1. WHEN a comment is submitted through `CommentService.Create`
   (`internal/content/comments.go:80`) THE system SHALL sanitize the comment
   content through the Policy **before persistence**, at the tier Requirement 2
   selects for the submitter. This path is anonymous and public and has been
   unsanitized since M4.
2. WHEN a post or page is created through `PostWriteService.Create`
   (`internal/content/writeservices.go:88`) THE system SHALL sanitize
   `post_content`, `post_excerpt` and `post_title` through the Policy before
   persistence.
3. WHEN a post or page is updated through `PostWriteService.Update`
   (`internal/content/writeservices.go:122`) THE system SHALL sanitize the
   **caller-supplied** `post_content`, `post_excerpt` and `post_title` before
   they are merged into the stored record — which is after the authorization and
   optimistic-concurrency checks and **after** the revision snapshot of the
   pre-edit state. `Update` snapshots `cur` before mutating it
   (`writeservices.go`'s `s.revisions.Snapshot(ctx, cur, ...)`), so this
   ordering leaves the snapshot holding exactly the historical stored value it
   holds today, and leaves the persisted row holding the sanitized new value.
   Neither the snapshot nor the stored row SHALL ever hold an unsanitized
   caller value. **"Caller-supplied" is not directly observable at this site**;
   its operational definition is criterion 4.10, which SHALL be read as part of
   this criterion rather than as an exception to it.
4. THE Policy SHALL be invoked such that **every transport inherits it without
   its own call**: the admin API (`internal/web/adminapi_posts.go:72`), REST
   posts (`internal/web/rest_posts.go:207`) and REST comments
   (`internal/web/rest_comments.go:150`) SHALL all be covered by the
   invocations in 4.1–4.3, and adding a fourth transport over the same write
   services SHALL require no new sanitization call. A per-handler invocation
   would restate the policy three times and would be the duplication roadmap
   Requirement 15.4 forbids.
5. THE Policy SHALL be applied on **write paths only, for the *post content*,
   *post excerpt* and *post title* field kinds**. No read path, no listing, no
   REST serializer and no template helper SHALL sanitize those three, so roadmap
   Requirement 15.3's "imported content keeps rendering as trusted HTML" holds
   unchanged and M1–M7's read behavior does not regress. THE single exception is
   *comment content*, which is sanitized on render as well as on write
   (Requirements 6.9–6.11); it is an exception with a stated argument
   (Requirement 6.11), not a general license for read-path sanitization, and no
   other field kind SHALL acquire one under this milestone.
6. THE `template.HTML` casts at `internal/web/view.go:37-38` SHALL remain
   **structurally unchanged** — same fields, same types, same cast sites. Only
   the comment addressing them changes (Requirement 7.1). There SHALL be **no
   render-layer backstop over *post content* or *post excerpt***: a second,
   laxer sanitization pass at render would create a second policy to reason
   about, and even a same-tier pass would visibly rewrite trusted imported
   content, because tier B mirrors the broad `$allowedposttags` list and a broad
   list is wide enough that re-filtering imported markup changes what an
   operator already publishes — which roadmap Requirement 15.3 forbids. THAT
   reasoning is the whole ground of this rule, and it is **why** the rule is
   narrowed to post fields rather than abandoned: it does not transfer to
   comment content, for the reasons criterion 6.11 states.
7. THE system SHALL NOT enable any new REST write route. The
   `restNotImplemented` registrations in `internal/web/rest_media.go` and
   `internal/web/rest_users.go` SHALL remain unchanged and SHALL continue to
   respond `501`. Media writes (10.B), user writes (10.C) and user profile
   fields (roadmap Requirement 17.4) are out of scope.
8. WHEN sanitization empties a field whose input was non-empty — a comment body
   consisting entirely of disallowed markup — THE system SHALL evaluate the
   existing required-field validation **against the sanitized value**, so such a
   submission is handled exactly as an empty submission is handled today: the
   public comment handler's `author == "" || email == "" || commentContent == ""`
   check (`internal/web/comments.go`) rejects it with `400`. THE Policy itself
   SHALL NOT reject content; it reports only what survived, and the calling path
   keeps owning validation.
9. THE Policy SHALL apply to the comment fields carrying content only. Comment
   author name, email and URL SHALL keep their existing validation and their
   existing auto-escaped rendering; this milestone does not restate M4's
   comment-field validation.
10. WHERE a field's caller value is **byte-identical to the stored value** on an
    update THE system SHALL **skip sanitization of that field** and SHALL write
    the stored bytes through unchanged. THIS is the operational definition of
    4.3's "caller-supplied", and it is a **rule, not an optimization**.
    `PostWriteService.Update` receives a fully-populated `domain.Post`, and
    `parseRESTPostWrite` in `internal/web/rest_posts.go` deliberately merges the
    stored record as its base so a sparse `PATCH {"title":"x"}` does not wipe
    content — so on that request `p.Content == cur.Content`, and sanitizing it
    would rewrite stored pre-M10a or imported content in place, which
    Requirement 5.2 forbids in terms and Requirement 5.1 forbids in intent. THE
    safety argument SHALL be recorded with the rule: a write whose value is
    already the value in the row **introduces no new bytes**, so the rule cannot
    admit unsanitized content, and every value that **changes** the row is
    sanitized. THE rule SHALL be asserted directly by the test Requirement 9.14
    requires, not left as a comment.
11. THE rejected alternative SHALL be recorded as a **named follow-up rather
    than as part of this milestone**: threading a field mask, or pointer-typed
    fields, from the transports into `PostWriteService.Update` so the service can
    distinguish an omitted field from a supplied one. IT is the more correct
    architecture and the right eventual shape, but it changes the signature of
    the service both write transports share and ripples into the admin API's
    deliberate full-replacement contract, so it SHALL NOT be attempted here.

### Requirement 5 — Pre-existing content is never retroactively sanitized

**User Story:** As a site owner, I want to know exactly what this milestone
protects me from, so I do not mistake "new writes are sanitized" for "everything
stored is safe".

#### Acceptance Criteria

1. Content stored **before** this milestone SHALL NOT be re-sanitized in
   storage. This covers both content imported from a WordPress database (roadmap
   Requirement 15.3) **and** content authored through M6's admin CRUD or M5's
   REST comment write, which were unsanitized when they ran. *Post content* and
   *post excerpt* SHALL additionally continue to **render verbatim** through the
   existing `template.HTML` casts at `internal/web/view.go:37-38`, which already
   render stored bytes verbatim today, so for post fields nothing changes in
   either direction. *Comment content* SHALL likewise keep its stored bytes
   untouched, but SHALL be filtered at tier A **on the way out**
   (Requirements 6.9–6.11) rather than rendered verbatim, because its render site
   is new and was previously escaped.
2. THE system SHALL perform **no backfill**: no migration, no startup scan, no
   lazy re-sanitize-on-read and no write-back of sanitized content over stored
   rows.
3. THE system SHALL perform **no provenance tracking**. No column, no postmeta
   key and no commentmeta key SHALL record whether a stored row was written
   before or after the Policy existed. A provenance flag would be a schema
   change (Requirement 1.4 forbids it), would be unreliable for imported rows,
   and would only be load-bearing for the **post-field** render backstop
   Requirement 4.6 rejects. THE comment backstop of Requirements 6.9–6.11 SHALL
   NOT depend on provenance either: it applies tier A **unconditionally**, to
   every comment row, which is what makes it implementable without knowing when
   or by whom a row was written.
4. THE system SHALL NOT gain an audit command. No `grimoire-cli` subcommand
   scanning stored content for unsanitized markup is in scope.
5. THE guarantee this milestone establishes SHALL be stated **precisely** in
   `docs/compatibility.md` (Requirement 7.5) as *"every value written through
   grimoire from M10a onward is sanitized at the writer's tier"*, and SHALL NOT
   be described as stored content being safe.
6. THE consequence SHALL be recorded as a **known limitation, scoped to post
   fields**: a site whose pre-M10a authored *post content* or *post excerpt*
   already contains a stored script is not repaired by this milestone. For post
   fields this milestone closes the inflow; it does not drain the pool. THAT
   framing SHALL NOT be applied to comments: a pre-M10a comment row is **not**
   left exposed, because the tier-A backstop of Requirements 6.9–6.11 filters it
   on render. THE stored row is still not repaired — nothing is written back —
   but it can no longer reach a reader as live markup.
7. THE remaining `template.HTML` casts SHALL therefore be documented
   (Requirement 7.1) with the **two cases distinguished**, because they are not
   alike: the two post casts at `internal/web/view.go:37-38` carry **pre-M10a and
   imported content verbatim**, and are the casts a future reader must weigh when
   deciding whether a post backstop is warranted; the new comment cast
   (Requirement 6.7) carries **tier-A-sanitized content, always** — regardless of
   when the row was written, by whom, or whether it was imported — and therefore
   needs no such judgement. Describing all three as carrying unsanitized content
   generally, or all three as equally guarded, would be wrong in opposite
   directions.
8. Criterion 5.2's no-backfill rule and criterion 5.4's no-audit-command rule
   SHALL **remain in force** after the comment backstop is added. THE backstop
   makes both **unnecessary rather than relaxed**: it removes the exposure a
   one-shot `grimoire-cli comments sanitize` pass would have been written to
   close, so there is no longer a reason to reopen either rule.

### Requirement 6 — Comment content stops being escaped twice

**User Story:** As a commenter, I want the `<em>` the site told me I may use to
render as emphasis, not as the four characters `<em>`.

#### Acceptance Criteria

1. THE double escape SHALL be removed: `html.EscapeString` SHALL come out of
   `commentView` (`internal/web/comments.go:102`) and
   `render.CommentView.Content` (`internal/render/comments.go:9`) SHALL become
   `template.HTML`.
2. THE reason SHALL be recorded as a **deliberate user-visible defect fix, not
   a regression**, so review does not misread it. Today comment content is
   escaped twice — once in `commentView` and again by `{{.Content}}` in
   `themes/default/templates/partials/comments.tmpl:10`, which auto-escapes
   because the field is a `string` — so a commenter who types `<b>Hello</b>`
   sees the literal markup as text. Leaving `EscapeString` in place after
   sanitization would make the allow-list pointless: everything tier A
   preserves would still reach the reader as text.
3. `internal/web/comments_public_test.go:104`, which pins the doubly-escaped
   output `&amp;lt;b&amp;gt;Hello&amp;lt;/b&amp;gt;`, SHALL be **intentionally
   inverted**, and the replacing assertion SHALL carry a comment naming this
   milestone and the reason, so the inversion reads as intended rather than as
   a test loosened to make a change pass.
4. **SAFETY-CRITICAL.** The pending-comment echo at
   `internal/web/handlers.go:210-211` builds a `render.CommentView` from the raw
   query-string `author` and `content` values of a redirect this handler itself
   issued. WHEN `CommentView.Content` becomes `template.HTML` THE echoed value
   SHALL pass through **the same Policy, at the same tier and field kind** as a
   stored comment — the tier the **actual submitter's** capabilities select
   under Requirement 2, which is tier A for any submitter lacking
   `unfiltered_html` and tier C for a holder, never a tier inferred from the
   echo path looking anonymous. An unsanitized `template.HTML` at that site
   would be a
   **reflected-XSS vector introduced by this fix** — the query string is
   attacker-supplyable in a link, and today's `html.EscapeString` at
   `handlers.go:211` is the only thing standing in front of it.
5. `render.CommentView.Author` SHALL **stay a `string`** and SHALL keep being
   auto-escaped, at both the stored-comment and pending-echo sites. The trust
   boundary widens to comment *content* only.
6. THE echo and the eventually-published comment SHALL agree **conditionally,
   and the condition SHALL be stated rather than dropped**. WHERE the echoing
   GET carries the submitter's own principal — which is the case for the
   redirect the handler itself issues, including the logged-in-editor tier-C
   case — THE echoed value and the stored value SHALL be byte-identical, since
   both are the Policy's output for the same field kind and the same
   submitter-selected tier. IF the echoing GET does **not** carry the submitter's
   principal — a session that expired between the POST and the GET, or a link
   opened by a third party — THEN agreement SHALL NOT be claimed, and THE echoed
   value SHALL instead be sanitized at the **requesting** principal's tier, which
   is never laxer than that principal's own writing tier. Total agreement and the
   absence of reflected XSS are **mutually exclusive** given a query-string echo,
   because the echo is a separate GET whose only non-forgeable tier input is its
   own principal and whose only other channel is the attacker-controlled query
   string; this milestone SHALL take safety, so the safety property holds
   unconditionally and the agreement property holds under its stated
   precondition. NEITHER SHALL be stated as total. THE two mechanisms that would
   make agreement total — a server-side flash keyed to the session with
   `author`/`content` dropped from the redirect, or echoing by comment ID and
   rendering through the stored-comment path — SHALL be recorded as follow-ups
   rather than adopted here, since Requirement 6.4 hardens the existing echo
   rather than replacing the mechanism.
7. THE new `template.HTML` cast site SHALL carry its own `TRUST BOUNDARY`
   comment naming the tier that produced the value and the Policy invocation
   that guarantees it, matching the existing convention at `view.go:15`. The
   tree gains a third cast site, and an undocumented one would undo the reason
   the first two are reviewable.
8. `internal/content/rest.go:334` SHALL keep its `html.EscapeString` on the REST
   comment `content.rendered` field, **unchanged and deliberately**. REST
   `content.rendered` fidelity is roadmap group 10.D, which is out of scope, and
   changing it here would widen a REST response contract this milestone has not
   specified. THE resulting asymmetry — the public page renders allow-listed
   comment markup while the REST field returns it escaped — SHALL be recorded in
   `docs/compatibility.md` (Requirement 7.5) so it reads as a scoped deferral
   rather than an inconsistency nobody noticed.
9. **SAFETY-CRITICAL.** *Comment content* SHALL be sanitized at **tier A on
   render**, in addition to being sanitized on write. THIS is a **second
   application of the same tier** — not a second policy, not a laxer pass and not
   a different allow-list — so there remains exactly one component and exactly
   one set of lists to reason about (Requirement 1.1). THE render-time tier SHALL
   be tier A **unconditionally**, independent of the rendering request's
   principal and of the tier the stored value was written at, because provenance
   is not tracked (Requirement 5.3) and an unconditional tight list is therefore
   the only rule the render site can implement correctly. THE exposure this
   closes is precise: removing `html.EscapeString` (6.1) with no backstop would
   make a comment row approved before M10a and containing `<script>` render as
   **live markup** where it renders as **inert text** today, because
   `internal/web/handlers.go:201` lists only approved comments
   (`Statuses: []string{"1"}`) and passes them to `commentView` at
   `handlers.go:206`, so that escape is currently the only thing neutralizing a
   stored script in an approved comment.
10. THE backstop SHALL apply at **both** comment render sites: the
    stored-comment path via `commentView` (`internal/web/comments.go:102`) and the
    pending-comment echo (`internal/web/handlers.go:210-211`). Neither site SHALL
    cast a comment value to `template.HTML` without it. AT the echo site the
    backstop is in addition to, not instead of, the submitter-tier sanitization
    Requirement 6.4 requires.
11. THE reasoning that grounds the post-field rule in 4.6 SHALL be recorded as
    **not transferring to comments**, so the narrowing reads as reasoned rather
    than as the rule being abandoned. WordPress filters comment content with the
    **same** tight `$allowedtags` list tier A mirrors, so a tier-A backstop
    applies the list WordPress already applied rather than a different one. THREE
    consequences follow, and each SHALL be stated: imported WordPress comments are
    already tier-A-shaped and pass through **unchanged**; comments written after
    M10a are already tier-A-sanitized, so property P1's idempotence makes the
    backstop a **no-op** for them; and the only rows the backstop alters are
    **pre-M10a grimoire-authored** comments, which is exactly the exposure being
    closed. THE tier-C case SHALL be stated too rather than left implicit: a
    comment written by an `unfiltered_html` holder is still **stored**
    byte-identically (Requirement 2.3 governs storage), and is **rendered**
    through tier A like every other comment, so tier C buys an editor no
    additional markup in a rendered comment body.
12. THE performance consequence SHALL be stated rather than assumed: comment
    rendering now sanitizes **once per comment per request**, where it previously
    escaped a string. Running the tight tier-A list over typically-short comment
    bodies is expected to be negligible against the database round trip that
    fetched them, so this milestone SHALL accept it and the design SHALL NOT be
    required to add caching, precomputation or a stored-sanitized column for it.
    IF measurement on a realistic comment thread contradicts that expectation
    THEN the design SHALL record the measurement and the mitigation rather than
    leaving the cost unexamined.

### Requirement 7 — Correct the statements this milestone invalidates

**User Story:** As someone evaluating grimoire's security posture, I want the
trust-boundary comments and the compatibility doc to describe what the code does
now, not what it did before the sanitizer existed.

#### Acceptance Criteria

1. THE `TRUST BOUNDARY` comment at `internal/web/view.go:15-23` SHALL be
   rewritten. Its claim that the casts are safe only because the database is
   trusted, and its instruction that "any future write/admin path ... MUST
   sanitize post_content and post_excerpt (e.g. bluemonday) before they reach
   these casts", both become **false** when this milestone lands: the write
   paths exist, they are sanitized, and the library is chosen. THE replacement
   SHALL state that write paths are sanitized at the boundary as of M10a and
   that the remaining casts carry pre-M10a and imported content
   (Requirement 5.7).
2. `docs/compatibility.md`'s "Trusted-content boundary" section
   (`docs/compatibility.md:347`) SHALL be rewritten for the same reason. Two
   statements there are specifically false after this milestone: that content
   submitted through the M5–M7 write paths is rendered "with no additional HTML
   sanitization at the render layer today", and that "adding sanitization (e.g.
   `bluemonday`) at those sites remains the recommended hardening path". THE
   operator-trust advice built on them SHALL be replaced rather than edited
   around.
3. THE rewritten section SHALL name the **three tiers** and what each allows in
   summary, so an operator can decide which roles to grant without reading the
   design.
4. THE rewritten section SHALL state that **no new REST write route is enabled**
   by this milestone — `/wp-json/wp/v2/media` and `/wp-json/wp/v2/users` writes
   still respond `501` — so a reader does not infer that 10.B or 10.C shipped
   alongside the policy.
5. `docs/compatibility.md` SHALL additionally record, each as its own stated
   decision rather than an implementation detail: every WordPress `kses` rule
   the chosen library cannot express (Requirement 2.12); the enumeration's
   WordPress **7.1** provenance and why 6.x parity is unaffected
   (Requirement 2.14); the plain-text `post_title` divergence, stated at its true
   **14-element** width (Requirement 3.7), together with the **entity-sequence**
   data loss the fixed-point iteration causes (Requirements 3.4, 3.8); the
   known limitation that pre-M10a **post** content is never retroactively
   sanitized (Requirements 5.5, 5.6); the **comment-only tier-A render
   backstop**, stated as closing an exposure the double-escape fix would
   otherwise have opened, with its no-op-for-imported-and-post-M10a-rows argument
   (Requirements 6.9–6.11); the comment double-escape fix as a user-visible
   change (Requirement 6.2); and the REST `content.rendered` asymmetry
   (Requirement 6.8). THE `<`/`>` loss in title text SHALL be recorded as
   **documented behavior and not as a grimoire-specific divergence**, because
   WordPress loses those characters too (Requirement 3.4); listing it among the
   divergences would misinform the operator it is written for.
6. `../wordpress-core-parity-roadmap/tasks.md` group **10.A** SHALL be ticked
   with a note naming what shipped, and `../README.md`'s milestone index SHALL
   gain this milestone's row. Checkboxes SHALL be ticked as the work lands, not
   retroactively. THE note SHALL state that 10.B–10.G remain open, since the
   roadmap's M10 row otherwise reads as complete.
7. THE root `README.md` "Status" section SHALL be updated where it describes
   grimoire as having no sanitization, and SHALL NOT claim REST write parity
   this milestone does not deliver.

### Requirement 8 — Library selection constraints, not a library mandate

**User Story:** As a maintainer of a deliberately lean module, I want the
dependency decision made against written constraints in the design, with its
cost recorded, rather than assumed by the requirements.

#### Acceptance Criteria

1. THIS document SHALL **not mandate a library**. THE choice SHALL be made in
   `design.md`, per roadmap task 10.A's instruction to choose and vet a concrete
   library "as part of that design, not before".
2. THE chosen mechanism SHALL satisfy every following constraint, and the design
   SHALL record the evidence for each: **allow-list based** (never deny-list);
   able to express **per-element attribute allow-lists** and **URL-scheme
   allow-lists**; **deterministic** output (Requirement 2.7); **no network or
   filesystem access** (Requirement 2.8); parses HTML with a **real parser**,
   not regular expressions; **actively maintained**; and a dependency footprint
   acceptable against a module with 12 direct requirements.
3. THE design SHALL evaluate **`bluemonday` first**, because it is already the
   named candidate in `internal/web/view.go:22` and `docs/compatibility.md:371`
   and evaluating something else first would leave those references
   unexplained.
4. THE design SHALL record `bluemonday`'s **cost** explicitly:
   `golang.org/x/net` plus two CSS packages, none of which appears in `go.mod`
   today — `go.mod`'s indirect block carries no `golang.org/x/net` entry.
5. IF `bluemonday` fails any constraint in 8.2 THEN THE design SHALL name the
   constraint it failed and the fallback taken — another vetted library, or an
   in-tree allow-list sanitizer over `golang.org/x/net/html` — rather than
   leaving the decision undocumented or silently reversed during
   implementation.
6. THE dependency SHALL be added at a **pinned version** in `go.mod`/`go.sum`
   only. No build-time code generation and no runtime plugin loading SHALL be
   introduced, consistent with the compiled-binary, no-dynamic-loading posture
   `pkg/extensions` established in M5.
7. A **deny-list or regular-expression-based** filter SHALL NOT be used, at any
   tier, for any field kind. Tag-stripping by regular expression is the failure
   mode this requirement exists to foreclose, including for the plain-text title
   path of Requirement 3.

### Requirement 9 — Test coverage

#### Acceptance Criteria

1. Every behavior in this document SHALL land **test-first**: a failing test,
   then the implementation that passes it. The task list SHALL be ordered so
   this is visible rather than claimed.
2. THE per-tier allow-lists SHALL be covered by table-driven unit tests with
   **no database**: for each tier and field kind, a case per allow-listed
   element and attribute, and a rejection case per `<script>`, `<style>`,
   `<iframe>`, `<form>`, `<input>`, an `on*` handler, a `javascript:` URL and a
   `data:` URL.
3. THE tier-selection matrix SHALL be covered by a test enumerating anonymous,
   subscriber, contributor, author, editor and administrator against each field
   kind and asserting the selected tier. IT SHALL build principals through the
   real `auth.CapabilitiesForRoles`/`auth.NewPrincipal` rather than a
   hand-written capability map, so a future change to `roles.go` surfaces here;
   it SHALL include a **custom role granted `unfiltered_html`** selecting tier C
   (Requirement 2.4); and it SHALL assert that **subscriber, contributor and
   author commenting all select tier A** (Requirement 2.5), which is the cell an
   earlier draft of this spec got wrong and the one a regression would most
   plausibly reintroduce.
4. EACH write path SHALL have a test proving the Policy is applied: a comment
   created through `CommentService.Create` stores sanitized content; a post
   created and a post updated through `PostWriteService` store sanitized
   content and excerpt and a plain-text title. THE assertions SHALL be made on
   what reached the writer port, so a future refactor that moves sanitization
   out of the write service fails them.
5. EACH transport SHALL have a test asserting the same property end-to-end —
   admin API, REST posts, REST comments — so a transport that bypassed the write
   service and wrote directly could not pass.
6. THE tier-C bypass SHALL have a test asserting **byte-identical**
   persistence for an editor and an administrator, using input that tier B would
   demonstrably alter, so a bypass that accidentally normalizes whitespace or
   re-serializes markup fails.
7. THE update ordering of Requirement 4.3 SHALL have a test asserting that the
   revision snapshot holds the pre-edit **stored** value while the post-update
   row holds the sanitized new value.
8. THE pending-comment echo SHALL have its own tests (Requirement 6.4): a GET
   carrying `?comment=pending&content=<script>alert(1)</script>` renders no
   `<script>` element, and the same request with an allow-listed `<em>` renders
   a real `<em>`. A test asserting only the second would pass a vulnerable
   implementation.
9. THE correctness properties below SHALL each have an executable test.
10. THE WordPress parity claim (Requirement 2.11) SHALL be backed by a
    **fixture set** of `kses`-relevant inputs with WordPress's own outputs,
    captured once and committed with their provenance recorded, rather than by
    an assertion nobody can run. Every fixture where grimoire's output differs
    SHALL be enumerated as a documented divergence (Requirement 2.12) rather
    than deleted from the fixture set.
11. No new migration SHALL be added, and the task list SHALL verify this
    explicitly (Requirement 1.4).
12. No test in this milestone SHALL require a database, a network, a live
    WordPress instance or environment gating. The Policy is pure
    (Requirement 1.2), so nothing here needs the env-gated real-database
    harness M9a and M9b use.
13. `gofmt`, `go vet`, `go build ./...` and `go test ./...` SHALL be green.
14. THE unchanged-field rule of Requirement 4.10 SHALL have a test asserting that
    a `PATCH` which does not touch content — a sparse update of the title alone —
    leaves a stored `<script>` in that row **byte-identical**, so a future change
    that sanitizes the merged base value fails here rather than in production.
15. THE comment render backstop (Requirements 6.9–6.11) SHALL have a test proving
    that a comment row containing `<script>` **inserted directly into storage**,
    simulating a pre-M10a row that never passed through the Policy, renders
    **inert**: no `<script>` element reaches the rendered page. IT SHALL be
    paired with an assertion that the same row's stored bytes are **unchanged**
    after rendering, so the backstop cannot be implemented as the write-back
    Requirement 5.2 forbids. "Inserted directly into storage" SHALL mean seeded
    through the existing in-memory comment fake rather than a real database, so
    criterion 9.12 continues to hold.

## Correctness properties

These are the properties worth asserting over **generated** input rather than
over a table of examples, because each one quantifies over all inputs and the
interesting failures are inputs nobody thought to tabulate — nested tags,
unclosed tags, mixed-case attribute names, doubled encodings, hostile URL
schemes. A sanitizer is exactly the kind of component where an example table
reports success on the cases its author already understood.

No property-testing dependency is required, and none SHALL be added for this:
the properties are expressible with the standard library — `testing/quick` for
generated structured input and Go's native fuzzing (`go test -fuzz`) for
generated byte strings. The repository has no `Fuzz` target today, so the design
SHALL state which mechanism it uses and where the corpus lives.

| # | Property | Statement | Why it matters |
|---|---|---|---|
| P1 | **Idempotence** | For all inputs `x`, all tiers `t` and every field kind `k` **except *post title***: `sanitize(k, t, sanitize(k, t, x)) == sanitize(k, t, x)`. Titles are excluded by statement, not by exception handling, and are covered by P9 instead. | A non-idempotent sanitizer means output is not in the language it claims to produce — the classic symptom of a filter that can be walked past by pre-encoding. Quantifying over *all* field kinds while a bounded loop rescues one of them would make the property read as a coincidence rather than a design (Requirement 3.8). |
| P2 | **Allow-list closure** | For all `x`, parsing `sanitize(k, t, x)` yields no element and no attribute outside tier `t`'s allow-list, and no URL-bearing attribute whose scheme is outside the allowed scheme set. | This is the safety property itself, stated over all inputs instead of over the rejection cases of Requirement 9.2. It is asserted on the parsed tree, not by substring search. |
| P3 | **Tier monotonicity** | `allow(A) ⊆ allow(B) ⊆ allow(C)`, and for all `x`, every element surviving in `sanitize(k, A, x)` also survives in `sanitize(k, B, x)`. | Requirement 2.6. A containment property of the allow-lists themselves, not of writers: the tight list must never permit markup the broad list forbids. An inversion between the two lists is something an example table, organized per tier, structurally cannot see. |
| P4 | **`unfiltered_html` bypass is identity** | For all `x`: `sanitize(k, C, x) == x`, byte for byte, including for malformed and non-UTF-8 input. | Requirement 2.3. Catches a bypass implemented as "the laxest list" or as a parse-and-re-serialize round trip, both of which look correct on well-formed fixtures. |
| P5 | **Titles carry no markup delimiters** | For all `x`, `sanitize(title, t, x)` contains no `<` and no `>`, and introduces no HTML entity sequence. | Requirements 3.1–3.3. This is the property that makes keeping `PostView.Title` a `string` safe, so it is worth quantifying rather than sampling. |
| P6 | **Determinism and purity** | For all `x`, repeated and concurrent invocations yield identical output; output does not depend on wall-clock time, host timezone, locale or map iteration order. | Requirements 1.9, 2.7. Map-iteration-order dependence is the realistic failure here, and it is invisible in a single-threaded example test. |
| P7 | **Echo/stored agreement, conditional** | **Given** that the echoing GET carries the submitter's own principal — true of the redirect the handler itself issues, including the logged-in-editor tier-C case — then for all `x` the pending-comment echo value equals `sanitize(comment, t, x)` for the tier `t` that principal's capabilities select: `A` for any submitter lacking `unfiltered_html`, `C` for a holder. The property is **not** claimed when the precondition fails (an expired session, a forged link); those cases are pinned instead as unconditional safety assertions — the echo is sanitized at the requesting principal's tier, which is never laxer than that principal's own writing tier. | Requirement 6.6. Ties the reflected-XSS-sensitive site of Requirement 6.4 to the same function, field kind **and** tier as the stored path, so the two cannot drift. Stated conditionally because total agreement and the absence of reflected XSS are mutually exclusive given a query-string echo: the echo is a separate GET whose only non-forgeable tier input is its own principal. Neither half may be written as total. |
| P8 | **WordPress fixed point** | For each committed parity fixture `x`: `kses(sanitize(k, t, x)) == sanitize(k, t, x)`. | Requirement 2.11's round-trip claim. Stated over the fixture set rather than over all inputs, because the oracle is captured WordPress output, not a function this repository can call. |
| P9 | **Title convergence** | For all `x`, title normalization reaches a fixed point within the 16-pass bound of Requirement 3.8, and its result satisfies `f(f(x)) == f(x)`; inputs that do not converge within the bound fail closed and persist nothing. | The property that replaces P1 for *post title*, and a stronger one: it asserts both that the loop terminates inside its bound over generated input and that the bounded result is genuinely a fixed point, which is what kills the pre-encoding walk-past class for titles rather than merely surviving one pass of it. |

## Out of scope

- **10.B — REST media write parity.** `POST /wp-json/wp/v2/media` and the
  single-item update verbs stay `501`. Roadmap task 10.A must land first and
  this spec is that landing; enabling the route here would invert the ordering
  the roadmap calls non-optional.
- **10.C — REST user write parity.** `POST /wp-json/wp/v2/users` and
  `POST /wp-json/wp/v2/users/{id}` stay `501`, and roadmap Requirement 17.4's
  user profile fields (`description`) get no field kind yet — no write path
  reaches them today, since `domain.UserRepository` exposes `Create` and
  `UpdatePass` only. WHEN 10.C lands it SHALL add a field kind to this Policy
  rather than a second component.
- **10.D — `content.rendered` improvements.** Gutenberg delimiter stripping and
  `srcset`/`sizes`/`loading`/`decoding` attributes are untouched, including the
  comment `content.rendered` escape at `internal/content/rest.go:334`
  (Requirement 6.8).
- **10.E — M10 test coverage for newly enabled routes.** The capability-matrix
  tests for 10.B/10.C's routes belong with those routes. Requirement 9 covers
  this milestone's own surface only.
- **10.F — Navigation menu editing.** Menu item titles and URLs are
  user-supplied content crossing a write boundary, which is why the roadmap
  places 10.F after 10.A. No menu write path exists today, so there is nothing
  here to sanitize yet.
- **10.G — `$wp$` as grimoire's issued password format.** Unrelated to content
  safety, and tied to 10.C's password writes.
- **Enabling any new write route at all.** This milestone adds a policy in front
  of write paths that already exist. It adds no route, no verb and no field.
- **A render-layer backstop over *post content* or *post excerpt*.** Rejected by
  Requirement 4.6, for the reason stated there. The `template.HTML` casts at
  `view.go:37-38` keep their shape; only their comment changes. THE comment-only
  tier-A backstop of Requirements 6.9–6.11 is **in** scope and is the single
  exception; it does not extend to post fields.
- **Retroactive sanitization, provenance tracking and audit tooling.** All three
  rejected by Requirement 5. No backfill, no schema flag, no `grimoire-cli`
  scan.
- **Term names and descriptions.** M7's REST term CRUD writes them, and roadmap
  Requirement 15.1 does not list them. No template casts a term name or
  description to `template.HTML`, so both are auto-escaped everywhere they are
  emitted today and neither is a stored-XSS surface. Adding them later SHALL
  reuse this Policy with a new field kind.
- **Option values, media filenames and `unfiltered_upload`.** File-content and
  upload-path safety is M4's existing traversal-safe serving; this milestone
  filters HTML in content fields, nothing else.
- **M4's spam filter and moderation defaults.** Unchanged. Sanitization is not
  moderation: a sanitized comment is still held for moderation exactly as it is
  today, and the spam filter still sees the submission.
- **Shortcode and Gutenberg block semantics.** The Policy filters HTML. It does
  not interpret, validate or rewrite `[shortcode]` text or `<!-- wp:* -->`
  delimiters, and block comments in tier B/C content are unaffected.
- **Charset and encoding normalization** beyond whatever the chosen library
  performs as a consequence of parsing, which Requirement 2.12 requires the
  design to enumerate if it diverges from WordPress.
- **Cross-vendor storage contract tests.** The Policy sits above storage
  (Requirement 1.3); there is nothing vendor-dependent to assert in
  `internal/storage/storagetest`.
