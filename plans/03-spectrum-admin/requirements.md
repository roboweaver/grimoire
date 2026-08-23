# Requirements — M3: Adobe React Spectrum Admin UI

## Introduction

This milestone gives grimoire a first-class **admin interface**. Building on M1's
read-only content core and M2's authentication, sessions, roles, and capabilities,
M3 adds an **Adobe React Spectrum** single-page application (SPA) that is **served
by the Go binary itself** — its production build is embedded with `go:embed`, so
there is **no Node.js runtime at serve time** and the single-binary story holds.
The SPA authenticates with the **same M2 session cookie**, talks to new **Go
JSON APIs** under `/admin/api`, and is gated by WordPress **capabilities**.

The defining success criterion: point the *same* grimoire binary at an **existing,
populated WordPress database**, sign in as a real WordPress user, and use a
Spectrum-styled admin to see a **dashboard of content counts**, browse a
**paginated list of posts and pages (including drafts)**, and open a **single
post/page detail** — identically on MySQL, PostgreSQL, or SQLite by changing only
configuration, and **without applying any schema migration** to that live database.

M3 is deliberately **read-only**. It reads through additive, pure-`SELECT` ports so
it can overlay a production WordPress database with zero risk. Content **creation,
update, deletion**, media, and the rich editor are **deferred to milestone 06 (CRUD)**, which
will build on M2's existing Go-internal write services and the CSRF-header pattern
this spec designs but does not yet exercise.

Out of scope for M3: any write/mutation HTTP endpoint (create/update/delete —
milestone 06); media library and uploads (milestone 06+); comments; plugins; the full WordPress REST
API surface; server-side rendering of the admin; and a Node.js runtime dependency
at serve time.

## Requirements

### Requirement 1 — Embedded Spectrum SPA served by the Go binary

**User Story:** As an operator, I want the admin UI to ship inside the single
grimoire binary, so that deploying the admin requires no separate web server,
Node.js runtime, or asset host.

#### Acceptance Criteria
1. THE system SHALL embed the admin SPA's production build (`dist`) into the Go binary using `go:embed`, mirroring the existing embedded-assets pattern, so that no filesystem assets or Node.js runtime are required to serve the admin.
2. THE system SHALL serve the admin SPA under the `/admin` URL prefix, registered ahead of the public catch-all `/{slug}` route so admin paths are never shadowed by content resolution.
3. WHEN a request targets a hashed static asset under `/admin` (e.g. `/admin/assets/app-<hash>.js`) that exists in the embedded build, THEN the system SHALL serve that file with a long-lived immutable `Cache-Control` header.
4. WHEN a request targets an `/admin` path that is NOT an existing embedded asset (a client-side route), THEN the system SHALL serve the SPA entry document (`index.html`) with a non-cacheable `Cache-Control` header (SPA fallback routing).
5. WHEN a request targets a missing hashed asset under `/admin/assets/`, THEN the system SHALL return `404 Not Found` rather than falling back to `index.html`, so broken asset references surface clearly.
6. THE embedded build SHALL always compile: a committed placeholder entry document SHALL exist so `go build ./...` succeeds before the frontend has ever been built.

### Requirement 2 — Authenticated, capability-gated admin access

**User Story:** As a site owner, I want the admin restricted to authorized staff,
so that only users whose WordPress role permits it can reach the back office.

#### Acceptance Criteria
1. THE system SHALL protect every `/admin` and `/admin/api` route with the M2 session middleware and SHALL reuse the existing session cookie — no new auth mechanism is introduced.
2. WHEN an unauthenticated `GET`/`HEAD` request targets an `/admin` page, THEN the system SHALL redirect (`303 See Other`) to `/login?redirect=<original-path>`, reusing the existing open-redirect-safe redirect handling.
3. WHEN an unauthenticated request targets an `/admin/api` endpoint, THEN the system SHALL respond `401 Unauthorized` with a JSON error body and SHALL NOT redirect.
4. WHEN an authenticated user lacks the required capability for an admin resource, THEN the system SHALL respond `403 Forbidden` (JSON for API routes) AND SHALL NOT reveal whether the resource exists.
5. THE system SHALL gate the admin shell and its content APIs on the `edit_posts` capability, so that back-office roles (contributor and above) are admitted and subscribers are not.
6. THE session/current-user endpoint SHALL require only a valid session (not `edit_posts`), so the SPA can distinguish "not logged in" from "logged in but not permitted" and render an appropriate message.

### Requirement 3 — Current session / user JSON endpoint

**User Story:** As the admin SPA, I want to fetch the current user and session
context on load, so that I can render the authenticated shell, gate views by
capability, and hold the CSRF token for future writes.

