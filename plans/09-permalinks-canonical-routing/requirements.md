# M9a — Permalinks & Canonical Routing: Requirements

## Introduction

grimoire serves single posts from one flat `/{slug}` route and ignores the
`permalink_structure` option entirely. For any site that is not using
WordPress's "plain" permalinks — which is most sites, and every site using one
of WordPress's presets — that means **every URL the site has ever published
returns 404**, while the only URL that works is one WordPress would have
redirected away from. This is tracked as
[#23](https://github.com/roboweaver/grimoire/issues/23) and is the most severe
remaining functional gap before grimoire can be pointed at a real WordPress
database.

This milestone refines groups **9.A**, **9.B** and **9.F** of
[`../wordpress-core-parity-roadmap`](../wordpress-core-parity-roadmap) into
implementable requirements: read the permalink options, serve and canonicalize
the core token set, and extend the template hierarchy to cover the archive
kinds those routes imply.

**Scope was deliberately split.** The roadmap's M9 also contains 9.C (tag, date
and author archives) and 9.D (nested category hierarchy). Those are *additive*
— they create new browsing surfaces that no existing URL depends on. Permalinks
are *corrective*: they fix URLs that are broken today. Bundling them would delay
the fix behind roughly twice the work, so 9.C/9.D move to a follow-on spec. See
"Out of scope" for the design decisions already resolved on their behalf so the
follow-on inherits them rather than relitigating.

Traces to roadmap Requirements 10, 11, 14, and to the M1 template-hierarchy
deferral now recorded as 9.F.

## Requirements

### Requirement 1 — Read the permalink option set

**User Story:** As a site owner who configured permalinks in WordPress, I want
grimoire to read that configuration from my database, so I do not have to
restate it in grimoire's own config file.

#### Acceptance Criteria

1. THE system SHALL read `permalink_structure`, `category_base` and `tag_base`
   from `{prefix}options` using the existing `content.OptionService.Get`
   accessor. No new repository method is required — the accessor is already
   generic and string-keyed.
