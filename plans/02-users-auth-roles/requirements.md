# Requirements — M2: Users, Authentication & Roles

## Introduction

This milestone gives grimoire its identity and authorization foundation. Building
on M1's read-only content core, M2 adds **users**, **password authentication**,
**server-side sessions**, WordPress-compatible **roles and capabilities**, and a
**Go-internal content write API** guarded by capability checks. It also ships a
minimal, server-rendered **login/logout UI** in the default theme.

The defining success criterion: grimoire can be pointed at an **existing WordPress
database**, authenticate a user against that user's stored WordPress password hash
(phpass `$P$`/`$H$` or bcrypt), establish a secure session, and enforce that user's
WordPress role (administrator/editor/author/contributor/subscriber) when creating,
updating, or deleting content — and the *same* binary does this identically on
MySQL, PostgreSQL, or SQLite by changing only configuration. New passwords are
written as bcrypt; a legacy phpass hash is transparently upgraded to bcrypt on the
user's next successful login.

This is the authorization substrate the M3 Adobe React Spectrum admin SPA will
consume. Out of scope for M2: JSON CRUD HTTP endpoints (M3), the admin SPA (M3),
WordPress 6.8 `$wp$`-prehashed bcrypt hashes, email verification and
password-reset flows, comments, media, plugins, and the REST API.

## Requirements

### Requirement 1 — WordPress-compatible user & metadata schema

**User Story:** As a developer, I want grimoire's user schema to mirror the
WordPress `wp_users` and `wp_usermeta` tables on each vendor, so that grimoire is
compatible with existing WordPress accounts and portable across vendors.

#### Acceptance Criteria
1. THE system SHALL extend the M1 `{prefix}users` table to the full WordPress column set: `user_pass`, `user_email`, `user_url`, `user_registered`, `user_activation_key`, and `user_status` (in addition to the existing `ID`, `user_login`, `user_nicename`, `display_name`).
2. THE system SHALL define a `{prefix}usermeta` table matching WordPress column names and semantics: `umeta_id`, `user_id`, `meta_key`, `meta_value`, with indexes on `user_id` and `meta_key`.
3. THE system SHALL define a grimoire-native `{prefix}sessions` table for server-side sessions with columns `id`, `user_id`, `csrf_token`, `created`, and `expires`, indexed on `expires` and `user_id`.
4. WHEN migrations are applied for a given vendor, THEN the system SHALL create/extend these using dialect-appropriate types, maintained as vendor-specific migration sets so dialect differences never leak into shared code.
5. WHEN migrations are applied more than once, THEN the system SHALL be idempotent AND SHALL NOT error on already-applied migrations.
6. THE system SHALL store a user's roles in the usermeta key `{prefix}capabilities` as a PHP-serialized associative array (e.g. `a:1:{s:13:"administrator";b:1;}`) AND SHALL write the legacy `{prefix}user_level` meta for fidelity.

### Requirement 2 — Password hashing and WordPress hash compatibility

**User Story:** As a returning WordPress user, I want to sign in with my existing
password, so that migrating to grimoire does not force a password reset.

#### Acceptance Criteria
1. WHEN the system stores a new or changed password, THEN it SHALL hash it with bcrypt (`golang.org/x/crypto/bcrypt`) AND SHALL NEVER store or log plaintext.
2. WHEN a user logs in AND their stored hash is a WordPress phpass portable hash (`$P$` or `$H$`), THEN the system SHALL verify the password against that hash using a pure-Go implementation.
3. WHEN a user logs in AND their stored hash is a bcrypt hash (`$2a$`, `$2b$`, or `$2y$`), THEN the system SHALL verify it, normalizing the `$2y$` prefix as needed.
4. WHEN a login succeeds AND the stored hash is not bcrypt at the configured cost, THEN the system SHALL rehash the password with bcrypt AND SHALL persist the new hash (transparent upgrade).
5. WHEN comparing a candidate against a stored hash, THEN the system SHALL use a constant-time comparison AND SHALL NOT reveal, via error or timing, whether the username exists.
6. IF a stored hash is in an unsupported format (e.g. WordPress 6.8 `$wp$` prehashed bcrypt), THEN the system SHALL fail that login cleanly with the generic invalid-credentials result AND SHALL document the limitation.

