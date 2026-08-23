# Tasks — M2.2: WordPress-faithful excerpts (fix escaped HTML in list views)

Implementation checklist for M2.2, following strict TDD (red → green → refactor).
Tasks are ordered so each builds on the last and references the requirements it
satisfies. Keep `gofmt -l .` empty, `go vet ./...`, `go build ./...`, and
`go test -count=1 ./...` green after every phase (SQLite unconditional;
MySQL/Postgres contract gated on `GRIMOIRE_TEST_MYSQL_DSN` /
`GRIMOIRE_TEST_POSTGRES_DSN`; real-WP-DB validation gated on `GRIMOIRE_TEST_WP_DSN`).
Commit in logical increments with the trailer
`Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.

## Phase 0 — Spec
- [x] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and add the `02.2` row to `plans/README.md` milestone index.
  - _Acceptance:_ Three Kiro spec files exist matching M2/M2.1's style; README row `02.2` links to this directory. _(All requirements)_

## Phase 1 — Excerpt derivation helper (`internal/content`)
- [x] 1.1 Write failing `internal/content/excerpt_test.go`:
  - `TestExcerptManualHTMLPreserved`: `Excerpt(Post{Excerpt:"<p>x</p>"})` returns `"<p>x</p>"` unchanged.
  - `TestExcerptAutoStripsBlockCommentsAndTags`: empty excerpt, content `<!-- wp:paragraph --><p>Hello <b>world</b></p><!-- /wp:paragraph -->` → `"<p>Hello world</p>"`; result contains no `wp:` and no `<!--`.
  - `TestExcerptTruncatesOver55Words`: empty excerpt, content of 60 plain words → `<p>` + first 55 words + `…</p>`; assert the ellipsis present and word count 55.
  - `TestExcerptNoTruncateUnder55Words`: empty excerpt, content of 10 words → `<p>`+those 10 words+`</p>`, no trailing `…`.
  - `TestExcerptEmptyContentAndExcerpt`: `Excerpt(Post{})` returns `""` (no panic, no `<p></p>`); also assert tags-only content (`<p></p>`) → `""`.
- [x] 1.2 Run `go test ./internal/content/ -run Excerpt -v`; expect FAIL (compile error — `content.Excerpt` undefined).
- [x] 1.3 Create `internal/content/excerpt.go` implementing `Excerpt(p domain.Post) string`: manual pass-through; auto = strip `<!-- wp:… -->` block comments → shortcodes (`\[[^\]]*\]`) → tags (`<[^>]*>`) → collapse whitespace → 55-word trim (append `…` only when truncated) → wrap `<p>…</p>`; empty stays `""`. Entities are left encoded (no html-decode — see 6.x). No `html/template` import.
- [x] 1.4 Run `go test ./internal/content/ -run Excerpt -v`; expect PASS. Then `go test ./internal/content/ -count=1` for no regressions.
  - _Acceptance:_ Manual excerpt preserved as HTML; auto excerpt strips block comments/shortcodes/tags, collapses whitespace, truncates at 55 words with `…`, wraps in `<p>`; empty→empty; no panics. _(Req 1.1, 2.1–2.5, 3.1–3.2, 4.1–4.2)_

## Phase 2 — Render field type + template-level proof (`internal/render`)
- [x] 2.1 Write failing `TestIndexRendersManualHTMLExcerpt` in `internal/render` (e.g. `excerpt_render_test.go`): render `index` with `IndexData{Posts:[]PostView{{Slug:"s",Title:"T",Excerpt: template.HTML("<p>x</p>")}}}`; assert output contains `<p>x</p>` and does NOT contain `&lt;p&gt;`.
- [x] 2.2 Run `go test ./internal/render/ -run TestIndexRendersManualHTMLExcerpt -v`; expect FAIL (compile error — `template.HTML` not assignable to `string` `Excerpt`, or escaped output).
- [x] 2.3 Change `render.PostView.Excerpt` from `string` to `template.HTML` in `internal/render/view.go` (update the field doc comment to note it is trusted excerpt HTML per the web trust boundary).
- [x] 2.4 Run `go test ./internal/render/ -count=1 -v`; expect PASS, including the new test and existing goldens (`index.html`, `category.html` unchanged — plain-text fixtures render identically).
  - _Acceptance:_ Index template renders a manual HTML excerpt raw (not escaped); reverting the type change fails `TestIndexRendersManualHTMLExcerpt`; existing golden tests still pass with no regen. _(Req 1.1–1.3, 6.2)_

## Phase 3 — Web wiring + trust boundary (`internal/web`)
- [x] 3.1 Write failing `internal/web/view_test.go`:
  - `TestPostViewManualExcerptRendersHTML`: `postView(Post{Excerpt:"<p>manual</p>", Content:"..."})` → `.Excerpt == template.HTML("<p>manual</p>")`.
  - `TestPostViewEmptyExcerptAutoDerives`: `postView(Post{Excerpt:"", Content:"<!-- wp:paragraph --><p>Body text here</p><!-- /wp:paragraph -->"})` → `.Excerpt == template.HTML("<p>Body text here</p>")`.
- [x] 3.2 Run `go test ./internal/web/ -run TestPostView -v`; expect FAIL (excerpt still raw `p.Excerpt` string / type mismatch).
- [x] 3.3 Edit `internal/web/view.go`: set `Excerpt: template.HTML(content.Excerpt(p))`; extend the package/`postView` `TRUST BOUNDARY` comment so it covers **both** `Content` and the derived `Excerpt` (any future untrusted/write path must sanitize before either cast). Add the `internal/content` import.
- [x] 3.4 Run `go test ./internal/web/ -count=1 -v`; expect PASS.
  - _Acceptance:_ `postView` maps manual excerpts to raw-HTML `Excerpt` and empty excerpts to the auto-generated `<p>…</p>`; the single trust-boundary comment covers both fields; helper still free of `html/template`. _(Req 1.1, 2.1–2.5, 5.1–5.2)_

## Phase 4 — Docs
- [x] 4.1 Update `docs/compatibility.md`: add excerpt semantics (`the_excerpt` manual-as-HTML vs auto-from-content, Gutenberg block-comment stripping, ~55-word trim + `…`) and extend the **Trusted-content boundary** section to state it now covers `Excerpt` (raw HTML via `template.HTML` at the web boundary; future untrusted/write paths must sanitize before the cast).
  - _Acceptance:_ `docs/compatibility.md` documents excerpt behavior and the extended trust boundary. _(Req 5.3)_

## Phase 5 — Gate, docs deviations, PR
- [x] 5.1 Full gate: `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test -count=1 ./...` (green). Fill the `design.md` Implementation Deviations section with anything discovered.
- [x] 5.2 Commit; open a PR against `main` using the `roboweaver` gh identity (`unset GH_TOKEN && gh auth switch -u roboweaver`, verify `gh api user -q .login`, then restore `robw_adobe`). Title: `M2.2 — WordPress-faithful excerpts (fix escaped HTML in list views)`. Do **not** merge.
  - _Acceptance:_ Full gate green; no untrusted-content path introduced; no hardcoded DSNs/hashes; PR opened against `main` (not merged); creator notified with the PR link. _(Req 6.1–6.4)_

## Phase 6 — Post-review fix (Scout HIGH: no entity decode in auto-excerpt)
- [x] 6.1 Add failing `content.TestExcerptPreservesEscapedEntities`: empty excerpt + content `"Here is code: &lt;script&gt;alert(1)&lt;/script&gt; and more text follows here."` → assert result CONTAINS `&lt;script&gt;` and NOT `<script>`; run → FAIL (pipeline decodes to live markup).
- [x] 6.2 Remove `html.UnescapeString` from the auto path in `internal/content/excerpt.go` and drop the now-unused `html` import; run `go test ./internal/content/ -run Excerpt -v` → PASS (all cases, including the pre-existing block-comment/tags test whose input has no entities).
  - _Acceptance:_ Auto-excerpt leaves entities encoded (WP-faithful — `wp_trim_excerpt` does not html-decode); raw emission no longer turns stored `&lt;script&gt;` into live markup; re-adding the decode fails the regression test. LOW nits (manual `wpautop` wrap; registered-shortcode-only stripping) deferred — see design.md. _(Scout HIGH)_