2. WHEN `permalink_structure` is absent or empty (WordPress's "plain" setting)
   THE system SHALL serve today's flat `/{slug}` post route and
   `/category/{slug}` category route with behavior unchanged from before this
   milestone.
3. WHEN `category_base` is empty THE system SHALL use `category` as the
   category archive base segment, matching WordPress's default.
4. WHEN `category_base` is set to a non-empty value THE system SHALL use that
   value as the category archive base segment in place of `category`.
5. `tag_base` SHALL be read and exposed by the resolver in this milestone even
   though no tag route is served until the follow-on spec implements 9.C, so
   that the option-reading surface is complete and does not need revisiting.
6. THE system SHALL read these options **once at startup**, not per request.
   `OptionService` performs no caching, so a per-request read would add a
   database round trip to every page load. See `design.md` for the restart
   consequence this creates and why it is accepted.

### Requirement 2 — Resolve the core permalink token set

**User Story:** As a site visitor following a published link, I want the URL to
resolve to the post it has always resolved to.

#### Acceptance Criteria

1. THE system SHALL support the tokens `%postname%`, `%post_id%`, `%year%`,
   `%monthnum%` and `%day%`, in whatever order and combination
   `permalink_structure` specifies, for both `post` and `page` types.
2. THE system SHALL treat `%year%` as 4 digits and `%monthnum%`/`%day%` as
   exactly 2 digits, matching WordPress's zero-padded output, and SHALL NOT
   match a 1-digit month or day.
3. WHEN a request path matches the configured structure AND the extracted
   identifying token resolves to a published post THE system SHALL render that
   post with status `200`.
4. THE identifying token SHALL be `%postname%` when present, otherwise
   `%post_id%`. WHEN a structure contains neither THE system SHALL treat the
   structure as unsupported per Requirement 4, because no such structure can
   identify a unique post.
5. WHEN date tokens are present in the structure THE system SHALL verify the
   extracted date components against the resolved post's `post_date`, and
   SHALL respond `404` on mismatch rather than serving the post at a date path
   that is not its own.
6. WHEN a request path does not match the configured structure and matches no
   other registered route THE system SHALL respond `404`, unchanged from
   today's behavior.
7. Plugin-registered rewrite rules, custom post types and custom taxonomies
   SHALL remain out of scope, per the roadmap's compatibility boundary.

### Requirement 3 — Canonical redirects

**User Story:** As a site visitor arriving from an old link, a search result, or
a link grimoire's own REST API generated earlier, I want to land on the post
rather than a 404.

#### Acceptance Criteria

1. WHEN `permalink_structure` is non-empty AND a request arrives at the flat
   `/{slug}` path for a post whose canonical path differs THE system SHALL
   respond `301` with `Location` set to the canonical computed path.
2. WHEN a request arrives at a path that matches the structure but is not
   byte-identical to the post's canonical path — differing only in
   trailing-slash presence, or in a date component that is still consistent
   with the post — THE system SHALL respond `301` to the canonical path rather
   than rendering at the non-canonical path.
3. THE canonical path's trailing slash SHALL be determined by whether
   `permalink_structure` itself ends in `/`. The opposite form SHALL `301` to
   the canonical form. This prevents the same content being served at two URLs.
4. Redirects SHALL preserve the query string.
5. THE system SHALL NOT redirect in a loop: the canonical path for a post SHALL
   be a fixed point, and a request already at the canonical path SHALL render
   `200` without a redirect. This SHALL be covered by an explicit test.
6. WHEN `permalink_structure` is empty THE system SHALL issue no canonical
   redirects at all, since the flat path is then canonical.

### Requirement 4 — Unsupported or unparseable structures fail loudly, not silently

**User Story:** As an operator, if grimoire cannot honor my permalink
configuration, I want to be told at startup rather than discover it as a
site-wide 404.

#### Acceptance Criteria

1. WHEN `permalink_structure` is non-empty but contains a token outside
   Requirement 2's supported set (for example `%category%`, `%author%`), or
   contains no identifying token per Requirement 2.4, THE system SHALL treat
   the structure as unsupported.
2. WHEN the structure is unsupported THE system SHALL fall back to serving the
   flat `/{slug}` route AND SHALL emit a startup log record at `WARN` naming
   the offending structure and the specific unsupported token(s).
3. THE system SHALL start successfully in this case. Refusing to boot is
   explicitly rejected: grimoire reads a database it does not own, and a
   structure it cannot parse should degrade to a working flat site rather than
   an outage.
4. THE warning SHALL state that published URLs will not resolve while the
   fallback is active, so the operator understands the consequence rather than
   only the cause.
5. `grimoire-cli migrate -check` SHALL report the resolved permalink structure
   and whether it is supported, so the condition is discoverable before the
   server is started. This reuses the existing preflight-report surface.

### Requirement 5 — Extend the template hierarchy (9.F)

**User Story:** As a theme author, I want new route kinds to resolve templates
through the same predictable fallback chain the existing kinds use.

#### Acceptance Criteria

1. THE system SHALL extend the existing `render.hierarchy` map — the mechanism
   already exists and already falls back through `archive` to `index` — rather
   than introducing a second template-resolution path.
2. THE hierarchy SHALL gain `tag`, `author` and `date` kinds, each resolving in
   the order `{kind}` → `archive` → `index`.
3. These kinds SHALL be registered in this milestone even though 9.C serves no
   such routes yet, so that the follow-on spec adds route handlers only, with
   no template plumbing left to discover.
4. Slug-specific and entity-specific lookups (`category-{slug}`,
   `author-{nicename}`, `tag-{slug}`) SHALL be out of scope. No in-tree theme
   uses them and each multiplies the lookup surface; the existing mechanism
   makes them cheap to add later.
5. WHEN a theme provides none of a kind's candidate templates THE system SHALL
   continue to fall back to `index` rather than erroring, preserving current
   behavior.

### Requirement 6 — REST `link` fields reflect the canonical path

**User Story:** As a REST API consumer, I want the `link` field to be a URL that
actually serves the post.

#### Acceptance Criteria

1. THE `link` field produced by `internal/content/rest.go`'s `postLink` SHALL be
   the canonical permalink path computed from `permalink_structure`, not the
   hard-coded `"/" + slug`.
2. WHEN `permalink_structure` is empty THE `link` field SHALL remain `/{slug}`,
   unchanged.
3. THE web layer SHALL continue to resolve the returned relative path to an
   absolute URL from the request's scheme and host, unchanged from today.
4. `commentLink` and `userLink` SHALL remain on their current
   plain-permalink-shaped fallbacks (`/?p={id}#comment-{id}`, `/?author={id}`),
   because the routes they point at are not implemented until the follow-on
   spec. Changing them here would advertise URLs that 404.

### Requirement 7 — Test coverage

#### Acceptance Criteria

1. Permalink parsing and canonical-path construction SHALL be covered by pure
   unit tests with no database, including: every supported token individually;
   at least the three WordPress presets ("Day and name", "Month and name",
   "Post name"); zero-padding; an unsupported-token structure; a structure with
   no identifying token; and both trailing-slash forms.
2. Canonical redirect behavior SHALL be covered by handler tests asserting
   exact status codes and `Location` values, including the fixed-point case
   from Requirement 3.5.
3. THE date-mismatch `404` from Requirement 2.5 SHALL have an explicit test.
4. Behavior SHALL be verified against a **real WordPress database fixture**,
   gated behind an environment variable in the manner of
   [`../02.1-wp-hash-real-db`](../02.1-wp-hash-real-db), so CI stays hermetic
   while the parity claim is checked against real data shapes rather than only
   synthetic fixtures. The fixture SHALL exercise a dated permalink structure
   and a non-default table prefix.
5. No new migration SHALL be added. This milestone reads two additional option
   rows and adds no column, table or index; the task list SHALL verify this
   explicitly.

## Out of scope (deferred)

Deferred to the follow-on spec covering roadmap groups **9.C** and **9.D**:

- **Tag, date and author archive routes** (9.C). Requirement 5 lands their
  template kinds so the follow-on adds handlers only.
- **Nested category hierarchy** (9.D), including `domain.Term.ParentID`, the
  descendant-aware term queries, and the nested `/category/news/local` route.

**Decisions already resolved for that follow-on**, recorded here so they are not
relitigated — both were open questions the roadmap explicitly deferred to a
design document:

| Question | Resolution |
|---|---|
| Does a parent category archive include descendant categories' posts? (roadmap Req 13.3) | **Yes.** It is WordPress's default, and this milestone's premise is that an existing site's listings do not change. Requires a descendant-aware variant of `CountPublishedByTermSlug` so pagination totals stay correct. |
| Are nested category routes served *in addition to* or *instead of* flat `/category/{slug}`? (roadmap Req 13.2) | **Nested is canonical; flat `301`s to it.** Consistent with this milestone's treatment of post permalinks, keeps existing flat links working, and avoids serving one category at two `200` URLs. |

Also out of scope, unchanged from the roadmap's compatibility boundary:

- Plugin-registered rewrite rules, custom post types, custom taxonomies.
- `%category%` and `%author%` permalink tokens. They require resolving a term or
  user *during* path matching, which is a materially harder problem than the
  date/name/id tokens, and neither appears in a WordPress preset.
- Changing `permalink_structure` without restarting grimoire (see `design.md`).
