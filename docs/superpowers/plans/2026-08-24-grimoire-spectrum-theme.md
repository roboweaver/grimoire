# Grimoire Spectrum Theme (Direction A: Faithful) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the Grimoire public theme (`themes/default`) so it keeps its current WordPress-reference-site silhouette (header, two-column card grid of posts, single-post view, comments, nav) but is rendered with real Adobe Spectrum CSS (`@spectrum-css/*`) tokens and components instead of unstyled HTML.

**Architecture:** Add a Node build-time-only tool (`web/theme/`) that vendors `@spectrum-css/*` package CSS into `themes/default/static/css/vendor/` (committed output, mirroring the existing `web/admin/` → `internal/admin/dist` convention and its `admin-freshness` CI job). Add a new disk-based static file route (`/theme/static/*`) in `internal/web` so the server can serve that CSS at runtime (theme files already load from disk, unlike the embedded admin SPA). Edit the Go `html/template` files in `themes/default/templates/` to add Spectrum component classes and light structural markup (card wrappers, dates, read-more links) — the underlying Go data model (`PostView`) has no image/tag fields, so Direction A matches only the layout/typography/spacing silhouette, not literal images or tags. Every template edit regenerates and hand-reviews the byte-exact golden fixtures in `internal/render/testdata/golden/`.

**Tech Stack:** Go 1.26 (`html/template`, `chi` router), Node 22 (build-time only, no Node at runtime), `@spectrum-css/tokens`, `@spectrum-css/typography`, `@spectrum-css/page`, `@spectrum-css/card`, `@spectrum-css/link`, `@spectrum-css/button`, `@spectrum-css/textfield`, `@spectrum-css/divider`.

---

## Scope notes (read first)

- The Grimoire `PostView` struct (`internal/render/view.go`) only has `Slug, Title, Excerpt, Content, Date, Author`. There are **no** featured-image or tag/category-chip fields today. Direction A therefore recreates the WordPress reference's card *layout* (title, excerpt, date, "Read more" action, 2-column grid) using Spectrum Card/Typography/Button classes — it does **not** add images or tag chips. Adding those would require new content-model work and is out of scope for this plan.
- Every task that edits a `.tmpl` file **must** regenerate the golden fixtures with `go test ./internal/render/... -run TestGolden -update` and the diff must be reviewed with `git diff` before committing (see `internal/render/golden_test.go`).
- Spectrum CSS token/class names below are best-effort based on public Spectrum CSS documentation. Task 1 includes an explicit verification step: after `npm install`, grep the installed packages' own CSS/README files for the real custom-property and class names and correct any mismatches in `theme.css`/templates before proceeding to later tasks. Do not silently trust the names below over what the installed packages actually define.

---

## File Structure

New files:
- `web/theme/package.json` — Node build tool manifest (spectrum-css deps + build script)
- `web/theme/build.mjs` — copies vendor CSS from `node_modules/@spectrum-css/*/dist/index.css` into `themes/default/static/css/vendor/`
- `themes/default/static/css/vendor/.gitkeep` → replaced by real vendored files after first build
- `themes/default/static/css/spectrum.css` — thin manifest that `@import`s the vendored files in the right cascade order
- `themes/default/static/css/theme.css` — hand-written composition rules (layout grid, spacing, card/nav/comment-form layout) using only Spectrum custom properties/classes, no hardcoded colors

Modified files:
- `internal/web/static.go` — add `registerThemeStatic`
- `internal/web/handlers.go` — add `WithThemeStatic` method + field on `Server`
- `internal/web/router.go` — call the new registration in `Routes()`
- `internal/web/static_test.go` — test for the new route
- `internal/web/handlers_test.go` — wire `WithThemeStatic` into `newTestServer`
- `cmd/grimoire/main.go` — call `.WithThemeStatic(*themesDir, cfg.Theme)` (or equivalent) when constructing the server
- `themes/default/templates/base.tmpl`, `index.tmpl`, `archive.tmpl`, `category.tmpl`, `single.tmpl`, `page.tmpl`, `partials/nav-menu.tmpl`, `partials/comments.tmpl` — Spectrum classes/markup
- `internal/render/testdata/golden/{index,single,category}.html` — regenerated via `-update`, reviewed by hand
- `Makefile` — add `theme-css` target mirroring `admin`
- `.github/workflows/ci.yml` — add `theme-css-freshness` job mirroring `admin-freshness`

