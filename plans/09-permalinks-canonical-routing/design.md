# M9a — Permalinks & Canonical Routing: Design

## Overview

Today `internal/web/router.go` registers exactly one single-post route, last, as
a catch-all:

```go
r.Method(http.MethodGet, "/{slug}", s.handler(s.single))   // router.go:154
```

and `single` reads that one parameter and looks the post up by slug:

```go
slug := chi.URLParam(r, "slug")
post, err := s.posts.BySlug(ctx, slug)                     // handlers.go:52-54
```

Nothing reads `permalink_structure`. A site configured with WordPress's "Day and
name" preset therefore 404s every URL it publishes, while serving those posts at
a flat path WordPress itself `301`s away from.

This design adds a **new `internal/routing` package** that turns
`permalink_structure` into two pure operations — *parse a request path into
identifying components*, and *build a post's canonical path* — and wires it into
route registration and the `single` handler. No storage schema changes; two extra
option reads at startup.

## Architecture

```
                     startup
{prefix}options ──► OptionService.Get ──► routing.Parse(structure)
                                               │
                                               ├─► routing.Structure  (tokens, order, trailing slash, chi pattern)
                                               │
router.go ◄────────── Structure.ChiPatterns ───┘   register both slash forms alongside "/{slug}"
    │
    ▼
single handler ──► Structure.Match(path) ──► identifier (+ date parts)
                          │
                          ├─ no match ──────────────────► 404
                          ├─ resolves, path == canonical ► 200 render
                          └─ resolves, path != canonical ► 301 Location: canonical
```

### Why a new package

9.A left the resolver's home open between `internal/content` and a new
`internal/routing`. This design chooses **`internal/routing`**:

- Its inputs are a structure string and a path; its outputs are components and a
  path. It needs neither storage nor HTTP, so it is unit-testable with no
  fixtures and no database — which is what makes Requirement 7.1 cheap.
- `internal/content` holds *content services* that depend on repository ports.
  Putting a pure string/path transformer there would mean the package no longer
  has one job.
- The web layer already depends on `internal/content`; adding a sibling leaf
  package introduces no cycle.

### Why the structure is read at startup, not per request

`content.OptionService` performs no caching — `Get` calls straight through to
`OptionRepository.Get` on every invocation. Resolving the permalink structure
per request would therefore add a database round trip to *every* page load,
including ones that hit no other query.

Reading once at startup also means the chi route pattern can be **computed from
the structure and registered in the router**, keeping all path matching inside
chi's radix tree rather than hand-rolling precedence against the existing
`/category/{slug}`, `/wp-content/uploads/*` and `/{slug}` routes.

**The accepted consequence:** changing `permalink_structure` in WordPress
requires restarting grimoire. This mirrors WordPress's own model, where changing
permalinks requires flushing rewrite rules, and it is stated in
`requirements.md` as an explicit out-of-scope item rather than left as a
surprise.

**The alternative considered and rejected:** register `/*` and parse the whole
path inside the handler. That would pick up structure changes live, but it moves
route precedence out of chi into hand-written ordering logic, and it still needs
a cache to avoid the per-request option read — so it trades a restart for two
new failure modes.

## New / changed components

### `internal/routing` (new package)

```go
// Token is one supported permalink token.
type Token int

const (
    TokenPostname Token = iota
    TokenPostID
    TokenYear
    TokenMonth
    TokenDay
)

// Structure is a parsed, validated permalink_structure.
type Structure struct {
    // Raw is the option value as read, for logging and diagnostics.
    Raw string
    // Flat reports that no structure is configured (WordPress "plain"),
    // in which case the flat /{slug} route is canonical and no redirects
    // are issued.
    Flat bool
    // TrailingSlash mirrors whether Raw ends in "/".
    TrailingSlash bool
    // CategoryBase / TagBase are the resolved archive base segments,
    // defaulting to "category" / "tag".
    CategoryBase string
    TagBase      string
}

// Parse parses a permalink_structure plus the category/tag base options.
// An empty structure yields Flat. A structure containing an unsupported
// token, or no identifying token, yields ErrUnsupported wrapping a
// message naming the offending tokens -- the caller logs it and falls
// back to Flat (Requirement 4).
func Parse(structure, categoryBase, tagBase string) (Structure, error)

// ChiPatterns returns the chi route patterns for this structure: the form
// without a trailing slash and the form with one, in that order. Empty when
// Flat.
//
// Revised from `ChiPattern() string` during Phase 1. chi matches the two
// slash forms as DISTINCT routes, so registering only the canonical one
// makes chi 404 the other before any handler can redirect it -- which
// would defeat Requirement 3.3. Both must be registered and both must
// reach the handler, which then redirects whichever is non-canonical.
func (s Structure) ChiPatterns() []string

// ParamsFromPath splits a path into the chi-style parameter map this
// structure would produce, without needing a router. Added in Phase 1 so
// the canonical fixed-point property (Requirement 3.5) can be asserted in
// a pure unit test rather than only through a live router.
func (s Structure) ParamsFromPath(path string) (map[string]string, bool)

// SupportedTokens lists the tokens this package understands, for the
// startup warning and migrate -check's report (Requirement 4.2, 4.5).
func SupportedTokens() []string

// Match extracts the identifying component and any date components from a
// request path already matched by ChiPattern. Returns ok=false when a
// component fails its shape rule (Requirement 2.2's digit widths).
func (s Structure) Match(params map[string]string) (Ref, bool)

// Ref identifies a post by whichever token the structure carries.
type Ref struct {
    Slug   string // set when the structure has %postname%
    ID     int64  // set when the structure has %post_id%
    Year   int    // 0 when absent
    Month  int
    Day    int
}

// Canonical builds the canonical path for a post. It is the single source
// of truth for both the redirect target and the REST link field, so the
// two cannot disagree (Requirement 6.1).
func (s Structure) Canonical(p domain.Post) string
```

