# Requirements — M2.1: WordPress 6.8 `$wp$` Hashes & Real-DB Validation

## Introduction

M2 gave grimoire password authentication against WordPress phpass (`$P$`/`$H$`)
and bcrypt (`$2y$`/`$2a$`) hashes, transparently upgrading legacy phpass to bcrypt
on login. M2 **explicitly deferred** WordPress 6.8+'s new `$wp$`-prefixed hashes.

Restoring a **real** WordPress 7.1 database (accuweaver.com, 118,958 users) into
local MariaDB and pointing grimoire at it surfaced the cost of that deferral. A
hash-format census of the real `{prefix}users` table:

| Format   | Users  | grimoire M2 |
|----------|-------:|-------------|
| `$wp$…`  | 99,336 | ❌ cannot verify |
| `$P$B…`  | 19,622 | ✅ verifies (+ rehashes → bcrypt) |
| bcrypt   |      — | ✅ verifies |

~84% of real users cannot authenticate today. **M2.1 closes this gap** by adding
`$wp$` verification to grimoire's auth layer, wiring it into the existing login
path, and proving the result against the real database behind an env gate.

M2.1 also hardens one smaller real-data finding: the `{prefix}capabilities`
serialized meta in the wild uses **both** `b:1` (boolean true) and `s:1:"1"`
(string `"1"`) to mean "has role". grimoire must treat a truthy scalar identically
to boolean true, exactly as WordPress does.

Out of scope for M2.1 (unchanged from M2): JSON CRUD endpoints, the admin SPA,
email verification / password-reset flows, comments, media, plugins, the REST API,
and any **write** against the real demo database (it is validated **read-only**).

## The `$wp$` format

WordPress 6.8 wraps bcrypt to remove bcrypt's 72-byte input-truncation problem.
`wp_hash_password($pw)` returns the literal prefix `$wp$` immediately followed by a
standard bcrypt hash of a **pre-hash** of the password:

```
$wp$  +  bcrypt( base64_encode( <pre-hash of password> ), cost = 12 )
```

so a stored value looks like `$wp$2y$12$…` — i.e. `$wp` then a normal `$2y$12$…`
bcrypt string. The bcrypt **input** is the pre-hash, not the raw password.

> **Pre-hash correction (authoritative).** The M2.1 kickoff described the pre-hash
> as `base64_encode(sha384_raw(password))` (plain SHA-384). The real WordPress 6.8
> source (`wp-includes/pluggable.php`, `wp_hash_password` / `wp_check_password`)
> uses **HMAC-SHA384** with the domain-separation key `"wp-sha384"`:
> `base64_encode( hash_hmac('sha384', $password, 'wp-sha384', true) )`.
> This was confirmed by reading the WP source, reproducing the algorithm in Go, and
> verifying the golden vector below against a live `wp_check_password`. The golden
> vector itself is genuine and unchanged.

### Golden test vector

- plaintext: `grimoire-test-123`
- stored hash: `$wp$2y$12$iWN5xRwDE7i9R5jVJvCyqOxS1CNnwUggQaF8O2W9Bg8TuXQz.ngrS`

## Requirements

### Requirement 1 — Verify WordPress 6.8 `$wp$` password hashes

**User Story:** As a user migrated from WordPress 6.8+, I want grimoire to accept
my existing `$wp$` password hash, so that I can log in without resetting my
password.

#### Acceptance Criteria
1. WHEN a stored hash begins with the literal prefix `$wp$`, THEN the system SHALL recognize it as a WordPress 6.8 wrapped-bcrypt hash AND SHALL NOT report it as an unknown/unsupported format.
2. WHEN verifying a password against a `$wp$` hash, THE system SHALL compute the pre-hash `base64_encode( HMAC_SHA384( password, key = "wp-sha384" ) )` AND SHALL bcrypt-compare that pre-hash against the embedded bcrypt hash (the stored value with the leading `$wp` stripped, leaving a standard `$2y$…` bcrypt string).
3. WHEN the embedded bcrypt hash uses the `$2y$` identifier, THE system SHALL treat it as equivalent to `$2a$` for comparison (the algorithms are identical), reusing M2's existing normalization.
4. WHEN the correct password is verified against the golden vector, THE system SHALL return success; WHEN any other password is verified against it, THE system SHALL return failure.
5. WHEN a `$wp$` value is malformed (empty embedded bcrypt, truncated, non-bcrypt remainder), THE system SHALL return failure WITHOUT panicking AND WITHOUT reporting success.
6. THE `$wp$` verification SHALL be constant-time with respect to the password (bcrypt comparison) AND SHALL preserve M2's no-user-enumeration property (the login path's dummy-hash compare for missing users is unchanged).