---

### Task 1: Vendor Spectrum CSS via a Node build tool

**Files:**
- Create: `web/theme/package.json`
- Create: `web/theme/build.mjs`
- Create: `themes/default/static/css/vendor/.gitkeep`
- Modify: `Makefile`

- [ ] **Step 1: Create the package manifest**

```json
{
  "name": "grimoire-theme-css",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "build": "node build.mjs"
  },
  "dependencies": {
    "@spectrum-css/tokens": "^13",
    "@spectrum-css/typography": "^9",
    "@spectrum-css/page": "^7",
    "@spectrum-css/card": "^6",
    "@spectrum-css/link": "^5",
    "@spectrum-css/button": "^8",
    "@spectrum-css/textfield": "^7",
    "@spectrum-css/divider": "^4"
  }
}
```

- [ ] **Step 2: Install and record the exact resolved versions**

Run: `cd web/theme && npm install`
This creates `web/theme/package-lock.json` (commit it) and `web/theme/node_modules` (do not commit; add `web/theme/node_modules/` to `.gitignore` if not already ignored).

- [ ] **Step 3: Verify actual token/class names before writing any CSS**

Run:
```bash
grep -o -- '--spectrum-[a-zA-Z0-9-]*' web/theme/node_modules/@spectrum-css/tokens/dist/index.css | sort -u | head -60
grep -o 'class="[^"]*"' web/theme/node_modules/@spectrum-css/card/dist/index.css 2>/dev/null || true
sed -n '1,80p' web/theme/node_modules/@spectrum-css/card/README.md
sed -n '1,80p' web/theme/node_modules/@spectrum-css/textfield/README.md
sed -n '1,80p' web/theme/node_modules/@spectrum-css/button/README.md
```
Record the real custom-property names (color/spacing/radius/shadow/font tokens) and real component class names (e.g. confirm whether it is `spectrum-Card`, `spectrum-Card-title`, `spectrum-Card-footer`, etc., and the same for `spectrum-Textfield`, `spectrum-Button`, `spectrum-Button-label`, `spectrum-FieldLabel`). If any name in Tasks 3-7 below doesn't match what you find here, use the real name from the installed package instead — this is not optional.

- [ ] **Step 4: Write the build script**

```js
// web/theme/build.mjs
// Copies the compiled CSS from each vendored @spectrum-css/* package into
// themes/default/static/css/vendor/, so the Go server can serve plain static
// files at runtime with no Node dependency. Node is build-time only.
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(here, "..", "..", "themes", "default", "static", "css", "vendor");
mkdirSync(outDir, { recursive: true });

const packages = [
  "tokens",
  "typography",
  "page",
  "card",
  "link",
  "button",
  "textfield",
  "divider",
];

const manifestLines = [
  "/* GENERATED FILE. Do not edit by hand.",
  " * Run `make theme-css` (web/theme/build.mjs) to regenerate. */",
];

for (const pkg of packages) {
  const src = join(here, "node_modules", `@spectrum-css`, pkg, "dist", "index.css");
  const destName = `${pkg}.css`;
  const dest = join(outDir, destName);
  copyFileSync(src, dest);
  manifestLines.push(`@import url("vendor/${destName}");`);
  console.log(`copied ${pkg} -> themes/default/static/css/vendor/${destName}`);
}

const manifestPath = join(here, "..", "..", "themes", "default", "static", "css", "spectrum.css");
writeFileSync(manifestPath, manifestLines.join("\n") + "\n");
console.log(`wrote ${manifestPath}`);
```

- [ ] **Step 5: Add the `.gitkeep` placeholder and run the build**

```bash
mkdir -p themes/default/static/css/vendor
touch themes/default/static/css/vendor/.gitkeep
cd web/theme && npm run build
```
Expected: `themes/default/static/css/vendor/{tokens,typography,page,card,link,button,textfield,divider}.css` and `themes/default/static/css/spectrum.css` now exist and are non-empty. Remove `.gitkeep` if git already tracks the real files.

