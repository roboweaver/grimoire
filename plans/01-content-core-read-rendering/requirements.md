# Requirements — M1: Content Core & Public Read Rendering

## Introduction

This milestone delivers grimoire's foundational slice: a pure-Go service that
connects to a **WordPress-compatible database** on any of three supported vendors
(MySQL/MariaDB, PostgreSQL, SQLite), reads core content (posts, pages,
taxonomies, options), and renders a **public, SEO-friendly website** using a
theme of Go `html/template` files. No PHP runtime is involved, and the database
vendor is selected purely through configuration.

The defining success criterion: grimoire can be pointed at an **existing
WordPress MySQL database** and correctly render its posts and pages read-only,
and the *same* binary can serve the *same* content from a PostgreSQL or SQLite
copy of that data by changing only configuration.

Out of scope for M1: authentication, the admin UI (React Spectrum), writing
content through the app, comments, media handling, plugins, and the REST API.

## Requirements

### Requirement 1 — Database vendor selection via configuration

**User Story:** As an operator, I want to choose the database vendor and
connection string in configuration, so that I can run grimoire on my organization's
preferred database without recompiling.

#### Acceptance Criteria
1. WHEN grimoire starts with a config specifying `database.vendor` of `mysql`, `postgres`, or `sqlite`, THEN the system SHALL initialize the matching storage adapter and establish a connection.
2. IF `database.vendor` is missing or is not a supported value, THEN the system SHALL refuse to start AND SHALL emit an error identifying the invalid value and the supported set.
3. IF the database connection cannot be established at startup, THEN the system SHALL exit non-zero AND SHALL log the vendor and a redacted DSN (credentials removed).
4. WHEN a `database.table_prefix` is configured, THEN the system SHALL apply that prefix to all WordPress table names (defaulting to `wp_`).

### Requirement 2 — WordPress-compatible schema and per-dialect migrations

**User Story:** As a developer, I want grimoire's schema to mirror the WordPress
core tables on each supported vendor, so that grimoire is compatible with existing
WordPress data and portable across vendors.

