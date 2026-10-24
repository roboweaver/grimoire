# Implementation Plan: M10.G — $wp$ as grimoire's issued password format

## Overview

Implementation checklist for M10.G (roadmap group **10.G**, the **final
milestone of roadmap group 10**). Tasks are ordered so each builds on the last,
and each references the exact files, the exact symbols named in `design.md`'s
"Components and Interfaces" / "Data Models" sections, and the Requirement/AC plus
the Inherited decision (D1–D6) it satisfies. Keep `gofmt -l .` empty and
`go vet ./...`, `go build ./...`, `go test ./...` green after every phase.

> **Scope is ISSUANCE only.** The entire verification surface — `password.Verify`
> routing `$wp$`→`wpVerify`, phpass, and `$2y$`→`normalizeBcrypt`, each with the
> `wpMagicQuotesSlash` retry — already exists and does **not** change, and the
> login-rehash transport (`SessionManager.Login`'s `if
> password.NeedsRehash(u.Pass) { if nh, herr := password.Hash(pw); herr == nil {
> _ = m.Users.UpdatePass(ctx, u.ID, nh) } }`, `session.go:98`) is already wired.
> The surgical change is three edits in **one package** (`internal/auth/password`):
> add `HashWP`, repoint `Hash` to call it, and flip the `NeedsRehash` bcrypt
> branch to return `true` for any recognized bcrypt. All three issuance sites —
> `userservice.go:76` (`provision`→`Create`/`Bootstrap`), `userservice.go:109`
> (`SetPassword`), and `session.go:99` (login rehash) — flip at once through the
> single `password.Hash` choke point with **no caller edit** (D1). `$wp$` is
> terminal (verified in place, never rehashed — guaranteed by `NeedsRehash`
> checking `isWPHash` before `isBcrypt`). There is **no config flag**: `$wp$`
> issuance is unconditional (D4/R8). `fasthash.go`, `$generic$`, and Application
> Password hashing are untouched (R9). This milestone **supersedes 10.C's**
> "never introduce a second hash scheme" wording: `$wp$` *replaces* bcrypt as the
> single issued scheme (D3/R6).
>
> **Standing gate convention.** `gofmt -l .` empty, and `go vet ./...`,
> `go build ./...`, `go test ./...` green at the end of every phase, matching
> M10.B/M10.C's close-out discipline (D6/R10.7).
>
> **Strict TDD spine.** Within each phase the failing test is written **before**
> the implementation that passes it, matching how M5–M10.D were run. The ordering
> below makes that spine visible — every behavior-adding sub-task is preceded by
> the sub-task that writes its failing test (R10).
>
> **Two existing tests change expectations — deliberately, as part of the test
> tasks.** `TestHashAndVerify` (`password_test.go`) currently asserts `Hash`
> output is `$2`-prefixed; task 1.1 flips that assertion to `$wp$`, since the
> issued format changes (R2.1). `TestNeedsRehash`'s `{"bcrypt default cost", …,
> false}` cell is the pre-10.G policy; task 2.1 flips it to `true` — the D2
> reversal (R3.1). A third existing test outside the password package —
> `TestSessionManager_LoginRehashesPhpass` (`session_test.go`), which asserts the
> rehashed hash starts with `$2` — is updated to expect `$wp$` as part of the
> Phase 3 integration task, since the rehash now emits `$wp$`. These are UPDATED
> to the new expectations, not left asserting old behavior.

## Tasks

## Phase 1 — `HashWP` + repoint `Hash` (`internal/auth/password`)

- [ ] 1.1 Write the failing issuance tests first in
      `internal/auth/password/password_test.go`. Flip `TestHashAndVerify`: change
      the `strings.HasPrefix(h, "$2")` assertion (and its failure message) to
      require `strings.HasPrefix(h, "$wp$")`, keeping the round-trip assertions
      that `Verify("s3cret-passphrase", h)` → `(true, nil)` and `Verify("wrong",
      h)` → `(false, nil)` (R1.2, 1.3, 2.1, 10.1). Add a focused `TestHashWP` that
      calls `HashWP` directly and asserts: its output is `$wp$`-prefixed (R1.1,
      1.2); `Verify(pw, h)` → `(true, nil)` and `Verify("wrong", h)` →
      `(false, nil)` (R1.3); and a truncation-defeat case — construct two distinct
      passwords longer than 72 bytes that **share their first 72 bytes** (e.g. a
      72-byte common prefix plus differing tails), hash each with `HashWP`, and
      assert `Verify(pwA, HashWP(pwA))` is true while `Verify(pwB, HashWP(pwA))` is
      false (and symmetrically), proving the HMAC-SHA384 pre-hash feeds the whole
      password to bcrypt rather than a 72-byte-truncated prefix (R7.1, 7.2). Reuse
      `wpGoldenPlain` where a known plaintext is handy; embed no real user hashes
      (R10.5). These FAIL until 1.2 (`HashWP` absent; `Hash` still `$2`).
      Files: `internal/auth/password/password_test.go`
      Verify: `go test ./internal/auth/password` — fails (`HashWP` undefined;
      `TestHashAndVerify` `$wp$` assertion unmet). _(Req 1.1, 1.2, 1.3, 2.1, 7.1,
      7.2, 10.1, 10.5; D1, D5)_

- [ ] 1.2 Add `HashWP` and repoint `Hash` per design. In
      `internal/auth/password/wp.go` add the exported
      `HashWP(password string) (string, error)` as
      `b, err := bcrypt.GenerateFromPassword(wpPreHash(password), DefaultCost)`;
      `if err != nil { return "", err }`; `return wpPrefix + string(b), nil` — no
      `[]byte(...)` conversion (`wpPreHash` already returns `[]byte`), prefixing
      with the existing `wpPrefix` (`"$wp$"`) constant (R1.1, 1.4, 1.5, 7.1). In
      `internal/auth/password/password.go` replace the body of `Hash` with
      `return HashWP(password)`, removing the old
      `bcrypt.GenerateFromPassword([]byte(password), DefaultCost)` body entirely
      (no dead bcrypt-only issuer left behind — bcrypt is verify-only after this,
      D3/R6); the signature is unchanged so all three callers compile untouched
      (R2.1–2.4, D1). Update the `Hash` doc comment to state it delegates to
      `HashWP` and emits `$wp$`. Leave `wpPreHash`, `wpVerify`, `isWPHash`,
      `Verify`, and `normalizeBcrypt` unchanged (R5).
      Files: `internal/auth/password/wp.go`, `internal/auth/password/password.go`
      Verify: `go test ./internal/auth/password` — the 1.1 tests pass;
      `go vet ./...` clean. _(Req 1.1, 1.4, 1.5, 2.1, 5.1, 6.1, 6.2, 7.1; D1, D3,
      D5)_

## Phase 2 — `NeedsRehash` bcrypt-branch flip (`internal/auth/password`)

- [ ] 2.1 Write the failing upgrade-policy tests first in
      `internal/auth/password/password_test.go`. In `TestNeedsRehash`, flip the
      `{"bcrypt default cost", string(strong), false}` case to expect `true` — the
      D2 reversal (R3.1) — and keep `{"phpass always rehash", …, true}` (R3.3),
      `{"bcrypt below default cost", string(weak), true}` (R3.2), and
      `{"unknown format", "garbage", true}` (R3.4). `strong`/`weak` stay built with
      `bcrypt.GenerateFromPassword` in-test. Leave `TestNeedsRehashWPHash` asserting
      `NeedsRehash(wpGolden) == false` (R3.5). Add a case (extend `TestNeedsRehash`
      or a focused test) that a FRESH `Hash` output — now `$wp$` after Phase 1 —
      reports `NeedsRehash == false`, confirming an issued hash is terminal
      (R3.5, 10.2). This FAILS until 2.2 (the bcrypt branch still returns
      `cost < DefaultCost`, i.e. `false` for `strong`).
      Files: `internal/auth/password/password_test.go`
      Verify: `go test ./internal/auth/password` — fails (default-cost bcrypt case
      expects `true`, impl still returns `false`). _(Req 3.1, 3.2, 3.3, 3.4, 3.5,
      10.2, 10.3; D2)_

- [ ] 2.2 Flip only the bcrypt branch of `NeedsRehash` in
      `internal/auth/password/password.go`: change the `case isBcrypt(hash):` body
      from the `cost, err := bcrypt.Cost(...); return cost < DefaultCost` logic to
      a bare `return true` — any recognized bcrypt is now legacy and flagged for
      upgrade regardless of cost (D2/R3.1, 3.2). Leave the switch's branch order
      intact: `case isWPHash(hash): return false` stays FIRST, so an issued `$wp$`
      hash returns `false` before the bcrypt branch is ever reached and stays
      terminal despite its embedded `DefaultCost` bcrypt (R3.5, Finding 4). Leave
      the `phpass.Identify` → `true` and `default: return true` branches unchanged
      (R3.3, 3.4). The branch no longer calls `bcrypt.Cost`/`normalizeBcrypt`;
      those remain used by `Verify`/`wpVerify`, so no import is removed.
      Files: `internal/auth/password/password.go`
      Verify: `go test ./internal/auth/password` — the 2.1 tests pass and all
      existing password tests (verification, golden `$wp$`) still pass;
      `go vet ./...` clean. _(Req 3.1, 3.2, 3.3, 3.4, 3.5; D2)_

## Phase 3 — Login-upgrade integration test (`internal/auth`)

- [ ] 3.1 Add the end-to-end login-upgrade test over `SessionManager.Login`,
      reusing the existing session test harness, and reconcile the one existing
      session test that now expects the new format. This is a **test-only** task:
      the rehash behavior is already wired in `session.go` and is made correct by
      Phases 1–2, so there is no separate impl task. In `internal/auth` (add a new
      `*_test.go` or extend `session_test.go`) build a manager via `newTestManager`
      and, using `fakeUsers` (observing upgrades through its `updated[id]` map):
      (a) seed a legacy **plain-bcrypt** row — construct the hash in-test with
      `bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)`, not
      `password.Hash` (which now emits `$wp$`) — log in with the correct password,
      and assert `users.updated[id]` is set and `strings.HasPrefix(newHash,
      "$wp$")` (R4.1); (b) seed a **phpass** row (the existing
      `$P$6Salt.AbCUzpPVIK9ZXMm5j8H0Tczx.` / `"admin"` vector) → correct login →
      assert the upgrade wrote a `$wp$` hash (R4.2); (c) seed a **`$wp$`** row
      (`wpGoldenHash`/`wpGoldenLogin`) → correct login → assert `users.updated[id]`
      is **not** set (R4.3); (d) a **wrong password** on a seeded row → assert
      `ErrInvalidCredentials` and no `updated` entry (R4.5). Also UPDATE the
      existing `TestSessionManager_LoginRehashesPhpass`, whose
      `strings.HasPrefix(newHash, "$2")` assertion is now wrong, to expect
      `strings.HasPrefix(newHash, "$wp$")` (the rehash emits `$wp$` after Phases
      1–2, R4.2). Leave `TestSessionManager_LoginWPHashNoRehash` as-is (it already
      asserts no rehash of a `$wp$` row, R4.3).
      Files: `internal/auth/session_test.go` (and/or a new
      `internal/auth/*_test.go`)
      Verify: `go test ./internal/auth` — new and updated assertions pass; the full
      session suite is green. _(Req 4.1, 4.2, 4.3, 4.5, 10.4; D2)_

## Phase 4 — Final gate

- [ ] 4.1 Verify the no-new-surface invariants and run the full gate suite.
      Confirm this milestone added no new environment variable, config field, or
      feature flag gating `$wp$` issuance (D4/R8) — grep the diff for any new
      `GRIMOIRE_*`/config-struct field; confirm `internal/auth/fasthash.go`
      (`HashFast`/`VerifyFast`, the `$generic$` Application Password path) is
      untouched (R9.1, 9.2); confirm `internal/content/userservice.go`
      (`provision`:76, `SetPassword`:109) and `internal/auth/session.go` (`Login`
      rehash:99, `verifyPassword`, the `$2a$` `dummyHash` constant) have **no**
      production-source change — their only behavior change is inherited through
      `password.Hash`/`NeedsRehash` (R2.2, 2.3, 2.4, Finding 6). Then run
      `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...` and
      fix anything red. Confirm no test added by this milestone needs a database,
      network, or live WordPress beyond the existing session harness, and that
      `go test ./...` requires no new `GRIMOIRE_TEST_*` env var and adds no new Go
      dependency (R10.6, 10.7).
      Files: (verification only) `internal/auth/fasthash.go`,
      `internal/content/userservice.go`, `internal/auth/session.go`
      Verify: all four gate commands green; no new config field/env var/dependency
      introduced; `fasthash.go` and the named production files unchanged except
      through inherited `password.Hash`/`NeedsRehash` behavior. _(Req 2.2, 2.3,
      2.4, 8.1, 8.2, 9.1, 9.2, 10.6, 10.7; D4)_

## Notes

- Each task references the exact Requirement/AC and Inherited decision (D1–D6) it
  satisfies, for traceability back to `requirements.md` and `design.md`.
- Checkpoints are the per-phase green gate: `gofmt`/`vet`/`build`/`test` after
  every phase, matching M10.B/M10.C/M10.D's close-out discipline (D6/R10.7).
- The TDD spine is explicit: every behavior-adding sub-task (1.2, 2.2) is preceded
  by the sub-task that writes its failing test (1.1, 2.1). Phase 3 is test-only —
  it asserts already-wired behavior made correct by Phases 1–2 — so it carries no
  separate impl task and depends on both prior phases landing.
- No sub-task is marked optional (`*`): the issuance round-trip, the flipped
  `NeedsRehash` table, and the login-upgrade integration test are this milestone's
  done-definition (R10.1–10.4), not nice-to-haves — so each is a core task. This
  matches M10.B/M10.C, whose tests were likewise unmarked.
- No property-based tests: this milestone is a **fixed issuer** (`HashWP` has no
  input-varying contract beyond "prefix is `$wp$` and it round-trips", already an
  example assertion) and a **fixed-table classifier** (`NeedsRehash` is a
  four-branch upgrade policy enumerated exhaustively by the `TestNeedsRehash`
  table). There is no universal "for all inputs X, P(X)" the example tables don't
  already pin — the input space that matters is the small set of hash-format
  classes, each tested by one representative, matching the example-test posture of
  M10.B/M10.C (design "Testing strategy").

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2"] },
    { "id": 2, "tasks": ["2.1"] },
    { "id": 3, "tasks": ["2.2"] },
    { "id": 4, "tasks": ["3.1"] },
    { "id": 5, "tasks": ["4.1"] }
  ]
}
```