- [ ] **Step 6: Add the Makefile target**

Add near the existing `admin` target in `Makefile`:
```makefile
.PHONY: theme-css
theme-css:
	cd web/theme && npm ci && npm run build
```

- [ ] **Step 7: Commit**

```bash
git add web/theme themes/default/static/css Makefile
git commit -m "build: vendor Spectrum CSS for the public theme"
```

---

### Task 2: Serve theme static files from Go

**Files:**
- Modify: `internal/web/static.go`
- Modify: `internal/web/handlers.go`
- Modify: `internal/web/router.go`
- Modify: `internal/web/handlers_test.go`
- Test: `internal/web/static_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/web/static_test.go` (follow the existing test style in that file):
```go
func TestThemeStaticServesVendoredCSS(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/theme/static/css/spectrum.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /theme/static/css/spectrum.css: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", ct)
	}
}
```
(Add `"strings"` to the import block if not already present.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestThemeStaticServesVendoredCSS -v`
Expected: FAIL — 404, because no such route exists yet.

- [ ] **Step 3: Add `registerThemeStatic` to `internal/web/static.go`**

Add alongside the existing `registerStatic` function:
```go
// registerThemeStatic mounts the active theme's static/ directory (e.g. its
// vendored Spectrum CSS) at /theme/static/*. Unlike registerStatic (which
// serves go:embed'd assets), themes are already loaded from disk at runtime,
// so this serves directly from themeDir with no embedding.
func registerThemeStatic(r chi.Router, themeStaticDir string) {
	fileServer := http.StripPrefix("/theme/static/", http.FileServer(http.Dir(themeStaticDir)))
	r.Get("/theme/static/*", func(w http.ResponseWriter, req *http.Request) {
		fileServer.ServeHTTP(w, req)
	})
}
```

- [ ] **Step 4: Thread the theme static directory into `Server`**

In `internal/web/handlers.go`, find the `Server` struct definition and add a field, e.g.:
```go
type Server struct {
	// ...existing fields...
	themeStaticDir string
}
```
Add a chained setter next to any existing `With...` methods (e.g. `WithAuth`, `WithAdmin` — match whatever pattern already exists in this file):
```go
// WithThemeStatic configures the server to serve the given theme's static/
// directory (e.g. its vendored Spectrum CSS) at /theme/static/*. themesDir is
// the root themes directory (as passed to render.Load), theme is the active
// theme name.
func (s *Server) WithThemeStatic(themesDir, theme string) *Server {
	s.themeStaticDir = filepath.Join(themesDir, theme, "static")
	return s
}
```
Add `"path/filepath"` to imports if not already present.

- [ ] **Step 5: Register the route in `Routes()`**

In `internal/web/router.go`, find the call to `registerStatic(r)` and add, immediately after it:
```go
if s.themeStaticDir != "" {
	registerThemeStatic(r, s.themeStaticDir)
}
```
This must be registered before the catch-all `/{slug}` route (same rule that already applies to `registerStatic`).

- [ ] **Step 6: Wire it into the test server helper**

In `internal/web/handlers_test.go`, update `newTestServer` where `srv := web.NewServer(...)` is built:
```go
srv := web.NewServer(
	content.NewPostService(repos.Posts),
	content.NewTermService(repos.Terms, repos.Posts),
	content.NewOptionService(repos.Options),
	eng,
	nil,
).WithThemeStatic(filepath.Join("..", "..", "themes"), "default")
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/web/... -run TestThemeStaticServesVendoredCSS -v`
Expected: PASS

- [ ] **Step 8: Wire it into `cmd/grimoire/main.go`**

Find where `web.NewServer(...)` is constructed in `cmd/grimoire/main.go` and where the `--themes` flag / theme name are available, then chain:
```go
srv = srv.WithThemeStatic(*themesDir, cfg.Theme)
```
(Use whatever the existing local variable names are for the themes dir flag and configured theme name in that file — match the exact identifiers already in `main.go`, don't introduce new ones.)

- [ ] **Step 9: Run the full web package test suite**

Run: `go test ./internal/web/...`
Expected: all tests PASS

- [ ] **Step 10: Commit**

```bash
git add internal/web cmd/grimoire/main.go
git commit -m "feat: serve theme static assets at /theme/static/*"
```

---

### Task 3: Apply Spectrum shell classes to `base.tmpl`

**Files:**
- Modify: `themes/default/templates/base.tmpl`
- Modify: `themes/default/static/css/theme.css` (create if it doesn't exist yet from Task 1 — if Task 1's build script only wrote `spectrum.css`, create `theme.css` by hand now)
- Test: `internal/render/testdata/golden/{index,single,category}.html` (regenerated)

- [ ] **Step 1: Confirm current golden tests pass before changing anything**

Run: `go test ./internal/render/...`
Expected: PASS (baseline, before any template edits)

- [ ] **Step 2: Edit `base.tmpl`**

Replace the file's `<html>`/`<head>`/`<header>`/`<footer>` structure with:
```html
{{define "base"}}<!doctype html>
<html lang="en" class="spectrum spectrum--light spectrum--medium">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{block "title" .}}{{.SiteTitle}}{{end}}</title>
<link rel="icon" href="/favicon.ico" sizes="any">
<link rel="icon" type="image/png" sizes="32x32" href="/assets/icons/favicon-32x32.png">
<link rel="icon" type="image/png" sizes="16x16" href="/assets/icons/favicon-16x16.png">
<link rel="apple-touch-icon" sizes="180x180" href="/assets/icons/apple-touch-icon.png">
<link rel="manifest" href="/assets/icons/site.webmanifest">
<link rel="stylesheet" href="/theme/static/css/spectrum.css">
<link rel="stylesheet" href="/theme/static/css/theme.css">
</head>
<body class="theme-body">
<header class="theme-header"><a class="spectrum-Link spectrum-Link--quiet theme-header-link" href="/">{{.SiteTitle}}</a></header>
<main class="theme-main">{{block "content" .}}{{end}}</main>
<footer class="theme-footer"><small class="spectrum-Body spectrum-Body--sizeS">Powered by grimoire</small></footer>
</body>
</html>{{end}}
```
(Keep whatever favicon/manifest `<link>` tags already exist in the file — only add the two new `<link rel="stylesheet">` lines and the class attributes; do not remove existing head links.)

- [ ] **Step 3: Create `themes/default/static/css/theme.css` (skip if Task 1 already created it)**

```css
/* Grimoire public theme composition rules (Direction A: Faithful).
 * Built entirely on Spectrum CSS custom properties from spectrum.css —
 * verify every --spectrum-* name below against the installed packages
 * (see Task 1, Step 3) and correct any that don't match. */