### Requirement 2 — Do not rehash `$wp$` on login

**User Story:** As an operator, I want grimoire to leave strong `$wp$` hashes in
place, so that logins do not trigger unnecessary password-column writes and the
stored format stays WordPress-round-trip compatible.

#### Acceptance Criteria
1. WHEN a user with a `$wp$` hash logs in successfully, THE system SHALL report the hash as NOT needing a rehash AND SHALL NOT write a new `user_pass` value.
2. WHEN a user with a phpass `$P$`/`$H$` hash logs in successfully, THE system SHALL continue to rehash to grimoire's preferred format (bcrypt), unchanged from M2.
3. THE preferred hashing format for **new** passwords SHALL remain bcrypt for M2.1; adopting `$wp$` as grimoire's own new-hash format is recorded as a deferred open design decision and SHALL NOT be silently enabled in this milestone.

### Requirement 3 — Capabilities scalar truthiness (`s:1:"1"` == `b:1`)

**User Story:** As a developer pointing grimoire at a real WordPress database, I
want role parsing to accept the string form of "true", so that users whose
`{prefix}capabilities` meta stores `s:1:"1"` are granted their role exactly as
those storing `b:1`.

#### Acceptance Criteria
1. WHEN `{prefix}capabilities` deserializes to an associative array whose value for a role key is boolean true (`b:1`), THE system SHALL treat that role as granted.
2. WHEN the value for a role key is the string `"1"` (`s:1:"1"`) or any other truthy PHP scalar, THE system SHALL treat that role as granted, identically to boolean true.
3. WHEN the value for a role key is a falsy scalar (`b:0`, the string `"0"`, or empty string), THE system SHALL treat that role as NOT granted.

### Requirement 4 — Env-gated real-WordPress-DB validation

**User Story:** As a maintainer, I want an opt-in test that exercises grimoire
against the real restored WordPress database, so that `$wp$`/phpass support is
proven on real data while CI without the database still passes.

#### Acceptance Criteria
1. THE real-DB validation test SHALL be skipped unless the environment variable `GRIMOIRE_TEST_WP_DSN` is set, mirroring M2's `GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN` gating, so the default `go test ./...` stays green without the database.
2. WHEN the gate is set, THE test SHALL open grimoire's storage against the DSN with table prefix from `GRIMOIRE_TEST_WP_PREFIX` (default `accuweaver`) WITHOUT running migrations, AND SHALL read recent posts through grimoire's own read path, asserting at least one post is returned.
3. WHEN the gate is set, THE test SHALL locate a real user whose stored `user_pass` begins with `$P$` and another whose stored `user_pass` begins with `$wp$` (via a read-only query), load each through grimoire's user read path, AND assert grimoire **recognizes** each format — i.e. verifying a deliberately wrong password returns `(false, nil)` and NOT an unknown-format error.
4. WHEN `GRIMOIRE_TEST_WP_LOGIN` and `GRIMOIRE_TEST_WP_PASSWORD` are additionally set, THE test SHALL verify that real user's actual password through grimoire's verifier (read-only, without invoking the rehash/write login path) and assert success.
5. THE test SHALL NOT modify the real database (no migrations, no writes) AND SHALL NOT hardcode any real user's password hash into the repository; real hashes SHALL be read from the live database at test time only when the gate is on.
6. THE always-on unit tests SHALL assert `$wp$` support using only the synthetic `grimoire-test-123` golden vector, never real user data.

### Requirement 5 — Quality gate & compatibility

**User Story:** As a maintainer, I want M2.1 to uphold M1/M2's engineering
guarantees, so that the change is safe and portable.

#### Acceptance Criteria
1. THE change SHALL keep `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` succeeding, and `go test -count=1 ./...` green (SQLite unconditional; MySQL/Postgres and the real-DB test gated on their env vars).
2. THE `internal/auth/password` and `internal/auth` packages SHALL remain free of database-driver imports.
3. THE work SHALL follow strict TDD: a failing test precedes each behavior change.
4. THE login flow's public behavior for phpass and bcrypt users SHALL be unchanged (regression-locked by test).
