# Requirements Document

M10.G — `$wp$` as grimoire's issued password format

## Introduction

Grimoire already *verifies* every WordPress password format a shared or imported
database can hold. `password.Verify` (`internal/auth/password/password.go:52`)
routes WordPress 6.8 wrapped-bcrypt (`$wp$`, `internal/auth/password/wp.go:43`
`wpVerify`), phpass portable hashes (`$P$`/`$H$`), and PHP-written bcrypt
(`$2y$`, normalized to `$2a$` by `normalizeBcrypt` at
`internal/auth/password/password.go:140`), and each legacy path retries against
the `addslashes()`-escaped form of the password (`wpMagicQuotesSlash`,
`internal/auth/password/password.go:85`) to cover WordPress's historical
magic-quotes quirk. The login path is likewise already wired to upgrade on a
successful verify: `SessionManager.Login` calls
`if password.NeedsRehash(u.Pass) { if nh, herr := password.Hash(pw); herr == nil
{ _ = m.Users.UpdatePass(ctx, u.ID, nh) } }`
(`internal/auth/session.go:98`).

The gap this milestone closes is **issuance**, not verification. `password.Hash`
calls `bcrypt.GenerateFromPassword([]byte(password), DefaultCost)` directly
(`internal/auth/password/password.go:33`), so every hash grimoire *writes* is
plain bcrypt (`$2a$`), not WordPress 6.8's `$wp$`. On a database shared with a
live WordPress 6.8 install — the configuration `configs/grimoire.podman.yaml`
and spec `plans/02.1-wp-hash-real-db` both target — a password changed through
grimoire is a bare `$2a$` hash, which a WordPress 6.8 `wp_check_password`
rejects, so the change does not round-trip back to WordPress. A second gap is
the upgrade policy: `NeedsRehash` today returns `false` for a plain bcrypt hash
at `DefaultCost` (`internal/auth/password/password.go:125`), so existing bcrypt
rows are never migrated to `$wp$` even though the login path is standing ready
to do so.

This milestone lands in M10 because 10.C (`plans/10.2-rest-user-write`) enabled
REST user *writes* including password fields — the first point where grimoire
writes a password hash over a public API — so the issued format must become
WordPress-faithful before that surface is relied on for a shared database.
10.G is folded in from M2.1's deferral (recorded under "Open decisions" in
`plans/README.md` with "Target: a later auth milestone"), traces to roadmap
group **10.G** (`plans/wordpress-core-parity-roadmap/tasks.md:867`), and is the
**final milestone of roadmap group 10** (10.A–10.F already shipped).

## Glossary

- **`$wp$` (WordPress 6.8 wrapped-bcrypt)** — the password format WordPress 6.8+
  stores and the format this milestone makes grimoire issue:
  `"$wp$" + bcrypt(wpPreHash(password))` at `DefaultCost`. Verification of this
  format already exists (`wpVerify`, `internal/auth/password/wp.go:43`); the
  embedded value after the literal `$wp` is a standard `$2y$`/`$2a$` bcrypt hash.
- **Pre-hash (`wpPreHash`)** — `base64(hmac_sha384(password, "wp-sha384"))`
  (`internal/auth/password/wp.go:30`), using constants `wpHMACKey = "wp-sha384"`
  and `wpPrefix = "$wp$"` (`internal/auth/password/wp.go:22`). It domain-separates
  the input and bounds its length (see Pre-hash length safety, D5) before bcrypt.
- **`HashWP`** — the new exported issuer this milestone adds,
  `HashWP(password string) (string, error)`, returning
  `"$wp$" + bcrypt(wpPreHash(password))`. It reproduces WordPress 6.8's
  `wp_hash_password`.
- **The issued format** — the single hash scheme grimoire writes for new and
  changed passwords. After this milestone that is `$wp$`, unconditionally.
- **Legacy formats** — hash schemes grimoire *verifies* but no longer *issues*:
  bcrypt (`$2a$`/`$2b$`/`$2y$`, recognized by `isBcrypt`,
  `internal/auth/password/password.go:133`) and phpass portable hashes
  (`$P$`/`$H$`, via `phpass.Identify`). These remain verify-only and are upgraded
  to `$wp$` on next successful login.
- **Rehash-on-login upgrade** — the already-wired migration in
  `SessionManager.Login` (`internal/auth/session.go:98`): after a correct-password
  verify, if `NeedsRehash(u.Pass)` is true, the row is re-stored with a fresh
  `password.Hash(pw)`. This milestone's change to `Hash` (emit `$wp$`) and to
  `NeedsRehash` (flag plain bcrypt) is what makes that path migrate legacy rows
  to `$wp$`.