.theme-body {
	margin: 0;
	background: var(--spectrum-global-color-gray-100);
	color: var(--spectrum-global-color-gray-900);
}

.theme-header {
	display: flex;
	align-items: center;
	padding: var(--spectrum-global-dimension-size-300) var(--spectrum-global-dimension-size-400);
	background: var(--spectrum-global-color-gray-50);
	border-bottom: 1px solid var(--spectrum-global-color-gray-300);
}

.theme-header-link {
	font-size: var(--spectrum-global-dimension-font-size-400);
}

.theme-main {
	max-width: 960px;
	margin: 0 auto;
	padding: var(--spectrum-global-dimension-size-400);
}

.theme-footer {
	text-align: center;
	padding: var(--spectrum-global-dimension-size-300);
	color: var(--spectrum-global-color-gray-700);
}
```

- [ ] **Step 4: Regenerate golden fixtures**

Run: `go test ./internal/render/... -run TestGolden -update`

- [ ] **Step 5: Review the diff by hand**

Run: `git diff internal/render/testdata/golden`
Confirm the diff only reflects the intended `base.tmpl` structural changes (new classes, new stylesheet links) across all three golden files, and nothing unrelated changed.

- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add themes/default/templates/base.tmpl themes/default/static/css/theme.css internal/render/testdata/golden
git commit -m "style: apply Spectrum shell classes to base template"
```

---

### Task 4: Style the post-listing card grid (`index.tmpl`, `archive.tmpl`, `category.tmpl`)