#### Acceptance Criteria
1. THE system SHALL define, for M1, the tables `posts`, `postmeta`, `options`, `terms`, `term_taxonomy`, `term_relationships`, and `users` (each with the configured prefix) matching WordPress column names and semantics.
2. WHEN migrations are applied for a given vendor, THEN the system SHALL create these tables using types appropriate to that dialect (e.g. `LONGTEXT` on MySQL mapped to `TEXT` on PostgreSQL/SQLite; `BIGINT UNSIGNED` mapped to the dialect's equivalent).
3. WHEN migrations are applied more than once, THEN the system SHALL be idempotent AND SHALL NOT error on already-applied migrations.
4. THE system SHALL maintain migrations as separate, vendor-specific sets so that dialect differences never leak into shared code.

### Requirement 3 — Repository interfaces and swappable vendor adapters

**User Story:** As a maintainer, I want all content access to go through
domain-defined repository interfaces, so that adding or switching a database
vendor requires only a new adapter and no change to business logic.

#### Acceptance Criteria
1. THE domain package SHALL define repository interfaces for posts, terms/taxonomies, and options, and SHALL NOT import any database driver.
2. THE system SHALL provide adapter implementations of those interfaces for `mysql`, `postgres`, and `sqlite`.
3. WHEN a new vendor adapter is added that satisfies the repository interfaces, THEN the system SHALL support that vendor WITHOUT changes to the `content`, `web`, or `render` packages.
4. THE system SHALL select and construct the active adapter set from configuration at startup via a single factory.

### Requirement 4 — Read core content (posts and pages)

**User Story:** As a site visitor, I want to see published posts and pages, so
that I can read the site's content.

#### Acceptance Criteria
1. WHEN content is requested, THEN the system SHALL return only rows whose `post_status` is `publish`.
2. WHEN listing content for the home page, THEN the system SHALL return rows of `post_type` `post`, most-recent first, paginated.
3. WHEN a single item is requested by slug, THEN the system SHALL resolve it by `post_name` and `post_type` (`post` or `page`).
4. IF no published item matches the requested slug, THEN the system SHALL cause the web layer to respond `404 Not Found`.
5. THE system SHALL expose each item's title, slug, content, excerpt, publish date, and author identifier to the render layer.

### Requirement 5 — Taxonomies (categories and tags)

**User Story:** As a site visitor, I want to browse posts by category, so that I
can find related content.

#### Acceptance Criteria
1. THE system SHALL read terms via the `terms` → `term_taxonomy` → `term_relationships` join, matching WordPress semantics.
2. WHEN a category archive is requested by category slug, THEN the system SHALL return published posts associated with that term, most-recent first, paginated.
3. IF a requested category slug does not exist, THEN the system SHALL cause the web layer to respond `404 Not Found`.
4. THE system SHALL expose each term's name, slug, and taxonomy to the render layer.

### Requirement 6 — Site options

**User Story:** As a site owner, I want site-wide settings (like the site title)
read from the options table, so that the rendered site reflects existing
WordPress configuration.

#### Acceptance Criteria
1. THE system SHALL read named values from the `options` table.
2. WHEN rendering any public page, THEN the system SHALL make at least the site title (`blogname`) and tagline (`blogdescription`) available to templates.
3. IF a requested option is absent, THEN the system SHALL return an empty value AND SHALL NOT error.

### Requirement 7 — Public routing and rendering

**User Story:** As a site visitor, I want clean URLs for the home page, single
posts/pages, and category archives, so that I can navigate and share links.

#### Acceptance Criteria
1. THE system SHALL route `GET /` to a paginated list of recent posts (home).
2. THE system SHALL route `GET /{slug}` to a single post or page resolved by slug.
3. THE system SHALL route `GET /category/{slug}` to a category archive.
4. WHEN rendering a page, THEN the system SHALL select a template following a subset of the WordPress template hierarchy (`single`, `page`, `archive`/`category`, `index` as fallback).
5. WHEN a template renders successfully, THEN the system SHALL respond `200 OK` with `Content-Type: text/html; charset=utf-8`.
6. THE rendered HTML SHALL be complete server-side (no client-side JavaScript required to display content).

### Requirement 8 — Theme loading

**User Story:** As a site builder, I want a theme to be a folder of templates and
assets, so that I can customize presentation without touching Go code.

#### Acceptance Criteria
1. WHEN the configured theme directory exists, THEN the system SHALL load its templates at startup.
2. IF a required base template is missing from the active theme, THEN the system SHALL refuse to start AND SHALL name the missing template.
3. THE system SHALL ship a `default` theme sufficient to render home, single, and category views.

### Requirement 9 — Error handling and observability

**User Story:** As an operator, I want consistent errors and structured logs, so
that I can diagnose problems across different database vendors.

#### Acceptance Criteria
1. THE system SHALL normalize vendor-specific "no rows" results into a single domain not-found error.
2. WHEN a handler receives a domain not-found error, THEN the system SHALL respond `404 Not Found`.
3. WHEN a handler receives any other error, THEN the system SHALL respond `500 Internal Server Error` AND SHALL log the error with request context using `log/slog`.
4. THE system SHALL NOT expose internal error details or SQL in HTTP responses.

### Requirement 10 — Operational CLI

**User Story:** As an operator, I want a CLI to apply migrations and seed sample
data, so that I can stand up grimoire against a fresh database.

#### Acceptance Criteria
1. WHEN `grimoire-cli migrate` runs with a valid config, THEN the system SHALL apply the selected vendor's migrations and report the resulting schema version.
2. WHEN `grimoire-cli seed` runs against a migrated database, THEN the system SHALL insert a small set of sample posts, a category, and required options.
3. IF a CLI command runs with an invalid or unreachable database config, THEN the system SHALL exit non-zero with a clear message.

### Requirement 11 — Cross-vendor verification

**User Story:** As a maintainer, I want automated proof that every vendor adapter
behaves identically, so that "switchable database" is guaranteed, not assumed.

#### Acceptance Criteria
1. THE system SHALL define a single repository contract test suite that is executed against each supported vendor.
2. WHEN the contract suite runs against `sqlite`, THEN it SHALL execute by default with no external services (in-memory or file-based).
3. WHEN the contract suite runs against `mysql` and `postgres`, THEN it SHALL execute against ephemeral instances (e.g. containers) AND SHALL be skippable in constrained environments via an explicit opt-out.
4. THE contract suite SHALL assert identical results for posts, taxonomy, and options access across all vendors for the same seed data.
