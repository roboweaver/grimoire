# Tasks — M3: Adobe React Spectrum Admin UI

Implementation checklist for M3, following strict TDD. Tasks are ordered so each
builds on the last and references the requirements it satisfies. Keep
`gofmt -l .` empty, `go vet ./...`, `go build ./...`, and `go test ./...` green
after every phase (SQLite unconditional; MySQL/Postgres contract runs gated on
`GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN`; any real-WP-DB check
gated like M2.1). The React build is a **build-time-only** dependency — the
runtime stays pure Go. Commit in logical increments with the trailer
`Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.

## Phase 0 — Spec
- [ ] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and update the `plans/README.md` milestone index (row 03 → Specified; note the milestone 06 CRUD deferral).
  - _Acceptance:_ Three Kiro spec files exist matching M1/M2 style with mermaid diagrams; README row 03 links to this directory. _(All requirements)_
- [ ] 0.2 Report spec-written to the creator session and obtain user spec review before coding.
  - _Acceptance:_ Creator notified; user approves the spec and the five open questions are resolved.

## Phase 1 — Additive read/count ports (`internal/domain`)
- [ ] 1.1 Write failing tests for the new ports' shapes and filter semantics (empty filter = all types/statuses; type/status filtering; limit/offset).
- [ ] 1.2 Add `AdminPostFilter`, `AdminPostRepository` (`ListForAdmin`/`CountForAdmin`), and `PostCounter`/`UserCounter`/`TermCounter` to `internal/domain` (no driver imports).
  - _Acceptance:_ Ports compile and are documented as pure reads; no schema/migration added. _(Req 4.1, 5.1–5.4, 10.1, 10.4)_

## Phase 2 — Vendor implementations + contract tests (`internal/storage/wprepo`)
- [ ] 2.1 Extend `internal/storage/storagetest` with admin-list assertions (drafts + pages included, ordered by date desc, pagination) and count assertions (posts by status, pages, categories, users), parameterized across all vendors.
- [ ] 2.2 Implement `ListForAdmin`/`CountForAdmin` and the `Count*` methods in `internal/storage/wprepo` using Bun, threading `vendor` through `rebind.Rebind` at any raw exec site; map `sql.ErrNoRows`→`domain.ErrNotFound` where relevant.
- [ ] 2.3 Expose the new ports through `storage.Set`/`storage.New`.
  - _Acceptance:_ Contract suite passes on SQLite by default and on MySQL/Postgres when DSNs are set; identical results across vendors; queries are read-only. _(Req 4, 5, 10.1–10.4)_

## Phase 3 — Admin read service (`internal/content`)
- [ ] 3.1 Write failing tests for an admin read service: paginated list (clamps `perPage` via the existing `DefaultPerPage`/`MaxPerPage`), detail by id (`ErrNotFound` passthrough), and dashboard counts aggregation.
- [ ] 3.2 Implement `internal/content/adminread.go` composing the Phase-2 ports and reusing `ByID` for detail; return domain types (no HTTP concerns).
  - _Acceptance:_ Pagination metadata (`total`, `totalPages`) is correct; missing id → `ErrNotFound`; counts match seeded data. _(Req 4.2–4.4, 5.2–5.4, 6.2–6.3)_

## Phase 4 — Admin JSON API (`internal/web`)
- [ ] 4.1 Write failing `httptest` handler tests (fakes for the read service + `Sessions`/principal): `session` returns identity + `csrfToken` and omits secrets; `stats`/`posts`/`posts/{id}` return the documented shapes; `401` unauth, `403` without `edit_posts`, `404` missing id, `405` non-GET; error body is `{error:{code,message}}` with no SQL/secret leakage.
- [ ] 4.2 Implement `internal/web/adminapi.go` handlers over the admin read service and `PrincipalFrom(ctx)`; JSON encode; `slog` each request without secrets.
  - _Acceptance:_ All handler tests pass; responses never leak SQL/hashes/session or CSRF secrets; status codes match the table. _(Req 3.1–3.4, 4.1–4.4, 5.1–5.4, 6.1–6.4, 11.1–11.3)_

## Phase 5 — Embedded SPA package (`internal/admin`)
- [ ] 5.1 Commit a placeholder `internal/admin/dist/index.html` so `//go:embed all:dist` compiles before the first frontend build.
- [ ] 5.2 Write failing tests over a fake `fs.FS`: existing asset served with immutable cache header; unknown non-asset path falls back to `index.html` (no-cache); missing `assets/*` → 404.
- [ ] 5.3 Implement `internal/admin/admin.go` (`//go:embed all:dist`, `fs.Sub`, `Handler(prefix)`), a pure leaf with no domain/web imports.
  - _Acceptance:_ Fallback/asset/404 tests pass; package imports only stdlib + embed. _(Req 1.1–1.6)_