**Files:**
- Modify: `themes/default/templates/index.tmpl`
- Modify: `themes/default/templates/archive.tmpl`
- Modify: `themes/default/templates/category.tmpl`
- Modify: `themes/default/static/css/theme.css`
- Test: `internal/render/testdata/golden/{index,category}.html` (regenerated)

- [ ] **Step 1: Edit `index.tmpl`**

```html
{{define "content"}}
<div class="theme-page-head">
<h1 class="spectrum-Heading spectrum-Heading--sizeXL">{{.SiteTitle}}</h1>
{{if .Tagline}}<p class="spectrum-Body spectrum-Body--sizeL theme-tagline">{{.Tagline}}</p>{{end}}
</div>
<div class="theme-card-grid">
{{range .Posts}}
<article class="spectrum-Card theme-card">
<div class="spectrum-Card-body">
<div class="spectrum-Card-header"><h2 class="spectrum-Card-title"><a class="spectrum-Link spectrum-Link--quiet" href="/{{.Slug}}">{{.Title}}</a></h2></div>
<div class="spectrum-Card-content">
<time class="spectrum-Body spectrum-Body--sizeS theme-card-date" datetime="{{.Date.Format "2006-01-02"}}">{{.Date.Format "Jan 2, 2006"}}</time>
<div class="spectrum-Body spectrum-Body--sizeM theme-card-excerpt">{{.Excerpt}}</div>
</div>
</div>
<div class="spectrum-Card-footer"><a class="spectrum-Button spectrum-Button--secondary spectrum-Button--sizeM" href="/{{.Slug}}"><span class="spectrum-Button-label">Read more</span></a></div>
</article>
{{else}}
<p class="spectrum-Body spectrum-Body--sizeM">No posts yet.</p>
{{end}}
</div>
{{end}}
```

- [ ] **Step 2: Apply the same card-grid markup to `archive.tmpl` and `category.tmpl`**