`Canonical` is deliberately the only place a permalink is constructed.
Requirement 3.5's no-loop property follows from `Canonical` being a pure function
of the post: a request whose path equals `Canonical(post)` renders, anything else
redirects to it, and `Canonical(Canonical(post))` is trivially the same string.

### `internal/web`

- **`router.go`** — when `Structure` is not `Flat`, register **both** patterns
  from `Structure.ChiPatterns()` mapped to the `single` handler, *before* the existing
  `/{slug}` route. `/{slug}` stays registered, and with a non-flat structure its
  handler becomes the canonical-redirect path (Requirement 3.1) rather than a
  renderer. Registration order relative to `/category/{slug}`, `/`, `/login` and
  `/wp-content/uploads/*` is unchanged; the new pattern has a fixed segment count
  and cannot shadow them.
- **`handlers.go`** — `single` gains structure awareness:
  1. If `Flat`, behave exactly as today.
  2. Otherwise build a `Ref` from the chi params. Resolve by slug (existing
     `posts.BySlug`) or by id.
  3. If date tokens are present, compare them to `post.Date`; mismatch → 404
     (Requirement 2.5).
  4. Compare the request path to `Canonical(post)`; differ → 301 preserving the
     query string (Requirement 3.2, 3.4).
  5. Otherwise render, unchanged.
- **Resolving by `%post_id%`** needs a published-post-by-id read.
  `PostRepository` has only `RecentPosts`, `BySlug` and `ByTermSlug` — no id
  lookup. An id lookup *does* exist on the write-side port
  (`repository.go:245`), but its contract is explicitly "the stored post/page by
  primary key **regardless of status**". Reusing it on the public path would
  serve drafts, private and trashed posts to anonymous visitors at a guessable
  URL. This design therefore adds
  `PostRepository.PublishedByID(ctx, id int64, types ...string) (Post, error)`
  mirroring `BySlug`'s published-only, type-defaulting semantics.

  Named `PublishedByID` rather than `ByID` (revised during Phase 2):
  `*wprepo.PostRepo` is the single concrete type satisfying both this port and
  `PostWriter`, so a second `ByID` collides with the existing one outright. The
  rename is the better outcome anyway — two same-named lookups with opposite
  disclosure properties would be a trap. This is a correctness and
  disclosure boundary, not a layering preference.

### `internal/render`

Extend the existing map (`engine.go:26`), no mechanism change:

```go
"tag":    {"tag", "archive", "index"},
"author": {"author", "archive", "index"},
"date":   {"date", "archive", "index"},
```

`Render` already falls back to `{"index"}` for an unknown kind and returns an
error only when no candidate is loaded, so Requirement 5.5 holds with no code
change.

### `internal/content/rest.go`

`postLink(slug string) string` becomes structure-aware. Rather than threading a
`Structure` through every REST call site, the service that owns REST mapping
takes the `Structure` once at construction and `postLink` becomes a method or
closure over it. `commentLink`/`userLink` are untouched (Requirement 6.4).

### `cmd/grimoire/main.go`

After config load and before `web.NewServer`: read the three options, call
`routing.Parse`, log the outcome at `INFO` when supported and `WARN` when
falling back (Requirement 4.2/4.4), and pass the `Structure` to the server and
REST mapper.

