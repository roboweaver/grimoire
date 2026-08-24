# Tasks — M5: Extensions & REST API Parity

Implementation checklist for M5, following strict TDD. Tasks are ordered so
each builds on the last and references the requirements it satisfies. Keep
`gofmt -l .` empty, `go vet ./...`, `go build ./...`, and `go test ./...`
green after every phase (SQLite unconditional; MySQL/Postgres contract runs
gated on `GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN`; any
real-WP-DB check gated like M2.1). M5 adds **no migration file** — see
`design.md`'s Migrations section before assuming one is needed. Commit in
logical increments with the trailer
`Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.

## Phase 0 — Spec
- [x] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and update the `plans/README.md` milestone index (row 05 → Specified; note that most post/page/media REST writes are deferred to M6).
  - _Acceptance:_ Three Kiro spec files exist matching M1–M4 style with Mermaid diagrams; README row 05 links to this directory. _(All requirements)_
- [ ] 0.2 Report spec-written to the creator session and obtain user/reviewer spec approval before coding.
  - _Acceptance:_ Creator notified; GPT-5.5 + Kimi K3 review cycle completes and any open questions are resolved.

## Phase 1 — Extension hook registry (`internal/extensions`)
- [ ] 1.1 Write failing tests: actions run in registration order; a panicking action is recovered/logged and does not stop later actions; filters chain in registration order (each filter's output feeds the next); a filter error short-circuits the chain and the pre-error value is never observed downstream; `DoAction`/`ApplyFilters` against a fixed registered set are race-free (`go test -race`); firing an unregistered hook is a no-op (actions) or returns the input value unchanged with a nil error (filters).
- [ ] 1.2 Implement `registry.go`: `ActionFunc`, `FilterFunc[T any]`, `RegisterAction`, `RegisterFilter[T any]`, `DoAction`, `ApplyFilters[T any]`, internal per-hook-name storage with a mutex (register happens at `init()` time; fire happens per-request — both must be goroutine-safe).
- [ ] 1.3 Write `doc.go`: package-level doc comment stating explicitly that this is a **native Go compiled-extension mechanism**, and that it does **not** run PHP or any real wordpress.org plugin, matching the non-goals language in `requirements.md` Requirement 12.
  - _Acceptance:_ `internal/extensions` has zero imports from `domain`/`content`/`web`/`storage`; all registry tests pass including `-race`. _(Req 10.1–10.6, 12.1–12.4)_

## Phase 2 — Domain port + storage adapter for post→term IDs (`internal/domain`, `internal/storage`)
- [ ] 2.1 Write failing tests for `PostTermsRepository` semantics (empty result for a post with no terms of the given taxonomy; taxonomy filter isolates `category` from `post_tag`; nonexistent post ID returns an empty slice, not an error).
- [ ] 2.2 Add `PostTermsRepository` interface (`TermsForPost(ctx, postID int64, taxonomy string) ([]int64, error)`) to `domain/repository.go` — no driver imports.
- [ ] 2.3 Extend `storagetest` fixtures with a post related to two `category` terms and one `post_tag` term via `{prefix}term_relationships`/`{prefix}term_taxonomy`; write `runPostTermsContract` and call it from `RunContract`, parameterized across vendors.
- [ ] 2.4 Implement `wprepo/postterms.go` (`PostTermsRepo`, Bun raw JOIN over `term_relationships`⋈`term_taxonomy`, `rebind.Rebind(vendorOf(db), q)`, compile-time `var _ domain.PostTermsRepository = (*PostTermsRepo)(nil)`); wire into `storage.Set`/`storage.New`.
  - _Acceptance:_ Contract suite passes on SQLite by default and MySQL/Postgres when DSNs are set; identical results across vendors; introduces zero schema change. _(Req 14.1–14.4)_

## Phase 3 — Application Passwords (`internal/auth`)
- [ ] 3.1 Write failing tests: creating an Application Password appends to (and does not clobber) an existing `_application_passwords` usermeta array; the plaintext secret is returned exactly once at creation and is never recoverable from the stored value; a **fixture** value shaped like a real WordPress-authored `_application_passwords` entry (phpass/`$wp$` hash) verifies via the same `Verify` call with no format-specific branching by the caller; a wrong secret fails to verify; a successful verify updates `last_used`/`last_ip` for that UUID only; revoking a UUID removes it from the stored array and a subsequent verify with its old secret fails; verifying against a user with no `_application_passwords` meta at all fails cleanly (no panic, no error wrongly implying "user not found").
- [ ] 3.2 Implement `auth/apppassword.go`: `ApplicationPassword` type, `internal/php.Serialize`/`Unserialize` codec for the `_application_passwords` usermeta value (matching WordPress's real array-of-associative-arrays shape: `uuid`, `app_id`, `name`, `password` (hash), `created`, `last_used`, `last_ip`), `Create` (bcrypt-hash a new random secret via `internal/auth/password.Hash`, append, persist via `UserMetaRepository.Set`), `Verify(ctx, login, plaintextSecret) (Principal, error)` (loads the user's `_application_passwords`, tries each entry's hash via `internal/auth/password.Verify`, updates `last_used`/`last_ip` on match), `List(ctx, userID)`, `Revoke(ctx, userID, uuid)`.
  - _Acceptance:_ All apppassword tests pass, including the real-WordPress-hash-shaped fixture; no plaintext secret is ever logged or stored. _(Req 8.1–8.7)_

## Phase 4 — REST view-model mapping (`internal/content`)
- [ ] 4.1 Write failing tests: post/page view-model field names and formats match WordPress (`id`, `date`, `date_gmt`, `slug`, `status`, `title.rendered`, `content.rendered`, `content.protected`, `excerpt.rendered`, `author`, `featured_media`, `comment_status`, `ping_status`, `categories`, `tags`, `link`); comment view-model fields (`id`, `post`, `parent`, `author_name`, `author_url`, `date`, `date_gmt`, `content.rendered`, `link`, `status` mapped from the raw `0/1/spam/trash` enum to WP's `hold`/`approved`/`spam`/`trash` string); media view-model fields (`id`, `date`, `slug`, `source_url`, `mime_type`, `media_details`, `post` (attached-to)); users view-model fields differ by context (`view`: `id`,`name`,`slug`,`avatar_urls`,`link`; `edit`, capability-gated: adds `email`,`roles`,`capabilities`,`registered_date`); `categories`/`tags` populated via the new `PostTermsRepository`; unpublished/draft posts excluded from anonymous `view`-context reads but included for a principal with `edit_posts`.
- [ ] 4.2 Implement `content/rest.go`: mapping functions from existing domain types (`domain.Post`, `domain.Comment`, `domain.Media`, `domain.User`) + `PostTermsRepository` into the WP-shaped view-model structs above, parameterized by REST `context` (`view`/`edit`) where WordPress itself varies fields by context.
  - _Acceptance:_ View-model tests pass; mapping calls only existing service/port methods (no new business logic, no direct DB access). _(Req 2.1–2.6, 3.1–3.4, 4.1–4.4, 5.1–5.5, 6.1–6.6, 14.1–14.2)_

## Phase 5 — REST HTTP surface (`internal/web`)
- [ ] 5.1 Write failing `httptest` tests: `GET /wp-json/` and `GET /wp-json/wp/v2/` return an index body; collection endpoints return `X-WP-Total`/`X-WP-TotalPages` headers and a JSON array; `_links.self`/`_links.collection` present on every resource; `?_embed` on a post/comment/media response produces `_embedded` with the resolved author/parent/featured-media sub-resources; pagination params (`page`, `per_page`, capped at a sane max) affect `LIMIT`/`OFFSET`; unknown route → `404 rest_no_route`; unsupported method on a known route → `405`; missing/unpublished resource → `404` with the WP error shape `{code,message,data:{status}}` (never the admin `{error:{...}}` shape); deferred write methods (`POST`/`PUT`/`PATCH`/`DELETE` on posts/pages/media) → `501 rest_not_implemented` with a body noting deferral to M6.
- [ ] 5.2 Implement `rest_envelope.go`: WP-shaped error writer, `_links`/`_embedded` builders, pagination header helper, response writer that always sets `Content-Type: application/json`.
- [ ] 5.3 Implement `rest_posts.go` (`GET /wp-json/wp/v2/posts`, `GET /wp-json/wp/v2/posts/{id}`, `GET /wp-json/wp/v2/pages`, `GET /wp-json/wp/v2/pages/{id}`, plus `501` handlers for their write methods).
- [ ] 5.4 Implement `rest_media.go` (`GET /wp-json/wp/v2/media`, `GET /wp-json/wp/v2/media/{id}`, plus `501` handlers for its write methods).
- [ ] 5.5 Implement `rest_users.go` read endpoints (`GET /wp-json/wp/v2/users`, `GET /wp-json/wp/v2/users/{id}`, view/edit context gating).
- [ ] 5.6 Implement `rest_router.go`: mounts `/wp-json/*` ahead of the public catch-all in `router.go`, resolves auth (session cookie **or** Application Password, see Phase 6), fires `rest.pre_dispatch` before dispatch and `rest.response` on the assembled value before marshal+write.
  - _Acceptance:_ All Phase 5 handler tests pass; routing precedence matches `design.md`'s diagram (no shadowing of `/admin`, `/admin/api`, `/wp-content/uploads/`); results identical across vendors. _(Req 1.1–1.5, 2.1–2.8, 4.1–4.6, 5.1–5.6, 6.1–6.6, 7.5, 13.1–13.5)_

## Phase 6 — Application Password auth + REST comment write (`internal/web`)
- [ ] 6.1 Write failing tests: `Authorization: Basic` with a valid login:secret pair resolves a `Principal` and skips the CSRF check; an invalid pair → `401 rest_invalid_credentials`; no `Authorization` header falls through to session-cookie resolution; a session-cookie-authenticated write without/with-mismatched `X-CSRF-Token` → `403 rest_forbidden`; an anonymous (no session, no Basic auth) comment POST is accepted and subject to the same M4 spam/moderation defaults; comment POST to a closed/missing post → `403`/`404`; successful creation returns `201` with the WP comment shape and fires `comment.submitted` exactly once.
- [ ] 6.2 Implement `apppasswordauth.go` (`ApplicationPasswordAuth` middleware: parses HTTP Basic, calls `auth.Verify`, sets a request-scoped `Principal` on success, falls through to session middleware when no/invalid Basic header is present — never itself denies a request that carries no credentials).
- [ ] 6.3 Implement `rest_comments.go`: `GET` collection/single (approved-only for anonymous, all statuses for a principal with `moderate_comments`) and `POST` create, delegating to the **existing, unmodified** `content.CommentService.Create`.
- [ ] 6.4 Add the `comment.submitted` `DoAction` call inside `content/comments.go`'s `CommentService.Create`, firing after successful persistence regardless of resulting status (including `spam`).
  - _Acceptance:_ All Phase 6 tests pass; REST comment creation reuses M4's abuse posture unchanged (no second, weaker comment-creation code path); Application-Password-authenticated writes are never CSRF-checked; session-cookie-authenticated writes always are. _(Req 3.1–3.4, 7.1–7.6, 8.1–8.3, 11.3)_

## Phase 7 — Application Password self-service endpoints (`internal/web`)
- [ ] 7.1 Write failing tests: `GET /wp-json/wp/v2/users/me/application-passwords` lists the caller's own entries with **no** hash/secret field; `POST` creates one and returns the plaintext secret exactly once in that response only; `DELETE /{uuid}` revokes and a subsequent auth attempt with the old secret fails; all three require a session-cookie principal (an Application-Password-authenticated request to these three endpoints is rejected, to prevent a credential minting/revoking its own replacements); `POST`/`DELETE` enforce `X-CSRF-Token`; acting on another user's UUID → `404`.
- [ ] 7.2 Implement the three handlers in `rest_users.go` (or a dedicated `rest_apppasswords.go`), backed by `auth.List`/`Create`/`Revoke` from Phase 3.
  - _Acceptance:_ All Phase 7 tests pass; no secret ever appears in a list/read response; cross-user management is rejected. _(Req 9.1–9.6)_

## Phase 8 — Post-render extension point (`internal/web`, theme)
- [ ] 8.1 Write failing tests: a test filter registered on `render.post_html` observably changes the rendered HTML of a public single/page request; with **no** filter registered, the response is byte-for-byte identical to pre-M5 behavior (regression guard); the admin SPA response and REST JSON responses are **not** passed through this filter.
- [ ] 8.2 Wire `extensions.ApplyFilters(ctx, "render.post_html", buf.Bytes())` into `web/handlers.go`'s `renderHTML` buffer-then-write path (single/page handlers only), writing the filter's error as a `500` if one occurs.
  - _Acceptance:_ All Phase 8 tests pass. _(Req 11.1, 11.4–11.5)_

## Phase 9 — Config + wiring (`internal/config`, `cmd/grimoire`)
- [ ] 9.1 Add any small REST-specific config knobs needed (e.g. a `per_page` max cap) with sane WordPress-matching defaults (WordPress's own default/max is 10/100); document in the example config. If no new config is actually needed once Phase 5 is implemented, state that explicitly here instead of adding an empty struct.
- [ ] 9.2 Thread `PostTermsRepository`, the Application Password auth/service, and the REST router through `cmd/grimoire/main.go`; add a documented, no-op-by-default blank-import extension point comment (e.g. `// import _ "your/extension/package" to compile in a grimoire extension`) demonstrating how a real extension package would be linked in.
  - _Acceptance:_ Server boots with defaults; `/wp-json/wp/v2/` is reachable out of the box with zero extensions registered; no driver imports leak above storage. _(Req 1.1, 10.1, 12.4, 14.4)_

## Phase 10 — Full-suite validation, docs, and PR
- [ ] 10.1 Run the full test matrix: `gofmt -l .` empty, `go vet ./...`, `go build ./...`, `go test ./...` on SQLite; MySQL/Postgres contract runs when their DSNs are set; the extension-point regression tests (Phase 8.1's "no filter registered" case, and equivalents for `rest.response`/`comment.submitted`) all pass with zero extensions registered, confirming M5 changes nothing when unused.
- [ ] 10.2 Overlay-safety spot check: exercise REST reads, REST comment creation, and Application Password verification against a database seeded to resemble an existing WordPress export (no M5-specific schema present beyond what M1–M4 already require); optionally env-gated against a real WP DB per M2.1's pattern.
- [ ] 10.3 Update top-level docs (e.g. `README.md`/`docs/`, if such exist and document API surface) to mention the new `/wp-json` REST endpoints and the extension mechanism at a high level; do not duplicate the full spec — link to `plans/05-extensions-rest-api/`.
- [ ] 10.4 Update `plans/README.md` row 05 to `✅` (or the milestone's completion marker per that file's convention) once implementation and review both land — **not** part of this spec-only PR; leave row 05 at the spec-authored marker (e.g. `📝 Specified`) for the spec PR itself.
- [ ] 10.5 Open a PR against `main`; do not merge it — a GPT-5.5 + Kimi K3 review cycle happens first, coordinated by the parent session.
  - _Acceptance:_ Full test matrix green; overlay-safety check passes; PR open against `main`, unmerged, with a description summarizing scope and linking to the three spec files. _(All requirements)_