### Requirement 3 — Roles and capabilities

**User Story:** As a site owner, I want grimoire to honor WordPress's default roles,
so that each account can do exactly what its role allows.

#### Acceptance Criteria
1. THE system SHALL define the five default WordPress roles — `administrator`, `editor`, `author`, `contributor`, `subscriber` — each with its standard WordPress capability set.
2. WHEN a user's `{prefix}capabilities` meta is read, THEN the system SHALL resolve it into an effective set of capabilities (role capabilities unioned with any per-user capabilities set to true).
3. THE system SHALL expose a single check, `Can(principal, capability)`, that returns whether an authenticated user holds a capability.
4. THE roles/capabilities logic SHALL live in the domain-facing auth layer and SHALL NOT import any database driver.

### Requirement 4 — Authentication and server-side sessions

**User Story:** As a user, I want to log in and stay logged in securely, so that I
can perform authorized actions without re-entering credentials on every request.

#### Acceptance Criteria
1. WHEN valid credentials are submitted, THEN the system SHALL create a session record in the `{prefix}sessions` table AND SHALL set a cookie containing an opaque, high-entropy random token.
2. THE system SHALL store only a SHA-256 hash of the session token server-side, never the raw token.
3. THE session cookie SHALL be `HttpOnly`, SHALL set `SameSite=Lax`, AND SHALL set `Secure` when the request is served over TLS.
4. WHEN an authenticated request arrives, THEN the system SHALL resolve the session token to its user and load the user's capabilities into a request-scoped principal.
5. THE system SHALL apply a 14-day rolling expiry: WHEN an authenticated session is used AND its remaining lifetime is less than half the maximum, THEN the system SHALL extend `expires`.
6. IF a session token is missing, unknown, or expired, THEN the system SHALL treat the request as unauthenticated AND SHALL NOT error.
7. WHEN a user logs out, THEN the system SHALL delete the session record AND SHALL clear the cookie.
8. THE system SHALL support session revocation, including deleting all sessions for a user (e.g. on password change) AND deleting expired sessions.

### Requirement 5 — CSRF protection

**User Story:** As a site owner, I want state-changing form submissions protected
from cross-site request forgery, so that a malicious page cannot act as a
logged-in user.

#### Acceptance Criteria
1. WHEN the login form is rendered, THEN the system SHALL embed a CSRF token AND SHALL bind it to the client via a `HttpOnly`, `SameSite=Lax` cookie (double-submit).
2. WHEN a login is submitted, THEN the system SHALL reject the request with `403 Forbidden` IF the submitted token is absent OR does not match the cookie (constant-time compare).
3. THE system SHALL store a per-session synchronizer CSRF token for authenticated state-changing requests AND SHALL expose a reusable CSRF middleware that validates it (form field or header).
4. WHEN logout is submitted by an authenticated user, THEN the system SHALL require and validate the session CSRF token.

### Requirement 6 — Internal content write API with authorization

**User Story:** As a maintainer, I want a Go-internal service API to create, update,
and delete content with role enforcement, so that the M3 admin UI has a safe,
capability-checked foundation to build on.

#### Acceptance Criteria
1. THE system SHALL provide write operations for posts and pages (create, update, delete), taxonomy terms (create, update, delete), options (set), and users (create, update, delete, list) as Go-internal services.
2. WHEN a write operation is invoked, THEN it SHALL require an authenticated principal AND SHALL enforce the corresponding WordPress capability before mutating any data.
3. THE system SHALL enforce ownership-aware capabilities for posts/pages: editing another user's content SHALL require `edit_others_posts`/`edit_others_pages`; publishing SHALL require `publish_posts`/`publish_pages`; deleting another user's content SHALL require `delete_others_posts`/`delete_others_pages`.
4. Term writes SHALL require `manage_categories`; option writes SHALL require `manage_options`; user writes SHALL require `create_users`/`edit_users`/`delete_users`/`list_users` respectively.
5. IF a principal lacks the required capability, THEN the operation SHALL return a permission error AND SHALL NOT perform the mutation.
6. WHEN a write is authorized, THEN it SHALL persist through the domain repository ports, rebinding `?`→`$N` for PostgreSQL wherever raw SQL is used, consistent with M1.
7. THE M2 write API SHALL be Go-internal only; the system SHALL NOT expose JSON CRUD HTTP endpoints in this milestone (deferred to M3).

