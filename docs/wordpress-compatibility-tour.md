# WordPress Compatibility Visual Tour

This tour is for prospective users evaluating grimoire as a WordPress-schema-
compatible alternative. It compares WordPress and grimoire side by side
against the **same existing local content** — the same posts, categories,
and uploads in the same MySQL/MariaDB database — captured locally for this
comparison only.

These pairs demonstrate **behavioral parity** (same data, same routes, same
publishing semantics), not pixel-identical rendering. Themes, admin chrome,
and styling intentionally differ between the two products.

See [`./compatibility.md`](./compatibility.md) for the full technical
compatibility guide.

---

## Public home

| WordPress | Grimoire |
| --- | --- |
| ![WordPress public home showing published posts](./images/compatibility/public-home-wordpress.png) | ![Grimoire public home showing the same published posts](./images/compatibility/public-home-grimoire.png) |

**What matches:** Both list the same published posts, in the same order,
with the same titles, excerpts, and links.

**Intentional differences:** The products use different themes and page
chrome.

**Current limits:** This pair does not claim plugin or theme-rendering
parity.

## Single published post

| WordPress | Grimoire |
| --- | --- |
| ![WordPress single post view](./images/compatibility/single-published-post-wordpress.png) | ![Grimoire single post view of the same post](./images/compatibility/single-published-post-grimoire.png) |

**What matches:** Same slug, title, body, publish date, and categories.

**Intentional differences:** Typography and theme chrome differ; no plugin
rendering is implied.

**Current limits:** Grimoire's public router only supports flat `/{slug}`
routes. It does not implement WordPress's configurable, date-based post
permalink structures (for example WordPress's `/YYYY/MM/DD/slug/`).

## Category archive

| WordPress | Grimoire |
| --- | --- |
| ![WordPress category archive](./images/compatibility/category-archive-wordpress.png) | ![Grimoire category archive for the same category](./images/compatibility/category-archive-grimoire.png) |

**What matches:** Same category membership, order, titles, and post links.

**Intentional differences:** Archive styling and controls differ.

**Current limits:** grimoire's taxonomy model has no parent/child category
relationship. WordPress's `github` category is a child of `technology`, so
its canonical WordPress URL is nested (`/category/technology/github/`);
grimoire only supports a flat `/category/<slug>` route and has no concept of
nested categories.

## Admin dashboard

| WordPress | Grimoire |
| --- | --- |
| ![WordPress admin dashboard](./images/compatibility/admin-dashboard-wordpress.png) | ![Grimoire admin dashboard](./images/compatibility/admin-dashboard-grimoire.png) |

**What matches:** Both are the logged-in landing page for managing the same
site's content.

**Intentional differences:** WordPress admin and the embedded Adobe React
Spectrum admin use different navigation, widgets, and visual design.

**Current limits:** None beyond the navigation/widget differences already
noted.

## Posts list/editor

| WordPress | Grimoire |
| --- | --- |
| ![WordPress posts list, published only](./images/compatibility/posts-list-editor-wordpress.png) | ![Grimoire posts list, published only](./images/compatibility/posts-list-editor-grimoire.png) |

**What matches:** Both show the published post list with entry points to
edit the same post.

**Intentional differences:** Editing controls and layout differ; this
capture does not demonstrate saving, autosave, or Gutenberg block-editor
parity.

**Current limits:** WordPress's list uses its native `?post_status=publish`
filter. grimoire's admin posts list has no equivalent published-only filter
today, so this screenshot's published-only view reflects a capture-time
adjustment rather than a real product control.

## Media library

| WordPress | Grimoire |
| --- | --- |
| ![WordPress media library search results](./images/compatibility/media-library-wordpress.png) | ![Grimoire media library filtered to the same post's attachments](./images/compatibility/media-library-grimoire.png) |

**What matches:** Both show existing, real attachments from the same
uploads directory related to the selected post.

**Intentional differences:** Layout and metadata presentation differ; no
upload or REST-write capability is implied.

**Current limits:** WordPress's admin media search (`?s=`) is free-text and
returns 3 matching attachments here. grimoire's admin media page has no
free-text search or working attachment-scoping URL parameter today — its
`parentId` query parameter is accepted by the backend API but is not read by
the admin page, so this screenshot's single-attachment view reflects a
capture-time adjustment rather than a real product control.

## Representative REST response

| WordPress | Grimoire |
| --- | --- |
| ![WordPress REST API response for the selected post](./images/compatibility/rest-response-wordpress.png) | ![Grimoire REST API response for the same post](./images/compatibility/rest-response-grimoire.png) |

**What matches:** Both serve `GET /wp-json/wp/v2/posts?slug=how-to-painlessly-run-multiple-github-accounts-on-one-machine&status=publish`
and return the same post's fields.

**Intentional differences:** Key order and some optional fields may differ.

**Current limits:** One `GET` request is not a claim of total REST API
parity; see [`./compatibility.md`](./compatibility.md) for the full
supported surface.