## Phase 6 — Routing + capability gate (`internal/web`, `cmd/grimoire`)
- [ ] 6.1 Write failing router tests: `/admin` and `/admin/api/*` mounted **before** the catch-all; unauthenticated GET `/admin` → 303 `/login?redirect=/admin`; unauthenticated API → 401 JSON; authenticated-without-`edit_posts` → 403; the public catch-all still resolves a normal slug.
- [ ] 6.2 Add `internal/web/adminroutes.go` and a `WithAdmin(handler)` option on the server; register the SPA handler and the API group under `RequireLogin` + `RequireCapability("edit_posts")` (session endpoint under `RequireLogin` only); wire it in `cmd/grimoire/main.go` after `WithAuth`.
  - _Acceptance:_ Admin paths win over `/{slug}`; auth/capability redirects and codes are correct; M1/M2 routes unaffected. _(Req 1.1, 2.1–2.6, 3.1, 7.1)_

## Phase 7 — React Spectrum SPA (`web/admin`)
- [ ] 7.1 Scaffold `web/admin` (Vite + React + TypeScript); add `@adobe/react-spectrum`, `@spectrum-icons/workflow`; `vite.config.ts` with `base: "/admin/"` and `build.outDir: ../../internal/admin/dist`.
- [ ] 7.2 Implement the app shell: `<Provider theme={defaultTheme}>`, Spectrum layout/nav, and a same-origin `fetch` client sending credentials, mapping `401`→`/login?redirect=…` and `403`→insufficient-permissions view.
- [ ] 7.3 Implement the three views with Spectrum components: **Dashboard** (`/admin/api/stats`), **PostsList** (paginated `/admin/api/posts`, incl. drafts/pages, loading/empty/error states), **PostDetail** (`/admin/api/posts/{id}`).
  - _Acceptance:_ SPA builds; uses only Spectrum components/tokens/icons (no hardcoded palette, bundled Adobe Clean, MUI/Ant/etc., or scraped logo); handles loading/empty/error and 401/403. _(Req 2.4, 3.x consumer, 8.1–8.6)_

## Phase 8 — Build & embed pipeline (`Makefile`, CI)
- [ ] 8.1 Add a `make admin` target (`cd web/admin && npm ci && npm run build`) that writes `internal/admin/dist`; run it and commit the built assets.
- [ ] 8.2 Add a CI freshness job: `make admin` then `git diff --exit-code internal/admin/dist` (fails on drift); ensure `go build`/`go test ./...` need no Node.
  - _Acceptance:_ `make admin` reproduces committed `dist`; CI drift check works; a clean `go install ./...` produces a working admin with no Node present. _(Req 1.6, 9.1–9.4)_

## Phase 9 — End-to-end & validate
- [ ] 9.1 End-to-end SQLite test: seed a capable user + posts → `POST /login` → `GET /admin` (200 shell) → `GET /admin/api/{session,stats,posts,posts/{id}}` return expected JSON → expired/no session → 401/redirect.
- [ ] 9.2 Confirm overlay-safety: the admin read paths run against a DB with no M3 migrations (only the additive M2 `{prefix}sessions` table); optionally env-gated against a real WP DB like M2.1.
- [ ] 9.3 Record any deviations back into `requirements.md` (Implementation deviations) and `design.md`.
  - _Acceptance:_ E2E passes; no schema changes required to serve the admin; deviations documented. _(Req 7.1, 10.1–10.4, 11.x)_

## Phase 10 — Validate & PR
- [ ] 10.1 Full gate sweep: `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...` all green; MySQL/Postgres contract runs if DSNs available; `make admin` freshness check clean.
- [ ] 10.2 Open the M3 PR against `main` (do **not** merge) and report PR-open to the creator session.
  - _Acceptance:_ CI-equivalent gates pass locally; PR open with a milestone summary and the M3-read-only / milestone 06-CRUD scope note. _(All requirements; Milestone success criterion)_
