# Tasks — M1: Content Core & Public Read Rendering

Implementation checklist for M1. Tasks are ordered so each builds on the last.
Each references the requirements it satisfies. Check items off as they land.

## Phase 0 — Project setup
- [ ] 0.1 Add dependencies (`chi`, `bun` + `bun` dialects for mysql/pg/sqlite, and the **pure-Go** `modernc.org/sqlite` driver to keep CGO off and preserve the single static-binary goal) and run `go mod tidy`.
  - _Acceptance:_ `go build ./...` succeeds with pinned versions in `go.mod`/`go.sum`.
- [ ] 0.2 Establish CI (build, `go vet`, `gofmt -l`, unit tests on SQLite) and a `Makefile`/task runner for `run`, `migrate`, `test`.
  - _Acceptance:_ CI passes on a clean checkout; SQLite tests run without external services. _(Req 11.2)_

## Phase 1 — Configuration & startup
- [ ] 1.1 Implement `internal/config` loader (YAML + env overrides), validation, and DSN redaction.
  - _Acceptance:_ Invalid/missing vendor fails fast with a clear message; credentials never appear in logs. _(Req 1.2, 1.3)_
- [ ] 1.2 Wire `cmd/grimoire` to load config, build the storage factory, load the theme, and serve; graceful shutdown.
  - _Acceptance:_ Server starts against a valid SQLite config and serves `/healthz`. _(Req 1.1)_

## Phase 2 — Domain
- [ ] 2.1 Define entities (`Post`, `Term`, `Option`) and the `ErrNotFound` sentinel in `internal/domain`.
- [ ] 2.2 Define `PostRepository`, `TermRepository`, `OptionRepository` interfaces (no driver imports).
  - _Acceptance:_ `internal/domain` compiles with zero database-driver imports. _(Req 3.1)_

## Phase 3 — Schema & migrations
- [ ] 3.1 Author M1 migrations for **MySQL** (7 tables, `wp_` prefix, WordPress-accurate types).
- [ ] 3.2 Author equivalent **PostgreSQL** migrations (type-mapped).
- [ ] 3.3 Author equivalent **SQLite** migrations (type-mapped).
- [ ] 3.4 Implement migration runner in `grimoire-cli migrate` (ordered, idempotent, reports version).
  - _Acceptance:_ Running `migrate` twice on each vendor is a no-op the second time. _(Req 2.2, 2.3, 2.4, 10.1)_

## Phase 4 — Storage adapters
- [ ] 4.1 Implement `internal/storage/sqlite` adapters for all three repositories via Bun; translate `sql.ErrNoRows` → `domain.ErrNotFound`.
- [ ] 4.2 Implement `internal/storage/postgres` adapters.
- [ ] 4.3 Implement `internal/storage/mysql` adapters.
- [ ] 4.4 Implement `storage.Factory(cfg)` selecting the adapter set from config.
  - _Acceptance:_ Factory returns a working repository set for each configured vendor. _(Req 3.2, 3.3, 3.4)_

## Phase 5 — Cross-vendor contract tests
- [ ] 5.1 Build `internal/storage/storagetest` — one assertion suite parameterized by adapter, with shared seed data.
- [ ] 5.2 Run the suite on SQLite by default; add container-backed runs for MySQL/Postgres gated by an env opt-out.
  - _Acceptance:_ Identical results across all vendors for posts, taxonomy, and options on the same seed. _(Req 11.1, 11.3, 11.4)_

## Phase 6 — Content services
- [ ] 6.1 `PostService`: recent posts (publish-only, paginated) and by-slug (post/page).
  - _Acceptance:_ Non-`publish` rows are never returned; missing slug yields `ErrNotFound`. _(Req 4.1–4.5)_
- [ ] 6.2 `TermService`: resolve category by slug and list its published posts.
  - _Acceptance:_ Unknown category slug yields `ErrNotFound`. _(Req 5.1–5.4)_
- [ ] 6.3 `OptionService`: get named options; absent → empty, no error.
  - _Acceptance:_ `blogname`/`blogdescription` available; missing option returns "". _(Req 6.1–6.3)_

## Phase 7 — Rendering & theme
- [ ] 7.1 Implement `internal/render`: theme loading at startup (fail fast on missing base) and the template-hierarchy subset.
  - _Acceptance:_ Missing base template names the file and prevents startup. _(Req 8.1, 8.2)_
- [ ] 7.2 Complete the `default` theme: `base`, `index`, `single`, `page`, `category` templates.
  - _Acceptance:_ Home, single, and category views render complete server-side HTML. _(Req 7.6, 8.3)_
- [ ] 7.3 Add golden-file render tests for the default theme.

## Phase 8 — Web layer
- [ ] 8.1 Implement `chi` router and handlers for `/`, `/{slug}`, `/category/{slug}`.
- [ ] 8.2 Add middleware: request-scoped `slog`, panic recovery, and error→status mapping (404/500, no internal leakage).
  - _Acceptance:_ 404 for unknown slug/category; 500 logs error without exposing SQL; 200 sets `text/html; charset=utf-8`. _(Req 7.1–7.5, 9.1–9.4)_

## Phase 9 — CLI seed & end-to-end
- [ ] 9.1 Implement `grimoire-cli seed` (sample posts, one category, required options) for a migrated database.
  - _Acceptance:_ After `migrate` + `seed`, the server renders a populated home page. _(Req 10.2)_
- [ ] 9.2 End-to-end smoke test: migrate → seed → start → assert home, single, and category responses. Run on SQLite in CI.
- [ ] 9.3 **Compatibility check:** point grimoire at a real exported WordPress MySQL database and verify posts/pages render read-only.
  - _Acceptance:_ Existing WordPress content renders without modification to the source data. _(Milestone success criterion)_

## Phase 10 — Docs
- [ ] 10.1 Update `README.md` and `configs/` with verified run instructions for each vendor.
- [ ] 10.2 Record any deviations from this spec back into `requirements.md`/`design.md`.
