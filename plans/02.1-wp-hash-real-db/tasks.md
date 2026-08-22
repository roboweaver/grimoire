# Tasks — M2.1: WordPress 6.8 `$wp$` Hashes & Real-DB Validation

Implementation checklist for M2.1, following strict TDD. Tasks are ordered so each
builds on the last and references the requirements it satisfies. Keep
`gofmt -l .` empty, `go vet ./...`, `go build ./...`, and `go test -count=1 ./...`
green after every phase (SQLite unconditional; MySQL/Postgres contract gated on
`GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN`; real-WP-DB validation
gated on `GRIMOIRE_TEST_WP_DSN`). Commit in logical increments with the trailer
`Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.

## Phase 0 — Spec
- [ ] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and add the `02.1` row to the `plans/README.md` milestone index.
  - _Acceptance:_ Three Kiro spec files exist matching M2's style; README row `02.1` links to this directory. _(All requirements)_

## Phase 1 — `$wp$` verification (`internal/auth/password`)
- [ ] 1.1 Write failing `TestVerifyWPHash` in `password_test.go`: golden vector `$wp$2y$12$iWN5xRwDE7i9R5jVJvCyqOxS1CNnwUggQaF8O2W9Bg8TuXQz.ngrS` verifies `grimoire-test-123` as true; a wrong password is false; malformed `$wp$` inputs (`"$wp$"`, `"$wp$notbcrypt"`, `"$wp$2y$12$short"`) are `(false, nil)` with no panic.
- [ ] 1.2 Write failing `TestNeedsRehashWPHash`: `NeedsRehash("$wp$2y$12$…")` is `false`.
- [ ] 1.3 Run the two tests; expect FAIL (compile error `isWPHash`/`wpVerify` undefined, or wrong result).
- [ ] 1.4 Create `internal/auth/password/wp.go` (`wpPrefix`, `wpHMACKey`, `isWPHash`, `wpPreHash` via `hmac.New(sha512.New384, …)` + `base64.StdEncoding`, `wpVerify` stripping `hash[len("$wp"):]` through `normalizeBcrypt` then `bcrypt.CompareHashAndPassword`).
- [ ] 1.5 Edit `password.go`: add `case isWPHash(hash): return wpVerify(password, hash)` to `Verify` **before** the bcrypt arm; add `case isWPHash(hash): return false` to `NeedsRehash`; update the package doc comment to list `$wp$`.
- [ ] 1.6 Run `go test ./internal/auth/password/... -run 'WPHash' -v`; expect PASS. Then `go test ./internal/auth/... -count=1` to confirm no M2 regressions.
  - _Acceptance:_ Golden vector verifies; wrong/malformed reject cleanly; `$wp$` reports no rehash; existing phpass/bcrypt tests still pass. _(Req 1.1–1.6, 2.1, 2.3)_

## Phase 2 — Login no-rehash regression lock (`internal/auth`)
- [ ] 2.1 Write failing `TestLoginWPHashNoRehash` in `session_test.go`: seed an in-memory user whose `Pass` is the golden `$wp$` hash; `Login(ctx, login, "grimoire-test-123")` succeeds and returns a session; the fake `UserRepository.UpdatePass` is asserted **never** called (add a call counter/spy to the existing fake if absent).
- [ ] 2.2 Run `go test ./internal/auth/ -run TestLoginWPHashNoRehash -v`; expect PASS immediately (routing already correct via `NeedsRehash=false`). If it fails, fix wiring — do not weaken the assertion.
  - _Acceptance:_ A `$wp$` user authenticates through the real `SessionManager.Login` with zero password-column writes; phpass/bcrypt login behavior unchanged. _(Req 2.1, 2.2, 5.4)_

## Phase 3 — Capabilities scalar truthiness (`internal/auth`)
- [ ] 3.1 Write failing `TestParseCapabilitiesStringOne` in `principal_test.go`: `ParseCapabilities(`a:1:{s:13:"administrator";s:1:"1";}`)` yields roles containing `administrator` (assert identical to the `b:1` variant). Add a falsy case: `s:1:"0"` / `b:0` does NOT grant.
- [ ] 3.2 Run `go test ./internal/auth/ -run TestParseCapabilities -v`; expect PASS (`truthy()` already accepts `"1"`). Fix `truthy`/`ParseCapabilities` only if it surprises.
  - _Acceptance:_ `s:1:"1"` grants the role identically to `b:1`; falsy scalars do not. _(Req 3.1–3.3)_

## Phase 4 — Env-gated real-WP-DB validation (`internal/storage/storagetest`)
- [ ] 4.1 Create `internal/storage/storagetest/wprealdb_test.go` with `TestRealWordPressDB` skipping unless `GRIMOIRE_TEST_WP_DSN` is set; prefix from `GRIMOIRE_TEST_WP_PREFIX` (default `accuweaver`); vendor `mysql`.
- [ ] 4.2 Open via `storage.New` (no migrate), assert `Posts.RecentPosts(ctx,5,0)` returns ≥1 post; via `repos.DB()` read one `$P$%` and one `$wp$%` login, load with `Users.ByLogin`, assert `password.Verify("definitely-wrong-"+…, u.Pass)` is `(false, nil)` and not `ErrUnknownFormat`; optional real-credential success path gated on `GRIMOIRE_TEST_WP_LOGIN`/`GRIMOIRE_TEST_WP_PASSWORD`.
- [ ] 4.3 Run with the gate **off** (`go test ./internal/storage/...`) → test skips, suite green. Run once with the gate **on** against the real DSN and capture output.
  - _Acceptance:_ Skips cleanly without the DB; with the gate on, reads posts and recognizes both `$P$` and `$wp$` on real data; no writes, no hardcoded real hashes. _(Req 4.1–4.6, 5.1)_

## Phase 5 — Gate, docs, PR
- [ ] 5.1 Full gate: `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test -count=1 ./...` (green). Fill the `design.md` Implementation Deviations section with anything discovered.
- [ ] 5.2 Run the real-DB gate ON once end-to-end; record the post count and the `$P$`/`$wp$` recognition results (and real-credential e2e if run) for the creator report.
- [ ] 5.3 Commit; open a PR against `main` using the `roboweaver` gh identity (`unset GH_TOKEN && gh auth switch -u roboweaver`, verify `gh api user -q .login`, then restore `robw_adobe`). Do **not** merge.
  - _Acceptance:_ Full gate green; real-DB validation results captured; PR opened against `main` (not merged); creator notified with PR link + validation results + the two design decisions. _(Req 5.1–5.4)_
