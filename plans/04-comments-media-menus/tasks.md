# Tasks — M4: Comments, Media Library, Navigation Menus

Implementation checklist for M4, following strict TDD. Tasks are ordered so each
builds on the last and references the requirements it satisfies. Keep
`gofmt -l .` empty, `go vet ./...`, `go build ./...`, and `go test ./...` green
after every phase (SQLite unconditional; MySQL/Postgres contract runs gated on
`GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN`; any real-WP-DB check
gated like M2.1). The React build is a **build-time-only** dependency — the
runtime stays pure Go. Commit in logical increments with the trailer
`Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.

## Phase 0 — Spec
- [ ] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and update the `plans/README.md` milestone index (row 04 → Specified; note menu-editing deferral to a later milestone).
  - _Acceptance:_ Three Kiro spec files exist matching M1–M3 style with Mermaid diagrams; README row 04 links to this directory. _(All requirements)_
- [ ] 0.2 Report spec-written to the creator session and obtain user spec review before coding.
  - _Acceptance:_ Creator notified; user approves the spec and any open questions are resolved.

## Phase 1 — Domain types + ports (`internal/domain`)
- [ ] 1.1 Write failing tests for the new port shapes and filter semantics (empty `CommentFilter.Statuses` = all; `PostID` scoping; `MediaFilter.ParentID`; menu empty-degradation contract).
- [ ] 1.2 Add entities `Comment`, `CommentMeta`, `Media`, `NavMenu`, `NavMenuItem` to `entities.go`, modeling `comment_approved` as the raw string enum (`"0"/"1"/"spam"/"trash"`).
- [ ] 1.3 Add ports `CommentRepository`, `CommentWriter`, `CommentMetaRepository`, `MediaRepository`, `MediaWriter`, `NavMenuRepository`, `CommentSpamFilter`, and `CommentFilter`/`MediaFilter` structs to `repository.go` (no driver imports).
  - _Acceptance:_ Ports compile; reads documented as additive `SELECT`/`COUNT`; only the comments tables imply schema (greenfield migration), media/menus reuse existing tables. _(Req 1.1, 5.1, 5.4–5.5, 6.4, 10.4, 10.6, 13.1–13.4)_

## Phase 2 — Vendor adapters + migration + contract tests (`internal/storage`)
- [ ] 2.1 Add the greenfield-only additive migration `0003_comments_media_menus.up.sql` (mysql/postgres/sqlite) creating `{{prefix}}comments` + `{{prefix}}commentmeta` with `IF NOT EXISTS`, WP-compatible columns/indexes; SQLite quotes `"comment_ID"` and maps DATETIME→TEXT.
- [ ] 2.2 Extend `storagetest` `SeedFixtures` with deterministic comment rows (each status), attachment posts + `_wp_attached_file` meta, and a `nav_menu` term with `nav_menu_item` posts + `_menu_item_*` meta.
- [ ] 2.3 Write `runCommentsContract`, `runMediaContract`, `runMenusContract` (list/count/by-id, create defaults + `UpdateStatus`/`SetParent`, `MenuBySlug` tree, empty-menu degradation) and invoke them from `RunContract`, parameterized across vendors.
- [ ] 2.4 Implement `wprepo/comments.go`, `wprepo/media.go`, `wprepo/menus.go` with Bun (`*Columns`, `*Row`+`toDomain()`, `rebind.Rebind(vendorOf(db), q)` for raw JOINs, `insertReturningID`, `errNotFoundIfZero`); add compile-time `var _ domain.X = (*Repo)(nil)` assertions; map `sql.ErrNoRows`→`domain.ErrNotFound`.
- [ ] 2.5 Wire the new repos through `storage.Set`/`storage.New` (fields `Comments`, `CommentWriter`, `CommentMeta`, `Media`, `MediaWriter`, `NavMenus`), each backing repo constructed once and exposed under all the ports it satisfies.
  - _Acceptance:_ Contract suite passes on SQLite by default and on MySQL/Postgres when DSNs are set; identical results across vendors; media/menu reads add no table; overlay-safe. _(Req 5.2–5.3, 6.1–6.5, 9.2, 10.1–10.5, 13.1–13.5)_

## Phase 3 — Content services + spam filter (`internal/content`)
- [ ] 3.1 Write failing tests: comment submit defaults to `comment_approved='0'`; closed/missing/unpublished post rejected; spam-filter outcome routed to the right status; attachment path assembly (`YYYY/MM`, de-dup, sanitized name); menu tree built from flat items via `_menu_item_menu_item_parent`.
- [ ] 3.2 Implement `content/comments.go` (`CommentService`: `Submit`, `List`, `SetStatus`), `content/media.go` (`MediaService`: `Store`, `Attach`, `List`, URL assembly), `content/menus.go` (`MenuService`: `Menu`, `Menus`, tree builder).
- [ ] 3.3 Implement `content/spam.go`: the `CommentSpamFilter` default (honeypot, per-IP rate limit, link/keyword heuristic) returning `approve`/`hold`/`spam`; spam quarantined, not dropped.
  - _Acceptance:_ Services return domain types (no HTTP concerns); queue default holds strangers; spam quarantined as `'spam'`; menu tree correct; missing parent → `ErrNotFound`. _(Req 2.2–2.5, 3.1–3.5, 8.2–8.3, 9.2–9.4, 10.3, 11.x consumer)_

## Phase 4 — Public HTTP: comments, uploads, nav render (`internal/web`, `internal/render`, theme)
- [ ] 4.1 Write failing `httptest` tests: comment submit (double-submit token pass/fail, honeypot, held-by-default, `404`/`403` for missing/closed post, escaped echo); uploads server (serve with correct `Content-Type`/`Cache-Control`, traversal/`..`/symlink → `404`/`400`, missing → `404`); nav render into the theme.
- [ ] 4.2 Extend `authmiddleware.go` `requireSessionCSRF` to also accept an `X-CSRF-Token` header (constant-time compare); add a double-submit token helper for the anonymous comment form.
- [ ] 4.3 Implement `web/comments.go` (public list wired into single/page render + `POST` submit) and `render/comments.go` + `partials/comments.tmpl` (approved list, empty state, submit form with honeypot + token; comment content escaped/sanitized).
- [ ] 4.4 Implement `web/uploads.go` (read-only, traversal-safe file server honoring `MediaConfig`; document the proxy mode) and register it plus the comment routes in `router.go` **before** the public catch-all and without shadowing `/admin`.
- [ ] 4.5 Implement `render/menus.go` + `partials/nav-menu.tmpl` and include the nav menu in `single.tmpl`/`page.tmpl`/base header (nested `<ul>/<li>`, active item marked, labels/URLs escaped, empty-location degradation).
  - _Acceptance:_ Public comment queue + escaping works; uploads serve safely and reject traversal; nav menu renders from a seeded `nav_menu`; routes don't shadow framework paths. _(Req 1.2–1.6, 2.1–2.7, 3.2, 7.1–7.6, 11.1–11.3, 12.3, 12.6, 15.4)_

## Phase 5 — Admin JSON API (`internal/web`)
- [ ] 5.1 Write failing handler tests (fakes for services + `Sessions`/principal): comments list/status, media list/upload/attach, menus read — asserting `401` unauth, `403` without capability, `403` on missing/mismatched `X-CSRF-Token`, `404` missing id, `413` oversized upload, `400` disallowed MIME, `405` bad method, and `{error:{code,message}}` bodies with no SQL/secret/path leakage.
- [ ] 5.2 Implement `adminapi_comments.go` (GET list filterable by status/post; `POST /{id}/status` gated `moderate_comments` + CSRF).
- [ ] 5.3 Implement `adminapi_media.go` (GET list; `POST` multipart upload gated `upload_files` + CSRF with server-side sniff/allowlist/size; `POST /{id}/attach`).
- [ ] 5.4 Implement `adminapi_menus.go` (GET menus, GET menu by id, gated `edit_posts`) and mount all groups in `adminroutes.go` under the existing auth/capability middleware, before the SPA fallback.
  - _Acceptance:_ All handler tests pass; capabilities + CSRF enforced; responses never leak secrets/SQL/paths; status codes match the API table; results identical across vendors. _(Req 4.1–4.6, 6.1–6.5, 8.1–8.6, 9.1–9.5, 11.4, 12.1–12.2, 12.5, 15.1–15.4)_

## Phase 6 — Config + wiring (`internal/config`, `cmd/grimoire`)
- [ ] 6.1 Add `MediaConfig` (`uploads_dir`, `base_url`, `max_bytes`, `allow_mime`, `serve_mode`, `proxy_origin`) with sane defaults and validation; document in the example config.
- [ ] 6.2 Thread the new services, spam filter, and uploads config through `cmd/grimoire/main.go` (`web.NewServer(...).WithAuth(...).WithAdmin(...)` gains comment/media/menu handlers + the uploads route).
  - _Acceptance:_ Server boots with defaults; uploads dir + limits are configurable; no driver imports leak above storage. _(Req 7.1, 7.6, 8.4, 13.4)_

## Phase 7 — React Spectrum SPA (`web/admin`)
- [ ] 7.1 Add `Comment`, `Media`, `NavMenu` types to `api/types.ts` and client methods to `api/client.ts` (`listComments`/`moderateComment`, `listMedia`/`uploadMedia`/`attachMedia`, `listMenus`), attaching `X-CSRF-Token` (from `/admin/api/session`) on unsafe calls and mapping `401`→login, `403`→forbidden.
- [ ] 7.2 Add **Comments**, **Media**, **Menus** entries to the `AppShell.tsx` `NAV` array and routes in `App.tsx`.
- [ ] 7.3 Implement `views/Comments.tsx` (Spectrum `TableView`, status filter, per-row/bulk approve/unapprove/spam/trash), `views/Media.tsx` (Spectrum grid + `DropZone`/`FileTrigger` upload with progress + success/error), `views/Menus.tsx` (read-only nested tree/list, editing-deferred note).
  - _Acceptance:_ SPA builds; uses only Spectrum components/tokens/icons (no hardcoded palette, bundled Adobe Clean, third-party UI kit, or scraped logo); each view handles loading/empty/error and 401/403; keyboard + screen-reader accessible. _(Req 11.5, 14.1–14.6)_

## Phase 8 — Build & embed pipeline (`Makefile`, CI)
- [ ] 8.1 Rebuild the embedded SPA (`make admin`) so `internal/admin/dist` includes the new views; commit the built assets.
- [ ] 8.2 Confirm the CI freshness job (`make admin` then `git diff --exit-code internal/admin/dist`) still passes and that `go build`/`go test ./...` need no Node.
  - _Acceptance:_ `make admin` reproduces committed `dist`; drift check clean; a clean `go install ./...` yields a working admin with no Node present. _(Req 14.1, 14.6)_

## Phase 9 — Integration, end-to-end & overlay validation
- [ ] 9.1 End-to-end SQLite test: seed a capable user + published post → `POST /comments` (held) → `GET` post shows no public comment yet → `POST /admin/api/comments/{id}/status` approve (with CSRF) → post render now lists the comment.
- [ ] 9.2 End-to-end media test: `POST /admin/api/media` (multipart) → file written under `YYYY/MM` → attachment row created → `GET /wp-content/uploads/...` serves it; disallowed MIME/oversized rejected; traversal rejected.
- [ ] 9.3 End-to-end menu test: seed a `nav_menu` term + items → public theme header renders the nested menu → `GET /admin/api/menus` returns the same tree.
- [ ] 9.4 Overlay-safety: run comment moderation + media/menu reads against a DB with only the additive migrations (no destructive change); optionally env-gated against a real WP DB like M2.1. Record any deviations in `requirements.md`/`design.md`.
  - _Acceptance:_ All three e2e flows pass; overlay adds no schema beyond the additive comments migration; deviations documented. _(Req 1.x, 2.x, 4.4, 7.x, 9.x, 10.x, 11.x, 13.1–13.5)_

## Phase 10 — Validate & PR
- [ ] 10.1 Full gate sweep: `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...` all green; MySQL/Postgres contract runs if DSNs available; `make admin` freshness check clean.
- [ ] 10.2 Open the M4 PR against `main` (do **not** merge) and report PR-open to the creator session, noting the menu-editing deferral and the first-write-path CSRF/spam posture.
  - _Acceptance:_ CI-equivalent gates pass locally; PR open with a milestone summary and the read-only-menus / deferred-editing scope note. _(All requirements; Milestone success criterion)_