- **`$generic$` / Application Passwords (OUT of scope)** — `auth.HashFast`
  (`internal/auth/fasthash.go:28`) reproduces WordPress 6.8's `wp_fast_hash()`
  (keyed BLAKE2b, `$generic$` prefix) for high-entropy Application Password
  secrets, never user-chosen login passwords. It is unrelated to user-login
  hashing and is unchanged by this milestone.

## Inherited decisions

These are fixed inputs (D1–D6). They are not re-opened by this document.

| ID | Decision | Consequence |
|---|---|---|
| **D1** | Add `password.HashWP(password string) (string, error)` emitting `"$wp$" + bcrypt(wpPreHash(password))` at `DefaultCost`, and repoint `password.Hash` to call `HashWP`. | Every issuance site switches to `$wp$` at once with no caller change: `userservice.go:76` (create) and `:109` (password update), and `session.go:99` (login rehash). The bcrypt-only generation logic may remain available internally but is no longer the issued format. |
| **D2** | `NeedsRehash` returns TRUE for plain bcrypt even at `DefaultCost`, TRUE for phpass, TRUE for below-cost bcrypt, TRUE for unknown; FALSE only for `$wp$` (terminal). | Reverses today's `DefaultCost`-bcrypt→`false` (`internal/auth/password/password.go:125`). The already-wired login path then migrates every legacy row (bcrypt + phpass) to `$wp$` transparently on successful login; `$wp$` is verified in place and never rehashed. |
| **D3** | 10.G deliberately supersedes 10.C's "never introduce a second hash scheme": `$wp$` becomes THE single issued scheme, and bcrypt + phpass become verify-only legacy formats. | `$wp$` is not a *second* scheme added alongside bcrypt — it *replaces* bcrypt as the one issued scheme. 10.C's wording (`plans/10.2-rest-user-write`) is reconciled here and in Traceability. |
| **D4** | No new config flag; `$wp$` issuance is unconditional. | Grimoire always issues `$wp$`, matching the WordPress 6.8 default. The goal is a shared-DB round-trip: a password changed in grimoire verifies in a live WordPress 6.8 install, and vice versa. |
| **D5** | Pre-hash length safety: `wpPreHash` yields base64 of a 48-byte HMAC-SHA384 = 64 bytes, within bcrypt's 72-byte input limit. | No silent truncation. This is WHY WordPress wraps the password with the HMAC pre-hash: passwords longer than 72 bytes would otherwise be truncated by bare bcrypt (documented at `internal/auth/password/wp.go:12`). |
| **D6** | Tests cover issuance round-trip + upgrade policy; pure Go, no new dependency; toolchain green. | Reuse the existing `wpGolden`/`wpGoldenPlain` vector (`internal/auth/password/password_test.go`). No SQLite beyond the existing session test harness, no new env var or dependency; `gofmt`, `go vet`, `go build ./...`, `go test ./...` green at the end of every task. |

## Requirements

### Requirement 1: `HashWP` issues a correct, round-tripping `$wp$` hash

**User Story:** As a grimoire operator sharing a database with WordPress 6.8, I
want issued password hashes to be WordPress 6.8 `$wp$` hashes, so that a password
set in grimoire is accepted by a live WordPress install.

#### Acceptance Criteria

1. THE Password_Package SHALL expose `HashWP(password string) (string, error)`
   returning `"$wp$"` concatenated with a `DefaultCost` bcrypt hash of
   `wpPreHash(password)`.
2. WHEN `HashWP` is given any password, THE Password_Package SHALL return a hash
   whose prefix is the literal `"$wp$"`.
3. WHEN a password is hashed by `HashWP`, THE Password_Package SHALL cause
   `Verify(password, hashWPoutput)` to return `(true, nil)` and
   `Verify(wrongPassword, hashWPoutput)` to return `(false, nil)`.
4. THE `$wp$` hash emitted by `HashWP` SHALL be byte-structurally equivalent to
   WordPress 6.8 `wp_hash_password` output such that a WordPress 6.8
   `wp_check_password(password, hashWPoutput)` returns true for the correct
   password (shared-DB round-trip, D4).
5. IF `bcrypt.GenerateFromPassword` returns an error, THEN THE Password_Package
   SHALL return that error from `HashWP` and an empty hash string.

### Requirement 2: `password.Hash` issues `$wp$` with no caller change

**User Story:** As a maintainer, I want the existing issuance sites to emit
`$wp$` without edits, so that create, password-update, and login-rehash all
produce the WordPress-faithful format from one change.

#### Acceptance Criteria

1. THE Password_Package SHALL implement `Hash(password string) (string, error)`
   by delegating to `HashWP`, so `Hash` returns a `"$wp$"`-prefixed hash.
2. WHEN `content.UserService` creates a user (`userservice.go:76`), THE
   UserService SHALL store a `$wp$` hash without any change to its call of
   `password.Hash`.
