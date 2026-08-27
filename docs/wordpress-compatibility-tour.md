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

| WordPress | grimoire |
| --- | --- |
| ![WordPress public home showing published posts](./images/compatibility/public-home-wordpress.png) | ![grimoire public home showing the same published posts](./images/compatibility/public-home-grimoire.png) |

**What matches:** Both list the same published posts, in the same order,
with the same titles and links, from the same source post data.

**Intentional differences:** The products use different themes and page
chrome. When a post has an explicit custom excerpt, both products render
that same stored text verbatim (the first post's excerpt above is
byte-identical on both sides). When a post has no custom excerpt, each
product derives one from the post body using its own truncation rule —
WordPress's theme truncates to a shorter length than grimoire's ~55-word
auto-excerpt, so the second post's auto-generated excerpt above is visibly
longer on the grimoire side even though both are derived from the same
source content.

**Current limits:** This pair does not claim plugin or theme-rendering
parity.

**Pagination:** As of M8, both products page through older posts once
there are more posts than fit on one page, with matching out-of-range
behavior — a page number past the last available page returns `404` on
both, once the site has at least one published post. This screenshot
pair only shows the first page; it has not been recaptured to
demonstrate a later page.

## Single published post

| WordPress | grimoire |
| --- | --- |
| ![WordPress single post view](./images/compatibility/single-published-post-wordpress.png) | ![grimoire single post view of the same post](./images/compatibility/single-published-post-grimoire.png) |

**What matches:** Same slug, title, body, and publish date.

**Intentional differences:** Typography and theme chrome differ; no plugin
rendering is implied.