Both files render a post list the same way `index.tmpl` does (check each file's current `{{range .Posts}}` block and replace it with the identical `<div class="theme-card-grid">...</div>` block from Step 1). `category.tmpl` additionally has a heading for `.Term.Name` — keep that heading, just add `class="spectrum-Heading spectrum-Heading--sizeXL"` to it, and wrap the post loop in the same `theme-card-grid` markup as Step 1.

- [ ] **Step 3: Add card/grid CSS to `theme.css`**

Append:
```css
.theme-page-head {
	margin-bottom: var(--spectrum-global-dimension-size-400);
}

.theme-tagline {
	color: var(--spectrum-global-color-gray-700);
	margin-top: var(--spectrum-global-dimension-size-75);
}

.theme-card-grid {
	display: grid;
	grid-template-columns: repeat(2, 1fr);
	gap: var(--spectrum-global-dimension-size-300);
}

@media (max-width: 640px) {
	.theme-card-grid {
		grid-template-columns: 1fr;
	}
}

.theme-card {
	padding: var(--spectrum-global-dimension-size-250);
}

.theme-card-date {
	display: block;
	color: var(--spectrum-global-color-gray-700);
	margin-bottom: var(--spectrum-global-dimension-size-100);
}

.theme-card-excerpt {
	margin-top: var(--spectrum-global-dimension-size-100);
}
```

- [ ] **Step 4: Regenerate golden fixtures**

Run: `go test ./internal/render/... -run TestGolden -update`

- [ ] **Step 5: Review the diff by hand**

Run: `git diff internal/render/testdata/golden`
Confirm `index.html` and `category.html` now show the card markup and nothing else changed unexpectedly.

- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add themes/default/templates/index.tmpl themes/default/templates/archive.tmpl themes/default/templates/category.tmpl themes/default/static/css/theme.css internal/render/testdata/golden
git commit -m "style: apply Spectrum card grid to post listing templates"
```

---

### Task 5: Style the single post and page views

**Files:**
- Modify: `themes/default/templates/single.tmpl`
- Modify: `themes/default/templates/page.tmpl`
- Modify: `themes/default/static/css/theme.css`
- Test: `internal/render/testdata/golden/single.html` (regenerated)

- [ ] **Step 1: Edit `single.tmpl`**

Keep the existing `{{template "nav-menu" .Menu}}` and `{{template "comments" .}}` calls in place; replace the `<article>` block between them with:
```html
<article class="theme-article">
<h1 class="spectrum-Heading spectrum-Heading--sizeXXL">{{.Post.Title}}</h1>
<time class="spectrum-Body spectrum-Body--sizeS theme-article-date" datetime="{{.Post.Date.Format "2006-01-02"}}">{{.Post.Date.Format "January 2, 2006"}}</time>
<div class="spectrum-Body spectrum-Body--sizeM theme-article-content">{{.Post.Content}}</div>
</article>
```

- [ ] **Step 2: Edit `page.tmpl`**

Replace the existing `<article class="page">...</article>` block with (no date — pages aren't dated content in the current UI, matching the pre-change template's behavior):
```html
<article class="theme-article theme-page">
<h1 class="spectrum-Heading spectrum-Heading--sizeXXL">{{.Post.Title}}</h1>
<div class="spectrum-Body spectrum-Body--sizeM theme-article-content">{{.Post.Content}}</div>
</article>
```

- [ ] **Step 3: Add article CSS to `theme.css`**

Append:
```css
.theme-article {
	max-width: 720px;
	margin: 0 auto;
}

.theme-article-date {
	display: block;
	color: var(--spectrum-global-color-gray-700);
	margin: var(--spectrum-global-dimension-size-100) 0 var(--spectrum-global-dimension-size-300);
}
```

- [ ] **Step 4: Regenerate golden fixtures**

Run: `go test ./internal/render/... -run TestGolden -update`

- [ ] **Step 5: Review the diff by hand**

Run: `git diff internal/render/testdata/golden/single.html`

- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add themes/default/templates/single.tmpl themes/default/templates/page.tmpl themes/default/static/css/theme.css internal/render/testdata/golden
git commit -m "style: apply Spectrum typography to single post and page templates"
```

---

### Task 6: Style the comments partial

**Files:**
- Modify: `themes/default/templates/partials/comments.tmpl`
- Modify: `themes/default/static/css/theme.css`
- Test: `internal/render/testdata/golden/single.html` (regenerated)

- [ ] **Step 1: Edit `partials/comments.tmpl`**

Keep all existing Go template logic/fields exactly as they are (comment list iteration, `.PendingComment`, `.CommentToken`, the honeypot `website` field, form `action`/`method`) — only add Spectrum classes and wrap the existing inputs in Spectrum field containers, verified against the real markup you inspected in Task 1 Step 3:
```html
{{define "comments"}}
<section id="comments" class="theme-comments">
<h2 class="spectrum-Heading spectrum-Heading--sizeL">{{.CommentCount}} Comments</h2>
{{if .PendingComment}}<p class="spectrum-Body spectrum-Body--sizeS theme-comment-pending">Your comment is awaiting moderation: {{.PendingComment.Content}}</p>{{end}}
{{if .Comments}}
<ol class="theme-comment-list">
{{range .Comments}}
<li class="theme-comment">
<p class="spectrum-Body spectrum-Body--sizeS theme-comment-meta"><strong>{{.Author}}</strong> <time datetime="{{.Date.Format "2006-01-02"}}">{{.Date.Format "Jan 2, 2006"}}</time></p>
<div class="spectrum-Body spectrum-Body--sizeM">{{.Content}}</div>
</li>
{{end}}
</ol>
{{else}}
<p class="spectrum-Body spectrum-Body--sizeM">No comments yet</p>
{{end}}
<form method="post" action="/comment" class="theme-comment-form">
<input type="hidden" name="post_id" value="{{.Post.Slug}}">
<input type="hidden" name="comment_csrf_token" value="{{.CommentToken}}">
<div class="spectrum-Textfield theme-form-field">
<label class="spectrum-FieldLabel" for="comment-author">Name</label>
<input class="spectrum-Textfield-input" id="comment-author" name="author">
</div>
<div class="spectrum-Textfield theme-form-field">
<label class="spectrum-FieldLabel" for="comment-email">Email</label>
<input class="spectrum-Textfield-input" id="comment-email" name="email">
</div>
<div class="spectrum-Textfield theme-form-field">
<label class="spectrum-FieldLabel" for="comment-url">URL</label>
<input class="spectrum-Textfield-input" id="comment-url" name="url">
</div>
<p style="display:none"><label>Website <input name="website"></label></p>
<div class="spectrum-Textfield theme-form-field theme-form-field--textarea">
<label class="spectrum-FieldLabel" for="comment-content">Comment</label>
<textarea class="spectrum-Textfield-input" id="comment-content" name="content"></textarea>
</div>
<button type="submit" class="spectrum-Button spectrum-Button--cta spectrum-Button--sizeM"><span class="spectrum-Button-label">Submit comment</span></button>
</form>
</section>
{{end}}
```
If the existing template has extra fields/attributes not shown above (e.g. `required`, `maxlength`, ARIA attributes), keep them — only add the classes and wrapper `<div>`s shown here.

- [ ] **Step 2: Add comment/form CSS to `theme.css`**

Append:
```css
.theme-comments {
	max-width: 720px;
	margin: var(--spectrum-global-dimension-size-500) auto 0;
	border-top: 1px solid var(--spectrum-global-color-gray-300);
	padding-top: var(--spectrum-global-dimension-size-400);
}

.theme-comment-list {
	list-style: none;
	padding: 0;
}

.theme-comment {
	padding: var(--spectrum-global-dimension-size-200) 0;
	border-bottom: 1px solid var(--spectrum-global-color-gray-200);
}

.theme-form-field {
	margin-bottom: var(--spectrum-global-dimension-size-200);
}
```

- [ ] **Step 3: Regenerate golden fixtures**

Run: `go test ./internal/render/... -run TestGolden -update`

- [ ] **Step 4: Review the diff by hand**

Run: `git diff internal/render/testdata/golden/single.html`
Confirm the comment form markup matches what you wrote and every original field/value (`post_id`, `comment_csrf_token`, honeypot `website`) is still present.

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add themes/default/templates/partials/comments.tmpl themes/default/static/css/theme.css internal/render/testdata/golden
git commit -m "style: apply Spectrum form and button classes to comments partial"
```

---

### Task 7: Style the nav menu partial

**Files:**
- Modify: `themes/default/templates/partials/nav-menu.tmpl`
- Modify: `themes/default/static/css/theme.css`
- Test: `internal/render/testdata/golden/single.html` (regenerated, only if `single.tmpl` renders a non-empty menu in its golden fixture — check first)

- [ ] **Step 1: Inspect the current file and golden output**

Run: `cat themes/default/templates/partials/nav-menu.tmpl`
Run: `grep -A3 theme-nav internal/render/testdata/golden/single.html || grep -A3 '<nav' internal/render/testdata/golden/single.html`
Confirm the recursive structure (top-level `<ul>` plus a nested items partial for `.Children`) before editing, so you preserve the exact recursion.

- [ ] **Step 2: Edit `partials/nav-menu.tmpl`**

Preserve the existing recursive template names exactly as they are today (do not rename them) — only add classes, e.g.:
```html
{{define "nav-menu"}}
{{if .Items}}
<nav class="theme-nav">
<ul class="theme-nav-list">
{{template "nav-menu-items" .Items}}
</ul>
</nav>
{{end}}
{{end}}

{{define "nav-menu-items"}}
{{range .}}
<li class="theme-nav-item"><a class="spectrum-Link spectrum-Link--quiet" href="{{.URL}}">{{.Label}}</a>{{if .Children}}<ul class="theme-nav-sublist">{{template "nav-menu-items" .Children}}</ul>{{end}}</li>
{{end}}
{{end}}
```
If the real template defines different field names (e.g. `.Label` vs `.Title`, `.URL` vs `.Link`) or different template names, use the real ones from Step 1 — do not rename existing fields/templates.

- [ ] **Step 3: Add nav CSS to `theme.css`**

Append:
```css
.theme-nav-list,
.theme-nav-sublist {
	display: flex;
	gap: var(--spectrum-global-dimension-size-200);
	list-style: none;
	padding: 0;
	margin: 0;
}
```

- [ ] **Step 4: Regenerate golden fixtures**

Run: `go test ./internal/render/... -run TestGolden -update`

- [ ] **Step 5: Review the diff by hand**

Run: `git diff internal/render/testdata/golden`

- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add themes/default/templates/partials/nav-menu.tmpl themes/default/static/css/theme.css internal/render/testdata/golden
git commit -m "style: apply Spectrum link classes to nav menu partial"
```

---

### Task 8: Add a CI freshness gate for the vendored theme CSS

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Read the existing `admin-freshness` job for the exact pattern to mirror**

Run: `grep -n -A20 "admin-freshness" .github/workflows/ci.yml`

- [ ] **Step 2: Add a matching job**

Add a new job to `.github/workflows/ci.yml`, copying the `admin-freshness` job's `runs-on`, checkout, and Node setup steps exactly, but pointed at the theme build:
```yaml
  theme-css-freshness:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
      - name: Build theme CSS
        run: make theme-css
      - name: Fail if vendored theme CSS is stale
        run: git diff --exit-code -- themes/default/static/css
```
(Match the exact `actions/checkout` and `actions/setup-node` versions already pinned in the `admin-freshness` job rather than the versions shown here, if they differ.)

- [ ] **Step 3: Validate the workflow YAML**

Run: `python3 -c "import yaml, sys; yaml.safe_load(open('.github/workflows/ci.yml'))"`
Expected: no output (valid YAML).

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add freshness check for vendored theme CSS"
```

---

### Task 9: Manual visual verification against the WordPress reference

**Files:** none (verification only)

- [ ] **Step 1: Start the WordPress reference site**

Run: `cd ~/Sites/grimoire-wp && wp server --host=127.0.0.1 --port=8080 &`
Verify: `curl -sS -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080/` prints `200`

- [ ] **Step 2: Start Grimoire locally**

Run: `go run ./cmd/grimoire --themes themes --addr 127.0.0.1:8081` (adjust flags to match whatever flags actually exist in `cmd/grimoire/main.go` — check `go run ./cmd/grimoire --help` first if unsure)

- [ ] **Step 3: Compare side by side**

Open `http://127.0.0.1:8080/` and `http://127.0.0.1:8081/` in a browser. Confirm:
- Two-column card grid silhouette matches on desktop widths, collapses to one column on narrow widths
- Header, card, and button styling uses Spectrum colors/spacing (no default browser blue links, no unstyled buttons)
- Single post view, comments list, and comment form render with Spectrum typography and no layout breakage
- No literal image or tag mismatches are treated as bugs — those are out of scope per this plan's Scope notes

- [ ] **Step 4: Record any follow-up issues**

If gaps are found that are in-scope for this plan (e.g. a missed class, a broken breakpoint), fix them in the relevant task's files, regenerate golden fixtures again, and amend that task's commit. If gaps are out of scope (e.g. "I want featured images too"), note them as a follow-up item for a future plan rather than expanding this one.

---

## Self-Review

**Spec coverage:** WordPress-reference silhouette (header/nav, 2-column card grid, single post, comments, footer) → Tasks 3-7. Real Spectrum CSS via `@spectrum-css/*` (not hand-rolled hex) → Task 1 + all CSS in Tasks 3-7 reference only `--spectrum-*` custom properties. Node build-time-only / committed output / CI freshness (mirroring `03-spectrum-admin`) → Tasks 1, 2, 8. Golden-file safety → explicit `-update` + `git diff` review step in every template-editing task. Manual visual check against the WordPress reference → Task 9.

**Placeholder scan:** No task contains "TBD"/"handle as needed"/unshown code. Every CSS/template edit has full literal content. Task 1 Step 3 and the comments/nav-menu tasks explicitly instruct verifying real class/field names against the installed packages/existing files rather than assuming the plan's names are correct — this is a deliberate, concrete verification step, not a placeholder.

**Type/name consistency:** `WithThemeStatic(themesDir, theme string) *Server` is defined once in Task 2 and used identically in Task 2 Step 8 (`main.go`) and Task 2 Step 6 (test helper). CSS class names (`theme-card`, `theme-card-grid`, `theme-article`, `theme-comments`, `theme-nav-list`, etc.) are introduced once each and reused consistently across the template tasks that reference them.