3. WHEN `content.UserService` updates a password (`userservice.go:109`), THE
   UserService SHALL store a `$wp$` hash without any change to its call of
   `password.Hash`.
4. WHEN `SessionManager.Login` rehashes on successful login
   (`session.go:99`), THE SessionManager SHALL store a `$wp$` hash without any
   change to its call of `password.Hash`.

### Requirement 3: `NeedsRehash` upgrade policy (D2)

**User Story:** As a grimoire operator migrating from a WordPress database, I
want legacy bcrypt and phpass rows flagged for upgrade, so that they migrate to
`$wp$` on the user's next login.

#### Acceptance Criteria

1. WHEN `NeedsRehash` is given a plain bcrypt hash at `DefaultCost`, THE
   Password_Package SHALL return `true` (the DELIBERATE change from today's
   `false` at `internal/auth/password/password.go:125`).
2. WHEN `NeedsRehash` is given a bcrypt hash below `DefaultCost`, THE
   Password_Package SHALL return `true`.
3. WHEN `NeedsRehash` is given a phpass portable hash (`$P$`/`$H$`), THE
   Password_Package SHALL return `true`.
4. IF `NeedsRehash` is given a hash in an unrecognized format, THEN THE
   Password_Package SHALL return `true`.
5. WHEN `NeedsRehash` is given a `$wp$` hash, THE Password_Package SHALL return
   `false` (terminal — verified in place, never rehashed).

### Requirement 4: Login transparently upgrades legacy rows to `$wp$`

**User Story:** As a returning user whose stored hash is legacy bcrypt or phpass,
I want my hash upgraded to `$wp$` when I log in correctly, so that my account
converges on the WordPress 6.8 format without a password reset.

#### Acceptance Criteria

1. WHEN a user with a stored plain bcrypt hash logs in with the correct password,
   THE SessionManager SHALL re-store that user's hash as a `$wp$` hash via
   `UpdatePass`.
2. WHEN a user with a stored phpass hash logs in with the correct password, THE
   SessionManager SHALL re-store that user's hash as a `$wp$` hash via
   `UpdatePass`.
3. WHEN a user whose stored hash is already `$wp$` logs in with the correct
   password, THE SessionManager SHALL NOT re-store the hash (because
   `NeedsRehash` returns `false` for `$wp$`).
4. IF a correct-password login's rehash `UpdatePass` fails, THEN THE
   SessionManager SHALL still complete the login successfully (the rehash is best
   effort, `session.go:98`).
5. IF a login's password is incorrect, THEN THE SessionManager SHALL NOT re-store
   or upgrade the stored hash.

### Requirement 5: Verification behavior is unchanged (no regression)

**User Story:** As a grimoire operator with an imported WordPress database, I want
every legacy hash to keep verifying exactly as before, so that this issuance
change introduces no login regression.

#### Acceptance Criteria

1. WHEN `Verify` is given a `$wp$` hash and the correct password, THE
   Password_Package SHALL return `(true, nil)`, unchanged from current behavior.
2. WHEN `Verify` is given a phpass hash and the correct password, THE
   Password_Package SHALL return `(true, nil)`, unchanged from current behavior.
3. WHEN `Verify` is given a `$2y$` bcrypt hash and the correct password, THE
   Password_Package SHALL return `(true, nil)` via `normalizeBcrypt`, unchanged
   from current behavior.
4. WHEN `Verify` is given a legacy `$wp$` or phpass hash whose real password
   contains a quote, backslash, or NUL byte, THE Password_Package SHALL still
   match via the `wpMagicQuotesSlash` fallback, unchanged from current behavior.
5. IF `Verify` is given an unrecognized hash format, THEN THE Password_Package
   SHALL return `ErrUnknownFormat`, unchanged from current behavior.

### Requirement 6: Single-issued-scheme reconciliation of 10.C (D3)

**User Story:** As a maintainer reading the specs, I want 10.C's "never a second
hash scheme" reconciled explicitly, so that the deliberate change of issued
scheme is documented rather than implied.

#### Acceptance Criteria

1. THE Password_Package SHALL issue exactly one hash scheme (`$wp$`) for all new
   and changed user-login passwords.
2. THE Password_Package SHALL treat bcrypt (`$2a$`/`$2b$`/`$2y$`) and phpass
   (`$P$`/`$H$`) as verify-only legacy formats, not issued formats.
3. THE 10.G specification SHALL record that it supersedes 10.C's reuse-M2-bcrypt
   / "never introduce a second hash scheme" wording, because `$wp$` replaces
   bcrypt as the single issued scheme rather than being added alongside it.

### Requirement 7: Pre-hash length safety (D5)

**User Story:** As a user with a very long passphrase, I want my full password to
contribute to the hash, so that it is not silently truncated to 72 bytes.

#### Acceptance Criteria