#### Acceptance Criteria
1. THE system SHALL expose `GET /admin/api/session` returning JSON with the authenticated user's `id`, `login`, `displayName`, `roles`, and effective `capabilities`.
2. THE session response SHALL include the per-session synchronizer `csrfToken` so the SPA can attach it to unsafe requests in milestone 06.
3. WHEN the caller is authenticated but lacks `edit_posts`, THEN the response SHALL still succeed (200) and convey the capability set, so the SPA can show an "insufficient permissions" state rather than a hard error.
4. THE session response SHALL NOT include the password hash, session token, cookie value, or any secret beyond the CSRF synchronizer token.

### Requirement 4 — Dashboard content counts JSON endpoint

**User Story:** As an editor, I want an at-a-glance dashboard, so that I can see how
much content exists without paging through lists.

#### Acceptance Criteria
1. THE system SHALL expose `GET /admin/api/stats` returning JSON counts for at least: published posts, draft posts, pages, categories, and users.
2. THE counts SHALL be computed via additive, read-only `COUNT` queries that introduce **no** schema change and work unchanged against an existing WordPress database.
3. THE counts SHALL be identical (for identical data) across MySQL, PostgreSQL, and SQLite, with no dialect-specific behavior leaking into shared code.
4. THE endpoint SHALL require the `edit_posts` capability.

### Requirement 5 — Paginated posts/pages listing JSON endpoint

**User Story:** As an editor, I want a paginated, filterable list of content
including drafts, so that I can review everything that has been written.

#### Acceptance Criteria
1. THE system SHALL expose `GET /admin/api/posts` returning a JSON page of content items with fields sufficient for a list view: `id`, `title`, `slug`, `type`, `status`, `author`, and `date`.
2. THE endpoint SHALL accept `page`, `perPage`, `type`, and `status` query parameters AND SHALL clamp `perPage` to the existing pagination bounds (default 10, maximum 100).
3. THE listing SHALL include non-published statuses (e.g. `draft`, `pending`, `private`) and both `post` and `page` types, unlike the public read path which is limited to published posts.
4. THE response SHALL include pagination metadata (`page`, `perPage`, `total`, `totalPages`) so the SPA can render page controls, where `total` is obtained via an additive read-only `COUNT`.
5. WHEN `page` exceeds the available range, THEN the endpoint SHALL return an empty `items` array with correct metadata rather than an error.
6. THE endpoint SHALL require the `edit_posts` capability.

### Requirement 6 — Single post/page detail JSON endpoint

**User Story:** As an editor, I want to open one item and see its full content, so
that I can review a specific post or page.

#### Acceptance Criteria
1. THE system SHALL expose `GET /admin/api/posts/{id}` returning the full item as JSON: `id`, `title`, `slug`, `type`, `status`, `author`, `date`, `excerpt`, and `content`.
2. WHEN no item matches the given id, THEN the endpoint SHALL return `404 Not Found` with a JSON error body.
3. THE endpoint SHALL return items of any status and type (drafts and pages included), reusing the existing by-id read port.
4. THE endpoint SHALL require the `edit_posts` capability.

### Requirement 7 — CSRF and same-origin security posture

**User Story:** As a security reviewer, I want the admin's API to follow the same
CSRF and same-origin discipline as the existing login flow, so that adding write
endpoints later does not require rethinking the model.

#### Acceptance Criteria
1. THE admin SPA SHALL be served from the same origin as its APIs so the existing `HttpOnly`, `SameSite=Lax` session cookie authenticates API calls without a token exchange.
2. THE M3 API SHALL expose only **safe** (`GET`) methods; because no state-changing method is served, CSRF tokens are not required to be validated in M3.
3. THE design SHALL define the unsafe-request CSRF contract for milestone 06: the SPA reads `csrfToken` from `GET /admin/api/session` and echoes it on unsafe requests via an `X-CSRF-Token` header, validated against the per-session synchronizer token in constant time.
4. THE system SHALL NOT weaken any M2 cookie attribute (`HttpOnly`, `SameSite`, `Secure`-when-TLS) to accommodate the SPA.

### Requirement 8 — React Spectrum admin experience

**User Story:** As an Adobe employee, I want the admin to look and behave like an
Adobe product, so that it is consistent, accessible, and familiar.