**Current limits:** grimoire's public router only supports flat `/{slug}`
routes. It does not implement WordPress's configurable, date-based post
permalink structures (for example WordPress's `/YYYY/MM/DD/slug/`).

## Category archive

| WordPress | grimoire |
| --- | --- |
| ![WordPress category archive](./images/compatibility/category-archive-wordpress.png) | ![grimoire category archive for the same category](./images/compatibility/category-archive-grimoire.png) |

**What matches:** Same category membership, order, titles, and post links.

**Intentional differences:** Archive styling and controls differ.

**Current limits:** grimoire's taxonomy model has no parent/child category
relationship. WordPress's `github` category is a child of `technology`, so
its canonical WordPress URL is nested (`/category/technology/github/`);
grimoire only supports a flat `/category/<slug>` route and has no concept of
nested categories.

**Pagination (M8):** As with the public home page above, category archives
now page through older posts once there are more posts than fit on one
page, with the same matching out-of-range `404` behavior on both products.
This screenshot pair only shows the first page; it has not been
recaptured for M8.

## Admin dashboard

| WordPress | grimoire |
| --- | --- |
| ![WordPress admin dashboard](./images/compatibility/admin-dashboard-wordpress.png) | ![grimoire admin dashboard](./images/compatibility/admin-dashboard-grimoire.png) |

**What matches:** Both are the logged-in landing page for managing the same
site's content.

**Intentional differences:** WordPress admin and the embedded Adobe React
Spectrum admin use different navigation, widgets, and visual design.

**Current limits:** Both screenshots have the logged-in user's real display
name replaced with the placeholder "Admin User" before capture, and the
WordPress screenshot has its "Recent Comments" activity widget removed —
both are in-browser redactions to avoid showing real names, not product
differences. In this specific capture, WordPress's "At a Glance" and "Quick
Draft" widget bodies render empty/collapsed — this is the actual state of
this WordPress installation at capture time, not something suppressed for
the screenshot; only "Site Health Status" and "WordPress Events and News"
show populated widget content.

## Posts list/editor

| WordPress | grimoire |
| --- | --- |
| ![WordPress posts list filtered to published posts (145 items)](./images/compatibility/posts-list-editor-wordpress.png) | ![grimoire posts list capture-redacted to its 7 published rows only, with unpublished rows removed before capture](./images/compatibility/posts-list-editor-grimoire.png) |

**What matches:** Both are the admin list view with entry points to edit the
same underlying posts, backed by the same database, showing published posts
only.

**Intentional differences:** Editing controls and layout differ; this
capture does not demonstrate saving, autosave, or Gutenberg block-editor
parity. grimoire's admin list also has no separate Author column, unlike
WordPress's.

**Current limits:** WordPress's screenshot uses its native
`?post_status=publish` filter — a real, working product control — to show
145 published posts across the product's own real pagination. grimoire's
admin posts list has no equivalent published-only filter today, so its
published-only view here is **not** a product control: every non-`publish`
row was removed from the live DOM before capture, and the on-screen "7
published item(s) shown (capture-redacted; unpublished rows removed)"
caption is disclosure text substituted for grimoire's real pagination/count
footer. The two visible counts are therefore not comparable — WordPress's
145 reflects an actual published-post total; grimoire's 7 reflects only
what a capture-time DOM redaction left on this one page, not the site's
real published-post count. The logged-in user's display name is replaced
with "Admin User" in both screenshots (redaction only, not a product
difference); WordPress's Author column, which also reads "Admin User" per
row here, received that same admin-identity redaction. This is distinct
from the public byline shown on the single-post page above ("Rob Weaver"),
which is left unredacted because a post's author byline is already public,
published information rather than an admin-only credential.

**Filters (M8):** The published-only gap described above is now addressed
for live use: grimoire's admin post list has gained an equivalent `status`
filter (alongside new `search` and `author` filters), all backed by
pagination and URL query state. The screenshot pair above predates that
work and still relies on the capture-time DOM redaction described above
rather than a live filter; it has not been recaptured for M8.

## Media library

| WordPress | grimoire |
| --- | --- |
| ![WordPress media library search results for "git" (3 attachments, 2 unattached)](./images/compatibility/media-library-wordpress.png) | ![grimoire media library DOM-filtered to the one attachment parented to the selected post](./images/compatibility/media-library-grimoire.png) |

**What matches:** Both show real, existing attachments from the same
uploads directory. `git_user` — the one attachment actually parented to the
selected post — appears in both screenshots.

**Intentional differences:** Layout and metadata presentation differ; no
upload or REST-write capability is implied.

**Current limits:** These two screenshots use different, non-equivalent
filters. The WordPress screenshot uses its native free-text admin search
(`?s=git`), which returns 3 attachments matching that text — only one of
them (`git_user`) is attached to the selected post; the other two
(`github-routing-diagram`, `github_hacker`) are unattached. grimoire's admin
media page has no free-text search, and its `parentId` query parameter is
accepted by the backend API but is not read by the admin page, so its
unfiltered media library (well over a thousand attachments in this database)
cannot be narrowed by URL alone. To make a legible, single-attachment
comparison possible, the grimoire screenshot's DOM was edited before capture
to remove every attachment card and row except `git_user`'s — it is not a
working product filter and is disclosed here as a capture-time adjustment,
not a grimoire feature.

The `git_user` attachment's own artwork depicts a fictional git-configuration
screenshot (a mock repository page and mock terminal session) that includes
placeholder names and fictional email addresses for a persona called "Sarah
Coder." This artwork is already public: it's embedded in the selected
post's own published body, visible directly in the raw `content.rendered`
HTML in the "Representative REST response" section below (post id 400774,
`alt="Git user woes"`), and that post's text explicitly states its "org,
user, and key names" are placeholders. No additional redaction was applied
to this already-public, already-fictional illustration.

**Filters (M8):** The gaps described above are now addressed for live
use: grimoire's admin media page has gained free-text `search`, `type`,
and `after`/`before` upload-date-range filters, plus a parent-post picker
that drives the `parentId` query parameter (previously accepted by the
backend API but not exposed in the admin UI), pagination, and a mutually
exclusive grid/list view toggle. The screenshot pair above predates that
work and still relies on the capture-time DOM edit described above rather
than a live filter; it has not been recaptured for M8.

## Representative REST response

| WordPress | grimoire |
| --- | --- |
| ![WordPress REST API response for the selected post](./images/compatibility/rest-response-wordpress.png) | ![grimoire REST API response for the same post](./images/compatibility/rest-response-grimoire.png) |

**What matches:** Both serve `GET /wp-json/wp/v2/posts?slug=how-to-painlessly-run-multiple-github-accounts-on-one-machine&status=publish`
and return the same post's fields — both responses are for post `id: 400774`.

**Intentional differences:** Key order and some optional fields may differ.
The `content.rendered` field itself differs semantically between the two
responses in two ways. First, WordPress strips Gutenberg block-comment
delimiters (for example `<!-- wp:paragraph -->`) from the rendered HTML;
grimoire retains them un-stripped. Second, for the embedded `<img>` tag both
responses emit the *same* root-relative `src` (no host), but WordPress
additionally injects responsive-image attributes — `srcset` (with
width-keyed URLs that *are* absolute, on WordPress's own host), `sizes`,
`loading`, and `decoding` — that grimoire's rendering does not add. Both
represent the same underlying post content, but a consumer parsing
`content.rendered` as final, fully-processed HTML should account for both
differences. Both responses also have their local capture hostname replaced
in-page with a placeholder domain (`wordpress.example.test` /
`grimoire.example.test`) before capture so no local network detail is shown
in most fields; the one exception is the WordPress response's stored
`guid.rendered` value, which is emitted verbatim as originally recorded in
the database and was not part of the capture-time substitution, so it still
shows the original database host (`accuweaver.com`) rather than the
placeholder domain — disclosed here rather than silently edited, to keep
the displayed JSON faithful to what the server actually returns. Aside from
that one field, all other JSON structure and values are exactly as returned
by each server.

**Current limits:** One `GET` request is not a claim of total REST API
parity; see [`./compatibility.md`](./compatibility.md) for the full
supported surface.