### `grimoire-cli migrate -check`

Extend the existing preflight report with a line naming the resolved structure
and whether it is supported (Requirement 4.5). Read-only; no behavior change to
the migration paths.

## Status codes

| Condition | Status |
|---|---|
| Path matches structure, resolves, is canonical | `200` |
| Path matches structure, resolves, is not canonical | `301` + `Location` |
| Flat `/{slug}` request while a structure is configured | `301` + `Location` |
| Path matches structure, date components contradict the post | `404` |
| Path matches structure, no such published post | `404` |
| Path matches no route | `404` (unchanged) |
| Any request while structure is unsupported/unparseable | as today (flat) |

`301` rather than `302` throughout, matching WordPress's `redirect_canonical`
and signalling permanence to search engines.

## Migrations

**None.** This milestone reads two additional rows from an existing table. The
task list verifies no file appears under any `migrations` directory, mirroring
M8's zero-schema-change check.

## Security considerations

- **Open redirect.** Redirect targets are built exclusively by
  `Structure.Canonical` from a database-resolved post, never from user input, so
  a request cannot steer the `Location` header. The existing `safeRedirect`
  helper used by the login flow is not applicable and is not reused, to avoid
  implying user-controlled input is involved.
- **Path traversal.** Components come from chi's parsed parameters, which cannot
  contain `/`. Date components are additionally constrained to fixed-width
  digits. `%postname%` reaches only a parameterised `BySlug` query.
- **Enumeration via `%post_id%`.** A structure containing `%post_id%` lets a
  visitor iterate ids, but `BySlug`/`ByID` return published rows only, so this
  exposes nothing the site does not already publish. Unchanged from WordPress.
- **Redirect loops** are addressed by the fixed-point property above and covered
  by an explicit test, because a loop here would be a self-inflicted denial of
  service on every post URL.

## SEO considerations

The whole point of this milestone is that an existing site's URLs keep working,
so the SEO posture is the requirement rather than a side effect:

- Canonical form is single-valued: exactly one path per post returns `200`, every
  other recognised form `301`s to it. Trailing-slash handling (Requirement 3.3)
  exists specifically to prevent the duplicate-content case where `/a/b/` and
  `/a/b` both render.
- `301` preserves accumulated link equity, where a `302` would not.
- REST `link` values agreeing with the served canonical path (Requirement 6)
  matters because consumers syndicate those URLs.

## Testing strategy

1. **Pure unit tests, `internal/routing`** — the bulk of the coverage, no
   database. Table-driven across the three WordPress presets, each token
   individually, digit-width rules, both trailing-slash forms, unsupported
   tokens, and the no-identifying-token case.
2. **Handler tests, `internal/web`** — exact status and `Location` assertions for
   each row of the status-code table, using the existing handler-test fakes.
   Includes the fixed-point no-loop test and the date-mismatch 404.
3. **Cross-vendor contract test** — `PostRepository.PublishedByID` added to the existing
   `storagetest` contract so all three vendors are covered, matching how
   `BySlug` is already tested.
4. **e2e** — one test in `test/e2e` booting the stack with a dated structure and
   asserting a published post's canonical URL renders while its flat URL
   redirects. Uses the `testDSN` helper so it inherits the SQLite `busy_timeout`
   fix from #34.
5. **Real-WordPress fixture, env-gated** — mirrors
   `../02.1-wp-hash-real-db`'s approach: skipped unless a DSN env var is set, so
   CI stays hermetic. Verifies against real data shapes, specifically a dated
   structure and a non-default table prefix, since the podman stack in
   `accuweaverllc/scripts` provides exactly that (`accuweaver` prefix,
   `/%year%/%monthnum%/%day%/%postname%/`).

## Traceability

| Requirement | Components |
|---|---|
| 1 — read option set | `cmd/grimoire/main.go`, `content.OptionService`, `routing.Parse` |
| 2 — token resolution | `routing.Structure.Match`, `routing.Ref`, `web.single`, `PostRepository.PublishedByID` |
| 3 — canonical redirects | `routing.Structure.Canonical`, `web.single`, `router.go` |
| 4 — loud fallback | `routing.Parse` + `ErrUnsupported`, `cmd/grimoire/main.go`, `grimoire-cli migrate -check` |
| 5 — template hierarchy | `internal/render/engine.go` `hierarchy` map |
| 6 — REST link | `internal/content/rest.go` `postLink` |
| 7 — test coverage | `internal/routing` unit tests, `internal/web` handler tests, `storagetest`, `test/e2e` |