1. THE Password_Package SHALL bcrypt-hash the output of `wpPreHash` (base64 of a
   48-byte HMAC-SHA384 = 64 bytes), which is within bcrypt's 72-byte input limit.
2. WHEN a password longer than 72 bytes is hashed by `HashWP`, THE
   Password_Package SHALL incorporate the entire password into the HMAC pre-hash
   with no truncation of the password content.
3. THE 10.G specification SHALL document that the HMAC pre-hash exists precisely
   so passwords longer than 72 bytes are not truncated by bare bcrypt
   (`internal/auth/password/wp.go:12`).

### Requirement 8: No new config flag (D4)

**User Story:** As a grimoire operator, I want `$wp$` issuance to be the default
with no setup, so that a shared-WordPress-6.8 database works out of the box.

#### Acceptance Criteria

1. THE Password_Package SHALL issue `$wp$` unconditionally, with no opt-in or
   opt-out configuration setting.
2. THE 10.G milestone SHALL NOT add any new environment variable, config field,
   or feature flag to gate `$wp$` issuance.

### Requirement 9: Application Password `$generic$` hashing is out of scope

**User Story:** As a maintainer, I want the Application Password path left
untouched, so that this login-password milestone does not change secret hashing.

#### Acceptance Criteria

1. THE 10.G milestone SHALL NOT change `auth.HashFast`, `auth.VerifyFast`, or the
   `$generic$` format (`internal/auth/fasthash.go`).
2. THE 10.G milestone SHALL change only the user-login password issuance path and
   the `NeedsRehash` upgrade trigger, not the Application Password hashing path.

### Requirement 10: Test coverage, pure Go, toolchain green (D6)

**User Story:** As a maintainer, I want the issuance change and upgrade policy
asserted by pure-Go tests, so that the round-trip and migration behavior are
regression-protected.

#### Acceptance Criteria

1. THE Test_Suite SHALL assert that `Hash` now emits a `"$wp$"`-prefixed hash
   that `Verify` accepts for the correct password and rejects for a wrong one.
2. THE Test_Suite SHALL assert that `NeedsRehash` of a fresh `Hash` output is
   `false`.
3. THE Test_Suite SHALL assert that a `DefaultCost` plain bcrypt hash now reports
   `NeedsRehash == true` (the D2 policy change), while a `$wp$` hash reports
   `false` and phpass / below-cost bcrypt / unknown report `true`.
4. THE Test_Suite SHALL include a login-upgrade integration test over
   `SessionManager.Login` that migrates a legacy bcrypt (and/or phpass) row to a
   `$wp$` hash after a correct-password login, and asserts a `$wp$` row is not
   rehashed.
5. THE Test_Suite SHALL reuse the existing `wpGolden`/`wpGoldenPlain` vector
   (`internal/auth/password/password_test.go`) and SHALL NOT embed real user
   hashes.
6. THE 10.G milestone SHALL add no new Go dependency and no new environment
   variable, and SHALL require no SQLite beyond the existing session test harness.
7. THE 10.G milestone SHALL keep `gofmt`, `go vet`, `go build ./...`, and
   `go test ./...` green at the end of every task.

## Out of scope

- **Application Password `$generic$` issuance.** `auth.HashFast`/`VerifyFast`
  (`internal/auth/fasthash.go`) and the `$generic$` format are unchanged; this
  milestone is user-login passwords only.
- **Any config flag.** No opt-in/opt-out setting, env var, or feature flag for
  `$wp$` issuance (D4/R8).
- **Changing verification behavior.** Verification of `$wp$`, phpass, `$2y$`, and
  the magic-quotes fallback is unchanged; this milestone changes only issuance
  and the `NeedsRehash` upgrade trigger (R5).
- **MySQL/Postgres-gated tests beyond the existing harness.** Tests stay pure Go
  and use only the existing session test harness (D6/R10).
- **The already-shipped M10 milestones.** 10.A (write-boundary content policy),
  10.B (REST media write, PR #54), 10.C (REST user write,
  `plans/10.2-rest-user-write`), 10.D (`content.rendered`), 10.E (M10 test
  coverage), and 10.F (menu editing) are already shipped and are not revisited.

## Traceability

- Refines roadmap group **10.G** (`plans/wordpress-core-parity-roadmap/tasks.md:867`),
  "`$wp$` as grimoire's issued password format", the final milestone of group 10.
- Folded in from **M2.1's deferral**, recorded under "Open decisions" in
  `plans/README.md` with "Target: a later auth milestone" and no milestone
  attached; also the subject of `plans/02.1-wp-hash-real-db` (shared-DB target).
- **Supersedes 10.C's hash-scheme wording** (`plans/10.2-rest-user-write`): where
  10.C said to reuse M2's bcrypt and "never introduce a second hash scheme", 10.G
  makes `$wp$` the single issued scheme (D3/R6) and reconciles that wording here.
