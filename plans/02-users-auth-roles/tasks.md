# Tasks — M2: Users, Authentication & Roles

Implementation checklist for M2, following strict TDD. Tasks are ordered so each
builds on the last and references the requirements it satisfies. Keep
`gofmt -l .` empty, `go vet ./...`, `go build ./...`, and `go test ./...` green
after every phase (SQLite unconditional; MySQL/Postgres contract runs gated on
`GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN`). Commit in logical
increments with the trailer
`Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.

## Phase 0 — Spec
- [x] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and update the `plans/README.md` milestone index (row 02 → Specified).
  - _Acceptance:_ Three Kiro spec files exist matching M1's style; README row 02 links to this directory. _(All requirements)_
- [x] 0.2 Report spec-written to the creator session and obtain user spec review before coding.
  - _Acceptance:_ Creator notified; user approves the spec.

## Phase 1 — PHP serialize/unserialize (`internal/php`)
- [x] 1.1 Write failing tests for `Serialize`/`Unserialize` covering `bool`, `int`, `string`, and associative arrays, including real WordPress `{prefix}capabilities` strings.
- [x] 1.2 Implement `internal/php` to pass them; no driver imports.
  - _Acceptance:_ `a:1:{s:13:"administrator";b:1;}` round-trips to `map[string]any{"administrator": true}` and back. _(Req 1.6, 3.2)_

## Phase 2 — Password & phpass (`internal/auth/password`, `internal/auth/phpass`)
- [x] 2.1 Write failing `phpass.Verify` tests against known WordPress `$P$` vectors.
- [x] 2.2 Implement the pure-Go phpass portable-hash verifier (iterated MD5 + phpass base-64).
- [x] 2.3 Write failing tests for `password.Hash`/`Verify`/`NeedsRehash` (bcrypt, phpass, `$2y$`→`$2a$` normalization, unknown → false).
- [x] 2.4 Implement `internal/auth/password` dispatching by prefix, constant-time, never logging plaintext.
  - _Acceptance:_ bcrypt and phpass passwords verify; phpass and low-cost bcrypt report `NeedsRehash`; `$wp$`/unknown reject cleanly. _(Req 2.1–2.6)_

## Phase 3 — Schema & migration 0002
- [x] 3.1 Author `0002_users_auth.up.sql` for **sqlite**, **postgres**, **mysql**: extend `{{prefix}}users` columns, create `{{prefix}}usermeta`, create `{{prefix}}sessions` (Postgres `ADD COLUMN IF NOT EXISTS`; sqlite/mysql plain `ADD COLUMN`; tables via `CREATE TABLE IF NOT EXISTS` with indexes).
- [x] 3.2 Confirm the embedded migration runner applies `0002` after `0001` and is idempotent.
  - _Acceptance:_ `migrate` runs clean twice on SQLite (and MySQL/Postgres when DSNs set); `schema_migrations` records `0002`. _(Req 1.1–1.5, 10.2)_

## Phase 4 — Repositories + contract tests
- [x] 4.1 Add `User`/`Session` entities and `UserRepository`/`UserMetaRepository`/`SessionRepository` + Post/Term/Option writer ports to `internal/domain` (no driver imports).
- [x] 4.2 Implement the repos and content write methods in `internal/storage/wprepo`, threading `vendor` through `rebind.Rebind` at any raw exec site; translate `sql.ErrNoRows`→`domain.ErrNotFound`.
- [x] 4.3 Extend `storage.Set`/`storage.New` to expose Users, UserMeta, Sessions, and the writers.
- [x] 4.4 Extend `internal/storage/storagetest` with user/usermeta/session CRUD (incl. serialized capabilities round-trip) and content write assertions, parameterized across all vendors.
  - _Acceptance:_ Contract suite passes on SQLite by default and on MySQL/Postgres when DSNs are set; identical results across vendors. _(Req 1, 6.6, 10.1–10.4)_

## Phase 5 — Roles & capabilities (`internal/auth`)
- [x] 5.1 Write failing tests for the five roles' capability sets, `Can`, and `CapabilitiesFromMeta`.
- [x] 5.2 Implement `Principal`, the roles→capabilities map, `Can`, and meta parsing (via `internal/php`).
  - _Acceptance:_ Each role resolves its documented capabilities; per-user caps union correctly; no driver imports. _(Req 3.1–3.4)_

## Phase 6 — Session manager (`internal/auth`)
- [x] 6.1 Write failing tests for `Login` (verify + rehash-on-success + session create), `Authenticate` (hash lookup + expiry + rolling refresh), `Logout`, `GC`, `RevokeUser`.
- [x] 6.2 Implement `SessionManager` over the repos + `password`; store only SHA-256(token); generic errors; constant-time; rolling expiry refresh when remaining < half TTL.
  - _Acceptance:_ Valid login mints a session and rehashes a phpass hash; expired tokens authenticate as anonymous; logout/GC/revoke delete the right rows. _(Req 2.4, 4.1–4.8)_

## Phase 7 — Content write services + authorization (`internal/content`)
- [x] 7.1 Write failing allow/deny tests across the five roles for post/page create/update/delete (ownership), term writes, option set, and user writes.
- [x] 7.2 Implement the authorization policy helper and the Post/Term/Option/User write services taking an `auth.Principal`, enforcing capabilities before any mutation.
  - _Acceptance:_ Ownership rules hold (`edit_others_*`/`publish_*`/`delete_others_*`); term→`manage_categories`, option→`manage_options`, user→`create_users`/`edit_users`/`delete_users`/`list_users`; denial performs no write. _(Req 6.1–6.7)_

## Phase 8 — Web auth (login/logout, middleware, template)
- [x] 8.1 Write failing handler tests: `GET /login` renders the form + CSRF field; `POST /login` valid → 303 + `Set-Cookie` (HttpOnly/Lax/Secure); invalid → generic error, no enumeration; missing/mismatched CSRF → 403; `POST /logout` clears session + cookie.
- [x] 8.2 Implement `SessionMiddleware`, `CSRFMiddleware`, `RequireLogin`/`RequireCapability`, the login/logout handlers, and `themes/default/login.tmpl`; wire routes `GET/POST /login`, `POST /logout`.
  - _Acceptance:_ Handler tests pass; responses never leak SQL/hashes/tokens; cookie attributes correct. _(Req 4.3, 5.1–5.4, 7.1–7.5, 9.1–9.2)_

## Phase 9 — CLI, config & end-to-end
- [x] 9.1 Add session/cookie config keys to `internal/config` (+ sample configs); no signing secret.
- [x] 9.2 Implement `grimoire-cli createadmin` (bcrypt + `administrator` capabilities + `user_level=10`; refuse existing login) and `sessions gc` (delete expired, report count).
- [x] 9.3 End-to-end SQLite test: `createadmin` → `POST /login` → authenticated request → `POST /logout`.
- [x] 9.4 Record any deviations back into `requirements.md` (Implementation deviations) and `design.md`.
  - _Acceptance:_ `createadmin` then login succeeds; `sessions gc` removes only expired rows; CLI never prints secrets. _(Req 8.1–8.4)_

## Phase 10 — Validate & PR
- [ ] 10.1 Full gate sweep: `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...` all green; run MySQL/Postgres contract runs if DSNs available.
- [ ] 10.2 Open **PR #2** against `main` (do **not** merge) and report PR-open to the creator session.
  - _Acceptance:_ CI-equivalent gates pass locally; PR open with a milestone summary. _(All requirements; Milestone success criterion)_