### Requirement 7 — Login UI

**User Story:** As a user, I want a simple login page, so that I can sign in
through the browser.

#### Acceptance Criteria
1. THE system SHALL route `GET /login` to a server-rendered login form using the active theme, including the CSRF token field.
2. WHEN credentials are posted to `POST /login` and are valid, THEN the system SHALL establish a session AND SHALL redirect (`303 See Other`) to a post-login destination.
3. WHEN credentials are invalid, THEN the system SHALL re-render the login form with a single generic message ("invalid username or password") AND SHALL NOT indicate whether the username exists.
4. THE system SHALL provide logout via `POST /logout`, which clears the session and cookie and redirects to a public page.
5. THE default theme SHALL include a `login` template composing the theme base, sufficient to render the form and its error state.

### Requirement 8 — Operational CLI

**User Story:** As an operator, I want to create the first administrator and manage
sessions from the CLI, so that I can bootstrap and maintain an authenticated site.

#### Acceptance Criteria
1. WHEN `grimoire-cli createadmin` runs with a valid config and the required user details, THEN the system SHALL create a user with a bcrypt password hash AND SHALL set `{prefix}capabilities` to `administrator` (and the matching `{prefix}user_level`).
2. IF a user with the given login already exists, THEN `createadmin` SHALL fail with a clear message AND SHALL NOT overwrite the existing account unless explicitly instructed.
3. WHEN `grimoire-cli sessions gc` runs, THEN the system SHALL delete expired session records AND SHALL report the number removed.
4. IF a CLI command runs with an invalid or unreachable database config, THEN the system SHALL exit non-zero with a clear message AND SHALL NOT print secrets.

### Requirement 9 — Error handling, security & observability

**User Story:** As an operator, I want authentication failures handled consistently
and securely, so that the system is safe to expose and easy to diagnose.

#### Acceptance Criteria
1. THE system SHALL NOT expose internal error details, SQL, or password hashes in HTTP responses.
2. WHEN authentication or authorization fails, THEN the system SHALL log the event with request context using `log/slog` WITHOUT logging plaintext passwords, full hashes, or raw session tokens.
3. THE system SHALL NOT commit any secret material to the repository; authentication requires no static application secret (sessions are opaque and DB-backed).
4. WHEN a handler panics, THEN the existing recovery middleware SHALL convert it to `500` without leaking internals (unchanged from M1).
5. THE system SHALL redact DSN credentials in logs (unchanged from M1).

### Requirement 10 — Cross-vendor verification

**User Story:** As a maintainer, I want automated proof that user, session, and
content-write access behave identically on every vendor, so that "switchable
database" continues to hold for authenticated writes.

#### Acceptance Criteria
1. THE system SHALL extend the single repository contract test suite to cover the user, usermeta, and session repositories AND the content write operations.
2. WHEN the contract suite runs against `sqlite`, THEN it SHALL execute by default with no external services.
3. WHEN the contract suite runs against `mysql` and `postgres`, THEN it SHALL execute only when the vendor DSN is provided (`GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN`) AND SHALL skip otherwise.
4. THE contract suite SHALL assert identical results across all vendors for: creating and reading a user, reading/writing usermeta (including a PHP-serialized capabilities round-trip), session create/lookup/delete/expiry, and content create/update/delete.

---

## Implementation deviations (recorded during M2 build)

_This section is populated during implementation, mirroring M1. It records
decisions that refine the design while preserving the ports-and-adapters
boundaries, the swappable-vendor guarantee, and the security requirements above._

_(none yet)_