#### Acceptance Criteria
1. THE admin UI SHALL be built with `@adobe/react-spectrum` components, `@spectrum-icons/workflow` icons, and Spectrum design tokens for color, spacing, and typography — no hardcoded hex colors, ad-hoc component libraries, or off-scale spacing for chrome.
2. THE SPA SHALL provide at least three views: a **dashboard** (counts from Req 4), a **posts/pages list** (Req 5, paginated), and a **post detail** (Req 6).
3. THE SPA SHALL render explicit loading, empty, and error states for each data-driven view rather than blank screens.
4. WHEN a data request returns `401`, THEN the SPA SHALL send the user to `/login?redirect=<current-admin-path>`; WHEN it returns `403`, THEN the SPA SHALL render an "insufficient permissions" state.
5. THE SPA SHALL wrap the app in the Spectrum `Provider` with an appropriate theme and SHALL be keyboard-navigable and screen-reader-labeled per Spectrum defaults.
6. THE SPA SHALL NOT bundle or hardcode the Adobe Clean font or Adobe brand logos; it SHALL consume Spectrum's font stack via tokens/components and source any brand asset from Brand Center if needed.

### Requirement 9 — Build and embed pipeline without runtime Node.js

**User Story:** As a contributor, I want a reproducible way to build and embed the
admin assets, so that the Go binary always ships a current UI without requiring
Node.js to run it.

#### Acceptance Criteria
1. THE React source SHALL live under `web/admin/` and SHALL NOT be part of the Go module's build/dependency graph.
2. THE system SHALL provide a `make admin` target that builds the SPA (via the frontend toolchain) into the embedded `dist` directory consumed by `go:embed`.
3. THE built `dist` SHALL be committed to the repository so `go build`, `go test`, and `go install ./...` succeed with **no** Node.js present.
4. CI SHALL verify embedded-asset freshness by running `make admin` and failing if the committed `dist` differs from a fresh build.
5. THE runtime dependency graph SHALL remain pure Go: no JavaScript engine or Node process is spawned to serve the admin.

### Requirement 10 — DB-vendor-agnostic, existing-WP-DB-overlay compatibility

**User Story:** As a developer, I want M3 to preserve grimoire's core constraints,
so that the admin works on any supported vendor and against a live WordPress DB.

#### Acceptance Criteria
1. THE M3 admin and its APIs SHALL introduce **no** new migration and **no** schema change; all new data access SHALL be additive, read-only `SELECT`/`COUNT` ports.
2. WHEN grimoire overlays an existing, populated WordPress database (only the additive M2 `{prefix}sessions` table added), THEN the admin read APIs SHALL function without altering that database.
3. THE admin APIs SHALL return identical shapes and behavior across MySQL, PostgreSQL, and SQLite, verified by vendor-agnostic tests; any real-DB validation SHALL be environment-gated like M2.1.
4. THE new read/count ports SHALL be defined in the domain layer and implemented per-vendor in the storage layer, with no database driver imported by the domain or web layers.

### Requirement 11 — API error handling, observability, and no leakage

**User Story:** As an operator, I want admin API failures to be safe and legible,
so that clients get consistent errors and logs aid debugging without leaking
secrets.

#### Acceptance Criteria
1. THE admin APIs SHALL return errors as a consistent JSON shape (e.g. `{ "error": { "code": "...", "message": "..." } }`) with appropriate status codes (`400`, `401`, `403`, `404`, `500`).
2. THE APIs SHALL NEVER include SQL text, stack traces, driver errors, password hashes, or session/CSRF secrets in a client response.
3. THE APIs SHALL log server-side failures via the existing structured logger (`slog`) with enough context to debug, without logging secrets or full request bodies.
4. THE APIs SHALL set `Content-Type: application/json` on all JSON responses and SHALL reject unsupported methods with `405 Method Not Allowed`.

## Implementation deviations

The M3 implementation follows this specification with the following minor,
spec-consistent clarifications recorded during development:

1. **Post content rendered as escaped, preformatted text.** Per the design's
   Security considerations, the read-only detail view displays post content as
   text (whitespace-preserving `pre-wrap`), not as rendered HTML. No
   `dangerouslySetInnerHTML` is used; a sandboxed/rendered HTML preview is
   deferred to the milestone 06 editor.
2. **SPA client router.** The SPA uses `react-router` (basename `/admin`)
   bridged into the React Spectrum `Provider` `router` prop so Spectrum
   pressables/links perform client-side navigation. This satisfies the
   "Spectrum components only" intent while keeping SPA-fallback routing.
3. **Empty-state icon.** The shared empty state uses
   `@spectrum-icons/workflow/Document` (the illustrations package
   `NoSearchResults` is not a project dependency).
4. **CI freshness as a dedicated job.** Asset-freshness (Req 9.4) runs in a
   separate `admin-freshness` CI job with Node; the Go `build-test` job remains
   Node-free (Req 9.5).
